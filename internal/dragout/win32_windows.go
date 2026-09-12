//go:build windows

package dragout

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// syscallPinned is syscall.SyscallN with the one pragma it does not carry,
// and it is what every call out through a COM vtable in this package goes
// through.
//
// syscall.SyscallN is //go:uintptrkeepalive and //go:nosplit: a uintptr
// argument that is really a converted pointer is kept alive across the
// call, but it is not forced onto the heap — and the runtime's own note on
// those functions says why keeping it alive is not enough, "stack copying
// does not account for uintptrkeepalive, so the stack must not grow".
// SyscallN cannot grow the stack itself. The foreign function it calls can
// re-enter Go through a callback, though, and that callback runs on a stack
// that may grow and so be copied; an out-parameter still named by its old
// stack address is then written to memory nothing will read.
//
// //go:uintptrescapes is the fix, and hand-rolling the allocation is not
// one: escape analysis is free to put a new(uint32) back on the stack, and
// does. The pragma instead forces every argument written as
// uintptr(unsafe.Pointer(x)) at a call site onto the heap for the duration
// of the call. x/sys/windows Proc.Call carries exactly this pragma over
// exactly this call, which is why the procXxx.Call sites here need nothing.
//
//go:uintptrescapes
func syscallPinned(fn uintptr, args ...uintptr) (r1, r2 uintptr, err syscall.Errno) {
	return syscall.SyscallN(fn, args...)
}

// 64-bit Windows only: a COM vtable slot is one uintptr wide and every
// callback below decodes its arguments on that assumption. Refuse to build
// rather than produce a binary that corrupts silently.
const _ = uint(unsafe.Sizeof(uintptr(0)) - 8)

// golang.org/x/sys/windows binds neither the OLE entry points nor the
// global-memory ones nor the three the self-drop needs, so they are bound
// here. NewLazySystemDLL rather than syscall.NewLazyDLL because it loads
// from System32 only: a DLL left beside the executable must not be able to
// answer.
var (
	modole32    = windows.NewLazySystemDLL("ole32.dll")
	moduser32   = windows.NewLazySystemDLL("user32.dll")
	modkernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procOleInitialize                 = modole32.NewProc("OleInitialize")
	procOleUninitialize               = modole32.NewProc("OleUninitialize")
	procDoDragDrop                    = modole32.NewProc("DoDragDrop")
	procReleaseStgMedium              = modole32.NewProc("ReleaseStgMedium")
	procCoCreateFreeThreadedMarshaler = modole32.NewProc("CoCreateFreeThreadedMarshaler")

	procRegisterClipboardFormW = moduser32.NewProc("RegisterClipboardFormatW")
	procGetClipFormatName      = moduser32.NewProc("GetClipboardFormatNameW")
	procGetCursorPos           = moduser32.NewProc("GetCursorPos")
	procWindowFromPoint        = moduser32.NewProc("WindowFromPoint")
	procGetAncestor            = moduser32.NewProc("GetAncestor")
	procGetClassName           = moduser32.NewProc("GetClassNameW")
	procGetWindowRect          = moduser32.NewProc("GetWindowRect")

	// The drag thread's own five, none of which existed here while the
	// drag ran on the window's thread (drag_windows.go).
	procAttachThreadInput        = moduser32.NewProc("AttachThreadInput")
	procGetWindowThreadProcessId = moduser32.NewProc("GetWindowThreadProcessId")
	procReleaseCapture           = moduser32.NewProc("ReleaseCapture")
	procRegisterClassExW         = moduser32.NewProc("RegisterClassExW")
	procCreateWindowExW          = moduser32.NewProc("CreateWindowExW")
	procDestroyWindow            = moduser32.NewProc("DestroyWindow")
	procDefWindowProcW           = moduser32.NewProc("DefWindowProcW")

	procGetModuleHandleW = modkernel32.NewProc("GetModuleHandleW")

	procGlobalAlloc   = modkernel32.NewProc("GlobalAlloc")
	procGlobalFree    = modkernel32.NewProc("GlobalFree")
	procGlobalLock    = modkernel32.NewProc("GlobalLock")
	procGlobalUnlock  = modkernel32.NewProc("GlobalUnlock")
	procGlobalSize    = modkernel32.NewProc("GlobalSize")
	procRtlMoveMemory = modkernel32.NewProc("RtlMoveMemory")
)

// HRESULTs. Values printed from the Windows SDK headers (winerror.h via
// windows.h, SDK 10.0.26100.0) rather than recalled.
const (
	sOK          = 0x00000000
	sFALSE       = 0x00000001
	eNotImpl     = 0x80004001
	eNoInterface = 0x80004002
	ePointer     = 0x80004003
	eUnexpected  = 0x8000FFFF
	eInvalidArg  = 0x80070057

	dvEFormatEtc = 0x80040064
	dvELIndex    = 0x80040068
	dvETymed     = 0x80040069
	dvEDvAspect  = 0x8004006B

	dataSSameFormatEtc = 0x00040130
	oleEAdviseNotSupp  = 0x80040003
	stgEMediumFull     = 0x80030070
	rpcEChangedMode    = 0x80010106

	dragDropSDrop              = 0x00040100
	dragDropSCancel            = 0x00040101
	dragDropSUseDefaultCursors = 0x00040102
)

// Data-transfer constants, read out of the SDK headers.
const (
	tymedHGlobal    = 1
	dvAspectContent = 1

	// The docs for IDataObjectAsyncCapability speak of VARIANT_TRUE and
	// VARIANT_FALSE even though the parameters are typed BOOL. VARIANT_TRUE
	// is (VARIANT_BOOL)-1, which widens to -1 in a BOOL, so use -1: it
	// satisfies a target testing "if (b)", "b != VARIANT_FALSE" and
	// "b == VARIANT_TRUE" alike, where 1 would fail the last.
	variantTrue  = 0xFFFFFFFF
	variantFalse = 0

	// MK_LBUTTON, in IDropSource::QueryContinueDrag's grfKeyState.
	mkLButton = 0x0001

	// GA_ROOT for GetAncestor: "Retrieves the root window by walking the
	// chain of parent windows."
	gaRoot = 2

	// HWND_MESSAGE, ((HWND)-3) in winuser.h, is the parent that makes a
	// window message-only, which is how the drag thread gets a message
	// queue without putting anything on screen: "To create a message-only
	// window, supply HWND_MESSAGE ... in the hWndParent parameter"
	// (CreateWindowExW), and such a window "is not visible, has no z-order,
	// cannot be enumerated, and does not receive broadcast messages. The
	// window simply dispatches messages" (Window Features, "Message-Only
	// Windows").
	hwndMessage = ^uintptr(2) // (HWND)-3

	// GHND == GMEM_MOVEABLE|GMEM_ZEROINIT, the conventional allocation for
	// an HGLOBAL handed to a clipboard or data-transfer consumer.
	gHND = 0x0042
)

type point struct {
	x, y int32
}

// rect is RECT: the window frame the self-drop reads beside the hit test.
type rect struct {
	left, top, right, bottom int32
}

// pointInRect is the frame test itself, separated from every API call so
// that it can be checked without a window. A RECT's right and bottom edges
// are exclusive, the way every Win32 rectangle is.
func pointInRect(p point, r rect) bool {
	return p.x >= r.left && p.x < r.right && p.y >= r.top && p.y < r.bottom
}

// formatEtc mirrors FORMATETC (objidl.h): sizeof 32, cfFormat at 0, ptd at
// 8, dwAspect at 16, lindex at 20, tymed at 24 on x64.
type formatEtc struct {
	cfFormat uint16
	_        [6]byte
	ptd      uintptr
	dwAspect uint32
	lindex   int32
	tymed    uint32
	_        uint32
}

// wndClassExW mirrors WNDCLASSEXW (winuser.h): the class the drag thread's
// message-only window is made of. Everything but the procedure, the module
// and the name is zero — there is nothing to paint, no cursor to set and no
// menu, the window existing only so that the thread has a queue.
type wndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     windows.Handle
	hIcon         windows.Handle
	hCursor       windows.Handle
	hbrBackground windows.Handle
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       windows.Handle
}

// stgMedium mirrors STGMEDIUM (objidl.h): sizeof 24, tymed at 0, the union
// at 8, pUnkForRelease at 16 on x64. The union member is hGlobal here.
type stgMedium struct {
	tymed          uint32
	_              uint32
	data           uintptr
	pUnkForRelease uintptr
}

func globalAlloc(flags, size uintptr) uintptr {
	h, _, _ := procGlobalAlloc.Call(flags, size)
	return h
}

func globalFree(h uintptr) {
	procGlobalFree.Call(h)
}

func globalLock(h uintptr) uintptr {
	p, _, _ := procGlobalLock.Call(h)
	return p
}

func globalUnlock(h uintptr) {
	procGlobalUnlock.Call(h)
}

// globalSize is the length check before anything reads out of a medium a
// caller handed over: zero is both "not a handle" and "nothing to read".
func globalSize(h uintptr) uintptr {
	n, _, _ := procGlobalSize.Call(h)
	return n
}

// copyIntoNative copies src into memory Windows owns — a locked HGLOBAL —
// which arrives as a bare address and nothing else. Going through
// RtlMoveMemory rather than reconstructing a Go pointer from that integer is
// what keeps it an integer: an address that came back from an API as a
// uintptr never has to be guessed at as the start of an object of some type
// and length.
func copyIntoNative(dst uintptr, src []byte) {
	if len(src) == 0 {
		return
	}
	procRtlMoveMemory.Call(dst, uintptr(unsafe.Pointer(&src[0])), uintptr(len(src)))
}

// copyFromNative is the other direction: reading a few bytes out of a
// medium a caller handed us, again without pretending a foreign address is
// a Go pointer.
func copyFromNative(dst []byte, src uintptr) {
	if len(dst) == 0 || src == 0 {
		return
	}
	procRtlMoveMemory.Call(uintptr(unsafe.Pointer(&dst[0])), src, uintptr(len(dst)))
}

// registerClipboardFormat registers a Shell clipboard format name and
// returns its CLIPFORMAT. The Shell formats other than CF_HDROP are not
// predefined; each must be registered by name.
func registerClipboardFormat(name string) uint16 {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		panic("dragout: bad clipboard format name: " + err.Error())
	}
	cf, _, _ := procRegisterClipboardFormW.Call(uintptr(unsafe.Pointer(p)))
	if cf == 0 {
		panic("dragout: RegisterClipboardFormatW failed for " + name)
	}
	return uint16(cf)
}

// clipboardFormatName resolves a registered CLIPFORMAT back to its name, so
// the log can say what a target asked for rather than a number. Predefined
// formats have no registered name; GetClipboardFormatNameW returns zero for
// those.
func clipboardFormatName(cf uint16) string {
	var buf [256]uint16
	n, _, _ := procGetClipFormatName.Call(uintptr(cf), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

// cursorPosition is where the cursor is right now. It is window-station
// wide and belongs to no thread, which is what lets the drop source read it
// from the drag's own thread. A variable, so that a test can put a release
// anywhere without a mouse.
var cursorPosition = func() (point, bool) {
	var pt point
	if r, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt))); r == 0 {
		return point{}, false
	}
	return pt, true
}

// windowFrame is GetWindowRect over a window handle: the second of the two
// signals the self-drop is read from, and the one that survives a window
// the hit test will not name. A variable, for the same reason.
var windowFrame = func(hwnd uintptr) (rect, bool) {
	if hwnd == 0 {
		return rect{}, false
	}
	var r rect
	if ok, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); ok == 0 {
		return rect{}, false
	}
	return r, true
}

// cursorRootWindow is the top-level window under the cursor right now:
// GetCursorPos, WindowFromPoint, GetAncestor(GA_ROOT). It is what the
// self-drop compares with the caller's window at the button's release. A
// variable, so that a test of the drop source can put a window there.
var cursorRootWindow = func() uintptr {
	pt, ok := cursorPosition()
	if !ok {
		return 0
	}
	// POINT is passed by value: two LONGs packed into one 64-bit register.
	packed := uintptr(uint32(pt.x)) | uintptr(uint32(pt.y))<<32
	h, _, _ := procWindowFromPoint.Call(packed)
	if h == 0 {
		return 0
	}
	root, _, _ := procGetAncestor.Call(h, gaRoot)
	if root == 0 {
		return h
	}
	return root
}

// windowClassName is GetClassNameW over a window handle: the class, never
// the title — a window's title is a document's name, and no file name is
// ever logged (APP.md §3). "The maximum length for lpszClassName is 256"
// (RegisterClass), so 257 units hold any class name and its terminator.
// Empty for a handle that is gone or was never one; a variable, so that a
// test of the drop source can put a class there.
var windowClassName = func(hwnd uintptr) string {
	if hwnd == 0 {
		return ""
	}
	var buf [257]uint16
	n, _, _ := procGetClassName.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

// hrName renders an HRESULT the way the SDK names it, so that a log line
// can be grepped for a symbol rather than a hex number.
func hrName(hr uintptr) string {
	v := uint32(hr)
	names := map[uint32]string{
		sOK:                        "S_OK",
		sFALSE:                     "S_FALSE",
		eNotImpl:                   "E_NOTIMPL",
		eNoInterface:               "E_NOINTERFACE",
		ePointer:                   "E_POINTER",
		eUnexpected:                "E_UNEXPECTED",
		eInvalidArg:                "E_INVALIDARG",
		dvEFormatEtc:               "DV_E_FORMATETC",
		dvELIndex:                  "DV_E_LINDEX",
		dvETymed:                   "DV_E_TYMED",
		dvEDvAspect:                "DV_E_DVASPECT",
		dataSSameFormatEtc:         "DATA_S_SAMEFORMATETC",
		oleEAdviseNotSupp:          "OLE_E_ADVISENOTSUPPORTED",
		stgEMediumFull:             "STG_E_MEDIUMFULL",
		rpcEChangedMode:            "RPC_E_CHANGED_MODE",
		dragDropSDrop:              "DRAGDROP_S_DROP",
		dragDropSCancel:            "DRAGDROP_S_CANCEL",
		dragDropSUseDefaultCursors: "DRAGDROP_S_USEDEFAULTCURSORS",
	}
	if n, ok := names[v]; ok {
		return fmt.Sprintf("%s (0x%08X)", n, v)
	}
	return fmt.Sprintf("0x%08X", v)
}

// effectName renders a DROPEFFECT mask.
func effectName(e uint32) string {
	if e == 0 {
		return "DROPEFFECT_NONE"
	}
	s := ""
	add := func(bit uint32, name string) {
		if e&bit != 0 {
			if s != "" {
				s += "|"
			}
			s += name
		}
	}
	add(dropEffectCopy, "DROPEFFECT_COPY")
	add(dropEffectMove, "DROPEFFECT_MOVE")
	add(dropEffectLink, "DROPEFFECT_LINK")
	add(0x80000000, "DROPEFFECT_SCROLL")
	if s == "" {
		s = fmt.Sprintf("0x%08X", e)
	}
	return s
}

// failed is FAILED(hr): the sign bit of an HRESULT.
func failed(hr uintptr) bool { return int32(uint32(hr)) < 0 }

// feString renders a FORMATETC for the log, every member named, so that the
// trace of a drag says not only which format a target asked for but which
// aspect, index and media — the parts a refusal is about. Lifted from
// tools/dragproto.
func feString(fe *formatEtc) string {
	if fe == nil {
		return "<nil FORMATETC>"
	}
	return fmt.Sprintf("{%s, ptd=0x%X, %s, lindex=%d, %s}",
		formatName(fe.cfFormat), fe.ptd, aspectName(fe.dwAspect), fe.lindex, tymedName(fe.tymed))
}

// aspectName renders a DVASPECT, the values read out of the SDK's wtypes.h.
func aspectName(a uint32) string {
	switch a {
	case 1:
		return "DVASPECT_CONTENT"
	case 2:
		return "DVASPECT_THUMBNAIL"
	case 4:
		return "DVASPECT_ICON"
	case 8:
		return "DVASPECT_DOCPRINT"
	}
	return fmt.Sprintf("aspect=0x%X", a)
}

// tymedName renders a TYMED mask; GetData callers routinely OR several
// together. The bits are objidl.h's.
func tymedName(t uint32) string {
	s := ""
	add := func(bit uint32, name string) {
		if t&bit != 0 {
			if s != "" {
				s += "|"
			}
			s += name
		}
	}
	add(1, "TYMED_HGLOBAL")
	add(2, "TYMED_FILE")
	add(4, "TYMED_ISTREAM")
	add(8, "TYMED_ISTORAGE")
	add(16, "TYMED_GDI")
	add(32, "TYMED_MFPICT")
	add(64, "TYMED_ENHMF")
	if s == "" {
		if t == 0 {
			return "TYMED_NULL"
		}
		s = fmt.Sprintf("tymed=0x%08X", t)
	}
	return s
}

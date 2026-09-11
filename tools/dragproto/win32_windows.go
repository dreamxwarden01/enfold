//go:build windows

package main

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// syscallPinned is syscall.SyscallN with the one pragma it does not carry, and
// it is what every call out through a COM vtable in this package goes through.
//
// syscall.SyscallN is //go:uintptrkeepalive and //go:nosplit: a uintptr
// argument that is really a converted pointer is kept alive across the call,
// but it is not forced onto the heap -- and the runtime's own note on those
// functions says why keeping it alive is not enough, "stack copying does not
// account for uintptrkeepalive, so the stack must not grow". SyscallN cannot
// grow the stack itself. The foreign function it calls can re-enter Go through
// a callback, though, and that callback runs on a stack that may grow and so be
// copied; an out-parameter still named by its old stack address is then written
// to memory nothing will read. The symptom is a call that quietly did nothing:
// the destination of a CopyTo takes every byte and reports back zero.
//
// //go:uintptrescapes is the fix, and hand-rolling the allocation is not one:
// escape analysis is free to put a new(uint32) or a make([]uint32, 1) back on
// the stack, and does. The pragma instead forces every argument written as
// uintptr(unsafe.Pointer(x)) at a call site onto the heap for the duration of
// the call. x/sys/windows Proc.Call carries exactly this pragma over exactly
// this call, which is why the procXxx.Call sites in this package need nothing.
//
//go:uintptrescapes
func syscallPinned(fn uintptr, args ...uintptr) (r1, r2 uintptr, err syscall.Errno) {
	return syscall.SyscallN(fn, args...)
}

// The prototype is 64-bit Windows only, and two things depend on that. A COM
// vtable slot is one uintptr wide, and IStream::Seek receives its 8-byte
// LARGE_INTEGER whole, in a single register, which is how streamSeek reads it.
// On 32-bit Windows that one argument arrives as two stack slots and every
// stream callback after it would be decoded off by one, so refuse to build
// rather than produce a binary that corrupts silently.
const _ = uint(unsafe.Sizeof(uintptr(0)) - 8)

// golang.org/x/sys/windows binds neither the window and OLE entry points nor
// the global-memory ones, so they are bound here. NewLazySystemDLL rather than
// syscall.NewLazyDLL because it loads from System32 only: a "psapi.dll" left
// beside the executable must not be able to answer.
var (
	modole32    = windows.NewLazySystemDLL("ole32.dll")
	moduser32   = windows.NewLazySystemDLL("user32.dll")
	modkernel32 = windows.NewLazySystemDLL("kernel32.dll")
	modgdi32    = windows.NewLazySystemDLL("gdi32.dll")
	modpsapi    = windows.NewLazySystemDLL("psapi.dll")

	procOleInitialize     = modole32.NewProc("OleInitialize")
	procOleUninitialize   = modole32.NewProc("OleUninitialize")
	procDoDragDrop        = modole32.NewProc("DoDragDrop")
	procCoTaskMemAlloc    = modole32.NewProc("CoTaskMemAlloc")
	procReleaseStgMedium  = modole32.NewProc("ReleaseStgMedium")
	procGetClipFormatName = moduser32.NewProc("GetClipboardFormatNameW")

	// The one entry point -agile adds. It is in ole32.dll and declared in
	// combaseapi.h as
	//   HRESULT CoCreateFreeThreadedMarshaler(LPUNKNOWN punkOuter,
	//                                         LPUNKNOWN *ppunkMarshal);
	procCoCreateFreeThreadedMarshaler = modole32.NewProc("CoCreateFreeThreadedMarshaler")

	procRegisterClassExW       = moduser32.NewProc("RegisterClassExW")
	procCreateWindowExW        = moduser32.NewProc("CreateWindowExW")
	procDefWindowProcW         = moduser32.NewProc("DefWindowProcW")
	procGetMessageW            = moduser32.NewProc("GetMessageW")
	procTranslateMessage       = moduser32.NewProc("TranslateMessage")
	procDispatchMessageW       = moduser32.NewProc("DispatchMessageW")
	procPostQuitMessage        = moduser32.NewProc("PostQuitMessage")
	procLoadCursorW            = moduser32.NewProc("LoadCursorW")
	procGetSystemMetrics       = moduser32.NewProc("GetSystemMetrics")
	procRegisterClipboardFormW = moduser32.NewProc("RegisterClipboardFormatW")
	procPostMessageW           = moduser32.NewProc("PostMessageW")
	procSetCapture             = moduser32.NewProc("SetCapture")
	procReleaseCapture         = moduser32.NewProc("ReleaseCapture")
	procBeginPaint             = moduser32.NewProc("BeginPaint")
	procEndPaint               = moduser32.NewProc("EndPaint")
	procDrawTextW              = moduser32.NewProc("DrawTextW")
	procGetClientRect          = moduser32.NewProc("GetClientRect")
	procInvalidateRect         = moduser32.NewProc("InvalidateRect")
	procShowWindow             = moduser32.NewProc("ShowWindow")
	procUpdateWindow           = moduser32.NewProc("UpdateWindow")
	procGetSysColorBrush       = moduser32.NewProc("GetSysColorBrush")

	procGetModuleHandleW = modkernel32.NewProc("GetModuleHandleW")
	procGlobalAlloc      = modkernel32.NewProc("GlobalAlloc")
	procGlobalFree       = modkernel32.NewProc("GlobalFree")
	procGlobalLock       = modkernel32.NewProc("GlobalLock")
	procGlobalUnlock     = modkernel32.NewProc("GlobalUnlock")
	procGlobalSize       = modkernel32.NewProc("GlobalSize")
	procRtlMoveMemory    = modkernel32.NewProc("RtlMoveMemory")

	procGetStockObject = modgdi32.NewProc("GetStockObject")
	procSelectObject   = modgdi32.NewProc("SelectObject")
	procSetBkMode      = modgdi32.NewProc("SetBkMode")

	procGetProcessMemoryInfo = modpsapi.NewProc("GetProcessMemoryInfo")
)

// HRESULTs. Values printed from the Windows SDK headers (winerror.h via
// windows.h, SDK 10.0.26100.0) rather than recalled.
const (
	sOK          = 0x00000000
	sFALSE       = 0x00000001
	eNotImpl     = 0x80004001
	eNoInterface = 0x80004002
	ePointer     = 0x80004003
	eFail        = 0x80004005
	eUnexpected  = 0x8000FFFF
	eInvalidArg  = 0x80070057
	eOutOfMemory = 0x8007000E

	dvEFormatEtc = 0x80040064
	dvELIndex    = 0x80040068
	dvETymed     = 0x80040069
	dvEDvAspect  = 0x8004006B

	dataSSameFormatEtc = 0x00040130
	oleEAdviseNotSupp  = 0x80040003

	stgEInvalidFunction = 0x80030001
	stgEAccessDenied    = 0x80030005
	stgEInvalidPointer  = 0x80030009
	stgEMediumFull      = 0x80030070
	stgEInvalidFlag     = 0x800300FF

	// STG_E_REVERTED, 0x80030102, is what a stream answers once the program has
	// been torn down under a target that is still holding it. The COM error
	// table glosses it "Attempted to use an object that has ceased to exist",
	// and it is in the documented return list of ISequentialStream::Read,
	// IStream::Seek and IStream::Stat -- which is the whole reason it is the
	// code used here: a target that checks return values has been told about
	// this one on the very pages that describe the methods it is calling.
	//
	// The method pages gloss it more narrowly, as "The object has been
	// invalidated by a revert operation above it in the transaction tree", and
	// nothing on Learn documents it for "the provider went away". What COM does
	// document for that is a disconnected *proxy* -- RPC_E_DISCONNECTED and
	// CO_E_OBJNOTCONNECTED, which CoDisconnectObject makes a proxy return -- and
	// there is no proxy here: with -agile the target holds the object itself.
	// So the choice is between a code documented for these methods with a
	// slightly wrong story and a code with the right story that these methods
	// never mention. This picks the former, and says so.
	stgEReverted = 0x80030102

	dragDropSDrop               = 0x00040100
	dragDropSCancel             = 0x00040101
	dragDropSUseDefaultCursors  = 0x00040102
	dragDropENotRegisteredValue = 0x80040100
)

// Drag-and-drop, data-transfer and storage constants, all read out of the SDK
// headers.
const (
	dropEffectNone = 0
	dropEffectCopy = 1
	dropEffectMove = 2
	dropEffectLink = 4

	tymedNull    = 0
	tymedHGlobal = 1
	tymedIStream = 4

	dvAspectContent = 1

	streamSeekSet = 0
	streamSeekCur = 1
	streamSeekEnd = 2

	stgTyStream = 2
	stgmRead    = 0

	statFlagDefault = 0
	statFlagNoName  = 1
	statFlagNoOpen  = 2

	// The docs for IDataObjectAsyncCapability speak of VARIANT_TRUE and
	// VARIANT_FALSE even though the parameters are typed BOOL. VARIANT_TRUE is
	// (VARIANT_BOOL)-1, which widens to -1 in a BOOL, so use -1: it satisfies a
	// target testing "if (b)", "b != VARIANT_FALSE" and "b == VARIANT_TRUE"
	// alike, where 1 would fail the last.
	variantTrue  = 0xFFFFFFFF
	variantFalse = 0
)

// FILEDESCRIPTORW dwFlags (FD_FLAGS in shlobj_core.h).
const (
	fdCLSID      = 0x00000001
	fdSizePoint  = 0x00000002
	fdAttributes = 0x00000004
	fdCreateTime = 0x00000008
	fdAccessTime = 0x00000010
	fdWritesTime = 0x00000020
	fdFileSize   = 0x00000040
	fdProgressUI = 0x00004000
	fdLinkUI     = 0x00008000
	fdUnicode    = 0x80000000
)

// Window, memory and metric constants.
const (
	csVRedraw = 0x0001
	csHRedraw = 0x0002

	wsOverlappedWindow = 0x00CF0000
	wsVisible          = 0x10000000
	cwUseDefault       = ^uintptr(0x7FFFFFFF) // (int)0x80000000 sign-extended

	swShowNormal = 1

	wmDestroy     = 0x0002
	wmPaint       = 0x000F
	wmClose       = 0x0010
	wmMouseMove   = 0x0200
	wmLButtonDown = 0x0201
	wmLButtonUp   = 0x0202

	mkLButton = 0x0001

	smCXDrag = 68
	smCYDrag = 69

	idcArrow      = 32512
	colorWindow   = 5
	defaultGUIFnt = 17
	transparent   = 1

	dtLeft       = 0x0000
	dtTop        = 0x0000
	dtCenter     = 0x0001
	dtWordBreak  = 0x0010
	dtExpandTabs = 0x0040
	dtNoPrefix   = 0x0800

	fileAttributeNormal = 0x00000080

	// GHND == GMEM_MOVEABLE|GMEM_ZEROINIT, the conventional allocation for an
	// HGLOBAL handed to a clipboard or data-transfer consumer. GPTR ==
	// GMEM_FIXED|GMEM_ZEROINIT, which returns a plain pointer; that is what the
	// COM object blocks use, since they are never handles.
	gHND = 0x0042
	gPTR = 0x0040
)

type point struct {
	x, y int32
}

type rect struct {
	left, top, right, bottom int32
}

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

type msgW struct {
	hwnd     windows.Handle
	message  uint32
	_        uint32
	wParam   uintptr
	lParam   uintptr
	time     uint32
	pt       point
	lPrivate uint32
}

type paintStruct struct {
	hdc         windows.Handle
	fErase      int32
	rcPaint     rect
	fRestore    int32
	fIncUpdate  int32
	rgbReserved [32]byte
}

// formatEtc mirrors FORMATETC (objidl.h): sizeof 32, cfFormat at 0, ptd at 8,
// dwAspect at 16, lindex at 20, tymed at 24 on x64.
type formatEtc struct {
	cfFormat uint16
	_        [6]byte
	ptd      uintptr
	dwAspect uint32
	lindex   int32
	tymed    uint32
	_        uint32
}

// stgMedium mirrors STGMEDIUM (objidl.h): sizeof 24, tymed at 0, the union at
// 8, pUnkForRelease at 16 on x64. The union member is hGlobal for TYMED_HGLOBAL
// and pstm for TYMED_ISTREAM; one uintptr covers both.
type stgMedium struct {
	tymed          uint32
	_              uint32
	data           uintptr
	pUnkForRelease uintptr
}

// statStg mirrors STATSTG (objidl.h): sizeof 80 on x64, offsets pwcsName 0,
// type 8, cbSize 16, mtime 24, ctime 32, atime 40, grfMode 48,
// grfLocksSupported 52, clsid 56, grfStateBits 72, reserved 76.
type statStg struct {
	pwcsName          uintptr
	typ               uint32
	_                 uint32
	cbSize            uint64
	mtime             windows.Filetime
	ctime             windows.Filetime
	atime             windows.Filetime
	grfMode           uint32
	grfLocksSupported uint32
	clsid             windows.GUID
	grfStateBits      uint32
	reserved          uint32
}

// processMemoryCounters mirrors PROCESS_MEMORY_COUNTERS (psapi.h): sizeof 72 on
// x64, cb 0, PageFaultCount 4, PeakWorkingSetSize 8, WorkingSetSize 16,
// PagefileUsage 56, PeakPagefileUsage 64.
type processMemoryCounters struct {
	cb                         uint32
	pageFaultCount             uint32
	peakWorkingSetSize         uintptr
	workingSetSize             uintptr
	quotaPeakPagedPoolUsage    uintptr
	quotaPagedPoolUsage        uintptr
	quotaPeakNonPagedPoolUsage uintptr
	quotaNonPagedPoolUsage     uintptr
	pagefileUsage              uintptr
	peakPagefileUsage          uintptr
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

// globalSize is the length check before anything reads out of a medium a caller
// handed over. "If the function succeeds, the return value is the size of the
// specified global memory object, in bytes. If the specified handle is not
// valid or if the object has been discarded, the return value is zero" -- so
// zero is both "not a handle" and "nothing to read", which is exactly the
// answer a reader wants for either. Note the documented caveat that "the size
// of a memory block may be larger than the size requested when the memory was
// allocated": this is a lower bound on what may be read, never a statement of
// what the sender meant to put there.
func globalSize(h uintptr) uintptr {
	n, _, _ := procGlobalSize.Call(h)
	return n
}

// copyIntoNative copies src into memory Windows owns -- a locked HGLOBAL, a
// CoTaskMemAlloc block -- which arrives as a bare address and nothing else.
// Going through RtlMoveMemory rather than reconstructing a Go pointer from that
// integer is what keeps it an integer: an address that came back from an API as
// a uintptr never has to be guessed at as the start of an object of some type
// and length.
//
// This is not a claim that no foreign pointer ever reaches Go code. The COM
// callbacks take typed pointers into COM-owned memory -- pmedium *stgMedium,
// rgelt *formatEtc, pv *byte and the rest -- because that is how the ABI passes
// them, and dereferencing those is safe for a different reason: those addresses
// lie outside the Go heap, so the collector has nothing to trace through them
// and the stack copier never rewrites them. What copyIntoNative avoids is the
// other direction, inventing a Go pointer for a block whose type and extent
// only the API knows.
func copyIntoNative(dst uintptr, src []byte) {
	if len(src) == 0 {
		return
	}
	procRtlMoveMemory.Call(dst, uintptr(unsafe.Pointer(&src[0])), uintptr(len(src)))
}

// copyFromNative is the other direction: reading a few bytes out of a medium a
// caller handed us, again without pretending a foreign address is a Go pointer.
func copyFromNative(dst []byte, src uintptr) {
	if len(dst) == 0 || src == 0 {
		return
	}
	procRtlMoveMemory.Call(uintptr(unsafe.Pointer(&dst[0])), src, uintptr(len(dst)))
}

// coTaskMemAlloc allocates with the allocator COM's out-parameters are
// documented to use, so that the caller's CoTaskMemFree matches.
func coTaskMemAlloc(size uintptr) uintptr {
	p, _, _ := procCoTaskMemAlloc.Call(size)
	return p
}

// clipboardFormatName resolves a registered CLIPFORMAT back to its name, so the
// log can say "FileContents" rather than 49301. Predefined formats have no
// registered name; GetClipboardFormatNameW returns zero for those.
func clipboardFormatName(cf uint16) string {
	var buf [256]uint16
	n, _, _ := procGetClipFormatName.Call(uintptr(cf), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

func getSystemMetrics(index int32) int32 {
	r, _, _ := procGetSystemMetrics.Call(uintptr(index))
	return int32(r)
}

// registerClipboardFormat registers a Shell clipboard format name and returns
// its CLIPFORMAT. The Shell formats other than CF_HDROP are not predefined;
// each must be registered by name (Handling Shell Data Transfer Scenarios,
// "General Guidelines").
func registerClipboardFormat(name string) uint16 {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		panic("dragproto: bad clipboard format name: " + err.Error())
	}
	cf, _, _ := procRegisterClipboardFormW.Call(uintptr(unsafe.Pointer(p)))
	if cf == 0 {
		panic("dragproto: RegisterClipboardFormatW failed for " + name)
	}
	return uint16(cf)
}

// peakWorkingSet returns the process's peak working set in bytes.
func peakWorkingSet() (uint64, bool) {
	var c processMemoryCounters
	c.cb = uint32(unsafe.Sizeof(c))
	h, err := windows.GetCurrentProcess()
	if err != nil {
		return 0, false
	}
	r, _, _ := procGetProcessMemoryInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&c)), uintptr(c.cb))
	if r == 0 {
		return 0, false
	}
	return uint64(c.peakWorkingSetSize), true
}

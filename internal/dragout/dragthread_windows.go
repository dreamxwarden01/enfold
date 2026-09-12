//go:build windows

package dragout

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The parts the drag's own thread is made of (APP.md §3, ruled 2026-09-11
// on the measurements in docs/research/drag-out.md): the apartment, the
// message-only window that gives the thread a queue, the input attachment,
// the capture the window thread has to let go of, and DoDragDrop itself.
// drag_windows.go is the order they are used in; this file is what each
// one is.
//
// Every one of them is a variable rather than a plain function, and that is
// deliberate: a test of the thread's lifecycle must not take an apartment,
// must not create a window and must not start a drag, so it puts its own
// call in each seam and watches the order they are made in. The real
// implementations are one line each and are exercised by the build, the
// vet gate and a real drag.
var (
	// oleInitializeOnThread is OleInitialize(NULL) on the calling thread.
	// "Initializes the COM library on the current apartment, identifies the
	// concurrency model as single-thread apartment (STA)", which is the
	// apartment DoDragDrop needs: "You must call OleInitialize before
	// calling this function." S_FALSE — "The COM library is already
	// initialized on this apartment" — is a success, and a success that
	// still has to be balanced: "each successful call to OleInitialize,
	// including those that return S_FALSE, must be balanced by a
	// corresponding call to OleUninitialize."
	oleInitializeOnThread = func() uintptr {
		hr, _, _ := procOleInitialize.Call(0)
		return hr
	}

	// oleUninitializeOnThread is the balance, and it is the drag thread's
	// last OLE act. It "Closes the COM library on the apartment, releases
	// any class factories, other COM objects, or servers held by the
	// apartment" — which is why drag_windows.go asks first whether the
	// target still holds a data object of ours.
	oleUninitializeOnThread = func() {
		procOleUninitialize.Call()
	}

	// newDragWindow and closeDragWindow are the thread's message-only
	// window: created on it, destroyed on it — "A thread cannot use
	// DestroyWindow to destroy a window created by a different thread."
	newDragWindow   = createMessageOnlyWindow
	closeDragWindow = func(hwnd uintptr) {
		procDestroyWindow.Call(hwnd)
	}

	// attachThreadInput is AttachThreadInput, in the direction Chromium
	// used and the prototype measured: idAttach the drag thread, idAttachTo
	// the window's. It answers whether the call succeeded and the error
	// Windows gave, because both go in the log.
	attachThreadInput = func(attach, attachTo uint32, on bool) (bool, error) {
		f := uintptr(0)
		if on {
			f = 1
		}
		r, _, err := procAttachThreadInput.Call(uintptr(attach), uintptr(attachTo), f)
		if r == 0 {
			return false, err
		}
		return true, nil
	}

	// windowThreadID is GetWindowThreadProcessId with no process wanted:
	// the parameter is optional, and what is wanted is the return, "the
	// identifier of the thread that created the window". Zero for a handle
	// that is not a window — "If the window handle is invalid, the return
	// value is zero" — and zero never matches a real thread, so the caller
	// needs no other guard.
	windowThreadID = func(hwnd uintptr) uint32 {
		if hwnd == 0 {
			return 0
		}
		tid, _, _ := procGetWindowThreadProcessId.Call(hwnd, 0)
		return uint32(tid)
	}

	// releaseMouseCapture is ReleaseCapture, which "Releases the mouse
	// capture from a window in the current thread" — the current thread,
	// so this one is asked of the window's thread and never of the drag's.
	releaseMouseCapture = func() bool {
		r, _, _ := procReleaseCapture.Call()
		return r != 0
	}

	// doDragDrop is the drag itself, on whatever thread calls it.
	doDragDrop = func(data, source uintptr, allowed uint32, effect *uint32) uintptr {
		hr, _, _ := procDoDragDrop.Call(data, source, uintptr(allowed),
			uintptr(unsafe.Pointer(effect)))
		return hr
	}

	// pumpMessage is one turn of the loop the drag thread runs while it
	// stays behind for a target that kept the data object: GetMessage, which
	// blocks until there is something, and DispatchMessage.
	//
	// The window filter is NULL on purpose — "retrieves messages for any
	// window that belongs to the current thread" — because the window this
	// thread has to serve is not only its own: OleInitialize makes a hidden
	// one of its own on the apartment's thread, and that is where a late
	// call to an apartment-bound object of ours would arrive. Serving only
	// EnfoldDragThread's queue would be a loop that looks like a pump and
	// answers nothing.
	//
	// False is the queue being done with: "If the function retrieves the
	// WM_QUIT message, the return value is zero", and "If there is an
	// error, the return value is -1" — for which the page's own example is
	// an invalid window handle. Neither is a thing to keep looping on.
	pumpMessage = func() bool {
		var m msgW
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return false
		}
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
		return true
	}

	// postWake posts WM_NULL to the drag thread's own window so that a
	// GetMessage sitting on an idle queue returns and the stay can look at
	// its channels again. PostMessage "places (posts) a message in the
	// message queue associated with the thread that created the specified
	// window and returns without waiting", which is what lets another
	// goroutine do it.
	postWake = func(hwnd uintptr) bool {
		r, _, _ := procPostMessageW.Call(hwnd, wmNull, 0, 0)
		return r != 0
	}
)

// ---------------------------------------------------------------------------
// The message-only window.

// dragWindowClass is the class the drag thread's window is made of. Window
// classes are documented as "process specific" — the scope named is the
// process and the module, never a thread — so the class is registered once
// and every drag's thread creates its own window of it.
const dragWindowClass = "EnfoldDragThread"

var (
	// dragWndProc answers nothing of its own: the window exists to give the
	// thread a message queue, not to do anything. The callback is made once
	// — syscall.NewCallback's table is finite — and lives for the process.
	dragWndProc = syscall.NewCallback(func(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
		r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
		return r
	})

	dragClassOnce sync.Once
	dragClassAtom uintptr
	dragClassErr  error
)

func registerDragWindowClass() (uintptr, error) {
	dragClassOnce.Do(func() {
		name, err := windows.UTF16PtrFromString(dragWindowClass)
		if err != nil {
			dragClassErr = err
			return
		}
		hinst, _, _ := procGetModuleHandleW.Call(0)
		wc := wndClassExW{
			lpfnWndProc:   dragWndProc,
			hInstance:     windows.Handle(hinst),
			lpszClassName: name,
		}
		wc.cbSize = uint32(unsafe.Sizeof(wc))
		atom, _, callErr := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
		if atom == 0 {
			dragClassErr = fmt.Errorf("RegisterClassExW(%s): %w", dragWindowClass, callErr)
			return
		}
		dragClassAtom = atom
	})
	return dragClassAtom, dragClassErr
}

// createMessageOnlyWindow gives the calling thread a message queue and
// nothing else: HWND_MESSAGE as the parent is what makes it message-only,
// and what that costs the desktop is in the constant's own note.
//
// The queue is the point. AttachThreadInput "fails if either of the
// specified threads does not have a message queue", and "the system creates
// a thread's message queue when the thread makes its first call to one of
// the USER or GDI functions" — this is that call, made where it can be seen
// rather than left to whichever USER call happened to come first.
//
// It cannot be unit-tested: creating a window needs a window station, a
// desktop and a thread that owns the result, and the whole point of the
// call is the side effect on the calling thread. The tests put their own
// function in newDragWindow instead, and a real drag covers this one.
func createMessageOnlyWindow() (uintptr, error) {
	atom, err := registerDragWindowClass()
	if err != nil {
		return 0, err
	}
	hinst, _, _ := procGetModuleHandleW.Call(0)
	h, _, callErr := procCreateWindowExW.Call(
		0, atom, 0,
		0, 0, 0, 0, 0,
		hwndMessage, 0, hinst, 0)
	if h == 0 {
		return 0, fmt.Errorf("CreateWindowExW(HWND_MESSAGE): %w", callErr)
	}
	return h, nil
}

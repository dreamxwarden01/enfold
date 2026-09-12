//go:build windows

package main

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// -thread and -disable: the two things WinRAR's window appears to do that ours
// does not.
//
// The real application's window is frozen for the whole of a drag, and its
// frontend -- which is told what is happening by messages posted to the main
// thread's hidden window -- shows nothing at all. WinRAR's window, during a drag
// out of it, looks held rather than dead: it repaints, it is plainly not
// accepting clicks, and it comes back the instant the button goes up.
//
// Two separate mechanisms could produce that, and this file adds both so they
// can be told apart by measurement rather than by argument:
//
//   - -thread runs DoDragDrop on a thread of its own, joining the two threads'
//     input with AttachThreadInput for the duration, so the window's thread is
//     never inside the modal loop at all and its message loop keeps running.
//   - -disable calls EnableWindow(hwnd, FALSE) for the one stretch where the
//     window has nothing to offer -- from the post-release GetData handing the
//     paths back to DoDragDrop returning. EnableWindow is documented to stop
//     "mouse and keyboard input to the specified window" and nothing else, so
//     the window should still paint and still process messages posted to it.
//
// What Microsoft documents about the thread DoDragDrop may be called from is:
// nothing. The page says "You must call OleInitialize before calling this
// function" and that the function "enters a loop in which it calls various
// methods in the IDropSource and IDropTarget interfaces". It does not say which
// thread may call it, that the caller needs a message queue, or that the caller
// must own the window under the cursor. So -thread is an experiment and not an
// implementation of a documented capability, and the README says so.

var (
	// threadMode and disableMode are the two flags. They are written once in
	// main, before the window exists and before any goroutine of ours is
	// started, and read from every thread afterwards.
	threadMode  bool
	disableMode bool

	// oleThreadID is the -thread thread: the one that calls OleInitialize and
	// DoDragDrop when the window's thread does not. It is zero in every other
	// mode, and zero never matches a real thread id, so the accounting that
	// consults it needs no other guard.
	oleThreadID atomic.Uint32

	// oleHWND is that thread's message-only window, kept so that the drag can
	// stop posting to it and drain its queue before it goes away.
	oleHWND atomic.Uintptr

	// aliveTicks counts the -thread heartbeat, which is the window thread
	// saying, from inside its own loop, that it is still dispatching.
	aliveTicks atomic.Int64
)

// ---------------------------------------------------------------------------
// The dedicated OLE thread.

const oleWindowClass = "EnfoldDragProtoOleThread"

var (
	oleWndProcCallback = syscall.NewCallback(oleWndProc)
	oleClassOnce       sync.Once
	oleClassAtom       uintptr
	oleClassErr        error
)

// oleWndProc is the message-only window's procedure. It handles the probe and
// nothing else: the window exists to give the thread a message queue, not to do
// anything.
func oleWndProc(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	if probeMsg != 0 && message == probeMsg {
		handleProbe(destOle, wParam, lParam)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(message), wParam, lParam)
	return r
}

func registerOleWindowClass() (uintptr, error) {
	oleClassOnce.Do(func() {
		hinst, _, _ := procGetModuleHandleW.Call(0)
		cls, err := windows.UTF16PtrFromString(oleWindowClass)
		if err != nil {
			oleClassErr = err
			return
		}
		wc := wndClassExW{
			lpfnWndProc:   oleWndProcCallback,
			hInstance:     windows.Handle(hinst),
			lpszClassName: cls,
		}
		wc.cbSize = uint32(unsafe.Sizeof(wc))
		atom, _, callErr := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
		if atom == 0 {
			oleClassErr = fmt.Errorf("RegisterClassExW(%s): %w", oleWindowClass, callErr)
			return
		}
		oleClassAtom = atom
	})
	return oleClassAtom, oleClassErr
}

// createMessageOnlyWindow gives the calling thread a message queue and a window
// to post to. HWND_MESSAGE as the parent is what makes it message-only: "It is
// not visible, has no z-order, cannot be enumerated, and does not receive
// broadcast messages. The window simply dispatches messages." That is exactly
// what is wanted here -- nothing on screen, and a queue that only this
// experiment posts into.
//
// It cannot be unit-tested. Creating a window needs a real window station and a
// desktop, a registered class and a thread that owns the result, and the whole
// point of the call is the side effect on the calling thread; a test that made
// one would be testing user32 rather than this program. The build and vet gates
// cover the shape of the call, and the log of a real run covers the rest.
func createMessageOnlyWindow() (uintptr, error) {
	atom, err := registerOleWindowClass()
	if err != nil {
		return 0, err
	}
	title, err := windows.UTF16PtrFromString("Enfold drag-out prototype - OLE thread")
	if err != nil {
		return 0, err
	}
	hinst, _, _ := procGetModuleHandleW.Call(0)
	h, _, callErr := procCreateWindowExW.Call(
		0, atom, uintptr(unsafe.Pointer(title)),
		0, 0, 0, 0, 0,
		hwndMessage, 0, hinst, 0)
	if h == 0 {
		return 0, fmt.Errorf("CreateWindowExW(HWND_MESSAGE): %w", callErr)
	}
	return h, nil
}

// runDragOnOwnThread is -thread. The window thread hands the drag over and goes
// straight back to its loop; everything the drag needs -- the apartment, the
// window, the data object, the drop source and DoDragDrop itself -- is made and
// called on the new thread.
//
// done is what releases the one-drag-at-a-time guard, and it runs last: a defer
// registered first is a defer that runs after the apartment has been taken down
// and the window destroyed, which is the order a second gesture must not be able
// to interleave with.
func runDragOnOwnThread(n int, done func()) {
	go func() {
		defer done()

		// The apartment, the window and DoDragDrop must all be the same OS
		// thread's, exactly as they are in main. Without this the goroutine
		// could be moved between OS threads at any call and none of the three
		// would belong to the thread the next one ran on.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		tid := windows.GetCurrentThreadId()
		oleThreadID.Store(tid)
		defer oleThreadID.Store(0)
		logf("-thread: drag %d runs on a dedicated OLE thread (tid %d); the window thread (tid %d) keeps its own message loop",
			n, tid, dragThreadID.Load())

		// Same contract as main's: S_FALSE is a success that must still be
		// balanced, a failure is fatal to this drag and to nothing else.
		hr, _, _ := procOleInitialize.Call(0)
		logf("-thread: OleInitialize(NULL) on tid %d -> %s", tid, hrName(hr))
		if int32(uint32(hr)) < 0 {
			logf("-thread: drag %d is abandoned -- without an apartment on this thread DoDragDrop cannot be called here", n)
			return
		}
		defer func() {
			// The same caution as main's, and for the same reason: this call
			// releases every COM object of the apartment, so a target still
			// holding one would have it destroyed underneath.
			if live := liveComObjects(); len(live) > 0 {
				logf("-thread: skipping OleUninitialize on tid %d: the target still holds %d object(s), and this call is what would take them away",
					tid, len(live))
				return
			}
			procOleUninitialize.Call()
			logf("-thread: OleUninitialize on tid %d", tid)
		}()

		hwnd, err := createMessageOnlyWindow()
		if err != nil {
			// Not fatal on purpose: whether DoDragDrop works on a thread with
			// no window of its own is one of the things being measured, so the
			// drag goes ahead and the log says what it was missing.
			logf("-thread: could not create the message-only window: %v -- the drag goes ahead without one, and what DoDragDrop does then is part of the answer", err)
		} else {
			logf("-thread: message-only window 0x%X created on tid %d with HWND_MESSAGE as its parent, so this thread has a message queue",
				hwnd, tid)
			oleHWND.Store(hwnd)
			registerProbeWindow(hwnd, destOle)
			defer func() {
				oleHWND.Store(0)
				unregisterProbeWindow(hwnd)
				procDestroyWindow.Call(hwnd)
				logf("-thread: the message-only window 0x%X was destroyed", hwnd)
			}()
		}

		runDrag(n)
	}()
}

// drainOleQueue empties the OLE thread's own queue once, AFTER DoDragDrop has
// returned. The thread pumps nothing of its own during the drag -- that is the
// experiment -- so without this a probe that was posted, queued and never
// dispatched would look exactly like a probe that was never posted at all. One
// drain after the fact tells the two apart, and cannot affect what the modal
// loop did, because the modal loop is over.
func drainOleQueue() {
	h := oleHWND.Load()
	if h == 0 {
		return
	}
	// Stop posting to it first, so that what is drained is what the drag put
	// there rather than a moving target.
	unregisterProbeWindow(h)
	var m msgW
	total, found := 0, 0
	for {
		r, _, _ := procPeekMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, pmRemove)
		if r == 0 {
			break
		}
		total++
		if probeMsg != 0 && m.message == probeMsg {
			found++
		}
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	logf("-thread: DoDragDrop has returned; draining the OLE thread's own queue once -- %d message(s) were still in it, %d of them probes. Anything found here was posted during the drag and never dispatched by the modal loop",
		total, found)
}

// ---------------------------------------------------------------------------
// -disable.

// -disable is phase-specific, and the phases are the reason it exists.
//
// Disabling for the whole gesture would be wrong for the real application. The
// window has work to do during the hover -- a Cancel button, and a drop back
// onto itself -- and during the extraction, which is where a progress strip
// would live. The one stretch where the window genuinely has nothing to offer is
// the one AFTER the post-release GetData has handed the paths back: from there
// the copy is Explorer's and the source is not even told about it. So that is
// the stretch the window is held for, and the window comes back the moment
// DoDragDrop returns. That is also what WinRAR's window looks like from outside.
//
// EnableWindow "Enables or disables mouse and keyboard input to the specified
// window or control", and the documented side effects are two messages sent
// before it returns -- WM_CANCELMODE, then WM_ENABLE if the state changed.
// Nothing on that page says what happens to messages that are POSTED to a
// disabled window, or whether it still paints; the probe receipts under
// -disable are the measurement of the first and the eye is the measurement of
// the second.
//
// windowHeld is the one-hold-at-a-time guard, so that the release on every
// completion path is a no-op when nothing was held.
var windowHeld atomic.Bool

// holdWindowAfterHandover is the moment: a post-release GetData has given the
// target what it came for -- the staged paths, or a contents stream -- and from
// here the copy is the target's business and the window has nothing to offer.
// A request during the hover never gets here, which is the whole point of the
// phase check its callers make.
//
// It can be called from any thread, including one of Explorer's under -agile,
// because it never calls EnableWindow itself unless it is already on the
// window's thread.
func holdWindowAfterHandover(why string) {
	// Only while a drag is actually running. A target that still holds the data
	// object can call GetData long after DoDragDrop returned -- the first real
	// 5 GiB drop had one stream taken before the drop and read after it -- and a
	// hold taken then would have nothing left to give it back.
	if !disableMode || !dragRunning.Load() || !windowHeld.CompareAndSwap(false, true) {
		return
	}
	requestWindowEnabled(false, why)
}

// releaseWindowHold is called on every path out of a drag, so a cancelled drag,
// an abandoned one and a completed one all leave the window enabled.
func releaseWindowHold(why string) {
	if !disableMode || !windowHeld.CompareAndSwap(true, false) {
		return
	}
	requestWindowEnabled(true, why)
}

// requestWindowEnabled gets the change onto the window's own thread. EnableWindow
// SENDS WM_CANCELMODE and WM_ENABLE before it returns, and a send from another
// thread blocks until the window's thread picks them up; posting instead means
// no thread of ours ever waits on another, which is the rule -thread is built
// around. When this already IS the window thread the call is made directly,
// because a post would then not be delivered until the modal loop dispatched it
// -- which is the very thing under test.
func requestWindowEnabled(enable bool, why string) {
	h := mainHWND.Load()
	if h == 0 {
		return
	}
	verb := "disable"
	if enable {
		verb = "enable"
	}
	if windows.GetCurrentThreadId() == dragThreadID.Load() {
		applyWindowEnabled(h, enable, why+" (applied here: this is the window thread)")
		return
	}
	arg := uintptr(0)
	if enable {
		arg = 1
	}
	r, _, _ := procPostMessageW.Call(h, wmSetEnabled, arg, 0)
	if r == 0 {
		logf("-disable: could not ask the window thread to %s the window -- PostMessageW refused it (%s)", verb, why)
		return
	}
	logf("-disable: asked the window thread to %s the window -- %s", verb, why)
}

// applyWindowEnabled is the call itself, and runs on the window thread only.
func applyWindowEnabled(h uintptr, enable bool, note string) {
	arg := uintptr(0)
	if enable {
		arg = 1
	}
	prev, _, _ := procEnableWindow.Call(h, arg)
	logf("-disable: EnableWindow(0x%X, %v) -- it was %s before and is %s now; %s",
		h, enable, enabledWord(prev == 0), enabledWord(windowEnabled(h)), note)
}

// enabledWord reads EnableWindow's return the way its documentation defines it:
// "If the window was previously disabled, the return value is nonzero."
func enabledWord(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

// ---------------------------------------------------------------------------
// AttachThreadInput.

// attachDragInput is what makes -thread more than a thread with a window on it.
//
// Input state belongs to a thread. AttachThreadInput "Attaches or detaches the
// input processing mechanism of one thread to that of another thread", and what
// that buys is documented in one sentence: "a thread can share its input states
// (such as keyboard states and the current focus window) with another thread.
// Keyboard and mouse events received by both threads are processed in the order
// they were received." Nothing on that page mentions DoDragDrop, mouse capture,
// or running a drag off-thread -- every one of those is folklore, and the README
// says so.
//
// The DIRECTION here is Chromium's, not the obvious one. Its drag ran on a
// "Chrome_DragDropThread" and the call it made, on the UI thread, was
//
//	AttachThreadInput(drag_out_thread_id, GetCurrentThreadId(), TRUE);
//
// -- idAttach the DRAG thread, idAttachTo the UI thread, with the comment
// "Attach the input state of the background thread to the UI thread so that
// SetCursor can work from the background thread." That is the one direction with
// evidence behind it from a shipped browser, so it is the one used, and the log
// prints both ids in the order they are passed so a reader never has to guess.
//
// Two documented caveats apply and are worth watching for in the log. The call
// "fails if either of the specified threads does not have a message queue",
// which is why the message-only window is created first. And "key state ... is
// reset after a call to AttachThreadInput" -- so if this resets the left button
// under the drag, the very first QueryContinueDrag will see the button up and
// drop immediately. That failure has a signature: QueryContinueDrag #1 returning
// DRAGDROP_S_DROP within milliseconds of the call.
//
// The attachment is undone as soon as DoDragDrop returns, on every path: the
// state is shared for as long as it lasts, and a thread left attached to one
// that has exited is a shape nothing here wants to be in.
func attachDragInput() func() {
	if !threadMode {
		return func() {}
	}
	win := dragThreadID.Load()
	drag := windows.GetCurrentThreadId()
	if win == 0 || win == drag {
		return func() {}
	}
	r, _, err := procAttachThreadInput.Call(uintptr(drag), uintptr(win), 1)
	if r == 0 {
		logf("-thread: AttachThreadInput(idAttach=drag tid %d, idAttachTo=window tid %d, TRUE) FAILED (%v) -- the drag runs with its own input state, and whether that is enough is part of the answer",
			drag, win, err)
		return func() {}
	}
	logf("-thread: AttachThreadInput(idAttach=drag tid %d, idAttachTo=window tid %d, TRUE) succeeded: the two threads share one input state for the duration, and the documented key-state reset has just happened",
		drag, win)
	return func() {
		back, _, derr := procAttachThreadInput.Call(uintptr(drag), uintptr(win), 0)
		if back == 0 {
			logf("-thread: AttachThreadInput(idAttach=drag tid %d, idAttachTo=window tid %d, FALSE) FAILED (%v) -- the threads are STILL attached, which is a finding of its own",
				drag, win, derr)
			return
		}
		logf("-thread: AttachThreadInput(idAttach=drag tid %d, idAttachTo=window tid %d, FALSE): the input states are separate again", drag, win)
	}
}

// keyStateWords renders the grfKeyState IDropSource is handed, which is the only
// thing a drop source is told about what the user is doing. The raw value is
// printed beside the names because MK_ALT is not among winuser.h's MK_ flags and
// nothing here will guess at a bit.
func keyStateWords(keys uint32) string {
	var parts []string
	add := func(bit uint32, name string) {
		if keys&bit != 0 {
			parts = append(parts, name)
		}
	}
	add(mkLButton, "MK_LBUTTON")
	add(mkRButton, "MK_RBUTTON")
	add(mkMButton, "MK_MBUTTON")
	add(mkShift, "MK_SHIFT")
	add(mkControl, "MK_CONTROL")
	add(mkXButton1, "MK_XBUTTON1")
	add(mkXButton2, "MK_XBUTTON2")
	if len(parts) == 0 {
		return fmt.Sprintf("no keys or buttons (0x%04X)", keys)
	}
	s := parts[0]
	for _, p := range parts[1:] {
		s += "|" + p
	}
	return fmt.Sprintf("%s (0x%04X)", s, keys)
}

// ---------------------------------------------------------------------------
// The heartbeat, which is -thread's other half.

// startAliveTimer puts a 250 ms WM_TIMER on the main window so that the window
// thread reports, from inside its own loop, that it is still dispatching while
// the drag runs somewhere else. It is set from the window thread, in the gesture
// that starts the drag, because SetTimer's timer belongs to the window and the
// window belongs to that thread.
//
// A timer is worth having beside the probe: WM_TIMER is generated for the queue
// rather than posted into it, so the two together say whether a loop is running
// at all and whether posted messages in particular get through.
func startAliveTimer(hwnd uintptr) {
	if !threadMode || hwnd == 0 {
		return
	}
	aliveTicks.Store(0)
	r, _, err := procSetTimer.Call(hwnd, timerAlive, uintptr(probeInterval/time.Millisecond), 0)
	if r == 0 {
		logf("-thread: SetTimer on the main window failed (%v); the window thread's liveness will have to be read from the probe alone", err)
		return
	}
	logf("-thread: a %s WM_TIMER on the main window will report the window thread's own loop while the drag runs elsewhere", probeInterval)
}

func stopAliveTimer(hwnd uintptr) {
	if !threadMode || hwnd == 0 {
		return
	}
	procKillTimer.Call(hwnd, timerAlive)
	logf("-thread: the drag is over; the window thread dispatched %d WM_TIMER tick(s) of %s while it ran",
		aliveTicks.Load(), probeInterval)
}

// aliveTick is the timer's line, and it is deliberately NOT throttled: the
// claim -thread has to make good is that the window thread keeps running
// through every phase, and a gap in a line that should appear four times a
// second is the only way to see the claim fail. It is a line per 250 ms only
// while a drag is running under -thread, and the timer is killed when the drag
// ends.
func aliveTick() {
	n := aliveTicks.Add(1)
	logf("-thread: the window thread is alive and dispatching -- WM_TIMER tick %d, the drag is in phase %s",
		n, currentDragPhase())
}

// ---------------------------------------------------------------------------
// Where the button came up.

// noteRelease writes down the release position and whether it was over this
// program's own window. A drag source is never told where the drop landed --
// which is the point made under "What this deliberately does not do" -- but the
// cursor's own position at the moment the button comes up is something anyone
// can read, and one thing it settles is whether the user let go over the window
// they dragged from.
//
// It matters for -thread and -disable in particular. Under -thread the drag runs
// on a thread that owns no window, so nothing about this test may depend on the
// caller's thread; GetCursorPos, GetWindowRect and WindowFromPoint are all
// window-station-wide and none of them is. Under -disable the hit test alone
// would be wrong: WindowFromPoint "does not retrieve a handle to a hidden or
// disabled window", so a disabled main window is invisible to it -- which is why
// the frame test is taken as well, and why the two are reported separately.
func noteRelease() {
	p, ok := cursorPos()
	if !ok {
		logf("the button came up, but GetCursorPos failed, so the release position is unknown")
		return
	}
	main := mainHWND.Load()
	frame, haveFrame := windowRect(main)
	under := windowUnderPoint(p)
	self, why := selfDropVerdict(p, frame, haveFrame, under, main)
	if self {
		logf("the button came up at screen (%d, %d): %s -- a SELF-DROP. The prototype's window is not a drop target, so nothing should come of it",
			p.x, p.y, why)
		return
	}
	logf("the button came up at screen (%d, %d): %s", p.x, p.y, why)
}

// selfDropVerdict is the decision itself, separated from every API call so that
// it can be checked without a window. under is the root window the hit test
// named (zero if there was none), main is ours, and haveFrame says whether the
// frame is usable at all.
func selfDropVerdict(p point, frame rect, haveFrame bool, under, main uintptr) (bool, string) {
	if main == 0 {
		// Nothing to compare against, so nothing may be called a self-drop: a
		// frame read for a window that is not there describes no window.
		return false, fmt.Sprintf("over %s; there is no window of ours to compare it with", windowWord(under))
	}
	inFrame := haveFrame && pointInRect(p, frame)
	hit := under != 0 && under == main

	switch {
	case hit && inFrame:
		return true, fmt.Sprintf("over our own window 0x%X, by both the hit test and its frame", main)
	case inFrame:
		// The case -disable creates, and the reason the frame is consulted at
		// all: a disabled window is not returned by WindowFromPoint.
		return true, fmt.Sprintf("inside our own window's frame, though the hit test named %s -- which is what a disabled or covered window looks like to WindowFromPoint",
			windowWord(under))
	case hit:
		// The frame could not be read, or the point is in a part of the window
		// the frame does not cover. The hit test is the stronger signal.
		return true, fmt.Sprintf("over our own window 0x%X by the hit test, though not inside the frame that was read for it", main)
	case haveFrame:
		return false, fmt.Sprintf("outside our own window (its frame is %d,%d-%d,%d) and over %s",
			frame.left, frame.top, frame.right, frame.bottom, windowWord(under))
	default:
		return false, fmt.Sprintf("over %s; our own window's frame could not be read, so only the hit test was used", windowWord(under))
	}
}

func windowWord(h uintptr) string {
	if h == 0 {
		return "no window at all"
	}
	return fmt.Sprintf("window 0x%X", h)
}

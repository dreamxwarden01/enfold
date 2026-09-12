//go:build windows

package dragout

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/windows"
)

// The drag itself, on a thread of its own (APP.md §3, ruled 2026-09-11 on
// the measurements in docs/research/drag-out.md, "The drag thread,
// measured"), one native drag at a time, and the teardown at the end of the
// process.
//
// Why a thread of its own. With the window's thread as the drag thread —
// what this was until the ruling — DoDragDrop's modal loop does dispatch
// messages posted to the window during the hover, but inside the target's
// synchronous Drop it pumps almost nothing: none of the probes posted
// during a 64 MiB extraction arrived before that phase ended, and all of
// them 122 ms later. On a dedicated thread, with the input attached, the
// drag ran, the cursor followed, the drops landed, and the window's own
// queue stayed alive throughout — extraction included. So the whole of one
// drag lives here: the apartment is this thread's, the message-only window
// is this thread's, the data object, the drop source and their enumerator
// are made here, and the result goes back to the caller down a channel. The
// caller's thread waits on that channel or does not, as it likes; nothing
// on this side ever waits on the caller.
//
// What Microsoft documents about the thread DoDragDrop may be called from
// is nothing at all: the page says "You must call OleInitialize before
// calling this function" and no more. The apartment is therefore the one
// documented requirement, and it is met here rather than borrowed from the
// window's thread; everything else about this shape — the message-only
// window, the input attachment and its direction — is the prototype's
// measurement, and tools/dragproto's README says how each was arrived at.

var (
	// running is the one drag at a time per process: set by Begin and
	// cleared when the drag thread has finished its teardown. Two cannot
	// run at once — DoDragDrop is modal on its thread, and one gesture is
	// all a mouse has — and a second Begin while one runs is ErrBusy.
	running atomic.Bool

	// endLog is the log the process's own teardown speaks with: Shutdown
	// runs long after the last drag's Options are gone, and a line about a
	// data object a target still holds is worth having in the same file as
	// the drag it belonged to.
	endMu  sync.Mutex
	endLog func(string, ...any) = discard
)

func setEndLog(log func(string, ...any)) {
	if log == nil {
		return
	}
	endMu.Lock()
	endLog = log
	endMu.Unlock()
}

func endLogger() func(string, ...any) {
	endMu.Lock()
	defer endMu.Unlock()
	return endLog
}

// Shutdown is the end of the process's use of this package, after
// everything else. Every staging folder still registered is decided about —
// a folder nothing was handed out of goes, one whose paths a target has is
// left manifested for the next launch's scavenge, because consumers open
// dropped paths late — and every data object a target still holds is
// revoked: from here on its GetData answers E_UNEXPECTED rather than name
// files out of a drag that is over.
//
// There is no OleUninitialize here any more, and nothing for one to
// balance: OLE belongs to each drag's own thread now and is closed there
// (see endApartment). What the revoke is for is the finding that made it
// (tools/dragproto, "Findings so far"): OleUninitialize "Closes the COM
// library on the apartment, releases any class factories, other COM
// objects, or servers held by the apartment", and in the prototype's second
// real drop that one call took the data object from five references to zero
// under a target still using it — its progress dialog could not even be
// cancelled afterwards. So an object a target still holds is never
// destroyed by us; it is revoked, and process exit ends the transfer in a
// way the target's own RPC layer understands.
func Shutdown() {
	log := endLogger()
	closeAllStages()
	if held := revokeLiveDataObjects(); held > 0 {
		log("drag: shutdown: %d data object(s) a target still holds were revoked rather than destroyed under it", held)
	}
}

// revokeLiveDataObjects is the forced teardown's half over the objects:
// every data object the target still holds answers E_UNEXPECTED from here
// on. It returns how many there were.
func revokeLiveDataObjects() int {
	held := 0
	for _, o := range liveComObjects() {
		if d, ok := o.impl.(*dataObject); ok {
			d.revoked.Store(true)
			held++
		}
	}
	return held
}

// Drag is one native drag, from Begin to the end of its DoDragDrop; the
// staging folder it made outlives it as the watch and the scavenge decide.
type Drag struct {
	opts    Options
	s       *stage
	log     func(string, ...any)
	started atomic.Bool
	// held: the target still had a reference to the data object when we
	// gave ours back, so the apartment is left open rather than closed over
	// an object somebody is using. Written on the drag thread before its
	// own teardown reads it.
	held bool
}

// Begin makes the staging folder atomically with its manifest and builds
// what the drag needs, before DoDragDrop: a target that asks for CF_HDROP
// during the hover is answered with the final paths, and they exist from
// here. It answers ErrBusy while another drag is running. Nothing here
// touches OLE — the apartment is the drag thread's, and that thread does
// not exist until Start — so Begin may be called from any goroutine, the
// window's thread included.
func Begin(opts Options) (*Drag, error) {
	setEndLog(opts.Log)
	if !running.CompareAndSwap(false, true) {
		return nil, ErrBusy
	}
	s, err := newStage(opts)
	if err != nil {
		running.Store(false)
		return nil, err
	}
	d := &Drag{opts: opts, s: s, log: s.log}
	d.log("drag %s: staging folder made for %d item(s), %d at the top", s.id, len(s.paths), len(s.drop))
	return d, nil
}

// Start puts the drag on a thread of its own and hands back the channel its
// one Outcome arrives on. It returns as soon as the thread is running: the
// caller may wait on the channel or not, and either way no thread of this
// package's ever waits on the caller's.
//
// Before the hand-off it asks the window's thread to let go of the mouse
// capture, through Options.OnWindowThread — ReleaseCapture "Releases the
// mouse capture from a window in the current thread", so it is the window's
// thread's to make and not this one's. That is the one moment Start borrows
// the caller's thread for, and it is over before the drag thread exists.
//
// Start must therefore not be called ON the window's own thread: it would
// be asking that thread to run something while standing on it. The shell
// calls it from the goroutine of a bound call, which is never the main one.
func (d *Drag) Start() (<-chan Outcome, error) {
	if !d.started.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("dragout: the drag was started twice")
	}
	d.releaseCapture()
	out := make(chan Outcome, 1)
	go d.thread(out)
	return out, nil
}

// Run is Start and the wait in one, for a caller that has a goroutine to
// spare and wants the drag's end as a return value — the shape the core's
// operation is written against. It blocks until DoDragDrop has returned and
// the drag thread has taken itself down, which is as long as Explorer's
// copy and its conflict dialog last, and it must not be called on the
// window's own thread for the reason Start gives.
//
// When it returns the drag is over: Options.OnPhase has reported Done with
// the reason, and the staging folder has been deleted or left for the
// scavenge (APP.md §3, ruled 2026-09-11).
func (d *Drag) Run() (Result, error) {
	ch, err := d.Start()
	if err != nil {
		return Result{}, err
	}
	o := <-ch
	return o.Result, o.Err
}

// Cancel ends the drag from any thread: a running extraction returns at its
// next chunk with the context's error, so the drop's GetData fails and the
// target abandons the drop, and a drag still in the hover ends at the
// source's next QueryContinueDrag as Escape would.
func (d *Drag) Cancel() {
	d.s.cancel()
}

// releaseCapture is the hand-off's one call on the window's thread. A drag
// that begins from a press the window's thread is holding the mouse for
// would otherwise run against that capture: the window would go on
// receiving the mouse wherever the cursor went, and the drag would never
// see it leave. The prototype released it on the window thread before
// starting the drag thread and needed no message forwarding afterwards,
// which is the shape kept here.
//
// A caller with no window thread to ask — a test, a shell that captures
// nothing — leaves OnWindowThread nil and nothing is released.
func (d *Drag) releaseCapture() {
	if d.opts.OnWindowThread == nil {
		return
	}
	var had bool
	d.opts.OnWindowThread(func() { had = releaseMouseCapture() })
	d.log("drag %s: ReleaseCapture on the window's own thread before the hand-off -> %v", d.s.id, had)
}

// thread is the drag's goroutine, from its first line to its last. The
// three defers below are the order the end has to happen in, and a defer
// registered first runs last: the apartment is closed and the window
// destroyed inside run, then running is cleared, and only then does the
// answer go out. That last step is the one that matters to the user: a
// caller who has heard that the drag is over may start the next gesture in
// the same breath, and it must not meet this one's teardown as ErrBusy.
func (d *Drag) thread(out chan<- Outcome) {
	var o Outcome
	defer func() { out <- o }()
	defer running.Store(false)
	defer func() {
		if r := recover(); r != nil {
			// A panic here is ours, and it would otherwise take the process
			// with it: a goroutine's panic is not the caller's to recover,
			// and no drag is worth the window and the vault going with it.
			// The recovered value only — never a file name (APP.md §3).
			d.log("drag %s: the drag thread panicked and was recovered: %v", d.s.id, r)
			o = Outcome{Result: d.panicked(), Err: fmt.Errorf("dragout: the drag thread panicked: %v", r)}
		}
	}()
	res, err := d.run()
	o = Outcome{Result: res, Err: err}
}

// panicked is the end a recovered panic gives the drag: the operation hears
// Done/failed, so the caller's strip goes rather than standing for ever,
// and the staging folder goes with it — unless DoDragDrop had already
// returned and the end was decided, in which case that decision stands.
func (d *Drag) panicked() Result {
	s := d.s
	s.mu.Lock()
	over := s.dragOver
	s.mu.Unlock()
	if !over {
		s.reportDone(Failed)
		s.remove()
	}
	return Result{Folder: s.root}
}

// run is everything the drag thread does, in the order it does it: the
// apartment, the window, the input attachment, the objects, the drag. The
// teardown is that list read backwards, which is what the defers are for —
// the objects go back, the input is detached, the window is destroyed, the
// apartment is closed, and only then is the thread let go of.
func (d *Drag) run() (Result, error) {
	// The apartment, the window and DoDragDrop must all be one OS thread's.
	// Without this the goroutine could be moved between threads at any call
	// and none of the three would belong to the thread the next one ran on.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	s := d.s
	tid := windows.GetCurrentThreadId()
	win := windowThreadID(d.opts.Window)
	d.log("drag %s: the drag runs on a thread of its own (tid %d); the window 0x%X belongs to thread %d, which keeps its own message loop throughout", s.id, tid, d.opts.Window, win)

	hr := oleInitializeOnThread()
	d.log("drag %s: OleInitialize(NULL) on tid %d -> %s", s.id, tid, hrName(hr))
	if failed(hr) {
		// RPC_E_CHANGED_MODE would be this thread already being an MTA,
		// which no drag can run from; anything else is as fatal. It is
		// fatal to this drag and to nothing else.
		return d.abandon(fmt.Errorf("dragout: OleInitialize: %s", hrName(hr)))
	}
	defer d.endApartment(tid)

	hwnd, werr := newDragWindow()
	if werr != nil {
		// Fatal on purpose. The measured recipe has the window in it —
		// AttachThreadInput "fails if either of the specified threads does
		// not have a message queue" — and a drag that skipped it would be a
		// shape nobody has measured. A failed drag with a line in the log
		// is the better answer.
		return d.abandon(fmt.Errorf("dragout: the drag thread's message-only window: %w", werr))
	}
	d.log("drag %s: the drag thread's message-only window 0x%X was created with HWND_MESSAGE as its parent, so this thread has a queue", s.id, hwnd)
	defer func() {
		closeDragWindow(hwnd)
		d.log("drag %s: the drag thread's message-only window 0x%X was destroyed", s.id, hwnd)
	}()

	detach := d.attachInput(tid, win)
	defer detach()

	// Made here, on this thread, and not before: an apartment object made
	// on one thread and used from another is the shape this whole ruling is
	// about. The data object is agile, so a call from the target's own RPC
	// thread runs there rather than being marshalled back to this one; the
	// drop source is not, and OLE calls it on this thread and nowhere else.
	dataObj := newHDropDataObject(s, d.log)
	srcObj := newDropSourceObject(s, d.opts.Window, d.log)
	// "Release the data object." Ours, on every path out of here — a drag
	// that fell over included, because an object nobody gives back is a
	// pinned cell and a stage held alive for the life of the process. It is
	// the first thing the teardown does, and endApartment the last, because
	// what the one finds is what the other decides on.
	defer d.releaseObjects(dataObj, srcObj)

	var effect uint32 = dropEffectNone
	start := time.Now()
	hr = doDragDrop(dataObj.unknown(), srcObj.unknown(), allowedEffects, &effect)
	took := time.Since(start)
	d.log("drag %s: DoDragDrop returned %s after %s, effect %s", s.id, hrName(hr), took.Round(time.Millisecond), effectName(effect))

	// "If the return value is DRAGDROP_S_DROP, DoDragDrop calls
	// IDropTarget::Drop ... The DoDragDrop function returns the last effect
	// code to the source"; DRAGDROP_S_CANCEL is the cancel, and anything
	// failed is a drag that could not be run at all. The effect is the
	// truth about what the target did, the transfer being synchronous.
	var end Reason
	var err error
	switch {
	case uint32(hr) == dragDropSCancel:
		end = Cancelled
	case failed(hr):
		end = Failed
		err = fmt.Errorf("dragout: DoDragDrop: %s", hrName(hr))
	}
	// The end of the drag: the reason for the strip and the folder's fate,
	// both decided here, because there is nothing left to wait for.
	s.dragEnded(end, effect)

	s.mu.Lock()
	res := Result{SelfDrop: s.selfDrop, Extracted: s.extracted && !s.failed, Effect: effect, Folder: s.root}
	s.mu.Unlock()
	return res, err
}

// releaseObjects gives back the two references this side made. A target
// that holds one of its own past the drop keeps the data object alive, and
// that is the one fact endApartment turns on: the object answers out of a
// drag that is over until Shutdown revokes it, rather than being destroyed
// under a target still calling it.
func (d *Drag) releaseObjects(dataObj, srcObj *comObject) {
	srcObj.release()
	if n := dataObj.release(); n > 0 {
		d.held = true
		d.log("drag %s: the target still holds the data object (%d reference(s)) after the drag", d.s.id, n)
	}
}

// abandon is a drag the thread could not run at all: the caller hears
// Done/failed, so its strip goes, and the staging folder goes with it —
// nothing was ever handed out of it.
func (d *Drag) abandon(err error) (Result, error) {
	d.s.reportDone(Failed)
	d.s.remove()
	return Result{Folder: d.s.root}, err
}

// attachInput joins the drag thread's input state to the window thread's
// for the length of the drag, and hands back the undoing of it.
//
// Input state belongs to a thread. AttachThreadInput lets "a thread ...
// share its input states (such as keyboard states and the current focus
// window) with another thread", and "keyboard and mouse events received by
// both threads are processed in the order they were received". Nothing on
// that page mentions DoDragDrop, mouse capture or running a drag
// off-thread: this is the prototype's measurement, not a documented
// capability, and experiments 19–21 are what stand behind it.
//
// The DIRECTION is Chromium's — idAttach the drag thread, idAttachTo the
// window's — because that is the one with a shipped browser behind it, and
// it is the one that worked every time in the prototype. Two documented
// caveats are worth watching for in the log: the call "fails if either of
// the specified threads does not have a message queue", which is why the
// message-only window is made first; and "key state ... is reset after a
// call to AttachThreadInput", so a reset that took the left button down
// with it would show as the very first QueryContinueDrag returning
// DRAGDROP_S_DROP milliseconds later.
//
// The attachment is undone on every path out, DoDragDrop's failures
// included: the state is shared for as long as the drag lasts and no
// longer, and a thread left attached to one that has exited is a shape
// nothing here wants to be in. The documentation does not require the
// detach; this is a choice, and this is why.
func (d *Drag) attachInput(drag, window uint32) func() {
	if window == 0 || window == drag {
		// Nothing to attach to: no window, or a caller whose window is this
		// thread's, which Begin's contract does not allow but which would
		// be "A thread cannot attach to itself" if it happened.
		d.log("drag %s: no input attachment: the window's thread is %d and the drag's is %d", d.s.id, window, drag)
		return func() {}
	}
	ok, err := attachThreadInput(drag, window, true)
	if !ok {
		d.log("drag %s: AttachThreadInput(idAttach=drag tid %d, idAttachTo=window tid %d, TRUE) FAILED (%v): the drag runs with its own input state", d.s.id, drag, window, err)
		return func() {}
	}
	d.log("drag %s: AttachThreadInput(idAttach=drag tid %d, idAttachTo=window tid %d, TRUE) succeeded: the two threads share one input state for the drag's length", d.s.id, drag, window)
	return func() {
		back, derr := attachThreadInput(drag, window, false)
		if !back {
			d.log("drag %s: AttachThreadInput(idAttach=drag tid %d, idAttachTo=window tid %d, FALSE) FAILED (%v): the threads are STILL attached, which is a finding of its own", d.s.id, drag, window, derr)
			return
		}
		d.log("drag %s: AttachThreadInput(idAttach=drag tid %d, idAttachTo=window tid %d, FALSE): the input states are separate again", d.s.id, drag, window)
	}
}

// endApartment is the drag thread's last OLE act, and the one place this
// package decides not to make a documented call.
//
// OleUninitialize "Closes the COM library on the apartment, releases any
// class factories, other COM objects, or servers held by the apartment".
// When the target still holds a data object of ours, that sentence is the
// whole problem: the prototype watched exactly this call take an object
// from five references to zero under a target that was still using it. So
// the balance is skipped in that one case and the apartment is left as it
// is — the thread ends either way, the object is agile and answers on
// whichever thread calls it, and Shutdown revokes it at the process's end
// rather than letting it name files out of a drag that is over.
func (d *Drag) endApartment(tid uint32) {
	if d.held {
		d.log("drag %s: skipping OleUninitialize on tid %d: the target still holds a data object, and that call is what would take it away", d.s.id, tid)
		return
	}
	oleUninitializeOnThread()
	d.log("drag %s: OleUninitialize on tid %d: the drag's apartment is closed", d.s.id, tid)
}

//go:build windows

package dragout

import (
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// The drag's own thread, tested without one thing it does to the system
// (APP.md §3, ruled 2026-09-11): no apartment is taken, no window is made,
// no input is attached to anything and no drag is started. Every one of
// those is a seam in dragthread_windows.go, and this file puts its own call
// in each of them, so that what is checked is the lifecycle — the order,
// the thread, the balance and the answer — rather than user32's behaviour.

// windowTID is the thread the tests say the caller's window belongs to. Any
// value that is not this process's drag thread will do; a real one is never
// needed, because AttachThreadInput is a seam here too.
const windowTID = uint32(0xD9A6)

// fakeSystem records everything the drag thread asks of the system, in
// order and with the thread it asked from, and answers each call the way
// the test wants it answered.
type fakeSystem struct {
	mu     sync.Mutex
	events []event

	// The answers. Zero values are the ordinary ones: OleInitialize
	// succeeds, the window is made, the attachment takes.
	oleHR     uintptr
	windowErr error
	attachNo  bool
	// drag is DoDragDrop itself, and the test's whole story: it runs on the
	// drag thread with the object pointers the drag built.
	drag func(f *fakeSystem, data, source uintptr, allowed uint32, effect *uint32) uintptr
}

type event struct {
	what string
	tid  uint32
}

func (f *fakeSystem) note(what string) {
	f.mu.Lock()
	f.events = append(f.events, event{what, windows.GetCurrentThreadId()})
	f.mu.Unlock()
}

func (f *fakeSystem) list() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.events))
	for i, e := range f.events {
		out[i] = e.what
	}
	return out
}

// oneThread is the proof of runtime.LockOSThread: every call the drag made
// to the system was made from one and the same OS thread, and it was not
// the caller's.
func (f *fakeSystem) oneThread(t *testing.T, from string) uint32 {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.events) == 0 {
		t.Fatalf("%s: the drag asked the system for nothing at all", from)
	}
	tid := f.events[0].tid
	for _, e := range f.events {
		if e.tid != tid {
			t.Fatalf("%s: %q ran on thread %d, but the drag began on thread %d: the thread was not locked", from, e.what, e.tid, tid)
		}
	}
	if tid == windows.GetCurrentThreadId() {
		t.Fatalf("%s: the drag ran on the caller's own thread (%d)", from, tid)
	}
	return tid
}

func (f *fakeSystem) has(what string) bool {
	for _, e := range f.list() {
		if e == what {
			return true
		}
	}
	return false
}

// install puts the fake in every seam for the length of the test.
func (f *fakeSystem) install(t *testing.T) *fakeSystem {
	t.Helper()
	oleIn, oleOut := oleInitializeOnThread, oleUninitializeOnThread
	newWin, closeWin := newDragWindow, closeDragWindow
	attach, tid, capture, drag := attachThreadInput, windowThreadID, releaseMouseCapture, doDragDrop
	t.Cleanup(func() {
		oleInitializeOnThread, oleUninitializeOnThread = oleIn, oleOut
		newDragWindow, closeDragWindow = newWin, closeWin
		attachThreadInput, windowThreadID, releaseMouseCapture, doDragDrop = attach, tid, capture, drag
	})

	oleInitializeOnThread = func() uintptr {
		f.note("OleInitialize")
		return f.oleHR
	}
	oleUninitializeOnThread = func() { f.note("OleUninitialize") }
	newDragWindow = func() (uintptr, error) {
		if f.windowErr != nil {
			f.note("CreateWindow failed")
			return 0, f.windowErr
		}
		f.note("CreateWindow")
		return 0xBEEF, nil
	}
	closeDragWindow = func(hwnd uintptr) {
		if hwnd != 0xBEEF {
			t.Errorf("DestroyWindow(0x%X), want the window the drag made", hwnd)
		}
		f.note("DestroyWindow")
	}
	windowThreadID = func(hwnd uintptr) uint32 { return windowTID }
	releaseMouseCapture = func() bool {
		f.note("ReleaseCapture")
		return true
	}
	attachThreadInput = func(a, to uint32, on bool) (bool, error) {
		if to != windowTID {
			t.Errorf("AttachThreadInput(idAttachTo=%d), want the window's thread %d", to, windowTID)
		}
		if a != windows.GetCurrentThreadId() {
			t.Errorf("AttachThreadInput(idAttach=%d), want the drag's own thread %d", a, windows.GetCurrentThreadId())
		}
		if on {
			f.note("AttachThreadInput TRUE")
		} else {
			f.note("AttachThreadInput FALSE")
		}
		return !f.attachNo, nil
	}
	doDragDrop = func(data, source uintptr, allowed uint32, effect *uint32) uintptr {
		f.note("DoDragDrop")
		if f.drag == nil {
			return dragDropSCancel
		}
		return f.drag(f, data, source, allowed, effect)
	}
	return f
}

// beginFake starts one drag over the fake system, with a staging root under
// the test's own folder and a window handle that is never dereferenced.
func beginFake(t *testing.T, f *fakeSystem, items []Item) (*Drag, *phaseLog, *testLog) {
	t.Helper()
	// The test's own goroutine is locked to its OS thread for the length of
	// the test, and that is what makes "on the caller's thread" and "on the
	// drag's" tellable apart at all: an unlocked goroutine wanders between
	// threads at every call, and the thread it leaves is one the drag's own
	// goroutine may be given next.
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	isolateStages(t)
	f.install(t)
	ph, lg := &phaseLog{}, &testLog{}
	d, err := Begin(Options{
		Root: t.TempDir(), Window: 0x1000, Items: items,
		Extract: writeItems(items), OnPhase: ph.on, Log: lg.printf,
		OnWindowThread: func(fn func()) { fn() },
	})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	t.Cleanup(func() {
		running.Store(false)
		d.s.finish()
	})
	return d, ph, lg
}

func waitFor(t *testing.T, ch <-chan Outcome) Outcome {
	t.Helper()
	select {
	case o := <-ch:
		return o
	case <-time.After(10 * time.Second):
		t.Fatal("the drag thread never answered")
	}
	return Outcome{}
}

// TestTheDragThreadsLifecycle is the whole of one drag on its own thread,
// in order: the capture let go of on the caller's side first, then the
// apartment, the message-only window, the input attachment, DoDragDrop, and
// the same list backwards. Every call after the first is on one locked OS
// thread, and it is not the caller's.
func TestTheDragThreadsLifecycle(t *testing.T) {
	items := []Item{{Name: "one.bin", Size: 4}}
	var d *Drag
	f := &fakeSystem{drag: func(f *fakeSystem, data, source uintptr, allowed uint32, effect *uint32) uintptr {
		// The button comes up over somebody's window, and the drop's own
		// request follows: the stage's real state machine, driven from
		// where DoDragDrop would drive it.
		d.s.arm(false, "CabinetWClass")
		if _, ok := d.s.requestPaths(); !ok {
			t.Error("the drop's CF_HDROP request was refused")
		}
		*effect = dropEffectCopy
		return dragDropSDrop
	}}
	var ph *phaseLog
	var lg *testLog
	d, ph, lg = beginFake(t, f, items)

	ch, err := d.Start()
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	o := waitFor(t, ch)
	if o.Err != nil {
		t.Fatalf("the drag answered %v", o.Err)
	}
	if !o.Result.Extracted || o.Result.Effect != dropEffectCopy || o.Result.SelfDrop {
		t.Fatalf("the result is %+v, want a copy that extracted", o.Result)
	}
	want := []string{
		"ReleaseCapture",
		"OleInitialize",
		"CreateWindow",
		"AttachThreadInput TRUE",
		"DoDragDrop",
		"AttachThreadInput FALSE",
		"DestroyWindow",
		"OleUninitialize",
	}
	if got := f.list(); !sameOrder(got, want) {
		t.Fatalf("the drag thread did\n%v\nwant\n%v", got, want)
	}
	// ReleaseCapture is the caller's thread's, by contract: it is the one
	// call that must NOT be on the drag thread.
	f.mu.Lock()
	capTID := f.events[0].tid
	f.mu.Unlock()
	if capTID != windows.GetCurrentThreadId() {
		t.Errorf("ReleaseCapture ran on thread %d, not the caller's %d", capTID, windows.GetCurrentThreadId())
	}
	f.mu.Lock()
	f.events = f.events[1:]
	f.mu.Unlock()
	f.oneThread(t, "the drag")

	if got := ph.list(); len(got) != 3 || got[2].Step != Done || got[2].Reason != Copied {
		t.Fatalf("phases %v, want Preparing, Awaiting, Done/Copied", got)
	}
	if running.Load() {
		t.Error("the drag is still marked running after its thread ended")
	}
	// The class is logged and decides nothing: the folder is left for the
	// scavenge like any other target's.
	if !strings.Contains(lg.text(), "CabinetWClass") {
		t.Errorf("the log does not name the class the button came up over:\n%s", lg.text())
	}
	if m, ok := ReadManifest(o.Result.Folder); !ok || m.State != StateHandedOut {
		t.Errorf("the folder is manifested %q %v, want %q", m.State, ok, StateHandedOut)
	}
}

// sameOrder is the event list compared as a sequence, so that a missing
// call, an extra one or a swapped pair all read the same way in the failure.
func sameOrder(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestTwoDragsInARow: the thread is per drag, and the second one takes its
// own apartment, its own window and its own attachment. A drag is refused
// while one runs, and allowed the moment the first thread has finished its
// teardown — the order the running flag is cleared in is what makes that
// true.
func TestTwoDragsInARow(t *testing.T) {
	items := []Item{{Name: "two.bin", Size: 4}}
	release := make(chan struct{})
	f := &fakeSystem{drag: func(f *fakeSystem, data, source uintptr, allowed uint32, effect *uint32) uintptr {
		<-release
		return dragDropSCancel
	}}
	d, _, _ := beginFake(t, f, items)
	ch, err := d.Start()
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// While it runs: a second Begin is ErrBusy and a second Start on the
	// same drag is refused outright.
	if _, err := Begin(Options{Root: t.TempDir(), Items: items, Extract: writeItems(items)}); err != ErrBusy {
		t.Fatalf("a second Begin while one runs -> %v, want ErrBusy", err)
	}
	if _, err := d.Start(); err == nil {
		t.Fatal("the same drag was started twice")
	}
	close(release)
	if o := waitFor(t, ch); o.Err != nil {
		t.Fatalf("the first drag answered %v", o.Err)
	}

	d2, ph2, _ := beginFake(t, f, items)
	f.mu.Lock()
	f.events = nil
	f.mu.Unlock()
	f.drag = func(f *fakeSystem, data, source uintptr, allowed uint32, effect *uint32) uintptr {
		return dragDropSCancel
	}
	ch2, err := d2.Start()
	if err != nil {
		t.Fatalf("the second Start: %v", err)
	}
	if o := waitFor(t, ch2); o.Err != nil {
		t.Fatalf("the second drag answered %v", o.Err)
	}
	for _, what := range []string{"OleInitialize", "CreateWindow", "AttachThreadInput TRUE", "AttachThreadInput FALSE", "DestroyWindow", "OleUninitialize"} {
		if !f.has(what) {
			t.Errorf("the second drag never did %s: %v", what, f.list())
		}
	}
	if got := ph2.list(); len(got) != 1 || got[0].Reason != Cancelled {
		t.Fatalf("the second drag's phases are %v, want Done/Cancelled", got)
	}
}

// TestAPanicOnTheDragThreadIsNotTheProcess: a goroutine's panic is nobody
// else's to recover, and the window, the vault and the key would go with
// it. So the thread recovers its own, answers a failed drag, and takes its
// apartment and its window down on the way out — the unwinding runs the
// same defers a return would.
func TestAPanicOnTheDragThreadIsNotTheProcess(t *testing.T) {
	items := []Item{{Name: "boom.bin", Size: 4}}
	f := &fakeSystem{drag: func(f *fakeSystem, data, source uintptr, allowed uint32, effect *uint32) uintptr {
		panic("the drag thread fell over")
	}}
	d, ph, lg := beginFake(t, f, items)
	live := comRegistrySize()
	ch, err := d.Start()
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	o := waitFor(t, ch)
	if o.Err == nil || !strings.Contains(o.Err.Error(), "panicked") {
		t.Fatalf("a panicking drag answered %v, want an error that says so", o.Err)
	}
	if got := comRegistrySize(); got != live {
		t.Errorf("%d live COM cell(s) after the panic, want %d: the objects were never given back", got, live)
	}
	if got := ph.list(); len(got) == 0 || got[len(got)-1].Step != Done || got[len(got)-1].Reason != Failed {
		t.Fatalf("phases %v, want Done/Failed last: the caller's strip would stand for ever", got)
	}
	for _, what := range []string{"AttachThreadInput FALSE", "DestroyWindow", "OleUninitialize"} {
		if !f.has(what) {
			t.Errorf("the panic skipped %s: %v", what, f.list())
		}
	}
	if _, err := os.Stat(o.Result.Folder); !os.IsNotExist(err) {
		t.Errorf("a drag that fell over left its staging folder: %v", err)
	}
	if running.Load() {
		t.Error("the drag is still marked running after the panic")
	}
	if !strings.Contains(lg.text(), "panicked") {
		t.Errorf("the log says nothing about the panic:\n%s", lg.text())
	}
	// And another drag can run afterwards, which is the whole point.
	if _, err := Begin(Options{Root: t.TempDir(), Items: items, Extract: writeItems(items)}); err != nil {
		t.Fatalf("a drag after the panic -> %v", err)
	}
	running.Store(false)
}

// TestTheApartmentIsLeftOpenUnderAHeldObject is the prototype's finding
// turned into behaviour: OleUninitialize "releases any class factories,
// other COM objects, or servers held by the apartment", and it was watched
// taking a data object from five references to zero under a target that was
// still using it. So a target that still holds one keeps it.
func TestTheApartmentIsLeftOpenUnderAHeldObject(t *testing.T) {
	items := []Item{{Name: "held.bin", Size: 4}}
	var held *comObject
	f := &fakeSystem{drag: func(f *fakeSystem, data, source uintptr, allowed uint32, effect *uint32) uintptr {
		// A target keeping a reference past the drop, as Explorer's did.
		o, _, ok := comLookup(data)
		if !ok {
			t.Error("the data object the drag passed to DoDragDrop is not one of ours")
			return dragDropSCancel
		}
		held = o
		o.addRef()
		return dragDropSCancel
	}}
	d, _, lg := beginFake(t, f, items)
	ch, err := d.Start()
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, ch)
	if f.has("OleUninitialize") {
		t.Error("the apartment was closed over an object the target still holds")
	}
	if !f.has("DestroyWindow") {
		t.Error("the window was left behind as well")
	}
	if !strings.Contains(lg.text(), "skipping OleUninitialize") {
		t.Errorf("the log does not say why the apartment was left open:\n%s", lg.text())
	}
	// Shutdown is what the held object meets in the end: revoked, never
	// destroyed under its holder.
	Shutdown()
	if !held.impl.(*dataObject).revoked.Load() {
		t.Error("the held data object was not revoked at shutdown")
	}
	held.release()
}

// TestADragThatCouldNotStart: the two ways the thread gives up before
// DoDragDrop, each leaving the caller with an error, a Done/failed phase
// and no staging folder — and each balancing exactly what it took.
func TestADragThatCouldNotStart(t *testing.T) {
	items := []Item{{Name: "no.bin", Size: 4}}
	t.Run("the apartment is refused", func(t *testing.T) {
		f := &fakeSystem{oleHR: rpcEChangedMode}
		d, ph, _ := beginFake(t, f, items)
		ch, err := d.Start()
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		o := waitFor(t, ch)
		if o.Err == nil || !strings.Contains(o.Err.Error(), "OleInitialize") {
			t.Fatalf("answered %v, want an OleInitialize error", o.Err)
		}
		if f.has("DoDragDrop") || f.has("CreateWindow") {
			t.Errorf("the drag went on without an apartment: %v", f.list())
		}
		if f.has("OleUninitialize") {
			t.Error("a failed OleInitialize was balanced, which is the one thing it must not be")
		}
		if got := ph.list(); len(got) != 1 || got[0].Reason != Failed {
			t.Fatalf("phases %v, want Done/Failed", got)
		}
		if _, err := os.Stat(o.Result.Folder); !os.IsNotExist(err) {
			t.Errorf("the staging folder outlived a drag that never ran: %v", err)
		}
	})
	t.Run("the message-only window is refused", func(t *testing.T) {
		f := &fakeSystem{windowErr: os.ErrPermission}
		d, ph, _ := beginFake(t, f, items)
		ch, err := d.Start()
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		o := waitFor(t, ch)
		if o.Err == nil || !strings.Contains(o.Err.Error(), "message-only window") {
			t.Fatalf("answered %v, want the window's error", o.Err)
		}
		if f.has("DoDragDrop") {
			t.Error("the drag ran on a thread with no message queue")
		}
		if !f.has("OleUninitialize") {
			t.Error("the apartment it took was not balanced")
		}
		if got := ph.list(); len(got) != 1 || got[0].Reason != Failed {
			t.Fatalf("phases %v, want Done/Failed", got)
		}
	})
}

// TestADragWithNoInputToAttachTo: a caller with no window — a test, a
// gesture from nowhere — is a drag that runs with its own input state
// rather than no drag at all, and the log says so.
func TestADragWithNoInputToAttachTo(t *testing.T) {
	items := []Item{{Name: "alone.bin", Size: 4}}
	f := &fakeSystem{}
	d, _, lg := beginFake(t, f, items)
	windowThreadID = func(hwnd uintptr) uint32 { return 0 }
	ch, err := d.Start()
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if o := waitFor(t, ch); o.Err != nil {
		t.Fatalf("answered %v", o.Err)
	}
	if f.has("AttachThreadInput TRUE") {
		t.Error("the drag attached its input to a thread it could not name")
	}
	if !f.has("DoDragDrop") {
		t.Error("the drag did not run at all")
	}
	if !strings.Contains(lg.text(), "no input attachment") {
		t.Errorf("the log does not say the attachment was skipped:\n%s", lg.text())
	}
}

// TestAnAttachmentThatWasRefused: AttachThreadInput can fail — a thread
// without a message queue, a journal hook, another desktop — and a drag
// that ran with its own input state is a finding in the log, not a drag
// abandoned. Nothing is detached afterwards, there being nothing attached.
func TestAnAttachmentThatWasRefused(t *testing.T) {
	items := []Item{{Name: "refused.bin", Size: 4}}
	f := &fakeSystem{attachNo: true}
	d, _, lg := beginFake(t, f, items)
	ch, err := d.Start()
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if o := waitFor(t, ch); o.Err != nil {
		t.Fatalf("answered %v", o.Err)
	}
	if !f.has("DoDragDrop") {
		t.Error("a refused attachment stopped the drag")
	}
	if f.has("AttachThreadInput FALSE") {
		t.Error("the drag detached an input state it never attached")
	}
	if !f.has("OleUninitialize") || !f.has("DestroyWindow") {
		t.Errorf("the teardown was not run: %v", f.list())
	}
	if !strings.Contains(lg.text(), "FAILED") {
		t.Errorf("the log does not say the attachment was refused:\n%s", lg.text())
	}
}

// TestRunIsStartAndTheWait: the shape the core's operation is written
// against, and the one Shell.DragOut calls from the goroutine of a bound
// call.
func TestRunIsStartAndTheWait(t *testing.T) {
	items := []Item{{Name: "run.bin", Size: 4}}
	f := &fakeSystem{drag: func(f *fakeSystem, data, source uintptr, allowed uint32, effect *uint32) uintptr {
		*effect = dropEffectNone
		return dragDropSCancel
	}}
	d, ph, _ := beginFake(t, f, items)
	res, err := d.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Extracted || res.SelfDrop {
		t.Fatalf("a cancelled drag answered %+v", res)
	}
	if got := ph.list(); len(got) != 1 || got[0].Reason != Cancelled {
		t.Fatalf("phases %v, want Done/Cancelled", got)
	}
	if running.Load() {
		t.Error("Run returned before the thread had finished its teardown")
	}
}

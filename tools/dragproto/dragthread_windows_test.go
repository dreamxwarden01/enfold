//go:build windows

package main

import (
	"strings"
	"testing"
	"time"
)

// What can and cannot be tested here.
//
// The message-only window CANNOT be unit-tested. createMessageOnlyWindow needs a
// window station, a desktop, a registered class and a thread that owns the
// result, and its whole purpose is the side effect on the calling thread -- a
// test that made one would be testing user32, not this program. The same goes
// for AttachThreadInput, EnableWindow and the WM_TIMER heartbeat: each is one
// call whose effect is on the window system. The build and vet gates cover the
// shape of those calls; the log of a real drag covers the rest, which is what
// experiments 18-21 are for.
//
// What is testable is every decision taken around them: the release-position
// verdict, the -poll-after verdict, the flag combinations, and the fact that a
// run with none of the three flags does exactly what it did before they existed.

// withFlags gives one test the experiment flags and the window handle, and puts
// them back afterwards.
func withFlags(t *testing.T, thread, disable bool) {
	t.Helper()
	prevThread, prevDisable := threadMode, disableMode
	prevHWND := mainHWND.Load()
	prevHeld := windowHeld.Load()
	prevRunning := dragRunning.Load()
	threadMode, disableMode = thread, disable
	// No window: every Win32 call in these paths checks for it first, so the
	// decisions can be driven without one.
	mainHWND.Store(0)
	windowHeld.Store(false)
	// A drag is in progress, which is the state -disable's hold is only allowed
	// to be taken in.
	dragRunning.Store(true)
	t.Cleanup(func() {
		threadMode, disableMode = prevThread, prevDisable
		mainHWND.Store(prevHWND)
		windowHeld.Store(prevHeld)
		dragRunning.Store(prevRunning)
	})
}

func TestPointInRect(t *testing.T) {
	// PtInRect's rule: "The rectangle is defined to include the left and top
	// sides, and to exclude the right and bottom sides." A window's frame from
	// GetWindowRect is exactly that, so an off-by-one here would call a release
	// one pixel outside the window a self-drop.
	r := rect{left: 10, top: 20, right: 110, bottom: 220}
	cases := []struct {
		p    point
		want bool
	}{
		{point{10, 20}, true},    // the included corner
		{point{109, 219}, true},  // the last included pixel
		{point{110, 219}, false}, // the excluded right edge
		{point{109, 220}, false}, // the excluded bottom edge
		{point{9, 20}, false},
		{point{10, 19}, false},
		{point{60, 120}, true},
	}
	for _, c := range cases {
		if got := pointInRect(c.p, r); got != c.want {
			t.Errorf("pointInRect(%v, %v) = %v, want %v", c.p, r, got, c.want)
		}
	}
}

// TestPackPoint pins the one thing that goes wrong silently: a negative
// coordinate, which is what a second monitor to the left of or above the primary
// one produces. Sign extension over the other half would send WindowFromPoint
// somewhere else entirely.
func TestPackPoint(t *testing.T) {
	cases := []struct {
		p    point
		want uintptr
	}{
		{point{0, 0}, 0},
		{point{1, 2}, 0x0000000200000001},
		{point{-1, 0}, 0x00000000FFFFFFFF},
		{point{0, -1}, 0xFFFFFFFF00000000},
		{point{-1920, -100}, 0xFFFFFF9CFFFFF880},
	}
	for _, c := range cases {
		if got := packPoint(c.p); got != c.want {
			t.Errorf("packPoint(%v) = 0x%016X, want 0x%016X", c.p, got, c.want)
		}
	}
}

// TestSelfDropVerdict is the release-position decision, and the row that matters
// most is the -disable one: WindowFromPoint "does not retrieve a handle to a
// hidden or disabled window", so a disabled main window is invisible to the hit
// test and only the frame can say the release was over it.
func TestSelfDropVerdict(t *testing.T) {
	const main = 0x1234
	const other = 0x9999
	frame := rect{left: 100, top: 100, right: 300, bottom: 300}
	inside := point{200, 200}
	outside := point{900, 900}

	cases := []struct {
		name      string
		p         point
		frame     rect
		haveFrame bool
		under     uintptr
		wantSelf  bool
		wantSays  string
	}{
		{"both agree it is ours", inside, frame, true, main, true, "by both the hit test and its frame"},
		{"disabled: the frame alone", inside, frame, true, other, true, "disabled or covered window looks like"},
		{"disabled with no window under the cursor", inside, frame, true, 0, true, "no window at all"},
		{"the hit test alone", outside, frame, true, main, true, "by the hit test"},
		{"somebody else's window", outside, frame, true, other, false, "outside our own window"},
		{"no frame to read", outside, rect{}, false, other, false, "could not be read"},
		{"no frame and no hit", outside, rect{}, false, 0, false, "no window at all"},
	}
	for _, c := range cases {
		self, why := selfDropVerdict(c.p, c.frame, c.haveFrame, c.under, main)
		if self != c.wantSelf {
			t.Errorf("%s: self = %v, want %v (%s)", c.name, self, c.wantSelf, why)
		}
		if !strings.Contains(why, c.wantSays) {
			t.Errorf("%s: %q does not say %q", c.name, why, c.wantSays)
		}
	}

	// A main window we do not have cannot be dropped on by accident.
	if self, _ := selfDropVerdict(inside, frame, true, 0, 0); self {
		t.Error("a release was called a self-drop with no main window to compare against")
	}
}

func TestKeyStateWords(t *testing.T) {
	cases := []struct {
		keys uint32
		want string
	}{
		{mkLButton, "MK_LBUTTON (0x0001)"},
		{mkLButton | mkControl, "MK_LBUTTON|MK_CONTROL (0x0009)"},
		{mkShift | mkControl, "MK_SHIFT|MK_CONTROL (0x000C)"},
		{0, "no keys or buttons (0x0000)"},
		// MK_ALT is not one of winuser.h's MK_ flags and is deliberately not
		// decoded; the raw value still carries it.
		{0x0080, "no keys or buttons (0x0080)"},
	}
	for _, c := range cases {
		if got := keyStateWords(c.keys); got != c.want {
			t.Errorf("keyStateWords(0x%04X) = %q, want %q", c.keys, got, c.want)
		}
	}
}

func TestEnabledWord(t *testing.T) {
	// EnableWindow: "If the window was previously disabled, the return value is
	// nonzero" -- so a zero return means it was enabled, which is how the call
	// sites read it.
	if got := enabledWord(true); got != "enabled" {
		t.Errorf("enabledWord(true) = %q", got)
	}
	if got := enabledWord(false); got != "disabled" {
		t.Errorf("enabledWord(false) = %q", got)
	}
}

// TestDisableIsInertWithoutTheFlag: without -disable nothing must touch the
// window's enabled state, whatever the drag does.
func TestDisableIsInertWithoutTheFlag(t *testing.T) {
	withFlags(t, false, false)
	read := captureLog(t)

	holdWindowAfterHandover("the extraction is done")
	releaseWindowHold("the drag is over")

	if windowHeld.Load() {
		t.Error("the window was marked held without -disable")
	}
	if s := read(); strings.Contains(s, "-disable") {
		t.Errorf("something logged a -disable line without the flag: %q", s)
	}
}

// TestDisableHoldsOnceAndReleasesOnce is the state machine -disable is: one hold
// per drag whatever the target asks for, and a release on every completion path
// that is harmless when nothing was held.
func TestDisableHoldsOnceAndReleasesOnce(t *testing.T) {
	withFlags(t, false, true)

	// A release before any hold -- an abandoned drag -- must do nothing.
	releaseWindowHold("the drag was abandoned")
	if windowHeld.Load() {
		t.Error("a release with nothing held left the window marked held")
	}

	holdWindowAfterHandover("the paths were handed back")
	if !windowHeld.Load() {
		t.Fatal("the window was not marked held after the hand-over")
	}
	// A second request -- Explorer asking for CF_HDROP again -- must not stack.
	holdWindowAfterHandover("the paths were handed back again")
	if !windowHeld.Load() {
		t.Fatal("a second hand-over cleared the hold")
	}

	releaseWindowHold("DoDragDrop returned")
	if windowHeld.Load() {
		t.Error("the window was still marked held after the release")
	}
	// And the deferred release on the way out of the drag must be a no-op.
	releaseWindowHold("the drag is over")
	if windowHeld.Load() {
		t.Error("a second release marked the window held again")
	}

	// A GetData that arrives after the drag is over -- a target that kept the
	// data object, which the first real 5 GiB drop did -- must not disable a
	// window that nothing is going to enable again.
	dragRunning.Store(false)
	holdWindowAfterHandover("a late GetData")
	if windowHeld.Load() {
		t.Error("the window was held by a request that arrived after the drag had ended")
	}
}

// TestThreadFlagCombinations: each of the three is independent, and the ones
// that need a thread or a window of their own must do nothing at all when they
// are not in force. This is what keeps a run of the earlier experiments -- which
// pass none of these flags -- identical to what it was.
func TestThreadFlagCombinations(t *testing.T) {
	for _, c := range []struct {
		name             string
		thread, disable  bool
		wantAttachLogged bool
	}{
		{"nothing", false, false, false},
		{"-disable alone", false, true, false},
		{"-thread alone", true, false, true},
		{"-thread -disable", true, true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			withFlags(t, c.thread, c.disable)
			read := captureLog(t)

			// dragThreadID is zero in a test, so attachDragInput has no window
			// thread to attach to and must decline rather than call user32 with
			// a thread id of zero.
			detach := attachDragInput()
			detach()
			if s := read(); strings.Contains(s, "AttachThreadInput") {
				t.Errorf("attachDragInput called into user32 with no window thread: %q", s)
			}

			// The heartbeat and the queue drain both need a window; with none
			// they must be silent.
			startAliveTimer(0)
			stopAliveTimer(0)
			drainOleQueue()
			if s := read(); strings.Contains(s, "WM_TIMER") || strings.Contains(s, "draining") {
				t.Errorf("the heartbeat or the drain ran without a window: %q", s)
			}
		})
	}
}

// TestAliveTickIsNotThrottled: the heartbeat's whole job is that a missing line
// is visible, so every 250 ms tick must produce one. Throttling it would hide
// exactly the failure -thread is being tested for.
func TestAliveTickIsNotThrottled(t *testing.T) {
	prev := aliveTicks.Load()
	t.Cleanup(func() { aliveTicks.Store(prev) })
	aliveTicks.Store(0)
	read := captureLog(t)

	for i := 0; i < 8; i++ {
		aliveTick()
	}
	if lines := strings.Count(read(), "the window thread is alive"); lines != 8 {
		t.Errorf("%d heartbeat lines for 8 ticks, want one per tick", lines)
	}
	if got := aliveTicks.Load(); got != 8 {
		t.Errorf("%d ticks counted, want 8", got)
	}
}

// TestPollAfterVerdict is the cross-volume question, and the verdict is what
// decides whether the real application may delete its staging folder the moment
// DoDragDrop returns.
func TestPollAfterVerdict(t *testing.T) {
	const window = 20 * time.Second
	cases := []struct {
		name  string
		facts pollAfterFacts
		want  string
	}{
		{
			// The cross-volume copy: Explorer is still reading long after the
			// return. This is the answer that forbids deleting at the return.
			name: "still being read after the return",
			facts: pollAfterFacts{
				window: window, samples: 80, busy: 60,
				lastBusy: 18 * time.Second, present: 80,
				files: []string{"big.bin"}, folder: true,
			},
			want: "could NOT have been deleted at the return",
		},
		{
			// The same-volume move: the file is renamed away before the first
			// sample and nothing ever holds it.
			name:  "renamed away at once",
			facts: pollAfterFacts{window: window, samples: 80, lastBusy: -1},
			want:  "could have been deleted at the return",
		},
		{
			name: "left behind, never touched",
			facts: pollAfterFacts{
				window: window, samples: 80, lastBusy: -1, present: 80, folder: true,
			},
			want: "nothing came back for them",
		},
		{
			name:  "no sample taken",
			facts: pollAfterFacts{window: window, lastBusy: -1},
			want:  "no sample was taken",
		},
	}
	for _, c := range cases {
		got := pollAfterVerdict(c.facts)
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: %q does not say %q", c.name, got, c.want)
		}
	}

	// The busy verdict has to name the file and the moment, because those are
	// what a reader compares between a same-volume and a cross-volume drop.
	busy := pollAfterVerdict(pollAfterFacts{
		window: window, samples: 80, busy: 60, lastBusy: 18 * time.Second,
		present: 80, files: []string{"big.bin"}, folder: true,
	})
	for _, want := range []string{"big.bin", "18s", "60 of 80"} {
		if !strings.Contains(busy, want) {
			t.Errorf("the busy verdict %q does not say %q", busy, want)
		}
	}
}

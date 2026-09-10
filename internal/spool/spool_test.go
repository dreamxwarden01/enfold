package spool

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// The spooler watch (APP.md §3 Shell, §6). Nothing here touches the real
// spooler and nothing submits a print: the gate below is what the diffing and
// the polling are proved against.

func set(jobs ...Job) Set {
	s := Set{}
	for _, j := range jobs {
		s[j] = struct{}{}
	}
	return s
}

// A job id is the printer's, so the same number on two printers is two jobs;
// a job that left the queue is not a new one.
func TestNewJobSince(t *testing.T) {
	a := Job{Printer: "Microsoft Print to PDF", ID: 3}
	b := Job{Printer: "Microsoft Print to PDF", ID: 4}
	other := Job{Printer: "Office", ID: 3}
	for _, tc := range []struct {
		name  string
		now   Set
		begun Set
		want  bool
	}{
		{"nothing at all", set(), set(), false},
		{"the same queue", set(a), set(a), false},
		{"a new id", set(a, b), set(a), true},
		{"the first job of an empty queue", set(a), set(), true},
		{"the same id on another printer", set(a, other), set(a), true},
		{"a job that finished and left", set(), set(a), false},
		{"one left and none arrived", set(a), set(a, b), false},
	} {
		if got := tc.now.NewJobSince(tc.begun); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

// reply is one answer to one reading.
type reply struct {
	set Set
	err error
}

// gate is a Snapshotter the test steps: every reading waits for the test to
// take the call and then to answer it, so the poller, the caller inside End
// and the clock interleave only where the test says. A call the test takes
// and never answers is a spooler that has stalled — which is the one thing
// neither Begin nor End may wait on for ever.
type gate struct {
	req    chan chan reply
	closed chan struct{}
	once   sync.Once
}

func newGate(t *testing.T) *gate {
	g := &gate{req: make(chan chan reply), closed: make(chan struct{})}
	// Whatever is still inside a reading when the test ends is let go, so no
	// goroutine outlives it.
	t.Cleanup(func() { g.once.Do(func() { close(g.closed) }) })
	return g
}

func (g *gate) Snapshot() (Set, error) {
	ch := make(chan reply, 1)
	select {
	case g.req <- ch:
	case <-g.closed:
		return Set{}, nil
	}
	select {
	case r := <-ch:
		return r.set, r.err
	case <-g.closed:
		return Set{}, nil
	}
}

// call takes the next reading asked for and leaves it unanswered, failing the
// test if none is asked for: a watch that does not poll never gets here. A
// reading taken after another has been answered proves that the earlier one
// was recorded — the poller records before it asks again.
func (g *gate) call(t *testing.T) chan reply {
	t.Helper()
	select {
	case ch := <-g.req:
		return ch
	case <-time.After(5 * time.Second):
		t.Fatal("the watch asked for no reading")
		return nil
	}
}

// answer takes the next reading and answers it.
func (g *gate) answer(t *testing.T, s Set, err error) {
	t.Helper()
	g.call(t) <- reply{s, err}
}

// tick is the watch's clock: it moves only when the watch sleeps, so the
// windows below are counted rather than waited out. Both the poller and the
// caller inside a wait sleep on it, so it is guarded — and every sleep gives
// up a real millisecond as well, so that a goroutine the watch is waiting on
// is scheduled rather than outrun by a clock that costs nothing to advance.
type tick struct {
	mu  sync.Mutex
	now time.Time
}

func (c *tick) get() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }

func (c *tick) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
	time.Sleep(time.Millisecond)
}

func newWatch(g *gate) (*Watch, *tick) {
	c := &tick{now: time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)}
	w := New(g)
	w.Now, w.Sleep = c.get, c.advance
	return w, c
}

// begin runs Begin on its own goroutine and answers its reading with s: the
// reading is the test's to hand over, so Begin cannot be called inline.
func begin(t *testing.T, w *Watch, g *gate, s Set) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- w.Begin() }()
	g.answer(t, s, nil)
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Begin did not return")
		return nil
	}
}

// The finding of the outside audit of 2026-09-09: a job that spools and
// completes while the print dialog stands is in no queue that Begin or End
// would read on its own, and the dialog used to ask a user who had already
// printed. The poller runs from Begin, so the job is remembered.
func TestAJobThatCameAndWentIsCaught(t *testing.T) {
	job := Job{Printer: "Microsoft Print to PDF", ID: 7}
	g := newGate(t)
	w, _ := newWatch(g)
	if err := begin(t, w, g, set()); err != nil {
		t.Fatal(err)
	}
	// The dialog stands: the job appears, and by the time End is called it
	// has finished and left the queue again.
	g.answer(t, set(job), nil)
	g.answer(t, set(), nil)
	g.call(t) // taken after the two above: both were recorded
	submitted, err := w.End(context.Background())
	if err != nil || !submitted {
		t.Fatalf("a job that came and went: %v %v", submitted, err)
	}
}

// answering answers every reading with s until the returned stop is called:
// a spooler that is being read normally while a wait runs.
func answering(g *gate, s Set) (stop func()) {
	done := make(chan struct{})
	go func() {
		for {
			select {
			case ch := <-g.req:
				ch <- reply{s, nil}
			case <-done:
				return
			}
		}
	}()
	return func() { close(done) }
}

// A job standing before the print is not the print's: a cancelled print
// leaves the queue as it was, and the watch says so — false and no error —
// after the whole window. The spooler answers throughout: that is what
// separates this from a stalled one, which is never called a cancellation.
func TestEndTimesOutOnACancelledPrint(t *testing.T) {
	standing := set(Job{Printer: "Office", ID: 1})
	g := newGate(t)
	w, clk := newWatch(g)
	if err := begin(t, w, g, standing); err != nil {
		t.Fatal(err)
	}
	stop := answering(g, standing) // the queue has not changed, and says so
	defer stop()
	start := clk.get()
	submitted, err := w.End(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if submitted {
		t.Fatal("a queue that did not change was called a submission")
	}
	if waited := clk.get().Sub(start); waited < PollWindow {
		t.Fatalf("it gave up after %v, not %v", waited, PollWindow)
	}
	// One Begin, one End: the watch is spent, and a second End answers from
	// nothing rather than from the last print's jobs.
	if _, err := w.End(context.Background()); err == nil {
		t.Fatal("a second End without a fresh Begin answered")
	}
}

// A spooler that stalls is abandoned at the deadline, and the answer is
// ErrStalled — not "nothing was submitted" (the outside audit of 2026-09-09:
// a window with no reading in it holds nothing that tells a cancelled print
// from an unreadable spooler, and the dialog would tell the user their key
// was not printed while the sheet stood in the tray). What End must never do
// either is hold the dialog for as long as winspool takes.
func TestEndAnswersAtTheDeadlineWhenAReadingStalls(t *testing.T) {
	g := newGate(t)
	w, clk := newWatch(g)
	if err := begin(t, w, g, set()); err != nil {
		t.Fatal(err)
	}
	g.call(t) // the poller is inside a reading that will never answer
	start := clk.get()
	done := make(chan bool, 1)
	go func() {
		submitted, err := w.End(context.Background())
		if !errors.Is(err, ErrStalled) {
			t.Errorf("End over a stalled reading: %v", err)
		}
		done <- submitted
	}()
	select {
	case submitted := <-done:
		if submitted {
			t.Fatal("a stalled spooler was called a submission")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("End waited for the stalled reading")
	}
	if waited := clk.get().Sub(start); waited != PollWindow {
		t.Fatalf("it gave up after %v, not %v", waited, PollWindow)
	}
}

// A watch nobody ends is spent after maxWatch. PrintEnd is skipped whenever
// the page throws inside window.print() or the dialog goes down mid-print,
// and the poller behind it used to read every printer on the machine four
// times a second for the life of the process (the outside audit of
// 2026-09-09).
func TestAWatchNobodyEndsStopsReading(t *testing.T) {
	g := newGate(t)
	w, clk := newWatch(g)
	if err := begin(t, w, g, set()); err != nil {
		t.Fatal(err)
	}
	start := clk.get()
	// Every reading is answered, so nothing but maxWatch can stop the poller;
	// the clock moves only when it sleeps, so the five minutes are counted
	// rather than waited out.
	spent := false
	for i := 0; i < int(maxWatch/PollInterval)+8 && !spent; i++ {
		select {
		case ch := <-g.req:
			ch <- reply{set(), nil}
		case <-time.After(2 * time.Second):
			spent = true
		}
	}
	if !spent {
		t.Fatal("the poller was still reading the spooler after maxWatch")
	}
	if ran := clk.get().Sub(start); ran < maxWatch {
		t.Fatalf("the poller stopped after %v, before %v", ran, maxWatch)
	}
	// The watch is spent, not merely quiet: an End that arrives now is
	// refused, so the page asks rather than answering from a stale snapshot.
	if _, err := w.End(context.Background()); err == nil {
		t.Fatal("an End after the watch was spent answered")
	}
}

// Begin is held by nothing either: a spooler that has not answered within the
// window is an error there, so the page falls back to its second confirmation
// before it prints rather than after.
func TestBeginAnswersAtTheDeadlineWhenItsReadingStalls(t *testing.T) {
	g := newGate(t)
	w, clk := newWatch(g)
	start := clk.get()
	done := make(chan error, 1)
	go func() { done <- w.Begin() }()
	g.call(t) // taken, never answered
	select {
	case err := <-done:
		if !errors.Is(err, ErrStalled) {
			t.Fatalf("Begin over a stalled reading: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Begin waited for the stalled reading")
	}
	if waited := clk.get().Sub(start); waited != PollWindow {
		t.Fatalf("Begin gave up after %v, not %v", waited, PollWindow)
	}
	if _, err := w.End(context.Background()); err == nil {
		t.Fatal("End after a Begin that failed answered")
	}
}

// A job whose id was reused is a new job only if the id is one Begin did not
// hold: an id that stood there is not a print of ours.
func TestEndIgnoresTheJobsThatStood(t *testing.T) {
	standing := Job{Printer: "Office", ID: 1}
	g := newGate(t)
	w, _ := newWatch(g)
	if err := begin(t, w, g, set(standing)); err != nil {
		t.Fatal(err)
	}
	g.answer(t, set(standing), nil)
	g.answer(t, set(standing, Job{Printer: "Office", ID: 2}), nil)
	g.call(t)
	submitted, err := w.End(context.Background())
	if err != nil || !submitted {
		t.Fatalf("the second job: %v %v", submitted, err)
	}
}

// A spooler that cannot be read is an error at both ends — the page then
// falls back to its second confirmation — and End without a Begin that
// succeeded is refused rather than answering from nothing.
func TestUnreadableSpoolerIsAnError(t *testing.T) {
	boom := errors.New("the spooler is not answering")
	g := newGate(t)
	w, _ := newWatch(g)
	done := make(chan error, 1)
	go func() { done <- w.Begin() }()
	g.answer(t, nil, boom)
	if err := <-done; !errors.Is(err, boom) {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := w.End(context.Background()); err == nil {
		t.Fatal("End after a Begin that failed answered")
	}
	// Begun cleanly, unreadable afterwards: still an error, never "nothing
	// was printed".
	g2 := newGate(t)
	w2, _ := newWatch(g2)
	if err := begin(t, w2, g2, set()); err != nil {
		t.Fatal(err)
	}
	g2.answer(t, nil, boom)
	g2.call(t)
	if _, err := w2.End(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("End: %v", err)
	}
}

// A cancelled context ends the wait rather than holding the page for the
// whole window.
func TestEndStopsWithItsContext(t *testing.T) {
	g := newGate(t)
	w, _ := newWatch(g)
	if err := begin(t, w, g, set()); err != nil {
		t.Fatal(err)
	}
	g.call(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := w.End(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("End under a cancelled context: %v", err)
	}
}

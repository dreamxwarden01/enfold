package spool

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The spooler watch (APP.md §3 Shell, §6). Nothing here touches the real
// spooler and nothing submits a print: the fake below is what the diffing
// and the polling are proved against.

// fake is a Snapshotter a test drives: each call answers the next queue, and
// the last one stands once the list runs out.
type fake struct {
	queues []Set
	err    error
	calls  int
}

func (f *fake) Snapshot() (Set, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if len(f.queues) == 0 {
		return Set{}, nil
	}
	i := f.calls - 1
	if i >= len(f.queues) {
		i = len(f.queues) - 1
	}
	return f.queues[i], nil
}

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

// newWatch is a watch over the fake whose clock only moves when it sleeps,
// so the poll window is counted rather than waited out.
func newWatch(f *fake) (*Watch, *time.Time) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	w := New(f)
	w.Now = func() time.Time { return now }
	w.Sleep = func(d time.Duration) { now = now.Add(d) }
	return w, &now
}

// A job that appears while the watch polls is a submission, answered the
// moment it shows: the page needs no second question.
func TestEndSeesAJobThatAppearsWhilePolling(t *testing.T) {
	job := Job{Printer: "Microsoft Print to PDF", ID: 7}
	f := &fake{queues: []Set{set(), set(), set(), set(job)}}
	w, now := newWatch(f)
	if err := w.Begin(); err != nil {
		t.Fatal(err)
	}
	start := *now
	submitted, err := w.End(context.Background())
	if err != nil || !submitted {
		t.Fatalf("a job that appeared: %v %v", submitted, err)
	}
	// It answered the moment the job showed, not at the end of the window.
	if waited := now.Sub(start); waited != 2*PollInterval {
		t.Fatalf("it waited %v", waited)
	}
	if f.calls != 4 { // one for Begin, three for the polls
		t.Fatalf("snapshots: %d", f.calls)
	}
}

// A job standing before the print is not the print's: a cancelled print
// leaves the queue as it was, and the watch says so after the whole window.
func TestEndTimesOutOnACancelledPrint(t *testing.T) {
	standing := set(Job{Printer: "Office", ID: 1})
	f := &fake{queues: []Set{standing}}
	w, now := newWatch(f)
	if err := w.Begin(); err != nil {
		t.Fatal(err)
	}
	start := *now
	submitted, err := w.End(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if submitted {
		t.Fatal("a queue that did not change was called a submission")
	}
	if waited := now.Sub(start); waited != PollWindow {
		t.Fatalf("it gave up after %v, not %v", waited, PollWindow)
	}
	if want := int(PollWindow/PollInterval) + 2; f.calls != want { // Begin, then one poll per interval and the last
		t.Fatalf("snapshots: %d, want %d", f.calls, want)
	}
}

// A job whose id was reused is still a new job to the watch only if the id
// is one Begin did not hold: an id that stood there is not a print of ours.
func TestEndIgnoresTheJobsThatStood(t *testing.T) {
	standing := Job{Printer: "Office", ID: 1}
	f := &fake{queues: []Set{set(standing), set(standing), set(standing, Job{Printer: "Office", ID: 2})}}
	w, _ := newWatch(f)
	if err := w.Begin(); err != nil {
		t.Fatal(err)
	}
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
	f := &fake{err: boom}
	w, _ := newWatch(f)
	if err := w.Begin(); !errors.Is(err, boom) {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := w.End(context.Background()); err == nil {
		t.Fatal("End after a Begin that failed answered")
	}
	// Begun cleanly, unreadable afterwards: still an error, never "nothing
	// was printed".
	f2 := &fake{queues: []Set{set()}}
	w2, _ := newWatch(f2)
	if err := w2.Begin(); err != nil {
		t.Fatal(err)
	}
	f2.err = boom
	if _, err := w2.End(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("End: %v", err)
	}
}

// A cancelled context ends the poll rather than holding the page for the
// whole window.
func TestEndStopsWithItsContext(t *testing.T) {
	f := &fake{queues: []Set{set()}}
	w, _ := newWatch(f)
	if err := w.Begin(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := w.End(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("End under a cancelled context: %v", err)
	}
}

// Package spool watches the Windows print spooler across a print, so that
// the recovery key's dialog can tell a print that was submitted from one the
// user cancelled (APP.md §3 Shell, §6; DECISIONS 2026-09-09 — BitLocker
// knows the difference and Enfold's second confirmation existed only because
// it did not).
//
// The shape is deliberately small: a Snapshot of every job the machine's
// printers hold, and a Watch that takes one before window.print() and then
// keeps reading until End answers after afterprint. A job that later fails
// still counts — it was submitted — and a spooler that cannot be read at all,
// one that never answers within the window included, is an error, on which
// the page falls back to asking.
//
// Nothing here ever creates a job.
package spool

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrUnsupported is what Snapshot answers where there is no print spooler to
// read (every platform but Windows).
var ErrUnsupported = errors.New("spool: reading the print spooler needs Windows")

// ErrStalled is what a reading that never answered comes back as: the call
// into the spooler was abandoned at its deadline rather than held onto. It is
// also End's answer when its whole window passes with no reading having come
// back at all — a spooler that stalls is one that cannot be read, and there
// is nothing there to tell it from a cancelled print. The page treats it like
// any other unreadable spooler and asks.
var ErrStalled = errors.New("spool: the print spooler did not answer in time")

// Job is one print job. Ids are handed out per printer, so the printer's
// name is part of the identity and two printers' job 3 are two jobs.
type Job struct {
	Printer string
	ID      uint32
}

func (j Job) String() string { return fmt.Sprintf("%s#%d", j.Printer, j.ID) }

// Set is the jobs the spooler held at one moment.
type Set map[Job]struct{}

// NewJobSince reports whether s holds a job other did not: the question a
// print asks, since a job that finished and left the queue between the two
// snapshots is not what is being looked for — a new id is.
func (s Set) NewJobSince(other Set) bool {
	for j := range s {
		if _, had := other[j]; !had {
			return true
		}
	}
	return false
}

// Snapshotter reads the machine's spooler. System is the real one; a test
// supplies its own, so the flow is exercised without a printer.
type Snapshotter interface {
	Snapshot() (Set, error)
}

// System reads this machine's print spooler.
type System struct{}

// Snapshot is every job of every local and connected printer.
func (System) Snapshot() (Set, error) { return snapshot() }

// The poll across a print: how long End waits for the spooler to show the
// job, how often the spooler is read, and how often a wait looks at what the
// readings have answered. Three seconds is what APP.md §6 allows before the
// answer is "nothing was submitted".
const (
	PollWindow   = 3 * time.Second
	PollInterval = 250 * time.Millisecond
	waitGrain    = 10 * time.Millisecond
)

// maxWatch bounds a watch nobody ends. A Begin whose End never arrives — the
// page throws inside window.print(), or the dialog is torn down mid-print —
// would otherwise leave a poller reading every printer on the machine four
// times a second for the life of the process. The watch is spent when it
// passes: the poller stops, and an End after that is refused like one without
// a Begin, so the page asks rather than answering from a snapshot minutes old.
const maxWatch = 5 * time.Minute

// Watch is one print's worth of watching: Begin before window.print(), End
// after afterprint.
//
// Begin does more than snapshot: it starts a poller that reads the spooler
// every PollInterval and remembers any job the Begin snapshot did not hold,
// until End has answered. A job that spools and completes while the print
// dialog stands is in no queue either call would have read on its own, and
// the dialog would then ask a user who had already printed — which is the
// second confirmation this package exists to remove (the outside audit of
// 2026-09-09).
//
// No reading holds a caller. Every Snapshot call runs on a goroutine of its
// own and is abandoned when it has not answered by the deadline — Begin's own
// budget of PollWindow, End's window, or End's context — and the answer comes
// from the readings that did come back, or from the deadline. An abandoned
// call leaks until winspool returns: there is no way to cancel one, and the
// alternative is a stalled spooler holding the dialog.
//
// A watch nobody ends is spent after maxWatch: the poller stops reading
// there, and an End that arrives afterwards is refused rather than answered
// from a snapshot minutes old.
//
// Now and Sleep are the clock, replaced by tests; both are the real ones
// when zero.
type Watch struct {
	src   Snapshotter
	Now   func() time.Time
	Sleep func(time.Duration)

	mu      sync.Mutex
	begun   Set
	started bool
	seen    bool  // a job Begin did not hold has been read
	err     error // what the last reading failed with, if it did
	// reads counts the readings that came back, failure included. End tells
	// a window in which the spooler answered and held nothing new from one in
	// which it never answered at all: the first is a cancelled print, the
	// second is a spooler that cannot be read (APP.md §3 Shell, §6).
	reads uint64
	// stop is the running poller's identity: closed and cleared when the
	// watch is over, which is how a reading that comes back late knows it
	// was abandoned.
	stop chan struct{}
}

// New builds a watch over a spooler.
func New(src Snapshotter) *Watch { return &Watch{src: src} }

func (w *Watch) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *Watch) sleep(d time.Duration) {
	if w.Sleep != nil {
		w.Sleep(d)
		return
	}
	time.Sleep(d)
}

// Begin snapshots the jobs standing before the print and starts the poller
// that watches for a new one from here on. An unreadable spooler — including
// one that does not answer within PollWindow — is an error here, so the page
// can fall back to its second confirmation before it prints rather than
// after.
func (w *Watch) Begin() error {
	w.halt() // a Begin without an End: the previous poller is abandoned
	s, err := w.read(context.Background(), w.now().Add(PollWindow))
	if err != nil {
		w.mu.Lock()
		w.started = false
		w.mu.Unlock()
		return err
	}
	w.mu.Lock()
	w.begun, w.started, w.seen, w.err, w.reads = s, true, false, nil, 0
	w.stop = make(chan struct{})
	stop := w.stop
	w.mu.Unlock()
	go w.poll(stop, w.now().Add(maxWatch))
	return nil
}

// End answers whether a job appeared that Begin did not see. The poller has
// been reading since Begin, so a job that came and went while the dialog
// stood is already remembered; End keeps waiting up to PollWindow for one
// and returns the moment a new job has been seen. A reading that failed is an
// error, and only then does the page ask the user. A print job that later
// fails still counts: it was submitted.
//
// "Nothing was submitted" is claimed only of a spooler that answered: a whole
// window in which no reading came back at all is ErrStalled, not a cancelled
// print. The dialog turns false into "cancelled", and that must never be said
// of a sheet of 48 digits that may be standing in a printer's tray.
func (w *Watch) End(ctx context.Context) (bool, error) {
	w.mu.Lock()
	started := w.started
	w.mu.Unlock()
	if !started {
		return false, errors.New("spool: End without a Begin that succeeded")
	}
	// One Begin, one End: the snapshot is spent here. A second End without a
	// fresh Begin would poll against the previous print's jobs and could
	// call a job that print submitted a new one — and the answer is what the
	// dialog claims about a key leaving the machine.
	defer func() {
		w.mu.Lock()
		w.started = false
		w.mu.Unlock()
		w.halt()
	}()
	deadline := w.now().Add(PollWindow)
	w.mu.Lock()
	entered := w.reads // the readings that had come back before this wait
	w.mu.Unlock()
	for {
		w.mu.Lock()
		seen, err, reads := w.seen, w.err, w.reads
		w.mu.Unlock()
		switch {
		case seen:
			return true, nil
		case err != nil:
			return false, err
		}
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if !w.now().Before(deadline) {
			if reads == entered {
				// The window passed with the spooler never answering: there
				// is nothing here that says the print was cancelled.
				return false, ErrStalled
			}
			return false, nil
		}
		w.sleep(waitGrain)
	}
}

// poll reads the spooler every PollInterval until the watch is over or until
// it is spent at until (Begin's now plus maxWatch). It is the goroutine every
// reading after Begin's runs on, so a reading that stalls stalls only this:
// End answers from what came back before it, or from its deadline, and this
// goroutine ends whenever winspool finally returns.
func (w *Watch) poll(stop chan struct{}, until time.Time) {
	for {
		w.sleep(PollInterval)
		if w.over(stop) {
			return
		}
		if !w.now().Before(until) {
			w.expire(stop)
			return
		}
		s, err := w.src.Snapshot()
		w.mu.Lock()
		if w.stop != stop {
			w.mu.Unlock() // abandoned: this reading is nobody's answer
			return
		}
		w.err, w.reads = err, w.reads+1
		if err == nil && s.NewJobSince(w.begun) {
			w.seen = true
		}
		w.mu.Unlock()
	}
}

// expire ends a watch nobody ended: the poller stops reading and the watch is
// spent, so an End that arrives after maxWatch is refused rather than
// answered from a snapshot minutes old.
func (w *Watch) expire(stop chan struct{}) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stop != stop {
		return // another Begin, or an End, got there first
	}
	close(w.stop)
	w.stop = nil
	w.started = false
}

// over reports whether the watch this poller belongs to has ended.
func (w *Watch) over(stop chan struct{}) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stop != stop
}

// halt ends the running poller, if there is one; a reading it is inside is
// abandoned where it stands.
func (w *Watch) halt() {
	w.mu.Lock()
	if w.stop != nil {
		close(w.stop)
		w.stop = nil
	}
	w.mu.Unlock()
}

// read takes one reading on a goroutine of its own and waits for it until
// ctx ends or the deadline passes, whichever comes first. A reading that has
// not answered by then is abandoned — its goroutine lives until winspool
// returns and its answer is dropped.
func (w *Watch) read(ctx context.Context, deadline time.Time) (Set, error) {
	type reading struct {
		set Set
		err error
	}
	ch := make(chan reading, 1)
	go func() {
		s, err := w.src.Snapshot()
		ch <- reading{s, err}
	}()
	for {
		select {
		case r := <-ch:
			return r.set, r.err
		default:
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !w.now().Before(deadline) {
			return nil, ErrStalled
		}
		w.sleep(waitGrain)
	}
}

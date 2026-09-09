// Package spool watches the Windows print spooler across a print, so that
// the recovery key's dialog can tell a print that was submitted from one the
// user cancelled (APP.md §3 Shell, §6; DECISIONS 2026-09-09 — BitLocker
// knows the difference and Enfold's second confirmation existed only because
// it did not).
//
// The shape is deliberately small: a Snapshot of every job the machine's
// printers hold, and a Watch that takes one before window.print() and polls
// for a new one after afterprint. A job that later fails still counts — it
// was submitted — and a spooler that cannot be read at all is an error, on
// which the page falls back to asking.
//
// Nothing here ever creates a job.
package spool

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrUnsupported is what Snapshot answers where there is no print spooler to
// read (every platform but Windows).
var ErrUnsupported = errors.New("spool: reading the print spooler needs Windows")

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

// The poll after a print: how long to wait for the spooler to show the job,
// and how often to look. Three seconds is what APP.md §6 allows before the
// answer is "nothing was submitted".
const (
	PollWindow   = 3 * time.Second
	PollInterval = 250 * time.Millisecond
)

// Watch is one print's worth of watching: Begin before window.print(), End
// after afterprint.
//
// Now and Sleep are the clock, replaced by tests; both are the real ones
// when zero.
type Watch struct {
	src   Snapshotter
	Now   func() time.Time
	Sleep func(time.Duration)

	begun   Set
	started bool
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

// Begin snapshots the jobs standing before the print. An unreadable spooler
// is an error here, so the page can fall back to its second confirmation
// before it prints rather than after.
func (w *Watch) Begin() error {
	s, err := w.src.Snapshot()
	if err != nil {
		w.started = false
		return err
	}
	w.begun, w.started = s, true
	return nil
}

// End answers whether a job appeared that Begin did not see. It polls for
// PollWindow and returns the moment one does; a spooler that cannot be read
// is an error, and only then does the page ask the user. A print job that
// later fails still counts: it was submitted.
func (w *Watch) End(ctx context.Context) (bool, error) {
	if !w.started {
		return false, errors.New("spool: End without a Begin that succeeded")
	}
	// One Begin, one End: the snapshot is spent here. A second End without a
	// fresh Begin would poll against the previous print's jobs and could
	// call a job that print submitted a new one — and the answer is what the
	// dialog claims about a key leaving the machine.
	defer func() { w.started = false }()
	deadline := w.now().Add(PollWindow)
	for {
		s, err := w.src.Snapshot()
		if err != nil {
			return false, err
		}
		if s.NewJobSince(w.begun) {
			return true, nil
		}
		if !w.now().Before(deadline) {
			return false, nil
		}
		if err := ctx.Err(); err != nil {
			return false, err
		}
		w.sleep(PollInterval)
	}
}

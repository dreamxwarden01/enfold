package app

import (
	"time"
)

// ResolveForShutdown is the ordered, bounded end of the process (APP.md
// §5): a running operation is cancelled — its transaction aborted, nothing
// published, which the format tolerates — each owed receipt is written, the
// archives are closed, then the lock, bounded to the budget in all. Nothing
// is committed here: since 2026-09-09 every operation commits at its own
// end, so an archive between operations has nothing outstanding (§2.3).
// Called by the shell's shutdown hook and by the tray's Quit after the user
// answered; never shows UI.
func (c *Core) ResolveForShutdown(budget time.Duration) {
	deadline := c.now().Add(budget)
	c.mu.Lock()
	cer := c.cer
	if cer != nil {
		cer.latch = true
		cer.cancelReason = string(ReasonExit)
		cer.cancel()
	}
	c.mu.Unlock()
	if cer != nil {
		// A slot change holds the handle; the receipts below would be
		// refused while it exists. An install must finish or not start.
		// And any ceremony's card call becomes the pending touch only when
		// the cancelled ceremony disowns it (APP.md §2.2), which the wait
		// for that touch below must come after. It is cancelled: wait for
		// its end, within the budget — a cancel is immediate in every
		// step, so this is microseconds unless a derivation is running.
		select {
		case <-cer.done:
		case <-time.After(budget / 2):
		}
	}
	c.mu.Lock()
	var list []*openArchive
	var running []*op
	for _, oa := range c.archives {
		// The process is ending, so every page is left with it (APP.md
		// §2.3): a handle whose operation does not end within the budget is
		// then closed by that operation's own end rather than kept.
		oa.mounted = false
		list = append(list, oa)
		running = append(running, c.cancelOpsLocked(oa.id)...)
	}
	c.mu.Unlock()
	// A cancelled add or replace aborts its transaction on the way out; the
	// wait is what lets the archive be closed rather than left behind.
	if remaining := deadline.Sub(c.now()); remaining > 0 {
		c.awaitOps(running, remaining)
	}
	// The receipts a lock or a ceremony stranded are written while the
	// session is still there (APP.md §2.3, §5).
	c.mu.Lock()
	c.applyOwedLocked()
	c.mu.Unlock()
	for _, oa := range list {
		if !oa.opMu.TryLock() {
			continue // an operation did not end in time: its handle is its own
		}
		c.mu.Lock()
		if c.archives[oa.id] == oa {
			c.closeArchiveLocked(oa)
		}
		c.mu.Unlock()
		oa.opMu.Unlock()
	}
	c.LockNow(ReasonExit)
}

// AwaitPendingTouch waits for a pending touch (APP.md §2.2) to end, so
// that the card is released — and reset — by this process rather than
// by Windows at the cleanup of a dead client's connection. The shell
// calls it from the tray's Quit after the window and the tray are gone,
// so nobody stands at a frozen window for it; the key gives up on its
// own in about 15 s. Logoff does not wait: the OS's own reset was
// measured (DESIGN.md §11 trap 27).
func (c *Core) AwaitPendingTouch() {
	c.awaitPending(pendingExitWait)
}

// pendingExitWait bounds the exit's wait for a pending touch: the key's
// own timeout, and its release.
const pendingExitWait = 20 * time.Second

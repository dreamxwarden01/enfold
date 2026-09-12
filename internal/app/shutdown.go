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

// awaitLocks waits for the unbounded halves of the locks already made
// (afterLock), which is what closes the vault's file: at most
// lockCloseBudget and never past deadline, the end's own. It is a step
// inside that budget rather than another one after it (APP.md §5), so a
// resolution that used all three seconds leaves it none — afterLock waits
// first for a cancelled ceremony and then for a pending touch, and neither
// is the shutdown's to wait for.
func (c *Core) awaitLocks(deadline time.Time) {
	done := make(chan struct{})
	go func() {
		c.lockWG.Wait()
		close(done)
	}()
	budget := c.lockWait
	if left := deadline.Sub(c.now()); left < budget {
		budget = left
	}
	if budget > 0 {
		t := time.NewTimer(budget)
		defer t.Stop()
		select {
		case <-done:
			return
		case <-t.C:
		}
	} else {
		select {
		case <-done:
			return
		default:
		}
	}
	c.log("shutdown: the lock did not close the vault's file within %v", budget)
}

// closeBudget is the whole of Close: the resolution of APP.md §5 and then
// what is left of it for the handle the lock closes.
const closeBudget = 3 * time.Second

// lockCloseBudget bounds Close's share of it for that handle. Closing the
// handle is a CloseHandle — microseconds — so the budget is only for what
// afterLock waits for first, and a second is already far more than a handle
// needs: it is the slice the exit's own housekeeping gets of the ~3 s
// shutdown (APP.md §5, main_windows.go), and the process is ending either
// way.
const lockCloseBudget = time.Second

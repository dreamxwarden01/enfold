package app

import (
	"context"
	"time"
)

// ResolveForShutdown is the ordered, bounded end of the process (APP.md
// §5): every dirty archive committed under a fresh short context, each
// receipt recorded, the archives closed, then the lock. A commit that does
// not finish in time stays unpublished, which the format tolerates. Called
// by the shell's shutdown hook and by the tray's Quit after the user
// answered; never shows UI.
func (c *Core) ResolveForShutdown(budget time.Duration) {
	deadline := c.now().Add(budget)
	c.mu.Lock()
	if c.cer != nil {
		c.cer.latch = true
		c.cer.cancelReason = string(ReasonExit)
		c.cer.cancel()
	}
	var list []*openArchive
	for _, oa := range c.archives {
		list = append(list, oa)
	}
	c.mu.Unlock()
	for _, oa := range list {
		if !oa.opMu.TryLock() {
			continue // an operation is running; its transaction stays unpublished
		}
		c.mu.Lock()
		if oa.tx != nil {
			remaining := deadline.Sub(c.now())
			if remaining > 0 {
				ctx, cancel := context.WithTimeout(context.Background(), remaining)
				rec, err := oa.tx.Commit(ctx)
				cancel()
				if err == nil {
					c.clearDirtyLocked(oa)
					oa.refreshSnapshot()
					c.recordReceiptLocked(oa, rec, nil)
				} else {
					c.log("shutdown: %s not saved: %v", oa.name, err)
				}
			}
		}
		if oa.tx == nil {
			c.closeArchiveLocked(oa)
		} else {
			// Unpublished: leave the file as the last commit left it.
			oa.tx.Abort()
			c.clearDirtyLocked(oa)
			c.closeArchiveLocked(oa)
		}
		c.mu.Unlock()
		oa.opMu.Unlock()
	}
	c.LockNow(ReasonExit)
}

//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestTheExitSweepsBeforeTheDragOutShutsDown is endDragOut's whole reason to
// exist (APP.md §3, ruled 2026-09-11): the sweep of the staging root runs
// while this run's own folders are still registered live and still
// manifested "live", and dragout.Shutdown — which is what takes them off
// that register — only after it. The other order would leave the hour's age
// limit as the one thing between the sweep and a drag whose files a target
// is reading at that moment.
func TestTheExitSweepsBeforeTheDragOutShutsDown(t *testing.T) {
	var order []string
	var got time.Duration
	endDragOut(func(budget time.Duration) {
		order = append(order, "sweep")
		got = budget
	}, dragSweepBudget, func() {
		order = append(order, "shutdown")
	})
	if len(order) != 2 || order[0] != "sweep" || order[1] != "shutdown" {
		t.Fatalf("the exit ran %v, want the sweep and then the drag out's shutdown", order)
	}
	if got != dragSweepBudget {
		t.Fatalf("the sweep was given %s, want the %s budget", got, dragSweepBudget)
	}
	// Housekeeping is a guest in the shutdown's budget, never the whole of
	// it: the archives and the lock are what it is for.
	if dragSweepBudget <= 0 || dragSweepBudget >= shutdownBudget {
		t.Fatalf("the sweep's budget is %s of a %s shutdown, want a bounded slice of it", dragSweepBudget, shutdownBudget)
	}
}

// TestTheStagingRootIsUnderTemp: the staging root is %TEMP%\Enfold\drag and
// nothing of it is under the data folder any more (ruled 2026-09-11). The
// user's own TMP or TEMP is what os.TempDir reads, so a redirection — a RAM
// disk, another volume — is honoured; the test proves that by moving it.
func TestTheStagingRootIsUnderTemp(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)
	root := dragStagingRoot()
	if want := filepath.Join(tmp, appName, "drag"); root != want {
		t.Fatalf("the staging root is %s, want %s — os.TempDir is GetTempPath, which reads TMP and TEMP", root, want)
	}
	// Nothing was created by asking: the root is made by the first drag,
	// and an old %LOCALAPPDATA%\Enfold\drag is not this program's to touch.
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("naming the staging root created it: %v", err)
	}
}

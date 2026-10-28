package app

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

// removeTempDirs removes, once the core is closed, the folder t.TempDir gave
// the harness and every t.TempDir the test made after it — the folders that
// only this core, a later one or the test itself can be using — before
// testing's own cleanup removes the test's whole temporary root. The harness
// calls it right after its t.TempDir, so that it runs after the core's
// Close; and once per test, so that with two harnesses it runs after both.
// A folder made before the harness is left to testing, since a core made
// before it is closed after this runs.
//
// It is patient where testing is not. On the development machine a file
// the test binary deletes is now and then followed by a copy of it the
// anti-virus makes beside it for a few milliseconds — NAME.EXT.tmp, owned
// by Administrators, which is the owner of what a SYSTEM service makes —
// and RemoveAll then fails on the folder with "The directory is not empty",
// which testing never retries (it retries a denied access and a sharing
// violation only). Measured 2026-10-28: 24 of 1,600 runs of
// TestACompactionSaysWhatItGaveBack and TestDragOutRunsAsAnOperation failed
// so, with no goroutine of the core's left running. A folder another
// account's file keeps from going is tried again, up to strayPatience; a
// file this process made is never waited out — something of the core's that
// outlived its Close is a failure, and it is named. Anything else, a handle
// still open among them, is left to testing's removal, which comes after
// every other cleanup and judges it as it always has.
func removeTempDirs(t *testing.T, dir string) {
	t.Helper()
	root := filepath.Dir(dir)
	from, err := strconv.Atoi(filepath.Base(dir))
	if err != nil {
		t.Fatalf("not a folder t.TempDir made: %s", dir)
	}
	if _, registered := tempRoots.LoadOrStore(root, true); registered {
		return
	}
	t.Cleanup(func() {
		defer tempRoots.Delete(root)
		entries, _ := os.ReadDir(root)
		for _, e := range entries {
			if n, err := strconv.Atoi(e.Name()); err == nil && e.IsDir() && n >= from {
				removePatiently(t, filepath.Join(root, e.Name()))
			}
		}
	})
}

// removePatiently is the removal of one folder removeTempDirs makes.
func removePatiently(t *testing.T, dir string) {
	start := time.Now()
	var first error
	for {
		err := os.RemoveAll(dir)
		if err == nil {
			if first != nil {
				t.Logf("removed after %v waiting out another account's file: %v", time.Since(start).Round(time.Millisecond), first)
			}
			return
		}
		if first == nil {
			first = err
		}
		wait, ours := strayVerdict(dir, err)
		if len(ours) > 0 {
			t.Errorf("after the core's Close, %s still holds what this process made: %q (%v)", dir, ours, err)
			return
		}
		if !wait || time.Since(start) > strayPatience {
			return // testing's own removal comes last and reports it
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// tempRoots are the roots whose removal is registered, so that it is
// registered once per test.
var tempRoots sync.Map

// strayPatience bounds the wait for another account's file to go: what
// testing gives a denied access. The anti-virus's copies were gone within
// 200 ms every time they were looked at.
const strayPatience = 2 * time.Second

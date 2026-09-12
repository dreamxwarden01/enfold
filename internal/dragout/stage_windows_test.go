//go:build windows

package dragout

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// holdOpen opens a file with no sharing at all, the way a consumer that
// keeps a dropped file open holds it: a delete fails with a sharing
// violation until the handle goes.
func holdOpen(t *testing.T, path string) (release func()) {
	t.Helper()
	return holdOpenWith(t, path, 0, 0)
}

// holdOpenWith is holdOpen with the share mode and flags of the caller's
// choosing: FILE_SHARE_DELETE is the consumer a delete does not stop, and
// FILE_FLAG_BACKUP_SEMANTICS is how a directory is held. The open is
// retried for a moment, because the machine's on-access scanner may have
// the freshly written file open itself, and its handle would refuse ours
// the same way.
func holdOpenWith(t *testing.T, path string, share, flags uint32) (release func()) {
	t.Helper()
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	var h windows.Handle
	deadline := time.Now().Add(3 * time.Second)
	for {
		h, err = windows.CreateFile(p, windows.GENERIC_READ, share, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|flags, 0)
		if err == nil {
			break
		}
		if err != windows.ERROR_SHARING_VIOLATION || time.Now().After(deadline) {
			t.Fatalf("holding the staged item open: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	var once sync.Once
	return func() { once.Do(func() { windows.CloseHandle(h) }) }
}

// noBackoff stands in for the sweep's sleeps and records what they would
// have waited, so that the bounded backoff is asserted and not sat through.
func noBackoff(t *testing.T) *[]time.Duration {
	t.Helper()
	waits := &[]time.Duration{}
	prev := sleepBackoff
	sleepBackoff = func(d time.Duration) { *waits = append(*waits, d) }
	t.Cleanup(func() { sleepBackoff = prev })
	return waits
}

// agedStage is a drag whose extraction ran and whose watch has let go —
// the folder is the sweep's — with its manifest dated past the hour.
func agedStage(t *testing.T, items []Item) (*stage, *testLog) {
	t.Helper()
	s, _, lg := newTestStage(t, items, nil)
	s.arm(false, "CabinetWClass")
	if _, ok := s.requestPaths(); !ok {
		t.Fatal("the extraction was refused")
	}
	s.finish()
	m, ok := ReadManifest(s.root)
	if !ok || m.State != StateHandedOut {
		t.Fatalf("the manifest after the extraction is %+v %v", m, ok)
	}
	m.Created = time.Now().Add(-2 * time.Hour)
	if err := WriteManifest(s.root, m); err != nil {
		t.Fatal(err)
	}
	return s, lg
}

// TestFailedDeleteNamesNoFileAndLeavesTheManifest: a delete that a held
// file defeats is logged with the error Windows gave and never the file's
// name (APP.md §3), and leaves the folder manifested done, off the live
// set, for the sweep — which takes it once the handle has gone. The
// bounded backoff itself is what a test cannot sit through; the verdict
// stands in for it.
func TestFailedDeleteNamesNoFileAndLeavesTheManifest(t *testing.T) {
	isolateStages(t)
	const name = "secret-report.bin"
	s, _, lg := newTestStage(t, []Item{{Name: name, Size: 8}}, nil)
	s.arm(false, "CabinetWClass")
	if _, ok := s.requestPaths(); !ok {
		t.Fatal("the extraction was refused")
	}
	release := holdOpen(t, s.paths[0])
	defer release()

	s.remove()
	s.mu.Lock()
	removed, deleted := s.removed, s.deleted
	s.mu.Unlock()
	if removed || !deleted {
		t.Fatalf("removed=%v deleted=%v after a delete a held file defeated, want false/true", removed, deleted)
	}
	if text := lg.text(); strings.Contains(text, name) || strings.Contains(text, s.root) {
		t.Fatalf("the log names a path:\n%s", text)
	}
	if !strings.Contains(lg.text(), "could not be deleted") {
		t.Fatalf("the log does not say the delete failed:\n%s", lg.text())
	}
	if m, ok := ReadManifest(s.root); !ok || m.State != StateDone {
		t.Fatalf("the folder left behind is manifested %q %v, want %q for the sweep", m.State, ok, StateDone)
	}
	if stageIsActive(s.root) {
		t.Error("a resolved stage is still registered, so the sweep would leave it")
	}
	if remove, _ := scavengeVerdict(scavengeFacts{haveManifest: true, state: StateDone, age: 2 * time.Hour, maxAge: time.Hour}); !remove {
		t.Fatal("the sweep would leave a folder manifested done and past the limit")
	}

	release()
	sweep := &testLog{}
	rep := scavenge(filepath.Dir(s.root), time.Hour, time.Now().Add(2*time.Hour), sweepMode{retry: true}, sweep.printf)
	if rep.Removed != 1 || rep.Left != 0 {
		t.Fatalf("the sweep removed %d and left %d, want 1/0:\n%s", rep.Removed, rep.Left, sweep.text())
	}
	if _, err := os.Stat(s.root); !os.IsNotExist(err) {
		t.Fatalf("the folder survived the sweep: %v", err)
	}
	if strings.Contains(sweep.text(), name) {
		t.Fatalf("the sweep's log names a file:\n%s", sweep.text())
	}
}

// TestSweepAsksBeforeItDeletes is APP.md §3's order: the exclusive open
// first, and nothing deleted while it reports a sharing violation. The
// consumer here holds the file with FILE_SHARE_DELETE — a delete would go
// through, and the file would go from under a program still reading it —
// so only the probe stands between the two. The folder is retried with the
// bounded backoff, 1 s, 10 s, 60 s, and left for the next sweep, which takes
// it once the handle has gone.
func TestSweepAsksBeforeItDeletes(t *testing.T) {
	isolateStages(t)
	const name = "still-reading.bin"
	s, _ := agedStage(t, []Item{{Name: name, Size: 8}, {Name: "other.bin", Size: 8}})
	release := holdOpenWith(t, s.paths[0], windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, 0)
	defer release()
	waits := noBackoff(t)

	sweep := &testLog{}
	rep := scavenge(filepath.Dir(s.root), time.Hour, time.Now(), sweepMode{retry: true}, sweep.printf)
	if rep.Removed != 0 || rep.Left != 1 {
		t.Fatalf("the sweep removed %d and left %d, want 0/1:\n%s", rep.Removed, rep.Left, sweep.text())
	}
	for i, p := range s.paths {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("staged file %d was deleted under a consumer still reading: %v", i, err)
		}
	}
	if m, ok := ReadManifest(s.root); !ok || m.State != StateHandedOut {
		t.Fatalf("the folder is manifested %+v %v after a sweep that deleted nothing", m, ok)
	}
	if text := sweep.text(); !strings.Contains(text, "in use by another process") || !strings.Contains(text, "nothing deleted") || !strings.Contains(text, "leaving") {
		t.Fatalf("the log does not say the folder was in use and left:\n%s", text)
	}
	if strings.Contains(sweep.text(), name) {
		t.Fatalf("the sweep's log names a file:\n%s", sweep.text())
	}
	if len(*waits) != 3 || (*waits)[0] != time.Second || (*waits)[1] != 10*time.Second || (*waits)[2] != time.Minute {
		t.Fatalf("the backoff waited %v, want 1 s, 10 s, 60 s and then leave", *waits)
	}

	release()
	sweep = &testLog{}
	if rep := scavenge(filepath.Dir(s.root), time.Hour, time.Now(), sweepMode{retry: true}, sweep.printf); rep.Removed != 1 || rep.Left != 0 {
		t.Fatalf("the next sweep removed %d and left %d, want 1/0:\n%s", rep.Removed, rep.Left, sweep.text())
	}
	if _, err := os.Stat(s.root); !os.IsNotExist(err) {
		t.Fatalf("the folder survived the next sweep: %v", err)
	}
}

// TestSweepKeepsTheManifestWhenADeleteFailsHalfWay: a staged folder a
// consumer holds open — an Explorer window on it — is not a file the probe
// asks about, so the delete runs and fails on it. The items that could go
// went, the manifest was never reached, and it is rewritten "done" with the
// drag's own date, so that the next sweep — once the handle has gone — takes
// the folder rather than being forbidden to touch it (APP.md §3).
func TestSweepKeepsTheManifestWhenADeleteFailsHalfWay(t *testing.T) {
	isolateStages(t)
	items := []Item{{Name: "Docs", IsDir: true}, {Name: `Docs\a.txt`, Size: 5}, {Name: "b.txt", Size: 5}}
	s, _ := agedStage(t, items)
	before, _ := ReadManifest(s.root)
	release := holdOpenWith(t, s.paths[0], 0, windows.FILE_FLAG_BACKUP_SEMANTICS)
	defer release()
	noBackoff(t)

	sweep := &testLog{}
	if rep := scavenge(filepath.Dir(s.root), time.Hour, time.Now(), sweepMode{retry: true}, sweep.printf); rep.Removed != 0 || rep.Left != 1 {
		t.Fatalf("the sweep removed %d and left %d, want 0/1:\n%s", rep.Removed, rep.Left, sweep.text())
	}
	if _, err := os.Stat(s.paths[0]); err != nil {
		t.Fatalf("the held folder went: %v", err)
	}
	if _, err := os.Stat(s.paths[2]); !os.IsNotExist(err) {
		t.Errorf("the free item was not deleted: %v", err)
	}
	m, ok := ReadManifest(s.root)
	if !ok {
		t.Fatal("the failed delete took the manifest: the folder is nobody's now")
	}
	if m.State != StateDone || !m.Created.Equal(before.Created) {
		t.Fatalf("the manifest left behind is %+v, want %q dated %s", m, StateDone, before.Created)
	}
	if text := sweep.text(); !strings.Contains(text, "could not remove") || !strings.Contains(text, "rewritten") {
		t.Fatalf("the log does not say the delete failed and the manifest was rewritten:\n%s", text)
	}
	if text := sweep.text(); strings.Contains(text, "Docs") || strings.Contains(text, "a.txt") || strings.Contains(text, "b.txt") {
		t.Fatalf("the sweep's log names an item:\n%s", text)
	}

	release()
	sweep = &testLog{}
	if rep := scavenge(filepath.Dir(s.root), time.Hour, time.Now(), sweepMode{retry: true}, sweep.printf); rep.Removed != 1 || rep.Left != 0 {
		t.Fatalf("the next sweep removed %d and left %d, want 1/0:\n%s", rep.Removed, rep.Left, sweep.text())
	}
	if _, err := os.Stat(s.root); !os.IsNotExist(err) {
		t.Fatalf("the folder survived the next sweep: %v", err)
	}
}

// TestTheSweepAtExitWaitsForNothing is the pass a normal exit makes (APP.md
// §3, ruled 2026-09-11): one attempt at each folder and not a second of the
// backoff — a shutdown has a budget, and a folder a consumer still holds is
// the next launch's sweep to take, not this one's to sit and wait for —
// while the free folder beside it, past the hour, still goes. The counts
// say which was which, because a sweep that removes nothing for that reason
// is working exactly as intended.
func TestTheSweepAtExitWaitsForNothing(t *testing.T) {
	isolateStages(t)
	const name = "still-reading.bin"
	s, _ := agedStage(t, []Item{{Name: name, Size: 8}})
	root := filepath.Dir(s.root)
	free := filepath.Join(root, "ffffffff")
	if err := os.MkdirAll(free, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(free, "payload.bin"), []byte("plaintext"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteManifest(free, Manifest{Tool: manifestTool, Version: manifestVersion, State: StateDone, Created: time.Now().Add(-2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	release := holdOpen(t, s.paths[0])
	defer release()
	waits := noBackoff(t)

	sweep := &testLog{}
	rep := ScavengeAtExit(root, time.Hour, 10*time.Second, sweep.printf)
	if len(*waits) != 0 {
		t.Fatalf("the sweep at exit slept %v; the pass at exit waits for nothing", *waits)
	}
	if rep.Removed != 1 || rep.Left != 1 || rep.InUse != 1 || rep.Unreached != 0 {
		t.Fatalf("the pass came to %+v, want 1 removed, 1 left, 1 of them in use, 0 not reached:\n%s", rep, sweep.text())
	}
	if _, err := os.Stat(s.paths[0]); err != nil {
		t.Errorf("the staged file was deleted under a consumer still reading it: %v", err)
	}
	if _, err := os.Stat(free); !os.IsNotExist(err) {
		t.Errorf("the free folder past the hour survived the pass: %v", err)
	}
	if m, ok := ReadManifest(s.root); !ok || m.State != StateHandedOut {
		t.Fatalf("the folder in use is manifested %+v %v: the next launch's sweep must still recognise it", m, ok)
	}
	if text := sweep.text(); !strings.Contains(text, "in use by another process") || !strings.Contains(text, "the next launch") {
		t.Fatalf("the log does not say the folder was in use and left for the next launch:\n%s", text)
	}
	if strings.Contains(sweep.text(), name) {
		t.Fatalf("the sweep's log names a file:\n%s", sweep.text())
	}

	// The handle gone, the next launch's sweep takes it.
	release()
	sweep = &testLog{}
	if rep := ScavengeAtExit(root, time.Hour, 10*time.Second, sweep.printf); rep.Removed != 1 || rep.Left != 0 {
		t.Fatalf("the next pass came to %+v, want 1 removed and 0 left:\n%s", rep, sweep.text())
	}
	if _, err := os.Stat(s.root); !os.IsNotExist(err) {
		t.Fatalf("the folder survived the next pass: %v", err)
	}
}

package app

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dreamxwarden01/enfold/internal/format"
)

// Helpers for the inspector's tests: reading and stamping registry records
// without going through the actions under test.

// record is a copy of one registry record, or a failure.
func (h *harness) record(id string) *format.ArchiveRecord {
	h.t.Helper()
	aid, ok := parseID(id)
	if !ok {
		h.t.Fatalf("bad id %q", id)
	}
	h.c.mu.Lock()
	defer h.c.mu.Unlock()
	sess, e := h.c.sessionLocked()
	if e != nil {
		h.t.Fatalf("session: %v", e)
	}
	a := findRecord(sess.Registry(), aid)
	if a == nil {
		h.t.Fatalf("no record %s", id)
	}
	cp := *a
	cp.Versions = append([]format.VersionRecord(nil), a.Versions...)
	return &cp
}

// hasRecord reports whether the registry still holds the record.
func (h *harness) hasRecord(id string) bool {
	aid, _ := parseID(id)
	h.c.mu.Lock()
	defer h.c.mu.Unlock()
	sess, e := h.c.sessionLocked()
	if e != nil {
		h.t.Fatalf("session: %v", e)
	}
	return findRecord(sess.Registry(), aid) != nil
}

// editRecord changes a record directly, with the write's own modified_at, so
// a test can age or move a record without the action that would do it.
func (h *harness) editRecord(id string, mut func(a *format.ArchiveRecord, modifiedAt int64)) {
	h.t.Helper()
	aid, _ := parseID(id)
	if e := h.c.updateRegistryAt(true, func(g *registry, at int64) error {
		a := findRecord(g, aid)
		if a == nil {
			return coded(CodeArchiveNotFound)
		}
		mut(a, at)
		return nil
	}); e != nil {
		h.t.Fatalf("edit record: %v", e)
	}
}

// newArchive creates an archive and returns its id and path.
func (h *harness) newArchive(name string) (string, string) {
	h.t.Helper()
	p := filepath.Join(h.dir, name+".enf")
	id, e := h.c.CreateArchive(p, name, compressionNormal)
	if e != nil {
		h.t.Fatalf("create %s: %v", name, e)
	}
	return id, p
}

// exportBackup writes a backup of the vault kept here, answering the export
// ceremony's password prompt.
func (h *harness) exportBackup(path string) *Error {
	h.t.Helper()
	h.rec.reset()
	if e := h.c.ExportBackup(path); e != nil {
		return e
	}
	p := h.rec.waitCeremony(h.t, StepPassword, true)
	if e := h.c.SubmitSecret("password", p.PromptID, testPassword); e != nil {
		h.t.Fatalf("submit: %v", e)
	}
	h.rec.waitCeremony(h.t, StepDone, false)
	return nil
}

// inspectRecords runs the merge's ceremony over path with this vault's own
// way in and returns the handle the ceremony ends with.
func (h *harness) inspectRecords(path string) string {
	h.t.Helper()
	h.rec.reset()
	if e := h.c.InspectRecords(path); e != nil {
		h.t.Fatalf("inspect records: %v", e)
	}
	p := h.rec.waitCeremony(h.t, StepPassword, true)
	if e := h.c.SubmitSecret("password", p.PromptID, testPassword); e != nil {
		h.t.Fatalf("submit: %v", e)
	}
	st := h.rec.waitCeremony(h.t, StepRecords, false)
	if st.SlotLabel == "" {
		h.t.Fatalf("the records ceremony left no handle: %+v", st)
	}
	return st.SlotLabel
}

// fieldNames are a struct's exported field names.
func fieldNames(v any) []string {
	t := reflect.TypeOf(v)
	out := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		out = append(out, t.Field(i).Name)
	}
	return out
}

// probeLog records what the stubbed probe was asked, safely across the pass's
// goroutine and the test's.
type probeLog struct {
	mu   sync.Mutex
	seen map[string]int
}

func newProbeLog() *probeLog { return &probeLog{seen: map[string]int{}} }

func (l *probeLog) note(p string) {
	l.mu.Lock()
	l.seen[p]++
	l.mu.Unlock()
}

func (l *probeLog) count(p string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.seen[p]
}

func (l *probeLog) total() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, v := range l.seen {
		n += v
	}
	return n
}

// A forgotten record still holds its keys, so every operation but Restore and
// Delete answers archive.forgotten — never archive.not_found, which stays the
// answer for a record already purged (APP.md §13).
func TestForgetRefusesTheOperationsOfASoftDeletedRecord(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, p := h.newArchive("A")
	if e := h.c.ForgetArchive(id); e != nil {
		t.Fatalf("forget: %v", e)
	}
	cases := []struct {
		what string
		got  *Error
	}{
		{"open", errOf(h.c.OpenArchive(id))},
		{"rename", h.c.RenameArchive(id, "B")},
		{"describe", h.c.SetArchiveDescription(id, "x")},
		{"hide", h.c.HideArchive(id, true)},
		{"unhide", h.c.HideArchive(id, false)},
		{"locate", h.c.Locate(id, p)},
		{"verify", errOf2(h.c.Verify(id))},
		{"compact", errOf2(h.c.Compact(id))},
		{"rotate key", errOf2(h.c.RotateKey(id))},
	}
	for _, c := range cases {
		if !isCode(c.got, CodeArchiveForgotten) {
			t.Errorf("%s on a forgotten record: %v, want archive.forgotten", c.what, c.got)
		}
	}
	// Details is not refused: the pane must render Restore.
	if _, e := h.c.ArchiveDetails(id); e != nil {
		t.Fatalf("details of a forgotten record: %v", e)
	}
	// A record that is gone answers not_found, not forgotten.
	gone := "ff" + strings.Repeat("00", 15)
	if e := h.c.RenameArchive(gone, "B"); !isCode(e, CodeArchiveNotFound) {
		t.Fatalf("rename of a purged record: %v", e)
	}
}

func errOf(_ ArchiveStat, e *Error) *Error { return e }
func errOf2(_ string, e *Error) *Error     { return e }

// The retention clock never restarts: a second Forget, and a Delete of an
// already forgotten record, leave forgotten_at where it was (A.15).
func TestForgetDoesNotRestartTheRetentionClock(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, p := h.newArchive("A")
	if e := h.c.ForgetArchive(id); e != nil {
		t.Fatalf("forget: %v", e)
	}
	first := h.record(id).ForgottenAt
	if first == 0 {
		t.Fatal("forgotten_at not set")
	}
	if first != h.status().ModifiedAt {
		t.Fatalf("forgotten_at %d is not the write's own modified_at %d", first, h.status().ModifiedAt)
	}
	was := h.status().ModifiedAt
	if e := h.c.ForgetArchive(id); e != nil {
		t.Fatalf("second forget: %v", e)
	}
	if got := h.record(id).ForgottenAt; got != first {
		t.Fatalf("a second forget moved the clock: %d -> %d", first, got)
	}
	// And it wrote nothing at all: a commit that changed nothing would
	// restamp the vault's modified_at (FORMAT.md R35).
	if got := h.status().ModifiedAt; got != was {
		t.Fatalf("a second forget restamped modified_at: %d -> %d", was, got)
	}
	// So does a rename or a description set to the value already held, and
	// a restore of a record that is not forgotten.
	id2, _ := h.newArchive("B")
	was = h.status().ModifiedAt
	if e := h.c.RenameArchive(id2, "B"); e != nil {
		t.Fatalf("rename to the same name: %v", e)
	}
	if e := h.c.SetArchiveDescription(id2, ""); e != nil {
		t.Fatalf("description set to the empty it holds: %v", e)
	}
	if e := h.c.RestoreArchive(id2); e != nil {
		t.Fatalf("restore of a record that is not forgotten: %v", e)
	}
	if got := h.status().ModifiedAt; got != was {
		t.Fatalf("a write that changed nothing restamped modified_at: %d -> %d", was, got)
	}
	// A real change still lands.
	if e := h.c.RenameArchive(id2, "B2"); e != nil {
		t.Fatalf("rename: %v", e)
	}
	if got := h.status().ModifiedAt; got == was {
		t.Fatalf("a rename that changed the name did not restamp modified_at: %d", got)
	}
	// The same through Delete, which completes its own forget.
	if e := h.c.DeleteArchive(id, true); e != nil {
		t.Fatalf("delete: %v", e)
	}
	if _, err := os.Stat(p); err == nil {
		t.Fatal("the file is still there")
	}
	if got := h.record(id).ForgottenAt; got != first {
		t.Fatalf("delete of a forgotten record moved the clock: %d -> %d", first, got)
	}
}

// Restore clears forgotten_at and the record is an ordinary one again.
func TestRestoreClearsForgottenAt(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, _ := h.newArchive("A")
	h.c.ForgetArchive(id)
	if list, _ := h.c.ListArchives(false); len(list) != 0 {
		t.Fatalf("a forgotten record is listed without showHidden: %+v", list)
	}
	list, _ := h.c.ListArchives(true)
	if len(list) != 1 || list[0].ForgottenAt == 0 {
		t.Fatalf("show hidden: %+v", list)
	}
	if e := h.c.RestoreArchive(id); e != nil {
		t.Fatalf("restore: %v", e)
	}
	if got := h.record(id).ForgottenAt; got != 0 {
		t.Fatalf("forgotten_at after restore: %d", got)
	}
	if _, e := h.c.OpenArchive(id); e != nil {
		t.Fatalf("open after restore: %v", e)
	}
}

// The guard behind the per-call refusals: a registry write that changes a
// forgotten record is refused unless it says it means to (A.2).
func TestNoRegistryWriteUpdatesAForgottenRecord(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, _ := h.newArchive("A")
	aid, _ := parseID(id)
	h.c.ForgetArchive(id)
	e := h.c.updateRegistry(func(g *registry) error {
		findRecord(g, aid).Name = "renamed behind the guard"
		return nil
	})
	if !isCode(e, CodeArchiveForgotten) {
		t.Fatalf("an unguarded write of a forgotten record: %v", e)
	}
	if got := h.record(id).Name; got != "A" {
		t.Fatalf("the refused write landed: %q", got)
	}
	// The same write with the intent goes through.
	if e := h.c.updateRegistryAt(true, func(g *registry, _ int64) error {
		findRecord(g, aid).Name = "B"
		return nil
	}); e != nil {
		t.Fatalf("the guarded write: %v", e)
	}
	if got := h.record(id).Name; got != "B" {
		t.Fatalf("the intended write did not land: %q", got)
	}
	// A record that is not forgotten is untouched by the guard.
	other, _ := h.newArchive("C")
	if e := h.c.RenameArchive(other, "C2"); e != nil {
		t.Fatalf("rename of a live record: %v", e)
	}
}

// Delete removes the file only when the envelope's archive_id is the
// record's, and only then is the record forgotten (APP.md §13, trap 28).
func TestDeleteRemovesTheFileOnlyOnAnArchiveIDMatch(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	idA, pa := h.newArchive("A")
	_, pb := h.newArchive("B")
	mine, err := os.ReadFile(pa)
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := os.ReadFile(pb)
	if err != nil {
		t.Fatal(err)
	}
	// Another archive's file at A's place: nothing removed, nothing forgotten.
	if err := os.WriteFile(pa, theirs, 0o600); err != nil {
		t.Fatal(err)
	}
	if e := h.c.DeleteArchive(idA, true); !isCode(e, CodeArchiveNotThisOne) {
		t.Fatalf("delete over another archive: %v", e)
	}
	if _, err := os.Stat(pa); err != nil {
		t.Fatal("the other archive's file was removed")
	}
	if h.record(idA).Forgotten() {
		t.Fatal("the record was forgotten against a file that is not its own")
	}
	// Its own file back: removed, and only then forgotten.
	if err := os.WriteFile(pa, mine, 0o600); err != nil {
		t.Fatal(err)
	}
	if e := h.c.DeleteArchive(idA, true); e != nil {
		t.Fatalf("delete: %v", e)
	}
	if _, err := os.Stat(pa); err == nil {
		t.Fatal("the file is still there")
	}
	if !h.record(idA).Forgotten() {
		t.Fatal("the record was not forgotten after the file went")
	}
}

// A delete aimed at another archive leaves that archive and its record
// exactly as they were.
func TestDeleteOfAnotherArchiveTouchesNothing(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	idA, pa := h.newArchive("A")
	idB, pb := h.newArchive("B")
	theirs, _ := os.ReadFile(pb)
	if err := os.WriteFile(pa, theirs, 0o600); err != nil {
		t.Fatal(err)
	}
	before := h.record(idB)
	if e := h.c.DeleteArchive(idA, true); !isCode(e, CodeArchiveNotThisOne) {
		t.Fatalf("delete: %v", e)
	}
	if _, err := os.Stat(pb); err != nil {
		t.Fatal("B's own file went")
	}
	if got := h.record(idB); got.Forgotten() || got.Revision != before.Revision {
		t.Fatalf("B's record changed: %+v", got)
	}
	if h.record(idA).Forgotten() {
		t.Fatal("A's record was forgotten")
	}
}

// Absence is decided on the parent folder, never on the open of the file: a
// leaf that is not in its folder is the proven mismatch, and a folder that is
// not there at all is unreachable.
func TestDeleteDecidesAbsenceOnTheParentFolder(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, p := h.newArchive("A")
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if e := h.c.DeleteArchive(id, true); !isCode(e, CodeArchiveNotThisOne) {
		t.Fatalf("delete of an absent leaf: %v", e)
	}
	if h.record(id).Forgotten() {
		t.Fatal("the record was forgotten for an absent file")
	}
	// A folder that is not there: unreachable, and nothing is decided.
	h.editRecord(id, func(a *format.ArchiveRecord, _ int64) {
		a.LastPath = filepath.Join(h.dir, "no-such-folder", "A.enf")
	})
	if e := h.c.DeleteArchive(id, true); !isCode(e, CodeArchiveUnreachable) {
		t.Fatalf("delete under an absent folder: %v", e)
	}
	if h.record(id).Forgotten() {
		t.Fatal("the record was forgotten for an unreachable path")
	}
	// A record with no path at all is the same answer.
	h.editRecord(id, func(a *format.ArchiveRecord, _ int64) { a.LastPath = "" })
	if e := h.c.DeleteArchive(id, true); !isCode(e, CodeArchiveUnreachable) {
		t.Fatalf("delete with no recorded path: %v", e)
	}
}

// A file that is there and cannot be read as an archive keeps its record: a
// record dropped against a file that could not be read destroys the keys of
// an archive that may still be intact.
func TestDeleteOfAnUnreadableFileForgetsNothing(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, p := h.newArchive("A")
	junk := make([]byte, format.SuperblockSize)
	for i := range junk {
		junk[i] = 0x5A
	}
	if err := os.WriteFile(p, junk, 0o600); err != nil {
		t.Fatal(err)
	}
	if e := h.c.DeleteArchive(id, true); !isCode(e, CodeArchiveInvalid) {
		t.Fatalf("delete of a file that is not an archive: %v", e)
	}
	if h.record(id).Forgotten() || fileGone(p) {
		t.Fatal("something was touched")
	}
	// A short file is the same: nothing removed, nothing forgotten.
	if err := os.WriteFile(p, []byte("too short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if e := h.c.DeleteArchive(id, true); !isCode(e, CodeArchiveInvalid) {
		t.Fatalf("delete of a short file: %v", e)
	}
	if h.record(id).Forgotten() || fileGone(p) {
		t.Fatal("something was touched by the short file")
	}
}

func fileGone(p string) bool { _, err := os.Stat(p); return err != nil }

// A removal that fails leaves the record alone, and the page is told the file
// could not be removed.
func TestDeleteFailedRemovalLeavesTheRecord(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, p := h.newArchive("A")
	saved := deleteArchiveFile
	deleteArchiveFile = func(string, [16]byte) (bool, error) {
		return false, errArchiveRemoveFailed
	}
	defer func() { deleteArchiveFile = saved }()
	if e := h.c.DeleteArchive(id, true); !isCode(e, CodeArchiveDeleteFailed) {
		t.Fatalf("delete: %v", e)
	}
	if h.record(id).Forgotten() {
		t.Fatal("the record was forgotten although the file was not removed")
	}
	if fileGone(p) {
		t.Fatal("the file went after all")
	}
}

// The archive is closed first: an open archive, and a live preview reader on
// it, are both archive.busy for Forget and Delete (A.7).
func TestDeleteRefusesWhileTheArchiveIsOpen(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, _ := h.newArchive("A")
	if _, e := h.c.OpenArchive(id); e != nil {
		t.Fatal(e)
	}
	if e := h.c.DeleteArchive(id, true); !isCode(e, CodeArchiveBusy) {
		t.Fatalf("delete while open: %v", e)
	}
	if e := h.c.ForgetArchive(id); !isCode(e, CodeArchiveBusy) {
		t.Fatalf("forget while open: %v", e)
	}
	if e := h.c.DeleteArchive(id, false); !isCode(e, CodeArchiveBusy) {
		t.Fatalf("delete without the file while open: %v", e)
	}
	if e := h.c.CloseArchive(id); e != nil {
		t.Fatal(e)
	}
	if e := h.c.ForgetArchive(id); e != nil {
		t.Fatalf("forget once closed: %v", e)
	}
}

// Delete releases the state mutex for the folder read and the removal, so it
// claims the record first: an Open that lands in that window is refused
// rather than handed a file the call is unlinking and a record it is about to
// forget (APP.md §13).
func TestDeleteClaimsTheRecordAcrossTheRemoval(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, p := h.newArchive("A")
	inside := make(chan struct{})
	release := make(chan struct{})
	saved := deleteArchiveFile
	deleteArchiveFile = func(path string, want [16]byte) (bool, error) {
		close(inside)
		<-release
		return saved(path, want)
	}
	defer func() { deleteArchiveFile = saved }()
	done := make(chan *Error, 1)
	go func() { done <- h.c.DeleteArchive(id, true) }()
	<-inside
	// The removal is in flight and the mutex is free.
	if _, e := h.c.OpenArchive(id); !isCode(e, CodeArchiveBusy) {
		t.Fatalf("open during a delete: %v", e)
	}
	if e := h.c.ForgetArchive(id); !isCode(e, CodeArchiveBusy) {
		t.Fatalf("forget during a delete: %v", e)
	}
	close(release)
	if e := <-done; e != nil {
		t.Fatalf("delete: %v", e)
	}
	if !fileGone(p) {
		t.Fatal("the file is still there")
	}
	if !h.record(id).Forgotten() {
		t.Fatal("the record was not forgotten")
	}
	// The claim is released with the call.
	h.c.mu.Lock()
	held := len(h.c.deleting)
	h.c.mu.Unlock()
	if held != 0 {
		t.Fatalf("the delete kept its claim: %d", held)
	}
}

// A preview reader is live on the open archive: the refusal is the same one,
// and it is the reader that must have ended before the file is touched.
func TestDeleteRefusesWhileAPreviewReaderIsLive(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, _ := h.newArchive("A")
	if _, e := h.c.OpenArchive(id); e != nil {
		t.Fatal(e)
	}
	aid, _ := parseID(id)
	h.c.mu.Lock()
	h.c.archives[aid].readers++
	h.c.mu.Unlock()
	if e := h.c.DeleteArchive(id, true); !isCode(e, CodeArchiveBusy) {
		t.Fatalf("delete while a preview reader is live: %v", e)
	}
	h.c.mu.Lock()
	h.c.archives[aid].readers--
	h.c.mu.Unlock()
}

// The file is opened once with DELETE access and unlinked through that same
// handle: a reader that does not permit deletion is met at the open, before
// anything is read, rather than after the id has been checked.
func TestDeleteUnlinksThroughItsOwnHandle(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the seam is Windows's")
	}
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, p := h.newArchive("A")
	aid, _ := parseID(id)
	f, err := os.Open(p) // share read|write, never delete
	if err != nil {
		t.Fatal(err)
	}
	removed, err := deleteArchiveFileIfMatches(p, aid)
	if removed || !errors.Is(err, errArchiveHeld) {
		t.Fatalf("held file: removed=%v err=%v", removed, err)
	}
	f.Close()
	// A wrong id is read from that same handle and stops the removal.
	var other [16]byte
	other[0] = 0xAB
	if removed, err := deleteArchiveFileIfMatches(p, other); removed || !errors.Is(err, errArchiveNotThisOne) {
		t.Fatalf("wrong id: removed=%v err=%v", removed, err)
	}
	if fileGone(p) {
		t.Fatal("the file went on a wrong id")
	}
	if removed, err := deleteArchiveFileIfMatches(p, aid); !removed || err != nil {
		t.Fatalf("own id: removed=%v err=%v", removed, err)
	}
	if !fileGone(p) {
		t.Fatal("the file is still there")
	}
}

// The purge has one trigger: the end of a successful unlock of the vault kept
// here, in one registry write, with archives.changed naming what went.
func TestPurgeRunsOnceAtTheEndOfAnUnlock(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, _ := h.newArchive("A")
	keep, _ := h.newArchive("B")
	h.c.ForgetArchive(id)
	h.editRecord(id, func(a *format.ArchiveRecord, at int64) {
		a.ForgottenAt = at - format.ForgottenRetentionSeconds - 60
	})
	// No other registry write purges.
	if e := h.c.RenameArchive(keep, "B2"); e != nil {
		t.Fatal(e)
	}
	if !h.hasRecord(id) {
		t.Fatal("a registry write that is not an unlock purged")
	}
	h.c.LockNow(ReasonManual)
	h.rec.waitState(t, StateLocked)
	h.rec.reset()
	h.unlockWithPassword()
	if h.hasRecord(id) {
		t.Fatal("the record survived the unlock's purge")
	}
	if !h.hasRecord(keep) {
		t.Fatal("a record that is not forgotten was purged")
	}
	// The purge's own announcement, wherever it lands among the unlock's
	// events: archives.changed carrying what went.
	var purged []string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && purged == nil {
		for _, ev := range h.rec.snapshot() {
			if c, ok := ev.payload.(ArchivesChanged); ok && len(c.Purged) > 0 {
				purged = c.Purged
			}
		}
		if purged == nil {
			time.Sleep(5 * time.Millisecond)
		}
	}
	if len(purged) != 1 || purged[0] != "A" {
		t.Fatalf("archives.changed carried %v", purged)
	}
}

// The retention is measured against the modified_at of the write doing the
// dropping, never a second reading of a clock (FORMAT.md §18.2).
func TestPurgeUsesTheWritesOwnModifiedAt(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	inside, _ := h.newArchive("Inside")
	outside, _ := h.newArchive("Outside")
	h.c.ForgetArchive(inside)
	h.c.ForgetArchive(outside)
	var boundary int64
	h.editRecord(inside, func(a *format.ArchiveRecord, at int64) {
		// Just inside the retention, measured against the value this write
		// itself carries.
		a.ForgottenAt = at - format.ForgottenRetentionSeconds + 60
		boundary = a.ForgottenAt
	})
	h.editRecord(outside, func(a *format.ArchiveRecord, at int64) {
		a.ForgottenAt = at - format.ForgottenRetentionSeconds - 60
	})
	before := h.status().ModifiedAt
	h.c.LockNow(ReasonManual)
	h.rec.waitState(t, StateLocked)
	h.unlockWithPassword()
	if !h.hasRecord(inside) {
		t.Fatal("a record still inside its retention was dropped")
	}
	if h.hasRecord(outside) {
		t.Fatal("a record past its retention was kept")
	}
	if got := h.record(inside).ForgottenAt; got != boundary {
		t.Fatalf("the survivor's forgotten_at moved: %d -> %d", boundary, got)
	}
	if after := h.status().ModifiedAt; after <= before {
		t.Fatalf("the purge did not write: modified_at %d -> %d", before, after)
	}
}

// Every record is left to the next unlock while the vault is Tampered.
func TestPurgeSkipsEveryRecordWhileTampered(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, _ := h.newArchive("A")
	h.c.ForgetArchive(id)
	h.editRecord(id, func(a *format.ArchiveRecord, at int64) {
		a.ForgottenAt = at - format.ForgottenRetentionSeconds - 60
	})
	h.c.mu.Lock()
	h.c.vault.tampered, h.c.vault.tamperedReason = errors.New("region hash"), CodeTamperedHash
	h.c.purgeForgottenLocked()
	h.c.mu.Unlock()
	if !h.hasRecord(id) {
		t.Fatal("the purge ran while the vault was Tampered")
	}
	h.c.mu.Lock()
	h.c.vault.tampered, h.c.vault.tamperedReason = nil, ""
	h.c.purgeForgottenLocked()
	h.c.mu.Unlock()
	if h.hasRecord(id) {
		t.Fatal("the record survived a purge on a vault that is not Tampered")
	}
}

// The unlock of a staged copy — VerifyBackup, InspectFile, InspectRecords —
// publishes nothing and therefore purges nothing, and writes nothing to the
// vault kept here.
func TestStagedCopyUnlocksPurgeNothing(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, _ := h.newArchive("A")
	backup := filepath.Join(h.dir, "backup.eks")
	if e := h.exportBackup(backup); e != nil {
		t.Fatalf("export: %v", e)
	}
	h.c.ForgetArchive(id)
	h.editRecord(id, func(a *format.ArchiveRecord, at int64) {
		a.ForgottenAt = at - format.ForgottenRetentionSeconds - 60
	})
	// InspectRecords over the same vault's backup: it reads and holds the
	// handle, and it must not purge.
	before := h.status().ModifiedAt
	handle := h.inspectRecords(backup)
	if !h.hasRecord(id) {
		t.Fatal("InspectRecords purged")
	}
	if got := h.status().ModifiedAt; got != before {
		t.Fatalf("InspectRecords wrote the vault: modified_at %d -> %d", before, got)
	}
	h.c.DiscardRecords(handle)
	if _, e := h.c.InspectFile(backup); e != nil {
		t.Fatalf("inspect file: %v", e)
	}
	if !h.hasRecord(id) {
		t.Fatal("InspectFile purged")
	}
	// VerifyBackup runs from Locked and unlocks the staged copy with the
	// recovery key.
	h.c.LockNow(ReasonManual)
	h.rec.waitState(t, StateLocked)
	locked := h.status().ModifiedAt
	h.rec.reset()
	if e := h.c.VerifyBackup(backup); e != nil {
		t.Fatalf("verify backup: %v", e)
	}
	st := h.rec.waitCeremony(t, StepRecovery, true)
	if e := h.c.SubmitSecret("recovery", st.PromptID, h.recovery); e != nil {
		t.Fatal(e)
	}
	h.rec.waitCeremony(t, StepDone, false)
	if got := h.status().ModifiedAt; got != locked {
		t.Fatalf("VerifyBackup wrote the vault: modified_at %d -> %d", locked, got)
	}
}

// List asks the file system nothing: file missing is the presence map's, and
// a record no pass has measured has no note at all.
func TestListNeverStatsAFile(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, p := h.newArchive("A")
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	h.waitPresencePass()
	log := newProbeLog()
	saved := probePath
	probePath = func(p string) error { log.note(p); return os.ErrNotExist }
	defer func() { probePath = saved }()
	list, e := h.c.ListArchives(false)
	if e != nil {
		t.Fatal(e)
	}
	if log.total() != 0 {
		t.Fatalf("List probed %d paths", log.total())
	}
	if len(list) != 1 || list[0].Note != "" {
		t.Fatalf("an unmeasured record carries a note: %+v", list)
	}
	// The pass is what fills the cell.
	if e := h.c.CheckFiles(); e != nil {
		t.Fatal(e)
	}
	h.waitPresencePass()
	list, _ = h.c.ListArchives(false)
	if list[0].Note != CodeArchiveMissing {
		t.Fatalf("after the pass: %+v", list[0])
	}
	if got := log.count(p); got != 1 {
		t.Fatalf("the pass probed %s %d times", p, got)
	}
	_ = id
}

// fakeVolumes answers for the paths a test names, and records what it was
// asked about.
type fakeVolumes struct {
	mu    sync.Mutex
	fixed map[string]bool
	asked []string
}

func (v *fakeVolumes) LocalFixed(p string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.asked = append(v.asked, p)
	return v.fixed[p]
}

func (v *fakeVolumes) questions() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]string(nil), v.asked...)
}

// waitPresencePass waits for the running pass to finish.
func (h *harness) waitPresencePass() {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		h.c.mu.Lock()
		running := h.c.presencePass
		h.c.mu.Unlock()
		if !running {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	h.t.Fatal("the presence pass did not finish")
}

// Only paths on a local fixed volume of this machine are probed: a UNC path
// is not even asked about, and a volume that is not fixed is left unmeasured
// rather than reported missing (APP.md §13).
func TestPresencePassSkipsUNCAndNonFixedVolumes(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	h.waitPresencePass()
	unc, _ := h.newArchive("Share")
	removable, _ := h.newArchive("Stick")
	_, lp := h.newArchive("Local")
	uncPath := `\\host\share\Share.enf`
	stickPath := filepath.Join(h.dir, "removable", "Stick.enf")
	h.editRecord(unc, func(a *format.ArchiveRecord, _ int64) { a.LastPath = uncPath })
	h.editRecord(removable, func(a *format.ArchiveRecord, _ int64) { a.LastPath = stickPath })
	vols := &fakeVolumes{fixed: map[string]bool{lp: true}}
	h.c.mu.Lock()
	h.c.deps.Volumes = vols
	h.c.mu.Unlock()
	log := newProbeLog()
	saved := probePath
	probePath = func(p string) error { log.note(p); return os.ErrNotExist }
	defer func() { probePath = saved }()
	if e := h.c.CheckFiles(); e != nil {
		t.Fatal(e)
	}
	h.waitPresencePass()
	if log.count(uncPath) != 0 || log.count(stickPath) != 0 {
		t.Fatal("a path off a local fixed volume was probed")
	}
	if log.count(lp) != 1 {
		t.Fatalf("the local path was probed %d times", log.count(lp))
	}
	for _, p := range vols.questions() {
		if p == uncPath {
			t.Fatal("a UNC path was put to the volume test at all")
		}
	}
	list, _ := h.c.ListArchives(true)
	for _, row := range list {
		if row.Path == lp && row.Note != CodeArchiveMissing {
			t.Fatalf("the measured row: %+v", row)
		}
		if (row.Path == uncPath || row.Path == stickPath) && row.Note != "" {
			t.Fatalf("an unmeasured row carries a note: %+v", row)
		}
	}
}

// A path that does not answer within the budget is abandoned and never
// reported missing; the pass goes on to the next path.
func TestCheckFilesAbandonsAPathOverBudget(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	h.waitPresencePass()
	slow, sp := h.newArchive("Slow")
	quick, qp := h.newArchive("Quick")
	block := make(chan struct{})
	entered := make(chan struct{}, 1)
	log := newProbeLog()
	saved := probePath
	probePath = func(p string) error {
		log.note(p)
		if p == sp {
			entered <- struct{}{}
			<-block
		}
		return nil
	}
	unblock := sync.OnceFunc(func() { close(block) })
	defer func() { probePath = saved; unblock() }()
	if e := h.c.CheckFiles(); e != nil {
		t.Fatal(e)
	}
	<-entered
	// The budget is the core's clock, armed before the question was asked.
	h.clk.Advance(presenceBudget)
	h.waitPresencePass()
	list, _ := h.c.ListArchives(false)
	for _, row := range list {
		switch row.Path {
		case sp:
			if row.Note != "" {
				t.Fatalf("an abandoned probe was reported: %+v", row)
			}
		case qp:
			if row.Note != "" {
				t.Fatalf("a path that answered was reported missing: %+v", row)
			}
		}
	}
	// The abandoned probe still holds the one probe slot, so the next path
	// is never asked while it is inside its syscall — that is what bounds
	// the leak to one goroutine (§9 Q16, amendment C).
	if n := log.count(qp); n != 0 {
		t.Fatalf("a second probe was started while the first was blocked: %s asked %d time(s)", qp, n)
	}
	// Nor does a second pass start one.
	if e := h.c.CheckFiles(); e != nil {
		t.Fatal(e)
	}
	h.waitPresencePass()
	if n := log.count(sp); n != 1 {
		t.Fatalf("a second pass asked the blocked path again: %d", n)
	}
	if n := log.count(qp); n != 0 {
		t.Fatalf("a second pass probed past the blocked path: %d", n)
	}
	// When it finally answers the slot is free and the next pass measures.
	// The abandoned goroutine is still inside its syscall when unblock
	// returns, and it hands its answer over on its own schedule: the slot is
	// free only once it has, so that is what is waited for rather than the
	// next pass being raced against it.
	unblock()
	answered := time.Now().Add(5 * time.Second)
	for {
		h.c.mu.Lock()
		free := h.c.probeOut == nil || len(h.c.probeOut) > 0
		h.c.mu.Unlock()
		if free {
			break
		}
		if time.Now().After(answered) {
			t.Fatal("the abandoned probe never answered")
		}
		time.Sleep(2 * time.Millisecond)
	}
	if e := h.c.CheckFiles(); e != nil {
		t.Fatal(e)
	}
	h.waitPresencePass()
	if n := log.count(qp); n != 1 {
		t.Fatalf("the path was not measured once the slot was free: %d", n)
	}
	_, _ = slow, quick
}

// Details is one record read whole, refused while the vault is locked.
func TestDetailsIsRefusedWhileLocked(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, p := h.newArchive("A")
	h.c.SetArchiveDescription(id, "the photos")
	d, e := h.c.ArchiveDetails(id)
	if e != nil {
		t.Fatal(e)
	}
	if d.Name != "A" || d.Description != "the photos" || d.LastPath != p {
		t.Fatalf("details: %+v", d)
	}
	if len(d.Versions) != 1 || d.Versions[0].State != "current" || d.Versions[0].KID != d.CurrentKID {
		t.Fatalf("versions: %+v", d.Versions)
	}
	h.c.LockNow(ReasonManual)
	h.rec.waitState(t, StateLocked)
	if _, e := h.c.ArchiveDetails(id); !isCode(e, CodeVaultLocked) {
		t.Fatalf("details while locked: %v", e)
	}
}

// Nothing in ArchiveDetails is key material: the guard is the type itself, so
// a field added later has to be looked at (APP.md §1's boundary).
func TestDetailsCarriesNoKeyMaterial(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, _ := h.newArchive("A")
	d, e := h.c.ArchiveDetails(id)
	if e != nil {
		t.Fatal(e)
	}
	banned := []string{"key", "nonce", "wrap", "offset", "secret", "vmk", "kwk"}
	for _, f := range fieldNames(d) {
		low := strings.ToLower(f)
		for _, b := range banned {
			if strings.Contains(low, b) {
				t.Errorf("ArchiveDetails.%s looks like key material", f)
			}
		}
	}
	for _, f := range fieldNames(VersionView{}) {
		low := strings.ToLower(f)
		for _, b := range banned {
			if strings.Contains(low, b) {
				t.Errorf("VersionView.%s looks like key material", f)
			}
		}
	}
	// The record's own wrapped key never leaves the registry.
	rec := h.record(id)
	if strings.Contains(strings.ToLower(d.CurrentKID), "0000000000000000000000000000000000") {
		t.Fatal("current kid is empty")
	}
	if hexID(rec.Versions[0].KID) != d.Versions[0].KID {
		t.Fatal("the version list is not the record's")
	}
}

// The description is bounded in bytes, not characters, and empty clears it.
func TestSetDescriptionBounds(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, _ := h.newArchive("A")
	if e := h.c.SetArchiveDescription(id, strings.Repeat("a", format.MaxDescriptionLen)); e != nil {
		t.Fatalf("1024 bytes: %v", e)
	}
	if e := h.c.SetArchiveDescription(id, strings.Repeat("a", format.MaxDescriptionLen+1)); !isCode(e, CodeDescriptionLong) {
		t.Fatalf("1025 bytes: %v", e)
	}
	// 400 four-byte runes are under 1 024 characters and over 1 024 bytes.
	if e := h.c.SetArchiveDescription(id, strings.Repeat("😀", 400)); !isCode(e, CodeDescriptionLong) {
		t.Fatalf("400 emoji: %v", e)
	}
	if e := h.c.SetArchiveDescription(id, "\xff\xfe"); !isCode(e, CodeDescriptionLong) {
		t.Fatalf("invalid UTF-8: %v", e)
	}
	if e := h.c.SetArchiveDescription(id, ""); e != nil {
		t.Fatalf("clearing: %v", e)
	}
	if got := h.record(id).Description; got != "" {
		t.Fatalf("description after clearing: %q", got)
	}
	list, _ := h.c.ListArchives(false)
	if list[0].Description != "" {
		t.Fatalf("row: %+v", list[0])
	}
	h.c.SetArchiveDescription(id, "second line")
	list, _ = h.c.ListArchives(false)
	if list[0].Description != "second line" {
		t.Fatalf("row: %+v", list[0])
	}
}

// Rename is bounded app-side: empty, over 1 024 bytes or not UTF-8 is
// refused, because the confirmation for Forget and Delete is the name typed
// (§9 Q14).
func TestRenameBounds(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, _ := h.newArchive("A")
	if e := h.c.RenameArchive(id, ""); !isCode(e, CodeArchiveName) {
		t.Fatalf("empty: %v", e)
	}
	if e := h.c.RenameArchive(id, strings.Repeat("n", maxNameLen+1)); !isCode(e, CodeArchiveName) {
		t.Fatalf("too long: %v", e)
	}
	if e := h.c.RenameArchive(id, "\xff"); !isCode(e, CodeArchiveName) {
		t.Fatalf("invalid UTF-8: %v", e)
	}
	if e := h.c.RenameArchive(id, strings.Repeat("n", maxNameLen)); e != nil {
		t.Fatalf("1024 bytes: %v", e)
	}
	if e := h.c.RenameArchive(id, "Photos"); e != nil {
		t.Fatal(e)
	}
	if got := h.record(id).Name; got != "Photos" {
		t.Fatalf("name: %q", got)
	}
}

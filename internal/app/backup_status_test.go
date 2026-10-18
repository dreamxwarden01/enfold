package app

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dreamxwarden01/enfold/internal/format"
)

// VaultStatus names the archives open in this process, sorted by name
// (APP.md 2.3, direction 9): after a lock each is still decrypted and still
// saves, so the lock screen and the banners say which. The list follows a
// rename and a close, survives the lock, and is never null.
func TestStatusNamesTheOpenArchives(t *testing.T) {
	h := newHarness(t, nil, nil)
	if st := h.status(); st.Opened == nil || len(st.Opened) != 0 || st.OpenArchives != 0 {
		t.Fatalf("nothing open: %+v / %d", st.Opened, st.OpenArchives)
	}
	b, err := json.Marshal(h.status())
	if err != nil || !strings.Contains(string(b), `"opened":[]`) {
		t.Fatalf("an empty list crosses as []: %s %v", b, err)
	}

	h.unlockWithPassword()
	idB, _ := h.newArchive("beta")
	idA, _ := h.newArchive("Alpha")
	h.newArchive("Gamma") // in the registry, never opened
	for _, id := range []string{idB, idA} {
		if _, e := h.c.OpenArchive(id); e != nil {
			t.Fatalf("open: %v", e)
		}
	}
	want := func(when string, refs ...ArchiveRef) {
		t.Helper()
		st := h.status()
		if st.OpenArchives != len(refs) || len(st.Opened) != len(refs) {
			t.Fatalf("%s: %d open, %+v; want %+v", when, st.OpenArchives, st.Opened, refs)
		}
		for i := range refs {
			if st.Opened[i] != refs[i] {
				t.Fatalf("%s: %+v, want %+v", when, st.Opened, refs)
			}
		}
	}
	// By name, case aside: "Alpha" before "beta".
	want("both open", ArchiveRef{idA, "Alpha"}, ArchiveRef{idB, "beta"})

	// A rename of an open archive is announced on the state, so a lock
	// screen drawn from the event names it as it is now.
	h.rec.reset()
	if e := h.c.RenameArchive(idB, "Aardvark"); e != nil {
		t.Fatal(e)
	}
	h.rec.waitFor(t, EventVaultState, func(p any) bool {
		st, ok := p.(VaultStatus)
		return ok && len(st.Opened) == 2 && st.Opened[0].Name == "Aardvark"
	})
	want("renamed", ArchiveRef{idB, "Aardvark"}, ArchiveRef{idA, "Alpha"})

	// The page's archives stay open after a lock (APP.md 2.3), and so do
	// their names here.
	h.c.Lock()
	h.rec.waitState(t, StateLocked)
	want("locked", ArchiveRef{idB, "Aardvark"}, ArchiveRef{idA, "Alpha"})

	if e := h.c.CloseArchive(idB); e != nil {
		t.Fatal(e)
	}
	want("one closed", ArchiveRef{idA, "Alpha"})
}

// BackupStatus counts what the last backup lacks — archives created after
// it, and key versions created after it on the archives it holds — against
// created_at, never modified_at (APP.md 13, direction 9). The counts are
// an unlocked registry's: Locked and Tampered answer them as 0 and keep At.
func TestBackupStatusCountsWhatTheBackupLacks(t *testing.T) {
	h := newHarness(t, nil, nil)
	if got := h.c.BackupStatus(); got != (BackupStatus{}) {
		t.Fatalf("locked, never backed up: %+v", got)
	}
	h.unlockWithPassword()
	idA, _ := h.newArchive("A")
	h.newArchive("B")

	if got := h.c.BackupStatus(); got != (BackupStatus{At: 0, Archives: 2, Missing: 2}) {
		t.Fatalf("never backed up: %+v", got)
	}

	if e := h.exportBackup(filepath.Join(t.TempDir(), "backup.eks")); e != nil {
		t.Fatalf("export: %v", e)
	}
	at := h.clk.Now().Unix()
	if got := h.c.BackupStatus(); got != (BackupStatus{At: at, Archives: 2}) {
		t.Fatalf("a backup after both: %+v", got)
	}

	// A save moves modified_at and the record's last_written_at, and is
	// not something the backup lacks.
	h.clk.Advance(time.Minute)
	h.editRecord(idA, func(a *format.ArchiveRecord, modifiedAt int64) { a.LastWrittenAt = modifiedAt })
	if got := h.c.BackupStatus(); got != (BackupStatus{At: at, Archives: 2}) {
		t.Fatalf("after a save: %+v", got)
	}

	idC, _ := h.newArchive("C")
	if got := h.c.BackupStatus(); got != (BackupStatus{At: at, Archives: 3, Missing: 1}) {
		t.Fatalf("one created after the backup: %+v", got)
	}

	// A re-key after the backup: the backup holds A's old key alone.
	opID, e := h.c.RotateKey(idA)
	if e != nil {
		t.Fatal(e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != "" {
		t.Fatalf("rotate: %+v", o)
	}
	// And one of C, which the backup lacks whole: counted once, as missing.
	opID, e = h.c.RotateKey(idC)
	if e != nil {
		t.Fatal(e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != "" {
		t.Fatalf("rotate: %+v", o)
	}
	if got := h.c.BackupStatus(); got != (BackupStatus{At: at, Archives: 3, Missing: 1, Rekeyed: 1}) {
		t.Fatalf("a re-key after the backup: %+v", got)
	}

	// A second re-key of A: still one archive re-keyed since, not two — the
	// card counts archives, not key versions.
	opID, e = h.c.RotateKey(idA)
	if e != nil {
		t.Fatal(e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != "" {
		t.Fatalf("rotate again: %+v", o)
	}
	if got := h.c.BackupStatus(); got != (BackupStatus{At: at, Archives: 3, Missing: 1, Rekeyed: 1}) {
		t.Fatalf("A re-keyed twice after the backup: %+v", got)
	}

	// A forgotten record is not counted: nothing is asked of it.
	if e := h.c.ForgetArchive(idC); e != nil {
		t.Fatal(e)
	}
	if got := h.c.BackupStatus(); got != (BackupStatus{At: at, Archives: 2, Rekeyed: 1}) {
		t.Fatalf("C forgotten: %+v", got)
	}

	// Tampered: the registry may not be the vault's own, so no count.
	h.c.mu.Lock()
	h.c.vault.tampered, h.c.vault.tamperedReason = errors.New("region hash"), CodeTamperedHash
	h.c.mu.Unlock()
	if got := h.c.BackupStatus(); got != (BackupStatus{At: at}) {
		t.Fatalf("tampered: %+v", got)
	}
	h.c.mu.Lock()
	h.c.vault.tampered, h.c.vault.tamperedReason = nil, ""
	h.c.mu.Unlock()

	// Locked: only an unlocked registry is read; the date stands.
	h.c.Lock()
	h.rec.waitState(t, StateLocked)
	if got := h.c.BackupStatus(); got != (BackupStatus{At: at}) {
		t.Fatalf("locked: %+v", got)
	}
}

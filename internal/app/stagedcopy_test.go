package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The staged copy of an imported backup (the 2026-11-06 outside audit's
// second pass, U2). An import of a backup whose first way in is a YubiKey
// holds the staged copy open from the recovery key's proof to the end;
// the new YubiKey's proof (proveKey) runs as an attempt that holds only
// the card. Cancelled there, that attempt is the pending touch, but the
// handle is still the import's: the import closes it and removes the copy
// after the close, since Windows refuses to delete a file still open. The
// copy is never left to the next start's sweep, and every other end of an
// import still removes it — or adopts it, on success.

// backupImport is a backup of a YubiKey vault and a fresh core, with a new
// YubiKey in its reader, to import the backup into.
type backupImport struct {
	t      *testing.T
	backup string
	digits string
	data   string
	card   *fakeCard
	c      *Core
	rec    *recorder
}

func newBackupImport(t *testing.T) *backupImport {
	t.Helper()
	card := newFakeCard("123456")
	pub := card.addKey(0x9d, true)
	cards := &fakeCards{card: card}
	cards.setReaders("Yubico A")
	h := newHarnessEntangled(t, cards, pub, "the source vault's password")
	backup := filepath.Join(h.dir, "backup.eks")
	exportBackupWithRecovery(t, h.vault, backup, h.recovery)
	second := newFakeCard("654321")
	second.addKey(0x9d, true)
	failResets(second)
	cards2 := &fakeCards{card: second}
	cards2.setReaders("Yubico B")
	data := t.TempDir()
	c, rec := freshCoreWithCards(t, data, cards2)
	return &backupImport{t: t, backup: backup, digits: digits(h.recovery), data: data, card: second, c: c, rec: rec}
}

// begin starts the import and proves it with the recovery key; it returns
// at the new YubiKey's PIN.
func (b *backupImport) begin() CeremonyState {
	b.t.Helper()
	b.rec.reset()
	if e := b.c.ImportFile(b.backup, "Restored", MethodRecovery, EnrollOptions{Kind: EnrollToken, Label: "New key"}, false); e != nil {
		b.t.Fatal(e)
	}
	r := b.rec.waitCeremony(b.t, StepRecovery, true)
	if e := b.c.SubmitSecret("recovery", r.PromptID, b.digits); e != nil {
		b.t.Fatal(e)
	}
	return b.rec.waitCeremony(b.t, StepPIN, true)
}

// settled waits for the import to have ended and no touch to be pending.
func (b *backupImport) settled() {
	b.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		st := b.c.Status()
		if st.Ceremony == nil && !st.PendingTouch {
			return
		}
		if time.Now().After(deadline) {
			b.t.Fatalf("the import never settled: %+v", st)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// noStagedCopy fails the test when a staged copy is in the data folder:
// checked at once, since the import removes it before the ceremony ends.
func (b *backupImport) noStagedCopy(what string) {
	b.t.Helper()
	entries, err := os.ReadDir(b.data)
	if err != nil {
		b.t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), incomingPrefix) {
			b.t.Fatalf("%s: the staged copy was left behind: %s", what, e.Name())
		}
	}
}

// A cancel at the new YubiKey's PIN ends its proof as the pending touch;
// the import closes the staged copy and removes it, ten times in a row.
func TestABackupImportCancelledAtTheNewKeysPINLeavesNoCopy(t *testing.T) {
	b := newBackupImport(t)
	for i := range 10 {
		b.begin()
		if e := b.c.CancelUnlock(); e != nil {
			t.Fatal(e)
		}
		b.settled()
		b.noStagedCopy(fmt.Sprintf("cancelled at the new YubiKey's PIN, run %d", i))
		cardBalanced(t, "the new YubiKey", b.card)
	}
}

// Every other end of an import of a backup: the copy is removed, or, on
// success, installed as the vault.
func TestEveryEndOfABackupImportRemovesTheCopy(t *testing.T) {
	ends := []struct {
		name string
		run  func(b *backupImport)
	}{
		{"cancelled at the recovery key", func(b *backupImport) {
			b.rec.reset()
			if e := b.c.ImportFile(b.backup, "Restored", MethodRecovery, EnrollOptions{Kind: EnrollToken, Label: "New key"}, false); e != nil {
				b.t.Fatal(e)
			}
			b.rec.waitCeremony(b.t, StepRecovery, true)
			if e := b.c.CancelUnlock(); e != nil {
				b.t.Fatal(e)
			}
		}},
		{"locked at the new YubiKey's PIN", func(b *backupImport) {
			b.begin()
			b.c.LockNow(ReasonManual)
		}},
		{"cancelled at the new YubiKey's touch", func(b *backupImport) {
			b.card.mu.Lock()
			b.card.holdTouch = true
			b.card.mu.Unlock()
			pin := b.begin()
			if e := b.c.SubmitSecret("pin", pin.PromptID, "654321"); e != nil {
				b.t.Fatal(e)
			}
			b.rec.waitCeremony(b.t, StepTouch, false)
			if e := b.c.CancelUnlock(); e != nil {
				b.t.Fatal(e)
			}
			b.waitStatus(func(s VaultStatus) bool { return s.Ceremony == nil })
			b.noStagedCopy("the touch still pending")
			b.card.press()
		}},
		{"failed: the new YubiKey's proof refused", func(b *backupImport) {
			b.card.mu.Lock()
			b.card.proofLies = true
			b.card.mu.Unlock()
			pin := b.begin()
			if e := b.c.SubmitSecret("pin", pin.PromptID, "654321"); e != nil {
				b.t.Fatal(e)
			}
			b.rec.waitCeremony(b.t, StepFailed, false)
			if e := b.c.CancelUnlock(); e != nil {
				b.t.Fatal(e)
			}
		}},
	}
	for _, end := range ends {
		t.Run(end.name, func(t *testing.T) {
			b := newBackupImport(t)
			b.t = t
			end.run(b)
			b.settled()
			b.noStagedCopy(end.name)
			cardBalanced(t, "the new YubiKey", b.card)
			if s := b.c.Status(); s.State != StateNone {
				t.Fatalf("after the end the vault is %s", s.State)
			}
		})
	}
	t.Run("installed", func(t *testing.T) {
		b := newBackupImport(t)
		pin := b.begin()
		if e := b.c.SubmitSecret("pin", pin.PromptID, "654321"); e != nil {
			t.Fatal(e)
		}
		b.rec.waitCeremony(t, StepDone, false)
		b.settled()
		b.noStagedCopy("installed")
		cardBalanced(t, "the new YubiKey", b.card)
		if s := b.c.Status(); s.State != StateLocked {
			t.Fatalf("after the install the vault is %s", s.State)
		}
	})
}

// waitStatus waits for the status to satisfy pred.
func (b *backupImport) waitStatus(pred func(VaultStatus) bool) {
	b.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !pred(b.c.Status()) {
		if time.Now().After(deadline) {
			b.t.Fatalf("the status never matched: %+v", b.c.Status())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

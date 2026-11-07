package app

import (
	"crypto/elliptic"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dreamxwarden01/enfold/internal/format"
	"github.com/dreamxwarden01/enfold/internal/kdf"
	"github.com/dreamxwarden01/enfold/internal/keystore"
)

// The repairs of the switch audit of 2026-11-07: nothing is written to a
// YubiKey unless the vault, asked from the handle the proof opened, will
// accept it (findings 1 and 3), and the keys Enfold generated belong to one
// vault and one unlock (finding 2). The fake cards stand in for the
// YubiKeys; nothing here opens a real one. None runs in parallel.

// madeNow is how many serials made lists, and its epoch.
func (h *harness) madeNow() (int, uint64) {
	h.c.mu.Lock()
	defer h.c.mu.Unlock()
	return len(h.c.vault.made), h.c.vault.madeEpoch
}

// staleCacheWithoutPassword makes the cached slots forget the vault's
// standalone password, as a cache read before another act added one would.
func (h *harness) staleCacheWithoutPassword() {
	h.c.mu.Lock()
	h.c.vault.slots = slices.DeleteFunc(slices.Clone(h.c.vault.slots), func(s keystore.SlotInfo) bool {
		return s.Type == format.SlotStandalonePassword
	})
	h.c.vault.switchReason = CodeSlotNotFound
	h.c.mu.Unlock()
}

// F1: BeginEnroll(token) passes its cached check on a vault whose cache
// does not show its password; the handle the proof opened refuses it with
// vault.slot_policy, and no card is opened.
func TestEnrollTokenStaleCacheIsRefusedFromTheHandle(t *testing.T) {
	card, _ := cardWithKey("123456")
	h, cards := switchHarness(t, card)
	h.staleCacheWithoutPassword()
	if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollToken, Label: "Desk YubiKey"}); e != nil {
		t.Fatalf("the cached check: %v", e)
	}
	h.answerRecovery(h.recovery)
	st := h.end()
	if st.Step != StepFailed || st.Error != CodeSlotPolicy {
		t.Fatalf("the end: %+v", st)
	}
	if n := cards.openAttempts(); n != 0 {
		t.Fatalf("%d card opens", n)
	}
	h.lockAndSettle() // the lock reads the facts again from the file
	h.stillAPasswordVault("after the refusal")
}

// anotherProcess opens the vault file at path with its recovery key, as
// another Enfold would while this one is Locked, and runs change on it.
func anotherProcess(t *testing.T, path, recovery string, change func(u *keystore.Unlocked) error) {
	t.Helper()
	rk, err := kdf.ParseRecoveryDigits(recovery)
	if err != nil {
		t.Fatal(err)
	}
	ks, err := keystore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ks.Close()
	u, err := ks.Unlock(keystore.RecoveryCredential{Key: rk})
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	if err := change(u); err != nil {
		t.Fatal(err)
	}
}

// F1: Finish setting up from a cache read while Locked, the file changed by
// another process since — a password added, or its password turned on —
// is refused from the file the recovery key opened, before any card work.
func TestFinishSetupStaleCacheIsRefusedFromTheFile(t *testing.T) {
	for _, c := range []struct {
		name   string
		change func(u *keystore.Unlocked) error
		want   Code
	}{
		{"a password added", func(u *keystore.Unlocked) error {
			return u.AddSlot(keystore.PasswordSlot{Password: "another process's words", Argon2: fast, Label: "Password"})
		}, CodeSlotPolicy},
		{"its password turned on", func(u *keystore.Unlocked) error {
			return u.SetEntangled(true, &keystore.Entangle{Password: "another process's words", Argon2: fast})
		}, CodeSetupPasswordOn},
	} {
		t.Run(c.name, func(t *testing.T) {
			h, cards := lostOnlyHarness(t, "")
			if st := h.status(); !st.SetupNeeded || st.Entangled {
				t.Fatalf("the lock screen: %+v", st)
			}
			anotherProcess(t, h.vault, h.recovery, c.change)
			opens := cards.openAttempts()
			h.rec.reset()
			if e := h.c.FinishSetup(EnrollOptions{Kind: EnrollToken, Label: "First"}); e != nil {
				t.Fatalf("the cached check: %v", e)
			}
			h.answerRecovery(h.recovery)
			st := h.end()
			if st.Step != StepFailed || st.Error != c.want {
				t.Fatalf("the end: %+v", st)
			}
			if n := cards.openAttempts() - opens; n != 0 {
				t.Fatalf("%d card opens", n)
			}
		})
	}
}

// F3: a label over the house's bound or the format's is refused at every
// entrance, before any ceremony and any card; a full slot region is refused
// from the handle after the proof, before any card.
func TestALabelAndAFullRegionAreRefusedBeforeAnyCard(t *testing.T) {
	card, _ := cardWithKey("123456")
	h, cards := switchHarness(t, card)
	labels := []string{strings.Repeat("x", keystore.MaxLabelChars+1), strings.Repeat("x", format.MaxLabelBytes+1)}
	mark := h.mark()
	for _, label := range labels {
		for what, e := range map[string]*Error{
			"switch":            h.c.BeginSwitchToToken(SwitchOptions{Label: label, KeepPassword: true}),
			"enrol a YubiKey":   h.c.BeginEnroll(EnrollOptions{Kind: EnrollToken, Label: label}),
			"enrol a recovery":  h.c.BeginEnroll(EnrollOptions{Kind: EnrollRecovery, Label: label}),
			"enrol a password":  h.c.BeginEnroll(EnrollOptions{Kind: EnrollPassword, Label: label}),
			"finish setting up": h.c.FinishSetup(EnrollOptions{Kind: EnrollToken, Label: label}),
		} {
			if !isCode(e, CodeParams) {
				t.Fatalf("%s with a label of %d bytes: %v", what, len(label), e)
			}
		}
	}
	h.noCeremonySince(mark, "the refused labels")
	if n := cards.openAttempts(); n != 0 {
		t.Fatalf("%d card opens", n)
	}
	// With no vault kept, a creation and an import reach their own label
	// check, past the gates a vault kept here answers first.
	c, rec := freshCoreWithCards(t, t.TempDir(), cards)
	src := newHarness(t, nil, nil)
	backup := filepath.Join(src.dir, "backup.eks")
	exportBackupWithRecovery(t, src.vault, backup, src.recovery)
	rec.reset()
	for _, label := range labels {
		if e := c.CreateVault(filepath.Join(t.TempDir(), "new.eks"), "", EnrollOptions{Kind: EnrollToken, Label: label}, false); !isCode(e, CodeParams) {
			t.Fatalf("create with a label of %d bytes: %v", len(label), e)
		}
		if e := c.ImportFile(backup, "", MethodRecovery, EnrollOptions{Kind: EnrollToken, Label: label}, false); !isCode(e, CodeParams) {
			t.Fatalf("import with a label of %d bytes: %v", len(label), e)
		}
	}
	if n := len(rec.snapshot()); n != 0 {
		t.Fatalf("%d events from the refused creation and import", n)
	}

	// A region of 32 records: the YubiKey is refused after the recovery
	// key's proof, from the handle, with no card opened.
	specs := make([]keystore.SlotSpec, format.MaxSlots)
	var first string
	for i := range specs {
		r, d := newRecoverySpec(t, "paper")
		specs[i] = r
		if i == 0 {
			first = d
		}
	}
	full := rekeyHarness(t, cards, "", first, specs...)
	full.unlockWithRecovery(first)
	opens := cards.openAttempts()
	full.rec.reset()
	if e := full.c.BeginEnroll(EnrollOptions{Kind: EnrollToken}); e != nil {
		t.Fatalf("the cached check: %v", e)
	}
	full.answerRecovery(first)
	st := full.end()
	if st.Step != StepFailed || st.Error != CodeSlotsFull {
		t.Fatalf("the end: %+v", st)
	}
	if n := cards.openAttempts() - opens; n != 0 {
		t.Fatalf("%d card opens", n)
	}
}

// generatingCard is a YubiKey whose 9d holds a key Enfold cannot use, so an
// enrolment generates a new key, its management key behind its PIN.
func generatingCard() *fakeCard {
	card := newFakeCard("123456")
	card.mgmt = make([]byte, 24)
	card.addKey(0x9d, false)
	return card
}

// F2: a lock that lands while a generation finishes leaves made empty — the
// key the card returns afterwards is not remembered — so the next unlock
// generates its own key rather than reuse the last unlock's.
func TestALockDuringAGenerationLeavesNothingRemembered(t *testing.T) {
	card := generatingCard()
	reached, release := make(chan struct{}), make(chan struct{})
	var held atomic.Bool
	card.onGenerate = func() {
		if held.CompareAndSwap(false, true) {
			close(reached)
			<-release
		}
	}
	h, _ := switchHarness(t, card)
	_, epoch := h.madeNow()
	if e := h.c.BeginSwitchToToken(SwitchOptions{KeepPassword: true}); e != nil {
		t.Fatal(e)
	}
	h.proveWithPassword()
	h.answerPIN("123456") // the management key's PIN
	select {
	case <-reached:
	case <-time.After(10 * time.Second):
		t.Fatal("the generation was never reached")
	}
	h.c.Lock() // lands while the key is made on the card and not yet returned
	close(release)
	if st := h.ended(); st.Step != StepFailed || st.Error != CodeCancelled {
		t.Fatalf("the end: %+v", st)
	}
	h.settleLock()
	if n, e := h.madeNow(); n != 0 || e == epoch {
		t.Fatalf("after the lock: made lists %d serials, epoch %d (was %d)", n, e, epoch)
	}
	if n := card.keyCount(); n != 2 {
		t.Fatalf("%d keys on the card", n)
	}

	// The next unlock: the key the previous one made is not reused.
	h.unlockWithPassword()
	h.rec.reset()
	if e := h.c.BeginSwitchToToken(SwitchOptions{KeepPassword: true}); e != nil {
		t.Fatal(e)
	}
	h.proveWithPassword()
	h.answerPIN("123456")
	if st := h.ended(); st.Step != StepDone || !st.Generated {
		t.Fatalf("the next unlock's act: %+v", st)
	}
	if n := card.keyCount(); n != 3 {
		t.Fatalf("%d keys on the card: the previous unlock's key was reused", n)
	}
}

// F2: opening another vault drops made and moves the epoch; opening the
// same one again does not.
func TestOpeningAnotherVaultForgetsTheKeysMade(t *testing.T) {
	h := newHarness(t, nil, nil)
	other := newHarness(t, nil, nil)
	h.c.mu.Lock()
	scope := madeScope{vault: h.c.vault.vaultID, epoch: h.c.vault.madeEpoch}
	h.c.vault.made, h.c.vault.madeScope = map[uint32][][]byte{1: {[]byte("a key")}}, scope
	h.c.mu.Unlock()
	if err := h.c.openVaultFile(h.vault, "Test vault"); err != nil {
		t.Fatal(err)
	}
	if n, e := h.madeNow(); n != 1 || e != scope.epoch {
		t.Fatalf("the same vault again: %d serials, epoch %d", n, e)
	}
	if err := h.c.openVaultFile(other.vault, "Other vault"); err != nil {
		t.Fatal(err)
	}
	if n, e := h.madeNow(); n != 0 || e == scope.epoch {
		t.Fatalf("another vault: %d serials, epoch %d", n, e)
	}
}

// importBackupWith imports the backup at path into c with a YubiKey as its
// first way in, proved by digits, and returns the import's last state.
func importBackupWith(t *testing.T, c *Core, rec *recorder, path, digits string, pin string) CeremonyState {
	t.Helper()
	rec.reset()
	if e := c.ImportFile(path, "Restored", MethodRecovery, EnrollOptions{Kind: EnrollToken}, true); e != nil {
		t.Fatal(e)
	}
	r := rec.waitCeremony(t, StepRecovery, true)
	if e := c.SubmitSecret("recovery", r.PromptID, digits); e != nil {
		t.Fatal(e)
	}
	p := rec.waitCeremony(t, StepPIN, true)
	if e := c.SubmitSecret("pin", p.PromptID, pin); e != nil {
		t.Fatal(e)
	}
	st := rec.waitFor(t, EventVaultCeremony, func(x any) bool {
		s, ok := x.(CeremonyState)
		return ok && s.Kind == "import" && (s.Step == StepDone || s.Step == StepFailed)
	}).(CeremonyState)
	if st.Step == StepFailed {
		c.CancelUnlock() // a failure parks until the page closes it
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if s := c.Status(); s.Ceremony == nil && !s.PendingTouch {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("the import never settled: %+v", c.Status())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// F2: a key generated for one backup's first way in is not reused for
// another vault's: backup A's import makes a key and fails, backup B's
// generates its own; then B's commit, and the open of B, leave made empty.
func TestAKeyMadeForOneBackupIsNotAnothersFirstWayIn(t *testing.T) {
	a, b := newHarness(t, nil, nil), newHarness(t, nil, nil)
	backupA, backupB := filepath.Join(a.dir, "a.eks"), filepath.Join(b.dir, "b.eks")
	exportBackupWithRecovery(t, a.vault, backupA, a.recovery)
	exportBackupWithRecovery(t, b.vault, backupB, b.recovery)
	card := generatingCard()
	card.proofLies = true
	cards := readerWith(card)
	c, rec := freshCoreWithCards(t, t.TempDir(), cards)
	if st := importBackupWith(t, c, rec, backupA, digits(a.recovery), "123456"); st.Step != StepFailed || st.Error != CodeTokenProof || !st.Generated {
		t.Fatalf("backup A: %+v", st)
	}
	c.mu.Lock()
	n, scope := len(c.vault.made), c.vault.madeScope
	c.mu.Unlock()
	if n != 1 || scope.vault == ([16]byte{}) {
		t.Fatalf("after backup A: %d serials, scope %x", n, scope.vault)
	}
	card.mu.Lock()
	card.proofLies = false
	card.mu.Unlock()
	if st := importBackupWith(t, c, rec, backupB, digits(b.recovery), "123456"); st.Step != StepDone || !st.Generated {
		t.Fatalf("backup B: %+v", st)
	}
	if k := card.keyCount(); k != 3 {
		t.Fatalf("%d keys on the card: backup A's key was reused for backup B", k)
	}
	c.mu.Lock()
	n = len(c.vault.made)
	c.mu.Unlock()
	if n != 0 {
		t.Fatalf("after backup B: made lists %d serials", n)
	}
}

// F2: Finish setting up's commit forgets the key it enrolled, as landChange
// does after every other commit; within one unlock the ordinary reuse still
// works (TestSwitchToTokenReusesTheKeyItGenerated).
func TestFinishSetupForgetsTheKeyItEnrolled(t *testing.T) {
	h, cards := lostOnlyHarness(t, "")
	card := generatingCard()
	cards.setCard(card)
	h.rec.reset()
	if e := h.c.FinishSetup(EnrollOptions{Kind: EnrollToken, Label: "First"}); e != nil {
		t.Fatal(e)
	}
	h.answerRecovery(h.recovery)
	h.answerPIN("123456")
	if st := h.done(); !st.Generated {
		t.Fatalf("the end: %+v", st)
	}
	h.rec.waitState(t, StateUnlocked)
	if n, _ := h.madeNow(); n != 0 {
		t.Fatalf("made lists %d serials after the setup enrolled its key", n)
	}
}

// The second pass of the same audit (2026-11-07): Broken ends an unlock's
// memory of the keys generated, as a lock does (finding 1), and a Busy open
// of the same vault does not (finding 4); a YubiKey's default name is
// measured with its serial when the room is borderline (finding 2); and the
// stand-in's placeholder key is no identity (finding 3).

// noCeremony waits for the core to hold no ceremony.
func (h *harness) noCeremony() {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		h.c.mu.Lock()
		gone := h.c.cer == nil
		h.c.mu.Unlock()
		if gone {
			return
		}
		if time.Now().After(deadline) {
			h.t.Fatal("the act did not end")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// lyingProof makes card's proof lie, or tell the truth again.
func lyingProof(card *fakeCard, lies bool) {
	card.mu.Lock()
	card.proofLies = lies
	card.mu.Unlock()
}

// Finding 1: a commit whose outcome is unknown enters Broken with a key of
// this unlock remembered; Broken drops it and moves the epoch, so after
// Reopen and a new unlock the enrolment generates its own key.
func TestBrokenForgetsTheKeysMade(t *testing.T) {
	card := generatingCard()
	cards := readerWith(card)
	r1, first := newRecoverySpec(t, "paper")
	r2, _ := newRecoverySpec(t, "paper 2")
	h := rekeyHarness(t, cards, "", first, r1, r2)
	h.unlockWithRecovery(first)
	lyingProof(card, true)
	if st := enrolOnRecoveryVault(t, h, first); st.Step != StepFailed || st.Error != CodeTokenProof || !st.Generated {
		t.Fatalf("the enrolment that generates: %+v", st)
	}
	lyingProof(card, false)

	// The act begun again reuses the key (the same unlock), and its
	// commit's outcome is unknown: Broken.
	h.c.seams.failAt = func(point string) error {
		if point == "commit" {
			return keystore.ErrIndeterminate
		}
		return nil
	}
	_, epoch := h.madeNow()
	if st := enrolOnRecoveryVault(t, h, first); st.Step != StepFailed || st.Error != CodeIndeterminate || !st.Generated {
		t.Fatalf("the enrolment whose commit is unknown: %+v", st)
	}
	if n := card.keyCount(); n != 2 {
		t.Fatalf("%d keys on the card: the key of this unlock was not reused", n)
	}
	if s := h.status().State; s != StateBroken {
		t.Fatalf("state %s", s)
	}
	if n, e := h.madeNow(); n != 0 || e == epoch {
		t.Fatalf("Broken left made with %d serials, epoch %d (was %d)", n, e, epoch)
	}
	h.c.seams.failAt = nil
	if e := h.c.Reopen(); e != nil {
		t.Fatal(e)
	}
	h.unlockWithRecovery(first)
	if st := enrolOnRecoveryVault(t, h, first); st.Step != StepDone || !st.Generated {
		t.Fatalf("the enrolment after Broken: %+v", st)
	}
	if n := card.keyCount(); n != 3 {
		t.Fatalf("%d keys on the card: the unlock before Broken lent its key", n)
	}
}

// finishSetupFails runs Finish setting up on h with the card's proof lying,
// so that the act generates a key for the file's first way in (or reuses
// the one remembered) and fails with it remembered.
func finishSetupFails(t *testing.T, h *harness, card *fakeCard) {
	t.Helper()
	lyingProof(card, true)
	defer lyingProof(card, false)
	h.rec.reset()
	if e := h.c.FinishSetup(EnrollOptions{Kind: EnrollToken, Label: "First"}); e != nil {
		t.Fatal(e)
	}
	h.answerRecovery(h.recovery)
	h.answerPIN("123456")
	if st := h.end(); st.Step != StepFailed || st.Error != CodeTokenProof || !st.Generated {
		t.Fatalf("the setup that generates: %+v", st)
	}
	h.c.CancelUnlock() // a failure parks until the page closes it
	h.noCeremony()
}

// busyDetour has another process hold the vault file while Reopen runs
// (Busy), then reopens it once released.
func busyDetour(t *testing.T, h *harness) {
	t.Helper()
	held, err := keystore.Open(h.vault)
	if err != nil {
		t.Fatal(err)
	}
	if e := h.c.Reopen(); !isCode(e, CodeVaultBusy) {
		held.Close()
		t.Fatalf("a reopen of a file held elsewhere: %v", e)
	}
	held.Close()
	if st := h.status(); st.State != StateBusy {
		t.Fatalf("the busy open: %s", st.State)
	}
	if e := h.c.Reopen(); e != nil {
		t.Fatal(e)
	}
}

// Finding 4: a Busy open between two opens of the same vault is no lock and
// no other vault, so the key a failed setup made is still remembered after
// it, and reused; a lock drops it.
func TestABusyDetourKeepsTheKeysMade(t *testing.T) {
	h, cards := lostOnlyHarness(t, "")
	card := generatingCard()
	cards.setCard(card)
	finishSetupFails(t, h, card)
	n, epoch := h.madeNow()
	if n != 1 {
		t.Fatalf("after the failed setup: made lists %d serials", n)
	}
	busyDetour(t, h)
	if n, e := h.madeNow(); n != 1 || e != epoch {
		t.Fatalf("after the Busy detour: %d serials, epoch %d (was %d)", n, e, epoch)
	}
	h.lockAndSettle()
	if n, e := h.madeNow(); n != 0 || e == epoch {
		t.Fatalf("after the lock: %d serials, epoch %d (was %d)", n, e, epoch)
	}

	// Again, and this time the setup after the detour reuses the key.
	finishSetupFails(t, h, card)
	if k := card.keyCount(); k != 3 {
		t.Fatalf("%d keys on the card after the second failed setup", k)
	}
	busyDetour(t, h)
	h.rec.reset()
	if e := h.c.FinishSetup(EnrollOptions{Kind: EnrollToken, Label: "First"}); e != nil {
		t.Fatal(e)
	}
	h.answerRecovery(h.recovery)
	h.answerPIN("123456")
	if st := h.done(); !st.Generated {
		t.Fatalf("the setup after the detour: %+v", st)
	}
	if k := card.keyCount(); k != 3 {
		t.Fatalf("%d keys on the card: the detour lost the key made before it", k)
	}
}

// nearFull stands in front of the keystore's verdict (regionTest) a slot
// region with room for a YubiKey record whose name is at most room bytes:
// nothing Enfold writes fills the region by its bytes. The keystore's own
// measure of such a region is TestTheDefaultNameFitsWhereTheLongestDoesNot.
func nearFull(t *testing.T, room int) {
	t.Helper()
	regionTest = func(label string) error {
		if len(label) > room {
			return keystore.ErrFull
		}
		return nil
	}
	t.Cleanup(func() { regionTest = nil })
}

// Finding 2: a YubiKey with no name given is measured at its longest name
// only to admit, at its shortest only to refuse, and between the two with
// its own serial once the card is read, before anything is asked of it.
func TestTheDefaultNameIsMeasuredWithItsSerial(t *testing.T) {
	recoveryVault := func(t *testing.T, card *fakeCard) (*harness, *fakeCards, string) {
		cards := readerWith(card)
		r1, first := newRecoverySpec(t, "paper")
		r2, _ := newRecoverySpec(t, "paper 2")
		h := rekeyHarness(t, cards, "", first, r1, r2)
		h.unlockWithRecovery(first)
		h.rec.reset()
		return h, cards, first
	}
	begin := func(t *testing.T, h *harness, digits string) {
		if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollToken}); e != nil {
			t.Fatalf("begin: %v", e)
		}
		h.answerRecovery(digits)
	}
	name := keyName(1234567) // the fakes' serial
	if len(keyName(math.MaxUint32)) <= len(name) {
		t.Fatal("the longest name is not longer than the card's")
	}
	t.Run("room for the card's name, not the longest", func(t *testing.T) {
		card, _ := cardWithKey("123456")
		h, _, first := recoveryVault(t, card)
		nearFull(t, len(name))
		begin(t, h, first)
		h.answerPIN("123456")
		h.done()
		if s := h.slot(name); s.Type != "hardware" {
			t.Fatalf("the enrolled slot: %+v", s)
		}
	})
	t.Run("room for no name", func(t *testing.T) {
		card := generatingCard()
		h, cards, first := recoveryVault(t, card)
		nearFull(t, len(keyName(0))-1)
		opens := cards.openAttempts()
		begin(t, h, first)
		if st := h.end(); st.Step != StepFailed || st.Error != CodeSlotsFull {
			t.Fatalf("the end: %+v", st)
		}
		if n := cards.openAttempts() - opens; n != 0 {
			t.Fatalf("%d card opens", n)
		}
	})
	t.Run("room for a shorter name than the card's", func(t *testing.T) {
		card := generatingCard()
		h, cards, first := recoveryVault(t, card)
		nearFull(t, len(name)-1)
		opens := cards.openAttempts()
		mark := h.mark()
		begin(t, h, first)
		st := h.end()
		if st.Step != StepFailed || st.Error != CodeSlotsFull || st.Generated {
			t.Fatalf("the end: %+v", st)
		}
		if n := cards.openAttempts() - opens; n == 0 {
			t.Fatal("refused before the card was read: the serial's name was never measured")
		}
		for _, s := range h.ceremoniesSince(mark) {
			if s.Step == StepPIN || s.Step == StepManagementKey {
				t.Fatalf("the card was asked for its PIN: %+v", s)
			}
		}
		if k := card.keyCount(); k != 1 {
			t.Fatalf("%d keys on the card", k)
		}
	})
}

// Finding 2 again, through the switch: the switch to a YubiKey measures the
// card's own name before the PIN is asked and before the new password's
// derivation, so a region with room only for a shorter name ends with
// vault.slots_full and no key on the card (keys.go, switchToToken).
func TestTheSwitchMeasuresTheDefaultNameWithItsSerial(t *testing.T) {
	name := keyName(1234567) // the fakes' serial
	t.Run("room for the card's name", func(t *testing.T) {
		card, _ := cardWithKey("123456")
		h, _ := switchHarness(t, card)
		nearFull(t, len(name))
		if e := h.c.BeginSwitchToToken(SwitchOptions{KeepPassword: true}); e != nil {
			t.Fatal(e)
		}
		h.proveWithPassword()
		h.answerPIN("123456")
		if st := h.ended(); st.Step != StepDone {
			t.Fatalf("the end: %+v", st)
		}
		if s := h.slot(name); s.Type != "hardware" {
			t.Fatalf("the enrolled slot: %+v", s)
		}
	})
	t.Run("room for a shorter name than the card's", func(t *testing.T) {
		card := generatingCard()
		h, cards := switchHarness(t, card)
		nearFull(t, len(name)-1)
		opens := cards.openAttempts()
		mark := h.mark()
		if e := h.c.BeginSwitchToToken(SwitchOptions{KeepPassword: true}); e != nil {
			t.Fatal(e)
		}
		h.proveWithPassword()
		st := h.ended()
		if st.Step != StepFailed || st.Error != CodeSlotsFull || st.Generated {
			t.Fatalf("the end: %+v", st)
		}
		if cards.openAttempts() == opens {
			t.Fatal("refused before the card was read: the serial's name was never measured")
		}
		for _, s := range h.ceremoniesSince(mark) {
			if s.Step == StepPIN || s.Step == StepManagementKey {
				t.Fatalf("the card was asked for its PIN: %+v", s)
			}
		}
		if k := card.keyCount(); k != 1 {
			t.Fatalf("%d keys on the card", k)
		}
	})
	t.Run("room for no name", func(t *testing.T) {
		card := generatingCard()
		h, cards := switchHarness(t, card)
		nearFull(t, len(keyName(0))-1)
		opens := cards.openAttempts()
		if e := h.c.BeginSwitchToToken(SwitchOptions{KeepPassword: true}); e != nil {
			t.Fatal(e)
		}
		h.proveWithPassword()
		if st := h.ended(); st.Step != StepFailed || st.Error != CodeSlotsFull {
			t.Fatalf("the end: %+v", st)
		}
		if n := cards.openAttempts() - opens; n != 0 {
			t.Fatalf("%d card opens", n)
		}
	})
}

// Finding 2 again, through the first way in of a vault whose only way in
// was lost: the setup measures the card's own name once it is read, before
// anything is asked of it (vaultfile.go, the adoption's nameAdmits).
func TestFinishSetupMeasuresTheDefaultNameWithItsSerial(t *testing.T) {
	name := keyName(1234567) // the fakes' serial
	t.Run("room for the card's name", func(t *testing.T) {
		h, cards := lostOnlyHarness(t, "")
		card, _ := cardWithKey("123456")
		cards.setCard(card)
		nearFull(t, len(name))
		h.rec.reset()
		if e := h.c.FinishSetup(EnrollOptions{Kind: EnrollToken}); e != nil {
			t.Fatal(e)
		}
		h.answerRecovery(h.recovery)
		h.answerPIN("123456")
		h.done()
		h.rec.waitState(t, StateUnlocked)
		if s := h.slot(name); s.Type != "hardware" {
			t.Fatalf("the enrolled slot: %+v", s)
		}
	})
	t.Run("room for a shorter name than the card's", func(t *testing.T) {
		h, cards := lostOnlyHarness(t, "")
		card := generatingCard()
		cards.setCard(card)
		nearFull(t, len(name)-1)
		h.rec.reset()
		mark := h.mark()
		opens := cards.openAttempts()
		if e := h.c.FinishSetup(EnrollOptions{Kind: EnrollToken}); e != nil {
			t.Fatal(e)
		}
		h.answerRecovery(h.recovery)
		st := h.end()
		if st.Step != StepFailed || st.Error != CodeSlotsFull || st.Generated {
			t.Fatalf("the end: %+v", st)
		}
		if cards.openAttempts() == opens {
			t.Fatal("refused before the card was read: the serial's name was never measured")
		}
		for _, s := range h.ceremoniesSince(mark) {
			if s.Step == StepPIN || s.Step == StepManagementKey {
				t.Fatalf("the card was asked for its PIN: %+v", s)
			}
		}
		if k := card.keyCount(); k != 1 {
			t.Fatalf("%d keys on the card", k)
		}
	})
}

// p256Generator is P-256's generator as an uncompressed point: the
// keystore's stand-in key for a YubiKey not yet read.
func p256Generator() []byte {
	p := elliptic.P256().Params()
	b := make([]byte, format.P256PubSize)
	b[0] = 4
	p.Gx.FillBytes(b[1:33])
	p.Gy.FillBytes(b[33:])
	return b
}

// Finding 3: a vault whose YubiKey's public key is the stand-in's
// placeholder still takes another YubiKey.
func TestAVaultHoldingThePlaceholderKeyTakesAnotherYubiKey(t *testing.T) {
	card, _ := cardWithKey("123456")
	cards := readerWith(card)
	r, digits := newRecoverySpec(t, "paper")
	h := rekeyHarness(t, cards, "", digits, r, keystore.HardwareSlot{PublicKey: p256Generator(), Label: "Old YubiKey"})
	h.unlockWithRecovery(digits)
	h.rec.reset()
	if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollToken, Label: "Desk YubiKey", WithRecoveryKey: true}); e != nil {
		t.Fatalf("begin: %v", e)
	}
	h.answerRecovery(digits)
	h.answerPIN("123456")
	h.done()
	if s := h.slot("Desk YubiKey"); s.Type != "hardware" {
		t.Fatalf("the enrolled slot: %+v", s)
	}
}

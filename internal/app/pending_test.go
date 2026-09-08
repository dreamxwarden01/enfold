package app

import (
	"testing"
	"time"

	"github.com/dreamxwarden01/enfold/internal/kdf"
	"github.com/dreamxwarden01/enfold/internal/keystore"
)

// The pending touch (APP.md §2.2): a cancel is immediate for the page, the
// card call goes on in the background, an unlock adopts it, and the touch
// is bounded to two rounds.

// waitStatus polls the status until pred holds.
func (h *harness) waitStatus(pred func(VaultStatus) bool) VaultStatus {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		st := h.status()
		if pred(st) {
			return st
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.t.Fatalf("status never matched: %+v", h.status())
	return VaultStatus{}
}

// heldUnlock starts a token unlock on a card whose touch is held, and
// takes it to the touch.
func heldUnlock(t *testing.T) (*harness, *fakeCard) {
	t.Helper()
	card := newFakeCard("123456")
	pub := card.addKey(0x9d, true)
	card.holdTouch = true
	cards := &fakeCards{card: card}
	cards.setReaders("Yubico A")
	h := newHarness(t, cards, pub)
	if e := h.c.BeginUnlock(MethodToken); e != nil {
		t.Fatal(e)
	}
	pin := h.rec.waitCeremony(t, StepPIN, true)
	h.c.SubmitSecret("pin", pin.PromptID, "123456")
	h.rec.waitCeremony(t, StepTouch, false)
	return h, card
}

// A cancel during the touch ends the ceremony at once — the page sees it
// cancelled, the vault is Locked, the status says a touch is pending —
// while the card call goes on; when the key gives up, nobody having
// picked the touch up, the card is released and nothing was published.
func TestCancelDuringTouchIsImmediate(t *testing.T) {
	h, card := heldUnlock(t)
	if e := h.c.CancelUnlock(); e != nil {
		t.Fatal(e)
	}
	if f := h.rec.waitCeremony(t, StepFailed, false); f.Error != CodeCancelled {
		t.Fatalf("after the cancel: %+v", f)
	}
	h.rec.waitState(t, StateLocked)
	st := h.waitStatus(func(s VaultStatus) bool { return s.PendingTouch })
	if st.State != StateLocked || st.Ceremony != nil {
		t.Fatalf("pending touch with the wrong state: %+v", st)
	}
	if card.closeCount() != 0 {
		t.Fatal("the card was closed under the call in flight")
	}
	if e := h.c.BeginUnlock(MethodPassword); e != nil && e.Code == CodeCeremonyRunning {
		t.Fatal("a cancelled ceremony still counts as running")
	} else if e == nil {
		h.c.CancelUnlock()
		h.rec.waitState(t, StateLocked)
	}
	card.giveUp()
	h.waitStatus(func(s VaultStatus) bool { return !s.PendingTouch })
	if card.closeCount() != 1 {
		t.Fatalf("closes after the key gave up: %d", card.closeCount())
	}
	if h.status().State != StateLocked {
		t.Fatalf("published after a cancel: %v", h.status().State)
	}
	if n := card.touchCount(); n != 1 {
		t.Fatalf("touch prompts for a pending touch nobody owns: %d", n)
	}
}

// An unlock that begins while the touch is pending adopts it: its panel
// opens at the Touch step with no PIN asked, and the touch unlocks.
func TestPendingTouchIsAdoptedByAnUnlock(t *testing.T) {
	h, card := heldUnlock(t)
	h.c.CancelUnlock()
	h.rec.waitState(t, StateLocked)
	h.waitStatus(func(s VaultStatus) bool { return s.PendingTouch })
	h.rec.reset()
	before := len(h.rec.snapshot())
	if e := h.c.BeginUnlock(MethodToken); e != nil {
		t.Fatal(e)
	}
	touch := h.rec.waitCeremony(t, StepTouch, false)
	if touch.N != 1 || !touch.PINAsked || touch.SlotLabel != "Test key" {
		t.Fatalf("the adopted touch: %+v", touch)
	}
	for _, ev := range h.rec.snapshot()[before:] {
		if s, ok := ev.payload.(CeremonyState); ok && s.Step == StepPIN {
			t.Fatal("the adopter was asked for the PIN")
		}
	}
	if h.status().PendingTouch {
		t.Fatal("still pending after the adoption")
	}
	// The panel stays at the touch while the key blinks: nothing after the
	// adoption says "deriving".
	time.Sleep(100 * time.Millisecond)
	if cs := h.status().Ceremony; cs == nil || cs.Step != StepTouch || cs.N != 1 {
		t.Fatalf("the adopted panel left the touch: %+v", cs)
	}
	card.press()
	h.rec.waitCeremony(t, StepDone, false)
	h.rec.waitState(t, StateUnlocked)
	if n := card.touchCount(); n != 1 {
		t.Fatalf("touches: %d", n)
	}
	if card.closeCount() != 1 {
		t.Fatalf("closes after the unlock: %d", card.closeCount())
	}
}

// The key giving up on the touch starts one more round, never a third:
// the second end fails the ceremony with token.touch — a finish, not a
// park — and the card is released.
func TestTouchRoundsAreTwo(t *testing.T) {
	h, card := heldUnlock(t)
	card.giveUp()
	second := h.rec.waitFor(t, EventVaultCeremony, func(x any) bool {
		s, ok := x.(CeremonyState)
		return ok && s.Step == StepTouch && s.N == 2
	}).(CeremonyState)
	if second.Error != "" {
		t.Fatalf("the continuation carries a note: %+v", second)
	}
	card.giveUp()
	if f := h.rec.waitCeremony(t, StepFailed, false); f.Error != CodeTokenTouch {
		t.Fatalf("after two rounds: %+v", f)
	}
	h.rec.waitState(t, StateLocked)
	if n := card.touchCount(); n != 2 {
		t.Fatalf("touch prompts: %d", n)
	}
	if card.closeCount() != 1 {
		t.Fatalf("closes: %d", card.closeCount())
	}
	if h.status().Ceremony != nil || h.status().PendingTouch {
		t.Fatalf("not over: %+v", h.status())
	}
	// Not a park: the next unlock begins at once, and asks for the PIN
	// again, the close having reset the card.
	h.rec.reset()
	if e := h.c.BeginUnlock(MethodToken); e != nil {
		t.Fatal(e)
	}
	h.rec.waitCeremony(t, StepPIN, true)
	h.c.CancelUnlock()
	h.rec.waitState(t, StateLocked)
}

// The rounds belong to the attempt: an adopted touch gets the one
// continuation the attempt has left, then fails the adopter.
func TestAdoptedTouchGetsOneContinuation(t *testing.T) {
	h, card := heldUnlock(t)
	h.c.CancelUnlock()
	h.rec.waitState(t, StateLocked)
	h.waitStatus(func(s VaultStatus) bool { return s.PendingTouch })
	h.rec.reset()
	if e := h.c.BeginUnlock(MethodToken); e != nil {
		t.Fatal(e)
	}
	h.rec.waitCeremony(t, StepTouch, false)
	card.giveUp()
	h.rec.waitFor(t, EventVaultCeremony, func(x any) bool {
		s, ok := x.(CeremonyState)
		return ok && s.Step == StepTouch && s.N == 2
	})
	card.giveUp()
	if f := h.rec.waitCeremony(t, StepFailed, false); f.Error != CodeTokenTouch {
		t.Fatalf("after the continuation: %+v", f)
	}
	h.rec.waitState(t, StateLocked)
	if n := card.touchCount(); n != 2 {
		t.Fatalf("touch prompts: %d", n)
	}
}

// A lock trigger leaves the pending touch unadoptable: the next unlock
// waits for it with token.pending as its note, then opens the card afresh
// and asks for the PIN.
func TestPendingTouchIsDroppedByALock(t *testing.T) {
	h, card := heldUnlock(t)
	h.c.CancelUnlock()
	h.rec.waitState(t, StateLocked)
	h.waitStatus(func(s VaultStatus) bool { return s.PendingTouch })
	h.c.LockNow(ReasonWorkstation)
	h.rec.reset()
	if e := h.c.BeginUnlock(MethodToken); e != nil {
		t.Fatal(e)
	}
	waiting := h.rec.waitFor(t, EventVaultCeremony, func(x any) bool {
		s, ok := x.(CeremonyState)
		return ok && s.Error == CodeTokenPending
	}).(CeremonyState)
	if waiting.Step != StepWaitingForKey {
		t.Fatalf("waiting elsewhere: %+v", waiting)
	}
	if n := card.touchCount(); n != 1 {
		t.Fatalf("the dropped touch was continued: %d prompts", n)
	}
	card.giveUp()
	pin := h.rec.waitCeremony(t, StepPIN, true)
	if pin.Error != "" {
		t.Fatalf("the note outlived the wait: %+v", pin)
	}
	if card.closeCount() != 1 {
		t.Fatalf("the pending touch's card was not released: %d closes", card.closeCount())
	}
	h.c.SubmitSecret("pin", pin.PromptID, "123456")
	h.rec.waitCeremony(t, StepTouch, false)
	card.press()
	h.rec.waitState(t, StateUnlocked)
}

// A typed credential that needs the file the pending touch holds waits for
// it at Deriving, with the note, and then opens.
func TestRecoveryUnlockWaitsForThePendingTouch(t *testing.T) {
	h, card := heldUnlock(t)
	h.c.CancelUnlock()
	h.rec.waitState(t, StateLocked)
	h.waitStatus(func(s VaultStatus) bool { return s.PendingTouch })
	h.rec.reset()
	if e := h.c.BeginUnlock(MethodRecovery); e != nil {
		t.Fatal(e)
	}
	rk := h.rec.waitCeremony(t, StepRecovery, true)
	h.c.SubmitSecret("recovery", rk.PromptID, h.recovery)
	waiting := h.rec.waitFor(t, EventVaultCeremony, func(x any) bool {
		s, ok := x.(CeremonyState)
		return ok && s.Error == CodeTokenPending
	}).(CeremonyState)
	if waiting.Step != StepDeriving {
		t.Fatalf("waiting elsewhere: %+v", waiting)
	}
	if st := h.status().State; st != StateUnlocking {
		t.Fatalf("state while waiting: %v", st)
	}
	card.giveUp()
	h.rec.waitCeremony(t, StepDone, false)
	h.rec.waitState(t, StateUnlocked)
}

// The file a pending touch holds is never "open in another Enfold": the
// openers say token.pending, and open again once it ends.
func TestOpenersSayPendingWhileTheTouchIsPending(t *testing.T) {
	h, card := heldUnlock(t)
	h.c.CancelUnlock()
	h.rec.waitState(t, StateLocked)
	h.waitStatus(func(s VaultStatus) bool { return s.PendingTouch })
	if e := h.c.Reopen(); !isCode(e, CodeTokenPending) {
		t.Fatalf("reopen: %v", e)
	}
	if e := h.c.OpenVaultFile(h.vault, ""); !isCode(e, CodeTokenPending) {
		t.Fatalf("open: %v", e)
	}
	if st := h.status().State; st != StateLocked {
		t.Fatalf("state after the refusals: %v", st)
	}
	card.giveUp()
	h.waitStatus(func(s VaultStatus) bool { return !s.PendingTouch })
	if e := h.c.Reopen(); e != nil {
		t.Fatalf("reopen after: %v", e)
	}
}

// A slot change cancelled at its touch leaves a pending touch on the
// vault's own handle: registry writes wait for it, and the next slot
// change adopts it — the same VMK — with no PIN asked.
func TestPendingTouchOfASlotChangeHoldsTheHandle(t *testing.T) {
	card := newFakeCard("123456")
	pub := card.addKey(0x9d, true)
	cards := &fakeCards{card: card}
	cards.setReaders("Yubico A")
	h := newHarness(t, cards, pub)
	h.unlockWithToken()
	card.holdTouch = true
	h.rec.reset()
	if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollToken, Label: "Second"}); e != nil {
		t.Fatal(e)
	}
	pin := h.rec.waitCeremony(t, StepPIN, true)
	h.c.SubmitSecret("pin", pin.PromptID, "123456")
	h.rec.waitCeremony(t, StepTouch, false)
	h.c.CancelUnlock()
	if f := h.rec.waitCeremony(t, StepFailed, false); f.Error != CodeCancelled {
		t.Fatalf("after the cancel: %+v", f)
	}
	st := h.waitStatus(func(s VaultStatus) bool { return s.PendingTouch && s.Ceremony == nil })
	if st.State != StateUnlocked {
		t.Fatalf("a cancelled slot change left the vault %v", st.State)
	}
	if e := h.c.updateRegistry(func(g *registry) error { return nil }); !isCode(e, CodeCeremonyRunning) {
		t.Fatalf("a registry write under the pending touch: %v", e)
	}
	// The next slot change — another kind — adopts: Touch at once, no PIN.
	h.rec.reset()
	before := len(h.rec.snapshot())
	if e := h.c.ExportBackup(h.dir + "/backup.eks"); e != nil {
		t.Fatal(e)
	}
	touch := h.rec.waitCeremony(t, StepTouch, false)
	if touch.Kind != "export" || touch.N != 1 || !touch.PINAsked {
		t.Fatalf("the adopted touch: %+v", touch)
	}
	for _, ev := range h.rec.snapshot()[before:] {
		if s, ok := ev.payload.(CeremonyState); ok && (s.Step == StepPIN || s.Error == CodeTokenPending) {
			t.Fatalf("the adopter was made to wait or asked for the PIN: %+v", s)
		}
	}
	if n := card.touchCount(); n != 2 { // the unlock's, and the held one
		t.Fatalf("touch prompts after the adoption: %d", n)
	}
	time.Sleep(100 * time.Millisecond)
	if cs := h.status().Ceremony; cs == nil || cs.Step != StepTouch {
		t.Fatalf("the adopted mutation's panel left the touch: %+v", cs)
	}
	if e := h.c.updateRegistry(func(g *registry) error { return nil }); e == nil || e.Code != CodeCeremonyRunning {
		// The adopter holds the handle now: still refused, by the live
		// ceremony.
		t.Fatalf("a registry write under the live slot change: %v", e)
	}
	// Cancelled again, it is pending again; the key giving up ends it.
	h.c.CancelUnlock()
	h.rec.waitCeremony(t, StepFailed, false)
	h.waitStatus(func(s VaultStatus) bool { return s.Ceremony == nil && s.PendingTouch })
	card.giveUp()
	h.waitStatus(func(s VaultStatus) bool { return !s.PendingTouch })
	if e := h.c.updateRegistry(func(g *registry) error { return nil }); e != nil {
		t.Fatalf("a registry write after: %v", e)
	}
	if h.status().State != StateUnlocked || card.closeCount() != 2 {
		t.Fatalf("state: %v closes=%d", h.status().State, card.closeCount())
	}
}

// makeVaultWithKey makes a vault whose ways in are a recovery key and the
// hardware key pub.
func makeVaultWithKey(t *testing.T, path string, pub []byte) {
	t.Helper()
	rk, _ := kdf.NewRecoveryKey()
	unl, err := keystore.Create(path, keystore.CreateOptions{Slots: []keystore.SlotSpec{
		keystore.RecoverySlot{Key: rk, Label: "Recovery key"},
		keystore.HardwareSlot{PublicKey: pub, Label: "Test key"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ks := unl.Keystore()
	unl.Close()
	ks.Close()
}

// An import's agreement is another file's: nothing adopts it, and an
// import tried again while it stands is answered with token.pending.
func TestImportsPendingTouchIsNotAdopted(t *testing.T) {
	h := newHarness(t, nil, nil)
	other := h.dir + "/other.eks"
	card := newFakeCard("123456")
	pub := card.addKey(0x9d, true)
	card.holdTouch = true
	makeVaultWithKey(t, other, pub)
	cards := &fakeCards{card: card}
	cards.setReaders("Yubico A")
	data := t.TempDir()
	rec := &recorder{}
	c, err := New(Deps{Cards: cards, Events: rec, Clock: newFakeClock(), DataDir: data, Log: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	c.Start()
	t.Cleanup(c.Close)
	if e := c.ImportFile(other, "Other", MethodToken, EnrollOptions{}, false); e != nil {
		t.Fatal(e)
	}
	pin := rec.waitCeremony(t, StepPIN, true)
	c.SubmitSecret("pin", pin.PromptID, "123456")
	rec.waitCeremony(t, StepTouch, false)
	c.CancelUnlock()
	rec.waitCeremony(t, StepFailed, false)
	deadline := time.Now().Add(5 * time.Second)
	for !c.Status().PendingTouch && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	// Nothing to unlock here (no vault kept), so the import is tried
	// again: it waits, it does not adopt.
	if e := c.ImportFile(other, "Other", MethodToken, EnrollOptions{}, false); !isCode(e, CodeTokenPending) {
		t.Fatalf("an import over the pending touch: %v", e)
	}
	card.giveUp()
	for c.Status().PendingTouch && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if c.Status().PendingTouch || staged(data) {
		t.Fatalf("after the key gave up: pending=%v staged=%v", c.Status().PendingTouch, staged(data))
	}
}

// The exit waits for the pending touch, so the card is not left
// PIN-verified for the next program.
func TestExitWaitsForThePendingTouch(t *testing.T) {
	h, card := heldUnlock(t)
	h.c.CancelUnlock()
	h.waitStatus(func(s VaultStatus) bool { return s.PendingTouch })
	go func() {
		time.Sleep(150 * time.Millisecond)
		card.giveUp()
	}()
	started := time.Now()
	h.c.ResolveForShutdown(time.Second)
	h.c.AwaitPendingTouch()
	if card.closeCount() != 1 {
		t.Fatalf("the exit did not wait for the card's release: %d closes after %v", card.closeCount(), time.Since(started))
	}
}

// The exit cancels a ceremony whose touch nobody had cancelled — the
// common case: quit from the tray while the key blinks — and still
// waits for the card's answer and release.
func TestExitWaitsForAnOwnedTouch(t *testing.T) {
	h, card := heldUnlock(t)
	go func() {
		time.Sleep(150 * time.Millisecond)
		card.giveUp()
	}()
	h.c.ResolveForShutdown(time.Second)
	h.c.AwaitPendingTouch()
	if card.closeCount() != 1 || h.status().PendingTouch {
		t.Fatalf("after the exit: closes=%d pending=%v", card.closeCount(), h.status().PendingTouch)
	}
}

// A backup import whose enrolment proof is cancelled at its touch: the
// ceremony ends at once, the staged copy's handle is closed by whoever
// still holds it, and the copy is removed when the pending touch ends.
func TestDisownedProofLeavesNoStagedCopy(t *testing.T) {
	h := newHarness(t, nil, nil)
	backup := h.dir + "/backup.eks"
	exportBackup(t, h.vault, backup)
	card := newFakeCard("123456")
	card.addKey(0x9d, true)
	card.holdTouch = true
	cards := &fakeCards{card: card}
	cards.setReaders("Yubico A")
	data := t.TempDir()
	rec := &recorder{}
	c, err := New(Deps{Cards: cards, Events: rec, Clock: newFakeClock(), DataDir: data, Log: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	c.Start()
	t.Cleanup(c.Close)
	if e := c.ImportFile(backup, "Restored", MethodRecovery, EnrollOptions{Kind: EnrollToken}, false); e != nil {
		t.Fatal(e)
	}
	r := rec.waitCeremony(t, StepRecovery, true)
	c.SubmitSecret("recovery", r.PromptID, digits(h.recovery))
	pin := rec.waitCeremony(t, StepPIN, true)
	c.SubmitSecret("pin", pin.PromptID, "123456")
	rec.waitCeremony(t, StepTouch, false)
	c.CancelUnlock()
	if f := rec.waitCeremony(t, StepFailed, false); f.Error != CodeCancelled {
		t.Fatalf("after the cancel: %+v", f)
	}
	rec.waitState(t, StateNone)
	if !c.Status().PendingTouch {
		t.Fatal("no pending touch")
	}
	card.giveUp()
	deadline := time.Now().Add(5 * time.Second)
	for c.Status().PendingTouch && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if c.Status().PendingTouch {
		t.Fatal("the pending touch did not end")
	}
	if staged(data) {
		t.Fatal("the staged copy was left behind")
	}
	if card.closeCount() != 1 {
		t.Fatalf("closes: %d", card.closeCount())
	}
}

// The key pulled during the touch: the attempt ends with the key gone,
// which the owner takes back to waiting — or, cancelled, ends with.
func TestKeyPulledDuringTheTouch(t *testing.T) {
	h, card := heldUnlock(t)
	card.pull()
	back := h.rec.waitFor(t, EventVaultCeremony, func(x any) bool {
		s, ok := x.(CeremonyState)
		return ok && s.Step == StepWaitingForKey && s.Error == CodeTokenNoCard
	}).(CeremonyState)
	if back.Step != StepWaitingForKey {
		t.Fatalf("after the pull: %+v", back)
	}
	if h.status().PendingTouch {
		t.Fatal("pending after the key answered")
	}
	// Back, and unlocked the ordinary way.
	card.setRemoved(false)
	card.holdTouch = false
	pin := h.rec.waitCeremony(t, StepPIN, true)
	h.c.SubmitSecret("pin", pin.PromptID, "123456")
	h.rec.waitState(t, StateUnlocked)
}

// A create's proof cancelled at its touch: the ceremony ends at once, the
// pending touch is never adopted by anything, and the card is released
// when the key gives up.
func TestProofCancelIsImmediate(t *testing.T) {
	card := newFakeCard("123456")
	card.addKey(0x9d, true)
	card.holdTouch = true
	cards := &fakeCards{card: card}
	cards.setReaders("Yubico A")
	dir := t.TempDir()
	rec := &recorder{}
	c, _ := New(Deps{Cards: cards, Events: rec, Clock: newFakeClock(), DataDir: dir + "/data", Log: t.Logf})
	c.Start()
	defer c.Close()
	c.SetAppOrigin("wails://wails")
	if e := c.CreateVault("", "Mine", EnrollOptions{Kind: EnrollToken}, false); e != nil {
		t.Fatal(e)
	}
	pin := rec.waitCeremony(t, StepPIN, true)
	c.SubmitSecret("pin", pin.PromptID, "123456")
	rec.waitCeremony(t, StepTouch, false)
	c.CancelUnlock()
	if f := rec.waitCeremony(t, StepFailed, false); f.Error != CodeCancelled {
		t.Fatalf("after the cancel: %+v", f)
	}
	rec.waitState(t, StateNone)
	if !c.Status().PendingTouch {
		t.Fatal("no pending touch")
	}
	if e := c.CreateVault("", "Mine", EnrollOptions{Kind: EnrollToken}, false); !isCode(e, CodeTokenPending) {
		t.Fatalf("a create over the pending touch: %v", e)
	}
	card.giveUp()
	deadline := time.Now().Add(5 * time.Second)
	for c.Status().PendingTouch && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if c.Status().PendingTouch || card.closeCount() != 1 {
		t.Fatalf("after the key gave up: pending=%v closes=%d", c.Status().PendingTouch, card.closeCount())
	}
}

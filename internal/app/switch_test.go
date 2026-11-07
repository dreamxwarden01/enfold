package app

import (
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dreamxwarden01/enfold/internal/format"
	"github.com/dreamxwarden01/enfold/internal/keystore"
)

// The core of Switch to a YubiKey… (kind toToken, ruled 2026-11-07): a
// password vault traded for a YubiKey in one commit, the password going on
// as the vault's, asked after the YubiKey's PIN. The fake cards stand in
// for the YubiKeys; nothing here opens a real one. A test that sets
// keptSeen or a seam does not run in parallel.

// switchHarness is a password vault (testPassword) and a recovery key, card
// in the reader, unlocked with the password, the recorder reset.
func switchHarness(t *testing.T, card *fakeCard) (*harness, *fakeCards) {
	t.Helper()
	cards := readerWith(card)
	h := newHarness(t, cards, nil)
	h.unlockWithPassword()
	h.rec.reset()
	return h, cards
}

// watchKept collects every buffer an act keeps a password in, the proof's
// or the one chosen, so that a test can see each zeroed at the act's end.
func watchKept(t *testing.T) func() [][]byte {
	t.Helper()
	var mu sync.Mutex
	var seen [][]byte
	f := func(b []byte) {
		mu.Lock()
		seen = append(seen, b)
		mu.Unlock()
	}
	keptSeen.Store(&f)
	t.Cleanup(func() { keptSeen.Store(nil) })
	return func() [][]byte {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(seen)
	}
}

// requireZeroed fails the test unless want buffers were kept and every one
// of them is all zeros now.
func requireZeroed(t *testing.T, bufs [][]byte, want int) {
	t.Helper()
	if len(bufs) != want {
		t.Fatalf("%d passwords kept, want %d", len(bufs), want)
	}
	for i, b := range bufs {
		if len(b) == 0 {
			t.Fatalf("kept buffer %d is empty", i)
		}
		for _, x := range b {
			if x != 0 {
				t.Fatalf("kept buffer %d was not zeroed", i)
			}
		}
	}
}

// ended waits for the act's last state, Done or a Failed that is not a
// refusal offering Use another YubiKey (a Failed with the prompt of kind
// another), and for the ceremony to be gone.
func (h *harness) ended() CeremonyState {
	h.t.Helper()
	st := h.rec.waitFor(h.t, EventVaultCeremony, func(x any) bool {
		s, ok := x.(CeremonyState)
		return ok && s.Kind == "toToken" && (s.Step == StepDone || (s.Step == StepFailed && !(s.PromptID != "" && anotherHelps(s.Error))))
	}).(CeremonyState)
	limit := time.Now().Add(10 * time.Second)
	for {
		h.c.mu.Lock()
		gone := h.c.cer == nil
		h.c.mu.Unlock()
		if gone {
			return st
		}
		if time.Now().After(limit) {
			h.t.Fatalf("the act did not end after %+v", st)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// answerPIN answers the next PIN prompt.
func (h *harness) answerPIN(pin string) {
	h.t.Helper()
	p := h.rec.waitCeremony(h.t, StepPIN, true)
	if e := h.c.SubmitSecret("pin", p.PromptID, pin); e != nil {
		h.t.Fatalf("submit the PIN: %v", e)
	}
}

// stillAPasswordVault fails the test unless the vault is as it was: its
// standalone password and its recovery key, no YubiKey, the switch off.
func (h *harness) stillAPasswordVault(what string) {
	h.t.Helper()
	var types []string
	for _, s := range h.c.Slots() {
		types = append(types, s.Type)
	}
	slices.Sort(types)
	if !slices.Equal(types, []string{"password", "recovery"}) {
		h.t.Fatalf("%s: the unlock methods are %v", what, types)
	}
	if st := h.c.EntangledState(); st.On || st.HasHardware || !st.HasStandalone {
		h.t.Fatalf("%s: %+v", what, st)
	}
}

// stepsSince are the distinct steps from mark on, in order, with a step
// that repeats in a row told once: the sequence the page sees.
func (h *harness) stepsSince(mark int) []string {
	var out []string
	for _, s := range h.ceremoniesSince(mark) {
		x := string(s.Step)
		if s.Step == StepSwapKey && s.RemoveLabel != "" {
			x += "(" + s.RemoveLabel + ")"
		}
		if s.PromptID != "" {
			x += "?"
		}
		if s.Choose {
			x += "+choose"
		}
		if s.Error != "" {
			x += "[" + string(s.Error) + "]"
		}
		if len(out) == 0 || out[len(out)-1] != x {
			out = append(out, x)
		}
	}
	return out
}

// The same words: proved by the password, which the act keeps for K_P, the
// new YubiKey's PIN alone, then one commit. The vault then opens with the
// YubiKey's PIN and the same password, and the password alone opens nothing.
func TestSwitchToTokenWithTheSameWords(t *testing.T) {
	card, pub := cardWithKey("123456")
	h, cards := switchHarness(t, card)
	kept := watchKept(t)
	if st := h.c.EntangledState(); !st.CanSwitch || st.SwitchReason != "" || !st.HasStandalone || st.HasHardware || st.On {
		t.Fatalf("before: %+v", st)
	}
	mark := h.mark()
	if e := h.c.BeginSwitchToToken(SwitchOptions{Label: "Desk YubiKey", KeepPassword: true}); e != nil {
		t.Fatal(e)
	}
	h.proveWithPassword()
	if first := h.ceremoniesSince(mark)[0]; !first.KeepPassword || first.Kind != "toToken" || first.Rekey {
		t.Fatalf("the act's first state: %+v", first)
	}
	h.answerPIN("123456")
	st := h.ended()
	t.Logf("same words: %s", strings.Join(h.stepsSince(mark), " > "))
	if st.Step != StepDone || st.SlotLabel != "Desk YubiKey" || !st.KeepPassword || st.Rekey || st.Generated {
		t.Fatalf("the end: %+v", st)
	}
	if st.After == nil || !st.After.Entangled || len(st.After.RecoveryIDs) != 1 || st.After.SetupNeeded {
		t.Fatalf("the After facts: %+v", st.After)
	}
	requireZeroed(t, kept(), 1)
	es := h.c.EntangledState()
	if !es.On || !es.HasHardware || es.HasStandalone || es.CanSwitch || es.SwitchReason != "" {
		t.Fatalf("after: %+v", es)
	}
	slots := h.c.Slots()
	if len(slots) != 2 || h.slot("Desk YubiKey").Type != "hardware" || h.generation() != 1 {
		t.Fatalf("slots %+v, generation %d", slots, h.generation())
	}
	if h.owes() {
		t.Fatal("the act left its retirement owed")
	}
	_ = pub
	_ = cards

	// The lock screen: the YubiKey's PIN, then the same password.
	h.lockAndSettle()
	if !h.status().Entangled {
		t.Fatal("the lock screen does not know the password is on")
	}
	h.entangled = testPassword
	h.unlockWithToken()
	h.lockAndSettle()
	h.rec.reset()
	if e := h.c.BeginUnlock(MethodPassword); e == nil {
		p := h.rec.waitFor(t, EventVaultCeremony, func(x any) bool {
			s, ok := x.(CeremonyState)
			return ok && (s.Step == StepFailed || (s.Step == StepPassword && s.PromptID != ""))
		}).(CeremonyState)
		if p.Step == StepPassword {
			h.c.SubmitSecret("password", p.PromptID, testPassword)
			if f := h.rec.waitCeremony(t, StepFailed, false); f.Error == "" {
				t.Fatalf("the password alone: %+v", f)
			}
		}
	}
	if h.status().State == StateUnlocked {
		t.Fatal("the password alone unlocked the vault")
	}
}

// A new password, proved by the old one and re-keyed: the old password is
// not kept (only the one chosen is, and it is zeroed), the commit re-keys,
// and the vault opens with the YubiKey and the new password.
func TestSwitchToTokenWithANewPasswordAndARekey(t *testing.T) {
	card, _ := cardWithKey("123456")
	h, _ := switchHarness(t, card)
	kept := watchKept(t)
	mark := h.mark()
	if e := h.c.BeginSwitchToToken(SwitchOptions{Rekey: true}); e != nil {
		t.Fatal(e)
	}
	h.proveWithPassword()
	h.chooseNew("a brand new password")
	h.answerPIN("123456")
	st := h.ended()
	t.Logf("new password, re-keyed: %s", strings.Join(h.stepsSince(mark), " > "))
	if st.Step != StepDone || !st.Rekey || st.KeepPassword || st.SlotLabel != keyName(1234567) {
		t.Fatalf("the end: %+v", st)
	}
	requireZeroed(t, kept(), 1)
	if h.generation() != 2 {
		t.Fatalf("generation %d", h.generation())
	}
	h.lockAndSettle()
	h.entangled = "a brand new password"
	h.unlockWithToken()
}

// A new password proved by a recovery key (O4): nothing of the proof is
// kept; the recovery key's prompt comes first, then the choice.
func TestSwitchToTokenProvedByARecoveryKey(t *testing.T) {
	card, _ := cardWithKey("123456")
	h, _ := switchHarness(t, card)
	kept := watchKept(t)
	mark := h.mark()
	if e := h.c.BeginSwitchToToken(SwitchOptions{Label: "Desk YubiKey", WithRecoveryKey: true}); e != nil {
		t.Fatal(e)
	}
	h.answerRecovery(h.recovery)
	h.chooseNew("chosen after the recovery key")
	h.answerPIN("123456")
	st := h.ended()
	t.Logf("recovery-key proof: %s", strings.Join(h.stepsSince(mark), " > "))
	if st.Step != StepDone || st.Method != string(MethodRecovery) {
		t.Fatalf("the end: %+v", st)
	}
	requireZeroed(t, kept(), 1)
	h.lockAndSettle()
	h.entangled = "chosen after the recovery key"
	h.unlockWithToken()
}

// Every refusal before the ceremony asks nothing and opens no card: a
// recovery-key proof with the same words (O4), no smart-card service, a
// vault with a YubiKey, a vault with 2 passwords, a locked vault; and
// BeginEnroll's YubiKey on a password vault (O11). EntangledState carries
// the reason the page greys with.
func TestSwitchToTokenRefusesBeforeTheCeremony(t *testing.T) {
	card, _ := cardWithKey("123456")
	h, cards := switchHarness(t, card)
	mark := h.mark()
	if e := h.c.BeginSwitchToToken(SwitchOptions{KeepPassword: true, WithRecoveryKey: true}); !isCode(e, CodeParams) {
		t.Fatalf("a recovery key with the same words: %v", e)
	}
	if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollToken}); !isCode(e, CodeSlotPolicy) {
		t.Fatalf("a YubiKey added beside the password: %v", e)
	}
	h.noCeremonySince(mark, "the refusals")
	if cards.openAttempts() != 0 {
		t.Fatalf("%d opens before the refusals", cards.openAttempts())
	}

	noService := newHarness(t, nil, nil)
	noService.unlockWithPassword()
	if e := noService.c.BeginSwitchToToken(SwitchOptions{}); !isCode(e, CodeTokenNoService) {
		t.Fatalf("no smart-card service: %v", e)
	}

	two := rekeyHarnessWithPasswords(t, cards)
	two.unlockWithPassword()
	if st := two.c.EntangledState(); st.CanSwitch || st.SwitchReason != CodeSlotPolicy || !st.HasStandalone {
		t.Fatalf("2 passwords: %+v", st)
	}
	mark = two.mark()
	if e := two.c.BeginSwitchToToken(SwitchOptions{KeepPassword: true}); !isCode(e, CodeSlotPolicy) {
		t.Fatalf("2 passwords: %v", e)
	}
	two.noCeremonySince(mark, "2 passwords")

	tokenCard, pub := cardWithKey("123456")
	yk := newHarness(t, readerWith(tokenCard), pub)
	yk.unlockWithToken()
	if st := yk.c.EntangledState(); st.CanSwitch || st.SwitchReason != "" || st.HasStandalone || !st.HasHardware {
		t.Fatalf("a YubiKey vault: %+v", st)
	}
	if e := yk.c.BeginSwitchToToken(SwitchOptions{}); !isCode(e, CodeSlotNotFound) {
		t.Fatalf("a YubiKey vault: %v", e)
	}

	locked := newHarness(t, readerWith(newFakeCard("1")), nil)
	if st := locked.c.EntangledState(); st.CanSwitch || st.SwitchReason != "" || !st.HasStandalone {
		t.Fatalf("locked: %+v", st)
	}
	if e := locked.c.BeginSwitchToToken(SwitchOptions{}); !isCode(e, CodeNeedsUnlock) {
		t.Fatalf("locked: %v", e)
	}
}

// rekeyHarnessWithPasswords is a vault of 2 standalone passwords (the
// first testPassword) and a recovery key, cards in the reader, locked.
func rekeyHarnessWithPasswords(t *testing.T, cards *fakeCards) *harness {
	t.Helper()
	r, digits := newRecoverySpec(t, "Recovery key")
	return rekeyHarness(t, cards, "", digits, r,
		keystore.PasswordSlot{Password: testPassword, Argon2: fast, Label: "Password"},
		keystore.PasswordSlot{Password: "the second password", Argon2: fast, Label: "Second"})
}

// A blocked PIN in the second half: the act stands at Blocked with the
// prompt of kind another, the proof and the password kept; answered, the
// blocked YubiKey is named until it leaves, and another one inserted is
// enrolled. Nothing was written to the vault until the commit.
func TestSwitchToTokenUseAnotherYubiKeyAfterABlockedPIN(t *testing.T) {
	blocked, _ := cardWithKey("123456")
	blocked.retries, blocked.serial = 0, 1111
	other, otherPub := cardWithKey("123456")
	other.serial = 2222
	h, cards := switchHarness(t, blocked)
	kept := watchKept(t)
	mark := h.mark()
	if e := h.c.BeginSwitchToToken(SwitchOptions{KeepPassword: true}); e != nil {
		t.Fatal(e)
	}
	h.proveWithPassword()
	at := h.rec.waitFor(t, EventVaultCeremony, func(x any) bool {
		s, ok := x.(CeremonyState)
		return ok && s.Step == StepBlocked && s.PromptID != "" && s.Error == CodeTokenPINBlocked
	}).(CeremonyState)
	h.stillAPasswordVault("at the blocked PIN")
	if e := h.c.SubmitSecret("another", at.PromptID, ""); e != nil {
		t.Fatal(e)
	}
	h.rec.waitFor(t, EventVaultCeremony, func(x any) bool {
		s, ok := x.(CeremonyState)
		return ok && s.Step == StepSwapKey && s.RemoveLabel == keyName(1111) && s.Error == ""
	})
	cards.setCard(other)
	h.answerPIN("123456")
	st := h.ended()
	t.Logf("a blocked PIN, then another YubiKey: %s", strings.Join(h.stepsSince(mark), " > "))
	if st.Step != StepDone || st.SlotLabel != keyName(2222) {
		t.Fatalf("the end: %+v", st)
	}
	requireZeroed(t, kept(), 1)
	h.c.mu.Lock()
	enrolled := slices.ContainsFunc(h.c.vault.slots, func(s keystore.SlotInfo) bool { return string(s.PublicKey) == string(otherPub) })
	h.c.mu.Unlock()
	if !enrolled {
		t.Fatal("the YubiKey enrolled is not the other one")
	}
	if blocked.verifyCount() != 0 {
		t.Fatalf("%d VERIFYs sent to the blocked YubiKey", blocked.verifyCount())
	}
}

// A cancel at each prompt and wait of the act ends it with nothing changed
// and every kept password zeroed: the proof, the choice, the insert wait,
// the new YubiKey's PIN, and the refusal that offers another YubiKey.
func TestSwitchToTokenCancelledAtEachStep(t *testing.T) {
	for _, c := range []struct {
		name  string
		keep  bool
		kept  int
		until func(h *harness, cards *fakeCards)
	}{
		{"at the proof", true, 0, func(h *harness, _ *fakeCards) {
			h.rec.waitCeremony(h.t, StepPassword, true)
		}},
		{"at the choice", false, 0, func(h *harness, _ *fakeCards) {
			h.proveWithPassword()
			h.rec.waitFor(h.t, EventVaultCeremony, func(x any) bool {
				s, ok := x.(CeremonyState)
				return ok && s.Step == StepPassword && s.Choose && s.PromptID != ""
			})
		}},
		{"at the insert wait", true, 1, func(h *harness, cards *fakeCards) {
			cards.setReaders()
			h.proveWithPassword()
			h.rec.waitCeremony(h.t, StepWaitingForKey, false)
		}},
		{"at the PIN", false, 1, func(h *harness, _ *fakeCards) {
			h.proveWithPassword()
			h.chooseNew("a brand new password")
			h.rec.waitCeremony(h.t, StepPIN, true)
		}},
		{"at Use another YubiKey", true, 1, func(h *harness, cards *fakeCards) {
			cards.card.mu.Lock()
			cards.card.retries = 0
			cards.card.mu.Unlock()
			h.proveWithPassword()
			h.rec.waitFor(h.t, EventVaultCeremony, func(x any) bool {
				s, ok := x.(CeremonyState)
				return ok && s.Step == StepBlocked && s.PromptID != ""
			})
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			card, _ := cardWithKey("123456")
			h, cards := switchHarness(t, card)
			kept := watchKept(t)
			if e := h.c.BeginSwitchToToken(SwitchOptions{KeepPassword: c.keep}); e != nil {
				t.Fatal(e)
			}
			c.until(h, cards)
			if e := h.c.CancelUnlock(); e != nil {
				t.Fatal(e)
			}
			st := h.ended()
			if st.Step != StepFailed || st.Error != CodeCancelled {
				t.Fatalf("the end: %+v", st)
			}
			if c.kept == 0 {
				if n := len(kept()); n != 0 {
					t.Fatalf("%d passwords kept before the cancel", n)
				}
			} else {
				requireZeroed(t, kept(), c.kept)
			}
			h.stillAPasswordVault("after the cancel")
			if h.generation() != 1 || h.owes() {
				t.Fatalf("generation %d, owed %v", h.generation(), h.owes())
			}
		})
	}
}

// A key generated in the act and then refused (it does not prove itself),
// the act cancelled at Use another YubiKey: the end says a key was
// generated; begun again, the act reuses that key on the same YubiKey
// rather than generate a second one, and the end still says so until Done.
func TestSwitchToTokenReusesTheKeyItGenerated(t *testing.T) {
	card := newFakeCard("123456")
	card.mgmt = make([]byte, 24)
	card.addKey(0x9d, false) // 9d holds a key Enfold cannot use: a new one goes to 82
	card.proofLies = true
	h, _ := switchHarness(t, card)
	mark := h.mark()
	if e := h.c.BeginSwitchToToken(SwitchOptions{KeepPassword: true}); e != nil {
		t.Fatal(e)
	}
	h.proveWithPassword()
	h.answerPIN("123456") // the management key's PIN
	at := h.rec.waitFor(t, EventVaultCeremony, func(x any) bool {
		s, ok := x.(CeremonyState)
		return ok && s.Step == StepFailed && s.PromptID != "" && s.Error == CodeTokenProof
	}).(CeremonyState)
	if !at.Generated {
		t.Fatalf("the refusal after a generation: %+v", at)
	}
	h.c.CancelUnlock()
	st := h.ended()
	t.Logf("generated, refused, cancelled: %s", strings.Join(h.stepsSince(mark), " > "))
	if st.Error != CodeCancelled || !st.Generated {
		t.Fatalf("the end: %+v", st)
	}
	if n := card.keyCount(); n != 2 {
		t.Fatalf("%d keys on the card", n)
	}
	h.stillAPasswordVault("after the cancel")

	// Start again: the same YubiKey, the key reused, no second generated.
	card.mu.Lock()
	card.proofLies = false
	card.mu.Unlock()
	h.rec.reset()
	mark = h.mark()
	if e := h.c.BeginSwitchToToken(SwitchOptions{KeepPassword: true}); e != nil {
		t.Fatal(e)
	}
	h.proveWithPassword()
	h.answerPIN("123456") // the agreement's PIN: no management key is needed
	st = h.ended()
	t.Logf("begun again, the key reused: %s", strings.Join(h.stepsSince(mark), " > "))
	if st.Step != StepDone || !st.Generated {
		t.Fatalf("the end: %+v", st)
	}
	for _, s := range h.ceremoniesSince(mark) {
		if s.Step == StepManagementKey {
			t.Fatal("the management key was asked for a key already made")
		}
	}
	if n := card.keyCount(); n != 2 {
		t.Fatalf("%d keys on the card after the act begun again", n)
	}
	// Enrolled now, the key leaves the list of keys generated and not
	// enrolled: a later enrolment of this YubiKey in this unlock is met by
	// the rules of before, never by this key (APP.md §3).
	h.c.mu.Lock()
	left := len(h.c.vault.made)
	h.c.mu.Unlock()
	if left != 0 {
		t.Fatalf("made still lists %d serials after the key was enrolled", left)
	}
}

// The trade's commit failing, before and after its bodies were written:
// the act fails with its code, nothing was changed (one commit), what a
// failed commit wrote is retired at once, and the password still unlocks.
func TestSwitchToTokenCommitFails(t *testing.T) {
	t.Run("before the commit", func(t *testing.T) {
		card, _ := cardWithKey("123456")
		h, _ := switchHarness(t, card)
		kept := watchKept(t)
		h.c.seams.failAt = func(p string) error {
			if p == "commit" {
				return errInjected
			}
			return nil
		}
		defer func() { h.c.seams.failAt = nil }()
		if e := h.c.BeginSwitchToToken(SwitchOptions{KeepPassword: true, Rekey: true}); e != nil {
			t.Fatal(e)
		}
		h.proveWithPassword()
		h.answerPIN("123456")
		st := h.ended()
		if st.Step != StepFailed || st.Error == CodeCancelled || st.Error == "" || st.After != nil {
			t.Fatalf("the end: %+v", st)
		}
		requireZeroed(t, kept(), 1)
		h.stillAPasswordVault("after the failed commit")
		h.lockAndSettle()
		h.unlockWithPassword()
	})
	t.Run("after its bodies were written", func(t *testing.T) {
		card, _ := cardWithKey("123456")
		h, _ := switchHarness(t, card)
		// Once: the retirement the act runs at once after it must land.
		var once sync.Once
		restore := keystore.SetCommitFault(func(step string) error {
			var err error
			if step == "sync" {
				once.Do(func() { err = errInjected })
			}
			return err
		})
		defer restore()
		if e := h.c.BeginSwitchToToken(SwitchOptions{KeepPassword: true}); e != nil {
			t.Fatal(e)
		}
		h.proveWithPassword()
		h.answerPIN("123456")
		st := h.ended()
		restore()
		if st.Step != StepFailed || st.Error == CodeCancelled {
			t.Fatalf("the end: %+v", st)
		}
		h.stillAPasswordVault("after the failed commit")
		if h.owes() || h.generation() != 1 {
			t.Fatalf("owed %v, generation %d", h.owes(), h.generation())
		}
		h.lockAndSettle()
		h.unlockWithPassword()
	})
}

// A lock in the second half ends the act with nothing changed, the kept
// password zeroed, and the password still unlocks.
func TestSwitchToTokenLockedMidAct(t *testing.T) {
	card, _ := cardWithKey("123456")
	h, _ := switchHarness(t, card)
	kept := watchKept(t)
	if e := h.c.BeginSwitchToToken(SwitchOptions{KeepPassword: true}); e != nil {
		t.Fatal(e)
	}
	h.proveWithPassword()
	h.rec.waitCeremony(t, StepPIN, true)
	h.lockAndSettle()
	h.ended()
	requireZeroed(t, kept(), 1)
	h.unlockWithPassword()
	h.stillAPasswordVault("after the lock")
}

// Yesterday's wait inside the act: a YubiKey a record of the file already
// holds (a retired record here) is named and waited out on insertion,
// asked nothing, and the next one inserted is enrolled.
func TestSwitchToTokenWaitsOutAYubiKeyAlreadyARecord(t *testing.T) {
	cardR, pubR := cardWithKey("123456")
	old := hardwareRecords
	hardwareRecords = func(ks *keystore.Keystore) []keystore.SlotInfo {
		return append(ks.HardwareRecords(), keystore.SlotInfo{RecipientID: [16]byte{0x52}, Type: format.SlotExternalECDH, Label: "Retired key", PublicKey: pubR})
	}
	defer func() { hardwareRecords = old }()
	h, cards := switchHarness(t, cardR)
	mark := h.mark()
	if e := h.c.BeginSwitchToToken(SwitchOptions{KeepPassword: true}); e != nil {
		t.Fatal(e)
	}
	h.proveWithPassword()
	h.waitSwap("Retired key", CodeDuplicateSlot)
	h.noPINSince(mark, "a retired record's YubiKey")
	fresh, _ := cardWithKey("654321")
	cards.setCard(fresh)
	h.answerPIN("654321")
	st := h.ended()
	t.Logf("a YubiKey already a record: %s", strings.Join(h.stepsSince(mark), " > "))
	if st.Step != StepDone || cardR.verifyCount() != 0 {
		t.Fatalf("the end: %+v, %d VERIFYs to the retired record's YubiKey", st, cardR.verifyCount())
	}
}

// keyCount is how many keys the card holds, whatever its state.
func (f *fakeCard) keyCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.keys)
}

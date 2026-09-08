package app

import (
	"bytes"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dreamxwarden01/enfold/internal/format"
	"github.com/dreamxwarden01/enfold/internal/kdf"
	"github.com/dreamxwarden01/enfold/internal/keystore"
)

// entangledHarness is a hardware vault whose entangled password is on, with
// the card that opens it and the reader set behind it.
func entangledHarness(t *testing.T, password string) (*harness, *fakeCard, *fakeCards) {
	t.Helper()
	card := newFakeCard("123456")
	pub := card.addKey(0x9d, true)
	cards := &fakeCards{card: card}
	cards.setReaders("Yubico A")
	return newHarnessEntangled(t, cards, pub, password), card, cards
}

// vaultIDOf is the id of the vault the core keeps.
func vaultIDOf(c *Core) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return hexID(c.vault.vaultID)
}

// The switch is the vault's, not a slot's, and it is a plaintext fact of
// the slot region header: the lock screen has it with no handle open, which
// is what lets it ask for the password before the PIN (FORMAT.md §6, §18.1;
// APP.md §2.1's cached facts).
func TestVaultStatusCarriesTheEntangledSwitchWhileLocked(t *testing.T) {
	h, _, _ := entangledHarness(t, "the vault password")
	if st := h.status(); st.State != StateLocked || !st.Entangled {
		t.Fatalf("locked status: %+v", st)
	}
	h.unlockWithToken()
	if st := h.status(); !st.Entangled {
		t.Fatalf("unlocked status: %+v", st)
	}
	h.c.Lock()
	h.rec.waitState(t, StateLocked)
	if st := h.status(); !st.Entangled {
		t.Fatalf("after locking again: %+v", st)
	}
	// A vault whose switch is off says so, locked and unlocked alike.
	off := newHarness(t, nil, nil)
	if st := off.status(); st.Entangled {
		t.Fatalf("a vault with no password claims one: %+v", st)
	}
	off.unlockWithPassword()
	if st := off.status(); st.Entangled {
		t.Fatalf("a vault with no password claims one while unlocked: %+v", st)
	}
}

// The vault file's own size is on the status, for the Archives header line
// (APP.md §13): the logical end the last commit computed, so it is known
// while Locked and follows a registry write.
func TestVaultStatusCarriesTheVaultFileSize(t *testing.T) {
	h := newHarness(t, nil, nil)
	fi, err := os.Stat(h.vault)
	if err != nil {
		t.Fatal(err)
	}
	st := h.status()
	if st.VaultFileSize == 0 || st.VaultFileSize != uint64(fi.Size()) {
		t.Fatalf("size %d, the file is %d", st.VaultFileSize, fi.Size())
	}
	h.unlockWithPassword()
	if got := h.status().VaultFileSize; got != st.VaultFileSize {
		t.Fatalf("the size changed at the unlock: %d, was %d", got, st.VaultFileSize)
	}
	if _, e := h.c.CreateArchive(filepath.Join(h.dir, "a.enf"), "A", false); e != nil {
		t.Fatal(e)
	}
	fi2, err := os.Stat(h.vault)
	if err != nil {
		t.Fatal(err)
	}
	if got := h.status().VaultFileSize; got != uint64(fi2.Size()) {
		t.Fatalf("after a registry write: %d, the file is %d", got, fi2.Size())
	}
}

// The lock screen asks for the password from the vault's switch, once,
// before the PIN (APP.md §13). The wording follows the way in chosen, which
// the ceremony reports as Method — with the switch on, "did a secret get
// asked" no longer tells a token flow from a password one.
func TestLockScreenAsksThePasswordFromTheVaultsSwitch(t *testing.T) {
	h, _, _ := entangledHarness(t, "the vault password")
	h.rec.reset()
	if e := h.c.BeginUnlock(MethodToken); e != nil {
		t.Fatal(e)
	}
	pw := h.rec.waitCeremony(t, StepPassword, true)
	if pw.Choose {
		t.Fatalf("the vault's own password marked choose: %+v", pw)
	}
	if pw.Method != string(MethodToken) {
		t.Fatalf("the token flow's password prompt does not say which way in: %+v", pw)
	}
	h.c.SubmitSecret("password", pw.PromptID, "the vault password")
	pin := h.rec.waitCeremony(t, StepPIN, true)
	if pin.Method != string(MethodToken) {
		t.Fatalf("the PIN prompt does not say which way in: %+v", pin)
	}
	h.c.SubmitSecret("pin", pin.PromptID, "123456")
	h.rec.waitCeremony(t, StepDone, false)
	h.rec.waitState(t, StateUnlocked)

	// Asked once: one password prompt in the whole unlock.
	prompts := 0
	for _, e := range h.rec.snapshot() {
		if s, ok := e.payload.(CeremonyState); ok && s.Kind == "unlock" && s.Step == StepPassword && s.PromptID != "" {
			prompts++
		}
	}
	if prompts != 1 {
		t.Fatalf("password prompts in one token unlock: %d", prompts)
	}

	// A wrong one is said and asked again in place, and the typed value
	// lives with the attempt rather than being asked for from the start.
	h.c.Lock()
	h.rec.waitState(t, StateLocked)
	h.rec.reset()
	mark := len(h.rec.snapshot())
	if e := h.c.BeginUnlock(MethodToken); e != nil {
		t.Fatal(e)
	}
	pw = h.rec.waitCeremony(t, StepPassword, true)
	h.c.SubmitSecret("password", pw.PromptID, "not the vault password")
	pin = h.rec.waitCeremony(t, StepPIN, true)
	h.c.SubmitSecret("pin", pin.PromptID, "123456")
	again := h.rec.waitFor(t, EventVaultCeremony, func(x any) bool {
		s, ok := x.(CeremonyState)
		return ok && s.Step == StepPassword && s.PromptID != "" && s.PromptID != pw.PromptID
	}).(CeremonyState)
	if again.Error != CodeAuth {
		t.Fatalf("a wrong vault password was not said in place: %+v", again)
	}
	h.c.SubmitSecret("password", again.PromptID, "the vault password")
	h.rec.waitState(t, StateUnlocked)
	// The PIN was not asked a second time: the attempt kept the token.
	pins := 0
	for _, e := range h.rec.snapshot()[mark:] {
		if s, ok := e.payload.(CeremonyState); ok && s.Step == StepPIN && s.PromptID != "" {
			pins++
		}
	}
	if pins != 1 {
		t.Fatalf("PIN prompts while the password was asked again: %d", pins)
	}
}

// The standalone password slot and the recovery slot are never entangled
// (FORMAT.md §3.1): neither way in asks for the vault's password, whatever
// the switch says.
func TestStandaloneAndRecoveryNeverAskTheEntangledPassword(t *testing.T) {
	h, _, _ := entangledHarness(t, "the vault password")
	h.rec.reset()
	if e := h.c.BeginUnlock(MethodRecovery); e != nil {
		t.Fatal(e)
	}
	r := h.rec.waitCeremony(t, StepRecovery, true)
	if r.Method != string(MethodRecovery) {
		t.Fatalf("the recovery prompt does not say which way in: %+v", r)
	}
	h.c.SubmitSecret("recovery", r.PromptID, h.recovery)
	h.rec.waitCeremony(t, StepDone, false)
	h.rec.waitState(t, StateUnlocked)
	for _, e := range h.rec.snapshot() {
		if s, ok := e.payload.(CeremonyState); ok && s.Step == StepPassword {
			t.Fatalf("a recovery unlock asked for the vault's password: %+v", s)
		}
	}
	// The standalone password slot cannot coexist with a hardware slot
	// (§6.4's policy), so its half of the rule is that its own password is
	// the slot's and the vault's switch is off.
	pv := newHarness(t, nil, nil)
	if st := pv.status(); st.Entangled {
		t.Fatalf("a password vault's switch: %+v", st)
	}
	pv.rec.reset()
	if e := pv.c.BeginUnlock(MethodPassword); e != nil {
		t.Fatal(e)
	}
	p := pv.rec.waitCeremony(t, StepPassword, true)
	if p.Method != string(MethodPassword) {
		t.Fatalf("the password prompt does not say which way in: %+v", p)
	}
	pv.c.SubmitSecret("password", p.PromptID, testPassword)
	pv.rec.waitState(t, StateUnlocked)
}

// The typed vault password lives with the attempt (APP.md §2.2): a ceremony
// that adopts a pending touch neither loses it nor asks for it again.
func TestAdoptedTouchKeepsTheTypedEntangledPassword(t *testing.T) {
	card := newFakeCard("123456")
	pub := card.addKey(0x9d, true)
	card.holdTouch = true
	cards := &fakeCards{card: card}
	cards.setReaders("Yubico A")
	h := newHarnessEntangled(t, cards, pub, "the vault password")
	if e := h.c.BeginUnlock(MethodToken); e != nil {
		t.Fatal(e)
	}
	pw := h.rec.waitCeremony(t, StepPassword, true)
	h.c.SubmitSecret("password", pw.PromptID, "the vault password")
	pin := h.rec.waitCeremony(t, StepPIN, true)
	h.c.SubmitSecret("pin", pin.PromptID, "123456")
	h.rec.waitCeremony(t, StepTouch, false)

	// Cancelled at the touch: the card call goes on as the pending touch,
	// carrying the password with it.
	h.c.CancelUnlock()
	h.rec.waitState(t, StateLocked)
	h.waitStatus(func(s VaultStatus) bool { return s.PendingTouch })
	h.rec.reset()
	before := len(h.rec.snapshot())
	if e := h.c.BeginUnlock(MethodToken); e != nil {
		t.Fatal(e)
	}
	touch := h.rec.waitCeremony(t, StepTouch, false)
	if touch.Method != string(MethodToken) {
		t.Fatalf("the adopted ceremony does not say which way in: %+v", touch)
	}
	for _, ev := range h.rec.snapshot()[before:] {
		if s, ok := ev.payload.(CeremonyState); ok && s.PromptID != "" {
			t.Fatalf("the adopter was asked for a secret again: %+v", s)
		}
	}
	card.press()
	h.rec.waitCeremony(t, StepDone, false)
	h.rec.waitState(t, StateUnlocked)
}

// Enrolling a key into an entangled vault asks for no password: the key
// inherits the vault's switch and is wrapped from the kept K_P, offline
// (APP.md §13, FORMAT.md §18.1). The enrolled key then opens the vault with
// the vault's password and its own PIN.
func TestEnrollIntoAnEntangledVaultAsksNoPassword(t *testing.T) {
	h, first, cards := entangledHarness(t, "the vault password")
	h.unlockWithToken()
	h.rec.reset()
	if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollToken, Label: "Travel key"}); e != nil {
		t.Fatal(e)
	}
	// The unlock half asks for the vault's password (it opens with a token)
	// and the PIN; nothing after that asks for a secret the user chooses.
	pw := h.rec.waitCeremony(t, StepPassword, true)
	if pw.Choose {
		t.Fatalf("the unlock half's password marked choose: %+v", pw)
	}
	// The way in is a token even though the first prompt is a password:
	// Method is the only field that says so (§5.1).
	if pw.Method != string(MethodToken) {
		t.Fatalf("the mutation's password prompt: method %q, want token: %+v", pw.Method, pw)
	}
	h.c.SubmitSecret("password", pw.PromptID, "the vault password")
	pin := h.rec.waitCeremony(t, StepPIN, true)
	if pin.Method != string(MethodToken) {
		t.Fatalf("the mutation's PIN prompt: method %q, want token", pin.Method)
	}
	h.c.SubmitSecret("pin", pin.PromptID, "123456")
	swap := h.rec.waitCeremony(t, StepSwapKey, false)
	if swap.RemoveLabel != "Test key" {
		t.Fatalf("swap step: %+v", swap)
	}
	second := newFakeCard("654321")
	second.mgmt = []byte("0123456789abcdef0123456789abcdef")
	first.setRemoved(true)
	cards.setReaders()
	time.Sleep(2 * readerPoll)
	cards.setCard(second)
	cards.setReaders("Yubico B")
	pin2 := h.rec.waitCeremony(t, StepPIN, true)
	h.c.SubmitSecret("pin", pin2.PromptID, "654321")
	h.rec.waitCeremony(t, StepDone, false)
	for _, e := range h.rec.snapshot() {
		if s, ok := e.payload.(CeremonyState); ok && s.Kind == "enroll" && s.Choose {
			t.Fatalf("the enrolment chose a secret: %+v", s)
		}
	}
	hw := 0
	for _, s := range h.c.Slots() {
		if s.Type == "hardware" {
			hw++
		}
	}
	if hw != 2 {
		t.Fatalf("slots after the enrolment: %+v", h.c.Slots())
	}
	// The new key opens the vault with the vault's password.
	h.c.Lock()
	h.rec.waitState(t, StateLocked)
	h.rec.reset()
	if e := h.c.BeginUnlock(MethodToken); e != nil {
		t.Fatal(e)
	}
	pw = h.rec.waitCeremony(t, StepPassword, true)
	h.c.SubmitSecret("password", pw.PromptID, "the vault password")
	pin = h.rec.waitCeremony(t, StepPIN, true)
	h.c.SubmitSecret("pin", pin.PromptID, "654321")
	h.rec.waitCeremony(t, StepDone, false)
	h.rec.waitState(t, StateUnlocked)
}

// Turning the switch on where every active slot is a hardware key is
// refused for the invariant before the ceremony starts and before a
// password is typed (FORMAT.md §6.4, APP.md §13); adding a recovery slot is
// what makes it possible.
func TestSetEntangledOnIsRefusedByTheInvariantBeforeAnyPrompt(t *testing.T) {
	c, rec := hardwareOnlyCore(t)

	if got := c.EntangledState(); got.On || got.CanEnable || got.Reason != CodeInvariant {
		t.Fatalf("entangled state on a hardware-only vault: %+v", got)
	}
	rec.reset()
	if e := c.SetEntangled(true); !isCode(e, CodeInvariant) {
		t.Fatalf("turning it on: %v", e)
	}
	// Refused before anything: no ceremony was started, so nothing could
	// have prompted.
	if st := c.Status(); st.Ceremony != nil || st.Entangled {
		t.Fatalf("a refused switch left a ceremony: %+v", st)
	}

	if e := c.BeginEnroll(EnrollOptions{Kind: EnrollRecovery, Label: "Paper"}); e != nil {
		t.Fatal(e)
	}
	pin := rec.waitCeremony(t, StepPIN, true)
	c.SubmitSecret("pin", pin.PromptID, "123456")
	rec.waitCeremony(t, StepRecovery, false)
	rec.waitState(t, StateUnlocked) // afterMutation refreshed the facts
	if got := c.EntangledState(); !got.CanEnable || got.Reason != "" {
		t.Fatalf("after adding a recovery slot: %+v", got)
	}
	// And the switch can now be turned on: the new password is chosen once.
	rec.reset()
	if e := c.SetEntangled(true); e != nil {
		t.Fatal(e)
	}
	pin = rec.waitCeremony(t, StepPIN, true)
	c.SubmitSecret("pin", pin.PromptID, "123456")
	pw := rec.waitCeremony(t, StepPassword, true)
	if !pw.Choose {
		t.Fatalf("the new password is not marked choose: %+v", pw)
	}
	c.SubmitSecret("password", pw.PromptID, "the vault password")
	rec.waitCeremony(t, StepDone, false)
	if st := c.Status(); !st.Entangled {
		t.Fatalf("the switch did not go on: %+v", st)
	}
}

// CanEnable greys the switch ahead of any ceremony, as Removable greys
// Remove; while Locked it is false, since no ceremony can start there
// anyway (APP.md §13, B.22).
func TestEntangledStateCanEnableGreysTheSwitchAhead(t *testing.T) {
	h, _, _ := entangledHarness(t, "the vault password")
	if got := h.c.EntangledState(); !got.On || got.CanEnable {
		t.Fatalf("locked: %+v", got)
	}
	h.unlockWithToken()
	if got := h.c.EntangledState(); !got.On || !got.CanEnable || got.Reason != "" {
		t.Fatalf("unlocked and entangled: %+v", got)
	}
	off := newHarness(t, nil, nil)
	if got := off.c.EntangledState(); got.On || got.CanEnable {
		t.Fatalf("a locked password vault: %+v", got)
	}
	off.unlockWithPassword()
	// A password vault keeps a recovery slot beside its standalone slot, so
	// the sets stay disjoint whatever the switch says.
	if got := off.c.EntangledState(); got.On || !got.CanEnable || got.Reason != "" {
		t.Fatalf("an unlocked password vault: %+v", got)
	}
}

// Changing the password never asks for the old one — nothing in the call
// takes it (APP.md §13, the ruling of 2026-09-07) — and the key opens with
// the new one afterwards and not with the old.
func TestSetEntangledNeverAsksTheOldPassword(t *testing.T) {
	h, _, _ := entangledHarness(t, "the first password")
	h.unlockWithToken()
	h.rec.reset()
	if e := h.c.ChangeEntangledPassword(); e != nil {
		t.Fatal(e)
	}
	// The unlock half asks for the vault's password as it always does; the
	// change itself asks only for the new one, marked choose.
	h.answerTokenUnlock("123456")
	pw := h.rec.waitCeremony(t, StepPassword, true)
	if !pw.Choose {
		t.Fatalf("the new password is not marked choose: %+v", pw)
	}
	h.c.SubmitSecret("password", pw.PromptID, "the second password")
	h.rec.waitCeremony(t, StepDone, false)
	chosen := 0
	for _, e := range h.rec.snapshot() {
		if s, ok := e.payload.(CeremonyState); ok && s.Kind == "entangle" && s.Step == StepPassword && s.PromptID != "" && s.Choose {
			chosen++
		}
	}
	if chosen != 1 {
		t.Fatalf("chosen-password prompts in one change: %d", chosen)
	}
	if st := h.status(); !st.Entangled {
		t.Fatalf("the switch after a change: %+v", st)
	}

	// The retired password no longer opens the key; the new one does.
	h.c.Lock()
	h.rec.waitState(t, StateLocked)
	h.rec.reset()
	if e := h.c.BeginUnlock(MethodToken); e != nil {
		t.Fatal(e)
	}
	old := h.rec.waitCeremony(t, StepPassword, true)
	h.c.SubmitSecret("password", old.PromptID, "the first password")
	pin := h.rec.waitCeremony(t, StepPIN, true)
	h.c.SubmitSecret("pin", pin.PromptID, "123456")
	again := h.rec.waitFor(t, EventVaultCeremony, func(x any) bool {
		s, ok := x.(CeremonyState)
		return ok && s.Step == StepPassword && s.PromptID != "" && s.PromptID != old.PromptID
	}).(CeremonyState)
	if again.Error != CodeAuth {
		t.Fatalf("the retired password was not refused: %+v", again)
	}
	h.c.SubmitSecret("password", again.PromptID, "the second password")
	h.rec.waitState(t, StateUnlocked)
	h.entangled = "the second password"
}

// Turning the switch off is never refused and takes no password: the key
// then opens with its PIN alone (FORMAT.md §6.4, APP.md §13).
func TestSetEntangledOffIsNeverRefused(t *testing.T) {
	h, _, _ := entangledHarness(t, "the vault password")
	h.unlockWithToken()
	h.rec.reset()
	if e := h.c.SetEntangled(false); e != nil {
		t.Fatal(e)
	}
	h.answerTokenUnlock("123456")
	h.rec.waitCeremony(t, StepDone, false)
	h.entangled = ""
	if st := h.status(); st.Entangled {
		t.Fatalf("the switch stayed on: %+v", st)
	}
	for _, e := range h.rec.snapshot() {
		if s, ok := e.payload.(CeremonyState); ok && s.Kind == "entangle" && s.Choose {
			t.Fatalf("turning the switch off asked for a password: %+v", s)
		}
	}
	// The key opens with its PIN alone now.
	h.c.Lock()
	h.rec.waitState(t, StateLocked)
	h.rec.reset()
	mark := len(h.rec.snapshot())
	if e := h.c.BeginUnlock(MethodToken); e != nil {
		t.Fatal(e)
	}
	pin := h.rec.waitCeremony(t, StepPIN, true)
	h.c.SubmitSecret("pin", pin.PromptID, "123456")
	h.rec.waitState(t, StateUnlocked)
	for _, e := range h.rec.snapshot()[mark:] {
		if s, ok := e.payload.(CeremonyState); ok && s.Kind == "unlock" && s.Step == StepPassword {
			t.Fatalf("the key still asks for a password: %+v", s)
		}
	}
	// Changing a password the vault has not got is refused before any
	// prompt, so a salt redraw cannot happen by the wrong button.
	if e := h.c.ChangeEntangledPassword(); !isCode(e, CodeParams) {
		t.Fatalf("changing a password the vault has not got: %v", e)
	}
}

// A change re-wraps every active hardware slot from the kept K_P, offline:
// a key that is not in any reader while the write runs opens with the new
// password afterwards, and no token is asked for anything by the write
// itself (FORMAT.md §18.1, APP.md §13). The recovery slot is untouched.
func TestChangeEntangledPasswordRewrapsEveryHardwareSlotOffline(t *testing.T) {
	a := newFakeCard("123456")
	pubA := a.addKey(0x9d, true)
	b := newFakeCard("654321")
	pubB := b.addKey(0x9d, true)
	dir := t.TempDir()
	vault := filepath.Join(dir, "vault.eks")
	rk, err := kdf.NewRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	unl, err := keystore.Create(vault, keystore.CreateOptions{
		Slots: []keystore.SlotSpec{
			keystore.RecoverySlot{Key: rk, Label: "Recovery key"},
			keystore.HardwareSlot{PublicKey: pubA, Label: "Key A"},
			keystore.HardwareSlot{PublicKey: pubB, Label: "Key B"},
		},
		Entangle: &keystore.Entangle{Password: "the first password", Argon2: fast},
	})
	if err != nil {
		t.Fatal(err)
	}
	ks := unl.Keystore()
	unl.Close()
	ks.Close()
	// Only key A is ever in a reader.
	cards := &fakeCards{card: a}
	cards.setReaders("Yubico A")
	c, rec := freshCoreWithCards(t, filepath.Join(dir, "data"), cards)
	if e := c.OpenVaultFile(vault, "Two keys"); e != nil {
		t.Fatal(e)
	}
	rec.reset()
	if e := c.BeginUnlock(MethodToken); e != nil {
		t.Fatal(e)
	}
	pw := rec.waitCeremony(t, StepPassword, true)
	c.SubmitSecret("password", pw.PromptID, "the first password")
	pin := rec.waitCeremony(t, StepPIN, true)
	c.SubmitSecret("pin", pin.PromptID, "123456")
	rec.waitState(t, StateUnlocked)

	rec.reset()
	opensBefore, touchesBefore := cards.openCount(), a.touchCount()
	if e := c.ChangeEntangledPassword(); e != nil {
		t.Fatal(e)
	}
	pw = rec.waitCeremony(t, StepPassword, true)
	c.SubmitSecret("password", pw.PromptID, "the first password") // the unlock half
	pin = rec.waitCeremony(t, StepPIN, true)
	c.SubmitSecret("pin", pin.PromptID, "123456")
	chosen := rec.waitCeremony(t, StepPassword, true)
	if !chosen.Choose {
		t.Fatalf("the new password is not marked choose: %+v", chosen)
	}
	c.SubmitSecret("password", chosen.PromptID, "the second password")
	rec.waitCeremony(t, StepDone, false)

	// The write itself opened no further card and asked no further touch:
	// exactly one of each, the unlock half's.
	if n := cards.openCount() - opensBefore; n != 1 {
		t.Fatalf("cards opened by the change: %d", n)
	}
	if n := a.touchCount() - touchesBefore; n != 1 {
		t.Fatalf("touches asked by the change: %d", n)
	}
	if b.touchCount() != 0 {
		t.Fatalf("the key that was not there was touched: %d", b.touchCount())
	}

	// Key B, absent throughout, opens with the new password.
	c.Lock()
	rec.waitState(t, StateLocked)
	cards.setCard(b)
	rec.reset()
	if e := c.BeginUnlock(MethodToken); e != nil {
		t.Fatal(e)
	}
	pw = rec.waitCeremony(t, StepPassword, true)
	c.SubmitSecret("password", pw.PromptID, "the second password")
	pin = rec.waitCeremony(t, StepPIN, true)
	c.SubmitSecret("pin", pin.PromptID, "654321")
	rec.waitState(t, StateUnlocked)

	// And the recovery key still opens it, untouched by the change.
	c.Lock()
	rec.waitState(t, StateLocked)
	rec.reset()
	if e := c.BeginUnlock(MethodRecovery); e != nil {
		t.Fatal(e)
	}
	r := rec.waitCeremony(t, StepRecovery, true)
	c.SubmitSecret("recovery", r.PromptID, rk.Digits())
	rec.waitState(t, StateUnlocked)
}

// A slot region whose generation is not the superblock's is tampering, not
// a credential that is behind: no other way in is offered, the vault says
// which check said so, and the verdict does not outlive the file
// (FORMAT.md §6.2, §8; APP.md §2.2, §13).
func TestGenerationMismatchIsTamperedNotBehind(t *testing.T) {
	h := newHarness(t, nil, nil)
	// Rotate once through a handle of its own, so both directions of a
	// mismatch exist, then edit the plaintext, checksummed generation.
	ks, err := keystore.Open(h.vault)
	if err != nil {
		t.Fatal(err)
	}
	unl, err := ks.Unlock(keystore.PasswordCredential{Password: testPassword})
	if err != nil {
		t.Fatal(err)
	}
	if err := unl.Rotate(); err != nil {
		t.Fatal(err)
	}
	unl.Close()
	ks.Close()
	good, err := os.ReadFile(h.vault)
	if err != nil {
		t.Fatal(err)
	}
	edited := bytes.Clone(good)
	for _, cp := range []format.Copy{format.CopyA, format.CopyB} {
		off := cp.KeystoreSuperblockOff()
		sb, err := format.DecodeKeystoreSuperblock(edited[off : off+format.SuperblockSize])
		if err != nil {
			continue
		}
		sb.VMKGeneration = 1
		enc, err := sb.Encode()
		if err != nil {
			t.Fatal(err)
		}
		copy(edited[off:], enc)
	}
	if err := os.WriteFile(h.vault, edited, 0o600); err != nil {
		t.Fatal(err)
	}
	if e := h.c.Reopen(); e != nil {
		t.Fatal(e)
	}
	h.rec.reset()
	if e := h.c.BeginUnlock(MethodPassword); e != nil {
		t.Fatal(e)
	}
	p := h.rec.waitCeremony(t, StepPassword, true)
	h.c.SubmitSecret("password", p.PromptID, testPassword)
	f := h.rec.waitCeremony(t, StepFailed, false)
	if f.Error != CodeTamperedGeneration {
		t.Fatalf("a generation mismatch parked at %+v", f)
	}
	if st := h.status(); !st.Tampered || st.TamperedReason != CodeTamperedGeneration {
		t.Fatalf("the vault does not say it was tampered with: %+v", st)
	}
	h.c.CancelUnlock()
	h.rec.waitState(t, StateLocked)
	st := h.status()
	if st.State != StateLocked || !st.Tampered || st.TamperedReason != CodeTamperedGeneration {
		t.Fatalf("after the ceremony ended: %+v", st)
	}
	warned := false
	for _, w := range st.Warnings {
		warned = warned || w == CodeVaultTampered
	}
	if !warned {
		t.Fatalf("no tampering warning: %+v", st.Warnings)
	}

	// The good file again: the verdict is the file's, not the session's.
	if err := os.WriteFile(h.vault, good, 0o600); err != nil {
		t.Fatal(err)
	}
	if e := h.c.Reopen(); e != nil {
		t.Fatal(e)
	}
	if st := h.status(); st.Tampered || st.TamperedReason != "" {
		t.Fatalf("the verdict outlived the file: %+v", st)
	}
	h.unlockWithPassword()
}

// The writes R25 freezes are refused while the vault is tampered with, and
// the status carries the reason so the page can word it (A.5, FORMAT.md §6).
func TestEntangleAndExportAreRefusedWhileTampered(t *testing.T) {
	h, _, _ := entangledHarness(t, "the vault password")
	h.unlockWithToken()
	// An R25 mismatch is what leaves this state; making one on disk is the
	// keystore's own test, so the state is set here directly.
	h.c.mu.Lock()
	h.c.vault.tampered, h.c.vault.tamperedReason = keystore.ErrTampered, CodeTamperedHash
	h.c.vault.warnings[CodeVaultTampered] = true
	h.c.mu.Unlock()
	if st := h.status(); !st.Tampered || st.TamperedReason != CodeTamperedHash {
		t.Fatalf("status: %+v", st)
	}
	if e := h.c.SetEntangled(false); !isCode(e, CodeVaultTampered) {
		t.Fatalf("turning the switch off while tampered: %v", e)
	}
	if e := h.c.ChangeEntangledPassword(); !isCode(e, CodeVaultTampered) {
		t.Fatalf("changing the password while tampered: %v", e)
	}
	if e := h.c.ExportBackup(filepath.Join(t.TempDir(), "b.eks")); !isCode(e, CodeVaultTampered) {
		t.Fatalf("exporting while tampered: %v", e)
	}
	if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollRecovery, Label: "Paper"}); !isCode(e, CodeVaultTampered) {
		t.Fatalf("enrolling while tampered: %v", e)
	}

	// The two pre-checks that run before beginMutation answer the freeze
	// first: on a vault the invariant would refuse and on one whose switch
	// is off, the reason must still be the one the user can act on (A.5).
	c2, rec2 := hardwareOnlyCore(t)
	rec2.reset()
	if got := c2.EntangledState(); got.On || got.CanEnable {
		t.Fatalf("a hardware-only vault: %+v", got)
	}
	c2.mu.Lock()
	c2.vault.tampered, c2.vault.tamperedReason = keystore.ErrTampered, CodeTamperedHash
	c2.vault.warnings[CodeVaultTampered] = true
	c2.mu.Unlock()
	if e := c2.SetEntangled(true); !isCode(e, CodeVaultTampered) {
		t.Fatalf("turning the switch on while tampered: %v", e)
	}
	if e := c2.ChangeEntangledPassword(); !isCode(e, CodeVaultTampered) {
		t.Fatalf("changing the password of an unentangled vault while tampered: %v", e)
	}
	if st := c2.Status(); st.Ceremony != nil {
		t.Fatalf("a refused call left a ceremony: %+v", st)
	}
}

// hardwareOnlyCore is an unlocked vault whose only ways in are two hardware
// keys: the switch is off and the invariant refuses turning it on.
func hardwareOnlyCore(t *testing.T) (*Core, *recorder) {
	t.Helper()
	card := newFakeCard("123456")
	pubA := card.addKey(0x9d, true)
	other := newFakeCard("654321")
	pubB := other.addKey(0x9d, true)
	dir := t.TempDir()
	vault := filepath.Join(dir, "vault.eks")
	unl, err := keystore.Create(vault, keystore.CreateOptions{Slots: []keystore.SlotSpec{
		keystore.HardwareSlot{PublicKey: pubA, Label: "Key A"},
		keystore.HardwareSlot{PublicKey: pubB, Label: "Key B"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ks := unl.Keystore()
	unl.Close()
	ks.Close()
	cards := &fakeCards{card: card}
	cards.setReaders("Yubico A")
	c, rec := freshCoreWithCards(t, filepath.Join(dir, "data"), cards)
	if e := c.OpenVaultFile(vault, "Hardware only"); e != nil {
		t.Fatal(e)
	}
	rec.reset()
	if e := c.BeginUnlock(MethodToken); e != nil {
		t.Fatal(e)
	}
	pin := rec.waitCeremony(t, StepPIN, true)
	c.SubmitSecret("pin", pin.PromptID, "123456")
	rec.waitState(t, StateUnlocked)
	return c, rec
}

// lastExportAt is written by an export and by nothing else (APP.md §13).
func TestLastExportAtIsWrittenOnlyByExport(t *testing.T) {
	h := newHarness(t, nil, nil)
	if got := h.c.LastExportAt(); got != 0 {
		t.Fatalf("never exported: %d", got)
	}
	h.unlockWithPassword()
	// A rotation and a registry write leave it alone.
	h.rec.reset()
	if e := h.c.RotateNow(); e != nil {
		t.Fatal(e)
	}
	p := h.rec.waitCeremony(t, StepPassword, true)
	h.c.SubmitSecret("password", p.PromptID, testPassword)
	h.rec.waitCeremony(t, StepDone, false)
	if _, e := h.c.CreateArchive(filepath.Join(h.dir, "a.enf"), "A", false); e != nil {
		t.Fatal(e)
	}
	if got := h.c.LastExportAt(); got != 0 {
		t.Fatalf("something other than an export stamped it: %d", got)
	}

	out := filepath.Join(t.TempDir(), "backup.eks")
	h.rec.reset()
	if e := h.c.ExportBackup(out); e != nil {
		t.Fatal(e)
	}
	p = h.rec.waitCeremony(t, StepPassword, true)
	h.c.SubmitSecret("password", p.PromptID, testPassword)
	h.rec.waitCeremony(t, StepDone, false)
	want := h.clk.Now().Unix()
	if got := h.c.LastExportAt(); got != want {
		t.Fatalf("after the export: %d, want %d", got, want)
	}
	// It is the settings file's, so it reads back from disk.
	file := loadSettings(filepath.Join(h.dir, "data"))
	if file.LastExport == nil || file.LastExport.At != want || file.LastExport.VaultID != vaultIDOf(h.c) {
		t.Fatalf("settings file: %+v", file.LastExport)
	}
}

// It reads as never for another vault's id and for a stamp ahead of this
// clock: the page never holds another vault's id and never compares clocks
// for a value that words a destructive confirmation (APP.md §13).
func TestLastExportAtReadsAsNeverForAnotherVaultOrAFutureTime(t *testing.T) {
	h := newHarness(t, nil, nil)
	now := h.clk.Now().Unix()
	mine := vaultIDOf(h.c)
	other := "00112233445566778899aabbccddeeff"
	for _, tc := range []struct {
		name string
		le   *lastExport
		want int64
	}{
		{"absent", nil, 0},
		{"zero", &lastExport{VaultID: mine, At: 0}, 0},
		{"another vault", &lastExport{VaultID: other, At: now - 60}, 0},
		{"ahead of the clock", &lastExport{VaultID: mine, At: now + 60}, 0},
		{"this vault, in the past", &lastExport{VaultID: mine, At: now - 60}, now - 60},
		{"this vault, now", &lastExport{VaultID: mine, At: now}, now},
	} {
		h.c.mu.Lock()
		h.c.settings.LastExport = tc.le
		h.c.mu.Unlock()
		if got := h.c.LastExportAt(); got != tc.want {
			t.Fatalf("%s: %d, want %d", tc.name, got, tc.want)
		}
	}
	// A settings file with a malformed id is loaded permissively: the
	// stamp is dropped rather than the file.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"),
		[]byte(`{"theme":"dark","lastExportAt":{"vaultId":"nonsense","at":5}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got := loadSettings(dir)
	if got.Theme != "dark" {
		t.Fatalf("the rest of the file was dropped: %+v", got)
	}
	if got.LastExport != nil {
		t.Fatalf("a malformed stamp was kept: %+v", got.LastExport)
	}
}

// The recovery key's ID is on the view of every recovery slot and nowhere
// else, derived once for the view, the ceremony and the file (FORMAT §18.4).
func TestSlotViewCarriesTheRecoveryID(t *testing.T) {
	h := newHarness(t, nil, nil)
	for _, s := range h.c.Slots() {
		if s.Type != "recovery" {
			if s.RecoveryID != "" {
				t.Fatalf("a %s slot carries a key ID: %+v", s.Type, s)
			}
			continue
		}
		rid, ok := parseID(s.RecipientID)
		if !ok {
			t.Fatalf("the slot's id: %q", s.RecipientID)
		}
		if want := recoveryID(rid); s.RecoveryID != want {
			t.Fatalf("the slot's ID is %q, want %q", s.RecoveryID, want)
		}
		if len(s.RecoveryID) != 9 || s.RecoveryID[4] != '-' {
			t.Fatalf("the ID's shape: %q", s.RecoveryID)
		}
		if s.RecoveryID != strings.ToUpper(s.RecoveryID) {
			t.Fatalf("the ID is not upper-cased: %q", s.RecoveryID)
		}
		if s.RecoveryID[:4]+s.RecoveryID[5:] != strings.ToUpper(s.RecipientID[:8]) {
			t.Fatalf("the ID is not the first eight hex digits: %q of %q", s.RecoveryID, s.RecipientID)
		}
	}
	// A second recovery slot gets its own, and the ceremony that made it
	// shows the same one.
	h.unlockWithPassword()
	h.rec.reset()
	if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollRecovery, Label: "Second paper"}); e != nil {
		t.Fatal(e)
	}
	p := h.rec.waitCeremony(t, StepPassword, true)
	h.c.SubmitSecret("password", p.PromptID, testPassword)
	shown := h.rec.waitCeremony(t, StepRecovery, false)
	if shown.RecoveryID == "" {
		t.Fatalf("the new key was shown without its ID: %+v", shown)
	}
	found := false
	for _, s := range h.c.Slots() {
		if s.Label == "Second paper" {
			found = s.RecoveryID == shown.RecoveryID
		}
	}
	if !found {
		t.Fatalf("the ID shown is not the new slot's: %q of %+v", shown.RecoveryID, h.c.Slots())
	}
}

// The saved text file carries the ID, so the sheet says which key it is
// (FORMAT.md §18.4).
func TestSavedRecoveryKeyFileCarriesTheID(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.c.SetAppOrigin("wails://wails")
	h.unlockWithPassword()
	rid, _ := recoverySlotID(t, h)
	var want string
	for _, s := range h.c.Slots() {
		if s.RecipientID == rid {
			want = s.RecoveryID
		}
	}
	shown := reveal(t, h, rid)
	if shown.RecoveryID != want || want == "" {
		t.Fatalf("the reveal's ID: %q, the view's %q", shown.RecoveryID, want)
	}
	out := filepath.Join(t.TempDir(), "key.txt")
	if e := h.c.SaveRecoveryKey(path.Base(shown.SlotLabel), out); e != nil {
		t.Fatal(e)
	}
	text, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(text, []byte("Key ID: "+want)) {
		t.Fatalf("the saved file does not name the key: %s", text)
	}
}

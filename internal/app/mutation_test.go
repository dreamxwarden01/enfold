package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dreamxwarden01/enfold/internal/kdf"
	"github.com/dreamxwarden01/enfold/internal/keystore"
)

// A lock trigger during a slot change cancels it: the VMK and the card do
// not outlive the lock, and the handle is closed so the vault reopens.
func TestLockCancelsMutationCeremony(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	h.rec.reset()
	if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollPassword, Label: "Second"}); e != nil {
		t.Fatal(e)
	}
	p1 := h.rec.waitCeremony(t, StepPassword, true)
	h.c.SubmitSecret("password", p1.PromptID, testPassword)
	// The VMK is in hand; the new password is being asked for.
	h.rec.waitFor(t, EventVaultCeremony, func(p any) bool {
		s, ok := p.(CeremonyState)
		return ok && s.Step == StepPassword && s.PromptID != "" && s.PromptID != p1.PromptID
	})
	h.c.LockNow(ReasonWorkstation)
	f := h.rec.waitCeremony(t, StepFailed, false)
	if f.Error != CodeCancelled {
		t.Fatalf("ceremony after lock: %+v", f)
	}
	h.rec.waitState(t, StateLocked)
	if slots := h.c.Slots(); len(slots) != 2 {
		t.Fatalf("the cancelled enrolment added a slot: %+v", slots)
	}
	// The handle was released after the ceremony: the vault unlocks again
	// instead of reporting itself busy.
	h.unlockWithPassword()
}

// Registry writes wait for a slot change; a Save meanwhile owes its
// receipt, which is paid when the ceremony ends.
func TestRegistryWritesWaitForMutation(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, e := h.c.CreateArchive(filepath.Join(h.dir, "a.enf"), "A", compressionNormal)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := h.c.OpenArchive(id); e != nil {
		t.Fatal(e)
	}
	f := filepath.Join(h.dir, "f.txt")
	os.WriteFile(f, []byte("owed"), 0o600)
	opID, _ := h.c.AddFiles(id, rootID, []string{f}, PolicySkip)
	h.rec.waitOp(t, opID)

	h.rec.reset()
	if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollPassword, Label: "Second"}); e != nil {
		t.Fatal(e)
	}
	p1 := h.rec.waitCeremony(t, StepPassword, true)
	h.c.SubmitSecret("password", p1.PromptID, testPassword)
	p2 := h.rec.waitFor(t, EventVaultCeremony, func(p any) bool {
		s, ok := p.(CeremonyState)
		return ok && s.Step == StepPassword && s.PromptID != "" && s.PromptID != p1.PromptID
	}).(CeremonyState)
	// The ceremony holds the handle: a direct registry write is refused,
	// a Save commits its archive and owes the receipt.
	if e := h.c.HideArchive(id, true); !isCode(e, CodeCeremonyRunning) {
		t.Fatalf("hide during ceremony: %v", e)
	}
	opID, e = h.c.Save(id)
	if e != nil {
		t.Fatalf("save during ceremony: %v", e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != "" {
		t.Fatalf("save op: %+v", o)
	}
	list, _ := h.c.ListArchives(false)
	if !list[0].ReceiptOwed {
		t.Fatalf("receipt not owed: %+v", list[0])
	}
	// A slot change refuses to start while a registry-writing op runs is
	// covered by the op having finished here; end the ceremony.
	h.c.SubmitSecret("password", p2.PromptID, "second password")
	h.rec.waitCeremony(t, StepDone, false)
	deadline := time.Now().Add(3 * time.Second)
	for {
		list, _ = h.c.ListArchives(false)
		if !list[0].ReceiptOwed && list[0].Files == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("owed receipt not paid after the ceremony: %+v", list[0])
		}
		time.Sleep(10 * time.Millisecond)
	}
	if e := h.c.HideArchive(id, true); e != nil {
		t.Fatalf("hide after ceremony: %v", e)
	}
}

// Enrolling a second YubiKey: the key that unlocked is released and must
// be removed; the next key is enrolled through the management key.
func TestEnrollTokenSwapsKeys(t *testing.T) {
	first := newFakeCard("123456")
	pub := first.addKey(0x9d, true)
	cards := &fakeCards{card: first}
	cards.setReaders("Yubico A")
	h := newHarness(t, cards, pub)

	// Unlock with the first key.
	if e := h.c.BeginUnlock(MethodToken); e != nil {
		t.Fatal(e)
	}
	pin := h.rec.waitCeremony(t, StepPIN, true)
	h.c.SubmitSecret("pin", pin.PromptID, "123456")
	h.rec.waitCeremony(t, StepDone, false)
	h.rec.waitState(t, StateUnlocked)

	// Enrol: the ceremony recovers the VMK with the first key, then asks
	// for the swap.
	h.rec.reset()
	if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollToken, Label: "Travel key"}); e != nil {
		t.Fatal(e)
	}
	pin = h.rec.waitCeremony(t, StepPIN, true)
	h.c.SubmitSecret("pin", pin.PromptID, "123456")
	h.rec.waitCeremony(t, StepSwapKey, false)
	deadline := time.Now().Add(2 * time.Second)
	for {
		first.mu.Lock()
		closed := first.closed
		first.mu.Unlock()
		if closed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the unlocking key was not released before the swap")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Leaving the first key in changes nothing; removing it and inserting
	// an empty second key continues.
	time.Sleep(2 * readerPoll)
	if st := h.status(); st.Ceremony == nil || st.Ceremony.Step != StepSwapKey {
		t.Fatalf("swap step did not hold: %+v", st.Ceremony)
	}
	second := newFakeCard("654321")
	second.mgmt = []byte("0123456789abcdef0123456789abcdef")
	cards.setReaders()
	time.Sleep(2 * readerPoll)
	cards.setCard(second)
	cards.setReaders("Yubico B")
	pin2 := h.rec.waitCeremony(t, StepPIN, true)
	if pin2.PromptID == pin.PromptID {
		t.Fatal("no new PIN prompt for the second key")
	}
	h.c.SubmitSecret("pin", pin2.PromptID, "654321")
	h.rec.waitCeremony(t, StepDone, false)
	slots := h.c.Slots()
	hw := 0
	for _, s := range slots {
		if s.Type == "hardware" {
			hw++
		}
	}
	if len(slots) != 3 || hw != 2 {
		t.Fatalf("slots after enrolment: %+v", slots)
	}
	second.mu.Lock()
	_, generated := second.keys[0x9d]
	second.mu.Unlock()
	if !generated {
		t.Fatal("no key generated in 9d of the second card")
	}
	if st := h.status(); st.State != StateUnlocked {
		t.Fatalf("state after enrolment: %s", st.State)
	}
}

// A slot change does not start while an operation is writing the
// registry, and CreateVault leaves the configured vault alone on failure.
func TestMutationGuards(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, _ := h.c.CreateArchive(filepath.Join(h.dir, "a.enf"), "A", compressionNormal)
	h.c.OpenArchive(id)
	// Verify on an archive runs the hash and then writes the registry.
	opID, e := h.c.Verify(id)
	if e != nil {
		t.Fatal(e)
	}
	if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollPassword, Label: "x"}); !isCode(e, CodeOpRunning) && !isCode(e, CodeNeedsUnlock) && e != nil {
		// The op may already have finished; then the enrolment simply starts.
		t.Fatalf("enroll during verify: %v", e)
	}
	h.rec.waitOp(t, opID)
	h.c.CancelUnlock()

	// With a vault kept, a create — anywhere — is refused outright (APP.md
	// §2.1); the vault's facts stand.
	h.c.Lock()
	h.rec.waitState(t, StateLocked)
	h.rec.reset()
	bad := filepath.Join(h.dir, "no-such-dir", "v.eks")
	if e := h.c.CreateVault(bad, "New", EnrollOptions{Kind: EnrollPassword, Label: "pw"}, false); !isCode(e, CodeVaultKept) {
		t.Fatalf("create with a vault kept: %v", e)
	}
	h.c.CloseArchive(id)
	if e := h.c.CreateVault(bad, "New", EnrollOptions{Kind: EnrollPassword, Label: "pw"}, true); !isCode(e, CodeVaultKept) {
		t.Fatalf("create with a vault kept, replace: %v", e)
	}
	if st := h.status(); st.Path != h.vault || st.DisplayName != "Test vault" {
		t.Fatalf("vault facts touched by a refused create: %+v", st)
	}
	h.unlockWithPassword()
}

// The user may swap keys while a prompt stands. Since Revision 2 an
// enrolment asks for no password of its own — an enrolled key inherits the
// vault's switch and is wrapped from the kept K_P (APP.md §13) — so the
// prompt this hangs on is the one the generate path raises for the
// management key: the enrolment goes on with the key that is there instead
// of waiting for a removal that already happened. And while the unlocking
// key is still in, the swap step names it.
func TestEnrollSwapDuringThePrompt(t *testing.T) {
	old := keepAliveEvery
	keepAliveEvery = 30 * time.Millisecond
	defer func() { keepAliveEvery = old }()
	first := newFakeCard("123456")
	pub := first.addKey(0x9d, true)
	cards := &fakeCards{card: first}
	cards.setReaders("Yubico A")
	h := newHarness(t, cards, pub)
	if e := h.c.BeginUnlock(MethodToken); e != nil {
		t.Fatal(e)
	}
	pin := h.rec.waitCeremony(t, StepPIN, true)
	h.c.SubmitSecret("pin", pin.PromptID, "123456")
	h.rec.waitCeremony(t, StepDone, false)
	h.rec.waitState(t, StateUnlocked)

	// The unlocking key stays in: the swap step says to remove it, and no
	// password is asked anywhere in the enrolment.
	h.rec.reset()
	if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollToken, Label: "Travel key"}); e != nil {
		t.Fatal(e)
	}
	pin = h.rec.waitCeremony(t, StepPIN, true)
	h.c.SubmitSecret("pin", pin.PromptID, "123456")
	swap := h.rec.waitCeremony(t, StepSwapKey, false)
	if swap.RemoveLabel != "Test key" || swap.InsertLabel == "" {
		t.Fatalf("swap step does not name the key to remove: %+v", swap)
	}

	// The second key is empty, so the enrolment goes to the generate path
	// and asks for its PIN; the user swaps to a third key while that prompt
	// stands, in the same reader.
	second := newFakeCard("654321")
	second.mgmt = []byte("0123456789abcdef0123456789abcdef")
	first.setRemoved(true)
	cards.setReaders()
	time.Sleep(2 * readerPoll)
	cards.setCard(second)
	cards.setReaders("Yubico A")
	pin2 := h.rec.waitCeremony(t, StepPIN, true)
	if pin2.PromptID == pin.PromptID {
		t.Fatal("no new PIN prompt for the key being enrolled")
	}
	third := newFakeCard("999999")
	third.mgmt = []byte("0123456789abcdef0123456789abcdef")
	second.setRemoved(true)
	cards.setCard(third)
	// The held card is probed while the prompt stands, so the pull ends it
	// and the enrolment waits for a key again.
	w := h.rec.waitCeremony(t, StepWaitingForKey, false)
	if w.Error != CodeTokenNoCard {
		t.Fatalf("waiting again without the note: %+v", w)
	}
	pin3 := h.rec.waitCeremony(t, StepPIN, true)
	if pin3.PromptID == pin2.PromptID {
		t.Fatal("the swapped key got no prompt of its own")
	}
	h.c.SubmitSecret("pin", pin3.PromptID, "999999")
	h.rec.waitCeremony(t, StepDone, false)
	hw := 0
	for _, s := range h.c.Slots() {
		if s.Type == "hardware" {
			hw++
		}
	}
	if hw != 2 {
		t.Fatalf("slots after the swapped enrolment: %+v", h.c.Slots())
	}
	third.mu.Lock()
	_, generated := third.keys[0x9d]
	third.mu.Unlock()
	if !generated {
		t.Fatal("the key that was there at the end was not the one enrolled")
	}
	// No chosen-secret prompt was raised anywhere in the enrolment.
	for _, e := range h.rec.snapshot() {
		if s, ok := e.payload.(CeremonyState); ok && s.Kind == "enroll" && s.Step == StepPassword {
			t.Fatalf("an enrolment asked for a password: %+v", s)
		}
	}
}

// The unlock half of a slot change waits for the key again when it is
// pulled during the PIN, as an unlock does; nothing warns afterwards
// about a verified state the pull took with it.
func TestKeyPulledDuringMutationPINGoesBackToWaiting(t *testing.T) {
	old := keepAliveEvery
	keepAliveEvery = 30 * time.Millisecond
	defer func() { keepAliveEvery = old }()
	first := newFakeCard("123456")
	pub := first.addKey(0x9d, true)
	cards := &fakeCards{card: first}
	cards.setReaders("Yubico A")
	h := newHarness(t, cards, pub)
	if e := h.c.BeginUnlock(MethodToken); e != nil {
		t.Fatal(e)
	}
	pin := h.rec.waitCeremony(t, StepPIN, true)
	h.c.SubmitSecret("pin", pin.PromptID, "123456")
	h.rec.waitCeremony(t, StepDone, false)
	h.rec.waitState(t, StateUnlocked)

	h.rec.reset()
	if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollRecovery, Label: "Paper"}); e != nil {
		t.Fatal(e)
	}
	pin = h.rec.waitCeremony(t, StepPIN, true)
	first.setRemoved(true)
	cards.setReaders()
	w := h.rec.waitCeremony(t, StepWaitingForKey, false)
	if w.Error != CodeTokenNoCard {
		t.Fatalf("waiting again without the note: %+v", w)
	}
	first.setRemoved(false)
	cards.setReaders("Yubico A")
	pin2 := h.rec.waitCeremony(t, StepPIN, true)
	if pin2.PromptID == pin.PromptID || pin2.Error != "" {
		t.Fatalf("second prompt: %+v", pin2)
	}
	h.c.SubmitSecret("pin", pin2.PromptID, "123456")
	h.rec.waitCeremony(t, StepRecovery, false)
	for _, e := range h.rec.snapshot() {
		if e.name == EventVaultWarning {
			t.Fatalf("a warning after the pull: %+v", e.payload)
		}
	}
}

// A password enrolment releases the key that unlocked before asking for
// the password: the key is not needed any more, and pulling it meanwhile
// is no event. (The vault's policy then refuses a standalone password
// beside a hardware slot — the refusal, not the pull, is what ends it.)
func TestEnrollPasswordReleasesTheKeyBeforeThePrompt(t *testing.T) {
	old := keepAliveEvery
	keepAliveEvery = 30 * time.Millisecond
	defer func() { keepAliveEvery = old }()
	first := newFakeCard("123456")
	pub := first.addKey(0x9d, true)
	cards := &fakeCards{card: first}
	cards.setReaders("Yubico A")
	h := newHarness(t, cards, pub)
	if e := h.c.BeginUnlock(MethodToken); e != nil {
		t.Fatal(e)
	}
	pin := h.rec.waitCeremony(t, StepPIN, true)
	h.c.SubmitSecret("pin", pin.PromptID, "123456")
	h.rec.waitCeremony(t, StepDone, false)
	h.rec.waitState(t, StateUnlocked)

	h.rec.reset()
	if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollPassword, Label: "Words"}); e != nil {
		t.Fatal(e)
	}
	pin = h.rec.waitCeremony(t, StepPIN, true)
	h.c.SubmitSecret("pin", pin.PromptID, "123456")
	pw := h.rec.waitCeremony(t, StepPassword, true)
	first.mu.Lock()
	closed := first.closed
	first.mu.Unlock()
	if !closed {
		t.Fatal("the unlocking key is held across the password prompt")
	}
	first.setRemoved(true)
	cards.setReaders()
	time.Sleep(3 * keepAliveEvery)
	if e := h.c.SubmitSecret("password", pw.PromptID, "correct horse battery"); e != nil {
		t.Fatalf("the prompt did not stand: %v", e)
	}
	if f := h.rec.waitCeremony(t, StepFailed, false); f.Error != CodeSlotPolicy {
		t.Fatalf("ended by something other than the policy: %+v", f)
	}
}

// Whether a slot can go is on the view, from the invariant, so the page
// greys Remove before any ceremony is run for it.
func TestRemovableFollowsTheInvariant(t *testing.T) {
	h := newHarness(t, nil, nil)
	for _, s := range h.c.Slots() {
		if s.Removable {
			t.Fatalf("one of two ways in marked removable: %+v", s)
		}
	}
	h.unlockWithPassword()
	h.rec.reset()
	if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollRecovery, Label: "Second paper"}); e != nil {
		t.Fatal(e)
	}
	p := h.rec.waitCeremony(t, StepPassword, true)
	h.c.SubmitSecret("password", p.PromptID, testPassword)
	h.rec.waitCeremony(t, StepRecovery, false)
	var removable int
	for _, s := range h.c.Slots() {
		if s.Removable {
			removable++
		}
	}
	// Recovery + recovery are disjoint, and so is either with the
	// password: every one of the three may go.
	if removable != 3 {
		t.Fatalf("removable after a third way in: %d of %+v", removable, h.c.Slots())
	}
}

// With the vault's password on, every hardware slot's required-secret set
// holds that one password, so two tokens are no longer disjoint from each
// other and the last recovery slot is what keeps the invariant: it cannot
// go (FORMAT.md §6.4, APP.md §13). With the switch off it can.
func TestRemovableFalseForTheLastRecoverySlotWhileEntangled(t *testing.T) {
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
		Entangle: &keystore.Entangle{Password: "the vault password", Argon2: fast},
	})
	if err != nil {
		t.Fatal(err)
	}
	ks := unl.Keystore()
	unl.Close()
	ks.Close()

	cards := &fakeCards{card: a}
	cards.setReaders("Yubico A")
	h := &harness{t: t, dir: dir, vault: vault, recovery: rk.Digits(), entangled: "the vault password", clk: newFakeClock(), rec: &recorder{}, cards: cards}
	c, err := New(Deps{Cards: cards, Events: h.rec, Clock: h.clk, DataDir: filepath.Join(dir, "data"), Log: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Start(); err != nil {
		t.Fatal(err)
	}
	h.c = c
	t.Cleanup(c.Close)
	if e := c.OpenVaultFile(vault, "Test vault"); e != nil {
		t.Fatal(e)
	}
	check := func(what string, wantRecovery bool) {
		t.Helper()
		for _, s := range h.c.Slots() {
			switch s.Type {
			case "recovery":
				if s.Removable != wantRecovery {
					t.Fatalf("%s: the recovery slot's Removable is %v: %+v", what, s.Removable, s)
				}
			case "hardware":
				if !s.Removable {
					t.Fatalf("%s: a token beside a second token and a recovery slot may not go: %+v", what, s)
				}
			}
		}
	}
	check("entangled", false)
	h.unlockWithToken()
	check("entangled, unlocked", false)

	h.rec.reset()
	if e := h.c.SetEntangled(false); e != nil {
		t.Fatal(e)
	}
	h.answerTokenUnlock("123456")
	h.rec.waitCeremony(t, StepDone, false)
	h.entangled = ""
	check("switch off", true)
}

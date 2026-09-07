package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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
	id, e := h.c.CreateArchive(filepath.Join(h.dir, "a.enf"), "A", false)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := h.c.OpenArchive(id); e != nil {
		t.Fatal(e)
	}
	f := filepath.Join(h.dir, "f.txt")
	os.WriteFile(f, []byte("owed"), 0o600)
	opID, _ := h.c.AddFiles(id, "", []string{f}, PolicySkip)
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
	id, _ := h.c.CreateArchive(filepath.Join(h.dir, "a.enf"), "A", false)
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

	// CreateVault to a bad path fails without touching the vault's facts.
	h.c.Lock()
	h.rec.waitState(t, StateLocked)
	h.rec.reset()
	bad := filepath.Join(h.dir, "no-such-dir", "v.eks")
	// An open archive refuses a new vault, whose registry its saves would
	// miss; closed, the create to a bad path fails without touching the
	// vault's facts.
	if e := h.c.CreateVault(bad, "New", EnrollOptions{Kind: EnrollPassword, Label: "pw"}, false); !isCode(e, CodeArchivesOpen) {
		t.Fatalf("create with an archive open: %v", e)
	}
	h.c.CloseArchive(id)
	if e := h.c.CreateVault(bad, "New", EnrollOptions{Kind: EnrollPassword, Label: "pw"}, false); e != nil {
		t.Fatal(e)
	}
	p := h.rec.waitCeremony(t, StepPassword, true)
	h.c.SubmitSecret("password", p.PromptID, "whatever")
	h.rec.waitCeremony(t, StepFailed, false)
	h.rec.waitState(t, StateLocked)
	if st := h.status(); st.Path != h.vault || st.DisplayName != "Test vault" {
		t.Fatalf("vault facts replaced by a failed create: %+v", st)
	}
	h.unlockWithPassword()
}

// Enrolling a token with an entangled password asks for the password
// before the key is touched (the prompt says it is a new one), and the
// new key then unlocks with password + PIN.
func TestEnrollTokenEntangledAsksPasswordFirst(t *testing.T) {
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
	opensBefore := cards.openCount()
	if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollToken, Label: "Travel key", Entangle: true}); e != nil {
		t.Fatal(e)
	}
	pin = h.rec.waitCeremony(t, StepPIN, true)
	if pin.Choose {
		t.Fatalf("the unlocking PIN is not a new secret: %+v", pin)
	}
	h.c.SubmitSecret("pin", pin.PromptID, "123456")
	// The password comes before the swap, and is marked as chosen now.
	pw := h.rec.waitCeremony(t, StepPassword, true)
	if !pw.Choose {
		t.Fatalf("the entangled password prompt is not marked choose: %+v", pw)
	}
	// By then the unlocking key has been released (it is never held across
	// a prompt) and no other card has been opened.
	deadline := time.Now().Add(2 * time.Second)
	for {
		first.mu.Lock()
		closed := first.closed
		first.mu.Unlock()
		if closed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the unlocking key is held across the password prompt")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := cards.openCount(); n != opensBefore+1 {
		t.Fatalf("cards opened before the password was chosen: %d (the unlocking open only was expected)", n-opensBefore)
	}
	second := newFakeCard("654321")
	second.mgmt = []byte("0123456789abcdef0123456789abcdef")
	h.c.SubmitSecret("password", pw.PromptID, "entangled words")
	h.rec.waitCeremony(t, StepSwapKey, false)
	if st := h.c.Status(); st.Ceremony == nil || st.Ceremony.Choose {
		t.Fatalf("choose survived the prompt: %+v", st.Ceremony)
	}
	cards.setReaders()
	time.Sleep(2 * readerPoll)
	cards.setCard(second)
	cards.setReaders("Yubico B")
	pin2 := h.rec.waitCeremony(t, StepPIN, true)
	h.c.SubmitSecret("pin", pin2.PromptID, "654321")
	h.rec.waitCeremony(t, StepDone, false)
	var entangled int
	for _, s := range h.c.Slots() {
		if s.Type == "hardware" && s.Entangled {
			entangled++
		}
	}
	if entangled != 1 {
		t.Fatalf("slots after enrolment: %+v", h.c.Slots())
	}

	// The new key needs the password, then its PIN.
	h.c.Lock()
	h.rec.waitState(t, StateLocked)
	h.rec.reset()
	if e := h.c.BeginUnlock(MethodToken); e != nil {
		t.Fatal(e)
	}
	pw = h.rec.waitCeremony(t, StepPassword, true)
	if pw.Choose {
		t.Fatalf("an existing password marked choose: %+v", pw)
	}
	h.c.SubmitSecret("password", pw.PromptID, "entangled words")
	pin = h.rec.waitCeremony(t, StepPIN, true)
	h.c.SubmitSecret("pin", pin.PromptID, "654321")
	h.rec.waitCeremony(t, StepDone, false)
	h.rec.waitState(t, StateUnlocked)
}

// The user may swap keys while the password prompt stands — in the same
// port, so the reader set never looks empty. The enrolment goes on with
// the key that is there instead of waiting for a removal that already
// happened; and while the unlocking key is still in, the swap step names
// it.
func TestEnrollSwapDuringPasswordPrompt(t *testing.T) {
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

	// First: the unlocking key stays in; the swap step says to remove it.
	h.rec.reset()
	if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollToken, Label: "Travel key", Entangle: true}); e != nil {
		t.Fatal(e)
	}
	pin = h.rec.waitCeremony(t, StepPIN, true)
	h.c.SubmitSecret("pin", pin.PromptID, "123456")
	pw := h.rec.waitCeremony(t, StepPassword, true)
	h.c.SubmitSecret("password", pw.PromptID, "entangled words")
	swap := h.rec.waitCeremony(t, StepSwapKey, false)
	if swap.RemoveLabel != "Test key" || swap.InsertLabel == "" {
		t.Fatalf("swap step does not name the key to remove: %+v", swap)
	}
	if e := h.c.CancelUnlock(); e != nil {
		t.Fatal(e)
	}
	h.rec.waitCeremony(t, StepFailed, false)

	// Then: the swap happens during the prompt, same reader name.
	h.rec.reset()
	if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollToken, Label: "Travel key", Entangle: true}); e != nil {
		t.Fatal(e)
	}
	pin = h.rec.waitCeremony(t, StepPIN, true)
	h.c.SubmitSecret("pin", pin.PromptID, "123456")
	pw = h.rec.waitCeremony(t, StepPassword, true)
	second := newFakeCard("654321")
	second.mgmt = []byte("0123456789abcdef0123456789abcdef")
	cards.setCard(second)
	h.c.SubmitSecret("password", pw.PromptID, "entangled words")
	pin2 := h.rec.waitCeremony(t, StepPIN, true)
	h.c.SubmitSecret("pin", pin2.PromptID, "654321")
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
}

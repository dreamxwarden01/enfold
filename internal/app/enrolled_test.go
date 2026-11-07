package app

import (
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dreamxwarden01/enfold/internal/format"
	"github.com/dreamxwarden01/enfold/internal/keystore"
)

// The tests of R1 (ruled 2026-11-06, APP.md §3 Keys): a YubiKey that
// already unlocks the vault, inserted as the one an enrolment adds, is
// refused on insertion, as a wait. The card is read (Keys: no PIN, no
// touch) before anything is asked of it; the state is the swap's,
// StepSwapKey, with RemoveLabel naming the unlock method it already is and
// vault.duplicate_slot as a note; the wait ends by itself when that card
// leaves and the enrolment goes on with the next one. The fake cards hand
// every reader the one current card, so a swap here is setCard and
// setReaders, and every assertion is on labels and public keys, never on
// serials (the fakes share one); Keys answers ErrTokenNoCard for a card
// pulled with setRemoved, as the real card does.

// waitSwap waits for the swap's state naming remove with note.
func (h *harness) waitSwap(remove string, note Code) CeremonyState {
	h.t.Helper()
	return h.rec.waitFor(h.t, EventVaultCeremony, func(p any) bool {
		s, ok := p.(CeremonyState)
		return ok && s.Step == StepSwapKey && s.RemoveLabel == remove && s.Error == note
	}).(CeremonyState)
}

// noPINSince fails the test if, from mark on and from the second half's
// first swap on, a PIN or management key was asked for, a touch waited for
// or a key generated: what R1 forbids for a card that already unlocks the
// vault. (A recovery key's proof derives before the second half.)
func (h *harness) noPINSince(mark int, what string) {
	h.t.Helper()
	second := false
	for _, s := range h.ceremoniesSince(mark) {
		second = second || s.Step == StepSwapKey
		if !second {
			continue
		}
		if s.Step == StepPIN || s.Step == StepManagementKey || s.Step == StepTouch || s.Step == StepDeriving {
			h.t.Fatalf("%s: asked at %s: %+v", what, s.Step, s)
		}
	}
}

// hardwareSlots is how many of the vault's unlock methods are YubiKeys.
func (h *harness) hardwareSlots() int {
	n := 0
	for _, s := range h.c.Slots() {
		if s.Type == "hardware" {
			n++
		}
	}
	return n
}

// beginTokenEnrolByYubiKey unlocks with the YubiKey in the reader, begins
// adding a YubiKey and proves with the same one, and returns the first
// swap's state: the proving YubiKey to come out.
func (h *harness) beginTokenEnrolByYubiKey() CeremonyState {
	h.t.Helper()
	h.unlockWithToken()
	h.rec.reset()
	if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollToken, Label: "New key"}); e != nil {
		h.t.Fatal(e)
	}
	h.answerTokenUnlock("123456")
	return h.rec.waitCeremony(h.t, StepSwapKey, false)
}

// The same YubiKey put back after a YubiKey's proof: its first swap keeps
// today's words (the proving YubiKey named, no note) and holds while it is
// in; pulled and put back, it is named with the note and asked nothing;
// pulled again, the wait clears by itself and a fresh YubiKey is enrolled.
// The events come in this order, each with a greater Seq, since the page
// keys its views on them.
func TestAYubiKeyPutBackIsRefusedOnInsertion(t *testing.T) {
	first, pubA := cardWithKey("123456")
	cards := readerWith(first)
	h := newHarness(t, cards, pubA)
	mark := len(h.rec.snapshot())
	swap := h.beginTokenEnrolByYubiKey()
	if swap.RemoveLabel != "Test key" || swap.Error != "" {
		t.Fatalf("the first swap: %+v", swap)
	}
	// Left in, the proving YubiKey holds the first swap, with no note.
	time.Sleep(3 * readerPoll)
	if st := h.status(); st.Ceremony == nil || st.Ceremony.Step != StepSwapKey || st.Ceremony.RemoveLabel != "Test key" || st.Ceremony.Error != "" {
		t.Fatalf("the first swap did not hold: %+v", st.Ceremony)
	}
	verifies, ecdhs := first.verifyCount(), first.ecdhCount()

	cards.setReaders()
	h.rec.waitCeremony(t, StepWaitingForKey, false)
	asked := h.mark()
	cards.setReaders("Yubico A")
	h.waitSwap("Test key", CodeDuplicateSlot)
	time.Sleep(3 * readerPoll)
	if st := h.status(); st.Ceremony == nil || st.Ceremony.Step != StepSwapKey || st.Ceremony.Error != CodeDuplicateSlot {
		t.Fatalf("the wait did not hold: %+v", st.Ceremony)
	}
	h.noPINSince(asked, "the YubiKey put back")
	if first.verifyCount() != verifies || first.ecdhCount() != ecdhs {
		t.Fatalf("the YubiKey put back was sent a VERIFY or an agreement: %d/%d, was %d/%d", first.verifyCount(), first.ecdhCount(), verifies, ecdhs)
	}

	cards.setReaders()
	h.waitSwap("", "")
	fresh, pubB := cardWithKey("654321")
	cards.setCard(fresh)
	cards.setReaders("Yubico B")
	pin := h.rec.waitCeremony(t, StepPIN, true)
	if pin.SlotLabel != "New key" || pin.Error != "" {
		t.Fatalf("the new YubiKey's PIN: %+v", pin)
	}
	h.c.SubmitSecret("pin", pin.PromptID, "654321")
	h.done()
	if n := h.hardwareSlots(); n != 2 {
		t.Fatalf("%d YubiKeys after the enrolment: %+v", n, h.c.Slots())
	}
	// The fresh YubiKey's 9d key is the one enrolled: the vault's slots
	// hold its public key, and it proved itself.
	h.slot("New key")
	h.c.mu.Lock()
	var enrolled bool
	for _, s := range h.c.vault.slots {
		enrolled = enrolled || string(s.PublicKey) == string(pubB)
	}
	h.c.mu.Unlock()
	if !enrolled || fresh.ecdhCount() == 0 {
		t.Fatalf("the YubiKey enrolled is not the fresh one: enrolled %v, %d agreements", enrolled, fresh.ecdhCount())
	}

	// The events in order: the first swap, its end, the empty reader, the
	// refusal as a wait, its end, the empty reader, the new YubiKey's PIN,
	// its touch, Done; every Seq greater than the last.
	type look struct {
		step   CeremonyStep
		remove string
		note   Code
	}
	want := []look{
		{StepSwapKey, "Test key", ""},
		{StepSwapKey, "", ""},
		{StepWaitingForKey, "", ""},
		{StepSwapKey, "Test key", CodeDuplicateSlot},
		{StepSwapKey, "", ""},
		{StepWaitingForKey, "", ""},
		{StepPIN, "", ""},
		{StepTouch, "", ""},
		{StepDone, "", ""},
	}
	states := h.ceremoniesSince(mark)
	var last uint64
	i := 0
	for k, s := range states {
		if s.Seq <= last {
			t.Fatalf("Seq not increasing at %d: %d after %d", k, s.Seq, last)
		}
		last = s.Seq
		if s.Kind != "enroll" || i == len(want) {
			continue
		}
		w := want[i]
		if s.Step == w.step && s.RemoveLabel == w.remove && s.Error == w.note {
			if w.note == CodeDuplicateSlot && k+1 < len(states) {
				if next := states[k+1]; next.Step != StepSwapKey || next.RemoveLabel != "" || next.Error != "" {
					t.Fatalf("the refusal's wait was followed by %+v", next)
				}
			}
			i++
		}
	}
	if i != len(want) {
		t.Fatalf("the events lack %+v; saw %+v", want[i], states)
	}
}

// Another YubiKey of the vault swapped in for the proving one, in the same
// port, is named in its turn and asked nothing; swapped back to the proving
// one without the reader ever emptying, the wait names that one instead;
// a fresh YubiKey ends it.
func TestAnotherYubiKeyOfTheVaultIsRefusedOnInsertion(t *testing.T) {
	cardA, pubA := cardWithKey("123456")
	cardB, pubB := cardWithKey("123456")
	r, digits := newRecoverySpec(t, "Paper")
	cards := readerWith(cardA)
	h := rekeyHarness(t, cards, "", digits,
		keystore.HardwareSlot{PublicKey: pubA, Label: "Office key"},
		keystore.HardwareSlot{PublicKey: pubB, Label: "Home key"}, r)
	swap := h.beginTokenEnrolByYubiKey()
	if swap.RemoveLabel != "Office key" {
		t.Fatalf("the first swap: %+v", swap)
	}
	asked := h.mark()
	cards.setCard(cardB)
	h.waitSwap("Home key", CodeDuplicateSlot)
	cards.setCard(cardA)
	h.waitSwap("Office key", CodeDuplicateSlot)
	h.noPINSince(asked, "a YubiKey of the vault")
	if cardB.verifyCount() != 0 || cardB.ecdhCount() != 0 {
		t.Fatalf("the other YubiKey of the vault was asked: %d/%d", cardB.verifyCount(), cardB.ecdhCount())
	}
	fresh, _ := cardWithKey("654321")
	cards.setCard(fresh)
	h.waitSwap("", "")
	pin := h.rec.waitCeremony(t, StepPIN, true)
	h.c.SubmitSecret("pin", pin.PromptID, "654321")
	h.done()
	if n := h.hardwareSlots(); n != 3 {
		t.Fatalf("%d YubiKeys after the enrolment", n)
	}
}

// A recovery key's proof has no first swap: a YubiKey of the vault in the
// reader from the start is the refusal's wait at once, never its PIN.
func TestARecoveryProofWithAYubiKeyOfTheVaultIn(t *testing.T) {
	cardA, pubA := cardWithKey("123456")
	cards := readerWith(cardA)
	r, digits := newRecoverySpec(t, "Paper")
	h := rekeyHarness(t, cards, "", digits, keystore.HardwareSlot{PublicKey: pubA, Label: "A"}, r)
	h.unlockWithRecovery(digits)
	h.rec.reset()
	mark := h.mark()
	if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollToken, Label: "new", WithRecoveryKey: true}); e != nil {
		t.Fatal(e)
	}
	h.answerRecovery(h.recovery)
	h.waitSwap("A", CodeDuplicateSlot)
	h.noPINSince(mark, "a recovery proof's YubiKey of the vault")
	if cardA.verifyCount() != 0 {
		t.Fatalf("the YubiKey of the vault was sent %d VERIFYs", cardA.verifyCount())
	}
	cards.setReaders()
	h.waitSwap("", "")
	cardB, _ := cardWithKey("654321")
	cards.setCard(cardB)
	cards.setReaders("Yubico B")
	pin := h.rec.waitCeremony(t, StepPIN, true)
	h.c.SubmitSecret("pin", pin.PromptID, "654321")
	h.done()
	if n := len(h.c.Slots()); n != 3 {
		t.Fatalf("%d slots", n)
	}
}

// A YubiKey whose key in a retired slot is already an unlock method was
// given a second key and enrolled twice when its 9d key was unusable (a
// key generated in the next retired slot) or usable but not the vault's
// (9d reused). Every key on the card is compared, so both are refused,
// asked nothing, and nothing is generated; Cancel ends the wait, and no
// unlock method is added.
func TestAYubiKeyEnrolledThroughARetiredSlotIsNotEnrolledTwice(t *testing.T) {
	for _, usable9d := range []bool{false, true} {
		card := newFakeCard("123456")
		card.mgmt = []byte("0123456789abcdef0123456789abcdef")
		card.addKey(0x9d, usable9d)
		pub82 := card.addKey(0x82, true)
		cards := readerWith(card)
		r, digits := newRecoverySpec(t, "Paper")
		h := rekeyHarness(t, cards, "", digits, keystore.HardwareSlot{PublicKey: pub82, Label: "Retired-slot key"}, r)
		h.unlockWithRecovery(digits)
		h.rec.reset()
		mark := h.mark()
		if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollToken, Label: "again", WithRecoveryKey: true}); e != nil {
			t.Fatal(e)
		}
		h.answerRecovery(h.recovery)
		h.waitSwap("Retired-slot key", CodeDuplicateSlot)
		time.Sleep(2 * readerPoll)
		h.noPINSince(mark, "a YubiKey enrolled through a retired slot")
		card.mu.Lock()
		_, generated := card.keys[0x83]
		card.mu.Unlock()
		if generated || card.verifyCount() != 0 || card.ecdhCount() != 0 {
			t.Fatalf("usable 9d %v: generated %v, %d VERIFYs, %d agreements", usable9d, generated, card.verifyCount(), card.ecdhCount())
		}
		h.c.CancelUnlock()
		if f := h.end(); f.Step != StepFailed || f.Error != CodeCancelled {
			t.Fatalf("Cancel during the wait: %+v", f)
		}
		if n := len(h.c.Slots()); n != 2 {
			t.Fatalf("usable 9d %v: %d slots after the cancel", usable9d, n)
		}
	}
}

// A card swapped between the check and the enrolment cannot slip through:
// the card checked is held from the check on, so a swap during the fresh
// card's PIN comes back through the gone-key wait, and the YubiKey of the
// vault swapped in is checked again and refused. Put back, the fresh card
// is enrolled.
func TestACardSwappedAfterTheCheckIsCheckedAgain(t *testing.T) {
	old := keepAliveEvery
	keepAliveEvery = 30 * time.Millisecond
	defer func() { keepAliveEvery = old }()
	cardA, pubA := cardWithKey("123456")
	cards := readerWith(cardA)
	h := newHarness(t, cards, pubA)
	h.beginTokenEnrolByYubiKey()
	cards.setReaders()
	h.rec.waitCeremony(t, StepWaitingForKey, false)
	fresh := newFakeCard("654321")
	fresh.mgmt = []byte("0123456789abcdef0123456789abcdef")
	cards.setCard(fresh)
	cards.setReaders("Yubico A")
	pin := h.rec.waitCeremony(t, StepPIN, true)
	verifies := cardA.verifyCount()
	asked := h.mark()
	fresh.setRemoved(true)
	cards.setCard(cardA)
	w := h.rec.waitCeremony(t, StepWaitingForKey, false)
	if w.Error != CodeTokenNoCard {
		t.Fatalf("waiting again without the note: %+v", w)
	}
	h.waitSwap("Test key", CodeDuplicateSlot)
	h.noPINSince(asked, "the YubiKey of the vault swapped in")
	if cardA.verifyCount() != verifies {
		t.Fatal("the YubiKey of the vault swapped in was sent a VERIFY")
	}
	cards.setReaders()
	h.waitSwap("", "")
	fresh.setRemoved(false)
	cards.setCard(fresh)
	cards.setReaders("Yubico A")
	pin2 := h.rec.waitCeremony(t, StepPIN, true)
	if pin2.PromptID == pin.PromptID {
		t.Fatal("no new PIN prompt for the fresh YubiKey")
	}
	h.c.SubmitSecret("pin", pin2.PromptID, "654321")
	h.done()
	if n := h.hardwareSlots(); n != 2 {
		t.Fatalf("%d YubiKeys after the enrolment", n)
	}
}

// Two readers end the first swap while the proving YubiKey is still in;
// the fresh one taken out again leaves the proving one alone, which is now
// the refusal's wait rather than its PIN and touch.
func TestTwoReadersThenTheProvingYubiKeyAlone(t *testing.T) {
	cardA, pubA := cardWithKey("123456")
	cards := readerWith(cardA)
	h := newHarness(t, cards, pubA)
	h.beginTokenEnrolByYubiKey()
	asked := h.mark()
	verifies := cardA.verifyCount()
	cards.setReaders("Yubico A", "Yubico B")
	h.rec.waitCeremony(t, StepTwoKeys, false)
	cards.setReaders("Yubico A")
	h.waitSwap("Test key", CodeDuplicateSlot)
	h.noPINSince(asked, "the proving YubiKey left alone")
	if cardA.verifyCount() != verifies {
		t.Fatal("the proving YubiKey was sent a VERIFY")
	}
	h.c.CancelUnlock()
	if f := h.end(); f.Step != StepFailed || f.Error != CodeCancelled {
		t.Fatalf("Cancel: %+v", f)
	}
}

// The refusal's wait is bounded by the second half's one deadline, armed
// once: a wait that comes twice does not lengthen it, and at the deadline
// the act ends as a wait's deadline ends it. The session's own timers are
// stopped first, so that the deadline, not the idle lock, is what ends it.
func TestTheRefusalsWaitIsBounded(t *testing.T) {
	cardA, pubA := cardWithKey("123456")
	r, digits := newRecoverySpec(t, "Paper")
	cards := readerWith(cardA)
	h := rekeyHarness(t, cards, "", digits, keystore.HardwareSlot{PublicKey: pubA, Label: "A"}, r)
	h.unlockWithRecovery(digits)
	h.c.mu.Lock()
	for _, tm := range []Timer{h.c.vault.idleTimer, h.c.vault.absTimer} {
		if tm != nil {
			tm.Stop()
		}
	}
	h.c.mu.Unlock()
	h.rec.reset()
	if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollToken, Label: "new", WithRecoveryKey: true}); e != nil {
		t.Fatal(e)
	}
	h.answerRecovery(h.recovery)
	h.waitSwap("A", CodeDuplicateSlot)
	h.clk.Advance(promptWait - 10*time.Minute)
	cards.setReaders()
	h.waitSwap("", "")
	cards.setReaders("Yubico A")
	h.waitSwap("A", CodeDuplicateSlot)
	if st := h.status(); st.Ceremony == nil || st.Ceremony.Step != StepSwapKey {
		t.Fatalf("ended before its deadline: %+v", st.Ceremony)
	}
	h.clk.Advance(10 * time.Minute)
	if f := h.end(); tupleOf(f) != (tuple{StepFailed, "A", "", CodeCancelled}) {
		t.Fatalf("at the deadline: %+v", f)
	}
	if !h.logged("left by wait_deadline") {
		t.Fatal("the deadline is not what ended the wait")
	}
	if n := len(h.c.Slots()); n != 2 {
		t.Fatalf("%d slots", n)
	}
}

// ---- The repairs the outside audit of 2026-11-06 asked for (A1 to A4).

// tuple is what the page tells a ceremony's views apart by: the step, the
// YubiKey named to come out, the one named on the chip, and the note.
type tuple struct {
	step   CeremonyStep
	remove string
	slot   string
	note   Code
}

func tupleOf(s CeremonyState) tuple { return tuple{s.Step, s.RemoveLabel, s.SlotLabel, s.Error} }

// tuplesSince are the ceremony's states from mark on, as tuples.
func (h *harness) tuplesSince(mark int) []tuple {
	var out []tuple
	for _, s := range h.ceremoniesSince(mark) {
		out = append(out, tupleOf(s))
	}
	return out
}

// exactly waits for len(want) states from mark on, then three polls more,
// and fails the test unless the states are exactly want: every event, in
// order, with nothing between and nothing after.
func (h *harness) exactly(mark int, what string, want ...tuple) {
	h.t.Helper()
	limit := time.Now().Add(10 * time.Second)
	for len(h.tuplesSince(mark)) < len(want) && time.Now().Before(limit) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(3 * readerPoll)
	if got := h.tuplesSince(mark); !slices.Equal(got, want) {
		h.t.Fatalf("%s:\n got  %+v\n want %+v", what, got, want)
	}
}

// atLeast waits until count() reaches want, under a generous timeout,
// and fails the test with what otherwise: a wait for what the core was
// seen to do, not for a time it would take on an idle machine (the
// 2026-11-06 outside audit's second pass, U3).
func (h *harness) atLeast(what string, count func() int, want int) {
	h.t.Helper()
	limit := time.Now().Add(30 * time.Second)
	for count() < want {
		if time.Now().After(limit) {
			h.t.Fatalf("%s: %d, want at least %d", what, count(), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitTuple waits for the state w.
func (h *harness) waitTuple(w tuple) {
	h.t.Helper()
	h.rec.waitFor(h.t, EventVaultCeremony, func(p any) bool {
		s, ok := p.(CeremonyState)
		return ok && tupleOf(s) == w
	})
}

// watchCards arms the cards' close hooks: a card closed while the ceremony
// still names it its held card is counted, since every exit that closes a
// card by hand must forget it first (unhold). Every close is looked at:
// the hook takes the core's mutex with TryLock, retried while another
// goroutine holds it, since a close made under the mutex itself could
// never take it; a close the hook could not look at within a second —
// one made under the mutex, which no exit does — is counted as well, so
// that cardsSettled fails rather than passing over it.
func (h *harness) watchCards(cards ...*fakeCard) *atomic.Int32 {
	heldAtClose := new(atomic.Int32)
	for _, card := range cards {
		card.mu.Lock()
		card.onClose = func() {
			limit := time.Now().Add(time.Second)
			for !h.c.mu.TryLock() {
				if time.Now().After(limit) {
					h.t.Errorf("a close of a card made under the core's mutex, or held from it for a second")
					heldAtClose.Add(1)
					return
				}
				time.Sleep(50 * time.Microsecond)
			}
			if h.c.cer != nil && h.c.cer.held == Card(card) {
				heldAtClose.Add(1)
			}
			h.c.mu.Unlock()
		}
		card.mu.Unlock()
	}
	return heldAtClose
}

// cardsSettled fails the test unless, the ceremony over, every open of each
// card was closed exactly once, none opened while an open of it stood,
// none closed while still held, no card was left held, and no failed
// reset was warned of from mark on: nothing on these exits sent a VERIFY.
func (h *harness) cardsSettled(mark int, heldAtClose *atomic.Int32, cards ...*fakeCard) {
	h.t.Helper()
	for i, card := range cards {
		card.mu.Lock()
		opened, closes, reopened, over := card.opened, card.closes, card.reopened, card.overClose
		card.mu.Unlock()
		if opened != closes || reopened != 0 || over != 0 {
			h.t.Fatalf("card %d: %d opens, %d closes, %d opened while open, %d closed twice", i, opened, closes, reopened, over)
		}
	}
	if n := heldAtClose.Load(); n != 0 {
		h.t.Fatalf("%d cards closed while still held", n)
	}
	if h.logged("the key was still held at the end") {
		h.t.Fatal("a card was left held at the end")
	}
	for _, e := range h.rec.snapshot()[mark:] {
		if w, ok := e.payload.(Warning); ok && w.Code == CodeTokenReset {
			h.t.Fatal("a failed reset was warned of")
		}
	}
	if slices.Contains(h.status().Warnings, CodeTokenReset) {
		h.t.Fatal("the status carries a failed reset")
	}
}

// failResets makes every close of the cards report a failed reset, which
// a close after a VERIFY would warn of.
func failResets(cards ...*fakeCard) {
	for _, card := range cards {
		card.mu.Lock()
		card.closeErr = ErrTokenResetFailed
		card.mu.Unlock()
	}
}

// enrolByRecovery is a vault of specs and a recovery key, unlocked with
// the recovery key, adding a YubiKey ("New key") proved by the recovery
// key, card in the reader. The mark is from before the act began.
func enrolByRecovery(t *testing.T, card *fakeCard, specs ...keystore.SlotSpec) (*harness, *fakeCards, int) {
	t.Helper()
	cards := readerWith(card)
	r, digits := newRecoverySpec(t, "Paper")
	h := rekeyHarness(t, cards, "", digits, append(specs, r)...)
	h.unlockWithRecovery(digits)
	h.rec.reset()
	mark := h.mark()
	if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollToken, Label: "New key", WithRecoveryKey: true}); e != nil {
		t.Fatal(e)
	}
	h.answerRecovery(h.recovery)
	return h, cards, mark
}

// fromSwap are the tuples from the first swap's state on.
func fromSwap(all []tuple) []tuple {
	for i, x := range all {
		if x.step == StepSwapKey {
			return all[i:]
		}
	}
	return nil
}

// A1: the wait's probe never parks. A card another program holds is a
// poll that told nothing, for a moment or for longer than openCard's
// retries (which park the first swap's probe and the enrolment's open),
// and the label and the note stand; free again, the wait goes on, and a
// fresh YubiKey ends it. The state is never Busy.
func TestABusyCardNeverParksTheRefusalsWait(t *testing.T) {
	oldRetries, oldEvery := busyRetries, busyRetryEvery
	busyRetries, busyRetryEvery = 1, 10*time.Millisecond // openCard would park within 20 ms
	defer func() { busyRetries, busyRetryEvery = oldRetries, oldEvery }()
	cardA, pubA := cardWithKey("123456")
	h, cards, _ := enrolByRecovery(t, cardA, keystore.HardwareSlot{PublicKey: pubA, Label: "A"})
	h.waitSwap("A", CodeDuplicateSlot)
	mark := h.mark()
	attempts := cards.openAttempts()
	cards.setOpenErr(ErrTokenBusy)
	// Three probes while busy, waited for as they are seen; then nothing
	// may have been said of them.
	h.atLeast("the wait's probes while busy", func() int { return cards.openAttempts() - attempts }, 3)
	h.exactly(mark, "a busy reader during the wait")
	cardA.setKeysErr(ErrTokenBusy) // busy at the read too
	cards.setOpenErr(nil)
	h.exactly(mark, "a busy read during the wait")
	cardA.setKeysErr(nil)
	cards.mu.Lock()
	cards.busyOpens = 3
	cards.mu.Unlock()
	h.exactly(mark, "a short busy spell during the wait")
	if !h.logged("the reader is busy; still waiting") {
		t.Fatal("the busy spell is not in the log")
	}
	fresh, _ := cardWithKey("654321")
	cards.setCard(fresh)
	h.waitSwap("", "")
	pin := h.rec.waitCeremony(t, StepPIN, true)
	h.c.SubmitSecret("pin", pin.PromptID, "654321")
	h.done()
	for _, s := range h.ceremoniesSince(0) {
		if s.Step == StepBusy {
			t.Fatalf("the wait parked: %+v", s)
		}
	}
	if n := h.hardwareSlots(); n != 2 {
		t.Fatalf("%d YubiKeys", n)
	}
}

// A1: an open that fails for another reason than a card gone or busy ends
// the wait as "none of the vault's as far as can be told", and the
// enrolment meets that device as it meets any device inserted: no PIV
// parks it at its failure, as before the refusal existed; Cancel ends it.
func TestADeviceWithoutPIVEndsTheRefusalsWait(t *testing.T) {
	cardA, pubA := cardWithKey("123456")
	h, cards, _ := enrolByRecovery(t, cardA, keystore.HardwareSlot{PublicKey: pubA, Label: "A"})
	h.waitSwap("A", CodeDuplicateSlot)
	mark := h.mark()
	cards.setOpenErr(ErrTokenNoPIV)
	h.exactly(mark, "no PIV during the wait",
		tuple{StepSwapKey, "", "", ""},
		tuple{StepFailed, "", "", CodeTokenNoPIV})
	mark = h.mark()
	h.c.CancelUnlock()
	h.waitTuple(tuple{StepFailed, "", "", CodeCancelled})
	h.rec.waitCeremonyEnded(t, h.ceremoniesSince(mark)[0])
}

// A4: a lock landing during the wait ends the act cancelled, holds no
// card and adds nothing (the core reviewer's scratch test, adapted).
func TestALockDuringTheRefusalsWait(t *testing.T) {
	cardA, pubA := cardWithKey("123456")
	h, _, begun := enrolByRecovery(t, cardA, keystore.HardwareSlot{PublicKey: pubA, Label: "A"})
	held := h.watchCards(cardA)
	failResets(cardA)
	h.waitSwap("A", CodeDuplicateSlot)
	mark := h.mark()
	h.c.LockNow(ReasonWorkstation)
	if f := h.end(); tupleOf(f) != (tuple{StepFailed, "A", "", CodeCancelled}) {
		t.Fatalf("after the lock: %+v", f)
	}
	h.rec.waitState(t, StateLocked)
	h.c.mu.Lock()
	cer := h.c.cer
	h.c.mu.Unlock()
	if cer != nil {
		t.Fatal("a ceremony stands after the lock")
	}
	if cardA.verifyCount() != 0 {
		t.Fatal("VERIFY sent")
	}
	if got := h.tuplesSince(mark); len(got) != 1 {
		t.Fatalf("the lock's events: %+v", got)
	}
	h.cardsSettled(begun, held, cardA)
	if n := len(h.c.Slots()); n != 2 {
		t.Fatalf("%d slots", n)
	}
}

// A4: a YubiKey half out, its reader still listed, is a poll that told
// nothing, whether the open or the read of its keys finds it gone: no
// event at all, the note standing; its reader gone, the wait ends with no
// note left on the empty reader's state.
func TestAYubiKeyPulledWhileItsReaderStaysListed(t *testing.T) {
	for _, how := range []string{"open", "keys"} {
		cardA, pubA := cardWithKey("123456")
		h, cards, begun := enrolByRecovery(t, cardA, keystore.HardwareSlot{PublicKey: pubA, Label: "A"})
		held := h.watchCards(cardA)
		h.waitSwap("A", CodeDuplicateSlot)
		mark := h.mark()
		if how == "open" {
			cards.setOpenErr(ErrTokenNoCard)
		} else {
			cardA.setRemoved(true)
		}
		h.exactly(mark, how+": half out")
		cards.setOpenErr(nil)
		cards.setReaders()
		h.exactly(mark, how+": out",
			tuple{StepSwapKey, "", "", ""},
			tuple{StepWaitingForKey, "", "", ""})
		h.c.CancelUnlock()
		h.end()
		h.cardsSettled(begun, held, cardA)
	}
}

// A4: the Smart Card service stopping during the wait (the last reader
// gone) ends it as an empty reader set does; staying down, the waiting
// state says so; back with a fresh YubiKey, the note clears before the
// enrolment goes on.
func TestTheServiceStoppingDuringTheRefusalsWait(t *testing.T) {
	old := noServiceNoteAfter
	noServiceNoteAfter = 3
	defer func() { noServiceNoteAfter = old }()
	cardA, pubA := cardWithKey("123456")
	h, cards, _ := enrolByRecovery(t, cardA, keystore.HardwareSlot{PublicKey: pubA, Label: "A"})
	h.waitSwap("A", CodeDuplicateSlot)
	mark := h.mark()
	cards.setReadersErr(ErrTokenNoService)
	h.exactly(mark, "the service stopped",
		tuple{StepSwapKey, "", "", ""},
		tuple{StepWaitingForKey, "", "", ""},
		tuple{StepWaitingForKey, "", "", CodeTokenNoService})
	mark = h.mark()
	fresh, _ := cardWithKey("654321")
	cards.setCard(fresh)
	cards.setReadersErr(nil)
	pin := h.rec.waitCeremony(t, StepPIN, true)
	if got := h.tuplesSince(mark); len(got) < 2 || got[0] != (tuple{StepWaitingForKey, "", "", ""}) {
		t.Fatalf("the service back: %+v", got)
	}
	h.c.SubmitSecret("pin", pin.PromptID, "654321")
	h.done()
}

// A4: two readers during the wait end it; TwoKeys follows; the fresh one
// alone is enrolled, its PIN alone asked (the core reviewer's, adapted).
func TestTwoReadersDuringTheRefusalsWait(t *testing.T) {
	cardA, pubA := cardWithKey("123456")
	h, cards, _ := enrolByRecovery(t, cardA, keystore.HardwareSlot{PublicKey: pubA, Label: "A"})
	h.waitSwap("A", CodeDuplicateSlot)
	mark := h.mark()
	cards.setReaders("Yubico A", "Yubico B")
	h.exactly(mark, "two readers",
		tuple{StepSwapKey, "", "", ""},
		tuple{StepTwoKeys, "", "", ""})
	fresh, _ := cardWithKey("654321")
	cards.setCard(fresh)
	cards.setReaders("Yubico B")
	pin := h.rec.waitCeremony(t, StepPIN, true)
	if pin.Error != "" || pin.RemoveLabel != "" || pin.SlotLabel != "New key" {
		t.Fatalf("pin: %+v", pin)
	}
	h.c.SubmitSecret("pin", pin.PromptID, "654321")
	h.done()
}

// A4: a vault with no YubiKey and a standalone password: adding a YubiKey
// is refused before the proof and before any card is opened, as
// vault.slot_policy (ruled 2026-11-07; until then the enrolment ran its PIN
// and touch and the keystore refused it at AddSlot, after a key could have
// been generated on the card). The way there is Switch to a YubiKey…
// (switch_test.go).
func TestAVaultWithNoYubiKeyHasNothingToRefuse(t *testing.T) {
	fresh, _ := cardWithKey("654321")
	cards := readerWith(fresh)
	h := newHarness(t, cards, nil)
	h.unlockWithPassword()
	h.rec.reset()
	mark := h.mark()
	for _, recovery := range []bool{false, true} {
		if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollToken, Label: "first", WithRecoveryKey: recovery}); !isCode(e, CodeSlotPolicy) {
			t.Fatalf("a YubiKey beside the password (recovery proof %v): %v", recovery, e)
		}
	}
	h.noCeremonySince(mark, "the refused enrolment")
	if cards.openAttempts() != 0 || fresh.openedCount() != 0 {
		t.Fatalf("%d opens of the reader before the refusal", cards.openAttempts())
	}
	// A password and a recovery key are still added as before.
	if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollRecovery, Label: "Second sheet"}); e != nil {
		t.Fatal(e)
	}
	h.proveWithPassword()
	h.rec.waitCeremony(t, StepRecovery, false)
}

// A4: the check's every exit and the wait's every probe close each card
// they open exactly once, forget it before closing it, and never warn of
// a failed reset, since nothing there sent a VERIFY: a match (the check,
// then every poll), a key gone at the read, a card none of the vault's
// (the probe's end, the check's pass), across a whole enrolment.
func TestTheRefusalOpensAndClosesEachCardOnce(t *testing.T) {
	cardA, pubA := cardWithKey("123456")
	cardB, pubB := cardWithKey("123456")
	h, cards, begun := enrolByRecovery(t, cardA,
		keystore.HardwareSlot{PublicKey: pubA, Label: "A"}, keystore.HardwareSlot{PublicKey: pubB, Label: "B"})
	fresh, _ := cardWithKey("654321")
	held := h.watchCards(cardA, cardB, fresh)
	failResets(cardA, cardB)
	h.waitSwap("A", CodeDuplicateSlot)
	// The check's open and one probe of the wait at least, each closed.
	h.atLeast("A's opens closed during its wait", cardA.closeCount, 2)
	cards.setCard(cardB)
	h.waitSwap("B", CodeDuplicateSlot)
	h.atLeast("B's opens closed during its wait", cardB.closeCount, 2)
	// Gone at the read, its reader listed: a probe that began after the
	// removal has opened it, met it gone and closed it.
	cardB.setRemoved(true)
	h.atLeast("B's probes closed while gone at the read", cardB.closeCount, cardB.openedCount()+1)
	cardB.setRemoved(false)
	cards.setReaders()
	h.waitSwap("", "")
	h.rec.waitCeremony(t, StepWaitingForKey, false)
	cards.setCard(cardA)
	cards.setReaders("Yubico A")
	h.waitSwap("A", CodeDuplicateSlot)
	h.atLeast("A's opens once it is back", cardA.openedCount, 3)
	cards.setCard(fresh)
	pin := h.rec.waitCeremony(t, StepPIN, true)
	h.c.SubmitSecret("pin", pin.PromptID, "654321")
	h.done()
	h.cardsSettled(begun, held, cardA, cardB, fresh)
	if cardA.verifyCount() != 0 || cardB.verifyCount() != 0 {
		t.Fatal("a YubiKey of the vault was sent a VERIFY")
	}
	if cardA.openedCount() < 3 || cardB.openedCount() < 3 {
		t.Fatalf("the probes did not run: A %d, B %d opens", cardA.openedCount(), cardB.openedCount())
	}
}

// A4: a read of the keys that fails for another reason than a card gone or
// busy. At the check, the act fails as a failed read failed it before the
// check existed, the card closed once and unverified; during the wait, the
// wait ends (none of the vault's, as far as can be told) and the
// enrolment's own check meets the same failure.
func TestAKeysErrorAtTheCheckAndDuringTheWait(t *testing.T) {
	cardA, pubA := cardWithKey("123456")
	odd, _ := cardWithKey("654321")
	odd.setKeysErr(errInjected)
	h, _, begun := enrolByRecovery(t, odd, keystore.HardwareSlot{PublicKey: pubA, Label: "A"})
	held := h.watchCards(odd)
	failResets(odd)
	f := h.end()
	if f.Step != StepFailed || f.Error == "" || f.Error == CodeDuplicateSlot || f.Error == CodeCancelled {
		t.Fatalf("a failed read at the check: %+v", f)
	}
	atCheck := f.Error
	h.cardsSettled(begun, held, odd)
	if odd.openedCount() != 1 || odd.verifyCount() != 0 {
		t.Fatalf("%d opens, %d VERIFYs", odd.openedCount(), odd.verifyCount())
	}
	if got := fromSwap(h.tuplesSince(begun)); !slices.Equal(got, []tuple{{StepSwapKey, "", "", ""}, {StepFailed, "", "", atCheck}}) {
		t.Fatalf("the events: %+v", got)
	}

	h2, _, begun2 := enrolByRecovery(t, cardA, keystore.HardwareSlot{PublicKey: pubA, Label: "A"})
	held2 := h2.watchCards(cardA)
	failResets(cardA)
	h2.waitSwap("A", CodeDuplicateSlot)
	mark := h2.mark()
	cardA.setKeysErr(errInjected)
	h2.exactly(mark, "a failed read during the wait",
		tuple{StepSwapKey, "", "", ""},
		tuple{StepFailed, "", "", atCheck})
	h2.rec.waitCeremonyEnded(t, h2.ceremoniesSince(mark)[1])
	h2.cardsSettled(begun2, held2, cardA)
	if !h2.logged("reading the keys while waiting") {
		t.Fatal("the probe's failed read is not in the log")
	}
}

// A4: a YubiKey whose vault key is an UNUSABLE key (no touch policy, say)
// is the vault's all the same: the comparison is by public key, usable or
// not, so it is refused, asked nothing, and nothing is generated on it.
func TestAMatchingUnusableKeyIsRefused(t *testing.T) {
	card := newFakeCard("123456")
	card.mgmt = []byte("0123456789abcdef0123456789abcdef")
	pub := card.addKey(0x9d, false)
	h, _, begun := enrolByRecovery(t, card, keystore.HardwareSlot{PublicKey: pub, Label: "No-touch key"})
	h.waitSwap("No-touch key", CodeDuplicateSlot)
	h.exactly(h.mark(), "the unusable key's wait holds")
	if got := fromSwap(h.tuplesSince(begun)); !slices.Equal(got, []tuple{{StepSwapKey, "", "", ""}, {StepSwapKey, "No-touch key", "", CodeDuplicateSlot}}) {
		t.Fatalf("the events: %+v", got)
	}
	card.mu.Lock()
	n := len(card.keys)
	card.mu.Unlock()
	if n != 1 || card.verifyCount() != 0 || card.ecdhCount() != 0 {
		t.Fatalf("%d keys on the card, %d VERIFYs, %d agreements", n, card.verifyCount(), card.ecdhCount())
	}
	h.c.CancelUnlock()
	h.end()
}

// A4: a swap exactly between the check and the enrolment: the fresh card
// passes the check, and before the enrolment reads it again it is pulled
// and a YubiKey of the vault put in its place. The card checked is the
// card held, so the enrolment finds it gone, waits again, and the YubiKey
// of the vault comes back through the check and is refused; the fresh
// one, put back, is enrolled.
func TestASwapBetweenTheCheckAndTheEnrolment(t *testing.T) {
	cardA, pubA := cardWithKey("123456")
	fresh, pubF := cardWithKey("654321")
	cards := readerWith(fresh)
	var armed atomic.Bool
	armed.Store(true)
	fresh.onKeys = func() {
		if armed.CompareAndSwap(true, false) {
			fresh.setRemoved(true)
			cards.setCard(cardA)
		}
	}
	r, digits := newRecoverySpec(t, "Paper")
	h := rekeyHarness(t, cards, "", digits, keystore.HardwareSlot{PublicKey: pubA, Label: "A"}, r)
	h.unlockWithRecovery(digits)
	h.rec.reset()
	begun := h.mark()
	if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollToken, Label: "New key", WithRecoveryKey: true}); e != nil {
		t.Fatal(e)
	}
	h.answerRecovery(h.recovery)
	h.waitSwap("A", CodeDuplicateSlot)
	if got := fromSwap(h.tuplesSince(begun)); !slices.Equal(got, []tuple{
		{StepSwapKey, "", "", ""},
		{StepSwapKey, "", "New key", ""},
		{StepWaitingForKey, "", "New key", CodeTokenNoCard},
		{StepSwapKey, "A", "New key", CodeDuplicateSlot},
	}) {
		t.Fatalf("the events: %+v", got)
	}
	if fresh.verifyCount() != 0 || cardA.verifyCount() != 0 || fresh.ecdhCount() != 0 {
		t.Fatal("a VERIFY or an agreement before the refusal")
	}
	cards.setReaders()
	h.waitSwap("", "")
	fresh.setRemoved(false)
	cards.setCard(fresh)
	cards.setReaders("Yubico A")
	pin := h.rec.waitCeremony(t, StepPIN, true)
	h.c.SubmitSecret("pin", pin.PromptID, "654321")
	h.done()
	h.c.mu.Lock()
	var enrolled bool
	for _, s := range h.c.vault.slots {
		enrolled = enrolled || string(s.PublicKey) == string(pubF)
	}
	h.c.mu.Unlock()
	if !enrolled || h.hardwareSlots() != 2 || cardA.verifyCount() != 0 {
		t.Fatalf("enrolled %v, %d YubiKeys, %d VERIFYs to A", enrolled, h.hardwareSlots(), cardA.verifyCount())
	}
}

// A2: a retired hardware record is refused as an active one is. The
// keystore's duplicate rule counts every non-empty hardware record, so the
// enrolment excludes them all (hardwareRecords); a retired record here is
// handed to the core through that seam, the keystore's own test pinning
// that the file's list holds one and AddSlot refuses it. It is no unlock
// method (the Keys view does not list it), and the wait names it all the
// same, with the words of an active one (loose, knowingly: the record).
func TestARetiredRecordsYubiKeyIsRefusedOnInsertion(t *testing.T) {
	cardA, pubA := cardWithKey("123456")
	cardR, pubR := cardWithKey("123456")
	var mu sync.Mutex
	var seen [][]byte
	old := hardwareRecords
	hardwareRecords = func(ks *keystore.Keystore) []keystore.SlotInfo {
		out := ks.HardwareRecords()
		mu.Lock()
		for _, s := range out {
			seen = append(seen, s.PublicKey)
		}
		mu.Unlock()
		return append(out, keystore.SlotInfo{RecipientID: [16]byte{0x52}, Type: format.SlotExternalECDH, Label: "Retired key", PublicKey: pubR})
	}
	defer func() { hardwareRecords = old }()
	h, cards, begun := enrolByRecovery(t, cardR, keystore.HardwareSlot{PublicKey: pubA, Label: "A"})
	held := h.watchCards(cardR)
	h.waitSwap("Retired key", CodeDuplicateSlot)
	mu.Lock()
	ok := len(seen) == 1 && string(seen[0]) == string(pubA)
	mu.Unlock()
	if !ok {
		t.Fatal("the enrolment did not exclude the file's hardware records")
	}
	for _, s := range h.c.Slots() {
		if s.Label == "Retired key" {
			t.Fatal("the retired record is listed as an unlock method")
		}
	}
	h.noPINSince(begun, "a retired record's YubiKey")
	cards.setReaders()
	h.waitSwap("", "")
	cards.setCard(cardA)
	cards.setReaders("Yubico A")
	h.waitSwap("A", CodeDuplicateSlot)
	fresh, _ := cardWithKey("654321")
	cards.setCard(fresh)
	pin := h.rec.waitCeremony(t, StepPIN, true)
	h.c.SubmitSecret("pin", pin.PromptID, "654321")
	h.done()
	h.cardsSettled(begun, held, cardR)
	if cardR.verifyCount() != 0 || h.hardwareSlots() != 2 {
		t.Fatalf("%d VERIFYs to the retired record's YubiKey, %d YubiKeys", cardR.verifyCount(), h.hardwareSlots())
	}
}

// A3: an adopted backup's first way in (FinishSetup here; an import of a
// backup takes the same adoptBackup) excludes the file's hardware records.
// Its active slots are recovery keys alone, but the reader accepts a
// retired YubiKey's record, which AddFirstWayIn refuses; the YubiKey it
// names is waited out on insertion, with no first swap, and a fresh one
// is the vault's first way in.
func TestASetupWaitsOutTheBackupsRetiredYubiKey(t *testing.T) {
	cardR, pubR := cardWithKey("123456")
	h, cards := lostOnlyHarness(t, "")
	cards.setCard(cardR)
	var calls, own atomic.Int32
	old := hardwareRecords
	hardwareRecords = func(ks *keystore.Keystore) []keystore.SlotInfo {
		calls.Add(1)
		own.Add(int32(len(ks.HardwareRecords())))
		return []keystore.SlotInfo{{RecipientID: [16]byte{0x52}, Type: format.SlotExternalECDH, Label: "Retired key", PublicKey: pubR}}
	}
	defer func() { hardwareRecords = old }()
	if !h.status().SetupNeeded {
		t.Fatal("the vault does not need its setup")
	}
	mark := h.mark()
	if e := h.c.FinishSetup(EnrollOptions{Kind: EnrollToken, Label: "First"}); e != nil {
		t.Fatal(e)
	}
	h.answerRecovery(h.recovery)
	h.waitSwap("Retired key", CodeDuplicateSlot)
	if got := fromSwap(h.tuplesSince(mark)); !slices.Equal(got, []tuple{{StepSwapKey, "", "", ""}, {StepSwapKey, "Retired key", "", CodeDuplicateSlot}}) {
		t.Fatalf("the setup's events: %+v", got)
	}
	if calls.Load() != 1 || own.Load() != 0 || cardR.verifyCount() != 0 {
		t.Fatalf("%d calls, %d records of the file's own, %d VERIFYs", calls.Load(), own.Load(), cardR.verifyCount())
	}
	fresh, _ := cardWithKey("654321")
	cards.setCard(fresh)
	pin := h.rec.waitCeremony(t, StepPIN, true)
	h.c.SubmitSecret("pin", pin.PromptID, "654321")
	h.done()
	h.rec.waitState(t, StateUnlocked)
	if n := h.hardwareSlots(); n != 1 {
		t.Fatalf("%d YubiKeys after the setup", n)
	}
}

// A4: the audit's table, every event's full tuple (step, removeLabel,
// slotLabel, error), slotLabel included. P is the proof's label, the
// proving YubiKey's after a YubiKey's proof and empty after a recovery
// key's; N the new YubiKey's.
//
// The table is cut into tests of its own so that none runs long: each
// segment waits three polls after its last event to see that nothing
// follows.
const tupleN = "New key"

// beginByYubiKey: a vault of A ("Office key"), B ("Home key") and a
// recovery key, proved by A, at the first swap; P is its slotLabel.
func beginByYubiKey(t *testing.T) (*harness, *fakeCards, *fakeCard, *fakeCard, string) {
	t.Helper()
	cardA, pubA := cardWithKey("123456")
	cardB, pubB := cardWithKey("123456")
	r, digits := newRecoverySpec(t, "Paper")
	cards := readerWith(cardA)
	h := rekeyHarness(t, cards, "", digits,
		keystore.HardwareSlot{PublicKey: pubA, Label: "Office key"},
		keystore.HardwareSlot{PublicKey: pubB, Label: "Home key"}, r)
	h.unlockWithToken()
	h.rec.reset()
	if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollToken, Label: tupleN}); e != nil {
		t.Fatal(e)
	}
	h.answerTokenUnlock("123456")
	swap := h.rec.waitCeremony(t, StepSwapKey, false)
	if swap.RemoveLabel != "Office key" || swap.Error != "" {
		t.Fatalf("the first swap: %+v", swap)
	}
	return h, cards, cardA, cardB, swap.SlotLabel
}

// The proving YubiKey left in, then put back, another of the vault's in
// its port, out, A again, two readers, A alone, busy opens, Cancel.
func TestTheRefusalsTuplesPutBackAndSwapped(t *testing.T) {
	dup := CodeDuplicateSlot
	{
		h, cards, cardA, cardB, P := beginByYubiKey(t)
		if P != "Office key" {
			t.Fatalf("P is %q", P)
		}
		proofs := cardA.verifyCount()
		mark := h.mark()
		h.exactly(mark, "the proving YubiKey left in")
		cards.setReaders()
		h.waitTuple(tuple{StepWaitingForKey, "", P, ""})
		cards.setReaders("Yubico A")
		h.exactly(mark, "removed, then put back",
			tuple{StepSwapKey, "", P, ""},
			tuple{StepWaitingForKey, "", P, ""},
			tuple{StepSwapKey, "Office key", P, dup})
		mark = h.mark()
		cards.setCard(cardB)
		h.exactly(mark, "B in the same port for the refused A", tuple{StepSwapKey, "Home key", P, dup})
		mark = h.mark()
		cards.setReaders()
		h.exactly(mark, "the refused YubiKey out, the reader empty",
			tuple{StepSwapKey, "", P, ""},
			tuple{StepWaitingForKey, "", P, ""})
		mark = h.mark()
		cards.setCard(cardA)
		cards.setReaders("Yubico A")
		h.exactly(mark, "A in again", tuple{StepSwapKey, "Office key", P, dup})
		mark = h.mark()
		cards.setReaders("Yubico A", "Yubico B")
		h.exactly(mark, "two readers end the refusal",
			tuple{StepSwapKey, "", P, ""},
			tuple{StepTwoKeys, "", P, ""})
		mark = h.mark()
		cards.setReaders("Yubico A")
		h.exactly(mark, "A left alone", tuple{StepSwapKey, "Office key", P, dup})
		mark = h.mark()
		cards.mu.Lock()
		cards.busyOpens = 3
		cards.mu.Unlock()
		h.exactly(mark, "busy opens that end")
		h.c.CancelUnlock()
		h.exactly(mark, "Cancel during the refusal", tuple{StepFailed, "Office key", P, CodeCancelled})
		h.rec.waitCeremonyEnded(t, h.ceremoniesSince(mark)[0])
		if cardA.verifyCount() != proofs || cardB.verifyCount() != 0 {
			t.Fatalf("VERIFYs after the proof: A %d, B %d", cardA.verifyCount()-proofs, cardB.verifyCount())
		}
	}
}

// Another of the vault's YubiKeys for the proving one, directly; and two
// readers ending the first swap, the proving one left alone, then a lock.
func TestTheRefusalsTuplesAfterTheFirstSwap(t *testing.T) {
	dup := CodeDuplicateSlot
	{
		h, cards, _, cardB, P := beginByYubiKey(t)
		mark := h.mark()
		cards.setCard(cardB)
		h.exactly(mark, "B for the proving A",
			tuple{StepSwapKey, "", P, ""},
			tuple{StepSwapKey, "Home key", P, dup})
		h.c.CancelUnlock()
		h.end()
	}
	{
		h, cards, _, _, P := beginByYubiKey(t)
		mark := h.mark()
		cards.setReaders("Yubico A", "Yubico B")
		h.exactly(mark, "two readers",
			tuple{StepSwapKey, "", P, ""},
			tuple{StepTwoKeys, "", P, ""})
		mark = h.mark()
		cards.setReaders("Yubico A")
		h.exactly(mark, "the proving one left alone", tuple{StepSwapKey, "Office key", P, dup})
		h.c.LockNow(ReasonWorkstation)
		if f := h.end(); tupleOf(f) != (tuple{StepFailed, "Office key", P, CodeCancelled}) {
			t.Fatalf("a lock during the refusal: %+v", f)
		}
	}
}

// A recovery key's proof with A in, then a fresh YubiKey for the refused
// one, enrolled; and a fresh YubiKey pulled at its PIN, A in its place.
func TestTheRefusalsTuplesOfAFreshYubiKey(t *testing.T) {
	const N = tupleN
	dup := CodeDuplicateSlot
	{
		cardA, pubA := cardWithKey("123456")
		h, cards, begun := enrolByRecovery(t, cardA, keystore.HardwareSlot{PublicKey: pubA, Label: "A"})
		h.waitSwap("A", dup)
		if got := fromSwap(h.tuplesSince(begun)); !slices.Equal(got, []tuple{{StepSwapKey, "", "", ""}, {StepSwapKey, "A", "", dup}}) {
			t.Fatalf("a recovery key's proof with A in: %+v", got)
		}
		mark := h.mark()
		fresh, _ := cardWithKey("654321")
		cards.setCard(fresh)
		pin := h.rec.waitCeremony(t, StepPIN, true)
		h.c.SubmitSecret("pin", pin.PromptID, "654321")
		h.done()
		h.exactly(mark, "the fresh one, enrolled",
			tuple{StepSwapKey, "", "", ""},
			tuple{StepSwapKey, "", N, ""},
			tuple{StepSwapKey, "", N, ""},
			tuple{StepPIN, "", N, ""},
			tuple{StepTouch, "", N, ""},
			tuple{StepReleasing, "", N, ""},
			tuple{StepDeriving, "", N, ""},
			tuple{StepDeriving, "", N, ""},
			tuple{StepDone, "", N, ""})
	}
	{
		old := keepAliveEvery
		keepAliveEvery = 30 * time.Millisecond
		defer func() { keepAliveEvery = old }()
		h, cards, cardA, _, P := beginByYubiKey(t)
		cards.setReaders()
		h.waitTuple(tuple{StepWaitingForKey, "", P, ""})
		fresh, _ := cardWithKey("654321")
		cards.setCard(fresh)
		cards.setReaders("Yubico A")
		h.rec.waitCeremony(t, StepPIN, true)
		mark := h.mark()
		fresh.setRemoved(true)
		cards.setCard(cardA)
		h.exactly(mark, "pulled at its PIN, A in",
			tuple{StepWaitingForKey, "", N, CodeTokenNoCard},
			tuple{StepSwapKey, "Office key", N, dup})
		h.c.CancelUnlock()
		h.end()
	}
}

// A2: the enrolment's exclusion is the keystore's HardwareRecords, never
// Slots: the YubiKeys alone (a recovery key has no public key to match),
// and every one the duplicate rule counts, which the keystore's own test
// pins for a retired record.
func TestTheExclusionIsTheKeystoresHardwareRecords(t *testing.T) {
	_, pubA := cardWithKey("123456")
	r, _ := newRecoverySpec(t, "Paper")
	path := filepath.Join(t.TempDir(), "vault.eks")
	writeVault(t, path, "", keystore.HardwareSlot{PublicKey: pubA, Label: "A"}, r)
	ks, err := keystore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ks.Close()
	got := hardwareRecords(ks)
	if len(got) != 1 || got[0].Type != format.SlotExternalECDH || string(got[0].PublicKey) != string(pubA) || len(ks.Slots()) != 2 {
		t.Fatalf("the exclusion: %+v", got)
	}
}

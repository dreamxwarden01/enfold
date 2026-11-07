package app

import (
	"testing"
	"time"

	"github.com/dreamxwarden01/enfold/internal/keystore"
)

// The review of the switch audit's repairs (2026-11-07): the memory of the
// keys generated (made) still serves the act begun again in the same
// unlock, for an enrolment as for the switch, and no longer serves one
// after a lock. The fake cards stand in for the YubiKeys; nothing here
// opens a real one.

// enrolOnRecoveryVault begins Add a YubiKey on h's recovery-key vault,
// proves it with digits, answers the management key's PIN and returns the
// act's end, the ceremony gone.
func enrolOnRecoveryVault(t *testing.T, h *harness, digits string) CeremonyState {
	t.Helper()
	h.rec.reset()
	if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollToken, Label: "Desk YubiKey"}); e != nil {
		t.Fatalf("begin: %v", e)
	}
	h.answerRecovery(digits)
	h.answerPIN("123456")
	st := h.rec.waitFor(t, EventVaultCeremony, func(x any) bool {
		s, ok := x.(CeremonyState)
		return ok && s.Kind == "enroll" && (s.Step == StepDone || s.Step == StepFailed)
	}).(CeremonyState)
	if st.Step == StepFailed {
		h.c.CancelUnlock() // a failure parks until the page closes it
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		h.c.mu.Lock()
		gone := h.c.cer == nil
		h.c.mu.Unlock()
		if gone {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("the act did not end after %+v", st)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Within one unlock an enrolment begun again reuses the key the failed one
// generated (one key on the card besides 9d's); after a lock and a new
// unlock it does not (a key of its own).
func TestEnrolmentReusesItsKeyUntilTheLock(t *testing.T) {
	card := generatingCard()
	card.proofLies = true
	cards := readerWith(card)
	r1, first := newRecoverySpec(t, "paper")
	r2, _ := newRecoverySpec(t, "paper 2")
	h := rekeyHarness(t, cards, "", first, []keystore.SlotSpec{r1, r2}...)
	h.unlockWithRecovery(first)

	if st := enrolOnRecoveryVault(t, h, first); st.Step != StepFailed || st.Error != CodeTokenProof || !st.Generated {
		t.Fatalf("the first enrolment: %+v", st)
	}
	if n := card.keyCount(); n != 2 {
		t.Fatalf("%d keys on the card after the first enrolment", n)
	}
	if st := enrolOnRecoveryVault(t, h, first); st.Step != StepFailed || st.Error != CodeTokenProof || !st.Generated {
		t.Fatalf("the enrolment begun again: %+v", st)
	}
	if n := card.keyCount(); n != 2 {
		t.Fatalf("%d keys on the card: the key of this unlock was not reused", n)
	}

	h.lockAndSettle()
	h.unlockWithRecovery(first)
	card.mu.Lock()
	card.proofLies = false
	card.mu.Unlock()
	if st := enrolOnRecoveryVault(t, h, first); st.Step != StepDone || !st.Generated {
		t.Fatalf("the enrolment after the lock: %+v", st)
	}
	if n := card.keyCount(); n != 3 {
		t.Fatalf("%d keys on the card: the previous unlock's key was reused", n)
	}
	if n, _ := h.madeNow(); n != 0 {
		t.Fatalf("made lists %d serials after the key was enrolled", n)
	}
}

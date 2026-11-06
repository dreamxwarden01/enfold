package app

import (
	"sync/atomic"
	"testing"
	"time"
)

// The tests of the page's recovery-key switch against the real core
// (ruled 2026-11-06, R8 and R9; APP.md §13): Use recovery key instead, or
// Use recovery key at a blocked PIN, cancels the enrolment's proof and
// begins the enrolment again with a recovery key as its proof. The page
// (KeysPage.svelte switchToRecovery) calls CancelUnlock, waits until the
// store has seen the cancelled ceremony end (its failed event with
// ceremony.cancelled), and calls BeginEnroll at once. These tests call
// BeginEnroll inside that very event, on the goroutine that emits it — the
// earliest the page could ever see it — at each place the switch is now
// offered while the ceremony may hold a card: the PIN's prompt (opened,
// once with a VERIFY sent), the vault's password asked alone after an
// accepted PIN and a wrong password (the card verified, a touch made, the
// agreement still the attempt's), and Blocked (parked). Each must be
// begun, not refused as ceremony.in_progress; the card must be closed once
// for every open; a failed reset must be warned of only by the release the
// core's rule says may follow a VERIFY (releaseCard, never after the
// switch); no pending touch may be left; and in the second half the
// YubiKey of the vault still in the reader is waited out by R1, asked
// nothing, before a fresh one is enrolled.

// switched is what the switch did inside the cancelled ceremony's event.
type switched struct {
	fired atomic.Bool
	err   *Error
	at    int // the recorder's length at the cancelled event
	done  chan struct{}
}

// switchOnCancel installs the page's switch: the first enrol ceremony that
// ends cancelled begins the enrolment again with a recovery key, from
// inside its own failed event.
func (h *harness) switchOnCancel() *switched {
	s := &switched{done: make(chan struct{})}
	h.rec.onEvent(func(name string, p any) {
		st, ok := p.(CeremonyState)
		if !ok || name != EventVaultCeremony || st.Kind != "enroll" || st.Step != StepFailed || st.Error != CodeCancelled {
			return
		}
		if !s.fired.CompareAndSwap(false, true) {
			return
		}
		s.at = len(h.rec.snapshot())
		s.err = h.c.BeginEnroll(EnrollOptions{Kind: EnrollToken, Label: "New key", WithRecoveryKey: true})
		close(s.done)
	})
	return s
}

// resetWarnings counts the failed resets warned of in events [from, to).
func (h *harness) resetWarnings(from, to int) int {
	n := 0
	all := h.rec.snapshot()
	if to < 0 || to > len(all) {
		to = len(all)
	}
	for _, e := range all[from:to] {
		if w, ok := e.payload.(Warning); ok && w.Code == CodeTokenReset {
			n++
		}
	}
	return n
}

// cardBalanced fails the test unless every open of card was closed exactly
// once and none was opened while an open of it stood.
func cardBalanced(t *testing.T, what string, card *fakeCard) {
	t.Helper()
	card.mu.Lock()
	opened, closes, reopened, over := card.opened, card.closes, card.reopened, card.overClose
	card.mu.Unlock()
	if opened != closes || reopened != 0 || over != 0 {
		t.Fatalf("%s: %d opens, %d closes, %d opened while open, %d closed twice", what, opened, closes, reopened, over)
	}
}

// switchCase is one place the switch is pressed: how the vault is made,
// how the proof is brought there, and how many failed resets the cancelled
// proof's release warns of (with every close of the vault's YubiKey
// reporting a failed reset).
type switchCase struct {
	name      string
	entangled string
	// before runs on the vault's YubiKey before the act begins.
	before func(card *fakeCard)
	// reach answers the proof up to the place the switch is pressed and
	// returns the state there.
	reach func(h *harness) CeremonyState
	// want is the step the switch is pressed at.
	want CeremonyStep
	// warned: failed resets warned of before the cancelled event.
	warned int
	// pending: the cancel leaves the agreement pending, which the recovery
	// key's proof waits to end (settlePending) rather than refusing.
	pending bool
}

func TestTheSwitchBeginsAgainTheMomentTheProofEnds(t *testing.T) {
	setRetries := func(n int) func(*fakeCard) {
		return func(card *fakeCard) {
			card.mu.Lock()
			card.retries = n
			card.mu.Unlock()
		}
	}
	pinPrompt := func(h *harness) CeremonyState { return h.rec.waitCeremony(h.t, StepPIN, true) }
	cases := []switchCase{
		{
			// Opened, its retries read, nothing verified: the cancel ends
			// the prompt, and the exit from the PIN step on is the release
			// step (verifiedExit), which warns of a failed reset.
			name: "the PIN's prompt", want: StepPIN, warned: 1, reach: pinPrompt,
		},
		{
			// A VERIFY sent and refused: the PIN asked again in place.
			name: "the PIN's prompt after a wrong PIN", want: StepPIN, warned: 1,
			reach: func(h *harness) CeremonyState {
				p := pinPrompt(h)
				if e := h.c.SubmitSecret("pin", p.PromptID, "000000"); e != nil {
					h.t.Fatal(e)
				}
				return h.rec.waitFor(h.t, EventVaultCeremony, func(x any) bool {
					s, ok := x.(CeremonyState)
					return ok && s.Step == StepPIN && s.PromptID != "" && s.Error == CodeTokenPIN
				}).(CeremonyState)
			},
		},
		{
			// R9: the PIN accepted, a wrong vault's password known only after
			// the touch; the card goes back before the password is asked
			// alone (attempt.releaseCardForRetry, the one warning), and the
			// agreement is the attempt's, which the cancel leaves pending
			// until its prompt ends.
			name: "the password alone after a wrong one", entangled: "the vault's password", want: StepPassword, warned: 1, pending: true,
			reach: func(h *harness) CeremonyState {
				p := pinPrompt(h)
				if e := h.c.SubmitSecret("pin", p.PromptID, "123456"); e != nil {
					h.t.Fatal(e)
				}
				pw := h.rec.waitCeremony(h.t, StepPassword, true)
				if e := h.c.SubmitSecret("password", pw.PromptID, "not the vault's password"); e != nil {
					h.t.Fatal(e)
				}
				return h.rec.waitFor(h.t, EventVaultCeremony, func(x any) bool {
					s, ok := x.(CeremonyState)
					return ok && s.Step == StepPassword && s.PromptID != "" && s.Error == CodeAuth
				}).(CeremonyState)
			},
		},
		{
			// R8: the last try typed wrong blocks the PIN; the card is
			// released through the release step before the park.
			name: "Blocked by the last try", want: StepBlocked, warned: 1, before: setRetries(1),
			reach: func(h *harness) CeremonyState {
				p := pinPrompt(h)
				if e := h.c.SubmitSecret("pin", p.PromptID, "000000"); e != nil {
					h.t.Fatal(e)
				}
				return h.rec.waitCeremony(h.t, StepBlocked, false)
			},
		},
		{
			// R8: a YubiKey blocked before it was inserted. Nothing is sent
			// to it, but the exit is from the PIN step, so the release step
			// runs and its failed reset is warned of, as the core's rule
			// says (verifyPIN, verifiedExit).
			name: "Blocked on insertion", want: StepBlocked, warned: 1, before: setRetries(0),
			reach: func(h *harness) CeremonyState { return h.rec.waitCeremony(h.t, StepBlocked, false) },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cardA, pubA := cardWithKey("123456")
			cards := readerWith(cardA)
			var h *harness
			if tc.entangled != "" {
				h = newHarnessEntangled(t, cards, pubA, tc.entangled)
			} else {
				h = newHarness(t, cards, pubA)
			}
			h.unlockWithToken()
			failResets(cardA)
			if tc.before != nil {
				tc.before(cardA)
			}
			heldAtClose := h.watchCards(cardA)
			h.rec.reset()
			mark := h.mark()
			s := h.switchOnCancel()
			if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollToken, Label: "New key"}); e != nil {
				t.Fatal(e)
			}
			at := tc.reach(h)
			if at.Step != tc.want {
				t.Fatalf("the switch is pressed at %s, want %s", at.Step, tc.want)
			}
			touches := cardA.touchCount()
			if e := h.c.CancelUnlock(); e != nil {
				t.Fatal(e)
			}
			select {
			case <-s.done:
			case <-time.After(5 * time.Second):
				t.Fatal("the cancelled ceremony sent no failed event")
			}
			h.rec.onEvent(nil)
			if s.err != nil {
				t.Fatalf("BeginEnroll inside the cancelled event was refused: %v", s.err)
			}
			if n := h.resetWarnings(mark, s.at); n != tc.warned {
				t.Fatalf("%d failed resets warned of by the cancelled proof, want %d", n, tc.warned)
			}
			if h.logged("the touch is pending") != tc.pending {
				t.Fatalf("the cancel left a pending touch: %v, want %v", !tc.pending, tc.pending)
			}
			verifies := cardA.verifyCount()

			// The recovery key's proof, then the second half: the vault's
			// YubiKey still in is waited out by R1, asked nothing.
			h.answerRecovery(h.recovery)
			h.waitSwap("Test key", CodeDuplicateSlot)
			cards.setReaders()
			h.waitSwap("", "")
			fresh, _ := cardWithKey("654321")
			cards.setCard(fresh)
			cards.setReaders("Yubico B")
			pin := h.rec.waitCeremony(t, StepPIN, true)
			if pin.SlotLabel != "New key" {
				t.Fatalf("the new YubiKey's PIN: %+v", pin)
			}
			if e := h.c.SubmitSecret("pin", pin.PromptID, "654321"); e != nil {
				t.Fatal(e)
			}
			h.done()

			if n := h.hardwareSlots(); n != 2 {
				t.Fatalf("%d YubiKeys after the enrolment", n)
			}
			if cardA.verifyCount() != verifies || cardA.touchCount() != touches {
				t.Fatalf("the vault's YubiKey was asked after the switch: %d VERIFYs (was %d), %d touches (was %d)", cardA.verifyCount(), verifies, cardA.touchCount(), touches)
			}
			if n := h.resetWarnings(s.at, -1); n != 0 {
				t.Fatalf("%d failed resets warned of after the switch", n)
			}
			cardBalanced(t, "the vault's YubiKey", cardA)
			cardBalanced(t, "the new YubiKey", fresh)
			if n := heldAtClose.Load(); n != 0 {
				t.Fatalf("%d closes of a card still held", n)
			}
			if h.logged("the key was still held at the end") {
				t.Fatal("a card was left held at the end")
			}
			h.c.mu.Lock()
			pending := h.c.pending
			h.c.mu.Unlock()
			if pending != nil {
				t.Fatal("a pending touch was left behind")
			}
		})
	}
}

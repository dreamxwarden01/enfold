package app

import (
	"path/filepath"
	"testing"
	"time"
)

// The tests of the card's one release around a wrong entangled password
// (the 2026-11-06 page review's finding 1; APP.md §2.2, Wrong password).
// A wrong vault's password is known only after the touch, so the attempt
// gives the card back before it asks the password alone
// (attempt.releaseCardForRetry): one release, one warning when its reset
// fails. From then on the card is nobody's: whatever ends the ceremony at
// that prompt — a cancel, a lock trigger, the kept touch's deadline — or
// after it — a second and a third wrong password, the right one with no
// YubiKey in the reader — closes nothing more and shows no release step,
// for each of the three callers of the agreement: the lock screen's
// unlock, the Vault page's proof and the import's proof. A cancel at that
// prompt is the pending touch's end, whichever goroutine sees it first.

// passwordRig is one caller of the agreement on a vault whose password
// is on, with the YubiKey that opens it.
type passwordRig struct {
	h     *harness
	card  *fakeCard
	cards *fakeCards
	// begin starts the act; proved waits until the act has gone past its
	// proof with the right password, and ends it.
	begin  func()
	proved func()
}

const rigPassword = "the vault's password"

// rigUnlock is the lock screen's unlock (ceremony.go unlock).
func rigUnlock(t *testing.T) *passwordRig {
	h, card, cards := entangledHarness(t, rigPassword)
	h.rec.reset()
	r := &passwordRig{h: h, card: card, cards: cards}
	r.begin = func() {
		if e := h.c.BeginUnlock(MethodToken); e != nil {
			t.Fatal(e)
		}
	}
	r.proved = func() {
		h.rec.waitState(t, StateUnlocked)
		h.waitStatus(func(s VaultStatus) bool { return s.Ceremony == nil })
	}
	return r
}

// rigProof is the Vault page's proof (keys.go), here an enrolment's: the
// proof is past when the second half asks for the swap, which names the
// YubiKey that proved as the one to take out, with no note, even though
// the attempt gave its card back for the retry (releaseUnlocking).
func rigProof(t *testing.T) *passwordRig {
	h, card, cards := entangledHarness(t, rigPassword)
	h.unlockWithToken()
	h.rec.reset()
	r := &passwordRig{h: h, card: card, cards: cards}
	r.begin = func() {
		if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollToken, Label: "New key"}); e != nil {
			t.Fatal(e)
		}
	}
	r.proved = func() {
		h.waitSwap("Test key", "")
		if e := h.c.CancelUnlock(); e != nil {
			t.Fatal(e)
		}
		h.end()
	}
	return r
}

// rigImport is the import's proof (vaultfile.go prove) of an entangled
// vault, into a machine that keeps none.
func rigImport(t *testing.T) *passwordRig {
	card := newFakeCard("123456")
	pub := card.addKey(0x9d, true)
	cards := &fakeCards{card: card}
	cards.setReaders("Yubico A")
	dir := t.TempDir()
	removeTempDirs(t, dir)
	src := filepath.Join(dir, "theirs.eks")
	makeEntangledVault(t, src, pub, rigPassword)
	h := &harness{t: t, dir: dir, entangled: rigPassword, clk: newFakeClock(), rec: &recorder{}, cards: cards}
	c, err := New(Deps{Cards: cards, Events: h.rec, Clock: h.clk, DataDir: filepath.Join(dir, "data"), Log: h.log})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Start(); err != nil {
		t.Fatal(err)
	}
	h.c = c
	h.rec.hear(c)
	t.Cleanup(c.Close)
	r := &passwordRig{h: h, card: card, cards: cards}
	r.begin = func() {
		if e := c.ImportFile(src, "Theirs", MethodToken, EnrollOptions{}, false); e != nil {
			t.Fatal(e)
		}
	}
	r.proved = func() {
		h.rec.waitCeremony(t, StepDone, false)
		h.waitStatus(func(s VaultStatus) bool { return s.Ceremony == nil && s.State == StateLocked })
	}
	return r
}

var rigs = []struct {
	name string
	make func(*testing.T) *passwordRig
}{
	{"the lock screen's unlock", rigUnlock},
	{"the Vault page's proof", rigProof},
	{"the import's proof", rigImport},
}

// toPasswordAlone answers the PIN and a wrong vault's password, and
// returns the password asked alone: the card is given back by then.
func (r *passwordRig) toPasswordAlone() CeremonyState {
	h := r.h
	h.t.Helper()
	r.begin()
	pin := h.rec.waitCeremony(h.t, StepPIN, true)
	if e := h.c.SubmitSecret("pin", pin.PromptID, "123456"); e != nil {
		h.t.Fatal(e)
	}
	pw := h.rec.waitCeremony(h.t, StepPassword, true)
	if e := h.c.SubmitSecret("password", pw.PromptID, "not the vault's password"); e != nil {
		h.t.Fatal(e)
	}
	return r.askedAgain(pw.PromptID)
}

// askedAgain waits for the password asked alone under a prompt other
// than after.
func (r *passwordRig) askedAgain(after string) CeremonyState {
	h := r.h
	h.t.Helper()
	return h.rec.waitFor(h.t, EventVaultCeremony, func(x any) bool {
		s, ok := x.(CeremonyState)
		return ok && s.Step == StepPassword && s.PromptID != "" && s.PromptID != after && s.Error == CodeAuth
	}).(CeremonyState)
}

// releasedOnce fails the test unless the card was opened and closed in
// balance and never closed twice, closed nothing more from the password
// asked alone on (closes), showed no release step from there (mark), and
// warned of warned failed resets in all.
func (r *passwordRig) releasedOnce(mark, closes, warned int) {
	h := r.h
	h.t.Helper()
	cardBalanced(h.t, "the vault's YubiKey", r.card)
	if n := r.card.closeCount(); n != closes {
		h.t.Fatalf("the card was closed %d more times after it was given back", n-closes)
	}
	for _, s := range h.ceremoniesSince(mark) {
		if s.Step == StepReleasing {
			h.t.Fatalf("a release step for a card already given back: %+v", s)
		}
	}
	if n := h.resetWarnings(0, -1); n != warned {
		h.t.Fatalf("%d failed resets warned of, want %d", n, warned)
	}
	if h.logged("the key was still held at the end") {
		h.t.Fatal("a card was left held at the end")
	}
}

// handoverOrders are the two orders in which the ceremony and its attempt
// can see a cancel at the password asked alone, each forced through the
// handover's hook, and the order the scheduler picks. "await first" holds
// the attempt's end until await has seen the cancel and disowned it;
// "the attempt first" holds await until the attempt has ended, so that it
// is run's own disowning (T2 of the 2026-11-06 page review) that makes
// the end the pending touch's: without it this order fails every run.
var handoverOrders = []string{"as scheduled", "await first", "the attempt first"}

// forceOrder installs the hook for order, before the act begins, so that
// the hook is never written while a goroutine of the core may read it.
func forceOrder(t *testing.T, h *harness, order string) {
	switch order {
	case "await first":
		setHandoverHook(func(at string, _ *ceremony, a *attempt) {
			if at != "answered" {
				return
			}
			deadline := time.Now().Add(10 * time.Second)
			for {
				h.c.mu.Lock()
				o := a.owner
				h.c.mu.Unlock()
				if o == nil {
					return
				}
				if time.Now().After(deadline) {
					t.Error("await never saw the cancel")
					return
				}
				time.Sleep(time.Millisecond)
			}
		})
	case "the attempt first":
		setHandoverHook(func(at string, _ *ceremony, a *attempt) {
			if at == "await" {
				<-a.done
			}
		})
	default:
		return
	}
	t.Cleanup(func() { setHandoverHook(nil) })
}

// setHandoverHook installs f as the handover's hook, or takes the hook
// away with nil. The store is atomic, so a failing test's cleanup may run
// while an attempt of its core still reads the hook.
func setHandoverHook(f func(at string, cer *ceremony, a *attempt)) {
	if f == nil {
		handoverHook.Store(nil)
		return
	}
	handoverHook.Store(&f)
}

// Whatever ends the ceremony at the password asked alone ends the pending
// touch the cancel leaves, and nothing more is closed: the card went back
// before the prompt, once, with its one warning when its reset failed.
// Each order of the handover is forced, and the scheduler's too.
func TestThePasswordAloneEndsWithTheCardReleasedOnce(t *testing.T) {
	ends := []struct {
		name string
		end  func(r *passwordRig)
	}{
		{"cancelled", func(r *passwordRig) {
			if e := r.h.c.CancelUnlock(); e != nil {
				r.h.t.Fatal(e)
			}
		}},
		{"locked", func(r *passwordRig) { r.h.c.LockNow(ReasonManual) }},
		{"the kept touch's deadline", func(r *passwordRig) { r.h.clk.Advance(tokenCacheLife + time.Second) }},
	}
	for _, rig := range rigs {
		for _, end := range ends {
			for _, fails := range []bool{true, false} {
				for _, order := range handoverOrders {
					name := rig.name + "/" + end.name + "/" + order + "/reset succeeds"
					warned := 0
					if fails {
						name = rig.name + "/" + end.name + "/" + order + "/reset fails"
						warned = 1
					}
					t.Run(name, func(t *testing.T) {
						r := rig.make(t)
						forceOrder(t, r.h, order)
						if fails {
							failResets(r.card)
						}
						r.toPasswordAlone()
						mark, closes := r.h.mark(), r.card.closeCount()
						end.end(r)
						r.h.waitStatus(func(s VaultStatus) bool { return s.Ceremony == nil && !s.PendingTouch })
						if !r.h.logged("the touch is pending") {
							t.Fatal("the end at the password asked alone was not the pending touch's")
						}
						r.releasedOnce(mark, closes, warned)
					})
				}
			}
		}
	}
}

// The retry needs nothing from the card (APP.md §2.2): with the YubiKey
// pulled after it was given back, a second and a third wrong password are
// asked again and the right one unlocks, or proves; with it pulled before
// it was given back, the release reports nothing and the right password
// still serves. Either way the card is closed once and no release step
// follows the password asked alone.
func TestTheRightPasswordAfterAWrongOneNeedsNoCard(t *testing.T) {
	for _, rig := range rigs {
		t.Run(rig.name+"/pulled after the release", func(t *testing.T) {
			r := rig.make(t)
			failResets(r.card)
			again := r.toPasswordAlone()
			mark, closes := r.h.mark(), r.card.closeCount()
			r.card.setRemoved(true)
			r.cards.setReaders()
			for _, wrong := range []string{"still not it", "nor this"} {
				if e := r.h.c.SubmitSecret("password", again.PromptID, wrong); e != nil {
					t.Fatal(e)
				}
				again = r.askedAgain(again.PromptID)
			}
			if e := r.h.c.SubmitSecret("password", again.PromptID, rigPassword); e != nil {
				t.Fatal(e)
			}
			r.proved()
			r.releasedOnce(mark, closes, 1)
		})
		t.Run(rig.name+"/pulled before the release", func(t *testing.T) {
			r := rig.make(t)
			r.card.mu.Lock()
			r.card.holdTouch = true
			r.card.mu.Unlock()
			r.begin()
			pin := r.h.rec.waitCeremony(t, StepPIN, true)
			if e := r.h.c.SubmitSecret("pin", pin.PromptID, "123456"); e != nil {
				t.Fatal(e)
			}
			pw := r.h.rec.waitCeremony(t, StepPassword, true)
			if e := r.h.c.SubmitSecret("password", pw.PromptID, "not the vault's password"); e != nil {
				t.Fatal(e)
			}
			r.h.rec.waitCeremony(t, StepTouch, false)
			// The touch is given and the key pulled before the derivation
			// refuses the password: the release finds the key gone.
			r.card.setRemoved(true)
			r.cards.setReaders()
			r.card.press()
			again := r.askedAgain(pw.PromptID)
			mark, closes := r.h.mark(), r.card.closeCount()
			if e := r.h.c.SubmitSecret("password", again.PromptID, rigPassword); e != nil {
				t.Fatal(e)
			}
			r.proved()
			r.releasedOnce(mark, closes, 0)
		})
	}
}

// A pending touch adopted by the next unlock, whose wrong password is
// refused only after the adopter took it over: the attempt gives the card
// back under its new owner, and the adopter, which took the card from the
// adoption, closes nothing more when the right password unlocks.
func TestAnAdoptedTouchGivesTheCardBackOnce(t *testing.T) {
	r := rigUnlock(t)
	h, card := r.h, r.card
	failResets(card)
	card.mu.Lock()
	card.holdTouch = true
	card.mu.Unlock()
	r.begin()
	pin := h.rec.waitCeremony(t, StepPIN, true)
	if e := h.c.SubmitSecret("pin", pin.PromptID, "123456"); e != nil {
		t.Fatal(e)
	}
	pw := h.rec.waitCeremony(t, StepPassword, true)
	if e := h.c.SubmitSecret("password", pw.PromptID, "not the vault's password"); e != nil {
		t.Fatal(e)
	}
	h.rec.waitCeremony(t, StepTouch, false)
	if e := h.c.CancelUnlock(); e != nil {
		t.Fatal(e)
	}
	h.waitStatus(func(s VaultStatus) bool { return s.Ceremony == nil && s.PendingTouch })
	h.rec.reset()
	if e := h.c.BeginUnlock(MethodToken); e != nil {
		t.Fatal(e)
	}
	h.rec.waitCeremony(t, StepTouch, false)
	card.press()
	again := r.askedAgain(pw.PromptID)
	mark, closes := h.mark(), card.closeCount()
	if closes != card.openedCount() {
		t.Fatalf("the card was not given back before the password asked alone: %d opens, %d closes", card.openedCount(), closes)
	}
	if e := h.c.SubmitSecret("password", again.PromptID, rigPassword); e != nil {
		t.Fatal(e)
	}
	r.proved()
	r.releasedOnce(mark, closes, 1)
}

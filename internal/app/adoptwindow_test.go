package app

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The adopter's window (the 2026-11-06 outside audit's second pass, U1;
// APP.md §2.2, "The pending touch outlives its ceremony"). An unlock, or a
// Vault page's proof, that adopts the pending touch holds it from the
// adoption to its wait (await); a cancel, a lock or the kept touch's
// deadline can end the adopter inside that stretch, and the attempt then
// ends as the pending touch again (run disowns it) before the adopter has
// looked. The adoption must not turn into a fresh agreement there: the
// adopter awaits the attempt it adopted, learns that it is the pending
// touch's (errDisowned) and touches neither its handle nor its card. A
// second agreement on the same handle would overlap the attempt's own
// close of it, or use it closed, since the keystore is single-owner.

// windowBarrier holds the adopter where agree begins, before it reads the
// attempt it waits on, and records every attempt the adopter then awaits.
type windowBarrier struct {
	t       *testing.T
	h       *harness
	armed   atomic.Bool
	reached chan *attempt // the attempt the adopter holds at agree
	release chan struct{}

	mu      sync.Mutex
	adopter *ceremony
	awaited []*attempt
}

// barrierAtAgree installs the hook before any ceremony runs, so that the
// hook is never written while a goroutine of the core may read it; it
// does nothing until armed.
func barrierAtAgree(t *testing.T, h *harness) *windowBarrier {
	b := &windowBarrier{t: t, h: h, reached: make(chan *attempt, 1), release: make(chan struct{})}
	setHandoverHook(func(at string, cer *ceremony, a *attempt) {
		switch at {
		case "agree":
			if !b.armed.CompareAndSwap(true, false) {
				return
			}
			h.c.mu.Lock()
			held := cer.att
			h.c.mu.Unlock()
			b.mu.Lock()
			b.adopter = cer
			b.mu.Unlock()
			b.reached <- held
			<-b.release
		case "await":
			b.mu.Lock()
			if cer == b.adopter {
				b.awaited = append(b.awaited, a)
			}
			b.mu.Unlock()
		}
	})
	t.Cleanup(func() { setHandoverHook(nil) })
	return b
}

// pendAtTheTouch begins the act, answers the PIN and a wrong vault's
// password, and cancels it while the touch is held: the attempt, which
// still holds the card and (for an unlock) the handle, is the pending
// touch. It returns that attempt.
func (r *passwordRig) pendAtTheTouch() *attempt {
	h := r.h
	h.t.Helper()
	r.card.mu.Lock()
	r.card.holdTouch = true
	r.card.mu.Unlock()
	h.rec.reset()
	r.begin()
	pin := h.rec.waitCeremony(h.t, StepPIN, true)
	if e := h.c.SubmitSecret("pin", pin.PromptID, "123456"); e != nil {
		h.t.Fatal(e)
	}
	pw := h.rec.waitCeremony(h.t, StepPassword, true)
	if e := h.c.SubmitSecret("password", pw.PromptID, "not the vault's password"); e != nil {
		h.t.Fatal(e)
	}
	h.rec.waitCeremony(h.t, StepTouch, false)
	if e := h.c.CancelUnlock(); e != nil {
		h.t.Fatal(e)
	}
	h.waitStatus(func(s VaultStatus) bool { return s.Ceremony == nil && s.PendingTouch })
	h.c.mu.Lock()
	defer h.c.mu.Unlock()
	if h.c.pending == nil {
		h.t.Fatal("no pending touch after the cancel at the touch")
	}
	return h.c.pending
}

// over waits for the attempt to have ended, released and left the core.
func over(t *testing.T, a *attempt) {
	t.Helper()
	select {
	case <-a.done:
	case <-time.After(10 * time.Second):
		t.Fatal("the adopted attempt never ended")
	}
}

// Whatever ends the adopter between its adoption and its wait, the attempt
// it adopted is the only agreement there is: the adopter awaits that one,
// starts none, closes nothing of it, and the act after it works.
func TestAnAdopterEndedBeforeItsWaitStartsNoAgreement(t *testing.T) {
	adopters := []struct {
		name string
		make func(*testing.T) *passwordRig
	}{
		{"the lock screen's unlock", rigUnlock},
		{"the Vault page's proof", rigProof},
	}
	ends := []struct {
		name string
		// end ends the adopter held at agree and lets the touch answer
		// with the wrong password; it returns what to wait for before the
		// adopter is let go, nil for nothing more.
		end func(r *passwordRig) <-chan struct{}
	}{
		{"cancelled", func(r *passwordRig) <-chan struct{} {
			if e := r.h.c.CancelUnlock(); e != nil {
				r.h.t.Fatal(e)
			}
			r.card.press()
			return nil
		}},
		{"locked", func(r *passwordRig) <-chan struct{} {
			locked := make(chan struct{})
			go func() {
				defer close(locked)
				r.h.c.LockNow(ReasonManual)
			}()
			r.card.press()
			return locked
		}},
		{"the kept touch's deadline", func(r *passwordRig) <-chan struct{} {
			// The touch answers while the adopter still owns the attempt:
			// the wrong password is asked again on its panel, and the kept
			// touch's five minutes run out there.
			r.card.press()
			r.askedAgain("")
			r.h.clk.Advance(tokenCacheLife + time.Second)
			return nil
		}},
	}
	for _, ad := range adopters {
		for _, end := range ends {
			t.Run(ad.name+"/"+end.name, func(t *testing.T) {
				r := ad.make(t)
				h := r.h
				b := barrierAtAgree(t, h)
				pend := r.pendAtTheTouch()
				ecdhs := r.card.ecdhCount()

				// The adopter: the same act again, which takes the pending
				// touch over and is held where agree begins.
				b.armed.Store(true)
				h.rec.reset()
				r.begin()
				h.rec.waitCeremony(t, StepTouch, false)
				var held *attempt
				select {
				case held = <-b.reached:
				case <-time.After(10 * time.Second):
					t.Fatal("the adopter never reached agree")
				}
				if held != pend {
					t.Fatalf("the adopter holds %p at agree, not the pending touch %p", held, pend)
				}

				wait := end.end(r)
				over(t, pend)
				h.c.mu.Lock()
				owner, pending := pend.owner, h.c.pending
				h.c.mu.Unlock()
				if owner != nil || pending != nil {
					t.Fatalf("after its end the attempt has owner %p, the core pending %p", owner, pending)
				}

				close(b.release)
				if wait != nil {
					select {
					case <-wait:
					case <-time.After(10 * time.Second):
						t.Fatal("the lock never returned")
					}
				}
				h.waitStatus(func(s VaultStatus) bool { return s.Ceremony == nil && !s.PendingTouch })

				b.mu.Lock()
				awaited := append([]*attempt(nil), b.awaited...)
				b.mu.Unlock()
				if len(awaited) != 1 || awaited[0] != pend {
					t.Fatalf("the adopter awaited %v, want only the attempt it adopted %p: a second agreement began", awaited, pend)
				}
				if n := r.card.ecdhCount(); n != ecdhs {
					t.Fatalf("%d agreements asked of the card after the adoption", n-ecdhs)
				}
				if h.logged(": failed: ") {
					t.Fatal("the adopter failed rather than ending as the pending touch's")
				}
				// What a second agreement met in the reproduction: the handle
				// the attempt had closed, or the card it had released.
				if h.logged("keystore: closed") || h.logged("token: closed") {
					t.Fatal("an agreement used a handle or a card already closed")
				}
				cardBalanced(t, "the vault's YubiKey", r.card)
				if h.logged("the key was still held at the end") {
					t.Fatal("a card was left held at the end")
				}

				// The act after it: an unlock opens the vault file again,
				// which the handle the attempt held would refuse (busy) had it
				// not been closed; a proof runs on the vault's own handle.
				r.card.mu.Lock()
				r.card.holdTouch = false
				r.card.mu.Unlock()
				h.rec.reset()
				if end.name == "locked" {
					h.unlockWithToken()
					return
				}
				r.begin()
				h.answerTokenUnlock("123456")
				r.proved()
			})
		}
	}
}

// The ordinary adoption is unchanged: an adopter that reaches its wait
// before the touch answers takes the answer — here the right password
// asked again after a wrong one — and the act completes, with the one
// attempt awaited.
func TestAnAdopterAtItsWaitTakesTheAnswer(t *testing.T) {
	for _, ad := range []struct {
		name string
		make func(*testing.T) *passwordRig
	}{{"the lock screen's unlock", rigUnlock}, {"the Vault page's proof", rigProof}} {
		t.Run(ad.name, func(t *testing.T) {
			r := ad.make(t)
			h := r.h
			b := barrierAtAgree(t, h)
			pend := r.pendAtTheTouch()
			b.armed.Store(true)
			h.rec.reset()
			r.begin()
			h.rec.waitCeremony(t, StepTouch, false)
			<-b.reached
			close(b.release)
			// At its wait, then the touch.
			deadline := time.Now().Add(10 * time.Second)
			for {
				b.mu.Lock()
				n := len(b.awaited)
				b.mu.Unlock()
				if n > 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("the adopter never reached its wait")
				}
				time.Sleep(time.Millisecond)
			}
			r.card.press()
			again := r.askedAgain("")
			if e := h.c.SubmitSecret("password", again.PromptID, rigPassword); e != nil {
				t.Fatal(e)
			}
			r.proved()
			b.mu.Lock()
			awaited := append([]*attempt(nil), b.awaited...)
			b.mu.Unlock()
			if len(awaited) != 1 || awaited[0] != pend {
				t.Fatalf("the adopter awaited %v, want only %p", awaited, pend)
			}
			cardBalanced(t, "the vault's YubiKey", r.card)
		})
	}
}

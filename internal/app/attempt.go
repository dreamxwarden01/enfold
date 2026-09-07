package app

import (
	"errors"
	"fmt"
	"time"

	"github.com/dreamxwarden01/enfold/internal/keystore"
)

// attempt is a token's agreement in flight — the card call that waits for
// the touch, which no PC/SC call cuts short (DESIGN.md §11 trap 23) — on
// its own goroutine, so that the ceremony waiting on it can end without
// it (APP.md §2.2, "The pending touch outlives its ceremony"). It owns
// what the call needs: the open card, the token inside its credential,
// the keystore handle when the ceremony opened one, and its purpose. Its
// owner is the ceremony waiting on it; disowned by a cancel, it is the
// core's pending touch until the card answers, adoptable by the next
// unlock of the same vault.
type attempt struct {
	c    *Core
	kind string // the ceremony kind it was started for, for the log
	// adoptable: the agreement is this vault's VMK through one of its
	// slots — an unlock's, or the unlock half of a slot change, export or
	// reveal — which the next such ceremony may take over; a proof's, an
	// import's and a verification's never are (APP.md §2.2).
	adoptable bool
	path      string // the vault file, for the adoption's match
	slot      keystore.SlotInfo
	card      Card
	ks        *keystore.Keystore
	// ownsKS: the ceremony opened the handle for this attempt (an unlock
	// from Locked, an import's or a verification's staged copy): released
	// with the attempt when nobody owns it. vaultHandle: the handle is
	// the vault's own (a slot change, a reveal), which every guard on
	// that handle treats as the ceremony still running.
	ownsKS      bool
	vaultHandle bool
	hc          keystore.HardwareCredential
	prompter    *ceremonyPrompter
	// op is one agreement: an unlock's keystore.Unlock, or a proof's ECDH
	// checked against the key's public point (nil Unlocked then). cleanup
	// runs after the release when nobody owned the attempt at its end.
	op      func() (*keystore.Unlocked, error)
	cleanup func()

	// Guarded by Core.mu.
	owner    *ceremony
	dropped  bool // a lock trigger, a slot change: never adoptable
	finished bool
	rounds   int          // touch rounds the key gave up on
	touch    TouchRequest // the last touch prompt, for an adopter's panel
	touched  bool

	// The answer, readable once done is closed.
	unl  *keystore.Unlocked
	err  error
	done chan struct{}
}

// maxTouchRounds is how many GENERAL AUTHENTICATEs the key may give up on
// before an owned attempt fails with token.touch: the first, and one
// continuation (APP.md §2.2 "Two rounds, then a failure").
const maxTouchRounds = 2

// errDisowned is what await answers a ceremony cancelled before the card
// did: the ceremony holds nothing of the attempt any more — not the card,
// not the handle — and must release nothing.
var errDisowned = fmt.Errorf("%w: the pending touch goes on", ErrTokenCancelled)

// errProof is the proof's own refusal: the agreement is not the key's.
var errProof = errors.New("the key's agreement does not match its public key")

// startAttempt begins the agreement on its own goroutine, owned by cer.
func (cer *ceremony) startAttempt(a *attempt) *attempt {
	a.c = cer.c
	a.kind = cer.kind
	a.done = make(chan struct{})
	cer.c.mu.Lock()
	a.owner = cer
	cer.att = a
	if a.prompter != nil {
		a.prompter.attach(cer, a)
	}
	// What the ceremony would clean up at its end — an import's staged
	// copy — is the attempt's to do instead when the attempt outlives
	// the ceremony; an attempt that ends owned runs no cleanup, and the
	// ceremony's own runs as before.
	if own, prev := cer.cleanup, a.cleanup; own != nil {
		a.cleanup = func() {
			if prev != nil {
				prev()
			}
			own()
		}
	}
	cer.c.mu.Unlock()
	go a.run()
	return a
}

// pendingOwns says whether the pending touch holds ks — an unlock's
// attempt owns the handle it was given; a proof's holds only the card —
// so that a ceremony ending disowned closes exactly what is still its own.
func (cer *ceremony) pendingOwns(ks *keystore.Keystore) bool {
	c := cer.c
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pending != nil && c.pending.ownsKS && c.pending.ks == ks
}

// run is the attempt's goroutine: the agreement until it succeeds or
// fails for good, then the handover — to the owner, which takes the
// answer and what the attempt held, or, with no owner, to nobody: the
// answer is closed unused and everything released.
func (a *attempt) run() {
	unl, err := a.loop()
	c := a.c
	c.mu.Lock()
	a.finished = true
	a.unl, a.err = unl, err
	owned := a.owner != nil
	c.mu.Unlock()
	if owned {
		close(a.done)
		return
	}
	a.discard()
}

// loop retries the agreement the way §2.2 says: a refused PIN is noted
// for the next prompt; the key giving up on a touch starts one more round
// while the attempt has an owner, never a third; an entangled password
// refused is asked again on the owner's panel. Without an owner nothing
// is asked and nothing continued.
func (a *attempt) loop() (*keystore.Unlocked, error) {
	for {
		unl, err := a.op()
		if err == nil {
			return unl, nil
		}
		var pe *TokenPINError
		switch {
		case keyGone(err):
			// Pulled — during a prompt (the prober's finding rides under the
			// cancel the prompt ended with) or during the touch: the
			// owner's to wait for again; nobody's, when there is none.
			return nil, err
		case errors.As(err, &pe):
			if o := a.ownerNow(); o != nil {
				o.notePINWrong()
			}
			continue
		case errors.Is(err, ErrTokenTouch):
			if !a.anotherRound() {
				return nil, err
			}
			continue
		case errors.Is(err, ErrTokenPINRequired):
			continue
		case errors.Is(err, ErrTokenCancelled):
			// The prompter found no live owner. One may have adopted the
			// attempt since: then the prompt is asked of it.
			if a.ownerNow() != nil {
				continue
			}
			return nil, err
		case a.hc.Password != "" && errors.Is(err, keystore.ErrAuth):
			o := a.ownerNow()
			if o == nil {
				return nil, ErrTokenCancelled
			}
			pw, aerr := o.askNote("password", StepPassword, PINStatus{}, CodeAuth)
			if aerr != nil {
				return nil, aerr
			}
			a.c.mu.Lock()
			a.hc.Password = pw
			a.c.mu.Unlock()
			o.set(func(s *CeremonyState) { s.Step = StepDeriving })
			continue
		}
		return nil, err
	}
}

// credential is the token's credential as it stands: the entangled
// password may have been asked again meanwhile.
func (a *attempt) credential() keystore.HardwareCredential {
	a.c.mu.Lock()
	defer a.c.mu.Unlock()
	return a.hc
}

// anotherRound records that the key gave up on a touch and says whether
// the agreement is asked for again: only with a live owner, and only once.
func (a *attempt) anotherRound() bool {
	c := a.c
	c.mu.Lock()
	defer c.mu.Unlock()
	a.rounds++
	o := a.owner
	if o == nil || o.ctx.Err() != nil {
		return false
	}
	if a.rounds >= maxTouchRounds {
		c.log("ceremony %s: the key gave up on the touch %d times: failing", o.kind, a.rounds)
		return false
	}
	c.log("ceremony %s: the key gave up on the touch; asking once more", o.kind)
	return true
}

// ownerNow is the ceremony that owns the attempt and is still running;
// nil for the pending touch.
func (a *attempt) ownerNow() *ceremony {
	c := a.c
	c.mu.Lock()
	defer c.mu.Unlock()
	if a.owner == nil || a.owner.ctx.Err() != nil {
		return nil
	}
	return a.owner
}

// noteTouch keeps the touch prompt for an adopter's panel.
func (a *attempt) noteTouch(req TouchRequest) {
	c := a.c
	c.mu.Lock()
	a.touch, a.touched = req, true
	c.mu.Unlock()
}

// await waits for the answer or the ceremony's cancellation, whichever
// comes first. Answered, the owner takes the Unlocked (or the error) and
// what the attempt held — the card, the handle — as if it had made the
// call itself. Cancelled first, the attempt is disowned and becomes the
// core's pending touch, and errDisowned says the ceremony holds nothing
// of it.
func (a *attempt) await(cer *ceremony) (*keystore.Unlocked, error) {
	c := a.c
	select {
	case <-a.done:
	case <-cer.ctx.Done():
		c.mu.Lock()
		if a.finished {
			// The answer landed with the cancel: it is the owner's, to
			// close as a cancelled ceremony closes everything.
			c.mu.Unlock()
			<-a.done
			break
		}
		a.owner = nil
		if cer.latch {
			a.dropped = true // a lock trigger: never adopted
		}
		if a.prompter != nil {
			a.prompter.attach(nil, a)
		}
		c.pending = a
		cer.att = nil
		c.mu.Unlock()
		c.log("ceremony %s: cancelled with the key still answering: the touch is pending", cer.kind)
		c.emitState()
		return nil, errDisowned
	}
	c.mu.Lock()
	cer.att = nil
	c.mu.Unlock()
	return a.unl, a.err
}

// discard ends a pending touch nobody adopted: the answer, if a touch
// came, is closed unused; the handle the ceremony opened and the card
// (with its reset) are released, in that order; the receipts a slot
// change's handle kept waiting are paid.
func (a *attempt) discard() {
	c := a.c
	if a.unl != nil {
		a.unl.Close()
		a.unl = nil
	}
	if a.err != nil && !errors.Is(a.err, ErrTokenTouch) && !errors.Is(a.err, ErrTokenCancelled) {
		c.log("pending touch (%s): ended with: %v", a.kind, a.err)
	} else {
		c.log("pending touch (%s): over", a.kind)
	}
	a.release()
	c.mu.Lock()
	if c.pending == a {
		c.pending = nil
	}
	if a.vaultHandle && c.vault.state == StateUnlocked {
		c.applyOwedLocked()
	}
	c.mu.Unlock()
	close(a.done)
	c.emitState()
}

// release closes what the attempt holds: the handle it opened, then the
// card. Called by the attempt only when nobody owns it; an owner closes
// them itself.
func (a *attempt) release() {
	if a.ownsKS && a.ks != nil {
		a.ks.Close()
	}
	if a.card != nil {
		a.c.releaseCard(a.card, a.kind)
	}
	if a.cleanup != nil {
		a.cleanup()
	}
}

// adoptPending takes over the pending touch when it is one this ceremony
// would have started itself: this vault's VMK through one of its slots —
// the same agreement, whichever kind was cancelled — not dropped by a
// trigger or a slot change (APP.md §2.2 "The same VMK adopts"). The panel
// opens at the Touch step. Nil when there is nothing to adopt; the caller
// then waits for whatever is pending (settlePending).
func (cer *ceremony) adoptPending(path string, slots []keystore.SlotInfo) *attempt {
	c := cer.c
	c.mu.Lock()
	p := c.pending
	if p == nil || !p.adoptable || !(cer.kind == "unlock" || cer.mutation) || p.dropped || p.finished || p.owner != nil || !samePath(p.path, path) {
		c.mu.Unlock()
		return nil
	}
	found := false
	for _, s := range slots {
		if s.RecipientID == p.slot.RecipientID && s.PublicKey != nil && string(s.PublicKey) == string(p.slot.PublicKey) {
			found = true
			break
		}
	}
	if !found {
		c.mu.Unlock()
		return nil
	}
	p.owner = cer
	if p.prompter != nil {
		p.prompter.attach(cer, p)
	}
	c.pending = nil
	cer.att = p
	cer.held = p.card
	cer.unlockPub, cer.unlockLabel = p.slot.PublicKey, p.slot.Label
	cer.state.Step, cer.state.ReaderCount, cer.state.SlotLabel, cer.state.Error, cer.state.PromptID = StepTouch, 1, p.slot.Label, "", ""
	cer.state.N, cer.state.PINAsked = p.touch.N, p.touch.PINAsked
	cer.state.Seq = c.bump()
	st := cer.state
	c.mu.Unlock()
	c.log("ceremony %s: adopted the pending touch", cer.kind)
	c.emit(EventVaultCeremony, st)
	c.emitState()
	return p
}

// settlePending waits for the pending touch to end, with token.pending
// as the note on the step the ceremony waits in, so that a ceremony that
// cannot adopt it — or a typed credential that needs the file it holds —
// goes on as if it had never been there.
func (cer *ceremony) settlePending() error {
	c := cer.c
	noted := false
	for {
		c.mu.Lock()
		p := c.pending
		c.mu.Unlock()
		if p == nil {
			if noted {
				cer.note("")
			}
			return nil
		}
		if !noted {
			c.log("ceremony %s: waiting for the pending touch to end", cer.kind)
			cer.note(CodeTokenPending)
			noted = true
		}
		select {
		case <-p.done:
		case <-cer.ctx.Done():
			return ErrTokenCancelled
		}
	}
}

// pendingDone is the pending touch's end, for the lock and the exit to
// wait on; nil when none is pending.
func (c *Core) pendingDone() <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending == nil {
		return nil
	}
	return c.pending.done
}

// awaitPending waits for the pending touch to end, at most d: the exit's
// wait (APP.md §5), so that the card is not left PIN-verified for the
// next program.
func (c *Core) awaitPending(d time.Duration) {
	done := c.pendingDone()
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-time.After(d):
		c.log("shutdown: the pending touch did not end within %v", d)
	}
}

// dropPendingLocked leaves the pending touch unadoptable: after a lock
// trigger, after a slot change. Caller holds the state mutex.
func (c *Core) dropPendingLocked() {
	if c.pending != nil {
		c.pending.dropped = true
	}
}

// releaseCard closes a card and turns a failed reset into the warning
// the state carries (token.reset_failed): a warning, never a failure.
func (c *Core) releaseCard(card Card, kind string) {
	err := card.Close()
	if err != nil {
		c.log("ceremony %s: releasing the key: %v", kind, err)
	}
	if err != nil && errors.Is(err, ErrTokenResetFailed) {
		c.mu.Lock()
		c.vault.warnings[CodeTokenReset] = true
		c.mu.Unlock()
		c.emit(EventVaultWarning, Warning{Code: CodeTokenReset})
	}
}

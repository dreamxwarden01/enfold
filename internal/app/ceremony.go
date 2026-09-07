package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sync"
	"time"

	"github.com/dreamxwarden01/enfold/internal/format"
	"github.com/dreamxwarden01/enfold/internal/kdf"
	"github.com/dreamxwarden01/enfold/internal/keystore"
)

// ceremony is one run of the token or secret flow of APP.md §2.2: a
// goroutine, a cancellation, a latch the lock trigger sets, and one prompt
// mailbox at a time. Its state field is what the panel renders.
type ceremony struct {
	kind   string
	ctx    context.Context
	cancel context.CancelFunc
	c      *Core

	// Guarded by Core.mu.
	state        CeremonyState
	prompt       *prompt
	latch        bool
	cancelReason string
	// mutation: a slot change that commits to the open handle from this
	// goroutine; registry writes wait while it runs.
	mutation bool
	// card is the token a mutation ceremony unlocked with, until released.
	card Card
	// restore is the state the vault returns to when the ceremony ends
	// without publishing: Locked for an unlock, None for a create from
	// nothing.
	restore VaultState

	done chan struct{}
}

// parkAt is an error a flow returns to be parked by its caller after the
// caller has released what it holds (the keystore file, the card).
type parkAt struct {
	step CeremonyStep
	code Code
}

func (p *parkAt) Error() string { return "park: " + string(p.code) }

// prompt is a mailbox with an identity: answered once, by the id it was
// issued with, never by the next prompt's answer.
type prompt struct {
	id   string
	kind string // pin | password | recovery | mgmtkey
	ch   chan string
}

// UnlockMethod is which way in a ceremony takes.
type UnlockMethod string

const (
	MethodToken    UnlockMethod = "token"
	MethodPassword UnlockMethod = "password"
	MethodRecovery UnlockMethod = "recovery"
)

// promptWait is how long a prompt or the wait for a key may stand: the
// session's absolute default. The card call itself is never bounded.
const promptWait = defaultAbsolute

// readerPoll is how often the waiting state looks for a reader.
const readerPoll = 500 * time.Millisecond

// BeginUnlock starts a ceremony. One at a time; ceremony.in_progress or
// ceremony.releasing otherwise.
func (c *Core) BeginUnlock(method UnlockMethod) *Error {
	c.mu.Lock()
	v := &c.vault
	switch v.state {
	case StateLocked:
	case StateUnlocking:
		c.mu.Unlock()
		return coded(CodeCeremonyRunning)
	case StateReleasing:
		c.mu.Unlock()
		return coded(CodeReleasing)
	case StateBroken:
		c.mu.Unlock()
		return coded(CodeVaultBroken)
	case StateBusy:
		c.mu.Unlock()
		return coded(CodeVaultBusy)
	case StateUnlocked:
		c.mu.Unlock()
		return nil
	default:
		c.mu.Unlock()
		return coded(CodeNoVault)
	}
	if c.cer != nil {
		// A cancelled ceremony is still finishing with the handle.
		c.mu.Unlock()
		return coded(CodeCeremonyRunning)
	}
	switch method {
	case MethodToken:
		if c.deps.Cards == nil {
			c.mu.Unlock()
			return coded(CodeTokenNoService)
		}
	case MethodPassword, MethodRecovery:
	default:
		c.mu.Unlock()
		return coded(CodeParams)
	}
	cer := c.newCeremonyLocked("unlock")
	v.state = StateUnlocking
	c.bump()
	c.mu.Unlock()
	c.emitState()
	go cer.run(func(ctx context.Context) error { return cer.unlock(method) })
	return nil
}

// newCeremonyLocked installs a ceremony. Caller holds the state mutex.
func (c *Core) newCeremonyLocked(kind string) *ceremony {
	ctx, cancel := context.WithCancel(context.Background())
	cer := &ceremony{kind: kind, ctx: ctx, cancel: cancel, c: c, done: make(chan struct{}), restore: c.vault.state}
	if cer.restore != StateNone {
		cer.restore = StateLocked
	}
	cer.state = CeremonyState{Kind: kind, Step: StepWaitingForKey, Seq: c.seq}
	c.cer = cer
	return cer
}

// CancelUnlock ends the ceremony from the user's side. Idempotent.
func (c *Core) CancelUnlock() *Error {
	c.mu.Lock()
	cer := c.cer
	if cer == nil {
		c.mu.Unlock()
		return coded(CodeNoCeremony)
	}
	cer.cancelReason = "user"
	cer.cancel()
	c.mu.Unlock()
	return nil
}

// SubmitSecret answers the outstanding prompt of the given kind and id.
// It never blocks; a wrong or stale id is ceremony.stale_prompt. The
// value goes into a one-slot mailbox the prompt owns.
func (c *Core) SubmitSecret(kind, promptID, value string) *Error {
	c.mu.Lock()
	cer := c.cer
	if cer == nil {
		c.mu.Unlock()
		return coded(CodeNoCeremony)
	}
	p := cer.prompt
	if p == nil || p.id != promptID || p.kind != kind {
		c.mu.Unlock()
		return coded(CodeStalePrompt)
	}
	cer.prompt = nil // consumed exactly once
	c.mu.Unlock()
	p.ch <- value // cap 1, and the slot was empty: never blocks
	return nil
}

// run executes fn with a top-level recover that routes to "lock and
// report", and always ends the ceremony.
func (cer *ceremony) run(fn func(ctx context.Context) error) {
	defer close(cer.done)
	defer func() {
		if r := recover(); r != nil {
			cer.c.log("ceremony panic: %v", r)
			cer.finish(coded(CodeInternal))
			cer.c.LockNow(ReasonPanic)
		}
	}()
	cer.set(func(*CeremonyState) {}) // the opening state, so the panel starts from an event
	err := fn(cer.ctx)
	var e *Error
	if err != nil {
		var pk *parkAt
		switch {
		case errors.As(err, &pk):
			// The flow asked to be parked after releasing what it held.
			err = cer.park(pk.step, pk.code)
			e = coded(CodeCancelled)
		case errors.Is(err, context.Canceled), errors.Is(err, ErrTokenCancelled):
			e = coded(CodeCancelled)
		default:
			e = classify(err)
			if e.Code == CodeInternal {
				cer.c.log("ceremony: %v", err)
			}
		}
		// A commit whose outcome is unknown, or one the file refused as
		// conflicting, leaves the handle broken: the state says so.
		if cer.mutation && (errors.Is(err, keystore.ErrIndeterminate) || errors.Is(err, keystore.ErrConflict)) {
			cer.c.mu.Lock()
			cer.c.brokenLocked(err)
			cer.c.mu.Unlock()
		}
	}
	cer.finish(e)
}

// check reports the cancellation, for a flow between two blocking steps.
func (cer *ceremony) check() error {
	if cer.ctx.Err() != nil {
		return ErrTokenCancelled
	}
	return nil
}

// releaseCard closes the card a mutation ceremony unlocked with, once.
func (cer *ceremony) releaseCard() {
	cer.c.mu.Lock()
	card := cer.card
	cer.card = nil
	cer.c.mu.Unlock()
	cer.closeCard(card)
}

// finish clears the ceremony and returns the vault to Locked unless the
// flow published Unlocked. After a slot change the receipts that waited
// for it are written.
func (cer *ceremony) finish(e *Error) {
	c := cer.c
	c.mu.Lock()
	if c.cer == cer {
		c.cer = nil
	}
	if c.vault.state == StateUnlocking || c.vault.state == StateReleasing {
		c.vault.state = cer.restore
	}
	if cer.mutation && c.vault.state == StateUnlocked {
		c.applyOwedLocked()
	}
	cer.cancel()
	if e != nil {
		cer.state.Step, cer.state.Error = StepFailed, e.Code
	} else if cer.state.Step != StepDone && cer.state.Step != StepRecovery {
		// StepRecovery stays: the final event is the one-time URL the
		// panel shows after the flow has ended.
		cer.state.Step = StepDone
	}
	final := cer.state
	final.Seq = c.bump()
	c.mu.Unlock()
	c.emit(EventVaultCeremony, final)
	c.emitState()
}

// set updates the visible state and emits it.
func (cer *ceremony) set(mut func(s *CeremonyState)) {
	c := cer.c
	c.mu.Lock()
	mut(&cer.state)
	cer.state.Seq = c.bump()
	st := cer.state
	c.mu.Unlock()
	c.emit(EventVaultCeremony, st)
}

// ask issues a prompt of kind, waits for its answer, the cancellation or
// the prompt deadline. status is what the panel shows.
func (cer *ceremony) ask(kind string, step CeremonyStep, status PINStatus) (string, error) {
	c := cer.c
	p := &prompt{id: randomID(), kind: kind, ch: make(chan string, 1)}
	c.mu.Lock()
	cer.prompt = p
	cer.state.Step, cer.state.PromptID = step, p.id
	cer.state.Retries, cer.state.RetriesKnown, cer.state.Verified = status.Retries, status.RetriesKnown, status.Verified
	cer.state.Error = ""
	cer.state.Seq = c.bump()
	st := cer.state
	c.mu.Unlock()
	c.emit(EventVaultCeremony, st)
	timer := c.deps.Clock.AfterFunc(promptWait, func() { cer.cancelWith("prompt_deadline") })
	defer timer.Stop()
	select {
	case v := <-p.ch:
		c.mu.Lock()
		if cer.prompt == p {
			cer.prompt = nil
		}
		cer.state.PromptID = ""
		c.mu.Unlock()
		return v, nil
	case <-cer.ctx.Done():
		c.mu.Lock()
		if cer.prompt == p {
			cer.prompt = nil
		}
		cer.state.PromptID = ""
		c.mu.Unlock()
		return "", ErrTokenCancelled
	}
}

func (cer *ceremony) cancelWith(reason string) {
	cer.c.mu.Lock()
	if cer.cancelReason == "" {
		cer.cancelReason = reason
	}
	cer.c.mu.Unlock()
	cer.cancel()
}

// wait sleeps d or until cancelled.
func (cer *ceremony) wait(d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-cer.ctx.Done():
		return ErrTokenCancelled
	}
}

// park holds the ceremony in a terminal-until-cancelled step.
func (cer *ceremony) park(step CeremonyStep, code Code) error {
	cer.set(func(s *CeremonyState) { s.Step, s.Error = step, code })
	<-cer.ctx.Done()
	return ErrTokenCancelled
}

// ceremonyPrompter is the piv-facing side: PIN from the mailbox, touch as
// an event.
type ceremonyPrompter struct {
	cer   *ceremony
	label string
	mu    sync.Mutex
	n     int
}

func (p *ceremonyPrompter) PIN(status PINStatus) (string, error) {
	if status.Blocked() {
		return "", ErrTokenPINBlocked
	}
	p.cer.set(func(s *CeremonyState) { s.SlotLabel = p.label })
	return p.cer.ask("pin", StepPIN, status)
}

func (p *ceremonyPrompter) Touch(req TouchRequest) {
	p.mu.Lock()
	p.n = req.N
	p.mu.Unlock()
	p.cer.set(func(s *CeremonyState) {
		s.Step, s.N, s.PINAsked, s.PromptID = StepTouch, req.N, req.PINAsked, ""
	})
}

// waitForNoReader waits until every reader is gone: the key that unlocked
// has been removed, so the one inserted next is a different key.
func (cer *ceremony) waitForNoReader() error {
	deadline := cer.c.deps.Clock.AfterFunc(promptWait, func() { cer.cancelWith("wait_deadline") })
	defer deadline.Stop()
	for {
		names, err := cer.c.deps.Cards.Readers()
		if err != nil {
			if errors.Is(err, ErrTokenNoService) {
				return cer.park(StepFailed, CodeTokenNoService)
			}
			return err
		}
		if len(names) == 0 {
			return nil
		}
		if err := cer.wait(readerPoll); err != nil {
			return err
		}
	}
}

// waitForOneReader is the WaitingForKey / TwoKeys loop.
func (cer *ceremony) waitForOneReader() (string, error) {
	deadline := cer.c.deps.Clock.AfterFunc(promptWait, func() { cer.cancelWith("wait_deadline") })
	defer deadline.Stop()
	for {
		names, err := cer.c.deps.Cards.Readers()
		if err != nil {
			if errors.Is(err, ErrTokenNoService) {
				return "", cer.park(StepFailed, CodeTokenNoService)
			}
			return "", err
		}
		switch len(names) {
		case 1:
			return names[0], nil
		case 0:
			cer.setIf(StepWaitingForKey, 0)
		default:
			cer.setIf(StepTwoKeys, len(names))
		}
		if err := cer.wait(readerPoll); err != nil {
			return "", err
		}
	}
}

// setIf updates the step only when it changes, so the poll does not flood.
func (cer *ceremony) setIf(step CeremonyStep, readers int) {
	c := cer.c
	c.mu.Lock()
	if cer.state.Step == step && cer.state.ReaderCount == readers {
		c.mu.Unlock()
		return
	}
	cer.state.Step, cer.state.ReaderCount, cer.state.Error = step, readers, ""
	cer.state.Seq = c.bump()
	st := cer.state
	c.mu.Unlock()
	c.emit(EventVaultCeremony, st)
}

// openCard opens the one reader, parking on the errors the user must act
// on (DESIGN trap 24: never retry a reader in a loop).
func (cer *ceremony) openCard(reader string) (Card, error) {
	card, err := cer.c.deps.Cards.Open(reader)
	if err != nil {
		switch {
		case errors.Is(err, ErrTokenBusy):
			return nil, cer.park(StepBusy, CodeTokenBusy)
		case errors.Is(err, ErrTokenNoPIV):
			return nil, cer.park(StepFailed, CodeTokenNoPIV)
		case errors.Is(err, ErrTokenUnsupported):
			return nil, cer.park(StepFailed, CodeTokenUnsupport)
		case errors.Is(err, ErrTokenNoCard):
			return nil, cer.park(StepFailed, CodeTokenNoCard)
		}
		return nil, err
	}
	return card, nil
}

// matchSlot finds the vault's hardware slot the card holds a key for.
func (cer *ceremony) matchSlot(card Card) (keystore.SlotInfo, KeyInfo, bool, error) {
	keys, err := card.Keys()
	if err != nil {
		return keystore.SlotInfo{}, KeyInfo{}, false, err
	}
	cer.c.mu.Lock()
	slots := cer.c.vault.slots
	cer.c.mu.Unlock()
	for _, k := range keys {
		if k.PublicKey == nil {
			continue
		}
		for _, s := range slots {
			if s.PublicKey != nil && string(s.PublicKey) == string(k.PublicKey) {
				return s, k, true, nil
			}
		}
	}
	return keystore.SlotInfo{}, KeyInfo{}, false, nil
}

// credential runs the token part of the flow and returns a credential the
// keystore can take, with the Card to close afterwards. The password of an
// entangled slot is collected before the PIN (the credential is assembled
// whole; the PIN prompt fires inside ECDH).
func (cer *ceremony) tokenCredential() (keystore.HardwareCredential, Card, keystore.SlotInfo, error) {
	reader, err := cer.waitForOneReader()
	if err != nil {
		return keystore.HardwareCredential{}, nil, keystore.SlotInfo{}, err
	}
	cer.set(func(s *CeremonyState) { s.Step, s.ReaderCount = StepProbing, 1 })
	card, err := cer.openCard(reader)
	if err != nil {
		return keystore.HardwareCredential{}, nil, keystore.SlotInfo{}, err
	}
	slot, key, ok, err := cer.matchSlot(card)
	if err != nil {
		card.Close()
		return keystore.HardwareCredential{}, nil, keystore.SlotInfo{}, err
	}
	if !ok {
		card.Close()
		return keystore.HardwareCredential{}, nil, keystore.SlotInfo{}, cer.park(StepNoMatch, CodeTokenNoKey)
	}
	if slot.Stale {
		card.Close()
		return keystore.HardwareCredential{}, nil, keystore.SlotInfo{}, cer.park(StepFailed, CodeVaultStale)
	}
	cred := keystore.HardwareCredential{}
	if slot.EntangledPassword {
		pw, err := cer.ask("password", StepPassword, PINStatus{})
		if err != nil {
			card.Close()
			return keystore.HardwareCredential{}, nil, keystore.SlotInfo{}, err
		}
		cred.Password = pw
	}
	tok, err := card.Token(key.PublicKey, &ceremonyPrompter{cer: cer, label: slot.Label})
	if err != nil {
		card.Close()
		if errors.Is(err, ErrTokenNotUsable) {
			return keystore.HardwareCredential{}, nil, keystore.SlotInfo{}, cer.park(StepFailed, CodeTokenNotUsable)
		}
		return keystore.HardwareCredential{}, nil, keystore.SlotInfo{}, err
	}
	cred.Token = tok
	return cred, card, slot, nil
}

// closeCard runs the release step; a failed reset is a warning.
func (cer *ceremony) closeCard(card Card) {
	if card == nil {
		return
	}
	cer.set(func(s *CeremonyState) { s.Step = StepReleasing })
	c := cer.c
	c.mu.Lock()
	if c.vault.state == StateUnlocking {
		c.vault.state = StateReleasing
	}
	c.mu.Unlock()
	if err := card.Close(); err != nil && errors.Is(err, ErrTokenResetFailed) {
		c.mu.Lock()
		c.vault.warnings[CodeTokenReset] = true
		c.mu.Unlock()
		c.emit(EventVaultWarning, Warning{Code: CodeTokenReset})
	}
}

// unlockWith derives the session with the credential, retrying the PIN,
// touch and password errors the way §2.2 says, on the same open Card.
func (cer *ceremony) unlockWith(ks *keystore.Keystore, cred keystore.Credential, hc *keystore.HardwareCredential) (*keystore.Unlocked, error) {
	for attempt := 0; ; attempt++ {
		if hc != nil {
			cred = *hc
		}
		unl, err := ks.Unlock(cred)
		if err == nil {
			return unl, nil
		}
		var pe *TokenPINError
		switch {
		case hc != nil && errors.As(err, &pe):
			// The prompter already showed the count; ECDH will prompt again.
			continue
		case hc != nil && errors.Is(err, ErrTokenTouch):
			continue
		case hc != nil && errors.Is(err, ErrTokenPINRequired):
			continue
		case hc != nil && errors.Is(err, ErrTokenPINBlocked):
			return nil, &parkAt{StepBlocked, CodeTokenPINBlocked}
		case hc != nil && errors.Is(err, ErrTokenTooMany):
			return nil, &parkAt{StepFailed, CodeTokenTooMany}
		case hc != nil && errors.Is(err, keystore.ErrAuth) && hc.Password != "":
			// The entangled password was wrong; ask again, same card.
			pw, aerr := cer.ask("password", StepPassword, PINStatus{})
			if aerr != nil {
				return nil, aerr
			}
			hc.Password = pw
			continue
		case errors.Is(err, keystore.ErrStale):
			return nil, &parkAt{StepFailed, CodeVaultStale}
		case errors.Is(err, ErrTokenCancelled), errors.Is(err, context.Canceled):
			return nil, err
		}
		return nil, err
	}
}

// openVault opens the vault file for an unlock, parking on what the user
// must act on.
func (cer *ceremony) openVault(path string) (*keystore.Keystore, error) {
	ks, err := keystore.Open(path)
	if err != nil {
		switch {
		case errors.Is(err, keystore.ErrBusy):
			return nil, &parkAt{StepFailed, CodeVaultBusy}
		case errors.Is(err, fs.ErrNotExist):
			return nil, &parkAt{StepFailed, CodeVaultNotFound}
		case errors.Is(err, format.ErrInvalid), errors.Is(err, format.ErrTruncated):
			return nil, &parkAt{StepFailed, CodeVaultInvalid}
		}
		return nil, err
	}
	return ks, nil
}

// unlock is the whole unlock flow.
func (cer *ceremony) unlock(method UnlockMethod) error {
	c := cer.c
	c.mu.Lock()
	path := c.vault.path
	c.mu.Unlock()

	var (
		cred keystore.Credential
		hc   *keystore.HardwareCredential
		card Card
	)
	switch method {
	case MethodToken:
		h, cd, _, err := cer.tokenCredential()
		if err != nil {
			return err
		}
		card, hc = cd, &h
		defer cer.closeCard(card)
	case MethodPassword:
		pw, err := cer.ask("password", StepPassword, PINStatus{})
		if err != nil {
			return err
		}
		cred = keystore.PasswordCredential{Password: pw}
	case MethodRecovery:
		digits, err := cer.ask("recovery", StepRecovery, PINStatus{})
		if err != nil {
			return err
		}
		rk, perr := kdf.ParseRecoveryDigits(digits)
		if perr != nil {
			return cer.park(StepFailed, CodeAuth)
		}
		cred = keystore.RecoveryCredential{Key: rk}
	}

	ks, err := cer.openVault(path)
	if err != nil {
		return err
	}
	cer.set(func(s *CeremonyState) { s.Step = StepDeriving })
	unl, err := cer.unlockWith(ks, cred, hc)
	if err != nil {
		ks.Close() // the file is held open only while Unlocked; a park is not that
		if errors.Is(err, keystore.ErrAuth) || errors.Is(err, keystore.ErrNoSlot) || errors.Is(err, keystore.ErrVerifier) {
			return &parkAt{StepFailed, CodeAuth}
		}
		return err
	}
	// Publish, unless a lock trigger latched the ceremony or the user
	// cancelled it while the keys were being derived.
	c.mu.Lock()
	if cer.latch || cer.ctx.Err() != nil {
		c.mu.Unlock()
		unl.Close()
		ks.Close()
		return ErrTokenCancelled
	}
	c.publishUnlockedLocked(ks, unl)
	c.applyOwedLocked()
	c.mu.Unlock()
	cer.set(func(s *CeremonyState) { s.Step = StepDone })
	if hc != nil {
		hc.Token = nil
	}
	return nil
}

var _ = fmt.Sprintf

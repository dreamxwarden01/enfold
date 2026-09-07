package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
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
	// commits: an import or a build that installs a file at the vault's
	// place; shutdown waits for it, so the install is never half done.
	commits bool
	// card is the token a mutation ceremony unlocked with, until released;
	// unlockPub and unlockLabel say which slot it was, for the swap that
	// follows an enrolment.
	card        Card
	unlockPub   []byte
	unlockLabel string
	// held is the card open right now, probed while a prompt stands.
	held Card
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
	id     string
	kind   string // pin | password | recovery | mgmtkey
	choose bool   // a secret being chosen now: the minimum applies
	ch     chan string
	gone   chan error // the key held for this prompt went away
}

// keepAliveEvery is how often a held card is probed while a prompt stands:
// the host resets an exclusive connection that carries nothing for about
// five seconds (DESIGN.md §11 trap 25), and a probe every three keeps it
// — and tells within three seconds that the key was pulled.
var keepAliveEvery = 3 * time.Second

// keyGone reports the errors that mean the key is no longer there to talk
// to: the flow goes back to waiting for it.
func keyGone(err error) bool {
	return errors.Is(err, ErrTokenNoCard) || errors.Is(err, ErrTokenNoReader) || errors.Is(err, ErrTokenNoService) || errors.Is(err, ErrTokenReset)
}

// minPasswordLen is the least a chosen password may be: BitLocker's rule,
// checked here as the backstop and on the page as the guide. Neither an
// entropy estimate nor a strength meter is a substitute for it.
const minPasswordLen = 8

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
	case MethodToken, MethodPassword:
		if c.setupNeededLocked() {
			// A backup adopted but not set up has no such way in; the
			// recovery key still opens it, and FinishSetup is the action.
			c.mu.Unlock()
			return coded(CodeSetupNeeded)
		}
		if method == MethodToken && c.deps.Cards == nil {
			c.mu.Unlock()
			return coded(CodeTokenNoService)
		}
	case MethodRecovery:
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
	if p.kind == "password" && p.choose && len([]rune(value)) < minPasswordLen {
		// The prompt stands; the page says why.
		c.mu.Unlock()
		return coded(CodePasswordShort)
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
			cer.c.log("ceremony %s: parked at %s: %s", cer.kind, pk.step, pk.code)
			err = cer.park(pk.step, pk.code)
			e = coded(CodeCancelled)
		case errors.Is(err, context.Canceled), errors.Is(err, ErrTokenCancelled):
			e = coded(CodeCancelled)
			cer.c.mu.Lock()
			step, code, reason := cer.state.Step, cer.state.Error, cer.cancelReason
			cer.c.mu.Unlock()
			if code != "" && code != CodeCancelled {
				cer.c.log("ceremony %s: parked at %s: %s (left by %s)", cer.kind, step, code, mustString(reason, "user"))
			} else {
				cer.c.log("ceremony %s: cancelled (%s)", cer.kind, mustString(reason, "user"))
			}
		default:
			e = classify(err)
			cer.c.log("ceremony %s: failed: %s: %v", cer.kind, e.Code, err)
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
	return cer.askWith(kind, step, status, false)
}

// askNew asks for a secret the user is choosing now — a new password, a
// new entangled password — rather than one they already hold; the state
// says so (Choose) so the page can label the field "choose".
func (cer *ceremony) askNew(kind string, step CeremonyStep) (string, error) {
	return cer.askWith(kind, step, PINStatus{}, true)
}

func (cer *ceremony) askWith(kind string, step CeremonyStep, status PINStatus, choose bool) (string, error) {
	c := cer.c
	p := &prompt{id: randomID(), kind: kind, choose: choose, ch: make(chan string, 1), gone: make(chan error, 1)}
	c.mu.Lock()
	cer.prompt = p
	held := cer.held
	cer.state.Step, cer.state.PromptID, cer.state.Choose = step, p.id, choose
	cer.state.Retries, cer.state.RetriesKnown, cer.state.Verified = status.Retries, status.RetriesKnown, status.Verified
	cer.state.Error = ""
	cer.state.Seq = c.bump()
	st := cer.state
	c.mu.Unlock()
	c.emit(EventVaultCeremony, st)
	timer := c.deps.Clock.AfterFunc(promptWait, func() { cer.cancelWith("prompt_deadline") })
	defer timer.Stop()
	stop := make(chan struct{})
	defer close(stop)
	if held != nil {
		go cer.keepAlive(held, p, stop)
	}
	clear := func() {
		c.mu.Lock()
		if cer.prompt == p {
			cer.prompt = nil
		}
		cer.state.PromptID, cer.state.Choose = "", false
		c.mu.Unlock()
	}
	select {
	case v := <-p.ch:
		clear()
		return v, nil
	case err := <-p.gone:
		clear()
		return "", err
	case <-cer.ctx.Done():
		clear()
		return "", ErrTokenCancelled
	}
}

// keepAlive probes the held card while a prompt stands, so that the
// exclusive connection is never idle long enough for the host to reset it
// and so that a key pulled meanwhile is noticed: the prompt then ends with
// the key's error and the flow goes back to waiting for it. A probe that
// finds the card busy with its own operation, or closed, says nothing.
func (cer *ceremony) keepAlive(card Card, p *prompt, stop <-chan struct{}) {
	t := time.NewTicker(keepAliveEvery)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		_, err := card.PINState()
		switch {
		case err == nil, errors.Is(err, ErrTokenBusy), errors.Is(err, ErrTokenClosed):
			continue
		case keyGone(err):
			cer.c.log("ceremony %s: the key went away during the prompt: %v", cer.kind, err)
			select {
			case p.gone <- err:
			default:
			}
			return
		default:
			cer.c.log("ceremony %s: probe while prompting: %v", cer.kind, err)
		}
	}
}

// awayNote sets the waiting state's note to say the key went away, so
// the strip tells the user to insert it again; the note clears when a
// reader is seen.
func (cer *ceremony) awayNote(err error) {
	cer.c.log("ceremony %s: back to waiting for the key: %v", cer.kind, err)
	c := cer.c
	c.mu.Lock()
	cer.state.Step, cer.state.ReaderCount, cer.state.Error, cer.state.PromptID = StepWaitingForKey, 0, CodeTokenNoCard, ""
	cer.state.Seq = c.bump()
	st := cer.state
	c.mu.Unlock()
	c.emit(EventVaultCeremony, st)
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

// noServiceNoteAfter is how many consecutive polls without the Smart Card
// service pass before the waiting state says so (10 s at readerPoll).
var noServiceNoteAfter = 20

// readerPoller lists the readers for a waiting loop, treating a stopped
// Smart Card service as no reader: Windows starts the service when a
// reader arrives and stops it when the last one leaves, so "no service"
// while waiting is the empty reader set, not a failure. It is logged once
// per wait, and after noServiceNoteAfter polls in a row the waiting state
// carries token.no_service as a note, so a service that stays down is
// not an hour of silence.
type readerPoller struct {
	cer    *ceremony
	logged bool
	run    int
}

func (p *readerPoller) names() ([]string, error) {
	names, err := p.cer.c.deps.Cards.Readers()
	if err != nil {
		if !errors.Is(err, ErrTokenNoService) {
			return nil, err
		}
		if !p.logged {
			p.cer.c.log("ceremony %s: no Smart Card service; treated as no reader", p.cer.kind)
			p.logged = true
		}
		p.run++
		if p.run == noServiceNoteAfter {
			p.cer.note(CodeTokenNoService)
		}
		return nil, nil
	}
	if p.run > 0 {
		p.run = 0
		p.cer.note("")
	}
	return names, nil
}

// note sets or clears the waiting state's note without changing the step.
func (cer *ceremony) note(code Code) {
	c := cer.c
	c.mu.Lock()
	if cer.state.Error == code {
		c.mu.Unlock()
		return
	}
	cer.state.Error = code
	cer.state.Seq = c.bump()
	st := cer.state
	c.mu.Unlock()
	c.emit(EventVaultCeremony, st)
}

// waitForOtherKey waits until the key that unlocked is out of the reader:
// no reader, or the one card present is another key. The swap may already
// have happened while a prompt stood, and a swap in the same port shows
// the same reader name throughout, so a present card is probed for the
// unlocking key (Keys: no PIN, no touch) once per poll instead of waiting
// for a reader set that may never look empty. Two readers are left to
// waitForOneReader's TwoKeys.
func (cer *ceremony) waitForOtherKey(unlockPub []byte) error {
	deadline := cer.c.deps.Clock.AfterFunc(promptWait, func() { cer.cancelWith("wait_deadline") })
	defer deadline.Stop()
	poll := &readerPoller{cer: cer}
	for {
		names, err := poll.names()
		if err != nil {
			return err
		}
		if len(names) != 1 {
			return nil
		}
		same, err := cer.holdsKey(names[0], unlockPub)
		if err != nil {
			return err
		}
		if !same {
			return nil
		}
		if err := cer.wait(readerPoll); err != nil {
			return err
		}
	}
}

// holdsKey opens the reader long enough to see whether its card holds
// pub. Nothing is verified, so the close has no reset to report.
func (cer *ceremony) holdsKey(reader string, pub []byte) (bool, error) {
	card, err := cer.openCard(reader)
	if err != nil {
		return false, err
	}
	keys, err := card.Keys()
	card.Close()
	if err != nil {
		return false, err
	}
	for _, k := range keys {
		if k.PublicKey != nil && string(k.PublicKey) == string(pub) {
			return true, nil
		}
	}
	return false, nil
}

// waitForOneReader is the WaitingForKey / TwoKeys loop.
func (cer *ceremony) waitForOneReader() (string, error) {
	deadline := cer.c.deps.Clock.AfterFunc(promptWait, func() { cer.cancelWith("wait_deadline") })
	defer deadline.Stop()
	poll := &readerPoller{cer: cer}
	for {
		names, err := poll.names()
		if err != nil {
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

// setIf updates the step only when it changes, so the poll does not flood;
// a note the poller set stands.
func (cer *ceremony) setIf(step CeremonyStep, readers int) {
	c := cer.c
	c.mu.Lock()
	if cer.state.Step == step && cer.state.ReaderCount == readers {
		c.mu.Unlock()
		return
	}
	cer.state.Step, cer.state.ReaderCount = step, readers
	if cer.state.Error != CodeTokenNoService && !(cer.state.Error == CodeTokenNoCard && readers == 0) {
		cer.state.Error = "" // a note stands while nothing is inserted
	}
	cer.state.Seq = c.bump()
	st := cer.state
	c.mu.Unlock()
	c.emit(EventVaultCeremony, st)
}

// openCard opens the one reader, parking on the errors the user must act
// on (DESIGN trap 24: never retry a reader in a loop).
// busyRetries is how long an open is retried when another program holds
// the card — a few seconds: Windows' own services take a card for a
// moment after it is inserted or reset — before the ceremony parks.
var busyRetries, busyRetryEvery = 8, 250 * time.Millisecond

func (cer *ceremony) openCard(reader string) (Card, error) {
	var card Card
	var err error
	for attempt := 0; ; attempt++ {
		card, err = cer.c.deps.Cards.Open(reader)
		if err == nil {
			break
		}
		if errors.Is(err, ErrTokenBusy) && attempt < busyRetries {
			if err := cer.wait(busyRetryEvery); err != nil {
				return nil, err
			}
			continue
		}
		cer.c.log("ceremony %s: open %q: %v", cer.kind, reader, err)
		switch {
		case errors.Is(err, ErrTokenBusy):
			return nil, cer.park(StepBusy, CodeTokenBusy)
		case errors.Is(err, ErrTokenNoPIV):
			return nil, cer.park(StepFailed, CodeTokenNoPIV)
		case errors.Is(err, ErrTokenUnsupported):
			return nil, cer.park(StepFailed, CodeTokenUnsupport)
		}
		return nil, err // a key gone is the caller's to wait for again
	}
	cer.c.mu.Lock()
	cer.held = card
	cer.c.mu.Unlock()
	return card, nil
}

// unhold forgets a card that is being closed by hand.
func (cer *ceremony) unhold(card Card) {
	cer.c.mu.Lock()
	if cer.held == card {
		cer.held = nil
	}
	cer.c.mu.Unlock()
}

// matchSlot finds the vault's hardware slot the card holds a key for.
func (cer *ceremony) matchSlot(card Card, slots []keystore.SlotInfo) (keystore.SlotInfo, KeyInfo, []KeyInfo, bool, error) {
	keys, err := card.Keys()
	if err != nil {
		return keystore.SlotInfo{}, KeyInfo{}, nil, false, err
	}
	for _, k := range keys {
		if k.PublicKey == nil {
			continue
		}
		for _, s := range slots {
			if s.PublicKey != nil && string(s.PublicKey) == string(k.PublicKey) {
				return s, k, keys, true, nil
			}
		}
	}
	return keystore.SlotInfo{}, KeyInfo{}, keys, false, nil
}

// credential runs the token part of the flow and returns a credential the
// keystore can take, with the Card to close afterwards. The password of an
// entangled slot is collected before the PIN (the credential is assembled
// whole; the PIN prompt fires inside ECDH).
func (cer *ceremony) tokenCredentialFor(slots []keystore.SlotInfo) (keystore.HardwareCredential, Card, keystore.SlotInfo, error) {
	reader, err := cer.waitForOneReader()
	if err != nil {
		return keystore.HardwareCredential{}, nil, keystore.SlotInfo{}, err
	}
	cer.set(func(s *CeremonyState) { s.Step, s.ReaderCount = StepProbing, 1 })
	card, err := cer.openCard(reader)
	if err != nil {
		return keystore.HardwareCredential{}, nil, keystore.SlotInfo{}, err
	}
	slot, key, keys, ok, err := cer.matchSlot(card, slots)
	if err != nil {
		cer.c.log("ceremony %s: reading the keys: %v", cer.kind, err)
		cer.unhold(card)
		card.Close()
		return keystore.HardwareCredential{}, nil, keystore.SlotInfo{}, err
	}
	if !ok {
		cer.c.log("ceremony %s: no slot matches the keys on the token (%s)", cer.kind, describeKeys(keys))
		cer.unhold(card)
		card.Close()
		return keystore.HardwareCredential{}, nil, keystore.SlotInfo{}, cer.park(StepNoMatch, CodeTokenNoKey)
	}
	if slot.Stale {
		cer.unhold(card)
		card.Close()
		return keystore.HardwareCredential{}, nil, keystore.SlotInfo{}, cer.park(StepFailed, CodeVaultStale)
	}
	cred := keystore.HardwareCredential{}
	if slot.EntangledPassword {
		pw, err := cer.ask("password", StepPassword, PINStatus{})
		if err != nil {
			cer.unhold(card)
			card.Close()
			return keystore.HardwareCredential{}, nil, keystore.SlotInfo{}, err
		}
		cred.Password = pw
	}
	tok, err := card.Token(key.PublicKey, &ceremonyPrompter{cer: cer, label: slot.Label})
	if err != nil {
		cer.unhold(card)
		card.Close()
		if errors.Is(err, ErrTokenNotUsable) {
			return keystore.HardwareCredential{}, nil, keystore.SlotInfo{}, cer.park(StepFailed, CodeTokenNotUsable)
		}
		return keystore.HardwareCredential{}, nil, keystore.SlotInfo{}, err
	}
	cred.Token = tok
	return cred, card, slot, nil
}

// describeKeys lists what a token holds, for the log: slots, usability,
// why not. Nothing secret.
func describeKeys(keys []KeyInfo) string {
	var parts []string
	for _, k := range keys {
		s := fmt.Sprintf("%02x %s pin=%s touch=%s usable=%v", byte(k.Slot), k.Algorithm, k.PINPolicy, k.TouchPolicy, k.Usable)
		if !k.Usable {
			s += " (" + k.WhyNot + ")"
		}
		parts = append(parts, s)
	}
	if len(parts) == 0 {
		return "no keys"
	}
	return strings.Join(parts, "; ")
}

// closeCard runs the release step; a failed reset is a warning.
func (cer *ceremony) closeCard(card Card) {
	if card == nil {
		return
	}
	cer.unhold(card)
	cer.set(func(s *CeremonyState) { s.Step = StepReleasing })
	c := cer.c
	c.mu.Lock()
	if c.vault.state == StateUnlocking {
		c.vault.state = StateReleasing
	}
	c.mu.Unlock()
	err := card.Close()
	if err != nil {
		c.log("ceremony %s: releasing the key: %v", cer.kind, err)
	}
	if err != nil && errors.Is(err, ErrTokenResetFailed) {
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
	path, slots := c.vault.path, c.vault.slots
	c.mu.Unlock()

	var (
		cred keystore.Credential
		hc   *keystore.HardwareCredential
		card Card
		ks   *keystore.Keystore
		unl  *keystore.Unlocked
		err  error
	)
	for {
		cred, hc, card, err = cer.credential(method, slots)
		if err != nil {
			return err
		}
		ks, err = cer.openVault(path)
		if err != nil {
			cer.closeCard(card)
			return err
		}
		unl, err = cer.unlockFile(ks, cred, hc)
		if err != nil {
			ks.Close() // the file is held open only while Unlocked; a park is not that
			if hc != nil && keyGone(err) {
				// The key went during the PIN or the touch: back to waiting.
				cer.unhold(card)
				card.Close()
				cer.awayNote(err)
				continue
			}
			cer.closeCard(card)
			return err
		}
		break
	}
	if card != nil {
		defer cer.closeCard(card)
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

// credential collects the way in for method against slots — the vault's,
// or an incoming file's: the token flow (the Card returned is the
// caller's to close), a password, or the recovery digits. A key that goes
// away while the token flow runs — pulled during the PIN, reset under a
// probe that could not heal it — is waited for again, not a failure.
func (cer *ceremony) credential(method UnlockMethod, slots []keystore.SlotInfo) (keystore.Credential, *keystore.HardwareCredential, Card, error) {
	switch method {
	case MethodToken:
		for {
			h, card, _, err := cer.tokenCredentialFor(slots)
			if err != nil {
				if keyGone(err) {
					cer.awayNote(err)
					continue
				}
				return nil, nil, nil, err
			}
			return nil, &h, card, nil
		}
	case MethodPassword:
		pw, err := cer.ask("password", StepPassword, PINStatus{})
		if err != nil {
			return nil, nil, nil, err
		}
		return keystore.PasswordCredential{Password: pw}, nil, nil, nil
	case MethodRecovery:
		digits, err := cer.ask("recovery", StepRecovery, PINStatus{})
		if err != nil {
			return nil, nil, nil, err
		}
		rk, perr := kdf.ParseRecoveryDigits(digits)
		if perr != nil {
			return nil, nil, nil, cer.park(StepFailed, CodeAuth)
		}
		return keystore.RecoveryCredential{Key: rk}, nil, nil, nil
	}
	return nil, nil, nil, coded(CodeParams)
}

// unlockFile derives the VMK from an open file, mapping a refusal to the
// park the user must act on. The caller closes ks on error.
func (cer *ceremony) unlockFile(ks *keystore.Keystore, cred keystore.Credential, hc *keystore.HardwareCredential) (*keystore.Unlocked, error) {
	cer.set(func(s *CeremonyState) { s.Step = StepDeriving })
	unl, err := cer.unlockWith(ks, cred, hc)
	if err != nil {
		if errors.Is(err, keystore.ErrAuth) || errors.Is(err, keystore.ErrNoSlot) || errors.Is(err, keystore.ErrVerifier) {
			return nil, &parkAt{StepFailed, CodeAuth}
		}
		return nil, err
	}
	return unl, nil
}

var _ = fmt.Sprintf

package app

import (
	"crypto/ecdh"
	"crypto/rand"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/dreamxwarden01/enfold/internal/kdf"
	"github.com/dreamxwarden01/enfold/internal/keystore"
)

// fast keeps the password slots cheap; the product's parameters are not
// what these tests are about.
var fast = kdf.Argon2Params{MemKiB: 64, Time: 1, Threads: 1}

func init() { defaultArgon2 = fast }

// fakeClock is a manual clock: AfterFunc timers fire from Advance, in
// order, outside the clock's lock.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	c       *fakeClock
	at      time.Time
	f       func()
	stopped bool
	fired   bool
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) AfterFunc(d time.Duration, f func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{c: c, at: c.now.Add(d), f: f}
	c.timers = append(c.timers, t)
	return t
}

func (t *fakeTimer) Stop() bool {
	t.c.mu.Lock()
	defer t.c.mu.Unlock()
	was := !t.stopped && !t.fired
	t.stopped = true
	return was
}

// Advance moves the clock and fires what came due, earliest first.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	var due []*fakeTimer
	var keep []*fakeTimer
	for _, t := range c.timers {
		switch {
		case t.stopped || t.fired:
		case !t.at.After(c.now):
			t.fired = true
			due = append(due, t)
		default:
			keep = append(keep, t)
		}
	}
	c.timers = keep
	c.mu.Unlock()
	sort.SliceStable(due, func(i, j int) bool { return due[i].at.Before(due[j].at) })
	for _, t := range due {
		t.f()
	}
}

// recorder keeps every event. Waits are sequential: each one scans from
// where the previous match ended, so a test's expectations read in the
// order the core emits.
type recorder struct {
	mu     sync.Mutex
	events []event
	cursor int
	// hook lets a test act inside an event, on the goroutine that emitted
	// it: the only way to reach a running operation at a known point —
	// cancelling one mid-add, for instance — without racing it.
	hook func(name string, payload any)
}

type event struct {
	name    string
	payload any
}

func (r *recorder) Emit(name string, payload any) {
	r.mu.Lock()
	r.events = append(r.events, event{name, payload})
	hook := r.hook
	r.mu.Unlock()
	if hook != nil {
		hook(name, payload)
	}
}

// onEvent installs the hook; nil clears it.
func (r *recorder) onEvent(f func(name string, payload any)) {
	r.mu.Lock()
	r.hook = f
	r.mu.Unlock()
}

// waitFor polls for an event matching pred, from the start; it fails the
// test after the timeout.
func (r *recorder) waitFor(t *testing.T, name string, pred func(any) bool) any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		for i := r.cursor; i < len(r.events); i++ {
			e := r.events[i]
			if e.name == name && pred(e.payload) {
				r.cursor = i + 1
				r.mu.Unlock()
				return e.payload
			}
		}
		r.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	r.mu.Lock()
	var names []string
	for i, e := range r.events {
		mark := ""
		if i == r.cursor {
			mark = ">> "
		}
		names = append(names, fmt.Sprintf("%s%s:%v", mark, e.name, brief(e.payload)))
	}
	r.mu.Unlock()
	t.Fatalf("no %s event matched after the cursor; saw:\n%v", name, names)
	return nil
}

func brief(p any) string {
	switch v := p.(type) {
	case VaultStatus:
		return string(v.State)
	case CeremonyState:
		return fmt.Sprintf("%s/%s/%s", v.Kind, v.Step, v.Error)
	case OpView:
		return fmt.Sprintf("%s/%s/%v/%s", v.Kind, v.Phase, v.Finished, v.Error)
	}
	return ""
}

// waitCeremony waits for a ceremony event at step with, when nonempty, a
// prompt id.
func (r *recorder) waitCeremony(t *testing.T, step CeremonyStep, needPrompt bool) CeremonyState {
	t.Helper()
	p := r.waitFor(t, EventVaultCeremony, func(p any) bool {
		s, ok := p.(CeremonyState)
		return ok && s.Step == step && (!needPrompt || s.PromptID != "")
	})
	return p.(CeremonyState)
}

// waitState waits for the vault to reach state.
func (r *recorder) waitState(t *testing.T, state VaultState) {
	t.Helper()
	r.waitFor(t, EventVaultState, func(p any) bool {
		s, ok := p.(VaultStatus)
		return ok && s.State == state
	})
}

// waitOp waits for the operation to finish and returns its view.
func (r *recorder) waitOp(t *testing.T, id string) OpView {
	t.Helper()
	p := r.waitFor(t, EventOpDone, func(p any) bool {
		o, ok := p.(OpView)
		return ok && o.ID == id
	})
	return p.(OpView)
}

// snapshot is every event recorded so far.
func (r *recorder) snapshot() []event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]event(nil), r.events...)
}

// reset moves the cursor past everything recorded so far.
func (r *recorder) reset() {
	r.mu.Lock()
	r.cursor = len(r.events)
	r.mu.Unlock()
}

// fakeCards is a controllable reader set and one card.
type fakeCards struct {
	mu         sync.Mutex
	readers    []string
	readersErr error // Readers fails with this while set
	openErr    error
	busyOpens  int // the next this many opens answer ErrTokenBusy
	card       *fakeCard
	opens      int // opens that succeeded
	attempts   int // every open
}

// setOpenErr makes every open fail with err (nil: succeed again).
func (f *fakeCards) setOpenErr(err error) {
	f.mu.Lock()
	f.openErr = err
	f.mu.Unlock()
}

func (f *fakeCards) openAttempts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attempts
}

func (f *fakeCards) setReaders(names ...string) {
	f.mu.Lock()
	f.readers = names
	f.mu.Unlock()
}

// openCount is how many times a card has been opened so far.
func (f *fakeCards) openCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.opens
}

// setCard swaps the card behind the reader: the key the user inserts next.
func (f *fakeCards) setCard(c *fakeCard) {
	f.mu.Lock()
	f.card = c
	f.mu.Unlock()
}

func (f *fakeCards) setReadersErr(err error) {
	f.mu.Lock()
	f.readersErr = err
	f.mu.Unlock()
}

func (f *fakeCards) Readers() ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.readersErr != nil {
		return nil, f.readersErr
	}
	return append([]string(nil), f.readers...), nil
}

func (f *fakeCards) Open(reader string) (Card, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts++
	if f.busyOpens > 0 {
		f.busyOpens--
		return nil, ErrTokenBusy
	}
	if f.openErr != nil {
		return nil, f.openErr
	}
	f.opens++
	f.card.mu.Lock()
	f.card.closed = false
	f.card.mu.Unlock()
	return f.card, nil
}

// fakeCard holds software keys and enforces PIN and touch the way the
// token does: every operation asks for the PIN, then a touch.
type fakeCard struct {
	mu        sync.Mutex
	keys      map[Slot]*ecdh.PrivateKey
	usable    map[Slot]bool
	pin       string
	retries   int
	closed    bool
	closes    int
	closeErr  error
	mgmt      []byte // the PIN-protected management key; nil when none
	touches   []TouchRequest
	ops       int
	removed   bool // pulled: every operation answers ErrTokenNoCard
	verified  bool // PIN-once: a VERIFY stands for this handle, as on the card
	proofLies bool // the agreement the token computes is not its key's: the proof must refuse it
	// verifies counts the VERIFYs the card was sent — the ceremony's own
	// (VerifyPIN) and the token's — and ecdhs every agreement asked for, so
	// that a test can prove the agreement sends no VERIFY after the
	// ceremony's, and a retry from the cached H reaches no card at all.
	verifies int
	ecdhs    int
	// touchFails: this many ECDH calls answer "not touched in time" first,
	// as a key nobody touches does when its wait runs out; -1 for ever.
	touchFails int
	// holdTouch: the ECDH blocks at the touch until the test answers —
	// press (the agreement), giveUp (the key's own timeout: ErrTokenTouch)
	// or pull (the key removed: ErrTokenNoCard) — as the real card call
	// blocks and cannot be interrupted (DESIGN.md §11 trap 23).
	holdTouch bool
	touchCh   chan touchAnswer
}

// touchAnswer is how a held touch ends.
type touchAnswer int

const (
	touchPressed touchAnswer = iota
	touchGaveUp
	touchPulled
)

// press answers the held touch with the touch.
func (f *fakeCard) press() { f.touchCh <- touchPressed }

// giveUp ends the held touch the way the key does on its own.
func (f *fakeCard) giveUp() { f.touchCh <- touchGaveUp }

// pull removes the key during the held touch.
func (f *fakeCard) pull() {
	f.setRemoved(true)
	f.touchCh <- touchPulled
}

// heldTouches is how many ECDH calls have reached the touch and are (or
// were) held there.
func (f *fakeCard) touchCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.touches)
}

// closeCount is how many times the card was closed.
func (f *fakeCard) closeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closes
}

// setRemoved pulls the key, or puts it back.
func (f *fakeCard) setRemoved(gone bool) {
	f.mu.Lock()
	f.removed = gone
	f.mu.Unlock()
}

func newFakeCard(pin string) *fakeCard {
	return &fakeCard{keys: map[Slot]*ecdh.PrivateKey{}, usable: map[Slot]bool{}, pin: pin, retries: 3, touchCh: make(chan touchAnswer, 1)}
}

// addKey puts a fresh P-256 key in slot and returns its public point.
func (f *fakeCard) addKey(slot Slot, usable bool) []byte {
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	f.mu.Lock()
	f.keys[slot] = priv
	f.usable[slot] = usable
	f.mu.Unlock()
	return priv.PublicKey().Bytes()
}

func (f *fakeCard) Serial() uint32 { return 1234567 }

func (f *fakeCard) PINState() (PINStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return PINStatus{}, ErrTokenClosed
	}
	if f.removed {
		return PINStatus{}, ErrTokenNoCard
	}
	return PINStatus{Retries: f.retries, RetriesKnown: true}, nil
}

// VerifyPIN is the card's own VERIFY: the PIN alone, no touch. A right
// one leaves the handle verified, as on the card, so the agreement that
// follows sends none.
func (f *fakeCard) VerifyPIN(pin string) (PINStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return PINStatus{}, ErrTokenClosed
	}
	if f.removed {
		return PINStatus{}, ErrTokenNoCard
	}
	f.verifies++
	if f.retries == 0 {
		return PINStatus{}, ErrTokenPINBlocked
	}
	if pin != f.pin {
		f.retries--
		if f.retries == 0 {
			return PINStatus{}, ErrTokenPINBlocked
		}
		return PINStatus{}, &TokenPINError{Retries: f.retries}
	}
	f.verified = true
	return PINStatus{Verified: true}, nil
}

// verifyCount is how many VERIFYs the card has been sent.
func (f *fakeCard) verifyCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.verifies
}

// ecdhCount is how many agreements the card has been asked for.
func (f *fakeCard) ecdhCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ecdhs
}

func (f *fakeCard) info(slot Slot) KeyInfo {
	k := KeyInfo{Slot: slot, PublicKey: f.keys[slot].PublicKey().Bytes(), Usable: f.usable[slot], Algorithm: "P-256", TouchPolicy: "always", PINPolicy: "once"}
	if !k.Usable {
		k.WhyNot, k.TouchPolicy = "the key can be used without a touch", "never"
	}
	return k
}

func (f *fakeCard) Keys() ([]KeyInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil, ErrTokenClosed
	}
	var out []KeyInfo
	for _, s := range []Slot{0x9d, 0x82, 0x83, 0x84} {
		if _, ok := f.keys[s]; ok {
			out = append(out, f.info(s))
		}
	}
	return out, nil
}

func (f *fakeCard) Inspect(slot Slot) (KeyInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.keys[slot]; !ok {
		return KeyInfo{}, ErrTokenNoKey
	}
	return f.info(slot), nil
}

func (f *fakeCard) FirstEmptySlot() (Slot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range []Slot{0x9d, 0x82, 0x83, 0x84} {
		if _, ok := f.keys[s]; !ok {
			return s, nil
		}
	}
	return 0, ErrTokenFull
}

func (f *fakeCard) ProtectedManagementKey(pin string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if pin != f.pin {
		f.retries--
		if f.retries == 0 {
			return nil, ErrTokenPINBlocked
		}
		return nil, &TokenPINError{Retries: f.retries}
	}
	f.verified = true
	if f.mgmt == nil {
		return nil, ErrTokenNoProtectedKey
	}
	return append([]byte(nil), f.mgmt...), nil
}

func (f *fakeCard) Generate(mgmtKey []byte, o GenerateOptions) (KeyInfo, error) {
	f.mu.Lock()
	if f.mgmt != nil && string(mgmtKey) != string(f.mgmt) {
		f.mu.Unlock()
		return KeyInfo{}, ErrTokenManagementKey
	}
	if _, ok := f.keys[o.Slot]; ok && !o.Overwrite {
		k := f.info(o.Slot)
		f.mu.Unlock()
		return KeyInfo{}, &TokenOccupiedError{Slot: o.Slot, Key: k}
	}
	f.mu.Unlock()
	f.addKey(o.Slot, true)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.info(o.Slot), nil
}

func (f *fakeCard) Token(pub []byte, p Prompter) (keystore.Token, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for s, k := range f.keys {
		if string(k.PublicKey().Bytes()) == string(pub) {
			if !f.usable[s] {
				return nil, ErrTokenNotUsable
			}
			return &fakeToken{card: f, slot: s, p: p}, nil
		}
	}
	return nil, ErrTokenNoKey
}

func (f *fakeCard) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	f.verified = false // the close resets the card
	f.closes++
	if f.removed && f.closeErr == nil {
		// The real Close cannot reset a card that is gone — and reports
		// the card gone, not a failed reset: an unpowered card holds no
		// verified state (DESIGN.md §11 trap 26).
		return ErrTokenNoCard
	}
	return f.closeErr
}

type fakeToken struct {
	card *fakeCard
	slot Slot
	p    Prompter
	n    int
}

func (t *fakeToken) PublicKey() []byte {
	t.card.mu.Lock()
	defer t.card.mu.Unlock()
	return t.card.keys[t.slot].PublicKey().Bytes()
}

func (t *fakeToken) ECDH(epk []byte) ([]byte, error) {
	t.n++
	if t.n > 8 {
		return nil, ErrTokenTooMany
	}
	t.card.mu.Lock()
	t.card.ecdhs++
	closed, verified := t.card.closed, t.card.verified
	st := PINStatus{Retries: t.card.retries, RetriesKnown: true}
	t.card.mu.Unlock()
	if closed {
		return nil, ErrTokenClosed
	}
	if st.Blocked() {
		return nil, ErrTokenPINBlocked
	}
	// PIN once, as on the card: a VERIFY that stands is not asked again
	// on the same handle. asked is what the real Token reports in its
	// touch request: whether this agreement asked for the PIN itself.
	asked := !verified
	if !verified {
		pin, err := t.p.PIN(st)
		if err != nil {
			// As the real Token does: the prompter's error, under a cancel.
			return nil, fmt.Errorf("%w: %w", ErrTokenCancelled, err)
		}
		t.card.mu.Lock()
		t.card.verifies++
		if pin != t.card.pin {
			t.card.retries--
			left := t.card.retries
			t.card.mu.Unlock()
			if left == 0 {
				return nil, ErrTokenPINBlocked
			}
			return nil, &TokenPINError{Retries: left}
		}
		t.card.verified = true
		t.card.mu.Unlock()
	}
	t.card.mu.Lock()
	if t.card.touchFails != 0 {
		if t.card.touchFails > 0 {
			t.card.touchFails--
		}
		t.card.touches = append(t.card.touches, TouchRequest{N: t.n, PINAsked: asked})
		t.card.mu.Unlock()
		t.p.Touch(TouchRequest{N: t.n, PINAsked: asked})
		time.Sleep(300 * time.Millisecond) // the key's own wait, in miniature
		return nil, ErrTokenTouch
	}
	hold := t.card.holdTouch
	t.card.ops++
	t.card.touches = append(t.card.touches, TouchRequest{N: t.n, PINAsked: asked})
	priv := t.card.keys[t.slot]
	t.card.mu.Unlock()
	t.p.Touch(TouchRequest{N: t.n, PINAsked: asked})
	if hold {
		// The key waits, and nothing but the touch, its own timeout or
		// its removal ends the call.
		switch <-t.card.touchCh {
		case touchGaveUp:
			return nil, ErrTokenTouch
		case touchPulled:
			return nil, ErrTokenNoCard
		}
	}
	pub, err := ecdh.P256().NewPublicKey(epk)
	if err != nil {
		return nil, err
	}
	h, err := priv.ECDH(pub)
	if err != nil {
		return nil, err
	}
	t.card.mu.Lock()
	lies := t.card.proofLies
	t.card.mu.Unlock()
	if lies {
		h[0] ^= 1
	}
	return h, nil
}

// harness is one core with fakes over a fresh vault.
type harness struct {
	t        *testing.T
	dir      string
	vault    string
	recovery string // the recovery key's digits
	// entangled is the vault's password when its switch is on (FORMAT.md
	// §6), empty when it is off: what a token unlock is asked for first.
	entangled string
	c         *Core
	clk       *fakeClock
	rec       *recorder
	cards     *fakeCards
}

const testPassword = "correct horse battery staple"

// newHarness makes a vault with a recovery slot and either a password slot
// or, when hwPub is given, a hardware slot (the keystore invariant keeps
// the two apart), and a core over it, locked. The vault's switch is off.
func newHarness(t *testing.T, cards *fakeCards, hwPub []byte) *harness {
	t.Helper()
	return newHarnessEntangled(t, cards, hwPub, "")
}

// newHarnessEntangled is newHarness with the vault's entangled password
// chosen: the switch lives in the slot region header and belongs to the
// vault, not to a slot (FORMAT.md §6, §18.1). An empty password leaves it
// off. A password vault cannot be entangled usefully — the standalone slot
// is never entangled — so it is the hardware harness this is for.
func newHarnessEntangled(t *testing.T, cards *fakeCards, hwPub []byte, password string) *harness {
	t.Helper()
	dir := t.TempDir()
	vault := filepath.Join(dir, "vault.eks")
	rk, err := kdf.NewRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	specs := []keystore.SlotSpec{keystore.RecoverySlot{Key: rk, Label: "Recovery key"}}
	if hwPub != nil {
		specs = append(specs, keystore.HardwareSlot{PublicKey: hwPub, Label: "Test key"})
	} else {
		specs = append(specs, keystore.PasswordSlot{Password: testPassword, Argon2: fast, Label: "Password"})
	}
	opts := keystore.CreateOptions{Slots: specs}
	if password != "" {
		opts.Entangle = &keystore.Entangle{Password: password, Argon2: fast}
	}
	unl, err := keystore.Create(vault, opts)
	if err != nil {
		t.Fatal(err)
	}
	ks := unl.Keystore()
	unl.Close()
	ks.Close()

	h := &harness{t: t, dir: dir, vault: vault, recovery: rk.Digits(), entangled: password, clk: newFakeClock(), rec: &recorder{}, cards: cards}
	var cs Cards
	if cards != nil {
		cs = cards
	}
	c, err := New(Deps{Cards: cs, Events: h.rec, Clock: h.clk, DataDir: filepath.Join(dir, "data"), Log: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Start(); err != nil {
		t.Fatal(err)
	}
	h.c = c
	t.Cleanup(c.Close)
	if e := c.OpenVaultFile(vault, "Test vault"); e != nil {
		t.Fatalf("open vault: %v", e)
	}
	return h
}

// unlockWithPassword runs the password ceremony to Unlocked.
func (h *harness) unlockWithPassword() {
	h.t.Helper()
	h.rec.reset()
	if e := h.c.BeginUnlock(MethodPassword); e != nil {
		h.t.Fatalf("begin: %v", e)
	}
	st := h.rec.waitCeremony(h.t, StepPassword, true)
	if e := h.c.SubmitSecret("password", st.PromptID, testPassword); e != nil {
		h.t.Fatalf("submit: %v", e)
	}
	h.rec.waitCeremony(h.t, StepDone, false)
	h.rec.waitState(h.t, StateUnlocked)
}

// unlockWithToken unlocks through the fake card's PIN and touch, answering
// the vault's password after the PIN when its switch is on.
func (h *harness) unlockWithToken() {
	h.t.Helper()
	h.rec.reset()
	if e := h.c.BeginUnlock(MethodToken); e != nil {
		h.t.Fatalf("begin: %v", e)
	}
	h.answerTokenUnlock("123456")
	h.rec.waitCeremony(h.t, StepDone, false)
	h.rec.waitState(h.t, StateUnlocked)
}

// answerTokenUnlock answers the unlock half of any ceremony that opens a
// hardware vault, in the one order of APP.md §2.2: the key's PIN first,
// verified at the card, then the vault's password when its switch is on
// (FORMAT.md §6).
func (h *harness) answerTokenUnlock(pin string) {
	h.t.Helper()
	p := h.rec.waitCeremony(h.t, StepPIN, true)
	if e := h.c.SubmitSecret("pin", p.PromptID, pin); e != nil {
		h.t.Fatalf("submit the PIN: %v", e)
	}
	if h.entangled != "" {
		pw := h.rec.waitCeremony(h.t, StepPassword, true)
		if pw.Choose {
			h.t.Fatalf("the vault's own password marked choose: %+v", pw)
		}
		if e := h.c.SubmitSecret("password", pw.PromptID, h.entangled); e != nil {
			h.t.Fatalf("submit the vault's password: %v", e)
		}
	}
}

func (h *harness) status() VaultStatus { return h.c.Status() }

// attempt is the agreement the running ceremony waits on, or the pending
// touch when none does: the handle a test reads the kept touch through.
func (h *harness) attempt() *attempt {
	h.c.mu.Lock()
	defer h.c.mu.Unlock()
	if h.c.cer != nil && h.c.cer.att != nil {
		return h.c.cer.att
	}
	return h.c.pending
}

func isCode(e *Error, c Code) bool { return e != nil && e.Code == c }

var _ = errors.Is

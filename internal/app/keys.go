package app

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dreamxwarden01/enfold/internal/format"
	"github.com/dreamxwarden01/enfold/internal/kdf"
	"github.com/dreamxwarden01/enfold/internal/keystore"
)

// defaultArgon2 is DESIGN §6's baseline: memory is the lever, t clamped
// low, p fixed at 4 and stored with the slot. 512 MiB.
var defaultArgon2 = kdf.Argon2Params{MemKiB: 512 * 1024, Time: 1, Threads: 4}

// Slots lists the ways to unlock, from the cached facts.
func (c *Core) Slots() []SlotView {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]SlotView, 0, len(c.vault.slots))
	for _, s := range c.vault.slots {
		v := slotView(s)
		v.Removable = c.vault.removable[s.RecipientID]
		out = append(out, v)
	}
	return out
}

// EntangledState is the Keys page's row for the vault's password (APP.md
// §13): the header's switch, and whether §6.4 would let it be turned on —
// the same predicate that greys Remove, evaluated with no ceremony and no
// VMK. Both come from the facts cached at lock, since the handle is closed
// while Locked; CanEnable is false there, where no ceremony can start.
func (c *Core) EntangledState() EntangledState {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := EntangledState{On: c.vault.entangled}
	if c.vault.state != StateUnlocked {
		return st
	}
	st.CanEnable = c.vault.canEnable
	if !st.CanEnable {
		st.Reason = CodeInvariant
	}
	return st
}

// SetEntangled turns the vault's password on or off (APP.md §13): a
// ceremony for the VMK by any way in, then one offline write that draws a
// fresh entangle_salt, re-wraps every active hardware slot from the kept
// K_P and moves the header's switch (FORMAT.md §6, §18.1). Turning it on
// asks for the new password — the page confirms it in two fields under the
// one prompt — and is refused for the invariant before the ceremony starts
// and before a password is typed; turning it off asks for nothing and is
// never refused. The old password is not asked in either direction: whoever
// reaches the VMK can already enrol a way in of their own.
func (c *Core) SetEntangled(on bool) *Error {
	if on {
		c.mu.Lock()
		unlocked, can := c.vault.state == StateUnlocked, c.vault.canEnable
		frozen := c.vault.tampered != nil
		c.mu.Unlock()
		// R25's freeze is read here as well as in beginWithVMK: a
		// pre-check that answered first would hide the reason behind an
		// invariant the user cannot act on (APP.md §13).
		if unlocked && frozen {
			return coded(CodeVaultTampered)
		}
		if unlocked && !can {
			return coded(CodeInvariant)
		}
	}
	return c.beginMutation("entangle", func(cer *ceremony, unl *keystore.Unlocked) error {
		// The key that unlocked is not needed any more: K_P is reached
		// through the VMK, so it is released before any prompt.
		cer.releaseCard()
		var e *keystore.Entangle
		if on {
			pw, err := cer.askNew("password", StepPassword)
			if err != nil {
				return err
			}
			e = &keystore.Entangle{Password: pw, Argon2: defaultArgon2}
		}
		if err := cer.check(); err != nil {
			return err
		}
		if err := unl.SetEntangled(on, e); err != nil {
			return err
		}
		c.afterMutation()
		return nil
	})
}

// ChangeEntangledPassword replaces the vault's password (APP.md §13): the
// same one-write shape as SetEntangled, and the old password is never a
// field. Refused before any prompt while the switch is off, so a salt
// redraw cannot happen by the wrong button.
func (c *Core) ChangeEntangledPassword() *Error {
	c.mu.Lock()
	on, unlocked := c.vault.entangled, c.vault.state == StateUnlocked
	frozen := c.vault.tampered != nil
	c.mu.Unlock()
	// The freeze answers before the switch-off refusal, for the same
	// reason as in SetEntangled: the reason must be the one to act on.
	if unlocked && frozen {
		return coded(CodeVaultTampered)
	}
	if unlocked && !on {
		return coded(CodeParams)
	}
	return c.beginMutation("entangle", func(cer *ceremony, unl *keystore.Unlocked) error {
		cer.releaseCard()
		pw, err := cer.askNew("password", StepPassword)
		if err != nil {
			return err
		}
		if err := cer.check(); err != nil {
			return err
		}
		if err := unl.ChangeEntangledPassword(keystore.Entangle{Password: pw, Argon2: defaultArgon2}); err != nil {
			return err
		}
		c.afterMutation()
		return nil
	})
}

// EnrollKind is which kind of slot an enrollment adds.
type EnrollKind string

const (
	EnrollToken    EnrollKind = "token"
	EnrollPassword EnrollKind = "password"
	EnrollRecovery EnrollKind = "recovery"
)

// EnrollOptions describes the slot to add. Entangle is the vault's switch
// and belongs to the moments a vault's entanglement is chosen rather than
// inherited — a create, an import's adopted backup, FinishSetup — never to
// BeginEnroll, where an enrolled key takes the vault's setting (APP.md §13).
type EnrollOptions struct {
	Kind     EnrollKind
	Label    string
	Entangle bool
}

// recipientSet is the ids a handle's slots hold right now, so that the slot
// a mutation added can be told from the ones that were there.
func recipientSet(slots []keystore.SlotInfo) map[[16]byte]bool {
	m := make(map[[16]byte]bool, len(slots))
	for _, s := range slots {
		m[s.RecipientID] = true
	}
	return m
}

// newRecoveryID is the ID (FORMAT.md §18.4) of the recovery slot in slots
// that before did not hold.
func newRecoveryID(slots []keystore.SlotInfo, before map[[16]byte]bool) string {
	for _, s := range slots {
		if !before[s.RecipientID] && s.Type == format.SlotRecovery {
			return recoveryID(s.RecipientID)
		}
	}
	return ""
}

// beginMutation is a slot change that needs the VMK: the ceremony re-runs to
// obtain an Unlocked on the open handle, fn uses it, and it is closed.
func (c *Core) beginMutation(kind string, fn func(cer *ceremony, unl *keystore.Unlocked) error) *Error {
	return c.beginWithVMK(kind, false, fn)
}

// beginWithVMK runs fn with an Unlocked recovered through a protector. It
// holds the handle like a slot change whether or not fn writes the file:
// keystore.Unlock is not a read of the shared handle, so a registry write
// waits for it and it waits for one. The reveal of APP.md §3 Keys is the
// reading that allowTampered admits.
func (c *Core) beginWithVMK(kind string, allowTampered bool, fn func(cer *ceremony, unl *keystore.Unlocked) error) *Error {
	c.mu.Lock()
	v := &c.vault
	if v.state != StateUnlocked {
		c.mu.Unlock()
		return coded(CodeNeedsUnlock)
	}
	if !allowTampered && v.tampered != nil {
		c.mu.Unlock()
		return coded(CodeVaultTampered)
	}
	if c.cer != nil {
		c.mu.Unlock()
		return coded(CodeCeremonyRunning)
	}
	// An operation about to write the registry would race the ceremony's
	// own use of the one handle: it goes first.
	for _, o := range c.ops {
		if !o.finished && writesRegistry(o.kind) {
			c.mu.Unlock()
			return coded(CodeOpRunning)
		}
	}
	cer := c.newCeremonyLocked(kind)
	cer.mutation = true
	c.mu.Unlock()
	c.emitState()
	go cer.run(func(ctx context.Context) error {
		unl, card, err := cer.acquireUnlocked()
		if err != nil {
			return err
		}
		c.mu.Lock()
		cer.card = card
		c.mu.Unlock()
		defer cer.releaseCard()
		defer unl.Close()
		if err := cer.check(); err != nil {
			return err // a lock landed while the VMK was being recovered
		}
		return fn(cer, unl)
	})
	return nil
}

// writesRegistry: the operation kinds that end with a registry receipt.
func writesRegistry(kind string) bool {
	switch kind {
	case "save", "verify", "compact", "rotate":
		return true
	}
	return false
}

// acquireUnlocked runs the unlock flow against the open handle and keeps
// the Unlocked (the VMK) for the caller. The method is the one that
// opened the vault's kind of slot: a token when a usable one is enrolled,
// else a password; the recovery key only when the vault has nothing else
// — an adopted backup being set up, or a vault whose one token is stale.
func (cer *ceremony) acquireUnlocked() (*keystore.Unlocked, Card, error) {
	c := cer.c
	c.mu.Lock()
	ks := c.vault.ks
	slots := c.vault.slots
	entangled := c.vault.entangled
	var hasToken, hasPassword bool
	for _, s := range slots {
		if s.PublicKey != nil {
			hasToken = true
		}
		if s.Type == format.SlotStandalonePassword {
			hasPassword = true
		}
	}
	c.mu.Unlock()
	if ks == nil {
		return nil, nil, coded(CodeNeedsUnlock)
	}
	if !hasToken && !hasPassword {
		// A pending touch of a cancelled slot change is inside
		// keystore.Unlock on this very handle: it ends first (§2.2). The
		// token flow adopts it instead (tokenCredential).
		if err := cer.settlePending(); err != nil {
			return nil, nil, err
		}
		cred, _, _, err := cer.credential(MethodRecovery, nil, false)
		if err != nil {
			return nil, nil, err
		}
		unl, err := cer.unlockFile(ks, cred, nil)
		if err != nil {
			return nil, nil, err
		}
		return unl, nil, nil
	}
	if hasToken && c.deps.Cards != nil {
		// The way in, recorded before the first prompt: on an entangled
		// vault this branch asks for the vault's password too — after the
		// PIN (§2.2) — so nothing else distinguishes it from a
		// standalone-password way in (§5.1).
		cer.setMethod(MethodToken)
		for {
			h, card, slot, err := cer.tokenCredential(slots, entangled)
			if err != nil {
				return nil, nil, err
			}
			c.mu.Lock()
			cer.unlockPub, cer.unlockLabel = slot.PublicKey, slot.Label
			c.mu.Unlock()
			if cer.adopted() == nil {
				// An adopted attempt is at its touch already (unlockFile).
				cer.set(func(s *CeremonyState) { s.Step = StepDeriving })
			}
			unl, err := cer.agree(ks, false, h)
			if err == nil {
				return unl, card, nil
			}
			if errors.Is(err, errDisowned) {
				return nil, nil, err // the pending touch holds the card now
			}
			if keyGone(err) {
				// Pulled during the PIN or the touch: back to waiting.
				cer.unhold(card)
				card.Close()
				if err := cer.away(err); err != nil {
					return nil, nil, err
				}
				continue
			}
			cer.closeCard(card) // released before any park
			return nil, nil, err
		}
	}
	cer.setMethod(MethodPassword)
	pw, err := cer.ask("password", StepPassword, PINStatus{})
	if err != nil {
		return nil, nil, err
	}
	cer.set(func(s *CeremonyState) { s.Step = StepDeriving })
	if err := cer.settlePending(); err != nil { // the handle a pending touch is inside
		return nil, nil, err
	}
	unl, err := cer.unlockWith(ks, keystore.PasswordCredential{Password: pw})
	if err != nil {
		return nil, nil, err
	}
	return unl, nil, nil
}

// afterMutation refreshes the cached facts and tells the frontend.
func (c *Core) afterMutation() {
	c.mu.Lock()
	if c.vault.ks != nil {
		c.cacheFactsLocked(c.vault.ks)
	}
	c.dropPendingLocked() // a slot change: whatever was pending is not adopted
	c.mu.Unlock()
	c.emitState()
}

// BeginEnroll adds a slot after a ceremony for the VMK; for a token, the
// token flow follows (APP.md §3, Keys).
func (c *Core) BeginEnroll(o EnrollOptions) *Error {
	if o.Label == "" && o.Kind == EnrollRecovery {
		return coded(CodeParams) // a key's label defaults to its serial, a password's to "Password"
	}
	switch o.Kind {
	case EnrollToken:
		if c.deps.Cards == nil {
			return coded(CodeTokenNoService)
		}
	case EnrollPassword, EnrollRecovery:
	default:
		return coded(CodeParams)
	}
	return c.beginMutation("enroll", func(cer *ceremony, unl *keystore.Unlocked) error {
		var spec keystore.SlotSpec
		switch o.Kind {
		case EnrollPassword:
			// The key that unlocked is not needed any more: released before
			// the prompt, so that a key pulled meanwhile is no event.
			cer.releaseCard()
			pw, err := cer.askNew("password", StepPassword)
			if err != nil {
				return err
			}
			spec = keystore.PasswordSlot{Password: pw, Argon2: defaultArgon2, Label: mustString(o.Label, "Password")}
		case EnrollRecovery:
			rk, err := kdf.NewRecoveryKey()
			if err != nil {
				return err
			}
			spec = keystore.RecoverySlot{Key: rk, Label: o.Label}
			if err := cer.check(); err != nil {
				return err
			}
			before := recipientSet(unl.Keystore().Slots())
			if err := unl.AddSlot(spec); err != nil {
				return err
			}
			// Shown over the one-time channel with its ID, and kept once
			// more in the vault (FORMAT R38, §18.4).
			id := newRecoveryID(unl.Keystore().Slots(), before)
			url, err := cer.mint(rk.Digits(), o.Label, id)
			if err != nil {
				return err
			}
			cer.set(func(s *CeremonyState) { s.Step, s.SlotLabel, s.RecoveryID = StepRecovery, url, id })
			c.afterMutation()
			return nil
		case EnrollToken:
			// The key that unlocked is released first — a verified card is
			// not held across a prompt. An enrolled key inherits the vault's
			// switch and is wrapped from the kept K_P, so no password is
			// asked here (APP.md §13).
			unlockPub := cer.releaseUnlocking()
			hs := keystore.HardwareSlot{Label: o.Label}
			pub, serial, err := cer.enrollToken(unlockPub, o.Label)
			if err != nil {
				return err
			}
			hs.PublicKey, hs.Label = pub, mustString(o.Label, keyName(serial))
			spec = hs
		}
		if err := cer.check(); err != nil {
			return err
		}
		if err := unl.AddSlot(spec); err != nil {
			return err
		}
		c.afterMutation()
		return nil
	})
}

// releaseUnlocking releases the card that unlocked, when the ceremony
// holds one, and returns the public key it unlocked with (nil when none):
// the key enrollToken waits to see leave the reader. The card is held
// exclusively — opening the reader again while it is would only report
// it busy — and a verified card must not stay held into a prompt, where
// the user may pull it and the close would then report a reset that
// never happened.
func (cer *ceremony) releaseUnlocking() []byte {
	cer.c.mu.Lock()
	pub := cer.unlockPub
	if cer.card == nil {
		pub = nil
	}
	cer.c.mu.Unlock()
	cer.releaseCard()
	return pub
}

// enrollToken is the token half of an enrollment, after releaseUnlocking:
// wait for the key that unlocked (unlockPub, when one did) to be out of
// the reader and the key to enroll inserted, then reuse a usable key in
// 9d or generate one in the first empty slot. Waiting for the unlocking
// key to leave is what keeps it from being re-opened and enrolled twice by
// accident.
func (cer *ceremony) enrollToken(unlockPub []byte, label string) ([]byte, uint32, error) {
	cer.c.mu.Lock()
	remove := cer.unlockLabel
	cer.c.mu.Unlock()
	if unlockPub == nil {
		remove = ""
	}
	cer.set(func(s *CeremonyState) {
		s.Step, s.RemoveLabel, s.InsertLabel = StepSwapKey, remove, "the key to enroll"
	})
	if unlockPub != nil {
		if err := cer.waitForOtherKey(unlockPub); err != nil {
			return nil, 0, err
		}
		cer.set(func(s *CeremonyState) { s.Step, s.RemoveLabel = StepSwapKey, "" })
	}
	deadline := cer.c.deps.Clock.AfterFunc(promptWait, func() { cer.cancelWith("wait_deadline") })
	defer deadline.Stop()
	delay := readerPoll
	for {
		pub, serial, err := cer.enrollOnce(label)
		if err == nil || !keyGone(err) {
			return pub, serial, err
		}
		// Pulled during the PIN, the management key or the proof: waited
		// for again.
		if err := cer.awayAndWait(err, delay); err != nil {
			return nil, 0, err
		}
		delay = min(delay*2, awayRetryMax)
	}
}

// keyName is a token's label when the user gave none: its serial number,
// which tells one YubiKey from another.
func keyName(serial uint32) string { return fmt.Sprintf("YubiKey %d", serial) }

// enrollOnce is one attempt at the key to enrol: wait for it, open it,
// reuse or generate, and have it prove itself. The serial comes back with
// the key, to name it.
func (cer *ceremony) enrollOnce(label string) ([]byte, uint32, error) {
	reader, err := cer.waitForOneReader()
	if err != nil {
		return nil, 0, err
	}
	card, err := cer.openCard(reader)
	if err != nil {
		return nil, 0, err
	}
	serial := card.Serial()
	pub, err := cer.enrollOn(card, mustString(label, keyName(serial)))
	if errors.Is(err, errDisowned) {
		return nil, 0, err // the pending touch holds the card now
	}
	if keyGone(err) {
		// The key is not there to reset: a close that reported it would
		// warn of a verified state the pull took with it.
		cer.unhold(card)
		card.Close()
		return nil, 0, err
	}
	cer.closeCard(card)
	return pub, serial, err
}

// enrollOn is enrollOnce's work on the open card: reuse a usable key in
// 9d, else generate one in the first empty slot — and then the key
// proves itself, whichever it was (proveKey): enrolling a key is a
// ceremony the user performs, never something a key in the reader
// undergoes by itself.
func (cer *ceremony) enrollOn(card Card, label string) ([]byte, error) {
	cer.set(func(s *CeremonyState) { s.SlotLabel = label }) // the chip names this key, not the one that unlocked
	pub, err := cer.keyToEnroll(card)
	if err != nil {
		return nil, err
	}
	if err := cer.proveKey(card, pub, label); err != nil {
		return nil, err
	}
	return pub, nil
}

// proveKey has the key prove itself before the vault depends on it (APP.md
// §3 Keys): its PIN and its touch, and an agreement checked against its
// public key — so a key that does not work is never enrolled, and a key
// is never enrolled without the hand that holds it.
func (cer *ceremony) proveKey(card Card, pub []byte, label string) error {
	peer, err := ecdh.P256().NewPublicKey(pub)
	if err != nil {
		return fmt.Errorf("%w: the key's public key: %v", ErrTokenUnsupported, err)
	}
	eph, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	want, err := eph.ECDH(peer)
	if err != nil {
		return err
	}
	p := &ceremonyPrompter{cer: cer, label: label}
	tok, err := card.Token(pub, p)
	if err != nil {
		kdf.Zero(want)
		if errors.Is(err, ErrTokenNotUsable) {
			return &parkAt{StepFailed, CodeTokenNotUsable}
		}
		return err
	}
	// The proof runs as an attempt, like an unlock's agreement (APP.md
	// §2.2): a cancel ends the ceremony at once and the call goes on as
	// the pending touch. Its purpose is its own ephemeral key, so nothing
	// ever adopts it; the attempt owns the expected point and zeroes it.
	epk := eph.PublicKey().Bytes()
	a := &attempt{slot: keystore.SlotInfo{Label: label}, card: card, hc: keystore.HardwareCredential{Token: tok}, prompter: p}
	a.op = func() (*keystore.Unlocked, error) {
		got, err := tok.ECDH(epk)
		if err != nil {
			return nil, err
		}
		same := subtle.ConstantTimeCompare(got, want) == 1
		kdf.Zero(got)
		if !same {
			return nil, errProof
		}
		return nil, nil
	}
	a.cleanup = func() { kdf.Zero(want) }
	cer.startAttempt(a)
	_, err = a.await(cer)
	switch {
	case err == nil:
		kdf.Zero(want)
		cer.c.log("ceremony %s: the key proved itself", cer.kind)
		return nil
	case errors.Is(err, errDisowned):
		return err // want is the attempt's to zero
	case errors.Is(err, errProof):
		kdf.Zero(want)
		cer.c.log("ceremony %s: the key's agreement does not match its public key", cer.kind)
		return &parkAt{StepFailed, CodeTokenProof}
	}
	kdf.Zero(want)
	switch {
	case errors.Is(err, ErrTokenPINBlocked):
		return &parkAt{StepBlocked, CodeTokenPINBlocked}
	case errors.Is(err, ErrTokenTooMany):
		return &parkAt{StepFailed, CodeTokenTooMany}
	}
	return err
}

// keyToEnroll is the key the card will be enrolled with: a usable key
// already in 9d, else one generated in the first empty slot.
func (cer *ceremony) keyToEnroll(card Card) ([]byte, error) {
	keys, err := card.Keys()
	if err != nil {
		cer.c.log("ceremony %s: reading the keys: %v", cer.kind, err)
		return nil, err
	}
	cer.c.log("ceremony %s: token holds %s", cer.kind, describeKeys(keys))
	for _, k := range keys {
		if k.Slot == 0x9d && k.Usable {
			cer.c.log("ceremony %s: reusing the key in 9d", cer.kind)
			return k.PublicKey, nil
		}
	}
	slot, err := card.FirstEmptySlot()
	if err != nil {
		cer.c.log("ceremony %s: no empty slot: %v", cer.kind, err)
		if errors.Is(err, ErrTokenFull) {
			return nil, &parkAt{StepFailed, CodeTokenFull} // parked after the card is released
		}
		return nil, err
	}
	cer.c.log("ceremony %s: generating in %02x", cer.kind, byte(slot))
	// The management key: the PIN-protected one first, else typed as hex.
	// A refused PIN is said and asked again, in place; only a blocked PIN
	// parks.
	var mgmt []byte
	var note Code
	for {
		st, err := card.PINState()
		if err != nil {
			cer.c.log("ceremony %s: PIN state: %v", cer.kind, err)
			return nil, err
		}
		if st.Blocked() {
			return nil, &parkAt{StepBlocked, CodeTokenPINBlocked}
		}
		pin, err := cer.askNote("pin", StepPIN, st, note)
		if err != nil {
			return nil, err
		}
		mgmt, err = card.ProtectedManagementKey(pin)
		if err == nil {
			break
		}
		var pe *TokenPINError
		switch {
		case errors.As(err, &pe):
			note = CodeTokenPIN
			continue
		case errors.Is(err, ErrTokenPINBlocked):
			return nil, &parkAt{StepBlocked, CodeTokenPINBlocked}
		case errors.Is(err, ErrTokenNoProtectedKey):
			cer.c.log("ceremony %s: no PIN-protected management key; asking for it", cer.kind)
			hexKey, aerr := cer.ask("mgmtkey", StepManagementKey, PINStatus{})
			if aerr != nil {
				return nil, aerr
			}
			mgmt, err = decodeHexKey(hexKey)
			if err != nil {
				return nil, &parkAt{StepFailed, CodeTokenMgmtKey}
			}
		default:
			cer.c.log("ceremony %s: management key after the PIN: %v", cer.kind, err)
			return nil, err
		}
		break
	}
	defer kdf.Zero(mgmt)
	cer.set(func(s *CeremonyState) { s.Step = StepDeriving })
	info, err := card.Generate(mgmt, GenerateOptions{Slot: slot})
	if err != nil {
		cer.c.log("ceremony %s: generate in %02x: %v", cer.kind, byte(slot), err)
		return nil, err
	}
	cer.c.log("ceremony %s: generated in %02x", cer.kind, byte(slot))
	return info.PublicKey, nil
}

// RevealRecoveryKey shows a recovery slot's key again (APP.md §3 Keys):
// the VMK is recovered through a protector — never the recovery key —
// and the slot's escrow record (FORMAT R38) opened and checked against
// the slot; the digits go to the page over the one-time URL, as at
// creation, and stay behind its handle for a save. What can be refused
// before any prompt is: no such recovery slot, no record for it, no
// protector to ask. It writes nothing, and runs on a tampered vault.
func (c *Core) RevealRecoveryKey(recipientID string) *Error {
	rid, ok := parseID(recipientID)
	if !ok {
		return coded(CodeParams)
	}
	c.mu.Lock()
	v := &c.vault
	if v.state != StateUnlocked {
		c.mu.Unlock()
		return coded(CodeNeedsUnlock)
	}
	var label string
	found, protector := false, false
	for _, s := range v.slots {
		if s.RecipientID == rid && s.Type == format.SlotRecovery {
			found, label = true, s.Label
		}
		if s.PublicKey != nil || s.Type == format.SlotStandalonePassword {
			protector = true
		}
	}
	c.mu.Unlock()
	switch {
	case !found:
		return coded(CodeSlotNotFound)
	case !protector:
		return coded(CodeSetupNeeded)
	}
	return c.beginWithVMK("reveal", true, func(cer *ceremony, unl *keystore.Unlocked) error {
		cer.releaseCard() // nothing further needs the key
		if err := cer.check(); err != nil {
			return err
		}
		rk, err := unl.RecoveryKey(rid)
		if err != nil {
			if errors.Is(err, keystore.ErrEscrowMissing) {
				// Bookkeeping, not damage: there is nothing to repair, since
				// the key exists only on paper. Said after the unlock, which
				// is never refused over it (FORMAT R38).
				return &parkAt{StepFailed, CodeEscrowMissing}
			}
			if errors.Is(err, format.ErrInvalid) {
				// The record does not open: the vault's problem, not an archive's.
				return &parkAt{StepFailed, CodeVaultInvalid}
			}
			return err
		}
		id := recoveryID(rid)
		url, err := cer.mint(rk.Digits(), label, id)
		kdf.Zero(rk[:])
		if err != nil {
			return err
		}
		cer.set(func(s *CeremonyState) { s.Step, s.SlotLabel, s.RecoveryID = StepRecovery, url, id })
		return nil
	})
}

// SaveRecoveryKey writes the recovery key behind handle — the token of the
// one-time URL a reveal minted — to path, a text file the user chose, so
// that the digits never ride a bound call (APP.md §1). Not into the data
// folder or beneath it, not into the folder of a vault kept elsewhere, not
// under a staging name (vault.recovery_place); a handle that is unknown,
// dropped or expired is ceremony.stale_prompt. An existing regular file is
// replaced — the Save dialog asked — and anything else at the path
// refused; the file is synced before this returns. The folder the user
// chose is the file's protection: the mode is asked for where it means
// something.
func (c *Core) SaveRecoveryKey(handle, path string) *Error {
	if path == "" || !filepath.IsAbs(path) {
		return coded(CodeParams)
	}
	c.mu.Lock()
	name, vaultPath := c.vault.displayName, c.vault.path
	c.mu.Unlock()
	if reservedName(path) || insideDir(c.deps.DataDir, path) || (vaultPath != "" && samePath(filepath.Dir(path), filepath.Dir(vaultPath))) {
		return coded(CodeRecoveryPlace)
	}
	if fi, err := os.Lstat(path); err == nil && !fi.Mode().IsRegular() {
		return coded(CodeRecoveryPlace)
	}
	digits, label, id, ok := c.preview.secretValue(handle)
	if !ok {
		return coded(CodeStalePrompt)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return c.fail("save recovery key", err)
	}
	_, err = f.WriteString(recoveryKeyText(name, label, id, c.now(), digits))
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return c.fail("save recovery key", err)
	}
	c.log("recovery key saved to %s", path)
	return nil
}

// insideDir reports whether path is dir or lies beneath it.
func insideDir(dir, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel == "." || !(rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// DropRecoveryKey ends a reveal's handle: the dialog closed.
func (c *Core) DropRecoveryKey(handle string) *Error {
	c.preview.dropSecret(handle)
	return nil
}

// recoveryKeyText is the saved file: the digits, what they are for, and
// what they are not. Windows line endings, for Notepad.
func recoveryKeyText(name, label, id string, at time.Time, digits string) string {
	lines := []string{
		"Enfold recovery key",
		"Vault: " + name,
		"Way in: " + label,
		"Key ID: " + id, // FORMAT.md §18.4: what tells one sheet from another
		"Saved: " + at.Format("2006-01-02 15:04"),
		"",
		digits,
		"",
		"This key opens the vault without a YubiKey or a password: anyone who has it",
		"can open every archive the vault holds the keys to. Keep it secret, and keep",
		"it where you can reach it when you need it.",
		"",
		"It does not replace the vault file itself: without that file, no key opens",
		"the archives. Keep a backup of the vault as well (Keys > Export a backup).",
		"",
	}
	return strings.Join(lines, "\r\n")
}

// RemoveSlot removes a way in after a ceremony; the invariant may refuse.
func (c *Core) RemoveSlot(recipientID string) *Error {
	rid, ok := parseID(recipientID)
	if !ok {
		return coded(CodeParams)
	}
	return c.beginMutation("remove", func(cer *ceremony, unl *keystore.Unlocked) error {
		if err := cer.check(); err != nil {
			return err
		}
		if err := unl.RemoveSlot(rid); err != nil {
			return err
		}
		c.afterMutation()
		return nil
	})
}

// RotateNow rotates the VMK and re-derives the session (APP.md §2.1).
func (c *Core) RotateNow() *Error {
	return c.beginMutation("rotate", func(cer *ceremony, unl *keystore.Unlocked) error {
		return c.rotateWith(cer, unl)
	})
}

func (c *Core) rotateWith(cer *ceremony, unl *keystore.Unlocked) error {
	if err := cer.check(); err != nil {
		return err
	}
	if err := unl.Rotate(); err != nil {
		return err // run() enters Broken on an indeterminate outcome
	}
	next, err := unl.Session()
	if err != nil {
		return err
	}
	c.mu.Lock()
	v := &c.vault
	if v.state != StateUnlocked || v.ks != unl.Keystore() {
		// A lock landed meanwhile: the rotation is on disk, but the new
		// keys are not installed behind a Locked state.
		c.mu.Unlock()
		next.Lock()
		return coded(CodeNeedsUnlock)
	}
	old := v.sess
	v.sess = next
	if old != nil {
		old.Lock()
	}
	c.cacheFactsLocked(v.ks)
	c.armTimersLocked()
	c.mu.Unlock()
	c.emitState()
	return nil
}

// ExportBackup writes a keystore file of registry plus recovery slots.
// Not into the data folder, which is Enfold's to sweep.
func (c *Core) ExportBackup(path string) *Error {
	if reservedName(path) || samePath(filepath.Dir(path), c.deps.DataDir) {
		return coded(CodeParams)
	}
	return c.beginMutation("export", func(cer *ceremony, unl *keystore.Unlocked) error {
		if err := cer.check(); err != nil {
			return err
		}
		if err := unl.Export(path); err != nil {
			return err
		}
		c.recordExport()
		return nil
	})
}

// recordExport stamps the settings file with this vault's last backup
// (APP.md §13): a local, unauthenticated convenience that words the
// confirmations of Forget and Delete and pre-selects the rotate dialog's
// backup checkbox. It gates nothing cryptographic, so a failed write is
// logged and nothing else.
func (c *Core) recordExport() {
	c.mu.Lock()
	c.settings.LastExport = &lastExport{VaultID: hexID(c.vault.vaultID), At: c.now().Unix()}
	file := c.settings
	c.mu.Unlock()
	if err := saveSettings(c.deps.DataDir, file); err != nil {
		c.log("settings: %v", err)
	}
}

// LastExportAt is when a backup of the vault kept here was last written,
// in Unix seconds, and 0 for never (APP.md §13). The four checks are made
// here rather than on the page: absent, zero, another vault's id, or a
// stamp ahead of this clock all read as never, so no page ever holds
// another vault's id or compares clocks for a value that words a
// destructive confirmation.
func (c *Core) LastExportAt() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	le := c.settings.LastExport
	switch {
	case le == nil || le.At <= 0:
		return 0
	case c.vault.state == StateNone || le.VaultID != hexID(c.vault.vaultID):
		return 0
	case le.At > c.now().Unix():
		return 0
	}
	return le.At
}

// CreateVault makes a new keystore with a recovery slot and a first slot
// of the given kind, both needed for the invariant. The recovery key is
// handed to the frontend over the one-time channel, as the URL in the
// ceremony state — and kept once more in the vault, to be shown again
// from the Keys page (FORMAT R38). path is where the vault lives: empty
// for the one place (APP.md §2.1), else a vault kept elsewhere. The
// keystore is built as the incoming file beside its destination and
// installed only after the ceremony's latch and cancel are checked — a
// create cut short leaves nothing behind — so every create ends Locked.
// With a vault kept there is no second one (vault.kept); the one create
// with a vault configured is the rebuild over the damaged file at its
// own place (§2.1); anything already at the chosen place needs replace.
func (c *Core) CreateVault(path, displayName string, first EnrollOptions, replace bool) *Error {
	if path == "" {
		path = c.defaultVaultPath()
	}
	if reservedName(path) || first.Kind != EnrollToken && first.Kind != EnrollPassword {
		return coded(CodeParams)
	}
	if first.Kind == EnrollToken && c.deps.Cards == nil {
		return coded(CodeTokenNoService)
	}
	c.mu.Lock()
	if e := c.importGateLocked(false); e != nil {
		c.mu.Unlock()
		return e
	}
	// One vault (APP.md §2.1): with one kept, a second is never made; with
	// one damaged, the rebuild is at its own place and nowhere else.
	if c.vault.state != StateNone || (c.vault.missing != "" && c.vault.damaged && !samePath(path, c.vault.missing)) {
		c.mu.Unlock()
		return coded(CodeVaultKept)
	}
	if len(c.archives) > 0 {
		// A save from an open archive would land in the wrong registry.
		c.mu.Unlock()
		return coded(CodeArchivesOpen)
	}
	c.mu.Unlock()
	if !replace {
		// Anything at the destination — a damaged vault, a file, something
		// that cannot even be looked at — is replaced only knowingly.
		if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
			return coded(CodeVaultExists)
		}
	}
	c.mu.Lock()
	if e := c.importGateLocked(false); e != nil {
		c.mu.Unlock()
		return e
	}
	cer := c.newCeremonyLocked("create")
	cer.commits = true // installs at the vault's place: shutdown waits for it
	c.vault.state = StateUnlocking
	// The previous vault's path and facts stand until the new one exists.
	c.mu.Unlock()
	c.emitState()
	staged := filepath.Join(filepath.Dir(path), stagingName(incomingPrefix)) // beside its destination: one rename
	go cer.run(func(ctx context.Context) error {
		installed := false
		defer func() {
			if !installed {
				os.Remove(staged)
			}
		}()
		rk, err := kdf.NewRecoveryKey()
		if err != nil {
			return err
		}
		spec, ent, err := cer.firstWayIn(first)
		if err != nil {
			return err
		}
		if err := cer.check(); err != nil {
			return err
		}
		cer.set(func(s *CeremonyState) { s.Step = StepDeriving })
		unl, err := keystore.Create(staged, keystore.CreateOptions{
			Slots:    []keystore.SlotSpec{keystore.RecoverySlot{Key: rk, Label: "Recovery key"}, spec},
			Entangle: ent, // the one moment a vault's switch is chosen (APP.md §13)
		})
		if err != nil {
			return err
		}
		ks := unl.Keystore()
		rid := newRecoveryID(ks.Slots(), nil)
		unl.Close()
		ks.Close()
		// Built; installed only if nothing cut the ceremony short meanwhile.
		c.mu.Lock()
		cut := cer.latch || cer.ctx.Err() != nil
		c.mu.Unlock()
		if cut {
			return ErrTokenCancelled
		}
		if err := c.install(staged, path, displayName); err != nil {
			return err
		}
		installed = true
		// Installed: the key is shown now — unless a lock trigger landed
		// meanwhile, which drops every held key; then it is shown from
		// the Keys page (FORMAT R38), and the create still succeeded.
		url, err := cer.mint(rk.Digits(), "Recovery key", rid)
		if err != nil {
			c.log("ceremony %s: installed; a lock landed before the key was shown", cer.kind)
			cer.set(func(s *CeremonyState) { s.Step = StepDone })
			return nil
		}
		// And the vault opens now, with the key just made: the user is in
		// without unlocking again, and the key is shown over the vault.
		cer.enterCreated(path, rk)
		cer.set(func(s *CeremonyState) { s.Step, s.SlotLabel, s.RecoveryID = StepRecovery, url, rid })
		return nil
	})
	return nil
}

// enterCreated opens the vault just installed with the recovery key just
// made and publishes the session, so that a create ends Unlocked with the
// key shown over the vault (APP.md §2.1) — no second unlock for a way in
// the user chose a minute ago. A lock trigger that landed meanwhile, or a
// file that will not open, leaves it Locked with the key still shown: the
// vault is in place either way, and the lock screen says so.
func (cer *ceremony) enterCreated(path string, rk kdf.RecoveryKey) {
	c := cer.c
	ks, err := keystore.Open(path)
	if err != nil {
		c.log("ceremony %s: created; opening it: %v", cer.kind, err)
		return
	}
	unl, err := ks.Unlock(keystore.RecoveryCredential{Key: rk})
	if err != nil {
		c.log("ceremony %s: created; unlocking it: %v", cer.kind, err)
		ks.Close()
		return
	}
	c.mu.Lock()
	if cer.latch || cer.ctx.Err() != nil || c.vault.state != StateLocked || !samePath(c.vault.path, path) {
		c.mu.Unlock()
		unl.Close()
		ks.Close()
		return
	}
	c.publishUnlockedLocked(ks, unl) // settles the owed receipts
	c.mu.Unlock()
	c.emitState()
}

// decodeHexKey parses a typed management key.
func decodeHexKey(s string) ([]byte, error) {
	b := make([]byte, 0, 32)
	var nib []byte
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			nib = append(nib, byte(r-'0'))
		case r >= 'a' && r <= 'f':
			nib = append(nib, byte(r-'a'+10))
		case r >= 'A' && r <= 'F':
			nib = append(nib, byte(r-'A'+10))
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
		default:
			return nil, errors.New("not hex")
		}
	}
	if len(nib)%2 != 0 {
		return nil, errors.New("odd length")
	}
	for i := 0; i < len(nib); i += 2 {
		b = append(b, nib[i]<<4|nib[i+1])
	}
	switch len(b) {
	case 16, 24, 32:
		return b, nil
	}
	return nil, errors.New("length")
}

var _ = format.SlotRecovery

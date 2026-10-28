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
	"slices"
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
// never refused. No field asks for the old password in either direction,
// though a YubiKey's proof asks the current one with its PIN: whoever
// reaches the VMK can already enrol a way in of their own. rekey re-keys
// the vault in the same commit as turning it on, and backupPath, when not
// empty, is a backup written after both (ruled 2026-10-27); turning the
// password off takes neither, and is refused with params if given either.
func (c *Core) SetEntangled(on, rekey bool, backupPath string) *Error {
	if !on && (rekey || backupPath != "") {
		return coded(CodeParams)
	}
	c.mu.Lock()
	unlocked, can := c.vault.state == StateUnlocked, c.vault.canEnable
	frozen := c.vault.tampered != nil
	var e *Error
	switch {
	// R25's freeze is read here as well as in beginAs: a pre-check that
	// answered first would hide the reason behind an invariant the user
	// cannot act on (APP.md §13).
	case on && unlocked && frozen:
		e = coded(CodeVaultTampered)
	case on && unlocked && !can:
		e = coded(CodeInvariant)
	default:
		e = c.backupRefusalLocked(backupPath, [16]byte{})
	}
	c.mu.Unlock()
	if e != nil {
		return e
	}
	rekeys := on && rekey
	return c.beginAs("entangle", actOpts{rekey: rekeys}, func(cer *ceremony, unl *keystore.Unlocked) error {
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
		commit := func() error { return unl.SetEntangled(on, e) }
		if rekeys {
			commit = func() error { return unl.SetEntangledAndRotate(*e) }
		}
		if err := c.landChange(cer, unl, rekeys, commit); err != nil {
			return err
		}
		c.backUpAfter(cer, unl, backupPath)
		return nil
	})
}

// ChangeEntangledPassword replaces the vault's password (APP.md §13): the
// same one-write shape as SetEntangled, and the old password is never a
// field. Refused before any prompt while the switch is off, so a salt
// redraw cannot happen by the wrong button. Its own act, kind "password",
// with an end of its own; rekey re-keys the vault in the same commit, and
// backupPath, when not empty, is a backup written after both (ruled
// 2026-10-27).
func (c *Core) ChangeEntangledPassword(rekey bool, backupPath string) *Error {
	c.mu.Lock()
	on, unlocked := c.vault.entangled, c.vault.state == StateUnlocked
	frozen := c.vault.tampered != nil
	var e *Error
	switch {
	// The freeze answers before the switch-off refusal, for the same
	// reason as in SetEntangled: the reason must be the one to act on.
	case unlocked && frozen:
		e = coded(CodeVaultTampered)
	case unlocked && !on:
		e = coded(CodeParams)
	default:
		e = c.backupRefusalLocked(backupPath, [16]byte{})
	}
	c.mu.Unlock()
	if e != nil {
		return e
	}
	return c.beginAs("password", actOpts{rekey: rekey}, func(cer *ceremony, unl *keystore.Unlocked) error {
		cer.releaseCard()
		pw, err := cer.askNew("password", StepPassword)
		if err != nil {
			return err
		}
		ent := keystore.Entangle{Password: pw, Argon2: defaultArgon2}
		commit := func() error { return unl.ChangeEntangledPassword(ent) }
		if rekey {
			commit = func() error { return unl.ChangeEntangledPasswordAndRotate(ent) }
		}
		if err := c.landChange(cer, unl, rekey, commit); err != nil {
			return err
		}
		c.backUpAfter(cer, unl, backupPath)
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
// WithRecoveryKey is the user's word at BeginEnroll that no YubiKey of the
// vault is at hand: the proof is then a recovery key (ruled 2026-10-27),
// for a YubiKey or a recovery key added to a vault that has a YubiKey and a
// recovery key, and never otherwise.
type EnrollOptions struct {
	Kind            EnrollKind
	Label           string
	Entangle        bool
	WithRecoveryKey bool
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

// actOpts is how an act begins: whether it may run on a tampered vault (the
// reveal), whether it re-keys in the same commit as its change, the YubiKey a
// re-keying removal does not ask for, and whether the proof is a recovery key
// by the user's word (ruled 2026-10-27).
type actOpts struct {
	allowTampered bool
	rekey         bool
	without       [16]byte
	recoveryProof bool
	// reads: the act writes nothing into the vault file and saves no
	// backup (the reveal), so the barrier does not hold it. The merge's
	// inspection writes nothing either but is the first half of a held act,
	// and does not set it.
	reads bool
}

// beginMutation is an act that needs the VMK and writes the vault file or a
// backup, with none of beginAs's options: the ceremony re-runs to obtain an
// Unlocked on the open handle, fn uses it, and it is closed. The barrier
// holds it and a tampered vault refuses it. The export is its caller; the
// slot acts call beginAs with their own options.
func (c *Core) beginMutation(kind string, fn func(cer *ceremony, unl *keystore.Unlocked) error) *Error {
	return c.beginAs(kind, actOpts{}, fn)
}

// beginWithVMK runs fn with an Unlocked recovered through a protector, for an
// act that only reads: the reveal of APP.md §3 Keys, the one allowTampered
// admits.
func (c *Core) beginWithVMK(kind string, allowTampered bool, fn func(cer *ceremony, unl *keystore.Unlocked) error) *Error {
	return c.beginAs(kind, actOpts{allowTampered: allowTampered, reads: true}, fn)
}

// beginAs runs fn with an Unlocked recovered through a protector. It holds
// the handle like a slot change whether or not fn writes the file:
// keystore.Unlock is not a read of the shared handle, so a registry write
// waits for it and it waits for one. Under the one hold of the state mutex
// that creates the ceremony it refuses a Delete that holds its claim on a
// record, so that no change and failed retirement land between that delete's
// removal of the file and its forgetting of the record, and, unless the act
// only reads, it meets the barrier (actBarrierLocked): no ceremony that
// writes the file or a backup begins over an owed retirement, and none
// begins between a barrier and its write.
func (c *Core) beginAs(kind string, o actOpts, fn func(cer *ceremony, unl *keystore.Unlocked) error) *Error {
	c.mu.Lock()
	v := &c.vault
	if v.state != StateUnlocked {
		c.mu.Unlock()
		return coded(CodeNeedsUnlock)
	}
	if !o.allowTampered && v.tampered != nil {
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
	if len(c.deleting) > 0 {
		c.mu.Unlock()
		return coded(CodeOpRunning)
	}
	if !o.reads {
		if e := c.actBarrierLocked(); e != nil {
			c.mu.Unlock()
			if e.Code == CodeIndeterminate || e.Code == CodeConflict {
				c.emitState() // the barrier entered Broken
			}
			return e
		}
	}
	cer := c.newCeremonyLocked(kind)
	cer.mutation = true
	cer.state.Rekey = o.rekey
	cer.without, cer.recoveryProof = o.without, o.recoveryProof
	if o.without != ([16]byte{}) {
		cer.state.Without = hexID(o.without)
	}
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
// else a password; the recovery key when the vault has nothing else — an
// adopted backup being set up, a vault whose one token is stale, or one
// whose only YubiKey is the one a re-keying removal takes away (without,
// which never counts as a YubiKey to prove with) — and when the user has
// said no YubiKey of the vault is at hand (recoveryProof, ruled
// 2026-10-27). The token flow is still handed every slot: the YubiKey being
// removed, inserted anyway, proves as it does today, rather than parking the
// ceremony on a "no slot matches" that would read as if it were not the
// vault's.
func (cer *ceremony) acquireUnlocked() (*keystore.Unlocked, Card, error) {
	c := cer.c
	c.mu.Lock()
	ks := c.vault.ks
	slots := c.vault.slots
	entangled := c.vault.entangled
	var hasToken, hasPassword bool
	for _, s := range slots {
		if s.PublicKey != nil && (cer.without == [16]byte{} || s.RecipientID != cer.without) {
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
	if cer.recoveryProof || (!hasToken && !hasPassword) {
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
				// Pulled during the PIN or the touch: back to waiting. The
				// plain close is deliberate: the pull took the verified
				// state with it, so there is no reset to report.
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
// token flow follows (APP.md §3, Keys). With WithRecoveryKey the proof is a
// recovery key: refused for a password, which never joins a vault that has
// a YubiKey and so never waits for one; on a vault without a YubiKey, which
// never waits for one either; and on a vault without a recovery key. The
// commit lands through landChange, so that the enrolment, like every act
// that writes the slot region, leaves the file retired.
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
	if o.WithRecoveryKey {
		if o.Kind == EnrollPassword {
			return coded(CodeParams)
		}
		c.mu.Lock()
		var e *Error
		if c.vault.state == StateUnlocked {
			var hardware, recovery bool
			for _, s := range c.vault.slots {
				hardware = hardware || s.PublicKey != nil
				recovery = recovery || s.Type == format.SlotRecovery
			}
			switch {
			case c.vault.tampered != nil:
				e = coded(CodeVaultTampered)
			case !hardware:
				e = coded(CodeParams)
			case !recovery:
				e = coded(CodeNoRecoverySlot)
			}
		}
		c.mu.Unlock()
		if e != nil {
			return e
		}
	}
	return c.beginAs("enroll", actOpts{recoveryProof: o.WithRecoveryKey}, func(cer *ceremony, unl *keystore.Unlocked) error {
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
			before := recipientSet(unl.Keystore().Slots())
			if err := c.landChange(cer, unl, false, func() error { return unl.AddSlot(spec) }); err != nil {
				return err
			}
			// Shown over the one-time channel with its ID, and kept once
			// more in the vault (FORMAT R38, §18.4).
			id := newRecoveryID(unl.Keystore().Slots(), before)
			url, err := cer.mint(rk.Digits(), o.Label, id)
			if err != nil {
				// The key is the vault's: a lock that latched the ceremony
				// after the commit dropped what it would show, and the key
				// is shown again from the Vault page through a protector
				// (FORMAT R38). The act is done, as a create's is.
				c.log("ceremony %s: added; a lock landed before the key was shown", cer.kind)
				return nil
			}
			cer.set(func(s *CeremonyState) { s.Step, s.SlotLabel, s.RecoveryID = StepRecovery, url, id })
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
		return c.landChange(cer, unl, false, func() error { return unl.AddSlot(spec) })
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
//
// The heading is recoveryKeyTitle — the same string the page uses as the
// suggested file name and as the print's document.title, so the sheet, the
// file and the page all say which key this is (APP.md §6, DECISIONS
// 2026-09-09, BitLocker's own shape).
func recoveryKeyText(name, label, id string, at time.Time, digits string) string {
	lines := []string{
		recoveryKeyTitle(id),
		"Vault: " + name,
		"Way in: " + label,
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

// recoveryKeyTitle names one recovery key: the key's ID and never the
// vault's name, which says nothing about which sheet this is (FORMAT.md
// §18.4). It heads the saved file and the printed sheet; the page adds
// ".txt" for the Save dialog's suggested name and sets document.title to it
// for the print, so Print to PDF offers the same name. The core imposes
// nothing on the name the user then chooses (SaveRecoveryKey judges the
// place, never the name).
// The trim is what the page does (frontend/src/lib/print.ts): an empty ID
// leaves the bare "Enfold Keystore Recovery Key" rather than a trailing
// space, so the two strings §6 requires to be the same are the same.
func recoveryKeyTitle(id string) string {
	return strings.TrimSpace("Enfold Keystore Recovery Key " + id)
}

// RemoveSlot removes a way in after a ceremony (APP.md §13). rekey re-keys
// the vault in the same commit, so that the method removed opens nothing the
// vault holds from now on (FORMAT §8), and backupPath, when not empty, is a
// backup written after both (ruled 2026-10-27). What the invariant refuses
// is refused before the proof, from the cached facts, the tampered reason
// first; the keystore's checks inside the ceremony stay the authority. A
// re-keying removal of a YubiKey does not ask for that YubiKey: where it was
// the vault's last, a recovery key proves it (ruled 2026-10-27).
func (c *Core) RemoveSlot(recipientID string, rekey bool, backupPath string) *Error {
	rid, ok := parseID(recipientID)
	if !ok {
		return coded(CodeParams)
	}
	o := actOpts{rekey: rekey}
	c.mu.Lock()
	var e *Error
	if c.vault.state == StateUnlocked {
		var slot *keystore.SlotInfo
		for i := range c.vault.slots {
			if c.vault.slots[i].RecipientID == rid {
				slot = &c.vault.slots[i]
			}
		}
		switch {
		case c.vault.tampered != nil:
			e = coded(CodeVaultTampered)
		case slot == nil:
			e = coded(CodeSlotNotFound)
		case !c.vault.removable[rid]:
			e = coded(CodeInvariant)
		}
		if e == nil && rekey && slot.PublicKey != nil {
			o.without = rid
		}
	}
	if e == nil {
		e = c.backupRefusalLocked(backupPath, rid)
	}
	c.mu.Unlock()
	if e != nil {
		return e
	}
	return c.beginAs("remove", o, func(cer *ceremony, unl *keystore.Unlocked) error {
		commit := func() error { return unl.RemoveSlot(rid) }
		if rekey {
			commit = func() error { return unl.RemoveSlotAndRotate(rid) }
		}
		if err := c.landChange(cer, unl, rekey, commit); err != nil {
			return err
		}
		c.backUpAfter(cer, unl, backupPath)
		return nil
	})
}

// RotateNow rotates the VMK and re-derives the session (APP.md §2.1);
// backupPath, when not empty, is a backup written after the re-key and its
// retirement, at the new key (ruled 2026-10-27).
func (c *Core) RotateNow(backupPath string) *Error {
	c.mu.Lock()
	var e *Error
	if c.vault.state == StateUnlocked && c.vault.tampered != nil {
		e = coded(CodeVaultTampered)
	} else {
		e = c.backupRefusalLocked(backupPath, [16]byte{})
	}
	c.mu.Unlock()
	if e != nil {
		return e
	}
	return c.beginAs("rotate", actOpts{}, func(cer *ceremony, unl *keystore.Unlocked) error {
		if err := c.landChange(cer, unl, true, unl.Rotate); err != nil {
			return err
		}
		c.backUpAfter(cer, unl, backupPath)
		return nil
	})
}

// unknownOutcome reports the two keystore errors that leave the handle
// Broken: a commit whose outcome this handle cannot know, and another
// writer's commit met on disk.
func unknownOutcome(err error) bool {
	return errors.Is(err, keystore.ErrIndeterminate) || errors.Is(err, keystore.ErrConflict)
}

// landChange runs an act's commit, then R41's retirement in full, then brings
// the core onto what was committed: the next Session after a commit that
// advanced the generation (APP.md §2.1), the cached facts otherwise. A lock
// that lands after the commit leaves the act done: the change is on disk, its
// retirement still runs, and new keys are zeroed rather than installed behind
// a Locked state.
//
// Once commit has returned nil it returns nil or one of the two errors that
// enter Broken, from the retirement: never an ordinary error, which the page
// reads as "Nothing was changed." and, for a replacement, as keys never
// added, which would tell the user to destroy the vault's own recovery keys.
// A commit that failed after writing is retired at once, inside the act, so
// that what it wrote is not left in the file for a later mutation or the
// next unlock to find.
func (c *Core) landChange(cer *ceremony, unl *keystore.Unlocked, rekeys bool, commit func() error) error {
	if err := cer.check(); err != nil {
		return err // the last point at which a cancel or a lock changes nothing
	}
	cer.set(func(s *CeremonyState) { s.Step, s.Phase = StepDeriving, "commit" })
	ks := unl.Keystore()
	err := c.seams.fail("commit")
	if err == nil {
		err = commit()
	}
	if err != nil {
		return c.retireFailedCommit(cer, unl, err)
	}
	if c.seams.committed != nil {
		c.seams.committed() // a test, between the change and its retirement
	}
	// Every retiring commit the file needs and their scrubs, not one pass.
	// It runs whether or not a lock landed after the commit: the handle is
	// still the ceremony's, since the lock's close waits for it.
	rerr := c.seams.fail("retire")
	if rerr == nil {
		rerr = unl.Retire()
	}
	c.mu.Lock()
	o, rerr := c.settleRetireLocked(ks, rerr)
	c.mu.Unlock()
	switch o {
	case retireUnknown:
		return rerr
	case retireTampered, retireStillOwed:
		// The change stands, and the vault stays open for reading and for
		// the files inside its archives; every mutation that writes a record
		// or a secret, and every backup, waits behind the barrier. The bar
		// is set only while something is owed: an error that came with a
		// retirement that left nothing owed says nothing the file still
		// holds (retireLanded).
		c.log("ceremony %s: the change landed; its retirement did not: %v", cer.kind, rerr)
		code, remnant := classify(rerr).Code, ks.RetireRemnantOnly()
		cer.set(func(s *CeremonyState) { s.RetireError, s.RetireRemnant = code, remnant })
	}
	if rekeys {
		c.installNext(cer, unl)
	} else {
		c.afterMutation()
	}
	// The end's next step reads what the act left on disk, never the
	// page's slot list of before (the outside review's second reading,
	// finding 7).
	after := afterFactsOf(ks)
	cer.set(func(s *CeremonyState) { s.After = after })
	return nil
}

// retireFailedCommit ends an act whose commit through unl failed, err being
// its error: a commit that wrote anything before it failed left the file
// owed (FORMAT R41), and the ceremony still holds the handle, so what it
// wrote — a replacement's new slots wrapping the VMK it drew, a failed
// enrolment's record or a setup's first way in wrapping the current one — is
// retired now rather than left for a later gated mutation or the next
// unlock. The retirement changes no state, so "Nothing was changed." stays
// true. The cached facts follow the file either way, so that the barrier
// sees at once what is still owed, and the Vault page and the lock screen
// the date and the size the file has. It returns the error the act ends
// with: an unknown outcome of the retirement, which enters Broken, in place
// of the commit's, else the commit's own.
func (c *Core) retireFailedCommit(cer *ceremony, unl *keystore.Unlocked, err error) error {
	ks := unl.Keystore()
	if !unknownOutcome(err) && ks.RetireOwed() {
		rerr := c.seams.fail("retire")
		if rerr == nil {
			rerr = unl.Retire()
		}
		c.mu.Lock()
		o, rerr := c.settleRetireLocked(ks, rerr)
		c.mu.Unlock()
		switch o {
		case retireUnknown:
			return rerr
		case retireTampered, retireStillOwed:
			c.log("ceremony %s: a failed commit's bytes could not be retired: %v", cer.kind, rerr)
		}
		return vaultErr(err)
	}
	c.mu.Lock()
	c.noteFileLocked(ks, false)
	c.mu.Unlock()
	return vaultErr(err)
}

// installNext brings the core onto the generation a re-keying act committed:
// the next Session is derived and installed, the old one zeroed. A Session
// that cannot be derived — reachable only through a handle no longer
// current, and not expected after the handle's own commit — locks the vault
// as a stale Session reaching a service does, the act still done; a lock
// that landed first gets the new keys zeroed rather than installed behind a
// Locked state, the next unlock opening at the new generation.
func (c *Core) installNext(cer *ceremony, unl *keystore.Unlocked) {
	if c.seams.rekeyed != nil {
		c.seams.rekeyed() // a test, the commits landed and the Session not yet installed
	}
	ks := unl.Keystore()
	err := c.seams.fail("session")
	var next *keystore.Session
	if err == nil {
		next, err = unl.Session()
	}
	if err != nil {
		c.log("ceremony %s: re-keyed; the next Session could not be derived: %v", cer.kind, err)
		go c.LockNow(ReasonStale) // its close waits for this ceremony
		return
	}
	c.mu.Lock()
	v := &c.vault
	if v.state != StateUnlocked || v.ks != ks {
		c.mu.Unlock()
		next.Lock()
		c.log("ceremony %s: re-keyed; a lock landed before the session was installed", cer.kind)
		return
	}
	old := v.sess
	v.sess = next
	if old != nil {
		old.Lock()
	}
	// The merge handles hold archive keys wrapped under the KWK just
	// replaced: merged now, they would publish keys no Session of this
	// vault unwraps. They are dropped with the old Session, and the dialog's
	// merge answers ceremony.stale_prompt (the outside review of the diff,
	// finding 4).
	v.incoming = nil
	c.cacheFactsLocked(v.ks)
	c.dropPendingLocked() // a slot change: whatever was pending is not adopted
	c.armTimersLocked()   // never extends a running idle deadline
	c.mu.Unlock()
	c.emitState()
}

// afterFactsOf is what an act left committed, read from the handle's
// plaintext slots and header, which the ceremony still owns whether or not
// a lock landed: the end's next step is decided by them (CeremonyState.After).
func afterFactsOf(ks *keystore.Keystore) *AfterFacts {
	slots := ks.Slots()
	f := &AfterFacts{RecoveryIDs: []string{}, Entangled: ks.Entangled(), SetupNeeded: setupNeeded(slots)}
	for _, s := range slots {
		if s.Type == format.SlotRecovery {
			f.RecoveryIDs = append(f.RecoveryIDs, recoveryID(s.RecipientID))
		}
	}
	return f
}

// vaultErr: a record of the vault file that does not open (format.ErrInvalid,
// which keystore's corrupt wraps) is the vault's problem, not an archive's,
// as the reveal and the merge already say. The cause rides along for the
// log; the code is vault.invalid.
func vaultErr(err error) error {
	if errors.Is(err, format.ErrInvalid) {
		return fmt.Errorf("%w: %v", coded(CodeVaultInvalid), err)
	}
	return err
}

// recoveryHex is the hex recipient IDs of the active recovery slots, the
// recovery keys a backup made now holds (keystore.Export copies the active
// recovery slots and no other).
func recoveryHex(slots []keystore.SlotInfo) []string {
	var out []string
	for _, s := range slots {
		if s.Type == format.SlotRecovery {
			out = append(out, hexID(s.RecipientID))
		}
	}
	return out
}

// backupPlaceOK is where a backup may be written: not under a staging name,
// and not into the data folder, which is Enfold's to sweep.
func (c *Core) backupPlaceOK(path string) bool {
	return !reservedName(path) && !samePath(filepath.Dir(path), c.deps.DataDir)
}

// backupRefusalLocked is what every act with a backup refuses before its
// ceremony (APP.md §13): a place a backup never goes, and, while Unlocked, a
// vault that would keep no recovery key after the act, since a backup holds
// the recovery slots alone. removed is the slot the act takes away, zero
// for none. "" is no backup and refuses nothing. Caller holds the state
// mutex.
func (c *Core) backupRefusalLocked(path string, removed [16]byte) *Error {
	if path == "" {
		return nil
	}
	if !c.backupPlaceOK(path) {
		return coded(CodeParams)
	}
	if c.vault.state != StateUnlocked {
		return nil // beginAs answers vault.needs_unlock
	}
	for _, s := range c.vault.slots {
		if s.Type == format.SlotRecovery && s.RecipientID != removed {
			return nil
		}
	}
	return coded(CodeNoRecoverySlot)
}

// backUpAfter writes the backup an act was asked for (APP.md §13), from the
// Unlocked the act proved, AFTER its change and its retirement, and only once
// the retirement has landed in full (ruled 2026-10-27, R-C): the export of
// FORMAT §15 at the vault's new generation, then lastExportAt with that
// generation and the recovery keys it holds. The change stands whatever
// happens here, so a failure is the act's BackupError, not its error, and the
// act still ends Done. "" takes no backup; a lock that landed before it is
// admitted skips it; a retirement still owed skips it with BackupError
// vault.retire_owed, the stamp unchanged; once admitted it runs to its end.
func (c *Core) backUpAfter(cer *ceremony, unl *keystore.Unlocked, path string) {
	if path == "" {
		return
	}
	ks := unl.Keystore()
	// The admission, under the state mutex and released before anything
	// else: cer.set and recordExport take it themselves.
	c.mu.Lock()
	if c.vault.state != StateUnlocked || c.vault.ks != ks {
		c.mu.Unlock()
		// The user is gone: nothing is written behind a Locked state, and
		// the backups card asks again at the next unlock.
		c.log("ceremony %s: backup skipped: a lock landed after the change", cer.kind)
		return
	}
	if ks.RetireOwed() {
		c.mu.Unlock()
		// The order is the change, its retirement, then the backup, kept
		// where the retirement fails: a backup taken now would stamp a
		// vault that still holds what the act withdrew.
		c.log("ceremony %s: backup skipped: the retirement did not land", cer.kind)
		cer.set(func(s *CeremonyState) { s.BackupError = CodeRetireOwed })
		return
	}
	c.mu.Unlock()
	cer.set(func(s *CeremonyState) { s.Phase = "backup" })
	if err := unl.Export(path); err != nil {
		code := classify(err).Code
		c.log("ceremony %s: the backup was not written: %s: %v", cer.kind, code, err)
		cer.set(func(s *CeremonyState) { s.BackupError = code })
		return
	}
	c.recordExport(ks.Generation(), recoveryHex(ks.Slots()))
	cer.set(func(s *CeremonyState) { s.BackedUp = true })
}

// ExportBackup writes a keystore file of registry plus recovery slots.
// Not into the data folder, which is Enfold's to sweep. Held by the barrier
// like every act that writes a record or a secret: a backup of a vault that
// still owes a retirement would carry what the owed change withdrew.
func (c *Core) ExportBackup(path string) *Error {
	if !c.backupPlaceOK(path) {
		return coded(CodeParams)
	}
	c.mu.Lock()
	var e *Error
	if c.vault.state == StateUnlocked && c.vault.tampered != nil {
		e = coded(CodeVaultTampered)
	} else {
		e = c.backupRefusalLocked(path, [16]byte{})
	}
	c.mu.Unlock()
	if e != nil {
		return e
	}
	return c.beginMutation("export", func(cer *ceremony, unl *keystore.Unlocked) error {
		if err := cer.check(); err != nil {
			return err
		}
		cer.set(func(s *CeremonyState) { s.Phase = "backup" })
		if err := unl.Export(path); err != nil {
			return err
		}
		ks := unl.Keystore()
		c.recordExport(ks.Generation(), recoveryHex(ks.Slots()))
		return nil
	})
}

// recordExport stamps the settings file with this vault's last backup
// (APP.md §13): a local, unauthenticated convenience that words the
// confirmations of Forget and Delete, pre-selects the rotate dialog's
// backup checkbox and, by gen and recovery, lets the backups card say when
// the vault was re-keyed or its recovery keys replaced since. It gates
// nothing cryptographic, so a failed write is logged and nothing else.
func (c *Core) recordExport(gen uint64, recovery []string) {
	c.mu.Lock()
	c.settings.LastExport = &lastExport{VaultID: hexID(c.vault.vaultID), At: c.now().Unix(), Gen: gen, Recovery: recovery}
	file := c.settings
	c.mu.Unlock()
	if err := saveSettings(c.deps.DataDir, file); err != nil {
		c.log("settings: %v", err)
	}
}

// ReplaceRecoveryKeys makes as many new recovery keys as the vault has, shows
// each to be kept, and on the user's one confirmation replaces every old one
// and re-keys the vault in one commit (ruled 2026-10-27; FORMAT §8, R38), so
// that no new key is ever escrowed under a state an old one still opens.
// Refused before the proof on a tampered vault, on one whose methods are all
// recovery keys (no protector would remain to show a new key again, and the
// replacement never proves with the key it replaces) and on one with no
// recovery key. backupPath, when not empty, is a backup written after the
// replacement and its retirement.
func (c *Core) ReplaceRecoveryKeys(backupPath string) *Error {
	c.mu.Lock()
	var e *Error
	if c.vault.state == StateUnlocked {
		switch {
		case c.vault.tampered != nil:
			e = coded(CodeVaultTampered)
		case c.setupNeededLocked():
			e = coded(CodeSetupNeeded)
		case !c.hasRecoveryLocked():
			e = coded(CodeNoRecoverySlot)
		}
	}
	if e == nil {
		e = c.backupRefusalLocked(backupPath, [16]byte{})
	}
	c.mu.Unlock()
	if e != nil {
		return e
	}
	return c.beginAs("replace", actOpts{rekey: true}, func(cer *ceremony, unl *keystore.Unlocked) error {
		return c.replaceWith(cer, unl, backupPath)
	})
}

// hasRecoveryLocked reports an active recovery slot among the cached facts.
// Caller holds the state mutex.
func (c *Core) hasRecoveryLocked() bool {
	for _, s := range c.vault.slots {
		if s.Type == format.SlotRecovery {
			return true
		}
	}
	return false
}

// replaceWith is the replacement's ceremony after its proof: the old keys in
// the order the Vault page lists them, then for each a new key made, shown and
// kept, one at a time, so that the second is not drawn until the first is
// kept; then the one confirmation, then the one transaction, then the backup.
// A cancel, a lock or a prompt's deadline before the commit ends it with
// nothing written: no key drawn so far has a slot. Whatever the outcome,
// every handle it minted is dropped and every prepared key zeroed: finish
// drops no secret, and the page's Cancel calls only CancelUnlock.
func (c *Core) replaceWith(cer *ceremony, unl *keystore.Unlocked, backupPath string) error {
	cer.releaseCard() // nothing after the proof needs the YubiKey: step 4 of §8 wraps from public keys
	var preps []*keystore.PreparedRecovery
	var minted []string
	defer func() {
		for _, p := range preps {
			p.Zero()
		}
		for _, tok := range minted {
			c.preview.dropSecret(tok)
		}
	}()
	var olds []keystore.SlotInfo
	for _, s := range unl.Keystore().Slots() {
		if s.Type == format.SlotRecovery {
			olds = append(olds, s)
		}
	}
	n := len(olds)
	if n == 0 {
		return coded(CodeNoRecoverySlot) // the pre-check's backstop
	}
	oldIDs := make([]string, n)
	for i, s := range olds {
		oldIDs[i] = recoveryID(s.RecipientID)
	}
	cer.set(func(s *CeremonyState) { s.OldIDs, s.KeepCount = oldIDs, n })
	for i := 1; i <= n; i++ {
		// The new slot takes the old one's label, which is where the user
		// keeps that sheet, so the new sheet goes where the old one was.
		label := olds[i-1].Label
		err := func() error {
			rk, err := kdf.NewRecoveryKey()
			if err != nil {
				return err
			}
			defer kdf.Zero(rk[:]) // the prepared slot holds its own copy, in a locked page
			prep, err := unl.PrepareRecovery(rk, label, preps)
			if err != nil {
				return err
			}
			preps = append(preps, prep)
			id := recoveryID(prep.RecipientID())
			url, err := cer.mint(rk.Digits(), label, id)
			// The prepared slot and the minted secret hold what is needed from
			// here on: this ordinary-memory copy is erased now, before the
			// keep prompt, which can wait minutes, and not by the deferred
			// Zero after it (SCOPE.md: a replacement's new keys live in locked
			// pages until it ends).
			kdf.Zero(rk[:])
			if err != nil {
				return err
			}
			minted = append(minted, secretToken(url))
			return cer.keep(i, n, url, id)
		}()
		if err != nil {
			return err
		}
	}
	if err := cer.confirm(); err != nil {
		return err
	}
	if err := c.landChange(cer, unl, true, func() error { return unl.ReplaceRecoveryAndRotate(preps) }); err != nil {
		return err
	}
	c.backUpAfter(cer, unl, backupPath)
	return nil
}

// secretToken is the handle a one-time URL carries, its last segment
// (mintSecret's "/s/<token>").
func secretToken(url string) string {
	return url[strings.LastIndex(url, "/")+1:]
}

// keep shows the i-th of a replacement's n new recovery keys — its one-time
// URL and its ID — and waits for the user to have kept it: the prompt of kind
// kept, answered through SubmitSecret with an empty value, the one mailbox a
// ceremony is answered by. No second confirmation follows, whichever way the
// key was kept (ruled 2026-09-09; R-B).
func (cer *ceremony) keep(i, n int, url, id string) error {
	c := cer.c
	c.mu.Lock()
	cer.state.SlotLabel, cer.state.RecoveryID = url, id
	cer.state.KeepIndex, cer.state.KeepCount = i, n
	// A fresh slice, so that a state already emitted keeps the list it had.
	cer.state.NewIDs = append(append([]string(nil), cer.state.NewIDs...), id)
	c.mu.Unlock()
	_, err := cer.ask("kept", StepKeepKey, PINStatus{})
	return err
}

// confirm asks the replacement's one confirmation before its commit: the
// page draws each new key beside the one it replaces, and the press that
// answers it is the user's last word (ruled 2026-10-27, R-B). The last key's
// URL is not carried into it: every key is kept by now.
func (cer *ceremony) confirm() error {
	c := cer.c
	c.mu.Lock()
	cer.state.SlotLabel, cer.state.RecoveryID = "", ""
	c.mu.Unlock()
	_, err := cer.ask("confirm", StepConfirm, PINStatus{})
	return err
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
	return c.lastExportAtLocked()
}

// lastExportAtLocked is LastExportAt under the state mutex.
func (c *Core) lastExportAtLocked() int64 {
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

// BackupStatus is how the vault kept here stands against its last backup
// (APP.md 13, direction 9): At is LastExportAt's answer, and the counts are
// the archives the last backup lacks and those it holds with an older key
// — each record's created_at and each version's against At, never
// modified_at, which every save moves. Only an unlocked registry is read, as for the effective
// timeouts, so Locked, no vault and Tampered answer the counts as 0: a
// Tampered registry may not be the vault's own, and the page's danger
// banner, not a backup count, is what it owes the user then. One known
// gap: a record or a version merged in from another file keeps the
// created_at it was made with (merge.go), so one made before At and merged
// after it reads as in the backup when it is not. The line words and gates
// nothing (lastExportAt is unauthenticated), so the gap is a wording one.
func (c *Core) BackupStatus() BackupStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	bs := BackupStatus{At: c.lastExportAtLocked()}
	sess, e := c.sessionLocked()
	if e != nil || c.vault.tampered != nil {
		return bs
	}
	if bs.At != 0 {
		// A backup at a retired key continues at that key when adopted, and
		// one whose recovery keys were all replaced opens only with keys the
		// user was told to destroy: the card asks for a new one in both.
		le := c.settings.LastExport
		bs.VaultRekeyed = le.Gen != 0 && le.Gen < c.vault.generation
		if len(le.Recovery) > 0 {
			bs.KeysReplaced = true
			active := recoveryHex(c.vault.slots)
			for _, id := range le.Recovery {
				if slices.Contains(active, id) {
					bs.KeysReplaced = false
					break
				}
			}
		}
	}
	g := sess.Registry()
	for i := range g.Archives {
		a := &g.Archives[i]
		if a.Forgotten() {
			continue
		}
		bs.Archives++
		if bs.At == 0 || a.CreatedAt > bs.At {
			bs.Missing++
			continue
		}
		// One count per archive, however many re-keys it had since: the
		// card says "<n> archives re-keyed since", a count of archives.
		for _, v := range a.Versions {
			if v.CreatedAt > bs.At {
				bs.Rekeyed++
				break
			}
		}
	}
	return bs
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
	if err := c.publishUnlockedLocked(ks, unl); err != nil { // settles the owed receipts
		// A new file owes nothing, so this is not expected; Broken says it.
		c.log("ceremony %s: created; publishing it: %v", cer.kind, err)
	}
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

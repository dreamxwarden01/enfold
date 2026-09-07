package app

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

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
		out = append(out, slotView(s))
	}
	return out
}

// EnrollKind is which kind of slot an enrollment adds.
type EnrollKind string

const (
	EnrollToken    EnrollKind = "token"
	EnrollPassword EnrollKind = "password"
	EnrollRecovery EnrollKind = "recovery"
)

// EnrollOptions describes the slot to add.
type EnrollOptions struct {
	Kind     EnrollKind
	Label    string
	Entangle bool // token slot with an entangled password
}

// mutation is a slot change that needs the VMK: the ceremony re-runs to
// obtain an Unlocked on the open handle, fn uses it, and it is closed.
func (c *Core) beginMutation(kind string, fn func(cer *ceremony, unl *keystore.Unlocked) error) *Error {
	c.mu.Lock()
	v := &c.vault
	if v.state != StateUnlocked {
		c.mu.Unlock()
		return coded(CodeNeedsUnlock)
	}
	if v.tampered != nil {
		c.mu.Unlock()
		return coded(CodeVaultTampered)
	}
	if c.cer != nil {
		c.mu.Unlock()
		return coded(CodeCeremonyRunning)
	}
	// An operation about to write the registry would race the ceremony's
	// own commits on the one handle: it goes first.
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
	var hasToken, hasPassword bool
	for _, s := range slots {
		if s.PublicKey != nil && !s.Stale {
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
		cred, _, _, err := cer.credential(MethodRecovery, nil)
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
		h, card, slot, err := cer.tokenCredentialFor(slots)
		if err != nil {
			return nil, nil, err
		}
		c.mu.Lock()
		cer.unlockPub, cer.unlockLabel = slot.PublicKey, slot.Label
		c.mu.Unlock()
		cer.set(func(s *CeremonyState) { s.Step = StepDeriving })
		unl, err := cer.unlockWith(ks, nil, &h)
		if err != nil {
			cer.closeCard(card) // released before any park
			return nil, nil, err
		}
		return unl, card, nil
	}
	pw, err := cer.ask("password", StepPassword, PINStatus{})
	if err != nil {
		return nil, nil, err
	}
	cer.set(func(s *CeremonyState) { s.Step = StepDeriving })
	unl, err := ks.Unlock(keystore.PasswordCredential{Password: pw})
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
	c.mu.Unlock()
	c.emitState()
}

// BeginEnroll adds a slot after a ceremony for the VMK; for a token, the
// token flow follows (APP.md §3, Keys).
func (c *Core) BeginEnroll(o EnrollOptions) *Error {
	if o.Label == "" {
		return coded(CodeParams)
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
			pw, err := cer.askNew("password", StepPassword)
			if err != nil {
				return err
			}
			spec = keystore.PasswordSlot{Password: pw, Argon2: defaultArgon2, Label: o.Label}
		case EnrollRecovery:
			rk, err := kdf.NewRecoveryKey()
			if err != nil {
				return err
			}
			spec = keystore.RecoverySlot{Key: rk, Label: o.Label}
			if err := cer.check(); err != nil {
				return err
			}
			if err := unl.AddSlot(spec); err != nil {
				return err
			}
			// Shown once, over the one-time channel.
			url := c.preview.mintSecret(rk.Digits())
			cer.set(func(s *CeremonyState) { s.Step, s.SlotLabel = StepRecovery, url })
			c.afterMutation()
			return nil
		case EnrollToken:
			// The key that unlocked is released first — a verified card is
			// not held across a prompt — then the entangled password is
			// chosen before the key to enrol is touched: a cancel here
			// leaves nothing generated on the token.
			unlockPub := cer.releaseUnlocking()
			hs := keystore.HardwareSlot{Label: o.Label}
			if o.Entangle {
				pw, err := cer.askNew("password", StepPassword)
				if err != nil {
					return err
				}
				hs.Password, hs.Argon2 = pw, defaultArgon2
			}
			pub, err := cer.enrollToken(unlockPub)
			if err != nil {
				return err
			}
			hs.PublicKey = pub
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
func (cer *ceremony) enrollToken(unlockPub []byte) ([]byte, error) {
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
			return nil, err
		}
		cer.set(func(s *CeremonyState) { s.Step, s.RemoveLabel = StepSwapKey, "" })
	}
	reader, err := cer.waitForOneReader()
	if err != nil {
		return nil, err
	}
	card, err := cer.openCard(reader)
	if err != nil {
		return nil, err
	}
	defer cer.closeCard(card)
	keys, err := card.Keys()
	if err != nil {
		return nil, err
	}
	for _, k := range keys {
		if k.Slot == 0x9d && k.Usable {
			return k.PublicKey, nil
		}
	}
	slot, err := card.FirstEmptySlot()
	if err != nil {
		if errors.Is(err, ErrTokenFull) {
			return nil, &parkAt{StepFailed, CodeTokenFull} // parked after the card is released
		}
		return nil, err
	}
	// The management key: the PIN-protected one first, else typed as hex.
	st, err := card.PINState()
	if err != nil {
		return nil, err
	}
	if st.Blocked() {
		return nil, &parkAt{StepBlocked, CodeTokenPINBlocked}
	}
	var mgmt []byte
	pin, err := cer.ask("pin", StepPIN, st)
	if err != nil {
		return nil, err
	}
	mgmt, err = card.ProtectedManagementKey(pin)
	if err != nil {
		if errors.Is(err, ErrTokenNoProtectedKey) {
			hexKey, aerr := cer.ask("mgmtkey", StepManagementKey, PINStatus{})
			if aerr != nil {
				return nil, aerr
			}
			mgmt, err = decodeHexKey(hexKey)
			if err != nil {
				return nil, &parkAt{StepFailed, CodeTokenMgmtKey}
			}
		} else {
			return nil, err
		}
	}
	defer kdf.Zero(mgmt)
	cer.set(func(s *CeremonyState) { s.Step = StepDeriving })
	info, err := card.Generate(mgmt, GenerateOptions{Slot: slot})
	if err != nil {
		return nil, err
	}
	return info.PublicKey, nil
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
	if _, err := unl.Rotate(keystore.RotateOptions{}); err != nil {
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
		return unl.Export(path)
	})
}

// CreateVault makes a new keystore with a recovery slot and a first slot
// of the given kind, both needed for the invariant. The recovery key is
// handed to the frontend once, over the one-time channel, as the URL in
// the ceremony state. path is where the vault lives: empty for the one
// place (APP.md §2.1), else a vault kept elsewhere. The keystore is built
// as the incoming file beside its destination and installed only after
// the ceremony's latch and cancel are checked — a create cut short leaves
// nothing behind, and nothing is ever adopted whose recovery key was not
// shown — so every create ends Locked. Replacing the vault kept here, or
// any file already at the destination, needs replace; with a vault kept,
// no archive may be open.
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
	configured := c.vault.state != StateNone
	replacing := configured && (samePath(path, c.defaultVaultPath()) || samePath(path, c.vault.path))
	if configured && len(c.archives) > 0 {
		// A save from an open archive would land in the wrong registry.
		c.mu.Unlock()
		return coded(CodeArchivesOpen)
	}
	c.mu.Unlock()
	if !replace {
		// Anything at the destination — a vault, a file, something that
		// cannot even be looked at — is replaced only knowingly.
		if _, err := os.Stat(path); replacing || !errors.Is(err, fs.ErrNotExist) {
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
		spec, err := cer.firstSlotSpec(first)
		if err != nil {
			return err
		}
		if err := cer.check(); err != nil {
			return err
		}
		cer.set(func(s *CeremonyState) { s.Step = StepDeriving })
		unl, err := keystore.Create(staged, keystore.CreateOptions{Slots: []keystore.SlotSpec{keystore.RecoverySlot{Key: rk, Label: "Recovery key"}, spec}})
		if err != nil {
			return err
		}
		ks := unl.Keystore()
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
		// Installed: the key is shown whatever happens now.
		url := c.preview.mintSecret(rk.Digits())
		cer.set(func(s *CeremonyState) { s.Step, s.SlotLabel = StepRecovery, url })
		return nil
	})
	return nil
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

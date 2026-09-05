package keystore

import (
	"errors"
	"fmt"

	"github.com/dreamxwarden01/enfold/internal/format"
	"github.com/dreamxwarden01/enfold/internal/kdf"
)

// Unlocked is a keystore whose VMK is in memory. It exists for slot
// mutations, rotation and export, which need the VMK; everything else goes
// through Session, after which Close destroys the VMK (DESIGN.md §10).
//
// An Unlocked is bound to the VMK generation it opened: after a rotation —
// its own or another handle's — every mutation and Session refuses with
// ErrStale, because its VMK no longer wraps anything in the file.
type Unlocked struct {
	k        *Keystore
	vmk      [32]byte
	gen      uint64   // the generation this VMK belongs to
	slot     [16]byte // the recipient ID of the slot that opened the vault
	password []byte   // the entangled password used, normalised; nil otherwise
	tampered error    // ErrTampered when the slot region failed R25
	closed   bool
}

// Unlock opens the vault with c. It tries every active slot the credential
// fits; a slot that opens wins. Errors, in the order they are decided:
// ErrNoSlot when nothing fits; ErrPasswordRequired; ErrVerifier or ErrAuth
// when the slot does not open; ErrStale when it opened to a VMK behind the
// superblock's generation (§6.2); a corruption error when the registry does
// not decrypt under the recovered VMK. A slot region that fails R25 does not
// stop the unlock: see Unlocked.Tampered.
func (k *Keystore) Unlock(c Credential) (*Unlocked, error) {
	if err := k.usable(); err != nil {
		return nil, err
	}
	var (
		vmk     [32]byte
		gen     uint64
		opened  *format.SlotRecord
		lastErr error = ErrNoSlot
	)
	defer kdf.Zero(vmk[:]) // the Unlocked below holds its own copy
	for i := range k.slots {
		s := &k.slots[i]
		if s.State != format.SlotActive {
			continue
		}
		v, g, err := openSlot(s, c, k.sb.VaultID)
		if err == nil {
			vmk, gen, opened = v, g, s
			break
		}
		if !errors.Is(err, ErrNoSlot) {
			lastErr = err
		}
	}
	if opened == nil {
		return nil, lastErr
	}
	switch {
	case gen < k.sb.VMKGeneration:
		return nil, fmt.Errorf("%w: slot holds generation %d, vault is at %d", ErrStale, gen, k.sb.VMKGeneration)
	case gen > k.sb.VMKGeneration:
		return nil, corrupt("slot holds generation %d, ahead of the superblock's %d", gen, k.sb.VMKGeneration)
	}
	meta := kdf.MetadataKey(vmk, k.sb.VaultID)
	reg, err := k.openRegistry(meta)
	kdf.Zero(meta)
	if err != nil {
		if errors.Is(err, ErrAuth) {
			return nil, corrupt("registry does not decrypt under the VMK this slot holds")
		}
		return nil, err
	}
	k.reg = reg
	u := &Unlocked{k: k, vmk: vmk, gen: gen, slot: opened.RecipientID}
	if err := reg.VerifySlotRegion(k.region); err != nil {
		u.tampered = fmt.Errorf("%w: %v", ErrTampered, err)
	}
	if hc, ok := c.(HardwareCredential); ok && opened.Flags&format.FlagEntangledPassword != 0 {
		u.password, _ = kdf.NormalizePassword(hc.Password)
	}
	return u, nil
}

// Keystore is the file this Unlocked was opened over. Closing it is the
// caller's job; Unlocked.Close only destroys the VMK.
func (u *Unlocked) Keystore() *Keystore { return u.k }

// Tampered is nil, or ErrTampered when the live slot region does not match
// the hash the registry authenticates (R25). The vault is readable through
// the slot that opened it, and the registry can still be updated — a
// registry write carries the authenticated hash forward, never a fresh one
// over the unverified region; AddSlot, RemoveSlot, Rotate, RewrapStale and
// Export refuse until the region is repaired.
func (u *Unlocked) Tampered() error { return u.tampered }

// Registry is the decrypted registry, shared with every handle over the same
// Keystore. Mutate it only through UpdateRegistry.
func (u *Unlocked) Registry() *format.Registry { return u.k.reg }

// OpenedBy is the recipient ID of the slot that opened the vault; zero for
// the Unlocked that Create returns.
func (u *Unlocked) OpenedBy() [16]byte { return u.slot }

// current refuses an Unlocked that is closed, or whose VMK a rotation has
// left behind.
func (u *Unlocked) current() error {
	if u.closed {
		return ErrClosed
	}
	if err := u.k.usable(); err != nil {
		return err
	}
	if u.gen != u.k.sb.VMKGeneration {
		return fmt.Errorf("%w: this handle holds generation %d, vault is at %d", ErrStale, u.gen, u.k.sb.VMKGeneration)
	}
	return nil
}

// Session derives the cached keys of DESIGN.md §10 from the VMK. The
// Unlocked stays usable until Close; call Close as soon as no mutation is
// pending, so that the VMK is gone while the session lives. A Session is
// bound to the generation it was derived at, like the Unlocked.
func (u *Unlocked) Session() (*Session, error) {
	if err := u.current(); err != nil {
		return nil, err
	}
	vaultID := u.k.sb.VaultID
	return &Session{
		k:    u.k,
		gen:  u.gen,
		kwk:  kdf.KWK(u.vmk, vaultID),
		meta: kdf.MetadataKey(u.vmk, vaultID),
		db:   kdf.DBKey(u.vmk, vaultID),
	}, nil
}

// Close destroys the VMK and the entangled password.
func (u *Unlocked) Close() {
	if u.closed {
		return
	}
	u.closed = true
	kdf.Zero(u.vmk[:])
	kdf.Zero(u.password)
	u.password = nil
}

// Session is the unlocked vault as the rest of the program sees it: the
// three cached keys and the registry, with the VMK gone. It is not safe for
// concurrent use. After a rotation its keys are those of the previous VMK,
// and every operation refuses with ErrStale: derive a new Session from a
// fresh Unlocked.
type Session struct {
	k      *Keystore
	gen    uint64
	kwk    []byte
	meta   []byte
	db     []byte
	locked bool
}

// live refuses a Session that is locked, whose keystore is unusable, or
// whose keys a rotation has left behind.
func (s *Session) live() error {
	if s.locked {
		return ErrClosed
	}
	if err := s.k.usable(); err != nil {
		return err
	}
	if s.gen != s.k.sb.VMKGeneration {
		return fmt.Errorf("%w: session holds generation %d, vault is at %d", ErrStale, s.gen, s.k.sb.VMKGeneration)
	}
	return nil
}

// Keystore is the file this Session runs over. Closing it is the caller's
// job; Lock only destroys the cached keys.
func (s *Session) Keystore() *Keystore { return s.k }

// Registry is the current registry, shared with every handle over the same
// Keystore. Mutate it only through UpdateRegistry.
func (s *Session) Registry() *format.Registry { return s.k.reg }

// DBKey is the key for local caches (§3); nil once the Session is locked or
// stale.
func (s *Session) DBKey() []byte {
	if s.live() != nil {
		return nil
	}
	return s.db
}

// UpdateRegistry applies fn to a copy of the registry and commits the
// result in one superblock flip. fn returning an error abandons the change.
// The slot region is untouched and its authenticated hash carried over.
func (s *Session) UpdateRegistry(fn func(*format.Registry) error) error {
	if err := s.live(); err != nil {
		return err
	}
	return updateRegistry(s.k, s.meta, fn)
}

// updateRegistry is the one path a registry change takes: clone the
// Keystore's registry, let fn edit the clone, validate, commit. The commit
// installs the clone as the Keystore's registry.
func updateRegistry(k *Keystore, meta []byte, fn func(*format.Registry) error) error {
	next, err := cloneRegistry(k.reg)
	if err != nil {
		return err
	}
	if err := fn(next); err != nil {
		return err
	}
	if err := next.Validate(); err != nil {
		return err
	}
	return k.commit(txn{reg: next, meta: meta, gen: k.sb.VMKGeneration, pending: k.sb.RotationPending != 0})
}

// cloneRegistry deep-copies a registry through its own codec, so that a
// failed update leaves the current one untouched.
func cloneRegistry(g *format.Registry) (*format.Registry, error) {
	b, err := g.Encode()
	if err != nil {
		return nil, err
	}
	defer kdf.Zero(b)
	return format.DecodeRegistry(b)
}

// UnwrapArchiveKey opens the archive key of one version record under KWK
// (§7.2, R22).
func (s *Session) UnwrapArchiveKey(archiveID [16]byte, v *format.VersionRecord) ([32]byte, error) {
	if err := s.live(); err != nil {
		return [32]byte{}, err
	}
	key, err := kdf.UnwrapKey(s.kwk, v.WrappedArchiveKey, v.WrapNonce, format.ArchiveKeyAAD(archiveID, v.KID))
	if err != nil {
		return [32]byte{}, fmt.Errorf("%w: archive %x version %x", ErrAuth, archiveID, v.KID)
	}
	return key, nil
}

// WrapArchiveKey seals an archive key for a new version record under KWK.
func (s *Session) WrapArchiveKey(archiveID, kid [16]byte, key [32]byte) (wrapped [format.WrappedKeySize]byte, nonce [format.NonceSize]byte, err error) {
	if err := s.live(); err != nil {
		return wrapped, nonce, err
	}
	return kdf.WrapKey(s.kwk, key, format.ArchiveKeyAAD(archiveID, kid))
}

// Lock destroys the cached keys. The Session is unusable afterwards.
func (s *Session) Lock() {
	if s.locked {
		return
	}
	s.locked = true
	kdf.Zero(s.kwk)
	kdf.Zero(s.meta)
	kdf.Zero(s.db)
	s.kwk, s.meta, s.db = nil, nil, nil
}

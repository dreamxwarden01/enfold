package keystore

import (
	"crypto/subtle"
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
	k    *Keystore
	vmk  [32]byte
	gen  uint64   // the generation this VMK belongs to
	slot [16]byte // the recipient ID of the slot that opened the vault
	// kp is the vault's K_P as this handle read it from the kind-2 secrets
	// record, nil while the header's entangle is 0. It is present whatever
	// credential opened the vault — which is what lets a key be enrolled, the
	// password changed and the VMK rotated from a recovery-key or standalone
	// password way in, with no token and no password typed (§3.1, §18.1). It is
	// this handle's copy and no more: a change on another handle over the same
	// file replaces the record without moving the generation, so every wrap
	// re-reads the record (Unlocked.entangleKey) instead of trusting this.
	kp       *[32]byte
	tampered error // ErrTampered when the slot region failed R25
	closed   bool
}

// entangledKey is kdf.EntangledKey behind a variable so that a test can count
// how often the package runs Argon2id. K_P depends on the password and the slot
// region header alone (§3.1), so it is derived once per unlock, outside the
// slot loop, and never once per slot — and once per set or change, in newHeader,
// which is the only other site. Every K_P derivation in the package goes
// through it, so a count of it is a count of them.
var entangledKey = kdf.EntangledKey

// Unlock opens the vault with c. It tries every active slot the credential
// fits; a slot that opens wins. Errors, in the order they are decided:
// ErrPasswordRequired, decided from the slot region header before any token is
// touched (§6); ErrNoSlot when nothing fits; ErrVerifier or ErrAuth when the
// slot does not open; ErrTampered when the slot opened to a generation that is
// not the superblock's, in either direction (§6.2); a corruption error when
// the registry does not decrypt under the recovered VMK. A slot region that
// fails R25 does not stop the unlock: see Unlocked.Tampered.
func (k *Keystore) Unlock(c Credential) (*Unlocked, error) {
	if err := k.usable(); err != nil {
		return nil, err
	}
	vaultID := k.sb.VaultID

	// K_P first, once: it depends on the password and the header, not on the
	// slot, so deriving it inside the loop would run Argon2id once per slot
	// (§3.1). The header's parameters were checked against R24 when the region
	// decoded, so no hostile header reaches the allocation.
	var kp *[32]byte
	if hc, ok := c.(HardwareCredential); ok && k.hdr.Entangle {
		// NormalizePassword's only error is the empty result (R4), and a
		// password that normalises away is as absent as one never typed.
		pw, err := kdf.NormalizePassword(hc.Password)
		if err != nil {
			return nil, ErrPasswordRequired
		}
		derived, err := entangledKey(pw, kdf.EntangleSalt(k.hdr.EntangleSalt), vaultID,
			kdf.Argon2Params{MemKiB: k.hdr.Argon2M, Time: k.hdr.Argon2T, Threads: k.hdr.Argon2P})
		kdf.Zero(pw)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrParams, err)
		}
		// The kept copy is the authoritative one; this one is only compared
		// with it and then gone.
		defer kdf.Zero(derived[:])
		kp = &derived
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
		v, g, err := openSlot(s, c, kp, vaultID)
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
	// Both directions are a verdict, not a diagnosis (§6.2, §18.1): no slot
	// can be behind a rotation any more, so a recovered generation that is not
	// the superblock's means the region and the superblock do not belong
	// together — a spliced or rolled-back region.
	switch {
	case gen < k.sb.VMKGeneration:
		return nil, fmt.Errorf("%w: the slot holds generation %d, the superblock %d: a rolled-back or spliced slot region", ErrTampered, gen, k.sb.VMKGeneration)
	case gen > k.sb.VMKGeneration:
		return nil, fmt.Errorf("%w: the slot holds generation %d, ahead of the superblock's %d: a spliced slot region", ErrTampered, gen, k.sb.VMKGeneration)
	}
	meta := kdf.MetadataKey(vmk, vaultID)
	reg, err := k.openRegistry(meta)
	kdf.Zero(meta)
	if err != nil {
		if errors.Is(err, ErrAuth) {
			return nil, corrupt("registry does not decrypt under the VMK this slot holds")
		}
		return nil, err
	}

	// Four fail-closed checks the format layer cannot make: it sees one
	// structure at a time and never the slot region beside the registry.
	//
	// 1. The registry agrees with the header it was written beside (§7.6).
	// The header is the unauthenticated side, so a disagreement is a
	// statement about it.
	if err := reg.CheckEntangleAgreement(k.hdr.Entangle); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTampered, err)
	}
	// 2 and 3. No vmk_history record carries the current generation, and
	// none carries one above it (§18.2): the history is what the vault has
	// retired, and a record at or ahead of the live generation is a registry
	// this writer could not have produced.
	for i := range reg.Secrets {
		g, ok := reg.Secrets[i].HistoryGeneration()
		if !ok {
			continue
		}
		if g >= k.sb.VMKGeneration {
			return nil, corrupt("the secrets section keeps a vmk_history record for generation %d, at or above the vault's %d", g, k.sb.VMKGeneration)
		}
	}
	// 4. The kept K_P is authoritative; a derived one must equal it. They can
	// differ only if the header and the authenticated registry disagree, so a
	// mismatch is a statement about the header (§3.1, §18.1).
	var kept *[32]byte
	if k.hdr.Entangle {
		rec := reg.Secret(format.SecretEntangledKey, [16]byte{})
		if rec == nil { // CheckEntangleAgreement above already refused this
			return nil, corrupt("the registry keeps no entangled_key record")
		}
		kwks := kdf.KWKSecrets(vmk, vaultID)
		pt, err := openSecretWith(kwks, vaultID, rec)
		kdf.Zero(kwks)
		if err != nil {
			return nil, corrupt("the entangled_key record does not open under KWK_secrets")
		}
		kept = &pt
	}
	if kp != nil && (kept == nil || subtle.ConstantTimeCompare(kp[:], kept[:]) != 1) {
		if kept != nil {
			kdf.Zero(kept[:])
		}
		return nil, fmt.Errorf("%w: the K_P the header derives is not the one the registry keeps", ErrTampered)
	}

	k.reg = reg
	u := &Unlocked{k: k, vmk: vmk, gen: gen, slot: opened.RecipientID, kp: kept}
	if err := reg.VerifySlotRegion(k.region); err != nil {
		u.tampered = fmt.Errorf("%w: %v", ErrTampered, err)
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
// over the unverified region; AddSlot, RemoveSlot, Rotate, Export and every
// change to the header's entangle byte refuse until the region is repaired.
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

// Close destroys the VMK and the vault's K_P.
func (u *Unlocked) Close() {
	if u.closed {
		return
	}
	u.closed = true
	kdf.Zero(u.vmk[:])
	if u.kp != nil {
		kdf.Zero(u.kp[:])
		u.kp = nil
	}
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

// UpdateRegistryAt is UpdateRegistry with the modified_at this commit will
// carry handed to fn — the same value the superblock and the registry record.
// A decision that depends on it (§18.2: what a write may drop, and the
// forgotten_at a write stores) is then made against the value that lands, never
// against a second reading of the clock.
func (s *Session) UpdateRegistryAt(fn func(g *format.Registry, modifiedAt int64) error) error {
	if err := s.live(); err != nil {
		return err
	}
	return updateRegistryAt(s.k, s.meta, fn)
}

// updateRegistry is the one path a registry change takes: clone the
// Keystore's registry, let fn edit the clone, validate, commit. The commit
// installs the clone as the Keystore's registry.
func updateRegistry(k *Keystore, meta []byte, fn func(*format.Registry) error) error {
	return updateRegistryAt(k, meta, func(g *format.Registry, _ int64) error { return fn(g) })
}

// updateRegistryAt is updateRegistry with the commit's modified_at computed
// once, before fn runs, and carried into the commit as tx.at — so what fn was
// told and what the file records are one value (R35, §18.2).
func updateRegistryAt(k *Keystore, meta []byte, fn func(*format.Registry, int64) error) error {
	at := stamp(k.sb.ModifiedAt)
	next, err := cloneRegistry(k.reg)
	if err != nil {
		return err
	}
	if err := fn(next, at); err != nil {
		return err
	}
	if err := next.Validate(); err != nil {
		return err
	}
	return k.commit(txn{reg: next, meta: meta, gen: k.sb.VMKGeneration, at: at})
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

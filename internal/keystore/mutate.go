package keystore

import (
	"crypto/ecdh"
	"crypto/rand"
	"errors"
	"fmt"
	"os"

	"github.com/dreamxwarden01/enfold/internal/format"
	"github.com/dreamxwarden01/enfold/internal/kdf"
)

// CreateOptions configures a new keystore.
type CreateOptions struct {
	// Slots are the initial slots; together they must satisfy the invariant.
	Slots []SlotSpec
	// DeviceID is this replica's identity (SYNC.md); zero draws a random one.
	DeviceID [16]byte
}

// Create writes a new keystore at path, which must not exist, and returns it
// unlocked. It draws the VMK, the vault ID and the device identity key, and
// wraps the VMK into every slot in opts.
func Create(path string, opts CreateOptions) (*Unlocked, error) {
	var vaultID [16]byte
	if _, err := rand.Read(vaultID[:]); err != nil {
		return nil, err
	}
	var vmk [32]byte
	if _, err := rand.Read(vmk[:]); err != nil {
		return nil, err
	}
	const gen = 1
	slots := make([]format.SlotRecord, 0, len(opts.Slots))
	for _, spec := range opts.Slots {
		s, err := newRecord(spec, vaultID, vmk, gen)
		if err != nil {
			kdf.Zero(vmk[:])
			return nil, err
		}
		slots = append(slots, s)
	}
	if err := checkInvariant(slots); err != nil {
		kdf.Zero(vmk[:])
		return nil, err
	}

	reg := &format.Registry{DeviceID: opts.DeviceID}
	if reg.DeviceID == [16]byte{} {
		if _, err := rand.Read(reg.DeviceID[:]); err != nil {
			kdf.Zero(vmk[:])
			return nil, err
		}
	}
	identity, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		kdf.Zero(vmk[:])
		return nil, err
	}
	kwkID := kdf.KWKIdentity(vmk, vaultID)
	reg.WrappedIdentityKey, reg.IdentityNonce, err = kdf.WrapKey(kwkID, [32]byte(identity.Bytes()), format.IdentityKeyAAD(vaultID, reg.DeviceID))
	kdf.Zero(kwkID)
	if err != nil {
		kdf.Zero(vmk[:])
		return nil, err
	}
	meta := kdf.MetadataKey(vmk, vaultID)
	defer kdf.Zero(meta)
	k, err := create(path, vaultID, slots, reg, meta, gen)
	if err != nil {
		kdf.Zero(vmk[:])
		return nil, err
	}
	// Re-read the registry through the file, so that what the caller holds is
	// exactly what a later Open will see.
	k.reg, err = k.openRegistry(meta)
	if err != nil {
		kdf.Zero(vmk[:])
		k.Close()
		os.Remove(path)
		return nil, err
	}
	return &Unlocked{k: k, vmk: vmk, gen: gen}, nil
}

// mutable refuses every slot mutation while the Unlocked is closed, stale or
// the slot region is not what the registry says it is (R25, trap 15).
func (u *Unlocked) mutable() error {
	if err := u.current(); err != nil {
		return err
	}
	if u.tampered != nil {
		return u.tampered
	}
	return nil
}

// AddSlot wraps the VMK into a new slot and commits. The result must satisfy
// the invariant, and a token may be enrolled once.
func (u *Unlocked) AddSlot(spec SlotSpec) error {
	if err := u.mutable(); err != nil {
		return err
	}
	if hs, ok := spec.(HardwareSlot); ok {
		for i := range u.k.slots {
			if u.k.slots[i].State == format.SlotActive && u.k.slots[i].Type == format.SlotExternalECDH &&
				string(u.k.slots[i].SlotPubkey) == string(hs.PublicKey) {
				return ErrDuplicate
			}
		}
	}
	s, err := newRecord(spec, u.k.sb.VaultID, u.vmk, u.gen)
	if err != nil {
		return err
	}
	for i := range u.k.slots {
		if u.k.slots[i].RecipientID == s.RecipientID {
			return ErrDuplicate
		}
	}
	slots := append(cloneSlots(u.k.slots), s)
	if err := checkInvariant(slots); err != nil {
		return err
	}
	return u.commitSlots(slots, u.k.sb.RotationPending != 0)
}

// RemoveSlot deletes the slot with this recipient ID and commits. Removal
// revokes nothing by itself (DESIGN.md §5); the caller offers Rotate, with
// rotation pre-selected.
func (u *Unlocked) RemoveSlot(recipientID [16]byte) error {
	if err := u.mutable(); err != nil {
		return err
	}
	slots := cloneSlots(u.k.slots)
	idx := -1
	for i := range slots {
		if slots[i].RecipientID == recipientID {
			idx = i
		}
	}
	if idx < 0 {
		return ErrNotFound
	}
	slots = append(slots[:idx], slots[idx+1:]...)
	if err := checkInvariant(slots); err != nil {
		return err
	}
	pending := false
	for i := range slots {
		pending = pending || slots[i].Flags&format.FlagRewrapStale != 0
	}
	return u.commitSlots(slots, pending)
}

// RotateOptions configures Rotate.
type RotateOptions struct {
	// SharedPassword says that every hardware slot with an entangled password
	// uses the password that just unlocked the vault, so that Rotate may
	// re-wrap them all with it. Nothing in the file can check that: a slot
	// whose password is in fact different would from then on open with the
	// unlocking password instead. Left false, only the slot that opened the
	// vault is re-wrapped and the others are left stale for RewrapStale.
	SharedPassword bool
}

// Rotate performs the VMK rotation of §8: a fresh VMK and generation, every
// archive key and the identity key re-wrapped under the new subordinate
// keys, and the new VMK wrapped into every active slot from its stored
// public keys. Software slots and hardware slots without a password need no
// credential; a hardware slot with an entangled password needs that
// password, which only the slot that opened the vault is known to have (see
// RotateOptions). The slots left stale are returned; RotationPending stays
// set until RewrapStale has brought each of them up to date. Everything
// lands in one superblock flip.
func (u *Unlocked) Rotate(opts RotateOptions) ([]SlotInfo, error) {
	if err := u.mutable(); err != nil {
		return nil, err
	}
	vaultID := u.k.sb.VaultID
	var newVMK [32]byte
	if _, err := rand.Read(newVMK[:]); err != nil {
		return nil, err
	}
	// Zeroed on every path; on success u.vmk holds its own copy by then.
	defer kdf.Zero(newVMK[:])
	newGen := u.gen + 1

	// Registry: archive keys and the identity key under the new KWKs.
	reg, err := cloneRegistry(u.k.reg)
	if err != nil {
		return nil, err
	}
	kwk, kwkNew := kdf.KWK(u.vmk, vaultID), kdf.KWK(newVMK, vaultID)
	defer kdf.Zero(kwk)
	defer kdf.Zero(kwkNew)
	for a := range reg.Archives {
		ar := &reg.Archives[a]
		for v := range ar.Versions {
			ver := &ar.Versions[v]
			aad := format.ArchiveKeyAAD(ar.ArchiveID, ver.KID)
			key, err := kdf.UnwrapKey(kwk, ver.WrappedArchiveKey, ver.WrapNonce, aad)
			if err != nil {
				return nil, corrupt("archive %x version %x does not unwrap under the current KWK", ar.ArchiveID, ver.KID)
			}
			ver.WrappedArchiveKey, ver.WrapNonce, err = kdf.WrapKey(kwkNew, key, aad)
			kdf.Zero(key[:])
			if err != nil {
				return nil, err
			}
		}
	}
	idAAD := format.IdentityKeyAAD(vaultID, reg.DeviceID)
	kwkID, kwkIDNew := kdf.KWKIdentity(u.vmk, vaultID), kdf.KWKIdentity(newVMK, vaultID)
	defer kdf.Zero(kwkID)
	defer kdf.Zero(kwkIDNew)
	identity, err := kdf.UnwrapKey(kwkID, reg.WrappedIdentityKey, reg.IdentityNonce, idAAD)
	if err != nil {
		return nil, corrupt("identity key does not unwrap under the current KWK_identity")
	}
	reg.WrappedIdentityKey, reg.IdentityNonce, err = kdf.WrapKey(kwkIDNew, identity, idAAD)
	kdf.Zero(identity[:])
	if err != nil {
		return nil, err
	}

	// Slots: the new VMK into every active one, from stored public keys.
	slots := cloneSlots(u.k.slots)
	var stale []SlotInfo
	for i := range slots {
		s := &slots[i]
		if s.State != format.SlotActive {
			continue
		}
		var pw []byte
		if s.RecipientID == u.slot || opts.SharedPassword {
			pw = u.password
		}
		err := rewrap(s, pw, vaultID, newVMK, newGen)
		if err == nil {
			s.Flags &^= format.FlagRewrapStale
			continue
		}
		if !errors.Is(err, ErrPasswordRequired) {
			return nil, err
		}
		// Left exactly as it is: it still holds the previous VMK (§8).
		s.Flags |= format.FlagRewrapStale
		stale = append(stale, infoOf(s))
	}

	metaNew := kdf.MetadataKey(newVMK, vaultID)
	defer kdf.Zero(metaNew)
	if err := u.k.commit(txn{slots: slots, reg: reg, meta: metaNew, gen: newGen, pending: len(stale) > 0}); err != nil {
		return nil, err
	}
	kdf.Zero(u.vmk[:])
	u.vmk, u.gen = newVMK, newGen
	return stale, nil
}

// RewrapStale brings one slot a rotation left behind up to date. c is that
// slot's own credential: it is checked against the slot's existing wrap —
// the previous VMK, which is what a stale slot holds — so that a wrong
// password cannot be written in, and only then is the current VMK wrapped
// into the slot. For a hardware slot that means the token must be present.
// The slots still stale afterwards are returned.
func (u *Unlocked) RewrapStale(c Credential) ([]SlotInfo, error) {
	if err := u.mutable(); err != nil {
		return nil, err
	}
	vaultID := u.k.sb.VaultID
	slots := cloneSlots(u.k.slots)
	found := false
	var stale []SlotInfo
	for i := range slots {
		s := &slots[i]
		if s.State != format.SlotActive || s.Flags&format.FlagRewrapStale == 0 {
			continue
		}
		if found {
			stale = append(stale, infoOf(s))
			continue
		}
		old, _, err := openSlot(s, c, vaultID)
		if errors.Is(err, ErrNoSlot) {
			stale = append(stale, infoOf(s))
			continue
		}
		if err != nil {
			return nil, err
		}
		kdf.Zero(old[:])
		var pw []byte
		if hc, ok := c.(HardwareCredential); ok && s.Flags&format.FlagEntangledPassword != 0 {
			if pw, err = kdf.NormalizePassword(hc.Password); err != nil {
				return nil, fmt.Errorf("%w: %v", ErrParams, err)
			}
			defer kdf.Zero(pw)
		}
		if err := rewrap(s, pw, vaultID, u.vmk, u.gen); err != nil {
			return nil, err
		}
		s.Flags &^= format.FlagRewrapStale
		found = true
	}
	if !found {
		return stale, ErrNoSlot
	}
	return stale, u.commitSlots(slots, len(stale) > 0)
}

// rewrap wraps vmk into an existing record from its stored public keys,
// replacing epk (and, for software slots, the ML-KEM ciphertext) and the
// wrapped VMK. Nothing else in the record changes.
func rewrap(s *format.SlotRecord, pw []byte, vaultID [16]byte, vmk [32]byte, gen uint64) error {
	switch s.Type {
	case format.SlotExternalECDH:
		return wrapHardware(s, pw, vaultID, vmk, gen)
	case format.SlotStandalonePassword:
		return wrapHybrid(s, kdf.HybridPassword, vaultID, vmk, gen)
	case format.SlotRecovery:
		return wrapHybrid(s, kdf.HybridRecovery, vaultID, vmk, gen)
	}
	return corrupt("slot %x has type %d", s.RecipientID, s.Type)
}

// commitSlots writes a new slot region with the registry re-encrypted to
// carry its hash.
func (u *Unlocked) commitSlots(slots []format.SlotRecord, pending bool) error {
	reg, err := cloneRegistry(u.k.reg)
	if err != nil {
		return err
	}
	meta := kdf.MetadataKey(u.vmk, u.k.sb.VaultID)
	defer kdf.Zero(meta)
	return u.k.commit(txn{slots: slots, reg: reg, meta: meta, gen: u.gen, pending: pending})
}

// UpdateRegistry is Session.UpdateRegistry for callers still holding the
// Unlocked. It works while the slot region is tampered: the authenticated
// hash is carried forward, not recomputed.
func (u *Unlocked) UpdateRegistry(fn func(*format.Registry) error) error {
	if err := u.current(); err != nil {
		return err
	}
	meta := kdf.MetadataKey(u.vmk, u.k.sb.VaultID)
	defer kdf.Zero(meta)
	return updateRegistry(u.k, meta, fn)
}

// Export writes the backup of §15 (R28) to path, which must not exist: a
// keystore file with the same vault ID and registry whose slot region holds
// only the active recovery slots. The recovery key opens it like any
// keystore, which is how it is verified and how it is restored — open it,
// unlock with the recovery key, and enrol new slots.
func (u *Unlocked) Export(path string) error {
	if err := u.mutable(); err != nil {
		return err
	}
	var slots []format.SlotRecord
	for i := range u.k.slots {
		s := &u.k.slots[i]
		if s.State == format.SlotActive && s.Type == format.SlotRecovery && s.Flags&format.FlagRewrapStale == 0 {
			slots = append(slots, *s)
		}
	}
	if len(slots) == 0 {
		return ErrNoRecoverySlot
	}
	reg, err := cloneRegistry(u.k.reg)
	if err != nil {
		return err
	}
	meta := kdf.MetadataKey(u.vmk, u.k.sb.VaultID)
	defer kdf.Zero(meta)
	k, err := create(path, u.k.sb.VaultID, slots, reg, meta, u.gen)
	if err != nil {
		return err
	}
	return k.Close()
}

func cloneSlots(slots []format.SlotRecord) []format.SlotRecord {
	out := make([]format.SlotRecord, len(slots))
	for i := range slots {
		out[i] = slots[i]
		out[i].EPK = append([]byte(nil), slots[i].EPK...)
		out[i].SlotPubkey = append([]byte(nil), slots[i].SlotPubkey...)
		out[i].MLKEMEK = append([]byte(nil), slots[i].MLKEMEK...)
		out[i].MLKEMCT = append([]byte(nil), slots[i].MLKEMCT...)
	}
	return out
}

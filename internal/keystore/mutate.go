package keystore

import (
	"crypto/ecdh"
	"crypto/rand"
	"fmt"
	"os"
	"slices"

	"github.com/dreamxwarden01/enfold/internal/format"
	"github.com/dreamxwarden01/enfold/internal/kdf"
)

// Entangle is the vault's entangled password as a mutation carries it. The
// Argon2id parameters come from the caller — §6: "a write never takes these
// values from the file" — and are checked against R24 before anything is
// derived. The salt is never a caller's: every set and every change draws
// sixteen fresh bytes.
type Entangle struct {
	Password string
	Argon2   kdf.Argon2Params
}

// CreateOptions configures a new keystore.
type CreateOptions struct {
	// Slots are the initial slots; together they must satisfy the invariant.
	Slots []SlotSpec
	// DeviceID is this replica's identity (SYNC.md); zero draws a random one.
	DeviceID [16]byte
	// Entangle is the vault's password, or nil for a vault whose switch
	// starts off (§6, §18.1).
	Entangle *Entangle
}

// newHeader builds the slot region header a write lands (§6): the zero header
// for e nil, otherwise entangle 1 with the caller's Argon2id parameters and
// sixteen fresh random entangle_salt bytes, and the K_P they derive. The
// password is normalised and an empty one refused before anything is derived
// (R4, §3.1), which is the one call site every set, change and create shares.
func newHeader(e *Entangle, vaultID [16]byte) (format.SlotRegionHeader, *[32]byte, error) {
	var hdr format.SlotRegionHeader
	if e == nil {
		return hdr, nil, nil
	}
	pw, err := kdf.NormalizePassword(e.Password)
	if err != nil {
		return hdr, nil, fmt.Errorf("%w: %v", ErrParams, err)
	}
	defer kdf.Zero(pw)
	if err := e.Argon2.Validate(); err != nil {
		return hdr, nil, fmt.Errorf("%w: %v", ErrParams, err)
	}
	hdr = format.SlotRegionHeader{Entangle: true, Argon2M: e.Argon2.MemKiB, Argon2T: e.Argon2.Time, Argon2P: e.Argon2.Threads}
	if _, err := rand.Read(hdr.EntangleSalt[:]); err != nil {
		return format.SlotRegionHeader{}, nil, err
	}
	kp, err := entangledKey(pw, kdf.EntangleSalt(hdr.EntangleSalt), vaultID, e.Argon2)
	if err != nil {
		return format.SlotRegionHeader{}, nil, fmt.Errorf("%w: %v", ErrParams, err)
	}
	return hdr, &kp, nil
}

// Create writes a new keystore at path, which must not exist, and returns it
// unlocked. It draws the VMK, the vault ID and the device identity key, builds
// the slot region header from opts.Entangle, and wraps the VMK into every slot
// in opts.
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
	// The header comes first: every hardware slot below is wrapped from the
	// K_P it derives, and checkInvariant is evaluated over its entangle byte.
	hdr, kp, err := newHeader(opts.Entangle, vaultID)
	if err != nil {
		kdf.Zero(vmk[:])
		return nil, err
	}
	// K_P is zeroed on every error path, as the VMK already is.
	fail := func(err error) (*Unlocked, error) {
		kdf.Zero(vmk[:])
		if kp != nil {
			kdf.Zero(kp[:])
		}
		return nil, err
	}

	reg := &format.Registry{DeviceID: opts.DeviceID}
	slots := make([]format.SlotRecord, 0, len(opts.Slots))
	for _, spec := range opts.Slots {
		s, err := newRecord(spec, vaultID, kp, vmk, gen)
		if err != nil {
			return fail(err)
		}
		slots = append(slots, s)
		if rs, ok := spec.(RecoverySlot); ok {
			rec, err := secretRecord(vmk, vaultID, format.SecretRecoveryEscrow, s.RecipientID, rs.Key.Padded())
			if err != nil {
				return fail(err)
			}
			reg.SetSecret(rec)
		}
	}
	if kp != nil {
		rec, err := secretRecord(vmk, vaultID, format.SecretEntangledKey, [16]byte{}, *kp)
		if err != nil {
			return fail(err)
		}
		reg.SetSecret(rec)
	}
	if err := checkInvariant(hdr, slots); err != nil {
		return fail(err)
	}

	if reg.DeviceID == [16]byte{} {
		if _, err := rand.Read(reg.DeviceID[:]); err != nil {
			return fail(err)
		}
	}
	identity, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return fail(err)
	}
	kwkID := kdf.KWKIdentity(vmk, vaultID)
	reg.WrappedIdentityKey, reg.IdentityNonce, err = kdf.WrapKey(kwkID, [32]byte(identity.Bytes()), format.IdentityKeyAAD(vaultID, reg.DeviceID))
	kdf.Zero(kwkID)
	if err != nil {
		return fail(err)
	}
	meta := kdf.MetadataKey(vmk, vaultID)
	defer kdf.Zero(meta)
	k, err := create(path, vaultID, hdr, slots, reg, meta, gen, 0)
	if err != nil {
		return fail(err)
	}
	// Re-read the registry through the file, so that what the caller holds is
	// exactly what a later Open will see.
	k.reg, err = k.openRegistry(meta)
	if err != nil {
		k.Close()
		os.Remove(path)
		return fail(err)
	}
	// The Unlocked carries the K_P just derived — the same value the kind-2
	// record holds — so an entangled create can enrol, rotate or change the
	// switch at once.
	return &Unlocked{k: k, vmk: vmk, gen: gen, kp: kp}, nil
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
// the invariant, and a token may be enrolled once. A hardware slot is wrapped
// from the vault's kept K_P with no token present and no password typed
// (§18.1); the header is copied forward.
func (u *Unlocked) AddSlot(spec SlotSpec) error {
	if err := u.mutable(); err != nil {
		return err
	}
	// K_P comes from the live registry, never from this handle's own copy:
	// another handle over the same file may have changed the vault's password
	// or turned the switch off since this one opened, without moving the
	// generation that would make this handle stale. The kind-2 record is the
	// authoritative copy (§18.1, §7.6), so wrapping from anything else could
	// commit an active slot no credential opens.
	kp, err := u.entangleKey()
	if err != nil {
		return err
	}
	if kp != nil {
		defer kdf.Zero(kp[:])
	}
	if err := requireEntangleKey(u.k.hdr, kp); err != nil {
		return err
	}
	if hs, ok := spec.(HardwareSlot); ok {
		for i := range u.k.slots {
			if u.k.slots[i].State != format.SlotEmpty && u.k.slots[i].Type == format.SlotExternalECDH &&
				string(u.k.slots[i].SlotPubkey) == string(hs.PublicKey) {
				return ErrDuplicate
			}
		}
	}
	s, err := newRecord(spec, u.k.sb.VaultID, kp, u.vmk, u.gen)
	if err != nil {
		return err
	}
	for i := range u.k.slots {
		if u.k.slots[i].RecipientID == s.RecipientID {
			return ErrDuplicate
		}
	}
	slots := append(cloneSlots(u.k.slots), s)
	if err := checkInvariant(u.k.hdr, slots); err != nil {
		return err
	}
	var secret *format.SecretRecord
	if rs, ok := spec.(RecoverySlot); ok {
		rec, err := secretRecord(u.vmk, u.k.sb.VaultID, format.SecretRecoveryEscrow, s.RecipientID, rs.Key.Padded())
		if err != nil {
			return err
		}
		secret = &rec
	}
	return u.commitSlots(u.k.hdr, slots, func(reg *format.Registry) {
		if secret != nil {
			reg.SetSecret(*secret)
		}
	})
}

// AddFirstWayIn is the first way in of an adopted backup: the slot and the
// vault's entanglement in ONE commit (§15, R28). An export carries no K_P and
// its header says entangle 0, so adopting one chooses the entanglement afresh —
// the password typed at that moment writes a fresh entangle_salt, a new K_P and
// the header's entangle byte in the same flip as the slot it enrols. e nil
// takes the first way in with the switch off.
func (u *Unlocked) AddFirstWayIn(spec SlotSpec, e *Entangle) error {
	if err := u.mutable(); err != nil {
		return err
	}
	// The precondition, enforced rather than assumed: an adopted backup's
	// header carries no entanglement at all (R28, §15). This call redraws the
	// header, so on a vault whose switch is already on it would turn the
	// vault's password off — or redraw its salt — as a side effect of enrolling
	// a slot, and turning the switch off only shrinks §6.4's sets, so nothing
	// below would refuse it. The switch is SetEntangled's to move (§18.1).
	if u.k.hdr != (format.SlotRegionHeader{}) {
		return fmt.Errorf("%w: AddFirstWayIn is the first way in of an adopted backup, whose header carries no entanglement", ErrParams)
	}
	vaultID := u.k.sb.VaultID
	// The header first: a hardware slot is wrapped from the K_P it derives, and
	// the invariant below is evaluated over its entangle byte.
	hdr, kp, err := newHeader(e, vaultID)
	if err != nil {
		return err
	}
	fail := func(err error) error {
		if kp != nil {
			kdf.Zero(kp[:])
		}
		return err
	}
	if hs, ok := spec.(HardwareSlot); ok {
		for i := range u.k.slots {
			if u.k.slots[i].State != format.SlotEmpty && u.k.slots[i].Type == format.SlotExternalECDH &&
				string(u.k.slots[i].SlotPubkey) == string(hs.PublicKey) {
				return fail(ErrDuplicate)
			}
		}
	}
	s, err := newRecord(spec, vaultID, kp, u.vmk, u.gen)
	if err != nil {
		return fail(err)
	}
	for i := range u.k.slots {
		if u.k.slots[i].RecipientID == s.RecipientID {
			return fail(ErrDuplicate)
		}
	}
	slots := append(cloneSlots(u.k.slots), s)
	var secret *format.SecretRecord
	if rs, ok := spec.(RecoverySlot); ok {
		rec, err := secretRecord(u.vmk, vaultID, format.SecretRecoveryEscrow, s.RecipientID, rs.Key.Padded())
		if err != nil {
			return fail(err)
		}
		secret = &rec
	}
	// The new record is already wrapped for this header, so it is the one slot
	// writeEntangle does not re-wrap.
	if err := u.writeEntangle(hdr, kp, slots, len(slots)-1, secret); err != nil {
		return fail(err)
	}
	u.installKP(kp)
	return nil
}

// SetEntangled turns the vault's password on or off in one flip (§18.1).
//
// On: sixteen fresh entangle_salt bytes, a new K_P, every ACTIVE hardware slot
// re-wrapped offline from its stored slot_pubkey, and the entangled_key record
// written — all in the same commit as the header, so the two can never disagree
// on disk (§7.6). Refused with ErrInvariant when §6.4 would break, before the
// header is written and before the password is used.
//
// Off (e nil): the header's five fields zeroed, the entangled_key record
// dropped, every active hardware slot re-wrapped with pre = H; never refused.
//
// The old password is not asked in either direction and cannot be: nothing in
// the call takes it. K_P is reached through the VMK this Unlocked already
// holds, whatever credential opened the vault (APP.md §13).
func (u *Unlocked) SetEntangled(on bool, e *Entangle) error {
	if err := u.mutable(); err != nil {
		return err
	}
	switch {
	case on && e == nil:
		return fmt.Errorf("%w: turning the vault's password on needs the new password", ErrParams)
	case !on && e != nil:
		return fmt.Errorf("%w: turning the vault's password off takes no password", ErrParams)
	}
	// §6.4 over the sets the header this commit would write makes, before
	// anything is written and before the password is used: only the entangle
	// byte enters the predicate (secrets), so the verdict is known before a salt
	// is drawn and before Argon2id runs. Turning the switch off only ever
	// shrinks the sets, so that direction cannot be refused here.
	if err := checkInvariant(format.SlotRegionHeader{Entangle: on}, u.k.slots); err != nil {
		return err
	}
	hdr, kp, err := newHeader(e, u.k.sb.VaultID)
	if err != nil {
		return err
	}
	if err := u.writeEntangle(hdr, kp, cloneSlots(u.k.slots), -1, nil); err != nil {
		if kp != nil {
			kdf.Zero(kp[:])
		}
		return err
	}
	u.installKP(kp)
	return nil
}

// ChangeEntangledPassword is SetEntangled's act on a vault whose switch is
// already on: a fresh entangle_salt, a new K_P, every active hardware slot
// re-wrapped and the entangled_key record replaced, in one flip. Refused with
// ErrParams when the switch is off, so a salt redraw cannot happen by the wrong
// button. The old password is never a parameter (APP.md §13).
func (u *Unlocked) ChangeEntangledPassword(e Entangle) error {
	if err := u.mutable(); err != nil {
		return err
	}
	if !u.k.hdr.Entangle {
		return fmt.Errorf("%w: the vault has no entangled password to change", ErrParams)
	}
	hdr, kp, err := newHeader(&e, u.k.sb.VaultID)
	if err != nil {
		return err
	}
	if err := u.writeEntangle(hdr, kp, cloneSlots(u.k.slots), -1, nil); err != nil {
		kdf.Zero(kp[:])
		return err
	}
	u.installKP(kp)
	return nil
}

// writeEntangle is the one shape every change to the header's entangle byte
// takes: the invariant over the records the commit writes and the header it
// writes them under (§6.4), every ACTIVE hardware slot re-wrapped from kp —
// retired ones are never opened, counted or exported and keep a pre from the
// previous K_P — and the entangled_key record written or dropped in the same
// commit (§7.6). skip is the index of a record already wrapped for this header,
// or -1. Nothing reaches the file when any step fails (§1, fail closed).
func (u *Unlocked) writeEntangle(hdr format.SlotRegionHeader, kp *[32]byte, slots []format.SlotRecord, skip int, secret *format.SecretRecord) error {
	if err := checkInvariant(hdr, slots); err != nil {
		return err
	}
	if err := requireEntangleKey(hdr, kp); err != nil {
		return err
	}
	vaultID := u.k.sb.VaultID
	for i := range slots {
		s := &slots[i]
		if i == skip || s.State != format.SlotActive || s.Type != format.SlotExternalECDH {
			continue
		}
		if err := rewrap(s, kp, vaultID, u.vmk, u.gen); err != nil {
			return err
		}
	}
	var kpRec *format.SecretRecord
	if kp != nil {
		rec, err := secretRecord(u.vmk, vaultID, format.SecretEntangledKey, [16]byte{}, *kp)
		if err != nil {
			return err
		}
		kpRec = &rec
	}
	return u.commitSlots(hdr, slots, func(reg *format.Registry) {
		if secret != nil {
			reg.SetSecret(*secret)
		}
		if kpRec != nil {
			reg.SetSecret(*kpRec)
		} else {
			reg.DeleteSecret(format.SecretEntangledKey, [16]byte{})
		}
	})
}

// installKP replaces the vault's K_P on this handle once the commit that
// changed it landed, zeroing the one it retires. Called on no other path: a
// rotation re-wraps K_P, it never replaces it (§18.1).
func (u *Unlocked) installKP(kp *[32]byte) {
	if u.kp != nil {
		kdf.Zero(u.kp[:])
	}
	u.kp = kp
}

// HistoryGenerations are the VMK generations the secrets section remembers,
// ascending (§7.6, §18.2). The retired VMKs themselves never leave the package;
// this is what the vault can still open a backup of its own from. Nil for a
// handle a rotation has left behind.
func (u *Unlocked) HistoryGenerations() []uint64 {
	if u.current() != nil {
		return nil
	}
	return historyGens(u.k.reg)
}

// historyGens is the sorted generation list behind HistoryGenerations. The
// section's own order is (kind, id) over a little-endian generation, which is
// not numeric order, so the sort is not decoration (§7.6).
func historyGens(g *format.Registry) []uint64 {
	var out []uint64
	for i := range g.Secrets {
		if gen, ok := g.Secrets[i].HistoryGeneration(); ok {
			out = append(out, gen)
		}
	}
	slices.Sort(out)
	return out
}

// sealSecretWith seals one 32-byte secret under an already-derived
// KWK_secrets, with a fresh nonce and the AAD of R22 (§7.6).
func sealSecretWith(kwks []byte, vaultID [16]byte, kind format.SecretKind, id [16]byte, plain [32]byte) (format.SecretRecord, error) {
	rec := format.SecretRecord{Kind: kind, ID: id}
	var err error
	rec.Ciphertext, rec.Nonce, err = kdf.WrapKey(kwks, plain, format.SecretAAD(vaultID, kind, id))
	return rec, err
}

// openSecretWith is the inverse: the record's own kind and id build the AAD,
// so a record moved between kinds or ids fails to open (R22).
func openSecretWith(kwks []byte, vaultID [16]byte, rec *format.SecretRecord) ([32]byte, error) {
	return kdf.UnwrapKey(kwks, rec.Ciphertext, rec.Nonce, format.SecretAAD(vaultID, rec.Kind, rec.ID))
}

// entangleKey is the vault's K_P as the file keeps it right now: the plaintext
// of the entangled_key record under KWK_secrets, or nil while the live header's
// entangle is 0 (§3.1, §7.6). The record's copy is the authoritative one
// (§18.1) — a rotation re-wraps it, a change replaces it, and either may have
// happened on another handle over the same file since this one opened, with no
// change of generation to make this handle stale. Every wrap therefore reads it
// here rather than trusting a copy taken at unlock. The caller zeroes it.
func (u *Unlocked) entangleKey() (*[32]byte, error) {
	if !u.k.hdr.Entangle {
		return nil, nil
	}
	rec := u.k.reg.Secret(format.SecretEntangledKey, [16]byte{})
	if rec == nil {
		return nil, corrupt("the registry keeps no entangled_key record")
	}
	vaultID := u.k.sb.VaultID
	kwks := kdf.KWKSecrets(u.vmk, vaultID)
	defer kdf.Zero(kwks)
	pt, err := openSecretWith(kwks, vaultID, rec)
	if err != nil {
		return nil, corrupt("the entangled_key record does not open under KWK_secrets")
	}
	return &pt, nil
}

// secretRecord seals one record of the secrets section under KWK_secrets
// (§7.6, R22, R38). A recovery key reaches it through RecoveryKey.Padded,
// since a plaintext shorter than 32 bytes is zero-padded.
func secretRecord(vmk [32]byte, vaultID [16]byte, kind format.SecretKind, id [16]byte, plain [32]byte) (format.SecretRecord, error) {
	kwks := kdf.KWKSecrets(vmk, vaultID)
	defer kdf.Zero(kwks)
	return sealSecretWith(kwks, vaultID, kind, id, plain)
}

// RecoveryKey opens the recovery_escrow record of an active recovery slot
// (R38): the recovery key kept once more under the VMK, which only this
// Unlocked holds — a Session cannot. ErrNotFound when no active recovery slot
// has this recipient ID; ErrEscrowMissing when the registry keeps no record
// for it. It reads only, so a tampered slot region does not refuse it.
func (u *Unlocked) RecoveryKey(recipientID [16]byte) (kdf.RecoveryKey, error) {
	if err := u.current(); err != nil {
		return kdf.RecoveryKey{}, err
	}
	found := false
	for i := range u.k.slots {
		s := &u.k.slots[i]
		if s.RecipientID == recipientID && s.State == format.SlotActive && s.Type == format.SlotRecovery {
			found = true
		}
	}
	if !found {
		return kdf.RecoveryKey{}, ErrNotFound
	}
	rec := u.k.reg.Secret(format.SecretRecoveryEscrow, recipientID)
	if rec == nil {
		return kdf.RecoveryKey{}, ErrEscrowMissing
	}
	vaultID := u.k.sb.VaultID
	kwks := kdf.KWKSecrets(u.vmk, vaultID)
	defer kdf.Zero(kwks)
	pt, err := openSecretWith(kwks, vaultID, rec)
	if err != nil {
		return kdf.RecoveryKey{}, corrupt("the kept recovery key for slot %x does not open under the current KWK_secrets", recipientID)
	}
	r, err := kdf.RecoveryKeyFromPadded(pt)
	kdf.Zero(pt[:])
	if err != nil {
		return kdf.RecoveryKey{}, corrupt("the kept recovery key for slot %x is not zero-padded", recipientID)
	}
	// The key shown must be the slot's: derived, it gives the slot's public
	// key (§6.3), or it is not shown.
	if !u.keyFitsSlot(recipientID, r) {
		kdf.Zero(r[:])
		return kdf.RecoveryKey{}, ErrEscrowMismatch
	}
	return r, nil
}

// keyFitsSlot reports whether r derives the active recovery slot's public
// key (§6.3).
func (u *Unlocked) keyFitsSlot(recipientID [16]byte, r kdf.RecoveryKey) bool {
	for i := range u.k.slots {
		s := u.k.slots[i]
		if s.RecipientID != recipientID || s.State != format.SlotActive || s.Type != format.SlotRecovery {
			continue
		}
		skX, _, err := recoveryKeys(&s, r, u.k.sb.VaultID)
		return err == nil && kdf.VerifyX25519(skX, s.SlotPubkey)
	}
	return false
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
	if err := checkInvariant(u.k.hdr, slots); err != nil {
		return err
	}
	// A recovery slot's recovery_escrow record goes with it (commitSlots
	// prunes it); K_P and the VMK history do not (R38).
	return u.commitSlots(u.k.hdr, slots, nil)
}

// Rotate performs the VMK rotation of §8, entire and atomic: a fresh VMK and
// generation, every archive key and the identity key re-wrapped under the new
// subordinate keys, the whole secrets section re-encrypted under the new
// KWK_secrets with the retiring VMK appended as a vmk_history record, and the
// new VMK wrapped into every active slot from its stored public keys plus the
// K_P step 3 held. No credential need be present and no password typed, so a
// rotation that commits is complete: no stale slot, no partial rotation, no
// options. Everything lands in one superblock flip; anything that fails is
// abandoned before it (§1, fail closed).
func (u *Unlocked) Rotate() error {
	if err := u.mutable(); err != nil {
		return err
	}
	// No entry check over this handle's own K_P: step 3 recovers the
	// authoritative one from the secrets section and the guard below it, over
	// that value and the header the commit copies forward, is the one that
	// decides — before any slot is re-wrapped. A rotation must be possible from
	// the VMK alone, whatever the handle held when it opened (§8, "no rotation
	// is deferred").
	vaultID := u.k.sb.VaultID
	var newVMK [32]byte
	if _, err := rand.Read(newVMK[:]); err != nil {
		return err
	}
	// Zeroed on every path; on success u.vmk holds its own copy by then.
	defer kdf.Zero(newVMK[:])
	newGen := u.gen + 1

	// Step 2: every archive key under the new KWK. No archive file is
	// touched — the keys themselves are unchanged, only their wrappers.
	reg, err := cloneRegistry(u.k.reg)
	if err != nil {
		return err
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
				return corrupt("archive %x version %x does not unwrap under the current KWK", ar.ArchiveID, ver.KID)
			}
			ver.WrappedArchiveKey, ver.WrapNonce, err = kdf.WrapKey(kwkNew, key, aad)
			kdf.Zero(key[:])
			if err != nil {
				return err
			}
		}
	}

	// Step 3, the identity key under KWK_identity'.
	idAAD := format.IdentityKeyAAD(vaultID, reg.DeviceID)
	kwkID, kwkIDNew := kdf.KWKIdentity(u.vmk, vaultID), kdf.KWKIdentity(newVMK, vaultID)
	defer kdf.Zero(kwkID)
	defer kdf.Zero(kwkIDNew)
	identity, err := kdf.UnwrapKey(kwkID, reg.WrappedIdentityKey, reg.IdentityNonce, idAAD)
	if err != nil {
		return corrupt("identity key does not unwrap under the current KWK_identity")
	}
	reg.WrappedIdentityKey, reg.IdentityNonce, err = kdf.WrapKey(kwkIDNew, identity, idAAD)
	kdf.Zero(identity[:])
	if err != nil {
		return err
	}

	// Step 3, the secrets section. Pruned first, so a removal-then-rotation
	// drops the removed slot's recovery_escrow record and never carries it
	// forward (R38); kinds 2 and 3 belong to the vault and survive. Then every
	// kept record is decrypted under the retiring KWK_secrets — keeping the
	// entangled_key's plaintext as the K_P step 4 needs — and re-encrypted
	// under KWK_secrets' with a fresh nonce and its AAD unchanged. A record
	// that does not open, or one left un-re-encrypted, abandons the rotation
	// before the flip (§1, fail closed).
	pruneSecrets(reg, u.k.slots)
	kwkS, kwkSNew := kdf.KWKSecrets(u.vmk, vaultID), kdf.KWKSecrets(newVMK, vaultID)
	defer kdf.Zero(kwkS)
	defer kdf.Zero(kwkSNew)
	var kp *[32]byte
	defer func() {
		if kp != nil {
			kdf.Zero(kp[:])
		}
	}()
	for i := range reg.Secrets {
		rec := &reg.Secrets[i]
		pt, err := openSecretWith(kwkS, vaultID, rec)
		if err != nil {
			return corrupt("the secrets record of kind %d, id %x, does not unwrap under the current KWK_secrets", rec.Kind, rec.ID)
		}
		if rec.Kind == format.SecretEntangledKey {
			kept := pt
			kp = &kept
		}
		next, err := sealSecretWith(kwkSNew, vaultID, rec.Kind, rec.ID, pt)
		kdf.Zero(pt[:])
		if err != nil {
			return err
		}
		*rec = next
	}
	// The retiring VMK, keyed by the generation it held — the value before
	// step 1's increment (§8 step 3, §18.2). Two records for one generation
	// are invalid, so a section that already holds this one is refused.
	histID := format.VMKHistoryID(u.gen)
	if reg.Secret(format.SecretVMKHistory, histID) != nil {
		return corrupt("the secrets section already keeps a vmk_history record for generation %d", u.gen)
	}
	hist, err := sealSecretWith(kwkSNew, vaultID, format.SecretVMKHistory, histID, u.vmk)
	if err != nil {
		return err
	}
	reg.SetSecret(hist)
	// The header is copied forward, so its entangle byte still decides whether
	// step 4 needs K_P; the section it was read from has just been checked.
	if err := requireEntangleKey(u.k.hdr, kp); err != nil {
		return err
	}

	// Step 4: the new VMK into every active slot, from stored public keys —
	// a hardware slot from a fresh ECDH against its slot_pubkey plus that K_P,
	// a software slot from a fresh ML-KEM encapsulation. Any failure is fatal:
	// there is no password to ask for and nothing to defer (§8).
	slots := cloneSlots(u.k.slots)
	for i := range slots {
		s := &slots[i]
		if s.State != format.SlotActive {
			continue
		}
		if err := rewrap(s, kp, vaultID, newVMK, newGen); err != nil {
			return err
		}
	}

	// Step 5: the slot region, the registry, then the superblock flip. u.kp is
	// left as it was — a rotation re-wraps K_P, it never replaces it (§18.1).
	metaNew := kdf.MetadataKey(newVMK, vaultID)
	defer kdf.Zero(metaNew)
	if err := u.k.commit(txn{slots: slots, reg: reg, meta: metaNew, gen: newGen}); err != nil {
		return err
	}
	kdf.Zero(u.vmk[:])
	u.vmk, u.gen = newVMK, newGen
	return nil
}

// rewrap wraps vmk into an existing record from its stored public keys,
// replacing epk (and, for software slots, the ML-KEM ciphertext) and the
// wrapped VMK. kp is the vault's K_P, nil while the header's entangle is 0.
// Nothing else in the record changes.
func rewrap(s *format.SlotRecord, kp *[32]byte, vaultID [16]byte, vmk [32]byte, gen uint64) error {
	switch s.Type {
	case format.SlotExternalECDH:
		return wrapHardware(s, kp, vaultID, vmk, gen)
	case format.SlotStandalonePassword:
		return wrapHybrid(s, kdf.HybridPassword, vaultID, vmk, gen)
	case format.SlotRecovery:
		return wrapHybrid(s, kdf.HybridRecovery, vaultID, vmk, gen)
	}
	return corrupt("slot %x has type %d", s.RecipientID, s.Type)
}

// commitSlots writes a slot region — the header hdr and these records — with
// the registry re-encrypted to carry its hash; edit, when given, changes the
// registry copy first. A recovery_escrow record whose recovery slot is not
// among the active ones is dropped here (R38): kind 1's life is its slot's.
// commit makes the header/registry agreement check before the flip.
func (u *Unlocked) commitSlots(hdr format.SlotRegionHeader, slots []format.SlotRecord, edit func(*format.Registry)) error {
	reg, err := cloneRegistry(u.k.reg)
	if err != nil {
		return err
	}
	if edit != nil {
		edit(reg)
	}
	pruneSecrets(reg, slots)
	meta := kdf.MetadataKey(u.vmk, u.k.sb.VaultID)
	defer kdf.Zero(meta)
	return u.k.commit(txn{slots: slots, hdr: &hdr, reg: reg, meta: meta, gen: u.gen})
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

// UpdateRegistryAt is Session.UpdateRegistryAt for callers still holding the
// Unlocked: fn is handed the modified_at this commit will carry (§18.2, R35).
func (u *Unlocked) UpdateRegistryAt(fn func(g *format.Registry, modifiedAt int64) error) error {
	if err := u.current(); err != nil {
		return err
	}
	meta := kdf.MetadataKey(u.vmk, u.k.sb.VaultID)
	defer kdf.Zero(meta)
	return updateRegistryAt(u.k, meta, fn)
}

// Export writes the backup of §15 (R28) to path, which must not exist: a
// keystore file with the same vault ID, generation and registry whose slot
// region holds only the active recovery slots. The recovery key opens it like
// any keystore, which is how it is verified and how it is restored — open it,
// unlock with the recovery key, and enrol new slots.
//
// Its header is written with entangle 0, a zero entangle_salt and zero Argon2
// parameters, and adopting it chooses the entanglement afresh at its first way
// in (§15). Of the secrets section it carries the recovery_escrow records of
// exactly the slots it holds and every vmk_history record — the history is the
// vault's memory of itself, so a vault rebuilt from the export keeps it — and
// never the entangled_key: an export holds no hardware slot, and K_P survives
// every rotation, so one that carried it would hand the vault's second factor
// to whoever leaks the export, for good. Nothing is re-encrypted: the export
// carries the same generation, so the records travel verbatim under the same
// KWK_secrets.
func (u *Unlocked) Export(path string) error {
	if err := u.mutable(); err != nil {
		return err
	}
	var slots []format.SlotRecord
	for i := range u.k.slots {
		s := &u.k.slots[i]
		if s.State == format.SlotActive && s.Type == format.SlotRecovery {
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
	pruneSecrets(reg, slots) // kind 1 of the slots the export carries
	reg.DeleteSecret(format.SecretEntangledKey, [16]byte{})
	meta := kdf.MetadataKey(u.vmk, u.k.sb.VaultID)
	defer kdf.Zero(meta)
	k, err := create(path, u.k.sb.VaultID, format.SlotRegionHeader{}, slots, reg, meta, u.gen, u.k.sb.ModifiedAt)
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

// pruneSecrets keeps only the recovery_escrow records of the recovery slots
// active in slots (R38): every write of the slot region carries the kind-1
// records of exactly the region it writes, because that record's life is its
// slot's. That rule is kind 1's alone — the entangled_key and every
// vmk_history record belong to the vault, not to a slot, and no slot-region
// write ever drops one (§7.6, §18.2). The section's ascending (kind, id) order
// survives a filter, so nothing needs re-sorting.
func pruneSecrets(reg *format.Registry, slots []format.SlotRecord) {
	live := make(map[[16]byte]bool, len(slots))
	for i := range slots {
		if slots[i].State == format.SlotActive && slots[i].Type == format.SlotRecovery {
			live[slots[i].RecipientID] = true
		}
	}
	kept := reg.Secrets[:0]
	for _, s := range reg.Secrets {
		if s.Kind == format.SecretRecoveryEscrow && !live[s.ID] {
			continue
		}
		kept = append(kept, s)
	}
	reg.Secrets = kept
	if len(reg.Secrets) == 0 {
		reg.Secrets = nil
	}
}

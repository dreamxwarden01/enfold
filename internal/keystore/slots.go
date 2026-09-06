package keystore

import (
	"bytes"
	"crypto/ecdh"
	"crypto/mlkem"
	"crypto/rand"
	"errors"
	"fmt"

	"github.com/dreamxwarden01/enfold/internal/format"
	"github.com/dreamxwarden01/enfold/internal/kdf"
)

// Token is a P-256 key held elsewhere — a YubiKey's PIV slot, a phone's
// secure element — seen through the one operation the hardware slot needs.
// The PIV layer implements it; tests use a software key.
type Token interface {
	// PublicKey is the token's key in uncompressed X9.62 form (65 bytes). It
	// is what a slot record stores as slot_pubkey and what an unlock matches
	// on.
	PublicKey() []byte
	// ECDH returns the 32-byte X coordinate of the shared point with epk, an
	// uncompressed P-256 point the caller has already validated. On a token
	// this is where the PIN and touch happen; the keystore calls it once per
	// unlock, from one goroutine, and passes its error through unchanged.
	// A Token may stop working once whoever made it closes the connection
	// behind it.
	ECDH(epk []byte) ([]byte, error)
}

// Credential is what Unlock takes: one of PasswordCredential,
// RecoveryCredential or HardwareCredential.
type Credential interface {
	credential()
}

// PasswordCredential opens a standalone password slot.
type PasswordCredential struct {
	Password string
}

// RecoveryCredential opens a recovery slot.
type RecoveryCredential struct {
	Key kdf.RecoveryKey
}

// HardwareCredential opens the hardware slot enrolled for Token, with its
// entangled password when the slot has one (ignored when it does not).
type HardwareCredential struct {
	Token    Token
	Password string
}

func (PasswordCredential) credential() {}
func (RecoveryCredential) credential() {}
func (HardwareCredential) credential() {}

// SlotSpec describes a slot to add: one of PasswordSlot, RecoverySlot or
// HardwareSlot.
type SlotSpec interface {
	spec()
}

// PasswordSlot is a standalone password slot (§3.1, slot_type 2).
type PasswordSlot struct {
	Password string
	Argon2   kdf.Argon2Params
	Label    string
}

// RecoverySlot is a recovery slot (§3.1, slot_type 3). The caller draws the
// key with kdf.NewRecoveryKey so that it can show the digits to the user.
type RecoverySlot struct {
	Key   kdf.RecoveryKey
	Label string
}

// HardwareSlot is a hardware slot (§3.1, slot_type 1) for the token whose
// public key this is; the token itself need not be present. Password, when
// set, is entangled with the token (DESIGN.md §4) and Argon2 must then be
// set too.
type HardwareSlot struct {
	PublicKey []byte
	Source    format.KeySource // 0 means KeySourceYubiKeyPIV
	Password  string
	Argon2    kdf.Argon2Params
	Label     string
}

func (PasswordSlot) spec() {}
func (RecoverySlot) spec() {}
func (HardwareSlot) spec() {}

// SlotInfo describes a slot without revealing anything secret.
type SlotInfo struct {
	RecipientID       [16]byte
	Type              format.SlotType
	Label             string
	CreatedAt         int64
	EntangledPassword bool
	// Stale: a rotation could not re-wrap this slot; it still holds the
	// previous VMK (§8).
	Stale bool
	// PublicKey is the token's key for a hardware slot, nil otherwise.
	PublicKey []byte
}

func infoOf(s *format.SlotRecord) SlotInfo {
	info := SlotInfo{
		RecipientID:       s.RecipientID,
		Type:              s.Type,
		Label:             s.Label,
		CreatedAt:         s.CreatedAt,
		EntangledPassword: s.Flags&format.FlagEntangledPassword != 0,
		Stale:             s.Flags&format.FlagRewrapStale != 0,
	}
	if s.Type == format.SlotExternalECDH {
		info.PublicKey = bytes.Clone(s.SlotPubkey)
	}
	return info
}

// secrets is a slot's required-secret set (§6.4): the names of everything
// needed to open it. Every entangled password is the one name "pwd", since
// the file cannot tell two apart.
func secrets(s *format.SlotRecord) []string {
	switch s.Type {
	case format.SlotExternalECDH:
		set := []string{"token:" + string(s.SlotPubkey)}
		if s.Flags&format.FlagEntangledPassword != 0 {
			set = append(set, "pwd")
		}
		return set
	case format.SlotStandalonePassword:
		return []string{"password"}
	case format.SlotRecovery:
		return []string{"recovery:" + string(s.RecipientID[:])}
	}
	return nil
}

func disjoint(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return false
			}
		}
	}
	return true
}

// checkInvariant applies §6.4 and DESIGN.md §5 to a proposed slot set: two
// active slots with disjoint secret sets must exist, and a standalone
// password slot may not coexist with a hardware slot.
func checkInvariant(slots []format.SlotRecord) error {
	var active []*format.SlotRecord
	hardware, password := false, false
	for i := range slots {
		if slots[i].State != format.SlotActive {
			continue
		}
		active = append(active, &slots[i])
		hardware = hardware || slots[i].Type == format.SlotExternalECDH
		password = password || slots[i].Type == format.SlotStandalonePassword
	}
	if hardware && password {
		return ErrPolicy
	}
	// R34: a token is enrolled once. The decoder refuses a region in which
	// two non-empty records share a slot_pubkey; this keeps Create and every
	// mutation from producing one. Every non-empty record counts, as in the
	// decoder — an empty one carries no key.
	seenPub := make(map[string]struct{}, len(slots))
	for i := range slots {
		if slots[i].State == format.SlotEmpty {
			continue
		}
		if _, dup := seenPub[string(slots[i].SlotPubkey)]; dup {
			return ErrDuplicate
		}
		seenPub[string(slots[i].SlotPubkey)] = struct{}{}
	}
	for i := range active {
		for j := i + 1; j < len(active); j++ {
			if disjoint(secrets(active[i]), secrets(active[j])) {
				return nil
			}
		}
	}
	return ErrInvariant
}

// newRecord builds a slot record for spec, wrapping vmk at generation gen
// into it.
func newRecord(spec SlotSpec, vaultID [16]byte, vmk [32]byte, gen uint64) (format.SlotRecord, error) {
	s := format.SlotRecord{State: format.SlotActive, CreatedAt: now()}
	if _, err := rand.Read(s.RecipientID[:]); err != nil {
		return s, err
	}
	switch sp := spec.(type) {
	case PasswordSlot:
		pw, err := kdf.NormalizePassword(sp.Password)
		if err != nil {
			return s, fmt.Errorf("%w: %v", ErrParams, err)
		}
		if err := sp.Argon2.Validate(); err != nil {
			return s, fmt.Errorf("%w: %v", ErrParams, err)
		}
		s.Type, s.Curve, s.Label = format.SlotStandalonePassword, format.CurveX25519, sp.Label
		s.Argon2M, s.Argon2T, s.Argon2P = sp.Argon2.MemKiB, sp.Argon2.Time, sp.Argon2.Threads
		if _, err := rand.Read(s.Salt[:]); err != nil {
			return s, err
		}
		if _, err := rand.Read(s.SlotSalt[:]); err != nil {
			return s, err
		}
		skX, dk, err := passwordKeys(&s, pw, vaultID)
		kdf.Zero(pw)
		if err != nil {
			return s, err
		}
		s.SlotPubkey, s.MLKEMEK = kdf.HybridPublic(skX, dk)
		return s, wrapHybrid(&s, kdf.HybridPassword, vaultID, vmk, gen)

	case RecoverySlot:
		s.Type, s.Curve, s.Label = format.SlotRecovery, format.CurveX25519, sp.Label
		if _, err := rand.Read(s.SlotSalt[:]); err != nil {
			return s, err
		}
		skX, dk, err := recoveryKeys(&s, sp.Key, vaultID)
		if err != nil {
			return s, err
		}
		s.SlotPubkey, s.MLKEMEK = kdf.HybridPublic(skX, dk)
		return s, wrapHybrid(&s, kdf.HybridRecovery, vaultID, vmk, gen)

	case HardwareSlot:
		if _, err := ecdh.P256().NewPublicKey(sp.PublicKey); err != nil {
			return s, fmt.Errorf("%w: token public key: %v", ErrParams, err)
		}
		s.Type, s.Curve, s.Label = format.SlotExternalECDH, format.CurveP256, sp.Label
		s.KeySource = sp.Source
		if s.KeySource == 0 {
			s.KeySource = format.KeySourceYubiKeyPIV
		}
		s.SlotPubkey = bytes.Clone(sp.PublicKey)
		var pw []byte
		if sp.Password == "" && sp.Argon2 != (kdf.Argon2Params{}) {
			return s, fmt.Errorf("%w: Argon2 parameters without an entangled password", ErrParams)
		}
		if sp.Password != "" {
			var err error
			if pw, err = kdf.NormalizePassword(sp.Password); err != nil {
				return s, fmt.Errorf("%w: %v", ErrParams, err)
			}
			if err := sp.Argon2.Validate(); err != nil {
				return s, fmt.Errorf("%w: %v", ErrParams, err)
			}
			s.Flags |= format.FlagEntangledPassword
			s.Argon2M, s.Argon2T, s.Argon2P = sp.Argon2.MemKiB, sp.Argon2.Time, sp.Argon2.Threads
			if _, err := rand.Read(s.Salt[:]); err != nil {
				return s, err
			}
		}
		err := wrapHardware(&s, pw, vaultID, vmk, gen)
		kdf.Zero(pw)
		return s, err
	}
	return s, fmt.Errorf("%w: unknown slot spec %T", ErrParams, spec)
}

// passwordKeys derives a password slot's key pair from the password and the
// record's salts.
func passwordKeys(s *format.SlotRecord, pw []byte, vaultID [16]byte) (*ecdh.PrivateKey, *mlkem.DecapsulationKey1024, error) {
	p := kdf.Argon2Params{MemKiB: s.Argon2M, Time: s.Argon2T, Threads: s.Argon2P}
	ikm, err := kdf.PasswordSlotIKM(pw, kdf.Salt(s.Salt), vaultID, s.RecipientID, p)
	if err != nil {
		return nil, nil, err
	}
	defer kdf.Zero(ikm)
	seedX, seedK, err := kdf.HybridSeeds(kdf.HybridPassword, ikm, kdf.SlotSalt(s.SlotSalt), vaultID, s.RecipientID)
	if err != nil {
		return nil, nil, err
	}
	defer kdf.Zero(seedX[:])
	defer kdf.Zero(seedK[:])
	skX, dk, err := kdf.HybridKeys(seedX, seedK)
	return skX, dk, err
}

// recoveryKeys derives a recovery slot's key pair from R and the record's
// slot_salt.
func recoveryKeys(s *format.SlotRecord, r kdf.RecoveryKey, vaultID [16]byte) (*ecdh.PrivateKey, *mlkem.DecapsulationKey1024, error) {
	seedX, seedK, err := kdf.HybridSeeds(kdf.HybridRecovery, r[:], kdf.SlotSalt(s.SlotSalt), vaultID, s.RecipientID)
	if err != nil {
		return nil, nil, err
	}
	defer kdf.Zero(seedX[:])
	defer kdf.Zero(seedK[:])
	skX, dk, err := kdf.HybridKeys(seedX, seedK)
	return skX, dk, err
}

// wrapHybrid wraps vmk into a software slot from its stored public halves
// (§3.1, "wrapping — offline"): fresh E and ML-KEM ciphertext, then the VMK
// under the slot's IK. The record is changed only once everything succeeded.
func wrapHybrid(s *format.SlotRecord, kind kdf.HybridKind, vaultID [16]byte, vmk [32]byte, gen uint64) error {
	pre, E, ct, err := kdf.HybridWrap(kind, s.SlotPubkey, s.MLKEMEK, vaultID, s.RecipientID)
	if err != nil {
		return err
	}
	defer kdf.Zero(pre[:])
	next := *s
	next.EPK, next.MLKEMCT = E, ct
	ik := kdf.DeriveIK(pre, vaultID, s.RecipientID)
	defer kdf.Zero(ik)
	if err := wrapVMK(&next, ik, vaultID, vmk, gen); err != nil {
		return err
	}
	*s = next
	return nil
}

// wrapHardware wraps vmk into a hardware slot from the token's public key:
// a fresh ephemeral P-256 pair, ECDH against slot_pubkey, then the
// token-only or entangled derivation. pw is the normalised entangled
// password, nil when the slot has none; a slot that needs one and gets none
// is ErrPasswordRequired, decided before anything is computed. The record is
// changed only once everything succeeded, so a refused re-wrap leaves the
// slot exactly as it was (§8).
func wrapHardware(s *format.SlotRecord, pw []byte, vaultID [16]byte, vmk [32]byte, gen uint64) error {
	if s.Flags&format.FlagEntangledPassword != 0 && len(pw) == 0 {
		return ErrPasswordRequired
	}
	pub, err := ecdh.P256().NewPublicKey(s.SlotPubkey)
	if err != nil {
		return corrupt("slot_pubkey: %v", err)
	}
	e, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	h, err := e.ECDH(pub)
	if err != nil {
		return err
	}
	defer kdf.Zero(h)
	next := *s
	next.EPK = e.PublicKey().Bytes()
	pre, err := hardwarePre(&next, h, pw, vaultID)
	if err != nil {
		return err
	}
	defer kdf.Zero(pre[:])
	ik := kdf.DeriveIK(pre, vaultID, s.RecipientID)
	defer kdf.Zero(ik)
	if err := wrapVMK(&next, ik, vaultID, vmk, gen); err != nil {
		return err
	}
	*s = next
	return nil
}

// hardwarePre is the hardware slot's pre for shared secret h: token-only, or
// entangled with pw when the record says so (§3.1). A record that requires a
// password and a call without one is ErrPasswordRequired, never a downgrade.
func hardwarePre(s *format.SlotRecord, h, pw []byte, vaultID [16]byte) ([32]byte, error) {
	if s.Flags&format.FlagEntangledPassword == 0 {
		return kdf.HardwarePreToken(h)
	}
	if len(pw) == 0 {
		return [32]byte{}, ErrPasswordRequired
	}
	p := kdf.Argon2Params{MemKiB: s.Argon2M, Time: s.Argon2T, Threads: s.Argon2P}
	return kdf.HardwarePreEntangled(h, pw, kdf.Salt(s.Salt), vaultID, s.RecipientID, p)
}

// wrapVMK draws the record's wrap_nonce, computes the AAD over the record as
// it will be written (R14), and seals VMK ‖ generation.
func wrapVMK(s *format.SlotRecord, ik []byte, vaultID [16]byte, vmk [32]byte, gen uint64) error {
	if _, err := rand.Read(s.WrapNonce[:]); err != nil {
		return err
	}
	s.WrappedVMK = [format.WrappedVMKSize]byte{}
	aad, err := s.AAD(vaultID)
	if err != nil {
		return err
	}
	wrapped, err := kdf.WrapVMKWithNonce(ik, vmk, gen, s.WrapNonce, aad)
	if err != nil {
		return err
	}
	s.WrappedVMK = wrapped
	return nil
}

// unwrapVMK opens a record's wrapped VMK under ik.
func unwrapVMK(s *format.SlotRecord, ik []byte, vaultID [16]byte) ([32]byte, uint64, error) {
	aad, err := s.AAD(vaultID)
	if err != nil {
		return [32]byte{}, 0, err
	}
	vmk, gen, err := kdf.UnwrapVMK(ik, s.WrappedVMK, s.WrapNonce, aad)
	if err != nil {
		return [32]byte{}, 0, ErrAuth
	}
	return vmk, gen, nil
}

// openSlot derives a slot's IK from the credential and unwraps its VMK. A
// credential that does not fit the record is ErrNoSlot; one that fits but
// does not produce its key is ErrVerifier; a key that does not open the
// record is ErrAuth.
func openSlot(s *format.SlotRecord, c Credential, vaultID [16]byte) ([32]byte, uint64, error) {
	var zero [32]byte
	switch cr := c.(type) {
	case PasswordCredential:
		if s.Type != format.SlotStandalonePassword {
			return zero, 0, ErrNoSlot
		}
		pw, err := kdf.NormalizePassword(cr.Password)
		if err != nil {
			return zero, 0, fmt.Errorf("%w: %v", ErrParams, err)
		}
		defer kdf.Zero(pw)
		skX, dk, err := passwordKeys(s, pw, vaultID)
		if err != nil {
			return zero, 0, err
		}
		return openHybrid(s, kdf.HybridPassword, skX, dk, vaultID)

	case RecoveryCredential:
		if s.Type != format.SlotRecovery {
			return zero, 0, ErrNoSlot
		}
		skX, dk, err := recoveryKeys(s, cr.Key, vaultID)
		if err != nil {
			return zero, 0, err
		}
		return openHybrid(s, kdf.HybridRecovery, skX, dk, vaultID)

	case HardwareCredential:
		if s.Type != format.SlotExternalECDH || cr.Token == nil || !bytes.Equal(s.SlotPubkey, cr.Token.PublicKey()) {
			return zero, 0, ErrNoSlot
		}
		var pw []byte
		if s.Flags&format.FlagEntangledPassword != 0 {
			if cr.Password == "" {
				return zero, 0, ErrPasswordRequired
			}
			var err error
			if pw, err = kdf.NormalizePassword(cr.Password); err != nil {
				return zero, 0, fmt.Errorf("%w: %v", ErrParams, err)
			}
			defer kdf.Zero(pw)
		}
		// Trap 2: the epk comes from the file; validate it before it reaches
		// the token. format.SlotRecord.Validate did so on decode; do it here
		// too, so this function cannot be reached with an unvalidated point.
		if _, err := ecdh.P256().NewPublicKey(s.EPK); err != nil {
			return zero, 0, corrupt("epk: %v", err)
		}
		h, err := cr.Token.ECDH(s.EPK)
		if err != nil {
			return zero, 0, err
		}
		defer kdf.Zero(h)
		pre, err := hardwarePre(s, h, pw, vaultID)
		if err != nil {
			return zero, 0, err
		}
		defer kdf.Zero(pre[:])
		ik := kdf.DeriveIK(pre, vaultID, s.RecipientID)
		defer kdf.Zero(ik)
		return unwrapVMK(s, ik, vaultID)
	}
	return zero, 0, fmt.Errorf("%w: unknown credential %T", ErrParams, c)
}

// openHybrid is the software-slot half of openSlot: the X25519 verifier
// (§6.3), then the hybrid unwrap and the VMK.
func openHybrid(s *format.SlotRecord, kind kdf.HybridKind, skX *ecdh.PrivateKey, dk *mlkem.DecapsulationKey1024, vaultID [16]byte) ([32]byte, uint64, error) {
	if !kdf.VerifyX25519(skX, s.SlotPubkey) {
		return [32]byte{}, 0, ErrVerifier
	}
	pre, err := kdf.HybridUnwrap(kind, skX, dk, s.EPK, s.MLKEMCT, vaultID, s.RecipientID)
	if err != nil {
		if errors.Is(err, kdf.ErrHybridInput) {
			return [32]byte{}, 0, corrupt("slot %x: %v", s.RecipientID, err)
		}
		return [32]byte{}, 0, err
	}
	defer kdf.Zero(pre[:])
	ik := kdf.DeriveIK(pre, vaultID, s.RecipientID)
	defer kdf.Zero(ik)
	return unwrapVMK(s, ik, vaultID)
}

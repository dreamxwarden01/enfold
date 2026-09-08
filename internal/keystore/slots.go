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

// HardwareCredential opens the hardware slot enrolled for Token, with the
// vault's entangled password when the slot region header's entangle is 1
// (§6, §18.1). A password handed in while the switch is off is ignored, not
// refused: R4's "never inferred from length" is about the file, not about
// what a caller offers.
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
// public key this is; the token itself need not be present. It carries no
// password and no Argon2 parameters since Revision 2 (§18.1): an enrolled key
// inherits the vault's switch from the slot region header and is wrapped from
// the K_P the vault already keeps.
type HardwareSlot struct {
	PublicKey []byte
	Source    format.KeySource // 0 means KeySourceYubiKeyPIV
	Label     string
}

func (PasswordSlot) spec() {}
func (RecoverySlot) spec() {}
func (HardwareSlot) spec() {}

// SlotInfo describes a slot without revealing anything secret. Whether a
// hardware slot needs the vault password is not a slot fact since Revision 2:
// it is Keystore.Entangled (§6, §18.1).
type SlotInfo struct {
	RecipientID [16]byte
	Type        format.SlotType
	Label       string
	CreatedAt   int64
	// PublicKey is the token's key for a hardware slot, nil otherwise.
	PublicKey []byte
}

func infoOf(s *format.SlotRecord) SlotInfo {
	info := SlotInfo{
		RecipientID: s.RecipientID,
		Type:        s.Type,
		Label:       s.Label,
		CreatedAt:   s.CreatedAt,
	}
	if s.Type == format.SlotExternalECDH {
		info.PublicKey = bytes.Clone(s.SlotPubkey)
	}
	return info
}

// secrets is a slot's required-secret set (§6.4): the names of everything
// needed to open it, under the header hdr. While the vault is entangled every
// active hardware slot also needs the vault's password — the one name "pwd"
// for all of them, since there is one password. Slots of type 2 and 3 are
// never entangled, which is what keeps a recovery slot disjoint from every
// token.
func secrets(hdr format.SlotRegionHeader, s *format.SlotRecord) []string {
	switch s.Type {
	case format.SlotExternalECDH:
		set := []string{"token:" + string(s.SlotPubkey)}
		if hdr.Entangle {
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
// password slot may not coexist with a hardware slot. Its inputs are the
// records and the header, so every caller passes the header the commit would
// write: a 0→1 switch of entangle is evaluated over the sets the new value
// would make, before the header is written and before any slot is re-wrapped.
func checkInvariant(hdr format.SlotRegionHeader, slots []format.SlotRecord) error {
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
			if disjoint(secrets(hdr, active[i]), secrets(hdr, active[j])) {
				return nil
			}
		}
	}
	return ErrInvariant
}

// requireEntangleKey refuses a wrap that would need K_P and does not have it.
// Since Revision 2 no wrap can ask for a password (§8, "No rotation is
// deferred"): reaching one with the header's entangle 1 and no K_P is an
// internal invariant violation, never a prompt. That is what "no rotation is
// deferred" rests on.
func requireEntangleKey(hdr format.SlotRegionHeader, kp *[32]byte) error {
	if hdr.Entangle && kp == nil {
		return fmt.Errorf("%w: the vault is entangled and K_P is not held", ErrParams)
	}
	return nil
}

// newRecord builds a slot record for spec, wrapping vmk at generation gen
// into it. kp is the vault's K_P, nil while the header's entangle is 0; a
// hardware slot inherits the vault's switch and reads nothing about it from
// its own record (§18.1).
func newRecord(spec SlotSpec, vaultID [16]byte, kp *[32]byte, vmk [32]byte, gen uint64) (format.SlotRecord, error) {
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
		// salt and argon2_m/t/p stay zero on a hardware slot (R13, R24): the
		// vault's are the slot region header's.
		return s, wrapHardware(&s, kp, vaultID, vmk, gen)
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
// token-only or entangled derivation. kp is the vault's K_P, nil while the
// header's entangle is 0. No token need be present and no password typed,
// which is what makes enrolment, the switch and every rotation offline
// (§18.1). The record is changed only once everything succeeded, so a refused
// re-wrap leaves the slot exactly as it was (§8).
func wrapHardware(s *format.SlotRecord, kp *[32]byte, vaultID [16]byte, vmk [32]byte, gen uint64) error {
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
	pre, err := hardwarePre(&next, h, kp, vaultID)
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

// hardwarePre is the hardware slot's pre for shared secret h: pre = H while
// kp is nil, else HKDF(H ‖ K_P, …) (§3.1, R3, R5). Which one is decided by the
// caller from the slot region header, never from the record: the record's own
// salt and Argon2 fields are zero on a hardware slot since Revision 2 and are
// never read (R13, R24).
func hardwarePre(s *format.SlotRecord, h []byte, kp *[32]byte, vaultID [16]byte) ([32]byte, error) {
	if kp == nil {
		return kdf.HardwarePreToken(h)
	}
	return kdf.HardwarePreEntangled(h, *kp, vaultID, s.RecipientID)
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
// record is ErrAuth. kp is the vault's K_P, derived once per unlock from the
// header and the password before the slot loop (§3.1), nil while entangle is
// 0; nothing here reads a record for entanglement.
func openSlot(s *format.SlotRecord, c Credential, kp *[32]byte, vaultID [16]byte) ([32]byte, uint64, error) {
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
		pre, err := hardwarePre(s, h, kp, vaultID)
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

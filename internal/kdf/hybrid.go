package kdf

import (
	"crypto/ecdh"
	"crypto/mlkem"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
)

// HybridKind selects the info strings of the two hybrid software slots (R3):
// identical shapes, distinct domains.
type HybridKind uint8

const (
	HybridRecovery HybridKind = 1
	HybridPassword HybridKind = 2
)

func (k HybridKind) infos() (x, kem, pre string, err error) {
	switch k {
	case HybridRecovery:
		return InfoRecoveryX, InfoRecoveryK, InfoRecoveryPre, nil
	case HybridPassword:
		return InfoPasswordX, InfoPasswordK, InfoPasswordPre, nil
	}
	return "", "", "", fmt.Errorf("kdf: unknown hybrid kind %d", k)
}

// Sizes of the hybrid slot's public material.
const (
	X25519PubSize = 32
	MLKEMEKSize   = 1568
	MLKEMCTSize   = 1568
)

// ErrHybridInput is returned for public material of the wrong length or a
// public key the curve rejects.
var ErrHybridInput = errors.New("kdf: invalid hybrid slot input")

// HybridSeeds derives the two seeds from a slot's IKM — R for the recovery
// slot, A for the password slot — with the slot's slot_salt as the HKDF salt
// (R3, R8, R13).
func HybridSeeds(kind HybridKind, ikm []byte, slotSalt SlotSalt, vaultID, recipientID [IDSize]byte) (seedX [SeedXSize]byte, seedK [SeedKSize]byte, err error) {
	xi, ki, _, err := kind.infos()
	if err != nil {
		return seedX, seedK, err
	}
	// R is 16 bytes and A is 32; anything shorter is a truncated or absent
	// credential, and must not quietly become a derivable key pair.
	if len(ikm) < IDSize {
		return seedX, seedK, ErrKeySize
	}
	x := hkdfN(ikm, slotSalt[:], Info(xi, vaultID, recipientID), SeedXSize)
	k := hkdfN(ikm, slotSalt[:], Info(ki, vaultID, recipientID), SeedKSize)
	copy(seedX[:], x)
	copy(seedK[:], k)
	Zero(x)
	Zero(k)
	return seedX, seedK, nil
}

// HybridKeys turns the seeds into key pairs (R9): seed_x is the X25519 private
// scalar as-is, seed_k is the FIPS 203 d ‖ z seed. Both are deterministic.
func HybridKeys(seedX [SeedXSize]byte, seedK [SeedKSize]byte) (*ecdh.PrivateKey, *mlkem.DecapsulationKey1024, error) {
	skX, err := ecdh.X25519().NewPrivateKey(seedX[:])
	if err != nil {
		return nil, nil, err
	}
	dk, err := mlkem.NewDecapsulationKey1024(seedK[:])
	if err != nil {
		return nil, nil, err
	}
	return skX, dk, nil
}

// HybridPublic is what a slot record stores for a hybrid slot: pk_x as
// slot_pubkey and ek as mlkem_ek.
func HybridPublic(skX *ecdh.PrivateKey, dk *mlkem.DecapsulationKey1024) (pkX, ek []byte) {
	return skX.PublicKey().Bytes(), dk.EncapsulationKey().Bytes()
}

// HybridWrap produces pre for a hybrid slot from its public halves alone
// (§3.1, "wrapping — offline"): a fresh X25519 pair e/E, H_x = ECDH(e, pk_x),
// (K_k, ct) = ek.Encapsulate(), pre = HKDF(H_x ‖ K_k, ∅, combine info). It
// returns pre with the E and ct the record must store. The shared secrets are
// zeroed before returning; the ephemeral scalar e lives inside a crypto/ecdh
// key object that offers no wipe, so its lifetime is the heap's.
//
// The public halves come from a slot record. Before a rotation re-wraps into
// one, the caller must have verified the slot region against the registry's
// authenticated hash (R25): a substituted pk_x or ek would otherwise receive
// the new VMK.
func HybridWrap(kind HybridKind, pkX, ek []byte, vaultID, recipientID [IDSize]byte) (pre [KeySize]byte, E, ct []byte, err error) {
	e, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return pre, nil, nil, err
	}
	return hybridWrapWith(kind, pkX, ek, e, vaultID, recipientID)
}

// hybridWrapWith is HybridWrap with a caller-supplied ephemeral key, for tests
// that pin E.
func hybridWrapWith(kind HybridKind, pkX, ek []byte, e *ecdh.PrivateKey, vaultID, recipientID [IDSize]byte) (pre [KeySize]byte, E, ct []byte, err error) {
	_, _, pi, err := kind.infos()
	if err != nil {
		return pre, nil, nil, err
	}
	pub, err := ecdh.X25519().NewPublicKey(pkX)
	if err != nil {
		return pre, nil, nil, fmt.Errorf("%w: pk_x: %v", ErrHybridInput, err)
	}
	ekey, err := mlkem.NewEncapsulationKey1024(ek)
	if err != nil {
		return pre, nil, nil, fmt.Errorf("%w: mlkem_ek: %v", ErrHybridInput, err)
	}
	hx, err := e.ECDH(pub)
	if err != nil {
		return pre, nil, nil, fmt.Errorf("%w: %v", ErrHybridInput, err)
	}
	kk, ct := ekey.Encapsulate()
	pre = combine(hx, kk, pi, vaultID, recipientID)
	Zero(hx)
	Zero(kk)
	return pre, e.PublicKey().Bytes(), ct, nil
}

// HybridUnwrap recovers pre on unlock from the private halves and the stored E
// and ct (§3.1, "unlock"). ML-KEM uses implicit rejection: a wrong dk yields a
// wrong K_k and therefore a wrong pre, and nothing here signals it — the
// failure surfaces only when the VMK unwrap fails to authenticate (§6.3).
func HybridUnwrap(kind HybridKind, skX *ecdh.PrivateKey, dk *mlkem.DecapsulationKey1024, E, ct []byte, vaultID, recipientID [IDSize]byte) (pre [KeySize]byte, err error) {
	_, _, pi, err := kind.infos()
	if err != nil {
		return pre, err
	}
	pub, err := ecdh.X25519().NewPublicKey(E)
	if err != nil {
		return pre, fmt.Errorf("%w: E: %v", ErrHybridInput, err)
	}
	hx, err := skX.ECDH(pub)
	if err != nil {
		return pre, fmt.Errorf("%w: %v", ErrHybridInput, err)
	}
	kk, err := dk.Decapsulate(ct)
	if err != nil {
		Zero(hx)
		return pre, fmt.Errorf("%w: mlkem_ct: %v", ErrHybridInput, err)
	}
	pre = combine(hx, kk, pi, vaultID, recipientID)
	Zero(hx)
	Zero(kk)
	return pre, nil
}

// combine is pre = HKDF(H_x ‖ K_k, salt = ∅, info) with the X25519 secret first
// (R10).
func combine(hx, kk []byte, info string, vaultID, recipientID [IDSize]byte) (pre [KeySize]byte) {
	ikm := make([]byte, 0, len(hx)+len(kk))
	ikm = append(ikm, hx...)
	ikm = append(ikm, kk...)
	out := hkdfN(ikm, nil, Info(info, vaultID, recipientID), KeySize)
	Zero(ikm)
	copy(pre[:], out)
	Zero(out)
	return pre
}

// VerifyX25519 implements the §6.3 verifier for the X25519 half of a software
// slot: the derived private key must reproduce the stored slot_pubkey before
// any unwrap is attempted, so "wrong credential" and "corrupt record" stay
// distinguishable.
func VerifyX25519(skX *ecdh.PrivateKey, slotPubkey []byte) bool {
	return subtle.ConstantTimeCompare(skX.PublicKey().Bytes(), slotPubkey) == 1
}

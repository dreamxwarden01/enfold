package kdf

import "golang.org/x/text/unicode/norm"

// NormalizePassword returns the UTF-8 encoding of the NFC-normalised string
// (R4) and rejects an empty result. The same passphrase typed on two machines
// with different input methods must derive the same key. NFC, not NFKC: R4 is
// about combining sequences, and compatibility folding would silently equate
// visually distinct passwords.
func NormalizePassword(s string) ([]byte, error) {
	p := []byte(norm.NFC.String(s))
	if len(p) == 0 {
		return nil, ErrEmptyPassword
	}
	return p, nil
}

// HardwarePreToken is pre = H, the hardware slot's pre while the slot region
// header's entangle is 0: the raw 32-byte P-256 ECDH output from the token
// (R5), and no Argon2id runs at all (§3.1).
//
// The two hardware derivations are separate functions on purpose: "no
// entangled password" is a vault-wide fact the slot region header records in
// its entangle field (R4, FORMAT §6), and a caller chooses this function
// because of that field — never because a password happened to be absent.
func HardwarePreToken(h []byte) (pre [KeySize]byte, err error) {
	if len(h) != KeySize {
		return pre, ErrKeySize
	}
	copy(pre[:], h)
	return pre, nil
}

// EntangledKey is K_P = Argon2id(P, vault_salt', m, t, p) → 32 (§3.1): the
// vault's password alone, no token and no slot, which is what lets K_P be kept
// under KWK_secrets and every hardware slot be re-wrapped offline (§18.1).
// password is the NormalizePassword result (R4); an empty one is
// ErrEmptyPassword, since "no entangled password" is the header's entangle = 0
// and never an inference from length. The parameters are validated against
// R24 before Argon2id runs, so a hostile header never reaches the allocation.
// Argon2id's slice is zeroed before returning.
func EntangledKey(password []byte, es EntangleSalt, vaultID [IDSize]byte, p Argon2Params) (kp [KeySize]byte, err error) {
	if len(password) == 0 {
		return kp, ErrEmptyPassword
	}
	if err := p.Validate(); err != nil {
		return kp, err
	}
	vsp := VaultSaltPrime(es, vaultID)
	out := argon2id(password, vsp[:], p)
	copy(kp[:], out)
	Zero(out)
	return kp, nil
}

// HardwarePreEntangled is
//
//	pre = HKDF(H ‖ K_P, salt = ∅, info = "Enfold/v1/entangle" ‖ vault_id ‖ recipient_id)
//
// the hardware slot's pre while the slot region header's entangle is 1 (§3.1,
// R3, R5). It takes K_P, not a password: that is what lets §8 step 4 re-wrap
// every hardware slot with no token present and no password typed. H first,
// K_P second; the concatenation is zeroed before returning.
func HardwarePreEntangled(h []byte, kp [KeySize]byte, vaultID, recipientID [IDSize]byte) (pre [KeySize]byte, err error) {
	if len(h) != KeySize {
		return pre, ErrKeySize
	}
	ikm := make([]byte, 0, 2*KeySize)
	ikm = append(ikm, h...)
	ikm = append(ikm, kp[:]...)
	out := hkdfN(ikm, nil, Info(InfoEntangle, vaultID, recipientID), KeySize)
	Zero(ikm)
	copy(pre[:], out)
	Zero(out)
	return pre, nil
}

// PasswordSlotIKM is A = Argon2id(P, salt', m, t, p) for the standalone
// password slot (R8): no HMAC fold, since there is no H to fold with. The
// result is the IKM from which the slot's seeds are derived (HybridSeeds).
func PasswordSlotIKM(password []byte, salt Salt, vaultID, recipientID [IDSize]byte, p Argon2Params) ([]byte, error) {
	if len(password) == 0 {
		return nil, ErrEmptyPassword
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	sp := SaltPrime(salt, vaultID, recipientID)
	return argon2id(password, sp[:], p), nil
}

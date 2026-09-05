package kdf

import (
	"crypto/hmac"
	"crypto/sha256"

	"golang.org/x/text/unicode/norm"
)

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

// HMACFold is pwd' = HMAC-SHA256(key = H, msg = P): H as the key, the
// normalised password as the message, not the other way round (R7).
func HMACFold(h, password []byte) []byte {
	m := hmac.New(sha256.New, h)
	m.Write(password)
	return m.Sum(nil)
}

// HardwarePreToken is the hardware slot's pre when the record's
// has_entangled_password flag is clear: pre = H, the raw 32-byte P-256 ECDH
// output from the token (R5), and Argon2id is skipped entirely (§3.1).
//
// The two hardware derivations are separate functions on purpose: "no
// password" is a state the slot record records in flags bit0 (R4), and a
// caller chooses this function because of that flag — never because a
// password happened to be absent.
func HardwarePreToken(h []byte) (pre [KeySize]byte, err error) {
	if len(h) != KeySize {
		return pre, ErrKeySize
	}
	copy(pre[:], h)
	return pre, nil
}

// HardwarePreEntangled is the hardware slot's pre when has_entangled_password
// is set: Argon2id(HMACFold(H, P), salt', m, t, p) (§3.1, R6, R7, R13).
// password is the NormalizePassword result; an empty one is refused, so a
// skipped prompt cannot degrade to the token-only derivation. The fold output
// and Argon2id's slice are zeroed before returning.
func HardwarePreEntangled(h, password []byte, salt Salt, vaultID, recipientID [IDSize]byte, p Argon2Params) (pre [KeySize]byte, err error) {
	if len(h) != KeySize {
		return pre, ErrKeySize
	}
	if len(password) == 0 {
		return pre, ErrEmptyPassword
	}
	if err := p.Validate(); err != nil {
		return pre, err
	}
	sp := SaltPrime(salt, vaultID, recipientID)
	fold := HMACFold(h, password)
	out := argon2id(fold, sp[:], p)
	Zero(fold)
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

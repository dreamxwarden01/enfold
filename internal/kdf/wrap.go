package kdf

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
)

// ErrAuth is returned when an unwrap fails to authenticate: wrong key, wrong
// AAD, or a modified ciphertext. The three are indistinguishable by design.
var ErrAuth = errors.New("kdf: authentication failed")

// newNonce draws a fresh 96-bit GCM nonce. Every wrap in this package draws
// its own and returns it to the caller for storage, so a (key, nonce) pair can
// be reused only by a caller that deliberately bypasses the API.
func newNonce() ([NonceSize]byte, error) {
	var n [NonceSize]byte
	_, err := rand.Read(n[:])
	return n, err
}

func gcm(key []byte) (cipher.AEAD, error) {
	if len(key) != KeySize {
		return nil, ErrKeySize
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// WrapVMK seals VMK ‖ u64 vmk_generation (little-endian) under a slot's IK
// with AES-256-GCM under a fresh nonce: 40 bytes in, 56 out (R12). aad is the
// slot record's AAD (format.SlotRecord.AAD). The nonce is returned for the
// record's wrap_nonce field.
func WrapVMK(ik []byte, vmk [KeySize]byte, generation uint64, aad []byte) (wrapped [VMKWrapSize]byte, nonce [NonceSize]byte, err error) {
	nonce, err = newNonce()
	if err != nil {
		return wrapped, nonce, err
	}
	wrapped, err = WrapVMKWithNonce(ik, vmk, generation, nonce, aad)
	return wrapped, nonce, err
}

// WrapVMKWithNonce is WrapVMK under a nonce the caller drew. The slot record's
// AAD (R14) covers wrap_nonce, so the keystore layer must draw the nonce, put
// it in the record, compute the AAD, and only then wrap — this is that path.
// The caller is responsible for the nonce being fresh.
func WrapVMKWithNonce(ik []byte, vmk [KeySize]byte, generation uint64, nonce [NonceSize]byte, aad []byte) ([VMKWrapSize]byte, error) {
	var out [VMKWrapSize]byte
	g, err := gcm(ik)
	if err != nil {
		return out, err
	}
	pt := make([]byte, KeySize+8)
	copy(pt, vmk[:])
	binary.LittleEndian.PutUint64(pt[KeySize:], generation)
	copy(out[:], g.Seal(nil, nonce[:], pt, aad))
	Zero(pt)
	return out, nil
}

// UnwrapVMK is the inverse of WrapVMK. On success it also reports the
// generation the blob carried, so a stale slot is diagnosed by comparison with
// the superblock's vmk_generation rather than misreported as corruption (§6.2).
func UnwrapVMK(ik []byte, wrapped [VMKWrapSize]byte, nonce [NonceSize]byte, aad []byte) (vmk [KeySize]byte, generation uint64, err error) {
	g, err := gcm(ik)
	if err != nil {
		return vmk, 0, err
	}
	pt, err := g.Open(nil, nonce[:], wrapped[:], aad)
	if err != nil {
		return vmk, 0, ErrAuth
	}
	copy(vmk[:], pt[:KeySize])
	generation = binary.LittleEndian.Uint64(pt[KeySize:])
	Zero(pt)
	return vmk, generation, nil
}

// WrapKey seals a 32-byte key under a 32-byte key-encryption key with a fresh
// nonce: an archive key under KWK, a per-file DEK under the archive wrap key,
// the identity key under KWK_identity, and every record of the secrets section
// under KWK_secrets (§7.6) — a recovery key reaching it through
// RecoveryKey.Padded, since a secrets plaintext shorter than 32 bytes is
// zero-padded. 32 bytes in, 48 out. The AAD for each is fixed by FORMAT.md R22
// and built by the format package.
func WrapKey(kek []byte, key [KeySize]byte, aad []byte) (wrapped [KeyWrapSize]byte, nonce [NonceSize]byte, err error) {
	nonce, err = newNonce()
	if err != nil {
		return wrapped, nonce, err
	}
	wrapped, err = wrapKeyWithNonce(kek, key, nonce, aad)
	return wrapped, nonce, err
}

func wrapKeyWithNonce(kek []byte, key [KeySize]byte, nonce [NonceSize]byte, aad []byte) ([KeyWrapSize]byte, error) {
	var out [KeyWrapSize]byte
	g, err := gcm(kek)
	if err != nil {
		return out, err
	}
	copy(out[:], g.Seal(nil, nonce[:], key[:], aad))
	return out, nil
}

// UnwrapKey is the inverse of WrapKey.
func UnwrapKey(kek []byte, wrapped [KeyWrapSize]byte, nonce [NonceSize]byte, aad []byte) ([KeySize]byte, error) {
	var key [KeySize]byte
	g, err := gcm(kek)
	if err != nil {
		return key, err
	}
	pt, err := g.Open(nil, nonce[:], wrapped[:], aad)
	if err != nil {
		return key, ErrAuth
	}
	copy(key[:], pt)
	Zero(pt)
	return key, nil
}

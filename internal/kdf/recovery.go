package kdf

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// RecoveryKey is the 128-bit recovery key R (§3.1). Its user-facing form is 48
// digits in 8 groups of 6 (R11).
type RecoveryKey [RecoveryKeySize]byte

// RecoveryKeySize is the recovery key's length: 128 bits (DESIGN.md §5).
const RecoveryKeySize = 16

const (
	recoveryGroups   = 8
	recoveryGroupLen = 6
	recoveryMul      = 11
	recoveryMax      = 65536 * recoveryMul // exclusive bound on a group's value
)

// ErrRecoveryDigits is returned for a string that is not a well-formed
// recovery key: wrong group count, non-digits, a group not divisible by 11 or
// out of range.
var ErrRecoveryDigits = errors.New("kdf: malformed recovery key")

// NewRecoveryKey draws a fresh random R.
func NewRecoveryKey() (RecoveryKey, error) {
	var r RecoveryKey
	if _, err := rand.Read(r[:]); err != nil {
		return RecoveryKey{}, err
	}
	return r, nil
}

// Digits renders R for display (R11): 8 little-endian u16 values, each ×11,
// zero-padded to 6 digits, joined by '-'. The ×11 gives every group a checksum
// property: a single-digit typo is never a multiple of 11.
func (r RecoveryKey) Digits() string {
	var b strings.Builder
	b.Grow(recoveryGroups*recoveryGroupLen + recoveryGroups - 1)
	for i := 0; i < recoveryGroups; i++ {
		if i > 0 {
			b.WriteByte('-')
		}
		v := binary.LittleEndian.Uint16(r[2*i:])
		fmt.Fprintf(&b, "%06d", uint32(v)*recoveryMul)
	}
	return b.String()
}

// ParseRecoveryDigits decodes what the user typed (R23). Any Unicode
// whitespace and any dash — including the en and em dashes that word
// processors substitute for '-' — is ignored, so groups may be separated or
// run together; what is checked is exactly 48 ASCII digits, each group of 6
// below 720896 and divisible by 11.
func ParseRecoveryDigits(s string) (RecoveryKey, error) {
	var digits strings.Builder
	digits.Grow(recoveryGroups * recoveryGroupLen)
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9':
			digits.WriteRune(c)
		case unicode.IsSpace(c) || c == '-' || c == '‐' || c == '‑' || c == '‒' || c == '–' || c == '—' || c == '−':
		default:
			// Position only: the string is key material, and errors reach logs.
			return RecoveryKey{}, fmt.Errorf("%w: unexpected character at position %d", ErrRecoveryDigits, len(digits.String()))
		}
	}
	d := digits.String()
	if len(d) != recoveryGroups*recoveryGroupLen {
		return RecoveryKey{}, fmt.Errorf("%w: %d digits, want %d", ErrRecoveryDigits, len(d), recoveryGroups*recoveryGroupLen)
	}
	var r RecoveryKey
	for i := 0; i < recoveryGroups; i++ {
		g := d[i*recoveryGroupLen : (i+1)*recoveryGroupLen]
		var n uint32
		for _, c := range g {
			n = n*10 + uint32(c-'0')
		}
		if n >= recoveryMax || n%recoveryMul != 0 {
			// Never echo the group: five of its six digits are correct key material.
			return RecoveryKey{}, fmt.Errorf("%w: group %d fails its check", ErrRecoveryDigits, i+1)
		}
		binary.LittleEndian.PutUint16(r[2*i:], uint16(n/recoveryMul))
	}
	return r, nil
}

// Padded is R ‖ sixteen zero bytes: the 32-byte plaintext of a
// recovery_escrow record (§7.6), ready for WrapKey under KWK_secrets.
func (r RecoveryKey) Padded() [KeySize]byte {
	var pt [KeySize]byte
	copy(pt[:], r[:])
	return pt
}

// RecoveryKeyFromPadded is the inverse: a reader takes the first sixteen bytes
// and rejects the record when the tail is not zero (§7.6), which is
// ErrSecretPadding. The tail is compared in constant time — the plaintext is
// key material, and a record that fails this check has already authenticated.
func RecoveryKeyFromPadded(pt [KeySize]byte) (RecoveryKey, error) {
	var r RecoveryKey
	var zero [KeySize - RecoveryKeySize]byte
	if subtle.ConstantTimeCompare(pt[RecoveryKeySize:], zero[:]) != 1 {
		return r, ErrSecretPadding
	}
	copy(r[:], pt[:RecoveryKeySize])
	return r, nil
}

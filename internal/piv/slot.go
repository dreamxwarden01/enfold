//go:build windows

package piv

import (
	"crypto/ecdsa"
	"fmt"

	pivgo "github.com/go-piv/piv-go/v2/piv"
)

// Slot names a PIV key slot. Only the Key Management slot 9d and the
// retired key-management slots 82–95 are valid here; every other slot —
// 9a, 9c, 9e — is refused by every method with ErrForbiddenSlot, so no key
// operation in this program ever targets them (DECISIONS.md 2026-08-31,
// 2026-09-04). What the package's traffic names besides these key slots is
// listed in the package documentation.
type Slot uint8

// SlotKeyManagement is 9d, the slot PIV designates for key agreement and the
// one the design uses.
const SlotKeyManagement Slot = 0x9d

const (
	retiredFirst = 0x82
	retiredLast  = 0x95
)

// ParseSlot accepts a slot number the package may use — 0x9d or
// 0x82–0x95 — and nothing else.
func ParseSlot(b byte) (Slot, bool) {
	s := Slot(b)
	return s, s.Allowed()
}

// AllSlots is every slot the package may address, in the order Generate's
// callers scan for a free one: 9d first, then 82–95.
func AllSlots() []Slot {
	out := []Slot{SlotKeyManagement}
	for s := retiredFirst; s <= retiredLast; s++ {
		out = append(out, Slot(s))
	}
	return out
}

// Allowed reports whether the slot is one this package may address.
func (s Slot) Allowed() bool {
	return s == SlotKeyManagement || (s >= retiredFirst && s <= retiredLast)
}

// Retired reports whether the slot is one of 82–95.
func (s Slot) Retired() bool { return s >= retiredFirst && s <= retiredLast }

func (s Slot) String() string { return fmt.Sprintf("%02x", uint8(s)) }

// pivSlot maps to the library's slot, which also carries the certificate
// object ID. Every path checks Allowed through this.
func (s Slot) pivSlot() (pivgo.Slot, error) {
	if !s.Allowed() {
		return pivgo.Slot{}, ErrForbiddenSlot
	}
	if s == SlotKeyManagement {
		return pivgo.SlotKeyManagement, nil
	}
	ps, ok := pivgo.RetiredKeyManagementSlot(uint32(s))
	if !ok {
		return pivgo.Slot{}, fmt.Errorf("%w: retired slot %s unknown to the library", ErrUnsupported, s)
	}
	return ps, nil
}

// Algorithm is a key's algorithm as the token reports it.
type Algorithm uint8

const (
	AlgorithmUnknown Algorithm = iota
	AlgorithmP256
	AlgorithmP384
	AlgorithmEd25519
	AlgorithmX25519
	AlgorithmRSA1024
	AlgorithmRSA2048
	AlgorithmRSA3072
	AlgorithmRSA4096
)

func (a Algorithm) String() string {
	switch a {
	case AlgorithmP256:
		return "P-256"
	case AlgorithmP384:
		return "P-384"
	case AlgorithmEd25519:
		return "Ed25519"
	case AlgorithmX25519:
		return "X25519"
	case AlgorithmRSA1024:
		return "RSA-1024"
	case AlgorithmRSA2048:
		return "RSA-2048"
	case AlgorithmRSA3072:
		return "RSA-3072"
	case AlgorithmRSA4096:
		return "RSA-4096"
	}
	return "unknown"
}

// PINPolicy is when the token demands the PIN before using a key. Fixed at
// generation.
type PINPolicy uint8

const (
	PINPolicyUnknown PINPolicy = iota
	// PINPolicyNever: the key works without a PIN. Not usable here: the
	// hardware slot's secret is derived after PIN and touch (FORMAT.md §3).
	PINPolicyNever
	// PINPolicyOnce: one VERIFY stands until the card is reset or powered
	// down (DESIGN.md §11 trap 14). The design's stated policy (DESIGN.md
	// §3); the session semantics around it are still the user's decision.
	PINPolicyOnce
	// PINPolicyAlways: every operation needs a VERIFY first.
	PINPolicyAlways
	// PINPolicyMatchOnce and PINPolicyMatchAlways are the YubiKey Bio's
	// fingerprint policies. Not supported by this package.
	PINPolicyMatchOnce
	PINPolicyMatchAlways
)

func (p PINPolicy) String() string {
	switch p {
	case PINPolicyNever:
		return "never"
	case PINPolicyOnce:
		return "once"
	case PINPolicyAlways:
		return "always"
	case PINPolicyMatchOnce:
		return "match-once"
	case PINPolicyMatchAlways:
		return "match-always"
	}
	return "unknown"
}

// TouchPolicy is whether the token waits for a touch before using a key.
// Fixed at generation. Only TouchPolicyAlways is usable, for keys this
// package generates and for keys it reuses alike: the card never releases
// a key without the user's participation (SCOPE.md), and Cached would
// release it for 15 s after one touch.
type TouchPolicy uint8

const (
	TouchPolicyUnknown TouchPolicy = iota
	TouchPolicyNever
	TouchPolicyAlways
	// TouchPolicyCached: one touch stands for 15 s.
	TouchPolicyCached
)

func (t TouchPolicy) String() string {
	switch t {
	case TouchPolicyNever:
		return "never"
	case TouchPolicyAlways:
		return "always"
	case TouchPolicyCached:
		return "cached"
	}
	return "unknown"
}

// Origin is whether the key was generated on the token or imported into it.
// Self-reported by the token; only Attest proves it.
type Origin uint8

const (
	OriginUnknown Origin = iota
	OriginGenerated
	OriginImported
)

func (o Origin) String() string {
	switch o {
	case OriginGenerated:
		return "generated"
	case OriginImported:
		return "imported"
	}
	return "unknown"
}

// KeyInfo describes what a slot holds, from GET METADATA and the presence
// of a certificate object: no PIN, no touch. A slot with a certificate but
// no key has Algorithm AlgorithmUnknown and Certificate true; it counts as
// occupied, because some other program provisioned it.
type KeyInfo struct {
	Slot        Slot
	Algorithm   Algorithm
	PINPolicy   PINPolicy
	TouchPolicy TouchPolicy
	Origin      Origin
	// PublicKey is the 65-byte uncompressed X9.62 point of a P-256 key —
	// what a keystore slot record stores as slot_pubkey — and nil for every
	// other algorithm.
	PublicKey []byte
	// Certificate: a certificate object is stored in the slot. This package
	// never needs one; it tells the user whether other software (Windows'
	// smart-card stack, for one) can see the key.
	Certificate bool

	ecdsaPub *ecdsa.PublicKey // the parsed, on-curve-checked key, for the token
}

// Usable reports whether the keystore may enroll this key, and Token
// succeeds for exactly the keys Usable reports: P-256 with a reportable
// public key, touch policy always, PIN policy once or always. Anything
// else — Cached touch, no PIN, fingerprint policies, other algorithms — is
// refused for reuse the same way it is never generated, and enrollment
// falls through to generating in an empty slot (DECISIONS.md 2026-09-04:
// never overwrite an occupied slot).
func (k KeyInfo) Usable() bool {
	return k.Algorithm == AlgorithmP256 && len(k.PublicKey) == 65 && k.ecdsaPub != nil &&
		k.TouchPolicy == TouchPolicyAlways &&
		(k.PINPolicy == PINPolicyOnce || k.PINPolicy == PINPolicyAlways)
}

// WhyNotUsable explains a false Usable, for the user.
func (k KeyInfo) WhyNotUsable() string {
	switch {
	case k.Usable():
		return ""
	case k.Algorithm == AlgorithmUnknown && k.Certificate:
		return "the slot holds a certificate without a key"
	case k.Algorithm != AlgorithmP256:
		return "the key is " + k.Algorithm.String() + ", not P-256"
	case len(k.PublicKey) != 65 || k.ecdsaPub == nil:
		return "the token does not report the key's public key"
	case k.TouchPolicy == TouchPolicyCached:
		return "the key releases itself for 15 seconds after one touch (touch policy cached)"
	case k.TouchPolicy != TouchPolicyAlways:
		return "the key can be used without a touch"
	case k.PINPolicy == PINPolicyNever:
		return "the key can be used without the PIN"
	case k.PINPolicy == PINPolicyMatchOnce || k.PINPolicy == PINPolicyMatchAlways:
		return "the key uses fingerprint verification, which this program does not support"
	}
	return "the token reports a PIN policy this program does not understand"
}

var (
	algorithms = map[pivgo.Algorithm]Algorithm{
		pivgo.AlgorithmEC256: AlgorithmP256, pivgo.AlgorithmEC384: AlgorithmP384,
		pivgo.AlgorithmEd25519: AlgorithmEd25519, pivgo.AlgorithmX25519: AlgorithmX25519,
		pivgo.AlgorithmRSA1024: AlgorithmRSA1024, pivgo.AlgorithmRSA2048: AlgorithmRSA2048,
		pivgo.AlgorithmRSA3072: AlgorithmRSA3072, pivgo.AlgorithmRSA4096: AlgorithmRSA4096,
	}
	pinPolicies = map[pivgo.PINPolicy]PINPolicy{
		pivgo.PINPolicyNever: PINPolicyNever, pivgo.PINPolicyOnce: PINPolicyOnce, pivgo.PINPolicyAlways: PINPolicyAlways,
		pivgo.PINPolicyMatchOnce: PINPolicyMatchOnce, pivgo.PINPolicyMatchAlways: PINPolicyMatchAlways,
	}
	pinPoliciesOut = map[PINPolicy]pivgo.PINPolicy{PINPolicyOnce: pivgo.PINPolicyOnce, PINPolicyAlways: pivgo.PINPolicyAlways}
	touchPolicies  = map[pivgo.TouchPolicy]TouchPolicy{
		pivgo.TouchPolicyNever: TouchPolicyNever, pivgo.TouchPolicyAlways: TouchPolicyAlways, pivgo.TouchPolicyCached: TouchPolicyCached,
	}
	origins = map[pivgo.Origin]Origin{pivgo.OriginGenerated: OriginGenerated, pivgo.OriginImported: OriginImported}
)

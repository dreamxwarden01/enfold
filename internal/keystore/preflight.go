package keystore

import (
	"crypto/elliptic"
	"fmt"
	"unicode/utf8"

	"github.com/dreamxwarden01/enfold/internal/format"
)

// The preflight of a slot that would join the region (the switch audit of
// 2026-11-07, its findings 1 and 3): every refusal the commit would raise
// that depends only on the records, the header and the slot's plaintext
// facts — §6.4 and DESIGN.md §5, the label, the region's room — decided
// before anything is derived and before a key exists. A caller asks it
// before a YubiKey is asked to generate a key, so that nothing is written
// to a card for a vault that would refuse it; the mutations ask it again
// before their Argon2id, so that no refusal follows a derivation.

// MaxLabelChars is the house's own bound on a slot label, in characters
// (Unicode code points): a name fits a card's line, and far below the
// format's own limit (format.MaxLabelBytes, which 120 characters of at most
// four bytes each never reach). The page mirrors it with maxlength 120,
// which counts UTF-16 units, so nothing the page sends is longer.
const MaxLabelChars = 120

// CheckLabel refuses, with ErrParams, a label that is not valid UTF-8, is
// longer than MaxLabelChars characters, or is longer than the format allows.
func CheckLabel(label string) error {
	if !utf8.ValidString(label) || len(label) > format.MaxLabelBytes || utf8.RuneCountInString(label) > MaxLabelChars {
		return fmt.Errorf("%w: a slot label is valid UTF-8 of at most %d characters", ErrParams, MaxLabelChars)
	}
	return nil
}

// labelOf is the label a spec carries.
func labelOf(spec SlotSpec) string {
	switch sp := spec.(type) {
	case PasswordSlot:
		return sp.Label
	case RecoverySlot:
		return sp.Label
	case HardwareSlot:
		return sp.Label
	}
	return ""
}

// standInPoint is a valid P-256 point (the curve's generator), the public
// key of a hardware stand-in before the YubiKey has given its own. It is
// never written, and it is a size, never an identity: a file may hold a
// record of this very key, so a stand-in carrying it takes no part in the
// duplicate rule (checkSet; the switch audit of 2026-11-07, second pass,
// finding 3).
var standInPoint = func() []byte {
	p := elliptic.P256().Params()
	b := make([]byte, format.P256PubSize)
	b[0] = 4
	p.Gx.FillBytes(b[1:33])
	p.Gy.FillBytes(b[33:])
	return b
}()

// standIn is the record spec would add with nothing secret in it: the type,
// the key source, the label, the Argon2id parameters and every field at the
// size the real record has, so that the checks below give the verdict the
// wrapped record will. A hardware spec's public key stands in when it has
// one, the generator otherwise; a password's and a recovery key's are
// zero, theirs being drawn at the commit.
func standIn(spec SlotSpec) (format.SlotRecord, error) {
	s := format.SlotRecord{State: format.SlotActive}
	switch sp := spec.(type) {
	case HardwareSlot:
		pub := sp.PublicKey
		if pub == nil {
			pub = standInPoint
		}
		s.Type, s.Curve, s.Label, s.KeySource = format.SlotExternalECDH, format.CurveP256, sp.Label, sp.Source
		if s.KeySource == 0 {
			s.KeySource = format.KeySourceYubiKeyPIV
		}
		s.EPK, s.SlotPubkey = standInPoint, pub
	case PasswordSlot, RecoverySlot:
		s.Type, s.Curve, s.Label = format.SlotRecovery, format.CurveX25519, labelOf(spec)
		if pw, ok := sp.(PasswordSlot); ok {
			s.Type = format.SlotStandalonePassword
			s.Argon2M, s.Argon2T, s.Argon2P = pw.Argon2.MemKiB, pw.Argon2.Time, pw.Argon2.Threads
		}
		s.EPK, s.SlotPubkey = make([]byte, format.X25519PubSize), make([]byte, format.X25519PubSize)
		s.MLKEMEK, s.MLKEMCT = make([]byte, format.MLKEMEKSize), make([]byte, format.MLKEMCTSize)
	default:
		return s, fmt.Errorf("%w: unknown slot spec %T", ErrParams, spec)
	}
	return s, nil
}

// admit is the preflight of spec joining slots under the header hdr the
// commit would write, in the order a refusal is told: §6.4 and DESIGN.md §5
// over the final set (ErrPolicy, ErrInvariant, ErrDuplicate), the label
// (ErrParams), then the region's room (ErrFull); a stand-in the format
// refuses for another reason (a key source it does not know, Argon2id
// parameters outside R24) is ErrParams. Nothing secret is read and nothing
// derived. A stand-in whose identity is not known — a YubiKey not yet
// read, every password and recovery key — is kept out of the duplicate
// rule (knownIdentity); a YubiKey's own key is checked with it.
func admit(hdr format.SlotRegionHeader, slots []format.SlotRecord, spec SlotSpec) error {
	stand, err := standIn(spec)
	if err != nil {
		return err
	}
	fresh := -1
	if !knownIdentity(spec) {
		fresh = len(slots)
	}
	if err := checkSet(hdr, append(cloneSlots(slots), stand), fresh); err != nil {
		return err
	}
	if err := CheckLabel(stand.Label); err != nil {
		return err
	}
	return fits(slots, stand)
}

// knownIdentity reports whether spec carries the key the record will hold:
// a YubiKey's public key, once the card has given it. A password's and a
// recovery key's are drawn at the commit.
func knownIdentity(spec SlotSpec) bool {
	hs, ok := spec.(HardwareSlot)
	return ok && hs.PublicKey != nil
}

// fits reports whether stand fits beside slots in one slot region: no more
// than format.MaxSlots records, and the encoded header and records within
// format.SlotRegionSize — the bounds format.SlotRegion.Encode applies at the
// commit, measured here on the very encodings it would write.
func fits(slots []format.SlotRecord, stand format.SlotRecord) error {
	if len(slots)+1 > format.MaxSlots {
		return ErrFull
	}
	b, err := stand.Encode()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrParams, err)
	}
	n := format.MinSlotRegionLen + len(b)
	for i := range slots {
		rec, err := slots[i].Encode()
		if err != nil {
			return err
		}
		n += len(rec)
	}
	if n > format.SlotRegionSize {
		return ErrFull
	}
	return nil
}

// CanAddSlot is AddSlot's verdict on spec decided from the records and the
// header alone, with no VMK: nil when nothing in them refuses it. A
// HardwareSlot's PublicKey may be nil — asked before the YubiKey has made
// or shown its key — and a PasswordSlot's Password is not read. What it
// cannot know is the handle's own state (closed, stale, tampered), which
// AddSlot still refuses first.
func (k *Keystore) CanAddSlot(spec SlotSpec) error {
	return admit(k.hdr, k.slots, spec)
}

// CanAddFirstWayIn is AddFirstWayIn's verdict on spec, with the switch on
// when entangle, decided the same way: ErrParams for a header that already
// carries an entanglement (the precondition AddFirstWayIn enforces), then
// admit under the header the first way in would write.
func (k *Keystore) CanAddFirstWayIn(spec SlotSpec, entangle bool) error {
	if k.hdr != (format.SlotRegionHeader{}) {
		return fmt.Errorf("%w: a first way in is written over a header that carries no entanglement", ErrParams)
	}
	return admit(format.SlotRegionHeader{Entangle: entangle}, k.slots, spec)
}

// CanTradePasswordTo is TradePasswordForToken's verdict on hs, decided the
// same way: CanTradePassword's reasons in their order, then admit over the
// set the trade writes (the standalone password gone, hs added) under the
// switch on. hs.PublicKey may be nil.
func (k *Keystore) CanTradePasswordTo(hs HardwareSlot) error {
	if err := canTradePassword(k.slots); err != nil {
		return err
	}
	return admit(format.SlotRegionHeader{Entangle: true}, withoutPassword(k.slots), hs)
}

// withoutPassword is slots less its active standalone password records.
func withoutPassword(slots []format.SlotRecord) []format.SlotRecord {
	out := make([]format.SlotRecord, 0, len(slots))
	for i := range slots {
		if slots[i].State == format.SlotActive && slots[i].Type == format.SlotStandalonePassword {
			continue
		}
		out = append(out, slots[i])
	}
	return out
}

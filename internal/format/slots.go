package format

import (
	"bytes"
	"crypto/ecdh"
	"unicode/utf8"
)

// SlotRecord is one slot-region record (docs/FORMAT.md §6). Field names follow
// the specification; the two public keys mean different things per slot type:
//
//	SlotExternalECDH        EPK = per-slot ephemeral P-256 key, SlotPubkey = the token's key
//	SlotStandalonePassword  EPK = E (X25519), SlotPubkey = pk_x; MLKEM fields present
//	SlotRecovery            same shape as the password slot
//
// credential_id is on the wire (u16 length, always 0) but not in the struct:
// no v1 slot type carries one, and a non-empty value is rejected on read.
//
// Decoding is canonical: every byte of a valid record is represented, so
// re-encoding a decoded record reproduces the bytes read. AAD relies on that.
type SlotRecord struct {
	State       SlotState
	Type        SlotType
	KeySource   KeySource // SlotExternalECDH only; must be 0 for software slots
	Curve       CurveID
	RecipientID [16]byte
	Flags       uint32
	Label       string
	CreatedAt   int64
	EPK         []byte
	SlotPubkey  []byte
	Salt        [32]byte
	Argon2M     uint32 // KiB
	Argon2T     uint32
	Argon2P     uint8
	SlotSalt    [32]byte
	MLKEMEK     []byte // 1568 bytes for software slots, empty for hardware slots
	MLKEMCT     []byte // 1568 bytes for software slots, empty for hardware slots
	WrapNonce   [NonceSize]byte
	WrappedVMK  [WrappedVMKSize]byte
}

func pubkeySizeFor(c CurveID) (int, bool) {
	switch c {
	case CurveP256:
		return P256PubSize, true
	case CurveX25519:
		return X25519PubSize, true
	}
	return 0, false
}

var (
	zero16 [16]byte
	zero32 [32]byte
	zero12 [NonceSize]byte
	zero56 [WrappedVMKSize]byte
)

// Validate checks the record against the rules of §6, §14 and R21. An empty
// slot (State == SlotEmpty) must be entirely zero apart from its state: it is a
// placeholder, and a placeholder carrying unknown values would be exactly the
// silent skip §1 forbids.
func (s *SlotRecord) Validate() error {
	switch s.State {
	case SlotEmpty:
		if s.Type != 0 || s.KeySource != 0 || s.Curve != 0 || s.RecipientID != zero16 || s.Flags != 0 ||
			s.Label != "" || s.CreatedAt != 0 || len(s.EPK) != 0 || len(s.SlotPubkey) != 0 || s.Salt != zero32 ||
			s.Argon2M != 0 || s.Argon2T != 0 || s.Argon2P != 0 || s.SlotSalt != zero32 ||
			len(s.MLKEMEK) != 0 || len(s.MLKEMCT) != 0 || s.WrapNonce != zero12 || s.WrappedVMK != zero56 {
			return invalidf("empty slot carries non-zero fields")
		}
		return nil
	case SlotActive, SlotRetired:
	default:
		return invalidf("slot_state %d unknown", s.State)
	}
	if s.Flags&^knownSlotFlags != 0 {
		return invalidf("slot flags 0x%x carry unknown bits", s.Flags)
	}
	if !utf8.ValidString(s.Label) || len(s.Label) > 0xFFFF {
		return invalidf("slot label invalid")
	}
	if s.KeySource == KeySourcePRFDerived {
		return invalidf("key_source prf-derived is reserved")
	}
	pubSize, ok := pubkeySizeFor(s.Curve)
	if !ok {
		return invalidf("curve_id %d unknown", s.Curve)
	}
	if len(s.EPK) != pubSize || len(s.SlotPubkey) != pubSize {
		return invalidf("public key lengths %d/%d do not match curve %d (want %d)", len(s.EPK), len(s.SlotPubkey), s.Curve, pubSize)
	}
	if s.Curve == CurveP256 {
		// On-curve check (DESIGN.md §11 trap 2): crypto/ecdh rejects the point at
		// infinity and off-curve encodings, so a hostile epk never reaches the token.
		if _, err := ecdh.P256().NewPublicKey(s.EPK); err != nil {
			return invalidf("epk is not a valid P-256 point: %v", err)
		}
		if _, err := ecdh.P256().NewPublicKey(s.SlotPubkey); err != nil {
			return invalidf("slot_pubkey is not a valid P-256 point: %v", err)
		}
	}
	argonNeeded := false
	switch s.Type {
	case SlotExternalECDH:
		switch s.KeySource {
		case KeySourceYubiKeyPIV, KeySourcePhoneNative:
		default:
			return invalidf("key_source %d unknown", s.KeySource)
		}
		if len(s.MLKEMEK) != 0 || len(s.MLKEMCT) != 0 {
			return invalidf("hardware slot carries ML-KEM material")
		}
		if s.Flags&FlagPRFRawSaltMode != 0 {
			return invalidf("prf_raw_salt_mode is reserved")
		}
		argonNeeded = s.Flags&FlagEntangledPassword != 0
	case SlotStandalonePassword, SlotRecovery:
		if s.KeySource != 0 {
			return invalidf("software slot carries key_source %d", s.KeySource)
		}
		if s.Curve != CurveX25519 {
			return invalidf("software slot must use X25519, got curve %d", s.Curve)
		}
		if len(s.MLKEMEK) != MLKEMEKSize || len(s.MLKEMCT) != MLKEMCTSize {
			return invalidf("software slot ML-KEM lengths %d/%d, want %d/%d", len(s.MLKEMEK), len(s.MLKEMCT), MLKEMEKSize, MLKEMCTSize)
		}
		if s.Flags&(FlagEntangledPassword|FlagPRFRawSaltMode|FlagUVRequired) != 0 {
			return invalidf("software slot carries hardware-only flags 0x%x", s.Flags)
		}
		argonNeeded = s.Type == SlotStandalonePassword
	default:
		return invalidf("slot_type %d unknown", s.Type)
	}
	if argonNeeded {
		if err := ValidateArgon2(s.Argon2M, s.Argon2T, s.Argon2P); err != nil {
			return err
		}
	} else if s.Argon2M != 0 || s.Argon2T != 0 || s.Argon2P != 0 {
		return invalidf("argon2 parameters m=%d t=%d p=%d on a slot that does not use Argon2id", s.Argon2M, s.Argon2T, s.Argon2P)
	}
	return nil
}

// ValidateArgon2 applies R24: m in [8·p, MaxArgon2MemKiB] KiB, t in
// [1, MaxArgon2Time], p in [1, MaxArgon2Threads], and m × t ≤ MaxArgon2Work.
func ValidateArgon2(memKiB, time uint32, threads uint8) error {
	if threads < 1 || threads > MaxArgon2Threads || time < 1 || time > MaxArgon2Time ||
		memKiB < MinArgon2MemKiB || memKiB < 8*uint32(threads) || memKiB > MaxArgon2MemKiB ||
		uint64(memKiB)*uint64(time) > MaxArgon2Work {
		return invalidf("argon2 parameters m=%d KiB t=%d p=%d outside R24 bounds", memKiB, time, threads)
	}
	return nil
}

// body encodes everything after record_len.
func (s *SlotRecord) body() ([]byte, error) {
	w := &writer{b: make([]byte, 0, 256+len(s.MLKEMEK)+len(s.MLKEMCT)+len(s.Label))}
	w.u8(uint8(s.State))
	w.u8(uint8(s.Type))
	w.u8(uint8(s.KeySource))
	w.u8(uint8(s.Curve))
	w.fixed(s.RecipientID[:])
	w.u32(s.Flags)
	w.str(s.Label)
	w.i64(s.CreatedAt)
	w.bytes16(s.EPK)
	w.bytes16(s.SlotPubkey)
	w.fixed(s.Salt[:])
	w.u32(s.Argon2M)
	w.u32(s.Argon2T)
	w.u8(s.Argon2P)
	w.fixed(s.SlotSalt[:])
	w.bytes16(s.MLKEMEK)
	w.bytes16(s.MLKEMCT)
	w.u16(0) // credential_id: empty for every v1 slot
	w.fixed(s.WrapNonce[:])
	w.fixed(s.WrappedVMK[:])
	return w.done()
}

// Encode returns record_len ‖ body.
func (s *SlotRecord) Encode() ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	body, err := s.body()
	if err != nil {
		return nil, err
	}
	w := &writer{b: make([]byte, 0, 4+len(body))}
	w.u32(uint32(len(body)))
	w.fixed(body)
	return w.done()
}

// AAD returns the associated data for this record's wrapped_vmk (§6.1, R14):
// every byte of the record from slot_state through wrap_nonce inclusive,
// followed by vault_id, with one bit excepted — rewrap_stale (R29). That bit
// is set by a rotation that could not reach the slot's secret, so it cannot
// be authenticated by that secret; it is a hint for the UI, and the
// authenticated truth about staleness is the generation inside wrapped_vmk
// (§6.2). Neither record_len nor wrapped_vmk is part of the AAD. The record
// is validated first, so the error is meaningful.
func (s *SlotRecord) AAD(vaultID [16]byte) ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	body, err := s.body()
	if err != nil {
		return nil, err
	}
	aad := make([]byte, 0, len(body)-WrappedVMKSize+16)
	aad = append(aad, body[:len(body)-WrappedVMKSize]...)
	aad[flagsOffset] &^= byte(FlagRewrapStale)
	aad = append(aad, vaultID[:]...)
	return aad, nil
}

// flagsOffset is where flags start in a record body: after slot_state,
// slot_type, key_source, curve_id and recipient_id. FlagRewrapStale lives in
// its first, little-endian byte.
const flagsOffset = 4 + 16

func decodeSlotBody(r *reader) *SlotRecord {
	s := &SlotRecord{}
	s.State = SlotState(r.u8())
	s.Type = SlotType(r.u8())
	s.KeySource = KeySource(r.u8())
	s.Curve = CurveID(r.u8())
	r.fixed(s.RecipientID[:])
	s.Flags = r.u32()
	s.Label = r.str()
	s.CreatedAt = r.i64()
	s.EPK = r.bytes16()
	s.SlotPubkey = r.bytes16()
	r.fixed(s.Salt[:])
	s.Argon2M = r.u32()
	s.Argon2T = r.u32()
	s.Argon2P = r.u8()
	r.fixed(s.SlotSalt[:])
	s.MLKEMEK = r.bytes16()
	s.MLKEMCT = r.bytes16()
	if cred := r.bytes16(); len(cred) != 0 {
		r.invalidf("credential_id is not used by any v1 slot")
	}
	r.fixed(s.WrapNonce[:])
	r.fixed(s.WrappedVMK[:])
	return s
}

func decodeSlotRecord(r *reader, ctx string) (*SlotRecord, error) {
	n := r.u32()
	if r.err != nil {
		return nil, r.err
	}
	if uint64(n) > uint64(r.remaining()) {
		r.truncatedf("record_len %d exceeds the %d bytes left", n, r.remaining())
		return nil, r.err
	}
	body := r.sub(int(n), ctx)
	s := decodeSlotBody(body)
	if err := body.done(); err != nil {
		return nil, err
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return s, nil
}

// DecodeSlotRecord parses one record_len-prefixed record and requires it to be
// consumed exactly.
func DecodeSlotRecord(b []byte) (*SlotRecord, error) {
	r := newReader(b, "slot record")
	s, err := decodeSlotRecord(r, "slot record")
	if err != nil {
		return nil, err
	}
	if err := r.done(); err != nil {
		return nil, err
	}
	return s, nil
}

// EncodeSlotRegion encodes a whole slot region: u32 slot_count, u32 reserved,
// then the records. The result must fit in SlotRegionSize.
func EncodeSlotRegion(slots []SlotRecord) ([]byte, error) {
	if len(slots) > MaxSlots {
		return nil, invalidf("%d slots exceed the cap of %d", len(slots), MaxSlots)
	}
	w := &writer{b: make([]byte, 0, 4096)}
	w.u32(uint32(len(slots)))
	w.u32(0)
	for i := range slots {
		rec, err := slots[i].Encode()
		if err != nil {
			return nil, err
		}
		w.fixed(rec)
	}
	if len(w.b) > SlotRegionSize {
		return nil, invalidf("slot region of %d bytes exceeds %d", len(w.b), SlotRegionSize)
	}
	return w.done()
}

// DecodeSlotRegion parses exactly slot_region_len bytes of a slot region.
func DecodeSlotRegion(b []byte) ([]SlotRecord, error) {
	if len(b) > SlotRegionSize {
		return nil, invalidf("slot region of %d bytes exceeds %d", len(b), SlotRegionSize)
	}
	r := newReader(b, "slot region")
	n := r.u32()
	r.skip(4) // reserved
	if r.err != nil {
		return nil, r.err
	}
	if n > MaxSlots {
		return nil, invalidf("slot_count %d exceeds the cap of %d", n, MaxSlots)
	}
	slots := make([]SlotRecord, 0, n)
	seen := make(map[[16]byte]struct{}, n)
	seenPub := make(map[string]struct{}, n)
	for i := uint32(0); i < n; i++ {
		s, err := decodeSlotRecord(r, "slot record "+itoa(int(i)))
		if err != nil {
			return nil, err
		}
		if s.State != SlotEmpty {
			// R21: recipient_id names a slot; two records with one name
			// would make removal and re-wrap ambiguous.
			if _, dup := seen[s.RecipientID]; dup {
				return nil, invalidf("slot record %d repeats recipient_id %x", i, s.RecipientID)
			}
			seen[s.RecipientID] = struct{}{}
			// R34: one public key, one slot. A region that names the same
			// token in several slots would run the token's ceremony once
			// per slot on an unlock — a run of touch prompts from a file
			// that is only checksummed.
			if _, dup := seenPub[string(s.SlotPubkey)]; dup {
				return nil, invalidf("slot record %d repeats slot_pubkey", i)
			}
			seenPub[string(s.SlotPubkey)] = struct{}{}
		}
		slots = append(slots, *s)
	}
	if err := r.done(); err != nil {
		return nil, err
	}
	return slots, nil
}

// SlotRegionCanonical reports whether re-encoding the given records
// reproduces b exactly, ignoring the region header's reserved word. Decoders
// are canonical by construction; this is the property the fuzz targets assert.
func SlotRegionCanonical(b []byte, slots []SlotRecord) bool {
	enc, err := EncodeSlotRegion(slots)
	if err != nil || len(enc) != len(b) {
		return false
	}
	return bytes.Equal(enc[:4], b[:4]) && bytes.Equal(enc[8:], b[8:])
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	p := len(buf)
	for i > 0 {
		p--
		buf[p] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[p:])
}

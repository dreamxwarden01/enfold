package format

import (
	"bytes"
	"crypto/ecdh"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func seq(start byte, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = start + byte(i)
	}
	return b
}

func fill16(v byte) (a [16]byte) {
	for i := range a {
		a[i] = v
	}
	return
}

func fill32(v byte) (a [32]byte) {
	for i := range a {
		a[i] = v
	}
	return
}

// p256Point derives a real P-256 public key from a fixed scalar, so fixtures
// pass the on-curve check.
func p256Point(scalar byte) []byte {
	k, err := ecdh.P256().NewPrivateKey(seq(scalar, 32))
	if err != nil {
		panic(err)
	}
	return k.PublicKey().Bytes()
}

// hardwareSlot is a slot as Revision 2 writes one: no Argon2 material at all
// (R13, R24) and no reserved flag bit set. The vault's entangled password
// lives in the slot region header now (§18.1).
func hardwareSlot() SlotRecord {
	epk := p256Point(0x10)
	pub := p256Point(0x50)
	s := SlotRecord{
		State: SlotActive, Type: SlotExternalECDH, KeySource: KeySourceYubiKeyPIV, Curve: CurveP256,
		RecipientID: fill16(0xA1), Flags: FlagUVRequired, Label: "YubiKey 5C — desk", CreatedAt: 1_756_000_000,
		EPK: epk, SlotPubkey: pub, SlotSalt: fill32(0x22),
	}
	copy(s.WrapNonce[:], seq(0x30, 12))
	copy(s.WrappedVMK[:], seq(0x40, 56))
	return s
}

// entangledHeader is a header with the vault's password on: a non-zero salt
// and Argon2 parameters inside R24 (§6).
func entangledHeader() SlotRegionHeader {
	return SlotRegionHeader{Entangle: true, Argon2M: 524288, Argon2T: 2, Argon2P: 4, EntangleSalt: fill16(0x71)}
}

// region is the shorthand for a region whose header is not what is under
// test: entangle off, the other four fields zero (§6).
func region(slots ...SlotRecord) *SlotRegion { return &SlotRegion{Slots: slots} }

func encodeRegion(t *testing.T, r *SlotRegion) []byte {
	t.Helper()
	b, err := r.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func softwareSlot(t SlotType) SlotRecord {
	s := SlotRecord{
		State: SlotActive, Type: t, Curve: CurveX25519,
		RecipientID: fill16(0xB2), Label: "recovery", CreatedAt: 1_756_000_001,
		EPK: seq(0x60, 32), SlotPubkey: seq(0x70, 32), SlotSalt: fill32(0x44),
		MLKEMEK: bytes.Repeat([]byte{0xEE}, MLKEMEKSize), MLKEMCT: bytes.Repeat([]byte{0xCC}, MLKEMCTSize),
	}
	// R13: salt and the Argon2 parameters are the standalone password slot's
	// alone; a recovery slot writes all four as zero.
	if t == SlotStandalonePassword {
		s.Salt = fill32(0x33)
		s.Argon2M, s.Argon2T, s.Argon2P = 1048576, 3, 4
	}
	copy(s.WrapNonce[:], seq(0x80, 12))
	copy(s.WrappedVMK[:], seq(0x90, 56))
	return s
}

func TestErrorsAreInvalid(t *testing.T) {
	if !errors.Is(ErrTruncated, ErrInvalid) || !errors.Is(ErrTrailing, ErrInvalid) {
		t.Fatal("shape errors must be ErrInvalid")
	}
	// A region whose header promises a record and then stops one byte into the
	// record_len: the header itself is complete, so this is a truncation.
	short := make([]byte, MinSlotRegionLen+1)
	short[0] = 1 // slot_count
	_, err := DecodeSlotRegion(short)
	if !errors.Is(err, ErrTruncated) || !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "offset") {
		t.Fatalf("truncation error %q lacks class or context", err)
	}
}

func TestKeystoreSuperblockRoundTripAndLayout(t *testing.T) {
	s := &KeystoreSuperblock{Seq: 7, VaultID: fill16(0x55), SlotRegionOff: SlotRegionBOff, SlotRegionLen: 7000,
		RegistryOff: RegistryMinOff, RegistryLen: 1234, VMKGeneration: 3, ModifiedAt: 0x0102030405060708}
	copy(s.RegistryNonce[:], seq(1, 12))
	copy(s.RegistryTag[:], seq(0x20, 16))
	b, err := s.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != SuperblockSize {
		t.Fatalf("len %d", len(b))
	}
	// Layout pinned by hand: magic, version 1 LE, reserved, seq 7 LE.
	want := "454e464f4c444b01" + "0100" + "0000" + "0700000000000000"
	if got := hex.EncodeToString(b[:20]); got != want {
		t.Fatalf("header %s, want %s", got, want)
	}
	if b[104] != 0 { // rotation_pending is reserved and written zero (§5)
		t.Fatalf("rotation_pending at offset 104 is 0x%02x, want 0", b[104])
	}
	if hex.EncodeToString(b[105:113]) != "0807060504030201" { // modified_at, i64 LE, at 105
		t.Fatalf("modified_at at 105: %x", b[105:113])
	}
	d, err := DecodeKeystoreSuperblock(b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s, d) {
		t.Fatalf("round trip mismatch\n%+v\n%+v", s, d)
	}
	if d.LiveSlotRegion() != CopyB {
		t.Fatal("LiveSlotRegion")
	}
	// Reserved bytes are ignored on read; a flipped checksum is not.
	b2 := append([]byte(nil), b...)
	b2[200] = 0xFF
	if _, err := DecodeKeystoreSuperblock(b2); err == nil {
		t.Fatal("checksum did not cover reserved bytes")
	}
	// rotation_pending is reserved: a non-zero byte on the wire, with the
	// checksum recomputed over it, decodes without error and re-encodes as
	// zero (§5, §18.3). Failing on it would be a one-byte denial of service.
	pending := append([]byte(nil), b...)
	pending[104] = 0xFF
	sum := sha256.Sum256(pending[:checksumOffset])
	copy(pending[checksumOffset:], sum[:])
	dp, err := DecodeKeystoreSuperblock(pending)
	if err != nil {
		t.Fatalf("rotation_pending 0xFF refused: %v", err)
	}
	if !reflect.DeepEqual(s, dp) {
		t.Fatalf("rotation_pending reached the struct: %+v", dp)
	}
	if again, err := dp.Encode(); err != nil || again[104] != 0 {
		t.Fatalf("rotation_pending re-encoded as 0x%02x: %v", again[104], err)
	}
	// AAD layout: 16 + 8 + 8 + 12 + 2 + 8 (R35: modified_at is authenticated).
	aad := s.RegistryAAD()
	if len(aad) != 54 || !bytes.Equal(aad[:16], s.VaultID[:]) || aad[16] != 0x00 || aad[17] != 0x20 || aad[18] != 0x04 || aad[44] != 1 || aad[45] != 0 ||
		hex.EncodeToString(aad[46:54]) != "0807060504030201" {
		t.Fatalf("registry AAD %x", aad)
	}
	neg := *s
	neg.ModifiedAt = -1
	if _, err := neg.Encode(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("negative modified_at accepted: %v", err)
	}
	// Bounds and extents.
	big := *s
	big.RegistryLen = MaxRegistryLen + 1
	if _, err := big.Encode(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("registry_len above the cap accepted: %v", err)
	}
	if err := s.ValidateExtents(RegistryMinOff + 1234 + TagSize); err != nil {
		t.Fatalf("exact fit rejected: %v", err)
	}
	if err := s.ValidateExtents(RegistryMinOff + 1234 + TagSize - 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("registry past EOF accepted: %v", err)
	}
	if err := s.ValidateExtents(100); !errors.Is(err, ErrInvalid) {
		t.Fatalf("file shorter than fixed regions accepted: %v", err)
	}
}

func TestCopy(t *testing.T) {
	if CopyA.Other() != CopyB || CopyB.Other() != CopyA {
		t.Fatal("Other")
	}
	if CopyA.KeystoreSuperblockOff() != KeystoreSuperblockAOff || CopyB.KeystoreSuperblockOff() != KeystoreSuperblockBOff ||
		CopyA.SlotRegionOff() != SlotRegionAOff || CopyB.SlotRegionOff() != SlotRegionBOff ||
		CopyA.ArchiveSuperblockOff() != ArchiveSuperblockAOff || CopyB.ArchiveSuperblockOff() != ArchiveSuperblockBOff {
		t.Fatal("offsets")
	}
	if c, ok := SlotRegionCopy(SlotRegionBOff); !ok || c != CopyB {
		t.Fatal("SlotRegionCopy")
	}
	if _, ok := SlotRegionCopy(0x3000); ok {
		t.Fatal("SlotRegionCopy accepted a stray offset")
	}
}

func TestPickKeystoreSuperblock(t *testing.T) {
	mk := func(seq uint64) []byte {
		s := &KeystoreSuperblock{Seq: seq, SlotRegionOff: SlotRegionAOff, SlotRegionLen: MinSlotRegionLen,
			RegistryOff: RegistryMinOff, VMKGeneration: 1}
		b, err := s.Encode()
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if s, which, stale, err := PickKeystoreSuperblock(mk(3), mk(4)); err != nil || which != CopyB || s.Seq != 4 || stale != nil {
		t.Fatalf("got %v %v %v %v", s, which, stale, err)
	}
	garbage := make([]byte, SuperblockSize)
	if s, which, stale, err := PickKeystoreSuperblock(mk(9), garbage); err != nil || which != CopyA || s.Seq != 9 || !errors.Is(stale, ErrInvalid) {
		t.Fatalf("got %v %v %v %v", s, which, stale, err)
	}
	if _, _, _, err := PickKeystoreSuperblock(garbage, garbage); !errors.Is(err, ErrInvalid) || strings.Contains(err.Error(), "\n") {
		t.Fatalf("two invalid superblocks: %v", err)
	}
	if _, _, _, err := PickKeystoreSuperblock(mk(5), mk(5)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("equal seq accepted: %v", err)
	}
}

// §5: rotation_pending is reserved since Revision 2. The struct carries no
// field for it, so no caller can set one, and both directions treat the byte
// as write-zero, ignore-on-read.
func TestRotationPendingIsReserved(t *testing.T) {
	for _, f := range reflect.VisibleFields(reflect.TypeOf(KeystoreSuperblock{})) {
		if strings.Contains(strings.ToLower(f.Name), "rotation") {
			t.Fatalf("KeystoreSuperblock still carries %s", f.Name)
		}
	}
	s := &KeystoreSuperblock{Seq: 1, SlotRegionOff: SlotRegionAOff, SlotRegionLen: MinSlotRegionLen,
		RegistryOff: RegistryMinOff, VMKGeneration: 4}
	b, err := s.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if b[104] != 0 {
		t.Fatalf("Encode wrote rotation_pending 0x%02x", b[104])
	}
	b[104] = 0x7F
	sum := sha256.Sum256(b[:checksumOffset])
	copy(b[checksumOffset:], sum[:])
	d, err := DecodeKeystoreSuperblock(b)
	if err != nil || !reflect.DeepEqual(s, d) {
		t.Fatalf("a non-zero rotation_pending is not ignored on read: %v %+v", err, d)
	}
}

// §5: vmk_generation is 1 at creation and only ever increments, "so 0 never
// exists". Encode and Decode both bind, which keeps §6.2's verdict — a
// recovered generation that is not the superblock's is tampering — from being
// reachable with a zeroed field.
func TestVMKGenerationFloor(t *testing.T) {
	s := &KeystoreSuperblock{Seq: 1, SlotRegionOff: SlotRegionAOff, SlotRegionLen: MinSlotRegionLen,
		RegistryOff: RegistryMinOff, VMKGeneration: 1}
	b, err := s.Encode()
	if err != nil {
		t.Fatal(err)
	}
	zeroed := *s
	zeroed.VMKGeneration = 0
	if _, err := zeroed.Encode(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("vmk_generation 0 encoded: %v", err)
	}
	// And on the wire, with the checksum recomputed so the gate is validate().
	wire := append([]byte(nil), b...)
	copy(wire[96:104], make([]byte, 8)) // vmk_generation, u64 LE, at 96
	sum := sha256.Sum256(wire[:checksumOffset])
	copy(wire[checksumOffset:], sum[:])
	if _, err := DecodeKeystoreSuperblock(wire); !errors.Is(err, ErrInvalid) {
		t.Fatalf("vmk_generation 0 decoded: %v", err)
	}
	// The floor is the only bound: any non-zero value is fine.
	s.VMKGeneration = ^uint64(0)
	if _, err := s.Encode(); err != nil {
		t.Fatalf("a large generation refused: %v", err)
	}
}

func TestSlotRegionRoundTrip(t *testing.T) {
	r := &SlotRegion{
		Header: entangledHeader(),
		Slots:  []SlotRecord{hardwareSlot(), softwareSlot(SlotRecovery), softwareSlot(SlotStandalonePassword), {State: SlotEmpty}},
	}
	r.Slots[2].RecipientID = fill16(0xB3) // recipient_id is unique within a region (R21)
	r.Slots[2].SlotPubkey = seq(0x71, 32) // and so is slot_pubkey (R34)
	b := encodeRegion(t, r)
	if len(b) > 8*1024 {
		t.Fatalf("four slots encode to %d bytes; expected ~7 KB", len(b))
	}
	d, err := DecodeSlotRegion(b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r, d) {
		t.Fatalf("round trip mismatch\n%+v\n%+v", r.Header, d.Header)
	}
	if !SlotRegionCanonical(b, d) {
		t.Fatal("region round trip is not byte-exact")
	}
	// A hardware slot record is exactly 338 bytes with this label (R18): the
	// retired fields stay on the wire as zero, so R18 and R14's span are
	// unchanged by Revision 2.
	hs := hardwareSlot()
	one, err := hs.Encode()
	if err != nil || len(one) != 338 {
		t.Fatalf("hardware slot record: %d bytes, %v", len(one), err)
	}
}

// §6: the header is 32 bytes with the field offsets pinned here by hand, the
// way the superblock's are.
func TestSlotRegionHeaderRoundTrip(t *testing.T) {
	r := &SlotRegion{Header: SlotRegionHeader{
		Entangle: true, Argon2M: 0x00080000, Argon2T: 3, Argon2P: 4, EntangleSalt: fill16(0x71),
	}}
	b := encodeRegion(t, r)
	if len(b) != MinSlotRegionLen {
		t.Fatalf("an empty region is %d bytes, want %d", len(b), MinSlotRegionLen)
	}
	if hex.EncodeToString(b[0:4]) != "00000000" {
		t.Fatalf("slot_count not at 0: %x", b[0:4])
	}
	if b[4] != 1 {
		t.Fatalf("entangle not at 4: %d", b[4])
	}
	if b[5] != 4 {
		t.Fatalf("argon2_p not at 5: %d", b[5])
	}
	if b[6] != 0 || b[7] != 0 {
		t.Fatalf("reserved 6-7 not zero: %x", b[6:8])
	}
	if hex.EncodeToString(b[8:12]) != "00000800" {
		t.Fatalf("argon2_m not at 8: %x", b[8:12])
	}
	if hex.EncodeToString(b[12:16]) != "03000000" {
		t.Fatalf("argon2_t not at 12: %x", b[12:16])
	}
	if !bytes.Equal(b[16:32], bytes.Repeat([]byte{0x71}, 16)) {
		t.Fatalf("entangle_salt not at 16: %x", b[16:32])
	}
	d, err := DecodeSlotRegion(b)
	if err != nil || !reflect.DeepEqual(r.Header, d.Header) || len(d.Slots) != 0 {
		t.Fatalf("round trip: %v %+v", err, d)
	}
	// slot_count is the record count, not a field: a region with records puts
	// it at offset 0 and the first record at 32.
	withSlots := &SlotRegion{Header: r.Header, Slots: []SlotRecord{hardwareSlot()}}
	wb := encodeRegion(t, withSlots)
	if hex.EncodeToString(wb[0:4]) != "01000000" {
		t.Fatalf("slot_count %x", wb[0:4])
	}
	hs := hardwareSlot()
	rec, _ := hs.Encode()
	if !bytes.Equal(wb[MinSlotRegionLen:], rec) {
		t.Fatal("the first record does not begin at offset 32")
	}
}

// §6: entangle 0 means the other four fields are zero and R24 is NOT applied;
// entangle 1 means a non-zero salt and R24 in full, checked before any
// derivation. The third value of the byte is invalid (§1).
func TestSlotRegionHeaderValidity(t *testing.T) {
	ok := func(name string, h SlotRegionHeader) {
		t.Helper()
		if err := h.Validate(); err != nil {
			t.Errorf("%s refused: %v", name, err)
		}
		if _, err := (&SlotRegion{Header: h}).Encode(); err != nil {
			t.Errorf("%s refused by Encode: %v", name, err)
		}
	}
	bad := func(name string, h SlotRegionHeader) {
		t.Helper()
		if err := h.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s accepted: %v", name, err)
		}
		if _, err := (&SlotRegion{Header: h}).Encode(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s accepted by Encode: %v", name, err)
		}
	}
	// Off: all four zero, although m = t = p = 0 would fail ValidateArgon2 —
	// the bounds are not applied on this branch.
	ok("entangle 0, all zero", SlotRegionHeader{})
	bad("entangle 0 with argon2_m", SlotRegionHeader{Argon2M: 65536})
	bad("entangle 0 with argon2_t", SlotRegionHeader{Argon2T: 3})
	bad("entangle 0 with argon2_p", SlotRegionHeader{Argon2P: 4})
	bad("entangle 0 with a salt", SlotRegionHeader{EntangleSalt: fill16(0x71)})
	// On: a real salt and R24 in full.
	ok("entangle 1", entangledHeader())
	bad("entangle 1 with a zero salt", SlotRegionHeader{Entangle: true, Argon2M: 65536, Argon2T: 3, Argon2P: 4})
	h := entangledHeader()
	h.Argon2M = MaxArgon2MemKiB + 1
	bad("argon2_m above the cap", h)
	h = entangledHeader()
	h.Argon2M = 0xFFFFFFFF // the 4 TiB request that took the dev machine down (R24)
	bad("argon2_m 4 TiB", h)
	h = entangledHeader()
	h.Argon2T = MaxArgon2Time + 1
	bad("argon2_t above the cap", h)
	h = entangledHeader()
	h.Argon2P = MaxArgon2Threads + 1
	bad("argon2_p above the cap", h)
	h = entangledHeader()
	h.Argon2M, h.Argon2T = MaxArgon2MemKiB, 5 // work ceiling: 2 GiB × 5 > 8 GiB·passes
	bad("work above the ceiling", h)
	h = entangledHeader()
	h.Argon2M, h.Argon2P = 16, 4 // m < 8p
	bad("argon2_m below 8p", h)
	h = entangledHeader()
	h.Argon2T = 0
	bad("argon2_t zero while on", h)
	// Exactly at the caps is accepted.
	h = entangledHeader()
	h.Argon2M, h.Argon2T, h.Argon2P = MaxArgon2MemKiB, MaxArgon2Work/MaxArgon2MemKiB, MaxArgon2Threads
	ok("at the caps", h)
	// The wire's third value for entangle is invalid.
	b := encodeRegion(t, &SlotRegion{})
	b[4] = 2
	if _, err := DecodeSlotRegion(b); !errors.Is(err, ErrInvalid) {
		t.Errorf("entangle 2 accepted: %v", err)
	}
	// The header is validated before any record is read, so a hostile Argon2
	// header never reaches a derivation even with a well-formed record after
	// it (R24).
	hostile := encodeRegion(t, &SlotRegion{Header: entangledHeader(), Slots: []SlotRecord{hardwareSlot()}})
	hostile[8], hostile[9], hostile[10], hostile[11] = 0xFF, 0xFF, 0xFF, 0xFF
	if _, err := DecodeSlotRegion(hostile); !errors.Is(err, ErrInvalid) {
		t.Errorf("a 4 TiB header on the wire accepted: %v", err)
	}
	// And the header is checked BEFORE the records, not merely at some point:
	// this region promises one record of 0xFFFFFFFF bytes, so a decoder that
	// read records first would answer ErrTruncated and the hostile Argon2
	// parameters would already have been handed to a caller.
	first := append(encodeRegion(t, &SlotRegion{Header: entangledHeader()}), 0xFF, 0xFF, 0xFF, 0xFF)
	first[0] = 1 // slot_count
	first[8], first[9], first[10], first[11] = 0xFF, 0xFF, 0xFF, 0xFF
	err := func() error { _, e := DecodeSlotRegion(first); return e }()
	if !errors.Is(err, ErrInvalid) || errors.Is(err, ErrTruncated) || !strings.Contains(err.Error(), "argon2") {
		t.Errorf("the header is not validated before the records: %v", err)
	}
}

// §6: the first record begins at offset 32, so a region shorter than the
// header is invalid — not truncated, since there is no length field to have
// over-promised.
func TestSlotRegionTooShort(t *testing.T) {
	for n := 0; n < MinSlotRegionLen; n++ {
		if _, err := DecodeSlotRegion(make([]byte, n)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("a %d-byte region was accepted: %v", n, err)
		}
	}
	if _, err := DecodeSlotRegion(make([]byte, MinSlotRegionLen)); err != nil {
		t.Fatalf("a bare 32-byte header refused: %v", err)
	}
	if _, err := DecodeSlotRegion(make([]byte, SlotRegionSize+1)); !errors.Is(err, ErrInvalid) {
		t.Fatal("a region above SlotRegionSize accepted")
	}
}

// R21: canonicality covers the header too, with the two reserved bytes at
// [6, 8) the only window a decode may drop.
func TestSlotRegionHeaderIsCanonical(t *testing.T) {
	b := encodeRegion(t, &SlotRegion{Header: entangledHeader(), Slots: []SlotRecord{hardwareSlot()}})
	d, err := DecodeSlotRegion(b)
	if err != nil {
		t.Fatal(err)
	}
	if !SlotRegionCanonical(b, d) {
		t.Fatal("an intact region is not canonical")
	}
	// Junk in the reserved bytes is ignored on read and re-encodes as zero,
	// and SlotRegionCanonical says so.
	for _, off := range []int{6, 7} {
		junk := append([]byte(nil), b...)
		junk[off] = 0xAB
		dj, err := DecodeSlotRegion(junk)
		if err != nil {
			t.Fatalf("reserved byte %d refused: %v", off, err)
		}
		if !reflect.DeepEqual(d, dj) {
			t.Fatalf("reserved byte %d reached the struct", off)
		}
		if !SlotRegionCanonical(junk, dj) {
			t.Fatalf("reserved byte %d is outside the canonical window", off)
		}
		if again, _ := dj.Encode(); again[off] != 0 {
			t.Fatalf("reserved byte %d re-encoded as 0x%02x", off, again[off])
		}
	}
	// The window is exactly [6, 8) and no wider: a header byte outside it that
	// differs from the struct's own re-encoding is not canonical. argon2_p is
	// the one header field a single-byte edit leaves valid on both sides.
	shifted := append([]byte(nil), b...)
	shifted[5]++
	ds, err := DecodeSlotRegion(shifted)
	if err != nil {
		t.Fatalf("argon2_p+1 refused: %v", err)
	}
	if SlotRegionCanonical(b, ds) {
		t.Fatal("argon2_p sits inside the canonical window")
	}
	if !SlotRegionCanonical(shifted, ds) {
		t.Fatal("the region it was decoded from is not canonical")
	}
	// Every other header byte is inside the window.
	for _, off := range []int{0, 4, 5, 8, 12, 16, 31} {
		junk := append([]byte(nil), b...)
		junk[off] ^= 0x01
		dj, err := DecodeSlotRegion(junk)
		if err != nil {
			continue // an edit the header rejects is caught more strongly
		}
		if SlotRegionCanonical(junk, dj) && reflect.DeepEqual(d, dj) {
			t.Fatalf("header byte %d is neither represented nor rejected", off)
		}
	}
}

// R13, R24, §18.1: a hardware slot carries none of the four Argon2 fields —
// they moved to the header — and the retired flag bits are refused.
func TestHardwareSlotCarriesNoArgon2Material(t *testing.T) {
	bad := func(name string, mut func(*SlotRecord)) {
		t.Helper()
		s := hardwareSlot()
		mut(&s)
		if _, err := s.Encode(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
	bad("salt", func(s *SlotRecord) { s.Salt = fill32(0x11) })
	bad("argon2_m", func(s *SlotRecord) { s.Argon2M = 65536 })
	bad("argon2_t", func(s *SlotRecord) { s.Argon2T = 3 })
	bad("argon2_p", func(s *SlotRecord) { s.Argon2P = 4 })
	bad("legal argon2 parameters", func(s *SlotRecord) { s.Argon2M, s.Argon2T, s.Argon2P = 65536, 3, 4 })
	// A recovery slot is the same (R13); the standalone password slot is the
	// one that keeps them.
	rec := softwareSlot(SlotRecovery)
	rec.Salt = fill32(0x33)
	if _, err := rec.Encode(); !errors.Is(err, ErrInvalid) {
		t.Errorf("recovery slot with a salt accepted: %v", err)
	}
	pw := softwareSlot(SlotStandalonePassword)
	if _, err := pw.Encode(); err != nil {
		t.Errorf("standalone password slot refused: %v", err)
	}
	// slot_salt is deliberately unconstrained on a hardware slot.
	free := hardwareSlot()
	free.SlotSalt = fill32(0xEE)
	if _, err := free.Encode(); err != nil {
		t.Errorf("hardware slot_salt refused: %v", err)
	}
}

// §6: bit 0 (was has_entangled_password) and bit 3 (was rewrap_stale) are
// reserved since Revision 2 and must be zero, on every slot type.
func TestReservedFlagBitsRefused(t *testing.T) {
	if knownSlotFlags&(1<<0) != 0 || knownSlotFlags&(1<<3) != 0 {
		t.Fatalf("knownSlotFlags 0x%x still admits a reserved bit", knownSlotFlags)
	}
	for name, base := range map[string]SlotRecord{
		"hardware": hardwareSlot(), "recovery": softwareSlot(SlotRecovery), "password": softwareSlot(SlotStandalonePassword),
	} {
		for _, bit := range []uint32{1 << 0, 1 << 3} {
			s := base
			s.Flags |= bit
			if _, err := s.Encode(); !errors.Is(err, ErrInvalid) {
				t.Errorf("%s: flag bit 0x%x accepted: %v", name, bit, err)
			}
			// And on the wire: flags start at body offset 20.
			rec, err := base.Encode()
			if err != nil {
				t.Fatal(err)
			}
			rec[4+20] |= byte(bit)
			if _, err := DecodeSlotRecord(rec); !errors.Is(err, ErrInvalid) {
				t.Errorf("%s: flag bit 0x%x on the wire accepted: %v", name, bit, err)
			}
		}
	}
}

// §6.1: the AAD is all of flags, with no exception since Revision 2. Every
// settable bit changes it and nothing is masked out.
func TestSlotAADHasNoExceptions(t *testing.T) {
	s := hardwareSlot()
	s.Flags = 0
	base, err := s.AAD(fill16(0x55))
	if err != nil {
		t.Fatal(err)
	}
	for _, bit := range []uint32{FlagUVRequired} {
		f := s
		f.Flags |= bit
		aad, err := f.AAD(fill16(0x55))
		if err != nil {
			t.Fatalf("flag 0x%x: %v", bit, err)
		}
		if bytes.Equal(base, aad) {
			t.Fatalf("flag 0x%x is not in the AAD", bit)
		}
	}
	// The AAD is exactly record[4 : len-56] ‖ vault_id, byte for byte, with
	// no masking anywhere in the flags word.
	rec, err := s.Encode()
	if err != nil {
		t.Fatal(err)
	}
	want := append(append([]byte(nil), rec[4:len(rec)-WrappedVMKSize]...), bytes.Repeat([]byte{0x55}, 16)...)
	if !bytes.Equal(base, want) {
		t.Fatalf("AAD is not the record's own bytes\n%x\n%x", base, want)
	}
	// A software slot too — its flags word is zero, so a mask would be
	// invisible there; assert the identity instead.
	sw := softwareSlot(SlotStandalonePassword)
	swAAD, err := sw.AAD(fill16(0x55))
	if err != nil {
		t.Fatal(err)
	}
	swRec, _ := sw.Encode()
	if !bytes.Equal(swAAD[:len(swAAD)-16], swRec[4:len(swRec)-WrappedVMKSize]) {
		t.Fatal("software slot AAD is not the record's own bytes")
	}
}

func TestSlotRegionRejectsDuplicatePublicKey(t *testing.T) {
	// R34: one token, one slot. Two active records naming the same
	// slot_pubkey would run the token's ceremony twice on one unlock.
	a := hardwareSlot()
	b := hardwareSlot()
	b.RecipientID[0] ^= 1
	if _, err := DecodeSlotRegion(encodeRegion(t, region(a, b))); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate slot_pubkey accepted: %v", err)
	}
	// A retired record still counts: it names the token as well.
	b.State = SlotRetired
	if _, err := DecodeSlotRegion(encodeRegion(t, region(a, b))); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate slot_pubkey on a retired record accepted: %v", err)
	}
}

func TestSlotRegionRejectsDuplicateRecipient(t *testing.T) {
	a := hardwareSlot()
	b := hardwareSlot()
	if _, err := DecodeSlotRegion(encodeRegion(t, region(a, b))); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate recipient_id accepted: %v", err)
	}
	b.RecipientID[0] ^= 1
	b.SlotPubkey = p256Point(0x51) // distinct token too (R34)
	if _, err := DecodeSlotRegion(encodeRegion(t, region(a, b))); err != nil {
		t.Fatalf("distinct recipient_ids refused: %v", err)
	}
	// Two empty slots share the zero recipient_id and that is fine.
	if _, err := DecodeSlotRegion(encodeRegion(t, region(SlotRecord{}, SlotRecord{}))); err != nil {
		t.Fatalf("two empty slots refused: %v", err)
	}
}

func TestKeystoreExtentsOverflow(t *testing.T) {
	s := KeystoreSuperblock{SlotRegionOff: SlotRegionAOff, SlotRegionLen: 8, RegistryOff: ^uint64(0) - 15, RegistryLen: 0}
	if err := s.ValidateExtents(1 << 20); !errors.Is(err, ErrInvalid) {
		t.Fatalf("wrapped registry extent accepted: %v", err)
	}
}

func TestSlotAADPinned(t *testing.T) {
	s := hardwareSlot()
	rec, _ := s.Encode()
	aad, err := s.AAD(fill16(0x55))
	if err != nil {
		t.Fatal(err)
	}
	// AAD = record without its u32 length and without the trailing 56-byte wrapped_vmk, then vault_id.
	wantLen := len(rec) - 4 - WrappedVMKSize + 16
	if len(aad) != wantLen {
		t.Fatalf("AAD len %d want %d", len(aad), wantLen)
	}
	if !bytes.Equal(aad[:len(aad)-16], rec[4:len(rec)-WrappedVMKSize]) {
		t.Fatal("AAD does not equal record[slot_state..wrap_nonce]")
	}
	if aad[0] != byte(SlotActive) || aad[1] != byte(SlotExternalECDH) {
		t.Fatal("AAD does not start at slot_state")
	}
	if !bytes.Equal(aad[len(aad)-16:], bytes.Repeat([]byte{0x55}, 16)) {
		t.Fatal("AAD does not end with vault_id")
	}
	// The standalone password slot is the one that still carries Argon2
	// parameters, and they are in its AAD (§6.1).
	pw := softwareSlot(SlotStandalonePassword)
	pwAAD, err := pw.AAD(fill16(0x55))
	if err != nil {
		t.Fatal(err)
	}
	pw2 := pw
	pw2.Argon2M = 8
	pwAAD2, _ := pw2.AAD(fill16(0x55))
	if bytes.Equal(pwAAD, pwAAD2) {
		t.Fatal("argon2_m is not in the AAD")
	}
	// Every settable flag bit is inside the AAD; bits 0 and 3 are refused at
	// Encode rather than excepted from it (R29 and R30 are retired).
	for _, bit := range []uint32{FlagUVRequired} {
		flagged := s
		flagged.Flags = bit
		aadFlag, err := flagged.AAD(fill16(0x55))
		if err != nil {
			t.Fatalf("flag 0x%x: %v", bit, err)
		}
		plain := s
		plain.Flags = 0
		aadPlain, _ := plain.AAD(fill16(0x55))
		if bytes.Equal(aadPlain, aadFlag) {
			t.Fatalf("flag 0x%x is not in the AAD", bit)
		}
	}
	for _, bit := range []uint32{1 << 0, 1 << 3} {
		reserved := s
		reserved.Flags |= bit
		if _, err := reserved.Encode(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("reserved flag bit 0x%x encoded: %v", bit, err)
		}
	}
	bad := s
	bad.Curve = 9
	if _, err := bad.AAD(fill16(0x55)); !errors.Is(err, ErrInvalid) {
		t.Fatal("AAD of an invalid record did not fail")
	}
}

// Every byte in the AAD range of a decoded record must be authenticated: flip
// it, and either decoding fails or the recomputed AAD differs. The reviewer's
// probe that found the key_source hole, kept as a permanent test.
func TestSlotAADCoversEveryByte(t *testing.T) {
	for name, s := range map[string]SlotRecord{"hardware": hardwareSlot(), "recovery": softwareSlot(SlotRecovery), "password": softwareSlot(SlotStandalonePassword)} {
		rec, err := s.Encode()
		if err != nil {
			t.Fatal(err)
		}
		orig, _ := s.AAD(fill16(0x55))
		for off := 4; off < len(rec)-WrappedVMKSize; off++ {
			for _, delta := range []byte{0x01, 0x80} {
				mut := append([]byte(nil), rec...)
				mut[off] ^= delta
				d, err := DecodeSlotRecord(mut)
				if err != nil {
					continue
				}
				got, err := d.AAD(fill16(0x55))
				if err != nil {
					t.Fatalf("%s: AAD after flipping byte %d: %v", name, off, err)
				}
				if bytes.Equal(got, orig) {
					t.Fatalf("%s: byte %d flipped by 0x%02x decodes fine yet leaves the AAD unchanged", name, off, delta)
				}
			}
		}
	}
}

func TestSlotValidation(t *testing.T) {
	bad := func(name string, mut func(*SlotRecord)) {
		t.Helper()
		s := hardwareSlot()
		mut(&s)
		if _, err := s.Encode(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	bad("unknown flag", func(s *SlotRecord) { s.Flags |= 1 << 9 })
	bad("reserved key source", func(s *SlotRecord) { s.KeySource = KeySourcePRFDerived })
	bad("unknown key source", func(s *SlotRecord) { s.KeySource = 9 })
	bad("compressed P-256 key", func(s *SlotRecord) { s.EPK = seq(0x02, 33) })
	bad("wrong key length", func(s *SlotRecord) { s.SlotPubkey = seq(1, 64) })
	bad("off-curve P-256 epk", func(s *SlotRecord) { s.EPK = append([]byte{0x04}, seq(0x10, 64)...) })
	bad("off-curve P-256 slot_pubkey", func(s *SlotRecord) { s.SlotPubkey[10] ^= 1 })
	bad("hardware slot with mlkem", func(s *SlotRecord) { s.MLKEMEK = seq(0, 10) })
	bad("unknown state", func(s *SlotRecord) { s.State = 7 })
	bad("unknown type", func(s *SlotRecord) { s.Type = 4 })
	bad("empty slot with junk", func(s *SlotRecord) { s.State = SlotEmpty })
	// R24's bounds now live on the standalone password slot, the one slot
	// type that still runs Argon2id, and on the slot region header.
	badPassword := func(name string, mut func(*SlotRecord)) {
		t.Helper()
		s := softwareSlot(SlotStandalonePassword)
		mut(&s)
		if _, err := s.Encode(); !errors.Is(err, ErrInvalid) {
			t.Errorf("password slot %s: err = %v, want ErrInvalid", name, err)
		}
	}
	badPassword("argon2 work above the cap", func(s *SlotRecord) { s.Argon2M = MaxArgon2MemKiB; s.Argon2T = 5 })
	badPassword("argon2 t zero", func(s *SlotRecord) { s.Argon2T = 0 })
	badPassword("argon2 m below 8p", func(s *SlotRecord) { s.Argon2M = 16; s.Argon2P = 4 })
	badPassword("argon2 m above the cap", func(s *SlotRecord) { s.Argon2M = MaxArgon2MemKiB + 1 })
	badPassword("argon2 t above the cap", func(s *SlotRecord) { s.Argon2T = MaxArgon2Time + 1 })
	badPassword("argon2 p above the cap", func(s *SlotRecord) {
		s.Argon2P = MaxArgon2Threads + 1
		s.Argon2M = 8 * (MaxArgon2Threads + 1)
	})
	badPassword("argon2 4 TiB", func(s *SlotRecord) { s.Argon2M = 0xFFFFFFFF })
	badPassword("no parameters at all", func(s *SlotRecord) { s.Argon2M, s.Argon2T, s.Argon2P = 0, 0, 0 })
	if s := hardwareSlot(); true {
		if _, err := s.Encode(); err != nil {
			t.Errorf("hardware slot with zero Argon2 fields rejected: %v", err)
		}
	}
	if s := softwareSlot(SlotRecovery); true {
		s.Argon2M = 8192
		if _, err := s.Encode(); !errors.Is(err, ErrInvalid) {
			t.Errorf("recovery slot with argon2 params accepted: %v", err)
		}
	}
	if s := softwareSlot(SlotStandalonePassword); true {
		s.Argon2M, s.Argon2T, s.Argon2P = MaxArgon2MemKiB, 4, 4
		if _, err := s.Encode(); err != nil {
			t.Errorf("argon2 m at the cap rejected: %v", err)
		}
	}

	sw := softwareSlot(SlotRecovery)
	sw.Curve = CurveP256
	if _, err := sw.Encode(); !errors.Is(err, ErrInvalid) {
		t.Errorf("software slot on P-256 accepted: %v", err)
	}
	sw = softwareSlot(SlotRecovery)
	sw.MLKEMCT = sw.MLKEMCT[:100]
	if _, err := sw.Encode(); !errors.Is(err, ErrInvalid) {
		t.Errorf("short mlkem_ct accepted: %v", err)
	}
	sw = softwareSlot(SlotStandalonePassword)
	sw.Flags = FlagUVRequired
	if _, err := sw.Encode(); !errors.Is(err, ErrInvalid) {
		t.Errorf("hardware flag on software slot accepted: %v", err)
	}
	sw = softwareSlot(SlotRecovery)
	sw.KeySource = KeySourceYubiKeyPIV
	if _, err := sw.Encode(); !errors.Is(err, ErrInvalid) {
		t.Errorf("key_source on a software slot accepted: %v", err)
	}
	// On the wire: a reserved key_source byte on a software slot fails closed.
	rs := softwareSlot(SlotRecovery)
	rec, _ := rs.Encode()
	rec[4+2] = byte(KeySourcePRFDerived)
	if _, err := DecodeSlotRecord(rec); !errors.Is(err, ErrInvalid) {
		t.Errorf("reserved key_source byte on a software slot accepted: %v", err)
	}
	// A non-empty credential_id fails closed.
	hs := hardwareSlot()
	rec, _ = hs.Encode()
	credOff := len(rec) - WrappedVMKSize - NonceSize - 2
	rec[credOff] = 1
	rec = append(rec[:credOff+2], append([]byte{0xAB}, rec[credOff+2:]...)...)
	rec[0]++ // record_len grew by one
	if _, err := DecodeSlotRecord(rec); !errors.Is(err, ErrInvalid) {
		t.Errorf("credential_id accepted: %v", err)
	}
	// An empty slot must be all zero; state alone is fine.
	if _, err := (&SlotRecord{State: SlotEmpty}).Encode(); err != nil {
		t.Errorf("canonical empty slot rejected: %v", err)
	}

	many := make([]SlotRecord, MaxSlots+1)
	for i := range many {
		many[i] = hardwareSlot()
	}
	if _, err := (&SlotRegion{Slots: many}).Encode(); !errors.Is(err, ErrInvalid) {
		t.Errorf("33 slots accepted: %v", err)
	}
	b := encodeRegion(t, region(hardwareSlot()))
	if _, err := DecodeSlotRegion(append(b, 0)); !errors.Is(err, ErrTrailing) {
		t.Errorf("trailing byte accepted: %v", err)
	}
	if _, err := DecodeSlotRegion(b[:len(b)-1]); !errors.Is(err, ErrTruncated) {
		t.Errorf("truncated region: %v", err)
	}
}

func secretRecord(kind SecretKind, id [16]byte, fill byte) SecretRecord {
	s := SecretRecord{Kind: kind, ID: id}
	copy(s.Nonce[:], seq(fill, NonceSize))
	copy(s.Ciphertext[:], seq(fill, WrappedSecretSize))
	return s
}

func sampleRegistry() *Registry {
	g := &Registry{DeviceID: fill16(0xD1), ModifiedAt: 1_756_000_100}
	copy(g.WrappedIdentityKey[:], seq(0, 48))
	copy(g.IdentityNonce[:], seq(0xA0, 12))
	v1 := VersionRecord{KID: fill16(0x01), CreatedAt: 10, RetiredAt: 20, State: VersionRetired}
	v2 := VersionRecord{KID: fill16(0x02), CreatedAt: 20, State: VersionCurrent}
	copy(v1.WrappedArchiveKey[:], seq(1, 48))
	copy(v2.WrappedArchiveKey[:], seq(2, 48))
	g.Archives = []ArchiveRecord{{
		ArchiveID: fill16(0xAA), Name: "tax records", LastPath: `D:\vault\tax.efd`, Policy: PolicyAlwaysRequireFullAuth,
		CreatedAt: 5, CurrentKID: fill16(0x02), LastCiphertextHash: fill32(0xCC), LastStoredSize: 1 << 30,
		LastWrittenAt: 30, Revision: 4, LastWriter: fill16(0xD1), Description: "the returns, 2019 onwards",
		Versions: []VersionRecord{v1, v2},
	}}
	var pk [32]byte
	copy(pk[:], seq(0x10, 32))
	g.Peers = []PeerPin{{IdentityPubkey: pk, DeviceName: "phone", PairedAt: 1, LastSeenAt: 2,
		Capabilities: CapOpenArchive | CapFullSync, DeviceClass: DeviceEnrolledPersonal}}
	// The secrets section covers all three kinds, in the order §7.6 requires.
	g.Secrets = []SecretRecord{
		secretRecord(SecretRecoveryEscrow, fill16(0xE1), 0xB0),
		secretRecord(SecretEntangledKey, [16]byte{}, 0xC0),
		secretRecord(SecretVMKHistory, VMKHistoryID(1), 0xD0),
		secretRecord(SecretVMKHistory, VMKHistoryID(2), 0xE0),
	}
	return g
}

// secretsSectionLen is what the secrets section of an encoded sampleRegistry
// takes: the count and its records.
const secretsSectionLen = 4 + 4*minSecretRecord

func TestRegistryRoundTripAndValidation(t *testing.T) {
	g := sampleRegistry()
	g.IdleMinutes, g.AbsoluteMinutes = 15, 90
	g.Archives[0].LastSeq, g.Archives[0].HashAtSeq = 12, 12
	b, err := g.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if b[0] != registryVersion {
		t.Fatalf("registry_version %d, want %d", b[0], registryVersion)
	}
	d, err := DecodeRegistry(b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g, d) {
		t.Fatalf("round trip mismatch\n%+v\n%+v", g, d)
	}
	bad := func(name string, mut func(*Registry)) {
		t.Helper()
		g := sampleRegistry()
		mut(g)
		if _, err := g.Encode(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	bad("current kid missing", func(g *Registry) { g.Archives[0].CurrentKID = fill16(0x09) })
	bad("current kid retired", func(g *Registry) { g.Archives[0].CurrentKID = fill16(0x01) })
	bad("two current versions", func(g *Registry) {
		g.Archives[0].Versions[0].State = VersionCurrent
		g.Archives[0].Versions[0].RetiredAt = 0
	})
	bad("retired without retired_at", func(g *Registry) { g.Archives[0].Versions[0].RetiredAt = 0 })
	bad("no versions", func(g *Registry) { g.Archives[0].Versions = nil })
	bad("unknown policy bit", func(g *Registry) { g.Archives[0].Policy = 1 << 6 })
	// The level field is bits 3–5; 5, 6 and 7 are not defined (§7.1).
	for lvl := uint32(5); lvl <= 7; lvl++ {
		bad(fmt.Sprintf("compression level %d", lvl), func(g *Registry) {
			g.Archives[0].Policy = lvl << PolicyLevelShift
		})
	}
	bad("hash ahead of last_seq", func(g *Registry) { g.Archives[0].LastSeq = 3; g.Archives[0].HashAtSeq = 4 })
	bad("duplicate archive", func(g *Registry) {
		g.Archives = append(g.Archives, g.Archives[0])
		g.Archives[1].Versions = []VersionRecord{{KID: fill16(0x03), State: VersionCurrent}}
		g.Archives[1].CurrentKID = fill16(0x03)
	})
	bad("duplicate kid across archives", func(g *Registry) {
		a := g.Archives[0]
		a.ArchiveID = fill16(0xBB)
		a.Versions = []VersionRecord{{KID: fill16(0x02), State: VersionCurrent}}
		g.Archives = append(g.Archives, a)
	})
	bad("unknown capability", func(g *Registry) { g.Peers[0].Capabilities = 1 << 5 })
	bad("unknown device class", func(g *Registry) { g.Peers[0].DeviceClass = 3 })
	// Cut inside the last record: the count guard refuses it (invalid) —
	// cut before the secrets section, the record itself comes up short
	// (truncated); both fail closed.
	if _, err := DecodeRegistry(b[:len(b)-3]); !errors.Is(err, ErrInvalid) {
		t.Errorf("truncated registry: %v", err)
	}
	if _, err := DecodeRegistry(b[:len(b)-secretsSectionLen-3]); !errors.Is(err, ErrTruncated) {
		t.Errorf("truncated registry: %v", err)
	}
	// A short peer key on the wire fails closed.
	short := append([]byte(nil), b...)
	peerOff := len(b) - secretsSectionLen - (2 + 32 + 2 + len("phone") + 8 + 8 + 4 + 1)
	short[peerOff] = 31
	short = append(short[:peerOff+2+31], short[peerOff+2+32:]...)
	if _, err := DecodeRegistry(short); !errors.Is(err, ErrInvalid) {
		t.Errorf("31-byte peer key accepted: %v", err)
	}
	// A hostile archive_count must not allocate.
	h := append([]byte(nil), b...)
	copy(h[4+16+8+48+12+32+2+2:], []byte{0xFF, 0xFF, 0xFF, 0x7F})
	if _, err := DecodeRegistry(h); !errors.Is(err, ErrInvalid) {
		t.Errorf("hostile count: %v", err)
	}
	// A hostile secret_count neither.
	h = append([]byte(nil), b...)
	copy(h[len(h)-secretsSectionLen:], []byte{0xFF, 0xFF, 0xFF, 0x7F})
	if _, err := DecodeRegistry(h); !errors.Is(err, ErrInvalid) {
		t.Errorf("hostile secret count: %v", err)
	}
	// §7.6: two records for one (kind, id), and any section out of order.
	bad("duplicate secret", func(g *Registry) { g.Secrets = append(g.Secrets, g.Secrets[0]) })
	bad("secrets out of order", func(g *Registry) { g.Secrets[0], g.Secrets[1] = g.Secrets[1], g.Secrets[0] })
	bad("unknown secret kind", func(g *Registry) { g.Secrets[0].Kind = 4 })
	// Version 3 is the only version read or written: version 2, version 1 and
	// anything else are refused (§7, §18.2).
	for _, v := range []byte{0, 1, 2, 4, 0xFF} {
		other := append([]byte(nil), b...)
		other[0] = v
		if _, err := DecodeRegistry(other); !errors.Is(err, ErrInvalid) {
			t.Errorf("registry_version %d accepted: %v", v, err)
		}
	}
}

// The compression level of §7.1's bits 3–5 survives a round trip beside
// the other policy bits, and a value the field cannot name is refused
// rather than compressed at (§1).
func TestArchivePolicyCompressionLevel(t *testing.T) {
	levels := []uint32{PolicyLevelUnset, PolicyLevelFastest, PolicyLevelNormal, PolicyLevelBetter, PolicyLevelBest}
	for _, lvl := range levels {
		policy, err := SetPolicyLevel(PolicyHidden|PolicyAlwaysRequireFullAuth, lvl)
		if err != nil {
			t.Fatalf("level %d: %v", lvl, err)
		}
		if got := PolicyLevel(policy); got != lvl {
			t.Fatalf("level %d read back as %d", lvl, got)
		}
		// The level lives in its own bits: the others are untouched.
		if policy&PolicyHidden == 0 || policy&PolicyAlwaysRequireFullAuth == 0 || policy&PolicyNoCompression != 0 {
			t.Fatalf("level %d disturbed the other bits: 0x%x", lvl, policy)
		}
		g := sampleRegistry()
		g.Archives[0].Policy = policy
		b, err := g.Encode()
		if err != nil {
			t.Fatalf("level %d: encode: %v", lvl, err)
		}
		d, err := DecodeRegistry(b)
		if err != nil {
			t.Fatalf("level %d: decode: %v", lvl, err)
		}
		if got := PolicyLevel(d.Archives[0].Policy); got != lvl {
			t.Fatalf("level %d came back as %d", lvl, got)
		}
	}
	// Replacing a level replaces it, rather than or-ing into it.
	policy, err := SetPolicyLevel(PolicyLevelBest<<PolicyLevelShift, PolicyLevelFastest)
	if err != nil || PolicyLevel(policy) != PolicyLevelFastest {
		t.Fatalf("replacing a level: 0x%x %v", policy, err)
	}
	for _, lvl := range []uint32{5, 6, 7, 8, 1 << 20} {
		if _, err := SetPolicyLevel(0, lvl); !errors.Is(err, ErrInvalid) {
			t.Fatalf("level %d was accepted: %v", lvl, err)
		}
	}
}

// §7, §18.2: registry version 3 only. No migration path survives, so a
// version-2 registry — even one whose remaining bytes are perfectly
// well-formed — is refused rather than upgraded.
func TestRegistryVersionThreeOnly(t *testing.T) {
	b, err := sampleRegistry().Encode()
	if err != nil {
		t.Fatal(err)
	}
	if registryVersion != 3 {
		t.Fatalf("registryVersion is %d", registryVersion)
	}
	if b[0] != 3 || b[1] != 0 || b[2] != 0 || b[3] != 0 {
		t.Fatalf("registry_version on the wire %x", b[:4])
	}
	// The old version-2 shape: version 2 with the section a v2 reader knew.
	v2 := append([]byte(nil), b...)
	v2[0] = 2
	if _, err := DecodeRegistry(v2); !errors.Is(err, ErrInvalid) {
		t.Errorf("version 2 accepted: %v", err)
	}
	// The old version-1 shape: version 1, ending after the peer pins.
	v1 := append([]byte(nil), b[:len(b)-secretsSectionLen]...)
	v1[0] = 1
	if _, err := DecodeRegistry(v1); !errors.Is(err, ErrInvalid) {
		t.Errorf("version 1 accepted: %v", err)
	}
	// And a version-3 registry with no secrets at all is fine: an export
	// leaves the entangled_key behind (R28) and a young vault has no history.
	g := sampleRegistry()
	g.Secrets = nil
	empty, err := g.Encode()
	if err != nil {
		t.Fatal(err)
	}
	d, err := DecodeRegistry(empty)
	if err != nil || len(d.Secrets) != 0 {
		t.Fatalf("an empty secrets section: %v %+v", err, d.Secrets)
	}
	if again, _ := d.Encode(); !bytes.Equal(again, empty) {
		t.Fatal("an empty secrets section does not round-trip byte-exactly")
	}
}

// §7.6: kind 1–3 only; kind 2's id is sixteen zero bytes; kind 3's tail is
// zero and the generation it carries is non-zero, since generation 0 never
// exists (§5).
func TestSecretRecordShapeRules(t *testing.T) {
	bad := func(name string, mut func(*SecretRecord)) {
		t.Helper()
		s := secretRecord(SecretRecoveryEscrow, fill16(0xE1), 0xB0)
		mut(&s)
		g := sampleRegistry()
		g.Secrets = []SecretRecord{s}
		if _, err := g.Encode(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
	bad("kind 0", func(s *SecretRecord) { s.Kind = 0 })
	bad("kind 4", func(s *SecretRecord) { s.Kind = 4 })
	bad("kind 255", func(s *SecretRecord) { s.Kind = 255 })
	bad("entangled_key with an id", func(s *SecretRecord) { s.Kind, s.ID = SecretEntangledKey, fill16(0x01) })
	bad("entangled_key with one bit set", func(s *SecretRecord) {
		s.Kind, s.ID = SecretEntangledKey, [16]byte{}
		s.ID[15] = 1
	})
	bad("vmk_history with a non-zero tail", func(s *SecretRecord) {
		s.Kind, s.ID = SecretVMKHistory, VMKHistoryID(7)
		s.ID[8] = 1
	})
	bad("vmk_history of generation 0", func(s *SecretRecord) { s.Kind, s.ID = SecretVMKHistory, VMKHistoryID(0) })
	// A recovery_escrow id has no shape at all — it is a recipient_id.
	for _, id := range [][16]byte{{}, fill16(0x00), fill16(0xFF), VMKHistoryID(9)} {
		g := sampleRegistry()
		g.Secrets = []SecretRecord{secretRecord(SecretRecoveryEscrow, id, 0xB0)}
		if _, err := g.Encode(); err != nil {
			t.Errorf("recovery_escrow id %x refused: %v", id, err)
		}
	}
	// On the wire too: the kind byte opens each record.
	g := sampleRegistry()
	g.Secrets = []SecretRecord{secretRecord(SecretRecoveryEscrow, fill16(0xE1), 0xB0)}
	b, err := g.Encode()
	if err != nil {
		t.Fatal(err)
	}
	b[len(b)-minSecretRecord] = 9
	if _, err := DecodeRegistry(b); !errors.Is(err, ErrInvalid) {
		t.Errorf("kind 9 on the wire accepted: %v", err)
	}
}

// §7.6, R21: strictly ascending by kind, then by id compared as UNSIGNED
// bytes. The ids here straddle 0x7F so that a signed comparison sorts them the
// other way and fails.
func TestSecretsSectionOrdering(t *testing.T) {
	low := secretRecord(SecretRecoveryEscrow, fill16(0x00), 0x10)
	high := secretRecord(SecretRecoveryEscrow, fill16(0xFF), 0x20)
	mid := secretRecord(SecretRecoveryEscrow, fill16(0x7F), 0x30)
	g := sampleRegistry()
	g.Secrets = []SecretRecord{low, mid, high}
	b, err := g.Encode()
	if err != nil {
		t.Fatalf("unsigned order refused: %v", err)
	}
	if _, err := DecodeRegistry(b); err != nil {
		t.Fatalf("unsigned order refused on read: %v", err)
	}
	// A signed comparison would put 0xFF… first; the decoder must refuse it.
	g.Secrets = []SecretRecord{high, low, mid}
	if _, err := g.Encode(); !errors.Is(err, ErrInvalid) {
		t.Fatal("a signed ordering was accepted")
	}
	// Kind orders before id.
	g.Secrets = []SecretRecord{
		secretRecord(SecretVMKHistory, VMKHistoryID(1), 0x40),
		secretRecord(SecretRecoveryEscrow, fill16(0xFF), 0x50),
	}
	if _, err := g.Encode(); !errors.Is(err, ErrInvalid) {
		t.Fatal("kind 3 before kind 1 was accepted")
	}
	// SetSecret keeps the order whatever order the caller works in, and
	// replaces rather than duplicating.
	g = sampleRegistry()
	g.Secrets = nil
	rewritten := secretRecord(SecretRecoveryEscrow, fill16(0x7F), 0x70) // a re-wrap of mid
	for _, rec := range []SecretRecord{high, mid, low, secretRecord(SecretEntangledKey, [16]byte{}, 0x60), rewritten} {
		g.SetSecret(rec)
	}
	if len(g.Secrets) != 4 {
		t.Fatalf("SetSecret produced %d records", len(g.Secrets))
	}
	if _, err := g.Encode(); err != nil {
		t.Fatalf("SetSecret left the section out of order: %v", err)
	}
	if got := g.Secrets[1].Nonce[0]; got != 0x70 {
		t.Fatalf("SetSecret did not replace in place: nonce starts 0x%02x", got)
	}
	// Secret, SecretsOfKind and DeleteSecret agree with the section.
	if s := g.Secret(SecretRecoveryEscrow, fill16(0x7F)); s == nil || s.Nonce[0] != 0x70 {
		t.Fatalf("Secret: %+v", s)
	}
	if s := g.Secret(SecretVMKHistory, fill16(0x7F)); s != nil {
		t.Fatal("Secret matched on id alone")
	}
	if n := len(g.SecretsOfKind(SecretRecoveryEscrow)); n != 3 {
		t.Fatalf("SecretsOfKind: %d", n)
	}
	if n := len(g.SecretsOfKind(SecretVMKHistory)); n != 0 {
		t.Fatalf("SecretsOfKind for an absent kind: %d", n)
	}
	if !g.DeleteSecret(SecretEntangledKey, [16]byte{}) || g.DeleteSecret(SecretEntangledKey, [16]byte{}) {
		t.Fatal("DeleteSecret")
	}
	if _, err := g.Encode(); err != nil {
		t.Fatalf("DeleteSecret left the section out of order: %v", err)
	}
}

// §7.6: exactly one entangled_key record when the header's entangle is 1 and
// none when it is 0. DecodeRegistry does not apply it — the format layer never
// sees the slot region — so it is a call the keystore makes at unlock and on
// every write that lands a header.
func TestRegistryEntangleAgreement(t *testing.T) {
	g := sampleRegistry() // carries one entangled_key record
	if err := g.CheckEntangleAgreement(true); err != nil {
		t.Fatalf("one record with entangle 1: %v", err)
	}
	if err := g.CheckEntangleAgreement(false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("one record with entangle 0: %v", err)
	}
	// Decoding does not apply it: an export's registry (entangle 0, no K_P)
	// and this one are both well-formed on their own.
	b, err := g.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeRegistry(b); err != nil {
		t.Fatalf("decode applied the cross-check: %v", err)
	}
	g.DeleteSecret(SecretEntangledKey, [16]byte{})
	if err := g.CheckEntangleAgreement(false); err != nil {
		t.Fatalf("no record with entangle 0: %v", err)
	}
	if err := g.CheckEntangleAgreement(true); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no record with entangle 1: %v", err)
	}
	// Two entangled_key records cannot exist: kind 2's id is fixed at zero
	// and the section is strictly ascending, so the shape rules forbid it.
	g.Secrets = append(g.Secrets, secretRecord(SecretEntangledKey, [16]byte{}, 0xC0), secretRecord(SecretEntangledKey, [16]byte{}, 0xC1))
	if _, err := g.Encode(); !errors.Is(err, ErrInvalid) {
		t.Fatal("two entangled_key records encoded")
	}
}

// §7.6: a vmk_history id is the retired generation as a little-endian u64 in
// bytes 0–7, bytes 8–15 zero, and HistoryGeneration reads it back for that
// kind alone.
func TestVMKHistoryID(t *testing.T) {
	id := VMKHistoryID(0x0102030405060708)
	if hex.EncodeToString(id[:]) != "0807060504030201"+"0000000000000000" {
		t.Fatalf("VMKHistoryID %x", id)
	}
	for _, gen := range []uint64{1, 2, 255, 256, 1 << 40, ^uint64(0)} {
		s := secretRecord(SecretVMKHistory, VMKHistoryID(gen), 0xD0)
		got, ok := s.HistoryGeneration()
		if !ok || got != gen {
			t.Fatalf("generation %d round-tripped as %d (ok=%v)", gen, got, ok)
		}
	}
	// Only kind 3 answers: a recipient_id is not a number.
	for _, k := range []SecretKind{SecretRecoveryEscrow, SecretEntangledKey} {
		s := secretRecord(k, [16]byte{}, 0xD0)
		if _, ok := s.HistoryGeneration(); ok {
			t.Fatalf("kind %d answered HistoryGeneration", k)
		}
	}
}

// §7.1, §18.2: description is at most 1 024 bytes of valid UTF-8 and
// forgotten_at is never negative. Both sit immediately after hash_at_seq.
func TestArchiveDescriptionAndForgottenAt(t *testing.T) {
	g := sampleRegistry()
	g.Archives[0].Description = strings.Repeat("a", MaxDescriptionLen)
	if _, err := g.Encode(); err != nil {
		t.Fatalf("a 1024-byte description refused: %v", err)
	}
	g.Archives[0].Description = strings.Repeat("a", MaxDescriptionLen+1)
	if _, err := g.Encode(); !errors.Is(err, ErrInvalid) {
		t.Fatal("a 1025-byte description accepted")
	}
	// Bytes, not characters: 400 four-byte runes are 1 600 bytes.
	g.Archives[0].Description = strings.Repeat("\U0001F600", 400)
	if _, err := g.Encode(); !errors.Is(err, ErrInvalid) {
		t.Fatal("1600 bytes of emoji accepted")
	}
	// Empty round-trips as empty.
	g = sampleRegistry()
	g.Archives[0].Description = ""
	b, err := g.Encode()
	if err != nil {
		t.Fatal(err)
	}
	d, err := DecodeRegistry(b)
	if err != nil || d.Archives[0].Description != "" {
		t.Fatalf("empty description: %v %q", err, d.Archives[0].Description)
	}
	// Invalid UTF-8 on the wire is refused, and the two fields sit where §7.1
	// puts them: description then forgotten_at, right after hash_at_seq.
	g = sampleRegistry()
	g.Archives[0].Description = "ab"
	g.Archives[0].ForgottenAt = 0x0102030405060708
	b, err = g.Encode()
	if err != nil {
		t.Fatal(err)
	}
	// archive record body: 16 + name + last_path + 4 + 8 + 16 + 32 + 8 + 8 + 8 + 16 + 8 + 8
	descOff := 4 + 16 + 8 + 48 + 12 + 32 + 2 + 2 + 4 +
		16 + (2 + len("tax records")) + (2 + len(`D:\vault\tax.efd`)) + 4 + 8 + 16 + 32 + 8 + 8 + 8 + 16 + 8 + 8
	if b[descOff] != 2 || b[descOff+1] != 0 || string(b[descOff+2:descOff+4]) != "ab" {
		t.Fatalf("description is not where §7.1 puts it: %x", b[descOff:descOff+4])
	}
	if hex.EncodeToString(b[descOff+4:descOff+12]) != "0807060504030201" {
		t.Fatalf("forgotten_at does not follow the description: %x", b[descOff+4:descOff+12])
	}
	if hex.EncodeToString(b[descOff+12:descOff+16]) != "02000000" {
		t.Fatalf("version_count does not follow forgotten_at: %x", b[descOff+12:descOff+16])
	}
	junk := append([]byte(nil), b...)
	junk[descOff+2] = 0xFF // an invalid UTF-8 lead byte
	if _, err := DecodeRegistry(junk); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid UTF-8 in a description accepted")
	}
	// A negative forgotten_at is refused in both directions.
	g.Archives[0].ForgottenAt = -1
	if _, err := g.Encode(); !errors.Is(err, ErrInvalid) {
		t.Fatal("a negative forgotten_at encoded")
	}
	neg := append([]byte(nil), b...)
	copy(neg[descOff+4:descOff+12], bytes.Repeat([]byte{0xFF}, 8))
	if _, err := DecodeRegistry(neg); !errors.Is(err, ErrInvalid) {
		t.Fatal("a negative forgotten_at decoded")
	}
	// Forgotten() is forgotten_at != 0.
	a := ArchiveRecord{}
	if a.Forgotten() {
		t.Fatal("a fresh record reads as forgotten")
	}
	a.ForgottenAt = 1
	if !a.Forgotten() {
		t.Fatal("forgotten_at 1 does not read as forgotten")
	}
}

// §18.2: thirty days, strictly greater, measured against the write's own
// modified_at — and a stamp from a faster clock is freshly forgotten, never
// overdue.
func TestPurgeForgotten(t *testing.T) {
	const forgot = 1_756_000_000
	mk := func(id byte, forgottenAt int64) ArchiveRecord {
		return ArchiveRecord{
			ArchiveID: fill16(id), Name: "a", CurrentKID: fill16(id ^ 0xFF), ForgottenAt: forgottenAt,
			Versions: []VersionRecord{{KID: fill16(id ^ 0xFF), State: VersionCurrent}},
		}
	}
	g := &Registry{Archives: []ArchiveRecord{mk(1, forgot)}}
	if dropped := g.PurgeForgotten(forgot + ForgottenRetentionSeconds); len(dropped) != 0 || len(g.Archives) != 1 {
		t.Fatalf("a record was dropped exactly at the retention edge: %d", len(dropped))
	}
	dropped := g.PurgeForgotten(forgot + ForgottenRetentionSeconds + 1)
	if len(dropped) != 1 || dropped[0].ArchiveID != fill16(1) || len(g.Archives) != 0 {
		t.Fatalf("a record one second past the edge survived: %d dropped, %d kept", len(dropped), len(g.Archives))
	}
	// A forgotten_at ahead of the write's modified_at is never dropped, no
	// matter how far ahead: a faster clock means freshly forgotten.
	g = &Registry{Archives: []ArchiveRecord{mk(2, forgot)}}
	for _, at := range []int64{0, forgot - 1, forgot - ForgottenRetentionSeconds*10} {
		if dropped := g.PurgeForgotten(at); len(dropped) != 0 {
			t.Fatalf("a future forgotten_at was dropped at modified_at %d", at)
		}
	}
	// A record that is not forgotten is never touched.
	g = &Registry{Archives: []ArchiveRecord{mk(3, 0)}}
	if dropped := g.PurgeForgotten(forgot + ForgottenRetentionSeconds*100); len(dropped) != 0 || len(g.Archives) != 1 {
		t.Fatal("a live record was purged")
	}
	// Mixed: only the overdue ones go, the rest keep their order, and the
	// registry is still valid afterwards.
	g = sampleRegistry()
	g.Archives = []ArchiveRecord{mk(4, 0), mk(5, forgot), mk(6, forgot+ForgottenRetentionSeconds), mk(7, 0)}
	dropped = g.PurgeForgotten(forgot + ForgottenRetentionSeconds + 1)
	if len(dropped) != 1 || dropped[0].ArchiveID != fill16(5) {
		t.Fatalf("dropped %d records: %v", len(dropped), dropped)
	}
	if len(g.Archives) != 3 || g.Archives[0].ArchiveID != fill16(4) || g.Archives[1].ArchiveID != fill16(6) || g.Archives[2].ArchiveID != fill16(7) {
		t.Fatalf("survivors out of order: %+v", g.Archives)
	}
	if err := g.Validate(); err != nil {
		t.Fatalf("the purged registry does not validate: %v", err)
	}
	// The dropped record is returned whole, keys and all, so the caller can
	// say what went (APP §13).
	if len(dropped[0].Versions) != 1 {
		t.Fatal("the dropped record lost its versions")
	}
}

// R25: the registry's authenticated hash of the slot region catches a
// substituted public key, an added record, a spliced-in old region — and an
// edited header, which no slot record's AAD covers (§6).
func TestSlotRegionHashDetectsSubstitution(t *testing.T) {
	r := &SlotRegion{Header: entangledHeader(), Slots: []SlotRecord{hardwareSlot(), softwareSlot(SlotRecovery)}}
	region := encodeRegion(t, r)
	g := sampleRegistry()
	g.SlotRegionHash = SlotRegionHash(region)
	if g.SlotRegionHash != sha256.Sum256(region) {
		t.Fatal("SlotRegionHash is not SHA-256 of the region")
	}
	if err := g.VerifySlotRegion(region); err != nil {
		t.Fatalf("intact region rejected: %v", err)
	}
	// The attack: replace the recovery slot's public key with the attacker's.
	d, err := DecodeSlotRegion(region)
	if err != nil {
		t.Fatal(err)
	}
	swapped := *d
	swapped.Slots = append([]SlotRecord(nil), d.Slots...)
	swapped.Slots[1].SlotPubkey = seq(0x99, 32)
	if err := g.VerifySlotRegion(encodeRegion(t, &swapped)); !errors.Is(err, ErrInvalid) {
		t.Fatal("substituted slot_pubkey went undetected")
	}
	// An added slot, and an old region spliced back.
	added := &SlotRegion{Header: r.Header, Slots: []SlotRecord{r.Slots[0], softwareSlot(SlotStandalonePassword)}}
	if err := g.VerifySlotRegion(encodeRegion(t, added)); !errors.Is(err, ErrInvalid) {
		t.Fatal("added slot went undetected")
	}
	old := &SlotRegion{Header: r.Header, Slots: []SlotRecord{hardwareSlot()}}
	if err := g.VerifySlotRegion(encodeRegion(t, old)); !errors.Is(err, ErrInvalid) {
		t.Fatal("spliced old region went undetected")
	}
	// The header: R25 is one of only two things authenticating it, so every
	// edit to it must change the hash.
	for name, mut := range map[string]func(*SlotRegionHeader){
		"entangle flipped":    func(h *SlotRegionHeader) { *h = SlotRegionHeader{} },
		"argon2_m downgraded": func(h *SlotRegionHeader) { h.Argon2M = MinArgon2MemKiB * 8 },
		"argon2_t downgraded": func(h *SlotRegionHeader) { h.Argon2T = 1 },
		"argon2_p changed":    func(h *SlotRegionHeader) { h.Argon2P = 1; h.Argon2M = 65536 },
		"entangle_salt swapped": func(h *SlotRegionHeader) {
			h.EntangleSalt = fill16(0x72)
		},
	} {
		edited := &SlotRegion{Header: r.Header, Slots: r.Slots}
		mut(&edited.Header)
		if err := g.VerifySlotRegion(encodeRegion(t, edited)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s went undetected", name)
		}
	}
	// The hash rides inside the registry plaintext.
	b, _ := g.Encode()
	dr, _ := DecodeRegistry(b)
	if dr.SlotRegionHash != g.SlotRegionHash {
		t.Fatal("hash lost in the round trip")
	}
}

func TestEnvelope(t *testing.T) {
	e := &Envelope{ArchiveID: fill16(0xA5), KID: fill16(0x5A)}
	b := e.Encode()
	if !bytes.Equal(b[:8], []byte("ENFOLDA\x01")) || b[8] != 1 || b[9] != 0 {
		t.Fatalf("envelope header %x", b[:12])
	}
	d, err := DecodeEnvelope(b)
	if err != nil || *d != *e {
		t.Fatalf("round trip: %v %+v", err, d)
	}
	b[12] ^= 1
	if _, err := DecodeEnvelope(b); err == nil {
		t.Fatal("tampered archive_id passed the checksum")
	}
	if _, err := DecodeEnvelope(b[:100]); err == nil {
		t.Fatal("short envelope accepted")
	}
}

func TestArchiveSuperblockAndIndexAAD(t *testing.T) {
	s := &ArchiveSuperblock{Seq: 2, IndexOff: 0x3000, IndexLen: 500, FreeMapOff: 0x4000, FreeMapLen: 20, FreeMapHash: fill32(0x77)}
	copy(s.IndexNonce[:], seq(1, 12))
	copy(s.IndexTag[:], seq(0x20, 16))
	b, err := s.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b[:8], []byte("ENFOLDS\x01")) {
		t.Fatalf("magic %x", b[:8])
	}
	d, err := DecodeArchiveSuperblock(b)
	if err != nil || !reflect.DeepEqual(s, d) {
		t.Fatalf("round trip: %v", err)
	}
	aad := s.IndexAAD(fill16(0xA5), fill16(0x5A))
	if len(aad) != 62 || aad[32] != 0x00 || aad[33] != 0x30 || aad[60] != 1 || aad[61] != 0 {
		t.Fatalf("index AAD %x", aad)
	}
	// Pick: higher seq wins, a damaged copy is reported, equal seq is corruption.
	s2 := *s
	s2.Seq = 3
	b2, _ := s2.Encode()
	if live, which, stale, err := PickArchiveSuperblock(b, b2); err != nil || which != CopyB || live.Seq != 3 || stale != nil {
		t.Fatalf("pick: %v %v %v %v", live, which, stale, err)
	}
	if live, which, stale, err := PickArchiveSuperblock(make([]byte, SuperblockSize), b); err != nil || which != CopyB || live.Seq != 2 || !errors.Is(stale, ErrInvalid) {
		t.Fatalf("pick with damaged A: %v %v %v %v", live, which, stale, err)
	}
	if _, _, _, err := PickArchiveSuperblock(b, b); !errors.Is(err, ErrInvalid) {
		t.Fatalf("equal seq accepted: %v", err)
	}
	// Bounds and extents.
	bad := &ArchiveSuperblock{Seq: 1, IndexOff: 0x100, IndexLen: 1, FreeMapOff: 0x4000, FreeMapLen: 4}
	if _, err := bad.Encode(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("index inside fixed regions accepted: %v", err)
	}
	bad = &ArchiveSuperblock{Seq: 1, IndexOff: 0x3000, IndexLen: MaxIndexLen + 1, FreeMapOff: 0x4000, FreeMapLen: 4}
	if _, err := bad.Encode(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("index_len above the cap accepted: %v", err)
	}
	if err := s.ValidateExtents(0x4000 + 20); err != nil {
		t.Fatalf("exact fit rejected: %v", err)
	}
	if err := s.ValidateExtents(0x4000 + 19); !errors.Is(err, ErrInvalid) {
		t.Fatalf("free map past EOF accepted: %v", err)
	}
	overlap := *s
	overlap.FreeMapOff = 0x3100
	if err := overlap.ValidateExtents(1 << 20); !errors.Is(err, ErrInvalid) {
		t.Fatalf("overlapping extents accepted: %v", err)
	}
}

// idN is a distinct record id for n ≥ 1, so that a test can build a chain
// longer than the 255 values fill16 offers.
func idN(n int) (a [16]byte) {
	binary.LittleEndian.PutUint64(a[:8], uint64(n))
	return
}

func dirRec(id [16]byte, parent [16]byte, name string) DirRecord {
	return DirRecord{DirID: id, State: FileLive, ParentID: parent, Name: name, ModifiedAt: 90, Revision: 1, LastWriter: fill16(0xA1)}
}

func fileRec(id [16]byte, parent [16]byte, name string) FileRecord {
	return FileRecord{FileID: id, State: FileLive, ParentID: parent, Name: name,
		OrigSize: 0, StoredSize: RawStoredSize(0), Storage: StorageRaw, DataOff: ArchiveDataStart,
		ChunkSize: ChunkSize, Alg: AlgAES256GCM, DEKEpoch: 1, Revision: 1}
}

// sampleIndex is a small tree (§11): one live directory under the root, one
// tombstoned directory under it, and three files — one in the live directory,
// one tombstone under the tombstoned directory, one at the root.
func sampleIndex() *Index {
	video := dirRec(fill16(0xD1), RootID, "video")
	old := DirRecord{DirID: fill16(0xD2), State: FileTombstone, ParentID: fill16(0xD1), Name: "old",
		ModifiedAt: 80, Revision: 3, LastWriter: fill16(0xA2)}
	f := FileRecord{FileID: fill16(0xF1), State: FileLive, ParentID: fill16(0xD1), Name: "holiday.mp4",
		OrigSize: 3*ChunkSize + 17, Storage: StorageRaw, ContentHash: fill32(0x99), DataOff: 0x5000,
		ChunkSize: ChunkSize, Alg: AlgAES256GCM, DEKEpoch: 2, DEKCreatedAt: 100, Revision: 1,
		LastWriter: fill16(0xD1), ModifiedAt: 101} // last_writer is not an identity (R39)
	f.StoredSize = RawStoredSize(f.OrigSize)
	copy(f.DEKNonce[:], seq(1, 12))
	copy(f.WrappedDEK[:], seq(0x30, 48))
	g := FileRecord{FileID: fill16(0xF2), State: FileTombstone, ParentID: fill16(0xD2), Name: "",
		Storage: StorageZstdDict, ChunkSize: ChunkSize, Alg: AlgAES256GCM}
	z := FileRecord{FileID: fill16(0xF3), State: FileLive, ParentID: RootID, Name: "empty.txt",
		OrigSize: 0, StoredSize: RawStoredSize(0), Storage: StorageRaw, DataOff: 0x9000,
		ChunkSize: ChunkSize, Alg: AlgAES256GCM}
	return &Index{Dict: fakeDict(1, 100), Dirs: []DirRecord{video, old}, Files: []FileRecord{f, g, z}}
}

// fakeDict is a byte string with a zstd dictionary's magic and ID (R27) and
// arbitrary bytes after; the format layer checks no more than that.
func fakeDict(id uint32, n int) []byte {
	d := append([]byte{0x37, 0xa4, 0x30, 0xec}, byte(id), byte(id>>8), byte(id>>16), byte(id>>24))
	return append(d, seq(8, n-8)...)
}

func TestIndexRoundTripAndValidation(t *testing.T) {
	x := sampleIndex()
	b, err := x.Encode()
	if err != nil {
		t.Fatal(err)
	}
	d, err := DecodeIndex(b)
	if err != nil || !reflect.DeepEqual(x, d) {
		t.Fatalf("round trip: %v", err)
	}
	// A directory record's own fields survive the trip (§11).
	if got := d.Dirs[0]; got.DirID != fill16(0xD1) || got.State != FileLive || got.ParentID != RootID ||
		got.Name != "video" || got.ModifiedAt != 90 || got.Revision != 1 || got.LastWriter != fill16(0xA1) {
		t.Fatalf("directory record: %+v", got)
	}
	if got := d.Dirs[1]; got.State != FileTombstone || got.ParentID != fill16(0xD1) || got.Name != "old" ||
		got.ModifiedAt != 80 || got.Revision != 3 || got.LastWriter != fill16(0xA2) {
		t.Fatalf("directory tombstone: %+v", got)
	}
	if RawStoredSize(0) != 16 || RawStoredSize(1) != 17 || RawStoredSize(ChunkSize) != ChunkSize+16 || RawStoredSize(ChunkSize+1) != ChunkSize+1+32 {
		t.Fatal("RawStoredSize")
	}
	if RawStoredSize(MaxOrigSize) == 0 || RawStoredSize(MaxOrigSize+1) != 0 || RawStoredSize(^uint64(0)) != 0 {
		t.Fatal("RawStoredSize bounds")
	}
	bad := func(name string, mut func(*Index)) {
		t.Helper()
		x := sampleIndex()
		mut(x)
		if _, err := x.Encode(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	bad("reserved alg", func(x *Index) { x.Files[0].Alg = AlgChaCha20Poly1305 })
	bad("unknown alg", func(x *Index) { x.Files[0].Alg = 7 })
	bad("chunk size", func(x *Index) { x.Files[0].ChunkSize = 4096 })
	bad("raw size mismatch", func(x *Index) { x.Files[0].StoredSize++ })
	bad("orig_size above the cap", func(x *Index) { x.Files[0].OrigSize = MaxOrigSize + 1; x.Files[0].StoredSize = 0 })
	bad("overflowing raw record", func(x *Index) { x.Files[0].OrigSize = ^uint64(0) - 65535; x.Files[0].StoredSize = ^uint64(0) - 65519 })
	bad("dict storage without dict", func(x *Index) { x.Dict = nil })
	bad("dict too large", func(x *Index) { x.Dict = fakeDict(1, MaxDictSize+1) })
	bad("dict wrong magic", func(x *Index) { x.Dict = seq(0, 100) })
	bad("dict ID 0", func(x *Index) { x.Dict = fakeDict(0, 100) })
	bad("dict too short", func(x *Index) { x.Dict = fakeDict(1, 8)[:7] })
	bad("empty file compressed", func(x *Index) { x.Files[2].Storage = StorageZstd })
	bad("live file without name", func(x *Index) { x.Files[0].Name = "" })
	bad("live directory without name", func(x *Index) { x.Dirs[0].Name = "" })
	bad("data in fixed region", func(x *Index) { x.Files[0].DataOff = 0x1000 })
	bad("unknown storage", func(x *Index) { x.Files[0].Storage = 9 })
	bad("unknown file state", func(x *Index) { x.Files[0].State = 0 })
	bad("unknown directory state", func(x *Index) { x.Dirs[0].State = 0 })
	if _, err := DecodeIndex(append(b, 1)); !errors.Is(err, ErrTrailing) {
		t.Errorf("trailing: %v", err)
	}
	// index_version 1 — the object-key model — is refused, never migrated (§11).
	v1 := append([]byte(nil), b...)
	binary.LittleEndian.PutUint32(v1[:4], 1)
	if _, err := DecodeIndex(v1); !errors.Is(err, ErrInvalid) {
		t.Errorf("index_version 1 accepted: %v", err)
	}
	// pack_id is reserved: non-zero bytes on the wire are ignored, and the record still decodes.
	off := 4 + 4 + len(x.Dict) + 4 // version, dict, dir_count
	for i := range x.Dirs {
		body, err := x.Dirs[i].body()
		if err != nil {
			t.Fatal(err)
		}
		off += 4 + len(body)
	}
	off += 4 + 4 // file_count and the first file's record_len
	packOff := off + 16 + 1 + 16 + 2 + len(x.Files[0].Name) + 8 + 8 + 1 + 32 + 8 + 4 + 2 + NonceSize + WrappedKeySize + 4 + 8
	junk := append([]byte(nil), b...)
	junk[packOff] = 0xAB
	if d, err := DecodeIndex(junk); err != nil || !reflect.DeepEqual(x, d) {
		t.Fatalf("pack_id not ignored on read: %v", err)
	}
}

// TestIndexTreeReaderRefusals patches valid bytes so that the tree breaks
// without the encoder's help: R39 is the reader's rule as much as the
// writer's, and an archive from an untrusted place never reaches Encode.
func TestIndexTreeReaderRefusals(t *testing.T) {
	x := sampleIndex()
	b, err := x.Encode()
	if err != nil {
		t.Fatal(err)
	}
	body0, err := x.Dirs[0].body()
	if err != nil {
		t.Fatal(err)
	}
	dir0 := 4 + 4 + len(x.Dict) + 4 + 4 // version, dict, dir_count, first record_len
	dir1 := dir0 + len(body0) + 4
	patch := func(name string, off int, v []byte) {
		t.Helper()
		p := append([]byte(nil), b...)
		copy(p[off:], v)
		if _, err := DecodeIndex(p); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s accepted on decode: %v", name, err)
		}
	}
	patch("all-zero dir_id", dir0, make([]byte, 16))
	patch("duplicate dir_id", dir1, x.Dirs[0].DirID[:])
	unknown := fill16(0xEE)
	patch("unknown parent", dir0+16+1, unknown[:])
}

func TestIndexTree(t *testing.T) {
	root := RootID
	ok := func(name string, x *Index) {
		t.Helper()
		if _, err := x.Encode(); err != nil {
			t.Errorf("%s refused: %v", name, err)
		}
	}
	bad := func(name string, x *Index) {
		t.Helper()
		if _, err := x.Encode(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s accepted: err = %v", name, err)
		}
	}

	// Identities (R39): no all-zero id, no id twice, one id space across both
	// record tables.
	bad("all-zero dir_id", &Index{Dirs: []DirRecord{dirRec(RootID, root, "a")}})
	bad("all-zero file_id", &Index{Files: []FileRecord{fileRec(RootID, root, "a")}})
	bad("duplicate dir_id", &Index{Dirs: []DirRecord{dirRec(idN(1), root, "a"), dirRec(idN(1), root, "b")}})
	bad("duplicate file_id", &Index{Files: []FileRecord{fileRec(idN(1), root, "a"), fileRec(idN(1), root, "b")}})
	bad("file_id equals a dir_id", &Index{
		Dirs:  []DirRecord{dirRec(idN(1), root, "a")},
		Files: []FileRecord{fileRec(idN(1), root, "b")},
	})
	// A tombstone counts alongside the live records in all three.
	dup := dirRec(idN(1), root, "gone")
	dup.State = FileTombstone
	bad("duplicate id against a tombstone", &Index{Dirs: []DirRecord{dirRec(idN(1), root, "a"), dup}})

	// Chains (R39): a live record's parent is the root or a live directory.
	bad("unknown parent, directory", &Index{Dirs: []DirRecord{dirRec(idN(1), idN(9), "a")}})
	bad("unknown parent, file", &Index{Files: []FileRecord{fileRec(idN(1), idN(9), "a")}})
	bad("file parented on a file", &Index{Files: []FileRecord{fileRec(idN(1), root, "a"), fileRec(idN(2), idN(1), "b")}})
	tomb := dirRec(idN(1), root, "gone")
	tomb.State = FileTombstone
	bad("tombstoned parent of a live directory", &Index{Dirs: []DirRecord{tomb, dirRec(idN(2), idN(1), "a")}})
	bad("tombstoned parent of a live file", &Index{
		Dirs:  []DirRecord{tomb},
		Files: []FileRecord{fileRec(idN(2), idN(1), "a")},
	})
	// A tombstone's parent may name anything, including another tombstone.
	deadChild := dirRec(idN(2), idN(1), "child")
	deadChild.State = FileTombstone
	deadFile := fileRec(idN(3), idN(9), "x")
	deadFile.State = FileTombstone
	deadFile.StoredSize, deadFile.DataOff = 0, 0
	ok("tombstones under anything", &Index{Dirs: []DirRecord{tomb, deadChild}, Files: []FileRecord{deadFile}})

	// Cycles.
	bad("self-parent", &Index{Dirs: []DirRecord{dirRec(idN(1), idN(1), "a")}})
	bad("2-cycle", &Index{Dirs: []DirRecord{dirRec(idN(1), idN(2), "a"), dirRec(idN(2), idN(1), "b")}})
	bad("3-cycle", &Index{Dirs: []DirRecord{
		dirRec(idN(1), idN(3), "a"), dirRec(idN(2), idN(1), "b"), dirRec(idN(3), idN(2), "c"),
	}})

	// Depth: MaxTreeDepth steps to the root are enough, one more is not.
	chain := func(n int) *Index {
		dirs := make([]DirRecord, 0, n)
		parent := root
		for i := 1; i <= n; i++ {
			dirs = append(dirs, dirRec(idN(i), parent, "d"))
			parent = idN(i)
		}
		return &Index{Dirs: dirs}
	}
	ok("depth 255", chain(MaxTreeDepth))
	bad("depth 256", chain(MaxTreeDepth+1))

	// The joined path of a live record is at most MaxPathLen bytes (R20).
	ok("joined path of 4096", indexWithPath(MaxPathLen))
	bad("joined path of 4097", indexWithPath(MaxPathLen+1))

	// Sibling uniqueness among the live children of one parent, files and
	// directories in one namespace, under simple case folding.
	bad("A.txt beside a.txt", &Index{Files: []FileRecord{fileRec(idN(1), root, "A.txt"), fileRec(idN(2), root, "a.txt")}})
	bad("two folders folding onto one name", &Index{Dirs: []DirRecord{dirRec(idN(1), root, "Photos"), dirRec(idN(2), root, "photos")}})
	bad("a file and a directory sharing a folded name", &Index{
		Dirs:  []DirRecord{dirRec(idN(1), root, "Notes")},
		Files: []FileRecord{fileRec(idN(2), root, "notes")},
	})
	ok("the same two names under different parents", &Index{
		Dirs: []DirRecord{dirRec(idN(1), root, "a"), dirRec(idN(2), root, "b"),
			dirRec(idN(3), idN(1), "x"), dirRec(idN(4), idN(2), "x")},
		Files: []FileRecord{fileRec(idN(5), idN(1), "n.txt"), fileRec(idN(6), idN(2), "n.txt")},
	})
	// A tombstone is not a live sibling, so a deleted record's name is free
	// (R32: keeping a tombstone's name reserves nothing).
	freed := dirRec(idN(1), root, "Report")
	freed.State = FileTombstone
	ok("a tombstone's name is not reserved", &Index{
		Dirs:  []DirRecord{freed},
		Files: []FileRecord{fileRec(idN(2), root, "report")},
	})
	// Changing only the case of a name is a rename, not a collision: one
	// record is not its own sibling.
	ok("one record, one name", &Index{Files: []FileRecord{fileRec(idN(1), root, "Report.TXT")}})
}

// indexWithPath builds an index holding one file whose joined path is exactly
// n bytes: a chain of directories, each name as long as R20 allows, and a last
// one sized to make up the difference.
func indexWithPath(n int) *Index {
	remain := n - 1 // the file's own name, "f"
	var dirs []DirRecord
	parent := RootID
	for i := 1; remain > 0; i++ {
		l := remain - 1
		if l > MaxNameUnits {
			l = MaxNameUnits
		}
		if l < 1 {
			panic("indexWithPath: no room for a directory name")
		}
		d := dirRec(idN(i), parent, strings.Repeat("d", l))
		dirs = append(dirs, d)
		parent = d.DirID
		remain -= l + 1
	}
	return &Index{Dirs: dirs, Files: []FileRecord{fileRec(idN(len(dirs)+1), parent, "f")}}
}

// R39 folds live sibling names with strings.EqualFold, and Validate compares
// a map key instead of every pair; the key has to answer the same question.
func TestFoldKeyMatchesEqualFold(t *testing.T) {
	names := []string{"a", "A", "report.txt", "Report.TXT", "k", "K", "K", "s", "S", "ſ",
		"straße", "STRAẞE", "ß", "ẞ", "i", "I", "İ", "ı",
		"Α", "α", "ς", "σ", "ab", "abc", "世界", "世"}
	for _, a := range names {
		for _, b := range names {
			if got, want := FoldKey(a) == FoldKey(b), strings.EqualFold(a, b); got != want {
				t.Errorf("FoldKey(%q)==FoldKey(%q) is %v, EqualFold is %v", a, b, got, want)
			}
		}
	}
}

func TestValidateName(t *testing.T) {
	good := []string{"a", "file.txt", "目录.mp4", "COM", "COM10", "LPT0", "config.txt", "a b.e",
		"..a", strings.Repeat("世", MaxNameUnits), strings.Repeat("a", MaxNameUnits)}
	for _, n := range good {
		if err := ValidateName(n); err != nil {
			t.Errorf("%q rejected: %v", n, err)
		}
	}
	badNames := []string{"", "/", "a/b", "dir/file.txt", "/abs", "a/", ".", "..", `a\b`, "C:x", "a:b",
		"a*", "q?", "<", ">", "|", `"`, "nul", "NUL.txt", "con", "Com1", "LPT9.log", "aux", "prn.",
		"x.", "x ", "a\x00b", "a\tb", "a\x7fb", strings.Repeat("世", MaxNameUnits+1),
		strings.Repeat("a", MaxNameUnits+1), "\U0001F600" + strings.Repeat("a", MaxNameUnits-1)}
	for _, n := range badNames {
		if err := ValidateName(n); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q accepted", n)
		}
	}
	// R20 says no control character, which is every category Cc: the C1
	// block U+0080–U+009F as much as C0 and DEL. NEL (U+0085) is a line
	// break on the platforms this extracts to; U+00E9 is not a control character
	// and a name is not held to ASCII (the outside audit of 2026-09-09).
	for _, n := range []string{"a\u0085b", "a\u0080b", "a\u009fb", "\u0085"} {
		if err := ValidateName(n); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q accepted: a C1 control character is a control character", n)
		}
	}
	for _, n := range []string{"caf\u00e9.txt", "\u00a0nbsp", "na\u00efve"} {
		if err := ValidateName(n); err != nil {
			t.Errorf("%q rejected: %v", n, err)
		}
	}
	// A non-BMP scalar is two UTF-16 code units, so 128 of them are 256.
	if err := ValidateName(strings.Repeat("\U0001F600", 128)); !errors.Is(err, ErrInvalid) {
		t.Errorf("256 code units accepted")
	}
	if err := ValidateName(strings.Repeat("\U0001F600", 127) + "a"); err != nil {
		t.Errorf("255 code units rejected: %v", err)
	}
	// Tombstones keep whatever name they had, even an empty one or a path.
	x := sampleIndex()
	x.Dirs[1].Name = "../was-here"
	x.Files[1].Name = "a/b\\c"
	if _, err := x.Encode(); err != nil {
		t.Errorf("tombstone name rejected: %v", err)
	}
}

func TestFreeMap(t *testing.T) {
	m := &FreeMap{Extents: []Extent{{0x3000, 0x1000}, {0x8000, 16}}}
	b, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 4+32 {
		t.Fatalf("len %d", len(b))
	}
	d, err := DecodeFreeMap(b)
	if err != nil || !reflect.DeepEqual(m, d) {
		t.Fatalf("round trip: %v", err)
	}
	enc, h, err := m.Hash()
	if err != nil || !bytes.Equal(enc, b) {
		t.Fatalf("Hash: %v", err)
	}
	if _, err := DecodeFreeMapChecked(b, h); err != nil {
		t.Fatalf("checked decode: %v", err)
	}
	h[0] ^= 1
	if _, err := DecodeFreeMapChecked(b, h); !errors.Is(err, ErrInvalid) {
		t.Fatalf("hash mismatch accepted: %v", err)
	}
	for name, ext := range map[string][]Extent{
		"overlap":  {{0x3000, 0x2000}, {0x4000, 1}},
		"unsorted": {{0x8000, 1}, {0x3000, 1}},
		"empty":    {{0x3000, 0}},
		"fixed":    {{0x100, 1}},
		"overflow": {{^uint64(0) - 1, 4}},
	} {
		if _, err := (&FreeMap{Extents: ext}).Encode(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
	if d, err := DecodeFreeMap([]byte{0, 0, 0, 0}); err != nil || len(d.Extents) != 0 {
		t.Fatalf("empty map: %v", err)
	}
	if _, err := DecodeFreeMap([]byte{0xFF, 0xFF, 0xFF, 0x7F}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("hostile count: %v", err)
	}
}

func TestChunkNonceAndAAD(t *testing.T) {
	if n := ChunkNonce(0, false); n != [12]byte{} {
		t.Fatalf("nonce 0: %x", n)
	}
	if n := ChunkNonce(1, false); hex.EncodeToString(n[:]) != "010000000000000000000000" {
		t.Fatalf("nonce 1: %x", n)
	}
	if n := ChunkNonce(0x0102030405060708, true); hex.EncodeToString(n[:]) != "080706050403020100000001" {
		t.Fatalf("nonce LE+final: %x", n)
	}
	aad := ChunkAAD(fill16(0xA5), fill16(0xF1), AlgAES256GCM, ChunkSize)
	if len(aad) != 38 || hex.EncodeToString(aad[32:]) != "010000000100" {
		t.Fatalf("chunk AAD %x", aad)
	}
}

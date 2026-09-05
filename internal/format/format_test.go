package format

import (
	"bytes"
	"crypto/ecdh"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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

func hardwareSlot() SlotRecord {
	epk := p256Point(0x10)
	pub := p256Point(0x50)
	s := SlotRecord{
		State: SlotActive, Type: SlotExternalECDH, KeySource: KeySourceYubiKeyPIV, Curve: CurveP256,
		RecipientID: fill16(0xA1), Flags: FlagEntangledPassword, Label: "YubiKey 5C — desk", CreatedAt: 1_756_000_000,
		EPK: epk, SlotPubkey: pub, Salt: fill32(0x11), Argon2M: 524288, Argon2T: 2, Argon2P: 4, SlotSalt: fill32(0x22),
	}
	copy(s.WrapNonce[:], seq(0x30, 12))
	copy(s.WrappedVMK[:], seq(0x40, 56))
	return s
}

func softwareSlot(t SlotType) SlotRecord {
	s := SlotRecord{
		State: SlotActive, Type: t, Curve: CurveX25519,
		RecipientID: fill16(0xB2), Label: "recovery", CreatedAt: 1_756_000_001,
		EPK: seq(0x60, 32), SlotPubkey: seq(0x70, 32), Salt: fill32(0x33), SlotSalt: fill32(0x44),
		MLKEMEK: bytes.Repeat([]byte{0xEE}, MLKEMEKSize), MLKEMCT: bytes.Repeat([]byte{0xCC}, MLKEMCTSize),
	}
	if t == SlotStandalonePassword {
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
	_, err := DecodeSlotRegion([]byte{1, 0, 0, 0, 0, 0, 0, 0, 5})
	if !errors.Is(err, ErrTruncated) || !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "offset") {
		t.Fatalf("truncation error %q lacks class or context", err)
	}
}

func TestKeystoreSuperblockRoundTripAndLayout(t *testing.T) {
	s := &KeystoreSuperblock{Seq: 7, VaultID: fill16(0x55), SlotRegionOff: SlotRegionBOff, SlotRegionLen: 7000,
		RegistryOff: RegistryMinOff, RegistryLen: 1234, VMKGeneration: 3, RotationPending: 1}
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
	if b[8+2+2+8+16+8+8+8+8+12+16+8] != 1 { // rotation_pending sits at 104
		t.Fatal("rotation_pending not at offset 104")
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
	// AAD layout: 16 + 8 + 8 + 12 + 2.
	aad := s.RegistryAAD()
	if len(aad) != 46 || !bytes.Equal(aad[:16], s.VaultID[:]) || aad[16] != 0x00 || aad[17] != 0x20 || aad[18] != 0x04 || aad[44] != 1 || aad[45] != 0 {
		t.Fatalf("registry AAD %x", aad)
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
		s := &KeystoreSuperblock{Seq: seq, SlotRegionOff: SlotRegionAOff, SlotRegionLen: 8, RegistryOff: RegistryMinOff}
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

func TestSlotRegionRoundTrip(t *testing.T) {
	slots := []SlotRecord{hardwareSlot(), softwareSlot(SlotRecovery), softwareSlot(SlotStandalonePassword), {State: SlotEmpty}}
	b, err := EncodeSlotRegion(slots)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > 8*1024 {
		t.Fatalf("four slots encode to %d bytes; expected ~7 KB", len(b))
	}
	d, err := DecodeSlotRegion(b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(slots, d) {
		t.Fatalf("round trip mismatch")
	}
	if !SlotRegionCanonical(b, d) {
		t.Fatal("region round trip is not byte-exact")
	}
	// A hardware slot record is exactly 338 bytes with this label (R18).
	hs := hardwareSlot()
	one, err := hs.Encode()
	if err != nil || len(one) != 338 {
		t.Fatalf("hardware slot record: %d bytes, %v", len(one), err)
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
	s2 := s
	s2.Argon2M = 8
	aad2, _ := s2.AAD(fill16(0x55))
	if bytes.Equal(aad, aad2) {
		t.Fatal("argon2_m is not in the AAD")
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
	bad("argon2 work above the cap", func(s *SlotRecord) { s.Argon2M = MaxArgon2MemKiB; s.Argon2T = 5 })
	bad("hardware slot with mlkem", func(s *SlotRecord) { s.MLKEMEK = seq(0, 10) })
	bad("entangled password without argon2", func(s *SlotRecord) { s.Argon2T = 0 })
	bad("argon2 m below 8p", func(s *SlotRecord) { s.Argon2M = 16; s.Argon2P = 4 })
	bad("unknown state", func(s *SlotRecord) { s.State = 7 })
	bad("unknown type", func(s *SlotRecord) { s.Type = 4 })
	bad("empty slot with junk", func(s *SlotRecord) { s.State = SlotEmpty })
	// R24: bounds, and no parameters on a slot that does not run Argon2id.
	bad("argon2 m above the cap", func(s *SlotRecord) { s.Argon2M = MaxArgon2MemKiB + 1 })
	bad("argon2 t above the cap", func(s *SlotRecord) { s.Argon2T = MaxArgon2Time + 1 })
	bad("argon2 p above the cap", func(s *SlotRecord) { s.Argon2P = MaxArgon2Threads + 1; s.Argon2M = 8 * (MaxArgon2Threads + 1) })
	bad("argon2 4 TiB", func(s *SlotRecord) { s.Argon2M = 0xFFFFFFFF })
	bad("params on a slot without a password", func(s *SlotRecord) { s.Flags &^= FlagEntangledPassword })
	if s := hardwareSlot(); true {
		s.Flags &^= FlagEntangledPassword
		s.Argon2M, s.Argon2T, s.Argon2P = 0, 0, 0
		if _, err := s.Encode(); err != nil {
			t.Errorf("hardware slot without password and zero params rejected: %v", err)
		}
	}
	if s := softwareSlot(SlotRecovery); true {
		s.Argon2M = 8192
		if _, err := s.Encode(); !errors.Is(err, ErrInvalid) {
			t.Errorf("recovery slot with argon2 params accepted: %v", err)
		}
	}
	if s := hardwareSlot(); true {
		s.Argon2M = MaxArgon2MemKiB
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
	if _, err := EncodeSlotRegion(many); !errors.Is(err, ErrInvalid) {
		t.Errorf("33 slots accepted: %v", err)
	}
	b, _ := EncodeSlotRegion([]SlotRecord{hardwareSlot()})
	if _, err := DecodeSlotRegion(append(b, 0)); !errors.Is(err, ErrTrailing) {
		t.Errorf("trailing byte accepted: %v", err)
	}
	if _, err := DecodeSlotRegion(b[:len(b)-1]); !errors.Is(err, ErrTruncated) {
		t.Errorf("truncated region: %v", err)
	}
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
		LastWrittenAt: 30, Revision: 4, LastWriter: fill16(0xD1), Versions: []VersionRecord{v1, v2},
	}}
	var pk [32]byte
	copy(pk[:], seq(0x10, 32))
	g.Peers = []PeerPin{{IdentityPubkey: pk, DeviceName: "phone", PairedAt: 1, LastSeenAt: 2,
		Capabilities: CapOpenArchive | CapFullSync, DeviceClass: DeviceEnrolledPersonal}}
	return g
}

func TestRegistryRoundTripAndValidation(t *testing.T) {
	g := sampleRegistry()
	b, err := g.Encode()
	if err != nil {
		t.Fatal(err)
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
	bad("unknown policy bit", func(g *Registry) { g.Archives[0].Policy = 1 << 4 })
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
	if _, err := DecodeRegistry(b[:len(b)-3]); !errors.Is(err, ErrTruncated) {
		t.Errorf("truncated registry: %v", err)
	}
	// A short peer key on the wire fails closed.
	short := append([]byte(nil), b...)
	peerOff := len(b) - (2 + 32 + 2 + len("phone") + 8 + 8 + 4 + 1)
	short[peerOff] = 31
	short = append(short[:peerOff+2+31], short[peerOff+2+32:]...)
	if _, err := DecodeRegistry(short); !errors.Is(err, ErrInvalid) {
		t.Errorf("31-byte peer key accepted: %v", err)
	}
	// A hostile archive_count must not allocate.
	h := append([]byte(nil), b...)
	copy(h[4+16+8+48+12+32:], []byte{0xFF, 0xFF, 0xFF, 0x7F})
	if _, err := DecodeRegistry(h); !errors.Is(err, ErrInvalid) {
		t.Errorf("hostile count: %v", err)
	}
}

// R25: the registry's authenticated hash of the slot region catches a
// substituted public key, an added record, and a spliced-in old region.
func TestSlotRegionHashDetectsSubstitution(t *testing.T) {
	region, err := EncodeSlotRegion([]SlotRecord{hardwareSlot(), softwareSlot(SlotRecovery)})
	if err != nil {
		t.Fatal(err)
	}
	g := sampleRegistry()
	g.SlotRegionHash = SlotRegionHash(region)
	if g.SlotRegionHash != sha256.Sum256(region) {
		t.Fatal("SlotRegionHash is not SHA-256 of the region")
	}
	if err := g.VerifySlotRegion(region); err != nil {
		t.Fatalf("intact region rejected: %v", err)
	}
	// The attack: replace the recovery slot's public key with the attacker's.
	slots, _ := DecodeSlotRegion(region)
	slots[1].SlotPubkey = seq(0x99, 32)
	swapped, _ := EncodeSlotRegion(slots)
	if err := g.VerifySlotRegion(swapped); !errors.Is(err, ErrInvalid) {
		t.Fatal("substituted slot_pubkey went undetected")
	}
	// An added slot, and an old region spliced back.
	added, _ := EncodeSlotRegion(append(slots[:1], softwareSlot(SlotStandalonePassword)))
	if err := g.VerifySlotRegion(added); !errors.Is(err, ErrInvalid) {
		t.Fatal("added slot went undetected")
	}
	old, _ := EncodeSlotRegion([]SlotRecord{hardwareSlot()})
	if err := g.VerifySlotRegion(old); !errors.Is(err, ErrInvalid) {
		t.Fatal("spliced old region went undetected")
	}
	// The hash rides inside the registry plaintext.
	b, _ := g.Encode()
	d, _ := DecodeRegistry(b)
	if d.SlotRegionHash != g.SlotRegionHash {
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

func sampleIndex() *Index {
	f := FileRecord{FileID: fill16(0xF1), State: FileLive, Name: "video/holiday.mp4", OrigSize: 3*ChunkSize + 17,
		Storage: StorageRaw, ContentHash: fill32(0x99), DataOff: 0x5000, ChunkSize: ChunkSize, Alg: AlgAES256GCM,
		DEKEpoch: 2, DEKCreatedAt: 100, Revision: 1, LastWriter: fill16(0xD1), ModifiedAt: 101}
	f.StoredSize = RawStoredSize(f.OrigSize)
	copy(f.DEKNonce[:], seq(1, 12))
	copy(f.WrappedDEK[:], seq(0x30, 48))
	g := FileRecord{FileID: fill16(0xF2), State: FileTombstone, Name: "", Storage: StorageZstdDict, ChunkSize: ChunkSize, Alg: AlgAES256GCM}
	z := FileRecord{FileID: fill16(0xF3), State: FileLive, Name: "empty.txt", OrigSize: 0, StoredSize: RawStoredSize(0),
		Storage: StorageZstd, DataOff: 0x9000, ChunkSize: ChunkSize, Alg: AlgAES256GCM}
	return &Index{Dict: seq(0, 100), Files: []FileRecord{f, g, z}}
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
	bad("live file without name", func(x *Index) { x.Files[0].Name = "" })
	bad("duplicate file id", func(x *Index) { x.Files[2].FileID = x.Files[0].FileID })
	bad("data in fixed region", func(x *Index) { x.Files[0].DataOff = 0x1000 })
	bad("unknown storage", func(x *Index) { x.Files[0].Storage = 9 })
	bad("unknown state", func(x *Index) { x.Files[0].State = 0 })
	if _, err := DecodeIndex(append(b, 1)); !errors.Is(err, ErrTrailing) {
		t.Errorf("trailing: %v", err)
	}
	// pack_id is reserved: non-zero bytes on the wire are ignored, and the record still decodes.
	rec0 := 4 + 4 + 100 + 4 + 4 // first record body offset
	packOff := rec0 + 16 + 1 + 2 + len("video/holiday.mp4") + 8 + 8 + 1 + 32 + 8 + 4 + 2 + NonceSize + WrappedKeySize + 4 + 8
	junk := append([]byte(nil), b...)
	junk[packOff] = 0xAB
	if d, err := DecodeIndex(junk); err != nil || !reflect.DeepEqual(x, d) {
		t.Fatalf("pack_id not ignored on read: %v", err)
	}
}

func TestValidateFileName(t *testing.T) {
	good := []string{"a", "dir/file.txt", "深/层/目录.mp4", "COM", "COM10", "LPT0", "config.txt", "a b/c d.e"}
	for _, n := range good {
		if err := ValidateFileName(n); err != nil {
			t.Errorf("%q rejected: %v", n, err)
		}
	}
	badNames := []string{"", "/abs", "a/", "a//b", ".", "..", "a/../b", "./a", `a\b`, "C:x", "a:b", "a*", "q?", "<", ">", "|", `"`,
		"nul", "NUL.txt", "con", "Com1", "LPT9.log", "aux", "prn.", "x.", "x ", "a\x00b", "a\tb", "a\x7fb", strings.Repeat("a", MaxFileNameLen+1)}
	for _, n := range badNames {
		if err := ValidateFileName(n); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q accepted", n)
		}
	}
	// Tombstones keep whatever name they had, even an empty one.
	x := sampleIndex()
	x.Files[1].Name = "../was-here"
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

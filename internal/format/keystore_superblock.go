package format

import (
	"bytes"
	"crypto/sha256"
)

// MaxRegistryLen bounds registry_len (docs/FORMAT.md R19). The keystore is "a
// few MB even with thousands of archives" (§4); a value beyond this is a
// corrupt superblock, not a large vault.
const MaxRegistryLen = 64 << 20

// KeystoreSuperblock is the 4 KiB keystore superblock (§5). Two copies
// alternate; the valid one with the higher Seq is live.
//
// rotation_pending, on the wire between vmk_generation and modified_at, has no
// field here: it is reserved since Revision 2 (§5, §18.3), so Encode writes
// zero and Decode skips it. A non-zero byte on disk therefore decodes without
// error and re-encodes as zero — failing on a checksummed plaintext byte would
// hand anyone with write access a one-byte denial of service.
type KeystoreSuperblock struct {
	Seq           uint64
	VaultID       [16]byte
	SlotRegionOff uint64 // SlotRegionAOff or SlotRegionBOff
	SlotRegionLen uint64 // encoded length of the live slot region, in [MinSlotRegionLen, SlotRegionSize]
	RegistryOff   uint64 // ≥ RegistryMinOff
	RegistryLen   uint64 // ciphertext length, excluding the tag; ≤ MaxRegistryLen
	RegistryNonce [NonceSize]byte
	RegistryTag   [TagSize]byte
	// VMKGeneration is 1 at creation and incremented on each rotation, so 0
	// never exists (§5) and is rejected here in both directions.
	VMKGeneration uint64
	// ModifiedAt is the wall-clock time (Unix seconds) of the commit that
	// sealed the registry this superblock points at; never decreases
	// across commits; an export's is the time of the export. Readable
	// before any unlock, so two copies of a keystore can be told apart;
	// authenticated through the registry AAD (R35).
	ModifiedAt int64
}

func (s *KeystoreSuperblock) validate() error {
	if _, ok := SlotRegionCopy(s.SlotRegionOff); !ok {
		return invalidf("keystore superblock: slot_region_off 0x%x is neither copy A nor copy B", s.SlotRegionOff)
	}
	if s.SlotRegionLen < MinSlotRegionLen || s.SlotRegionLen > SlotRegionSize {
		return invalidf("keystore superblock: slot_region_len %d outside [%d, %d]", s.SlotRegionLen, MinSlotRegionLen, SlotRegionSize)
	}
	if s.VMKGeneration == 0 {
		return invalidf("keystore superblock: vmk_generation 0 never exists")
	}
	if s.RegistryOff < RegistryMinOff {
		return invalidf("keystore superblock: registry_off 0x%x is inside the fixed regions", s.RegistryOff)
	}
	if s.ModifiedAt < 0 {
		return invalidf("keystore superblock: modified_at %d is negative", s.ModifiedAt)
	}
	if s.RegistryLen > MaxRegistryLen {
		return invalidf("keystore superblock: registry_len %d exceeds %d", s.RegistryLen, MaxRegistryLen)
	}
	return nil
}

// ValidateExtents checks that the registry ciphertext and its tag lie inside a
// file of the given size. The fixed regions are required to exist in full.
func (s *KeystoreSuperblock) ValidateExtents(fileSize uint64) error {
	if fileSize < RegistryMinOff {
		return invalidf("keystore file of %d bytes is shorter than its fixed regions (%d)", fileSize, RegistryMinOff)
	}
	end := s.RegistryOff + s.RegistryLen + TagSize // RegistryLen is bounded; RegistryOff is not, hence the first test
	if s.RegistryOff > fileSize || end > fileSize {
		return invalidf("registry extent [0x%x, 0x%x) exceeds the file size %d", s.RegistryOff, end, fileSize)
	}
	return nil
}

// LiveSlotRegion reports which slot-region copy the superblock points at.
func (s *KeystoreSuperblock) LiveSlotRegion() Copy {
	c, _ := SlotRegionCopy(s.SlotRegionOff)
	return c
}

// Encode returns the 4096-byte superblock with its checksum filled in.
func (s *KeystoreSuperblock) Encode() ([]byte, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	w := &writer{b: make([]byte, 0, SuperblockSize)}
	w.fixed(KeystoreMagic[:])
	w.u16(FormatVersion)
	w.u16(0) // reserved0
	w.u64(s.Seq)
	w.fixed(s.VaultID[:])
	w.u64(s.SlotRegionOff)
	w.u64(s.SlotRegionLen)
	w.u64(s.RegistryOff)
	w.u64(s.RegistryLen)
	w.fixed(s.RegistryNonce[:])
	w.fixed(s.RegistryTag[:])
	w.u64(s.VMKGeneration)
	w.u8(0) // rotation_pending: reserved since Revision 2 (§5, §18.3)
	w.i64(s.ModifiedAt)
	w.zeros(checksumOffset - len(w.b)) // reserved1
	sum := sha256.Sum256(w.b)
	w.fixed(sum[:])
	return w.done()
}

// DecodeKeystoreSuperblock parses and checksums one 4096-byte superblock.
func DecodeKeystoreSuperblock(b []byte) (*KeystoreSuperblock, error) {
	if len(b) != SuperblockSize {
		return nil, invalidf("keystore superblock is %d bytes, want %d", len(b), SuperblockSize)
	}
	if !bytes.Equal(b[:8], KeystoreMagic[:]) {
		return nil, invalidf("keystore superblock: bad magic")
	}
	sum := sha256.Sum256(b[:checksumOffset])
	if !bytes.Equal(sum[:], b[checksumOffset:]) {
		return nil, invalidf("keystore superblock: checksum mismatch")
	}
	r := newReader(b[8:checksumOffset], "keystore superblock")
	if v := r.u16(); v != FormatVersion {
		return nil, versionf("keystore superblock", v)
	}
	r.skip(2) // reserved0: ignored on read (§1)
	s := &KeystoreSuperblock{}
	s.Seq = r.u64()
	r.fixed(s.VaultID[:])
	s.SlotRegionOff = r.u64()
	s.SlotRegionLen = r.u64()
	s.RegistryOff = r.u64()
	s.RegistryLen = r.u64()
	r.fixed(s.RegistryNonce[:])
	r.fixed(s.RegistryTag[:])
	s.VMKGeneration = r.u64()
	r.skip(1) // rotation_pending: reserved, ignored on read (§1, §5)
	s.ModifiedAt = r.i64()
	if r.err != nil { // cannot happen with a fixed-size input, kept for symmetry
		return nil, r.err
	}
	// reserved1: ignored on read.
	if err := s.validate(); err != nil {
		return nil, err
	}
	return s, nil
}

// RegistryAAD is the AAD for the registry ciphertext (§7):
// vault_id ‖ seq ‖ registry_off ‖ registry_len ‖ registry_nonce ‖
// format_version ‖ modified_at. A pure byte construction over the superblock
// as given.
//
// Seq is in it for the same reason it is in the index AAD (§5, §7): the
// superblock is checksummed and not authenticated, so without it a registry
// kept from an earlier commit could be promoted by raising the plaintext seq
// and recomputing the checksum. With it, that file fails to authenticate.
func (s *KeystoreSuperblock) RegistryAAD() []byte {
	w := &writer{b: make([]byte, 0, 16+8+8+8+NonceSize+2+8)}
	w.fixed(s.VaultID[:])
	w.u64(s.Seq)
	w.u64(s.RegistryOff)
	w.u64(s.RegistryLen)
	w.fixed(s.RegistryNonce[:])
	w.u16(FormatVersion)
	w.i64(s.ModifiedAt)
	return w.b
}

// PickKeystoreSuperblock chooses the live superblock from copies A and B: the
// valid one with the higher Seq. stale is the losing copy's decode error when
// that copy was damaged rather than merely older; err is set when neither copy
// is usable.
func PickKeystoreSuperblock(a, b []byte) (live *KeystoreSuperblock, which Copy, stale error, err error) {
	return pick(a, b, DecodeKeystoreSuperblock, func(s *KeystoreSuperblock) uint64 { return s.Seq })
}

package format

import (
	"bytes"
	"crypto/sha256"
)

// Bounds on the archive's variable extents (docs/FORMAT.md R19). Beyond these a
// superblock is corrupt, not describing a large archive.
const (
	MaxIndexLen   = 1 << 30
	MaxFreeMapLen = 64 << 20
)

// Envelope is the plaintext 4 KiB archive envelope (§10). It says only what is
// needed to find the right key, and nothing in it is trusted.
type Envelope struct {
	ArchiveID [16]byte
	KID       [16]byte
}

// Encode returns the 4096-byte envelope with its checksum filled in. The
// envelope has no invalid states, so this cannot fail.
func (e *Envelope) Encode() []byte {
	w := &writer{b: make([]byte, 0, SuperblockSize)}
	w.fixed(ArchiveMagic[:])
	w.u16(FormatVersion)
	w.u16(0)
	w.fixed(e.ArchiveID[:])
	w.fixed(e.KID[:])
	w.zeros(checksumOffset - len(w.b))
	sum := sha256.Sum256(w.b)
	w.fixed(sum[:])
	return w.b
}

// DecodeEnvelope parses and checksums an envelope.
func DecodeEnvelope(b []byte) (*Envelope, error) {
	if len(b) != SuperblockSize {
		return nil, invalidf("envelope is %d bytes, want %d", len(b), SuperblockSize)
	}
	if !bytes.Equal(b[:8], ArchiveMagic[:]) {
		return nil, invalidf("envelope: bad magic")
	}
	sum := sha256.Sum256(b[:checksumOffset])
	if !bytes.Equal(sum[:], b[checksumOffset:]) {
		return nil, invalidf("envelope: checksum mismatch")
	}
	r := newReader(b[8:checksumOffset], "envelope")
	// The checksum has already held, so this envelope is what a writer meant
	// to leave: an unsupported version is refused as a version, never
	// recovered from as damage (§10, R33).
	if v := r.u16(); v != FormatVersion {
		return nil, versionf("envelope", v)
	}
	r.skip(2)
	e := &Envelope{}
	r.fixed(e.ArchiveID[:])
	r.fixed(e.KID[:])
	return e, r.err
}

// ArchiveSuperblock is the 4 KiB archive superblock (§11). It mirrors the
// keystore superblock: seq, the index extent with its AEAD nonce and tag, the
// free-space map extent with its hash, and a checksum. Two copies alternate.
type ArchiveSuperblock struct {
	Seq         uint64
	IndexOff    uint64 // ≥ ArchiveDataStart
	IndexLen    uint64 // ciphertext length, excluding the tag; ≤ MaxIndexLen
	IndexNonce  [NonceSize]byte
	IndexTag    [TagSize]byte
	FreeMapOff  uint64   // ≥ ArchiveDataStart
	FreeMapLen  uint64   // in [4, MaxFreeMapLen]
	FreeMapHash [32]byte // SHA-256 of the encoded free-space map (§13)
}

func (s *ArchiveSuperblock) validate() error {
	if s.IndexOff < ArchiveDataStart {
		return invalidf("archive superblock: index_off 0x%x is inside the fixed regions", s.IndexOff)
	}
	if s.IndexLen > MaxIndexLen {
		return invalidf("archive superblock: index_len %d exceeds %d", s.IndexLen, MaxIndexLen)
	}
	if s.FreeMapOff < ArchiveDataStart {
		return invalidf("archive superblock: freemap_off 0x%x is inside the fixed regions", s.FreeMapOff)
	}
	if s.FreeMapLen < 4 || s.FreeMapLen > MaxFreeMapLen {
		return invalidf("archive superblock: freemap_len %d outside [4, %d]", s.FreeMapLen, MaxFreeMapLen)
	}
	return nil
}

// ValidateExtents checks that the index ciphertext with its tag and the
// free-space map lie inside a file of the given size and do not overlap each
// other. Lengths are bounded by validate, so the sums cannot overflow.
func (s *ArchiveSuperblock) ValidateExtents(fileSize uint64) error {
	if fileSize < ArchiveDataStart {
		return invalidf("archive file of %d bytes is shorter than its fixed regions (%d)", fileSize, ArchiveDataStart)
	}
	iEnd := s.IndexOff + s.IndexLen + TagSize
	if s.IndexOff > fileSize || iEnd > fileSize {
		return invalidf("index extent [0x%x, 0x%x) exceeds the file size %d", s.IndexOff, iEnd, fileSize)
	}
	fEnd := s.FreeMapOff + s.FreeMapLen
	if s.FreeMapOff > fileSize || fEnd > fileSize {
		return invalidf("free-map extent [0x%x, 0x%x) exceeds the file size %d", s.FreeMapOff, fEnd, fileSize)
	}
	if s.IndexOff < fEnd && s.FreeMapOff < iEnd {
		return invalidf("index extent [0x%x, 0x%x) overlaps free-map extent [0x%x, 0x%x)", s.IndexOff, iEnd, s.FreeMapOff, fEnd)
	}
	return nil
}

// Encode returns the 4096-byte superblock with its checksum filled in.
func (s *ArchiveSuperblock) Encode() ([]byte, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	w := &writer{b: make([]byte, 0, SuperblockSize)}
	w.fixed(ArchiveSuperblockMagic[:])
	w.u16(FormatVersion)
	w.u16(0)
	w.u64(s.Seq)
	w.u64(s.IndexOff)
	w.u64(s.IndexLen)
	w.fixed(s.IndexNonce[:])
	w.fixed(s.IndexTag[:])
	w.u64(s.FreeMapOff)
	w.u64(s.FreeMapLen)
	w.fixed(s.FreeMapHash[:])
	w.zeros(checksumOffset - len(w.b))
	sum := sha256.Sum256(w.b)
	w.fixed(sum[:])
	return w.done()
}

// DecodeArchiveSuperblock parses and checksums one archive superblock.
func DecodeArchiveSuperblock(b []byte) (*ArchiveSuperblock, error) {
	if len(b) != SuperblockSize {
		return nil, invalidf("archive superblock is %d bytes, want %d", len(b), SuperblockSize)
	}
	if !bytes.Equal(b[:8], ArchiveSuperblockMagic[:]) {
		return nil, invalidf("archive superblock: bad magic")
	}
	sum := sha256.Sum256(b[:checksumOffset])
	if !bytes.Equal(sum[:], b[checksumOffset:]) {
		return nil, invalidf("archive superblock: checksum mismatch")
	}
	r := newReader(b[8:checksumOffset], "archive superblock")
	if v := r.u16(); v != FormatVersion {
		return nil, versionf("archive superblock", v)
	}
	r.skip(2)
	s := &ArchiveSuperblock{}
	s.Seq = r.u64()
	s.IndexOff = r.u64()
	s.IndexLen = r.u64()
	r.fixed(s.IndexNonce[:])
	r.fixed(s.IndexTag[:])
	s.FreeMapOff = r.u64()
	s.FreeMapLen = r.u64()
	r.fixed(s.FreeMapHash[:])
	if r.err != nil {
		return nil, r.err
	}
	if err := s.validate(); err != nil {
		return nil, err
	}
	return s, nil
}

// IndexAAD is the AAD for the file index ciphertext (§11):
// archive_id ‖ kid ‖ seq ‖ index_off ‖ index_len ‖ index_nonce ‖ format_version.
// A pure byte construction over the superblock as given.
//
// Seq is in it because the superblock is checksummed and not authenticated
// (§11, R36): an index authenticates under the sequence number it was sealed
// with and under no other, so an older index kept in place under a raised
// plaintext seq — with the unkeyed checksum recomputed — fails to open rather
// than passing as the current state.
func (s *ArchiveSuperblock) IndexAAD(archiveID, kid [16]byte) []byte {
	w := &writer{b: make([]byte, 0, 16+16+8+8+8+NonceSize+2)}
	w.fixed(archiveID[:])
	w.fixed(kid[:])
	w.u64(s.Seq)
	w.u64(s.IndexOff)
	w.u64(s.IndexLen)
	w.fixed(s.IndexNonce[:])
	w.u16(FormatVersion)
	return w.b
}

// PickArchiveSuperblock chooses the live superblock from copies A and B: the
// valid one with the higher Seq. stale is the losing copy's decode error when
// that copy was damaged rather than merely older; err is set when neither copy
// is usable.
func PickArchiveSuperblock(a, b []byte) (live *ArchiveSuperblock, which Copy, stale error, err error) {
	return pick(a, b, DecodeArchiveSuperblock, func(s *ArchiveSuperblock) uint64 { return s.Seq })
}

// Extent is one free extent (§13).
type Extent struct {
	Off uint64
	Len uint64
}

// FreeMap is the free-space map (§13): extents sorted by offset, none empty,
// none overlapping.
type FreeMap struct {
	Extents []Extent
}

// Validate checks ordering, overlap and overflow. Whether extents lie inside
// the file is the archive layer's concern, since only it knows the file size.
func (m *FreeMap) Validate() error {
	var end uint64
	for i, e := range m.Extents {
		if e.Len == 0 {
			return invalidf("free extent %d is empty", i)
		}
		if e.Off+e.Len < e.Off {
			return invalidf("free extent %d overflows", i)
		}
		if e.Off < ArchiveDataStart {
			return invalidf("free extent %d starts inside the fixed regions", i)
		}
		if i > 0 && e.Off < end {
			return invalidf("free extents %d and %d overlap or are unsorted", i-1, i)
		}
		end = e.Off + e.Len
	}
	return nil
}

// Encode returns the free-space map: u32 extent_count, then (u64 off, u64 len)*.
func (m *FreeMap) Encode() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if uint64(4+16*len(m.Extents)) > MaxFreeMapLen {
		return nil, invalidf("free-space map of %d extents exceeds %d bytes", len(m.Extents), MaxFreeMapLen)
	}
	w := &writer{b: make([]byte, 0, 4+16*len(m.Extents))}
	w.u32(uint32(len(m.Extents)))
	for _, e := range m.Extents {
		w.u64(e.Off)
		w.u64(e.Len)
	}
	return w.done()
}

// Hash encodes the map and returns the value the superblock stores for it, so
// that the stored hash and the stored bytes can never come from different maps.
func (m *FreeMap) Hash() (encoded []byte, hash [32]byte, err error) {
	encoded, err = m.Encode()
	if err != nil {
		return nil, hash, err
	}
	return encoded, sha256.Sum256(encoded), nil
}

// DecodeFreeMap parses a free-space map.
func DecodeFreeMap(b []byte) (*FreeMap, error) {
	if uint64(len(b)) > MaxFreeMapLen {
		return nil, invalidf("free-space map of %d bytes exceeds %d", len(b), MaxFreeMapLen)
	}
	r := newReader(b, "free-space map")
	n := r.count(r.u32(), 16)
	m := &FreeMap{Extents: make([]Extent, 0, n)}
	for i := 0; i < n && r.err == nil; i++ {
		m.Extents = append(m.Extents, Extent{Off: r.u64(), Len: r.u64()})
	}
	if err := r.done(); err != nil {
		return nil, err
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return m, nil
}

// DecodeFreeMapChecked verifies the bytes against the hash the superblock
// carries before parsing them; a mismatch is reported before any parsing.
func DecodeFreeMapChecked(b []byte, want [32]byte) (*FreeMap, error) {
	if sha256.Sum256(b) != want {
		return nil, invalidf("free-space map hash mismatch")
	}
	return DecodeFreeMap(b)
}

package format

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"reflect"
	"testing"
)

// Every decoder must survive arbitrary input without panicking, and anything it
// accepts must survive a re-encode / re-decode round trip unchanged. Where the
// decoder is canonical — every accepted byte is represented in the struct — the
// stronger property holds and is asserted: re-encoding reproduces the input
// exactly. That is what SlotRecord.AAD depends on. Structures with reserved
// fields that §1 says to ignore on read (superblocks, envelope, the slot-region
// header, pack_id in file records) legitimately drop bytes and keep the weaker
// struct-level property.

// withChecksum lets the fuzzer own a superblock's body: the tail is replaced by
// the correct SHA-256 so the decoder gets past the checksum gate.
func withChecksum(data []byte) []byte {
	buf := make([]byte, SuperblockSize)
	copy(buf, data)
	sum := sha256.Sum256(buf[:checksumOffset])
	copy(buf[checksumOffset:], sum[:])
	return buf
}

func FuzzDecodeKeystoreSuperblock(f *testing.F) {
	s := &KeystoreSuperblock{Seq: 1, SlotRegionOff: SlotRegionAOff, SlotRegionLen: MinSlotRegionLen,
		RegistryOff: RegistryMinOff, RegistryLen: 10, VMKGeneration: 1}
	b, err := s.Encode()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(b)
	f.Add(b[:checksumOffset]) // body only: the target adds the checksum
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = DecodeKeystoreSuperblock(data) // raw path: must not panic, checksum usually fails
		d, err := DecodeKeystoreSuperblock(withChecksum(data))
		if err != nil {
			return
		}
		b, err := d.Encode()
		if err != nil {
			t.Fatalf("re-encode: %v", err)
		}
		d2, err := DecodeKeystoreSuperblock(b)
		if err != nil || !reflect.DeepEqual(d, d2) {
			t.Fatalf("re-decode: %v", err)
		}
		_ = d.RegistryAAD()
		_ = d.ValidateExtents(uint64(len(data)))
	})
}

func FuzzPickKeystoreSuperblock(f *testing.F) {
	mk := func(seq uint64) []byte {
		b, err := (&KeystoreSuperblock{Seq: seq, SlotRegionOff: SlotRegionAOff, SlotRegionLen: MinSlotRegionLen,
			RegistryOff: RegistryMinOff, VMKGeneration: 1}).Encode()
		if err != nil {
			f.Fatal(err)
		}
		return b
	}
	f.Add(mk(1), mk(2))
	f.Add(mk(3)[:checksumOffset], []byte{})
	// A fresh file (§4): copy A at seq 1, copy B the same superblock at 0.
	f.Add(mk(1), mk(0))
	// Two valid copies with equal seq: a corrupt file, never a tie the picker
	// settles by taking either (§4).
	f.Add(mk(2), mk(2))
	// One valid copy, the other unreadable — the state a torn commit leaves.
	f.Add(mk(7), []byte{1, 2, 3})
	f.Fuzz(func(t *testing.T, a, b []byte) {
		_, _, _, _ = PickKeystoreSuperblock(a, b)
		ca, cb := withChecksum(a), withChecksum(b)
		// The oracle is independent of pick: decode each copy, then say what
		// §4 requires of the choice, rather than only asking whether the
		// winner pick returned decodes.
		da, errA := DecodeKeystoreSuperblock(ca)
		db, errB := DecodeKeystoreSuperblock(cb)
		live, which, stale, err := PickKeystoreSuperblock(ca, cb)
		want := func(w Copy, d *KeystoreSuperblock, loserErr error) {
			t.Helper()
			if err != nil {
				t.Fatalf("copy %s should have won (seq A %v, B %v): %v", w, errA, errB, err)
			}
			if which != w || !reflect.DeepEqual(live, d) {
				t.Fatalf("picked copy %s, want %s", which, w)
			}
			if (stale == nil) != (loserErr == nil) {
				t.Fatalf("stale %v for a loser whose decode was %v", stale, loserErr)
			}
		}
		switch {
		case errA != nil && errB != nil:
			if err == nil {
				t.Fatal("two unreadable copies produced a winner")
			}
			return
		case errA != nil:
			want(CopyB, db, errA)
		case errB != nil:
			want(CopyA, da, errB)
		case da.Seq > db.Seq:
			want(CopyA, da, nil)
		case db.Seq > da.Seq:
			want(CopyB, db, nil)
		default:
			if err == nil {
				t.Fatalf("two valid copies at seq %d produced a winner", da.Seq)
			}
			return
		}
		winner := ca
		if which == CopyB {
			winner = cb
		}
		d, err := DecodeKeystoreSuperblock(winner)
		if err != nil || !reflect.DeepEqual(live, d) {
			t.Fatalf("winner does not decode to the returned block: %v", err)
		}
	})
}

func FuzzDecodeSlotRegion(f *testing.F) {
	add := func(r *SlotRegion) {
		b, err := r.Encode()
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	// entangle 1, entangle 0, and a bare 32-byte header — the floor.
	add(&SlotRegion{Header: entangledHeader(), Slots: []SlotRecord{hardwareSlot(), softwareSlot(SlotRecovery)}})
	add(&SlotRegion{Slots: []SlotRecord{{State: SlotEmpty}, hardwareSlot()}})
	add(&SlotRegion{})
	f.Fuzz(func(t *testing.T, data []byte) {
		d, err := DecodeSlotRegion(data)
		if err != nil {
			return
		}
		if !SlotRegionCanonical(data, d) {
			t.Fatalf("accepted region is not canonical")
		}
		if err := d.Header.Validate(); err != nil {
			t.Fatalf("accepted region carries an invalid header: %v", err)
		}
		for i := range d.Slots {
			if _, err := d.Slots[i].AAD([16]byte{}); err != nil {
				t.Fatalf("AAD: %v", err)
			}
		}
	})
}

func FuzzDecodeSlotRecord(f *testing.F) {
	for _, s := range []SlotRecord{hardwareSlot(), softwareSlot(SlotStandalonePassword), {State: SlotEmpty}} {
		b, _ := s.Encode()
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		d, err := DecodeSlotRecord(data)
		if err != nil {
			return
		}
		b, err := d.Encode()
		if err != nil {
			t.Fatalf("re-encode: %v", err)
		}
		if !bytes.Equal(b, data) {
			t.Fatalf("accepted record is not canonical")
		}
	})
}

func FuzzDecodeRegistry(f *testing.F) {
	b, err := sampleRegistry().Encode()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(b)
	f.Add([]byte{3, 0, 0, 0}) // version 3 alone: the only version there is (§18.2)
	f.Fuzz(func(t *testing.T, data []byte) {
		d, err := DecodeRegistry(data)
		if err != nil {
			return
		}
		b, err := d.Encode()
		if err != nil {
			t.Fatalf("re-encode: %v", err)
		}
		// Since Revision 2 every accepted encoding is canonical: there is no
		// migration path left to make an accepted input differ from its
		// re-encoding (R21, §7).
		if !bytes.Equal(b, data) {
			t.Fatalf("accepted registry is not canonical")
		}
	})
}

func FuzzDecodeEnvelope(f *testing.F) {
	e := (&Envelope{ArchiveID: fill16(1), KID: fill16(2)}).Encode()
	f.Add(e)
	f.Add(e[:checksumOffset])
	// An envelope of a version this reader does not support: the target adds
	// the checksum, so the body alone reaches the version gate (§10, R33).
	future := bytes.Clone(e[:checksumOffset])
	binary.LittleEndian.PutUint16(future[8:], FormatVersion+1)
	f.Add(future)
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = DecodeEnvelope(data)
		d, err := DecodeEnvelope(withChecksum(data))
		if err != nil {
			return
		}
		d2, err := DecodeEnvelope(d.Encode())
		if err != nil || *d != *d2 {
			t.Fatalf("re-decode: %v", err)
		}
	})
}

func FuzzDecodeArchiveSuperblock(f *testing.F) {
	s := &ArchiveSuperblock{Seq: 1, IndexOff: ArchiveDataStart, IndexLen: 1, FreeMapOff: ArchiveDataStart + 100, FreeMapLen: 4}
	b, _ := s.Encode()
	f.Add(b)
	f.Add(b[:checksumOffset])
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = DecodeArchiveSuperblock(data)
		d, err := DecodeArchiveSuperblock(withChecksum(data))
		if err != nil {
			return
		}
		b, err := d.Encode()
		if err != nil {
			t.Fatalf("re-encode: %v", err)
		}
		d2, err := DecodeArchiveSuperblock(b)
		if err != nil || !reflect.DeepEqual(d, d2) {
			t.Fatalf("re-decode: %v", err)
		}
		_ = d.IndexAAD([16]byte{}, [16]byte{})
		_ = d.ValidateExtents(uint64(len(data)) * 1024)
	})
}

func FuzzDecodeIndex(f *testing.F) {
	b, _ := sampleIndex().Encode()
	f.Add(b)
	// The smallest valid index: version 2, no dictionary, no directories, no
	// files.
	f.Add([]byte{2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	// A tree deep enough to exercise the chain walk, and one whose records
	// hang off the root only.
	flat, _ := (&Index{
		Dirs:  []DirRecord{dirRec(idN(1), RootID, "a")},
		Files: []FileRecord{fileRec(idN(2), RootID, "b"), fileRec(idN(3), idN(1), "c")},
	}).Encode()
	f.Add(flat)
	deep, _ := indexWithPath(MaxPathLen).Encode()
	f.Add(deep)
	// index_version 1 — refused, never migrated.
	f.Add([]byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		d, err := DecodeIndex(data)
		if err != nil {
			return
		}
		b, err := d.Encode()
		if err != nil {
			t.Fatalf("re-encode: %v", err)
		}
		d2, err := DecodeIndex(b)
		if err != nil || !reflect.DeepEqual(d, d2) {
			t.Fatalf("re-decode: %v", err)
		}
		// Bytes, not structures: comparing decoded values cannot see a decoder
		// that drops a field and an encoder that writes another of the same
		// width in its place. `pack_id` is the one span §1 tells a reader to
		// ignore, so it is the one span masked — the slot region's harness
		// masks its header's two reserved bytes the same way (R21).
		if !indexCanonical(data, b) {
			t.Fatalf("accepted index is not canonical")
		}
	})
}

// indexCanonical reports whether an accepted index re-encodes to the bytes it
// was decoded from, ignoring only the `pack_id` of each file record (§11,
// reserved and ignored on read). The spans are walked out of enc, which the
// encoder produced, so a malformed input cannot steer the masking; a length
// that differs is already a failure.
func indexCanonical(data, enc []byte) bool {
	if len(data) != len(enc) {
		return false
	}
	spans := packIDSpans(enc)
	if spans == nil {
		return false
	}
	a, b := append([]byte(nil), data...), append([]byte(nil), enc...)
	for _, s := range spans {
		for i := s[0]; i < s[1]; i++ {
			a[i], b[i] = 0, 0
		}
	}
	return bytes.Equal(a, b)
}

// packIDSpans walks an encoded index (§11: u32 index_version, u32-prefixed
// dict, u32 dir_count and its records, u32 file_count and its records, each
// record a u32 record_len and that many body bytes) and returns the byte range
// of every file record's pack_id — the last 48 bytes of a body hold pack_id
// (16), revision (8), last_writer (16) and modified_at (8). nil means the
// bytes are not a well-formed index.
func packIDSpans(b []byte) [][2]int {
	off := 0
	u32 := func() (uint32, bool) {
		if off+4 > len(b) {
			return 0, false
		}
		v := binary.LittleEndian.Uint32(b[off:])
		off += 4
		return v, true
	}
	skip := func(n uint32) bool {
		if uint64(n) > uint64(len(b)-off) {
			return false
		}
		off += int(n)
		return true
	}
	if _, ok := u32(); !ok { // index_version
		return nil
	}
	dict, ok := u32()
	if !ok || !skip(dict) {
		return nil
	}
	nd, ok := u32()
	if !ok {
		return nil
	}
	for i := uint32(0); i < nd; i++ {
		l, ok := u32()
		if !ok || !skip(l) {
			return nil
		}
	}
	nf, ok := u32()
	if !ok {
		return nil
	}
	spans := make([][2]int, 0, nf)
	for i := uint32(0); i < nf; i++ {
		l, ok := u32()
		if !ok || l < 48 || !skip(l) {
			return nil
		}
		spans = append(spans, [2]int{off - 48, off - 32})
	}
	if off != len(b) {
		return nil
	}
	return spans
}

func FuzzDecodeFreeMap(f *testing.F) {
	b, _ := (&FreeMap{Extents: []Extent{{ArchiveDataStart, 16}, {ArchiveDataStart + 64, 1}}}).Encode()
	f.Add(b)
	f.Add([]byte{0, 0, 0, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		d, err := DecodeFreeMap(data)
		if err != nil {
			return
		}
		b, err := d.Encode()
		if err != nil {
			t.Fatalf("re-encode: %v", err)
		}
		if !bytes.Equal(b, data) {
			t.Fatalf("accepted free-space map is not canonical")
		}
	})
}

package format

import (
	"bytes"
	"crypto/sha256"
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
	f.Fuzz(func(t *testing.T, a, b []byte) {
		_, _, _, _ = PickKeystoreSuperblock(a, b)
		ca, cb := withChecksum(a), withChecksum(b)
		live, which, _, err := PickKeystoreSuperblock(ca, cb)
		if err != nil {
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
	f.Add([]byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		d, err := DecodeIndex(data)
		if err != nil {
			return
		}
		b, err := d.Encode()
		if err != nil {
			t.Fatalf("re-encode: %v", err)
		}
		// pack_id is ignored on read, so only the struct-level round trip holds.
		d2, err := DecodeIndex(b)
		if err != nil || !reflect.DeepEqual(d, d2) {
			t.Fatalf("re-decode: %v", err)
		}
		if len(b) != len(data) {
			t.Fatalf("re-encoded length %d != %d", len(b), len(data))
		}
	})
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

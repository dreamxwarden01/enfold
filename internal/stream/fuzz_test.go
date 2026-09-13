package stream

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/dreamxwarden01/enfold/internal/format"
)

// FuzzRoundTrip writes data through a Writer in pieces chosen by split and
// reads it back sequentially and by random access.
func FuzzRoundTrip(f *testing.F) {
	f.Add([]byte{}, uint16(0))
	f.Add([]byte{1}, uint16(1))
	f.Add(pattern(ChunkSize), uint16(7))
	f.Add(pattern(ChunkSize+1), uint16(65535))
	f.Add(pattern(2*ChunkSize+5), uint16(3))
	f.Fuzz(func(t *testing.T, data []byte, split uint16) {
		var buf bytes.Buffer
		w, err := NewWriter(&buf, testDEK, testArchiveID, testFileID)
		if err != nil {
			t.Fatal(err)
		}
		step := int(split) + 1
		for p := data; len(p) > 0; {
			n := min(step, len(p))
			if _, err := w.Write(p[:n]); err != nil {
				t.Fatal(err)
			}
			p = p[n:]
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		ct := buf.Bytes()
		if uint64(len(ct)) != format.RawStoredSize(uint64(len(data))) {
			t.Fatalf("blob %d bytes for %d plaintext", len(ct), len(data))
		}
		r, err := NewReader(bytes.NewReader(ct), uint64(len(ct)), testDEK, testArchiveID, testFileID)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(r)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("sequential: %v", err)
		}
		// Random access from a position derived from split.
		off := int(split) % (len(data) + 1)
		if _, err := r.Seek(int64(off), io.SeekStart); err != nil {
			t.Fatal(err)
		}
		got, err = io.ReadAll(r)
		if err != nil || !bytes.Equal(got, data[off:]) {
			t.Fatalf("from %d: %v", off, err)
		}
	})
}

// FuzzReader feeds arbitrary bytes as a blob of their own length. The Reader
// must refuse the framing or fail to authenticate; it must never panic or
// return plaintext.
func FuzzReader(f *testing.F) {
	f.Add([]byte{})
	f.Add(make([]byte, TagSize))
	f.Add(make([]byte, TagSize+1))
	f.Add(make([]byte, SealedChunkSize))
	f.Add(make([]byte, SealedChunkSize+TagSize))
	f.Add(make([]byte, SealedChunkSize+TagSize+1))
	f.Add(encrypt(f, pattern(3), nil))
	// One valid chunk is not enough to reach the failures that happen *after*
	// a chunk has authenticated: the counter in the nonce, the final flag, and
	// the reader's refusal to report a clean end before a final chunk verifies
	// (R26, §12). These are three sealed chunks — 65536, 65536, 5 — and the
	// ways a blob of them goes wrong.
	multi := encrypt(f, pattern(2*ChunkSize+5), nil)
	edit := func(fn func([]byte)) []byte {
		b := append([]byte(nil), multi...)
		fn(b)
		return b
	}
	f.Add(multi)                                                           // valid: must round-trip exactly
	f.Add(edit(func(b []byte) { b[SealedChunkSize+10] ^= 0x40 }))          // corruption in the second chunk
	f.Add(edit(func(b []byte) { b[len(b)-1] ^= 1 }))                       // corruption in the final chunk's tag
	f.Add(multi[:2*SealedChunkSize])                                       // cut at a chunk boundary: a canonical length whose last chunk is not final
	f.Add(multi[:len(multi)-1])                                            // cut inside the final chunk
	f.Add(append(append([]byte(nil), multi...), make([]byte, TagSize)...)) // extended past the final chunk
	f.Add(edit(func(b []byte) {                                            // two chunks swapped: the counter catches it
		var tmp [SealedChunkSize]byte
		copy(tmp[:], b[:SealedChunkSize])
		copy(b[:SealedChunkSize], b[SealedChunkSize:2*SealedChunkSize])
		copy(b[SealedChunkSize:2*SealedChunkSize], tmp[:])
	}))
	f.Fuzz(func(t *testing.T, data []byte) {
		r, err := NewReader(bytes.NewReader(data), uint64(len(data)), testDEK, testArchiveID, testFileID)
		if err != nil {
			if !errors.Is(err, format.ErrInvalid) {
				t.Fatalf("unexpected constructor error: %v", err)
			}
			return
		}
		got, err := io.ReadAll(r)
		if err == nil {
			// Only a blob written under the test key can open. The seed
			// corpus contains one; anything the fuzzer derives from it
			// must round-trip exactly.
			if want := refEncode(t, testDEK, testArchiveID, testFileID, got); !bytes.Equal(want, data) {
				t.Fatalf("opened %d bytes from a blob that is not their encoding", len(got))
			}
			return
		}
		if !errors.Is(err, ErrAuth) && !errors.Is(err, format.ErrTruncated) {
			t.Fatalf("unexpected read error: %v", err)
		}
	})
}

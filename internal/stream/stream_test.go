package stream

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math/rand/v2"
	"testing"

	"github.com/dreamxwarden01/enfold/internal/format"
	"github.com/dreamxwarden01/enfold/internal/kdf"
)

// Fixed inputs shared by the tests. Nothing here is secret.
var (
	testDEK       = [KeySize]byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f}
	testArchiveID = [16]byte{0xa0, 0xa1, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7, 0xa8, 0xa9, 0xaa, 0xab, 0xac, 0xad, 0xae, 0xaf}
	testFileID    = [16]byte{0xf0, 0xf1, 0xf2, 0xf3, 0xf4, 0xf5, 0xf6, 0xf7, 0xf8, 0xf9, 0xfa, 0xfb, 0xfc, 0xfd, 0xfe, 0xff}
)

// pattern is a deterministic non-repeating-looking plaintext of n bytes.
func pattern(n int) []byte {
	p := make([]byte, n)
	for i := range p {
		p[i] = byte((i*7 + i/251) % 256)
	}
	return p
}

// refSeal is an independent chunk encryption written from the prose of §12
// and §1 rather than from format.ChunkNonce/ChunkAAD: nonce bytes 0..10 are
// the counter little-endian, byte 11 is the flag; the AAD is the two IDs, then
// alg_id 1 as a little-endian u16, then 65536 as a little-endian u32.
func refSeal(t *testing.T, dek [KeySize]byte, archiveID, fileID [16]byte, counter uint64, final bool, pt []byte) []byte {
	t.Helper()
	block, err := aes.NewCipher(dek[:])
	if err != nil {
		t.Fatal(err)
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, 12)
	for i := 0; i < 11; i++ {
		nonce[i] = byte(counter >> (8 * i))
	}
	if final {
		nonce[11] = 1
	}
	var aad []byte
	aad = append(aad, archiveID[:]...)
	aad = append(aad, fileID[:]...)
	aad = append(aad, 1, 0)       // alg_id = 1
	aad = append(aad, 0, 0, 1, 0) // chunk_size = 65536
	return g.Seal(nil, nonce, pt, aad)
}

// refEncode is the whole-blob reference: 65536-byte chunks, the last one
// final, exactly one empty final chunk for an empty plaintext.
func refEncode(t *testing.T, dek [KeySize]byte, archiveID, fileID [16]byte, pt []byte) []byte {
	t.Helper()
	var out []byte
	var counter uint64
	for {
		n := min(len(pt), ChunkSize)
		final := len(pt) <= ChunkSize
		out = append(out, refSeal(t, dek, archiveID, fileID, counter, final, pt[:n])...)
		counter++
		pt = pt[n:]
		if final {
			return out
		}
	}
}

func encrypt(t testing.TB, pt []byte, chunking []int) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := NewWriter(&buf, testDEK, testArchiveID, testFileID)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunking) == 0 {
		if _, err := w.Write(pt); err != nil {
			t.Fatal(err)
		}
	} else {
		for i := 0; len(pt) > 0; i++ {
			n := min(chunking[i%len(chunking)], len(pt))
			if _, err := w.Write(pt[:n]); err != nil {
				t.Fatal(err)
			}
			pt = pt[n:]
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if w.Written() != uint64(buf.Len()) {
		t.Fatalf("Written %d, buffer %d", w.Written(), buf.Len())
	}
	return buf.Bytes()
}

func decrypt(t *testing.T, ct []byte) ([]byte, error) {
	t.Helper()
	r, err := NewReader(bytes.NewReader(ct), uint64(len(ct)), testDEK, testArchiveID, testFileID)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

var lengths = []int{0, 1, 2, 15, 16, 17, 100, ChunkSize - 1, ChunkSize, ChunkSize + 1, 2*ChunkSize - 1, 2 * ChunkSize, 2*ChunkSize + 1, 3*ChunkSize + 7}

func TestRoundTrip(t *testing.T) {
	for _, n := range lengths {
		pt := pattern(n)
		ct := encrypt(t, pt, nil)
		if want := format.RawStoredSize(uint64(n)); uint64(len(ct)) != want {
			t.Errorf("n=%d: blob is %d bytes, RawStoredSize says %d", n, len(ct), want)
		}
		if ref := refEncode(t, testDEK, testArchiveID, testFileID, pt); !bytes.Equal(ct, ref) {
			t.Errorf("n=%d: blob differs from the reference encoding", n)
		}
		got, err := decrypt(t, ct)
		if err != nil {
			t.Errorf("n=%d: %v", n, err)
			continue
		}
		if !bytes.Equal(got, pt) {
			t.Errorf("n=%d: plaintext differs", n)
		}
	}
}

func TestWriteChunkingDoesNotMatter(t *testing.T) {
	pt := pattern(2*ChunkSize + 1234)
	want := encrypt(t, pt, nil)
	for _, chunking := range [][]int{{1}, {7, 13}, {ChunkSize}, {ChunkSize - 1, 2}, {ChunkSize + 1}, {3 * ChunkSize}, {65535, 1, 1}} {
		if got := encrypt(t, pt, chunking); !bytes.Equal(got, want) {
			t.Errorf("chunking %v changed the blob", chunking)
		}
	}
}

// TestPinned freezes the blob for one input so that any drift in the nonce,
// AAD or framing shows up as a changed hash rather than as a still-passing
// round trip.
func TestPinned(t *testing.T) {
	pins := map[int]string{
		0:               "3d5b464c75791f707f8c612d8b458207fdf65036db13ef212fcddd539ae90c8e",
		1:               "efecebfe656bb8090abb5673c3d87408babb41fe06ab8b759d5fffefc092a012",
		ChunkSize:       "9ccfa2e4f8846a1f6cd4b55ebdbc843869573ca30a5471eba37ad49c8f715186",
		2*ChunkSize + 3: "d9c9b14ca9890a9c4a2ce2d6a24bf527b58e95cd334709cb938d37d280d3c30b",
	}
	for n, want := range pins {
		ct := encrypt(t, pattern(n), nil)
		sum := sha256.Sum256(ct)
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Errorf("n=%d: sha256 %s, pinned %s", n, got, want)
		}
	}
}

func TestPlaintextLen(t *testing.T) {
	for n := uint64(0); n <= 3*ChunkSize+40; n++ {
		stored := format.RawStoredSize(n)
		got, err := PlaintextLen(stored)
		if err != nil || got != n {
			t.Fatalf("PlaintextLen(RawStoredSize(%d)=%d) = %d, %v", n, stored, got, err)
		}
	}
	// Every stored length in range is valid iff some n produces it.
	valid := map[uint64]bool{}
	for n := uint64(0); n <= 4*ChunkSize; n++ {
		valid[format.RawStoredSize(n)] = true
	}
	for stored := uint64(0); stored <= 3*SealedChunkSize+40; stored++ {
		_, err := PlaintextLen(stored)
		if valid[stored] != (err == nil) {
			t.Errorf("PlaintextLen(%d): err=%v, want valid=%v", stored, err, valid[stored])
		}
		if err != nil && !errors.Is(err, format.ErrInvalid) {
			t.Errorf("PlaintextLen(%d): %v does not wrap ErrInvalid", stored, err)
		}
	}
	// The counter bound.
	if got, err := PlaintextLen(format.RawStoredSize(format.MaxOrigSize)); err != nil || got != format.MaxOrigSize {
		t.Errorf("at MaxOrigSize: %d, %v", got, err)
	}
	if _, err := PlaintextLen(uint64(MaxChunks)*SealedChunkSize + 17); !errors.Is(err, format.ErrInvalid) {
		t.Errorf("2^32+1 chunks accepted: %v", err)
	}
	if _, err := PlaintextLen(^uint64(0)); !errors.Is(err, format.ErrInvalid) {
		t.Errorf("max uint64 accepted: %v", err)
	}
}

func TestWrongKeyOrIdentity(t *testing.T) {
	pt := pattern(100)
	ct := encrypt(t, pt, nil)
	other := func(b [16]byte) [16]byte { b[0] ^= 1; return b }
	var badDEK [KeySize]byte
	copy(badDEK[:], testDEK[:])
	badDEK[31] ^= 1
	cases := []struct {
		name string
		dek  [KeySize]byte
		aID  [16]byte
		fID  [16]byte
	}{
		{"dek", badDEK, testArchiveID, testFileID},
		{"archive", testDEK, other(testArchiveID), testFileID},
		{"file", testDEK, testArchiveID, other(testFileID)},
	}
	for _, c := range cases {
		r, err := NewReader(bytes.NewReader(ct), uint64(len(ct)), c.dek, c.aID, c.fID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.ReadAll(r); !errors.Is(err, ErrAuth) {
			t.Errorf("wrong %s: got %v, want ErrAuth", c.name, err)
		}
	}
}

func TestTamper(t *testing.T) {
	// Every byte of a short blob (one chunk) and of a two-chunk blob whose
	// second chunk is short.
	for _, n := range []int{0, 5, ChunkSize + 3} {
		ct := encrypt(t, pattern(n), nil)
		for i := range ct {
			bad := bytes.Clone(ct)
			bad[i] ^= 0x80
			if _, err := decrypt(t, bad); !errors.Is(err, ErrAuth) {
				t.Errorf("n=%d flip byte %d: got %v, want ErrAuth", n, i, err)
			}
		}
	}
}

func TestReorder(t *testing.T) {
	ct := encrypt(t, pattern(3*ChunkSize+5), nil)
	bad := bytes.Clone(ct)
	copy(bad[:SealedChunkSize], ct[SealedChunkSize:2*SealedChunkSize])
	copy(bad[SealedChunkSize:2*SealedChunkSize], ct[:SealedChunkSize])
	if _, err := decrypt(t, bad); !errors.Is(err, ErrAuth) {
		t.Errorf("swapped chunks: got %v, want ErrAuth", err)
	}
	// Chunk 1 in place of chunk 2 (duplicate), keeping the final chunk.
	bad = bytes.Clone(ct)
	copy(bad[2*SealedChunkSize:3*SealedChunkSize], ct[SealedChunkSize:2*SealedChunkSize])
	if _, err := decrypt(t, bad); !errors.Is(err, ErrAuth) {
		t.Errorf("duplicated chunk: got %v, want ErrAuth", err)
	}
}

func TestTruncation(t *testing.T) {
	pt := pattern(2*ChunkSize + 9)
	ct := encrypt(t, pt, nil)

	// Cut at a chunk boundary with a matching stored length: the framing is
	// canonical, so only the final flag can catch it.
	cut := ct[:2*SealedChunkSize]
	if _, err := PlaintextLen(uint64(len(cut))); err != nil {
		t.Fatalf("boundary cut should be a canonical length: %v", err)
	}
	if _, err := decrypt(t, cut); !errors.Is(err, ErrAuth) {
		t.Errorf("boundary cut: got %v, want ErrAuth", err)
	}

	// Source shorter than the stated length.
	for _, short := range []int{1, TagSize, SealedChunkSize, len(ct) - 1} {
		r, err := NewReader(bytes.NewReader(ct[:len(ct)-short]), uint64(len(ct)), testDEK, testArchiveID, testFileID)
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.ReadAll(r)
		if !errors.Is(err, format.ErrTruncated) {
			t.Errorf("source short by %d: got %v, want ErrTruncated", short, err)
		}
	}

	// A non-canonical stated length is refused before any I/O.
	for _, stored := range []uint64{0, 15, SealedChunkSize + 1, SealedChunkSize + TagSize} {
		if _, err := NewReader(bytes.NewReader(ct), stored, testDEK, testArchiveID, testFileID); !errors.Is(err, format.ErrInvalid) {
			t.Errorf("stored=%d: got %v, want ErrInvalid", stored, err)
		}
	}

	// Trailing bytes after the blob are outside it and ignored.
	withTrailer := append(bytes.Clone(ct), 1, 2, 3)
	r, err := NewReader(bytes.NewReader(withTrailer), uint64(len(ct)), testDEK, testArchiveID, testFileID)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := io.ReadAll(r); err != nil || !bytes.Equal(got, pt) {
		t.Errorf("trailer: %v", err)
	}
}

// TestEmptyFinalAfterFull crafts the encoding age v1.0.0 produced for a
// plaintext that is a multiple of the chunk size — a full non-final chunk and
// then an empty final one. The tags verify; the framing is rejected anyway.
func TestEmptyFinalAfterFull(t *testing.T) {
	c, err := newChunker(testDEK, testArchiveID, testFileID)
	if err != nil {
		t.Fatal(err)
	}
	var blob []byte
	blob = c.seal(blob, 0, false, pattern(ChunkSize))
	blob = c.seal(blob, 1, true, nil)
	if _, err := NewReader(bytes.NewReader(blob), uint64(len(blob)), testDEK, testArchiveID, testFileID); !errors.Is(err, format.ErrInvalid) {
		t.Errorf("got %v, want ErrInvalid", err)
	}
	// The same bytes with the canonical framing for 65536 bytes: chunk 0
	// was sealed non-final, so it must not open as final.
	if _, err := decrypt(t, blob[:SealedChunkSize]); !errors.Is(err, ErrAuth) {
		t.Errorf("got %v, want ErrAuth", err)
	}
}

func TestSeek(t *testing.T) {
	pt := pattern(3*ChunkSize + 321)
	ct := encrypt(t, pt, nil)
	r, err := NewReader(bytes.NewReader(ct), uint64(len(ct)), testDEK, testArchiveID, testFileID)
	if err != nil {
		t.Fatal(err)
	}
	if r.Size() != int64(len(pt)) {
		t.Fatalf("Size %d, want %d", r.Size(), len(pt))
	}
	if end, err := r.Seek(0, io.SeekEnd); err != nil || end != int64(len(pt)) {
		t.Fatalf("SeekEnd: %d, %v", end, err)
	}
	if n, err := r.Read(make([]byte, 1)); n != 0 || err != io.EOF {
		t.Fatalf("read at end: %d, %v", n, err)
	}
	rng := rand.New(rand.NewPCG(1, 2))
	// The boundary cases first, deterministically, then random ones.
	fixed := [][2]int{{len(pt), 0}, {len(pt), 1}, {len(pt) + 1, 0}, {len(pt) + 1, 5}, {len(pt) + 9, 1}, {len(pt) - 1, 1}, {len(pt) - 1, 2}, {0, 0}}
	for i := 0; i < 500+len(fixed); i++ {
		var off, n int
		if i < len(fixed) {
			off, n = fixed[i][0], fixed[i][1]
		} else {
			off, n = rng.IntN(len(pt)+10), rng.IntN(2*ChunkSize+10)
		}
		if _, err := r.Seek(int64(off), io.SeekStart); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, n)
		got, err := io.ReadFull(r, buf)
		want := max(0, min(n, len(pt)-off))
		if got != want {
			t.Fatalf("off=%d n=%d: read %d, want %d (%v)", off, n, got, want, err)
		}
		if want > 0 && err != nil && (got == n || err != io.ErrUnexpectedEOF) {
			t.Fatalf("off=%d n=%d: %v", off, n, err)
		}
		lo := min(off, len(pt))
		if !bytes.Equal(buf[:got], pt[lo:lo+got]) {
			t.Fatalf("off=%d n=%d: bytes differ", off, n)
		}
		if cur, err := r.Seek(0, io.SeekCurrent); err != nil || cur != int64(off+got) {
			t.Fatalf("SeekCurrent after read: %d, %v", cur, err)
		}
	}
	// Relative seeks and rejection of negative positions.
	if _, err := r.Seek(10, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if pos, err := r.Seek(-5, io.SeekCurrent); err != nil || pos != 5 {
		t.Errorf("SeekCurrent -5: %d, %v", pos, err)
	}
	if _, err := r.Seek(-6, io.SeekCurrent); err == nil {
		t.Error("negative position accepted")
	}
	if _, err := r.Seek(-1, io.SeekStart); err == nil {
		t.Error("negative start accepted")
	}
	if _, err := r.Seek(0, 7); err == nil {
		t.Error("bad whence accepted")
	}
	if _, err := r.Seek(1, io.SeekEnd); err != nil {
		t.Error("seek past end refused")
	}
}

// TestEOFAuthenticatesFinalChunk: io.EOF is never reported on a blob whose
// final chunk has not opened — the empty file being the case where nothing
// else would ever touch it.
func TestEOFAuthenticatesFinalChunk(t *testing.T) {
	for _, n := range []int{0, 5, ChunkSize + 3} {
		ct := encrypt(t, pattern(n), nil)
		ct[len(ct)-1] ^= 1
		r, err := NewReader(bytes.NewReader(ct), uint64(len(ct)), testDEK, testArchiveID, testFileID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.Seek(0, io.SeekEnd); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Read(make([]byte, 1)); !errors.Is(err, ErrAuth) {
			t.Errorf("n=%d: read at end of a blob with a bad final tag: %v, want ErrAuth", n, err)
		}
		if _, err := r.Seek(100, io.SeekEnd); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Read(make([]byte, 1)); !errors.Is(err, ErrAuth) {
			t.Errorf("n=%d: read past end of a blob with a bad final tag: %v, want ErrAuth", n, err)
		}
	}
}

// TestErrorsAreNotSticky: a chunk that fails does not poison the chunks that
// are intact, so a caller that wants to salvage can seek around it.
func TestErrorsAreNotSticky(t *testing.T) {
	pt := pattern(3 * ChunkSize)
	ct := encrypt(t, pt, nil)
	ct[SealedChunkSize+100] ^= 1 // chunk 1
	r, err := NewReader(bytes.NewReader(ct), uint64(len(ct)), testDEK, testArchiveID, testFileID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	if !errors.Is(err, ErrAuth) || len(got) != ChunkSize {
		t.Fatalf("sequential: %d bytes, %v", len(got), err)
	}
	if _, err := r.Seek(2*ChunkSize, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	got, err = io.ReadAll(r)
	if err != nil || !bytes.Equal(got, pt[2*ChunkSize:]) {
		t.Fatalf("after seek: %d bytes, %v", len(got), err)
	}
}

type failWriter struct{ n int } // fails on the n-th Write

func (f *failWriter) Write(p []byte) (int, error) {
	f.n--
	if f.n <= 0 {
		return 0, errors.New("disk full")
	}
	return len(p), nil
}

func TestWriterErrors(t *testing.T) {
	// A failing destination: the error sticks, Close reports it, nothing more
	// is counted as written.
	fw := &failWriter{n: 2}
	w, err := NewWriter(fw, testDEK, testArchiveID, testFileID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(pattern(2*ChunkSize + 1)); err == nil {
		t.Fatal("write succeeded through a failing destination")
	}
	if w.Written() != SealedChunkSize {
		t.Errorf("Written %d, want %d", w.Written(), SealedChunkSize)
	}
	if _, err := w.Write([]byte{1}); err == nil || err.Error() != "disk full" {
		t.Errorf("error did not stick: %v", err)
	}
	if err := w.Close(); err == nil || err.Error() != "disk full" {
		t.Errorf("Close after failure: %v", err)
	}
	if err := w.Close(); err == nil || err.Error() != "disk full" {
		t.Errorf("second Close: %v", err)
	}

	// Write after Close.
	var buf bytes.Buffer
	w, err = NewWriter(&buf, testDEK, testArchiveID, testFileID)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte{1}); !errors.Is(err, ErrClosed) {
		t.Errorf("write after close: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if buf.Len() != TagSize {
		t.Errorf("empty blob is %d bytes", buf.Len())
	}

	// The size bound, checked before anything is written.
	w, err = NewWriter(&buf, testDEK, testArchiveID, testFileID)
	if err != nil {
		t.Fatal(err)
	}
	w.total = format.MaxOrigSize - 1
	if _, err := w.Write([]byte{1, 2}); !errors.Is(err, ErrTooLarge) {
		t.Errorf("over MaxOrigSize: %v", err)
	}
	if _, err := w.Write([]byte{1}); err != nil {
		t.Errorf("exactly MaxOrigSize: %v", err)
	}
	if _, err := w.Write([]byte{1}); !errors.Is(err, ErrTooLarge) {
		t.Errorf("one past MaxOrigSize: %v", err)
	}
	// The counter guard behind it.
	w, err = NewWriter(io.Discard, testDEK, testArchiveID, testFileID)
	if err != nil {
		t.Fatal(err)
	}
	w.counter = MaxChunks
	if err := w.flush(false); !errors.Is(err, ErrTooLarge) {
		t.Errorf("counter at MaxChunks: %v", err)
	}
}

func TestReaderAfterClose(t *testing.T) {
	ct := encrypt(t, pattern(10), nil)
	r, err := NewReader(bytes.NewReader(ct), uint64(len(ct)), testDEK, testArchiveID, testFileID)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Read(make([]byte, 1)); !errors.Is(err, ErrClosed) {
		t.Errorf("read after close: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// ioErrReaderAt returns a non-EOF error from ReadAt.
type ioErrReaderAt struct{}

func (ioErrReaderAt) ReadAt([]byte, int64) (int, error) { return 0, errors.New("bad sector") }

// negativeReaderAt violates the io.ReaderAt contract with a negative count.
type negativeReaderAt struct{}

func (negativeReaderAt) ReadAt([]byte, int64) (int, error) { return -1, nil }

func TestSourceError(t *testing.T) {
	r, err := NewReader(ioErrReaderAt{}, 17, testDEK, testArchiveID, testFileID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(r); err == nil || err.Error() != "bad sector" {
		t.Errorf("I/O error not passed through: %v", err)
	}
	r, err = NewReader(negativeReaderAt{}, 17, testDEK, testArchiveID, testFileID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(r); !errors.Is(err, format.ErrTruncated) {
		t.Errorf("negative count from ReadAt: %v, want ErrTruncated", err)
	}
}

func TestZeroLengthRead(t *testing.T) {
	ct := encrypt(t, pattern(10), nil)
	r, err := NewReader(bytes.NewReader(ct), uint64(len(ct)), testDEK, testArchiveID, testFileID)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := r.Read(nil); n != 0 || err != nil {
		t.Errorf("Read(nil) = %d, %v", n, err)
	}
}

func TestKeySizeMatchesHierarchy(t *testing.T) {
	if KeySize != kdf.KeySize {
		t.Fatalf("stream.KeySize %d, kdf.KeySize %d", KeySize, kdf.KeySize)
	}
}

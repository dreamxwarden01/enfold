package compress

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"runtime"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/dreamxwarden01/enfold/internal/format"
)

// text is n bytes of word salad: compressible, not trivially so.
func text(n int, seed uint64) []byte {
	words := []string{"the", "archive", "keeps", "every", "file", "under", "its", "own", "key", "and",
		"rotates", "nothing", "without", "being", "asked", "which", "is", "what", "makes", "editing",
		"cheap", "zstd", "window", "chunk", "index", "record", "0123456789", "\n"}
	rng := rand.New(rand.NewPCG(seed, 7))
	var b bytes.Buffer
	for b.Len() < n {
		b.WriteString(words[rng.IntN(len(words))])
		b.WriteByte(' ')
	}
	return b.Bytes()[:n]
}

// noise is n bytes that do not compress.
func noise(n int, seed uint64) []byte {
	rng := rand.New(rand.NewPCG(seed, 11))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(rng.Uint32())
	}
	return b
}

func compressWith(t testing.TB, p Params, in []byte) []byte {
	t.Helper()
	w, err := NewWriter(p)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Release()
	return compressOn(t, w, in)
}

func compressOn(t testing.TB, w *Writer, in []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := w.Reset(&buf, int64(len(in))); err != nil {
		t.Fatal(err)
	}
	// Feed in uneven pieces.
	for p, k := in, 1; len(p) > 0; k = k*3 + 1 {
		n := min(k, len(p))
		if _, err := w.Write(p[:n]); err != nil {
			t.Fatal(err)
		}
		p = p[n:]
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func decompressWith(t testing.TB, dict []byte, frame []byte, size uint64, withDict bool) ([]byte, error) {
	t.Helper()
	r, err := NewReader(dict)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := r.Reset(bytes.NewReader(frame), size, withDict); err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

func header(t testing.TB, frame []byte) zstd.Header {
	t.Helper()
	var h zstd.Header
	if err := h.Decode(frame); err != nil {
		t.Fatalf("header: %v", err)
	}
	return h
}

// emptyFrame is a complete zstd frame for no bytes: magic, a frame header
// with no flags and a 1 KiB window, and one last raw block of length 0.
var emptyFrame = []byte{0x28, 0xb5, 0x2f, 0xfd, 0x00, 0x00, 0x01, 0x00, 0x00}

// skippableFrame is a skippable frame carrying n bytes of payload.
func skippableFrame(n int) []byte {
	f := binary.LittleEndian.AppendUint32(nil, 0x184D2A50)
	f = binary.LittleEndian.AppendUint32(f, uint32(n))
	return append(f, make([]byte, n)...)
}

func TestParamsValidate(t *testing.T) {
	bad := []Params{
		{},
		{Level: 5},
		{Level: Default, Window: 3000},
		{Level: Default, Window: MinWindow / 2},
		{Level: Default, Window: MaxWindow * 2},
		{Level: Default, Concurrency: -1},
		{Level: Default, Window: MaxParallelWindow * 2, Concurrency: 2},
		{Level: Default, Padding: -1},
		{Level: Default, Padding: MaxPadding + 1},
		{Level: Default, Dict: []byte("not a dictionary")},
	}
	for _, p := range bad {
		if err := p.Validate(); !errors.Is(err, ErrParams) {
			t.Errorf("%+v: %v, want ErrParams", p, err)
		}
	}
	good := []Params{
		{Level: Fastest},
		{Level: Best, Window: MaxWindow},
		{Level: Default, Window: MaxParallelWindow, Concurrency: 2},
		{Level: Better, Window: MinWindow, Concurrency: 4, Padding: 4096},
	}
	for _, p := range good {
		if err := p.Validate(); err != nil {
			t.Errorf("%+v: %v", p, err)
		}
	}
	for l := Level(0); l < 6; l++ {
		_ = l.String()
	}
}

func TestRoundTrip(t *testing.T) {
	inputs := map[string][]byte{
		"byte":  {42},
		"255":   text(255, 21),
		"256":   text(256, 22),
		"1024":  text(1024, 23),
		"1025":  text(1025, 24),
		"65535": text(65535, 25),
		"100k":  text(100<<10, 26),
		"text":  text(300<<10, 1),
		"noise": noise(200<<10, 2),
		"zeros": make([]byte, 1<<20),
	}
	for _, level := range []Level{Fastest, Default, Better, Best} {
		for name, in := range inputs {
			frame := compressWith(t, Params{Level: level}, in)
			// The library's header rules, asserted so that a change in them
			// fails here rather than silently weakening the Reader's
			// pre-decode checks: no content-size field below 256 bytes; one
			// from 256; a single-segment frame (content size doubling as the
			// window) for a stream the encoder buffers whole above 1 KiB.
			h := header(t, frame)
			if len(in) < 256 && h.HasFCS {
				t.Errorf("%s/%s: content size in the header below 256 bytes", level, name)
			}
			if len(in) >= 256 && !h.HasFCS {
				t.Errorf("%s/%s: no content size in the header", level, name)
			}
			if h.HasFCS && h.FrameContentSize != uint64(len(in)) {
				t.Errorf("%s/%s: header content size %d", level, name, h.FrameContentSize)
			}
			// The encoder buffers a stream whole up to its block size — 64 KiB
			// at the fastest level, 128 KiB at the others — and such a stream
			// above 1 KiB is a single-segment frame.
			blockSize := 128 << 10
			if level == Fastest {
				blockSize = 64 << 10
			}
			if single := len(in) > 1024 && len(in) <= blockSize; single != h.SingleSegment {
				t.Errorf("%s/%s: single-segment %v, want %v", level, name, h.SingleSegment, single)
			}
			if h.DictionaryID != 0 {
				t.Errorf("%s/%s: header references dictionary %d", level, name, h.DictionaryID)
			}
			got, err := decompressWith(t, nil, frame, uint64(len(in)), false)
			if err != nil {
				t.Errorf("%s/%s: %v", level, name, err)
				continue
			}
			if !bytes.Equal(got, in) {
				t.Errorf("%s/%s: plaintext differs", level, name)
			}
		}
	}
}

func TestEmptyRefused(t *testing.T) {
	w, err := NewWriter(Params{Level: Fastest})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Release()
	var buf bytes.Buffer
	for _, size := range []int64{0, -1, -2} {
		if err := w.Reset(&buf, size); !errors.Is(err, ErrParams) {
			t.Errorf("Writer.Reset size %d: %v", size, err)
		}
	}
	if _, err := decompressWith(t, nil, emptyFrame, 0, false); !errors.Is(err, ErrParams) {
		t.Errorf("Reader.Reset size 0: %v", err)
	}
	if _, err := decompressWith(t, nil, emptyFrame, 1, false); !errors.Is(err, ErrSizeMismatch) {
		t.Errorf("empty frame for a 1-byte record: %v", err)
	}
}

func TestReuseIsDeterministic(t *testing.T) {
	in := text(100<<10, 3)
	p := Params{Level: Default}
	fresh := compressWith(t, p, in)
	w, err := NewWriter(p)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Release()
	for i := 0; i < 3; i++ {
		if got := compressOn(t, w, in); !bytes.Equal(got, fresh) {
			t.Fatalf("stream %d from a reused Writer differs from a fresh one", i)
		}
		if got := compressOn(t, w, noise(1000, uint64(i))); len(got) < 1000 {
			t.Fatalf("noise compressed to %d bytes", len(got))
		}
	}
	r, err := NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for i := 0; i < 3; i++ {
		if err := r.Reset(bytes.NewReader(fresh), uint64(len(in)), false); err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(r)
		if err != nil || !bytes.Equal(got, in) {
			t.Fatalf("stream %d from a reused Reader: %v", i, err)
		}
		if _, err := r.Read(make([]byte, 1)); err != io.EOF {
			t.Fatalf("read after EOF: %v", err)
		}
	}
}

func TestPadding(t *testing.T) {
	in := text(50<<10, 4)
	p := Params{Level: Fastest, Padding: 4096}
	frame := compressWith(t, p, in)
	if len(frame)%4096 != 0 {
		t.Fatalf("padded output is %d bytes", len(frame))
	}
	if header(t, frame).Skippable {
		t.Fatal("padding came first")
	}
	got, err := decompressWith(t, nil, frame, uint64(len(in)), false)
	if err != nil || !bytes.Equal(got, in) {
		t.Fatalf("round trip: %v", err)
	}
	// A frame shorter than the header read-ahead, followed by padding: the
	// padding begins inside the read-ahead and must still be found there.
	for _, tiny := range [][]byte{{0x30}, text(5, 41), text(12, 42)} {
		frame := compressWith(t, p, tiny)
		if got, err := decompressWith(t, nil, frame, uint64(len(tiny)), false); err != nil || !bytes.Equal(got, tiny) {
			t.Errorf("%d-byte padded stream: %v", len(tiny), err)
		}
	}
	// Padding is skippable frames after the real one; more of them, or a
	// hand-made one, are equally acceptable.
	extra := append(bytes.Clone(frame), skippableFrame(100)...)
	extra = append(extra, skippableFrame(0)...)
	got, err = decompressWith(t, nil, extra, uint64(len(in)), false)
	if err != nil || !bytes.Equal(got, in) {
		t.Fatalf("extra skippable frames: %v", err)
	}
}

func TestMaxEncodedSizeIsABound(t *testing.T) {
	inputs := [][]byte{{1}, text(255, 30), text(100<<10, 31), noise(100<<10, 32), noise(4095, 33), noise(4096, 34), noise(7, 35), noise(8, 36), noise(9, 37)}
	for _, pad := range []int{0, 1, 2, 3, 5, 7, 8, 9, 1000, 4096} {
		w, err := NewWriter(Params{Level: Fastest, Padding: pad})
		if err != nil {
			t.Fatal(err)
		}
		for _, in := range inputs {
			frame := compressOn(t, w, in)
			if m := w.MaxEncodedSize(len(in)); len(frame) > m || (pad > 1 && m%pad != 0) {
				t.Errorf("pad %d, %d bytes in: %d out, bound %d", pad, len(in), len(frame), m)
			}
		}
		w.Release()
	}
}

// samples are small records that share structure, the case a dictionary is
// for.
func samples(n int, seed uint64) [][]byte {
	rng := rand.New(rand.NewPCG(seed, 5))
	out := make([][]byte, n)
	for i := range out {
		out[i] = []byte(fmt.Sprintf(`{"id":%d,"name":"user-%d","email":"user%d@example.invalid","roles":["reader","writer"],"created":"2026-09-%02d","flags":%d}`,
			rng.IntN(1<<20), rng.IntN(1000), rng.IntN(1000), 1+rng.IntN(28), rng.IntN(64)))
	}
	return out
}

func TestDict(t *testing.T) {
	train := samples(200, 1)
	dict, err := BuildDict(train, 7, Default, 0)
	if err != nil {
		t.Fatal(err)
	}
	if id, err := DictID(dict); err != nil || id != 7 {
		t.Fatalf("DictID %d, %v", id, err)
	}
	in := samples(1, 99)[0]
	with := compressWith(t, Params{Level: Default, Dict: dict}, in)
	without := compressWith(t, Params{Level: Default}, in)
	if len(with) >= len(without) {
		t.Errorf("dictionary did not help: %d with, %d without", len(with), len(without))
	}
	if h := header(t, with); h.DictionaryID != 7 {
		t.Errorf("frame references dictionary %d", h.DictionaryID)
	}
	got, err := decompressWith(t, dict, with, uint64(len(in)), true)
	if err != nil || !bytes.Equal(got, in) {
		t.Fatalf("round trip with dictionary: %v", err)
	}
	// The record and the frame must agree about the dictionary.
	if _, err := decompressWith(t, dict, with, uint64(len(in)), false); !errors.Is(err, ErrDictMismatch) {
		t.Errorf("dict frame, record says none: %v", err)
	}
	if _, err := decompressWith(t, dict, without, uint64(len(in)), true); !errors.Is(err, ErrDictMismatch) {
		t.Errorf("plain frame, record says dict: %v", err)
	}
	if _, err := decompressWith(t, nil, with, uint64(len(in)), false); !errors.Is(err, ErrDictMismatch) {
		t.Errorf("dict frame, reader has none: %v", err)
	}
	if _, err := decompressWith(t, nil, with, uint64(len(in)), true); !errors.Is(err, ErrParams) {
		t.Errorf("record says dict, reader has none: %v", err)
	}
	// An empty frame in front of the dictionary frame: the first header
	// passes the dictionary check, and only the first frame is decoded, so
	// the stream comes up short instead of decoding through the dictionary.
	if _, err := decompressWith(t, dict, append(bytes.Clone(emptyFrame), with...), uint64(len(in)), false); !errors.Is(err, ErrCorrupt) {
		t.Errorf("empty frame then dict frame: %v", err)
	}
	// A different dictionary under the same ID: the frame passes the header
	// check and fails the decode.
	other, err := BuildDict(samples(200, 2), 7, Default, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decompressWith(t, other, with, uint64(len(in)), true); !errors.Is(err, ErrCorrupt) {
		t.Errorf("wrong dictionary, same ID: %v", err)
	}

	// Large training samples: the library's trainer cannot take more than
	// one block per sample, so BuildDict truncates them (a 1 MiB sample
	// used to panic the process).
	big := append(train, bytes.Repeat([]byte("large sample content, repeated; "), 1<<15))
	if d, err := BuildDict(big, 8, Fastest, 0); err != nil {
		t.Errorf("large sample: %v", err)
	} else if id, err := DictID(d); err != nil || id != 8 {
		t.Errorf("large sample dictionary: id %d, %v", id, err)
	}

	// Builder refusals.
	if _, err := BuildDict(train, 0, Default, 0); !errors.Is(err, ErrParams) {
		t.Errorf("id 0: %v", err)
	}
	if _, err := BuildDict(train, 1, Level(9), 0); !errors.Is(err, ErrParams) {
		t.Errorf("bad level: %v", err)
	}
	if _, err := BuildDict(train, 1, Default, MaxDictContent+1); !errors.Is(err, ErrParams) {
		t.Errorf("oversized: %v", err)
	}
	if _, err := BuildDict([][]byte{[]byte("short"), nil}, 1, Default, 0); !errors.Is(err, ErrParams) {
		t.Errorf("no usable sample: %v", err)
	}
	// One sample: its first half is the content, the rest trains.
	if d, err := BuildDict([][]byte{text(4000, 51)}, 9, Default, 0); err != nil {
		t.Errorf("one sample: %v", err)
	} else if id, err := DictID(d); err != nil || id != 9 {
		t.Errorf("one-sample dictionary: id %d, %v", id, err)
	}
	if _, err := BuildDict([][]byte{bytes.Repeat([]byte("ab"), 2000)}, 9, Default, 0); !errors.Is(err, ErrParams) {
		t.Errorf("pure repetition: %v", err)
	}
	if _, err := DictID(make([]byte, MaxDictSize+1)); !errors.Is(err, ErrParams) {
		t.Errorf("DictID over MaxDictSize: %v", err)
	}
	if _, err := DictID(dict[:20]); !errors.Is(err, ErrParams) {
		t.Errorf("DictID truncated: %v", err)
	}
}

func TestSelectHistory(t *testing.T) {
	s := [][]byte{bytes.Repeat([]byte("a"), 100), bytes.Repeat([]byte("b"), 10), bytes.Repeat([]byte("c"), 50)}
	if h := selectHistory(s, 1000); len(h) != 160 {
		t.Errorf("under budget: %d", len(h))
	}
	h := selectHistory(s, 60)
	if len(h) != 60 {
		t.Fatalf("budget 60 gave %d", len(h))
	}
	// 20 each in the first round; b is exhausted at 10, and its 10 go to
	// a and c: 25 / 10 / 25.
	if bytes.Count(h, []byte("a")) != 25 || bytes.Count(h, []byte("b")) != 10 || bytes.Count(h, []byte("c")) != 25 {
		t.Errorf("shares: %q", h)
	}
}

func TestSizeMismatch(t *testing.T) {
	// With a content size in the header the mismatch is caught at Reset;
	// a stream under 256 bytes has none, and is caught by counting.
	long := text(300<<10, 6)
	longFrame := compressWith(t, Params{Level: Default}, long)
	short := text(100, 7)
	shortFrame := compressWith(t, Params{Level: Default}, short)
	if !header(t, longFrame).HasFCS || header(t, shortFrame).HasFCS {
		t.Fatal("header content-size assumptions do not hold")
	}
	r, err := NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, delta := range []int{1, -1} {
		size := uint64(len(long) + delta)
		if err := r.Reset(bytes.NewReader(longFrame), size, false); !errors.Is(err, ErrSizeMismatch) {
			t.Errorf("long frame, size %d: Reset %v", size, err)
		}
		size = uint64(len(short) + delta)
		if err := r.Reset(bytes.NewReader(shortFrame), size, false); err != nil {
			t.Fatalf("short frame, size %d: Reset %v", size, err)
		}
		if _, err := io.ReadAll(r); !errors.Is(err, ErrSizeMismatch) {
			t.Errorf("short frame, size %d: read %v", size, err)
		}
		if _, err := r.Read(make([]byte, 1)); !errors.Is(err, ErrSizeMismatch) {
			t.Errorf("error not sticky: %v", err)
		}
	}
	if err := r.Reset(bytes.NewReader(longFrame), format.MaxOrigSize+1, false); !errors.Is(err, ErrParams) {
		t.Errorf("size over MaxOrigSize: %v", err)
	}
}

func TestCorrupt(t *testing.T) {
	in := text(30<<10, 8)
	frame := compressWith(t, Params{Level: Default}, in)
	positions := []int{0, 3, 4, 5, 6, 7, 8, 9, 10, len(frame) / 2, len(frame) - 5, len(frame) - 1}
	for _, pos := range positions {
		bad := bytes.Clone(frame)
		bad[pos] ^= 0x55
		_, err := decompressWith(t, nil, bad, uint64(len(in)), false)
		if !errors.Is(err, ErrCorrupt) {
			t.Errorf("flip at %d: %v, want ErrCorrupt", pos, err)
		}
	}
	for _, cut := range []int{1, 4, 10, len(frame) / 2, len(frame) - 1} {
		_, err := decompressWith(t, nil, frame[:cut], uint64(len(in)), false)
		if !errors.Is(err, ErrCorrupt) {
			t.Errorf("cut to %d: %v, want ErrCorrupt", cut, err)
		}
	}
	if _, err := decompressWith(t, nil, nil, uint64(len(in)), false); !errors.Is(err, ErrCorrupt) {
		t.Errorf("empty stream: %v", err)
	}
	cases := map[string][]byte{
		"leading skippable frame": append(skippableFrame(0), frame...),
		"trailing garbage":        append(bytes.Clone(frame), 1, 2, 3),
		"trailing partial header": append(bytes.Clone(frame), 0x28, 0xb5, 0x2f),
		"second frame":            append(bytes.Clone(frame), frame...),
		"trailing empty frame":    append(bytes.Clone(frame), emptyFrame...),
		"padding then frame":      append(append(bytes.Clone(frame), skippableFrame(5)...), emptyFrame...),
		"truncated skippable":     append(bytes.Clone(frame), skippableFrame(100)[:50]...),
		"leading empty frame":     append(bytes.Clone(emptyFrame), frame...),
		"reserved block type":     func() []byte { b := bytes.Clone(frame); b[header(t, frame).HeaderSize] |= 0x06; return b }(),
	}
	for name, stream := range cases {
		if _, err := decompressWith(t, nil, stream, uint64(len(in)), false); !errors.Is(err, ErrCorrupt) {
			t.Errorf("%s: %v, want ErrCorrupt", name, err)
		}
	}
}

// failAfter returns a source error after n bytes.
type failAfter struct {
	r   io.Reader
	n   int
	err error
}

func (f *failAfter) Read(p []byte) (int, error) {
	if f.n <= 0 {
		return 0, f.err
	}
	n, err := f.r.Read(p[:min(len(p), f.n)])
	f.n -= n
	return n, err
}

func TestSourceErrorPassesThrough(t *testing.T) {
	in := text(200<<10, 9)
	frame := compressWith(t, Params{Level: Default}, in)
	sentinel := errors.New("bad sector")
	r, err := NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, at := range []int{3, 20, len(frame) / 2, len(frame) - 2, len(frame)} {
		src := &failAfter{r: bytes.NewReader(frame), n: at, err: sentinel}
		err := r.Reset(src, uint64(len(in)), false)
		if err == nil {
			_, err = io.ReadAll(r)
		}
		if !errors.Is(err, sentinel) || errors.Is(err, ErrCorrupt) {
			t.Errorf("source error after %d bytes: %v", at, err)
		}
	}
}

func TestWindowLimit(t *testing.T) {
	// A header demanding more window than the record's size can need is
	// refused at Reset, before the decoder allocates anything.
	h := zstd.Header{WindowSize: 1 << 20, HasCheckSum: true}
	frame, err := h.AppendTo(nil)
	if err != nil {
		t.Fatal(err)
	}
	frame = append(frame, 0, 0, 0, 0, 0, 0, 0)
	if _, err := decompressWith(t, nil, frame, 10, false); !errors.Is(err, ErrWindow) {
		t.Errorf("1 MiB window for 10 bytes: %v", err)
	}
	// The same header is fine for a record that could need it.
	if _, err := decompressWith(t, nil, frame, 1<<20, false); errors.Is(err, ErrWindow) {
		t.Errorf("1 MiB window for 1 MiB: %v", err)
	}
	// Our own Writer's frames always fit: with a declared size the encoder
	// shrinks the header's window to the power of two above the content,
	// which is exactly the Reader's limit.
	in := text(300<<10, 10)
	big := compressWith(t, Params{Level: Default, Window: 1 << 22}, in)
	if h := header(t, big); h.WindowSize != 1<<19 {
		t.Errorf("header window %d for a 300 KiB declared stream", h.WindowSize)
	}
	if got, err := decompressWith(t, nil, big, uint64(len(in)), false); err != nil || !bytes.Equal(got, in) {
		t.Errorf("4 MiB Params.Window, 300 KiB file: %v", err)
	}
	// The Reader's own cap still applies above the size-derived limit.
	long := text(5<<20, 11)
	wide := compressWith(t, Params{Level: Fastest, Window: 1 << 22}, long)
	if h := header(t, wide); h.WindowSize != 1<<22 {
		t.Errorf("header window %d for a 5 MiB stream", h.WindowSize)
	}
	r, err := newReader(nil, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := r.Reset(bytes.NewReader(wide), uint64(len(long)), false); !errors.Is(err, ErrWindow) {
		t.Errorf("4 MiB window under a 1 MiB cap: %v", err)
	}
	// A single-segment frame declares no window; its content size is the
	// window, and it is held to the same limit (here: 1 GiB for a 1 GiB
	// record, above MaxWindow).
	single := zstd.Header{SingleSegment: true, HasFCS: true, FrameContentSize: 1 << 30, HasCheckSum: true}
	sframe, err := single.AppendTo(nil)
	if err != nil {
		t.Fatal(err)
	}
	sframe = append(sframe, 0, 0, 0, 0, 0, 0, 0)
	if _, err := decompressWith(t, nil, sframe, 1<<30, false); !errors.Is(err, ErrWindow) {
		t.Errorf("1 GiB single-segment frame: %v", err)
	}
	// windowLimit arithmetic at the edges.
	full, _ := NewReader(nil)
	defer full.Close()
	for size, want := range map[uint64]uint64{1: MinWindow, 1023: MinWindow, 1024: 2048, 1025: 2048, 1 << 20: 1 << 21, MaxWindow: MaxWindow, format.MaxOrigSize: MaxWindow} {
		if got := full.windowLimit(size); got != want {
			t.Errorf("windowLimit(%d) = %d, want %d", size, got, want)
		}
	}
}

// TestLargestHeader: the largest legal frame header is 18 bytes — a 4-byte
// dictionary ID and an 8-byte content size — one more than the library's
// zstd.HeaderMaxSize, which omits the magic. The walker's read-ahead must
// hold it.
func TestLargestHeader(t *testing.T) {
	h := zstd.Header{WindowSize: 1 << 20, DictionaryID: 70000, HasFCS: true, FrameContentSize: 0xFFFFFFFF, HasCheckSum: true}
	frame, err := h.AppendTo(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(frame) != 18 || len(frameReader{}.head) < 18+3 {
		t.Fatalf("header %d bytes, read-ahead %d", len(frame), len(frameReader{}.head))
	}
	frame = append(frame, 0, 0, 0)
	var f frameReader
	got, err := f.start(bytes.NewReader(frame))
	if err != nil || got.HeaderSize != 18 || got.DictionaryID != 70000 || got.FrameContentSize != 0xFFFFFFFF {
		t.Fatalf("start: %+v, %v", got, err)
	}
	// And through Reset: the checks fail on the record, not on the parse.
	r, err := NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := r.Reset(bytes.NewReader(frame), 0xFFFFFFFF, false); !errors.Is(err, ErrDictMismatch) {
		t.Errorf("18-byte header: %v, want ErrDictMismatch", err)
	}
}

// zeroReader never progresses.
type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { return 0, nil }

func TestNoProgressSource(t *testing.T) {
	r, err := NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := r.Reset(zeroReader{}, 10, false); !errors.Is(err, io.ErrNoProgress) {
		t.Errorf("stalled source at the header: %v", err)
	}
	in := text(200<<10, 50)
	frame := compressWith(t, Params{Level: Default}, in)
	stall := io.MultiReader(bytes.NewReader(frame[:len(frame)/2]), zeroReader{})
	if err := r.Reset(stall, uint64(len(in)), false); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(r); !errors.Is(err, io.ErrNoProgress) {
		t.Errorf("stalled source mid-frame: %v", err)
	}
}

func TestProbe(t *testing.T) {
	d := ProbeBytes(noise(1<<20, 12))
	if d.Compressible || d.Ratio <= RawThreshold || d.Sampled != 3*SampleSize {
		t.Errorf("noise: %+v", d)
	}
	d = ProbeBytes(text(1<<20, 13))
	if !d.Compressible || d.Ratio > 0.6 || d.Sampled != 3*SampleSize {
		t.Errorf("text: %+v", d)
	}
	d = ProbeBytes(nil)
	if d.Compressible || d.Ratio != 1 || d.Sampled != 0 {
		t.Errorf("empty: %+v", d)
	}
	small := text(100<<10, 14)
	d = ProbeBytes(small)
	if !d.Compressible || d.Sampled != int64(len(small)) {
		t.Errorf("small: %+v", d)
	}
	d = ProbeBytes(noise(3*SampleSize, 15))
	if d.Sampled != 3*SampleSize || d.Compressible {
		t.Errorf("three samples exactly: %+v", d)
	}
	// A compressible head on an incompressible body: the median of the
	// three samples says raw, where the mean (about 0.78) would have said
	// compress.
	mixed := append(text(SampleSize, 16), noise(4<<20, 17)...)
	d = ProbeBytes(mixed)
	if d.Compressible || d.Ratio <= RawThreshold || d.Sampled != 3*SampleSize {
		t.Errorf("mixed: %+v", d)
	}
	// The mirror: a compressible middle between two incompressible thirds
	// is stored raw too (the accepted cost of the median).
	mirror := append(append(noise(2<<20, 18), text(SampleSize, 19)...), noise(2<<20, 20)...)
	if d = ProbeBytes(mirror); d.Compressible {
		t.Errorf("mirror: %+v", d)
	}
	if _, err := Probe(bytes.NewReader(make([]byte, 100)), 1000); err == nil {
		t.Error("short source accepted")
	}
	if _, err := Probe(bytes.NewReader(nil), -1); !errors.Is(err, ErrParams) {
		t.Errorf("negative size: %v", err)
	}
	// Probes run in parallel without sharing an encoder.
	done := make(chan Decision, 8)
	for i := 0; i < 8; i++ {
		go func() { done <- ProbeBytes(text(1<<20, uint64(40+i))) }()
	}
	for i := 0; i < 8; i++ {
		if d := <-done; !d.Compressible {
			t.Errorf("parallel probe: %+v", d)
		}
	}
}

func TestWriterAndReaderStates(t *testing.T) {
	w, err := NewWriter(Params{Level: Fastest})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte{1}); !errors.Is(err, ErrNoStream) {
		t.Errorf("write before Reset: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("Close without stream: %v", err)
	}
	var buf bytes.Buffer
	if err := w.Reset(&buf, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte{1, 2, 3, 4}); !errors.Is(err, ErrParams) {
		t.Errorf("over the declared size: %v", err)
	}
	if err := w.Close(); !errors.Is(err, ErrParams) {
		t.Errorf("Close after over-write: %v", err)
	}
	buf.Reset()
	if err := w.Reset(&buf, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); !errors.Is(err, ErrParams) {
		t.Errorf("Close under the declared size: %v", err)
	}
	if _, err := w.Write([]byte{1}); !errors.Is(err, ErrNoStream) {
		t.Errorf("write after Close: %v", err)
	}
	// Still usable after both failures.
	if got := compressOn(t, w, []byte("still fine")); len(got) == 0 {
		t.Error("no output after recovery")
	}
	w.Release()
	w.Release()
	func() {
		defer func() {
			if recover() == nil {
				t.Error("MaxEncodedSize after Release did not panic")
			}
		}()
		w.MaxEncodedSize(1)
	}()
	if err := w.Reset(&buf, 1); !errors.Is(err, ErrClosed) {
		t.Errorf("Reset after Release: %v", err)
	}
	if _, err := w.Write([]byte{1}); !errors.Is(err, ErrClosed) {
		t.Errorf("write after Release: %v", err)
	}
	if err := w.Close(); !errors.Is(err, ErrClosed) {
		t.Errorf("Close after Release: %v", err)
	}

	r, err := NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Read(make([]byte, 1)); !errors.Is(err, ErrNoStream) {
		t.Errorf("read before Reset: %v", err)
	}
	// A failed Reset leaves no stream.
	if err := r.Reset(bytes.NewReader(nil), 5, false); err == nil {
		t.Error("empty stream accepted")
	}
	if _, err := r.Read(make([]byte, 1)); !errors.Is(err, ErrNoStream) {
		t.Errorf("read after failed Reset: %v", err)
	}
	r.Close()
	r.Close()
	if _, err := r.Read(make([]byte, 1)); !errors.Is(err, ErrClosed) {
		t.Errorf("read after Close: %v", err)
	}
	if err := r.Reset(bytes.NewReader(nil), 1, false); !errors.Is(err, ErrClosed) {
		t.Errorf("Reset after Close: %v", err)
	}
}

// goroutinesSettle waits for the goroutine count to come back to base.
func goroutinesSettle(t *testing.T, base int, what string) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if runtime.NumGoroutine() <= base {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("%s: %d goroutines, started with %d", what, runtime.NumGoroutine(), base)
}

func TestConcurrentWriter(t *testing.T) {
	base := runtime.NumGoroutine()
	// A 64 KiB window makes jobs of 512 KiB, so 2 MiB is four of them.
	p := Params{Level: Default, Window: 1 << 16, Concurrency: 4}
	in := text(2<<20, 20)
	frame := compressWith(t, p, in)
	got, err := decompressWith(t, nil, frame, uint64(len(in)), false)
	if err != nil || !bytes.Equal(got, in) {
		t.Fatalf("concurrent stream: %v", err)
	}
	goroutinesSettle(t, base, "after a completed stream and Release")

	// Released mid-stream.
	w, err := NewWriter(p)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := w.Reset(&buf, int64(len(in))); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(in[:1<<20]); err != nil {
		t.Fatal(err)
	}
	w.Release()
	goroutinesSettle(t, base, "after Release mid-stream")

	// A stream that fails: Close must not leave the workers behind.
	w, err = NewWriter(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Reset(&buf, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(in[:1<<20]); !errors.Is(err, ErrParams) {
		t.Fatalf("over-write: %v", err)
	}
	if err := w.Close(); !errors.Is(err, ErrParams) {
		t.Fatalf("Close: %v", err)
	}
	goroutinesSettle(t, base, "after a failed stream")
	// And a successful Close without Release.
	if got := compressOn(t, w, in); len(got) == 0 {
		t.Fatal("no output")
	}
	goroutinesSettle(t, base, "after a successful Close")
	w.Release()
}

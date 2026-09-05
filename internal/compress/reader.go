package compress

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/bits"

	"github.com/klauspost/compress/zstd"

	"github.com/dreamxwarden01/enfold/internal/format"
)

// Reader decompresses streams, one per Reset, each held to what the index
// says about it (R27). It runs synchronously on the caller's goroutine — one
// goroutine per file is the archive layer's parallelism, not one per block —
// and holds one window of history, so it is not safe for concurrent use.
//
// Within a stream, errors are sticky: a frame that failed once has left the
// decoder with nothing worth continuing from.
type Reader struct {
	dec       *zstd.Decoder
	dictID    uint32 // the registered dictionary, 0 when none
	maxWindow uint64
	active    bool
	closed    bool
	size      uint64 // the record's plaintext length
	n         uint64 // decoded so far
	err       error
	src       *errReader
	frame     frameReader
}

// NewReader returns a Reader that can decode frames written with dict, or
// without one; dict is nil for an archive without a dictionary. It parses the
// dictionary and allocates the decoder once: keep the Reader and Reset it per
// file.
func NewReader(dict []byte) (*Reader, error) {
	return newReader(dict, MaxWindow)
}

func newReader(dict []byte, maxWindow uint64) (*Reader, error) {
	var dictID uint32
	opts := []zstd.DOption{
		// Synchronous decoding is load-bearing twice over: the decoder then
		// reads its source lazily, so nothing touches the frame walker from
		// another goroutine, and it stays on the streaming path, where the
		// two limits below are window caps rather than output caps. They are
		// the library-side backstop; the bound that matters is Reset's own
		// check of the frame's window against the record.
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderMaxWindow(maxWindow),
		zstd.WithDecoderMaxMemory(maxWindow),
	}
	if dict != nil {
		id, err := DictID(dict)
		if err != nil {
			return nil, err
		}
		dictID = id
		opts = append(opts, zstd.WithDecoderDicts(dict))
	}
	dec, err := zstd.NewReader(nil, opts...)
	if err != nil {
		return nil, err
	}
	return &Reader{dec: dec, dictID: dictID, maxWindow: maxWindow}, nil
}

// windowLimit is the largest window a frame for size bytes of plaintext may
// declare: the smallest power of two above size, at least MinWindow, at most
// the Reader's cap. A frame cannot usefully reference further back than its
// content is long, and a Writer given the size never asks for more (the
// library's header window is exactly this power of two, capped by
// Params.Window), so the decoder's allocation is bounded by the record.
func (r *Reader) windowLimit(size uint64) uint64 {
	return min(r.maxWindow, max(uint64(MinWindow), uint64(1)<<bits.Len64(size)))
}

// Reset starts decoding a new stream from src, which must yield exactly the
// bytes the archive stored for the file — the plaintext of its
// internal/stream blob — and nothing else. size is the record's orig_size,
// at least 1 (an empty file is stored raw, R27); withDict says whether the
// record's storage is 3, in which case the frame must reference the Reader's
// dictionary, and otherwise it must reference none.
//
// Reset reads the frame header and refuses the stream before decoding
// anything if the header contradicts the record: the wrong dictionary
// (ErrDictMismatch), a declared content size other than size
// (ErrSizeMismatch), or a window above what size can need (ErrWindow). An
// empty stream, or one that starts with a skippable frame, is ErrCorrupt.
func (r *Reader) Reset(src io.Reader, size uint64, withDict bool) error {
	if r.closed {
		return ErrClosed
	}
	r.active = false
	if size == 0 || size > format.MaxOrigSize {
		return fmt.Errorf("%w: size %d not in [1, %d]", ErrParams, size, uint64(format.MaxOrigSize))
	}
	var want uint32
	if withDict {
		if r.dictID == 0 {
			return fmt.Errorf("%w: record requires a dictionary but the reader has none", ErrParams)
		}
		want = r.dictID
	}

	r.src = &errReader{r: src}
	h, err := r.frame.start(r.src)
	if err != nil {
		return err
	}
	// A single-segment frame declares no window: the format makes its
	// content size the window, and the library always emits a content-size
	// field for such a frame, so the size check below pins it to the record
	// before the window check compares it. The two checks may not be
	// separated.
	window := h.WindowSize
	if h.SingleSegment {
		window = max(h.FrameContentSize, uint64(MinWindow))
	}
	switch {
	case h.Skippable:
		return fmt.Errorf("%w: stream starts with a skippable frame", ErrCorrupt)
	case h.DictionaryID != want:
		return fmt.Errorf("%w: frame uses dictionary %d, record says %d", ErrDictMismatch, h.DictionaryID, want)
	case h.HasFCS && h.FrameContentSize != size:
		return fmt.Errorf("%w: frame declares %d bytes, record says %d", ErrSizeMismatch, h.FrameContentSize, size)
	case window > r.windowLimit(size):
		return fmt.Errorf("%w: frame window %d for a %d-byte file, limit %d", ErrWindow, window, size, r.windowLimit(size))
	}

	if err := r.dec.Reset(&r.frame); err != nil {
		return err
	}
	r.active, r.size, r.n, r.err = true, size, 0, nil
	return nil
}

// Read returns decompressed bytes, and io.EOF once exactly size bytes have
// been returned and the stream has been found to hold nothing after the
// frame but skippable padding. A frame that produces more or fewer bytes is
// ErrSizeMismatch; a malformed one, a second frame, or trailing bytes are
// ErrCorrupt; an error from the source is returned as it is.
func (r *Reader) Read(p []byte) (int, error) {
	if r.closed {
		return 0, ErrClosed
	}
	if !r.active {
		return 0, ErrNoStream
	}
	if r.err != nil {
		return 0, r.err
	}
	n, err := r.dec.Read(p)
	r.n += uint64(n)
	if r.n > r.size {
		clear(p[:n])
		r.err = fmt.Errorf("%w: more than %d bytes", ErrSizeMismatch, r.size)
		return 0, r.err
	}
	switch {
	case err == nil:
		return n, nil
	case errors.Is(err, io.EOF):
		if r.n != r.size {
			r.err = fmt.Errorf("%w: %d of %d bytes", ErrSizeMismatch, r.n, r.size)
			return n, r.err
		}
		if err := r.frame.finish(); err != nil {
			r.err = r.classify(err)
			return n, r.err
		}
		r.err = io.EOF
		return n, io.EOF
	}
	r.err = r.classify(err)
	return n, r.err
}

// classify decides whose fault a failure is: the source's, if the source
// returned an error; the frame walker's, if it refused the frame; else the
// frame's, as seen by the decoder.
func (r *Reader) classify(err error) error {
	if r.src.err != nil && !errors.Is(r.src.err, io.EOF) {
		return r.src.err
	}
	if errors.Is(err, ErrCorrupt) {
		return err
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("%w: frame is truncated", ErrCorrupt)
	}
	return fmt.Errorf("%w: %v", ErrCorrupt, err)
}

// Close releases the decoder and its window. The Reader is unusable
// afterwards.
func (r *Reader) Close() error {
	if !r.closed {
		r.closed = true
		r.active = false
		r.dec.Close()
		r.dec = nil
		r.src = nil
		r.frame = frameReader{}
	}
	return nil
}

// errReader remembers the last error the source returned, so that a decoder
// failure caused by the source can be reported as the source's error rather
// than as corruption, and turns a source that keeps returning (0, nil) into
// io.ErrNoProgress instead of letting Reset or Read spin on it.
type errReader struct {
	r    io.Reader
	err  error
	idle int // consecutive (0, nil) reads
}

const maxIdleReads = 100

func (e *errReader) Read(p []byte) (int, error) {
	n, err := e.r.Read(p)
	if n == 0 && err == nil && len(p) > 0 {
		e.idle++
		if e.idle >= maxIdleReads {
			err = io.ErrNoProgress
		}
	} else {
		e.idle = 0
	}
	if err != nil {
		e.err = err
	}
	return n, err
}

// frameReader hands the decoder exactly one zstd frame and then EOF, walking
// the frame's block headers to find where it ends, so that the decoder never
// sees a second frame and the checks Reset made on the first header hold for
// everything decoded. finish then reads what follows and accepts only
// skippable frames — the Writer's padding — before the source's own EOF.
//
// The walk needs no decompression: a frame is a header of a known size, then
// blocks whose 3-byte headers give the block's stored size (1 for an RLE
// block) and a last-block flag, then a 4-byte checksum if the header says so.
type frameReader struct {
	src io.Reader
	// head holds the read-ahead: 4 magic bytes, at most 14 bytes of frame
	// header (descriptor, window, 4-byte dictionary ID, 8-byte content
	// size) and a 3-byte block header. Not zstd.HeaderMaxSize, which is 17:
	// it counts the spec's 14-byte header without the magic and cannot hold
	// the largest legal header, an 18-byte one.
	head     [headReadAhead]byte
	buffered []byte // header bytes read ahead, served before src
	pending  []byte // block-header bytes parsed, to be served next
	pendBuf  [3]byte
	state    frameState
	remain   int  // bytes left in the current pass-through segment
	tail     int  // checksum bytes to pass after the last block's payload
	checksum bool // the frame carries a checksum
	err      error
}

const headReadAhead = 4 + 14 + 3

type frameState uint8

const (
	framePass        frameState = iota // serving remain bytes through
	frameBlockHeader                   // the next 3 bytes are a block header
	frameDone                          // the frame has been served in full
)

// start reads and parses the first frame header from src and primes the
// walker to serve the frame from its first byte.
func (f *frameReader) start(src io.Reader) (zstd.Header, error) {
	*f = frameReader{src: src}
	n, err := io.ReadFull(src, f.head[:])
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		if errors.Is(err, io.EOF) {
			return zstd.Header{}, fmt.Errorf("%w: empty stream", ErrCorrupt)
		}
		return zstd.Header{}, err
	}
	var h zstd.Header
	if err := h.Decode(f.head[:n]); err != nil {
		return zstd.Header{}, fmt.Errorf("%w: frame header: %v", ErrCorrupt, err)
	}
	f.buffered = f.head[:n]
	f.checksum = h.HasCheckSum
	f.state = framePass
	f.remain = h.HeaderSize
	return h, nil
}

// pull reads into p from the read-ahead first, then from the source.
func (f *frameReader) pull(p []byte) (int, error) {
	if len(f.buffered) > 0 {
		n := copy(p, f.buffered)
		f.buffered = f.buffered[n:]
		return n, nil
	}
	return f.src.Read(p)
}

// truncated turns the source's clean EOF into the frame's fault.
func truncated(err error) error {
	if errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: frame is truncated", ErrCorrupt)
	}
	return err
}

// pullFull fills p or fails.
func (f *frameReader) pullFull(p []byte) error {
	for len(p) > 0 {
		n, err := f.pull(p)
		p = p[n:]
		if err != nil && len(p) > 0 {
			return truncated(err)
		}
	}
	return nil
}

func (f *frameReader) Read(p []byte) (int, error) {
	for {
		if len(f.pending) > 0 {
			n := copy(p, f.pending)
			f.pending = f.pending[n:]
			return n, nil
		}
		if f.err != nil {
			return 0, f.err
		}
		switch f.state {
		case frameDone:
			return 0, io.EOF

		case frameBlockHeader:
			bh := f.pendBuf[:]
			if err := f.pullFull(bh); err != nil {
				f.err = err
				return 0, err
			}
			v := uint32(bh[0]) | uint32(bh[1])<<8 | uint32(bh[2])<<16
			size := int(v >> 3)
			switch (v >> 1) & 3 {
			case 1: // RLE: one stored byte; size is what it expands to
				size = 1
			case 3:
				f.err = fmt.Errorf("%w: reserved block type", ErrCorrupt)
				return 0, f.err
			}
			f.pending = bh
			f.remain = size
			f.state = framePass
			if v&1 != 0 { // last block
				f.tail = -1
				if f.checksum {
					f.tail = 4
				}
			}

		case framePass:
			if f.remain == 0 {
				switch {
				case f.tail > 0:
					f.remain, f.tail = f.tail, -1
				case f.tail < 0:
					f.state = frameDone
				default:
					f.state = frameBlockHeader
				}
				continue
			}
			n, err := f.pull(p[:min(len(p), f.remain)])
			f.remain -= n
			if err != nil && f.remain > 0 {
				f.err = truncated(err)
				if n > 0 {
					return n, nil
				}
				return 0, f.err
			}
			return n, nil
		}
	}
}

// finish, called once the decoder has reported the end of the frame, checks
// that the walker agrees and that nothing follows the frame in the source
// but skippable frames. What follows may begin inside the header read-ahead
// when the frame is shorter than that, so the trailer is read through the
// same path as the frame.
func (f *frameReader) finish() error {
	if f.state != frameDone || len(f.pending) != 0 {
		return fmt.Errorf("%w: decoder ended before the frame did", ErrCorrupt)
	}
	rest := io.MultiReader(bytes.NewReader(f.buffered), f.src)
	f.buffered = nil
	var hdr [8]byte
	for {
		n, err := io.ReadFull(rest, hdr[:])
		if err != nil {
			if errors.Is(err, io.EOF) && n == 0 {
				return nil
			}
			if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
				return fmt.Errorf("%w: %d trailing bytes after the frame", ErrCorrupt, n)
			}
			return err
		}
		var h zstd.Header
		if err := h.Decode(hdr[:]); err != nil {
			return fmt.Errorf("%w: after the frame: %v", ErrCorrupt, err)
		}
		if !h.Skippable {
			return fmt.Errorf("%w: a second frame follows the first", ErrCorrupt)
		}
		if _, err := io.CopyN(io.Discard, rest, int64(h.SkippableSize)); err != nil {
			return truncated(err)
		}
	}
}

package compress

import (
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"
)

// Writer compresses streams, one zstd frame each, under one Params. Reset
// starts a stream on a destination; Write feeds it; Close finishes the frame.
// A Writer is reusable: after Close, Reset starts the next stream with the
// encoder's buffers already allocated.
//
// With Concurrency above 1 the encoder runs goroutines that hold it, and
// everything it has allocated, until Reset, a successful Close, or Release
// stops them: such a Writer must be Released when it is done with, or it
// leaks — the garbage collector cannot reclaim what a goroutine references.
// When a Write or Close fails, the destination may have grown further during
// the failing call, from work such an encoder had already dispatched; what
// it holds is an incomplete frame in every case.
type Writer struct {
	enc    *zstd.Encoder
	pad    int
	active bool
	closed bool
	size   int64 // declared plaintext length of the stream
	n      int64 // plaintext bytes accepted so far
	err    error // sticky within a stream
}

// NewWriter returns a Writer for p. It parses the dictionary and allocates
// the encoder's window and tables, which for a large window is not cheap:
// keep the Writer and Reset it rather than making one per file.
func NewWriter(p Params) (*Writer, error) {
	opts, err := p.options()
	if err != nil {
		return nil, err
	}
	enc, err := zstd.NewWriter(nil, opts...)
	if err != nil {
		return nil, err
	}
	return &Writer{enc: enc, pad: p.Padding}, nil
}

// Reset starts a new stream on dst for exactly size bytes of plaintext,
// size at least 1: an empty file is stored raw (R27), and the size is what
// lets a Reader bound the decoder's window from the record rather than from
// the frame. The size reaches the frame header whenever the format can carry
// it — from 256 bytes, or in the single-segment frame of a stream the
// encoder buffers whole — so a Reader checks it before decoding; below that
// both sides enforce it by counting. Reset on an unfinished stream abandons
// it, leaving the destination with an incomplete frame.
func (w *Writer) Reset(dst io.Writer, size int64) error {
	if w.closed {
		return ErrClosed
	}
	if size < 1 {
		return fmt.Errorf("%w: size %d, must be at least 1", ErrParams, size)
	}
	w.enc.ResetContentSize(dst, size)
	w.active, w.size, w.n, w.err = true, size, 0, nil
	return nil
}

// Write compresses p into the current stream. Writing more than the declared
// size is ErrParams.
func (w *Writer) Write(p []byte) (int, error) {
	if w.closed {
		return 0, ErrClosed
	}
	if !w.active {
		return 0, ErrNoStream
	}
	if w.err != nil {
		return 0, w.err
	}
	if int64(len(p)) > w.size-w.n {
		w.err = fmt.Errorf("%w: more than the declared %d bytes written", ErrParams, w.size)
		w.abandon()
		return 0, w.err
	}
	n, err := w.enc.Write(p)
	w.n += int64(n)
	if err != nil {
		w.err = err
		w.abandon()
	}
	return n, err
}

// abandon stops the encoder's work on the current stream and joins any
// goroutines a parallel encoder started, so that once it returns nothing
// more can reach the destination. It is not silent: a parallel encoder
// flushes jobs already dispatched to the destination it was Reset on,
// because the library's Reset drains them before swapping the writer. The
// frame is incomplete either way, and the caller discards it.
func (w *Writer) abandon() {
	w.enc.Reset(io.Discard)
}

// Close finishes the current stream's frame and flushes it to the
// destination. Fewer bytes than Reset declared is ErrParams. The Writer stays
// usable through Reset; Close without an active stream does nothing.
func (w *Writer) Close() error {
	if w.closed {
		return ErrClosed
	}
	if !w.active {
		return nil
	}
	w.active = false
	if w.err != nil {
		return w.err
	}
	if w.n != w.size {
		w.abandon()
		return fmt.Errorf("%w: %d of the declared %d bytes written", ErrParams, w.n, w.size)
	}
	if err := w.enc.Close(); err != nil {
		w.abandon()
		return err
	}
	return nil
}

// Release abandons any active stream, stops the encoder's goroutines, drops
// the encoder so its buffers can be collected, and makes the Writer
// unusable. Mandatory for a Writer with Concurrency above 1; harmless
// otherwise.
func (w *Writer) Release() {
	if w.closed {
		return
	}
	w.closed = true
	w.active = false
	w.abandon()
	w.enc = nil
}

// MaxEncodedSize is an upper bound on the output of a stream of n plaintext
// bytes under this Writer's Params, padding included. An archive layer that
// must reserve space before compressing can reserve this and give back the
// rest. Not valid after Release.
func (w *Writer) MaxEncodedSize(n int) int {
	if w.closed {
		panic("compress: MaxEncodedSize after Release")
	}
	m := w.enc.MaxEncodedSize(n)
	if w.pad > 1 {
		// The library's bound is exact only when its own estimate happens
		// to be a multiple of the padding. A real output shorter than the
		// estimate is padded to the first multiple at least 8 bytes above
		// it — a skippable frame is never smaller than its 8-byte header —
		// which for a padding under 8 is more than one step further. Round
		// the estimate plus 8 up to a multiple.
		m = (m + 8 + w.pad - 1) / w.pad * w.pad
	}
	return m
}

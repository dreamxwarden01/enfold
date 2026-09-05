package stream

import (
	"io"

	"github.com/dreamxwarden01/enfold/internal/format"
)

// Writer encrypts the plaintext written to it into one STREAM blob on dst.
// Close writes the final chunk; a Writer that is not closed has not produced a
// complete blob, and a reader will reject what it did produce.
//
// The DEK must be fresh: it must never have sealed a chunk before, and it must
// never be given to a second Writer (§12).
type Writer struct {
	c       *chunker
	dst     io.Writer
	buf     []byte // plaintext waiting to be sealed; cap ChunkSize
	out     []byte // sealed-chunk scratch; cap SealedChunkSize
	counter uint64 // chunks sealed so far
	total   uint64 // plaintext bytes accepted
	written uint64 // blob bytes handed to dst
	err     error  // sticky: after a failure nothing more is written
	closed  bool
}

// NewWriter returns a Writer sealing under dek for the file fileID of the
// archive archiveID. The two identifiers are bound into every chunk's AAD, so
// a blob only opens for the file it was written for.
func NewWriter(dst io.Writer, dek [KeySize]byte, archiveID, fileID [16]byte) (*Writer, error) {
	c, err := newChunker(dek, archiveID, fileID)
	if err != nil {
		return nil, err
	}
	return &Writer{
		c:   c,
		dst: dst,
		buf: make([]byte, 0, ChunkSize),
		out: make([]byte, 0, SealedChunkSize),
	}, nil
}

// Write buffers p, sealing a full chunk as non-final whenever more input is
// known to follow it. A chunk is never written until the Writer knows whether
// it is the last, so the output does not depend on how the plaintext was split
// across calls.
func (w *Writer) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if w.closed {
		return 0, ErrClosed
	}
	if uint64(len(p)) > format.MaxOrigSize-w.total {
		return 0, ErrTooLarge
	}
	n := 0
	for len(p) > 0 {
		if len(w.buf) == ChunkSize {
			if err := w.flush(false); err != nil {
				return n, err
			}
		}
		k := copy(w.buf[len(w.buf):ChunkSize], p)
		w.buf = w.buf[:len(w.buf)+k]
		p = p[k:]
		n += k
	}
	w.total += uint64(n)
	return n, nil
}

// flush seals the buffered plaintext as chunk w.counter and writes it.
func (w *Writer) flush(final bool) error {
	if w.counter >= MaxChunks {
		// Unreachable behind Write's size check; kept so that the counter can
		// never wrap into a reused nonce whatever happens above.
		w.err = ErrTooLarge
		return w.err
	}
	w.out = w.c.seal(w.out[:0], w.counter, final, w.buf)
	w.counter++
	clear(w.buf)
	w.buf = w.buf[:0]
	if _, err := w.dst.Write(w.out); err != nil {
		w.err = err
		return err
	}
	w.written += uint64(len(w.out))
	return nil
}

// Close seals whatever is buffered — possibly nothing — as the final chunk and
// zeroes the plaintext buffer. It does not close dst. A second Close is a
// no-op that returns the first one's error.
func (w *Writer) Close() error {
	if w.closed {
		return w.err
	}
	w.closed = true
	if w.err != nil {
		return w.err
	}
	err := w.flush(true)
	clear(w.buf[:cap(w.buf)])
	return err
}

// Written is the number of blob bytes handed to dst so far; after a successful
// Close it is the file's stored_size, equal to format.RawStoredSize of the
// plaintext length.
func (w *Writer) Written() uint64 { return w.written }

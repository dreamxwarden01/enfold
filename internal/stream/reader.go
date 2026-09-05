package stream

import (
	"errors"
	"fmt"
	"io"

	"github.com/dreamxwarden01/enfold/internal/format"
)

// Reader decrypts one STREAM blob of a known length. It implements io.Reader
// and io.Seeker over the plaintext, so it can be handed to http.ServeContent
// for range requests; a seek costs at most one re-decrypted chunk.
//
// Reader keeps one decrypted chunk and is not safe for concurrent use. That
// includes http.ServeContent's multi-range path, which reads from a goroutine
// of its own that can outlive the handler (net/http/fs.go, the io.Pipe
// branch): a handler must not Close a Reader it handed to ServeContent when it
// returns — open one per request and let it go, or refuse multi-range
// requests. Errors are not sticky: each Read decides afresh from the position,
// and a chunk that failed once fails again.
type Reader struct {
	c      *chunker
	src    io.ReaderAt
	stored uint64 // blob length
	size   uint64 // plaintext length
	chunks uint64 // ≥ 1
	pos    uint64 // plaintext position; may exceed size after a Seek
	cur    uint64 // chunk held in plain, when have
	have   bool
	plain  []byte // cap ChunkSize
	buf    []byte // sealed-chunk scratch; cap SealedChunkSize
	closed bool
}

// NewReader returns a Reader over the blob occupying the first stored bytes of
// src, which is usually an io.SectionReader onto the file's extent. stored is
// the index's stored_size and must be a canonical length (PlaintextLen); the
// archive layer has authenticated it before it gets here, and it is what tells
// the Reader which chunk must carry the final flag.
func NewReader(src io.ReaderAt, stored uint64, dek [KeySize]byte, archiveID, fileID [16]byte) (*Reader, error) {
	size, err := PlaintextLen(stored)
	if err != nil {
		return nil, err
	}
	c, err := newChunker(dek, archiveID, fileID)
	if err != nil {
		return nil, err
	}
	return &Reader{
		c:      c,
		src:    src,
		stored: stored,
		size:   size,
		chunks: chunkCount(stored),
		plain:  make([]byte, 0, ChunkSize),
		buf:    make([]byte, SealedChunkSize),
	}, nil
}

// Size is the plaintext length.
func (r *Reader) Size() int64 { return int64(r.size) }

// Read fills p from the current position, crossing chunk boundaries as needed,
// and returns io.EOF once the position reaches Size. io.EOF is only ever
// returned after the final chunk has been opened: an empty file is one empty
// final chunk whose tag still has to verify, and a Seek past the end must not
// turn into a way of reporting a clean end on a blob nobody authenticated.
func (r *Reader) Read(p []byte) (int, error) {
	if r.closed {
		return 0, ErrClosed
	}
	n := 0
	for len(p) > 0 && r.pos < r.size {
		if err := r.load(r.pos / ChunkSize); err != nil {
			return n, err
		}
		k := copy(p, r.plain[r.pos-r.cur*ChunkSize:])
		r.pos += uint64(k)
		n += k
		p = p[k:]
	}
	if n == 0 && len(p) > 0 {
		if err := r.load(r.chunks - 1); err != nil {
			return 0, err
		}
		return 0, io.EOF
	}
	return n, nil
}

// load makes chunk i the held chunk, reading and opening it unless it already
// is. A short read is format.ErrTruncated; an I/O error from src is returned
// as is.
func (r *Reader) load(i uint64) error {
	if r.have && r.cur == i {
		return nil
	}
	r.have = false
	final := i == r.chunks-1
	clen := uint64(SealedChunkSize)
	if final {
		clen = r.stored - (r.chunks-1)*SealedChunkSize
	}
	off := i * SealedChunkSize // < 2^49, so int64 is safe
	buf := r.buf[:clen]
	n, err := r.src.ReadAt(buf, int64(off))
	if n < 0 || uint64(n) < clen {
		if err == nil || errors.Is(err, io.EOF) {
			err = fmt.Errorf("%w: chunk %d at offset %d: %d of %d bytes", format.ErrTruncated, i, off, n, clen)
		}
		return err
	}
	out, err := r.c.open(r.plain[:0], i, final, buf)
	if err != nil {
		clear(r.plain[:cap(r.plain)])
		return err
	}
	r.plain, r.cur, r.have = out, i, true
	return nil
}

// Seek sets the plaintext position. Seeking past Size is allowed, as with a
// file, and Read then reports io.EOF.
func (r *Reader) Seek(offset int64, whence int) (int64, error) {
	var base int64
	switch whence {
	case io.SeekStart:
		base = 0
	case io.SeekCurrent:
		base = int64(r.pos)
	case io.SeekEnd:
		base = int64(r.size)
	default:
		return 0, fmt.Errorf("stream: invalid whence %d", whence)
	}
	pos := base + offset
	if (offset > 0 && pos < base) || pos < 0 {
		return 0, errors.New("stream: negative position")
	}
	r.pos = uint64(pos)
	return pos, nil
}

// Close zeroes the buffers that held plaintext. It does not close src.
func (r *Reader) Close() error {
	if !r.closed {
		r.closed = true
		r.have = false
		clear(r.plain[:cap(r.plain)])
		clear(r.buf[:cap(r.buf)])
	}
	return nil
}

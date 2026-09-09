package archive

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"sync/atomic"

	"github.com/dreamxwarden01/enfold/internal/compress"
	"github.com/dreamxwarden01/enfold/internal/format"
	"github.com/dreamxwarden01/enfold/internal/kdf"
	"github.com/dreamxwarden01/enfold/internal/stream"
)

// Reader reads one file's plaintext. It is an io.ReadSeeker: a raw file
// seeks at the cost of one chunk; a compressed file seeks by restarting the
// decompression and discarding up to the target, which is what
// http.ServeContent needs (it seeks to the end for the size, then back).
// Every Reader is independent — one per request — and holds its file's
// extent against reuse until Close. Not safe for concurrent use. A Reader
// is a snapshot of the file as it was when opened; Archive.Close ends it.
type Reader struct {
	a       *Archive
	info    FileInfo
	e       extent
	sr      *stream.Reader
	cr      *compress.Reader
	section *io.SectionReader
	pos     int64 // plaintext position, compressed files only
	closed  bool
	dead    atomic.Bool // set by Archive.Close from another goroutine
}

// OpenReader opens a live file for reading. Only a file has content, so a
// directory's id — like an unknown one — is ErrNotFound; what a caller does
// with a live directory is list its children and create it on extraction
// (APP.md §3), never read it.
func (a *Archive) OpenReader(id [16]byte) (*Reader, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.usable(); err != nil {
		return nil, err
	}
	r := a.record(id)
	if r == nil {
		return nil, ErrNotFound
	}
	dek, err := kdf.UnwrapKey(a.wrapKey, r.WrappedDEK, r.DEKNonce, format.DEKAAD(a.archiveID, r.FileID, r.DEKEpoch))
	if err != nil {
		return nil, corrupt("file %x: DEK does not unwrap", r.FileID)
	}
	defer kdf.Zero(dek[:])
	e := extent{Off: r.DataOff, Len: r.StoredSize}
	section := io.NewSectionReader(a.f, int64(e.Off), int64(e.Len))
	sr, err := stream.NewReader(section, e.Len, dek, a.archiveID, r.FileID)
	if err != nil {
		return nil, err
	}
	rd := &Reader{a: a, info: infoOf(r), e: e, sr: sr, section: section}
	if r.Storage != format.StorageRaw {
		var dict []byte
		if r.Storage == format.StorageZstdDict {
			dict = a.index.Dict
		}
		cr, err := compress.NewReader(dict)
		if err != nil {
			sr.Close()
			return nil, err
		}
		if err := cr.Reset(sr, r.OrigSize, r.Storage == format.StorageZstdDict); err != nil {
			cr.Close()
			sr.Close()
			return nil, err
		}
		rd.cr = cr
	}
	a.held[e]++
	a.readers[rd] = struct{}{}
	a.pool.removeOverlap(e)
	if a.tx != nil {
		a.tx.pool.removeOverlap(e)
	}
	return rd, nil
}

// Info describes the file as it was when the Reader opened.
func (r *Reader) Info() FileInfo { return r.info }

// Size is the plaintext length.
func (r *Reader) Size() int64 { return int64(r.info.Size) }

func (r *Reader) usable() error {
	if r.closed || r.dead.Load() {
		return ErrClosed
	}
	return nil
}

func (r *Reader) Read(p []byte) (int, error) {
	if err := r.usable(); err != nil {
		return 0, err
	}
	if r.cr == nil {
		return r.sr.Read(p)
	}
	n, err := r.cr.Read(p)
	r.pos += int64(n)
	return n, err
}

// Seek sets the plaintext position. For a compressed file a backward seek
// restarts the stream and a forward seek decompresses and discards, so the
// cost is proportional to the target offset.
func (r *Reader) Seek(offset int64, whence int) (int64, error) {
	if err := r.usable(); err != nil {
		return 0, err
	}
	if r.cr == nil {
		return r.sr.Seek(offset, whence)
	}
	var target int64
	switch whence {
	case io.SeekStart:
		target = offset
	case io.SeekCurrent:
		target = r.pos + offset
	case io.SeekEnd:
		target = r.Size() + offset
	default:
		return 0, fmt.Errorf("%w: whence %d", ErrParams, whence)
	}
	if target < 0 {
		return 0, fmt.Errorf("%w: negative position", ErrParams)
	}
	if target < r.pos {
		if _, err := r.sr.Seek(0, io.SeekStart); err != nil {
			return 0, err
		}
		if err := r.cr.Reset(r.sr, r.info.Size, r.info.Storage == format.StorageZstdDict); err != nil {
			return 0, err
		}
		r.pos = 0
	}
	if target > r.pos {
		skip := min(target, r.Size()) - r.pos
		if skip > 0 {
			n, err := io.CopyN(io.Discard, r.cr, skip)
			r.pos += n
			if err != nil && !errors.Is(err, io.EOF) {
				return 0, err
			}
		}
		// Positions past the end are allowed, as with a file; Read then
		// reports EOF.
		r.pos = target
	}
	return r.pos, nil
}

// kill is Archive.Close's side: the decoders are closed (which zeroes what
// they held) and the Reader refuses from then on. Caller holds a.mu; the
// Reader's own goroutine may be mid-Read, which then fails on the closed
// decoder or the closed file.
func (r *Reader) kill() {
	if r.dead.Swap(true) {
		return
	}
	if r.cr != nil {
		r.cr.Close()
	}
	r.sr.Close()
}

// Close releases the hold on the file's extent.
func (r *Reader) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	a := r.a
	a.mu.Lock()
	defer a.mu.Unlock()
	if !r.dead.Swap(true) {
		if r.cr != nil {
			r.cr.Close()
		}
		r.sr.Close()
	}
	if _, tracked := a.readers[r]; !tracked {
		return nil // Archive.Close already released everything
	}
	delete(a.readers, r)
	if a.held[r.e]--; a.held[r.e] <= 0 {
		delete(a.held, r.e)
		// Back into the pool only if it is free now and not quarantined.
		if a.free.contains(r.e) && !a.retired.intersects(r.e) && (a.tx == nil || !a.tx.pending.intersects(r.e)) {
			if !a.pool.intersects(r.e) {
				a.pool.insert(r.e)
			}
		}
	}
	return nil
}

// Extract streams the file's plaintext into w, verifying the content hash
// at the end. Any error means the bytes already written to w are not the
// file and must be discarded by the caller (trap 17); ExtractTo does that
// discipline for a destination path.
func (a *Archive) Extract(ctx context.Context, id [16]byte, w io.Writer) error {
	r, err := a.OpenReader(id)
	if err != nil {
		return err
	}
	defer r.Close()
	h := sha256.New()
	buf := make([]byte, 256<<10)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, rerr := r.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
			if _, err := w.Write(buf[:n]); err != nil {
				return err
			}
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				break
			}
			return rerr
		}
	}
	if [32]byte(h.Sum(nil)) != r.info.ContentHash {
		return ErrContentHash
	}
	return nil
}

// ExtractTo writes the file to path: into a temporary beside it with
// restrictive permissions, synced, and moved into place only after a clean
// end and a matching content hash. path must not exist; the final move is
// exclusive, so a file that appears meanwhile is not replaced (os.ErrExist).
func (a *Archive) ExtractTo(ctx context.Context, id [16]byte, path string) error {
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("%w: %s", os.ErrExist, path)
	}
	tmp, err := tempName(path, "part")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			f.Close()
			os.Remove(tmp)
		}
	}()
	if err := a.Extract(ctx, id, f); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := placeExclusive(tmp, path); err != nil {
		return err
	}
	ok = true
	return nil
}

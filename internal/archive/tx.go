package archive

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"github.com/dreamxwarden01/enfold/internal/compress"
	"github.com/dreamxwarden01/enfold/internal/format"
	"github.com/dreamxwarden01/enfold/internal/kdf"
	"github.com/dreamxwarden01/enfold/internal/stream"
)

// Tx is one transaction: changes recorded against a working copy of the
// index, data written into extents no superblock references, and one
// superblock flip at Commit. Abort discards everything. A Tx is not safe
// for concurrent use; the Archive allows one at a time. Readers stay
// usable throughout: the Archive's lock is held around a transaction's
// bookkeeping, never while a file is compressed and sealed.
type Tx struct {
	a       *Archive
	index   *format.Index // the working copy; touched only by the Tx's goroutine
	pool    *space        // what this transaction may still allocate (a.mu)
	allocs  *space        // what it has allocated, to leave out of the published map (a.mu)
	pending *space        // what it has freed, to publish as free and quarantine (a.mu)
	size0   uint64        // the file size at Begin, to truncate back to on Abort
	changed bool
	done    bool
}

// Begin opens a transaction. Only one may be open.
func (a *Archive) Begin() (*Tx, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.writable(); err != nil {
		return nil, err
	}
	if a.tx != nil {
		return nil, ErrTxOpen
	}
	index, err := cloneIndex(a.index)
	if err != nil {
		return nil, err
	}
	tx := &Tx{a: a, index: index, pool: a.pool.clone(), allocs: newSpace(nil), pending: newSpace(nil), size0: a.size}
	a.tx = tx
	return tx, nil
}

func cloneIndex(x *format.Index) (*format.Index, error) {
	b, err := x.Encode()
	if err != nil {
		return nil, err
	}
	defer kdf.Zero(b)
	return format.DecodeIndex(b)
}

// live refuses a finished transaction or an unusable Archive. Caller holds
// a.mu.
func (tx *Tx) live() error {
	if tx.done {
		return ErrClosed
	}
	return tx.a.usable()
}

// alloc takes n bytes from the pool, or appends them at the end of the file.
// atEnd forces the append, for reservations that will be shrunk. Caller
// holds a.mu.
func (tx *Tx) alloc(n uint64, atEnd bool) (extent, error) {
	a := tx.a
	if n == 0 {
		return extent{}, fmt.Errorf("%w: empty allocation", ErrInternal)
	}
	if !atEnd {
		if e, ok := tx.pool.exactOrFirstFit(n); ok {
			tx.allocs.insert(e)
			return e, tx.assertWritable(e)
		}
	}
	if a.size+n < a.size || a.size+n > 1<<62 {
		return extent{}, ErrNoSpace
	}
	e := extent{Off: a.size, Len: n}
	a.size += n
	tx.allocs.insert(e)
	return e, tx.assertWritable(e)
}

// release gives an allocation back: truncated away when it is the tail of
// the file, into the pool otherwise. Caller holds a.mu.
func (tx *Tx) release(e extent) {
	a := tx.a
	tx.allocs.remove(e)
	if end(e) == a.size {
		if err := a.f.Truncate(int64(e.Off)); err == nil {
			a.size = e.Off
			return
		}
	}
	if !tx.pool.intersects(e) {
		tx.pool.insert(e)
	}
}

// assertWritable is §13's writer-side check: the extent about to be written
// overlaps nothing the live superblock references, nothing the losing copy
// references, nothing a reader holds, and nothing this transaction freed.
// Caller holds a.mu.
func (tx *Tx) assertWritable(e extent) error {
	a := tx.a
	bad := func(what string) error {
		return fmt.Errorf("%w: allocation [0x%x, 0x%x) overlaps %s", ErrInternal, e.Off, end(e), what)
	}
	if overlaps(e, extent{Off: a.sb.IndexOff, Len: a.sb.IndexLen + format.TagSize}) {
		return bad("the live index")
	}
	if overlaps(e, extent{Off: a.sb.FreeMapOff, Len: a.sb.FreeMapLen}) {
		return bad("the live free map")
	}
	for i := range a.index.Files {
		if r := &a.index.Files[i]; r.State == format.FileLive && overlaps(e, extent{Off: r.DataOff, Len: r.StoredSize}) {
			return bad(fmt.Sprintf("file %x", r.FileID))
		}
	}
	if a.retired.intersects(e) {
		return bad("an extent the previous commit freed")
	}
	if tx.pending.intersects(e) {
		return bad("an extent this transaction freed")
	}
	for h := range a.held {
		if overlaps(e, h) {
			return bad("an extent a reader holds")
		}
	}
	return nil
}

// shrink returns the unused tail of a reservation appended at the end of
// the file, truncating the file. Caller holds a.mu.
func (tx *Tx) shrink(e extent, used uint64) error {
	a := tx.a
	if used > e.Len {
		return fmt.Errorf("%w: %d bytes written into a %d-byte reservation", ErrInternal, used, e.Len)
	}
	if end(e) != a.size {
		return fmt.Errorf("%w: reservation [0x%x, 0x%x) is not the tail of the file", ErrInternal, e.Off, end(e))
	}
	if used == e.Len {
		return nil
	}
	if err := a.f.Truncate(int64(e.Off + used)); err != nil {
		return err
	}
	tx.allocs.remove(extent{Off: e.Off + used, Len: e.Len - used})
	a.size = e.Off + used
	return nil
}

// liveName reports whether a live record in the working index has the name.
func (tx *Tx) liveName(name string, except [16]byte) bool {
	for i := range tx.index.Files {
		r := &tx.index.Files[i]
		if r.State == format.FileLive && r.Name == name && r.FileID != except {
			return true
		}
	}
	return false
}

// plan decides how a file of size bytes is stored (DESIGN.md §9, R27),
// from the transaction's own view of the dictionary.
func (tx *Tx) plan(src io.ReaderAt, size int64) (format.Storage, error) {
	a := tx.a
	if size == 0 || a.opts.NoCompression {
		return format.StorageRaw, nil
	}
	// A small file with a dictionary present is the case the dictionary
	// exists for, and the probe — which has no dictionary — would call it
	// incompressible for the frame overhead alone.
	if len(tx.index.Dict) > 0 && size <= a.opts.DictBelow {
		return format.StorageZstdDict, nil
	}
	d, err := compress.Probe(src, size)
	if err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return 0, fmt.Errorf("%w: shorter than %d bytes", ErrSourceChanged, size)
		}
		return 0, err
	}
	if !d.Compressible {
		return format.StorageRaw, nil
	}
	return format.StorageZstd, nil
}

// Add stores a new file. src must hold exactly size bytes; a source that
// yields more or fewer is ErrSourceChanged. The write is complete when Add
// returns; the record is published by Commit.
func (tx *Tx) Add(ctx context.Context, name string, src io.ReaderAt, size int64) (FileInfo, error) {
	a := tx.a
	a.mu.Lock()
	err := tx.live()
	a.mu.Unlock()
	if err != nil {
		return FileInfo{}, err
	}
	if err := format.ValidateFileName(name); err != nil {
		return FileInfo{}, err
	}
	if tx.liveName(name, [16]byte{}) {
		return FileInfo{}, fmt.Errorf("%w: %q", ErrExists, name)
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return FileInfo{}, err
	}
	rec := format.FileRecord{FileID: id, State: format.FileLive, Name: name, Revision: 1, DEKEpoch: 1}
	if err := tx.store(ctx, &rec, src, size); err != nil {
		return FileInfo{}, err
	}
	tx.index.Files = append(tx.index.Files, rec)
	tx.changed = true
	return infoOf(&rec), nil
}

// Replace rewrites a live file's content under a fresh DEK (§12), keeping
// its identity and name. The old extent is freed at Commit.
func (tx *Tx) Replace(ctx context.Context, id [16]byte, src io.ReaderAt, size int64) (FileInfo, error) {
	a := tx.a
	a.mu.Lock()
	err := tx.live()
	a.mu.Unlock()
	if err != nil {
		return FileInfo{}, err
	}
	old := findRecord(tx.index, id)
	if old == nil {
		return FileInfo{}, ErrNotFound
	}
	rec := *old
	rec.DEKEpoch++
	rec.Revision++
	if err := tx.store(ctx, &rec, src, size); err != nil {
		return FileInfo{}, err
	}
	a.mu.Lock()
	tx.pending.insert(extent{Off: old.DataOff, Len: old.StoredSize})
	a.mu.Unlock()
	*old = rec
	tx.changed = true
	return infoOf(old), nil
}

// store writes src into a fresh extent under a fresh DEK and fills rec's
// content fields; rec's identity, epoch and revision are the caller's. The
// lock is held for allocation and bookkeeping, not for the sealing.
func (tx *Tx) store(ctx context.Context, rec *format.FileRecord, src io.ReaderAt, size int64) (err error) {
	a := tx.a
	if size < 0 || uint64(size) > format.MaxOrigSize-(format.MaxOrigSize>>8) {
		return fmt.Errorf("%w: size %d", ErrNoSpace, size)
	}
	storage, err := tx.plan(src, size)
	if err != nil {
		return err
	}
	var dek [32]byte
	if _, err := rand.Read(dek[:]); err != nil {
		return err
	}
	defer kdf.Zero(dek[:])

	var cw *compress.Writer
	if storage != format.StorageRaw {
		var dict []byte
		if storage == format.StorageZstdDict {
			if dict = tx.index.Dict; len(dict) == 0 {
				return fmt.Errorf("%w: dictionary storage without a dictionary", ErrInternal)
			}
		}
		a.mu.Lock()
		cw, err = a.writer(dict)
		a.mu.Unlock()
		if err != nil {
			return err
		}
	}

	var (
		e      extent
		stored uint64
		hash   [32]byte
	)
	// A reservation taken and then not filled is given back on every
	// failure, so a failed store leaves neither a hole nor a tail.
	reserved := false
	defer func() {
		if err != nil && reserved {
			a.mu.Lock()
			tx.release(e)
			a.mu.Unlock()
		}
	}()
	switch {
	case storage == format.StorageRaw:
		// Exact size: first-fit.
		n := format.RawStoredSize(uint64(size))
		a.mu.Lock()
		e, err = tx.alloc(n, false)
		a.mu.Unlock()
		if err != nil {
			return err
		}
		reserved = true
		w := &boundedWriter{f: a.f, off: e.Off, limit: n}
		if stored, hash, err = seal(ctx, w, src, size, nil, dek, a.archiveID, rec.FileID); err != nil {
			return err
		}
		if stored != n {
			err = fmt.Errorf("%w: raw blob of %d bytes for %d plaintext bytes", ErrInternal, stored, n)
			return err
		}
	case size <= a.opts.InMemoryBelow:
		// Small: compress into memory, then place the exact size.
		var buf bytes.Buffer
		if stored, hash, err = seal(ctx, &buf, src, size, cw, dek, a.archiveID, rec.FileID); err != nil {
			return err
		}
		a.mu.Lock()
		e, err = tx.alloc(stored, false)
		a.mu.Unlock()
		if err != nil {
			return err
		}
		reserved = true
		if _, err = a.f.WriteAt(buf.Bytes(), int64(e.Off)); err != nil {
			return err
		}
	default:
		// Large: a reservation at the end of the file, shrunk afterwards.
		bound := format.RawStoredSize(uint64(cw.MaxEncodedSize(int(size))))
		if bound == 0 {
			return fmt.Errorf("%w: size %d", ErrNoSpace, size)
		}
		a.mu.Lock()
		e, err = tx.alloc(bound, true)
		a.mu.Unlock()
		if err != nil {
			return err
		}
		reserved = true
		w := &boundedWriter{f: a.f, off: e.Off, limit: bound}
		if stored, hash, err = seal(ctx, w, src, size, cw, dek, a.archiveID, rec.FileID); err != nil {
			return err
		}
		a.mu.Lock()
		err = tx.shrink(e, stored)
		a.mu.Unlock()
		if err != nil {
			return err
		}
		e.Len = stored
	}

	rec.OrigSize, rec.StoredSize, rec.Storage = uint64(size), stored, storage
	rec.ContentHash, rec.DataOff = hash, e.Off
	rec.ChunkSize, rec.Alg = format.ChunkSize, format.AlgAES256GCM
	rec.WrappedDEK, rec.DEKNonce, err = kdf.WrapKey(a.wrapKey, dek, format.DEKAAD(a.archiveID, rec.FileID, rec.DEKEpoch))
	if err != nil {
		return err
	}
	rec.DEKCreatedAt, rec.ModifiedAt, rec.LastWriter = now(), now(), a.opts.DeviceID
	return nil
}

// seal runs the write pipeline: src, hashed, [compressed,] STREAM-sealed
// into dst. It reads exactly size bytes from src and refuses a source that
// has more or fewer. On any failure the compressor's stream is abandoned
// here, while dst is still the extent this transaction is about to give
// back: a parallel encoder flushes what it had dispatched to the writer it
// was reset on, and that must never be a later commit's extent.
func seal(ctx context.Context, dst io.Writer, src io.ReaderAt, size int64, cw *compress.Writer, dek [32]byte, archiveID, fileID [16]byte) (stored uint64, hash [32]byte, err error) {
	sw, err := stream.NewWriter(dst, dek, archiveID, fileID)
	if err != nil {
		return 0, hash, err
	}
	var w io.Writer = sw
	if cw != nil {
		if err := cw.Reset(sw, size); err != nil {
			return 0, hash, err
		}
		w = cw
		defer func() {
			if err != nil {
				cw.Close() // abandons the unfinished stream; its error is not ours
			}
		}()
	}
	h := sha256.New()
	r := io.NewSectionReader(src, 0, size)
	buf := make([]byte, 256<<10)
	var got int64
	for got < size {
		if err := ctx.Err(); err != nil {
			return 0, hash, err
		}
		n, rerr := r.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
			if _, err := w.Write(buf[:n]); err != nil {
				return 0, hash, err
			}
			got += int64(n)
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				break
			}
			return 0, hash, rerr
		}
	}
	if got != size {
		return 0, hash, fmt.Errorf("%w: %d of %d bytes", ErrSourceChanged, got, size)
	}
	// The source must end where it said it would.
	if n, rerr := src.ReadAt(buf[:1], size); n > 0 || (rerr != nil && !errors.Is(rerr, io.EOF)) {
		if n > 0 {
			return 0, hash, fmt.Errorf("%w: more than %d bytes", ErrSourceChanged, size)
		}
		return 0, hash, rerr
	}
	if cw != nil {
		if err := cw.Close(); err != nil {
			return 0, hash, err
		}
	}
	if err := sw.Close(); err != nil {
		return 0, hash, err
	}
	return sw.Written(), [32]byte(h.Sum(nil)), nil
}

// boundedWriter writes sequentially into one extent and refuses to exceed
// it: the last thing between a bound that lied and a neighbouring extent.
type boundedWriter struct {
	f     io.WriterAt
	off   uint64
	limit uint64
	n     uint64
}

func (b *boundedWriter) Write(p []byte) (int, error) {
	if uint64(len(p)) > b.limit-b.n {
		return 0, fmt.Errorf("%w: write of %d bytes exceeds the %d-byte reservation", ErrInternal, len(p), b.limit)
	}
	n, err := b.f.WriteAt(p, int64(b.off+b.n))
	b.n += uint64(n)
	return n, err
}

// Delete turns a live record into a tombstone (R32): identity, name and
// the merge fields stay, with revision advanced; content fields are zeroed
// and the dictionary reference dropped; dek_epoch stays monotone. The
// extent is freed at Commit. Deletion is cryptographic erasure — the
// ciphertext stays in the freed extent until it is reused or compacted
// away.
func (tx *Tx) Delete(id [16]byte) error {
	a := tx.a
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := tx.live(); err != nil {
		return err
	}
	r := findRecord(tx.index, id)
	if r == nil {
		return ErrNotFound
	}
	tx.pending.insert(extent{Off: r.DataOff, Len: r.StoredSize})
	r.State = format.FileTombstone
	r.OrigSize, r.StoredSize, r.DataOff = 0, 0, 0
	r.Storage = format.StorageRaw
	r.ContentHash = [32]byte{}
	r.WrappedDEK, r.DEKNonce = [format.WrappedKeySize]byte{}, [format.NonceSize]byte{}
	r.DEKCreatedAt = 0
	r.Revision++
	r.LastWriter, r.ModifiedAt = a.opts.DeviceID, now()
	tx.changed = true
	return nil
}

// Rename changes a live file's name.
func (tx *Tx) Rename(id [16]byte, name string) error {
	a := tx.a
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := tx.live(); err != nil {
		return err
	}
	if err := format.ValidateFileName(name); err != nil {
		return err
	}
	r := findRecord(tx.index, id)
	if r == nil {
		return ErrNotFound
	}
	if r.Name == name {
		return nil
	}
	if tx.liveName(name, id) {
		return fmt.Errorf("%w: %q", ErrExists, name)
	}
	r.Name = name
	r.Revision++
	r.LastWriter, r.ModifiedAt = a.opts.DeviceID, now()
	tx.changed = true
	return nil
}

// SetDictionary installs dict as the index dictionary, or clears it with
// nil, for this transaction's later adds and for the archive once
// committed. Refused while any record — tombstones included — references
// the current one (ErrDictInUse): a frame names its dictionary by ID.
func (tx *Tx) SetDictionary(dict []byte) error {
	a := tx.a
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := tx.live(); err != nil {
		return err
	}
	for i := range tx.index.Files {
		if tx.index.Files[i].Storage == format.StorageZstdDict {
			return ErrDictInUse
		}
	}
	if dict != nil {
		if _, err := compress.DictID(dict); err != nil {
			return err
		}
	}
	tx.index.Dict = bytes.Clone(dict)
	tx.changed = true
	return nil
}

// Abort discards the transaction: data it appended is truncated away, its
// allocations return to the pool, nothing was published. Safe to call
// after Commit, when it does nothing.
func (tx *Tx) Abort() {
	a := tx.a
	a.mu.Lock()
	defer a.mu.Unlock()
	tx.abortLocked()
}

// abortLocked is Abort with a.mu held.
func (tx *Tx) abortLocked() {
	a := tx.a
	if tx.done {
		return
	}
	tx.done = true
	if a.tx == tx {
		a.tx = nil
	}
	if a.closed || a.broken != nil {
		return
	}
	if a.size > tx.size0 {
		if err := a.f.Truncate(int64(tx.size0)); err == nil {
			a.size = tx.size0
		}
	}
}

// Commit publishes the transaction in one superblock flip and returns what
// the registry needs to know. A transaction that changed nothing commits
// nothing. After a failure at or after the commit point the Archive is
// Broken; before it, the transaction is aborted and the Archive usable.
func (tx *Tx) Commit(ctx context.Context) (Receipt, error) {
	a := tx.a
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := tx.live(); err != nil {
		return Receipt{}, err
	}
	if !tx.changed {
		tx.abortLocked()
		return Receipt{Seq: a.sb.Seq, Size: a.size, WrittenAt: now()}, nil
	}
	rec, err := a.commit(ctx, tx, tx.index, a.kid, a.indexKey)
	if err != nil {
		if a.broken == nil {
			tx.abortLocked()
		}
		return Receipt{}, err
	}
	return rec, nil
}

// commit writes index under (kid, indexKey) with tx's allocations and
// frees, then flips. Caller holds a.mu. On success the transaction is
// finished and the Archive's state is the new one.
func (a *Archive) commit(ctx context.Context, tx *Tx, index *format.Index, kid [16]byte, indexKey []byte) (Receipt, error) {
	if err := ctx.Err(); err != nil {
		return Receipt{}, err
	}
	if err := index.Validate(); err != nil {
		return Receipt{}, err
	}
	plain, err := index.Encode()
	if err != nil {
		return Receipt{}, err
	}
	defer kdf.Zero(plain)
	if uint64(len(plain)) > format.MaxIndexLen {
		return Receipt{}, fmt.Errorf("%w: index of %d bytes exceeds %d", ErrNoSpace, len(plain), format.MaxIndexLen)
	}

	// The index extent, first-fit or appended; then the sealed index.
	next := *a.sb
	next.Seq++
	ie, err := tx.alloc(uint64(len(plain))+format.TagSize, false)
	if err != nil {
		return Receipt{}, err
	}
	next.IndexOff, next.IndexLen = ie.Off, uint64(len(plain))
	if _, err := rand.Read(next.IndexNonce[:]); err != nil {
		return Receipt{}, err
	}
	sealed, tag, err := sealIndex(indexKey, plain, &next, a.archiveID, kid)
	if err != nil {
		return Receipt{}, err
	}
	next.IndexTag = tag
	if _, err := a.f.WriteAt(sealed, int64(ie.Off)); err != nil {
		return Receipt{}, err
	}

	// The published map: everything the new state does not use — the old
	// map minus this transaction's allocations, plus what it freed and the
	// two metadata extents the flip releases. Appended at the end, through
	// the same allocation gate as everything else.
	oldIndex := extent{Off: a.sb.IndexOff, Len: a.sb.IndexLen + format.TagSize}
	oldMap := extent{Off: a.sb.FreeMapOff, Len: a.sb.FreeMapLen}
	free := a.free.clone()
	free.subtract(tx.allocs)
	free = free.merged(tx.pending).merged(newSpace([]extent{oldIndex, oldMap}))
	if n := 4 + 16*uint64(len(free.x)); n > format.MaxFreeMapLen {
		return Receipt{}, fmt.Errorf("%w: free map of %d bytes exceeds %d", ErrNoSpace, n, format.MaxFreeMapLen)
	}
	encMap, hash, err := free.freeMap().Hash()
	if err != nil {
		return Receipt{}, err
	}
	me, err := tx.alloc(uint64(len(encMap)), true)
	if err != nil {
		return Receipt{}, err
	}
	if _, err := a.f.WriteAt(encMap, int64(me.Off)); err != nil {
		return Receipt{}, err
	}
	next.FreeMapOff, next.FreeMapLen, next.FreeMapHash = me.Off, me.Len, hash

	if err := a.f.Sync(); err != nil {
		return Receipt{}, err
	}
	encSB, err := next.Encode()
	if err != nil {
		return Receipt{}, err
	}
	// The commit point. A failure from here on leaves the outcome unknown.
	target := a.live.Other()
	if _, err := a.f.WriteAt(encSB, int64(target.ArchiveSuperblockOff())); err != nil {
		a.broken = fmt.Errorf("%w: superblock write failed: %v", ErrIndeterminate, err)
		return Receipt{}, a.broken
	}
	if err := a.f.Sync(); err != nil {
		a.broken = fmt.Errorf("%w: the change may already be durable: %v", ErrIndeterminate, err)
		return Receipt{}, a.broken
	}

	// Committed: install the new state and rotate the quarantine. A
	// dictionary writer built for the previous dictionary goes.
	a.loser = append([]extent{oldIndex, oldMap}, tx.pending.extents()...)
	if a.wDict != nil && !bytes.Equal(a.wDictFor, index.Dict) {
		a.wDict.Release()
		a.wDict, a.wDictFor = nil, nil
	}
	a.sb, a.live, a.index, a.free = &next, target, index, free
	a.retired = newSpace(a.loser)
	a.rebuildPool()
	a.freeMapRebuilt = nil
	tx.done = true
	a.tx = nil
	return Receipt{Seq: next.Seq, Size: a.size, WrittenAt: now()}, nil
}

// One-transaction conveniences.

func (a *Archive) oneTx(ctx context.Context, fn func(*Tx) error) (Receipt, error) {
	tx, err := a.Begin()
	if err != nil {
		return Receipt{}, err
	}
	if err := fn(tx); err != nil {
		tx.Abort()
		return Receipt{}, err
	}
	return tx.Commit(ctx)
}

// Add stores one file in its own transaction.
func (a *Archive) Add(ctx context.Context, name string, src io.ReaderAt, size int64) (FileInfo, Receipt, error) {
	var info FileInfo
	rec, err := a.oneTx(ctx, func(tx *Tx) (err error) {
		info, err = tx.Add(ctx, name, src, size)
		return err
	})
	return info, rec, err
}

// Replace rewrites one file in its own transaction.
func (a *Archive) Replace(ctx context.Context, id [16]byte, src io.ReaderAt, size int64) (FileInfo, Receipt, error) {
	var info FileInfo
	rec, err := a.oneTx(ctx, func(tx *Tx) (err error) {
		info, err = tx.Replace(ctx, id, src, size)
		return err
	})
	return info, rec, err
}

// Delete removes one file in its own transaction.
func (a *Archive) Delete(ctx context.Context, id [16]byte) (Receipt, error) {
	return a.oneTx(ctx, func(tx *Tx) error { return tx.Delete(id) })
}

// Rename renames one file in its own transaction.
func (a *Archive) Rename(ctx context.Context, id [16]byte, name string) (Receipt, error) {
	return a.oneTx(ctx, func(tx *Tx) error { return tx.Rename(id, name) })
}

// SetDictionary installs or clears the dictionary in its own transaction.
func (a *Archive) SetDictionary(ctx context.Context, dict []byte) (Receipt, error) {
	return a.oneTx(ctx, func(tx *Tx) error { return tx.SetDictionary(dict) })
}

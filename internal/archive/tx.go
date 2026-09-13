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
	tree    *tree         // the working copy's id space (R39)
	pool    *space        // what this transaction may still allocate (a.mu)
	allocs  *space        // what it has allocated, to leave out of the published map (a.mu)
	pending *space        // what it has freed, to publish as free and quarantine (a.mu)
	size0   uint64        // the file size at Begin, to truncate back to on Abort
	changed bool
	done    bool
}

// Begin opens a transaction. Only one may be open. An archive whose
// sequence is exhausted opens none: a transaction that could not commit
// would still have written content into free extents (§4, committable).
func (a *Archive) Begin() (*Tx, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.committable(); err != nil {
		return nil, err
	}
	if a.tx != nil {
		return nil, ErrTxOpen
	}
	index, err := cloneIndex(a.index)
	if err != nil {
		return nil, err
	}
	tx := &Tx{a: a, index: index, tree: newTree(index), pool: a.pool.clone(), allocs: newSpace(nil), pending: newSpace(nil), size0: a.size}
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

// take claims exactly e from the pool: R40's placement, which alloc cannot
// make — its first fit may lie above the source and its fallback appends,
// and a move that does not lower the data is not a move. e must lie within
// one free extent of what this transaction may allocate from; what does not
// — live data, an extent under quarantine, one a reader holds, or one another
// move has taken — is ErrStalePlan, since a plan is made against a state and
// this is not it. The file never grows here. Caller holds a.mu.
func (tx *Tx) take(e extent) error {
	if e.Len == 0 {
		return fmt.Errorf("%w: empty allocation", ErrInternal)
	}
	if !tx.pool.contains(e) {
		return fmt.Errorf("%w: [0x%x, 0x%x) is not free space this transaction may allocate", ErrStalePlan, e.Off, end(e))
	}
	tx.pool.remove(e)
	tx.allocs.insert(e)
	return tx.assertWritable(e)
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

// place is the check every staged creation, rename and move passes: the name
// is one valid path element (R20), the parent is the root or a live directory
// of the working index, no live child of that parent already folds onto the
// name, and the record — with every live record beneath it — still fits R39's
// depth and path bounds. Everything is asked of the index this transaction is
// building, never of the one last committed, so a directory staged a moment
// ago is as real a parent as any; and a change that would break any of it is
// refused before the working index is touched.
func (tx *Tx) place(kids map[[16]byte][]ref, r ref, parent [16]byte, name string) error {
	if err := format.ValidateName(name); err != nil {
		return err
	}
	if !tx.tree.parentUsable(parent) {
		return fmt.Errorf("%w: %x is not the root or a live directory", ErrNotFound, parent)
	}
	if tx.tree.liveName(parent, name, r.id) {
		return fmt.Errorf("%w: %q", ErrExists, name)
	}
	return tx.tree.checkBounds(kids, r, parent, name)
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

// Add stores a new file under parentID — format.RootID for the top level —
// with name as one path element (R20); nothing is looked up by a path. src
// must hold exactly size bytes; a source that yields more or fewer is
// ErrSourceChanged. The write is complete when Add returns; the record is
// published by Commit.
func (tx *Tx) Add(ctx context.Context, parentID [16]byte, name string, src io.ReaderAt, size int64) (FileInfo, error) {
	a := tx.a
	a.mu.Lock()
	err := tx.live()
	a.mu.Unlock()
	if err != nil {
		return FileInfo{}, err
	}
	id, err := tx.tree.mintID()
	if err != nil {
		return FileInfo{}, err
	}
	if err := tx.place(nil, ref{id, false}, parentID, name); err != nil {
		return FileInfo{}, err
	}
	rec := format.FileRecord{FileID: id, State: format.FileLive, ParentID: parentID, Name: name, Revision: 1, DEKEpoch: 1}
	if err := tx.store(ctx, &rec, src, size); err != nil {
		return FileInfo{}, err
	}
	tx.tree.appendFile(rec)
	tx.changed = true
	return infoOf(&rec), nil
}

// AddDir stages a directory record under parentID with the folder's own time
// (R32: nothing writes modified_at again). A folder is a record, so an empty
// one is a real thing that survives a commit — never a fiction of the page
// (DESIGN.md trap 31). Its id is minted fresh (R39).
func (tx *Tx) AddDir(parentID [16]byte, name string, modifiedAt int64) (DirInfo, error) {
	a := tx.a
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := tx.live(); err != nil {
		return DirInfo{}, err
	}
	id, err := tx.tree.mintID()
	if err != nil {
		return DirInfo{}, err
	}
	if err := tx.place(nil, ref{id, true}, parentID, name); err != nil {
		return DirInfo{}, err
	}
	rec := format.DirRecord{
		DirID: id, State: format.FileLive, ParentID: parentID, Name: name,
		ModifiedAt: modifiedAt, Revision: 1, LastWriter: a.opts.DeviceID,
	}
	tx.tree.appendDir(rec)
	tx.changed = true
	return dirInfoOf(&rec), nil
}

// Replace rewrites a live file's content under a fresh DEK (§12), keeping
// its identity, its name and its parent — it is the in-place edit, never a
// delete and an add, and it cannot collide (R39). The old extent is freed at
// Commit. A directory's id is ErrKindMismatch: replacing a folder with a file
// is not an edit of that folder.
func (tx *Tx) Replace(ctx context.Context, id [16]byte, src io.ReaderAt, size int64) (FileInfo, error) {
	a := tx.a
	a.mu.Lock()
	err := tx.live()
	a.mu.Unlock()
	if err != nil {
		return FileInfo{}, err
	}
	old := tx.tree.liveFile(id)
	if old == nil {
		if tx.tree.liveDir(id) != nil {
			return FileInfo{}, fmt.Errorf("%w: %x is a directory", ErrKindMismatch, id)
		}
		return FileInfo{}, ErrNotFound
	}
	freed := extent{Off: old.DataOff, Len: old.StoredSize}
	rec := *old
	rec.DEKEpoch++
	rec.Revision++
	if err := tx.store(ctx, &rec, src, size); err != nil {
		return FileInfo{}, err
	}
	a.mu.Lock()
	tx.pending.insert(freed)
	a.mu.Unlock()
	// The record is taken again rather than held across the write: it is a
	// position in a slice the transaction may have grown meanwhile.
	*tx.tree.fileRec(id) = rec
	tx.changed = true
	return infoOf(&rec), nil
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

// Delete turns a live record into a tombstone. A file (R32): identity, name,
// parent and the merge fields stay, with revision and modified_at advanced;
// content fields are zeroed and the dictionary reference dropped; dek_epoch
// stays monotone. A directory: the same write tombstones it and every live
// record beneath it (R39), the subtree taken from this transaction's own
// index rather than from the caller, so a live record never stands under a
// tombstone; the folder's record keeps dir_id, parent_id and name, advances
// revision and last_writer only, and its modified_at is left exactly as it
// was, because a folder's time is the folder's own and not a clock (R32).
// Every extent the write frees is released at Commit. Deletion is
// cryptographic erasure — the ciphertext stays in the freed extent until it
// is reused or compacted away.
func (tx *Tx) Delete(id [16]byte) error {
	a := tx.a
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := tx.live(); err != nil {
		return err
	}
	if id == format.RootID {
		return fmt.Errorf("%w: the root is not a record", ErrParams)
	}
	self, _, _, ok := tx.tree.live(id)
	if !ok {
		return ErrNotFound
	}
	doomed := []ref{self}
	if self.isDir {
		below, err := tx.tree.subtree(tx.tree.childRefs(), id)
		if err != nil {
			return err
		}
		doomed = append(doomed, below...)
	}
	for _, r := range doomed {
		if r.isDir {
			d := tx.tree.dirRec(r.id)
			d.State = format.FileTombstone
			d.Revision++
			d.LastWriter = a.opts.DeviceID
			continue
		}
		f := tx.tree.fileRec(r.id)
		tx.pending.insert(extent{Off: f.DataOff, Len: f.StoredSize})
		f.State = format.FileTombstone
		f.OrigSize, f.StoredSize, f.DataOff = 0, 0, 0
		f.Storage = format.StorageRaw
		f.ContentHash = [32]byte{}
		f.WrappedDEK, f.DEKNonce = [format.WrappedKeySize]byte{}, [format.NonceSize]byte{}
		f.DEKCreatedAt = 0
		f.Revision++
		f.LastWriter, f.ModifiedAt = a.opts.DeviceID, now()
	}
	tx.changed = true
	return nil
}

// Rename changes one live record's name, file or directory alike. A name that
// folds onto a live sibling of the record's own parent is ErrExists; a change
// of case alone is not one, since a record is not its own sibling (R39).
// Renaming a directory lengthens every descendant's path, so the whole
// subtree is re-checked against the path bound before anything is touched. It
// writes name, revision and last_writer, and never modified_at (APP.md §3).
func (tx *Tx) Rename(id [16]byte, name string) error {
	a := tx.a
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := tx.live(); err != nil {
		return err
	}
	if id == format.RootID {
		return fmt.Errorf("%w: the root is not a record", ErrParams)
	}
	self, was, parent, ok := tx.tree.live(id)
	if !ok {
		return ErrNotFound
	}
	if was == name {
		return nil
	}
	var kids map[[16]byte][]ref
	if self.isDir {
		kids = tx.tree.childRefs()
	}
	if err := tx.place(kids, self, parent, name); err != nil {
		return err
	}
	if self.isDir {
		d := tx.tree.dirRec(id)
		d.Name = name
		d.Revision++
		d.LastWriter = a.opts.DeviceID
	} else {
		f := tx.tree.fileRec(id)
		f.Name = name
		f.Revision++
		f.LastWriter = a.opts.DeviceID
	}
	tx.changed = true
	return nil
}

// Move re-parents one live record, file or directory: one record written
// whatever subtree hangs beneath it, which is the point of the tree. The
// destination must be the root or a directory live in this transaction's
// index — an unknown, tombstoned or file destination is ErrNotFound; a
// directory moved into itself or into one of its descendants is
// ErrMoveIntoSelf; a name a live child of the destination already holds under
// case folding is ErrExists; and a subtree that would then stand more than
// format.MaxTreeDepth below the root, or any of whose records would join to a
// path over format.MaxPathLen bytes, is ErrTreeBounds — those two are the
// subtree's bounds, not the named record's (R39). A record already under
// parentID is a no-op. It writes parent_id, revision and last_writer, and
// never modified_at (APP.md §3).
func (tx *Tx) Move(id, parentID [16]byte) error {
	a := tx.a
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := tx.live(); err != nil {
		return err
	}
	if id == format.RootID {
		return fmt.Errorf("%w: the root is not a record", ErrParams)
	}
	self, name, parent, ok := tx.tree.live(id)
	if !ok {
		return ErrNotFound
	}
	if parent == parentID {
		return nil
	}
	if self.isDir && tx.tree.isBelow(parentID, id) {
		return fmt.Errorf("%w: %x is %x or one of its descendants", ErrMoveIntoSelf, parentID, id)
	}
	var kids map[[16]byte][]ref
	if self.isDir {
		kids = tx.tree.childRefs()
	}
	if err := tx.place(kids, self, parentID, name); err != nil {
		return err
	}
	if self.isDir {
		d := tx.tree.dirRec(id)
		d.ParentID = parentID
		d.Revision++
		d.LastWriter = a.opts.DeviceID
	} else {
		f := tx.tree.fileRec(id)
		f.ParentID = parentID
		f.Revision++
		f.LastWriter = a.opts.DeviceID
	}
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
// nothing — Archive.Publish is the explicit way to commit the index
// unchanged (R40, reclaim.go). After a failure at or after the commit point
// the Archive is Broken; before it, the transaction is aborted and the
// Archive usable.
//
// A commit that leaves a free run at the end of the file gives it back: the
// archive layer issues at once, inside this call, the empty commit R31's
// amended quarantine rule needs before the file may be truncated (trim.go).
// The sequence then advances by two rather than by one, and the Receipt is
// the one that follows the truncation.
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

	// The index extent, first-fit or appended; then the sealed index. The
	// sequence is advanced first because it can be refused (§4): a commit
	// that cannot number itself writes nothing at all.
	next := *a.sb
	if next.Seq, err = nextSeq(a.sb.Seq); err != nil {
		return Receipt{}, err
	}
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
	a.sb, a.live, a.index, a.tree, a.free = &next, target, index, newTree(index), free
	a.retired = newSpace(a.loser)
	a.rebuildPool()
	a.freeMapRebuilt = nil
	tx.done = true
	a.tx = nil
	// The tail comes back (R31 as amended, APP.md §2.3): a commit that left
	// a free run at the end of the file is followed at once by the empty
	// commit that lets it be truncated. A failure before that commit's own
	// flip is nobody's to hear — this one is durable, and the next commit
	// finds the tail and tries again; one at or after it breaks the Archive
	// like any other indeterminate commit. A sequence that cannot be advanced
	// is a refusal of that second commit alone (§4): this one is published,
	// so it is reported as the success it is and the tail simply stays.
	if err := a.reclaimTail(plain, kid, indexKey); err != nil && !errors.Is(err, ErrSeqExhausted) {
		return Receipt{}, err
	}
	return Receipt{Seq: a.sb.Seq, Size: a.size, WrittenAt: now()}, nil
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
func (a *Archive) Add(ctx context.Context, parentID [16]byte, name string, src io.ReaderAt, size int64) (FileInfo, Receipt, error) {
	var info FileInfo
	rec, err := a.oneTx(ctx, func(tx *Tx) (err error) {
		info, err = tx.Add(ctx, parentID, name, src, size)
		return err
	})
	return info, rec, err
}

// AddDir stages one directory in its own transaction.
func (a *Archive) AddDir(ctx context.Context, parentID [16]byte, name string, modifiedAt int64) (DirInfo, Receipt, error) {
	var info DirInfo
	rec, err := a.oneTx(ctx, func(tx *Tx) (err error) {
		info, err = tx.AddDir(parentID, name, modifiedAt)
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

// Rename renames one record in its own transaction.
func (a *Archive) Rename(ctx context.Context, id [16]byte, name string) (Receipt, error) {
	return a.oneTx(ctx, func(tx *Tx) error { return tx.Rename(id, name) })
}

// Move re-parents one record in its own transaction.
func (a *Archive) Move(ctx context.Context, id, parentID [16]byte) (Receipt, error) {
	return a.oneTx(ctx, func(tx *Tx) error { return tx.Move(id, parentID) })
}

// SetDictionary installs or clears the dictionary in its own transaction.
func (a *Archive) SetDictionary(ctx context.Context, dict []byte) (Receipt, error) {
	return a.oneTx(ctx, func(tx *Tx) error { return tx.SetDictionary(dict) })
}

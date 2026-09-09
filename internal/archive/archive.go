package archive

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/dreamxwarden01/enfold/internal/compress"
	"github.com/dreamxwarden01/enfold/internal/format"
	"github.com/dreamxwarden01/enfold/internal/kdf"
	"github.com/dreamxwarden01/enfold/internal/stream"
)

// Archive is an open archive file. It is safe for concurrent use: one
// transaction at a time, readers independent of it. Every method takes the
// Archive's lock except ID, which is fixed at Open; a transaction holds it
// only around its bookkeeping, not while a file is compressed and sealed, so
// readers keep serving while a large file is added.
type Archive struct {
	mu   sync.Mutex
	f    *os.File
	path string
	size uint64
	opts Options
	lock *fileLock

	env       *format.Envelope
	archiveID [16]byte
	kid       [16]byte // the kid the index opened under
	indexKey  []byte
	wrapKey   []byte

	sb    *format.ArchiveSuperblock
	live  format.Copy
	index *format.Index
	tree  *tree // index's two record tables by id (R39)

	free    *space         // the published free map, as on disk
	pool    *space         // free − retired − held: what may be allocated now
	retired *space         // freed by the previous commit; the losing superblock still references it
	held    map[extent]int // extents open Readers hold
	readers map[*Reader]struct{}
	loser   []extent // what the losing superblock copy references, for the record

	tx     *Tx
	broken error
	closed bool

	wPlain   *compress.Writer
	wDict    *compress.Writer
	wDictFor []byte // the dictionary wDict was built with

	stale          error
	envelopeStale  bool
	freeMapRebuilt error
}

// Create writes a new, empty archive at path, which must not exist, for
// archiveID under the key named kid, and opens it.
func Create(path string, archiveID, kid [16]byte, key [32]byte, opts Options) (*Archive, error) {
	opts = opts.withDefaults()
	if opts.ReadOnly {
		return nil, fmt.Errorf("%w: Create with ReadOnly", ErrParams)
	}
	if opts.DeviceID == [16]byte{} {
		return nil, fmt.Errorf("%w: DeviceID is required", ErrParams)
	}
	indexKey := kdf.ArchiveIndexKey(key, archiveID)
	defer kdf.Zero(indexKey)

	plain, err := (&format.Index{}).Encode()
	if err != nil {
		return nil, err
	}
	sb := format.ArchiveSuperblock{Seq: 1, IndexOff: format.ArchiveDataStart, IndexLen: uint64(len(plain))}
	if _, err := rand.Read(sb.IndexNonce[:]); err != nil {
		return nil, err
	}
	sealed, tag, err := sealIndex(indexKey, plain, &sb, archiveID, kid)
	if err != nil {
		return nil, err
	}
	sb.IndexTag = tag
	encMap, hash, err := (&format.FreeMap{}).Hash()
	if err != nil {
		return nil, err
	}
	sb.FreeMapOff = sb.IndexOff + sb.IndexLen + format.TagSize
	sb.FreeMapLen = uint64(len(encMap))
	sb.FreeMapHash = hash
	older := sb
	older.Seq = 0
	encA, err := sb.Encode()
	if err != nil {
		return nil, err
	}
	encB, err := older.Encode()
	if err != nil {
		return nil, err
	}
	env := format.Envelope{ArchiveID: archiveID, KID: kid}

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	failed := true
	defer func() {
		if failed {
			f.Close()
			os.Remove(path)
		}
	}()
	writes := []struct {
		b   []byte
		off uint64
	}{
		{env.Encode(), format.EnvelopeOff},
		{sealed, sb.IndexOff},
		{encMap, sb.FreeMapOff},
		{encA, format.CopyA.ArchiveSuperblockOff()},
		{encB, format.CopyB.ArchiveSuperblockOff()},
	}
	for _, w := range writes {
		if _, err := f.WriteAt(w.b, int64(w.off)); err != nil {
			return nil, err
		}
	}
	if err := f.Sync(); err != nil {
		return nil, err
	}
	f.Close()
	a, err := Open(path, []Key{{KID: kid, Key: key}}, opts)
	if err != nil {
		return nil, err
	}
	failed = false
	return a, nil
}

// ReadEnvelope reads the plaintext envelope: which archive this is and
// which key it expects. Nothing in it is trusted (§10).
func ReadEnvelope(path string) (*format.Envelope, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var b [format.SuperblockSize]byte
	if _, err := f.ReadAt(b[:], int64(format.EnvelopeOff)); err != nil {
		return nil, corrupt("envelope: %v", err)
	}
	return format.DecodeEnvelope(b[:])
}

// Open opens the archive at path with the first of keys that opens its
// index — the one the envelope names first, then the others (R33), which is
// how an interrupted rotation is recovered from. It never writes to the
// file and never modifies keys. A writable handle takes an exclusive lock on
// the file and needs Options.DeviceID.
func Open(path string, keys []Key, opts Options) (*Archive, error) {
	opts = opts.withDefaults()
	if !opts.ReadOnly && opts.DeviceID == [16]byte{} {
		return nil, fmt.Errorf("%w: DeviceID is required for a writable handle", ErrParams)
	}
	if err := opts.Compress.Validate(); err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("%w: no keys", ErrParams)
	}
	flag := os.O_RDWR
	if opts.ReadOnly {
		flag = os.O_RDONLY
	}
	f, err := os.OpenFile(path, flag, 0)
	if err != nil {
		return nil, err
	}
	a := &Archive{f: f, path: path, opts: opts, held: map[extent]int{}, readers: map[*Reader]struct{}{}}
	ok := false
	defer func() {
		if !ok {
			a.Close()
		}
	}()
	if !opts.ReadOnly {
		lock, err := lockFile(f, path)
		if err != nil {
			return nil, err
		}
		a.lock = lock
	}
	if err := a.load(keys); err != nil {
		return nil, err
	}
	ok = true
	return a, nil
}

// load reads and checks everything: envelope, superblocks, index under the
// first key that opens it, free map, and the cross-checks of §13 and R19.
func (a *Archive) load(keys []Key) error {
	st, err := a.f.Stat()
	if err != nil {
		return err
	}
	a.size = uint64(st.Size())
	if a.size < format.ArchiveDataStart {
		return corrupt("file of %d bytes is shorter than the fixed regions", a.size)
	}
	var envB, sbA, sbB [format.SuperblockSize]byte
	for _, r := range []struct {
		b   []byte
		off uint64
	}{{envB[:], format.EnvelopeOff}, {sbA[:], format.CopyA.ArchiveSuperblockOff()}, {sbB[:], format.CopyB.ArchiveSuperblockOff()}} {
		if _, err := a.f.ReadAt(r.b, int64(r.off)); err != nil {
			return err
		}
	}
	env, err := format.DecodeEnvelope(envB[:])
	if err != nil {
		return err
	}
	a.env, a.archiveID = env, env.ArchiveID
	sb, live, stale, err := format.PickArchiveSuperblock(sbA[:], sbB[:])
	if err != nil {
		return err
	}
	if err := sb.ValidateExtents(a.size); err != nil {
		return err
	}
	a.sb, a.live, a.stale = sb, live, stale

	// The index, under the first key that opens it: the envelope's kid
	// first, the others after (R33). The caller's slice is not touched.
	ct := make([]byte, sb.IndexLen+format.TagSize)
	if _, err := a.f.ReadAt(ct, int64(sb.IndexOff)); err != nil {
		return err
	}
	if [format.TagSize]byte(ct[sb.IndexLen:]) != sb.IndexTag {
		return corrupt("index tag in the file differs from the superblock's")
	}
	var index *format.Index
	for pass := 0; pass < 2 && index == nil; pass++ {
		for _, k := range keys {
			if (k.KID == env.KID) != (pass == 0) {
				continue
			}
			indexKey := kdf.ArchiveIndexKey(k.Key, a.archiveID)
			plain, err := openIndex(indexKey, ct, sb, a.archiveID, k.KID)
			if err != nil {
				kdf.Zero(indexKey)
				continue
			}
			index, err = format.DecodeIndex(plain)
			kdf.Zero(plain)
			if err != nil {
				kdf.Zero(indexKey)
				return err
			}
			a.kid, a.indexKey = k.KID, indexKey
			a.wrapKey = kdf.ArchiveWrapKey(k.Key, a.archiveID)
			break
		}
	}
	if index == nil {
		return ErrKey
	}
	a.index, a.tree = index, newTree(index)
	a.envelopeStale = a.kid != env.KID

	// The losing copy: what it still references is quarantined from
	// allocation, so that a torn live copy opens one commit behind. Its
	// superblock is only checksummed and its index, though authenticated,
	// is checked like the live one before its extents are believed.
	a.loser = nil
	loserB := sbB[:]
	if live == format.CopyB {
		loserB = sbA[:]
	}
	if lsb, err := format.DecodeArchiveSuperblock(loserB); err == nil && lsb.ValidateExtents(a.size) == nil {
		a.loser = []extent{{Off: lsb.IndexOff, Len: lsb.IndexLen + format.TagSize}, {Off: lsb.FreeMapOff, Len: lsb.FreeMapLen}}
		lct := make([]byte, lsb.IndexLen+format.TagSize)
		if _, err := a.f.ReadAt(lct, int64(lsb.IndexOff)); err == nil {
			if plain, err := openIndex(a.indexKey, lct, lsb, a.archiveID, a.kid); err == nil {
				lidx, err := format.DecodeIndex(plain)
				kdf.Zero(plain)
				if err == nil {
					if exts, err := liveExtents(lidx, lsb, a.size); err == nil {
						a.loser = append(a.loser, exts...)
					}
				}
			}
		}
	}
	return a.check()
}

// liveExtents returns the live records' extents of an index, having checked
// that they lie inside the file, fit the STREAM framing, and overlap neither
// each other nor the superblock's index and free-map extents.
func liveExtents(x *format.Index, sb *format.ArchiveSuperblock, size uint64) ([]extent, error) {
	type owned struct {
		e    extent
		what string
	}
	all := []owned{
		{extent{Off: sb.IndexOff, Len: sb.IndexLen + format.TagSize}, "index"},
		{extent{Off: sb.FreeMapOff, Len: sb.FreeMapLen}, "free map"},
	}
	var out []extent
	for i := range x.Files {
		r := &x.Files[i]
		if r.State != format.FileLive {
			continue
		}
		e := extent{Off: r.DataOff, Len: r.StoredSize}
		if end(e) > size {
			return nil, corrupt("file %x extent [0x%x, 0x%x) exceeds the file size %d", r.FileID, e.Off, end(e), size)
		}
		if _, err := stream.PlaintextLen(r.StoredSize); err != nil {
			return nil, corrupt("file %x stored_size %d fits no STREAM framing", r.FileID, r.StoredSize)
		}
		all = append(all, owned{e, fmt.Sprintf("file %x", r.FileID)})
		out = append(out, e)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].e.Off < all[j].e.Off })
	for i := 1; i < len(all); i++ {
		if all[i].e.Off < end(all[i-1].e) {
			return nil, corrupt("%s overlaps %s", all[i].what, all[i-1].what)
		}
	}
	return out, nil
}

// check runs the open-time cross-checks and builds the allocation view.
func (a *Archive) check() error {
	sb := a.sb
	exts, err := liveExtents(a.index, sb, a.size)
	if err != nil {
		return err
	}
	used := newSpace(nil)
	used.insert(extent{Off: sb.IndexOff, Len: sb.IndexLen + format.TagSize})
	used.insert(extent{Off: sb.FreeMapOff, Len: sb.FreeMapLen})
	for _, e := range exts {
		used.insert(e)
	}

	// The free map: hashed, well-formed, inside the file, disjoint from
	// everything used. A map that fails is rebuilt from the index — the
	// index is authenticated, the map only checksummed (§13).
	encMap := make([]byte, sb.FreeMapLen)
	if _, err := a.f.ReadAt(encMap, int64(sb.FreeMapOff)); err != nil {
		return err
	}
	free := &space{}
	fm, err := format.DecodeFreeMapChecked(encMap, sb.FreeMapHash)
	if err == nil {
		// The decoded map is sorted and disjoint (format.FreeMap.Validate);
		// take it as is after the bounds and overlap checks, without
		// re-inserting extent by extent.
		for _, e := range fm.Extents {
			if end(e) > a.size {
				err = corrupt("free extent [0x%x, 0x%x) exceeds the file size %d", e.Off, end(e), a.size)
				break
			}
			if used.intersects(e) {
				err = corrupt("free extent [0x%x, 0x%x) overlaps a live extent", e.Off, end(e))
				break
			}
		}
		if err == nil {
			free = (&space{}).merged(&space{x: fm.Extents})
		}
	}
	if err != nil {
		a.freeMapRebuilt = err
		free = &space{}
	}
	// The loser's extents are reserved (used, when nobody else claims them)
	// or quarantined (retired, when the map lists them free); either way
	// they are not allocated. Whatever nobody references — a tail an
	// interrupted transaction appended — is free.
	loser := newSpace(nil)
	for _, e := range a.loser {
		loser.union(e)
	}
	reserved := loser.clone()
	reserved.subtract(free)
	covered := used.merged(reserved).merged(free)
	gap := newSpace([]extent{{Off: format.ArchiveDataStart, Len: a.size - format.ArchiveDataStart}})
	gap.subtract(covered)
	a.free = free.merged(gap)
	a.retired = loser.clone()
	a.retired.subtract(used)
	a.rebuildPool()
	return nil
}

// rebuildPool recomputes what may be allocated: the published map minus the
// previous commit's frees minus what readers hold. Caller holds a.mu.
func (a *Archive) rebuildPool() {
	a.pool = a.free.clone()
	a.pool.subtract(a.retired)
	for e := range a.held {
		a.pool.removeOverlap(e)
	}
}

func indexAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// sealIndex seals the index plaintext under key with the AAD the superblock
// fields define; sb must already carry the extent and nonce.
func sealIndex(key, plain []byte, sb *format.ArchiveSuperblock, archiveID, kid [16]byte) ([]byte, [format.TagSize]byte, error) {
	g, err := indexAEAD(key)
	if err != nil {
		return nil, [format.TagSize]byte{}, err
	}
	sealed := g.Seal(nil, sb.IndexNonce[:], plain, sb.IndexAAD(archiveID, kid))
	return sealed, [format.TagSize]byte(sealed[len(plain):]), nil
}

// openIndex decrypts ct ‖ tag under key. A failure is reported as ErrKey:
// wrong key, wrong kid, or a modified index are indistinguishable.
func openIndex(key, ct []byte, sb *format.ArchiveSuperblock, archiveID, kid [16]byte) ([]byte, error) {
	g, err := indexAEAD(key)
	if err != nil {
		return nil, err
	}
	plain, err := g.Open(nil, sb.IndexNonce[:], ct, sb.IndexAAD(archiveID, kid))
	if err != nil {
		return nil, ErrKey
	}
	return plain, nil
}

// usable refuses a closed or broken Archive. Caller holds a.mu.
func (a *Archive) usable() error {
	if a.closed {
		return ErrClosed
	}
	if a.broken != nil {
		return a.broken
	}
	return nil
}

// writable refuses a handle that may not change the file. Caller holds a.mu.
func (a *Archive) writable() error {
	if err := a.usable(); err != nil {
		return err
	}
	if a.opts.ReadOnly {
		return ErrReadOnly
	}
	return nil
}

// Broken is nil, or the error of a commit that failed at or after its
// commit point; every further operation refuses until the file is reopened.
func (a *Archive) Broken() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.broken
}

// ID is the archive's identity, fixed at Open.
func (a *Archive) ID() [16]byte { return a.archiveID }

// KID is the key the archive is currently under; RotateKey changes it.
func (a *Archive) KID() [16]byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.kid
}

// Stale is the damage found on the superblock copy that lost, when it lost
// by being damaged rather than older; nil when both were sound.
func (a *Archive) Stale() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.stale
}

// EnvelopeStale reports that the index opened under a kid other than the
// envelope's — a key rotation was interrupted before the envelope was
// rewritten. RepairEnvelope fixes it; Open does not write.
func (a *Archive) EnvelopeStale() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.envelopeStale
}

// FreeMapRebuilt is the error the free map on disk failed with, when it
// failed its hash or its consistency checks and was rebuilt from the index;
// the rebuilt map is written by the next commit. nil when the map was sound.
func (a *Archive) FreeMapRebuilt() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.freeMapRebuilt
}

// Stat reports the file's size, its live file count, and how much of it the
// published free map lists as free. On a closed or broken Archive it
// reports zeros: the last known state is not the file's.
// Seq is the live superblock's sequence number: the keyless identity of
// the file's state (FORMAT.md R36). Zero when the Archive is not usable.
func (a *Archive) Seq() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.usable() != nil || a.sb == nil {
		return 0
	}
	return a.sb.Seq
}

// Stat's count is live files only: a directory is a record of its own (R39)
// but occupies no data region, so counting folders among the files would make
// the number mean neither one thing nor the other. Dirs is the directories.
func (a *Archive) Stat() (size uint64, files int, free uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.usable() != nil {
		return 0, 0, 0
	}
	for i := range a.index.Files {
		if a.index.Files[i].State == format.FileLive {
			files++
		}
	}
	return a.size, files, a.free.total()
}

// Files lists every live file, in the index's record order. The list is the
// decrypted index; it must not cross the WebView boundary whole (DESIGN.md
// §10) — page it. Nil on a closed or broken Archive.
func (a *Archive) Files() []FileInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.usable() != nil {
		return nil
	}
	var out []FileInfo
	for i := range a.index.Files {
		if a.index.Files[i].State == format.FileLive {
			out = append(out, infoOf(&a.index.Files[i]))
		}
	}
	return out
}

// Dirs lists every live directory, in the index's record order. It is the
// other half of the snapshot Files gives: the tree the caller lists, walks
// and draws a breadcrumb from is these two together (APP.md §2.3), never a
// projection of prefixes.
func (a *Archive) Dirs() []DirInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.usable() != nil {
		return nil
	}
	var out []DirInfo
	for i := range a.index.Dirs {
		if a.index.Dirs[i].State == format.FileLive {
			out = append(out, dirInfoOf(&a.index.Dirs[i]))
		}
	}
	return out
}

// Info describes one live file by ID. A directory's id is InfoDir's.
func (a *Archive) Info(id [16]byte) (FileInfo, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.usable() != nil {
		return FileInfo{}, false
	}
	if r := a.tree.liveFile(id); r != nil {
		return infoOf(r), true
	}
	return FileInfo{}, false
}

// InfoDir describes one live directory by ID.
func (a *Archive) InfoDir(id [16]byte) (DirInfo, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.usable() != nil {
		return DirInfo{}, false
	}
	if d := a.tree.liveDir(id); d != nil {
		return dirInfoOf(d), true
	}
	return DirInfo{}, false
}

// Children lists the live children of one directory — format.RootID is the
// root — in the index's record order, directories and files apart. There is
// no lookup by name and none by path: a listing is the tree's children of an
// id, and an id that is neither the root nor a live directory is ErrNotFound
// rather than an empty listing under a folder that is gone (APP.md §3).
func (a *Archive) Children(parentID [16]byte) ([]DirInfo, []FileInfo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.usable(); err != nil {
		return nil, nil, err
	}
	if !a.tree.parentUsable(parentID) {
		return nil, nil, ErrNotFound
	}
	var dirs []DirInfo
	var files []FileInfo
	for i := range a.index.Dirs {
		if d := &a.index.Dirs[i]; d.State == format.FileLive && d.ParentID == parentID {
			dirs = append(dirs, dirInfoOf(d))
		}
	}
	for i := range a.index.Files {
		if f := &a.index.Files[i]; f.State == format.FileLive && f.ParentID == parentID {
			files = append(files, infoOf(f))
		}
	}
	return dirs, files, nil
}

// Path is the joined path of one live record of either kind: its ancestors'
// names and its own, separated by '/' (R20, R39). The root's is empty. It is
// derived from the tree on demand — no record carries a path.
func (a *Archive) Path(id [16]byte) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.usable(); err != nil {
		return "", err
	}
	return a.tree.path(id)
}

// record finds a live file record by ID; nil when there is none.
func (a *Archive) record(id [16]byte) *format.FileRecord {
	return a.tree.liveFile(id)
}

// Hash is the SHA-256 of the whole file as it is now, for the registry's
// last_ciphertext_hash. It reads the whole file under the lock.
func (a *Archive) Hash(ctx context.Context) ([32]byte, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.usable(); err != nil {
		return [32]byte{}, err
	}
	h := sha256.New()
	if err := hashFile(ctx, a.f, a.size, h); err != nil {
		return [32]byte{}, err
	}
	return [32]byte(h.Sum(nil)), nil
}

// hashFile feeds the first size bytes of f into h. A file shorter than that
// is an error, never a hash of stale buffer contents.
func hashFile(ctx context.Context, f *os.File, size uint64, h hashWriter) error {
	buf := make([]byte, 1<<20)
	for off := uint64(0); off < size; {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := min(uint64(len(buf)), size-off)
		got, err := f.ReadAt(buf[:n], int64(off))
		if uint64(got) != n {
			if err == nil {
				err = corrupt("file is shorter than %d bytes", size)
			}
			return err
		}
		h.Write(buf[:n])
		off += n
	}
	return nil
}

type hashWriter interface {
	Write([]byte) (int, error)
}

// RepairEnvelope rewrites the envelope with the kid the index opened under,
// clearing EnvelopeStale. It changes the file's bytes, so the caller updates
// the registry's ciphertext hash alongside.
func (a *Archive) RepairEnvelope() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.writable(); err != nil {
		return err
	}
	return a.writeEnvelope()
}

// writeEnvelope writes the envelope for the current kid. Caller holds a.mu.
func (a *Archive) writeEnvelope() error {
	env := format.Envelope{ArchiveID: a.archiveID, KID: a.kid}
	if _, err := a.f.WriteAt(env.Encode(), int64(format.EnvelopeOff)); err != nil {
		return err
	}
	if err := a.f.Sync(); err != nil {
		return err
	}
	a.env, a.envelopeStale = &env, false
	return nil
}

// Close releases the file and the lock, zeroes the derived keys, ends the
// open transaction, and invalidates every Reader: their reads fail with
// ErrClosed from then on, and what they had decrypted is zeroed.
func (a *Archive) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil
	}
	a.closed = true
	return a.teardown()
}

// teardown releases everything an open handle holds. Caller holds a.mu and
// has set a.closed.
func (a *Archive) teardown() error {
	if a.tx != nil {
		a.tx.done = true
		a.tx = nil
	}
	for r := range a.readers {
		r.kill()
	}
	clear(a.readers)
	clear(a.held)
	if a.wPlain != nil {
		a.wPlain.Release()
		a.wPlain = nil
	}
	if a.wDict != nil {
		a.wDict.Release()
		a.wDict = nil
	}
	kdf.Zero(a.indexKey)
	kdf.Zero(a.wrapKey)
	a.indexKey, a.wrapKey = nil, nil
	var err error
	if a.lock != nil {
		err = a.lock.release()
		a.lock = nil
	}
	if a.f != nil {
		err = errors.Join(err, a.f.Close())
	}
	return err
}

// writer returns the compress.Writer for the given dictionary — nil for
// none — creating it on first use and rebuilding it when the dictionary
// differs from the one it was built with. Caller holds a.mu.
func (a *Archive) writer(dict []byte) (*compress.Writer, error) {
	p := a.opts.Compress
	if len(dict) == 0 {
		if a.wPlain == nil {
			w, err := compress.NewWriter(p)
			if err != nil {
				return nil, err
			}
			a.wPlain = w
		}
		return a.wPlain, nil
	}
	if a.wDict != nil && !bytes.Equal(a.wDictFor, dict) {
		a.wDict.Release()
		a.wDict, a.wDictFor = nil, nil
	}
	if a.wDict == nil {
		p.Dict = dict
		w, err := compress.NewWriter(p)
		if err != nil {
			return nil, err
		}
		a.wDict, a.wDictFor = w, bytes.Clone(dict)
	}
	return a.wDict, nil
}

// tempName builds a sibling path for a temporary file.
func tempName(target, suffix string) (string, error) {
	var r [8]byte
	if _, err := rand.Read(r[:]); err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(target), fmt.Sprintf(".%s.%s-%x", filepath.Base(target), suffix, r)), nil
}

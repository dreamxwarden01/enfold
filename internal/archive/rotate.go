package archive

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/dreamxwarden01/enfold/internal/format"
	"github.com/dreamxwarden01/enfold/internal/kdf"
)

// RotateKey re-keys the archive (§7.3): every live DEK is re-wrapped under
// the new archive key's wrap key, the index is re-sealed under its index
// key with newKID in the AAD, and the envelope is rewritten. No file data
// is touched, and dek_epoch does not move — the DEK AAD does not include
// the kid. Every record is carried over as it stands, directories included,
// in the order §11 requires and with no id changed, so the tree a caller was
// holding is the tree it gets back (R33).
//
// Contract: the caller has already committed the new version to the
// registry, current, with the old one retired. Then a crash anywhere in
// here leaves the archive under one of two keys the registry knows, and
// Open with both candidates finds it. Calling RotateKey with a key the
// registry does not yet hold risks an archive under a key that exists
// nowhere.
func (a *Archive) RotateKey(ctx context.Context, newKID [16]byte, newKey [32]byte) (Receipt, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.writable(); err != nil {
		return Receipt{}, err
	}
	if a.tx != nil {
		return Receipt{}, ErrTxOpen
	}
	if newKID == a.kid {
		return Receipt{}, fmt.Errorf("%w: new kid equals the current one", ErrParams)
	}
	wrapNew := kdf.ArchiveWrapKey(newKey, a.archiveID)
	indexNew := kdf.ArchiveIndexKey(newKey, a.archiveID)
	failed := true
	defer func() {
		if failed {
			kdf.Zero(wrapNew)
			kdf.Zero(indexNew)
		}
	}()

	// A whole new record set, one DEK in memory at a time.
	index, err := cloneIndex(a.index)
	if err != nil {
		return Receipt{}, err
	}
	for i := range index.Files {
		r := &index.Files[i]
		if r.State != format.FileLive {
			continue
		}
		aad := format.DEKAAD(a.archiveID, r.FileID, r.DEKEpoch)
		dek, err := kdf.UnwrapKey(a.wrapKey, r.WrappedDEK, r.DEKNonce, aad)
		if err != nil {
			return Receipt{}, corrupt("file %x: DEK does not unwrap under the current key", r.FileID)
		}
		r.WrappedDEK, r.DEKNonce, err = kdf.WrapKey(wrapNew, dek, aad)
		kdf.Zero(dek[:])
		if err != nil {
			return Receipt{}, err
		}
	}

	tx := &Tx{a: a, index: index, tree: newTree(index), pool: a.pool.clone(), allocs: newSpace(nil), pending: newSpace(nil), size0: a.size, changed: true}
	rec, err := a.commit(ctx, tx, index, newKID, indexNew)
	if err != nil {
		if a.broken == nil && a.size > tx.size0 {
			if terr := a.f.Truncate(int64(tx.size0)); terr == nil {
				a.size = tx.size0
			}
		}
		return Receipt{}, err
	}
	failed = false
	kdf.Zero(a.wrapKey)
	kdf.Zero(a.indexKey)
	a.wrapKey, a.indexKey, a.kid = wrapNew, indexNew, newKID
	// The envelope, last: until it is rewritten the archive opens through
	// the candidate list, and EnvelopeStale says so.
	if err := a.writeEnvelope(); err != nil {
		a.envelopeStale = true
		return rec, fmt.Errorf("archive: rotated, but the envelope was not rewritten: %w", err)
	}
	return rec, nil
}

// Compact rewrites the archive without free space into a fresh file beside
// it and renames it over the original (R33). Every record — directory and
// file, live and tombstone — and the dictionary are carried over in the order
// §11 requires, no dir_id or file_id changes, and live extents are copied
// verbatim (ciphertext, unchanged DEKs, §13); only a live file's data_off
// moves, so the tree a caller was holding survives compaction. Before
// the rename the new file's superblocks, extents and index are read back
// and checked; file contents are copied verbatim and not re-authenticated.
// This handle is closed by the operation — on success its file no longer
// exists, on a failed rename the original is intact — and the caller
// reopens the path; the returned hash and size are the new file's, for the
// registry. Readers must be closed first (ErrBusy otherwise).
//
// progress, when non-nil, is called on this goroutine with the Archive's
// lock held, once per chunk copied; it must not call back into the Archive
// (only ID is lock-free) — cancel through ctx.
func (a *Archive) Compact(ctx context.Context, progress func(done, total uint64)) (hash [32]byte, size uint64, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.writable(); err != nil {
		return hash, 0, err
	}
	if a.tx != nil {
		return hash, 0, ErrTxOpen
	}
	if len(a.held) != 0 {
		return hash, 0, fmt.Errorf("%w: readers are open", ErrBusy)
	}

	index, err := cloneIndex(a.index)
	if err != nil {
		return hash, 0, err
	}
	live := make([]*format.FileRecord, 0, len(index.Files))
	var total uint64
	for i := range index.Files {
		if r := &index.Files[i]; r.State == format.FileLive {
			live = append(live, r)
			total += r.StoredSize
		}
	}
	sort.Slice(live, func(i, j int) bool { return live[i].DataOff < live[j].DataOff })

	tmp, err := tempName(a.path, "compact")
	if err != nil {
		return hash, 0, err
	}
	out, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return hash, 0, err
	}
	ok := false
	defer func() {
		if !ok {
			out.Close()
			os.Remove(tmp)
		}
	}()
	h := sha256.New()
	// Writes are sequential and feed the hash as they go; the superblocks
	// are patched in at the end and the hash recomputed over the file.
	var written uint64
	write := func(b []byte, off uint64) error {
		if off != written {
			return fmt.Errorf("%w: compaction writes out of order", ErrInternal)
		}
		if _, err := out.WriteAt(b, int64(off)); err != nil {
			return err
		}
		h.Write(b)
		written += uint64(len(b))
		return nil
	}
	env := format.Envelope{ArchiveID: a.archiveID, KID: a.kid}
	if err := write(env.Encode(), format.EnvelopeOff); err != nil {
		return hash, 0, err
	}
	// Superblocks are written last (they need the index extent); reserve.
	zeros := make([]byte, 2*format.SuperblockSize)
	if err := write(zeros, format.CopyA.ArchiveSuperblockOff()); err != nil {
		return hash, 0, err
	}
	// Data, packed from the start of the data region.
	off := uint64(format.ArchiveDataStart)
	buf := make([]byte, 1<<20)
	var done uint64
	for _, r := range live {
		if err := ctx.Err(); err != nil {
			return hash, 0, err
		}
		src := io.NewSectionReader(a.f, int64(r.DataOff), int64(r.StoredSize))
		r.DataOff = off
		for {
			// Per chunk, not per file: one record can be many gigabytes, and a
			// cancel that waits for the next file is not a cancel (the outside
			// audit of 2026-09-09).
			if err := ctx.Err(); err != nil {
				return hash, 0, err
			}
			n, rerr := src.Read(buf)
			if n > 0 {
				if err := write(buf[:n], off); err != nil {
					return hash, 0, err
				}
				off += uint64(n)
				done += uint64(n)
				if progress != nil {
					progress(done, total)
				}
			}
			if rerr != nil {
				if errors.Is(rerr, io.EOF) {
					break
				}
				return hash, 0, rerr
			}
		}
	}
	// Index, then an empty free map, then both superblocks.
	plain, err := index.Encode()
	if err != nil {
		return hash, 0, err
	}
	defer kdf.Zero(plain)
	sb := format.ArchiveSuperblock{Seq: 1, IndexOff: off, IndexLen: uint64(len(plain))}
	if _, err := rand.Read(sb.IndexNonce[:]); err != nil {
		return hash, 0, err
	}
	sealed, tag, err := sealIndex(a.indexKey, plain, &sb, a.archiveID, a.kid)
	if err != nil {
		return hash, 0, err
	}
	sb.IndexTag = tag
	if err := write(sealed, off); err != nil {
		return hash, 0, err
	}
	off += uint64(len(sealed))
	encMap, mapHash, err := (&format.FreeMap{}).Hash()
	if err != nil {
		return hash, 0, err
	}
	sb.FreeMapOff, sb.FreeMapLen, sb.FreeMapHash = off, uint64(len(encMap)), mapHash
	if err := write(encMap, off); err != nil {
		return hash, 0, err
	}
	older := sb
	older.Seq = 0
	encA, err := sb.Encode()
	if err != nil {
		return hash, 0, err
	}
	encB, err := older.Encode()
	if err != nil {
		return hash, 0, err
	}
	// The superblocks land in their reserved slots; the hash was fed zeros
	// for them, so recompute it over the finished file instead.
	if _, err := out.WriteAt(encA, int64(format.CopyA.ArchiveSuperblockOff())); err != nil {
		return hash, 0, err
	}
	if _, err := out.WriteAt(encB, int64(format.CopyB.ArchiveSuperblockOff())); err != nil {
		return hash, 0, err
	}
	if err := out.Sync(); err != nil {
		return hash, 0, err
	}
	size = written
	h.Reset()
	if err := hashFile(ctx, out, size, h); err != nil {
		return hash, 0, err
	}
	hash = [32]byte(h.Sum(nil))
	if err := out.Close(); err != nil {
		return hash, 0, err
	}
	// Verify before the irreversible step: the new file must open under the
	// same key, and every record must be where the index says.
	if err := a.verifyCompacted(tmp); err != nil {
		return hash, 0, err
	}
	// The rename cannot proceed over a file this handle holds open and
	// locked. From here on the handle is closed whatever happens: on a
	// failed rename the original is untouched and the caller reopens it.
	// The path stays claimed in this process until the rename has returned,
	// so no second writer opens the original in the window.
	a.closed = true
	lock := a.lock
	a.lock = nil
	if lock != nil {
		lock.releaseOS()
		defer lock.releasePath()
	}
	a.f.Close()
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
	if err := os.Rename(tmp, a.path); err != nil {
		return hash, 0, fmt.Errorf("archive: compacted file not moved into place (the original is intact; reopen it): %w", err)
	}
	ok = true
	return hash, size, nil
}

// verifyCompacted reads the compacted file's structure with this handle's
// derived keys: superblocks, extents, and the index under the index key.
func (a *Archive) verifyCompacted(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	var sbA, sbB [format.SuperblockSize]byte
	if _, err := f.ReadAt(sbA[:], int64(format.CopyA.ArchiveSuperblockOff())); err != nil {
		return err
	}
	if _, err := f.ReadAt(sbB[:], int64(format.CopyB.ArchiveSuperblockOff())); err != nil {
		return err
	}
	sb, _, _, err := format.PickArchiveSuperblock(sbA[:], sbB[:])
	if err != nil {
		return fmt.Errorf("%w: compacted superblocks: %v", ErrInternal, err)
	}
	if err := sb.ValidateExtents(uint64(st.Size())); err != nil {
		return fmt.Errorf("%w: compacted extents: %v", ErrInternal, err)
	}
	ct := make([]byte, sb.IndexLen+format.TagSize)
	if _, err := f.ReadAt(ct, int64(sb.IndexOff)); err != nil {
		return err
	}
	plain, err := openIndex(a.indexKey, ct, sb, a.archiveID, a.kid)
	if err != nil {
		return fmt.Errorf("%w: compacted index does not open", ErrInternal)
	}
	defer kdf.Zero(plain)
	index, err := format.DecodeIndex(plain)
	if err != nil {
		return fmt.Errorf("%w: compacted index: %v", ErrInternal, err)
	}
	for i := range index.Files {
		if r := &index.Files[i]; r.State == format.FileLive && r.DataOff+r.StoredSize > uint64(st.Size()) {
			return fmt.Errorf("%w: compacted file %x lies outside the file", ErrInternal, r.FileID)
		}
	}
	return nil
}

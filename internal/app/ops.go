package app

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/dreamxwarden01/enfold/internal/archive"
	"github.com/dreamxwarden01/enfold/internal/compress"
	"github.com/dreamxwarden01/enfold/internal/format"
	"github.com/dreamxwarden01/enfold/internal/kdf"
)

// op is a long operation: an id, progress coalesced to a few events per
// second, a result the frontend can fetch again after a window is rebuilt.
type op struct {
	id        string
	kind      string
	archiveID string
	startedAt time.Time
	done      atomic.Uint64
	total     atomic.Uint64
	phase     atomic.Value // string
	cancel    context.CancelFunc
	finished  bool
	err       *Error
	results   []FileOutcome
	c         *Core
	lastEmit  time.Time
}

func (o *op) view() OpView {
	v := OpView{ID: o.id, Kind: o.kind, ArchiveID: o.archiveID, Done: o.done.Load(), Total: o.total.Load(), StartedAt: o.startedAt.Unix(), Finished: o.finished, Results: o.results}
	if p, ok := o.phase.Load().(string); ok {
		v.Phase = p
	}
	if o.err != nil {
		v.Error = o.err.Code
	}
	return v
}

// progress records and, at most ten times a second, emits.
func (o *op) progress(done, total uint64, phase string) {
	o.done.Store(done)
	o.total.Store(total)
	if phase != "" {
		o.phase.Store(phase)
	}
	now := o.c.now()
	o.c.mu.Lock()
	if now.Sub(o.lastEmit) < 100*time.Millisecond && done != total {
		o.c.mu.Unlock()
		return
	}
	o.lastEmit = now
	v := o.view()
	o.c.mu.Unlock()
	o.c.emit(EventOpProgress, v)
}

// startOp registers an operation and runs fn on its own goroutine.
func (c *Core) startOp(kind, archiveID string, fn func(ctx context.Context, o *op) ([]FileOutcome, error)) string {
	ctx, cancel := context.WithCancel(context.Background())
	o := &op{id: randomID(), kind: kind, archiveID: archiveID, startedAt: c.now(), cancel: cancel, c: c}
	o.phase.Store("starting")
	c.mu.Lock()
	c.ops[o.id] = o
	c.mu.Unlock()
	go func() {
		defer func() {
			if r := recover(); r != nil {
				c.log("op %s panic: %v", kind, r)
				c.finishOp(o, nil, coded(CodeInternal))
			}
		}()
		results, err := fn(ctx, o)
		var e *Error
		if err != nil {
			if errors.Is(err, context.Canceled) {
				e = coded(CodeOpCancelled)
			} else {
				e = c.fail(kind, err)
			}
		}
		c.finishOp(o, results, e)
	}()
	return o.id
}

func (c *Core) finishOp(o *op, results []FileOutcome, e *Error) {
	c.mu.Lock()
	o.finished, o.results, o.err = true, results, e
	v := o.view()
	c.mu.Unlock()
	c.emit(EventOpDone, v)
	c.emitState()
	// Keep finished ops for a while so a rebuilt window sees the outcome.
	c.deps.Clock.AfterFunc(5*time.Minute, func() {
		c.mu.Lock()
		delete(c.ops, o.id)
		c.mu.Unlock()
	})
}

// CancelOp cancels a running operation.
func (c *Core) CancelOp(id string) *Error {
	c.mu.Lock()
	o := c.ops[id]
	c.mu.Unlock()
	if o == nil {
		return coded(CodeOpNotFound)
	}
	o.cancel()
	return nil
}

// Op returns an operation's current view.
func (c *Core) Op(id string) (OpView, *Error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	o := c.ops[id]
	if o == nil {
		return OpView{}, coded(CodeOpNotFound)
	}
	return o.view(), nil
}

// AddPolicy is what happens when an added name already exists.
type AddPolicy string

const (
	PolicySkip     AddPolicy = "skip"
	PolicyReplace  AddPolicy = "replace"
	PolicyKeepBoth AddPolicy = "keep-both"
)

// AddFiles stages files under folder. Each file is one Tx.Add with its
// size from os.Stat; a source that changes underneath is reported per file
// and the rest continue.
func (c *Core) AddFiles(id, folder string, paths []string, policy AddPolicy) (string, *Error) {
	oa, e := c.findArchive(id)
	if e != nil {
		return "", e
	}
	if len(paths) == 0 {
		return "", coded(CodeParams)
	}
	items := make([]addItem, 0, len(paths))
	for _, p := range paths {
		items = append(items, addItem{src: p, name: path.Base(filepath.ToSlash(p))})
	}
	return c.startOp("add", id, func(ctx context.Context, o *op) ([]FileOutcome, error) {
		return c.addItems(ctx, o, oa, strings.Trim(folder, "/"), items, policy)
	}), nil
}

type addItem struct {
	src  string
	name string // relative name under the folder, `/`-separated
}

// AddFolder stages a directory tree under folder/<base>.
func (c *Core) AddFolder(id, folder, dir string, policy AddPolicy) (string, *Error) {
	oa, e := c.findArchive(id)
	if e != nil {
		return "", e
	}
	base := filepath.Base(dir)
	return c.startOp("add", id, func(ctx context.Context, o *op) ([]FileOutcome, error) {
		var items []addItem
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return err
			}
			items = append(items, addItem{src: p, name: base + "/" + filepath.ToSlash(rel)})
			return nil
		})
		if err != nil {
			return nil, err
		}
		return c.addItems(ctx, o, oa, strings.Trim(folder, "/"), items, policy)
	}), nil
}

// addItems is the shared add loop: pre-flight every composed name against
// R20 and the merged view, then add one by one.
func (c *Core) addItems(ctx context.Context, o *op, oa *openArchive, folder string, items []addItem, policy AddPolicy) ([]FileOutcome, error) {
	oa.opMu.Lock()
	defer oa.opMu.Unlock()
	if policy == "" {
		policy = PolicySkip
	}
	results := make([]FileOutcome, 0, len(items))
	var total uint64
	sizes := make([]int64, len(items))
	for i, it := range items {
		st, err := os.Stat(it.src)
		if err != nil {
			sizes[i] = -1
			continue
		}
		sizes[i] = st.Size()
		total += uint64(st.Size())
	}
	o.progress(0, total, "adding")
	var done uint64
	for i, it := range items {
		if ctx.Err() != nil {
			return results, ctx.Err()
		}
		res := FileOutcome{Path: it.src, Name: it.name}
		if sizes[i] < 0 {
			res.Outcome, res.Code = "failed", CodeIO
			results = append(results, res)
			continue
		}
		name := it.name
		if folder != "" {
			name = folder + "/" + it.name
		}
		if err := format.ValidateFileName(name); err != nil {
			res.Outcome, res.Code = "failed", CodeFileName
			results = append(results, res)
			continue
		}
		c.mu.Lock()
		existing, exists := oa.lookupMerged(name)
		if err := c.beginLocked(oa); err != nil {
			c.mu.Unlock()
			return results, err
		}
		c.mu.Unlock()
		if exists {
			switch policy {
			case PolicySkip:
				res.Outcome = "skipped"
				results = append(results, res)
				done += uint64(sizes[i])
				o.progress(done, total, "adding")
				continue
			case PolicyKeepBoth:
				n2, ok := keepBothName(name, func(cand string) bool {
					c.mu.Lock()
					_, taken := oa.lookupMerged(cand)
					c.mu.Unlock()
					return taken
				})
				if !ok {
					res.Outcome, res.Code = "failed", CodeFileName
					results = append(results, res)
					continue
				}
				name = n2
				exists = false
			}
		}
		f, err := os.Open(it.src)
		if err != nil {
			res.Outcome, res.Code = "failed", CodeIO
			results = append(results, res)
			continue
		}
		var info archive.FileInfo
		if exists && policy == PolicyReplace {
			info, err = oa.tx.Replace(ctx, existing.ID, f, sizes[i])
		} else {
			info, err = oa.tx.Add(ctx, name, f, sizes[i])
		}
		f.Close()
		if err != nil {
			if ctx.Err() != nil {
				return results, ctx.Err()
			}
			res.Outcome, res.Code = "failed", classify(err).Code
			results = append(results, res)
			continue
		}
		c.mu.Lock()
		if exists && policy == PolicyReplace {
			oa.stageReplace(info)
			res.Outcome = "replaced"
		} else {
			p := &pendingChange{kind: "added", name: name, info: info}
			oa.overlay[info.ID] = p
			oa.adds = append(oa.adds, p)
			res.Outcome = "added"
		}
		oa.seq++
		c.touchArchiveLocked(oa)
		c.mu.Unlock()
		results = append(results, res)
		done += uint64(sizes[i])
		o.progress(done, total, "adding")
	}
	c.mu.Lock()
	c.settleLocked(oa) // nothing staged after all: not dirty
	c.mu.Unlock()
	c.emitArchiveChanged(oa)
	c.emitState()
	return results, nil
}

// keepBothName inserts " (2)", " (3)", … before the extension until the
// name is free, up to a bound, validating each candidate.
func keepBothName(name string, taken func(string) bool) (string, bool) {
	dir, base := "", name
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		dir, base = name[:i+1], name[i+1:]
	}
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for n := 2; n < 1000; n++ {
		cand := fmt.Sprintf("%s%s (%d)%s", dir, stem, n, ext)
		if format.ValidateFileName(cand) != nil {
			return "", false
		}
		if !taken(cand) {
			return cand, true
		}
	}
	return "", false
}

// ReplaceFile is the in-place edit: the file's content from src, same id.
func (c *Core) ReplaceFile(id, fileID, src string) (string, *Error) {
	oa, e := c.findArchive(id)
	if e != nil {
		return "", e
	}
	fid, ok := parseID(fileID)
	if !ok {
		return "", coded(CodeParams)
	}
	return c.startOp("replace", id, func(ctx context.Context, o *op) ([]FileOutcome, error) {
		oa.opMu.Lock()
		defer oa.opMu.Unlock()
		st, err := os.Stat(src)
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		cur, found := oa.currentInfo(fid)
		if !found {
			c.mu.Unlock()
			return nil, archive.ErrNotFound
		}
		if err := c.beginLocked(oa); err != nil {
			c.mu.Unlock()
			return nil, err
		}
		c.mu.Unlock()
		f, err := os.Open(src)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		o.progress(0, uint64(st.Size()), "replacing")
		info, err := oa.tx.Replace(ctx, fid, f, st.Size())
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		oa.stageReplace(info)
		oa.seq++
		c.touchArchiveLocked(oa)
		c.mu.Unlock()
		o.progress(uint64(st.Size()), uint64(st.Size()), "replacing")
		c.emitArchiveChanged(oa)
		c.emitState()
		return []FileOutcome{{Name: cur.Name, Path: src, Outcome: "replaced"}}, nil
	}), nil
}

// ExtractPolicy is what happens when a destination exists.
type ExtractPolicy string

const (
	ExtractSkip   ExtractPolicy = "skip"
	ExtractRename ExtractPolicy = "rename"
)

// Extract writes committed files under dir: target = dir/FromSlash(name),
// parents created, existing targets skipped or renamed (never replaced),
// each file all-or-nothing (trap 17), the batch not.
func (c *Core) Extract(id string, fileIDs []string, dir string, policy ExtractPolicy) (string, *Error) {
	oa, e := c.findArchive(id)
	if e != nil {
		return "", e
	}
	if !filepath.IsAbs(dir) {
		return "", coded(CodeParams)
	}
	ids := make([][16]byte, 0, len(fileIDs))
	for _, s := range fileIDs {
		fid, ok := parseID(s)
		if !ok {
			return "", coded(CodeParams)
		}
		ids = append(ids, fid)
	}
	if policy == "" {
		policy = ExtractSkip
	}
	return c.startOp("extract", id, func(ctx context.Context, o *op) ([]FileOutcome, error) {
		// Plan: resolve every destination first, de-duplicating
		// case-folded collisions inside the batch, before the first byte.
		type item struct {
			fid  [16]byte
			name string
			dst  string
			size uint64
		}
		c.mu.Lock()
		var items []item
		var total uint64
		for _, fid := range ids {
			info, ok := oa.currentInfo(fid)
			if !ok {
				continue
			}
			if k := oa.pendingKind(fid); k == "added" || k == "replaced" {
				continue // not previewable/extractable until Save
			}
			items = append(items, item{fid: fid, name: info.Name, size: info.Size})
			total += info.Size
		}
		c.mu.Unlock()
		if len(items) == 0 {
			return nil, coded(CodeFileNotFound)
		}
		used := map[string]bool{}
		root := filepath.Clean(dir)
		for i := range items {
			dst := filepath.Join(root, filepath.FromSlash(items[i].name))
			rel, err := filepath.Rel(root, dst)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return nil, coded(CodeFileName)
			}
			key := strings.ToLower(dst)
			for n := 2; used[key]; n++ {
				dst = renamed(filepath.Join(root, filepath.FromSlash(items[i].name)), n)
				key = strings.ToLower(dst)
			}
			used[key] = true
			items[i].dst = dst
		}
		results := make([]FileOutcome, 0, len(items))
		var done uint64
		o.progress(0, total, "extracting")
		for _, it := range items {
			if ctx.Err() != nil {
				return results, ctx.Err()
			}
			res := FileOutcome{Name: it.name, Path: it.dst}
			if err := os.MkdirAll(filepath.Dir(it.dst), 0o700); err != nil {
				res.Outcome, res.Code = "failed", CodeIO
				results = append(results, res)
				continue
			}
			c.mu.Lock()
			oa.readers++
			c.touchArchiveLocked(oa)
			c.mu.Unlock()
			err := oa.a.ExtractTo(ctx, it.fid, it.dst)
			for n := 2; err != nil && errors.Is(err, os.ErrExist) && policy == ExtractRename && n < 1000; n++ {
				it.dst = renamed(filepath.Join(root, filepath.FromSlash(it.name)), n)
				res.Path = it.dst
				err = oa.a.ExtractTo(ctx, it.fid, it.dst)
			}
			c.mu.Lock()
			oa.readers--
			c.mu.Unlock()
			switch {
			case err == nil:
				res.Outcome = "extracted"
			case errors.Is(err, os.ErrExist):
				res.Outcome = "skipped"
			case ctx.Err() != nil:
				return results, ctx.Err()
			default:
				res.Outcome, res.Code = "failed", classify(err).Code
			}
			results = append(results, res)
			done += it.size
			o.progress(done, total, "extracting")
		}
		return results, nil
	}), nil
}

// renamed inserts " (n)" before the extension of a path.
func renamed(p string, n int) string {
	ext := filepath.Ext(p)
	return fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(p, ext), n, ext)
}

// Save commits the transaction and records the receipt. Gated on the
// session before it starts; one state-mutex section spans the commit and
// the registry write; a receipt the write cannot record is owed.
func (c *Core) Save(id string) (string, *Error) {
	oa, e := c.findArchive(id)
	if e != nil {
		return "", e
	}
	c.mu.Lock()
	if _, e := c.sessionLocked(); e != nil {
		c.mu.Unlock()
		return "", coded(CodeNeedsUnlock)
	}
	c.mu.Unlock()
	return c.startOp("save", id, func(ctx context.Context, o *op) ([]FileOutcome, error) {
		oa.opMu.Lock()
		defer oa.opMu.Unlock()
		o.progress(0, 1, "saving")
		c.mu.Lock()
		tx := oa.tx
		if tx == nil {
			c.mu.Unlock()
			return nil, nil
		}
		if _, e := c.sessionLocked(); e != nil {
			c.mu.Unlock()
			return nil, e
		}
		c.mu.Unlock()
		// The commit — index seal, free map, two syncs — runs under the
		// archive's own mutex only: the state mutex stays short so a lock
		// trigger is never held up by I/O. A lock that lands in between
		// leaves the receipt owed (APP.md §2.3), which the next unlock pays.
		rec, err := tx.Commit(ctx)
		c.mu.Lock()
		if err != nil {
			if errors.Is(err, archive.ErrIndeterminate) {
				oa.state = "needs_reopen"
				c.clearDirtyLocked(oa)
				c.mu.Unlock()
				c.emitArchivesChanged()
				return nil, err
			}
			// Any other failure aborted the transaction (the archive's
			// contract): the staged changes are gone, and the page must not
			// keep showing them as pending.
			n := oa.dirty()
			c.clearDirtyLocked(oa)
			oa.refreshSnapshot()
			oa.seq++
			c.mu.Unlock()
			c.log("save of %s failed, %d changes discarded: %v", oa.name, n, err)
			c.emit(EventVaultWarning, Warning{Code: "archive.changes_discarded"})
			c.emitArchiveChanged(oa)
			c.emitArchivesChanged()
			return nil, err
		}
		c.clearDirtyLocked(oa)
		oa.refreshSnapshot()
		oa.lastSavedAt = rec.WrittenAt
		oa.seq++
		c.recordReceiptLocked(oa, rec, nil)
		c.mu.Unlock()
		o.progress(1, 1, "saved")
		c.emitArchiveChanged(oa)
		c.emitArchivesChanged()
		return nil, nil
	}), nil
}

// recordReceiptLocked writes a receipt to the registry or owes it. Caller
// holds the state mutex.
func (c *Core) recordReceiptLocked(oa *openArchive, rec archive.Receipt, hash *[32]byte) {
	r := owedReceipt{kid: oa.kid, seq: rec.Seq, size: rec.Size, writtenAt: rec.WrittenAt, hash: hash}
	e := c.updateRegistryLocked(func(g *registry) error {
		a := findRecord(g, oa.id)
		if a == nil {
			return nil
		}
		a.LastStoredSize, a.LastWrittenAt, a.LastSeq = r.size, r.writtenAt, r.seq
		a.Revision++
		a.LastWriter = g.DeviceID
		if hash != nil {
			a.LastCiphertextHash, a.HashAtSeq = *hash, r.seq
		}
		return nil
	})
	if e != nil {
		c.owed[oa.id] = r
		oa.receiptOwed = true
		c.log("receipt owed for %s: %v", oa.name, e)
		return
	}
	oa.receiptOwed = false
	oa.copyMismatch = false // the registry now names this copy
	delete(c.owed, oa.id)
}

// Verify re-hashes the file and refreshes the registry's hash (R36).
func (c *Core) Verify(id string) (string, *Error) {
	if e := c.refuseIfForgotten(id); e != nil {
		return "", e
	}
	oa, e := c.findArchive(id)
	if e != nil {
		return "", e
	}
	return c.startOp("verify", id, func(ctx context.Context, o *op) ([]FileOutcome, error) {
		oa.opMu.Lock()
		defer oa.opMu.Unlock()
		c.mu.Lock()
		if oa.tx != nil {
			c.mu.Unlock()
			return nil, coded(CodeArchiveDirty)
		}
		c.mu.Unlock()
		o.progress(0, 1, "hashing")
		h, err := oa.a.Hash(ctx)
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		seq := oa.a.Seq()
		size, _, _ := oa.a.Stat()
		e := c.updateRegistryLocked(func(g *registry) error {
			a := findRecord(g, oa.id)
			if a == nil {
				return nil
			}
			a.LastCiphertextHash, a.HashAtSeq, a.LastSeq, a.LastStoredSize = h, seq, seq, size
			return nil
		})
		if e == nil {
			oa.copyMismatch = false // the registry now names this copy
		}
		c.mu.Unlock()
		if e != nil {
			return nil, e
		}
		o.progress(1, 1, "verified")
		c.emitArchivesChanged()
		return nil, nil
	}), nil
}

// Compact rewrites the archive without free space (APP.md §2.3): refused
// unless open and clean; previews quiesced; the handle is finished by the
// call and the path reopened.
func (c *Core) Compact(id string) (string, *Error) {
	if e := c.refuseIfForgotten(id); e != nil {
		return "", e
	}
	oa, e := c.findArchive(id)
	if e != nil {
		return "", e
	}
	c.mu.Lock()
	if oa.tx != nil {
		c.mu.Unlock()
		return "", coded(CodeArchiveDirty)
	}
	if _, e := c.sessionLocked(); e != nil {
		c.mu.Unlock()
		return "", coded(CodeNeedsUnlock)
	}
	// Fit before the absolute cap: a rough estimate over 200 MB/s.
	size := oa.size
	remaining := c.vault.absoluteAt.Sub(c.now())
	if est := time.Duration(size/200e6) * time.Second; est > remaining {
		c.mu.Unlock()
		return "", coded(CodeTooSlow)
	}
	oa.quiesced = true
	c.mu.Unlock()
	return c.startOp("compact", id, func(ctx context.Context, o *op) ([]FileOutcome, error) {
		oa.opMu.Lock()
		defer oa.opMu.Unlock()
		// Drain previews: wait for outstanding readers, bounded.
		deadline := c.now().Add(30 * time.Second)
		for {
			c.mu.Lock()
			n := oa.readers
			c.mu.Unlock()
			if n == 0 {
				break
			}
			if c.now().After(deadline) {
				c.mu.Lock()
				oa.quiesced = false
				c.mu.Unlock()
				return nil, coded(CodeArchiveBusy)
			}
			time.Sleep(100 * time.Millisecond)
		}
		c.mu.Lock()
		if oa.tx != nil || c.archives[oa.id] != oa {
			// A change was staged between the gate and the operation's
			// turn on the handle: the archive stays as it is.
			oa.quiesced = false
			c.mu.Unlock()
			return nil, coded(CodeArchiveDirty)
		}
		oa.state = "compacting"
		c.mu.Unlock()
		o.progress(0, 1, "compacting")
		hash, newSize, err := oa.a.Compact(ctx, func(done, total uint64) { o.progress(done, total, "compacting") })
		// Whatever happened, this handle is finished: closed (idempotent
		// after a successful compaction) so that a failure never leaves the
		// file locked, and forgotten.
		c.mu.Lock()
		c.closeArchiveLocked(oa)
		if err == nil {
			// The receipt is recorded (or owed) now, before the reopen: a
			// reopen that fails must not lose it. A compacted file starts
			// again at seq 1.
			c.recordReceiptLocked(oa, archive.Receipt{Seq: 1, Size: newSize, WrittenAt: c.now().Unix()}, &hash)
		}
		c.mu.Unlock()
		if err != nil {
			c.emitArchivesChanged()
			c.emitState()
			return nil, err
		}
		// Reopen for the page.
		if _, e := c.OpenArchive(hexID(oa.id)); e != nil {
			return nil, e
		}
		o.progress(1, 1, "compacted")
		c.emitArchivesChanged()
		return nil, nil
	}), nil
}

// RotateKey is registry-first (R33, trap 21): the new version published,
// the old retired, then the archive adopts it, then the receipt.
func (c *Core) RotateKey(id string) (string, *Error) {
	if e := c.refuseIfForgotten(id); e != nil {
		return "", e
	}
	oa, e := c.findArchive(id)
	if e != nil {
		return "", e
	}
	c.mu.Lock()
	if oa.tx != nil {
		c.mu.Unlock()
		return "", coded(CodeArchiveDirty)
	}
	if _, e := c.sessionLocked(); e != nil {
		c.mu.Unlock()
		return "", coded(CodeNeedsUnlock)
	}
	c.mu.Unlock()
	return c.startOp("rotate", id, func(ctx context.Context, o *op) ([]FileOutcome, error) {
		oa.opMu.Lock()
		defer oa.opMu.Unlock()
		var key [32]byte
		var kid [16]byte
		if _, err := rand.Read(key[:]); err != nil {
			return nil, err
		}
		if _, err := rand.Read(kid[:]); err != nil {
			return nil, err
		}
		defer kdf.Zero(key[:])
		o.progress(0, 3, "registry")
		c.mu.Lock()
		if oa.tx != nil {
			// Staged between the gate and the operation's turn.
			c.mu.Unlock()
			return nil, coded(CodeArchiveDirty)
		}
		sess, e := c.sessionLocked()
		if e != nil {
			c.mu.Unlock()
			return nil, e
		}
		wrapped, nonce, err := sess.WrapArchiveKey(oa.id, kid, key)
		if err != nil {
			c.mu.Unlock()
			return nil, err
		}
		now := c.now().Unix()
		e = c.updateRegistryLocked(func(g *registry) error {
			a := findRecord(g, oa.id)
			if a == nil {
				return errors.New("archive record missing")
			}
			for i := range a.Versions {
				if a.Versions[i].State == format.VersionCurrent {
					a.Versions[i].State, a.Versions[i].RetiredAt = format.VersionRetired, now
				}
			}
			a.Versions = append(a.Versions, format.VersionRecord{KID: kid, WrappedArchiveKey: wrapped, WrapNonce: nonce, CreatedAt: now, State: format.VersionCurrent})
			a.CurrentKID = kid
			return nil
		})
		c.mu.Unlock()
		if e != nil {
			return nil, e
		}
		o.progress(1, 3, "archive")
		rec, err := oa.a.RotateKey(ctx, kid, key)
		if err != nil {
			// The registry already holds the new version; the archive is
			// still under the old key (the next open unwraps both). Only a
			// rotation that reached the file changes what this handle says;
			// an unknown outcome leaves the handle for a reopen.
			stale := oa.a.EnvelopeStale()
			c.mu.Lock()
			if stale {
				c.vault.warnings["archive.envelope_stale"] = true
			}
			if errors.Is(err, archive.ErrIndeterminate) {
				oa.state = "needs_reopen"
			}
			if rec.Seq != 0 {
				oa.kid, oa.keyVersion = kid, oa.keyVersion+1
				c.recordReceiptLocked(oa, rec, nil)
			}
			c.mu.Unlock()
			c.emitArchiveChanged(oa)
			c.emitArchivesChanged()
			return nil, err
		}
		c.mu.Lock()
		oa.kid = kid
		oa.keyVersion++
		oa.refreshSnapshot()
		c.recordReceiptLocked(oa, rec, nil)
		c.mu.Unlock()
		o.progress(3, 3, "rotated")
		c.emitArchivesChanged()
		return nil, nil
	}), nil
}

// CreateArchive makes a new archive file and its registry record. method
// is the compression the archive is created with — store · fastest ·
// normal · better · best — written into the record's policy (FORMAT.md
// §7.1 bits 2–5) so that every later writer of it, on any machine,
// compresses the same way; anything else is params. A path where a file
// already exists is refused with archive.exists: the archive layer
// creates with O_EXCL and Enfold never overwrites a file it did not make
// (APP.md §6). The folder is remembered for the next create's dialog.
func (c *Core) CreateArchive(p, name, method string) (string, *Error) {
	if !filepath.IsAbs(p) {
		return "", coded(CodeParams)
	}
	// The same rule a rename applies (records.go, FORMAT §7.1): the name is the
	// trusted one and the typed consent for Forget and Delete, so it is bounded
	// before a file exists for it.
	if name == "" || len(name) > maxNameLen || !utf8.ValidString(name) {
		return "", coded(CodeArchiveName)
	}
	policy, ok := policyForMethod(method)
	if !ok {
		return "", coded(CodeParams)
	}
	var id, kid [16]byte
	var key [32]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", c.fail("create", err)
	}
	if _, err := rand.Read(kid[:]); err != nil {
		return "", c.fail("create", err)
	}
	if _, err := rand.Read(key[:]); err != nil {
		return "", c.fail("create", err)
	}
	defer kdf.Zero(key[:])
	c.mu.Lock()
	sess, e := c.sessionLocked()
	if e != nil {
		c.mu.Unlock()
		return "", e
	}
	wrapped, nonce, err := sess.WrapArchiveKey(id, kid, key)
	if err != nil {
		c.mu.Unlock()
		return "", c.fail("wrap", err)
	}
	opts := archive.Options{
		DeviceID:      sess.Registry().DeviceID,
		NoCompression: policy&format.PolicyNoCompression != 0,
		Compress:      compress.Params{Level: compressLevelOf(policy)},
		DictBelow:     c.settings.DictionaryBelow,
	}
	c.mu.Unlock()
	a, err := archive.Create(p, id, kid, key, opts)
	if err != nil {
		if os.IsExist(err) {
			// O_EXCL: something is there already, and Enfold never writes
			// over a file it did not make (DESIGN.md trap 28).
			return "", coded(CodeArchiveExists)
		}
		return "", c.fail("create archive", err)
	}
	seq := a.Seq()
	size, _, _ := a.Stat()
	a.Close()
	now := c.now().Unix()
	if e := c.updateRegistry(func(g *registry) error {
		g.Archives = append(g.Archives, format.ArchiveRecord{
			ArchiveID: id, Name: name, LastPath: p, Policy: policy, CreatedAt: now, CurrentKID: kid,
			LastStoredSize: size, LastWrittenAt: now, LastSeq: seq, Revision: 1, LastWriter: g.DeviceID,
			Versions: []format.VersionRecord{{KID: kid, WrappedArchiveKey: wrapped, WrapNonce: nonce, CreatedAt: now, State: format.VersionCurrent}},
		})
		return nil
	}); e != nil {
		os.Remove(p)
		return "", e
	}
	c.rememberArchiveFolder(filepath.Dir(p))
	c.emitArchivesChanged()
	return hexID(id), nil
}

// rememberArchiveFolder records where the last archive was made, so that
// the next New archive dialog opens there (APP.md §6, settings.json's
// lastArchiveFolder). A convenience: a folder that could not be written
// down is logged and nothing else — the archive is made either way.
func (c *Core) rememberArchiveFolder(dir string) {
	c.mu.Lock()
	if c.settings.LastArchiveFolder == dir {
		c.mu.Unlock()
		return
	}
	c.settings.LastArchiveFolder = dir
	file := c.settings
	c.mu.Unlock()
	if err := saveSettings(c.deps.DataDir, file); err != nil {
		c.log("settings: recording the archive folder: %v", err)
	}
}

// HideArchive and UnhideArchive flip the hidden policy bit.
func (c *Core) HideArchive(id string, hidden bool) *Error {
	aid, ok := parseID(id)
	if !ok {
		return coded(CodeParams)
	}
	e := c.updateRegistry(func(g *registry) error {
		a := findRecord(g, aid)
		if a == nil {
			return coded(CodeArchiveNotFound)
		}
		if a.Forgotten() {
			return coded(CodeArchiveForgotten)
		}
		if hidden {
			a.Policy |= format.PolicyHidden
		} else {
			a.Policy &^= format.PolicyHidden
		}
		return nil
	})
	if e == nil {
		c.emitArchivesChanged()
	}
	return e
}

// Locate records where a moved archive file is now.
func (c *Core) Locate(id, newPath string) *Error {
	aid, ok := parseID(id)
	if !ok || !filepath.IsAbs(newPath) {
		return coded(CodeParams)
	}
	if _, err := os.Stat(newPath); err != nil {
		return coded(CodeArchiveMissing)
	}
	e := c.updateRegistry(func(g *registry) error {
		a := findRecord(g, aid)
		if a == nil {
			return coded(CodeArchiveNotFound)
		}
		if a.Forgotten() {
			return coded(CodeArchiveForgotten)
		}
		a.LastPath = newPath
		return nil
	})
	if e == nil {
		c.emitArchivesChanged()
	}
	return e
}

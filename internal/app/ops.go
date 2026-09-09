package app

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
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
// done is closed when it ends, so a caller that cancelled one can wait for
// the transaction to be aborted before it closes the archive.
type op struct {
	id        string
	kind      string
	archiveID string
	startedAt time.Time
	done      atomic.Uint64
	total     atomic.Uint64
	phase     atomic.Value // string
	cancel    context.CancelFunc
	over      chan struct{}
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

// countingReaderAt reports how far a write has read into its source, so that
// the bar moves inside one large file (APP.md §3: progress is by bytes, not
// by file). What it reports is a front, not a running sum: a file is read
// more than once — the storage plan samples it before the seal reads it
// whole — and it must be counted once.
type countingReaderAt struct {
	src  io.ReaderAt
	size int64
	seen atomic.Int64
	on   func(read int64)
}

func (r *countingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	n, err := r.src.ReadAt(p, off)
	if n > 0 {
		r.advance(off, int64(n))
	}
	return n, err
}

// advance moves the front when the read continues it. A read that starts
// beyond what has been read is the compression probe sampling the middle or
// the end of the file (DESIGN.md §9), not progress through it, and moves
// nothing; and a read past the end — the archive layer's one-byte check that
// the source ends where it said — stops at size.
func (r *countingReaderAt) advance(off, n int64) {
	for {
		seen := r.seen.Load()
		if off > seen {
			return
		}
		end := off + n
		if end > r.size {
			end = r.size
		}
		if end <= seen {
			return
		}
		if r.seen.CompareAndSwap(seen, end) {
			if r.on != nil {
				r.on(end)
			}
			return
		}
	}
}

// startOp registers an operation and runs fn on its own goroutine.
func (c *Core) startOp(kind, archiveID string, fn func(ctx context.Context, o *op) ([]FileOutcome, error)) string {
	ctx, cancel := context.WithCancel(context.Background())
	o := &op{id: randomID(), kind: kind, archiveID: archiveID, startedAt: c.now(), cancel: cancel, over: make(chan struct{}), c: c}
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
	if o.finished { // a panic after a finish: the first outcome stands
		c.mu.Unlock()
		return
	}
	o.finished, o.results, o.err = true, results, e
	v := o.view()
	c.mu.Unlock()
	close(o.over)
	c.emit(EventOpDone, v)
	c.emitState()
	// Keep finished ops for a while so a rebuilt window sees the outcome.
	c.deps.Clock.AfterFunc(5*time.Minute, func() {
		c.mu.Lock()
		delete(c.ops, o.id)
		c.mu.Unlock()
	})
}

// CancelOp cancels a running operation. On a running add or replace this is
// Abort: nothing is published (APP.md §2.3).
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

// AddPolicy is what happens when an incoming item and the item in the way
// are the same kind (APP.md §3). Kinds that differ never replace.
type AddPolicy string

const (
	PolicySkip     AddPolicy = "skip"
	PolicyReplace  AddPolicy = "replace"
	PolicyKeepBoth AddPolicy = "keep-both"
)

// addNode is one item an add's walk found: a file to store or a folder to
// make, with what is beneath it. A folder is a node of its own — never a
// prefix of a name — so an empty subfolder and every folder's modified time
// survive (FORMAT.md R39, DESIGN.md trap 31).
type addNode struct {
	src        string
	name       string // one path element
	isDir      bool
	modifiedAt int64
	size       int64
	children   []*addNode
	refuse     Code // the source itself could not be used
}

// planItem is what the pre-flight decided for one node, before the first
// Tx.Add (APP.md §3): the whole batch is resolved — names, kinds, the folded
// matches and R39's bounds — so the user is asked once and no source is
// silently skipped.
type planItem struct {
	node   *addNode
	action string // create | enter | add | replace | skip | fail
	code   Code
	name   string // the name it takes in the archive
	joined string // its joined path there
	// Its destination: an existing directory (or the root), or a folder
	// this batch creates, whose id is known only once it is written.
	parentID   [16]byte
	parentPlan *planItem
	// The record in the way, for enter and replace.
	existing     [16]byte
	existingPlan *planItem
	children     []*planItem
	id           [16]byte // filled in as the item is written
	written      bool
}

// addDest is where a run of nodes goes: the destination's identity, its
// depth and path prefix for R39's bounds, its joined path, and what this
// batch has already planned into it.
type addDest struct {
	id      [16]byte
	plan    *planItem
	depth   int
	prefix  int
	joined  string
	planned []*planItem
}

// AddFiles adds files under parentID in one transaction, committed at its
// end (APP.md §2.3). Every name is one element, validated with
// format.ValidateName and matched case folded against the live children of
// its parent (APP.md §3).
func (c *Core) AddFiles(id, parentID string, paths []string, policy AddPolicy) (string, *Error) {
	oa, pid, e := c.addTarget(id, parentID)
	if e != nil {
		return "", e
	}
	if len(paths) == 0 {
		return "", coded(CodeParams)
	}
	srcs := append([]string(nil), paths...)
	return c.startOp("add", id, func(ctx context.Context, o *op) ([]FileOutcome, error) {
		nodes := make([]*addNode, 0, len(srcs))
		for _, src := range srcs {
			n := &addNode{src: src, name: sourceName(src)}
			st, err := os.Stat(src)
			switch {
			case err != nil:
				n.refuse = CodeIO
			case st.IsDir():
				// A folder is AddFolder's call: it is a record with its own
				// time and a subtree to walk, not a file to store.
				n.refuse = CodeParams
				n.isDir = true
			default:
				n.size, n.modifiedAt = st.Size(), st.ModTime().Unix()
			}
			nodes = append(nodes, n)
		}
		return c.addTree(ctx, o, oa, pid, nodes, policy)
	}), nil
}

// AddFolder adds a directory tree under parentID in one transaction. Every
// directory the walk creates becomes a record with its own time; an existing
// directory of that name is entered whatever the policy, so one source
// folder is never split across two records (APP.md §3).
func (c *Core) AddFolder(id, parentID, dir string, policy AddPolicy) (string, *Error) {
	oa, pid, e := c.addTarget(id, parentID)
	if e != nil {
		return "", e
	}
	return c.startOp("add", id, func(ctx context.Context, o *op) ([]FileOutcome, error) {
		root, err := walkSource(ctx, dir)
		if err != nil {
			return nil, err
		}
		return c.addTree(ctx, o, oa, pid, []*addNode{root}, policy)
	}), nil
}

// addTarget resolves the archive and the destination folder: the core
// validates that the archive is open and that the id is the root or a live
// directory, and refuses loudly (APP.md §3, the file drop's data-dir-id).
func (c *Core) addTarget(id, parentID string) (*openArchive, [16]byte, *Error) {
	oa, e := c.findArchive(id)
	if e != nil {
		return nil, [16]byte{}, e
	}
	pid, ok := parseID(parentID)
	if !ok {
		return nil, [16]byte{}, coded(CodeParams)
	}
	c.mu.Lock()
	usable := oa.merge().dirUsable(pid)
	c.mu.Unlock()
	if !usable {
		return nil, [16]byte{}, coded(CodeFileNotFound)
	}
	return oa, pid, nil
}

// sourceName is the leaf of a source path, as the record's name.
func sourceName(p string) string {
	return path.Base(filepath.ToSlash(strings.TrimRight(p, `\/`)))
}

// walkSource builds the tree of records an AddFolder would make. A source
// that cannot be read is one node with its refusal, so the results name it
// rather than the operation failing.
func walkSource(ctx context.Context, dir string) (*addNode, error) {
	st, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	root := &addNode{src: dir, name: sourceName(dir), isDir: st.IsDir(), modifiedAt: st.ModTime().Unix()}
	if !root.isDir {
		root.size = st.Size()
		return root, nil
	}
	top := filepath.Clean(dir)
	byPath := map[string]*addNode{top: root}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		p = filepath.Clean(p)
		if p == top {
			return walkErr
		}
		parent := byPath[filepath.Dir(p)]
		if parent == nil {
			return nil // its folder was refused: nothing beneath it
		}
		n := &addNode{src: p, name: filepath.Base(p), isDir: d != nil && d.IsDir()}
		parent.children = append(parent.children, n)
		if walkErr != nil {
			n.refuse = CodeIO
			if n.isDir {
				return fs.SkipDir
			}
			return nil
		}
		if info, err := d.Info(); err != nil {
			n.refuse = CodeIO
		} else {
			n.modifiedAt = info.ModTime().Unix()
			if !n.isDir {
				n.size = info.Size()
			}
		}
		if n.isDir {
			byPath[p] = n
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return root, nil
}

// addTree pre-flights the whole walk against the committed view and then
// runs it inside one transaction: nothing is written until every item has
// been decided, and the commit at the end is what publishes any of it. An
// add that wrote nothing after all — every item skipped or refused —
// publishes nothing rather than committing an empty change.
func (c *Core) addTree(ctx context.Context, o *op, oa *openArchive, parentID [16]byte, nodes []*addNode, policy AddPolicy) ([]FileOutcome, error) {
	oa.opMu.Lock()
	defer oa.opMu.Unlock()
	if policy == "" {
		policy = PolicySkip
	}
	c.mu.Lock()
	m := oa.merge()
	if !m.dirUsable(parentID) {
		c.mu.Unlock()
		return nil, coded(CodeFileNotFound)
	}
	depth, prefix, _ := m.dirPos(parentID)
	d := &addDest{id: parentID, depth: depth, prefix: prefix, joined: m.path(parentID)}
	plan := planNodes(m, d, nodes, policy)
	c.mu.Unlock()
	var total uint64
	var count func(items []*planItem)
	count = func(items []*planItem) {
		for _, it := range items {
			if (it.action == "add" || it.action == "replace") && it.node.size > 0 {
				total += uint64(it.node.size)
			}
			count(it.children)
		}
	}
	count(plan)
	o.progress(0, total, "adding")
	tx, e := c.beginOp(oa)
	if e != nil {
		return nil, e
	}
	results := []FileOutcome{}
	var done uint64
	wrote := false
	err := c.runPlan(ctx, o, oa, tx, plan, &results, &done, total, &wrote)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil || !wrote {
		// Cancelled, failed, or nothing to write after all: the transaction
		// is dropped and nothing is published (APP.md §2.3).
		c.abortOp(oa, tx)
		return results, err
	}
	// The commit is not cancellable: the writing is done, and a Cancel that
	// arrives now would leave the outcome to a race rather than to the user.
	if e := c.commitOp(context.WithoutCancel(ctx), oa, tx); e != nil {
		return results, e
	}
	return results, nil
}

// planNodes decides one run of siblings and, for a folder that is created or
// entered, everything beneath it.
func planNodes(m *merged, d *addDest, nodes []*addNode, policy AddPolicy) []*planItem {
	out := make([]*planItem, 0, len(nodes))
	for _, n := range nodes {
		it := &planItem{node: n, name: n.name, parentID: d.id, parentPlan: d.plan}
		out = append(out, it)
		if n.refuse != "" {
			it.action, it.code = "fail", n.refuse
			continue
		}
		if format.ValidateName(n.name) != nil {
			// Never silently skipped: one outcome, named. When the refused
			// name is a directory's, nothing beneath it is added and the
			// walk goes on with the folder's siblings.
			it.action, it.code = "fail", CodeFileName
			continue
		}
		rec, pl := inTheWay(m, d, n.name)
		switch {
		case rec == nil && pl == nil:
			it.action = "create"
			if !n.isDir {
				it.action = "add"
			}
		case n.isDir && kindOf(rec, pl):
			// Two directories: the incoming one is entered whatever the
			// policy, keeping its dir_id and its own modified_at.
			it.action = "enter"
			if rec != nil {
				it.existing = rec.id
			} else {
				it.existingPlan = pl
			}
		case n.isDir != kindOf(rec, pl):
			// Kinds that differ never replace, in either direction.
			switch policy {
			case PolicyKeepBoth:
				it.action, it.name = keepBoth(m, d, n.name, n.isDir)
			case PolicyReplace:
				it.action, it.code = "fail", CodeKindMismatch
			default:
				it.action = "skip"
			}
		default: // two files
			switch policy {
			case PolicyKeepBoth:
				it.action, it.name = keepBoth(m, d, n.name, n.isDir)
			case PolicyReplace:
				it.action = "replace"
				if rec != nil {
					it.existing = rec.id
				} else {
					it.existingPlan = pl
				}
			default:
				it.action = "skip"
			}
		}
		if it.action == "create" || it.action == "add" {
			if e := boundsAt(m, d.depth, d.prefix, n.isDir, it.name, nil); e != nil {
				it.action, it.code = "fail", e.Code
			}
		}
		it.joined = join(d.joined, it.name)
		if it.action == "fail" || it.action == "skip" {
			continue
		}
		d.planned = append(d.planned, it)
		if !n.isDir || len(n.children) == 0 {
			continue
		}
		sub := &addDest{}
		switch {
		case it.action == "enter" && it.existingPlan == nil:
			ex := m.rec(it.existing)
			sub.id = ex.id
			sub.depth, sub.prefix, _ = m.dirPos(ex.id)
			sub.joined = m.path(ex.id)
		case it.action == "enter":
			sub.plan = it.existingPlan
			sub.depth, sub.prefix = d.depth+1, d.prefix+len(it.existingPlan.name)+1
			sub.joined = it.existingPlan.joined
		default: // created
			sub.plan = it
			sub.depth, sub.prefix = d.depth+1, d.prefix+len(it.name)+1
			sub.joined = it.joined
		}
		it.children = planNodes(m, sub, n.children, policy)
	}
	return out
}

// inTheWay is the live child of the destination whose name folds onto name,
// or the item this batch has already planned there.
func inTheWay(m *merged, d *addDest, name string) (*mergedRec, *planItem) {
	if d.plan == nil {
		if r := m.sibling(d.id, name, [16]byte{}); r != nil {
			return r, nil
		}
	}
	for _, p := range d.planned {
		if strings.EqualFold(p.name, name) {
			return nil, p
		}
	}
	return nil, nil
}

// kindOf reports whether the item in the way is a directory.
func kindOf(rec *mergedRec, pl *planItem) bool {
	if rec != nil {
		return rec.isDir
	}
	return pl.node.isDir
}

// keepBoth takes the next free name — name (2), name (3), … , before the
// extension for a file and at the end of the whole name for a directory —
// the first that no live sibling holds under case folding.
func keepBoth(m *merged, d *addDest, name string, isDir bool) (action, chosen string) {
	stem, ext := name, ""
	if !isDir {
		ext = path.Ext(name)
		stem = strings.TrimSuffix(name, ext)
	}
	for n := 2; n < 1000; n++ {
		cand := fmt.Sprintf("%s (%d)%s", stem, n, ext)
		if format.ValidateName(cand) != nil {
			break
		}
		if rec, pl := inTheWay(m, d, cand); rec == nil && pl == nil {
			if isDir {
				return "create", cand
			}
			return "add", cand
		}
	}
	return "fail", name
}

func join(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

// runPlan writes the pre-flighted walk into the transaction, parents before
// children. A folder that could not be written takes its subtree with it:
// one outcome for the folder and nothing beneath it. wrote says whether the
// transaction holds anything, so an add that changed nothing commits
// nothing.
func (c *Core) runPlan(ctx context.Context, o *op, oa *openArchive, tx *archive.Tx, items []*planItem, results *[]FileOutcome, done *uint64, total uint64, wrote *bool) error {
	for _, it := range items {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		res := FileOutcome{Path: it.node.src, Name: it.joined, IsDir: it.node.isDir}
		parent := it.parentID
		if it.parentPlan != nil {
			parent = it.parentPlan.id
		}
		switch it.action {
		case "fail":
			res.Outcome, res.Code = "failed", it.code
			if it.code == "" {
				res.Code = CodeFileName
			}
			*results = append(*results, res)
			continue
		case "skip":
			res.Outcome = "skipped"
			*results = append(*results, res)
			continue
		case "enter":
			it.id, it.written = it.existing, true
			if it.existingPlan != nil {
				it.id = it.existingPlan.id
				it.written = it.existingPlan.written
			}
			res.Outcome = "entered"
			*results = append(*results, res)
		case "create":
			info, err := tx.AddDir(parent, it.name, it.node.modifiedAt)
			if err != nil {
				res.Outcome, res.Code = "failed", classify(err).Code
				*results = append(*results, res)
				continue // nothing beneath a folder that was not made
			}
			it.id, it.written, *wrote = info.ID, true, true
			res.Outcome = "created"
			*results = append(*results, res)
		case "add", "replace":
			err := c.addFile(ctx, o, tx, it, parent, &res, *done, total)
			if res.Outcome == "added" || res.Outcome == "replaced" {
				*wrote = true
			}
			*results = append(*results, res)
			if err != nil {
				return err
			}
			*done += uint64(max64(it.node.size, 0))
			o.progress(*done, total, "adding")
			continue
		}
		if !it.written && it.action == "enter" {
			continue // the folder it would have entered was not made
		}
		if err := c.runPlan(ctx, o, oa, tx, it.children, results, done, total, wrote); err != nil {
			return err
		}
	}
	return nil
}

// addFile writes one file into the transaction: an add of a fresh record, or
// the in-place edit of the record in the way. Its bytes are counted as they
// are read, so the bar moves inside one large file (APP.md §3). A source
// that changed underneath between the walk and the write is one outcome, not
// a failed operation.
func (c *Core) addFile(ctx context.Context, o *op, tx *archive.Tx, it *planItem, parent [16]byte, res *FileOutcome, base, total uint64) error {
	target := it.existing
	if it.existingPlan != nil {
		if !it.existingPlan.written {
			res.Outcome, res.Code = "failed", CodeFileNotFound
			return nil
		}
		target = it.existingPlan.id
	}
	f, err := os.Open(it.node.src)
	if err != nil {
		res.Outcome, res.Code = "failed", CodeIO
		return nil
	}
	defer f.Close()
	src := &countingReaderAt{src: f, size: it.node.size, on: func(read int64) {
		o.progress(base+uint64(read), total, "adding")
	}}
	var info archive.FileInfo
	if it.action == "replace" {
		info, err = tx.Replace(ctx, target, src, it.node.size)
	} else {
		info, err = tx.Add(ctx, parent, it.name, src, it.node.size)
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		res.Outcome, res.Code = "failed", classify(err).Code
		return nil
	}
	it.id, it.written = info.ID, true
	if it.action == "replace" {
		res.Outcome = "replaced"
	} else {
		res.Outcome = "added"
	}
	return nil
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// ReplaceFile is the in-place edit: the file's content from src, same id,
// its own transaction (APP.md §2.3). Cancel aborts it.
func (c *Core) ReplaceFile(id, fileID, src string) (string, *Error) {
	oa, e := c.findArchive(id)
	if e != nil {
		return "", e
	}
	fid, ok := parseID(fileID)
	if !ok || fid == format.RootID {
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
		m := oa.merge()
		cur := m.live(fid)
		if cur == nil || cur.isDir {
			c.mu.Unlock()
			return nil, archive.ErrNotFound
		}
		name := m.path(fid)
		c.mu.Unlock()
		f, err := os.Open(src)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		total := uint64(max64(st.Size(), 0))
		o.progress(0, total, "replacing")
		tx, e := c.beginOp(oa)
		if e != nil {
			return nil, e
		}
		rd := &countingReaderAt{src: f, size: st.Size(), on: func(read int64) {
			o.progress(uint64(read), total, "replacing")
		}}
		if _, err := tx.Replace(ctx, fid, rd, st.Size()); err != nil {
			c.abortOp(oa, tx)
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			c.abortOp(oa, tx)
			return nil, err
		}
		if e := c.commitOp(context.WithoutCancel(ctx), oa, tx); e != nil {
			return nil, e
		}
		o.progress(total, total, "replacing")
		return []FileOutcome{{Name: name, Path: src, Outcome: "replaced"}}, nil
	}), nil
}

// ExtractPolicy is what happens when a destination file exists.
type ExtractPolicy string

const (
	ExtractSkip   ExtractPolicy = "skip"
	ExtractRename ExtractPolicy = "rename"
)

// extractItem is one record of the plan, resolved before the first byte.
type extractItem struct {
	id         [16]byte
	parentID   [16]byte
	isDir      bool
	path       string // the joined archive path
	dst        string
	size       uint64
	modifiedAt int64
	depth      int
}

// Extract writes a plan of records under dir (APP.md §3). The plan is a
// set — each selected record, every live record beneath a selected
// directory, and the ancestor directories of all of them up to the root —
// ordered parents before anything under them, and a directory is in it
// because its record is live, never because a file needed a parent (DESIGN
// trap 31): an empty folder extracts as an empty folder. The all-zero id
// among recordIDs is the root and extracts everything; an empty recordIDs is
// params, never everything. The destination is created if it does not exist
// and remembered as settings.json's lastExtractFolder, which the extract
// dialog prefills next time.
func (c *Core) Extract(id string, recordIDs []string, dir string, policy ExtractPolicy) (string, *Error) {
	oa, e := c.findArchive(id)
	if e != nil {
		return "", e
	}
	if !filepath.IsAbs(dir) {
		return "", coded(CodeParams)
	}
	if len(recordIDs) == 0 {
		return "", coded(CodeParams)
	}
	all := false
	ids := make([][16]byte, 0, len(recordIDs))
	for _, s := range recordIDs {
		rid, ok := parseID(s)
		if !ok {
			return "", coded(CodeParams)
		}
		if rid == format.RootID {
			all = true
			continue
		}
		ids = append(ids, rid)
	}
	if policy == "" {
		policy = ExtractSkip
	}
	root := filepath.Clean(dir)
	return c.startOp("extract", id, func(ctx context.Context, o *op) ([]FileOutcome, error) {
		c.mu.Lock()
		items, e := extractPlan(oa.merge(), ids, all)
		c.mu.Unlock()
		if e != nil {
			return nil, e
		}
		if len(items) == 0 {
			return nil, coded(CodeFileNotFound)
		}
		// Every target is resolved before the first byte, case-folded
		// destinations de-duplicated, each asserted to lie under dir. Every
		// record satisfying R20 and R39 does, so a failure means the index
		// is not the one the reader validated: the whole operation fails
		// with file.name, a plan-time invariant and not an item's outcome.
		used := make(map[string][16]byte, len(items))
		var total uint64
		for i := range items {
			it := &items[i]
			it.dst = filepath.Join(root, filepath.FromSlash(it.path))
			rel, err := filepath.Rel(root, filepath.Clean(it.dst))
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return nil, coded(CodeFileName)
			}
			key := strings.ToLower(it.dst)
			if other, dup := used[key]; dup && other != it.id {
				return nil, coded(CodeFileName)
			}
			used[key] = it.id
			if !it.isDir {
				total += it.size
			}
		}
		if err := os.MkdirAll(root, 0o700); err != nil {
			return nil, err
		}
		c.rememberFolder(extractFolder, root)
		results := make([]FileOutcome, 0, len(items))
		at := make(map[[16]byte]int, len(items))
		gone := map[[16]byte]bool{}
		var made []extractItem
		var done uint64
		o.progress(0, total, "extracting")
		for _, it := range items {
			if ctx.Err() != nil {
				return results, ctx.Err()
			}
			res := FileOutcome{Name: it.path, Path: it.dst, IsDir: it.isDir}
			at[it.id] = len(results)
			if gone[it.parentID] {
				// A directory that cannot be created takes its subtree with
				// it, each record beneath it failing in turn.
				res.Outcome, res.Code = "failed", CodeIO
				gone[it.id] = true
				results = append(results, res)
				continue
			}
			if it.isDir {
				// Created into the parent the order has already made; an
				// existing folder is used as it stands — never renamed,
				// never pre-Lstat'ed, never emptied.
				err := os.Mkdir(it.dst, 0o700)
				switch {
				case err == nil:
					res.Outcome = "created"
					made = append(made, it)
				case errors.Is(err, os.ErrExist):
					res.Outcome = "skipped"
				default:
					res.Outcome, res.Code = "failed", CodeIO
					gone[it.id] = true
				}
				results = append(results, res)
				continue
			}
			c.mu.Lock()
			oa.readers++
			c.touchArchiveLocked(oa)
			c.mu.Unlock()
			base := done
			count := func(written uint64) { o.progress(base+written, total, "extracting") }
			err := extractFile(ctx, oa.a, it.id, it.dst, count)
			for n := 2; err != nil && errors.Is(err, os.ErrExist) && policy == ExtractRename && n < 1000; n++ {
				res.Path = renamed(it.dst, n)
				err = extractFile(ctx, oa.a, it.id, res.Path, count)
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
		// Each directory this extraction created then takes its modified_at,
		// deepest first and only once everything beneath it has landed; a
		// folder that was already there keeps its own time (DESIGN trap 28),
		// and a time that will not set leaves the folder created with io.
		sort.SliceStable(made, func(i, j int) bool { return made[i].depth > made[j].depth })
		for _, it := range made {
			t := time.Unix(it.modifiedAt, 0)
			if err := os.Chtimes(it.dst, t, t); err != nil {
				if i, ok := at[it.id]; ok {
					results[i].Code = CodeIO
				}
			}
		}
		return results, nil
	}), nil
}

// extractPlan resolves the set and orders it parents-first. A record reached
// twice is planned once. Caller holds the state mutex.
func extractPlan(m *merged, ids [][16]byte, all bool) ([]extractItem, *Error) {
	want := map[[16]byte]bool{}
	for _, rid := range ids {
		r := m.live(rid)
		if r == nil {
			return nil, coded(CodeFileNotFound)
		}
		want[r.id] = true
		if r.isDir {
			for _, k := range m.subtree(r.id) {
				want[k.id] = true
			}
		}
		for cur := r.parentID; cur != format.RootID; {
			a := m.rec(cur)
			if a == nil {
				break
			}
			want[a.id] = true
			cur = a.parentID
		}
	}
	var out []extractItem
	var walk func(parent [16]byte, depth int)
	walk = func(parent [16]byte, depth int) {
		for _, r := range m.kids[parent] {
			if !all && !want[r.id] {
				continue
			}
			out = append(out, extractItem{
				id: r.id, parentID: r.parentID, isDir: r.isDir, path: m.path(r.id),
				size: r.size, modifiedAt: r.modifiedAt, depth: depth,
			})
			if r.isDir {
				walk(r.id, depth+1)
			}
		}
	}
	walk(format.RootID, 0)
	return out, nil
}

// renamed inserts " (n)" before the extension of a path.
func renamed(p string, n int) string {
	ext := filepath.Ext(p)
	return fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(p, ext), n, ext)
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
// unless Open, gated on the session, and it waits its turn on the handle
// like every other operation; previews are quiesced; the handle is finished
// by the call and the path reopened.
func (c *Core) Compact(id string) (string, *Error) {
	if e := c.refuseIfForgotten(id); e != nil {
		return "", e
	}
	oa, e := c.findArchive(id)
	if e != nil {
		return "", e
	}
	c.mu.Lock()
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
		if c.archives[oa.id] != oa {
			// The archive was closed between the gate and the operation's
			// turn on the handle.
			oa.quiesced = false
			c.mu.Unlock()
			return nil, coded(CodeArchiveNotOpen)
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
	c.rememberFolder(archiveFolder, filepath.Dir(p))
	c.emitArchivesChanged()
	return hexID(id), nil
}

// rememberFolder records one of the settings file's two remembered folders —
// where the last archive was made, where the last extraction went — so that
// the next dialog opens there (APP.md §3's lastExtractFolder, §6's
// lastArchiveFolder). A convenience: a folder that could not be written down
// is logged and nothing else, and the operation stands either way.
func (c *Core) rememberFolder(which folderKind, dir string) {
	c.mu.Lock()
	field := &c.settings.LastArchiveFolder
	if which == extractFolder {
		field = &c.settings.LastExtractFolder
	}
	if *field == dir {
		c.mu.Unlock()
		return
	}
	*field = dir
	file := c.settings
	c.mu.Unlock()
	if err := saveSettings(c.deps.DataDir, file); err != nil {
		c.log("settings: recording %s: %v", which, err)
	}
}

// folderKind names which remembered folder rememberFolder writes.
type folderKind string

const (
	archiveFolder folderKind = "the archive folder"
	extractFolder folderKind = "the extract folder"
)

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

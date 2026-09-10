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
	// items is what the operation plans to write — the files of an add, a
	// replace or an extract — set once, when the plan is made, and zero
	// until then and for the kinds that count nothing (APP.md §3).
	items atomic.Int64
	// returned is what a reclaim gave the file system back — the file's
	// size before the run less its size after — set as the run ends and
	// zero for every other kind (OpView.Returned).
	returned atomic.Uint64
	phase    atomic.Value // string
	cancel   context.CancelFunc
	over     chan struct{}
	// policy and destination are an extract's, fixed before the operation
	// is registered and empty for every other kind (OpView.Policy).
	policy, destination string
	// committing is set once the writing is done and the commit has been
	// entered: from there the operation is being saved under a context no
	// cancel reaches, and CancelOp answers op.committing rather than
	// reporting a cancel it did not perform (the outside audit of
	// 2026-09-09). It is read and written under the state mutex.
	committing bool
	finished   bool
	err        *Error
	results    []FileOutcome
	c          *Core
	lastEmit   time.Time
}

func (o *op) view() OpView {
	v := OpView{
		ID: o.id, Kind: o.kind, ArchiveID: o.archiveID,
		Done: o.done.Load(), Total: o.total.Load(), Items: int(o.items.Load()),
		StartedAt: o.startedAt.Unix(), Finished: o.finished, Results: o.results,
		Policy: o.policy, Destination: o.destination, Returned: o.returned.Load(),
	}
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
	return c.startOpWith(kind, archiveID, nil, fn)
}

// startOpWith is startOp with the operation described before it is
// registered: describe fills in what the view carries from the first event
// on — an extract's policy and destination — and is nil for the rest.
func (c *Core) startOpWith(kind, archiveID string, describe func(o *op), fn func(ctx context.Context, o *op) ([]FileOutcome, error)) string {
	ctx, cancel := context.WithCancel(context.Background())
	o := &op{id: randomID(), kind: kind, archiveID: archiveID, startedAt: c.now(), cancel: cancel, over: make(chan struct{}), c: c}
	o.phase.Store("starting")
	if describe != nil {
		describe(o)
	}
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
	// The operation was one of the things holding the archive open. With it
	// over, an archive whose page has been left — or one this operation
	// opened for itself, the page never having been there — is closed and its
	// keys go (APP.md §2.3).
	closed := false
	var oa *openArchive
	if aid, ok := parseID(o.archiveID); ok {
		if oa = c.archives[aid]; oa != nil {
			closed = c.dropIfUnheldLocked(oa)
		}
	}
	c.mu.Unlock()
	close(o.over)
	c.emit(EventOpDone, v)
	if closed {
		c.emitArchivesChanged()
	}
	c.emitState()
	// Every operation's end is a trigger for the run (APP.md §2.3): the
	// commit's own end is where the plan is usually made, but the trigger a
	// reader's close carries is lost without this one — an extract releases
	// its own readers with a count of its own and never plans, and a preview
	// that ends while another operation is registered is refused there and
	// then (reclaimDueLocked), with nothing left to remember it by. The
	// operation is over, so it is not one of the operations that hold a run
	// off. A reclaim's own end is not a trigger: a run the user cancelled is
	// resumed by the next qualifying commit and never by itself, and one
	// that ended of its own accord has nothing left to plan.
	if oa != nil && !closed && o.kind != "reclaim" {
		c.reclaimIfWorth(oa, o)
	}
	// Keep finished ops for a while so a rebuilt window sees the outcome.
	c.deps.Clock.AfterFunc(5*time.Minute, func() {
		c.mu.Lock()
		delete(c.ops, o.id)
		c.mu.Unlock()
	})
}

// CancelOp cancels a running operation. On a running add or replace this is
// Abort: nothing is published (APP.md §2.3).
//
// An operation that has entered its commit is past that: the commit runs
// under a context no cancel reaches, so cancelling would publish the change
// and report it as cancelled all the same. It answers op.committing instead —
// "The operation is already being saved; it will finish." — and the
// operation's own result stays the real one.
//
// The handover is one critical section: the decision and the cancel that
// follows it are both under the state mutex, and markCommitting takes the
// same mutex to look at the context. Either the cancel lands first and the
// commit is refused — the transaction is aborted and nothing is published —
// or the commit is entered first and the cancel is refused. There is no
// instant in between in which a cancel is answered nil while the change is
// published all the same (the outside audit of 2026-09-09, finding 3).
func (c *Core) CancelOp(id string) *Error {
	c.mu.Lock()
	defer c.mu.Unlock()
	o := c.ops[id]
	if o == nil {
		return coded(CodeOpNotFound)
	}
	if o.committing && !o.finished {
		return classify(ErrOpCommitting)
	}
	// Under the mutex on purpose: a context.CancelFunc closes a channel and
	// touches nothing of the core, so it cannot come back in here.
	o.cancel()
	return nil
}

// markCommitting moves the operation into its commit, unless a cancel has
// already landed: false is a cancel that won, and the caller aborts its
// transaction and answers the context's error. Called on the operation's own
// goroutine, with opMu held and the state mutex free.
func (o *op) markCommitting(ctx context.Context) bool {
	o.c.mu.Lock()
	defer o.c.mu.Unlock()
	if ctx.Err() != nil {
		return false
	}
	o.committing = true
	return true
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
	var files int
	var count func(items []*planItem)
	count = func(items []*planItem) {
		for _, it := range items {
			if it.action == "add" || it.action == "replace" {
				// The plan is made: the strip can say how many files this is
				// (APP.md §3, OpView.Items), folders not among them — they
				// are records the walk makes, not bytes it writes.
				files++
				if it.node.size > 0 {
					total += uint64(it.node.size)
				}
			}
			count(it.children)
		}
	}
	count(plan)
	o.items.Store(int64(files))
	o.progress(0, total, "adding")
	t, e := c.beginOp(oa, o)
	if e != nil {
		return nil, e
	}
	defer t.end() // any exit that is not the commit below aborts (APP.md §2.3)
	results := []FileOutcome{}
	var done uint64
	wrote := false
	err := c.runPlan(ctx, o, oa, t.tx, plan, &results, &done, total, &wrote)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil || !wrote {
		// Cancelled, failed, or nothing to write after all: the transaction
		// is dropped and nothing is published (APP.md §2.3).
		t.abort()
		return results, err
	}
	// The commit is not cancellable: the writing is done, and a Cancel that
	// arrives now would leave the outcome to a race rather than to the user.
	// It is told so — the operation is committing, and CancelOp says that
	// rather than answering a cancel it did not perform. A cancel that got
	// in first wins instead: the transaction is dropped, as on any other
	// cancel, and nothing is published.
	if !o.markCommitting(ctx) {
		t.abort()
		return results, ctx.Err()
	}
	if e := t.commit(context.WithoutCancel(ctx)); e != nil {
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
		res := FileOutcome{Path: it.node.src, Name: it.joined, IsDir: it.node.isDir, ModifiedAt: it.node.modifiedAt}
		if !it.node.isDir {
			res.Size = uint64(max64(it.node.size, 0))
		}
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
			if it.written {
				res.ID = hexID(it.id)
			}
			*results = append(*results, res)
		case "create":
			info, err := tx.AddDir(parent, it.name, it.node.modifiedAt)
			if err != nil {
				res.Outcome, res.Code = "failed", classify(err).Code
				*results = append(*results, res)
				continue // nothing beneath a folder that was not made
			}
			it.id, it.written, *wrote = info.ID, true, true
			res.Outcome, res.ID = "created", hexID(info.ID)
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
	res.ID = hexID(info.ID)
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
		o.items.Store(1) // one file, which the strip says in the singular
		o.progress(0, total, "replacing")
		t, e := c.beginOp(oa, o)
		if e != nil {
			return nil, e
		}
		defer t.end() // as the add: every exit but the commit is an abort
		rd := &countingReaderAt{src: f, size: st.Size(), on: func(read int64) {
			o.progress(uint64(read), total, "replacing")
		}}
		if _, err := t.tx.Replace(ctx, fid, rd, st.Size()); err != nil {
			t.abort()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			t.abort()
			return nil, err
		}
		if !o.markCommitting(ctx) { // a cancel that got in first wins
			t.abort()
			return nil, ctx.Err()
		}
		if e := t.commit(context.WithoutCancel(ctx)); e != nil {
			return nil, e
		}
		o.progress(total, total, "replacing")
		return []FileOutcome{{Name: name, Path: src, Outcome: "replaced", ID: fileID, Size: total, ModifiedAt: st.ModTime().Unix()}}, nil
	}), nil
}

// ExtractPolicy is what happens when a destination file exists (APP.md §3,
// ruled 2026-09-10). replace is the default — what every archiver's wizard
// does — and ask is the drag-and-drop shape: what collides with nothing is
// extracted and every collision comes back as an outcome for the page to ask
// about, which then re-issues Extract for the chosen ids with replace or
// rename. Anything else is params.
type ExtractPolicy string

const (
	ExtractReplace ExtractPolicy = "replace"
	ExtractSkip    ExtractPolicy = "skip"
	ExtractRename  ExtractPolicy = "rename"
	ExtractAsk     ExtractPolicy = "ask"
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
// params, never everything.
//
// The destination is the page's to decide — it prefills the dialog by the
// rule of §3, and the core keeps no folder from the last time (ruled
// 2026-09-10) — and is created here if it does not exist.
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
	switch policy {
	case "":
		policy = ExtractReplace // the wizard default of every archiver (APP.md §3)
	case ExtractReplace, ExtractSkip, ExtractRename, ExtractAsk:
	default:
		return "", coded(CodeParams)
	}
	root := filepath.Clean(dir)
	// The policy and the destination ride on the operation from its first
	// event (OpView.Policy, Destination): the conflict question is derived
	// from the operation itself, never from the call's return (APP.md §3).
	describe := func(o *op) { o.policy, o.destination = string(policy), root }
	return c.startOpWith("extract", id, describe, func(ctx context.Context, o *op) ([]FileOutcome, error) {
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
		var files int
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
				// The plan is resolved, so the strip can say how many files
				// are coming out (APP.md §3, OpView.Items); the folders it
				// creates on the way are not files.
				files++
				total += it.size
			}
		}
		if err := os.MkdirAll(root, 0o700); err != nil {
			return nil, err
		}
		results := make([]FileOutcome, 0, len(items))
		at := make(map[[16]byte]int, len(items))
		gone := map[[16]byte]bool{}
		var made []extractItem
		var done uint64
		o.items.Store(int64(files))
		o.progress(0, total, "extracting")
		for _, it := range items {
			if ctx.Err() != nil {
				return results, ctx.Err()
			}
			// Every outcome names the record and carries the archive copy's
			// size and date — known before the first byte — so a conflict
			// can be re-issued by id and compared without walking the tree.
			res := FileOutcome{Name: it.path, Path: it.dst, IsDir: it.isDir, ID: hexID(it.id), ModifiedAt: it.modifiedAt}
			if !it.isDir {
				res.Size = it.size
			}
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
			// The operation itself holds the archive open (finishOp is what
			// closes one nothing holds), so this reader is counted for the
			// compaction's drain alone: the run this extraction's readers
			// could unblock is planned when the extraction ends, as every
			// operation's end plans one (finishOp), and not here.
			c.mu.Lock()
			oa.readers++
			c.mu.Unlock()
			base := done
			count := func(written uint64) { o.progress(base+written, total, "extracting") }
			err := extractFile(ctx, oa.a, it.id, it.dst, policy == ExtractReplace, count)
			for n := 2; err != nil && errors.Is(err, os.ErrExist) && policy == ExtractRename && n < 1000; n++ {
				res.Path = renamed(it.dst, n)
				err = extractFile(ctx, oa.a, it.id, res.Path, false, count)
			}
			c.mu.Lock()
			oa.readers--
			c.mu.Unlock()
			switch {
			case err == nil:
				res.Outcome = "extracted"
			case errors.Is(err, os.ErrExist) && policy == ExtractAsk:
				// The collision is the page's to resolve: it asks and
				// re-issues Extract for the chosen ids with replace or
				// rename. Existing is a stat taken after the collision was
				// seen — by the cheap pre-check or by the exclusive create's
				// refusal — and decides nothing: nothing is ever placed over
				// a file on a stat's word (APP.md §3). A file that went in
				// between leaves the outcome with none.
				res.Outcome, res.Existing = "conflict", existingFile(res.Path)
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

// Verify re-hashes the file and refreshes the registry's hash (R36). Like
// every operation of the Archives page it never asks for the archive to be
// opened first (APP.md §2.3): a closed one is opened for the operation and
// closed again when it ends.
func (c *Core) Verify(id string) (string, *Error) {
	if e := c.refuseIfForgotten(id); e != nil {
		return "", e
	}
	oa, e := c.acquireForOperation(id)
	if e != nil {
		return "", e
	}
	return c.startOp("verify", id, func(ctx context.Context, o *op) ([]FileOutcome, error) {
		c.releaseClaim(oa) // the operation is registered: it holds the handle now
		oa.opMu.Lock()
		defer oa.opMu.Unlock()
		o.progress(0, 1, "hashing")
		// A verify is where a torn envelope is made good (FORMAT.md R33,
		// amended 2026-09-09). An envelope that does not name the kid the
		// index opened under leaves the file readable only through the
		// registry record that still holds its archive_id — the envelope
		// exists so that the file is self-describing — and the repair was
		// left undone because rewriting it changes the ciphertext hash. Here
		// the hash is recomputed anyway, so the repaired bytes are the ones
		// hashed and recorded and LastCiphertextHash stays true. A repair
		// that fails is logged and the verify goes on: the file is readable
		// either way.
		if oa.a.EnvelopeStale() {
			if err := oa.a.RepairEnvelope(); err != nil {
				c.log("archive %s: the envelope could not be rewritten: %v", oa.name, err)
			}
		}
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

// Compact rewrites the archive without free space (APP.md §2.3): the
// archive is opened for the operation when its page is not open (§2.3, ruled
// 2026-09-10), gated on the session, and it waits its turn on the handle
// like every other operation; previews are quiesced; the handle is finished
// by the call and the path reopened.
func (c *Core) Compact(id string) (string, *Error) {
	if e := c.refuseIfForgotten(id); e != nil {
		return "", e
	}
	oa, e := c.acquireForOperation(id)
	if e != nil {
		return "", e
	}
	c.mu.Lock()
	if _, e := c.sessionLocked(); e != nil {
		c.mu.Unlock()
		c.releaseClaim(oa) // no operation to run: a handle opened for one goes
		return "", coded(CodeNeedsUnlock)
	}
	if !c.compactionFitsLocked(oa) {
		c.mu.Unlock()
		c.releaseClaim(oa)
		return "", coded(CodeTooSlow)
	}
	oa.quiesced = true
	c.mu.Unlock()
	return c.startCompaction(oa), nil
}

// compactionFitsLocked is the rough estimate a compaction is refused on: the
// whole file rewritten at some 200 MB/s, against what is left of the
// session's absolute cap. Caller holds the state mutex.
func (c *Core) compactionFitsLocked(oa *openArchive) bool {
	remaining := c.vault.absoluteAt.Sub(c.now())
	return time.Duration(oa.size/200e6)*time.Second <= remaining
}

// startCompaction runs the whole-file compaction the user asked for (APP.md
// §2.3): previews drained, the handle finished and the path reopened, the
// receipt recorded before the reopen. It is the Archives page's own since
// R40 — the core's own reclaim moves extents in place (startReclaim) and
// never rewrites the file — so what a failure leaves is the user's choice:
// they chose to leave, and the archive ends closed. Caller has set
// oa.quiesced and taken a claim on the handle under the state mutex.
func (c *Core) startCompaction(oa *openArchive) string {
	return c.startOp("compact", hexID(oa.id), func(ctx context.Context, o *op) ([]FileOutcome, error) {
		c.releaseClaim(oa) // the operation is registered: it holds the handle now
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
		// Whether the page holds this archive is read here and not before:
		// a page that opened it while the compaction ran joined this handle,
		// and the reopen below carries that on. A Compact of the Archives
		// page reopens unmounted and finishOp closes it (APP.md §2.3).
		mounted := oa.mounted
		c.closeArchiveLocked(oa)
		if err == nil {
			// The receipt is recorded (or owed) now, before the reopen: a
			// reopen that fails must not lose it. A compacted file starts
			// again at seq 1.
			c.recordReceiptLocked(oa, archive.Receipt{Seq: 1, Size: newSize, WrittenAt: c.now().Unix()}, &hash)
		}
		c.mu.Unlock()
		if err != nil {
			// The fresh file is discarded and the archive is left as it was:
			// closed, since the user chose to leave it for the compaction.
			c.emitArchivesChanged()
			c.emitState()
			return nil, err
		}
		// Reopen — for the page when the page is there, and as the core's own
		// handle when this Compact was the Archives page's, which finishOp
		// then closes (APP.md §2.3).
		if _, e := c.openArchiveFor(hexID(oa.id), mounted); e != nil {
			return nil, e
		}
		o.progress(1, 1, "compacted")
		c.emitArchivesChanged()
		return nil, nil
	})
}

// Reclaiming space (APP.md §2.3 "Space comes back" as amended on
// 2026-09-10, FORMAT.md R40, DECISIONS 2026-09-10). The archive layer gives
// the file's tail back with the commit that frees it (R31 as amended), but
// a hole with live data above it stays where it is, and an archive emptied
// by a delete stood at the size of what it had held. So the core lowers the
// live data itself: after any commit — and when the last reader holding an
// extent closes — it plans R40's in-place compaction on the free map in
// memory (archive.PlanReclaim) and, when the run would give the file system
// enough back, moves live extents down into the holes before them, a budget
// of bytes per commit, as a follow-on operation the strip shows as
// Reclaiming space. The rule is weighed on the run and not on the first of
// its commits — a plan is one commit deep, and the run of two commits that
// the first of three equal files deleted needs would never have been begun
// on what that first commit alone promises. No second file, never twice the
// size on disk, and a Cancel ends it between two chunks of a copy — the
// archive consistent and simply less compacted, the run resumed by the next
// qualifying commit.
const (
	// reclaimFloor is what a run must give the file system back before it
	// is worth its moves, and reclaimShare the share of the bytes it would
	// have to move that the gain must also be: a quarter, where lowering
	// the live data is worth the tail it returns — a 64 MiB hole at the head
	// of a 100 GB archive is not, and that is what Compact is for, on
	// request. Both measure bytes returned, never the size of any hole,
	// since a hole filled behind a file that cannot move returns nothing.
	// reclaimBudget is the ciphertext one commit moves at most, so that an
	// archive of many small files does not pay one index rewrite per file.
	// All three are the rule, not a setting; reclaimRule is what the core
	// measures against, so that a test can lower them.
	reclaimFloor  uint64 = 64 << 20
	reclaimShare  uint64 = 4
	reclaimBudget uint64 = 64 << 20
)

// reclaimRule is the three as one value, so the seam is one field.
type reclaimRule struct{ floor, share, budget uint64 }

// worth answers whether a plan earns its run: the tail the run gives back is
// at or over the floor and at least the rule's share of what the run would
// move. It is the run that is measured, never its first commit — a plan is
// one commit deep, and the first of three equal files deleted leaves [hole S]
// [A: S][B: S], whose first commit moves A behind a tail B still anchors and
// returns nothing at all, while the run returns S (archive.ReclaimPlan's
// RunTailReturned and RunBytesToMove). With nothing to move the tail is a
// free run an interrupted follow-up left, which one empty commit gives back.
func (r reclaimRule) worth(plan archive.ReclaimPlan) bool {
	return plan.RunTailReturned >= r.floor && plan.RunTailReturned*r.share >= plan.RunBytesToMove
}

// batch is the leading moves of a plan that fit the budget of source
// bytes: at least one, so that a file larger than the budget still moves,
// on its own. A plan answers for one commit — its moves free their sources
// into quarantine and the commit's own index takes a hole — so the rest of
// the plan is never used: the run plans again after the commit.
func (r reclaimRule) batch(moves []archive.Move) []archive.Move {
	var n int
	var bytes uint64
	for n < len(moves) && (n == 0 || bytes+moves[n].From.Len <= r.budget) {
		bytes += moves[n].From.Len
		n++
	}
	return moves[:n]
}

// reclaimIfWorth is what every commit ends with, and what the last reader
// of an archive ends with: the run is planned and started when it has
// earned it. The commit's own operation is not one of the operations that
// hold it off — it is over — and the session is not asked: a move is a
// commit like any other, the archive key is in memory, and the receipt it
// owes waits for the session (APP.md §2.3). The plan is asked for with the
// budget the run will commit by, so that the run it weighs is the run it
// would make: the commits are budgeted, and a budget moves the placements
// and not only the moment (archive.PlanReclaim). It is read outside the
// state mutex, since it takes the archive's own; a claim keeps the handle
// meanwhile, as at any other start of an operation. Caller holds opMu or
// nothing; the run's own goroutine waits for it.
func (c *Core) reclaimIfWorth(oa *openArchive, self *op) {
	c.mu.Lock()
	if !c.reclaimDueLocked(oa, self) {
		c.mu.Unlock()
		return
	}
	rule := c.reclaim
	oa.reclaiming = true
	oa.claims++ // released once the operation is registered, or below
	c.mu.Unlock()
	plan := oa.a.PlanReclaim(rule.budget)
	if !rule.worth(plan) {
		c.mu.Lock()
		oa.reclaiming = false
		c.mu.Unlock()
		c.releaseClaim(oa)
		return
	}
	c.log("archive %s: reclaiming %d bytes of the tail for %d bytes moved over %d commits", oa.name, plan.RunTailReturned, plan.RunBytesToMove, plan.Commits)
	c.startReclaim(oa)
}

// reclaimDueLocked answers whether a run may be planned at all: the handle
// is the open one and nothing else is running on it — a run started under
// another operation would only wait on the handle, and one started under a
// reclaim would be recursive. Caller holds the state mutex.
func (c *Core) reclaimDueLocked(oa *openArchive, self *op) bool {
	if c.archives[oa.id] != oa || oa.state != "open" || oa.quiesced || oa.reclaiming {
		return false
	}
	id := hexID(oa.id)
	for _, o := range c.ops {
		if o != self && !o.finished && o.archiveID == id {
			return false
		}
	}
	return true
}

// errReclaimIncomplete is the reclaim's own outcome when a move commit
// fails: the edit that started it committed and stays committed, and the
// page says "saved; reclaim incomplete", never that the edit failed.
var errReclaimIncomplete = errors.New("app: reclaiming space did not finish")

// startReclaim runs R40 under an operation of kind "reclaim": one commit at
// a time, each on a plan made afresh under the handle's turn — Publish when
// the hole the plan wants is still under R31's quarantine, else the leading
// moves that fit the budget — with the registry receipt after every commit
// as every commit has, until a plan has no moves. Readers are not quiesced:
// a move copies a source a reader may be reading and never retargets it,
// and the plan leaves a held extent where it lies. Progress is by bytes
// moved over what the first plan said the whole run would move, which a
// later plan may raise; the cancel reaches every chunk of the copy through
// ctx, and Close archive cancels the same way. What came back is measured on
// the file — its size before against after — and said separately from what
// was moved (OpView.Returned).
//
// The rule is a guard over the run as well as the gate before it: before
// every commit the fresh plan is weighed against what the run has cost so
// far, and a run whose moves have outrun what has come back plus what is
// still promised stops where it is — the same share, measured on the same
// two figures. An estimate answers for the layout it walked, and neither a
// bound it was cut short at nor a reader that arrived since is a reason to
// go on moving bytes for a tail that will not come. Stopping is not a
// failure: every commit made is committed, the archive is consistent and
// simply less compacted, and the next qualifying commit takes the run up
// again (APP.md §2.3). Caller has set oa.reclaiming and taken a claim on the
// handle under the state mutex.
func (c *Core) startReclaim(oa *openArchive) string {
	return c.startOp("reclaim", hexID(oa.id), func(ctx context.Context, o *op) ([]FileOutcome, error) {
		c.releaseClaim(oa) // the operation is registered: it holds the handle now
		c.mu.Lock()
		oa.reclaiming = false // the ops map says so from here
		c.mu.Unlock()
		oa.opMu.Lock()
		defer oa.opMu.Unlock()
		if err := ctx.Err(); err != nil {
			// A Close archive that took the handle's turn first cancelled
			// the run before it began, as it does any operation.
			return nil, err
		}
		c.mu.Lock()
		if c.archives[oa.id] != oa || oa.state != "open" {
			// The archive was closed between the plan and the operation's
			// turn on the handle.
			c.mu.Unlock()
			return nil, coded(CodeArchiveNotOpen)
		}
		rule := c.reclaim
		before := oa.size
		c.mu.Unlock()
		// Whatever ends the run — the last plan, the guard below, a cancel, a
		// failure — the file has shrunk by what the commits so far gave back.
		defer func() { o.returned.Store(c.returnedSoFar(oa, before)) }()

		var total, moved uint64
		o.progress(0, 0, "moving")
		// A plan the archive has moved on from is refused whole and writes
		// nothing (archive.ErrStalePlan): it is made again, a bounded number
		// of times, since under the handle's turn only a reader's coming or
		// going can change the map. Publish spends the quarantine, so a run
		// of them without a move is not a run: bounded the same way.
		stale, published := 0, 0
		for {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			plan := oa.a.PlanReclaim(rule.budget)
			if total == 0 {
				// The whole run's bytes, not this commit's: the progress bar
				// is the run's, and the run is what the rule weighed.
				total = plan.RunBytesToMove
			}
			// The rule, measured again on the run as it now stands: what has
			// come back so far and what this fresh plan still promises,
			// against what the moves have cost. An estimate is an estimate —
			// it is cut short on a long layout, and a reader that arrived
			// since can hold the tail where it lies — so the rule is a guard
			// over the run and not only a gate before it. Over it, the run
			// stops where it is: not a failure, since every commit it made is
			// committed and the archive is consistent and simply less
			// compacted, and the next qualifying commit takes it up again
			// (APP.md §2.3).
			if back := c.returnedSoFar(oa, before); moved > rule.share*(back+plan.RunTailReturned) {
				c.log("archive %s: the reclaim stopped after %d bytes moved for %d back; what is left promises %d",
					oa.name, moved, back, plan.RunTailReturned)
				return nil, nil
			}
			var rec archive.Receipt
			var err error
			switch {
			case plan.NeedsPublish, len(plan.Moves) == 0 && plan.TailReturned > 0 && published == 0:
				// The hole the plan wants was freed by the commit just made,
				// or a free tail an interrupted follow-up left is on offer:
				// the empty commit that publishes the one gives back the
				// other.
				if published++; published > 3 {
					c.log("archive %s: the reclaim's plan still wants a quarantined hole after %d empty commits; left for the next commit", oa.name, published-1)
					return nil, nil
				}
				rec, err = oa.a.Publish(ctx)
			case len(plan.Moves) == 0:
				return nil, nil
			default:
				batch := rule.batch(plan.Moves)
				var bytes uint64
				for _, m := range batch {
					bytes += m.From.Len
				}
				total = max(total, moved+bytes)
				rec, err = oa.a.MoveExtents(ctx, batch, func(done, _ uint64) {
					o.progress(moved+done, total, "moving")
				})
				if err == nil {
					moved += bytes
					published = 0
				}
			}
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return nil, err
				}
				if errors.Is(err, archive.ErrStalePlan) && stale < 3 {
					stale++
					continue
				}
				return nil, c.reclaimFailed(oa, err)
			}
			c.reclaimCommitted(oa, rec)
		}
	})
}

// returnedSoFar is what a run has given the file system back by now: the
// file's size when the run began against the size the core's snapshot of it
// stands at, which every commit refreshes (reclaimCommitted). Never below
// zero — a move commit's own metadata may leave the file larger for a moment
// (APP.md §2.3), and a run that has given nothing back has given nothing
// back.
func (c *Core) returnedSoFar(oa *openArchive, before uint64) uint64 {
	c.mu.Lock()
	after := oa.size
	c.mu.Unlock()
	if after >= before {
		return 0
	}
	return before - after
}

// reclaimCommitted is the bookkeeping every commit has (opTx.commit): the
// snapshot taken again, the receipt written or owed, the page told.
func (c *Core) reclaimCommitted(oa *openArchive, rec archive.Receipt) {
	c.mu.Lock()
	oa.refreshSnapshot()
	oa.lastSavedAt = rec.WrittenAt
	oa.seq++
	seq := oa.seq
	c.recordReceiptLocked(oa, rec, nil)
	c.mu.Unlock()
	c.emit(EventArchiveChanged, ArchiveChanged{ID: hexID(oa.id), Seq: seq})
	c.emitArchivesChanged()
	c.emitState()
}

// reclaimFailed ends the run on a commit that failed. The edit that started
// the run is saved whatever happened here, so the outcome is the reclaim's
// own code, with the archive layer's error in the log. A commit whose
// outcome is unknown finishes the handle, as it does for any operation
// (opTx.commit): the page must reopen the file to learn what landed.
func (c *Core) reclaimFailed(oa *openArchive, err error) error {
	c.log("archive %s: reclaiming space did not finish: %v", oa.name, err)
	c.mu.Lock()
	if errors.Is(err, archive.ErrIndeterminate) {
		oa.state = "needs_reopen"
	} else {
		// Any other failure aborted the transaction (the archive's
		// contract): nothing was published and the snapshot still stands.
		oa.refreshSnapshot()
	}
	c.mu.Unlock()
	c.emitArchivesChanged()
	c.emitState()
	return fmt.Errorf("%w: %w", errReclaimIncomplete, err)
}

// RotateKey is registry-first (R33, trap 21): the new version published,
// the old retired, then the archive adopts it, then the receipt. The
// Archives page never asks for the archive to be opened first (APP.md §2.3).
func (c *Core) RotateKey(id string) (string, *Error) {
	if e := c.refuseIfForgotten(id); e != nil {
		return "", e
	}
	oa, e := c.acquireForOperation(id)
	if e != nil {
		return "", e
	}
	c.mu.Lock()
	if _, e := c.sessionLocked(); e != nil {
		c.mu.Unlock()
		c.releaseClaim(oa) // no operation to run: a handle opened for one goes
		return "", coded(CodeNeedsUnlock)
	}
	c.mu.Unlock()
	return c.startOp("rotate", id, func(ctx context.Context, o *op) ([]FileOutcome, error) {
		c.releaseClaim(oa) // the operation is registered: it holds the handle now
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
	c.rememberArchiveFolder(filepath.Dir(p))
	c.emitArchivesChanged()
	return hexID(id), nil
}

// rememberArchiveFolder records where the last archive was made, so that the
// next New archive dialog opens there (APP.md §6's lastArchiveFolder — the
// one remembered folder left, an extract's destination having become the
// page's own rule on 2026-09-10). A convenience: a folder that could not be
// written down is logged and nothing else, and the create stands either way.
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

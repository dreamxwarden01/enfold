package archive

import (
	"crypto/rand"
	"fmt"
	"strings"

	"github.com/dreamxwarden01/enfold/internal/format"
)

// tree is one index's id space (FORMAT.md R39): both record tables looked up
// by id. A record is held as a position in the index's slice rather than as a
// pointer, because appending a record may move the slice's backing array while
// a position stays what it was; nothing is ever removed — a deletion is a
// tombstone — so a position, once taken, names the same record for the life of
// the index.
type tree struct {
	x     *format.Index
	dirs  map[[16]byte]int
	files map[[16]byte]int
}

// ref names one record of either table.
type ref struct {
	id    [16]byte
	isDir bool
}

func newTree(x *format.Index) *tree {
	t := &tree{x: x, dirs: make(map[[16]byte]int, len(x.Dirs)), files: make(map[[16]byte]int, len(x.Files))}
	for i := range x.Dirs {
		t.dirs[x.Dirs[i].DirID] = i
	}
	for i := range x.Files {
		t.files[x.Files[i].FileID] = i
	}
	return t
}

// dirRec and fileRec find a record whatever its state; liveDir and liveFile
// find one only while it is live. A tombstone is not a parent, not a sibling
// and not a thing to act on (R39).
func (t *tree) dirRec(id [16]byte) *format.DirRecord {
	if i, ok := t.dirs[id]; ok {
		return &t.x.Dirs[i]
	}
	return nil
}

func (t *tree) fileRec(id [16]byte) *format.FileRecord {
	if i, ok := t.files[id]; ok {
		return &t.x.Files[i]
	}
	return nil
}

func (t *tree) liveDir(id [16]byte) *format.DirRecord {
	if d := t.dirRec(id); d != nil && d.State == format.FileLive {
		return d
	}
	return nil
}

func (t *tree) liveFile(id [16]byte) *format.FileRecord {
	if f := t.fileRec(id); f != nil && f.State == format.FileLive {
		return f
	}
	return nil
}

// live describes a live record of either kind, which is what Rename, Move,
// Delete and Path address: R39 gives the two tables one id space.
func (t *tree) live(id [16]byte) (r ref, name string, parent [16]byte, ok bool) {
	if d := t.liveDir(id); d != nil {
		return ref{id, true}, d.Name, d.ParentID, true
	}
	if f := t.liveFile(id); f != nil {
		return ref{id, false}, f.Name, f.ParentID, true
	}
	return ref{}, "", [16]byte{}, false
}

// name is the record's own name, whatever its kind.
func (t *tree) name(r ref) string {
	if r.isDir {
		return t.dirRec(r.id).Name
	}
	return t.fileRec(r.id).Name
}

// appendDir and appendFile add a record and index it in one step, so the maps
// can never fall behind the tables.
func (t *tree) appendDir(d format.DirRecord) {
	t.dirs[d.DirID] = len(t.x.Dirs)
	t.x.Dirs = append(t.x.Dirs, d)
}

func (t *tree) appendFile(f format.FileRecord) {
	t.files[f.FileID] = len(t.x.Files)
	t.x.Files = append(t.x.Files, f)
}

// mintID draws a fresh random 128-bit id the way a KID is minted (FORMAT §7.2),
// never one the index already holds and never one it has freed: tombstones
// count, because a recycled id would let a peer's delete land on a new record
// (R39).
func (t *tree) mintID() ([16]byte, error) {
	for i := 0; i < 8; i++ {
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return id, err
		}
		if id == format.RootID {
			continue
		}
		if _, dup := t.dirs[id]; dup {
			continue
		}
		if _, dup := t.files[id]; dup {
			continue
		}
		return id, nil
	}
	return [16]byte{}, fmt.Errorf("%w: no unused record id in eight draws", ErrInternal)
}

// parentUsable reports whether id may be given records as their parent: the
// root, or a live directory of this index (R39 — a live record never stands
// under a tombstone, and nothing may be staged beneath a deleted folder).
func (t *tree) parentUsable(id [16]byte) bool {
	return id == format.RootID || t.liveDir(id) != nil
}

// liveName is R39's sibling rule as the writing layer holds it: among the live
// children of one parent, files and directories share one namespace and their
// names are unique under simple Unicode case folding. except is the record
// being renamed or moved — a record is not its own sibling, so changing only
// the case of a name is a rename, never a collision. A tombstone is not a live
// sibling, so a deleted record's name is free.
func (t *tree) liveName(parent [16]byte, name string, except [16]byte) bool {
	for i := range t.x.Dirs {
		d := &t.x.Dirs[i]
		if d.State == format.FileLive && d.ParentID == parent && d.DirID != except && strings.EqualFold(d.Name, name) {
			return true
		}
	}
	for i := range t.x.Files {
		f := &t.x.Files[i]
		if f.State == format.FileLive && f.ParentID == parent && f.FileID != except && strings.EqualFold(f.Name, name) {
			return true
		}
	}
	return false
}

// dirPos is where a live directory stands: how many parents up the root is,
// and the byte length its children's paths start at — its own joined path plus
// one for the separator, the arithmetic format.Index.Validate uses. The root
// is depth 0 with prefix 0.
func (t *tree) dirPos(id [16]byte) (depth, prefix int, err error) {
	for cur := id; cur != format.RootID; {
		d := t.liveDir(cur)
		if d == nil {
			return 0, 0, ErrNotFound
		}
		depth++
		prefix += len(d.Name) + 1
		if depth > format.MaxTreeDepth {
			return 0, 0, fmt.Errorf("%w: directory %x stands more than %d directories below the root", ErrTreeBounds, id, format.MaxTreeDepth)
		}
		cur = d.ParentID
	}
	return depth, prefix, nil
}

// path is the joined path of a live record: its ancestors' names and its own,
// separated by '/' (R20, R39). The root's is empty.
func (t *tree) path(id [16]byte) (string, error) {
	if id == format.RootID {
		return "", nil
	}
	_, name, parent, ok := t.live(id)
	if !ok {
		return "", ErrNotFound
	}
	els := []string{name}
	for cur := parent; cur != format.RootID; {
		d := t.liveDir(cur)
		if d == nil {
			return "", fmt.Errorf("%w: record %x stands under %x, which is not a live directory", ErrInternal, id, cur)
		}
		els = append(els, d.Name)
		if len(els) > format.MaxTreeDepth+1 {
			return "", fmt.Errorf("%w: the chain above record %x is longer than %d", ErrInternal, id, format.MaxTreeDepth)
		}
		cur = d.ParentID
	}
	for i, j := 0, len(els)-1; i < j; i, j = i+1, j-1 {
		els[i], els[j] = els[j], els[i]
	}
	return strings.Join(els, "/"), nil
}

// isBelow reports whether id is dir itself or one of its descendants — what a
// move into itself or into a descendant is refused by (R39).
func (t *tree) isBelow(id, dir [16]byte) bool {
	for cur, steps := id, 0; cur != format.RootID && steps <= format.MaxTreeDepth; steps++ {
		if cur == dir {
			return true
		}
		d := t.liveDir(cur)
		if d == nil {
			return false
		}
		cur = d.ParentID
	}
	return false
}

// childRefs indexes the live records by parent, for the subtree walks a
// delete, a directory rename and a move need: R39 makes a writer's blast
// radius the subtree, never the one record it was handed, and a scan per node
// would be quadratic in the index.
func (t *tree) childRefs() map[[16]byte][]ref {
	m := make(map[[16]byte][]ref, len(t.x.Dirs)+1)
	for i := range t.x.Dirs {
		if d := &t.x.Dirs[i]; d.State == format.FileLive {
			m[d.ParentID] = append(m[d.ParentID], ref{d.DirID, true})
		}
	}
	for i := range t.x.Files {
		if f := &t.x.Files[i]; f.State == format.FileLive {
			m[f.ParentID] = append(m[f.ParentID], ref{f.FileID, false})
		}
	}
	return m
}

// subtree returns every live record beneath dir, parents before children —
// the set a directory's deletion tombstones in one write (R39).
func (t *tree) subtree(kids map[[16]byte][]ref, dir [16]byte) ([]ref, error) {
	limit := len(t.x.Dirs) + len(t.x.Files)
	out := make([]ref, 0, len(kids[dir]))
	queue := append([]ref(nil), kids[dir]...)
	for len(queue) > 0 {
		r := queue[0]
		queue = queue[1:]
		if len(out) >= limit {
			return nil, fmt.Errorf("%w: the record tree beneath %x has a cycle", ErrInternal, dir)
		}
		out = append(out, r)
		if r.isDir {
			queue = append(queue, kids[r.id]...)
		}
	}
	return out, nil
}

// checkBounds is R39's writer side: a record about to be created, renamed or
// re-parented, and every live record beneath it, must still join to a path of
// at most format.MaxPathLen bytes, and every live directory among them must
// still stand at most format.MaxTreeDepth below the root. Renaming a
// directory lengthens every descendant's path; moving one re-depths all of
// them — and in each case the record the caller named looks fine. kids may be
// nil when the record has no descendants (a file, or one not yet created).
func (t *tree) checkBounds(kids map[[16]byte][]ref, r ref, parent [16]byte, name string) error {
	depth, prefix, err := t.dirPos(parent)
	if err != nil {
		return err
	}
	over := func(id [16]byte, n int) error {
		return fmt.Errorf("%w: record %x joins to a path of %d bytes, over %d", ErrTreeBounds, id, n, format.MaxPathLen)
	}
	deep := func(id [16]byte, d int) error {
		return fmt.Errorf("%w: directory %x would stand %d directories below the root, over %d", ErrTreeBounds, id, d, format.MaxTreeDepth)
	}
	if n := prefix + len(name); n > format.MaxPathLen {
		return over(r.id, n)
	}
	if !r.isDir {
		return nil
	}
	if depth+1 > format.MaxTreeDepth {
		return deep(r.id, depth+1)
	}
	type at struct {
		r      ref
		depth  int // the record's own depth, root-relative
		prefix int // the byte length its own path starts at
	}
	queue := make([]at, 0, len(kids[r.id]))
	for _, c := range kids[r.id] {
		queue = append(queue, at{c, depth + 2, prefix + len(name) + 1})
	}
	limit := len(t.x.Dirs) + len(t.x.Files)
	for visited := 0; len(queue) > 0; visited++ {
		cur := queue[0]
		queue = queue[1:]
		if visited >= limit {
			return fmt.Errorf("%w: the record tree beneath %x has a cycle", ErrInternal, r.id)
		}
		nm := t.name(cur.r)
		if n := cur.prefix + len(nm); n > format.MaxPathLen {
			return over(cur.r.id, n)
		}
		if !cur.r.isDir {
			continue
		}
		if cur.depth > format.MaxTreeDepth {
			return deep(cur.r.id, cur.depth)
		}
		for _, c := range kids[cur.r.id] {
			queue = append(queue, at{c, cur.depth + 1, cur.prefix + len(nm) + 1})
		}
	}
	return nil
}

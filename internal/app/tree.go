package app

import (
	"strings"

	"github.com/dreamxwarden01/enfold/internal/format"
)

// The merged view (APP.md §2.3): the committed snapshot — the archive's
// Files() and Dirs() together — with the overlay of staged changes applied,
// held as the tree FORMAT.md R39 describes. A directory is a record with an
// id, a file hangs off its parent by id, and the root is the all-zero id,
// which never has a record. Nothing here is derived from a path and no
// folder is inferred from a name (DESIGN.md trap 31): the page's rows, its
// breadcrumb, every pre-flight and the extraction plan all read this tree.

// The Pending vocabulary (APP.md §2.3): one entry per record and one word
// per entry. `added` outlives every later change to a staged-added record,
// `replaced` outranks `renamed` and `moved`, a record both renamed and moved
// reads `moved`, and `deleted` never sits on a staged add — that un-stages
// instead.
const (
	pendingAdded    = "added"
	pendingReplaced = "replaced"
	pendingRenamed  = "renamed"
	pendingMoved    = "moved"
	pendingDeleted  = "deleted"
)

// mergedRec is one record as the merged view has it: the committed record's
// fields with the overlay's name, parent and — for a replace — content.
type mergedRec struct {
	id       [16]byte
	parentID [16]byte
	isDir    bool
	name     string
	pending  string // "" | added | replaced | renamed | moved | deleted
	// A file carries what its row shows of its content. A directory's
	// modifiedAt is the folder's own time and never a change clock
	// (FORMAT.md §11, R32).
	size       uint64
	storedSize uint64
	storage    format.Storage
	modifiedAt int64
}

// merged is one reading of that view: every record reachable from the root,
// and the children of each id in listing order.
type merged struct {
	byID map[[16]byte]*mergedRec
	kids map[[16]byte][]*mergedRec
}

// merge builds the view. Caller holds the state mutex.
//
// A directory staged for deletion keeps its row — greyed, not enterable —
// and nothing beneath it is listed, previewed or extracted while the
// deletion stands (APP.md §3), so the walk stops there. A record whose
// parent went with an un-staged folder is reachable from nowhere and is
// simply not in the view.
func (oa *openArchive) merge() *merged {
	recs := make([]*mergedRec, 0, len(oa.dirSnap)+len(oa.snap)+len(oa.adds))
	for i := range oa.dirSnap {
		d := &oa.dirSnap[i]
		recs = append(recs, oa.applyOverlay(&mergedRec{
			id: d.ID, parentID: d.ParentID, isDir: true, name: d.Name, modifiedAt: d.ModifiedAt,
		}))
	}
	for i := range oa.snap {
		f := &oa.snap[i]
		recs = append(recs, oa.applyOverlay(&mergedRec{
			id: f.ID, parentID: f.ParentID, name: f.Name,
			size: f.Size, storedSize: f.StoredSize, storage: f.Storage, modifiedAt: f.ModifiedAt,
		}))
	}
	for _, p := range oa.adds {
		r := &mergedRec{id: p.id, parentID: p.parentID, isDir: p.isDir, name: p.name, pending: p.kind}
		if p.isDir {
			r.modifiedAt = p.dir.ModifiedAt
		} else {
			r.size, r.storedSize, r.storage, r.modifiedAt = p.file.Size, p.file.StoredSize, p.file.Storage, p.file.ModifiedAt
		}
		recs = append(recs, r)
	}
	kids := make(map[[16]byte][]*mergedRec, len(recs)+1)
	for _, r := range recs {
		kids[r.parentID] = append(kids[r.parentID], r)
	}
	m := &merged{
		byID: make(map[[16]byte]*mergedRec, len(recs)),
		kids: make(map[[16]byte][]*mergedRec, len(recs)+1),
	}
	queue := [][16]byte{format.RootID}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, r := range kids[p] {
			if _, seen := m.byID[r.id]; seen {
				continue
			}
			m.byID[r.id] = r
			m.kids[p] = append(m.kids[p], r)
			if r.isDir && r.pending != pendingDeleted {
				queue = append(queue, r.id)
			}
		}
	}
	return m
}

// applyOverlay puts the staged change on a committed record. A deleted
// entry keeps the name and parent the record had when the deletion was
// staged, so the greyed row stands where the user last saw it.
func (oa *openArchive) applyOverlay(r *mergedRec) *mergedRec {
	p := oa.overlay[r.id]
	if p == nil {
		return r
	}
	r.pending, r.name, r.parentID = p.kind, p.name, p.parentID
	if p.kind == pendingReplaced {
		r.size, r.storedSize, r.storage, r.modifiedAt = p.file.Size, p.file.StoredSize, p.file.Storage, p.file.ModifiedAt
	}
	return r
}

// rec finds a record of the view whatever its pending word.
func (m *merged) rec(id [16]byte) *mergedRec { return m.byID[id] }

// live is the record a call may act on: one of the view not staged for
// deletion. The root is never a record (APP.md §3), so it is never live.
func (m *merged) live(id [16]byte) *mergedRec {
	r := m.byID[id]
	if r == nil || r.pending == pendingDeleted {
		return nil
	}
	return r
}

// dirUsable reports whether id may be listed into, or given records: the
// root, or a directory live in the view — a folder staged by CreateFolder
// counts, one staged for deletion does not (APP.md §3).
func (m *merged) dirUsable(id [16]byte) bool {
	if id == format.RootID {
		return true
	}
	r := m.live(id)
	return r != nil && r.isDir
}

// sibling is R39's fold as the app's pre-flight holds it: the live child of
// parent whose name folds onto name, the record itself excepted — a record
// is not its own sibling, so a change of case alone is a rename and never a
// collision. A tombstone is not a live sibling, so a staged-deleted row
// reserves no name.
func (m *merged) sibling(parent [16]byte, name string, except [16]byte) *mergedRec {
	for _, r := range m.kids[parent] {
		if r.id == except || r.pending == pendingDeleted {
			continue
		}
		if strings.EqualFold(r.name, name) {
			return r
		}
	}
	return nil
}

// dirPos is a directory's depth below the root and the byte length at which
// its children's paths start — the arithmetic FORMAT.md R39 and the archive
// layer's own walk use, so the app's pre-flight and the encoder agree. ok is
// false when id is not the root or a directory live in the view.
func (m *merged) dirPos(id [16]byte) (depth, prefix int, ok bool) {
	for cur := id; cur != format.RootID; {
		d := m.live(cur)
		if d == nil || !d.isDir {
			return 0, 0, false
		}
		depth++
		prefix += len(d.name) + 1
		if depth > format.MaxTreeDepth {
			return 0, 0, false
		}
		cur = d.parentID
	}
	return depth, prefix, true
}

// path is the joined path of a record: its ancestors' names and its own,
// separated by '/' (R20, R39). The root's is empty.
func (m *merged) path(id [16]byte) string {
	r := m.byID[id]
	if id == format.RootID || r == nil {
		return ""
	}
	els := []string{r.name}
	for cur, n := r.parentID, 0; cur != format.RootID && n <= format.MaxTreeDepth; n++ {
		d := m.byID[cur]
		if d == nil {
			break
		}
		els = append(els, d.name)
		cur = d.parentID
	}
	for i, j := 0, len(els)-1; i < j; i, j = i+1, j-1 {
		els[i], els[j] = els[j], els[i]
	}
	return strings.Join(els, "/")
}

// subtree is every record beneath dir in the view, parents before children.
func (m *merged) subtree(dir [16]byte) []*mergedRec {
	out := make([]*mergedRec, 0, len(m.kids[dir]))
	queue := append([]*mergedRec(nil), m.kids[dir]...)
	for len(queue) > 0 {
		r := queue[0]
		queue = queue[1:]
		out = append(out, r)
		if r.isDir {
			queue = append(queue, m.kids[r.id]...)
		}
	}
	return out
}

// isBelow reports whether id is dir itself or one of its descendants — what
// a move into itself or into a descendant is refused by (R39).
func (m *merged) isBelow(id, dir [16]byte) bool {
	for cur, n := id, 0; cur != format.RootID && n <= format.MaxTreeDepth+1; n++ {
		if cur == dir {
			return true
		}
		r := m.byID[cur]
		if r == nil {
			return false
		}
		cur = r.parentID
	}
	return false
}

// bounds is R39's writer-side subtree rule as the app's pre-flight holds it:
// a record about to be created, renamed or re-parented, and every record
// beneath it, must still join to a path of at most format.MaxPathLen bytes,
// and every directory among them must still stand at most
// format.MaxTreeDepth below the root. The record the caller named always
// looks fine — renaming a folder lengthens every path beneath it, moving one
// re-depths all of them — so the subtree is what is measured, here rather
// than at the seal. depth and prefix are the destination parent's, so a
// record the walk has not created yet is measured the same way. Both bounds
// hold for live records only — a tombstone keeps its name and is held to
// neither (R32, and the archive layer's own walk indexes live records alone)
// — so a row staged for deletion is not walked and the pre-flight answers
// what the encoder would, never more.
func boundsAt(m *merged, depth, prefix int, isDir bool, name string, kids []*mergedRec) *Error {
	if prefix+len(name) > format.MaxPathLen {
		return coded(CodeTreeBounds)
	}
	if !isDir {
		return nil
	}
	if depth+1 > format.MaxTreeDepth {
		return coded(CodeTreeBounds)
	}
	type at struct {
		r      *mergedRec
		depth  int
		prefix int
	}
	queue := make([]at, 0, len(kids))
	for _, c := range kids {
		if c.pending == pendingDeleted {
			continue
		}
		queue = append(queue, at{c, depth + 2, prefix + len(name) + 1})
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.prefix+len(cur.r.name) > format.MaxPathLen {
			return coded(CodeTreeBounds)
		}
		if !cur.r.isDir {
			continue
		}
		if cur.depth > format.MaxTreeDepth {
			return coded(CodeTreeBounds)
		}
		for _, c := range m.kids[cur.r.id] {
			if c.pending == pendingDeleted {
				continue
			}
			queue = append(queue, at{c, cur.depth + 1, cur.prefix + len(cur.r.name) + 1})
		}
	}
	return nil
}

// bounds measures a record of the view against a destination parent: the
// record's own subtree travels with it, so its children are what is walked.
func (m *merged) bounds(id [16]byte, isDir bool, parent [16]byte, name string) *Error {
	depth, prefix, ok := m.dirPos(parent)
	if !ok {
		return coded(CodeFileNotFound)
	}
	return boundsAt(m, depth, prefix, isDir, name, m.kids[id])
}

// boundsNew measures a record that does not exist yet — CreateFolder's
// folder, and every record an add's walk would make. It has no subtree.
func (m *merged) boundsNew(parent [16]byte, isDir bool, name string) *Error {
	depth, prefix, ok := m.dirPos(parent)
	if !ok {
		return coded(CodeFileNotFound)
	}
	return boundsAt(m, depth, prefix, isDir, name, nil)
}

// batch resolves a selection of record ids against the view and drops every
// record whose own ancestor is in the same batch: it travels with that
// ancestor, for a deletion as for a move (APP.md §3). A repeated id is one
// record, and an id that is not live is file.not_found.
func (m *merged) batch(ids [][16]byte) ([]*mergedRec, *Error) {
	recs := make([]*mergedRec, 0, len(ids))
	seen := make(map[[16]byte]bool, len(ids))
	for _, id := range ids {
		r := m.live(id)
		if r == nil {
			return nil, coded(CodeFileNotFound)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		recs = append(recs, r)
	}
	out := make([]*mergedRec, 0, len(recs))
	for _, r := range recs {
		absorbed := false
		for _, o := range recs {
			if o.id != r.id && o.isDir && m.isBelow(r.parentID, o.id) {
				absorbed = true
				break
			}
		}
		if !absorbed {
			out = append(out, r)
		}
	}
	return out, nil
}

// crumbs is the chain from the root down to dirID inclusive, never empty:
// its first entry is the root under the archive's name, its last is dirID
// itself, and a staged directory stands in it like any other, so the page
// draws the whole breadcrumb from this alone (APP.md §3).
func (m *merged) crumbs(dirID [16]byte, rootName string) []Crumb {
	var up []Crumb
	for cur, n := dirID, 0; cur != format.RootID && n <= format.MaxTreeDepth; n++ {
		r := m.byID[cur]
		if r == nil {
			break
		}
		up = append(up, Crumb{ID: hexID(r.id), Name: r.name})
		cur = r.parentID
	}
	out := make([]Crumb, 0, len(up)+1)
	out = append(out, Crumb{ID: hexID(format.RootID), Name: rootName})
	for i := len(up) - 1; i >= 0; i-- {
		out = append(out, up[i])
	}
	return out
}

// sizeBeneath is the sum of the plaintext of every file in a directory's
// subtree, which is what a folder row's Size shows (APP.md §3). A row staged
// for deletion is not in the sum: the number tracks what a save would keep,
// which is the only reading consistent with a deleted folder's own row —
// nothing beneath it is listed while the deletion stands, so it shows zero.
func (m *merged) sizeBeneath(dir [16]byte) uint64 {
	var n uint64
	for _, r := range m.subtree(dir) {
		if !r.isDir && r.pending != pendingDeleted {
			n += r.size
		}
	}
	return n
}

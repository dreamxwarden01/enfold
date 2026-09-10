package archive

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dreamxwarden01/enfold/internal/format"
)

// chain builds a run of nested directories under parent, each named as name
// returns, and gives back every id from the topmost down.
func chain(t testing.TB, tx *Tx, parent [16]byte, n int, name func(i int) string) [][16]byte {
	t.Helper()
	ids := make([][16]byte, 0, n)
	cur := parent
	for i := 0; i < n; i++ {
		d, err := tx.AddDir(cur, name(i), int64(1700000000+i))
		if err != nil {
			t.Fatalf("chain at %d: %v", i, err)
		}
		ids = append(ids, d.ID)
		cur = d.ID
	}
	return ids
}

// TestAddDirEmptyFolderSurvives is the ask the tree exists for: a folder is a
// record, so an empty one is written at save and is still there on reopen
// (FORMAT §11, R39, DESIGN.md trap 31).
func TestAddDirEmptyFolderSurvives(t *testing.T) {
	a, fx := newFixture(t, Options{})
	photos := mkdir(t, a, root, "Photos", 1700001000)
	empty := mkdir(t, a, photos.ID, "empty", 1700002000)
	if photos.Revision != 1 || photos.LastWriter != deviceID || photos.ModifiedAt != 1700001000 {
		t.Fatalf("directory record: %+v", photos)
	}
	// Nothing hangs off it and nothing needs to: the record is the folder.
	if dirs, files, err := a.Children(empty.ID); err != nil || len(dirs) != 0 || len(files) != 0 {
		t.Fatalf("empty folder: %d dirs %d files %v", len(dirs), len(files), err)
	}
	if _, files, _ := a.Children(root); len(files) != 0 {
		t.Fatalf("%d files at the root", len(files))
	}
	if _, n, _ := a.Stat(); n != 0 {
		t.Errorf("Stat counted %d files for two folders", n)
	}
	// Refusals around a parent id.
	if _, _, err := a.AddDir(ctx, rnd16(t), "x", 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("mkdir under an unknown parent: %v", err)
	}
	if _, _, err := a.AddDir(ctx, root, "Photos", 1); !errors.Is(err, ErrExists) {
		t.Errorf("mkdir onto a live folder's name: %v", err)
	}
	if _, _, err := a.AddDir(ctx, root, "photos", 1); !errors.Is(err, ErrExists) {
		t.Errorf("mkdir onto a folded name: %v", err)
	}
	if _, _, err := a.AddDir(ctx, root, "a/b", 1); !errors.Is(err, format.ErrInvalid) {
		t.Errorf("a path as a folder name: %v", err)
	}
	// A folder is not a file with content: replacing one is not an edit of it.
	if _, _, err := a.Replace(ctx, photos.ID, bytes.NewReader([]byte("x")), 1); !errors.Is(err, ErrKindMismatch) {
		t.Errorf("replace a directory: %v", err)
	}
	if _, err := a.OpenReader(photos.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("open a directory for reading: %v", err)
	}
	// The root is a directory where a directory is named and never a record
	// where one is acted on (APP.md §3).
	if _, err := a.Delete(ctx, root); !errors.Is(err, ErrParams) {
		t.Errorf("delete the root: %v", err)
	}
	if _, err := a.Rename(ctx, root, "x"); !errors.Is(err, ErrParams) {
		t.Errorf("rename the root: %v", err)
	}
	if _, err := a.Move(ctx, root, photos.ID); !errors.Is(err, ErrParams) {
		t.Errorf("move the root: %v", err)
	}
	a.Close()

	b := fx.open(t)
	if len(b.Dirs()) != 2 || len(b.Files()) != 0 {
		t.Fatalf("after reopen: %d dirs %d files", len(b.Dirs()), len(b.Files()))
	}
	d, ok := b.InfoDir(empty.ID)
	if !ok || d.Name != "empty" || d.ParentID != photos.ID || d.ModifiedAt != 1700002000 {
		t.Fatalf("empty folder after reopen: %+v %v", d, ok)
	}
	if got := pathOf(t, b, empty.ID); got != "Photos/empty" {
		t.Errorf("path %q", got)
	}
}

// TestDeleteDirectoryTombstonesSubtree: one call, one commit, the whole
// subtree tombstoned (R39) with each kind's own tombstone rule (R32), and the
// files' extents back in the free map.
func TestDeleteDirectoryTombstonesSubtree(t *testing.T) {
	a, fx := newFixture(t, Options{})
	top := mkdir(t, a, root, "top", 1700003000)
	mid := mkdir(t, a, top.ID, "mid", 1700004000)
	deep := mkdir(t, a, mid.ID, "deep", 1700005000)
	keep := add(t, a, root, "keep.bin", noise(20000, 200))
	files := []FileInfo{
		add(t, a, top.ID, "a.bin", noise(30000, 201)),
		add(t, a, mid.ID, "b.bin", noise(30000, 202)),
		add(t, a, deep.ID, "c.bin", noise(30000, 203)),
	}
	size0, _, _ := a.Stat()
	seq0 := a.Seq()

	if _, err := a.Delete(ctx, top.ID); err != nil {
		t.Fatal(err)
	}
	// One commit for the subtree, and the one that follows every commit
	// which leaves a free run at the end of the file (R31 as amended,
	// trim.go): the three files were the tail, so it comes back here.
	if a.Seq() != seq0+2 {
		t.Errorf("the subtree took %d commits", a.Seq()-seq0)
	}
	if len(a.Dirs()) != 0 {
		t.Errorf("%d live directories left", len(a.Dirs()))
	}
	if got := a.Files(); len(got) != 1 || got[0].ID != keep.ID {
		t.Errorf("%d live files left", len(got))
	}
	var bytesFreed uint64
	for _, f := range files {
		bytesFreed += f.StoredSize
	}
	if size1, _, _ := a.Stat(); size1 > size0-bytesFreed {
		t.Errorf("the file went %d → %d, expected at least %d back", size0, size1, bytesFreed)
	}
	// R32, the file rule: identity, name, parent and dek_epoch kept, content
	// zeroed, revision and modified_at advanced.
	for _, f := range files {
		r := a.tree.fileRec(f.ID)
		if r == nil || r.State != format.FileTombstone {
			t.Fatalf("file %x: %+v", f.ID, r)
		}
		if r.Name == "" || r.ParentID == format.RootID || r.DEKEpoch != 1 {
			t.Errorf("file tombstone lost its shape: %+v", r)
		}
		if r.OrigSize != 0 || r.StoredSize != 0 || r.DataOff != 0 || r.ContentHash != [32]byte{} ||
			r.WrappedDEK != [format.WrappedKeySize]byte{} || r.DEKNonce != [format.NonceSize]byte{} ||
			r.DEKCreatedAt != 0 || r.Storage != format.StorageRaw {
			t.Errorf("file tombstone kept content: %+v", r)
		}
		if r.Revision != 2 || r.LastWriter != deviceID || r.ModifiedAt == 0 {
			t.Errorf("file tombstone's merge fields: %+v", r)
		}
	}
	// R32, the directory rule: one field short — modified_at is the folder's
	// own time and no change advances it.
	for _, d := range []DirInfo{top, mid, deep} {
		r := a.tree.dirRec(d.ID)
		if r == nil || r.State != format.FileTombstone {
			t.Fatalf("directory %x: %+v", d.ID, r)
		}
		if r.DirID != d.ID || r.ParentID != d.ParentID || r.Name != d.Name {
			t.Errorf("directory tombstone lost its shape: %+v", r)
		}
		if r.ModifiedAt != d.ModifiedAt {
			t.Errorf("directory tombstone advanced modified_at: %d → %d", d.ModifiedAt, r.ModifiedAt)
		}
		if r.Revision != 2 || r.LastWriter != deviceID {
			t.Errorf("directory tombstone's merge fields: %+v", r)
		}
	}
	// Nothing may be staged beneath a tombstone, and the whole tree is gone
	// from every listing.
	if _, _, err := a.Children(mid.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("children of a tombstoned folder: %v", err)
	}
	if _, _, err := a.Add(ctx, deep.ID, "late.bin", bytes.NewReader([]byte("x")), 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("add under a tombstone: %v", err)
	}
	if _, _, err := a.AddDir(ctx, deep.ID, "late", 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("mkdir under a tombstone: %v", err)
	}
	if _, err := a.Move(ctx, keep.ID, deep.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("move under a tombstone: %v", err)
	}
	if _, err := a.Delete(ctx, top.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete twice: %v", err)
	}
	// A tombstone reserves no name: the folder and the file come back.
	again := mkdir(t, a, root, "top", 1700006000)
	if again.ID == top.ID {
		t.Error("a new record reused a tombstone's id")
	}
	add(t, a, again.ID, "a.bin", []byte("fresh"))
	a.Close()

	b := fx.open(t)
	if len(b.Dirs()) != 1 || len(b.Files()) != 2 {
		t.Fatalf("after reopen: %d dirs %d files", len(b.Dirs()), len(b.Files()))
	}
	if len(b.index.Dirs) != 4 || len(b.index.Files) != 5 {
		t.Fatalf("tombstones dropped: %d dir records, %d file records", len(b.index.Dirs), len(b.index.Files))
	}
}

// TestRenameOneRecord: a folder rename is one record whatever hangs beneath
// it, and every descendant's path follows from the tree.
func TestRenameOneRecord(t *testing.T) {
	a, fx := newFixture(t, Options{})
	dir := mkdir(t, a, root, "before", 1700007000)
	sub := mkdir(t, a, dir.ID, "sub", 1700008000)
	child := add(t, a, sub.ID, "child.txt", text(5000, 210))
	sibling := add(t, a, root, "sibling.txt", text(1000, 211))
	if got := pathOf(t, a, child.ID); got != "before/sub/child.txt" {
		t.Fatalf("path %q", got)
	}
	if _, err := a.Rename(ctx, dir.ID, "after"); err != nil {
		t.Fatal(err)
	}
	if got := pathOf(t, a, child.ID); got != "after/sub/child.txt" {
		t.Errorf("path after the rename: %q", got)
	}
	// One record written: the descendants are untouched, and so is the
	// folder's own time.
	d, _ := a.InfoDir(dir.ID)
	if d.Name != "after" || d.Revision != 2 || d.LastWriter != deviceID || d.ModifiedAt != 1700007000 {
		t.Errorf("renamed folder: %+v", d)
	}
	if s, _ := a.InfoDir(sub.ID); s.Revision != 1 || s.ModifiedAt != 1700008000 {
		t.Errorf("a descendant folder was rewritten: %+v", s)
	}
	if c, _ := a.Info(child.ID); c.Revision != 1 || c.ModifiedAt != child.ModifiedAt {
		t.Errorf("a descendant file was rewritten: %+v", c)
	}
	// A rename writes name, revision and last_writer, and a move writes
	// parent_id, revision and last_writer; neither writes modified_at, for
	// either kind (APP.md §3).
	if _, err := a.Rename(ctx, sibling.ID, "renamed.txt"); err != nil {
		t.Fatal(err)
	}
	if s, _ := a.Info(sibling.ID); s.Name != "renamed.txt" || s.Revision != 2 || s.ModifiedAt != sibling.ModifiedAt || s.LastWriter != deviceID {
		t.Errorf("renamed file: %+v, was %+v", s, sibling)
	}
	if _, err := a.Move(ctx, sibling.ID, sub.ID); err != nil {
		t.Fatal(err)
	}
	if s, _ := a.Info(sibling.ID); s.ParentID != sub.ID || s.Revision != 3 || s.ModifiedAt != sibling.ModifiedAt {
		t.Errorf("moved file: %+v", s)
	}
	if _, err := a.Move(ctx, sibling.ID, root); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Rename(ctx, sibling.ID, "sibling.txt"); err != nil {
		t.Fatal(err)
	}
	// Rename refusals, both kinds, per parent and folded.
	if _, err := a.Rename(ctx, dir.ID, "sibling.txt"); !errors.Is(err, ErrExists) {
		t.Errorf("folder onto a live file's name: %v", err)
	}
	if _, err := a.Rename(ctx, sibling.ID, "AFTER"); !errors.Is(err, ErrExists) {
		t.Errorf("file onto a folded folder name: %v", err)
	}
	if _, err := a.Rename(ctx, child.ID, "sibling.txt"); err != nil {
		t.Errorf("the same name under another parent: %v", err)
	}
	if _, err := a.Rename(ctx, child.ID, "child.txt"); err != nil {
		t.Fatal(err)
	}
	// A record is not its own sibling: a change of case alone is a rename.
	if _, err := a.Rename(ctx, dir.ID, "AFTER"); err != nil {
		t.Fatalf("case-only rename: %v", err)
	}
	if d, _ := a.InfoDir(dir.ID); d.Name != "AFTER" || d.Revision != 3 {
		t.Errorf("case-only rename: %+v", d)
	}
	if _, err := a.Rename(ctx, dir.ID, "CON"); !errors.Is(err, format.ErrInvalid) {
		t.Errorf("reserved device name: %v", err)
	}
	if _, err := a.Rename(ctx, rnd16(t), "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("rename an unknown id: %v", err)
	}
	a.Close()

	b := fx.open(t)
	if got := pathOf(t, b, child.ID); got != "AFTER/sub/child.txt" {
		t.Errorf("path after reopen: %q", got)
	}
}

// TestRenameDirectoryRechecksSubtreePaths: renaming a folder lengthens every
// descendant's path, so the bound is the subtree's and not the record's
// (R39). Two-sided: 4096 accepted, 4097 refused.
func TestRenameDirectoryRechecksSubtreePaths(t *testing.T) {
	a, _ := newFixture(t, Options{})
	tx, err := a.Begin()
	if err != nil {
		t.Fatal(err)
	}
	// Twenty folders of 200 bytes: the deepest folder's children start at
	// 20 × 201 = 4020 bytes.
	name := func(i int) string { return strings.Repeat(fmt.Sprintf("%c", 'a'+i), 200) }
	ids := chain(t, tx, root, 20, name)
	deepest := ids[len(ids)-1]
	// A file of 76 bytes joins to exactly 4096.
	if _, err := tx.Add(ctx, deepest, strings.Repeat("f", 76), bytes.NewReader([]byte("x")), 1); err != nil {
		t.Fatalf("a path of exactly %d: %v", format.MaxPathLen, err)
	}
	if _, err := tx.Add(ctx, deepest, strings.Repeat("g", 77), bytes.NewReader([]byte("x")), 1); !errors.Is(err, ErrTreeBounds) {
		t.Fatalf("a path of %d: %v", format.MaxPathLen+1, err)
	}
	if _, err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	// The top folder renamed one byte longer would push the deepest file to
	// 4097; the same length is fine.
	if _, err := a.Rename(ctx, ids[0], strings.Repeat("z", 201)); !errors.Is(err, ErrTreeBounds) {
		t.Errorf("a rename lengthening the subtree past the bound: %v", err)
	}
	if _, err := a.Rename(ctx, ids[0], strings.Repeat("z", 200)); err != nil {
		t.Errorf("a rename of the same length: %v", err)
	}
	// A shorter name frees room for every record beneath, and the next
	// lengthening is measured against what the subtree looks like now.
	if _, err := a.Rename(ctx, ids[0], "z"); err != nil {
		t.Errorf("a shorter rename: %v", err)
	}
	if _, err := a.Rename(ctx, ids[0], strings.Repeat("y", 200)); err != nil {
		t.Errorf("a rename back to the old length: %v", err)
	}
}

// TestMove: one record re-parented whatever hangs beneath it, and every
// refusal R39 names — into itself, into a descendant, onto a folded name,
// past the depth bound and past the path bound.
func TestMove(t *testing.T) {
	a, fx := newFixture(t, Options{})
	src := mkdir(t, a, root, "src", 1700009000)
	dst := mkdir(t, a, root, "dst", 1700010000)
	inner := mkdir(t, a, src.ID, "inner", 1700011000)
	leaf := add(t, a, inner.ID, "leaf.txt", text(4000, 220))
	loose := add(t, a, root, "loose.txt", text(1000, 221))

	if _, err := a.Move(ctx, src.ID, dst.ID); err != nil {
		t.Fatal(err)
	}
	if got := pathOf(t, a, leaf.ID); got != "dst/src/inner/leaf.txt" {
		t.Errorf("path after the move: %q", got)
	}
	// One record written; the subtree travelled without being rewritten.
	d, _ := a.InfoDir(src.ID)
	if d.ParentID != dst.ID || d.Revision != 2 || d.LastWriter != deviceID || d.ModifiedAt != 1700009000 {
		t.Errorf("moved folder: %+v", d)
	}
	if i, _ := a.InfoDir(inner.ID); i.Revision != 1 || i.ParentID != src.ID {
		t.Errorf("a descendant folder was rewritten: %+v", i)
	}
	if l, _ := a.Info(leaf.ID); l.Revision != 1 || l.ModifiedAt != leaf.ModifiedAt {
		t.Errorf("a descendant file was rewritten: %+v", l)
	}
	// A record already under the destination is a no-op.
	before := a.Seq()
	if _, err := a.Move(ctx, src.ID, dst.ID); err != nil {
		t.Errorf("re-move: %v", err)
	}
	if a.Seq() != before {
		t.Error("a no-op move committed")
	}
	// Into itself, into a descendant, and onto a folded name.
	if _, err := a.Move(ctx, src.ID, src.ID); !errors.Is(err, ErrMoveIntoSelf) {
		t.Errorf("into itself: %v", err)
	}
	if _, err := a.Move(ctx, src.ID, inner.ID); !errors.Is(err, ErrMoveIntoSelf) {
		t.Errorf("into a descendant: %v", err)
	}
	if _, err := a.Move(ctx, dst.ID, inner.ID); !errors.Is(err, ErrMoveIntoSelf) {
		t.Errorf("into a deeper descendant: %v", err)
	}
	if _, err := a.Move(ctx, loose.ID, rnd16(t)); !errors.Is(err, ErrNotFound) {
		t.Errorf("into an unknown destination: %v", err)
	}
	if _, err := a.Move(ctx, loose.ID, leaf.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("into a file: %v", err)
	}
	if _, err := a.Move(ctx, rnd16(t), dst.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("an unknown record: %v", err)
	}
	clash := add(t, a, dst.ID, "LOOSE.TXT", text(500, 222))
	if _, err := a.Move(ctx, loose.ID, dst.ID); !errors.Is(err, ErrExists) {
		t.Errorf("onto a folded name at the destination: %v", err)
	}
	if _, err := a.Delete(ctx, clash.ID); err != nil {
		t.Fatal(err)
	}
	// A tombstone is not a live sibling, so the name is free now.
	if _, err := a.Move(ctx, loose.ID, dst.ID); err != nil {
		t.Errorf("onto a tombstone's name: %v", err)
	}
	a.Close()

	b := fx.open(t)
	if got := pathOf(t, b, leaf.ID); got != "dst/src/inner/leaf.txt" {
		t.Errorf("path after reopen: %q", got)
	}
	if got := pathOf(t, b, loose.ID); got != "dst/loose.txt" {
		t.Errorf("moved file after reopen: %q", got)
	}
}

// TestMoveRefusesSubtreeBounds: the two bounds a move must answer for the
// whole subtree it carries, each proved on both sides (R39).
func TestMoveRefusesSubtreeBounds(t *testing.T) {
	a, _ := newFixture(t, Options{})
	tx, err := a.Begin()
	if err != nil {
		t.Fatal(err)
	}
	// A chain 250 deep, and a six-deep subtree standing at the root.
	deep := chain(t, tx, root, 250, func(i int) string { return fmt.Sprintf("d%d", i) })
	sub := chain(t, tx, root, 6, func(i int) string { return fmt.Sprintf("s%d", i) })
	if _, err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	// Under the 250th folder the subtree's deepest would stand at 256.
	if _, err := a.Move(ctx, sub[0], deep[249]); !errors.Is(err, ErrTreeBounds) {
		t.Fatalf("depth %d: %v", format.MaxTreeDepth+1, err)
	}
	// Under the 249th it stands at exactly 255.
	if _, err := a.Move(ctx, sub[0], deep[248]); err != nil {
		t.Fatalf("depth %d: %v", format.MaxTreeDepth, err)
	}
	if _, err := a.Move(ctx, sub[0], root); err != nil {
		t.Fatal(err)
	}

	// The path bound. Twenty folders of 200 bytes give a prefix of 4020; a
	// file two bytes and its own name below that lands on 4096 or 4097.
	b, _ := newFixture(t, Options{})
	tx, err = b.Begin()
	if err != nil {
		t.Fatal(err)
	}
	long := chain(t, tx, root, 20, func(i int) string { return strings.Repeat(fmt.Sprintf("%c", 'a'+i), 200) })
	s, err := tx.AddDir(root, "s", 1700012000)
	if err != nil {
		t.Fatal(err)
	}
	fits, err := tx.Add(ctx, s.ID, strings.Repeat("f", 74), bytes.NewReader([]byte("x")), 1)
	if err != nil {
		t.Fatal(err)
	}
	over, err := tx.Add(ctx, s.ID, strings.Repeat("g", 75), bytes.NewReader([]byte("x")), 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Move(ctx, s.ID, long[19]); !errors.Is(err, ErrTreeBounds) {
		t.Fatalf("a subtree joining to %d bytes: %v", format.MaxPathLen+1, err)
	}
	// With the offending record one byte shorter the same move is fine, and
	// the record that already fitted is at exactly the bound.
	if _, err := b.Rename(ctx, over.ID, strings.Repeat("g", 74)); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Move(ctx, s.ID, long[19]); err != nil {
		t.Fatalf("a subtree joining to %d bytes: %v", format.MaxPathLen, err)
	}
	if got := pathOf(t, b, fits.ID); len(got) != format.MaxPathLen {
		t.Errorf("the deepest path is %d bytes", len(got))
	}
}

// TestStagedDirectoryIsARealParent: R39's sibling and parent rules are asked
// of the index the transaction is building, so a folder staged a moment ago
// is as real a parent — and as real a sibling — as any.
func TestStagedDirectoryIsARealParent(t *testing.T) {
	a, fx := newFixture(t, Options{})
	other := mkdir(t, a, root, "other", 1700013000)
	victim := add(t, a, other.ID, "Photos", text(500, 230))
	tx, err := a.Begin()
	if err != nil {
		t.Fatal(err)
	}
	staged, err := tx.AddDir(root, "Photos", 1700014000)
	if err != nil {
		t.Fatal(err)
	}
	// A file added into it needs no commit in between.
	if _, err := tx.Add(ctx, staged.ID, "inside.txt", bytes.NewReader(text(300, 231)), 300); err != nil {
		t.Fatalf("add into a staged folder: %v", err)
	}
	// Every operation that gives a record a name or a parent folds against it.
	if _, err := tx.Add(ctx, root, "photos", bytes.NewReader([]byte("x")), 1); !errors.Is(err, ErrExists) {
		t.Errorf("add: %v", err)
	}
	if _, err := tx.AddDir(root, "PHOTOS", 1); !errors.Is(err, ErrExists) {
		t.Errorf("mkdir: %v", err)
	}
	if err := tx.Rename(other.ID, "pHoToS"); !errors.Is(err, ErrExists) {
		t.Errorf("rename: %v", err)
	}
	if err := tx.Move(victim.ID, root); !errors.Is(err, ErrExists) {
		t.Errorf("move: %v", err)
	}
	// The refusals staged nothing.
	if err := tx.Move(victim.ID, staged.ID); err != nil {
		t.Fatalf("move into the staged folder: %v", err)
	}
	if _, err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	a.Close()

	b := fx.open(t)
	if got := pathOf(t, b, victim.ID); got != "Photos/Photos" {
		t.Errorf("path after the commit: %q", got)
	}
	if dirs, files, err := b.Children(root); err != nil || len(dirs) != 2 || len(files) != 0 {
		t.Fatalf("root: %d dirs %d files %v", len(dirs), len(files), err)
	}
	if _, files, _ := b.Children(staged.ID); len(files) != 2 {
		t.Errorf("staged folder holds %d files", len(files))
	}
}

// TestCompactAndRotateKeepTheTree: neither operation changes an id or the
// order of the record tables, so the tree a caller was holding is the tree it
// gets back (R33).
func TestCompactAndRotateKeepTheTree(t *testing.T) {
	a, fx := newFixture(t, Options{})
	top := mkdir(t, a, root, "top", 1700015000)
	sub := mkdir(t, a, top.ID, "sub", 1700016000)
	gone := mkdir(t, a, root, "gone", 1700017000)
	kept := add(t, a, sub.ID, "kept.bin", noise(40000, 240))
	add(t, a, top.ID, "second.bin", text(20000, 241))
	dropped := add(t, a, root, "dropped.bin", noise(50000, 242))
	if _, err := a.Delete(ctx, dropped.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Delete(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}
	order := func(x *format.Index) string {
		var b strings.Builder
		for i := range x.Dirs {
			fmt.Fprintf(&b, "d%x:%d ", x.Dirs[i].DirID, x.Dirs[i].State)
		}
		for i := range x.Files {
			fmt.Fprintf(&b, "f%x:%d ", x.Files[i].FileID, x.Files[i].State)
		}
		return b.String()
	}
	want := order(a.index)

	newKey := Key{KID: rnd16(t), Key: rnd32(t)}
	if _, err := a.RotateKey(ctx, newKey.KID, newKey.Key); err != nil {
		t.Fatal(err)
	}
	if got := order(a.index); got != want {
		t.Errorf("rotation changed the record tables:\n got %s\nwant %s", got, want)
	}
	if p := pathOf(t, a, kept.ID); p != "top/sub/kept.bin" {
		t.Errorf("path after rotation: %q", p)
	}
	if _, _, err := a.Compact(ctx, nil); err != nil {
		t.Fatal(err)
	}
	fx.key = newKey
	b := fx.open(t)
	if got := order(b.index); got != want {
		t.Errorf("compaction changed the record tables:\n got %s\nwant %s", got, want)
	}
	if p := pathOf(t, b, kept.ID); p != "top/sub/kept.bin" {
		t.Errorf("path after compaction: %q", p)
	}
	if got := extract(t, b, kept.ID); !bytes.Equal(got, noise(40000, 240)) {
		t.Error("content after compaction")
	}
	if d, ok := b.InfoDir(sub.ID); !ok || d.ModifiedAt != 1700016000 || d.ParentID != top.ID {
		t.Errorf("directory record after compaction: %+v %v", d, ok)
	}
}

// TestCommitRefusesInvalidTree: the encoder is the backstop R39 asks for. It
// is driven here through a working index broken behind the transaction's
// back, because every path that reaches it legitimately refuses first.
func TestCommitRefusesInvalidTree(t *testing.T) {
	a, _ := newFixture(t, Options{})
	dir := mkdir(t, a, root, "dir", 1700018000)
	for name, breakIt := range map[string]func(x *format.Index){
		"a file under an unknown parent": func(x *format.Index) {
			x.Files[len(x.Files)-1].ParentID = [16]byte{9, 9, 9}
		},
		"a directory under itself": func(x *format.Index) {
			x.Dirs[0].ParentID = x.Dirs[0].DirID
		},
		"two live siblings folding onto one name": func(x *format.Index) {
			x.Files[len(x.Files)-1].Name = "CLASH"
			x.Dirs[0].Name = "clash"
			x.Dirs[0].ParentID, x.Files[len(x.Files)-1].ParentID = format.RootID, format.RootID
		},
		"an id in both tables": func(x *format.Index) {
			x.Files[len(x.Files)-1].FileID = x.Dirs[0].DirID
		},
	} {
		tx, err := a.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Add(ctx, dir.ID, "f.txt", bytes.NewReader(text(1000, 250)), 1000); err != nil {
			t.Fatal(err)
		}
		breakIt(tx.index)
		if _, err := tx.Commit(ctx); !errors.Is(err, format.ErrInvalid) {
			t.Errorf("%s: sealed with %v", name, err)
		}
		// The refusal is before the commit point: the archive is usable and
		// the transaction gone.
		if err := a.Broken(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if a.tx != nil {
			t.Errorf("%s: the transaction outlived the refusal", name)
		}
	}
	// And it still works.
	f := add(t, a, dir.ID, "f.txt", text(1000, 250))
	if got := pathOf(t, a, f.ID); got != "dir/f.txt" {
		t.Errorf("path %q", got)
	}
}

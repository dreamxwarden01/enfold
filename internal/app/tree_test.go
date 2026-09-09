package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The archive page over a tree (APP.md §2.3, §3; FORMAT.md R39): ids at the
// boundary, the merged view of the snapshot and the overlay, and the
// operations that read and write it. Nothing here speaks a path except as
// something the tree derives.

// rootID is the archive's root as the boundary spells it: the all-zero id,
// 32 lowercase hex digits, the same value the format writes as a top-level
// record's parent_id.
const rootID = "00000000000000000000000000000000"

func rowsByName(p Page) map[string]FileRow {
	m := make(map[string]FileRow, len(p.Rows))
	for _, r := range p.Rows {
		m[r.Name] = r
	}
	return m
}

// openArchive creates an archive and opens it.
func (h *harness) openArchive(t *testing.T, name string) string {
	t.Helper()
	id, _ := h.newArchive(name)
	if _, e := h.c.OpenArchive(id); e != nil {
		t.Fatalf("open %s: %v", name, e)
	}
	return id
}

// page reads one directory, failing the test if it cannot be listed.
func (h *harness) page(t *testing.T, id, dirID string) Page {
	t.Helper()
	p, e := h.c.Page(id, dirID, "name", 0, 500)
	if e != nil {
		t.Fatalf("page of %s: %v", dirID, e)
	}
	return p
}

// row finds one row of a directory by name.
func (h *harness) row(t *testing.T, id, dirID, name string) FileRow {
	t.Helper()
	r, ok := rowsByName(h.page(t, id, dirID))[name]
	if !ok {
		t.Fatalf("no row %q under %s", name, dirID)
	}
	return r
}

// src writes a source file and returns its path.
func (h *harness) src(t *testing.T, rel, content string) string {
	t.Helper()
	p := filepath.Join(h.dir, "src", filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// add stages files under parentID and waits for the operation.
func (h *harness) add(t *testing.T, id, parentID string, policy AddPolicy, paths ...string) OpView {
	t.Helper()
	opID, e := h.c.AddFiles(id, parentID, paths, policy)
	if e != nil {
		t.Fatalf("add files: %v", e)
	}
	return h.rec.waitOp(t, opID)
}

// addFolder walks a source folder into parentID and waits.
func (h *harness) addFolder(t *testing.T, id, parentID, dir string, policy AddPolicy) OpView {
	t.Helper()
	opID, e := h.c.AddFolder(id, parentID, dir, policy)
	if e != nil {
		t.Fatalf("add folder: %v", e)
	}
	return h.rec.waitOp(t, opID)
}

func (h *harness) save(t *testing.T, id string) {
	t.Helper()
	opID, e := h.c.Save(id)
	if e != nil {
		t.Fatalf("save: %v", e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != "" {
		t.Fatalf("save op: %+v", o)
	}
}

// outDir is a fresh extraction destination. Extracted files go under
// GOTMPDIR when it is set: on the dev machine that directory is excluded
// from the antivirus, whose scan of a fresh file otherwise holds it open
// while the temp dir is being removed.
func outDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp(os.Getenv("GOTMPDIR"), "enfold-extract")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

// A page is one directory of the tree: its own children, their joined paths,
// the sum beneath a folder, and the breadcrumb from the root down to it,
// root-inclusive and carrying the archive's name.
func TestPageOverATreeWithCrumbs(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Tree")

	a, e := h.c.CreateFolder(id, rootID, "a")
	if e != nil {
		t.Fatalf("create a: %v", e)
	}
	b, e := h.c.CreateFolder(id, a, "b")
	if e != nil {
		t.Fatalf("create b: %v", e)
	}
	h.add(t, id, b, PolicySkip, h.src(t, "deep.txt", "0123456789"))
	h.add(t, id, rootID, PolicySkip, h.src(t, "top.txt", "top"))
	h.save(t, id)

	root := h.page(t, id, rootID)
	if root.Total != 2 || len(root.Rows) != 2 {
		t.Fatalf("root page: %+v", root)
	}
	names := rowsByName(root)
	// Folders sort first; a folder's Size is the sum beneath it and its
	// Total counts children, never the subtree.
	if !names["a"].IsDir || names["a"].Size != 10 || names["a"].Path != "a" || names["a"].ParentID != rootID {
		t.Fatalf("folder row: %+v", names["a"])
	}
	if names["top.txt"].IsDir || names["top.txt"].Path != "top.txt" || names["top.txt"].Storage == "" {
		t.Fatalf("file row: %+v", names["top.txt"])
	}
	if len(root.Crumbs) != 1 || root.Crumbs[0].ID != rootID || root.Crumbs[0].Name != "Tree" {
		t.Fatalf("root crumbs: %+v", root.Crumbs)
	}
	deep := h.page(t, id, b)
	if deep.Total != 1 || deep.Rows[0].Path != "a/b/deep.txt" || deep.Rows[0].ParentID != b {
		t.Fatalf("deep page: %+v", deep.Rows)
	}
	if len(deep.Crumbs) != 3 || deep.Crumbs[0].ID != rootID || deep.Crumbs[1].ID != a || deep.Crumbs[2].ID != b {
		t.Fatalf("deep crumbs: %+v", deep.Crumbs)
	}
	// An empty folder is not a folder that went: zero rows, Total zero.
	empty, e := h.c.CreateFolder(id, rootID, "empty")
	if e != nil {
		t.Fatal(e)
	}
	if p := h.page(t, id, empty); p.Total != 0 || len(p.Rows) != 0 || len(p.Crumbs) != 2 {
		t.Fatalf("empty folder page: %+v", p)
	}
}

// A dirID the page still holds can be gone — Discard drops what a
// transaction staged, a Delete takes a subtree the page may be standing in —
// and is told so, never given an empty listing under a breadcrumb that still
// names the place.
func TestAPageWhoseFolderWentIsToldSo(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Gone")

	staged, e := h.c.CreateFolder(id, rootID, "staged")
	if e != nil {
		t.Fatal(e)
	}
	h.page(t, id, staged) // enterable while it is staged
	if e := h.c.Discard(id); e != nil {
		t.Fatal(e)
	}
	if _, e := h.c.Page(id, staged, "name", 0, 10); !isCode(e, CodeFileNotFound) {
		t.Fatalf("a folder Discard dropped: %v", e)
	}
	kept, _ := h.c.CreateFolder(id, rootID, "kept")
	h.save(t, id)
	if e := h.c.DeleteRecords(id, []string{kept}); e != nil {
		t.Fatal(e)
	}
	if _, e := h.c.Page(id, kept, "name", 0, 10); !isCode(e, CodeFileNotFound) {
		t.Fatalf("a folder staged for deletion: %v", e)
	}
	// An id that is not 32 hex digits is params, and one that never named a
	// record is file.not_found.
	if _, e := h.c.Page(id, "nothex", "name", 0, 10); !isCode(e, CodeParams) {
		t.Fatalf("a malformed id: %v", e)
	}
	if _, e := h.c.Page(id, strings.Repeat("ab", 16), "name", 0, 10); !isCode(e, CodeFileNotFound) {
		t.Fatalf("an unknown id: %v", e)
	}
}

// The root is a directory where a directory is named and params where a
// record is acted on: it is never renamed, moved, previewed or tombstoned.
func TestTheRootIsNamedButNeverActedOn(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Root")
	h.add(t, id, rootID, PolicySkip, h.src(t, "f.txt", "f"))
	h.save(t, id)
	f := h.row(t, id, rootID, "f.txt").ID

	if e := h.c.DeleteRecords(id, []string{rootID}); !isCode(e, CodeParams) {
		t.Fatalf("delete the root: %v", e)
	}
	if e := h.c.RenameRecord(id, rootID, "x"); !isCode(e, CodeParams) {
		t.Fatalf("rename the root: %v", e)
	}
	if e := h.c.MoveRecords(id, []string{rootID}, f); !isCode(e, CodeParams) {
		t.Fatalf("move the root: %v", e)
	}
	if _, e := h.c.PreviewURL(id, rootID); !isCode(e, CodeParams) {
		t.Fatalf("preview the root: %v", e)
	}
	if _, _, e := h.c.PreviewText(id, rootID, 16); !isCode(e, CodeParams) {
		t.Fatalf("preview the root as text: %v", e)
	}
	if _, e := h.c.ReplaceFile(id, rootID, h.src(t, "g.txt", "g")); !isCode(e, CodeParams) {
		t.Fatalf("replace the root: %v", e)
	}
	// Empty recordIDs is params, never everything.
	if _, e := h.c.Extract(id, nil, outDir(t), ExtractSkip); !isCode(e, CodeParams) {
		t.Fatalf("extract nothing: %v", e)
	}
	// A file's id where a directory is wanted is file.not_found.
	if _, e := h.c.Page(id, f, "name", 0, 10); !isCode(e, CodeFileNotFound) {
		t.Fatalf("page of a file: %v", e)
	}
	if _, e := h.c.CreateFolder(id, f, "x"); !isCode(e, CodeFileNotFound) {
		t.Fatalf("create under a file: %v", e)
	}
	if st, _ := h.c.Stat(id); st.Dirty != 0 {
		t.Fatalf("a refusal staged something: %+v", st)
	}
}

// A folder made in the app is a record: staged at once, listed, enterable,
// and it survives the save with nothing in it. Discard drops it.
func TestCreateFolderIsARecord(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Made")

	nid, e := h.c.CreateFolder(id, rootID, "Photos")
	if e != nil {
		t.Fatalf("create folder: %v", e)
	}
	if st, _ := h.c.Stat(id); st.Dirty != 1 || st.State != "dirty" {
		t.Fatalf("a staged folder is one change: %+v", st)
	}
	r := h.row(t, id, rootID, "Photos")
	if !r.IsDir || r.Pending != "added" || r.ID != nid {
		t.Fatalf("staged folder row: %+v", r)
	}
	// A second folder of a folded-equal name is refused; the name itself is
	// validated like any other's.
	if _, e := h.c.CreateFolder(id, rootID, "photos"); !isCode(e, CodeFileExists) {
		t.Fatalf("a folded collision: %v", e)
	}
	// CheckNames folds too — it is asked before anything is offered to the
	// archive, so the fold is the app's own.
	col, e := h.c.CheckNames(id, rootID, []string{"PHOTOS", "free"})
	if e != nil || len(col) != 1 || col[0].Name != "PHOTOS" || col[0].Existing != nid || !col[0].ExistingIsDir {
		t.Fatalf("CheckNames over a folded name: %+v %v", col, e)
	}
	if _, e := h.c.CreateFolder(id, rootID, "a/b"); !isCode(e, CodeFileName) {
		t.Fatalf("a name with a slash: %v", e)
	}
	if _, e := h.c.CreateFolder(id, rootID, "CON"); !isCode(e, CodeFileName) {
		t.Fatalf("a reserved device name: %v", e)
	}
	// It is a real parent while it is staged.
	inner, e := h.c.CreateFolder(id, nid, "2024")
	if e != nil {
		t.Fatalf("create under a staged folder: %v", e)
	}
	h.add(t, id, inner, PolicySkip, h.src(t, "p.txt", "pic"))
	h.save(t, id)
	if p := h.page(t, id, nid); p.Total != 1 || p.Rows[0].Name != "2024" || p.Rows[0].Pending != "" {
		t.Fatalf("after save: %+v", p.Rows)
	}
	// An empty folder made after the save survives its own commit.
	empty, _ := h.c.CreateFolder(id, rootID, "Empty")
	h.save(t, id)
	if p := h.page(t, id, empty); p.Total != 0 {
		t.Fatalf("the empty folder took something with it: %+v", p)
	}
	if h.row(t, id, rootID, "Empty").ID != empty {
		t.Fatal("the empty folder did not survive the save")
	}
	// Discard drops the folders that transaction staged.
	dropped, _ := h.c.CreateFolder(id, rootID, "Dropped")
	if e := h.c.Discard(id); e != nil {
		t.Fatal(e)
	}
	if _, ok := rowsByName(h.page(t, id, rootID))["Dropped"]; ok {
		t.Fatal("Discard kept a staged folder")
	}
	if _, e := h.c.Page(id, dropped, "name", 0, 10); !isCode(e, CodeFileNotFound) {
		t.Fatalf("the dropped folder is still enterable: %v", e)
	}
}

// AddFolder makes a record for every directory the walk creates, with the
// source folder's own time, and enters a directory that is already there
// whatever the policy — so one source folder is never split across two
// records and no policy tombstones a subtree the user was never shown.
func TestAddFolderCreatesAndEnters(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Walk")

	h.src(t, "pics/one.txt", "one")
	h.src(t, "pics/inner/deep.txt", "deep")
	pics := filepath.Join(h.dir, "src", "pics")
	// An empty subfolder is a record too.
	if err := os.MkdirAll(filepath.Join(pics, "blank"), 0o700); err != nil {
		t.Fatal(err)
	}
	when := time.Date(2021, 3, 4, 5, 6, 7, 0, time.UTC)
	for _, d := range []string{pics, filepath.Join(pics, "inner"), filepath.Join(pics, "blank")} {
		if err := os.Chtimes(d, when, when); err != nil {
			t.Fatal(err)
		}
	}
	o := h.addFolder(t, id, rootID, pics, PolicySkip)
	if o.Error != "" {
		t.Fatalf("walk: %+v", o)
	}
	counts := map[string]int{}
	for _, r := range o.Results {
		counts[r.Outcome]++
	}
	if counts["created"] != 3 || counts["added"] != 2 {
		t.Fatalf("outcomes: %+v", o.Results)
	}
	for _, r := range o.Results {
		if (r.Outcome == "created") != r.IsDir {
			t.Fatalf("a directory outcome without IsDir: %+v", r)
		}
	}
	h.save(t, id)
	top := h.row(t, id, rootID, "pics")
	if !top.IsDir || top.ModifiedAt != when.Unix() {
		t.Fatalf("the folder's own time was not kept: %+v", top)
	}
	if b := h.row(t, id, top.ID, "blank"); b.ModifiedAt != when.Unix() {
		t.Fatalf("an empty subfolder: %+v", b)
	}
	if p := h.page(t, id, h.row(t, id, top.ID, "blank").ID); p.Total != 0 {
		t.Fatalf("the empty subfolder is not empty: %+v", p)
	}

	// The same source again, with a new file in it: the folder is entered,
	// keeping its dir_id and its own time, and the policy goes on applying
	// to what the walk carries inside.
	h.src(t, "pics/two.txt", "two")
	o = h.addFolder(t, id, rootID, pics, PolicySkip)
	got := map[string]string{}
	for _, r := range o.Results {
		got[r.Name] = r.Outcome
	}
	if got["pics"] != "entered" || got["pics/one.txt"] != "skipped" || got["pics/two.txt"] != "added" {
		t.Fatalf("second walk: %+v", o.Results)
	}
	if got["pics/inner"] != "entered" || got["pics/blank"] != "entered" {
		t.Fatalf("subfolders were not entered: %+v", o.Results)
	}
	if st, _ := h.c.Stat(id); st.Dirty != 1 {
		t.Fatalf("entering staged something: %+v", st)
	}
	h.save(t, id)
	again := h.row(t, id, rootID, "pics")
	if again.ID != top.ID || again.ModifiedAt != when.Unix() {
		t.Fatalf("the entered folder is not the same record: %+v", again)
	}
	// What the walk carried inside landed inside, not beside.
	if r := h.row(t, id, top.ID, "two.txt"); r.ParentID != top.ID || r.Path != "pics/two.txt" {
		t.Fatalf("the new file did not land in the entered folder: %+v", r)
	}
	if _, beside := rowsByName(h.page(t, id, rootID))["two.txt"]; beside {
		t.Fatal("the new file landed beside the entered folder")
	}
	// Even under replace, the incoming directory is entered.
	o = h.addFolder(t, id, rootID, pics, PolicyReplace)
	for _, r := range o.Results {
		if r.IsDir && r.Outcome != "entered" {
			t.Fatalf("replace did not enter: %+v", r)
		}
	}
}

// Kinds that differ never replace, in either direction: skip leaves the item
// out with its subtree, keep-both takes the next free name, and replace
// fails that one item with file.kind_mismatch while the rest of the batch
// runs.
func TestKindsThatDifferNeverReplace(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Kinds")

	// A file called "pics" at the root, and a source folder of that name.
	h.add(t, id, rootID, PolicySkip, h.src(t, "pics", "a file, not a folder"))
	h.save(t, id)
	h.src(t, "tree/pics/one.txt", "one")
	pics := filepath.Join(h.dir, "src", "tree", "pics")

	o := h.addFolder(t, id, rootID, pics, PolicySkip)
	if len(o.Results) != 1 || o.Results[0].Outcome != "skipped" || !o.Results[0].IsDir {
		t.Fatalf("skip left the subtree in: %+v", o.Results)
	}
	o = h.addFolder(t, id, rootID, pics, PolicyReplace)
	if len(o.Results) != 1 || o.Results[0].Outcome != "failed" || o.Results[0].Code != CodeKindMismatch {
		t.Fatalf("replace across kinds: %+v", o.Results)
	}
	if st, _ := h.c.Stat(id); st.Dirty != 0 {
		t.Fatalf("a refused walk staged something: %+v", st)
	}
	// The other direction: a file offered where a folder stands.
	folder, _ := h.c.CreateFolder(id, rootID, "docs")
	h.save(t, id)
	_ = folder
	o = h.add(t, id, rootID, PolicyReplace, h.src(t, "docs", "a file"))
	if len(o.Results) != 1 || o.Results[0].Code != CodeKindMismatch {
		t.Fatalf("a file over a folder: %+v", o.Results)
	}
	// keep-both takes the next free name: " (2)" at the end of a whole
	// directory name, before the extension of a file's.
	o = h.addFolder(t, id, rootID, pics, PolicyKeepBoth)
	if o.Results[0].Name != "pics (2)" || o.Results[0].Outcome != "created" {
		t.Fatalf("keep-both for a folder: %+v", o.Results)
	}
	o = h.add(t, id, rootID, PolicyKeepBoth, h.src(t, "docs", "a file"))
	if o.Results[0].Name != "docs (2)" {
		t.Fatalf("keep-both for a file: %+v", o.Results)
	}
	h.save(t, id)
	// And a file with an extension keeps it.
	h.add(t, id, rootID, PolicySkip, h.src(t, "note.txt", "one"))
	h.save(t, id)
	o = h.add(t, id, rootID, PolicyKeepBoth, h.src(t, "note.txt", "one"))
	if o.Results[0].Name != "note (2).txt" {
		t.Fatalf("keep-both before the extension: %+v", o.Results)
	}
	h.save(t, id)
	// A source whose name differs from a live sibling's only in case is the
	// item already there: the pre-flight folds, so the policy decides it
	// rather than the archive refusing the add.
	up := h.src(t, "up/NOTE.TXT", "one")
	o = h.add(t, id, rootID, PolicySkip, up)
	if len(o.Results) != 1 || o.Results[0].Outcome != "skipped" {
		t.Fatalf("a folded name under skip: %+v", o.Results)
	}
	o = h.add(t, id, rootID, PolicyKeepBoth, up)
	if o.Results[0].Outcome != "added" || o.Results[0].Name != "NOTE (3).TXT" {
		t.Fatalf("a folded name under keep-both: %+v", o.Results)
	}
	h.save(t, id)
	// Two sources of one batch whose names fold onto each other: the second
	// meets what the first planned, and the policy decides it there too.
	fresh, _ := h.c.CreateFolder(id, rootID, "fresh")
	o = h.add(t, id, fresh, PolicySkip, h.src(t, "d1/dup.txt", "1"), h.src(t, "d2/DUP.TXT", "2"))
	if len(o.Results) != 2 || o.Results[0].Outcome != "added" || o.Results[1].Outcome != "skipped" {
		t.Fatalf("a folded pair in one batch: %+v", o.Results)
	}
	o = h.add(t, id, fresh, PolicyKeepBoth, h.src(t, "d2/DUP.TXT", "2"))
	if o.Results[0].Name != "fresh/DUP (2).TXT" {
		t.Fatalf("keep-both against a name this batch planned: %+v", o.Results)
	}
	names := rowsByName(h.page(t, id, rootID))
	for _, want := range []string{"pics", "pics (2)", "docs", "docs (2)", "note.txt", "note (2).txt"} {
		if _, ok := names[want]; !ok {
			t.Fatalf("missing %q: %+v", want, names)
		}
	}
	if names["pics (2)"].IsDir != true || names["docs (2)"].IsDir != false {
		t.Fatalf("kept-both kinds: %+v", names)
	}
}

// Deleting a directory is one staged change however large the subtree, and
// one row: the folder keeps its place marked deleted, greyed and not
// enterable, nothing beneath it is listed, and nothing may be staged into it.
// A tombstone reserves no name.
func TestDeleteOfADirectory(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Del")

	d, _ := h.c.CreateFolder(id, rootID, "d")
	inner, _ := h.c.CreateFolder(id, d, "inner")
	h.add(t, id, d, PolicySkip, h.src(t, "f.txt", "f"))
	h.add(t, id, inner, PolicySkip, h.src(t, "g.txt", "g"))
	h.save(t, id)

	// A staged add beneath a committed folder goes into the tombstoning:
	// its bytes are already in the file and the free map reclaims them at
	// the commit, and the entry the deletion swallows leaves the overlay.
	gid := h.row(t, id, inner, "g.txt").ID
	h.add(t, id, inner, PolicySkip, h.src(t, "later.txt", "later"))
	if e := h.c.DeleteRecords(id, []string{d}); e != nil {
		t.Fatalf("delete: %v", e)
	}
	if st, _ := h.c.Stat(id); st.Dirty != 1 {
		t.Fatalf("a deleted folder of five records is one change: %+v", st)
	}
	// Nothing beneath it is listed, previewed or extracted while the
	// deletion stands.
	if _, e := h.c.PreviewURL(id, gid); !isCode(e, CodeFileNotFound) {
		t.Fatalf("a record beneath a deleted folder is previewable: %v", e)
	}
	opID, e := h.c.Extract(id, []string{gid}, outDir(t), ExtractSkip)
	if e != nil {
		t.Fatal(e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != CodeFileNotFound {
		t.Fatalf("a record beneath a deleted folder is extractable: %+v", o)
	}
	row := h.row(t, id, rootID, "d")
	if row.Pending != "deleted" || !row.IsDir {
		t.Fatalf("the greyed row: %+v", row)
	}
	if _, e := h.c.Page(id, d, "name", 0, 10); !isCode(e, CodeFileNotFound) {
		t.Fatalf("a deleted folder is enterable: %v", e)
	}
	if _, e := h.c.Page(id, inner, "name", 0, 10); !isCode(e, CodeFileNotFound) {
		t.Fatalf("a folder beneath a deleted one: %v", e)
	}
	// Nothing may be added, created or moved into it or beneath it.
	if _, e := h.c.CreateFolder(id, d, "x"); !isCode(e, CodeFileNotFound) {
		t.Fatalf("create into a deleted folder: %v", e)
	}
	if _, e := h.c.AddFiles(id, d, []string{h.src(t, "h.txt", "h")}, PolicySkip); !isCode(e, CodeFileNotFound) {
		t.Fatalf("add into a deleted folder: %v", e)
	}
	if _, e := h.c.AddFolder(id, inner, filepath.Join(h.dir, "src"), PolicySkip); !isCode(e, CodeFileNotFound) {
		t.Fatalf("add a folder beneath a deleted one: %v", e)
	}
	other, _ := h.c.CreateFolder(id, rootID, "other")
	if e := h.c.MoveRecords(id, []string{other}, d); !isCode(e, CodeFileNotFound) {
		t.Fatalf("move into a deleted folder: %v", e)
	}
	// A tombstone is not a live sibling, so the name is free again.
	again, e := h.c.CreateFolder(id, rootID, "d")
	if e != nil {
		t.Fatalf("a new folder beside the tombstone: %v", e)
	}
	if col, _ := h.c.CheckNames(id, rootID, []string{"d"}); len(col) != 1 || col[0].Existing != again {
		t.Fatalf("CheckNames counted the staged-deleted sibling: %+v", col)
	}
	h.save(t, id)
	names := rowsByName(h.page(t, id, rootID))
	// The tombstoned folder is gone; the new one of that name and the
	// folder made beside it are what the save published.
	if len(names) != 2 || names["d"].ID != again || names["other"].ID == "" {
		t.Fatalf("after the save: %+v", names)
	}
	if _, e := h.c.Page(id, d, "name", 0, 10); !isCode(e, CodeFileNotFound) {
		t.Fatalf("the tombstoned folder came back: %v", e)
	}
}

// Deleting a record whose whole existence is staged un-stages it instead of
// tombstoning: the records staged under it go with it, and a committed
// record that was moved into it goes back where the move found it, its move
// un-staged too — a rename staged with that move still stands.
func TestUnstagingAStagedFolderReturnsWhatWasMovedIn(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Unstage")

	h.add(t, id, rootID, PolicySkip, h.src(t, "keep.txt", "k"), h.src(t, "moved.txt", "m"))
	h.save(t, id)
	moved := h.row(t, id, rootID, "moved.txt").ID

	nid, e := h.c.CreateFolder(id, rootID, "new")
	if e != nil {
		t.Fatal(e)
	}
	inside, _ := h.c.CreateFolder(id, nid, "inside")
	h.add(t, id, inside, PolicySkip, h.src(t, "staged.txt", "s"))
	if e := h.c.MoveRecords(id, []string{moved}, nid); e != nil {
		t.Fatalf("move in: %v", e)
	}
	if h.row(t, id, nid, "moved.txt").Pending != "moved" {
		t.Fatal("the move was not staged as moved")
	}
	if e := h.c.RenameRecord(id, moved, "renamed.txt"); e != nil {
		t.Fatalf("rename: %v", e)
	}
	if r := h.row(t, id, nid, "renamed.txt"); r.Pending != "moved" {
		t.Fatalf("a record both renamed and moved reads moved: %+v", r)
	}
	// Un-stage the folder.
	if e := h.c.DeleteRecords(id, []string{nid}); e != nil {
		t.Fatalf("un-stage: %v", e)
	}
	names := rowsByName(h.page(t, id, rootID))
	if _, still := names["new"]; still {
		t.Fatalf("the staged folder stayed: %+v", names)
	}
	back, ok := names["renamed.txt"]
	if !ok || back.ID != moved || back.ParentID != rootID {
		t.Fatalf("the moved record did not come back: %+v", names)
	}
	if back.Pending != "renamed" {
		t.Fatalf("the move was not un-staged: %+v", back)
	}
	if st, _ := h.c.Stat(id); st.Dirty != 1 {
		t.Fatalf("only the rename is left: %+v", st)
	}
	// The records staged under the folder went with it.
	if _, e := h.c.Page(id, inside, "name", 0, 10); !isCode(e, CodeFileNotFound) {
		t.Fatalf("a folder staged inside stayed: %v", e)
	}
	h.save(t, id)
	if h.row(t, id, rootID, "renamed.txt").ID != moved {
		t.Fatal("the rename did not survive")
	}
	if p := h.page(t, id, rootID); p.Total != 2 {
		t.Fatalf("what the save published: %+v", p.Rows)
	}
}

// Rename and Move are pre-flighted against R39 on the merged view before
// anything is staged, with the four codes of APP.md §3.
func TestRenameAndMoveRefusals(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Refuse")

	a, _ := h.c.CreateFolder(id, rootID, "a")
	b, _ := h.c.CreateFolder(id, a, "b")
	h.add(t, id, rootID, PolicySkip, h.src(t, "one.txt", "1"), h.src(t, "two.txt", "2"))
	h.save(t, id)
	one := h.row(t, id, rootID, "one.txt").ID
	two := h.row(t, id, rootID, "two.txt").ID

	// file.exists: a name a live sibling already holds under case folding.
	if e := h.c.RenameRecord(id, one, "TWO.TXT"); !isCode(e, CodeFileExists) {
		t.Fatalf("rename onto a folded sibling: %v", e)
	}
	// A change of case alone is a rename, never a collision.
	if e := h.c.RenameRecord(id, one, "One.TXT"); e != nil {
		t.Fatalf("a case-only rename: %v", e)
	}
	// file.name, and file.not_found for an id that is not live.
	if e := h.c.RenameRecord(id, one, "x/y"); !isCode(e, CodeFileName) {
		t.Fatalf("a name with a slash: %v", e)
	}
	if e := h.c.RenameRecord(id, strings.Repeat("cd", 16), "z"); !isCode(e, CodeFileNotFound) {
		t.Fatalf("rename an unknown id: %v", e)
	}
	// file.move_into_self, both ways.
	if e := h.c.MoveRecords(id, []string{a}, a); !isCode(e, CodeMoveIntoSelf) {
		t.Fatalf("move into itself: %v", e)
	}
	if e := h.c.MoveRecords(id, []string{a}, b); !isCode(e, CodeMoveIntoSelf) {
		t.Fatalf("move into a descendant: %v", e)
	}
	// file.exists at the destination, and two of one batch taking one name.
	if e := h.c.MoveRecords(id, []string{two}, a); e != nil {
		t.Fatalf("move: %v", e)
	}
	h.save(t, id)
	h.add(t, id, rootID, PolicySkip, h.src(t, "two.txt", "2"))
	h.save(t, id)
	two2 := h.row(t, id, rootID, "two.txt").ID
	if e := h.c.MoveRecords(id, []string{two2}, a); !isCode(e, CodeFileExists) {
		t.Fatalf("move onto a held name: %v", e)
	}
	// file.not_found for a destination that is not the root or a live
	// directory, and for a record that is not live.
	if e := h.c.MoveRecords(id, []string{two2}, one); !isCode(e, CodeFileNotFound) {
		t.Fatalf("move onto a file: %v", e)
	}
	if e := h.c.MoveRecords(id, []string{strings.Repeat("ef", 16)}, a); !isCode(e, CodeFileNotFound) {
		t.Fatalf("move an unknown id: %v", e)
	}
	// A record already under the destination is a no-op, not an error.
	if e := h.c.MoveRecords(id, []string{two2}, rootID); e != nil {
		t.Fatalf("a no-op move: %v", e)
	}
	if st, _ := h.c.Stat(id); st.Dirty != 0 {
		t.Fatalf("a refused or no-op batch staged something: %+v", st)
	}
}

// A record whose own ancestor is in the same batch travels with that
// ancestor and is dropped from the batch.
func TestMoveBatchAbsorbsDescendants(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Absorb")

	a, _ := h.c.CreateFolder(id, rootID, "a")
	b, _ := h.c.CreateFolder(id, a, "b")
	h.add(t, id, b, PolicySkip, h.src(t, "f.txt", "f"))
	c, _ := h.c.CreateFolder(id, rootID, "c")
	h.save(t, id)

	if e := h.c.MoveRecords(id, []string{a, b}, c); e != nil {
		t.Fatalf("move: %v", e)
	}
	// One record written, not two: b is still under a.
	if st, _ := h.c.Stat(id); st.Dirty != 1 {
		t.Fatalf("the descendant was moved too: %+v", st)
	}
	h.save(t, id)
	if r := h.row(t, id, c, "a"); r.ID != a {
		t.Fatalf("a is not under c: %+v", r)
	}
	if r := h.row(t, id, a, "b"); r.ID != b || r.ParentID != a {
		t.Fatalf("b left its parent: %+v", r)
	}
	if p := h.page(t, id, b); p.Total != 1 || p.Rows[0].Path != "c/a/b/f.txt" {
		t.Fatalf("the subtree's paths: %+v", p.Rows)
	}
}

// The depth and joined-path bounds are the moved or renamed subtree's, not
// the named record's: the record the caller named always looks fine, so both
// are caught in the pre-flight rather than at the seal (FORMAT.md R39).
func TestRenameAndMoveHoldTheSubtreeBounds(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Bounds")

	// A one-byte top over sixteen folders of 250 bytes: the deepest joined
	// path is 4017 bytes, and the top's own name is what moves it.
	const seg = 250
	top, e := h.c.CreateFolder(id, rootID, "a")
	if e != nil {
		t.Fatal(e)
	}
	parent := top
	for i := 0; i < 16; i++ {
		name := strings.Repeat(string(rune('a'+i)), seg)
		nid, e := h.c.CreateFolder(id, parent, name)
		if e != nil {
			t.Fatalf("chain %d: %v", i, e)
		}
		parent = nid
	}
	h.save(t, id)
	if p := h.page(t, id, parent); len(p.Crumbs) != 18 {
		t.Fatalf("the chain is not 17 deep: %d", len(p.Crumbs))
	}
	// Renaming the top lengthens every path beneath it, and the record the
	// caller named looks fine either way: 4096 is accepted, 4097 is not.
	if e := h.c.RenameRecord(id, top, strings.Repeat("z", 81)); !isCode(e, CodeTreeBounds) {
		t.Fatalf("a joined path of 4097: %v", e)
	}
	if e := h.c.RenameRecord(id, top, strings.Repeat("z", 80)); e != nil {
		t.Fatalf("a joined path of 4096: %v", e)
	}
	if e := h.c.Discard(id); e != nil {
		t.Fatal(e)
	}
	// Moving the chain under a folder re-lengthens all of it the same way.
	long, e := h.c.CreateFolder(id, rootID, strings.Repeat("q", 79))
	if e != nil {
		t.Fatal(e)
	}
	short, e := h.c.CreateFolder(id, rootID, strings.Repeat("w", 78))
	if e != nil {
		t.Fatal(e)
	}
	fits, e := h.c.CreateFolder(id, rootID, "fits")
	if e != nil {
		t.Fatal(e)
	}
	h.save(t, id)
	if e := h.c.MoveRecords(id, []string{top}, long); !isCode(e, CodeTreeBounds) {
		t.Fatalf("a move that would exceed the path bound: %v", e)
	}
	// The batch is refused whole and in place: a record that would fit does
	// not move because another in the same batch would not, so the user
	// retries with a name rather than finding half a selection moved. This
	// is the app's own pre-flight and not the archive's per-call refusal,
	// which would already have staged the first record.
	if e := h.c.MoveRecords(id, []string{fits, top}, long); !isCode(e, CodeTreeBounds) {
		t.Fatalf("a batch with one record over the bound: %v", e)
	}
	if st, _ := h.c.Stat(id); st.Dirty != 0 {
		t.Fatalf("half a selection moved: %+v", st)
	}
	if r := h.row(t, id, rootID, "fits"); r.ID != fits {
		t.Fatalf("the record that fits was moved: %+v", r)
	}
	if e := h.c.MoveRecords(id, []string{top}, short); e != nil {
		t.Fatalf("a move that fits: %v", e)
	}
	h.save(t, id)
	if r := h.row(t, id, short, "a"); r.ID != top {
		t.Fatalf("the chain did not move: %+v", r)
	}
}

// Extract plans a set ordered parents-first, creates a folder because its
// record is live — an empty folder extracts as an empty folder — sets the
// times of the folders it made, and uses a folder that was already there as
// it stands.
func TestExtractAllWithFoldersAndTimes(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Out")

	h.src(t, "shots/x.txt", "x")
	h.src(t, "shots/inner/y.txt", "y")
	shots := filepath.Join(h.dir, "src", "shots")
	inner := filepath.Join(shots, "inner")
	when := time.Date(2019, 7, 8, 9, 10, 11, 0, time.UTC)
	innerWhen := time.Date(2018, 2, 3, 4, 5, 6, 0, time.UTC)
	if err := os.Chtimes(inner, innerWhen, innerWhen); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(shots, when, when); err != nil {
		t.Fatal(err)
	}
	h.addFolder(t, id, rootID, shots, PolicySkip)
	if _, e := h.c.CreateFolder(id, rootID, "empty"); e != nil {
		t.Fatal(e)
	}
	h.add(t, id, rootID, PolicySkip, h.src(t, "g.txt", "g"))
	h.save(t, id)
	emptyAt := h.row(t, id, rootID, "empty").ModifiedAt

	out := outDir(t)
	opID, e := h.c.Extract(id, []string{rootID}, out, ExtractSkip)
	if e != nil {
		t.Fatalf("extract: %v", e)
	}
	o := h.rec.waitOp(t, opID)
	if o.Error != "" || len(o.Results) != 6 {
		t.Fatalf("extract op: %+v", o)
	}
	// Parents before anything under them, and the progress total counts
	// file plaintext only.
	at := map[string]int{}
	for i, r := range o.Results {
		at[r.Name] = i
		if j := strings.LastIndexByte(r.Name, '/'); j >= 0 {
			p, ok := at[r.Name[:j]]
			if !ok || p >= i {
				t.Fatalf("a record before its parent: %+v", o.Results)
			}
		}
	}
	if o.Total != 3 {
		t.Fatalf("folders added bytes to the total: %d", o.Total)
	}
	for _, r := range o.Results {
		if r.IsDir && r.Outcome != "created" {
			t.Fatalf("a folder was not created: %+v", r)
		}
	}
	st, err := os.Stat(filepath.Join(out, "empty"))
	if err != nil || !st.IsDir() {
		t.Fatalf("the empty folder did not extract: %v", err)
	}
	if st.ModTime().Unix() != emptyAt {
		t.Fatalf("the empty folder's time: %d, want %d", st.ModTime().Unix(), emptyAt)
	}
	st, err = os.Stat(filepath.Join(out, "shots"))
	if err != nil {
		t.Fatal(err)
	}
	if st.ModTime().Unix() != when.Unix() {
		t.Fatalf("the folder's time was set before its contents: %v", st.ModTime())
	}
	// A folder the extraction created inside another gets its own time, and
	// its parent's is not the time creating it left behind: a folder's time
	// is set only once everything beneath it has landed.
	st, err = os.Stat(filepath.Join(out, "shots", "inner"))
	if err != nil {
		t.Fatal(err)
	}
	if st.ModTime().Unix() != innerWhen.Unix() {
		t.Fatalf("the nested folder's time: %v", st.ModTime())
	}
	if b, _ := os.ReadFile(filepath.Join(out, "shots", "x.txt")); string(b) != "x" {
		t.Fatalf("shots/x.txt: %q", b)
	}

	// A second extraction into the same place: the folders are used as they
	// stand — never renamed, never emptied — and the files are skipped.
	marker := filepath.Join(out, "shots", "mine.txt")
	if err := os.WriteFile(marker, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	opID, _ = h.c.Extract(id, []string{rootID}, out, ExtractSkip)
	o = h.rec.waitOp(t, opID)
	for _, r := range o.Results {
		if r.Outcome != "skipped" {
			t.Fatalf("a second extraction: %+v", r)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "shots (2)")); !os.IsNotExist(err) {
		t.Fatal("the existing folder was forked")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the existing folder was emptied: %v", err)
	}
}

// Every target is resolved before the first byte and asserted to lie under
// dir. Every record satisfying R20 and R39 does, so a failure means the
// index is not the one the reader validated: the whole operation fails with
// file.name and nothing is written.
func TestExtractContainmentMissFailsWhole(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Miss")
	h.add(t, id, rootID, PolicySkip, h.src(t, "ok.txt", "ok"))
	h.save(t, id)

	// The snapshot is bent the way only a broken index could bend it.
	aid, _ := parseID(id)
	h.c.mu.Lock()
	oa := h.c.archives[aid]
	for i := range oa.snap {
		if oa.snap[i].Name == "ok.txt" {
			oa.snap[i].Name = ".."
		}
	}
	h.c.mu.Unlock()

	out := filepath.Join(outDir(t), "never")
	opID, e := h.c.Extract(id, []string{rootID}, out, ExtractSkip)
	if e != nil {
		t.Fatalf("extract: %v", e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != CodeFileName || len(o.Results) != 0 {
		t.Fatalf("a containment miss: %+v", o)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("the destination was made before the plan was proved")
	}
}

// A committed record moved into a staged folder goes back where the move
// found it — and to the root when that folder is not there any more, its row
// still saying moved, since no record may be left naming a parent that is
// not there (APP.md §3). A way back whose name is taken refuses the whole
// call before anything is un-staged.
func TestUnstagingWhenTheParentTheMoveFoundItUnderWent(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Orphan")

	a, _ := h.c.CreateFolder(id, rootID, "a")
	h.add(t, id, a, PolicySkip, h.src(t, "x.txt", "x"))
	h.save(t, id)
	x := h.row(t, id, a, "x.txt").ID

	f, e := h.c.CreateFolder(id, rootID, "f")
	if e != nil {
		t.Fatal(e)
	}
	if e := h.c.MoveRecords(id, []string{x}, f); e != nil {
		t.Fatalf("move out: %v", e)
	}
	if e := h.c.DeleteRecords(id, []string{a}); e != nil {
		t.Fatalf("delete the folder the move found it under: %v", e)
	}
	if e := h.c.DeleteRecords(id, []string{f}); e != nil {
		t.Fatalf("un-stage: %v", e)
	}
	names := rowsByName(h.page(t, id, rootID))
	back, ok := names["x.txt"]
	if !ok || back.ID != x || back.ParentID != rootID {
		t.Fatalf("the record did not come back to the root: %+v", names)
	}
	if back.Pending != "moved" {
		t.Fatalf("the row must say where the record actually is: %+v", back)
	}
	if _, still := names["f"]; still {
		t.Fatalf("the staged folder stayed: %+v", names)
	}
	h.save(t, id)
	if r := h.row(t, id, rootID, "x.txt"); r.ID != x || r.ParentID != rootID {
		t.Fatalf("the save did not publish it at the root: %+v", r)
	}

	// The same sequence with the way back's name already taken: file.exists,
	// and the un-stage is refused whole rather than dropping the record.
	id2 := h.openArchive(t, "Taken")
	a2, _ := h.c.CreateFolder(id2, rootID, "a")
	h.add(t, id2, a2, PolicySkip, h.src(t, "same/y.txt", "y"))
	h.add(t, id2, rootID, PolicySkip, h.src(t, "y.txt", "top"))
	h.save(t, id2)
	y := h.row(t, id2, a2, "y.txt").ID

	f2, _ := h.c.CreateFolder(id2, rootID, "f")
	if e := h.c.MoveRecords(id2, []string{y}, f2); e != nil {
		t.Fatalf("move out: %v", e)
	}
	if e := h.c.DeleteRecords(id2, []string{a2}); e != nil {
		t.Fatalf("delete a: %v", e)
	}
	st, _ := h.c.Stat(id2)
	if e := h.c.DeleteRecords(id2, []string{f2}); !isCode(e, CodeFileExists) {
		t.Fatalf("an un-stage with nowhere to put the record back: %v", e)
	}
	if now, _ := h.c.Stat(id2); now.Dirty != st.Dirty {
		t.Fatalf("a refused un-stage staged something: %+v", now)
	}
	if r := h.row(t, id2, f2, "y.txt"); r.ID != y {
		t.Fatalf("the record left the staged folder: %+v", r)
	}
}

// The depth and joined-path bounds hold for live records only: a tombstone
// keeps its name and is held to neither (FORMAT.md R32), so the app's
// pre-flight walks the live subtree alone and answers what the encoder
// answers — the save proves it.
func TestSubtreeBoundsIgnoreARowStagedForDeletion(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Tombstoned")

	const seg = 250
	top, e := h.c.CreateFolder(id, rootID, "a")
	if e != nil {
		t.Fatal(e)
	}
	parent, deepest := top, ""
	for i := 0; i < 16; i++ {
		nid, e := h.c.CreateFolder(id, parent, strings.Repeat(string(rune('a'+i)), seg))
		if e != nil {
			t.Fatalf("chain %d: %v", i, e)
		}
		parent, deepest = nid, nid
	}
	h.save(t, id)

	// With the whole chain live the rename is over the path bound by one.
	if e := h.c.RenameRecord(id, top, strings.Repeat("z", 81)); !isCode(e, CodeTreeBounds) {
		t.Fatalf("a joined path of 4097: %v", e)
	}
	if e := h.c.DeleteRecords(id, []string{deepest}); e != nil {
		t.Fatalf("delete the deepest folder: %v", e)
	}
	// The tombstone is not measured, so the live subtree is what decides.
	if e := h.c.RenameRecord(id, top, strings.Repeat("z", 81)); e != nil {
		t.Fatalf("a staged-deleted row held to the bounds: %v", e)
	}
	h.save(t, id)
	if r := h.row(t, id, rootID, strings.Repeat("z", 81)); r.ID != top {
		t.Fatalf("the rename did not survive the seal: %+v", r)
	}
}

// A move batch is refused whole and in place even when the refusal comes
// from the archive rather than the pre-flight: nothing is left half moved
// (APP.md §3).
func TestAMoveBatchTheArchiveRefusesIsPutBack(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Whole")

	h.add(t, id, rootID, PolicySkip, h.src(t, "one.txt", "1"), h.src(t, "two.txt", "2"))
	d, _ := h.c.CreateFolder(id, rootID, "d")
	h.save(t, id)
	one := h.row(t, id, rootID, "one.txt").ID
	two := h.row(t, id, rootID, "two.txt").ID

	// A staged change opens the transaction; then the second record of the
	// batch is tombstoned in the archive's own index behind the overlay's
	// back, so the merged view still shows it live and the pre-flight
	// passes. Only a bug of ours puts the two out of step — which is the
	// case the promise is about.
	if _, e := h.c.CreateFolder(id, rootID, "keep"); e != nil {
		t.Fatal(e)
	}
	aid, _ := parseID(id)
	tid, _ := parseID(two)
	h.c.mu.Lock()
	err := h.c.archives[aid].tx.Delete(tid)
	h.c.mu.Unlock()
	if err != nil {
		t.Fatalf("bend the working index: %v", err)
	}

	if e := h.c.MoveRecords(id, []string{one, two}, d); !isCode(e, CodeFileNotFound) {
		t.Fatalf("the refusal: %v", e)
	}
	if st, _ := h.c.Stat(id); st.Dirty != 1 {
		t.Fatalf("half a selection moved: %+v", st)
	}
	if r := h.row(t, id, rootID, "one.txt"); r.ID != one || r.ParentID != rootID || r.Pending != "" {
		t.Fatalf("the first record of the batch stayed moved: %+v", r)
	}
	if p := h.page(t, id, d); p.Total != 0 {
		t.Fatalf("the destination took a record: %+v", p.Rows)
	}
}

// A folder row's Size is the sum of what a save would keep: a row staged for
// deletion is not in it, which is the reading a deleted folder's own row
// already takes — nothing beneath it is listed, so it shows zero.
func TestAFolderSizeDropsWhatIsStagedForDeletion(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Sizes")

	d, _ := h.c.CreateFolder(id, rootID, "d")
	inner, _ := h.c.CreateFolder(id, d, "inner")
	h.add(t, id, d, PolicySkip, h.src(t, "a.txt", "0123456789"))
	h.add(t, id, inner, PolicySkip, h.src(t, "b.txt", "01234"))
	h.save(t, id)
	if r := h.row(t, id, rootID, "d"); r.Size != 15 {
		t.Fatalf("the sum beneath the folder: %+v", r)
	}
	b := h.row(t, id, inner, "b.txt").ID
	if e := h.c.DeleteRecords(id, []string{b}); e != nil {
		t.Fatal(e)
	}
	if r := h.row(t, id, rootID, "d"); r.Size != 10 {
		t.Fatalf("a deleted file is still in the sum: %+v", r)
	}
	if e := h.c.DeleteRecords(id, []string{d}); e != nil {
		t.Fatal(e)
	}
	if r := h.row(t, id, rootID, "d"); r.Pending != "deleted" || r.Size != 0 {
		t.Fatalf("a folder staged for deletion: %+v", r)
	}
	h.save(t, id)
	if p := h.page(t, id, rootID); p.Total != 0 {
		t.Fatalf("what the save published: %+v", p.Rows)
	}
}

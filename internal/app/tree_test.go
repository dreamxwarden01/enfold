package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dreamxwarden01/enfold/internal/archive"
)

// The archive page over a tree (APP.md §2.3, §3; FORMAT.md R39): ids at the
// boundary, the committed snapshot re-taken after every commit, and the
// operations that read and write it. Each operation is its own transaction,
// so what a call returns is what the file holds. Nothing here speaks a path
// except as something the tree derives.

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

// stat is the status strip, failing the test if the archive is not open.
func (h *harness) stat(t *testing.T, id string) ArchiveStat {
	t.Helper()
	st, e := h.c.Stat(id)
	if e != nil {
		t.Fatalf("stat: %v", e)
	}
	return st
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

// add adds files under parentID and waits for the operation.
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

// A dirID the page still holds can be gone — a Delete takes a subtree the
// page may be standing in — and is told so, never given an empty listing
// under a breadcrumb that still names the place.
func TestAPageWhoseFolderWentIsToldSo(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Gone")

	kept, e := h.c.CreateFolder(id, rootID, "kept")
	if e != nil {
		t.Fatal(e)
	}
	h.page(t, id, kept) // enterable while it is there
	if e := h.c.DeleteRecords(id, []string{kept}); e != nil {
		t.Fatal(e)
	}
	if _, e := h.c.Page(id, kept, "name", 0, 10); !isCode(e, CodeFileNotFound) {
		t.Fatalf("a folder the delete took: %v", e)
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
	f := h.row(t, id, rootID, "f.txt").ID
	before := h.stat(t, id).Seq

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
	if _, e := h.c.Extract(id, nil, outDir(t), ExtractSkip, nil); !isCode(e, CodeParams) {
		t.Fatalf("extract nothing: %v", e)
	}
	// A file's id where a directory is wanted is file.not_found.
	if _, e := h.c.Page(id, f, "name", 0, 10); !isCode(e, CodeFileNotFound) {
		t.Fatalf("page of a file: %v", e)
	}
	if _, e := h.c.CreateFolder(id, f, "x"); !isCode(e, CodeFileNotFound) {
		t.Fatalf("create under a file: %v", e)
	}
	if st := h.stat(t, id); st.Seq != before {
		t.Fatalf("a refusal committed something: %+v", st)
	}
}

// A folder made in the app is a record, committed at once: listed,
// enterable, and it stands with nothing in it (FORMAT.md R39).
func TestCreateFolderIsARecord(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Made")

	nid, e := h.c.CreateFolder(id, rootID, "Photos")
	if e != nil {
		t.Fatalf("create folder: %v", e)
	}
	r := h.row(t, id, rootID, "Photos")
	if !r.IsDir || r.ID != nid {
		t.Fatalf("the folder's row: %+v", r)
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
	// It is a real parent at once.
	inner, e := h.c.CreateFolder(id, nid, "2024")
	if e != nil {
		t.Fatalf("create under a fresh folder: %v", e)
	}
	h.add(t, id, inner, PolicySkip, h.src(t, "p.txt", "pic"))
	if p := h.page(t, id, nid); p.Total != 1 || p.Rows[0].Name != "2024" {
		t.Fatalf("the folder's children: %+v", p.Rows)
	}
	// An empty folder survives its own commit: a reopen of the file still
	// has it.
	empty, _ := h.c.CreateFolder(id, rootID, "Empty")
	if e := h.c.CloseArchive(id); e != nil {
		t.Fatal(e)
	}
	if _, e := h.c.OpenArchive(id); e != nil {
		t.Fatal(e)
	}
	if p := h.page(t, id, empty); p.Total != 0 {
		t.Fatalf("the empty folder took something with it: %+v", p)
	}
	if h.row(t, id, rootID, "Empty").ID != empty {
		t.Fatal("the empty folder did not survive the commit")
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
	before := h.stat(t, id).Seq
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
	// One commit for the whole walk, whatever it entered.
	if st := h.stat(t, id); st.Seq != before+1 {
		t.Fatalf("the walk's commits: %d, from %d", st.Seq, before)
	}
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
	h.src(t, "tree/pics/one.txt", "one")
	pics := filepath.Join(h.dir, "src", "tree", "pics")

	o := h.addFolder(t, id, rootID, pics, PolicySkip)
	if len(o.Results) != 1 || o.Results[0].Outcome != "skipped" || !o.Results[0].IsDir {
		t.Fatalf("skip left the subtree in: %+v", o.Results)
	}
	before := h.stat(t, id).Seq
	o = h.addFolder(t, id, rootID, pics, PolicyReplace)
	if len(o.Results) != 1 || o.Results[0].Outcome != "failed" || o.Results[0].Code != CodeKindMismatch {
		t.Fatalf("replace across kinds: %+v", o.Results)
	}
	// A walk that wrote nothing publishes nothing.
	if st := h.stat(t, id); st.Seq != before {
		t.Fatalf("a refused walk committed something: %+v", st)
	}
	// The other direction: a file offered where a folder stands.
	if _, e := h.c.CreateFolder(id, rootID, "docs"); e != nil {
		t.Fatal(e)
	}
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
	// And a file with an extension keeps it.
	h.add(t, id, rootID, PolicySkip, h.src(t, "note.txt", "one"))
	o = h.add(t, id, rootID, PolicyKeepBoth, h.src(t, "note.txt", "one"))
	if o.Results[0].Name != "note (2).txt" {
		t.Fatalf("keep-both before the extension: %+v", o.Results)
	}
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

// Deleting a directory takes its whole subtree in one commit (FORMAT.md R39,
// R32): the folder and everything beneath it go at once, nothing beneath it
// is previewed or extracted afterwards, and the name is free again. There is
// no undo — the page asked before this ran.
func TestDeleteOfADirectoryCommitsTheSubtree(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Del")

	d, _ := h.c.CreateFolder(id, rootID, "d")
	inner, _ := h.c.CreateFolder(id, d, "inner")
	h.add(t, id, d, PolicySkip, h.src(t, "f.txt", "f"))
	h.add(t, id, inner, PolicySkip, h.src(t, "g.txt", "g"))
	gid := h.row(t, id, inner, "g.txt").ID
	other, _ := h.c.CreateFolder(id, rootID, "other")
	before := h.stat(t, id)
	if before.Records != 5 {
		t.Fatalf("records before the delete: %+v", before)
	}

	if e := h.c.DeleteRecords(id, []string{d}); e != nil {
		t.Fatalf("delete: %v", e)
	}
	// One commit however large the subtree, and the whole subtree is gone.
	st := h.stat(t, id)
	if st.Seq != before.Seq+1 {
		t.Fatalf("a deleted folder of four records is one commit: %d, from %d", st.Seq, before.Seq)
	}
	if st.Records != 1 || st.Files != 0 {
		t.Fatalf("the subtree survived: %+v", st)
	}
	if _, still := rowsByName(h.page(t, id, rootID))["d"]; still {
		t.Fatal("the deleted folder is still listed")
	}
	if _, e := h.c.PreviewURL(id, gid); !isCode(e, CodeFileNotFound) {
		t.Fatalf("a record beneath a deleted folder is previewable: %v", e)
	}
	opID, e := h.c.Extract(id, []string{gid}, outDir(t), ExtractSkip, nil)
	if e != nil {
		t.Fatal(e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != CodeFileNotFound {
		t.Fatalf("a record beneath a deleted folder is extractable: %+v", o)
	}
	if _, e := h.c.Page(id, d, "name", 0, 10); !isCode(e, CodeFileNotFound) {
		t.Fatalf("a deleted folder is enterable: %v", e)
	}
	if _, e := h.c.Page(id, inner, "name", 0, 10); !isCode(e, CodeFileNotFound) {
		t.Fatalf("a folder beneath a deleted one: %v", e)
	}
	// A tombstone is not a live sibling, so the name is free again.
	again, e := h.c.CreateFolder(id, rootID, "d")
	if e != nil {
		t.Fatalf("a new folder beside the tombstone: %v", e)
	}
	if col, _ := h.c.CheckNames(id, rootID, []string{"d"}); len(col) != 1 || col[0].Existing != again {
		t.Fatalf("CheckNames over the new folder: %+v", col)
	}
	// It is the file that holds all this, not the handle: a reopen agrees.
	if e := h.c.CloseArchive(id); e != nil {
		t.Fatal(e)
	}
	if _, e := h.c.OpenArchive(id); e != nil {
		t.Fatal(e)
	}
	names := rowsByName(h.page(t, id, rootID))
	if len(names) != 2 || names["d"].ID != again || names["other"].ID != other {
		t.Fatalf("after the reopen: %+v", names)
	}
}

// Rename and Move are pre-flighted against R39 before anything is written,
// with the four codes of APP.md §3.
func TestRenameAndMoveRefusals(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Refuse")

	a, _ := h.c.CreateFolder(id, rootID, "a")
	b, _ := h.c.CreateFolder(id, a, "b")
	h.add(t, id, rootID, PolicySkip, h.src(t, "one.txt", "1"), h.src(t, "two.txt", "2"))
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
	h.add(t, id, rootID, PolicySkip, h.src(t, "two.txt", "2"))
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
	before := h.stat(t, id).Seq
	// A record already under the destination is a no-op, not an error.
	if e := h.c.MoveRecords(id, []string{two2}, rootID); e != nil {
		t.Fatalf("a no-op move: %v", e)
	}
	if st := h.stat(t, id); st.Seq != before {
		t.Fatalf("a refused or no-op batch committed something: %+v", st)
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
	before := h.stat(t, id).Seq

	if e := h.c.MoveRecords(id, []string{a, b}, c); e != nil {
		t.Fatalf("move: %v", e)
	}
	// One commit, and b is still under a rather than beside it.
	if st := h.stat(t, id); st.Seq != before+1 {
		t.Fatalf("the move's commits: %d, from %d", st.Seq, before)
	}
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
	if e := h.c.RenameRecord(id, top, "a"); e != nil {
		t.Fatalf("rename back: %v", e)
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
	if e := h.c.MoveRecords(id, []string{top}, long); !isCode(e, CodeTreeBounds) {
		t.Fatalf("a move that would exceed the path bound: %v", e)
	}
	// The batch is refused whole and in place: a record that would fit does
	// not move because another in the same batch would not, so the user
	// retries with a name rather than finding half a selection moved.
	before := h.stat(t, id).Seq
	if e := h.c.MoveRecords(id, []string{fits, top}, long); !isCode(e, CodeTreeBounds) {
		t.Fatalf("a batch with one record over the bound: %v", e)
	}
	if st := h.stat(t, id); st.Seq != before {
		t.Fatalf("half a selection moved: %+v", st)
	}
	if r := h.row(t, id, rootID, "fits"); r.ID != fits {
		t.Fatalf("the record that fits was moved: %+v", r)
	}
	if e := h.c.MoveRecords(id, []string{top}, short); e != nil {
		t.Fatalf("a move that fits: %v", e)
	}
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
	emptyAt := h.row(t, id, rootID, "empty").ModifiedAt

	out := outDir(t)
	opID, e := h.c.Extract(id, []string{rootID}, out, ExtractSkip, nil)
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
	opID, _ = h.c.Extract(id, []string{rootID}, out, ExtractSkip, nil)
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
	// A file already there is not overwritten, and rename takes the next
	// free name beside it rather than replacing anything.
	opID, _ = h.c.Extract(id, []string{rootID}, out, ExtractRename, nil)
	o = h.rec.waitOp(t, opID)
	if b, _ := os.ReadFile(filepath.Join(out, "g (2).txt")); string(b) != "g" {
		t.Fatalf("rename did not take the next name: %q (%+v)", b, o.Results)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "g.txt")); string(b) != "g" {
		t.Fatalf("the file already there was touched: %q", b)
	}
}

// replace is the default policy (APP.md §3, ruled 2026-09-10): the content
// is written through the temporary and placed over the file already there in
// one move — never by unlinking it first — so what was there is gone only
// once the whole new file has landed, and a failure before the move leaves
// it exactly as it was. An empty policy is replace; a word that is neither
// of the four is params.
func TestExtractReplacePlacesOverTheOldFile(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Over")
	h.add(t, id, rootID, PolicySkip,
		h.src(t, "a.txt", "the new content"),
		h.src(t, "big.bin", strings.Repeat("payload ", 1<<17))) // 1 MiB: several chunks

	if _, e := h.c.Extract(id, []string{rootID}, outDir(t), "clobber", nil); !isCode(e, CodeParams) {
		t.Fatalf("an unknown policy: %v", e)
	}

	out := outDir(t)
	if err := os.WriteFile(filepath.Join(out, "a.txt"), []byte("the old content"), 0o600); err != nil {
		t.Fatal(err)
	}
	aid := h.row(t, id, rootID, "a.txt").ID
	opID, e := h.c.Extract(id, []string{aid}, out, "", nil) // empty: replace
	if e != nil {
		t.Fatal(e)
	}
	o := h.rec.waitOp(t, opID)
	if o.Error != "" || len(o.Results) != 1 || o.Results[0].Outcome != "extracted" {
		t.Fatalf("replace over an existing file: %+v", o)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "a.txt")); string(b) != "the new content" {
		t.Fatalf("what stands there now: %q", b)
	}
	if left := temporaries(t, out); len(left) != 0 {
		t.Fatalf("the temporary was left behind: %v", left)
	}

	// A failure mid-write: the extraction is cancelled after its first chunk
	// has landed in the temporary. The old file is untouched and no
	// temporary is left.
	archiveID, _ := parseID(id)
	h.c.mu.Lock()
	oa := h.c.archives[archiveID]
	h.c.mu.Unlock()
	fid, _ := parseID(h.row(t, id, rootID, "big.bin").ID)
	dst := filepath.Join(out, "big.bin")
	if err := os.WriteFile(dst, []byte("the old big file"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	chunks := 0
	err := extractFile(ctx, extractFS{}, oa.a, fid, dst, true, func(uint64) { chunks++; cancel() })
	if err == nil {
		t.Fatal("the cancelled extraction reported success")
	}
	if chunks == 0 {
		t.Fatal("the extraction failed before it had written anything: not a failure mid-write")
	}
	if b, _ := os.ReadFile(dst); string(b) != "the old big file" {
		t.Fatalf("a failure mid-write touched the old file: %q", b)
	}
	if left := temporaries(t, out); len(left) != 0 {
		t.Fatalf("the failed extraction left a temporary: %v", left)
	}
}

// temporaries lists the extraction temporaries lying in a folder.
func temporaries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".enfold-") {
			out = append(out, e.Name())
		}
	}
	return out
}

// ask is the drag-and-drop shape (APP.md §3, ruled 2026-09-10): everything
// that collides with nothing is extracted, and each collision comes back as
// a conflict outcome carrying the existing file's size and date — a stat
// taken after the placement refused — for the page to ask about and re-issue
// with replace or rename. skip and rename are unchanged beside it.
func TestExtractAskReportsTheConflicts(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Asking")
	h.add(t, id, rootID, PolicySkip, h.src(t, "a.txt", "archived a"), h.src(t, "b.txt", "archived b"))

	out := outDir(t)
	there := filepath.Join(out, "a.txt")
	if err := os.WriteFile(there, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	when := time.Date(2021, 3, 4, 5, 6, 7, 0, time.UTC)
	if err := os.Chtimes(there, when, when); err != nil {
		t.Fatal(err)
	}

	opID, e := h.c.Extract(id, []string{rootID}, out, ExtractAsk, nil)
	if e != nil {
		t.Fatal(e)
	}
	o := h.rec.waitOp(t, opID)
	if o.Error != "" || len(o.Results) != 2 {
		t.Fatalf("ask: %+v", o)
	}
	byName := map[string]FileOutcome{}
	for _, r := range o.Results {
		byName[r.Name] = r
	}
	conflict, extracted := byName["a.txt"], byName["b.txt"]
	if conflict.Outcome != "conflict" {
		t.Fatalf("the collision: %+v", conflict)
	}
	if conflict.Existing == nil || conflict.Existing.Size != 4 || conflict.Existing.ModifiedAt != when.Unix() {
		t.Fatalf("what the conflict says of the file in the way: %+v", conflict.Existing)
	}
	if extracted.Outcome != "extracted" || extracted.Existing != nil {
		t.Fatalf("what collided with nothing: %+v", extracted)
	}
	if b, _ := os.ReadFile(there); string(b) != "mine" {
		t.Fatalf("ask wrote over the file in the way: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "b.txt")); string(b) != "archived b" {
		t.Fatalf("the rest was not extracted: %q", b)
	}

	// The page asks, and re-issues for the chosen id. Skip leaves it, rename
	// takes the next free name beside it, replace takes its place.
	only := []string{h.row(t, id, rootID, "a.txt").ID}
	opID, _ = h.c.Extract(id, only, out, ExtractSkip, nil)
	if o := h.rec.waitOp(t, opID); o.Results[0].Outcome != "skipped" {
		t.Fatalf("skip: %+v", o.Results)
	}
	opID, _ = h.c.Extract(id, only, out, ExtractRename, nil)
	if o := h.rec.waitOp(t, opID); o.Results[0].Outcome != "extracted" {
		t.Fatalf("rename: %+v", o.Results)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "a (2).txt")); string(b) != "archived a" {
		t.Fatalf("rename did not take the next name: %q", b)
	}
	opID, _ = h.c.Extract(id, only, out, ExtractReplace, nil)
	if o := h.rec.waitOp(t, opID); o.Results[0].Outcome != "extracted" {
		t.Fatalf("replace: %+v", o.Results)
	}
	if b, _ := os.ReadFile(there); string(b) != "archived a" {
		t.Fatalf("replace did not place the new file: %q", b)
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
	opID, e := h.c.Extract(id, []string{rootID}, out, ExtractSkip, nil)
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

// The depth and joined-path bounds hold for live records only: a deleted
// subtree is gone from the index, so the app's pre-flight measures the live
// tree and answers what the encoder answers — the commit proves it.
func TestSubtreeBoundsMeasureTheLiveTree(t *testing.T) {
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

	// With the whole chain live the rename is over the path bound by one.
	if e := h.c.RenameRecord(id, top, strings.Repeat("z", 81)); !isCode(e, CodeTreeBounds) {
		t.Fatalf("a joined path of 4097: %v", e)
	}
	if e := h.c.DeleteRecords(id, []string{deepest}); e != nil {
		t.Fatalf("delete the deepest folder: %v", e)
	}
	// The tombstone is not measured, so the live subtree is what decides,
	// and the seal agrees — the rename commits.
	if e := h.c.RenameRecord(id, top, strings.Repeat("z", 81)); e != nil {
		t.Fatalf("a deleted row held to the bounds: %v", e)
	}
	if r := h.row(t, id, rootID, strings.Repeat("z", 81)); r.ID != top {
		t.Fatalf("the rename did not survive the seal: %+v", r)
	}
}

// A move batch the archive refuses is refused whole: the transaction is
// dropped, so nothing is published and nothing is left half moved (APP.md
// §3).
func TestAMoveBatchTheArchiveRefusesIsDroppedWhole(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Whole")

	h.add(t, id, rootID, PolicySkip, h.src(t, "one.txt", "1"))
	d, _ := h.c.CreateFolder(id, rootID, "d")
	one := h.row(t, id, rootID, "one.txt").ID

	// A record the snapshot has and the index does not: the pre-flight sees
	// it live and the archive answers not-found. Only a bug of ours puts the
	// two out of step — which is the case the promise is about.
	aid, _ := parseID(id)
	var ghost [16]byte
	ghost[0] = 0xAA
	h.c.mu.Lock()
	oa := h.c.archives[aid]
	oa.snap = append(oa.snap, archive.FileInfo{ID: ghost, Name: "ghost.txt"})
	oa.byID[ghost] = len(oa.snap) - 1
	before := oa.seq
	h.c.mu.Unlock()

	if e := h.c.MoveRecords(id, []string{one, hexID(ghost)}, d); !isCode(e, CodeFileNotFound) {
		t.Fatalf("the refusal: %v", e)
	}
	if st := h.stat(t, id); st.Seq != before {
		t.Fatalf("a refused batch committed something: %+v", st)
	}
	if r := h.row(t, id, rootID, "one.txt"); r.ID != one || r.ParentID != rootID {
		t.Fatalf("the first record of the batch stayed moved: %+v", r)
	}
	if p := h.page(t, id, d); p.Total != 0 {
		t.Fatalf("the destination took a record: %+v", p.Rows)
	}
}

// A folder row's Size is the sum of the plaintext beneath it, and a delete
// takes what it removes out of that sum at once.
func TestAFolderSizeIsTheSumBeneathIt(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Sizes")

	d, _ := h.c.CreateFolder(id, rootID, "d")
	inner, _ := h.c.CreateFolder(id, d, "inner")
	h.add(t, id, d, PolicySkip, h.src(t, "a.txt", "0123456789"))
	h.add(t, id, inner, PolicySkip, h.src(t, "b.txt", "01234"))
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
	if p := h.page(t, id, rootID); p.Total != 0 {
		t.Fatalf("what the delete left: %+v", p.Rows)
	}
}

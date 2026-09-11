package app

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// The order of a listing (APP.md §3 Page, ruled 2026-09-10): the core's, so
// that every page of a long directory comes in one order and Children
// answers the folder's ids in it.

// The grammar: one or two signed keys, the second one the name tie-break's
// direction. What it does not read is params, never a silent default.
func TestSortGrammar(t *testing.T) {
	good := map[string]sortOrder{
		"":            {key: "name"},
		"name":        {key: "name"},
		"-name":       {key: "name", desc: true, nameDesc: true},
		"name,name":   {key: "name"},
		"-name,-name": {key: "name", desc: true, nameDesc: true},
		// A redundant tie-break for a name primary is read, the primary's
		// direction winning (the outside review's finding 15).
		"name,-name":     {key: "name"},
		"-name,name":     {key: "name", desc: true, nameDesc: true},
		"size":           {key: "size"},
		"-size":          {key: "size", desc: true},
		"size,-name":     {key: "size", nameDesc: true},
		"-modified,name": {key: "modified", desc: true},
		"type,-name":     {key: "type", nameDesc: true},
	}
	for in, want := range good {
		got, ok := parseSortOrder(in)
		if !ok || got != want {
			t.Errorf("%q: got %+v, %v; want %+v", in, got, ok, want)
		}
	}
	bad := []string{"colour", "-", "size,", ",name", "size,size", "size,-modified", "size,name,name", " size", "SIZE"}
	for _, in := range bad {
		if o, ok := parseSortOrder(in); ok {
			t.Errorf("%q was read as %+v", in, o)
		}
	}
}

// names lists a fixture's names in the order given.
func names(recs []*mergedRec) []string {
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r.name
	}
	return out
}

func sortFixture() []*mergedRec {
	n := 0
	rec := func(name string, dir bool, size uint64, mod int64) *mergedRec {
		n++
		r := &mergedRec{isDir: dir, name: name, size: size, modifiedAt: mod}
		r.id[0] = byte(n)
		return r
	}
	return []*mergedRec{
		// Directories among the files, in no order of their own.
		rec("zeta", true, 0, 50),
		rec("file10.txt", false, 10, 10),
		rec("10", true, 0, 40),
		rec("résumé.txt", false, 3, 30),
		rec("9.txt", false, 9, 9),
		rec("Alpha", true, 0, 60),
		rec("10.txt", false, 10, 10),
		rec("file2.txt", false, 2, 2),
		rec("resume.txt", false, 3, 30),
		rec("b.txt", false, 5, 5),
		rec(".env", false, 1, 1),
		rec("a.tar.gz", false, 7, 7),
		rec("한글.txt", false, 4, 4),
		rec("汉字.txt", false, 4, 4),
		rec("README", false, 6, 6),
		rec("Photo.JPG", false, 8, 8),
		rec("notes.md", false, 5, 5),
	}
}

// name is the general collation: digits by value (2 before 10, file2 before
// file10), accents secondary (resume before résumé), case ignored (Photo
// among the p's, README among the r's), and the scripts in the Unicode
// collation's order — Latin, then Hangul, then Han. Directories first under
// both directions.
func TestSortByName(t *testing.T) {
	coll := newNameCollator()
	asc := []string{
		"10", "Alpha", "zeta",
		".env", "9.txt", "10.txt", "a.tar.gz", "b.txt", "file2.txt", "file10.txt", "notes.md",
		"Photo.JPG", "README", "resume.txt", "résumé.txt", "한글.txt", "汉字.txt",
	}
	got := names(coll.sortedKids(sortFixture(), sortOrder{key: "name"}))
	if strings.Join(got, " ") != strings.Join(asc, " ") {
		t.Fatalf("name ascending:\n got %q\nwant %q", got, asc)
	}
	// Descending reverses the whole of the order, directories still first.
	desc := []string{"zeta", "Alpha", "10"}
	for i := len(asc) - 1; i >= 3; i-- {
		desc = append(desc, asc[i])
	}
	got = names(coll.sortedKids(sortFixture(), sortOrder{key: "name", desc: true, nameDesc: true}))
	if strings.Join(got, " ") != strings.Join(desc, " ") {
		t.Fatalf("name descending:\n got %q\nwant %q", got, desc)
	}
	// Two names equal under the collation are ordered by code point, so the
	// order is total: B (U+0042) before b (U+0062). Siblings of one folder
	// never fold onto each other (R39), so the fixture is the sorter's own.
	same := []*mergedRec{{name: "b"}, {name: "B"}, {name: "ｂ"}}
	if got := names(coll.sortedKids(same, sortOrder{key: "name"})); strings.Join(got, "") != "Bbｂ" {
		t.Fatalf("the code-point tie-break: %q", got)
	}
	if got := names(coll.sortedKids(same, sortOrder{key: "name", desc: true, nameDesc: true})); strings.Join(got, "") != "ｂbB" {
		t.Fatalf("the code-point tie-break, descending: %q", got)
	}
}

// size orders files by their plaintext size; directories, which show none,
// stay first in both directions and are ordered by name among themselves;
// ties fall to the name in the name key's direction.
func TestSortBySize(t *testing.T) {
	coll := newNameCollator()
	got := names(coll.sortedKids(sortFixture(), sortOrder{key: "size"}))
	want := []string{
		"10", "Alpha", "zeta",
		".env", "file2.txt", "resume.txt", "résumé.txt", "한글.txt", "汉字.txt", "b.txt", "notes.md",
		"README", "a.tar.gz", "Photo.JPG", "9.txt", "10.txt", "file10.txt",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("size ascending:\n got %q\nwant %q", got, want)
	}
	got = names(coll.sortedKids(sortFixture(), sortOrder{key: "size", desc: true}))
	want = []string{
		"10", "Alpha", "zeta",
		"10.txt", "file10.txt", "9.txt", "Photo.JPG", "a.tar.gz", "README", "b.txt", "notes.md",
		"한글.txt", "汉字.txt", "resume.txt", "résumé.txt", "file2.txt", ".env",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("size descending:\n got %q\nwant %q", got, want)
	}
	// The tie-break's direction is the name key's own: -size with -name
	// reverses the ties and the directories, and nothing else.
	got = names(coll.sortedKids(sortFixture(), sortOrder{key: "size", desc: true, nameDesc: true}))
	want = []string{
		"zeta", "Alpha", "10",
		"file10.txt", "10.txt", "9.txt", "Photo.JPG", "a.tar.gz", "README", "notes.md", "b.txt",
		"汉字.txt", "한글.txt", "résumé.txt", "resume.txt", "file2.txt", ".env",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("size descending, name descending:\n got %q\nwant %q", got, want)
	}
}

// type is the extension case-folded — the part after the last dot, none
// when the dot is the first character or there is none, gz for a.tar.gz —
// files without one first in both directions, ties by name.
func TestSortByType(t *testing.T) {
	for in, want := range map[string]string{
		"a.tar.gz": "gz", "Photo.JPG": "jpg", ".env": "", "README": "", "notes.md": "md", "x.y.": "",
	} {
		if got := extensionOf(in); got != want {
			t.Errorf("extensionOf(%q) = %q, want %q", in, got, want)
		}
	}
	coll := newNameCollator()
	got := names(coll.sortedKids(sortFixture(), sortOrder{key: "type"}))
	want := []string{
		"10", "Alpha", "zeta",
		".env", "README", "a.tar.gz", "Photo.JPG", "notes.md",
		"9.txt", "10.txt", "b.txt", "file2.txt", "file10.txt", "resume.txt", "résumé.txt", "한글.txt", "汉字.txt",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("type ascending:\n got %q\nwant %q", got, want)
	}
	got = names(coll.sortedKids(sortFixture(), sortOrder{key: "type", desc: true}))
	want = []string{
		"10", "Alpha", "zeta",
		".env", "README",
		"9.txt", "10.txt", "b.txt", "file2.txt", "file10.txt", "resume.txt", "résumé.txt", "한글.txt", "汉字.txt",
		"notes.md", "Photo.JPG", "a.tar.gz",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("type descending:\n got %q\nwant %q", got, want)
	}
}

// modified is the record's modified_at, a directory's its own time.
func TestSortByModified(t *testing.T) {
	coll := newNameCollator()
	got := names(coll.sortedKids(sortFixture(), sortOrder{key: "modified", desc: true}))
	want := []string{
		"Alpha", "zeta", "10",
		"resume.txt", "résumé.txt", "10.txt", "file10.txt", "9.txt", "Photo.JPG", "a.tar.gz", "README",
		"b.txt", "notes.md", "한글.txt", "汉字.txt", "file2.txt", ".env",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("modified descending:\n got %q\nwant %q", got, want)
	}
}

// Through the boundary: Page sorts with the same order, so pages taken at
// successive offsets are one list; Children answers the folder's ids in
// that order and counts what Total counts; the sort string is checked.
func TestPageOrderIsOneListAcrossOffsets(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Sorted")
	var paths []string
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 1; i <= 12; i++ {
		// Sizes that collide in pairs, so the name tie-break is exercised
		// across a page boundary too.
		p := h.src(t, fmt.Sprintf("f%02d.txt", i), strings.Repeat("x", (i+1)/2))
		when := base.Add(time.Duration(13-i) * time.Hour)
		if err := os.Chtimes(p, when, when); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	h.add(t, id, rootID, PolicySkip, paths...)
	for _, d := range []string{"Beta", "alpha"} {
		if _, e := h.c.CreateFolder(id, rootID, d); e != nil {
			t.Fatal(e)
		}
	}

	for _, sortBy := range []string{"", "-name", "-size,name", "size,-name", "type", "-modified,-name"} {
		whole, e := h.c.Page(id, rootID, sortBy, 0, 100)
		if e != nil {
			t.Fatalf("%q: %v", sortBy, e)
		}
		if whole.Total != 14 || len(whole.Rows) != 14 {
			t.Fatalf("%q: %d rows of %d", sortBy, len(whole.Rows), whole.Total)
		}
		if !whole.Rows[0].IsDir || !whole.Rows[1].IsDir || whole.Rows[2].IsDir {
			t.Fatalf("%q: directories are not first: %+v", sortBy, whole.Rows)
		}
		var paged []string
		for off := 0; off < whole.Total; off += 5 {
			p, e := h.c.Page(id, rootID, sortBy, off, 5)
			if e != nil {
				t.Fatal(e)
			}
			for _, r := range p.Rows {
				paged = append(paged, r.ID)
			}
		}
		kids, e := h.c.Children(id, rootID, sortBy)
		if e != nil {
			t.Fatalf("children %q: %v", sortBy, e)
		}
		for i, r := range whole.Rows {
			if paged[i] != r.ID || kids[i].ID != r.ID || kids[i].IsDir != r.IsDir {
				t.Fatalf("%q: row %d is %s whole, %s paged, %s in Children", sortBy, i, r.Name, paged[i], kids[i].ID)
			}
		}
		if len(paged) != 14 || len(kids) != 14 {
			t.Fatalf("%q: %d paged, %d children", sortBy, len(paged), len(kids))
		}
	}
	// The orders themselves, at the boundary: name is the default.
	whole, _ := h.c.Page(id, rootID, "", 0, 100)
	if got := whole.Rows[0].Name + " " + whole.Rows[1].Name + " " + whole.Rows[2].Name + " " + whole.Rows[13].Name; got != "alpha Beta f01.txt f12.txt" {
		t.Fatalf("the default order: %q", got)
	}
	whole, _ = h.c.Page(id, rootID, "-size,-name", 0, 100)
	if got := whole.Rows[2].Name + " " + whole.Rows[3].Name + " " + whole.Rows[13].Name; got != "f12.txt f11.txt f01.txt" {
		t.Fatalf("-size,-name: %q", got)
	}
	whole, _ = h.c.Page(id, rootID, "-modified", 0, 100)
	if got := whole.Rows[2].Name + " " + whole.Rows[13].Name; got != "f01.txt f12.txt" {
		t.Fatalf("-modified: %q", got)
	}
	whole, _ = h.c.Page(id, rootID, "-size", 0, 100)
	if got := whole.Rows[2].Name + " " + whole.Rows[3].Name; got != "f11.txt f12.txt" {
		t.Fatalf("-size with the name tie-break ascending: %q", got)
	}

	if _, e := h.c.Page(id, rootID, "colour", 0, 10); !isCode(e, CodeParams) {
		t.Fatalf("an unknown sort key: %v", e)
	}
	if _, e := h.c.Children(id, rootID, "size,size"); !isCode(e, CodeParams) {
		t.Fatalf("an unreadable tie-break: %v", e)
	}
	if _, e := h.c.Children(id, "nothex", ""); !isCode(e, CodeParams) {
		t.Fatalf("a bad id: %v", e)
	}
	if _, e := h.c.Children(id, h.row(t, id, rootID, "f01.txt").ID, ""); !isCode(e, CodeFileNotFound) {
		t.Fatalf("a file's id: %v", e)
	}
	kids, _ := h.c.Children(id, h.row(t, id, rootID, "alpha").ID, "")
	if len(kids) != 0 {
		t.Fatalf("an empty folder's children: %v", kids)
	}
}

// The page's limit is clamped to a thousand rows — never reset to two
// hundred for asking for more — and one of zero or less is two hundred.
func TestPageLimitIsClamped(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Long")
	paths := make([]string, 0, 1005)
	for i := 0; i < 1005; i++ {
		paths = append(paths, h.src(t, fmt.Sprintf("n%04d", i), "x"))
	}
	h.add(t, id, rootID, PolicySkip, paths...)
	for limit, want := range map[int]int{5000: 1000, 1001: 1000, 1000: 1000, 0: 200, -1: 200, 7: 7} {
		p, e := h.c.Page(id, rootID, "", 0, limit)
		if e != nil {
			t.Fatal(e)
		}
		if len(p.Rows) != want || p.Total != 1005 {
			t.Fatalf("limit %d: %d rows of %d, want %d", limit, len(p.Rows), p.Total, want)
		}
	}
	p, _ := h.c.Page(id, rootID, "", 1000, 1000)
	if len(p.Rows) != 5 || p.Rows[0].Name != "n1000" {
		t.Fatalf("the last page: %d rows, first %q", len(p.Rows), p.Rows[0].Name)
	}
	kids, _ := h.c.Children(id, rootID, "")
	if len(kids) != 1005 {
		t.Fatalf("children: %d", len(kids))
	}
}

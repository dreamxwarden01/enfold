package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// Extract's names and the destination's refusals (APP.md §3, ruled
// 2026-09-10): a record written under another element for one extract
// only, and a name or a path the volume would not take reported per record
// with the rest of the batch written.

// outcomesByName indexes an operation's results by the record's archive
// path.
func outcomesByName(o OpView) map[string]FileOutcome {
	m := make(map[string]FileOutcome, len(o.Results))
	for _, r := range o.Results {
		m[r.Name] = r
	}
	return m
}

// names maps a record id to the one path element it is written under in
// this extract: a file under a new name, a directory's new name carrying its
// subtree; the record is untouched, and the outcome's Path is what was
// written. A name that breaks R20, an id that is not a record's, or two
// records landing on one path is params.
func TestExtractNamesWriteUnderTheGivenElement(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Named")
	h.src(t, "d/x.txt", "x")
	h.src(t, "d/inner/y.txt", "y")
	h.addFolder(t, id, rootID, filepath.Join(h.dir, "src", "d"), PolicySkip)
	h.add(t, id, rootID, PolicySkip, h.src(t, "a.txt", "a"), h.src(t, "b.txt", "b"))
	aid := h.row(t, id, rootID, "a.txt").ID
	bid := h.row(t, id, rootID, "b.txt").ID
	did := h.row(t, id, rootID, "d").ID

	for _, bad := range []map[string]string{
		{aid: "bad/name"}, {aid: "CON"}, {aid: ""}, {aid: strings.Repeat("n", 256)},
		{rootID: "root"}, {"nothex": "fine.txt"},
	} {
		if _, e := h.c.Extract(id, []string{rootID}, outDir(t), ExtractSkip, bad); !isCode(e, CodeParams) {
			t.Fatalf("names %v: %v", bad, e)
		}
	}

	out := outDir(t)
	opID, e := h.c.Extract(id, []string{rootID}, out, ExtractSkip, map[string]string{aid: "renamed.txt", did: "D2"})
	if e != nil {
		t.Fatal(e)
	}
	o := h.rec.waitOp(t, opID)
	if o.Error != "" || len(o.Results) != 6 {
		t.Fatalf("names: %+v", o)
	}
	by := outcomesByName(o)
	for name, want := range map[string]string{
		"a.txt":         filepath.Join(out, "renamed.txt"),
		"b.txt":         filepath.Join(out, "b.txt"),
		"d":             filepath.Join(out, "D2"),
		"d/x.txt":       filepath.Join(out, "D2", "x.txt"),
		"d/inner":       filepath.Join(out, "D2", "inner"),
		"d/inner/y.txt": filepath.Join(out, "D2", "inner", "y.txt"),
	} {
		r, ok := by[name]
		if !ok || r.Path != want {
			t.Fatalf("%s: %+v, want Path %s", name, r, want)
		}
		if r.Outcome != "extracted" && r.Outcome != "created" {
			t.Fatalf("%s: %+v", name, r)
		}
		if !r.IsDir {
			if b, err := os.ReadFile(want); err != nil || string(b) != name[len(name)-5:len(name)-4] {
				t.Fatalf("%s was not written at %s: %q %v", name, want, b, err)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(out, "a.txt")); !os.IsNotExist(err) {
		t.Fatal("the record's own name was written too")
	}
	if _, err := os.Stat(filepath.Join(out, "d")); !os.IsNotExist(err) {
		t.Fatal("the directory's own name was made too")
	}
	// The record itself is untouched.
	if h.row(t, id, rootID, "a.txt").ID != aid || h.row(t, id, rootID, "d").ID != did {
		t.Fatal("a name for one extract renamed the record")
	}

	// A collision under the new name follows policy: skip leaves the file
	// there, rename takes the next name beside it, and Path says which.
	opID, _ = h.c.Extract(id, []string{aid}, out, ExtractSkip, map[string]string{aid: "renamed.txt"})
	if o := h.rec.waitOp(t, opID); o.Results[0].Outcome != "skipped" {
		t.Fatalf("skip under the new name: %+v", o.Results)
	}
	opID, _ = h.c.Extract(id, []string{aid}, out, ExtractRename, map[string]string{aid: "renamed.txt"})
	o = h.rec.waitOp(t, opID)
	if r := o.Results[0]; r.Outcome != "extracted" || r.Path != filepath.Join(out, "renamed (2).txt") {
		t.Fatalf("rename under the new name: %+v", r)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "renamed (2).txt")); string(b) != "a" {
		t.Fatalf("the renamed copy: %q", b)
	}

	// Two records of one extract sent to one name is the caller's error.
	opID, e = h.c.Extract(id, []string{aid, bid}, outDir(t), ExtractSkip, map[string]string{aid: "same.txt", bid: "SAME.txt"})
	if e != nil {
		t.Fatal(e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != CodeParams || len(o.Results) != 0 {
		t.Fatalf("two records on one name: %+v", o)
	}
	// The de-duplication folds as R39 folds siblings — Unicode simple
	// folding, not a lower-casing: a long s (U+017F) renamed beside a
	// planned s.txt is one name to strings.EqualFold and to the index, so
	// it is one name here (the outside review's finding 5).
	h.add(t, id, rootID, PolicySkip, h.src(t, "s.txt", "s"))
	sid := h.row(t, id, rootID, "s.txt").ID
	opID, _ = h.c.Extract(id, []string{aid, sid}, outDir(t), ExtractSkip, map[string]string{aid: "ſ.txt"})
	if o := h.rec.waitOp(t, opID); o.Error != CodeParams || len(o.Results) != 0 {
		t.Fatalf("a folded collision with a planned name: %+v", o)
	}
	// A name for a record the plan does not reach is not used.
	opID, _ = h.c.Extract(id, []string{bid}, outDir(t), ExtractSkip, map[string]string{aid: "unused.txt"})
	if o := h.rec.waitOp(t, opID); o.Error != "" || len(o.Results) != 1 || o.Results[0].Name != "b.txt" {
		t.Fatalf("an unused name: %+v", o)
	}
}

// refusing stands in for a volume that will not take some names: the
// placement of a temporary onto a final name whose base is in names, and
// the creation of a directory whose base is in dirs, answer errno — what
// the platform's own placement and mkdir would return from the volume.
func refusing(errno syscall.Errno, names, dirs map[string]bool) extractFS {
	return extractFS{
		place: func(tmp, path string, replace bool) error {
			if names[filepath.Base(path)] {
				return &os.LinkError{Op: "rename", Old: tmp, New: path, Err: errno}
			}
			return extractFS{}.placeFile(tmp, path, replace)
		},
		mkdir: func(path string) error {
			if dirs[filepath.Base(path)] {
				return &os.PathError{Op: "mkdir", Path: path, Err: errno}
			}
			return extractFS{}.makeDir(path)
		},
	}
}

// A name the destination refuses is that record's outcome, name_refused
// with file.name_refused — a file's on placement, a directory's on its
// creation, the directory's reported once for its top with nothing beneath
// it attempted or listed (the outside review's finding 6) — and the rest
// of the batch is written; a temporary it refuses is path_refused.
// Keep-both numbering stops at the first refusal that is not "already
// exists". Nothing of it is the operation's error.
func TestExtractRefusalsArePerRecord(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Refused")
	h.src(t, "deep/x.txt", "x")
	h.src(t, "deep/inner/y.txt", "y")
	h.src(t, "fine/z.txt", "z")
	h.addFolder(t, id, rootID, filepath.Join(h.dir, "src", "deep"), PolicySkip)
	h.addFolder(t, id, rootID, filepath.Join(h.dir, "src", "fine"), PolicySkip)
	h.add(t, id, rootID, PolicySkip, h.src(t, "long.txt", "long"), h.src(t, "ok.txt", "ok"))

	h.c.extractFS = refusing(syscall.ENAMETOOLONG, map[string]bool{"long.txt": true}, map[string]bool{"deep": true})
	out := outDir(t)
	opID, e := h.c.Extract(id, []string{rootID}, out, ExtractSkip, nil)
	if e != nil {
		t.Fatal(e)
	}
	o := h.rec.waitOp(t, opID)
	if o.Error != "" {
		t.Fatalf("a refusal became the operation's error: %+v", o)
	}
	by := outcomesByName(o)
	if len(o.Results) != 5 {
		// deep once, nothing beneath it; fine, fine/z.txt, long.txt, ok.txt.
		t.Fatalf("the refused folder's subtree was listed: %+v", o.Results)
	}
	if r := by["long.txt"]; r.Outcome != "name_refused" || r.Code != CodeFileNameRefused || r.Path != filepath.Join(out, "long.txt") {
		t.Fatalf("the refused name: %+v", r)
	}
	if r := by["deep"]; r.Outcome != "name_refused" || r.Code != CodeFileNameRefused || !r.IsDir || r.Path != filepath.Join(out, "deep") {
		t.Fatalf("the refused folder: %+v", r)
	}
	for _, name := range []string{"fine", "fine/z.txt", "ok.txt"} {
		if r := by[name]; r.Outcome != "extracted" && r.Outcome != "created" {
			t.Fatalf("the rest of the batch: %s %+v", name, r)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(out, "ok.txt")); string(b) != "ok" {
		t.Fatalf("the rest of the batch was not written: %q", b)
	}
	entries, _ := os.ReadDir(out)
	for _, en := range entries {
		if strings.HasPrefix(en.Name(), ".enfold-") {
			t.Fatalf("a refused placement left its temporary: %s", en.Name())
		}
	}

	// Shorten and Rename… re-issue with names; a shorter name the volume
	// takes is extracted under it, with Path saying so.
	lid := h.row(t, id, rootID, "long.txt").ID
	opID, _ = h.c.Extract(id, []string{lid}, out, ExtractSkip, map[string]string{lid: "l.txt"})
	if r := h.rec.waitOp(t, opID).Results[0]; r.Outcome != "extracted" || r.Path != filepath.Join(out, "l.txt") {
		t.Fatalf("the shortened name: %+v", r)
	}
	// A directory's name likewise: the folder is created under the new
	// name and its subtree, held back by the refusal, comes out beneath it.
	did := h.row(t, id, rootID, "deep").ID
	opID, _ = h.c.Extract(id, []string{did}, out, ExtractSkip, map[string]string{did: "dp"})
	o = h.rec.waitOp(t, opID)
	by = outcomesByName(o)
	if o.Error != "" || len(o.Results) != 4 || by["deep"].Outcome != "created" || by["deep"].Path != filepath.Join(out, "dp") {
		t.Fatalf("the shortened folder: %+v", o)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "dp", "inner", "y.txt")); string(b) != "y" {
		t.Fatalf("the shortened folder's subtree: %q", b)
	}

	// Under rename, a refusal on a numbered name ends the numbering: the
	// outcome is the refusal, and no longer name is tried.
	if err := os.WriteFile(filepath.Join(out, "ok.txt"), []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.c.extractFS = refusing(syscall.ENAMETOOLONG, map[string]bool{"ok (2).txt": true}, nil)
	okid := h.row(t, id, rootID, "ok.txt").ID
	opID, _ = h.c.Extract(id, []string{okid}, out, ExtractRename, nil)
	if r := h.rec.waitOp(t, opID).Results[0]; r.Outcome != "name_refused" || r.Path != filepath.Join(out, "ok (2).txt") {
		t.Fatalf("a refusal inside the numbering: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(out, "ok (3).txt")); !os.IsNotExist(err) {
		t.Fatal("the numbering went on past the refusal")
	}

	// The temporary refused is the path refused: extractFile marks the
	// step, and the classifier reads the mark.
	if c := classify(fmt.Errorf("%w: %w", errPathRefused, syscall.ENAMETOOLONG)).Code; c != CodeFilePathRefused {
		t.Fatalf("a refused temporary classifies as %s", c)
	}
	if c := classify(fmt.Errorf("%w: %w", errNameRefused, syscall.ENAMETOOLONG)).Code; c != CodeFileNameRefused {
		t.Fatalf("a refused name classifies as %s", c)
	}
	// Bare, a volume's refusal is the path's: the destination root that
	// would not be made is the operation's error, before any outcome.
	if c := classify(&os.PathError{Op: "mkdir", Path: "x", Err: syscall.ENAMETOOLONG}).Code; c != CodeFilePathRefused {
		t.Fatalf("a bare refusal classifies as %s", c)
	}
	// An error that is not a refusal is still what it was.
	if c := classify(&os.PathError{Op: "mkdir", Path: "x", Err: syscall.EACCES}); c.Code != CodeIO {
		t.Fatalf("a permission error classifies as %s", c.Code)
	}
	if errors.Is(fmt.Errorf("%w: %w", errNameRefused, syscall.ENAMETOOLONG), errPathRefused) {
		t.Fatal("the two marks are one")
	}
}

// Keep-both numbering is bounded — (2) to (999) — and when every name is
// taken the outcome is failed with file.exists, not skipped: keep both was
// asked for and nothing was kept.
func TestExtractKeepBothExhaustionFails(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Full")
	h.add(t, id, rootID, PolicySkip, h.src(t, "g.txt", "g"))
	out := outDir(t)
	if err := os.WriteFile(filepath.Join(out, "g.txt"), []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	for n := 2; n < 1000; n++ {
		if err := os.WriteFile(filepath.Join(out, fmt.Sprintf("g (%d).txt", n)), []byte("mine"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	opID, e := h.c.Extract(id, []string{rootID}, out, ExtractRename, nil)
	if e != nil {
		t.Fatal(e)
	}
	o := h.rec.waitOp(t, opID)
	if o.Error != "" || len(o.Results) != 1 {
		t.Fatalf("exhaustion: %+v", o)
	}
	if r := o.Results[0]; r.Outcome != "failed" || r.Code != CodeFileExists {
		t.Fatalf("every name taken: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(out, "g (1000).txt")); !os.IsNotExist(err) {
		t.Fatal("the numbering went past (999)")
	}
	// Skip and ask are still what they were.
	opID, _ = h.c.Extract(id, []string{rootID}, out, ExtractSkip, nil)
	if r := h.rec.waitOp(t, opID).Results[0]; r.Outcome != "skipped" || r.Code != "" {
		t.Fatalf("skip: %+v", r)
	}
}

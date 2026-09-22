package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The launch argument of APP.md §14: the first argument that is a file
// that is there is what opens, the rest are counted in a toast, and a
// relative path belongs to the launch's working directory and not to this
// process's.
func TestOpenArgumentsTakesTheFilesInOrder(t *testing.T) {
	dir := t.TempDir()
	one := filepath.Join(dir, "one.efd")
	two := filepath.Join(dir, "two.efd")
	for _, p := range []string{one, two} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sub := filepath.Join(dir, "folder")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	args := []string{
		"--flag",                            // Enfold takes none; not a path
		filepath.Join(dir, "not-there.efd"), // nothing to open
		sub,                                 // a folder is not a file
		"two.efd",                           // relative, against the launch's own folder
		one,                                 // absolute
	}
	got := openArguments(args, dir)
	if len(got) != 2 {
		t.Fatalf("openArguments gave %v, want the two files that are there", got)
	}
	if !filepath.IsAbs(got[0]) || !filepath.IsAbs(got[1]) {
		t.Fatalf("openArguments gave %v, want absolute paths — the core refuses a relative one", got)
	}
	if filepath.Base(got[0]) != "two.efd" || filepath.Base(got[1]) != "one.efd" {
		t.Fatalf("openArguments gave %v, want two.efd first — the order the launch gave", got)
	}
	// The relative one resolved against the working directory it was
	// given, never this process's.
	if got[0] != two {
		t.Fatalf("the relative argument became %s, want %s", got[0], two)
	}
}

// A launch that named nothing openable opens nothing: no slot is filled
// and no event is emitted.
func TestOpenArgumentsOfNothing(t *testing.T) {
	dir := t.TempDir()
	if got := openArguments(nil, dir); got != nil {
		t.Fatalf("no arguments gave %v", got)
	}
	if got := openArguments([]string{"", "-x", "--", filepath.Join(dir, "missing")}, dir); got != nil {
		t.Fatalf("flags and a missing file gave %v", got)
	}
}

// SecondInstanceData.Args is the whole os.Args of the launch that handed
// over, the program's own path included — a file that exists, so passing
// it on would have Enfold open Enfold.
func TestAfterProgramDropsTheExecutable(t *testing.T) {
	if got := afterProgram([]string{`C:\Programs\Enfold\enfold.exe`, "a.efd"}); len(got) != 1 || got[0] != "a.efd" {
		t.Fatalf("afterProgram gave %v, want the arguments after the program", got)
	}
	if got := afterProgram(nil); got != nil {
		t.Fatalf("afterProgram of nothing gave %v", got)
	}
	if got := afterProgram([]string{"enfold.exe"}); len(got) != 0 {
		t.Fatalf("afterProgram of the program alone gave %v", got)
	}
}

// The slot: a launch's request waits there for the page's boot, each
// request is numbered so the page opens it once however it reached it,
// and the take empties it.
func TestPendingOpenIsTakenOnce(t *testing.T) {
	var p pendingOpen
	if req := p.stage(nil); req != nil {
		t.Fatalf("staging nothing gave %+v", req)
	}
	if got := p.take(); got.Seq != 0 || got.Path != "" {
		t.Fatalf("an empty slot gave %+v, want Seq 0", got)
	}
	req := p.stage([]string{"a.efd", "b.efd", "c.efd"})
	if req == nil || req.Seq != 1 || req.Path != "a.efd" || len(req.Rest) != 2 {
		t.Fatalf("staged %+v, want the first opened and two more named", req)
	}
	got := p.take()
	if got.Seq != 1 || got.Path != "a.efd" || len(got.Rest) != 2 {
		t.Fatalf("took %+v, want the request staged", got)
	}
	if again := p.take(); again.Seq != 0 {
		t.Fatalf("the slot answered %+v twice", again)
	}
	// A launch after the slot was emptied is a request of its own.
	if r := p.stage([]string{"d.efd"}); r == nil || r.Seq != 2 || r.Path != "d.efd" {
		t.Fatalf("the launch after the take staged %+v, want Seq 2 on d.efd", r)
	}
}

// A second launch before the page has come for the first: the two merge
// rather than one replacing the other (the review's finding 2). Two
// double-clicks before the window has drawn are two files the user asked
// for, and dropping the first would lose the one they asked for first and
// say nothing about it at all.
func TestPendingOpenMergesALaunchOntoOneStillWaiting(t *testing.T) {
	var p pendingOpen
	p.stage([]string{"first.efd", "second.efd"})
	r := p.stage([]string{"third.efd", "fourth.efd"})
	if r == nil || r.Seq != 2 {
		t.Fatalf("the second launch staged %+v, want Seq 2 — a request the page has not seen", r)
	}
	if r.Path != "first.efd" {
		t.Fatalf("the merged request opens %s, want the file asked for first", r.Path)
	}
	if got := strings.Join(r.Rest, ","); got != "second.efd,third.efd,fourth.efd" {
		t.Fatalf("the rest is %q, want every other file named, in the order they were asked for", got)
	}
	// The same file double-clicked twice is one file, and the one that
	// opens is never named among the rest either.
	r = p.stage([]string{"first.efd", "third.efd", "fifth.efd"})
	if got := strings.Join(r.Rest, ","); got != "second.efd,third.efd,fourth.efd,fifth.efd" {
		t.Fatalf("the rest is %q, want the duplicates dropped", got)
	}
	// And the take hands the whole of it over, once.
	got := p.take()
	if got.Seq != 3 || got.Path != "first.efd" || len(got.Rest) != 4 {
		t.Fatalf("took %+v, want the merged request", got)
	}
	if again := p.take(); again.Seq != 0 {
		t.Fatalf("the slot answered %+v twice", again)
	}
}

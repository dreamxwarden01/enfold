package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// More tests of the add's checkpoints (ruled 2026-11-03, APP.md
// §2.3): the rule's two arms each on their own — the tests beside these
// lower both to zero — and a folder add stopped between checkpoints, whose
// results must name only what a commit published.

// withRule sets the checkpoint rule for one test.
func withRule(t *testing.T, r checkpointRule) {
	t.Helper()
	was := addCheckpoint
	addCheckpoint = r
	t.Cleanup(func() { addCheckpoint = was })
}

// rowsAtEachCommit adds the paths and returns how many rows the root held
// at each archive.changed of this archive.
func (h *harness) rowsAtEachCommit(t *testing.T, id string, paths []string, onProgress func()) (OpView, []int) {
	t.Helper()
	var mu sync.Mutex
	var rows []int
	h.rec.onEvent(func(name string, payload any) {
		switch v := payload.(type) {
		case ArchiveChanged:
			if name != EventArchiveChanged || v.ID != id {
				return
			}
			p, e := h.c.Page(id, rootID, "name", 0, 100)
			mu.Lock()
			if e == nil {
				rows = append(rows, len(p.Rows))
			} else {
				rows = append(rows, -1)
			}
			mu.Unlock()
		case OpView:
			if name == EventOpProgress && onProgress != nil {
				onProgress()
			}
		}
	})
	o := h.add(t, id, rootID, PolicySkip, paths...)
	h.rec.onEvent(nil)
	mu.Lock()
	defer mu.Unlock()
	return o, append([]int(nil), rows...)
}

// The bytes arm alone: with the time arm out of reach, a checkpoint comes
// once the files written since the last commit reach the figure, and never
// splits a file — 64 KiB files against 100 KiB commit two at a time.
func TestTheBytesArmCommitsEveryTwoFiles(t *testing.T) {
	withRule(t, checkpointRule{every: time.Hour, bytes: 100 << 10})
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Bytes")
	paths, _ := h.sources(t, "b", 5, 64<<10)
	before := h.stat(t, id)
	o, rows := h.rowsAtEachCommit(t, id, paths, nil)
	if o.Error != "" || len(o.Results) != 5 {
		t.Fatalf("the add: %+v", o)
	}
	if fmt.Sprint(rows) != "[2 4 5]" {
		t.Fatalf("rows at each commit: %v", rows)
	}
	if st := h.stat(t, id); st.Seq != before.Seq+3 || st.Files != 5 {
		t.Fatalf("after the add: %+v, was %+v", st, before)
	}
}

// The time arm alone, on the core's clock: with the default rule and the
// clock moved a second at every progress event, every file after the first
// finds the one before it committed.
func TestTheTimeArmOnTheCoresClock(t *testing.T) {
	withRule(t, checkpointRule{every: time.Second, bytes: 256 << 20})
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Seconds")
	paths, _ := h.sources(t, "s", 4, 8<<10)
	o, rows := h.rowsAtEachCommit(t, id, paths, func() { h.clk.Advance(time.Second) })
	if o.Error != "" || len(o.Results) != 4 {
		t.Fatalf("the add: %+v", o)
	}
	if fmt.Sprint(rows) != "[1 2 3 4]" {
		t.Fatalf("rows at each commit: %v", rows)
	}
}

// A folder add stopped between checkpoints — inside the file after the
// second — reports only what the commits published: every folder and file
// among its results is in the archive, nothing else is, and the folders
// made after the last checkpoint went with the abort.
func TestAStoppedFolderAddReportsOnlyWhatLanded(t *testing.T) {
	withRule(t, checkpointRule{})
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Stopped tree")
	h.src(t, "walk/a.txt", "a")
	h.src(t, "walk/b/c.txt", "cc")
	h.src(t, "walk/d/e.txt", "eee")
	h.src(t, "walk/f/g.txt", "gggg")
	if err := os.MkdirAll(filepath.Join(h.dir, "src", "walk", "h"), 0o700); err != nil {
		t.Fatal(err)
	}
	before := h.stat(t, id)
	commits := 0
	answers := make(chan *Error, 1)
	var opID string
	ready := make(chan struct{})
	h.rec.onEvent(func(name string, payload any) {
		switch v := payload.(type) {
		case ArchiveChanged:
			if name == EventArchiveChanged && v.ID == id {
				commits++
				h.clk.Advance(time.Second) // let the next progress be said
			}
		case OpView:
			if name != EventOpProgress {
				return
			}
			<-ready
			// The first progress said after the second checkpoint is the
			// third file's: the cancel lands inside it or right after it,
			// before the next checkpoint either way.
			if commits == 2 && v.ID == opID && len(answers) == 0 {
				answers <- h.c.CancelOp(opID)
			}
		}
	})
	defer h.rec.onEvent(nil)
	var e *Error
	opID, e = h.c.AddFolder(id, rootID, filepath.Join(h.dir, "src", "walk"), PolicySkip)
	if e != nil {
		t.Fatal(e)
	}
	close(ready)
	o := h.rec.waitOp(t, opID)
	h.rec.onEvent(nil)
	if len(answers) != 1 {
		t.Fatalf("no cancel was taken after the second checkpoint (commits %d): %+v", commits, o)
	}
	if ans := <-answers; ans != nil {
		t.Fatalf("the cancel answered %v", ans)
	}
	if o.Error != CodeOpCancelled {
		t.Fatalf("the stopped folder add: %+v", o)
	}
	files, records := 0, 0
	for _, r := range o.Results {
		switch r.Outcome {
		case "added":
			files++
			records++
		case "created":
			records++
		default:
			t.Fatalf("an outcome the commits did not publish: %+v", r)
		}
	}
	st := h.stat(t, id)
	if st.Seq != before.Seq+2 || st.Files != files || st.Records != records {
		t.Fatalf("committed %+v (was %+v) against results %+v", st, before, o.Results)
	}
	if files != 2 {
		t.Fatalf("two checkpoints should keep two files: %+v", o.Results)
	}
	// Every result is a record of the archive, found by its path.
	for _, r := range o.Results {
		dir := rootID
		var cur FileRow
		for _, el := range splitSlash(r.Name) {
			cur = h.row(t, id, dir, el)
			dir = cur.ID
		}
		if cur.ID != r.ID {
			t.Fatalf("%s: the result's id %s is not the record's %s", r.Name, r.ID, cur.ID)
		}
	}
	h.reopen(t, id)
	h.verifies(t, id)
}

func splitSlash(p string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(p); i++ {
		if i == len(p) || p[i] == '/' {
			if i > start {
				out = append(out, p[start:i])
			}
			start = i + 1
		}
	}
	return out
}

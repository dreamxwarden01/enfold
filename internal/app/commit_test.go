package app

import (
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// An operation is a transaction (APP.md §2.3, DECISIONS 2026-09-09): Begin
// at its start, Commit at its end, then the receipt, one registry write and
// archive.changed. Cancel is Abort — nothing published, and the bytes it
// wrote lie in extents the committed free map still holds free.

// incompressible is n bytes the compressor will not shrink, so that a source
// is read whole and stored at about its own size: a file that compresses to
// nothing would neither move the bar nor grow the archive.
func incompressible(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	r := rand.New(rand.NewSource(1))
	r.Read(b)
	return b
}

// Every operation commits once: the archive's sequence advances by one per
// operation and the registry records the receipt each of them owes.
func TestEachOperationIsOneCommitWithItsReceipt(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "One")

	seq := h.stat(t, id).Seq
	lastSeq := uint64(0)
	step := func(what string, run func()) {
		t.Helper()
		run()
		st := h.stat(t, id)
		if st.Seq != seq+1 {
			t.Fatalf("%s: the archive's sequence went %d → %d", what, seq, st.Seq)
		}
		seq = st.Seq
		if st.ReceiptOwed {
			t.Fatalf("%s: the receipt was owed under a live session: %+v", what, st)
		}
		// The registry has the commit: last_seq advances with every one, so
		// a reopen never finds a file ahead of its record.
		d, e := h.c.ArchiveDetails(id)
		if e != nil {
			t.Fatalf("%s: details: %v", what, e)
		}
		if d.LastSeq <= lastSeq {
			t.Fatalf("%s: the registry's last_seq stood at %d", what, d.LastSeq)
		}
		lastSeq = d.LastSeq
	}

	var folder, file, second string
	step("create folder", func() {
		var e *Error
		folder, e = h.c.CreateFolder(id, rootID, "docs")
		if e != nil {
			t.Fatal(e)
		}
	})
	step("add files", func() {
		if o := h.add(t, id, rootID, PolicySkip, h.src(t, "a.txt", "one")); o.Error != "" {
			t.Fatalf("add: %+v", o)
		}
		file = h.row(t, id, rootID, "a.txt").ID
	})
	step("replace", func() {
		opID, e := h.c.ReplaceFile(id, file, h.src(t, "b.txt", "another content"))
		if e != nil {
			t.Fatal(e)
		}
		if o := h.rec.waitOp(t, opID); o.Error != "" {
			t.Fatalf("replace: %+v", o)
		}
	})
	step("rename", func() {
		if e := h.c.RenameRecord(id, file, "renamed.txt"); e != nil {
			t.Fatal(e)
		}
	})
	step("move", func() {
		if e := h.c.MoveRecords(id, []string{file}, folder); e != nil {
			t.Fatal(e)
		}
	})
	step("add folder", func() {
		h.src(t, "walk/inner/c.txt", "c")
		if o := h.addFolder(t, id, rootID, filepath.Join(h.dir, "src", "walk"), PolicySkip); o.Error != "" {
			t.Fatalf("add folder: %+v", o)
		}
		second = h.row(t, id, rootID, "walk").ID
	})
	step("delete", func() {
		if e := h.c.DeleteRecords(id, []string{second}); e != nil {
			t.Fatal(e)
		}
	})

	// Under a lock the commit still happens and the receipt is owed, which
	// the next unlock pays before any archive is opened (APP.md §2.3).
	h.c.Lock()
	h.rec.waitState(t, StateLocked)
	if _, e := h.c.CreateFolder(id, rootID, "after"); e != nil {
		t.Fatalf("create folder while locked: %v", e)
	}
	st := h.stat(t, id)
	if st.Seq != seq+1 || !st.ReceiptOwed {
		t.Fatalf("a commit under a lock: %+v", st)
	}
	h.unlockWithPassword()
	if st := h.stat(t, id); st.ReceiptOwed {
		t.Fatalf("the owed receipt was not paid at the unlock: %+v", st)
	}
	d, _ := h.c.ArchiveDetails(id)
	if d.LastSeq <= lastSeq {
		t.Fatalf("the paid receipt did not reach the registry: %+v", d)
	}
}

// Records counts live files and directories together — what Extract all is
// greyed on — and Files counts files alone.
func TestRecordsCountsFilesAndFolders(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Count")

	if st := h.stat(t, id); st.Records != 0 || st.Files != 0 {
		t.Fatalf("a fresh archive: %+v", st)
	}
	// A folder alone is a record: the file count cannot say whether the
	// tree holds anything (APP.md §3).
	folder, e := h.c.CreateFolder(id, rootID, "empty")
	if e != nil {
		t.Fatal(e)
	}
	if st := h.stat(t, id); st.Records != 1 || st.Files != 0 {
		t.Fatalf("one empty folder: %+v", st)
	}
	inner, _ := h.c.CreateFolder(id, folder, "inner")
	h.add(t, id, rootID, PolicySkip, h.src(t, "a.txt", "a"), h.src(t, "b.txt", "b"))
	h.add(t, id, inner, PolicySkip, h.src(t, "c.txt", "c"))
	if st := h.stat(t, id); st.Records != 5 || st.Files != 3 {
		t.Fatalf("two folders and three files: %+v", st)
	}
	if e := h.c.DeleteRecords(id, []string{folder}); e != nil {
		t.Fatal(e)
	}
	if st := h.stat(t, id); st.Records != 2 || st.Files != 2 {
		t.Fatalf("after the folder went with its subtree: %+v", st)
	}
}

// Progress is by bytes: a single file read in chunks moves the bar between
// the start and the end of that one file (APP.md §3).
func TestProgressMovesInsideOneFile(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Bar")

	big := filepath.Join(h.dir, "big.bin")
	if err := os.WriteFile(big, incompressible(t, 2<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	// Progress is coalesced to ten events a second against the core's own
	// clock, which here is the fake one and does not run: every event after
	// the first would be swallowed. The clock is moved on from inside each
	// event, so the throttle passes what a real second would.
	h.rec.onEvent(func(name string, _ any) {
		if name == EventOpProgress {
			h.clk.Advance(150 * time.Millisecond)
		}
	})
	defer h.rec.onEvent(nil)
	opID, e := h.c.AddFiles(id, rootID, []string{big}, PolicySkip)
	if e != nil {
		t.Fatal(e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != "" {
		t.Fatalf("add: %+v", o)
	}
	var inside int
	for _, ev := range h.rec.snapshot() {
		if ev.name != EventOpProgress {
			continue
		}
		o, ok := ev.payload.(OpView)
		if !ok || o.ID != opID {
			continue
		}
		if o.Total != 2<<20 {
			t.Fatalf("the total is not the batch's plaintext: %+v", o)
		}
		if o.Done > 0 && o.Done < o.Total {
			inside++
		}
	}
	if inside == 0 {
		t.Fatal("the bar stood still for the whole file: no progress between its start and its end")
	}
}

// And on the way out: an extract counts the bytes written of the file in
// hand, so a single large file moves the bar there too (APP.md §3 Events —
// "an add, a replace and an extract count the bytes read of the file in
// hand"). The counting sits on extract.go's writer, which is its own path.
func TestExtractProgressMovesInsideOneFile(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Out")

	big := filepath.Join(h.dir, "big.bin")
	if err := os.WriteFile(big, incompressible(t, 2<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	h.add(t, id, rootID, PolicySkip, big)

	// As above: the throttle is against the core's fake clock, which is
	// moved on from inside each event so that what a real second would pass
	// is passed here.
	h.rec.onEvent(func(name string, _ any) {
		if name == EventOpProgress {
			h.clk.Advance(150 * time.Millisecond)
		}
	})
	defer h.rec.onEvent(nil)
	opID, e := h.c.Extract(id, []string{rootID}, outDir(t), ExtractSkip)
	if e != nil {
		t.Fatal(e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != "" {
		t.Fatalf("extract: %+v", o)
	}
	var inside int
	for _, ev := range h.rec.snapshot() {
		if ev.name != EventOpProgress {
			continue
		}
		o, ok := ev.payload.(OpView)
		if !ok || o.ID != opID {
			continue
		}
		if o.Total != 2<<20 {
			t.Fatalf("the total is not the plan's plaintext: %+v", o)
		}
		if o.Done > 0 && o.Done < o.Total {
			inside++
		}
	}
	if inside == 0 {
		t.Fatal("the bar stood still for the whole file: no progress between its start and its end")
	}
}

// Cancel on a running add is Abort: nothing is published, the archive's
// sequence does not move, and the bytes the add wrote are given back — they
// lay in extents the committed free map still held free (APP.md §2.3).
func TestCancelOfARunningAddPublishesNothing(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Cancel")

	dir := filepath.Join(h.dir, "src", "batch")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one.bin", "two.bin"} {
		if err := os.WriteFile(filepath.Join(dir, name), incompressible(t, 2<<20), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	before := h.stat(t, id)

	// The cancel is taken inside the operation's own progress event, on its
	// own goroutine: it lands at a known point of the add rather than
	// racing it. By then the first file has been read whole.
	var opID string
	h.rec.onEvent(func(name string, payload any) {
		if name != EventOpProgress {
			return
		}
		o, ok := payload.(OpView)
		if !ok || o.ID != opID || o.Done < 2<<20 {
			return
		}
		h.c.CancelOp(opID)
	})
	defer h.rec.onEvent(nil)
	opID, e := h.c.AddFolder(id, rootID, dir, PolicySkip)
	if e != nil {
		t.Fatal(e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != CodeOpCancelled {
		t.Fatalf("the cancelled add: %+v", o)
	}
	if p := h.page(t, id, rootID); p.Total != 0 {
		t.Fatalf("a cancelled add published rows: %+v", p.Rows)
	}
	st := h.stat(t, id)
	if st.Seq != before.Seq || st.Records != 0 {
		t.Fatalf("a cancelled add committed: %+v, was %+v", st, before)
	}
	if st.Size != before.Size {
		t.Fatalf("the aborted add's bytes were not given back: %d, was %d", st.Size, before.Size)
	}
	// And the space is there for the next add: the same batch, uncancelled,
	// lands in the file the abort left behind.
	h.rec.onEvent(nil)
	if o := h.addFolder(t, id, rootID, dir, PolicySkip); o.Error != "" {
		t.Fatalf("the add after the cancel: %+v", o)
	}
	if st := h.stat(t, id); st.Records != 3 || st.Files != 2 {
		t.Fatalf("after the second add: %+v", st)
	}
}

// Every extraction records where it went, so the extract dialog's
// destination is prefilled with it next time (APP.md §3, settings.json's
// lastExtractFolder). Read-only on the Settings view, like the archive
// folder: only an extraction writes it.
func TestExtractRemembersItsFolder(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Where")
	h.add(t, id, rootID, PolicySkip, h.src(t, "a.txt", "a"))

	if got := h.c.GetSettings().LastExtractFolder; got != "" {
		t.Fatalf("a fresh core already has a folder: %q", got)
	}
	// A destination that does not exist yet is created (APP.md §3).
	out := filepath.Join(outDir(t), "made", "here")
	opID, e := h.c.Extract(id, []string{rootID}, out, ExtractSkip)
	if e != nil {
		t.Fatal(e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != "" {
		t.Fatalf("extract: %+v", o)
	}
	if _, err := os.Stat(filepath.Join(out, "a.txt")); err != nil {
		t.Fatalf("the destination was not made: %v", err)
	}
	if got := h.c.GetSettings().LastExtractFolder; got != out {
		t.Fatalf("lastExtractFolder %q, want %q", got, out)
	}
	// It survives a restart of the core: the file carries it.
	if got := loadSettings(h.c.deps.DataDir).LastExtractFolder; got != out {
		t.Fatalf("the settings file says %q", got)
	}
	// And Set ignores it.
	s := h.c.GetSettings()
	s.LastExtractFolder = filepath.Join(h.dir, "elsewhere")
	if e := h.c.SetSettings(s); e != nil {
		t.Fatal(e)
	}
	if got := h.c.GetSettings().LastExtractFolder; got != out {
		t.Fatalf("Set wrote the folder: %q", got)
	}
}

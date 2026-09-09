package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dreamxwarden01/enfold/internal/compress"
)

// Replace is the in-place edit and its own commit: the record keeps its id,
// the row shows the new size, and the file holds it at once.
func TestReplaceKeepsTheRecordAndCommits(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, _ := h.c.CreateArchive(filepath.Join(h.dir, "a.enf"), "A", compressionNormal)
	h.c.OpenArchive(id)
	small := filepath.Join(h.dir, "small.txt")
	big := filepath.Join(h.dir, "big.txt")
	os.WriteFile(small, []byte("small"), 0o600)
	os.WriteFile(big, []byte("a much bigger content than before"), 0o600)
	opID, _ := h.c.AddFiles(id, rootID, []string{small}, PolicySkip)
	h.rec.waitOp(t, opID)
	page, _ := h.c.Page(id, rootID, "name", 0, 10)
	if len(page.Rows) != 1 || page.Rows[0].Size != 5 {
		t.Fatalf("added: %+v", page.Rows)
	}
	was := page.Rows[0].ID
	before := h.stat(t, id).Seq
	opID, e := h.c.ReplaceFile(id, was, big)
	if e != nil {
		t.Fatal(e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != "" {
		t.Fatalf("replace: %+v", o)
	}
	page, _ = h.c.Page(id, rootID, "name", 0, 10)
	if len(page.Rows) != 1 || page.Rows[0].ID != was || page.Rows[0].Size != 33 {
		t.Fatalf("after replace: %+v", page.Rows)
	}
	if st := h.stat(t, id); st.Seq != before+1 {
		t.Fatalf("the replace's commits: %d, from %d", st.Seq, before)
	}
	// It is the file that holds it: a reopen reads the new content back.
	if e := h.c.CloseArchive(id); e != nil {
		t.Fatal(e)
	}
	if _, e := h.c.OpenArchive(id); e != nil {
		t.Fatal(e)
	}
	text, _, e := h.c.PreviewText(id, was, 64)
	if e != nil || text != "a much bigger content than before" {
		t.Fatalf("after the reopen: %q %v", text, e)
	}
}

// A file that is not the copy the registry last saw is flagged, never
// adopted silently; the next commit records this copy and clears it. The row's
// Note is the presence map's until a pass has run: an unmeasured record is
// blank, never "missing", so nothing takes the slot the mismatch needs
// (APP.md §13).
func TestCopyMismatchIsShown(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	h.waitPresencePass()
	ap := filepath.Join(h.dir, "a.enf")
	id, _ := h.c.CreateArchive(ap, "A", compressionNormal)
	older, err := os.ReadFile(ap)
	if err != nil {
		t.Fatal(err)
	}
	// The file is gone and no pass has run: the row says nothing about it.
	if err := os.Remove(ap); err != nil {
		t.Fatal(err)
	}
	if list, _ := h.c.ListArchives(false); list[0].Note != "" {
		t.Fatalf("an unmeasured record: %+v", list[0])
	}
	if e := h.c.CheckFiles(); e != nil {
		t.Fatal(e)
	}
	h.waitPresencePass()
	if list, _ := h.c.ListArchives(false); list[0].Note != CodeArchiveMissing {
		t.Fatalf("after a pass over an absent file: %+v", list[0])
	}
	if err := os.WriteFile(ap, older, 0o600); err != nil {
		t.Fatal(err)
	}
	if e := h.c.CheckFiles(); e != nil {
		t.Fatal(e)
	}
	h.waitPresencePass()
	if list, _ := h.c.ListArchives(false); list[0].Note != "" {
		t.Fatalf("after a pass over a file that is there: %+v", list[0])
	}
	h.c.OpenArchive(id)
	f := filepath.Join(h.dir, "f.txt")
	os.WriteFile(f, []byte("newer"), 0o600)
	opID, _ := h.c.AddFiles(id, rootID, []string{f}, PolicySkip)
	if o := h.rec.waitOp(t, opID); o.Error != "" {
		t.Fatalf("add: %+v", o)
	}
	if e := h.c.CloseArchive(id); e != nil {
		t.Fatal(e)
	}
	// An older copy of the file comes back (a restored backup).
	if err := os.WriteFile(ap, older, 0o600); err != nil {
		t.Fatal(err)
	}
	st, e := h.c.OpenArchive(id)
	if e != nil {
		t.Fatal(e)
	}
	if !st.CopyMismatch || st.Files != 0 {
		t.Fatalf("mismatch not flagged: %+v", st)
	}
	list, _ := h.c.ListArchives(false)
	if list[0].Note != CodeArchiveCopyMismatch {
		t.Fatalf("list note: %+v", list[0])
	}
	// Working on this copy records it — every operation commits and writes
	// its receipt — and the flag is gone at the next open.
	opID, _ = h.c.AddFiles(id, rootID, []string{f}, PolicySkip)
	if o := h.rec.waitOp(t, opID); o.Error != "" {
		t.Fatalf("add 2: %+v", o)
	}
	h.c.CloseArchive(id)
	st, _ = h.c.OpenArchive(id)
	if st.CopyMismatch {
		t.Fatalf("flag survived a commit that recorded this copy: %+v", st)
	}
}

// Closing an archive whose commit ended indeterminate releases the handle so
// the file can be opened again.
func TestCloseAfterNeedsReopen(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, _ := h.c.CreateArchive(filepath.Join(h.dir, "a.enf"), "A", compressionNormal)
	h.c.OpenArchive(id)
	aid, _ := parseID(id)
	h.c.mu.Lock()
	oa := h.c.archives[aid]
	oa.state = "needs_reopen"
	h.c.mu.Unlock()
	if _, e := h.c.Stat(id); !isCode(e, CodeArchiveNeedsReopen) {
		t.Fatalf("stat: %v", e)
	}
	if e := h.c.CloseArchive(id); e != nil {
		t.Fatalf("close: %v", e)
	}
	if _, e := h.c.OpenArchive(id); e != nil {
		t.Fatalf("reopen after close: %v", e)
	}
}

// A page asked for with a negative offset starts at the beginning.
func TestPageNegativeOffset(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, _ := h.c.CreateArchive(filepath.Join(h.dir, "a.enf"), "A", compressionNormal)
	h.c.OpenArchive(id)
	if _, e := h.c.Page(id, rootID, "name", -5, 10); e != nil {
		t.Fatal(e)
	}
}

// The compression method is chosen at creation and carried in the record's
// policy, so that every writer of the archive follows it (FORMAT.md §7.1
// bits 2–5, APP.md §3): the views name it, the writer's options follow it,
// and a word that is not one of the five is params.
func TestCreateArchiveCarriesTheCompressionMethod(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	if _, e := h.c.CreateArchive(filepath.Join(h.dir, "x.enf"), "X", "turbo"); !isCode(e, CodeParams) {
		t.Fatalf("an unknown method: %v", e)
	}
	if _, e := h.c.CreateArchive(filepath.Join(h.dir, "x.enf"), "X", ""); !isCode(e, CodeParams) {
		t.Fatalf("no method at all: %v", e)
	}
	for _, tc := range []struct {
		method string
		raw    bool
		level  compress.Level
	}{
		{compressionStore, true, compress.Default},
		{compressionFastest, false, compress.Fastest},
		{compressionNormal, false, compress.Default},
		{compressionBetter, false, compress.Better},
		{compressionBest, false, compress.Best},
	} {
		id, e := h.c.CreateArchive(filepath.Join(h.dir, tc.method+".enf"), tc.method, tc.method)
		if e != nil {
			t.Fatalf("%s: %v", tc.method, e)
		}
		d, e := h.c.ArchiveDetails(id)
		if e != nil || d.Method != tc.method {
			t.Fatalf("%s: details %+v %v", tc.method, d, e)
		}
		var found *ArchiveSummary
		list, _ := h.c.ListArchives(false)
		for i := range list {
			if list[i].ID == id {
				found = &list[i]
			}
		}
		if found == nil || found.Method != tc.method {
			t.Fatalf("%s: summary %+v", tc.method, found)
		}
		// The record's policy is what every open of the archive follows.
		aid, _ := parseID(id)
		h.c.mu.Lock()
		sess, _ := h.c.sessionLocked()
		rec := findRecord(sess.Registry(), aid)
		opts := h.c.archiveOptionsLocked(rec)
		h.c.mu.Unlock()
		if opts.NoCompression != tc.raw || opts.Compress.Level != tc.level {
			t.Fatalf("%s: options %+v", tc.method, opts)
		}
		// And an open reads it back onto the handle, so a list while locked
		// says the same word.
		if _, e := h.c.OpenArchive(id); e != nil {
			t.Fatalf("%s: open: %v", tc.method, e)
		}
		h.c.mu.Lock()
		got := h.c.archives[aid].method
		h.c.mu.Unlock()
		if got != tc.method {
			t.Fatalf("%s: the open handle says %q", tc.method, got)
		}
		if e := h.c.CloseArchive(id); e != nil {
			t.Fatal(e)
		}
	}
}

// Enfold never writes over a file it did not make: a create onto an
// occupied path is refused in place, and the file is untouched (APP.md §6,
// the archive layer's O_EXCL).
func TestCreateArchiveRefusesAnOccupiedPath(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	p := filepath.Join(h.dir, "taken.enf")
	if err := os.WriteFile(p, []byte("not ours"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, e := h.c.CreateArchive(p, "Taken", compressionNormal); !isCode(e, CodeArchiveExists) {
		t.Fatalf("create onto a file: %v", e)
	}
	b, err := os.ReadFile(p)
	if err != nil || string(b) != "not ours" {
		t.Fatalf("the file was touched: %q %v", b, err)
	}
	if list, _ := h.c.ListArchives(true); len(list) != 0 {
		t.Fatalf("a refused create left a record: %+v", list)
	}
}

// The folder of every created archive is remembered for the next Save
// dialog (APP.md §6, settings.json's lastArchiveFolder), read-only on the
// Settings view.
func TestCreateArchiveRemembersItsFolder(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	if got := h.c.GetSettings().LastArchiveFolder; got != "" {
		t.Fatalf("a fresh core already has a folder: %q", got)
	}
	sub := filepath.Join(h.dir, "archives")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, e := h.c.CreateArchive(filepath.Join(sub, "a.enf"), "A", compressionNormal); e != nil {
		t.Fatal(e)
	}
	if got := h.c.GetSettings().LastArchiveFolder; got != sub {
		t.Fatalf("lastArchiveFolder %q, want %q", got, sub)
	}
	// It survives a restart of the core: the file carries it.
	if got := loadSettings(h.c.deps.DataDir).LastArchiveFolder; got != sub {
		t.Fatalf("the settings file says %q", got)
	}
	// And Set ignores it: only a create writes it.
	s := h.c.GetSettings()
	s.LastArchiveFolder = filepath.Join(h.dir, "elsewhere")
	if e := h.c.SetSettings(s); e != nil {
		t.Fatal(e)
	}
	if got := h.c.GetSettings().LastArchiveFolder; got != sub {
		t.Fatalf("Set wrote the folder: %q", got)
	}
}

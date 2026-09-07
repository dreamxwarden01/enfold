package app

import (
	"os"
	"path/filepath"
	"testing"
)

// A staged add that is replaced stays one staged add: the row shows the
// new size and can still be un-staged by deleting it.
func TestReplaceOfStagedAddStaysOne(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, _ := h.c.CreateArchive(filepath.Join(h.dir, "a.enf"), "A", false)
	h.c.OpenArchive(id)
	small := filepath.Join(h.dir, "small.txt")
	big := filepath.Join(h.dir, "big.txt")
	os.WriteFile(small, []byte("small"), 0o600)
	os.WriteFile(big, []byte("a much bigger content than before"), 0o600)
	opID, _ := h.c.AddFiles(id, "", []string{small}, PolicySkip)
	h.rec.waitOp(t, opID)
	page, _ := h.c.Page(id, "", "name", 0, 10)
	if len(page.Rows) != 1 || page.Rows[0].Pending != "added" || page.Rows[0].Size != 5 {
		t.Fatalf("staged: %+v", page.Rows)
	}
	opID, e := h.c.ReplaceFile(id, page.Rows[0].FileID, big)
	if e != nil {
		t.Fatal(e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != "" {
		t.Fatalf("replace: %+v", o)
	}
	page, _ = h.c.Page(id, "", "name", 0, 10)
	if len(page.Rows) != 1 || page.Rows[0].Pending != "added" || page.Rows[0].Size != 33 {
		t.Fatalf("after replace: %+v", page.Rows)
	}
	if st, _ := h.c.Stat(id); st.Dirty != 1 {
		t.Fatalf("dirty after replace: %+v", st)
	}
	if e := h.c.DeleteFiles(id, []string{page.Rows[0].FileID}); e != nil {
		t.Fatal(e)
	}
	page, _ = h.c.Page(id, "", "name", 0, 10)
	if len(page.Rows) != 0 {
		t.Fatalf("staged add not un-staged: %+v", page.Rows)
	}
}

// A file that is not the copy the registry last saw is flagged, never
// adopted silently; the next save records this copy and clears it.
func TestCopyMismatchIsShown(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	ap := filepath.Join(h.dir, "a.enf")
	id, _ := h.c.CreateArchive(ap, "A", false)
	older, err := os.ReadFile(ap)
	if err != nil {
		t.Fatal(err)
	}
	h.c.OpenArchive(id)
	f := filepath.Join(h.dir, "f.txt")
	os.WriteFile(f, []byte("newer"), 0o600)
	opID, _ := h.c.AddFiles(id, "", []string{f}, PolicySkip)
	h.rec.waitOp(t, opID)
	opID, _ = h.c.Save(id)
	if o := h.rec.waitOp(t, opID); o.Error != "" {
		t.Fatalf("save: %+v", o)
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
	// Working on this copy and saving records it; the flag stays until
	// the archive is reopened against a matching record.
	opID, _ = h.c.AddFiles(id, "", []string{f}, PolicySkip)
	h.rec.waitOp(t, opID)
	opID, _ = h.c.Save(id)
	if o := h.rec.waitOp(t, opID); o.Error != "" {
		t.Fatalf("save 2: %+v", o)
	}
	h.c.CloseArchive(id)
	st, _ = h.c.OpenArchive(id)
	if st.CopyMismatch {
		t.Fatalf("flag survived a save that recorded this copy: %+v", st)
	}
}

// Closing an archive whose save ended indeterminate releases the handle so
// the file can be opened again.
func TestCloseAfterNeedsReopen(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, _ := h.c.CreateArchive(filepath.Join(h.dir, "a.enf"), "A", false)
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
	id, _ := h.c.CreateArchive(filepath.Join(h.dir, "a.enf"), "A", false)
	h.c.OpenArchive(id)
	if _, e := h.c.Page(id, "", "name", -5, 10); e != nil {
		t.Fatal(e)
	}
}

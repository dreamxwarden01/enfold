package app

import (
	"os"
	"path/filepath"
	"testing"
)

// Opening a file that is not a vault leaves the configured vault as it
// was: state, path and facts.
func TestOpenVaultFileFailureKeepsVault(t *testing.T) {
	h := newHarness(t, nil, nil)
	bad := filepath.Join(h.dir, "bad.eks")
	os.WriteFile(bad, []byte("not a keystore at all"), 0o600)
	if e := h.c.OpenVaultFile(bad, "Bad"); !isCode(e, CodeVaultInvalid) {
		t.Fatalf("garbage file: %v", e)
	}
	st := h.status()
	if st.State != StateLocked || st.Path != h.vault || st.DisplayName != "Test vault" || !st.HasPasswordSlot {
		t.Fatalf("vault replaced by a failed open: %+v", st)
	}
	if e := h.c.OpenVaultFile(filepath.Join(h.dir, "missing.eks"), "x"); !isCode(e, CodeVaultNotFound) {
		t.Fatalf("missing file: %v", e)
	}
	if st := h.status(); st.State != StateLocked || st.Path != h.vault {
		t.Fatalf("vault replaced by a missing file: %+v", st)
	}
	h.unlockWithPassword()
}

// A create that fails from a fresh core returns to "no vault", not to a
// Locked state over an empty path.
func TestCreateVaultFailureFromNone(t *testing.T) {
	dir := t.TempDir()
	rec := &recorder{}
	c, _ := New(Deps{Events: rec, Clock: newFakeClock(), DataDir: filepath.Join(dir, "data")})
	c.Start()
	defer c.Close()
	bad := filepath.Join(dir, "no-such-dir", "v.eks")
	if e := c.CreateVault(bad, "New", EnrollOptions{Kind: EnrollPassword, Label: "pw"}); e != nil {
		t.Fatal(e)
	}
	p := rec.waitCeremony(t, StepPassword, true)
	c.SubmitSecret("password", p.PromptID, "whatever")
	rec.waitCeremony(t, StepFailed, false)
	rec.waitState(t, StateNone)
	if st := c.Status(); st.State != StateNone || st.Path != "" {
		t.Fatalf("after a failed create: %+v", st)
	}
}

// An add in which every file is skipped stages nothing and leaves the
// archive clean; un-staging the last staged add does the same.
func TestNothingStagedIsClean(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, _ := h.c.CreateArchive(filepath.Join(h.dir, "a.enf"), "A", false)
	h.c.OpenArchive(id)
	f := filepath.Join(h.dir, "f.txt")
	os.WriteFile(f, []byte("once"), 0o600)
	opID, _ := h.c.AddFiles(id, "", []string{f}, PolicySkip)
	h.rec.waitOp(t, opID)
	opID, _ = h.c.Save(id)
	h.rec.waitOp(t, opID)
	// The same name again, skipped: nothing staged.
	opID, _ = h.c.AddFiles(id, "", []string{f}, PolicySkip)
	if o := h.rec.waitOp(t, opID); len(o.Results) != 1 || o.Results[0].Outcome != "skipped" {
		t.Fatalf("second add: %+v", o)
	}
	if st, _ := h.c.Stat(id); st.Dirty != 0 || st.State != "open" {
		t.Fatalf("skipped add left the archive dirty: %+v", st)
	}
	// Stage one add and un-stage it.
	g := filepath.Join(h.dir, "g.txt")
	os.WriteFile(g, []byte("twice"), 0o600)
	opID, _ = h.c.AddFiles(id, "", []string{g}, PolicySkip)
	h.rec.waitOp(t, opID)
	page, _ := h.c.Page(id, "", "name", 0, 10)
	var staged string
	for _, r := range page.Rows {
		if r.Pending == "added" {
			staged = r.FileID
		}
	}
	if staged == "" {
		t.Fatalf("no staged row: %+v", page.Rows)
	}
	if e := h.c.DeleteFiles(id, []string{staged}); e != nil {
		t.Fatal(e)
	}
	if st, _ := h.c.Stat(id); st.Dirty != 0 || st.State != "open" || st.CapAt != 0 {
		t.Fatalf("un-staging the last add left the archive dirty: %+v", st)
	}
	// Verify is allowed again on a clean archive.
	if _, e := h.c.Verify(id); e != nil {
		t.Fatalf("verify: %v", e)
	}
}

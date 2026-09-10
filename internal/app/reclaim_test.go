package app

import (
	"os"
	"testing"
)

// Reclaiming space (APP.md §2.3, DECISIONS 2026-09-09 "Space comes back"):
// the archive layer gives the tail back with the commit that frees it, and
// what is left is a hole with live data over it. After any commit that leaves
// the free space at or above the rule — 64 MiB and a quarter of the file —
// the core compacts the archive itself, as a follow-on operation the strip
// calls Reclaiming space.
//
// The thresholds here are lowered through the core's own rule rather than
// met: a test that made a 64 MiB archive would prove the same thing slowly.

// smallRule is the rule these tests measure against: a few kilobytes free,
// and still a quarter of the file.
var smallRule = reclaimRule{floor: 4 << 10, share: 4}

// holed makes an archive of two files and deletes the first, which leaves its
// extent as a hole under the second — space no truncation can give back. It
// returns the archive's id, the row of the file that stays, and the file's
// size on disk before the delete.
func (h *harness) holed(t *testing.T, name string, size int) (string, FileRow, uint64) {
	t.Helper()
	id, path := h.newArchive(name)
	if _, e := h.c.OpenArchive(id); e != nil {
		t.Fatalf("open %s: %v", name, e)
	}
	first := h.src(t, name+"-first.bin", string(incompressible(t, size)))
	second := h.src(t, name+"-second.bin", string(incompressible(t, size)))
	if o := h.add(t, id, rootID, PolicySkip, first, second); o.Error != "" {
		t.Fatalf("add: %+v", o)
	}
	stay := h.row(t, id, rootID, name+"-second.bin")
	drop := h.row(t, id, rootID, name+"-first.bin")
	before := h.stat(t, id).Size
	if e := h.c.DeleteRecords(id, []string{drop.ID}); e != nil {
		t.Fatalf("delete: %v", e)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	return id, stay, before
}

// waitReclaim waits for the follow-on compaction to finish.
func (h *harness) waitReclaim(t *testing.T) OpView {
	t.Helper()
	p := h.rec.waitFor(t, EventOpDone, func(p any) bool {
		o, ok := p.(OpView)
		return ok && o.Kind == "reclaim"
	})
	return p.(OpView)
}

// reclaimRunning reports whether a reclaim of any archive is on the books.
func (h *harness) reclaimRunning() bool {
	h.c.mu.Lock()
	defer h.c.mu.Unlock()
	for _, o := range h.c.ops {
		if o.kind == "reclaim" && !o.finished {
			return true
		}
	}
	return false
}

// A delete that leaves a hole over the rule is followed by the compaction,
// and the file ends at what it holds, with every file that stayed readable.
func TestADeleteOverTheRuleReclaimsTheSpace(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	h.c.mu.Lock()
	h.c.reclaim = smallRule
	h.c.mu.Unlock()

	h.rec.reset()
	id, stay, before := h.holed(t, "Reclaimed", 40000)
	if o := h.waitReclaim(t); o.Error != "" {
		t.Fatalf("the reclaim failed: %+v", o)
	}
	st := h.stat(t, id)
	if st.Size >= before-40000 {
		t.Errorf("the file stood at %d, from %d: the hole was not reclaimed", st.Size, before)
	}
	if st.FreeSpace != 0 {
		t.Errorf("free space after the reclaim: %d", st.FreeSpace)
	}
	if st.Files != 1 {
		t.Fatalf("files after the reclaim: %d", st.Files)
	}
	// The file that stayed is whole: it is read back through the archive.
	out := outDir(t)
	opID, e := h.c.Extract(id, []string{stay.ID}, out, ExtractSkip)
	if e != nil {
		t.Fatalf("extract: %v", e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != "" || len(o.Results) != 1 || o.Results[0].Outcome != "extracted" {
		t.Fatalf("extract after the reclaim: %+v", o)
	}
	// The receipt the compaction owes is in the registry, at the seq the
	// hash was taken over.
	d, e := h.c.ArchiveDetails(id)
	if e != nil {
		t.Fatal(e)
	}
	if d.LastSeq != 1 || d.HashAtSeq != d.LastSeq {
		t.Errorf("the reclaim's receipt: last_seq %d, hash at %d", d.LastSeq, d.HashAtSeq)
	}
}

// Under the rule nothing is started: a hole smaller than the floor is left
// where it is, for the next add to use.
func TestADeleteUnderTheRuleStartsNoReclaim(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	h.c.mu.Lock()
	h.c.reclaim = reclaimRule{floor: 1 << 20, share: 4} // a megabyte, over what this leaves
	h.c.mu.Unlock()

	h.rec.reset()
	id, _, before := h.holed(t, "Kept", 20000)
	if h.reclaimRunning() {
		t.Fatal("a reclaim started under the rule")
	}
	st := h.stat(t, id)
	if st.FreeSpace < 20000 {
		t.Errorf("the hole is not there: free %d", st.FreeSpace)
	}
	if st.Size < before-20000 {
		t.Errorf("the file was rewritten after all: %d → %d", before, st.Size)
	}
}

// A reclaim is an operation like any other: cancelled, it discards the fresh
// file and leaves the archive the one it was.
func TestACancelledReclaimLeavesTheArchiveAsItWas(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	h.c.mu.Lock()
	h.c.reclaim = smallRule
	h.c.mu.Unlock()

	id, _ := h.newArchive("Cancelled")
	if _, e := h.c.OpenArchive(id); e != nil {
		t.Fatal(e)
	}
	first := h.src(t, "cancel-first.bin", string(incompressible(t, 2<<20)))
	second := h.src(t, "cancel-second.bin", string(incompressible(t, 2<<20)))
	if o := h.add(t, id, rootID, PolicySkip, first, second); o.Error != "" {
		t.Fatalf("add: %+v", o)
	}
	drop := h.row(t, id, rootID, "cancel-first.bin")
	stay := h.row(t, id, rootID, "cancel-second.bin")
	path := h.record(id).LastPath

	// Cancel inside the first chunk's progress, on the goroutine that is
	// copying: the only point at which the operation is certainly running.
	h.rec.reset()
	h.rec.onEvent(func(name string, p any) {
		o, ok := p.(OpView)
		if !ok || name != EventOpProgress || o.Kind != "reclaim" || o.Finished {
			return
		}
		h.c.CancelOp(o.ID)
	})
	if e := h.c.DeleteRecords(id, []string{drop.ID}); e != nil {
		t.Fatalf("delete: %v", e)
	}
	o := h.waitReclaim(t)
	h.rec.onEvent(nil)
	if o.Error != CodeOpCancelled {
		t.Fatalf("the cancelled reclaim ended %+v", o)
	}
	// The archive is the one the delete left — the hole still in it, so
	// nothing was rewritten — with no temporary beside it. And it is still
	// open: a reclaim is a follow-on the core started while the user was
	// browsing, so APP.md §2.3's "the archive untouched until it finishes"
	// includes not putting them out of it. (A Compact they asked for ends
	// closed; that is their own choice to leave.)
	fileAfter, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(h.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if len(e.Name()) > 0 && e.Name()[0] == '.' {
			t.Errorf("the cancelled reclaim left %s behind", e.Name())
		}
	}
	st, e := h.c.Stat(id) // not OpenArchive: the page never lost the archive
	if e != nil {
		t.Fatalf("the archive was closed by a cancelled reclaim: %v", e)
	}
	if st.Files != 1 || st.FreeSpace < 2<<20 || st.Size != uint64(fileAfter.Size()) {
		t.Errorf("after the cancel: %+v, the file is %d bytes", st, fileAfter.Size())
	}
	out := outDir(t)
	opID, e := h.c.Extract(id, []string{stay.ID}, out, ExtractSkip)
	if e != nil {
		t.Fatalf("extract: %v", e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != "" || len(o.Results) != 1 || o.Results[0].Outcome != "extracted" {
		t.Fatalf("the file that stayed, after a cancelled reclaim: %+v", o)
	}
}

// Compact is gated on Session.Live, and so is this: a commit made while the
// vault is locked leaves the space where it is, says so, and the next
// qualifying commit tries again.
func TestALockedSessionSkipsTheReclaim(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	h.c.mu.Lock()
	h.c.reclaim = smallRule
	h.c.mu.Unlock()

	id, _ := h.newArchive("Locked")
	if _, e := h.c.OpenArchive(id); e != nil {
		t.Fatal(e)
	}
	first := h.src(t, "locked-first.bin", string(incompressible(t, 40000)))
	second := h.src(t, "locked-second.bin", string(incompressible(t, 40000)))
	if o := h.add(t, id, rootID, PolicySkip, first, second); o.Error != "" {
		t.Fatalf("add: %+v", o)
	}
	drop := h.row(t, id, rootID, "locked-first.bin")

	h.c.Lock()
	h.rec.waitState(t, StateLocked)
	h.rec.reset()
	if e := h.c.DeleteRecords(id, []string{drop.ID}); e != nil {
		t.Fatalf("delete under a lock: %v", e)
	}
	if h.reclaimRunning() {
		t.Fatal("a reclaim started while the vault was locked")
	}
	if !h.logged("wait for an unlock to be reclaimed") {
		t.Error("the skipped reclaim was not logged")
	}
	st := h.stat(t, id)
	if st.FreeSpace < 40000 {
		t.Fatalf("the hole is not there: %+v", st)
	}
	// The next qualifying commit, once the session is live again, takes it.
	h.unlockWithPassword()
	h.rec.reset()
	if e := h.c.RenameRecord(id, h.row(t, id, rootID, "locked-second.bin").ID, "kept.bin"); e != nil {
		t.Fatalf("rename: %v", e)
	}
	if o := h.waitReclaim(t); o.Error != "" {
		t.Fatalf("the reclaim after the unlock: %+v", o)
	}
	if st := h.stat(t, id); st.FreeSpace != 0 {
		t.Errorf("free space after the reclaim: %+v", st)
	}
}

// Never under another operation of the same archive: a compaction started
// there would only wait on the handle, and one started under a reclaim would
// be recursive. The commit's own operation is not one of them — by the time
// the rule is measured, it is over.
func TestNoReclaimWhileAnotherOperationRuns(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()

	// The hole is made under the rule as it stands, so that nothing is
	// started before the test asks.
	h.rec.reset()
	id, stay, _ := h.holed(t, "Busy", 40000)
	if h.reclaimRunning() {
		t.Fatal("a reclaim started before the rule was lowered")
	}
	aid, _ := parseID(id)
	h.c.mu.Lock()
	h.c.reclaim = smallRule
	oa := h.c.archives[aid]
	due := h.c.reclaimDueLocked(oa, nil)
	h.c.mu.Unlock()
	if !due {
		t.Fatalf("the archive does not meet the lowered rule: free %d of %d", oa.free, oa.size)
	}

	// An operation of this archive stands: the rule is not met while it does.
	other := &op{id: randomID(), kind: "extract", archiveID: id, startedAt: h.c.now(), over: make(chan struct{}), c: h.c}
	h.c.mu.Lock()
	h.c.ops[other.id] = other
	blocked := h.c.reclaimDueLocked(oa, nil)
	h.c.mu.Unlock()
	if blocked {
		t.Error("the rule was met while another operation of the archive ran")
	}
	// And a commit made while it stands starts nothing.
	if e := h.c.RenameRecord(id, stay.ID, "renamed.bin"); e != nil {
		t.Fatalf("rename: %v", e)
	}
	if h.reclaimRunning() {
		t.Fatal("a reclaim started under a running operation")
	}
	// The operation that is committing is not one of them: with the other
	// one over, the next commit takes it.
	h.c.mu.Lock()
	other.finished = true
	h.c.mu.Unlock()
	close(other.over)
	h.rec.reset()
	if e := h.c.RenameRecord(id, stay.ID, "renamed-again.bin"); e != nil {
		t.Fatalf("rename: %v", e)
	}
	if o := h.waitReclaim(t); o.Error != "" {
		t.Fatalf("the reclaim: %+v", o)
	}
	if st := h.stat(t, id); st.FreeSpace != 0 {
		t.Errorf("free space after the reclaim: %+v", st)
	}
}

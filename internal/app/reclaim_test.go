package app

import (
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/dreamxwarden01/enfold/internal/archive"
	"github.com/dreamxwarden01/enfold/internal/format"
)

// Reclaiming space (APP.md §2.3 "Space comes back" as amended on 2026-09-10,
// FORMAT.md R40, DECISIONS 2026-09-10): the archive layer gives the tail
// back with the commit that frees it, and what is left is a hole with live
// data over it. After any commit — and when the last reader of an archive
// closes — the core plans R40's in-place compaction and, when the run would
// give the file system back the floor and at least a quarter of what it
// would move, lowers the live data into the holes a budget per commit, as a
// follow-on operation the strip calls Reclaiming space.
//
// The thresholds here are lowered through the core's own rule rather than
// met: a test that made a 64 MiB archive would prove the same thing slowly.
// The first file of most layouts here is the largest, so that one plan
// carries every file after it and the run is one commit — a plan is one
// commit deep (archive.PlanReclaim). A hole that only the first of several
// equal files fits is the other shape, where the first commit gives nothing
// back and the second gives the hole: the rule is weighed on what the whole
// run returns (RunTailReturned, RunBytesToMove), which is what
// TestADeleteOfTheFirstOfThreeEqualFilesReclaimsTheHole is about.

// smallRule is the rule these tests measure against: a few kilobytes of the
// tail back, still a quarter of what moves, and a budget no test reaches
// except the one that lowers it.
var smallRule = reclaimRule{floor: 4 << 10, share: 4, budget: 64 << 20}

// metadataSlack is what the file may hold beyond its live data once the run
// is over: the index and the free map of the follow-up commit, and the
// small holes the run's own index extents left behind under them.
const metadataSlack = 64 << 10

// holed makes an archive of a large file followed by the given files and
// deletes the large one, which leaves its extent as a hole under the rest —
// space no truncation can give back. It returns the archive's id, the rows
// of the files that stay in order, and the file's size on disk before the
// delete.
func (h *harness) holed(t *testing.T, name string, first int, rest ...int) (string, []FileRow, uint64) {
	t.Helper()
	id, path := h.newArchive(name)
	if _, e := h.c.OpenArchive(id); e != nil {
		t.Fatalf("open %s: %v", name, e)
	}
	srcs := []string{h.src(t, name+"-first.bin", string(incompressible(t, first)))}
	for i, n := range rest {
		srcs = append(srcs, h.src(t, name+"-"+string(rune('a'+i))+".bin", string(incompressible(t, n))))
	}
	if o := h.add(t, id, rootID, PolicySkip, srcs...); o.Error != "" {
		t.Fatalf("add: %+v", o)
	}
	var stay []FileRow
	for i := range rest {
		stay = append(stay, h.row(t, id, rootID, name+"-"+string(rune('a'+i))+".bin"))
	}
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

// lowerRule sets the core's rule for the test.
func (h *harness) lowerRule(r reclaimRule) {
	h.c.mu.Lock()
	h.c.reclaim = r
	h.c.mu.Unlock()
}

// waitReclaim waits for the follow-on run to finish.
func (h *harness) waitReclaim(t *testing.T) OpView {
	t.Helper()
	p := h.rec.waitFor(t, EventOpDone, func(p any) bool {
		o, ok := p.(OpView)
		return ok && o.Kind == "reclaim"
	})
	return p.(OpView)
}

// runningReclaim is the id of the reclaim on the books, or "".
func (h *harness) runningReclaim() string {
	h.c.mu.Lock()
	defer h.c.mu.Unlock()
	for _, o := range h.c.ops {
		if o.kind == "reclaim" && !o.finished {
			return o.id
		}
	}
	return ""
}

// reclaimRunning reports whether a reclaim of any archive is on the books.
func (h *harness) reclaimRunning() bool { return h.runningReclaim() != "" }

// open is the core's handle on an open archive, for what the page cannot
// see: the file's own sequence and its layout.
func (h *harness) open(t *testing.T, id string) *openArchive {
	t.Helper()
	aid, _ := parseID(id)
	h.c.mu.Lock()
	defer h.c.mu.Unlock()
	oa := h.c.archives[aid]
	if oa == nil {
		t.Fatal("the archive is not open")
	}
	return oa
}

// liveBytes is the stored size of every live file: what the file must hold.
func (h *harness) liveBytes(t *testing.T, id string) uint64 {
	t.Helper()
	var n uint64
	for _, f := range h.open(t, id).a.Files() {
		n += f.StoredSize
	}
	return n
}

// extractsAll reads every file that stays back through the archive: a move
// is verbatim, and the reader verifies every tag.
func (h *harness) extractsAll(t *testing.T, id string, rows []FileRow) {
	t.Helper()
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	opID, e := h.c.Extract(id, ids, outDir(t), ExtractSkip, nil)
	if e != nil {
		t.Fatalf("extract: %v", e)
	}
	o := h.rec.waitOp(t, opID)
	if o.Error != "" || len(o.Results) != len(rows) {
		t.Fatalf("extract after the reclaim: %+v", o)
	}
	for _, r := range o.Results {
		if r.Outcome != "extracted" {
			t.Fatalf("%s after the reclaim: %+v", r.Name, r)
		}
	}
}

// readsBack reads every row through the archive's own Reader, which verifies
// every tag: a move is verbatim, so a file the run has lowered reads as it
// always did. It is not one of the core's operations, and that is the point
// — an operation's end is a trigger of its own (ops.go finishOp), so a test
// that must look at the archive between two commits looks at it this way
// rather than by extracting and racing the run its extraction started.
func (h *harness) readsBack(t *testing.T, id string, rows []FileRow) {
	t.Helper()
	oa := h.open(t, id)
	for _, row := range rows {
		fid, ok := parseID(row.ID)
		if !ok {
			t.Fatalf("%s: %q is not an id", row.Name, row.ID)
		}
		rd, err := oa.a.OpenReader(fid)
		if err != nil {
			t.Fatalf("%s: open: %v", row.Name, err)
		}
		n, err := io.Copy(io.Discard, rd)
		rd.Close()
		if err != nil {
			t.Fatalf("%s: read: %v", row.Name, err)
		}
		if uint64(n) != row.Size {
			t.Errorf("%s: read %d bytes of %d", row.Name, n, row.Size)
		}
	}
}

// receiptCurrent checks that the registry holds the file's own sequence:
// the receipt every commit owes was written.
func (h *harness) receiptCurrent(t *testing.T, id string, when string) {
	t.Helper()
	oa := h.open(t, id)
	d, e := h.c.ArchiveDetails(id)
	if e != nil {
		t.Fatalf("%s: details: %v", when, e)
	}
	if seq := oa.a.Seq(); d.LastSeq != seq {
		t.Errorf("%s: the registry's last_seq is %d, the file is at %d", when, d.LastSeq, seq)
	}
}

// A delete that leaves a hole over the rule is followed by the run, and the
// file ends at about what it holds, every file that stayed readable, the
// receipt of every commit in the registry, and the operation saying what
// came back.
func TestADeleteOfTheFirstFileReclaimsTheTail(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	h.lowerRule(smallRule)

	h.rec.reset()
	id, stay, before := h.holed(t, "Reclaimed", 1<<20, 300<<10, 300<<10)
	o := h.waitReclaim(t)
	if o.Error != "" {
		t.Fatalf("the reclaim failed: %+v", o)
	}
	st := h.stat(t, id)
	live := h.liveBytes(t, id)
	if st.Size > live+metadataSlack {
		t.Errorf("the file stood at %d for %d live bytes, from %d: the hole was not reclaimed", st.Size, live, before)
	}
	if st.FreeSpace > metadataSlack {
		t.Errorf("free space after the reclaim: %d", st.FreeSpace)
	}
	if st.Files != 2 {
		t.Fatalf("files after the reclaim: %d", st.Files)
	}
	// What came back is the file's own shrinking, said apart from what
	// moved: the hole was a megabyte and the moves 600 KiB.
	if o.Returned != before-st.Size || o.Returned < 1<<20-metadataSlack {
		t.Errorf("the reclaim reported %d bytes back; the file went %d → %d", o.Returned, before, st.Size)
	}
	if o.Total < 600<<10 || o.Done != o.Total {
		t.Errorf("the reclaim's progress ended at %d of %d; the moves were 600 KiB", o.Done, o.Total)
	}
	h.extractsAll(t, id, stay)
	if st.ReceiptOwed {
		t.Error("the receipt was owed under a live session")
	}
	h.receiptCurrent(t, id, "after the run")
}

// The layout a plan one commit deep cannot answer for: three files of one
// size, the first deleted. The hole holds exactly one of the two that stay,
// so the first commit moves that one and gives nothing back — the other
// still anchors the tail — and only the commit after it returns the hole.
// The rule is measured on the run, so the run is made, and the file ends at
// what it holds.
func TestADeleteOfTheFirstOfThreeEqualFilesReclaimsTheHole(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()

	// Under the rule as it stands (64 MiB) nothing starts, so the plan can be
	// read where the core would read it.
	h.rec.reset()
	id, stay, before := h.holed(t, "Equal", 1<<20, 1<<20, 1<<20)
	if h.reclaimRunning() {
		t.Fatal("a reclaim started under the standing rule")
	}
	plan := h.open(t, id).a.PlanReclaim(smallRule.budget)
	if len(plan.Moves) != 1 {
		t.Fatalf("the plan is not one move of one of two equal files behind a hole of their size: %+v", plan.Moves)
	}
	// The gap this test is about: on its first commit alone the run would
	// have been refused, and the archive would have stayed at the size of
	// what it no longer holds.
	if plan.TailReturned >= smallRule.floor && plan.TailReturned*smallRule.share >= plan.BytesToMove {
		t.Fatalf("this layout no longer tests the gap: the first commit alone earns the run, %d back for %d moved", plan.TailReturned, plan.BytesToMove)
	}
	if !smallRule.worth(plan) {
		t.Fatalf("the run is not worth making: %d back for %d moved over %d commits", plan.RunTailReturned, plan.RunBytesToMove, plan.Commits)
	}
	t.Logf("the first commit promises %d back for %d moved; the run %d for %d over %d commits",
		plan.TailReturned, plan.BytesToMove, plan.RunTailReturned, plan.RunBytesToMove, plan.Commits)

	// The rule lowered, the next commit weighs the run and starts it.
	h.lowerRule(smallRule)
	h.rec.reset()
	if e := h.c.RenameRecord(id, stay[0].ID, "renamed.bin"); e != nil {
		t.Fatalf("rename: %v", e)
	}
	o := h.waitReclaim(t)
	if o.Error != "" {
		t.Fatalf("the reclaim: %+v", o)
	}
	st := h.stat(t, id)
	live := h.liveBytes(t, id)
	if st.Size > live+metadataSlack {
		t.Errorf("the file stood at %d for %d live bytes, from %d: the hole was not reclaimed", st.Size, live, before)
	}
	if o.Returned < 1<<20-metadataSlack || o.Returned != before-st.Size {
		t.Errorf("the reclaim reported %d bytes back; the file went %d → %d and the hole was a megabyte", o.Returned, before, st.Size)
	}
	// The progress total is the run's, not its first commit's: both files.
	if o.Total < 2<<20-metadataSlack || o.Done != o.Total {
		t.Errorf("the reclaim's progress ended at %d of %d; the run moves both files", o.Done, o.Total)
	}
	if st.Files != 2 {
		t.Fatalf("files after the reclaim: %d", st.Files)
	}
	h.extractsAll(t, id, stay)
	h.receiptCurrent(t, id, "after the run")
}

// Every commit of the run has its receipt: the registry is brought to the
// file's sequence after each one, not at the end alone.
func TestEachReclaimCommitHasItsReceipt(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	// A budget of two of the files: the eight behind the hole take four
	// commits. The delete's own follow-up commit published the hole — the
	// map the add appended was the tail, and freeing it gave the tail back
	// — so no empty commit opens the run; a replace is where one does
	// (TestAReplaceLeavesAQuarantinedHoleAndTheRunPublishesFirst).
	h.lowerRule(reclaimRule{floor: 4 << 10, share: 4, budget: 256 << 10})

	var mu sync.Mutex
	var commits int
	var wrong []string
	h.rec.reset()
	h.rec.onEvent(func(name string, p any) {
		if name != EventArchiveChanged || !h.reclaimRunning() {
			return
		}
		ev := p.(ArchiveChanged)
		d, e := h.c.ArchiveDetails(ev.ID)
		oa := h.open(t, ev.ID)
		mu.Lock()
		defer mu.Unlock()
		commits++
		if e != nil || d.LastSeq != oa.a.Seq() {
			wrong = append(wrong, ev.ID)
		}
	})
	sizes := make([]int, 8)
	for i := range sizes {
		sizes[i] = 100 << 10
	}
	id, stay, _ := h.holed(t, "Receipts", 1<<20, sizes...)
	if o := h.waitReclaim(t); o.Error != "" {
		t.Fatalf("the reclaim failed: %+v", o)
	}
	h.rec.onEvent(nil)
	mu.Lock()
	defer mu.Unlock()
	if commits != 4 {
		t.Errorf("the run made %d commits; eight files two per commit is 4", commits)
	}
	if len(wrong) != 0 {
		t.Errorf("%d commits ended without their receipt in the registry", len(wrong))
	}
	st := h.stat(t, id)
	if live := h.liveBytes(t, id); st.Size > live+metadataSlack {
		t.Errorf("the file stood at %d for %d live bytes", st.Size, live)
	}
	h.extractsAll(t, id, stay)
}

// A replace frees the old extent and appends the new, so the tail stays
// live and the hole is still under R31's quarantine when the run is
// planned: its first commit is the empty one that publishes it, and the
// moves follow.
func TestAReplaceLeavesAQuarantinedHoleAndTheRunPublishesFirst(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	h.lowerRule(smallRule)
	id := h.openArchive(t, "Replaced")
	first := h.src(t, "replaced.bin", string(incompressible(t, 1<<20)))
	mid := h.src(t, "replaced-mid.bin", string(incompressible(t, 300<<10)))
	last := h.src(t, "replaced-last.bin", string(incompressible(t, 300<<10)))
	if o := h.add(t, id, rootID, PolicySkip, first, mid, last); o.Error != "" {
		t.Fatalf("add: %+v", o)
	}
	before := h.stat(t, id).Size

	var mu sync.Mutex
	var sizes []uint64
	h.rec.reset()
	h.rec.onEvent(func(name string, p any) {
		if name != EventArchiveChanged || !h.reclaimRunning() {
			return
		}
		oa := h.open(t, p.(ArchiveChanged).ID)
		h.c.mu.Lock()
		size := oa.size
		h.c.mu.Unlock()
		mu.Lock()
		sizes = append(sizes, size)
		mu.Unlock()
	})
	// The same name, smaller: the old megabyte is freed under the two files
	// that follow it and the new copy lands at the tail.
	if err := os.WriteFile(first, incompressible(t, 50<<10)[:50<<10], 0o600); err != nil {
		t.Fatal(err)
	}
	if o := h.add(t, id, rootID, PolicyReplace, first); o.Error != "" {
		t.Fatalf("replace: %+v", o)
	}
	if o := h.waitReclaim(t); o.Error != "" {
		t.Fatalf("the reclaim after the replace: %+v", o)
	}
	h.rec.onEvent(nil)
	mu.Lock()
	defer mu.Unlock()
	// Two commits: the empty one, which gives nothing back — the tail is
	// live — and the one that moves every file down and cuts the tail.
	if len(sizes) != 2 || sizes[0] < before || sizes[1] >= sizes[0] {
		t.Errorf("the run's commits left the file at %v, from %d; expected an empty commit and then the moves", sizes, before)
	}
	st := h.stat(t, id)
	if live := h.liveBytes(t, id); st.Size > live+metadataSlack || st.Files != 3 {
		t.Errorf("the file stood at %d for %d live bytes: %+v", st.Size, live, st)
	}
	rows := []FileRow{h.row(t, id, rootID, "replaced.bin"), h.row(t, id, rootID, "replaced-mid.bin"), h.row(t, id, rootID, "replaced-last.bin")}
	h.extractsAll(t, id, rows)
	h.receiptCurrent(t, id, "after the run")
}

// Under the floor nothing is started: a hole that would give back less than
// the floor is left where it is, for the next add to use.
func TestADeleteUnderTheFloorStartsNoReclaim(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	h.lowerRule(reclaimRule{floor: 1 << 20, share: 4, budget: 64 << 20}) // a megabyte, over what this leaves

	h.rec.reset()
	id, _, before := h.holed(t, "Kept", 200<<10, 100<<10)
	if h.reclaimRunning() {
		t.Fatal("a reclaim started under the floor")
	}
	st := h.stat(t, id)
	if st.FreeSpace < 200<<10 {
		t.Errorf("the hole is not there: free %d", st.FreeSpace)
	}
	if st.Size < before-metadataSlack {
		t.Errorf("the file was compacted after all: %d → %d", before, st.Size)
	}
}

// The quarter: a run that would give back over the floor but under a quarter
// of what it must move is refused, and it is the run that is weighed, not its
// first commit. One hole under eight files of its own size: every commit
// lowers one file by one hole, and the tail comes back once — the last file's
// — when the eighth has moved. A whole file's worth of tail for eight files
// moved is not worth making; that is what Compact is for, on request.
func TestTheQuarterRuleRefusesASmallGainForALargeMove(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	h.lowerRule(smallRule)
	const size = 64 << 10
	behind := make([]int, 8)
	for i := range behind {
		behind[i] = size
	}
	h.rec.reset()
	id, stay, before := h.holed(t, "Quarter", size, behind...)
	// The plan is what the rule measured: over the floor, under the quarter,
	// at run level — a single commit's figures would refuse it for the floor
	// instead, which is a different rule and a different test.
	plan := h.open(t, id).a.PlanReclaim(smallRule.budget)
	if plan.RunTailReturned < smallRule.floor || plan.RunTailReturned*smallRule.share >= plan.RunBytesToMove {
		t.Fatalf("the plan does not test the quarter: %d back for %d moved over %d commits", plan.RunTailReturned, plan.RunBytesToMove, plan.Commits)
	}
	if h.reclaimRunning() {
		t.Fatal("a reclaim started for a gain under a quarter of the move")
	}
	st := h.stat(t, id)
	if st.FreeSpace < size {
		t.Errorf("the hole is not there: free %d", st.FreeSpace)
	}
	if st.Size < before-metadataSlack {
		t.Errorf("the file was compacted after all: %d → %d", before, st.Size)
	}
	// Nor does the next commit start one: the layout is the same.
	if e := h.c.RenameRecord(id, stay[0].ID, "renamed.bin"); e != nil {
		t.Fatalf("rename: %v", e)
	}
	if h.reclaimRunning() {
		t.Fatal("a later commit started the refused reclaim")
	}
}

// A reader holding an extent leaves it where it lies: with the tail file
// held nothing would come back, so nothing starts; the reader's end is where
// the plan is made again, and the run follows it.
func TestAReaderHoldingASourceDelaysTheReclaimUntilItCloses(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	h.lowerRule(smallRule)
	id := h.openArchive(t, "Held")
	first := h.src(t, "held-first.bin", string(incompressible(t, 1<<20)))
	mid := h.src(t, "held-mid.bin", string(incompressible(t, 300<<10)))
	last := h.src(t, "held-last.bin", string(incompressible(t, 300<<10)))
	if o := h.add(t, id, rootID, PolicySkip, first, mid, last); o.Error != "" {
		t.Fatalf("add: %+v", o)
	}
	stay := []FileRow{h.row(t, id, rootID, "held-mid.bin"), h.row(t, id, rootID, "held-last.bin")}
	drop := h.row(t, id, rootID, "held-first.bin")

	// A body in flight over the preview transport: the archive's Reader on
	// the tail file, counted as preview.go counts it.
	oa := h.open(t, id)
	lastID, _ := parseID(stay[1].ID)
	rd, err := oa.a.OpenReader(lastID)
	if err != nil {
		t.Fatal(err)
	}
	h.c.mu.Lock()
	oa.readers++
	h.c.mu.Unlock()

	h.rec.reset()
	if e := h.c.DeleteRecords(id, []string{drop.ID}); e != nil {
		t.Fatalf("delete: %v", e)
	}
	if h.reclaimRunning() {
		t.Fatal("a reclaim started while a reader held the tail")
	}
	if p := oa.a.PlanReclaim(smallRule.budget); p.RunTailReturned >= smallRule.floor {
		t.Fatalf("with the tail held the run promises %d bytes back", p.RunTailReturned)
	}
	// The body ends as the transport ends it: the Reader closed, then the
	// count let go — and the run starts there.
	rd.Close()
	h.c.releaseReader(oa)
	if o := h.waitReclaim(t); o.Error != "" {
		t.Fatalf("the reclaim after the reader closed: %+v", o)
	}
	st := h.stat(t, id)
	if live := h.liveBytes(t, id); st.Size > live+metadataSlack {
		t.Errorf("the file stood at %d for %d live bytes after the reader closed", st.Size, live)
	}
	h.extractsAll(t, id, stay)
}

// cancelReclaimAfterCommits cancels the running reclaim from inside the event
// its n-th commit announces: the only point at which the run is certainly
// between two commits.
func (h *harness) cancelReclaimAfterCommits(n int, cancel func(opID string)) {
	var mu sync.Mutex
	var commits int
	h.rec.onEvent(func(name string, p any) {
		if name != EventArchiveChanged {
			return
		}
		opID := h.runningReclaim()
		if opID == "" {
			return
		}
		mu.Lock()
		commits++
		hit := commits == n
		mu.Unlock()
		if hit {
			cancel(opID)
		}
	})
}

// A cancel between two commits leaves the archive consistent — the files
// moved so far where they went, the rest where they were, every one
// readable — and the next commit takes the run up again to the end.
func TestACancelledReclaimIsResumedByTheNextCommit(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	// One file per commit, so that a cancel lands with some moved and some
	// not.
	h.lowerRule(reclaimRule{floor: 4 << 10, share: 4, budget: 512 << 10})

	// Cancel once the second move has landed: one file is still where it
	// was.
	h.rec.reset()
	h.cancelReclaimAfterCommits(2, func(opID string) {
		if e := h.c.CancelOp(opID); e != nil {
			t.Errorf("cancel: %v", e)
		}
	})
	id, stay, _ := h.holed(t, "Cancelled", 2<<20, 512<<10, 512<<10, 512<<10)
	o := h.waitReclaim(t)
	h.rec.onEvent(nil)
	if o.Error != CodeOpCancelled {
		t.Fatalf("the cancelled reclaim ended %+v", o)
	}
	// The archive is still open — a reclaim is a follow-on the core started
	// while the user was browsing — consistent, and less compacted than the
	// run would have left it.
	st, e := h.c.Stat(id)
	if e != nil {
		t.Fatalf("the archive was closed by a cancelled reclaim: %v", e)
	}
	live := h.liveBytes(t, id)
	if st.Files != 3 || st.Size <= live+metadataSlack {
		t.Errorf("after the cancel: %+v for %d live bytes; the run should have been cut short", st, live)
	}
	// Read where the run left them, not extracted: an extraction's end would
	// be a trigger of its own, and the commit below is what this is about.
	h.readsBack(t, id, stay)
	h.receiptCurrent(t, id, "after the cancel")

	// The next commit plans again and finishes the run.
	h.rec.reset()
	if e := h.c.RenameRecord(id, stay[0].ID, "renamed.bin"); e != nil {
		t.Fatalf("rename: %v", e)
	}
	if o := h.waitReclaim(t); o.Error != "" {
		t.Fatalf("the reclaim after the rename: %+v", o)
	}
	st = h.stat(t, id)
	if st.Size > live+metadataSlack {
		t.Errorf("the file stood at %d for %d live bytes after the resumed run", st.Size, live)
	}
	h.extractsAll(t, id, stay)
}

// Close archive is the kill switch (APP.md §2.3): it cancels the reclaim
// like any other operation and never waits behind it, the archive closes
// consistent, and an Open finds every file where the run left it.
func TestCloseArchiveCancelsAReclaim(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	h.lowerRule(reclaimRule{floor: 4 << 10, share: 4, budget: 512 << 10})

	// The hook runs on the reclaim's own goroutine, which holds the
	// handle's turn that Close waits for: the close is asked for from
	// here and made from the test's. The hook then waits until the kill
	// switch has marked the handle closing — which it does under the state
	// mutex, in the same section as the cancel — so that the run finds its
	// context cancelled before a third move begins.
	closeNow := make(chan struct{}, 1)
	h.rec.reset()
	h.cancelReclaimAfterCommits(2, func(opID string) {
		closeNow <- struct{}{}
		for i := 0; i < 5000 && !h.closing(h.opArchive(opID)); i++ {
			time.Sleep(time.Millisecond)
		}
	})
	id, stay, _ := h.holed(t, "Closed", 2<<20, 512<<10, 512<<10, 512<<10)
	<-closeNow
	if e := h.c.CloseArchive(id); e != nil {
		t.Fatalf("close during a reclaim: %v", e)
	}
	o := h.waitReclaim(t)
	h.rec.onEvent(nil)
	if o.Error != CodeOpCancelled {
		t.Fatalf("the reclaim under a close ended %+v", o)
	}
	if h.isHeld(id) {
		t.Fatal("the archive stayed open after the close")
	}
	if _, e := h.c.OpenArchive(id); e != nil {
		t.Fatalf("reopen: %v", e)
	}
	if st := h.stat(t, id); st.Files != 3 {
		t.Fatalf("after the reopen: %+v", st)
	}
	// Read rather than extracted, for the reason readsBack gives: the run is
	// taken up by the commit below and not by a reading of the files.
	h.readsBack(t, id, stay)
	// And the run goes on from there at the next commit.
	h.rec.reset()
	if e := h.c.RenameRecord(id, stay[0].ID, "renamed.bin"); e != nil {
		t.Fatalf("rename: %v", e)
	}
	if o := h.waitReclaim(t); o.Error != "" {
		t.Fatalf("the reclaim after the reopen: %+v", o)
	}
	if st, live := h.stat(t, id), h.liveBytes(t, id); st.Size > live+metadataSlack {
		t.Errorf("the file stood at %d for %d live bytes after the resumed run", st.Size, live)
	}
}

// opArchive is the archive an operation runs on.
func (h *harness) opArchive(opID string) string {
	h.c.mu.Lock()
	defer h.c.mu.Unlock()
	if o := h.c.ops[opID]; o != nil {
		return o.archiveID
	}
	return ""
}

// closing reports whether the kill switch has the handle: marked closing,
// its operations cancelled, waiting for the handle's turn.
func (h *harness) closing(id string) bool {
	aid, _ := parseID(id)
	h.c.mu.Lock()
	defer h.c.mu.Unlock()
	oa := h.c.archives[aid]
	return oa == nil || oa.state == "closing"
}

// A move is a commit like any other and asks nothing of the session: the
// archive key is in memory, so a commit made while the vault is locked is
// followed by the run all the same, and what the run's commits owe the
// registry waits for the unlock, as every receipt does (APP.md §2.3).
func TestAReclaimUnderALockOwesItsReceipt(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	h.lowerRule(smallRule)
	id := h.openArchive(t, "Locked")
	first := h.src(t, "locked-first.bin", string(incompressible(t, 1<<20)))
	second := h.src(t, "locked-second.bin", string(incompressible(t, 300<<10)))
	if o := h.add(t, id, rootID, PolicySkip, first, second); o.Error != "" {
		t.Fatalf("add: %+v", o)
	}
	drop := h.row(t, id, rootID, "locked-first.bin")
	stay := []FileRow{h.row(t, id, rootID, "locked-second.bin")}

	h.c.Lock()
	h.rec.waitState(t, StateLocked)
	h.rec.reset()
	if e := h.c.DeleteRecords(id, []string{drop.ID}); e != nil {
		t.Fatalf("delete under a lock: %v", e)
	}
	if o := h.waitReclaim(t); o.Error != "" {
		t.Fatalf("the reclaim under a lock: %+v", o)
	}
	st := h.stat(t, id)
	if live := h.liveBytes(t, id); st.Size > live+metadataSlack {
		t.Errorf("the file stood at %d for %d live bytes under a lock", st.Size, live)
	}
	if !st.ReceiptOwed {
		t.Error("the run's receipt was not owed under a lock")
	}
	// The unlock pays what is owed: the registry is at the file's sequence.
	h.unlockWithPassword()
	if st := h.stat(t, id); st.ReceiptOwed {
		t.Error("the receipt is still owed after the unlock")
	}
	h.receiptCurrent(t, id, "after the unlock")
	h.extractsAll(t, id, stay)
}

// Never under another operation of the same archive: a run started there
// would only wait on the handle, and one started under a reclaim would be
// recursive. The commit's own operation is not one of them — by the time
// the rule is measured, it is over.
func TestNoReclaimWhileAnotherOperationRuns(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()

	// The hole is made under the rule as it stands, so that nothing is
	// started before the test asks.
	h.rec.reset()
	id, stay, _ := h.holed(t, "Busy", 1<<20, 300<<10)
	if h.reclaimRunning() {
		t.Fatal("a reclaim started before the rule was lowered")
	}
	h.lowerRule(smallRule)
	oa := h.open(t, id)
	h.c.mu.Lock()
	due := h.c.reclaimDueLocked(oa, nil)
	h.c.mu.Unlock()
	if !due {
		t.Fatal("the archive is not free for a run")
	}

	// An operation of this archive stands: nothing is planned while it does.
	other := &op{id: randomID(), kind: "extract", archiveID: id, startedAt: h.c.now(), over: make(chan struct{}), c: h.c}
	h.c.mu.Lock()
	h.c.ops[other.id] = other
	blocked := h.c.reclaimDueLocked(oa, nil)
	h.c.mu.Unlock()
	if blocked {
		t.Error("a run was due while another operation of the archive ran")
	}
	// And a commit made while it stands starts nothing.
	if e := h.c.RenameRecord(id, stay[0].ID, "renamed.bin"); e != nil {
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
	if e := h.c.RenameRecord(id, stay[0].ID, "renamed-again.bin"); e != nil {
		t.Fatalf("rename: %v", e)
	}
	if o := h.waitReclaim(t); o.Error != "" {
		t.Fatalf("the reclaim: %+v", o)
	}
	if st, live := h.stat(t, id), h.liveBytes(t, id); st.Size > live+metadataSlack {
		t.Errorf("the file stood at %d for %d live bytes", st.Size, live)
	}
}

// The rule's own arithmetic, apart from any archive. What it weighs is the
// run: a first commit that promises nothing is no answer either way, and the
// last case is the layout that started this — [hole S][A: S][B: S], where the
// commit the plan answers for moves A for no gain at all.
func TestReclaimRuleArithmetic(t *testing.T) {
	r := reclaimRule{floor: 64 << 20, share: 4, budget: 64 << 20}
	cases := []struct {
		plan  archive.ReclaimPlan
		worth bool
	}{
		{archive.ReclaimPlan{RunTailReturned: 64 << 20, RunBytesToMove: 256 << 20}, true},
		{archive.ReclaimPlan{RunTailReturned: 64<<20 - 1, RunBytesToMove: 1}, false},
		{archive.ReclaimPlan{RunTailReturned: 64 << 20, RunBytesToMove: 256<<20 + 1}, false},
		{archive.ReclaimPlan{RunTailReturned: 100 << 20}, true}, // a free tail, nothing to move
		{archive.ReclaimPlan{}, false},
		// The one-commit figures are not what is weighed, either way.
		{archive.ReclaimPlan{TailReturned: 100 << 20, BytesToMove: 1}, false},
		{archive.ReclaimPlan{TailReturned: 0, BytesToMove: 64 << 20, RunTailReturned: 64 << 20, RunBytesToMove: 128 << 20}, true},
	}
	for _, c := range cases {
		if got := r.worth(c.plan); got != c.worth {
			t.Errorf("worth(%+v) = %v", c.plan, got)
		}
	}
	// The batch is the leading moves within the budget, and never fewer
	// than one: a file over the budget moves on its own.
	moves := []archive.Move{{From: format.Extent{Len: 30 << 20}}, {From: format.Extent{Len: 30 << 20}}, {From: format.Extent{Len: 30 << 20}}}
	if got := r.batch(moves); len(got) != 2 {
		t.Errorf("a budget of 64 MiB took %d moves of 30 MiB", len(got))
	}
	big := []archive.Move{{From: format.Extent{Len: 100 << 20}}, {From: format.Extent{Len: 1}}}
	if got := r.batch(big); len(got) != 1 {
		t.Errorf("a file over the budget: %d moves", len(got))
	}
	if got := r.batch(nil); len(got) != 0 {
		t.Errorf("no moves: %d", len(got))
	}
}

// The rule is a guard over the run and not only the gate before it (APP.md
// §2.3, the outside review of 2026-09-10, finding 1). An estimate answers
// for the layout it walked, and the layout can change under the run: here a
// preview opens on the tail file between two commits, and an extent a reader
// holds is left where it lies with no truncation passing it, so every plan
// after that has moves and none of them can give the file system anything
// back. The run stops where it is rather than moving bytes for a tail that
// will not come — not a failure: every commit it made is committed, and the
// reader's own end takes the run up again.
func TestAReclaimStopsWhenItsMovesOutrunWhatComesBack(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	// One file per commit, so that the guard is weighed between two of them.
	h.lowerRule(reclaimRule{floor: 4 << 10, share: 4, budget: 512 << 10})
	id := h.openArchive(t, "Outrun")
	first := h.src(t, "outrun-first.bin", string(incompressible(t, 2<<20)))
	one := h.src(t, "outrun-a.bin", string(incompressible(t, 512<<10)))
	two := h.src(t, "outrun-b.bin", string(incompressible(t, 512<<10)))
	three := h.src(t, "outrun-c.bin", string(incompressible(t, 512<<10)))
	if o := h.add(t, id, rootID, PolicySkip, first, one, two, three); o.Error != "" {
		t.Fatalf("add: %+v", o)
	}
	stay := []FileRow{
		h.row(t, id, rootID, "outrun-a.bin"),
		h.row(t, id, rootID, "outrun-b.bin"),
		h.row(t, id, rootID, "outrun-c.bin"),
	}
	drop := h.row(t, id, rootID, "outrun-first.bin")
	oa := h.open(t, id)
	tailID, _ := parseID(stay[2].ID)

	// The preview opens on the tail file the moment the run's first commit
	// lands, counted as preview.go counts it: from there nothing the run can
	// move gives the file system a byte back.
	var mu sync.Mutex
	var rd *archive.Reader
	h.rec.reset()
	h.rec.onEvent(func(name string, p any) {
		if name != EventArchiveChanged || !h.reclaimRunning() {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if rd != nil {
			return
		}
		r, err := oa.a.OpenReader(tailID)
		if err != nil {
			t.Errorf("the preview on the tail file: %v", err)
			return
		}
		h.c.mu.Lock()
		oa.readers++
		h.c.mu.Unlock()
		rd = r
	})
	if e := h.c.DeleteRecords(id, []string{drop.ID}); e != nil {
		t.Fatalf("delete: %v", e)
	}
	o := h.waitReclaim(t)
	h.rec.onEvent(nil)
	if o.Error != "" {
		t.Fatalf("the run the guard stopped ended in an error: %+v", o)
	}
	if !h.logged("the reclaim stopped after") {
		t.Error("the run stopped without a word in the log")
	}
	// It stopped where the guard weighed it: after the one commit the budget
	// allowed, and not after moving every file the plan still offered.
	if o.Done > 512<<10+metadataSlack {
		t.Errorf("the run moved %d bytes of %d before it stopped; the guard is weighed before the second commit", o.Done, o.Total)
	}
	st := h.stat(t, id)
	live := h.liveBytes(t, id)
	if st.Files != 3 || st.Size <= live+metadataSlack {
		t.Errorf("after the guard: %+v for %d live bytes; the run should have been cut short", st, live)
	}
	h.readsBack(t, id, stay)
	h.receiptCurrent(t, id, "after the guard")

	// The preview ends, and the run it stopped is taken up again — now the
	// tail can come back, and the plan promises it.
	mu.Lock()
	r := rd
	mu.Unlock()
	if r == nil {
		t.Fatal("the preview never opened: the run was not the one this test is about")
	}
	r.Close()
	h.rec.reset()
	h.c.releaseReader(oa)
	if o := h.waitReclaim(t); o.Error != "" {
		t.Fatalf("the reclaim after the preview ended: %+v", o)
	}
	if st, live := h.stat(t, id), h.liveBytes(t, id); st.Size > live+metadataSlack {
		t.Errorf("the file stood at %d for %d live bytes after the run went on", st.Size, live)
	}
	h.extractsAll(t, id, stay)
}

// The trigger that was lost (the outside review of 2026-09-10, finding 2):
// a delete whose space a preview holds, an extraction begun while the
// preview is still there, and the preview closing under it. releaseReader
// finds the extraction registered and is refused there and then; the
// extraction releases its own readers with a count of its own and plans
// nothing; and the run waited for the next edit that never came. Every
// operation's end is a trigger now (ops.go finishOp), so it starts when the
// extraction ends.
func TestTheRunStartsWhenTheOperationARunningPreviewClosedUnderEnds(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	h.lowerRule(smallRule)
	id := h.openArchive(t, "LastReader")
	first := h.src(t, "reader-first.bin", string(incompressible(t, 1<<20)))
	mid := h.src(t, "reader-mid.bin", string(incompressible(t, 300<<10)))
	last := h.src(t, "reader-last.bin", string(incompressible(t, 300<<10)))
	if o := h.add(t, id, rootID, PolicySkip, first, mid, last); o.Error != "" {
		t.Fatalf("add: %+v", o)
	}
	stay := []FileRow{h.row(t, id, rootID, "reader-mid.bin"), h.row(t, id, rootID, "reader-last.bin")}
	drop := h.row(t, id, rootID, "reader-first.bin")

	// The preview: the archive's Reader on the tail file, counted as
	// preview.go counts it. With the tail held nothing would come back.
	oa := h.open(t, id)
	lastID, _ := parseID(stay[1].ID)
	rd, err := oa.a.OpenReader(lastID)
	if err != nil {
		t.Fatal(err)
	}
	h.c.mu.Lock()
	oa.readers++
	h.c.mu.Unlock()

	h.rec.reset()
	if e := h.c.DeleteRecords(id, []string{drop.ID}); e != nil {
		t.Fatalf("delete: %v", e)
	}
	if h.reclaimRunning() {
		t.Fatal("a reclaim started while a preview held the tail")
	}

	// The preview ends inside the extraction, at its first word of progress:
	// either while the extraction's own reader is counted beside it, or
	// between two of its files — both are a release that plans nothing.
	var once sync.Once
	h.rec.onEvent(func(name string, p any) {
		if name != EventOpProgress {
			return
		}
		if v, ok := p.(OpView); !ok || v.Kind != "extract" {
			return
		}
		once.Do(func() {
			rd.Close()
			h.c.releaseReader(oa)
		})
	})
	ids := []string{stay[0].ID, stay[1].ID}
	opID, e := h.c.Extract(id, ids, outDir(t), ExtractSkip, nil)
	if e != nil {
		t.Fatalf("extract: %v", e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != "" {
		t.Fatalf("the extraction: %+v", o)
	}
	h.rec.onEvent(nil)
	never := false
	once.Do(func() { never = true })
	if never {
		t.Fatal("the extraction said nothing of its progress: the preview never closed under it")
	}

	// The extraction's end is the trigger.
	if o := h.waitReclaim(t); o.Error != "" {
		t.Fatalf("the reclaim after the extraction ended: %+v", o)
	}
	st := h.stat(t, id)
	if live := h.liveBytes(t, id); st.Size > live+metadataSlack {
		t.Errorf("the file stood at %d for %d live bytes after the extraction ended", st.Size, live)
	}
	h.readsBack(t, id, stay)
	h.receiptCurrent(t, id, "after the run")
}

// A reclaim's own end is not a trigger (APP.md §2.3): a run the user
// cancelled is taken up by the next qualifying commit and never by itself,
// or the cancel would be a pause of one instant. The layout is worth a run
// the whole time it is left alone, which is what the commit at the end
// proves.
func TestACancelledReclaimDoesNotStartItselfAgain(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	h.lowerRule(reclaimRule{floor: 4 << 10, share: 4, budget: 512 << 10})

	h.rec.reset()
	h.cancelReclaimAfterCommits(1, func(opID string) {
		if e := h.c.CancelOp(opID); e != nil {
			t.Errorf("cancel: %v", e)
		}
	})
	id, stay, _ := h.holed(t, "Stopped", 2<<20, 512<<10, 512<<10, 512<<10)
	o := h.waitReclaim(t)
	h.rec.onEvent(nil)
	if o.Error != CodeOpCancelled {
		t.Fatalf("the cancelled reclaim ended %+v", o)
	}
	for i := 0; i < 200; i++ {
		if h.reclaimRunning() {
			t.Fatal("the cancelled reclaim started itself again")
		}
		time.Sleep(time.Millisecond)
	}
	live := h.liveBytes(t, id)
	if st := h.stat(t, id); st.Size <= live+metadataSlack {
		t.Errorf("the run finished after all: %+v for %d live bytes", st, live)
	}

	// The next commit is what takes it up, and it finishes: the layout was
	// worth a run for every one of those milliseconds.
	h.rec.reset()
	if e := h.c.RenameRecord(id, stay[0].ID, "renamed.bin"); e != nil {
		t.Fatalf("rename: %v", e)
	}
	if o := h.waitReclaim(t); o.Error != "" {
		t.Fatalf("the reclaim after the rename: %+v", o)
	}
	if st := h.stat(t, id); st.Size > live+metadataSlack {
		t.Errorf("the file stood at %d for %d live bytes after the resumed run", st.Size, live)
	}
	h.extractsAll(t, id, stay)
}

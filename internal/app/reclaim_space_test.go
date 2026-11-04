package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dreamxwarden01/enfold/internal/archive"
	"github.com/dreamxwarden01/enfold/internal/format"
)

// Reclaim space as the owner ruled it on 2026-11-03 and 2026-11-04 (APP.md
// §2.3 "Space comes back"): the plan is made off the commit's own call and
// its figures kept on the open archive; the core runs only a cheap run by
// itself; the user's *Reclaim space* runs the rest; Compact checks the
// drive before it writes.

// summaryOf is the archives list's row for id.
func (h *harness) summaryOf(t *testing.T, id string) ArchiveSummary {
	t.Helper()
	list, e := h.c.ListArchives(true)
	if e != nil {
		t.Fatalf("list: %v", e)
	}
	for _, s := range list {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("%s is not listed", id)
	return ArchiveSummary{}
}

// The delete answers the page before its plan is made: the plan is held on
// its own goroutine, the delete has returned all the same, the figures the
// page reads are the older state's meanwhile, and once the plan is let go
// they land — on Stat and on the list's row alike — with archives.changed.
func TestADeleteReturnsBeforeItsPlanLands(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	h.lowerRule(reclaimRule{floor: 1 << 62, share: 4, budget: 64 << 20, cheap: 256 << 20}) // figures only
	id := h.openArchive(t, "Async")
	first := h.src(t, "async-first.bin", string(incompressible(t, 1<<20)))
	rest := h.src(t, "async-rest.bin", string(incompressible(t, 300<<10)))
	if o := h.add(t, id, rootID, PolicySkip, first, rest); o.Error != "" {
		t.Fatalf("add: %+v", o)
	}
	before := h.planned(t, id)
	drop := h.row(t, id, rootID, "async-first.bin")

	held, release := make(chan struct{}), make(chan struct{})
	h.c.seams.planning = func() {
		held <- struct{}{}
		<-release
	}
	returned := make(chan *Error, 1)
	go func() { returned <- h.c.DeleteRecords(id, []string{drop.ID}) }()
	<-held // the plan is being made, and waits here
	if e := <-returned; e != nil {
		t.Fatalf("delete: %v", e)
	}
	st := h.stat(t, id)
	if st.Reclaim == nil || st.Reclaim.AtSeq == st.Seq || *st.Reclaim != before {
		t.Fatalf("while the plan is held the figures are %+v at seq %d; the last ones were %+v", st.Reclaim, st.Seq, before)
	}
	h.rec.reset()
	h.c.seams.planning = nil
	close(release)
	f := h.planned(t, id)
	h.rec.waitFor(t, EventArchivesChanged, func(any) bool { return true })

	plan := h.open(t, id).a.PlanReclaim(reclaimBudget)
	want := ReclaimView{Back: plan.RunTailReturned, Move: plan.RunBytesToMove, Complete: plan.RunComplete, AtSeq: h.stat(t, id).Seq}
	if f != want || !f.Complete || f.Back < 1<<20-metadataSlack || f.Move < 300<<10 {
		t.Fatalf("the figures that landed: %+v; the plan says %+v", f, want)
	}
	if st := h.stat(t, id); st.Reclaim == nil || *st.Reclaim != want || st.CompactNeeds != st.Size-st.FreeSpace {
		t.Errorf("Stat after the plan: %+v", st)
	}
	if s := h.summaryOf(t, id); s.Reclaim == nil || *s.Reclaim != want || s.CompactNeeds == 0 {
		t.Errorf("the list's row after the plan: %+v", s)
	}
	if h.reclaimRunning() {
		t.Error("a run started over the floor")
	}

	// A closed archive has no figures: they need the index, and the index
	// needs the key.
	if e := h.c.CloseArchive(id); e != nil {
		t.Fatalf("close: %v", e)
	}
	if s := h.summaryOf(t, id); s.Reclaim != nil || s.CompactNeeds != 0 {
		t.Errorf("a closed archive's row carries figures: %+v", s)
	}
}

// A run over the cheap bound is not the core's to start (the owner's
// softening, 2026-11-03): the figures say what it would give back and the
// archive stays as it is, a commit after that refreshes them and starts
// nothing, and the user's *Reclaim space* makes the run, to the end.
func TestARunOverTheCheapBoundWaitsForTheUser(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	h.lowerRule(reclaimRule{floor: 4 << 10, share: 4, budget: 64 << 20, cheap: 256 << 10})
	id, stay, before := h.holed(t, "Dear", 1<<20, 300<<10, 300<<10)
	f := h.planned(t, id)
	if !f.Complete || f.Move <= 256<<10 || f.Back < 1<<20-metadataSlack {
		t.Fatalf("the figures: %+v", f)
	}
	if h.reclaimRunning() {
		t.Fatal("the core started a run over the cheap bound")
	}
	if e := h.c.RenameRecord(id, stay[0].ID, "renamed.bin"); e != nil {
		t.Fatalf("rename: %v", e)
	}
	if g := h.planned(t, id); g.AtSeq <= f.AtSeq || !g.Complete || g.Back < 1<<20-metadataSlack {
		t.Errorf("the figures after the rename: %+v, before %+v", g, f)
	}
	if h.reclaimRunning() {
		t.Fatal("a later commit started the run over the cheap bound")
	}
	opID, e := h.c.ReclaimSpace(id)
	if e != nil {
		t.Fatalf("reclaim space: %v", e)
	}
	o := h.rec.waitOp(t, opID)
	if o.Error != "" || o.Kind != "reclaim" || !o.Requested || o.Ended != "finished" {
		t.Fatalf("the user's run: %+v", o)
	}
	st := h.stat(t, id)
	if live := h.liveBytes(t, id); st.Size > live+metadataSlack {
		t.Errorf("the file stood at %d for %d live bytes after the user's run", st.Size, live)
	}
	// What the run says it gave back is the file's own shrink to within a
	// commit's metadata — before is the size ahead of the delete and the
	// rename, whose own commits moved the file by their metadata, while the
	// run measures from its own start (ops.go returnedSoFar) — and it is most
	// of the 1 MiB deleted. Both must hold.
	shrink := int64(before) - int64(st.Size)
	if d := int64(o.Returned) - shrink; d > metadataSlack || d < -metadataSlack {
		t.Errorf("the user's run says %d back; the file went %d -> %d", o.Returned, before, st.Size)
	}
	if o.Returned < 1<<20-metadataSlack {
		t.Errorf("the user's run says only %d back", o.Returned)
	}
	if o.Moved < 600<<10 {
		t.Errorf("the user's run says it moved %d", o.Moved)
	}
	// Its end made the figures again: nothing left to give back.
	if g := h.planned(t, id); !g.Complete || g.Back != 0 {
		t.Errorf("the figures after the run: %+v", g)
	}
	h.extractsAll(t, id, stay)
}

// *Reclaim space* opens nothing and is refused with a code: an id that is
// not one, an archive that is not open, a whole plan that says nothing comes
// back, and a reclaim of the archive already registered.
func TestReclaimSpaceRefusals(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	if _, e := h.c.ReclaimSpace("not an id"); e == nil || e.Code != CodeParams {
		t.Errorf("a bad id: %v", e)
	}
	closed, _ := h.newArchive("Closed")
	if _, e := h.c.ReclaimSpace(closed); e == nil || e.Code != CodeArchiveNotOpen {
		t.Errorf("a closed archive: %v", e)
	}
	if h.isHeld(closed) {
		t.Error("the refusal opened the archive")
	}

	id := h.openArchive(t, "Packed")
	if o := h.add(t, id, rootID, PolicySkip, h.src(t, "packed.bin", string(incompressible(t, 64<<10)))); o.Error != "" {
		t.Fatalf("add: %+v", o)
	}
	if f := h.planned(t, id); !f.Complete || f.Back != 0 {
		t.Fatalf("a packed archive's figures: %+v", f)
	}
	if _, e := h.c.ReclaimSpace(id); e == nil || e.Code != CodeNothingToReclaim {
		t.Errorf("nothing to reclaim: %v", e)
	}

	other := &op{id: randomID(), kind: "reclaim", archiveID: id, startedAt: h.c.now(), over: make(chan struct{}), c: h.c}
	h.c.mu.Lock()
	h.c.ops[other.id] = other
	h.c.mu.Unlock()
	if _, e := h.c.ReclaimSpace(id); e == nil || e.Code != CodeArchiveBusy {
		t.Errorf("a reclaim already running: %v", e)
	}
	h.c.mu.Lock()
	other.finished = true
	h.c.mu.Unlock()
	close(other.over)
}

// The guard the runs are weighed against, apart from any archive: the
// core's own at the rule's share; the user's at the bargain its first plan
// showed — the ratio it promised when that is worse than the rule's, a
// bound at half again what it said it would move — and on whole plans
// only; and either one ended when a whole plan promises nothing more.
func TestTheRunGuard(t *testing.T) {
	const M = 1 << 20
	whole := func(back, move uint64) archive.ReclaimPlan {
		return archive.ReclaimPlan{RunTailReturned: back, RunBytesToMove: move, RunComplete: true}
	}
	auto := runGuard{share: 4}
	if got := auto.stop(whole(10*M, 40*M), 40*M, 0); got != "" {
		t.Errorf("the core's run at the share: %q", got)
	}
	if got := auto.stop(whole(10*M, 40*M), 40*M+1, 0); got != "guard" {
		t.Errorf("the core's run over the share: %q", got)
	}
	if got := auto.stop(archive.ReclaimPlan{RunTailReturned: M}, 5*M, 0); got != "guard" {
		t.Errorf("the core's run on a plan cut short: %q", got)
	}
	if got := auto.stop(whole(0, 8*M), 0, 0); got != "finished" {
		t.Errorf("a whole plan with nothing back: %q", got)
	}

	// The user chose 1 GB back for 9 GB moved.
	user := runGuard{requested: true, share: 4}.bargain(whole(1024*M, 9*1024*M))
	if user.share != 9 || user.bound != 9*1024*M*3/2 {
		t.Fatalf("the user's bargain: %+v", user)
	}
	if got := user.stop(whole(1024*M, 5*1024*M), 4*1024*M, 0); got != "" {
		t.Errorf("the user's run at nine to one is stopped: %q", got)
	}
	if got := user.stop(whole(1024*M, 5*1024*M), 9*1024*M+1, 0); got != "guard" {
		t.Errorf("the user's run over nine to one: %q", got)
	}
	big := runGuard{requested: true, share: 4}.bargain(whole(1024*M, 1024*M))
	if big.share != 4 || big.bound != 1536*M {
		t.Fatalf("a cheap bargain: %+v", big)
	}
	if got := big.stop(whole(1024*M, 1024*M), 1536*M, 1024*M); got != "" {
		t.Errorf("at the bound: %q", got)
	}
	if got := big.stop(whole(1024*M, 1024*M), 1536*M+1, 1024*M); got != "bound" {
		t.Errorf("past the bound: %q", got)
	}
	// A first plan cut short sets no bargain, and a plan cut short weighs
	// nothing: the run goes on until no more comes back, or a cancel.
	cut := runGuard{requested: true, share: 4}.bargain(archive.ReclaimPlan{RunBytesToMove: 3 * 1024 * M})
	if cut.share != 4 || cut.bounded {
		t.Fatalf("a bargain cut short: %+v", cut)
	}
	if got := cut.stop(archive.ReclaimPlan{RunBytesToMove: 3 * 1024 * M}, 10*1024*M, 0); got != "" {
		t.Errorf("the user's run on a plan cut short: %q", got)
	}

	// A whole first plan that promises a tail and no move bounds the run at
	// nothing moved, which is a bound and not the absence of one (the
	// outside audit of 2026-11-04, finding 4): the empty commit that gives
	// the tail back goes ahead, and a plan with moves after it — a reader's
	// end exposed them — stops the run, however well the share would
	// cover them.
	tail := runGuard{requested: true, share: 4}.bargain(whole(64*M, 0))
	if !tail.bounded || tail.bound != 0 || tail.share != 4 {
		t.Fatalf("a tail-only bargain: %+v", tail)
	}
	if got := tail.stop(whole(64*M, 0), 0, 0); got != "" {
		t.Errorf("the tail's own empty commit is stopped: %q", got)
	}
	exposed := whole(64*M, 8*M)
	exposed.Moves = []archive.Move{{From: format.Extent{Off: 128 * M, Len: 8 * M}, To: format.Extent{Off: 4096, Len: 8 * M}}}
	if got := tail.stop(exposed, 0, 64*M); got != "bound" {
		t.Errorf("moves after a bargain of none: %q", got)
	}
	if got := cut.stop(exposed, 0, 64*M); got != "" {
		t.Errorf("an unbounded run on the same plan: %q", got)
	}

	// The user's budget per commit: the rule's, or eight times the index.
	r := reclaimRule{budget: 64 * M}
	if b := r.requestedBudget(1 * M); b != 64*M {
		t.Errorf("a small index: %d", b)
	}
	if b := r.requestedBudget(25 * M); b != 200*M {
		t.Errorf("a 25 MiB index: %d", b)
	}
}

// Compact checks the drive before it writes (ruled 2026-11-04): with less
// free than its fresh copy needs it is refused, the figures and the drive
// on the error and the archive left as it was; with enough it runs; and a
// drive that cannot be measured is not refused on a guess.
func TestCompactChecksTheDrive(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	h.lowerRule(reclaimRule{floor: 1 << 62, share: 4, budget: 64 << 20, cheap: 256 << 20})
	id, _, _ := h.holed(t, "Roomy", 1<<20, 300<<10)
	h.planned(t, id)
	st := h.stat(t, id)
	if st.CompactNeeds != st.Size-st.FreeSpace || st.CompactNeeds == 0 {
		t.Fatalf("the need: %+v", st)
	}

	var asked string
	h.c.diskFree = func(p string) (string, uint64, error) {
		asked = p
		return "D:", st.CompactNeeds - 1, nil
	}
	_, e := h.c.Compact(id)
	if e == nil || e.Code != CodeCompactNoSpace || e.Needed != st.CompactNeeds || e.Free == nil || *e.Free != st.CompactNeeds-1 || e.Volume != "D:" {
		t.Fatalf("a drive one byte short: %+v", e)
	}
	if asked != h.open(t, id).path {
		t.Errorf("the drive asked about was %q", asked)
	}
	if after := h.stat(t, id); after.Size != st.Size || after.State != "open" {
		t.Errorf("the refused compaction changed the archive: %+v", after)
	}

	// A full drive still says how much is free: "free": 0 crosses, where
	// every other code sends no such field (errors.go Error.Free).
	h.c.diskFree = func(string) (string, uint64, error) { return "D:", 0, nil }
	if _, e := h.c.Compact(id); e == nil || e.Code != CodeCompactNoSpace {
		t.Fatalf("a full drive: %+v", e)
	} else if b, _ := json.Marshal(e); !strings.Contains(string(b), `"free":0`) {
		t.Errorf("a full drive's refusal crosses as %s", b)
	}
	if b, _ := json.Marshal(&Error{Code: CodeIO}); string(b) != `{"code":"io"}` {
		t.Errorf("another code crosses as %s", b)
	}

	h.c.diskFree = func(string) (string, uint64, error) { return "", 0, errors.New("no answer") }
	opID, e := h.c.Compact(id)
	if e != nil {
		t.Fatalf("a drive that cannot be measured: %v", e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != "" {
		t.Fatalf("the compaction: %+v", o)
	}

	h.c.diskFree = func(string) (string, uint64, error) { return "D:", 1 << 40, nil }
	opID, e = h.c.Compact(id)
	if e != nil {
		t.Fatalf("a roomy drive: %v", e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != "" {
		t.Fatalf("the second compaction: %+v", o)
	}
}

// archivesChangedCount is how many archives.changed the window has been sent.
func (h *harness) archivesChangedCount() int {
	n := 0
	for _, ev := range h.rec.snapshot() {
		if _, ok := ev.payload.(ArchivesChanged); ok {
			n++
		}
	}
	return n
}

// A plan made again at the same state lands the same figure and tells the
// window nothing (ops.go planLoop): an extract's or a verify's end, an open
// and the last preview reader's close each make one, and each
// archives.changed costs the window a refresh of the list and the backup
// page. A plan whose figure did change is still announced.
func TestAPlanThatChangesNothingIsNotAnnounced(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	h.lowerRule(reclaimRule{floor: 1 << 62, share: 4, budget: 64 << 20, cheap: 256 << 20}) // figures only
	id, _, _ := h.holed(t, "Quiet", 1<<20, 300<<10)
	f := h.planned(t, id)
	oa := h.open(t, id)
	settle := func() {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			h.c.mu.Lock()
			busy := oa.planning
			h.c.mu.Unlock()
			if !busy {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("the plan never landed")
			}
			time.Sleep(time.Millisecond)
		}
	}

	n := h.archivesChangedCount()
	h.c.planReclaim(oa, nil, false)
	settle()
	if g := h.planned(t, id); g != f {
		t.Fatalf("the same state planned again gave %+v, before %+v", g, f)
	}
	// The event is sent after the plan's flag is let go, so a stray one
	// is given a moment to arrive before the count is read.
	time.Sleep(50 * time.Millisecond)
	if got := h.archivesChangedCount(); got != n {
		t.Errorf("an unchanged figure sent %d archives.changed", got-n)
	}

	// The same figure at a newer state is news: it is no longer pending.
	h.c.mu.Lock()
	oa.seq++
	h.c.mu.Unlock()
	n = h.archivesChangedCount()
	h.c.planReclaim(oa, nil, false)
	settle()
	if g := h.planned(t, id); g.AtSeq != f.AtSeq+1 {
		t.Fatalf("the figure after the bump: %+v", g)
	}
	deadline := time.Now().Add(5 * time.Second)
	for h.archivesChangedCount() == n {
		if time.Now().After(deadline) {
			t.Fatal("a figure that caught up with the state was not announced")
		}
		time.Sleep(time.Millisecond)
	}
}

// Compact measures the drive again once the handle is its own (the outside
// audit of 2026-11-04, finding 1): asked while an add held the handle, the
// first check measured the archive before the add committed; the add then
// grew it past what the drive holds, and the compaction, measured on that
// first figure, would have written until the disk was full. It is refused
// instead, before it writes, with the archive left open as it was.
func TestCompactMeasuresTheDriveAgainOnItsTurn(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	h.lowerRule(reclaimRule{floor: 1 << 62, share: 4, budget: 64 << 20, cheap: 256 << 20}) // figures only
	id, _, _ := h.holed(t, "Turn", 1<<20, 300<<10)
	h.planned(t, id)
	st := h.stat(t, id)
	room := st.CompactNeeds + 256<<10
	var mu sync.Mutex
	var asked []uint64
	h.c.diskFree = func(string) (string, uint64, error) {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, room)
		return "D:", room, nil
	}

	// The handle is taken, as an add's run holds it.
	oa := h.open(t, id)
	oa.opMu.Lock()
	opID, e := h.c.Compact(id)
	if e != nil {
		oa.opMu.Unlock()
		t.Fatalf("the first check, against the archive as it stood: %v", e)
	}
	// The add commits a mebibyte while the compaction waits its turn: the
	// archive layer's transaction, then the core's own bookkeeping of a
	// commit, as the add's would be.
	late := incompressible(t, 1<<20)
	root, _ := parseID(rootID)
	tx, err := oa.a.Begin()
	if err != nil {
		oa.opMu.Unlock()
		t.Fatal(err)
	}
	if _, err := tx.Add(context.Background(), root, "late.bin", bytes.NewReader(late), int64(len(late))); err != nil {
		tx.Abort()
		oa.opMu.Unlock()
		t.Fatal(err)
	}
	rec, err := tx.Commit(context.Background())
	if err != nil {
		oa.opMu.Unlock()
		t.Fatal(err)
	}
	h.c.reclaimCommitted(oa, rec)
	grown := h.stat(t, id)
	if grown.CompactNeeds <= room {
		oa.opMu.Unlock()
		t.Fatalf("the add did not grow the need past the drive: %d for %d free", grown.CompactNeeds, room)
	}
	oa.opMu.Unlock()

	o := h.rec.waitOp(t, opID)
	if o.Error != CodeCompactNoSpace {
		t.Fatalf("the compaction on its turn: %+v", o)
	}
	mu.Lock()
	n := len(asked)
	mu.Unlock()
	if n != 2 {
		t.Errorf("the drive was asked %d times; once at the call and once on the turn", n)
	}
	after := h.stat(t, id)
	if after.State != "open" || after.Size != grown.Size {
		t.Errorf("the refused compaction changed the archive: %+v, was %+v", after, grown)
	}
	h.c.mu.Lock()
	quiesced := oa.quiesced
	h.c.mu.Unlock()
	if quiesced {
		t.Error("the refused compaction left the previews quiesced")
	}

	// With the room there, the same archive compacts.
	h.c.diskFree = func(string) (string, uint64, error) { return "D:", 1 << 40, nil }
	if opID, e = h.c.Compact(id); e != nil {
		t.Fatalf("a roomy drive: %v", e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != "" {
		t.Fatalf("the compaction with room: %+v", o)
	}
}

// Two previews, one on the tail file: the plan leaves both files where
// they lie, and says nothing comes back. The tail's preview ends while the
// other stays — not the last reader — and the figure is made again, so the
// page offers Reclaim space and ReclaimSpace runs rather than refusing on a
// zero that reader caused (the outside audit of 2026-11-04, finding 3).
func TestAReaderThatIsNotTheLastLiftsAZeroItCaused(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	h.lowerRule(reclaimRule{floor: 1 << 62, share: 4, budget: 64 << 20, cheap: 256 << 20}) // figures only
	id := h.openArchive(t, "Pinned")
	first := h.src(t, "pinned-first.bin", string(incompressible(t, 1<<20)))
	mid := h.src(t, "pinned-mid.bin", string(incompressible(t, 300<<10)))
	last := h.src(t, "pinned-last.bin", string(incompressible(t, 300<<10)))
	if o := h.add(t, id, rootID, PolicySkip, first, mid, last); o.Error != "" {
		t.Fatalf("add: %+v", o)
	}
	stay := []FileRow{h.row(t, id, rootID, "pinned-mid.bin"), h.row(t, id, rootID, "pinned-last.bin")}
	drop := h.row(t, id, rootID, "pinned-first.bin")

	// Two bodies in flight, counted as preview.go counts them.
	oa := h.open(t, id)
	midID, _ := parseID(stay[0].ID)
	lastID, _ := parseID(stay[1].ID)
	rdMid, err := oa.a.OpenReader(midID)
	if err != nil {
		t.Fatal(err)
	}
	rdLast, err := oa.a.OpenReader(lastID)
	if err != nil {
		rdMid.Close()
		t.Fatal(err)
	}
	h.c.mu.Lock()
	oa.readers += 2
	h.c.mu.Unlock()
	defer func() {
		rdMid.Close()
		h.c.releaseReader(oa)
	}()

	if e := h.c.DeleteRecords(id, []string{drop.ID}); e != nil {
		t.Fatalf("delete: %v", e)
	}
	if f := h.planned(t, id); !f.Complete || f.Back != 0 {
		t.Fatalf("with both files held the figure is %+v; the test needs a whole zero", f)
	}
	if _, e := h.c.ReclaimSpace(id); e == nil || e.Code != CodeNothingToReclaim {
		t.Fatalf("ReclaimSpace on the held zero: %v", e)
	}

	rdLast.Close()
	h.c.releaseReader(oa)
	f := h.planned(t, id)
	if f.Back == 0 {
		t.Fatalf("after the tail's preview ended the figure is still %+v", f)
	}
	if row := h.summaryOf(t, id); row.Reclaim == nil || row.Reclaim.Back != f.Back {
		t.Errorf("the list row does not carry the new figure: %+v", row.Reclaim)
	}
	opID, e := h.c.ReclaimSpace(id)
	if e != nil {
		t.Fatalf("ReclaimSpace after the tail's preview ended: %v", e)
	}
	o := h.rec.waitOp(t, opID)
	if o.Error != "" || o.Returned == 0 {
		t.Fatalf("the run: %+v", o)
	}
	h.readsBack(t, id, stay)
}

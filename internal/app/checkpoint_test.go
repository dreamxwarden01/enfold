package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dreamxwarden01/enfold/internal/archive"
)

// An add commits at checkpoints (ruled 2026-11-03, the user's: "add and
// replace should be atomic per file, as Explorer does: a file shows in the
// list as soon as it is added; cancel drops only the queued files and the
// one being added, and keeps what was added"; APP.md §2.3). A checkpoint is
// taken as a file is about to begin, once one was written whole since the
// last commit and a second or 256 MiB has come; the tests lower the rule to
// zero, so every file after the first finds the one before it committed and
// an add of n files is n commits. Nothing here writes more than a few MiB or
// waits on real time.

// everyFile lowers the checkpoint rule to zero for one test: a checkpoint
// before every file but the first.
func everyFile(t *testing.T) {
	t.Helper()
	was := addCheckpoint
	addCheckpoint = checkpointRule{}
	t.Cleanup(func() { addCheckpoint = was })
}

// atFlip installs the checkpoint's test seam for one test.
func atFlip(t *testing.T, f func()) {
	t.Helper()
	checkpointFlip = f
	t.Cleanup(func() { checkpointFlip = nil })
}

// sources writes n sources of the given size, each its own bytes, and
// returns their paths and contents in order.
func (h *harness) sources(t *testing.T, prefix string, n, size int) ([]string, [][]byte) {
	t.Helper()
	var paths []string
	var bodies [][]byte
	for i := 0; i < n; i++ {
		b := incompressible(t, size+i) // a byte longer each, so no two are alike
		b[0] = byte(i)
		paths = append(paths, h.src(t, fmt.Sprintf("%s/%s%d.bin", prefix, prefix, i+1), string(b)))
		bodies = append(bodies, b)
	}
	return paths, bodies
}

// readsAs reads one record through the archive's own Reader, which verifies
// every tag, and compares it with what was added. It returns an error rather
// than failing, since the checkpoint tests call it from inside an event on
// the operation's goroutine, where a test may not stop.
func readsAs(oa *openArchive, id string, want []byte) error {
	fid, ok := parseID(id)
	if !ok {
		return fmt.Errorf("%q is not an id", id)
	}
	rd, err := oa.a.OpenReader(fid)
	if err != nil {
		return err
	}
	defer rd.Close()
	got, err := io.ReadAll(rd)
	if err != nil {
		return err
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("read %d bytes, not the %d added", len(got), len(want))
	}
	return nil
}

// changedOf counts archive.changed for one archive in the recorder.
func (h *harness) changedOf(id string) int {
	n := 0
	for _, e := range h.rec.snapshot() {
		if c, ok := e.payload.(ArchiveChanged); ok && e.name == EventArchiveChanged && c.ID == id {
			n++
		}
	}
	return n
}

// reopen closes the archive and opens it again: the open checks the file
// whole — both superblocks, the index's tag, every live extent against the
// free map (archive.Open's check) — and a free map it had to rebuild is
// said by FreeMapRebuilt.
func (h *harness) reopen(t *testing.T, id string) *openArchive {
	t.Helper()
	if e := h.c.CloseArchive(id); e != nil {
		t.Fatalf("close: %v", e)
	}
	if _, e := h.c.OpenArchive(id); e != nil {
		t.Fatalf("reopen: %v", e)
	}
	oa := h.open(t, id)
	if err := oa.a.FreeMapRebuilt(); err != nil {
		t.Fatalf("the free map was not consistent at the reopen: %v", err)
	}
	return oa
}

// verifies runs the archive's own Verify (R36): the file hashed whole and
// the fingerprint recorded.
func (h *harness) verifies(t *testing.T, id string) {
	t.Helper()
	opID, e := h.c.Verify(id)
	if e != nil {
		t.Fatalf("verify: %v", e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != "" {
		t.Fatalf("verify: %+v", o)
	}
}

// Five files with the rule at zero are five commits: four checkpoints and
// the final commit, each its own archive.changed, its own sequence and its
// own receipt, every file committed so far readable at each one, and no
// commit carrying a file that was not yet written whole.
func TestAnAddCommitsAtEachCheckpoint(t *testing.T) {
	everyFile(t)
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Checkpoints")
	oa := h.open(t, id)
	paths, bodies := h.sources(t, "five", 5, 64<<10)

	before := h.stat(t, id)
	fileSeq0 := oa.a.Seq()
	var mu sync.Mutex
	var problems []string
	var fileSeqs []uint64
	var landed []int
	h.rec.onEvent(func(name string, payload any) {
		c, ok := payload.(ArchiveChanged)
		if name != EventArchiveChanged || !ok || c.ID != id {
			return
		}
		// On the operation's own goroutine, between the commit and the next
		// Begin: what the page would list now, read back through the archive.
		p, e := h.c.Page(id, rootID, "name", 0, 100)
		mu.Lock()
		defer mu.Unlock()
		fileSeqs = append(fileSeqs, oa.a.Seq())
		if e != nil {
			problems = append(problems, fmt.Sprintf("page: %v", e))
			return
		}
		landed = append(landed, len(p.Rows))
		for _, row := range p.Rows {
			var i int
			if _, err := fmt.Sscanf(row.Name, "five%d.bin", &i); err != nil || i < 1 || i > 5 {
				problems = append(problems, "an unexpected row "+row.Name)
				continue
			}
			if err := readsAs(oa, row.ID, bodies[i-1]); err != nil {
				problems = append(problems, fmt.Sprintf("%s at commit %d: %v", row.Name, len(landed), err))
			}
		}
	})
	o := h.add(t, id, rootID, PolicySkip, paths...)
	h.rec.onEvent(nil)
	if o.Error != "" || len(o.Results) != 5 {
		t.Fatalf("the add: %+v", o)
	}
	for _, r := range o.Results {
		if r.Outcome != "added" {
			t.Fatalf("%s: %+v", r.Name, r)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for _, p := range problems {
		t.Error(p)
	}
	// One file more at every commit: a checkpoint never carries the file
	// being written, and never splits one.
	if fmt.Sprint(landed) != "[1 2 3 4 5]" {
		t.Errorf("the rows at each commit: %v", landed)
	}
	if n := h.changedOf(id); n != 5 {
		t.Errorf("archive.changed %d times for five files at zero", n)
	}
	if st := h.stat(t, id); st.Seq != before.Seq+5 || st.Files != 5 || st.ReceiptOwed {
		t.Errorf("after five commits: %+v, was %+v", st, before)
	}
	// The file's own sequence advances by one per commit, and by one more
	// for a commit that freed the tail (the follow-up empty commit, R31 as
	// amended): never by less, never by more than two.
	prev, trims := fileSeq0, 0
	for i, s := range fileSeqs {
		switch s - prev {
		case 1:
		case 2:
			trims++
		default:
			t.Errorf("commit %d moved the file's sequence %d -> %d", i+1, prev, s)
		}
		prev = s
	}
	t.Logf("file sequence %d -> %d over %d commits (%d tail trims): %v", fileSeq0, oa.a.Seq(), len(fileSeqs), trims, fileSeqs)
	if oa.a.Seq() != fileSeq0+uint64(len(fileSeqs)+trims) {
		t.Errorf("the file's sequence %d is not %d commits and %d trims past %d", oa.a.Seq(), len(fileSeqs), trims, fileSeq0)
	}
	h.receiptCurrent(t, id, "after the checkpointed add")
}

// With the rule at its default an add is one commit, as it was: the clock
// the tests run on does not move, so a second never passes, and five small
// files are far under 256 MiB.
func TestAnAddUnderTheRuleIsOneCommit(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Default")
	paths, _ := h.sources(t, "few", 5, 4<<10)
	before := h.stat(t, id)
	if o := h.add(t, id, rootID, PolicySkip, paths...); o.Error != "" {
		t.Fatalf("the add: %+v", o)
	}
	if st := h.stat(t, id); st.Seq != before.Seq+1 || st.Files != 5 {
		t.Fatalf("the add under the default rule: %+v, was %+v", st, before)
	}
}

// A cancel after the second checkpoint — through the operation's own
// context, while the third file is being read — keeps the two files the
// checkpoints committed and drops the third and everything after it. The
// operation ends op.cancelled with the outcomes of the committed files and
// none for the rest, which is what the done line counts ("Stopped. 2 of 5
// files were added to …"); the archive reopens clean, its free map
// consistent, and Verify hashes it whole.
func TestACancelAfterTheSecondCheckpointKeepsTwoFiles(t *testing.T) {
	everyFile(t)
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Stopped")
	oa := h.open(t, id)
	const size = 256 << 10
	paths, bodies := h.sources(t, "five", 5, size)

	before := h.stat(t, id)
	var opID string
	ready := make(chan struct{})
	commits := 0
	var atSecond ArchiveStat
	answers := make(chan *Error, 1)
	h.rec.onEvent(func(name string, payload any) {
		switch v := payload.(type) {
		case ArchiveChanged:
			if name != EventArchiveChanged || v.ID != id {
				return
			}
			commits++
			if commits == 2 {
				atSecond, _ = h.c.Stat(id)
				// Progress is coalesced to ten events a second on the
				// core's clock, which stands still here: moving it lets the
				// third file's first read be said, and the cancel be taken
				// inside it.
				h.clk.Advance(time.Second)
			}
		case OpView:
			if name != EventOpProgress || commits != 2 {
				return
			}
			<-ready
			if v.ID == opID && v.Done > 2*size+1 && len(answers) == 0 {
				answers <- h.c.CancelOp(opID)
			}
		}
	})
	defer h.rec.onEvent(nil)
	var e *Error
	opID, e = h.c.AddFiles(id, rootID, paths, PolicySkip)
	if e != nil {
		t.Fatal(e)
	}
	close(ready)
	o := h.rec.waitOp(t, opID)
	h.rec.onEvent(nil)
	select {
	case ans := <-answers:
		if ans != nil {
			t.Fatalf("the cancel between checkpoints answered %v", ans)
		}
	default:
		t.Fatal("no cancel was taken inside the third file")
	}
	if o.Error != CodeOpCancelled {
		t.Fatalf("the stopped add: %+v", o)
	}
	// The done line's inputs: five planned, two outcomes, both added.
	if o.Items != 5 || len(o.Results) != 2 {
		t.Fatalf("the stopped add's results: items %d, %+v", o.Items, o.Results)
	}
	for i, r := range o.Results {
		if r.Outcome != "added" || r.Name != fmt.Sprintf("five%d.bin", i+1) {
			t.Fatalf("result %d: %+v", i, r)
		}
	}
	p := h.page(t, id, rootID)
	if p.Total != 2 {
		t.Fatalf("rows after the stop: %+v", p.Rows)
	}
	for i, name := range []string{"five1.bin", "five2.bin"} {
		if err := readsAs(oa, h.row(t, id, rootID, name).ID, bodies[i]); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	st := h.stat(t, id)
	if st.Seq != before.Seq+2 || st.Files != 2 {
		t.Fatalf("the stopped add committed %+v, was %+v", st, before)
	}
	// The third file's bytes went with the abort: the file is the size the
	// second checkpoint left it.
	if st.Size != atSecond.Size {
		t.Fatalf("the aborted file's bytes were not given back: %d, at the second checkpoint %d", st.Size, atSecond.Size)
	}
	h.receiptCurrent(t, id, "after the stop")
	oa = h.reopen(t, id)
	for i, name := range []string{"five1.bin", "five2.bin"} {
		if err := readsAs(oa, h.row(t, id, rootID, name).ID, bodies[i]); err != nil {
			t.Fatalf("%s after the reopen: %v", name, err)
		}
	}
	h.verifies(t, id)
	// And the rest can be added after: the same batch under skip adds the
	// three that were dropped.
	o = h.add(t, id, rootID, PolicySkip, paths...)
	if o.Error != "" || h.stat(t, id).Files != 5 {
		t.Fatalf("the add after the stop: %+v", o)
	}
}

// A cancel that lands once a checkpoint has decided to commit — inside the
// window before the flip — is taken (CancelOp answers nil, not
// op.committing): the commit runs under a context no cancel reaches, so the
// file it carries lands, and the add stops right after it, ending
// op.cancelled with that file among its results.
func TestACancelDuringACheckpointLandsTheFileAndStops(t *testing.T) {
	everyFile(t)
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Flip")
	paths, _ := h.sources(t, "four", 4, 32<<10)
	before := h.stat(t, id)

	ids := make(chan string, 1)
	answers := make(chan *Error, 1)
	flips := 0
	atFlip(t, func() {
		flips++
		if flips == 2 { // the checkpoint before the third file: it carries the second
			answers <- h.c.CancelOp(<-ids)
		}
	})
	opID, e := h.c.AddFiles(id, rootID, paths, PolicySkip)
	if e != nil {
		t.Fatal(e)
	}
	ids <- opID
	o := h.rec.waitOp(t, opID)
	if ans := <-answers; ans != nil {
		t.Fatalf("a cancel during a checkpoint answered %v", ans)
	}
	if o.Error != CodeOpCancelled || len(o.Results) != 2 {
		t.Fatalf("the add stopped at a checkpoint: %+v", o)
	}
	if st := h.stat(t, id); st.Seq != before.Seq+2 || st.Files != 2 {
		t.Fatalf("the checkpoint the cancel met did not land: %+v, was %+v", st, before)
	}
	if p := h.page(t, id, rootID); p.Total != 2 {
		t.Fatalf("rows: %+v", p.Rows)
	}

	// The final commit is unchanged: a cancel there is refused, and the
	// add ends with every file (TestCancelDuringTheCommitIsRefused, under
	// the rule at zero).
	h.rec.reset()
	more, _ := h.sources(t, "last", 3, 8<<10)
	refused := make(chan *Error, 1)
	h.rec.onEvent(func(name string, payload any) {
		c, ok := payload.(ArchiveChanged)
		if name != EventArchiveChanged || !ok || c.ID != id {
			return
		}
		// The add whose commit this is, if it is the final one.
		final := ""
		h.c.mu.Lock()
		for _, o := range h.c.ops {
			if o.kind == "add" && !o.finished && o.committing {
				final = o.id
			}
		}
		h.c.mu.Unlock()
		if final != "" && len(refused) == 0 {
			refused <- h.c.CancelOp(final)
		}
	})
	defer h.rec.onEvent(nil)
	lastID, e := h.c.AddFiles(id, rootID, more, PolicySkip)
	if e != nil {
		t.Fatal(e)
	}
	o = h.rec.waitOp(t, lastID)
	h.rec.onEvent(nil)
	if ans := <-refused; ans == nil || ans.Code != CodeOpCommitting {
		t.Fatalf("a cancel during the final commit answered %v", ans)
	}
	if o.Error != "" || len(o.Results) != 3 {
		t.Fatalf("the add whose final commit a cancel met: %+v", o)
	}
}

// A panic after a checkpoint — thrown by the progress sink inside the third
// file, the failure TestAPanicInsideAnAddLeavesNoTransactionOpen exercises —
// is recovered by the operation runner, and the two files the checkpoints
// committed are in the archive: the operation reports them, as a failure
// part way does (APP.md §2.3). It once reported none, the outcomes kept in
// a local the recovery never sees (the outside audit of 2026-11-03,
// finding 1).
func TestAPanicAfterTheCheckpointsReportsWhatTheyCommitted(t *testing.T) {
	everyFile(t)
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Panic after")
	const size = 256 << 10
	paths, bodies := h.sources(t, "three", 3, size)
	before := h.stat(t, id)

	commits := 0
	h.rec.onEvent(func(name string, payload any) {
		switch v := payload.(type) {
		case ArchiveChanged:
			if name != EventArchiveChanged || v.ID != id {
				return
			}
			commits++
			if commits == 2 {
				// As in TestACancelAfterTheSecondCheckpointKeepsTwoFiles:
				// the clock moves so the third file's first read is said.
				h.clk.Advance(time.Second)
			}
		case OpView:
			if name == EventOpProgress && commits == 2 && v.Kind == "add" && v.Done > 2*size+1 {
				panic("the progress sink threw")
			}
		}
	})
	defer h.rec.onEvent(nil)
	opID, e := h.c.AddFiles(id, rootID, paths, PolicySkip)
	if e != nil {
		t.Fatal(e)
	}
	o := h.rec.waitOp(t, opID)
	h.rec.onEvent(nil)
	if o.Error != CodeInternal {
		t.Fatalf("the panicking add: %+v", o)
	}
	if len(o.Results) != 2 {
		t.Fatalf("the panicking add reported %+v, not the two committed", o.Results)
	}
	for i, r := range o.Results {
		if r.Outcome != "added" || r.Name != fmt.Sprintf("three%d.bin", i+1) {
			t.Fatalf("result %d: %+v", i, r)
		}
	}
	if st := h.stat(t, id); st.Seq != before.Seq+2 || st.Files != 2 {
		t.Fatalf("the checkpoints committed %+v, was %+v", st, before)
	}
	oa := h.open(t, id)
	for i, name := range []string{"three1.bin", "three2.bin"} {
		if err := readsAs(oa, h.row(t, id, rootID, name).ID, bodies[i]); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	// The third file's transaction was aborted by the guard: the next add
	// opens one of its own.
	if o := h.add(t, id, rootID, PolicySkip, paths...); o.Error != "" || h.stat(t, id).Files != 3 {
		t.Fatalf("the add after the panic: %+v", o)
	}
}

// A panic in what a checkpoint sends after its commit stood — here the
// archive.changed sink of the second — still reports both files: the
// outcomes are on record the moment the commit stands, before its events
// (opTx.stood).
func TestAPanicInACheckpointsEventsReportsTheCommit(t *testing.T) {
	everyFile(t)
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Panic in event")
	paths, _ := h.sources(t, "evt", 3, 16<<10)
	before := h.stat(t, id)

	commits := 0
	h.rec.onEvent(func(name string, payload any) {
		if v, ok := payload.(ArchiveChanged); ok && name == EventArchiveChanged && v.ID == id {
			commits++
			if commits == 2 {
				panic("the archive.changed sink threw")
			}
		}
	})
	defer h.rec.onEvent(nil)
	opID, e := h.c.AddFiles(id, rootID, paths, PolicySkip)
	if e != nil {
		t.Fatal(e)
	}
	o := h.rec.waitOp(t, opID)
	h.rec.onEvent(nil)
	if o.Error != CodeInternal || len(o.Results) != 2 {
		t.Fatalf("the add whose checkpoint's event threw: %+v", o)
	}
	for i, r := range o.Results {
		if r.Outcome != "added" || r.Name != fmt.Sprintf("evt%d.bin", i+1) {
			t.Fatalf("result %d: %+v", i, r)
		}
	}
	if st := h.stat(t, id); st.Seq != before.Seq+2 || st.Files != 2 {
		t.Fatalf("the checkpoints committed %+v, was %+v", st, before)
	}
	if o := h.add(t, id, rootID, PolicySkip, paths...); o.Error != "" || h.stat(t, id).Files != 3 {
		t.Fatalf("the add after the panic: %+v", o)
	}
}

// Close archive while an add runs — the drain cancels the operation and waits
// for it — keeps what the checkpoints committed: the archive reopens with
// those files and no other, clean.
func TestClosingMidAddKeepsTheCheckpoints(t *testing.T) {
	everyFile(t)
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Drain")
	paths, bodies := h.sources(t, "five", 5, 16<<10)

	reached, release := make(chan struct{}), make(chan struct{})
	commits := 0
	h.rec.onEvent(func(name string, payload any) {
		c, ok := payload.(ArchiveChanged)
		if name != EventArchiveChanged || !ok || c.ID != id {
			return
		}
		commits++
		if commits == 2 {
			close(reached)
			<-release
		}
	})
	defer h.rec.onEvent(nil)
	opID, e := h.c.AddFiles(id, rootID, paths, PolicySkip)
	if e != nil {
		t.Fatal(e)
	}
	<-reached
	closed := make(chan *Error, 1)
	go func() { closed <- h.c.CloseArchive(id) }()
	// The drain has cancelled the operation once its context says so; the
	// add is then let go, and stops before its third file.
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.c.mu.Lock()
		o := h.c.ops[opID]
		cancelled := o != nil && o.ctx.Err() != nil
		h.c.mu.Unlock()
		if cancelled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Close archive did not cancel the add")
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	o := h.rec.waitOp(t, opID)
	if e := <-closed; e != nil {
		t.Fatalf("close: %v", e)
	}
	h.rec.onEvent(nil)
	if o.Error != CodeOpCancelled || len(o.Results) != 2 {
		t.Fatalf("the drained add: %+v", o)
	}
	if _, e := h.c.OpenArchive(id); e != nil {
		t.Fatalf("reopen: %v", e)
	}
	oa := h.open(t, id)
	if err := oa.a.FreeMapRebuilt(); err != nil {
		t.Fatalf("the free map at the reopen: %v", err)
	}
	if p := h.page(t, id, rootID); p.Total != 2 {
		t.Fatalf("rows after the drain: %+v", p.Rows)
	}
	for i, name := range []string{"five1.bin", "five2.bin"} {
		if err := readsAs(oa, h.row(t, id, rootID, name).ID, bodies[i]); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	h.verifies(t, id)
}

// A lock in the middle of an add stops nothing — the archive key is in
// memory (APP.md §2.3) — and every checkpoint after it commits with its
// receipt owed; the owed receipt is the newest commit's, and the unlock pays
// it, so the registry names the file's own sequence.
func TestALockMidAddOwesTheCheckpointsReceipts(t *testing.T) {
	everyFile(t)
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Locked")
	oa := h.open(t, id)
	paths, bodies := h.sources(t, "four", 4, 16<<10)
	before := h.stat(t, id)

	commits := 0
	var owed []bool
	h.rec.onEvent(func(name string, payload any) {
		c, ok := payload.(ArchiveChanged)
		if name != EventArchiveChanged || !ok || c.ID != id {
			return
		}
		commits++
		st, _ := h.c.Stat(id)
		owed = append(owed, st.ReceiptOwed)
		if commits == 1 {
			h.c.Lock()
		}
	})
	o := h.add(t, id, rootID, PolicySkip, paths...)
	h.rec.onEvent(nil)
	if o.Error != "" || len(o.Results) != 4 {
		t.Fatalf("the add a lock met: %+v", o)
	}
	if fmt.Sprint(owed) != "[false true true true]" {
		t.Errorf("receipts owed at each commit: %v", owed)
	}
	st := h.stat(t, id)
	if st.Seq != before.Seq+4 || st.Files != 4 || !st.ReceiptOwed {
		t.Fatalf("after the add under a lock: %+v", st)
	}
	for i := range paths {
		name := fmt.Sprintf("four%d.bin", i+1)
		if err := readsAs(oa, h.row(t, id, rootID, name).ID, bodies[i]); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	h.unlockWithPassword()
	if st := h.stat(t, id); st.ReceiptOwed {
		t.Fatalf("the owed receipt was not paid at the unlock: %+v", st)
	}
	h.receiptCurrent(t, id, "after the unlock")
}

// A checkpoint never starts the reclaim: the add is still running and holds
// the handle's turn, so a run started there would be registered beside it
// and wait behind it. The final commit plans one as every commit does, and
// it runs once the add is over.
func TestTheReclaimWaitsForTheAddsEnd(t *testing.T) {
	everyFile(t)
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	// A hole under live data, made while no run is worth it: a large first
	// file deleted below a smaller one.
	h.lowerRule(reclaimRule{floor: 1 << 62, share: 1, budget: 64 << 20})
	id, _, _ := h.holed(t, "Hole", 1<<20, 256<<10)
	h.lowerRule(smallRule)
	paths, _ := h.sources(t, "few", 4, 4<<10)

	var during []bool
	h.rec.onEvent(func(name string, payload any) {
		c, ok := payload.(ArchiveChanged)
		if name != EventArchiveChanged || !ok || c.ID != id {
			return
		}
		h.c.mu.Lock()
		aid, _ := parseID(id)
		started := h.c.archives[aid].reclaiming
		h.c.mu.Unlock()
		during = append(during, started || h.reclaimRunning())
	})
	o := h.add(t, id, rootID, PolicySkip, paths...)
	h.rec.onEvent(nil)
	if o.Error != "" {
		t.Fatalf("the add: %+v", o)
	}
	// The final commit plans the run after its own archive.changed, so no
	// commit of the add, its last included, finds one planned or running.
	if fmt.Sprint(during) != "[false false false false]" {
		t.Fatalf("a reclaim planned or running at each commit: %v", during)
	}
	r := h.waitReclaim(t)
	if r.Error != "" || r.Returned == 0 {
		t.Fatalf("the run after the add: %+v", r)
	}
}

// AddFolder commits at its checkpoints as AddFiles does: the folders made
// along the way go with the next commit, so a folder is never listed before
// the commit that carries it, and the tree comes out whole — an empty folder
// included.
func TestAddFolderCommitsItsFoldersWithTheFiles(t *testing.T) {
	everyFile(t)
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Tree")
	h.src(t, "walk/a.txt", "a")
	h.src(t, "walk/sub1/b.txt", "bb")
	h.src(t, "walk/sub1/sub2/c.txt", "ccc")
	h.src(t, "walk/sub3/d.txt", "dddd")
	if err := os.MkdirAll(filepath.Join(h.dir, "src", "walk", "empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	before := h.stat(t, id)
	o := h.addFolder(t, id, rootID, filepath.Join(h.dir, "src", "walk"), PolicySkip)
	if o.Error != "" {
		t.Fatalf("the folder add: %+v", o)
	}
	outcomes := map[string]string{}
	for _, r := range o.Results {
		outcomes[r.Name] = r.Outcome
	}
	want := map[string]string{
		"walk": "created", "walk/a.txt": "added", "walk/empty": "created", "walk/sub1": "created",
		"walk/sub1/b.txt": "added", "walk/sub1/sub2": "created", "walk/sub1/sub2/c.txt": "added",
		"walk/sub3": "created", "walk/sub3/d.txt": "added",
	}
	if fmt.Sprint(outcomes) != fmt.Sprint(want) {
		t.Fatalf("outcomes: %v", outcomes)
	}
	// Four files: three checkpoints and the final commit.
	st := h.stat(t, id)
	if st.Seq != before.Seq+4 || st.Files != 4 || st.Records != 9 {
		t.Fatalf("the folder add: %+v, was %+v", st, before)
	}
	walk := h.row(t, id, rootID, "walk")
	sub1 := h.row(t, id, walk.ID, "sub1")
	sub2 := h.row(t, id, sub1.ID, "sub2")
	if r := h.row(t, id, sub2.ID, "c.txt"); r.Size != 3 {
		t.Fatalf("c.txt: %+v", r)
	}
	if p := h.page(t, id, h.row(t, id, walk.ID, "empty").ID); p.Total != 0 {
		t.Fatalf("the empty folder: %+v", p.Rows)
	}
}

// The plan's outcomes hold across checkpoints: a skip and a replace of what
// was there before, and a replace of a file this same batch added a
// checkpoint earlier — the plan's own item, by then a committed record,
// replaced in place with its id kept.
func TestSkipAndReplaceAcrossCheckpoints(t *testing.T) {
	everyFile(t)
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Policies")
	if o := h.add(t, id, rootID, PolicySkip, h.src(t, "old/x.txt", "x old"), h.src(t, "old/y.txt", "y old")); o.Error != "" {
		t.Fatalf("seed: %+v", o)
	}
	x := h.row(t, id, rootID, "x.txt")

	o := h.add(t, id, rootID, PolicySkip, h.src(t, "s/a.txt", "a"), h.src(t, "s/x.txt", "x new"), h.src(t, "s/b.txt", "b"))
	got := []string{}
	for _, r := range o.Results {
		got = append(got, r.Name+":"+r.Outcome)
	}
	if o.Error != "" || fmt.Sprint(got) != "[a.txt:added x.txt:skipped b.txt:added]" {
		t.Fatalf("under skip: %v %+v", got, o)
	}

	o = h.add(t, id, rootID, PolicyReplace,
		h.src(t, "r1/c.txt", "c"), h.src(t, "r1/x.txt", "x newer"), h.src(t, "r1/d.txt", "d"),
		h.src(t, "r1/dup.txt", "first"), h.src(t, "r2/dup.txt", "second, and longer"))
	got = got[:0]
	for _, r := range o.Results {
		got = append(got, r.Name+":"+r.Outcome)
	}
	if o.Error != "" || fmt.Sprint(got) != "[c.txt:added x.txt:replaced d.txt:added dup.txt:added dup.txt:replaced]" {
		t.Fatalf("under replace: %v %+v", got, o)
	}
	if o.Results[3].ID != o.Results[4].ID {
		t.Fatalf("the batch's own file was not replaced in place: %+v", o.Results)
	}
	oa := h.open(t, id)
	if r := h.row(t, id, rootID, "x.txt"); r.ID != x.ID {
		t.Fatalf("x.txt was not replaced in place: %+v, was %+v", r, x)
	} else if err := readsAs(oa, r.ID, []byte("x newer")); err != nil {
		t.Fatalf("x.txt: %v", err)
	}
	if err := readsAs(oa, h.row(t, id, rootID, "dup.txt").ID, []byte("second, and longer")); err != nil {
		t.Fatalf("dup.txt: %v", err)
	}
	h.reopen(t, id)
}

// R31's quarantine across a checkpoint: the transaction begun after a
// checkpoint must not write where that checkpoint freed — the replaced
// file's old extent, the index and the map before it — since the losing
// superblock still names them until the next commit. Taken at the second
// checkpoint, after the transaction it began wrote the next file and before
// its own commit, a copy of the file whose newer superblock is spoiled
// opens on the older one and reads the replaced file's old content intact:
// nothing the checkpoint freed was written over. The other copy, spoiled
// the other way, reads the new content.
func TestACheckpointsFreedExtentIsHeldForOneCommit(t *testing.T) {
	everyFile(t)
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Quarantine")
	oldBody := incompressible(t, 128<<10)
	if o := h.add(t, id, rootID, PolicySkip, h.src(t, "old/r.bin", string(oldBody))); o.Error != "" {
		t.Fatalf("seed: %+v", o)
	}
	rID := h.row(t, id, rootID, "r.bin").ID
	oa := h.open(t, id)
	newBody := bytes.Repeat([]byte{7}, 1)
	newBody = append(newBody, incompressible(t, 96<<10)...)
	// s1 fits the old extent and no other hole: the small holes the
	// commits before left are far shorter, so a pool that offered the old
	// extent would place s1 there.
	fits := incompressible(t, 100<<10)
	fits[0] = 9
	small := []string{h.src(t, "q/s1.bin", string(fits)), h.src(t, "q/s2.bin", "another")}

	var mu sync.Mutex
	var reads []string
	flips := 0
	atFlip(t, func() {
		flips++
		if flips != 2 {
			return
		}
		// The second checkpoint: the first published the replacement and
		// freed the old extent; s1 is written into the transaction since.
		got, err := h.losingCopies(oa, rID, oldBody, newBody)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			reads = append(reads, "error: "+err.Error())
			return
		}
		reads = got
	})
	o := h.add(t, id, rootID, PolicyReplace, append([]string{h.src(t, "new/r.bin", string(newBody))}, small...)...)
	if o.Error != "" || len(o.Results) != 3 {
		t.Fatalf("the add: %+v", o)
	}
	// Every outcome is checked, so that a pool which offered the
	// quarantined extent is named here — s1 refused as a write into a
	// retired extent — and not only by the superblocks below.
	for i, want := range []string{"replaced", "added", "added"} {
		if r := o.Results[i]; r.Outcome != want {
			t.Fatalf("outcome %d: %+v, want %s", i, r, want)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	// One copy names the state before the first checkpoint — the old
	// content — and the other the state after it.
	if fmt.Sprint(reads) != "[new old]" && fmt.Sprint(reads) != "[old new]" {
		t.Fatalf("the two superblocks read: %v", reads)
	}
	if err := readsAs(oa, rID, newBody); err != nil {
		t.Fatalf("r.bin: %v", err)
	}
	h.reopen(t, id)
}

// losingCopies copies the archive file twice, spoils superblock copy A in one
// and copy B in the other, opens each read-only on the keys the registry
// holds, and says what the record reads as in each: "old", "new", or why
// not.
func (h *harness) losingCopies(oa *openArchive, rID string, oldBody, newBody []byte) ([]string, error) {
	raw, err := os.ReadFile(oa.path)
	if err != nil {
		return nil, err
	}
	h.c.mu.Lock()
	sess, e := h.c.sessionLocked()
	if e != nil {
		h.c.mu.Unlock()
		return nil, e
	}
	rec := findRecord(sess.Registry(), oa.id)
	keys, e := h.c.archiveKeysLocked(rec)
	opts := h.c.archiveOptionsLocked(rec)
	h.c.mu.Unlock()
	if e != nil {
		return nil, e
	}
	defer zeroKeys(keys)
	opts.ReadOnly = true
	fid, _ := parseID(rID)
	var out []string
	for i, off := range []int{0x1000, 0x2000} {
		spoiled := bytes.Clone(raw)
		for j := 0; j < 64; j++ {
			spoiled[off+j] ^= 0xFF
		}
		p := filepath.Join(h.dir, fmt.Sprintf("copy%d.efd", i))
		if err := os.WriteFile(p, spoiled, 0o600); err != nil {
			return nil, err
		}
		a, err := archive.Open(p, keys, opts)
		if err != nil {
			out = append(out, "open: "+err.Error())
			continue
		}
		var buf bytes.Buffer
		err = a.Extract(context.Background(), fid, &buf)
		a.Close()
		switch {
		case err != nil:
			out = append(out, "read: "+err.Error())
		case bytes.Equal(buf.Bytes(), oldBody):
			out = append(out, "old")
		case bytes.Equal(buf.Bytes(), newBody):
			out = append(out, "new")
		default:
			out = append(out, "other content")
		}
	}
	return out, nil
}

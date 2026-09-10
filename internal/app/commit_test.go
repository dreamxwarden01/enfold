package app

import (
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dreamxwarden01/enfold/internal/format"
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

// Every operation that opens a transaction aborts it on any exit that is not
// a commit (APP.md §2.3). A panic inside the add — here thrown by the
// progress sink and recovered by the operation runner — used to leave the
// transaction open, and every later operation on that archive then answered
// archive.dirty until the handle was closed (the outside audit of
// 2026-09-09).
func TestAPanicInsideAnAddLeavesNoTransactionOpen(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Panic")

	big := filepath.Join(h.dir, "big.bin")
	if err := os.WriteFile(big, incompressible(t, 2<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	// Thrown on the operation's own goroutine, inside the add's read of its
	// source: the transaction is open and bytes are already in the file.
	h.rec.onEvent(func(name string, payload any) {
		if name != EventOpProgress {
			return
		}
		if o, ok := payload.(OpView); ok && o.Kind == "add" && o.Done > 0 {
			panic("the progress sink threw")
		}
	})
	opID, e := h.c.AddFiles(id, rootID, []string{big}, PolicySkip)
	if e != nil {
		t.Fatal(e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != CodeInternal {
		t.Fatalf("the panicking add: %+v", o)
	}
	h.rec.onEvent(nil)

	// Nothing was published, and the archive is clean between operations: the
	// next one opens a transaction of its own and commits it.
	if p := h.page(t, id, rootID); p.Total != 0 {
		t.Fatalf("the panicking add published rows: %+v", p.Rows)
	}
	if o := h.add(t, id, rootID, PolicySkip, h.src(t, "after.txt", "after")); o.Error != "" {
		t.Fatalf("the add after the panic: %+v", o)
	}
	if p := h.page(t, id, rootID); p.Total != 1 {
		t.Fatalf("after the panic and the add: %+v", p.Rows)
	}
}

// A cancel that arrives once the commit has been entered is not a cancel:
// the commit runs under a context no cancel reaches, so the change is
// published whatever the answer, and reporting it as cancelled would be a
// lie about what is in the file. CancelOp says the operation is being saved,
// and the operation's own result stays the real one (the outside audit of
// 2026-09-09).
func TestCancelDuringTheCommitIsRefused(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Committing")

	// archive.changed is emitted from inside the commit, on the operation's
	// own goroutine and before it finishes: a cancel taken there lands in the
	// window this is about.
	ids := make(chan string, 1)
	answers := make(chan *Error, 1)
	h.rec.onEvent(func(name string, payload any) {
		if name != EventArchiveChanged {
			return
		}
		select {
		case opID := <-ids:
			answers <- h.c.CancelOp(opID)
		default:
		}
	})
	opID, e := h.c.AddFiles(id, rootID, []string{h.src(t, "one.txt", "one")}, PolicySkip)
	if e != nil {
		t.Fatal(e)
	}
	ids <- opID
	o := h.rec.waitOp(t, opID)
	h.rec.onEvent(nil)
	select {
	case ans := <-answers:
		if ans == nil || ans.Code != CodeOpCommitting {
			t.Fatalf("a cancel taken during the commit answered %v", ans)
		}
	default:
		t.Fatal("no cancel was taken during the commit")
	}
	if o.Error != "" {
		t.Fatalf("the committed operation's result is not the real one: %+v", o)
	}
	if p := h.page(t, id, rootID); p.Total != 1 {
		t.Fatalf("the commit the cancel could not stop published nothing: %+v", p.Rows)
	}
	// The operation is over: a cancel after it is neither refused nor acted
	// on, as it never was.
	if e := h.c.CancelOp(opID); e != nil {
		t.Fatalf("a cancel after the operation ended: %v", e)
	}
}

// The other side of that handover: a cancel that arrives before the commit
// is entered wins, and the commit is refused rather than run under a context
// no cancel reaches. Both decisions are made under the state mutex, so there
// is no instant in which CancelOp answers nil — "it was cancelled" — while
// the operation goes on to publish (the outside audit of 2026-09-09, finding
// 3). The cancel here lands exactly in that window: after the operation's
// last look at its context, before it marks itself committing.
func TestACancelBeforeTheCommitIsEnteredWins(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Handover")

	atTheWindow, cancelled := make(chan struct{}), make(chan struct{})
	marked := make(chan bool, 1)
	opID := h.c.startOp("add", id, func(ctx context.Context, o *op) ([]FileOutcome, error) {
		close(atTheWindow) // the writing is done; the commit is not entered
		<-cancelled
		ok := o.markCommitting(ctx)
		marked <- ok
		if !ok {
			return nil, ctx.Err()
		}
		return nil, nil
	})
	<-atTheWindow
	if e := h.c.CancelOp(opID); e != nil {
		t.Fatalf("a cancel before the commit was entered: %v", e)
	}
	close(cancelled)
	if <-marked {
		t.Fatal("the commit was entered after a cancel had been answered")
	}
	if o := h.rec.waitOp(t, opID); o.Error != CodeOpCancelled {
		t.Fatalf("the cancelled operation: %+v", o)
	}
}

// Every operation that opens a transaction aborts it on any exit that is not
// a commit — a panic raised inside Commit itself included. The flag that
// tells the deferred guard there is nothing left to abort used to be set
// before the call, so a panic there escaped the guard: the transaction
// stayed open and every later operation on that archive answered
// archive.dirty until the handle was closed (the outside audit of
// 2026-09-09, finding 4).
func TestAPanicInsideTheCommitLeavesNoTransactionOpen(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "CommitPanic")
	aid, _ := parseID(id)
	h.c.mu.Lock()
	oa := h.c.archives[aid]
	h.c.mu.Unlock()
	if oa == nil {
		t.Fatal("the archive is not open")
	}

	// The panic is raised inside Tx.Commit, where no failure of the app's own
	// can put one: the nil context it is called with is dereferenced there
	// (archive/tx.go commit reads ctx.Err() first). What the transaction did
	// before it is a real change, so the commit is not the empty one.
	func() {
		oa.opMu.Lock()
		defer oa.opMu.Unlock()
		tx, e := h.c.beginOp(oa, nil)
		if e != nil {
			t.Fatal(e)
		}
		defer func() {
			if r := recover(); r == nil {
				t.Error("the commit did not panic")
			}
		}()
		defer tx.end() // the guard: any exit that is not a commit aborts
		if _, err := tx.tx.AddDir(format.RootID, "F", 0); err != nil {
			t.Fatal(err)
		}
		tx.commit(nil)
	}()

	// Nothing was published and no transaction was left open: the next
	// operation begins one of its own and commits it.
	if p := h.page(t, id, rootID); p.Total != 0 {
		t.Fatalf("the panicking commit published rows: %+v", p.Rows)
	}
	if o := h.add(t, id, rootID, PolicySkip, h.src(t, "after.txt", "after")); o.Error != "" {
		t.Fatalf("the add after the panicking commit: %+v", o)
	}
	if p := h.page(t, id, rootID); p.Total != 1 {
		t.Fatalf("after the panic and the add: %+v", p.Rows)
	}
}

// The temporary an extract builds into is a short hidden name beside the
// target, never the target's own name with a suffix: a record may carry the
// 255 UTF-16 code units R20 allows, which is what the volume it came from
// holds, and 255 plus a suffix is a name no volume will take — the file could
// not be extracted at all (the outside audit of 2026-09-09).
func TestExtractOfAName255UnitsLong(t *testing.T) {
	dir := t.TempDir()
	longest := strings.Repeat("n", format.MaxNameUnits)
	tmp, err := extractTempName(filepath.Join(dir, longest))
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Dir(tmp); got != dir {
		t.Fatalf("the temporary is not beside the target: %s", tmp)
	}
	base := filepath.Base(tmp)
	if len(base) > 64 || strings.Contains(base, longest) {
		t.Fatalf("the temporary carries the target's name: %q", base)
	}
	if again, err := extractTempName(filepath.Join(dir, longest)); err != nil || again == tmp {
		t.Fatalf("two temporaries for one target: %q %v", again, err)
	}

	// And end to end, where the volume takes such a name at all.
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Long")
	name := strings.Repeat("n", format.MaxNameUnits-4) + ".txt"
	src := filepath.Join(h.dir, "src", name)
	if err := os.MkdirAll(filepath.Dir(src), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("long"), 0o600); err != nil {
		t.Skipf("this volume refuses a name of %d units: %v", format.MaxNameUnits, err)
	}
	if o := h.add(t, id, rootID, PolicySkip, src); o.Error != "" {
		t.Fatalf("add: %+v", o)
	}
	out := outDir(t)
	opID, e := h.c.Extract(id, []string{rootID}, out, ExtractSkip)
	if e != nil {
		t.Fatal(e)
	}
	o := h.rec.waitOp(t, opID)
	if o.Error != "" || len(o.Results) != 1 || o.Results[0].Outcome != "extracted" {
		t.Fatalf("the extract of a %d-unit name: %+v", format.MaxNameUnits, o)
	}
	if b, err := os.ReadFile(filepath.Join(out, name)); err != nil || string(b) != "long" {
		t.Fatalf("what landed: %q %v", b, err)
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

// The strip counts (APP.md §3, DECISIONS 2026-09-09 "the strip says what it
// does"): OpView.Items is what the plan holds, so the operation strip says
// "Adding 3 files" rather than a phase word, and "Adding 1 file" in the
// singular. Folders are not among them — they are records the walk makes,
// not bytes it writes — and an operation that counts nothing carries zero.
func TestOpViewCountsTheFilesItPlans(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Counted")

	o := h.add(t, id, rootID, PolicySkip,
		h.src(t, "c1.txt", "one"), h.src(t, "c2.txt", "two"), h.src(t, "c3.txt", "three"))
	if o.Error != "" || o.Items != 3 {
		t.Fatalf("three files added: %+v", o)
	}
	// A folder of two files under one subfolder: two files, two folders.
	h.src(t, "tree/inner/d1.txt", "d")
	h.src(t, "tree/d2.txt", "e")
	o = h.addFolder(t, id, rootID, filepath.Join(h.dir, "src", "tree"), PolicySkip)
	if o.Error != "" || o.Items != 2 {
		t.Fatalf("a folder of two files: %+v", o)
	}
	// A replace is one file, in the singular.
	opID, e := h.c.ReplaceFile(id, h.row(t, id, rootID, "c1.txt").ID, h.src(t, "c1b.txt", "one again"))
	if e != nil {
		t.Fatal(e)
	}
	if o = h.rec.waitOp(t, opID); o.Error != "" || o.Items != 1 {
		t.Fatalf("replace: %+v", o)
	}
	// An extract counts what it will write: the five files, not the folders
	// it makes on the way.
	opID, e = h.c.Extract(id, []string{rootID}, outDir(t), ExtractSkip)
	if e != nil {
		t.Fatal(e)
	}
	if o = h.rec.waitOp(t, opID); o.Error != "" || o.Items != 5 {
		t.Fatalf("extract all: %+v", o)
	}
	// An operation with nothing to count says nothing.
	opID, e = h.c.Verify(id)
	if e != nil {
		t.Fatal(e)
	}
	if o = h.rec.waitOp(t, opID); o.Error != "" || o.Items != 0 {
		t.Fatalf("verify: %+v", o)
	}
}

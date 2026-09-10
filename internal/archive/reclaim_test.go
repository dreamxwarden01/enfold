package archive

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"

	"github.com/dreamxwarden01/enfold/internal/format"
)

// In-place compaction (FORMAT.md R40, APP.md §2.3 as amended on 2026-09-10,
// DECISIONS 2026-09-10): a live extent moved verbatim into a hole before it
// by an ordinary commit, the tail cut by the follow-up as it comes free.
// Every archive here stores raw (NoCompression), so that an extent's size is
// format.RawStoredSize of its plaintext and the layout a test builds is the
// one it reasons about; a hole is made by deleting a file once the files
// after it are in place, the delete's own follow-up commit having then
// trimmed the metadata above the last live byte. The crash seam of
// reclaim.go has failTrimAt's shape, so trim_test.go's builders arm it.

// extentOf is where a live file's data lies now, which FileInfo does not say.
func extentOf(t testing.TB, a *Archive, id [16]byte) extent {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	r := a.record(id)
	if r == nil {
		t.Fatalf("file %x is not live", id)
	}
	return extent{Off: r.DataOff, Len: r.StoredSize}
}

// spaces reads the three allocation sets under the lock.
func spaces(a *Archive) (free, retired, pool *space) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.free.clone(), a.retired.clone(), a.pool.clone()
}

// sameMoves compares two plans' moves, which a Publish must not change.
func sameMoves(a, b []Move) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// withMoveChunk lowers the copy's chunk for one test, so that a file of a
// few hundred KiB is walked in several chunks.
func withMoveChunk(t testing.TB, n uint64) {
	t.Helper()
	was := moveChunk
	moveChunk = n
	t.Cleanup(func() { moveChunk = was })
}

// A hole at the front and a file at the tail that fits it: the plan moves the
// file, the commit copies its ciphertext byte for byte, the record points at
// the copy, it reads back through Reader and Extract with every tag verifying,
// and the tail it left comes back with the follow-up commit.
func TestAMoveIsVerbatimAndGivesTheTailBack(t *testing.T) {
	a, fx := newFixture(t, Options{NoCompression: true})
	front := add(t, a, root, "front.bin", noise(100000, 400))
	keepData := noise(200000, 401)
	keep := add(t, a, root, "keep.bin", keepData)
	lastData := noise(50000, 402)
	last := add(t, a, root, "last.bin", lastData)
	if _, err := a.Delete(ctx, front.ID); err != nil {
		t.Fatal(err)
	}
	before, _, _ := a.Stat()
	from := extentOf(t, a, last.ID)
	cipher := snapshot(t, fx.path)[from.Off:end(from)]

	plan := a.PlanReclaim(0)
	if len(plan.Moves) != 1 || plan.Moves[0].ID != last.ID || plan.Moves[0].From != from {
		t.Fatalf("plan %+v; expected one move of last.bin from %+v", plan, from)
	}
	m := plan.Moves[0]
	if m.To.Len != from.Len || end(m.To) > from.Off {
		t.Fatalf("the destination %+v is not a hole of the source's length wholly before %+v", m.To, from)
	}
	if plan.BytesToMove != from.Len || plan.NeedsPublish {
		t.Errorf("the plan reports %d bytes to move (want %d), NeedsPublish=%v", plan.BytesToMove, from.Len, plan.NeedsPublish)
	}
	if plan.TailReturned < from.Len || plan.TailReturned > from.Len+metadataSlack {
		t.Errorf("TailReturned %d; the moved file is %d bytes", plan.TailReturned, from.Len)
	}

	seq := a.Seq()
	var calls int
	var lastDone uint64
	rec, err := a.MoveExtents(ctx, plan.Moves, func(done, total uint64) {
		calls++
		lastDone = done
		if total != from.Len {
			t.Errorf("progress total %d, the move is %d bytes", total, from.Len)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls == 0 || lastDone != from.Len {
		t.Errorf("progress: %d calls, last %d of %d", calls, lastDone, from.Len)
	}
	if a.Seq() != seq+2 {
		t.Errorf("the move and its follow-up advanced the sequence by %d", a.Seq()-seq)
	}
	after, _, _ := a.Stat()
	if rec.Seq != a.Seq() || rec.Size != after {
		t.Errorf("receipt %+v, archive at seq %d size %d", rec, a.Seq(), after)
	}
	if after > before-plan.TailReturned || after+metadataSlack < before-plan.TailReturned {
		t.Errorf("the file went %d → %d; the plan promised %d back", before, after, plan.TailReturned)
	}
	if s := sizeOnDisk(t, fx.path); s != after {
		t.Errorf("the archive says %d bytes, the file is %d", after, s)
	}
	if got := extentOf(t, a, last.ID); got != m.To {
		t.Errorf("the record lies at %+v, the move went to %+v", got, m.To)
	}
	if !bytes.Equal(snapshot(t, fx.path)[m.To.Off:end(m.To)], cipher) {
		t.Error("the copy is not the ciphertext byte for byte")
	}
	if got := extract(t, a, last.ID); !bytes.Equal(got, lastData) {
		t.Error("the moved file through Extract")
	}
	if got := extract(t, a, keep.ID); !bytes.Equal(got, keepData) {
		t.Error("the file that stayed")
	}
	r, err := a.OpenReader(last.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.e != m.To {
		t.Errorf("a new reader holds %+v, the copy is at %+v", r.e, m.To)
	}
	if got, err := io.ReadAll(r); err != nil || !bytes.Equal(got, lastData) {
		t.Errorf("the moved file through a Reader: %v", err)
	}
	r.Close()
	insideTheFile(t, a)
	disjoint(t, a)
	if p := a.PlanReclaim(0); len(p.Moves) != 0 || p.TailReturned != 0 {
		t.Errorf("after the run the plan still wants %+v", p)
	}
	if h, err := a.Hash(ctx); err != nil || h == [32]byte{} {
		t.Errorf("hash after the move: %v", err)
	}

	// The file itself, reopened: the moved state, every file readable.
	b := reread(t, fx)
	if b.Stale() != nil || b.FreeMapRebuilt() != nil {
		t.Errorf("reopened with damage: stale=%v map=%v", b.Stale(), b.FreeMapRebuilt())
	}
	if got := extentOf(t, b, last.ID); got != m.To {
		t.Errorf("reopened, the record lies at %+v, the move went to %+v", got, m.To)
	}
	if got := extract(t, b, last.ID); !bytes.Equal(got, lastData) {
		t.Error("the moved file, reopened")
	}
	if got := extract(t, b, keep.ID); !bytes.Equal(got, keepData) {
		t.Error("the file that stayed, reopened")
	}
	insideTheFile(t, b)
	disjoint(t, b)
}

// A file larger than every hole before it is skipped, the run going on to
// the next (R40): the small file behind it moves, the big one stays.
func TestThePlanSkipsWhatNoHoleBeforeItHolds(t *testing.T) {
	a, _ := newFixture(t, Options{NoCompression: true})
	hole := add(t, a, root, "hole.bin", noise(64<<10, 410))
	bigData := noise(1<<20, 411)
	big := add(t, a, root, "big.bin", bigData)
	smallData := noise(32<<10, 412)
	small := add(t, a, root, "small.bin", smallData)
	if _, err := a.Delete(ctx, hole.ID); err != nil {
		t.Fatal(err)
	}
	bigAt := extentOf(t, a, big.ID)
	smallAt := extentOf(t, a, small.ID)

	plan := a.PlanReclaim(0)
	if len(plan.Moves) != 1 || plan.Moves[0].ID != small.ID {
		t.Fatalf("plan %+v; expected the small file alone to move", plan.Moves)
	}
	m := plan.Moves[0]
	if m.From != smallAt || m.To.Off != format.ArchiveDataStart || m.To.Len != smallAt.Len {
		t.Errorf("move %+v; expected %+v into the hole at the front", m, smallAt)
	}
	if plan.BytesToMove != smallAt.Len {
		t.Errorf("BytesToMove %d, the small file is %d", plan.BytesToMove, smallAt.Len)
	}
	if plan.TailReturned < smallAt.Len || plan.TailReturned > smallAt.Len+metadataSlack {
		t.Errorf("TailReturned %d; the file behind the big one is %d bytes", plan.TailReturned, smallAt.Len)
	}
	if plan.NeedsPublish {
		t.Error("the delete's follow-up commit published the hole; nothing is quarantined")
	}
	if _, err := a.MoveExtents(ctx, plan.Moves, nil); err != nil {
		t.Fatal(err)
	}
	if got := extentOf(t, a, big.ID); got != bigAt {
		t.Errorf("the big file moved from %+v to %+v", bigAt, got)
	}
	if got := extentOf(t, a, small.ID); got != m.To {
		t.Errorf("the small file lies at %+v, not at %+v", got, m.To)
	}
	if got := extract(t, a, big.ID); !bytes.Equal(got, bigData) {
		t.Error("the big file")
	}
	if got := extract(t, a, small.ID); !bytes.Equal(got, smallData) {
		t.Error("the small file")
	}
	insideTheFile(t, a)
	disjoint(t, a)
}

// A hole filled behind a file that cannot move returns nothing: the floor
// measures bytes the file system gets back, not the size of any hole (the
// outside critique of 2026-09-10). The small file still moves — the plan is
// honest about the moves and about what they give — and the commit leaves
// the file no shorter than its own metadata allows.
func TestAHoleBehindAnUnmovableTailReturnsNothing(t *testing.T) {
	a, _ := newFixture(t, Options{NoCompression: true})
	hole := add(t, a, root, "hole.bin", noise(64<<10, 420))
	smallData := noise(16<<10, 421)
	small := add(t, a, root, "small.bin", smallData)
	bigData := noise(1<<20, 422)
	big := add(t, a, root, "big.bin", bigData)
	if _, err := a.Delete(ctx, hole.ID); err != nil {
		t.Fatal(err)
	}
	before, _, _ := a.Stat()
	plan := a.PlanReclaim(0)
	if len(plan.Moves) != 1 || plan.Moves[0].ID != small.ID {
		t.Fatalf("plan %+v; expected the small file alone to move", plan.Moves)
	}
	if plan.TailReturned != 0 {
		t.Errorf("TailReturned %d behind a tail file no hole holds", plan.TailReturned)
	}
	// And nothing at run level either: no number of commits moves the file
	// that anchors the tail, so the hole the small one leaves behind is worth
	// nothing however long the run goes on. That is the figure the caller's
	// floor measures (APP.md §2.3): bytes returned, never the size of a hole.
	if plan.RunTailReturned != 0 {
		t.Errorf("RunTailReturned %d behind a tail file no hole holds", plan.RunTailReturned)
	}
	if plan.RunBytesToMove != plan.BytesToMove || plan.Commits != 1 {
		t.Errorf("the run moves %d bytes over %d commits; the one move is all there is", plan.RunBytesToMove, plan.Commits)
	}
	if _, err := a.MoveExtents(ctx, plan.Moves, nil); err != nil {
		t.Fatal(err)
	}
	if after, _, _ := a.Stat(); after+metadataSlack < before {
		t.Errorf("the file went %d → %d with the tail file in place", before, after)
	}
	if got := runReclaim(t, a); got != 0 {
		t.Errorf("the run went on for %d more commits behind the unmovable tail", got)
	}
	if after, _, _ := a.Stat(); after+metadataSlack < before {
		t.Errorf("the file went %d → %d over the whole run", before, after)
	}
	if got := extract(t, a, small.ID); !bytes.Equal(got, smallData) {
		t.Error("the small file")
	}
	if got := extract(t, a, big.ID); !bytes.Equal(got, bigData) {
		t.Error("the big file")
	}
	insideTheFile(t, a)
	disjoint(t, a)
}

// An extent a Reader holds is left where it lies for the run, and the plan
// made after the Reader closes includes it (APP.md §2.3).
func TestThePlanLeavesAHeldSourceForTheRun(t *testing.T) {
	a, _ := newFixture(t, Options{NoCompression: true})
	hole := add(t, a, root, "hole.bin", noise(64<<10, 430))
	small := add(t, a, root, "small.bin", noise(16<<10, 431))
	add(t, a, root, "big.bin", noise(1<<20, 432))
	if _, err := a.Delete(ctx, hole.ID); err != nil {
		t.Fatal(err)
	}
	r, err := a.OpenReader(small.ID)
	if err != nil {
		t.Fatal(err)
	}
	if plan := a.PlanReclaim(0); len(plan.Moves) != 0 || plan.BytesToMove != 0 {
		t.Errorf("the plan moves %+v while a reader holds the only movable file", plan.Moves)
	}
	r.Close()
	plan := a.PlanReclaim(0)
	if len(plan.Moves) != 1 || plan.Moves[0].ID != small.ID {
		t.Errorf("after the reader closed the plan moves %+v", plan.Moves)
	}
}

// A hole freed by the commit just made, with no tail follow-up to publish it,
// is still under R31's quarantine: the plan says NeedsPublish, MoveExtents
// refuses the plan whole and writes nothing, and Publish — the empty commit
// — makes the same moves possible.
func TestAHoleStillInQuarantineNeedsPublishFirst(t *testing.T) {
	a, _ := newFixture(t, Options{NoCompression: true})
	gone := add(t, a, root, "gone.bin", noise(120000, 440))
	bData := noise(100000, 441)
	b := add(t, a, root, "b.bin", bData)
	cData := noise(100000, 442)
	c := add(t, a, root, "c.bin", cData)
	goneAt := extentOf(t, a, gone.ID)
	// One commit that frees the interior extent and appends a new file: the
	// tail is live, so nothing is trimmed and the hole stays quarantined.
	tx, err := a.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Delete(gone.ID); err != nil {
		t.Fatal(err)
	}
	dData := noise(40000, 443)
	d, err := tx.Add(ctx, root, "d.bin", bytes.NewReader(dData), int64(len(dData)))
	if err != nil {
		t.Fatal(err)
	}
	seq := a.Seq()
	if _, err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if a.Seq() != seq+1 {
		t.Fatalf("the commit gave a tail back (%d commits): the quarantine is not what this test is watching", a.Seq()-seq)
	}
	_, retired, _ := spaces(a)
	if !retired.intersects(goneAt) {
		t.Fatal("the deleted extent is not quarantined")
	}

	plan := a.PlanReclaim(0)
	if !plan.NeedsPublish {
		t.Fatalf("plan %+v wants a quarantined hole and does not say so", plan)
	}
	if len(plan.Moves) != 1 || plan.Moves[0].ID != b.ID || !overlaps(plan.Moves[0].To, goneAt) {
		t.Fatalf("plan %+v; expected b.bin into the deleted file's hole %+v", plan.Moves, goneAt)
	}
	bAt := extentOf(t, a, b.ID)
	before, _, _ := a.Stat()
	seq = a.Seq()
	if _, err := a.MoveExtents(ctx, plan.Moves, nil); !errors.Is(err, ErrStalePlan) {
		t.Fatalf("a move into a quarantined hole: %v", err)
	}
	if size, _, _ := a.Stat(); size != before || a.Seq() != seq {
		t.Errorf("the refused move changed the file: size %d → %d, seq %d → %d", before, size, seq, a.Seq())
	}
	if got := extentOf(t, a, b.ID); got != bAt {
		t.Errorf("the refused move moved b.bin: %+v → %+v", bAt, got)
	}
	if tx, err := a.Begin(); err != nil {
		t.Fatalf("a transaction after the refused move: %v", err)
	} else {
		tx.Abort()
	}

	rec, err := a.Publish(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Seq != a.Seq() || a.Seq() <= seq {
		t.Errorf("publish: receipt %+v, archive at seq %d (was %d)", rec, a.Seq(), seq)
	}
	if _, retired, _ := spaces(a); retired.intersects(goneAt) {
		t.Error("the deleted extent is still quarantined after Publish")
	}
	again := a.PlanReclaim(0)
	if again.NeedsPublish {
		t.Error("the plan still wants a Publish after one")
	}
	if !sameMoves(again.Moves, plan.Moves) {
		t.Errorf("the moves changed under Publish: %+v → %+v", plan.Moves, again.Moves)
	}
	if _, err := a.MoveExtents(ctx, again.Moves, nil); err != nil {
		t.Fatal(err)
	}
	if got := extentOf(t, a, b.ID); got != again.Moves[0].To {
		t.Errorf("b.bin lies at %+v, the move went to %+v", got, again.Moves[0].To)
	}
	for _, f := range []struct {
		id   [16]byte
		data []byte
	}{{b.ID, bData}, {c.ID, cData}, {d.ID, dData}} {
		if got := extract(t, a, f.id); !bytes.Equal(got, f.data) {
			t.Errorf("file %x after the run", f.id)
		}
	}
	insideTheFile(t, a)
	disjoint(t, a)
}

// A crash after the copy and before the commit began: the handle aborts and
// the file, reopened, is the original state — the record where it was, the
// copy's bytes free space that the next commit writes over, nothing
// truncated — and the same run then goes through.
func TestACrashBetweenTheCopyAndTheCommitLeavesTheOriginals(t *testing.T) {
	a, fx := newFixture(t, Options{NoCompression: true})
	front := add(t, a, root, "front.bin", noise(100000, 450))
	keepData := noise(200000, 451)
	keep := add(t, a, root, "keep.bin", keepData)
	lastData := noise(50000, 452)
	last := add(t, a, root, "last.bin", lastData)
	if _, err := a.Delete(ctx, front.ID); err != nil {
		t.Fatal(err)
	}
	before, _, _ := a.Stat()
	seq := a.Seq()
	from := extentOf(t, a, last.ID)
	cipher := snapshot(t, fx.path)[from.Off:end(from)]
	plan := a.PlanReclaim(0)
	if len(plan.Moves) != 1 {
		t.Fatalf("plan %+v", plan)
	}
	m := plan.Moves[0]

	var crashed []byte
	failMoveAt = func(step int, b []byte, off uint64) (int, bool) {
		if step != 2 {
			return 0, false
		}
		crashed = snapshot(t, fx.path)
		return 0, true
	}
	_, err := a.MoveExtents(ctx, plan.Moves, nil)
	failMoveAt = nil
	if !errors.Is(err, errTornWrite) {
		t.Fatalf("a crash before the commit: %v", err)
	}
	// The handle: nothing published, nothing grown, no transaction left open.
	if a.Seq() != seq || a.Broken() != nil {
		t.Errorf("seq %d (was %d), broken=%v", a.Seq(), seq, a.Broken())
	}
	if size, _, _ := a.Stat(); size != before {
		t.Errorf("the aborted move left the file at %d; it was %d", size, before)
	}
	if got := extentOf(t, a, last.ID); got != from {
		t.Errorf("the aborted move moved the record: %+v → %+v", from, got)
	}
	if tx, err := a.Begin(); err != nil {
		t.Fatalf("a transaction after the abort: %v", err)
	} else {
		tx.Abort()
	}
	if got := extract(t, a, last.ID); !bytes.Equal(got, lastData) {
		t.Error("the original, on the handle")
	}
	// The copy did land, where the committed map holds free space.
	if !bytes.Equal(crashed[m.To.Off:end(m.To)], cipher) {
		t.Fatal("the copy is not on the file at the destination")
	}

	// The file as the crash left it, reopened.
	path := filepath.Join(t.TempDir(), "crashed.efd")
	restore(t, path, crashed)
	c, err := Open(path, []Key{fx.key}, fx.opts)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Seq() != seq || c.Stale() != nil || c.FreeMapRebuilt() != nil {
		t.Errorf("reopened: seq %d (want %d) stale=%v map=%v", c.Seq(), seq, c.Stale(), c.FreeMapRebuilt())
	}
	if got := extentOf(t, c, last.ID); got != from {
		t.Errorf("reopened, the record lies at %+v; it was at %+v", got, from)
	}
	if free, _, _ := spaces(c); !free.contains(m.To) {
		t.Errorf("the copy's bytes at %+v are not free space", m.To)
	}
	if s := sizeOnDisk(t, path); s != before {
		t.Errorf("the crashed file is %d bytes; it was %d, and nothing may be truncated", s, before)
	}
	if got := extract(t, c, last.ID); !bytes.Equal(got, lastData) {
		t.Error("the original, reopened")
	}
	if got := extract(t, c, keep.ID); !bytes.Equal(got, keepData) {
		t.Error("the file that stayed, reopened")
	}
	insideTheFile(t, c)
	disjoint(t, c)

	// The copy's bytes are nobody's: the next commit writes over them, and
	// the run goes on from there.
	addedData := noise(30000, 453)
	added := add(t, c, root, "added.bin", addedData)
	if at := extentOf(t, c, added.ID); !overlaps(at, m.To) {
		t.Errorf("the add landed at %+v, not over the abandoned copy at %+v", at, m.To)
	}
	again := c.PlanReclaim(0)
	if len(again.Moves) != 1 || again.Moves[0].ID != last.ID {
		t.Fatalf("the plan after the crash: %+v", again.Moves)
	}
	if _, err := c.MoveExtents(ctx, again.Moves, nil); err != nil {
		t.Fatal(err)
	}
	for _, f := range []struct {
		id   [16]byte
		data []byte
	}{{keep.ID, keepData}, {last.ID, lastData}, {added.ID, addedData}} {
		if got := extract(t, c, f.id); !bytes.Equal(got, f.data) {
			t.Errorf("file %x after the run", f.id)
		}
	}
	insideTheFile(t, c)
	disjoint(t, c)
}

// A write of the copy that tore — the disk taking half a chunk and failing —
// aborts the move: the originals are live, the file is what it was, the torn
// bytes lie in free space, and the same plan then goes through.
func TestATornCopyAbortsTheMove(t *testing.T) {
	withMoveChunk(t, 64<<10)
	a, fx := newFixture(t, Options{NoCompression: true})
	front := add(t, a, root, "front.bin", noise(300000, 460))
	keepData := noise(400000, 461) // larger than the hole: it stays, and the file behind it is the one move
	keep := add(t, a, root, "keep.bin", keepData)
	lastData := noise(200000, 462)
	last := add(t, a, root, "last.bin", lastData)
	if _, err := a.Delete(ctx, front.ID); err != nil {
		t.Fatal(err)
	}
	before, _, _ := a.Stat()
	seq := a.Seq()
	from := extentOf(t, a, last.ID)
	plan := a.PlanReclaim(0)
	if len(plan.Moves) != 1 || plan.Moves[0].From != from {
		t.Fatalf("plan %+v", plan)
	}

	var tore landing
	failMoveAt = tearTrimAt(1, 2, &tore)
	_, err := a.MoveExtents(ctx, plan.Moves, nil)
	failMoveAt = nil
	if !errors.Is(err, errTornWrite) {
		t.Fatalf("a torn copy: %v", err)
	}
	if a.Seq() != seq || a.Broken() != nil {
		t.Errorf("seq %d (was %d), broken=%v", a.Seq(), seq, a.Broken())
	}
	if size, _, _ := a.Stat(); size != before {
		t.Errorf("the aborted move left the file at %d; it was %d", size, before)
	}
	if s := sizeOnDisk(t, fx.path); s != before {
		t.Errorf("the file is %d bytes on disk; it was %d", s, before)
	}
	if got := extentOf(t, a, last.ID); got != from {
		t.Errorf("the aborted move moved the record: %+v → %+v", from, got)
	}
	if got := extract(t, a, last.ID); !bytes.Equal(got, lastData) {
		t.Error("the original after the torn copy")
	}
	tore.on(t, fx.path)
	if tore.off < plan.Moves[0].To.Off || tore.off+uint64(len(tore.b)) > end(plan.Moves[0].To) {
		t.Errorf("the torn write landed at %d, outside the destination %+v", tore.off, plan.Moves[0].To)
	}
	b := reread(t, fx)
	if got := extract(t, b, last.ID); !bytes.Equal(got, lastData) {
		t.Error("the original, reopened")
	}
	if free, _, _ := spaces(b); !free.contains(plan.Moves[0].To) {
		t.Error("the torn bytes are not in free space")
	}
	insideTheFile(t, b)
	disjoint(t, b)
	b.Close()

	again := a.PlanReclaim(0)
	if !sameMoves(again.Moves, plan.Moves) {
		t.Errorf("the plan changed under an aborted move: %+v → %+v", plan.Moves, again.Moves)
	}
	if _, err := a.MoveExtents(ctx, again.Moves, nil); err != nil {
		t.Fatal(err)
	}
	if got := extract(t, a, last.ID); !bytes.Equal(got, lastData) {
		t.Error("the moved file")
	}
	if got := extract(t, a, keep.ID); !bytes.Equal(got, keepData) {
		t.Error("the file that stayed")
	}
	if after, _, _ := a.Stat(); after >= before {
		t.Errorf("the file went %d → %d", before, after)
	}
	insideTheFile(t, a)
	disjoint(t, a)
}

// A cancel is honoured per chunk of the copy, not per extent (R40): the first
// chunk is the last, the transaction aborts, the originals are live and the
// file is what it was.
func TestACancelInsideACopyLeavesTheOriginals(t *testing.T) {
	withMoveChunk(t, 64<<10)
	a, fx := newFixture(t, Options{NoCompression: true})
	front := add(t, a, root, "front.bin", noise(300000, 470))
	keep := add(t, a, root, "keep.bin", noise(400000, 471)) // larger than the hole: it stays
	lastData := noise(200000, 472)
	last := add(t, a, root, "last.bin", lastData)
	if _, err := a.Delete(ctx, front.ID); err != nil {
		t.Fatal(err)
	}
	before, _, _ := a.Stat()
	seq := a.Seq()
	from := extentOf(t, a, last.ID)
	plan := a.PlanReclaim(0)
	if len(plan.Moves) != 1 {
		t.Fatalf("plan %+v", plan)
	}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var calls int
	_, err := a.MoveExtents(cctx, plan.Moves, func(done, total uint64) {
		calls++
		cancel()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancel inside the copy: %v", err)
	}
	if calls != 1 {
		t.Errorf("the copy went on for %d chunks after the cancel", calls)
	}
	if a.Seq() != seq || a.Broken() != nil {
		t.Errorf("seq %d (was %d), broken=%v", a.Seq(), seq, a.Broken())
	}
	if size, _, _ := a.Stat(); size != before {
		t.Errorf("the cancelled move left the file at %d; it was %d", size, before)
	}
	if s := sizeOnDisk(t, fx.path); s != before {
		t.Errorf("the file is %d bytes on disk; it was %d", s, before)
	}
	if got := extentOf(t, a, last.ID); got != from {
		t.Errorf("the cancelled move moved the record: %+v → %+v", from, got)
	}
	if got := extract(t, a, last.ID); !bytes.Equal(got, lastData) {
		t.Error("the original after the cancel")
	}
	if again := a.PlanReclaim(0); !sameMoves(again.Moves, plan.Moves) {
		t.Errorf("the plan changed under a cancelled move: %+v → %+v", plan.Moves, again.Moves)
	}
	b := reread(t, fx)
	if got := extract(t, b, last.ID); !bytes.Equal(got, lastData) {
		t.Error("the original, reopened")
	}
	if got := extract(t, b, keep.ID); got == nil {
		t.Error("the file that stayed, reopened")
	}
	insideTheFile(t, b)
	disjoint(t, b)
}

// A crash right after the flip — the follow-up commit never began: the moved
// state is durable, the source quarantined (published free, not allocatable
// until the commit after next), nothing truncated; R31's fallback over it is
// the original state with the source still readable; and the next commit
// gives the tail back.
func TestACrashAfterTheFlipLeavesTheMovedStateAndTheTail(t *testing.T) {
	a, fx := newFixture(t, Options{NoCompression: true})
	front := add(t, a, root, "front.bin", noise(100000, 480))
	keepData := noise(200000, 481)
	keep := add(t, a, root, "keep.bin", keepData)
	lastData := noise(50000, 482)
	last := add(t, a, root, "last.bin", lastData)
	if _, err := a.Delete(ctx, front.ID); err != nil {
		t.Fatal(err)
	}
	before, _, _ := a.Stat()
	seq := a.Seq()
	from := extentOf(t, a, last.ID)
	plan := a.PlanReclaim(0)
	if len(plan.Moves) != 1 {
		t.Fatalf("plan %+v", plan)
	}
	m := plan.Moves[0]

	failTrimAt = skipTrimAt(1)
	_, err := a.MoveExtents(ctx, plan.Moves, nil)
	failTrimAt = nil
	if err != nil {
		t.Fatal(err)
	}
	if a.Seq() != seq+1 || a.Broken() != nil {
		t.Fatalf("seq %d (was %d), broken=%v", a.Seq(), seq, a.Broken())
	}
	size, _, _ := a.Stat()
	if size < before || size > before+metadataSlack {
		t.Errorf("the file went %d → %d over a move whose follow-up did not begin", before, size)
	}
	if s := sizeOnDisk(t, fx.path); s != size {
		t.Errorf("the archive says %d bytes, the file is %d", size, s)
	}
	if got := extentOf(t, a, last.ID); got != m.To {
		t.Errorf("the record lies at %+v, the move went to %+v", got, m.To)
	}
	free, retired, pool := spaces(a)
	if !free.contains(from) || !retired.intersects(from) || pool.intersects(from) {
		t.Errorf("the source %+v: free=%v retired=%v pool=%v; want published free and quarantined", from, free.contains(from), retired.intersects(from), pool.intersects(from))
	}
	if got := extract(t, a, last.ID); !bytes.Equal(got, lastData) {
		t.Error("the moved file")
	}
	insideTheFile(t, a)
	disjoint(t, a)

	// Reopened: the moved state, the source quarantined there too — the
	// losing copy still names it.
	b := reread(t, fx)
	if b.Seq() != seq+1 || b.Stale() != nil || b.FreeMapRebuilt() != nil {
		t.Errorf("reopened: seq %d (want %d) stale=%v map=%v", b.Seq(), seq+1, b.Stale(), b.FreeMapRebuilt())
	}
	if got := extentOf(t, b, last.ID); got != m.To {
		t.Errorf("reopened, the record lies at %+v, the move went to %+v", got, m.To)
	}
	if _, retired, _ := spaces(b); !retired.intersects(from) {
		t.Error("reopened, the source is not quarantined")
	}
	if got := extract(t, b, last.ID); !bytes.Equal(got, lastData) {
		t.Error("the moved file, reopened")
	}
	insideTheFile(t, b)
	disjoint(t, b)
	b.Close()

	// R31's fallback: the live copy damaged, the file opens one commit
	// behind — the original state, whose file at the source still reads.
	img := snapshot(t, fx.path)
	sbA, err := format.DecodeArchiveSuperblock(img[format.CopyA.ArchiveSuperblockOff() : format.CopyA.ArchiveSuperblockOff()+format.SuperblockSize])
	if err != nil {
		t.Fatal(err)
	}
	sbB, err := format.DecodeArchiveSuperblock(img[format.CopyB.ArchiveSuperblockOff() : format.CopyB.ArchiveSuperblockOff()+format.SuperblockSize])
	if err != nil {
		t.Fatal(err)
	}
	liveOff := format.CopyA.ArchiveSuperblockOff()
	if sbB.Seq > sbA.Seq {
		liveOff = format.CopyB.ArchiveSuperblockOff()
	}
	img[liveOff+200] ^= 1
	path := filepath.Join(t.TempDir(), "torn.efd")
	restore(t, path, img)
	c, err := Open(path, []Key{fx.key}, fx.opts)
	if err != nil {
		t.Fatal(err)
	}
	if c.Stale() == nil || c.Seq() != seq {
		t.Errorf("the fallback: stale=%v seq %d (want %d)", c.Stale(), c.Seq(), seq)
	}
	if got := extentOf(t, c, last.ID); got != from {
		t.Errorf("the fallback state has the record at %+v; it was at %+v", got, from)
	}
	if got := extract(t, c, last.ID); !bytes.Equal(got, lastData) {
		t.Error("the fallback state's file does not read: the quarantine did not hold")
	}
	if got := extract(t, c, keep.ID); !bytes.Equal(got, keepData) {
		t.Error("the fallback state's other file")
	}
	c.Close()

	// The next commit on the handle finds the tail and gives it back.
	if _, err := a.Rename(ctx, keep.ID, "kept.bin"); err != nil {
		t.Fatal(err)
	}
	after, _, _ := a.Stat()
	if after > before-from.Len {
		t.Errorf("the next commit did not reclaim the tail: %d, the file was %d before a %d-byte file moved down", after, before, from.Len)
	}
	if s := sizeOnDisk(t, fx.path); s != after {
		t.Errorf("the archive says %d bytes, the file is %d", after, s)
	}
	if got := extract(t, a, last.ID); !bytes.Equal(got, lastData) {
		t.Error("the moved file, after the tail came back")
	}
	if got := extract(t, a, keep.ID); !bytes.Equal(got, keepData) {
		t.Error("the file that stayed, after the tail came back")
	}
	insideTheFile(t, a)
	disjoint(t, a)
}

// One commit moves several extents, each to its own hole (R40); the tail the
// last of them left comes back with the follow-up; and the whole-file
// Compact still works over the moved layout.
func TestTwoMovesInOneCommitThenCompact(t *testing.T) {
	a, fx := newFixture(t, Options{NoCompression: true})
	first := add(t, a, root, "first.bin", noise(60000, 490))
	wallData := noise(200000, 491) // no hole before it holds it: the wall between the two holes
	wall := add(t, a, root, "wall.bin", wallData)
	second := add(t, a, root, "second.bin", noise(60000, 492))
	cData := noise(50000, 493)
	c := add(t, a, root, "c.bin", cData)
	dData := noise(50000, 494)
	d := add(t, a, root, "d.bin", dData)
	tx, err := a.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Delete(first.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Delete(second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	before, _, _ := a.Stat()
	wallAt := extentOf(t, a, wall.ID)

	plan := a.PlanReclaim(0)
	if len(plan.Moves) != 2 || plan.Moves[0].ID != c.ID || plan.Moves[1].ID != d.ID {
		t.Fatalf("plan %+v; expected c.bin then d.bin", plan.Moves)
	}
	mc, md := plan.Moves[0], plan.Moves[1]
	if overlaps(mc.To, md.To) || end(mc.To) > wallAt.Off || md.To.Off < end(wallAt) {
		t.Fatalf("the moves %+v and %+v are not one into each hole around the wall %+v", mc.To, md.To, wallAt)
	}
	if plan.BytesToMove != mc.From.Len+md.From.Len {
		t.Errorf("BytesToMove %d", plan.BytesToMove)
	}
	seq := a.Seq()
	if _, err := a.MoveExtents(ctx, plan.Moves, nil); err != nil {
		t.Fatal(err)
	}
	if a.Seq() != seq+2 {
		t.Errorf("two moves and the follow-up advanced the sequence by %d", a.Seq()-seq)
	}
	after, _, _ := a.Stat()
	if after > before-plan.TailReturned || after+metadataSlack < before-plan.TailReturned {
		t.Errorf("the file went %d → %d; the plan promised %d back", before, after, plan.TailReturned)
	}
	if got := extentOf(t, a, c.ID); got != mc.To {
		t.Errorf("c.bin lies at %+v, the move went to %+v", got, mc.To)
	}
	if got := extentOf(t, a, d.ID); got != md.To {
		t.Errorf("d.bin lies at %+v, the move went to %+v", got, md.To)
	}
	if got := extentOf(t, a, wall.ID); got != wallAt {
		t.Errorf("the wall moved: %+v → %+v", wallAt, got)
	}
	files := []struct {
		id   [16]byte
		data []byte
	}{{wall.ID, wallData}, {c.ID, cData}, {d.ID, dData}}
	for _, f := range files {
		if got := extract(t, a, f.id); !bytes.Equal(got, f.data) {
			t.Errorf("file %x after the moves", f.id)
		}
	}
	insideTheFile(t, a)
	disjoint(t, a)
	b := reread(t, fx)
	for _, f := range files {
		if got := extract(t, b, f.id); !bytes.Equal(got, f.data) {
			t.Errorf("file %x after the moves, reopened", f.id)
		}
	}
	insideTheFile(t, b)
	disjoint(t, b)
	b.Close()

	// The whole-file Compact over the moved layout.
	hash, size, err := a.Compact(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := fx.open(t)
	if h, err := e.Hash(ctx); err != nil || h != hash {
		t.Errorf("hash after compact: %v", err)
	}
	if s, n, free := e.Stat(); s != size || n != 3 || free != 0 {
		t.Errorf("after compact: size %d (want %d) files %d free %d", s, size, n, free)
	}
	for _, f := range files {
		if got := extract(t, e, f.id); !bytes.Equal(got, f.data) {
			t.Errorf("file %x after compact", f.id)
		}
	}
}

// A Reader holding the source keeps reading it where it lies and is never
// retargeted (R40): the plan leaves such an extent alone, and a move of it —
// handed to MoveExtents directly — is honoured, the hold keeping the source
// from reuse until the Reader closes.
func TestAReaderKeepsItsExtentThroughAMove(t *testing.T) {
	a, fx := newFixture(t, Options{NoCompression: true})
	hole := add(t, a, root, "hole.bin", noise(100000, 500))
	smallData := noise(50000, 501)
	small := add(t, a, root, "small.bin", smallData)
	add(t, a, root, "big.bin", noise(1<<20, 502))
	if _, err := a.Delete(ctx, hole.ID); err != nil {
		t.Fatal(err)
	}
	r, err := a.OpenReader(small.ID)
	if err != nil {
		t.Fatal(err)
	}
	head := make([]byte, 20000)
	if _, err := io.ReadFull(r, head); err != nil {
		t.Fatal(err)
	}
	from := r.e
	cipher := snapshot(t, fx.path)[from.Off:end(from)]
	if plan := a.PlanReclaim(0); len(plan.Moves) != 0 {
		t.Fatalf("the plan moves a held extent: %+v", plan.Moves)
	}
	free, _, _ := spaces(a)
	to, ok := free.firstFitBefore(from.Len, from.Off)
	if !ok {
		t.Fatal("no hole before the held file")
	}
	if _, err := a.MoveExtents(ctx, []Move{{ID: small.ID, From: from, To: to}}, nil); err != nil {
		t.Fatal(err)
	}
	if got := extentOf(t, a, small.ID); got != to {
		t.Errorf("the record lies at %+v, the move went to %+v", got, to)
	}
	if r.e != from {
		t.Errorf("the reader was retargeted to %+v", r.e)
	}
	rest, err := io.ReadAll(r)
	if err != nil || !bytes.Equal(append(head, rest...), smallData) {
		t.Fatalf("the reader over the moved file: %v", err)
	}
	if _, _, pool := spaces(a); pool.intersects(from) {
		t.Error("the held source is in the pool")
	}
	if !bytes.Equal(snapshot(t, fx.path)[from.Off:end(from)], cipher) {
		t.Error("the held source was written over")
	}
	r.Close()
	r2, err := a.OpenReader(small.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r2.e != to {
		t.Errorf("a new reader holds %+v, the copy is at %+v", r2.e, to)
	}
	r2.Close()
	if got := extract(t, a, small.ID); !bytes.Equal(got, smallData) {
		t.Error("the moved file")
	}
	insideTheFile(t, a)
	disjoint(t, a)
}

// What is not a move is refused before anything is written: an empty run, a
// destination of another length or not wholly before the source (ErrParams),
// a source that is not where the plan found it, a destination that is not
// free, the same file twice (ErrStalePlan). Nothing changes, no transaction
// is left open, and the real move then goes through.
func TestMoveExtentsRefusesWhatIsNotAMove(t *testing.T) {
	a, _ := newFixture(t, Options{NoCompression: true})
	hole := add(t, a, root, "hole.bin", noise(100000, 510))
	keep := add(t, a, root, "keep.bin", noise(200000, 511))
	lastData := noise(50000, 512)
	last := add(t, a, root, "last.bin", lastData)
	if _, err := a.Delete(ctx, hole.ID); err != nil {
		t.Fatal(err)
	}
	plan := a.PlanReclaim(0)
	if len(plan.Moves) != 1 {
		t.Fatalf("plan %+v", plan)
	}
	m := plan.Moves[0]
	keepAt := extentOf(t, a, keep.ID)
	before, _, _ := a.Stat()
	seq := a.Seq()
	for _, tc := range []struct {
		what  string
		moves []Move
		want  error
	}{
		{"no moves", nil, ErrParams},
		{"another length", []Move{{ID: m.ID, From: m.From, To: extent{Off: m.To.Off, Len: m.To.Len - 16}}}, ErrParams},
		{"not wholly before", []Move{{ID: m.ID, From: m.From, To: extent{Off: m.From.Off - 16, Len: m.From.Len}}}, ErrParams},
		{"a source elsewhere", []Move{{ID: m.ID, From: extent{Off: m.From.Off + 16, Len: m.From.Len}, To: m.To}}, ErrStalePlan},
		{"an unknown file", []Move{{ID: hole.ID, From: m.From, To: m.To}}, ErrStalePlan},
		{"a destination over live data", []Move{{ID: m.ID, From: m.From, To: extent{Off: keepAt.Off, Len: m.From.Len}}}, ErrStalePlan},
		{"the same file twice", []Move{m, {ID: m.ID, From: m.From, To: extent{Off: end(m.To), Len: m.From.Len}}}, ErrStalePlan},
	} {
		if _, err := a.MoveExtents(ctx, tc.moves, nil); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", tc.what, err, tc.want)
		}
		if size, _, _ := a.Stat(); size != before || a.Seq() != seq {
			t.Errorf("%s: changed the file: size %d → %d, seq %d → %d", tc.what, before, size, seq, a.Seq())
		}
		if got := extentOf(t, a, last.ID); got != m.From {
			t.Errorf("%s: moved the record to %+v", tc.what, got)
		}
		if tx, err := a.Begin(); err != nil {
			t.Errorf("%s: a transaction afterwards: %v", tc.what, err)
		} else {
			tx.Abort()
		}
	}
	if _, err := a.MoveExtents(ctx, plan.Moves, nil); err != nil {
		t.Fatal(err)
	}
	if got := extract(t, a, last.ID); !bytes.Equal(got, lastData) {
		t.Error("the moved file")
	}
	insideTheFile(t, a)
	disjoint(t, a)
}

// Publish is the empty commit: the same index one sequence on, the
// quarantine spent, and — as with any commit — a free tail given back by the
// follow-up. Here the tail is one an interrupted follow-up left behind.
func TestPublishIsAnEmptyCommitThatGivesTheTailBack(t *testing.T) {
	a, fx := newFixture(t, Options{NoCompression: true})
	keepData := noise(20000, 520)
	keep := add(t, a, root, "keep.bin", keepData)
	before, _, _ := a.Stat()
	last := add(t, a, root, "last.bin", noise(200000, 521))
	grown, _, _ := a.Stat()

	failTrimAt = skipTrimAt(3)
	_, err := a.Delete(ctx, last.ID)
	failTrimAt = nil
	if err != nil {
		t.Fatal(err)
	}
	if s, _, _ := a.Stat(); s < grown {
		t.Fatalf("the interrupted follow-up truncated the file: %d → %d", grown, s)
	}
	seq := a.Seq()
	rec, err := a.Publish(ctx)
	if err != nil {
		t.Fatal(err)
	}
	after, _, _ := a.Stat()
	if rec.Seq != a.Seq() || rec.Size != after {
		t.Errorf("receipt %+v, archive at seq %d size %d", rec, a.Seq(), after)
	}
	if a.Seq() != seq+2 {
		t.Errorf("the empty commit and its follow-up advanced the sequence by %d", a.Seq()-seq)
	}
	if after > before+metadataSlack {
		t.Errorf("Publish did not give the tail back: %d, the file was %d before the add", after, before)
	}
	if s := sizeOnDisk(t, fx.path); s != after {
		t.Errorf("the archive says %d bytes, the file is %d", after, s)
	}
	if names := liveNames(t, a); len(names) != 1 || names[0] != "keep.bin" {
		t.Errorf("live records after Publish: %v", names)
	}
	if got := extract(t, a, keep.ID); !bytes.Equal(got, keepData) {
		t.Error("the file that stayed")
	}
	insideTheFile(t, a)
	disjoint(t, a)

	// Not beside an open transaction, and not on a read-only handle.
	tx, err := a.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Publish(ctx); !errors.Is(err, ErrTxOpen) {
		t.Errorf("Publish beside a transaction: %v", err)
	}
	tx.Abort()
	ro := reread(t, fx)
	if _, err := ro.Publish(ctx); !errors.Is(err, ErrReadOnly) {
		t.Errorf("Publish on a read-only handle: %v", err)
	}
	if p := ro.PlanReclaim(0); len(p.Moves) != 0 {
		t.Errorf("a read-only handle plans %+v", p.Moves)
	}
}

// The run rather than its first commit (ReclaimPlan.RunTailReturned,
// RunBytesToMove and Commits). A plan is one commit deep, and a source the
// plan's own moves free is not a hole for the files behind it in that same
// plan: [hole S][A: S][B: S] moves A and gives nothing back, because B still
// anchors the tail. The estimate walks the caller's own plan-move-plan loop
// in memory so that the rule which decides whether a run is worth making
// (APP.md §2.3) is measured on what the run gives back.

// endToEnd stages one file per size in a single transaction and rewrites the
// archive whole, so that the data lies end to end with no metadata between
// the files: the layout the test builds is the layout on disk, and a delete
// leaves exactly one hole of exactly one file's size. It answers the handle
// the rewrite left, the records in order and their plaintext.
func endToEnd(t testing.TB, fx *fixture, a *Archive, seed uint64, sizes ...int) (*Archive, []FileInfo, [][]byte) {
	t.Helper()
	tx, err := a.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var files []FileInfo
	var data [][]byte
	for i, n := range sizes {
		d := noise(n, seed+uint64(i))
		f, err := tx.Add(ctx, root, string(rune('a'+i))+".bin", bytes.NewReader(d), int64(n))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
		data = append(data, d)
	}
	if _, err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Compact(ctx, nil); err != nil {
		t.Fatal(err)
	}
	return fx.open(t), files, data
}

// runReclaim works the caller's own loop (APP.md §2.3, startReclaim) to its
// end with no budget at all: a fresh plan per commit, Publish where the plan
// wants a hole the last commit freed or a free tail is all there is to take,
// MoveExtents otherwise, until a plan has no moves. It answers the commits it
// made.
func runReclaim(t testing.TB, a *Archive) int {
	t.Helper()
	commits, _ := runReclaimBudget(t, a, 0)
	return commits
}

// runReclaimBudget is that loop under the caller's budget per commit: the
// leading moves of each plan whose sources fit in it, never fewer than one
// (ops.go reclaimRule.batch). It answers the commits it made and the bytes
// they copied, which is what a budgeted estimate must agree with.
func runReclaimBudget(t testing.TB, a *Archive, budget uint64) (commits int, moved uint64) {
	t.Helper()
	published := 0
	for commits < 1024 {
		p := a.PlanReclaim(budget)
		switch {
		case p.NeedsPublish, len(p.Moves) == 0 && p.TailReturned > 0 && published == 0:
			published++
			if _, err := a.Publish(ctx); err != nil {
				t.Fatal(err)
			}
		case len(p.Moves) == 0:
			return commits, moved
		default:
			published = 0
			batch := batchMoves(p.Moves, budget)
			var bytes uint64
			for _, m := range batch {
				bytes += m.From.Len
			}
			if _, err := a.MoveExtents(ctx, batch, nil); err != nil {
				t.Fatalf("the run's commit %d over %+v: %v", commits+1, batch, err)
			}
			moved += bytes
		}
		commits++
	}
	t.Fatal("the run did not converge in 1024 commits")
	return commits, moved
}

// batchMoves is the caller's rule (ops.go reclaimRule.batch): the leading
// moves within the budget, at least one, so that a file larger than the
// budget still moves on its own. A budget of 0 takes the whole plan.
func batchMoves(moves []Move, budget uint64) []Move {
	if budget == 0 {
		return moves
	}
	var n int
	var bytes uint64
	for n < len(moves) && (n == 0 || bytes+moves[n].From.Len <= budget) {
		bytes += moves[n].From.Len
		n++
	}
	return moves[:n]
}

// oneHole asserts the layout a run-level test rests on: a single hole of
// exactly the deleted file's size at the front, nothing quarantined, and the
// metadata above the last live byte.
func oneHole(t testing.TB, a *Archive, firstLive extent) extent {
	t.Helper()
	free, retired, _ := spaces(a)
	if len(free.x) != 1 || len(retired.x) != 0 || end(free.x[0]) != firstLive.Off {
		t.Fatalf("the layout is not one hole before the first live file: free=%+v retired=%+v first=%+v", free.x, retired.x, firstLive)
	}
	return free.x[0]
}

// The first of three equal files deleted — the case the one-commit figures
// cannot answer for. The plan moves A into the hole and promises nothing;
// the run moves A, publishes what that freed, moves B into it and gives the
// hole back, which is what the estimate says: two files moved, one file's
// worth of tail, three commits.
func TestTheRunLevelEstimateSeesPastTheFirstCommit(t *testing.T) {
	a, fx := newFixture(t, Options{NoCompression: true})
	a, files, data := endToEnd(t, fx, a, 900, 100000, 100000, 100000)
	if _, err := a.Delete(ctx, files[0].ID); err != nil {
		t.Fatal(err)
	}
	tighten(t, a)
	aAt, bAt := extentOf(t, a, files[1].ID), extentOf(t, a, files[2].ID)
	if aAt.Len != bAt.Len {
		t.Fatalf("the two files that stay are not of one size: %+v %+v", aAt, bAt)
	}
	hole := oneHole(t, a, aAt)
	if hole.Len != aAt.Len {
		t.Fatalf("the hole %+v is not the size of the files behind it (%d)", hole, aAt.Len)
	}
	before, _, _ := a.Stat()

	plan := a.PlanReclaim(0)
	// One commit: A into the hole, and nothing comes back — B is still the
	// tail, and A's source is not a hole for B in this same plan.
	if len(plan.Moves) != 1 || plan.Moves[0].ID != files[1].ID || plan.Moves[0].To != hole {
		t.Fatalf("plan %+v; expected the first live file into the hole %+v", plan.Moves, hole)
	}
	if plan.BytesToMove != aAt.Len || plan.TailReturned != 0 || plan.NeedsPublish {
		t.Errorf("the one-commit figures: %d to move, %d back, NeedsPublish=%v", plan.BytesToMove, plan.TailReturned, plan.NeedsPublish)
	}
	// The run: both files move, the tail comes back once B has, and the
	// commits are the move, the empty one that publishes A's source — freed
	// by the move, so quarantined for the commit after it — and the move
	// that fills it.
	if plan.RunBytesToMove != aAt.Len+bAt.Len {
		t.Errorf("RunBytesToMove %d; the run moves both files, %d bytes", plan.RunBytesToMove, aAt.Len+bAt.Len)
	}
	if plan.RunTailReturned < aAt.Len || plan.RunTailReturned > aAt.Len+metadataSlack {
		t.Errorf("RunTailReturned %d; the run gives one file's extent back, %d bytes", plan.RunTailReturned, aAt.Len)
	}
	// Three, because the estimate reads R31 as it is written: a source freed
	// at one commit is allocatable from the commit after next, so the move,
	// the empty commit that publishes A's source, and the move into it. The
	// run itself makes two — the first move left a tail to cut, and the
	// follow-up commit that cuts it retires the losing copy onto the moved
	// state, which spends the quarantine there and then (trim.go step 1). The
	// estimate does not model that: a commit with no tail to cut has no
	// follow-up, so counting the empty one is the answer that is never short,
	// and Commits is for a log line in any case. What the two agree on is the
	// bytes.
	if plan.Commits != 3 {
		t.Errorf("Commits %d; the run is a move, the publish of what it freed, and the second move", plan.Commits)
	}

	if got := runReclaim(t, a); got > plan.Commits {
		t.Errorf("the run made %d commits, over the %d the estimate allowed for", got, plan.Commits)
	} else {
		t.Logf("the estimate allowed for %d commits; the run made %d — the first move's own follow-up spent the quarantine", plan.Commits, got)
	}
	after, _, _ := a.Stat()
	if after > before-plan.RunTailReturned {
		t.Errorf("RunTailReturned promised %d bytes and the run gave %d: the estimate is not on the low side", plan.RunTailReturned, before-after)
	}
	if got := extentOf(t, a, files[1].ID); got.Off != hole.Off {
		t.Errorf("the first file lies at %+v, the hole was at %+v", got, hole)
	}
	if got := extentOf(t, a, files[2].ID); got.Off != end(hole) {
		t.Errorf("the second file lies at %+v, not behind the first at %d", got, end(hole))
	}
	if s := sizeOnDisk(t, fx.path); s != after {
		t.Errorf("the archive says %d bytes, the file is %d", after, s)
	}
	readsAll(t, a, "after the run", []wantFile{{files[1].ID, data[1]}, {files[2].ID, data[2]}})
	insideTheFile(t, a)
	disjoint(t, a)
	if p := a.PlanReclaim(0); len(p.Moves) != 0 || p.RunTailReturned != 0 || p.Commits != 0 {
		t.Errorf("after the run the estimate still wants %+v", p)
	}
}

// A step whose move only the step before it made possible: the hole at the
// front is spent to the byte by the file that fits it exactly, and the file
// behind — smaller than the hole, and with nothing before it while the hole
// stands — moves only into the source that first move freed.
func TestARunLevelStepMovesIntoWhatAnEarlierStepFreed(t *testing.T) {
	a, fx := newFixture(t, Options{NoCompression: true})
	a, files, data := endToEnd(t, fx, a, 920, 200000, 200000, 150000)
	if _, err := a.Delete(ctx, files[0].ID); err != nil {
		t.Fatal(err)
	}
	tighten(t, a)
	aAt, bAt := extentOf(t, a, files[1].ID), extentOf(t, a, files[2].ID)
	hole := oneHole(t, a, aAt)
	if hole.Len != aAt.Len || bAt.Len >= hole.Len {
		t.Fatalf("the layout wants a hole of the first file's size (%+v, %+v) that the second (%+v) does not fill", hole, aAt, bAt)
	}
	before, _, _ := a.Stat()

	plan := a.PlanReclaim(0)
	if len(plan.Moves) != 1 || plan.Moves[0].ID != files[1].ID {
		t.Fatalf("plan %+v; the hole holds the first file exactly and nothing is left of it", plan.Moves)
	}
	if plan.TailReturned != 0 {
		t.Errorf("TailReturned %d behind a tail file that cannot move yet", plan.TailReturned)
	}
	if plan.RunBytesToMove != aAt.Len+bAt.Len || plan.Commits != 3 {
		t.Errorf("the run moves %d bytes over %d commits; expected %d over 3", plan.RunBytesToMove, plan.Commits, aAt.Len+bAt.Len)
	}
	if plan.RunTailReturned < aAt.Len || plan.RunTailReturned > aAt.Len+metadataSlack {
		t.Errorf("RunTailReturned %d; the tail comes back by the first file's extent, %d bytes", plan.RunTailReturned, aAt.Len)
	}

	runReclaim(t, a)
	after, _, _ := a.Stat()
	if after > before-plan.RunTailReturned {
		t.Errorf("RunTailReturned promised %d bytes and the run gave %d", plan.RunTailReturned, before-after)
	}
	if got := extentOf(t, a, files[2].ID); got.Off != aAt.Off {
		t.Errorf("the second file lies at %+v; the run should have put it in the source the first move freed, at %d", got, aAt.Off)
	}
	readsAll(t, a, "after the run", []wantFile{{files[1].ID, data[1]}, {files[2].ID, data[2]}})
	insideTheFile(t, a)
	disjoint(t, a)
}

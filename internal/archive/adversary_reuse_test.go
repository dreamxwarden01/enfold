package archive

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/dreamxwarden01/enfold/internal/format"
)

// The adversary's pass over what a move leaves behind and who is reading
// meanwhile: the source a move frees must wait its one commit before a
// write may land on it, with the original state readable behind the live
// copy until then; a cancel or a torn write at every chunk of the copy
// must leave the originals and the file; a Reader opened on the file while
// its ciphertext is being copied, or holding the old tail before the move,
// keeps its extent — unreused, untruncated — until it closes.

// readAllOf drains a Reader.
func readAllOf(r *Reader) ([]byte, error) { return io.ReadAll(r) }

// tighten runs empty commits until nothing is quarantined and no free space
// lies above the last live byte: the layout a follow-up leaves, from which
// a test can reason about what the next commit will do. An add whose index
// lands right at the last live byte keeps its predecessor's metadata as a
// hole, so a run of adds is not tight by itself.
func tighten(t testing.TB, a *Archive) {
	t.Helper()
	tight := func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		keepEnd := uint64(format.ArchiveDataStart)
		for i := range a.index.Files {
			if r := &a.index.Files[i]; r.State == format.FileLive {
				keepEnd = max(keepEnd, r.DataOff+r.StoredSize)
			}
		}
		return len(a.retired.x) == 0 && a.sb.IndexOff == keepEnd &&
			a.sb.FreeMapOff == keepEnd+a.sb.IndexLen+format.TagSize && a.size == a.sb.FreeMapOff+a.sb.FreeMapLen
	}
	for i := 0; i < 4; i++ {
		if tight() {
			return
		}
		if _, err := a.Publish(ctx); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("the archive did not settle into a tight layout")
}

// A hole, a wall no hole holds, a small file behind the wall and a large
// one at the tail: the small file moves, its source is a hole of its own
// behind the wall — the map grows, the commit's own map stands where the
// follow-up would go, nothing is trimmed — and the source sits in
// quarantine. The losing copy is then the original state, its file readable
// at the source; the next commit may not write there; the one after may.
func TestAQuarantinedSourceWaitsItsCommit(t *testing.T) {
	a, fx := newFixture(t, Options{NoCompression: true})
	// One transaction, then a rewrite: the files end to end with no
	// metadata between them, so the delete leaves exactly one hole.
	tx, err := a.Begin()
	if err != nil {
		t.Fatal(err)
	}
	hole, err := tx.Add(ctx, root, "hole.bin", bytes.NewReader(noise(100000, 750)), 100000)
	if err != nil {
		t.Fatal(err)
	}
	wallData := noise(150000, 755)
	wall, err := tx.Add(ctx, root, "wall.bin", bytes.NewReader(wallData), int64(len(wallData))) // larger than the hole: it stays
	if err != nil {
		t.Fatal(err)
	}
	aData := noise(50000, 751)
	fa, err := tx.Add(ctx, root, "a.bin", bytes.NewReader(aData), int64(len(aData)))
	if err != nil {
		t.Fatal(err)
	}
	bData := noise(1<<20, 752)
	fb, err := tx.Add(ctx, root, "b.bin", bytes.NewReader(bData), int64(len(bData))) // the tail, live: no trim
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Compact(ctx, nil); err != nil {
		t.Fatal(err)
	}
	a = fx.open(t)
	if _, err := a.Delete(ctx, hole.ID); err != nil {
		t.Fatal(err)
	}
	tighten(t, a)
	wallAt := extentOf(t, a, wall.ID)
	if free, retired, _ := spaces(a); len(retired.x) != 0 || len(free.x) != 1 || end(free.x[0]) != wallAt.Off {
		t.Fatalf("the layout is not one hole before the wall: free=%+v retired=%+v", free.x, retired.x)
	}
	from := extentOf(t, a, fa.ID)
	plan := a.PlanReclaim(0)
	if len(plan.Moves) != 1 || plan.Moves[0].ID != fa.ID || plan.NeedsPublish {
		t.Fatalf("plan %+v", plan)
	}
	to := plan.Moves[0].To
	seq := a.Seq()
	if _, err := a.MoveExtents(ctx, plan.Moves, nil); err != nil {
		t.Fatal(err)
	}
	if got := extentOf(t, a, fa.ID); got != to {
		t.Errorf("a.bin lies at %+v, the move went to %+v", got, to)
	}
	// The layout this test rests on: the map grew by an extent (the source
	// is a new hole), so the commit's own map stands where the follow-up
	// would place its own and nothing is trimmed — one commit, the source
	// quarantined. A different arithmetic would be a different test.
	if a.Seq() != seq+1 {
		t.Fatalf("the move commit ran a follow-up (%d commits); this test needs the source quarantined", a.Seq()-seq)
	}
	free, retired, pool := spaces(a)
	if !free.contains(from) || !retired.intersects(from) || pool.intersects(from) {
		t.Fatalf("the source %+v: free=%v retired=%v pool=%v; want published free and quarantined", from, free.contains(from), retired.intersects(from), pool.intersects(from))
	}
	want := []wantFile{{fa.ID, aData}, {fb.ID, bData}, {wall.ID, wallData}}
	readsAll(t, a, "after the move", want)

	// R31 behind the live copy: the original state, a.bin at the source.
	img := snapshot(t, fx.path)
	live, _, ok := copies(t, img)
	if !ok {
		t.Fatal("the losing copy does not decode")
	}
	torn := bytes.Clone(img)
	clear(torn[live : live+format.SuperblockSize])
	c := openImage(t, fx, torn, "fallback")
	if c.Seq() != seq || c.Stale() == nil {
		t.Errorf("the fallback opened at seq %d (want %d), stale=%v", c.Seq(), seq, c.Stale())
	}
	if got := extentOf(t, c, fa.ID); got != from {
		t.Errorf("the fallback state has a.bin at %+v, the original had it at %+v", got, from)
	}
	readsAll(t, c, "the fallback state", want)
	insideTheFile(t, c)
	disjoint(t, c)
	c.Close()

	// The next commit: a file of the source's exact size may not land on it.
	xData := noise(50000, 753)
	fx1 := add(t, a, root, "x.bin", xData)
	if xAt := extentOf(t, a, fx1.ID); overlaps(xAt, from) {
		t.Errorf("the commit after the move wrote x.bin at %+v, over the quarantined source %+v", xAt, from)
	}
	want = append(want, wantFile{fx1.ID, xData})
	readsAll(t, a, "after the add", want)
	insideTheFile(t, a)
	disjoint(t, a)
	fallbackReads(t, fx, snapshot(t, fx.path), "after-the-add", want[:3])

	// The commit after next: the source is the pool's.
	if _, _, pool := spaces(a); !pool.contains(from) {
		t.Errorf("after the next commit the source %+v is still not allocatable", from)
	}
	yData := noise(50000, 754)
	fy := add(t, a, root, "y.bin", yData)
	t.Logf("y.bin, of the source's size, landed at %+v (the source was %+v)", extentOf(t, a, fy.ID), from)
	want = append(want, wantFile{fy.ID, yData})
	readsAll(t, a, "after the second add", want)
	insideTheFile(t, a)
	disjoint(t, a)
	fallbackReads(t, fx, snapshot(t, fx.path), "after-the-second-add", want[:4])
}

// chunkedFixture is a hole at the front, a file too large for it, and a
// file at the tail the copy walks in several chunks.
func chunkedFixture(t testing.TB, seed uint64) (a *Archive, fx *fixture, keep, last FileInfo, keepData, lastData []byte, plan ReclaimPlan) {
	t.Helper()
	withMoveChunk(t, 64<<10)
	a, fx = newFixture(t, Options{NoCompression: true})
	front := add(t, a, root, "front.bin", noise(300000, seed))
	keepData = noise(400000, seed+1)
	keep = add(t, a, root, "keep.bin", keepData)
	lastData = noise(200000, seed+2)
	last = add(t, a, root, "last.bin", lastData)
	if _, err := a.Delete(ctx, front.ID); err != nil {
		t.Fatal(err)
	}
	plan = a.PlanReclaim(0)
	if len(plan.Moves) != 1 || plan.Moves[0].ID != last.ID || plan.NeedsPublish {
		t.Fatalf("plan %+v", plan)
	}
	if n := (plan.Moves[0].From.Len + moveChunk - 1) / moveChunk; n < 3 {
		t.Fatalf("the copy is %d chunks; this fixture wants several", n)
	}
	return
}

// A cancel at every chunk of the copy, the last one included — where the
// copy is whole and the commit is what the cancel stops: the originals are
// live, the file is what it was, the chunks that landed lie in the
// destination and nowhere else, and the same plan then goes through.
func TestACancelAtEveryChunk(t *testing.T) {
	a, fx, keep, last, keepData, lastData, plan := chunkedFixture(t, 760)
	m := plan.Moves[0]
	chunks := (m.From.Len + moveChunk - 1) / moveChunk
	cipher := snapshot(t, fx.path)[m.From.Off:end(m.From)]
	before, _, _ := a.Stat()
	seq := a.Seq()
	want := []wantFile{{keep.ID, keepData}, {last.ID, lastData}}
	for k := uint64(1); k <= chunks; k++ {
		cctx, cancel := context.WithCancel(ctx)
		var calls uint64
		_, err := a.MoveExtents(cctx, plan.Moves, func(done, total uint64) {
			calls++
			if calls == k {
				cancel()
			}
		})
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("a cancel at chunk %d: %v", k, err)
		}
		if calls != k {
			t.Errorf("the copy went on for %d chunks after a cancel at chunk %d", calls, k)
		}
		if a.Seq() != seq || a.Broken() != nil {
			t.Errorf("chunk %d: seq %d (was %d), broken=%v", k, a.Seq(), seq, a.Broken())
		}
		if s, _, _ := a.Stat(); s != before {
			t.Errorf("chunk %d: the cancelled move left the file at %d; it was %d", k, s, before)
		}
		if s := sizeOnDisk(t, fx.path); s != before {
			t.Errorf("chunk %d: the file is %d bytes on disk; it was %d", k, s, before)
		}
		if got := extentOf(t, a, last.ID); got != m.From {
			t.Errorf("chunk %d: the cancelled move moved the record: %+v → %+v", k, m.From, got)
		}
		img := snapshot(t, fx.path)
		landed := min(k*moveChunk, m.From.Len)
		if !bytes.Equal(img[m.To.Off:m.To.Off+landed], cipher[:landed]) {
			t.Errorf("chunk %d: the %d bytes copied before the cancel are not in the destination", k, landed)
		}
		if !bytes.Equal(img[m.From.Off:end(m.From)], cipher) {
			t.Errorf("chunk %d: the source was written", k)
		}
		if again := a.PlanReclaim(0); !sameMoves(again.Moves, plan.Moves) {
			t.Errorf("chunk %d: the plan changed under a cancelled move: %+v → %+v", k, plan.Moves, again.Moves)
		}
		b := reread(t, fx)
		if b.Seq() != seq || b.Stale() != nil || b.FreeMapRebuilt() != nil {
			t.Errorf("chunk %d: reopened at seq %d (want %d) stale=%v map=%v", k, b.Seq(), seq, b.Stale(), b.FreeMapRebuilt())
		}
		readsAll(t, b, "reopened after the cancel", want)
		insideTheFile(t, b)
		disjoint(t, b)
		b.Close()
	}
	if _, err := a.MoveExtents(ctx, plan.Moves, nil); err != nil {
		t.Fatal(err)
	}
	if after, _, _ := a.Stat(); after >= before {
		t.Errorf("the file went %d → %d", before, after)
	}
	readsAll(t, a, "after the move", want)
	insideTheFile(t, a)
	disjoint(t, a)
	fallbackReads(t, fx, snapshot(t, fx.path), "after-the-move", want)
}

// A write of the copy that fails at every chunk — torn in half, and stopped
// before its first byte — aborts the move each time: the originals are live,
// the file is what it was, the torn bytes lie in the destination and the
// source is untouched; then the plan goes through.
func TestATornCopyAtEveryChunk(t *testing.T) {
	a, fx, keep, last, keepData, lastData, plan := chunkedFixture(t, 770)
	m := plan.Moves[0]
	chunks := int((m.From.Len + moveChunk - 1) / moveChunk)
	cipher := snapshot(t, fx.path)[m.From.Off:end(m.From)]
	before, _, _ := a.Stat()
	seq := a.Seq()
	want := []wantFile{{keep.ID, keepData}, {last.ID, lastData}}
	for k := 1; k <= chunks; k++ {
		for _, tear := range []int{-1, 0} {
			what := "torn"
			if tear == 0 {
				what = "not begun"
			}
			var tore landing
			got := &tore
			if tear == 0 {
				got = nil
			}
			failMoveAt = armTrim(1, k, tear, got)
			_, err := a.MoveExtents(ctx, plan.Moves, nil)
			failMoveAt = nil
			if !errors.Is(err, errTornWrite) {
				t.Fatalf("chunk %d %s: %v", k, what, err)
			}
			if a.Seq() != seq || a.Broken() != nil {
				t.Errorf("chunk %d %s: seq %d (was %d), broken=%v", k, what, a.Seq(), seq, a.Broken())
			}
			if s := sizeOnDisk(t, fx.path); s != before {
				t.Errorf("chunk %d %s: the file is %d bytes on disk; it was %d", k, what, s, before)
			}
			if got := extentOf(t, a, last.ID); got != m.From {
				t.Errorf("chunk %d %s: the record moved: %+v → %+v", k, what, m.From, got)
			}
			img := snapshot(t, fx.path)
			if !bytes.Equal(img[m.From.Off:end(m.From)], cipher) {
				t.Errorf("chunk %d %s: the source was written", k, what)
			}
			if tear != 0 {
				tore.on(t, fx.path)
				if tore.off < m.To.Off || tore.off+uint64(len(tore.b)) > end(m.To) {
					t.Errorf("chunk %d: the torn write landed at %d, outside the destination %+v", k, tore.off, m.To)
				}
			}
			if tx, err := a.Begin(); err != nil {
				t.Errorf("chunk %d %s: a transaction afterwards: %v", k, what, err)
			} else {
				tx.Abort()
			}
			b := reread(t, fx)
			readsAll(t, b, "reopened after the torn copy", want)
			if free, _, _ := spaces(b); !free.contains(m.To) {
				t.Errorf("chunk %d %s: the torn bytes are not in free space", k, what)
			}
			insideTheFile(t, b)
			disjoint(t, b)
			b.Close()
		}
	}
	if again := a.PlanReclaim(0); !sameMoves(again.Moves, plan.Moves) {
		t.Errorf("the plan changed under the aborted moves: %+v → %+v", plan.Moves, again.Moves)
	}
	if _, err := a.MoveExtents(ctx, plan.Moves, nil); err != nil {
		t.Fatal(err)
	}
	if after, _, _ := a.Stat(); after >= before {
		t.Errorf("the file went %d → %d", before, after)
	}
	readsAll(t, a, "after the move", want)
	insideTheFile(t, a)
	disjoint(t, a)
	fallbackReads(t, fx, snapshot(t, fx.path), "after-the-move", want)
}

// A Reader opened on the file while its ciphertext is being copied holds
// the source: the move commits, the Reader is never retargeted and reads
// the file whole from where it lies, the source is neither reused nor
// truncated while the hold lasts, and once the Reader closes the tail is
// on offer and the next commit gives it back.
func TestAReaderOpenedDuringTheCopyKeepsTheSource(t *testing.T) {
	a, fx, keep, last, keepData, lastData, plan := chunkedFixture(t, 780)
	m := plan.Moves[0]
	cipher := snapshot(t, fx.path)[m.From.Off:end(m.From)]
	before, _, _ := a.Stat()
	var r *Reader
	head := make([]byte, 30000)
	_, err := a.MoveExtents(ctx, plan.Moves, func(done, total uint64) {
		if r != nil {
			return
		}
		var err error
		if r, err = a.OpenReader(last.ID); err != nil {
			t.Errorf("a reader during the copy: %v", err)
			return
		}
		if _, err := io.ReadFull(r, head); err != nil {
			t.Errorf("reading during the copy: %v", err)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if r == nil {
		t.Fatal("no reader was opened")
	}
	defer r.Close()
	if r.e != m.From {
		t.Errorf("the reader was retargeted to %+v", r.e)
	}
	if got := extentOf(t, a, last.ID); got != m.To {
		t.Errorf("the record lies at %+v, the move went to %+v", got, m.To)
	}
	if s := sizeOnDisk(t, fx.path); s < end(m.From) {
		t.Errorf("the file is %d bytes: the tail a reader holds, ending at %d, was cut", s, end(m.From))
	}
	rest, err := readAllOf(r)
	if err != nil || !bytes.Equal(append(head, rest...), lastData) {
		t.Errorf("the reader over the moved file: %v", err)
	}
	if !bytes.Equal(snapshot(t, fx.path)[m.From.Off:end(m.From)], cipher) {
		t.Error("the held source was written over")
	}
	free, _, pool := spaces(a)
	if !free.contains(m.From) || pool.intersects(m.From) {
		t.Errorf("the held source %+v: free=%v pool=%v; want published free and out of the pool", m.From, free.contains(m.From), pool.intersects(m.From))
	}
	want := []wantFile{{keep.ID, keepData}, {last.ID, lastData}}
	readsAll(t, a, "with the reader open", want)
	insideTheFile(t, a)
	disjoint(t, a)
	if p := a.PlanReclaim(0); len(p.Moves) != 0 {
		t.Errorf("with the reader open the plan wants %+v", p.Moves)
	}

	r.Close()
	p := a.PlanReclaim(0)
	if len(p.Moves) != 0 || p.TailReturned == 0 {
		t.Errorf("with the reader closed the plan is %+v; the tail above keep.bin should be on offer", p)
	}
	if _, err := a.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	if after, _, _ := a.Stat(); after > before-m.From.Len {
		t.Errorf("the file went %d → %d; a %d-byte file moved down and its reader closed", before, after, m.From.Len)
	}
	readsAll(t, a, "after the tail came back", want)
	insideTheFile(t, a)
	disjoint(t, a)
	fallbackReads(t, fx, snapshot(t, fx.path), "after-the-tail", want)
}

// A move of the old tail while a Reader holds it, handed over directly (the
// plan leaves it alone): the follow-up stops at the held extent, the Reader
// reads it whole, its bytes are untouched; and after the Reader closes the
// plan offers the tail and Publish gives it back.
func TestTheFollowUpStopsAtAHeldOldTail(t *testing.T) {
	m := newMoveFixture(t, 790)
	a, fx := m.a, m.fx
	r, err := a.OpenReader(m.last.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.e != m.from {
		t.Fatalf("the reader holds %+v, the tail file lies at %+v", r.e, m.from)
	}
	if p := a.PlanReclaim(0); len(p.Moves) != 0 || p.TailReturned != 0 {
		t.Errorf("with the tail held the plan is %+v", p)
	}
	cipher := snapshot(t, fx.path)[m.from.Off:end(m.from)]
	before, _, _ := a.Stat()
	to := m.plan.Moves[0].To
	if _, err := a.MoveExtents(ctx, m.plan.Moves, nil); err != nil {
		t.Fatal(err)
	}
	if s := sizeOnDisk(t, fx.path); s < end(m.from) {
		t.Errorf("the file is %d bytes: the held tail ending at %d was cut", s, end(m.from))
	}
	if got := extentOf(t, a, m.last.ID); got != to {
		t.Errorf("the record lies at %+v, the move went to %+v", got, to)
	}
	if got, err := readAllOf(r); err != nil || !bytes.Equal(got, m.lastData) {
		t.Errorf("the reader over the held tail: %v", err)
	}
	if !bytes.Equal(snapshot(t, fx.path)[m.from.Off:end(m.from)], cipher) {
		t.Error("the held tail was written over")
	}
	readsAll(t, a, "with the reader open", m.files())
	insideTheFile(t, a)
	disjoint(t, a)

	r.Close()
	p := a.PlanReclaim(0)
	if len(p.Moves) != 0 || p.TailReturned == 0 {
		t.Errorf("with the reader closed the plan is %+v; the old tail should be on offer", p)
	}
	if _, err := a.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	after, _, _ := a.Stat()
	if after > before-m.from.Len+64 {
		t.Errorf("the file went %d → %d; a %d-byte tail moved down", before, after, m.from.Len)
	}
	if s := sizeOnDisk(t, fx.path); s != after {
		t.Errorf("the archive says %d bytes, the file is %d", after, s)
	}
	readsAll(t, a, "after the tail came back", m.files())
	insideTheFile(t, a)
	disjoint(t, a)
	fallbackReads(t, fx, snapshot(t, fx.path), "after-the-tail", m.files())
}

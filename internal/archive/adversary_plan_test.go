package archive

import (
	"bytes"
	"errors"
	"testing"

	"github.com/dreamxwarden01/enfold/internal/format"
)

// The adversary's pass over the plan and the gate (reclaim.go, tx.take): a
// plan on an archive with nothing to move, destinations that a plan would
// never choose but a caller might hand over — a hole a reader holds, two
// destinations that collide, a destination that is another move's source,
// one the losing copy still references — and a plan worked through in
// batches, which the contract forbids. Every refusal must write nothing:
// the image is compared byte for byte.

// unchanged asserts that a refused call left the file exactly as it was, the
// handle at the same state, and no transaction open.
func unchanged(t testing.TB, a *Archive, what string, img []byte, seq uint64) {
	t.Helper()
	if !bytes.Equal(snapshot(t, a.path), img) {
		t.Errorf("%s: the file changed under a refused call", what)
	}
	if a.Seq() != seq || a.Broken() != nil {
		t.Errorf("%s: seq %d (was %d), broken=%v", what, a.Seq(), seq, a.Broken())
	}
	tx, err := a.Begin()
	if err != nil {
		t.Errorf("%s: a transaction afterwards: %v", what, err)
		return
	}
	tx.Abort()
}

// Nothing to move: the plan on a fresh archive and on one whose files lie
// end to end is empty and promises nothing; MoveExtents with no moves is
// ErrParams; Publish with nothing free is a pair of commits that changes
// the size by nothing and every file survives; and a closed handle plans
// nothing and refuses the rest.
func TestThePlanWithNothingToMove(t *testing.T) {
	a, fx := newFixture(t, Options{NoCompression: true})
	if p := a.PlanReclaim(0); len(p.Moves) != 0 || p.BytesToMove != 0 || p.TailReturned != 0 || p.NeedsPublish {
		t.Errorf("the plan on an empty archive: %+v", p)
	}
	if _, err := a.MoveExtents(ctx, nil, nil); !errors.Is(err, ErrParams) {
		t.Errorf("no moves: %v", err)
	}
	empty, _, _ := a.Stat()
	seq := a.Seq()
	if _, err := a.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	if s, _, _ := a.Stat(); s != empty {
		t.Errorf("Publish on an empty archive changed its size: %d → %d", empty, s)
	}
	if a.Seq() <= seq {
		t.Errorf("Publish did not advance the sequence: %d → %d", seq, a.Seq())
	}
	insideTheFile(t, a)
	disjoint(t, a)

	var want []wantFile
	for i, n := range []int{30000, 20000, 40000} {
		data := noise(n, 700+uint64(i))
		f := add(t, a, root, string(rune('a'+i))+".bin", data)
		want = append(want, wantFile{f.ID, data})
	}
	// Each add left its predecessor's index and free map as a hole between
	// the files, too small for any of them: holes that hold nothing are no
	// moves and no tail.
	if _, _, free := a.Stat(); free == 0 {
		t.Fatalf("the adds left no metadata holes; this test wants holes nothing fits")
	}
	p := a.PlanReclaim(0)
	if len(p.Moves) != 0 || p.BytesToMove != 0 || p.TailReturned != 0 || p.NeedsPublish {
		t.Errorf("the plan with holes nothing fits: %+v", p)
	}
	// The whole-file rewrite leaves no hole at all.
	if _, _, err := a.Compact(ctx, nil); err != nil {
		t.Fatal(err)
	}
	a = fx.open(t)
	if _, _, free := a.Stat(); free != 0 {
		t.Fatalf("after Compact the files do not lie end to end: %d bytes free", free)
	}
	p = a.PlanReclaim(0)
	if len(p.Moves) != 0 || p.BytesToMove != 0 || p.TailReturned != 0 || p.NeedsPublish {
		t.Errorf("the plan with no holes: %+v", p)
	}
	before, _, _ := a.Stat()
	seq = a.Seq()
	rec, err := a.Publish(ctx)
	if err != nil {
		t.Fatal(err)
	}
	after, _, _ := a.Stat()
	if after != before {
		t.Errorf("Publish with nothing free changed the size: %d → %d", before, after)
	}
	if rec.Seq != a.Seq() || a.Seq() != seq+2 {
		t.Errorf("Publish with nothing free: receipt %+v, seq %d → %d (the commit and its follow-up)", rec, seq, a.Seq())
	}
	if s := sizeOnDisk(t, fx.path); s != after {
		t.Errorf("the archive says %d bytes, the file is %d", after, s)
	}
	readsAll(t, a, "after Publish", want)
	insideTheFile(t, a)
	disjoint(t, a)
	fallbackReads(t, fx, snapshot(t, fx.path), "after-publish", want)

	a.Close()
	if p := a.PlanReclaim(0); len(p.Moves) != 0 || p.TailReturned != 0 {
		t.Errorf("a closed handle plans %+v", p)
	}
	if _, err := a.MoveExtents(ctx, []Move{{}}, nil); !errors.Is(err, ErrClosed) {
		t.Errorf("MoveExtents on a closed handle: %v", err)
	}
	if _, err := a.Publish(ctx); !errors.Is(err, ErrClosed) {
		t.Errorf("Publish on a closed handle: %v", err)
	}
}

// A hole a Reader holds — a deleted file's extent, free and unquarantined,
// with the Reader still on it — is left out of the plan, and a move handed
// over with that hole as its destination is refused whole; the Reader keeps
// reading; and once it closes the same move is planned and goes through.
func TestADestinationOverAHeldExtentIsRefused(t *testing.T) {
	a, fx := newFixture(t, Options{NoCompression: true})
	goneData := noise(100000, 710)
	gone := add(t, a, root, "gone.bin", goneData)
	keepData := noise(200000, 711)
	keep := add(t, a, root, "keep.bin", keepData)
	lastData := noise(50000, 712)
	last := add(t, a, root, "last.bin", lastData)
	r, err := a.OpenReader(gone.ID)
	if err != nil {
		t.Fatal(err)
	}
	held := r.e
	if _, err := a.Delete(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}
	free, retired, pool := spaces(a)
	if !free.contains(held) || retired.intersects(held) || pool.intersects(held) {
		t.Fatalf("the held extent %+v: free=%v retired=%v pool=%v; want free, unquarantined and out of the pool", held, free.contains(held), retired.intersects(held), pool.intersects(held))
	}
	from := extentOf(t, a, last.ID)
	if p := a.PlanReclaim(0); len(p.Moves) != 0 {
		t.Errorf("the plan moves into a held hole: %+v", p.Moves)
	}
	img := snapshot(t, fx.path)
	seq := a.Seq()
	to := extent{Off: held.Off, Len: from.Len}
	if _, err := a.MoveExtents(ctx, []Move{{ID: last.ID, From: from, To: to}}, nil); !errors.Is(err, ErrStalePlan) {
		t.Errorf("a move into a held hole: %v", err)
	}
	unchanged(t, a, "a move into a held hole", img, seq)
	if got := extentOf(t, a, last.ID); got != from {
		t.Errorf("the refused move moved the record: %+v → %+v", from, got)
	}
	if got, err := readAllOf(r); err != nil || !bytes.Equal(got, goneData) {
		t.Errorf("the reader over the held hole: %v", err)
	}
	r.Close()
	// The hole the reader held merges with whatever free space adjoins it
	// (the Create-time index and map lie just before the first file), so the
	// plan's destination is the merged hole's start; it covers the held one.
	p := a.PlanReclaim(0)
	if len(p.Moves) != 1 || p.Moves[0].ID != last.ID || !overlaps(p.Moves[0].To, held) || p.Moves[0].To.Off > held.Off {
		t.Fatalf("after the reader closed the plan is %+v, want last.bin into the hole at %+v", p.Moves, held)
	}
	if _, err := a.MoveExtents(ctx, p.Moves, nil); err != nil {
		t.Fatal(err)
	}
	readsAll(t, a, "after the move", []wantFile{{keep.ID, keepData}, {last.ID, lastData}})
	insideTheFile(t, a)
	disjoint(t, a)
}

// Two holes around a wall and two files behind it, the plan one move into
// each: destinations that collide with each other, or with the other move's
// source, are refused whole — nothing written, no record moved — and the
// same two moves in the other order go through.
func TestTwoMovesWhoseDestinationsCollideAreRefusedWhole(t *testing.T) {
	a, fx := newFixture(t, Options{NoCompression: true})
	first := add(t, a, root, "first.bin", noise(60000, 720))
	wallData := noise(200000, 721)
	wall := add(t, a, root, "wall.bin", wallData)
	second := add(t, a, root, "second.bin", noise(60000, 722))
	cData := noise(50000, 723)
	c := add(t, a, root, "c.bin", cData)
	dData := noise(50000, 724)
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
	plan := a.PlanReclaim(0)
	if len(plan.Moves) != 2 || plan.Moves[0].ID != c.ID || plan.Moves[1].ID != d.ID || plan.NeedsPublish {
		t.Fatalf("plan %+v", plan)
	}
	mc, md := plan.Moves[0], plan.Moves[1]
	if mc.From.Len != md.From.Len {
		t.Fatalf("c.bin and d.bin store to different sizes: %d and %d", mc.From.Len, md.From.Len)
	}
	img := snapshot(t, fx.path)
	seq := a.Seq()
	for _, tc := range []struct {
		what  string
		moves []Move
	}{
		{"the same destination twice", []Move{mc, {ID: d.ID, From: md.From, To: mc.To}}},
		{"destinations that overlap", []Move{mc, {ID: d.ID, From: md.From, To: extent{Off: mc.To.Off + 16, Len: md.From.Len}}}},
		{"a destination that is the other move's source", []Move{mc, {ID: d.ID, From: md.From, To: mc.From}}},
		{"a destination that is the other move's source, that move second", []Move{{ID: d.ID, From: md.From, To: mc.From}, mc}},
		{"a destination over the wall", []Move{mc, {ID: d.ID, From: md.From, To: extent{Off: extentOf(t, a, wall.ID).Off, Len: md.From.Len}}}},
	} {
		if _, err := a.MoveExtents(ctx, tc.moves, nil); !errors.Is(err, ErrStalePlan) {
			t.Errorf("%s: %v, want %v", tc.what, err, ErrStalePlan)
		}
		unchanged(t, a, tc.what, img, seq)
		if got := extentOf(t, a, c.ID); got != mc.From {
			t.Errorf("%s: c.bin moved to %+v", tc.what, got)
		}
		if got := extentOf(t, a, d.ID); got != md.From {
			t.Errorf("%s: d.bin moved to %+v", tc.what, got)
		}
	}
	// The plan's own two moves in the other order: order is not a constraint.
	if _, err := a.MoveExtents(ctx, []Move{md, mc}, nil); err != nil {
		t.Fatal(err)
	}
	if got := extentOf(t, a, c.ID); got != mc.To {
		t.Errorf("c.bin lies at %+v, the move went to %+v", got, mc.To)
	}
	if got := extentOf(t, a, d.ID); got != md.To {
		t.Errorf("d.bin lies at %+v, the move went to %+v", got, md.To)
	}
	want := []wantFile{{wall.ID, wallData}, {c.ID, cData}, {d.ID, dData}}
	readsAll(t, a, "after the moves", want)
	insideTheFile(t, a)
	disjoint(t, a)
	fallbackReads(t, fx, snapshot(t, fx.path), "after-the-moves", want)
}

// tx.take is the gate every destination passes, so it is put to each thing
// it must refuse, directly: live data, an extent the losing copy still
// references (freed by the commit just made and quarantined), an extent a
// reader holds, an extent already taken, an extent that straddles two holes,
// and an empty one; and what it must accept — free space the transaction may
// allocate — exactly once.
func TestTakeIsTheGate(t *testing.T) {
	a, _ := newFixture(t, Options{NoCompression: true})
	gone := add(t, a, root, "gone.bin", noise(100000, 730))
	held := add(t, a, root, "held.bin", noise(30000, 731))
	keep := add(t, a, root, "keep.bin", noise(200000, 732))
	goneAt := extentOf(t, a, gone.ID)
	// A reader on held.bin, then held.bin and gone.bin deleted with a file
	// appended in the same commit: the tail stays live, nothing is trimmed,
	// and both freed extents sit in quarantine — held.bin's under a hold too.
	r, err := a.OpenReader(held.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	heldAt := r.e
	tx, err := a.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Delete(gone.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Delete(held.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Add(ctx, root, "d.bin", bytes.NewReader(noise(40000, 733)), 40000); err != nil {
		t.Fatal(err)
	}
	seq := a.Seq()
	if _, err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if a.Seq() != seq+1 {
		t.Fatalf("the commit gave a tail back (%d commits); the quarantine is what this test needs", a.Seq()-seq)
	}
	free, retired, pool := spaces(a)
	if !free.contains(goneAt) || !retired.intersects(goneAt) || pool.intersects(goneAt) {
		t.Fatalf("gone.bin's extent %+v: free=%v retired=%v pool=%v", goneAt, free.contains(goneAt), retired.intersects(goneAt), pool.intersects(goneAt))
	}
	keepAt := extentOf(t, a, keep.ID)
	a.mu.Lock()
	oldIndex := extent{Off: a.loser[0].Off, Len: a.loser[0].Len}
	liveIndex := extent{Off: a.sb.IndexOff, Len: a.sb.IndexLen + format.TagSize}
	a.mu.Unlock()
	if !retired.intersects(oldIndex) || !free.contains(oldIndex) {
		t.Fatalf("the previous index %+v is not quarantined free space: free=%v retired=%v", oldIndex, free.contains(oldIndex), retired.intersects(oldIndex))
	}
	type probe struct {
		what string
		e    extent
		want error
	}
	probeAll := func(phase string, probes []probe) {
		t.Helper()
		tx, err := a.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Abort()
		for _, tc := range probes {
			a.mu.Lock()
			err := tx.take(tc.e)
			a.mu.Unlock()
			if !errors.Is(err, tc.want) {
				t.Errorf("%s: take(%s %+v): %v, want %v", phase, tc.what, tc.e, err, tc.want)
			}
		}
	}

	// The commit right after: what that commit freed is the losing copy's.
	probeAll("the commit after", []probe{
		{"live data", extent{Off: keepAt.Off, Len: 1000}, ErrStalePlan},
		{"the live index", liveIndex, ErrStalePlan},
		{"the previous commit's index, quarantined", oldIndex, ErrStalePlan},
		{"a deleted file's extent, quarantined", goneAt, ErrStalePlan},
		{"part of it", extent{Off: goneAt.Off + 16, Len: 32}, ErrStalePlan},
		{"a deleted file's extent, quarantined and held", extent{Off: heldAt.Off, Len: 1000}, ErrStalePlan},
		{"an extent running past the end of the file", extent{Off: goneAt.Off, Len: 1 << 40}, ErrStalePlan},
		{"an empty extent", extent{Off: goneAt.Off, Len: 0}, ErrInternal},
	})

	// The commit after next may allocate what the earlier one freed;
	// held.bin's extent stays out of the pool while the reader lives.
	if _, err := a.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	free, retired, pool = spaces(a)
	if !pool.contains(goneAt) || !pool.contains(oldIndex) {
		t.Fatalf("after Publish the freed extents are not in the pool: gone=%v index=%v (free=%v/%v retired=%v/%v)", pool.contains(goneAt), pool.contains(oldIndex), free.contains(goneAt), free.contains(oldIndex), retired.intersects(goneAt), retired.intersects(oldIndex))
	}
	if !free.contains(heldAt) || pool.intersects(heldAt) {
		t.Fatalf("the held extent %+v: free=%v pool=%v; want free and out of the pool", heldAt, free.contains(heldAt), pool.intersects(heldAt))
	}
	a.mu.Lock()
	liveIndex = extent{Off: a.sb.IndexOff, Len: a.sb.IndexLen + format.TagSize}
	a.mu.Unlock()
	half := extent{Off: goneAt.Off, Len: goneAt.Len / 2}
	tx, err = a.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Abort()
	for _, tc := range []probe{
		{"live data", extent{Off: keepAt.Off, Len: 1000}, ErrStalePlan},
		{"the live index", liveIndex, ErrStalePlan},
		{"an extent a reader holds", extent{Off: heldAt.Off, Len: 1000}, ErrStalePlan},
		{"an extent straddling the held one and free space", extent{Off: heldAt.Off - 100, Len: 200}, ErrStalePlan},
		{"an extent running past the end of the file", extent{Off: goneAt.Off, Len: 1 << 40}, ErrStalePlan},
		{"an empty extent", extent{Off: goneAt.Off, Len: 0}, ErrInternal},
		{"the index freed two commits ago", oldIndex, nil},
		{"free space the transaction may allocate", half, nil},
		{"the same free space twice", half, ErrStalePlan},
		{"an extent overlapping what was taken", extent{Off: half.Off + 16, Len: 32}, ErrStalePlan},
		{"the rest of that hole", extent{Off: end(half), Len: goneAt.Len - half.Len}, nil},
	} {
		a.mu.Lock()
		err := tx.take(tc.e)
		a.mu.Unlock()
		if !errors.Is(err, tc.want) {
			t.Errorf("the commit after next: take(%s %+v): %v, want %v", tc.what, tc.e, err, tc.want)
		}
	}
	a.mu.Lock()
	if !tx.allocs.contains(goneAt) || tx.pool.intersects(goneAt) {
		t.Errorf("after two takes gone.bin's extent is not wholly this transaction's: allocs=%v pool=%v", tx.allocs.contains(goneAt), tx.pool.intersects(goneAt))
	}
	a.mu.Unlock()
}

// A plan answers for one commit: worked through in two batches, the second
// batch is either still a fit — the move goes through — or refused whole
// (ErrStalePlan), never acted on in part; and the plan re-made after the
// first batch finishes the run.
func TestAPlanWorkedThroughInBatchesIsRefusedOrHarmless(t *testing.T) {
	a, fx := newFixture(t, Options{NoCompression: true})
	first := add(t, a, root, "first.bin", noise(60000, 740))
	wallData := noise(200000, 741)
	wall := add(t, a, root, "wall.bin", wallData)
	second := add(t, a, root, "second.bin", noise(60000, 742))
	cData := noise(50000, 743)
	c := add(t, a, root, "c.bin", cData)
	dData := noise(50000, 744)
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
	plan := a.PlanReclaim(0)
	if len(plan.Moves) != 2 {
		t.Fatalf("plan %+v", plan)
	}
	want := []wantFile{{wall.ID, wallData}, {c.ID, cData}, {d.ID, dData}}
	if _, err := a.MoveExtents(ctx, plan.Moves[:1], nil); err != nil {
		t.Fatal(err)
	}
	readsAll(t, a, "after the first batch", want)
	img := snapshot(t, fx.path)
	seq := a.Seq()
	dAt := extentOf(t, a, d.ID)
	_, err = a.MoveExtents(ctx, plan.Moves[1:], nil)
	switch {
	case err == nil:
		t.Logf("the second batch still fit")
		if got := extentOf(t, a, d.ID); got != plan.Moves[1].To {
			t.Errorf("d.bin lies at %+v, the move went to %+v", got, plan.Moves[1].To)
		}
	case errors.Is(err, ErrStalePlan):
		t.Logf("the second batch was refused: %v", err)
		unchanged(t, a, "the second batch", img, seq)
		if got := extentOf(t, a, d.ID); got != dAt {
			t.Errorf("the refused batch moved d.bin: %+v → %+v", dAt, got)
		}
	default:
		t.Fatalf("the second batch: %v", err)
	}
	readsAll(t, a, "after the second batch", want)
	insideTheFile(t, a)
	disjoint(t, a)

	for i := 0; i < 3; i++ {
		p := a.PlanReclaim(0)
		if p.NeedsPublish {
			if _, err := a.Publish(ctx); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if len(p.Moves) == 0 {
			break
		}
		if _, err := a.MoveExtents(ctx, p.Moves, nil); err != nil {
			t.Fatalf("the re-made plan %+v: %v", p.Moves, err)
		}
	}
	if p := a.PlanReclaim(0); len(p.Moves) != 0 {
		t.Errorf("the run did not converge: %+v", p.Moves)
	}
	if after, _, _ := a.Stat(); after > before-plan.BytesToMove {
		t.Errorf("the file went %d → %d over a run that moved %d bytes down", before, after, plan.BytesToMove)
	}
	readsAll(t, a, "after the run", want)
	insideTheFile(t, a)
	disjoint(t, a)
	fallbackReads(t, fx, snapshot(t, fx.path), "after-the-run", want)
}

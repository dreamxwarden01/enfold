package archive

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/dreamxwarden01/enfold/internal/format"
)

// The adversary's smaller probes of the move step: a context cancelled
// before the first chunk, a read-only handle, and a plan made over a free
// map Open had to rebuild.

// A context cancelled before MoveExtents is called copies nothing: no
// progress, no change to the file, the plan intact; and a read-only handle
// refuses the move before anything is staged.
func TestAMoveUnderACancelledContextOrAReadOnlyHandle(t *testing.T) {
	m := newMoveFixture(t, 810)
	a, fx := m.a, m.fx
	img := snapshot(t, fx.path)
	seq := a.Seq()
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	var calls int
	_, err := a.MoveExtents(cctx, m.plan.Moves, func(done, total uint64) { calls++ })
	if !errors.Is(err, context.Canceled) || calls != 0 {
		t.Errorf("a cancelled context: %v after %d progress calls", err, calls)
	}
	unchanged(t, a, "a cancelled context", img, seq)
	if _, err := a.Publish(cctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Publish under a cancelled context: %v", err)
	}
	unchanged(t, a, "Publish under a cancelled context", img, seq)

	ro := reread(t, fx)
	if _, err := ro.MoveExtents(ctx, m.plan.Moves, nil); !errors.Is(err, ErrReadOnly) {
		t.Errorf("MoveExtents on a read-only handle: %v", err)
	}
	if !bytes.Equal(snapshot(t, ro.path), img) {
		t.Error("the read-only handle wrote")
	}
	ro.Close()

	if again := a.PlanReclaim(0); !sameMoves(again.Moves, m.plan.Moves) {
		t.Errorf("the plan changed: %+v → %+v", m.plan.Moves, again.Moves)
	}
	if _, err := a.MoveExtents(ctx, m.plan.Moves, nil); err != nil {
		t.Fatal(err)
	}
	readsAll(t, a, "after the move", m.files())
	insideTheFile(t, a)
	disjoint(t, a)
}

// A free map Open had to rebuild — one byte of it flipped — is the map the
// plan is made on: the hole is still found from what nothing references,
// the move goes through, and the commit publishes a map that decodes.
func TestAMoveOverARebuiltFreeMap(t *testing.T) {
	m := newMoveFixture(t, 820)
	a, fx := m.a, m.fx
	to := m.plan.Moves[0].To
	a.Close()
	img := snapshot(t, fx.path)
	live, _, _ := copies(t, img)
	sb := sbAt(t, img, live)
	img[sb.FreeMapOff+sb.FreeMapLen-1] ^= 1
	c := openImage(t, fx, img, "damaged-map")
	if c.FreeMapRebuilt() == nil {
		t.Fatal("the damaged map was not reported")
	}
	if free, retired, _ := spaces(c); !free.contains(to) || retired.intersects(to) {
		t.Fatalf("the rebuilt map: the hole %+v free=%v retired=%v", to, free.contains(to), retired.intersects(to))
	}
	plan := c.PlanReclaim(0)
	if len(plan.Moves) != 1 || plan.Moves[0] != m.plan.Moves[0] || plan.NeedsPublish {
		t.Fatalf("the plan over the rebuilt map is %+v, the plan over the intact one was %+v", plan.Moves, m.plan.Moves)
	}
	if _, err := c.MoveExtents(ctx, plan.Moves, nil); err != nil {
		t.Fatal(err)
	}
	if c.FreeMapRebuilt() != nil {
		t.Error("the commit did not clear the rebuilt-map report")
	}
	if got := extentOf(t, c, m.last.ID); got != to {
		t.Errorf("last.bin lies at %+v, the move went to %+v", got, to)
	}
	readsAll(t, c, "after the move", m.files())
	insideTheFile(t, c)
	disjoint(t, c)
	d := reread(t, &fixture{path: c.path, archiveID: fx.archiveID, key: fx.key, opts: fx.opts})
	if d.FreeMapRebuilt() != nil || d.Stale() != nil {
		t.Errorf("reopened: map=%v stale=%v", d.FreeMapRebuilt(), d.Stale())
	}
	readsAll(t, d, "reopened", m.files())
	insideTheFile(t, d)
	disjoint(t, d)
}

// Close is the kill switch (APP.md §2.3): called under a copy in flight —
// here from the progress callback, which runs without the Archive's lock —
// it ends the move with an error, not a panic, publishes nothing, and
// leaves the file on the original state with the chunks that landed lying
// in free space; a fresh handle then plans the same move and makes it.
func TestCloseDuringTheCopyAbortsTheMove(t *testing.T) {
	a, fx, keep, last, keepData, lastData, plan := chunkedFixture(t, 830)
	m := plan.Moves[0]
	img := snapshot(t, fx.path)
	seq := a.Seq()
	var calls int
	_, err := a.MoveExtents(ctx, plan.Moves, func(done, total uint64) {
		if calls++; calls == 2 {
			a.Close()
		}
	})
	if err == nil {
		t.Fatal("the move went through on a closed handle")
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, ErrIndeterminate) {
		t.Errorf("the closed handle's move ended with %v", err)
	}
	if calls != 2 {
		t.Errorf("the copy went on for %d chunks after Close", calls)
	}
	if _, err := a.MoveExtents(ctx, plan.Moves, nil); !errors.Is(err, ErrClosed) {
		t.Errorf("MoveExtents after Close: %v", err)
	}
	after := snapshot(t, fx.path)
	if len(after) != len(img) {
		t.Errorf("the file is %d bytes; it was %d, and a closed handle truncates nothing", len(after), len(img))
	}
	for _, off := range []uint64{format.CopyA.ArchiveSuperblockOff(), format.CopyB.ArchiveSuperblockOff()} {
		if !bytes.Equal(after[off:off+format.SuperblockSize], img[off:off+format.SuperblockSize]) {
			t.Error("a superblock changed under a move that did not commit")
		}
	}
	if !bytes.Equal(after[m.From.Off:end(m.From)], img[m.From.Off:end(m.From)]) {
		t.Error("the source was written")
	}
	b := fx.open(t)
	if b.Seq() != seq || b.Stale() != nil || b.FreeMapRebuilt() != nil {
		t.Errorf("reopened: seq %d (want %d) stale=%v map=%v", b.Seq(), seq, b.Stale(), b.FreeMapRebuilt())
	}
	if got := extentOf(t, b, last.ID); got != m.From {
		t.Errorf("reopened, the record lies at %+v; it was at %+v", got, m.From)
	}
	if free, _, _ := spaces(b); !free.contains(m.To) {
		t.Errorf("the landed chunks at %+v are not free space", m.To)
	}
	want := []wantFile{{keep.ID, keepData}, {last.ID, lastData}}
	readsAll(t, b, "reopened", want)
	insideTheFile(t, b)
	disjoint(t, b)
	again := b.PlanReclaim(0)
	if !sameMoves(again.Moves, plan.Moves) {
		t.Errorf("the plan changed: %+v → %+v", plan.Moves, again.Moves)
	}
	if _, err := b.MoveExtents(ctx, again.Moves, nil); err != nil {
		t.Fatal(err)
	}
	if s := sizeOnDisk(t, fx.path); s >= uint64(len(img)) {
		t.Errorf("the move on the fresh handle gave nothing back: %d bytes, the file was %d", s, len(img))
	}
	readsAll(t, b, "after the move", want)
	insideTheFile(t, b)
	disjoint(t, b)
	fallbackReads(t, fx, snapshot(t, fx.path), "after-the-move", want)
}

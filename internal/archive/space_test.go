package archive

import (
	"math/rand/v2"
	"sort"
	"testing"
	"time"

	"github.com/dreamxwarden01/enfold/internal/format"
)

// The free-space set's own operations (space.go), against the
// implementation they replaced on 2026-11-03, kept here as the oracle: union
// subtracted the whole set from the new extent and inserted what was left,
// and subtract removed the other set's extents one at a time. The new ones
// look only at the neighbours (union) or walk both sets once (subtract); the
// answers must be the same, extent for extent, and the set must stay
// sorted, disjoint and coalesced — no two extents adjacent — whatever the
// layout.

// oldSpace is the set as it was before 2026-11-03, word for word but for the
// receiver's type: the oracle.
type oldSpace struct{ x []extent }

func (s *oldSpace) insert(e extent) {
	if e.Len == 0 {
		return
	}
	i := sort.Search(len(s.x), func(i int) bool { return s.x[i].Off >= e.Off })
	if i > 0 && end(s.x[i-1]) == e.Off {
		s.x[i-1].Len += e.Len
		e = s.x[i-1]
		i--
		s.x = append(s.x[:i], s.x[i+1:]...)
	}
	if i < len(s.x) && end(e) == s.x[i].Off {
		e.Len += s.x[i].Len
		s.x = append(s.x[:i], s.x[i+1:]...)
	}
	s.x = append(s.x, extent{})
	copy(s.x[i+1:], s.x[i:])
	s.x[i] = e
}

func (s *oldSpace) remove(e extent) bool {
	if e.Len == 0 {
		return true
	}
	i := sort.Search(len(s.x), func(i int) bool { return end(s.x[i]) > e.Off })
	if i >= len(s.x) || s.x[i].Off > e.Off || end(s.x[i]) < end(e) {
		return false
	}
	host := s.x[i]
	s.x = append(s.x[:i], s.x[i+1:]...)
	if e.Off > host.Off {
		s.insert(extent{Off: host.Off, Len: e.Off - host.Off})
	}
	if end(e) < end(host) {
		s.insert(extent{Off: end(e), Len: end(host) - end(e)})
	}
	return true
}

func (s *oldSpace) removeOverlap(e extent) {
	if e.Len == 0 {
		return
	}
	for {
		i := sort.Search(len(s.x), func(i int) bool { return end(s.x[i]) > e.Off })
		if i >= len(s.x) || s.x[i].Off >= end(e) {
			return
		}
		cut := extent{Off: max(s.x[i].Off, e.Off)}
		cut.Len = min(end(s.x[i]), end(e)) - cut.Off
		s.remove(cut)
	}
}

func (s *oldSpace) subtract(o *oldSpace) {
	for _, e := range o.x {
		s.removeOverlap(e)
	}
}

func (s *oldSpace) union(e extent) {
	if e.Len == 0 {
		return
	}
	t := &oldSpace{}
	t.insert(e)
	t.subtract(s)
	for _, p := range t.x {
		s.insert(p)
	}
}

// coalesced fails the test unless the set is sorted, every extent is
// non-empty, and no two touch.
func coalesced(t *testing.T, what string, xs []extent) {
	t.Helper()
	for i, e := range xs {
		if e.Len == 0 {
			t.Fatalf("%s: extent %d is empty: %v", what, i, xs)
		}
		if i > 0 && end(xs[i-1]) >= e.Off {
			t.Fatalf("%s: extents %d and %d touch or overlap: %v", what, i-1, i, xs)
		}
	}
}

func sameExtents(t *testing.T, what string, got, want []extent) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d extents, the oracle %d\n got %v\nwant %v", what, len(got), len(want), got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s: extent %d is %v, the oracle %v", what, i, got[i], want[i])
		}
	}
}

// randomExtent is an extent of a small address space, so that random ones
// meet, overlap, touch and contain each other often.
func randomExtent(r *rand.Rand, span uint64) extent {
	off := r.Uint64N(span)
	return extent{Off: off, Len: 1 + r.Uint64N(min(span/8, span-off))}
}

func TestSpaceUnionAndSubtractMatchTheOldImplementation(t *testing.T) {
	r := rand.New(rand.NewPCG(2026, 1103))
	for round := 0; round < 400; round++ {
		span := uint64(64 + r.IntN(4096))
		s, old := &space{}, &oldSpace{}
		var all []extent
		for i, n := 0, r.IntN(200); i < n; i++ {
			e := randomExtent(r, span)
			if r.IntN(8) == 0 {
				e.Len = 0 // the empty extent changes nothing
			}
			all = append(all, e)
			s.union(e)
			old.union(e)
			coalesced(t, "union", s.x)
			sameExtents(t, "union", s.x, old.x)
		}
		// spaceOf is the same union taken in one sorted pass.
		sameExtents(t, "spaceOf", spaceOf(all).x, old.x)

		o, oldO := &space{}, &oldSpace{}
		for i, n := 0, r.IntN(120); i < n; i++ {
			e := randomExtent(r, span)
			o.union(e)
			oldO.union(e)
		}
		s.subtract(o)
		old.subtract(oldO)
		coalesced(t, "subtract", s.x)
		sameExtents(t, "subtract", s.x, old.x)

		// remove of a piece of one extent, whole or in part, at either end
		// or in the middle.
		for i := 0; i < 20 && len(s.x) > 0; i++ {
			h := s.x[r.IntN(len(s.x))]
			a := h.Off + r.Uint64N(h.Len)
			e := extent{Off: a, Len: 1 + r.Uint64N(end(h)-a)}
			if !s.remove(e) || !old.remove(e) {
				t.Fatalf("remove %v of %v refused", e, h)
			}
			coalesced(t, "remove", s.x)
			sameExtents(t, "remove", s.x, old.x)
		}
		if s.remove(extent{Off: span + 10, Len: 1}) {
			t.Fatal("remove of an extent outside the set was accepted")
		}
	}
}

// The layouts the reclaim study measured (metadata-and-figures.md section
// 2), built in memory the way its bench built them: n files of one size
// packed from the data start, those del picks deleted and left as holes, the
// index at some 300 bytes a record and the map after it. No file is written.
func syntheticLayout(n int, sz uint64, del func(i int) bool) *reclaimState {
	off := uint64(format.ArchiveDataStart)
	s := &reclaimState{retired: &space{}, held: &space{}, keepMin: format.ArchiveDataStart}
	var holes []extent
	for i := 0; i < n; i++ {
		e := extent{Off: off, Len: sz}
		if del(i) {
			holes = append(holes, e)
		} else {
			var id [16]byte
			id[0], id[1], id[2], id[3] = byte(i), byte(i>>8), byte(i>>16), byte(i>>24)
			s.live = append(s.live, source{id, e})
		}
		off += sz
	}
	s.holes = spaceOf(holes)
	s.ilen = uint64(n)*300 + format.TagSize
	s.mapFloor = 4 + 16*uint64(len(s.holes.x))
	s.size = off + s.ilen + s.mapFloor
	return s
}

// planOf is what PlanReclaim makes of a snapshot: the commit it answers
// for, then the run under the budget.
func planOf(s *reclaimState, budget uint64) (moved, back uint64, commits int, complete bool) {
	s.step(s.holes.clone(), 0)
	return s.estimateRun(budget)
}

// withinASecond fails the test when f takes a second or more; the race
// detector slows the walk several times over, and is allowed ten.
func withinASecond(t *testing.T, what string, f func()) {
	t.Helper()
	limit := time.Second
	if raceOn {
		limit *= 10
	}
	t0 := time.Now()
	f()
	d := time.Since(t0)
	t.Logf("%s: %v", what, d)
	if d >= limit {
		t.Errorf("%s took %v; the bound is %v", what, d, limit)
	}
}

// A plan over 50 000 holes — every other file of 100 000 deleted, which one
// selection can do — took 71 s before union was made local (the study's
// measurement). It is made after the commit now, off the edit's path, but
// every commit still makes one, and it must take under a second.
func TestAPlanOverFiftyThousandHolesTakesUnderASecond(t *testing.T) {
	s := syntheticLayout(100_000, 256<<10, func(i int) bool { return i%2 == 0 })
	if len(s.holes.x) != 50_000 {
		t.Fatalf("the layout has %d holes", len(s.holes.x))
	}
	var moved uint64
	withinASecond(t, "plan, 50k holes", func() { moved, _, _, _ = planOf(s, 64<<20) })
	if moved == 0 {
		t.Error("the plan over 50 000 holes moves nothing")
	}
}

// 100 000 live runs that never touch — 200 000 files with every other one
// deleted — cost reclaimTail's union pass 8.7 s on every commit before
// 2026-11-03. The plan, followUpAt's run (the same set and the same map
// size reclaimTail computes) and a union taken one run at a time must each
// take under a second now.
func TestAHundredThousandSeparatedRunsTakeUnderASecond(t *testing.T) {
	s := syntheticLayout(200_000, 32<<10, func(i int) bool { return i%2 == 1 })
	if len(s.live) != 100_000 {
		t.Fatalf("the layout has %d live files", len(s.live))
	}
	withinASecond(t, "plan, 100k separated runs", func() { planOf(s, 64<<20) })

	index := &format.Index{}
	for _, f := range s.live {
		index.Files = append(index.Files, format.FileRecord{FileID: f.id, State: format.FileLive, DataOff: f.e.Off, StoredSize: f.e.Len})
	}
	var run extent
	withinASecond(t, "followUpAt, 100k separated runs", func() { run = (&Archive{}).followUpAt(index, s.ilen) })
	last := s.live[len(s.live)-1].e
	if run.Off != end(last) {
		t.Errorf("the follow-up's run starts at 0x%x, the last live byte ends at 0x%x", run.Off, end(last))
	}
	// A hole after each live run but the last: the map the follow-up writes
	// lists 99 999 of them.
	if want := s.ilen + 4 + 16*99_999; run.Len != want {
		t.Errorf("the follow-up's run is %d bytes, want %d", run.Len, want)
	}
	withinASecond(t, "union, 100k separated runs one at a time", func() {
		live := &space{}
		for _, f := range s.live {
			live.union(f.e)
		}
		if len(live.x) != 100_000 {
			t.Errorf("%d runs", len(live.x))
		}
	})
}

// The layout the old estimate answered wrongly in silence: 10 000 files of
// 4 MiB with every tenth deleted. The whole run moves some 36 GB to give
// about 4 GB back, longer than the dry run's bounds let it walk, and the walk
// used to stop with 3.7 GB moved and nothing back and say nothing of it — a
// figure a page would have shown as nothing to reclaim. It says now that it
// was cut short, and the page shows no in-place figure (APP.md §2.3). A short
// run on the same kind of layout is walked to its end and says so.
func TestAnEstimateCutShortSaysSo(t *testing.T) {
	s := syntheticLayout(10_000, 4<<20, func(i int) bool { return i%10 == 0 })
	var moved, back uint64
	var complete bool
	withinASecond(t, "plan, every tenth of 10 000 deleted", func() { moved, back, _, complete = planOf(s, 64<<20) })
	t.Logf("every tenth deleted: %d MiB moved, %d MiB back, complete %v", moved>>20, back>>20, complete)
	if complete {
		t.Fatalf("the walk of a 36 GB run says it reached the end: %d moved, %d back", moved, back)
	}
	if moved == 0 {
		t.Error("the walk moved nothing")
	}

	// The tail of the same archive deleted: nothing to move, the tail back,
	// walked to the end.
	tail := syntheticLayout(10_000, 4<<20, func(i int) bool { return i >= 9_900 })
	moved, back, _, complete = planOf(tail, 64<<20)
	if !complete || moved != 0 || back < 399<<20 {
		t.Errorf("the free tail: %d moved, %d back, complete %v", moved, back, complete)
	}
	// Fifty files deleted under fifty more: the run moves the fifty down
	// and gives the hole back, walked to the end.
	mid := syntheticLayout(10_000, 4<<20, func(i int) bool { return i >= 9_900 && i < 9_950 })
	moved, back, _, complete = planOf(mid, 64<<20)
	t.Logf("fifty deleted under fifty: %d MiB moved, %d MiB back, complete %v", moved>>20, back>>20, complete)
	if !complete || moved != 200<<20 || back < 199<<20 {
		t.Errorf("fifty deleted under fifty: %d moved, %d back, complete %v", moved, back, complete)
	}
}

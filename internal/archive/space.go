package archive

import (
	"sort"

	"github.com/dreamxwarden01/enfold/internal/format"
)

// extent is a half-open byte range [Off, Off+Len) of the file.
type extent = format.Extent

func end(e extent) uint64 { return e.Off + e.Len }

func overlaps(a, b extent) bool {
	return a.Len != 0 && b.Len != 0 && a.Off < end(b) && b.Off < end(a)
}

// space is a set of disjoint extents kept sorted by offset and coalesced:
// no two are adjacent. It serves as the published free map, the allocation
// pool, and the quarantine sets.
type space struct {
	x []extent
}

func newSpace(extents []extent) *space {
	s := &space{}
	for _, e := range extents {
		s.insert(e)
	}
	return s
}

func (s *space) clone() *space {
	return &space{x: append([]extent(nil), s.x...)}
}

func (s *space) extents() []extent { return append([]extent(nil), s.x...) }

func (s *space) total() uint64 {
	var n uint64
	for _, e := range s.x {
		n += e.Len
	}
	return n
}

// insert adds e, merging with neighbours. e must not overlap anything
// present: that is an invariant violation in this package's own
// bookkeeping, not a condition file contents can produce — file-derived
// extents go through union.
func (s *space) insert(e extent) {
	if e.Len == 0 {
		return
	}
	i := sort.Search(len(s.x), func(i int) bool { return s.x[i].Off >= e.Off })
	if (i > 0 && overlaps(s.x[i-1], e)) || (i < len(s.x) && overlaps(s.x[i], e)) {
		panic("archive: extent overlaps the space it is inserted into")
	}
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

// union adds whatever part of e the set does not already hold. It tolerates
// overlap, which is what extents taken from a file need. Only the extents
// that touch e are looked at — the first that ends at or after e begins and
// those up to the last that begins at or before e ends, found by binary
// search — and they are folded into one with e, so the set stays coalesced.
// It subtracted the whole set from e until 2026-11-03, which made every call
// linear in the set and a plan over 50 000 holes take 71 s (the reclaim
// study's analyst, metadata-and-figures.md section 2).
func (s *space) union(e extent) {
	if e.Len == 0 {
		return
	}
	// i: the first extent that is not wholly before e and apart from it.
	i := sort.Search(len(s.x), func(i int) bool { return end(s.x[i]) >= e.Off })
	// j: the first extent at or after i that lies wholly after e and apart.
	j := i + sort.Search(len(s.x)-i, func(k int) bool { return s.x[i+k].Off > end(e) })
	if i == j {
		s.x = append(s.x, extent{})
		copy(s.x[i+1:], s.x[i:])
		s.x[i] = e
		return
	}
	lo, hi := min(e.Off, s.x[i].Off), max(end(e), end(s.x[j-1]))
	s.x[i] = extent{Off: lo, Len: hi - lo}
	s.x = append(s.x[:i+1], s.x[j:]...)
}

// spaceOf is the union of extents in any order, overlapping or not: sorted
// once and coalesced in one pass, so that a set built from every live file
// of an archive costs n log n rather than n insertions into the middle of a
// growing slice (reclaimTail, followUpAt and the plan's step build theirs
// this way).
func spaceOf(extents []extent) *space {
	xs := make([]extent, 0, len(extents))
	for _, e := range extents {
		if e.Len != 0 {
			xs = append(xs, e)
		}
	}
	sort.Slice(xs, func(i, j int) bool { return xs[i].Off < xs[j].Off })
	out := &space{x: xs[:0]}
	for _, e := range xs {
		if n := len(out.x); n > 0 && e.Off <= end(out.x[n-1]) {
			if last := &out.x[n-1]; end(e) > end(*last) {
				last.Len = end(e) - last.Off
			}
			continue
		}
		out.x = append(out.x, e)
	}
	return out
}

// merged returns s ∪ o as a fresh set, in one linear pass over the two
// sorted operands — inserting one at a time is quadratic when they
// interleave, which a hostile free map can arrange.
func (s *space) merged(o *space) *space {
	out := &space{x: make([]extent, 0, len(s.x)+len(o.x))}
	push := func(e extent) {
		if e.Len == 0 {
			return
		}
		if n := len(out.x); n > 0 {
			last := &out.x[n-1]
			if e.Off <= end(*last) {
				// Overlapping or adjacent: extend.
				if end(e) > end(*last) {
					last.Len = end(e) - last.Off
				}
				return
			}
		}
		out.x = append(out.x, e)
	}
	i, j := 0, 0
	for i < len(s.x) || j < len(o.x) {
		if j == len(o.x) || (i < len(s.x) && s.x[i].Off <= o.x[j].Off) {
			push(s.x[i])
			i++
		} else {
			push(o.x[j])
			j++
		}
	}
	return out
}

// remove takes e out; e must lie within one extent of the set. What is left
// of the host is written in its place, and an extent taken whole is dropped
// from the nearer end of the slice: a plan takes its holes lowest first, and
// shifting everything above the lowest hole for every one it spends made a
// step over many holes quadratic.
func (s *space) remove(e extent) bool {
	if e.Len == 0 {
		return true
	}
	i := sort.Search(len(s.x), func(i int) bool { return end(s.x[i]) > e.Off })
	if i >= len(s.x) || s.x[i].Off > e.Off || end(s.x[i]) < end(e) {
		return false
	}
	host := s.x[i]
	left := extent{Off: host.Off, Len: e.Off - host.Off}
	right := extent{Off: end(e), Len: end(host) - end(e)}
	switch {
	case left.Len > 0 && right.Len > 0:
		s.x[i] = left
		s.x = append(s.x, extent{})
		copy(s.x[i+2:], s.x[i+1:])
		s.x[i+1] = right
	case left.Len > 0:
		s.x[i] = left
	case right.Len > 0:
		s.x[i] = right
	default:
		s.drop(i)
	}
	return true
}

// drop deletes the i-th extent, moving whichever side of it is shorter.
func (s *space) drop(i int) {
	if i < len(s.x)/2 {
		copy(s.x[1:i+1], s.x[:i])
		s.x = s.x[1:]
		return
	}
	s.x = append(s.x[:i], s.x[i+1:]...)
}

// contains reports whether e lies within one extent of the set.
func (s *space) contains(e extent) bool {
	if e.Len == 0 {
		return true
	}
	i := sort.Search(len(s.x), func(i int) bool { return end(s.x[i]) > e.Off })
	return i < len(s.x) && s.x[i].Off <= e.Off && end(s.x[i]) >= end(e)
}

// intersects reports whether e overlaps any extent of the set.
func (s *space) intersects(e extent) bool {
	if e.Len == 0 {
		return false
	}
	i := sort.Search(len(s.x), func(i int) bool { return end(s.x[i]) > e.Off })
	return i < len(s.x) && s.x[i].Off < end(e)
}

// firstFit takes the lowest extent of at least n bytes, removing what it
// takes, and reports whether it found one.
func (s *space) firstFit(n uint64) (extent, bool) {
	for _, e := range s.x {
		if e.Len >= n {
			got := extent{Off: e.Off, Len: n}
			s.remove(got)
			return got, true
		}
	}
	return extent{}, false
}

// firstFitBefore takes the lowest n bytes that end at or before limit —
// the earliest hole wholly before a source that holds it, which is where R40
// places a move — removing what it takes, and reports whether it found any.
// It is firstFit with a ceiling: the set is sorted, so the first extent that
// cannot end in time ends the search.
func (s *space) firstFitBefore(n, limit uint64) (extent, bool) {
	for _, e := range s.x {
		if e.Off+n > limit {
			break
		}
		if e.Len >= n {
			got := extent{Off: e.Off, Len: n}
			s.remove(got)
			return got, true
		}
	}
	return extent{}, false
}

// exactOrFirstFit prefers an extent of exactly n bytes, then the first that
// fits; exact fits keep the map from fragmenting into unusable tails.
func (s *space) exactOrFirstFit(n uint64) (extent, bool) {
	for _, e := range s.x {
		if e.Len == n {
			s.remove(e)
			return e, true
		}
	}
	return s.firstFit(n)
}

// subtract removes every part of s that lies within o, in one pass over the
// two sorted sets: each extent of s is cut by the extents of o that overlap
// it, which a pointer into o finds without going back, since both are
// sorted. It removed o's extents one at a time until 2026-11-03, a binary
// search and a shift of the slice each — the cost union paid on every call
// (above). What is left of a coalesced set stays coalesced: cutting only
// opens gaps.
func (s *space) subtract(o *space) {
	if len(s.x) == 0 || len(o.x) == 0 {
		return
	}
	out := make([]extent, 0, len(s.x))
	j := 0
	for _, a := range s.x {
		cur, stop := a.Off, end(a)
		// Nothing of o that ends at or before a begins can cut a, or any
		// extent of s after it.
		for j < len(o.x) && end(o.x[j]) <= cur {
			j++
		}
		for k := j; k < len(o.x) && o.x[k].Off < stop && cur < stop; k++ {
			b := o.x[k]
			if b.Len == 0 {
				continue
			}
			if b.Off > cur {
				out = append(out, extent{Off: cur, Len: b.Off - cur})
			}
			cur = max(cur, end(b))
		}
		if cur < stop {
			out = append(out, extent{Off: cur, Len: stop - cur})
		}
	}
	s.x = out
}

// removeOverlap removes the intersection of e with the set, whatever shape
// it has.
func (s *space) removeOverlap(e extent) {
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

// freeMap returns the set as the format's free map.
func (s *space) freeMap() *format.FreeMap {
	return &format.FreeMap{Extents: s.extents()}
}

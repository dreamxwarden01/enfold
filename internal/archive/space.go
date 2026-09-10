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
// overlap, which is what extents taken from a file need.
func (s *space) union(e extent) {
	if e.Len == 0 {
		return
	}
	t := newSpace([]extent{e})
	t.subtract(s)
	for _, p := range t.x {
		s.insert(p)
	}
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

// remove takes e out; e must lie within one extent of the set.
func (s *space) remove(e extent) bool {
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

// subtract removes every part of s that lies within o.
func (s *space) subtract(o *space) {
	for _, e := range o.x {
		s.removeOverlap(e)
	}
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

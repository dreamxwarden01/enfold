package app

import (
	"bytes"
	"sort"
	"strings"
	"sync"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// The order of a directory listing (APP.md §3 Page, ruled 2026-09-10). The
// sort is the core's so that paging stays consistent: every page of a long
// directory comes in one order, and Children answers the folder's ids in
// that same order.

// sortOrder is Page's sort parsed: the column, its direction, and the
// direction of the name tie-break — the Name column's own state, which the
// page keeps while another column sorts.
type sortOrder struct {
	key      string // name | size | type | modified
	desc     bool
	nameDesc bool
}

// parseSortOrder reads the sort grammar of APP.md §3: one or two signed keys,
// comma-separated — the column (`name`, `size`, `type` or `modified`, a
// leading `-` for descending), then optionally `name` or `-name` for the
// tie-break's direction. The empty string is `name`. With `name` as the
// column the second key is redundant — the primary already orders the names
// — and the grammar allows it either way (the outside review's finding 15),
// so it is read and the primary's direction wins. Anything else is false,
// and the caller answers params.
func parseSortOrder(s string) (sortOrder, bool) {
	if s == "" {
		return sortOrder{key: "name"}, true
	}
	parts := strings.Split(s, ",")
	if len(parts) > 2 {
		return sortOrder{}, false
	}
	key, desc, ok := signedKey(parts[0])
	if !ok {
		return sortOrder{}, false
	}
	switch key {
	case "name", "size", "type", "modified":
	default:
		return sortOrder{}, false
	}
	o := sortOrder{key: key, desc: desc}
	if key == "name" {
		o.nameDesc = desc
	}
	if len(parts) == 2 {
		tie, tieDesc, ok := signedKey(parts[1])
		if !ok || tie != "name" {
			return sortOrder{}, false
		}
		if key != "name" {
			o.nameDesc = tieDesc
		}
	}
	return o, true
}

// signedKey splits one key into its column and its direction. An empty
// column — "", "-", or the empty half of "size," — is not a key.
func signedKey(s string) (key string, desc bool, ok bool) {
	key, desc = strings.CutPrefix(s, "-")
	return key, desc, key != ""
}

// nameCollator is the `name` order: a general collation, the root locale
// of x/text/collate with Numeric and IgnoreCase (APP.md §3, checked on the
// machine 2026-09-10) — runs of digits by value, accents secondary, case
// ignored, scripts in the Unicode collation's order and Han by code point
// within each block — and never a locale's, which would differ by machine.
// The collator keeps iterator state of its own and is not safe for
// concurrent use, so it is one per core behind its own mutex, built once:
// New reads the tables in, which is the part worth doing once.
type nameCollator struct {
	mu sync.Mutex
	c  *collate.Collator
}

func newNameCollator() *nameCollator {
	return &nameCollator{c: collate.New(language.Und, collate.Numeric, collate.IgnoreCase)}
}

// keys is the collation key of every record's name, in the records' order.
// The keys are compared as bytes, which for a directory of fifty thousand
// rows costs one collation per name rather than one per comparison. One
// Buffer holds them all: a key is a slice into the buffer's allocation and
// stays valid until the buffer is reset, which this never does.
func (n *nameCollator) keys(recs []*mergedRec) [][]byte {
	n.mu.Lock()
	defer n.mu.Unlock()
	var buf collate.Buffer
	out := make([][]byte, len(recs))
	for i, r := range recs {
		out[i] = n.c.KeyFromString(&buf, r.name)
	}
	return out
}

// sortRec is one record with what the comparison needs of it resolved.
type sortRec struct {
	r   *mergedRec
	key []byte // the collation key of the name
	ext string // the folded extension, for `type`
}

// sortedKids orders one directory's children as APP.md §3 says: directories
// before files under every key and both directions, then the key, then the
// name in the tie-break's direction. Two names equal under the collation are
// ordered by code point, so the order is total and a page boundary never
// falls between two rows the sort cannot tell apart. The returned slice is
// the caller's; kids is not reordered.
func (n *nameCollator) sortedKids(kids []*mergedRec, o sortOrder) []*mergedRec {
	recs := make([]sortRec, len(kids))
	for i, k := range kids {
		recs[i] = sortRec{r: k}
		if !k.isDir {
			recs[i].ext = extensionOf(k.name)
		}
	}
	for i, key := range n.keys(kids) {
		recs[i].key = key
	}
	byName := func(a, b *sortRec) int {
		if c := bytes.Compare(a.key, b.key); c != 0 {
			return c
		}
		return strings.Compare(a.r.name, b.r.name) // UTF-8 byte order is code-point order
	}
	sort.SliceStable(recs, func(i, j int) bool {
		a, b := &recs[i], &recs[j]
		if a.r.isDir != b.r.isDir {
			return a.r.isDir
		}
		switch o.key {
		case "name":
			if c := byName(a, b); c != 0 {
				return (c < 0) != o.desc
			}
		case "size":
			// A directory shows no size, so the key does not order them:
			// they stay first in both directions and fall to the name.
			if !a.r.isDir && a.r.size != b.r.size {
				return (a.r.size < b.r.size) != o.desc
			}
		case "type":
			// The folded extensions in plain string order. A file with none
			// — a dotfile, a bare name — is first in both directions, as a
			// directory is: it has no type to take part in the order.
			if (a.ext == "") != (b.ext == "") {
				return a.ext == ""
			}
			if c := strings.Compare(a.ext, b.ext); c != 0 {
				return (c < 0) != o.desc
			}
		case "modified":
			if a.r.modifiedAt != b.r.modifiedAt {
				return (a.r.modifiedAt < b.r.modifiedAt) != o.desc
			}
		}
		if c := byName(a, b); c != 0 {
			return (c < 0) != o.nameDesc
		}
		return false
	})
	out := make([]*mergedRec, len(recs))
	for i := range recs {
		out[i] = recs[i].r
	}
	return out
}

// extensionOf is the `type` key of a name, the rule APP.md §6 gives the list:
// the part after the last dot, case-folded; none when the dot is the first
// character (`.env`) or there is no dot; `gz` for `a.tar.gz`. A directory
// has none, whatever its name says.
func extensionOf(name string) string {
	i := strings.LastIndexByte(name, '.')
	if i <= 0 {
		return ""
	}
	return strings.ToLower(name[i+1:])
}

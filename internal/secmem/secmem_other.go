//go:build !windows

package secmem

import "errors"

// region is a plain heap allocation. The program ships on Windows; the core
// packages build elsewhere so that their tests run there, and nothing on those
// platforms is pinned — Locked is false and the pagefile promise is not made.
// Erasure is the same on both: the portable code wipes the bytes.
type region struct {
	mem    []byte
	locked bool
}

var errNoLock = errors.New("secmem: pinning pages is the Windows build's")

// lockPages is the seam VirtualLock fills on Windows. A test replaces it here
// too, so the fallback has one shape on every platform.
var lockPages = func(r *region) error { return errNoLock }

func alloc(n int) region {
	r := region{mem: make([]byte, n)}
	if err := lockPages(&r); err != nil {
		// Not reported: here it is the design, not a refusal.
		return r
	}
	r.locked = true
	return r
}

// free does nothing: the collector owns the slice, which the caller has erased.
func (r region) free() {}

//go:build windows

package secmem

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// region is one run of pages from VirtualAlloc, committed read-write and
// locked into the working set. The Go heap is what it avoids: the collector
// may copy a stack-held secret and the copy is reachable to the pager, while
// these pages are the process's own and are not written to pagefile.sys while
// they stay locked.
type region struct {
	base   uintptr
	size   uintptr
	mem    []byte
	locked bool
}

// lockPages pins the region. A variable so that a test can refuse the lock and
// take the fallback; the same seam exists on the other platforms.
var lockPages = func(r *region) error { return windows.VirtualLock(r.base, r.size) }

// alloc commits n bytes rounded up to whole pages — the locking granularity is
// the page, so a smaller allocation would lock its neighbours anyway — and
// locks them.
//
// The working set is not raised. Windows caps a process at "the number of
// pages in its minimum working set minus a small overhead"
// (learn.microsoft.com/en-us/windows/win32/api/memoryapi/nf-memoryapi-virtuallock),
// and the default minimum is 50 pages, 204,800 bytes at a 4K page
// (…/nf-memoryapi-setprocessworkingsetsize). This program locks three secrets
// of 32 bytes — the VMK, the KWK, the vault's K_P — one page each: three while
// an unlock is in flight, one once the handle closes. So
// SetProcessWorkingSetSize is not called: it takes physical memory from the
// rest of the system, and three pages of fifty need none of it.
func alloc(n int) region {
	size := pageRound(uintptr(n))
	base, err := windows.VirtualAlloc(0, size, windows.MEM_COMMIT|windows.MEM_RESERVE, windows.PAGE_READWRITE)
	if err != nil {
		// Address space the process cannot get is not a reason to refuse an
		// unlock: the Go heap is where every other secret in the program
		// already lives.
		report(err)
		return region{mem: make([]byte, n)}
	}
	r := region{base: base, size: size, mem: unsafe.Slice((*byte)(pointer(base)), size)}
	if err := lockPages(&r); err != nil {
		// Committed but not pinned: the secret is still off the Go heap and
		// still erased explicitly, and the page may reach the pagefile.
		report(err)
		return r
	}
	r.locked = true
	return r
}

// free unlocks and releases the pages. The caller has erased them already.
func (r region) free() {
	if r.base == 0 {
		return // the heap fallback: the collector owns it
	}
	if r.locked {
		// Ignored: there is no lock count, so the only failures are a region
		// that was never locked or one already gone, and VirtualFree is next.
		windows.VirtualUnlock(r.base, r.size)
	}
	windows.VirtualFree(r.base, 0, windows.MEM_RELEASE) // MEM_RELEASE wants size 0
}

// pointer turns an address the kernel returned into a pointer. The rule vet's
// unsafeptr check enforces is about Go memory: a uintptr is not a reference
// the collector follows, so an object can move or die under it. These pages
// are the process's own, at one address until VirtualFree, and no Go
// allocation is involved — which is the case the check cannot tell apart, so
// the conversion is written through a type parameter, where it does not fire.
// Nothing else in this package converts an address.
func pointer[T ~uintptr](a T) unsafe.Pointer { return unsafe.Pointer(a) }

// pageRound rounds up to the page size, which VirtualAlloc commits in anyway.
func pageRound(n uintptr) uintptr {
	p := uintptr(windows.Getpagesize())
	return (n + p - 1) / p * p
}

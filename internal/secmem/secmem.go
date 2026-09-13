// Package secmem holds a retained secret in pages the process allocates
// outside the Go heap, pinned with VirtualLock on Windows and erased
// explicitly when the owner is done with them.
//
// It exists for three values and no others (docs/SCOPE.md): the VMK, the
// session's KWK and the vault's K_P. The threat it answers is the one in
// scope — the pagefile, and a later read of the disk — not a debugger and not
// another process. A secret on the GC heap can be copied by the collector and
// paged out between the copy and the zeroing; a locked page is not written to
// pagefile.sys while it stays locked.
//
// What it does not cover, and cannot: every library the secret is handed to
// keeps its own copy — crypto/aes builds a key schedule, HKDF copies the IKM,
// Argon2 fills megabytes — and those copies are ordinary Go memory.
// Hibernation writes all of RAM whatever is locked, and a dump someone
// configured elsewhere is outside the process (DESIGN.md §2). This is the
// smaller, honest thing rather than an enclave: memguard was measured against
// it and bought no more (DECISIONS.md, 2026-09-13).
//
// A Buffer must stay reachable while its bytes are in use. The cleanup below
// releases the pages once nothing refers to the Buffer, and the bytes live
// outside the heap, where the collector cannot see that a library still holds
// them — so a caller keeps the Buffer in a field and adds runtime.KeepAlive
// wherever a slice of it crosses into a call.
package secmem

import (
	"log"
	"runtime"
	"sync"
)

// Log takes the one line this package produces: the pages are not locked. The
// default is the standard logger, so the line is never lost silently; a
// process with its own log points this at it at startup, before the first
// secret.
var Log = log.Printf

var reported sync.Once

// report says it once. The reason is the same for every secret, and a line per
// unlock would drown the log it is written to.
func report(err error) {
	reported.Do(func() {
		Log("secmem: pages are not locked, a secret can reach the pagefile: %v", err)
	})
}

// Buffer is one secret's pages. It is not safe for concurrent use beyond the
// bookkeeping below: the bytes are the owner's to serialise, as the keystore's
// Session and Unlocked already are.
type Buffer struct {
	mu      sync.Mutex
	r       region
	n       int // the bytes asked for; the region is whole pages
	zeros   int
	freed   bool
	cleanup runtime.Cleanup
}

// New allocates n bytes. It never fails: a refused allocation falls back to
// the Go heap and a refused lock leaves the pages unpinned, each reported
// once, because an unlock that failed for want of a page would be a worse
// failure than a secret that is only zeroed.
func New(n int) *Buffer {
	if n <= 0 {
		panic("secmem: New needs a positive size")
	}
	b := &Buffer{r: alloc(n), n: n}
	// The safety net for a Buffer nobody frees. It erases and releases and
	// logs nothing: it runs long after the mistake, on another goroutine,
	// where a line would say only that someone forgot. AddCleanup, not
	// SetFinalizer: it cannot resurrect the Buffer and Free can stop it.
	b.cleanup = runtime.AddCleanup(b, release, b.r)
	return b
}

// Bytes is the secret, nil once the buffer is freed. The slice points into the
// pages, so a copy taken from it is a copy the taker must erase.
func (b *Buffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.freed {
		return nil
	}
	return b.r.mem[:b.n:b.n] // capped: an append must not reach the rest of the page
}

// Zero erases the pages in place; the buffer stays usable.
func (b *Buffer) Zero() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.freed {
		return
	}
	wipe(b.r.mem)
	b.zeros++
}

// Free erases the pages, unpins them and gives them back. A second Free does
// nothing, and Bytes is nil afterwards: reading released pages faults.
func (b *Buffer) Free() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.freed {
		return
	}
	b.freed = true
	b.cleanup.Stop()
	release(b.r)
	b.zeros++
	b.r = region{}
}

// Locked reports whether the pages are pinned: false on a platform with no
// VirtualLock, and after a refusal report has already named.
func (b *Buffer) Locked() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return !b.freed && b.r.locked
}

// Zeros counts the erasures, Zero's and Free's. It is how an owner proves a
// secret was erased once the pages are gone — the only question left to ask
// then, since reading them would fault.
func (b *Buffer) Zeros() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.zeros
}

// release erases a region and gives it back. Free's body and the cleanup's, so
// that both do the same thing in the same order.
func release(r region) {
	wipe(r.mem)
	r.free()
}

// wipe overwrites b and keeps it alive across the loop. There is no
// RtlSecureZeroMemory to call — it is an inline macro in C and x/sys/windows
// declares nothing for it — so this is the pattern: the stores are to memory
// the compiler cannot prove dead, and KeepAlive holds the fallback's heap
// slice until they have run.
func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
	runtime.KeepAlive(b)
}

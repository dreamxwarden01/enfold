package archive

import "sync"

// One handle per path per process (FORMAT.md R31 as amended on 2026-09-09,
// doc.go "Handles"). R31 lets a writer truncate the tail of the file and
// forbids it to cut into an extent an open Reader still holds — and the
// Readers a handle knows about are its own, entries in its own map. A second
// handle on the same file therefore hides its Readers from the first, and a
// read-only handle hides them behind an OS lock that never sees it: the
// writable handle's trim would cut the file under a Reader it cannot see.
//
// The registry is what makes the rule checkable inside one process. Every
// Open claims the path it opened and Close gives the claim back: a path a
// writable handle holds is open to nobody else, and a path any handle holds
// is open to no writer. Two read-only handles are allowed — neither writes,
// so neither can move the ground under the other.
//
// The key is the path as the OS lock code canonicalises it (canonical),
// which on Windows folds case. Two names for one file that differ otherwise
// — a `\\?\` spelling, a junction, an 8.3 short name, a substituted drive —
// are two keys here, and what the table then catches depends on the handle. A
// second *writable* handle under another spelling is still refused, by the
// exclusive lock it takes on the file itself, as another process is. A
// read-only one is not: it takes no lock at all (archive.go, lockFile is for
// writers), so it is admitted beside the writer and its Readers are invisible
// to the trim. That handle is outside the rule, like a reader in another
// process: it fails closed on the chunk it was reading — a tag that does not
// verify, or an extent past the end of the file — and is never served the
// wrong bytes. (The keystore's table has the same hole, recorded in
// DECISIONS.md under 2026-09-06, "Keystore" — there LockFileEx is the
// fallback, and here, for a read-only handle, there is none. A real guard
// would key this map on the file identity the opened handle reports — volume
// and index — rather than on the path, at the cost of claiming only after the
// open.)
type openClaim struct {
	writer  bool
	readers int
}

var (
	openMu    sync.Mutex
	openPaths = map[string]*openClaim{}
)

// claimPath registers a handle on path and returns the key that releases it.
// ErrBusy is the refusal.
func claimPath(path string, write bool) (string, error) {
	key := canonical(path)
	openMu.Lock()
	defer openMu.Unlock()
	if c := openPaths[key]; c != nil {
		if c.writer || write {
			return "", ErrBusy
		}
		c.readers++
		return key, nil
	}
	c := &openClaim{writer: write}
	if !write {
		c.readers = 1
	}
	openPaths[key] = c
	return key, nil
}

// releasePath gives one claim back; the last one out drops the entry.
func releasePath(key string, write bool) {
	openMu.Lock()
	defer openMu.Unlock()
	c := openPaths[key]
	if c == nil {
		return
	}
	if write {
		c.writer = false
	} else if c.readers > 0 {
		c.readers--
	}
	if !c.writer && c.readers == 0 {
		delete(openPaths, key)
	}
}

package archive

import (
	"errors"
	"fmt"

	"github.com/dreamxwarden01/enfold/internal/format"
)

var (
	// ErrKey: no candidate key opened the index. The envelope's kid names
	// which key the archive expects; a wrong key, or the wrong archive, ends
	// here.
	ErrKey = errors.New("archive: no key opens this archive")
	// ErrReadOnly: the Archive was opened read-only, or the operation needs a
	// writer.
	ErrReadOnly = errors.New("archive: read-only")
	// ErrBusy: this archive is already open. One handle per path per process
	// (doc.go "Handles"): a path a writable handle holds is open to no other
	// handle, and a path any handle holds is open to no writer. Another
	// process holding the file's exclusive lock ends here too, as does an
	// operation that needs the readers closed.
	ErrBusy = errors.New("archive: already open")
	// ErrIndeterminate: a commit failed at or after its commit point; the
	// file may hold either state. Reopen it (Archive.Broken).
	ErrIndeterminate = errors.New("archive: commit outcome unknown")
	// ErrClosed: the Archive, transaction or Reader is closed.
	ErrClosed = errors.New("archive: closed")
	// ErrExists: a live child of the same parent already holds this name
	// under simple case folding (R39 — files and directories share one
	// namespace, and the platforms this project extracts to would put A.txt
	// and a.txt on one file).
	ErrExists = errors.New("archive: name already exists")
	// ErrNotFound: no live record has this ID, or an id given as a parent or
	// a destination is neither the root nor a live directory.
	ErrNotFound = errors.New("archive: no such record")
	// ErrMoveIntoSelf: a directory would be moved into itself or into one of
	// its own descendants (R39).
	ErrMoveIntoSelf = errors.New("archive: a directory cannot be moved into itself")
	// ErrTreeBounds: the change would stand a live directory more than
	// format.MaxTreeDepth below the root, or join a live record to a path
	// over format.MaxPathLen bytes. Both bounds are the whole subtree's, not
	// the named record's (R39), so a rename or a move is refused for what it
	// would do to records beneath it.
	ErrTreeBounds = errors.New("archive: the tree's depth or path bound would be exceeded")
	// ErrKindMismatch: the id names a record of the other kind — a directory
	// where a file's content is edited.
	ErrKindMismatch = errors.New("archive: the id names a record of the other kind")
	// ErrSourceChanged: the source yielded more or fewer bytes than its
	// declared size.
	ErrSourceChanged = errors.New("archive: source size differs from the declared size")
	// ErrContentHash: the extracted plaintext does not hash to the record's
	// content_hash — a bug in this program's own pipeline, or a record
	// written by a broken writer; the AEAD already rules out tampering.
	ErrContentHash = errors.New("archive: content hash mismatch")
	// ErrDictInUse: the dictionary cannot change while records reference it.
	ErrDictInUse = errors.New("archive: dictionary is referenced by stored files")
	// ErrTxOpen: a transaction is already open on this Archive.
	ErrTxOpen = errors.New("archive: a transaction is open")
	// ErrStalePlan: a move (R40) does not fit the archive's state — its
	// source is not where the plan found it, or its destination is not free
	// space the transaction may allocate: live, under R31's quarantine
	// (ReclaimPlan.NeedsPublish, Publish lifts it), held by a reader, or
	// taken by another move. A plan is made against one committed state and
	// answers for one commit; nothing was written — plan again.
	ErrStalePlan = errors.New("archive: the plan does not fit the archive's state")
	// ErrNoSpace: the file would exceed the format's bounds.
	ErrNoSpace = errors.New("archive: size limit")
	// ErrSeqExhausted: the superblock's seq is at 2^64 − 1 and the commit
	// would have to wrap it to 0, which would make the new state lose to the
	// old one. The writer refuses before anything is written (FORMAT.md §4);
	// nothing in the file changes and the archive stays usable, read and
	// write alike, for everything but a commit.
	ErrSeqExhausted = errors.New("archive: the superblock sequence is exhausted")
	// ErrParams: an argument the package refuses.
	ErrParams = errors.New("archive: invalid parameters")
	// ErrInternal: an invariant this package maintains was found broken —
	// an allocation that overlaps live data, a bound that was exceeded. The
	// transaction is abandoned; nothing was written where it should not be.
	ErrInternal = errors.New("archive: internal invariant violated")
)

// corrupt wraps a structural failure so that errors.Is(err, format.ErrInvalid)
// answers "was this file well-formed" for the whole stack.
func corrupt(msg string, a ...any) error {
	return fmt.Errorf("%w: archive: %s", format.ErrInvalid, fmt.Sprintf(msg, a...))
}

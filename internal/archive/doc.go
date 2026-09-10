// Package archive is the archive file of docs/FORMAT.md Part II: a plaintext
// envelope, two alternating superblocks, an encrypted file index, a
// plaintext free-space map, and a data region of per-file STREAM blobs.
//
// It sits on internal/format (the codecs), internal/kdf (the archive key's
// two subordinate keys and the DEK wraps), internal/stream (the per-file
// AEAD) and internal/compress (zstd and the sampling probe). It does not
// know about the keystore: the archive key is a parameter, and the caller
// looks it up in the registry by the envelope's archive_id and kid.
//
// # The index is a tree
//
// A directory is a record with an id and a file hangs off its parent by that
// id (FORMAT.md §11, R39, DESIGN.md trap 31): an empty folder exists, a
// folder's time survives, and moving or renaming a folder changes one record.
// Nothing is derived from a path and nothing is looked up by one — Children
// lists a directory's live children, Path joins a record's ancestors' names
// with its own, and format.RootID is the implicit root, which has no record.
// The transaction holds R39's invariants ahead of the work: every staged
// change that creates a record or gives a live one a new name or parent is
// checked against the index the transaction is building — one valid path
// element, a parent that is the root or a live directory, no live sibling
// folding onto the name — and re-checks that record and every live record
// beneath it against the depth and joined-path bounds, because renaming a
// folder lengthens every descendant's path and moving one re-depths all of
// it. format.Index.Encode runs the same walk over the whole index at the
// seal, which is the backstop, not the substitute: it answers a bug of our
// own at save rather than at the operation the caller performed.
//
// # Transactions
//
// Every change to an archive is a transaction ending in one superblock flip.
// Begin opens one; Add, AddDir, Replace, Delete, Rename, Move and
// SetDictionary record changes on it; Commit writes them. Deleting a
// directory tombstones it and every live record beneath it in the same write,
// the subtree taken from the transaction's own index rather than from the
// caller. The single-operation methods on Archive
// are one-transaction conveniences. Within a transaction data is written into
// extents that no superblock references — free space, or past the end of the
// file — so a crash before the flip leaves the previous state intact, and the
// flip itself is one 4 KiB write with a checksum.
//
// The free-space map has two faces. The published map, written with each
// commit and hashed into the superblock (§13), lists every extent the new
// state does not use. The allocation pool is smaller: it excludes extents
// freed by the current transaction and by the previous one — so the losing
// superblock copy, which still references what the previous commit freed,
// stays fully valid until the commit after next overwrites it, and an archive
// whose live copy is torn opens one commit behind — and it excludes extents
// an open Reader still holds. The map itself is always appended at the end
// of the file, which is what lets its own extent be known before it is
// encoded; the index goes first-fit into the pool or is appended. Nothing is
// truncated except a reservation this transaction made at the end of the
// file and did not fill. Space freed at the tail is reclaimed by Compact.
//
// A failure at or after the superblock write leaves the outcome unknown; the
// Archive then refuses every operation (ErrIndeterminate, Broken) and the
// caller reopens the file to see which state won. A failure before it
// abandons the transaction with the in-memory pool restored.
//
// # Allocation for compressed files
//
// The size of a compressed file is known only when it has been compressed.
// A file up to Options.InMemoryBelow is compressed and sealed into memory,
// so its exact stored size can be placed first-fit; a larger one is written
// straight into a reservation at the end of the file sized by
// compress.Writer.MaxEncodedSize — an upper bound — and the unused tail is
// truncated off. Raw files, whose stored size is exact (R26), go first-fit.
// Reserving the bound inside a hole would leave a hole no file could reuse.
//
// # Reading
//
// OpenReader returns an independent Reader per call, so the loopback preview
// server can serve every request from its own Reader (internal/stream
// documents why http.ServeContent's multi-range path must not share one).
// While a Reader is open, its extent is held: a Replace or Delete of the same
// file commits normally and the Reader keeps reading the content it opened —
// the extent is not reused or truncated until the Reader closes. A Reader on
// a compressed file seeks by restarting the decompression, which is enough
// for ServeContent and for scrubbing at a cost proportional to the offset.
//
// An error from a Reader means the file is unreadable, and whatever was
// produced before it is to be discarded (DESIGN.md §11 trap 17). ExtractTo
// builds into a temporary beside the target and renames it into place only
// after a clean end and a matching content hash; Extract streams into a
// caller's writer and leaves that discipline to the caller.
//
// # Keys
//
// Open takes candidates — kid and archive key pairs — and reports which one
// opened the index. That is what makes archive-key rotation (§7.3) safe: the
// caller records the new version in the registry, retiring the old, and only
// then calls RotateKey; a crash between the two leaves an archive under one
// of two keys the registry knows. The reverse order would leave, on a crash,
// an archive under a key that exists nowhere. Open never writes: an envelope
// whose kid is not the one that opened the index is reported (EnvelopeStale)
// and repaired only by RepairEnvelope. The envelope is not the way in, only
// the fast one (R33, amended after the outside audit of 2026-09-09): it is
// rewritten in place, so a crash inside that write leaves a checksum that
// fails, and a caller that passes Options.ArchiveID — the archive_id its own
// record holds — opens such a file all the same, every key tried against the
// index, whose AAD binds archive_id and kid. Only a file no key opens is
// corrupt.
//
// # Single writer
//
// A writable Archive holds an exclusive lock on the file for its life, and the
// package refuses a second writable handle on the same path within the
// process. In-place transactions on a folder a sync client rewrites are not
// supported; there, Compact into a fresh file is the safe pattern.
//
// # Secrets
//
// The archive key is used to derive the index key and the wrap key and then
// dropped; Close zeroes both derived keys. Each Reader holds the AEAD of its
// file's DEK for its life, inside crypto/cipher where it cannot be wiped, as
// internal/stream documents. The decoded index — names above all — lives in
// memory while the Archive is open; DESIGN.md §10 forbids handing the whole
// list to the WebView, so Files is documented accordingly.
package archive

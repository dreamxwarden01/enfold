// Package stream implements the STREAM chunked AEAD of docs/FORMAT.md §12: a
// file's content as a sequence of AES-256-GCM chunks under one per-file DEK,
// each chunk sealed with a nonce made of an 11-byte little-endian counter and a
// final-chunk flag, and with archive_id ‖ file_id ‖ alg_id ‖ chunk_size as
// associated data.
//
// Framing is canonical (R26). Chunk i occupies bytes [i·65552, (i+1)·65552) of
// the blob; every chunk but the last carries exactly 65536 plaintext bytes; the
// last carries between 1 and 65536, or 0 only when it is the only chunk. A
// plaintext of n bytes therefore has exactly one encoding of length
// format.RawStoredSize(n), and PlaintextLen inverts it.
//
// The reader is told the blob's length and works out from it which chunk is
// final. It never tries a chunk under both flags, and never has to see EOF to
// know it has read everything: the length comes from the index, which the
// archive layer has already authenticated, and the final flag inside the
// authenticated nonce is the second, independent check. A blob that ends
// before its stated length is format.ErrTruncated; a chunk that does not open
// under its expected counter and flag — tampered, reordered, cut at a chunk
// boundary, wrong key, wrong file — is ErrAuth. Both fail closed, but a
// sequential caller may already have consumed authentic plaintext from earlier
// chunks. Anything that materialises a file from a Reader must treat an error
// as "discard what you built", not "keep what you got" (DESIGN.md §11, trap
// 17).
//
// A Writer buffers one chunk of plaintext and seals it once it knows whether
// more follows; the final chunk is written by Close, and a Writer that is not
// closed has not written a complete blob. Every Writer must be given a DEK that
// has never sealed anything: the archive layer mints a fresh one for every
// write of a file (§12), which is what makes (DEK, counter) reuse impossible.
//
// Buffers that held plaintext are zeroed when a Writer or Reader is closed.
// The expanded AES key inside crypto/cipher cannot be reached, as in
// internal/kdf.
package stream

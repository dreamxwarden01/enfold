// Package compress is the zstd layer of the archive: the compression policy
// of docs/DESIGN.md §9 and the decoder discipline of §11 trap 16, on top of
// github.com/klauspost/compress/zstd, the one CGO-free zstd in Go.
//
// What a file's content goes through is fixed by the index record's storage
// field (docs/FORMAT.md §11): raw, one zstd frame, or one zstd frame written
// against the archive's trained dictionary. This package produces and
// consumes that one frame; the AEAD around it is internal/stream, and the
// decision between raw and zstd is Probe, which compresses a few samples at
// the fastest level and reports whether the file is worth compressing at all.
//
// A Reader is told what the index says — the plaintext length, and whether
// the frame must use the dictionary — before it looks at a byte of the frame,
// and it holds the frame to it: the frame header's dictionary ID and, when
// present, its declared content size must match; its window must not exceed
// what its length can need — the smallest power of two above the record's
// size, at least MinWindow and never above MaxWindow, since content cannot
// reference further back than it is long; the frame must be the only one,
// followed by nothing but skippable padding; and the decoded length must
// come out exactly. Every limit the decoder runs under comes from the index
// or from this package's constants, never from the frame (trap 16). An error
// from a Reader means the file is unreadable, and whatever was decoded before
// it is to be discarded, as for internal/stream (trap 17).
//
// Writer and Reader are reusable across streams through Reset, because a zstd
// encoder or decoder allocates its window and tables once and an archive with
// many small files should not pay that per file.
//
// Dictionaries are the library's own format, built by BuildDict from samples
// the caller chooses. The selection of dictionary content is a plain
// round-robin over the heads of every other sample, the rest left to train
// the entropy tables — not COVER training. It is good enough for the
// many-similar-small-files case the dictionary exists for, and a better
// selector can replace it without a format change, because the dictionary
// is just bytes in the index.
package compress

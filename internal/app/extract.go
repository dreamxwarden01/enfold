package app

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/dreamxwarden01/enfold/internal/archive"
)

// extractFile writes one record's plaintext to path with the discipline
// APP.md §3 asks for — all-or-nothing, `os.ErrExist` rather than an
// overwrite — while counting the bytes as they land, so that the bar moves
// inside one large file (§3: progress is by bytes, not by file).
//
// The archive layer's own ExtractTo does the same placement and is what this
// would otherwise call; it takes no progress hook, and the counting has to
// sit on the writer, so the temporary and the exclusive move are done here
// instead. Everything about the content — the decryption, the chunk seals
// and the whole-file content hash — is still archive.Extract's, and a
// mismatch there leaves nothing at path.
func extractFile(ctx context.Context, a *archive.Archive, id [16]byte, path string, on func(written uint64)) error {
	// A cheap first answer for the common case; the exclusive move below is
	// what actually decides, so nothing here is a check the placement then
	// trusts (DECISIONS: ExtractTo's existence check was a TOCTOU until the
	// final move became exclusive).
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("%w: %s", os.ErrExist, path)
	}
	tmp, err := extractTempName(path)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			f.Close()
			os.Remove(tmp)
		}
	}()
	if err := a.Extract(ctx, id, &countingWriter{w: f, on: on}); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := placeExclusive(tmp, path); err != nil {
		return err
	}
	ok = true
	return nil
}

// countingWriter reports the plaintext written so far. The callback is the
// operation's progress, which throttles to ten events a second of its own.
type countingWriter struct {
	w io.Writer
	n uint64
	// on is called after every chunk; nil counts nothing.
	on func(written uint64)
}

func (w *countingWriter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	if n > 0 {
		w.n += uint64(n)
		if w.on != nil {
			w.on(w.n)
		}
	}
	return n, err
}

// extractTempName is a hidden name beside the target, on the same volume so
// that the move into place is a rename. Its length is its own — 29 units,
// whatever the target's — because a name of the 255 units R20 allows is a
// name a volume can hold, and one carrying the target's would then be too
// long to create (the outside audit of 2026-09-09). It is random rather than
// derived, so two extracts of one name into one folder do not collide; the
// O_EXCL below is what proves it.
func extractTempName(target string) (string, error) {
	var r [8]byte
	if _, err := rand.Read(r[:]); err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(target), fmt.Sprintf(".enfold-%x.part", r)), nil
}

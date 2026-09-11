package app

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/dreamxwarden01/enfold/internal/archive"
)

// The two marks an extract puts on a volume's refusal, so that the outcome
// can say which step it refused (APP.md §3, ruled 2026-09-10): the final
// name — the outcome is name_refused and a shorter name may help — or the
// temporary, a fixed short name in the same folder, in which case the path
// and not the leaf is what the volume refuses and no name helps. classify
// maps each to its code; the volume's own error stays in the chain for the
// log.
var (
	errNameRefused = errors.New("app: the destination refused the name")
	errPathRefused = errors.New("app: the destination refused the path")
)

// extractFS is where an extract touches the destination's file system: the
// placement of a temporary onto its final name and the creation of a
// directory. The zero value is the platform's own; a test sets a function
// to stand in for a volume the test machine does not have — one that
// refuses a name — the way reclaimRule stands in for the constants. A field
// left nil is the real thing.
type extractFS struct {
	place func(tmp, path string, replace bool) error
	mkdir func(path string) error
}

func (fs extractFS) placeFile(tmp, path string, replace bool) error {
	if fs.place != nil {
		return fs.place(tmp, path, replace)
	}
	if replace {
		return placeReplace(tmp, path)
	}
	return placeExclusive(tmp, path)
}

func (fs extractFS) makeDir(path string) error {
	if fs.mkdir != nil {
		return fs.mkdir(path)
	}
	return os.Mkdir(path, 0o700)
}

// extractFile writes one record's plaintext to path with the discipline
// APP.md §3 asks for — all-or-nothing, and `os.ErrExist` rather than an
// overwrite unless replace was chosen — while counting the bytes as they
// land, so that the bar moves inside one large file (§3: progress is by
// bytes, not by file).
//
// replace is the policy of §3 as amended on 2026-09-10: the content goes
// through the same temporary and is placed over the file already there in
// one move, never by unlinking it first, so a failure anywhere before the
// move leaves the old file exactly as it was.
//
// A collision may be seen twice over: by the cheap pre-check below, which
// spares the decryption of a file that will not be placed, or by the
// exclusive move's refusal when the file appeared in between. Either is a
// collision; what neither is, is a licence to write — nothing is ever placed
// over a file on a stat's word, only by the exclusive create or by the
// replace the user chose (the outside review of 2026-09-10, finding 11,
// accepted as documented).
//
// The archive layer's own ExtractTo does the same placement and is what this
// would otherwise call; it takes no progress hook, and the counting has to
// sit on the writer, so the temporary and the move are done here instead.
// Everything about the content — the decryption, the chunk seals and the
// whole-file content hash — is still archive.Extract's, and a mismatch there
// leaves nothing at path.
//
// A refusal of the volume's (refusedByVolume) is marked with the step it
// refused: the temporary's creation is errPathRefused — its name is short
// and fixed, so the folder's path is what was refused — and the placement
// onto the final name is errNameRefused. Every other error goes up as it is.
func extractFile(ctx context.Context, fs extractFS, a *archive.Archive, id [16]byte, path string, replace bool, on func(written uint64)) error {
	// The cheap pre-check: it may see the collision first, and it spares
	// the decryption; the exclusive move below is what places a file, so
	// nothing here is a check the placement then trusts (DECISIONS:
	// ExtractTo's existence check was a TOCTOU until the final move became
	// exclusive). Under replace nothing is in the way by definition, so the
	// file is not looked at before it is replaced.
	if !replace {
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("%w: %s", os.ErrExist, path)
		}
	}
	tmp, err := extractTempName(path)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if refusedByVolume(err) {
			return fmt.Errorf("%w: %w", errPathRefused, err)
		}
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
	if err := fs.placeFile(tmp, path, replace); err != nil {
		if refusedByVolume(err) {
			return fmt.Errorf("%w: %w", errNameRefused, err)
		}
		return err
	}
	ok = true
	return nil
}

// existingFile is what the file in the way says about itself: the size and
// the modified time an `ask` extract hands back with its conflict outcome
// (APP.md §3). It is a stat taken after the collision was seen — by the
// pre-check or by the exclusive create's refusal — and never one that
// decides a write: nothing is placed over a file on its word. It answers
// nil when the file has gone in between, which leaves the conflict standing
// with nothing to show for it.
func existingFile(path string) *ExistingFile {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil
	}
	size := fi.Size()
	if size < 0 {
		size = 0
	}
	return &ExistingFile{Size: uint64(size), ModifiedAt: fi.ModTime().Unix()}
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

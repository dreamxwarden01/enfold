//go:build !windows

package app

import (
	"errors"
	"fmt"
	"os"
)

// placeExclusive moves tmp onto path without replacing anything there: a
// link, which fails when the name is taken, and then the temporary's own
// removal (APP.md §3). Enfold ships on Windows (SCOPE.md); this keeps the
// package buildable and testable elsewhere.
func placeExclusive(tmp, path string) error {
	if err := os.Link(tmp, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%w: %s", os.ErrExist, path)
		}
		return err
	}
	// The link landed, so the placement has succeeded: the file is at path
	// and the caller must be told so. A temporary that will not unlink is
	// litter, never a failed extraction — reporting it would leave the item
	// `failed` with a good file on disk (APP.md §3: each file is
	// all-or-nothing).
	_ = os.Remove(tmp)
	return nil
}

// placeReplace moves tmp onto path over whatever is there, in one operation
// — os.Rename, which replaces atomically here — so that the old file is
// never unlinked first (APP.md §3's replace). Enfold ships on Windows
// (SCOPE.md); this keeps the package buildable and testable elsewhere.
func placeReplace(tmp, path string) error {
	return os.Rename(tmp, path)
}

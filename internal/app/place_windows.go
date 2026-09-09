//go:build windows

package app

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// placeExclusive moves tmp onto path and refuses to replace anything there:
// MoveFileEx with no MOVEFILE_REPLACE_EXISTING, so a file that appears
// between the extraction and this is not overwritten (APP.md §3, DESIGN.md
// trap 28 — Enfold never writes over a file it did not make). os.Rename
// cannot be used: Go's own rename passes MOVEFILE_REPLACE_EXISTING.
func placeExclusive(tmp, path string) error {
	from, err := windows.UTF16PtrFromString(tmp)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	if err := windows.MoveFileEx(from, to, 0); err != nil {
		if errors.Is(err, windows.ERROR_ALREADY_EXISTS) || errors.Is(err, windows.ERROR_FILE_EXISTS) {
			return fmt.Errorf("%w: %s", os.ErrExist, path)
		}
		return &os.LinkError{Op: "rename", Old: tmp, New: path, Err: err}
	}
	return nil
}

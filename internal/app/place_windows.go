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

// placeReplace moves tmp onto path over whatever is there: MoveFileEx with
// MOVEFILE_REPLACE_EXISTING, which is one operation — the old file is never
// unlinked first, so a failure leaves it where it was and the destination
// never stands empty (APP.md §3's replace, ruled 2026-09-10). Only a file the
// user asked to replace ever reaches here; every other policy places
// exclusively.
func placeReplace(tmp, path string) error {
	from, err := windows.UTF16PtrFromString(tmp)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	if err := windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING); err != nil {
		return &os.LinkError{Op: "rename", Old: tmp, New: path, Err: err}
	}
	return nil
}

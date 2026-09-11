//go:build windows

package app

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

// refusedByVolume reports a name or a path the destination would not take
// (APP.md §3, ruled 2026-09-10): ERROR_FILENAME_EXCED_RANGE (206, the path
// as a whole is over what the volume or the API takes), ERROR_INVALID_NAME
// (123, what NTFS answers for a component it cannot hold) and ENAMETOOLONG,
// which Go's syscall package defines on Windows too. The three are the
// destination's to say and are never known beforehand: R20 checked the name
// on the way in against Windows' own rules, and what is left is the path's
// length and the volume's own limits.
func refusedByVolume(err error) bool {
	return errors.Is(err, windows.ERROR_FILENAME_EXCED_RANGE) ||
		errors.Is(err, windows.ERROR_INVALID_NAME) ||
		errors.Is(err, syscall.ENAMETOOLONG)
}

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

//go:build windows

package app

import (
	"fmt"

	"github.com/dreamxwarden01/enfold/internal/format"
	"golang.org/x/sys/windows"
)

// deleteArchiveFileIfMatches opens the file at path once, reads its envelope
// from that handle, and — only when the envelope's archive_id is want —
// unlinks it through the same handle (APP.md §13, DESIGN.md trap 28). The
// name is never used a second time, so nothing can be swapped in between the
// check and the removal.
//
// The handle asks for DELETE beside GENERIC_READ, which os.Open does not
// (GOROOT/src/syscall/syscall_windows.go: READ|WRITE only), so os.Remove
// afterwards would hit a sharing violation against our own handle; and it
// permits every share mode, so a reader elsewhere that itself permits
// deletion does not turn this into "busy". FileDispositionInfo is a class
// constant taking one byte, not a struct.
func deleteArchiveFileIfMatches(path string, want [16]byte) (bool, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false, fmt.Errorf("%w: %v", errArchiveUnreachable, err)
	}
	h, err := windows.CreateFile(p,
		windows.DELETE|windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return false, openFailure(path, err)
	}
	defer windows.CloseHandle(h)
	buf := make([]byte, format.SuperblockSize)
	for off := 0; off < len(buf); {
		var n uint32
		if err := windows.ReadFile(h, buf[off:], &n, nil); err != nil {
			return false, fmt.Errorf("%w: reading %s: %v", errArchiveNotOne, path, err)
		}
		if n == 0 {
			return false, fmt.Errorf("%w: %s is %d bytes, shorter than an envelope", errArchiveNotOne, path, off)
		}
		off += int(n)
	}
	env, err := format.DecodeEnvelope(buf)
	if err != nil {
		return false, fmt.Errorf("%w: %s: %v", errArchiveNotOne, path, err)
	}
	if env.ArchiveID != want {
		return false, fmt.Errorf("%w: %s holds archive %x", errArchiveNotThisOne, path, env.ArchiveID)
	}
	var b byte = 1
	if err := windows.SetFileInformationByHandle(h, windows.FileDispositionInfo, &b, 1); err != nil {
		return false, fmt.Errorf("%w: %s: %v", errArchiveRemoveFailed, path, err)
	}
	return true, nil
}

// openFailure maps what the one open can fail with: a folder or volume that
// is not there is unreachable, a leaf that went between the folder listing
// and the open is the proven mismatch, and everything else — held, denied —
// is busy, with nothing removed and nothing forgotten.
func openFailure(path string, err error) error {
	switch err {
	case windows.ERROR_PATH_NOT_FOUND, windows.ERROR_BAD_NETPATH, windows.ERROR_NOT_READY, windows.ERROR_INVALID_NAME:
		return fmt.Errorf("%w: %s: %v", errArchiveUnreachable, path, err)
	case windows.ERROR_FILE_NOT_FOUND:
		return fmt.Errorf("%w: %s is gone: %v", errArchiveNotThisOne, path, err)
	}
	return fmt.Errorf("%w: %s: %v", errArchiveHeld, path, err)
}

//go:build windows

package archive

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// fileLock is an exclusive lock on an archive open for writing: one byte
// far past any offset the file will reach, locked through LockFileEx — a
// Windows byte-range lock is mandatory and would block reads of the range
// by other handles, so it must not cover real data — so that another
// process fails to lock it. A second handle in this process never reaches
// the lock: the registry of open paths (registry.go) refuses it first, and
// that registry holds read-only handles too, which take no OS lock at all.
type fileLock struct {
	f *os.File
}

// canonical is the key a path is held under, here and in the registry: an
// absolute, cleaned path, folded to lower case because Windows file names
// are compared that way.
func canonical(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return strings.ToLower(filepath.Clean(abs))
}

func lockFile(f *os.File) (*fileLock, error) {
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, lockRange())
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBusy, err)
	}
	return &fileLock{f: f}, nil
}

// release drops the OS lock; the file must still be open. The claim on the
// path is the Archive's own and outlives this by as long as it must
// (Compact holds it across the rename).
func (l *fileLock) release() error {
	return windows.UnlockFileEx(windows.Handle(l.f.Fd()), 0, 1, 0, lockRange())
}

// lockRange is the locked byte: offset 2^62, beyond any file the format
// allows (extents are bounded far below it).
func lockRange() *windows.Overlapped {
	return &windows.Overlapped{Offset: 0, OffsetHigh: 1 << 30}
}

// placeExclusive moves tmp to path, failing with os.ErrExist if path exists
// at that moment rather than replacing it.
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

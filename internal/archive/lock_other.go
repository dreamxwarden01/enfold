//go:build !windows

package archive

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// fileLock is an exclusive lock on an archive open for writing: flock on
// the descriptor, so that another process fails to take it. A second handle
// in this process never reaches the lock: the registry of open paths
// (registry.go) refuses it first, and that registry holds read-only handles
// too, which take no OS lock at all.
type fileLock struct {
	f *os.File
}

// canonical is the key a path is held under, here and in the registry: an
// absolute, cleaned path, case kept.
func canonical(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return filepath.Clean(abs)
}

func lockFile(f *os.File) (*fileLock, error) {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBusy, err)
	}
	return &fileLock{f: f}, nil
}

// release drops the OS lock; the file must still be open. The claim on the
// path is the Archive's own and outlives this by as long as it must
// (Compact holds it across the rename).
func (l *fileLock) release() error {
	return syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
}

// placeExclusive moves tmp to path, failing with os.ErrExist if path exists
// at that moment rather than replacing it: link, then unlink the temporary.
func placeExclusive(tmp, path string) error {
	if err := os.Link(tmp, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%w: %s", os.ErrExist, path)
		}
		return err
	}
	return os.Remove(tmp)
}

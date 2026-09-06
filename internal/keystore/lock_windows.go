//go:build windows

package keystore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/windows"
)

// fileLock is an exclusive lock on an open keystore file: one byte
// far past any offset the file will reach, locked through LockFileEx — a
// Windows byte-range lock is mandatory and would block reads of the range
// by other handles, so it must not cover real data — so that another
// process fails to lock it, plus a process-wide table of paths, so that a
// second handle in this process fails before touching the file.
type fileLock struct {
	f    *os.File
	path string
}

var (
	openMu    sync.Mutex
	openPaths = map[string]struct{}{}
)

func canonical(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return strings.ToLower(filepath.Clean(abs))
}

func lockFile(f *os.File, path string) (*fileLock, error) {
	key := canonical(path)
	openMu.Lock()
	if _, busy := openPaths[key]; busy {
		openMu.Unlock()
		return nil, ErrBusy
	}
	openPaths[key] = struct{}{}
	openMu.Unlock()
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, lockRange())
	if err != nil {
		openMu.Lock()
		delete(openPaths, key)
		openMu.Unlock()
		// Only contention means "open elsewhere"; a file system that cannot
		// lock at all is an I/O failure, not another Enfold.
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
			return nil, fmt.Errorf("%w: %v", ErrBusy, err)
		}
		return nil, fmt.Errorf("keystore: locking %s: %w", path, err)
	}
	return &fileLock{f: f, path: key}, nil
}

// releaseOS drops the OS lock; the file must still be open.
func (l *fileLock) releaseOS() error {
	return windows.UnlockFileEx(windows.Handle(l.f.Fd()), 0, 1, 0, lockRange())
}

// releasePath drops the in-process claim on the path.
func (l *fileLock) releasePath() {
	openMu.Lock()
	delete(openPaths, l.path)
	openMu.Unlock()
}

func (l *fileLock) release() error {
	err := l.releaseOS()
	l.releasePath()
	return err
}

// lockRange is the locked byte: offset 2^62, beyond any file the format
// allows (extents are bounded far below it).
func lockRange() *windows.Overlapped {
	return &windows.Overlapped{Offset: 0, OffsetHigh: 1 << 30}
}

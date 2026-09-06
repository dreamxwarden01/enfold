//go:build !windows

package keystore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// fileLock is an exclusive lock on an open keystore file: flock on
// the descriptor, plus a process-wide table of paths so that a second handle
// in this process fails before touching the file.
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
	return filepath.Clean(abs)
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
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		openMu.Lock()
		delete(openPaths, key)
		openMu.Unlock()
		// Only contention means "open elsewhere"; a file system that cannot
		// lock at all is an I/O failure, not another Enfold.
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, fmt.Errorf("%w: %v", ErrBusy, err)
		}
		return nil, fmt.Errorf("keystore: locking %s: %w", path, err)
	}
	return &fileLock{f: f, path: key}, nil
}

// releaseOS drops the OS lock; the file must still be open.
func (l *fileLock) releaseOS() error {
	return syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
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

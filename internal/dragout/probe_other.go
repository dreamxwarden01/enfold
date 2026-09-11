//go:build !windows

package dragout

import "os"

// stagedFileInUse has no exclusive open to ask on other platforms; the
// staged route itself runs on Windows alone, and this only keeps the
// portable half — the state machine and the scavenge — compiling and
// testable everywhere.
func stagedFileInUse(path string) (inUse bool, exists bool, err error) {
	if _, err := os.Lstat(path); err != nil {
		if os.IsNotExist(err) {
			return false, false, nil
		}
		return false, true, err
	}
	return false, true, nil
}

// isReparsePoint is a symlink here: the one kind of link the rest of the
// world has.
func isReparsePoint(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.Mode()&os.ModeSymlink != 0
}

//go:build !windows

package app

import "fmt"

// deleteArchiveFileIfMatches has no counterpart outside Windows: removing a
// file through the handle its envelope was read from is what DESIGN.md trap
// 28 asks for, and the app ships on Windows. Nothing is read and nothing is
// removed here, so a Delete answers archive.delete_failed with the record and
// the file both untouched.
func deleteArchiveFileIfMatches(path string, _ [16]byte) (bool, error) {
	return false, fmt.Errorf("%w: %s: unlinking through the file's own handle needs Windows", errArchiveRemoveFailed, path)
}

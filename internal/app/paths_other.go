//go:build !windows

package app

import "os"

// The three questions paths_windows.go answers with Win32 calls, answered
// here with what the rest of the world has. The application runs on Windows;
// this keeps the core building and its tests running everywhere.

// longPath has no 8.3 short names to expand.
func longPath(p string) string { return p }

// isReparsePoint is a symbolic link here: the one kind of link the rest of
// the world has, and Lstat reports it in the mode.
func isReparsePoint(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.Mode()&os.ModeSymlink != 0
}

// finalPath has no handle-to-path call to make, so the caller falls back on
// the name it opened and on os.SameFile, which needs no path at all.
func finalPath(f *os.File) (string, bool) { return "", false }

// canonicalPath has no reparse points to resolve that filepath.EvalSymlinks
// does not already resolve, so the caller falls back on that.
func canonicalPath(p string) (string, bool) { return "", false }

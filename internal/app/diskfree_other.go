//go:build !windows

package app

import "errors"

// platformDiskFree is not measured off Windows, the one platform Enfold
// ships for: Compact's check is then skipped rather than guessed (ops.go
// compactionRoomFor).
func platformDiskFree(string) (string, uint64, error) {
	return "", 0, errors.New("app: free disk space is measured on Windows only")
}

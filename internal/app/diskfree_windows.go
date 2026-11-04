//go:build windows

package app

import (
	"strings"

	"golang.org/x/sys/windows"
)

// platformDiskFree answers the free space on the volume path is on, as the
// caller may use it — GetDiskFreeSpaceEx's figure for the caller, which a
// quota lowers — and the volume as Windows names it, "D:" for a drive, or
// the folder a volume is mounted on. The volume is found as isLocalFixed
// finds it (localfixed_windows.go): GetVolumePathName walks up from the
// path until a mount point answers. It is Compact's check (ops.go
// compactionRoomFor, ruled 2026-11-04).
func platformDiskFree(path string) (string, uint64, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", 0, err
	}
	root := make([]uint16, windows.MAX_PATH+1)
	if err := windows.GetVolumePathName(p, &root[0], uint32(len(root))); err != nil {
		return "", 0, err
	}
	var avail, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(&root[0], &avail, &total, &free); err != nil {
		return "", 0, err
	}
	return strings.TrimSuffix(windows.UTF16ToString(root), `\`), avail, nil
}

//go:build windows

package app

import "golang.org/x/sys/windows"

// isLocalFixed reports whether path sits on a local fixed volume of this
// machine (APP.md §13): the volume the path belongs to is found first —
// GetVolumePathName walks up until a mount point answers, so a folder
// mounted from a network share is judged by its mount and not by its
// drive letter — and only DRIVE_FIXED is probed. A path that does not
// resolve to a volume is not measured.
func isLocalFixed(path string) bool {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	root := make([]uint16, windows.MAX_PATH+1)
	if err := windows.GetVolumePathName(p, &root[0], uint32(len(root))); err != nil {
		return false
	}
	return windows.GetDriveType(&root[0]) == windows.DRIVE_FIXED
}

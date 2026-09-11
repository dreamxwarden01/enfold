//go:build windows

package dragout

import (
	"golang.org/x/sys/windows"
)

// stagedFileInUse opens the file with dwShareMode 0, documented as
// "Prevents subsequent open operations on a file or device if they request
// delete, read, or write access" — and the other direction is the one this
// uses: "You cannot request a sharing mode that conflicts with the access
// mode that is specified in an existing request that has an open handle.
// CreateFile would fail and the GetLastError function would return
// ERROR_SHARING_VIOLATION." So the request fails exactly while somebody else
// has the file open, whoever they are ("the sharing options for each open
// handle remain in effect until that handle is closed, regardless of process
// context"). That failure is the measurement.
func stagedFileInUse(path string) (inUse bool, exists bool, err error) {
	p, perr := windows.UTF16PtrFromString(path)
	if perr != nil {
		return false, false, perr
	}
	h, cerr := windows.CreateFile(p, windows.GENERIC_READ, 0, nil,
		windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if cerr == nil {
		windows.CloseHandle(h)
		return false, true, nil
	}
	switch cerr {
	case windows.ERROR_SHARING_VIOLATION:
		return true, true, nil
	case windows.ERROR_FILE_NOT_FOUND, windows.ERROR_PATH_NOT_FOUND:
		return false, false, nil
	}
	// ERROR_ACCESS_DENIED and the rest: not a sharing answer, so it says
	// nothing about the consumer. Report it and let the caller log it.
	return false, true, cerr
}

// isReparsePoint asks the attributes rather than the mode bits: a junction
// is a reparse point that os.Lstat does not report as a symlink.
func isReparsePoint(path string) bool {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	attrs, err := windows.GetFileAttributes(p)
	if err != nil {
		return false
	}
	return attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

//go:build windows

package dragout

import (
	"errors"

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

// stillActive is STILL_ACTIVE, which winbase.h defines as STATUS_PENDING
// (0x103) and GetExitCodeProcess documents as the value it returns "if the
// process has not terminated". x/sys/windows has no constant for it.
const stillActive = 259

// probeProcess asks after the process pid: whether it is there, and when it
// started (the manifest's owner, stage.go). PROCESS_QUERY_LIMITED_INFORMATION
// is the least that will do, and is documented as the access "a process that
// has [it] can use ... GetExitCodeProcess and GetPriorityClass" and, on the
// GetProcessTimes page, as an access that function accepts.
//
// How OpenProcess's failures are read:
//
//   - ERROR_INVALID_PARAMETER is the PID naming nothing. The page lists no
//     error codes at all, but this is the documented failure of the call
//     for a process id that is not a process's — it is what the System Idle
//     Process and a freed id both give — and it is the one refusal that
//     says the process is gone rather than out of reach.
//   - ERROR_ACCESS_DENIED is the opposite: the id resolved to a process and
//     the refusal was about this process's rights over it, not about its
//     existence. Something is there, so the answer is alive — with no start
//     time, which is what stops manifestOwner from mistaking it for the
//     particular process a manifest names.
//   - Anything else says nothing either way, and is unknown.
//
// A handle is not yet a running process: a process object outlives its
// process while any handle to it is open, so GetExitCodeProcess is asked as
// well, and only STILL_ACTIVE is taken as running. A process that exits
// with 259 of its own reads as alive here, which leaves its folder where it
// stands: the cautious way round.
//
// The start time is the raw creation FILETIME — 100-ns ticks since 1601 —
// kept as the number the platform gave rather than a time.Time, since it is
// only ever compared with itself. Zero when it could not be had, and a zero
// is never a match: the caller reads an alive with no time as unknown.
func probeProcess(pid int) (started int64, state ownerState) {
	if pid <= 0 {
		return 0, ownerUnknown
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		switch {
		case errors.Is(err, windows.ERROR_INVALID_PARAMETER):
			return 0, ownerGone
		case errors.Is(err, windows.ERROR_ACCESS_DENIED):
			return 0, ownerAlive
		}
		return 0, ownerUnknown
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err == nil && code != stillActive {
		return 0, ownerGone
	}
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return 0, ownerAlive
	}
	return int64(creation.HighDateTime)<<32 | int64(creation.LowDateTime), ownerAlive
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

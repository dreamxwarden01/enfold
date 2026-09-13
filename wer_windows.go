//go:build windows

package main

import (
	"os"
	"path/filepath"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// maxPath is MAX_PATH, which winbase.h defines as 260 and which is counted
// in characters — UTF-16 units here, not bytes.
const maxPath = 260

// werExclusionName is what WerAddExcludedApplication is given for the
// executable at exe, and whether that is the full path.
//
// The parameter is documented as "a pointer to a string that specifies the
// name of the executable file, including the file name extension ... limited
// to MAX_PATH characters", and the call fails rather than truncating: an
// installation deep enough to pass that would have the exclusion silently
// absent, which is the one outcome worth avoiding. The API takes a bare
// name as well — the documented parameter is a name, and a path is only the
// more specific spelling of one — so a path too long to pass is replaced by
// the executable's base name. That excludes any executable of that name
// rather than this one alone, which is broader than intended and still far
// better than no exclusion at all.
//
// The limit is read as "at most MAX_PATH characters", so a string of
// MAX_PATH - 1 or more is not offered: the terminating NUL is a character
// the buffer has to hold too, and nothing here is worth spending the last
// unit of doubt on.
func werExclusionName(exe string) (name string, full bool) {
	if len(utf16.Encode([]rune(exe))) < maxPath-1 {
		return exe, true
	}
	return filepath.Base(exe), false
}

// wer.dll from System32, never from the application's folder: a lazy DLL by
// plain name would search beside the executable first.
var procWerAddExcludedApplication = windows.NewLazySystemDLL("wer.dll").NewProc("WerAddExcludedApplication")

// excludeFromWER takes this executable off Windows Error Reporting's list for
// the current user — SCOPE.md's "automatic Windows Error Reporting is off for
// the process", beside the locked pages that keep a secret out of the pagefile.
//
// Most of the work is already done by the SetErrorMode call above it:
// SEM_NOGPFAULTERRORBOX means "the system does not invoke Windows Error
// Reporting" for this process's faults, so on a Windows 11 with default
// settings a crash raises no report and writes no dump. WER's local dump
// collection is the one thing that would write memory to disk, and it is
// opt-in: the HKLM …\Windows Error Reporting\LocalDumps key, off by default
// and administrator-only to turn on, configured independently of the rest of
// WER — which is also why neither the error mode nor this call can switch it
// off. A LocalDumps key someone set is the "dumps someone configured
// elsewhere" the document leaves outside the promise.
//
// So this call adds only what the error mode does not cover: a report raised
// for something other than this process's own fault, and a descendant or a
// loaded component that resets the mode. bAllUsers is FALSE, so the list is
// written under HKCU and no elevation is asked for; the failure is logged and
// nothing more, since the program runs with or without it.
//
// learn.microsoft.com/en-us/windows/win32/api/errhandlingapi/nf-errhandlingapi-seterrormode
// learn.microsoft.com/en-us/windows/win32/api/werapi/nf-werapi-weraddexcludedapplication
// learn.microsoft.com/en-us/windows/win32/wer/collecting-user-mode-dumps
func excludeFromWER(logf func(string, ...any)) {
	exe, err := os.Executable()
	if err != nil {
		logf("wer: no executable path, not excluded from error reporting: %v", err)
		return
	}
	arg, full := werExclusionName(exe)
	name, err := windows.UTF16PtrFromString(arg)
	if err != nil {
		logf("wer: %s is not a usable path, not excluded from error reporting: %v", arg, err)
		return
	}
	// The HRESULT is the first return; E_ACCESSDENIED is what a registry the
	// user cannot write gives back.
	hr, _, _ := procWerAddExcludedApplication.Call(uintptr(unsafe.Pointer(name)), 0)
	if hr != 0 {
		logf("wer: WerAddExcludedApplication(%s): 0x%08x", arg, uint32(hr))
		return
	}
	if !full {
		logf("wer: excluded by name (%s) rather than by path: %s is at or past MAX_PATH, which the call will not take", arg, exe)
	}
}

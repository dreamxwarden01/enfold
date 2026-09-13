//go:build windows

package app

import (
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

// What Windows lets a path be called, and what it lets a handle say about
// itself. Everything here answers the same question from a different side:
// is this the vault, whatever it has been spelled as (ops.go, the vault is
// never a source and never a destination).

const (
	// VOLUME_NAME_DOS and FILE_NAME_NORMALIZED, both defined as 0x0 in
	// fileapi.h. GetFinalPathNameByHandleW documents the pair as its
	// default: "Return the path with the drive letter" and "Return the
	// normalized drive name", which is the spelling the rest of this
	// package compares against. x/sys/windows has neither constant.
	volumeNameDOS      = 0x0
	fileNameNormalized = 0x0
)

// longPath is p with its 8.3 short names expanded, and p itself when
// Windows will not say — an unreachable path, a volume with short names
// turned off, or a path with none in it, which GetLongPathNameW returns
// unchanged anyway.
//
// This matters because a short name is a second name for the same file:
// %LOCALAPPDATA%\Enfold may also be reached as ...\ENFOLD~1, and a
// comparison that only folds case sees two different folders. The
// documented shape is the usual two-call one — "If the function succeeds,
// the return value is the length, in TCHARs, of the string ... If the
// lpszLongPath buffer is too small ... the return value is the required
// buffer size, in TCHARs" — so a short buffer is asked once and the answer
// sized from the refusal.
func longPath(p string) string {
	u, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return p
	}
	buf := make([]uint16, len(p)+16)
	for {
		n, err := windows.GetLongPathName(u, &buf[0], uint32(len(buf)))
		if err != nil || n == 0 {
			return p
		}
		if int(n) <= len(buf) {
			return windows.UTF16ToString(buf[:n])
		}
		buf = make([]uint16, n)
	}
}

// isReparsePoint reports a junction, a symbolic link or any other reparse
// point at path, and never follows it.
//
// The attribute is read out of the Win32FileAttributeData Lstat leaves
// behind rather than judged from the Go file mode: which mode bits a
// junction ends up with — ModeSymlink, ModeIrregular, or neither — has
// moved between Go releases, while FILE_ATTRIBUTE_REPARSE_POINT is the
// file system's own word and has not.
func isReparsePoint(path string) bool {
	fi, err := os.Lstat(path)
	if err != nil {
		return false
	}
	d, ok := fi.Sys().(*syscall.Win32FileAttributeData)
	return ok && d.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

// finalPath is the path an open handle actually reached: every link
// followed, every short name expanded, the volume named by its drive
// letter. GetFinalPathNameByHandleW is documented to give "the final path
// for the specified file", which is the point — the name a caller passed is
// a request, and this is what the file system made of it.
//
// It answers false when Windows will not say. The path it does give carries
// the \\?\ prefix, which the caller's own canonicalisation takes off.
func finalPath(f *os.File) (string, bool) {
	h := windows.Handle(f.Fd())
	if h == windows.InvalidHandle {
		return "", false
	}
	return finalPathOf(h)
}

// canonicalPath is the same question asked of a path rather than of a
// handle: it opens the path and hands back what the file system says it
// reached.
//
// This, and not filepath.EvalSymlinks, is what follows a junction. Whether
// Go reports a junction as ModeSymlink, as ModeIrregular or as a plain
// directory has moved between releases, and EvalSymlinks walks on the mode:
// on this machine it leaves a junction exactly as it found it, so a folder
// junctioned onto the data folder compared as an unrelated path. The file
// system has no such doubt — it resolves the reparse point on the open —
// and one call settles links, junctions, 8.3 names, the volume's spelling
// and the case together.
//
// The open asks for no access at all, which is documented to "query certain
// metadata ... even if GENERIC_READ access would have been denied", and
// shares everything, so it disturbs nothing and is refused by nothing that
// is merely busy — the vault, held open by the keystore, answers it.
// FILE_FLAG_BACKUP_SEMANTICS is what lets a directory be opened at all.
func canonicalPath(p string) (string, bool) {
	u, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return "", false
	}
	h, err := windows.CreateFile(u, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", false
	}
	defer windows.CloseHandle(h)
	return finalPathOf(h)
}

// finalPathOf is the two-call shape GetFinalPathNameByHandleW documents:
// the return "is the length of the string received ... not including the
// size of the terminating null character" on success, and "the size of the
// buffer required to hold the path, including the terminating null
// character" when the buffer was too small.
func finalPathOf(h windows.Handle) (string, bool) {
	buf := make([]uint16, 512)
	for {
		n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), volumeNameDOS|fileNameNormalized)
		if err != nil || n == 0 {
			return "", false
		}
		if int(n) < len(buf) {
			return windows.UTF16ToString(buf[:n]), true
		}
		buf = make([]uint16, n+1)
	}
}

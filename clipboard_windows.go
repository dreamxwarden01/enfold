//go:build windows

package main

import (
	"errors"
	"os"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/dreamxwarden01/enfold/internal/app"
)

// The clipboard half of *Paste* (APP.md §3 Shell.ClipboardFiles, §6; ruled
// 2026-10-26): the files a copy or a cut in Explorer left on the Windows
// clipboard. The WebView's own clipboard holds no files — Explorer puts a
// CF_HDROP there, which navigator.clipboard never sees — so the shell reads
// it. golang.org/x/sys/windows binds none of these, so they are bound here
// over lockwatch_windows.go's user32 and kernel32, from System32 only.
var (
	shell32 = windows.NewLazySystemDLL("shell32.dll")

	procOpenClipboard              = user32.NewProc("OpenClipboard")
	procCloseClipboard             = user32.NewProc("CloseClipboard")
	procIsClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	procGetClipboardData           = user32.NewProc("GetClipboardData")
	procRegisterClipboardFormatW   = user32.NewProc("RegisterClipboardFormatW")
	procGlobalLock                 = kernel32.NewProc("GlobalLock")
	procGlobalUnlock               = kernel32.NewProc("GlobalUnlock")
	procGlobalSize                 = kernel32.NewProc("GlobalSize")
	procRtlMoveMemory              = kernel32.NewProc("RtlMoveMemory")
	procDragQueryFileW             = shell32.NewProc("DragQueryFileW")
)

const (
	cfHDrop = 15
	// clipOpenTries and clipOpenWait: another process may hold the
	// clipboard for a moment (a clipboard manager reading what was just
	// copied), so the open is tried a few times before io is answered.
	clipOpenTries = 3
	clipOpenWait  = 50 * time.Millisecond
	// dragQueryCount is DragQueryFileW's iFile asking for the count.
	dragQueryCount = 0xFFFFFFFF
)

var errClipboardBusy = errors.New("clipboard: held by another process")

// clipboardFiles is Shell.ClipboardFiles: the clipboard's CF_HDROP and its
// "Preferred DropEffect", copied out while the clipboard is open and
// stat'ed for their kind after it is closed, so that a slow drive never
// keeps the clipboard from every other program on the desktop.
func (s *shell) clipboardFiles() (app.Clipboard, error) {
	paths, move, err := readClipboardDrop()
	if err != nil {
		if s.log != nil {
			s.log("clipboard: %v", err)
		}
		return app.Clipboard{Files: []app.ClipFile{}}, err
	}
	return app.ClipboardOf(paths, move, os.Stat), nil
}

// readClipboardDrop opens the clipboard, reads what it names and closes it.
// The open and the close are one thread's (OpenClipboard(0) binds the
// clipboard to the calling task), so the goroutine is pinned for the read.
func readClipboardDrop() (paths []string, move bool, err error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	opened := false
	for i := 0; i < clipOpenTries; i++ {
		if r, _, _ := procOpenClipboard.Call(0); r != 0 {
			opened = true
			break
		}
		if i < clipOpenTries-1 {
			time.Sleep(clipOpenWait)
		}
	}
	if !opened {
		return nil, false, errClipboardBusy
	}
	defer procCloseClipboard.Call()

	if r, _, _ := procIsClipboardFormatAvailable.Call(cfHDrop); r == 0 {
		return nil, false, nil // no files: text, an image, or nothing at all
	}
	h, _, _ := procGetClipboardData.Call(cfHDrop)
	if h == 0 {
		return nil, false, nil
	}
	block, ok := globalBytes(h)
	if !ok {
		return nil, false, nil
	}
	paths, err = app.ParseHDrop(block)
	if errors.Is(err, app.ErrHDropNarrow) {
		// An older program's ANSI names: DragQueryFileW converts them with
		// the system code page, which only the shell has.
		paths, err = dragQueryFiles(h), nil
	}
	if err != nil {
		return nil, false, err
	}
	move = clipboardMove()
	return paths, move, nil
}

// clipboardMove reads "Preferred DropEffect" on the open clipboard: a cut in
// Explorer writes DROPEFFECT_MOVE there.
func clipboardMove() bool {
	name, err := windows.UTF16PtrFromString("Preferred DropEffect")
	if err != nil {
		return false
	}
	f, _, _ := procRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(name)))
	if f == 0 {
		return false
	}
	if r, _, _ := procIsClipboardFormatAvailable.Call(f); r == 0 {
		return false
	}
	h, _, _ := procGetClipboardData.Call(f)
	if h == 0 {
		return false
	}
	b, ok := globalBytes(h)
	return ok && app.ParseDropEffect(b)
}

// globalBytes copies a clipboard HGLOBAL out under its lock, bounded by
// GlobalSize, so that the parse never reads memory the block does not own.
// The copy goes through RtlMoveMemory, as internal/dragout's does, rather
// than rebuilding a Go pointer from the bare address GlobalLock returns.
func globalBytes(h uintptr) ([]byte, bool) {
	n, _, _ := procGlobalSize.Call(h)
	if n == 0 {
		return nil, false
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return nil, false
	}
	defer procGlobalUnlock.Call(h)
	b := make([]byte, n)
	procRtlMoveMemory.Call(uintptr(unsafe.Pointer(&b[0])), p, n)
	return b, true
}

// dragQueryFiles names the paths of an HDROP through DragQueryFileW:
// counting them first, then sizing and reading each.
func dragQueryFiles(h uintptr) []string {
	count, _, _ := procDragQueryFileW.Call(h, dragQueryCount, 0, 0)
	paths := make([]string, 0, count)
	for i := uintptr(0); i < count; i++ {
		n, _, _ := procDragQueryFileW.Call(h, i, 0, 0)
		if n == 0 {
			continue
		}
		buf := make([]uint16, n+1)
		got, _, _ := procDragQueryFileW.Call(h, i, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if got == 0 {
			continue
		}
		paths = append(paths, windows.UTF16ToString(buf[:got]))
	}
	return paths
}

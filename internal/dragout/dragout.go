// Package dragout is the drag out of the window (APP.md §3, ruled
// 2026-09-10 and 2026-09-11): the staged route, 7-Zip's and WinRAR's, with
// what their authors and users learnt folded in. One native OLE drag per
// gesture, run on a thread of its own so that the window's queue stays
// alive through the whole of it (drag_windows.go), with a pure-Go,
// cgo-free data object — agile, so that what runs
// inside the drop's GetData runs on Explorer's worker thread and never the
// WebView's — offering CF_HDROP by delayed rendering over a staging folder
// the caller's Extract fills at the first request after the button's
// release, CFSTR_PREFERREDDROPEFFECT = move beside it, and a scavenge that
// takes what a drop left behind once it is an hour old.
//
// What it deliberately does NOT offer is IDataObjectAsyncCapability (ruled
// 2026-09-11, WinRAR's model): without it Windows obliges the target to
// finish the drop inside IDropTarget::Drop, so DoDragDrop returns when
// Explorer has copied — or the user has answered its conflict dialog, Skip
// included — with the real effect, and the drag is over when it returns.
// With it, Explorer copied in the background, never called EndOperation,
// and after a Skip neither read the file, nor moved it, nor let the object
// go: nothing could say the drop was done.
//
// The mechanism was proved by tools/dragproto (its README, "Findings so
// far") and is lifted from it; what the prototype had for measuring — the
// window, the message loop, the synthetic files, the virtual-file route, the
// flags and the per-thread tally — stays there. Nothing here knows an
// archive: the caller plans the items, writes them when asked, and hears the
// phases. Nothing here is logged with a file name in it (APP.md §3).
package dragout

import (
	"context"
	"errors"
	"time"
)

// Item is one thing the drag offers: a file or a folder, named by its path
// under the staging folder — the record's own name for a top-level
// selection, its path below a selected directory — with backslashes, so
// that a folder's subtree lands as a folder.
type Item struct {
	Name    string
	Size    uint64
	ModTime time.Time
	IsDir   bool
}

// Step is where a drag has got to, as the caller's strip shows it (APP.md
// §3): Preparing while the drop's own request runs the extraction, Awaiting
// from the extraction's end until DoDragDrop returns — the time the target
// spends finishing the drop inside Drop, which is the caller's bar standing
// full — and Done.
type Step int

const (
	Preparing Step = iota + 1
	Awaiting
	Done
)

// Reason is how a drag ended. The drop being synchronous — the object
// offers no IDataObjectAsyncCapability, so the target must finish inside
// Drop — every one of these is read off DoDragDrop's own return and its
// effect, and none of them is a guess (APP.md §3, ruled 2026-09-11).
type Reason int

const (
	// SelfDrop: the button came up over the caller's own window. Nothing was
	// extracted and the folder is gone; the page turns it into a Move.
	SelfDrop Reason = iota + 1
	// Cancelled: Escape, a release over nothing, Cancel from outside — or a
	// drop the target did nothing with (DROPEFFECT_NONE), which is what
	// Skip and a cancelled conflict dialog come back as. The drop is over
	// either way.
	Cancelled
	// Refused: the target took the drop and never asked for the paths.
	Refused
	// Moved: the effect was move and every staged item is gone — a
	// same-volume move, the cleanest end a drag has.
	Moved
	// Copied: the target copied the staged files (DROPEFFECT_COPY, or a
	// move that left them where they were).
	Copied
	// Failed: the extraction failed, or the drag itself could not be run.
	Failed
)

// String is the word the caller's view carries for the reason.
func (r Reason) String() string {
	switch r {
	case SelfDrop:
		return "self_drop"
	case Cancelled:
		return "cancelled"
	case Refused:
		return "refused"
	case Moved:
		return "moved"
	case Copied:
		return "copied"
	case Failed:
		return "failed"
	}
	return ""
}

// Phase is one report to Options.OnPhase: the step, and on Done the reason.
type Phase struct {
	Step   Step
	Reason Reason
}

// Options is one drag.
type Options struct {
	// Root is the folder every staging folder is made under —
	// %LOCALAPPDATA%\Enfold\drag in the application, never %TEMP%: ours to
	// scavenge, outside what OneDrive backs up, on the volume most drops
	// land on, which is what makes a move a rename.
	Root string
	// Window is the caller's top-level window: a release over it is a
	// self-drop, and its own thread is the one the drag thread attaches its
	// input to for the drag's length (GetWindowThreadProcessId returns "the
	// identifier of the thread that created the window", so the handle is
	// all this package needs to find it).
	Window uintptr
	// OnWindowThread runs f on the thread that owns Window and returns when
	// it has run — the shell's application.InvokeSync. The drag asks for
	// exactly one thing through it, before it starts its own thread:
	// ReleaseCapture, which "Releases the mouse capture from a window in
	// the current thread" and so has to be the window thread's call and not
	// the drag thread's. It is never used again, and no thread of this
	// package's ever waits on the caller's after that. Nil is a caller with
	// no capture to release.
	OnWindowThread func(f func())
	Items          []Item
	// Extract writes every item under dir. It runs inside the drop's own
	// GetData, on the target's thread; ctx is cancelled by Cancel. The
	// caller reports its own progress. An error fails the request, so the
	// target is never handed the names of files that are not there.
	Extract func(ctx context.Context, dir string) error
	// OnPhase hears the phases, from whichever thread they happen on; it
	// must not block on anything the drag itself waits for.
	OnPhase func(Phase)
	Log     func(format string, args ...any)
}

// Result is what the drag answers once DoDragDrop has returned, which is
// where the drag ends: the target had to finish the drop inside Drop, and
// OnPhase has reported Done with the reason before the answer arrives.
type Result struct {
	SelfDrop bool
	// Extracted: the extraction had run and succeeded by the time
	// DoDragDrop returned.
	Extracted bool
	// Effect is DoDragDrop's own answer, and it is the truth: with no
	// IDataObjectAsyncCapability on the object the target could not defer
	// the transfer, so the effect is what it actually did — NONE, COPY or
	// MOVE (APP.md §3, ruled 2026-09-11).
	Effect uint32
	// Folder is the drag's own folder, <Root>\<id>: the manifest at its
	// top, and every path CF_HDROP named beneath its "items" subfolder —
	// <Folder>\items\<name> — so a dropped path is this drag's exactly when
	// it lies under Folder.
	Folder string
}

// Outcome is one drag's whole answer, as the drag's own thread posts it
// back: what Run would return, down a channel instead. Exactly one arrives
// on the channel Start hands out, whatever became of the drag — a panic on
// that thread included.
type Outcome struct {
	Result Result
	Err    error
}

var (
	// ErrUnsupported is Begin on a platform with no native drag at all.
	ErrUnsupported = errors.New("dragout: the native drag is not available here")
	// ErrBusy is a second Begin while a drag is running: two cannot run at
	// once in one process.
	ErrBusy = errors.New("dragout: a drag is already running")
)

// discard is the log when the caller gave none.
func discard(string, ...any) {}

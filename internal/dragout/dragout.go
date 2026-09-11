// Package dragout is the drag out of the window (APP.md §3, ruled
// 2026-09-10 and 2026-09-11): the staged route, 7-Zip's and WinRAR's, with
// what their authors and users learnt folded in. One native OLE drag per
// gesture, with a pure-Go, cgo-free data object — agile, so that what runs
// inside the drop's GetData runs on Explorer's worker thread and never the
// WebView's — offering CF_HDROP by delayed rendering over a staging folder
// the caller's Extract fills at the first request after the button's
// release, CFSTR_PREFERREDDROPEFFECT = move beside it, and a scavenge that
// takes what a drop left behind once it is an hour old.
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
// once the target has the paths and until the staged files are gone, read
// and left alone, or the target says it has finished, and Done.
type Step int

const (
	Preparing Step = iota + 1
	Awaiting
	Done
)

// Reason is how a drag ended.
type Reason int

const (
	// SelfDrop: the button came up over the caller's own window. Nothing was
	// extracted and the folder is gone; the page turns it into a Move.
	SelfDrop Reason = iota + 1
	// Cancelled: Escape, a release over nothing, or Cancel from outside.
	Cancelled
	// Refused: the target took the drop and then let the data object go
	// without ever asking for the files.
	Refused
	// Moved: every staged file was taken away by the target — a same-volume
	// move, the cleanest end a drag has.
	Moved
	// Ended: the target said its transfer was over (EndOperation).
	Ended
	// Idle: the staged files were read and then left alone for five
	// seconds, or the target let the data object go and touched nothing for
	// as long. Whatever happens to the folder from here is the scavenge's.
	Idle
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
	case Ended:
		return "ended"
	case Idle:
		return "idle"
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
	// self-drop.
	Window uintptr
	Items  []Item
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

// Result is what Run answers once DoDragDrop has returned. The drag may go
// on after it — a target that negotiated the asynchronous protocol asks for
// the files on a thread of its own — and OnPhase says how it ends.
type Result struct {
	SelfDrop bool
	// Extracted: the extraction had run and succeeded by the time
	// DoDragDrop returned.
	Extracted bool
	// Effect is DoDragDrop's own answer. For an asynchronous drop it is
	// DROPEFFECT_NONE whatever the target went on to do (measured
	// 2026-09-11): what the target did is learnt from the files.
	Effect uint32
	// Folder is the drag's own folder, <Root>\<id>: the manifest at its
	// top, and every path CF_HDROP named beneath its "items" subfolder —
	// <Folder>\items\<name> — so a dropped path is this drag's exactly when
	// it lies under Folder.
	Folder string
}

var (
	// ErrUnsupported is Begin on a platform without OLE, or after an
	// InitOLE that failed.
	ErrUnsupported = errors.New("dragout: the native drag is not available here")
	// ErrBusy is a second Begin while a drag is running: two cannot run at
	// once in one process.
	ErrBusy = errors.New("dragout: a drag is already running")
)

// discard is the log when the caller gave none.
func discard(string, ...any) {}

package app

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/dreamxwarden01/enfold/internal/dragout"
	"github.com/dreamxwarden01/enfold/internal/format"
)

// The drag out of the window (APP.md §3, ruled 2026-09-10 and 2026-09-11):
// a press-and-move over selected rows calls Shell.DragOut, the shell runs
// one native drag on a thread of its own, and the core's part is what an
// operation is — the plan, the extraction the drop's own request runs into
// the staging folder, the strip's two phases and the archive held open
// meanwhile — with nothing of COM in it, and nothing of any one thread's.
// The native drag itself is internal/dragout's, reached through Deps.Drag
// so that a test drives the same operation with a fake and no COM runs.

// DragHandle is one native drag the shell runs: Run waits for it to end,
// on any goroutine but the window thread's — the drag has a thread of its
// own and DoDragDrop is modal there, not here — and Cancel from any.
type DragHandle interface {
	Run() (dragout.Result, error)
	Cancel()
}

// DragStarter begins a native drag over the options: dragout.Begin on
// Windows, a test's fake.
type DragStarter func(dragout.Options) (DragHandle, error)

// dragRoot is where every drag stages: Deps.DragRoot, or the drag folder
// under the data folder — %LOCALAPPDATA%\Enfold\drag in the application
// (APP.md §3).
func (c *Core) dragRoot() string {
	if c.deps.DragRoot != "" {
		return c.deps.DragRoot
	}
	return filepath.Join(c.deps.DataDir, "drag")
}

// dragIDs parses a drag's selection. An empty selection is params, as it
// is for an extract; so is the root, which a drag never offers — the
// explicit Extract all is for the whole archive, since staging serves it
// badly (APP.md §3).
func dragIDs(recordIDs []string) ([][16]byte, *Error) {
	if len(recordIDs) == 0 {
		return nil, coded(CodeParams)
	}
	ids := make([][16]byte, 0, len(recordIDs))
	for _, s := range recordIDs {
		rid, ok := parseID(s)
		if !ok || rid == format.RootID {
			return nil, coded(CodeParams)
		}
		ids = append(ids, rid)
	}
	return ids, nil
}

// dragPlan resolves a drag's selection into the items the staging folder
// takes: each selected record at the top of the folder under its own name,
// a selected directory's subtree beneath it, and a record whose own
// ancestor is selected travelling with that ancestor (merged.batch) — never
// the ancestors up to the root, which an extract writes and a drag does not,
// since what lands on the desktop is what was dragged. Two selected records
// that would take one name at the top are file.exists, as two records of
// one batch are for a move; an id that is not live is file.not_found. The
// order is parents before anything under them, which the extraction needs.
// Caller holds the state mutex.
func dragPlan(m *merged, ids [][16]byte) ([]extractItem, *Error) {
	recs, e := m.batch(ids)
	if e != nil {
		return nil, e
	}
	tops := make(map[string]bool, len(recs))
	var out []extractItem
	var walk func(r *mergedRec, under string, depth int)
	walk = func(r *mergedRec, under string, depth int) {
		rel := r.name
		if under != "" {
			rel = under + "/" + r.name
		}
		out = append(out, extractItem{
			id: r.id, parentID: r.parentID, isDir: r.isDir, path: m.path(r.id),
			out: rel, size: r.size, modifiedAt: r.modifiedAt, depth: depth,
		})
		if r.isDir {
			for _, k := range m.kids[r.id] {
				walk(k, rel, depth+1)
			}
		}
	}
	for _, r := range recs {
		key := format.FoldKey(r.name)
		if tops[key] {
			return nil, coded(CodeFileExists)
		}
		tops[key] = true
		walk(r, "", 0)
	}
	return out, nil
}

// dragItems is the plan as the native drag sees it: relative paths under
// the staging folder, with the platform's separator.
func dragItems(items []extractItem) []dragout.Item {
	out := make([]dragout.Item, 0, len(items))
	for _, it := range items {
		out = append(out, dragout.Item{
			Name: filepath.FromSlash(it.out), Size: it.size,
			ModTime: time.Unix(it.modifiedAt, 0), IsDir: it.isDir,
		})
	}
	return out
}

// DragOutPlan is what a drag of recordIDs would stage: the selected live
// records and everything beneath a selected directory, as relative paths
// under the staging folder, with their sizes and modified times. An empty
// or root selection is params.
func (c *Core) DragOutPlan(archiveID string, recordIDs []string) ([]dragout.Item, *Error) {
	oa, e := c.findArchive(archiveID)
	if e != nil {
		return nil, e
	}
	ids, e := dragIDs(recordIDs)
	if e != nil {
		return nil, e
	}
	c.mu.Lock()
	items, e := dragPlan(oa.merge(), ids)
	c.mu.Unlock()
	if e != nil {
		return nil, e
	}
	return dragItems(items), nil
}

// DragOut is one drag out of the window: the operation the strip shows,
// and the native drag the shell runs on a thread of its own. BeginDragOut
// makes it and the shell calls Run; the operation ends when the drag
// reports how it ended, which for a drop is after Run has returned.
// Nothing here belongs to a thread: the phases arrive from whichever
// thread they happen on, and the extraction runs on the target's.
type DragOut struct {
	c     *Core
	oa    *openArchive
	items []extractItem
	o     *op
	opID  string
	drag  DragHandle
	total uint64
	files int
	// records is what the gesture named — the selected records themselves,
	// folders included — which is what the strip counts (OpView.DragItems).
	records int

	mu      sync.Mutex
	results []FileOutcome
	err     error // the extraction's, or the drag's own
	reason  dragout.Reason
	// awaiting: the drag has reported Awaiting, so the files are the
	// target's and a cancel has nothing of the drag left to end.
	awaiting bool
	// done is closed when the drag has reported Done; runOver when Run has
	// returned, which is where a failed DoDragDrop's error lands.
	done     chan struct{}
	doneOnce sync.Once
	runOver  chan struct{}
	runOnce  sync.Once
}

// BeginDragOut plans the drag, begins the native drag over the plan — the
// staging folder exists from here, so a hover-time request can be answered
// with the final paths — and registers the operation of kind dragout that
// holds the archive open until the drag ends. window is the shell's
// top-level HWND, for the self-drop and for the thread the drag attaches
// its input to. The shell calls Run next, on the goroutine of its bound
// call. A drag already running is drag.busy; no native drag at all is
// drag.unsupported.
func (c *Core) BeginDragOut(archiveID string, recordIDs []string, window uintptr) (*DragOut, *Error) {
	oa, e := c.findArchive(archiveID)
	if e != nil {
		return nil, e
	}
	ids, e := dragIDs(recordIDs)
	if e != nil {
		return nil, e
	}
	if c.deps.Drag == nil {
		return nil, coded(CodeDragUnsupported)
	}
	c.mu.Lock()
	items, e := dragPlan(oa.merge(), ids)
	c.mu.Unlock()
	if e != nil {
		return nil, e
	}
	d := &DragOut{c: c, oa: oa, items: items, done: make(chan struct{}), runOver: make(chan struct{})}
	for _, it := range items {
		if it.depth == 0 {
			// What the gesture named, folders included: the strip says
			// "Extracting 2 items" from the moment the drag starts and
			// through all three phases (APP.md §3, OpView.DragItems).
			d.records++
		}
		if !it.isDir {
			// The files the extraction writes and their bytes: the bar's
			// measure while preparing (OpView.Items, OpView.Total); the
			// folders are not files.
			d.files++
			d.total += it.size
		}
	}
	h, err := c.deps.Drag(dragout.Options{
		Root: c.dragRoot(), Window: window, Items: dragItems(items),
		Extract: d.extract, OnPhase: d.onPhase, Log: c.log,
	})
	if err != nil {
		switch {
		case errors.Is(err, dragout.ErrBusy):
			return nil, coded(CodeDragBusy)
		case errors.Is(err, dragout.ErrUnsupported):
			return nil, coded(CodeDragUnsupported)
		}
		return nil, c.fail("drag out", err)
	}
	d.drag = h
	// The operation is registered before the drag runs, as every
	// operation is before its work: from here it is one of the things
	// holding the archive open (dropIfUnheldLocked), Leave lets it finish,
	// and Close, CancelOp and the shutdown cancel it (APP.md §2.3).
	d.opID = c.startOpWith("dragout", archiveID, func(o *op) {
		o.phase.Store("dragging")
		o.items.Store(int64(d.files))
		o.dragItems.Store(int64(d.records))
		d.o = o
	}, d.wait)
	return d, nil
}

// OpID is the dragout operation's id.
func (d *DragOut) OpID() string { return d.opID }

// Run is the shell's half: the native drag, which takes a thread of its
// own and answers here when it is over. This call waits, so it must not be
// made on the window's own thread — the shell makes it from the goroutine
// of a bound call, and the main thread is left free for the page. It
// returns when DoDragDrop has returned — the page has its answer then — and
// the operation goes on until the drag reports its end.
func (d *DragOut) Run() (DragOutResult, *Error) {
	res, err := d.drag.Run()
	if err != nil {
		d.mu.Lock()
		if d.err == nil {
			d.err = err
		}
		d.mu.Unlock()
	}
	d.runOnce.Do(func() { close(d.runOver) })
	out := DragOutResult{SelfDrop: res.SelfDrop, Extracted: res.Extracted, Effect: res.Effect, Folder: res.Folder, OpID: d.opID}
	if err != nil {
		// The drag itself could not be run — an OLE failure, the wrong
		// thread — which the drag has already reported as Done/failed and
		// the log has in full.
		return out, d.c.fail("drag out", err)
	}
	return out, nil
}

// extract is Options.Extract: the drop's own request, inside GetData on
// Explorer's thread, running the extract machinery with policy replace
// into the staging folder — the same plan, the same outcomes, progress by
// bytes on the operation under the phase preparing. It runs under the
// operation's context, so CancelOp and the kill switch reach it at its
// next chunk, and under the drag's own, so the drag's Cancel does too; an
// error fails the request, and Explorer abandons the drop rather than be
// handed the names of files that are not there (APP.md §3). A batch with a
// refused or failed record is such an error: the drop is all or nothing.
func (d *DragOut) extract(dragCtx context.Context, dir string) error {
	if err := d.o.ctx.Err(); err != nil {
		// The operation was cancelled before the drop's request arrived —
		// the kill switch, a shutdown, a cancel the hover outlived — and
		// has ended, its archive perhaps closed with it: the request fails
		// here, before anything is written or reported against it.
		d.mu.Lock()
		if d.err == nil {
			d.err = err
		}
		d.mu.Unlock()
		return err
	}
	ctx, cancel := context.WithCancel(d.o.ctx)
	defer cancel()
	stop := context.AfterFunc(dragCtx, cancel)
	defer stop()
	results, err := d.c.extractItems(ctx, d.o, d.oa, d.items, dir, ExtractReplace, "preparing")
	if err == nil {
		// extractItems looks at the context before each item and nowhere
		// after the last: a cancel that lands during the last file's
		// placement, or during the pass that sets the folders' times, comes
		// back as a success. It is still a cancel — the operation is over,
		// its archive perhaps closed with it — and the request has to fail
		// for it, or the target is handed the names of files the cleanup is
		// about to take (APP.md §3).
		err = ctx.Err()
	}
	if err == nil {
		for _, r := range results {
			switch r.Outcome {
			case "extracted", "created":
			default:
				code := r.Code
				if code == "" {
					code = CodeIO
				}
				err = coded(code)
			}
			if err != nil {
				break
			}
		}
	}
	d.mu.Lock()
	d.results = results
	if err != nil && d.err == nil {
		d.err = err
	}
	d.mu.Unlock()
	return err
}

// onPhase is Options.OnPhase, from whichever thread the drag reports on:
// preparing puts the bar on the strip with the plan's bytes, awaiting
// leaves that bar standing full — the staging is done and what Explorer
// does inside its own Drop is not ours to measure, and there is no Cancel
// from here (APP.md §3, ruled 2026-09-11) — and Done ends the operation
// with the reason as its DragResult.
func (d *DragOut) onPhase(p dragout.Phase) {
	switch p.Step {
	case dragout.Preparing:
		d.o.progress(0, d.total, "preparing")
	case dragout.Awaiting:
		d.mu.Lock()
		d.awaiting = true
		d.mu.Unlock()
		d.o.progress(d.total, d.total, "awaiting")
	case dragout.Done:
		d.mu.Lock()
		d.reason = p.Reason
		d.mu.Unlock()
		d.doneOnce.Do(func() { close(d.done) })
	}
}

// wait is the operation's own goroutine: it waits for the drag to end and
// answers with the extraction's outcomes. A cancel from outside — CancelOp,
// the kill switch, the shutdown — is passed to the drag, which ends at its
// next QueryContinueDrag during the hover and at the extraction's next
// chunk while preparing, bounded because a still mouse asks nothing and a
// cancelled operation must not linger over an archive the kill switch has
// closed. A drag already awaiting is not cancelled at all: there is nothing
// of it left to end — the files are Explorer's, the folder the watch's and
// the scavenge's — and the operation ends there and then rather than
// behind a strip the page has already left.
func (d *DragOut) wait(ctx context.Context, o *op) ([]FileOutcome, error) {
	select {
	case <-d.done:
	case <-ctx.Done():
		d.mu.Lock()
		awaiting := d.awaiting
		d.mu.Unlock()
		if !awaiting {
			d.drag.Cancel()
			select {
			case <-d.done:
			case <-time.After(dragCancelWait):
				d.c.log("drag out: the drag did not end within %s of the cancel; the operation ends without it", dragCancelWait)
			}
		}
		d.mu.Lock()
		if d.reason == 0 {
			d.reason = dragout.Cancelled
		}
		d.mu.Unlock()
	}
	d.mu.Lock()
	reason := d.reason
	d.mu.Unlock()
	if reason == dragout.Failed {
		// A failed DoDragDrop's error lands as Run returns, after the drag
		// has reported its end.
		<-d.runOver
	}
	d.mu.Lock()
	results, err := d.results, d.err
	d.mu.Unlock()
	o.dragResult.Store(reason.String())
	if err != nil {
		return results, err
	}
	if reason == dragout.Cancelled && ctx.Err() != nil {
		return results, ctx.Err()
	}
	return results, nil
}

// dragCancelWait bounds a cancelled operation's wait for the drag's end.
const dragCancelWait = 5 * time.Second

// startDragScavenge is the sweep of the staging root at launch and every
// ten minutes while running (APP.md §3): every manifested folder of ours
// whose state is not live and whose age is past an hour goes, one still in
// use retried with the bounded backoff and left for the next sweep. On a
// goroutine, since one pass can sit in that backoff for over a minute, and
// stopped with the core.
func (c *Core) startDragScavenge() {
	root := c.dragRoot()
	go dragout.Scavenge(root, dragout.ScavengeAge, c.log)
	c.scheduleDragScavenge(root)
}

func (c *Core) scheduleDragScavenge(root string) {
	c.deps.Clock.AfterFunc(dragout.ScavengeInterval, func() {
		c.mu.Lock()
		closed := c.closed
		c.mu.Unlock()
		if closed {
			return
		}
		go dragout.Scavenge(root, dragout.ScavengeAge, c.log)
		c.scheduleDragScavenge(root)
	})
}

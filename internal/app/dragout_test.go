package app

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dreamxwarden01/enfold/internal/dragout"
)

// The drag out of the window as the core sees it (APP.md §3): the plan, the
// operation of kind dragout with its two phases, the archive held open for
// the drag's length, and the scavenge at Start. The native drag is a fake
// here — what the shell's dragout.Begin would build, driven by the test —
// so no COM runs and no window exists.

// fakeDrag is one native drag stood in for: Run plays the script the test
// set, with the options the core built, and Cancel is observed through a
// channel a script can wait on.
type fakeDrag struct {
	opts   dragout.Options
	script func(f *fakeDrag) (dragout.Result, error)
	// ctx is the drag's own context as the real drag hands it to Extract,
	// cancelled by Cancel; cancelled is the same fact for a script to wait
	// on.
	ctx       context.Context
	cancel    context.CancelFunc
	cancelled chan struct{}
	once      sync.Once
}

func (f *fakeDrag) Run() (dragout.Result, error) { return f.script(f) }

func (f *fakeDrag) Cancel() {
	f.once.Do(func() {
		f.cancel()
		close(f.cancelled)
	})
}

// fakeDrags is the harness's Deps.Drag: every Begin builds a fakeDrag over
// the current script, or answers the error the test set.
type fakeDrags struct {
	mu     sync.Mutex
	script func(f *fakeDrag) (dragout.Result, error)
	err    error
	begun  []*fakeDrag
}

func (d *fakeDrags) begin(o dragout.Options) (DragHandle, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err != nil {
		return nil, d.err
	}
	f := &fakeDrag{opts: o, script: d.script, cancelled: make(chan struct{})}
	f.ctx, f.cancel = context.WithCancel(context.Background())
	d.begun = append(d.begun, f)
	return f, nil
}

// useDrags gives the harness's core a fake native drag and a staging root
// under the test's own folder.
func (h *harness) useDrags() *fakeDrags {
	d := &fakeDrags{}
	h.c.deps.Drag = d.begin
	return d
}

// dragSource is an archive with two files at the top and a folder with a
// subtree, and the ids of a.txt, the folder and the file inside it.
func (h *harness) dragSource(t *testing.T) (id, aID, dID, xID string) {
	t.Helper()
	id = h.openArchive(t, "Dragged")
	h.src(t, "d/x.txt", "xx")
	h.src(t, "d/inner/y.txt", "yyy")
	h.addFolder(t, id, rootID, filepath.Join(h.dir, "src", "d"), PolicySkip)
	h.add(t, id, rootID, PolicySkip, h.src(t, "a.txt", "a"), h.src(t, "b.txt", "bb"))
	aID = h.row(t, id, rootID, "a.txt").ID
	dID = h.row(t, id, rootID, "d").ID
	xID = h.row(t, id, dID, "x.txt").ID
	return id, aID, dID, xID
}

// The plan: the selected records at the top under their own names, a
// selected folder's subtree beneath it, a record whose ancestor is selected
// travelling with it, and params for an empty or root selection.
func TestDragOutPlan(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, aID, dID, xID := h.dragSource(t)

	items, e := h.c.DragOutPlan(id, []string{aID, dID})
	if e != nil {
		t.Fatal(e)
	}
	want := []dragout.Item{
		{Name: "a.txt", Size: 1},
		{Name: "d", IsDir: true},
		{Name: filepath.Join("d", "inner"), IsDir: true},
		{Name: filepath.Join("d", "inner", "y.txt"), Size: 3},
		{Name: filepath.Join("d", "x.txt"), Size: 2},
	}
	if len(items) != len(want) {
		t.Fatalf("planned %d items, want %d: %+v", len(items), len(want), items)
	}
	// The order is parents before anything under them; siblings come in the
	// tree's own order, which the check below does not pin.
	byName := map[string]dragout.Item{}
	for i, it := range items {
		byName[it.Name] = it
		if it.ModTime.IsZero() {
			t.Errorf("item %d has no modified time", i)
		}
	}
	for _, w := range want {
		got, ok := byName[w.Name]
		if !ok || got.Size != w.Size || got.IsDir != w.IsDir {
			t.Errorf("item %q: %+v, want %+v", w.Name, got, w)
		}
	}
	if items[0].Name != "a.txt" && items[0].Name != "d" {
		t.Errorf("the plan does not start at the top: %+v", items[0])
	}
	// A record whose ancestor is selected travels with the ancestor: planned
	// once, under the folder.
	items, e = h.c.DragOutPlan(id, []string{dID, xID})
	if e != nil || len(items) != 4 {
		t.Fatalf("a folder and a file inside it: %+v %v", items, e)
	}
	for _, it := range items {
		if it.Name == "x.txt" {
			t.Fatal("the file inside the selected folder was planned at the top as well")
		}
	}
	// Two selected records that would take one name at the top.
	h.src(t, "e/x.txt", "x2")
	h.addFolder(t, id, rootID, filepath.Join(h.dir, "src", "e"), PolicySkip)
	eID := h.row(t, id, rootID, "e").ID
	x2ID := h.row(t, id, eID, "x.txt").ID
	if _, e := h.c.DragOutPlan(id, []string{xID, x2ID}); !isCode(e, CodeFileExists) {
		t.Fatalf("two x.txt at the top: %v", e)
	}
	// Refusals: empty, the root, a malformed id, a dead id.
	for _, bad := range [][]string{{}, {rootID}, {aID, rootID}, {"nothex"}} {
		if _, e := h.c.DragOutPlan(id, bad); !isCode(e, CodeParams) {
			t.Fatalf("plan of %v: %v", bad, e)
		}
	}
	if _, e := h.c.DragOutPlan(id, []string{"00000000000000000000000000000001"}); !isCode(e, CodeFileNotFound) {
		t.Fatalf("a dead id: %v", e)
	}
	// And nothing without the archive open.
	if e := h.c.LeaveArchive(id); e != nil {
		t.Fatal(e)
	}
	if _, e := h.c.DragOutPlan(id, []string{aID}); !isCode(e, CodeArchiveNotOpen) {
		t.Fatalf("plan on a left archive: %v", e)
	}
}

// No native drag: drag.unsupported before anything is planned; a drag
// already running: drag.busy.
func TestDragOutRefusals(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, aID, _, _ := h.dragSource(t)
	if _, e := h.c.BeginDragOut(id, []string{aID}, 0); !isCode(e, CodeDragUnsupported) {
		t.Fatalf("without a native drag: %v", e)
	}
	drags := h.useDrags()
	drags.err = dragout.ErrBusy
	if _, e := h.c.BeginDragOut(id, []string{aID}, 0); !isCode(e, CodeDragBusy) {
		t.Fatalf("with a drag running: %v", e)
	}
	drags.err = dragout.ErrUnsupported
	if _, e := h.c.BeginDragOut(id, []string{aID}, 0); !isCode(e, CodeDragUnsupported) {
		t.Fatalf("with OLE unavailable: %v", e)
	}
	if _, e := h.c.BeginDragOut(id, nil, 0); !isCode(e, CodeParams) {
		t.Fatalf("an empty selection: %v", e)
	}
	for _, o := range h.status().Ops {
		if o.Kind == "dragout" {
			t.Fatalf("a refused drag registered an operation: %+v", o)
		}
	}
}

// The drag out proper: the operation's phases and events, the extraction
// into the staging folder with the same outcomes an extract has, the
// archive held open until the drag ends, and the reason as DragResult.
func TestDragOutRunsAsAnOperation(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, aID, dID, _ := h.dragSource(t)
	drags := h.useDrags()
	stage := t.TempDir()
	drags.script = func(f *fakeDrag) (dragout.Result, error) {
		// A hover, a release, the drop's own request: Preparing, the
		// extraction, Awaiting.
		f.opts.OnPhase(dragout.Phase{Step: dragout.Preparing})
		if err := f.opts.Extract(f.ctx, stage); err != nil {
			f.opts.OnPhase(dragout.Phase{Step: dragout.Done, Reason: dragout.Failed})
			return dragout.Result{Folder: stage}, nil
		}
		f.opts.OnPhase(dragout.Phase{Step: dragout.Awaiting})
		return dragout.Result{Extracted: true, Effect: 2, Folder: stage}, nil
	}
	h.rec.reset()
	d, e := h.c.BeginDragOut(id, []string{aID, dID}, 0x1234)
	if e != nil {
		t.Fatal(e)
	}
	f := drags.begun[0]
	if f.opts.Window != 0x1234 || f.opts.Root != filepath.Join(h.dir, "data", "drag") || len(f.opts.Items) != 5 {
		t.Fatalf("the drag was begun with %+v", f.opts)
	}
	// The operation exists before the drag runs, in its hover phase, with
	// the plan's count already on it.
	o, e := h.c.Op(d.OpID())
	// Items is the files the extraction will write and the bar's measure;
	// DragItems is what the gesture named, folders included, which is what
	// the strip counts — "Extracting 2 items" (APP.md §3).
	if e != nil || o.Kind != "dragout" || o.Phase != "dragging" || o.Items != 3 || o.DragItems != 2 || o.Finished {
		t.Fatalf("the operation before the drag: %+v %v", o, e)
	}

	res, e := d.Run()
	if e != nil {
		t.Fatal(e)
	}
	if res.OpID != d.OpID() || !res.Extracted || res.SelfDrop || res.Effect != 2 || res.Folder != stage {
		t.Fatalf("the drag's result: %+v", res)
	}
	// The strip's first phase: preparing, by bytes, with the count.
	p := h.rec.waitFor(t, EventOpProgress, func(p any) bool {
		o, ok := p.(OpView)
		return ok && o.ID == d.OpID() && o.Phase == "preparing"
	}).(OpView)
	if p.Items != 3 || p.DragItems != 2 || p.Total != 6 {
		t.Fatalf("preparing: %+v", p)
	}
	// Then awaiting: the bar stands full — the staging is done, and what
	// the target does inside its own Drop is not measured here (APP.md §3,
	// ruled 2026-09-11) — and there is nothing to cancel.
	a := h.rec.waitFor(t, EventOpProgress, func(p any) bool {
		o, ok := p.(OpView)
		return ok && o.ID == d.OpID() && o.Phase == "awaiting"
	}).(OpView)
	if a.Total != 6 || a.Done != 6 || a.Finished {
		t.Fatalf("awaiting: %+v", a)
	}
	// The files are in the staging folder under the plan's names.
	for rel, want := range map[string]string{"a.txt": "a", "d/x.txt": "xx", "d/inner/y.txt": "yyy"} {
		b, err := os.ReadFile(filepath.Join(stage, filepath.FromSlash(rel)))
		if err != nil || string(b) != want {
			t.Fatalf("%s in the staging folder: %q %v", rel, b, err)
		}
	}
	// The drag holds the archive like an operation: leaving the page lets
	// it finish rather than closing under it.
	if e := h.c.LeaveArchive(id); e != nil {
		t.Fatal(e)
	}
	if !h.isHeld(id) {
		t.Fatal("leaving the page closed the archive under a drag still awaiting")
	}
	if _, e := h.c.Stat(id); !isCode(e, CodeArchiveNotOpen) {
		t.Fatalf("a left archive still answers the page: %v", e)
	}
	// Explorer moves the files away: the drag ends, and with it the
	// operation and the archive.
	f.opts.OnPhase(dragout.Phase{Step: dragout.Done, Reason: dragout.Moved})
	done := h.rec.waitOp(t, d.OpID())
	if done.Error != "" || done.DragResult != "moved" || done.Kind != "dragout" {
		t.Fatalf("the finished drag: %+v", done)
	}
	if len(done.Results) != 5 {
		t.Fatalf("outcomes: %+v", done.Results)
	}
	for _, r := range done.Results {
		if r.Outcome != "extracted" && r.Outcome != "created" {
			t.Fatalf("outcome %+v", r)
		}
		if filepath.Dir(r.Path) != stage && filepath.Dir(filepath.Dir(r.Path)) != stage && filepath.Dir(filepath.Dir(filepath.Dir(r.Path))) != stage {
			t.Fatalf("an outcome outside the staging folder: %+v", r)
		}
	}
	if h.isHeld(id) {
		t.Fatal("the archive stayed open after the drag ended and its page was left")
	}
}

// Cancel during preparing: the extraction returns at its next chunk with
// the context's error, the callback answers the drag with it — the drop's
// GetData fails and Explorer abandons the drop — and the operation ends
// cancelled.
func TestDragOutCancelDuringPreparing(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Cancelled")
	big := h.src(t, "big.bin", string(incompressible(t, 8<<20)))
	h.add(t, id, rootID, PolicySkip, big)
	bigID := h.row(t, id, rootID, "big.bin").ID
	drags := h.useDrags()
	stage := t.TempDir()
	var extractErr error
	drags.script = func(f *fakeDrag) (dragout.Result, error) {
		f.opts.OnPhase(dragout.Phase{Step: dragout.Preparing})
		extractErr = f.opts.Extract(f.ctx, stage)
		if extractErr != nil {
			f.opts.OnPhase(dragout.Phase{Step: dragout.Done, Reason: dragout.Cancelled})
			return dragout.Result{Folder: stage}, nil
		}
		f.opts.OnPhase(dragout.Phase{Step: dragout.Awaiting})
		return dragout.Result{Extracted: true, Folder: stage}, nil
	}
	d, e := h.c.BeginDragOut(id, []string{bigID}, 0)
	if e != nil {
		t.Fatal(e)
	}
	// The cancel lands inside a progress event of the extraction — on the
	// drag's own goroutine, some way into the file.
	var once sync.Once
	h.rec.onEvent(func(name string, payload any) {
		o, ok := payload.(OpView)
		if !ok || name != EventOpProgress || o.ID != d.OpID() || o.Phase != "preparing" || o.Done == 0 {
			return
		}
		once.Do(func() {
			if e := h.c.CancelOp(d.OpID()); e != nil {
				t.Errorf("cancel: %v", e)
			}
		})
	})
	defer h.rec.onEvent(nil)
	res, e := d.Run()
	if e != nil {
		t.Fatal(e)
	}
	if res.Extracted {
		t.Fatal("a cancelled extraction counted as extracted")
	}
	if extractErr == nil {
		t.Fatal("the callback returned nil after the cancel: GetData would have handed out the names")
	}
	o := h.rec.waitOp(t, d.OpID())
	if o.Error != CodeOpCancelled || o.DragResult != "cancelled" {
		t.Fatalf("the cancelled drag: %+v", o)
	}
	if _, err := os.Stat(filepath.Join(stage, "big.bin")); !os.IsNotExist(err) {
		t.Fatalf("a cancelled extraction left the file: %v", err)
	}
}

// A cancel that lands during the last file's placement — after the
// extraction's last look at the context, with every byte written and the
// folders' times still to set — must still fail the request: the callback
// checks the context after extractItems returns, so GetData answers
// E_UNEXPECTED, Explorer abandons the drop, and the operation ends
// op.cancelled rather than awaiting behind a strip the cancel already left.
func TestDragOutCancelDuringTheLastPlacement(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, _, dID, _ := h.dragSource(t)
	drags := h.useDrags()
	stage := t.TempDir()
	var extractErr error
	drags.script = func(f *fakeDrag) (dragout.Result, error) {
		f.opts.OnPhase(dragout.Phase{Step: dragout.Preparing})
		extractErr = f.opts.Extract(f.ctx, stage)
		if extractErr != nil {
			f.opts.OnPhase(dragout.Phase{Step: dragout.Done, Reason: dragout.Cancelled})
			return dragout.Result{Folder: stage}, nil
		}
		f.opts.OnPhase(dragout.Phase{Step: dragout.Awaiting})
		return dragout.Result{Extracted: true, Folder: stage}, nil
	}
	// The folder d and everything under it: the last placement is a file's,
	// and the timestamp pass over d comes after it.
	d, e := h.c.BeginDragOut(id, []string{dID}, 0)
	if e != nil {
		t.Fatal(e)
	}
	placements := 0
	h.c.extractFS = extractFS{place: func(tmp, path string, replace bool) error {
		placements++
		if placements == 2 {
			// The last file of the plan: the cancel lands while it is
			// being placed, and the placement itself succeeds.
			if e := h.c.CancelOp(d.OpID()); e != nil {
				t.Errorf("cancel: %v", e)
			}
		}
		return extractFS{}.placeFile(tmp, path, replace)
	}}
	res, e := d.Run()
	if e != nil {
		t.Fatal(e)
	}
	if placements != 2 {
		t.Fatalf("%d placements, want the plan's 2 files", placements)
	}
	if res.Extracted {
		t.Fatal("a cancelled extraction counted as extracted")
	}
	if extractErr == nil {
		t.Fatal("the callback returned nil after the cancel: GetData would have handed out the names")
	}
	o := h.rec.waitOp(t, d.OpID())
	if o.Error != CodeOpCancelled || o.DragResult != "cancelled" {
		t.Fatalf("the cancelled drag: %+v", o)
	}
}

// A self-drop finishes the operation at once with a result of its own, so
// the page knows to Move, and nothing was extracted.
func TestDragOutSelfDropFinishesAtOnce(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, aID, _, _ := h.dragSource(t)
	drags := h.useDrags()
	stage := t.TempDir()
	extracts := 0
	drags.script = func(f *fakeDrag) (dragout.Result, error) {
		f.opts.OnPhase(dragout.Phase{Step: dragout.Done, Reason: dragout.SelfDrop})
		return dragout.Result{SelfDrop: true, Folder: stage}, nil
	}
	d, e := h.c.BeginDragOut(id, []string{aID}, 0)
	if e != nil {
		t.Fatal(e)
	}
	drags.begun[0].opts.Extract = func(ctx context.Context, dir string) error { extracts++; return nil }
	res, e := d.Run()
	if e != nil || !res.SelfDrop || res.Extracted {
		t.Fatalf("a self-drop's result: %+v %v", res, e)
	}
	o := h.rec.waitOp(t, d.OpID())
	if o.Error != "" || o.DragResult != "self_drop" || len(o.Results) != 0 {
		t.Fatalf("the self-drop's operation: %+v", o)
	}
	if extracts != 0 {
		t.Fatal("a self-drop ran the extraction")
	}
	// The page still has the archive: nothing was left.
	if _, e := h.c.Stat(id); e != nil {
		t.Fatalf("the archive after a self-drop: %v", e)
	}
}

// Close archive is the kill switch: a drag still in the hover is cancelled
// through its operation, ends at the drag's next question, and the archive
// closes without waiting for it.
func TestCloseDuringADrag(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, aID, _, _ := h.dragSource(t)
	drags := h.useDrags()
	drags.script = func(f *fakeDrag) (dragout.Result, error) {
		// The button is down and the cursor wanders: nothing happens until
		// Cancel, which is Escape from the outside.
		<-f.cancelled
		f.opts.OnPhase(dragout.Phase{Step: dragout.Done, Reason: dragout.Cancelled})
		return dragout.Result{}, nil
	}
	d, e := h.c.BeginDragOut(id, []string{aID}, 0)
	if e != nil {
		t.Fatal(e)
	}
	ran := make(chan DragOutResult, 1)
	go func() {
		res, _ := d.Run()
		ran <- res
	}()
	if e := h.c.CloseArchive(id); e != nil {
		t.Fatalf("close under a drag: %v", e)
	}
	select {
	case res := <-ran:
		if res.SelfDrop || res.Extracted {
			t.Fatalf("a cancelled drag's result: %+v", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the kill switch did not end the drag")
	}
	o := h.rec.waitOp(t, d.OpID())
	if o.Error != CodeOpCancelled || o.DragResult != "cancelled" {
		t.Fatalf("the drag under the kill switch: %+v", o)
	}
	if h.isHeld(id) {
		t.Fatal("the archive is still held after the kill switch")
	}
	if _, e := h.c.Stat(id); !isCode(e, CodeArchiveNotOpen) {
		t.Fatalf("the archive after the kill switch: %v", e)
	}
}

// Close during awaiting: the files are Explorer's and the folder the
// watch's, so the operation ends at once — no cancel is passed to a drag
// that has nothing left to end, and nothing waits behind the bound — and
// the archive closes with it.
func TestCloseDuringAwaitingFinishesAtOnce(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, aID, _, _ := h.dragSource(t)
	drags := h.useDrags()
	stage := t.TempDir()
	drags.script = func(f *fakeDrag) (dragout.Result, error) {
		f.opts.OnPhase(dragout.Phase{Step: dragout.Preparing})
		if err := f.opts.Extract(f.ctx, stage); err != nil {
			return dragout.Result{Folder: stage}, err
		}
		f.opts.OnPhase(dragout.Phase{Step: dragout.Awaiting})
		return dragout.Result{Extracted: true, Folder: stage}, nil
	}
	d, e := h.c.BeginDragOut(id, []string{aID}, 0)
	if e != nil {
		t.Fatal(e)
	}
	res, e := d.Run()
	if e != nil || !res.Extracted {
		t.Fatalf("the drag's result: %+v %v", res, e)
	}
	h.rec.waitFor(t, EventOpProgress, func(p any) bool {
		o, ok := p.(OpView)
		return ok && o.ID == d.OpID() && o.Phase == "awaiting"
	})
	start := time.Now()
	if e := h.c.CloseArchive(id); e != nil {
		t.Fatalf("close under an awaiting drag: %v", e)
	}
	o := h.rec.waitOp(t, d.OpID())
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("the operation took %s to end after the kill switch; nothing was left to wait for", took)
	}
	if o.Error != CodeOpCancelled || o.DragResult != "cancelled" || len(o.Results) != 1 {
		t.Fatalf("the awaiting drag under the kill switch: %+v", o)
	}
	select {
	case <-drags.begun[0].cancelled:
		t.Fatal("a drag past its extraction was cancelled: there was nothing of it left to end")
	default:
	}
	if h.isHeld(id) {
		t.Fatal("the archive is still held after the kill switch")
	}
	// The drag reports its own end when DoDragDrop returns, which the
	// finished operation takes no notice of.
	drags.begun[0].opts.OnPhase(dragout.Phase{Step: dragout.Done, Reason: dragout.Copied})
	if o, e := h.c.Op(d.OpID()); e != nil || o.DragResult != "cancelled" {
		t.Fatalf("a late Done changed the finished operation: %+v %v", o, e)
	}
}

// The scavenge at Start: a manifested folder of ours under the staging root,
// past the hour and not live, goes; a young one and a foreign one stay.
func TestDragScavengeAtStart(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "data", "drag")
	mk := func(name string, m *dragout.Manifest) string {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "payload.bin"), []byte("plaintext"), 0o600); err != nil {
			t.Fatal(err)
		}
		if m != nil {
			if err := dragout.WriteManifest(p, *m); err != nil {
				t.Fatal(err)
			}
		}
		return p
	}
	now := time.Now()
	old := mk("aaaaaaaa", &dragout.Manifest{Tool: "enfold", Version: 1, State: dragout.StateHandedOut, Created: now.Add(-2 * time.Hour)})
	young := mk("bbbbbbbb", &dragout.Manifest{Tool: "enfold", Version: 1, State: dragout.StateHandedOut, Created: now.Add(-5 * time.Minute)})
	foreign := mk("cccccccc", nil)

	rec := &recorder{}
	c, err := New(Deps{Events: rec, Clock: newFakeClock(), DataDir: filepath.Join(dir, "data")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(old); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the launch sweep did not remove the old folder")
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, stays := range []string{young, foreign} {
		if _, err := os.Stat(stays); err != nil {
			t.Fatalf("the sweep removed %s: %v", filepath.Base(stays), err)
		}
	}
}

// The staged copy pays no fsync and a real extract does (APP.md §3, ruled
// 2026-09-11, after the bar was watched stalling for seconds at the end of
// every file): the same plan, the same temporary, the same placement, and
// the flush is the one difference between them. The staged copy is
// disposable — Explorer's own copy is what lands, and the scavenge takes
// whatever is left — so its durability is the destination's business.
func TestTheStagedCopySkipsTheFsync(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, aID, _, _ := h.dragSource(t)
	syncs := 0
	h.c.extractFS = extractFS{sync: func(f *os.File) error {
		syncs++
		return extractFS{}.syncFile(f)
	}}

	drags := h.useDrags()
	stage := t.TempDir()
	drags.script = func(f *fakeDrag) (dragout.Result, error) {
		f.opts.OnPhase(dragout.Phase{Step: dragout.Preparing})
		if err := f.opts.Extract(f.ctx, stage); err != nil {
			f.opts.OnPhase(dragout.Phase{Step: dragout.Done, Reason: dragout.Failed})
			return dragout.Result{Folder: stage}, err
		}
		f.opts.OnPhase(dragout.Phase{Step: dragout.Awaiting})
		return dragout.Result{Extracted: true, Effect: 2, Folder: stage}, nil
	}
	d, e := h.c.BeginDragOut(id, []string{aID}, 0)
	if e != nil {
		t.Fatal(e)
	}
	res, e := d.Run()
	if e != nil || !res.Extracted {
		t.Fatalf("the staging drag: %+v %v", res, e)
	}
	if b, err := os.ReadFile(filepath.Join(stage, "a.txt")); err != nil || string(b) != "a" {
		t.Fatalf("the staged file: %q %v", b, err)
	}
	if syncs != 0 {
		t.Errorf("the staging extract made %d fsync(s): the bar stalls at every file's end for them", syncs)
	}

	// The same record, extracted the way the user asks for it: the flush is
	// paid, once for the file.
	out := t.TempDir()
	opID, e := h.c.Extract(id, []string{aID}, out, ExtractSkip, nil)
	if e != nil {
		t.Fatal(e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != "" {
		t.Fatalf("the extract: %+v", o)
	}
	if syncs != 1 {
		t.Errorf("a real extract made %d fsync(s), want one for its one file", syncs)
	}
}

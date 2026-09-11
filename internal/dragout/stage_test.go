package dragout

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"
)

// The staged route's portable half. None of these opens a window, starts a
// drag or writes anything outside t.TempDir(): the root is injected
// everywhere precisely so that a test never touches %LOCALAPPDATA%, where
// the real application's vault lives.

// testLog collects the log lines of one test.
type testLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *testLog) printf(format string, args ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}

func (l *testLog) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

// phaseLog collects the phases a stage reports, in order.
type phaseLog struct {
	mu     sync.Mutex
	phases []Phase
}

func (p *phaseLog) on(ph Phase) {
	p.mu.Lock()
	p.phases = append(p.phases, ph)
	p.mu.Unlock()
}

func (p *phaseLog) list() []Phase {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Phase(nil), p.phases...)
}

// isolateStages gives one test the stage registry to itself.
func isolateStages(t *testing.T) {
	t.Helper()
	liveStages.mu.Lock()
	prev := liveStages.m
	liveStages.m = make(map[string]*stage)
	liveStages.mu.Unlock()
	t.Cleanup(func() {
		liveStages.mu.Lock()
		liveStages.m = prev
		liveStages.mu.Unlock()
	})
}

// writeItems is a stand-in for the core's extract: every item lands under
// dir with its name as its content, folders made on the way.
func writeItems(items []Item) func(ctx context.Context, dir string) error {
	return func(ctx context.Context, dir string) error {
		for _, it := range items {
			if err := ctx.Err(); err != nil {
				return err
			}
			p := filepath.Join(dir, filepath.FromSlash(it.Name))
			if it.IsDir {
				if err := os.MkdirAll(p, 0o700); err != nil {
					return err
				}
				continue
			}
			if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(p, []byte(it.Name), 0o600); err != nil {
				return err
			}
		}
		return nil
	}
}

// newTestStage makes a stage under a fresh root with the given items and a
// writing extractor unless one is given.
func newTestStage(t *testing.T, items []Item, extract func(ctx context.Context, dir string) error) (*stage, *phaseLog, *testLog) {
	t.Helper()
	if extract == nil {
		extract = writeItems(items)
	}
	ph := &phaseLog{}
	lg := &testLog{}
	s, err := newStage(Options{Root: t.TempDir(), Items: items, Extract: extract, OnPhase: ph.on, Log: lg.printf}, time.Hour)
	if err != nil {
		t.Fatalf("newStage: %v", err)
	}
	t.Cleanup(s.finish)
	return s, ph, lg
}

// endDrag is DoDragDrop returning, with the watch's goroutine stepped
// aside so that the test drives poll by hand at the moments of its
// choosing.
func endDrag(s *stage, end Reason) {
	s.noteDragEnded(end)
	s.stopOnce.Do(func() { close(s.stop) })
}

// ---------------------------------------------------------------------------
// DROPFILES.

// decodeDropFiles is the consumer's side, written out longhand: it reads
// the block the way DragQueryFile would, so that the test asserts against
// the documented layout rather than against the encoder's own idea of it.
func decodeDropFiles(t *testing.T, blob []byte) (pFiles uint32, ptX, ptY int32, fNC, fWide uint32, paths []string) {
	t.Helper()
	if len(blob) < dropFilesHeaderSize {
		t.Fatalf("the block is %d bytes, shorter than the %d-byte DROPFILES header", len(blob), dropFilesHeaderSize)
	}
	pFiles = binary.LittleEndian.Uint32(blob[0:])
	ptX = int32(binary.LittleEndian.Uint32(blob[4:]))
	ptY = int32(binary.LittleEndian.Uint32(blob[8:]))
	fNC = binary.LittleEndian.Uint32(blob[12:])
	fWide = binary.LittleEndian.Uint32(blob[16:])

	list := blob[pFiles:]
	if len(list)%2 != 0 {
		t.Fatalf("the file list is %d bytes, not a whole number of UTF-16 units", len(list))
	}
	units := make([]uint16, len(list)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(list[2*i:])
	}
	if len(units) < 1 || units[len(units)-1] != 0 {
		t.Fatal("the file list does not end in a NUL")
	}
	// "An additional null character is appended to the final string to
	// terminate the array": drop that one, then split on the per-path NULs.
	units = units[:len(units)-1]
	if len(units) == 0 {
		return pFiles, ptX, ptY, fNC, fWide, nil
	}
	if units[len(units)-1] != 0 {
		t.Fatal("the last path is not NUL-terminated (no double NUL at the end)")
	}
	for _, part := range strings.Split(string(utf16.Decode(units)), "\x00") {
		if part == "" {
			continue
		}
		paths = append(paths, part)
	}
	return pFiles, ptX, ptY, fNC, fWide, paths
}

func TestEncodeDropFilesLayout(t *testing.T) {
	// The constants first: they are a derivation from the documented field
	// types (DWORD, POINT = two LONGs, BOOL = int, all 32-bit, no pointers),
	// and the derivation is the thing to pin.
	if dropFilesOffPFiles != 0 || dropFilesOffPoint != 4 || dropFilesOffFNC != 12 || dropFilesOffFWide != 16 {
		t.Fatalf("DROPFILES offsets are pFiles=%d pt=%d fNC=%d fWide=%d, want 0/4/12/16",
			dropFilesOffPFiles, dropFilesOffPoint, dropFilesOffFNC, dropFilesOffFWide)
	}
	if dropFilesHeaderSize != 20 {
		t.Fatalf("DROPFILES is %d bytes, want 20 (4 + 8 + 4 + 4)", dropFilesHeaderSize)
	}
	if cfHDrop != 15 {
		t.Fatalf("CF_HDROP is %d, want 15", cfHDrop)
	}

	want := []string{`C:\Users\a\AppData\Local\Enfold\drag\0a1b2c3d\one.bin`, `C:\x\two.txt`}
	blob := encodeDropFiles(want)

	pFiles, ptX, ptY, fNC, fWide, got := decodeDropFiles(t, blob)
	if pFiles != dropFilesHeaderSize {
		t.Errorf("pFiles = %d, want %d: the list starts immediately after the header", pFiles, dropFilesHeaderSize)
	}
	if ptX != 0 || ptY != 0 || fNC != 0 {
		t.Errorf("pt = (%d,%d), fNC = %d; all three belong to a WM_DROPFILES receiver and must be zero here", ptX, ptY, fNC)
	}
	if fWide == 0 {
		t.Error("fWide is zero, which claims the paths are ANSI; they are UTF-16")
	}
	if len(got) != len(want) {
		t.Fatalf("decoded %d path(s), want %d: %q", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("path %d decoded as %q, want %q", i, got[i], want[i])
		}
	}
	// The exact length, computed from the documented shape: the header, then
	// each path plus its NUL, then the list's own NUL, two bytes per UTF-16
	// unit.
	units := 1
	for _, p := range want {
		units += len(utf16.Encode([]rune(p))) + 1
	}
	if n := dropFilesHeaderSize + 2*units; len(blob) != n {
		t.Errorf("the block is %d bytes, want %d", len(blob), n)
	}
	// And the tail really is two NUL units, which is the whole of "double
	// null-terminated".
	tail := blob[len(blob)-4:]
	if tail[0] != 0 || tail[1] != 0 || tail[2] != 0 || tail[3] != 0 {
		t.Errorf("the block does not end in two NUL units: % x", tail)
	}
}

func TestEncodeDropFilesUnicodeAndEmptyList(t *testing.T) {
	// A surrogate pair and a non-ASCII name: fWide is a claim about these
	// bytes, and a path that does not survive the round trip would be a file
	// the target cannot find.
	want := []string{`C:\Users\a\档案\proto-5GiB.bin`, `C:\Users\a\emoji-\U0001F600.bin`}
	_, _, _, _, fWide, got := decodeDropFiles(t, encodeDropFiles(want))
	if fWide == 0 {
		t.Error("fWide is zero for UTF-16 paths")
	}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("path %d round-tripped as %q, want %q", i, got, want[i])
		}
	}
	// An empty list is still a valid block: the header plus the array's own
	// terminator.
	blob := encodeDropFiles(nil)
	if len(blob) != dropFilesHeaderSize+2 {
		t.Fatalf("an empty list encoded to %d bytes, want %d", len(blob), dropFilesHeaderSize+2)
	}
	if _, _, _, _, _, paths := decodeDropFiles(t, blob); len(paths) != 0 {
		t.Fatalf("an empty list decoded to %q", paths)
	}
}

func TestEncodeDropEffect(t *testing.T) {
	// "The structure's hGlobal member points to a DWORD value": four bytes,
	// and nothing else in the block. The default is 7-Zip's: copy and move
	// allowed, move preferred, because the staged file is disposable and a
	// same-volume move is one rename.
	blob := encodeDropEffect(preferredEffect)
	if len(blob) != 4 {
		t.Fatalf("CFSTR_PREFERREDDROPEFFECT rendered %d bytes, want 4 (a DWORD)", len(blob))
	}
	if v := binary.LittleEndian.Uint32(blob); v != dropEffectMove {
		t.Fatalf("the DWORD is %d, want DROPEFFECT_MOVE (%d)", v, dropEffectMove)
	}
	if allowedEffects != dropEffectCopy|dropEffectMove {
		t.Fatalf("allowed effects = %d, want COPY|MOVE", allowedEffects)
	}
}

// ---------------------------------------------------------------------------
// The staging folder and the state machine.

func TestStagePathsNameTheTopLevelItems(t *testing.T) {
	items := []Item{
		{Name: "photo.jpg", Size: 10},
		{Name: "Docs", IsDir: true},
		{Name: `Docs\a.txt`, Size: 1},
		{Name: `Docs\inner`, IsDir: true},
		{Name: `Docs\inner\b.txt`, Size: 1},
	}
	s, _, _ := newTestStage(t, items, nil)
	if len(s.paths) != len(items) {
		t.Fatalf("%d paths for %d items", len(s.paths), len(items))
	}
	// Every item lies under the items folder beneath the drag's own, where
	// the manifest sits at the top (APP.md §3).
	if s.itemsDir != filepath.Join(s.root, "items") {
		t.Fatalf("the items folder is %q, want <folder>\\items", s.itemsDir)
	}
	if fi, err := os.Stat(s.itemsDir); err != nil || !fi.IsDir() {
		t.Fatalf("the items folder does not exist before the drag: %v", err)
	}
	for i, p := range s.paths {
		if p != filepath.Join(s.itemsDir, items[i].Name) {
			t.Errorf("path %d is %q, want it under the items folder", i, p)
		}
	}
	// CF_HDROP names the folder, not the files inside it: naming the files
	// would drop them loose into the destination and the subfolder would
	// never appear there at all.
	if len(s.drop) != 2 || s.drop[0] != filepath.Join(s.itemsDir, "photo.jpg") || s.drop[1] != filepath.Join(s.itemsDir, "Docs") {
		t.Fatalf("CF_HDROP names %q, want the two top-level items", s.drop)
	}
	// The watch covers every item, the folders told apart: a folder is
	// asked whether it is there, never whether it is open.
	if !s.armWatch() || len(s.watch) != 5 {
		t.Fatalf("the watch covers %d items, want all 5", len(s.watch))
	}
	for i, w := range s.watch {
		if w.isDir != items[i].IsDir {
			t.Errorf("watch entry %d isDir=%v, want %v", i, w.isDir, items[i].IsDir)
		}
	}
	if filepath.Base(filepath.Dir(s.root)) == "" || len(filepath.Base(s.root)) != 8 {
		t.Fatalf("the staging folder is %q, want <root>\\<8 hex>", s.root)
	}
	// An item that escapes the folder, or that leaves nothing at the top,
	// is refused before a folder exists for it.
	for _, bad := range [][]Item{
		{{Name: `..\evil.txt`}},
		{{Name: `C:\evil.txt`}},
		{{Name: `sub\only.txt`}},
		{},
	} {
		root := t.TempDir()
		if _, err := newStage(Options{Root: root, Items: bad, Extract: writeItems(bad)}, time.Hour); err == nil {
			t.Errorf("items %v were accepted", bad)
		}
		if entries, _ := os.ReadDir(root); len(entries) != 0 {
			t.Errorf("a refused stage left %d folder(s) behind for %v", len(entries), bad)
		}
	}
}

func TestStageArmStateMachine(t *testing.T) {
	items := []Item{{Name: "one.bin", Size: 1000}, {Name: "two.bin", Size: 64}}
	s, ph, _ := newTestStage(t, items, nil)

	// Before the release: the final paths, over files that do not exist yet.
	paths, ok := s.requestPaths()
	if !ok {
		t.Fatal("a hover request was refused")
	}
	if len(paths) != len(items) {
		t.Fatalf("a hover request named %d path(s), want %d", len(paths), len(items))
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s exists during the hover; nothing should be written before the release", filepath.Base(p))
		}
	}
	s.mu.Lock()
	early, armed := s.early, s.armed
	s.mu.Unlock()
	if early != 1 || armed {
		t.Fatalf("after one hover request: early=%d armed=%v, want 1/false", early, armed)
	}
	// A hover request hands the paths out: the folder outlives the drop from
	// here, and the manifest says so.
	if m, _ := ReadManifest(s.root); m.State != StateHandedOut {
		t.Fatalf("after a hover request the manifest says %q, want %q", m.State, StateHandedOut)
	}
	if len(ph.list()) != 0 {
		t.Fatalf("a hover request reported phases %v; nothing has happened yet", ph.list())
	}

	// The button comes up, not over our own window.
	s.arm(false)
	s.mu.Lock()
	armed = s.armed
	s.mu.Unlock()
	if !armed {
		t.Fatal("the stage is not armed after arm()")
	}

	// The first request after the release extracts, with Preparing before
	// and Awaiting after.
	after, ok := s.requestPaths()
	if !ok {
		t.Fatal("the first post-release request was refused")
	}
	for i, p := range after {
		if p != paths[i] {
			t.Errorf("the path changed after the release: %q, was %q", p, paths[i])
		}
		blob, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("%s was not written by the extraction: %v", filepath.Base(p), err)
		}
		if string(blob) != items[i].Name {
			t.Errorf("%s holds %q", filepath.Base(p), blob)
		}
	}
	if got := ph.list(); len(got) != 2 || got[0].Step != Preparing || got[1].Step != Awaiting {
		t.Fatalf("the extraction reported %v, want Preparing then Awaiting", got)
	}

	// A later request hands back the same paths and rewrites nothing.
	// Tampering with the file is how that is observed.
	if err := os.WriteFile(after[0], []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	again, ok := s.requestPaths()
	if !ok {
		t.Fatal("a repeat request was refused")
	}
	if len(again) != len(after) || again[0] != after[0] {
		t.Fatalf("a repeat request named %q, want %q", again, after)
	}
	if blob, err := os.ReadFile(after[0]); err != nil || string(blob) != "tampered" {
		t.Fatalf("the repeat request re-extracted the file (%q, %v); it must reuse what is there", blob, err)
	}
	s.mu.Lock()
	req, early := s.requests, s.early
	s.mu.Unlock()
	if req != 3 || early != 1 {
		t.Fatalf("requests=%d early=%d, want 3/1", req, early)
	}
	if got := ph.list(); len(got) != 2 {
		t.Fatalf("a repeat request reported another phase: %v", got)
	}
}

// TestStageFailedExtractionFailsGetData is the rule APP.md §3 states
// against 7-Zip's behaviour: a request that cannot be honoured fails rather
// than naming files that are not there, and stays failed.
func TestStageFailedExtractionFailsGetData(t *testing.T) {
	items := []Item{{Name: "half.bin", Size: 8}}
	boom := errors.New("the archive refused")
	calls := 0
	s, ph, lg := newTestStage(t, items, func(ctx context.Context, dir string) error {
		calls++
		os.WriteFile(filepath.Join(dir, "half.bin"), []byte("half"), 0o600)
		return boom
	})
	s.arm(false)

	paths, ok := s.requestPaths()
	if ok || paths != nil {
		t.Fatalf("a failed extraction still named %q", paths)
	}
	// And it stays failed: a second request must not quietly hand out the
	// names of the half-written files, nor run the extraction again.
	if _, ok := s.requestPaths(); ok {
		t.Fatal("the second request after a failure was honoured")
	}
	if calls != 1 {
		t.Fatalf("the extraction ran %d times", calls)
	}
	s.mu.Lock()
	failed, handed, written := s.failed, s.handedOut, s.written
	s.mu.Unlock()
	if !failed || !written {
		t.Error("the stage does not remember that the extraction was begun and failed")
	}
	if handed {
		t.Error("a stage whose extraction failed must not count as having handed anything out")
	}
	if got := ph.list(); len(got) != 2 || got[0].Step != Preparing || got[1].Step != Done || got[1].Reason != Failed {
		t.Fatalf("phases %v, want Preparing then Done/Failed", got)
	}
	if !strings.Contains(lg.text(), "GetData fails rather than name files that are not there") {
		t.Errorf("the log does not say why the request failed:\n%s", lg.text())
	}
	// Once the drag is over, the half-written files go at once: nothing
	// valid was handed out, so nothing is waiting for them.
	endDrag(s, 0)
	if !s.poll(time.Now()) {
		t.Fatal("the poll did not resolve a failed drag")
	}
	if _, err := os.Stat(s.root); !os.IsNotExist(err) {
		t.Errorf("the folder of a failed extraction survived: %v", err)
	}
}

// A cancelled extraction — Cancel from outside, or the operation's own
// cancel through the context — is Cancelled, not Failed, and fails the
// request the same way.
func TestStageCancelledExtractionIsCancelled(t *testing.T) {
	items := []Item{{Name: "c.bin", Size: 8}}
	s, ph, _ := newTestStage(t, items, func(ctx context.Context, dir string) error {
		<-ctx.Done()
		return ctx.Err()
	})
	s.arm(false)
	go func() {
		time.Sleep(20 * time.Millisecond)
		s.cancel()
	}()
	if _, ok := s.requestPaths(); ok {
		t.Fatal("a cancelled extraction was honoured")
	}
	if got := ph.list(); len(got) != 2 || got[1].Step != Done || got[1].Reason != Cancelled {
		t.Fatalf("phases %v, want Preparing then Done/Cancelled", got)
	}
}

// A self-drop hands the paths out to the WebView's own drop and extracts
// nothing; the folder goes as soon as DoDragDrop returns.
func TestStageSelfDropExtractsNothing(t *testing.T) {
	isolateStages(t)
	items := []Item{{Name: "s.bin", Size: 8}}
	calls := 0
	s, ph, _ := newTestStage(t, items, func(ctx context.Context, dir string) error {
		calls++
		return nil
	})
	if _, ok := s.requestPaths(); !ok {
		t.Fatal("the hover request was refused")
	}
	s.arm(true)
	paths, ok := s.requestPaths()
	if !ok || len(paths) != 1 {
		t.Fatalf("the self-drop's request answered %q %v", paths, ok)
	}
	if calls != 0 {
		t.Fatal("a self-drop ran the extraction")
	}
	s.noteDragEnded(0)
	if got := ph.list(); len(got) != 1 || got[0].Step != Done || got[0].Reason != SelfDrop {
		t.Fatalf("phases %v, want Done/SelfDrop alone", got)
	}
	if _, err := os.Stat(s.root); !os.IsNotExist(err) {
		t.Errorf("a self-drop's folder survived DoDragDrop's return: %v", err)
	}
	if stageIsActive(s.root) {
		t.Error("a self-drop's stage is still registered")
	}
}

// Escape, and a DoDragDrop that could not run, are Done at DoDragDrop's
// return and the empty folder goes at the watch's first tick — not when
// whatever the cursor hovered over lets the data object go (APP.md §3: at
// once when the drag ends with nothing handed out). Only an accepted drop
// waits for the release, since a target that negotiated the asynchronous
// protocol asks after DoDragDrop has returned; TestStageRefusedDrop is that
// one. A request that would extract after the folder went is refused
// rather than write into a folder nothing manifests any more.
func TestStageCancelledDragGoesAtOnce(t *testing.T) {
	for _, end := range []Reason{Cancelled, Failed} {
		t.Run(end.String(), func(t *testing.T) {
			isolateStages(t)
			s, ph, lg := newTestStage(t, []Item{{Name: "e.bin"}}, nil)
			s.requestPaths() // a hover request: the paths were handed out
			endDrag(s, end)
			if got := ph.list(); len(got) != 1 || got[0].Step != Done || got[0].Reason != end {
				t.Fatalf("phases %v, want Done/%s", got, end)
			}
			if !s.poll(time.Now()) {
				t.Fatal("the first poll left the folder for the target to let go of")
			}
			if _, err := os.Stat(s.root); !os.IsNotExist(err) {
				t.Errorf("the folder of a drag that ended %s survived: %v", end, err)
			}
			if !strings.Contains(lg.text(), "ended without a drop") {
				t.Errorf("the log does not say why the folder went:\n%s", lg.text())
			}
			s.arm(false)
			if paths, ok := s.requestPaths(); ok {
				t.Fatalf("a request after the folder went was honoured with %q", paths)
			}
			if _, err := os.Stat(s.root); !os.IsNotExist(err) {
				t.Errorf("the refused request brought the folder back: %v", err)
			}
		})
	}
}

// TestStageLayoutKeepsTheManifestClearOfTheItems: a record may be called
// manifest.json, so the items live a level below the manifest (APP.md §3)
// and the drag's own bookkeeping is never what a dragged name overwrites —
// nor what a delete of the items takes.
func TestStageLayoutKeepsTheManifestClearOfTheItems(t *testing.T) {
	isolateStages(t)
	items := []Item{{Name: manifestName, Size: 9}, {Name: "items", IsDir: true}, {Name: `items\` + manifestName, Size: 9}}
	s, ph, _ := newTestStage(t, items, nil)
	if _, ok := s.requestPaths(); !ok {
		t.Fatal("the hover request was refused")
	}
	s.arm(false)
	paths, ok := s.requestPaths()
	if !ok {
		t.Fatal("the extraction was refused")
	}
	for _, p := range paths {
		if filepath.Dir(p) != s.itemsDir {
			t.Errorf("CF_HDROP names %q, want a path directly under the items folder", p)
		}
	}
	// The record's bytes are at its path; the manifest is still ours and
	// still says what the drag said.
	if blob, err := os.ReadFile(s.paths[0]); err != nil || string(blob) != manifestName {
		t.Fatalf("the record called manifest.json holds %q, %v", blob, err)
	}
	m, ok := ReadManifest(s.root)
	if !ok || m.State != StateHandedOut || len(m.Files) != 3 || m.Files[0] != filepath.Join(itemsDirName, manifestName) {
		t.Fatalf("after the extraction the manifest is %+v %v", m, ok)
	}
	// The target moves everything away: the drag ends Moved and the folder,
	// manifest and all, goes.
	for _, p := range s.drop {
		if err := os.RemoveAll(p); err != nil {
			t.Fatal(err)
		}
	}
	endDrag(s, 0)
	if !s.poll(time.Now()) {
		t.Fatal("the poll did not finish a completed move")
	}
	if got := ph.list(); len(got) != 3 || got[2].Reason != Moved {
		t.Fatalf("phases %v, want Done/Moved last", got)
	}
	if _, err := os.Stat(s.root); !os.IsNotExist(err) {
		t.Errorf("the folder survived a completed move: %v", err)
	}
}

// TestDirectoryOnlyMoveEndsTheDrag: a selection that is only folders — an
// empty one, a tree of empty ones — has no file for the watch to see
// leaving, so the folders themselves are watched, and their going is the
// move (APP.md §3: the staged items, files and folders alike, gone).
func TestDirectoryOnlyMoveEndsTheDrag(t *testing.T) {
	isolateStages(t)
	items := []Item{{Name: "Empty", IsDir: true}, {Name: "Tree", IsDir: true}, {Name: `Tree\inner`, IsDir: true}}
	s, ph, lg := newTestStage(t, items, nil)
	s.arm(false)
	if _, ok := s.requestPaths(); !ok {
		t.Fatal("the extraction was refused")
	}
	endDrag(s, 0)
	if s.poll(time.Now()) {
		t.Fatal("the poll resolved a drag whose folders are all still there")
	}
	if got := ph.list(); len(got) != 2 || got[1].Step != Awaiting {
		t.Fatalf("phases %v, want Preparing then Awaiting", got)
	}
	// The target renames the two top-level folders away.
	for _, p := range s.drop {
		if err := os.Rename(p, filepath.Join(t.TempDir(), filepath.Base(p))); err != nil {
			t.Fatalf("simulating the target's move: %v", err)
		}
	}
	if !s.poll(time.Now()) {
		t.Fatal("the poll did not finish the drag once every folder was gone")
	}
	if got := ph.list(); len(got) != 3 || got[2].Step != Done || got[2].Reason != Moved {
		t.Fatalf("phases %v, want Done/Moved last", got)
	}
	if !strings.Contains(lg.text(), "staged folder 0 gone (moved away by the target)") {
		t.Errorf("the log never said the folder was moved:\n%s", lg.text())
	}
	if strings.Contains(lg.text(), "Empty") || strings.Contains(lg.text(), "Tree") {
		t.Errorf("the log names a folder:\n%s", lg.text())
	}
	if _, err := os.Stat(s.root); !os.IsNotExist(err) {
		t.Errorf("the staging folder survived a completed move: %v", err)
	}
}

// TestReleasedWithoutAReadEndsAsIdle is the fourth way the awaiting phase
// ends (APP.md §3): the target let the data object go and five seconds
// passed with no read. The folder stays for a consumer that keeps the paths
// for a later read; the strip does not.
func TestReleasedWithoutAReadEndsAsIdle(t *testing.T) {
	isolateStages(t)
	s, ph, _ := newTestStage(t, []Item{{Name: "kept.bin", Size: 16}}, nil)
	s.arm(false)
	if _, ok := s.requestPaths(); !ok {
		t.Fatal("the extraction was refused")
	}
	endDrag(s, 0)
	now := time.Now()
	if s.poll(now) {
		t.Fatal("the poll resolved the stage while the target still held the object")
	}
	// Explorer lets go — its own references reach zero without an
	// EndOperation — and nothing reads the file.
	s.noteReleased()
	s.mu.Lock()
	s.lastTouch = now
	s.mu.Unlock()
	if s.poll(now.Add(idleAfter - time.Second)) {
		t.Fatal("the poll resolved the stage four seconds after the release")
	}
	if len(ph.list()) != 2 {
		t.Fatalf("phases %v before the five seconds", ph.list())
	}
	if !s.poll(now.Add(idleAfter)) {
		t.Fatal("the poll did not end the awaiting phase five seconds after the release")
	}
	if got := ph.list(); len(got) != 3 || got[2].Reason != Idle {
		t.Fatalf("phases %v, want Done/Idle last", got)
	}
	if _, err := os.Stat(s.paths[0]); err != nil {
		t.Fatalf("the file was deleted under a consumer that may still read it: %v", err)
	}
	if m, ok := ReadManifest(s.root); !ok || m.State != StateHandedOut {
		t.Fatalf("the folder is manifested %q %v, want %q for the scavenge", m.State, ok, StateHandedOut)
	}
}

// A drop the target took and then let go of without ever asking for the
// files is Refused, and the empty folder goes.
func TestStageRefusedDrop(t *testing.T) {
	isolateStages(t)
	s, ph, _ := newTestStage(t, []Item{{Name: "r.bin"}}, nil)
	s.requestPaths()
	s.arm(false)
	endDrag(s, 0)
	s.poll(time.Now())
	if len(ph.list()) != 0 {
		t.Fatalf("phases %v before the target let go", ph.list())
	}
	s.noteReleased()
	if !s.poll(time.Now()) {
		t.Fatal("the poll did not resolve the refused drop")
	}
	if got := ph.list(); len(got) != 1 || got[0].Step != Done || got[0].Reason != Refused {
		t.Fatalf("phases %v, want Done/Refused", got)
	}
	if _, err := os.Stat(s.root); !os.IsNotExist(err) {
		t.Errorf("the folder of a refused drop survived: %v", err)
	}
}

// ---------------------------------------------------------------------------
// The manifest.

func TestManifestLifecycle(t *testing.T) {
	s, _, _ := newTestStage(t, []Item{{Name: "m.bin", Size: 256}}, nil)

	m, ok := ReadManifest(s.root)
	if !ok {
		t.Fatal("a new staging folder has no manifest of ours")
	}
	if m.Tool != manifestTool || m.Version != manifestVersion {
		t.Errorf("manifest tool/version = %q/%d", m.Tool, m.Version)
	}
	if m.State != StateLive {
		t.Errorf("a new staging folder is in state %q, want %q", m.State, StateLive)
	}
	if m.PID != os.Getpid() {
		t.Errorf("manifest pid = %d, want %d", m.PID, os.Getpid())
	}
	// The files are named from the folder's top, where the manifest is: a
	// level down, under items.
	if len(m.Files) != 1 || m.Files[0] != filepath.Join(itemsDirName, "m.bin") {
		t.Errorf("manifest files = %q", m.Files)
	}
	if m.Created.IsZero() {
		t.Error("the manifest has no creation time, which is what the scavenge ages")
	}
	// A foreign folder is not ours, whatever it contains.
	other := t.TempDir()
	blob, _ := json.Marshal(map[string]any{"tool": "somebody-else", "state": "done"})
	if err := os.WriteFile(filepath.Join(other, manifestName), blob, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadManifest(other); ok {
		t.Fatal("a manifest written by another tool was accepted as ours")
	}
	if err := os.WriteFile(filepath.Join(other, manifestName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadManifest(other); ok {
		t.Fatal("an unparsable manifest was accepted as ours")
	}
	// The deletion is written down before it is attempted, so a delete that
	// fails leaves a folder the sweep will take.
	s.setState(StateDone)
	if m, _ := ReadManifest(s.root); m.State != StateDone {
		t.Fatalf("setState wrote %q", m.State)
	}
}

// ---------------------------------------------------------------------------
// The decision tables.

func TestCleanupDecisionTable(t *testing.T) {
	const hour = time.Hour
	cases := []struct {
		name string
		ev   stageEvents
		want stageAction
	}{
		{"a self-drop goes the moment the drag is over",
			stageEvents{selfDrop: true, dragOver: true, handedOut: true, maxAge: hour}, stageDelete},
		{"a failed extraction goes at once, half-written files and all",
			stageEvents{dragOver: true, written: true, extractFailed: true, handedOut: true, anyLeft: true, maxAge: hour}, stageDelete},
		{"Escape: no drop happened, and the empty folder goes at once",
			stageEvents{dragOver: true, dragEnd: Cancelled, maxAge: hour}, stageDelete},
		{"... whatever the hover target still holds",
			stageEvents{dragOver: true, dragEnd: Cancelled, handedOut: true, maxAge: hour}, stageDelete},
		{"a DoDragDrop that could not run: the same",
			stageEvents{dragOver: true, dragEnd: Failed, handedOut: true, maxAge: hour}, stageDelete},
		{"a drop taken, nothing written and the target let go: the folder goes",
			stageEvents{dragOver: true, released: true, maxAge: hour}, stageDelete},
		{"... including one where a hover request took the paths",
			stageEvents{dragOver: true, handedOut: true, released: true, maxAge: hour}, stageDelete},
		{"a drop taken, nothing written and the target still holds the object: it may ask on a thread of its own",
			stageEvents{dragOver: true, handedOut: true, maxAge: hour}, stageWait},
		{"a same-volume move took every file: nothing is left to keep",
			stageEvents{dragOver: true, written: true, extracted: true, handedOut: true, anyLeft: false, maxAge: hour}, stageDelete},
		{"EndOperation on a negotiated transfer ends it",
			stageEvents{dragOver: true, written: true, extracted: true, handedOut: true, anyLeft: true, asyncOp: true, endOperation: true, maxAge: hour}, stageDelete},
		{"EndOperation without a negotiated operation is not a signal at all",
			stageEvents{dragOver: true, written: true, extracted: true, handedOut: true, anyLeft: true, endOperation: true, maxAge: hour}, stageWait},
		{"a file still open holds everything up",
			stageEvents{dragOver: true, written: true, extracted: true, handedOut: true, anyLeft: true, anyInUse: true, endOperation: true, asyncOp: true, maxAge: hour}, stageWait},
		{"paths handed out and no EndOperation: the folder outlives the drop",
			stageEvents{dragOver: true, written: true, extracted: true, handedOut: true, anyLeft: true, age: 5 * time.Minute, maxAge: hour}, stageWait},
		{"... until it is past the scavenge age",
			stageEvents{dragOver: true, written: true, extracted: true, handedOut: true, anyLeft: true, age: 2 * hour, maxAge: hour}, stageDelete},
		{"... or the awaiting phase is over: left for the scavenge, and the watch stops",
			stageEvents{dragOver: true, written: true, extracted: true, handedOut: true, anyLeft: true, phaseDone: true, maxAge: hour}, stageLeave},
		{"during the hover, nothing is decided",
			stageEvents{maxAge: hour}, stageWait},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := decideCleanup(tc.ev)
			if d.action != tc.want {
				t.Fatalf("decided %q (%s), want %q", d.action, d.why, tc.want)
			}
			if d.why == "" {
				t.Error("a decision with no reason: the log would say nothing")
			}
		})
	}
}

// TestAwaitingEndTable is what clears the strip's second phase, the four
// ways APP.md §3 names: the items gone, the target's EndOperation, the files
// read and then left alone for five seconds, or the target gone and five
// seconds passed with no read.
func TestAwaitingEndTable(t *testing.T) {
	staged := stageEvents{dragOver: true, written: true, extracted: true, handedOut: true, anyLeft: true}
	with := func(f func(e *stageEvents)) stageEvents {
		e := staged
		f(&e)
		return e
	}
	cases := []struct {
		name string
		ev   stageEvents
		want Reason
	}{
		{"nothing before the drag is over", with(func(e *stageEvents) { e.dragOver = false }), 0},
		{"nothing twice", with(func(e *stageEvents) { e.phaseDone = true }), 0},
		{"a refused drop: nothing written and the target let go", stageEvents{dragOver: true, released: true}, Refused},
		{"nothing written and the target still there", stageEvents{dragOver: true}, 0},
		{"a cancel already said so", stageEvents{dragOver: true, released: true, dragEnd: Cancelled}, 0},
		{"the extraction still running", with(func(e *stageEvents) { e.extracted = false }), 0},
		{"a failed extraction was reported when it failed", with(func(e *stageEvents) { e.extractFailed = true }), 0},
		{"every file gone is a move", with(func(e *stageEvents) { e.anyLeft = false }), Moved},
		{"EndOperation on a negotiated transfer", with(func(e *stageEvents) { e.asyncOp, e.endOperation = true, true }), Ended},
		{"EndOperation with a file still open waits", with(func(e *stageEvents) { e.asyncOp, e.endOperation, e.anyInUse = true, true, true }), 0},
		{"a file in use: not yet", with(func(e *stageEvents) { e.anyInUse, e.anyEverUsed, e.untouchedFor = true, true, time.Minute }), 0},
		{"read and left alone for five seconds", with(func(e *stageEvents) { e.anyEverUsed, e.untouchedFor = true, idleAfter }), Idle},
		{"read and left alone for four", with(func(e *stageEvents) { e.anyEverUsed, e.untouchedFor = true, idleAfter-time.Second }), 0},
		{"never read, the target still holding on: wait", with(func(e *stageEvents) { e.untouchedFor = time.Minute }), 0},
		{"never read, the target gone five seconds ago: idle", with(func(e *stageEvents) { e.released, e.untouchedFor = true, idleAfter }), Idle},
		{"never read, the target gone four seconds ago: not yet", with(func(e *stageEvents) { e.released, e.untouchedFor = true, idleAfter-time.Second }), 0},
		{"the target gone, and a read still going: wait", with(func(e *stageEvents) {
			e.released, e.anyInUse, e.anyEverUsed, e.untouchedFor = true, true, true, time.Minute
		}), 0},
		{"the target gone, a read since it ended five seconds ago: idle", with(func(e *stageEvents) { e.released, e.anyEverUsed, e.untouchedFor = true, true, idleAfter }), Idle},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := awaitingEnd(tc.ev); got != tc.want {
				t.Fatalf("awaitingEnd = %v, want %v", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The scavenge.

func TestScavengeVerdict(t *testing.T) {
	const hour = time.Hour
	cases := []struct {
		name string
		f    scavengeFacts
		want bool
	}{
		{"a folder with no manifest of ours is never touched",
			scavengeFacts{age: 10 * hour, maxAge: hour}, false},
		{"a reparse point is never followed and never removed",
			scavengeFacts{reparse: true, haveManifest: true, state: StateDone, age: 10 * hour, maxAge: hour}, false},
		{"a live drag is never swept",
			scavengeFacts{haveManifest: true, state: StateLive, age: 10 * hour, maxAge: hour}, false},
		{"a drag this process still watches is left to it",
			scavengeFacts{haveManifest: true, state: StateHandedOut, active: true, age: 10 * hour, maxAge: hour}, false},
		{"younger than the limit",
			scavengeFacts{haveManifest: true, state: StateHandedOut, age: 59 * time.Minute, maxAge: hour}, false},
		{"exactly the limit is not past it",
			scavengeFacts{haveManifest: true, state: StateHandedOut, age: hour, maxAge: hour}, false},
		{"handed out and past the limit",
			scavengeFacts{haveManifest: true, state: StateHandedOut, age: hour + time.Second, maxAge: hour}, true},
		{"a folder whose deletion was decided and failed",
			scavengeFacts{haveManifest: true, state: StateDone, age: 2 * hour, maxAge: hour}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, why := scavengeVerdict(tc.f)
			if got != tc.want {
				t.Fatalf("verdict = %v (%s), want %v", got, why, tc.want)
			}
			if why == "" {
				t.Error("a verdict with no reason")
			}
		})
	}
}

// TestScavengeBackoffIsBounded: 1 s, 10 s, 60 s and then the folder is left
// for the next sweep. An unbounded retry would be a goroutine spinning on a
// file some consumer means to keep open for an hour.
func TestScavengeBackoffIsBounded(t *testing.T) {
	want := []time.Duration{time.Second, 10 * time.Second, time.Minute}
	for i, w := range want {
		got, more := scavengeBackoff(i)
		if !more || got != w {
			t.Fatalf("scavengeBackoff(%d) = %s,%v; want %s,true", i, got, more, w)
		}
	}
	if _, more := scavengeBackoff(len(want)); more {
		t.Fatalf("scavengeBackoff(%d) still wants to retry", len(want))
	}
}

// TestScavengeTouchesOnlyOurFolders builds a drag root by hand and sweeps
// it. Everything is under t.TempDir(); nothing here goes near %LOCALAPPDATA%.
func TestScavengeTouchesOnlyOurFolders(t *testing.T) {
	isolateStages(t)
	root := t.TempDir()
	now := time.Now()
	mk := func(name string, m *Manifest) string {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "payload.bin"), []byte("plaintext"), 0o600); err != nil {
			t.Fatal(err)
		}
		if m != nil {
			if err := WriteManifest(dir, *m); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	old := mk("aaaaaaaa", &Manifest{Tool: manifestTool, Version: manifestVersion, State: StateHandedOut, Created: now.Add(-2 * time.Hour)})
	done := mk("bbbbbbbb", &Manifest{Tool: manifestTool, Version: manifestVersion, State: StateDone, Created: now.Add(-90 * time.Minute)})
	live := mk("cccccccc", &Manifest{Tool: manifestTool, Version: manifestVersion, State: StateLive, Created: now.Add(-3 * time.Hour)})
	young := mk("dddddddd", &Manifest{Tool: manifestTool, Version: manifestVersion, State: StateHandedOut, Created: now.Add(-5 * time.Minute)})
	foreign := mk("somebody-elses-folder", nil)
	other := mk("eeeeeeee", &Manifest{Tool: "some-other-tool", State: StateDone, Created: now.Add(-10 * time.Hour)})
	stray := filepath.Join(root, "loose.txt")
	if err := os.WriteFile(stray, []byte("not a staging folder"), 0o600); err != nil {
		t.Fatal(err)
	}

	lg := &testLog{}
	removed, left := scavenge(root, time.Hour, now, lg.printf)
	if removed != 2 {
		t.Fatalf("the sweep removed %d, want 2", removed)
	}
	if left != 5 {
		t.Errorf("the sweep left %d entries, want 5", left)
	}
	for _, gone := range []string{old, done} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s survived the sweep", filepath.Base(gone))
		}
	}
	for _, stays := range []string{live, young, foreign, other, stray} {
		if _, err := os.Stat(stays); err != nil {
			t.Errorf("the sweep removed %s, which is not its to remove: %v", filepath.Base(stays), err)
		}
	}
	// A root that is not there is nothing to sweep, and not an error.
	if r, l := scavenge(filepath.Join(root, "missing"), time.Hour, now, lg.printf); r != 0 || l != 0 {
		t.Fatalf("a missing root swept %d/%d", r, l)
	}
	// The log names folders by their id and never a file.
	if strings.Contains(lg.text(), "payload.bin") {
		t.Errorf("the scavenge logged a file name:\n%s", lg.text())
	}
}

// TestScavengeLeavesALiveStageAlone: the ten-minute sweep and the drag
// running in this process must not race for the same folder.
func TestScavengeLeavesALiveStageAlone(t *testing.T) {
	isolateStages(t)
	parent := t.TempDir()
	s, err := newStage(Options{Root: parent, Items: []Item{{Name: "x.bin"}}, Extract: writeItems(nil)}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer s.finish()
	// Old enough and no longer "live" as far as the manifest goes — only the
	// live-stage registry protects it now.
	s.setState(StateHandedOut)
	m, _ := ReadManifest(s.root)
	m.Created = time.Now().Add(-10 * time.Hour)
	if err := WriteManifest(s.root, m); err != nil {
		t.Fatal(err)
	}
	if removed, _ := Scavenge(parent, time.Hour, nil); removed != 0 {
		t.Fatalf("the sweep removed %d folder(s) this process is still watching", removed)
	}
	if _, err := os.Stat(s.root); err != nil {
		t.Fatalf("the live stage's folder is gone: %v", err)
	}
	// Resolved, it is the sweep's.
	s.finish()
	if removed, _ := Scavenge(parent, time.Hour, nil); removed != 1 {
		t.Fatalf("the sweep removed %d folder(s) after the stage let go, want 1", removed)
	}
}

// ---------------------------------------------------------------------------
// Deleting a tree.

func TestRemoveTreeNoReparse(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "stage")
	sub := filepath.Join(dir, "Docs")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(dir, "a.bin"), filepath.Join(sub, "b.bin")} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := removeTreeNoReparse(dir); err != nil {
		t.Fatalf("removeTreeNoReparse: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("the tree survived: %v", err)
	}
	// A second call on nothing is not an error: the sweep and the drag's own
	// cleanup can both decide to remove the same folder.
	if err := removeTreeNoReparse(dir); err != nil {
		t.Fatalf("removing a folder that is already gone: %v", err)
	}
}

// TestRemoveTreeDoesNotFollowALink is the rule that matters if anything ever
// puts a junction under a staging folder. Creating a directory symlink needs
// a privilege an ordinary test process does not have, so this skips rather
// than fails when it cannot be set up.
func TestRemoveTreeDoesNotFollowALink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "precious")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(target, "vault.dat")
	if err := os.WriteFile(keep, []byte("do not delete"), 0o600); err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(root, "stage")
	if err := os.MkdirAll(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(stage, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create a directory symlink here (it needs a privilege): %v", err)
	}
	if !isReparsePoint(link) {
		t.Fatal("the symlink is not reported as a reparse point")
	}
	if err := removeTreeNoReparse(stage); err != nil {
		t.Fatalf("removeTreeNoReparse: %v", err)
	}
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Errorf("the staging folder survived: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("the delete followed the link and took the target with it: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Watching the staged files: the transitions, the first tick included.

func TestWatchedFileTransitions(t *testing.T) {
	now := time.Now()
	was := now.Add(-2 * time.Second)
	cases := []struct {
		name   string
		start  stagedFile
		probe  fileProbe
		staged bool
		// the four facts the cleanup policy reads back out
		seen, gone, inUse, everUsed bool
		line                        string // a substring the transition must log; "" means silence
	}{
		{name: "missing at the first tick, and the extraction had written it: a move that beat the watch",
			start: stagedFile{path: `C:\stage\a.bin`, since: was}, probe: fileProbe{}, staged: true,
			seen: true, gone: true, line: "gone before the first look"},
		{name: "missing at the first tick with nothing ever written: not a removal",
			start: stagedFile{path: `C:\stage\a.bin`, since: was}, probe: fileProbe{}, staged: false, line: ""},
		{name: "present at the first tick: the ordinary start",
			start: stagedFile{path: `C:\stage\a.bin`, since: was}, probe: fileProbe{exists: true}, staged: true, seen: true, line: ""},
		{name: "present and already being read at the first tick",
			start: stagedFile{path: `C:\stage\a.bin`, since: was}, probe: fileProbe{exists: true, inUse: true}, staged: true,
			seen: true, inUse: true, everUsed: true, line: ""},
		{name: "a file that was there and is not any more was moved",
			start: stagedFile{path: `C:\stage\a.bin`, seen: true, exists: true, since: was}, probe: fileProbe{}, staged: true,
			seen: true, gone: true, line: "gone (moved away by the target)"},
		{name: "a target opened it",
			start: stagedFile{path: `C:\stage\a.bin`, seen: true, exists: true, since: was}, probe: fileProbe{exists: true, inUse: true}, staged: true,
			seen: true, inUse: true, everUsed: true, line: "in use by another process"},
		{name: "and let go of it again",
			start: stagedFile{path: `C:\stage\a.bin`, seen: true, exists: true, inUse: true, everUsed: true, since: was}, probe: fileProbe{exists: true}, staged: true,
			seen: true, everUsed: true, line: "free"},
		{name: "gone and still gone is not said twice",
			start: stagedFile{path: `C:\stage\a.bin`, seen: true, gone: true, since: was}, probe: fileProbe{}, staged: true,
			seen: true, gone: true, line: ""},
		{name: "a file that comes back is said out loud",
			start: stagedFile{path: `C:\stage\a.bin`, seen: true, gone: true, since: was}, probe: fileProbe{exists: true}, staged: true,
			seen: true, line: "is back"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := tc.start
			line := w.observe(tc.probe, tc.staged, now, 0)
			if w.seen != tc.seen || w.gone != tc.gone || w.inUse != tc.inUse || w.everUsed != tc.everUsed {
				t.Fatalf("seen=%v gone=%v inUse=%v everUsed=%v; want %v/%v/%v/%v",
					w.seen, w.gone, w.inUse, w.everUsed, tc.seen, tc.gone, tc.inUse, tc.everUsed)
			}
			if tc.line == "" {
				if line != "" {
					t.Fatalf("the transition logged %q; this one has nothing to say", line)
				}
				return
			}
			if !strings.Contains(line, tc.line) {
				t.Fatalf("the transition logged %q, want it to contain %q", line, tc.line)
			}
			// Never a file name: the log names files by their index.
			if strings.Contains(line, "a.bin") {
				t.Fatalf("the transition line names the file: %q", line)
			}
		})
	}
}

// TestMoveBeforeTheFirstTickEndsTheDrag: the target renames the staged file
// away before the watch's first tick, and the stage has to recognise the
// completed move — the cleanest end a drag has — rather than sit in
// "handed-out" over an empty folder until the scavenge.
func TestMoveBeforeTheFirstTickEndsTheDrag(t *testing.T) {
	isolateStages(t)
	s, ph, lg := newTestStage(t, []Item{{Name: "moved.bin", Size: 4096}}, nil)
	s.arm(false)
	if _, ok := s.requestPaths(); !ok {
		t.Fatal("the extraction was refused")
	}
	// The drop: a same-volume move, which from here is a rename away. It
	// happens before the watch has looked even once.
	if err := os.Rename(s.paths[0], filepath.Join(t.TempDir(), "moved.bin")); err != nil {
		t.Fatalf("simulating the target's move: %v", err)
	}
	endDrag(s, 0)
	s.mu.Lock()
	watching := s.watching
	s.mu.Unlock()
	if !watching {
		t.Fatal("DoDragDrop's return did not start the watch")
	}
	if !s.poll(time.Now()) {
		t.Fatal("the first poll did not finish the stage; a completed move is the end of a drag")
	}
	if !strings.Contains(lg.text(), "gone before the first look") || !strings.Contains(lg.text(), "moved away by the target") {
		t.Errorf("the log never said the move beat the first tick:\n%s", lg.text())
	}
	if got := ph.list(); len(got) != 3 || got[2].Step != Done || got[2].Reason != Moved {
		t.Fatalf("phases %v, want Preparing, Awaiting, Done/Moved", got)
	}
	s.mu.Lock()
	state, removed := s.state, s.removed
	s.mu.Unlock()
	if state != StateDone || !removed {
		t.Errorf("state %q removed %v after a completed move", state, removed)
	}
	if _, err := os.Stat(s.root); !os.IsNotExist(err) {
		t.Errorf("the staging folder survived a completed move: %v", err)
	}
}

// TestIdleLeavesTheFolderForTheScavenge: files read and then left alone end
// the awaiting phase as Idle, the folder stays manifested handed-out, and
// the watch stops — the paths are a consumer's now.
func TestIdleLeavesTheFolderForTheScavenge(t *testing.T) {
	isolateStages(t)
	s, ph, _ := newTestStage(t, []Item{{Name: "idle.bin", Size: 16}}, nil)
	s.arm(false)
	if _, ok := s.requestPaths(); !ok {
		t.Fatal("the extraction was refused")
	}
	endDrag(s, 0)
	// Somebody read it: the watch saw it in use, then free.
	s.mu.Lock()
	s.watch[0].seen, s.watch[0].exists, s.watch[0].inUse, s.watch[0].everUsed = true, true, true, true
	s.looked = true
	s.mu.Unlock()
	now := time.Now()
	if s.poll(now) {
		t.Fatal("the poll resolved the stage while the file had only just been let go of")
	}
	if len(ph.list()) != 2 {
		t.Fatalf("phases %v before the five seconds", ph.list())
	}
	if !s.poll(now.Add(idleAfter + time.Second)) {
		t.Fatal("the poll did not stop the watch once the files were idle")
	}
	if got := ph.list(); len(got) != 3 || got[2].Reason != Idle {
		t.Fatalf("phases %v, want Done/Idle last", got)
	}
	if _, err := os.Stat(s.paths[0]); err != nil {
		t.Fatalf("an idle drag's file was deleted: %v", err)
	}
	if m, ok := ReadManifest(s.root); !ok || m.State != StateHandedOut {
		t.Fatalf("an idle drag's folder is manifested %q %v, want %q for the scavenge", m.State, ok, StateHandedOut)
	}
	if stageIsActive(s.root) {
		t.Error("an idle drag's stage is still registered, so the scavenge would never take it")
	}
}

// TestCloseQuietlyVisitsEveryStage is the close at exit: an earlier drag's
// folder is no less this run's to decide about than the last one's. The
// two decisions differ, and both have to be taken.
func TestCloseQuietlyVisitsEveryStage(t *testing.T) {
	isolateStages(t)
	parent := t.TempDir()
	mk := func(name string, handOut bool) *stage {
		t.Helper()
		items := []Item{{Name: name, Size: 8}}
		s, err := newStage(Options{Root: parent, Items: items, Extract: writeItems(items)}, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.finish)
		if handOut {
			s.arm(false)
			if _, ok := s.requestPaths(); !ok {
				t.Fatal("the extraction was refused")
			}
		}
		return s
	}
	handed := mk("handed.bin", true)
	untouched := mk("untouched.bin", false)
	closeAllStages()
	if _, err := os.Stat(handed.root); err != nil {
		t.Errorf("the close deleted a folder whose paths were handed out: %v", err)
	}
	if m, _ := ReadManifest(handed.root); m.State != StateHandedOut {
		t.Errorf("the folder left behind is manifested %q, want %q", m.State, StateHandedOut)
	}
	if _, err := os.Stat(untouched.root); !os.IsNotExist(err) {
		t.Errorf("the close left a folder nothing was ever handed out of: %v", err)
	}
	if len(remainingStages()) != 0 {
		t.Error("stages remain registered after the close")
	}
}

// The log never carries a file name (APP.md §3): a path error is logged as
// its operation and the error the system gave, and an error without a path
// is left as it is.
func TestLoggedErrorsCarryNoPath(t *testing.T) {
	inner := errors.New("the volume said no")
	err := sansPath(&os.PathError{Op: "remove", Path: filepath.Join("C:", "drag", "abcd1234", "secret.bin"), Err: inner})
	if s := err.Error(); strings.Contains(s, "secret.bin") || !strings.Contains(s, "remove") || !errors.Is(err, inner) {
		t.Fatalf("sansPath -> %q", s)
	}
	if plain := errors.New("plain"); sansPath(plain) != plain {
		t.Fatal("an error without a path was rewritten")
	}
}

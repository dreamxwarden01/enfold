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
	s, err := newStage(Options{Root: t.TempDir(), Items: items, Extract: extract, OnPhase: ph.on, Log: lg.printf})
	if err != nil {
		t.Fatalf("newStage: %v", err)
	}
	t.Cleanup(s.finish)
	return s, ph, lg
}

// dropOnto is the whole end of a drag over a target of the given window
// class: DoDragDrop returning with its effect, which under the synchronous
// contract is where the reason and the cleanup are both decided.
func dropOnto(s *stage, class string, end Reason, effect uint32) {
	s.mu.Lock()
	s.targetClass = class
	s.mu.Unlock()
	s.dragEnded(end, effect)
}

// The window classes the tests drop onto. Neither decides anything any
// more (APP.md §3, ruled 2026-09-11): both are here so that a test can
// prove the class is recorded, logged, and left out of every verdict.
const (
	explorerWindow = "CabinetWClass"
	otherWindow    = "Chrome_WidgetWin_1"
)

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
		if _, err := newStage(Options{Root: root, Items: bad, Extract: writeItems(bad)}); err == nil {
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
	s.arm(false, otherWindow)
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
	s.arm(false, otherWindow)

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
	dropOnto(s, otherWindow, 0, dropEffectNone)
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
	s.arm(false, otherWindow)
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
	s.arm(true, "")
	paths, ok := s.requestPaths()
	if !ok || len(paths) != 1 {
		t.Fatalf("the self-drop's request answered %q %v", paths, ok)
	}
	if calls != 0 {
		t.Fatal("a self-drop ran the extraction")
	}
	s.dragEnded(0, dropEffectNone)
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
// return and the empty folder goes with it — whatever was asked for during
// the hover, nothing was written, so there is no plaintext for anybody to
// read late (APP.md §3: at once when the drag ends with nothing written). A
// request that would extract after the folder went is refused rather than
// write into a folder nothing manifests any more.
func TestStageCancelledDragGoesAtOnce(t *testing.T) {
	for _, end := range []Reason{Cancelled, Failed} {
		t.Run(end.String(), func(t *testing.T) {
			isolateStages(t)
			s, ph, lg := newTestStage(t, []Item{{Name: "e.bin"}}, nil)
			s.requestPaths() // a hover request: the paths were handed out
			dropOnto(s, otherWindow, end, dropEffectNone)
			if got := ph.list(); len(got) != 1 || got[0].Step != Done || got[0].Reason != end {
				t.Fatalf("phases %v, want Done/%s", got, end)
			}
			if _, err := os.Stat(s.root); !os.IsNotExist(err) {
				t.Errorf("the folder of a drag that ended %s survived: %v", end, err)
			}
			if !strings.Contains(lg.text(), "the drag ended with nothing written") {
				t.Errorf("the log does not say why the folder went:\n%s", lg.text())
			}
			s.arm(false, otherWindow)
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
	s.arm(false, otherWindow)
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
	dropOnto(s, otherWindow, 0, dropEffectMove)
	if got := ph.list(); len(got) != 3 || got[2].Reason != Moved {
		t.Fatalf("phases %v, want Done/Moved last", got)
	}
	if _, err := os.Stat(s.root); !os.IsNotExist(err) {
		t.Errorf("the folder survived a completed move: %v", err)
	}
}

// TestDirectoryOnlyMoveEndsTheDrag: a selection that is only folders — an
// empty one, a tree of empty ones — has no file to be seen leaving, so the
// items CF_HDROP named are what "everything is gone" is asked of (APP.md
// §3: the staged items, files and folders alike, gone).
func TestDirectoryOnlyMoveEndsTheDrag(t *testing.T) {
	isolateStages(t)
	items := []Item{{Name: "Empty", IsDir: true}, {Name: "Tree", IsDir: true}, {Name: `Tree\inner`, IsDir: true}}
	s, ph, lg := newTestStage(t, items, nil)
	s.arm(false, otherWindow)
	if _, ok := s.requestPaths(); !ok {
		t.Fatal("the extraction was refused")
	}
	if got := ph.list(); len(got) != 2 || got[1].Step != Awaiting {
		t.Fatalf("phases %v, want Preparing then Awaiting", got)
	}
	// The target renames the two top-level folders away, inside its Drop,
	// and DoDragDrop comes back with DROPEFFECT_MOVE.
	for _, p := range s.drop {
		if err := os.Rename(p, filepath.Join(t.TempDir(), filepath.Base(p))); err != nil {
			t.Fatalf("simulating the target's move: %v", err)
		}
	}
	dropOnto(s, otherWindow, 0, dropEffectMove)
	if got := ph.list(); len(got) != 3 || got[2].Step != Done || got[2].Reason != Moved {
		t.Fatalf("phases %v, want Done/Moved last", got)
	}
	if strings.Contains(lg.text(), "Empty") || strings.Contains(lg.text(), "Tree") {
		t.Errorf("the log names a folder:\n%s", lg.text())
	}
	if _, err := os.Stat(s.root); !os.IsNotExist(err) {
		t.Errorf("the staging folder survived a completed move: %v", err)
	}
}

// TestAnotherTargetLeavesTheFolderForTheScavenge: a window that is not
// Explorer's took the copy, and a consumer may open the paths late (a
// browser's upload box) — so the folder stays manifested handed-out and the
// sweep is what takes it, an hour on (APP.md §3, ruled 2026-09-11).
func TestAnotherTargetLeavesTheFolderForTheScavenge(t *testing.T) {
	isolateStages(t)
	s, ph, lg := newTestStage(t, []Item{{Name: "kept.bin", Size: 16}}, nil)
	s.arm(false, otherWindow)
	if _, ok := s.requestPaths(); !ok {
		t.Fatal("the extraction was refused")
	}
	dropOnto(s, otherWindow, 0, dropEffectCopy)
	if got := ph.list(); len(got) != 3 || got[2].Step != Done || got[2].Reason != Copied {
		t.Fatalf("phases %v, want Preparing, Awaiting, Done/Copied", got)
	}
	if _, err := os.Stat(s.paths[0]); err != nil {
		t.Fatalf("the file was deleted under a consumer that may still read it: %v", err)
	}
	if m, ok := ReadManifest(s.root); !ok || m.State != StateHandedOut {
		t.Fatalf("the folder is manifested %q %v, want %q for the scavenge", m.State, ok, StateHandedOut)
	}
	if stageIsActive(s.root) {
		t.Error("the stage is still registered, so the scavenge would never take it")
	}
	if !strings.Contains(lg.text(), "left manifested for the scavenge") {
		t.Errorf("the log does not say the folder was left:\n%s", lg.text())
	}
}

// TestTheTargetsClassDecidesNothing is the ruling of 2026-09-11 evening
// (docs/research/drag-out.md, "The drag thread, measured"): a cross-volume
// drop onto an Explorer window had the staged file still open by another
// process 18.75 seconds after DoDragDrop returned, because the synchronous
// Drop hands the copy to Explorer's own engine and returns. So Explorer's
// own classes are now treated as any other window's — the reason is still
// read off the effect, and the folder is left manifested for the scavenge —
// and the class survives only in the log.
func TestTheTargetsClassDecidesNothing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		class  string
		effect uint32
		want   Reason
	}{
		{"a copy into a folder window", "CabinetWClass", dropEffectCopy, Copied},
		{"Skip in Explorer's conflict dialog", "CabinetWClass", dropEffectNone, Cancelled},
		{"a drop on the desktop", "Progman", dropEffectCopy, Copied},
		{"a drop on the desktop with Active Desktop on", "workerw", dropEffectCopy, Copied},
		{"somebody else's window", otherWindow, dropEffectCopy, Copied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateStages(t)
			s, ph, lg := newTestStage(t, []Item{{Name: "e.bin", Size: 16}}, nil)
			s.arm(false, tc.class)
			if _, ok := s.requestPaths(); !ok {
				t.Fatal("the extraction was refused")
			}
			s.dragEnded(0, tc.effect)
			if got := ph.list(); len(got) != 3 || got[2].Step != Done || got[2].Reason != tc.want {
				t.Fatalf("phases %v, want Done/%s last", got, tc.want)
			}
			if _, err := os.Stat(s.paths[0]); err != nil {
				t.Fatalf("the file was deleted under a target that may still be reading it: %v", err)
			}
			if m, ok := ReadManifest(s.root); !ok || m.State != StateHandedOut {
				t.Fatalf("the folder is manifested %q %v, want %q for the scavenge", m.State, ok, StateHandedOut)
			}
			if !strings.Contains(lg.text(), tc.class) {
				t.Errorf("the log does not say what class the button came up over:\n%s", lg.text())
			}
		})
	}
}

// A drop the target took without ever asking for the files is Refused, and
// the empty folder goes: nothing was written, so there is no plaintext and
// nothing for a late reader to open.
func TestStageRefusedDrop(t *testing.T) {
	isolateStages(t)
	s, ph, _ := newTestStage(t, []Item{{Name: "r.bin"}}, nil)
	s.requestPaths() // a hover request: the names, over an empty folder
	s.arm(false, otherWindow)
	dropOnto(s, otherWindow, 0, dropEffectNone)
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
// The decision tables. Both are pure functions over what DoDragDrop's
// return said, which is everything there is to know: the drop being
// synchronous, the target had to finish inside Drop (APP.md §3, ruled
// 2026-09-11).

// TestDropReasonTable is how a drag ends, in the six words APP.md §3 names.
func TestDropReasonTable(t *testing.T) {
	// A drop that ran the extraction and left the staged items where they
	// are — a copy, or a target that did nothing.
	copied := dropFacts{written: true, anyLeft: true, effect: dropEffectCopy, targetClass: explorerWindow}
	with := func(f func(e *dropFacts)) dropFacts {
		e := copied
		f(&e)
		return e
	}
	cases := []struct {
		name string
		f    dropFacts
		want Reason
	}{
		{"a release over our own window", with(func(e *dropFacts) { e.selfDrop = true }), SelfDrop},
		{"... whatever else was true of it", with(func(e *dropFacts) {
			e.selfDrop, e.written, e.effect = true, false, dropEffectMove
		}), SelfDrop},
		{"a DoDragDrop that could not run", with(func(e *dropFacts) { e.end = Failed }), Failed},
		{"an extraction that failed", with(func(e *dropFacts) { e.extractFailed = true }), Failed},
		{"Escape", with(func(e *dropFacts) { e.end, e.written, e.anyLeft = Cancelled, false, false }), Cancelled},
		{"a drop the target never asked for the files of", with(func(e *dropFacts) {
			e.written, e.anyLeft, e.effect = false, false, dropEffectNone
		}), Refused},
		// The Skip that hung the first real drag: Explorer asked, showed its
		// conflict dialog, and came back with no effect. The drop is over.
		{"Skip in Explorer's conflict dialog", with(func(e *dropFacts) { e.effect = dropEffectNone }), Cancelled},
		{"a copy", copied, Copied},
		{"a move that took everything", with(func(e *dropFacts) {
			e.effect, e.anyLeft = dropEffectMove, false
		}), Moved},
		{"a move that left the items where they were", with(func(e *dropFacts) { e.effect = dropEffectMove }), Copied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dropReason(tc.f); got != tc.want {
				t.Fatalf("dropReason = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestDropCleanupTable is what becomes of the staging folder: deleted the
// moment DoDragDrop returns when nobody can still want it, and left
// manifested for the scavenge when a consumer may open the paths late.
func TestDropCleanupTable(t *testing.T) {
	cases := []struct {
		name string
		f    dropFacts
		want stageAction
	}{
		{"a self-drop goes the moment the drag is over",
			dropFacts{selfDrop: true, targetClass: otherWindow}, stageDelete},
		{"a failed extraction goes at once, half-written files and all",
			dropFacts{written: true, extractFailed: true, anyLeft: true, targetClass: otherWindow}, stageDelete},
		{"Escape: nothing was written, and the empty folder goes",
			dropFacts{end: Cancelled, targetClass: otherWindow}, stageDelete},
		{"a DoDragDrop that could not run: the same",
			dropFacts{end: Failed, targetClass: otherWindow}, stageDelete},
		{"a drop the target never asked for the files of",
			dropFacts{targetClass: otherWindow}, stageDelete},
		{"a move that took every item leaves an empty folder, which goes",
			dropFacts{written: true, effect: dropEffectMove, targetClass: otherWindow}, stageDelete},
		{"an Explorer window was measured still reading eighteen seconds later: left",
			dropFacts{written: true, anyLeft: true, effect: dropEffectCopy, targetClass: "CabinetWClass"}, stageLeave},
		{"... and the desktop is no different",
			dropFacts{written: true, anyLeft: true, effect: dropEffectCopy, targetClass: "Progman"}, stageLeave},
		{"... and Skip in Explorer's dialog leaves the files it did not take",
			dropFacts{written: true, anyLeft: true, effect: dropEffectNone, targetClass: "CabinetWClass"}, stageLeave},
		{"another program's window may read the paths late: left for the scavenge",
			dropFacts{written: true, anyLeft: true, effect: dropEffectCopy, targetClass: otherWindow}, stageLeave},
		{"a window whose class could not be read is left too",
			dropFacts{written: true, anyLeft: true, effect: dropEffectCopy}, stageLeave},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := dropCleanup(tc.f)
			if d.action != tc.want {
				t.Fatalf("decided %q (%s), want %q", d.action, d.why, tc.want)
			}
			if d.why == "" {
				t.Error("a decision with no reason: the log would say nothing")
			}
		})
	}
}

// TestTheClassIsNeverInTheVerdict holds the ruling down where it can be
// checked in one line: the same facts under any window class decide the
// same way, Explorer's own included.
func TestTheClassIsNeverInTheVerdict(t *testing.T) {
	for _, class := range []string{"", "CabinetWClass", "ExploreWClass", "Progman", "WorkerW", "cabinetwclass", "Chrome_WidgetWin_1", "Notepad"} {
		f := dropFacts{written: true, anyLeft: true, effect: dropEffectCopy, targetClass: class}
		if d := dropCleanup(f); d.action != stageLeave {
			t.Errorf("a drop over a window of class %q decided %q: the class is back in the verdict", class, d.action)
		}
		if r := dropReason(f); r != Copied {
			t.Errorf("a drop over a window of class %q read as %s, want copied", class, r)
		}
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
		{"a live drag whose process is running is never swept, at any age",
			scavengeFacts{haveManifest: true, state: StateLive, owner: ownerAlive, age: 1000 * hour, maxAge: hour}, false},
		{"a live drag whose process is gone is swept under the ordinary age rule",
			scavengeFacts{haveManifest: true, state: StateLive, owner: ownerGone, age: 10 * hour, maxAge: hour}, true},
		{"a live drag whose process is gone is still given the age",
			scavengeFacts{haveManifest: true, state: StateLive, owner: ownerGone, age: 30 * time.Minute, maxAge: hour}, false},
		{"a live drag whose process is gone is still this process's while it watches",
			scavengeFacts{haveManifest: true, state: StateLive, owner: ownerGone, active: true, age: 10 * hour, maxAge: hour}, false},
		{"a live drag with no owner to ask after is left alone, at any age",
			scavengeFacts{haveManifest: true, state: StateLive, owner: ownerUnknown, age: 10000 * hour, maxAge: hour}, false},
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
	rep := scavenge(root, time.Hour, now, sweepMode{retry: true}, lg.printf)
	if rep.Removed != 2 {
		t.Fatalf("the sweep removed %d, want 2", rep.Removed)
	}
	if rep.Left != 5 {
		t.Errorf("the sweep left %d entries, want 5", rep.Left)
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
	if r := scavenge(filepath.Join(root, "missing"), time.Hour, now, sweepMode{retry: true}, lg.printf); r.Removed != 0 || r.Left != 0 {
		t.Fatalf("a missing root swept %d/%d", r.Removed, r.Left)
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
	s, err := newStage(Options{Root: parent, Items: []Item{{Name: "x.bin"}}, Extract: writeItems(nil)})
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
	if rep := Scavenge(parent, time.Hour, nil); rep.Removed != 0 {
		t.Fatalf("the sweep removed %d folder(s) this process is still watching", rep.Removed)
	}
	if _, err := os.Stat(s.root); err != nil {
		t.Fatalf("the live stage's folder is gone: %v", err)
	}
	// Resolved, it is the sweep's.
	s.finish()
	if rep := Scavenge(parent, time.Hour, nil); rep.Removed != 1 {
		t.Fatalf("the sweep removed %d folder(s) after the stage let go, want 1", rep.Removed)
	}
}

// answerOwner makes the sweep's question about a manifest's owner answer
// the same way every time, so that a crash mid-drag can be staged without
// one.
func answerOwner(t *testing.T, state ownerState) {
	t.Helper()
	prev := ownerOf
	ownerOf = func(Manifest) ownerState { return state }
	t.Cleanup(func() { ownerOf = prev })
}

// TestAManifestNamesItsOwner: the folder records who is dragging, so that a
// later sweep can ask whether they are still there. A manifest from a build
// that recorded no start time still parses, and reads as an owner nobody
// can ask after — which is the answer the old rule was built on.
func TestAManifestNamesItsOwner(t *testing.T) {
	isolateStages(t)
	s, err := newStage(Options{Root: t.TempDir(), Items: []Item{{Name: "x.bin"}}, Extract: writeItems(nil)})
	if err != nil {
		t.Fatal(err)
	}
	defer s.finish()
	m, ok := ReadManifest(s.root)
	if !ok {
		t.Fatal("a new staging folder has no manifest of ours")
	}
	if m.PID != os.Getpid() {
		t.Fatalf("manifest pid = %d, want %d", m.PID, os.Getpid())
	}
	if _, state := probeProcess(os.Getpid()); state == ownerAlive && m.PIDStarted == 0 {
		t.Error("the platform gives this process a start time and the manifest recorded none")
	}
	// Every transition writes the owner out again, so a folder that has
	// been handed out still names the process that holds it.
	s.setState(StateHandedOut)
	if after, _ := ReadManifest(s.root); after.PID != m.PID || after.PIDStarted != m.PIDStarted {
		t.Fatalf("the owner changed across a transition: %d/%d, was %d/%d", after.PID, after.PIDStarted, m.PID, m.PIDStarted)
	}

	// A version 1 manifest: no pidStarted at all, which must parse and read
	// as unknown rather than as a match.
	old := t.TempDir()
	blob, err := json.Marshal(map[string]any{
		"tool": manifestTool, "version": 1, "pid": m.PID,
		"created": s.created, "state": StateLive, "files": []string{"items/x.bin"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, manifestName), blob, 0o600); err != nil {
		t.Fatal(err)
	}
	v1, ok := ReadManifest(old)
	if !ok {
		t.Fatal("a manifest an older build wrote was refused as not ours")
	}
	if v1.PIDStarted != 0 || v1.PID != m.PID || v1.State != StateLive {
		t.Fatalf("an older manifest read as %+v", v1)
	}
	// A manifest naming no process at all can never be matched to one.
	if got := manifestOwner(Manifest{Tool: manifestTool, State: StateLive}); got != ownerUnknown {
		t.Fatalf("a manifest with no pid read as owner %v, want unknown", got)
	}
}

// TestTheSweepTakesALiveFolderWhoseOwnerDied is the hole this closes: a
// process killed mid-drag leaves a folder saying "live", and the old rule
// left it there forever with plaintext in it. Only a known-dead owner opens
// the folder to the sweep: a living one holds it at any age, and an owner
// nobody can ask after holds it at any age too.
func TestTheSweepTakesALiveFolderWhoseOwnerDied(t *testing.T) {
	isolateStages(t)
	now := time.Now()
	mk := func(t *testing.T, age time.Duration) string {
		t.Helper()
		root := t.TempDir()
		dir := filepath.Join(root, "aaaaaaaa")
		if err := os.MkdirAll(filepath.Join(dir, itemsDirName), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, itemsDirName, "secret.txt"), []byte("plaintext"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := WriteManifest(dir, Manifest{
			Tool: manifestTool, Version: manifestVersion, PID: 4242, PIDStarted: 99,
			State: StateLive, Created: now.Add(-age), Files: []string{"items/secret.txt"},
		}); err != nil {
			t.Fatal(err)
		}
		return root
	}
	cases := []struct {
		name  string
		owner ownerState
		age   time.Duration
		gone  bool
	}{
		{"the process that was dragging is gone", ownerGone, 2 * time.Hour, true},
		{"gone, but the folder is younger than the age", ownerGone, 5 * time.Minute, false},
		{"the process is still dragging", ownerAlive, 100 * time.Hour, false},
		{"nobody can be asked, so the folder stays however old it is", ownerUnknown, 10000 * time.Hour, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			answerOwner(t, tc.owner)
			root := mk(t, tc.age)
			lg := &testLog{}
			rep := scavenge(root, time.Hour, now, sweepMode{retry: true}, lg.printf)
			want := 0
			if tc.gone {
				want = 1
			}
			if rep.Removed != want {
				t.Fatalf("the sweep removed %d, want %d:\n%s", rep.Removed, want, lg.text())
			}
			_, err := os.Stat(filepath.Join(root, "aaaaaaaa"))
			if tc.gone != os.IsNotExist(err) {
				t.Fatalf("the folder's presence disagrees with the verdict: %v", err)
			}
		})
	}
}

// TestTheSweepAtExitStopsAtItsCap: the pass a normal exit makes is bounded
// in wall time as well as in attempts — a shutdown's budget belongs to the
// archives and the lock before it belongs to housekeeping — and it stops
// between folders, not inside one, saying how many it never reached. They
// are the next launch's: nothing here is retried, and nothing is left
// half-deleted.
func TestTheSweepAtExitStopsAtItsCap(t *testing.T) {
	isolateStages(t)
	root := t.TempDir()
	now := time.Now()
	var dirs []string
	for _, name := range []string{"aaaaaaaa", "bbbbbbbb", "cccccccc"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "payload.bin"), []byte("plaintext"), 0o600); err != nil {
			t.Fatal(err)
		}
		m := Manifest{Tool: manifestTool, Version: manifestVersion, State: StateDone, Created: now.Add(-2 * time.Hour)}
		if err := WriteManifest(dir, m); err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, dir)
	}
	// The clock the cap watches: the first folder falls inside the budget
	// and everything after it does not.
	prev := sweepNow
	calls := 0
	sweepNow = func() time.Time {
		calls++
		if calls == 1 {
			return now
		}
		return now.Add(time.Minute)
	}
	t.Cleanup(func() { sweepNow = prev })

	lg := &testLog{}
	rep := scavenge(root, time.Hour, now, sweepMode{deadline: now.Add(time.Second)}, lg.printf)
	if rep.Removed != 1 || rep.Unreached != 2 || rep.Left != 0 {
		t.Fatalf("the capped pass came to %+v, want 1 removed, 2 not reached, 0 left", rep)
	}
	if _, err := os.Stat(dirs[0]); !os.IsNotExist(err) {
		t.Errorf("the folder the pass did reach survived it: %v", err)
	}
	for _, stays := range dirs[1:] {
		if _, err := os.Stat(stays); err != nil {
			t.Errorf("the pass removed %s after its cap: %v", filepath.Base(stays), err)
		}
	}
	if !strings.Contains(lg.text(), "not reached") {
		t.Errorf("the log does not say what the cap stopped the pass from reaching:\n%s", lg.text())
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

// TestCloseQuietlyVisitsEveryStage is the close at exit: an earlier drag's
// folder is no less this run's to decide about than the last one's. The
// two decisions differ, and both have to be taken.
func TestCloseQuietlyVisitsEveryStage(t *testing.T) {
	isolateStages(t)
	parent := t.TempDir()
	mk := func(name string, handOut bool) *stage {
		t.Helper()
		items := []Item{{Name: name, Size: 8}}
		s, err := newStage(Options{Root: parent, Items: items, Extract: writeItems(items)})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.finish)
		if handOut {
			s.arm(false, otherWindow)
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

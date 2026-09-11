//go:build windows

package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

// The staged route's tests. None of them opens a window, starts a drag or
// writes anything outside t.TempDir(): the staging root is injected everywhere
// precisely so that a test never touches %LOCALAPPDATA%, where the real
// application's vault lives.

// ---------------------------------------------------------------------------
// Where the staging folder is allowed to be.

// TestStagingRootIsNotTheApplicationFolder is the rule with the worst
// consequence if it is ever broken: the scavenger deletes whole directories
// under this root, so the root must not be, or be inside, %LOCALAPPDATA%\Enfold.
func TestStagingRootIsNotTheApplicationFolder(t *testing.T) {
	const local = `C:\Users\somebody\AppData\Local`
	root := stagingRootIn(local)

	if want := filepath.Join(local, "Enfold-dragproto", "drag"); root != want {
		t.Fatalf("stagingRootIn = %q, want %q", root, want)
	}
	rel, err := filepath.Rel(local, root)
	if err != nil {
		t.Fatalf("the staging root is not under LOCALAPPDATA at all: %v", err)
	}
	first := strings.Split(rel, string(filepath.Separator))[0]
	if first != "Enfold-dragproto" {
		t.Fatalf("the first folder under LOCALAPPDATA is %q, want %q", first, "Enfold-dragproto")
	}
	// The string "Enfold" is a prefix of "Enfold-dragproto", which is exactly the
	// mistake this test exists to catch: the check has to be on the path
	// separator, not on the characters.
	if appFolder := filepath.Join(local, "Enfold") + string(filepath.Separator); strings.HasPrefix(root, appFolder) {
		t.Fatalf("the staging root %q is inside the real application's folder %q", root, appFolder)
	}
	if first == "Enfold" {
		t.Fatalf("the staging root is the application's own folder")
	}
}

func TestStagingRootUsesLocalAppData(t *testing.T) {
	t.Setenv("LOCALAPPDATA", `C:\somewhere\Local`)
	got, err := stagingRoot()
	if err != nil {
		t.Fatalf("stagingRoot: %v", err)
	}
	if want := `C:\somewhere\Local\Enfold-dragproto\drag`; got != want {
		t.Fatalf("stagingRoot = %q, want %q", got, want)
	}
	t.Setenv("LOCALAPPDATA", "")
	if _, err := stagingRoot(); err == nil {
		t.Fatal("stagingRoot succeeded with no LOCALAPPDATA; it must refuse rather than guess")
	}
}

// ---------------------------------------------------------------------------
// DROPFILES.

// decodeDropFiles is the consumer's side, written out longhand: it reads the
// block the way DragQueryFile would, so that the test asserts against the
// documented layout rather than against the encoder's own idea of it.
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
	// "An additional null character is appended to the final string to terminate
	// the array": drop that one, then split on the per-path NULs.
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
	// The constants first: they are a derivation from the documented field types
	// (DWORD, POINT = two LONGs, BOOL = int, all 32-bit, no pointers), and the
	// derivation is the thing to pin.
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

	want := []string{`C:\Users\a\AppData\Local\Enfold-dragproto\drag\0a1b2c3d\one.bin`, `C:\x\two.txt`}
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

	// The exact length, computed from the documented shape: the header, then each
	// path plus its NUL, then the list's own NUL, two bytes per UTF-16 unit.
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
	// A surrogate pair and a non-ASCII name: fWide is a claim about these bytes,
	// and a path that does not survive the round trip would be a file the target
	// cannot find.
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
	// "The structure's hGlobal member points to a DWORD value": four bytes, and
	// nothing else in the block.
	blob := encodeDropEffect(dropEffectMove)
	if len(blob) != 4 {
		t.Fatalf("CFSTR_PREFERREDDROPEFFECT rendered %d bytes, want 4 (a DWORD)", len(blob))
	}
	if v := binary.LittleEndian.Uint32(blob); v != dropEffectMove {
		t.Fatalf("the DWORD is %d, want DROPEFFECT_MOVE (%d)", v, dropEffectMove)
	}
}

func TestStageEffects(t *testing.T) {
	// The default is 7-Zip's: copy and move allowed, move preferred, because the
	// staged file is disposable and a same-volume move is one rename.
	var def stageConfig
	if got := def.allowedEffects(); got != dropEffectCopy|dropEffectMove {
		t.Errorf("allowed effects = %s, want COPY|MOVE", effectName(got))
	}
	if got := def.preferredEffect(); got != dropEffectMove {
		t.Errorf("preferred effect = %s, want DROPEFFECT_MOVE", effectName(got))
	}
	only := stageConfig{copyOnly: true}
	if got := only.allowedEffects(); got != dropEffectCopy {
		t.Errorf("-copy-only allowed effects = %s, want DROPEFFECT_COPY", effectName(got))
	}
	if got := only.preferredEffect(); got != dropEffectCopy {
		t.Errorf("-copy-only preferred effect = %s, want DROPEFFECT_COPY", effectName(got))
	}
}

// ---------------------------------------------------------------------------
// The state machine.

func newTestStage(t *testing.T, cfg stageConfig, files []synthFile) *dragStage {
	t.Helper()
	if cfg.maxAge == 0 {
		cfg.maxAge = time.Hour
	}
	s, err := newDragStage(t.TempDir(), files, cfg)
	if err != nil {
		t.Fatalf("newDragStage: %v", err)
	}
	// finish takes the stage out of the live set and stops any watcher; the
	// TempDir goes with the test.
	t.Cleanup(s.finish)
	return s
}

func TestStageArmStateMachine(t *testing.T) {
	files := []synthFile{{name: "one.bin", size: 1000, seed: 11}, {name: "two.bin", size: 64, seed: 12}}
	s := newTestStage(t, stageConfig{}, files)

	// Before the release: the final paths, over files that do not exist yet.
	paths, word, hr := s.requestPaths()
	if hr != sOK {
		t.Fatalf("a hover request returned %s, want S_OK", hrName(hr))
	}
	if word != "future paths" {
		t.Errorf("a hover request was answered as %q", word)
	}
	if len(paths) != len(files) {
		t.Fatalf("a hover request named %d path(s), want %d", len(paths), len(files))
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s exists during the hover; nothing should be written before the release", p)
		}
	}
	s.mu.Lock()
	early, armed := s.early, s.armed
	s.mu.Unlock()
	if early != 1 || armed {
		t.Fatalf("after one hover request: early=%d armed=%v, want 1/false", early, armed)
	}
	// The paths a hover request named are the ones the files will have -- never a
	// placeholder, which is the rule Edge's caching of the early name forced.
	if got := s.pathList(); len(got) != len(paths) || got[0] != paths[0] {
		t.Fatalf("the path list changed between the hover and now: %q then %q", paths, got)
	}

	// The button comes up.
	s.arm()
	s.mu.Lock()
	armed = s.armed
	s.mu.Unlock()
	if !armed {
		t.Fatal("the stage is not armed after arm()")
	}

	// The first request after the release extracts.
	after, word, hr := s.requestPaths()
	if hr != sOK {
		t.Fatalf("the first post-release request returned %s, want S_OK", hrName(hr))
	}
	if word == "future paths" {
		t.Error("the first post-release request was still treated as a hover request")
	}
	for i, p := range after {
		if p != paths[i] {
			t.Errorf("the path changed after the release: %q, was %q", p, paths[i])
		}
		blob, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("%s was not written by the extraction: %v", p, err)
		}
		want := make([]byte, files[i].size)
		patternAt(files[i].seed, 0, want)
		if string(blob) != string(want) {
			t.Errorf("%s holds %d bytes that do not match the generator", p, len(blob))
		}
	}

	// A later request hands back the same paths and rewrites nothing. Tampering
	// with the file is how that is observed: if the extraction ran again, the
	// tampering would be gone.
	if err := os.WriteFile(after[0], []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	again, _, hr := s.requestPaths()
	if hr != sOK {
		t.Fatalf("a repeat request returned %s, want S_OK", hrName(hr))
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
}

func TestStageEarlyWritesBeforeTheDrag(t *testing.T) {
	files := []synthFile{{name: "early.bin", size: 512, seed: 21}}
	s := newTestStage(t, stageConfig{early: true}, files)
	s.stageEarly()
	if _, err := os.Stat(s.paths[0]); err != nil {
		t.Fatalf("-stage-early did not write the file before the drag: %v", err)
	}
	// A hover request still answers with the paths, and the post-release request
	// finds the extraction already done.
	if _, _, hr := s.requestPaths(); hr != sOK {
		t.Fatalf("a hover request after -stage-early returned %s", hrName(hr))
	}
	s.arm()
	if err := os.WriteFile(s.paths[0], []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, hr := s.requestPaths(); hr != sOK {
		t.Fatalf("the post-release request returned %s", hrName(hr))
	}
	if blob, _ := os.ReadFile(s.paths[0]); string(blob) != "tampered" {
		t.Fatal("the post-release request re-extracted a file -stage-early had already written")
	}
}

// TestStageFailedExtractionFailsGetData is the rule APP.md 3 states against
// 7-Zip's behaviour: a request that cannot be honoured fails rather than naming
// files that are not there.
func TestStageFailedExtractionFailsGetData(t *testing.T) {
	// Bigger than one producer chunk, because -fail-extract fails partway
	// through on purpose -- a half-written file is the case worth having.
	files := []synthFile{{name: "half.bin", size: producerChunk + 4096, seed: 31}}
	s := newTestStage(t, stageConfig{failExtract: true}, files)
	s.arm()

	paths, _, hr := s.requestPaths()
	if hr != eUnexpected {
		t.Fatalf("a failed extraction returned %s, want E_UNEXPECTED", hrName(hr))
	}
	if paths != nil {
		t.Fatalf("a failed extraction still named %q", paths)
	}
	// E_FAIL is deliberately not used: it is not in GetData's documented return
	// list, and E_UNEXPECTED is.
	if hr == eFail {
		t.Fatal("E_FAIL is not one of GetData's documented return values")
	}
	// And it stays failed: a second request must not quietly hand out the names
	// of the half-written files.
	if _, _, hr := s.requestPaths(); hr != eUnexpected {
		t.Fatalf("the second request after a failure returned %s, want E_UNEXPECTED", hrName(hr))
	}
	s.mu.Lock()
	failed, handed := s.failed, s.handedOut
	s.mu.Unlock()
	if !failed {
		t.Error("the stage does not remember that the extraction failed")
	}
	if handed {
		t.Error("a stage whose extraction failed must not count as having handed anything out")
	}
}

// TestStageFolderDragNamesTheFolder: CF_HDROP has no way to say "a folder" other
// than naming it, and naming the files inside would drop them loose into the
// destination -- the subfolder would never appear there at all.
func TestStageFolderDragNamesTheFolder(t *testing.T) {
	files := buildFileSet(fileSetConfig{count: 2, size: 4096, folder: true})
	s := newTestStage(t, stageConfig{}, files)
	if len(s.drop) != 1 {
		t.Fatalf("a -folder drag names %d path(s): %q; want the one folder", len(s.drop), s.drop)
	}
	if want := filepath.Join(s.root, "Proto"); s.drop[0] != want {
		t.Fatalf("a -folder drag names %q, want %q", s.drop[0], want)
	}
	s.arm()
	if _, _, hr := s.requestPaths(); hr != sOK {
		t.Fatalf("the extraction returned %s", hrName(hr))
	}
	for _, p := range s.paths {
		if filepath.Dir(p) != s.drop[0] {
			t.Errorf("%s was not written inside the named folder", p)
		}
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
}

// TestStageCopiesASourceFileAndLeavesItAlone is -file through the staged route:
// the "extraction" is a copy of a real file into the staging folder. Two things
// are asserted, and the second matters more than the first -- the copy is
// byte-identical, and the source is exactly as it was. A prototype that moved,
// truncated or re-dated somebody's video would be a prototype that ate the thing
// it was pointed at.
func TestStageCopiesASourceFileAndLeavesItAlone(t *testing.T) {
	// Bigger than one producer chunk, so the copy goes round its loop more than
	// once and a short final chunk is exercised.
	path, want := writeSource(t, "holiday.mp4", producerChunk+4097)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	files, err := sourceFiles([]string{path}, false, 0)
	if err != nil {
		t.Fatalf("sourceFiles: %v", err)
	}
	s := newTestStage(t, stageConfig{}, files)
	s.arm()
	paths, _, hr := s.requestPaths()
	if hr != sOK {
		t.Fatalf("the extraction returned %s", hrName(hr))
	}
	if len(paths) != 1 || filepath.Base(paths[0]) != "holiday.mp4" {
		t.Fatalf("the staged paths are %q, want one named after the source", paths)
	}

	got, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatalf("the staged copy: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("the staged copy is %d bytes and does not match the source (%d bytes)", len(got), len(want))
	}
	// The copy carries the source's modification time, so what the target ends
	// up with is the file it was offered rather than one made during the drag.
	staged, err := os.Stat(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if !staged.ModTime().Equal(before.ModTime()) {
		t.Errorf("the staged copy is dated %s, want the source's %s", staged.ModTime(), before.ModTime())
	}

	// The source: same size, same modification time, same bytes, still there.
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the source is gone after the extraction: %v", err)
	}
	if after.Size() != before.Size() {
		t.Errorf("the source is now %d bytes, was %d", after.Size(), before.Size())
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("the source is now dated %s, was %s", after.ModTime(), before.ModTime())
	}
	stillThere, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stillThere, want) {
		t.Error("the source's own bytes changed")
	}
}

// ---------------------------------------------------------------------------
// The manifest.

func TestManifestLifecycle(t *testing.T) {
	files := []synthFile{{name: "m.bin", size: 256, seed: 41}}
	s := newTestStage(t, stageConfig{}, files)

	m, ok := readManifest(s.root)
	if !ok {
		t.Fatal("a new staging folder has no manifest of ours")
	}
	if m.Tool != manifestTool || m.Version != manifestVersion {
		t.Errorf("manifest tool/version = %q/%d", m.Tool, m.Version)
	}
	if m.State != stateLive {
		t.Errorf("a new staging folder is in state %q, want %q", m.State, stateLive)
	}
	if m.PID != os.Getpid() {
		t.Errorf("manifest pid = %d, want %d", m.PID, os.Getpid())
	}
	if len(m.Files) != 1 || m.Files[0] != "m.bin" {
		t.Errorf("manifest files = %q", m.Files)
	}
	if m.Created.IsZero() {
		t.Error("the manifest has no creation time, which is what the scavenge ages")
	}

	// Handing the paths out is the transition that makes the folder outlive the
	// drop, so it has to reach the disk.
	if _, _, hr := s.requestPaths(); hr != sOK {
		t.Fatalf("a hover request returned %s", hrName(hr))
	}
	if m, _ := readManifest(s.root); m.State != stateHandedOut {
		t.Fatalf("after a request the manifest says %q, want %q", m.State, stateHandedOut)
	}

	// A foreign folder is not ours, whatever it contains.
	other := t.TempDir()
	blob, _ := json.Marshal(map[string]any{"tool": "somebody-else", "state": "done"})
	if err := os.WriteFile(filepath.Join(other, manifestName), blob, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := readManifest(other); ok {
		t.Fatal("a manifest written by another tool was accepted as ours")
	}
	if err := os.WriteFile(filepath.Join(other, manifestName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := readManifest(other); ok {
		t.Fatal("an unparsable manifest was accepted as ours")
	}
}

// ---------------------------------------------------------------------------
// The cleanup decision table.

func TestCleanupDecisionTable(t *testing.T) {
	const hour = time.Hour
	cases := []struct {
		name string
		ev   stageEvents
		want stageAction
	}{
		{
			"keep wins over everything, the forced close included",
			stageEvents{keep: true, forcedClose: true, handedOut: true, maxAge: hour},
			stageKeepForever,
		},
		{
			"a forced close deletes and records what delete-at-reboot does",
			stageEvents{forcedClose: true, handedOut: true, anyInUse: true, anyLeft: true, maxAge: hour},
			stageForceDelete,
		},
		{
			"a drag that ended having written nothing goes at once",
			stageEvents{dragOver: true, maxAge: hour},
			stageDelete,
		},
		{
			"... including one where a hover request took the paths but nothing was written",
			stageEvents{dragOver: true, handedOut: true, maxAge: hour},
			stageDelete,
		},
		{
			"files written but never handed out (only -stage-early can do that)",
			stageEvents{dragOver: true, written: true, anyLeft: true, maxAge: hour},
			stageDelete,
		},
		{
			"a failed extraction goes at once, half-written files and all",
			stageEvents{dragOver: true, written: true, extractFailed: true, handedOut: true, anyLeft: true, maxAge: hour},
			stageDelete,
		},
		{
			"EndOperation on a negotiated transfer ends it",
			stageEvents{dragOver: true, written: true, handedOut: true, anyLeft: true, asyncOp: true, endOperation: true, maxAge: hour},
			stageDelete,
		},
		{
			"EndOperation without a negotiated operation is not a signal at all",
			stageEvents{dragOver: true, written: true, handedOut: true, anyLeft: true, endOperation: true, maxAge: hour},
			stageWait,
		},
		{
			"a file still open holds everything up",
			stageEvents{dragOver: true, written: true, handedOut: true, anyLeft: true, anyInUse: true, endOperation: true, asyncOp: true, maxAge: hour},
			stageWait,
		},
		{
			"paths handed out and no EndOperation: the folder outlives the drop",
			stageEvents{dragOver: true, written: true, handedOut: true, anyLeft: true, age: 5 * time.Minute, maxAge: hour},
			stageWait,
		},
		{
			"... until it is past the scavenge age",
			stageEvents{dragOver: true, written: true, handedOut: true, anyLeft: true, age: 2 * hour, maxAge: hour},
			stageDelete,
		},
		{
			"a same-volume move took every file: nothing is left to keep",
			stageEvents{dragOver: true, written: true, handedOut: true, anyLeft: false, maxAge: hour},
			stageDelete,
		},
		{
			"during the hover, nothing is decided",
			stageEvents{maxAge: hour},
			stageWait,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := decideStageCleanup(tc.ev)
			if d.action != tc.want {
				t.Fatalf("decided %q (%s), want %q", d.action, d.why, tc.want)
			}
			if d.why == "" {
				t.Error("a decision with no reason: the log would say nothing")
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
			scavengeFacts{reparse: true, haveManifest: true, state: stateDone, age: 10 * hour, maxAge: hour}, false},
		{"a live drag is never swept",
			scavengeFacts{haveManifest: true, state: stateLive, age: 10 * hour, maxAge: hour}, false},
		{"a drag this process still watches is left to it",
			scavengeFacts{haveManifest: true, state: stateHandedOut, active: true, age: 10 * hour, maxAge: hour}, false},
		{"younger than the limit",
			scavengeFacts{haveManifest: true, state: stateHandedOut, age: 59 * time.Minute, maxAge: hour}, false},
		{"exactly the limit is not past it",
			scavengeFacts{haveManifest: true, state: stateHandedOut, age: hour, maxAge: hour}, false},
		{"handed out and past the limit",
			scavengeFacts{haveManifest: true, state: stateHandedOut, age: hour + time.Second, maxAge: hour}, true},
		{"a folder whose deletion was decided and failed",
			scavengeFacts{haveManifest: true, state: stateDone, age: 2 * hour, maxAge: hour}, true},
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

// TestScavengeBackoffIsBounded: 1 s, 10 s, 60 s and then the folder is left for
// the next sweep. An unbounded retry would be a goroutine spinning on a file
// some consumer means to keep open for an hour.
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

// TestSweepStagingTouchesOnlyOurFolders builds a drag root by hand and sweeps
// it. Everything is under t.TempDir(); nothing here goes near %LOCALAPPDATA%.
func TestSweepStagingTouchesOnlyOurFolders(t *testing.T) {
	root := t.TempDir()
	now := time.Now()

	mk := func(name string, m *stageManifest) string {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "payload.bin"), []byte("plaintext"), 0o600); err != nil {
			t.Fatal(err)
		}
		if m != nil {
			if err := writeManifest(dir, *m); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}

	old := mk("aaaaaaaa", &stageManifest{Tool: manifestTool, Version: manifestVersion,
		State: stateHandedOut, Created: now.Add(-2 * time.Hour)})
	done := mk("bbbbbbbb", &stageManifest{Tool: manifestTool, Version: manifestVersion,
		State: stateDone, Created: now.Add(-90 * time.Minute)})
	live := mk("cccccccc", &stageManifest{Tool: manifestTool, Version: manifestVersion,
		State: stateLive, Created: now.Add(-3 * time.Hour)})
	young := mk("dddddddd", &stageManifest{Tool: manifestTool, Version: manifestVersion,
		State: stateHandedOut, Created: now.Add(-5 * time.Minute)})
	foreign := mk("somebody-elses-folder", nil)
	other := mk("eeeeeeee", &stageManifest{Tool: "some-other-tool", State: stateDone,
		Created: now.Add(-10 * time.Hour)})
	stray := filepath.Join(root, "loose.txt")
	if err := os.WriteFile(stray, []byte("not a staging folder"), 0o600); err != nil {
		t.Fatal(err)
	}

	removed, kept, failed := sweepStaging(root, time.Hour, now)
	if removed != 2 || failed != 0 {
		t.Fatalf("the sweep removed %d and failed on %d, want 2 and 0", removed, failed)
	}
	if kept != 5 {
		t.Errorf("the sweep left %d entries, want 5", kept)
	}
	for _, gone := range []string{old, done} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s survived the sweep", gone)
		}
	}
	for _, stays := range []string{live, young, foreign, other, stray} {
		if _, err := os.Stat(stays); err != nil {
			t.Errorf("the sweep removed %s, which is not its to remove: %v", stays, err)
		}
	}
}

// TestSweepLeavesALiveStageAlone: the ten-minute sweep and the drag running in
// this process must not race for the same folder.
func TestSweepLeavesALiveStageAlone(t *testing.T) {
	parent := t.TempDir()
	s, err := newDragStage(parent, []synthFile{{name: "x.bin", size: 16, seed: 51}}, stageConfig{maxAge: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	defer s.finish()
	// Old enough and no longer "live" as far as the manifest goes -- only the
	// live-stage registry protects it now.
	s.setState(stateHandedOut)
	m, _ := readManifest(s.root)
	m.Created = time.Now().Add(-10 * time.Hour)
	if err := writeManifest(s.root, m); err != nil {
		t.Fatal(err)
	}
	if removed, _, _ := sweepStaging(parent, time.Hour, time.Now()); removed != 0 {
		t.Fatalf("the sweep removed %d folder(s) this process is still watching", removed)
	}
	if _, err := os.Stat(s.root); err != nil {
		t.Fatalf("the live stage's folder is gone: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Deleting a tree.

func TestRemoveTreeNoReparse(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "stage")
	sub := filepath.Join(dir, "Proto")
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
// puts a junction under a staging folder. Creating a directory symlink needs a
// privilege an ordinary test process does not have, so this skips rather than
// fails when it cannot be set up.
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
		t.Fatalf("the delete followed the link and took %s with it: %v", keep, err)
	}
}

// ---------------------------------------------------------------------------
// The data object in -hdrop mode.

func TestHDropDataObjectFormats(t *testing.T) {
	s := newTestStage(t, stageConfig{}, []synthFile{{name: "f.bin", size: 128, seed: 61}})
	o := newHDropDataObject(s)
	defer o.release()
	d := o.impl.(*dataObject)

	if len(d.formats) != 2 {
		t.Fatalf("-hdrop offers %d formats, want 2", len(d.formats))
	}
	for _, f := range d.formats {
		if f.tymed != tymedHGlobal || f.dwAspect != dvAspectContent || f.lindex != -1 {
			t.Errorf("%s is offered as {aspect %d, lindex %d, %s}, want {CONTENT, -1, TYMED_HGLOBAL}",
				formatName(f.cfFormat), f.dwAspect, f.lindex, tymedName(f.tymed))
		}
	}
	if !d.offers(cfHDrop) || !d.offers(cfPreferredDropEffect) {
		t.Error("-hdrop does not offer both CF_HDROP and CFSTR_PREFERREDDROPEFFECT")
	}
	// The virtual-file formats are deliberately gone: the target picks the
	// format, so offering both routes would measure whichever it preferred.
	if d.offers(cfFileGroupDescriptorW) || d.offers(cfFileContents) {
		t.Error("-hdrop still offers the virtual-file formats")
	}

	// A GetData for a format this mode never offers is DV_E_FORMATETC, not a
	// medium or an index complaint.
	fe := formatEtc{cfFormat: cfFileContents, dwAspect: dvAspectContent, lindex: 0, tymed: tymedIStream}
	var medium stgMedium
	if hr := dataGetData(o.slotAddr(0), &fe, &medium); hr != dvEFormatEtc {
		t.Fatalf("GetData(FileContents) in -hdrop mode returned %s, want DV_E_FORMATETC", hrName(hr))
	}

	// And a hover request for CF_HDROP renders a real block.
	fe = formatEtc{cfFormat: cfHDrop, dwAspect: dvAspectContent, lindex: -1, tymed: tymedHGlobal}
	medium = stgMedium{}
	if hr := dataGetData(o.slotAddr(0), &fe, &medium); hr != sOK {
		t.Fatalf("GetData(CF_HDROP) returned %s", hrName(hr))
	}
	if medium.tymed != tymedHGlobal || medium.data == 0 {
		t.Fatalf("the medium is {tymed %s, data 0x%X}", tymedName(medium.tymed), medium.data)
	}
	if medium.pUnkForRelease != 0 {
		t.Error("pUnkForRelease must be NULL: the receiver frees the medium")
	}
	n := globalSize(medium.data)
	if n < dropFilesHeaderSize {
		t.Fatalf("the HGLOBAL is %d bytes", n)
	}
	blob := make([]byte, n)
	if p := globalLock(medium.data); p != 0 {
		copyFromNative(blob, p)
		globalUnlock(medium.data)
	} else {
		t.Fatal("GlobalLock failed on the medium we just allocated")
	}
	globalFree(medium.data)
	_, _, _, _, fWide, paths := decodeDropFiles(t, blob)
	if fWide == 0 {
		t.Error("fWide is zero in a rendered CF_HDROP block")
	}
	if len(paths) != 1 || paths[0] != s.paths[0] {
		t.Fatalf("the rendered block names %q, want %q", paths, s.paths)
	}

	// The preferred effect is a DWORD, and it is MOVE by default.
	fe = formatEtc{cfFormat: cfPreferredDropEffect, dwAspect: dvAspectContent, lindex: -1, tymed: tymedHGlobal}
	medium = stgMedium{}
	if hr := dataGetData(o.slotAddr(0), &fe, &medium); hr != sOK {
		t.Fatalf("GetData(CFSTR_PREFERREDDROPEFFECT) returned %s", hrName(hr))
	}
	four := make([]byte, 4)
	if p := globalLock(medium.data); p != 0 {
		copyFromNative(four, p)
		globalUnlock(medium.data)
	}
	globalFree(medium.data)
	if v := binary.LittleEndian.Uint32(four); v != dropEffectMove {
		t.Fatalf("CFSTR_PREFERREDDROPEFFECT = %s, want DROPEFFECT_MOVE", effectName(v))
	}
}

// ---------------------------------------------------------------------------
// Watching the staged files: the transitions, the first tick included.

// TestWatchedFileTransitions is the per-file decision table, and the first four
// cases are the defect it was written for. A same-volume drop is a rename, and
// Explorer can finish it inside the 250 ms before the watch's first tick: the
// file is then already missing the first time it is looked at. The earlier shape
// had no case for that at all, so it logged nothing, the file never counted as
// gone, and the stage sat in "handed-out" over an empty folder.
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
		{
			name:  "missing at the first tick, and the extraction had written it: a move that beat the watch",
			start: stagedFile{path: `C:\stage\a.bin`, since: was}, probe: fileProbe{}, staged: true,
			seen: true, gone: true,
			line: "GONE (moved away by the target) before the first poll tick",
		},
		{
			name:  "missing at the first tick with nothing ever written: not a removal",
			start: stagedFile{path: `C:\stage\a.bin`, since: was}, probe: fileProbe{}, staged: false,
			line: "",
		},
		{
			name:  "present at the first tick: the first-look line says it, not a transition",
			start: stagedFile{path: `C:\stage\a.bin`, since: was}, probe: fileProbe{exists: true}, staged: true,
			seen: true,
			line: "",
		},
		{
			name:  "present and already being read at the first tick",
			start: stagedFile{path: `C:\stage\a.bin`, since: was}, probe: fileProbe{exists: true, inUse: true}, staged: true,
			seen: true, inUse: true, everUsed: true,
			line: "",
		},
		{
			name:  "a file that was there and is not any more was moved",
			start: stagedFile{path: `C:\stage\a.bin`, seen: true, exists: true, since: was}, probe: fileProbe{}, staged: true,
			seen: true, gone: true,
			line: "GONE (moved away by the target)",
		},
		{
			name:  "a target opened it",
			start: stagedFile{path: `C:\stage\a.bin`, seen: true, exists: true, since: was},
			probe: fileProbe{exists: true, inUse: true}, staged: true,
			seen: true, inUse: true, everUsed: true,
			line: "in use by another process",
		},
		{
			name:  "and let go of it again",
			start: stagedFile{path: `C:\stage\a.bin`, seen: true, exists: true, inUse: true, everUsed: true, since: was},
			probe: fileProbe{exists: true}, staged: true,
			seen: true, everUsed: true,
			line: "staged file free",
		},
		{
			name:  "gone and still gone is not said twice",
			start: stagedFile{path: `C:\stage\a.bin`, seen: true, gone: true, since: was}, probe: fileProbe{}, staged: true,
			seen: true, gone: true,
			line: "",
		},
		{
			name:  "a file that comes back is said out loud",
			start: stagedFile{path: `C:\stage\a.bin`, seen: true, gone: true, since: was},
			probe: fileProbe{exists: true}, staged: true,
			seen: true,
			line: "staged file is back",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := tc.start
			lines := w.observe(tc.probe, tc.staged, now)
			if w.seen != tc.seen || w.gone != tc.gone || w.inUse != tc.inUse || w.everUsed != tc.everUsed {
				t.Fatalf("seen=%v gone=%v inUse=%v everUsed=%v; want %v/%v/%v/%v",
					w.seen, w.gone, w.inUse, w.everUsed, tc.seen, tc.gone, tc.inUse, tc.everUsed)
			}
			got := strings.Join(lines, "\n")
			if tc.line == "" {
				if got != "" {
					t.Fatalf("the transition logged %q; this one has nothing to say", got)
				}
				return
			}
			if !strings.Contains(got, tc.line) {
				t.Fatalf("the transition logged %q, want it to contain %q", got, tc.line)
			}
		})
	}
}

// TestFirstLookLineNamesEveryFile: the one line per stage that says what the
// watch started from. A log that goes straight from "watching 1 staged file(s)"
// to a cleanup decision never says whether the file was there at all.
func TestFirstLookLineNamesEveryFile(t *testing.T) {
	line := firstLookLine(`C:\stage\abcd1234`, []string{"a.bin: present, free", "b.bin: missing"})
	for _, want := range []string{`C:\stage\abcd1234`, "first look", "a.bin: present, free", "b.bin: missing"} {
		if !strings.Contains(line, want) {
			t.Fatalf("the first-look line %q does not mention %q", line, want)
		}
	}
	if got := lookWord(fileProbe{exists: true, inUse: true}); !strings.Contains(got, "in use") {
		t.Errorf("a file being read at the first look is described as %q", got)
	}
	if got := lookWord(fileProbe{}); got != "missing" {
		t.Errorf("a file that is not there at the first look is described as %q", got)
	}
	// Many files must not turn one line into a hundred.
	long := make([]string, 20)
	for i := range long {
		long[i] = "f.bin: missing"
	}
	if !strings.Contains(firstLookLine("root", long), "and 12 more") {
		t.Error("the first-look line does not bound itself for a drag of twenty files")
	}
}

// TestMoveBeforeTheFirstTickEndsTheDrag is the same defect end to end: the
// target renames the staged file away before the watch's first tick, and the
// stage has to recognise the completed move -- the cleanest end a drag has --
// rather than sit in "handed-out" over an empty folder until the scavenge.
func TestMoveBeforeTheFirstTickEndsTheDrag(t *testing.T) {
	isolateStages(t)
	log := captureLog(t)

	s := newTestStage(t, stageConfig{}, []synthFile{{name: "moved.bin", size: 4096, seed: 71}})
	s.arm()
	if _, _, hr := s.requestPaths(); hr != sOK {
		t.Fatalf("the extraction returned %s", hrName(hr))
	}
	// The drop: a same-volume move, which from here is a rename away. It happens
	// before the watch has looked even once.
	if err := os.Rename(s.paths[0], filepath.Join(t.TempDir(), "moved.bin")); err != nil {
		t.Fatalf("simulating the target's move: %v", err)
	}
	s.mu.Lock()
	s.dragOver = true
	s.mu.Unlock()
	if !s.armWatch() {
		t.Fatal("the watch did not arm")
	}
	if !s.poll(time.Now()) {
		t.Fatal("the first poll did not finish the stage; a completed move is the end of a drag")
	}

	out := log()
	for _, want := range []string{
		"the watch's first look",
		"moved.bin: missing",
		"GONE (moved away by the target) before the first poll tick",
		"every staged file was moved away by the target",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the log never said %q:\n%s", want, out)
		}
	}
	s.mu.Lock()
	state, removed, gone := s.state, s.removed, s.watch[0].gone
	s.mu.Unlock()
	if !gone {
		t.Error("the staged file is not marked gone after a move that beat the first tick")
	}
	if state != stateDone {
		t.Errorf("the manifest state is %q, want %q", state, stateDone)
	}
	if !removed {
		t.Error("the empty staging folder was not deleted")
	}
	if _, err := os.Stat(s.root); !os.IsNotExist(err) {
		t.Errorf("the staging folder survived a completed move: %v", err)
	}
}

// TestFirstTickWithNothingWrittenIsNotAMove: the other half of the same
// decision. A drag that ended before the extraction ever ran has no files, and
// their absence at the first tick must not be read as a target taking them.
func TestFirstTickWithNothingWrittenIsNotAMove(t *testing.T) {
	isolateStages(t)
	log := captureLog(t)

	s := newTestStage(t, stageConfig{}, []synthFile{{name: "never.bin", size: 4096, seed: 72}})
	s.mu.Lock()
	s.dragOver = true
	s.mu.Unlock()
	if !s.armWatch() {
		t.Fatal("the watch did not arm")
	}
	s.poll(time.Now())

	out := log()
	if !strings.Contains(out, "never.bin: missing") {
		t.Errorf("the first look did not report the missing file:\n%s", out)
	}
	if strings.Contains(out, "GONE") {
		t.Errorf("a file that was never written was reported as moved away:\n%s", out)
	}
	if !strings.Contains(out, "the drag ended and nothing was ever written") {
		t.Errorf("the cleanup did not take the empty folder for the right reason:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// The forced close, over every stage this run made.

// isolateStages gives one test the two stage registries to itself. The forced
// close and the exit summary walk every stage this process made, so a test that
// asserts on "every" must not inherit the stages of the tests before it.
func isolateStages(t *testing.T) {
	t.Helper()
	allStages.mu.Lock()
	prevAll := allStages.list
	allStages.list = nil
	allStages.mu.Unlock()
	liveStages.mu.Lock()
	prevLive := liveStages.m
	liveStages.m = make(map[string]*dragStage)
	liveStages.mu.Unlock()
	t.Cleanup(func() {
		allStages.mu.Lock()
		allStages.list = prevAll
		allStages.mu.Unlock()
		liveStages.mu.Lock()
		liveStages.m = prevLive
		liveStages.mu.Unlock()
	})
}

// holdOpen keeps a file open the way a consumer reading a dropped file does:
// CreateFileW with dwShareMode 0, which is what makes the delete below fail with
// a sharing violation rather than succeed quietly.
func holdOpen(t *testing.T, path string) {
	t.Helper()
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, 0, nil,
		windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("holding %s open: %v", path, err)
	}
	t.Cleanup(func() { windows.CloseHandle(h) })
}

// TestForcedCloseVisitsEveryStage is the first defect of the two-drag round. The
// first drag stalled in the target and its folder kept five gigabytes of
// plaintext; the second completed as a move and left an empty folder. The forced
// close logged exactly one deletion -- the second drag's -- and never looked at
// the first, because all it had was a pointer to the stage that happened to be
// last. Both have to be visited, and the one that cannot be deleted has to be
// reported rather than silently skipped.
func TestForcedCloseVisitsEveryStage(t *testing.T) {
	isolateStages(t)
	log := captureLog(t)

	// The delete-at-reboot attempt is recorded, not made: a test must not leave
	// PendingFileRenameOperations entries on the machine it runs on.
	var reboot []string
	prevReboot := deleteAtReboot
	deleteAtReboot = func(path string) {
		reboot = append(reboot, path)
		logf("staging: MoveFileExW(%s, NULL, MOVEFILE_DELAY_UNTIL_REBOOT) attempted; the test recorded it instead of calling it", path)
	}
	t.Cleanup(func() { deleteAtReboot = prevReboot })

	parent := t.TempDir()
	mk := func(name string, seed uint64) *dragStage {
		t.Helper()
		s, err := newDragStage(parent, []synthFile{{name: name, size: 2048, seed: seed}}, stageConfig{maxAge: time.Hour})
		if err != nil {
			t.Fatalf("newDragStage: %v", err)
		}
		t.Cleanup(s.finish)
		s.arm()
		if _, _, hr := s.requestPaths(); hr != sOK {
			t.Fatalf("the extraction for %s returned %s", name, hrName(hr))
		}
		return s
	}

	// Drag one: handed out, and a consumer still has the file open.
	stuck := mk("stuck.bin", 81)
	holdOpen(t, stuck.paths[0])
	// Drag two: the target moved the file away, so only the manifest is left.
	moved := mk("moved.bin", 82)
	if err := os.Remove(moved.paths[0]); err != nil {
		t.Fatalf("simulating the target's move: %v", err)
	}

	forceCloseAllStages()

	out := log()
	// Both visited, and the log says so in a way that counts them.
	for _, s := range []*dragStage{stuck, moved} {
		if !strings.Contains(out, "forced close 1 of 2: "+s.root) && !strings.Contains(out, "forced close 2 of 2: "+s.root) {
			t.Errorf("the forced close never visited %s:\n%s", s.root, out)
		}
	}

	// The one that could be deleted was.
	if !moved.removed {
		t.Error("the emptied staging folder was not deleted by the forced close")
	}
	if _, err := os.Stat(moved.root); !os.IsNotExist(err) {
		t.Errorf("the emptied staging folder survived: %v", err)
	}

	// The one that could not be is reported, with the error Windows gave and the
	// delete-at-reboot attempt, and it is still there.
	if stuck.removed {
		t.Fatal("the staging folder whose file is held open was reported as deleted")
	}
	if stuck.removeErr == nil {
		t.Fatal("the failed delete left no error behind, so nothing could be reported")
	}
	for _, want := range []string{
		"staging folder could NOT be deleted: " + stuck.root,
		"is still open by another process",
		"MOVEFILE_DELAY_UNTIL_REBOOT",
		"1 staging folder(s) of this run are left on disk (after the forced close)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the log never said %q:\n%s", want, out)
		}
	}
	if len(reboot) == 0 {
		t.Error("the forced close never attempted MOVEFILE_DELAY_UNTIL_REBOOT on what would not go")
	} else {
		var sawFile, sawDir bool
		for _, p := range reboot {
			if p == stuck.paths[0] {
				sawFile = true
			}
			if p == stuck.root {
				sawDir = true
			}
		}
		if !sawFile || !sawDir {
			t.Errorf("delete-at-reboot was attempted on %q; want the file and its folder", reboot)
		}
	}
	if _, err := os.Stat(stuck.paths[0]); err != nil {
		t.Errorf("the file that could not be deleted is gone after all: %v", err)
	}
	// And the folder is still one the sweep can recognise. A partial delete
	// takes the manifest with it, and a folder without ours is one the sweep is
	// forbidden to touch -- which would make a failed delete permanent.
	m, ok := readManifest(stuck.root)
	if !ok {
		t.Fatal("the folder left behind has no manifest of ours, so no sweep will ever take it")
	}
	if m.State != stateDone {
		t.Errorf("the folder left behind is manifested %q, want %q", m.State, stateDone)
	}
	if remove, why := scavengeVerdict(scavengeFacts{
		haveManifest: true, state: m.State, age: 2 * time.Hour, maxAge: time.Hour,
	}); !remove {
		t.Errorf("a later sweep would leave the folder behind: %s", why)
	}

	// And the exit summary can still name it, which the live set alone cannot:
	// the stage is resolved, so it has left liveStages for the sweep to take.
	left := stagesLeftOnDisk()
	if len(left) != 1 || left[0] != stuck {
		t.Fatalf("stagesLeftOnDisk() named %d folder(s), want just %s", len(left), stuck.root)
	}
	if d := left[0].disposition(); !strings.Contains(d, "LEFT ON DISK") || !strings.Contains(d, "the delete failed") {
		t.Errorf("the disposition of the folder left behind is %q", d)
	}
	if stageIsActive(stuck.root) {
		t.Error("a resolved stage is still in the live set, so the scavenge would never take its folder")
	}
}

// TestQuietCloseVisitsEveryStage is the same rule for the ordinary close: an
// earlier drag's folder is no less this run's to decide about than the last
// one's. The two decisions differ, and both have to be taken.
func TestQuietCloseVisitsEveryStage(t *testing.T) {
	isolateStages(t)
	log := captureLog(t)

	parent := t.TempDir()
	mk := func(name string, seed uint64, handOut bool) *dragStage {
		t.Helper()
		s, err := newDragStage(parent, []synthFile{{name: name, size: 512, seed: seed}}, stageConfig{maxAge: time.Hour})
		if err != nil {
			t.Fatalf("newDragStage: %v", err)
		}
		t.Cleanup(s.finish)
		if handOut {
			s.arm()
			if _, _, hr := s.requestPaths(); hr != sOK {
				t.Fatalf("the extraction for %s returned %s", name, hrName(hr))
			}
		}
		return s
	}
	handed := mk("handed.bin", 91, true)
	untouched := mk("untouched.bin", 92, false)

	quietCloseAllStages()

	out := log()
	// The folder whose paths a target has is left for the scavenge; the one
	// nothing was ever told about goes now. Both decisions were taken.
	if _, err := os.Stat(handed.root); err != nil {
		t.Errorf("the close deleted a folder whose paths were handed out: %v", err)
	}
	if _, err := os.Stat(untouched.root); !os.IsNotExist(err) {
		t.Errorf("the close left a folder nothing was ever handed out of: %v", err)
	}
	if !strings.Contains(out, "the window is closing, but the paths were handed out; "+handed.root) {
		t.Errorf("the close said nothing about %s:\n%s", handed.root, out)
	}
	if !strings.Contains(out, "staging folder deleted: "+untouched.root) {
		t.Errorf("the close said nothing about %s:\n%s", untouched.root, out)
	}
}

// TestAsyncEffectNote: DoDragDrop's effect out-parameter is DROPEFFECT_NONE for
// an asynchronous drop whatever the target goes on to do -- the round that moved
// a 5 GiB file to the desktop logged NONE for a move that completed -- so the
// line has to say so rather than let NONE be read as a refusal.
func TestAsyncEffectNote(t *testing.T) {
	if note := asyncEffectNote(true); !strings.Contains(note, "asynchronous") || !strings.Contains(note, "not reported here") {
		t.Fatalf("the asynchronous note is %q", note)
	}
	if note := asyncEffectNote(false); note != "" {
		t.Fatalf("a synchronous drop's effect line was annotated with %q; there the effect IS the answer", note)
	}
}

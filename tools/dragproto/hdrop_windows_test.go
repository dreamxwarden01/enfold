//go:build windows

package main

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
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

//go:build windows

package main

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

// -hdrop: the staged route, which is the one the design ruled for.
//
// The virtual-file mode is the record of what FileGroupDescriptorW plus
// FileContents does. APP.md 3 and DECISIONS.md ("Ruled: the staged route", then
// "The third research pass") chose the other mechanism -- 7-Zip's and WinRAR's,
// with what their authors and users learnt folded in -- and this file is it:
// CF_HDROP by *delayed rendering* over files extracted into a staging folder at
// drop time.
//
// The shape, which is 7-Zip's where 7-Zip was right and deliberately not where
// its own tracker says it was wrong:
//
//   - an empty, manifested staging folder is made BEFORE DoDragDrop, and the
//     paths the files will have are decided there and then;
//   - a GetData(CF_HDROP) during the hover -- and targets do ask during the
//     hover -- is answered with those FINAL paths. Never a placeholder
//     directory: Edge caches the early name, and Sticky Notes refuses a path
//     that does not exist, so the name handed out first has to be the name that
//     ends up meaning something;
//   - the button's release arms the extraction;
//   - the first GetData(CF_HDROP) after the release runs it, inside the call;
//   - a request that cannot be honoured FAILS GetData rather than handing out
//     the names of files that are not there (7-Zip returns the names either way,
//     which its own comments treat as a known defect);
//   - every later request hands back the same paths without writing anything.
//
// What the prototype adds, because measuring is what it is for: how long the
// target waited inside that GetData and on which thread it ran; when each staged
// file was opened, closed, or taken away by a move; every format the target
// asked for and how long before or after the button's release; and every
// deletion attempt with the error Windows gave.
//
// The staging folder is under %LOCALAPPDATA%\Enfold-dragproto\, never
// %LOCALAPPDATA%\Enfold\: that second one is the real application's folder, with
// a real user's vault in it, and a prototype has no business writing there --
// nor scavenging there, which is the more dangerous half.

const (
	// stagingAppFolder is deliberately not "Enfold". The scavenger below deletes
	// whole directories under this name, and pointing that at the application's
	// own folder is how a prototype eats a vault. A test holds this name.
	stagingAppFolder = "Enfold-dragproto"
	stagingSubFolder = "drag"
)

// stagingRootIn is the path half of that rule, separated from the environment so
// that a test can check it without a %LOCALAPPDATA% to write in.
func stagingRootIn(localAppData string) string {
	return filepath.Join(localAppData, stagingAppFolder, stagingSubFolder)
}

// stagingRoot is where every drag of this process stages. LOCALAPPDATA is the
// environment variable for FOLDERID_LocalAppData; %TEMP% is deliberately not
// used, for the reason APP.md gives -- a folder of ours is ours to scavenge, it
// is outside what OneDrive backs up, and it is on the volume most drops land on,
// which is what makes a move a rename.
func stagingRoot() (string, error) {
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		return "", fmt.Errorf("LOCALAPPDATA is not set")
	}
	return stagingRootIn(local), nil
}

// newStageID is the <random 8-hex id> of one drag's folder. crypto/rand because
// two drags a millisecond apart must not collide, and because a predictable name
// for a folder that will hold plaintext is a name somebody else can wait for.
func newStageID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand.Read is documented never to fail; if it ever did, a time-based
		// name is still unique enough for a prototype to carry on with.
		return fmt.Sprintf("%08x", uint32(time.Now().UnixNano()))
	}
	return hex.EncodeToString(b[:])
}

// ---------------------------------------------------------------------------
// The manifest.
//
// A scavenger that deletes directories needs to know which directories are its
// own, and an age alone cannot say. So every staging folder carries one small
// JSON file naming the tool that made it, when, which process, what state the
// drag is in and which files it staged. The sweep acts on folders that carry
// this and on nothing else: a folder a user put there by hand, a folder another
// program made, a reparse point pointing anywhere at all -- none of them are
// ours and none of them are touched.

const (
	manifestName    = "manifest.json"
	manifestTool    = "enfold-dragproto"
	manifestVersion = 1

	// The three states APP.md names. "live" is a drag still in progress -- the
	// folder is never swept while it says that; "handed-out" is a drag whose
	// paths a target has been given, which is the state that outlives the drop
	// on purpose, because consumers open the paths late; "done" is a folder
	// whose deletion has been decided, and it is written BEFORE the deletion is
	// attempted so that a deletion which fails still leaves a folder the next
	// sweep will take.
	stateLive      = "live"
	stateHandedOut = "handed-out"
	stateDone      = "done"
)

type stageManifest struct {
	Tool    string    `json:"tool"`
	Version int       `json:"version"`
	PID     int       `json:"pid"`
	Created time.Time `json:"created"`
	State   string    `json:"state"`
	Files   []string  `json:"files"`
}

func writeManifest(root string, m stageManifest) error {
	blob, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, manifestName), append(blob, '\n'), 0o600)
}

// readManifest reads a folder's manifest and says whether it is ours. A folder
// without one, with an unreadable one, or with one some other tool wrote is not
// ours, and the caller must leave it alone.
func readManifest(dir string) (stageManifest, bool) {
	blob, err := os.ReadFile(filepath.Join(dir, manifestName))
	if err != nil {
		return stageManifest{}, false
	}
	var m stageManifest
	if err := json.Unmarshal(blob, &m); err != nil {
		return stageManifest{}, false
	}
	if m.Tool != manifestTool {
		return stageManifest{}, false
	}
	return m, true
}

// ---------------------------------------------------------------------------
// DROPFILES.
//
// The layout is not recalled. DROPFILES declares, field for field:
//
//	typedef struct _DROPFILES {
//	  DWORD pFiles;   // "The offset of the file list from the beginning of this
//	                  //  structure, in bytes."
//	  POINT pt;       // "The drop point. The coordinates depend on fNC."
//	  BOOL  fNC;      // "A nonclient area flag..."
//	  BOOL  fWide;    // "A value that indicates whether the file contains ANSI
//	                  //  or Unicode characters. If the value is zero, the file
//	                  //  contains ANSI characters. Otherwise, it contains
//	                  //  Unicode characters."
//	} DROPFILES, *LPDROPFILES;
//
// and the page describes itself as "Defines the CF_HDROP clipboard format. The
// data that follows is a double null-terminated list of file names."
//
// The byte offsets are NOT on that page -- it has no Remarks and states neither
// a size nor an offset -- so they are derived here from the documented types
// rather than quoted: DWORD is "a 32-bit unsigned integer", BOOL is "typedef int
// BOOL" and int is 32 bits on both Windows architectures, POINT is two LONGs and
// LONG is "a 32-bit signed integer". Nothing in the structure is a pointer, so
// it is 20 bytes on x86 and x64 alike -- pFiles at 0, pt at 4, fNC at 12, fWide
// at 16 -- and the file list starts at the offset pFiles names, which is 20.
// That derivation is what the test pins.
//
// The list itself is the Shell Clipboard Formats page's, and that one is quoted:
// "The file name array consists of a series of strings, each containing one
// file's fully qualified path, including the terminating NULL character. An
// additional null character is appended to the final string to terminate the
// array." So: absolute paths, and a double NUL at the end. The medium is named
// on the scenarios page -- "Set the cfFormat member of the FORMATETC structure
// to CF_HDROP and the tymed member to TYMED_HGLOBAL".
const (
	dropFilesHeaderSize = 20
	dropFilesOffPFiles  = 0
	dropFilesOffPoint   = 4
	dropFilesOffFNC     = 12
	dropFilesOffFWide   = 16
)

// cfHDrop is CF_HDROP. "Unlike the other Shell formats, it is predefined, so
// there is no need to call RegisterClipboardFormat" -- it is a constant in
// winuser.h (CF_HDROP = 15), which is why registerFormats does not touch it.
const cfHDrop = 15

// encodeDropFiles renders the HGLOBAL contents of a CF_HDROP rendering: the
// DROPFILES header followed by the paths as double-NUL-terminated UTF-16.
//
// pt and fNC are left zero on purpose. They are the drop point and the
// nonclient-area flag a WM_DROPFILES receiver reads back out of the block; a
// data object handed to DoDragDrop is not the thing that knows where the cursor
// was, and a source that offers CF_HDROP this way leaves them alone.
func encodeDropFiles(paths []string) []byte {
	list := make([]uint16, 0, 64)
	for _, p := range paths {
		list = append(list, utf16.Encode([]rune(p))...)
		list = append(list, 0)
	}
	// The list's own terminator. With at least one path this is the second half
	// of the documented double NUL; with none it is the whole of an empty list.
	list = append(list, 0)

	buf := make([]byte, dropFilesHeaderSize+2*len(list))
	binary.LittleEndian.PutUint32(buf[dropFilesOffPFiles:], dropFilesHeaderSize)
	// fWide nonzero, which is what the member documents as "it contains Unicode
	// characters" -- 1, the conventional BOOL TRUE, not VARIANT_TRUE's -1. The
	// paths are UTF-16, so a zero here would be a lie about the bytes that follow
	// and a consumer would read them as ANSI.
	binary.LittleEndian.PutUint32(buf[dropFilesOffFWide:], 1)
	for i, u := range list {
		binary.LittleEndian.PutUint16(buf[dropFilesHeaderSize+2*i:], u)
	}
	return buf
}

// encodeDropEffect renders CFSTR_PREFERREDDROPEFFECT's data. The format "is used
// by the source to specify whether its preferred method of data transfer is
// move, copy, or link ... The structure's hGlobal member points to a DWORD
// value", so this is four bytes and nothing else.
func encodeDropEffect(effect uint32) []byte {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], effect)
	return b[:]
}

// ---------------------------------------------------------------------------
// The staging folder, and the state machine over it.

type stageConfig struct {
	// keep leaves the folder behind whatever happens, for inspection.
	keep bool
	// early writes the files at drag start instead of at the first post-release
	// request, so that a consumer which needs real files during the hover can be
	// told apart from one that does not.
	early bool
	// failExtract makes the extraction fail on purpose, to see what the target
	// does with a GetData that returns an error where it expected paths.
	failExtract bool
	// copyOnly restores the old behaviour -- DROPEFFECT_COPY alone, preferred
	// COPY -- for comparison against the move-preferred default.
	copyOnly bool
	// maxAge is -scavenge: how old a manifested folder has to be before a sweep
	// removes it.
	maxAge time.Duration
}

// allowedEffects is what DoDragDrop is told this drag permits, and preferred is
// what CFSTR_PREFERREDDROPEFFECT says the source would rather have.
//
// Move, by default, and this is 7-Zip's practice rather than an oversight: the
// staged file is a disposable copy, so a drop on the same volume -- the desktop,
// from %LOCALAPPDATA% -- becomes one rename with no second pass over the bytes
// and nothing left to clean up, and a drop on another volume is the target's
// copy followed by its own delete of the staging. The record inside the archive
// is never touched by either: a move here moves the temporary, never the
// original, which is the ruling APP.md 3 carries.
func (c stageConfig) allowedEffects() uint32 {
	if c.copyOnly {
		return dropEffectCopy
	}
	return dropEffectCopy | dropEffectMove
}

func (c stageConfig) preferredEffect() uint32 {
	if c.copyOnly {
		return dropEffectCopy
	}
	return dropEffectMove
}

// stagedFile is one file's half of the observation. Nothing here comes from the
// target: an exclusive open says whether somebody has the file open, and the
// file's presence says whether a move has already taken it away. Between them
// they are everything a source learns about a consumer that never calls
// EndOperation -- which, for a CF_HDROP source, may well be every consumer.
type stagedFile struct {
	path     string
	exists   bool
	seen     bool // it existed at least once, so a later absence is a removal
	inUse    bool
	everUsed bool
	gone     bool
	// since is when the current state began, for the "in use for 3.2s" half of
	// the transition lines.
	since time.Time
}

type formatAsk struct {
	kind   string // "GetData" or "QueryGetData"
	format string
	when   string // relative to the button's release
}

type dragStage struct {
	root  string // <parent>\<8 hex id>
	files []synthFile
	paths []string // the files on disk, in the order they are offered
	// drop is what CF_HDROP names. It is paths, except under -folder, where it
	// is the one subfolder that contains them: CF_HDROP has no other way to say
	// "a folder" -- the list is paths, and a path that names a directory is the
	// directory, with everything in it.
	drop []string
	cfg  stageConfig

	// extractMu serialises the extraction itself, and is deliberately not mu:
	// the extraction runs for as long as the files take (a -delay 200 run over
	// 512 MiB is a minute and a half) and everything else about the stage -- the
	// counters, the watcher, the cleanup decision, the close guard -- has to
	// stay answerable while it does.
	extractMu sync.Mutex

	mu         sync.Mutex
	created    time.Time
	state      string
	requests   int // GetData(CF_HDROP) calls, all of them
	early      int // ... of which arrived before the button came up
	armed      bool
	releasedAt time.Time
	extracted  bool
	failed     bool
	failErr    error
	written    bool
	handedOut  bool
	asyncOp    bool // StartOperation was called: the target negotiated the protocol
	endOp      bool
	dragOver   bool
	forced     bool
	deleted    bool
	watching   bool
	watch      []stagedFile
	asks       []formatAsk
	lastWhy    string
	bytes      int64
	extractDur time.Duration

	// looked is whether the watch has already reported its first look, and
	// expectStaged whether the extraction had written every staged file by the
	// time the watch began. The second one is what makes a file that is missing
	// at the very first tick a removal -- the target renamed it away inside those
	// 250 ms -- rather than a file that was never written at all.
	looked       bool
	expectStaged bool

	// removed is the folder actually being gone from disk, and it is deliberately
	// not the same fact as deleted: deleted means the stage is resolved and its
	// watcher stopped, which a forced close whose delete FAILED also is. Only
	// removed says there is nothing left, and removeErr is why there is.
	removed   bool
	removeErr error

	stopOnce sync.Once
	stop     chan struct{}
}

// liveStages is every stage this process still watches, by folder -- every one,
// not the drag that happened to be last. The sweeper consults it so that the
// ten-minute sweep never races the watcher of a folder this run is still looking
// after, and both close paths walk it so that a drag left behind by an earlier
// gesture is cleaned up rather than forgotten. A stage leaves this set the
// moment its fate is decided, so that a folder whose delete failed is the
// sweep's to take from then on.
var liveStages = struct {
	mu sync.Mutex
	m  map[string]*dragStage
}{m: make(map[string]*dragStage)}

// allStages is every stage this process ever made, in the order it made them.
// Unlike liveStages it keeps them after they are resolved, because the exit
// summary has to be able to say what became of each -- above all of one whose
// folder is still on disk because a delete failed.
var allStages = struct {
	mu   sync.Mutex
	list []*dragStage
}{}

func knownStages() []*dragStage {
	allStages.mu.Lock()
	defer allStages.mu.Unlock()
	return append([]*dragStage(nil), allStages.list...)
}

// newDragStage makes the folder, writes the manifest and works out the paths,
// all BEFORE DoDragDrop -- which is the whole point of the early half of the
// protocol, and the reason a hover-time request can be answered with the final
// names.
//
// parent is a parameter rather than stagingRoot() so that a test can point the
// whole mechanism at t.TempDir(): nothing in the test suite may write under
// %LOCALAPPDATA%.
func newDragStage(parent string, files []synthFile, cfg stageConfig) (*dragStage, error) {
	id := newStageID()
	root := filepath.Join(parent, id)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("creating the staging folder: %w", err)
	}
	s := &dragStage{
		root:    root,
		files:   files,
		cfg:     cfg,
		created: time.Now(),
		state:   stateLive,
		stop:    make(chan struct{}),
	}
	folders := make(map[string]bool)
	for i := range files {
		// filepath.Join cleans the descriptor name's own separator, which is how
		// -folder's "Proto\name" becomes a real subfolder rather than a file with
		// a backslash in its name.
		p := filepath.Join(root, files[i].name)
		s.paths = append(s.paths, p)
		if dir := filepath.Dir(p); dir != root {
			folders[dir] = true
		}
	}
	if len(folders) > 0 {
		// A folder drag: name the folders, not the files inside them. Naming the
		// files would drop them loose into the destination and the subfolder
		// would never appear there at all, which would make -folder invisible in
		// the very result it is meant to show.
		for d := range folders {
			s.drop = append(s.drop, d)
		}
		sort.Strings(s.drop)
	} else {
		s.drop = append(s.drop, s.paths...)
	}
	rel := make([]string, 0, len(s.paths))
	for _, p := range s.paths {
		if r, err := filepath.Rel(root, p); err == nil {
			rel = append(rel, r)
		}
	}
	if err := writeManifest(root, stageManifest{
		Tool:    manifestTool,
		Version: manifestVersion,
		PID:     os.Getpid(),
		Created: s.created,
		State:   stateLive,
		Files:   rel,
	}); err != nil {
		return nil, fmt.Errorf("writing the manifest: %w", err)
	}
	liveStages.mu.Lock()
	liveStages.m[root] = s
	liveStages.mu.Unlock()
	allStages.mu.Lock()
	allStages.list = append(allStages.list, s)
	allStages.mu.Unlock()
	return s, nil
}

// setState moves the manifest on. Every transition is written to disk, because
// the state is only worth anything to a sweep that runs after this process has
// gone.
func (s *dragStage) setState(state string) {
	s.mu.Lock()
	if s.state == state || s.deleted {
		s.mu.Unlock()
		return
	}
	s.state = state
	s.mu.Unlock()

	if err := s.persistManifest(state); err != nil {
		logf("staging: could not update the manifest to %q: %v", state, err)
		return
	}
	logf("staging: manifest state -> %q", state)
}

// persistManifest writes this stage's manifest out in the given state. It is
// separate from setState because the manifest has to be writable again after a
// delete that only half succeeded, where the state has not changed at all.
func (s *dragStage) persistManifest(state string) error {
	s.mu.Lock()
	created := s.created
	paths := append([]string(nil), s.paths...)
	s.mu.Unlock()

	rel := make([]string, 0, len(paths))
	for _, p := range paths {
		if r, err := filepath.Rel(s.root, p); err == nil {
			rel = append(rel, r)
		}
	}
	return writeManifest(s.root, stageManifest{
		Tool:    manifestTool,
		Version: manifestVersion,
		PID:     os.Getpid(),
		Created: created,
		State:   state,
		Files:   rel,
	})
}

// pathList is what a CF_HDROP rendering names right now. It never changes: the
// whole trick of delayed rendering is that the names are decided before the
// bytes exist, and a name once handed out is a name a consumer may have cached.
func (s *dragStage) pathList() []string {
	out := make([]string, len(s.drop))
	copy(out, s.drop)
	return out
}

// arm is the button coming up, from IDropSource::QueryContinueDrag. From here on
// the next CF_HDROP request is the drop's own and runs the extraction.
func (s *dragStage) arm() {
	s.mu.Lock()
	already := s.armed
	s.armed = true
	if !already {
		s.releasedAt = time.Now()
	}
	early := s.early
	s.mu.Unlock()
	if already {
		return
	}
	logf("staging: the button was released -- the extraction is armed (%d early CF_HDROP request(s) so far)", early)
}

// timing is how a format request is placed against the button's release, which
// is the axis every question about this mode is asked on.
func (s *dragStage) timing() string {
	s.mu.Lock()
	armed, at := s.armed, s.releasedAt
	s.mu.Unlock()
	if !armed {
		return "during the hover, the button still down"
	}
	return fmt.Sprintf("+%s after the release", time.Since(at).Round(time.Millisecond))
}

// noteFormat records a format the target asked about, with its timing. The list
// is bounded: a drag that asks ten thousand times is a finding in itself, and
// the counter says so without keeping ten thousand entries.
func (s *dragStage) noteFormat(kind, format string) {
	when := s.timing()
	s.mu.Lock()
	if len(s.asks) < 200 {
		s.asks = append(s.asks, formatAsk{kind: kind, format: format, when: when})
	}
	s.mu.Unlock()
}

// requestPaths is the state machine, and the one function that decides what a
// GetData(CF_HDROP) means. It returns the paths to render, a word for the log,
// and the HRESULT GetData should answer with.
//
// The failure code is E_UNEXPECTED, not E_FAIL, and that is checked rather than
// chosen: GetData's documented return values are DV_E_LINDEX, DV_E_FORMATETC,
// DV_E_TYMED, DV_E_DVASPECT, OLE_E_NOTRUNNING, STG_E_MEDIUMFULL, E_UNEXPECTED,
// E_INVALIDARG and E_OUTOFMEMORY. E_FAIL is not among them, and none of the
// DV_E_* codes is true here -- the FORMATETC was perfectly valid, the rendering
// is what failed -- so E_UNEXPECTED ("An unexpected error has occurred") is the
// documented way to say so. STG_E_MEDIUMFULL stays what the Notes to Implementers
// reserve it for: "If an attempt to allocate the medium fails".
func (s *dragStage) requestPaths() ([]string, string, uintptr) {
	s.mu.Lock()
	s.requests++
	n := s.requests
	armed := s.armed
	done := s.extracted
	failed := s.failed
	if !armed {
		s.early++
		early := s.early
		s.mu.Unlock()
		logf("early CF_HDROP request answered with future paths (request #%d, %d early so far; the files do not exist yet, and these are their FINAL names)", n, early)
		s.markHandedOut()
		return s.pathList(), "future paths", sOK
	}
	s.mu.Unlock()

	if failed {
		logf("CF_HDROP request #%d after a failed extraction -> E_UNEXPECTED (the names are not handed out over files that are not there)", n)
		return nil, "nothing: the extraction failed", eUnexpected
	}
	if done {
		logf("CF_HDROP request #%d after the extraction: the same paths again, nothing rewritten", n)
		s.handedOutAfterRelease()
		return s.pathList(), "the staged paths again", sOK
	}

	// Serialised, so that two requests racing in after the release -- which
	// -agile makes possible, on two of the target's threads -- extract once and
	// the second one waits for the first rather than writing over it.
	s.extractMu.Lock()
	defer s.extractMu.Unlock()
	s.mu.Lock()
	done, failed = s.extracted, s.failed
	s.mu.Unlock()
	if failed {
		logf("CF_HDROP request #%d: the extraction had already failed -> E_UNEXPECTED", n)
		return nil, "nothing: the extraction failed", eUnexpected
	}
	if done {
		logf("CF_HDROP request #%d: the extraction had already run (another request got there first); the same paths", n)
		s.handedOutAfterRelease()
		return s.pathList(), "the staged paths again", sOK
	}

	logf("CF_HDROP request #%d is the first after the release (%s): extracting into the staging folder, inside GetData, on %s",
		n, s.timing(), threadWord())
	err := s.runExtraction()
	if err != nil {
		return nil, "nothing: the extraction failed", eUnexpected
	}
	s.handedOutAfterRelease()
	return s.pathList(), "the paths of the files just written", sOK
}

// handedOutAfterRelease is markHandedOut plus the one thing that is only true
// after the release: the target now holds paths to files that exist, and from
// here the copy is its business. That is -disable's moment.
func (s *dragStage) handedOutAfterRelease() {
	s.markHandedOut()
	holdWindowAfterHandover("the post-release GetData has handed the staged paths back; from here the copy is the target's")
}

// runExtraction writes the files and records what happened. The caller holds
// extractMu.
func (s *dragStage) runExtraction() error {
	// The extraction is a phase of its own for -postprobe: it is the one place
	// the target is blocked inside a call of ours, so a posted message that
	// does not get through here is a different finding from one that does not
	// get through during the drop.
	enterExtraction()
	defer leaveExtraction()
	// One probe at the start, so that an extraction shorter than 64 MiB still
	// asks the question at least once.
	postProbe(srcExtract)

	start := time.Now()
	err := s.writeFiles()
	took := time.Since(start)
	s.mu.Lock()
	s.extracted = true
	s.extractDur = took
	if err != nil {
		s.failed = true
		s.failErr = err
	}
	bytes := s.bytes
	s.mu.Unlock()
	if err != nil {
		logf("staging: the extraction FAILED after %s and %d bytes: %v -- GetData will fail rather than name files that are not there",
			took.Round(time.Millisecond), bytes, err)
		return err
	}
	logf("staging: the extraction wrote %d file(s), %d bytes in %s -- the target waited that long inside GetData, on %s",
		len(s.paths), bytes, took.Round(time.Millisecond), threadWord())
	return nil
}

// markHandedOut is the manifest transition that matters most to the scavenger: a
// target has been given paths, so the folder has to outlive the drop, and only
// the sweep may take it from here.
func (s *dragStage) markHandedOut() {
	s.mu.Lock()
	first := !s.handedOut
	s.handedOut = true
	s.mu.Unlock()
	if first {
		s.setState(stateHandedOut)
	}
}

// stageEarly is -stage-early: write the files at drag start instead. The state
// machine then finds the extraction already done, so the drop's own request
// returns immediately -- which is the comparison the flag is for.
func (s *dragStage) stageEarly() {
	s.extractMu.Lock()
	defer s.extractMu.Unlock()
	logf("-stage-early: writing the files before DoDragDrop, on %s", threadWord())
	s.runExtraction()
}

// writeFiles is the "extraction": the same generator the virtual streams use,
// through a fixed buffer, into the staging folder. Bounded memory is the point
// it shares with the stream route -- a 5 GiB file must cost one chunk, not
// 5 GiB -- and -delay per MiB stands in for a slow decrypt, here on the caller's
// thread rather than on a producer goroutine, because that is where a real
// extraction inside GetData would run and how long the target waits for it is
// the measurement.
func (s *dragStage) writeFiles() error {
	buf := make([]byte, producerChunk)
	for i := range s.files {
		if err := s.writeOne(&s.files[i], s.paths[i], buf); err != nil {
			return err
		}
	}
	return nil
}

// writeOne is one file's half of the extraction. It is a method of its own only
// because a -file source needs a handle whose lifetime is this file's and not
// the whole set's.
func (s *dragStage) writeOne(f *synthFile, path string, buf []byte) error {
	if dir := filepath.Dir(path); dir != s.root {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}

	// Where the bytes come from: the generator for a synthetic file, the -file
	// source for a real one. Everything else about the write is the same either
	// way -- the one fixed buffer, the delay per MiB, the progress lines, the
	// failure injection -- because what is being measured is the write and the
	// target's wait for it, not the producer.
	fill := func(off int64, dst []byte) error {
		patternAt(f.seed, off, dst)
		return nil
	}
	if f.source != "" {
		src, err := openSourceForReading(f.source)
		if err != nil {
			return fmt.Errorf("opening the -file source %s: %w", f.source, err)
		}
		defer src.Close()
		fill = func(off int64, dst []byte) error {
			if _, err := io.ReadFull(src, dst); err != nil {
				return fmt.Errorf("reading %s at offset %d: %w (the source changed under the drag)", f.source, off, err)
			}
			return nil
		}
		logf("staging: %s is a copy of %s (%d bytes); the source is opened for reading only and is never written to, renamed or deleted",
			filepath.Base(path), f.source, f.size)
	}

	start := time.Now()
	fh, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}
	// Marked written before a single byte goes in: a file that exists at all
	// is plaintext somebody has to delete, and the cleanup has to know about
	// it even if the write below fails halfway.
	s.mu.Lock()
	s.written = true
	s.mu.Unlock()

	var off int64
	nextMark := int64(readThrottleBytes)
	for off < f.size {
		n := int64(len(buf))
		if r := f.size - off; r < n {
			n = r
		}
		// -fail-extract fails partway through the first file on purpose: a
		// failure that leaves half a file behind is the case worth seeing,
		// both for what Explorer shows and for what the cleanup does with it.
		if s.cfg.failExtract && off > 0 {
			fh.Close()
			return fmt.Errorf("-fail-extract: simulated failure after %d bytes of %s", off, filepath.Base(path))
		}
		if err := fill(off, buf[:n]); err != nil {
			fh.Close()
			return err
		}
		if f.delay > 0 {
			time.Sleep(time.Duration(float64(f.delay) * float64(n) / float64(1<<20)))
		}
		if _, err := fh.Write(buf[:n]); err != nil {
			fh.Close()
			return fmt.Errorf("writing %s: %w", path, err)
		}
		off += n
		s.mu.Lock()
		s.bytes += n
		s.mu.Unlock()
		if off >= nextMark {
			logf("staging: %s -- %d of %d bytes (%.0f%%), %s elapsed",
				filepath.Base(path), off, f.size, 100*float64(off)/float64(f.size),
				time.Since(start).Round(time.Millisecond))
			// One probe per 64 MiB, from whatever thread the extraction is on
			// -- with -agile that is Explorer's, which is the interesting case:
			// a message posted from the target's own thread to our window,
			// while the target is blocked inside this call.
			postProbe(srcExtract)
			for nextMark <= off {
				nextMark += readThrottleBytes
			}
		}
	}
	if err := fh.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", path, err)
	}
	// A copy of a real file carries the original's modification time, so that
	// what the target ends up with is the file it was offered rather than a file
	// that was made during the drag. The source itself is not touched.
	if !f.modTime.IsZero() {
		if err := os.Chtimes(path, f.modTime, f.modTime); err != nil {
			logf("staging: could not give %s the source's modification time: %v", path, err)
		}
	}
	logf("staging: wrote %s (%d bytes) in %s", path, f.size, time.Since(start).Round(time.Millisecond))
	return nil
}

// ---------------------------------------------------------------------------
// Watching the consumer, without being told anything by it.
//
// EndOperation is the only thing a source is ever told, and nothing proved that
// Explorer negotiates the asynchronous protocol for a CF_HDROP source at all --
// which is one of the questions this mode exists to answer. So the files are
// watched directly: an exclusive open fails while somebody else has the file
// open, and a file that has disappeared has been moved away by a target that
// took the drag as a move. Neither needs the target's cooperation.

// stagedFileInUse opens the file with dwShareMode 0, documented as "Prevents
// subsequent open operations on a file or device if they request delete, read,
// or write access" -- and the other direction is the one this uses: "You cannot
// request a sharing mode that conflicts with the access mode that is specified
// in an existing request that has an open handle. CreateFile would fail and the
// GetLastError function would return ERROR_SHARING_VIOLATION." So the request
// fails exactly while somebody else has the file open, whoever they are ("the
// sharing options for each open handle remain in effect until that handle is
// closed, regardless of process context"). That failure is the measurement.
func stagedFileInUse(path string) (inUse bool, exists bool, err error) {
	p, perr := windows.UTF16PtrFromString(path)
	if perr != nil {
		return false, false, perr
	}
	h, cerr := windows.CreateFile(p, windows.GENERIC_READ, 0, nil,
		windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if cerr == nil {
		windows.CloseHandle(h)
		return false, true, nil
	}
	switch cerr {
	case windows.ERROR_SHARING_VIOLATION:
		return true, true, nil
	case windows.ERROR_FILE_NOT_FOUND, windows.ERROR_PATH_NOT_FOUND:
		return false, false, nil
	}
	// ERROR_ACCESS_DENIED and the rest: not a sharing answer, so it says nothing
	// about the consumer. Report it and let the caller log it.
	return false, true, cerr
}

// startWatch begins the 250 ms poll over the staged files. It also drives the
// cleanup decision, on its own goroutine, so that a decision taken while the
// target is inside EndOperation is never carried out on the target's thread --
// deleting 5 GiB inside a call the target is waiting to return from would be a
// stall this prototype caused.
func (s *dragStage) startWatch() {
	if !s.armWatch() {
		return
	}
	go s.watchLoop()
}

// armWatch is startWatch without the goroutine: it sets the watch up and says
// whether there is one to run. Separated so that a test can drive poll by hand
// at the moment of its choosing -- the first tick above all, which is where the
// interesting transition turned out to be.
func (s *dragStage) armWatch() bool {
	s.mu.Lock()
	if s.watching || s.deleted {
		s.mu.Unlock()
		return false
	}
	s.watching = true
	now := time.Now()
	s.watch = make([]stagedFile, 0, len(s.paths))
	for _, p := range s.paths {
		s.watch = append(s.watch, stagedFile{path: p, since: now})
	}
	// Whether every staged file was on disk when the watch began. It is snapshot
	// here rather than read at each tick because it is a statement about the
	// past: it is what makes an absence at the first tick a removal.
	s.expectStaged = s.extracted && !s.failed && s.written
	staged := s.expectStaged
	s.mu.Unlock()
	logf("staging: watching %d staged file(s) every 250 ms with an exclusive open; "+
		"\"in use by another process\" is somebody reading it, \"gone\" is somebody moving it; "+
		"the extraction had written every one of them: %v", len(s.paths), staged)
	return true
}

func (s *dragStage) watchLoop() {
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			if s.poll(time.Now()) {
				return
			}
		}
	}
}

// fileProbe is one exclusive open's answer about a staged file. It is a value
// rather than a call inside the transition logic so that the whole table --
// including the first observation, which is where this went wrong -- can be
// tested without a file system and without a drag.
type fileProbe struct {
	exists bool
	inUse  bool
}

// lookWord is how a file looked the first time the watch saw it, for the one
// line per stage that says what the watch started from.
func lookWord(p fileProbe) string {
	switch {
	case !p.exists:
		return "missing"
	case p.inUse:
		return "present, in use by another process"
	default:
		return "present, free"
	}
}

// observe folds one probe into a watched file's state and returns the lines the
// transition is worth. It mutates w and nothing else.
//
// staged says the extraction had written every staged file before the watch
// began, and it is what makes the FIRST observation a transition like any other.
// That is a defect turned into behaviour: a same-volume drop is a rename, and
// Explorer can finish it inside the 250 ms before the first tick -- in the round
// that found this, the desktop copy's mtime was the staged file's to the
// millisecond. The earlier shape had no case for "missing the first time it was
// looked at", so it said nothing, the file never counted as gone, and the stage
// sat in "handed-out" over an empty folder until the scavenge took it an hour
// later. A completed move is the cleanest end a drag has, and it has to be
// recognised as one.
func (w *stagedFile) observe(p fileProbe, staged bool, now time.Time) []string {
	var lines []string
	if !w.seen {
		switch {
		case p.exists:
			// The ordinary start. No transition line: the first look is reported
			// once per stage, for every file at once, by the caller.
			w.seen, w.exists, w.since = true, true, now
			w.inUse = p.inUse
			if p.inUse {
				w.everUsed = true
			}
			return nil
		case staged:
			w.seen, w.gone, w.exists, w.since = true, true, false, now
			return append(lines, fmt.Sprintf(
				"staged file GONE (moved away by the target) before the first poll tick: %s "+
					"-- the extraction had written it, so the target took it within the first 250 ms", w.path))
		default:
			// Nothing was ever written here: the drag ended before the extraction
			// ran, or it failed short of this file. An absence is not a removal,
			// and there is nothing to conclude from it.
			return nil
		}
	}
	switch {
	case p.exists && w.gone:
		// It came back: a target that moved the file and put it back, or a
		// probe that raced a rename. Say so rather than leave the stage
		// believing there is nothing left to clean up.
		lines = append(lines, fmt.Sprintf("staged file is back: %s", w.path))
		w.gone, w.exists, w.since = false, true, now
	case !p.exists && !w.gone:
		// A file that was there and is not any more was taken, not closed:
		// this is what a same-volume move looks like from the source's side,
		// and it is the one outcome that leaves nothing to clean up.
		lines = append(lines, fmt.Sprintf("staged file GONE (moved away by the target): %s (it had existed for %s)",
			w.path, now.Sub(w.since).Round(time.Millisecond)))
		w.gone, w.exists, w.inUse, w.since = true, false, false, now
		return lines
	case !p.exists:
		// Gone, and still gone.
		return lines
	}
	if p.inUse != w.inUse {
		if p.inUse {
			lines = append(lines, fmt.Sprintf("staged file in use by another process: %s (free for %s before this)",
				w.path, now.Sub(w.since).Round(time.Millisecond)))
			w.everUsed = true
		} else {
			lines = append(lines, fmt.Sprintf("staged file free: %s (in use for %s)",
				w.path, now.Sub(w.since).Round(time.Millisecond)))
		}
		w.inUse = p.inUse
		w.since = now
	}
	return lines
}

// firstLookLine is the one line per stage that says what the watch found the
// first time it looked. Without it a log that goes straight from "watching 1
// staged file(s)" to a cleanup decision never says whether the file was there.
func firstLookLine(root string, looks []string) string {
	const shown = 8
	if len(looks) > shown {
		looks = append(append([]string(nil), looks[:shown]...),
			fmt.Sprintf("and %d more", len(looks)-shown))
	}
	return fmt.Sprintf("staging: the watch's first look at %s: %s", root, strings.Join(looks, "; "))
}

// poll probes every staged file, logs the transitions, and then asks the policy
// what to do. It returns true when the stage is finished with and the loop
// should end.
func (s *dragStage) poll(now time.Time) bool {
	var lines, looks []string
	s.mu.Lock()
	first := !s.looked
	s.looked = true
	staged := s.expectStaged
	for i := range s.watch {
		w := &s.watch[i]
		inUse, exists, err := stagedFileInUse(w.path)
		if err != nil {
			lines = append(lines, fmt.Sprintf("staging: probing %s failed: %v (that says nothing about the consumer)", w.path, err))
			if first {
				looks = append(looks, fmt.Sprintf("%s: could not be probed", filepath.Base(w.path)))
			}
			continue
		}
		p := fileProbe{exists: exists, inUse: inUse}
		if first {
			looks = append(looks, fmt.Sprintf("%s: %s", filepath.Base(w.path), lookWord(p)))
		}
		lines = append(lines, w.observe(p, staged, now)...)
	}
	ev := s.eventsLocked(now)
	s.mu.Unlock()

	if first {
		logf("%s", firstLookLine(s.root, looks))
	}
	for _, l := range lines {
		logf("%s", l)
	}
	return s.applyDecision(decideStageCleanup(ev), now)
}

// eventsLocked is the snapshot the policy decides over. Everything in it is a
// fact that was recorded, which is what makes the policy a pure function of them
// and a test of the decision table possible without a drag.
func (s *dragStage) eventsLocked(now time.Time) stageEvents {
	ev := stageEvents{
		keep:          s.cfg.keep,
		written:       s.written,
		handedOut:     s.handedOut,
		extractFailed: s.failed,
		endOperation:  s.endOp,
		asyncOp:       s.asyncOp,
		dragOver:      s.dragOver,
		forcedClose:   s.forced,
		age:           now.Sub(s.created),
		maxAge:        s.cfg.maxAge,
	}
	// anyLeft has to be pessimistic where it does not know: it is what the
	// "everything was moved away" arm of the policy turns on, and deciding that
	// nothing is left because nothing has been looked at yet would delete a
	// folder that is full.
	ev.anyLeft = s.written
	if len(s.watch) > 0 && s.written {
		ev.anyLeft = false
		for i := range s.watch {
			if s.watch[i].inUse {
				ev.anyInUse = true
			}
			if !s.watch[i].gone {
				ev.anyLeft = true
			}
		}
	}
	return ev
}

// ---------------------------------------------------------------------------
// The cleanup policy.
//
// Staged plaintext has to go, and the only question is when. The third research
// pass settled it against the obvious answer: deleting when the drop looks over
// is what took files from under FileZilla, VMware and a configuration dialog
// that kept 7-Zip's paths for minutes, and Igor Pavlov's own note on it is
// "another program can't open input files in that case". A currently unlocked
// file says nothing about whether a consumer reopens it later. So the folder
// outlives the drop on purpose, and the sweep is what takes it.
//
// It is a pure function over recorded events on purpose: the real thing will
// have to defend this table, and a table that can only be exercised by dragging
// a file onto the desktop cannot be defended at all.

type stageEvents struct {
	keep bool
	// written says anything at all reached the disk. An empty staging folder is
	// nobody's plaintext, so it goes the moment the drag is over.
	written bool
	// handedOut is the fact the whole policy turns on: a target has been given
	// these paths and may open them at any time from now on.
	handedOut     bool
	extractFailed bool
	endOperation  bool
	asyncOp       bool
	dragOver      bool
	forcedClose   bool
	anyInUse      bool
	// anyLeft is false once every staged file has gone -- moved away by the
	// target, which is what a same-volume move does.
	anyLeft bool
	age     time.Duration
	maxAge  time.Duration
}

type stageAction int

const (
	stageWait stageAction = iota
	stageDelete
	stageForceDelete // delete, and record what MOVEFILE_DELAY_UNTIL_REBOOT does with the rest
	stageKeepForever
)

type stageDecision struct {
	action stageAction
	why    string
}

func (a stageAction) String() string {
	switch a {
	case stageWait:
		return "wait"
	case stageDelete:
		return "delete"
	case stageForceDelete:
		return "delete, and try MOVEFILE_DELAY_UNTIL_REBOOT on what will not go"
	case stageKeepForever:
		return "keep"
	}
	return fmt.Sprintf("action %d", int(a))
}

// decideStageCleanup is the whole policy.
//
// -keep wins over everything, the forced close included: the flag exists so that
// a run can be inspected afterwards, and a forced close is precisely the run
// worth inspecting. The log says so rather than quietly leaving plaintext.
func decideStageCleanup(e stageEvents) stageDecision {
	switch {
	case e.keep:
		return stageDecision{stageKeepForever, "-keep: the staging folder is left where it is, for inspection"}

	case e.forcedClose:
		// The window is going away. Delete what can be deleted, and record what
		// delete-at-reboot does with the rest -- recorded, not relied on.
		return stageDecision{stageForceDelete, "a forced close: the window is going away while the staged files are still in use"}

	case e.dragOver && e.extractFailed:
		// Half-written files, and a GetData that failed rather than naming them.
		// Nothing valid was handed out, so nothing is waiting for them.
		return stageDecision{stageDelete, "the extraction failed, so nothing usable was ever handed out"}

	case e.dragOver && !e.written:
		// Escape, or a drop nothing accepted: the extraction never ran, so the
		// folder is empty whatever was asked for during the hover. A consumer
		// that cached a path from the hover has nothing to lose here -- the file
		// it names was never written.
		return stageDecision{stageDelete, "the drag ended and nothing was ever written"}

	case e.dragOver && !e.handedOut:
		// Files on disk (only -stage-early can do that) that no target was ever
		// given the names of. 7-Zip's evidence says targets usually do ask during
		// the hover, so this case is the interesting negative.
		return stageDecision{stageDelete, "the drag ended with nothing handed out"}

	case e.dragOver && e.handedOut && !e.anyLeft:
		// A same-volume move: the target renamed every staged file away, so the
		// folder is empty of everything but the manifest.
		return stageDecision{stageDelete, "every staged file was moved away by the target; only the empty folder is left"}

	case e.endOperation && e.asyncOp && !e.anyInUse:
		// The documented end of an asynchronous transfer -- and it ends *that
		// transfer*, not every later use of the paths by whatever the target was.
		// It is still the strongest signal a source is given, and APP.md 3 takes
		// it as one.
		return stageDecision{stageDelete, "EndOperation on a negotiated asynchronous transfer: the target's transfer is over"}

	case e.anyInUse:
		return stageDecision{stageWait, "a staged file is in use by another process"}

	case e.handedOut && e.age > e.maxAge:
		return stageDecision{stageDelete, fmt.Sprintf("the scavenge rule: handed out, %s old, past the %s limit", e.age.Round(time.Second), e.maxAge)}

	case e.handedOut:
		return stageDecision{stageWait, "the paths were handed out; the folder outlives the drop until the scavenge takes it, because consumers open them late"}
	}
	return stageDecision{stageWait, "waiting for the target to ask for the files"}
}

// applyDecision logs every decision that is not the one already in force, and
// carries out the ones that are not "wait". It returns true when there is
// nothing left to watch.
func (s *dragStage) applyDecision(d stageDecision, now time.Time) bool {
	s.mu.Lock()
	if s.deleted {
		s.mu.Unlock()
		return true
	}
	changed := d.why != s.lastWhy
	s.lastWhy = d.why
	s.mu.Unlock()

	if changed {
		logf("staging cleanup: %s -- %s", d.action, d.why)
	}
	switch d.action {
	case stageWait:
		return false
	case stageKeepForever:
		return true
	case stageDelete:
		return s.remove(false)
	case stageForceDelete:
		return s.remove(true)
	}
	return false
}

// remove deletes the staging folder and reports whether the stage is finished
// with. The manifest is moved to "done" first, on purpose: a deletion that fails
// then leaves behind a folder the next sweep -- this run's, or a later launch's
// -- will recognise as abandoned and take.
func (s *dragStage) remove(force bool) bool {
	s.setState(stateDone)
	logf("staging: deleting %s (forced=%v)", s.root, force)
	err := removeTreeNoReparse(s.root)
	if err == nil {
		s.mu.Lock()
		s.removed, s.removeErr = true, nil
		s.mu.Unlock()
		s.finish()
		logf("staging folder deleted: %s", s.root)
		return true
	}
	s.mu.Lock()
	s.removeErr = err
	s.mu.Unlock()
	// The error verbatim, as Windows gave it: a sharing violation here names the
	// one thing that stands between a forced exit and a folder full of
	// plaintext, and a summary of it would not.
	logf("staging folder could NOT be deleted: %s: %v", s.root, err)
	if busy := stagedTreeInUse(s.root); busy != "" {
		logf("staging: %s is still open by another process, which is why the delete failed", busy)
	}
	// A delete that only half succeeded usually takes the manifest with it --
	// os.ReadDir is sorted, so manifest.json goes before or after the payload
	// depending on nothing but its name -- and a folder without a manifest of
	// ours is a folder the sweep is forbidden to touch ever again. That would
	// turn a failed delete into plaintext left on disk for good, so the manifest
	// goes back, in "done", which is exactly what the sweep looks for.
	if s.onDisk() {
		if _, ok := readManifest(s.root); !ok {
			if werr := s.persistManifest(stateDone); werr != nil {
				logf("staging: %s has lost its manifest to the partial delete and it could not be rewritten (%v): the scavenge will not recognise the folder",
					s.root, werr)
			} else {
				logf("staging: the partial delete took the manifest of %s with it; it has been rewritten as %q so the scavenge still recognises the folder",
					s.root, stateDone)
			}
		}
	}
	if !force {
		// Left for the sweep. Nothing is retried here every 250 ms: a consumer
		// holding a file open would produce the same line four times a second,
		// and the folder is now manifested "done", which is exactly what the
		// sweep looks for.
		logf("staging: %s is left manifested %q for the scavenge", s.root, stateDone)
		s.finish()
		return true
	}
	s.tryDeleteAtReboot()
	s.finish()
	return true
}

// finish takes the stage out of the live set and stops its watcher.
func (s *dragStage) finish() {
	s.mu.Lock()
	s.deleted = true
	s.mu.Unlock()
	liveStages.mu.Lock()
	delete(liveStages.m, s.root)
	liveStages.mu.Unlock()
	s.stopOnce.Do(func() { close(s.stop) })
}

// tryDeleteAtReboot is the last resort, and it is recorded rather than relied
// on: MoveFileExW(path, NULL, MOVEFILE_DELAY_UNTIL_REBOOT). The flag is
// documented as "The system does not move the file until the operating system is
// restarted", and the NULL is the delete: "If dwFlags specifies
// MOVEFILE_DELAY_UNTIL_REBOOT and lpNewFileName is NULL, MoveFileEx registers
// the lpExistingFileName file to be deleted when the system restarts."
//
// Three documented facts shape what this does, and all three are why every
// result is logged verbatim rather than assumed. The privilege: the flag "can be
// used only if the process is in the context of a user who belongs to the
// administrators group or the LocalSystem account" -- an ordinary run of this
// prototype is neither, so a refusal is the expected answer, and recording
// exactly what an unelevated process gets is the point of calling it at all.
// Directories: "The system deletes a directory that is tagged for deletion with
// the MOVEFILE_DELAY_UNTIL_REBOOT flag only if it is empty ... The move and
// deletion operations are carried out at boot time in the same order that they
// are specified", hence every file first and the directories deepest-first. And
// what success means: "the return value cannot reflect success or failure in
// moving or deleting the file. Rather, it reflects success or failure in placing
// the appropriate entries into the registry" (PendingFileRenameOperations) -- so
// a success logged here is a registration, not a deletion.
//
// This is why the policy above does not lean on it. Chromium's use of it is, as
// DECISIONS.md puts it, an attempt rather than a guarantee; the sweep is the
// mechanism, and this is the note in the margin.
func (s *dragStage) tryDeleteAtReboot() {
	var dirs []string
	err := filepath.WalkDir(s.root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			logf("staging: walking %s: %v", path, err)
			return nil
		}
		if d.IsDir() {
			if isReparsePoint(path) {
				logf("staging: %s is a reparse point; not descending into it", path)
				return filepath.SkipDir
			}
			dirs = append(dirs, path)
			return nil
		}
		deleteAtReboot(path)
		return nil
	})
	if err != nil {
		logf("staging: walking the staging folder failed: %v", err)
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		deleteAtReboot(dirs[i])
	}
}

// deleteAtReboot is the call above, behind a variable for one reason: a test of
// the forced close has to be able to see that the attempt was made without the
// test machine ending up with real PendingFileRenameOperations entries for its
// temporary directories.
var deleteAtReboot = logDeleteAtReboot

func logDeleteAtReboot(path string) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		logf("staging: MoveFileExW(%s, NULL, MOVEFILE_DELAY_UNTIL_REBOOT) not attempted: %v", path, err)
		return
	}
	if err := windows.MoveFileEx(p, nil, windows.MOVEFILE_DELAY_UNTIL_REBOOT); err != nil {
		logf("staging: MoveFileExW(%s, NULL, MOVEFILE_DELAY_UNTIL_REBOOT) failed: %v", path, err)
		return
	}
	logf("staging: MoveFileExW(%s, NULL, MOVEFILE_DELAY_UNTIL_REBOOT) -> registered in PendingFileRenameOperations (a registration, not a deletion)", path)
}

// ---------------------------------------------------------------------------
// Deleting a tree without ever following a reparse point.
//
// APP.md 3 says the sweep never traverses a reparse point, and that rule is
// worth implementing rather than inheriting: a junction under a staging folder
// -- however it got there -- would otherwise turn a delete of our own temporary
// files into a delete of whatever it points at. A reparse point is removed as
// the link it is, and never descended into.

func isReparsePoint(path string) bool {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	attrs, err := windows.GetFileAttributes(p)
	if err != nil {
		return false
	}
	return attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

func removeTreeNoReparse(root string) error {
	info, err := os.Lstat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !info.IsDir() || isReparsePoint(root) {
		// Removing the link itself, which is what os.Remove does for a junction
		// or a symlink: RemoveDirectory deletes the reparse point, not the
		// directory it names.
		return os.Remove(root)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	var first error
	for _, e := range entries {
		path := filepath.Join(root, e.Name())
		var err error
		if e.IsDir() {
			err = removeTreeNoReparse(path)
		} else {
			err = os.Remove(path)
		}
		if err != nil && first == nil {
			first = err
		}
	}
	if err := os.Remove(root); err != nil && first == nil {
		first = err
	}
	return first
}

// ---------------------------------------------------------------------------
// The events the rest of the program feeds in.

// noteAsyncStarted is StartOperation: the target negotiated the asynchronous
// protocol. Whether Explorer does that for a CF_HDROP source at all is one of
// the questions -- no trace in the research proved it does.
func (s *dragStage) noteAsyncStarted() {
	s.mu.Lock()
	s.asyncOp = true
	s.mu.Unlock()
	logf("staging: the target negotiated the asynchronous protocol for a CF_HDROP source -- so EndOperation will mean something here")
}

// noteEndOperation is EndOperation, arriving on whatever thread the target calls
// it on. Nothing is deleted here: the watcher takes the decision on its own
// goroutine, within 250 ms, so that a delete never runs inside a call the target
// is waiting to return from.
func (s *dragStage) noteEndOperation() {
	s.mu.Lock()
	s.endOp = true
	s.mu.Unlock()
	logf("staging: EndOperation noted -- it ends the target's transfer, not every later use of the paths")
}

// noteDragEnded is DoDragDrop returning, with what it returned. The question it
// answers for the log is 7-Zip's: was CF_HDROP asked for at all before the drag
// went away?
func (s *dragStage) noteDragEnded(result string) {
	s.mu.Lock()
	s.dragOver = true
	req, early, handed := s.requests, s.early, s.handedOut
	s.mu.Unlock()
	if req == 0 {
		logf("staging: the drag ended (%s) and CF_HDROP was NEVER requested -- no target asked during the hover", result)
	} else {
		logf("staging: the drag ended (%s) after %d CF_HDROP request(s), %d of them during the hover -- targets do ask before the drop",
			result, req, early)
	}
	if !handed {
		logf("staging: nothing was handed out; the folder goes at once")
	}
	s.startWatch()
}

// forceCloseAllStages is the second close, and it visits EVERY staging folder
// this run still has registered rather than the drag that happened to be last.
//
// This is a defect turned into behaviour. In a two-drag round the first drag
// stalled inside the target -- Explorer never opened the staged file, and the
// folder kept five gigabytes of plaintext in state "handed-out" -- while the
// second completed as a move. The forced close logged exactly one deletion, the
// second drag's already-empty folder, and never so much as looked at the first:
// the only pointer it had was to the last stage. Both halves of the fix are
// here: every unresolved stage is kept in liveStages until it is resolved, and
// the close walks them all, saying what it attempted and what came of it.
func forceCloseAllStages() {
	stages := remainingStages()
	if len(stages) == 0 {
		logf("staging: the forced close has nothing to visit: no staging folder of this run is still registered")
		return
	}
	logf("staging: the forced close visits every staging folder still registered (%d)", len(stages))
	for i, s := range stages {
		logf("staging: forced close %d of %d: %s", i+1, len(stages), s.root)
		s.noteForcedClose()
		logf("staging: forced close %d of %d ended: %s -- %s", i+1, len(stages), s.root, s.disposition())
	}
	logStagesLeftOnDisk("after the forced close")
}

// quietCloseAllStages is the ordinary close, over every registered stage for the
// same reason: an earlier drag's folder is no less this run's to decide about
// than the last one's.
func quietCloseAllStages() {
	for _, s := range remainingStages() {
		s.closeQuietly()
	}
}

// stagesLeftOnDisk is every folder this run made that is still there, whatever
// the reason -- handed out and waiting for the scavenge, kept by -keep, or left
// by a delete that failed.
func stagesLeftOnDisk() []*dragStage {
	var left []*dragStage
	for _, s := range knownStages() {
		if s.onDisk() {
			left = append(left, s)
		}
	}
	return left
}

// logStagesLeftOnDisk names them, with the manifest state and the error of the
// delete that failed if one did. On disk a folder left behind on purpose and a
// folder a cleanup skipped look identical; only this says which it is.
func logStagesLeftOnDisk(when string) {
	left := stagesLeftOnDisk()
	if len(left) == 0 {
		logf("staging: no staging folder of this run is left on disk (%s)", when)
		return
	}
	logf("staging: %d staging folder(s) of this run are left on disk (%s):", len(left), when)
	for _, s := range left {
		logf("    %s: %s", s.root, s.disposition())
	}
}

// noteForcedClose is the second close: the window is going away with staged
// files still in use. It runs on the window thread and runs to completion there,
// because the process is about to exit and the watcher will not get another tick.
func (s *dragStage) noteForcedClose() {
	s.mu.Lock()
	if s.deleted {
		s.mu.Unlock()
		// Resolved by its own watcher between the close and this call. Say so
		// rather than pass over it in silence, which is what hid the defect.
		logf("staging: %s was already resolved before the forced close: %s", s.root, s.disposition())
		return
	}
	s.forced = true
	ev := s.eventsLocked(time.Now())
	s.mu.Unlock()
	d := decideStageCleanup(ev)
	logf("staging cleanup (forced close): %s -- %s", d.action, d.why)
	if d.action == stageKeepForever {
		return
	}
	s.remove(true)
}

// closeQuietly is the ordinary close, with nothing in flight.
//
// It does NOT delete a folder whose paths were handed out. That is the whole
// lesson of the third research pass: the consumer may open them minutes later,
// and this process ending says nothing about that. The folder is manifested, and
// the sweep -- this run's ten-minute one is over, so the next launch's -- is what
// takes it once it is past the age limit.
func (s *dragStage) closeQuietly() {
	s.mu.Lock()
	deleted, keep, handed := s.deleted, s.cfg.keep, s.handedOut
	s.mu.Unlock()
	if deleted {
		return
	}
	if keep {
		logf("staging cleanup: keep -- -keep: %s is left where it is, for inspection", s.root)
		return
	}
	if handed {
		logf("staging cleanup: wait -- the window is closing, but the paths were handed out; %s is left manifested for the scavenge (consumers open dropped paths late)", s.root)
		return
	}
	logf("staging cleanup: delete -- the window is closing and nothing was ever handed out")
	s.remove(false)
}

// inUseNow is what the window's close guard asks: is anybody reading a staged
// file right now? It reads the watcher's last poll rather than probing, so that
// a close is never held up by the file system.
func (s *dragStage) inUseNow() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var busy []string
	for i := range s.watch {
		if s.watch[i].inUse {
			busy = append(busy, s.watch[i].path)
		}
	}
	return busy
}

// onDisk is the honest answer to "is this folder still there", asked of the
// file system rather than of what the stage believes. A close that decided to
// delete and a delete that succeeded are two different facts.
func (s *dragStage) onDisk() bool {
	_, err := os.Lstat(s.root)
	return err == nil
}

// disposition is one line of what became of a folder, for the close and exit
// summaries.
func (s *dragStage) disposition() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dispositionLocked()
}

func (s *dragStage) dispositionLocked() string {
	switch {
	case s.removed:
		return fmt.Sprintf("deleted (manifest state %q at the end)", s.state)
	case s.removeErr != nil:
		return fmt.Sprintf("LEFT ON DISK, manifest state %q: the delete failed: %v", s.state, s.removeErr)
	case s.cfg.keep:
		return fmt.Sprintf("LEFT ON DISK by -keep, manifest state %q", s.state)
	}
	return fmt.Sprintf("LEFT ON DISK for the scavenge, manifest state %q", s.state)
}

// summary is the per-drag result line: everything asked for, when, what the
// extraction did, and where the folder stands.
func (s *dragStage) summary() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b strings.Builder
	fmt.Fprintf(&b, "staging summary for %s:\n", s.root)
	fmt.Fprintf(&b, "    CF_HDROP requests: %d in all, %d during the hover, %d after the release\n",
		s.requests, s.early, s.requests-s.early)
	if s.extracted {
		fmt.Fprintf(&b, "    extraction: %d bytes in %s, failed=%v", s.bytes, s.extractDur.Round(time.Millisecond), s.failed)
		if s.failErr != nil {
			fmt.Fprintf(&b, " (%v)", s.failErr)
		}
		b.WriteString("\n")
	} else {
		b.WriteString("    extraction: never ran\n")
	}
	fmt.Fprintf(&b, "    handed out=%v, async negotiated=%v, EndOperation=%v, manifest state=%q, resolved=%v\n",
		s.handedOut, s.asyncOp, s.endOp, s.state, s.deleted)
	fmt.Fprintf(&b, "    the folder: %s\n", s.dispositionLocked())
	for i := range s.watch {
		w := &s.watch[i]
		fmt.Fprintf(&b, "    %s: everUsed=%v, inUse=%v, gone=%v\n", w.path, w.everUsed, w.inUse, w.gone)
	}
	for _, a := range s.asks {
		fmt.Fprintf(&b, "    asked: %s %s (%s)\n", a.kind, a.format, a.when)
	}
	return strings.TrimRight(b.String(), "\n")
}

// threadWord names the thread a call arrived on the way the -agile experiment
// cares about it: the drag thread is this program's STA, anything else is the
// target's own.
func threadWord() string {
	tid := windows.GetCurrentThreadId()
	switch {
	case tid == dragThreadID.Load():
		return fmt.Sprintf("tid %d, the drag thread (this program's STA)", tid)
	case tid == oleThreadID.Load():
		return fmt.Sprintf("tid %d, the dedicated OLE thread (-thread), which is this program's too", tid)
	}
	return fmt.Sprintf("tid %d, a thread of the target's", tid)
}

// ---------------------------------------------------------------------------
// -poll-after: is the target still reading after DoDragDrop has returned?
//
// This is the question that decides whether a staging folder may be deleted at
// the return, and the answer is expected to differ by volume. A same-volume
// drop is one rename: the file is gone from the staging folder within
// milliseconds and nothing is reading anything afterwards. A cross-volume drop
// is a copy, and the copy is Explorer's, run on Explorer's own schedule with
// nothing said to the source -- so the staged file can still be open long after
// DoDragDrop has come back. Deleting at the return would then take the file out
// from under the copy.
//
// The measurement is the exclusive open the watch already uses, sampled for
// -poll-after seconds from the moment of the return.

// pollAfterFor is the -poll-after window, set once in main.
var pollAfterFor time.Duration

// pollAfterFacts is everything the verdict is decided from, so the decision can
// be checked without a drag, a file system or a clock.
type pollAfterFacts struct {
	window   time.Duration
	samples  int
	busy     int           // samples in which at least one staged file was open elsewhere
	lastBusy time.Duration // when that last happened; negative if it never did
	present  int           // samples in which at least one staged file was still on disk
	files    []string      // the files that were seen open, in the order first seen
	folder   bool          // the staging folder was still there at the end
}

// pollAfterVerdict is the sentence the experiment is read from.
func pollAfterVerdict(f pollAfterFacts) string {
	switch {
	case f.samples == 0:
		return fmt.Sprintf("no sample was taken in the %s after DoDragDrop returned", f.window)

	case f.busy > 0:
		return fmt.Sprintf("a staged file was STILL OPEN by another process %s after DoDragDrop returned "+
			"(%d of %d samples in the %s window; %s) -- for this drop the staging folder could NOT have been deleted at the return, "+
			"which is what a cross-volume copy looks like",
			f.lastBusy.Round(time.Millisecond), f.busy, f.samples, f.window, strings.Join(f.files, ", "))

	case f.present == 0:
		return fmt.Sprintf("every staged file was gone within the first sample and nothing held one open during the %s after DoDragDrop returned "+
			"-- the drop was a rename, and for this drop the staging folder could have been deleted at the return", f.window)

	case f.folder:
		return fmt.Sprintf("no staged file was open by anyone during the %s after DoDragDrop returned, but the file(s) and the folder are still there "+
			"-- nothing came back for them, so the return says nothing about when they may go", f.window)

	default:
		return fmt.Sprintf("no staged file was open by anyone during the %s after DoDragDrop returned, and the folder has gone", f.window)
	}
}

// reportPollAfter samples the staged files for window and writes the verdict.
// It runs on a goroutine because the answer takes the whole window to arrive and
// the drag may not wait for it.
func (s *dragStage) reportPollAfter(window time.Duration) {
	if window <= 0 {
		return
	}
	logf("staging: -poll-after -- sampling %d staged file(s) every 250 ms for %s from here, to see whether the target is still reading after the return",
		len(s.paths), window)
	go func() {
		start := time.Now()
		t := time.NewTicker(250 * time.Millisecond)
		defer t.Stop()
		facts := pollAfterFacts{window: window, lastBusy: -1}
		seen := map[string]bool{}
		for range t.C {
			at := time.Since(start)
			if at > window {
				break
			}
			facts.samples++
			anyBusy, anyPresent := false, false
			for _, p := range s.paths {
				inUse, exists, err := stagedFileInUse(p)
				if err != nil {
					continue
				}
				if exists {
					anyPresent = true
				}
				if inUse {
					anyBusy = true
					if !seen[p] {
						seen[p] = true
						facts.files = append(facts.files, filepath.Base(p))
					}
				}
			}
			if anyBusy {
				facts.busy++
				facts.lastBusy = at
			}
			if anyPresent {
				facts.present++
			}
		}
		facts.folder = s.onDisk()
		logf("staging: -poll-after for %s -- %s", s.root, pollAfterVerdict(facts))
	}()
}

// ---------------------------------------------------------------------------
// The scavenger.
//
// This is the policy, not the backstop. A drag hands out paths and then has no
// idea what happens to them; the folder stays, and a sweep at launch and every
// ten minutes afterwards removes the manifested folders that are past their age
// and not live. WinRAR's hour is the threshold, for WinRAR's stated reason --
// "external applications may still need them" -- and against a longer one
// because what lingers is plaintext.

// scavengeInterval is the "every ten minutes while running" of APP.md 3.
const scavengeInterval = 10 * time.Minute

// scavengeFacts is everything a sweep may look at before deciding to delete a
// directory. Keeping it a struct is what makes the decision testable without a
// file system, and what makes it obvious that "is it ours" is checked before
// anything else.
type scavengeFacts struct {
	reparse      bool
	haveManifest bool // ... and it is ours: readManifest already refused the rest
	state        string
	age          time.Duration
	maxAge       time.Duration
	active       bool // a stage this process is still watching
}

// scavengeVerdict decides one directory. Every "no" carries its reason, because
// a scavenger that silently leaves things behind is indistinguishable from one
// that is broken.
func scavengeVerdict(f scavengeFacts) (remove bool, why string) {
	switch {
	case f.reparse:
		return false, "a reparse point: never followed, never removed by the sweep"
	case !f.haveManifest:
		return false, "no manifest of ours: not this tool's folder"
	case f.active:
		return false, "this process is still watching this drag"
	case f.state == stateLive:
		return false, "the manifest says the drag is still live"
	case f.age <= f.maxAge:
		return false, fmt.Sprintf("only %s old, under the %s limit", f.age.Round(time.Second), f.maxAge)
	}
	return true, fmt.Sprintf("manifested %q, %s old, past the %s limit", f.state, f.age.Round(time.Second), f.maxAge)
}

// scavengeBackoff is the bounded retry APP.md 3 names: 1 s, 10 s, 60 s, and then
// the folder is left for the next sweep rather than retried forever.
func scavengeBackoff(attempt int) (time.Duration, bool) {
	switch attempt {
	case 0:
		return time.Second, true
	case 1:
		return 10 * time.Second, true
	case 2:
		return time.Minute, true
	}
	return 0, false
}

// remainingStages is every staging folder this process made and did not delete,
// for the exit log.
func remainingStages() []*dragStage {
	liveStages.mu.Lock()
	defer liveStages.mu.Unlock()
	out := make([]*dragStage, 0, len(liveStages.m))
	for _, s := range liveStages.m {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].root < out[j].root })
	return out
}

func stageIsActive(root string) bool {
	liveStages.mu.Lock()
	defer liveStages.mu.Unlock()
	_, ok := liveStages.m[root]
	return ok
}

// sweepStaging is one pass. It returns what it did, for the test and for the log.
func sweepStaging(root string, maxAge time.Duration, now time.Time) (removed, kept, failed int) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			logf("scavenge: %s does not exist; nothing to sweep", root)
		} else {
			logf("scavenge: cannot read %s: %v", root, err)
		}
		return 0, 0, 0
	}
	for _, e := range entries {
		path := filepath.Join(root, e.Name())
		facts := scavengeFacts{
			reparse: isReparsePoint(path),
			maxAge:  maxAge,
			active:  stageIsActive(path),
		}
		if !e.IsDir() {
			// A stray file under the drag root is not a staging folder and is not
			// ours to remove either.
			logf("scavenge: leaving %s: not a directory", path)
			kept++
			continue
		}
		if m, ok := readManifest(path); ok {
			facts.haveManifest = true
			facts.state = m.State
			facts.age = now.Sub(m.Created)
		}
		remove, why := scavengeVerdict(facts)
		if !remove {
			logf("scavenge: leaving %s: %s", path, why)
			kept++
			continue
		}
		if sweepOne(path, why) {
			removed++
		} else {
			failed++
		}
	}
	logf("scavenge: %d removed, %d left, %d could not be removed, under %s", removed, kept, failed, root)
	return removed, kept, failed
}

// sweepOne removes one folder, with the bounded backoff for a folder something
// still has open. Every attempt's error is logged as Windows gave it: a sharing
// violation here is the interesting one, because it means a consumer is still
// reading a file from a drag that ended an hour ago.
func sweepOne(path, why string) bool {
	logf("scavenge: removing %s: %s", path, why)
	for attempt := 0; ; attempt++ {
		err := removeTreeNoReparse(path)
		if err == nil {
			logf("scavenge: removed %s", path)
			return true
		}
		logf("scavenge: could not remove %s: %v", path, err)
		if busy := stagedTreeInUse(path); busy != "" {
			logf("scavenge: %s is still open by another process", busy)
		}
		wait, more := scavengeBackoff(attempt)
		if !more {
			logf("scavenge: leaving %s for the next sweep (the backoff is bounded: 1 s, 10 s, 60 s)", path)
			return false
		}
		logf("scavenge: retrying %s in %s", path, wait)
		time.Sleep(wait)
	}
}

// stagedTreeInUse names the first file under a folder that somebody else has
// open, or "" if none has. It is only used to explain a failed delete.
func stagedTreeInUse(root string) string {
	found := ""
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if d != nil && d.IsDir() && isReparsePoint(path) {
				return filepath.SkipDir
			}
			return nil
		}
		if inUse, _, err := stagedFileInUse(path); err == nil && inUse {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// startScavenger runs the sweep at launch and every ten minutes afterwards, on a
// goroutine of its own: a sweep can sit in a backoff for over a minute, and the
// window must not wait for it.
func startScavenger(root string, maxAge time.Duration) {
	go func() {
		logf("scavenge: sweeping %s at launch (manifested folders older than %s, state not %q)", root, maxAge, stateLive)
		sweepStaging(root, maxAge, time.Now())
		t := time.NewTicker(scavengeInterval)
		defer t.Stop()
		for range t.C {
			logf("scavenge: the %s sweep", scavengeInterval)
			sweepStaging(root, maxAge, time.Now())
		}
	}()
}

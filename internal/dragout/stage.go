package dragout

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

// The staging folder and the state machine over it — the half of the route
// that needs no COM and is therefore tested without a drag (APP.md §3).
//
// The shape, which is 7-Zip's where 7-Zip was right and deliberately not
// where its own tracker says it was wrong:
//
//   - an empty, manifested staging folder is made BEFORE DoDragDrop, and the
//     paths the files will have are decided there and then;
//   - a GetData(CF_HDROP) during the hover — targets do ask, twice on this
//     machine — is answered with those FINAL paths. Never a placeholder:
//     Edge caches the early name;
//   - the button's release arms the extraction; a release over the caller's
//     own window is a self-drop, which extracts nothing at all;
//   - the first GetData(CF_HDROP) after the release runs the caller's
//     Extract, inside the call, on the target's thread;
//   - a request that cannot be honoured — the extraction failed or was
//     cancelled — FAILS GetData rather than handing out the names of files
//     that are not there (7-Zip returns the names either way, which its
//     own comments treat as a known defect);
//   - every later request hands back the same paths without writing.

// ---------------------------------------------------------------------------
// The manifest.
//
// A scavenger that deletes directories needs to know which directories are
// its own, and an age alone cannot say. So every staging folder carries one
// small JSON file naming the tool that made it, when, which process and
// when that process started, what state the drag is in and which files it
// staged. The sweep acts on folders
// that carry this and on nothing else: a folder a user put there by hand, a
// folder another program made, a reparse point pointing anywhere at all —
// none of them are ours and none of them are touched.
//
// The manifest sits at the folder's top and the staged items under an
// "items" folder beneath it (APP.md §3): a record may itself be called
// manifest.json, and were the two to share a directory the record would
// overwrite the manifest — or the manifest the record — at the first
// transition. Kept a level apart, no dragged name can reach it.

const (
	manifestName = "manifest.json"
	itemsDirName = "items"
	manifestTool = "enfold"
	// Version 2 added the owner's start time beside its PID (Manifest,
	// manifestOwner). Nothing reads the number — ReadManifest asks after
	// the tool and nothing else, and every field is optional to a reader —
	// so it is a record of what was written and not a gate: a version 1
	// manifest, which names a PID and no start time, is read as an unknown
	// owner and treated as such.
	manifestVersion = 2

	// The three states APP.md names. StateLive is a drag still in progress
	// — the folder is never swept while it says that; StateHandedOut is a
	// drag whose paths a target has been given, which is the state that
	// outlives the drop on purpose, because consumers open the paths late;
	// StateDone is a folder whose deletion has been decided, written BEFORE
	// the deletion is attempted so that a deletion which fails still leaves
	// a folder the next sweep will take.
	StateLive      = "live"
	StateHandedOut = "handed-out"
	StateDone      = "done"
)

// Manifest is what a staging folder says about itself.
//
// PID and PIDStarted together name the owner: the process that wrote this
// manifest, and when that process started. The PID alone is not an
// identity — Windows hands a freed PID out again within seconds — so the
// creation time rides with it, and an owner is the same owner only when
// both match. PIDStarted is the FILETIME the platform gives, 100-ns ticks
// since 1601, and zero when the platform would not say or a version 1
// manifest never wrote one; a zero is "unknown", never "matches".
//
// Every field is optional to a reader: a manifest an older build wrote
// parses here with its new field at the zero value, which is exactly the
// answer the sweep must then act on (manifestOwner, scavengeVerdict).
type Manifest struct {
	Tool       string    `json:"tool"`
	Version    int       `json:"version"`
	PID        int       `json:"pid"`
	PIDStarted int64     `json:"pidStarted,omitempty"`
	Created    time.Time `json:"created"`
	State      string    `json:"state"`
	Files      []string  `json:"files"`
}

// WriteManifest writes a folder's manifest.
func WriteManifest(dir string, m Manifest) error {
	blob, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, manifestName), append(blob, '\n'), 0o600)
}

// ReadManifest reads a folder's manifest and says whether it is ours. A
// folder without one, with an unreadable one, or with one some other tool
// wrote is not ours, and the caller must leave it alone.
func ReadManifest(dir string) (Manifest, bool) {
	blob, err := os.ReadFile(filepath.Join(dir, manifestName))
	if err != nil {
		return Manifest{}, false
	}
	var m Manifest
	if err := json.Unmarshal(blob, &m); err != nil {
		return Manifest{}, false
	}
	if m.Tool != manifestTool {
		return Manifest{}, false
	}
	return m, true
}

// ---------------------------------------------------------------------------
// The owner of a manifest.
//
// A manifest that says "live" says a drag is in progress, and a drag in
// progress is never swept. That was once the whole of it, which left one
// hole: a process that crashed or was killed mid-drag leaves a folder
// saying "live" forever, and what it holds is plaintext. So the manifest
// records who is dragging, and the sweep asks after them.

// ownerState is what a sweep could learn about the process that wrote a
// manifest. ownerUnknown is not a middle ground between the other two: it
// is the sweep having no answer — the manifest names no owner, or the
// platform would not say — and the cautious reading is what it gets.
type ownerState int

const (
	ownerUnknown ownerState = iota
	ownerAlive
	ownerGone
)

// selfOwner is this process as a manifest records it: its id, and when it
// started where the platform says so cheaply (0 otherwise).
func selfOwner() (pid int, started int64) {
	pid = os.Getpid()
	started, _ = probeProcess(pid)
	return pid, started
}

// manifestOwner asks after the process a manifest names. Only three
// answers are possible, and only one of them lets a sweep act:
//
//   - gone, and nothing else, is the PID naming no process at all. A live
//     manifest whose owner is gone is a drag that died with its process.
//   - alive needs BOTH halves to agree: a process at that id, and the same
//     start time the manifest recorded. Windows hands a freed PID out again
//     within seconds, so an id alone is not an identity and is never read
//     as one.
//   - everything else is unknown: a manifest with no PID, a manifest from a
//     build that recorded no start time (version 1), or a process this one
//     may look at but not time. Unknown is not "probably dead" — it is the
//     sweep having no answer, and a live manifest it cannot vouch for is
//     left exactly where the older rule left it.
//
// A version 2 manifest always writes both halves, so unknown here means an
// older folder or a refusal, never one of this build's own drags.
func manifestOwner(m Manifest) ownerState {
	if m.PID <= 0 || m.PIDStarted == 0 {
		return ownerUnknown
	}
	started, state := probeProcess(m.PID)
	if state != ownerAlive {
		return state
	}
	if started == 0 {
		// There is a process at that id and no way to tell it from the one
		// that took the id after ours let it go.
		return ownerUnknown
	}
	if started != m.PIDStarted {
		return ownerGone
	}
	return ownerAlive
}

// ownerOf is how the sweep asks, a variable so that a test can answer for a
// process that never existed rather than arranging for one to die.
var ownerOf = manifestOwner

// newStageID is the <random 8 hex> of one drag's folder. crypto/rand because
// two drags a millisecond apart must not collide, and because a predictable
// name for a folder that will hold plaintext is a name somebody else can
// wait for.
func newStageID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%08x", uint32(time.Now().UnixNano()))
	}
	return hex.EncodeToString(b[:])
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
// and the page describes itself as "Defines the CF_HDROP clipboard format.
// The data that follows is a double null-terminated list of file names."
//
// The byte offsets are NOT on that page — it has no Remarks and states
// neither a size nor an offset — so they are derived here from the
// documented types rather than quoted: DWORD is "a 32-bit unsigned integer",
// BOOL is "typedef int BOOL" and int is 32 bits on both Windows
// architectures, POINT is two LONGs and LONG is "a 32-bit signed integer".
// Nothing in the structure is a pointer, so it is 20 bytes on x86 and x64
// alike — pFiles at 0, pt at 4, fNC at 12, fWide at 16 — and the file list
// starts at the offset pFiles names, which is 20. That derivation is what
// the test pins.
//
// The list itself is the Shell Clipboard Formats page's, and that one is
// quoted: "The file name array consists of a series of strings, each
// containing one file's fully qualified path, including the terminating NULL
// character. An additional null character is appended to the final string to
// terminate the array." So: absolute paths, and a double NUL at the end. The
// medium is named on the scenarios page — "Set the cfFormat member of the
// FORMATETC structure to CF_HDROP and the tymed member to TYMED_HGLOBAL".
const (
	dropFilesHeaderSize = 20
	dropFilesOffPFiles  = 0
	dropFilesOffPoint   = 4
	dropFilesOffFNC     = 12
	dropFilesOffFWide   = 16
)

// cfHDrop is CF_HDROP. "Unlike the other Shell formats, it is predefined, so
// there is no need to call RegisterClipboardFormat" — it is a constant in
// winuser.h (CF_HDROP = 15).
const cfHDrop = 15

// DROPEFFECT values, read out of oleidl.h.
const (
	dropEffectNone = 0
	dropEffectCopy = 1
	dropEffectMove = 2
	dropEffectLink = 4
)

// allowedEffects is what DoDragDrop is told the drag permits, and
// preferredEffect what CFSTR_PREFERREDDROPEFFECT says the source would
// rather have: copy and move allowed, move preferred (APP.md §3, 7-Zip's
// practice). The staged file is a disposable copy, so a drop on the same
// volume — the desktop, from %LOCALAPPDATA% — becomes one rename with no
// second pass over the bytes and nothing left to clean up, and a drop on
// another volume is the target's copy followed by its own delete of the
// staging. The record inside the archive is never touched by either: a move
// here moves the temporary, never the original.
const (
	allowedEffects  = dropEffectCopy | dropEffectMove
	preferredEffect = dropEffectMove
)

// ScavengeAge is the age past which a folder handed out is taken — WinRAR's
// hour, for WinRAR's stated reason ("external applications may still need
// them") and against a longer one because what lingers is plaintext — and
// ScavengeInterval how often the caller sweeps while running, after the
// sweep at launch and before the one at a normal exit (APP.md §3).
const (
	ScavengeAge      = time.Hour
	ScavengeInterval = 10 * time.Minute
)

// encodeDropFiles renders the HGLOBAL contents of a CF_HDROP rendering: the
// DROPFILES header followed by the paths as double-NUL-terminated UTF-16.
//
// pt and fNC are left zero on purpose. They are the drop point and the
// nonclient-area flag a WM_DROPFILES receiver reads back out of the block; a
// data object handed to DoDragDrop is not the thing that knows where the
// cursor was, and a source that offers CF_HDROP this way leaves them alone.
func encodeDropFiles(paths []string) []byte {
	list := make([]uint16, 0, 64)
	for _, p := range paths {
		list = append(list, utf16.Encode([]rune(p))...)
		list = append(list, 0)
	}
	// The list's own terminator. With at least one path this is the second
	// half of the documented double NUL; with none it is the whole of an
	// empty list.
	list = append(list, 0)

	buf := make([]byte, dropFilesHeaderSize+2*len(list))
	binary.LittleEndian.PutUint32(buf[dropFilesOffPFiles:], dropFilesHeaderSize)
	// fWide nonzero, which is what the member documents as "it contains
	// Unicode characters" — 1, the conventional BOOL TRUE, not VARIANT_TRUE's
	// -1. The paths are UTF-16, so a zero here would be a lie about the bytes
	// that follow and a consumer would read them as ANSI.
	binary.LittleEndian.PutUint32(buf[dropFilesOffFWide:], 1)
	for i, u := range list {
		binary.LittleEndian.PutUint16(buf[dropFilesHeaderSize+2*i:], u)
	}
	return buf
}

// encodeDropEffect renders CFSTR_PREFERREDDROPEFFECT's data. The format "is
// used by the source to specify whether its preferred method of data transfer
// is move, copy, or link ... The structure's hGlobal member points to a DWORD
// value", so this is four bytes and nothing else.
func encodeDropEffect(effect uint32) []byte {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], effect)
	return b[:]
}

// ---------------------------------------------------------------------------
// The staging folder, and the state machine over it.

// stage is one drag's folder and everything the drag learns about it.
type stage struct {
	id   string
	root string // <Root>\<8 hex id>: the drag's folder, the manifest at its top
	// itemsDir is <root>\items, where every staged item lives and where the
	// caller's Extract writes; the paths CF_HDROP names all lie beneath it.
	itemsDir string
	items    []Item
	paths    []string // one per item, in order, under itemsDir
	// drop is what CF_HDROP names: the top-level items only. CF_HDROP has
	// no other way to say "a folder" — the list is paths, and a path that
	// names a directory is the directory, with everything in it; naming the
	// files inside would drop them loose into the destination.
	drop    []string
	extract func(ctx context.Context, dir string) error
	onPhase func(Phase)
	log     func(string, ...any)
	ctx     context.Context
	cancel  context.CancelFunc

	// extractMu serialises the extraction itself, and is deliberately not
	// mu: the extraction runs for as long as the files take, and everything
	// else about the stage — the counters, the cleanup decision, the close
	// guard — has to stay answerable while it does.
	extractMu sync.Mutex

	mu         sync.Mutex
	created    time.Time
	state      string
	requests   int // GetData(CF_HDROP) calls, all of them
	early      int // ... of which arrived before the button came up
	armed      bool
	releasedAt time.Time
	selfDrop   bool
	// targetClass is the class name of the top-level window under the
	// cursor when the button came up — the one moment a source is given to
	// look. It is what says whether the folder can go the instant
	// DoDragDrop returns: Explorer and the desktop finish the drop inside
	// Drop, so when the call returns they are done with the paths (APP.md
	// §3, ruled 2026-09-11). Empty when there was no window there, or when
	// the class could not be read.
	targetClass string
	extracted   bool
	failed      bool
	failErr     error
	// written says the extraction was begun, so anything at all may be on
	// disk: half a file is plaintext somebody has to delete.
	written   bool
	handedOut bool
	dragOver  bool
	// dragEnd is what DoDragDrop's own return said when it said anything
	// final: Cancelled for DRAGDROP_S_CANCEL, Failed for a call that could
	// not run, and zero for the drop itself (DRAGDROP_S_DROP), whose
	// outcome the effect says.
	dragEnd Reason
	deleted bool
	// phaseDone: Done has been reported, once.
	phaseDone  bool
	extractDur time.Duration

	// removed is the folder actually being gone from disk, and it is
	// deliberately not the same fact as deleted: deleted means the stage is
	// resolved, which a delete that FAILED also is. Only removed says there
	// is nothing left, and removeErr is why there is.
	removed   bool
	removeErr error
}

// liveStages is every stage this process has not yet decided about, by
// folder. The scavenge consults it so that the ten-minute sweep never takes
// the folder of a drag that is still running, and the close at exit walks it
// so that a drag left behind by an earlier gesture is decided about rather
// than forgotten. A stage leaves this set the moment its fate is decided, so
// that a folder whose delete failed is the sweep's to take from then on.
var liveStages = struct {
	mu sync.Mutex
	m  map[string]*stage
}{m: make(map[string]*stage)}

// newStage makes the folder, writes the manifest and works out the paths,
// all BEFORE DoDragDrop — which is the whole point of the early half of the
// protocol, and the reason a hover-time request can be answered with the
// final names. Root is a parameter so that a test can point the whole
// mechanism at t.TempDir(): nothing in the test suite may write under
// %LOCALAPPDATA%.
func newStage(opts Options) (*stage, error) {
	if opts.Root == "" || len(opts.Items) == 0 || opts.Extract == nil {
		return nil, errors.New("dragout: a root, at least one item and an extractor are required")
	}
	// The names are judged before anything exists on disk for them: a
	// refused plan leaves no folder behind.
	names := make([]string, 0, len(opts.Items))
	tops := 0
	for _, it := range opts.Items {
		name := filepath.Clean(filepath.FromSlash(it.Name))
		if name == "." || name == ".." || filepath.IsAbs(name) || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("dragout: item %q is not a path under the staging folder", it.Name)
		}
		names = append(names, name)
		if !strings.Contains(name, string(filepath.Separator)) {
			tops++
		}
	}
	if tops == 0 {
		return nil, errors.New("dragout: no item stands at the top of the staging folder")
	}
	if err := os.MkdirAll(opts.Root, 0o700); err != nil {
		return nil, fmt.Errorf("creating the drag root: %w", sansPath(err))
	}
	id := newStageID()
	root := filepath.Join(opts.Root, id)
	// Mkdir, not MkdirAll: the id is fresh and the folder must be ours alone.
	if err := os.Mkdir(root, 0o700); err != nil {
		return nil, fmt.Errorf("creating the staging folder: %w", sansPath(err))
	}
	itemsDir := filepath.Join(root, itemsDirName)
	if err := os.Mkdir(itemsDir, 0o700); err != nil {
		os.Remove(root)
		return nil, fmt.Errorf("creating the items folder: %w", sansPath(err))
	}
	log := opts.Log
	if log == nil {
		log = discard
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &stage{
		id: id, root: root, itemsDir: itemsDir, items: opts.Items, extract: opts.Extract, onPhase: opts.OnPhase,
		log: log, ctx: ctx, cancel: cancel,
		created: time.Now(), state: StateLive,
	}
	if s.onPhase == nil {
		s.onPhase = func(Phase) {}
	}
	rel := make([]string, 0, len(names))
	for _, name := range names {
		p := filepath.Join(itemsDir, name)
		s.paths = append(s.paths, p)
		rel = append(rel, filepath.Join(itemsDirName, name))
		if !strings.Contains(name, string(filepath.Separator)) {
			s.drop = append(s.drop, p)
		}
	}
	pid, started := selfOwner()
	if err := WriteManifest(root, Manifest{
		Tool: manifestTool, Version: manifestVersion, PID: pid, PIDStarted: started,
		Created: s.created, State: StateLive, Files: rel,
	}); err != nil {
		cancel()
		os.Remove(itemsDir)
		os.Remove(root)
		return nil, fmt.Errorf("writing the manifest: %w", sansPath(err))
	}
	liveStages.mu.Lock()
	liveStages.m[root] = s
	liveStages.mu.Unlock()
	return s, nil
}

// setState moves the manifest on. Every transition is written to disk,
// because the state is only worth anything to a sweep that runs after this
// process has gone.
func (s *stage) setState(state string) {
	s.mu.Lock()
	if s.state == state || s.deleted {
		s.mu.Unlock()
		return
	}
	s.state = state
	s.mu.Unlock()
	if err := s.persistManifest(state); err != nil {
		s.log("drag %s: could not update the manifest to %q: %v", s.id, state, sansPath(err))
	}
}

// persistManifest writes this stage's manifest out in the given state. It is
// separate from setState because the manifest has to be writable again after
// a delete that only half succeeded, where the state has not changed at all.
func (s *stage) persistManifest(state string) error {
	rel := make([]string, 0, len(s.paths))
	for _, p := range s.paths {
		if r, err := filepath.Rel(s.root, p); err == nil {
			rel = append(rel, r)
		}
	}
	s.mu.Lock()
	created := s.created
	s.mu.Unlock()
	pid, started := selfOwner()
	return WriteManifest(s.root, Manifest{
		Tool: manifestTool, Version: manifestVersion, PID: pid, PIDStarted: started,
		Created: created, State: state, Files: rel,
	})
}

// pathList is what a CF_HDROP rendering names right now. It never changes:
// the whole trick of delayed rendering is that the names are decided before
// the bytes exist, and a name once handed out is a name a consumer may have
// cached.
func (s *stage) pathList() []string {
	out := make([]string, len(s.drop))
	copy(out, s.drop)
	return out
}

// arm is the button coming up, from IDropSource::QueryContinueDrag. From
// here on the next CF_HDROP request is the drop's own and runs the
// extraction — unless the button came up over the caller's own window, in
// which case nothing is ever extracted (APP.md §3, the self-drop).
//
// class is the class name of the top-level window under the cursor at that
// moment: the release is the one instant a source is given to see where the
// drop is going, and it is worth a line in the log for that alone. It
// decides nothing — the folder's fate turns on what was written and what
// the effect was, never on who the target is (APP.md §3, ruled
// 2026-09-11).
func (s *stage) arm(selfDrop bool, class string) {
	s.mu.Lock()
	already := s.armed
	s.armed = true
	if !already {
		s.releasedAt = time.Now()
		s.selfDrop = selfDrop
		s.targetClass = class
	}
	early := s.early
	s.mu.Unlock()
	if already {
		return
	}
	if selfDrop {
		s.log("drag %s: the button was released over Enfold's own window: a self-drop, nothing will be extracted (%d early request(s))", s.id, early)
		return
	}
	// The class name, never a title: a window's title is a document name and
	// a document name is a file name (APP.md §3).
	s.log("drag %s: the button was released over a window of class %q; the extraction is armed (%d early request(s))", s.id, class, early)
}

// timing is how a format request is placed against the button's release,
// which is the axis every question about delayed rendering is asked on.
func (s *stage) timing() string {
	s.mu.Lock()
	armed, at := s.armed, s.releasedAt
	s.mu.Unlock()
	if !armed {
		return "during the hover"
	}
	return fmt.Sprintf("+%s after the release", time.Since(at).Round(time.Millisecond))
}

// requestPaths is the state machine, and the one function that decides what
// a GetData(CF_HDROP) means. It returns the paths to render and whether the
// request is honoured; a refused request is E_UNEXPECTED to the caller —
// the documented way for GetData to say the rendering, not the FORMATETC,
// is what failed.
func (s *stage) requestPaths() ([]string, bool) {
	s.mu.Lock()
	s.requests++
	n := s.requests
	armed, selfDrop := s.armed, s.selfDrop
	done, failed := s.extracted, s.failed
	if !armed {
		s.early++
		s.mu.Unlock()
		s.log("drag %s: CF_HDROP request #%d during the hover, answered with the final paths of files that do not exist yet", s.id, n)
		s.markHandedOut()
		return s.pathList(), true
	}
	s.mu.Unlock()

	if selfDrop {
		// The drop is the WebView's own: it takes the paths to recognise
		// them as this drag's and opens nothing. The folder goes as soon as
		// DoDragDrop returns.
		s.log("drag %s: CF_HDROP request #%d after a self-drop: the paths, and nothing written", s.id, n)
		return s.pathList(), true
	}
	if failed {
		s.log("drag %s: CF_HDROP request #%d after a failed extraction -> E_UNEXPECTED", s.id, n)
		return nil, false
	}
	if done {
		s.log("drag %s: CF_HDROP request #%d after the extraction: the same paths again", s.id, n)
		s.markHandedOut()
		return s.pathList(), true
	}

	// Serialised, so that two requests racing in after the release — which
	// an agile object makes possible, on two of the target's threads —
	// extract once and the second one waits for the first rather than
	// writing over it.
	s.extractMu.Lock()
	defer s.extractMu.Unlock()
	s.mu.Lock()
	done, failed = s.extracted, s.failed
	gone := s.deleted
	s.mu.Unlock()
	if failed {
		return nil, false
	}
	if done {
		s.markHandedOut()
		return s.pathList(), true
	}
	if gone {
		// The folder's fate was decided while this request was on its way
		// — a drag that ended with nothing written lets its folder go at
		// once — and an extraction now would write into a folder the
		// cleanup has taken, with no manifest over it for the sweep.
		s.log("drag %s: CF_HDROP request #%d after the folder was let go of -> E_UNEXPECTED", s.id, n)
		return nil, false
	}
	s.log("drag %s: CF_HDROP request #%d is the first after the release (%s): extracting into the staging folder, inside GetData", s.id, n, s.timing())
	if err := s.runExtraction(); err != nil {
		return nil, false
	}
	s.markHandedOut()
	return s.pathList(), true
}

// runExtraction runs the caller's Extract into the items folder and records
// what happened. The caller holds extractMu. Preparing is reported before
// it and Awaiting after a success; a failure ends the drag there and then,
// Cancelled when the context was cancelled and Failed otherwise. Its start,
// end, result and bytes are the log's (APP.md §3): the bytes planned before,
// the bytes found on disk after — what a failure left behind is as much a
// fact about it as what a success wrote.
func (s *stage) runExtraction() error {
	s.mu.Lock()
	s.written = true
	s.mu.Unlock()
	files, planned := s.planned()
	s.log("drag %s: extraction started: %d file(s), %d bytes planned", s.id, files, planned)
	s.onPhase(Phase{Step: Preparing})
	start := time.Now()
	err := s.extract(s.ctx, s.itemsDir)
	took := time.Since(start)
	s.mu.Lock()
	s.extracted = true
	s.extractDur = took
	if err != nil {
		s.failed = true
		s.failErr = err
	}
	s.mu.Unlock()
	onDisk := s.bytesOnDisk()
	if err != nil {
		reason := Failed
		if s.ctx.Err() != nil || errors.Is(err, context.Canceled) {
			reason = Cancelled
		}
		s.log("drag %s: the extraction %s after %s with %d bytes on disk: %v -- GetData fails rather than name files that are not there", s.id, reason, took.Round(time.Millisecond), onDisk, sansPath(err))
		s.reportDone(reason)
		return err
	}
	s.log("drag %s: the extraction wrote %d item(s), %d bytes in %s -- the target waited that long inside GetData", s.id, len(s.paths), onDisk, took.Round(time.Millisecond))
	s.onPhase(Phase{Step: Awaiting})
	return nil
}

// planned is the plan's own count: the files among the items and their
// bytes, which is what the strip's bar is over.
func (s *stage) planned() (files int, bytes uint64) {
	for _, it := range s.items {
		if !it.IsDir {
			files++
			bytes += it.Size
		}
	}
	return files, bytes
}

// bytesOnDisk sums what the staged files hold right now — one Lstat per
// file, no name in any log line — so the extraction's end can say how much
// it wrote, and a failure how much it left.
func (s *stage) bytesOnDisk() uint64 {
	var n uint64
	for i, p := range s.paths {
		if s.items[i].IsDir {
			continue
		}
		if fi, err := os.Lstat(p); err == nil && fi.Mode().IsRegular() && fi.Size() > 0 {
			n += uint64(fi.Size())
		}
	}
	return n
}

// markHandedOut is the manifest transition that matters most to the
// scavenger: a target has been given paths, so the folder has to outlive
// the drop, and only the sweep may take it from here.
func (s *stage) markHandedOut() {
	s.mu.Lock()
	first := !s.handedOut
	s.handedOut = true
	s.mu.Unlock()
	if first {
		s.setState(StateHandedOut)
	}
}

// reportDone tells the caller how the drag ended, once.
func (s *stage) reportDone(r Reason) {
	s.mu.Lock()
	if s.phaseDone {
		s.mu.Unlock()
		return
	}
	s.phaseDone = true
	s.mu.Unlock()
	s.onPhase(Phase{Step: Done, Reason: r})
}

// ---------------------------------------------------------------------------
// The end of the drag.
//
// The data object does not offer IDataObjectAsyncCapability, and that one
// omission is what makes this half short (APP.md §3, ruled 2026-09-11):
// Windows then requires the target to finish the drop inside IDropTarget::
// Drop — Explorer's conflict dialog, the user's Replace, Skip or cancel,
// and the copy itself all happen before DoDragDrop returns, and the return
// carries the real effect. So there is nothing to watch and nothing to
// guess: the drag ends when DoDragDrop returns and the effect says how.
// What the return does not say is when the target is done with the staged
// files — measured, it may be a quarter of a minute later — so the folder
// outlives the drop wherever anything was handed out of it.

// dragEnded is DoDragDrop returning: the whole end of the drag, reason and
// cleanup, decided here and now. end is what the call itself reported —
// Cancelled for DRAGDROP_S_CANCEL, Failed for a call that could not run,
// zero for the drop — and effect its out-parameter.
func (s *stage) dragEnded(end Reason, effect uint32) {
	s.mu.Lock()
	s.dragOver = true
	s.dragEnd = end
	req, early := s.requests, s.early
	s.mu.Unlock()
	s.log("drag %s: DoDragDrop returned after %d CF_HDROP request(s), %d of them during the hover", s.id, req, early)
	f := s.dropFactsNow(end, effect)
	s.reportDone(dropReason(f))
	d := dropCleanup(f)
	s.log("drag %s: cleanup: %s -- %s", s.id, d.action, d.why)
	if d.action == stageDelete {
		s.remove()
		return
	}
	s.finish()
}

// dropFactsNow is the snapshot the two decisions are taken over. Everything
// in it was recorded as it happened, save the one look at the disk that
// says whether the items are still there — which is what a move looks like
// from the source's side, and the only thing a target never tells anybody.
func (s *stage) dropFactsNow(end Reason, effect uint32) dropFacts {
	s.mu.Lock()
	f := dropFacts{
		selfDrop:      s.selfDrop,
		end:           end,
		effect:        effect,
		written:       s.written,
		extractFailed: s.failed,
		targetClass:   s.targetClass,
	}
	s.mu.Unlock()
	if f.written {
		f.anyLeft = s.anyStagedLeft()
	}
	return f
}

// anyStagedLeft says whether any item CF_HDROP named is still in the
// staging folder. The top-level names are the whole question: a folder
// taken away takes its subtree with it, and a same-volume move is a rename
// of exactly these paths.
func (s *stage) anyStagedLeft() bool {
	for _, p := range s.drop {
		if _, err := os.Lstat(p); err == nil {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// The end of the drop: the reason and the cleanup.
//
// Both are pure functions over facts that were recorded as they happened,
// on purpose: a table that can only be exercised by dragging a file onto
// the desktop cannot be defended.
//
// Staged plaintext has to go, and the only question is when. The third
// research pass settled it against the obvious answer: deleting whenever
// the drop LOOKS over is what took files from under FileZilla, VMware and a
// configuration dialog that kept 7-Zip's paths for minutes, and Igor
// Pavlov's own note on it is "another program can't open input files in
// that case". The window the button came up over does not settle it either,
// and that is a measurement, not a caution (docs/research/drag-out.md, "The
// drag thread, measured", 2026-09-11): a cross-volume drop onto an Explorer
// window had the staged file still open by another process 18.75 seconds
// after DoDragDrop returned — the synchronous Drop hands the copy to
// Explorer's own engine and returns before the bytes have moved. So the
// class is logged and decides nothing (APP.md §3, ruled 2026-09-11).
//
// What is left is the short list of ends at which there is provably nothing
// to wait for: a self-drop, a failed extraction, a drag that wrote nothing,
// and a move that took every item away. Everything else leaves the folder
// standing, manifested handed-out, and the sweep takes it an hour later.

// dropFacts is everything the end of a drag is decided over.
type dropFacts struct {
	selfDrop bool
	// end is what DoDragDrop itself said: Cancelled for DRAGDROP_S_CANCEL,
	// Failed for a call that could not run, zero for the drop.
	end Reason
	// effect is DoDragDrop's out-parameter, and under the synchronous
	// contract it is the truth about what the target did: NONE, COPY or
	// MOVE.
	effect uint32
	// written says the drop's own request came and the extraction was begun:
	// the target asked for the files, and anything at all may have reached
	// the disk. It is not "the paths were handed out" — a request during the
	// hover hands those out and writes nothing — but "this drop asked", and
	// an empty staging folder is nobody's plaintext, so it goes the moment
	// the drag is over.
	written       bool
	extractFailed bool
	// anyLeft: an item CF_HDROP named is still in the staging folder. False
	// after a same-volume move, which renames every one of them away.
	anyLeft bool
	// targetClass is the class of the top-level window the button came up
	// over (stage.targetClass). It is carried this far to be said in the
	// log — a drag that left its folder behind should say who it was left
	// for — and it decides nothing.
	targetClass string
}

// dropReason is how the drag ended, in the words APP.md §3 names (ruled
// 2026-09-11): self-drop, failed, cancelled, refused, moved, copied. It is
// read off DoDragDrop's return and its effect, which the synchronous
// contract makes final — the target had to finish inside Drop.
func dropReason(f dropFacts) Reason {
	switch {
	case f.selfDrop:
		return SelfDrop
	case f.end == Failed || f.extractFailed:
		// A cancel that stopped the extraction has already reported itself
		// as Cancelled when it happened; reportDone keeps the first word.
		return Failed
	case f.end == Cancelled:
		// Escape, or a release over something that would take nothing.
		return Cancelled
	case !f.written:
		// The drop happened and the target never asked for the files. A
		// request during the hover is not one: it handed out names over an
		// empty folder, and nothing was ever transferred.
		return Refused
	case f.effect == dropEffectNone:
		// The drop happened and the target did nothing with it: Explorer's
		// Skip, its dialog cancelled, a target that refused half-way. The
		// drop is over either way, which is the whole point of asking
		// DoDragDrop rather than the files (APP.md §3).
		return Cancelled
	case f.effect == dropEffectMove && !f.anyLeft:
		// A same-volume move: the target renamed every staged item away.
		return Moved
	}
	// The effect was copy, or a move that left the staged items where they
	// were — the bytes went either way, and ours are still here.
	return Copied
}

type stageAction int

const (
	stageDelete stageAction = iota + 1
	// stageLeave: leave the folder, manifested handed-out, for the
	// scavenge — the paths are a consumer's now.
	stageLeave
)

type stageDecision struct {
	action stageAction
	why    string
}

func (a stageAction) String() string {
	switch a {
	case stageDelete:
		return "delete"
	case stageLeave:
		return "leave for the scavenge"
	}
	return fmt.Sprintf("action %d", int(a))
}

// dropCleanup is what becomes of the staging folder, decided once, when
// DoDragDrop returns.
func dropCleanup(f dropFacts) stageDecision {
	switch {
	case f.selfDrop:
		return stageDecision{stageDelete, "a self-drop: nothing was extracted and the page has what it needs"}

	case f.extractFailed:
		// Half-written files, and a GetData that failed rather than naming
		// them. Nothing valid was handed out, so nothing is waiting for them.
		return stageDecision{stageDelete, "the extraction failed, so nothing usable was ever handed out"}

	case !f.written:
		// Escape, a release over nothing, a target that never asked: the
		// folder is empty whatever was asked for during the hover.
		return stageDecision{stageDelete, "the drag ended with nothing written"}

	case f.effect == dropEffectMove && !f.anyLeft:
		return stageDecision{stageDelete, "every staged item was moved away by the target; only the empty folder is left"}
	}
	// The paths are a consumer's now, and consumers open dropped paths late
	// — a browser reads a dropped file when the upload starts, a
	// configuration dialog kept 7-Zip's paths for minutes, and Explorer
	// itself was measured still reading a staged file eighteen seconds
	// after the drop (the class says who, and nothing more).
	return stageDecision{stageLeave, fmt.Sprintf("the paths were handed out to a window of class %q; the folder is left manifested for the scavenge", f.targetClass)}
}

// remove deletes the staging folder and resolves the stage. The manifest is
// moved to "done" first, on purpose: a deletion that fails then leaves
// behind a folder the next sweep — this run's, or a later launch's — will
// recognise as abandoned and take. Every attempt is logged with the error
// Windows gave, never a file name.
func (s *stage) remove() {
	s.setState(StateDone)
	err := removeStageFolder(s.root)
	if err == nil {
		s.mu.Lock()
		s.removed, s.removeErr = true, nil
		s.mu.Unlock()
		s.finish()
		s.log("drag %s: staging folder deleted", s.id)
		return
	}
	s.mu.Lock()
	s.removeErr = err
	s.mu.Unlock()
	s.log("drag %s: the staging folder could not be deleted: %v", s.id, sansPath(err))
	// removeStageFolder leaves the manifest for last, so a delete that only
	// half succeeded ordinarily leaves it standing; the one way it can still
	// be gone is the folder itself refusing to go once emptied. A folder
	// without a manifest of ours is a folder the sweep is forbidden to touch
	// ever again, which would turn a failed delete into plaintext left on
	// disk for good — so the manifest goes back, in "done", which is exactly
	// what the sweep looks for.
	if _, lerr := os.Lstat(s.root); lerr == nil {
		if _, ok := ReadManifest(s.root); !ok {
			if werr := s.persistManifest(StateDone); werr != nil {
				s.log("drag %s: the partial delete took the manifest and it could not be rewritten (%v): the scavenge will not recognise the folder", s.id, sansPath(werr))
			} else {
				s.log("drag %s: the partial delete took the manifest; rewritten as %q so the scavenge still recognises the folder", s.id, StateDone)
			}
		}
	}
	// Left for the sweep. Nothing is retried here every 250 ms: a consumer
	// holding a file open would produce the same line four times a second.
	s.log("drag %s: the folder is left manifested %q for the scavenge", s.id, StateDone)
	s.finish()
}

// finish resolves the stage: it leaves the live set, so the scavenge may
// take the folder from here if one was left behind.
func (s *stage) finish() {
	s.mu.Lock()
	s.deleted = true
	s.mu.Unlock()
	liveStages.mu.Lock()
	delete(liveStages.m, s.root)
	liveStages.mu.Unlock()
}

// closeQuietly is the process ending with this stage unresolved. It does
// NOT delete a folder whose paths were handed out: the consumer may open
// them minutes later, and this process ending says nothing about that. The
// folder is manifested, and the sweep — the next launch's — takes it once
// it is past the age limit. A folder nothing was ever handed out of goes
// now.
func (s *stage) closeQuietly() {
	s.mu.Lock()
	deleted, handed := s.deleted, s.handedOut
	s.mu.Unlock()
	if deleted {
		return
	}
	if handed {
		s.log("drag %s: closing with the paths handed out; the folder is left manifested for the scavenge (consumers open dropped paths late)", s.id)
		s.finish()
		return
	}
	s.log("drag %s: closing with nothing handed out; the folder goes now", s.id)
	s.remove()
}

// closeAllStages is the close at exit, over every registered stage: an
// earlier drag's folder is no less this run's to decide about than the
// last one's.
func closeAllStages() {
	for _, s := range remainingStages() {
		s.closeQuietly()
	}
}

// remainingStages is every staging folder this process made and has not
// resolved, in a fixed order.
func remainingStages() []*stage {
	liveStages.mu.Lock()
	defer liveStages.mu.Unlock()
	out := make([]*stage, 0, len(liveStages.m))
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

// sansPath is an error as the log may carry it: an *os.PathError names the
// file it failed on, and nothing here is logged with a file name in it
// (APP.md §3) — the operation and the error Windows gave say what
// happened, and the drag's id says where.
func sansPath(err error) error {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return fmt.Errorf("%s: %w", pe.Op, pe.Err)
	}
	return err
}

// ---------------------------------------------------------------------------
// Deleting a tree without ever following a reparse point.
//
// APP.md §3 says the sweep never traverses a reparse point, and that rule is
// worth implementing rather than inheriting: a junction under a staging
// folder — however it got there — would otherwise turn a delete of our own
// temporary files into a delete of whatever it points at. A reparse point
// is removed as the link it is, and never descended into.

// removeStageFolder deletes one staging folder in the order that keeps the
// manifest last: the items and whatever else stands at the top first, and
// the manifest only once every one of them is gone. A delete that fails
// half-way therefore leaves a manifested folder — one the sweep may still
// act on — and never a folder of plaintext the sweep is forbidden to touch
// (APP.md §3). The folder's own removal comes after the manifest's; a folder
// that is already gone is not an error.
func removeStageFolder(root string) error {
	info, err := os.Lstat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !info.IsDir() || isReparsePoint(root) {
		return os.Remove(root)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	var first error
	for _, e := range entries {
		if e.Name() == manifestName {
			continue
		}
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
	if first != nil {
		return first
	}
	if err := os.Remove(filepath.Join(root, manifestName)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Remove(root)
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
		// Removing the link itself, which is what os.Remove does for a
		// junction or a symlink: RemoveDirectory deletes the reparse point,
		// not the directory it names.
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
// The scavenge.
//
// This is the policy, not the backstop. A drag hands out paths and then has
// no idea what happens to them; the folder stays, and a sweep at launch and
// every ten minutes afterwards removes the manifested folders that are past
// their age and not live. WinRAR's hour is the threshold, for WinRAR's
// stated reason — "external applications may still need them" — and
// against a longer one because what lingers is plaintext.
//
// "Not live" is the manifest's word AND its owner's: a folder saying "live"
// whose process is no longer there is a drag that died with it, and is
// swept under the ordinary age rule like any other. A folder whose owner
// cannot be asked after is left alone at any age, as it always was
// (manifestOwner).

// scavengeFacts is everything a sweep may look at before deciding to delete
// a directory. Keeping it a struct is what makes the decision testable
// without a file system, and what makes it obvious that "is it ours" is
// checked before anything else.
type scavengeFacts struct {
	reparse      bool
	haveManifest bool // ... and it is ours: ReadManifest already refused the rest
	state        string
	age          time.Duration
	maxAge       time.Duration
	active       bool       // a stage this process is still watching
	owner        ownerState // the process the manifest names, as far as this sweep could tell
}

// scavengeVerdict decides one directory. Every "no" carries its reason,
// because a scavenger that silently leaves things behind is
// indistinguishable from one that is broken.
//
// A live manifest is swept for exactly one reason: its owner is known to be
// gone. Not because it is old — no age makes it safe to delete under a drag
// this sweep cannot vouch for, and an unknown owner is left alone forever,
// which is the rule as it stood before the owner was recorded at all.
func scavengeVerdict(f scavengeFacts) (remove bool, why string) {
	switch {
	case f.reparse:
		return false, "a reparse point: never followed, never removed by the sweep"
	case !f.haveManifest:
		return false, "no manifest of ours: not this program's folder"
	case f.active:
		return false, "this process is still watching this drag"
	case f.state == StateLive && f.owner == ownerAlive:
		return false, "the manifest says the drag is still live and the process that wrote it is running"
	case f.state == StateLive && f.owner != ownerGone:
		return false, "the manifest says the drag is still live and names no owner this sweep could ask after"
	case f.age <= f.maxAge:
		return false, fmt.Sprintf("only %s old, under the %s limit", f.age.Round(time.Second), f.maxAge)
	case f.state == StateLive:
		return true, fmt.Sprintf("manifested %q with its owner gone, %s old, past the %s limit", f.state, f.age.Round(time.Second), f.maxAge)
	}
	return true, fmt.Sprintf("manifested %q, %s old, past the %s limit", f.state, f.age.Round(time.Second), f.maxAge)
}

// scavengeBackoff is the bounded retry APP.md §3 names: 1 s, 10 s, 60 s,
// and then the folder is left for the next sweep rather than retried
// forever.
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

// ScavengeReport is what one sweep came to. InUse is counted in Left as
// well: those are the folders the next sweep — this run's or the next
// launch's — is expected to take, and they are worth a number of their own
// because a sweep that removes nothing for that reason is working exactly
// as intended. Unreached is what a capped pass never looked at, and is
// counted nowhere else.
type ScavengeReport struct {
	Removed   int
	Left      int
	InUse     int
	Unreached int
}

// sweepMode is what one pass may do beyond a first attempt at a folder.
// The sweep while running has all the time in the world and retries a
// folder in use with the bounded backoff; the sweep at a normal exit is a
// guest in the shutdown's budget and does neither — it makes one pass, with
// no wait anywhere in it, and leaves the rest to the next launch.
type sweepMode struct {
	retry    bool      // the bounded backoff between attempts
	deadline time.Time // when the pass must stop; zero: no cap
}

// sweepNow is the clock the cap watches, a variable so that a test of the
// cap need not race a real one. The ages are judged by the caller's own
// now, which is a parameter.
var sweepNow = time.Now

// Scavenge is one sweep of root: every manifested folder of ours that no
// live drag holds — its state is not live, or the process whose drag it was
// is gone — that this process is not watching, and that is older
// than olderThan is removed, a folder something still has open retried with
// the bounded backoff and left for the next sweep. It can sit in that
// backoff for over a minute, so the caller runs it on a goroutine of its
// own. Every decision is logged, never with a file name.
func Scavenge(root string, olderThan time.Duration, log func(format string, args ...any)) ScavengeReport {
	if log == nil {
		log = discard
	}
	rep := scavenge(root, olderThan, sweepNow(), sweepMode{retry: true}, log)
	log("drag scavenge: %d removed, %d left (%d in use by another process)", rep.Removed, rep.Left, rep.InUse)
	return rep
}

// ScavengeAtExit is the sweep a normal exit makes (APP.md §3, ruled
// 2026-09-11: Windows cleans up for nobody — %TEMP% is not emptied at a
// boot, a delete-at-reboot needs an administrator and under Fast Startup a
// shutdown is a hibernation that never processes one — so this program is
// the only cleaner its leavings have). It is ONE pass with no backoff
// anywhere in it: a folder in use is left where it stands for the next
// launch rather than waited on. It is bounded to budget of wall time as
// well, since the shutdown's budget is not the sweep's to spend, and what
// the cap stopped it from reaching is logged and left; a budget of zero or
// less is no cap at all. It returns what the pass came to.
func ScavengeAtExit(root string, olderThan, budget time.Duration, log func(format string, args ...any)) ScavengeReport {
	if log == nil {
		log = discard
	}
	now := sweepNow()
	var mode sweepMode
	if budget > 0 {
		mode.deadline = now.Add(budget)
	}
	rep := scavenge(root, olderThan, now, mode, log)
	log("drag scavenge at exit: %d removed, %d left (%d in use, the next launch's), %d not reached within %s",
		rep.Removed, rep.Left, rep.InUse, rep.Unreached, budget)
	return rep
}

func scavenge(root string, maxAge time.Duration, now time.Time, mode sweepMode, log func(string, ...any)) ScavengeReport {
	var rep ScavengeReport
	entries, err := os.ReadDir(root)
	if err != nil {
		if !os.IsNotExist(err) {
			log("drag scavenge: cannot read the drag root: %v", sansPath(err))
		}
		return rep
	}
	for i, e := range entries {
		if !mode.deadline.IsZero() && !sweepNow().Before(mode.deadline) {
			// Between folders, never inside one: a folder half deleted is
			// still manifested and still the next sweep's (removeStageFolder
			// keeps the manifest for last), but a pass that stopped in the
			// middle of one would have paid for the cap twice over.
			rep.Unreached = len(entries) - i
			log("drag scavenge: the pass stopped at its cap with %d of %d entries not reached; they are the next sweep's", rep.Unreached, len(entries))
			break
		}
		path := filepath.Join(root, e.Name())
		if !e.IsDir() {
			// A stray file under the drag root is not a staging folder and
			// is not ours to remove either.
			rep.Left++
			continue
		}
		facts := scavengeFacts{reparse: isReparsePoint(path), maxAge: maxAge, active: stageIsActive(path)}
		if m, ok := ReadManifest(path); ok {
			facts.haveManifest = true
			facts.state = m.State
			facts.age = now.Sub(m.Created)
			// Only a live manifest turns on the answer, and the question
			// costs an OpenProcess, so it is not asked of the rest.
			if m.State == StateLive {
				facts.owner = ownerOf(m)
			}
		}
		remove, why := scavengeVerdict(facts)
		if !remove {
			log("drag scavenge: leaving %s: %s", e.Name(), why)
			rep.Left++
			continue
		}
		var m Manifest
		if facts.haveManifest {
			m, _ = ReadManifest(path)
		}
		switch sweepOne(path, e.Name(), why, m, mode, log) {
		case sweptRemoved:
			rep.Removed++
		case sweptInUse:
			rep.InUse++
			rep.Left++
		default:
			rep.Left++
		}
	}
	return rep
}

// sleepBackoff is the wait between a sweep's attempts, a variable so that a
// test of the bounded backoff need not sit through 71 seconds of it.
var sleepBackoff = time.Sleep

// sweepResult is what one folder's attempt came to: gone, held open by
// somebody, or a delete that failed for another reason. The last two are
// both "left", and the sweep counts them apart because only one of them
// says the folder is somebody's to give back.
type sweepResult int

const (
	sweptRemoved sweepResult = iota
	sweptInUse
	sweptFailed
)

// sweepOne removes one folder. The exclusive-open check comes first, over
// every staged file, BEFORE anything is deleted (APP.md §3): a sharing
// violation is a consumer still reading from a drag that ended an hour ago,
// and a consumer that opened its file with FILE_SHARE_DELETE would not stop
// a delete — the file would go from under it, which is the very thing the
// hour was for. A folder in use is retried with the bounded backoff and
// left for the next sweep — or, in a pass that does not retry, left at once.
// The delete itself takes the items first and the manifest last, and a
// delete that fails anywhere puts the manifest back in "done" — as
// stage.remove does — so that the next sweep may still act; every attempt's
// error is logged as Windows gave it, never a name.
func sweepOne(path, name, why string, m Manifest, mode sweepMode, log func(string, ...any)) sweepResult {
	log("drag scavenge: removing %s: %s", name, why)
	for attempt := 0; ; attempt++ {
		outcome := sweptFailed
		busy, probeErr := stagedFilesInUse(path)
		if probeErr != nil {
			// Not a sharing answer (ERROR_ACCESS_DENIED and the like): it
			// says nothing about a consumer, and the delete below is what
			// finds out.
			log("drag scavenge: %s: a staged file could not be probed: %v", name, probeErr)
		}
		if busy > 0 {
			outcome = sweptInUse
			log("drag scavenge: %s: %d staged file(s) in use by another process (a sharing violation on an exclusive open); nothing deleted", name, busy)
		} else {
			err := removeStageFolder(path)
			if err == nil {
				log("drag scavenge: removed %s", name)
				return sweptRemoved
			}
			log("drag scavenge: could not remove %s: %v", name, sansPath(err))
			restoreManifest(path, name, m, log)
		}
		if !mode.retry {
			log("drag scavenge: leaving %s for the next launch (this pass waits for nothing)", name)
			return outcome
		}
		wait, more := scavengeBackoff(attempt)
		if !more {
			log("drag scavenge: leaving %s for the next sweep (the backoff is bounded: 1 s, 10 s, 60 s)", name)
			return outcome
		}
		sleepBackoff(wait)
	}
}

// restoreManifest puts a folder's manifest back in "done" after a delete
// that failed: the one it had, its creation time kept so the age the sweep
// judges by is the drag's and not the failure's, or a fresh one in the same
// state when the folder never had one of ours to give back.
func restoreManifest(path, name string, m Manifest, log func(string, ...any)) {
	if _, err := os.Lstat(path); err != nil {
		return
	}
	if cur, ok := ReadManifest(path); ok && cur.State == StateDone {
		return
	}
	if m.Tool != manifestTool {
		pid, started := selfOwner()
		m = Manifest{Tool: manifestTool, Version: manifestVersion, PID: pid, PIDStarted: started, Created: time.Now()}
	}
	m.State = StateDone
	if err := WriteManifest(path, m); err != nil {
		log("drag scavenge: %s: the manifest could not be rewritten as %q (%v): the next sweep may not recognise the folder", name, StateDone, sansPath(err))
		return
	}
	log("drag scavenge: %s: the manifest is rewritten as %q for the next sweep", name, StateDone)
}

// stagedFilesInUse asks the exclusive-open question of every file under a
// staging folder — the manifest excepted, and a reparse point never entered
// — and counts the sharing violations. The first error that is not a
// sharing answer is returned beside the count; the count is what decides.
func stagedFilesInUse(root string) (busy int, err error) {
	var first error
	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, werr error) error {
		if werr != nil {
			if path == root && os.IsNotExist(werr) {
				return filepath.SkipAll
			}
			if first == nil {
				first = sansPath(werr)
			}
			return nil
		}
		if path != root && isReparsePoint(path) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || (filepath.Dir(path) == root && d.Name() == manifestName) {
			return nil
		}
		inUse, _, perr := stagedFileInUse(path)
		if perr != nil {
			if first == nil {
				first = sansPath(perr)
			}
			return nil
		}
		if inUse {
			busy++
		}
		return nil
	})
	if walkErr != nil && first == nil {
		first = sansPath(walkErr)
	}
	return busy, first
}

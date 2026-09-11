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
// small JSON file naming the tool that made it, when, which process, what
// state the drag is in and which files it staged. The sweep acts on folders
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
	manifestName    = "manifest.json"
	itemsDirName    = "items"
	manifestTool    = "enfold"
	manifestVersion = 1

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
type Manifest struct {
	Tool    string    `json:"tool"`
	Version int       `json:"version"`
	PID     int       `json:"pid"`
	Created time.Time `json:"created"`
	State   string    `json:"state"`
	Files   []string  `json:"files"`
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
// sweep at launch (APP.md §3).
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

// stagedFile is one item's half of the observation. Nothing here comes from
// the target: an exclusive open says whether somebody has the file open, and
// the item's presence says whether a move has already taken it away. Between
// them they are everything a source learns about a consumer that never
// calls EndOperation — which, for a CF_HDROP source, Explorer is (measured
// 2026-09-11: it negotiates the protocol and never ends it). A folder is
// watched for its presence alone: a consumer never opens one the way it
// opens a file, and a selection that is only folders — an empty one, a tree
// of empty ones — has nothing else to be seen leaving by.
type stagedFile struct {
	path     string
	isDir    bool
	exists   bool
	seen     bool // it existed at least once, so a later absence is a removal
	inUse    bool
	everUsed bool
	gone     bool
	// since is when the current state began, for the "in use for 3.2s" half
	// of the transition lines.
	since time.Time
}

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
	// maxAge is the scavenge rule inside the process: a folder handed out
	// and older than this is taken by the watch itself.
	maxAge time.Duration

	// extractMu serialises the extraction itself, and is deliberately not
	// mu: the extraction runs for as long as the files take, and everything
	// else about the stage — the counters, the watch, the cleanup decision,
	// the close guard — has to stay answerable while it does.
	extractMu sync.Mutex

	mu         sync.Mutex
	created    time.Time
	state      string
	requests   int // GetData(CF_HDROP) calls, all of them
	early      int // ... of which arrived before the button came up
	armed      bool
	releasedAt time.Time
	selfDrop   bool
	extracted  bool
	failed     bool
	failErr    error
	// written says the extraction was begun, so anything at all may be on
	// disk: half a file is plaintext somebody has to delete.
	written   bool
	handedOut bool
	asyncOp   bool // StartOperation was called: the target negotiated the protocol
	endOp     bool
	dragOver  bool
	// dragEnd is what DoDragDrop's own return said when it said anything
	// final: Cancelled, Refused or Failed, and zero for a drop the target
	// may still be working on.
	dragEnd Reason
	// released: the target let go of the data object, so no request can
	// arrive any more. It is what turns "nothing was written" into a
	// decision rather than a wait.
	released bool
	deleted  bool
	watching bool
	watch    []stagedFile
	looked   bool
	lastWhy  string
	// phaseDone: Done has been reported, once.
	phaseDone bool
	// lastTouch is the last moment anything happened to the staged files —
	// the extraction ended, a file was opened or let go of, the target
	// released the object — which is what "left alone for five seconds"
	// counts from.
	lastTouch  time.Time
	extractDur time.Duration

	// removed is the folder actually being gone from disk, and it is
	// deliberately not the same fact as deleted: deleted means the stage is
	// resolved and its watch stopped, which a delete that FAILED also is.
	// Only removed says there is nothing left, and removeErr is why there
	// is.
	removed   bool
	removeErr error

	stopOnce sync.Once
	stop     chan struct{}
}

// liveStages is every stage this process still watches, by folder. The
// scavenge consults it so that the ten-minute sweep never races the watch of
// a folder this run is still looking after, and the close at exit walks it
// so that a drag left behind by an earlier gesture is decided about rather
// than forgotten. A stage leaves this set the moment its fate is decided, so
// that a folder whose delete failed is the sweep's to take from then on.
var liveStages = struct {
	mu sync.Mutex
	m  map[string]*stage
}{m: make(map[string]*stage)}

// idleAfter is how long the staged files have to be left alone, once read
// or once the target let the object go, before the awaiting phase ends as
// Idle (APP.md §3: "read and then left alone for five seconds").
const idleAfter = 5 * time.Second

// newStage makes the folder, writes the manifest and works out the paths,
// all BEFORE DoDragDrop — which is the whole point of the early half of the
// protocol, and the reason a hover-time request can be answered with the
// final names. Root is a parameter so that a test can point the whole
// mechanism at t.TempDir(): nothing in the test suite may write under
// %LOCALAPPDATA%.
func newStage(opts Options, maxAge time.Duration) (*stage, error) {
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
		log: log, ctx: ctx, cancel: cancel, maxAge: maxAge,
		created: time.Now(), state: StateLive, stop: make(chan struct{}),
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
	if err := WriteManifest(root, Manifest{
		Tool: manifestTool, Version: manifestVersion, PID: os.Getpid(),
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
	return WriteManifest(s.root, Manifest{
		Tool: manifestTool, Version: manifestVersion, PID: os.Getpid(),
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
func (s *stage) arm(selfDrop bool) {
	s.mu.Lock()
	already := s.armed
	s.armed = true
	if !already {
		s.releasedAt = time.Now()
		s.selfDrop = selfDrop
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
	s.log("drag %s: the button was released; the extraction is armed (%d early request(s))", s.id, early)
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
	s.lastTouch = time.Now()
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
// The events the drag feeds in.

// noteAsyncStarted is StartOperation: the target negotiated the
// asynchronous protocol.
func (s *stage) noteAsyncStarted() {
	s.mu.Lock()
	s.asyncOp = true
	s.mu.Unlock()
	s.log("drag %s: the target negotiated the asynchronous protocol", s.id)
}

// noteEndOperation is EndOperation, arriving on whatever thread the target
// calls it on. Nothing is deleted here: the watch takes the decision on its
// own goroutine, within 250 ms, so that a delete never runs inside a call
// the target is waiting to return from.
func (s *stage) noteEndOperation() {
	s.mu.Lock()
	s.endOp = true
	s.lastTouch = time.Now()
	s.mu.Unlock()
	s.log("drag %s: EndOperation -- it ends the target's transfer, not every later use of the paths", s.id)
}

// noteReleased is the data object dying: the target has let go of it, so
// no request can arrive from here on.
func (s *stage) noteReleased() {
	s.mu.Lock()
	s.released = true
	s.lastTouch = time.Now()
	s.mu.Unlock()
}

// noteDragEnded is DoDragDrop returning. A self-drop deletes the folder now
// and says so; a cancel and a failure are final too; a drop is the target's
// to go on with, and the watch says how it ends.
func (s *stage) noteDragEnded(end Reason) {
	s.mu.Lock()
	s.dragOver = true
	s.dragEnd = end
	selfDrop := s.selfDrop
	req, early := s.requests, s.early
	s.mu.Unlock()
	s.log("drag %s: DoDragDrop returned after %d CF_HDROP request(s), %d of them during the hover", s.id, req, early)
	switch {
	case selfDrop:
		s.remove()
		s.reportDone(SelfDrop)
		return
	case end == Cancelled, end == Failed:
		s.reportDone(end)
	}
	s.startWatch()
}

// ---------------------------------------------------------------------------
// Watching the consumer, without being told anything by it.
//
// EndOperation is the only thing a source is ever told, and Explorer never
// sends it for a CF_HDROP source (measured 2026-09-11). So the files are
// watched directly: an exclusive open fails while somebody else has the
// file open, and a file that has disappeared has been moved away by a
// target that took the drag as a move. Neither needs the target's
// cooperation.

// startWatch begins the 250 ms poll over the staged items. It also drives
// the cleanup decision, on its own goroutine, so that a decision taken while
// the target is inside EndOperation is never carried out on the target's
// thread — deleting 5 GiB inside a call the target is waiting to return from
// would be a stall this program caused.
func (s *stage) startWatch() {
	if !s.armWatch() {
		return
	}
	go s.watchLoop()
}

// armWatch is startWatch without the goroutine: it sets the watch up and
// says whether there is one to run. Separated so that a test can drive poll
// by hand at the moment of its choosing — the first tick above all, which is
// where the interesting transition turned out to be. Every item is watched,
// folders included: "every staged item gone" is what a move looks like, and
// a selection of empty folders has no file to be seen leaving by.
func (s *stage) armWatch() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.watching || s.deleted {
		return false
	}
	s.watching = true
	now := time.Now()
	s.watch = make([]stagedFile, 0, len(s.paths))
	for i, p := range s.paths {
		s.watch = append(s.watch, stagedFile{path: p, isDir: s.items[i].IsDir, since: now})
	}
	return true
}

func (s *stage) watchLoop() {
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

// fileProbe is one exclusive open's answer about a staged file. It is a
// value rather than a call inside the transition logic so that the whole
// table — including the first observation — can be tested without a file
// system and without a drag.
type fileProbe struct {
	exists bool
	inUse  bool
}

// observe folds one probe into a watched file's state and returns the line
// the transition is worth, or "". It mutates w and nothing else; i is the
// file's index, which is how the log names it (never by its name).
//
// staged says the extraction had written every staged file, and it is what
// makes the FIRST observation a transition like any other. A same-volume
// drop is a rename, and Explorer can finish it inside the 250 ms before the
// first tick — in the round that found this, the desktop copy's mtime was
// the staged file's to the millisecond. A shape with no case for "missing
// the first time it was looked at" said nothing, the file never counted as
// gone, and the stage sat in "handed-out" over an empty folder until the
// scavenge took it an hour later. A completed move is the cleanest end a
// drag has, and it has to be recognised as one.
func (w *stagedFile) observe(p fileProbe, staged bool, now time.Time, i int) string {
	kind := w.kind()
	if !w.seen {
		switch {
		case p.exists:
			w.seen, w.exists, w.since = true, true, now
			w.inUse = p.inUse
			if p.inUse {
				w.everUsed = true
			}
			return ""
		case staged:
			w.seen, w.gone, w.exists, w.since = true, true, false, now
			return fmt.Sprintf("%s %d gone before the first look: the target took it within the first 250 ms", kind, i)
		default:
			// Nothing was ever written here: the drag ended before the
			// extraction ran, or it failed short of this item. An absence is
			// not a removal, and there is nothing to conclude from it.
			return ""
		}
	}
	switch {
	case p.exists && w.gone:
		w.gone, w.exists, w.since = false, true, now
		return fmt.Sprintf("%s %d is back", kind, i)
	case !p.exists && !w.gone:
		// An item that was there and is not any more was taken, not closed:
		// this is what a same-volume move looks like from the source's side,
		// and it is the one outcome that leaves nothing to clean up.
		line := fmt.Sprintf("%s %d gone (moved away by the target) after %s", kind, i, now.Sub(w.since).Round(time.Millisecond))
		w.gone, w.exists, w.inUse, w.since = true, false, false, now
		return line
	case !p.exists:
		return ""
	}
	if p.inUse != w.inUse {
		var line string
		if p.inUse {
			line = fmt.Sprintf("%s %d in use by another process (free for %s before this)", kind, i, now.Sub(w.since).Round(time.Millisecond))
			w.everUsed = true
		} else {
			line = fmt.Sprintf("%s %d free (in use for %s)", kind, i, now.Sub(w.since).Round(time.Millisecond))
		}
		w.inUse = p.inUse
		w.since = now
		return line
	}
	return ""
}

// kind is the word a transition line names the item by — never its name.
func (w *stagedFile) kind() string {
	if w.isDir {
		return "staged folder"
	}
	return "staged file"
}

// probe is one look at a staged item. A file is asked the exclusive-open
// question; a folder is asked only whether it is still there, since an
// exclusive open is not a question a directory answers and a consumer never
// holds one the way it holds a file — its going is what the watch learns
// from it, folders being moved whole.
func (w *stagedFile) probe() (fileProbe, error) {
	if !w.isDir {
		inUse, exists, err := stagedFileInUse(w.path)
		return fileProbe{exists: exists, inUse: inUse}, err
	}
	if _, err := os.Lstat(w.path); err != nil {
		if os.IsNotExist(err) {
			return fileProbe{}, nil
		}
		return fileProbe{}, sansPath(err)
	}
	return fileProbe{exists: true}, nil
}

// poll probes every staged item, logs the transitions, ends the awaiting
// phase when the items say it is over, and then asks the policy what to do
// with the folder. It returns true when the stage is finished with and the
// loop should end.
func (s *stage) poll(now time.Time) bool {
	var lines []string
	s.mu.Lock()
	first := !s.looked
	s.looked = true
	staged := s.extracted && !s.failed && s.written
	touched := false
	for i := range s.watch {
		w := &s.watch[i]
		p, err := w.probe()
		if err != nil {
			if first {
				lines = append(lines, fmt.Sprintf("%s %d could not be probed: %v", w.kind(), i, err))
			}
			continue
		}
		before := w.inUse
		if l := w.observe(p, staged, now, i); l != "" {
			lines = append(lines, l)
		}
		if w.inUse != before {
			touched = true
		}
	}
	if touched {
		s.lastTouch = now
	}
	ev := s.eventsLocked(now)
	s.mu.Unlock()
	for _, l := range lines {
		s.log("drag %s: %s", s.id, l)
	}
	if r := awaitingEnd(ev); r != 0 {
		// The decision below sees the end it just reported: an idle drag is
		// left for the scavenge in this tick, not the next.
		s.reportDone(r)
		ev.phaseDone = true
	}
	return s.applyDecision(decideCleanup(ev), now)
}

// eventsLocked is the snapshot the policy decides over. Everything in it is
// a fact that was recorded, which is what makes the policy a pure function
// of them and a test of the decision table possible without a drag.
func (s *stage) eventsLocked(now time.Time) stageEvents {
	ev := stageEvents{
		selfDrop:      s.selfDrop,
		written:       s.written,
		handedOut:     s.handedOut,
		extractFailed: s.failed,
		extracted:     s.extracted,
		endOperation:  s.endOp,
		asyncOp:       s.asyncOp,
		dragOver:      s.dragOver,
		dragEnd:       s.dragEnd,
		released:      s.released,
		phaseDone:     s.phaseDone,
		age:           now.Sub(s.created),
		maxAge:        s.maxAge,
	}
	// anyLeft has to be pessimistic where it does not know: it is what the
	// "everything was moved away" arm of the policy turns on, and deciding
	// that nothing is left because nothing has been looked at yet would
	// delete a folder that is full. Every item counts, folders too: a
	// selection of empty folders is moved away like anything else.
	ev.anyLeft = s.written
	if len(s.watch) > 0 && s.written && s.looked {
		ev.anyLeft = false
		for i := range s.watch {
			if s.watch[i].inUse {
				ev.anyInUse = true
			}
			if s.watch[i].everUsed {
				ev.anyEverUsed = true
			}
			if !s.watch[i].gone {
				ev.anyLeft = true
			}
		}
	}
	if !s.lastTouch.IsZero() {
		ev.untouchedFor = now.Sub(s.lastTouch)
	}
	return ev
}

// ---------------------------------------------------------------------------
// The cleanup policy.
//
// Staged plaintext has to go, and the only question is when. The third
// research pass settled it against the obvious answer: deleting when the
// drop looks over is what took files from under FileZilla, VMware and a
// configuration dialog that kept 7-Zip's paths for minutes, and Igor
// Pavlov's own note on it is "another program can't open input files in
// that case". A currently unlocked file says nothing about whether a
// consumer reopens it later. So the folder outlives the drop on purpose,
// and the sweep is what takes it.
//
// It is a pure function over recorded events on purpose: a table that can
// only be exercised by dragging a file onto the desktop cannot be defended.

type stageEvents struct {
	selfDrop bool
	// written says the extraction was begun: anything at all may have
	// reached the disk. An empty staging folder is nobody's plaintext, so
	// it goes the moment nobody can ask for it any more.
	written bool
	// handedOut is the fact the whole policy turns on: a target has been
	// given these paths and may open them at any time from now on.
	handedOut     bool
	extractFailed bool
	extracted     bool
	endOperation  bool
	asyncOp       bool
	dragOver      bool
	dragEnd       Reason
	// released: the target let go of the data object, so no request can
	// come. Without it "nothing was written" is a wait — a target that
	// negotiated the asynchronous protocol asks on a thread of its own,
	// after DoDragDrop has returned.
	released  bool
	phaseDone bool
	anyInUse  bool
	// anyLeft is false once every staged file has gone — moved away by
	// the target, which is what a same-volume move does.
	anyLeft      bool
	anyEverUsed  bool
	untouchedFor time.Duration
	age          time.Duration
	maxAge       time.Duration
}

type stageAction int

const (
	stageWait stageAction = iota
	stageDelete
	// stageLeave: stop watching and leave the folder, manifested handed-out,
	// for the scavenge — the paths are a consumer's now.
	stageLeave
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
	case stageLeave:
		return "leave for the scavenge"
	}
	return fmt.Sprintf("action %d", int(a))
}

// awaitingEnd is what ends the awaiting phase for the strip, the four ways
// APP.md §3 names: Moved when every staged item is gone, Ended when the
// target said its transfer was over, Idle when the files were read and then
// left alone for five seconds, and Idle again when the target let the data
// object go and five seconds passed with no read — a consumer that keeps
// the paths for a later read (a browser's upload box) would otherwise hold
// the strip for ever, and the folder stays for it. Refused when the target
// let the object go without ever asking for the files. Zero while there is
// nothing to say.
func awaitingEnd(e stageEvents) Reason {
	if e.phaseDone || !e.dragOver {
		return 0
	}
	if !e.written {
		if e.released && e.dragEnd == 0 {
			return Refused
		}
		return 0
	}
	if !e.extracted || e.extractFailed {
		return 0
	}
	switch {
	case !e.anyLeft:
		return Moved
	case e.endOperation && e.asyncOp && !e.anyInUse:
		return Ended
	case e.anyInUse:
		return 0
	case e.anyEverUsed && e.untouchedFor >= idleAfter:
		// Read, then left alone: untouchedFor counts from the last file
		// being let go of.
		return Idle
	case e.released && e.untouchedFor >= idleAfter:
		// The target let the object go and nothing has read the files
		// since: untouchedFor counts from the release itself, and a read
		// that began after it is the case above.
		return Idle
	}
	return 0
}

// decideCleanup is the whole policy over the folder.
func decideCleanup(e stageEvents) stageDecision {
	switch {
	case e.selfDrop && e.dragOver:
		return stageDecision{stageDelete, "a self-drop: nothing was extracted and the page has what it needs"}

	case e.dragOver && e.extractFailed:
		// Half-written files, and a GetData that failed rather than naming
		// them. Nothing valid was handed out, so nothing is waiting for them.
		return stageDecision{stageDelete, "the extraction failed, so nothing usable was ever handed out"}

	case e.dragOver && !e.written && (e.dragEnd == Cancelled || e.dragEnd == Failed):
		// Escape, a release over nothing, a DoDragDrop that could not run:
		// no drop happened, so no target is going to ask on a thread of its
		// own, and the empty folder goes now — not when whatever the cursor
		// hovered over gets round to letting the data object go (APP.md
		// §3: at once when the drag ends with nothing handed out).
		return stageDecision{stageDelete, "the drag ended without a drop and nothing was written"}

	case e.dragOver && !e.written && e.released:
		// A drop the target took and then let go of without ever asking
		// for the files: the folder is empty whatever was asked for during
		// the hover, and nobody can ask any more.
		return stageDecision{stageDelete, "the drag ended, nothing was written and the target let the data object go"}

	case e.dragOver && !e.written:
		// An accepted drop, and the target still holds the data object: one
		// that negotiated the asynchronous protocol asks on a thread of its
		// own, after DoDragDrop has returned, and the folder has to be
		// there for it.
		return stageDecision{stageWait, "nothing written yet, and the target still holds the data object"}

	case e.dragOver && e.handedOut && e.extracted && !e.anyLeft:
		// A same-volume move: the target renamed every staged file away, so
		// the folder is empty of everything but the manifest.
		return stageDecision{stageDelete, "every staged file was moved away by the target; only the empty folder is left"}

	case e.endOperation && e.asyncOp && !e.anyInUse:
		// The documented end of an asynchronous transfer — and it ends that
		// transfer, not every later use of the paths by whatever the target
		// was. It is still the strongest signal a source is given, and
		// APP.md §3 takes it as one.
		return stageDecision{stageDelete, "EndOperation on a negotiated asynchronous transfer: the target's transfer is over"}

	case e.anyInUse:
		return stageDecision{stageWait, "a staged file is in use by another process"}

	case e.handedOut && e.age > e.maxAge:
		return stageDecision{stageDelete, fmt.Sprintf("the scavenge rule: handed out, %s old, past the %s limit", e.age.Round(time.Second), e.maxAge)}

	case e.handedOut && e.phaseDone:
		// The awaiting phase is over and the paths are a consumer's: the
		// folder outlives the drop until the scavenge takes it, because
		// consumers open dropped paths late, and there is nothing left for
		// a poll four times a second to learn.
		return stageDecision{stageLeave, "the paths were handed out; the folder is left manifested for the scavenge"}

	case e.handedOut:
		return stageDecision{stageWait, "the paths were handed out; watching the staged files"}
	}
	return stageDecision{stageWait, "waiting for the target to ask for the files"}
}

// applyDecision logs every decision that is not the one already in force,
// and carries out the ones that are not "wait". It returns true when there
// is nothing left to watch.
func (s *stage) applyDecision(d stageDecision, now time.Time) bool {
	s.mu.Lock()
	if s.deleted {
		s.mu.Unlock()
		return true
	}
	changed := d.why != s.lastWhy
	s.lastWhy = d.why
	s.mu.Unlock()
	if changed {
		s.log("drag %s: cleanup: %s -- %s", s.id, d.action, d.why)
	}
	switch d.action {
	case stageWait:
		return false
	case stageLeave:
		s.finish()
		return true
	case stageDelete:
		s.remove()
		return true
	}
	return false
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

// finish takes the stage out of the live set and stops its watch.
func (s *stage) finish() {
	s.mu.Lock()
	s.deleted = true
	s.mu.Unlock()
	liveStages.mu.Lock()
	delete(liveStages.m, s.root)
	liveStages.mu.Unlock()
	s.stopOnce.Do(func() { close(s.stop) })
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
	active       bool // a stage this process is still watching
}

// scavengeVerdict decides one directory. Every "no" carries its reason,
// because a scavenger that silently leaves things behind is
// indistinguishable from one that is broken.
func scavengeVerdict(f scavengeFacts) (remove bool, why string) {
	switch {
	case f.reparse:
		return false, "a reparse point: never followed, never removed by the sweep"
	case !f.haveManifest:
		return false, "no manifest of ours: not this program's folder"
	case f.active:
		return false, "this process is still watching this drag"
	case f.state == StateLive:
		return false, "the manifest says the drag is still live"
	case f.age <= f.maxAge:
		return false, fmt.Sprintf("only %s old, under the %s limit", f.age.Round(time.Second), f.maxAge)
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

// Scavenge is one sweep of root: every manifested folder of ours whose
// state is not live, that this process is not watching, and that is older
// than olderThan is removed, a folder something still has open retried with
// the bounded backoff and left for the next sweep. It can sit in that
// backoff for over a minute, so the caller runs it on a goroutine of its
// own. It returns what it removed and what it left, the failures among the
// latter; every decision is logged, never with a file name.
func Scavenge(root string, olderThan time.Duration, log func(format string, args ...any)) (removed, left int) {
	if log == nil {
		log = discard
	}
	return scavenge(root, olderThan, time.Now(), log)
}

func scavenge(root string, maxAge time.Duration, now time.Time, log func(string, ...any)) (removed, left int) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if !os.IsNotExist(err) {
			log("drag scavenge: cannot read the drag root: %v", sansPath(err))
		}
		return 0, 0
	}
	for _, e := range entries {
		path := filepath.Join(root, e.Name())
		if !e.IsDir() {
			// A stray file under the drag root is not a staging folder and
			// is not ours to remove either.
			left++
			continue
		}
		facts := scavengeFacts{reparse: isReparsePoint(path), maxAge: maxAge, active: stageIsActive(path)}
		if m, ok := ReadManifest(path); ok {
			facts.haveManifest = true
			facts.state = m.State
			facts.age = now.Sub(m.Created)
		}
		remove, why := scavengeVerdict(facts)
		if !remove {
			log("drag scavenge: leaving %s: %s", e.Name(), why)
			left++
			continue
		}
		var m Manifest
		if facts.haveManifest {
			m, _ = ReadManifest(path)
		}
		if sweepOne(path, e.Name(), why, m, log) {
			removed++
		} else {
			left++
		}
	}
	log("drag scavenge: %d removed, %d left", removed, left)
	return removed, left
}

// sleepBackoff is the wait between a sweep's attempts, a variable so that a
// test of the bounded backoff need not sit through 71 seconds of it.
var sleepBackoff = time.Sleep

// sweepOne removes one folder. The exclusive-open check comes first, over
// every staged file, BEFORE anything is deleted (APP.md §3): a sharing
// violation is a consumer still reading from a drag that ended an hour ago,
// and a consumer that opened its file with FILE_SHARE_DELETE would not stop
// a delete — the file would go from under it, which is the very thing the
// hour was for. A folder in use is retried with the bounded backoff and
// left for the next sweep. The delete itself takes the items first and the
// manifest last, and a delete that fails anywhere puts the manifest back in
// "done" — as stage.remove does — so that the next sweep may still act;
// every attempt's error is logged as Windows gave it, never a name.
func sweepOne(path, name, why string, m Manifest, log func(string, ...any)) bool {
	log("drag scavenge: removing %s: %s", name, why)
	for attempt := 0; ; attempt++ {
		busy, probeErr := stagedFilesInUse(path)
		if probeErr != nil {
			// Not a sharing answer (ERROR_ACCESS_DENIED and the like): it
			// says nothing about a consumer, and the delete below is what
			// finds out.
			log("drag scavenge: %s: a staged file could not be probed: %v", name, probeErr)
		}
		if busy > 0 {
			log("drag scavenge: %s: %d staged file(s) in use by another process (a sharing violation on an exclusive open); nothing deleted", name, busy)
		} else {
			err := removeStageFolder(path)
			if err == nil {
				log("drag scavenge: removed %s", name)
				return true
			}
			log("drag scavenge: could not remove %s: %v", name, sansPath(err))
			restoreManifest(path, name, m, log)
		}
		wait, more := scavengeBackoff(attempt)
		if !more {
			log("drag scavenge: leaving %s for the next sweep (the backoff is bounded: 1 s, 10 s, 60 s)", name)
			return false
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
		m = Manifest{Tool: manifestTool, Version: manifestVersion, PID: os.Getpid(), Created: time.Now()}
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

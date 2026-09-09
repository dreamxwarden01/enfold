package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dreamxwarden01/enfold/internal/archive"
	"github.com/dreamxwarden01/enfold/internal/compress"
	"github.com/dreamxwarden01/enfold/internal/format"
	"github.com/dreamxwarden01/enfold/internal/kdf"
)

// openArchive is one archive the core holds open (APP.md §2.3): the
// committed snapshot — the files and the directories, re-taken after every
// index-republishing operation — the staged overlay keyed by record id, the
// tree the two make together, the preview token, the reader count and the
// two clocks. Fields are guarded by Core.mu; opMu serialises operations that
// touch the handle.
type openArchive struct {
	id   [16]byte
	path string
	name string
	a    *archive.Archive
	opMu sync.Mutex // one operation on the handle at a time
	tx   *archive.Tx
	// The committed snapshot is both record tables: a directory is a record
	// of its own (FORMAT.md R39) and no folder is projected from a name.
	snap    []archive.FileInfo
	dirSnap []archive.DirInfo
	byID    map[[16]byte]int
	dirByID map[[16]byte]int
	overlay map[[16]byte]*pendingChange
	adds    []*pendingChange // ordered staged creations, files and folders
	seq     uint64
	state   string // open | dirty | compacting | needs_reopen
	token   string
	readers int
	lastUse time.Time
	// The handle's figures, cached at every snapshot so that nothing under
	// the state mutex takes the archive's own mutex (a running hash holds
	// it for the whole read).
	size  uint64
	files int
	free  uint64
	// copyMismatch: the file's seq is not the one the registry last saw.
	copyMismatch bool
	// Clocks. Each arm bumps its generation and the callback carries the
	// one it was armed with, so a callback that was already running when
	// the clock was re-armed or cleared does nothing.
	idleTimer  Timer
	capTimer   Timer
	idleGen    uint64
	capGen     uint64
	dirtySince time.Time
	expiresAt  time.Time
	capAt      time.Time
	extensions int
	// Registry facts.
	kid        [16]byte
	keyVersion int
	// method is the archive's compression as the views name it: one of
	// the five words of APP.md §3, read from the record's policy.
	method      string
	receiptOwed bool
	lastSavedAt int64
	// Preview quiescing for Compact.
	quiesced bool
}

// pendingChange is one staged change (APP.md §2.3): one entry per record and
// one word per entry, keyed by the record's id, files and directories alike.
// name and parentID are the record's as the merged view has it — what a
// rename and a move write — and file or dir is the staged record itself for
// a creation or a replace.
type pendingChange struct {
	kind     string // added | replaced | renamed | moved | deleted
	id       [16]byte
	isDir    bool
	name     string
	parentID [16]byte
	file     archive.FileInfo // files
	dir      archive.DirInfo  // directories
}

// The staging helpers keep §2.3's precedence. Caller holds the state mutex.

// stageAdd records a creation — a file added or a folder made — as one
// staged add: the same pendingChange in the overlay and in adds, so it can
// still be renamed, moved, replaced or un-staged as one thing.
func (oa *openArchive) stageAdd(p *pendingChange) {
	oa.overlay[p.id] = p
	oa.adds = append(oa.adds, p)
}

// stageReplace records a replaced record. `added` outlives every later
// change to a staged-added record, so replacing one leaves it an add;
// otherwise `replaced` outranks the renamed or moved word that stood there.
func (oa *openArchive) stageReplace(r *mergedRec, info archive.FileInfo) {
	if p := oa.overlay[r.id]; p != nil {
		p.file = info
		if p.kind != pendingAdded {
			p.kind = pendingReplaced
		}
		return
	}
	oa.overlay[r.id] = &pendingChange{kind: pendingReplaced, id: r.id, name: r.name, parentID: r.parentID, file: info}
}

// stageRename writes the new name onto whatever entry stands: an add stays
// an add, a replace stays a replace, and a record renamed twice is renamed.
func (oa *openArchive) stageRename(r *mergedRec, name string) {
	if p := oa.overlay[r.id]; p != nil {
		p.name = name
		return
	}
	oa.overlay[r.id] = &pendingChange{kind: pendingRenamed, id: r.id, isDir: r.isDir, name: name, parentID: r.parentID}
}

// stageMove writes the new parent. A record both renamed and moved reads
// `moved`; an add and a replace keep their word.
func (oa *openArchive) stageMove(r *mergedRec, parentID [16]byte) {
	if p := oa.overlay[r.id]; p != nil {
		p.parentID = parentID
		if p.kind == pendingRenamed {
			p.kind = pendingMoved
		}
		return
	}
	oa.overlay[r.id] = &pendingChange{kind: pendingMoved, id: r.id, isDir: r.isDir, name: r.name, parentID: parentID}
}

// committed is the record as the last published index has it — where an
// un-staged move puts it back.
func (oa *openArchive) committed(id [16]byte) (parent [16]byte, name string, ok bool) {
	if i, found := oa.dirByID[id]; found {
		d := &oa.dirSnap[i]
		return d.ParentID, d.Name, true
	}
	if i, found := oa.byID[id]; found {
		f := &oa.snap[i]
		return f.ParentID, f.Name, true
	}
	return [16]byte{}, "", false
}

// owedReceipt is a Save's receipt a lock stranded (APP.md §2.3).
type owedReceipt struct {
	kid       [16]byte
	seq       uint64
	size      uint64
	writtenAt int64
	hash      *[32]byte
}

// dirty is the number of staged changes: one overlay entry is one change,
// so a deleted folder of 900 files is one and not 901 (APP.md §2.3).
func (oa *openArchive) dirty() int {
	if oa.tx == nil {
		return 0
	}
	return len(oa.overlay)
}

// archiveIdle is the default per-archive idle timeout: the session's.
const archiveExtension = 5 * time.Minute

// findArchive returns the open archive or the code.
func (c *Core) findArchive(id string) (*openArchive, *Error) {
	aid, ok := parseID(id)
	if !ok {
		return nil, coded(CodeParams)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	oa := c.archives[aid]
	if oa == nil {
		return nil, coded(CodeArchiveNotOpen)
	}
	switch oa.state {
	case "compacting":
		return nil, coded(CodeArchiveCompacting)
	case "needs_reopen":
		return nil, coded(CodeArchiveNeedsReopen)
	}
	return oa, nil
}

// ListArchives lists the registry's archives — the session's registry when
// unlocked, the open ones alone when locked. Hidden and forgotten records
// are listed together under showHidden and the Status column separates them
// (APP.md §13). Nothing here touches the file system: file missing is the
// presence map's, refreshed by the pass after an unlock and by CheckFiles,
// and a record no pass has measured has no Note at all.
func (c *Core) ListArchives(showHidden bool) ([]ArchiveSummary, *Error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := []ArchiveSummary{}
	seen := map[[16]byte]bool{}
	if sess, e := c.sessionLocked(); e == nil {
		g := sess.Registry()
		for i := range g.Archives {
			a := &g.Archives[i]
			if (a.Policy&format.PolicyHidden != 0 || a.Forgotten()) && !showHidden {
				continue
			}
			s := ArchiveSummary{
				ID: hexID(a.ArchiveID), Name: a.Name, Path: a.LastPath, StoredSize: a.LastStoredSize,
				LastWrittenAt: a.LastWrittenAt, KeyVersion: len(a.Versions),
				Method: methodOf(a.Policy), Hidden: a.Policy&format.PolicyHidden != 0,
				HashBehind: a.LastSeq - a.HashAtSeq, Description: a.Description, ForgottenAt: a.ForgottenAt,
			}
			if _, owed := c.owed[a.ArchiveID]; owed {
				s.ReceiptOwed = true
			}
			c.decorateLocked(&s, a.ArchiveID)
			if !s.Open && c.vault.presenceKnown[a.ArchiveID] && !c.vault.presence[a.ArchiveID] {
				s.Note = CodeArchiveMissing
			}
			seen[a.ArchiveID] = true
			out = append(out, s)
		}
	}
	for id, oa := range c.archives {
		if seen[id] {
			continue
		}
		s := ArchiveSummary{ID: hexID(id), Name: oa.name, Path: oa.path, KeyVersion: oa.keyVersion, Method: oa.method, LastWrittenAt: oa.lastSavedAt}
		c.decorateLocked(&s, id)
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

// decorateLocked adds what the core knows about an open archive.
func (c *Core) decorateLocked(s *ArchiveSummary, id [16]byte) {
	oa := c.archives[id]
	if oa == nil {
		return
	}
	s.Open, s.Dirty, s.State, s.ReceiptOwed = true, oa.dirty(), oa.state, s.ReceiptOwed || oa.receiptOwed
	if oa.state != "needs_reopen" && oa.state != "compacting" {
		s.Files, s.FreeSpace = oa.files, oa.free
		if oa.size > 0 {
			s.StoredSize = oa.size
		}
	}
	if oa.copyMismatch && s.Note == "" {
		s.Note = CodeArchiveCopyMismatch
	}
}

// emitArchivesChanged tells the frontend to re-fetch the list. Purged is
// empty on every change but the purge's own (APP.md §13).
func (c *Core) emitArchivesChanged() {
	c.emit(EventArchivesChanged, ArchivesChanged{Purged: []string{}})
}

// emitArchiveChanged bumps and announces one archive's sequence.
func (c *Core) emitArchiveChanged(oa *openArchive) {
	c.mu.Lock()
	oa.seq++
	seq := oa.seq
	c.mu.Unlock()
	c.emit(EventArchiveChanged, ArchiveChanged{ID: hexID(oa.id), Seq: seq})
}

// archiveKeys unwraps every version's key of a record, current first.
// Caller holds the state mutex; the keys are the caller's to zero.
func (c *Core) archiveKeysLocked(rec *format.ArchiveRecord) ([]archive.Key, *Error) {
	sess, e := c.sessionLocked()
	if e != nil {
		return nil, e
	}
	var keys []archive.Key
	add := func(v *format.VersionRecord) *Error {
		k, err := sess.UnwrapArchiveKey(rec.ArchiveID, v)
		if err != nil {
			return c.fail("unwrap archive key", err)
		}
		keys = append(keys, archive.Key{KID: v.KID, Key: k})
		return nil
	}
	for i := range rec.Versions {
		if rec.Versions[i].KID == rec.CurrentKID {
			if e := add(&rec.Versions[i]); e != nil {
				return nil, e
			}
		}
	}
	for i := range rec.Versions {
		if rec.Versions[i].KID != rec.CurrentKID {
			if e := add(&rec.Versions[i]); e != nil {
				zeroKeys(keys)
				return nil, e
			}
		}
	}
	return keys, nil
}

func zeroKeys(keys []archive.Key) {
	for i := range keys {
		kdf.Zero(keys[i].Key[:])
	}
}

// The compression methods a create chooses between (APP.md §3, §6): the
// five words the New archive dialog's segments carry. store is the
// policy's no_compression bit — every file raw, DESIGN.md trap 8 — and
// the other four are the level of FORMAT.md §7.1 bits 3–5, which every
// later writer of the archive follows, on any machine.
const (
	compressionStore   = "store"
	compressionFastest = "fastest"
	compressionNormal  = "normal"
	compressionBetter  = "better"
	compressionBest    = "best"
)

// policyForMethod turns one of those words into the record's policy bits;
// ok is false for anything else.
func policyForMethod(method string) (uint32, bool) {
	var level uint32
	switch method {
	case compressionStore:
		return format.PolicyNoCompression, true
	case compressionFastest:
		level = format.PolicyLevelFastest
	case compressionNormal:
		// Written explicitly rather than left unset, so that the record
		// says what it was created with and not what a writer defaults to.
		level = format.PolicyLevelNormal
	case compressionBetter:
		level = format.PolicyLevelBetter
	case compressionBest:
		level = format.PolicyLevelBest
	default:
		return 0, false
	}
	p, err := format.SetPolicyLevel(0, level)
	if err != nil {
		return 0, false
	}
	return p, true
}

// methodOf names a record's compression for the views: the raw bit first,
// since it decides on its own, then the level — an unset one reading as
// the writer's default, Normal (FORMAT.md §7.1).
func methodOf(policy uint32) string {
	if policy&format.PolicyNoCompression != 0 {
		return compressionStore
	}
	switch format.PolicyLevel(policy) {
	case format.PolicyLevelFastest:
		return compressionFastest
	case format.PolicyLevelBetter:
		return compressionBetter
	case format.PolicyLevelBest:
		return compressionBest
	}
	return compressionNormal
}

// compressLevelOf maps the record's level onto the compressor's presets;
// an unset field is the default, as FORMAT.md §7.1 says.
func compressLevelOf(policy uint32) compress.Level {
	switch format.PolicyLevel(policy) {
	case format.PolicyLevelFastest:
		return compress.Fastest
	case format.PolicyLevelBetter:
		return compress.Better
	case format.PolicyLevelBest:
		return compress.Best
	}
	return compress.Default
}

// archiveOptions builds the writer options from the record and settings.
// The archive's own policy decides how it is written, whoever opens it:
// the raw bit and the level of FORMAT.md §7.1 travel with the archive.
func (c *Core) archiveOptionsLocked(rec *format.ArchiveRecord) archive.Options {
	return archive.Options{
		DeviceID:      c.deviceIDLocked(),
		NoCompression: rec.Policy&format.PolicyNoCompression != 0,
		Compress:      compress.Params{Level: compressLevelOf(rec.Policy)},
		DictBelow:     c.settings.DictionaryBelow,
	}
}

func (c *Core) deviceIDLocked() [16]byte {
	if sess, e := c.sessionLocked(); e == nil {
		return sess.Registry().DeviceID
	}
	return [16]byte{}
}

// OpenArchive opens a registry archive under the session.
func (c *Core) OpenArchive(id string) (ArchiveStat, *Error) {
	aid, ok := parseID(id)
	if !ok {
		return ArchiveStat{}, coded(CodeParams)
	}
	c.mu.Lock()
	if oa := c.archives[aid]; oa != nil {
		st := c.statLocked(oa)
		c.mu.Unlock()
		return st, nil
	}
	if c.deleting[aid] {
		// A delete has claimed this record and its file is going: it is not
		// opened between that claim and the write (APP.md §13).
		c.mu.Unlock()
		return ArchiveStat{}, coded(CodeArchiveBusy)
	}
	sess, e := c.sessionLocked()
	if e != nil {
		c.mu.Unlock()
		return ArchiveStat{}, e
	}
	rec := findRecord(sess.Registry(), aid)
	if rec == nil {
		c.mu.Unlock()
		return ArchiveStat{}, coded(CodeArchiveNotFound)
	}
	if rec.Forgotten() {
		// The keys are still there: the record is restored, not found again
		// (APP.md §13).
		c.mu.Unlock()
		return ArchiveStat{}, coded(CodeArchiveForgotten)
	}
	keys, e := c.archiveKeysLocked(rec)
	if e != nil {
		c.mu.Unlock()
		return ArchiveStat{}, e
	}
	opts := c.archiveOptionsLocked(rec)
	path, name, kid, nv, method, lastAt, lastSeq := rec.LastPath, rec.Name, rec.CurrentKID, len(rec.Versions), methodOf(rec.Policy), rec.LastWrittenAt, rec.LastSeq
	c.mu.Unlock()

	a, err := archive.Open(path, keys, opts)
	zeroKeys(keys)
	if err != nil {
		if os.IsNotExist(err) {
			return ArchiveStat{}, coded(CodeArchiveMissing)
		}
		return ArchiveStat{}, c.fail("open archive", err)
	}
	oa := &openArchive{id: aid, path: path, name: name, a: a, kid: kid, keyVersion: nv, method: method, lastSavedAt: lastAt}
	oa.token = newToken()
	oa.refreshSnapshot()
	c.mu.Lock()
	if c.archives[aid] != nil { // raced with another Open
		c.mu.Unlock()
		a.Close()
		return c.OpenArchive(id)
	}
	if c.deleting[aid] {
		// A delete claimed the record while the file was being opened: the
		// handle is dropped rather than installed over a file that is going.
		c.mu.Unlock()
		a.Close()
		return ArchiveStat{}, coded(CodeArchiveBusy)
	}
	c.archives[aid] = oa
	oa.state = "open"
	oa.lastUse = c.now()
	c.armArchiveIdleLocked(oa)
	if a.Stale() != nil || a.FreeMapRebuilt() != nil || a.EnvelopeStale() {
		c.log("archive %s opened with warnings: stale=%v freemap=%v envelope=%v", name, a.Stale(), a.FreeMapRebuilt(), a.EnvelopeStale())
	}
	if lastSeq != 0 && lastSeq != a.Seq() {
		// The file is not the copy the record last saw: shown on the
		// archive until a save records this copy, never adopted silently.
		c.log("archive %s: registry saw seq %d, file is at %d", name, lastSeq, a.Seq())
		oa.copyMismatch = true
	}
	st := c.statLocked(oa)
	c.mu.Unlock()
	c.emitArchivesChanged()
	c.emitState()
	return st, nil
}

func findRecord(g *format.Registry, id [16]byte) *format.ArchiveRecord {
	for i := range g.Archives {
		if g.Archives[i].ArchiveID == id {
			return &g.Archives[i]
		}
	}
	return nil
}

func newToken() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

// refreshSnapshot re-takes the committed view — both record tables, since
// the index is a tree — after every index-republishing operation (APP.md
// §2.3). Caller holds opMu or is the opener.
func (oa *openArchive) refreshSnapshot() {
	oa.snap = oa.a.Files()
	oa.dirSnap = oa.a.Dirs()
	oa.byID = make(map[[16]byte]int, len(oa.snap))
	for i := range oa.snap {
		oa.byID[oa.snap[i].ID] = i
	}
	oa.dirByID = make(map[[16]byte]int, len(oa.dirSnap))
	for i := range oa.dirSnap {
		oa.dirByID[oa.dirSnap[i].ID] = i
	}
	oa.size, oa.files, oa.free = oa.a.Stat()
}

// CloseArchive closes an open, clean archive.
func (c *Core) CloseArchive(id string) *Error {
	aid, ok := parseID(id)
	if !ok {
		return coded(CodeParams)
	}
	c.mu.Lock()
	oa := c.archives[aid]
	if oa != nil && (oa.state == "compacting" || oa.quiesced) {
		// A compaction holds the archive's mutex for its whole run: say so
		// now rather than after it.
		c.mu.Unlock()
		return coded(CodeArchiveCompacting)
	}
	c.mu.Unlock()
	if oa == nil {
		return coded(CodeArchiveNotOpen)
	}
	oa.opMu.Lock()
	defer oa.opMu.Unlock()
	c.mu.Lock()
	if c.archives[aid] != oa {
		c.mu.Unlock()
		return coded(CodeArchiveNotOpen)
	}
	switch oa.state {
	case "compacting":
		c.mu.Unlock()
		return coded(CodeArchiveCompacting)
	case "needs_reopen":
		// The handle is broken but still holds the file: closing it is
		// what lets a reopen succeed.
	default:
		if oa.dirty() > 0 {
			c.mu.Unlock()
			return coded(CodeArchiveDirty)
		}
	}
	c.closeArchiveLocked(oa)
	c.mu.Unlock()
	c.emitArchivesChanged()
	c.emitState()
	return nil
}

// closeArchiveLocked drops the archive: token forgotten, timers stopped,
// handle closed (which fails every in-flight preview body). Caller holds
// the state mutex and opMu.
func (c *Core) closeArchiveLocked(oa *openArchive) {
	if oa.idleTimer != nil {
		oa.idleTimer.Stop()
	}
	if oa.capTimer != nil {
		oa.capTimer.Stop()
	}
	oa.token = ""
	delete(c.archives, oa.id)
	oa.a.Close()
	oa.state = "closed"
}

// CloseAllArchives closes the clean ones and reports the dirty ones.
func (c *Core) CloseAllArchives() []string {
	c.mu.Lock()
	var list []*openArchive
	for _, oa := range c.archives {
		list = append(list, oa)
	}
	c.mu.Unlock()
	var dirty []string
	for _, oa := range list {
		if e := c.CloseArchive(hexID(oa.id)); e != nil {
			dirty = append(dirty, hexID(oa.id))
		}
	}
	return dirty
}

// statLocked builds the status strip. Caller holds the state mutex.
func (c *Core) statLocked(oa *openArchive) ArchiveStat {
	st := ArchiveStat{ID: hexID(oa.id), Name: oa.name, Seq: oa.seq, KeyVersion: oa.keyVersion, Dirty: oa.dirty(), State: oa.state, LastSavedAt: oa.lastSavedAt, ReceiptOwed: oa.receiptOwed, CopyMismatch: oa.copyMismatch}
	if oa.state != "needs_reopen" && oa.state != "compacting" {
		st.Size, st.Files, st.FreeSpace = oa.size, oa.files+oa.stagedFiles(), oa.free
	}
	if !oa.expiresAt.IsZero() {
		st.ExpiresAt = oa.expiresAt.Unix()
	}
	if !oa.capAt.IsZero() {
		st.CapAt = oa.capAt.Unix()
	}
	if _, e := c.sessionLocked(); e == nil {
		st.SessionAlive = true
	}
	return st
}

// Stat is the open archive's status strip.
func (c *Core) Stat(id string) (ArchiveStat, *Error) {
	oa, e := c.findArchive(id)
	if e != nil {
		return ArchiveStat{}, e
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statLocked(oa), nil
}

// The two clocks (APP.md §2.3).

// touchArchiveLocked records activity on the archive and re-arms its idle
// clock. Caller holds the state mutex.
func (c *Core) touchArchiveLocked(oa *openArchive) {
	oa.lastUse = c.now()
	c.armArchiveIdleLocked(oa)
}

func (c *Core) armArchiveIdleLocked(oa *openArchive) {
	if oa.idleTimer != nil {
		oa.idleTimer.Stop()
	}
	idle := c.vault.idle
	if idle == 0 {
		idle = defaultIdle
	}
	oa.expiresAt = c.now().Add(idle)
	oa.idleGen++
	gen := oa.idleGen
	oa.idleTimer = c.deps.Clock.AfterFunc(idle, func() { c.archiveIdle(oa, gen) })
}

// archiveIdle is the idle clock's expiry, for the arm it was set by.
func (c *Core) archiveIdle(oa *openArchive, gen uint64) {
	if !oa.opMu.TryLock() {
		// An operation is running: that is activity. Look again later.
		c.mu.Lock()
		if c.archives[oa.id] == oa && oa.idleGen == gen {
			c.armArchiveIdleLocked(oa)
		}
		c.mu.Unlock()
		return
	}
	defer oa.opMu.Unlock()
	c.mu.Lock()
	if c.archives[oa.id] != oa || oa.idleGen != gen {
		c.mu.Unlock()
		return // closed, or re-armed while this callback was on its way
	}
	if oa.readers > 0 || oa.state == "compacting" {
		c.armArchiveIdleLocked(oa)
		c.mu.Unlock()
		return
	}
	if oa.dirty() == 0 {
		c.closeArchiveLocked(oa)
		c.mu.Unlock()
		c.emitArchivesChanged()
		c.emitState()
		return
	}
	// Dirty: never silently closed. Prompt, with bounded extensions; the
	// cap clock is what ends it.
	if oa.extensions < 2 {
		oa.extensions++
		oa.expiresAt = c.now().Add(archiveExtension)
		oa.idleGen++
		next := oa.idleGen
		oa.idleTimer = c.deps.Clock.AfterFunc(archiveExtension, func() { c.archiveIdle(oa, next) })
		ev := ArchiveExpiring{ID: hexID(oa.id), ClosesAt: oa.expiresAt.Unix(), Dirty: oa.dirty()}
		c.mu.Unlock()
		c.emit(EventArchiveExpiring, ev)
		return
	}
	oa.expiresAt = time.Time{}
	c.mu.Unlock()
}

// KeepOpen grants one extension to a dirty archive's idle clock: a real
// user action, bounded.
func (c *Core) KeepOpen(id string) *Error {
	oa, e := c.findArchive(id)
	if e != nil {
		return e
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if oa.extensions > 4 {
		return coded(CodeArchiveDirty)
	}
	oa.extensions++
	c.armArchiveIdleLocked(oa)
	return nil
}

// markDirtyLocked starts the cap clock on the first staged change.
func (c *Core) markDirtyLocked(oa *openArchive) {
	if !oa.dirtySince.IsZero() {
		return
	}
	oa.dirtySince = c.now()
	oa.state = "dirty"
	abs := c.vault.absolute
	if abs == 0 {
		abs = defaultAbsolute
	}
	oa.capAt = oa.dirtySince.Add(abs)
	oa.capGen++
	gen := oa.capGen
	oa.capTimer = c.deps.Clock.AfterFunc(abs, func() { c.archiveCap(oa, gen) })
}

// archiveCap is the dirty cap's expiry: abort, then close, and say so. A
// callback that waited behind a Save which cleared the dirty state finds
// its generation gone and does nothing.
func (c *Core) archiveCap(oa *openArchive, gen uint64) {
	oa.opMu.Lock()
	defer oa.opMu.Unlock()
	c.mu.Lock()
	if c.archives[oa.id] != oa || oa.tx == nil || oa.capGen != gen {
		c.mu.Unlock()
		return
	}
	n := oa.dirty()
	oa.tx.Abort()
	oa.tx = nil
	oa.overlay, oa.adds = nil, nil
	c.closeArchiveLocked(oa)
	c.mu.Unlock()
	c.log("archive %s: dirty cap expired, %d changes discarded", oa.name, n)
	c.emit(EventVaultWarning, Warning{Code: "archive.changes_discarded"})
	c.emitArchivesChanged()
	c.emitState()
}

// clearDirtyLocked ends the dirty state after Save or Discard.
func (c *Core) clearDirtyLocked(oa *openArchive) {
	oa.tx = nil
	oa.overlay, oa.adds = nil, nil
	oa.dirtySince, oa.capAt = time.Time{}, time.Time{}
	oa.extensions = 0
	oa.capGen++ // a cap callback already on its way is void
	if oa.capTimer != nil {
		oa.capTimer.Stop()
		oa.capTimer = nil
	}
	if oa.state == "dirty" {
		oa.state = "open"
	}
}

// The tree the page reads (APP.md §2.3, §3): the merged view of tree.go,
// listed one directory at a time. Nothing is projected from a name.

// pendingKind is the staged word on a record, or "".
func (oa *openArchive) pendingKind(id [16]byte) string {
	if p := oa.overlay[id]; p != nil {
		return p.kind
	}
	return ""
}

// stagedFiles counts the staged adds that are files: a folder occupies no
// data region, so counting one among the files would make the number mean
// neither thing (the archive layer's Stat draws the same line).
func (oa *openArchive) stagedFiles() int {
	n := 0
	for _, p := range oa.adds {
		if !p.isDir {
			n++
		}
	}
	return n
}

// currentFile is the live file as the merged view has it. Only a file has
// content, so a directory's id answers false, as an unknown one does.
func (oa *openArchive) currentFile(fid [16]byte) (archive.FileInfo, bool) {
	r := oa.merge().live(fid)
	if r == nil || r.isDir {
		return archive.FileInfo{}, false
	}
	if p := oa.overlay[fid]; p != nil && (p.kind == pendingAdded || p.kind == pendingReplaced) {
		f := p.file
		f.Name, f.ParentID = r.name, r.parentID
		return f, true
	}
	if i, ok := oa.byID[fid]; ok {
		f := oa.snap[i]
		f.Name, f.ParentID = r.name, r.parentID
		return f, true
	}
	return archive.FileInfo{}, false
}

// Page lists the live children of one directory in the merged view, files
// and folders in one list, with the breadcrumb from the root down to it
// (APP.md §3). dirID is a record id — the all-zero id is the root — never a
// path, and one that no longer names a live directory answers file.not_found
// rather than an empty listing under a breadcrumb that still names the
// place: the page walks the Crumbs it last held upwards until one answers.
func (c *Core) Page(id, dirID, sortBy string, offset, limit int) (Page, *Error) {
	oa, e := c.findArchive(id)
	if e != nil {
		return Page{}, e
	}
	did, ok := parseID(dirID)
	if !ok {
		return Page{}, coded(CodeParams)
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.touchArchiveLocked(oa)
	m := oa.merge()
	if !m.dirUsable(did) {
		return Page{}, coded(CodeFileNotFound)
	}
	rows := make([]FileRow, 0, len(m.kids[did]))
	for _, r := range m.kids[did] {
		rows = append(rows, fileRow(m, r))
	}
	sortRows(rows, sortBy)
	total := len(rows)
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	page := Page{Seq: oa.seq, Rows: rows[offset:end], Total: total, Crumbs: m.crumbs(did, oa.name)}
	if page.Rows == nil {
		page.Rows = []FileRow{}
	}
	return page, nil
}

// fileRow renders one record. A directory's Size is the sum beneath it and
// its ModifiedAt the record's own; it has no storage of its own.
func fileRow(m *merged, r *mergedRec) FileRow {
	row := FileRow{
		ID: hexID(r.id), ParentID: hexID(r.parentID), IsDir: r.isDir,
		Name: r.name, Path: m.path(r.id), ModifiedAt: r.modifiedAt, Pending: r.pending,
	}
	if r.isDir {
		row.Size = m.sizeBeneath(r.id)
		return row
	}
	row.Size = r.size
	switch r.storage {
	case format.StorageZstd:
		row.Storage = "zstd"
	case format.StorageZstdDict:
		row.Storage = "zstd+dict"
	default:
		row.Storage = "raw"
	}
	if r.storage != format.StorageRaw && r.size > 0 && r.storedSize < r.size {
		row.SavedPercent = int((r.size - r.storedSize) * 100 / r.size)
	}
	return row
}

func sortRows(rows []FileRow, by string) {
	less := func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		switch by {
		case "size", "-size":
			if a.Size != b.Size {
				return (a.Size < b.Size) != (by == "-size")
			}
		case "modified", "-modified":
			if a.ModifiedAt != b.ModifiedAt {
				return (a.ModifiedAt < b.ModifiedAt) != (by == "-modified")
			}
		case "-name":
			return strings.ToLower(a.Name) > strings.ToLower(b.Name)
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	}
	sort.SliceStable(rows, less)
}

// CheckNames reports which of the offered names collide with a live child of
// parentID, so the UI can ask once before an add (APP.md §3). A name given
// with a trailing "/" is offered as a directory — R20 keeps a "/" out of
// every real name, so the mark is unambiguous — and the collision carries
// the kind on both sides, so the dialog can say "Photos is a file here" and
// grey Replace whenever the two differ. A staged-deleted sibling reserves no
// name and is ignored. An offered name format.ValidateName refuses is not a
// collision and is not reported here: a Collision names the record in the way
// and there is none, and the add says so where APP.md §3 puts it — one
// FileOutcome of `failed` with `file.name`, named in the results and never
// silently skipped.
func (c *Core) CheckNames(id, parentID string, names []string) ([]Collision, *Error) {
	oa, e := c.findArchive(id)
	if e != nil {
		return nil, e
	}
	pid, ok := parseID(parentID)
	if !ok {
		return nil, coded(CodeParams)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	m := oa.merge()
	if !m.dirUsable(pid) {
		return nil, coded(CodeFileNotFound)
	}
	out := []Collision{}
	for _, n := range names {
		offered := strings.TrimSuffix(n, "/")
		isDir := offered != n
		if format.ValidateName(offered) != nil {
			continue // a name the walk will refuse, not a collision
		}
		x := m.sibling(pid, offered, [16]byte{})
		if x == nil {
			continue
		}
		out = append(out, Collision{
			Name: n, IsDir: isDir, Existing: hexID(x.id),
			ExistingIsDir: x.isDir, Pending: x.pending != "",
		})
	}
	return out, nil
}

// beginLocked opens the transaction on first change. Caller holds opMu and
// the state mutex.
func (c *Core) beginLocked(oa *openArchive) error {
	if oa.tx != nil {
		return nil
	}
	tx, err := oa.a.Begin()
	if err != nil {
		return err
	}
	oa.tx = tx
	oa.overlay = map[[16]byte]*pendingChange{}
	c.markDirtyLocked(oa)
	return nil
}

// CreateFolder stages a directory record under parentID (APP.md §3): a
// folder is a record, so an empty one survives the commit and is never a
// fiction of the page (FORMAT.md R39, DESIGN.md trap 31). It is staged at
// once, as Tx.Add is, and its modified_at is the time it was made — nothing
// writes it again (R32). The name is validated and matched like any other's.
func (c *Core) CreateFolder(id, parentID, name string) (string, *Error) {
	oa, e := c.findArchive(id)
	if e != nil {
		return "", e
	}
	pid, ok := parseID(parentID)
	if !ok {
		return "", coded(CodeParams)
	}
	oa.opMu.Lock()
	defer oa.opMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	m := oa.merge()
	if !m.dirUsable(pid) {
		return "", coded(CodeFileNotFound)
	}
	if format.ValidateName(name) != nil {
		return "", coded(CodeFileName)
	}
	if m.sibling(pid, name, [16]byte{}) != nil {
		return "", coded(CodeFileExists)
	}
	if e := m.boundsNew(pid, true, name); e != nil {
		return "", e
	}
	if err := c.beginLocked(oa); err != nil {
		return "", c.fail("begin", err)
	}
	info, err := oa.tx.AddDir(pid, name, c.now().Unix())
	if err != nil {
		c.settleLocked(oa)
		return "", c.fail("create folder", err)
	}
	oa.stageAdd(&pendingChange{kind: pendingAdded, id: info.ID, isDir: true, name: name, parentID: pid, dir: info})
	c.touchArchiveLocked(oa)
	oa.seq++
	go c.emit(EventArchiveChanged, ArchiveChanged{ID: hexID(oa.id), Seq: oa.seq})
	go c.emitState()
	return hexID(info.ID), nil
}

// DeleteRecords stages deletions (APP.md §3). A directory takes its subtree
// as the merged view has it, tombstoned in the same write (FORMAT.md R39):
// one staged change however large the subtree, and one row — the folder
// keeps its place in its parent's listing, greyed and not enterable, and
// nothing beneath it is listed while the deletion stands. A record whose
// whole existence is staged is un-staged instead, and a record whose own
// ancestor is in the same batch goes with that ancestor.
func (c *Core) DeleteRecords(id string, recordIDs []string) *Error {
	oa, e := c.findArchive(id)
	if e != nil {
		return e
	}
	ids := make([][16]byte, 0, len(recordIDs))
	for _, str := range recordIDs {
		rid, ok := parseID(str)
		if !ok || rid == format.RootID {
			return coded(CodeParams)
		}
		ids = append(ids, rid)
	}
	oa.opMu.Lock()
	defer oa.opMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.beginLocked(oa); err != nil {
		return c.fail("begin", err)
	}
	m := oa.merge()
	targets, e := m.batch(ids)
	if e != nil {
		c.settleLocked(oa)
		return e
	}
	// The un-stages are pre-flighted whole before anything is staged: a
	// committed record that was moved into a staged folder needs somewhere to
	// go back to, and the parent the move found it under may itself be staged
	// for deletion by now, so the fallback and the collision are settled here
	// rather than half-way through the batch (APP.md §3 — no record is left
	// naming a parent that is not there).
	back := map[[16]byte][16]byte{}
	unstaged := 0
	for _, r := range targets {
		if r.pending != pendingAdded {
			continue
		}
		if e := c.planDetachLocked(oa, m, r, back); e != nil {
			c.settleLocked(oa)
			return e
		}
		unstaged++
	}
	// Un-stage before tombstoning: a record whose way back leads into a
	// folder this same batch deletes goes with that deletion, never the other
	// way round, and the deletions then read a view that has it there.
	for _, r := range targets {
		if r.pending != pendingAdded {
			continue
		}
		if e := c.unstageLocked(oa, m, r, back); e != nil {
			c.settleLocked(oa)
			return e
		}
	}
	if unstaged > 0 {
		m = oa.merge()
	}
	for _, r := range targets {
		if r.pending == pendingAdded {
			continue
		}
		cur := m.live(r.id)
		if cur == nil {
			continue // it went with an un-stage
		}
		if e := c.stageDeleteLocked(oa, m, cur); e != nil {
			c.settleLocked(oa)
			return e
		}
	}
	c.settleLocked(oa)
	c.touchArchiveLocked(oa)
	oa.seq++
	go c.emit(EventArchiveChanged, ArchiveChanged{ID: hexID(oa.id), Seq: oa.seq})
	go c.emitState()
	return nil
}

// stageDeleteLocked tombstones a record and, for a directory, its whole
// subtree in one write. An entry the deletion swallows — a rename staged
// under the folder before it was deleted — leaves the overlay with it
// (APP.md §2.3).
func (c *Core) stageDeleteLocked(oa *openArchive, m *merged, r *mergedRec) *Error {
	if err := oa.tx.Delete(r.id); err != nil {
		return c.fail("delete", err)
	}
	if r.isDir {
		for _, k := range m.subtree(r.id) {
			delete(oa.overlay, k.id)
			oa.removeAdd(k.id)
		}
	}
	oa.overlay[r.id] = &pendingChange{kind: pendingDeleted, id: r.id, isDir: r.isDir, name: r.name, parentID: r.parentID}
	return nil
}

// unstageLocked drops a staged creation (APP.md §3): for a directory the
// records staged under it go with it, and a committed record that was moved
// into it goes back where the move found it, its move un-staged too, so no
// record is left naming a parent that is not there. The archive tombstones
// rather than un-stages — the transaction's bytes are reclaimed at Commit's
// free map or at Abort — so one Tx.Delete takes the whole staged subtree.
func (c *Core) unstageLocked(oa *openArchive, m *merged, r *mergedRec, back map[[16]byte][16]byte) *Error {
	if e := c.detachLocked(oa, m, r, back); e != nil {
		return e
	}
	if err := oa.tx.Delete(r.id); err != nil {
		return c.fail("un-stage", err)
	}
	delete(oa.overlay, r.id)
	oa.removeAdd(r.id)
	return nil
}

// planDetachLocked settles, before anything is staged, where every committed
// record beneath a staged directory goes when that directory is un-staged:
// the parent the move found it under when that is still a directory live in
// the view, and the root when it is not — a folder tombstoned since the move
// is not a parent any more, and the record must not be left naming it
// (APP.md §3). A name the destination already holds under case folding, that
// record's own included, is file.exists and refuses the whole call: nothing
// is dropped and nothing is renamed behind the user's back.
func (c *Core) planDetachLocked(oa *openArchive, m *merged, r *mergedRec, back map[[16]byte][16]byte) *Error {
	if !r.isDir {
		return nil
	}
	for _, k := range m.kids[r.id] {
		switch {
		case k.pending == pendingDeleted:
			// Its tombstone is staged already and a tombstone's parent may
			// name anything (FORMAT.md R32): the deletion stands.
		case k.pending == pendingAdded:
			if e := c.planDetachLocked(oa, m, k, back); e != nil {
				return e
			}
		default:
			was, _, ok := oa.committed(k.id)
			if !ok {
				return c.internalf("un-stage move", errors.New("no committed record for a moved id"))
			}
			if !m.dirUsable(was) {
				was = format.RootID
			}
			if m.sibling(was, k.name, k.id) != nil {
				return coded(CodeFileExists)
			}
			for id, dest := range back {
				o := m.rec(id)
				if dest == was && o != nil && strings.EqualFold(o.name, k.name) {
					return coded(CodeFileExists) // two records back to one name
				}
			}
			back[k.id] = was
		}
	}
	return nil
}

// detachLocked walks a staged directory's children before it is un-staged: a
// staged one loses its overlay entry, a committed one goes back out to the
// parent the plan gave it, and a record already staged for deletion stays
// deleted.
func (c *Core) detachLocked(oa *openArchive, m *merged, r *mergedRec, back map[[16]byte][16]byte) *Error {
	if !r.isDir {
		return nil
	}
	for _, k := range m.kids[r.id] {
		switch {
		case k.pending == pendingDeleted:
			// Its tombstone is staged already and a tombstone's parent may
			// name anything (FORMAT.md R32): the deletion stands.
		case k.pending == pendingAdded:
			if e := c.detachLocked(oa, m, k, back); e != nil {
				return e
			}
			delete(oa.overlay, k.id)
			oa.removeAdd(k.id)
		default:
			to, planned := back[k.id]
			if !planned {
				return c.internalf("un-stage move", errors.New("no planned parent for a moved id"))
			}
			if e := c.moveBackLocked(oa, k, to); e != nil {
				return e
			}
		}
	}
	return nil
}

// moveBackLocked returns a committed record to the parent the plan gave it
// and un-stages the move when that is the parent the move found it under; a
// rename staged with it stands, and a replace keeps its word. A record whose
// old parent went stays `moved`, since the row must say where the record
// actually is.
func (c *Core) moveBackLocked(oa *openArchive, k *mergedRec, to [16]byte) *Error {
	was, name, ok := oa.committed(k.id)
	if !ok {
		return c.internalf("un-stage move", errors.New("no committed record for a moved id"))
	}
	if err := oa.tx.Move(k.id, to); err != nil {
		return c.fail("un-stage move", err)
	}
	p := oa.overlay[k.id]
	if p == nil {
		return nil
	}
	p.parentID = to
	if p.kind == pendingMoved && to == was {
		if p.name == name {
			delete(oa.overlay, k.id)
		} else {
			p.kind = pendingRenamed
		}
	}
	return nil
}

// settleLocked ends a transaction that no longer holds a staged change
// (every add un-staged, every added file skipped): the archive is clean
// again, not "dirty with nothing to save". Caller holds both mutexes.
func (c *Core) settleLocked(oa *openArchive) {
	if oa.tx == nil || len(oa.overlay) > 0 {
		return
	}
	oa.tx.Abort()
	c.clearDirtyLocked(oa)
}

func (oa *openArchive) removeAdd(id [16]byte) {
	for i, p := range oa.adds {
		if p.id == id {
			oa.adds = append(oa.adds[:i], oa.adds[i+1:]...)
			return
		}
	}
}

// RenameRecord stages a rename of one record, file or directory (APP.md §3):
// a "/" is refused, a name that folds onto a live sibling of the record's own
// parent is file.exists — a change of case alone is not one, since a record
// is not its own sibling — and a folder whose new name would push a record
// beneath it past R39's path bound is file.tree_bounds. It writes name, and
// never modified_at.
func (c *Core) RenameRecord(id, recordID, newName string) *Error {
	oa, e := c.findArchive(id)
	if e != nil {
		return e
	}
	rid, ok := parseID(recordID)
	if !ok || rid == format.RootID {
		return coded(CodeParams)
	}
	oa.opMu.Lock()
	defer oa.opMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	m := oa.merge()
	r := m.live(rid)
	if r == nil {
		return coded(CodeFileNotFound)
	}
	if format.ValidateName(newName) != nil {
		return coded(CodeFileName)
	}
	if newName == r.name {
		return nil // already that name: no change, nothing staged
	}
	if m.sibling(r.parentID, newName, r.id) != nil {
		return coded(CodeFileExists)
	}
	if e := m.bounds(r.id, r.isDir, r.parentID, newName); e != nil {
		return e
	}
	if err := c.beginLocked(oa); err != nil {
		return c.fail("begin", err)
	}
	if err := oa.tx.Rename(rid, newName); err != nil {
		c.settleLocked(oa)
		return c.fail("rename", err)
	}
	oa.stageRename(r, newName)
	c.touchArchiveLocked(oa)
	oa.seq++
	go c.emit(EventArchiveChanged, ArchiveChanged{ID: hexID(oa.id), Seq: oa.seq})
	go c.emitState()
	return nil
}

// MoveRecords re-parents each record, one record written whatever subtree
// hangs beneath it (APP.md §3). The batch is pre-flighted against R39 on the
// merged view before anything is staged and is refused whole and in place, so
// the user retries with a name rather than finding half a selection moved: a
// destination that is not the root or a live directory is file.not_found, as
// is a record that is not live; a directory moved into itself or into a
// descendant is file.move_into_self; a name a live child of the destination
// already holds under case folding, or that two records of the batch would
// both take, is file.exists; and a subtree that would then stand too deep or
// join to too long a path is file.tree_bounds. A record whose own ancestor is
// in the batch travels with it, and one already under parentID is a no-op. It
// writes parent_id, and never modified_at.
func (c *Core) MoveRecords(id string, recordIDs []string, parentID string) *Error {
	oa, e := c.findArchive(id)
	if e != nil {
		return e
	}
	pid, ok := parseID(parentID)
	if !ok {
		return coded(CodeParams)
	}
	if len(recordIDs) == 0 {
		return coded(CodeParams)
	}
	ids := make([][16]byte, 0, len(recordIDs))
	for _, str := range recordIDs {
		rid, ok := parseID(str)
		if !ok || rid == format.RootID {
			return coded(CodeParams)
		}
		ids = append(ids, rid)
	}
	oa.opMu.Lock()
	defer oa.opMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	m := oa.merge()
	if !m.dirUsable(pid) {
		return coded(CodeFileNotFound)
	}
	batch, e := m.batch(ids)
	if e != nil {
		return e
	}
	var moving []*mergedRec
	for _, r := range batch {
		if r.parentID == pid {
			continue // already there
		}
		if r.isDir && m.isBelow(pid, r.id) {
			return coded(CodeMoveIntoSelf)
		}
		if m.sibling(pid, r.name, r.id) != nil {
			return coded(CodeFileExists)
		}
		for _, o := range moving {
			if strings.EqualFold(o.name, r.name) {
				return coded(CodeFileExists) // two of the batch, one name
			}
		}
		if e := m.bounds(r.id, r.isDir, pid, r.name); e != nil {
			return e
		}
		moving = append(moving, r)
	}
	if len(moving) == 0 {
		return nil // every record was already there
	}
	if err := c.beginLocked(oa); err != nil {
		return c.fail("begin", err)
	}
	// The staging loop is all-or-nothing as well: the pre-flight has proved
	// the batch legal, so a refusal here is the archive disagreeing with the
	// view the user was shown — and the batch is still refused whole and in
	// place, never left half moved (APP.md §3).
	done := make([]stagedMove, 0, len(moving))
	for _, r := range moving {
		u := stagedMove{id: r.id, parent: r.parentID}
		if p := oa.overlay[r.id]; p != nil {
			was := *p
			u.before = &was
		}
		if err := oa.tx.Move(r.id, pid); err != nil {
			oa.rollbackMoves(done)
			c.settleLocked(oa)
			return c.fail("move", err)
		}
		oa.stageMove(r, pid)
		done = append(done, u)
	}
	c.touchArchiveLocked(oa)
	oa.seq++
	go c.emit(EventArchiveChanged, ArchiveChanged{ID: hexID(oa.id), Seq: oa.seq})
	go c.emitState()
	return nil
}

// stagedMove is one record a move batch has already staged: where it stood
// and the overlay entry that stood on it, which is what a refused batch is
// put back to.
type stagedMove struct {
	id     [16]byte
	parent [16]byte
	before *pendingChange // a copy of the entry that stood; nil when none did
}

// rollbackMoves undoes the moves a refused batch had already staged, newest
// first. The parent each record came from was legal a moment ago, so a
// refusal from the archive here would be a bug of our own and there is
// nothing further to do about it; the overlay is put back either way, and a
// staged add's entry is restored in place because oa.adds holds the same
// pointer. Caller holds opMu and the state mutex.
func (oa *openArchive) rollbackMoves(done []stagedMove) {
	for i := len(done) - 1; i >= 0; i-- {
		u := done[i]
		_ = oa.tx.Move(u.id, u.parent)
		switch p := oa.overlay[u.id]; {
		case u.before == nil:
			delete(oa.overlay, u.id)
		case p != nil:
			*p = *u.before
		default:
			oa.overlay[u.id] = u.before
		}
	}
}

// Discard aborts the transaction.
func (c *Core) Discard(id string) *Error {
	oa, e := c.findArchive(id)
	if e != nil {
		return e
	}
	oa.opMu.Lock()
	defer oa.opMu.Unlock()
	c.mu.Lock()
	if oa.tx != nil {
		oa.tx.Abort()
	}
	c.clearDirtyLocked(oa)
	oa.refreshSnapshot()
	oa.seq++
	seq := oa.seq
	c.mu.Unlock()
	c.emit(EventArchiveChanged, ArchiveChanged{ID: hexID(oa.id), Seq: seq})
	c.emitArchivesChanged()
	c.emitState()
	return nil
}

// PreviewURL is the loopback URL of a committed file. It acts on a record, so
// the root is params and never previewed (APP.md §3).
func (c *Core) PreviewURL(id, fileID string) (string, *Error) {
	oa, e := c.findArchive(id)
	if e != nil {
		return "", e
	}
	fid, ok := parseID(fileID)
	if !ok || fid == format.RootID {
		return "", coded(CodeParams)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if oa.quiesced {
		return "", coded(CodeArchiveCompacting)
	}
	if k := oa.pendingKind(fid); k == pendingAdded || k == pendingReplaced || k == pendingDeleted {
		return "", coded(CodeFileNotFound)
	}
	// A committed file live in the merged view: nothing beneath a folder
	// staged for deletion is previewed while the deletion stands, and a
	// directory's id has no content to serve (APP.md §3).
	if _, ok := oa.currentFile(fid); !ok {
		return "", coded(CodeFileNotFound)
	}
	c.touchArchiveLocked(oa)
	return c.preview.url(oa.token, fid), nil
}

// PreviewText reads up to maxBytes of a committed file's plaintext. The root
// is params here too, for the same reason (APP.md §3).
func (c *Core) PreviewText(id, fileID string, maxBytes int) (string, bool, *Error) {
	oa, e := c.findArchive(id)
	if e != nil {
		return "", false, e
	}
	fid, ok := parseID(fileID)
	if !ok || fid == format.RootID {
		return "", false, coded(CodeParams)
	}
	if maxBytes <= 0 || maxBytes > 1<<20 {
		maxBytes = 256 << 10
	}
	c.mu.Lock()
	if oa.quiesced {
		c.mu.Unlock()
		return "", false, coded(CodeArchiveCompacting)
	}
	if k := oa.pendingKind(fid); k == pendingAdded || k == pendingReplaced || k == pendingDeleted {
		c.mu.Unlock()
		return "", false, coded(CodeFileNotFound)
	}
	if _, ok := oa.currentFile(fid); !ok {
		c.mu.Unlock()
		return "", false, coded(CodeFileNotFound)
	}
	c.touchArchiveLocked(oa)
	oa.readers++
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		oa.readers--
		c.mu.Unlock()
	}()
	r, err := oa.a.OpenReader(fid)
	if err != nil {
		return "", false, c.fail("preview text", err)
	}
	defer r.Close()
	buf := make([]byte, maxBytes+1)
	n, err := readFull(r, buf)
	if err != nil {
		return "", false, c.fail("preview text", err)
	}
	truncated := n > maxBytes
	if truncated {
		n = maxBytes
	}
	return string(buf[:n]), truncated, nil
}

func readFull(r interface{ Read([]byte) (int, error) }, buf []byte) (int, error) {
	n := 0
	for n < len(buf) {
		m, err := r.Read(buf[n:])
		n += m
		if err != nil {
			if errors.Is(err, errEOF) || err.Error() == "EOF" {
				return n, nil
			}
			return n, err
		}
	}
	return n, nil
}

var errEOF = errors.New("EOF")

// withContext is a small helper for ops that take a context.
func (c *Core) opContext() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}

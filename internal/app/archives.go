package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path"
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
// committed snapshot, the staged overlay, the folder projection, the
// preview token, the reader count and the two clocks. Fields are guarded by
// Core.mu; opMu serialises operations that touch the handle.
type openArchive struct {
	id      [16]byte
	path    string
	name    string
	a       *archive.Archive
	opMu    sync.Mutex // one operation on the handle at a time
	tx      *archive.Tx
	snap    []archive.FileInfo
	byID    map[[16]byte]int
	overlay map[[16]byte]*pendingChange
	adds    []*pendingChange // ordered staged adds
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
	kid           [16]byte
	keyVersion    int
	noCompression bool
	receiptOwed   bool
	lastSavedAt   int64
	// Preview quiescing for Compact.
	quiesced bool
}

// pendingChange is one staged change, keyed by file id.
type pendingChange struct {
	kind string // added | replaced | renamed | deleted
	name string // the new name for renamed; the name for added
	info archive.FileInfo
}

// stageReplace records a replaced record in the overlay. A record that is
// itself a staged add stays one object — the same pendingChange in the
// overlay and in adds — so that it can still be renamed or un-staged.
// Caller holds the state mutex.
func (oa *openArchive) stageReplace(info archive.FileInfo) {
	if p := oa.overlay[info.ID]; p != nil && p.kind == "added" {
		p.info = info
		return
	}
	name := info.Name
	if p := oa.overlay[info.ID]; p != nil && p.kind == "renamed" {
		name = p.name
	}
	oa.overlay[info.ID] = &pendingChange{kind: "replaced", name: name, info: info}
}

// owedReceipt is a Save's receipt a lock stranded (APP.md §2.3).
type owedReceipt struct {
	kid       [16]byte
	seq       uint64
	size      uint64
	writtenAt int64
	hash      *[32]byte
}

// dirty is the number of staged changes.
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
				NoCompression: a.Policy&format.PolicyNoCompression != 0, Hidden: a.Policy&format.PolicyHidden != 0,
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
		s := ArchiveSummary{ID: hexID(id), Name: oa.name, Path: oa.path, KeyVersion: oa.keyVersion, NoCompression: oa.noCompression, LastWrittenAt: oa.lastSavedAt}
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

// archiveOptions builds the writer options from the record and settings.
func (c *Core) archiveOptionsLocked(rec *format.ArchiveRecord) archive.Options {
	return archive.Options{
		DeviceID:      c.deviceIDLocked(),
		NoCompression: rec.Policy&format.PolicyNoCompression != 0,
		Compress:      compress.Params{Level: compress.Default},
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
	path, name, kid, nv, noComp, lastAt, lastSeq := rec.LastPath, rec.Name, rec.CurrentKID, len(rec.Versions), rec.Policy&format.PolicyNoCompression != 0, rec.LastWrittenAt, rec.LastSeq
	c.mu.Unlock()

	a, err := archive.Open(path, keys, opts)
	zeroKeys(keys)
	if err != nil {
		if os.IsNotExist(err) {
			return ArchiveStat{}, coded(CodeArchiveMissing)
		}
		return ArchiveStat{}, c.fail("open archive", err)
	}
	oa := &openArchive{id: aid, path: path, name: name, a: a, kid: kid, keyVersion: nv, noCompression: noComp, lastSavedAt: lastAt}
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

// refreshSnapshot re-takes the committed view. Caller holds opMu or is the
// opener.
func (oa *openArchive) refreshSnapshot() {
	oa.snap = oa.a.Files()
	oa.byID = make(map[[16]byte]int, len(oa.snap))
	for i := range oa.snap {
		oa.byID[oa.snap[i].ID] = i
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
		st.Size, st.Files, st.FreeSpace = oa.size, oa.files+len(oa.adds), oa.free
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

// The folder projection (APP.md §3, "Names and folders").

// mergedView is the committed snapshot with the overlay applied.
func (oa *openArchive) mergedView() []archive.FileInfo {
	out := make([]archive.FileInfo, 0, len(oa.snap)+len(oa.adds))
	for i := range oa.snap {
		f := oa.snap[i]
		if p := oa.overlay[f.ID]; p != nil {
			switch p.kind {
			case "deleted":
				continue
			case "renamed":
				f.Name = p.name
			case "replaced":
				f = p.info
			}
		}
		out = append(out, f)
	}
	for _, p := range oa.adds {
		out = append(out, p.info)
	}
	return out
}

func (oa *openArchive) pendingKind(id [16]byte) string {
	if p := oa.overlay[id]; p != nil {
		return p.kind
	}
	return ""
}

// Page projects the merged view onto one folder. folder is "" for the
// root or a `/`-separated prefix without a trailing slash.
func (c *Core) Page(id, folder, sortBy string, offset, limit int) (Page, *Error) {
	oa, e := c.findArchive(id)
	if e != nil {
		return Page{}, e
	}
	folder = strings.Trim(folder, "/")
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.touchArchiveLocked(oa)
	prefix := ""
	if folder != "" {
		prefix = folder + "/"
	}
	type folderAgg struct {
		files int
		size  uint64
		mod   int64
	}
	folders := map[string]*folderAgg{}
	var rows []FileRow
	for _, f := range oa.mergedView() {
		if !strings.HasPrefix(f.Name, prefix) {
			continue
		}
		rest := f.Name[len(prefix):]
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			name := rest[:i]
			agg := folders[name]
			if agg == nil {
				agg = &folderAgg{}
				folders[name] = agg
			}
			agg.files++
			agg.size += f.Size
			if f.ModifiedAt > agg.mod {
				agg.mod = f.ModifiedAt
			}
			continue
		}
		rows = append(rows, fileRow(f, rest, oa.pendingKind(f.ID)))
	}
	for name, agg := range folders {
		rows = append(rows, FileRow{Path: prefix + name, Name: name, IsFolder: true, Files: agg.files, Size: agg.size, ModifiedAt: agg.mod})
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
	page := Page{Seq: oa.seq, Folder: folder, Rows: rows[offset:end], Total: total}
	if page.Rows == nil {
		page.Rows = []FileRow{}
	}
	return page, nil
}

func fileRow(f archive.FileInfo, leaf, pending string) FileRow {
	r := FileRow{FileID: hexID(f.ID), Path: f.Name, Name: leaf, Size: f.Size, ModifiedAt: f.ModifiedAt, Pending: pending}
	switch f.Storage {
	case format.StorageZstd:
		r.Storage = "zstd"
	case format.StorageZstdDict:
		r.Storage = "zstd+dict"
	default:
		r.Storage = "raw"
	}
	if f.Storage != format.StorageRaw && f.Size > 0 && f.StoredSize < f.Size {
		r.SavedPercent = int((f.Size - f.StoredSize) * 100 / f.Size)
	}
	return r
}

func sortRows(rows []FileRow, by string) {
	less := func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.IsFolder != b.IsFolder {
			return a.IsFolder
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

// composeName joins a folder and a leaf into a stored name and validates
// it under R20.
func composeName(folder, leaf string) (string, error) {
	folder = strings.Trim(folder, "/")
	if leaf == "" || strings.ContainsRune(leaf, '/') {
		return "", format.ErrInvalid
	}
	name := leaf
	if folder != "" {
		name = folder + "/" + leaf
	}
	if err := format.ValidateFileName(name); err != nil {
		return "", err
	}
	return name, nil
}

// lookupMerged finds a live name in the merged view.
func (oa *openArchive) lookupMerged(name string) (archive.FileInfo, bool) {
	for _, f := range oa.mergedView() {
		if f.Name == name {
			return f, true
		}
	}
	return archive.FileInfo{}, false
}

// CheckNames reports which of the names, composed under folder, collide
// with the merged view, so the UI can ask once before an add.
func (c *Core) CheckNames(id, folder string, names []string) ([]Collision, *Error) {
	oa, e := c.findArchive(id)
	if e != nil {
		return nil, e
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := []Collision{}
	for _, n := range names {
		full, err := composeName(folder, path.Base(strings.ReplaceAll(n, "\\", "/")))
		if err != nil {
			out = append(out, Collision{Name: n, Existing: "", Pending: false})
			continue
		}
		if f, ok := oa.lookupMerged(full); ok {
			out = append(out, Collision{Name: n, Existing: hexID(f.ID), Pending: oa.pendingKind(f.ID) != ""})
		}
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

// DeleteFiles stages deletions.
func (c *Core) DeleteFiles(id string, fileIDs []string) *Error {
	oa, e := c.findArchive(id)
	if e != nil {
		return e
	}
	oa.opMu.Lock()
	defer oa.opMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.beginLocked(oa); err != nil {
		return c.fail("begin", err)
	}
	for _, s := range fileIDs {
		fid, ok := parseID(s)
		if !ok {
			return coded(CodeParams)
		}
		if p := oa.overlay[fid]; p != nil && p.kind == "added" {
			// A staged add: un-stage it (the transaction's bytes are
			// reclaimed at Commit's free map or Abort).
			if err := oa.tx.Delete(fid); err != nil {
				return c.fail("delete", err)
			}
			delete(oa.overlay, fid)
			oa.removeAdd(fid)
			continue
		}
		if _, ok := oa.byID[fid]; !ok {
			return coded(CodeFileNotFound)
		}
		if err := oa.tx.Delete(fid); err != nil {
			return c.fail("delete", err)
		}
		oa.overlay[fid] = &pendingChange{kind: "deleted"}
	}
	c.settleLocked(oa)
	c.touchArchiveLocked(oa)
	oa.seq++
	go c.emit(EventArchiveChanged, ArchiveChanged{ID: hexID(oa.id), Seq: oa.seq})
	go c.emitState()
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

func (oa *openArchive) removeAdd(fid [16]byte) {
	for i, p := range oa.adds {
		if p.info.ID == fid {
			oa.adds = append(oa.adds[:i], oa.adds[i+1:]...)
			return
		}
	}
}

// RenameFile stages a rename of the leaf; the folder stays.
func (c *Core) RenameFile(id, fileID, newLeaf string) *Error {
	oa, e := c.findArchive(id)
	if e != nil {
		return e
	}
	fid, ok := parseID(fileID)
	if !ok {
		return coded(CodeParams)
	}
	oa.opMu.Lock()
	defer oa.opMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	cur, found := oa.currentInfo(fid)
	if !found {
		return coded(CodeFileNotFound)
	}
	folder := ""
	if i := strings.LastIndexByte(cur.Name, '/'); i >= 0 {
		folder = cur.Name[:i]
	}
	name, err := composeName(folder, newLeaf)
	if err != nil {
		return coded(CodeFileName)
	}
	if name == cur.Name {
		return nil
	}
	if _, exists := oa.lookupMerged(name); exists {
		return coded(CodeFileExists)
	}
	if err := c.beginLocked(oa); err != nil {
		return c.fail("begin", err)
	}
	if err := oa.tx.Rename(fid, name); err != nil {
		return c.fail("rename", err)
	}
	if p := oa.overlay[fid]; p != nil && (p.kind == "added" || p.kind == "replaced") {
		p.info.Name = name
		p.name = name
	} else {
		oa.overlay[fid] = &pendingChange{kind: "renamed", name: name}
	}
	c.touchArchiveLocked(oa)
	oa.seq++
	go c.emit(EventArchiveChanged, ArchiveChanged{ID: hexID(oa.id), Seq: oa.seq})
	go c.emitState()
	return nil
}

// currentInfo is the file as the merged view has it.
func (oa *openArchive) currentInfo(fid [16]byte) (archive.FileInfo, bool) {
	if p := oa.overlay[fid]; p != nil {
		switch p.kind {
		case "deleted":
			return archive.FileInfo{}, false
		case "added", "replaced":
			return p.info, true
		case "renamed":
			if i, ok := oa.byID[fid]; ok {
				f := oa.snap[i]
				f.Name = p.name
				return f, true
			}
		}
	}
	if i, ok := oa.byID[fid]; ok {
		return oa.snap[i], true
	}
	return archive.FileInfo{}, false
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

// PreviewURL is the loopback URL of a committed file.
func (c *Core) PreviewURL(id, fileID string) (string, *Error) {
	oa, e := c.findArchive(id)
	if e != nil {
		return "", e
	}
	fid, ok := parseID(fileID)
	if !ok {
		return "", coded(CodeParams)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if oa.quiesced {
		return "", coded(CodeArchiveCompacting)
	}
	if k := oa.pendingKind(fid); k == "added" || k == "replaced" || k == "deleted" {
		return "", coded(CodeFileNotFound)
	}
	if _, ok := oa.byID[fid]; !ok {
		return "", coded(CodeFileNotFound)
	}
	c.touchArchiveLocked(oa)
	return c.preview.url(oa.token, fid), nil
}

// PreviewText reads up to maxBytes of a committed file's plaintext.
func (c *Core) PreviewText(id, fileID string, maxBytes int) (string, bool, *Error) {
	oa, e := c.findArchive(id)
	if e != nil {
		return "", false, e
	}
	fid, ok := parseID(fileID)
	if !ok {
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
	if k := oa.pendingKind(fid); k == "added" || k == "replaced" || k == "deleted" {
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

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
// commit — the preview token, the reader count and one idle clock. There is
// no overlay and no dirty state since 2026-09-09: every operation is its own
// transaction, committed at its end. Fields are guarded by Core.mu; opMu
// serialises operations that touch the handle, and a running operation holds
// it for the whole of its transaction.
type openArchive struct {
	id   [16]byte
	path string
	name string
	a    *archive.Archive
	opMu sync.Mutex // one operation on the handle at a time
	// The committed snapshot is both record tables: a directory is a record
	// of its own (FORMAT.md R39) and no folder is projected from a name.
	snap    []archive.FileInfo
	dirSnap []archive.DirInfo
	byID    map[[16]byte]int
	dirByID map[[16]byte]int
	seq     uint64
	state   string // open | compacting | needs_reopen
	token   string
	readers int
	lastUse time.Time
	// The handle's figures, cached at every snapshot so that nothing under
	// the state mutex takes the archive's own mutex (a running hash holds
	// it for the whole read). records counts live files and directories
	// together; files counts files alone.
	size    uint64
	files   int
	records int
	free    uint64
	// copyMismatch: the file's seq is not the one the registry last saw.
	copyMismatch bool
	// The idle clock. Each arm bumps its generation and the callback carries
	// the one it was armed with, so a callback that was already running when
	// the clock was re-armed or cleared does nothing.
	idleTimer Timer
	idleGen   uint64
	expiresAt time.Time
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

// owedReceipt is a commit's receipt a lock stranded (APP.md §2.3).
type owedReceipt struct {
	kid       [16]byte
	seq       uint64
	size      uint64
	writtenAt int64
	hash      *[32]byte
}

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
	s.Open, s.State, s.ReceiptOwed = true, oa.state, s.ReceiptOwed || oa.receiptOwed
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
		// archive until a commit records this copy, never adopted silently.
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
// the index is a tree — after every commit (APP.md §2.3). Caller holds opMu
// or is the opener.
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
	// Records is what Extract all is greyed on: live files and folders
	// together, since the file count alone cannot say whether the tree holds
	// anything (APP.md §3).
	oa.records = len(oa.snap) + len(oa.dirSnap)
}

// CloseArchive closes an open archive. An operation running on it holds the
// handle until it ends, one way or the other: the archive is clean between
// operations, so there is nothing to ask about (APP.md §2.3).
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
	if oa.state == "compacting" {
		c.mu.Unlock()
		return coded(CodeArchiveCompacting)
	}
	// A handle in needs_reopen is broken but still holds the file: closing
	// it is what lets a reopen succeed.
	c.closeArchiveLocked(oa)
	c.mu.Unlock()
	c.emitArchivesChanged()
	c.emitState()
	return nil
}

// closeArchiveLocked drops the archive: token forgotten, timer stopped,
// handle closed (which fails every in-flight preview body). Caller holds the
// state mutex and opMu.
func (c *Core) closeArchiveLocked(oa *openArchive) {
	if oa.idleTimer != nil {
		oa.idleTimer.Stop()
	}
	oa.idleGen++ // a callback already on its way is void
	oa.token = ""
	delete(c.archives, oa.id)
	oa.a.Close()
	oa.state = "closed"
}

// CloseAllArchives closes what it can and reports what stayed open — an
// archive being compacted, which holds its handle for the whole run.
func (c *Core) CloseAllArchives() []string {
	c.mu.Lock()
	var list []*openArchive
	for _, oa := range c.archives {
		list = append(list, oa)
	}
	c.mu.Unlock()
	var kept []string
	for _, oa := range list {
		if e := c.CloseArchive(hexID(oa.id)); e != nil {
			kept = append(kept, hexID(oa.id))
		}
	}
	return kept
}

// cancelOpsLocked cancels every unfinished operation of one archive and
// returns what to wait on. Caller holds the state mutex.
func (c *Core) cancelOpsLocked(aid [16]byte) []*op {
	id := hexID(aid)
	var running []*op
	for _, o := range c.ops {
		if !o.finished && o.archiveID == id {
			o.cancel()
			running = append(running, o)
		}
	}
	return running
}

// awaitOps waits for cancelled operations to end, bounded. A cancel reaches
// the write loop within one chunk, so this is milliseconds; the bound is
// there so that nothing waits forever on a syscall that will not return.
func (c *Core) awaitOps(running []*op, budget time.Duration) {
	if len(running) == 0 {
		return
	}
	limit := time.After(budget)
	for _, o := range running {
		select {
		case <-o.over:
		case <-limit:
			c.log("operation %s did not end within %v", o.kind, budget)
			return
		}
	}
}

// hasRunningOpLocked reports whether an unfinished operation stands against
// one archive. Caller holds the state mutex.
func (c *Core) hasRunningOpLocked(aid [16]byte) bool {
	id := hexID(aid)
	for _, o := range c.ops {
		if !o.finished && o.archiveID == id {
			return true
		}
	}
	return false
}

// closeForRecord makes room for a *Forget key…* of an archive that is open
// (APP.md §13): a running operation is cancelled — its transaction aborted,
// nothing published — and the archive is closed. There are no unsaved
// changes to ask about since 2026-09-09.
//
// The cancel comes first and is awaited, because an operation counts itself
// among the archive's readers while it holds a file open — an extract does
// so around every file it writes — and a readers count sampled before the
// cancel cannot tell that handle from a preview stream. Once the cancelled
// operations have ended, a reader still standing is a genuine preview: §13
// keeps `archive.busy` for it, since a stream someone is watching is not
// this call's to end. The archive stays open and busyForRecordLocked then
// refuses.
func (c *Core) closeForRecord(aid [16]byte) {
	c.mu.Lock()
	if c.archives[aid] == nil {
		c.mu.Unlock()
		return
	}
	running := c.cancelOpsLocked(aid)
	c.mu.Unlock()
	c.awaitOps(running, 3*time.Second)
	c.mu.Lock()
	oa := c.archives[aid]
	if oa == nil || oa.readers > 0 {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	c.CloseArchive(hexID(aid))
}

// closeQuietForRecord is the *Delete archive…* half of the same pair. §13's
// Delete paragraph is unamended: "the open archive is closed first, refused
// with `archive.busy` while an operation or a preview reader is live" — so a
// delete never cancels anything. Only a quiet archive is closed; against a
// live operation or reader the handle stays and busyForRecordLocked answers
// `archive.busy`.
func (c *Core) closeQuietForRecord(aid [16]byte) {
	c.mu.Lock()
	oa := c.archives[aid]
	if oa == nil || oa.readers > 0 || c.hasRunningOpLocked(aid) {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	c.CloseArchive(hexID(aid))
}

// statLocked builds the status strip. Caller holds the state mutex.
func (c *Core) statLocked(oa *openArchive) ArchiveStat {
	st := ArchiveStat{ID: hexID(oa.id), Name: oa.name, Seq: oa.seq, KeyVersion: oa.keyVersion, State: oa.state, LastSavedAt: oa.lastSavedAt, ReceiptOwed: oa.receiptOwed, CopyMismatch: oa.copyMismatch}
	if oa.state != "needs_reopen" && oa.state != "compacting" {
		st.Size, st.Files, st.Records, st.FreeSpace = oa.size, oa.files, oa.records, oa.free
	}
	if !oa.expiresAt.IsZero() {
		st.ExpiresAt = oa.expiresAt.Unix()
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

// The archive's one clock (APP.md §2.3, DESIGN.md §10): idleness — no
// running operation, no open reader, no request — closes it, and the archive
// is always clean between operations, so nothing is ever discarded by it.

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
		// An operation is running: it holds the clock. Look again later.
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
	c.closeArchiveLocked(oa)
	c.mu.Unlock()
	c.emitArchivesChanged()
	c.emitState()
}

// An operation is a transaction (APP.md §2.3, DECISIONS 2026-09-09): Begin
// at its start, Commit at its end, then the receipt, one Session.UpdateRegistry
// and archive.changed; Abort on failure and on Cancel.

// beginOp opens the transaction one operation runs in. Caller holds opMu.
func (c *Core) beginOp(oa *openArchive) (*archive.Tx, *Error) {
	tx, err := oa.a.Begin()
	if err != nil {
		return nil, c.fail("begin", err)
	}
	return tx, nil
}

// commitOp publishes it. The commit — index seal, free map, two syncs — runs
// under the archive's own mutex only, so the state mutex stays short and a
// lock trigger is never held up by I/O; the receipt is written under the
// state mutex right after, and a lock that lands between the two leaves it
// owed. Caller holds opMu.
func (c *Core) commitOp(ctx context.Context, oa *openArchive, tx *archive.Tx) *Error {
	rec, err := tx.Commit(ctx)
	c.mu.Lock()
	if err != nil {
		if errors.Is(err, archive.ErrIndeterminate) {
			// The outcome is unknown: the handle is finished and the page
			// must reopen the file to learn what landed.
			oa.state = "needs_reopen"
			c.mu.Unlock()
			c.emitArchivesChanged()
			c.emitState()
			return c.fail("commit", err)
		}
		// Any other failure aborted the transaction (the archive's
		// contract): nothing was published and the snapshot still stands.
		oa.refreshSnapshot()
		c.mu.Unlock()
		return c.fail("commit", err)
	}
	oa.refreshSnapshot()
	oa.lastSavedAt = rec.WrittenAt
	oa.seq++
	seq := oa.seq
	c.recordReceiptLocked(oa, rec, nil)
	c.touchArchiveLocked(oa)
	c.mu.Unlock()
	c.emit(EventArchiveChanged, ArchiveChanged{ID: hexID(oa.id), Seq: seq})
	c.emitArchivesChanged()
	c.emitState()
	return nil
}

// abortOp discards the transaction: nothing is published, and the bytes it
// wrote lie in extents the committed free map still holds free — the
// superblock the readers use never named them, so nothing is lost and
// nothing leaks (APP.md §2.3). The tail it appended is truncated away, which
// is why the snapshot's figures are taken again. Caller holds opMu.
func (c *Core) abortOp(oa *openArchive, tx *archive.Tx) {
	tx.Abort()
	c.mu.Lock()
	oa.refreshSnapshot()
	c.mu.Unlock()
}

// currentFile is the live file of the committed snapshot. Only a file has
// content, so a directory's id answers false, as an unknown one does.
func (oa *openArchive) currentFile(fid [16]byte) (archive.FileInfo, bool) {
	i, ok := oa.byID[fid]
	if !ok {
		return archive.FileInfo{}, false
	}
	return oa.snap[i], true
}

// Page lists the children of one directory, files and folders in one list,
// with the breadcrumb from the root down to it (APP.md §3). dirID is a
// record id — the all-zero id is the root — never a path, and one that no
// longer names a live directory answers file.not_found rather than an empty
// listing under a breadcrumb that still names the place: the page walks the
// Crumbs it last held upwards until one answers.
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
		Name: r.name, Path: m.path(r.id), ModifiedAt: r.modifiedAt,
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
// grey Replace whenever the two differ. An offered name format.ValidateName
// refuses is not a collision and is not reported here: a Collision names the
// record in the way and there is none, and the add says so where APP.md §3
// puts it — one FileOutcome of `failed` with `file.name`, named in the
// results and never silently skipped.
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
			Name: n, IsDir: isDir, Existing: hexID(x.id), ExistingIsDir: x.isDir,
		})
	}
	return out, nil
}

// CreateFolder commits a directory record under parentID at once (APP.md
// §3): a folder is a record, so an empty one is a real thing and never a
// fiction of the page (FORMAT.md R39, DESIGN.md trap 31), and its
// modified_at is the time it was made — nothing writes it again (R32). The
// name is validated and matched like any other's.
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
	m := oa.merge()
	if !m.dirUsable(pid) {
		c.mu.Unlock()
		return "", coded(CodeFileNotFound)
	}
	if format.ValidateName(name) != nil {
		c.mu.Unlock()
		return "", coded(CodeFileName)
	}
	if m.sibling(pid, name, [16]byte{}) != nil {
		c.mu.Unlock()
		return "", coded(CodeFileExists)
	}
	if e := m.boundsNew(pid, true, name); e != nil {
		c.mu.Unlock()
		return "", e
	}
	at := c.now().Unix()
	c.mu.Unlock()
	tx, e := c.beginOp(oa)
	if e != nil {
		return "", e
	}
	info, err := tx.AddDir(pid, name, at)
	if err != nil {
		c.abortOp(oa, tx)
		return "", c.fail("create folder", err)
	}
	if e := c.commitOp(context.Background(), oa, tx); e != nil {
		return "", e
	}
	return hexID(info.ID), nil
}

// DeleteRecords deletes each record in one transaction (APP.md §3). A
// directory takes its subtree as the index has it, tombstoned in the same
// commit (FORMAT.md R39, R32): one commit however large the subtree. The
// page asks first — naming files and folders apart, saying a folder takes
// everything beneath it and that this cannot be undone — and the core does
// not ask again: deletion is cryptographic erasure and there is no undo.
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
	targets, e := oa.merge().batch(ids)
	c.mu.Unlock()
	if e != nil {
		return e
	}
	if len(targets) == 0 {
		return nil
	}
	tx, e := c.beginOp(oa)
	if e != nil {
		return e
	}
	for _, r := range targets {
		if err := tx.Delete(r.id); err != nil {
			c.abortOp(oa, tx)
			return c.fail("delete", err)
		}
	}
	return c.commitOp(context.Background(), oa, tx)
}

// RenameRecord renames one record, file or directory (APP.md §3): a "/" is
// refused, a name that folds onto a live sibling of the record's own parent
// is file.exists — a change of case alone is not one, since a record is not
// its own sibling — and a folder whose new name would push a record beneath
// it past R39's path bound is file.tree_bounds. It writes name, and never
// modified_at. Pre-flighted before anything is written, then committed.
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
	m := oa.merge()
	r := m.live(rid)
	switch {
	case r == nil:
		c.mu.Unlock()
		return coded(CodeFileNotFound)
	case format.ValidateName(newName) != nil:
		c.mu.Unlock()
		return coded(CodeFileName)
	case newName == r.name:
		c.mu.Unlock()
		return nil // already that name: nothing is written
	case m.sibling(r.parentID, newName, r.id) != nil:
		c.mu.Unlock()
		return coded(CodeFileExists)
	}
	if e := m.bounds(r.id, r.isDir, r.parentID, newName); e != nil {
		c.mu.Unlock()
		return e
	}
	c.mu.Unlock()
	tx, e := c.beginOp(oa)
	if e != nil {
		return e
	}
	if err := tx.Rename(rid, newName); err != nil {
		c.abortOp(oa, tx)
		return c.fail("rename", err)
	}
	return c.commitOp(context.Background(), oa, tx)
}

// MoveRecords re-parents each record, one record written whatever subtree
// hangs beneath it (APP.md §3). The batch is pre-flighted against R39 before
// anything is written and is refused whole and in place, so the user retries
// with a name rather than finding half a selection moved: a destination that
// is not the root or a live directory is file.not_found, as is a record that
// is not live; a directory moved into itself or into a descendant is
// file.move_into_self; a name a live child of the destination already holds
// under case folding, or that two records of the batch would both take, is
// file.exists; and a subtree that would then stand too deep or join to too
// long a path is file.tree_bounds. A record whose own ancestor is in the
// batch travels with it, and one already under parentID is a no-op. It
// writes parent_id, and never modified_at. The whole batch is one
// transaction, so a refusal from the archive leaves nothing behind either.
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
	m := oa.merge()
	if !m.dirUsable(pid) {
		c.mu.Unlock()
		return coded(CodeFileNotFound)
	}
	batch, e := m.batch(ids)
	if e != nil {
		c.mu.Unlock()
		return e
	}
	var moving [][16]byte
	var names []string
	for _, r := range batch {
		if r.parentID == pid {
			continue // already there
		}
		if r.isDir && m.isBelow(pid, r.id) {
			c.mu.Unlock()
			return coded(CodeMoveIntoSelf)
		}
		if m.sibling(pid, r.name, r.id) != nil {
			c.mu.Unlock()
			return coded(CodeFileExists)
		}
		for _, o := range names {
			if strings.EqualFold(o, r.name) {
				c.mu.Unlock()
				return coded(CodeFileExists) // two of the batch, one name
			}
		}
		if e := m.bounds(r.id, r.isDir, pid, r.name); e != nil {
			c.mu.Unlock()
			return e
		}
		moving = append(moving, r.id)
		names = append(names, r.name)
	}
	c.mu.Unlock()
	if len(moving) == 0 {
		return nil // every record was already there
	}
	tx, e := c.beginOp(oa)
	if e != nil {
		return e
	}
	for _, rid := range moving {
		if err := tx.Move(rid, pid); err != nil {
			// The pre-flight proved the batch legal, so a refusal here is
			// the archive disagreeing with the view the user was shown: the
			// transaction is dropped whole and nothing is published.
			c.abortOp(oa, tx)
			return c.fail("move", err)
		}
	}
	return c.commitOp(context.Background(), oa, tx)
}

// PreviewURL is the loopback URL of a file. It acts on a record, so the root
// is params and never previewed (APP.md §3).
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
	// A file of the committed snapshot: a directory's id has no content to
	// serve (APP.md §3).
	if _, ok := oa.currentFile(fid); !ok {
		return "", coded(CodeFileNotFound)
	}
	c.touchArchiveLocked(oa)
	return c.preview.url(oa.token, fid), nil
}

// PreviewText reads up to maxBytes of a file's plaintext. The root is params
// here too, for the same reason (APP.md §3).
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

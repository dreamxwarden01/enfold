package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dreamxwarden01/enfold/internal/format"
)

// The Archives page is the vault's inspector (APP.md §13): it lists registry
// records, not files. This file holds the record's own actions — rename, the
// description, the whole record, forget, restore and delete — and the
// presence pass that decides the Status column's "file missing", which is
// never computed inside List.

// maxNameLen bounds a renamed archive's name: FORMAT.md §7.1 bounds
// description and says nothing about name, so this is the app's rule and not
// a wire one (§9 Q14). The confirmation for Forget and Delete is the
// archive's name typed, and an unbounded name defeats its own brake.
const maxNameLen = 1024

// RenameArchive writes the registry's trusted name (FORMAT.md §7.4): no
// ceremony, the session's key is enough. Refused for an empty name, one over
// maxNameLen bytes or one that is not valid UTF-8, and for a forgotten record.
func (c *Core) RenameArchive(id, name string) *Error {
	aid, ok := parseID(id)
	if !ok {
		return coded(CodeParams)
	}
	if name == "" || len(name) > maxNameLen || !utf8.ValidString(name) {
		return coded(CodeArchiveName)
	}
	e := c.updateRegistry(func(g *registry) error {
		a := findRecord(g, aid)
		if a == nil {
			return coded(CodeArchiveNotFound)
		}
		if a.Forgotten() {
			return coded(CodeArchiveForgotten)
		}
		if a.Name == name {
			return errNoChange // the name already held: no commit, no restamp
		}
		a.Name = name
		a.Revision++
		a.LastWriter = g.DeviceID
		return nil
	})
	if e != nil {
		return e
	}
	c.mu.Lock()
	if oa := c.archives[aid]; oa != nil {
		oa.name = name
	}
	c.mu.Unlock()
	c.emitArchivesChanged()
	return nil
}

// SetArchiveDescription writes the record's description (FORMAT.md §7.1): at
// most MaxDescriptionLen bytes of UTF-8 — bytes, not characters — and empty
// clears it. Refused for a forgotten record.
func (c *Core) SetArchiveDescription(id, text string) *Error {
	aid, ok := parseID(id)
	if !ok {
		return coded(CodeParams)
	}
	if len(text) > format.MaxDescriptionLen || !utf8.ValidString(text) {
		return coded(CodeDescriptionLong)
	}
	e := c.updateRegistry(func(g *registry) error {
		a := findRecord(g, aid)
		if a == nil {
			return coded(CodeArchiveNotFound)
		}
		if a.Forgotten() {
			return coded(CodeArchiveForgotten)
		}
		if a.Description == text {
			return errNoChange // the text already held: no commit, no restamp
		}
		a.Description = text
		a.Revision++
		a.LastWriter = g.DeviceID
		return nil
	})
	if e != nil {
		return e
	}
	c.emitArchivesChanged()
	return nil
}

// ArchiveDetails is one registry record read whole (APP.md §13), for the
// details pane and its modal. Refused with vault.locked outside Unlocked, and
// never refused for a forgotten record — the pane must render Restore.
func (c *Core) ArchiveDetails(id string) (ArchiveDetails, *Error) {
	aid, ok := parseID(id)
	if !ok {
		return ArchiveDetails{}, coded(CodeParams)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.vault.state != StateUnlocked {
		return ArchiveDetails{}, coded(CodeVaultLocked)
	}
	sess, e := c.sessionLocked()
	if e != nil {
		return ArchiveDetails{}, e
	}
	a := findRecord(sess.Registry(), aid)
	if a == nil {
		return ArchiveDetails{}, coded(CodeArchiveNotFound)
	}
	d := ArchiveDetails{
		ArchiveID: hexID(a.ArchiveID), Name: a.Name, Description: a.Description,
		CreatedAt: a.CreatedAt, LastPath: a.LastPath, CurrentKID: hexID(a.CurrentKID),
		Revision: a.Revision, LastWriter: hexID(a.LastWriter),
		LastSeq: a.LastSeq, HashAtSeq: a.HashAtSeq,
		LastCiphertextHash: hexHash(a.LastCiphertextHash),
		LastStoredSize:     a.LastStoredSize, LastWrittenAt: a.LastWrittenAt,
		ForgottenAt:           a.ForgottenAt,
		AlwaysRequireFullAuth: a.Policy&format.PolicyAlwaysRequireFullAuth != 0,
		Hidden:                a.Policy&format.PolicyHidden != 0,
		Method:                methodOf(a.Policy),
		Versions:              []VersionView{},
	}
	for i := range a.Versions {
		v := &a.Versions[i]
		state := "retired"
		if v.KID == a.CurrentKID {
			state = "current"
		}
		d.Versions = append(d.Versions, VersionView{KID: hexID(v.KID), CreatedAt: v.CreatedAt, RetiredAt: v.RetiredAt, State: state})
	}
	return d, nil
}

// refuseIfForgotten answers archive.forgotten for a record the vault holds as
// forgotten, and nothing for every other case, so the caller's own answers —
// not open, not found, locked — are unchanged (APP.md §13).
func (c *Core) refuseIfForgotten(id string) *Error {
	aid, ok := parseID(id)
	if !ok {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	sess, e := c.sessionLocked()
	if e != nil {
		return nil
	}
	if a := findRecord(sess.Registry(), aid); a != nil && a.Forgotten() {
		return coded(CodeArchiveForgotten)
	}
	return nil
}

// busyForRecordLocked is the predicate Forget and Delete share: the archive
// must be closed first, and no operation and no preview reader may be live
// against it (APP.md §13). Caller holds the state mutex.
func (c *Core) busyForRecordLocked(aid [16]byte) *Error {
	if oa := c.archives[aid]; oa != nil {
		return coded(CodeArchiveBusy)
	}
	if c.deleting[aid] {
		// A delete of this record is between its busy check and its write:
		// the file is about to go, so nothing else claims the record.
		return coded(CodeArchiveBusy)
	}
	if c.hasRunningOpLocked(aid) {
		return coded(CodeArchiveBusy)
	}
	return nil
}

// ForgetArchive drops the record softly (APP.md §13, FORMAT.md §18.2):
// forgotten_at becomes the write's own modified_at, never the raw clock, and
// a second Forget does not move it — the retention clock never restarts. The
// keys stay until the purge at an unlock more than thirty days later.
func (c *Core) ForgetArchive(id string) *Error {
	aid, ok := parseID(id)
	if !ok {
		return coded(CodeParams)
	}
	// The archive is closed first, a running operation cancelled with it
	// (APP.md §13): there are no unsaved changes to ask about.
	c.closeForRecord(aid)
	c.mu.Lock()
	if e := c.busyForRecordLocked(aid); e != nil {
		c.mu.Unlock()
		return e
	}
	e := c.updateRegistryAtLocked(false, func(g *registry, modifiedAt int64) error {
		a := findRecord(g, aid)
		if a == nil {
			return coded(CodeArchiveNotFound)
		}
		if a.Forgotten() {
			// Already forgotten: the clock stands and the write is
			// abandoned, so a second Forget does not restamp modified_at.
			return errNoChange
		}
		a.ForgottenAt = modifiedAt
		a.Revision++
		a.LastWriter = g.DeviceID
		return nil
	})
	c.mu.Unlock()
	if e != nil {
		return e
	}
	c.emitArchivesChanged()
	c.emitState()
	return nil
}

// RestoreArchive clears forgotten_at: the record is an ordinary one again.
func (c *Core) RestoreArchive(id string) *Error {
	aid, ok := parseID(id)
	if !ok {
		return coded(CodeParams)
	}
	e := c.updateRegistryAt(true, func(g *registry, _ int64) error {
		a := findRecord(g, aid)
		if a == nil {
			return coded(CodeArchiveNotFound)
		}
		if !a.Forgotten() {
			return errNoChange // not forgotten: nothing to clear
		}
		a.ForgottenAt = 0
		a.Revision++
		a.LastWriter = g.DeviceID
		return nil
	})
	if e != nil {
		return e
	}
	c.emitArchivesChanged()
	c.emitState()
	return nil
}

// updateRegistryAt is updateRegistry with the commit's own modified_at and
// the forgotten-record intent of APP.md §13.
func (c *Core) updateRegistryAt(allowForgotten bool, fn func(g *registry, modifiedAt int64) error) *Error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.updateRegistryAtLocked(allowForgotten, fn)
}

// The four outcomes of a delete (APP.md §13, DESIGN.md trap 28), as errors
// the build-tagged seam returns and this file maps to codes. Only a proven
// match and a proven mismatch ever touch the record.
var (
	errArchiveNotThisOne   = errors.New("app: the file at that path is another archive")
	errArchiveUnreachable  = errors.New("app: the archive's path cannot be reached")
	errArchiveHeld         = errors.New("app: the archive's file is held or access is denied")
	errArchiveNotOne       = errors.New("app: the file at that path is not an Enfold archive")
	errArchiveRemoveFailed = errors.New("app: the archive's file could not be removed")
)

// deleteArchiveFile is the seam of B.20, a var so that a test can produce the
// removal failure a real file system will not.
var deleteArchiveFile = deleteArchiveFileIfMatches

// DeleteArchive is Forget plus the file (APP.md §13). With alsoFile false it
// is exactly Forget — one path to one registry write, so the retention clock
// cannot be restarted by the other (§9 Q13). With it true the file at
// last_path is opened once and removed through that same handle, never by the
// name a second time: absence is decided on the parent folder, the archive_id
// is read from the handle that will do the removal, and the record is
// forgotten only after the file is gone.
func (c *Core) DeleteArchive(id string, alsoFile bool) *Error {
	if !alsoFile {
		return c.ForgetArchive(id)
	}
	aid, ok := parseID(id)
	if !ok {
		return coded(CodeParams)
	}
	// Unlike Forget, the archive is only closed if it is quiet: §13's Delete
	// paragraph is "the open archive is closed first, refused with
	// `archive.busy` while an operation or a preview reader is live", so a
	// running add is never killed by a delete — busyForRecordLocked answers
	// archive.busy below and the user cancels the operation first.
	c.closeQuietForRecord(aid)
	c.mu.Lock()
	if e := c.busyForRecordLocked(aid); e != nil {
		c.mu.Unlock()
		return e
	}
	sess, e := c.sessionLocked()
	if e != nil {
		c.mu.Unlock()
		return e
	}
	a := findRecord(sess.Registry(), aid)
	if a == nil {
		c.mu.Unlock()
		return coded(CodeArchiveNotFound)
	}
	path := a.LastPath
	// The record is claimed before the mutex is released: the folder read
	// and the removal are unbounded work, and an Open that landed in that
	// window would hold a file this call is about to unlink and a record
	// this call is about to forget (APP.md §13).
	if c.deleting == nil {
		c.deleting = map[[16]byte]bool{}
	}
	c.deleting[aid] = true
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.deleting, aid)
		c.mu.Unlock()
	}()
	if path == "" {
		// No copy of this archive is recorded anywhere: there is nothing to
		// remove, and forgetting the record against no file is the act the
		// page must ask for separately.
		return coded(CodeArchiveUnreachable)
	}
	// Absence is decided on the parent folder and never on the open of the
	// file itself: a file that is held answers "busy", not "gone".
	switch present, err := leafPresent(path); {
	case err != nil:
		c.log("delete %s: the folder of %s: %v", hexID(aid), filepath.Base(path), err)
		return coded(CodeArchiveUnreachable)
	case !present:
		return coded(CodeArchiveNotThisOne)
	}
	removed, err := deleteArchiveFile(path, aid)
	if err != nil {
		c.log("delete %s: %v", hexID(aid), err)
		switch {
		case errors.Is(err, errArchiveNotThisOne):
			return coded(CodeArchiveNotThisOne)
		case errors.Is(err, errArchiveUnreachable):
			return coded(CodeArchiveUnreachable)
		case errors.Is(err, errArchiveNotOne):
			return coded(CodeArchiveInvalid)
		case errors.Is(err, errArchiveRemoveFailed):
			return coded(CodeArchiveDeleteFailed)
		}
		return coded(CodeArchiveBusy)
	}
	if !removed {
		return coded(CodeArchiveDeleteFailed)
	}
	// The file is gone; only now is the record forgotten. An already
	// forgotten record keeps the forgotten_at it has: the retention clock
	// never restarts.
	e = c.updateRegistryAt(true, func(g *registry, modifiedAt int64) error {
		rec := findRecord(g, aid)
		if rec == nil {
			return errNoChange // purged while the file was being removed
		}
		if rec.Forgotten() {
			return errNoChange // the clock stands (A.15)
		}
		rec.ForgottenAt = modifiedAt
		rec.Revision++
		rec.LastWriter = g.DeviceID
		return nil
	})
	c.mu.Lock()
	if c.vault.presenceKnown != nil {
		c.vault.presenceKnown[aid], c.vault.presence[aid] = true, false
	}
	c.mu.Unlock()
	if e != nil {
		// The file went and the record did not: said as the removal that
		// left the record behind, so the page can offer Forget once more.
		c.log("delete %s: the file is gone and the record was not forgotten: %s", hexID(aid), e.Code)
		c.emitArchivesChanged()
		return coded(CodeArchiveDeleteFailed)
	}
	c.emitArchivesChanged()
	c.emitState()
	return nil
}

// leafPresent reports whether the file's leaf is in its parent folder,
// compared the way the file system here does. An error is the folder's: the
// volume, the share or the folder is not there.
func leafPresent(p string) (bool, error) {
	dir, leaf := filepath.Split(p)
	if dir == "" {
		dir = "."
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if strings.EqualFold(e.Name(), leaf) {
			return true, nil
		}
	}
	return false, nil
}

// The presence pass (APP.md §13).

// presenceBudget is what one path gets before it is abandoned: a path that
// does not answer is never reported missing.
const presenceBudget = 2 * time.Second

// probePath is the one file-system question the pass asks. A var so that a
// test can hold a path unanswered past the budget.
var probePath = func(p string) error { _, err := os.Stat(p); return err }

// CheckFiles refreshes the presence of every record's last_path (APP.md §13).
// It returns as soon as the pass has started: one path at a time with the
// state mutex released, and archives.changed follows when anything changed.
func (c *Core) CheckFiles() *Error {
	c.mu.Lock()
	_, e := c.sessionLocked()
	c.mu.Unlock()
	if e != nil {
		return e
	}
	c.startPresencePass()
	return nil
}

// presenceRow is one record's path, taken under the state mutex.
type presenceRow struct {
	id   [16]byte
	path string
}

// startPresencePass claims the pass and runs it on its own goroutine. The
// claim is made before this returns, so a caller — or a test — can see that
// a pass is running.
func (c *Core) startPresencePass() {
	c.mu.Lock()
	rows, vaultID, ok := c.presenceRowsLocked()
	c.mu.Unlock()
	if ok {
		go c.runPresencePass(rows, vaultID)
	}
}

// presenceRowsLocked claims the pass and lists what it will measure. Caller
// holds the state mutex.
func (c *Core) presenceRowsLocked() ([]presenceRow, [16]byte, bool) {
	sess, e := c.sessionLocked()
	if e != nil || c.presencePass {
		return nil, [16]byte{}, false
	}
	var rows []presenceRow
	for i := range sess.Registry().Archives {
		a := &sess.Registry().Archives[i]
		if a.LastPath == "" || a.Forgotten() {
			continue
		}
		rows = append(rows, presenceRow{a.ArchiveID, a.LastPath})
	}
	c.presencePass = true
	return rows, c.vault.vaultID, true
}

// runPresencePass measures the records' paths one at a time with the state
// mutex released, each with its own budget. Only paths on a local fixed
// volume of this machine are probed: a path whose syntax is not this
// platform's, a removable or network drive and every UNC path are left
// unmeasured, since opening \\host\share because a record says so is an
// outbound authentication to a host someone else named (APP.md §13). At most
// one pass runs at a time, and at most one probe is ever in flight: a path
// that went over its budget stops the rest of the pass measuring anything
// until it answers, which is what bounds the abandoned goroutines to one.
func (c *Core) runPresencePass(rows []presenceRow, vaultID [16]byte) {
	defer func() {
		c.mu.Lock()
		c.presencePass = false
		c.mu.Unlock()
	}()
	changed := false
	for _, r := range rows {
		if !c.localFixed(r.path) {
			continue
		}
		present, decided := c.probe(r.path)
		if !decided {
			continue // over budget: left as it was, never "missing"
		}
		c.mu.Lock()
		if c.vault.vaultID == vaultID && c.vault.state != StateNone {
			if c.vault.presence == nil {
				c.vault.presence, c.vault.presenceKnown = map[[16]byte]bool{}, map[[16]byte]bool{}
			}
			if !c.vault.presenceKnown[r.id] || c.vault.presence[r.id] != present {
				changed = true
			}
			c.vault.presenceKnown[r.id], c.vault.presence[r.id] = true, present
		}
		c.mu.Unlock()
	}
	if changed {
		c.emitArchivesChanged()
	}
}

// probe asks the file system for one path with a budget. The Stat runs on its
// own goroutine because it cannot be cancelled: a path that never answers
// leaks that goroutine until it does. That leak is deliberate and it is
// bounded to exactly one goroutine — the abandoned probe keeps the one probe
// slot, so no further path is probed, in this pass or a later one, until it
// answers (§9 Q16, amendment C). A path left unprobed is left as it was and
// is never reported missing.
func (c *Core) probe(p string) (present, decided bool) {
	// The budget is armed before the question is asked, so that a probe
	// cannot answer — or be observed to have started — before its own clock
	// exists.
	over := make(chan struct{})
	t := c.deps.Clock.AfterFunc(presenceBudget, func() { close(over) })
	defer t.Stop()
	answer := c.armProbe(p)
	if answer == nil {
		return false, false // the abandoned probe still holds the slot
	}
	select {
	case err := <-answer:
		c.mu.Lock()
		if c.probeOut == answer {
			c.probeOut = nil
		}
		c.mu.Unlock()
		return err == nil, true
	case <-over:
		return false, false // the goroutine keeps the slot until it answers
	}
}

// armProbe hands out the one probe slot and starts the question, or answers
// nil when a probe that went over its budget has still not returned. An
// abandoned probe that has since answered is reaped here, so a volume that
// recovers is measured again at the next pass.
func (c *Core) armProbe(p string) chan error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.probeOut != nil {
		select {
		case <-c.probeOut:
			c.probeOut = nil // it answered after it was abandoned
		default:
			return nil
		}
	}
	answer := make(chan error, 1)
	c.probeOut = answer
	go func() { answer <- probePath(p) }()
	return answer
}

// localFixed is the platform's verdict, or the shell's hook where one is
// given (tests, and a build that has no volume API).
func (c *Core) localFixed(p string) bool {
	if p == "" || strings.HasPrefix(p, `\\`) || strings.HasPrefix(p, "//") {
		return false // a UNC path is never probed, on any platform
	}
	if !filepath.IsAbs(p) {
		return false // not this platform's syntax
	}
	if c.deps.Volumes != nil {
		return c.deps.Volumes.LocalFixed(p)
	}
	return isLocalFixed(p)
}

func hexHash(h [32]byte) string { return fmt.Sprintf("%x", h[:]) }

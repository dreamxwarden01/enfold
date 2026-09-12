package app

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"time"

	"github.com/dreamxwarden01/enfold/internal/format"
	"github.com/dreamxwarden01/enfold/internal/keystore"
)

type registry = format.Registry

// vaultState is the session of DESIGN §10 as the core keeps it. Guarded
// by Core.mu.
type vaultState struct {
	state       VaultState
	path        string
	displayName string

	// Plaintext facts, cached at lock so the lock screen needs no handle
	// (APP.md §2.1: vault id, modified_at, the entangle switch, the slots).
	vaultID    [16]byte
	modifiedAt int64
	// entangled is the slot region header's switch and canEnable the §6.4
	// verdict for turning it on: both are read from the open handle, which
	// is nil while Locked, so both are cached like removable (FORMAT.md §6).
	entangled     bool
	canEnable     bool
	vaultFileSize uint64
	slots         []keystore.SlotInfo
	stale         error

	// Unlocked only.
	ks   *keystore.Keystore
	sess *keystore.Session
	// tampered is why the vault is frozen and tamperedReason which check
	// said so (FORMAT.md §6.2, R25). A generation mismatch is met while
	// unlocking and is the file's verdict, so it outlives the ceremony that
	// met it: the lock screen names it rather than inviting another way in.
	tampered       error
	tamperedReason Code
	idle           time.Duration
	absolute       time.Duration

	lastUnlockedAt time.Time
	locksAt        time.Time
	absoluteAt     time.Time
	idleTimer      Timer
	absTimer       Timer
	// idleGen and absGen retire the timers' callbacks. Every arm bumps its
	// own generation and the callback carries the value it was armed with: a
	// callback that has already fired and waits for the mutex — while
	// Activity renews the idle deadline, or while a lock ends the session —
	// finds its generation gone and does nothing. The two are separate
	// because Activity re-arms only the idle timer.
	idleGen   uint64
	absGen    uint64
	lastGrant time.Time // last accepted activity reset

	// note is the one quiet line the lock screen carries from a ceremony
	// that has already ended — today token.password_deadline, beside the
	// pending touch's own line (APP.md §2.2, §6). The next ceremony clears
	// it.
	note      Code
	warnings  map[Code]bool
	broken    error
	missing   string            // a configured vault that could not be opened at start
	damaged   bool              // the missing file is there and not a keystore: the rebuild of APP.md §2.1 applies
	removable map[[16]byte]bool // the slots the invariant lets go of (keystore.Removable)

	// presence is what the last pass found for a record's last_path and
	// presenceKnown which records were measured at all: a record absent from
	// presenceKnown has an empty Status cell, never "missing" (APP.md §13).
	presence      map[[16]byte]bool
	presenceKnown map[[16]byte]bool
	// incoming are the merge handles a records ceremony left: converted
	// records, held until merged, discarded or dropped by a lock trigger.
	incoming map[string]*incomingSet
}

// LockReason is why a lock trigger fired.
type LockReason string

const (
	ReasonManual      LockReason = "manual"
	ReasonIdle        LockReason = "idle"
	ReasonAbsolute    LockReason = "absolute"
	ReasonWorkstation LockReason = "workstation"
	ReasonDisplayOff  LockReason = "display_off"
	ReasonSuspend     LockReason = "suspend"
	ReasonInactive    LockReason = "inactive"
	ReasonLogoff      LockReason = "logoff"
	ReasonExit        LockReason = "exit"
	ReasonBroken      LockReason = "broken"
	ReasonStale       LockReason = "stale"
	ReasonPanic       LockReason = "panic"
)

// openVaultFile reads a keystore's plaintext facts and leaves it closed;
// the state becomes Locked (or Busy when another process holds it). A
// handle a Broken state still holds is closed first, and a pending lock's
// close is waited for, so the open never fails on this process's own lock.
func (c *Core) openVaultFile(path, displayName string) error {
	c.lockWG.Wait()
	c.mu.Lock()
	if old := c.vault.ks; old != nil {
		c.vault.ks = nil
		old.Close()
	}
	c.mu.Unlock()
	ks, err := keystore.Open(path)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		if errors.Is(err, keystore.ErrBusy) {
			// Busy needs the path, so that "try again" can retry it; the
			// facts are the file's, of which nothing is known yet.
			c.vault.path, c.vault.displayName, c.vault.missing, c.vault.damaged = path, displayName, "", false
			c.vault.vaultID, c.vault.modifiedAt, c.vault.slots, c.vault.stale = [16]byte{}, 0, nil, nil
			c.vault.entangled, c.vault.canEnable, c.vault.vaultFileSize = false, false, 0
			c.vault.tampered, c.vault.tamperedReason = nil, ""
			c.dropMeasurementsLocked()
			delete(c.vault.warnings, CodeVaultStale)
			delete(c.vault.warnings, CodeVaultTampered)
			c.vault.state = StateBusy
			c.bump()
			return err
		}
		// A file that does not open does not replace the configured vault:
		// its state, path and facts stand.
		return err
	}
	if ks.VaultID() != c.vault.vaultID {
		// Another vault: receipts owed to the previous one do not carry
		// over, its clean archives are closed, and what was measured or
		// read for its records is another vault's.
		c.owed = map[[16]byte]owedReceipt{}
		c.closeCleanArchivesLocked()
		c.dropMeasurementsLocked()
	}
	c.vault.path, c.vault.displayName, c.vault.missing, c.vault.damaged = path, displayName, "", false
	c.cacheFactsLocked(ks)
	// Tampering is known only after an unlock, and a generation mismatch is
	// a verdict on the file just closed: a fresh open starts clean.
	c.vault.tampered, c.vault.tamperedReason = nil, ""
	delete(c.vault.warnings, CodeVaultTampered)
	ks.Close()
	c.vault.state = StateLocked
	c.vault.broken = nil
	c.bump()
	return nil
}

// dropMeasurementsLocked forgets what was measured or read about this
// vault's records: the file-presence pass's results and every merge handle.
// Caller holds the state mutex.
func (c *Core) dropMeasurementsLocked() {
	c.vault.presence, c.vault.presenceKnown, c.vault.incoming = nil, nil, nil
}

// closeCleanArchivesLocked closes the open archives that are not busy: an
// archive is clean between operations, and one with an operation running
// keeps its handle until that operation ends. Caller holds the state mutex.
func (c *Core) closeCleanArchivesLocked() {
	for _, oa := range c.archives {
		if oa.state == "compacting" || !oa.opMu.TryLock() {
			continue
		}
		c.closeArchiveLocked(oa)
		oa.opMu.Unlock()
	}
}

// cacheFactsLocked copies the lock screen's facts from an open handle.
func (c *Core) cacheFactsLocked(ks *keystore.Keystore) {
	c.vault.vaultID = ks.VaultID()
	c.vault.modifiedAt = ks.ModifiedAt()
	c.vault.entangled = ks.Entangled()
	c.vault.canEnable = ks.CanEnableEntangled() == nil
	c.vault.vaultFileSize = ks.FileSize()
	c.vault.slots = ks.Slots()
	c.vault.removable = make(map[[16]byte]bool, len(c.vault.slots))
	for _, s := range c.vault.slots {
		c.vault.removable[s.RecipientID] = ks.Removable(s.RecipientID)
	}
	c.vault.stale = ks.Stale
	if ks.Stale != nil {
		c.vault.warnings[CodeVaultStale] = true
	} else {
		delete(c.vault.warnings, CodeVaultStale)
	}
}

// OpenVaultFile configures path as the vault — where it is, copying
// nothing — and reads its facts. Like an import it is refused while an
// archive is open, whose commits would land in the wrong registry. An
// empty displayName keeps the name already given.
func (c *Core) OpenVaultFile(path, displayName string) *Error {
	if reservedName(path) {
		return coded(CodeParams)
	}
	c.mu.Lock()
	if c.cer != nil {
		c.mu.Unlock()
		return coded(CodeCeremonyRunning)
	}
	if c.pending != nil {
		// A pending touch holds the file: opened again when it ends.
		c.mu.Unlock()
		return coded(CodeTokenPending)
	}
	if e := c.importGateLocked(false); e != nil && !(c.vault.state == StateBusy && samePath(path, c.vault.path)) {
		c.mu.Unlock()
		return e
	}
	if c.vault.state != StateNone && len(c.archives) > 0 && !samePath(path, c.vault.path) {
		c.mu.Unlock()
		return coded(CodeArchivesOpen)
	}
	if displayName == "" {
		displayName = mustString(c.settings.DisplayName, defaultDisplayName)
	}
	c.mu.Unlock()
	if err := c.openVaultFile(path, displayName); err != nil {
		if errors.Is(err, keystore.ErrBusy) {
			c.emitState()
			return coded(CodeVaultBusy)
		}
		c.mu.Lock()
		if c.vault.state == StateNone && samePath(path, c.vault.missing) {
			c.noteMissingLocked(path, err) // "try again" judges damage afresh
		}
		c.mu.Unlock()
		c.emitState()
		switch {
		case os.IsNotExist(err):
			return coded(CodeVaultNotFound)
		case errors.Is(err, format.ErrInvalid), errors.Is(err, format.ErrTruncated):
			return coded(CodeVaultInvalid)
		}
		return c.fail("open vault", err)
	}
	c.mu.Lock()
	c.settings.VaultPath, c.settings.DisplayName = c.overrideFor(path), displayName
	file := c.settings
	c.mu.Unlock()
	if err := saveSettings(c.deps.DataDir, file); err != nil {
		c.log("settings: %v", err)
	}
	c.emitState()
	return nil
}

// Status is the whole state, stamped with the current sequence.
func (c *Core) Status() VaultStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statusLocked()
}

func (c *Core) statusLocked() VaultStatus {
	v := &c.vault
	st := VaultStatus{
		Seq: c.seq, State: v.state, Path: v.path, DisplayName: v.displayName,
		ModifiedAt: v.modifiedAt, Entangled: v.entangled, VaultFileSize: v.vaultFileSize,
		Tampered: v.tampered != nil, TamperedReason: v.tamperedReason,
		Warnings: []Code{}, Ops: []OpView{},
	}
	if !v.lastUnlockedAt.IsZero() {
		st.LastUnlockedAt = v.lastUnlockedAt.Unix()
	}
	if v.state == StateUnlocked {
		st.LocksAt, st.AbsoluteAt = v.locksAt.Unix(), v.absoluteAt.Unix()
	}
	for code := range v.warnings {
		st.Warnings = append(st.Warnings, code)
	}
	for _, s := range v.slots {
		switch {
		case s.PublicKey != nil:
			st.HasHardwareSlot = true
		case s.Type == format.SlotStandalonePassword:
			st.HasPasswordSlot = true
		}
	}
	st.SetupNeeded = v.state != StateNone && len(v.slots) > 0 && !st.HasHardwareSlot && !st.HasPasswordSlot
	st.DefaultPath = c.defaultVaultPath()
	st.MissingPath = v.missing
	st.KeptElsewhere = v.state != StateNone && !samePath(v.path, c.defaultVaultPath())
	st.RetiredCopies = len(c.retired)
	if n := len(c.retired); n > 0 {
		st.RetiredPath = c.retired[n-1]
	}
	st.Damaged = v.missing != "" && v.damaged
	if n := len(c.damaged); n > 0 {
		st.DamagedCopyPath = c.damaged[n-1]
	}
	if c.cer != nil {
		cs := c.cer.state
		st.Ceremony = &cs
	}
	st.PendingTouch = c.pending != nil
	st.Note = v.note
	for _, o := range c.ops {
		st.Ops = append(st.Ops, o.view())
	}
	for range c.archives {
		st.OpenArchives++
	}
	return st
}

// emitState publishes the whole state after a change. Callers hold no
// lock.
func (c *Core) emitState() {
	c.mu.Lock()
	c.bump()
	st := c.statusLocked()
	c.mu.Unlock()
	c.emit(EventVaultState, st)
}

// session returns the live Session or the reason there is none. Callers
// hold the state mutex.
func (c *Core) sessionLocked() (*keystore.Session, *Error) {
	v := &c.vault
	switch v.state {
	case StateUnlocked:
	case StateBroken:
		return nil, coded(CodeVaultBroken)
	case StateNone:
		return nil, coded(CodeNoVault)
	default:
		return nil, coded(CodeNeedsUnlock)
	}
	if v.sess == nil {
		return nil, coded(CodeNeedsUnlock)
	}
	if err := v.sess.Live(); err != nil {
		if errors.Is(err, keystore.ErrStale) {
			if c.cer != nil && c.cer.mutation {
				// A rotation has committed and is about to install the
				// re-derived session; nothing writes meanwhile.
				return nil, coded(CodeCeremonyRunning)
			}
			// An invariant violation: the core should have re-derived it.
			// The stale keys are not left in memory behind an Unlocked state.
			c.log("session stale in Unlocked state: %v", err)
			go c.LockNow(ReasonStale)
			return nil, coded(CodeNeedsUnlock)
		}
		return nil, classify(err)
	}
	return v.sess, nil
}

// updateRegistry runs fn against the registry under the session, holding
// the state mutex across the commit so a software lock cannot land between
// a caller's archive commit and this receipt. Long callers must not use
// it for anything but the registry write itself.
func (c *Core) updateRegistry(fn func(g *registry) error) *Error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.updateRegistryLocked(fn)
}

func (c *Core) updateRegistryLocked(fn func(g *registry) error) *Error {
	return c.updateRegistryAtLocked(false, func(g *registry, _ int64) error { return fn(g) })
}

// updateRegistryAtLocked is the one registry write, with the commit's own
// modified_at handed to fn (FORMAT.md §18.2: a decision that depends on it is
// made against the value that lands, never against a second clock reading).
//
// allowForgotten is the intent of APP.md §13's rule "no registry write updates
// a forgotten record": only Restore, a merge tick that restores one, Delete's
// own forget and the purge set it, and every other write is refused if it
// changed a record the registry held as forgotten. The guard is the backstop
// behind the per-call refusals, not a substitute for them. Caller holds the
// state mutex.
func (c *Core) updateRegistryAtLocked(allowForgotten bool, fn func(g *registry, modifiedAt int64) error) *Error {
	sess, e := c.sessionLocked()
	if e != nil {
		return e
	}
	// A slot change commits to the same handle from its own goroutine; the
	// Keystore is not safe for concurrent use, so registry writes wait
	// until the ceremony is over (an operation's commit owes its receipt
	// meanwhile).
	if (c.cer != nil && c.cer.mutation) || (c.pending != nil && c.pending.vaultHandle) {
		return coded(CodeCeremonyRunning)
	}
	err := sess.UpdateRegistryAt(func(g *registry, at int64) error {
		var before map[[16]byte]format.ArchiveRecord
		if !allowForgotten {
			before = forgottenRecords(g)
		}
		if err := fn(g, at); err != nil {
			return err
		}
		if !allowForgotten {
			if id, ok := forgottenTouched(before, g); ok {
				return fmt.Errorf("%w: archive %x is forgotten", coded(CodeArchiveForgotten), id)
			}
		}
		return nil
	})
	if errors.Is(err, errNoChange) {
		// fn found nothing to write. The commit is abandoned rather than
		// made, so a write that changed nothing does not restamp the
		// vault's modified_at (FORMAT.md R35, §18.2) — the value the lock
		// screen shows and InspectFile compares.
		return nil
	}
	if err != nil {
		if errors.Is(err, keystore.ErrIndeterminate) || errors.Is(err, keystore.ErrConflict) {
			c.brokenLocked(err)
		}
		return classify(err)
	}
	c.vault.modifiedAt = c.vault.ks.ModifiedAt()
	c.vault.vaultFileSize = c.vault.ks.FileSize()
	return nil
}

// forgottenRecords copies the forgotten records of g, versions included, so
// that a write can be held to leaving them alone.
func forgottenRecords(g *registry) map[[16]byte]format.ArchiveRecord {
	var m map[[16]byte]format.ArchiveRecord
	for i := range g.Archives {
		a := &g.Archives[i]
		if !a.Forgotten() {
			continue
		}
		if m == nil {
			m = map[[16]byte]format.ArchiveRecord{}
		}
		cp := *a
		cp.Versions = append([]format.VersionRecord(nil), a.Versions...)
		m[a.ArchiveID] = cp
	}
	return m
}

// forgottenTouched reports the first record that was forgotten before the
// write and is not byte-for-byte what it was. A record that went entirely is
// the purge's doing and is checked by its caller's intent, not here.
func forgottenTouched(before map[[16]byte]format.ArchiveRecord, g *registry) ([16]byte, bool) {
	for i := range g.Archives {
		a := &g.Archives[i]
		was, had := before[a.ArchiveID]
		if !had {
			continue
		}
		if !reflect.DeepEqual(was, *a) {
			return a.ArchiveID, true
		}
	}
	return [16]byte{}, false
}

// brokenLocked enters Broken: keys zeroed, timers stopped, archives left
// open. Caller holds the state mutex.
func (c *Core) brokenLocked(err error) {
	v := &c.vault
	v.broken = err
	c.stopTimersLocked()
	if v.sess != nil {
		v.sess.Lock()
		v.sess = nil
	}
	v.state = StateBroken
	c.bump()
}

// Reopen leaves Broken (or refreshes a Locked vault's facts): the handle
// is closed and the file reopened.
func (c *Core) Reopen() *Error {
	c.mu.Lock()
	if c.vault.state == StateUnlocked || c.vault.state == StateUnlocking || c.vault.state == StateReleasing || c.cer != nil {
		c.mu.Unlock()
		return coded(CodeCeremonyRunning)
	}
	if c.pending != nil {
		c.mu.Unlock()
		return coded(CodeTokenPending)
	}
	if c.vault.ks != nil {
		c.vault.ks.Close()
		c.vault.ks = nil
	}
	path, name := c.vault.path, c.vault.displayName
	c.mu.Unlock()
	if path == "" {
		return coded(CodeNoVault)
	}
	err := c.openVaultFile(path, name) // waits for a pending lock's close
	if err != nil && !errors.Is(err, keystore.ErrBusy) {
		// The vault's file refuses to open: said so, as at start, so that
		// the screen names it — and offers the rebuild when it is damaged
		// rather than absent (APP.md §2.1).
		c.mu.Lock()
		c.dropFactsLocked(path, name)
		c.noteMissingLocked(path, err)
		c.mu.Unlock()
	}
	c.emitState()
	if err != nil {
		if errors.Is(err, keystore.ErrBusy) {
			return coded(CodeVaultBusy)
		}
		return c.fail("reopen", err)
	}
	return nil
}

// Lock is the manual lock.
func (c *Core) Lock() { c.LockNow(ReasonManual) }

// LockNow is the lock trigger, accepted in every state. It zeroes the
// session keys synchronously under the state mutex — a Modern Standby
// machine may freeze the process about two seconds after the display-off
// notification — and hands everything unbounded to a goroutine. Safe to
// call from any thread, including a Win32 message loop.
func (c *Core) LockNow(reason LockReason) {
	c.mu.Lock()
	after := c.lockLocked(reason)
	c.mu.Unlock()
	after()
}

// lockLocked is the whole of a lock that happens under the state mutex; it
// returns the rest — the preview's secrets and the unbounded half — for the
// caller to run once it releases the mutex. A caller that must decide and
// lock under one hold keeps it across both: a timer checks its deadline and
// locks without letting an Activity grant in between (APP.md §2). Caller
// holds the state mutex.
func (c *Core) lockLocked(reason LockReason) func() {
	v := &c.vault
	// A ceremony in any state is latched and cancelled: its VMK, its card
	// and its prompts must not outlive the lock (DESIGN §10).
	cer := c.cer
	if cer != nil {
		cer.latch = true
		if cer.cancelReason == "" {
			cer.cancelReason = string(reason)
		}
		cer.cancel()
	}
	// A pending touch is never adopted after a trigger (§2.2).
	c.dropPendingLocked()
	// A merge's converted records hold this vault's archive keys: dropped in
	// every state, like the recovery key a reveal holds (APP.md §13).
	c.vault.incoming = nil
	// A recovery key held for a reveal (APP.md §3 Keys) is dropped in every
	// state: nothing decrypted outlives a trigger.
	preview := c.preview
	if v.state != StateUnlocked {
		return func() {
			if preview != nil {
				preview.dropAllSecrets()
			}
		}
	}
	c.stopTimersLocked()
	if v.sess != nil {
		v.sess.Lock() // zeroes the keys; microseconds
	}
	ks := v.ks
	v.sess, v.ks = nil, nil
	v.tampered, v.tamperedReason = nil, ""
	v.state = StateLocked
	c.bump()
	c.lockWG.Add(1)
	return func() {
		if preview != nil {
			preview.dropAllSecrets()
		}
		go c.afterLock(ks, cer, reason)
	}
}

// afterLock is the unbounded half of a lock: it waits for a cancelled
// ceremony to finish with the handle, refreshes the cached facts, and
// closes it.
func (c *Core) afterLock(ks *keystore.Keystore, cer *ceremony, reason LockReason) {
	defer c.lockWG.Done()
	if cer != nil {
		<-cer.done // its deferred Unlocked.Close and card release run first
	}
	if done := c.pendingDone(); done != nil {
		<-done // a pending touch inside keystore.Unlock on the handle: it ends first
	}
	if ks != nil {
		// Refresh the cached facts from the handle we still hold, then
		// close it: nothing decrypted survives a lock.
		c.mu.Lock()
		c.cacheFactsLocked(ks)
		c.mu.Unlock()
		ks.Close()
	}
	c.log("locked: %s", reason)
	c.emitState()
	c.emitArchivesChanged()
}

// stopTimersLocked stops both session timers and retires their callbacks,
// so that one already past its Stop cannot lock a later session.
func (c *Core) stopTimersLocked() {
	c.vault.idleGen++
	c.vault.absGen++
	if c.vault.idleTimer != nil {
		c.vault.idleTimer.Stop()
		c.vault.idleTimer = nil
	}
	if c.vault.absTimer != nil {
		c.vault.absTimer.Stop()
		c.vault.absTimer = nil
	}
}

// armTimersLocked (re)arms both timers from the registry's values.
func (c *Core) armTimersLocked() {
	v := &c.vault
	if v.state != StateUnlocked || v.sess == nil {
		return
	}
	var idleMin, absMin uint16
	if g := v.sess.Registry(); g != nil {
		idleMin, absMin = g.IdleMinutes, g.AbsoluteMinutes
	}
	idle, abs, clamped := timeouts(idleMin, absMin)
	if clamped {
		v.warnings["vault.timeouts_clamped"] = true
	} else {
		delete(v.warnings, "vault.timeouts_clamped")
	}
	v.idle, v.absolute = idle, abs
	c.stopTimersLocked()
	now := c.now()
	// Re-arming from a non-input path (unlock, rotation, a settings change)
	// never extends a running idle deadline; only Activity does that.
	next := now.Add(idle)
	if v.locksAt.IsZero() || !v.locksAt.After(now) || next.Before(v.locksAt) {
		v.locksAt = next
	}
	v.idleGen++
	idleGen := v.idleGen
	v.idleTimer = c.deps.Clock.AfterFunc(v.locksAt.Sub(now), func() { c.idleExpired(idleGen) })
	// The absolute deadline follows the current setting from the unlock.
	v.absoluteAt = v.lastUnlockedAt.Add(abs)
	remaining := v.absoluteAt.Sub(now)
	if remaining <= 0 {
		remaining = time.Millisecond
	}
	v.absGen++
	absGen := v.absGen
	v.absTimer = c.deps.Clock.AfterFunc(remaining, func() { c.absoluteExpired(absGen) })
}

// idleExpired is the idle timer's callback. It locks only while the vault
// is Unlocked, this timer is still the session's own, and the deadline has
// really passed: a callback that fired while Activity held the mutex finds
// the deadline renewed and returns, and the timer Activity armed locks in
// its place. The check and the lock are one hold of the mutex, so no grant
// lands between them (APP.md §2).
func (c *Core) idleExpired(gen uint64) {
	c.mu.Lock()
	v := &c.vault
	if v.state != StateUnlocked || v.idleGen != gen || c.now().Before(v.locksAt) {
		c.mu.Unlock()
		return
	}
	after := c.lockLocked(ReasonIdle)
	c.mu.Unlock()
	after()
}

// absoluteExpired is the absolute timer's callback. The cap applies
// whatever the input did, so nothing but this session's own deadline
// answers for it; the check and the lock are one hold, as the idle timer's
// are.
func (c *Core) absoluteExpired(gen uint64) {
	c.mu.Lock()
	v := &c.vault
	if v.state != StateUnlocked || v.absGen != gen || c.now().Before(v.absoluteAt) {
		c.mu.Unlock()
		return
	}
	after := c.lockLocked(ReasonAbsolute)
	c.mu.Unlock()
	after()
}

// Activity is the frontend's heartbeat: a request to reset the idle timer,
// granted only when the session's own input agrees, at most every few
// seconds. Never a proof of presence.
func (c *Core) Activity() {
	c.mu.Lock()
	v := &c.vault
	if v.state != StateUnlocked {
		c.mu.Unlock()
		return
	}
	now := c.now()
	if now.Sub(v.lastGrant) < 2*time.Second {
		c.mu.Unlock()
		return
	}
	if c.deps.Input != nil {
		t, ok := c.deps.Input.LastInput()
		if !ok || !t.After(v.lastGrant) {
			c.mu.Unlock()
			return
		}
	}
	v.lastGrant = now
	if v.idleTimer != nil {
		v.idleTimer.Stop()
	}
	v.locksAt = now.Add(v.idle)
	v.idleGen++
	gen := v.idleGen
	v.idleTimer = c.deps.Clock.AfterFunc(v.idle, func() { c.idleExpired(gen) })
	c.mu.Unlock()
	// Every accepted grant emits the state once; the grant itself is what is
	// rate-limited, to one every two seconds.
	c.emitState()
}

// SetWarning raises or clears a warning the shell discovers (lock-trigger
// detection unavailable, BitLocker) and tells the frontend.
func (c *Core) SetWarning(code Code, on bool) {
	c.mu.Lock()
	if on {
		c.vault.warnings[code] = true
	} else {
		delete(c.vault.warnings, code)
	}
	c.mu.Unlock()
	if on {
		c.emit(EventVaultWarning, Warning{Code: code})
	}
	c.emitState()
}

// Readers lists the YubiKey readers.
func (c *Core) Readers() ([]Reader, *Error) {
	if c.deps.Cards == nil {
		return []Reader{}, nil
	}
	names, err := c.deps.Cards.Readers()
	if err != nil {
		if errors.Is(err, ErrTokenNoService) {
			return []Reader{}, nil // the service starts with the first reader
		}
		return nil, c.fail("readers", err)
	}
	out := make([]Reader, 0, len(names))
	for _, n := range names {
		out = append(out, Reader{Name: n})
	}
	return out, nil
}

// publishUnlocked installs a freshly derived session. Caller holds the
// state mutex; the ceremony's latch has been checked by the caller.
func (c *Core) publishUnlockedLocked(ks *keystore.Keystore, unl *keystore.Unlocked) {
	v := &c.vault
	sess, err := unl.Session()
	if err != nil {
		// Cannot happen right after an Unlock; treat as broken.
		c.log("session after unlock: %v", err)
		unl.Close()
		ks.Close()
		v.state = StateLocked
		c.bump()
		return
	}
	v.tampered = unl.Tampered()
	if v.tampered != nil {
		v.tamperedReason = CodeTamperedHash
	} else {
		v.tamperedReason = ""
	}
	unl.Close()
	if v.ks != nil && v.ks != ks {
		v.ks.Close()
	}
	if v.sess != nil {
		v.sess.Lock() // never replaced without being zeroed
	}
	v.ks, v.sess = ks, sess
	c.cacheFactsLocked(ks)
	now := c.now()
	v.lastUnlockedAt = now
	v.absoluteAt = time.Time{}
	v.locksAt = time.Time{}
	v.lastGrant = now
	v.state = StateUnlocked
	if v.tampered != nil {
		v.warnings[CodeVaultTampered] = true
	} else {
		delete(v.warnings, CodeVaultTampered)
	}
	c.armTimersLocked()
	c.settleAfterUnlockLocked()
	c.bump()
}

// settleAfterUnlockLocked is everything a successful unlock of the vault
// kept here settles before any archive is opened (APP.md §13): the receipts
// a lock stranded, then the purge of forgotten records whose retention has
// run out, then the file-presence pass. One site, reached from the one place
// that publishes the vault, so the ordering is structural rather than
// remembered — and the unlock of a staged copy (VerifyBackup, InspectFile,
// InspectRecords) never publishes, so it never purges. Caller holds the
// state mutex.
func (c *Core) settleAfterUnlockLocked() {
	c.applyOwedLocked()
	c.purgeForgottenLocked()
	if rows, vaultID, ok := c.presenceRowsLocked(); ok {
		go c.runPresencePass(rows, vaultID)
	}
}

// purgeForgottenLocked drops the forgotten records whose retention has run
// out, in one registry write whose own modified_at decides (FORMAT.md §18.2),
// and announces what went. It is skipped entirely while the vault is
// Tampered — every record is left to the next unlock — and a write refused
// because a mutation ceremony holds the handle loses nothing: the records
// stand and the next unlock purges them. Caller holds the state mutex.
func (c *Core) purgeForgottenLocked() {
	if c.vault.tampered != nil {
		return
	}
	sess, e := c.sessionLocked()
	if e != nil {
		return
	}
	any := false
	for i := range sess.Registry().Archives {
		if sess.Registry().Archives[i].Forgotten() {
			any = true
			break
		}
	}
	if !any {
		return
	}
	var purged []string
	e = c.updateRegistryAtLocked(true, func(g *registry, modifiedAt int64) error {
		for _, a := range g.PurgeForgotten(modifiedAt) {
			purged = append(purged, a.Name)
		}
		if len(purged) == 0 {
			// Nothing is due: the write is abandoned rather than committed,
			// so a vault that opens every day does not restamp its
			// modified_at for a purge that dropped nothing (R35).
			return errNoChange
		}
		return nil
	})
	switch {
	case len(purged) == 0:
		return
	case e != nil:
		c.log("purge of forgotten records: %s", e.Code)
		return
	}
	c.log("purge: %d forgotten record(s) dropped", len(purged))
	go c.emit(EventArchivesChanged, ArchivesChanged{Purged: purged})
}

// errNoChange abandons a registry write that would change nothing, so the
// commit — and with it modified_at — never records a write that wrote
// nothing (FORMAT.md R35, §18.2). updateRegistryAtLocked answers the caller
// with success. One view of the rule for the purge, a second Forget, a
// Restore of a record that is not forgotten, and a rename or a description
// set to the value already held.
var errNoChange = errors.New("app: the registry write changes nothing")

// applyOwedLocked writes receipts stranded by a lock before any archive is
// opened under the new session. Caller holds the state mutex.
func (c *Core) applyOwedLocked() {
	if len(c.owed) == 0 {
		return
	}
	owed := c.owed
	c.owed = map[[16]byte]owedReceipt{}
	e := c.updateRegistryLocked(func(g *registry) error {
		for id, r := range owed {
			for i := range g.Archives {
				a := &g.Archives[i]
				if a.ArchiveID != id || a.CurrentKID != r.kid {
					continue
				}
				if a.Forgotten() {
					// The record was forgotten while the receipt was owed:
					// its bookkeeping is moot, and no write but Restore's
					// updates a forgotten record (APP.md §13).
					continue
				}
				a.LastStoredSize, a.LastWrittenAt, a.LastSeq = r.size, r.writtenAt, r.seq
				a.Revision++
				a.LastWriter = g.DeviceID
				if r.hash != nil {
					a.LastCiphertextHash, a.HashAtSeq = *r.hash, r.seq
				}
			}
		}
		return nil
	})
	if e != nil {
		c.log("owed receipts: %v", e)
		for id, r := range owed {
			c.owed[id] = r
		}
		return
	}
	for id := range owed {
		if oa := c.archives[id]; oa != nil {
			oa.receiptOwed = false
		}
	}
}

// vaultIDLocked is the id the lock screen knows.
func (c *Core) vaultIDLocked() [16]byte { return c.vault.vaultID }

// mustString is a small helper for building labels.
func mustString(s string, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

var _ = fmt.Sprintf

package app

import (
	"errors"
	"fmt"
	"os"
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

	// Plaintext facts, cached at lock so the lock screen needs no handle.
	vaultID         [16]byte
	modifiedAt      int64
	rotationPending bool
	slots           []keystore.SlotInfo
	stale           error

	// Unlocked only.
	ks       *keystore.Keystore
	sess     *keystore.Session
	tampered error
	idle     time.Duration
	absolute time.Duration

	lastUnlockedAt time.Time
	locksAt        time.Time
	absoluteAt     time.Time
	idleTimer      Timer
	absTimer       Timer
	lastGrant      time.Time // last accepted activity reset

	warnings map[Code]bool
	broken   error
	missing  string            // a configured vault that could not be opened at start
	damaged  bool              // the missing file is there and not a keystore: the rebuild of APP.md §2.1 applies
	escrowed map[[16]byte]bool // the recovery slots that can be shown again (FORMAT R38); Unlocked only
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
			c.vault.vaultID, c.vault.modifiedAt, c.vault.rotationPending, c.vault.slots, c.vault.stale = [16]byte{}, 0, false, nil, nil
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
		// over, and its clean archives are closed.
		c.owed = map[[16]byte]owedReceipt{}
		c.closeCleanArchivesLocked()
	}
	c.vault.path, c.vault.displayName, c.vault.missing, c.vault.damaged = path, displayName, "", false
	c.cacheFactsLocked(ks)
	delete(c.vault.warnings, CodeVaultTampered) // known only after an unlock; a fresh file starts clean
	ks.Close()
	c.vault.state = StateLocked
	c.vault.broken = nil
	c.bump()
	return nil
}

// closeCleanArchivesLocked closes the open archives that hold no staged
// change and are not busy; the dirty ones keep their transaction until
// their cap. Caller holds the state mutex.
func (c *Core) closeCleanArchivesLocked() {
	for _, oa := range c.archives {
		if oa.tx != nil || oa.state == "compacting" || !oa.opMu.TryLock() {
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
	c.vault.rotationPending = ks.RotationPending()
	c.vault.slots = ks.Slots()
	c.vault.stale = ks.Stale
	if ks.Stale != nil {
		c.vault.warnings[CodeVaultStale] = true
	} else {
		delete(c.vault.warnings, CodeVaultStale)
	}
}

// OpenVaultFile configures path as the vault — where it is, copying
// nothing — and reads its facts. Like an import it is refused while an
// archive is open, whose saves would land in the wrong registry. An
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
		ModifiedAt: v.modifiedAt, RotationPending: v.rotationPending, Tampered: v.tampered != nil,
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
	for _, o := range c.ops {
		st.Ops = append(st.Ops, o.view())
	}
	for _, a := range c.archives {
		st.OpenArchives++
		if a.dirty() > 0 {
			st.DirtyArchives++
		}
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
	sess, e := c.sessionLocked()
	if e != nil {
		return e
	}
	// A slot change commits to the same handle from its own goroutine; the
	// Keystore is not safe for concurrent use, so registry writes wait
	// until the ceremony is over (a Save owes its receipt meanwhile).
	if c.cer != nil && c.cer.mutation {
		return coded(CodeCeremonyRunning)
	}
	if err := sess.UpdateRegistry(fn); err != nil {
		if errors.Is(err, keystore.ErrIndeterminate) || errors.Is(err, keystore.ErrConflict) {
			c.brokenLocked(err)
		}
		return classify(err)
	}
	c.vault.modifiedAt = c.vault.ks.ModifiedAt()
	c.vault.rotationPending = c.vault.ks.RotationPending()
	return nil
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
	v.escrowed = nil
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
	// A recovery key held for a reveal (APP.md §3 Keys) is dropped in every
	// state: nothing decrypted outlives a trigger.
	preview := c.preview
	if v.state != StateUnlocked {
		c.mu.Unlock()
		if preview != nil {
			preview.dropAllSecrets()
		}
		return
	}
	c.stopTimersLocked()
	if v.sess != nil {
		v.sess.Lock() // zeroes the keys; microseconds
	}
	ks := v.ks
	v.sess, v.ks = nil, nil
	v.tampered = nil
	v.escrowed = nil
	v.state = StateLocked
	c.bump()
	c.lockWG.Add(1)
	c.mu.Unlock()
	if preview != nil {
		preview.dropAllSecrets()
	}
	go c.afterLock(ks, cer, reason)
}

// afterLock is the unbounded half of a lock: it waits for a cancelled
// ceremony to finish with the handle, refreshes the cached facts, and
// closes it.
func (c *Core) afterLock(ks *keystore.Keystore, cer *ceremony, reason LockReason) {
	defer c.lockWG.Done()
	if cer != nil {
		<-cer.done // its deferred Unlocked.Close and card release run first
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

// stopTimersLocked stops both session timers.
func (c *Core) stopTimersLocked() {
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
	v.idleTimer = c.deps.Clock.AfterFunc(v.locksAt.Sub(now), func() { c.LockNow(ReasonIdle) })
	// The absolute deadline follows the current setting from the unlock.
	v.absoluteAt = v.lastUnlockedAt.Add(abs)
	remaining := v.absoluteAt.Sub(now)
	if remaining <= 0 {
		remaining = time.Millisecond
	}
	v.absTimer = c.deps.Clock.AfterFunc(remaining, func() { c.LockNow(ReasonAbsolute) })
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
	v.idleTimer = c.deps.Clock.AfterFunc(v.idle, func() { c.LockNow(ReasonIdle) })
	changed := now.Sub(v.lastGrant) > 0 // always; the emit is rate-limited below
	_ = changed
	c.mu.Unlock()
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
	unl.Close()
	if v.ks != nil && v.ks != ks {
		v.ks.Close()
	}
	if v.sess != nil {
		v.sess.Lock() // never replaced without being zeroed
	}
	v.ks, v.sess = ks, sess
	c.cacheFactsLocked(ks)
	c.refreshEscrowedLocked()
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
	c.bump()
}

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

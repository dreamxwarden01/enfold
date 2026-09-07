package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/dreamxwarden01/enfold/internal/format"
	"github.com/dreamxwarden01/enfold/internal/keystore"
)

// The vault file's place and the files that arrive from elsewhere
// (APP.md §2.1, One vault): one keystore per Windows user at
// defaultVaultPath; a vault or a backup imported by proving it first on a
// staged copy and installing it only then; the setup that turns an
// adopted backup into a vault; a backup verified without touching
// anything.

const (
	incomingPrefix = "vault.incoming-" // <prefix><id>.eks: an import or a build, beside its destination, until installed
	inspectPrefix  = "vault.inspect-"  // <prefix><id>.eks: a copy being inspected or verified, removed after
	retiredPrefix  = "vault-replaced-" // <prefix><unix>-<n>.eks: a vault kept as a dated copy
	damagedPrefix  = "vault-damaged-"  // <prefix><unix>-<n>.eks: the vault's own file, refused, kept for salvage
	otherPrefix    = "file-replaced-"  // <prefix><unix>-<n>.bin: something else that sat at a chosen place
)

// maxKeystoreBytes bounds what is copied into the data folder: a keystore
// is kilobytes to a few megabytes; anything larger is not one.
const maxKeystoreBytes = 64 << 20

// reservedName reports a path that names one of the staging files, which
// can never be the vault: an import would remove it.
func reservedName(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	return strings.HasPrefix(base, incomingPrefix) || strings.HasPrefix(base, inspectPrefix)
}

// stagingName is a fresh name for a staged file: stagings may overlap,
// and nothing of the user's is ever removed to make room.
func stagingName(prefix string) string { return prefix + randomID()[:12] + ".eks" }

// sweepStaging removes what an interrupted import, build or inspection
// left in the data folder. A build kept elsewhere leaves its incoming
// file beside its destination, outside this folder.
func (c *Core) sweepStaging() {
	entries, _ := os.ReadDir(c.deps.DataDir)
	for _, e := range entries {
		if !e.IsDir() && reservedName(e.Name()) {
			os.Remove(filepath.Join(c.deps.DataDir, e.Name()))
		}
	}
}

// stage copies src into the data folder under name, read raw: the source
// is never opened as a keystore (it may sit on read-only media, and its
// bytes may change under a reader), and what is inspected is what would
// be installed. Only a regular file of at most maxKeystoreBytes is taken.
func (c *Core) stage(src, name string) (string, error) {
	dst := filepath.Join(c.deps.DataDir, name)
	if samePath(src, dst) {
		return "", fs.ErrInvalid
	}
	if fi, err := os.Stat(src); err != nil {
		return "", err
	} else if !fi.Mode().IsRegular() || fi.Size() > maxKeystoreBytes {
		return "", fs.ErrInvalid
	}
	os.Remove(dst)
	if err := copyFile(src, dst, maxKeystoreBytes); err != nil {
		os.Remove(dst)
		return "", err
	}
	return dst, nil
}

// inspectName is a fresh staging name: inspections may overlap.
func (c *Core) inspectName() string { return stagingName(inspectPrefix) }

// copyFile writes dst, which must not exist, from src, and syncs it; more
// than limit bytes is refused as not a keystore.
func copyFile(src, dst string, limit int64) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	n, err := io.CopyN(out, in, limit+1)
	if err != nil && !errors.Is(err, io.EOF) {
		out.Close()
		return err
	}
	if n > limit {
		out.Close()
		return fs.ErrInvalid
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// InspectFile reads a keystore file's plaintext facts without a
// credential and says what it is: a backup (recovery slots only, R28's
// export) or a vault, and whether it is the vault kept here and newer —
// plaintext claims, proven only by the unlock an import runs. The file
// is read through a staged copy, so read-only media and a file another
// process holds work too.
func (c *Core) InspectFile(path string) (FileInfo, *Error) {
	c.mu.Lock()
	vid, mod, own, slots := c.vault.vaultID, c.vault.modifiedAt, c.vault.path, c.vault.slots
	configured := c.vault.state != StateNone
	busy := c.cer != nil
	c.mu.Unlock()
	if busy {
		return FileInfo{}, coded(CodeCeremonyRunning)
	}
	if configured && samePath(path, own) {
		info := FileInfo{Path: path, Kind: FileKindVault, ModifiedAt: mod, VaultMatches: true}
		countSlots(&info, slots)
		return info, nil
	}
	if _, err := os.Stat(path); err != nil {
		return FileInfo{}, c.fileError("inspect", err)
	}
	staged, err := c.stage(path, c.inspectName())
	if err != nil {
		return FileInfo{}, c.fileError("inspect", err)
	}
	defer os.Remove(staged)
	ks, err := keystore.Open(staged)
	if err != nil {
		return FileInfo{}, c.fileError("inspect", err)
	}
	defer ks.Close()
	info := inspect(ks, path)
	info.VaultMatches = configured && ks.VaultID() == vid
	info.Newer = info.VaultMatches && ks.ModifiedAt() > mod
	return info, nil
}

// inspect classifies an open keystore file.
func inspect(ks *keystore.Keystore, path string) FileInfo {
	info := FileInfo{Path: path, ModifiedAt: ks.ModifiedAt()}
	countSlots(&info, ks.Slots())
	return info
}

func countSlots(info *FileInfo, slots []keystore.SlotInfo) {
	info.Kind = FileKindBackup
	for _, s := range slots {
		info.SlotCount++
		switch {
		case s.PublicKey != nil:
			info.Hardware++
		case s.Type == format.SlotStandalonePassword:
			info.Password++
		case s.Type == format.SlotRecovery:
			info.Recovery++
		}
		if s.Type != format.SlotRecovery {
			info.Kind = FileKindVault
		}
	}
}

// fileError maps what opening a keystore file can fail with.
func (c *Core) fileError(what string, err error) *Error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return coded(CodeVaultNotFound)
	case errors.Is(err, keystore.ErrBusy):
		return coded(CodeVaultBusy)
	case errors.Is(err, format.ErrInvalid), errors.Is(err, format.ErrTruncated), errors.Is(err, fs.ErrInvalid):
		return coded(CodeVaultInvalid)
	}
	return c.fail(what, err)
}

// setupNeededLocked reports a vault with only recovery slots: an adopted
// backup whose setup did not finish. Caller holds the state mutex.
func (c *Core) setupNeededLocked() bool {
	if c.vault.state == StateNone || len(c.vault.slots) == 0 {
		return false
	}
	for _, s := range c.vault.slots {
		if s.Type != format.SlotRecovery {
			return false
		}
	}
	return true
}

// scanRetired lists the retired vaults and the damaged copies in the data
// folder, oldest first.
func (c *Core) scanRetired() {
	entries, _ := os.ReadDir(c.deps.DataDir)
	var retired, damaged []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".eks") {
			continue
		}
		switch {
		case strings.HasPrefix(n, retiredPrefix):
			retired = append(retired, filepath.Join(c.deps.DataDir, n))
		case strings.HasPrefix(n, damagedPrefix):
			damaged = append(damaged, filepath.Join(c.deps.DataDir, n))
		}
	}
	sort.Strings(retired)
	sort.Strings(damaged)
	c.mu.Lock()
	c.retired, c.damaged = retired, damaged
	c.mu.Unlock()
}

// noteMissingLocked records a configured vault whose file could not be
// opened: missing, or damaged when the file is there and refused — the
// one case a rebuild is offered for (APP.md §2.1). Caller holds the mutex.
func (c *Core) noteMissingLocked(path string, err error) {
	c.vault.missing = path
	c.vault.damaged = notAKeystore(err)
}

// notAKeystore is the format's verdict on a file: refused as malformed,
// rather than absent, unreadable or busy.
func notAKeystore(err error) bool {
	return errors.Is(err, format.ErrInvalid) || errors.Is(err, format.ErrTruncated)
}

// vaultPlace reports whether path is where the vault is, or was: the one
// place, the configured path, or the file that could not be opened.
func (c *Core) vaultPlace(path string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return samePath(path, c.defaultVaultPath()) || samePath(path, c.vault.path) || (c.vault.missing != "" && samePath(path, c.vault.missing))
}

// retire keeps whatever sits at path as a dated copy in the data folder —
// a copy, never a rename, so the vault's place is never empty — under a
// name taken with O_EXCL and a counter, so no copy is ever overwritten. A
// keystore is kept as a retired vault; the vault's own file that does not
// open as a damaged copy, for salvage (APP.md §2.1); anything else under a
// name that says so, which the status does not count. A file too large to
// be a keystore is refused.
func (c *Core) retire(path string, vaultPlace bool) (string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() || fi.Size() > maxKeystoreBytes {
		return "", fs.ErrInvalid
	}
	prefix, ext := retiredPrefix, ".eks"
	if ks, err := keystore.Open(path); err == nil {
		ks.Close()
	} else if !errors.Is(err, keystore.ErrBusy) {
		prefix, ext = otherPrefix, ".bin"
		if vaultPlace && notAKeystore(err) {
			prefix, ext = damagedPrefix, ".eks"
		}
	}
	stamp := c.now().Unix()
	for n := 0; n < 1000; n++ {
		dst := filepath.Join(c.deps.DataDir, fmt.Sprintf("%s%d-%03d%s", prefix, stamp, n, ext))
		err := copyFile(path, dst, maxKeystoreBytes)
		if err == nil {
			return dst, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			os.Remove(dst) // never a half copy under a retired name
			return "", err
		}
	}
	return "", errors.New("no free name for the retired copy")
}

// install makes a staged file the vault at dst (the one place, or a
// vault kept elsewhere): a file already at dst is retired first (a copy
// into the data folder), then the staged file — which sits in dst's
// directory — is renamed over dst, never empty for an instant. Up to
// the rename nothing has happened and an error says so; from the rename
// on the install has happened whatever follows: the settings are pointed
// at the file, a reopen that fails leaves the file named as missing for
// "try again", and nil is returned. The previous vault's handle is closed
// and a pending lock's close waited for first, since a rename cannot
// pass an open handle; a previous vault kept elsewhere stays where it
// is. Nothing is deleted by this.
func (c *Core) install(staged, dst, displayName string) error {
	c.lockWG.Wait()
	c.mu.Lock()
	if old := c.vault.ks; old != nil {
		c.vault.ks = nil
		old.Close()
	}
	c.mu.Unlock()
	var retired string
	if _, err := os.Stat(dst); err == nil {
		r, err := c.retire(dst, c.vaultPlace(dst))
		if err != nil {
			return installError("retiring the file at the vault's place", err)
		}
		retired = r
	} else if !errors.Is(err, fs.ErrNotExist) {
		// Something is there that cannot even be looked at: never overwrite it.
		return installError("the file at the vault's place", err)
	}
	if err := renameRetrying(staged, dst); err != nil {
		if retired != "" {
			os.Remove(retired) // nothing was replaced: no phantom copy
		}
		c.scanRetired()
		return installError("installing the vault", err)
	}
	c.scanRetired()
	// Committed. The settings name the file before anything else can fail.
	c.mu.Lock()
	c.settings.VaultPath, c.settings.DisplayName = c.overrideFor(dst), displayName
	file := c.settings
	c.mu.Unlock()
	if err := saveSettings(c.deps.DataDir, file); err != nil {
		c.log("install: settings: %v", err)
		c.mu.Lock()
		c.vault.warnings[CodeSettingsUnsaved] = true
		c.mu.Unlock()
		c.emit(EventVaultWarning, Warning{Code: CodeSettingsUnsaved})
	} else {
		c.mu.Lock()
		delete(c.vault.warnings, CodeSettingsUnsaved)
		c.mu.Unlock()
	}
	if err := c.openVaultFile(dst, displayName); err != nil {
		c.log("install: reopen %s: %v", dst, err)
		if !errors.Is(err, keystore.ErrBusy) {
			// The file is the vault; only the facts are missing. Say so
			// rather than keep the previous vault's facts standing.
			c.mu.Lock()
			c.dropFactsLocked(dst, displayName)
			c.noteMissingLocked(dst, err)
			c.mu.Unlock()
		}
	}
	return nil
}

// dropFactsLocked keeps a vault's path and name but forgets its facts:
// the state is None, with the file named as missing, until it opens.
// Caller holds the state mutex.
func (c *Core) dropFactsLocked(path, displayName string) {
	v := &c.vault
	v.state, v.path, v.displayName = StateNone, path, displayName
	v.vaultID, v.modifiedAt, v.rotationPending, v.slots, v.stale = [16]byte{}, 0, false, nil, nil
	v.broken = nil
	delete(v.warnings, CodeVaultStale)
	delete(v.warnings, CodeVaultTampered)
	c.bump()
}

// installError maps what an install can fail with to what the user can
// act on: a file in use (an antivirus or another process holding it) is
// "busy", a file that is not a keystore "invalid"; the rest is internal.
func installError(what string, err error) error {
	var errno syscall.Errno
	switch {
	case errors.Is(err, fs.ErrInvalid):
		return coded(CodeVaultInvalid)
	case errors.Is(err, fs.ErrPermission):
		return coded(CodeVaultBusy)
	case errors.As(err, &errno) && (errno == 32 || errno == 33): // ERROR_SHARING_VIOLATION, ERROR_LOCK_VIOLATION
		return coded(CodeVaultBusy)
	}
	return fmt.Errorf("%s: %w", what, err)
}

// renameRetrying renames with a few short retries: on Windows a freshly
// written file is often held for a moment by an antivirus scanner.
func renameRetrying(from, to string) error {
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		if err = os.Rename(from, to); err == nil {
			return nil
		}
		time.Sleep(time.Duration(50*(attempt+1)) * time.Millisecond)
	}
	return err
}

// importGateLocked is the state an import, a replacement or a verification
// may start from: no vault or a locked one, no ceremony, and — when a
// vault is kept — no open archive, whose saves would land in the wrong
// registry. Caller holds the state mutex.
func (c *Core) importGateLocked(needArchivesClosed bool) *Error {
	switch c.vault.state {
	case StateNone, StateLocked:
	case StateUnlocking:
		return coded(CodeCeremonyRunning)
	case StateReleasing:
		return coded(CodeReleasing)
	case StateBroken:
		return coded(CodeVaultBroken)
	case StateBusy:
		return coded(CodeVaultBusy)
	default:
		return coded(CodeVaultUnlocked)
	}
	if c.cer != nil {
		return coded(CodeCeremonyRunning)
	}
	if needArchivesClosed && c.vault.state != StateNone && len(c.archives) > 0 {
		return coded(CodeArchivesOpen)
	}
	return nil
}

// ImportFile makes the keystore file at path the vault kept here, once it
// has proved itself on a staged copy: a vault by unlocking with method,
// a backup by the recovery key and then the first way in of the setup
// ceremony (kind, label, entangle). Only then is the vault kept here
// retired (a dated copy) and the staged file installed; the ceremony ends
// Locked, since the install needs the handle closed. Over a vault kept
// here the call needs replace — every replacement, the "same vault,
// newer" one included, since both are plaintext claims — and no open
// archive.
func (c *Core) ImportFile(path, displayName string, method UnlockMethod, first EnrollOptions, replace bool) *Error {
	c.mu.Lock()
	if e := c.importGateLocked(true); e != nil {
		c.mu.Unlock()
		return e
	}
	configured := c.vault.state != StateNone
	c.mu.Unlock()
	if configured && !replace {
		return coded(CodeVaultExists)
	}
	if displayName == "" {
		displayName = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	if _, err := os.Stat(path); err != nil {
		return c.fileError("import", err)
	}
	staged, err := c.stage(path, stagingName(incomingPrefix))
	if err != nil {
		return c.fileError("import", err)
	}
	ks, err := keystore.Open(staged)
	if err != nil {
		os.Remove(staged)
		return c.fileError("import", err)
	}
	info := inspect(ks, path)
	ks.Close()
	if info.Kind == FileKindBackup {
		method = MethodRecovery
		if first.Kind != EnrollToken && first.Kind != EnrollPassword {
			os.Remove(staged)
			return coded(CodeParams)
		}
	}
	if (method == MethodToken || first.Kind == EnrollToken && info.Kind == FileKindBackup) && c.deps.Cards == nil {
		os.Remove(staged)
		return coded(CodeTokenNoService)
	}
	if method != MethodToken && method != MethodPassword && method != MethodRecovery {
		os.Remove(staged)
		return coded(CodeParams)
	}
	c.mu.Lock()
	if e := c.importGateLocked(true); e != nil {
		c.mu.Unlock()
		os.Remove(staged)
		return e
	}
	cer := c.newCeremonyLocked("import")
	cer.commits = true // installs at the vault's place: shutdown waits for it
	c.vault.state = StateUnlocking
	c.mu.Unlock()
	c.emitState()
	go cer.run(func(ctx context.Context) error {
		installed := false
		defer func() {
			if !installed {
				os.Remove(staged)
			}
		}()
		ks, err := cer.openVault(staged)
		if err != nil {
			return err
		}
		var unl *keystore.Unlocked
		if info.Kind == FileKindBackup {
			unl, err = cer.adoptBackup(ks, first)
		} else {
			unl, err = cer.prove(ks, method)
		}
		if err != nil {
			ks.Close()
			return err
		}
		tampered := unl.Tampered()
		unl.Close()
		ks.Close()
		if tampered != nil {
			return &parkAt{StepFailed, CodeVaultTampered}
		}
		if err := cer.check(); err != nil {
			return err
		}
		if err := c.install(staged, c.defaultVaultPath(), displayName); err != nil {
			return err
		}
		installed = true
		cer.set(func(s *CeremonyState) { s.Step = StepDone })
		return nil
	})
	return nil
}

// prove unlocks an incoming vault file with one of its own ways in: what
// makes a file the user's, rather than its plaintext.
func (cer *ceremony) prove(ks *keystore.Keystore, method UnlockMethod) (*keystore.Unlocked, error) {
	for {
		cred, hc, card, err := cer.credential(method, ks.Slots())
		if err != nil {
			return nil, err
		}
		unl, err := cer.unlockFile(ks, cred, hc)
		if hc != nil {
			hc.Token = nil
		}
		if err != nil && hc != nil && keyGone(err) {
			cer.unhold(card)
			card.Close()
			cer.awayNote(err)
			continue
		}
		cer.closeCard(card)
		if err == nil {
			cer.escrowOpenedKey(unl, cred)
		}
		return unl, err
	}
}

// adoptBackup opens an incoming backup with its recovery key and gives it
// its first way in; the file is a vault when it returns.
func (cer *ceremony) adoptBackup(ks *keystore.Keystore, first EnrollOptions) (*keystore.Unlocked, error) {
	cred, _, _, err := cer.credential(MethodRecovery, nil)
	if err != nil {
		return nil, err
	}
	unl, err := cer.unlockFile(ks, cred, nil)
	if err != nil {
		return nil, err
	}
	cer.escrowOpenedKey(unl, cred)
	spec, err := cer.firstSlotSpec(first)
	if err != nil {
		unl.Close()
		return nil, err
	}
	if err := cer.check(); err != nil {
		unl.Close()
		return nil, err
	}
	cer.set(func(s *CeremonyState) { s.Step = StepDeriving })
	if err := unl.AddSlot(spec); err != nil {
		unl.Close()
		return nil, err
	}
	return unl, nil
}

// firstSlotSpec collects the first way in of a new vault: the chosen
// secret first, then the key (APP.md §3, Vault). No card is held here.
func (cer *ceremony) firstSlotSpec(first EnrollOptions) (keystore.SlotSpec, error) {
	switch first.Kind {
	case EnrollPassword:
		pw, err := cer.askNew("password", StepPassword)
		if err != nil {
			return nil, err
		}
		return keystore.PasswordSlot{Password: pw, Argon2: defaultArgon2, Label: mustString(first.Label, "Password")}, nil
	case EnrollToken:
		hs := keystore.HardwareSlot{}
		if first.Entangle {
			pw, err := cer.askNew("password", StepPassword)
			if err != nil {
				return nil, err
			}
			hs.Password, hs.Argon2 = pw, defaultArgon2
		}
		pub, serial, err := cer.enrollToken(nil, first.Label) // nothing unlocked: no key to wait out
		if err != nil {
			return nil, err
		}
		hs.PublicKey, hs.Label = pub, mustString(first.Label, keyName(serial))
		return hs, nil
	}
	return nil, coded(CodeParams)
}

// FinishSetup gives a vault that has only its recovery slot — an adopted
// backup whose setup did not finish — its first way in: the recovery key,
// then the same ceremony as at creation, ending Unlocked.
func (c *Core) FinishSetup(first EnrollOptions) *Error {
	if first.Kind != EnrollToken && first.Kind != EnrollPassword {
		return coded(CodeParams)
	}
	if first.Kind == EnrollToken && c.deps.Cards == nil {
		return coded(CodeTokenNoService)
	}
	c.mu.Lock()
	if e := c.importGateLocked(false); e != nil {
		c.mu.Unlock()
		return e
	}
	if !c.setupNeededLocked() {
		none := c.vault.state == StateNone
		c.mu.Unlock()
		if none {
			return coded(CodeNoVault)
		}
		return coded(CodeParams)
	}
	path, name := c.vault.path, c.vault.displayName
	cer := c.newCeremonyLocked("setup")
	cer.mutation = true // AddSlot commits to the vault kept here: shutdown waits, an unknown outcome is Broken
	c.vault.state = StateUnlocking
	c.mu.Unlock()
	c.emitState()
	go cer.run(func(ctx context.Context) error {
		ks, err := cer.openVault(path)
		if err != nil {
			return err
		}
		unl, err := cer.adoptBackup(ks, first)
		if err != nil {
			ks.Close()
			return err
		}
		// The file is a vault from here on, whatever happens next.
		c.mu.Lock()
		if cer.latch || cer.ctx.Err() != nil {
			c.mu.Unlock()
			unl.Close()
			ks.Close()
			if err := c.openVaultFile(path, name); err != nil { // the facts, refreshed
				c.log("setup: refresh: %v", err)
			}
			return ErrTokenCancelled
		}
		c.publishUnlockedLocked(ks, unl) // finish() pays the owed receipts of a mutation
		c.mu.Unlock()
		cer.set(func(s *CeremonyState) { s.Step = StepDone })
		return nil
	})
	return nil
}

// VerifyBackup proves a backup opens — the recovery key unlocks a staged
// copy of it — and reports how many archives it names. Nothing is kept
// and nothing here changes (FORMAT §15: an untested backup is a belief).
func (c *Core) VerifyBackup(path string) *Error {
	if fi, err := os.Stat(path); err != nil {
		return c.fileError("verify", err)
	} else if !fi.Mode().IsRegular() || fi.Size() > maxKeystoreBytes {
		return coded(CodeVaultInvalid)
	}
	c.mu.Lock()
	if e := c.importGateLocked(false); e != nil {
		c.mu.Unlock()
		return e
	}
	cer := c.newCeremonyLocked("verify")
	c.vault.state = StateUnlocking
	c.mu.Unlock()
	c.emitState()
	go cer.run(func(ctx context.Context) error {
		staged, err := c.stage(path, c.inspectName())
		if err != nil {
			return c.fileError("verify", err)
		}
		defer os.Remove(staged)
		ks, err := cer.openVault(staged)
		if err != nil {
			return err
		}
		defer ks.Close()
		cred, _, _, err := cer.credential(MethodRecovery, nil)
		if err != nil {
			return err
		}
		unl, err := cer.unlockFile(ks, cred, nil)
		if err != nil {
			return err
		}
		defer unl.Close()
		if unl.Tampered() != nil {
			return &parkAt{StepFailed, CodeVaultTampered}
		}
		sess, err := unl.Session()
		if err != nil {
			return err
		}
		n := 0
		if g := sess.Registry(); g != nil {
			n = len(g.Archives)
		}
		sess.Lock()
		cer.set(func(s *CeremonyState) { s.Step, s.Archives = StepDone, n })
		return nil
	})
	return nil
}

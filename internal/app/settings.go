package app

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// lastExport is when this machine last wrote a backup, and of which vault
// (APP.md §13). It is local and unauthenticated: it words a confirmation
// and pre-selects a checkbox, and gates nothing.
type lastExport struct {
	VaultID string `json:"vaultId"`
	At      int64  `json:"at"`
}

// settingsFile is the machine-local part of Settings: nothing secret and
// nothing security-relevant (the timeouts live in the registry, R37).
type settingsFile struct {
	VaultPath   string `json:"vaultPath"`
	DisplayName string `json:"displayName"`
	// CloseAction is the close question's answer (APP.md §2.4, ruled
	// 2026-09-13): ask — the first close asks — tray, or quit. The older
	// closeToTray destroy/hide choice is gone: destroy is the behaviour,
	// and an old file's key is simply no longer read.
	CloseAction       string `json:"closeAction"`
	Theme             string `json:"theme"`
	Look              string `json:"look"`
	RecoveryRecordPct int    `json:"recoveryRecordPct"`
	DictionaryBelow   int64  `json:"dictionaryBelow"`
	// LastArchiveFolder is the folder the last archive was created in, so
	// that the next New archive dialog opens there (APP.md §6): a convenience
	// the page reads and never sets. The folder last extracted to is not kept
	// any more — an extract's destination is the page's own rule (§3, ruled
	// 2026-09-10).
	LastArchiveFolder string `json:"lastArchiveFolder,omitempty"`

	LastExport *lastExport `json:"lastExportAt,omitempty"`
}

func defaultSettings() settingsFile {
	return settingsFile{CloseAction: CloseAsk, Theme: "system", Look: "native", RecoveryRecordPct: 3, DictionaryBelow: 256 << 10}
}

// loadSettings reads the file; anything missing or unreadable is the
// default, silently — the file carries conveniences, not policy.
func loadSettings(dir string) settingsFile {
	s := defaultSettings()
	b, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		return s
	}
	var f settingsFile
	if json.Unmarshal(b, &f) != nil {
		return s
	}
	if f.VaultPath != "" {
		s.VaultPath = f.VaultPath
	}
	s.DisplayName = f.DisplayName
	if validCloseAction(f.CloseAction) {
		s.CloseAction = f.CloseAction
	}
	if f.Theme == "light" || f.Theme == "dark" {
		s.Theme = f.Theme
	}
	if f.RecoveryRecordPct >= 0 && f.RecoveryRecordPct <= 20 {
		s.RecoveryRecordPct = f.RecoveryRecordPct // 0: no recovery record (SCOPE.md: optional, 3% by default)
	}
	if f.DictionaryBelow >= 0 && f.DictionaryBelow <= 64<<20 {
		s.DictionaryBelow = f.DictionaryBelow
	}
	// Permissively, as a hint: whatever is there is offered to the dialog,
	// which copes with a folder that has gone.
	s.LastArchiveFolder = f.LastArchiveFolder
	// Permissively: a stamp is kept only when it names a vault id at all
	// and does not run backwards. Everything else about it is judged at
	// LastExportAt, against the vault actually kept.
	if f.LastExport != nil && f.LastExport.At >= 0 {
		if b, err := hex.DecodeString(f.LastExport.VaultID); err == nil && len(b) == 16 {
			le := *f.LastExport
			s.LastExport = &le
		}
	}
	return s
}

// validCloseAction reports whether v is one of the close question's three
// answers. Anything else — an older file's value, a page's typo — is not
// read and not stored; the default stands.
func validCloseAction(v string) bool {
	return v == CloseAsk || v == CloseTray || v == CloseQuit
}

// saveSettings writes temp-then-rename in the same directory.
func saveSettings(dir string, s settingsFile) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "settings.json.tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "settings.json"))
}

// Timeout policy (DESIGN §10, R37): defaults, and the clamps a reader
// applies to what the registry holds. There is no value that means off.
const (
	defaultIdle     = 10 * time.Minute
	defaultAbsolute = 60 * time.Minute
	maxIdle         = 30 * time.Minute
	maxAbsolute     = 8 * time.Hour
	minTimeout      = time.Minute
)

// timeouts clamps the registry's minutes; clamped reports whether a
// stored value was replaced.
func timeouts(idleMin, absMin uint16) (idle, abs time.Duration, clamped bool) {
	idle, abs = defaultIdle, defaultAbsolute
	if idleMin != 0 {
		d := time.Duration(idleMin) * time.Minute
		if d < minTimeout || d > maxIdle {
			clamped = true
		} else {
			idle = d
		}
	}
	if absMin != 0 {
		d := time.Duration(absMin) * time.Minute
		if d < minTimeout || d > maxAbsolute {
			clamped = true
		} else {
			abs = d
		}
	}
	if abs < idle {
		abs = idle
	}
	return idle, abs, clamped
}

// GetSettings returns the settings as the frontend edits them.
func (c *Core) GetSettings() Settings {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := Settings{
		VaultPath: c.settings.VaultPath, DisplayName: c.settings.DisplayName, CloseAction: c.settings.CloseAction,
		Theme: c.settings.Theme, Look: c.settings.Look, RecoveryRecordPct: c.settings.RecoveryRecordPct,
		DictionaryBelow: c.settings.DictionaryBelow, LastArchiveFolder: c.settings.LastArchiveFolder,
	}
	if c.vault.state == StateUnlocked && c.vault.sess != nil {
		g := c.vault.sess.Registry()
		if g != nil {
			s.IdleMinutes, s.AbsoluteMinutes = int(g.IdleMinutes), int(g.AbsoluteMinutes)
			s.TimeoutsFromVault, s.TimeoutsAdjustable = true, true
		}
	}
	return s
}

// SetCloseAction writes the close question's answer and nothing else
// (APP.md §2.4). Shell.CloseDecided remembers through this rather than
// through a whole-snapshot Set: a Get-mutate-Set from the shell takes the
// lock twice and writes every field, so a settings save the page has in
// flight — a theme, a timeout, staged and saved a moment before the user
// closed the window — would be overwritten by a snapshot taken before it,
// or would itself put the asking back. Here one field moves, over the
// settings as they are at this moment.
//
// Accepted, and small: a page save whose own snapshot carries the old
// closeAction and lands *after* this one still puts the asking back. The
// user would have had to be mid-save at the instant they answered the
// close question, and the answer is theirs to give again.
func (c *Core) SetCloseAction(action string) *Error {
	if !validCloseAction(action) {
		return coded(CodeParams)
	}
	c.mu.Lock()
	file := c.settings
	file.CloseAction = action
	c.mu.Unlock()
	// Commit, then apply (SetSettings says why).
	if err := saveSettings(c.deps.DataDir, file); err != nil {
		return c.fail("settings", err)
	}
	c.mu.Lock()
	c.settings.CloseAction = action
	c.mu.Unlock()
	return nil
}

// SetSettings stores the machine-local part and, while unlocked, the
// timeouts into the registry. Out-of-range timeouts are refused.
//
// The write commits before anything is applied: the new values are built
// on a copy, the file is written, and only a written file moves the
// settings in memory. A settings file that cannot be written is refused
// with the values in memory exactly as they were, so what the shell and
// the page read is never a setting that was not stored — the close
// question's gate reads CloseAction the moment the window closes, and a
// remembered answer that was refused must not be obeyed anyway.
func (c *Core) SetSettings(s Settings) *Error {
	if s.RecoveryRecordPct < 0 || s.RecoveryRecordPct > 20 {
		return coded(CodeParams)
	}
	if !validCloseAction(s.CloseAction) {
		return coded(CodeParams)
	}
	if s.Theme != "system" && s.Theme != "light" && s.Theme != "dark" {
		return coded(CodeParams)
	}
	if s.IdleMinutes < 0 || s.IdleMinutes > int(maxIdle/time.Minute) || s.AbsoluteMinutes < 0 || s.AbsoluteMinutes > int(maxAbsolute/time.Minute) {
		return coded(CodeParams)
	}
	c.mu.Lock()
	file := c.settings // a copy: nothing in memory moves until the file is written
	file.CloseAction, file.Theme, file.RecoveryRecordPct = s.CloseAction, s.Theme, s.RecoveryRecordPct
	if s.DictionaryBelow >= 0 && s.DictionaryBelow <= 64<<20 {
		file.DictionaryBelow = s.DictionaryBelow
	}
	if s.DisplayName != "" {
		file.DisplayName = s.DisplayName
	}
	sess := c.vault.sess
	unlocked := c.vault.state == StateUnlocked
	c.mu.Unlock()
	if err := saveSettings(c.deps.DataDir, file); err != nil {
		return c.fail("settings", err)
	}
	// Written: apply the fields this call owns, and only those, since the
	// copy above may have been overtaken — the export stamp and the last
	// archive folder are written from elsewhere and are nobody's to put
	// back here.
	c.mu.Lock()
	c.settings.CloseAction, c.settings.Theme, c.settings.RecoveryRecordPct = file.CloseAction, file.Theme, file.RecoveryRecordPct
	c.settings.DictionaryBelow, c.settings.DisplayName = file.DictionaryBelow, file.DisplayName
	c.mu.Unlock()
	changed := false
	if unlocked && sess != nil {
		if g := sess.Registry(); g != nil {
			changed = int(g.IdleMinutes) != s.IdleMinutes || int(g.AbsoluteMinutes) != s.AbsoluteMinutes
		}
	}
	if changed {
		err := c.updateRegistry(func(g *registry) error {
			g.IdleMinutes, g.AbsoluteMinutes = uint16(s.IdleMinutes), uint16(s.AbsoluteMinutes)
			return nil
		})
		if err != nil {
			return err
		}
		c.mu.Lock()
		c.armTimersLocked()
		c.mu.Unlock()
		c.emitState()
	}
	return nil
}

var errNoSettings = errors.New("no settings")

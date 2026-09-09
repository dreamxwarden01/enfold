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
	VaultPath         string `json:"vaultPath"`
	DisplayName       string `json:"displayName"`
	CloseToTray       string `json:"closeToTray"`
	Theme             string `json:"theme"`
	Look              string `json:"look"`
	RecoveryRecordPct int    `json:"recoveryRecordPct"`
	DictionaryBelow   int64  `json:"dictionaryBelow"`
	// LastArchiveFolder is the folder the last archive was created in, so
	// that the next New archive dialog opens there (APP.md §6), and
	// LastExtractFolder the folder last extracted to, which the extract
	// dialog's destination is prefilled with (§3). Conveniences the page
	// reads and never sets.
	LastArchiveFolder string `json:"lastArchiveFolder,omitempty"`
	LastExtractFolder string `json:"lastExtractFolder,omitempty"`

	LastExport *lastExport `json:"lastExportAt,omitempty"`
}

func defaultSettings() settingsFile {
	return settingsFile{CloseToTray: "destroy", Theme: "system", Look: "native", RecoveryRecordPct: 3, DictionaryBelow: 256 << 10}
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
	if f.CloseToTray == "hide" {
		s.CloseToTray = "hide"
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
	s.LastExtractFolder = f.LastExtractFolder
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
		VaultPath: c.settings.VaultPath, DisplayName: c.settings.DisplayName, CloseToTray: c.settings.CloseToTray,
		Theme: c.settings.Theme, Look: c.settings.Look, RecoveryRecordPct: c.settings.RecoveryRecordPct,
		DictionaryBelow: c.settings.DictionaryBelow, LastArchiveFolder: c.settings.LastArchiveFolder,
		LastExtractFolder: c.settings.LastExtractFolder,
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

// SetSettings stores the machine-local part and, while unlocked, the
// timeouts into the registry. Out-of-range timeouts are refused.
func (c *Core) SetSettings(s Settings) *Error {
	if s.RecoveryRecordPct < 0 || s.RecoveryRecordPct > 20 {
		return coded(CodeParams)
	}
	if s.CloseToTray != "destroy" && s.CloseToTray != "hide" {
		return coded(CodeParams)
	}
	if s.Theme != "system" && s.Theme != "light" && s.Theme != "dark" {
		return coded(CodeParams)
	}
	if s.IdleMinutes < 0 || s.IdleMinutes > int(maxIdle/time.Minute) || s.AbsoluteMinutes < 0 || s.AbsoluteMinutes > int(maxAbsolute/time.Minute) {
		return coded(CodeParams)
	}
	c.mu.Lock()
	c.settings.CloseToTray, c.settings.Theme, c.settings.RecoveryRecordPct = s.CloseToTray, s.Theme, s.RecoveryRecordPct
	if s.DictionaryBelow >= 0 && s.DictionaryBelow <= 64<<20 {
		c.settings.DictionaryBelow = s.DictionaryBelow
	}
	if s.DisplayName != "" {
		c.settings.DisplayName = s.DisplayName
	}
	file := c.settings
	sess := c.vault.sess
	unlocked := c.vault.state == StateUnlocked
	c.mu.Unlock()
	if err := saveSettings(c.deps.DataDir, file); err != nil {
		return c.fail("settings", err)
	}
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

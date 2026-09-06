// Package app is the application core of docs/APP.md: the session state
// machine, the unlock ceremony, open archives and their staged changes, the
// preview server, the settings and the registry — every decision and every
// secret — behind services the frontend binds and events it subscribes to.
// It has no notion of a window and does not import Wails or internal/piv;
// the shell (main_windows.go at the repository root) and one windows-tagged
// adapter (internal/app/pivcards) supply both.
package app

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/dreamxwarden01/enfold/internal/keystore"
)

// Clock is the core's time source; tests replace it.
type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, f func()) Timer
}

// Timer is what AfterFunc returns.
type Timer interface {
	Stop() bool
}

type realClock struct{}

func (realClock) Now() time.Time                            { return time.Now() }
func (realClock) AfterFunc(d time.Duration, f func()) Timer { return time.AfterFunc(d, f) }

// Emitter delivers events to the frontend. The shell's implementation
// calls Wails; tests record.
type Emitter interface {
	Emit(name string, payload any)
}

// InputSource corroborates the frontend's activity heartbeat with the
// session's real input (GetLastInputInfo on Windows). nil trusts the
// heartbeat, which only tests should do.
type InputSource interface {
	// LastInput is when the session last saw input, and whether that is
	// known.
	LastInput() (time.Time, bool)
}

// Logger receives the core's log lines. Nothing secret is ever logged.
type Logger func(format string, args ...any)

// Deps is what the shell supplies.
type Deps struct {
	Cards   Cards       // nil: hardware unlock unavailable
	Events  Emitter     // required
	Clock   Clock       // nil: the real clock
	Input   InputSource // nil: trust Activity
	Log     Logger      // nil: discard
	DataDir string      // %LOCALAPPDATA%\Enfold; settings.json lives here
}

// Core is the application state. One per process. Every method is safe
// for concurrent use; the state mutex is never held across long work.
type Core struct {
	deps Deps

	mu  sync.Mutex // the state mutex
	seq uint64     // bumped on every state change, stamped on every payload

	settings settingsFile
	vault    vaultState
	cer      *ceremony

	archives map[[16]byte]*openArchive
	ops      map[string]*op
	owed     map[[16]byte]owedReceipt
	preview  *previewServer

	// lockWG counts the unbounded halves of locks still running, so that
	// an open of the vault file waits for the handle they close.
	lockWG sync.WaitGroup

	closed bool
}

// New builds a core from its dependencies. It opens nothing and starts no
// goroutine: the shell calls Start after the application object exists
// (the single-instance guard runs inside application.New).
func New(d Deps) (*Core, error) {
	if d.Events == nil {
		return nil, fmt.Errorf("app: Events is required")
	}
	if d.Clock == nil {
		d.Clock = realClock{}
	}
	if d.Log == nil {
		d.Log = func(string, ...any) {}
	}
	if d.DataDir == "" {
		return nil, fmt.Errorf("app: DataDir is required")
	}
	c := &Core{deps: d, archives: map[[16]byte]*openArchive{}, ops: map[string]*op{}, owed: map[[16]byte]owedReceipt{}}
	c.vault.state = StateNone
	c.vault.warnings = map[Code]bool{}
	return c, nil
}

// Start loads the settings and, when a vault path is configured, reads
// its plaintext facts. It returns the port of the preview server, which
// binds now so that the shell can name it in the page's CSP.
func (c *Core) Start() (previewPort int, err error) {
	if err := os.MkdirAll(c.deps.DataDir, 0o700); err != nil {
		return 0, err
	}
	c.settings = loadSettings(c.deps.DataDir)
	c.preview, err = startPreview(c)
	if err != nil {
		return 0, err
	}
	if c.settings.VaultPath != "" {
		if err := c.openVaultFile(c.settings.VaultPath, c.settings.DisplayName); err != nil {
			c.log("start: vault %s: %v", c.settings.VaultPath, err)
		}
	}
	return c.preview.port, nil
}

// Close ends the process's use of the core: locks, closes archives after
// resolving them, stops the preview server. See ResolveForShutdown for the
// bounded, ordered version the shutdown hook uses.
func (c *Core) Close() {
	c.ResolveForShutdown(3 * time.Second)
	c.mu.Lock()
	c.closed = true
	p := c.preview
	c.mu.Unlock()
	if p != nil {
		p.stop()
	}
}

func (c *Core) log(format string, a ...any) { c.deps.Log(format, a...) }

// now is the clock, for the parts of the core that stamp times.
func (c *Core) now() time.Time { return c.deps.Clock.Now() }

// bump advances the sequence under the state mutex (caller holds it).
func (c *Core) bump() uint64 {
	c.seq++
	return c.seq
}

// emit delivers an event outside the state mutex.
func (c *Core) emit(name string, payload any) {
	c.deps.Events.Emit(name, payload)
}

// randomID is a 16-byte random id as hex.
func randomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func hexID(id [16]byte) string { return hex.EncodeToString(id[:]) }

func parseID(s string) ([16]byte, bool) {
	var id [16]byte
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 16 {
		return id, false
	}
	copy(id[:], b)
	return id, true
}

// settingsPath is the machine-local settings file.
func (c *Core) settingsPath() string { return filepath.Join(c.deps.DataDir, "settings.json") }

// slotView maps a keystore slot to its view.
func slotView(s keystore.SlotInfo) SlotView {
	v := SlotView{RecipientID: hexID(s.RecipientID), Label: s.Label, CreatedAt: s.CreatedAt, Entangled: s.EntangledPassword, Stale: s.Stale}
	switch {
	case s.PublicKey != nil:
		v.Type = "hardware"
	case s.Type == 3:
		v.Type = "recovery"
	default:
		v.Type = "password"
	}
	return v
}

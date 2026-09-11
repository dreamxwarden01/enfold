// Package app is the application core of docs/APP.md: the session state
// machine, the unlock ceremony, open archives and the operations that are
// each their own transaction (§2.3), the preview server, the settings and
// the registry — every decision and every secret — behind services the
// frontend binds and events it subscribes to.
// It has no notion of a window and does not import Wails or internal/piv;
// the shell (main_windows.go at the repository root) and one windows-tagged
// adapter (internal/app/pivcards) supply both.
package app

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// Volumes answers whether a path is on a local fixed volume of this machine
// — the only paths the presence pass probes (APP.md §13). nil uses the
// platform's own answer; the shell supplies one only where the platform has
// none, and tests use it to run the pass off a fixed disk.
type Volumes interface {
	LocalFixed(path string) bool
}

// Deps is what the shell supplies.
type Deps struct {
	Cards   Cards       // nil: hardware unlock unavailable
	Events  Emitter     // required
	Clock   Clock       // nil: the real clock
	Input   InputSource // nil: trust Activity
	Volumes Volumes     // nil: the platform's own answer
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
	// pending is the touch a cancelled ceremony left the key waiting for
	// (APP.md §2.2): the attempt goes on until the card answers.
	pending *attempt

	// reclaim is APP.md §2.3's rule for the in-place compaction the core
	// runs itself after a commit — 64 MiB of the tail given back, at least
	// a quarter of what it would have to move, and 64 MiB moved per commit.
	// It is held here rather than read from the constants so that a test
	// can lower it: a test that had to make a 64 MiB archive would prove
	// nothing more.
	reclaim reclaimRule
	// names is the `name` order of every listing (APP.md §3, sort.go): one
	// collator per core, built once, guarded by its own mutex.
	names *nameCollator
	// extractFS is where an extract touches the destination (extract.go);
	// the zero value is the platform's own, and a test sets a stand-in.
	extractFS extractFS

	archives map[[16]byte]*openArchive
	// opening are the archives an openArchiveFor is opening right now, each
	// with the channel it closes once the handle is installed or the open
	// has failed: a second opener waits on it and joins the handle, so that
	// the archive layer sees one Open per path (APP.md §2.3).
	opening map[[16]byte]chan struct{}
	// deleting are the records a Delete has claimed: it releases the state
	// mutex for the folder read and the removal, and nothing may open or
	// forget the record while it does (APP.md §13).
	deleting map[[16]byte]bool
	retired  []string // vault-replaced-*.eks in the data folder, oldest first
	damaged  []string // vault-damaged-*.eks in the data folder, oldest first: the vault's own file, refused, kept for salvage
	ops      map[string]*op
	owed     map[[16]byte]owedReceipt
	preview  *previewServer

	// lockWG counts the unbounded halves of locks still running, so that
	// an open of the vault file waits for the handle they close.
	lockWG sync.WaitGroup

	// presencePass: a file-presence pass is running. One at a time, which
	// is what bounds the probes it abandons (APP.md §13).
	presencePass bool
	// probeOut is the answer channel of a probe that went over its budget
	// and is still inside its syscall. It cannot be cancelled, so it holds
	// the one probe slot until it returns: no second probe is ever started
	// while it is out, in this pass or a later one (§9 Q16).
	probeOut chan error

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
	c := &Core{deps: d, archives: map[[16]byte]*openArchive{}, opening: map[[16]byte]chan struct{}{}, ops: map[string]*op{}, owed: map[[16]byte]owedReceipt{}}
	c.reclaim = reclaimRule{floor: reclaimFloor, share: reclaimShare, budget: reclaimBudget}
	c.names = newNameCollator()
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
	path := c.settings.VaultPath
	if path == "" {
		// The one place a vault lives (APP.md §2.1); a file put there by
		// hand is adopted.
		if _, err := os.Stat(c.defaultVaultPath()); err == nil {
			path = c.defaultVaultPath()
		}
	}
	c.sweepStaging()
	c.scanRetired()
	if path != "" {
		name := c.settings.DisplayName
		if name == "" {
			name = defaultDisplayName
		}
		if err := c.openVaultFile(path, name); err != nil {
			c.log("start: vault %s: %v", path, err)
			if !errors.Is(err, keystore.ErrBusy) {
				// Never "no vault yet": the screen names the file it could
				// not open, so nothing invites a second vault.
				c.mu.Lock()
				c.noteMissingLocked(path, err)
				c.mu.Unlock()
			}
		}
	}
	return c.preview.port, nil
}

// vaultFileName is the vault's name in the data folder: one vault per
// Windows user (APP.md §2.1).
const vaultFileName = "vault.eks"

// defaultDisplayName names a vault the user did not name.
const defaultDisplayName = "Personal vault"

// defaultVaultPath is where the vault lives unless kept elsewhere.
func (c *Core) defaultVaultPath() string { return filepath.Join(c.deps.DataDir, vaultFileName) }

// overrideFor is what settings.vaultPath holds for a vault at path: empty
// for the default place, the path otherwise.
func (c *Core) overrideFor(path string) string {
	if samePath(path, c.defaultVaultPath()) {
		return ""
	}
	return path
}

// samePath compares two paths the way the file system does here.
func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
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
	v := SlotView{RecipientID: hexID(s.RecipientID), Label: s.Label, CreatedAt: s.CreatedAt}
	switch {
	case s.PublicKey != nil:
		v.Type = "hardware"
	case s.Type == 3:
		v.Type = "recovery"
		v.RecoveryID = recoveryID(s.RecipientID)
	default:
		v.Type = "password"
	}
	return v
}

// recoveryID is the recovery key's ID (FORMAT.md §18.4): the first eight hex
// digits of the slot's recipient_id, upper-cased and grouped "3F7A-9C21".
// One derivation serves the view, the reveal and the saved text file, so the
// ID on the sheet is the ID on the screen.
func recoveryID(rid [16]byte) string {
	s := strings.ToUpper(hex.EncodeToString(rid[:4]))
	return s[:4] + "-" + s[4:]
}

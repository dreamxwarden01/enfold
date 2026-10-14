//go:build windows

package main

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/dreamxwarden01/enfold/internal/app"
	"github.com/dreamxwarden01/enfold/internal/brand"
)

// tray is the three-state tray icon of APP.md §2.4 with its menu and its
// tooltip. Both name the vault and say its state in one line (ruled
// 2026-10-14): the menu's first, greyed item, and the tooltip's start.
type tray struct {
	s     *shell
	t     *application.SystemTray
	head  *application.MenuItem
	open  *application.MenuItem
	lock  *application.MenuItem
	icons [3][2][]byte // state × {light, dark}

	mu     sync.Mutex
	state  brand.TrayState
	status app.VaultStatus
	idle   int // the idle timeout in minutes, 0 while not known
}

func newTray(s *shell) *tray {
	t := &tray{s: s, t: s.app.SystemTray.New()}
	for st := brand.TrayLocked; st <= brand.TrayUnlocked; st++ {
		t.icons[st][0] = brand.PNG(brand.Tray(32, st, false))
		t.icons[st][1] = brand.PNG(brand.Tray(32, st, true))
	}
	// Tray and menu callbacks run on the Wails main thread; anything that
	// waits on the window mutex (which other callers hold across main-
	// thread calls) or shows a dialog leaves that thread first.
	menu := s.app.Menu.New()
	// The header: which vault, and its state — never a command, so greyed.
	t.head = menu.Add(appName).SetEnabled(false)
	menu.AddSeparator()
	// Open, or Unlock… while the lock screen is what the window would show:
	// that one starts the ceremony as well (openOrUnlock).
	t.open = menu.Add("Open")
	t.open.OnClick(func(*application.Context) { go t.openOrUnlock() })
	t.lock = menu.Add("Lock now")
	t.lock.OnClick(func(*application.Context) { go s.core.Lock() })
	// No *Close all archives* since 2026-09-13 (APP.md §2.4): the window's
	// close already leaves every page, and while the window is up the page
	// closes archives — an item that changed the core under a page that did
	// not refresh was worse than none.
	menu.AddSeparator()
	menu.Add("Quit").OnClick(func(*application.Context) { go s.quit() })
	t.t.SetMenu(menu)
	t.t.OnClick(func() { go s.ensureWindow() })
	t.state = -1
	t.update(s.core.Status())
	t.t.Run()
	return t
}

// update applies a vault status: icon by state, the header and the menu's
// items, the tooltip. Nothing here ticks: the line says the idle rule or
// the clock time the session ends at, and both change only when the
// status does (APP.md §2.4, ruled 2026-10-14).
func (t *tray) update(st app.VaultStatus) {
	idle := 0
	if st.State == app.StateUnlocked && t.s.settings != nil {
		if set := t.s.settings(); set.TimeoutsFromVault {
			idle = idleMinutes(set.IdleMinutes)
		}
	}
	t.mu.Lock()
	t.status, t.idle = st, idle
	state := brand.TrayLocked
	switch {
	case st.State == app.StateUnlocked:
		state = brand.TrayUnlocked
	case st.OpenArchives > 0:
		state = brand.TrayLockedOpen
	}
	changed := state != t.state
	t.state = state
	t.mu.Unlock()
	if changed {
		// Windows' taskbar is dark in the dark theme: the light-ink icon is
		// the dark-mode one.
		t.t.SetIcon(t.icons[state][0])
		t.t.SetDarkModeIcon(t.icons[state][1])
	}
	// A menu label's "&" marks a mnemonic; a vault named "Q&A" keeps it.
	t.head.SetLabel(strings.ReplaceAll(vaultLine(st, idle), "&", "&&"))
	if unlocksFromTray(st) {
		t.open.SetLabel("Unlock…")
	} else {
		t.open.SetLabel("Open")
	}
	t.lock.SetEnabled(st.State == app.StateUnlocked)
	t.t.SetTooltip(tooltip(st, idle))
}

// openOrUnlock is the menu's first command. While the vault is locked and
// the window would show the lock screen, it is *Unlock…*: the ceremony is
// begun first, by the way the lock screen's own first button begins it —
// the YubiKey where the vault has one, the password otherwise — so the
// window opens on it already running, the explicit Unlock of the polish
// review's direction 1. Locked with archives still open, the window shows
// their page and its locked banner rather than the lock screen, and a
// ceremony begun under it would run unseen: there the item is *Open*, and
// the banner's own Unlock is one click away. A refusal (setup needed, the
// Smart Card service missing) is the lock screen's to say: the window
// opens all the same.
func (t *tray) openOrUnlock() {
	t.mu.Lock()
	st := t.status
	t.mu.Unlock()
	if unlocksFromTray(st) {
		method := app.MethodPassword
		if st.HasHardwareSlot {
			method = app.MethodToken
		}
		if e := t.s.core.BeginUnlock(method); e != nil && t.s.log != nil {
			t.s.log("tray: unlock not begun: %s", e.Code)
		}
	}
	t.s.ensureWindow()
}

// unlocksFromTray: the first item reads *Unlock…* and begins the ceremony.
func unlocksFromTray(st app.VaultStatus) bool {
	return st.State == app.StateLocked && st.OpenArchives == 0
}

// idleMinutes is the idle timeout the session runs on, from the registry's
// stored minutes: 0 is the default, and a value outside the permitted
// range is replaced by the default, as the core's reader replaces it
// (internal/app/settings.go timeouts: 10 minutes, 1 to 30).
func idleMinutes(stored int) int {
	if stored >= 1 && stored <= 30 {
		return stored
	}
	return 10
}

// vaultLine is the tray's one line about the vault — the menu's header
// and the tooltip's start: its name and its state, and while unlocked the
// deadline that is nearer, in the foot's words (APP.md §6): the idle rule
// — "Personal vault — Unlocked · locks after 10 min idle" — or, when the
// absolute cap comes first, the clock time it comes at — "· session ends
// 16:42". idle is 0 while the timeout is not known.
func vaultLine(st app.VaultStatus, idle int) string {
	name := st.DisplayName
	if name == "" {
		name = appName
	}
	switch st.State {
	case app.StateUnlocked:
		return name + " — Unlocked · " + deadline(st, idle)
	case app.StateUnlocking, app.StateReleasing:
		return name + " — Unlocking"
	case app.StateBroken:
		return name + " — Needs attention"
	case app.StateNone:
		return appName + " — No vault yet"
	}
	return name + " — Locked"
}

// deadline is the nearer of the two deadlines as words that never tick.
// Equal deadlines are the cap's: no input moves it.
func deadline(st app.VaultStatus, idle int) string {
	if st.AbsoluteAt > 0 && (st.LocksAt <= 0 || st.AbsoluteAt <= st.LocksAt) {
		return "session ends " + time.Unix(st.AbsoluteAt, 0).Format("15:04")
	}
	if idle <= 0 {
		return "locks when idle"
	}
	return fmt.Sprintf("locks after %d min idle", idle)
}

// tooltip is the vault's line with the open count and what is under way.
func tooltip(st app.VaultStatus, idle int) string {
	s := vaultLine(st, idle)
	switch st.OpenArchives {
	case 0:
	case 1:
		s += " · 1 archive open"
	default:
		s += fmt.Sprintf(" · %d archives open", st.OpenArchives)
	}
	// What is under way, rather than what is unsaved: an archive is clean
	// between operations since 2026-09-09 (APP.md §2.3).
	switch n := runningOps(st); n {
	case 0:
	case 1:
		s += " · 1 operation running"
	default:
		s += fmt.Sprintf(" · %d operations running", n)
	}
	return s
}

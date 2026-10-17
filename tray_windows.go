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
}

// eventShellUnlock is the tray's word to the page that an unlock was asked
// for there (APP.md §2.4, ruled 2026-10-17): the page shows its lock scene,
// over an open archive as the locked banner's own Unlock does, so that the
// ceremony the tray began is seen. No payload: the scene reads the
// ceremony from the status like any other.
const eventShellUnlock = "shell.unlock"

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
	// Open, or Unlock… while the vault is locked: that one starts the
	// ceremony as well (openOrUnlock).
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
	t.mu.Lock()
	t.status = st
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
	t.head.SetLabel(strings.ReplaceAll(vaultLine(st), "&", "&&"))
	if unlocksFromTray(st) {
		t.open.SetLabel("Unlock…")
	} else {
		t.open.SetLabel("Open")
	}
	t.lock.SetEnabled(st.State == app.StateUnlocked)
	t.t.SetTooltip(tooltip(st))
}

// openOrUnlock is the menu's first command. While the vault is locked it
// is *Unlock…*: the ceremony is begun first, by the way the lock screen's
// own first button begins it — the YubiKey where the vault has one, the
// password otherwise — then the window is shown and the page told to show
// its lock scene, which opens on the ceremony already running: the
// explicit Unlock of the polish review's direction 1. Locked with archives
// still open, the window would show their page and its locked banner, and
// a ceremony begun under it would run unseen; the page answers
// `shell.unlock` as the banner's own Unlock does, with the lock scene over
// the open archive (ruled 2026-10-17 — the item used to stay *Open* there).
// A refusal (setup needed, the Smart Card service missing) is the lock
// screen's to say: the window and the scene come all the same.
func (t *tray) openOrUnlock() {
	t.mu.Lock()
	st := t.status
	t.mu.Unlock()
	e := firstItem(st, t.s.core.BeginUnlock, t.s.ensureWindow, func() { t.s.app.Event.Emit(eventShellUnlock) })
	if e != nil && t.s.log != nil {
		t.s.log("tray: unlock not begun: %s", e.Code)
	}
}

// firstItem is openOrUnlock apart from the shell it acts on: begin the
// unlock where the item reads *Unlock…*, show the window, and then — the
// page being up to hear it — tell it to show the lock scene. It returns
// the refusal of the begin, if any.
func firstItem(st app.VaultStatus, begin func(app.UnlockMethod) *app.Error, show, tell func()) *app.Error {
	if !unlocksFromTray(st) {
		show()
		return nil
	}
	method := app.MethodPassword
	if st.HasHardwareSlot {
		method = app.MethodToken
	}
	e := begin(method)
	show()
	tell()
	return e
}

// unlocksFromTray: the first item reads *Unlock…* and begins the ceremony —
// in every locked state, with archives open or not (ruled 2026-10-17).
func unlocksFromTray(st app.VaultStatus) bool {
	return st.State == app.StateLocked
}

// vaultLine is the tray's one line about the vault — the menu's header
// and the tooltip's start: its name and its state, and while unlocked the
// deadline that is nearer, in the foot's words (APP.md §6): the idle rule
// — "Personal vault — Unlocked · locks after 10 min idle" — or, when the
// absolute cap comes first, the clock time it comes at — "· session ends
// 16:42". The idle minutes are the status's own, as the core applies them
// (IdleEffectiveMinutes, ruled 2026-10-17): the tray keeps no copy of the
// core's rule.
func vaultLine(st app.VaultStatus) string {
	name := st.DisplayName
	if name == "" {
		name = appName
	}
	switch st.State {
	case app.StateUnlocked:
		return name + " — Unlocked · " + deadline(st)
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
// Equal deadlines are the cap's: no input moves it. A status that names no
// idle timeout says no number.
func deadline(st app.VaultStatus) string {
	if st.AbsoluteAt > 0 && (st.LocksAt <= 0 || st.AbsoluteAt <= st.LocksAt) {
		return "session ends " + time.Unix(st.AbsoluteAt, 0).Format("15:04")
	}
	if st.IdleEffectiveMinutes <= 0 {
		return "locks when idle"
	}
	return fmt.Sprintf("locks after %d min idle", st.IdleEffectiveMinutes)
}

// tooltip is the vault's line with the open count and what is under way.
func tooltip(st app.VaultStatus) string {
	s := vaultLine(st)
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

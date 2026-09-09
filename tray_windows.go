//go:build windows

package main

import (
	"fmt"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/dreamxwarden01/enfold/internal/app"
	"github.com/dreamxwarden01/enfold/internal/brand"
)

// tray is the three-state tray icon of APP.md §2.4 with its menu and the
// countdown tooltip.
type tray struct {
	s     *shell
	t     *application.SystemTray
	lock  *application.MenuItem
	close *application.MenuItem
	icons [3][2][]byte // state × {light, dark}

	mu     sync.Mutex
	state  brand.TrayState
	status app.VaultStatus
	ticker *time.Ticker
	stop   chan struct{}
}

func newTray(s *shell) *tray {
	t := &tray{s: s, t: s.app.SystemTray.New(), stop: make(chan struct{})}
	for st := brand.TrayLocked; st <= brand.TrayUnlocked; st++ {
		t.icons[st][0] = brand.PNG(brand.Tray(32, st, false))
		t.icons[st][1] = brand.PNG(brand.Tray(32, st, true))
	}
	// Tray and menu callbacks run on the Wails main thread; anything that
	// waits on the window mutex (which other callers hold across main-
	// thread calls) or shows a dialog leaves that thread first.
	menu := s.app.Menu.New()
	menu.Add("Open").OnClick(func(*application.Context) { go s.ensureWindow() })
	t.lock = menu.Add("Lock now")
	t.lock.OnClick(func(*application.Context) { go s.core.Lock() })
	t.close = menu.Add("Close all archives")
	t.close.OnClick(func(*application.Context) {
		go func() {
			if kept := s.core.CloseAllArchives(); len(kept) > 0 {
				s.ensureWindow() // the page shows what would not close
			}
		}()
	})
	menu.AddSeparator()
	menu.Add("Quit").OnClick(func(*application.Context) { go s.quit() })
	t.t.SetMenu(menu)
	t.t.OnClick(func() { go s.ensureWindow() })
	t.state = -1
	t.update(s.core.Status())
	t.t.Run()
	go t.tick()
	return t
}

// update applies a vault status: icon by state, menu enablement, tooltip.
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
	t.lock.SetEnabled(st.State == app.StateUnlocked)
	t.close.SetEnabled(st.OpenArchives > 0)
	t.t.SetTooltip(t.tooltip())
}

// tooltip is "Enfold — locked" or the countdown with the open count.
func (t *tray) tooltip() string {
	t.mu.Lock()
	st := t.status
	t.mu.Unlock()
	var s string
	switch st.State {
	case app.StateUnlocked:
		left := time.Until(time.Unix(st.LocksAt, 0)).Round(time.Minute)
		if left < 0 {
			left = 0
		}
		s = fmt.Sprintf("%s — unlocked, locks in %s", appName, shortDuration(left))
	case app.StateUnlocking, app.StateReleasing:
		s = appName + " — unlocking"
	case app.StateBroken:
		s = appName + " — needs attention"
	default:
		s = appName + " — locked"
	}
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

func shortDuration(d time.Duration) string {
	m := int(d / time.Minute)
	switch {
	case m >= 60:
		return fmt.Sprintf("%dh %02dm", m/60, m%60)
	case m <= 1:
		return "1 min"
	}
	return fmt.Sprintf("%d min", m)
}

// tick refreshes the countdown while unlocked.
func (t *tray) tick() {
	t.ticker = time.NewTicker(30 * time.Second)
	defer t.ticker.Stop()
	for {
		select {
		case <-t.ticker.C:
			t.mu.Lock()
			unlocked := t.status.State == app.StateUnlocked
			t.mu.Unlock()
			if unlocked {
				t.t.SetTooltip(t.tooltip())
			}
		case <-t.stop:
			return
		}
	}
}

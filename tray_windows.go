//go:build windows

package main

import (
	"fmt"
	"math/bits"
	"strconv"
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
// While an operation runs, a second greyed line under it says which, on
// which archive and how far (direction 10 of the polish review), and the
// tooltip ends in the same words.
type tray struct {
	s     *shell
	t     *application.SystemTray
	head  *application.MenuItem
	run   *application.MenuItem // the running line; hidden while nothing runs
	open  *application.MenuItem
	lock  *application.MenuItem
	icons [3][2][]byte // state × {light, dark}

	mu     sync.Mutex
	state  brand.TrayState
	status app.VaultStatus
	// ended holds the operations a status or an op.done has shown
	// finished: a progress event is emitted after the core's lock is let
	// go, so one taken before an operation's end can land after the status
	// that ended it, and must not bring the line back (opEvent). Ids are
	// random and few per run, so the set is never pruned.
	ended map[string]bool
	// line and tip are what the running item and the tooltip last said, so
	// that a progress event that does not move the figure touches nothing.
	// refreshRunning's alone, so not under mu.
	line, tip string
	// kick wakes refreshRunning. The tooltip is set on the main thread and
	// waited for (InvokeSync), and progress is emitted from an operation's
	// own goroutine, which may hold its archive's lock: one goroutine of the
	// tray's own sets the line and the tooltip, and an operation never waits
	// on the main thread for it. One slot: a wake already pending covers
	// every change made before it is taken.
	kick chan struct{}
}

// eventShellUnlock is the tray's word to the page that an unlock was asked
// for there (APP.md §2.4, ruled 2026-10-17): the page shows its lock scene,
// over an open archive as the locked banner's own Unlock does, so that the
// ceremony the tray began is seen. No payload: the scene reads the
// ceremony from the status like any other.
const eventShellUnlock = "shell.unlock"

func newTray(s *shell) *tray {
	t := &tray{s: s, t: s.app.SystemTray.New(), ended: map[string]bool{}, kick: make(chan struct{}, 1)}
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
	// What runs, greyed for the same reason, and absent — removed from the
	// menu, never a blank line — while nothing does (direction 10).
	t.run = menu.Add("").SetEnabled(false).SetHidden(true)
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
	// After Run: the menu is built, and the line and the tooltip are set on
	// it rather than raced with its building. The first status's wake is
	// already waiting.
	go t.refreshRunning()
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
	t.mu.Lock()
	t.status = t.endedIn(st)
	t.mu.Unlock()
	t.wake()
}

// endedIn records the operations st has finished and returns st with every
// operation already seen to end marked so: a status built before an end
// can be delivered after it, and must not bring the running line back.
// The caller holds mu. Ops is copied only when something is marked.
func (t *tray) endedIn(st app.VaultStatus) app.VaultStatus {
	stale := false
	for _, o := range st.Ops {
		if o.Finished {
			t.ended[o.ID] = true
		} else if t.ended[o.ID] {
			stale = true
		}
	}
	if stale {
		ops := make([]app.OpView, len(st.Ops))
		for i, o := range st.Ops {
			o.Finished = o.Finished || t.ended[o.ID]
			ops[i] = o
		}
		st.Ops = ops
	}
	return st
}

// opEvent takes an operation's progress or its end into the status the
// tray holds, so that the running line's figure moves between vault
// statuses: the core emits a status when an operation ends, not while it
// runs, and not when it starts.
func (t *tray) opEvent(v app.OpView) {
	t.mu.Lock()
	if v.Finished {
		t.ended[v.ID] = true
	} else if t.ended[v.ID] {
		t.mu.Unlock()
		return // a progress event that lost the race with its own end
	}
	t.status = withOp(t.status, v)
	t.mu.Unlock()
	t.wake()
}

// wake asks refreshRunning for a pass, without waiting.
func (t *tray) wake() {
	select {
	case t.kick <- struct{}{}:
	default:
	}
}

// refreshRunning sets the running line and the tooltip from the status the
// tray holds, once per wake, and touches neither when its words have not
// changed. It runs for the life of the process.
func (t *tray) refreshRunning() {
	for range t.kick {
		t.mu.Lock()
		st := t.status
		t.mu.Unlock()
		t.applyRunning(st)
	}
}

// applyRunning is one pass of refreshRunning, on its goroutine only.
func (t *tray) applyRunning(st app.VaultStatus) {
	line, tip := runningLine(st), tooltip(st)
	lineChanged, tipChanged := line != t.line, tip != t.tip
	t.line, t.tip = line, tip
	if lineChanged {
		// The label first: a hidden item is put back with the label it has.
		if line != "" {
			t.run.SetLabel(strings.ReplaceAll(line, "&", "&&"))
		}
		t.run.SetHidden(line == "")
	}
	if tipChanged {
		t.t.SetTooltip(tip)
	}
}

// withOp is st with v in place of the operation of its id, or added when
// the status does not have it yet: an operation's start emits no status,
// so its first progress event is where the tray learns of it. The slice is
// copied, since the status is the emitter's payload, shared with the page;
// and an end is never undone by a progress event that arrives after it.
func withOp(st app.VaultStatus, v app.OpView) app.VaultStatus {
	ops := make([]app.OpView, 0, len(st.Ops)+1)
	found := false
	for _, o := range st.Ops {
		if o.ID == v.ID {
			found = true
			if !o.Finished || v.Finished {
				o = v
			}
		}
		ops = append(ops, o)
	}
	if !found {
		ops = append(ops, v)
	}
	st.Ops = ops
	return st
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

// runningLine is the tray's line about what runs (direction 10): the latest
// operation started, as "<verb> <archive> · 58%" ("Extracting from Photos
// 2024 · 58%"), with "+N more" when others run beside it, and "" when none
// does. The archive is named when the status has it among the opened ones
// (an operation holds its archive open while it runs, APP.md §2.3);
// otherwise the verb stands alone, in its bare form: "Extracting". The
// figure is done over total, floored so that 100% is only ever the end,
// and absent while the total is not known: a drag still hovering, an
// operation still planning.
func runningLine(st app.VaultStatus) string {
	var latest *app.OpView
	n := 0
	for i := range st.Ops {
		o := &st.Ops[i]
		if o.Finished {
			continue
		}
		n++
		// The latest started. StartedAt is in seconds, so a tie goes to the
		// greater id, which keeps the choice from flickering between two
		// statuses that list the same operations in another order.
		if latest == nil || o.StartedAt > latest.StartedAt || (o.StartedAt == latest.StartedAt && o.ID > latest.ID) {
			latest = o
		}
	}
	if latest == nil {
		return ""
	}
	verb, bare := opVerb(latest.Kind)
	s := bare
	for _, a := range st.Opened {
		if strings.EqualFold(a.ID, latest.ArchiveID) && a.Name != "" {
			s = verb + " " + a.Name
			break
		}
	}
	if latest.Total > 0 {
		pct := uint64(100)
		if latest.Done < latest.Total {
			// Exact at any size: done×100 in 128 bits, whose high word is
			// below total since done is, so the division cannot overflow.
			hi, lo := bits.Mul64(latest.Done, 100)
			pct, _ = bits.Div64(hi, lo, latest.Total)
		}
		s += " · " + strconv.FormatUint(pct, 10) + "%"
	}
	if n > 1 {
		s += fmt.Sprintf(" · +%d more", n-1)
	}
	return s
}

// opVerb is an operation kind's verb for the running line: with the
// archive's name after it, and alone. The kinds are OpView's: verify is the
// fingerprint's refresh, rotate the archive's re-key, and a drag out is
// said as one through its extraction too.
func opVerb(kind string) (withName, bare string) {
	switch kind {
	case "add":
		return "Adding to", "Adding"
	case "replace":
		return "Replacing in", "Replacing"
	case "extract":
		return "Extracting from", "Extracting"
	case "verify":
		return "Refreshing the fingerprint of", "Refreshing the fingerprint"
	case "compact":
		return "Compacting", "Compacting"
	case "reclaim":
		return "Reclaiming space in", "Reclaiming space"
	case "rotate":
		return "Re-keying", "Re-keying"
	case "dragout":
		return "Dragging out from", "Dragging out"
	}
	return "Working on", "Working"
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
	// between operations since 2026-09-09 (APP.md §2.3); in the running
	// line's own words (direction 10).
	if r := runningLine(st); r != "" {
		s += " · " + r
	}
	return s
}

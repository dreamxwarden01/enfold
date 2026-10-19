//go:build windows

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/dreamxwarden01/enfold/internal/app"
)

// The tray names the vault and its state in one line, the menu's header
// and the tooltip's start (APP.md §2.4, ruled 2026-10-14), in the foot's
// words: the idle rule, or the clock time the session ends at — nothing
// that ticks. The idle minutes are the status's (ruled 2026-10-17).
func TestTheTrayLineNamesTheVaultAndItsState(t *testing.T) {
	now := time.Now()
	unlocked := app.VaultStatus{
		State: app.StateUnlocked, DisplayName: "Personal vault",
		LocksAt: now.Add(10 * time.Minute).Unix(), AbsoluteAt: now.Add(time.Hour).Unix(),
		IdleEffectiveMinutes: 10, AbsoluteEffectiveMinutes: 60,
	}
	if got, want := vaultLine(unlocked), "Personal vault — Unlocked · locks after 10 min idle"; got != want {
		t.Errorf("unlocked: %q, want %q", got, want)
	}
	// The core's number, whatever it is: no rule of the tray's own between.
	chosen := unlocked
	chosen.IdleEffectiveMinutes = 25
	if got, want := vaultLine(chosen), "Personal vault — Unlocked · locks after 25 min idle"; got != want {
		t.Errorf("chosen: %q, want %q", got, want)
	}
	unknown := unlocked
	unknown.IdleEffectiveMinutes = 0
	if got, want := vaultLine(unknown), "Personal vault — Unlocked · locks when idle"; got != want {
		t.Errorf("idle not known: %q, want %q", got, want)
	}
	capped := unlocked
	capped.AbsoluteAt = now.Add(5 * time.Minute).Unix()
	if got, want := vaultLine(capped), "Personal vault — Unlocked · session ends "+time.Unix(capped.AbsoluteAt, 0).Format("15:04"); got != want {
		t.Errorf("cap nearer: %q, want %q", got, want)
	}
	locked := app.VaultStatus{State: app.StateLocked, DisplayName: "Personal vault"}
	if got, want := vaultLine(locked), "Personal vault — Locked"; got != want {
		t.Errorf("locked: %q, want %q", got, want)
	}
	if got, want := vaultLine(app.VaultStatus{State: app.StateLocked}), "Enfold — Locked"; got != want {
		t.Errorf("no name: %q, want %q", got, want)
	}
	// The tooltip ends in the running line's words (direction 10).
	busy := unlocked
	busy.OpenArchives = 1
	busy.Opened = []app.ArchiveRef{{ID: "a1", Name: "Photos 2024"}}
	busy.Ops = []app.OpView{{ID: "o1", Kind: "extract", ArchiveID: "a1", Done: 58, Total: 100}, {ID: "o0", Kind: "verify", Finished: true}}
	if got, want := tooltip(busy), "Personal vault — Unlocked · locks after 10 min idle · 1 archive open · Extracting from Photos 2024 · 58%"; got != want {
		t.Errorf("tooltip: %q, want %q", got, want)
	}
	idle := busy
	idle.Ops = []app.OpView{{ID: "o0", Kind: "verify", Finished: true}}
	if got, want := tooltip(idle), "Personal vault — Unlocked · locks after 10 min idle · 1 archive open"; got != want {
		t.Errorf("tooltip with nothing running: %q, want %q", got, want)
	}
}

// The tray's running line (direction 10): the latest operation started, its
// verb, its archive's name when the status has it open, and how far when
// the total is known; "+N more" beside others; nothing at all while nothing
// runs, which is what hides the menu item.
func TestTheTrayRunningLine(t *testing.T) {
	opened := []app.ArchiveRef{{ID: "a1", Name: "Photos 2024"}, {ID: "a2", Name: "Taxes"}}
	for _, c := range []struct {
		name string
		ops  []app.OpView
		want string
	}{
		{"nothing", nil, ""},
		{"only finished", []app.OpView{{ID: "x", Kind: "add", ArchiveID: "a1", Finished: true}}, ""},
		{"add", []app.OpView{{ID: "x", Kind: "add", ArchiveID: "a1", Done: 58, Total: 100}}, "Adding to Photos 2024 · 58%"},
		{"replace", []app.OpView{{ID: "x", Kind: "replace", ArchiveID: "a1", Done: 1, Total: 4}}, "Replacing in Photos 2024 · 25%"},
		{"extract", []app.OpView{{ID: "x", Kind: "extract", ArchiveID: "a1", Done: 2, Total: 3}}, "Extracting from Photos 2024 · 66%"},
		{"verify", []app.OpView{{ID: "x", Kind: "verify", ArchiveID: "a2", Done: 0, Total: 1}}, "Refreshing the fingerprint of Taxes · 0%"},
		{"compact", []app.OpView{{ID: "x", Kind: "compact", ArchiveID: "a2", Done: 9, Total: 10}}, "Compacting Taxes · 90%"},
		{"reclaim", []app.OpView{{ID: "x", Kind: "reclaim", ArchiveID: "a2", Done: 10, Total: 10}}, "Reclaiming space in Taxes · 100%"},
		{"rotate", []app.OpView{{ID: "x", Kind: "rotate", ArchiveID: "a2", Done: 1, Total: 2}}, "Re-keying Taxes · 50%"},
		{"dragout", []app.OpView{{ID: "x", Kind: "dragout", ArchiveID: "a1", Phase: "dragging"}}, "Dragging out from Photos 2024"},
		// No total yet: no figure. 99.99% is not 100%: that is only the end.
		{"planning", []app.OpView{{ID: "x", Kind: "add", ArchiveID: "a1"}}, "Adding to Photos 2024"},
		{"nearly", []app.OpView{{ID: "x", Kind: "add", ArchiveID: "a1", Done: 9999, Total: 10000}}, "Adding to Photos 2024 · 99%"},
		{"large", []app.OpView{{ID: "x", Kind: "add", ArchiveID: "a1", Done: 1 << 62, Total: 1<<63 + 1<<62}}, "Adding to Photos 2024 · 33%"},
		// An archive the status does not have open: the verb alone.
		{"not opened", []app.OpView{{ID: "x", Kind: "extract", ArchiveID: "zz", Done: 1, Total: 2}}, "Extracting · 50%"},
		{"no archive", []app.OpView{{ID: "x", Kind: "rotate", Done: 1, Total: 2}}, "Re-keying · 50%"},
		{"an id in capitals", []app.OpView{{ID: "x", Kind: "add", ArchiveID: "A1", Done: 1, Total: 2}}, "Adding to Photos 2024 · 50%"},
		{"unknown kind", []app.OpView{{ID: "x", Kind: "later", ArchiveID: "a1"}}, "Working on Photos 2024"},
		// Several: the latest started, the finished not counted, a tie in
		// the second to the greater id, whatever order the status lists.
		{"several", []app.OpView{
			{ID: "a", Kind: "extract", ArchiveID: "a1", StartedAt: 100, Done: 1, Total: 2},
			{ID: "b", Kind: "compact", ArchiveID: "a2", StartedAt: 200, Done: 3, Total: 4},
			{ID: "c", Kind: "add", ArchiveID: "a1", StartedAt: 300, Finished: true},
			{ID: "d", Kind: "add", ArchiveID: "a1", StartedAt: 50},
		}, "Compacting Taxes · 75% · +2 more"},
		{"a tie", []app.OpView{
			{ID: "b", Kind: "compact", ArchiveID: "a2", StartedAt: 200},
			{ID: "a", Kind: "extract", ArchiveID: "a1", StartedAt: 200},
		}, "Compacting Taxes · +1 more"},
		{"a tie, the other order", []app.OpView{
			{ID: "a", Kind: "extract", ArchiveID: "a1", StartedAt: 200},
			{ID: "b", Kind: "compact", ArchiveID: "a2", StartedAt: 200},
		}, "Compacting Taxes · +1 more"},
	} {
		st := app.VaultStatus{State: app.StateUnlocked, Opened: opened, OpenArchives: len(opened), Ops: c.ops}
		if got := runningLine(st); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// The tray learns of an operation from its progress, since its start emits
// no status, and follows it between statuses; an end that has been seen is
// never undone by a progress event that arrives after it. The status it is
// given is not written to: it is the payload the page is sent too.
func TestTheTrayFollowsAnOperation(t *testing.T) {
	st := app.VaultStatus{Opened: []app.ArchiveRef{{ID: "a1", Name: "Photos 2024"}}, Ops: []app.OpView{{ID: "o0", Kind: "verify", Finished: true}}}
	st = withOp(st, app.OpView{ID: "o1", Kind: "extract", ArchiveID: "a1", Done: 1, Total: 4})
	if got, want := runningLine(st), "Extracting from Photos 2024 · 25%"; got != want {
		t.Fatalf("learnt from progress: %q, want %q", got, want)
	}
	shared := st
	st = withOp(st, app.OpView{ID: "o1", Kind: "extract", ArchiveID: "a1", Done: 3, Total: 4})
	if got, want := runningLine(st), "Extracting from Photos 2024 · 75%"; got != want {
		t.Fatalf("moved: %q, want %q", got, want)
	}
	if got, want := runningLine(shared), "Extracting from Photos 2024 · 25%"; got != want || len(st.Ops) != 2 {
		t.Fatalf("the earlier status was written to (%q), or the op was added twice (%d)", got, len(st.Ops))
	}
	st = withOp(st, app.OpView{ID: "o1", Kind: "extract", ArchiveID: "a1", Done: 4, Total: 4, Finished: true})
	if got := runningLine(st); got != "" {
		t.Fatalf("ended: %q", got)
	}
	st = withOp(st, app.OpView{ID: "o1", Kind: "extract", ArchiveID: "a1", Done: 3, Total: 4})
	if got := runningLine(st); got != "" {
		t.Fatalf("a late progress event undid the end: %q", got)
	}
	// A status built before the end and delivered after it: the end the
	// tray has seen stands, and the status the page is sent is not touched.
	tr := &tray{ended: map[string]bool{}}
	tr.endedIn(st)
	late := app.VaultStatus{Opened: st.Opened, Ops: []app.OpView{{ID: "o1", Kind: "extract", ArchiveID: "a1", Done: 3, Total: 4}}}
	if got := runningLine(tr.endedIn(late)); got != "" {
		t.Fatalf("a late status undid the end: %q", got)
	}
	if late.Ops[0].Finished {
		t.Fatal("the late status itself was written to")
	}
	fresh := app.VaultStatus{Opened: st.Opened, Ops: []app.OpView{{ID: "o2", Kind: "add", ArchiveID: "a1"}}}
	if got, want := runningLine(tr.endedIn(fresh)), "Adding to Photos 2024"; got != want {
		t.Fatalf("another operation: %q, want %q", got, want)
	}
}

// Unlock… from any locked state (ruled 2026-10-17): the ceremony begun by
// the lock screen's first way, the window shown, and then the page told to
// show its lock scene — over the open archives too, where it would
// otherwise run unseen under their page. A refusal changes none of that:
// the lock screen says it. Anywhere else the item is Open: the window and
// nothing more.
func TestTheTrayUnlocksFromAnyLockedState(t *testing.T) {
	for _, c := range []struct {
		st   app.VaultStatus
		want string // the calls, in order
	}{
		{app.VaultStatus{State: app.StateLocked, HasPasswordSlot: true}, "begin password, show, tell"},
		{app.VaultStatus{State: app.StateLocked, HasHardwareSlot: true, HasPasswordSlot: true}, "begin token, show, tell"},
		{app.VaultStatus{State: app.StateLocked, HasHardwareSlot: true, OpenArchives: 1}, "begin token, show, tell"},
		{app.VaultStatus{State: app.StateLocked, HasPasswordSlot: true, OpenArchives: 3}, "begin password, show, tell"},
		{app.VaultStatus{State: app.StateUnlocked}, "show"},
		{app.VaultStatus{State: app.StateUnlocked, OpenArchives: 1}, "show"},
		{app.VaultStatus{State: app.StateUnlocking}, "show"},
		{app.VaultStatus{State: app.StateNone}, "show"},
		{app.VaultStatus{State: app.StateBusy}, "show"},
		{app.VaultStatus{State: app.StateBroken}, "show"},
	} {
		var calls []string
		e := firstItem(c.st,
			func(m app.UnlockMethod) *app.Error { calls = append(calls, "begin "+string(m)); return nil },
			func() { calls = append(calls, "show") },
			func() { calls = append(calls, "tell") })
		if got := strings.Join(calls, ", "); got != c.want || e != nil {
			t.Errorf("%s with %d open: %q (%v), want %q", c.st.State, c.st.OpenArchives, got, e, c.want)
		}
		if got, want := unlocksFromTray(c.st), strings.HasPrefix(c.want, "begin"); got != want {
			t.Errorf("%s with %d open: reads Unlock… %v, want %v", c.st.State, c.st.OpenArchives, got, want)
		}
	}
	// Setup needed, or the Smart Card service missing: refused, and the
	// window and the scene come all the same, for the lock screen to say so.
	var calls []string
	refused := &app.Error{Code: app.CodeSetupNeeded}
	e := firstItem(app.VaultStatus{State: app.StateLocked, OpenArchives: 1},
		func(app.UnlockMethod) *app.Error { calls = append(calls, "begin"); return refused },
		func() { calls = append(calls, "show") },
		func() { calls = append(calls, "tell") })
	if got := strings.Join(calls, ", "); got != "begin, show, tell" || e != refused {
		t.Errorf("refused: %q (%v), want the window and the scene, and the refusal returned", got, e)
	}
}

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
	busy := unlocked
	busy.OpenArchives = 1
	busy.Ops = []app.OpView{{Kind: "add"}, {Kind: "verify", Finished: true}}
	if got, want := tooltip(busy), "Personal vault — Unlocked · locks after 10 min idle · 1 archive open · 1 operation running"; got != want {
		t.Errorf("tooltip: %q, want %q", got, want)
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

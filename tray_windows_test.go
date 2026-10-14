//go:build windows

package main

import (
	"testing"
	"time"

	"github.com/dreamxwarden01/enfold/internal/app"
)

// The tray names the vault and its state in one line, the menu's header
// and the tooltip's start (APP.md §2.4, ruled 2026-10-14), in the foot's
// words: the idle rule, or the clock time the session ends at — nothing
// that ticks.
func TestTheTrayLineNamesTheVaultAndItsState(t *testing.T) {
	now := time.Now()
	unlocked := app.VaultStatus{
		State: app.StateUnlocked, DisplayName: "Personal vault",
		LocksAt: now.Add(10 * time.Minute).Unix(), AbsoluteAt: now.Add(time.Hour).Unix(),
	}
	if got, want := vaultLine(unlocked, 10), "Personal vault — Unlocked · locks after 10 min idle"; got != want {
		t.Errorf("unlocked: %q, want %q", got, want)
	}
	if got, want := vaultLine(unlocked, 0), "Personal vault — Unlocked · locks when idle"; got != want {
		t.Errorf("idle not known: %q, want %q", got, want)
	}
	capped := unlocked
	capped.AbsoluteAt = now.Add(5 * time.Minute).Unix()
	if got, want := vaultLine(capped, 10), "Personal vault — Unlocked · session ends "+time.Unix(capped.AbsoluteAt, 0).Format("15:04"); got != want {
		t.Errorf("cap nearer: %q, want %q", got, want)
	}
	locked := app.VaultStatus{State: app.StateLocked, DisplayName: "Personal vault"}
	if got, want := vaultLine(locked, 10), "Personal vault — Locked"; got != want {
		t.Errorf("locked: %q, want %q", got, want)
	}
	if got, want := vaultLine(app.VaultStatus{State: app.StateLocked}, 0), "Enfold — Locked"; got != want {
		t.Errorf("no name: %q, want %q", got, want)
	}
	busy := unlocked
	busy.OpenArchives = 1
	busy.Ops = []app.OpView{{Kind: "add"}, {Kind: "verify", Finished: true}}
	if got, want := tooltip(busy, 10), "Personal vault — Unlocked · locks after 10 min idle · 1 archive open · 1 operation running"; got != want {
		t.Errorf("tooltip: %q, want %q", got, want)
	}
}

// Unlock… only where the window would show the lock screen: locked with
// archives open, the window shows their page and its banner instead, and
// a ceremony begun under it would run unseen.
func TestTheTrayOffersUnlockOnlyOverTheLockScreen(t *testing.T) {
	for _, c := range []struct {
		st   app.VaultStatus
		want bool
	}{
		{app.VaultStatus{State: app.StateLocked}, true},
		{app.VaultStatus{State: app.StateLocked, OpenArchives: 1}, false},
		{app.VaultStatus{State: app.StateUnlocked}, false},
		{app.VaultStatus{State: app.StateUnlocking}, false},
		{app.VaultStatus{State: app.StateNone}, false},
		{app.VaultStatus{State: app.StateBusy}, false},
	} {
		if got := unlocksFromTray(c.st); got != c.want {
			t.Errorf("%s with %d open: %v, want %v", c.st.State, c.st.OpenArchives, got, c.want)
		}
	}
}

func TestTheTrayReadsTheIdleTimeoutAsTheCoreDoes(t *testing.T) {
	for stored, want := range map[int]int{0: 10, 1: 1, 5: 5, 30: 30, 31: 10, 45: 10} {
		if got := idleMinutes(stored); got != want {
			t.Errorf("idleMinutes(%d) = %d, want %d", stored, got, want)
		}
	}
}

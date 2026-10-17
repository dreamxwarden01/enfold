package app

import (
	"slices"
	"testing"
)

// The status carries the timeouts the session runs on, as timeouts()
// clamps them, so that the foot and the tray say the core's number and
// never re-derive its rule (APP.md §3, ruled 2026-10-17): nothing while
// locked, the defaults for an unset registry, a chosen value as chosen,
// and a stored value out of range as the clamp replaced it — not as it
// is stored.
func TestTheStatusCarriesTheEffectiveTimeouts(t *testing.T) {
	h := newHarness(t, nil, nil)
	if st := h.status(); st.IdleEffectiveMinutes != 0 || st.AbsoluteEffectiveMinutes != 0 {
		t.Fatalf("locked: %d/%d, want 0/0", st.IdleEffectiveMinutes, st.AbsoluteEffectiveMinutes)
	}
	h.unlockWithPassword()
	if st := h.status(); st.IdleEffectiveMinutes != 10 || st.AbsoluteEffectiveMinutes != 60 {
		t.Fatalf("defaults: %d/%d, want 10/60", st.IdleEffectiveMinutes, st.AbsoluteEffectiveMinutes)
	}
	s := h.c.GetSettings()
	s.IdleMinutes, s.AbsoluteMinutes = 5, 120
	if e := h.c.SetSettings(s); e != nil {
		t.Fatalf("set: %v", e)
	}
	if st := h.status(); st.IdleEffectiveMinutes != 5 || st.AbsoluteEffectiveMinutes != 120 {
		t.Fatalf("chosen: %d/%d, want 5/120", st.IdleEffectiveMinutes, st.AbsoluteEffectiveMinutes)
	}
	// A registry written by something other than SetSettings, which
	// refuses these: idle 45 is out of range and falls back to 10, and an
	// absolute cap of 5 below that idle is raised to it.
	if e := h.c.updateRegistry(func(g *registry) error {
		g.IdleMinutes, g.AbsoluteMinutes = 45, 5
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	h.c.Lock()
	h.rec.waitState(t, StateLocked)
	if st := h.status(); st.IdleEffectiveMinutes != 0 || st.AbsoluteEffectiveMinutes != 0 {
		t.Fatalf("locked again: %d/%d, want 0/0", st.IdleEffectiveMinutes, st.AbsoluteEffectiveMinutes)
	}
	h.unlockWithPassword()
	st := h.status()
	if st.IdleEffectiveMinutes != 10 || st.AbsoluteEffectiveMinutes != 10 {
		t.Fatalf("clamped: %d/%d, want 10/10", st.IdleEffectiveMinutes, st.AbsoluteEffectiveMinutes)
	}
	if !slices.Contains(st.Warnings, Code("vault.timeouts_clamped")) {
		t.Fatalf("no clamp warning: %v", st.Warnings)
	}
}

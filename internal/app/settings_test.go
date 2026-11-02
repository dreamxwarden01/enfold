package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestSettingsChangedFollowsAWrittenSave: SetSettings emits settings.changed
// once the file is written and the settings in memory have moved (APP.md
// §2.4) — the shell sets the window's caption from it, which Wails sets only
// when it makes the window. Every written save says so, an unchanged theme
// too: whether there is anything to repaint is the shell's to judge. What
// the event carries is the theme stored and nothing else, the vault's lock
// times least of all, which Settings carries while unlocked; and a save that
// is refused, by its values or by the file, says nothing.
func TestSettingsChangedFollowsAWrittenSave(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword() // so that Settings carries the registry's lock times
	since := func(mark int) []event {
		var out []event
		for _, e := range h.rec.snapshot()[mark:] {
			if e.name == EventSettingsChanged {
				out = append(out, e)
			}
		}
		return out
	}
	wire := func(e event) string {
		t.Helper()
		if _, ok := e.payload.(SettingsChanged); !ok {
			t.Fatalf("the payload is a %T, want a SettingsChanged", e.payload)
		}
		b, err := json.Marshal(e.payload)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	s := h.c.GetSettings()
	if !s.TimeoutsFromVault {
		t.Fatalf("a read while unlocked: %+v", s)
	}
	for _, theme := range []string{"dark", "light", "system"} {
		mark := len(h.rec.snapshot())
		s.Theme = theme
		if e := h.c.SetSettings(s); e != nil {
			t.Fatal(e)
		}
		got := since(mark)
		if len(got) != 1 {
			t.Fatalf("a save of %q emitted %d settings events, want one", theme, len(got))
		}
		if w, want := wire(got[0]), `{"theme":"`+theme+`"}`; w != want {
			t.Fatalf("a save of %q emitted %s, want %s", theme, w, want)
		}
	}

	// The theme as it was, another field moved: still one event.
	mark := len(h.rec.snapshot())
	s.PreviewPane = !s.PreviewPane
	if e := h.c.SetSettings(s); e != nil {
		t.Fatal(e)
	}
	if got := since(mark); len(got) != 1 || wire(got[0]) != `{"theme":"system"}` {
		t.Fatalf("a save that kept the theme emitted %d settings events, want one with the theme", len(got))
	}

	// Refused by its values: nothing was stored, so nothing is said.
	mark = len(h.rec.snapshot())
	bad := s
	bad.Theme = "sepia"
	if e := h.c.SetSettings(bad); !isCode(e, CodeParams) {
		t.Fatalf("a theme the core does not know: %v", e)
	}
	// Refused by the file: the seam TestSettingsRefuseLeaveMemoryAlone
	// uses, a directory where saveSettings writes its temporary file.
	blocked := filepath.Join(h.c.deps.DataDir, "settings.json.tmp")
	if err := os.MkdirAll(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	unwritten := s
	unwritten.Theme = "dark"
	if e := h.c.SetSettings(unwritten); e == nil {
		t.Fatal("a settings file that could not be written was not refused")
	}
	if got := since(mark); len(got) != 0 {
		t.Fatalf("refused saves emitted %d settings events, want none", len(got))
	}
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
}

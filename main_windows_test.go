//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// TestTheExitSweepsBeforeTheDragOutShutsDown is endDragOut's whole reason to
// exist (APP.md §3, ruled 2026-09-11): the sweep of the staging root runs
// while this run's own folders are still registered live and still
// manifested "live", and dragout.Shutdown — which is what takes them off
// that register — only after it. The other order would leave the hour's age
// limit as the one thing between the sweep and a drag whose files a target
// is reading at that moment.
func TestTheExitSweepsBeforeTheDragOutShutsDown(t *testing.T) {
	var order []string
	var got time.Duration
	endDragOut(func(budget time.Duration) {
		order = append(order, "sweep")
		got = budget
	}, dragSweepBudget, func() {
		order = append(order, "shutdown")
	})
	if len(order) != 2 || order[0] != "sweep" || order[1] != "shutdown" {
		t.Fatalf("the exit ran %v, want the sweep and then the drag out's shutdown", order)
	}
	if got != dragSweepBudget {
		t.Fatalf("the sweep was given %s, want the %s budget", got, dragSweepBudget)
	}
	// Housekeeping is a guest in the shutdown's budget, never the whole of
	// it: the archives and the lock are what it is for.
	if dragSweepBudget <= 0 || dragSweepBudget >= shutdownBudget {
		t.Fatalf("the sweep's budget is %s of a %s shutdown, want a bounded slice of it", dragSweepBudget, shutdownBudget)
	}
}

// TestTheStagingRootIsUnderTemp: the staging root is %TEMP%\Enfold\drag and
// nothing of it is under the data folder any more (ruled 2026-09-11). The
// user's own TMP or TEMP is what os.TempDir reads, so a redirection — a RAM
// disk, another volume — is honoured; the test proves that by moving it.
func TestTheStagingRootIsUnderTemp(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)
	root := dragStagingRoot()
	if want := filepath.Join(tmp, appName, "drag"); root != want {
		t.Fatalf("the staging root is %s, want %s — os.TempDir is GetTempPath, which reads TMP and TEMP", root, want)
	}
	// Nothing was created by asking: the root is made by the first drag,
	// and an old %LOCALAPPDATA%\Enfold\drag is not this program's to touch.
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("naming the staging root created it: %v", err)
	}
}

// TestTheCaptionFollowsTheSetting is the resolver the window's caption is
// made and kept in (APP.md §2.4): Light and Dark are themselves whatever
// Windows' own mode is, and Follow Windows ("system") is that mode — as is
// a value the core never stores, so that nothing unknown can pin the
// caption.
func TestTheCaptionFollowsTheSetting(t *testing.T) {
	for _, c := range []struct {
		theme  string
		osDark bool
		want   bool
	}{
		{"dark", false, true},
		{"dark", true, true},
		{"light", false, false},
		{"light", true, false},
		{"system", false, false},
		{"system", true, true},
		{"", true, true},
		{"sepia", false, false},
	} {
		if got := themeDark(c.theme, c.osDark); got != c.want {
			t.Errorf("themeDark(%q, Windows dark %v) = %v, want %v", c.theme, c.osDark, got, c.want)
		}
	}
	if captionOf(true) == captionOf(false) || captionOf(true) == captionUnknown || captionOf(false) == captionUnknown {
		t.Fatal("the caption's record does not tell dark, light and unknown apart")
	}
}

// TestTheGroundIsTheStylesheets: the window's BackgroundColour and its
// caption's colour in each theme are the painted ground's first row, the
// stylesheet's --ground-paint (APP.md §2.4, §6 Controls), so that nothing
// shows another colour before the page paints, where a resize uncovers the
// window, or where the caption meets the page's top row; a step that
// changes one changes both. Dark is declared twice, for the System
// theme's dark and for Dark chosen.
func TestTheGroundIsTheStylesheets(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("frontend", "src", "app.css"))
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	for _, dark := range []bool{false, true} {
		g := groundOf(dark)
		if g.Alpha != 255 {
			t.Errorf("the ground (dark %v) has alpha %d, want opaque", dark, g.Alpha)
		}
		decl := fmt.Sprintf("--ground-paint: #%02x%02x%02x;", g.Red, g.Green, g.Blue)
		want := 1
		if dark {
			want = 2
		}
		if n := strings.Count(css, decl); n < want {
			t.Errorf("app.css declares %q %d times, want %d at least (dark %v)", decl, n, want, dark)
		}
	}
	if groundOf(true) == groundOf(false) {
		t.Fatal("one ground for both themes")
	}
}

// TestColorref: a colour reaches DWM as a COLORREF, 0x00BBGGRR (the RGB
// macro, wingdi.h), as Wails' own NewRGBPtr builds it. The grounds read
// the same either way round, so a channel order swapped would not show in
// them; colours that are not palindromes prove the order.
func TestColorref(t *testing.T) {
	for _, c := range []application.RGBA{
		application.NewRGB(0x12, 0x34, 0x56),
		application.NewRGB(0xFF, 0x00, 0x00),
		application.NewRGB(0x00, 0x00, 0xFF),
		application.NewRGBA(0x01, 0x02, 0x03, 0x80),
	} {
		if got, want := colorref(c), *application.NewRGBPtr(c.Red, c.Green, c.Blue); got != want {
			t.Errorf("colorref(%+v) = %#08x, want %#08x", c, got, want)
		}
	}
	if got := colorref(application.NewRGB(0x12, 0x34, 0x56)); got != 0x563412 {
		t.Errorf("colorref(#123456) = %#08x, want 0x563412", got)
	}
	if got := colorref(groundOf(false)); got != 0xD5D3D5 {
		t.Errorf("the light ground's COLORREF is %#08x, want 0xd5d3d5", got)
	}
	if got := colorref(groundOf(true)); got != 0x262526 {
		t.Errorf("the dark ground's COLORREF is %#08x, want 0x262526", got)
	}
}

// TestTheCaptionColourRule is the caption's colour (APP.md §2.4): the
// ground's first row in the theme, on Windows 11 (build 22000 and later)
// with no contrast theme and no accent title bars; DWM's own everywhere
// else — on Windows 10, which ignores the attribute; under a contrast
// theme, whose caption is the system's; and where the user has asked for
// the accent colour on title bars (ColorPrevalence 1).
func TestTheCaptionColourRule(t *testing.T) {
	if dwmColourDefault != 0xFFFFFFFF {
		t.Fatalf("DWMWA_COLOR_DEFAULT is %#x, want 0xffffffff (dwmapi.h)", dwmColourDefault)
	}
	if captionColourBuild != 22000 {
		t.Fatalf("the caption's colour starts at build %d, want 22000, Windows 11's first", captionColourBuild)
	}
	for _, dark := range []bool{false, true} {
		ground := colorref(groundOf(dark))
		for _, c := range []struct {
			paints, contrast bool
			prevalence       uint64
			want             uint32
		}{
			{true, false, 0, ground},
			{false, false, 0, dwmColourDefault}, // Windows 10
			{true, true, 0, dwmColourDefault},   // a contrast theme
			{true, false, 1, dwmColourDefault},  // accent title bars
			{false, true, 1, dwmColourDefault},
			{false, false, 1, dwmColourDefault},
			{true, true, 1, dwmColourDefault},
		} {
			if got := captionColour(dark, c.paints, c.contrast, c.prevalence); got != c.want {
				t.Errorf("captionColour(dark %v, build 22000+ %v, contrast %v, ColorPrevalence %d) = %#08x, want %#08x",
					dark, c.paints, c.contrast, c.prevalence, got, c.want)
			}
		}
	}
	if captionColour(false, true, false, 0) == captionColour(true, true, false, 0) {
		t.Fatal("one caption colour for both themes")
	}
}

// TestTheWindowIsMadeWithItsCaption: the window's CustomTheme carries the
// caption's colour as the rule gives it, each theme's entries that theme's
// ground and the same active and inactive (Wails picks between them by
// the foreground once, as it makes the window), with no border and no
// caption text colour, those being DWM's; where the rule gives DWM's own
// colour there is no entry at all, and Wails sets nothing.
func TestTheWindowIsMadeWithItsCaption(t *testing.T) {
	s := customCaption(true, false, 0)
	for _, e := range []struct {
		name  string
		entry *application.WindowTheme
		dark  bool
	}{
		{"LightModeActive", s.LightModeActive, false},
		{"LightModeInactive", s.LightModeInactive, false},
		{"DarkModeActive", s.DarkModeActive, true},
		{"DarkModeInactive", s.DarkModeInactive, true},
	} {
		if e.entry == nil || e.entry.TitleBarColour == nil {
			t.Errorf("%s carries no caption colour", e.name)
			continue
		}
		if got, want := *e.entry.TitleBarColour, colorref(groundOf(e.dark)); got != want {
			t.Errorf("%s's caption is %#08x, want the ground's %#08x", e.name, got, want)
		}
		if e.entry.BorderColour != nil || e.entry.TitleTextColour != nil {
			t.Errorf("%s sets the border or the caption's text, which are DWM's", e.name)
		}
	}
	if s.DarkModeMenuBar != nil || s.LightModeMenuBar != nil {
		t.Error("the CustomTheme sets a menu bar, which the window has none of")
	}
	for _, c := range []struct {
		name             string
		paints, contrast bool
		prevalence       uint64
	}{
		{"Windows 10", false, false, 0},
		{"a contrast theme", true, true, 0},
		{"accent title bars", true, false, 1},
	} {
		s := customCaption(c.paints, c.contrast, c.prevalence)
		if s.LightModeActive != nil || s.LightModeInactive != nil || s.DarkModeActive != nil || s.DarkModeInactive != nil {
			t.Errorf("under %s the window is made with a caption colour", c.name)
		}
	}
}

// TestTheWindowIsNeverMadeSystemDefault guards the rule of APP.md §2.4: the
// window is made in the resolved Light or Dark, since a SystemDefault window
// carries Wails' own listener, which puts the caption back on the system's
// theme at every flip, over the user's explicit choice. The source is the
// evidence.
func TestTheWindowIsNeverMadeSystemDefault(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "application.SystemDefault") {
			t.Errorf("%s mentions application.SystemDefault", f)
		}
	}
}

// TestWindowsModeIsReadWithoutTheApp guards the first window of a launch
// (APP.md §2.4): it is made before the application runs, and Wails'
// Env.IsDarkMode answers false until then (beta.20,
// environment_manager.go 36-41), which made the first window light on a
// dark Windows under Follow Windows. Windows' own mode is read with
// w32.IsCurrentlyDarkMode, the registry read Env.IsDarkMode makes once the
// application runs. The source is the evidence.
func TestWindowsModeIsReadWithoutTheApp(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "Env.IsDarkMode(") {
			t.Errorf("%s calls Env.IsDarkMode, which answers false before the application runs", f)
		}
	}
}

// TestTheCaptionWaitsForThePage is the rule captionState holds the shell
// to (APP.md §2.4): no window is touched before its page has loaded, since
// Wails pumps the main thread's messages while WebView2 is being created
// and a background colour set from there can stop the main thread or end
// the process; a window's first load is when one made in a theme that has
// moved since is caught up, its theme and its caption's colour both; a
// reload keeps what the shell has set since; and a window made again from
// the tray is a new window, untouched until its own load.
func TestTheCaptionWaitsForThePage(t *testing.T) {
	light := captionLook{theme: captionLight, colour: captionColour(false, true, false, 0)}
	dark := captionLook{theme: captionDark, colour: captionColour(true, true, false, 0)}
	touched := func(c *captionState, id uint, want captionLook) (bool, bool) {
		return c.touch(id, want, false)
	}
	var c captionState
	for _, want := range []captionLook{light, dark} {
		if th, co := touched(&c, 1, want); th || co {
			t.Fatalf("a window whose page has not loaded is touched (want %+v)", want)
		}
	}

	// Window 1 loads, made light.
	c.loaded(1, light)
	if th, co := touched(&c, 1, light); th || co {
		t.Error("a window that wears the caption already is touched")
	}
	if th, co := touched(&c, 1, dark); !th || !co {
		t.Error("a loaded window made light, now resolving dark, is not caught up in theme and colour")
	}
	if th, co := touched(&c, 0, dark); th || co {
		t.Error("no window (id 0) is touched")
	}

	// The shell sets it dark; a reload says light again, as it was made.
	c.applied(true, true, dark)
	c.loaded(1, light)
	if c.wears != dark {
		t.Error("a reload put back the caption the window was made with")
	}
	if th, co := touched(&c, 1, dark); th || co {
		t.Error("a reload made the shell repaint a window that wears the caption")
	}

	// Closed to the tray and made again: window 2, made dark.
	if th, co := touched(&c, 2, light); th || co {
		t.Error("the new window is touched before its page has loaded")
	}
	c.loaded(2, dark)
	if th, co := touched(&c, 1, light); th || co {
		t.Error("the old window is still touched")
	}
	if th, co := touched(&c, 2, dark); th || co {
		t.Error("the new window does not wear the caption it was made with")
	}
	if th, co := touched(&c, 2, light); !th || !co {
		t.Error("the new window is not repainted for a flip")
	}

	// Made under a contrast theme, Wails sets no theme and no colour:
	// whatever resolves is applied at the load once that theme has gone.
	c.loaded(3, captionLook{theme: captionUnknown, colour: dwmColourDefault})
	for _, want := range []captionLook{light, dark} {
		if th, co := touched(&c, 3, want); !th || !co {
			t.Errorf("a window made under a contrast theme is not given %+v at its load", want)
		}
	}
}

// TestTheCaptionUnderAContrastTheme: under a contrast theme the shell
// never sets the caption's theme, and a caption it painted before the
// contrast theme began is given back to DWM; a caption DWM draws already
// is left alone; when the contrast theme ends, the theme and the ground
// come back. Accent title bars take the colour back the same way, the
// theme still the shell's.
func TestTheCaptionUnderAContrastTheme(t *testing.T) {
	light := captionLook{theme: captionLight, colour: captionColour(false, true, false, 0)}
	var c captionState
	c.loaded(1, light)

	// A contrast theme begins over the painted light caption.
	hc := captionLook{theme: captionLight, colour: captionColour(false, true, true, 0)}
	th, co := c.touch(1, hc, true)
	if th || !co {
		t.Fatalf("a contrast theme began: theme %v colour %v, want the colour alone, reset", th, co)
	}
	c.applied(th, co, hc)
	if c.wears.colour != dwmColourDefault || c.wears.theme != captionLight {
		t.Fatalf("under the contrast theme the caption wears %+v, want light with DWM's own colour", c.wears)
	}

	// The setting flips to dark under it: nothing is touched.
	hcDark := captionLook{theme: captionDark, colour: captionColour(true, true, true, 0)}
	if th, co := c.touch(1, hcDark, true); th || co {
		t.Errorf("under a contrast theme a flip set theme %v colour %v, want nothing", th, co)
	}

	// The contrast theme ends: dark and its ground.
	dark := captionLook{theme: captionDark, colour: captionColour(true, true, false, 0)}
	if th, co := c.touch(1, dark, false); !th || !co {
		t.Errorf("after the contrast theme the caption is caught up in theme %v colour %v, want both", th, co)
	}

	// Accent title bars turned on over a painted caption: the colour
	// alone goes back to DWM, and turned off again it comes back.
	c.loaded(2, light)
	accent := captionLook{theme: captionLight, colour: captionColour(false, true, false, 1)}
	th, co = c.touch(2, accent, false)
	if th || !co {
		t.Fatalf("accent title bars: theme %v colour %v, want the colour alone", th, co)
	}
	c.applied(th, co, accent)
	if th, co := c.touch(2, light, false); th || !co {
		t.Errorf("accent title bars off: theme %v colour %v, want the colour alone", th, co)
	}
}

// TestTheCaptionHearsWindowsColours: the messages that re-read the
// caption's colour (colourMessage) are the ones Windows sends when its
// colours may have changed, by their numbers in winuser.h, and no
// everyday message is among them.
func TestTheCaptionHearsWindowsColours(t *testing.T) {
	for _, m := range []struct {
		name string
		msg  uint32
		want bool
	}{
		{"WM_SETTINGCHANGE", 0x001A, true},
		{"WM_SYSCOLORCHANGE", 0x0015, true},
		{"WM_DWMCOLORIZATIONCOLORCHANGED", 0x0320, true},
		{"WM_PAINT", 0x000F, false},
		{"WM_SIZE", 0x0005, false},
		{"WM_ACTIVATE", 0x0006, false},
		{"WM_THEMECHANGED", 0x031A, false},
	} {
		if got := coloursChanged(m.msg); got != m.want {
			t.Errorf("coloursChanged(%s) = %v, want %v", m.name, got, m.want)
		}
	}
	s := &shell{}
	if code, handled := s.colourMessage(0, 0x000F, 0, 0); code != 0 || handled {
		t.Error("the interceptor handled a message itself; every message goes on to Wails")
	}
}

//go:build windows

package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/dreamxwarden01/enfold/internal/app"
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

// TestTheTouchIsTheStylesheets: the caption's colour while the touch's
// green is up is the touch's plain top in each theme, the stylesheet's
// --touch-top (APP.md §2.4, §6 Lock screen), and its transparent end is
// the same colour, so that the caption and the wash's first row meet with
// no line; a step that changes one changes both. Dark is declared twice,
// for the System theme's dark and for Dark chosen. The wash paints it
// over its gradient, held for the first px and gone 96 px down.
func TestTheTouchIsTheStylesheets(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("frontend", "src", "app.css"))
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	for _, dark := range []bool{false, true} {
		g := touchOf(dark)
		if g.Alpha != 255 {
			t.Errorf("the touch's top (dark %v) has alpha %d, want opaque", dark, g.Alpha)
		}
		want := 1
		if dark {
			want = 2
		}
		for _, decl := range []string{
			fmt.Sprintf("--touch-top: #%02x%02x%02x;", g.Red, g.Green, g.Blue),
			fmt.Sprintf("--touch-top-0: rgba(%d, %d, %d, 0);", g.Red, g.Green, g.Blue),
		} {
			if n := strings.Count(css, decl); n < want {
				t.Errorf("app.css declares %q %d times, want %d at least (dark %v)", decl, n, want, dark)
			}
		}
		if touchOf(dark) == groundOf(dark) {
			t.Errorf("the touch's top is the ground's (dark %v)", dark)
		}
	}
	if touchOf(true) == touchOf(false) {
		t.Fatal("one touch top for both themes")
	}
	if !strings.Contains(css, "linear-gradient(var(--touch-top) 1px, var(--touch-top-0) 96px) 0 0 / 100% 100% no-repeat,") {
		t.Error("the wash does not paint its plain top, held for its first px and gone 96 px down")
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
	if got := colorref(touchOf(false)); got != 0x5E690D {
		t.Errorf("the light touch's COLORREF is %#08x, want 0x5e690d", got)
	}
	if got := colorref(touchOf(true)); got != 0x434B0B {
		t.Errorf("the dark touch's COLORREF is %#08x, want 0x434b0b", got)
	}
}

// TestTheCaptionColourRule is the caption's colour (APP.md §2.4): the
// ground's first row in the theme, or the touch's plain top while the
// touch's green is up, on Windows 11 (build 22000 and later) with no
// contrast theme and no accent title bars; DWM's own everywhere else, the
// green or not — on Windows 10, which ignores the attribute; under a
// contrast theme, whose caption is the system's; and where the user has
// asked for the accent colour on title bars (ColorPrevalence 1).
func TestTheCaptionColourRule(t *testing.T) {
	if dwmColourDefault != 0xFFFFFFFF {
		t.Fatalf("DWMWA_COLOR_DEFAULT is %#x, want 0xffffffff (dwmapi.h)", dwmColourDefault)
	}
	if captionColourBuild != 22000 {
		t.Fatalf("the caption's colour starts at build %d, want 22000, Windows 11's first", captionColourBuild)
	}
	for _, dark := range []bool{false, true} {
		for _, green := range []bool{false, true} {
			painted := colorref(groundOf(dark))
			if green {
				painted = colorref(touchOf(dark))
			}
			for _, c := range []struct {
				paints, contrast bool
				prevalence       uint64
				want             uint32
			}{
				{true, false, 0, painted},
				{false, false, 0, dwmColourDefault}, // Windows 10
				{true, true, 0, dwmColourDefault},   // a contrast theme
				{true, false, 1, dwmColourDefault},  // accent title bars
				{false, true, 1, dwmColourDefault},
				{false, false, 1, dwmColourDefault},
				{true, true, 1, dwmColourDefault},
			} {
				if got := captionColour(dark, c.paints, c.contrast, c.prevalence, green); got != c.want {
					t.Errorf("captionColour(dark %v, build 22000+ %v, contrast %v, ColorPrevalence %d, green %v) = %#08x, want %#08x",
						dark, c.paints, c.contrast, c.prevalence, green, got, c.want)
				}
			}
		}
	}
	if captionColour(false, true, false, 0, false) == captionColour(true, true, false, 0, false) {
		t.Fatal("one caption colour for both themes")
	}
	if captionColour(false, true, false, 0, true) == captionColour(true, true, false, 0, true) {
		t.Fatal("one touch colour for both themes")
	}
	for _, dark := range []bool{false, true} {
		if captionColour(dark, true, false, 0, true) == captionColour(dark, true, false, 0, false) {
			t.Errorf("the green caption is the ground's (dark %v)", dark)
		}
	}
}

// TestTheCaptionTextRule is the caption's text (APP.md §2.4): white while
// the touch's green is up and the caption is the shell's to paint, so the
// window's name reads on the green in both themes; DWM's own, which DWM
// picks by the window's dark mode, while the ground is up and under every
// guard, green or not: white text on DWM's own caption, a contrast
// theme's or the accent's, could be white on white.
func TestTheCaptionTextRule(t *testing.T) {
	if captionInk != 0x00FFFFFF || captionInk == dwmColourDefault {
		t.Fatalf("the caption's ink is %#08x, want white as a COLORREF, 0x00ffffff, which is not DWMWA_COLOR_DEFAULT", captionInk)
	}
	if got := colorref(application.NewRGB(0xFF, 0xFF, 0xFF)); got != captionInk {
		t.Fatalf("white's COLORREF is %#08x, the ink %#08x", got, captionInk)
	}
	for _, c := range []struct {
		paints, contrast bool
		prevalence       uint64
		green            bool
		want             uint32
	}{
		{true, false, 0, true, captionInk},
		{true, false, 0, false, dwmColourDefault},
		{false, false, 0, true, dwmColourDefault}, // Windows 10
		{true, true, 0, true, dwmColourDefault},   // a contrast theme
		{true, false, 1, true, dwmColourDefault},  // accent title bars
		{false, true, 1, true, dwmColourDefault},
		{true, true, 1, true, dwmColourDefault},
		{false, false, 1, true, dwmColourDefault},
		{true, true, 0, false, dwmColourDefault},
		{true, false, 1, false, dwmColourDefault},
	} {
		if got := captionText(c.paints, c.contrast, c.prevalence, c.green); got != c.want {
			t.Errorf("captionText(build 22000+ %v, contrast %v, ColorPrevalence %d, green %v) = %#08x, want %#08x",
				c.paints, c.contrast, c.prevalence, c.green, got, c.want)
		}
		// The text is white only where the caption is the touch's green.
		painted := captionColour(false, c.paints, c.contrast, c.prevalence, c.green) == colorref(touchOf(false))
		if (captionText(c.paints, c.contrast, c.prevalence, c.green) == captionInk) != painted {
			t.Errorf("white text and the green caption part (build 22000+ %v, contrast %v, ColorPrevalence %d, green %v)",
				c.paints, c.contrast, c.prevalence, c.green)
		}
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
	light := captionLook{theme: captionLight, colour: captionColour(false, true, false, 0, false), text: dwmColourDefault}
	dark := captionLook{theme: captionDark, colour: captionColour(true, true, false, 0, false), text: dwmColourDefault}
	touched := func(c *captionState, id uint, want captionLook) (bool, bool) {
		th, co, tx := c.touch(id, want, false)
		if tx {
			t.Errorf("window %d's text was touched with no green (want %+v)", id, want)
		}
		return th, co
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
	c.applied(true, true, false, dark)
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
	c.loaded(3, captionLook{theme: captionUnknown, colour: dwmColourDefault, text: dwmColourDefault})
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
	look := func(dark, contrast bool, prevalence uint64) captionLook {
		return captionLook{theme: captionOf(dark), colour: captionColour(dark, true, contrast, prevalence, false), text: captionText(true, contrast, prevalence, false)}
	}
	light := look(false, false, 0)
	var c captionState
	c.loaded(1, light)

	// A contrast theme begins over the painted light caption.
	hc := look(false, true, 0)
	th, co, tx := c.touch(1, hc, true)
	if th || !co || tx {
		t.Fatalf("a contrast theme began: theme %v colour %v text %v, want the colour alone, reset", th, co, tx)
	}
	c.applied(th, co, tx, hc)
	if c.wears.colour != dwmColourDefault || c.wears.theme != captionLight {
		t.Fatalf("under the contrast theme the caption wears %+v, want light with DWM's own colour", c.wears)
	}

	// The setting flips to dark under it: nothing is touched.
	hcDark := look(true, true, 0)
	if th, co, tx := c.touch(1, hcDark, true); th || co || tx {
		t.Errorf("under a contrast theme a flip set theme %v colour %v text %v, want nothing", th, co, tx)
	}

	// The contrast theme ends: dark and its ground.
	dark := look(true, false, 0)
	if th, co, tx := c.touch(1, dark, false); !th || !co || tx {
		t.Errorf("after the contrast theme the caption is caught up in theme %v colour %v text %v, want the theme and the colour", th, co, tx)
	}

	// Accent title bars turned on over a painted caption: the colour
	// alone goes back to DWM, and turned off again it comes back.
	c.loaded(2, light)
	accent := look(false, false, 1)
	th, co, tx = c.touch(2, accent, false)
	if th || !co || tx {
		t.Fatalf("accent title bars: theme %v colour %v text %v, want the colour alone", th, co, tx)
	}
	c.applied(th, co, tx, accent)
	if th, co, tx := c.touch(2, light, false); th || !co || tx {
		t.Errorf("accent title bars off: theme %v colour %v text %v, want the colour alone", th, co, tx)
	}
}

// greenLook is the caption the rule gives a window: its theme, its colour
// and its text's, with the green up or not, on Windows 11.
func greenLook(dark, contrast bool, prevalence uint64, green bool) captionLook {
	return captionLook{
		theme:  captionOf(dark),
		colour: captionColour(dark, true, contrast, prevalence, green),
		text:   captionText(true, contrast, prevalence, green),
	}
}

// TestTheCaptionTakesTheGreen (APP.md §2.4, §6; ruled 2026-11-03): as the
// touch's green comes up the caption's colour and its text's are set, its
// theme left as it is; a repeat sets nothing; as the green goes both come
// back, the ground and DWM's own text; a theme flipped under the green
// keeps it, in the theme's own green; a contrast theme or accent title
// bars arriving while the green is up give the colour and the text back
// to DWM together, and the green is painted again when they go while it
// is still up.
func TestTheCaptionTakesTheGreen(t *testing.T) {
	var c captionState
	c.loaded(1, greenLook(false, false, 0, false))
	step := func(name string, want captionLook, contrast, wantTh, wantCo, wantTx bool) {
		t.Helper()
		th, co, tx := c.touch(1, want, contrast)
		if th != wantTh || co != wantCo || tx != wantTx {
			t.Fatalf("%s: theme %v colour %v text %v, want %v %v %v", name, th, co, tx, wantTh, wantCo, wantTx)
		}
		c.applied(th, co, tx, want)
		if contrast {
			want.theme = c.wears.theme // a contrast theme leaves the theme alone
		}
		if c.wears != want {
			t.Fatalf("%s: the caption wears %+v, want %+v", name, c.wears, want)
		}
	}
	step("the green comes up", greenLook(false, false, 0, true), false, false, true, true)
	if c.wears.colour != colorref(touchOf(false)) || c.wears.text != captionInk {
		t.Fatalf("on the green the caption wears %+v", c.wears)
	}
	step("the green said again", greenLook(false, false, 0, true), false, false, false, false)
	step("the green goes", greenLook(false, false, 0, false), false, false, true, true)
	if c.wears.colour != colorref(groundOf(false)) || c.wears.text != dwmColourDefault {
		t.Fatalf("after the green the caption wears %+v", c.wears)
	}
	step("the green again", greenLook(false, false, 0, true), false, false, true, true)
	step("dark under the green", greenLook(true, false, 0, true), false, true, true, false)
	step("a contrast theme under the green", greenLook(true, true, 0, true), true, false, true, true)
	if c.wears.colour != dwmColourDefault || c.wears.text != dwmColourDefault {
		t.Fatalf("under a contrast theme the caption wears %+v", c.wears)
	}
	step("the contrast theme ends, the green still up", greenLook(true, false, 0, true), false, false, true, true)
	step("accent title bars under the green", greenLook(true, false, 1, true), false, false, true, true)
	step("the green goes under accent title bars", greenLook(true, false, 1, false), false, false, false, false)
	step("accent title bars off", greenLook(true, false, 0, false), false, false, true, false)
}

// TestTheGreenStaysWithItsWindow: the page's word is held for the window
// it was said in (APP.md §2.4). A repeat moves nothing; a window made
// again from the tray, a new id, wears no green until its own page says
// so, and a caption on it is the ground's; the green said in it is its
// own; a load does not take it down.
func TestTheGreenStaysWithItsWindow(t *testing.T) {
	var g touchGreen
	if g.of(1) || g.of(0) {
		t.Fatal("a green before any word")
	}
	if !g.say(true, 1) || !g.of(1) {
		t.Fatal("the page's green did not reach its window")
	}
	if g.say(true, 1) {
		t.Error("a repeat moved the look")
	}
	// Closed to the tray mid-touch and made again: window 2.
	if g.of(2) {
		t.Error("a window made again wears the green of the one closed under it")
	}
	if want := colorref(groundOf(false)); captionColour(false, true, false, 0, g.of(2)) != want || captionText(true, false, 0, g.of(2)) != dwmColourDefault {
		t.Error("the window made again is not given the ground and DWM's text")
	}
	if !g.say(true, 2) || !g.of(2) || g.of(1) {
		t.Error("the new window's own page did not move the green to it")
	}
	if !g.say(false, 2) || g.of(2) {
		t.Error("the green did not go")
	}
	if g.say(false, 2) || g.say(false, 3) {
		t.Error("a repeated no moved the look")
	}
	if g.of(0) {
		t.Error("no window (id 0) wears the green")
	}
	// The window's page loaded again: the caption state is told, the
	// green is not, since the new page may have spoken first.
	g.say(true, 2)
	var c captionState
	c.loaded(2, greenLook(false, false, 0, false))
	c.loaded(2, greenLook(false, false, 0, false))
	if !g.of(2) {
		t.Error("a load took the green down")
	}
}

// TestTheGreenBackstop: a ceremony that leaves the stage while the green
// is up arms the backstop, which takes the green down after greenGrace
// unless the page has spoken since or a ceremony has come back; a state
// older than one seen is ignored, and a state while the green is down, or
// while a ceremony runs, arms nothing (APP.md §2.4).
func TestTheGreenBackstop(t *testing.T) {
	var g touchGreen
	if arm, _ := g.state(1, app.StateLocked); arm {
		t.Fatal("the backstop armed with no green")
	}
	g.state(2, app.StateUnlocking)
	g.say(true, 1)
	if arm, _ := g.state(3, app.StateUnlocking); arm {
		t.Fatal("the backstop armed while the ceremony runs")
	}
	if arm, _ := g.state(4, app.StateReleasing); arm {
		t.Fatal("the backstop armed while the ceremony releases the card")
	}

	// An unlock ends; the page takes the green down itself as the scene
	// leaves: the timer stands down.
	arm, said := g.state(5, app.StateUnlocked)
	if !arm {
		t.Fatal("an unlock's end did not arm the backstop")
	}
	g.say(false, 1)
	if g.expire(said) || g.of(1) {
		t.Fatal("the backstop moved a green the page had taken down")
	}

	// The page goes without a word (a reload that never reaches the
	// stage): the timer takes the green down.
	g.state(6, app.StateUnlocking)
	g.say(true, 1)
	arm, said = g.state(7, app.StateLocked)
	if !arm {
		t.Fatal("a ceremony's end did not arm the backstop")
	}
	if !g.expire(said) || g.of(1) {
		t.Fatal("the backstop left the green up")
	}
	if g.expire(said) {
		t.Error("the backstop fired twice")
	}

	// A page that speaks after the timer was armed keeps its green.
	g.state(8, app.StateUnlocking)
	g.say(true, 1)
	_, said = g.state(9, app.StateLocked)
	g.say(true, 1) // the page's lock stage mounted again, saying what it shows
	if g.expire(said) || !g.of(1) {
		t.Error("the backstop overrode a later word")
	}

	// A ceremony that comes back before the timer fires keeps the green.
	_, said = g.state(10, app.StateLocked)
	g.state(11, app.StateUnlocking)
	if g.expire(said) || !g.of(1) {
		t.Error("the backstop fired while a ceremony ran again")
	}

	// A state older than one seen is ignored.
	if arm, _ := g.state(9, app.StateLocked); arm || !g.running {
		t.Error("a late state was believed")
	}

	if greenGrace < time.Second {
		t.Errorf("greenGrace is %v", greenGrace)
	}
}

// TestTheGraceOutlastsTheSettle: the backstop waits longer than an
// unlock's Done stands on the green after the state says Unlocked — the
// settle and the scene's leave, frontend/src/lib/motion.ts's SETTLE and
// OUT — and twice that, so a caption is never given back over a green
// page that is about to take it down itself.
func TestTheGraceOutlastsTheSettle(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("frontend", "src", "lib", "motion.ts"))
	if err != nil {
		t.Fatal(err)
	}
	ms := func(name string) time.Duration {
		m := regexp.MustCompile(`export const ` + name + ` = (\d+);`).FindSubmatch(b)
		if m == nil {
			t.Fatalf("motion.ts declares no %s", name)
		}
		n, _ := strconv.Atoi(string(m[1]))
		return time.Duration(n) * time.Millisecond
	}
	if leave := ms("SETTLE") + ms("OUT"); greenGrace < 2*leave {
		t.Errorf("greenGrace %v is less than twice the settle and the leave, %v", greenGrace, leave)
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

// TestTheFadeRunsOnThePagesCurves: the caption's fade reads a timing
// function as the page says it (APP.md §2.4). The stylesheet's two curves,
// the wash's in and out, read as the Béziers they are, and those Béziers
// give what headless Edge gave the wash's opacity at the same moments
// (Chromium's own curve); CSS's keywords are its Béziers, ease giving what
// Edge gave; Svelte's cubicOut, the scene's lift, is the Bézier (1/3, 1,
// 2/3, 1) exactly, and the linear() the page says for the lift passes
// through its keyframes; a linear() joins its points with lines; and anything
// the shell cannot read moves evenly.
func TestTheFadeRunsOnThePagesCurves(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("frontend", "src", "app.css"))
	if err != nil {
		t.Fatal(err)
	}
	token := func(name string) fadeCurve {
		m := regexp.MustCompile(`\n  ` + name + `: ([^;]+);`).FindSubmatch(b)
		if m == nil {
			t.Fatalf("app.css declares no %s", name)
		}
		return cssCurve(string(m[1]))
	}
	enter, exit := token("--ease-enter"), token("--ease-exit")
	if want := (fadeCurve{x1: 0.2, y1: 0, x2: 0, y2: 1}); !reflect.DeepEqual(enter, want) {
		t.Errorf("--ease-enter reads %+v, want %+v", enter, want)
	}
	if want := (fadeCurve{x1: 0.4, y1: 0, x2: 1, y2: 1}); !reflect.DeepEqual(exit, want) {
		t.Errorf("--ease-exit reads %+v, want %+v", exit, want)
	}
	// What headless Edge gave each curve (edgeCurvePoints), to the six
	// figures it prints an opacity in.
	for _, k := range edgeCurvePoints {
		got := cssCurve(k.curve).at(k.p)
		if math.Abs(got-k.want) > 2e-6 {
			t.Errorf("%s at %.4f gives %.5f, Edge gave %.4f", k.curve, k.p, got, k.want)
		}
	}
	out3 := cssCurve("cubic-bezier(0.3333333, 1, 0.6666667, 1)")
	for i := 0; i <= 100; i++ {
		p := float64(i) / 100
		if got, want := out3.at(p), 1-math.Pow(1-p, 3); math.Abs(got-want) > 1e-6 {
			t.Fatalf("the lift's Bézier at %.2f gives %.7f, cubicOut %.7f", p, got, want)
		}
	}
	// The lift as the page says it is Svelte's keyframes, cubicOut at each
	// tenth of the 160 ms, to the four places the page writes.
	lift := cssCurve(liftCurve)
	for i := 0; i <= 10; i++ {
		p := float64(i) / 10
		if got, want := lift.at(p), 1-math.Pow(1-p, 3); math.Abs(got-want) > 1e-4 {
			t.Errorf("the lift's linear() at %.1f gives %.4f, Svelte's keyframe %.4f", p, got, want)
		}
	}
	for _, c := range []struct {
		curve string
		p     float64
		want  float64
	}{
		{"linear", 0.3, 0.3},
		{"LINEAR", 0.7, 0.7},
		{"linear(0, 0.5, 1)", 0.25, 0.25},
		{"linear(0, 1, 0)", 0.5, 1},
		{"linear(0, 1, 0)", 0.75, 0.5},
		{"linear(1, 0.271, 0)", 0, 1},
		{"linear(1, 0.271, 0)", 1, 0},
		{"steps(4, end)", 0.3, 0.3},
		{"linear(0, 0.5 50%, 1)", 0.3, 0.3},
		{"cubic-bezier(1.2, 0, 0, 1)", 0.3, 0.3},
		{"cubic-bezier(0.2, 0, 0)", 0.3, 0.3},
		{"", 0.3, 0.3},
		{"ease-in-out", 0.5, 0.5},
		{"cubic-bezier(0.2, 0, 0, 1)", 0, 0},
		{"cubic-bezier(0.2, 0, 0, 1)", 1, 1},
		{"cubic-bezier(0.2, 0, 0, 1)", -0.5, 0},
		{"cubic-bezier(0.2, 0, 0, 1)", 1.5, 1},
	} {
		if got := cssCurve(c.curve).at(c.p); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%q at %.2f gives %.6f, want %.6f", c.curve, c.p, got, c.want)
		}
	}
	if got := cssCurve(" cubic-bezier( 0.34 , 1.56 , 0.64 , 1 ) "); !reflect.DeepEqual(got, fadeCurve{x1: 0.34, y1: 1.56, x2: 0.64, y2: 1}) {
		t.Errorf("a spaced Bézier with an overshoot reads %+v", got)
	}
}

// edgeCurve is one point of a CSS curve as headless Edge drew it.
type edgeCurve struct {
	curve string
	p     float64
	want  float64
}

// edgeCurvePoints: an opacity animation from 0 to 1 over 1000 ms on each
// timing function, paused and seeked to each share of its time, its
// computed opacity read back (headless Edge 156, 2026-11-03,
// p5-build-scripts/eased/frames.mjs): the stylesheet's two curves, CSS's
// keywords, and the lift's cubicOut as a Bézier.
var edgeCurvePoints = []edgeCurve{
	{"cubic-bezier(0.2, 0, 0, 1)", 0.1, 0.15625},
	{"cubic-bezier(0.2, 0, 0, 1)", 0.25, 0.60722},
	{"cubic-bezier(0.2, 0, 0, 1)", 0.5, 0.877834},
	{"cubic-bezier(0.2, 0, 0, 1)", 0.75, 0.97548},
	{"cubic-bezier(0.2, 0, 0, 1)", 0.9, 0.996459},
	{"cubic-bezier(0.4, 0, 1, 1)", 0.1, 0.0183729},
	{"cubic-bezier(0.4, 0, 1, 1)", 0.25, 0.0986266},
	{"cubic-bezier(0.4, 0, 1, 1)", 0.5, 0.324815},
	{"cubic-bezier(0.4, 0, 1, 1)", 0.75, 0.630085},
	{"cubic-bezier(0.4, 0, 1, 1)", 0.9, 0.84375},
	{"ease", 0.1, 0.0947963},
	{"ease", 0.25, 0.408511},
	{"ease", 0.5, 0.802403},
	{"ease", 0.75, 0.960459},
	{"ease", 0.9, 0.994316},
	{"ease-in", 0.1, 0.0170266},
	{"ease-in", 0.25, 0.0934647},
	{"ease-in", 0.5, 0.315357},
	{"ease-in", 0.75, 0.621862},
	{"ease-in", 0.9, 0.839428},
	{"ease-out", 0.1, 0.160572},
	{"ease-out", 0.25, 0.378138},
	{"ease-out", 0.5, 0.684643},
	{"ease-out", 0.75, 0.906535},
	{"ease-out", 0.9, 0.982973},
	{"ease-in-out", 0.1, 0.0197225},
	{"ease-in-out", 0.25, 0.129162},
	{"ease-in-out", 0.5, 0.5},
	{"ease-in-out", 0.75, 0.870838},
	{"ease-in-out", 0.9, 0.980278},
	{"cubic-bezier(0.3333333, 1, 0.6666667, 1)", 0.1, 0.271},
	{"cubic-bezier(0.3333333, 1, 0.6666667, 1)", 0.25, 0.578125},
	{"cubic-bezier(0.3333333, 1, 0.6666667, 1)", 0.5, 0.875},
	{"cubic-bezier(0.3333333, 1, 0.6666667, 1)", 0.75, 0.984375},
	{"cubic-bezier(0.3333333, 1, 0.6666667, 1)", 0.9, 0.999},
}

// liftCurve is what the page says for the scene's lift (Shell.StageGreen,
// read in the mock, 2026-11-03; Lock.svelte sceneMove): the ground's share
// of the window's first row at 41 evenly spaced moments, the 11 keyframes
// Svelte samples cubicOut into over 160 ms and three between each two,
// which the browser joins with straight lines, so every fourth is a
// keyframe's (0, 0.271, 0.488, … 0.999, 1).
const liftCurve = "linear(0, 0.0677, 0.1355, 0.2032, 0.271, 0.3253, 0.3795, 0.4337, 0.488, 0.5302, " +
	"0.5725, 0.6148, 0.657, 0.6887, 0.7205, 0.7522, 0.784, 0.8067, 0.8295, 0.8522, " +
	"0.875, 0.8902, 0.9055, 0.9207, 0.936, 0.9453, 0.9545, 0.9637, 0.973, 0.9778, " +
	"0.9825, 0.9872, 0.992, 0.9938, 0.9955, 0.9972, 0.999, 0.9992, 0.9995, 0.9998, 1)"

// enterCurve is what the page says for the lock scene's entrance with the
// stage already green (Shell.StageGreen as the scene's introstart comes,
// read in the mock, 2026-11-03; Lock.svelte sceneMove): the green's share
// of the window's first row at 57 evenly spaced moments of the scene's
// 220 ms rise, its opacity times the share of the row it covers, which is
// nothing until its top edge, 8 px down at first, enters the row.
const enterCurve = "linear(0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, " +
	"0, 0, 0, 0, 0, 0, 0.0163, 0.0746, 0.1386, 0.218, 0.3101, 0.4118, 0.4839, 0.558, " +
	"0.633, 0.7081, 0.7533, 0.7978, 0.8415, 0.8841, 0.9055, 0.9265, 0.9471, 0.9672, " +
	"0.9745, 0.9817, 0.9889, 0.996, 0.997, 0.998, 0.999, 1)"

// edgeRow is the middle of the window's first row as headless Edge
// composited it ms into a change of the green: see
// TestTheCaptionMixesAsThePageComposites.
type edgeRow struct {
	theme string  // "light" or "dark"
	fade  string  // "in", "out" (the wash's on a timeout), "lift" (the scene's leave after Done), "enter" (the scene's entrance, the stage green)
	ms    float64 // the moment, from the change's start
	row   string  // "#rrggbb"
}

// edgeRows: every 16 ms through the wash's fades and about every 12 ms
// through the lift, in both themes (p5-build-scripts/eased/frames.mjs,
// 2026-11-03), and every 8 ms through the scene's entrance with the stage
// green (p5-build-scripts/eased/repair/entrance.mjs, 2026-11-03: a pending
// touch adopted by the tray's Unlock… over an open archive, every
// animation held as the entrance began and seeked).
var edgeRows = []edgeRow{
	{"light", "in", 0, "#d5d3d5"},
	{"light", "in", 16, "#c7cbcd"},
	{"light", "in", 32, "#92b0ad"},
	{"light", "in", 48, "#699a95"},
	{"light", "in", 64, "#4e8c84"},
	{"light", "in", 80, "#3c827a"},
	{"light", "in", 96, "#2f7b72"},
	{"light", "in", 112, "#24766c"},
	{"light", "in", 128, "#1d7268"},
	{"light", "in", 144, "#176f64"},
	{"light", "in", 160, "#146d62"},
	{"light", "in", 176, "#106a60"},
	{"light", "in", 192, "#0e6a5f"},
	{"light", "in", 208, "#0d695e"},
	{"light", "in", 220, "#0d695e"},
	{"light", "out", 0, "#0d695e"},
	{"light", "out", 16, "#116b60"},
	{"light", "out", 32, "#1a7066"},
	{"light", "out", 48, "#28786e"},
	{"light", "out", 64, "#3a8179"},
	{"light", "out", 80, "#4e8c84"},
	{"light", "out", 96, "#659893"},
	{"light", "out", 112, "#7ea5a1"},
	{"light", "out", 128, "#99b3b1"},
	{"light", "out", 144, "#b6c2c3"},
	{"light", "out", 160, "#d5d3d5"},
	{"light", "lift", 0.67, "#0e6a5f"},
	{"light", "lift", 12.77, "#367f76"},
	{"light", "lift", 25.11, "#5b928d"},
	{"light", "lift", 37.54, "#79a29e"},
	{"light", "lift", 49.98, "#93b0ae"},
	{"light", "lift", 62.48, "#a7baba"},
	{"light", "lift", 74.92, "#b6c3c3"},
	{"light", "lift", 87.4, "#c1c8c9"},
	{"light", "lift", 99.81, "#cacdce"},
	{"light", "lift", 112.21, "#cfd0d2"},
	{"light", "lift", 124.6, "#d2d2d3"},
	{"light", "lift", 137, "#d4d2d4"},
	{"light", "lift", 149.33, "#d5d3d5"},
	{"dark", "in", 0, "#262526"},
	{"dark", "in", 16, "#242728"},
	{"dark", "in", 32, "#1d322f"},
	{"dark", "in", 48, "#173a35"},
	{"dark", "in", 64, "#133f39"},
	{"dark", "in", 80, "#11423c"},
	{"dark", "in", 96, "#0f443e"},
	{"dark", "in", 112, "#0e463f"},
	{"dark", "in", 128, "#0d4841"},
	{"dark", "in", 144, "#0c4942"},
	{"dark", "in", 160, "#0c4a42"},
	{"dark", "in", 176, "#0c4b43"},
	{"dark", "in", 192, "#0b4b43"},
	{"dark", "in", 208, "#0b4b43"},
	{"dark", "in", 220, "#0b4b43"},
	{"dark", "out", 0, "#0b4b43"},
	{"dark", "out", 16, "#0c4b43"},
	{"dark", "out", 32, "#0d4842"},
	{"dark", "out", 48, "#0e463f"},
	{"dark", "out", 64, "#11423c"},
	{"dark", "out", 80, "#133f39"},
	{"dark", "out", 96, "#173a37"},
	{"dark", "out", 112, "#1a3632"},
	{"dark", "out", 128, "#1e312f"},
	{"dark", "out", 144, "#222b2b"},
	{"dark", "out", 160, "#262526"},
	{"dark", "lift", 0.58, "#0b4b43"},
	{"dark", "lift", 12.62, "#11443e"},
	{"dark", "lift", 24.65, "#163c38"},
	{"dark", "lift", 36.64, "#193733"},
	{"dark", "lift", 48.64, "#1d3230"},
	{"dark", "lift", 60.69, "#202f2e"},
	{"dark", "lift", 72.83, "#222c2b"},
	{"dark", "lift", 84.98, "#232929"},
	{"dark", "lift", 97.06, "#252828"},
	{"dark", "lift", 109.21, "#252727"},
	{"dark", "lift", 121.44, "#252526"},
	{"dark", "lift", 133.65, "#262526"},
	{"dark", "lift", 145.83, "#262526"},
	{"dark", "lift", 158.06, "#262526"},
	{"light", "enter", 0, "#d5d3d5"},
	{"light", "enter", 8, "#d5d3d5"},
	{"light", "enter", 16, "#d5d3d5"},
	{"light", "enter", 24, "#d5d3d5"},
	{"light", "enter", 32, "#d5d3d5"},
	{"light", "enter", 40, "#d5d3d5"},
	{"light", "enter", 48, "#d5d3d5"},
	{"light", "enter", 56, "#d5d3d5"},
	{"light", "enter", 64, "#d5d3d5"},
	{"light", "enter", 72, "#d5d3d5"},
	{"light", "enter", 80, "#d5d3d5"},
	{"light", "enter", 88, "#d5d3d5"},
	{"light", "enter", 96, "#d5d3d5"},
	{"light", "enter", 104, "#d5d3d5"},
	{"light", "enter", 112, "#d5d3d5"},
	{"light", "enter", 120, "#d4d2d4"},
	{"light", "enter", 128, "#bfc8c8"},
	{"light", "enter", 136, "#9fb6b4"},
	{"light", "enter", 144, "#79a39e"},
	{"light", "enter", 152, "#5b928c"},
	{"light", "enter", 160, "#41847d"},
	{"light", "enter", 168, "#2f7c72"},
	{"light", "enter", 176, "#22746a"},
	{"light", "enter", 184, "#197066"},
	{"light", "enter", 192, "#136c62"},
	{"light", "enter", 200, "#106a60"},
	{"light", "enter", 208, "#0e695e"},
	{"light", "enter", 216, "#0d695e"},
	{"light", "enter", 220, "#0d695e"},
	{"dark", "enter", 0, "#262526"},
	{"dark", "enter", 8, "#262526"},
	{"dark", "enter", 16, "#262526"},
	{"dark", "enter", 24, "#262526"},
	{"dark", "enter", 32, "#262526"},
	{"dark", "enter", 40, "#262526"},
	{"dark", "enter", 48, "#262526"},
	{"dark", "enter", 56, "#262526"},
	{"dark", "enter", 64, "#262526"},
	{"dark", "enter", 72, "#262526"},
	{"dark", "enter", 80, "#262526"},
	{"dark", "enter", 88, "#262526"},
	{"dark", "enter", 96, "#262526"},
	{"dark", "enter", 104, "#262526"},
	{"dark", "enter", 112, "#262526"},
	{"dark", "enter", 120, "#262526"},
	{"dark", "enter", 128, "#232929"},
	{"dark", "enter", 136, "#1f2f2e"},
	{"dark", "enter", 144, "#1a3634"},
	{"dark", "enter", 152, "#163c38"},
	{"dark", "enter", 160, "#13413c"},
	{"dark", "enter", 168, "#0f443d"},
	{"dark", "enter", 176, "#0e4740"},
	{"dark", "enter", 184, "#0c4941"},
	{"dark", "enter", 192, "#0c4a42"},
	{"dark", "enter", 200, "#0b4a42"},
	{"dark", "enter", 208, "#0b4b43"},
	{"dark", "enter", 216, "#0b4b43"},
	{"dark", "enter", 220, "#0b4b43"},
}

// TestTheCaptionMixesAsThePageComposites: a fade of the caption is the
// page's own first row at every moment (APP.md §2.4). At its start it is
// what the caption showed, the ground and DWM's ink or the green and
// white; at half time it is the sRGB mix of the two on the page's curve,
// and the row headless Edge composited at the same moment within a level;
// at its end it is the look itself, the green with white text or the
// ground with DWM's own text. Both ways, in both themes, and through the
// scene's lift and its entrance, whose curves the page reads off Svelte's
// keyframes.
func TestTheCaptionMixesAsThePageComposites(t *testing.T) {
	t0 := time.Unix(1_800_000_000, 0)
	at := func(f *captionFade, ms float64) (captionPair, bool) {
		return f.at(t0.Add(time.Duration(ms * float64(time.Millisecond))))
	}
	for _, dark := range []bool{false, true} {
		ground, green := colorref(groundOf(dark)), colorref(touchOf(dark))
		ink := captionDefaultInk(dark, true)
		onGround := captionPair{colour: ground, text: dwmColourDefault}
		onGreen := captionPair{colour: green, text: captionInk}
		ways := []struct {
			name       string
			ms         int
			curve      string
			from, to   captionPair
			fromInk    captionPair
			share      func(p float64) float64 // the green's share of the page's row at p
			start, end captionPair
		}{
			{"in", 220, "cubic-bezier(0.2, 0, 0, 1)", onGround, onGreen, captionPair{ground, ink},
				func(p float64) float64 { return cssCurve("cubic-bezier(0.2, 0, 0, 1)").at(p) },
				captionPair{ground, ink}, onGreen},
			{"out", 160, "cubic-bezier(0.4, 0, 1, 1)", onGreen, onGround, onGreen,
				func(p float64) float64 { return 1 - cssCurve("cubic-bezier(0.4, 0, 1, 1)").at(p) },
				onGreen, onGround},
			{"lift", 160, liftCurve, onGreen, onGround, onGreen,
				func(p float64) float64 { return 1 - cssCurve(liftCurve).at(p) },
				onGreen, onGround},
			{"enter", 220, enterCurve, onGround, onGreen, captionPair{ground, ink},
				func(p float64) float64 { return cssCurve(enterCurve).at(p) },
				captionPair{ground, ink}, onGreen},
		}
		for _, w := range ways {
			name := fmt.Sprintf("%s (dark %v)", w.name, dark)
			f := planFade(w.ms, w.curve, false, w.from, w.to, dark, true, t0)
			if f == nil {
				t.Fatalf("%s: no fade", name)
			}
			if f.from != w.fromInk {
				t.Errorf("%s: the fade starts from %+v, want %+v", name, f.from, w.fromInk)
			}
			if c, done := at(f, 0); done || c != w.start {
				t.Errorf("%s: at its start the caption is %+v (done %v), want %+v", name, c, done, w.start)
			}
			half := float64(w.ms) / 2
			c, done := at(f, half)
			if done {
				t.Fatalf("%s: ended at half time", name)
			}
			if want := mixColour(ground, green, w.share(0.5)); c.colour != want {
				t.Errorf("%s: at half time the caption is %06X, the page's mix %06X", name, c.colour, want)
			}
			if c, done := at(f, float64(w.ms)); !done || c != w.end {
				t.Errorf("%s: at its end the caption is %+v (done %v), want %+v", name, c, done, w.end)
			}
		}
	}
	// What headless Edge composited in the middle of the window's first
	// row (p5-build-scripts/eased/frames.mjs, 2026-11-03): through the
	// wash's fades, the timeline frozen and every animation seeked to the
	// moment, within a level of the shell's mix, Chromium's own rounding
	// being a premultiplied blend's; through the lift, the timeline at a
	// hundredth of its pace and the moment read just before and after the
	// capture, within two, a level being under a millisecond of the lift's
	// fast start; through the entrance, every animation held as it began
	// and seeked, within two, the row there being the scene's opacity times
	// the edge Chromium draws as the scene's top enters it.
	curves := map[string]string{"in": "cubic-bezier(0.2, 0, 0, 1)", "out": "cubic-bezier(0.4, 0, 1, 1)", "lift": liftCurve, "enter": enterCurve}
	lengths := map[string]int{"in": 220, "out": 160, "lift": 160, "enter": 220}
	for _, r := range edgeRows {
		dark := r.theme == "dark"
		ground, green := captionPair{colorref(groundOf(dark)), dwmColourDefault}, captionPair{colorref(touchOf(dark)), captionInk}
		from, to := ground, green
		if r.fade == "out" || r.fade == "lift" {
			from, to = green, ground
		}
		f := planFade(lengths[r.fade], curves[r.fade], false, from, to, dark, true, t0)
		c, _ := at(f, r.ms)
		row := hexColorref(t, r.row)
		allowed := 1
		if r.fade == "lift" || r.fade == "enter" {
			allowed = 2
		}
		if d := levels(c.colour, row); d > allowed {
			t.Errorf("%s %s at %.2f ms: the caption %06X, the page's row %s, %d levels apart", r.theme, r.fade, r.ms, c.colour, r.row, d)
		}
	}
}

// hexColorref is a "#rrggbb" as a COLORREF.
func hexColorref(t *testing.T, h string) uint32 {
	t.Helper()
	v, err := strconv.ParseUint(strings.TrimPrefix(h, "#"), 16, 32)
	if err != nil || len(h) != 7 {
		t.Fatalf("not a colour: %q", h)
	}
	return uint32(v>>16) | uint32(v>>8&0xFF)<<8 | uint32(v&0xFF)<<16
}

// levels is the largest difference between two COLORREFs' channels.
func levels(a, b uint32) int {
	d := 0
	for shift := 0; shift < 24; shift += 8 {
		x := int(a>>shift&0xFF) - int(b>>shift&0xFF)
		d = max(d, x, -x)
	}
	return d
}

// TestTheTextFadesFromDWMsInk: the caption's text is mixed from the ink
// DWM draws on the ground, in the theme and for the window as it is,
// active or not, to white as the green comes, and back, its last frame
// giving the text to DWM again; in light it passes through the greys
// between, from black on an active window and from DWM's dimmed grey on
// an inactive one, so that neither end steps; in dark it stays white.
func TestTheTextFadesFromDWMsInk(t *testing.T) {
	t0 := time.Unix(1_800_000_000, 0)
	for _, dark := range []bool{false, true} {
		for _, active := range []bool{true, false} {
			name := fmt.Sprintf("dark %v, active %v", dark, active)
			ink := captionDefaultInk(dark, active)
			ground := captionPair{colorref(groundOf(dark)), dwmColourDefault}
			green := captionPair{colorref(touchOf(dark)), captionInk}
			in := planFade(220, "linear", false, ground, green, dark, active, t0)
			if c, _ := in.at(t0); c.text != ink {
				t.Errorf("%s: the text at the start is %06X, want DWM's %06X", name, c.text, ink)
			}
			c, _ := in.at(t0.Add(110 * time.Millisecond))
			if want := mixColour(ink, captionInk, 0.5); c.text != want {
				t.Errorf("%s: the text at half way is %06X, want %06X", name, c.text, want)
			}
			out := planFade(160, "linear", false, green, ground, dark, active, t0)
			if c, done := out.at(t0.Add(159 * time.Millisecond)); done || levels(c.text, ink) > 2 {
				t.Errorf("%s: the text just before the end is %06X, want near DWM's %06X", name, c.text, ink)
			}
			if c, _ := out.at(t0.Add(160 * time.Millisecond)); c.text != dwmColourDefault {
				t.Errorf("%s: the last frame's text is %06X, want DWM's own", name, c.text)
			}
		}
	}
	// As the touch's review probed them (build 26300): black and the
	// dimmed #7F7E7F on the light ground; white on the dark one, not
	// probed, whatever the window's state.
	for _, k := range []struct {
		dark, active bool
		ink          uint32
	}{
		{false, true, 0x000000},
		{false, false, 0x7F7E7F},
		{true, true, 0xFFFFFF},
		{true, false, 0xFFFFFF},
	} {
		if got := captionDefaultInk(k.dark, k.active); got != k.ink {
			t.Errorf("dark %v, active %v: DWM's ink on the ground is %06X, want %06X", k.dark, k.active, got, k.ink)
		}
	}
}

// TestAFadeIsOnlyTheGreens: a change is eased only where the page gave a
// duration, the theme has not moved and the shell paints the caption at
// both ends (planFade); a page that draws its change at once (0 ms), a
// theme flipped, a caption DWM draws, at either end, is set at once.
// Under every guard the green moves nothing at all: the caption DWM draws
// is the same with the green up or down, so no change reaches planFade.
func TestAFadeIsOnlyTheGreens(t *testing.T) {
	t0 := time.Unix(1_800_000_000, 0)
	ground := captionPair{colorref(groundOf(false)), dwmColourDefault}
	green := captionPair{colorref(touchOf(false)), captionInk}
	if planFade(220, "ease", false, ground, green, false, true, t0) == nil {
		t.Fatal("the green's change is not eased")
	}
	for name, f := range map[string]*captionFade{
		"0 ms":                    planFade(0, "ease", false, ground, green, false, true, t0),
		"a negative duration":     planFade(-5, "ease", false, ground, green, false, true, t0),
		"the theme moved":         planFade(220, "ease", true, ground, green, false, true, t0),
		"DWM's caption, at first": planFade(220, "ease", false, captionPair{dwmColourDefault, dwmColourDefault}, green, false, true, t0),
		"DWM's caption, at last":  planFade(220, "ease", false, green, captionPair{dwmColourDefault, dwmColourDefault}, false, true, t0),
	} {
		if f != nil {
			t.Errorf("%s: eased, want at once", name)
		}
	}
	if f := planFade(60_000, "ease", false, ground, green, false, true, t0); f == nil || f.dur != fadeLongest {
		t.Errorf("a minute's fade was not held to %v", fadeLongest)
	}
	for _, g := range []struct {
		name       string
		paints     bool
		contrast   bool
		prevalence uint64
	}{
		{"Windows 10", false, false, 0},
		{"a contrast theme", true, true, 0},
		{"accent title bars", true, false, 1},
	} {
		for _, dark := range []bool{false, true} {
			up := captionPair{captionColour(dark, g.paints, g.contrast, g.prevalence, true), captionText(g.paints, g.contrast, g.prevalence, true)}
			down := captionPair{captionColour(dark, g.paints, g.contrast, g.prevalence, false), captionText(g.paints, g.contrast, g.prevalence, false)}
			if up != down {
				t.Errorf("%s (dark %v): the green moves the caption DWM draws", g.name, dark)
			}
			if planFade(220, "ease", false, down, up, dark, true, t0) != nil {
				t.Errorf("%s (dark %v): a fade on a caption DWM draws", g.name, dark)
			}
		}
	}
}

// fadeClock is a test's clock for the fader, moved by hand.
type fadeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fadeClock) get() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fadeClock) add(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// fadePaints records what a test's fades paint, in order.
type fadePaints struct {
	mu  sync.Mutex
	got []fadePaint
}

type fadePaint struct {
	fade  string
	c     captionPair
	frame bool
}

func (r *fadePaints) painter(name string) func(captionPair, bool) bool {
	return func(c captionPair, frame bool) bool {
		r.mu.Lock()
		r.got = append(r.got, fadePaint{name, c, frame})
		r.mu.Unlock()
		return true
	}
}

func (r *fadePaints) all() []fadePaint {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]fadePaint(nil), r.got...)
}

// waitFor polls cond for up to two seconds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("never: %s", what)
}

// TestTheFadeTurnsBackFromWhereItIs: a word from the page while a fade
// runs — a cancel mid-touch as the green comes — starts the new fade from
// the colours the caption shows at that moment, the first fade's then,
// never from either end; once the new one has started the old paints
// nothing more; the first frame of each and the last have DWM re-read the
// frame, the frames between only if fadeFrameChanged says so; the last
// frame is the look itself, and then the fader is still.
func TestTheFadeTurnsBackFromWhereItIs(t *testing.T) {
	clock := &fadeClock{now: time.Unix(1_800_000_000, 0)}
	paints := &fadePaints{}
	fd := &captionFader{invoke: func(fn func() bool) bool { return fn() }, now: clock.get}
	ground := captionPair{colorref(groundOf(false)), dwmColourDefault}
	green := captionPair{colorref(touchOf(false)), captionInk}

	in := planFade(220, "cubic-bezier(0.2, 0, 0, 1)", false, ground, green, false, true, clock.get())
	in.win, in.paint = 1, paints.painter("in")
	fd.start(in)
	waitFor(t, "the first frame", func() bool { return len(paints.all()) > 0 })
	if p := paints.all()[0]; p.c != in.from || !p.frame {
		t.Fatalf("the first frame painted %+v, want %+v with a frame change", p, in.from)
	}
	clock.add(100 * time.Millisecond)
	n := len(paints.all())
	waitFor(t, "a frame at 100 ms", func() bool { return len(paints.all()) > n+1 })

	shows, ok := fd.current(1, clock.get())
	if want, _ := in.at(clock.get()); !ok || shows != want {
		t.Fatalf("the caption shows %+v (%v), the fade's own at 100 ms is %+v", shows, ok, want)
	}
	if _, ok := fd.current(2, clock.get()); ok {
		t.Error("another window's caption is said to be fading")
	}
	out := planFade(70, "cubic-bezier(0.4, 0, 1, 1)", false, shows, ground, false, true, clock.get())
	out.win, out.paint = 1, paints.painter("out")
	fd.start(out)
	started := len(paints.all())
	waitFor(t, "the turned fade's first frame", func() bool {
		for _, p := range paints.all() {
			if p.fade == "out" {
				return true
			}
		}
		return false
	})
	got := paints.all()
	for _, p := range got[started:] {
		if p.fade == "in" {
			t.Fatal("the first fade painted after the second had started")
		}
	}
	var first *fadePaint
	for i := range got {
		if got[i].fade == "out" {
			first = &got[i]
			break
		}
	}
	if first.c != shows || !first.frame {
		t.Fatalf("the turned fade began at %+v, want %+v, where the caption was, with a frame change", first.c, shows)
	}
	for _, p := range got[1:] {
		if p.fade == "in" && p.frame != fadeFrameChanged {
			t.Errorf("a frame between the first and the last changed the frame: %+v", p)
		}
	}
	clock.add(70 * time.Millisecond)
	waitFor(t, "the last frame", func() bool {
		fd.mu.Lock()
		defer fd.mu.Unlock()
		return fd.run == nil
	})
	got = paints.all()
	if last := got[len(got)-1]; last.fade != "out" || last.c != ground || !last.frame {
		t.Fatalf("the last frame painted %+v, want the ground and DWM's text with a frame change", last)
	}
	if _, ok := fd.current(1, clock.get()); ok {
		t.Error("a fade is said to run after its last frame")
	}
	if fd.stop() {
		t.Error("stop found a fade after the last frame")
	}
}

// TestAStoppedFadePaintsNothing: a sync that sets the caption at once
// stops the fade first, and no frame of it is painted after; a window
// gone ends a fade at its next frame.
func TestAStoppedFadePaintsNothing(t *testing.T) {
	clock := &fadeClock{now: time.Unix(1_800_000_000, 0)}
	paints := &fadePaints{}
	fd := &captionFader{invoke: func(fn func() bool) bool { return fn() }, now: clock.get}
	ground := captionPair{colorref(groundOf(true)), dwmColourDefault}
	green := captionPair{colorref(touchOf(true)), captionInk}
	f := planFade(220, "ease", false, ground, green, true, true, clock.get())
	f.win, f.paint = 1, paints.painter("in")
	fd.start(f)
	waitFor(t, "the first frame", func() bool { return len(paints.all()) > 0 })
	if !fd.stop() {
		t.Fatal("stop found no fade running")
	}
	n := len(paints.all())
	time.Sleep(5 * fadeTick)
	if len(paints.all()) != n {
		t.Fatal("a stopped fade painted")
	}
	gone := planFade(220, "ease", false, ground, green, true, true, clock.get())
	gone.win, gone.paint = 1, func(captionPair, bool) bool { return false }
	fd.start(gone)
	waitFor(t, "the fade to end with its window", func() bool {
		fd.mu.Lock()
		defer fd.mu.Unlock()
		return fd.run == nil
	})
}

// TestTheFaderUnderRace runs the fader as the shell does, on the real
// ticker, its frames on one goroutine that stands for the main thread,
// while other goroutines start fades, stop them and read the caption at
// once, as the bound calls and the syncs would: go test -race finds no
// race, no two frames paint at once, and the fade started last ends on its
// own look.
func TestTheFaderUnderRace(t *testing.T) {
	mainThread := make(chan func())
	go func() {
		for fn := range mainThread {
			fn()
		}
	}()
	var busy, overlap atomic.Int32
	fd := &captionFader{invoke: func(fn func() bool) bool {
		done := make(chan bool)
		mainThread <- func() {
			if busy.Add(1) > 1 {
				overlap.Add(1)
			}
			r := fn()
			busy.Add(-1)
			done <- r
		}
		return <-done
	}}
	ground := captionPair{colorref(groundOf(false)), dwmColourDefault}
	green := captionPair{colorref(touchOf(false)), captionInk}
	var mu sync.Mutex
	var last captionPair
	paint := func(c captionPair, _ bool) bool {
		mu.Lock()
		last = c
		mu.Unlock()
		return true
	}
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				from, to := ground, green
				if (g+i)%2 == 1 {
					from, to = green, ground
				}
				if c, ok := fd.current(1, time.Now()); ok {
					from = c
				}
				switch i % 5 {
				case 4:
					fd.stop()
				default:
					f := planFade(30+i, "cubic-bezier(0.2, 0, 0, 1)", false, from, to, false, true, time.Now())
					f.win, f.paint = 1, paint
					fd.start(f)
				}
				time.Sleep(time.Duration(1+g) * time.Millisecond)
			}
		}(g)
	}
	wg.Wait()
	f := planFade(40, "cubic-bezier(0.4, 0, 1, 1)", false, green, ground, false, true, time.Now())
	f.win, f.paint = 1, paint
	fd.start(f)
	waitFor(t, "the last fade to end", func() bool {
		fd.mu.Lock()
		defer fd.mu.Unlock()
		return fd.run == nil
	})
	time.Sleep(3 * fadeTick) // any loop left over reaches its next frame and ends
	mu.Lock()
	defer mu.Unlock()
	if last != ground {
		t.Fatalf("the last fade ended on %+v, want the ground with DWM's text", last)
	}
	if overlap.Load() != 0 {
		t.Fatal("two frames painted at once")
	}
}

//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
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

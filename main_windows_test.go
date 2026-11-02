//go:build windows

package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
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

// TestTheGroundIsTheStylesheets: the window's BackgroundColour in each
// theme — what shows before the page first paints and where a resize
// uncovers the window — is the stylesheet's --ground, so that neither
// flashes another colour; a step that changes one changes both.
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
		decl := fmt.Sprintf("--ground: #%02x%02x%02x;", g.Red, g.Green, g.Blue)
		if !strings.Contains(css, decl) {
			t.Errorf("app.css declares no %q (dark %v)", decl, dark)
		}
	}
	if groundOf(true) == groundOf(false) {
		t.Fatal("one ground for both themes")
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
// moved since is caught up; a reload keeps what the shell has set since;
// and a window made again from the tray is a new window, untouched until
// its own load.
func TestTheCaptionWaitsForThePage(t *testing.T) {
	var c captionState
	for _, want := range []int32{captionLight, captionDark} {
		if c.touch(1, want) {
			t.Fatalf("a window whose page has not loaded is touched (want %d)", want)
		}
	}

	// Window 1 loads, made light.
	c.loaded(1, captionLight)
	if c.touch(1, captionLight) {
		t.Error("a window that wears the theme already is touched")
	}
	if !c.touch(1, captionDark) {
		t.Error("a loaded window made light, now resolving dark, is not caught up")
	}
	if c.touch(0, captionDark) {
		t.Error("no window (id 0) is touched")
	}

	// The shell sets it dark; a reload says light again, as it was made.
	c.wears = captionDark
	c.loaded(1, captionLight)
	if c.wears != captionDark {
		t.Error("a reload put back the theme the window was made in")
	}
	if c.touch(1, captionDark) {
		t.Error("a reload made the shell repaint a window that wears the theme")
	}

	// Closed to the tray and made again: window 2, made dark.
	if c.touch(2, captionLight) {
		t.Error("the new window is touched before its page has loaded")
	}
	c.loaded(2, captionDark)
	if c.touch(1, captionLight) {
		t.Error("the old window is still touched")
	}
	if c.touch(2, captionDark) || !c.touch(2, captionLight) {
		t.Error("the new window does not wear the theme it was made in")
	}

	// Made under a contrast theme, Wails sets no theme: whatever resolves
	// is applied at the load.
	c.loaded(3, captionUnknown)
	if !c.touch(3, captionLight) || !c.touch(3, captionDark) {
		t.Error("a window made under a contrast theme is not given a theme at its load")
	}
}

// TestTheBackdropRule is the switch's rule (APP.md §6, Controls: the
// ground): Mica only when ENFOLD_BACKDROP asks for it and this Windows
// draws the system backdrops (build 22621 and later), and the Solid window
// for everything else, since below 22621 Wails would lay a Windows 10
// blur, not a solid, under a translucent window.
func TestTheBackdropRule(t *testing.T) {
	for _, c := range []struct {
		asked     string
		supported bool
		want      backdrop
	}{
		{"mica", true, backdropMica},
		{"MICA", true, backdropMica},
		{" Mica ", true, backdropMica}, // a .cmd's `set ENFOLD_BACKDROP=Mica ` keeps the space
		{"mica", false, backdropSolid},
		{"", true, backdropSolid},
		{"", false, backdropSolid},
		{"solid", true, backdropSolid},
		{"acrylic", true, backdropSolid},
		{"tabbed", true, backdropSolid},
		{"mica2", true, backdropSolid},
		{"1", true, backdropSolid},
	} {
		if got := backdropFor(c.asked, c.supported); got != c.want {
			t.Errorf("backdropFor(%q, supported %v) = %s, want %s", c.asked, c.supported, got, c.want)
		}
	}
}

// TestTheWindowTellsThePageWhatItGot: the page learns the backdrop from the
// URL the shell makes the window at, so that URL says Mica exactly when the
// window is translucent over Mica, never on a request the shell turned
// down. The Solid window is the one made before the switch existed (the
// page at "/", a solid background, no backdrop, the opaque ground). Over
// Mica the window holds the same ground at alpha 0: under a translucent
// window the colour has no effect, and alpha 0 keeps any path that hands
// it to WebView2 from ever giving it an opaque ground under the material.
func TestTheWindowTellsThePageWhatItGot(t *testing.T) {
	for _, b := range []backdrop{backdropSolid, backdropMica} {
		for _, dark := range []bool{false, true} {
			l := b.look(dark)
			u, err := url.Parse(l.url)
			if err != nil {
				t.Fatalf("%s: the page's URL %q: %v", b, l.url, err)
			}
			if u.Path != "/" || u.IsAbs() {
				t.Errorf("%s: the page's URL %q is not the app's root", b, l.url)
			}
			saysMica := u.Query().Get("backdrop") == "mica"
			isMica := l.background == application.BackgroundTypeTranslucent && l.backdrop == application.Mica
			if saysMica != isMica || isMica != (b == backdropMica) {
				t.Errorf("%s: the URL says Mica %v, the window is translucent over Mica %v", b, saysMica, isMica)
			}
			g := groundOf(dark)
			if b == backdropSolid {
				want := windowLook{url: "/", background: application.BackgroundTypeSolid, backdrop: application.Auto, colour: g}
				if l != want {
					t.Errorf("the Solid window (dark %v) is %+v, want the window as it was, %+v", dark, l, want)
				}
				continue
			}
			if l.colour.Red != g.Red || l.colour.Green != g.Green || l.colour.Blue != g.Blue || l.colour.Alpha != 0 {
				t.Errorf("over Mica (dark %v) the window holds %+v, want the ground %+v at alpha 0", dark, l.colour, g)
			}
		}
	}
	if backdropMica.ground(true) != backdropMica.look(true).colour || backdropSolid.ground(false) != groundOf(false) {
		t.Fatal("the colour the window is made with is not the one a theme change sets (backdrop.ground)")
	}
}

// TestThePageReadsTheBackdropItIsGiven ties the shell's half to the page's
// (APP.md §6, Controls: the ground): main.ts reads the very parameter the
// shell's Mica URL carries and sets the attribute app.css's Mica block
// names; and that block is additive and stands outside a contrast theme —
// every rule in it names the attribute, and all of it is inside
// `@media not (forced-colors: active)`, since its body rule, (0,2,1),
// would otherwise beat the contrast block's Canvas ground, (0,0,1),
// wherever it stood.
func TestThePageReadsTheBackdropItIsGiven(t *testing.T) {
	u, err := url.Parse(backdropMica.look(false).url)
	if err != nil {
		t.Fatal(err)
	}
	if len(u.Query()) != 1 {
		t.Fatalf("the Mica URL carries %v, want the one parameter", u.Query())
	}
	var key, value string
	for k, v := range u.Query() {
		key, value = k, v[0]
	}
	ts, err := os.ReadFile(filepath.Join("frontend", "src", "main.ts"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		fmt.Sprintf("new URLSearchParams(location.search).get(%q) === %q", key, value),
		fmt.Sprintf("document.documentElement.dataset.backdrop = %q", value),
	} {
		if !strings.Contains(string(ts), want) {
			t.Errorf("main.ts has no %s", want)
		}
	}

	b, err := os.ReadFile(filepath.Join("frontend", "src", "app.css"))
	if err != nil {
		t.Fatal(err)
	}
	css := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(string(b), "")
	const guard = "@media not (forced-colors: active) {"
	attr := fmt.Sprintf("[data-backdrop=%q]", value)
	start := strings.Index(css, guard)
	if start < 0 || strings.Count(css, guard) != 1 {
		t.Fatalf("app.css has %d of %q, want the Mica block's one", strings.Count(css, guard), guard)
	}
	// The guard's own closing brace, by depth.
	end, depth := -1, 0
	for i := start + len(guard) - 1; i < len(css); i++ {
		switch css[i] {
		case '{':
			depth++
		case '}':
			depth--
		}
		if depth == 0 {
			end = i
			break
		}
	}
	if end < 0 {
		t.Fatal("the Mica block's guard is never closed")
	}
	block := css[start+len(guard) : end]
	if strings.Count(css, "data-backdrop") != strings.Count(block, "data-backdrop") {
		t.Error("app.css names data-backdrop outside the guarded Mica block")
	}
	rules := 0
	for _, m := range regexp.MustCompile(`([^{}]+)\{`).FindAllStringSubmatch(block, -1) {
		prelude := strings.TrimSpace(m[1])
		if strings.HasPrefix(prelude, "@media") {
			continue
		}
		rules++
		for _, sel := range strings.Split(prelude, ",") {
			if !strings.Contains(sel, attr) {
				t.Errorf("the Mica block's selector %q does not name %s", strings.TrimSpace(sel), attr)
			}
		}
	}
	for _, want := range []string{"body", ".layer", ".layer-foot"} {
		if !regexp.MustCompile(regexp.QuoteMeta(attr) + `\s+` + regexp.QuoteMeta(want) + `\s*\{`).MatchString(block) {
			t.Errorf("the Mica block has no rule for %s", want)
		}
	}
	if rules == 0 {
		t.Fatal("the Mica block has no rules")
	}
}

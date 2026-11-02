package brand

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The identity drawings (fork D, phase 5; frontend/src/components/Art.svelte)
// are static markup that a person can edit by hand, as the sprite's i-mark
// is, so this file holds them to the geometry they come from: the lock
// stage's mark is Sheet(96) cropped to 96 x 72, the first run's is
// Sheet(128), and the no-archives state's open box is built below on the
// sheet's grid, slope and strokes. When a test here fails it prints the
// markup the geometry draws, which is what goes into Art.svelte.

// artPart is one path of a drawing: its class, which gives it its paint in
// app.css (and the system's colours in a contrast theme), the stroke's
// width for a line (0 for a fill, which has no stroke), and its data.
type artPart struct {
	class string
	width float64
	d     string
}

// art is one drawing: the name Art.svelte carries as data-art, its box in
// CSS px, and its paths in painting order.
type art struct {
	name          string
	width, height float64
	parts         []artPart
}

// sheetArt draws the folded sheet as fork D does: the wash, the flap over
// it, the crease and the writing in the thin weight, and last the outline
// and the folds in the main one, which would otherwise lose their inner
// half under the flap's fill.
func sheetArt(name string, f Figure) art {
	return art{name: name, width: f.Width, height: f.Height, parts: []artPart{
		{"art-fill", 0, f.Outline()},
		{"art-flap", 0, f.Flap()},
		{"art-crease", f.Thin, f.Crease()},
		{"art-thin", f.Thin, f.Writing()},
		{"art-line", f.Stroke, f.Lines()},
	}}
}

// boxFigure is the no-archives state's drawing: the archive's own glyph,
// the sprite's i-box, drawn large and open (design-identity.md 1.2, art.py's
// openbox in the phase-5 study). It names what is missing, an archive.
type boxFigure struct {
	Width, Height, Stroke, Thin float64
	// The rim, an isometric rhombus: its back, right, front and left
	// corners.
	Back, Right, Front, Left Point
	// The walls' bottom corners under the rim's left, front and right ones.
	LeftFoot, FrontFoot, RightFoot Point
	// The two back flaps, standing out from the rim's back edges, each from
	// the rim's corner round to its outer tip.
	FlapLeft, FlapRight [4]Point
	// The inside's back corner, drawn thin from the rim's back corner down.
	CornerFoot Point
}

// openBox fits the open box to the 72 px box of the empty state. It shares
// the sheet's numbers where it can: its three vertical lines (both walls'
// outer edges and the front corner) on the 0.8 px grid, so the 1.6 px main
// stroke covers two whole device pixels at 125 %; its rim on the sheet's
// 30 degree slope, exactly, where the study's drawing had tan 30 to four
// places; and the sheet's two strokes at this size. Its proportions are the
// study's, worked in whole device pixels at 125 %, as Sheet works its own:
// the front corner at 45 (36 CSS px), the half-width 29 (23.2), the rim's
// side corners 38 down (30.4) and the walls 28 tall (22.4).
func openBox() boxFigure {
	css := func(dev int) float64 { return float64(4*dev) / 5 }
	xm, a, yu, h := css(45), css(29), css(38), css(28)
	rise := a / math.Sqrt(3)
	// The flaps lean out by the study's 7.2 across and 10.4 up.
	fx, fy := 7.2, 10.4
	s := Sheet(72)
	b := boxFigure{Width: 72, Height: 72, Stroke: s.Stroke, Thin: s.Thin}
	b.Back, b.Right, b.Front, b.Left = Point{xm, yu - rise}, Point{xm + a, yu}, Point{xm, yu + rise}, Point{xm - a, yu}
	b.LeftFoot, b.FrontFoot, b.RightFoot = Point{b.Left.X, b.Left.Y + h}, Point{xm, b.Front.Y + h}, Point{b.Right.X, b.Right.Y + h}
	b.FlapLeft = [4]Point{b.Left, b.Back, {b.Back.X - fx, b.Back.Y - fy}, {b.Left.X - fx, b.Left.Y - fy}}
	b.FlapRight = [4]Point{b.Back, b.Right, {b.Right.X + fx, b.Right.Y - fy}, {b.Back.X + fx, b.Back.Y - fy}}
	// The back corner seen through the open top, 0.62 of a wall down: in
	// this projection it would meet the rim's front corner at the floor.
	b.CornerFoot = Point{xm, b.Back.Y + h*0.62}
	return b
}

func (b boxFigure) flaps() string {
	return path(b.FlapLeft[:]...) + "Z" + path(b.FlapRight[:]...) + "Z"
}

func (b boxFigure) walls() string {
	return path(b.Left, b.Front, b.FrontFoot, b.LeftFoot) + "Z" + path(b.Front, b.Right, b.RightFoot, b.FrontFoot) + "Z"
}

// boxArt draws the open box: the flaps in the paper, the inside in the
// surface's alternate, the walls in the wash, the inside's corner in the
// thin weight, and every outline over them in the main one.
func boxArt() art {
	b := openBox()
	return art{name: "box", width: b.Width, height: b.Height, parts: []artPart{
		{"art-paper", 0, b.flaps()},
		{"art-inside", 0, path(b.Left, b.Back, b.Right, b.Front) + "Z"},
		{"art-fill", 0, b.walls()},
		{"art-thin", b.Thin, path(b.Back, b.CornerFoot)},
		{"art-line", b.Stroke, b.flaps() + b.walls()},
	}}
}

// arts is every drawing Art.svelte holds, by its data-art name.
func arts() []art {
	return []art{
		sheetArt("stage", Sheet(96).Cropped(72)),
		sheetArt("first", Sheet(128)),
		boxArt(),
	}
}

// markup is the drawing as Art.svelte writes it, with no whitespace between
// tags, as normalMarkup leaves the file's.
func (a art) markup() string {
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="idart" data-art="%s" width="%s" height="%s" viewBox="0 0 %s %s" aria-hidden="true">`,
		a.name, num(a.width), num(a.height), num(a.width), num(a.height))
	for _, p := range a.parts {
		if p.width > 0 {
			fmt.Fprintf(&b, `<path class="%s" stroke-width="%s" d="%s"/>`, p.class, num(p.width), p.d)
		} else {
			fmt.Fprintf(&b, `<path class="%s" d="%s"/>`, p.class, p.d)
		}
	}
	b.WriteString(`</svg>`)
	return b.String()
}

// The drift guard: every <svg> in Art.svelte is one of the drawings above,
// whole, element for element and attribute for attribute, whitespace aside;
// each drawing is there exactly once; and nothing else is drawn there, so a
// fourth drawing cannot join the file without joining this test. Comparing
// the whole element, not just the path data, catches what would move a
// vertical line off its device pixels without touching the data: a
// transform, a viewBox that no longer matches the box, a stroke's width.
func TestArtIsSheet(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "frontend", "src", "components", "Art.svelte"))
	if err != nil {
		t.Fatal(err)
	}
	// The script's comments and the markup's own are prose, which may name
	// an <svg> without drawing one.
	src = regexp.MustCompile(`(?s)<script\b.*?</script>|<!--.*?-->`).ReplaceAll(src, nil)
	found := regexp.MustCompile(`(?s)<svg\b.*?</svg>`).FindAll(src, -1)
	name := regexp.MustCompile(`\bdata-art="([^"]*)"`)
	want := map[string]string{}
	var all []string
	for _, a := range arts() {
		want[a.name] = a.markup()
		all = append(all, a.markup())
	}
	seen := map[string]int{}
	for _, f := range found {
		got := normalMarkup(string(f))
		m := name.FindStringSubmatch(got)
		if m == nil || want[m[1]] == "" {
			t.Fatalf("Art.svelte draws an <svg> that is none of the guarded drawings:\n%s\nthe drawings are:\n%s", got, strings.Join(all, "\n"))
		}
		seen[m[1]]++
		if got != want[m[1]] {
			t.Fatalf("Art.svelte's %q is not the geometry's:\n got %s\nwant %s", m[1], got, want[m[1]])
		}
	}
	for n, w := range want {
		if seen[n] != 1 {
			t.Fatalf("Art.svelte draws %q %d times, want once:\n%s", n, seen[n], w)
		}
	}
}

// The markup the geometry draws, logged for pasting into Art.svelte:
// go test -v -run TestArtMarkup ./internal/brand.
func TestArtMarkup(t *testing.T) {
	for _, a := range arts() {
		t.Logf("%s:\n%s", a.name, a.markup())
	}
}

// The open box keeps the sheet's rules: its vertical lines stand on whole
// device pixels at 125 %, its rim runs at 30 degrees either side of the
// horizontal, its walls are vertical and as tall as each other, and the
// whole of it, strokes and round joins included, fits its box.
func TestOpenBox(t *testing.T) {
	b := openBox()
	if b.Stroke != 1.6 || b.Thin != 1.2 {
		t.Fatalf("strokes %v and %v, want the sheet's 1.6 and 1.2", b.Stroke, b.Thin)
	}
	for _, x := range []float64{b.Left.X, b.Front.X, b.Right.X, b.CornerFoot.X} {
		if dev := x * 1.25; !near(dev, math.Round(dev), 1e-9) {
			t.Fatalf("a vertical line at %v CSS px is %v device px", x, dev)
		}
	}
	for _, e := range [][2]Point{{b.Left, b.Back}, {b.Back, b.Right}, {b.Right, b.Front}, {b.Front, b.Left}} {
		deg := math.Mod(math.Atan2(e[1].Y-e[0].Y, e[1].X-e[0].X)*180/math.Pi+360, 180)
		if !near(deg, 30, 1e-9) && !near(deg, 150, 1e-9) {
			t.Fatalf("a rim edge from %v to %v runs at %v degrees", e[0], e[1], deg)
		}
	}
	if b.LeftFoot.X != b.Left.X || b.RightFoot.X != b.Right.X || b.FrontFoot.X != b.Front.X ||
		!near(b.LeftFoot.Y-b.Left.Y, b.FrontFoot.Y-b.Front.Y, 1e-9) || !near(b.RightFoot.Y-b.Right.Y, b.FrontFoot.Y-b.Front.Y, 1e-9) {
		t.Fatalf("the walls are not vertical or not equal: %+v", b)
	}
	m := b.Stroke / 2
	pts := append(append([]Point{b.LeftFoot, b.FrontFoot, b.RightFoot, b.CornerFoot}, b.FlapLeft[:]...), b.FlapRight[:]...)
	for _, p := range pts {
		if p.X-m < 0 || p.X+m > b.Width || p.Y-m < 0 || p.Y+m > b.Height {
			t.Fatalf("%v with its stroke leaves the box %v x %v", p, b.Width, b.Height)
		}
	}
	// The path data is the points: the walls' outline reads back as the
	// rim's three near corners and the feet under them.
	walls := parsePath(t, b.walls())
	if len(walls) != 2 || len(walls[0]) != 4 || len(walls[1]) != 4 {
		t.Fatalf("the walls are %q", b.walls())
	}
}

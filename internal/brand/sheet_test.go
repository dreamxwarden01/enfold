package brand

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// sizes is every box the tests fit the sheet to: each whole size from the
// smallest Sheet takes to past the first run's 128, so a rule that holds
// only at the sizes in use today is caught.
func sizes() []int {
	var s []int
	for n := 16; n <= 160; n++ {
		s = append(s, n)
	}
	return s
}

func (f Figure) hexagon() []Point {
	return []Point{f.Top, f.UpperRight, f.LowerRight, f.Bottom, f.LowerLeft, f.UpperLeft}
}

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

// parsePath reads back the path data the figure writes ("M x y x y ... Z",
// any number of subpaths) as its subpaths' points. It knows that form only;
// anything else fails the test.
func parsePath(t *testing.T, d string) [][]Point {
	t.Helper()
	var out [][]Point
	for _, sub := range strings.Split(d, "M")[1:] {
		sub = strings.TrimSuffix(sub, "Z")
		f := strings.Fields(sub)
		if len(f)%2 != 0 || len(f) < 4 {
			t.Fatalf("path %q: subpath %q is not pairs of numbers", d, sub)
		}
		var pts []Point
		for i := 0; i < len(f); i += 2 {
			x, err1 := strconv.ParseFloat(f[i], 64)
			y, err2 := strconv.ParseFloat(f[i+1], 64)
			if err1 != nil || err2 != nil {
				t.Fatalf("path %q: %q %q are not numbers", d, f[i], f[i+1])
			}
			pts = append(pts, Point{x, y})
		}
		out = append(out, pts)
	}
	if len(out) == 0 && d != "" {
		t.Fatalf("path %q has no subpath", d)
	}
	return out
}

// The hexagon is regular: six equal edges, six 120 degree corners, and its
// vertical sides and the top-to-bottom axis are vertical.
func TestSheetRegular(t *testing.T) {
	for _, n := range sizes() {
		f := Sheet(n)
		h := f.hexagon()
		edge := math.Hypot(h[1].X-h[0].X, h[1].Y-h[0].Y)
		for i := range h {
			p, q, r := h[i], h[(i+1)%6], h[(i+2)%6]
			if l := math.Hypot(q.X-p.X, q.Y-p.Y); !near(l, edge, 1e-9*edge) {
				t.Fatalf("size %d: edge %d is %v long, edge 0 is %v", n, i, l, edge)
			}
			ux, uy, vx, vy := p.X-q.X, p.Y-q.Y, r.X-q.X, r.Y-q.Y
			cos := (ux*vx + uy*vy) / (math.Hypot(ux, uy) * math.Hypot(vx, vy))
			if !near(cos, -0.5, 1e-9) {
				t.Fatalf("size %d: the corner at vertex %d is %v degrees", n, (i+1)%6, math.Acos(cos)*180/math.Pi)
			}
		}
		if f.UpperLeft.X != f.LowerLeft.X || f.UpperRight.X != f.LowerRight.X || f.Top.X != f.Bottom.X {
			t.Fatalf("size %d: a vertical line is not vertical: %+v", n, f)
		}
		if !near(f.UpperRight.X-f.UpperLeft.X, 2*f.A, 1e-9) {
			t.Fatalf("size %d: width %v, half-width %v", n, f.UpperRight.X-f.UpperLeft.X, f.A)
		}
	}
}

// F is the hexagon's exact centre, which makes the diagonal one straight
// line through it; and the hexagon stands in the box's centre: exactly so
// vertically, and within half a grid step across, the price of the grid.
func TestSheetCentredF(t *testing.T) {
	for _, n := range sizes() {
		f := Sheet(n)
		var cx, cy float64
		for _, p := range f.hexagon() {
			cx, cy = cx+p.X/6, cy+p.Y/6
		}
		if !near(f.F.X, cx, 1e-9) || !near(f.F.Y, cy, 1e-9) {
			t.Fatalf("size %d: F %v is not the centre %v %v", n, f.F, cx, cy)
		}
		mid := Point{(f.UpperLeft.X + f.LowerRight.X) / 2, (f.UpperLeft.Y + f.LowerRight.Y) / 2}
		if !near(f.F.X, mid.X, 1e-9) || !near(f.F.Y, mid.Y, 1e-9) {
			t.Fatalf("size %d: the diagonal bends at F: %v against the midpoint %v", n, f.F, mid)
		}
		if f.F.Y != float64(n)/2 || math.Abs(f.F.X-float64(n)/2) > Grid/2+1e-9 {
			t.Fatalf("size %d: F %v is off the box's centre", n, f.F)
		}
	}
}

// Both sides and the top fold stand on whole device pixels at 125 %, so a
// 1.6 px stroke is two full device columns.
func TestSheetOnGrid(t *testing.T) {
	for _, n := range sizes() {
		f := Sheet(n)
		for _, x := range []float64{f.UpperLeft.X, f.F.X, f.UpperRight.X} {
			dev := x * 1.25
			if !near(dev, math.Round(dev), 1e-9) {
				t.Fatalf("size %d: a vertical line at %v CSS px is %v device px", n, x, dev)
			}
		}
	}
}

// Every segment the figure draws, its fills' edges included, runs in one of
// three directions: vertical, or 30 degrees above or below the horizontal.
// The directions are read from the path data as written, to a thousandth of
// a px, so the tolerance allows that rounding and nothing more.
func TestSheetThreeDirections(t *testing.T) {
	for _, n := range sizes() {
		f := Sheet(n)
		used := map[int]bool{}
		for _, d := range []string{f.Lines(), f.Flap(), f.Crease(), f.Writing()} {
			for _, sub := range parsePath(t, d) {
				if len(sub) > 2 {
					// The hexagon and the flap, the figure's only subpaths
					// of more than two points, are closed: their last
					// segment runs back to the start.
					sub = append(sub, sub[0])
				}
				for i := 1; i < len(sub); i++ {
					p, q := sub[i-1], sub[i]
					if p == q {
						continue
					}
					deg := math.Mod(math.Atan2(q.Y-p.Y, q.X-p.X)*180/math.Pi+360, 180)
					ok := false
					for _, want := range []int{30, 90, 150} {
						if near(deg, float64(want), 0.05) {
							used[want], ok = true, true
						}
					}
					if !ok {
						t.Fatalf("size %d: a segment of %q runs at %v degrees", n, d, deg)
					}
				}
			}
		}
		if len(used) != 3 {
			t.Fatalf("size %d: the figure uses the directions %v", n, used)
		}
	}
}

// The crease and the writing are drawn from 48 px up, and only there; the
// crease runs from the upper-right vertex to F, parallel to the upper-left
// edge; and the stroke is the sprite's 1.5 below 48 and the drawings' 1.6
// from it. CreaseFrom itself is held to 48, since the loop below reads it:
// lowered to 24, it would put a crease on the rail's mark and the loop would
// still pass.
func TestSheetCreaseFrom48(t *testing.T) {
	if CreaseFrom != 48 {
		t.Fatalf("CreaseFrom is %d, want 48", CreaseFrom)
	}
	for _, n := range sizes() {
		f := Sheet(n)
		if n < CreaseFrom {
			if f.Crease() != "" || f.Writing() != "" || f.Thin != 0 || f.Stroke != 1.5 {
				t.Fatalf("size %d: crease %q, writing %q, thin %v, stroke %v", n, f.Crease(), f.Writing(), f.Thin, f.Stroke)
			}
			continue
		}
		if f.Thin != 1.2 || f.Stroke != 1.6 {
			t.Fatalf("size %d: thin %v, stroke %v", n, f.Thin, f.Stroke)
		}
		c := parsePath(t, f.Crease())
		if len(c) != 1 || len(c[0]) != 2 || !near(c[0][0].X, f.UpperRight.X, 5e-4) || !near(c[0][0].Y, f.UpperRight.Y, 5e-4) ||
			!near(c[0][1].X, f.F.X, 5e-4) || !near(c[0][1].Y, f.F.Y, 5e-4) {
			t.Fatalf("size %d: the crease is %q", n, f.Crease())
		}
		cross := (f.F.X-f.UpperRight.X)*(f.Top.Y-f.UpperLeft.Y) - (f.F.Y-f.UpperRight.Y)*(f.Top.X-f.UpperLeft.X)
		if !near(cross, 0, 1e-9*f.A*f.A) {
			t.Fatalf("size %d: the crease is not parallel to the upper-left edge (cross %v)", n, cross)
		}
		if w := parsePath(t, f.Writing()); len(w) != 3 {
			t.Fatalf("size %d: %d lines of writing", n, len(w))
		}
	}
}

// The path data is the vertices: each path reads back as the points it
// names, within the thousandth it is rounded to.
func TestSheetPathsAreTheVertices(t *testing.T) {
	same := func(t *testing.T, n int, what string, got [][]Point, want ...[]Point) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("size %d: %s has %d subpaths, want %d", n, what, len(got), len(want))
		}
		for i := range want {
			if len(got[i]) != len(want[i]) {
				t.Fatalf("size %d: %s subpath %d has %d points, want %d", n, what, i, len(got[i]), len(want[i]))
			}
			for j := range want[i] {
				if !near(got[i][j].X, want[i][j].X, 5e-4) || !near(got[i][j].Y, want[i][j].Y, 5e-4) {
					t.Fatalf("size %d: %s point %d.%d is %v, want %v", n, what, i, j, got[i][j], want[i][j])
				}
			}
		}
	}
	for _, n := range sizes() {
		f := Sheet(n)
		hex := []Point{f.UpperLeft, f.Top, f.UpperRight, f.LowerRight, f.Bottom, f.LowerLeft}
		same(t, n, "Outline", parsePath(t, f.Outline()), hex)
		same(t, n, "Flap", parsePath(t, f.Flap()), []Point{f.Top, f.UpperRight, f.F})
		same(t, n, "Folds", parsePath(t, f.Folds()), []Point{f.Top, f.F}, []Point{f.UpperLeft, f.LowerRight})
		same(t, n, "Lines", parsePath(t, f.Lines()), hex, []Point{f.Top, f.F}, []Point{f.UpperLeft, f.LowerRight})
		if !strings.HasSuffix(f.Outline(), "Z") || !strings.HasSuffix(f.Flap(), "Z") {
			t.Fatalf("size %d: a fill's outline is not closed", n)
		}
	}
}

// The figure and its strokes fit their box, at every size and in the
// cropped boxes the drawings use, and the writing stays on the sheet's front,
// clear of the outline and the diagonal. The stroke reaches past a vertex by
// half its width, as round joins draw it.
func TestSheetFits(t *testing.T) {
	inside := func(t *testing.T, what string, f Figure) {
		t.Helper()
		m := f.Stroke / 2
		for _, p := range f.hexagon() {
			if p.X-m < 0 || p.X+m > f.Width || p.Y-m < 0 || p.Y+m > f.Height {
				t.Fatalf("%s: %v with its stroke leaves the box %v x %v", what, p, f.Width, f.Height)
			}
		}
		if f.Writing() == "" {
			return
		}
		// The front is the quadrilateral below the diagonal: upper left,
		// lower right, bottom, lower left, clockwise on screen. A point is
		// on it, clear of every edge by both strokes' halves, when it lies
		// that far to the right of each edge (y grows downwards).
		front := []Point{f.UpperLeft, f.LowerRight, f.Bottom, f.LowerLeft}
		clear := (f.Stroke + f.Thin) / 2
		for _, sub := range parsePath(t, f.Writing()) {
			for _, p := range sub {
				for i := range front {
					a, b := front[i], front[(i+1)%4]
					l := math.Hypot(b.X-a.X, b.Y-a.Y)
					if d := ((b.X-a.X)*(p.Y-a.Y) - (b.Y-a.Y)*(p.X-a.X)) / l; d < clear {
						t.Fatalf("%s: the writing's point %v is %v from edge %d of the front, want %v", what, p, d, i, clear)
					}
				}
			}
		}
	}
	for _, n := range sizes() {
		inside(t, "size "+strconv.Itoa(n), Sheet(n))
	}
	for _, c := range []struct {
		size   int
		height float64
	}{{96, 72}, {128, 96}} {
		f, g := Sheet(c.size), Sheet(c.size).Cropped(c.height)
		inside(t, "size "+strconv.Itoa(c.size)+" cropped", g)
		if g.Width != f.Width || g.Height != c.height || g.F.X != f.F.X || !near(g.F.Y, c.height/2, 1e-9) {
			t.Fatalf("size %d cropped to %v: %+v", c.size, c.height, g)
		}
	}
}

// At 96 px the sheet is fork D's D2 (scratchpad p5-study, art.py d2) with F
// moved to the centre, as the judge ruled: the same three columns, and every
// height within the 0.05 px by which D2 snapped its sides to the grid and so
// fell short of regular; F itself moves from 47.2 to 48.
func TestSheetKeepsD2(t *testing.T) {
	f := Sheet(96)
	if f.UpperLeft.X != 17.6 || f.F.X != 48 || f.UpperRight.X != 78.4 {
		t.Fatalf("columns %v %v %v, D2's are 17.6 48 78.4", f.UpperLeft.X, f.F.X, f.UpperRight.X)
	}
	for _, c := range []struct {
		got, d2 float64
	}{{f.Top.Y, 12.85}, {f.UpperRight.Y, 30.4}, {f.LowerRight.Y, 65.6}, {f.Bottom.Y, 83.15}} {
		if !near(c.got, c.d2, 0.05) {
			t.Fatalf("a height is %v, D2's is %v", c.got, c.d2)
		}
	}
	if f.F.Y != 48 {
		t.Fatalf("F at %v", f.F.Y)
	}
	t.Logf("Sheet(96): outline %s; flap %s; folds %s; crease %s; writing %s", f.Outline(), f.Flap(), f.Folds(), f.Crease(), f.Writing())
	c := f.Cropped(72)
	t.Logf("Sheet(96).Cropped(72): outline %s; flap %s; folds %s; crease %s; writing %s", c.Outline(), c.Flap(), c.Folds(), c.Crease(), c.Writing())
	g := Sheet(128)
	t.Logf("Sheet(128): outline %s; flap %s; folds %s; crease %s; writing %s", g.Outline(), g.Flap(), g.Folds(), g.Crease(), g.Writing())
}

// The drift guard: the sprite's i-mark (frontend/src/components/Icons.svelte)
// is Sheet(20), element for element. The sprite is markup that a person edits by
// hand, so without this test a later touch to the symbol, or to Sheet, would
// leave the window's mark and the drawings drawn from Sheet two figures
// again, which is what phase 5 set out to end.
//
// The test compares the whole symbol, its tag, every element and every
// attribute, with the one Sheet(20) draws, and lets only whitespace differ.
// Reading just the paths' data would pass a transform or a stroke width on a
// path, a group wrapped around the paths, or an extra element, and a nudge
// is a likely edit: the figure stands half a device pixel left of its box's
// centre by construction, and a translate to centre it would take its
// vertical lines off the device pixels they are drawn on.
func TestSpriteMarkIsSheet20(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "frontend", "src", "components", "Icons.svelte"))
	if err != nil {
		t.Fatal(err)
	}
	f := Sheet(20)
	symbol := `<symbol id="i-mark" viewBox="0 0 20 20">`
	for _, w := range [][2]string{{"mark-fill", f.Outline()}, {"mark-flap", f.Flap()}, {"mark-line", f.Lines()}} {
		symbol += fmt.Sprintf(`<path class="%s" d="%s"/>`, w[0], w[1])
	}
	symbol += `</symbol>`

	// Every symbol with the id i-mark is found, whatever else its tag holds:
	// with two, a use of #i-mark would draw whichever comes first, so the
	// test must not pass on the other.
	found := regexp.MustCompile(`(?s)<symbol\b[^>]*\bid="i-mark"[^>]*>.*?</symbol>`).FindAll(src, -1)
	if len(found) != 1 {
		t.Fatalf("Icons.svelte has %d i-mark symbols, want 1; Sheet(20) draws it as:\n%s", len(found), symbol)
	}
	if got := normalMarkup(string(found[0])); got != symbol {
		t.Fatalf("i-mark is not Sheet(20)'s symbol:\n got %s\nwant %s", got, symbol)
	}
}

// normalMarkup removes the whitespace that markup is free to vary: a run of
// it becomes one space, and none is kept beside a tag's angle brackets, so
// the symbol may be laid out on one line or several and still compare.
func normalMarkup(s string) string {
	s = regexp.MustCompile(`\s+`).ReplaceAllString(s, " ")
	s = regexp.MustCompile(` ?(/?>) ?`).ReplaceAllString(s, "$1")
	return regexp.MustCompile(` <`).ReplaceAllString(s, "<")
}

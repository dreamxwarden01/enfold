package brand

import (
	"math"
	"strconv"
	"strings"
)

// The mark's one geometry, the folded sheet (phase 5, ruled 2026-11-01).
// Every rendering of the mark comes from Sheet, so that no two of them can
// drift apart: the sprite's 20 px i-mark (frontend/src/components/
// Icons.svelte, held to Sheet(20) by sheet_test.go), the 96 and 128 px
// drawings of the lock stage and the first run and the no-archives open box
// (Art.svelte, held to Sheet by art_test.go), and the application's icon,
// which icon.go draws from Sheet in facets.
//
// The figure is a regular pointy-top hexagon, the sheet, with its top corner
// folded down to the centre F. Two folds show: from the top vertex down to
// F, and the straight diagonal from the upper-left vertex through F to the
// lower-right one. The flap, the sheet's back turned over, is the triangle
// of the top vertex, the upper-right vertex and F. Because F is the
// hexagon's exact centre (fork D's demo had it a unit above, which bent its
// diagonal by about 3 degrees), every edge and every line runs in one of
// three directions, vertical or 30 degrees either side of the horizontal,
// and the figure reads as one folded sheet rather than a sketch of one.

// Grid is one device pixel at the owner's 125 %, in CSS px. The figure's
// vertical lines (both sides and the fold from the top) are centred on
// multiples of it: a 1.6 px stroke there covers exactly two device pixels
// and a 1.5 px one two at 94 %, where a line between two grid lines smears
// over three. The slanted lines and the vertices' heights cannot be put on
// any grid, and need not be: a slanted edge is antialiased wherever it lies,
// and the figure has no horizontal one.
const Grid = 0.8

// CreaseFrom is the size, in CSS px, from which the figure draws its
// detail: the thin crease from the upper-right vertex to F, which draws the
// flap's fold, where the flap's fill meets the wash at only 1.15:1 in light
// and 1.58:1 in dark, and the lines of writing on the sheet's front. Below
// it there is no room for either between the 1.5 px lines.
const CreaseFrom = 48

// Point is a position in a drawing's box, in CSS px from its top-left corner.
type Point struct{ X, Y float64 }

// Figure is the folded sheet fitted to one box: its seven points, and the
// widths of the strokes it is drawn with.
type Figure struct {
	// Width and Height are the box's, in CSS px: Sheet's square, or a box
	// as wide and less tall after Cropped.
	Width, Height float64
	// A is the hexagon's half-width, from F to either vertical side.
	A float64
	// Stroke is the main line's width: the outline and the folds. It is 1.5
	// below CreaseFrom, the weight of every glyph in the sprite (.i in
	// app.css), and 1.6 from it, two whole device pixels at 125 %, for the
	// drawings at their size.
	Stroke float64
	// Thin is the width of the crease and the lines of writing, 1.2 from
	// CreaseFrom; below it neither is drawn and Thin is 0.
	Thin float64
	// The hexagon's six vertices, clockwise from the top, and the fold
	// vertex F at its centre.
	Top, UpperRight, LowerRight, Bottom, LowerLeft, UpperLeft, F Point
}

// Sheet fits the folded sheet to a square box of size CSS px (16 or more).
// It keeps the fit of fork D's 96 px drawing (D2 in the phase-5 study) at
// every size: the half-width is 30.4 of 96, put on the grid, and F stands on
// the grid line nearest the box's centre, so both sides and the top fold
// fall on whole device pixels. Where the centre falls exactly between two
// grid lines (a 20 px box is 25 device px wide), the figure takes the left
// one and stands half a device pixel left of centre, as the study's 20 px
// sheet does. Vertically nothing needs a grid, so F is the box's centre.
func Sheet(size int) Figure {
	if size < 16 {
		panic("brand: Sheet needs a box of 16 px or more")
	}
	// Device px at 125 % are CSS px times 5/4. Both values are worked out
	// in whole device px, with integers, so a tie is decided by the rule
	// and not by how 0.8 rounds in binary: the half-width is 30.4/96 of the
	// box (19/48 of its CSS size in device px), rounded half up; the centre
	// column is the nearest to 5/8 of the size, the lower on a tie.
	aDev := (19*size + 24) / 48
	cDev := (5*size + 3) / 8
	css := func(dev int) float64 { return float64(4*dev) / 5 }
	a, cx, cy := css(aDev), css(cDev), float64(size)/2
	s := a / math.Sqrt(3) // the rise of a 30 degree edge over a run of a
	f := Figure{
		Width: float64(size), Height: float64(size), A: a,
		Stroke: 1.5,
		Top:    Point{cx, cy - 2*s}, UpperRight: Point{cx + a, cy - s}, LowerRight: Point{cx + a, cy + s},
		Bottom: Point{cx, cy + 2*s}, LowerLeft: Point{cx - a, cy + s}, UpperLeft: Point{cx - a, cy - s},
		F: Point{cx, cy},
	}
	if size >= CreaseFrom {
		f.Stroke, f.Thin = 1.6, 1.2
	}
	return f
}

// Cropped returns the figure in a box as wide and height CSS px tall, the
// figure centred in it. A drawing's square box holds empty bands above and
// below the hexagon, and the lock stage's 96 px mark is cropped to 96 x 72
// to give that room back to the panel. The crop moves the figure up only:
// its vertical lines keep their columns, so it needs no grid.
func (f Figure) Cropped(height float64) Figure {
	dy := (height - f.Height) / 2
	for _, p := range []*Point{&f.Top, &f.UpperRight, &f.LowerRight, &f.Bottom, &f.LowerLeft, &f.UpperLeft, &f.F} {
		p.Y += dy
	}
	f.Height = height
	return f
}

// Outline is the hexagon's path data, closed, from the upper-left vertex:
// the wash's fill, and the first part of the main line.
func (f Figure) Outline() string {
	return path(f.UpperLeft, f.Top, f.UpperRight, f.LowerRight, f.Bottom, f.LowerLeft) + "Z"
}

// Flap is the path data of the flap, the triangle of the top vertex, the
// upper-right vertex and F, filled with --mark-flap.
func (f Figure) Flap() string {
	return path(f.Top, f.UpperRight, f.F) + "Z"
}

// Folds is the path data of the two folds: the top vertex down to F, and
// the diagonal from the upper-left vertex through F to the lower-right one.
func (f Figure) Folds() string {
	return path(f.Top, f.F) + path(f.UpperLeft, f.LowerRight)
}

// Lines is the path data of everything the main stroke draws, the outline
// and the folds, in the accent. It is drawn after the flap's fill, which
// would otherwise cover the inner half of the outline along the flap's edge.
func (f Figure) Lines() string {
	return f.Outline() + f.Folds()
}

// Crease is the path data of the thin accent line from the upper-right
// vertex to F, parallel to the upper-left edge, or "" below CreaseFrom.
func (f Figure) Crease() string {
	if f.Thin == 0 {
		return ""
	}
	return path(f.UpperRight, f.F)
}

// Writing is the path data of the three lines of writing on the sheet's
// front, below the diagonal and parallel to it, or "" below CreaseFrom. They
// are fork D's (D2): set in from the left side by 8 px of 96, the first 8.8
// below the diagonal and the next two 7.2 apart, ending 4.8 short of the top
// fold's column, and the last one 60 % as long, as a paragraph's last line is.
// They are drawn in the thin weight in --ink-3.
func (f Figure) Writing() string {
	if f.Thin == 0 {
		return ""
	}
	k := f.Width / 96
	slope := (f.F.Y - f.UpperLeft.Y) / (f.F.X - f.UpperLeft.X)
	inset := 8 * k
	run := f.A - inset - 4.8*k
	x := f.UpperLeft.X + inset
	y := f.UpperLeft.Y + slope*inset
	var b strings.Builder
	for i := range 3 {
		dy := (8.8 + 7.2*float64(i)) * k
		r := run
		if i == 2 {
			r = run * 0.6
		}
		b.WriteString(path(Point{x, y + dy}, Point{x + r, y + dy + slope*r}))
	}
	return b.String()
}

// path is one subpath through the points, its coordinates rounded to a
// thousandth of a CSS px, an error far below a device pixel.
func path(pts ...Point) string {
	var b strings.Builder
	for i, p := range pts {
		if i == 0 {
			b.WriteByte('M')
		} else {
			b.WriteByte(' ')
		}
		b.WriteString(num(p.X))
		b.WriteByte(' ')
		b.WriteString(num(p.Y))
	}
	return b.String()
}

// num writes v as the shortest decimal of its value to three places, with
// no trailing zeros and never "-0".
func num(v float64) string {
	r := math.Round(v*1000) / 1000
	if r == 0 {
		r = 0 // a negative zero compares equal, and is written positive
	}
	return strconv.FormatFloat(r, 'f', -1, 64)
}

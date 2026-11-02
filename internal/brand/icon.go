package brand

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"math"
)

// The application's icon (phase 5, ruled 2026-11-01) is the mark outside the
// window: the caption, the taskbar, Alt+Tab, Start, the installer and an
// .efd file in Explorer. It is Sheet's folded sheet in solid facets, with no
// tile. Windows 11's icons are unplated, and an .ico cannot follow the
// taskbar's theme, so one drawing has to stand on a light taskbar and on a
// dark one. The light falls from the top left, as on Windows' own icons:
// the face it falls on is a lighter tint of the accent, the front is the
// accent, the face beside the flap a darker tint, and the flap is the
// sheet's white back with its outer edge in the accent, so that a light
// taskbar does not swallow it. On either taskbar at least two facets stand
// above 3:1 (icon_test.go). The folds are not drawn: they are where two
// facets meet. The drawing is the icon designer's of the phase-5 study
// (design-icons.md, geom.mjs's appIconSVG), its colours and its shading as
// they stand; iconFigure fits it to each size's own pixels.

// IconSizes are the sizes, in pixels, of the .ico's entries. Each is drawn
// for its own pixels, never scaled from a larger one, since a scaled figure
// loses the whole-pixel sides that keep it crisp. They are the sizes the
// shell asks for at 100 and 125 % (as recalled): the small icon of the
// caption (16, 20), the taskbar's (24, 30), the large icon of Alt+Tab (32,
// 40), and Explorer's views and Start (48, 64, 128, 256).
var IconSizes = []int{16, 20, 24, 30, 32, 40, 48, 64, 128, 256}

// iconRoundFrom is the size from which the icon's six corners are eased,
// by a fortieth of the size; below it the radius would be under 1.2 px and
// would only blur the points.
const iconRoundFrom = 48

// The facets' paints. The front and the flap shade, top to foot, by a
// linear gradient across each one's bounding box, as the study's drawing
// does; the other two faces are flat.
var (
	iconLit       = color.NRGBA{0x22, 0xA0, 0x8D, 0xFF} // the upper-left face, where the light falls
	iconFrontTop  = color.NRGBA{0x17, 0x91, 0x7F, 0xFF} // the front at the top of the figure
	iconFrontFoot = AccentLight                         // and at its foot
	iconShaded    = color.NRGBA{0x0B, 0x5C, 0x51, 0xFF} // the face beside the flap, in its shadow
	iconFlapTop   = color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF} // the flap, the sheet's back, at its top corner
	iconFlapFoot  = color.NRGBA{0xD6, 0xEE, 0xE8, 0xFF} // and at its far corner, by F
	iconFlapEdge  = AccentLight                         // the flap's outer edge
)

// iconFigure fits Sheet's figure to an icon of n pixels: the same figure,
// scaled, so that the icon cannot drift from the window's mark. The
// half-width is 0.4 of the box, the study's, in whole pixels, and F stands
// on the pixel boundary at the box's middle (the left one of two when n is
// odd), so both sides and the top fold, the figure's only vertical lines,
// fall between pixels and draw as edges with no antialiasing at every size.
//
// Vertically F is the box's middle, except below iconRoundFrom, where the
// figure's top and bottom are sharp 30 degree points. Such a point never
// fills a pixel of its own row: reaching deep into the row it leaves a
// smear of partial pixels two or three wide, and barely entering it, it
// cuts into the next row's middle pixels. So the figure is moved up half a
// pixel when that brings its points nearer the middle of their rows; with
// F on a whole or a half pixel, the bottom point mirrors the top one.
func iconFigure(n int) Figure {
	a := math.Round(0.4 * float64(n))
	cx, cy := float64(n/2), float64(n)/2
	rise := 2 * a / math.Sqrt(3) // from F up to the top point
	if n < iconRoundFrom {
		// depth is how far into its pixel row the top point reaches.
		depth := func(cy float64) float64 {
			top := cy - rise
			return 1 - (top - math.Floor(top))
		}
		if up := cy - 0.5; up-rise >= 0 && math.Abs(depth(up)-0.5) < math.Abs(depth(cy)-0.5) {
			cy = up
		}
	}
	ref := Sheet(96)
	k := a / ref.A
	at := func(p Point) Point { return Point{cx + (p.X-ref.F.X)*k, cy + (p.Y-ref.F.Y)*k} }
	return Figure{
		Width: float64(n), Height: float64(n), A: a,
		Top: at(ref.Top), UpperRight: at(ref.UpperRight), LowerRight: at(ref.LowerRight),
		Bottom: at(ref.Bottom), LowerLeft: at(ref.LowerLeft), UpperLeft: at(ref.UpperLeft),
		F: at(ref.F),
	}
}

// Icon draws the application icon at n x n pixels (16 or more), on a
// transparent ground.
func Icon(n int) *image.NRGBA {
	if n < 16 {
		panic("brand: Icon needs 16 px or more")
	}
	f := iconFigure(n)
	outline := []Point{f.UpperLeft, f.Top, f.UpperRight, f.LowerRight, f.Bottom, f.LowerLeft}
	if n >= iconRoundFrom {
		outline = eased(outline, float64(n)/40)
	}
	inside := polygon(outline...)
	lit := polygon(f.UpperLeft, f.Top, f.F)
	flap := polygon(f.Top, f.UpperRight, f.F)
	shaded := polygon(f.F, f.UpperRight, f.LowerRight)
	// The flap's outer edge: a band along the top-right side, inside the
	// flap, a 48th of the icon wide and never under a pixel. The study
	// strokes the side twice as wide and clips the stroke to the figure, so
	// the stroke's square ends crossed the folds into the faces either side,
	// over the top fold into the lit face by half the band (2.7 px at 256).
	// Here the band ends at the folds, which stay edges between two facets.
	w := math.Max(1, float64(n)/48)
	run, fall := f.UpperRight.X-f.Top.X, f.UpperRight.Y-f.Top.Y
	side := math.Hypot(run, fall)
	in := Point{-fall / side * w, run / side * w} // w along the side's normal, towards F
	edge := polygon(f.Top, f.UpperRight,
		Point{f.UpperRight.X + in.X, f.UpperRight.Y + in.Y}, Point{f.Top.X + in.X, f.Top.Y + in.Y})

	cv := newCanvas(n, n)
	cv.shade(func(x, y float64) (color.NRGBA, bool) {
		switch {
		case !inside(x, y):
			return color.NRGBA{}, false
		case flap(x, y):
			if edge(x, y) {
				return iconFlapEdge, true
			}
			t := ((x-f.Top.X)/(f.UpperRight.X-f.Top.X) + (y-f.Top.Y)/(f.F.Y-f.Top.Y)) / 2
			return mix(iconFlapTop, iconFlapFoot, t), true
		case lit(x, y):
			return iconLit, true
		case shaded(x, y):
			return iconShaded, true
		default:
			return mix(iconFrontTop, iconFrontFoot, (y-f.Top.Y)/(f.Bottom.Y-f.Top.Y)), true
		}
	})
	return cv.image()
}

// eased is the polygon pts with each corner eased by r: the corner cut
// from r before it to r after it by the quadratic curve that has the
// corner for its control point, as SVG's Q draws it, in eight straight
// pieces.
func eased(pts []Point, r float64) []Point {
	const pieces = 8
	var out []Point
	for i, p := range pts {
		toward := func(q Point) Point {
			d := math.Hypot(q.X-p.X, q.Y-p.Y)
			return Point{p.X + (q.X-p.X)/d*r, p.Y + (q.Y-p.Y)/d*r}
		}
		p1, p2 := toward(pts[(i+len(pts)-1)%len(pts)]), toward(pts[(i+1)%len(pts)])
		for k := 0; k <= pieces; k++ {
			t := float64(k) / pieces
			u := 1 - t
			out = append(out, Point{u*u*p1.X + 2*u*t*p.X + t*t*p2.X, u*u*p1.Y + 2*u*t*p.Y + t*t*p2.Y})
		}
	}
	return out
}

// mix is a straight sRGB mix of a and b, t of the way from a (clamped to
// 0..1), which is how an SVG gradient interpolates by default.
func mix(a, b color.NRGBA, t float64) color.NRGBA {
	t = math.Max(0, math.Min(1, t))
	m := func(p, q uint8) uint8 { return uint8(math.Round(float64(p) + (float64(q)-float64(p))*t)) }
	return color.NRGBA{m(a.R, b.R), m(a.G, b.G), m(a.B, b.B), m(a.A, b.A)}
}

// ICO encodes the icon at each of IconSizes as a Windows .ico: the
// ICONDIR header, one 16-byte entry per size, then each image as a PNG.
// Windows has read PNG entries at every size since Vista, and the build's
// other readers take them too: wails3's .syso step copies each entry's
// bytes as they are (winres.LoadICO), and the file wails3 made before this
// one, which the installer packed, held PNG entries only. An entry's width
// and height bytes say 0 for 256.
func ICO() []byte {
	images := make([][]byte, len(IconSizes))
	for i, n := range IconSizes {
		images[i] = PNG(Icon(n))
	}
	var b bytes.Buffer
	le := binary.LittleEndian
	_ = binary.Write(&b, le, [3]uint16{0, 1, uint16(len(images))}) // reserved, type 1 (an icon), count
	offset := 6 + 16*len(images)
	for i, n := range IconSizes {
		side := uint8(n % 256) // 256 is written as 0
		_ = binary.Write(&b, le, struct {
			Width, Height, Colours, Reserved uint8
			Planes, BitCount                 uint16
			Size, Offset                     uint32
		}{side, side, 0, 0, 1, 32, uint32(len(images[i])), uint32(offset)})
		offset += len(images[i])
	}
	for _, img := range images {
		b.Write(img)
	}
	return b.Bytes()
}

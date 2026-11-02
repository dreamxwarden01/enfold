// Package brand draws Enfold's application icon and its tray icons: no
// binary assets in the repository, the same shapes at every size, both
// themes. Sheet (sheet.go) is the mark's one geometry, the folded sheet: the
// window's mark and drawings are drawn from it, and so is the application's
// icon (icon.go), which tools/appicon writes into the .ico before each build.
package brand

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

// The Native look's colours (docs/ui/native.html).
var (
	AccentLight = color.NRGBA{0x0E, 0x6F, 0x63, 0xFF}
	AccentDark  = color.NRGBA{0x54, 0xD2, 0xB9, 0xFF}
	InkLight    = color.NRGBA{0x19, 0x1A, 0x1D, 0xFF}
	InkDark     = color.NRGBA{0xF3, 0xF3, 0xF6, 0xFF}
)

// TrayState is what the tray icon says.
type TrayState int

const (
	TrayLocked     TrayState = iota // hollow: nothing in memory
	TrayLockedOpen                  // hollow with a dot: locked, archives still open
	TrayUnlocked                    // filled: keys in memory
)

const supersample = 4

// canvas is a coverage-accumulating raster: shapes are sampled at
// supersample² points per pixel and blended, so edges are smooth without
// a vector library.
type canvas struct {
	w, h int
	px   []color.NRGBA
}

func newCanvas(w, h int) *canvas { return &canvas{w: w, h: h, px: make([]color.NRGBA, w*h)} }

// fill paints c wherever inside says so, with coverage-weighted blending.
func (cv *canvas) fill(c color.NRGBA, inside func(x, y float64) bool) {
	cv.shade(func(x, y float64) (color.NRGBA, bool) { return c, inside(x, y) })
}

// shade paints each pixel with the mean of the colours paint gives its
// samples, at the share of the samples it paints at all. A figure of
// several regions is painted in one pass, each sample taking the colour of
// the region it falls in: where two regions meet, a pixel is part one and
// part the other, with nothing of what lies under them showing through.
// Filling the regions one over another, each at its own coverage, would
// let a seam of the ground through along every shared edge.
func (cv *canvas) shade(paint func(x, y float64) (color.NRGBA, bool)) {
	const n = supersample
	for py := 0; py < cv.h; py++ {
		for px := 0; px < cv.w; px++ {
			var r, g, b, a float64
			for sy := 0; sy < n; sy++ {
				for sx := 0; sx < n; sx++ {
					x := float64(px) + (float64(sx)+0.5)/n
					y := float64(py) + (float64(sy)+0.5)/n
					c, ok := paint(x, y)
					if !ok {
						continue
					}
					ca := float64(c.A) / 255
					r, g, b, a = r+float64(c.R)*ca, g+float64(c.G)*ca, b+float64(c.B)*ca, a+ca
				}
			}
			if a == 0 {
				continue
			}
			c := color.NRGBA{uint8(math.Round(r / a)), uint8(math.Round(g / a)), uint8(math.Round(b / a)), 0xFF}
			cv.px[py*cv.w+px] = over(cv.px[py*cv.w+px], c, a/(n*n))
		}
	}
}

// over composites src with alpha a over dst (both straight alpha).
func over(dst, src color.NRGBA, a float64) color.NRGBA {
	da := float64(dst.A) / 255
	oa := a + da*(1-a)
	if oa == 0 {
		return color.NRGBA{}
	}
	mix := func(d, s uint8) uint8 {
		v := (float64(s)*a + float64(d)*da*(1-a)) / oa
		return uint8(math.Round(v))
	}
	return color.NRGBA{mix(dst.R, src.R), mix(dst.G, src.G), mix(dst.B, src.B), uint8(math.Round(oa * 255))}
}

func (cv *canvas) image() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, cv.w, cv.h))
	for i, p := range cv.px {
		img.Pix[i*4], img.Pix[i*4+1], img.Pix[i*4+2], img.Pix[i*4+3] = p.R, p.G, p.B, p.A
	}
	return img
}

// polygon is the inside of the closed polygon through pts, by the even-odd
// rule: a point is inside when a ray from it crosses the outline an odd
// number of times. The icon's facets are drawn with it, and so is anything
// with straight sides that the discs and rings below cannot draw.
func polygon(pts ...Point) func(x, y float64) bool {
	x0, y0, x1, y1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, p := range pts {
		x0, y0, x1, y1 = math.Min(x0, p.X), math.Min(y0, p.Y), math.Max(x1, p.X), math.Max(y1, p.Y)
	}
	return func(x, y float64) bool {
		if x < x0 || x > x1 || y < y0 || y > y1 {
			return false
		}
		in := false
		for i, j := 0, len(pts)-1; i < len(pts); j, i = i, i+1 {
			a, b := pts[i], pts[j]
			if (a.Y > y) != (b.Y > y) && x < a.X+(y-a.Y)*(b.X-a.X)/(b.Y-a.Y) {
				in = !in
			}
		}
		return in
	}
}

func disc(cx, cy, r float64) func(x, y float64) bool {
	return func(x, y float64) bool {
		dx, dy := x-cx, y-cy
		return dx*dx+dy*dy <= r*r
	}
}

func ring(cx, cy, r, width float64) func(x, y float64) bool {
	outer, inner := disc(cx, cy, r), disc(cx, cy, r-width)
	return func(x, y float64) bool { return outer(x, y) && !inner(x, y) }
}

// Tray draws a tray icon of the given state; dark is for a dark taskbar,
// where the ink is light. It is drawn at the size the tray shows it
// (tray_windows.go asks for the system's small icon size), so nothing
// scales it. The figure is rings and discs, not the mark: the mark's tray
// states are fork C's, still open.
func Tray(size int, state TrayState, dark bool) *image.NRGBA {
	cv := newCanvas(size, size)
	s := float64(size)
	accent, ink := AccentLight, InkLight
	if dark {
		accent, ink = AccentDark, InkDark
	}
	cx, cy, r := s/2, s/2, s*0.40
	width := s * 0.12
	switch state {
	case TrayUnlocked:
		cv.fill(accent, disc(cx, cy, r))
		cv.fill(ink, disc(cx, cy, r*0.28))
	case TrayLockedOpen:
		cv.fill(ink, ring(cx, cy, r, width))
		cv.fill(accent, disc(cx, cy, r*0.32))
	default:
		cv.fill(ink, ring(cx, cy, r, width))
	}
	return cv.image()
}

// PNG encodes an image.
func PNG(img image.Image) []byte {
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		panic(err)
	}
	return b.Bytes()
}

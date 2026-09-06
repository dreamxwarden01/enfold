// Package brand draws Enfold's mark and tray icons: no binary assets in the
// repository, the same shapes at every size, both themes.
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
	GroundLight = color.NRGBA{0xF1, 0xF1, 0xF4, 0xFF}
	GroundDark  = color.NRGBA{0x1C, 0x1C, 0x20, 0xFF}
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
	const n = supersample
	for py := 0; py < cv.h; py++ {
		for px := 0; px < cv.w; px++ {
			hits := 0
			for sy := 0; sy < n; sy++ {
				for sx := 0; sx < n; sx++ {
					x := float64(px) + (float64(sx)+0.5)/n
					y := float64(py) + (float64(sy)+0.5)/n
					if inside(x, y) {
						hits++
					}
				}
			}
			if hits == 0 {
				continue
			}
			a := float64(hits) / (n * n) * float64(c.A) / 255
			cv.px[py*cv.w+px] = over(cv.px[py*cv.w+px], c, a)
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

// hexagon is the mark's outline: a regular hexagon, flat sides left and
// right, of circumradius r around (cx, cy).
func hexagon(cx, cy, r float64) func(x, y float64) bool {
	return func(x, y float64) bool {
		dx, dy := math.Abs(x-cx), math.Abs(y-cy)
		if dx > r*math.Sqrt(3)/2 || dy > r {
			return false
		}
		return dy <= r-dx/math.Sqrt(3)
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

// thickLine is a segment with round caps.
func thickLine(x1, y1, x2, y2, width float64) func(x, y float64) bool {
	dx, dy := x2-x1, y2-y1
	l2 := dx*dx + dy*dy
	return func(x, y float64) bool {
		t := 0.0
		if l2 > 0 {
			t = ((x-x1)*dx + (y-y1)*dy) / l2
			t = math.Max(0, math.Min(1, t))
		}
		px, py := x1+t*dx, y1+t*dy
		ex, ey := x-px, y-py
		return ex*ex+ey*ey <= width*width/4
	}
}

// Mark draws the application icon: a hexagonal box with its top face
// suggested by two edges, on a rounded ground, the accent on it.
func Mark(size int, dark bool) *image.NRGBA {
	cv := newCanvas(size, size)
	s := float64(size)
	ground, accent, ink := GroundLight, AccentLight, InkLight
	if dark {
		ground, accent, ink = GroundDark, AccentDark, InkDark
	}
	// Ground: a squircle-ish rounded square.
	radius := s * 0.22
	cv.fill(ground, func(x, y float64) bool {
		x, y = x-s/2, y-s/2
		hx, hy := s/2-radius, s/2-radius
		qx, qy := math.Max(math.Abs(x)-hx, 0), math.Max(math.Abs(y)-hy, 0)
		return qx*qx+qy*qy <= radius*radius
	})
	cx, cy, r := s/2, s/2, s*0.30
	stroke := s * 0.055
	// Box outline.
	outer, inner := hexagon(cx, cy, r), hexagon(cx, cy, r-stroke*1.15)
	cv.fill(accent, func(x, y float64) bool { return outer(x, y) && !inner(x, y) })
	// The two edges that make it a box.
	cv.fill(accent, thickLine(cx, cy-r, cx, cy, stroke))
	cv.fill(accent, thickLine(cx, cy, cx-r*math.Sqrt(3)/2, cy+r/2, stroke))
	cv.fill(accent, thickLine(cx, cy, cx+r*math.Sqrt(3)/2, cy+r/2, stroke))
	// A small dot at the vertex: the key.
	cv.fill(ink, disc(cx, cy, stroke*0.9))
	return cv.image()
}

// Tray draws a tray icon of the given state; dark is for a dark taskbar,
// where the ink is light.
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

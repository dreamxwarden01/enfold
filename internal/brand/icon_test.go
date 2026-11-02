package brand

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"sort"
	"sync"
	"testing"
)

// Windows 11's taskbars, light and dark (recalled, as in the phase-5
// study): the grounds the icon has to stand on, both at once.
var (
	taskbarLight = color.NRGBA{0xF3, 0xF3, 0xF3, 0xFF}
	taskbarDark  = color.NRGBA{0x20, 0x20, 0x20, 0xFF}
)

// icons renders every size once for the tests that read the pixels.
var icons = sync.OnceValue(func() map[int]*image.NRGBA {
	m := map[int]*image.NRGBA{}
	for _, n := range IconSizes {
		m[n] = Icon(n)
	}
	return m
})

func whole(v float64) bool { return math.Abs(v-math.Round(v)) < 1e-9 }

// TestIconFigureIsSheet holds the icon to the mark's one geometry: at every
// size its figure is Sheet's, scaled, with both sides and the top fold on
// whole pixels, inside the box, and F within half a pixel of its middle.
func TestIconFigureIsSheet(t *testing.T) {
	ref := Sheet(96)
	for _, n := range IconSizes {
		f := iconFigure(n)
		if f.A != math.Round(0.4*float64(n)) {
			t.Errorf("%d px: half-width %v, want 0.4 of the box in whole pixels", n, f.A)
		}
		pairs := [][2]Point{{f.Top, ref.Top}, {f.UpperRight, ref.UpperRight}, {f.LowerRight, ref.LowerRight},
			{f.Bottom, ref.Bottom}, {f.LowerLeft, ref.LowerLeft}, {f.UpperLeft, ref.UpperLeft}}
		for _, p := range pairs {
			gx, gy := (p[0].X-f.F.X)/f.A, (p[0].Y-f.F.Y)/f.A
			wx, wy := (p[1].X-ref.F.X)/ref.A, (p[1].Y-ref.F.Y)/ref.A
			if !near(gx, wx, 1e-9) || !near(gy, wy, 1e-9) {
				t.Errorf("%d px: a vertex at %v from F, Sheet's at %v", n, Point{gx, gy}, Point{wx, wy})
			}
		}
		for _, x := range []float64{f.UpperLeft.X, f.LowerLeft.X, f.Top.X, f.F.X, f.UpperRight.X, f.LowerRight.X} {
			if !whole(x) {
				t.Errorf("%d px: a vertical line at x %v, between pixel edges", n, x)
			}
		}
		if f.Top.Y < 0 || f.Bottom.Y > float64(n) || f.UpperLeft.X < 0 || f.UpperRight.X > float64(n) {
			t.Errorf("%d px: the figure leaves the box: %+v", n, f)
		}
		if math.Abs(f.F.X-float64(n)/2) > 0.5 || math.Abs(f.F.Y-float64(n)/2) > 0.5 {
			t.Errorf("%d px: F at %v, more than half a pixel from the middle", n, f.F)
		}
	}
}

// TestIconEdgesOnPixels reads the vertical lines back from the pixels: each
// side is a full column against an empty one, and the top fold leaves the
// lit face's column pure and the flap's free of it.
func TestIconEdgesOnPixels(t *testing.T) {
	for _, n := range IconSizes {
		img, f := icons()[n], iconFigure(n)
		r := 0.0
		if n >= iconRoundFrom {
			r = float64(n) / 40
		}
		left, right := int(f.UpperLeft.X), int(f.UpperRight.X)
		for y := int(math.Ceil(f.UpperLeft.Y + r)); y < int(math.Floor(f.LowerLeft.Y-r)); y++ {
			if img.NRGBAAt(left-1, y).A != 0 || img.NRGBAAt(left, y).A != 0xFF ||
				img.NRGBAAt(right-1, y).A != 0xFF || img.NRGBAAt(right, y).A != 0 {
				t.Errorf("%d px, row %d: the sides are not whole columns: %d %d | %d %d", n, y,
					img.NRGBAAt(left-1, y).A, img.NRGBAAt(left, y).A, img.NRGBAAt(right-1, y).A, img.NRGBAAt(right, y).A)
			}
		}
		// Column cx-1 lies wholly in the lit face below the upper-left
		// side (and below the eased top corner, which reaches r/2 down)
		// and above the diagonal; column cx wholly in the flap below its
		// accent edge and above the face beside it.
		cx, run := int(f.F.X), 1/math.Sqrt(3)
		top := f.Top.Y + r/2 + run
		band := math.Max(1, float64(n)/48) * 2 / math.Sqrt(3)
		rows := 0
		for y := int(math.Ceil(top)); y < int(math.Floor(f.F.Y-run)); y++ {
			if c := img.NRGBAAt(cx-1, y); c != iconLit {
				t.Errorf("%d px, row %d: the column left of the fold is %v, not the lit face's %v", n, y, c, iconLit)
			}
			if float64(y) >= top+band {
				rows++
				if c := img.NRGBAAt(cx, y); c.R < 0xD0 || c.A != 0xFF {
					t.Errorf("%d px, row %d: the column right of the fold is %v, part lit face", n, y, c)
				}
			}
		}
		if rows == 0 {
			t.Errorf("%d px: no row shows the fold", n)
		}
	}
}

// pure says whether an opaque pixel is one facet's paint rather than a
// blend of two that differ: within 12 of a flat paint or of a gradient's
// span. Two facets that nearly match (the lit face and the front's top
// are 23 apart) blend into something still within 12 of one of them,
// which the eye does not see as a blur either.
func pure(c color.NRGBA) bool {
	dist := func(a, b color.NRGBA) float64 {
		// from c to the segment a-b, in RGB
		ax, ay, az := float64(b.R)-float64(a.R), float64(b.G)-float64(a.G), float64(b.B)-float64(a.B)
		px, py, pz := float64(c.R)-float64(a.R), float64(c.G)-float64(a.G), float64(c.B)-float64(a.B)
		t := 0.0
		if l := ax*ax + ay*ay + az*az; l > 0 {
			t = math.Max(0, math.Min(1, (px*ax+py*ay+pz*az)/l))
		}
		return math.Hypot(math.Hypot(px-t*ax, py-t*ay), pz-t*az)
	}
	for _, s := range [][2]color.NRGBA{{iconLit, iconLit}, {iconShaded, iconShaded}, {iconFlapEdge, iconFlapEdge},
		{iconFrontTop, iconFrontFoot}, {iconFlapTop, iconFlapFoot}} {
		if dist(s[0], s[1]) <= 12 {
			return true
		}
	}
	return false
}

// solidShare measures an entry as the phase-5 study measured a glyph: the
// share of its ink (its alpha, summed) that lies in solid pixels, opaque
// to 90 % and pure.
func solidShare(img *image.NRGBA) (share float64, rows []float64) {
	b := img.Bounds()
	var ink, solid float64
	for y := 0; y < b.Dy(); y++ {
		var rowSolid float64
		for x := 0; x < b.Dx(); x++ {
			c := img.NRGBAAt(x, y)
			a := float64(c.A) / 255
			ink += a
			if a >= 0.9 && pure(c) {
				solid += a
				rowSolid += a
			}
		}
		rows = append(rows, rowSolid)
	}
	return solid / ink, rows
}

// TestIconSmallIsSolid holds the small entries, the 16 px one first,
// against the blur of the icon they replace (its 16 px entry, a downscale
// of 1024, had 0.17 of its accent in solid pixels): no row of
// antialiasing alone, which is what a sharp point placed deep in its row
// leaves, and most of the ink solid.
func TestIconSmallIsSolid(t *testing.T) {
	for _, n := range IconSizes {
		if n >= iconRoundFrom {
			continue
		}
		img := icons()[n]
		share, rows := solidShare(img)
		t.Logf("%d px: solid share %.2f", n, share)
		if n == 16 && share < 0.7 {
			t.Errorf("16 px: solid share %.2f, want 0.7 or more", share)
		}
		for y, s := range rows {
			if s > 0 {
				continue
			}
			partial := 0
			for x := 0; x < n; x++ {
				if img.NRGBAAt(x, y).A > 0 {
					partial++
				}
			}
			// A point's tip is at most two partial pixels; more is a smear.
			if partial > 2 {
				t.Errorf("%d px, row %d: %d pixels of ink, none of them solid", n, y, partial)
			}
		}
	}
}

func luminance(c color.NRGBA) float64 {
	lin := func(v uint8) float64 {
		x := float64(v) / 255
		if x <= 0.04045 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

func contrastRatio(a, b color.NRGBA) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// TestIconStandsOnBothTaskbars renders every size and measures each facet
// against both taskbars: its pixels that lie wholly inside it, the median
// by luminance. At least two facets must stand above 3:1 on each, so that
// the silhouette carries on a light taskbar and on a dark one.
func TestIconStandsOnBothTaskbars(t *testing.T) {
	for _, n := range IconSizes {
		img, f := icons()[n], iconFigure(n)
		w := math.Max(1, float64(n)/48)
		toward := func(p Point, k float64) Point { return Point{f.F.X + (p.X-f.F.X)*k, f.F.Y + (p.Y-f.F.Y)*k} }
		facets := []struct {
			name string
			pts  []Point
		}{
			{"lit face", []Point{f.UpperLeft, f.Top, f.F}},
			{"front", []Point{f.UpperLeft, f.LowerRight, f.Bottom, f.LowerLeft}},
			{"face beside the flap", []Point{f.F, f.UpperRight, f.LowerRight}},
			// The flap less its accent edge: the flap is equilateral with
			// F a half-width from the edge, so it shrinks about F.
			{"flap", []Point{toward(f.Top, (f.A-w)/f.A), toward(f.UpperRight, (f.A-w)/f.A), f.F}},
		}
		above := map[string]int{}
		for _, fc := range facets {
			in := polygon(fc.pts...)
			var px []color.NRGBA
			for y := 0; y < n; y++ {
				for x := 0; x < n; x++ {
					X, Y := float64(x), float64(y)
					if in(X+0.001, Y+0.001) && in(X+0.999, Y+0.001) && in(X+0.001, Y+0.999) && in(X+0.999, Y+0.999) {
						if c := img.NRGBAAt(x, y); c.A == 0xFF {
							px = append(px, c)
						}
					}
				}
			}
			if len(px) == 0 {
				t.Errorf("%d px: the %s has no pixel of its own", n, fc.name)
				continue
			}
			sort.Slice(px, func(i, j int) bool { return luminance(px[i]) < luminance(px[j]) })
			c := px[len(px)/2]
			cl, cd := contrastRatio(c, taskbarLight), contrastRatio(c, taskbarDark)
			if n == 16 || n == 256 {
				t.Logf("%d px, %s %v: %.2f light, %.2f dark", n, fc.name, c, cl, cd)
			}
			if cl >= 3 {
				above["light"]++
			}
			if cd >= 3 {
				above["dark"]++
			}
		}
		if above["light"] < 2 || above["dark"] < 2 {
			t.Errorf("%d px: facets above 3:1: %d on the light taskbar, %d on the dark, want 2 on each",
				n, above["light"], above["dark"])
		}
	}
}

// icoEntry is one ICONDIRENTRY as the file holds it.
type icoEntry struct {
	Width, Height, Colours, Reserved uint8
	Planes, BitCount                 uint16
	Size, Offset                     uint32
}

// readICO parses an .ico the way the shell's loader does: the ICONDIR,
// then each entry's image where the entry says it is.
func readICO(t *testing.T, b []byte) ([]icoEntry, [][]byte) {
	t.Helper()
	r := bytes.NewReader(b)
	var hdr [3]uint16
	if err := binary.Read(r, binary.LittleEndian, &hdr); err != nil {
		t.Fatal(err)
	}
	if hdr[0] != 0 || hdr[1] != 1 {
		t.Fatalf("ICONDIR reserved %d, type %d: not an icon", hdr[0], hdr[1])
	}
	entries := make([]icoEntry, hdr[2])
	if err := binary.Read(r, binary.LittleEndian, entries); err != nil {
		t.Fatal(err)
	}
	var images [][]byte
	for _, e := range entries {
		end := uint64(e.Offset) + uint64(e.Size)
		if e.Offset < uint32(6+16*len(entries)) || end > uint64(len(b)) {
			t.Fatalf("entry %+v points outside the images", e)
		}
		images = append(images, b[e.Offset:end])
	}
	return entries, images
}

// TestICOParses reads ICO back: one entry per size in IconSizes, each a
// 32-bit PNG of that size with exactly Icon's pixels, the images laid end
// to end after the directory.
func TestICOParses(t *testing.T) {
	b := ICO()
	entries, images := readICO(t, b)
	if len(entries) != len(IconSizes) {
		t.Fatalf("%d entries, want %d", len(entries), len(IconSizes))
	}
	next := uint32(6 + 16*len(entries))
	for i, e := range entries {
		n := IconSizes[i]
		side := int(e.Width)
		if side == 0 {
			side = 256
		}
		if side != n || e.Height != e.Width || e.Colours != 0 || e.Reserved != 0 || e.Planes != 1 || e.BitCount != 32 {
			t.Errorf("entry %d: %+v, want a 32-bit %d px entry", i, e, n)
		}
		if e.Offset != next {
			t.Errorf("entry %d at %d, want %d: the images are not end to end", i, e.Offset, next)
		}
		next = e.Offset + e.Size
		if !bytes.HasPrefix(images[i], []byte("\x89PNG\r\n\x1a\n")) {
			t.Errorf("entry %d is not a PNG", i)
			continue
		}
		img, err := png.Decode(bytes.NewReader(images[i]))
		if err != nil {
			t.Fatalf("entry %d: %v", i, err)
		}
		got, ok := img.(*image.NRGBA)
		if !ok || got.Bounds().Dx() != n || got.Bounds().Dy() != n {
			t.Fatalf("entry %d: %T %v, want a %d px NRGBA image", i, img, img.Bounds(), n)
		}
		if !bytes.Equal(got.Pix, icons()[n].Pix) {
			t.Errorf("entry %d: the pixels are not Icon(%d)'s", i, n)
		}
	}
	if next != uint32(len(b)) {
		t.Errorf("the file runs %d bytes past its last image", len(b)-int(next))
	}
}

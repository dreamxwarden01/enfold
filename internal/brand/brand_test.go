package brand

import (
	"bytes"
	"image/png"
	"testing"
)

func TestIconsDecode(t *testing.T) {
	for _, dark := range []bool{false, true} {
		img, err := png.Decode(bytes.NewReader(PNG(Mark(256, dark))))
		if err != nil {
			t.Fatal(err)
		}
		if b := img.Bounds(); b.Dx() != 256 || b.Dy() != 256 {
			t.Fatalf("mark bounds %v", b)
		}
		for _, st := range []TrayState{TrayLocked, TrayLockedOpen, TrayUnlocked} {
			img, err := png.Decode(bytes.NewReader(PNG(Tray(32, st, dark))))
			if err != nil {
				t.Fatal(err)
			}
			if b := img.Bounds(); b.Dx() != 32 || b.Dy() != 32 {
				t.Fatalf("tray bounds %v", b)
			}
			// The centre pixel tells the states apart: ink for unlocked, accent
			// for locked-with-open, transparent for locked.
			_, _, _, a := img.At(16, 16).RGBA()
			if (st == TrayLocked) != (a == 0) {
				t.Fatalf("state %d dark=%v: centre alpha %d", st, dark, a)
			}
		}
	}
}

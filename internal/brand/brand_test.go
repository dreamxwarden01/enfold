package brand

import (
	"bytes"
	"image/png"
	"testing"
)

func TestIconsDecode(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(PNG(Icon(256))))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 256 || b.Dy() != 256 {
		t.Fatalf("icon bounds %v", b)
	}
	// The tray draws at the system's small icon size: 16, 20, 24 and 32 px
	// at 100, 125, 150 and 200 %.
	for _, size := range []int{16, 20, 24, 32} {
		for _, dark := range []bool{false, true} {
			for _, st := range []TrayState{TrayLocked, TrayLockedOpen, TrayUnlocked} {
				img, err := png.Decode(bytes.NewReader(PNG(Tray(size, st, dark))))
				if err != nil {
					t.Fatal(err)
				}
				if b := img.Bounds(); b.Dx() != size || b.Dy() != size {
					t.Fatalf("tray bounds %v", b)
				}
				// The centre pixel tells the states apart: ink for unlocked,
				// accent for locked-with-open, transparent for locked.
				_, _, _, a := img.At(size/2, size/2).RGBA()
				if (st == TrayLocked) != (a == 0) {
					t.Fatalf("%d px, state %d dark=%v: centre alpha %d", size, st, dark, a)
				}
			}
		}
	}
}

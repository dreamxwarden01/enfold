//go:build windows

package keystore

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/dreamxwarden01/enfold/internal/format"
)

// lockBytes takes an exclusive byte-range lock on [off, off+n) of the file at
// path through a handle of its own. A Windows byte-range lock is mandatory,
// so every read or write of the range through another handle fails, as one
// of a bad sector would, until the returned function releases it.
func lockBytes(t *testing.T, path string, off, n uint64) (release func()) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	at := func() *windows.Overlapped {
		return &windows.Overlapped{Offset: uint32(off), OffsetHigh: uint32(off >> 32)}
	}
	h := windows.Handle(f.Fd())
	if err := windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, uint32(n), uint32(n>>32), at()); err != nil {
		f.Close()
		t.Fatal(err)
	}
	released := false
	release = func() {
		if released {
			return
		}
		released = true
		windows.UnlockFileEx(h, 0, uint32(n), uint32(n>>32), at())
		f.Close()
	}
	t.Cleanup(release)
	return release
}

// R41's predicate reads at Open what no reader of either state needs: the
// rest of both slot region copies and the registry area outside the named
// extents. When those bytes cannot be read the file still opens, owed by
// what could not be read, since a retirement that cannot be finished leaves
// the vault readable (R-A); the line is logged, the vault unlocks, and
// Retire meets the error and returns it, the file still owed and the handle
// sound, until the bytes read again and it finishes.
func TestAnUnreadablePredicateOpensOwed(t *testing.T) {
	cases := []struct {
		name string
		// at is the range made unreadable, found in the file's image.
		at   func(t *testing.T, img []byte) (off, n uint64)
		want retireState
	}{
		{"the other slot region copy", func(t *testing.T, img []byte) (uint64, uint64) {
			live, _ := liveOf(t, img)
			return live.LiveSlotRegion().Other().SlotRegionOff(), format.SlotRegionSize
		}, retireState{other: true}},
		{"a registry unit past the losing registry's tag", func(t *testing.T, img []byte) (uint64, uint64) {
			live, c := liveOf(t, img)
			end := extentOf(sbCopy(t, img, c.Other()))[1]
			unitEnd := (end + regUnit - 1) &^ (regUnit - 1)
			if end == unitEnd || unitEnd > live.RegistryOff {
				t.Fatalf("the fixture: the losing registry ends at 0x%x, the live one begins at 0x%x", end, live.RegistryOff)
			}
			return end, unitEnd - end
		}, retireState{remnant: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "v.eks")
			rk := recoveryKey(t)
			u := mustCreate(t, path, PasswordSlot{Password: "p", Argon2: fast}, RecoverySlot{Key: rk})
			// The live registry above the one create wrote at 0x42000, which
			// the losing copy names and whose last unit the scan reads.
			addArchives(t, u, 3, 0)
			if u.k.RetireOwed() {
				t.Fatalf("the fixture owes %+v", u.k.owed)
			}
			u.Close()
			u.k.Close()
			off, n := c.at(t, snapshot(t, path))
			release := lockBytes(t, path, off, n)
			lines := logged(t)
			k, err := Open(path)
			if err != nil {
				t.Fatalf("Open with bytes no reader needs unreadable: %v", err)
			}
			t.Cleanup(func() { k.Close() })
			if k.owed != c.want {
				t.Fatalf("opened owing %+v, want %+v", k.owed, c.want)
			}
			if len(*lines) != 1 {
				t.Fatalf("logged %q", *lines)
			}
			u, err = k.Unlock(RecoveryCredential{Key: rk})
			if err != nil {
				t.Fatalf("the vault does not unlock: %v", err)
			}
			defer u.Close()
			seq := k.sb.Seq
			if err := u.Retire(); err == nil {
				t.Fatal("Retire over bytes it can neither read nor write returned nil")
			}
			if !k.RetireOwed() || k.Broken() != nil || k.sb.Seq != seq {
				t.Fatalf("after a Retire that failed: owed %+v, broken %v, seq %d after %d", k.owed, k.Broken(), k.sb.Seq, seq)
			}
			release()
			if err := u.Retire(); err != nil {
				t.Fatal(err)
			}
			if k.RetireOwed() {
				t.Fatalf("still owed: %+v", k.owed)
			}
			requireClean(t, path, "after the retirement")
		})
	}
}

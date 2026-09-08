package keystore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/dreamxwarden01/enfold/internal/format"
)

// FuzzOpen writes arbitrary bytes over a valid keystore file and opens the
// result. Open must refuse with format.ErrInvalid or open cleanly; an Unlock
// with a wrong password against whatever opened must not panic. The seed is
// a real keystore so that the fuzzer starts from something parseable.
func FuzzOpen(f *testing.F) {
	dir := f.TempDir()
	path := filepath.Join(dir, "seed.eks")
	u, err := Create(path, CreateOptions{Slots: []SlotSpec{
		PasswordSlot{Password: "p", Argon2: fast},
		RecoverySlot{Key: recoveryKey(f)},
	}})
	if err != nil {
		f.Fatal(err)
	}
	u.Close()
	u.k.Close()
	seed, err := os.ReadFile(path)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed, uint32(0), uint8(0))
	f.Add(seed, uint32(format.KeystoreSuperblockAOff+16), uint8(1))
	f.Add(seed, uint32(format.SlotRegionAOff+8), uint8(0xff))
	// A byte inside the 32-byte slot region header (§6): argon2_m sits at
	// offset 8 of it, so this seed lands the Argon2 downgrade the guard below
	// is about.
	f.Add(seed, uint32(format.SlotRegionAOff+10), uint8(0x40))
	f.Add(seed[:format.RegistryMinOff+10], uint32(0), uint8(0))
	f.Fuzz(func(t *testing.T, data []byte, at uint32, x uint8) {
		if int(at) < len(data) {
			data[at] ^= x
		}
		if len(data) > 1<<20 {
			data = data[:1<<20]
		}
		p := filepath.Join(t.TempDir(), "f.eks")
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		k, err := Open(p)
		if err != nil {
			if !errors.Is(err, format.ErrInvalid) {
				t.Fatalf("unexpected Open error: %v", err)
			}
			return
		}
		defer k.Close()
		_ = k.Slots()
		// The slot region carries no checksum of its own — R25 is checked
		// after unlock — so a mutated argon2_m reaches Argon2id. R24's
		// ceiling is 2 GiB, a format bound, not one for parallel fuzz
		// workers. This target is about parsing and error classification.
		//
		// Since Revision 2 the slot region header is a second source of
		// Argon2 parameters (§6): K_P runs at the header's argon2_m for any
		// hardware credential, so the guard covers it too even though this
		// target only sends a password credential today.
		if k.hdr.Argon2M > 1024 {
			return
		}
		for i := range k.slots {
			if k.slots[i].Argon2M > 1024 {
				return
			}
		}
		if _, err := k.Unlock(PasswordCredential{Password: "wrong"}); err == nil {
			t.Fatal("wrong password opened a mutated file")
		}
	})
}

package keystore

import (
	"bytes"
	"crypto/ecdh"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/dreamxwarden01/enfold/internal/format"
	"github.com/dreamxwarden01/enfold/internal/kdf"
)

// FuzzOpen writes arbitrary bytes over a valid keystore file and opens the
// result. Open must refuse with format.ErrInvalid or open cleanly; whatever
// opened is then offered the credentials the seeds were built with, so that
// the paths behind an authenticating unlock — the registry's AEAD, the
// slot-region hash of R25, the header/registry agreement of §7.6, the K_P the
// registry keeps and the VMK history of §18.2 — are reachable and not only
// the rejection path a wrong password takes.
//
// The oracle: nothing panics; a refused unlock installs no registry and
// leaves the file byte for byte as it was; an unlock that succeeds agrees with
// an independent check of the slot region against the registry's hash. "A
// wrong password must fail" is asserted for the seeds alone — for arbitrary
// bytes it is not an oracle at all, since the fuzzer may derive a file that
// password legitimately opens.
// The fixtures' credentials are fixed bytes rather than fresh randomness.
// Real fuzzing runs the coordinator's corpus in worker processes, each of
// which builds these fixtures again: a token key or a recovery key drawn from
// the OS would differ per process, so no worker could open what the
// coordinator collected and every authenticated path — the registry's AEAD,
// R25's hash, the secrets section — would go unexercised behind a wrong
// credential. Passwords and Argon2 parameters are already constants.
var fixedRecoveryKey = kdf.RecoveryKey{
	0x52, 0x65, 0x63, 0x6f, 0x76, 0x65, 0x72, 0x79,
	0x20, 0x66, 0x75, 0x7a, 0x7a, 0x20, 0x6b, 0x65,
}

// fixedToken is the soft token of the hardware fixtures, on a fixed P-256
// scalar: small enough to be a valid private key, and the same in every
// process.
func fixedToken(f *testing.F) *softToken {
	f.Helper()
	var scalar [32]byte
	for i := range scalar {
		scalar[i] = byte(i + 1)
	}
	key, err := ecdh.P256().NewPrivateKey(scalar[:])
	if err != nil {
		f.Fatal(err)
	}
	return &softToken{key: key}
}

func FuzzOpen(f *testing.F) {
	dir := f.TempDir()
	tok := fixedToken(f)
	rk := fixedRecoveryKey
	const slotPassword = "p"
	const vaultPassword = "the vault's own"

	// build writes one keystore, runs after over it, and returns its bytes.
	build := func(name string, opts CreateOptions, after func(*Unlocked) error) []byte {
		path := filepath.Join(dir, name)
		u, err := Create(path, opts)
		if err != nil {
			f.Fatal(err)
		}
		if after != nil {
			if err := after(u); err != nil {
				f.Fatal(err)
			}
		}
		u.Close()
		u.k.Close()
		b, err := os.ReadFile(path)
		if err != nil {
			f.Fatal(err)
		}
		return b
	}

	software := CreateOptions{Slots: []SlotSpec{
		PasswordSlot{Password: slotPassword, Argon2: fast},
		RecoverySlot{Key: rk},
	}}
	// Four structures rather than one, so that a mutation lands in each of the
	// states the format allows: a fresh file (superblock A at seq 1, B at 0),
	// one commit further on (B live, A the loser), a vault with the entangled
	// password and a hardware slot (the header's five fields, K_P, the
	// entangled_key record), and one past a rotation (generation 2 and a
	// vmk_history record in the secrets section).
	aNewer := build("a.eks", software, nil)
	bNewer := build("b.eks", software, func(u *Unlocked) error {
		return u.UpdateRegistry(func(g *format.Registry) error {
			g.IdleMinutes = 5
			return nil
		})
	})
	entangled := build("e.eks", CreateOptions{
		Entangle: &Entangle{Password: vaultPassword, Argon2: fast},
		Slots: []SlotSpec{
			HardwareSlot{PublicKey: tok.PublicKey(), Label: "token"},
			RecoverySlot{Key: rk, Label: "paper"},
		},
	}, nil)
	rotated := build("r.eks", software, func(u *Unlocked) error { return u.Rotate() })

	// The credentials the seeds carry. Every structural seed must open with
	// one of them: a corpus of files that only ever fail to authenticate would
	// leave the registry's AEAD, R25's hash and the secrets section as
	// unreached as a wrong password leaves them.
	creds := []Credential{
		PasswordCredential{Password: slotPassword},
		RecoveryCredential{Key: rk},
		HardwareCredential{Token: tok, Password: vaultPassword},
	}
	for _, s := range []struct {
		name string
		b    []byte
	}{{"a.eks", aNewer}, {"b.eks", bNewer}, {"e.eks", entangled}, {"r.eks", rotated}} {
		path := filepath.Join(dir, "check-"+s.name)
		if err := os.WriteFile(path, s.b, 0o600); err != nil {
			f.Fatal(err)
		}
		k, err := Open(path)
		if err != nil {
			f.Fatalf("seed %s does not open: %v", s.name, err)
		}
		opened := false
		for _, c := range creds {
			u, err := k.Unlock(c)
			if err != nil {
				continue
			}
			if u.Tampered() != nil {
				f.Fatalf("seed %s is already tampered: %v", s.name, u.Tampered())
			}
			u.Close()
			opened = true
			break
		}
		// And a wrong password opens none of them. This belongs here, over a
		// fixture built in this process, rather than in the fuzz body: the
		// body sees arbitrary bytes, where a password that opens the file is
		// the fuzzer's own doing and no bug, and it cannot tell a seed from
		// anything else — the images carry vault ids, salts, ephemeral keys
		// and nonces drawn from the OS, so a corpus entry never equals the
		// fixture another process rebuilt. This check runs on every ordinary
		// `go test` and in every fuzz worker, which is what it was for.
		if _, err := k.Unlock(PasswordCredential{Password: "wrong"}); err == nil {
			f.Fatalf("a wrong password opened seed %s", s.name)
		}
		k.Close()
		if !opened {
			f.Fatalf("no credential opens seed %s", s.name)
		}
	}

	for _, s := range [][]byte{aNewer, bNewer, entangled, rotated} {
		f.Add(s, uint32(0), uint8(0))
	}
	f.Add(aNewer, uint32(format.KeystoreSuperblockAOff+16), uint8(1))
	f.Add(aNewer, uint32(format.SlotRegionAOff+8), uint8(0xff))
	// A byte inside the 32-byte slot region header (§6): argon2_m sits at
	// offset 8 of it, so this seed lands the Argon2 downgrade the guard below
	// is about.
	f.Add(entangled, uint32(format.SlotRegionAOff+10), uint8(0x40))
	// The slot region of a vault whose registry authenticates it (R25).
	f.Add(rotated, uint32(format.SlotRegionAOff+64), uint8(0x01))
	// The live superblock of the copy that won by being newer.
	f.Add(bNewer, uint32(format.KeystoreSuperblockBOff+24), uint8(0x80))
	f.Add(aNewer[:format.RegistryMinOff+10], uint32(0), uint8(0))

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
		// hardware credential, so the guard covers it too.
		if k.hdr.Argon2M > 1024 {
			return
		}
		for i := range k.slots {
			if k.slots[i].Argon2M > 1024 {
				return
			}
		}

		// Every credential the seeds carry, until one opens. A mutation the
		// authentication survives reaches everything Unlock does after the
		// slot; one it does not must leave the handle untouched.
		for _, c := range creds {
			had := k.reg != nil
			u, err := k.Unlock(c)
			if err != nil {
				switch {
				case errors.Is(err, ErrNoSlot), errors.Is(err, ErrVerifier), errors.Is(err, ErrAuth),
					errors.Is(err, ErrTampered), errors.Is(err, ErrPasswordRequired),
					errors.Is(err, ErrParams), errors.Is(err, kdf.ErrParams),
					errors.Is(err, format.ErrInvalid):
				default:
					t.Fatalf("unexpected Unlock error: %v", err)
				}
				if (k.reg != nil) != had {
					t.Fatal("a refused unlock installed a registry")
				}
				continue
			}
			if k.reg == nil || u.Registry() == nil {
				t.Fatal("an unlock that succeeded left no registry")
			}
			if u.Tampered() != nil && !errors.Is(u.Tampered(), ErrTampered) {
				t.Fatalf("Tampered is not ErrTampered: %v", u.Tampered())
			}
			// R25, checked here rather than trusted: the live region either
			// matches the hash the registry authenticates, or the handle says
			// it does not. It is never silently accepted, and a mismatch never
			// refuses the unlock.
			if err := u.Registry().VerifySlotRegion(k.region); (err != nil) != (u.Tampered() != nil) {
				t.Fatalf("Tampered %v for a region whose hash check was %v", u.Tampered(), err)
			}
			u.Close()
			break
		}
		// Opening and unlocking never write: a rejection leaves no half-made
		// state behind, and neither does a success.
		if got, err := os.ReadFile(p); err != nil || !bytes.Equal(got, data) {
			t.Fatalf("the file changed under Open/Unlock: %v", err)
		}
	})
}

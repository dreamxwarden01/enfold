package keystore

import (
	"bytes"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dreamxwarden01/enfold/internal/format"
)

// The review of the switch audit's repairs (2026-11-07): the preflight's
// verdict on the region's room is the encoding's, and the mutations not
// covered by preflight_test.go refuse a label before anything is derived.

// fits says yes exactly when the region the commit would encode does, for
// regions of records with labels of random lengths around the 128 KiB bound
// and around the 32-record cap; a record the format refuses for its label
// alone is not part of this (CheckLabel decides it before fits).
func TestFitsAgreesWithTheEncoding(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 11))
	tok := newToken(t)
	var kp, vmk [32]byte
	for i := 0; i < 400; i++ {
		n := rng.IntN(format.MaxSlots + 1)
		slots := make([]format.SlotRecord, n)
		// Labels that put the region near its bound: up to 8 records of up to
		// 20,000 bytes, the rest short.
		for j := range slots {
			label := strings.Repeat("y", rng.IntN(120))
			if j < 8 {
				label = strings.Repeat("y", rng.IntN(20000))
			}
			s, err := standIn(RecoverySlot{Label: label})
			if err != nil {
				t.Fatal(err)
			}
			s.RecipientID[0], s.RecipientID[1] = byte(j), 1
			slots[j] = s
		}
		var spec SlotSpec = HardwareSlot{PublicKey: tok.PublicKey(), Label: strings.Repeat("z", rng.IntN(MaxLabelChars+1))}
		if rng.IntN(2) == 0 {
			spec = PasswordSlot{Password: tradeWords, Argon2: fast, Label: strings.Repeat("z", rng.IntN(MaxLabelChars+1))}
		}
		stand, err := standIn(spec)
		if err != nil {
			t.Fatal(err)
		}
		real, err := newRecord(spec, [16]byte{1}, &kp, vmk, 1)
		if err != nil {
			t.Fatal(err)
		}
		region := format.SlotRegion{Slots: append(cloneSlots(slots), real)}
		_, encErr := region.Encode()
		fitErr := fits(slots, stand)
		if (encErr == nil) != (fitErr == nil) {
			t.Fatalf("case %d (%d records, %T): fits %v, the encoding %v", i, n, spec, fitErr, encErr)
		}
		if fitErr != nil && !errors.Is(fitErr, ErrFull) {
			t.Fatalf("case %d: fits refused with %v, not ErrFull", i, fitErr)
		}
	}
}

// AddSlot refuses a label before a password slot's Argon2id and before any
// write; Create refuses one before the vault's K_P is derived and before
// the file exists.
func TestAddSlotAndCreateRefuseALabelFirst(t *testing.T) {
	for name, label := range longLabels {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "v.eks")
			u := recoveryOnly(t, path, 2)
			before := snapshot(t, path)
			n := countArgon2(t)
			tok := newToken(t)
			if err := u.AddSlot(PasswordSlot{Password: tradeWords, Argon2: fast, Label: label}); !errors.Is(err, ErrParams) {
				t.Fatalf("AddSlot of a password: %v", err)
			}
			if err := u.AddSlot(HardwareSlot{PublicKey: tok.PublicKey(), Label: label}); !errors.Is(err, ErrParams) {
				t.Fatalf("AddSlot of a YubiKey: %v", err)
			}
			if !bytes.Equal(snapshot(t, path), before) {
				t.Fatal("a refused AddSlot wrote the file")
			}
			created := filepath.Join(t.TempDir(), "c.eks")
			_, err := Create(created, CreateOptions{
				Slots:    []SlotSpec{RecoverySlot{Key: recoveryKey(t), Label: "paper"}, HardwareSlot{PublicKey: tok.PublicKey(), Label: label}},
				Entangle: &Entangle{Password: tradeWords, Argon2: fast},
			})
			if !errors.Is(err, ErrParams) {
				t.Fatalf("Create: %v", err)
			}
			if _, err := os.Stat(created); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("the refused Create left a file: %v", err)
			}
			if *n != 0 {
				t.Fatalf("%d K_P derivations before the refusals", *n)
			}
		})
	}
}

package keystore

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/dreamxwarden01/enfold/internal/format"
	"github.com/dreamxwarden01/enfold/internal/kdf"
)

// The tests of the combined commits and the replacement: a change and its
// rotation in one flip, and the recovery keys replaced with a rotation in one
// flip (keystore tests 1 to 13, 21 and 32 of the VMK rotation design). Test 32
// sets commitFault, which is package-level, and TestPrepareRecoveryNamesTheSlotItMakes
// sets recipientIDs, so none of them runs in parallel.

// addArchive adds one archive record in its own registry save and returns it
// with the archive key it wraps, so that a test can show the key still
// unwraps after a rotation re-wrapped it.
func addArchive(t *testing.T, u *Unlocked) (format.ArchiveRecord, [32]byte) {
	t.Helper()
	var x format.ArchiveRecord
	var key [32]byte
	withSession(t, u, func(s *Session) {
		if err := s.UpdateRegistry(func(g *format.Registry) error {
			x, key = archiveWithKey(t, s, "the archive")
			g.Archives = append(g.Archives, x)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
	return x, key
}

// requireArchiveKey checks that the archive record x, as the handle's current
// registry holds it, unwraps to key through a Session the same Unlocked
// derives: the rotation re-wrapped it under the new KWK, and the handle holds
// the new VMK.
func requireArchiveKey(t *testing.T, u *Unlocked, x format.ArchiveRecord, key [32]byte) {
	t.Helper()
	var cur *format.ArchiveRecord
	for i := range u.Registry().Archives {
		if u.Registry().Archives[i].ArchiveID == x.ArchiveID {
			cur = &u.Registry().Archives[i]
		}
	}
	if cur == nil {
		t.Fatal("the archive record is gone")
	}
	if cur.Versions[0].WrappedArchiveKey == x.Versions[0].WrappedArchiveKey {
		t.Fatal("the archive key was not re-wrapped")
	}
	withSession(t, u, func(s *Session) {
		got, err := s.UnwrapArchiveKey(cur.ArchiveID, &cur.Versions[0])
		if err != nil || got != key {
			t.Fatalf("the archive key under the new KWK: %v", err)
		}
	})
}

// openSecret opens one record of the handle's secrets section under the
// KWK_secrets its VMK derives.
func openSecret(t *testing.T, u *Unlocked, kind format.SecretKind, id [16]byte) ([32]byte, error) {
	t.Helper()
	rec := u.Registry().Secret(kind, id)
	if rec == nil {
		return [32]byte{}, ErrNotFound
	}
	vaultID := u.k.sb.VaultID
	kwks := kdf.KWKSecrets(u.vmk(), vaultID)
	defer kdf.Zero(kwks)
	return openSecretWith(kwks, vaultID, rec)
}

// recordOf is the live record with this label.
func recordOf(t testing.TB, k *Keystore, label string) format.SlotRecord {
	t.Helper()
	for _, s := range k.slots {
		if s.Label == label {
			return s
		}
	}
	t.Fatalf("no slot labelled %q", label)
	return format.SlotRecord{}
}

// crashedBefore is after with the superblock copy the commit flipped given
// back its bytes from before: a power loss that took the superblock write
// and nothing else (as TestCrashBeforeFlip makes it).
func crashedBefore(before, after []byte, live format.Copy) []byte {
	crashed := bytes.Clone(after)
	target := live.Other().KeystoreSuperblockOff()
	copy(crashed[target:target+format.SuperblockSize], before[target:target+format.SuperblockSize])
	return crashed
}

// requireUnchanged checks that a refused call left the file's bytes and the
// handle's generation as they were.
func requireUnchanged(t *testing.T, u *Unlocked, path string, before []byte, gen uint64, what string) {
	t.Helper()
	if !bytes.Equal(snapshot(t, path), before) {
		t.Fatalf("%s: the file's bytes changed", what)
	}
	if u.k.Generation() != gen || u.gen != gen {
		t.Fatalf("%s: generation %d, handle %d, want %d", what, u.k.Generation(), u.gen, gen)
	}
}

// unlockIs opens the keystore with c and checks the outcome: want nil for an
// unlock at generation gen, otherwise the error it must be.
func unlockIs(t *testing.T, k *Keystore, c Credential, gen uint64, want error, what string) {
	t.Helper()
	u, err := k.Unlock(c)
	if err == nil {
		defer u.Close()
	}
	switch {
	case want == nil && err != nil:
		t.Fatalf("%s: %v", what, err)
	case want == nil && u.gen != gen:
		t.Fatalf("%s: opened at generation %d, want %d", what, u.gen, gen)
	case want != nil && !errors.Is(err, want):
		t.Fatalf("%s: %v, want %v", what, err, want)
	}
}

// 1. A removal and its rotation in one commit: one flip to generation 2, the
// removed slot's escrow dropped and K_P kept, every archive key re-wrapped,
// and the removed key no longer the key of any slot.
func TestRemoveSlotAndRotate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	v := newRetireVault(t, path, 0)
	u, k := v.u, v.u.k
	x, key := addArchive(t, u)
	kp := *u.kp()
	seq := k.sb.Seq
	if err := u.RemoveSlotAndRotate(v.spareRec.RecipientID); err != nil {
		t.Fatal(err)
	}
	if k.sb.Seq != seq+1 {
		t.Fatalf("seq %d after %d: not one commit", k.sb.Seq, seq)
	}
	if k.Generation() != 2 || u.gen != 2 {
		t.Fatalf("generation %d, handle %d", k.Generation(), u.gen)
	}
	if got := historyGenerations(u.Registry()); len(got) != 1 || got[0] != 1 {
		t.Fatalf("history %v", got)
	}
	if u.Registry().Secret(format.SecretRecoveryEscrow, v.spareRec.RecipientID) != nil {
		t.Fatal("the removed slot's escrow record was carried forward")
	}
	if n := secretsOf(u.Registry(), format.SecretRecoveryEscrow); n != 1 {
		t.Fatalf("%d escrow records, want the paper's alone", n)
	}
	for _, s := range k.slots {
		if s.RecipientID == v.spareRec.RecipientID {
			t.Fatal("the removed slot is still in the set")
		}
	}
	if *u.kp() != kp {
		t.Fatal("the handle's K_P changed")
	}
	if got, err := openSecret(t, u, format.SecretEntangledKey, [16]byte{}); err != nil || got != kp {
		t.Fatalf("the entangled_key record under the new KWK_secrets: %v", err)
	}
	requireArchiveKey(t, u, x, key)
	u.Close()
	k.Close()

	k = mustOpen(t, path)
	unlockIs(t, k, HardwareCredential{Token: v.tok, Password: v.pass}, 2, nil, "the token with the password")
	unlockIs(t, k, RecoveryCredential{Key: v.paper}, 2, nil, "paper")
	unlockIs(t, k, RecoveryCredential{Key: v.spare}, 0, ErrVerifier, "the removed spare")
}

// 2. A removal the set does not allow, or of no slot, refuses before any key
// is drawn: nothing is written and the generation stays.
func TestRemoveSlotAndRotateRefusesBeforeDrawing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	u := mustCreate(t, path, PasswordSlot{Password: "p", Argon2: fast}, RecoverySlot{Key: recoveryKey(t)})
	before := snapshot(t, path)
	if err := u.RemoveSlotAndRotate([16]byte{9}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unknown ID: %v", err)
	}
	requireUnchanged(t, u, path, before, 1, "an unknown ID")
	// TestInvariant's set: the password slot alone would remain.
	if err := u.RemoveSlotAndRotate(u.k.slots[1].RecipientID); !errors.Is(err, ErrInvariant) {
		t.Fatalf("the last recovery slot beside a password: %v", err)
	}
	requireUnchanged(t, u, path, before, 1, "a removal the invariant forbids")
	if len(u.k.slots) != 2 {
		t.Fatal("the refused removal changed the handle's set")
	}
}

// 3. A crash before the flip leaves the old state whole: the slot there and
// the removed credential still opening generation 1; after the flip, neither.
func TestRemoveSlotAndRotateCrashBeforeFlip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	v := newRetireVault(t, path, 1)
	before := snapshot(t, path)
	live := v.u.k.live
	if err := v.u.RemoveSlotAndRotate(v.spareRec.RecipientID); err != nil {
		t.Fatal(err)
	}
	v.u.Close()
	v.u.k.Close()
	after := snapshot(t, path)

	restore(t, path, crashedBefore(before, after, live))
	k := mustOpen(t, path)
	if k.Generation() != 1 || len(k.Slots()) != 3 {
		t.Fatalf("crashed: generation %d, %d slots", k.Generation(), len(k.Slots()))
	}
	unlockIs(t, k, RecoveryCredential{Key: v.spare}, 1, nil, "the spare in the old state")
	k.Close()

	restore(t, path, after)
	k = mustOpen(t, path)
	if k.Generation() != 2 || len(k.Slots()) != 2 {
		t.Fatalf("flipped: generation %d, %d slots", k.Generation(), len(k.Slots()))
	}
	unlockIs(t, k, RecoveryCredential{Key: v.spare}, 0, ErrVerifier, "the spare in the new state")
}

// 4. Before any retirement the inactive copy still holds the removed slot,
// wrapping the retiring VMK. A live superblock pointed at that copy — both
// its offset and its length taken from the inactive copy's own superblock,
// the checksum recomputed — does not let the removed credential in: the slot
// opens to generation 1 under a superblock at 2, a verdict of tampering
// (§6.2), and no handle is returned.
func TestRemoveSlotAndRotateDefeatsTheSplice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	v := newRetireVault(t, path, 1)
	if err := v.u.RemoveSlotAndRotate(v.spareRec.RecipientID); err != nil {
		t.Fatal(err)
	}
	if !holdsSlot(t, path, v.spareRec) {
		t.Fatal("the fixture: the inactive copy no longer holds the spare")
	}
	v.u.Close()
	v.u.k.Close()
	img := snapshot(t, path)
	sb, c := liveOf(t, img)
	inactive := sbCopy(t, img, c.Other())
	if inactive.SlotRegionOff == sb.SlotRegionOff {
		t.Fatal("the fixture: both superblock copies name one region copy")
	}
	sb.SlotRegionOff, sb.SlotRegionLen = inactive.SlotRegionOff, inactive.SlotRegionLen
	putSB(t, img, c, sb)
	restore(t, path, img)
	k := mustOpen(t, path)
	if k.Generation() != 2 || len(k.Slots()) != 3 {
		t.Fatalf("the splice: generation %d, %d slots", k.Generation(), len(k.Slots()))
	}
	u, err := k.Unlock(RecoveryCredential{Key: v.spare})
	if !errors.Is(err, ErrTampered) || u != nil {
		t.Fatalf("the removed credential through the splice: %v, handle %v", err, u != nil)
	}
}

// 5. A kept secrets record that does not open fails the combined commit
// closed, as it fails a rotation: nothing written, the slot still there. The
// removed slot's own escrow record is pruned before the loop, never opened,
// so its damage refuses nothing.
func TestRemoveSlotAndRotateFailsClosed(t *testing.T) {
	damage := func(t *testing.T, u *Unlocked, rid [16]byte) {
		t.Helper()
		if err := u.UpdateRegistry(func(g *format.Registry) error {
			rec := *g.Secret(format.SecretRecoveryEscrow, rid)
			rec.Ciphertext[0] ^= 1
			g.SetSecret(rec)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("a kept record", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "v.eks")
		v := newRetireVault(t, path, 1)
		damage(t, v.u, recordOf(t, v.u.k, "paper").RecipientID)
		before := snapshot(t, path)
		if err := v.u.RemoveSlotAndRotate(v.spareRec.RecipientID); !errors.Is(err, format.ErrInvalid) {
			t.Fatalf("over a record that does not open: %v", err)
		}
		requireUnchanged(t, v.u, path, before, 1, "the failed combined commit")
		if recordOf(t, v.u.k, "spare").RecipientID != v.spareRec.RecipientID {
			t.Fatal("the slot is gone")
		}
	})
	t.Run("the removed slot's own record", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "v.eks")
		v := newRetireVault(t, path, 1)
		damage(t, v.u, v.spareRec.RecipientID)
		if err := v.u.RemoveSlotAndRotate(v.spareRec.RecipientID); err != nil {
			t.Fatalf("the removed slot's damaged record was opened: %v", err)
		}
	})
}

// 6. On a region that fails R25 the combined commit refuses as a removal
// does, before anything is drawn or written.
func TestRemoveSlotAndRotateOnATamperedRegion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	u, k := tamperedVault(t, path)
	before := snapshot(t, path)
	// Without the tampering this removal would be allowed: a password and a
	// recovery key remain.
	if err := u.RemoveSlotAndRotate(k.slots[2].RecipientID); !errors.Is(err, ErrTampered) {
		t.Fatalf("on a tampered region: %v", err)
	}
	requireUnchanged(t, u, path, before, 1, "the refused removal")
}

// tamperedVault is TestTamperedRegion's file with a second recovery slot: the
// first recovery slot's public key replaced in the live region by another
// valid one, so the region decodes and fails R25. It returns an Unlocked
// opened with the password, over a Keystore the test owns.
func tamperedVault(t *testing.T, path string) (*Unlocked, *Keystore) {
	t.Helper()
	u := mustCreate(t, path, PasswordSlot{Password: "p", Argon2: fast}, RecoverySlot{Key: recoveryKey(t), Label: "one"}, RecoverySlot{Key: recoveryKey(t), Label: "two"})
	u.Close()
	u.k.Close()
	k := mustOpen(t, path)
	slots := cloneSlots(k.slots)
	var fake [32]byte
	rand.Read(fake[:])
	fake[31] &= 0x7f
	slots[1].SlotPubkey = fake[:]
	region, err := (&format.SlotRegion{Header: k.hdr, Slots: slots}).Encode()
	if err != nil || len(region) != len(k.region) {
		t.Fatalf("the tampered region: %v", err)
	}
	b := snapshot(t, path)
	copy(b[k.sb.SlotRegionOff:], region)
	k.Close()
	restore(t, path, b)
	k = mustOpen(t, path)
	u, err = k.Unlock(PasswordCredential{Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(u.Close)
	if !errors.Is(u.Tampered(), ErrTampered) {
		t.Fatalf("the fixture: Tampered %v", u.Tampered())
	}
	return u, k
}

// tokenVault is a token and a recovery key, with the vault's password on when
// pass is not empty: tests 7 to 10's set.
func tokenVault(t *testing.T, path, pass string) (*Unlocked, *softToken, kdf.RecoveryKey) {
	t.Helper()
	tok, rk := newToken(t), recoveryKey(t)
	opts := CreateOptions{Slots: []SlotSpec{HardwareSlot{PublicKey: tok.PublicKey(), Label: "A"}, RecoverySlot{Key: rk, Label: "paper"}}}
	if pass != "" {
		opts.Entangle = &Entangle{Password: pass, Argon2: fast}
	}
	return mustCreateWith(t, path, opts), tok, rk
}

// 7. A password change and its rotation in one commit: a new salt, a new K_P
// that the entangled_key record holds under the new KWK_secrets, the old
// password no longer opening the token's slot. A secrets section whose
// entangled_key does not open refuses with nothing written.
func TestChangeEntangledPasswordAndRotate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	u, tok, rk := tokenVault(t, path, "the old one")
	k := u.k
	oldKP, oldSalt, seq := *u.kp(), k.hdr.EntangleSalt, k.sb.Seq
	if err := u.ChangeEntangledPasswordAndRotate(Entangle{Password: "the new one", Argon2: fast}); err != nil {
		t.Fatal(err)
	}
	if k.sb.Seq != seq+1 || k.Generation() != 2 || u.gen != 2 {
		t.Fatalf("seq %d after %d, generation %d, handle %d", k.sb.Seq, seq, k.Generation(), u.gen)
	}
	if got := historyGenerations(u.Registry()); len(got) != 1 || got[0] != 1 {
		t.Fatalf("history %v", got)
	}
	if !k.hdr.Entangle || k.hdr.EntangleSalt == oldSalt {
		t.Fatal("the header kept its salt")
	}
	newKP := *u.kp()
	if newKP == oldKP {
		t.Fatal("the handle kept the old K_P")
	}
	got, err := openSecret(t, u, format.SecretEntangledKey, [16]byte{})
	if err != nil || got != newKP || got == oldKP {
		t.Fatalf("the entangled_key record under the new KWK_secrets: %v, new %v, old %v", err, got == newKP, got == oldKP)
	}
	u.Close()
	k.Close()

	k = mustOpen(t, path)
	unlockIs(t, k, HardwareCredential{Token: tok, Password: "the new one"}, 2, nil, "the token with the new password")
	unlockIs(t, k, HardwareCredential{Token: tok, Password: "the old one"}, 0, ErrAuth, "the token with the old password")
	unlockIs(t, k, RecoveryCredential{Key: rk}, 2, nil, "the recovery key")
	k.Close()

	t.Run("an entangled_key that does not open", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "v.eks")
		u, _, _ := tokenVault(t, path, "the old one")
		kp := *u.kp()
		if err := u.UpdateRegistry(func(g *format.Registry) error {
			rec := *g.Secret(format.SecretEntangledKey, [16]byte{})
			rec.Ciphertext[0] ^= 1
			g.SetSecret(rec)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		before := snapshot(t, path)
		hdr := u.k.hdr
		if err := u.ChangeEntangledPasswordAndRotate(Entangle{Password: "the new one", Argon2: fast}); !errors.Is(err, format.ErrInvalid) {
			t.Fatalf("over an entangled_key that does not open: %v", err)
		}
		requireUnchanged(t, u, path, before, 1, "the refused change")
		if *u.kp() != kp || u.k.hdr != hdr {
			t.Fatal("the refused change reached the handle")
		}
	})
}

// 8. With the switch off there is no password to change: refused before
// anything is derived or written.
func TestChangeEntangledPasswordAndRotateRefusedWhileOff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	u, _, _ := tokenVault(t, path, "")
	before := snapshot(t, path)
	n := countArgon2(t)
	if err := u.ChangeEntangledPasswordAndRotate(Entangle{Password: "new", Argon2: fast}); !errors.Is(err, ErrParams) {
		t.Fatalf("while the switch is off: %v", err)
	}
	requireUnchanged(t, u, path, before, 1, "the refused change")
	if *n != 0 {
		t.Fatalf("the refused change ran Argon2id %d times", *n)
	}
}

// 9. A crash before the flip leaves the old password opening generation 1.
func TestChangeEntangledPasswordAndRotateCrashBeforeFlip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	u, tok, _ := tokenVault(t, path, "the old one")
	before := snapshot(t, path)
	live := u.k.live
	if err := u.ChangeEntangledPasswordAndRotate(Entangle{Password: "the new one", Argon2: fast}); err != nil {
		t.Fatal(err)
	}
	u.Close()
	u.k.Close()
	after := snapshot(t, path)
	restore(t, path, crashedBefore(before, after, live))
	k := mustOpen(t, path)
	unlockIs(t, k, HardwareCredential{Token: tok, Password: "the old one"}, 1, nil, "the old password in the old state")
	unlockIs(t, k, HardwareCredential{Token: tok, Password: "the new one"}, 0, ErrAuth, "the new password in the old state")
	k.Close()
	restore(t, path, after)
	k = mustOpen(t, path)
	unlockIs(t, k, HardwareCredential{Token: tok, Password: "the new one"}, 2, nil, "the new password after the flip")
}

// 10. Turning the password on and rotating in one commit: the header's switch
// on with a fresh salt, exactly one entangled_key record holding the handle's
// K_P under the new KWK_secrets, and the token no longer opening without the
// password. Refused while the switch is on, and, on a set the switch would
// leave without two independent ways in, before the password is derived.
func TestSetEntangledAndRotate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	u, tok, rk := tokenVault(t, path, "")
	k := u.k
	if err := u.SetEntangledAndRotate(Entangle{Password: "now on", Argon2: fast}); err != nil {
		t.Fatal(err)
	}
	if k.Generation() != 2 || u.gen != 2 {
		t.Fatalf("generation %d, handle %d", k.Generation(), u.gen)
	}
	if got := historyGenerations(u.Registry()); len(got) != 1 || got[0] != 1 {
		t.Fatalf("history %v", got)
	}
	if !k.hdr.Entangle || k.hdr.EntangleSalt == ([16]byte{}) {
		t.Fatalf("the header: %+v", k.hdr)
	}
	if n := secretsOf(u.Registry(), format.SecretEntangledKey); n != 1 {
		t.Fatalf("%d entangled_key records", n)
	}
	if u.kp() == nil {
		t.Fatal("the handle holds no K_P")
	}
	if got, err := openSecret(t, u, format.SecretEntangledKey, [16]byte{}); err != nil || got != *u.kp() {
		t.Fatalf("the entangled_key record under the new KWK_secrets: %v", err)
	}

	before := snapshot(t, path)
	if err := u.SetEntangledAndRotate(Entangle{Password: "again", Argon2: fast}); !errors.Is(err, ErrParams) {
		t.Fatalf("while the switch is on: %v", err)
	}
	requireUnchanged(t, u, path, before, 2, "the refused second switch")
	u.Close()
	k.Close()
	k = mustOpen(t, path)
	unlockIs(t, k, HardwareCredential{Token: tok, Password: "now on"}, 2, nil, "the token with the password")
	unlockIs(t, k, HardwareCredential{Token: tok}, 0, ErrPasswordRequired, "the token without it")
	unlockIs(t, k, RecoveryCredential{Key: rk}, 2, nil, "the recovery key")
	k.Close()

	t.Run("a hardware-only set", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "hardware.eks")
		u := mustCreate(t, path, HardwareSlot{PublicKey: newToken(t).PublicKey()}, HardwareSlot{PublicKey: newToken(t).PublicKey()})
		before := snapshot(t, path)
		n := countArgon2(t)
		// Parameters that fail R24 if derived: the verdict must come first.
		if err := u.SetEntangledAndRotate(Entangle{Password: "x"}); !errors.Is(err, ErrInvariant) {
			t.Fatalf("on a hardware-only set: %v", err)
		}
		requireUnchanged(t, u, path, before, 1, "the refused switch")
		if *n != 0 || u.k.hdr.Entangle {
			t.Fatalf("the refused switch derived K_P %d times, header %+v", *n, u.k.hdr)
		}
	})
}

// replaceVault is test 11's set: a token with the password on, recovery keys
// "safe" and "office", and one archive record.
type replaceVault struct {
	path         string
	u            *Unlocked
	tok          *softToken
	safe, office kdf.RecoveryKey
	olds         []format.SlotRecord
	x            format.ArchiveRecord
	key          [32]byte
}

const replacePass = "the vault's password"

func newReplaceVault(t *testing.T, path string) *replaceVault {
	t.Helper()
	v := &replaceVault{path: path, tok: newToken(t), safe: recoveryKey(t), office: recoveryKey(t)}
	v.u = mustCreateWith(t, path, CreateOptions{
		Entangle: &Entangle{Password: replacePass, Argon2: fast},
		Slots: []SlotSpec{
			HardwareSlot{PublicKey: v.tok.PublicKey(), Label: "A"},
			RecoverySlot{Key: v.safe, Label: "safe"},
			RecoverySlot{Key: v.office, Label: "office"},
		},
	})
	v.x, v.key = addArchive(t, v.u)
	v.olds = []format.SlotRecord{recordOf(t, v.u.k, "safe"), recordOf(t, v.u.k, "office")}
	return v
}

// prepared makes one new key for each old recovery slot, with its label, as
// the replacement's ceremony does, and zeroes them when the test ends.
func (v *replaceVault) prepared(t *testing.T) ([]*PreparedRecovery, []kdf.RecoveryKey) {
	t.Helper()
	var preps []*PreparedRecovery
	var keys []kdf.RecoveryKey
	for _, old := range v.olds {
		r := recoveryKey(t)
		p, err := v.u.PrepareRecovery(r, old.Label, preps)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Zero)
		preps = append(preps, p)
		keys = append(keys, r)
	}
	return preps, keys
}

// 11. The replacement: every old recovery slot gone and its escrow with it,
// the new ones wrapped at the new generation with their escrow sealed under
// the new KWK_secrets alone, the labels carried, K_P kept and every archive
// key re-wrapped; reopened, the new keys open it and the old ones do not.
// An old recovery record kept retired goes as well (the A2 review's N1).
func TestReplaceRecoveryAndRotate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	v := newReplaceVault(t, path)
	u, k := v.u, v.u.k
	kp, seq := *u.kp(), k.sb.Seq
	preps, keys := v.prepared(t)
	if err := u.ReplaceRecoveryAndRotate(preps); err != nil {
		t.Fatal(err)
	}
	if k.sb.Seq != seq+1 || k.Generation() != 2 || u.gen != 2 {
		t.Fatalf("seq %d after %d, generation %d, handle %d", k.sb.Seq, seq, k.Generation(), u.gen)
	}
	if got := historyGenerations(u.Registry()); len(got) != 1 || got[0] != 1 {
		t.Fatalf("history %v", got)
	}
	escrow := u.Registry().SecretsOfKind(format.SecretRecoveryEscrow)
	if len(escrow) != 2 {
		t.Fatalf("%d escrow records", len(escrow))
	}
	for i, p := range preps {
		rid := p.RecipientID()
		got, err := openSecret(t, u, format.SecretRecoveryEscrow, rid)
		if err != nil {
			t.Fatalf("key %d's escrow under the new KWK_secrets: %v", i+1, err)
		}
		r, err := kdf.RecoveryKeyFromPadded(got)
		if err != nil || r != keys[i] {
			t.Fatalf("key %d's escrow holds another key: %v", i+1, err)
		}
		if !u.keyFitsSlot(rid, keys[i]) {
			t.Fatalf("key %d does not derive its slot's public key", i+1)
		}
	}
	for _, s := range k.slots {
		for _, old := range v.olds {
			if s.Type == format.SlotRecovery && s.RecipientID == old.RecipientID {
				t.Fatalf("the old recovery slot %q is still in the set", old.Label)
			}
		}
	}
	if *u.kp() != kp {
		t.Fatal("the handle's K_P changed")
	}
	if got, err := openSecret(t, u, format.SecretEntangledKey, [16]byte{}); err != nil || got != kp {
		t.Fatalf("the entangled_key record: %v", err)
	}
	requireArchiveKey(t, u, v.x, v.key)
	u.Close()
	k.Close()

	k = mustOpen(t, path)
	for i, r := range keys {
		unlockIs(t, k, RecoveryCredential{Key: r}, 2, nil, "a new key")
		if got := recordOf(t, k, v.olds[i].Label); got.RecipientID != preps[i].RecipientID() {
			t.Fatalf("the slot labelled %q is not the new key %d's", v.olds[i].Label, i+1)
		}
	}
	unlockIs(t, k, RecoveryCredential{Key: v.safe}, 0, ErrVerifier, "the old safe key")
	unlockIs(t, k, RecoveryCredential{Key: v.office}, 0, ErrVerifier, "the old office key")
	uu, err := k.Unlock(HardwareCredential{Token: v.tok, Password: replacePass})
	if err != nil {
		t.Fatalf("the token with the password: %v", err)
	}
	defer uu.Close()
	for i, p := range preps {
		if r, err := uu.RecoveryKey(p.RecipientID()); err != nil || r != keys[i] {
			t.Fatalf("the reveal of new key %d: %v", i+1, err)
		}
	}

	// A recovery record kept retired goes too: every record of slot_type 3
	// is removed whatever its state, so that no wrap of an older VMK rides
	// into the new state, and once retired the file holds no byte of it.
	// No Enfold writer makes such a record; a foreign or hand-made file can.
	rpath := filepath.Join(t.TempDir(), "retired.eks")
	rv := newReplaceVault(t, rpath)
	kept := cloneSlots(rv.u.k.slots)
	for i := range kept {
		if kept[i].RecipientID == rv.olds[1].RecipientID {
			kept[i].State = format.SlotRetired
		}
	}
	if err := rv.u.commitSlots(rv.u.k.hdr, kept, nil); err != nil {
		t.Fatal(err)
	}
	if err := rv.u.Retire(); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(rv.u.k.slots, func(s format.SlotRecord) bool {
		return s.RecipientID == rv.olds[1].RecipientID && s.State == format.SlotRetired
	}) || !holdsSlot(t, rpath, rv.olds[1]) {
		t.Fatal("the control: the set does not keep the office key's record retired")
	}
	rpreps, _ := rv.prepared(t)
	if err := rv.u.ReplaceRecoveryAndRotate(rpreps); err != nil {
		t.Fatal(err)
	}
	for _, s := range rv.u.k.slots {
		for _, old := range rv.olds {
			if s.Type == format.SlotRecovery && s.RecipientID == old.RecipientID {
				t.Fatalf("the old recovery record %q, state %d, is still in the set", old.Label, s.State)
			}
		}
	}
	if err := rv.u.Retire(); err != nil {
		t.Fatal(err)
	}
	for _, old := range rv.olds {
		if holdsSlot(t, rpath, old) {
			t.Fatalf("the raw bytes still hold the old recovery record %q", old.Label)
		}
	}
	rv.u.Close()
	rv.u.k.Close()
}

// 12. The replacement refuses with none prepared, with one prepared twice or
// already in the set, with one prepared for another vault, nil or already
// zeroed, on a set the invariant forbids, and on a tampered region: each
// before any key is drawn, with the file's bytes unchanged.
func TestReplaceRecoveryAndRotateRefuses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	v := newReplaceVault(t, path)
	u := v.u
	before := snapshot(t, path)
	refused := func(news []*PreparedRecovery, want error, what string) {
		t.Helper()
		if err := u.ReplaceRecoveryAndRotate(news); !errors.Is(err, want) {
			t.Fatalf("%s: %v, want %v", what, err, want)
		}
		requireUnchanged(t, u, path, before, 1, what)
	}
	refused(nil, ErrParams, "none prepared")
	preps, _ := v.prepared(t)
	refused([]*PreparedRecovery{preps[0], preps[0]}, ErrDuplicate, "one prepared twice")
	inSet := *preps[1]
	inSet.rec.RecipientID = v.olds[0].RecipientID
	refused([]*PreparedRecovery{preps[0], &inSet}, ErrDuplicate, "an ID already in the set")

	other := filepath.Join(t.TempDir(), "other.eks")
	ou := mustCreate(t, other, PasswordSlot{Password: "p", Argon2: fast}, RecoverySlot{Key: recoveryKey(t)})
	foreign, err := ou.PrepareRecovery(recoveryKey(t), "elsewhere", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer foreign.Zero()
	refused([]*PreparedRecovery{preps[0], foreign}, ErrParams, "a slot prepared for another vault")
	refused([]*PreparedRecovery{preps[0], nil}, ErrParams, "a nil slot")
	zeroed, err := u.PrepareRecovery(recoveryKey(t), "zeroed", preps)
	if err != nil {
		t.Fatal(err)
	}
	zeroed.Zero()
	refused([]*PreparedRecovery{preps[0], zeroed}, ErrParams, "a zeroed slot")

	// Two recovery keys alone: one new key in their place would leave a
	// single way in, which the invariant refuses.
	rpath := filepath.Join(t.TempDir(), "recovery-only.eks")
	ru := mustCreate(t, rpath, RecoverySlot{Key: recoveryKey(t)}, RecoverySlot{Key: recoveryKey(t)})
	one, err := ru.PrepareRecovery(recoveryKey(t), "one", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer one.Zero()
	rbefore := snapshot(t, rpath)
	if err := ru.ReplaceRecoveryAndRotate([]*PreparedRecovery{one}); !errors.Is(err, ErrInvariant) {
		t.Fatalf("two recovery keys replaced by one: %v", err)
	}
	requireUnchanged(t, ru, rpath, rbefore, 1, "the replacement the invariant refuses")

	tpath := filepath.Join(t.TempDir(), "tampered.eks")
	tu, _ := tamperedVault(t, tpath)
	tp, err := tu.PrepareRecovery(recoveryKey(t), "new", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tp.Zero()
	tbefore := snapshot(t, tpath)
	if err := tu.ReplaceRecoveryAndRotate([]*PreparedRecovery{tp}); !errors.Is(err, ErrTampered) {
		t.Fatalf("on a tampered region: %v", err)
	}
	requireUnchanged(t, tu, tpath, tbefore, 1, "the replacement on a tampered region")
}

// collidingIDs is the redraw's test seam: a source of recipient IDs whose
// first n draws begin with prefix, every byte after it random. draws counts
// what was asked of it.
type collidingIDs struct {
	prefix [4]byte
	n      int
	draws  int
}

func (c *collidingIDs) Read(b []byte) (int, error) {
	c.draws++
	if _, err := rand.Read(b); err != nil {
		return 0, err
	}
	if c.draws <= c.n {
		copy(b, c.prefix[:])
	}
	return len(b), nil
}

// drawIDsFrom sets recipientIDs for the rest of the test.
func drawIDsFrom(t *testing.T, r io.Reader) {
	t.Helper()
	saved := recipientIDs
	recipientIDs = r
	t.Cleanup(func() { recipientIDs = saved })
}

// 12, continued. A prepared slot is the one its key makes: its public keys
// derived from the key, nothing wrapped yet, its label carried, an ID whose
// first four bytes are no active slot's and no other prepared slot's (drawn
// again when they are), its key in a locked page that Zero erases, and
// nothing written.
func TestPrepareRecoveryNamesTheSlotItMakes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	v := newReplaceVault(t, path)
	u := v.u
	before := snapshot(t, path)
	r := recoveryKey(t)
	p, err := u.PrepareRecovery(r, "safe", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Zero()
	skX, dk, err := recoveryKeys(&p.rec, r, u.k.sb.VaultID)
	if err != nil {
		t.Fatal(err)
	}
	pub, ek := kdf.HybridPublic(skX, dk)
	if !bytes.Equal(pub, p.rec.SlotPubkey) || !bytes.Equal(ek, p.rec.MLKEMEK) {
		t.Fatal("the prepared public keys are not the ones the key derives")
	}
	if p.rec.Type != format.SlotRecovery || p.rec.State != format.SlotActive || p.rec.Label != "safe" || p.RecipientID() != p.rec.RecipientID {
		t.Fatalf("the prepared record: type %d, state %d, label %q", p.rec.Type, p.rec.State, p.rec.Label)
	}
	if len(p.rec.EPK) != 0 || p.rec.WrappedVMK != ([format.WrappedVMKSize]byte{}) || p.rec.WrapNonce != ([format.NonceSize]byte{}) {
		t.Fatal("the prepared record has something wrapped into it")
	}
	prefixFree := func(rid [16]byte, others ...*PreparedRecovery) {
		t.Helper()
		for _, s := range u.k.slots {
			if s.State == format.SlotActive && bytes.Equal(s.RecipientID[:4], rid[:4]) {
				t.Fatalf("the ID %x shares its first four bytes with slot %q", rid, s.Label)
			}
		}
		for _, o := range others {
			if bytes.Equal(o.rec.RecipientID[:4], rid[:4]) {
				t.Fatalf("the ID %x shares its first four bytes with another prepared slot", rid)
			}
		}
	}
	prefixFree(p.RecipientID())

	// The redraw, shown by a source whose first draw collides: with an
	// active slot, then with the slot already prepared.
	for _, c := range []struct {
		what   string
		prefix [16]byte
	}{{"an active slot", recordOf(t, u.k, "A").RecipientID}, {"a prepared slot", p.RecipientID()}} {
		ids := &collidingIDs{prefix: [4]byte(c.prefix[:4]), n: 1}
		drawIDsFrom(t, ids)
		q, err := u.PrepareRecovery(recoveryKey(t), "office", []*PreparedRecovery{p})
		if err != nil {
			t.Fatal(err)
		}
		q.Zero()
		if ids.draws != 2 {
			t.Fatalf("a first draw colliding with %s: %d draws", c.what, ids.draws)
		}
		prefixFree(q.RecipientID(), p)
	}
	// A source that never stops colliding is refused, not looped on.
	drawIDsFrom(t, &collidingIDs{prefix: [4]byte(p.rec.RecipientID[:4]), n: 1 << 30})
	if _, err := u.PrepareRecovery(recoveryKey(t), "office", []*PreparedRecovery{p}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("a source that always collides: %v", err)
	}

	buf := p.key
	if !bytes.Equal(buf.Bytes(), r[:]) {
		t.Fatal("the prepared slot's page does not hold its key")
	}
	if runtime.GOOS == "windows" && !buf.Locked() {
		t.Fatal("the prepared key's page is not locked")
	}
	p.Zero()
	if buf.Zeros() == 0 || buf.Bytes() != nil {
		t.Fatal("Zero did not erase the prepared key's page")
	}
	p.Zero() // a second Zero does nothing
	if !bytes.Equal(snapshot(t, path), before) {
		t.Fatal("preparing wrote to the file")
	}
}

// The rotation beneath the combined commits takes a new header only with the
// K_P it derived, and new recovery slots only with their escrow, one each
// (R38): arguments that disagree are refused before any key is drawn. A
// recovery slot the live set already has keeps the escrow record it has, so a
// vault whose record went missing still rotates, as it did before.
func TestRotateTakesOnlyArgumentsThatAgree(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	v := newReplaceVault(t, path)
	u := v.u
	before := snapshot(t, path)
	preps, keys := v.prepared(t)
	var kp [32]byte
	on := format.SlotRegionHeader{Entangle: true}
	off := format.SlotRegionHeader{}
	withNew := append(cloneSlots(u.k.slots), cloneSlots([]format.SlotRecord{preps[0].rec})...)
	add := func(i int) recoveryAdd { return recoveryAdd{rid: preps[i].RecipientID(), key: keys[i]} }
	for _, c := range []struct {
		what  string
		slots []format.SlotRecord
		hdr   *format.SlotRegionHeader
		kp    *[32]byte
		adds  []recoveryAdd
	}{
		{"a header without its K_P", cloneSlots(u.k.slots), &on, nil, nil},
		{"a K_P without its header", cloneSlots(u.k.slots), nil, &kp, nil},
		{"a K_P under a header whose password is off", cloneSlots(u.k.slots), &off, &kp, nil},
		{"a new recovery slot without its escrow", withNew, nil, nil, nil},
		{"an escrow for a slot not in the set", cloneSlots(u.k.slots), nil, nil, []recoveryAdd{add(0)}},
		{"an escrow for another new slot", withNew, nil, nil, []recoveryAdd{add(1)}},
		{"one new slot's escrow twice", withNew, nil, nil, []recoveryAdd{add(0), add(0)}},
		{"an escrow for a slot the live set has", cloneSlots(u.k.slots), nil, nil, []recoveryAdd{{rid: v.olds[0].RecipientID, key: v.safe}}},
	} {
		if err := u.rotate(c.slots, c.hdr, c.kp, c.adds); !errors.Is(err, ErrParams) {
			t.Fatalf("%s: %v", c.what, err)
		}
		requireUnchanged(t, u, path, before, 1, c.what)
	}
	// With the password off, a K_P handed in without its header would
	// otherwise reach the registry as an entangled_key record.
	offPath := filepath.Join(t.TempDir(), "off.eks")
	ou, _, _ := tokenVault(t, offPath, "")
	offBefore := snapshot(t, offPath)
	if err := ou.rotate(cloneSlots(ou.k.slots), nil, &kp, nil); !errors.Is(err, ErrParams) {
		t.Fatalf("a K_P without its header, the password off: %v", err)
	}
	requireUnchanged(t, ou, offPath, offBefore, 1, "a K_P without its header, the password off")

	if err := u.UpdateRegistry(func(g *format.Registry) error {
		g.DeleteSecret(format.SecretRecoveryEscrow, v.olds[0].RecipientID)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := u.Rotate(); err != nil {
		t.Fatalf("a rotation over a recovery slot whose escrow record is missing: %v", err)
	}
	if _, err := u.RecoveryKey(v.olds[0].RecipientID); !errors.Is(err, ErrEscrowMissing) {
		t.Fatalf("the missing escrow record after the rotation: %v", err)
	}
}

// 13. A replacement whose flip did not land added nothing: the old keys open
// generation 1 and the new keys open nothing.
func TestReplaceRecoveryAndRotateCrashBeforeFlip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	v := newReplaceVault(t, path)
	preps, keys := v.prepared(t)
	before := snapshot(t, path)
	live := v.u.k.live
	if err := v.u.ReplaceRecoveryAndRotate(preps); err != nil {
		t.Fatal(err)
	}
	v.u.Close()
	v.u.k.Close()
	after := snapshot(t, path)
	restore(t, path, crashedBefore(before, after, live))
	k := mustOpen(t, path)
	unlockIs(t, k, RecoveryCredential{Key: v.safe}, 1, nil, "the old safe key")
	unlockIs(t, k, RecoveryCredential{Key: v.office}, 1, nil, "the old office key")
	for _, r := range keys {
		// The old recovery slots fit a recovery key's shape, so a new key
		// meets the verifier, where a vault without one finds no slot.
		if _, err := k.Unlock(RecoveryCredential{Key: r}); !errors.Is(err, ErrVerifier) && !errors.Is(err, ErrNoSlot) {
			t.Fatalf("a new key in the old state: %v", err)
		}
	}
	k.Close()
	restore(t, path, after)
	k = mustOpen(t, path)
	unlockIs(t, k, RecoveryCredential{Key: keys[0]}, 2, nil, "a new key after the flip")
	unlockIs(t, k, RecoveryCredential{Key: v.safe}, 0, ErrVerifier, "the old safe key after the flip")
}

// 21. The outside review's finding 4, end to end: a recovery key R and an
// earlier copy of the file leak; a thief enrols a token X with the VMK R
// yields; the owner adds a token B, removes A with a re-key, changes the
// password with a re-key, replaces R and removes X with a re-key, retiring
// after each. The earlier copy then reaches nothing in the current file. The
// two halves of the cure that the rest cannot stand in for are each shown to
// matter: without the replacement the R the copy escrows still opens the
// file; without X's removal X with the new password does.
func TestAnEarlierCopyReachesNothingAfterTheWholeCure(t *testing.T) {
	for _, run := range []struct {
		name             string
		replace, removeX bool
	}{
		{"the whole cure", true, true},
		{"without the replacement", false, true},
		{"without X's removal", true, false},
	} {
		t.Run(run.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "v.eks")
			const pass, newPass = "the owner's", "the owner's new one"
			tokA, tokB, tokX := newToken(t), newToken(t), newToken(t)
			r := recoveryKey(t)
			u := mustCreateWith(t, path, CreateOptions{
				Entangle: &Entangle{Password: pass, Argon2: fast},
				Slots:    []SlotSpec{HardwareSlot{PublicKey: tokA.PublicKey(), Label: "A"}, RecoverySlot{Key: r, Label: "paper"}},
			})
			k := u.k
			vaultID := k.sb.VaultID
			addArchive(t, u)
			recA, recR := recordOf(t, k, "A"), recordOf(t, k, "paper")
			earlier := snapshot(t, path)

			// The thief, with the vault file and R.
			th, err := k.Unlock(RecoveryCredential{Key: r})
			if err != nil {
				t.Fatal(err)
			}
			if err := th.AddSlot(HardwareSlot{PublicKey: tokX.PublicKey(), Label: "X"}); err != nil {
				t.Fatal(err)
			}
			if err := th.Retire(); err != nil {
				t.Fatal(err)
			}
			th.Close()
			recX := recordOf(t, k, "X")

			// The owner's cure, each act retired at once.
			step := func(what string, act func() error) {
				t.Helper()
				if err := act(); err != nil {
					t.Fatalf("%s: %v", what, err)
				}
				if err := u.Retire(); err != nil || k.RetireOwed() {
					t.Fatalf("%s: Retire %v, owed %v", what, err, k.RetireOwed())
				}
			}
			step("add B", func() error { return u.AddSlot(HardwareSlot{PublicKey: tokB.PublicKey(), Label: "B"}) })
			step("remove A", func() error { return u.RemoveSlotAndRotate(recA.RecipientID) })
			step("change the password", func() error {
				return u.ChangeEntangledPasswordAndRotate(Entangle{Password: newPass, Argon2: fast})
			})
			if run.replace {
				step("replace R", func() error {
					p, err := u.PrepareRecovery(recoveryKey(t), "paper", nil)
					if err != nil {
						return err
					}
					defer p.Zero()
					return u.ReplaceRecoveryAndRotate([]*PreparedRecovery{p})
				})
			}
			if run.removeX {
				step("remove X", func() error { return u.RemoveSlotAndRotate(recX.RecipientID) })
			}
			// An archive created after the cure: its wrapper is a real one,
			// opening to its key under the current VMK, and the earlier
			// copy must reach it no more than the re-wrapped ones.
			late, lateKey := addArchive(t, u)
			withSession(t, u, func(s *Session) {
				if got, err := s.UnwrapArchiveKey(late.ArchiveID, &late.Versions[0]); err != nil || got != lateKey {
					t.Fatalf("the control: the late archive's key under the current VMK: %v", err)
				}
			})
			cur, err := cloneRegistry(u.Registry())
			if err != nil {
				t.Fatal(err)
			}
			curKP := *u.kp()
			u.Close()
			k.Close()

			// The earlier copy, opened with R, yields a VMK, a K_P and the R
			// it escrows.
			ek, _ := openedAs(t, earlier)
			eu, err := ek.Unlock(RecoveryCredential{Key: r})
			if err != nil {
				t.Fatal(err)
			}
			vmk0, kp0 := eu.vmk(), *eu.kp()
			escrowed, err := eu.RecoveryKey(recR.RecipientID)
			if err != nil {
				t.Fatal(err)
			}
			eu.Close()
			ek.Close()
			kwk0, kwkS0 := kdf.KWK(vmk0, vaultID), kdf.KWKSecrets(vmk0, vaultID)
			sawLate := false
			for _, a := range cur.Archives {
				sawLate = sawLate || a.ArchiveID == late.ArchiveID
				for _, ver := range a.Versions {
					if _, err := kdf.UnwrapKey(kwk0, ver.WrappedArchiveKey, ver.WrapNonce, format.ArchiveKeyAAD(a.ArchiveID, ver.KID)); err == nil {
						t.Fatal("an archive key wrapper of the current registry opens under the earlier VMK's KWK")
					}
				}
			}
			if !sawLate || len(cur.Archives) != 2 {
				t.Fatalf("the control: %d archives, the one created after the cure among them %v", len(cur.Archives), sawLate)
			}
			for i := range cur.Secrets {
				if _, err := openSecretWith(kwkS0, vaultID, &cur.Secrets[i]); err == nil {
					t.Fatalf("the current secrets record of kind %d opens under the earlier VMK's KWK_secrets", cur.Secrets[i].Kind)
				}
			}
			if kp0 == curKP {
				t.Fatal("the earlier copy's K_P is the current one")
			}

			k = mustOpen(t, path)
			if run.replace {
				unlockIs(t, k, RecoveryCredential{Key: escrowed}, 0, ErrVerifier, "the R the earlier copy escrows")
			} else {
				unlockIs(t, k, RecoveryCredential{Key: escrowed}, k.Generation(), nil, "without the replacement, the R the earlier copy escrows")
			}
			if run.removeX {
				unlockIs(t, k, HardwareCredential{Token: tokX, Password: newPass}, 0, ErrNoSlot, "X")
			} else {
				unlockIs(t, k, HardwareCredential{Token: tokX, Password: newPass}, k.Generation(), nil, "without its removal, X with the new password")
			}
			if holdsSlot(t, path, recA) {
				t.Fatal("the raw bytes hold A")
			}
			if run.replace && holdsSlot(t, path, recR) {
				t.Fatal("the raw bytes hold R")
			}
			if run.removeX && holdsSlot(t, path, recX) {
				t.Fatal("the raw bytes hold X")
			}
		})
	}
}

// otherCopyRecords are the records of the slot region in the copy no live
// superblock names whose recipient IDs are among rids, as a failed commit
// wrote them: what a test then searches the raw bytes for.
func otherCopyRecords(t *testing.T, img []byte, rids ...[16]byte) []format.SlotRecord {
	t.Helper()
	sb, _ := liveOf(t, img)
	c := regionCopy(img, sb.LiveSlotRegion().Other())
	end := len(c)
	for end > 0 && c[end-1] == 0 {
		end--
	}
	for n := end; n <= end+64 && n <= len(c); n++ {
		r, err := format.DecodeSlotRegion(c[:n])
		if err != nil {
			continue
		}
		var out []format.SlotRecord
		for _, s := range r.Slots {
			for _, rid := range rids {
				if s.RecipientID == rid {
					out = append(out, s)
				}
			}
		}
		if len(out) == len(rids) {
			return out
		}
	}
	t.Fatal("the other slot region copy does not hold the records")
	return nil
}

// 32. A replacement that fails after writing its bodies leaves the new keys'
// records and their wrapped VMK in the file; the act's Retire takes every
// byte of them away, and the old keys still open the vault. The same for a
// hardware slot whose enrolment failed after its region write.
func TestAFailedReplacementIsRetiredAway(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	v := newReplaceVault(t, path)
	u, k := v.u, v.u.k
	preps, keys := v.prepared(t)
	restoreFault := faultOn(t, "sync")
	err := u.ReplaceRecoveryAndRotate(preps)
	restoreFault()
	if !errors.Is(err, errFault) || k.Broken() != nil {
		t.Fatalf("the failing replacement: %v, Broken %v", err, k.Broken())
	}
	if k.Generation() != 1 || u.gen != 1 || !k.RetireOwed() {
		t.Fatalf("after the failure: generation %d, handle %d, owed %v", k.Generation(), u.gen, k.RetireOwed())
	}
	img := snapshot(t, path)
	written := otherCopyRecords(t, img, preps[0].RecipientID(), preps[1].RecipientID())
	for _, w := range written {
		if !holdsSlot(t, path, w) || len(w.EPK) == 0 || w.WrappedVMK == ([format.WrappedVMKSize]byte{}) {
			t.Fatal("the control: the failed commit did not leave a wrapped new record")
		}
	}
	if err := u.Retire(); err != nil {
		t.Fatal(err)
	}
	img = snapshot(t, path)
	for _, w := range written {
		if bytes.Contains(img, w.RecipientID[:]) || bytes.Contains(img, w.EPK) || bytes.Contains(img, w.WrappedVMK[:]) {
			t.Fatal("the raw bytes still hold a new key's recipient ID, epk or wrapped_vmk")
		}
	}
	requireClean(t, path, "after the replacement's retirement")
	if k.RetireOwed() {
		t.Fatal("still owed")
	}
	unlockIs(t, k, RecoveryCredential{Key: v.safe}, 1, nil, "the old safe key")
	unlockIs(t, k, RecoveryCredential{Key: v.office}, 1, nil, "the old office key")
	for _, r := range keys {
		// The old recovery slots remain, so a new key meets their verifier.
		if _, err := k.Unlock(RecoveryCredential{Key: r}); !errors.Is(err, ErrVerifier) && !errors.Is(err, ErrNoSlot) {
			t.Fatalf("a new key after the failed replacement: %v", err)
		}
	}

	t.Run("a hardware slot's enrolment", func(t *testing.T) {
		tok := newToken(t)
		restoreFault := faultOn(t, "registry")
		err := u.AddSlot(HardwareSlot{PublicKey: tok.PublicKey(), Label: "never"})
		restoreFault()
		if !errors.Is(err, errFault) || !k.RetireOwed() {
			t.Fatalf("the failing enrolment: %v, owed %v", err, k.RetireOwed())
		}
		stray := strayRecord(t, snapshot(t, path), k.slots)
		if !bytes.Equal(stray.SlotPubkey, tok.PublicKey()) {
			t.Fatal("the control: the record left is not the token's")
		}
		if err := u.Retire(); err != nil {
			t.Fatal(err)
		}
		if holdsSlot(t, path, stray) || bytes.Contains(snapshot(t, path), tok.PublicKey()) || k.RetireOwed() {
			t.Fatal("the failed enrolment's record is still in the file")
		}
		requireClean(t, path, "after the enrolment's retirement")
	})
}

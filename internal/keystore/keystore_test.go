package keystore

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dreamxwarden01/enfold/internal/format"
	"github.com/dreamxwarden01/enfold/internal/kdf"
)

// fast is the smallest Argon2id the format allows: tests are about the
// keystore, not the KDF.
var fast = kdf.Argon2Params{MemKiB: 64, Time: 1, Threads: 1}

// softToken is a P-256 key in software standing in for a YubiKey.
type softToken struct{ key *ecdh.PrivateKey }

func newToken(t testing.TB) *softToken {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &softToken{key: key}
}

func (s *softToken) PublicKey() []byte { return s.key.PublicKey().Bytes() }

func (s *softToken) ECDH(epk []byte) ([]byte, error) {
	pub, err := ecdh.P256().NewPublicKey(epk)
	if err != nil {
		return nil, err
	}
	return s.key.ECDH(pub)
}

func mustCreate(t testing.TB, path string, slots ...SlotSpec) *Unlocked {
	t.Helper()
	u, err := Create(path, CreateOptions{Slots: slots})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { u.Close(); u.k.Close() })
	return u
}

func mustOpen(t testing.TB, path string) *Keystore {
	t.Helper()
	k, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { k.Close() })
	return k
}

// archive is a minimal valid archive record for registry tests, with its
// archive key wrapped under the session's KWK so that a rotation can carry
// it.
func archive(t testing.TB, s *Session, name string) format.ArchiveRecord {
	t.Helper()
	var id [16]byte
	rand.Read(id[:])
	var ak [32]byte
	rand.Read(ak[:])
	wrapped, nonce, err := s.WrapArchiveKey(id, id, ak)
	if err != nil {
		t.Fatal(err)
	}
	return format.ArchiveRecord{ArchiveID: id, Name: name, CurrentKID: id, Versions: []format.VersionRecord{{KID: id, WrappedArchiveKey: wrapped, WrapNonce: nonce, State: format.VersionCurrent}}}
}

// withSession runs fn with a throwaway Session over u.
func withSession(t testing.TB, u *Unlocked, fn func(*Session)) {
	t.Helper()
	s, err := u.Session()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Lock()
	fn(s)
}

// registryEnds returns the end of the registry extent each superblock copy
// references, or 0 for a copy that does not decode.
func registryEnds(t testing.TB, path string) (a, b uint64) {
	t.Helper()
	data := snapshot(t, path)
	end := func(c format.Copy) uint64 {
		off := c.KeystoreSuperblockOff()
		sb, err := format.DecodeKeystoreSuperblock(data[off : off+format.SuperblockSize])
		if err != nil {
			return 0
		}
		return sb.RegistryOff + sb.RegistryLen + format.TagSize
	}
	return end(format.CopyA), end(format.CopyB)
}

func recoveryKey(t testing.TB) kdf.RecoveryKey {
	t.Helper()
	r, err := kdf.NewRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestPasswordAndRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	rk := recoveryKey(t)
	// The password is enrolled in its precomposed form and must also open
	// in the decomposed one (R4): café with U+00E9, then with e + U+0301.
	const nfc, nfd = "caf\u00e9 horse", "cafe\u0301 horse"
	u := mustCreate(t, path, PasswordSlot{Password: nfc, Argon2: fast, Label: "pw"}, RecoverySlot{Key: rk, Label: "paper"})
	if u.Tampered() != nil {
		t.Fatal(u.Tampered())
	}
	vault := u.k.VaultID()
	u.Close()
	if _, err := u.Session(); !errors.Is(err, ErrClosed) {
		t.Errorf("Session after Close: %v", err)
	}
	u.k.Close()

	k := mustOpen(t, path)
	defer k.Close()
	if k.VaultID() != vault || k.Generation() != 1 || k.RotationPending() || k.Stale != nil {
		t.Fatalf("reopened: vault %x gen %d pending %v stale %v", k.VaultID(), k.Generation(), k.RotationPending(), k.Stale)
	}
	if infos := k.Slots(); len(infos) != 2 || infos[0].Type != format.SlotStandalonePassword || infos[1].Type != format.SlotRecovery || infos[0].Label != "pw" {
		t.Fatalf("slots: %+v", infos)
	}

	u, err := k.Unlock(PasswordCredential{Password: nfc})
	if err != nil {
		t.Fatal(err)
	}
	if u.OpenedBy() != k.Slots()[0].RecipientID {
		t.Error("OpenedBy is not the password slot")
	}
	s, err := u.Session()
	if err != nil {
		t.Fatal(err)
	}
	u.Close()
	if len(s.DBKey()) != 32 || len(s.kwk) != 32 || len(s.meta) != 32 {
		t.Fatal("session keys missing")
	}
	s.Lock()
	if s.DBKey() != nil {
		t.Error("DBKey after Lock")
	}
	if err := s.UpdateRegistry(func(*format.Registry) error { return nil }); !errors.Is(err, ErrClosed) {
		t.Errorf("UpdateRegistry after Lock: %v", err)
	}

	// The same password in its decomposed form normalises to the same key.
	if nfc == nfd {
		t.Fatal("test literals are not distinct")
	}
	if u, err := k.Unlock(PasswordCredential{Password: nfd}); err != nil {
		t.Fatalf("decomposed password: %v", err)
	} else {
		u.Close()
	}
	// The recovery digits, in the forms R23 accepts: dashed, spaced, run
	// together, and with en dashes.
	digits := rk.Digits()
	groups := strings.Split(digits, "-")
	if len(groups) != 8 {
		t.Fatalf("digits %q", digits)
	}
	for _, form := range []string{digits, " " + strings.Join(groups, " ") + " ", strings.Join(groups, ""), strings.Join(groups, "\u2013")} {
		key, err := kdf.ParseRecoveryDigits(form)
		if err != nil {
			t.Fatal(err)
		}
		u, err := k.Unlock(RecoveryCredential{Key: key})
		if err != nil {
			t.Fatalf("recovery %q: %v", form, err)
		}
		u.Close()
	}

	// Failures, each with its own name.
	if _, err := k.Unlock(PasswordCredential{Password: "wrong"}); !errors.Is(err, ErrVerifier) {
		t.Errorf("wrong password: %v", err)
	}
	other := recoveryKey(t)
	if _, err := k.Unlock(RecoveryCredential{Key: other}); !errors.Is(err, ErrVerifier) {
		t.Errorf("wrong recovery key: %v", err)
	}
	if _, err := k.Unlock(HardwareCredential{Token: newToken(t)}); !errors.Is(err, ErrNoSlot) {
		t.Errorf("unenrolled token: %v", err)
	}
	if _, err := k.Unlock(PasswordCredential{}); !errors.Is(err, ErrParams) {
		t.Errorf("empty password: %v", err)
	}
	k.Close()
	if _, err := k.Unlock(PasswordCredential{Password: nfc}); !errors.Is(err, ErrClosed) {
		t.Errorf("unlock after Close: %v", err)
	}
}

func TestFixedClockAndKeystoreAccessors(t *testing.T) {
	saved := now
	now = func() int64 { return 1_700_000_000 }
	defer func() { now = saved }()
	path := filepath.Join(t.TempDir(), "v.eks")
	rk := recoveryKey(t)
	u := mustCreate(t, path, PasswordSlot{Password: "p", Argon2: fast}, RecoverySlot{Key: rk})
	if u.Keystore() == nil || u.Keystore() != u.k {
		t.Fatal("Unlocked.Keystore")
	}
	for _, s := range u.Keystore().Slots() {
		if s.CreatedAt != 1_700_000_000 {
			t.Errorf("CreatedAt %d", s.CreatedAt)
		}
	}
	if u.Registry().ModifiedAt != 1_700_000_000 {
		t.Errorf("ModifiedAt %d", u.Registry().ModifiedAt)
	}
	if u.OpenedBy() != [16]byte{} {
		t.Error("Create's Unlocked reports an opening slot")
	}
	s, err := u.Session()
	if err != nil || s.Keystore() != u.k || u.Keystore().Broken() != nil {
		t.Fatalf("session: %v", err)
	}
	s.Lock()
}

func TestInvariant(t *testing.T) {
	dir := t.TempDir()
	rk := recoveryKey(t)
	tokA, tokB := newToken(t), newToken(t)
	cases := []struct {
		name  string
		slots []SlotSpec
		want  error
	}{
		{"password alone", []SlotSpec{PasswordSlot{Password: "p", Argon2: fast}}, ErrInvariant},
		{"recovery alone", []SlotSpec{RecoverySlot{Key: rk}}, ErrInvariant},
		{"nothing", nil, ErrInvariant},
		{"password + hardware", []SlotSpec{PasswordSlot{Password: "p", Argon2: fast}, HardwareSlot{PublicKey: tokA.PublicKey()}, RecoverySlot{Key: rk}}, ErrPolicy},
		{"two tokens, entangled", []SlotSpec{HardwareSlot{PublicKey: tokA.PublicKey(), Password: "a", Argon2: fast}, HardwareSlot{PublicKey: tokB.PublicKey(), Password: "b", Argon2: fast}}, ErrInvariant},
		{"one token, entangled, plus recovery", []SlotSpec{HardwareSlot{PublicKey: tokA.PublicKey(), Password: "a", Argon2: fast}, RecoverySlot{Key: rk}}, nil},
		{"two tokens, plain", []SlotSpec{HardwareSlot{PublicKey: tokA.PublicKey()}, HardwareSlot{PublicKey: tokB.PublicKey()}}, nil},
		{"two passwords", []SlotSpec{PasswordSlot{Password: "p", Argon2: fast}, PasswordSlot{Password: "q", Argon2: fast}}, ErrInvariant},
		{"password + recovery", []SlotSpec{PasswordSlot{Password: "p", Argon2: fast}, RecoverySlot{Key: rk}}, nil},
		{"entangled without argon2", []SlotSpec{HardwareSlot{PublicKey: tokA.PublicKey(), Password: "a"}, RecoverySlot{Key: rk}}, ErrParams},
		{"argon2 without password", []SlotSpec{HardwareSlot{PublicKey: tokA.PublicKey(), Argon2: fast}, RecoverySlot{Key: rk}}, ErrParams},
		{"bad token key", []SlotSpec{HardwareSlot{PublicKey: []byte{4, 1, 2}}, RecoverySlot{Key: rk}}, ErrParams},
	}
	for i, c := range cases {
		path := filepath.Join(dir, "case"+string(rune('a'+i))+".eks")
		u, err := Create(path, CreateOptions{Slots: c.slots})
		if !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
		if err == nil {
			u.Close()
			u.k.Close()
		} else if _, statErr := os.Stat(path); statErr == nil {
			t.Errorf("%s: file left behind after a refused creation", c.name)
		}
	}

	// Removal is held to the same predicate.
	path := filepath.Join(dir, "remove.eks")
	u := mustCreate(t, path, PasswordSlot{Password: "p", Argon2: fast}, RecoverySlot{Key: rk})
	defer u.k.Close()
	defer u.Close()
	pw, rec := u.k.Slots()[0].RecipientID, u.k.Slots()[1].RecipientID
	if err := u.RemoveSlot(rec); !errors.Is(err, ErrInvariant) {
		t.Errorf("remove recovery: %v", err)
	}
	if err := u.RemoveSlot([16]byte{9}); !errors.Is(err, ErrNotFound) {
		t.Errorf("remove unknown: %v", err)
	}
	// A second recovery slot makes the password slot removable; a token
	// still cannot join while the password slot is there.
	if err := u.AddSlot(RecoverySlot{Key: recoveryKey(t), Label: "second"}); err != nil {
		t.Fatal(err)
	}
	if err := u.AddSlot(HardwareSlot{PublicKey: newToken(t).PublicKey()}); !errors.Is(err, ErrPolicy) {
		t.Errorf("token next to a password slot: %v", err)
	}
	if err := u.RemoveSlot(pw); err != nil {
		t.Errorf("remove the password slot: %v", err)
	}
	// Two recovery slots are two ways in; one is not.
	if err := u.RemoveSlot(rec); !errors.Is(err, ErrInvariant) {
		t.Errorf("remove one of the last two: %v", err)
	}
	if infos := u.k.Slots(); len(infos) != 2 || infos[0].RecipientID != rec || infos[1].Label != "second" {
		t.Errorf("slots after removals: %+v", infos)
	}
}

func TestHardware(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	tokA, tokB := newToken(t), newToken(t)
	rk := recoveryKey(t)
	u := mustCreate(t, path, HardwareSlot{PublicKey: tokA.PublicKey(), Label: "A"}, RecoverySlot{Key: rk})
	if err := u.AddSlot(HardwareSlot{PublicKey: tokB.PublicKey(), Password: "with pass", Argon2: fast, Label: "B"}); err != nil {
		t.Fatal(err)
	}
	if err := u.AddSlot(HardwareSlot{PublicKey: tokA.PublicKey()}); !errors.Is(err, ErrDuplicate) {
		t.Errorf("enrolling A twice: %v", err)
	}
	u.Close()
	u.k.Close()

	k := mustOpen(t, path)
	defer k.Close()
	infos := k.Slots()
	if len(infos) != 3 || !bytes.Equal(infos[0].PublicKey, tokA.PublicKey()) || infos[2].EntangledPassword != true || infos[0].EntangledPassword {
		t.Fatalf("slots: %+v", infos)
	}
	// A: token only; the password, if given, is ignored.
	for _, pw := range []string{"", "ignored"} {
		u, err := k.Unlock(HardwareCredential{Token: tokA, Password: pw})
		if err != nil {
			t.Fatalf("token A (password %q): %v", pw, err)
		}
		u.Close()
	}
	// B: token and password.
	if _, err := k.Unlock(HardwareCredential{Token: tokB}); !errors.Is(err, ErrPasswordRequired) {
		t.Errorf("B without password: %v", err)
	}
	if _, err := k.Unlock(HardwareCredential{Token: tokB, Password: "wrong"}); !errors.Is(err, ErrAuth) {
		t.Errorf("B with wrong password: %v", err)
	}
	u, err := k.Unlock(HardwareCredential{Token: tokB, Password: "with pass"})
	if err != nil {
		t.Fatal(err)
	}
	u.Close()
	// A token whose ECDH fails surfaces its error.
	if _, err := k.Unlock(HardwareCredential{Token: &failingToken{pub: tokA.PublicKey()}}); err == nil || errors.Is(err, ErrNoSlot) {
		t.Errorf("failing token: %v", err)
	}
}

type failingToken struct{ pub []byte }

func (f *failingToken) PublicKey() []byte           { return f.pub }
func (f *failingToken) ECDH([]byte) ([]byte, error) { return nil, errors.New("touch timed out") }

func snapshot(t testing.TB, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func restore(t testing.TB, path string, b []byte) {
	t.Helper()
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRotate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	tok := newToken(t)
	rk := recoveryKey(t)
	u := mustCreate(t, path, HardwareSlot{PublicKey: tok.PublicKey(), Password: "pass", Argon2: fast}, RecoverySlot{Key: rk})
	// An archive key to carry across the rotation.
	var archiveID, kid [16]byte
	rand.Read(archiveID[:])
	rand.Read(kid[:])
	var archiveKey [32]byte
	rand.Read(archiveKey[:])
	s, _ := u.Session()
	wrapped, nonce, err := s.WrapArchiveKey(archiveID, kid, archiveKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateRegistry(func(g *format.Registry) error {
		g.Archives = append(g.Archives, format.ArchiveRecord{ArchiveID: archiveID, Name: "docs", CurrentKID: kid, Versions: []format.VersionRecord{{KID: kid, WrappedArchiveKey: wrapped, WrapNonce: nonce, State: format.VersionCurrent}}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s.Lock()
	u.Close()
	u.k.Close()
	before := snapshot(t, path)

	k := mustOpen(t, path)
	u, err = k.Unlock(HardwareCredential{Token: tok, Password: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	stale, err := u.Rotate(RotateOptions{})
	if err != nil || len(stale) != 0 {
		t.Fatalf("rotate: %v, stale %+v", err, stale)
	}
	if k.Generation() != 2 || k.RotationPending() {
		t.Fatalf("after rotation: gen %d pending %v", k.Generation(), k.RotationPending())
	}
	// The archive key survived, under the new KWK.
	s, _ = u.Session()
	got, err := s.UnwrapArchiveKey(archiveID, &s.Registry().Archives[0].Versions[0])
	if err != nil || got != archiveKey {
		t.Fatalf("archive key after rotation: %v", err)
	}
	s.Lock()
	u.Close()
	k.Close()

	// Every slot opens at the new generation, from a fresh Open.
	k = mustOpen(t, path)
	for _, c := range []Credential{HardwareCredential{Token: tok, Password: "pass"}, RecoveryCredential{Key: rk}} {
		u, err := k.Unlock(c)
		if err != nil {
			t.Fatalf("%T after rotation: %v", c, err)
		}
		if u.gen != 2 {
			t.Errorf("%T: generation %d", c, u.gen)
		}
		u.Close()
	}
	after := snapshot(t, path)
	k.Close()

	// Splice: the pre-rotation slot region under the post-rotation
	// superblock and registry (§6.2). Each old record still opens, but to
	// generation 1, which the superblock's 2 exposes as stale.
	oldK := mustOpen(t, path)
	restore(t, path, before)
	oldOpen := mustOpen(t, path)
	restore(t, path, after)
	spliced := bytes.Clone(after)
	copy(spliced[oldK.sb.SlotRegionOff:], oldOpen.region)
	oldK.Close()
	oldOpen.Close()
	// The live superblock must point at the copy we overwrote and record
	// the old length; rewrite it with the same seq.
	k = mustOpen(t, path)
	sb := *k.sb
	k.Close()
	sb.SlotRegionLen = uint64(len(oldOpen.region))
	encoded, _ := sb.Encode()
	copy(spliced[k.live.KeystoreSuperblockOff():], encoded)
	restore(t, path, spliced)
	k = mustOpen(t, path)
	if _, err := k.Unlock(RecoveryCredential{Key: rk}); !errors.Is(err, ErrStale) {
		t.Errorf("spliced old region: %v, want ErrStale", err)
	}
	k.Close()
	restore(t, path, after)
}

func TestTamperedRegion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	rk := recoveryKey(t)
	u := mustCreate(t, path, PasswordSlot{Password: "p", Argon2: fast}, RecoverySlot{Key: rk})
	u.Close()
	u.k.Close()

	// Replace the recovery slot's public key with another valid one: the
	// record still decodes, and only that slot's own AAD would notice — at
	// the next rotation, when it would receive the new VMK.
	k := mustOpen(t, path)
	slots := cloneSlots(k.slots)
	var fake [32]byte
	rand.Read(fake[:])
	fake[31] &= 0x7f
	slots[1].SlotPubkey = fake[:]
	region, err := format.EncodeSlotRegion(slots)
	if err != nil {
		t.Fatal(err)
	}
	if len(region) != len(k.region) {
		t.Fatal("region length changed")
	}
	b := snapshot(t, path)
	copy(b[k.sb.SlotRegionOff:], region)
	k.Close()
	restore(t, path, b)

	k = mustOpen(t, path)
	defer k.Close()
	u, err = k.Unlock(PasswordCredential{Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	if !errors.Is(u.Tampered(), ErrTampered) {
		t.Fatalf("Tampered: %v", u.Tampered())
	}
	if _, err := u.Rotate(RotateOptions{}); !errors.Is(err, ErrTampered) {
		t.Errorf("rotate: %v", err)
	}
	if _, err := u.RewrapStale(RecoveryCredential{Key: rk}); !errors.Is(err, ErrTampered) {
		t.Errorf("rewrap: %v", err)
	}
	if err := u.AddSlot(RecoverySlot{Key: recoveryKey(t)}); !errors.Is(err, ErrTampered) {
		t.Errorf("add: %v", err)
	}
	if err := u.RemoveSlot(k.Slots()[1].RecipientID); !errors.Is(err, ErrTampered) {
		t.Errorf("remove: %v", err)
	}
	if err := u.Export(filepath.Join(t.TempDir(), "x.eks")); !errors.Is(err, ErrTampered) {
		t.Errorf("export: %v", err)
	}
	// Reading stays possible, including registry updates — which must not
	// launder the region: after one, a fresh unlock still sees the tamper
	// and rotation is still refused (R25's converse rule).
	s, err := u.Session()
	if err != nil {
		t.Fatal(err)
	}
	if err := u.UpdateRegistry(func(g *format.Registry) error {
		g.Archives = append(g.Archives, archive(t, s, "added while tampered"))
		return nil
	}); err != nil {
		t.Errorf("registry update while tampered: %v", err)
	}
	if err := s.UpdateRegistry(func(g *format.Registry) error { return nil }); err != nil {
		t.Errorf("session registry update while tampered: %v", err)
	}
	s.Lock()
	u.Close()
	k.Close()
	k = mustOpen(t, path)
	u, err = k.Unlock(PasswordCredential{Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(u.Tampered(), ErrTampered) {
		t.Fatalf("tamper laundered by a registry update: %v", u.Tampered())
	}
	if len(u.Registry().Archives) != 1 {
		t.Errorf("the update itself was lost: %d archives", len(u.Registry().Archives))
	}
	if _, err := u.Rotate(RotateOptions{}); !errors.Is(err, ErrTampered) {
		t.Errorf("rotate after a registry update: %v", err)
	}
	// And the substituted slot itself fails its AAD.
	if _, err := k.Unlock(RecoveryCredential{Key: rk}); !errors.Is(err, ErrVerifier) {
		t.Errorf("substituted slot: %v", err)
	}
}

// TestHandlesAcrossRotation: the registry is one object per Keystore, so a
// commit through one handle never reverts another's; and a rotation leaves
// every earlier handle stale, so none of them can write under the old keys.
func TestHandlesAcrossRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	rk := recoveryKey(t)
	u := mustCreate(t, path, PasswordSlot{Password: "p", Argon2: fast}, RecoverySlot{Key: rk})
	s, err := u.Session()
	if err != nil {
		t.Fatal(err)
	}
	// A Session commit, then an Unlocked commit: both survive.
	if err := s.UpdateRegistry(func(g *format.Registry) error {
		g.Archives = append(g.Archives, archive(t, s, "from the session"))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := u.AddSlot(RecoverySlot{Key: recoveryKey(t), Label: "second"}); err != nil {
		t.Fatal(err)
	}
	if len(u.Registry().Archives) != 1 || len(s.Registry().Archives) != 1 || u.Registry() != s.Registry() {
		t.Fatal("handles see different registries")
	}
	// A second Unlocked over the same Keystore, before the rotation.
	other, err := u.k.Unlock(RecoveryCredential{Key: rk})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := u.Rotate(RotateOptions{}); err != nil {
		t.Fatal(err)
	}
	// Everything derived before the rotation is stale, and says so.
	if err := s.UpdateRegistry(func(*format.Registry) error { return nil }); !errors.Is(err, ErrStale) {
		t.Errorf("session update after rotation: %v", err)
	}
	if _, _, err := s.WrapArchiveKey([16]byte{1}, [16]byte{2}, [32]byte{}); !errors.Is(err, ErrStale) {
		t.Errorf("session wrap after rotation: %v", err)
	}
	if s.DBKey() != nil {
		t.Error("stale session still hands out its DB key")
	}
	if err := other.AddSlot(RecoverySlot{Key: recoveryKey(t)}); !errors.Is(err, ErrStale) {
		t.Errorf("stale Unlocked mutation: %v", err)
	}
	if _, err := other.Session(); !errors.Is(err, ErrStale) {
		t.Errorf("session from a stale Unlocked: %v", err)
	}
	if err := other.UpdateRegistry(func(*format.Registry) error { return nil }); !errors.Is(err, ErrStale) {
		t.Errorf("stale Unlocked registry update: %v", err)
	}
	other.Close()
	// The rotating handle carries on, and a fresh session works.
	s2, err := u.Session()
	if err != nil {
		t.Fatal(err)
	}
	if err := s2.UpdateRegistry(func(g *format.Registry) error {
		g.Archives = append(g.Archives, archive(t, s2, "after rotation"))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s2.Lock()
	u.Close()
	u.k.Close()
	k := mustOpen(t, path)
	u, err = k.Unlock(RecoveryCredential{Key: rk})
	if err != nil {
		t.Fatal(err)
	}
	if len(u.Registry().Archives) != 2 || len(k.Slots()) != 3 || k.Generation() != 2 {
		t.Fatalf("after everything: %d archives, %d slots, gen %d", len(u.Registry().Archives), len(k.Slots()), k.Generation())
	}
	u.Close()
}

func TestDeferredRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	tokA, tokB := newToken(t), newToken(t)
	rk := recoveryKey(t)
	u := mustCreate(t, path,
		HardwareSlot{PublicKey: tokA.PublicKey(), Password: "alpha", Argon2: fast, Label: "A"},
		HardwareSlot{PublicKey: tokB.PublicKey(), Password: "bravo", Argon2: fast, Label: "B"},
		RecoverySlot{Key: rk})
	u.Close()
	u.k.Close()

	k := mustOpen(t, path)
	u, err := k.Unlock(HardwareCredential{Token: tokA, Password: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	stale, err := u.Rotate(RotateOptions{})
	if err != nil || len(stale) != 1 || stale[0].Label != "B" || !stale[0].Stale {
		t.Fatalf("rotate: %v, stale %+v", err, stale)
	}
	if !k.RotationPending() || k.Generation() != 2 {
		t.Fatalf("pending %v gen %d", k.RotationPending(), k.Generation())
	}
	if k.Slots()[1].Stale != true || k.Slots()[0].Stale {
		t.Fatalf("slot info: %+v", k.Slots())
	}
	u.Close()
	k.Close()

	k = mustOpen(t, path)
	if _, err := k.Unlock(HardwareCredential{Token: tokB, Password: "bravo"}); !errors.Is(err, ErrStale) {
		t.Errorf("stale slot: %v", err)
	}
	u, err = k.Unlock(HardwareCredential{Token: tokA, Password: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	// Bringing B up to date needs B's own credential, checked against what
	// B still holds: a wrong password is refused, a foreign credential fits
	// no stale slot, and the right one clears the flag.
	if _, err := u.RewrapStale(HardwareCredential{Token: tokB, Password: "nope"}); !errors.Is(err, ErrAuth) {
		t.Fatalf("rewrap with the wrong password: %v", err)
	}
	if _, err := u.RewrapStale(HardwareCredential{Token: tokB}); !errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("rewrap without password: %v", err)
	}
	if _, err := u.RewrapStale(RecoveryCredential{Key: rk}); !errors.Is(err, ErrNoSlot) {
		t.Fatalf("rewrap with a credential that fits no stale slot: %v", err)
	}
	if !k.RotationPending() {
		t.Error("pending cleared without a rewrap")
	}
	if left, err := u.RewrapStale(HardwareCredential{Token: tokB, Password: "bravo"}); err != nil || len(left) != 0 {
		t.Fatalf("rewrap: %v, %+v", err, left)
	}
	if k.RotationPending() {
		t.Error("still pending")
	}
	u.Close()
	k.Close()
	k = mustOpen(t, path)
	defer k.Close()
	u, err = k.Unlock(HardwareCredential{Token: tokB, Password: "bravo"})
	if err != nil {
		t.Fatalf("B after rewrap: %v", err)
	}
	if u.gen != 2 {
		t.Errorf("B at generation %d", u.gen)
	}
	u.Close()
	// Recovery has no password and was re-wrapped in the rotation.
	u, err = k.Unlock(RecoveryCredential{Key: rk})
	if err != nil {
		t.Fatalf("recovery: %v", err)
	}
	u.Close()
}

func TestSharedPasswordRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	tokA, tokB := newToken(t), newToken(t)
	rk := recoveryKey(t)
	u := mustCreate(t, path,
		HardwareSlot{PublicKey: tokA.PublicKey(), Password: "same", Argon2: fast, Label: "A"},
		HardwareSlot{PublicKey: tokB.PublicKey(), Password: "same", Argon2: fast, Label: "B"},
		RecoverySlot{Key: rk})
	u.Close()
	u.k.Close()
	k := mustOpen(t, path)
	u, err := k.Unlock(HardwareCredential{Token: tokA, Password: "same"})
	if err != nil {
		t.Fatal(err)
	}
	stale, err := u.Rotate(RotateOptions{SharedPassword: true})
	if err != nil || len(stale) != 0 || k.RotationPending() {
		t.Fatalf("shared rotation: %v, stale %+v, pending %v", err, stale, k.RotationPending())
	}
	u.Close()
	k.Close()
	k = mustOpen(t, path)
	u, err = k.Unlock(HardwareCredential{Token: tokB, Password: "same"})
	if err != nil || u.gen != 2 {
		t.Fatalf("B after shared rotation: %v", err)
	}
	u.Close()
	// Unlocked through a slot with no password, no entangled slot can be
	// re-wrapped, shared or not.
	u, err = k.Unlock(RecoveryCredential{Key: rk})
	if err != nil {
		t.Fatal(err)
	}
	stale, err = u.Rotate(RotateOptions{SharedPassword: true})
	if err != nil || len(stale) != 2 {
		t.Fatalf("rotation from the recovery slot: %v, stale %+v", err, stale)
	}
	u.Close()
}

func TestRegistryUpdates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	rk := recoveryKey(t)
	u := mustCreate(t, path, PasswordSlot{Password: "p", Argon2: fast}, RecoverySlot{Key: rk})
	s, _ := u.Session()
	u.Close()
	boom := errors.New("boom")
	if err := s.UpdateRegistry(func(g *format.Registry) error {
		g.Archives = append(g.Archives, archive(t, s, "never"))
		return boom
	}); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if len(s.Registry().Archives) != 0 {
		t.Fatal("a failed update changed the registry")
	}
	// A registry that does not validate is refused and leaves the file
	// untouched.
	seq := u.k.sb.Seq
	if err := s.UpdateRegistry(func(g *format.Registry) error {
		a := archive(t, s, "bad")
		a.Policy = 0xFFFF
		g.Archives = append(g.Archives, a)
		return nil
	}); !errors.Is(err, format.ErrInvalid) {
		t.Fatalf("invalid registry: %v", err)
	}
	if u.k.sb.Seq != seq {
		t.Fatal("a refused update advanced the sequence")
	}
	// Many updates of growing size exercise the alternating registry
	// placement and the trim.
	for i := 0; i < 12; i++ {
		name := string(bytes.Repeat([]byte("n"), 1000*i))
		if err := s.UpdateRegistry(func(g *format.Registry) error {
			g.Archives = append(g.Archives, archive(t, s, name))
			return nil
		}); err != nil {
			t.Fatalf("update %d: %v", i, err)
		}
		k := mustOpen(t, path)
		if k.sb.Seq != seq+uint64(i)+1 {
			t.Fatalf("update %d: seq %d", i, k.sb.Seq)
		}
		if a, b := registryEnds(t, path); k.size != max(a, b) {
			t.Errorf("update %d: file %d bytes, registries end at %d and %d", i, k.size, a, b)
		}
		u2, err := k.Unlock(PasswordCredential{Password: "p"})
		if err != nil {
			t.Fatalf("update %d: %v", i, err)
		}
		if len(u2.Registry().Archives) != i+1 {
			t.Fatalf("update %d: %d archives", i, len(u2.Registry().Archives))
		}
		u2.Close()
		k.Close()
	}
	s.Lock()
	u.k.Close()
}

// TestCrashBeforeFlip builds the file as a crash would leave it — the new
// slot region and registry written, the superblock not yet — and expects
// the previous state, intact.
func TestCrashBeforeFlip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	rk := recoveryKey(t)
	u := mustCreate(t, path, PasswordSlot{Password: "p", Argon2: fast}, RecoverySlot{Key: rk})
	u.Close()
	u.k.Close()
	before := snapshot(t, path)
	kBefore := mustOpen(t, path)
	live := kBefore.live
	kBefore.Close()

	k := mustOpen(t, path)
	u, err := k.Unlock(PasswordCredential{Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	// One commit that changes everything: region, registry, generation.
	if _, err := u.Rotate(RotateOptions{}); err != nil {
		t.Fatal(err)
	}
	u.Close()
	k.Close()
	after := snapshot(t, path)

	// Undo the superblock write only: the flipped copy gets its pre-commit
	// bytes back.
	crashed := bytes.Clone(after)
	target := live.Other().KeystoreSuperblockOff()
	copy(crashed[target:target+format.SuperblockSize], before[target:target+format.SuperblockSize])
	restore(t, path, crashed)
	k = mustOpen(t, path)
	if k.Generation() != 1 || len(k.Slots()) != 2 {
		t.Fatalf("crashed file: gen %d, %d slots", k.Generation(), len(k.Slots()))
	}
	u, err = k.Unlock(PasswordCredential{Password: "p"})
	if err != nil {
		t.Fatalf("crashed file: %v", err)
	}
	if u.Tampered() != nil {
		t.Errorf("crashed file: %v", u.Tampered())
	}
	u.Close()
	k.Close()

	// A damaged losing copy is reported but does not stop the open.
	damaged := bytes.Clone(after)
	damaged[live.KeystoreSuperblockOff()+100] ^= 1
	restore(t, path, damaged)
	k = mustOpen(t, path)
	if k.Stale == nil || k.Generation() != 2 {
		t.Errorf("damaged loser: stale %v gen %d", k.Stale, k.Generation())
	}
	k.Close()
	// A damaged live copy opens one commit behind: its registry was kept.
	damagedLive := bytes.Clone(after)
	damagedLive[live.Other().KeystoreSuperblockOff()+100] ^= 1
	restore(t, path, damagedLive)
	k = mustOpen(t, path)
	if k.Stale == nil || k.Generation() != 1 {
		t.Errorf("damaged live copy: stale %v gen %d", k.Stale, k.Generation())
	}
	if u, err := k.Unlock(PasswordCredential{Password: "p"}); err != nil {
		t.Errorf("one commit behind: %v", err)
	} else {
		u.Close()
	}
	k.Close()
	// Both damaged: no open.
	damaged[live.Other().KeystoreSuperblockOff()+100] ^= 1
	restore(t, path, damaged)
	if _, err := Open(path); !errors.Is(err, format.ErrInvalid) {
		t.Errorf("both damaged: %v", err)
	}
}

func TestExport(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "v.eks")
	rk := recoveryKey(t)
	tok := newToken(t)
	u := mustCreate(t, path, HardwareSlot{PublicKey: tok.PublicKey()}, RecoverySlot{Key: rk, Label: "paper"})
	withSession(t, u, func(s *Session) {
		if err := u.UpdateRegistry(func(g *format.Registry) error {
			g.Archives = append(g.Archives, archive(t, s, "photos"))
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
	export := filepath.Join(dir, "backup.eks")
	if err := u.Export(export); err != nil {
		t.Fatal(err)
	}
	if err := u.Export(export); err == nil {
		t.Error("export over an existing file")
	}
	vault := u.k.VaultID()
	u.Close()
	u.k.Close()

	// The export opens with the recovery key only, carries the registry,
	// and becomes a full keystore by enrolling new slots.
	e := mustOpen(t, export)
	defer e.Close()
	if e.VaultID() != vault || len(e.Slots()) != 1 || e.Slots()[0].Type != format.SlotRecovery {
		t.Fatalf("export: vault %x slots %+v", e.VaultID(), e.Slots())
	}
	if _, err := e.Unlock(HardwareCredential{Token: tok}); !errors.Is(err, ErrNoSlot) {
		t.Errorf("token against the export: %v", err)
	}
	ue, err := e.Unlock(RecoveryCredential{Key: rk})
	if err != nil {
		t.Fatal(err)
	}
	defer ue.Close()
	if ue.Tampered() != nil {
		t.Fatal(ue.Tampered())
	}
	if g := ue.Registry(); len(g.Archives) != 1 || g.Archives[0].Name != "photos" {
		t.Fatalf("export registry: %+v", g.Archives)
	}
	if err := ue.AddSlot(HardwareSlot{PublicKey: newToken(t).PublicKey(), Label: "replacement"}); err != nil {
		t.Fatalf("enrol into the export: %v", err)
	}
	// A keystore without a recovery slot cannot be exported.
	path2 := filepath.Join(dir, "v2.eks")
	u2 := mustCreate(t, path2, HardwareSlot{PublicKey: tok.PublicKey()}, HardwareSlot{PublicKey: newToken(t).PublicKey()})
	defer u2.k.Close()
	defer u2.Close()
	if err := u2.Export(filepath.Join(dir, "none.eks")); !errors.Is(err, ErrNoRecoverySlot) {
		t.Errorf("export without recovery slot: %v", err)
	}
}

func TestOpenRefusesDamage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	rk := recoveryKey(t)
	u := mustCreate(t, path, PasswordSlot{Password: "p", Argon2: fast}, RecoverySlot{Key: rk})
	u.Close()
	u.k.Close()
	good := snapshot(t, path)
	k := mustOpen(t, path)
	regOff, regLen := k.sb.RegistryOff, k.sb.RegistryLen
	k.Close()

	cases := map[string]func([]byte) []byte{
		"truncated to the fixed regions": func(b []byte) []byte { return b[:format.RegistryMinOff] },
		"truncated inside the registry":  func(b []byte) []byte { return b[:regOff+regLen/2] },
		"empty":                          func(b []byte) []byte { return nil },
		"garbage":                        func(b []byte) []byte { g := make([]byte, len(b)); rand.Read(g); return g },
		"registry tag mismatch":          func(b []byte) []byte { b[regOff+regLen] ^= 1; return b },
	}
	for name, mutate := range cases {
		restore(t, path, mutate(bytes.Clone(good)))
		if _, err := Open(path); !errors.Is(err, format.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Registry ciphertext damage is found at unlock, as corruption rather
	// than as a wrong credential.
	b := bytes.Clone(good)
	b[regOff+regLen/2] ^= 1
	restore(t, path, b)
	k = mustOpen(t, path)
	if _, err := k.Unlock(PasswordCredential{Password: "p"}); !errors.Is(err, format.ErrInvalid) || errors.Is(err, ErrVerifier) {
		t.Errorf("registry damage: %v", err)
	}
	k.Close()
	restore(t, path, good)
}

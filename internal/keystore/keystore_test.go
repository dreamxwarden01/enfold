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

// TestCommitNeedsRegistry: with modified_at in the registry AAD, a commit
// that re-seals nothing would leave a file no credential opens; commit
// refuses the shape before writing anything.
func TestCommitNeedsRegistry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	u := mustCreate(t, path, PasswordSlot{Password: "p", Argon2: fast}, RecoverySlot{Key: recoveryKey(t)})
	before := u.Keystore().ModifiedAt()
	if err := u.k.commit(txn{gen: u.gen}); !errors.Is(err, ErrParams) {
		t.Fatalf("registry-less commit: %v", err)
	}
	if err := u.k.commit(txn{slots: cloneSlots(u.k.slots), gen: u.gen}); !errors.Is(err, ErrParams) {
		t.Fatalf("slots without a registry: %v", err)
	}
	if u.Keystore().ModifiedAt() != before || u.Keystore().Broken() != nil {
		t.Fatal("a refused commit changed state")
	}
}

// TestModifiedAt: R35. The superblock dates every commit, never goes
// backwards, dates an export by its creation, and is authenticated by the
// registry, so an edited date fails to open.
func TestModifiedAt(t *testing.T) {
	saved := now
	clock := int64(1_700_000_000)
	now = func() int64 { return clock }
	defer func() { now = saved }()
	dir := t.TempDir()
	path := filepath.Join(dir, "v.eks")
	rk := recoveryKey(t)
	u := mustCreate(t, path, PasswordSlot{Password: "p", Argon2: fast}, RecoverySlot{Key: rk})
	if got := u.Keystore().ModifiedAt(); got != clock || u.Registry().ModifiedAt != clock {
		t.Fatalf("create: superblock %d registry %d", got, u.Registry().ModifiedAt)
	}
	// The clock goes backwards: the stamp still advances by one.
	clock = 1_600_000_000
	if err := u.UpdateRegistry(func(g *format.Registry) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if got := u.Keystore().ModifiedAt(); got != 1_700_000_001 || u.Registry().ModifiedAt != got {
		t.Fatalf("clock back: superblock %d registry %d", got, u.Registry().ModifiedAt)
	}
	// The clock moves on: the stamp follows it.
	clock = 1_700_000_500
	if err := u.AddSlot(RecoverySlot{Key: recoveryKey(t), Label: "second"}); err != nil {
		t.Fatal(err)
	}
	if got := u.Keystore().ModifiedAt(); got != 1_700_000_500 {
		t.Fatalf("clock on: %d", got)
	}
	// An export is dated by its creation.
	clock = 1_700_000_900
	export := filepath.Join(dir, "backup.eks")
	if err := u.Export(export); err != nil {
		t.Fatal(err)
	}
	e := mustOpen(t, export)
	if e.ModifiedAt() != 1_700_000_900 {
		t.Fatalf("export dated %d", e.ModifiedAt())
	}
	e.Close()
	// It is readable before an unlock, from the file alone.
	u.Close()
	u.k.Close()
	k := mustOpen(t, path)
	if k.ModifiedAt() != 1_700_000_500 {
		t.Fatalf("reopened: %d", k.ModifiedAt())
	}
	k.Close()
	// A doctored date: both superblock copies re-dated with valid checksums.
	// The file opens, but the registry no longer authenticates.
	data := snapshot(t, path)
	for _, c := range []format.Copy{format.CopyA, format.CopyB} {
		off := c.KeystoreSuperblockOff()
		sb, err := format.DecodeKeystoreSuperblock(data[off : off+format.SuperblockSize])
		if err != nil {
			continue
		}
		sb.ModifiedAt = 1_800_000_000
		enc, err := sb.Encode()
		if err != nil {
			t.Fatal(err)
		}
		copy(data[off:], enc)
	}
	restore(t, path, data)
	d := mustOpen(t, path)
	if d.ModifiedAt() != 1_800_000_000 {
		t.Fatalf("doctored date not read: %d", d.ModifiedAt())
	}
	if _, err := d.Unlock(PasswordCredential{Password: "p"}); !errors.Is(err, format.ErrInvalid) {
		t.Fatalf("doctored date opened: %v", err)
	}
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
		{"one token twice (R34)", []SlotSpec{HardwareSlot{PublicKey: tokA.PublicKey()}, HardwareSlot{PublicKey: tokA.PublicKey()}, RecoverySlot{Key: rk}}, ErrDuplicate},
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
	// R34 counts every non-empty record, retired ones included, as the
	// decoder does.
	retired := cloneSlots(u.k.slots)
	for i := range retired {
		if retired[i].Type == format.SlotExternalECDH {
			retired[i].State = format.SlotRetired
		}
	}
	dup := retired[0]
	dup.RecipientID[0] ^= 1
	dup.State = format.SlotActive
	if err := checkInvariant(append(retired, dup)); !errors.Is(err, ErrDuplicate) {
		t.Errorf("retired duplicate slot_pubkey accepted by the invariant: %v", err)
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
	newRegionOff := oldK.sb.SlotRegionOff
	oldK.Close() // one handle per file: the lock refuses a second
	restore(t, path, before)
	oldOpen := mustOpen(t, path)
	oldRegion := bytes.Clone(oldOpen.region)
	oldOpen.Close()
	restore(t, path, after)
	spliced := bytes.Clone(after)
	copy(spliced[newRegionOff:], oldRegion)
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
	// The kept recovery key is read, not refused as tampered (R38) — and
	// here the substituted slot is the recovery slot itself, so what the
	// reading finds is a key that does not fit the slot: never shown, and
	// never kept for it either.
	rid := k.Slots()[1].RecipientID
	if _, err := u.RecoveryKey(rid); !errors.Is(err, ErrEscrowMismatch) {
		t.Errorf("recovery key while tampered: %v", err)
	}
	if err := u.EscrowOpenedKey(rid, rk); err != nil {
		t.Errorf("a key already kept, while tampered: %v", err) // the record exists: nothing to do
	}
	if err := u.UpdateRegistry(func(g *format.Registry) error {
		g.Escrows = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := u.EscrowOpenedKey(rid, rk); !errors.Is(err, ErrEscrowMismatch) {
		t.Errorf("handing in for the substituted slot: %v", err)
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
		// The file holds exactly what the handle believes: the lock allows
		// no second handle, so the on-disk facts are read directly.
		if u.k.sb.Seq != seq+uint64(i)+1 {
			t.Fatalf("update %d: seq %d", i, u.k.sb.Seq)
		}
		if a, b := registryEnds(t, path); u.k.size != max(a, b) {
			t.Errorf("update %d: file %d bytes, registries end at %d and %d", i, u.k.size, a, b)
		}
	}
	s.Lock()
	u.k.Close()
	// Reopened, the last state is what a fresh handle sees.
	k := mustOpen(t, path)
	if k.sb.Seq != seq+12 {
		t.Fatalf("reopened: seq %d", k.sb.Seq)
	}
	u2, err := k.Unlock(PasswordCredential{Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if len(u2.Registry().Archives) != 12 {
		t.Fatalf("reopened: %d archives", len(u2.Registry().Archives))
	}
	u2.Close()
	k.Close()
}

// TestOneHandlePerFile: the OS lock refuses a second handle on an open
// keystore (ErrBusy), and a commit through a handle whose view of the file
// is stale is refused before anything is written (ErrConflict).
func TestOneHandlePerFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	rk := recoveryKey(t)
	u := mustCreate(t, path, PasswordSlot{Password: "p", Argon2: fast}, RecoverySlot{Key: rk})
	if _, err := Open(path); !errors.Is(err, ErrBusy) {
		t.Fatalf("second handle: %v", err)
	}
	u.Close()
	u.k.Close()
	k := mustOpen(t, path)
	u, err := k.Unlock(PasswordCredential{Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	s, err := u.Session()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Live(); err != nil {
		t.Fatalf("live session: %v", err)
	}
	// Another writer moved the file on: both superblock copies re-dated
	// with a higher seq, valid checksums.
	data := snapshot(t, path)
	for _, c := range []format.Copy{format.CopyA, format.CopyB} {
		off := c.KeystoreSuperblockOff()
		sb, err := format.DecodeKeystoreSuperblock(data[off : off+format.SuperblockSize])
		if err != nil {
			continue
		}
		sb.Seq += 10
		enc, err := sb.Encode()
		if err != nil {
			t.Fatal(err)
		}
		copy(data[off:], enc)
	}
	restore(t, path, data)
	err = s.UpdateRegistry(func(g *format.Registry) error { return nil })
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale handle committed: %v", err)
	}
	if k.Broken() == nil || s.Live() == nil {
		t.Fatal("a conflict must mark the handle for reopening")
	}
	s.Lock()
	if !errors.Is(s.Live(), ErrClosed) && !errors.Is(s.Live(), ErrConflict) {
		t.Fatalf("locked session live: %v", s.Live())
	}
	u.Close()
	k.Close()
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

// The recovery key is kept once more under the VMK (R38): an Unlocked can
// show it again; the record lives and dies with its slot, survives a
// rotation and travels in an export; a slot without one says so.
func TestRecoveryKeyEscrow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "v.eks")
	rk := recoveryKey(t)
	u := mustCreate(t, path, PasswordSlot{Password: "correct horse", Argon2: fast, Label: "pw"}, RecoverySlot{Key: rk, Label: "paper"})
	var rid, pwID [16]byte
	for _, s := range u.k.Slots() {
		if s.Type == format.SlotRecovery {
			rid = s.RecipientID
		} else {
			pwID = s.RecipientID
		}
	}
	if got, err := u.RecoveryKey(rid); err != nil || got != rk {
		t.Fatalf("recovery key from the escrow: %x %v", got, err)
	}
	if _, err := u.RecoveryKey(pwID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a password slot has no recovery key: %v", err)
	}
	if _, err := u.RecoveryKey([16]byte{9}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown slot: %v", err)
	}
	if len(u.Registry().Escrows) != 1 {
		t.Fatalf("escrows after create: %d", len(u.Registry().Escrows))
	}

	// A second recovery slot gets its own record; a rotation re-wraps both.
	rk2 := recoveryKey(t)
	if err := u.AddSlot(RecoverySlot{Key: rk2, Label: "second"}); err != nil {
		t.Fatal(err)
	}
	var rid2 [16]byte
	for _, s := range u.k.Slots() {
		if s.Type == format.SlotRecovery && s.RecipientID != rid {
			rid2 = s.RecipientID
		}
	}
	if _, err := u.Rotate(RotateOptions{}); err != nil {
		t.Fatal(err)
	}
	if got, err := u.RecoveryKey(rid); err != nil || got != rk {
		t.Fatalf("first key after a rotation: %x %v", got, err)
	}
	if got, err := u.RecoveryKey(rid2); err != nil || got != rk2 {
		t.Fatalf("second key after a rotation: %x %v", got, err)
	}

	// Removing the slot removes the record; an orphan record left by a
	// registry-only write is dropped by the next slot-region write.
	if err := u.RemoveSlot(rid2); err != nil {
		t.Fatal(err)
	}
	if _, err := u.RecoveryKey(rid2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("removed slot: %v", err)
	}
	if len(u.Registry().Escrows) != 1 {
		t.Fatalf("escrows after remove: %d", len(u.Registry().Escrows))
	}
	if err := u.UpdateRegistry(func(g *format.Registry) error {
		g.Escrows = append(g.Escrows, format.EscrowRecord{RecipientID: [16]byte{7}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := u.AddSlot(RecoverySlot{Key: recoveryKey(t), Label: "third"}); err != nil {
		t.Fatal(err)
	}
	for _, e := range u.Registry().Escrows {
		if e.RecipientID == ([16]byte{7}) {
			t.Fatal("an orphan escrow record survived a slot-region write")
		}
	}
	if len(u.Registry().Escrows) != 2 {
		t.Fatalf("escrows after the orphan was dropped: %d", len(u.Registry().Escrows))
	}

	// A slot whose record is missing — made before escrow — cannot be
	// shown again, until the key is handed in by an unlock through it.
	if err := u.UpdateRegistry(func(g *format.Registry) error {
		g.Escrows = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := u.RecoveryKey(rid); !errors.Is(err, ErrNoEscrow) {
		t.Fatalf("slot without escrow: %v", err)
	}
	if err := u.EscrowOpenedKey(rid, recoveryKey(t)); !errors.Is(err, ErrEscrowMismatch) {
		t.Fatalf("another key handed in: %v", err)
	}
	if err := u.EscrowOpenedKey(pwID, rk); !errors.Is(err, ErrNotFound) {
		t.Fatalf("handed in for a password slot: %v", err)
	}
	if err := u.EscrowOpenedKey(rid, rk); err != nil {
		t.Fatalf("handed in: %v", err)
	}
	if err := u.EscrowOpenedKey(rid, rk); err != nil {
		t.Fatalf("handed in again: %v", err)
	}
	if got, err := u.RecoveryKey(rid); err != nil || got != rk || len(u.Registry().Escrows) != 1 {
		t.Fatalf("after handing in: %x %v escrows=%d", got, err, len(u.Registry().Escrows))
	}
	// A record moved to another slot fails its AAD (corruption); one that
	// opens but holds another key is never shown.
	var rid3 [16]byte
	for _, s := range u.k.Slots() {
		if s.Type == format.SlotRecovery && s.RecipientID != rid {
			rid3 = s.RecipientID
		}
	}
	if err := u.UpdateRegistry(func(g *format.Registry) error {
		a := *g.Escrow(rid)
		a.RecipientID = rid3
		g.Escrows = append(g.Escrows, a)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := u.RecoveryKey(rid3); !errors.Is(err, format.ErrInvalid) {
		t.Fatalf("a record moved to another slot: %v", err)
	}
	// Such a record fails a rotation too (fail closed), so it goes first.
	if _, err := u.Rotate(RotateOptions{}); !errors.Is(err, format.ErrInvalid) {
		t.Fatalf("a rotation over a record that does not unwrap: %v", err)
	}
	if err := u.UpdateRegistry(func(g *format.Registry) error {
		g.Escrows = g.Escrows[:len(g.Escrows)-1]
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := u.UpdateRegistry(func(g *format.Registry) error {
		e, err := escrowRecord(u.vmk, u.k.sb.VaultID, rid, recoveryKey(t))
		if err != nil {
			return err
		}
		*g.Escrow(rid) = e
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := u.RecoveryKey(rid); !errors.Is(err, ErrEscrowMismatch) {
		t.Fatalf("a record holding another key: %v", err)
	}
	if err := u.UpdateRegistry(func(g *format.Registry) error {
		e, err := escrowRecord(u.vmk, u.k.sb.VaultID, rid, rk)
		if err != nil {
			return err
		}
		*g.Escrow(rid) = e
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// A rotation and an export carry no orphan either.
	if err := u.UpdateRegistry(func(g *format.Registry) error {
		g.Escrows = append(g.Escrows, format.EscrowRecord{RecipientID: [16]byte{8}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := u.Rotate(RotateOptions{}); err != nil {
		t.Fatal(err)
	}
	if u.Registry().Escrow([16]byte{8}) != nil {
		t.Fatal("an orphan survived a rotation")
	}
	if err := u.RemoveSlot(rid3); err != nil {
		t.Fatal(err)
	}
	// A record that does not open under KWK_recovery is corruption, never
	// a wrong credential.
	if err := u.UpdateRegistry(func(g *format.Registry) error {
		g.Escrows[0].WrappedRecoveryKey[0] ^= 1
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := u.RecoveryKey(rid); !errors.Is(err, format.ErrInvalid) {
		t.Fatalf("damaged escrow: %v", err)
	}
	if err := u.UpdateRegistry(func(g *format.Registry) error {
		g.Escrows[0].WrappedRecoveryKey[0] ^= 1
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// The export carries the record: opened with the recovery key, it
	// shows that key again.
	export := filepath.Join(dir, "backup.eks")
	if err := u.Export(export); err != nil {
		t.Fatal(err)
	}
	u.Close()
	u.k.Close()
	e := mustOpen(t, export)
	ue, err := e.Unlock(RecoveryCredential{Key: rk})
	if err != nil {
		t.Fatal(err)
	}
	defer ue.Close()
	if got, err := ue.RecoveryKey(rid); err != nil || got != rk {
		t.Fatalf("recovery key from the export: %x %v", got, err)
	}
	if n := len(ue.Registry().Escrows); n != 1 {
		t.Fatalf("the export carries records of slots it does not: %d", n)
	}
	// After Close the VMK is gone, and so is the way to the record.
	ue.Close()
	if _, err := ue.RecoveryKey(rid); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed: %v", err)
	}
}

// A vault whose registry an older Enfold wrote as version 1 — no escrow
// records — opens, keeps no recovery key until one is handed in, and is
// written back as version 2 by its next commit (R38).
func TestRegistryVersionOneMigrates(t *testing.T) {
	old := encodeRegistry
	encodeRegistry = func(g *format.Registry) ([]byte, error) {
		v1 := *g
		v1.Escrows = nil
		b, err := v1.Encode()
		if err != nil {
			return nil, err
		}
		b = append([]byte(nil), b...)
		b[0] = 1
		return b[:len(b)-4], nil // version 1 ends after the peer pin records
	}
	path := filepath.Join(t.TempDir(), "v.eks")
	rk := recoveryKey(t)
	u := mustCreate(t, path, PasswordSlot{Password: "p", Argon2: fast}, RecoverySlot{Key: rk, Label: "paper"})
	encodeRegistry = old
	var rid [16]byte
	for _, s := range u.k.Slots() {
		if s.Type == format.SlotRecovery {
			rid = s.RecipientID
		}
	}
	if len(u.Registry().Escrows) != 0 {
		t.Fatalf("a version-1 registry decoded with escrow records: %d", len(u.Registry().Escrows))
	}
	if _, err := u.RecoveryKey(rid); !errors.Is(err, ErrNoEscrow) {
		t.Fatalf("before any commit: %v", err)
	}
	// The next commit — here a new recovery slot — writes version 2, with
	// the new slot's record and still none for the old one.
	if err := u.AddSlot(RecoverySlot{Key: recoveryKey(t), Label: "second"}); err != nil {
		t.Fatal(err)
	}
	u.Close()
	u.k.Close()
	k := mustOpen(t, path)
	u2, err := k.Unlock(PasswordCredential{Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	defer u2.Close()
	if n := len(u2.Registry().Escrows); n != 1 || u2.Registry().Escrow(rid) != nil {
		t.Fatalf("after the first commit: %d records, old slot kept=%v", n, u2.Registry().Escrow(rid) != nil)
	}
	// Handed in, the old key is kept from then on.
	if err := u2.EscrowOpenedKey(rid, rk); err != nil {
		t.Fatal(err)
	}
	u2.Close()
	k.Close()
	k = mustOpen(t, path)
	u3, err := k.Unlock(RecoveryCredential{Key: rk})
	if err != nil {
		t.Fatal(err)
	}
	defer u3.Close()
	defer k.Close()
	if got, err := u3.RecoveryKey(rid); err != nil || got != rk || len(u3.Registry().Escrows) != 2 {
		t.Fatalf("after handing in: %x %v records=%d", got, err, len(u3.Registry().Escrows))
	}
}

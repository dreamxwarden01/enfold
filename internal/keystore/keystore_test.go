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

// softToken is a P-256 key in software standing in for a YubiKey. calls
// counts the ceremonies: an unlock touches the token exactly once, and a
// refusal decided before the loop touches it not at all.
type softToken struct {
	key   *ecdh.PrivateKey
	calls int
}

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
	s.calls++
	pub, err := ecdh.P256().NewPublicKey(epk)
	if err != nil {
		return nil, err
	}
	return s.key.ECDH(pub)
}

func mustCreate(t testing.TB, path string, slots ...SlotSpec) *Unlocked {
	t.Helper()
	return mustCreateWith(t, path, CreateOptions{Slots: slots})
}

func mustCreateWith(t testing.TB, path string, opts CreateOptions) *Unlocked {
	t.Helper()
	u, err := Create(path, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { u.Close(); u.k.Close() })
	return u
}

// countArgon2 counts the K_P derivations of the tests that follow. K_P depends
// on the password and the slot region header alone (§3.1), so an unlock runs
// Argon2id once however many hardware slots it walks.
func countArgon2(t testing.TB) *int {
	t.Helper()
	n := 0
	saved := entangledKey
	entangledKey = func(pw []byte, es kdf.EntangleSalt, vaultID [16]byte, p kdf.Argon2Params) ([32]byte, error) {
		n++
		return saved(pw, es, vaultID, p)
	}
	t.Cleanup(func() { entangledKey = saved })
	return &n
}

// secretsOf counts the records of one kind in a registry.
func secretsOf(g *format.Registry, kind format.SecretKind) int {
	return len(g.SecretsOfKind(kind))
}

// historyGenerations are the retired generations the secrets section holds,
// in section order.
func historyGenerations(g *format.Registry) []uint64 {
	var out []uint64
	for _, s := range g.SecretsOfKind(format.SecretVMKHistory) {
		gen, _ := s.HistoryGeneration()
		out = append(out, gen)
	}
	return out
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
	if k.VaultID() != vault || k.Generation() != 1 || k.Entangled() || k.Stale != nil {
		t.Fatalf("reopened: vault %x gen %d entangled %v stale %v", k.VaultID(), k.Generation(), k.Entangled(), k.Stale)
	}
	if k.FileSize() == 0 {
		t.Error("FileSize is zero")
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
	hdr := u.k.hdr
	if err := u.k.commit(txn{hdr: &hdr, gen: u.gen}); !errors.Is(err, ErrParams) {
		t.Fatalf("registry-less commit: %v", err)
	}
	if err := u.k.commit(txn{slots: cloneSlots(u.k.slots), hdr: &hdr, gen: u.gen}); !errors.Is(err, ErrParams) {
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

// TestInvariant: §6.4 is a predicate over the records and the slot region
// header together. The header's entangle byte, not a per-slot flag, decides
// whether every hardware slot shares the one secret "pwd" (§18.1).
func TestInvariant(t *testing.T) {
	dir := t.TempDir()
	rk := recoveryKey(t)
	tokA, tokB := newToken(t), newToken(t)
	vaultPass := &Entangle{Password: "the vault's", Argon2: fast}
	cases := []struct {
		name     string
		entangle *Entangle
		slots    []SlotSpec
		want     error
	}{
		{"password alone", nil, []SlotSpec{PasswordSlot{Password: "p", Argon2: fast}}, ErrInvariant},
		{"recovery alone", nil, []SlotSpec{RecoverySlot{Key: rk}}, ErrInvariant},
		{"nothing", nil, nil, ErrInvariant},
		{"password + hardware", nil, []SlotSpec{PasswordSlot{Password: "p", Argon2: fast}, HardwareSlot{PublicKey: tokA.PublicKey()}, RecoverySlot{Key: rk}}, ErrPolicy},
		{"two tokens, entangled", vaultPass, []SlotSpec{HardwareSlot{PublicKey: tokA.PublicKey()}, HardwareSlot{PublicKey: tokB.PublicKey()}}, ErrInvariant},
		{"one token, entangled, plus recovery", vaultPass, []SlotSpec{HardwareSlot{PublicKey: tokA.PublicKey()}, RecoverySlot{Key: rk}}, nil},
		{"two tokens, entangled, plus recovery", vaultPass, []SlotSpec{HardwareSlot{PublicKey: tokA.PublicKey()}, HardwareSlot{PublicKey: tokB.PublicKey()}, RecoverySlot{Key: rk}}, nil},
		{"two tokens, plain", nil, []SlotSpec{HardwareSlot{PublicKey: tokA.PublicKey()}, HardwareSlot{PublicKey: tokB.PublicKey()}}, nil},
		{"one token twice (R34)", nil, []SlotSpec{HardwareSlot{PublicKey: tokA.PublicKey()}, HardwareSlot{PublicKey: tokA.PublicKey()}, RecoverySlot{Key: rk}}, ErrDuplicate},
		{"two passwords", nil, []SlotSpec{PasswordSlot{Password: "p", Argon2: fast}, PasswordSlot{Password: "q", Argon2: fast}}, ErrInvariant},
		{"password + recovery", nil, []SlotSpec{PasswordSlot{Password: "p", Argon2: fast}, RecoverySlot{Key: rk}}, nil},
		{"entangled with an empty password", &Entangle{Argon2: fast}, []SlotSpec{HardwareSlot{PublicKey: tokA.PublicKey()}, RecoverySlot{Key: rk}}, ErrParams},
		{"entangled without argon2", &Entangle{Password: "a"}, []SlotSpec{HardwareSlot{PublicKey: tokA.PublicKey()}, RecoverySlot{Key: rk}}, ErrParams},
		{"bad token key", nil, []SlotSpec{HardwareSlot{PublicKey: []byte{4, 1, 2}}, RecoverySlot{Key: rk}}, ErrParams},
	}
	for i, c := range cases {
		path := filepath.Join(dir, "case"+string(rune('a'+i))+".eks")
		u, err := Create(path, CreateOptions{Slots: c.slots, Entangle: c.entangle})
		if !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
		if err == nil {
			if u.k.Entangled() != (c.entangle != nil) {
				t.Errorf("%s: Entangled %v", c.name, u.k.Entangled())
			}
			u.Close()
			u.k.Close()
		} else if _, statErr := os.Stat(path); statErr == nil {
			t.Errorf("%s: file left behind after a refused creation", c.name)
		}
	}

	// The header is one of the predicate's inputs, evaluated over the sets the
	// new value would make: a hardware-only vault cannot turn the switch on,
	// and it takes a recovery slot to become able to.
	hwOnly := filepath.Join(dir, "hardware-only.eks")
	uh := mustCreate(t, hwOnly, HardwareSlot{PublicKey: tokA.PublicKey()}, HardwareSlot{PublicKey: tokB.PublicKey()})
	if err := uh.k.CanEnableEntangled(); !errors.Is(err, ErrInvariant) {
		t.Errorf("CanEnableEntangled on a hardware-only vault: %v", err)
	}
	// SetEntangled agrees with the prediction, and refuses before the password
	// is used: no salt is drawn and Argon2id never runs.
	n := countArgon2(t)
	if err := uh.SetEntangled(true, vaultPass); !errors.Is(err, ErrInvariant) {
		t.Errorf("SetEntangled(true) on a hardware-only vault: %v", err)
	}
	if *n != 0 {
		t.Errorf("the refused switch derived K_P %d times", *n)
	}
	if uh.k.Entangled() || uh.k.hdr != (format.SlotRegionHeader{}) {
		t.Errorf("the refused switch wrote a header: %+v", uh.k.hdr)
	}
	// Turning it off is never refused, even on a vault that already has it off.
	if err := uh.SetEntangled(false, nil); err != nil {
		t.Errorf("SetEntangled(false) on a vault whose switch is off: %v", err)
	}
	if err := uh.AddSlot(RecoverySlot{Key: recoveryKey(t), Label: "paper"}); err != nil {
		t.Fatal(err)
	}
	if err := uh.k.CanEnableEntangled(); err != nil {
		t.Errorf("CanEnableEntangled once a recovery slot exists: %v", err)
	}
	if err := uh.SetEntangled(true, vaultPass); err != nil {
		t.Errorf("SetEntangled(true) once a recovery slot exists: %v", err)
	}
	if err := uh.SetEntangled(false, nil); err != nil {
		t.Errorf("turning it off again: %v", err)
	}
	// While the switch is on, the last recovery slot is what keeps the sets
	// disjoint, so it cannot be removed; with the switch off it can.
	ent := filepath.Join(dir, "entangled.eks")
	ue := mustCreateWith(t, ent, CreateOptions{
		Entangle: vaultPass,
		Slots:    []SlotSpec{HardwareSlot{PublicKey: tokA.PublicKey()}, HardwareSlot{PublicKey: tokB.PublicKey()}, RecoverySlot{Key: rk}},
	})
	var recID [16]byte
	for _, s := range ue.k.Slots() {
		if s.Type == format.SlotRecovery {
			recID = s.RecipientID
		}
	}
	if ue.k.Removable(recID) {
		t.Error("the last recovery slot is removable while the vault is entangled")
	}
	if err := ue.RemoveSlot(recID); !errors.Is(err, ErrInvariant) {
		t.Errorf("removing it: %v", err)
	}
	plain := filepath.Join(dir, "plain.eks")
	up := mustCreate(t, plain, HardwareSlot{PublicKey: tokA.PublicKey()}, HardwareSlot{PublicKey: tokB.PublicKey()}, RecoverySlot{Key: rk})
	for _, s := range up.k.Slots() {
		if s.Type == format.SlotRecovery && !up.k.Removable(s.RecipientID) {
			t.Error("with the switch off the two tokens are two ways in, so the recovery slot is removable")
		}
	}

	// Removal is held to the same predicate.
	path := filepath.Join(dir, "remove-cases.eks")
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

// TestHardware: entanglement is the vault's, so a key enrolled into a vault
// whose switch is off is token-only and a record carries nothing about it
// (§18.1, R13).
func TestHardware(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	tokA, tokB := newToken(t), newToken(t)
	rk := recoveryKey(t)
	u := mustCreate(t, path, HardwareSlot{PublicKey: tokA.PublicKey(), Label: "A"}, RecoverySlot{Key: rk})
	if err := u.AddSlot(HardwareSlot{PublicKey: tokB.PublicKey(), Label: "B"}); err != nil {
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
	if err := checkInvariant(u.k.hdr, append(retired, dup)); !errors.Is(err, ErrDuplicate) {
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
	if len(infos) != 3 || !bytes.Equal(infos[0].PublicKey, tokA.PublicKey()) || k.Entangled() {
		t.Fatalf("slots: %+v, entangled %v", infos, k.Entangled())
	}
	// Every hardware slot's record carries zero salt and zero Argon2
	// parameters: they are the header's now (R13, R24).
	for i := range k.slots {
		s := &k.slots[i]
		if s.Type != format.SlotExternalECDH {
			continue
		}
		if s.Salt != ([32]byte{}) || s.Argon2M != 0 || s.Argon2T != 0 || s.Argon2P != 0 || s.Flags != 0 {
			t.Errorf("hardware slot carries Argon2 material: salt %x m %d t %d p %d flags %d", s.Salt, s.Argon2M, s.Argon2T, s.Argon2P, s.Flags)
		}
	}
	// The switch is off, so a password handed in is ignored, not refused.
	for _, tok := range []*softToken{tokA, tokB} {
		for _, pw := range []string{"", "ignored"} {
			u, err := k.Unlock(HardwareCredential{Token: tok, Password: pw})
			if err != nil {
				t.Fatalf("token (password %q): %v", pw, err)
			}
			u.Close()
		}
	}
	// A token whose ECDH fails surfaces its error.
	if _, err := k.Unlock(HardwareCredential{Token: &failingToken{pub: tokA.PublicKey()}}); err == nil || errors.Is(err, ErrNoSlot) {
		t.Errorf("failing token: %v", err)
	}
}

// TestEntangledUnlock: the vault's password gates every hardware slot, is
// decided from the header before any token is touched, and is turned into K_P
// once per unlock rather than once per slot (§3.1, §6, §18.1).
func TestEntangledUnlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "v.eks")
	tokA, tokB := newToken(t), newToken(t)
	rk := recoveryKey(t)
	const pass = "the vault's own"
	u := mustCreateWith(t, path, CreateOptions{
		Entangle: &Entangle{Password: pass, Argon2: fast},
		Slots: []SlotSpec{
			HardwareSlot{PublicKey: tokA.PublicKey(), Label: "A"},
			HardwareSlot{PublicKey: tokB.PublicKey(), Label: "B"},
			RecoverySlot{Key: rk, Label: "paper"},
		},
	})
	if u.kp == nil {
		t.Fatal("Create did not install K_P on the Unlocked it returned")
	}
	u.Close()
	u.k.Close()

	k := mustOpen(t, path)
	defer k.Close()
	if !k.Entangled() {
		t.Fatal("the header does not say the vault is entangled")
	}
	// No password: refused from the header, before the slot loop and before
	// any ceremony. The tokens record that nothing was asked of them.
	tokA.calls, tokB.calls = 0, 0
	if _, err := k.Unlock(HardwareCredential{Token: tokA}); !errors.Is(err, ErrPasswordRequired) {
		t.Errorf("no password: %v", err)
	}
	if tokA.calls != 0 || tokB.calls != 0 {
		t.Errorf("the token was touched before the password was decided: %d, %d", tokA.calls, tokB.calls)
	}

	// One password opens every hardware slot, and each unlock derives K_P
	// once — B is behind A in the region, so a per-slot derivation would run
	// Argon2id twice.
	n := countArgon2(t)
	for _, tok := range []*softToken{tokA, tokB} {
		*n = 0
		uu, err := k.Unlock(HardwareCredential{Token: tok, Password: pass})
		if err != nil {
			t.Fatalf("token with the vault password: %v", err)
		}
		if uu.kp == nil {
			t.Error("a hardware unlock did not keep K_P")
		}
		uu.Close()
		if *n != 1 {
			t.Errorf("K_P derived %d times in one unlock", *n)
		}
	}

	// A wrong password derives a different K_P, so the slot's IK is wrong and
	// the wrapped VMK does not authenticate: a hardware slot has no verifier.
	if _, err := k.Unlock(HardwareCredential{Token: tokA, Password: "not it"}); !errors.Is(err, ErrAuth) {
		t.Errorf("wrong password: %v", err)
	}

	// The recovery slot ignores the switch: no password, no Argon2id.
	*n = 0
	ur, err := k.Unlock(RecoveryCredential{Key: rk})
	if err != nil {
		t.Fatalf("recovery key against an entangled vault: %v", err)
	}
	if *n != 0 {
		t.Errorf("a recovery unlock ran Argon2id for K_P %d times", *n)
	}
	// It still holds K_P, from the kind-2 record — which is what makes every
	// offline operation behave the same whatever opened the vault.
	if ur.kp == nil {
		t.Error("a recovery unlock did not read K_P from the registry")
	}
	ur.Close()

	// A standalone password slot ignores the switch too: its own secret is
	// its own, and the vault's password is not asked for.
	pwPath := filepath.Join(dir, "pw.eks")
	up := mustCreateWith(t, pwPath, CreateOptions{
		Entangle: &Entangle{Password: "vault", Argon2: fast},
		Slots:    []SlotSpec{PasswordSlot{Password: "slot", Argon2: fast}, RecoverySlot{Key: recoveryKey(t)}},
	})
	up.Close()
	up.k.Close()
	kp := mustOpen(t, pwPath)
	defer kp.Close()
	u2, err := kp.Unlock(PasswordCredential{Password: "slot"})
	if err != nil {
		t.Fatalf("standalone password slot in an entangled vault: %v", err)
	}
	u2.Close()
	if _, err := kp.Unlock(PasswordCredential{Password: "vault"}); !errors.Is(err, ErrVerifier) {
		t.Errorf("the vault password against the standalone slot: %v", err)
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
	u := mustCreateWith(t, path, CreateOptions{
		Entangle: &Entangle{Password: "pass", Argon2: fast},
		Slots:    []SlotSpec{HardwareSlot{PublicKey: tok.PublicKey()}, RecoverySlot{Key: rk}},
	})
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
	if err := u.Rotate(); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if k.Generation() != 2 {
		t.Fatalf("after rotation: gen %d", k.Generation())
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
	// generation 1, which the superblock's 2 exposes as a rolled-back region —
	// tampering, not a credential that is behind (§18.1).
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
	if _, err := k.Unlock(RecoveryCredential{Key: rk}); !errors.Is(err, ErrTampered) {
		t.Errorf("spliced old region: %v, want ErrTampered", err)
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
	region, err := (&format.SlotRegion{Header: k.hdr, Slots: slots}).Encode()
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
	if err := u.Rotate(); !errors.Is(err, ErrTampered) {
		t.Errorf("rotate: %v", err)
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
	if err := u.Rotate(); !errors.Is(err, ErrTampered) {
		t.Errorf("rotate after a registry update: %v", err)
	}
	// And the substituted slot itself fails its AAD.
	if _, err := k.Unlock(RecoveryCredential{Key: rk}); !errors.Is(err, ErrVerifier) {
		t.Errorf("substituted slot: %v", err)
	}
	u.Close()
	k.Close()

	// A header edit is caught the same way: R25 covers the header as part of
	// the region as written (§6), so an edit to a field the opening credential
	// never reads still shows as tampering. argon2_m sits at offset 8 of the
	// 32-byte header and 128 KiB is as legal as 64.
	path2 := filepath.Join(t.TempDir(), "hdr.eks")
	u2 := mustCreateWith(t, path2, CreateOptions{
		Entangle: &Entangle{Password: "vault", Argon2: fast},
		Slots:    []SlotSpec{HardwareSlot{PublicKey: newToken(t).PublicKey()}, RecoverySlot{Key: rk}},
	})
	u2.Close()
	u2.k.Close()
	k2 := mustOpen(t, path2)
	off := k2.sb.SlotRegionOff
	k2.Close()
	edited := snapshot(t, path2)
	edited[off+8] = 128 // argon2_m 64 → 128, still within R24
	restore(t, path2, edited)
	k2 = mustOpen(t, path2)
	defer k2.Close()
	if k2.hdr.Argon2M != 128 {
		t.Fatalf("the edit did not land: argon2_m %d", k2.hdr.Argon2M)
	}
	ue, err := k2.Unlock(RecoveryCredential{Key: rk})
	if err != nil {
		t.Fatalf("recovery unlock over an edited header: %v", err)
	}
	defer ue.Close()
	if !errors.Is(ue.Tampered(), ErrTampered) {
		t.Errorf("edited header: %v", ue.Tampered())
	}
	if err := ue.AddSlot(RecoverySlot{Key: recoveryKey(t)}); !errors.Is(err, ErrTampered) {
		t.Errorf("add over an edited header: %v", err)
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
	if err := u.Rotate(); err != nil {
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
	if err := u.Rotate(); err != nil {
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

// TestExport: an export is a keystore file whose slot region holds only the
// recovery slots (R28), and whose header always says entangle 0 even when it
// was taken from an entangled vault (§15).
func TestExport(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "v.eks")
	rk := recoveryKey(t)
	tok := newToken(t)
	u := mustCreateWith(t, path, CreateOptions{
		Entangle: &Entangle{Password: "the vault's", Argon2: fast},
		Slots:    []SlotSpec{HardwareSlot{PublicKey: tok.PublicKey()}, RecoverySlot{Key: rk, Label: "paper"}},
	})
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
	// The header is written afresh with the switch off and all four fields
	// zero, so the export presents itself unentangled (R28).
	if e.Entangled() || e.hdr != (format.SlotRegionHeader{}) {
		t.Fatalf("export header: %+v", e.hdr)
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
	// A key enrolled into the adopted export inherits its header, which says
	// the switch is off: it opens with the token alone, whatever the source
	// vault's password was.
	replacement := newToken(t)
	if err := ue.AddSlot(HardwareSlot{PublicKey: replacement.PublicKey(), Label: "replacement"}); err != nil {
		t.Fatalf("enrol into the export: %v", err)
	}
	if uh, err := e.Unlock(HardwareCredential{Token: replacement}); err != nil {
		t.Errorf("the replacement key against the adopted export: %v", err)
	} else {
		uh.Close()
	}

	// Adoption chooses the entanglement afresh: the first way in of a second
	// export lands the slot and the header in one commit, with a salt this
	// vault never had, and the source vault's password does not open it (§15).
	export2 := filepath.Join(dir, "backup2.eks")
	if err := ue.Export(export2); err != nil {
		t.Fatal(err)
	}
	e2 := mustOpen(t, export2)
	defer e2.Close()
	ue2, err := e2.Unlock(RecoveryCredential{Key: rk})
	if err != nil {
		t.Fatal(err)
	}
	defer ue2.Close()
	adopted := newToken(t)
	if err := ue2.AddFirstWayIn(HardwareSlot{PublicKey: adopted.PublicKey(), Label: "new key"},
		&Entangle{Password: "chosen at adoption", Argon2: fast}); err != nil {
		t.Fatalf("first way in: %v", err)
	}
	if !e2.Entangled() || e2.hdr.EntangleSalt == ([16]byte{}) {
		t.Fatalf("adopted header: %+v", e2.hdr)
	}
	if _, err := e2.Unlock(HardwareCredential{Token: adopted, Password: "the vault's"}); !errors.Is(err, ErrAuth) {
		t.Errorf("the source vault's password against the adopted export: %v", err)
	}
	if ua, err := e2.Unlock(HardwareCredential{Token: adopted, Password: "chosen at adoption"}); err != nil {
		t.Errorf("the adopted key and its own password: %v", err)
	} else {
		ua.Close()
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

// TestRecoveryKeyEscrow: the recovery key is kept once more under the VMK
// (R38), now as a kind-1 record of the secrets section. An Unlocked can show
// it again; the record lives and dies with its slot, survives a rotation and
// travels in an export — and that rule is kind 1's alone, so no slot-region
// write ever drops K_P or a retired VMK (§7.6, §18.2).
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
		t.Fatalf("recovery key from the secrets section: %x %v", got, err)
	}
	if _, err := u.RecoveryKey(pwID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a password slot has no recovery key: %v", err)
	}
	if _, err := u.RecoveryKey([16]byte{9}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown slot: %v", err)
	}
	if n := secretsOf(u.Registry(), format.SecretRecoveryEscrow); n != 1 {
		t.Fatalf("kind-1 records after create: %d", n)
	}

	// A second recovery slot gets its own record; a rotation re-encrypts both.
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
	if err := u.Rotate(); err != nil {
		t.Fatal(err)
	}
	if got, err := u.RecoveryKey(rid); err != nil || got != rk {
		t.Fatalf("first key after a rotation: %x %v", got, err)
	}
	if got, err := u.RecoveryKey(rid2); err != nil || got != rk2 {
		t.Fatalf("second key after a rotation: %x %v", got, err)
	}

	// Removing the slot removes its record; a slot-region write drops an
	// orphan kind-1 record left by a registry-only write — and keeps every
	// kind-3 record, which belongs to the vault and not to a slot.
	if err := u.RemoveSlot(rid2); err != nil {
		t.Fatal(err)
	}
	if _, err := u.RecoveryKey(rid2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("removed slot: %v", err)
	}
	if n := secretsOf(u.Registry(), format.SecretRecoveryEscrow); n != 1 {
		t.Fatalf("kind-1 records after remove: %d", n)
	}
	orphan, err := secretRecord(u.vmk, u.k.sb.VaultID, format.SecretRecoveryEscrow, [16]byte{7}, rk.Padded())
	if err != nil {
		t.Fatal(err)
	}
	if err := u.UpdateRegistry(func(g *format.Registry) error {
		g.SetSecret(orphan)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	historyBefore := historyGenerations(u.Registry())
	if len(historyBefore) != 1 || historyBefore[0] != 1 {
		t.Fatalf("history after one rotation: %v", historyBefore)
	}
	if err := u.AddSlot(RecoverySlot{Key: recoveryKey(t), Label: "third"}); err != nil {
		t.Fatal(err)
	}
	if u.Registry().Secret(format.SecretRecoveryEscrow, [16]byte{7}) != nil {
		t.Fatal("an orphan kind-1 record survived a slot-region write")
	}
	if n := secretsOf(u.Registry(), format.SecretRecoveryEscrow); n != 2 {
		t.Fatalf("kind-1 records after the orphan was dropped: %d", n)
	}
	if got := historyGenerations(u.Registry()); len(got) != 1 || got[0] != 1 {
		t.Fatalf("a slot-region write touched the VMK history: %v", got)
	}

	// A slot whose record is missing cannot be shown again: registry version 3
	// gives every recovery slot its record at the commit that creates it, so
	// this is bookkeeping, not a slot made before escrow (A.14).
	if err := u.UpdateRegistry(func(g *format.Registry) error {
		g.DeleteSecret(format.SecretRecoveryEscrow, rid)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := u.RecoveryKey(rid); !errors.Is(err, ErrEscrowMissing) {
		t.Fatalf("slot without a kept key: %v", err)
	}
	// The unlock is never refused over it.
	if err := u.UpdateRegistry(func(g *format.Registry) error {
		rec, err := secretRecord(u.vmk, u.k.sb.VaultID, format.SecretRecoveryEscrow, rid, rk.Padded())
		if err != nil {
			return err
		}
		g.SetSecret(rec)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// A record moved to another slot fails its AAD: the id is in it (R22).
	var rid3 [16]byte
	for _, s := range u.k.Slots() {
		if s.Type == format.SlotRecovery && s.RecipientID != rid {
			rid3 = s.RecipientID
		}
	}
	if err := u.UpdateRegistry(func(g *format.Registry) error {
		moved := *g.Secret(format.SecretRecoveryEscrow, rid)
		moved.ID = rid3
		g.SetSecret(moved)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := u.RecoveryKey(rid3); !errors.Is(err, format.ErrInvalid) {
		t.Fatalf("a record moved to another slot: %v", err)
	}
	// Such a record fails a rotation too (fail closed), so it goes first.
	if err := u.Rotate(); !errors.Is(err, format.ErrInvalid) {
		t.Fatalf("a rotation over a record that does not unwrap: %v", err)
	}
	if err := u.UpdateRegistry(func(g *format.Registry) error {
		rec, err := secretRecord(u.vmk, u.k.sb.VaultID, format.SecretRecoveryEscrow, rid3, recoveryKey(t).Padded())
		if err != nil {
			return err
		}
		g.SetSecret(rec)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// A record that opens but holds another key is never shown (§6.3).
	if _, err := u.RecoveryKey(rid3); !errors.Is(err, ErrEscrowMismatch) {
		t.Fatalf("a record holding another key: %v", err)
	}
	// A plaintext whose sixteen-byte pad is not zero is registry corruption,
	// not a wrong key (§7.6).
	var dirty [32]byte
	copy(dirty[:], rk[:])
	dirty[31] = 1
	if err := u.UpdateRegistry(func(g *format.Registry) error {
		rec, err := secretRecord(u.vmk, u.k.sb.VaultID, format.SecretRecoveryEscrow, rid, dirty)
		if err != nil {
			return err
		}
		g.SetSecret(rec)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := u.RecoveryKey(rid); !errors.Is(err, format.ErrInvalid) {
		t.Fatalf("a plaintext that is not zero-padded: %v", err)
	}
	// A record that does not open under KWK_secrets is corruption too.
	if err := u.UpdateRegistry(func(g *format.Registry) error {
		rec := *g.Secret(format.SecretRecoveryEscrow, rid)
		rec.Ciphertext[0] ^= 1
		g.SetSecret(rec)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := u.RecoveryKey(rid); !errors.Is(err, format.ErrInvalid) {
		t.Fatalf("damaged record: %v", err)
	}
	if err := u.UpdateRegistry(func(g *format.Registry) error {
		rec, err := secretRecord(u.vmk, u.k.sb.VaultID, format.SecretRecoveryEscrow, rid, rk.Padded())
		if err != nil {
			return err
		}
		g.SetSecret(rec)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := u.RemoveSlot(rid3); err != nil {
		t.Fatal(err)
	}

	// The export carries the record: opened with the recovery key, it shows
	// that key again.
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
	if n := secretsOf(ue.Registry(), format.SecretRecoveryEscrow); n != 1 {
		t.Fatalf("the export carries records of slots it does not: %d", n)
	}
	// After Close the VMK is gone, and so is the way to the record.
	ue.Close()
	if _, err := ue.RecoveryKey(rid); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed: %v", err)
	}
}

// TestRotationSecrets: §8 step 3. Every kept record is re-encrypted under the
// new KWK_secrets, K_P survives unchanged, and the retiring VMK is appended as
// a vmk_history record keyed by the generation it held.
func TestRotationSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	tok := newToken(t)
	rk := recoveryKey(t)
	const pass = "the vault's"
	u := mustCreateWith(t, path, CreateOptions{
		Entangle: &Entangle{Password: pass, Argon2: fast},
		Slots:    []SlotSpec{HardwareSlot{PublicKey: tok.PublicKey()}, RecoverySlot{Key: rk, Label: "paper"}},
	})
	vaultID := u.k.sb.VaultID
	if got := historyGenerations(u.Registry()); len(got) != 0 {
		t.Fatalf("a fresh vault keeps a VMK history: %v", got)
	}
	if secretsOf(u.Registry(), format.SecretEntangledKey) != 1 {
		t.Fatal("a fresh entangled vault keeps no K_P")
	}
	kp1 := *u.kp
	vmk1 := u.vmk

	if err := u.Rotate(); err != nil {
		t.Fatal(err)
	}
	if u.gen != 2 || u.k.Generation() != 2 {
		t.Fatalf("generation %d", u.gen)
	}
	// One history record, for the generation that retired — the value before
	// the increment — and none for the current one.
	if got := historyGenerations(u.Registry()); len(got) != 1 || got[0] != 1 {
		t.Fatalf("history after one rotation: %v", got)
	}
	// It holds the VMK that was retired, under the new KWK_secrets.
	kwks := kdf.KWKSecrets(u.vmk, vaultID)
	rec := u.Registry().Secret(format.SecretVMKHistory, format.VMKHistoryID(1))
	if rec == nil {
		t.Fatal("no vmk_history record for generation 1")
	}
	if got, err := openSecretWith(kwks, vaultID, rec); err != nil || got != vmk1 {
		t.Fatalf("the history record does not hold the retired VMK: %v", err)
	}
	// K_P is re-wrapped, never replaced (§18.1), so the password still opens
	// the token slot and the value is unchanged.
	kpRec := u.Registry().Secret(format.SecretEntangledKey, [16]byte{})
	if kpRec == nil {
		t.Fatal("the rotation dropped K_P")
	}
	if got, err := openSecretWith(kwks, vaultID, kpRec); err != nil || got != kp1 {
		t.Fatalf("K_P changed across the rotation: %v", err)
	}
	kdf.Zero(kwks)
	// And the kind-1 record still opens.
	if got, err := u.RecoveryKey(u.k.Slots()[1].RecipientID); err != nil || got != rk {
		t.Fatalf("recovery key after the rotation: %v", err)
	}

	// A second rotation appends generation 2 and keeps generation 1.
	if err := u.Rotate(); err != nil {
		t.Fatal(err)
	}
	if got := historyGenerations(u.Registry()); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("history after two rotations: %v", got)
	}
	u.Close()
	u.k.Close()

	// Reopened, every slot opens at the new generation and the section is
	// exactly what the writer left.
	k := mustOpen(t, path)
	defer k.Close()
	u2, err := k.Unlock(HardwareCredential{Token: tok, Password: pass})
	if err != nil {
		t.Fatalf("token after two rotations: %v", err)
	}
	defer u2.Close()
	if u2.gen != 3 {
		t.Errorf("generation %d", u2.gen)
	}
	if got := historyGenerations(u2.Registry()); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("history read back: %v", got)
	}
	if secretsOf(u2.Registry(), format.SecretEntangledKey) != 1 || secretsOf(u2.Registry(), format.SecretRecoveryEscrow) != 1 {
		t.Fatalf("secrets read back: %+v", u2.Registry().Secrets)
	}
}

// TestRotationFailsClosed: §8 step 3 and §1. A secrets record that does not
// unwrap, and a history record that would collide with the retiring
// generation, both abandon the rotation before the flip.
func TestRotationFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	rk := recoveryKey(t)
	u := mustCreate(t, path, PasswordSlot{Password: "p", Argon2: fast}, RecoverySlot{Key: rk})
	vaultID := u.k.sb.VaultID

	// A damaged kind-1 record.
	if err := u.UpdateRegistry(func(g *format.Registry) error {
		rec := *g.Secret(format.SecretRecoveryEscrow, u.k.Slots()[1].RecipientID)
		rec.Ciphertext[0] ^= 1
		g.SetSecret(rec)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	seq, gen := u.k.sb.Seq, u.k.Generation()
	if err := u.Rotate(); !errors.Is(err, format.ErrInvalid) {
		t.Fatalf("rotation over a record that does not unwrap: %v", err)
	}
	if u.k.sb.Seq != seq || u.k.Generation() != gen || u.gen != gen {
		t.Fatalf("a failed rotation committed: seq %d gen %d", u.k.sb.Seq, u.k.Generation())
	}
	if err := u.UpdateRegistry(func(g *format.Registry) error {
		rec, err := secretRecord(u.vmk, vaultID, format.SecretRecoveryEscrow, u.k.Slots()[1].RecipientID, rk.Padded())
		if err != nil {
			return err
		}
		g.SetSecret(rec)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// A history record already claiming the retiring generation. Two records
	// for one generation are invalid (§18.2), so the rotation is abandoned.
	if err := u.UpdateRegistry(func(g *format.Registry) error {
		var junk [32]byte
		rec, err := secretRecord(u.vmk, vaultID, format.SecretVMKHistory, format.VMKHistoryID(u.gen), junk)
		if err != nil {
			return err
		}
		g.SetSecret(rec)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	seq = u.k.sb.Seq
	if err := u.Rotate(); !errors.Is(err, format.ErrInvalid) {
		t.Fatalf("rotation over a colliding history record: %v", err)
	}
	if u.k.sb.Seq != seq || u.k.Generation() != gen {
		t.Fatalf("a failed rotation committed: seq %d gen %d", u.k.sb.Seq, u.k.Generation())
	}
	// The section is still what the failed attempt read: nothing new, and the
	// planted record untouched.
	if got := historyGenerations(u.Registry()); len(got) != 1 || got[0] != gen {
		t.Fatalf("the abandoned rotation changed the section: %v", got)
	}
	// While it sits there the unlock refuses it in its own right: no
	// vmk_history record may carry the vault's current generation (§18.2).
	if _, err := u.k.Unlock(PasswordCredential{Password: "p"}); !errors.Is(err, format.ErrInvalid) {
		t.Errorf("a history record at the current generation: %v", err)
	}
	if err := u.UpdateRegistry(func(g *format.Registry) error {
		g.DeleteSecret(format.SecretVMKHistory, format.VMKHistoryID(gen))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// With it gone the vault opens at its old generation, both ways in.
	for _, c := range []Credential{PasswordCredential{Password: "p"}, RecoveryCredential{Key: rk}} {
		uu, err := u.k.Unlock(c)
		if err != nil {
			t.Fatalf("%T after a failed rotation: %v", c, err)
		}
		if uu.gen != gen {
			t.Errorf("%T: generation %d", c, uu.gen)
		}
		uu.Close()
	}
	// And a rotation now succeeds, which proves the two refusals were about
	// the records and not about the vault.
	if err := u.Rotate(); err != nil {
		t.Fatalf("rotation once the section is sound: %v", err)
	}
	if got := historyGenerations(u.Registry()); len(got) != 1 || got[0] != gen {
		t.Fatalf("history after the successful rotation: %v", got)
	}
}

// TestRemovalThenRotation is the direct regression for the old pruneEscrows,
// which keyed on recipient_id across the whole section: under the secrets
// layout that would delete K_P and the entire VMK history at the very next
// slot-region write (R38, "that rule is kind 1's alone").
func TestRemovalThenRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	tok := newToken(t)
	rk, rk2 := recoveryKey(t), recoveryKey(t)
	const pass = "the vault's"
	u := mustCreateWith(t, path, CreateOptions{
		Entangle: &Entangle{Password: pass, Argon2: fast},
		Slots: []SlotSpec{
			HardwareSlot{PublicKey: tok.PublicKey(), Label: "A"},
			RecoverySlot{Key: rk, Label: "paper"},
			RecoverySlot{Key: rk2, Label: "spare"},
		},
	})
	kp := *u.kp
	// One rotation first, so there is a history record to lose.
	if err := u.Rotate(); err != nil {
		t.Fatal(err)
	}
	var spare [16]byte
	for _, s := range u.k.Slots() {
		if s.Label == "spare" {
			spare = s.RecipientID
		}
	}
	if err := u.RemoveSlot(spare); err != nil {
		t.Fatal(err)
	}
	// The removal took the spare's record and nothing else.
	if n := secretsOf(u.Registry(), format.SecretRecoveryEscrow); n != 1 {
		t.Fatalf("kind-1 records after the removal: %d", n)
	}
	if secretsOf(u.Registry(), format.SecretEntangledKey) != 1 {
		t.Fatal("the removal dropped K_P")
	}
	if got := historyGenerations(u.Registry()); len(got) != 1 || got[0] != 1 {
		t.Fatalf("the removal touched the VMK history: %v", got)
	}
	// And the rotation that follows carries the removed slot's record nowhere.
	if err := u.Rotate(); err != nil {
		t.Fatalf("rotation after a removal: %v", err)
	}
	if u.Registry().Secret(format.SecretRecoveryEscrow, spare) != nil {
		t.Fatal("the removed slot's record was carried forward")
	}
	if got := historyGenerations(u.Registry()); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("history after removal then rotation: %v", got)
	}
	vaultID := u.k.sb.VaultID
	kwks := kdf.KWKSecrets(u.vmk, vaultID)
	defer kdf.Zero(kwks)
	if got, err := openSecretWith(kwks, vaultID, u.Registry().Secret(format.SecretEntangledKey, [16]byte{})); err != nil || got != kp {
		t.Fatalf("K_P after removal then rotation: %v", err)
	}
	u.Close()
	u.k.Close()
	// The proof that matters: the token and the vault password still open it.
	k := mustOpen(t, path)
	defer k.Close()
	uu, err := k.Unlock(HardwareCredential{Token: tok, Password: pass})
	if err != nil {
		t.Fatalf("token after removal then rotation: %v", err)
	}
	uu.Close()
	// The removed key fits the remaining recovery slot's shape but is not its
	// key, so it fails the §6.3 verifier rather than finding no slot.
	if _, err := k.Unlock(RecoveryCredential{Key: rk2}); !errors.Is(err, ErrVerifier) {
		t.Errorf("the removed slot's key still opens the vault: %v", err)
	}
}

// TestGenerationMismatchIsTampering: since Revision 2 a recovered generation
// that is not the superblock's is a verdict in both directions (§6.2, §18.1).
// ErrStale keeps its own, narrower meaning: a handle the file has moved past.
func TestGenerationMismatchIsTampering(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	rk := recoveryKey(t)
	u := mustCreate(t, path, PasswordSlot{Password: "p", Argon2: fast}, RecoverySlot{Key: rk})
	if err := u.Rotate(); err != nil { // generation 2, so both directions exist
		t.Fatal(err)
	}
	u.Close()
	u.k.Close()
	good := snapshot(t, path)

	// The superblock's vmk_generation is plaintext and checksummed, not
	// authenticated, so an attacker can edit it and recompute the checksum.
	for _, gen := range []uint64{1, 3} {
		data := bytes.Clone(good)
		for _, c := range []format.Copy{format.CopyA, format.CopyB} {
			off := c.KeystoreSuperblockOff()
			sb, err := format.DecodeKeystoreSuperblock(data[off : off+format.SuperblockSize])
			if err != nil {
				continue
			}
			sb.VMKGeneration = gen
			enc, err := sb.Encode()
			if err != nil {
				t.Fatal(err)
			}
			copy(data[off:], enc)
		}
		restore(t, path, data)
		k := mustOpen(t, path)
		if _, err := k.Unlock(PasswordCredential{Password: "p"}); !errors.Is(err, ErrTampered) {
			t.Errorf("superblock generation %d against slots holding 2: %v, want ErrTampered", gen, err)
		}
		if errors.Is(k.Stale, ErrStale) {
			t.Error("a generation mismatch reported as staleness")
		}
		k.Close()
	}
	restore(t, path, good)

	// ErrStale is what a handle answers once its own vault has rotated past
	// it — a statement about the handle, never about a credential.
	k := mustOpen(t, path)
	defer k.Close()
	u2, err := k.Unlock(PasswordCredential{Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	defer u2.Close()
	other, err := k.Unlock(RecoveryCredential{Key: rk})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	s, err := other.Session()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Lock()
	if err := u2.Rotate(); err != nil {
		t.Fatal(err)
	}
	if err := other.AddSlot(RecoverySlot{Key: recoveryKey(t)}); !errors.Is(err, ErrStale) {
		t.Errorf("a second Unlocked after a rotation: %v", err)
	}
	if err := s.UpdateRegistry(func(*format.Registry) error { return nil }); !errors.Is(err, ErrStale) {
		t.Errorf("a Session after a rotation: %v", err)
	}
	// And the credential itself is not behind: it opens at the new generation.
	u3, err := k.Unlock(RecoveryCredential{Key: rk})
	if err != nil {
		t.Fatalf("the recovery key after the rotation: %v", err)
	}
	if u3.gen != k.Generation() {
		t.Errorf("re-unlocked at generation %d, vault at %d", u3.gen, k.Generation())
	}
	u3.Close()
}

// TestHeaderIsAuthenticatedByTheRegion: no slot record's AAD covers the slot
// region header, so the two things that do are the derivation and R25's hash
// (§6). Both are asserted here.
func TestHeaderIsAuthenticatedByTheRegion(t *testing.T) {
	dir := t.TempDir()
	rk := recoveryKey(t)
	const pass = "the vault's"

	// Each case edits one header byte on disk, keeping the region's length and
	// every record intact, and re-opens.
	cases := []struct {
		name string
		edit func(hdr []byte)
	}{
		{"argon2_m downgraded", func(hdr []byte) { hdr[8] = 128 }},
		{"argon2_t raised", func(hdr []byte) { hdr[12] = 2 }},
		{"entangle_salt swapped", func(hdr []byte) { hdr[16] ^= 0xFF }},
	}
	for _, c := range cases {
		path := filepath.Join(dir, c.name+".eks")
		tok := newToken(t)
		u := mustCreateWith(t, path, CreateOptions{
			Entangle: &Entangle{Password: pass, Argon2: kdf.Argon2Params{MemKiB: 64, Time: 1, Threads: 1}},
			Slots:    []SlotSpec{HardwareSlot{PublicKey: tok.PublicKey()}, RecoverySlot{Key: rk}},
		})
		u.Close()
		u.k.Close()
		k := mustOpen(t, path)
		off := k.sb.SlotRegionOff
		k.Close()
		data := snapshot(t, path)
		c.edit(data[off : off+format.MinSlotRegionLen])
		restore(t, path, data)

		k = mustOpen(t, path)
		// A way in that does not read the header opens, and R25 names the edit
		// as tampering — which is what covers a header no credential reads.
		ur, err := k.Unlock(RecoveryCredential{Key: rk})
		if err != nil {
			t.Fatalf("%s: recovery unlock: %v", c.name, err)
		}
		if !errors.Is(ur.Tampered(), ErrTampered) {
			t.Errorf("%s: Tampered %v", c.name, ur.Tampered())
		}
		if err := ur.Rotate(); !errors.Is(err, ErrTampered) {
			t.Errorf("%s: rotate over an edited header: %v", c.name, err)
		}
		ur.Close()
		// And the derivation binds it: all five fields feed K_P, and K_P feeds
		// every entangled hardware slot's IK, so an edited header yields a key
		// that opens nothing rather than a weaker one. §6 names what that looks
		// like at the moment it happens — "this credential did not open the
		// vault", indistinguishable from a wrong password — and the verdict of
		// tampering comes from the way in that does not read the header, above.
		if _, err := k.Unlock(HardwareCredential{Token: tok, Password: pass}); !errors.Is(err, ErrAuth) {
			t.Errorf("%s: hardware unlock over an edited header: %v", c.name, err)
		}
		k.Close()
	}

	// The kept K_P is the authoritative one and the derived one is compared
	// with it: they can differ only if the header and the authenticated
	// registry disagree, which is again a statement about the header.
	swapped := filepath.Join(dir, "kp.eks")
	tokS := newToken(t)
	us := mustCreateWith(t, swapped, CreateOptions{
		Entangle: &Entangle{Password: pass, Argon2: fast},
		Slots:    []SlotSpec{HardwareSlot{PublicKey: tokS.PublicKey()}, RecoverySlot{Key: rk}},
	})
	if err := us.UpdateRegistry(func(g *format.Registry) error {
		var other [32]byte
		other[0] = 1
		rec, err := secretRecord(us.vmk, us.k.sb.VaultID, format.SecretEntangledKey, [16]byte{}, other)
		if err != nil {
			return err
		}
		g.SetSecret(rec)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := us.k.Unlock(HardwareCredential{Token: tokS, Password: pass}); !errors.Is(err, ErrTampered) {
		t.Errorf("a kept K_P that is not the header's: %v, want ErrTampered", err)
	}
	// A way in that derives no K_P is not refused over it: it reads the kept
	// copy and carries on.
	if ur, err := us.k.Unlock(RecoveryCredential{Key: rk}); err != nil {
		t.Errorf("recovery unlock over a swapped K_P: %v", err)
	} else {
		ur.Close()
	}
	us.Close()
	us.k.Close()

	// Flipping entangle itself makes the registry disagree with the header,
	// which §7.6 refuses outright: the kind-2 record is still there.
	path := filepath.Join(dir, "switch.eks")
	tok := newToken(t)
	u := mustCreateWith(t, path, CreateOptions{
		Entangle: &Entangle{Password: pass, Argon2: fast},
		Slots:    []SlotSpec{HardwareSlot{PublicKey: tok.PublicKey()}, RecoverySlot{Key: rk}},
	})
	u.Close()
	u.k.Close()
	k := mustOpen(t, path)
	off := k.sb.SlotRegionOff
	k.Close()
	data := snapshot(t, path)
	data[off+4] = 0 // entangle 1 → 0
	// entangle 0 with non-zero Argon2 fields is not a decodable header, so the
	// four fields go too: the edit is the whole switch-off a writer would make.
	data[off+5] = 0
	for i := 8; i < format.MinSlotRegionLen; i++ {
		data[off+uint64(i)] = 0
	}
	restore(t, path, data)
	k = mustOpen(t, path)
	defer k.Close()
	if k.Entangled() {
		t.Fatal("the edit did not land")
	}
	if _, err := k.Unlock(RecoveryCredential{Key: rk}); !errors.Is(err, ErrTampered) {
		t.Errorf("a header that disagrees with the registry: %v, want ErrTampered", err)
	}
}

// TestExportContents: R28's list, item by item.
func TestExportContents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "v.eks")
	tok := newToken(t)
	rk, rk2 := recoveryKey(t), recoveryKey(t)
	u := mustCreateWith(t, path, CreateOptions{
		Entangle: &Entangle{Password: "the vault's", Argon2: fast},
		Slots: []SlotSpec{
			HardwareSlot{PublicKey: tok.PublicKey(), Label: "A"},
			RecoverySlot{Key: rk, Label: "paper"},
			RecoverySlot{Key: rk2, Label: "spare"},
		},
	})
	// Two rotations, so there is a history to carry.
	if err := u.Rotate(); err != nil {
		t.Fatal(err)
	}
	if err := u.Rotate(); err != nil {
		t.Fatal(err)
	}
	// One recovery slot removed, so the export must carry one kind-1 record
	// and not two.
	var spare [16]byte
	for _, s := range u.k.Slots() {
		if s.Label == "spare" {
			spare = s.RecipientID
		}
	}
	if err := u.RemoveSlot(spare); err != nil {
		t.Fatal(err)
	}
	export := filepath.Join(dir, "backup.eks")
	if err := u.Export(export); err != nil {
		t.Fatal(err)
	}
	gen := u.gen
	u.Close()
	u.k.Close()

	e := mustOpen(t, export)
	defer e.Close()
	if e.Generation() != gen {
		t.Errorf("export generation %d, vault %d", e.Generation(), gen)
	}
	// The header: entangle 0, zero salt, zero parameters.
	if e.hdr != (format.SlotRegionHeader{}) {
		t.Errorf("export header: %+v", e.hdr)
	}
	if len(e.Slots()) != 1 || e.Slots()[0].Type != format.SlotRecovery {
		t.Fatalf("export slots: %+v", e.Slots())
	}
	ue, err := e.Unlock(RecoveryCredential{Key: rk})
	if err != nil {
		t.Fatal(err)
	}
	defer ue.Close()
	if ue.Tampered() != nil {
		t.Fatal(ue.Tampered())
	}
	if ue.kp != nil {
		t.Error("the export's Unlocked holds a K_P")
	}
	g := ue.Registry()
	// The registry agrees with the header it travelled with.
	if err := g.CheckEntangleAgreement(false); err != nil {
		t.Errorf("exported registry: %v", err)
	}
	if n := secretsOf(g, format.SecretEntangledKey); n != 0 {
		t.Errorf("the export carries K_P: %d records", n)
	}
	if n := secretsOf(g, format.SecretRecoveryEscrow); n != 1 {
		t.Errorf("kind-1 records in the export: %d", n)
	}
	if g.Secret(format.SecretRecoveryEscrow, spare) != nil {
		t.Error("the export carries the removed slot's record")
	}
	if got := historyGenerations(g); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("the export's VMK history: %v", got)
	}
	// The records travelled verbatim: the same generation, so the same
	// KWK_secrets opens them and the reveal works.
	if got, err := ue.RecoveryKey(e.Slots()[0].RecipientID); err != nil || got != rk {
		t.Fatalf("reveal from the export: %x %v", got, err)
	}
	if _, err := e.Unlock(RecoveryCredential{Key: rk2}); !errors.Is(err, ErrVerifier) {
		t.Errorf("the removed slot's key against the export: %v", err)
	}
}

// slotRecord is the live record with this recipient ID.
func slotRecord(t testing.TB, k *Keystore, rid [16]byte) format.SlotRecord {
	t.Helper()
	for i := range k.slots {
		if k.slots[i].RecipientID == rid {
			return k.slots[i]
		}
	}
	t.Fatalf("no slot %x", rid)
	return format.SlotRecord{}
}

// encRecord is a slot record on the wire, for a byte-exact comparison of a
// record a mutation must not have touched.
func encRecord(t testing.TB, s format.SlotRecord) []byte {
	t.Helper()
	b, err := s.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// countForeign counts OpenForeign's candidate attempts, so a test can hold the
// package to "each candidate at most once" (§18.2).
func countForeign(t testing.TB) *int {
	t.Helper()
	n := 0
	saved := tryForeign
	tryForeign = func(vmk [32]byte, other *Keystore) (*format.Registry, error) {
		n++
		return saved(vmk, other)
	}
	t.Cleanup(func() { tryForeign = saved })
	return &n
}

// TestEntangleSwitch: turning the vault's password on, changing it and turning
// it off are one commit each and all three are offline (§18.1). Every one of
// them here runs from a recovery-key unlock with no token present, which is the
// point: K_P comes from the VMK, not from a password typed or a key touched.
func TestEntangleSwitch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "v.eks")
	rk := recoveryKey(t)
	tok := newToken(t)
	u := mustCreate(t, path, HardwareSlot{PublicKey: tok.PublicKey(), Label: "key"}, RecoverySlot{Key: rk, Label: "paper"})
	var hwID, recID [16]byte
	for _, s := range u.k.Slots() {
		switch s.Type {
		case format.SlotExternalECDH:
			hwID = s.RecipientID
		case format.SlotRecovery:
			recID = s.RecipientID
		}
	}
	u.Close()
	u.k.Close()

	k := mustOpen(t, path)
	defer k.Close()
	ur, err := k.Unlock(RecoveryCredential{Key: rk})
	if err != nil {
		t.Fatal(err)
	}
	defer ur.Close()
	if tok.calls != 0 {
		t.Fatalf("a recovery unlock touched the token %d times", tok.calls)
	}
	hwBefore := slotRecord(t, k, hwID)
	recWire := encRecord(t, slotRecord(t, k, recID))

	// On: a fresh salt, the caller's parameters, the hardware slot re-wrapped,
	// the recovery slot untouched, one entangled_key record.
	if err := ur.SetEntangled(true, &Entangle{Password: "first", Argon2: fast}); err != nil {
		t.Fatal(err)
	}
	salt1 := k.hdr.EntangleSalt
	if !k.Entangled() || salt1 == ([16]byte{}) {
		t.Fatalf("header after the switch: %+v", k.hdr)
	}
	if k.hdr.Argon2M != fast.MemKiB || k.hdr.Argon2T != fast.Time || k.hdr.Argon2P != fast.Threads {
		t.Errorf("header parameters: %+v", k.hdr)
	}
	hwOn := slotRecord(t, k, hwID)
	if bytes.Equal(hwOn.EPK, hwBefore.EPK) || hwOn.WrappedVMK == hwBefore.WrappedVMK {
		t.Error("the hardware slot was not re-wrapped by the switch")
	}
	if hwOn.Salt != ([32]byte{}) || hwOn.Argon2M != 0 || hwOn.Argon2T != 0 || hwOn.Argon2P != 0 || hwOn.Flags != 0 {
		t.Errorf("the switch put Argon2 material on a hardware slot: %+v", hwOn)
	}
	if !bytes.Equal(recWire, encRecord(t, slotRecord(t, k, recID))) {
		t.Error("the switch touched the recovery slot")
	}
	if n := secretsOf(k.reg, format.SecretEntangledKey); n != 1 {
		t.Errorf("entangled_key records after the switch: %d", n)
	}
	if tok.calls != 0 {
		t.Errorf("the switch touched the token %d times", tok.calls)
	}
	kp1 := *k.reg.Secret(format.SecretEntangledKey, [16]byte{})
	if _, err := k.Unlock(HardwareCredential{Token: tok}); !errors.Is(err, ErrPasswordRequired) {
		t.Errorf("token alone after the switch: %v", err)
	}
	if uh, err := k.Unlock(HardwareCredential{Token: tok, Password: "first"}); err != nil {
		t.Errorf("token and the new password: %v", err)
	} else {
		uh.Close()
	}

	// A second handle, opened while the password is "first" and never told
	// about the change below. It stays current — a change of password moves no
	// generation — so its mutations are not refused, and they must therefore
	// use the K_P the live header derives, not the one it read at unlock.
	urOld, err := k.Unlock(RecoveryCredential{Key: rk})
	if err != nil {
		t.Fatal(err)
	}
	defer urOld.Close()

	// Change: a salt this vault never had, a replaced record, the old password
	// dead — and the old password never asked, since nothing takes it.
	callsBefore := tok.calls
	if err := ur.ChangeEntangledPassword(Entangle{Password: "second", Argon2: fast}); err != nil {
		t.Fatal(err)
	}
	if tok.calls != callsBefore {
		t.Errorf("the change touched the token %d times", tok.calls-callsBefore)
	}
	salt2 := k.hdr.EntangleSalt
	if salt2 == salt1 || salt2 == ([16]byte{}) {
		t.Errorf("the change did not redraw entangle_salt: %x then %x", salt1, salt2)
	}
	if kp2 := *k.reg.Secret(format.SecretEntangledKey, [16]byte{}); kp2 == kp1 {
		t.Error("the change did not replace the entangled_key record")
	}
	if n := secretsOf(k.reg, format.SecretEntangledKey); n != 1 {
		t.Errorf("entangled_key records after the change: %d", n)
	}
	if !bytes.Equal(recWire, encRecord(t, slotRecord(t, k, recID))) {
		t.Error("the change touched the recovery slot")
	}
	if _, err := k.Unlock(HardwareCredential{Token: tok, Password: "first"}); !errors.Is(err, ErrAuth) {
		t.Errorf("the old password after a change: %v", err)
	}
	if uh, err := k.Unlock(HardwareCredential{Token: tok, Password: "second"}); err != nil {
		t.Errorf("the new password: %v", err)
	} else {
		uh.Close()
	}

	// A key enrolled from the handle that predates the change is wrapped from
	// the K_P the live header derives: the kind-2 record is authoritative
	// (§18.1, §7.6). From the copy that handle cached, the slot would commit
	// with no error and then open for nobody.
	late := newToken(t)
	if err := urOld.AddSlot(HardwareSlot{PublicKey: late.PublicKey(), Label: "late"}); err != nil {
		t.Fatalf("enrol from a handle opened before the change: %v", err)
	}
	if uh, err := k.Unlock(HardwareCredential{Token: late, Password: "second"}); err != nil {
		t.Errorf("the key enrolled from that handle does not open with the vault's password: %v", err)
	} else {
		uh.Close()
	}

	// AddFirstWayIn is the first way in of an adopted backup, whose header
	// carries no entanglement (R28, §15). On a vault whose switch is on it is
	// refused, so that enrolling a slot can never turn the vault's password off
	// or redraw its salt as a side effect.
	saltNow, kpNow := k.hdr.EntangleSalt, *k.reg.Secret(format.SecretEntangledKey, [16]byte{})
	seqNow := k.sb.Seq
	if err := ur.AddFirstWayIn(HardwareSlot{PublicKey: newToken(t).PublicKey()}, nil); !errors.Is(err, ErrParams) {
		t.Errorf("AddFirstWayIn on an entangled vault: %v", err)
	}
	if err := ur.AddFirstWayIn(HardwareSlot{PublicKey: newToken(t).PublicKey()},
		&Entangle{Password: "third", Argon2: fast}); !errors.Is(err, ErrParams) {
		t.Errorf("AddFirstWayIn with a password on an entangled vault: %v", err)
	}
	if !k.Entangled() || k.hdr.EntangleSalt != saltNow || k.sb.Seq != seqNow {
		t.Errorf("a refused first way in changed the header: %+v", k.hdr)
	}
	if rec := *k.reg.Secret(format.SecretEntangledKey, [16]byte{}); rec != kpNow {
		t.Error("a refused first way in replaced the entangled_key record")
	}
	if uh, err := k.Unlock(HardwareCredential{Token: tok, Password: "second"}); err != nil {
		t.Errorf("the vault's password after a refused first way in: %v", err)
	} else {
		uh.Close()
	}

	// An export taken while the vault is entangled opens with its recovery key
	// and presents itself unentangled (R28).
	export := filepath.Join(dir, "backup.eks")
	if err := ur.Export(export); err != nil {
		t.Fatal(err)
	}
	e := mustOpen(t, export)
	if e.Entangled() {
		t.Error("the export of an entangled vault says entangle 1")
	}
	uex, err := e.Unlock(RecoveryCredential{Key: rk})
	if err != nil {
		t.Fatalf("the export of an entangled vault does not open with its recovery key: %v", err)
	}
	if err := uex.Registry().CheckEntangleAgreement(false); err != nil {
		t.Errorf("exported registry: %v", err)
	}
	uex.Close()
	e.Close()

	// Off: the header's five fields zeroed, the record dropped, the hardware
	// slot back to pre = H — and a password handed in anyway is ignored, not
	// refused.
	hwEntangled := slotRecord(t, k, hwID)
	callsBefore = tok.calls
	if err := ur.SetEntangled(false, nil); err != nil {
		t.Fatal(err)
	}
	if k.Entangled() || k.hdr != (format.SlotRegionHeader{}) {
		t.Fatalf("header after turning it off: %+v", k.hdr)
	}
	if n := secretsOf(k.reg, format.SecretEntangledKey); n != 0 {
		t.Errorf("entangled_key records after turning it off: %d", n)
	}
	hwOff := slotRecord(t, k, hwID)
	if bytes.Equal(hwOff.EPK, hwEntangled.EPK) || hwOff.WrappedVMK == hwEntangled.WrappedVMK {
		t.Error("the hardware slot was not re-wrapped by turning the switch off")
	}
	if !bytes.Equal(recWire, encRecord(t, slotRecord(t, k, recID))) {
		t.Error("turning it off touched the recovery slot")
	}
	if tok.calls != callsBefore {
		t.Errorf("turning it off touched the token %d times", tok.calls-callsBefore)
	}
	if uh, err := k.Unlock(HardwareCredential{Token: tok}); err != nil {
		t.Errorf("token alone after turning it off: %v", err)
	} else {
		uh.Close()
	}
	if uh, err := k.Unlock(HardwareCredential{Token: tok, Password: "second"}); err != nil {
		t.Errorf("a password handed in while the switch is off should be ignored: %v", err)
	} else {
		uh.Close()
	}
	// The mirror case: the handle that predates the switch-off still holds a
	// K_P, and the live header says entangle 0. The slot it enrols is wrapped
	// with pre = H, so the token alone opens it — wrapped entangled under a
	// header that says otherwise, no credential ever would.
	afterOff := newToken(t)
	if err := urOld.AddSlot(HardwareSlot{PublicKey: afterOff.PublicKey(), Label: "after off"}); err != nil {
		t.Fatalf("enrol from a handle opened before the switch-off: %v", err)
	}
	if uh, err := k.Unlock(HardwareCredential{Token: afterOff}); err != nil {
		t.Errorf("the key enrolled from that handle does not open with the token alone: %v", err)
	} else {
		uh.Close()
	}
	// The change button cannot redraw a salt on a vault whose switch is off.
	if err := ur.ChangeEntangledPassword(Entangle{Password: "third", Argon2: fast}); !errors.Is(err, ErrParams) {
		t.Errorf("change with the switch off: %v", err)
	}
	if k.hdr != (format.SlotRegionHeader{}) {
		t.Errorf("the refused change wrote a header: %+v", k.hdr)
	}
	// Everything above survives a reopen.
	ur.Close()
	k.Close()
	k2 := mustOpen(t, path)
	defer k2.Close()
	if k2.Entangled() {
		t.Error("the switch came back after a reopen")
	}
	if u2, err := k2.Unlock(HardwareCredential{Token: tok}); err != nil {
		t.Errorf("reopened: %v", err)
	} else {
		u2.Close()
	}
}

// TestRotateFromAHandleOpenedBeforeTheSwitch: a rotation is possible from the
// VMK alone, whatever the handle held when it opened (§8, "no rotation is
// deferred"). A handle opened while the vault's switch was off rotates a vault
// another handle has since switched on, because step 3 recovers K_P from the
// secrets section — the handle's own copy decides nothing.
func TestRotateFromAHandleOpenedBeforeTheSwitch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	rk := recoveryKey(t)
	tok := newToken(t)
	u := mustCreate(t, path, HardwareSlot{PublicKey: tok.PublicKey()}, RecoverySlot{Key: rk})
	u.Close()
	u.k.Close()

	k := mustOpen(t, path)
	defer k.Close()
	// Opened while the switch is off, so this handle holds no K_P at all.
	early, err := k.Unlock(RecoveryCredential{Key: rk})
	if err != nil {
		t.Fatal(err)
	}
	defer early.Close()
	if early.kp != nil {
		t.Fatal("a handle opened while the switch is off holds a K_P")
	}
	on, err := k.Unlock(RecoveryCredential{Key: rk})
	if err != nil {
		t.Fatal(err)
	}
	if err := on.SetEntangled(true, &Entangle{Password: "vault", Argon2: fast}); err != nil {
		t.Fatal(err)
	}
	on.Close()

	gen := k.sb.VMKGeneration
	if err := early.Rotate(); err != nil {
		t.Fatalf("rotate from a handle opened before the switch: %v", err)
	}
	if k.sb.VMKGeneration != gen+1 {
		t.Fatalf("generation after the rotation: %d, was %d", k.sb.VMKGeneration, gen)
	}
	// Every way in still opens, so step 4 wrapped the hardware slot under the
	// K_P the header derives and not under nothing.
	if _, err := k.Unlock(HardwareCredential{Token: tok}); !errors.Is(err, ErrPasswordRequired) {
		t.Errorf("token alone after the rotation: %v", err)
	}
	if uh, err := k.Unlock(HardwareCredential{Token: tok, Password: "vault"}); err != nil {
		t.Errorf("the token and the vault's password after the rotation: %v", err)
	} else {
		uh.Close()
	}
	if ur, err := k.Unlock(RecoveryCredential{Key: rk}); err != nil {
		t.Errorf("the recovery key after the rotation: %v", err)
	} else {
		ur.Close()
	}
}

// TestEntangleInvariant: §6.4 is evaluated over the sets the header the commit
// would write makes, in both directions and before the password is used.
func TestEntangleInvariant(t *testing.T) {
	dir := t.TempDir()
	rk := recoveryKey(t)
	tokA, tokB := newToken(t), newToken(t)
	pass := &Entangle{Password: "vault", Argon2: fast}

	// Two tokens and nothing else, entangled: both need the one secret "pwd",
	// so there is only one way in.
	if _, err := Create(filepath.Join(dir, "two.eks"), CreateOptions{
		Entangle: pass,
		Slots:    []SlotSpec{HardwareSlot{PublicKey: tokA.PublicKey()}, HardwareSlot{PublicKey: tokB.PublicKey()}},
	}); !errors.Is(err, ErrInvariant) {
		t.Errorf("create entangled with two tokens: %v", err)
	}
	// One token plus a recovery slot is two independent ways in.
	uone := mustCreateWith(t, filepath.Join(dir, "one.eks"), CreateOptions{
		Entangle: pass,
		Slots:    []SlotSpec{HardwareSlot{PublicKey: tokA.PublicKey()}, RecoverySlot{Key: rk, Label: "paper"}},
	})
	if !uone.k.Entangled() {
		t.Error("one token plus a recovery slot, entangled")
	}
	// Two tokens plus a recovery slot: while the switch is on the recovery slot
	// is the only thing keeping the sets disjoint, so it is not removable;
	// CanEnableEntangled says the same from the other side.
	uok := mustCreateWith(t, filepath.Join(dir, "ok.eks"), CreateOptions{
		Entangle: pass,
		Slots: []SlotSpec{
			HardwareSlot{PublicKey: tokA.PublicKey()},
			HardwareSlot{PublicKey: tokB.PublicKey()},
			RecoverySlot{Key: rk, Label: "paper"},
		},
	})
	var recID [16]byte
	for _, s := range uok.k.Slots() {
		if s.Type == format.SlotRecovery {
			recID = s.RecipientID
		}
	}
	if uok.k.Removable(recID) {
		t.Error("the last recovery slot is removable while the vault is entangled")
	}
	if err := uok.SetEntangled(false, nil); err != nil {
		t.Fatalf("turning it off is never refused: %v", err)
	}
	if !uok.k.Removable(recID) {
		t.Error("with the switch off the two tokens are two ways in, so the recovery slot is removable")
	}
	if err := uok.k.CanEnableEntangled(); err != nil {
		t.Errorf("CanEnableEntangled with a recovery slot: %v", err)
	}
	if err := uok.SetEntangled(true, pass); err != nil {
		t.Errorf("turning it back on: %v", err)
	}

	// A hardware-only vault cannot turn the switch on, and the refusal is
	// decided before the password is used: no Argon2id, no salt, no write.
	uh := mustCreate(t, filepath.Join(dir, "hw.eks"),
		HardwareSlot{PublicKey: tokA.PublicKey()}, HardwareSlot{PublicKey: tokB.PublicKey()})
	if err := uh.k.CanEnableEntangled(); !errors.Is(err, ErrInvariant) {
		t.Errorf("CanEnableEntangled on a hardware-only vault: %v", err)
	}
	n := countArgon2(t)
	seq := uh.k.sb.Seq
	if err := uh.SetEntangled(true, pass); !errors.Is(err, ErrInvariant) {
		t.Errorf("SetEntangled(true) on a hardware-only vault: %v", err)
	}
	if *n != 0 {
		t.Errorf("the refused switch derived K_P %d times", *n)
	}
	if uh.k.sb.Seq != seq || uh.k.Entangled() {
		t.Error("the refused switch committed something")
	}
	// Off is never refused, on a vault that has it off as much as on one that
	// has it on.
	if err := uh.SetEntangled(false, nil); err != nil {
		t.Errorf("SetEntangled(false) on a vault whose switch is off: %v", err)
	}
	if err := uok.SetEntangled(false, nil); err != nil {
		t.Errorf("SetEntangled(false) on an entangled vault: %v", err)
	}
	// And the parameters are held to R4 and R24 wherever they come in.
	if err := uok.SetEntangled(true, &Entangle{Password: "vault"}); !errors.Is(err, ErrParams) {
		t.Errorf("switch without Argon2 parameters: %v", err)
	}
	if err := uok.SetEntangled(true, &Entangle{Argon2: fast}); !errors.Is(err, ErrParams) {
		t.Errorf("switch with an empty password: %v", err)
	}
	if err := uok.SetEntangled(true, nil); !errors.Is(err, ErrParams) {
		t.Errorf("switch on with no password at all: %v", err)
	}
	if err := uok.SetEntangled(false, pass); !errors.Is(err, ErrParams) {
		t.Errorf("switch off with a password: %v", err)
	}
}

// TestEntangleRefusedOnATamperedRegion: R25's freeze covers the entangle
// mutations. A vault whose slot region does not match the hash its registry
// authenticates opens and reads, and changes nothing about its password until
// the region is repaired.
func TestEntangleRefusedOnATamperedRegion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "v.eks")
	rk := recoveryKey(t)
	tok := newToken(t)
	u := mustCreateWith(t, path, CreateOptions{
		Entangle: &Entangle{Password: "vault", Argon2: fast},
		Slots:    []SlotSpec{HardwareSlot{PublicKey: tok.PublicKey()}, RecoverySlot{Key: rk, Label: "paper"}},
	})
	u.Close()
	u.k.Close()

	// argon2_t sits at offset 12 of the 32-byte header; 2 is as legal as 1.
	k := mustOpen(t, path)
	off := k.sb.SlotRegionOff
	k.Close()
	b := snapshot(t, path)
	b[off+12] = 2
	restore(t, path, b)

	k = mustOpen(t, path)
	defer k.Close()
	if k.hdr.Argon2T != 2 {
		t.Fatalf("the edit did not land: argon2_t %d", k.hdr.Argon2T)
	}
	ut, err := k.Unlock(RecoveryCredential{Key: rk})
	if err != nil {
		t.Fatal(err)
	}
	defer ut.Close()
	if !errors.Is(ut.Tampered(), ErrTampered) {
		t.Fatalf("Tampered: %v", ut.Tampered())
	}
	seq := k.sb.Seq
	if err := ut.SetEntangled(false, nil); !errors.Is(err, ErrTampered) {
		t.Errorf("SetEntangled(false) while tampered: %v", err)
	}
	if err := ut.SetEntangled(true, &Entangle{Password: "new", Argon2: fast}); !errors.Is(err, ErrTampered) {
		t.Errorf("SetEntangled(true) while tampered: %v", err)
	}
	if err := ut.ChangeEntangledPassword(Entangle{Password: "new", Argon2: fast}); !errors.Is(err, ErrTampered) {
		t.Errorf("ChangeEntangledPassword while tampered: %v", err)
	}
	if err := ut.AddFirstWayIn(HardwareSlot{PublicKey: newToken(t).PublicKey()},
		&Entangle{Password: "new", Argon2: fast}); !errors.Is(err, ErrTampered) {
		t.Errorf("AddFirstWayIn while tampered: %v", err)
	}
	if k.sb.Seq != seq {
		t.Error("a refused mutation committed something")
	}
	if k.hdr.Argon2T != 2 {
		t.Errorf("the header changed under a refused mutation: %+v", k.hdr)
	}
}

// TestOpenForeign: the vault knows every VMK it ever had (§18.2), so a backup
// of itself from an earlier generation opens with no key from the user; the
// file's own plaintext facts order nothing and decide nothing; and anything no
// VMK opens is ErrForeign, never corruption.
func TestOpenForeign(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "v.eks")
	rk := recoveryKey(t)
	tok := newToken(t)
	u := mustCreate(t, path, HardwareSlot{PublicKey: tok.PublicKey()}, RecoverySlot{Key: rk, Label: "paper"})

	var archID [16]byte
	var ak [32]byte
	withSession(t, u, func(s *Session) {
		a := archive(t, s, "photos")
		archID = a.ArchiveID
		key, err := s.UnwrapArchiveKey(a.ArchiveID, &a.Versions[0])
		if err != nil {
			t.Fatal(err)
		}
		ak = key
		if err := u.UpdateRegistry(func(g *format.Registry) error {
			g.Archives = append(g.Archives, a)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
	backup1 := filepath.Join(dir, "gen1.eks")
	if err := u.Export(backup1); err != nil {
		t.Fatal(err)
	}
	vaultAtGen1 := snapshot(t, path)

	if err := u.Rotate(); err != nil {
		t.Fatal(err)
	}
	if err := u.Rotate(); err != nil {
		t.Fatal(err)
	}
	if got := u.HistoryGenerations(); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("HistoryGenerations: %v", got)
	}
	backup3 := filepath.Join(dir, "gen3.eks")
	if err := u.Export(backup3); err != nil {
		t.Fatal(err)
	}

	// A backup from generation 1, against a vault at generation 3: it opens
	// with the history VMK, with no user key and no token.
	n := countForeign(t)
	beforeBackup := snapshot(t, backup1)
	beforeVault := snapshot(t, path)
	b1 := mustOpen(t, backup1)
	fr, err := u.OpenForeign(b1)
	if err != nil {
		t.Fatalf("a backup of this vault from generation 1: %v", err)
	}
	if fr.OpenedAt != 1 || fr.Generation != 1 || fr.VaultID != u.k.VaultID() {
		t.Errorf("foreign registry: openedAt %d generation %d vault %x", fr.OpenedAt, fr.Generation, fr.VaultID)
	}
	if fr.Tampered != nil {
		t.Errorf("Tampered on a sound backup: %v", fr.Tampered)
	}
	if fr.ModifiedAt != b1.ModifiedAt() {
		t.Errorf("ModifiedAt %d, file says %d", fr.ModifiedAt, b1.ModifiedAt())
	}
	if *n != 3 {
		t.Errorf("candidates attempted: %d, want the current VMK and two history VMKs", *n)
	}
	if tok.calls != 0 {
		t.Errorf("reading a backup touched the token %d times", tok.calls)
	}
	// The converted records unwrap under THIS vault's KWK, and the archive key
	// itself is unchanged: a rotation only ever replaced its wrapper.
	if len(fr.Registry.Archives) != 1 {
		t.Fatalf("incoming archives: %d", len(fr.Registry.Archives))
	}
	withSession(t, u, func(s *Session) {
		got, err := s.UnwrapArchiveKey(archID, &fr.Registry.Archives[0].Versions[0])
		if err != nil {
			t.Fatalf("a converted version record under this vault's KWK: %v", err)
		}
		if got != ak {
			t.Error("the conversion changed the archive key")
		}
	})
	b1.Close()
	if !bytes.Equal(beforeBackup, snapshot(t, backup1)) {
		t.Error("OpenForeign wrote to the file it read")
	}
	if !bytes.Equal(beforeVault, snapshot(t, path)) {
		t.Error("OpenForeign wrote to this vault")
	}

	// The file's plaintext vmk_generation is checksummed, not authenticated
	// (§5): doctored, with the superblock's checksum recomputed, the same
	// candidate still opens it and the same number of attempts are made.
	doctored := filepath.Join(dir, "doctored.eks")
	d := bytes.Clone(beforeBackup)
	dsbOff := format.CopyA.KeystoreSuperblockOff()
	dsb, err := format.DecodeKeystoreSuperblock(d[dsbOff : dsbOff+format.SuperblockSize])
	if err != nil {
		t.Fatal(err)
	}
	dsb.VMKGeneration = 99
	enc, err := dsb.Encode()
	if err != nil {
		t.Fatal(err)
	}
	copy(d[dsbOff:], enc)
	if err := os.WriteFile(doctored, d, 0o600); err != nil {
		t.Fatal(err)
	}
	*n = 0
	kd := mustOpen(t, doctored)
	frd, err := u.OpenForeign(kd)
	if err != nil {
		t.Fatalf("a doctored vmk_generation: %v", err)
	}
	if frd.OpenedAt != 1 {
		t.Errorf("the doctored generation changed which candidate opened it: %d", frd.OpenedAt)
	}
	if frd.Generation != 99 {
		t.Errorf("the file's own generation is reported as it stands: %d", frd.Generation)
	}
	if *n != 3 {
		t.Errorf("the doctored generation changed the attempt count: %d", *n)
	}
	kd.Close()

	// Another vault's export, with an archive of its own so the conversion has
	// work to do on the OpenForeignUnlocked path below.
	otherPath := filepath.Join(dir, "other.eks")
	rk2 := recoveryKey(t)
	uo := mustCreate(t, otherPath, PasswordSlot{Password: "p", Argon2: fast}, RecoverySlot{Key: rk2})
	var otherArch [16]byte
	var otherKey [32]byte
	withSession(t, uo, func(s *Session) {
		a := archive(t, s, "theirs")
		otherArch = a.ArchiveID
		key, err := s.UnwrapArchiveKey(a.ArchiveID, &a.Versions[0])
		if err != nil {
			t.Fatal(err)
		}
		otherKey = key
		if err := uo.UpdateRegistry(func(g *format.Registry) error {
			g.Archives = append(g.Archives, a)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
	otherExport := filepath.Join(dir, "other-backup.eks")
	if err := uo.Export(otherExport); err != nil {
		t.Fatal(err)
	}
	otherVault := uo.k.VaultID()
	uo.Close()
	uo.k.Close()

	// Roll this vault back to generation 1, as re-adopting an older copy would.
	// Now the generation-3 backup is from a generation this vault does not keep
	// and another vault's export never was this vault's: both are ErrForeign,
	// and neither is corruption.
	u.Close()
	u.k.Close()
	restore(t, path, vaultAtGen1)
	k1 := mustOpen(t, path)
	defer k1.Close()
	u1, err := k1.Unlock(RecoveryCredential{Key: rk})
	if err != nil {
		t.Fatal(err)
	}
	defer u1.Close()
	if got := u1.HistoryGenerations(); len(got) != 0 {
		t.Fatalf("a generation-1 vault keeps no history: %v", got)
	}
	for _, c := range []struct {
		name string
		path string
	}{
		{"a backup from a later generation", backup3},
		{"another vault's export", otherExport},
	} {
		*n = 0
		other := mustOpen(t, c.path)
		_, err := u1.OpenForeign(other)
		if !errors.Is(err, ErrForeign) {
			t.Errorf("%s: %v", c.name, err)
		}
		if errors.Is(err, format.ErrInvalid) {
			t.Errorf("%s was reported as corruption: %v", c.name, err)
		}
		if *n != 1 {
			t.Errorf("%s: %d attempts, want one candidate", c.name, *n)
		}
		other.Close()
	}

	// The recovery-key path: the staged file is unlocked with its own key and
	// its records are converted from its own VMK.
	oe := mustOpen(t, otherExport)
	defer oe.Close()
	uoe, err := oe.Unlock(RecoveryCredential{Key: rk2})
	if err != nil {
		t.Fatal(err)
	}
	defer uoe.Close()
	beforeOther := snapshot(t, otherExport)
	fo, err := u1.OpenForeignUnlocked(uoe)
	if err != nil {
		t.Fatalf("OpenForeignUnlocked: %v", err)
	}
	if fo.VaultID != otherVault || fo.OpenedAt != 0 || fo.Tampered != nil {
		t.Errorf("foreign unlocked: vault %x openedAt %d tampered %v", fo.VaultID, fo.OpenedAt, fo.Tampered)
	}
	if len(fo.Registry.Archives) != 1 {
		t.Fatalf("incoming archives: %d", len(fo.Registry.Archives))
	}
	withSession(t, u1, func(s *Session) {
		got, err := s.UnwrapArchiveKey(otherArch, &fo.Registry.Archives[0].Versions[0])
		if err != nil {
			t.Fatalf("a converted record under this vault's KWK: %v", err)
		}
		if got != otherKey {
			t.Error("the conversion changed the archive key")
		}
	})
	// The foreign file's own registry is untouched by the conversion.
	if v := uoe.Registry().Archives[0].Versions[0]; v.WrappedArchiveKey == fo.Registry.Archives[0].Versions[0].WrappedArchiveKey {
		t.Error("the conversion did not re-wrap, or it wrote through to the foreign registry")
	}
	if !bytes.Equal(beforeOther, snapshot(t, otherExport)) {
		t.Error("OpenForeignUnlocked wrote to the file it read")
	}
	// A handle over this same file is not a foreign file.
	if _, err := u1.OpenForeign(k1); !errors.Is(err, ErrParams) {
		t.Errorf("OpenForeign over its own keystore: %v", err)
	}
	if _, err := u1.OpenForeignUnlocked(u1); !errors.Is(err, ErrParams) {
		t.Errorf("OpenForeignUnlocked over its own keystore: %v", err)
	}
}

// TestSecretsSectionOrder: every writer of the secrets section keeps it in
// strict (kind, id) order, so the registry it commits decodes and re-encodes
// byte for byte (§7.6, R21). A section out of order would not decode at all,
// which is what makes this the writer's obligation and not the reader's.
func TestSecretsSectionOrder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "v.eks")
	tok := newToken(t)
	check := func(step string, g *format.Registry, wantSecrets int) {
		t.Helper()
		if len(g.Secrets) != wantSecrets {
			t.Errorf("%s: %d secrets, want %d", step, len(g.Secrets), wantSecrets)
		}
		b, err := g.Encode()
		if err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		again, err := format.DecodeRegistry(b)
		if err != nil {
			t.Fatalf("%s: the section does not decode: %v", step, err)
		}
		b2, err := again.Encode()
		if err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		if !bytes.Equal(b, b2) {
			t.Errorf("%s: the section is not byte-exact under decode/re-encode", step)
		}
	}

	u := mustCreateWith(t, path, CreateOptions{
		Entangle: &Entangle{Password: "vault", Argon2: fast},
		Slots: []SlotSpec{
			HardwareSlot{PublicKey: tok.PublicKey()},
			RecoverySlot{Key: recoveryKey(t), Label: "one"},
			RecoverySlot{Key: recoveryKey(t), Label: "two"},
		},
	})
	check("create", u.k.reg, 3) // two kind-1 records and K_P

	if err := u.AddSlot(RecoverySlot{Key: recoveryKey(t), Label: "three"}); err != nil {
		t.Fatal(err)
	}
	check("AddSlot", u.k.reg, 4)

	if err := u.SetEntangled(false, nil); err != nil {
		t.Fatal(err)
	}
	check("SetEntangled off", u.k.reg, 3)
	if err := u.SetEntangled(true, &Entangle{Password: "again", Argon2: fast}); err != nil {
		t.Fatal(err)
	}
	check("SetEntangled on", u.k.reg, 4)

	if err := u.Rotate(); err != nil {
		t.Fatal(err)
	}
	check("Rotate", u.k.reg, 5) // plus the retired generation
	if err := u.Rotate(); err != nil {
		t.Fatal(err)
	}
	check("Rotate again", u.k.reg, 6)
	if got := u.HistoryGenerations(); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("HistoryGenerations: %v", got)
	}
	// And the file itself decodes, which is the same rule enforced by a reader.
	u.Close()
	u.k.Close()
	k := mustOpen(t, path)
	defer k.Close()
	uk, err := k.Unlock(HardwareCredential{Token: tok, Password: "again"})
	if err != nil {
		t.Fatal(err)
	}
	defer uk.Close()
	check("reopened", uk.Registry(), 6)
}

// TestUpdateRegistryAt: fn is handed the modified_at the commit will carry, and
// the superblock and the registry then record exactly that value — so a
// decision that depends on it (§18.2) cannot be made against a second reading
// of the clock (R35).
func TestUpdateRegistryAt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	rk := recoveryKey(t)
	u := mustCreate(t, path, PasswordSlot{Password: "p", Argon2: fast}, RecoverySlot{Key: rk})

	var seen int64
	withSession(t, u, func(s *Session) {
		a := archive(t, s, "photos")
		if err := u.UpdateRegistryAt(func(g *format.Registry, at int64) error {
			seen = at
			g.Archives = append(g.Archives, a)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
	if seen == 0 {
		t.Fatal("fn was handed no modified_at")
	}
	if u.k.ModifiedAt() != seen {
		t.Errorf("superblock modified_at %d, fn was told %d", u.k.ModifiedAt(), seen)
	}
	if u.k.reg.ModifiedAt != seen {
		t.Errorf("registry modified_at %d, fn was told %d", u.k.reg.ModifiedAt, seen)
	}

	// The Session's is the same act, and the value it hands over is the one a
	// forgotten_at written inside the closure ends up holding.
	var at2 int64
	withSession(t, u, func(s *Session) {
		if err := s.UpdateRegistryAt(func(g *format.Registry, at int64) error {
			at2 = at
			g.Archives[0].ForgottenAt = at
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
	if at2 <= seen {
		t.Errorf("modified_at did not advance: %d then %d", seen, at2)
	}
	if u.k.ModifiedAt() != at2 || u.k.reg.Archives[0].ForgottenAt != at2 {
		t.Errorf("forgotten_at %d, modified_at %d, fn was told %d", u.k.reg.Archives[0].ForgottenAt, u.k.ModifiedAt(), at2)
	}
	// The retention arithmetic therefore runs against the value the file holds:
	// this write cannot drop the record it has just forgotten.
	if dropped := u.k.reg.PurgeForgotten(at2); len(dropped) != 0 {
		t.Errorf("a record forgotten by this very write was purged by it: %d", len(dropped))
	}

	// fn's error abandons the change, as UpdateRegistry's does.
	before := u.k.ModifiedAt()
	boom := errors.New("no")
	if err := u.UpdateRegistryAt(func(g *format.Registry, at int64) error {
		g.Archives = nil
		return boom
	}); !errors.Is(err, boom) {
		t.Errorf("fn's error: %v", err)
	}
	if u.k.ModifiedAt() != before || len(u.k.reg.Archives) != 1 {
		t.Error("a refused update landed anyway")
	}
	// It survives a reopen.
	u.Close()
	u.k.Close()
	k := mustOpen(t, path)
	defer k.Close()
	uk, err := k.Unlock(RecoveryCredential{Key: rk})
	if err != nil {
		t.Fatal(err)
	}
	defer uk.Close()
	if got := uk.Registry().Archives[0].ForgottenAt; got != at2 {
		t.Errorf("forgotten_at after a reopen: %d, want %d", got, at2)
	}
}

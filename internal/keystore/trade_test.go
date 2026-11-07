package keystore

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"

	"github.com/dreamxwarden01/enfold/internal/format"
	"github.com/dreamxwarden01/enfold/internal/kdf"
)

// The tests of the switch to a YubiKey (TradePasswordForToken, ruled
// 2026-11-07) and of the switch's end when a password joins a vault with no
// hardware slot (AddSlot, option A). A test that faults a commit sets the
// package-level commitFault (faultOn), so none of them runs in parallel.

const tradeWords = "the vault's own words"

// passwordVault is a standalone password and a recovery key, with the
// vault's switch on under entangled when it is not empty: the C2 state a
// password vault reaches when its switch is turned on with no YubiKey.
func passwordVault(t *testing.T, path, entangled string) (*Unlocked, kdf.RecoveryKey) {
	t.Helper()
	rk := recoveryKey(t)
	opts := CreateOptions{Slots: []SlotSpec{PasswordSlot{Password: tradeWords, Argon2: fast, Label: "Password"}, RecoverySlot{Key: rk, Label: "paper"}}}
	if entangled != "" {
		opts.Entangle = &Entangle{Password: entangled, Argon2: fast}
	}
	return mustCreateWith(t, path, opts), rk
}

// passwordRID is the recipient ID of the vault's one standalone password.
func passwordRID(t *testing.T, u *Unlocked) [16]byte {
	t.Helper()
	for _, s := range u.k.Slots() {
		if s.Type == format.SlotStandalonePassword {
			return s.RecipientID
		}
	}
	t.Fatal("no standalone password")
	return [16]byte{}
}

// requireTraded checks the file at path as the trade leaves it: one YubiKey
// and the recovery key, the switch on with exactly one entangled_key, the
// YubiKey opening with words and not without them or with others, the old
// standalone password opening nothing, the recovery key opening, all at gen.
func requireTraded(t *testing.T, path string, tok *softToken, rk kdf.RecoveryKey, words, not string, gen uint64) {
	t.Helper()
	k := mustOpen(t, path)
	defer k.Close()
	if !k.Entangled() || k.hdr.EntangleSalt == ([16]byte{}) || k.hdr.Argon2M == 0 {
		t.Fatalf("the header after the trade: %+v", k.hdr)
	}
	types := map[format.SlotType]int{}
	for _, s := range k.Slots() {
		types[s.Type]++
	}
	if types[format.SlotExternalECDH] != 1 || types[format.SlotRecovery] != 1 || types[format.SlotStandalonePassword] != 0 || len(k.Slots()) != 2 {
		t.Fatalf("the slots after the trade: %+v", k.Slots())
	}
	unlockIs(t, k, HardwareCredential{Token: tok, Password: words}, gen, nil, "the YubiKey with the words")
	unlockIs(t, k, HardwareCredential{Token: tok}, 0, ErrPasswordRequired, "the YubiKey alone")
	if not != "" {
		unlockIs(t, k, HardwareCredential{Token: tok, Password: not}, 0, ErrAuth, "the YubiKey with other words")
	}
	unlockIs(t, k, PasswordCredential{Password: tradeWords}, 0, ErrNoSlot, "the old password alone")
	unlockIs(t, k, RecoveryCredential{Key: rk}, gen, nil, "the recovery key")
	u, err := k.Unlock(RecoveryCredential{Key: rk})
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	if n := secretsOf(u.Registry(), format.SecretEntangledKey); n != 1 {
		t.Fatalf("%d entangled_key records", n)
	}
	got, err := openSecret(t, u, format.SecretEntangledKey, [16]byte{})
	if err != nil || u.kp() == nil || got != *u.kp() {
		t.Fatalf("the entangled_key record: %v", err)
	}
}

// 1. The trade in one commit, for the same words and a new password, each
// with and without the re-key: the password's slot gone, the YubiKey's
// there, the switch on with a fresh salt and one entangled_key holding the
// handle's K_P, one flip, and R41's retiring commit owed after it.
func TestTradePasswordForToken(t *testing.T) {
	for _, c := range []struct {
		name   string
		words  string
		rotate bool
	}{
		{"the same words", tradeWords, false},
		{"the same words, re-keyed", tradeWords, true},
		{"a new password", "a new password entirely", false},
		{"a new password, re-keyed", "a new password entirely", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "v.eks")
			u, rk := passwordVault(t, path, "")
			tok := newToken(t)
			k := u.k
			seq := k.sb.Seq
			n := countArgon2(t)
			if err := u.TradePasswordForToken(passwordRID(t, u), HardwareSlot{PublicKey: tok.PublicKey(), Label: "Desk YubiKey"}, Entangle{Password: c.words, Argon2: fast}, c.rotate); err != nil {
				t.Fatal(err)
			}
			if *n != 1 {
				t.Fatalf("the trade ran Argon2id %d times, want once", *n)
			}
			gen := uint64(1)
			if c.rotate {
				gen = 2
			}
			if k.sb.Seq != seq+1 || u.gen != gen || k.Generation() != gen {
				t.Fatalf("seq %d after %d, generation %d, handle %d", k.sb.Seq, seq, k.Generation(), u.gen)
			}
			if got := historyGenerations(u.Registry()); c.rotate != (len(got) == 1) {
				t.Fatalf("history %v", got)
			}
			if !k.RetireOwed() {
				t.Fatal("the trade owes no retirement: the state before is still in the file")
			}
			if err := u.Retire(); err != nil {
				t.Fatal(err)
			}
			if tok.calls != 0 {
				t.Fatalf("the trade touched the token %d times; it wraps from the public key", tok.calls)
			}
			u.Close()
			k.Close()
			not := ""
			if c.words != tradeWords {
				not = tradeWords
			}
			requireTraded(t, path, tok, rk, c.words, not, gen)
		})
	}
}

// 2. A C2 vault (a standalone password beside a switch left on with no
// YubiKey): the trade replaces the kept K_P with the typed words', under a
// fresh salt, and the words the switch was turned on with open nothing.
func TestTradePasswordForTokenReplacesAKeptKP(t *testing.T) {
	for _, rotate := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "v.eks")
		u, rk := passwordVault(t, path, "words set long ago")
		k := u.k
		oldSalt, oldKP := k.hdr.EntangleSalt, *u.kp()
		tok := newToken(t)
		if err := u.TradePasswordForToken(passwordRID(t, u), HardwareSlot{PublicKey: tok.PublicKey()}, Entangle{Password: tradeWords, Argon2: fast}, rotate); err != nil {
			t.Fatal(err)
		}
		if k.hdr.EntangleSalt == oldSalt || *u.kp() == oldKP {
			t.Fatal("the trade kept the C2 vault's salt or K_P")
		}
		gen := u.gen
		u.Retire()
		u.Close()
		k.Close()
		requireTraded(t, path, tok, rk, tradeWords, "words set long ago", gen)
	}
}

// 3. The same words never give the same key in the two roles: the slot's
// Argon2id salt and K_P's are domain-separated (FORMAT §3.1, R13), and the
// trade draws a fresh entangle_salt each time (defence in depth).
func TestTradeDrawsAFreshSalt(t *testing.T) {
	var salts [][16]byte
	for range 2 {
		path := filepath.Join(t.TempDir(), "v.eks")
		u, _ := passwordVault(t, path, "")
		tok := newToken(t)
		if err := u.TradePasswordForToken(passwordRID(t, u), HardwareSlot{PublicKey: tok.PublicKey()}, Entangle{Password: tradeWords, Argon2: fast}, false); err != nil {
			t.Fatal(err)
		}
		salts = append(salts, u.k.hdr.EntangleSalt)
	}
	if salts[0] == salts[1] || salts[0] == ([16]byte{}) {
		t.Fatalf("salts %x", salts)
	}
}

// 4. Every refusal comes before Argon2id and writes nothing: a token vault,
// a recipient ID that is not the standalone password, a public key that is
// not a point, a token a retired record already holds (§6.4 over the final
// set), an empty password, and a tampered region. The ranked reasons of
// CanTradePassword over the vault shapes the page greys.
func TestTradePasswordForTokenRefuses(t *testing.T) {
	t.Run("the reasons in their order", func(t *testing.T) {
		pw := format.SlotRecord{State: format.SlotActive, Type: format.SlotStandalonePassword}
		rec := format.SlotRecord{State: format.SlotActive, Type: format.SlotRecovery}
		hw := format.SlotRecord{State: format.SlotActive, Type: format.SlotExternalECDH}
		retiredPW := pw
		retiredPW.State = format.SlotRetired
		for _, c := range []struct {
			name  string
			slots []format.SlotRecord
			want  error
		}{
			{"a password and a recovery key", []format.SlotRecord{pw, rec}, nil},
			{"a password and 2 recovery keys", []format.SlotRecord{pw, rec, rec}, nil},
			{"recovery keys alone", []format.SlotRecord{rec, rec}, ErrNotFound},
			{"a YubiKey", []format.SlotRecord{hw, rec}, ErrNotFound},
			{"a retired password", []format.SlotRecord{retiredPW, rec, rec}, ErrNotFound},
			{"a password and a YubiKey", []format.SlotRecord{pw, hw, rec}, ErrNotFound},
			{"a password alone", []format.SlotRecord{pw}, ErrNoRecoverySlot},
			{"2 passwords and no recovery key", []format.SlotRecord{pw, pw}, ErrNoRecoverySlot},
			{"2 passwords", []format.SlotRecord{pw, pw, rec}, ErrPolicy},
		} {
			if got := canTradePassword(c.slots); !errors.Is(got, c.want) || (c.want == nil) != (got == nil) {
				t.Errorf("%s: %v, want %v", c.name, got, c.want)
			}
		}
	})
	refused := func(t *testing.T, u *Unlocked, path string, want error, do func() error) {
		t.Helper()
		before := snapshot(t, path)
		n := countArgon2(t)
		if err := do(); !errors.Is(err, want) {
			t.Fatalf("%v, want %v", err, want)
		}
		requireUnchanged(t, u, path, before, 1, "the refused trade")
		if *n != 0 {
			t.Fatalf("the refused trade ran Argon2id %d times", *n)
		}
	}
	ent := Entangle{Password: tradeWords, Argon2: fast}
	t.Run("a YubiKey vault", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "v.eks")
		u, tok, _ := tokenVault(t, path, "")
		if err := u.k.CanTradePassword(); !errors.Is(err, ErrNotFound) {
			t.Fatalf("CanTradePassword: %v", err)
		}
		refused(t, u, path, ErrNotFound, func() error {
			return u.TradePasswordForToken(u.k.slots[0].RecipientID, HardwareSlot{PublicKey: newToken(t).PublicKey()}, ent, false)
		})
		_ = tok
	})
	t.Run("2 passwords", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "v.eks")
		u := mustCreate(t, path, PasswordSlot{Password: "one password", Argon2: fast}, PasswordSlot{Password: "another one", Argon2: fast}, RecoverySlot{Key: recoveryKey(t)})
		if err := u.k.CanTradePassword(); !errors.Is(err, ErrPolicy) {
			t.Fatalf("CanTradePassword: %v", err)
		}
		refused(t, u, path, ErrPolicy, func() error {
			return u.TradePasswordForToken(u.k.slots[0].RecipientID, HardwareSlot{PublicKey: newToken(t).PublicKey()}, ent, true)
		})
	})
	for _, c := range []struct {
		name string
		want error
		do   func(t *testing.T, u *Unlocked) error
	}{
		{"the recovery key's ID", ErrNotFound, func(t *testing.T, u *Unlocked) error {
			for _, s := range u.k.Slots() {
				if s.Type == format.SlotRecovery {
					return u.TradePasswordForToken(s.RecipientID, HardwareSlot{PublicKey: newToken(t).PublicKey()}, ent, false)
				}
			}
			return nil
		}},
		{"no such ID", ErrNotFound, func(t *testing.T, u *Unlocked) error {
			return u.TradePasswordForToken([16]byte{1}, HardwareSlot{PublicKey: newToken(t).PublicKey()}, ent, false)
		}},
		{"not a point", ErrParams, func(t *testing.T, u *Unlocked) error {
			return u.TradePasswordForToken(passwordRID(t, u), HardwareSlot{PublicKey: bytes.Repeat([]byte{4}, 65)}, ent, false)
		}},
		{"an empty password", ErrParams, func(t *testing.T, u *Unlocked) error {
			return u.TradePasswordForToken(passwordRID(t, u), HardwareSlot{PublicKey: newToken(t).PublicKey()}, Entangle{Argon2: fast}, true)
		}},
		{"parameters outside R24", ErrParams, func(t *testing.T, u *Unlocked) error {
			return u.TradePasswordForToken(passwordRID(t, u), HardwareSlot{PublicKey: newToken(t).PublicKey()}, Entangle{Password: tradeWords}, false)
		}},
		{"a token a retired record holds", ErrDuplicate, func(t *testing.T, u *Unlocked) error {
			// The record is the handle's only, as a region a reader accepted
			// would hand it: §6.4 over the final set refuses it before the
			// salt is drawn.
			tok := newToken(t)
			u.k.slots = append(u.k.slots, format.SlotRecord{State: format.SlotRetired, Type: format.SlotExternalECDH, RecipientID: [16]byte{9}, SlotPubkey: tok.PublicKey()})
			return u.TradePasswordForToken(passwordRID(t, u), HardwareSlot{PublicKey: tok.PublicKey()}, ent, true)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "v.eks")
			u, _ := passwordVault(t, path, "")
			refused(t, u, path, c.want, func() error { return c.do(t, u) })
		})
	}
	t.Run("a tampered region", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "v.eks")
		u, _ := tamperedVault(t, path)
		refused(t, u, path, ErrTampered, func() error {
			return u.TradePasswordForToken(passwordRID(t, u), HardwareSlot{PublicKey: newToken(t).PublicKey()}, ent, false)
		})
	})
}

// 5. A crash at every write of the trade's commit leaves the password vault
// or the YubiKey vault, never a mix: the region, the registry and the
// barrier's fsync failing leave the old state live, the superblock write
// failing (the commit point) leaves it too, and the flip written with its
// fsync failing leaves the new one. Reopened, exactly one of the two ways
// in opens, and the recovery key opens either.
func TestTradePasswordForTokenCrashAtEveryWrite(t *testing.T) {
	for _, rotate := range []bool{false, true} {
		for _, step := range []string{"region", "registry", "sync", "superblock", "flipsync"} {
			name := step
			if rotate {
				name += ", re-keyed"
			}
			t.Run(name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "v.eks")
				u, rk := passwordVault(t, path, "")
				tok := newToken(t)
				k := u.k
				restoreFault := faultOn(t, step)
				err := u.TradePasswordForToken(passwordRID(t, u), HardwareSlot{PublicKey: tok.PublicKey()}, Entangle{Password: "new words here", Argon2: fast}, rotate)
				restoreFault()
				if err == nil {
					t.Fatal("the faulted trade succeeded")
				}
				u.Close()
				k.Close()
				landed := step == "flipsync"
				k = mustOpen(t, path)
				defer k.Close()
				pwErr, tokErr := ErrNoSlot, error(nil)
				if !landed {
					pwErr, tokErr = nil, ErrNoSlot
				}
				gen := uint64(1)
				if landed && rotate {
					gen = 2
				}
				unlockIs(t, k, PasswordCredential{Password: tradeWords}, gen, pwErr, "the password")
				if landed {
					unlockIs(t, k, HardwareCredential{Token: tok, Password: "new words here"}, gen, tokErr, "the YubiKey")
				} else {
					unlockIs(t, k, HardwareCredential{Token: tok}, gen, tokErr, "the YubiKey")
				}
				unlockIs(t, k, RecoveryCredential{Key: rk}, gen, nil, "the recovery key")
				if k.Entangled() != landed {
					t.Fatalf("the switch reads %v after a fault at %s", k.Entangled(), step)
				}
			})
		}
	}
}

// 6. Re-keyed, the trade leaves an earlier copy of the file behind at the
// old generation: that copy still opens with the password (nothing protects
// a copy already taken), and its VMK is not the one the YubiKey reaches now.
func TestTradeWithARekeyLeavesTheEarlierCopyBehind(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	u, _ := passwordVault(t, path, "")
	earlier := snapshot(t, path)
	oldVMK := u.vmk()
	tok := newToken(t)
	if err := u.TradePasswordForToken(passwordRID(t, u), HardwareSlot{PublicKey: tok.PublicKey()}, Entangle{Password: tradeWords, Argon2: fast}, true); err != nil {
		t.Fatal(err)
	}
	if err := u.Retire(); err != nil {
		t.Fatal(err)
	}
	u.Close()
	u.k.Close()
	k := mustOpen(t, path)
	nu, err := k.Unlock(HardwareCredential{Token: tok, Password: tradeWords})
	if err != nil {
		t.Fatal(err)
	}
	if nu.gen != 2 || nu.vmk() == oldVMK {
		t.Fatalf("the YubiKey reaches generation %d, the old VMK %v", nu.gen, nu.vmk() == oldVMK)
	}
	nu.Close()
	k.Close()

	copyPath := filepath.Join(t.TempDir(), "copy.eks")
	restore(t, copyPath, earlier)
	ck := mustOpen(t, copyPath)
	unlockIs(t, ck, PasswordCredential{Password: tradeWords}, 1, nil, "the earlier copy with the password")
	ck.Close()
}

// 7. Option A: a password added to a vault of recovery keys whose switch is
// on ends the switch in the same commit — the header zeroed, the
// entangled_key record dropped, the handle's K_P gone — and the switch to a
// YubiKey works afterwards from the words then typed; once that password is
// later removed, a YubiKey enrolled is wrapped from no old K_P.
func TestAPasswordAddedToAVaultWithNoYubiKeyEndsTheSwitch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	rkA, rkB := recoveryKey(t), recoveryKey(t)
	u := mustCreateWith(t, path, CreateOptions{
		Slots:    []SlotSpec{RecoverySlot{Key: rkA, Label: "A"}, RecoverySlot{Key: rkB, Label: "B"}},
		Entangle: &Entangle{Password: "words set long ago", Argon2: fast},
	})
	k := u.k
	if err := u.AddSlot(RecoverySlot{Key: recoveryKey(t), Label: "C"}); err != nil {
		t.Fatal(err)
	}
	if !k.Entangled() || u.kp() == nil {
		t.Fatal("a recovery key added ended the switch")
	}
	seq := k.sb.Seq
	if err := u.AddSlot(PasswordSlot{Password: tradeWords, Argon2: fast, Label: "Password"}); err != nil {
		t.Fatal(err)
	}
	if k.sb.Seq != seq+1 {
		t.Fatalf("the add took %d commits", k.sb.Seq-seq)
	}
	if k.Entangled() || k.hdr != (format.SlotRegionHeader{}) || u.kp() != nil {
		t.Fatalf("the switch after a password was added: %+v, K_P held %v", k.hdr, u.kp() != nil)
	}
	if n := secretsOf(u.Registry(), format.SecretEntangledKey); n != 0 {
		t.Fatalf("%d entangled_key records after the switch ended", n)
	}
	u.Retire()
	u.Close()
	k.Close()
	k = mustOpen(t, path)
	if k.Entangled() || k.CanTradePassword() != nil {
		t.Fatalf("reopened: entangled %v, CanTradePassword %v", k.Entangled(), k.CanTradePassword())
	}
	unlockIs(t, k, PasswordCredential{Password: tradeWords}, 1, nil, "the password added")

	// The switch to a YubiKey from there.
	u, err := k.Unlock(PasswordCredential{Password: tradeWords})
	if err != nil {
		t.Fatal(err)
	}
	tok := newToken(t)
	if err := u.TradePasswordForToken(passwordRID(t, u), HardwareSlot{PublicKey: tok.PublicKey()}, Entangle{Password: tradeWords, Argon2: fast}, false); err != nil {
		t.Fatal(err)
	}
	unlockIs(t, k, HardwareCredential{Token: tok, Password: tradeWords}, 1, nil, "the YubiKey after the switch")
	u.Close()
	k.Close()

	// A second vault: the password added, then removed, then a YubiKey
	// enrolled: it opens with its PIN alone, from no K_P.
	path2 := filepath.Join(t.TempDir(), "v2.eks")
	u2 := mustCreateWith(t, path2, CreateOptions{
		Slots:    []SlotSpec{RecoverySlot{Key: rkA, Label: "A"}, RecoverySlot{Key: rkB, Label: "B"}},
		Entangle: &Entangle{Password: "words set long ago", Argon2: fast},
	})
	if err := u2.AddSlot(PasswordSlot{Password: tradeWords, Argon2: fast}); err != nil {
		t.Fatal(err)
	}
	if err := u2.RemoveSlot(passwordRID(t, u2)); err != nil {
		t.Fatal(err)
	}
	tok2 := newToken(t)
	if err := u2.AddSlot(HardwareSlot{PublicKey: tok2.PublicKey()}); err != nil {
		t.Fatal(err)
	}
	unlockIs(t, u2.k, HardwareCredential{Token: tok2}, 1, nil, "the YubiKey with no password")
}

// 8. A password added while the switch is off copies the header forward as
// before, and the switch's end is one commit even where a fault fails it:
// the header and the record are still the old ones after a refused write.
func TestAPasswordAddedWithTheSwitchOffKeepsTheHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	u := mustCreate(t, path, RecoverySlot{Key: recoveryKey(t)}, RecoverySlot{Key: recoveryKey(t)})
	hdr := u.k.hdr
	if err := u.AddSlot(PasswordSlot{Password: tradeWords, Argon2: fast}); err != nil {
		t.Fatal(err)
	}
	if u.k.hdr != hdr {
		t.Fatal("the header moved")
	}

	path2 := filepath.Join(t.TempDir(), "v2.eks")
	u2 := mustCreateWith(t, path2, CreateOptions{
		Slots:    []SlotSpec{RecoverySlot{Key: recoveryKey(t)}, RecoverySlot{Key: recoveryKey(t)}},
		Entangle: &Entangle{Password: "words set long ago", Argon2: fast},
	})
	kp := *u2.kp()
	restoreFault := faultOn(t, "registry")
	err := u2.AddSlot(PasswordSlot{Password: tradeWords, Argon2: fast})
	restoreFault()
	if !errors.Is(err, errFault) {
		t.Fatalf("the faulted add: %v", err)
	}
	if !u2.k.Entangled() || u2.kp() == nil || *u2.kp() != kp {
		t.Fatal("a refused add ended the switch on the handle")
	}
}

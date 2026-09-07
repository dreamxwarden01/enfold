package kdf

import (
	"bytes"
	"crypto/rand"
	"errors"
	"strings"
	"testing"

	"github.com/dreamxwarden01/enfold/internal/format"
)

func randomID(t *testing.T) (id [IDSize]byte) {
	t.Helper()
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	return
}

func random32(t *testing.T) (s [32]byte) {
	t.Helper()
	if _, err := rand.Read(s[:]); err != nil {
		t.Fatal(err)
	}
	return
}

var tiny = Argon2Params{MemKiB: 8192, Time: 1, Threads: 4}

func TestArgon2Params(t *testing.T) {
	bad := []Argon2Params{
		{}, {MemKiB: 8192, Time: 0, Threads: 1}, {MemKiB: 8, Time: 1, Threads: 2}, {MemKiB: 8192, Time: 1, Threads: 0},
		{MemKiB: format.MaxArgon2MemKiB + 1, Time: 1, Threads: 1}, {MemKiB: 0xFFFFFFFF, Time: 1, Threads: 4},
		{MemKiB: 8192, Time: format.MaxArgon2Time + 1, Threads: 1}, {MemKiB: 8192, Time: 1, Threads: format.MaxArgon2Threads + 1},
	}
	for _, p := range bad {
		if err := p.Validate(); !errors.Is(err, ErrParams) {
			t.Errorf("%+v accepted", p)
		}
	}
	for _, p := range []Argon2Params{tiny, {MemKiB: format.MaxArgon2MemKiB, Time: 1, Threads: 1}, {MemKiB: 8 * 32, Time: 32, Threads: 32}} {
		if err := p.Validate(); err != nil {
			t.Errorf("%+v rejected: %v", p, err)
		}
	}
	// A hostile record's parameters never reach Argon2id: the derivation refuses first.
	h := make([]byte, KeySize)
	pw, _ := NormalizePassword("x")
	if _, err := HardwarePreEntangled(h, pw, Salt{}, [IDSize]byte{}, [IDSize]byte{}, Argon2Params{MemKiB: 0xFFFFFFFF, Time: 1, Threads: 4}); !errors.Is(err, ErrParams) {
		t.Fatalf("4 TiB argon2_m reached the derivation: %v", err)
	}
	// Work is bounded as well as memory (R24): 2 GiB × 32 passes is refused.
	if err := (Argon2Params{MemKiB: format.MaxArgon2MemKiB, Time: 32, Threads: 4}).Validate(); !errors.Is(err, ErrParams) {
		t.Fatal("2 GiB x 32 passes accepted")
	}
	if err := (Argon2Params{MemKiB: format.MaxArgon2MemKiB, Time: 4, Threads: 4}).Validate(); err != nil {
		t.Fatalf("2 GiB x 4 passes rejected: %v", err)
	}
}

func TestNormalizePassword(t *testing.T) {
	if _, err := NormalizePassword(""); !errors.Is(err, ErrEmptyPassword) {
		t.Fatal("empty password accepted")
	}
	a, _ := NormalizePassword("café")
	b, _ := NormalizePassword("café")
	if !bytes.Equal(a, b) {
		t.Fatalf("NFC/NFD differ: %x %x", a, b)
	}
	// NFC, not NFKC: compatibility variants stay distinct passwords.
	c, _ := NormalizePassword("ﬁ")
	d, _ := NormalizePassword("fi")
	if bytes.Equal(c, d) {
		t.Fatal("compatibility folding applied")
	}
	// Combining marks with no base still count as a non-empty password.
	if p, err := NormalizePassword("́"); err != nil || len(p) == 0 {
		t.Fatal("lone combining mark rejected")
	}
}

func TestHardwarePre(t *testing.T) {
	h := make([]byte, KeySize)
	rand.Read(h)
	vault, recip, salt := randomID(t), randomID(t), Salt(random32(t))
	pre, err := HardwarePreToken(h)
	if err != nil || !bytes.Equal(pre[:], h) {
		t.Fatalf("no password: %v", err)
	}
	if _, err := HardwarePreToken(h[:31]); !errors.Is(err, ErrKeySize) {
		t.Fatal("31-byte H accepted")
	}
	pw, _ := NormalizePassword("hunter2")
	if _, err := HardwarePreEntangled(h, pw, salt, vault, recip, Argon2Params{}); !errors.Is(err, ErrParams) {
		t.Fatal("password with zero params accepted")
	}
	if _, err := HardwarePreEntangled(h[:31], pw, salt, vault, recip, tiny); !errors.Is(err, ErrKeySize) {
		t.Fatal("31-byte H accepted")
	}
	// The entangled derivation never degrades to the token-only one (R4): an
	// absent password is an error, whether nil or empty.
	for _, empty := range [][]byte{nil, {}} {
		if _, err := HardwarePreEntangled(h, empty, salt, vault, recip, tiny); !errors.Is(err, ErrEmptyPassword) {
			t.Fatal("entangled derivation without a password accepted")
		}
	}
	p1, _ := HardwarePreEntangled(h, pw, salt, vault, recip, tiny)
	p2, _ := HardwarePreEntangled(h, pw, salt, vault, recip, tiny)
	if p1 != p2 {
		t.Fatal("not deterministic")
	}
	if p1 == pre {
		t.Fatal("entangled pre equals the token-only pre")
	}
	other, _ := NormalizePassword("hunter3")
	p3, _ := HardwarePreEntangled(h, other, salt, vault, recip, tiny)
	if p1 == p3 {
		t.Fatal("password does not influence pre")
	}
	swapped, _ := HardwarePreEntangled(h, pw, Salt(random32(t)), vault, recip, tiny)
	if swapped == p1 {
		t.Fatal("salt does not influence pre")
	}
}

func TestRecoveryDigits(t *testing.T) {
	r, err := NewRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	d := r.Digits()
	if len(d) != 55 || strings.Count(d, "-") != 7 {
		t.Fatalf("digits %q", d)
	}
	back, err := ParseRecoveryDigits(d)
	if err != nil || back != r {
		t.Fatalf("round trip: %v", err)
	}
	// Tolerant input (R23): spaces, no dashes, en dashes from a word processor,
	// ideographic spaces from a Chinese document, surrounding whitespace.
	for name, in := range map[string]string{
		"spaces":      "  " + strings.ReplaceAll(d, "-", " ") + "\n",
		"undashed":    strings.ReplaceAll(d, "-", ""),
		"en dash":     strings.ReplaceAll(d, "-", "–"),
		"em dash":     strings.ReplaceAll(d, "-", " — "),
		"ideographic": strings.ReplaceAll(d, "-", "　"),
		"nbsp":        strings.ReplaceAll(d, "-", " "),
		"mixed":       strings.ReplaceAll(strings.ReplaceAll(d, "-", " - "), " - ", "-\t"),
	} {
		if back, err := ParseRecoveryDigits(in); err != nil || back != r {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Every single-digit typo is caught by the ×11 check.
	for i, c := range d {
		if c == '-' {
			continue
		}
		mut := []byte(d)
		mut[i] = '0' + byte((int(c-'0')+1)%10)
		if _, err := ParseRecoveryDigits(string(mut)); !errors.Is(err, ErrRecoveryDigits) {
			t.Fatalf("typo at %d accepted: %s", i, mut)
		}
	}
	for _, bad := range []string{"", d[:54], d + "1", strings.ReplaceAll(d, "-", "x"), "１" + d[1:],
		"720896-000000-000000-000000-000000-000000-000000-000000"} {
		if _, err := ParseRecoveryDigits(bad); !errors.Is(err, ErrRecoveryDigits) {
			t.Errorf("%q accepted", bad)
		}
	}
	// The extreme values render and parse.
	var max RecoveryKey
	for i := range max {
		max[i] = 0xFF
	}
	if back, err := ParseRecoveryDigits(max.Digits()); err != nil || back != max {
		t.Fatalf("all-ones key: %s %v", max.Digits(), err)
	}
	if got := (RecoveryKey{}).Digits(); got != "000000-000000-000000-000000-000000-000000-000000-000000" {
		t.Fatalf("zero key: %s", got)
	}
}

func TestHybridRoundTrip(t *testing.T) {
	vault, recip, slotSalt := randomID(t), randomID(t), SlotSalt(random32(t))
	for _, kind := range []HybridKind{HybridRecovery, HybridPassword} {
		ikm := make([]byte, 32)
		rand.Read(ikm)
		seedX, seedK, err := HybridSeeds(kind, ikm, slotSalt, vault, recip)
		if err != nil {
			t.Fatal(err)
		}
		skX, dk, err := HybridKeys(seedX, seedK)
		if err != nil {
			t.Fatal(err)
		}
		pkX, ek := HybridPublic(skX, dk)
		if len(pkX) != X25519PubSize || len(ek) != MLKEMEKSize {
			t.Fatalf("public sizes %d %d", len(pkX), len(ek))
		}
		// Creation → offline wrap (public halves only) → unlock (private halves).
		pre, E, ct, err := HybridWrap(kind, pkX, ek, vault, recip)
		if err != nil {
			t.Fatal(err)
		}
		if len(E) != X25519PubSize || len(ct) != MLKEMCTSize {
			t.Fatalf("wrap output sizes %d %d", len(E), len(ct))
		}
		skX2, dk2, _ := HybridKeys(seedX, seedK) // re-derived on unlock
		got, err := HybridUnwrap(kind, skX2, dk2, E, ct, vault, recip)
		if err != nil || got != pre {
			t.Fatalf("kind %d: unwrap %v, match %v", kind, err, got == pre)
		}
		// Two wraps of the same slot differ (fresh e, fresh encapsulation) yet both unwrap.
		pre2, E2, ct2, _ := HybridWrap(kind, pkX, ek, vault, recip)
		if pre == pre2 || bytes.Equal(E, E2) || bytes.Equal(ct, ct2) {
			t.Fatal("re-wrap reused material")
		}
		if got, _ := HybridUnwrap(kind, skX, dk, E2, ct2, vault, recip); got != pre2 {
			t.Fatal("second wrap does not unwrap")
		}
		// The other kind derives different keys from the same IKM (domain separation).
		otherX, _, _ := HybridSeeds(3-kind, ikm, slotSalt, vault, recip)
		if otherX == seedX {
			t.Fatal("kinds share seeds")
		}
		// Implicit rejection: a wrong dk gives a different pre, not an error.
		wrongIKM := make([]byte, 32)
		rand.Read(wrongIKM)
		wx, wk, _ := HybridSeeds(kind, wrongIKM, slotSalt, vault, recip)
		wskX, wdk, _ := HybridKeys(wx, wk)
		if VerifyX25519(wskX, pkX) {
			t.Fatal("verifier accepted the wrong key")
		}
		wrong, err := HybridUnwrap(kind, wskX, wdk, E, ct, vault, recip)
		if err != nil || wrong == pre {
			t.Fatalf("wrong credential: err %v, same pre %v", err, wrong == pre)
		}
		// Malformed public material is rejected before any secret is touched.
		if _, _, _, err := HybridWrap(kind, pkX[:31], ek, vault, recip); !errors.Is(err, ErrHybridInput) {
			t.Fatal("short pk_x accepted")
		}
		if _, _, _, err := HybridWrap(kind, pkX, ek[:100], vault, recip); !errors.Is(err, ErrHybridInput) {
			t.Fatal("short ek accepted")
		}
		if _, err := HybridUnwrap(kind, skX, dk, E[:31], ct, vault, recip); !errors.Is(err, ErrHybridInput) {
			t.Fatal("short E accepted")
		}
		if _, err := HybridUnwrap(kind, skX, dk, E, ct[:100], vault, recip); !errors.Is(err, ErrHybridInput) {
			t.Fatal("short ct accepted")
		}
		// An all-zero E is a low-order point: crypto/ecdh refuses the all-zero shared secret.
		if _, err := HybridUnwrap(kind, skX, dk, make([]byte, 32), ct, vault, recip); !errors.Is(err, ErrHybridInput) {
			t.Fatal("all-zero E accepted")
		}
	}
	if _, _, err := HybridSeeds(9, make([]byte, 16), SlotSalt{}, vault, recip); err == nil {
		t.Fatal("unknown kind accepted")
	}
	// A truncated credential is refused, never derived from.
	if _, _, err := HybridSeeds(HybridRecovery, make([]byte, 15), SlotSalt{}, vault, recip); !errors.Is(err, ErrKeySize) {
		t.Fatal("15-byte IKM accepted")
	}
	if _, _, err := HybridSeeds(HybridRecovery, nil, SlotSalt{}, vault, recip); !errors.Is(err, ErrKeySize) {
		t.Fatal("nil IKM accepted")
	}
}

func TestRecoveryErrorsLeakNothing(t *testing.T) {
	r, _ := NewRecoveryKey()
	d := r.Digits()
	mut := []byte(d)
	mut[8] = '0' + byte((int(mut[8]-'0')+1)%10)
	_, err := ParseRecoveryDigits(string(mut))
	if err == nil {
		t.Fatal("typo accepted")
	}
	for i := 0; i+6 <= len(d); i++ {
		if g := d[i : i+6]; !strings.Contains(g, "-") && strings.Contains(err.Error(), g) {
			t.Fatalf("error %q echoes key digits", err)
		}
	}
	if _, err := ParseRecoveryDigits(d[:20] + "Z" + d[20:]); err == nil || strings.Contains(err.Error(), "Z") {
		t.Fatalf("error %q echoes the typed character", err)
	}
}

func TestPasswordSlotEndToEnd(t *testing.T) {
	vault, recip, salt, slotSalt := randomID(t), randomID(t), Salt(random32(t)), SlotSalt(random32(t))
	pw, _ := NormalizePassword("correct horse battery staple")
	a, err := PasswordSlotIKM(pw, salt, vault, recip, tiny)
	if err != nil {
		t.Fatal(err)
	}
	seedX, seedK, _ := HybridSeeds(HybridPassword, a, slotSalt, vault, recip)
	skX, dk, _ := HybridKeys(seedX, seedK)
	pkX, ek := HybridPublic(skX, dk)
	pre, E, ct, err := HybridWrap(HybridPassword, pkX, ek, vault, recip)
	if err != nil {
		t.Fatal(err)
	}
	ik := DeriveIK(pre, vault, recip)
	vmk := random32(t)
	aad := []byte("slot record bytes stand in here")
	wrapped, nonce, err := WrapVMK(ik, vmk, 3, aad)
	if err != nil {
		t.Fatal(err)
	}

	// Unlock: password → A → seeds → keys → verifier → pre → IK → VMK.
	a2, _ := PasswordSlotIKM(pw, salt, vault, recip, tiny)
	sx, sk, _ := HybridSeeds(HybridPassword, a2, slotSalt, vault, recip)
	skX2, dk2, _ := HybridKeys(sx, sk)
	if !VerifyX25519(skX2, pkX) {
		t.Fatal("verifier failed on the right password")
	}
	pre2, _ := HybridUnwrap(HybridPassword, skX2, dk2, E, ct, vault, recip)
	got, gen, err := UnwrapVMK(DeriveIK(pre2, vault, recip), wrapped, nonce, aad)
	if err != nil || got != vmk || gen != 3 {
		t.Fatalf("unlock failed: %v", err)
	}
	if _, err := PasswordSlotIKM(nil, salt, vault, recip, tiny); !errors.Is(err, ErrEmptyPassword) {
		t.Fatal("empty password accepted")
	}
	if _, err := PasswordSlotIKM(pw, salt, vault, recip, Argon2Params{}); !errors.Is(err, ErrParams) {
		t.Fatal("zero params accepted")
	}
}

func TestWrapAuthentication(t *testing.T) {
	kek := make([]byte, KeySize)
	rand.Read(kek)
	key, vmk := random32(t), random32(t)
	aad := []byte("aad")

	w, nonce, err := WrapKey(kek, key, aad)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := UnwrapKey(kek, w, nonce, aad); err != nil || got != key {
		t.Fatalf("round trip: %v", err)
	}
	tamper := w
	tamper[5] ^= 1
	if _, err := UnwrapKey(kek, tamper, nonce, aad); !errors.Is(err, ErrAuth) {
		t.Fatal("tampered ciphertext accepted")
	}
	if _, err := UnwrapKey(kek, w, nonce, []byte("other")); !errors.Is(err, ErrAuth) {
		t.Fatal("wrong AAD accepted")
	}
	other := make([]byte, KeySize)
	rand.Read(other)
	if _, err := UnwrapKey(other, w, nonce, aad); !errors.Is(err, ErrAuth) {
		t.Fatal("wrong key accepted")
	}
	if _, _, err := WrapKey(kek[:16], key, aad); !errors.Is(err, ErrKeySize) {
		t.Fatal("16-byte kek accepted")
	}
	// Every wrap draws its own nonce: re-wrapping under the same key never reuses one.
	w2, nonce2, _ := WrapKey(kek, key, aad)
	if nonce == nonce2 || w == w2 {
		t.Fatal("nonce reused across wraps")
	}

	wv, nv, err := WrapVMK(kek, vmk, 42, aad)
	if err != nil {
		t.Fatal(err)
	}
	got, gen, err := UnwrapVMK(kek, wv, nv, aad)
	if err != nil || got != vmk || gen != 42 {
		t.Fatalf("vmk round trip: %v %d", err, gen)
	}
	// The generation is inside the plaintext: flipping its ciphertext byte is a tamper, not a downgrade.
	tv := wv
	tv[KeySize] ^= 1
	if _, _, err := UnwrapVMK(kek, tv, nv, aad); !errors.Is(err, ErrAuth) {
		t.Fatal("tampered generation accepted")
	}
	wv2, nv2, _ := WrapVMK(kek, vmk, 43, aad)
	if nv == nv2 || wv == wv2 {
		t.Fatal("VMK re-wrap reused a nonce")
	}
}

func TestSubordinateKeysAreDistinct(t *testing.T) {
	vmk := random32(t)
	vault := randomID(t)
	keys := [][]byte{MetadataKey(vmk, vault), DBKey(vmk, vault), KWK(vmk, vault), KWKIdentity(vmk, vault), KWKRecovery(vmk, vault)}
	for i := range keys {
		for j := i + 1; j < len(keys); j++ {
			if bytes.Equal(keys[i], keys[j]) {
				t.Fatalf("keys %d and %d coincide", i, j)
			}
		}
	}
	if bytes.Equal(KWK(vmk, vault), KWK(vmk, randomID(t))) {
		t.Fatal("vault_id does not separate KWKs")
	}
}

func TestRecoveryKeyWrap(t *testing.T) {
	vmk := random32(t)
	vault := randomID(t)
	kek := KWKRecovery(vmk, vault)
	r, err := NewRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	aad := []byte("Enfold/v1/aad/recovery-escrow-test")
	w, n, err := WrapRecoveryKey(kek, r, aad)
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnwrapRecoveryKey(kek, w, n, aad)
	if err != nil || got != r {
		t.Fatalf("round trip: %v %x", err, got)
	}
	if _, err := UnwrapRecoveryKey(KWKIdentity(vmk, vault), w, n, aad); !errors.Is(err, ErrAuth) {
		t.Fatalf("another domain's key opened it: %v", err)
	}
	if _, err := UnwrapRecoveryKey(kek, w, n, []byte("other")); !errors.Is(err, ErrAuth) {
		t.Fatalf("another AAD opened it: %v", err)
	}
	w2, n2, _ := WrapRecoveryKey(kek, r, aad)
	if n == n2 || w == w2 {
		t.Fatal("a re-wrap reused a nonce")
	}
	if _, _, err := WrapRecoveryKey(kek[:16], r, aad); !errors.Is(err, ErrKeySize) {
		t.Fatalf("short KEK: %v", err)
	}
}

package kdf

import (
	"bytes"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
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
	// A hostile slot region header's parameters never reach Argon2id: the one
	// derivation that reads them refuses first (R24, FORMAT §6).
	pw, _ := NormalizePassword("x")
	if _, err := EntangledKey(pw, EntangleSalt{}, [IDSize]byte{}, Argon2Params{MemKiB: 0xFFFFFFFF, Time: 1, Threads: 4}); !errors.Is(err, ErrParams) {
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

// TestHardwarePreToken covers the branch the slot region header's entangle = 0
// selects: pre is H itself, and nothing else enters (§3.1, R5).
func TestHardwarePreToken(t *testing.T) {
	h := make([]byte, KeySize)
	rand.Read(h)
	pre, err := HardwarePreToken(h)
	if err != nil || !bytes.Equal(pre[:], h) {
		t.Fatalf("no password: %v", err)
	}
	if _, err := HardwarePreToken(h[:31]); !errors.Is(err, ErrKeySize) {
		t.Fatal("31-byte H accepted")
	}
}

// TestEntangledKey covers K_P (§3.1): the vault's password and the vault's
// salt, with no token and no slot in it.
func TestEntangledKey(t *testing.T) {
	es := EntangleSalt(randomID(t))
	vault := randomID(t)
	pw, _ := NormalizePassword("hunter2")

	kp, err := EntangledKey(pw, es, vault, tiny)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := EntangledKey(pw, es, vault, tiny)
	if kp != again {
		t.Fatal("not deterministic")
	}
	other, _ := NormalizePassword("hunter3")
	if k, _ := EntangledKey(other, es, vault, tiny); k == kp {
		t.Fatal("the password does not influence K_P")
	}
	if k, _ := EntangledKey(pw, EntangleSalt(randomID(t)), vault, tiny); k == kp {
		t.Fatal("entangle_salt does not influence K_P")
	}
	if k, _ := EntangledKey(pw, es, randomID(t), tiny); k == kp {
		t.Fatal("vault_id does not influence K_P")
	}
	if k, _ := EntangledKey(pw, es, vault, Argon2Params{MemKiB: 8192, Time: 2, Threads: 4}); k == kp {
		t.Fatal("the parameters do not influence K_P")
	}

	// One K_P serves every hardware slot of the vault — the property §8 step 4
	// rests on. There is no recipient_id to pass; the same value wraps two
	// slots, and only the IK derivation below separates them.
	h := make([]byte, KeySize)
	rand.Read(h)
	a, err := HardwarePreEntangled(h, kp, vault, randomID(t))
	if err != nil {
		t.Fatal(err)
	}
	b, err := HardwarePreEntangled(h, kp, vault, randomID(t))
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("two slots of one vault share a pre")
	}

	// R4: an absent password is an error, never a degradation to the
	// token-only branch, which the header alone selects.
	for _, empty := range [][]byte{nil, {}} {
		if _, err := EntangledKey(empty, es, vault, tiny); !errors.Is(err, ErrEmptyPassword) {
			t.Fatal("empty password accepted")
		}
	}
	if _, err := EntangledKey(pw, es, vault, Argon2Params{}); !errors.Is(err, ErrParams) {
		t.Fatal("zero params accepted")
	}
	// R24 again, at the one call site that reads the header's parameters: the
	// 4 TiB argon2_m that took the development machine down is refused before
	// Argon2id allocates anything.
	if _, err := EntangledKey(pw, es, vault, Argon2Params{MemKiB: 0xFFFFFFFF, Time: 1, Threads: 4}); !errors.Is(err, ErrParams) {
		t.Fatal("4 TiB argon2_m reached Argon2id")
	}
}

// TestHardwarePreEntangled covers the branch entangle = 1 selects: the token
// enters after the KDF, as IKM = H ‖ K_P (§3.1, R3).
func TestHardwarePreEntangled(t *testing.T) {
	h := make([]byte, KeySize)
	rand.Read(h)
	vault, recip := randomID(t), randomID(t)
	kp := random32(t)

	pre, err := HardwarePreEntangled(h, kp, vault, recip)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := HardwarePreEntangled(h, kp, vault, recip); again != pre {
		t.Fatal("not deterministic")
	}
	token, _ := HardwarePreToken(h)
	if pre == token {
		t.Fatal("the entangled pre equals the token-only pre")
	}
	if p, _ := HardwarePreEntangled(h, kp, vault, randomID(t)); p == pre {
		t.Fatal("recipient_id does not influence pre")
	}
	if p, _ := HardwarePreEntangled(h, kp, randomID(t), recip); p == pre {
		t.Fatal("vault_id does not influence pre")
	}
	flipped := kp
	flipped[0] ^= 1
	if p, _ := HardwarePreEntangled(h, flipped, vault, recip); p == pre {
		t.Fatal("a one-bit change in K_P leaves pre unchanged")
	}
	if _, err := HardwarePreEntangled(h[:31], kp, vault, recip); !errors.Is(err, ErrKeySize) {
		t.Fatal("31-byte H accepted")
	}

	// The IKM order is H ‖ K_P and not the transposition, computed here from
	// the rule rather than from the package.
	info := append(append([]byte(InfoEntangle), vault[:]...), recip[:]...)
	want, err := hkdf.Key(sha256.New, append(append([]byte{}, h...), kp[:]...), nil, string(info), KeySize)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pre[:], want) {
		t.Fatalf("pre is not HKDF over H ‖ K_P:\n got  %x\n want %x", pre, want)
	}
	transposed, err := hkdf.Key(sha256.New, append(append([]byte{}, kp[:]...), h...), nil, string(info), KeySize)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(pre[:], transposed) {
		t.Fatal("K_P ‖ H gives the same pre")
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
	keys := [][]byte{MetadataKey(vmk, vault), DBKey(vmk, vault), KWK(vmk, vault), KWKIdentity(vmk, vault), KWKSecrets(vmk, vault)}
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

// TestRecoveryKeyPadding covers §7.6's padding rule for the one secret that is
// shorter than 32 bytes.
func TestRecoveryKeyPadding(t *testing.T) {
	var allOnes RecoveryKey
	for i := range allOnes {
		allOnes[i] = 0xFF
	}
	random, err := NewRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	for name, r := range map[string]RecoveryKey{"random": random, "zero": {}, "ones": allOnes} {
		pt := r.Padded()
		if !bytes.Equal(pt[:RecoveryKeySize], r[:]) {
			t.Fatalf("%s: R is not the first sixteen bytes", name)
		}
		if !bytes.Equal(pt[RecoveryKeySize:], make([]byte, KeySize-RecoveryKeySize)) {
			t.Fatalf("%s: the pad is not sixteen zero bytes", name)
		}
		back, err := RecoveryKeyFromPadded(pt)
		if err != nil || back != r {
			t.Fatalf("%s: round trip: %v", name, err)
		}
	}
	// A reader rejects the record if the tail is not zero — each of the sixteen
	// pad bytes alone.
	for i := RecoveryKeySize; i < KeySize; i++ {
		pt := random.Padded()
		pt[i] = 1
		if _, err := RecoveryKeyFromPadded(pt); !errors.Is(err, ErrSecretPadding) {
			t.Fatalf("pad byte %d accepted", i)
		}
	}
}

// TestSecretsRecords covers the secrets section (§7.6, R22, R38): one wrapping
// domain, three kinds, and an AAD built here from the rule as written rather
// than from internal/format, so the 53 bytes have two independent
// constructions.
func TestSecretsRecords(t *testing.T) {
	vmk, vault := random32(t), randomID(t)
	kek := KWKSecrets(vmk, vault)
	recipient := randomID(t)

	r, err := NewRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	escrowAAD := secretsAAD(t, vault, 1, recipient)
	w, nonce, err := WrapKey(kek, r.Padded(), escrowAAD)
	if err != nil {
		t.Fatal(err)
	}
	if len(w) != KeyWrapSize {
		t.Fatalf("a secrets ciphertext is %d bytes, want %d", len(w), KeyWrapSize)
	}
	pt, err := UnwrapKey(kek, w, nonce, escrowAAD)
	if err != nil {
		t.Fatal(err)
	}
	got, err := RecoveryKeyFromPadded(pt)
	if err != nil || got != r {
		t.Fatalf("escrow round trip: %v", err)
	}

	// A plaintext whose pad is not zero authenticates and is still refused.
	bad := r.Padded()
	bad[KeySize-1] = 0x01
	wBad, nBad, err := WrapKey(kek, bad, escrowAAD)
	if err != nil {
		t.Fatal(err)
	}
	ptBad, err := UnwrapKey(kek, wBad, nBad, escrowAAD)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RecoveryKeyFromPadded(ptBad); !errors.Is(err, ErrSecretPadding) {
		t.Fatalf("a non-zero pad opened as a recovery key: %v", err)
	}

	// The AAD separates the kinds and the ids: a record sealed for one does not
	// open as another, inside a registry that authenticates as a whole.
	var zeroID [IDSize]byte
	var historyID [IDSize]byte
	historyID[0] = 7
	for name, aad := range map[string][]byte{
		"kind 2":      secretsAAD(t, vault, 2, recipient),
		"kind 3":      secretsAAD(t, vault, 3, recipient),
		"another id":  secretsAAD(t, vault, 1, randomID(t)),
		"zero id":     secretsAAD(t, vault, 1, zeroID),
		"other vault": secretsAAD(t, randomID(t), 1, recipient),
		"history id":  secretsAAD(t, vault, 1, historyID),
		"empty":       nil,
	} {
		if _, err := UnwrapKey(kek, w, nonce, aad); !errors.Is(err, ErrAuth) {
			t.Errorf("%s opened a kind 1 record", name)
		}
	}
	// Nor does any other wrapping domain below the VMK open it (§3.2).
	for name, other := range map[string][]byte{
		"KWK":          KWK(vmk, vault),
		"KWK_identity": KWKIdentity(vmk, vault),
		"metadata key": MetadataKey(vmk, vault),
		"another VMK":  KWKSecrets(random32(t), vault),
	} {
		if _, err := UnwrapKey(other, w, nonce, escrowAAD); !errors.Is(err, ErrAuth) {
			t.Errorf("%s opened a secrets record", name)
		}
	}

	// K_P and a retired VMK are 32 bytes already: they wrap with no padding.
	for name, rec := range map[string]struct {
		kind uint8
		id   [IDSize]byte
		key  [KeySize]byte
	}{
		"entangled_key": {2, zeroID, random32(t)},
		"vmk_history":   {3, historyID, random32(t)},
	} {
		aad := secretsAAD(t, vault, rec.kind, rec.id)
		sealed, n, err := WrapKey(kek, rec.key, aad)
		if err != nil {
			t.Fatal(err)
		}
		back, err := UnwrapKey(kek, sealed, n, aad)
		if err != nil || back != rec.key {
			t.Fatalf("%s round trip: %v", name, err)
		}
	}

	// Every write of a record draws a fresh nonce (R22).
	w2, nonce2, err := WrapKey(kek, r.Padded(), escrowAAD)
	if err != nil {
		t.Fatal(err)
	}
	if nonce == nonce2 || w == w2 {
		t.Fatal("a re-wrap reused a nonce")
	}
}

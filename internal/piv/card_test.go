//go:build windows

package piv

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"fmt"
	"testing"
	"time"

	pivgo "github.com/go-piv/piv-go/v2/piv"
)

const reader = "Yubico YubiKey OTP+FIDO+CCID 0"

func open(t *testing.T) *Card {
	t.Helper()
	c, err := Open(reader)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestOpenAndClose(t *testing.T) {
	fx := newFixture(t)
	fx.dev.verified = true // some program left it verified; the probe's reset clears it
	c := open(t)
	if c.Serial() != 12345678 || c.Version() != (Version{5, 7, 4}) || c.Reader() != reader {
		t.Fatalf("card: %+v", c)
	}
	st, err := c.PINState()
	if err != nil || st.Verified || !st.RetriesKnown || st.Retries != 3 {
		t.Fatalf("fresh card: %+v %v", st, err)
	}
	// Nothing verified through this Card: no reset on Close.
	if err := c.Close(); err != nil || fx.resets != 0 || fx.dev.closed != 1 {
		t.Fatalf("close: %v resets=%d closed=%d", err, fx.resets, fx.dev.closed)
	}
	if err := c.Close(); err != nil || fx.dev.closed != 1 {
		t.Errorf("second close: %v closed=%d", err, fx.dev.closed)
	}
	if _, err := c.PINState(); !errors.Is(err, ErrClosed) {
		t.Errorf("after close: %v", err)
	}
	if _, err := c.Inspect(SlotKeyManagement); !errors.Is(err, ErrClosed) {
		t.Errorf("after close: %v", err)
	}
}

func TestOpenFailures(t *testing.T) {
	fx := newFixture(t)
	for _, want := range []error{ErrBusy, ErrNoCard, ErrNoPIVApplet, ErrNoService, ErrNoReader} {
		fx.preflightErr = fmt.Errorf("%w: probe", want)
		if _, err := Open(reader); !errors.Is(err, want) {
			t.Errorf("preflight %v: got %v", want, err)
		}
	}
	fx.preflightErr = nil
	fx.dev.version = pivgo.Version{Major: 5, Minor: 2, Patch: 7}
	if _, err := Open(reader); !errors.Is(err, ErrUnsupported) {
		t.Errorf("old firmware: %v", err)
	}
	if fx.dev.closed != 0 {
		t.Errorf("device opened before the version check")
	}
	// piv-go's answer disagreeing with the probe: the card changed.
	fx.dev.version = pivgo.Version{Major: 5, Minor: 7, Patch: 4}
	preflight = func(string) (Version, error) { return Version{5, 4, 3}, nil }
	if _, err := Open(reader); !errors.Is(err, ErrNoCard) {
		t.Errorf("changed card: %v", err)
	}
	if fx.dev.closed != 1 {
		t.Errorf("device not closed on the failed open: %d", fx.dev.closed)
	}
}

func TestInspectKeysFindAndEmpty(t *testing.T) {
	fx := newFixture(t)
	k9d := fx.dev.addKey(0x9d, pivgo.PINPolicyOnce, pivgo.TouchPolicyAlways)
	fx.dev.slots[0x9d].cert = true
	k82 := fx.dev.addKey(0x82, pivgo.PINPolicyAlways, pivgo.TouchPolicyCached)
	fx.dev.slots[0x83] = &fakeSlot{cert: true} // certificate, no key
	fx.dev.slots[0x9e] = &fakeSlot{key: k82, alg: pivgo.AlgorithmRSA2048}
	c := open(t)
	defer c.Close()

	info, err := c.Inspect(SlotKeyManagement)
	if err != nil || info.Algorithm != AlgorithmP256 || info.PINPolicy != PINPolicyOnce || info.TouchPolicy != TouchPolicyAlways ||
		info.Origin != OriginGenerated || !info.Certificate || !bytes.Equal(info.PublicKey, pubBytes(k9d)) || !info.Usable() {
		t.Fatalf("9d: %+v %v (%s)", info, err, info.WhyNotUsable())
	}
	info, err = c.Inspect(Slot(0x82))
	if err != nil || info.Usable() || info.TouchPolicy != TouchPolicyCached || info.WhyNotUsable() == "" {
		t.Fatalf("82: %+v %v", info, err)
	}
	info, err = c.Inspect(Slot(0x83))
	if err != nil || !info.Certificate || info.Algorithm != AlgorithmUnknown || info.Usable() {
		t.Fatalf("83 (certificate only): %+v %v", info, err)
	}
	if _, err := c.Inspect(Slot(0x84)); !errors.Is(err, ErrEmpty) {
		t.Fatalf("84: %v", err)
	}
	for _, s := range []Slot{0x9a, 0x9c, 0x9e, 0xf9, 0x9b, 0x80, 0x81, 0x96, 0} {
		if _, err := c.Inspect(s); !errors.Is(err, ErrForbiddenSlot) {
			t.Errorf("slot %02x: %v", uint8(s), err)
		}
		if _, err := c.Attest(s); !errors.Is(err, ErrForbiddenSlot) {
			t.Errorf("attest %02x: %v", uint8(s), err)
		}
		if _, err := c.Generate(fx.dev.mgmtKey, GenerateOptions{Slot: s, Overwrite: true}); !errors.Is(err, ErrForbiddenSlot) {
			t.Errorf("generate %02x: %v", uint8(s), err)
		}
	}
	keys, err := c.Keys()
	if err != nil || len(keys) != 3 || keys[0].Slot != 0x9d || keys[1].Slot != 0x82 || keys[2].Slot != 0x83 {
		t.Fatalf("keys: %+v %v", keys, err)
	}
	if k, err := c.Find(pubBytes(k9d)); err != nil || k.Slot != SlotKeyManagement {
		t.Errorf("find 9d: %+v %v", k, err)
	}
	if k, err := c.Find(pubBytes(k82)); err != nil || k.Slot != 0x82 {
		t.Errorf("find 82: %+v %v", k, err)
	}
	other, _ := ecdh.P256().GenerateKey(rand.Reader)
	if _, err := c.Find(other.PublicKey().Bytes()); !errors.Is(err, ErrNoKey) {
		t.Errorf("find unknown: %v", err)
	}
	if _, err := c.Find([]byte{4, 5}); !errors.Is(err, ErrParams) {
		t.Errorf("find short: %v", err)
	}
	// 9d, 82 and 83 are occupied: the first empty slot is 84.
	if s, err := c.FirstEmptySlot(); err != nil || s != 0x84 {
		t.Errorf("first empty: %v %v", s, err)
	}
	if bad := fx.dev.namedOutsideAllowlist(); len(bad) != 0 {
		t.Errorf("the package named key slots outside the allowlist: %x", bad)
	}
}

func TestUsablePredicate(t *testing.T) {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	base := func() KeyInfo {
		return KeyInfo{Algorithm: AlgorithmP256, PublicKey: pubBytes(k), ecdsaPub: &k.PublicKey, TouchPolicy: TouchPolicyAlways, PINPolicy: PINPolicyOnce}
	}
	if !base().Usable() {
		t.Fatal("base not usable")
	}
	cases := map[string]func(*KeyInfo){
		"cached touch":  func(k *KeyInfo) { k.TouchPolicy = TouchPolicyCached },
		"no touch":      func(k *KeyInfo) { k.TouchPolicy = TouchPolicyNever },
		"unknown touch": func(k *KeyInfo) { k.TouchPolicy = TouchPolicyUnknown },
		"pin never":     func(k *KeyInfo) { k.PINPolicy = PINPolicyNever },
		"bio":           func(k *KeyInfo) { k.PINPolicy = PINPolicyMatchOnce },
		"bio always":    func(k *KeyInfo) { k.PINPolicy = PINPolicyMatchAlways },
		"P-384":         func(k *KeyInfo) { k.Algorithm = AlgorithmP384 },
		"RSA":           func(k *KeyInfo) { k.Algorithm = AlgorithmRSA2048; k.PublicKey = nil; k.ecdsaPub = nil },
		"no pubkey":     func(k *KeyInfo) { k.PublicKey = nil; k.ecdsaPub = nil },
		"cert only":     func(k *KeyInfo) { *k = KeyInfo{Certificate: true} },
	}
	for name, mut := range cases {
		k := base()
		mut(&k)
		if k.Usable() {
			t.Errorf("%s: usable", name)
		}
		if k.WhyNotUsable() == "" {
			t.Errorf("%s: no reason", name)
		}
	}
	// Always is the other acceptable PIN policy.
	ka := base()
	ka.PINPolicy = PINPolicyAlways
	if !ka.Usable() {
		t.Error("pin always: not usable")
	}
}

func TestSlots(t *testing.T) {
	all := AllSlots()
	if len(all) != 21 || all[0] != SlotKeyManagement || all[1] != 0x82 || all[20] != 0x95 {
		t.Fatalf("all: %v", all)
	}
	for b := 0; b < 256; b++ {
		s, ok := ParseSlot(byte(b))
		want := b == 0x9d || (b >= 0x82 && b <= 0x95)
		if ok != want || s.Allowed() != want {
			t.Errorf("%02x: ok=%v", b, ok)
		}
		if ok && (s.Retired() != (b != 0x9d)) {
			t.Errorf("%02x: retired=%v", b, s.Retired())
		}
	}
	if SlotKeyManagement.String() != "9d" || Slot(0x82).String() != "82" {
		t.Error("strings")
	}
}

func TestGenerate(t *testing.T) {
	fx := newFixture(t)
	c := open(t)
	defer c.Close()
	// Bad arguments never reach the token.
	if _, err := c.Generate([]byte{1, 2, 3}, GenerateOptions{Slot: SlotKeyManagement}); !errors.Is(err, ErrParams) {
		t.Errorf("short key: %v", err)
	}
	if _, err := c.Generate(fx.dev.mgmtKey, GenerateOptions{Slot: SlotKeyManagement, PINPolicy: PINPolicyNever}); !errors.Is(err, ErrParams) {
		t.Errorf("pin never: %v", err)
	}
	if _, err := c.Generate(fx.dev.mgmtKey, GenerateOptions{Slot: SlotKeyManagement, PINPolicy: PINPolicyMatchOnce}); !errors.Is(err, ErrParams) {
		t.Errorf("bio: %v", err)
	}
	if _, err := c.Generate(fx.dev.mgmtKey, GenerateOptions{}); !errors.Is(err, ErrForbiddenSlot) {
		t.Errorf("zero slot: %v", err)
	}
	if fx.dev.generates != 0 {
		t.Fatal("token asked to generate on bad arguments")
	}
	// A wrong management key.
	if _, err := c.Generate(bytes.Repeat([]byte{9}, 32), GenerateOptions{Slot: SlotKeyManagement}); !errors.Is(err, ErrManagementKey) {
		t.Errorf("wrong key: %v", err)
	}
	// The real thing: PIN once by default, touch always, into 9d.
	info, err := c.Generate(fx.dev.mgmtKey, GenerateOptions{Slot: SlotKeyManagement})
	if err != nil || !info.Usable() || info.PINPolicy != PINPolicyOnce || info.TouchPolicy != TouchPolicyAlways || info.Slot != SlotKeyManagement {
		t.Fatalf("generate: %+v %v", info, err)
	}
	if s := fx.dev.slots[0x9d]; s.touchPolicy != pivgo.TouchPolicyAlways || s.pinPolicy != pivgo.PINPolicyOnce {
		t.Errorf("policies on the card: %+v", s)
	}
	// Occupied now: refused without Overwrite, with what is there.
	_, err = c.Generate(fx.dev.mgmtKey, GenerateOptions{Slot: SlotKeyManagement, PINPolicy: PINPolicyAlways})
	var oe *OccupiedError
	if !errors.As(err, &oe) || !errors.Is(err, ErrOccupied) || oe.Key.Slot != SlotKeyManagement || !bytes.Equal(oe.Key.PublicKey, info.PublicKey) {
		t.Fatalf("occupied: %v", err)
	}
	// A certificate-only slot is occupied too.
	fx.dev.slots[0x82] = &fakeSlot{cert: true}
	if _, err := c.Generate(fx.dev.mgmtKey, GenerateOptions{Slot: 0x82}); !errors.As(err, &oe) || oe.Key.Algorithm != AlgorithmUnknown || !oe.Key.Certificate {
		t.Fatalf("certificate-only occupied: %v", err)
	}
	// With Overwrite, PIN policy always.
	info2, err := c.Generate(fx.dev.mgmtKey, GenerateOptions{Slot: SlotKeyManagement, PINPolicy: PINPolicyAlways, Overwrite: true})
	if err != nil || info2.PINPolicy != PINPolicyAlways || bytes.Equal(info2.PublicKey, info.PublicKey) {
		t.Fatalf("overwrite: %+v %v", info2, err)
	}
	// The management-key authentication verified something: reset on Close.
	if err := c.Close(); err != nil || fx.resets != 1 {
		t.Errorf("close after generate: %v resets=%d", err, fx.resets)
	}
	if bad := fx.dev.namedOutsideAllowlist(); len(bad) != 0 {
		t.Errorf("named outside the allowlist: %x", bad)
	}
}

func TestProtectedManagementKey(t *testing.T) {
	fx := newFixture(t)
	c := open(t)
	defer c.Close()
	if _, err := c.ProtectedManagementKey(""); !errors.Is(err, ErrParams) {
		t.Errorf("empty: %v", err)
	}
	if _, err := c.ProtectedManagementKey("123456789"); !errors.Is(err, ErrParams) {
		t.Errorf("long: %v", err)
	}
	if fx.dev.verifies != 0 {
		t.Fatal("a refused PIN reached the token")
	}
	if _, err := c.ProtectedManagementKey("123456"); !errors.Is(err, ErrNoProtectedKey) {
		t.Errorf("absent: %v", err)
	}
	fx.dev.protected = bytes.Repeat([]byte{3}, 32)
	// A wrong PIN costs one retry and is reported with the count.
	_, err := c.ProtectedManagementKey("000000")
	var pe *PINError
	if !errors.As(err, &pe) || pe.Retries != 2 {
		t.Fatalf("wrong PIN: %v", err)
	}
	mk, err := c.ProtectedManagementKey("123456")
	if err != nil || !bytes.Equal(mk, fx.dev.protected) {
		t.Fatalf("read: %x %v", mk, err)
	}
	if fx.dev.retries != 3 {
		t.Errorf("a correct PIN did not restore the counter: %d", fx.dev.retries)
	}
	// It is usable for generation.
	if _, err := c.Generate(mk, GenerateOptions{Slot: SlotKeyManagement}); !errors.Is(err, ErrManagementKey) {
		// the fake's management key is a different one; the point is the plumbing
		t.Errorf("generate with protected key: %v", err)
	}
	fx.dev.mgmtKey = fx.dev.protected
	if _, err := c.Generate(mk, GenerateOptions{Slot: SlotKeyManagement}); err != nil {
		t.Errorf("generate with protected key: %v", err)
	}
	// Blocked PIN.
	fx.dev.retries = 0
	if _, err := c.ProtectedManagementKey("123456"); !errors.Is(err, ErrPINBlocked) {
		t.Errorf("blocked: %v", err)
	}
	if v := DefaultManagementKey(); len(v) != 24 || &v[0] == &pivgo.DefaultManagementKey[0] {
		t.Error("DefaultManagementKey must be a copy")
	}
}

func TestTokenCeremonyOnce(t *testing.T) {
	fx := newFixture(t)
	k := fx.dev.addKey(0x9d, pivgo.PINPolicyOnce, pivgo.TouchPolicyAlways)
	c := open(t)
	defer c.Close()
	if _, err := c.Token(pubBytes(k), nil); !errors.Is(err, ErrParams) {
		t.Fatalf("nil prompter: %v", err)
	}
	p := &testPrompter{pins: []string{"123456"}}
	tok, err := c.Token(pubBytes(k), p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(tok.PublicKey(), pubBytes(k)) || tok.Info().Slot != SlotKeyManagement {
		t.Fatal("token identity")
	}
	eph, _ := ecdh.P256().GenerateKey(rand.Reader)
	want, _ := eph.ECDH(mustECDH(k))
	// A bad epk never reaches the token.
	if _, err := tok.ECDH([]byte{1, 2, 3}); !errors.Is(err, ErrParams) {
		t.Fatalf("bad epk: %v", err)
	}
	bad := bytes.Clone(eph.PublicKey().Bytes())
	bad[10] ^= 1
	if _, err := tok.ECDH(bad); !errors.Is(err, ErrParams) || fx.dev.ecdhCalls != 0 {
		t.Fatalf("off-curve epk: %v calls=%d", err, fx.dev.ecdhCalls)
	}
	got, err := tok.ECDH(eph.PublicKey().Bytes())
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("ecdh: %x %v", got, err)
	}
	// The ceremony: retries shown, one VERIFY, then the touch prompt, and
	// piv-go told not to prompt.
	if len(p.statuses) != 1 || p.statuses[0].Verified || !p.statuses[0].RetriesKnown || p.statuses[0].Retries != 3 {
		t.Errorf("PIN prompt status: %+v", p.statuses)
	}
	if fx.dev.verifies != 1 || len(p.touches) != 1 || p.touches[0] != (TouchRequest{Slot: SlotKeyManagement, N: 1, PINAsked: true}) {
		t.Errorf("verifies=%d touches=%+v", fx.dev.verifies, p.touches)
	}
	if fx.dev.lastAuth.PINPolicy != pivgo.PINPolicyNever || fx.dev.lastAuth.PINPrompt != nil || fx.dev.lastAuth.PIN != "" {
		t.Errorf("piv-go was left to handle the PIN: %+v", fx.dev.lastAuth)
	}
	// Second operation on a verified card: no PIN, touch only.
	eph2, _ := ecdh.P256().GenerateKey(rand.Reader)
	if _, err := tok.ECDH(eph2.PublicKey().Bytes()); err != nil {
		t.Fatal(err)
	}
	if len(p.statuses) != 1 || fx.dev.verifies != 1 || len(p.touches) != 2 || p.touches[1] != (TouchRequest{Slot: SlotKeyManagement, N: 2}) {
		t.Errorf("second op: statuses=%d verifies=%d touches=%+v", len(p.statuses), fx.dev.verifies, p.touches)
	}
	// Close resets the card, because a PIN went through.
	if err := c.Close(); err != nil || fx.resets != 1 || fx.dev.verified {
		t.Errorf("close: %v resets=%d verified=%v", err, fx.resets, fx.dev.verified)
	}
	if _, err := tok.ECDH(eph.PublicKey().Bytes()); !errors.Is(err, ErrClosed) {
		t.Errorf("token after close: %v", err)
	}
	if bad := fx.dev.namedOutsideAllowlist(); len(bad) != 0 {
		t.Errorf("named outside the allowlist: %x", bad)
	}
}

func mustECDH(k interface {
	ECDH() (*ecdh.PrivateKey, error)
}) *ecdh.PublicKey {
	p, err := k.ECDH()
	if err != nil {
		panic(err)
	}
	return p.PublicKey()
}

func TestTokenCeremonyAlways(t *testing.T) {
	fx := newFixture(t)
	k := fx.dev.addKey(0x9d, pivgo.PINPolicyAlways, pivgo.TouchPolicyAlways)
	c := open(t)
	defer c.Close()
	p := &testPrompter{pins: []string{"123456", "123456"}}
	tok, err := c.Token(pubBytes(k), p)
	if err != nil {
		t.Fatal(err)
	}
	eph, _ := ecdh.P256().GenerateKey(rand.Reader)
	for i := 1; i <= 2; i++ {
		if _, err := tok.ECDH(eph.PublicKey().Bytes()); err != nil {
			t.Fatalf("op %d: %v", i, err)
		}
	}
	// A PIN per operation; the second prompt sees a verified card and must
	// not claim "0 retries".
	if fx.dev.verifies != 2 || len(p.statuses) != 2 || !p.statuses[1].Verified || p.statuses[1].RetriesKnown {
		t.Errorf("verifies=%d statuses=%+v", fx.dev.verifies, p.statuses)
	}
	for _, st := range p.statuses {
		if st.RetriesKnown && st.Retries == 0 {
			t.Errorf("a prompt was shown 0 retries: %+v", st)
		}
	}
}

func TestTokenErrors(t *testing.T) {
	fx := newFixture(t)
	k := fx.dev.addKey(0x9d, pivgo.PINPolicyOnce, pivgo.TouchPolicyAlways)
	kc := fx.dev.addKey(0x82, pivgo.PINPolicyOnce, pivgo.TouchPolicyCached)
	c := open(t)
	defer c.Close()
	eph, _ := ecdh.P256().GenerateKey(rand.Reader)
	epk := eph.PublicKey().Bytes()

	// Unusable keys are refused at Token time, with the reason.
	if _, err := c.Token(pubBytes(kc), &testPrompter{}); !errors.Is(err, ErrNotUsable) {
		t.Fatalf("cached: %v", err)
	}
	// Cancel: no VERIFY, no touch prompt.
	p := &testPrompter{cancel: errors.New("user closed the dialog")}
	tok, _ := c.Token(pubBytes(k), p)
	if _, err := tok.ECDH(epk); !errors.Is(err, ErrCancelled) || fx.dev.verifies != 0 || len(p.touches) != 0 {
		t.Fatalf("cancel: %v verifies=%d touches=%d", err, fx.dev.verifies, len(p.touches))
	}
	// Wrong PIN: one attempt, the count, no touch prompt, no ECDH.
	p = &testPrompter{pins: []string{"111111"}}
	tok, _ = c.Token(pubBytes(k), p)
	_, err := tok.ECDH(epk)
	var pe *PINError
	if !errors.As(err, &pe) || pe.Retries != 2 || fx.dev.verifies != 1 || len(p.touches) != 0 || fx.dev.ecdhCalls != 0 {
		t.Fatalf("wrong PIN: %v verifies=%d touches=%d ecdh=%d", err, fx.dev.verifies, len(p.touches), fx.dev.ecdhCalls)
	}
	// An over-long PIN is refused before the token sees it.
	p = &testPrompter{pins: []string{"123456789"}}
	tok, _ = c.Token(pubBytes(k), p)
	if _, err := tok.ECDH(epk); !errors.Is(err, ErrParams) || fx.dev.verifies != 1 {
		t.Fatalf("long PIN: %v verifies=%d", err, fx.dev.verifies)
	}
	// No touch: 6982 with the card verified is ErrTouch.
	fx.dev.touch = false
	p = &testPrompter{pins: []string{"123456"}}
	tok, _ = c.Token(pubBytes(k), p)
	if _, err := tok.ECDH(epk); !errors.Is(err, ErrTouch) {
		t.Fatalf("no touch: %v", err)
	}
	fx.dev.touch = true
	// Blocked PIN: no prompt at all.
	fx.dev.reset()
	fx.dev.retries = 0
	p = &testPrompter{pins: []string{"123456"}}
	tok, _ = c.Token(pubBytes(k), p)
	if _, err := tok.ECDH(epk); !errors.Is(err, ErrPINBlocked) || len(p.statuses) != 0 {
		t.Fatalf("blocked: %v prompts=%d", err, len(p.statuses))
	}
	fx.dev.retries = 3
	// The card going away mid-operation.
	fx.dev.failECDH = errors.New("command failed: transmitting request: the smart card has been removed, so further communication is not possible")
	p = &testPrompter{pins: []string{"123456"}}
	tok, _ = c.Token(pubBytes(k), p)
	if _, err := tok.ECDH(epk); !errors.Is(err, ErrNoCard) {
		t.Fatalf("removed: %v", err)
	}
	fx.dev.failECDH = nil
	// The operation budget.
	p = &testPrompter{pins: []string{"123456"}}
	tok, _ = c.Token(pubBytes(k), p)
	for i := 0; i < MaxOperations; i++ {
		if _, err := tok.ECDH(epk); err != nil {
			t.Fatalf("op %d: %v", i, err)
		}
	}
	if _, err := tok.ECDH(epk); !errors.Is(err, ErrTooManyOperations) {
		t.Fatalf("budget: %v", err)
	}
	if len(p.touches) != MaxOperations || p.touches[MaxOperations-1].N != MaxOperations {
		t.Errorf("touch ordinals: %+v", p.touches)
	}
}

func TestPINRequiredAfterTheFact(t *testing.T) {
	// A card that answers 6982 and then says "not verified" to the empty
	// VERIFY: the package reports the PIN, not the touch.
	fx := newFixture(t)
	k := fx.dev.addKey(0x9d, pivgo.PINPolicyOnce, pivgo.TouchPolicyAlways)
	c := open(t)
	defer c.Close()
	p := &testPrompter{pins: []string{"123456"}}
	tok, _ := c.Token(pubBytes(k), p)
	eph, _ := ecdh.P256().GenerateKey(rand.Reader)
	fx.dev.failECDH = &unverifyingError{fx.dev}
	if _, err := tok.ECDH(eph.PublicKey().Bytes()); !errors.Is(err, ErrPINRequired) {
		t.Fatalf("got %v", err)
	}
}

// unverifyingError is a 6982 that also drops the card's verified state, as
// a card whose PIN policy is stricter than its metadata would.
type unverifyingError struct{ f *fakeDevice }

func (e *unverifyingError) Error() string {
	e.f.verified = false
	return "command failed: smart card error 6982: security status not satisfied"
}

func TestCloseResetFailure(t *testing.T) {
	fx := newFixture(t)
	k := fx.dev.addKey(0x9d, pivgo.PINPolicyOnce, pivgo.TouchPolicyAlways)
	c := open(t)
	tok, _ := c.Token(pubBytes(k), &testPrompter{pins: []string{"123456"}})
	eph, _ := ecdh.P256().GenerateKey(rand.Reader)
	if _, err := tok.ECDH(eph.PublicKey().Bytes()); err != nil {
		t.Fatal(err)
	}
	fx.resetFails = true
	if err := c.Close(); !errors.Is(err, ErrResetFailed) || !c.ResetFailed() {
		t.Fatalf("close: %v failed=%v", err, c.ResetFailed())
	}
	if err := c.Close(); err != nil || fx.resets != 1 {
		t.Errorf("second close retried the reset: %v %d", err, fx.resets)
	}
}

// TestCloseAfterGenerateWithFailedReset: the management-key authentication
// cannot be asked about, so a reset that did not happen after a Generate is
// reported even though no PIN is left on the card.
func TestCloseAfterGenerateWithFailedReset(t *testing.T) {
	fx := newFixture(t)
	c := open(t)
	if _, err := c.Generate(fx.dev.mgmtKey, GenerateOptions{Slot: SlotKeyManagement}); err != nil {
		t.Fatal(err)
	}
	fx.resetFails = true
	fx.dev.verified = false
	if err := c.Close(); !errors.Is(err, ErrResetFailed) || !c.ResetFailed() {
		t.Fatalf("close after generate: %v failed=%v", err, c.ResetFailed())
	}
	// A PIN-only dirty Card whose failed reset finds no PIN left is clean.
	fx = newFixture(t)
	k := fx.dev.addKey(0x9d, pivgo.PINPolicyOnce, pivgo.TouchPolicyAlways)
	c = open(t)
	tok, _ := c.Token(pubBytes(k), &testPrompter{pins: []string{"123456"}})
	eph, _ := ecdh.P256().GenerateKey(rand.Reader)
	if _, err := tok.ECDH(eph.PublicKey().Bytes()); err != nil {
		t.Fatal(err)
	}
	fx.resetFails = true
	fx.dev.verified = false // the card lost the state on its own (power-down)
	if err := c.Close(); err != nil || c.ResetFailed() {
		t.Fatalf("close with no PIN left: %v failed=%v", err, c.ResetFailed())
	}
}

// TestCloseWaitsForInFlightVerify is the blocker the review found: a Close
// issued while the PIN prompt is open must wait, and then reset, because
// the operation verifies the PIN after Close was called.
func TestCloseDuringPromptEndsTheOperation(t *testing.T) {
	// The prompt runs with the operation lock released (DESIGN.md §11 trap
	// 25), so a Close meanwhile — the user cancelled, the vault locked —
	// does not wait for the user: the operation finds the Card closed once
	// the prompt returns, and nothing was verified, so nothing is reset.
	fx := newFixture(t)
	k := fx.dev.addKey(0x9d, pivgo.PINPolicyOnce, pivgo.TouchPolicyAlways)
	c := open(t)
	inPrompt := make(chan struct{})
	release := make(chan struct{})
	tok, _ := c.Token(pubBytes(k), &blockingPrompter{inPrompt: inPrompt, release: release})
	eph, _ := ecdh.P256().GenerateKey(rand.Reader)
	opDone := make(chan error, 1)
	go func() {
		_, err := tok.ECDH(eph.PublicKey().Bytes())
		opDone <- err
	}()
	<-inPrompt // the operation is waiting for the user, holding nothing
	if err := c.Close(); err != nil {
		t.Fatalf("close during the prompt: %v", err)
	}
	if fx.resets != 0 {
		t.Fatal("Close reset a card nothing was verified on")
	}
	close(release) // the user types the PIN into a closed Card
	if err := <-opDone; !errors.Is(err, ErrClosed) {
		t.Fatalf("operation after the close: %v", err)
	}
	if fx.dev.verifies != 0 {
		t.Fatalf("a PIN was sent after the close: verifies=%d", fx.dev.verifies)
	}
}

// TestForeignVerifiedStateIsNotTrusted: a Card that finds the card verified
// without having verified it (a probe reset that did not take, another
// program's VERIFY) asks for the PIN once anyway.
func TestForeignVerifiedStateIsNotTrusted(t *testing.T) {
	fx := newFixture(t)
	fx.noProbeReset = true
	fx.dev.verified = true
	k := fx.dev.addKey(0x9d, pivgo.PINPolicyOnce, pivgo.TouchPolicyAlways)
	c := open(t)
	defer c.Close()
	p := &testPrompter{pins: []string{"123456"}}
	tok, _ := c.Token(pubBytes(k), p)
	eph, _ := ecdh.P256().GenerateKey(rand.Reader)
	if _, err := tok.ECDH(eph.PublicKey().Bytes()); err != nil {
		t.Fatal(err)
	}
	if len(p.statuses) != 1 || !p.statuses[0].Verified || p.statuses[0].RetriesKnown || fx.dev.verifies != 1 {
		t.Fatalf("foreign state: prompts=%+v verifies=%d", p.statuses, fx.dev.verifies)
	}
	// Now it is ours: the second operation needs no PIN.
	if _, err := tok.ECDH(eph.PublicKey().Bytes()); err != nil || fx.dev.verifies != 1 || len(p.statuses) != 1 {
		t.Fatalf("second op: %v verifies=%d prompts=%d", err, fx.dev.verifies, len(p.statuses))
	}
}

// TestGenerateTransportError: a card pulled during the management-key
// handshake is ErrNoCard, not "management key refused".
func TestGenerateTransportError(t *testing.T) {
	fx := newFixture(t)
	c := open(t)
	defer c.Close()
	fx.dev.failGenerate = errors.New("get auth challenge: command failed: transmitting request: the smart card has been removed, so further communication is not possible")
	if _, err := c.Generate(fx.dev.mgmtKey, GenerateOptions{Slot: SlotKeyManagement}); !errors.Is(err, ErrNoCard) || errors.Is(err, ErrManagementKey) {
		t.Fatalf("removed: %v", err)
	}
	fx.dev.failGenerate = errors.New("command failed: smart card error 6982: security status not satisfied")
	if _, err := c.Generate(fx.dev.mgmtKey, GenerateOptions{Slot: SlotKeyManagement}); !errors.Is(err, ErrManagementKey) {
		t.Fatalf("6982 on the management-key path: %v", err)
	}
	fx.dev.failGenerate = fmt.Errorf("verify: %w", pivgo.AuthErr{Retries: 0})
	if _, err := c.Generate(fx.dev.mgmtKey, GenerateOptions{Slot: SlotKeyManagement}); !errors.Is(err, ErrManagementKey) || errors.Is(err, ErrPINBlocked) {
		t.Fatalf("63xx on the management-key path must not read as a PIN: %v", err)
	}
}

func TestConcurrentOperationRefused(t *testing.T) {
	fx := newFixture(t)
	k := fx.dev.addKey(0x9d, pivgo.PINPolicyOnce, pivgo.TouchPolicyAlways)
	c := open(t)
	defer c.Close()
	inPrompt := make(chan struct{})
	release := make(chan struct{})
	p := &blockingPrompter{inPrompt: inPrompt, release: release}
	tok, _ := c.Token(pubBytes(k), p)
	eph, _ := ecdh.P256().GenerateKey(rand.Reader)
	done := make(chan error, 1)
	go func() {
		_, err := tok.ECDH(eph.PublicKey().Bytes())
		done <- err
	}()
	<-inPrompt
	// A probe passes while the prompt stands: it is what keeps the
	// connection alive. A second ceremony does not.
	if _, err := c.PINState(); err != nil {
		t.Errorf("probe during the prompt: %v", err)
	}
	tok2, _ := c.Token(pubBytes(k), &testPrompter{pins: []string{"123456"}})
	if _, err := tok2.ECDH(eph.PublicKey().Bytes()); !errors.Is(err, ErrInUse) {
		t.Errorf("second ceremony during the prompt: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// With the prompt over, the card is free for the next ceremony.
	if _, err := tok2.ECDH(eph.PublicKey().Bytes()); err != nil {
		t.Fatalf("after the prompt: %v", err)
	}
	// Two ceremonies, one VERIFY: the second found the card verified by us.
	if fx.dev.verifies != 1 {
		t.Errorf("verifies=%d", fx.dev.verifies)
	}
}

type blockingPrompter struct {
	inPrompt, release chan struct{}
}

func (p *blockingPrompter) PIN(PINStatus) (string, error) {
	close(p.inPrompt)
	<-p.release
	return "123456", nil
}

func (p *blockingPrompter) Touch(TouchRequest) {}

func TestAttestPlumbing(t *testing.T) {
	fx := newFixture(t)
	fx.dev.addKey(0x9d, pivgo.PINPolicyOnce, pivgo.TouchPolicyAlways)
	c := open(t)
	defer c.Close()
	// The fake cannot produce a chain to Yubico's roots (Verify needs them;
	// piv-go embeds them): the package must say so rather than hand back an
	// unchecked certificate. The real chain is checked by the hardware test.
	if _, err := c.Attest(SlotKeyManagement); !errors.Is(err, ErrAttestation) {
		t.Errorf("unverifiable: %v", err)
	}
	if _, err := c.Attest(Slot(0x82)); !errors.Is(err, ErrEmpty) {
		t.Errorf("empty: %v", err)
	}
}

func TestErrorMapping(t *testing.T) {
	cases := []struct {
		in   error
		want error
	}{
		{errors.New("connecting to smart card: the smart card cannot be accessed because of other connections outstanding"), ErrBusy},
		{errors.New("connecting to smart card: the operation requires a Smart Card, but no Smart Card is currently in the device"), ErrNoCard},
		{errors.New("connecting to pcsc: the Smart card resource manager is not running"), ErrNoService},
		{errors.New("connecting to smart card: cannot find a smart card reader"), ErrNoReader},
		{errors.New("transmitting request: power has been removed from the smart card, so that further communication is not possible"), ErrNoCard},
		{fmt.Errorf("verify pin: %w", pivgo.AuthErr{Retries: 0}), ErrPINBlocked},
		{fmt.Errorf("x: %w", ErrTouch), ErrTouch},
		// DESIGN.md §11 trap 26: a pulled key answers with Win32 codes
		// first, which piv-go prints by number; the facility's own "not
		// talking" codes it prints by text.
		{errors.New("command failed: transmitting request: unknown pcsc return code 0x0000001f"), ErrNoCard},
		{errors.New("transmitting request: unknown pcsc return code 0x00000016"), ErrNoCard},
		{errors.New("transmitting request: the reader or smart card is not ready to accept commands"), ErrNoCard},
		{errors.New("transmitting request: an internal communications error has been detected"), ErrNoCard},
		{errors.New("transmitting request: an unexpected card error has occurred"), ErrNoCard},
		{errors.New("transmitting request: a communications error with the smart card has been detected. More.."), ErrNoCard},
		{errors.New("transmitting request: the action was cancelled by the system, presumably to log off or shut down"), ErrNoService},
		{errors.New("transmitting request: the operation has been aborted to allow the server application to exit"), ErrNoService},
	}
	for _, tc := range cases {
		if got := mapErr(tc.in); !errors.Is(got, tc.want) {
			t.Errorf("%v: got %v", tc.in, got)
		}
	}
	var pe *PINError
	if got := mapErr(fmt.Errorf("verify pin: %w", pivgo.AuthErr{Retries: 2})); !errors.As(got, &pe) || pe.Retries != 2 {
		t.Errorf("auth err: %v", got)
	}
	if sw, ok := statusWord(errors.New("command failed: smart card error 6982: security status not satisfied")); !ok || sw != 0x6982 {
		t.Errorf("status word: %x %v", sw, ok)
	}
	if _, ok := statusWord(errors.New("nothing")); ok {
		t.Error("status word from nothing")
	}
	if got := mapErr(errors.New("something else")); got == nil || errors.Is(got, ErrNoCard) {
		t.Errorf("unknown: %v", got)
	}
	// A facility code piv-go has no text for names this program's own
	// mistake (an invalid handle), never a removal.
	if got := mapErr(errors.New("transmitting request: unknown pcsc return code 0x80100003")); got == nil || errors.Is(got, ErrNoCard) || errors.Is(got, ErrNoService) {
		t.Errorf("invalid handle classified as a removal: %v", got)
	}
}

// The host resets an idle exclusive connection (DESIGN.md §11 trap 25). A
// reset met at the VERIFY — the PIN typed slowly — is reconnected and the
// operation repeated with the PIN already collected: one prompt, one
// consumed VERIFY, and the agreement.
func TestTokenReconnectsAfterReset(t *testing.T) {
	fx := newFixture(t)
	k := fx.dev.addKey(0x9d, pivgo.PINPolicyOnce, pivgo.TouchPolicyAlways)
	c := open(t)
	defer c.Close()
	p := &testPrompter{pins: []string{"123456"}}
	tok, err := c.Token(pubBytes(k), p)
	if err != nil {
		t.Fatal(err)
	}
	eph, _ := ecdh.P256().GenerateKey(rand.Reader)
	want, _ := eph.ECDH(mustECDH(k))
	fx.dev.resetNext = true // the reset lands on the VERIFY, after the prompt
	got, err := tok.ECDH(eph.PublicKey().Bytes())
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("ecdh across a reset: %x %v", got, err)
	}
	if len(p.statuses) != 1 || fx.dev.verifies != 1 || fx.dev.reopens != 1 || len(p.touches) != 1 {
		t.Errorf("prompts=%d verifies=%d reopens=%d touches=%d", len(p.statuses), fx.dev.verifies, fx.dev.reopens, len(p.touches))
	}
	// A reset met by a plain query is reconnected too, with nothing asked.
	fx.dev.resetNext = true
	st, err := c.PINState()
	if err != nil || fx.dev.reopens != 2 || st.Verified {
		t.Fatalf("PIN state across a reset: %+v %v reopens=%d", st, err, fx.dev.reopens)
	}
	// The card that answers after a reconnect must be the same card.
	fx.dev.resetNext = true
	fx.dev.serial = 99
	if _, err := c.PINState(); !errors.Is(err, ErrNoCard) {
		t.Fatalf("another card after the reset: %v", err)
	}
}

// The app ends a prompt with the key's own error when its probe finds the
// key gone, and only that identity — surviving the trip through this
// package — sends its flow back to waiting rather than to a cancel.
func TestPrompterErrorKeepsItsIdentity(t *testing.T) {
	fx := newFixture(t)
	k := fx.dev.addKey(0x9d, pivgo.PINPolicyOnce, pivgo.TouchPolicyAlways)
	c := open(t)
	defer c.Close()
	gone := errors.New("the key went away")
	tok, _ := c.Token(pubBytes(k), &testPrompter{cancel: gone})
	eph, _ := ecdh.P256().GenerateKey(rand.Reader)
	_, err := tok.ECDH(eph.PublicKey().Bytes())
	if !errors.Is(err, ErrCancelled) || !errors.Is(err, gone) {
		t.Fatalf("the prompter's error was lost: %v", err)
	}
	// A plain cancel is not wrapped in itself.
	tok2, _ := c.Token(pubBytes(k), &testPrompter{cancel: ErrCancelled})
	if _, err := tok2.ECDH(eph.PublicKey().Bytes()); err != ErrCancelled {
		t.Fatalf("a cancel: %v", err)
	}
	if fx.dev.verifies != 0 {
		t.Fatalf("verifies=%d", fx.dev.verifies)
	}
}

// The operation resuming after its prompt waits for a probe on the card
// rather than refusing: the caller's keep-alive may be on it that instant.
func TestResumeAfterPromptWaitsForAProbe(t *testing.T) {
	fx := newFixture(t)
	k := fx.dev.addKey(0x9d, pivgo.PINPolicyOnce, pivgo.TouchPolicyAlways)
	c := open(t)
	defer c.Close()
	inPrompt, release := make(chan struct{}), make(chan struct{})
	tok, _ := c.Token(pubBytes(k), &blockingPrompter{inPrompt: inPrompt, release: release})
	eph, _ := ecdh.P256().GenerateKey(rand.Reader)
	done := make(chan error, 1)
	go func() {
		_, err := tok.ECDH(eph.PublicKey().Bytes())
		done <- err
	}()
	<-inPrompt
	c.op.Lock() // a probe in flight
	close(release)
	time.Sleep(30 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("resumed under the probe: %v", err)
	default:
	}
	c.op.Unlock()
	if err := <-done; err != nil {
		t.Fatalf("after the probe: %v", err)
	}
}

// A reconnect probes the reader first, as Open does (DESIGN.md §11 trap
// 24), and a probe that fails is the reconnect failing.
func TestReconnectProbesFirst(t *testing.T) {
	fx := newFixture(t)
	c := open(t)
	defer c.Close()
	fx.dev.resetNext = true
	if _, err := c.PINState(); err != nil {
		t.Fatal(err)
	}
	if fx.preflights != 2 || fx.dev.reopens != 1 {
		t.Fatalf("preflights=%d reopens=%d", fx.preflights, fx.dev.reopens)
	}
	fx.dev.resetNext = true
	fx.preflightErr = fmt.Errorf("%w: pulled", ErrNoCard)
	if _, err := c.PINState(); !errors.Is(err, ErrNoCard) {
		t.Fatalf("probe failed on the reconnect: %v", err)
	}
	if fx.dev.reopens != 1 {
		t.Fatalf("piv-go was handed a reader that failed the probe: reopens=%d", fx.dev.reopens)
	}
}

// A reconnect that fails leaves a Card that says so on every call — the
// card is gone as far as it is concerned — rather than one that
// transmits on the handle the reset closed; its Close disconnects
// nothing twice and resets nothing.
func TestFailedReconnectLosesTheCard(t *testing.T) {
	old := reopenRetries
	reopenRetries = 1
	defer func() { reopenRetries = old }()
	fx := newFixture(t)
	fx.dev.addKey(0x9d, pivgo.PINPolicyOnce, pivgo.TouchPolicyAlways)
	c := open(t)
	fx.dev.resetNext = true
	fx.openErr = fmt.Errorf("%w: sharing violation", ErrBusy)
	if _, err := c.PINState(); !errors.Is(err, ErrNoCard) {
		t.Fatalf("reset with no reopen: %v", err)
	}
	if fx.preflights != 3 {
		t.Fatalf("a busy reopen is retried once more: preflights=%d", fx.preflights)
	}
	fx.openErr = nil
	if _, err := c.Inspect(SlotKeyManagement); !errors.Is(err, ErrNoCard) {
		t.Fatalf("a lost card answered: %v", err)
	}
	if fx.dev.closed != 1 {
		t.Fatalf("closes=%d", fx.dev.closed)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close of a lost card: %v", err)
	}
	if fx.dev.closed != 1 || fx.resets != 0 {
		t.Fatalf("close disconnected a closed handle, or reset: closes=%d resets=%d", fx.dev.closed, fx.resets)
	}
}

// Every read heals a reset: Inspect, Keys, Attest, the management key.
func TestEveryReadReconnects(t *testing.T) {
	fx := newFixture(t)
	fx.dev.addKey(0x9d, pivgo.PINPolicyOnce, pivgo.TouchPolicyAlways)
	fx.dev.protected = bytes.Repeat([]byte{9}, 24)
	c := open(t)
	defer c.Close()
	fx.dev.resetNext = true
	if _, err := c.Inspect(SlotKeyManagement); err != nil {
		t.Fatalf("inspect: %v", err)
	}
	fx.dev.resetNext = true
	if keys, err := c.Keys(); err != nil || len(keys) != 1 {
		t.Fatalf("keys: %v %v", keys, err)
	}
	fx.dev.resetNext = true
	if _, err := c.Attest(SlotKeyManagement); !errors.Is(err, ErrAttestation) {
		t.Fatalf("attest: %v", err) // the fake's chain never verifies; the read itself healed
	}
	fx.dev.resetNext = true
	if mk, err := c.ProtectedManagementKey("123456"); err != nil || len(mk) != 24 {
		t.Fatalf("management key: %v", err)
	}
	if fx.dev.reopens != 4 {
		t.Fatalf("reopens=%d", fx.dev.reopens)
	}
}

// Generate across a reset: one met by the opening inspect is healed; a
// GENERATE the reset swallowed is sent again, and with Overwrite the key
// that was there is not mistaken for the new one; a GENERATE that ran
// before the reset is read back, not repeated.
func TestGenerateAcrossReset(t *testing.T) {
	fx := newFixture(t)
	old := fx.dev.addKey(0x9d, pivgo.PINPolicyOnce, pivgo.TouchPolicyAlways)
	c := open(t)
	defer c.Close()
	o := GenerateOptions{Slot: SlotKeyManagement, Overwrite: true}
	fx.dev.resetNext = true // lands on the opening inspect
	info, err := c.Generate(fx.dev.mgmtKey, o)
	if err != nil || bytes.Equal(info.PublicKey, pubBytes(old)) {
		t.Fatalf("reset on the inspect: %v %v", info, err)
	}
	if fx.dev.generates != 1 || fx.dev.reopens != 1 {
		t.Fatalf("generates=%d reopens=%d", fx.dev.generates, fx.dev.reopens)
	}
	prev := info.PublicKey
	fx.dev.resetBeforeGenerate = true // swallowed before the card ran it
	info, err = c.Generate(fx.dev.mgmtKey, o)
	if err != nil {
		t.Fatalf("reset on the generate: %v", err)
	}
	if bytes.Equal(info.PublicKey, prev) {
		t.Fatal("the key that was there came back as the new one")
	}
	if fx.dev.generates != 2 || fx.dev.reopens != 2 {
		t.Fatalf("generates=%d reopens=%d", fx.dev.generates, fx.dev.reopens)
	}
	prev = info.PublicKey
	fx.dev.resetAfterGenerate = true // the card generated; the answer was lost
	info, err = c.Generate(fx.dev.mgmtKey, o)
	if err != nil || bytes.Equal(info.PublicKey, prev) {
		t.Fatalf("reset after the generate: %v %v", info, err)
	}
	if fx.dev.generates != 3 || fx.dev.reopens != 3 {
		t.Fatalf("generated again instead of reading back: generates=%d reopens=%d", fx.dev.generates, fx.dev.reopens)
	}
	if got, _ := c.Inspect(SlotKeyManagement); !bytes.Equal(got.PublicKey, info.PublicKey) {
		t.Fatal("the read-back key is not the slot's")
	}
}

// A reset met by the probe that tells a missed touch from an unsatisfied
// PIN is healed, not reported as the missed touch.
func TestResetDuringTouchDisambiguationIsHealed(t *testing.T) {
	fx := newFixture(t)
	k := fx.dev.addKey(0x9d, pivgo.PINPolicyOnce, pivgo.TouchPolicyAlways)
	fx.dev.touch = false
	fx.dev.resetAfterTouchFail = true
	c := open(t)
	defer c.Close()
	p := &testPrompter{pins: []string{"123456"}}
	tok, _ := c.Token(pubBytes(k), p)
	eph, _ := ecdh.P256().GenerateKey(rand.Reader)
	_, err := tok.ECDH(eph.PublicKey().Bytes())
	if !errors.Is(err, ErrTouch) {
		t.Fatalf("no touch, after the reset was healed: %v", err)
	}
	if fx.dev.reopens != 1 || len(p.statuses) != 1 || fx.dev.verifies != 2 {
		t.Fatalf("reopens=%d prompts=%d verifies=%d", fx.dev.reopens, len(p.statuses), fx.dev.verifies)
	}
}

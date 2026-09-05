package kdf

import (
	"bytes"
	"crypto/ecdh"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

// The vector file is the contract: every intermediate value produced by
// tools/kdfvec, confirmed against an independent implementation. This test
// drives the package with the inputs and compares each stage. Every output
// key in the JSON is asserted here; the only untouched keys are the inputs
// themselves and the "_spec" label.

type argonJSON struct {
	MemKiB  uint32 `json:"m_kib"`
	Time    uint32 `json:"t"`
	Threads uint8  `json:"p"`
}

func (a argonJSON) params() Argon2Params {
	return Argon2Params{MemKiB: a.MemKiB, Time: a.Time, Threads: a.Threads}
}

type hybridJSON struct {
	SeedX      string `json:"seed_x"`
	SeedK      string `json:"seed_k"`
	PkX        string `json:"pk_x"`
	EK         string `json:"mlkem_ek"`
	E          string `json:"E"`
	HX         string `json:"H_x"`
	CT         string `json:"mlkem_ct"`
	KK         string `json:"K_k"`
	CombineIKM string `json:"combine_ikm"`
	Pre        string `json:"pre"`
	IK         string `json:"IK"`
	// recovery only
	R      string `json:"R"`
	Digits string `json:"digits"`
	// password only
	Password     string    `json:"password"`
	PasswordUTF8 string    `json:"password_utf8"`
	A            string    `json:"A"`
	Argon2       argonJSON `json:"argon2"`
}

type vectorsJSON struct {
	Inputs struct {
		VaultID     string    `json:"vault_id"`
		RecipientID string    `json:"recipient_id"`
		SlotSalt    string    `json:"slot_salt"`
		Argon2Salt  string    `json:"argon2_salt"`
		VMK         string    `json:"vmk"`
		VMKGen      uint64    `json:"vmk_generation"`
		ArchiveID   string    `json:"archive_id"`
		ArchiveKey  string    `json:"archive_key"`
		HwSK        string    `json:"hw_sk"`
		HwESK       string    `json:"hw_esk"`
		EphX        string    `json:"hybrid_eph_x25519_sk"`
		WrapNonce   string    `json:"wrap_nonce"`
		WrapAAD     string    `json:"wrap_aad"`
		NFC         string    `json:"password_nfc_input"`
		NFD         string    `json:"password_nfd_input"`
		Anchor      argonJSON `json:"argon2_anchor"`
		Tiny        argonJSON `json:"argon2_tiny"`
	} `json:"inputs"`
	InfoIK    string `json:"info_IK"`
	SaltPrime string `json:"salt_prime"`
	Hardware  struct {
		PkHW       string `json:"pk_hw"`
		EPK        string `json:"epk"`
		H          string `json:"H"`
		NoPassword struct {
			Pre string `json:"pre"`
			IK  string `json:"IK"`
		} `json:"no_password"`
		Tiny struct {
			Password     string    `json:"password"`
			PasswordUTF8 string    `json:"password_utf8"`
			PwdPrime     string    `json:"pwd_prime"`
			Argon2       argonJSON `json:"argon2"`
			Pre          string    `json:"pre"`
			IK           string    `json:"IK"`
		} `json:"password_tiny"`
		NFC struct {
			NFDInputUTF8 string    `json:"password_nfd_input_utf8"`
			NFCInputUTF8 string    `json:"password_nfc_input_utf8"`
			Normalised   string    `json:"password_normalised"`
			PwdPrime     string    `json:"pwd_prime"`
			Argon2       argonJSON `json:"argon2"`
			Pre          string    `json:"pre"`
			IK           string    `json:"IK"`
		} `json:"password_nfc"`
		Anchor struct {
			Password string    `json:"password"`
			PwdPrime string    `json:"pwd_prime"`
			Argon2   argonJSON `json:"argon2"`
			Pre      string    `json:"pre"`
			IK       string    `json:"IK"`
		} `json:"password_anchor"`
	} `json:"hardware_slot"`
	Recovery hybridJSON `json:"recovery_slot"`
	Password hybridJSON `json:"password_slot"`
	VMKKeys  struct {
		Metadata    string `json:"metadata_key"`
		DB          string `json:"db_key"`
		KWK         string `json:"KWK"`
		KWKIdentity string `json:"KWK_identity"`
	} `json:"vmk_keys"`
	ArchiveKeys struct {
		Index string `json:"index_key"`
		Wrap  string `json:"wrap_key"`
	} `json:"archive_keys"`
	WrappedVMK struct {
		IK        string `json:"IK"`
		Plaintext string `json:"plaintext"`
		Nonce     string `json:"nonce"`
		AAD       string `json:"aad"`
		Wrapped   string `json:"wrapped_vmk"`
	} `json:"wrapped_vmk"`
}

func loadVectors(t *testing.T) *vectorsJSON {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/kdf-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vectorsJSON
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return &v
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return b
}

func id16(t *testing.T, s string) (id [IDSize]byte) {
	b := mustHex(t, s)
	if len(b) != IDSize {
		t.Fatalf("id %q is %d bytes", s, len(b))
	}
	copy(id[:], b)
	return
}

func key32(t *testing.T, s string) (out [KeySize]byte) {
	b := mustHex(t, s)
	if len(b) != KeySize {
		t.Fatalf("%q is %d bytes, want 32", s, len(b))
	}
	copy(out[:], b)
	return
}

func expect(t *testing.T, name string, got []byte, wantHex string) {
	t.Helper()
	if hex.EncodeToString(got) != wantHex {
		t.Errorf("%s:\n got  %x\n want %s", name, got, wantHex)
	}
}

func TestVectors(t *testing.T) {
	v := loadVectors(t)
	vault, recip := id16(t, v.Inputs.VaultID), id16(t, v.Inputs.RecipientID)
	slotSalt, argonSalt := SlotSalt(key32(t, v.Inputs.SlotSalt)), Salt(key32(t, v.Inputs.Argon2Salt))

	expect(t, "info_IK", Info(InfoIK, vault, recip), v.InfoIK)
	sp := SaltPrime(argonSalt, vault, recip)
	expect(t, "salt_prime", sp[:], v.SaltPrime)

	// ---- hardware slot ----
	p256 := ecdh.P256()
	skHW, err := p256.NewPrivateKey(mustHex(t, v.Inputs.HwSK))
	if err != nil {
		t.Fatal(err)
	}
	esk, err := p256.NewPrivateKey(mustHex(t, v.Inputs.HwESK))
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "pk_hw", skHW.PublicKey().Bytes(), v.Hardware.PkHW)
	expect(t, "epk", esk.PublicKey().Bytes(), v.Hardware.EPK)
	h, err := skHW.ECDH(esk.PublicKey()) // what the token computes (R5)
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "H", h, v.Hardware.H)

	pre, err := HardwarePreToken(h)
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "no_password.pre", pre[:], v.Hardware.NoPassword.Pre)
	expect(t, "no_password.IK", DeriveIK(pre, vault, recip), v.Hardware.NoPassword.IK)

	pw, err := NormalizePassword(v.Hardware.Tiny.Password)
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "password_tiny.password_utf8", pw, v.Hardware.Tiny.PasswordUTF8)
	expect(t, "password_tiny.pwd_prime", HMACFold(h, pw), v.Hardware.Tiny.PwdPrime)
	pre, err = HardwarePreEntangled(h, pw, argonSalt, vault, recip, v.Hardware.Tiny.Argon2.params())
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "password_tiny.pre", pre[:], v.Hardware.Tiny.Pre)
	expect(t, "password_tiny.IK", DeriveIK(pre, vault, recip), v.Hardware.Tiny.IK)

	expect(t, "password_nfc.nfd_input_utf8", []byte(v.Inputs.NFD), v.Hardware.NFC.NFDInputUTF8)
	expect(t, "password_nfc.nfc_input_utf8", []byte(v.Inputs.NFC), v.Hardware.NFC.NFCInputUTF8)
	nfd, err := NormalizePassword(v.Inputs.NFD)
	if err != nil {
		t.Fatal(err)
	}
	nfc, err := NormalizePassword(v.Inputs.NFC)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(nfd, nfc) {
		t.Fatalf("NFD and NFC inputs normalise differently: %x vs %x", nfd, nfc)
	}
	expect(t, "password_nfc.normalised", nfd, v.Hardware.NFC.Normalised)
	expect(t, "password_nfc.pwd_prime", HMACFold(h, nfd), v.Hardware.NFC.PwdPrime)
	pre, err = HardwarePreEntangled(h, nfd, argonSalt, vault, recip, v.Hardware.NFC.Argon2.params())
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "password_nfc.pre", pre[:], v.Hardware.NFC.Pre)
	expect(t, "password_nfc.IK", DeriveIK(pre, vault, recip), v.Hardware.NFC.IK)

	anchorPW, err := NormalizePassword(v.Hardware.Anchor.Password)
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "password_anchor.pwd_prime", HMACFold(h, anchorPW), v.Hardware.Anchor.PwdPrime)
	if testing.Short() {
		t.Log("skipping the 512 MiB Argon2id anchor in -short mode")
	} else {
		pre, err = HardwarePreEntangled(h, anchorPW, argonSalt, vault, recip, v.Hardware.Anchor.Argon2.params())
		if err != nil {
			t.Fatal(err)
		}
		expect(t, "password_anchor.pre", pre[:], v.Hardware.Anchor.Pre)
		expect(t, "password_anchor.IK", DeriveIK(pre, vault, recip), v.Hardware.Anchor.IK)
	}

	// ---- hybrid slots ----
	eph, err := ecdh.X25519().NewPrivateKey(mustHex(t, v.Inputs.EphX))
	if err != nil {
		t.Fatal(err)
	}
	checkHybrid := func(name string, kind HybridKind, ikm []byte, hv *hybridJSON) {
		t.Helper()
		seedX, seedK, err := HybridSeeds(kind, ikm, slotSalt, vault, recip)
		if err != nil {
			t.Fatal(err)
		}
		expect(t, name+".seed_x", seedX[:], hv.SeedX)
		expect(t, name+".seed_k", seedK[:], hv.SeedK)
		skX, dk, err := HybridKeys(seedX, seedK)
		if err != nil {
			t.Fatal(err)
		}
		pkX, ek := HybridPublic(skX, dk)
		expect(t, name+".pk_x", pkX, hv.PkX)
		expect(t, name+".mlkem_ek", ek, hv.EK)
		if !VerifyX25519(skX, pkX) {
			t.Errorf("%s: verifier rejects its own public key", name)
		}

		// The wrap side with the pinned ephemeral: E and H_x must match. The ML-KEM
		// half of a live wrap is randomised, so it is checked through the unwrap side.
		_, E, _, err := hybridWrapWith(kind, pkX, ek, eph, vault, recip)
		if err != nil {
			t.Fatal(err)
		}
		expect(t, name+".E", E, hv.E)
		hx, err := skX.ECDH(eph.PublicKey())
		if err != nil {
			t.Fatal(err)
		}
		expect(t, name+".H_x", hx, hv.HX)

		// The unlock side against the pinned ciphertext.
		ct := mustHex(t, hv.CT)
		kk, err := dk.Decapsulate(ct)
		if err != nil {
			t.Fatal(err)
		}
		expect(t, name+".K_k", kk, hv.KK)
		expect(t, name+".combine_ikm", append(append([]byte{}, hx...), kk...), hv.CombineIKM)
		pre, err := HybridUnwrap(kind, skX, dk, mustHex(t, hv.E), ct, vault, recip)
		if err != nil {
			t.Fatal(err)
		}
		expect(t, name+".pre", pre[:], hv.Pre)
		expect(t, name+".IK", DeriveIK(pre, vault, recip), hv.IK)
	}

	r, err := ParseRecoveryDigits(v.Recovery.Digits)
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "recovery.R", r[:], v.Recovery.R)
	if r.Digits() != v.Recovery.Digits {
		t.Errorf("recovery digits: got %s want %s", r.Digits(), v.Recovery.Digits)
	}
	checkHybrid("recovery", HybridRecovery, r[:], &v.Recovery)

	pw, err = NormalizePassword(v.Password.Password)
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "password.password_utf8", pw, v.Password.PasswordUTF8)
	a, err := PasswordSlotIKM(pw, argonSalt, vault, recip, v.Password.Argon2.params())
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "password.A", a, v.Password.A)
	checkHybrid("password", HybridPassword, a, &v.Password)

	// ---- subordinate keys ----
	vmk := key32(t, v.Inputs.VMK)
	expect(t, "metadata_key", MetadataKey(vmk, vault), v.VMKKeys.Metadata)
	expect(t, "db_key", DBKey(vmk, vault), v.VMKKeys.DB)
	expect(t, "KWK", KWK(vmk, vault), v.VMKKeys.KWK)
	expect(t, "KWK_identity", KWKIdentity(vmk, vault), v.VMKKeys.KWKIdentity)
	archiveID := id16(t, v.Inputs.ArchiveID)
	archiveKey := key32(t, v.Inputs.ArchiveKey)
	expect(t, "index_key", ArchiveIndexKey(archiveKey, archiveID), v.ArchiveKeys.Index)
	expect(t, "wrap_key", ArchiveWrapKey(archiveKey, archiveID), v.ArchiveKeys.Wrap)

	// ---- wrapped_vmk (R12) ----
	var nonce [NonceSize]byte
	copy(nonce[:], mustHex(t, v.WrappedVMK.Nonce))
	expect(t, "wrapped_vmk.nonce", nonce[:], v.Inputs.WrapNonce)
	ik := mustHex(t, v.WrappedVMK.IK)
	expect(t, "wrapped_vmk.IK", ik, v.Hardware.NoPassword.IK)
	aad := mustHex(t, v.WrappedVMK.AAD)
	expect(t, "wrapped_vmk.aad", aad, v.Inputs.WrapAAD)
	pt := make([]byte, KeySize+8)
	copy(pt, vmk[:])
	binary.LittleEndian.PutUint64(pt[KeySize:], v.Inputs.VMKGen)
	expect(t, "wrapped_vmk.plaintext", pt, v.WrappedVMK.Plaintext)
	wrapped, err := WrapVMKWithNonce(ik, vmk, v.Inputs.VMKGen, nonce, aad)
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "wrapped_vmk", wrapped[:], v.WrappedVMK.Wrapped)
	got, gen, err := UnwrapVMK(ik, wrapped, nonce, aad)
	if err != nil || got != vmk || gen != v.Inputs.VMKGen {
		t.Fatalf("unwrap: %v, generation %d", err, gen)
	}
}

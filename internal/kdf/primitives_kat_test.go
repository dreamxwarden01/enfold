package kdf

// Known-answer tests for the primitives the KDF chain is built from, against vectors published
// by their specifications or reference implementations — not against this codebase.
//
// These exist so that a toolchain or dependency update that silently changed a primitive would
// fail here, before it could produce 32 plausible-looking bytes everywhere else. The chain
// itself is pinned by testdata/kdf-vectors.json (see FORMAT.md §3.3).

import (
	"bytes"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"golang.org/x/crypto/argon2"
)

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex in test vector: %v", err)
	}
	return b
}

// RFC 5869 Appendix A, SHA-256 cases. Test Case 3 is the one that matters most for this design:
// zero-length salt and info, which is what FORMAT.md §3.3 R1 calls "salt = ∅".
func TestHKDF_RFC5869(t *testing.T) {
	ikm := bytes.Repeat([]byte{0x0b}, 22)

	t.Run("TC1", func(t *testing.T) {
		got, err := hkdf.Key(sha256.New, ikm,
			unhex(t, "000102030405060708090a0b0c"),
			string(unhex(t, "f0f1f2f3f4f5f6f7f8f9")), 42)
		if err != nil {
			t.Fatal(err)
		}
		want := unhex(t, "3cb25f25faacd57a90434f64d0362f2a2d2d0a90cf1a5a4c5db02d56ecc4c5bf34007208d5b887185865")
		if !bytes.Equal(got, want) {
			t.Fatalf("got %x", got)
		}
	})

	t.Run("TC3 zero-length salt and info", func(t *testing.T) {
		want := unhex(t, "8da4e775a563c18f715f802a063c5a31b8a11f5c5ee1879ec3454e5f3c738d2d9d201395faa4b61a96c8")
		gotNil, err := hkdf.Key(sha256.New, ikm, nil, "", 42)
		if err != nil {
			t.Fatal(err)
		}
		gotEmpty, err := hkdf.Key(sha256.New, ikm, []byte{}, "", 42)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(gotNil, want) {
			t.Fatalf("nil salt: got %x", gotNil)
		}
		// R1 relies on nil and empty being the same thing. They are, because HMAC pads any
		// key shorter than the block size with zeros — but pin it rather than assume it.
		if !bytes.Equal(gotEmpty, gotNil) {
			t.Fatal("nil salt and empty salt derived different keys")
		}
	})
}

// Argon2 vectors. The Argon2i case is the single worked example in the reference
// implementation's README (echo -n "password" | argon2 somesalt -t 2 -m 16 -p 4 -l 24).
// The Argon2id cases were generated with that same reference CLI; they are the values
// golang.org/x/crypto/argon2 itself was validated against, and are reproduced here so this
// package does not silently inherit a change to that dependency's own tests.
func TestArgon2_Reference(t *testing.T) {
	pw, salt := []byte("password"), []byte("somesalt")

	t.Run("Argon2i README example", func(t *testing.T) {
		got := argon2.Key(pw, salt, 2, 1<<16, 4, 24)
		if !bytes.Equal(got, unhex(t, "45d7ac72e76f242b20b77b9bf9bf9d5915894e669a24e6c6")) {
			t.Fatalf("got %x", got)
		}
	})

	cases := []struct {
		time, memKiB uint32
		threads      uint8
		want         string
	}{
		{1, 64, 1, "655ad15eac652dc59f7170a7332bf49b8469be1fdb9c28bb"},
		{2, 64, 1, "068d62b26455936aa6ebe60060b0a65870dbfa3ddf8d41f7"},
		{4, 4096, 4, "145db9733a9f4ee43edf33c509be96b934d505a4efb33c5a"},
		{3, 1024, 6, "1640b932f4b60e272f5d2207b9a9c626ffa1bd88d2349016"},
	}
	for _, c := range cases {
		got := argon2.IDKey(pw, salt, c.time, c.memKiB, c.threads, 24)
		if !bytes.Equal(got, unhex(t, c.want)) {
			t.Errorf("Argon2id t=%d m=%d p=%d: got %x", c.time, c.memKiB, c.threads, got)
		}
	}
}

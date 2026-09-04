package kdf

// ML-KEM-1024 known-answer tests against NIST ACVP vectors (FIPS 203). The full run — 25 keyGen,
// 25 encapsulation, 10 decapsulation including the implicit-rejection branch, plus a negative
// control — was performed on 2026-09-04 and is recorded in DECISIONS.md; this keeps a small
// subset in the tree so a toolchain change cannot silently alter the primitive.
//
// Decapsulation is not repeated here: ACVP supplies only the expanded dk, which the public API
// cannot load, and the private-API route is too fragile to keep in a test that must survive Go
// upgrades. The round-trip in the vectors (FORMAT.md §3.1) covers it for our own seeds.

import (
	"bytes"
	"crypto/mlkem"
	"crypto/mlkem/mlkemtest"
	"encoding/json"
	"os"
	"testing"
)

type acvpSubset struct {
	KeyGen []struct {
		TcID     int
		D, Z, Ek string
	} `json:"keygen"`
	Encap []struct {
		TcID        int
		Ek, M, C, K string
	} `json:"encap"`
}

func TestMLKEM1024_ACVP(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/mlkem-acvp-subset.json")
	if err != nil {
		t.Fatal(err)
	}
	var v acvpSubset
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	for _, c := range v.KeyGen {
		seed := append(unhex(t, c.D), unhex(t, c.Z)...) // FIPS 203: d || z
		dk, err := mlkem.NewDecapsulationKey1024(seed)
		if err != nil {
			t.Fatalf("tcId %d: %v", c.TcID, err)
		}
		if !bytes.Equal(dk.EncapsulationKey().Bytes(), unhex(t, c.Ek)) {
			t.Errorf("tcId %d: ek mismatch", c.TcID)
		}
	}
	for _, c := range v.Encap {
		ek, err := mlkem.NewEncapsulationKey1024(unhex(t, c.Ek))
		if err != nil {
			t.Fatalf("tcId %d: %v", c.TcID, err)
		}
		k, ct, err := mlkemtest.Encapsulate1024(ek, unhex(t, c.M))
		if err != nil {
			t.Fatalf("tcId %d: %v", c.TcID, err)
		}
		if !bytes.Equal(ct, unhex(t, c.C)) || !bytes.Equal(k, unhex(t, c.K)) {
			t.Errorf("tcId %d: encapsulation mismatch", c.TcID)
		}
	}
	if len(v.KeyGen) == 0 || len(v.Encap) == 0 {
		t.Fatal("empty subset")
	}
}

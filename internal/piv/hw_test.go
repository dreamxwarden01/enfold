//go:build windows

package piv

import (
	"errors"
	"os"
	"testing"
)

// Hardware tests: run only with ENFOLD_PIV_HW=1 and a YubiKey attached.
// Everything here is read-only — no PIN, no touch, no generation; the
// only thing a run does to the token is the warm reset every Open performs
// in its probe, which clears volatile state and nothing else. What they
// check is what the fake cannot: the real PC/SC return codes, piv-go's
// real error texts, the probe-and-reset path, attestation against Yubico's
// roots.

func hwReaders(t *testing.T) []string {
	t.Helper()
	if os.Getenv("ENFOLD_PIV_HW") != "1" {
		t.Skip("set ENFOLD_PIV_HW=1 to run against a YubiKey")
	}
	readers, err := Readers()
	if err != nil {
		t.Fatal(err)
	}
	if len(readers) == 0 {
		t.Skip("no YubiKey reader attached")
	}
	return readers
}

func TestHWReadOnly(t *testing.T) {
	for _, r := range hwReaders(t) {
		c, err := Open(r)
		if err != nil {
			t.Fatalf("open %q: %v", r, err)
		}
		t.Logf("%s: firmware %s serial %d", r, c.Version(), c.Serial())
		if !c.Version().atLeast(5, 3) {
			t.Fatal("firmware floor not enforced")
		}
		st, err := c.PINState()
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("PIN state: %+v", st)
		if st.Verified {
			t.Error("the card is verified after Open's reset")
		}
		if !st.RetriesKnown || st.Retries == 0 {
			t.Errorf("retries not readable, or the PIN is blocked: %+v", st)
		}
		keys, err := c.Keys()
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range keys {
			t.Logf("slot %s: %s pin=%s touch=%s origin=%s cert=%v usable=%v %s", k.Slot, k.Algorithm, k.PINPolicy, k.TouchPolicy, k.Origin, k.Certificate, k.Usable(), k.WhyNotUsable())
			if k.Algorithm == AlgorithmP256 {
				found, err := c.Find(k.PublicKey)
				if err != nil || found.Slot != k.Slot {
					t.Errorf("find %s: %+v %v", k.Slot, found, err)
				}
				a, err := c.Attest(k.Slot)
				if err != nil {
					t.Logf("attest %s: %v", k.Slot, err)
					continue
				}
				t.Logf("attested: %+v", a)
				if a.Serial != c.Serial() || a.PINPolicy != k.PINPolicy || a.TouchPolicy != k.TouchPolicy {
					t.Errorf("attestation disagrees with metadata: %+v vs %+v", a, k)
				}
			}
		}
		if s, err := c.FirstEmptySlot(); err != nil && !errors.Is(err, ErrFull) {
			t.Errorf("first empty: %v", err)
		} else {
			t.Logf("first empty slot: %s (%v)", s, err)
		}
		// Nothing verified through this Card: a plain release.
		if err := c.Close(); err != nil || c.ResetFailed() {
			t.Errorf("close: %v reset-failed=%v", err, c.ResetFailed())
		}
	}
}

func TestHWErrors(t *testing.T) {
	readers := hwReaders(t)
	if _, err := Open("no such reader"); !errors.Is(err, ErrNoReader) {
		t.Errorf("unknown reader: %v", err)
	}
	c, err := Open(readers[0])
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// The Card holds the card exclusively: a second Open is refused at the
	// probe, before piv-go could leak a connection.
	if _, err := Open(readers[0]); !errors.Is(err, ErrBusy) {
		t.Errorf("second open: %v", err)
	}
	for _, s := range []Slot{0x9a, 0x9c, 0x9e, 0xf9} {
		if _, err := c.Inspect(s); !errors.Is(err, ErrForbiddenSlot) {
			t.Errorf("inspect %s: %v", s, err)
		}
	}
	if _, err := c.Attest(Slot(0x95)); !errors.Is(err, ErrEmpty) && err != nil {
		t.Logf("attest 95: %v", err)
	}
}

package keystore

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dreamxwarden01/enfold/internal/format"
)

// The preflight of a slot that would join the region (the switch audit of
// 2026-11-07, findings 1 and 3): the verdicts a caller asks before any card
// work, and the same refusals inside the mutations before their Argon2id.
// countArgon2 swaps a package variable, so none of these runs in parallel.

// longLabels are the labels refused: one over the house's bound, one over
// the format's own limit, one that is not UTF-8.
var longLabels = map[string]string{
	"over the house's 120 characters": strings.Repeat("x", MaxLabelChars+1),
	"over the format's 65,535 bytes":  strings.Repeat("x", format.MaxLabelBytes+1),
	"not UTF-8":                       "Desk \xff YubiKey",
}

func TestCheckLabel(t *testing.T) {
	for _, ok := range []string{"", "Desk YubiKey", strings.Repeat("x", MaxLabelChars), strings.Repeat("😀", MaxLabelChars)} {
		if err := CheckLabel(ok); err != nil {
			t.Fatalf("%d bytes refused: %v", len(ok), err)
		}
	}
	for name, bad := range longLabels {
		if err := CheckLabel(bad); !errors.Is(err, ErrParams) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if err := CheckLabel(strings.Repeat("😀", MaxLabelChars+1)); !errors.Is(err, ErrParams) {
		t.Fatalf("121 four-byte characters: %v", err)
	}
	// The format's limit is the one SlotRecord.Validate applies.
	rec, err := standIn(RecoverySlot{Label: strings.Repeat("x", format.MaxLabelBytes)})
	if err != nil {
		t.Fatal(err)
	}
	if err := rec.Validate(); err != nil {
		t.Fatalf("a label of format.MaxLabelBytes: %v", err)
	}
	rec.Label += "x"
	if err := rec.Validate(); !errors.Is(err, format.ErrInvalid) {
		t.Fatalf("a label one byte over format.MaxLabelBytes: %v", err)
	}
}

// The stand-in is the size of the record it stands for, byte for byte, so
// that the region's room is measured on what the commit will write.
func TestStandInIsTheRecordsSize(t *testing.T) {
	tok := newToken(t)
	var kp [32]byte
	var vmk [32]byte
	for _, spec := range []SlotSpec{
		HardwareSlot{PublicKey: tok.PublicKey(), Label: "Desk YubiKey"},
		HardwareSlot{Label: "Desk YubiKey"},
		RecoverySlot{Key: recoveryKey(t), Label: "paper"},
		PasswordSlot{Password: tradeWords, Argon2: fast, Label: "Password"},
	} {
		stand, err := standIn(spec)
		if err != nil {
			t.Fatal(err)
		}
		want := spec
		if hs, ok := spec.(HardwareSlot); ok && hs.PublicKey == nil {
			hs.PublicKey = tok.PublicKey()
			want = hs
		}
		real, err := newRecord(want, [16]byte{1}, &kp, vmk, 1)
		if err != nil {
			t.Fatal(err)
		}
		a, err := stand.Encode()
		if err != nil {
			t.Fatalf("%T: the stand-in does not encode: %v", spec, err)
		}
		b, err := real.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if len(a) != len(b) {
			t.Fatalf("%T: stand-in %d bytes, record %d", spec, len(a), len(b))
		}
	}
}

// fits measures both bounds: the record count and the encoded size.
func TestFitsMeasuresTheRegion(t *testing.T) {
	big, err := standIn(RecoverySlot{Label: strings.Repeat("x", 63000)})
	if err != nil {
		t.Fatal(err)
	}
	small, err := standIn(HardwareSlot{Label: "Desk YubiKey"})
	if err != nil {
		t.Fatal(err)
	}
	if err := fits([]format.SlotRecord{big}, small); err != nil {
		t.Fatalf("one big record and a YubiKey: %v", err)
	}
	if err := fits([]format.SlotRecord{big, big}, small); !errors.Is(err, ErrFull) {
		t.Fatalf("past 128 KiB: %v", err)
	}
	many := make([]format.SlotRecord, format.MaxSlots)
	for i := range many {
		many[i] = small
	}
	if err := fits(many[:format.MaxSlots-1], small); err != nil {
		t.Fatalf("the 32nd record: %v", err)
	}
	if err := fits(many, small); !errors.Is(err, ErrFull) {
		t.Fatalf("a 33rd record: %v", err)
	}
	if err := fits(nil, format.SlotRecord{State: format.SlotActive, Type: format.SlotExternalECDH, Curve: format.CurveP256, KeySource: 9, EPK: standInPoint, SlotPubkey: standInPoint}); !errors.Is(err, ErrParams) {
		t.Fatalf("a key source the format does not know: %v", err)
	}
}

// The trade refuses a label before its Argon2id, with the file untouched,
// and CanTradePasswordTo gives the same verdict with no VMK.
func TestTradeRefusesALabelBeforeTheKDF(t *testing.T) {
	for name, label := range longLabels {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "v.eks")
			u, _ := passwordVault(t, path, "")
			tok := newToken(t)
			before := snapshot(t, path)
			n := countArgon2(t)
			hs := HardwareSlot{PublicKey: tok.PublicKey(), Label: label}
			if err := u.k.CanTradePasswordTo(HardwareSlot{Label: label}); !errors.Is(err, ErrParams) {
				t.Fatalf("CanTradePasswordTo: %v", err)
			}
			if err := u.TradePasswordForToken(passwordRID(t, u), hs, Entangle{Password: tradeWords, Argon2: fast}, false); !errors.Is(err, ErrParams) {
				t.Fatalf("the trade: %v", err)
			}
			if *n != 0 {
				t.Fatalf("%d K_P derivations before the refusal", *n)
			}
			if !bytes.Equal(snapshot(t, path), before) {
				t.Fatal("the refused trade wrote the file")
			}
		})
	}
	path := filepath.Join(t.TempDir(), "v.eks")
	u, _ := passwordVault(t, path, "")
	if err := u.k.CanTradePasswordTo(HardwareSlot{Label: strings.Repeat("x", MaxLabelChars)}); err != nil {
		t.Fatalf("a label of 120 characters: %v", err)
	}
	if err := u.k.CanTradePasswordTo(HardwareSlot{Source: 9}); !errors.Is(err, ErrParams) {
		t.Fatalf("an unknown key source: %v", err)
	}
}

// recoveryOnly is a vault of n recovery keys and nothing else, the shape of
// an adopted backup: header zero, no way in but the keys.
func recoveryOnly(t *testing.T, path string, n int) *Unlocked {
	t.Helper()
	specs := make([]SlotSpec, n)
	for i := range specs {
		specs[i] = RecoverySlot{Key: recoveryKey(t), Label: "paper"}
	}
	return mustCreate(t, path, specs...)
}

// The first way in refuses a label and a full region before newHeader's
// Argon2id; AddSlot a full region before anything is written; and the
// verdicts with no VMK say the same, the policy and the header included.
func TestFirstWayInAndAddSlotRefuseBeforeTheKDF(t *testing.T) {
	tok := newToken(t)
	ent := &Entangle{Password: tradeWords, Argon2: fast}
	for name, label := range longLabels {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "v.eks")
			u := recoveryOnly(t, path, 2)
			before := snapshot(t, path)
			n := countArgon2(t)
			if err := u.k.CanAddFirstWayIn(HardwareSlot{Label: label}, true); !errors.Is(err, ErrParams) {
				t.Fatalf("CanAddFirstWayIn: %v", err)
			}
			if err := u.AddFirstWayIn(HardwareSlot{PublicKey: tok.PublicKey(), Label: label}, ent); !errors.Is(err, ErrParams) {
				t.Fatalf("AddFirstWayIn: %v", err)
			}
			if err := u.k.CanAddSlot(HardwareSlot{Label: label}); !errors.Is(err, ErrParams) {
				t.Fatalf("CanAddSlot: %v", err)
			}
			if *n != 0 || !bytes.Equal(snapshot(t, path), before) {
				t.Fatalf("%d K_P derivations; file changed %v", *n, !bytes.Equal(snapshot(t, path), before))
			}
		})
	}
	t.Run("a full region", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "v.eks")
		u := recoveryOnly(t, path, format.MaxSlots)
		before := snapshot(t, path)
		n := countArgon2(t)
		if err := u.k.CanAddFirstWayIn(HardwareSlot{}, true); !errors.Is(err, ErrFull) {
			t.Fatalf("CanAddFirstWayIn: %v", err)
		}
		if err := u.AddFirstWayIn(HardwareSlot{PublicKey: tok.PublicKey(), Label: "Desk YubiKey"}, ent); !errors.Is(err, ErrFull) {
			t.Fatalf("AddFirstWayIn: %v", err)
		}
		if err := u.k.CanAddSlot(HardwareSlot{}); !errors.Is(err, ErrFull) {
			t.Fatalf("CanAddSlot: %v", err)
		}
		if err := u.AddSlot(HardwareSlot{PublicKey: tok.PublicKey(), Label: "Desk YubiKey"}); !errors.Is(err, ErrFull) {
			t.Fatalf("AddSlot: %v", err)
		}
		if err := u.AddSlot(PasswordSlot{Password: tradeWords, Argon2: fast, Label: "Password"}); !errors.Is(err, ErrFull) {
			t.Fatalf("AddSlot of a password: %v", err)
		}
		if *n != 0 || !bytes.Equal(snapshot(t, path), before) {
			t.Fatalf("%d K_P derivations; file changed %v", *n, !bytes.Equal(snapshot(t, path), before))
		}
		// One record fewer, and the YubiKey fits.
		if err := recoveryOnly(t, filepath.Join(t.TempDir(), "w.eks"), format.MaxSlots-1).k.CanAddSlot(HardwareSlot{}); err != nil {
			t.Fatalf("the 32nd record: %v", err)
		}
	})
	t.Run("the policy and the header", func(t *testing.T) {
		pw, _ := passwordVault(t, filepath.Join(t.TempDir(), "p.eks"), "")
		if err := pw.k.CanAddSlot(HardwareSlot{}); !errors.Is(err, ErrPolicy) {
			t.Fatalf("a YubiKey beside a standalone password: %v", err)
		}
		if err := pw.k.CanAddFirstWayIn(HardwareSlot{}, false); !errors.Is(err, ErrPolicy) {
			t.Fatalf("a first way in beside a standalone password: %v", err)
		}
		on, _ := passwordVault(t, filepath.Join(t.TempDir(), "e.eks"), "the switch's words")
		if err := on.k.CanAddFirstWayIn(PasswordSlot{Argon2: fast, Label: "Password"}, false); !errors.Is(err, ErrParams) {
			t.Fatalf("a header with the switch on: %v", err)
		}
		ro := recoveryOnly(t, filepath.Join(t.TempDir(), "r.eks"), 2)
		if err := ro.k.CanAddFirstWayIn(HardwareSlot{Label: "Desk YubiKey"}, true); err != nil {
			t.Fatalf("a backup's first YubiKey: %v", err)
		}
		if err := ro.k.CanAddFirstWayIn(PasswordSlot{Argon2: fast, Label: "Password"}, false); err != nil {
			t.Fatalf("a backup's first password: %v", err)
		}
	})
}

// The second pass of the switch audit (2026-11-07): the stand-in's keys are
// sizes, never identities (finding 3); a default name the longest serial
// would not fit is measured at the commit with the real one (finding 2);
// and the counter of derivations sees a standalone password's (finding 5).

// A file whose YubiKey record holds the stand-in's placeholder key (P-256's
// generator) still admits another card, unnamed or with its key; the same
// key again is still a duplicate. A recovery record with a zero X25519 key
// refuses no password and no recovery key either.
func TestAStandInKeyIsNoIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	u := recoveryOnly(t, path, 2)
	if err := u.AddSlot(HardwareSlot{PublicKey: standInPoint, Label: "Old YubiKey"}); err != nil {
		t.Fatalf("a YubiKey of the generator: %v", err)
	}
	if err := u.k.CanAddSlot(HardwareSlot{Label: "Desk YubiKey"}); err != nil {
		t.Fatalf("CanAddSlot of a card not yet read: %v", err)
	}
	if err := u.k.CanAddFirstWayIn(HardwareSlot{Label: "Desk YubiKey"}, true); err != nil {
		t.Fatalf("CanAddFirstWayIn of a card not yet read: %v", err)
	}
	tok := newToken(t)
	if err := u.AddSlot(HardwareSlot{PublicKey: tok.PublicKey(), Label: "Desk YubiKey"}); err != nil {
		t.Fatalf("AddSlot of another card: %v", err)
	}
	if err := u.k.CanAddSlot(HardwareSlot{PublicKey: standInPoint}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("CanAddSlot of the generator again: %v", err)
	}
	if err := u.AddSlot(HardwareSlot{PublicKey: standInPoint, Label: "Again"}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("AddSlot of the generator again: %v", err)
	}
	if err := u.k.CanAddSlot(HardwareSlot{PublicKey: tok.PublicKey()}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("CanAddSlot of the card enrolled: %v", err)
	}

	// A password's and a recovery key's stand-ins carry zero keys.
	ro := recoveryOnly(t, filepath.Join(t.TempDir(), "r.eks"), 2)
	ro.k.slots[1].SlotPubkey = make([]byte, format.X25519PubSize) // in memory only, never committed
	for name, spec := range map[string]SlotSpec{
		"a recovery key": RecoverySlot{Label: "paper 3"},
		"a password":     PasswordSlot{Argon2: fast, Label: "Password"},
	} {
		if err := ro.k.CanAddSlot(spec); err != nil {
			t.Fatalf("CanAddSlot of %s beside a zero key: %v", name, err)
		}
	}
	if err := ro.k.CanAddFirstWayIn(PasswordSlot{Argon2: fast, Label: "Password"}, false); err != nil {
		t.Fatalf("CanAddFirstWayIn of a password beside a zero key: %v", err)
	}
}

// A region with room for "YubiKey 1234567" and not for the longest default
// name, "YubiKey 4294967295": the keystore refuses the longest and admits
// the card's own, and AddSlot commits it, measured on the bytes it writes.
func TestTheDefaultNameFitsWhereTheLongestDoesNot(t *testing.T) {
	const name, longest = "YubiKey 1234567", "YubiKey 4294967295"
	path := filepath.Join(t.TempDir(), "v.eks")
	u := recoveryOnly(t, path, 2)
	size := func(label string) int {
		t.Helper()
		s, err := standIn(HardwareSlot{Label: label})
		if err != nil {
			t.Fatal(err)
		}
		b, err := s.Encode()
		if err != nil {
			t.Fatal(err)
		}
		return len(b)
	}
	used := func() int {
		n := format.MinSlotRegionLen
		for i := range u.k.slots {
			b, err := u.k.slots[i].Encode()
			if err != nil {
				t.Fatal(err)
			}
			n += len(b)
		}
		return n
	}
	// Long labels, as a file written elsewhere may carry within the
	// format's 65,535 bytes, leave exactly the card's record's room.
	extra := format.SlotRegionSize - used() - size(name)
	for i := range u.k.slots {
		add := min(extra, format.MaxLabelBytes-len(u.k.slots[i].Label))
		u.k.slots[i].Label += strings.Repeat("y", add)
		extra -= add
	}
	if extra != 0 || format.SlotRegionSize-used() != size(name) || size(longest) <= size(name) {
		t.Fatalf("the region: %d bytes free, the card's record %d, the longest %d", format.SlotRegionSize-used(), size(name), size(longest))
	}
	if err := u.k.CanAddSlot(HardwareSlot{Label: longest}); !errors.Is(err, ErrFull) {
		t.Fatalf("the longest name: %v", err)
	}
	if err := u.k.CanAddSlot(HardwareSlot{Label: name}); err != nil {
		t.Fatalf("the card's name: %v", err)
	}
	tok := newToken(t)
	if err := u.AddSlot(HardwareSlot{PublicKey: tok.PublicKey(), Label: longest}); !errors.Is(err, ErrFull) {
		t.Fatalf("AddSlot with the longest name: %v", err)
	}
	if err := u.AddSlot(HardwareSlot{PublicKey: tok.PublicKey(), Label: name}); err != nil {
		t.Fatalf("AddSlot with the card's name: %v", err)
	}
	if used() != format.SlotRegionSize {
		t.Fatalf("the region holds %d bytes, not all %d", used(), format.SlotRegionSize)
	}
}

// countArgon2 sees a standalone password's derivation, at AddSlot and at
// an unlock, so that its zero after a refusal means what it says.
func TestTheCounterCountsAStandalonePassword(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	u := recoveryOnly(t, path, 2)
	n := countArgon2(t)
	if err := u.AddSlot(PasswordSlot{Password: tradeWords, Argon2: fast, Label: "Password"}); err != nil {
		t.Fatal(err)
	}
	if *n != 1 {
		t.Fatalf("AddSlot of a password: %d derivations counted", *n)
	}
	u.Close()
	u.k.Close()
	k := mustOpen(t, path)
	*n = 0
	uu, err := k.Unlock(PasswordCredential{Password: tradeWords})
	if err != nil {
		t.Fatal(err)
	}
	uu.Close()
	if *n != 1 {
		t.Fatalf("an unlock with a standalone password: %d derivations counted", *n)
	}
}

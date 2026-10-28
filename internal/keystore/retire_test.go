package keystore

import (
	"bytes"
	"crypto/aes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	mrand "math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dreamxwarden01/enfold/internal/format"
	"github.com/dreamxwarden01/enfold/internal/kdf"
)

// The tests of R41: the durable retirement of the state a commit leaves
// behind, the zero-filled writes and the scrub after every flip (keystore
// tests 14 to 20, 22 to 31 and 33 of the VMK rotation design). Several of them
// set commitFault or writeSeen, which are package-level, so none of them runs
// in parallel.

var errFault = errors.New("injected fault")

// faultOn fails every step of a commit or a scrub named in steps, for the
// rest of the test or until the returned function is called.
func faultOn(t *testing.T, steps ...string) (restore func()) {
	t.Helper()
	quietLog(t)
	restore = SetCommitFault(func(step string) error {
		for _, s := range steps {
			if s == step {
				return fmt.Errorf("%w at %s", errFault, step)
			}
		}
		return nil
	})
	t.Cleanup(restore)
	return restore
}

// quietLog sends the line a commit logs for a scrub that failed after its
// flip to the test's own log, where an injected fault belongs.
func quietLog(t *testing.T) {
	t.Helper()
	saved := Log
	Log = func(format string, a ...any) { t.Logf(format, a...) }
	t.Cleanup(func() { Log = saved })
}

// logged keeps every line Log takes for the rest of the test, and sends each
// to the test's own log too, so that a test can show that a line the
// keystore cannot return was not lost.
func logged(t *testing.T) *[]string {
	t.Helper()
	var lines []string
	saved := Log
	Log = func(format string, a ...any) {
		lines = append(lines, fmt.Sprintf(format, a...))
		t.Logf(format, a...)
	}
	t.Cleanup(func() { Log = saved })
	return &lines
}

// requireOwedAsOpened checks that the handle owes, clause by clause, what a
// fresh Open of the same bytes reads: a commit that failed leaves the handle
// owed exactly as its bytes are (R41), so that RetireRemnantOnly, which
// tells a scrub from a commit, answers on the handle as it would after a
// reopen.
func requireOwedAsOpened(t testing.TB, k *Keystore, path, when string) {
	t.Helper()
	fresh, _ := openedAs(t, snapshot(t, path))
	defer fresh.Close()
	if fresh.owed != k.owed {
		t.Fatalf("%s: the handle owes %+v, a fresh Open %+v", when, k.owed, fresh.owed)
	}
}

// seenWrite is one write a commit, a scrub or zeroOther made.
type seenWrite struct {
	what     string
	off, n   int64
	live     [2]uint64 // the live registry's extent at the moment of the write
	loser    [2]uint64 // the losing copy's, zero when it does not decode
	liveCopy format.Copy
}

// watchWrites records every write through writeSeen, with the named extents
// and the live slot region copy k held at the moment it was made.
func watchWrites(t *testing.T, k *Keystore) *[]seenWrite {
	t.Helper()
	var seen []seenWrite
	restore := SetWriteSeen(func(what string, off, n int64) {
		w := seenWrite{what: what, off: off, n: n, live: extentOf(k.sb), liveCopy: k.sb.LiveSlotRegion()}
		if k.loser != nil {
			w.loser = extentOf(k.loser)
		}
		seen = append(seen, w)
	})
	t.Cleanup(restore)
	return &seen
}

func extentOf(sb *format.KeystoreSuperblock) [2]uint64 {
	return [2]uint64{sb.RegistryOff, sb.RegistryOff + sb.RegistryLen + format.TagSize}
}

// touchesUnitOf reports whether [off, off+n) writes into a 4 KiB unit that
// holds a byte of the extent x.
func touchesUnitOf(off, n int64, x [2]uint64) bool {
	if x[1] <= x[0] {
		return false
	}
	lo, hi := x[0]&^(regUnit-1), (x[1]+regUnit-1)&^(regUnit-1)
	return uint64(off) < hi && lo < uint64(off+n)
}

func writesOf(seen []seenWrite, what string) []seenWrite {
	var out []seenWrite
	for _, w := range seen {
		if w.what == what {
			out = append(out, w)
		}
	}
	return out
}

// metaOf is the Metadata key an Unlocked of this file derives (its Session's
// meta, package-internal): what a holder of that generation's VMK has.
func metaOf(t *testing.T, u *Unlocked) []byte {
	t.Helper()
	s, err := u.Session()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Lock()
	return bytes.Clone(s.meta)
}

// knownPlaintext is the outside review's finding 1 as an attacker: with a
// Metadata key and the plaintext of an earlier registry of the same vault
// (from an earlier copy), it takes every 4 KiB-aligned offset s of the
// registry area as a registry's start and every 16-byte block p at or after
// s as that registry's block (p - s)/16, recovers a counter block by
// AES-decrypting the ciphertext XOR the guessed plaintext, and decrypts the
// file's following bytes in counter mode from it; it returns what it
// decrypted, for a test to search for an archive ID or a wrapped key.
//
// The registry is AES-GCM with a 96-bit nonce, whose block j is enciphered
// under the counter nonce ‖ (j + 2): a recovered counter whose last four bytes
// are the guessed block's number is a guess that was right.
func knownPlaintext(file []byte, meta []byte, earlier []byte) [][]byte {
	block, err := aes.NewCipher(meta)
	if err != nil {
		return nil
	}
	var out [][]byte
	for s := format.RegistryMinOff; s < len(file); s += regUnit {
		for j := 0; 16*(j+1) <= len(earlier) && s+16*(j+1) <= len(file); j++ {
			p := s + 16*j
			var x, ctr [16]byte
			for i := range x {
				x[i] = file[p+i] ^ earlier[16*j+i]
			}
			block.Decrypt(ctr[:], x[:])
			if binary.BigEndian.Uint32(ctr[12:]) != uint32(j+2) {
				continue
			}
			dec := make([]byte, len(file)-p)
			var ks [16]byte
			for q := 0; q < len(dec); q += 16 {
				binary.BigEndian.PutUint32(ctr[12:], uint32(j+2+q/16))
				block.Encrypt(ks[:], ctr[:])
				for i := 0; i < 16 && q+i < len(dec); i++ {
					dec[q+i] = file[p+q+i] ^ ks[i]
				}
			}
			out = append(out, dec)
			break
		}
	}
	return out
}

// foundIn reports whether any decrypted stream holds needle.
func foundIn(streams [][]byte, needle []byte) bool {
	for _, s := range streams {
		if bytes.Contains(s, needle) {
			return true
		}
	}
	return false
}

// holdsSlot reports whether the raw bytes hold the record's recipient ID,
// its epk or its 56-byte wrapped_vmk, which appear in plaintext only in a
// slot region copy (the registry is ciphertext): all three are searched,
// since a torn write can keep one without another.
func holdsSlot(t testing.TB, path string, rec format.SlotRecord) bool {
	t.Helper()
	b := snapshot(t, path)
	return bytes.Contains(b, rec.RecipientID[:]) ||
		(len(rec.EPK) > 0 && bytes.Contains(b, rec.EPK)) ||
		bytes.Contains(b, rec.WrappedVMK[:])
}

// dirtyUnit is the first 4 KiB unit of [0x42000, the file's length by
// os.Stat) that holds a nonzero byte outside the two registry extents the
// superblock copies name, read from the raw bytes and never from the handle;
// -1 when the registry area is clean. A copy that does not decode, or names
// an extent past the file, names nothing.
func dirtyUnit(t testing.TB, path string) int64 {
	t.Helper()
	b := snapshot(t, path)
	var named [][2]uint64
	for _, c := range []format.Copy{format.CopyA, format.CopyB} {
		off := c.KeystoreSuperblockOff()
		sb, err := format.DecodeKeystoreSuperblock(b[off : off+format.SuperblockSize])
		if err != nil {
			continue
		}
		if x := extentOf(sb); x[1] <= uint64(len(b)) {
			named = append(named, x)
		}
	}
	for i := format.RegistryMinOff; i < len(b); i++ {
		if b[i] == 0 {
			continue
		}
		in := false
		for _, x := range named {
			if x[0] <= uint64(i) && uint64(i) < x[1] {
				in = true
			}
		}
		if !in {
			return int64(i) &^ (regUnit - 1)
		}
	}
	return -1
}

func requireClean(t testing.TB, path, when string) {
	t.Helper()
	if u := dirtyUnit(t, path); u >= 0 {
		t.Fatalf("%s: the registry area holds a nonzero byte no superblock names in the unit at 0x%x", when, u)
	}
}

// liveOf is the live superblock of an image and the copy it lies in.
func liveOf(t testing.TB, img []byte) (*format.KeystoreSuperblock, format.Copy) {
	t.Helper()
	a := img[format.KeystoreSuperblockAOff : format.KeystoreSuperblockAOff+format.SuperblockSize]
	b := img[format.KeystoreSuperblockBOff : format.KeystoreSuperblockBOff+format.SuperblockSize]
	sb, c, _, err := format.PickKeystoreSuperblock(a, b)
	if err != nil {
		t.Fatal(err)
	}
	return sb, c
}

// damageLive flips one byte inside the live superblock copy's checksummed
// range, so that a reader opens the file through the other copy.
func damageLive(t testing.TB, img []byte) []byte {
	t.Helper()
	_, c := liveOf(t, img)
	out := bytes.Clone(img)
	out[c.KeystoreSuperblockOff()+100] ^= 0xff
	return out
}

// regionCopy is the 128 KiB of one slot region copy in an image.
func regionCopy(img []byte, c format.Copy) []byte {
	off := c.SlotRegionOff()
	return img[off : off+format.SlotRegionSize]
}

// requireSize checks that FileSize is the file's length, which R41 sets it to
// after every flip.
func requireSize(t testing.TB, k *Keystore, path, when string) {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if uint64(st.Size()) != k.FileSize() {
		t.Fatalf("%s: FileSize %d, the file is %d bytes", when, k.FileSize(), st.Size())
	}
}

// openedAs opens a copy of an image at a path of its own, so that a fresh
// Open can be compared with a handle the test still holds (the lock refuses a
// second handle on one path).
func openedAs(t testing.TB, img []byte) (*Keystore, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "copy.eks")
	restore(t, p, img)
	return mustOpen(t, p), p
}

// retireVault is the set TestRemovalThenRotation builds — a hardware slot
// with the vault's password, recovery "paper" and "spare" — with n archive
// records: the vault most of these tests change and then retire.
type retireVault struct {
	path         string
	u            *Unlocked
	tok          *softToken
	pass         string
	paper, spare kdf.RecoveryKey
	spareRec     format.SlotRecord
}

func newRetireVault(t *testing.T, path string, n int) *retireVault {
	t.Helper()
	v := &retireVault{path: path, tok: newToken(t), pass: "the vault's", paper: recoveryKey(t), spare: recoveryKey(t)}
	v.u = mustCreateWith(t, path, CreateOptions{
		Entangle: &Entangle{Password: v.pass, Argon2: fast},
		Slots: []SlotSpec{
			HardwareSlot{PublicKey: v.tok.PublicKey(), Label: "A"},
			RecoverySlot{Key: v.paper, Label: "paper"},
			RecoverySlot{Key: v.spare, Label: "spare"},
		},
	})
	if n > 0 {
		addArchives(t, v.u, n, 0)
	}
	for _, s := range v.u.k.slots {
		if s.Label == "spare" {
			v.spareRec = s
		}
	}
	if v.u.k.RetireOwed() {
		t.Fatal("a new vault owes a retirement")
	}
	return v
}

// addArchives adds n archive records, each with a description of desc bytes,
// in one registry save, and returns their IDs.
func addArchives(t testing.TB, u *Unlocked, n, desc int) [][16]byte {
	t.Helper()
	var ids [][16]byte
	withSession(t, u, func(s *Session) {
		if err := s.UpdateRegistry(func(g *format.Registry) error {
			for i := 0; i < n; i++ {
				a := archive(t, s, fmt.Sprintf("archive %d of %d", len(g.Archives), n))
				a.Description = string(bytes.Repeat([]byte{'d'}, desc))
				g.Archives = append(g.Archives, a)
				ids = append(ids, a.ArchiveID)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
	return ids
}

// dropArchives removes every archive record whose ID is in drop, in one
// registry save.
func dropArchives(t testing.TB, u *Unlocked, drop [][16]byte) {
	t.Helper()
	gone := map[[16]byte]bool{}
	for _, id := range drop {
		gone[id] = true
	}
	if err := u.UpdateRegistry(func(g *format.Registry) error {
		kept := g.Archives[:0]
		for _, a := range g.Archives {
			if !gone[a.ArchiveID] {
				kept = append(kept, a)
			}
		}
		g.Archives = kept
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// strayRecord is the record a failed commit left in the slot region copy no
// superblock names and that the live set does not hold: the region there is
// found by its own encoding, which ends at its last nonzero byte or within a
// few bytes of it.
func strayRecord(t testing.TB, img []byte, live []format.SlotRecord) format.SlotRecord {
	t.Helper()
	sb, _ := liveOf(t, img)
	c := regionCopy(img, sb.LiveSlotRegion().Other())
	end := len(c)
	for end > 0 && c[end-1] == 0 {
		end--
	}
	for n := end; n <= end+64 && n <= len(c); n++ {
		r, err := format.DecodeSlotRegion(c[:n])
		if err != nil {
			continue
		}
		for _, s := range r.Slots {
			known := false
			for _, l := range live {
				known = known || l.RecipientID == s.RecipientID
			}
			if !known {
				return s
			}
		}
	}
	t.Fatal("no record outside the live set in the other slot region copy")
	return format.SlotRecord{}
}

// 14. The predicate is the file's bytes: a new file and its export owe
// nothing; a change owes; a retirement and every registry save of a retired
// file leave nothing owed; a fresh Open says the same as the handle; and the
// handle's answer follows a commit that failed after writing.
func TestRetireOwedIsReadFromTheBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "v.eks")
	rk := recoveryKey(t)
	u := mustCreate(t, path, PasswordSlot{Password: "p", Argon2: fast}, RecoverySlot{Key: rk})
	k := u.k
	same := func(want bool, when string) {
		t.Helper()
		if got := k.RetireOwed(); got != want {
			t.Fatalf("%s: RetireOwed %v, want %v", when, got, want)
		}
		fresh, _ := openedAs(t, snapshot(t, path))
		if got := fresh.RetireOwed(); got != want {
			t.Fatalf("%s: a fresh Open says %v, the handle %v", when, got, want)
		}
		fresh.Close()
	}
	same(false, "a new file")
	requireSize(t, k, path, "a new file")
	exp := filepath.Join(dir, "backup.eks")
	if err := u.Export(exp); err != nil {
		t.Fatal(err)
	}
	if ek := mustOpen(t, exp); ek.RetireOwed() {
		t.Fatal("a new export owes a retirement")
	} else {
		ek.Close()
	}

	if err := u.AddSlot(RecoverySlot{Key: recoveryKey(t)}); err != nil {
		t.Fatal(err)
	}
	same(true, "after AddSlot")
	requireSize(t, k, path, "after AddSlot")
	if err := u.Retire(); err != nil {
		t.Fatal(err)
	}
	same(false, "after Retire")
	requireSize(t, k, path, "after Retire")

	// Registry-only saves of a retired file: the losing copy is one save
	// behind, at the same generation and naming the live copy, so nothing is
	// owed, including after a save that shrinks the registry at 0x42000.
	ids := addArchives(t, u, 12, 200)
	same(false, "after a save that grows the registry")
	requireSize(t, k, path, "after a growing save")
	dropArchives(t, u, ids[:11])
	if k.sb.RegistryOff != format.RegistryMinOff {
		t.Fatalf("the shrinking save landed at 0x%x, not at 0x42000", k.sb.RegistryOff)
	}
	same(false, "after a save that shrinks the registry at 0x42000")
	requireSize(t, k, path, "after a shrinking save")
	requireClean(t, path, "after a shrinking save")

	// Nothing owed, nothing written.
	before, seq := snapshot(t, path), k.sb.Seq
	if err := u.Retire(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, snapshot(t, path)) || k.sb.Seq != seq {
		t.Fatal("a Retire with nothing owed wrote")
	}

	// A commit that fails after writing a region copy leaves the handle owed
	// at once, and the next registry save carries the retirement.
	restoreFault := faultOn(t, "registry")
	if err := u.AddSlot(RecoverySlot{Key: recoveryKey(t)}); !errors.Is(err, errFault) {
		t.Fatalf("AddSlot with a failing registry write: %v", err)
	}
	restoreFault()
	if !k.RetireOwed() {
		t.Fatal("a failed AddSlot left the handle not owed")
	}
	requireOwedAsOpened(t, k, path, "after a failed AddSlot")
	stray := strayRecord(t, snapshot(t, path), k.slots)
	if !holdsSlot(t, path, stray) {
		t.Fatal("the control: the never-added record is not in the file")
	}
	withSession(t, u, func(s *Session) {
		if err := s.UpdateRegistry(func(g *format.Registry) error { g.IdleMinutes++; return nil }); err != nil {
			t.Fatal(err)
		}
	})
	if k.RetireOwed() {
		t.Fatal("a registry save did not carry the owed retirement")
	}
	if holdsSlot(t, path, stray) {
		t.Fatal("the raw bytes still hold the slot that was never added")
	}
	requireClean(t, path, "after the carried retirement")
	requireSize(t, k, path, "after the carried retirement")
	same(false, "after the carried retirement")

	// A registry-only save that fails at the barrier after writing its
	// registry: owed by the fourth clause alone, past the last flip's end,
	// which Retire clears with a scrub and no commit.
	restoreFault = faultOn(t, "sync")
	withSession(t, u, func(s *Session) {
		// Larger than every registry in the file, so that it lands after the
		// live one and past the end the last flip left.
		if err := s.UpdateRegistry(func(g *format.Registry) error {
			for i := range 20 {
				a := archive(t, s, fmt.Sprintf("lost %d", i))
				a.Description = string(bytes.Repeat([]byte{'x'}, 500))
				g.Archives = append(g.Archives, a)
			}
			return nil
		}); !errors.Is(err, errFault) {
			t.Fatalf("a save failing at sync: %v", err)
		}
	})
	restoreFault()
	if !k.RetireOwed() || !k.RetireRemnantOnly() {
		t.Fatalf("after a failed registry write: owed %v, remnant only %v", k.RetireOwed(), k.RetireRemnantOnly())
	}
	requireOwedAsOpened(t, k, path, "after a failed registry write")
	if k.end <= k.size {
		t.Fatalf("the failed registry did not move the physical end past the last flip's (%d, %d)", k.end, k.size)
	}
	if dirtyUnit(t, path) < 0 {
		t.Fatal("the control: the failed registry left nothing in the registry area")
	}
	seq = k.sb.Seq
	if err := u.Retire(); err != nil {
		t.Fatal(err)
	}
	if k.sb.Seq != seq {
		t.Fatal("a scrub alone made a commit")
	}
	if k.RetireOwed() {
		t.Fatal("still owed after the scrub")
	}
	requireClean(t, path, "after the scrub")
	// No flip, but the size shown is still the file's: the failed registry
	// lengthened the file, and the next flip's trim is what cuts it back.
	requireSize(t, k, path, "after the scrub")
}

// 15. After each act and its Retire, the file holds no state before it: the
// raw bytes keep no withdrawn record, and with the live superblock copy
// damaged the other copy opens the act's own state, where what the act
// withdrew fails. Without Retire the same damage opens the state before,
// where it still works, so the test fails if Retire stops retiring.
//
// SetEntangled, which the design's list does not name, is here too: turning
// the password on writes the slot region, and the state before it opens with
// a YubiKey alone.
func TestRetireLeavesNoPreviousState(t *testing.T) {
	type outcome struct {
		gen       uint64
		withdrawn error // what the withdrawn credential meets in the act's state; nil when nothing was withdrawn
	}
	spareCred := func(v *retireVault) Credential { return RecoveryCredential{Key: v.spare} }
	oldPassword := func(v *retireVault) Credential { return HardwareCredential{Token: v.tok, Password: v.pass} }
	tokenAlone := func(v *retireVault) Credential { return HardwareCredential{Token: v.tok} }
	spare := func(v *retireVault) []format.SlotRecord { return []format.SlotRecord{v.spareRec} }
	recoverySlots := func(v *retireVault) []format.SlotRecord {
		var out []format.SlotRecord
		for _, s := range v.u.k.slots {
			if s.Type == format.SlotRecovery {
				out = append(out, s)
			}
		}
		return out
	}
	// replace makes one new key for each recovery slot, with its label, and
	// replaces them all, as the replacement's ceremony does.
	replace := func(v *retireVault) error {
		var preps []*PreparedRecovery
		defer func() {
			for _, p := range preps {
				p.Zero()
			}
		}()
		for _, label := range []string{"paper", "spare"} {
			r, err := kdf.NewRecoveryKey()
			if err != nil {
				return err
			}
			p, err := v.u.PrepareRecovery(r, label, preps)
			kdf.Zero(r[:])
			if err != nil {
				return err
			}
			preps = append(preps, p)
		}
		return v.u.ReplaceRecoveryAndRotate(preps)
	}
	acts := []struct {
		name     string
		entangle bool
		act      func(v *retireVault) error
		after    outcome
		before   uint64
		// cred is the credential the act withdrew, nil when it withdrew none.
		cred func(v *retireVault) Credential
		// gone are the records the act removed or replaced, read before it,
		// nil when it removed none: the raw bytes hold none of them after
		// Retire, and still hold them without it.
		gone func(v *retireVault) []format.SlotRecord
	}{
		{"Rotate", true, func(v *retireVault) error { return v.u.Rotate() }, outcome{gen: 2}, 1, nil, nil},
		{"RemoveSlot", true, func(v *retireVault) error { return v.u.RemoveSlot(v.spareRec.RecipientID) },
			outcome{gen: 1, withdrawn: ErrVerifier}, 1, spareCred, spare},
		{"ChangeEntangledPassword", true, func(v *retireVault) error {
			return v.u.ChangeEntangledPassword(Entangle{Password: "the new one", Argon2: fast})
		}, outcome{gen: 1, withdrawn: ErrAuth}, 1, oldPassword, nil},
		{"SetEntangled", false, func(v *retireVault) error {
			return v.u.SetEntangled(true, &Entangle{Password: "now on", Argon2: fast})
		}, outcome{gen: 1, withdrawn: ErrPasswordRequired}, 1, tokenAlone, nil},
		{"RemoveSlotAndRotate", true, func(v *retireVault) error { return v.u.RemoveSlotAndRotate(v.spareRec.RecipientID) },
			outcome{gen: 2, withdrawn: ErrVerifier}, 1, spareCred, spare},
		{"ChangeEntangledPasswordAndRotate", true, func(v *retireVault) error {
			return v.u.ChangeEntangledPasswordAndRotate(Entangle{Password: "the new one", Argon2: fast})
		}, outcome{gen: 2, withdrawn: ErrAuth}, 1, oldPassword, nil},
		{"SetEntangledAndRotate", false, func(v *retireVault) error {
			return v.u.SetEntangledAndRotate(Entangle{Password: "now on", Argon2: fast})
		}, outcome{gen: 2, withdrawn: ErrPasswordRequired}, 1, tokenAlone, nil},
		{"ReplaceRecoveryAndRotate", true, replace, outcome{gen: 2, withdrawn: ErrVerifier}, 1, spareCred, recoverySlots},
	}
	for _, a := range acts {
		for _, retire := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/retire=%v", a.name, retire), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "v.eks")
				v := newRetireVault(t, path, 1)
				if !a.entangle {
					// The switch off, for the act that turns it on.
					if err := v.u.SetEntangled(false, nil); err != nil {
						t.Fatal(err)
					}
					if err := v.u.Retire(); err != nil {
						t.Fatal(err)
					}
				}
				var gone []format.SlotRecord
				if a.gone != nil {
					gone = a.gone(v)
				}
				if err := a.act(v); err != nil {
					t.Fatal(err)
				}
				if retire {
					if err := v.u.Retire(); err != nil {
						t.Fatal(err)
					}
				}
				for _, rec := range gone {
					if got := holdsSlot(t, path, rec); got == retire {
						t.Fatalf("the raw bytes hold the withdrawn slot %q: %v", rec.Label, got)
					}
				}
				v.u.Close()
				v.u.k.Close()
				damaged := damageLive(t, snapshot(t, path))
				restore(t, path, damaged)
				k := mustOpen(t, path)
				want := a.after.gen
				if !retire {
					want = a.before
				}
				if k.Generation() != want {
					t.Fatalf("through the other copy: generation %d, want %d", k.Generation(), want)
				}
				if a.name == "Rotate" {
					// What a rotation withdraws is its generation: after the
					// retirement the other copy opens at 2, without it at 1.
					return
				}
				u, err := k.Unlock(a.cred(v))
				if err == nil {
					u.Close()
				}
				if retire && !errors.Is(err, a.after.withdrawn) {
					t.Fatalf("the withdrawn credential through the other copy: %v, want %v", err, a.after.withdrawn)
				}
				if !retire && err != nil {
					t.Fatalf("without Retire the state before should open with the withdrawn credential: %v", err)
				}
			})
		}
	}
}

// 16. The retiring commit failing at each of its steps, after the design's
// change, RemoveSlotAndRotate, and after a removal at the same generation
// and a removal followed by a separate re-key, which place their registries
// differently. The number of archive records decides where the registries
// land: six keep the retiring commit's registry over the pre-change one (the
// placement table's first and second rows), twenty leave the pre-change one
// between the two named registries for the scrub (the second row, its start
// above 0x42000).
func TestRetireFaults(t *testing.T) {
	removal := func(v *retireVault) error { return v.u.RemoveSlot(v.spareRec.RecipientID) }
	combined := func(v *retireVault) error { return v.u.RemoveSlotAndRotate(v.spareRec.RecipientID) }
	changes := []struct {
		name     string
		gen      uint64
		archives int
		do       func(v *retireVault) error
		// remnant is whether the retiring commit, its scrub refused, leaves
		// part of the pre-change registry unnamed between the two named ones.
		remnant bool
	}{
		{"remove", 1, 6, removal, false},
		{"remove, then re-key", 2, 6, func(v *retireVault) error {
			if err := removal(v); err != nil {
				return err
			}
			return v.u.Rotate()
		}, false},
		{"remove, a registry past one unit", 1, 20, removal, true},
		{"remove and re-key", 2, 6, combined, false},
		{"remove and re-key, a registry past one unit", 2, 20, combined, true},
	}
	for _, ch := range changes {
		for _, step := range []string{"scrub", "region", "registry", "sync", "superblock", "flipsync"} {
			t.Run(ch.name+"/"+step, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "v.eks")
				v := newRetireVault(t, path, ch.archives)
				k := v.u.k
				if err := ch.do(v); err != nil {
					t.Fatal(err)
				}
				pre := *k.loser // the state before the change, named by the losing copy
				changeOff := k.sb.RegistryOff
				restoreFault := faultOn(t, step)
				err := v.u.Retire()
				restoreFault()
				switch step {
				case "scrub":
					if !errors.Is(err, errFault) || k.Broken() != nil {
						t.Fatalf("Retire: %v, Broken %v", err, k.Broken())
					}
					// Which row of the placement table the retiring commit's
					// registry took: the first overwrote the change's
					// predecessor from below and leaves its rest to the trim;
					// the second and third leave it unnamed for the scrub.
					rOff := k.sb.RegistryOff
					switch {
					case changeOff == format.RegistryMinOff && changeOff < pre.RegistryOff:
						t.Logf("row one: the change at 0x%x below 0x%x, the retiring commit at 0x%x", changeOff, pre.RegistryOff, rOff)
						st, _ := os.Stat(path)
						if want := max(extentOf(k.sb)[1], extentOf(k.loser)[1]); uint64(st.Size()) != want {
							t.Fatalf("row one: the file is %d bytes, the later named registry ends at %d", st.Size(), want)
						}
					case rOff == format.RegistryMinOff || rOff > changeOff:
						t.Logf("row two or three: the change at 0x%x after 0x%x, the retiring commit at 0x%x, owed %+v", changeOff, pre.RegistryOff, rOff, k.owed)
						if k.owed.needsCommit() {
							t.Fatalf("after the retiring flip only the fourth clause may be owed: %+v", k.owed)
						}
						if dirty := dirtyUnit(t, path) >= 0; dirty != k.RetireOwed() || dirty != ch.remnant {
							t.Fatalf("the handle says owed %v, the bytes %v, want %v", k.RetireOwed(), dirty, ch.remnant)
						}
					default:
						t.Fatalf("no row of the placement table: change at 0x%x, before at 0x%x, retiring at 0x%x", changeOff, pre.RegistryOff, rOff)
					}
					seq := k.sb.Seq
					if err := v.u.Retire(); err != nil {
						t.Fatal(err)
					}
					if k.sb.Seq != seq {
						t.Fatal("the second Retire made a commit")
					}
					requireClean(t, path, "after the second Retire")
					if k.RetireOwed() {
						t.Fatal("still owed after the second Retire")
					}
					return
				case "region", "registry", "sync":
					if !errors.Is(err, errFault) || k.Broken() != nil || !k.RetireOwed() {
						t.Fatalf("Retire: %v, Broken %v, owed %v", err, k.Broken(), k.RetireOwed())
					}
				case "superblock", "flipsync":
					if !errors.Is(err, ErrIndeterminate) || !errors.Is(k.Broken(), ErrIndeterminate) {
						t.Fatalf("Retire: %v, Broken %v", err, k.Broken())
					}
				}
				v.u.Close()
				k.Close()
				img := snapshot(t, path)
				if step == "registry" || step == "sync" {
					// The losing copy's state no longer opens through Enfold's
					// reader: its region copy now holds the live region.
					dp := filepath.Join(t.TempDir(), "damaged.eks")
					restore(t, dp, damageLive(t, img))
					if dk, err := Open(dp); err == nil {
						u, err := dk.Unlock(RecoveryCredential{Key: v.paper})
						if err == nil {
							u.Close()
						}
						dk.Close()
						if err == nil {
							t.Fatal("the losing copy's state still opens")
						}
					}
				}
				k = mustOpen(t, path)
				if k.Generation() != ch.gen {
					t.Fatalf("reopened at generation %d, want %d", k.Generation(), ch.gen)
				}
				switch step {
				case "region", "registry", "sync":
					if !k.RetireOwed() {
						t.Fatal("a fresh Open does not see the owed retirement")
					}
				case "flipsync":
					// The flip landed, its trim and scrub did not: the slot
					// regions and the losing copy owe nothing, and what the
					// scrub would have zeroed may remain.
					if k.owed.needsCommit() {
						t.Fatalf("after a flipped retiring commit: %+v", k.owed)
					}
				case "superblock":
					// The bodies are durable: the other region copy is the
					// live region followed by zeros, so the regions alone say
					// nothing is owed, while the losing copy still names the
					// state before the change.
					if k.owed.liveTail || k.owed.other || !k.owed.loser {
						t.Fatalf("after the superblock write was skipped: %+v", k.owed)
					}
					if k.loser == nil || k.loser.VMKGeneration != pre.VMKGeneration || k.loser.RegistryNonce != pre.RegistryNonce {
						t.Fatal("the losing copy is not the state before the change")
					}
				}
				u, err := k.Unlock(RecoveryCredential{Key: v.paper})
				if err != nil {
					t.Fatal(err)
				}
				defer u.Close()
				if _, err := k.Unlock(RecoveryCredential{Key: v.spare}); !errors.Is(err, ErrVerifier) {
					t.Fatalf("the spare after the change: %v", err)
				}
				losing := k.live.Other()
				liveSeq, nonce := k.sb.Seq, k.loser
				if err := u.Retire(); err != nil {
					t.Fatal(err)
				}
				if k.RetireOwed() || holdsSlot(t, path, v.spareRec) {
					t.Fatalf("after the reopened Retire: owed %v, spare held %v", k.RetireOwed(), holdsSlot(t, path, v.spareRec))
				}
				requireClean(t, path, "after the reopened Retire")
				if step == "superblock" {
					written := sbCopy(t, snapshot(t, path), losing)
					if written.Seq != liveSeq+1 || (nonce != nil && written.RegistryNonce == nonce.RegistryNonce) {
						t.Fatalf("the retiring commit did not write the losing copy: seq %d after %d", written.Seq, liveSeq)
					}
				}
			})
		}
	}
}

// 17. A registry save made while a removal is owed carries the retirement in
// its own flip; one whose carried pass fails before the flip is made again
// without it; one that fails in both changes nothing.
func TestARegistrySaveCarriesAnOwedRetirement(t *testing.T) {
	rename := func(g *format.Registry) error { g.Archives[0].Name = "renamed"; return nil }
	t.Run("carried", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "v.eks")
		v := newRetireVault(t, path, 1)
		k := v.u.k
		if err := v.u.RemoveSlot(v.spareRec.RecipientID); err != nil {
			t.Fatal(err)
		}
		if !k.RetireOwed() || !holdsSlot(t, path, v.spareRec) {
			t.Fatal("the control: a removal with no Retire should owe and hold the spare")
		}
		otherBefore := k.sb.LiveSlotRegion().Other()
		hash := k.reg.SlotRegionHash
		seq := k.sb.Seq
		withSession(t, v.u, func(s *Session) {
			if err := s.UpdateRegistry(rename); err != nil {
				t.Fatal(err)
			}
		})
		if k.RetireOwed() || holdsSlot(t, path, v.spareRec) {
			t.Fatalf("after the save: owed %v, spare held %v", k.RetireOwed(), holdsSlot(t, path, v.spareRec))
		}
		if k.sb.Seq != seq+1 || k.sb.LiveSlotRegion() != otherBefore {
			t.Fatalf("the save is one commit naming the other copy: seq %d after %d, copy %v", k.sb.Seq, seq, k.sb.LiveSlotRegion())
		}
		if k.reg.SlotRegionHash != hash {
			t.Fatal("the carried pass recomputed the slot region hash")
		}
		requireClean(t, path, "after the carried pass")
		v.u.Close()
		k.Close()
		k = mustOpen(t, path)
		u, err := k.Unlock(RecoveryCredential{Key: v.paper})
		if err != nil {
			t.Fatal(err)
		}
		defer u.Close()
		if u.Tampered() != nil || u.Registry().Archives[0].Name != "renamed" {
			t.Fatalf("reopened: tampered %v, name %q", u.Tampered(), u.Registry().Archives[0].Name)
		}
	})
	t.Run("the pass fails before its flip", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "v.eks")
		v := newRetireVault(t, path, 1)
		k := v.u.k
		if err := v.u.RemoveSlot(v.spareRec.RecipientID); err != nil {
			t.Fatal(err)
		}
		other := k.sb.LiveSlotRegion().Other()
		otherBytes := bytes.Clone(regionCopy(snapshot(t, path), other))
		seq := k.sb.Seq
		faultOn(t, "region")
		withSession(t, v.u, func(s *Session) {
			if err := s.UpdateRegistry(rename); err != nil {
				t.Fatalf("the save without its carried pass: %v", err)
			}
		})
		if k.sb.Seq != seq+1 || k.reg.Archives[0].Name != "renamed" {
			t.Fatalf("one commit should land: seq %d after %d", k.sb.Seq, seq)
		}
		if !bytes.Equal(otherBytes, regionCopy(snapshot(t, path), other)) {
			t.Fatal("the other region copy changed")
		}
		if !k.RetireOwed() {
			t.Fatal("no longer owed although the pass did not land")
		}
	})
	t.Run("both attempts fail", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "v.eks")
		v := newRetireVault(t, path, 1)
		k := v.u.k
		if err := v.u.RemoveSlot(v.spareRec.RecipientID); err != nil {
			t.Fatal(err)
		}
		restoreFault := faultOn(t, "registry")
		withSession(t, v.u, func(s *Session) {
			if err := s.UpdateRegistry(rename); !errors.Is(err, errFault) {
				t.Fatalf("the save: %v", err)
			}
		})
		restoreFault()
		if !k.RetireOwed() {
			t.Fatal("not owed after two failed attempts")
		}
		v.u.Close()
		k.Close()
		k = mustOpen(t, path)
		u, err := k.Unlock(RecoveryCredential{Key: v.paper})
		if err != nil {
			t.Fatal(err)
		}
		defer u.Close()
		if u.Registry().Archives[0].Name == "renamed" || !k.RetireOwed() {
			t.Fatalf("reopened: name %q, owed %v", u.Registry().Archives[0].Name, k.RetireOwed())
		}
	})
}

// tamperedOwedVault is test 18's file: a removal left unretired, a registry
// save that failed at the barrier after writing its registry (its carried pass
// refused before it wrote, so that the other region copy keeps the removed
// record), then the live region tampered as TestTamperedRegion does, the
// paper key's public key replaced. Reopened and unlocked with the token. With
// tail set, the live copy also holds a legacy tail past slot_region_len.
// Without failedSave there is no failed save, and the file owes by its other
// region copy and its losing superblock copy alone (and its tail).
func tamperedOwedVault(t *testing.T, tail, failedSave bool) (*retireVault, *Unlocked) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "v.eks")
	v := newRetireVault(t, path, 1)
	if err := v.u.RemoveSlot(v.spareRec.RecipientID); err != nil {
		t.Fatal(err)
	}
	if failedSave {
		// Larger than every registry in the file, so that it runs past the
		// end of both named ones into space neither superblock copy names.
		restoreFault := faultOn(t, "region", "sync")
		withSession(t, v.u, func(s *Session) {
			if err := s.UpdateRegistry(func(g *format.Registry) error {
				for i := range 20 {
					a := archive(t, s, fmt.Sprintf("lost %d", i))
					a.Description = string(bytes.Repeat([]byte{'x'}, 500))
					g.Archives = append(g.Archives, a)
				}
				return nil
			}); !errors.Is(err, errFault) {
				t.Fatalf("the failing save: %v", err)
			}
		})
		restoreFault()
	}
	k := v.u.k
	slots := cloneSlots(k.slots)
	var fake [32]byte
	rand.Read(fake[:])
	fake[31] &= 0x7f
	for i := range slots {
		if slots[i].Label == "paper" {
			slots[i].SlotPubkey = fake[:]
		}
	}
	region, err := (&format.SlotRegion{Header: k.hdr, Slots: slots}).Encode()
	if err != nil || len(region) != len(k.region) {
		t.Fatal("the tampered region does not keep its length")
	}
	off := k.sb.SlotRegionOff
	v.u.Close()
	k.Close()
	img := snapshot(t, path)
	copy(img[off:], region)
	if tail {
		for i := range 300 {
			img[off+uint64(len(region))+uint64(i)] = 0xa5
		}
	}
	restore(t, path, img)
	k = mustOpen(t, path)
	if failedSave && (!k.owed.other || !k.owed.loser || !k.owed.remnant || k.owed.liveTail != tail) ||
		!failedSave && k.owed != (retireState{liveTail: tail, other: true, loser: true}) {
		t.Fatalf("the fixture owes %+v", k.owed)
	}
	u, err := k.Unlock(HardwareCredential{Token: v.tok, Password: v.pass})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(u.Close)
	if !errors.Is(u.Tampered(), ErrTampered) {
		t.Fatalf("the fixture is not tampered: %v", u.Tampered())
	}
	return v, u
}

// 18. A tampered region is never copied forward, and everything else is
// retired: a registry-only commit in place of the retiring one, zeros over
// the region copy no superblock names once that commit has left no copy
// naming it, the scrub.
func TestATamperedRegionIsRetiredWithoutCopyingItForward(t *testing.T) {
	// retired is Retire on a tampered file with no tail in its live copy:
	// one registry-only commit, then one zero write over the region copy
	// neither superblock copy then names, none to the live copy, and nothing
	// owed after, the tampering still reported.
	retired := func(t *testing.T, v *retireVault, u *Unlocked) {
		t.Helper()
		k := u.k
		hash := k.reg.SlotRegionHash
		liveCopy := k.sb.LiveSlotRegion()
		seen := watchWrites(t, k)
		if err := u.Retire(); err != nil {
			t.Fatalf("Retire on a tampered region: %v", err)
		}
		if n := len(writesOf(*seen, "region")); n != 0 {
			t.Fatalf("%d region writes on a tampered region", n)
		}
		if len(writesOf(*seen, "registry")) != 1 || len(writesOf(*seen, "superblock")) != 1 {
			t.Fatalf("want one registry-only commit, saw %+v", *seen)
		}
		zero := writesOf(*seen, "zero")
		other := liveCopy.Other()
		if len(zero) != 1 || zero[0].off != int64(other.SlotRegionOff()) || zero[0].n != format.SlotRegionSize {
			t.Fatalf("want one zero write over the other region copy, saw %+v", zero)
		}
		// The zeros only after the superblock write that left the losing
		// copy naming the live region copy.
		for _, w := range *seen {
			if w.what == "superblock" {
				break
			}
			if w.what == "zero" {
				t.Fatalf("the zeros went over the other copy before the commit's superblock write: %+v", *seen)
			}
		}
		for _, w := range *seen {
			lo := int64(liveCopy.SlotRegionOff())
			if w.off < lo+format.SlotRegionSize && lo < w.off+w.n {
				t.Fatalf("a write reached the live region copy: %+v", w)
			}
		}
		if k.RetireOwed() {
			t.Fatalf("still owed: %+v", k.owed)
		}
		img := snapshot(t, v.path)
		for _, c := range []format.Copy{format.CopyA, format.CopyB} {
			if sbCopy(t, img, c).SlotRegionOff != liveCopy.SlotRegionOff() {
				t.Fatal("a superblock copy does not name the live region copy")
			}
		}
		if holdsSlot(t, v.path, v.spareRec) {
			t.Fatal("the raw bytes still hold the removed record")
		}
		requireClean(t, v.path, "after the tampered retirement")
		if k.reg.SlotRegionHash != hash {
			t.Fatal("the slot region hash moved")
		}
		u.Close()
		k.Close()
		k = mustOpen(t, v.path)
		u2, err := k.Unlock(HardwareCredential{Token: v.tok, Password: v.pass})
		if err != nil {
			t.Fatal(err)
		}
		defer u2.Close()
		if !errors.Is(u2.Tampered(), ErrTampered) {
			t.Fatal("the tampering is no longer reported")
		}
	}
	t.Run("retired", func(t *testing.T) {
		v, u := tamperedOwedVault(t, false, true)
		retired(t, v, u)
	})
	// Owed by its two regions alone, so that what decides the registry-only
	// commit first is the rule that zeros go over the other copy only once
	// no superblock copy names it, not a clause only a commit settles; and
	// zeroOther itself refuses, writing nothing, while the losing copy still
	// names that copy.
	t.Run("owed by its regions alone", func(t *testing.T) {
		v, u := tamperedOwedVault(t, false, false)
		seen := watchWrites(t, u.k)
		if err := u.k.zeroOther(); !errors.Is(err, ErrParams) {
			t.Fatalf("zeroOther while the losing copy names the copy it would zero: %v", err)
		}
		if len(*seen) != 0 {
			t.Fatalf("a refused zeroOther wrote: %+v", *seen)
		}
		if !holdsSlot(t, v.path, v.spareRec) {
			t.Fatal("the control: the other region copy does not hold the removed record")
		}
		retired(t, v, u)
	})
	t.Run("a legacy tail in the live copy", func(t *testing.T) {
		v, u := tamperedOwedVault(t, true, true)
		k := u.k
		liveCopy := k.sb.LiveSlotRegion()
		liveBytes := bytes.Clone(regionCopy(snapshot(t, v.path), liveCopy))
		seen := watchWrites(t, k)
		if err := u.Retire(); !errors.Is(err, ErrTampered) {
			t.Fatalf("Retire with a live tail on a tampered region: %v", err)
		}
		if len(writesOf(*seen, "region")) != 0 || len(writesOf(*seen, "zero")) != 1 {
			t.Fatalf("writes: %+v", *seen)
		}
		if !k.owed.liveTail || k.owed.other || k.owed.loser || k.owed.remnant || k.owed.mixed {
			t.Fatalf("want the first clause alone, owed %+v", k.owed)
		}
		if !bytes.Equal(liveBytes, regionCopy(snapshot(t, v.path), liveCopy)) {
			t.Fatal("the live copy was written")
		}
		// A registry save on a tampered region lands, carries no retiring
		// pass and scrubs what its own flip left.
		*seen = nil
		withSession(t, u, func(s *Session) {
			if err := s.UpdateRegistry(func(g *format.Registry) error { g.IdleMinutes++; return nil }); err != nil {
				t.Fatalf("a registry save on a tampered region: %v", err)
			}
		})
		if len(writesOf(*seen, "region")) != 0 {
			t.Fatal("a registry save carried a retiring pass over a tampered region")
		}
		if !bytes.Equal(liveBytes, regionCopy(snapshot(t, v.path), liveCopy)) {
			t.Fatal("the live copy was written")
		}
		requireClean(t, v.path, "after a save on a tampered region")
	})
}

// 19. A live copy with a tail past slot_region_len, as a writer before R41 can
// leave, takes two retiring commits, each into the copy not live at its time.
func TestRetireZeroesALegacyTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	rk := recoveryKey(t)
	u := mustCreate(t, path, PasswordSlot{Password: "p", Argon2: fast}, RecoverySlot{Key: rk})
	off, n := u.k.sb.SlotRegionOff, u.k.sb.SlotRegionLen
	u.Close()
	u.k.Close()
	img := snapshot(t, path)
	for i := range 700 {
		img[off+n+uint64(i)] = byte(i) | 1
	}
	restore(t, path, img)
	k := mustOpen(t, path)
	if !k.RetireOwed() || !k.owed.liveTail {
		t.Fatalf("a live tail is not owed: %+v", k.owed)
	}
	u, err := k.Unlock(PasswordCredential{Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	first := k.sb.LiveSlotRegion()
	seq := k.sb.Seq
	seen := watchWrites(t, k)
	if err := u.Retire(); err != nil {
		t.Fatal(err)
	}
	if k.sb.Seq != seq+2 {
		t.Fatalf("seq %d after %d: want two commits", k.sb.Seq, seq)
	}
	regions := writesOf(*seen, "region")
	if len(regions) != 2 || regions[0].off != int64(first.Other().SlotRegionOff()) || regions[1].off != int64(first.SlotRegionOff()) {
		t.Fatalf("region writes %+v", regions)
	}
	for _, w := range regions {
		if w.off == int64(w.liveCopy.SlotRegionOff()) {
			t.Fatalf("a region write to the copy live at its time: %+v", w)
		}
	}
	img = snapshot(t, path)
	for _, c := range []format.Copy{format.CopyA, format.CopyB} {
		r := regionCopy(img, c)
		if !bytes.Equal(r[:len(k.region)], k.region) || !allZero(r[len(k.region):]) {
			t.Fatalf("copy %v is not the live region followed by zeros", c)
		}
	}
	if k.RetireOwed() {
		t.Fatal("still owed")
	}
}

// 20. A shorter region written into a copy that held a longer one leaves only
// zeros past its length.
func TestRegionWriteZeroesTheRestOfTheCopy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	u := mustCreate(t, path, RecoverySlot{Key: recoveryKey(t)}, RecoverySlot{Key: recoveryKey(t)}, RecoverySlot{Key: recoveryKey(t)})
	fourth := RecoverySlot{Key: recoveryKey(t)}
	if err := u.AddSlot(fourth); err != nil {
		t.Fatal(err)
	}
	four := u.k.sb.LiveSlotRegion()
	fourLen := u.k.sb.SlotRegionLen
	if err := u.RemoveSlot(u.k.slots[3].RecipientID); err != nil {
		t.Fatal(err)
	}
	if err := u.SetEntangled(true, &Entangle{Password: "on", Argon2: fast}); err != nil {
		t.Fatal(err)
	}
	if u.k.sb.LiveSlotRegion() != four || len(u.k.slots) != 3 || u.k.sb.SlotRegionLen >= fourLen {
		t.Fatalf("the fixture: copy %v, %d slots, length %d after %d", u.k.sb.LiveSlotRegion(), len(u.k.slots), u.k.sb.SlotRegionLen, fourLen)
	}
	r := regionCopy(snapshot(t, path), four)
	if !allZero(r[u.k.sb.SlotRegionLen:]) {
		t.Fatal("the copy keeps bytes past the new slot_region_len")
	}
}

// 22. A retiring write torn after its first 512 bytes stays owed, and the
// record it did not reach is still there until one Retire clears it: after
// the design's RemoveSlotAndRotate, whose losing registry is under the
// generation-1 Metadata key, and after a removal at the same generation.
func TestATornRetiringWriteStaysOwed(t *testing.T) {
	for _, change := range []struct {
		name string
		do   func(v *retireVault) error
	}{
		{"RemoveSlotAndRotate", func(v *retireVault) error { return v.u.RemoveSlotAndRotate(v.spareRec.RecipientID) }},
		{"RemoveSlot", func(v *retireVault) error { return v.u.RemoveSlot(v.spareRec.RecipientID) }},
	} {
		t.Run(change.name, func(t *testing.T) { tornRetiringWrite(t, change.do) })
	}
}

// tornRetiringWrite is test 22 after one change.
func tornRetiringWrite(t *testing.T, change func(v *retireVault) error) {
	path := filepath.Join(t.TempDir(), "v.eks")
	v := newRetireVault(t, path, 1)
	k := v.u.k
	// The losing registry is sealed under the Metadata key of the state
	// before the change.
	meta := metaOf(t, v.u)
	if err := change(v); err != nil {
		t.Fatal(err)
	}
	liveRegion := bytes.Clone(k.region)
	other := k.sb.LiveSlotRegion().Other()
	loser := *k.loser
	v.u.Close()
	k.Close()
	img := snapshot(t, path)
	copy(img[other.SlotRegionOff():], liveRegion[:512])
	restore(t, path, img)
	if at := bytes.Index(regionCopy(img, other), v.spareRec.RecipientID[:]); at < 512 {
		t.Fatalf("the spare's record lies at %d, inside the part written", at)
	}
	k = mustOpen(t, path)
	if !k.RetireOwed() || !holdsSlot(t, path, v.spareRec) {
		t.Fatal("a torn retiring write is not owed, or lost the record past it")
	}
	if k.loser == nil || *k.loser != loser {
		t.Fatal("the losing superblock changed")
	}
	g, _ := registryAEAD(meta)
	ct := snapshot(t, path)[loser.RegistryOff:extentOf(&loser)[1]]
	if _, err := g.Open(nil, loser.RegistryNonce[:], ct, loser.RegistryAAD()); err != nil {
		t.Fatalf("the losing registry no longer opens: %v", err)
	}
	u, err := k.Unlock(RecoveryCredential{Key: v.paper})
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	if err := u.Retire(); err != nil {
		t.Fatal(err)
	}
	if k.RetireOwed() || holdsSlot(t, path, v.spareRec) {
		t.Fatal("one Retire did not clear the torn write")
	}
}

// 23. The third clause on its own: a losing copy at another generation, one
// that fails its checksum, or one whose registry extent does not lie inside
// the file (which the fourth clause then cannot tell from a remnant) is owed
// although both region copies hold the live region; so is one at the live
// generation naming a copy that holds zeros, which the second clause takes
// as retired; one at the live generation naming the live copy is not.
func TestALosingSuperblockAtAnOldGenerationIsOwed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	rk := recoveryKey(t)
	u := mustCreate(t, path, PasswordSlot{Password: "p", Argon2: fast}, RecoverySlot{Key: rk})
	if err := u.Rotate(); err != nil {
		t.Fatal(err)
	}
	if err := u.Retire(); err != nil {
		t.Fatal(err)
	}
	if u.k.Generation() != 2 || u.k.RetireOwed() {
		t.Fatal("the fixture is not a file retired at generation 2")
	}
	u.Close()
	u.k.Close()
	base := snapshot(t, path)
	liveSB, liveC := liveOf(t, base)
	losing := liveC.Other()
	cases := []struct {
		name string
		edit func(img []byte)
		owed bool
	}{
		{"generation 1 naming the other copy", func(img []byte) {
			sb := sbCopy(t, img, losing)
			sb.VMKGeneration = 1
			sb.SlotRegionOff = liveSB.LiveSlotRegion().Other().SlotRegionOff()
			putSB(t, img, losing, sb)
		}, true},
		{"generation 2 naming the live copy", func(img []byte) {
			sb := sbCopy(t, img, losing)
			sb.SlotRegionOff = liveSB.SlotRegionOff
			putSB(t, img, losing, sb)
		}, false},
		{"a checksum that fails", func(img []byte) {
			img[losing.KeystoreSuperblockOff()+100] ^= 0xff
		}, true},
		{"generation 2 naming the other copy, zeroed", func(img []byte) {
			other := liveSB.LiveSlotRegion().Other()
			clear(regionCopy(img, other))
			sb := sbCopy(t, img, losing)
			sb.SlotRegionOff = other.SlotRegionOff()
			putSB(t, img, losing, sb)
		}, true},
		{"generation 2 naming a registry past the file's end", func(img []byte) {
			sb := sbCopy(t, img, losing)
			sb.SlotRegionOff = liveSB.SlotRegionOff
			sb.RegistryOff = (uint64(len(img)) + regUnit) &^ (regUnit - 1)
			putSB(t, img, losing, sb)
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			img := bytes.Clone(base)
			c.edit(img)
			k, p := openedAs(t, img)
			if k.RetireOwed() != c.owed || (c.owed && !k.owed.loser) {
				t.Fatalf("owed %+v, want %v", k.owed, c.owed)
			}
			if k.owed.liveTail || k.owed.other {
				t.Fatalf("the regions owe something: %+v", k.owed)
			}
			u, err := k.Unlock(RecoveryCredential{Key: rk})
			if err != nil {
				t.Fatal(err)
			}
			defer u.Close()
			pred := *k.sb
			seq := k.sb.Seq
			if err := u.Retire(); err != nil {
				t.Fatal(err)
			}
			if k.RetireOwed() {
				t.Fatal("still owed after Retire")
			}
			if !c.owed {
				if k.sb.Seq != seq {
					t.Fatal("a Retire with nothing owed committed")
				}
				return
			}
			got := sbCopy(t, snapshot(t, p), k.live.Other())
			if got.Seq != pred.Seq || got.RegistryNonce != pred.RegistryNonce || got.VMKGeneration != 2 {
				t.Fatal("the losing copy is not the retiring commit's predecessor")
			}
		})
	}
}

// 24. Session.Retire: one retiring commit with the records unchanged and
// only modified_at advanced; on a tampered region, test 18's branch with no
// region write.
func TestSessionRetire(t *testing.T) {
	t.Run("retire", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "v.eks")
		v := newRetireVault(t, path, 2)
		k := v.u.k
		if err := v.u.RemoveSlot(v.spareRec.RecipientID); err != nil {
			t.Fatal(err)
		}
		s, err := v.u.Session()
		if err != nil {
			t.Fatal(err)
		}
		defer s.Lock()
		before, err := cloneRegistry(k.reg)
		if err != nil {
			t.Fatal(err)
		}
		seq := k.sb.Seq
		if err := s.Retire(); err != nil {
			t.Fatal(err)
		}
		if k.sb.Seq != seq+1 || k.RetireOwed() {
			t.Fatalf("seq %d after %d, owed %v", k.sb.Seq, seq, k.RetireOwed())
		}
		if k.reg.ModifiedAt <= before.ModifiedAt {
			t.Fatal("modified_at did not advance")
		}
		after, _ := cloneRegistry(k.reg)
		after.ModifiedAt = before.ModifiedAt
		a, _ := after.Encode()
		b, _ := before.Encode()
		if !bytes.Equal(a, b) {
			t.Fatal("the retiring commit changed more than the date")
		}
	})
	t.Run("tampered", func(t *testing.T) {
		v, u := tamperedOwedVault(t, false, true)
		s, err := u.Session()
		if err != nil {
			t.Fatal(err)
		}
		defer s.Lock()
		seen := watchWrites(t, u.k)
		if err := s.Retire(); err != nil {
			t.Fatalf("Session.Retire on a tampered region: %v", err)
		}
		if len(writesOf(*seen, "region")) != 0 || len(writesOf(*seen, "zero")) != 1 {
			t.Fatalf("writes: %+v", *seen)
		}
		if u.k.RetireOwed() || holdsSlot(t, v.path, v.spareRec) {
			t.Fatal("not retired")
		}
	})
}

// loserAtTheStart leaves a long registry at 0x42000 as the losing copy's and
// the live one above it, so that a short registry written next at 0x42000
// ends in a unit that holds the losing registry's bytes past its tag, and
// lies, padding and all, inside that registry's units. It returns the IDs of
// the thirty records the long registry holds.
func loserAtTheStart(t *testing.T, u *Unlocked) [][16]byte {
	t.Helper()
	ids := addArchives(t, u, 30, 100)
	extra := addArchives(t, u, 1, 0)
	dropArchives(t, u, extra)
	addArchives(t, u, 1, 0)
	if u.k.loser == nil || u.k.loser.RegistryOff != format.RegistryMinOff || u.k.sb.RegistryOff == format.RegistryMinOff {
		t.Fatalf("the fixture: live at 0x%x, losing copy %+v", u.k.sb.RegistryOff, u.k.loser)
	}
	return ids
}

// 25. A registry write zeroes the rest of its last 4 KiB unit, never past the
// file's end and never into the live registry's extent.
func TestARegistryWritePadsItsLastUnit(t *testing.T) {
	t.Run("below the live one", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "v.eks")
		u := mustCreate(t, path, PasswordSlot{Password: "p", Argon2: fast}, RecoverySlot{Key: recoveryKey(t)})
		ids := loserAtTheStart(t, u)
		before := snapshot(t, path)
		dropArchives(t, u, ids[:28])
		sb := u.k.sb
		if sb.RegistryOff != format.RegistryMinOff {
			t.Fatalf("the save landed at 0x%x", sb.RegistryOff)
		}
		tagEnd := extentOf(sb)[1]
		unitEnd := (tagEnd + regUnit - 1) &^ (regUnit - 1)
		if allZero(before[tagEnd:unitEnd]) {
			t.Fatal("the control: the unit held nothing past the new tag before the write")
		}
		if !allZero(snapshot(t, path)[tagEnd:unitEnd]) {
			t.Fatal("the registry's last unit keeps bytes past its tag")
		}
	})
	t.Run("after the live one at the file's end", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "v.eks")
		u := mustCreate(t, path, PasswordSlot{Password: "p", Argon2: fast}, RecoverySlot{Key: recoveryKey(t)})
		end := u.k.end
		seen := watchWrites(t, u.k)
		// The trim refused: padding written past the old end would otherwise
		// be cut away by the flip's trim before the file's length is read,
		// and the length alone would not show it.
		faultOn(t, "trim")
		addArchives(t, u, 3, 0)
		x := extentOf(u.k.sb)
		if x[0] < end {
			t.Fatalf("the fixture: the registry at 0x%x begins before the file's end %d", x[0], end)
		}
		// The write is the sealed bytes and nothing after them.
		w := writesOf(*seen, "registry")
		if len(w) != 1 || uint64(w[0].off) != x[0] || uint64(w[0].off+w[0].n) != x[1] {
			t.Fatalf("the registry write %+v is not its extent [0x%x, 0x%x)", w, x[0], x[1])
		}
		st, _ := os.Stat(path)
		if uint64(st.Size()) != x[1] {
			t.Fatalf("the file is %d bytes, the registry ends at %d", st.Size(), x[1])
		}
	})
	t.Run("a live registry off the grid", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "v.eks")
		rk := recoveryKey(t)
		u := mustCreate(t, path, PasswordSlot{Password: "p", Argon2: fast}, RecoverySlot{Key: rk})
		meta := metaOf(t, u)
		plain, err := u.k.reg.Encode()
		if err != nil {
			t.Fatal(err)
		}
		u.Close()
		u.k.Close()
		// Hand-made, as another writer could lay it out: the registry sealed
		// again at 0x42800, inside the unit a registry at 0x42000 ends in.
		img := snapshot(t, path)
		sb, c := liveOf(t, img)
		const offGrid = format.RegistryMinOff + 0x800
		if uint64(len(plain))+format.TagSize > 0x800 {
			t.Fatalf("the registry of %d bytes does not fit below the off-grid one", len(plain))
		}
		sb.RegistryOff = offGrid
		rand.Read(sb.RegistryNonce[:])
		g, _ := registryAEAD(meta)
		sealed := g.Seal(nil, sb.RegistryNonce[:], plain, sb.RegistryAAD())
		sb.RegistryTag = [format.TagSize]byte(sealed[len(plain):])
		img = append(img[:format.RegistryMinOff], make([]byte, 0x800)...)
		img = append(img, sealed...)
		putSB(t, img, c, sb)
		older := *sb
		older.Seq--
		putSB(t, img, c.Other(), &older)
		restore(t, path, img)
		k := mustOpen(t, path)
		u, err = k.Unlock(RecoveryCredential{Key: rk})
		if err != nil {
			t.Fatal(err)
		}
		defer u.Close()
		if err := u.UpdateRegistry(func(g *format.Registry) error { g.IdleMinutes++; return nil }); err != nil {
			t.Fatal(err)
		}
		if k.sb.RegistryOff != format.RegistryMinOff {
			t.Fatalf("the save landed at 0x%x", k.sb.RegistryOff)
		}
		after := snapshot(t, path)
		if !bytes.Equal(after[offGrid:offGrid+uint64(len(sealed))], sealed) {
			t.Fatal("the padding reached the off-grid registry")
		}
		u.Close()
		k.Close()
		fk, _ := openedAs(t, damageLive(t, after))
		fu, err := fk.Unlock(RecoveryCredential{Key: rk})
		if err != nil {
			t.Fatalf("the off-grid registry no longer opens: %v", err)
		}
		fu.Close()
	})
}

// archiveWithKey is archive with the archive key it wraps returned, so that a
// test can show what an attacker unwraps.
func archiveWithKey(t testing.TB, s *Session, name string) (format.ArchiveRecord, [32]byte) {
	t.Helper()
	var id [16]byte
	rand.Read(id[:])
	var ak [32]byte
	rand.Read(ak[:])
	wrapped, nonce, err := s.WrapArchiveKey(id, id, ak)
	if err != nil {
		t.Fatal(err)
	}
	return format.ArchiveRecord{ArchiveID: id, Name: name, CurrentKID: id, Versions: []format.VersionRecord{{KID: id, WrappedArchiveKey: wrapped, WrapNonce: nonce, State: format.VersionCurrent}}}, ak
}

// attacker is what a holder of an earlier copy of the file and of its VMK
// has: the Metadata key and the KWK of generation 1, and that copy's registry
// plaintext.
type attacker struct {
	meta, kwk []byte
	earlier   []byte
}

func attackerOf(t *testing.T, u *Unlocked) attacker {
	t.Helper()
	earlier, err := u.k.reg.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return attacker{meta: metaOf(t, u), kwk: bytes.Clone(kdf.KWK(u.vmk(), u.k.sb.VaultID)), earlier: earlier}
}

// reads reports whether the attacker finds x's archive ID or its wrapped
// archive key in img; when it finds the wrapped key, it checks that the bytes
// it recovered unwrap under the generation-1 KWK to x's archive key, so that
// a finding is a key read and not a coincidence.
func (a attacker) reads(t *testing.T, img []byte, x format.ArchiveRecord, key [32]byte) bool {
	t.Helper()
	streams := knownPlaintext(img, a.meta, a.earlier)
	v := x.Versions[0]
	for _, s := range streams {
		i := bytes.Index(s, v.WrappedArchiveKey[:])
		if i < 0 {
			continue
		}
		var w [format.WrappedKeySize]byte
		copy(w[:], s[i:])
		got, err := kdf.UnwrapKey(a.kwk, w, v.WrapNonce, format.ArchiveKeyAAD(x.ArchiveID, v.KID))
		if err != nil || got != key {
			t.Fatalf("the attacker's bytes do not unwrap to the archive key: %v", err)
		}
		return true
	}
	return foundIn(streams, x.ArchiveID[:])
}

// unnamedOnly is img with both named registry extents zeroed, so that a scan
// of it searches only what no superblock copy names.
func unnamedOnly(t testing.TB, img []byte) []byte {
	t.Helper()
	out := bytes.Clone(img)
	for _, c := range []format.Copy{format.CopyA, format.CopyB} {
		off := c.KeystoreSuperblockOff()
		sb, err := format.DecodeKeystoreSuperblock(img[off : off+format.SuperblockSize])
		if err != nil {
			continue
		}
		if x := extentOf(sb); x[1] <= uint64(len(out)) {
			clear(out[x[0]:x[1]])
		}
	}
	return out
}

// sizeSlack sets the description of the archive record x so that the
// registry g encodes to a length whose last 4 KiB unit, tag included, keeps
// between lo and hi bytes of slack: what decides whether a registry grown by
// one secrets record still fits below the next 4 KiB boundary.
func sizeSlack(t testing.TB, g *format.Registry, x int, lo, hi uint64) {
	t.Helper()
	for d := 0; d <= format.MaxDescriptionLen; d++ {
		g.Archives[x].Description = string(bytes.Repeat([]byte{'s'}, d))
		b, err := g.Encode()
		if err != nil {
			t.Fatal(err)
		}
		end := uint64(len(b)) + format.TagSize
		if slack := (regUnit - end%regUnit) % regUnit; slack >= lo && slack < hi {
			return
		}
	}
	t.Fatalf("no description length leaves a slack in [%d, %d)", lo, hi)
}

// shrinkFixture is tests 26 and 27's file up to the change: ten archive
// records at generation 1, the attacker's copy taken; archive X added, which
// grows the registry and places it after the live one; then eight records
// removed in two saves, the first still placed above, the second shrinking at
// 0x42000 and leaving the longer registry, X's record in it, in the gap below
// the losing copy's. The last save's slack is set in [lo, hi). With control
// set every scrub from X's save on is refused.
func shrinkFixture(t *testing.T, path string, lo, hi uint64, control bool) (*retireVault, attacker, format.ArchiveRecord, [32]byte) {
	t.Helper()
	v := newRetireVault(t, path, 10)
	att := attackerOf(t, v.u)
	if control {
		faultOn(t, "scrub")
	}
	k := v.u.k
	var x format.ArchiveRecord
	var key [32]byte
	withSession(t, v.u, func(s *Session) {
		if err := s.UpdateRegistry(func(g *format.Registry) error {
			x, key = archiveWithKey(t, s, string(bytes.Repeat([]byte{'X'}, 6000)))
			g.Archives = append(g.Archives, x)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
	grown := k.sb.RegistryOff
	if grown == format.RegistryMinOff {
		t.Fatal("the fixture: adding X did not place the registry after the live one")
	}
	ids := make([][16]byte, 0, 8)
	for _, a := range k.reg.Archives[:8] {
		ids = append(ids, a.ArchiveID)
	}
	dropArchives(t, v.u, ids[:1])
	if k.sb.RegistryOff <= grown {
		t.Fatalf("the fixture: the first removal landed at 0x%x, not above 0x%x", k.sb.RegistryOff, grown)
	}
	if err := v.u.UpdateRegistry(func(g *format.Registry) error {
		g.Archives = g.Archives[7:]
		sizeSlack(t, g, 0, lo, hi)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if k.sb.RegistryOff != format.RegistryMinOff {
		t.Fatalf("the fixture: the second removal landed at 0x%x, not at 0x42000", k.sb.RegistryOff)
	}
	return v, att, x, key
}

// 26. A registry left in the gap by a shrink at 0x42000 is ciphertext an
// attacker with the generation-1 VMK reads, and none of it is left once the
// change and its retirement have landed. The control refuses every scrub and
// finds X's wrapped key in the gap the second removal left.
//
// The design's control looked once the whole sequence had run; under the
// placement rule the change's own registry lands right after the one at
// 0x42000 and its trim cuts everything past the later named registry, so the
// gap is gone by then with or without the scrub. The control therefore looks
// where the gap exists, and tests 27 and 28 carry the controls that survive a
// change. The change is the design's RemoveSlotAndRotate, and a plain Rotate
// beside it, whose registry is one record longer.
func TestNoRemnantAfterAShrink(t *testing.T) {
	changes := []struct {
		name string
		do   func(v *retireVault) error
	}{
		{"RemoveSlotAndRotate", func(v *retireVault) error { return v.u.RemoveSlotAndRotate(v.spareRec.RecipientID) }},
		{"Rotate", func(v *retireVault) error { return v.u.Rotate() }},
	}
	for _, change := range changes {
		for _, control := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/control=%v", change.name, control), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "v.eks")
				v, att, x, key := shrinkFixture(t, path, 200, regUnit, control)
				gap := unnamedOnly(t, snapshot(t, path))
				if found := att.reads(t, gap, x, key); found != control {
					t.Fatalf("in the gap after the shrink: found %v", found)
				}
				if err := change.do(v); err != nil {
					t.Fatal(err)
				}
				err := v.u.Retire()
				if control {
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				requireClean(t, path, "after the retirement")
				if v.u.k.RetireOwed() {
					t.Fatal("still owed")
				}
				if att.reads(t, snapshot(t, path), x, key) {
					t.Fatal("the attacker reads X after the retirement")
				}
			})
		}
	}
}

// 27. As 26, but the registry grows past its slack at the change, so that the
// change's registry and then the retiring commit's land after the live one
// and the pre-change registry lies below them unnamed (2.2.3's placement
// table, third row): the scrub clears it, and without the scrub the attacker
// reads X from it once everything has landed. The change is Rotate, whose
// vmk_history record grows the registry: RemoveSlotAndRotate drops a
// recovery_escrow record of the same length as the record it adds, so its
// registry never grows and cannot reach the third row.
func TestNoRemnantAfterAGrowth(t *testing.T) {
	for _, control := range []bool{false, true} {
		t.Run(fmt.Sprintf("control=%v", control), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "v.eks")
			v, att, x, key := shrinkFixture(t, path, 0, 60, control)
			k := v.u.k
			pre := k.sb.RegistryOff
			if err := v.u.Rotate(); err != nil {
				t.Fatal(err)
			}
			change := k.sb.RegistryOff
			err := v.u.Retire()
			if retiring := k.sb.RegistryOff; pre != format.RegistryMinOff || change <= pre || retiring <= change {
				t.Fatalf("not the third row: before 0x%x, change 0x%x, retiring 0x%x", pre, change, retiring)
			}
			if control {
				if !att.reads(t, snapshot(t, path), x, key) {
					t.Fatal("the control: the attacker does not read X from the pre-change registry")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			requireClean(t, path, "after the retirement")
			if k.RetireOwed() || att.reads(t, snapshot(t, path), x, key) {
				t.Fatal("the pre-change registry is still readable")
			}
		})
	}
}

// 28. A save that fails after writing its registry past the last flip's end
// leaves it in reach of the trim, the padding and the scrub on the same
// handle, with no reopen (rule 8). The change is the design's
// RemoveSlotAndRotate, whose one flip is the one whose trim meets the failed
// registry, and beside it a removal and a separate re-key, the first of whose
// two flips meets it. The control is the attacker reading X from the failed
// registry, the only place X ever was, before the change.
func TestNoRemnantAfterAFailedWrite(t *testing.T) {
	runs := []struct {
		name string
		// setup leaves the live registry where the run needs it.
		setup func(t *testing.T, v *retireVault)
		// shrink drops this many archive records in the failed save beside
		// adding X, so that its registry is shorter than the change's.
		shrink int
		trim   bool
		// where checks where the failed registry and the change's landed.
		where func(t *testing.T, p placed)
	}{
		{"the live registry at 0x42000", func(t *testing.T, v *retireVault) {
			dropArchives(t, v.u, [][16]byte{v.u.k.reg.Archives[0].ArchiveID, v.u.k.reg.Archives[1].ArchiveID})
			if v.u.k.sb.RegistryOff != format.RegistryMinOff {
				t.Fatalf("the fixture: the live registry at 0x%x", v.u.k.sb.RegistryOff)
			}
		}, 3, false, func(t *testing.T, p placed) {
			at := (p.live[1] + regUnit - 1) &^ (regUnit - 1)
			if p.failed[0] != at || p.change[0] != at {
				t.Fatalf("not both at the boundary after the live registry: %+v", p)
			}
		}},
		{"the live registry above 0x42000", func(t *testing.T, v *retireVault) {
			if v.u.k.sb.RegistryOff == format.RegistryMinOff {
				t.Fatal("the fixture: the live registry at 0x42000")
			}
		}, 0, false, func(t *testing.T, p placed) {
			if p.failed[0] < p.live[1] || p.failed[0] < p.change[1] {
				t.Fatalf("the failed registry does not lie past both: %+v", p)
			}
		}},
		{"a failed save longer than the change's", func(t *testing.T, v *retireVault) {
			dropArchives(t, v.u, [][16]byte{v.u.k.reg.Archives[0].ArchiveID, v.u.k.reg.Archives[1].ArchiveID})
		}, 0, false, endsInside},
		{"the trim fails", func(t *testing.T, v *retireVault) {
			dropArchives(t, v.u, [][16]byte{v.u.k.reg.Archives[0].ArchiveID, v.u.k.reg.Archives[1].ArchiveID})
		}, 0, true, endsInside},
	}
	for _, combined := range []bool{true, false} {
		for _, r := range runs {
			change := "RemoveSlotAndRotate"
			if !combined {
				change = "RemoveSlot, then Rotate"
			}
			t.Run(change+"/"+r.name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "v.eks")
				v := newRetireVault(t, path, 8)
				k := v.u.k
				r.setup(t, v)
				att := attackerOf(t, v.u)
				p := placed{live: extentOf(k.sb)}
				seen := watchWrites(t, k)
				var x format.ArchiveRecord
				var key [32]byte
				var sealed uint64 // the failed registry's ciphertext and tag
				restoreFault := faultOn(t, "sync")
				withSession(t, v.u, func(s *Session) {
					if err := s.UpdateRegistry(func(g *format.Registry) error {
						x, key = archiveWithKey(t, s, string(bytes.Repeat([]byte{'X'}, 2000)))
						g.Archives = append(g.Archives[r.shrink:], x)
						// The commit sets only fixed-width fields before it
						// encodes, so the length is the one it seals.
						plain, err := g.Encode()
						sealed = uint64(len(plain)) + format.TagSize
						return err
					}); !errors.Is(err, errFault) {
						t.Fatalf("the failing save: %v", err)
					}
				})
				restoreFault()
				if o := k.owed; !(o.remnant || o.mixed) || o.liveTail || o.other || o.loser || k.end <= k.size {
					t.Fatalf("after the failed save: owed %+v, end %d, size %d", k.owed, k.end, k.size)
				}
				requireOwedAsOpened(t, k, path, "after the failed save")
				if !att.reads(t, snapshot(t, path), x, key) {
					t.Fatal("the control: the attacker does not read X from the failed registry")
				}
				// Written past the end the last flip left, so not padded: the
				// write is the sealed bytes and nothing after them, and the
				// physical end is raised to its end.
				w := writesOf(*seen, "registry")
				p.failed = [2]uint64{uint64(w[len(w)-1].off), uint64(w[len(w)-1].off + w[len(w)-1].n)}
				if p.failed[1]-p.failed[0] != sealed || p.failed[1] != k.end || p.failed[1] <= k.size {
					t.Fatalf("the failed registry of %d sealed bytes written at [0x%x, 0x%x), end %d, size %d", sealed, p.failed[0], p.failed[1], k.end, k.size)
				}
				if r.trim {
					faultOn(t, "trim")
				}
				flipped := func(when string) {
					t.Helper()
					requireSize(t, k, path, when)
					if r.trim {
						return
					}
					st, _ := os.Stat(path)
					if want := max(extentOf(k.sb)[1], extentOf(k.loser)[1]); uint64(st.Size()) != want {
						t.Fatalf("%s: the file is %d bytes, the later named registry ends at %d", when, st.Size(), want)
					}
				}
				if combined {
					if err := v.u.RemoveSlotAndRotate(v.spareRec.RecipientID); err != nil {
						t.Fatal(err)
					}
				} else if err := v.u.RemoveSlot(v.spareRec.RecipientID); err != nil {
					t.Fatal(err)
				}
				p.change = extentOf(k.sb)
				r.where(t, p)
				flipped("after the change's flip")
				requireClean(t, path, "after the change's flip")
				if !combined {
					if err := v.u.Rotate(); err != nil {
						t.Fatal(err)
					}
					flipped("after the rotation's flip")
				}
				if err := v.u.Retire(); err != nil {
					t.Fatal(err)
				}
				requireClean(t, path, "after the retirement")
				requireSize(t, k, path, "after the retirement")
				if k.RetireOwed() || att.reads(t, snapshot(t, path), x, key) {
					t.Fatal("the failed registry is still readable")
				}
			})
		}
	}
}

// placed is where test 28's registries lay: the live one before the failed
// save, the failed one, and the change's first.
type placed struct{ live, failed, change [2]uint64 }

// endsInside is test 28's third placement: the change's registry, written at
// the failed one's target, is the shorter and ends inside it.
func endsInside(t *testing.T, p placed) {
	t.Helper()
	if p.change[0] != p.failed[0] || p.change[1] >= p.failed[1] {
		t.Fatalf("the change's registry does not end inside the failed one: %+v", p)
	}
}

// 29. Across sixty saves whose registries grow and shrink at random, with a
// rotation and its retirement every ten, no scrub write touches a 4 KiB unit
// that held a byte of either named registry at its moment, none lies below
// 0x42000 or past the file's end, the registry area is clean after every
// commit, and every state still opens through its losing superblock copy.
func TestTheScrubNeverWritesANamedUnit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	rk := recoveryKey(t)
	u := mustCreate(t, path, PasswordSlot{Password: "p", Argon2: fast}, RecoverySlot{Key: rk})
	k := u.k
	seen := watchWrites(t, k)
	rng := mrand.New(mrand.NewPCG(29, 2026))
	scrubbed := 0
	check := func(when string) {
		t.Helper()
		for _, w := range writesOf(*seen, "scrub") {
			scrubbed++
			if w.off < format.RegistryMinOff || uint64(w.off+w.n) > k.FileSize() {
				t.Fatalf("%s: a scrub write at [0x%x, 0x%x) outside the registry area", when, w.off, w.off+w.n)
			}
			if touchesUnitOf(w.off, w.n, w.live) || touchesUnitOf(w.off, w.n, w.loser) {
				t.Fatalf("%s: a scrub write at 0x%x touched a named registry's unit", when, w.off)
			}
		}
		*seen = nil
		requireClean(t, path, when)
		requireSize(t, k, path, when)
		fk, _ := openedAs(t, damageLive(t, snapshot(t, path)))
		fu, err := fk.Unlock(RecoveryCredential{Key: rk})
		if err != nil {
			t.Fatalf("%s: the losing copy does not open: %v", when, err)
		}
		fu.Close()
		fk.Close()
	}
	for i := 1; i <= 60; i++ {
		withSession(t, u, func(s *Session) {
			if err := s.UpdateRegistry(func(g *format.Registry) error {
				if n := len(g.Archives); n > 0 {
					drop := rng.IntN(n + 1)
					g.Archives = g.Archives[drop:]
				}
				for range rng.IntN(8) {
					a := archive(t, s, fmt.Sprintf("save %d", i))
					a.Description = string(bytes.Repeat([]byte{'r'}, rng.IntN(format.MaxDescriptionLen)))
					g.Archives = append(g.Archives, a)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
		check(fmt.Sprintf("save %d", i))
		if i%10 == 0 {
			if err := u.Rotate(); err != nil {
				t.Fatal(err)
			}
			check(fmt.Sprintf("the rotation after save %d", i))
			if err := u.Retire(); err != nil {
				t.Fatal(err)
			}
			check(fmt.Sprintf("the retirement after save %d", i))
		}
	}
	if scrubbed == 0 {
		t.Fatal("the control: no save left a unit for the scrub, so nothing was shown")
	}
	t.Logf("%d scrub writes across the sequence", scrubbed)
}

// 30. A scrub cut short leaves the file owed by the fourth clause, and the
// next Retire, on a fresh Open, finishes it with no commit. A save whose
// scrub after its flip is refused still lands, and leaves the handle owed and
// a line in the log (R41, rule 6).
func TestAnInterruptedScrubStaysOwed(t *testing.T) {
	t.Run("a torn scrub", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "v.eks")
		rk := recoveryKey(t)
		u := mustCreate(t, path, PasswordSlot{Password: "p", Argon2: fast}, RecoverySlot{Key: rk})
		addArchives(t, u, 20, 0) // past one unit, above 0x42000
		if err := u.Rotate(); err != nil {
			t.Fatal(err)
		}
		restoreFault := faultOn(t, "scrub")
		if err := u.Retire(); !errors.Is(err, errFault) {
			t.Fatalf("the retiring commit with its scrub refused: %v", err)
		}
		restoreFault()
		unit := dirtyUnit(t, path)
		if unit < 0 {
			t.Fatal("the fixture: the retiring commit left no unnamed unit")
		}
		u.Close()
		u.k.Close()
		img := snapshot(t, path)
		var nonzero []int64
		for i := unit; i < min(unit+regUnit, int64(len(img))); i++ {
			if img[i] != 0 {
				nonzero = append(nonzero, i)
			}
		}
		for _, i := range nonzero[:len(nonzero)/2] {
			img[i] = 0
		}
		restore(t, path, img)
		k := mustOpen(t, path)
		if !k.RetireOwed() || !k.RetireRemnantOnly() {
			t.Fatalf("a torn scrub: %+v", k.owed)
		}
		u, err := k.Unlock(RecoveryCredential{Key: rk})
		if err != nil {
			t.Fatal(err)
		}
		defer u.Close()
		seq := k.sb.Seq
		if err := u.Retire(); err != nil {
			t.Fatal(err)
		}
		if k.sb.Seq != seq || k.RetireOwed() {
			t.Fatalf("seq %d after %d, owed %v", k.sb.Seq, seq, k.RetireOwed())
		}
		requireClean(t, path, "after the scrub")
	})
	t.Run("a save whose scrub is refused", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "v.eks")
		u := mustCreate(t, path, PasswordSlot{Password: "p", Argon2: fast}, RecoverySlot{Key: recoveryKey(t)})
		k := u.k
		ids := loserAtTheStart(t, u)
		losing := extentOf(k.loser)
		restoreFault := faultOn(t, "scrub")
		lines := logged(t)
		dropArchives(t, u, ids[:28])
		restoreFault()
		// The control: the save's registry, padding and all, lies inside
		// the losing registry's units, so that its own write made nothing
		// owed; what is owed is the rest of that registry, which its flip
		// left unnamed and its refused scrub left in place.
		x := extentOf(k.sb)
		if x[0] != format.RegistryMinOff || (x[1]+regUnit-1)&^(regUnit-1) > losing[1] {
			t.Fatalf("the fixture: the save at [0x%x, 0x%x), the losing registry at [0x%x, 0x%x)", x[0], x[1], losing[0], losing[1])
		}
		if dirtyUnit(t, path) < 0 {
			t.Fatal("the control: the flip left no unnamed bytes")
		}
		if !k.RetireOwed() || !k.RetireRemnantOnly() || k.Broken() != nil {
			t.Fatalf("a save whose scrub was refused: owed %+v, broken %v", k.owed, k.Broken())
		}
		requireOwedAsOpened(t, k, path, "a save whose scrub was refused")
		if len(*lines) != 1 || !strings.Contains((*lines)[0], errFault.Error()) {
			t.Fatalf("logged %q", *lines)
		}
		seq := k.sb.Seq
		if err := u.Retire(); err != nil {
			t.Fatal(err)
		}
		if k.sb.Seq != seq || k.RetireOwed() {
			t.Fatalf("seq %d after %d, owed %v", k.sb.Seq, seq, k.RetireOwed())
		}
		requireClean(t, path, "after the scrub")
	})
}

// 31. A legacy registry whose last unit holds an older record's bytes past
// its tag owes a retirement the scrub cannot make: the unit is named. Two
// retiring commits leave it unnamed, and their scrubs clear it.
func TestALegacyRegistrySlackTakesTwoPasses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	rk := recoveryKey(t)
	u := mustCreate(t, path, PasswordSlot{Password: "p", Argon2: fast}, RecoverySlot{Key: rk})
	end := extentOf(u.k.sb)[1]
	u.Close()
	u.k.Close()
	img := snapshot(t, path)
	if uint64(len(img)) != end || end%regUnit+96 > regUnit {
		t.Fatalf("the fixture: the file ends at %d, its registry at %d", len(img), end)
	}
	// What a writer before R41 could leave there: an older registry's bytes
	// past the tag of the one now live, inside its last unit.
	legacy := make([]byte, 96)
	rand.Read(legacy)
	img = append(img, legacy...)
	restore(t, path, img)
	k := mustOpen(t, path)
	if o := k.owed; !o.mixed || o.remnant || o.liveTail || o.other || o.loser {
		t.Fatalf("a legacy slack: %+v", k.owed)
	}
	seen := watchWrites(t, k)
	if err := k.scrub(); err != nil {
		t.Fatal(err)
	}
	slackUnit := int64(end) &^ (regUnit - 1)
	for _, w := range writesOf(*seen, "scrub") {
		if w.off < slackUnit+regUnit && slackUnit < w.off+w.n {
			t.Fatal("the scrub wrote a unit a named registry holds")
		}
	}
	if !k.owed.mixed {
		t.Fatal("the scrub cleared a mixed unit")
	}
	u, err := k.Unlock(PasswordCredential{Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	seq := k.sb.Seq
	*seen = nil
	if err := u.Retire(); err != nil {
		t.Fatal(err)
	}
	if k.sb.Seq != seq+2 {
		t.Fatalf("seq %d after %d: want two commits", k.sb.Seq, seq)
	}
	for _, w := range *seen {
		switch w.what {
		case "scrub":
			if touchesUnitOf(w.off, w.n, w.live) || touchesUnitOf(w.off, w.n, w.loser) {
				t.Fatalf("a scrub write at 0x%x touched a named registry's unit", w.off)
			}
		case "registry":
			if uint64(w.off) < w.live[1] && w.live[0] < uint64(w.off+w.n) {
				t.Fatalf("a registry write at 0x%x reached the live registry", w.off)
			}
		}
	}
	requireClean(t, path, "after two passes")
	if k.RetireOwed() {
		t.Fatalf("still owed: %+v", k.owed)
	}
}

// 33. The scrub reads the superblocks again first: a writer that ignored the
// lock and committed since the flip gets no zeros over its units, and the
// handle is Broken with ErrConflict, the flip that preceded it having landed.
func TestTheScrubChecksTheDiskFirst(t *testing.T) {
	// interloper, armed, writes by hand a valid superblock copy with a higher
	// seq into the losing copy at the scrub's start, as a writer that
	// ignores the lock would.
	interloper := func(t *testing.T, k *Keystore) (arm func()) {
		armed := false
		restore := SetCommitFault(func(step string) error {
			if step != "scrub" || !armed {
				return nil
			}
			armed = false
			sb := *k.sb
			sb.Seq = k.diskSeq + 1
			enc, err := sb.Encode()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := k.f.WriteAt(enc, int64(k.live.Other().KeystoreSuperblockOff())); err != nil {
				t.Fatal(err)
			}
			return nil
		})
		t.Cleanup(restore)
		return func() { armed = true }
	}
	t.Run("a save", func(t *testing.T) {
		quietLog(t)
		path := filepath.Join(t.TempDir(), "v.eks")
		v := newRetireVault(t, path, 1)
		k := v.u.k
		arm := interloper(t, k)
		seen := watchWrites(t, k)
		s, err := v.u.Session()
		if err != nil {
			t.Fatal(err)
		}
		defer s.Lock()
		arm()
		if err := s.UpdateRegistry(func(g *format.Registry) error { g.IdleMinutes++; return nil }); err != nil {
			t.Fatalf("a save whose flip landed: %v", err)
		}
		if n := len(writesOf(*seen, "scrub")); n != 0 {
			t.Fatalf("the scrub wrote %d units over another writer's commit", n)
		}
		if !errors.Is(k.Broken(), ErrConflict) {
			t.Fatalf("Broken: %v", k.Broken())
		}
		if err := s.UpdateRegistry(func(g *format.Registry) error { g.IdleMinutes++; return nil }); !errors.Is(err, ErrConflict) {
			t.Fatalf("the next save: %v", err)
		}
	})
	t.Run("Retire", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "v.eks")
		v := newRetireVault(t, path, 1)
		k := v.u.k
		if err := v.u.RemoveSlot(v.spareRec.RecipientID); err != nil {
			t.Fatal(err)
		}
		arm := interloper(t, k)
		seen := watchWrites(t, k)
		arm()
		if err := v.u.Retire(); !errors.Is(err, ErrConflict) {
			t.Fatalf("Retire: %v", err)
		}
		if n := len(writesOf(*seen, "scrub")); n != 0 {
			t.Fatalf("the scrub wrote %d units over another writer's commit", n)
		}
	})
}

// flushes counts the scrub's fsyncs through commitFault's "scrubsync" step,
// and fails each one while fail is set. That fsync is the barrier that makes
// a trim and the scrub's own zeros durable; a test cannot cut the power, so
// what it shows is whether the barrier was made, and what the handle says
// while it has not succeeded.
type flushes struct {
	asked int
	fail  bool
}

func watchFlushes(t *testing.T) *flushes {
	t.Helper()
	f := &flushes{}
	restore := SetCommitFault(func(step string) error {
		if step != "scrubsync" {
			return nil
		}
		f.asked++
		if f.fail {
			return fmt.Errorf("%w at %s", errFault, step)
		}
		return nil
	})
	t.Cleanup(restore)
	return f
}

// The outside review of the diff, findings 1 and 2: a retirement has not
// landed until the fsync after its trim and its zeros has succeeded (R41).
// Bytes the cache reads as retired say nothing of the disk, so neither the
// reread after a retiring commit nor a retry that finds nothing left to
// write takes them as retired, and the retry makes the fsync all the same.
// In each run the fsync fails once where the flip's scrub makes it, fails
// again on a retry that writes nothing, and then succeeds on a retry that
// makes no commit, after which nothing is owed.
func TestARetirementLandsOnlyOnceFlushed(t *testing.T) {
	// retries checks the two retries: owed after the first, whose fsync
	// fails; nothing owed after the second; neither writes a unit nor makes
	// a commit, and each asks for the fsync.
	retries := func(t *testing.T, u *Unlocked, path string, fl *flushes, seen *[]seenWrite) {
		t.Helper()
		k := u.k
		zeroed, seq, asked := len(writesOf(*seen, "scrub")), k.sb.Seq, fl.asked
		fl.fail = true
		if err := u.Retire(); !errors.Is(err, errFault) || !k.RetireOwed() || !k.RetireRemnantOnly() {
			t.Fatalf("a retry over cached zeros whose fsync failed: %v, owed %v", err, k.RetireOwed())
		}
		fl.fail = false
		if err := u.Retire(); err != nil {
			t.Fatal(err)
		}
		if fl.asked != asked+2 || len(writesOf(*seen, "scrub")) != zeroed || k.sb.Seq != seq || k.RetireOwed() || k.Broken() != nil {
			t.Fatalf("the retries: %d fsyncs asked after %d, %d scrub writes after %d, seq %d after %d, owed %v, Broken %v",
				fl.asked, asked, len(writesOf(*seen, "scrub")), zeroed, k.sb.Seq, seq, k.RetireOwed(), k.Broken())
		}
		requireClean(t, path, "after the retries")
		requireSize(t, k, path, "after the retries")
	}

	t.Run("the retiring commit's zeros", func(t *testing.T) {
		quietLog(t)
		path := filepath.Join(t.TempDir(), "v.eks")
		u := mustCreate(t, path, PasswordSlot{Password: "p", Argon2: fast}, RecoverySlot{Key: recoveryKey(t)})
		defer u.Close()
		k := u.k
		addArchives(t, u, 20, 0) // past one unit, above 0x42000
		if err := u.Rotate(); err != nil {
			t.Fatal(err)
		}
		seen := watchWrites(t, k)
		fl := watchFlushes(t)
		fl.fail = true
		if err := u.Retire(); !errors.Is(err, errFault) || k.Broken() != nil {
			t.Fatalf("a retiring commit whose scrub's fsync failed: %v, Broken %v", err, k.Broken())
		}
		// The control: the scrub wrote and asked for its fsync once, and
		// its zeros are in the cache, which is what the reread after the
		// retiring commit reads.
		if n := len(writesOf(*seen, "scrub")); n == 0 || fl.asked != 1 {
			t.Fatalf("the fixture: %d scrub writes, %d fsyncs asked", n, fl.asked)
		}
		requireClean(t, path, "the scrub's zeros in the cache")
		if !k.RetireOwed() || !k.RetireRemnantOnly() {
			t.Fatalf("taken as retired with its fsync failed: owed %v, remnant only %v, clauses %+v", k.RetireOwed(), k.RetireRemnantOnly(), k.owed)
		}
		// What the bytes owe is what a fresh Open of them says, nothing:
		// the fsync is owed beside the clauses, never as one of them.
		requireOwedAsOpened(t, k, path, "the scrub's fsync failed")
		retries(t, u, path, fl, seen)
	})

	t.Run("a save's zeros", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "v.eks")
		u := mustCreate(t, path, PasswordSlot{Password: "p", Argon2: fast}, RecoverySlot{Key: recoveryKey(t)})
		defer u.Close()
		k := u.k
		ids := loserAtTheStart(t, u)
		seen := watchWrites(t, k)
		fl := watchFlushes(t)
		fl.fail = true
		lines := logged(t)
		dropArchives(t, u, ids[:28])
		// The control, as test 30's: the save's flip left the rest of the
		// losing registry unnamed, and its scrub zeroed it in the cache.
		if n := len(writesOf(*seen, "scrub")); n == 0 || fl.asked != 1 {
			t.Fatalf("the fixture: %d scrub writes, %d fsyncs asked", n, fl.asked)
		}
		requireClean(t, path, "the save's zeros in the cache")
		if !k.RetireOwed() || !k.RetireRemnantOnly() || k.Broken() != nil {
			t.Fatalf("a save whose scrub's fsync failed: owed %v, remnant only %v, clauses %+v, Broken %v", k.RetireOwed(), k.RetireRemnantOnly(), k.owed, k.Broken())
		}
		if len(*lines) != 1 || !strings.Contains((*lines)[0], errFault.Error()) {
			t.Fatalf("logged %q", *lines)
		}
		retries(t, u, path, fl, seen)
	})

	t.Run("a trim", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "v.eks")
		u := mustCreate(t, path, PasswordSlot{Password: "p", Argon2: fast}, RecoverySlot{Key: recoveryKey(t)})
		defer u.Close()
		k := u.k
		// A long registry after the first one, then a short one back at
		// 0x42000: the long one, now the losing copy's, ends the file.
		ids := addArchives(t, u, 30, 100)
		dropArchives(t, u, ids)
		before := snapshot(t, path)
		seen := watchWrites(t, k)
		fl := watchFlushes(t)
		fl.fail = true
		lines := logged(t)
		// A save as short, placed after the live one over the long one's
		// first unit: its flip leaves the long one unnamed, and the trim
		// cuts the file at the save's tag.
		dropArchives(t, u, nil)
		after := snapshot(t, path)
		if x := extentOf(k.sb); k.loser.RegistryOff != format.RegistryMinOff || uint64(len(after)) != x[1] ||
			len(after) >= len(before) || allZero(before[len(after):]) {
			t.Fatalf("the fixture: the save at [0x%x, 0x%x), the file %d bytes after %d", x[0], x[1], len(after), len(before))
		}
		// The control: the scrub had nothing to write, so the cut is all
		// the fsync makes durable, and the registry ciphertext past it is
		// what a power loss before it can bring back.
		if n := len(writesOf(*seen, "scrub")); n != 0 {
			t.Fatalf("the fixture: %d scrub writes", n)
		}
		if fl.asked != 1 {
			t.Fatalf("the trim's cut was followed by %d fsyncs, want 1", fl.asked)
		}
		if !k.RetireOwed() || !k.RetireRemnantOnly() || k.Broken() != nil {
			t.Fatalf("a save whose trim was not made durable: owed %v, remnant only %v, clauses %+v, Broken %v", k.RetireOwed(), k.RetireRemnantOnly(), k.owed, k.Broken())
		}
		if len(*lines) != 1 || !strings.Contains((*lines)[0], errFault.Error()) {
			t.Fatalf("logged %q", *lines)
		}
		retries(t, u, path, fl, seen)
	})
}

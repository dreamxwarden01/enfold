package keystore

import (
	"bytes"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/dreamxwarden01/enfold/internal/format"
)

// What the format rulings of 2026-09-13 ask of this package (DECISIONS;
// FORMAT.md §4, §7, R36): the superblock's seq is in the registry's AAD, so a
// registry cannot be promoted by raising the plaintext number a reader takes
// for freshness; and the writer refuses a commit that would wrap the sequence
// at 2^64 − 1 rather than let the new state lose to the old one.

// sbCopy reads one superblock copy out of a keystore image.
func sbCopy(t testing.TB, img []byte, c format.Copy) *format.KeystoreSuperblock {
	t.Helper()
	off := c.KeystoreSuperblockOff()
	sb, err := format.DecodeKeystoreSuperblock(img[off : off+format.SuperblockSize])
	if err != nil {
		t.Fatalf("the superblock copy at 0x%x: %v", off, err)
	}
	return sb
}

// putSB writes a superblock into copy c of an image.
func putSB(t testing.TB, img []byte, c format.Copy, sb *format.KeystoreSuperblock) {
	t.Helper()
	enc, err := sb.Encode()
	if err != nil {
		t.Fatal(err)
	}
	copy(img[c.KeystoreSuperblockOff():], enc)
}

// The forgery the AAD's seq exists to stop, one level up from the archive's:
// the registry that is there, under a plaintext seq raised to the number a
// reader expects of the current copy — the superblock is checksummed and not
// authenticated, so the checksum costs nothing. The registry authenticates
// under the number it was sealed with and under one more (§4's copy B), so the
// promoted copy fails to unlock instead of passing as current (§7, R36).
func TestARaisedSeqDoesNotPromoteARegistry(t *testing.T) {
	const password = "the password"
	path := filepath.Join(t.TempDir(), "v.eks")
	u := mustCreate(t, path, PasswordSlot{Password: password, Argon2: fast}, RecoverySlot{Key: recoveryKey(t)})
	u.Close()
	u.k.Close()
	good, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// A fresh file is copy A at seq 1 and copy B at 0 (§4), so A is the one a
	// forger raises.
	forged := bytes.Clone(good)
	sb := sbCopy(t, forged, format.CopyA)
	if sb.Seq != 1 || sbCopy(t, forged, format.CopyB).Seq != 0 {
		t.Fatalf("a fresh file is at seq %d beside %d", sb.Seq, sbCopy(t, forged, format.CopyB).Seq)
	}
	sb.Seq += 2 // past copy B's, and past the one commit the tolerance allows
	putSB(t, forged, format.CopyA, sb)
	if err := os.WriteFile(path, forged, 0o600); err != nil {
		t.Fatal(err)
	}

	// The superblock is only checksummed, so the file opens; the registry is
	// where the forgery is caught, and it is caught as authentication rather
	// than as a wrong credential.
	k := mustOpen(t, path)
	_, err = k.Unlock(PasswordCredential{Password: password})
	// The slot opened and the VMK is this vault's; what failed is the
	// registry under it, which the unlock reports as corruption of the file
	// rather than as a credential that does not fit.
	if !errors.Is(err, format.ErrInvalid) {
		t.Fatalf("a promoted registry unlocked: %v", err)
	}
	if errors.Is(err, ErrVerifier) || errors.Is(err, ErrNoSlot) {
		t.Errorf("the forgery was reported as a wrong password: %v", err)
	}
	k.Close()

	// Untouched, the same file unlocks.
	if err := os.WriteFile(path, good, 0o600); err != nil {
		t.Fatal(err)
	}
	k2 := mustOpen(t, path)
	u2, err := k2.Unlock(PasswordCredential{Password: password})
	if err != nil {
		t.Fatalf("the restored file did not unlock: %v", err)
	}
	u2.Close()
	k2.Close()
}

// §4's initialisation is a promise as well as a shape: a new file's copy B
// holds copy A's superblock at seq 0, so a fresh file has no damage to report
// and, with copy A lost, still opens. The registry it names was sealed at seq
// 1, which is the one tolerance §7 allows — a copy naming a state one commit
// newer than its own number, never an older one.
func TestAFreshFilesOlderCopyStillOpens(t *testing.T) {
	const password = "the password"
	path := filepath.Join(t.TempDir(), "v.eks")
	u := mustCreate(t, path, PasswordSlot{Password: password, Argon2: fast}, RecoverySlot{Key: recoveryKey(t)})
	u.Close()
	u.k.Close()
	img, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	off := format.CopyA.KeystoreSuperblockOff()
	clear(img[off : off+format.SuperblockSize])
	if err := os.WriteFile(path, img, 0o600); err != nil {
		t.Fatal(err)
	}

	k := mustOpen(t, path)
	if k.Stale == nil {
		t.Error("the zeroed copy A went unreported")
	}
	u2, err := k.Unlock(PasswordCredential{Password: password})
	if err != nil {
		t.Fatalf("a fresh file's copy B did not open: %v", err)
	}
	if u2.Tampered() != nil {
		t.Errorf("the fallback reported tampering: %v", u2.Tampered())
	}
	u2.Close()
	k.Close()
}

// An unlock that leaned on the tolerance adopts the number the registry
// authenticated under — the state's, not the copy's — so the next commit
// seals at that number plus one. Otherwise the writer would seal a second
// state under a number an older copy of the file already holds, and the file
// it replaced could be put back as the current one: valid, openable, and at
// the same seq (§7, "the state's number").
func TestATolerantUnlockContinuesFromTheStatesNumber(t *testing.T) {
	const password = "the password"
	path := filepath.Join(t.TempDir(), "v.eks")
	u := mustCreate(t, path, PasswordSlot{Password: password, Argon2: fast}, RecoverySlot{Key: recoveryKey(t)})
	u.Close()
	u.k.Close()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	state := sbCopy(t, before, format.CopyA).Seq // the state's number; copy B carries one less

	// Copy A is lost, so the file opens through copy B, on the state it names
	// rather than on its own number.
	lost := bytes.Clone(before)
	off := format.CopyA.KeystoreSuperblockOff()
	clear(lost[off : off+format.SuperblockSize])
	if err := os.WriteFile(path, lost, 0o600); err != nil {
		t.Fatal(err)
	}
	k := mustOpen(t, path)
	u2, err := k.Unlock(PasswordCredential{Password: password})
	if err != nil {
		t.Fatalf("copy B did not open: %v", err)
	}
	if k.sb.Seq != state {
		t.Fatalf("the fallback stands at seq %d, the state it holds is %d", k.sb.Seq, state)
	}
	if err := u2.UpdateRegistry(func(g *format.Registry) error {
		g.IdleMinutes = 7
		return nil
	}); err != nil {
		t.Fatalf("the fallback did not commit: %v", err)
	}
	committed := k.sb.Seq
	if committed <= state {
		t.Fatalf("the commit left the sequence at %d, from %d", committed, state)
	}
	u2.Close()
	k.Close()

	// The replay: the bytes from before that commit, put back in place. They
	// must not stand at the number the commit recorded.
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	k2 := mustOpen(t, path)
	u3, err := k2.Unlock(PasswordCredential{Password: password})
	if err != nil {
		t.Fatalf("the replayed file did not unlock: %v", err)
	}
	if k2.sb.Seq >= committed {
		t.Fatalf("a replayed copy stands at seq %d, the commit's %d", k2.sb.Seq, committed)
	}
	u3.Close()
	k2.Close()
}

// seq counts commits and never wraps: a writer that would pass 2^64 − 1
// refuses the commit rather than wrap to 0, since a wrapped counter would make
// the new state lose to the old one (§4). No file reaches it, so the writer is
// put there by hand, through the live superblock this handle holds.
func TestTheKeystoreWriterDoesNotWrapTheSequence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	u := mustCreate(t, path, PasswordSlot{Password: "p", Argon2: fast}, RecoverySlot{Key: recoveryKey(t)})
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	real := u.k.sb.Seq

	u.k.sb.Seq = math.MaxUint64
	err = u.UpdateRegistry(func(g *format.Registry) error {
		g.IdleMinutes = 5
		return nil
	})
	if !errors.Is(err, ErrSeqExhausted) {
		t.Fatalf("a commit at 2^64 − 1: %v", err)
	}
	if u.k.Broken() != nil {
		t.Fatalf("the refusal broke the keystore: %v", u.k.Broken())
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("the refused commit wrote to the file")
	}

	// Refused, and nothing else is: the handle commits again once the
	// sequence is where a real file's would be.
	u.k.sb.Seq = real
	if err := u.UpdateRegistry(func(g *format.Registry) error {
		g.IdleMinutes = 5
		return nil
	}); err != nil {
		t.Fatalf("the keystore did not commit after the refusal: %v", err)
	}
	if u.k.sb.Seq != real+1 {
		t.Fatalf("the commit left the sequence at %d, want %d", u.k.sb.Seq, real+1)
	}
}

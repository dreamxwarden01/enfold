package keystore

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"time"

	"github.com/dreamxwarden01/enfold/internal/format"
	"github.com/dreamxwarden01/enfold/internal/kdf"
)

// now is the clock every timestamp comes from; tests replace it.
var now = func() int64 { return time.Now().Unix() }

// commitFault, set by a test, is asked at each step of a commit and of a
// scrub and fails it there with what it returns: "region" (before the slot
// region is written), "registry" (after it, before the registry), "sync"
// (before the barrier's fsync), "superblock" (in place of the superblock
// write: the commit point, so the commit is indeterminate), "flipsync"
// (after the superblock write, in place of its fsync), "trim" (in place of
// the trim's truncation, which then fails as an ignored error does),
// "scrub" (at the scrub's start, before its check of the disk: the commit
// has landed), "scrubsync" (in place of the scrub's fsync, the barrier that
// makes the trim before it and the scrub's own zeros durable) and "zero"
// (before zeroOther's write). nil outside tests.
var commitFault func(step string) error

// writeSeen, set by a test, is told what ("region", "registry",
// "superblock", "scrub" or "zero"), the offset and the length of every
// write a commit, a scrub or zeroOther makes, so that a test can show that
// none touched the live slot region copy or a 4 KiB unit of a named
// registry. nil outside tests.
var writeSeen func(what string, off, n int64)

// SetCommitFault and SetWriteSeen install commitFault and writeSeen for a
// test of another package: the app's tests fail a commit, a retirement or a
// scrub, or watch their writes, through them, since a package-level variable
// that is not exported cannot be set from internal/app. Each returns a
// function that puts the previous value back. Package-level, as now is: a
// test that sets one does not run in parallel. Never called outside tests.
func SetCommitFault(f func(step string) error) (restore func()) {
	prev := commitFault
	commitFault = f
	return func() { commitFault = prev }
}

// SetWriteSeen is SetCommitFault's twin for writeSeen.
func SetWriteSeen(f func(what string, off, n int64)) (restore func()) {
	prev := writeSeen
	writeSeen = f
	return func() { writeSeen = prev }
}

// faultAt asks the test seam whether this step of a commit, a scrub or
// zeroOther fails; nil when no test set one.
func faultAt(step string) error {
	if commitFault == nil {
		return nil
	}
	return commitFault(step)
}

// Log takes the lines the keystore cannot return (R41): a commit whose flip
// landed and whose scrub after it did not, which leaves the file owed and
// the handle sound, so the commit itself reports success; and an Open that
// could not read in full the bytes the predicate reads, which opens the file
// owed rather than refuse it. The default is the standard logger, so a line
// is never lost silently; a process with its own log points this at it at
// startup, as it does secmem.Log.
var Log = log.Printf

// stamp is the modified_at of a commit: the clock, but never less than one
// past the previous value, so that a clock set back cannot make a later
// state look older than an earlier one (R35).
func stamp(prev int64) int64 {
	if t := now(); t > prev {
		return t
	}
	return prev + 1
}

// Keystore is an open keystore file in its locked state: the live
// superblock, the live slot region, and the registry ciphertext. Nothing in
// it is secret.
type Keystore struct {
	f    *os.File
	path string
	size uint64
	sb   *format.KeystoreSuperblock
	// diskSeq is the seq of the superblock copy on disk that this handle
	// read or last wrote, which is not always sb.Seq: a registry that
	// authenticated under seq + 1 — §4's copy B, R31's retired copy — puts
	// the *state's* number in sb.Seq, since that is what a writer continues
	// from (§7), while the bytes on disk still carry the copy's. Only the
	// concurrent-writer check compares against it.
	diskSeq uint64
	live    format.Copy // which superblock copy is live
	region  []byte      // the live slot region as written
	// hdr is the slot region's 32-byte header (§6): the vault's entangle
	// switch, Argon2id parameters and entangle_salt. Plaintext, readable
	// before any credential, and copied forward byte for byte by every write
	// that does not change it.
	hdr   format.SlotRegionHeader
	slots []format.SlotRecord
	ct    []byte           // registry ciphertext ‖ tag
	reg   *format.Registry // the decrypted registry, once a credential opened it
	lock  *fileLock        // exclusive while open: one process, one handle
	// Stale is the damage found on the superblock copy that lost, when it
	// lost by being damaged rather than older: the file opened, but its last
	// write may not have completed. nil when both copies were sound.
	Stale  error
	broken error // set once a commit's outcome is unknown; see Broken
	closed bool
	// end is the file's physical end as far as this handle has written it
	// (R41): set from the stat at load, raised to the end of every registry
	// write before the write is made, whether it then lands or not, and
	// lowered only by a trim that succeeded. size is the end the last flip
	// left, which a commit that failed after writing its registry past it
	// does not move (the next flip sets it, or a Retire that needs only a
	// scrub takes the file's length); the padding, the scrub and the
	// predicate read up to end, so that a registry such a commit left is
	// never out of their reach.
	end uint64
	// loser is the losing superblock copy's decode, nil when it fails its
	// checksum: what the predicate's third clause reads, and whose registry
	// extent the scrub must never write.
	loser *format.KeystoreSuperblock
	// owed is R41's predicate over the file's own bytes, kept by clause:
	// read at load, set before a commit writes its first byte, computed again
	// after every flip. RetireOwed is its union.
	owed retireState
	// unsynced is set before a trim cuts the file and before a scrub writes
	// its zeros, and cleared only by the scrub's fsync after them: until that
	// fsync succeeds the file reads as retired through the system's cache
	// while the disk may still hold what was cut or zeroed, which a power
	// loss brings back (R41). It is not a clause of owed, which a reread of
	// the bytes computes again and the cache would clear; RetireOwed counts
	// it beside them.
	unsynced bool
}

// Broken is nil, or the error of a commit that failed at or after its
// commit point: the file may hold the new state or the old one, and this
// handle can no longer tell. Every further operation refuses; reopen the
// file. Exposed so that the UI can say so.
func (k *Keystore) Broken() error { return k.broken }

// usable refuses a closed or broken Keystore.
func (k *Keystore) usable() error {
	if k.closed {
		return ErrClosed
	}
	if k.broken != nil {
		return k.broken
	}
	return nil
}

// Open reads a keystore file. It fails closed on any structural problem
// (format.ErrInvalid); it does not need a credential.
func Open(path string) (*Keystore, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	lock, err := lockFile(f, path)
	if err != nil {
		f.Close()
		return nil, err
	}
	k, err := load(f, path)
	if err != nil {
		lock.release()
		f.Close()
		return nil, err
	}
	k.lock = lock
	return k, nil
}

func load(f *os.File, path string) (*Keystore, error) {
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := uint64(st.Size())
	if size < format.RegistryMinOff {
		return nil, corrupt("file of %d bytes is shorter than the fixed regions", size)
	}
	var a, b [format.SuperblockSize]byte
	if _, err := f.ReadAt(a[:], int64(format.KeystoreSuperblockAOff)); err != nil {
		return nil, err
	}
	if _, err := f.ReadAt(b[:], int64(format.KeystoreSuperblockBOff)); err != nil {
		return nil, err
	}
	sb, live, stale, err := format.PickKeystoreSuperblock(a[:], b[:])
	if err != nil {
		return nil, err
	}
	if err := sb.ValidateExtents(size); err != nil {
		return nil, err
	}
	// The losing copy decoded on its own, for R41: a writer reads it to decide
	// whether a retirement is owed, never a reader to choose a state. A copy
	// that fails its checksum is kept as nil, which the predicate counts as
	// owed: a torn write can leave an old registry's offset and nonce in it.
	losing := b[:]
	if live == format.CopyB {
		losing = a[:]
	}
	loser, err := format.DecodeKeystoreSuperblock(losing)
	if err != nil {
		loser = nil
	}
	region := make([]byte, sb.SlotRegionLen)
	if _, err := f.ReadAt(region, int64(sb.SlotRegionOff)); err != nil {
		return nil, err
	}
	sr, err := format.DecodeSlotRegion(region)
	if err != nil {
		return nil, err
	}
	ct := make([]byte, sb.RegistryLen+format.TagSize)
	if _, err := f.ReadAt(ct, int64(sb.RegistryOff)); err != nil {
		return nil, err
	}
	if [format.TagSize]byte(ct[sb.RegistryLen:]) != sb.RegistryTag {
		return nil, corrupt("registry tag in the file differs from the superblock's")
	}
	k := &Keystore{f: f, path: path, size: size, end: size, loser: loser, sb: sb, diskSeq: sb.Seq, live: live, region: region, hdr: sr.Header, slots: sr.Slots, ct: ct, Stale: stale}
	// The predicate reads what no reader of either state needs: the rest of
	// both slot region copies and the registry area outside the named
	// extents. An error there does not refuse a file a reader opens, since a
	// retirement that cannot be finished leaves the vault readable (R-A): the
	// handle opens owed by every clause the error left unread, which holds
	// the mutations behind the application's barrier, and Retire, meeting the
	// same bytes, either finishes or returns the error.
	if k.owed, err = k.readRetire(loser); err != nil {
		Log("keystore: the file could not be read in full to tell whether it owes a retirement, so it is taken to owe one: %v", err)
	}
	return k, nil
}

// Close releases the file. An Unlocked or Session over this Keystore is
// unusable afterwards.
func (k *Keystore) Close() error {
	if k.closed {
		return nil
	}
	k.closed = true
	if k.lock != nil {
		k.lock.release()
		k.lock = nil
	}
	return k.f.Close()
}

// Live reports whether the Session can still be used: nil, or ErrClosed
// after Lock, ErrStale after a rotation, or the keystore's own failure.
// The application gates every archive write that owes the registry a
// receipt on it before starting the write.
func (s *Session) Live() error { return s.live() }

// checkOnDisk refuses a commit whose view of the file is stale: another
// writer (another process — the OS lock stops the common case, not a
// non-cooperating one — or an earlier handle in this process) committed
// since this handle read the superblock. Nothing has been written when it
// fails; the handle is marked so the caller reopens the file.
func (k *Keystore) checkOnDisk() error {
	var a, b [format.SuperblockSize]byte
	if _, err := k.f.ReadAt(a[:], int64(format.KeystoreSuperblockAOff)); err != nil {
		return err
	}
	if _, err := k.f.ReadAt(b[:], int64(format.KeystoreSuperblockBOff)); err != nil {
		return err
	}
	sb, _, _, err := format.PickKeystoreSuperblock(a[:], b[:])
	if err != nil {
		return err
	}
	// Against diskSeq, not sb.Seq: this handle may have adopted the state's
	// number where the copy on disk carries one less (§7, diskSeq).
	if sb.Seq != k.diskSeq || sb.VaultID != k.sb.VaultID {
		k.broken = fmt.Errorf("%w: seq %d on disk, %d in memory", ErrConflict, sb.Seq, k.diskSeq)
		return k.broken
	}
	return nil
}

// VaultID is the vault's immutable identity.
func (k *Keystore) VaultID() [16]byte { return k.sb.VaultID }

// Generation is the current VMK generation (§6.2).
func (k *Keystore) Generation() uint64 { return k.sb.VMKGeneration }

// Entangled is the slot region header's switch (§6): whether the vault has an
// entangled password. Readable with no credential — one of the plaintext facts
// the lock screen caches while Locked (APP.md §2.1) — and what decides whether
// the password is asked before the PIN.
func (k *Keystore) Entangled() bool { return k.hdr.Entangle }

// FileSize is the keystore file's length: its physical end as the last flip
// left it, or as a Retire that needed only a scrub found it (R41), not a
// fresh Stat at the call, so a caller holding a state mutex can read it
// without a syscall.
func (k *Keystore) FileSize() uint64 { return k.size }

// ModifiedAt is when the keystore was last committed (Unix seconds, R35):
// the wall clock of the last change, never decreasing, and for an export
// the time it was made. Readable before an unlock, so a backup can be
// dated when the user picks it; authenticated by the registry once
// unlocked.
func (k *Keystore) ModifiedAt() int64 { return k.sb.ModifiedAt }

// Slots describes the active slots without revealing anything secret.
func (k *Keystore) Slots() []SlotInfo {
	var out []SlotInfo
	for i := range k.slots {
		if k.slots[i].State == format.SlotActive {
			out = append(out, infoOf(&k.slots[i]))
		}
	}
	return out
}

// registryAEAD returns the GCM instance for the registry under meta.
func registryAEAD(meta []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(meta)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// openRegistry decrypts the registry ciphertext under meta with the live
// superblock's AAD.
//
// The AAD binds that superblock's seq (§7), so a registry opens under the
// number it was sealed with — or under one more than the copy's own, which is
// the shape §4's initialisation leaves: a new file's copy B holds the same
// superblock at seq 0 beside copy A's 1, since two valid copies never carry
// equal seq, so a fresh file whose copy A is damaged opens through B. The
// tolerance runs one way only — a copy may name a state one commit newer than
// its own number, never an older one — so a registry can never be promoted to
// a higher seq than it was sealed under, which is what putting seq in the AAD
// is for (R36).
func (k *Keystore) openRegistry(meta []byte) (*format.Registry, error) {
	g, err := registryAEAD(meta)
	if err != nil {
		return nil, err
	}
	plain, err := g.Open(nil, k.sb.RegistryNonce[:], k.ct, k.sb.RegistryAAD())
	if err != nil && k.sb.Seq != math.MaxUint64 {
		retired := *k.sb
		retired.Seq++
		if plain, err = g.Open(nil, k.sb.RegistryNonce[:], k.ct, retired.RegistryAAD()); err == nil {
			// The state's number, not the copy's: this handle continues from
			// what the registry authenticated under, so the next commit
			// cannot seal a second state under a number an older copy of the
			// file already holds (§7). diskSeq keeps the copy's, which is
			// what the concurrent-writer check compares.
			k.sb.Seq = retired.Seq
		}
	}
	if err != nil {
		return nil, fmt.Errorf("%w: registry", ErrAuth)
	}
	reg, err := format.DecodeRegistry(plain)
	kdf.Zero(plain)
	if err != nil {
		return nil, err
	}
	return reg, nil
}

// txn is one commit: what changes, all of it landing in one superblock flip.
type txn struct {
	slots []format.SlotRecord // the new slot region's records; nil leaves them as they are
	// hdr is the slot region header this commit writes, non-nil only on a
	// write that changes it (§6). Otherwise the live header is copied forward
	// byte for byte. A header change is a slot-region write even when the
	// records are unchanged, so hdr alone rewrites the region.
	hdr  *format.SlotRegionHeader
	reg  *format.Registry // the registry to seal; every commit carries one (R25, R35)
	meta []byte           // Metadata key, required when reg is set
	gen  uint64           // the new VMK generation
	// at is the modified_at this commit carries when the caller has already
	// computed it — a decision that depends on it (§18.2) must be made against
	// the value the superblock and the registry will record, not a second
	// clock reading. Zero takes the clock (R35).
	at int64
	// retire makes this a retiring commit (R41): the live slot region, byte
	// for byte, into the copy the live superblock does not name, with the
	// registry's slot_region_hash carried rather than recomputed, since the
	// bytes are the ones it already vouches for. slots and hdr stay nil.
	retire bool
}

// commit writes tx in the order of §4 and §8: slot region into the inactive
// copy, registry into free space, sync, superblock into the inactive copy,
// sync, then trim the file to the later named registry's end, scrub the
// registry area and sync what the two changed (R41). On error before the
// superblock write nothing has changed; the superblock write itself is the
// commit point. A scrub that fails after the flip does not fail the commit,
// which has landed: the file stays owed and the error is logged; Retire is the
// caller that returns it.
func (k *Keystore) commit(tx txn) error {
	scrubErr, err := k.commitScrub(tx)
	if err == nil && scrubErr != nil {
		Log("keystore: a commit landed and the scrub after it did not, so the file still owes a retirement: %v", scrubErr)
	}
	return err
}

// commitScrub is commit with the scrub's outcome returned beside the
// commit's: err is the commit's, and scrubErr, meaningful only when err is
// nil, is what the zero-fill after the flip met, an ErrConflict from its
// check of the disk included.
func (k *Keystore) commitScrub(tx txn) (scrubErr, err error) {
	if err := k.usable(); err != nil {
		return nil, err
	}
	// Every commit re-seals the registry. It carries the slot region's hash,
	// which is what authenticates a region nobody else vouches for (R25),
	// and its AAD binds modified_at (R35), which every commit advances — a
	// commit that sealed nothing would leave a superblock whose date no
	// longer matches the ciphertext it points at, and the file would never
	// unlock again.
	if tx.reg == nil {
		return nil, fmt.Errorf("%w: commit without a registry", ErrParams)
	}
	if tx.meta == nil {
		return nil, fmt.Errorf("%w: registry without a Metadata key", ErrParams)
	}
	if tx.retire && (tx.slots != nil || tx.hdr != nil) {
		return nil, fmt.Errorf("%w: a retiring commit writes the live region, not a new one", ErrParams)
	}
	// A retiring commit carries the registry's hash forward over the bytes it
	// copies, so it is made only while they are the bytes the hash vouches
	// for: a region that fails R25 is never written forward.
	if tx.retire {
		if err := tx.reg.VerifySlotRegion(k.region); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrTampered, err)
		}
	}
	// The number this commit would carry, before anything is read or written:
	// seq counts commits and never wraps, so a writer at 2^64 − 1 refuses the
	// commit rather than let the new state lose to the old one (§4). No file
	// reaches it; the guard is what keeps "never wraps" a property of the
	// writer and not of the arithmetic.
	if k.sb.Seq == math.MaxUint64 {
		return nil, fmt.Errorf("%w: the superblock sequence is at 2^64 − 1", ErrSeqExhausted)
	}
	// The header this commit writes, and the writer's own fail-closed check
	// that the registry agrees with it (§7.6, R38): exactly one entangled_key
	// record while entangle is 1, none while it is 0. Before the flip, so a
	// disagreement is never written rather than only refused at the next
	// unlock.
	hdr := k.hdr
	if tx.hdr != nil {
		hdr = *tx.hdr
	}
	if err := tx.reg.CheckEntangleAgreement(hdr.Entangle); err != nil {
		return nil, err
	}
	// R41: a registry-only commit made while an earlier state is still in a
	// slot region copy or the losing superblock carries one retiring pass in
	// its own flip, so that every save makes progress. Never over a region
	// that fails R25, which is not copied forward. If the pass fails before
	// its flip, nothing it wrote counts and the save is made again at once as
	// the plain commit it is: a write the application does not hold back for
	// the retirement never fails for its sake. A conflict or an unknown
	// outcome has marked the handle and is returned as it is.
	if !tx.retire && tx.slots == nil && tx.hdr == nil &&
		(k.owed.liveTail || k.owed.other || k.owed.loser) && tx.reg.VerifySlotRegion(k.region) == nil {
		carried := tx
		carried.retire = true
		scrubErr, err := k.flip(carried, hdr)
		if err == nil || k.broken != nil {
			return scrubErr, err
		}
	}
	return k.flip(tx, hdr)
}

// flip is one attempt at a commit whose arguments commitScrub has checked:
// the bodies, the barrier, the superblock, then the trim, the scrub and the
// predicate computed again (R41).
func (k *Keystore) flip(tx txn, hdr format.SlotRegionHeader) (scrubErr, err error) {
	if err := k.checkOnDisk(); err != nil {
		return nil, err
	}
	next := *k.sb
	next.Seq++ // refused by commitScrub at 2^64 − 1 (§4)
	next.VMKGeneration = tx.gen
	if tx.at != 0 {
		next.ModifiedAt = tx.at
	} else {
		next.ModifiedAt = stamp(k.sb.ModifiedAt)
	}

	region, slots := k.region, k.slots
	changed := tx.slots != nil || tx.hdr != nil
	if changed || tx.retire {
		// A retiring commit writes the live region as it is on disk, never
		// re-encoded: the registry's hash vouches for exactly these bytes.
		encoded := k.region
		if changed {
			records := tx.slots
			if records == nil {
				records = k.slots
			}
			var err error
			encoded, err = (&format.SlotRegion{Header: hdr, Slots: records}).Encode()
			if err != nil {
				return nil, err
			}
			slots = records
		}
		// The whole 128 KiB copy, the rest of it zero (R41): a shorter region
		// written over a longer one would otherwise leave the longer one's
		// tail, which after a removal can be the removed record itself.
		full := make([]byte, format.SlotRegionSize)
		copy(full, encoded)
		target := k.sb.LiveSlotRegion().Other()
		if err := faultAt("region"); err != nil {
			return nil, err
		}
		// Owed before the first byte, so that a commit that fails after
		// writing anything leaves the handle owed exactly as its bytes are.
		k.owed.other = true
		if err := k.writeAt("region", full, target.SlotRegionOff()); err != nil {
			return nil, err
		}
		next.SlotRegionOff = target.SlotRegionOff()
		next.SlotRegionLen = uint64(len(encoded))
		region = encoded
	}

	var ct []byte
	{
		if changed {
			// The hash covers the header as part of the region as written
			// (§6, R25), which is one of the only two things authenticating
			// the header at all.
			tx.reg.SlotRegionHash = format.SlotRegionHash(region)
		}
		// Otherwise the hash stays what the registry carried in: the last
		// value the Metadata key authenticated. A registry-only write never
		// blesses a region nobody wrote (R25) — with a tampered region on
		// disk, the mismatch survives the write and is reported again at the
		// next unlock — and a retiring commit wrote the bytes it vouches for.
		tx.reg.ModifiedAt = next.ModifiedAt
		plain, err := tx.reg.Encode()
		if err != nil {
			return nil, err
		}
		defer kdf.Zero(plain)
		if uint64(len(plain)) > format.MaxRegistryLen {
			return nil, corrupt("registry of %d bytes exceeds %d", len(plain), format.MaxRegistryLen)
		}
		next.RegistryOff = k.registryTarget(uint64(len(plain)))
		next.RegistryLen = uint64(len(plain))
		if _, err := rand.Read(next.RegistryNonce[:]); err != nil {
			return nil, err
		}
		g, err := registryAEAD(tx.meta)
		if err != nil {
			return nil, err
		}
		sealed := g.Seal(nil, next.RegistryNonce[:], plain, next.RegistryAAD())
		next.RegistryTag = [format.TagSize]byte(sealed[len(plain):])
		// The rest of the registry's last 4 KiB unit is zeroed in the same
		// write (R41), so that no unit is ever shared between a named
		// registry and what an earlier one left there.
		padEnd := k.regPadEnd(next.RegistryOff, next.RegistryLen)
		out := make([]byte, padEnd-next.RegistryOff)
		copy(out, sealed)
		if err := faultAt("registry"); err != nil {
			return nil, err
		}
		// Owed, and the physical end raised, before the write: a registry
		// that lands whole or torn in space no superblock names is owed by
		// the fourth clause, and one past the last flip's end must stay in
		// reach of the trim, the padding and the scrub (R41).
		k.end = max(k.end, padEnd)
		k.oweRegistryWrite(next.RegistryOff, padEnd)
		if err := k.writeAt("registry", out, next.RegistryOff); err != nil {
			return nil, err
		}
		ct = sealed
	}

	if err := faultAt("sync"); err != nil {
		return nil, err
	}
	if err := k.f.Sync(); err != nil {
		return nil, err
	}
	encoded, err := next.Encode()
	if err != nil {
		return nil, err
	}
	// From here on a failure leaves the outcome unknown: the superblock may
	// be durable in part or in full. This handle stops reasoning about the
	// file (Broken); the caller reopens it and sees whichever state won.
	target := k.live.Other()
	if err := faultAt("superblock"); err != nil {
		k.broken = fmt.Errorf("%w: superblock write failed at the commit point: %v", ErrIndeterminate, err)
		return nil, k.broken
	}
	if err := k.writeAt("superblock", encoded, target.KeystoreSuperblockOff()); err != nil {
		k.broken = fmt.Errorf("%w: superblock write failed at the commit point: %v", ErrIndeterminate, err)
		return nil, k.broken
	}
	if err := faultAt("flipsync"); err != nil {
		k.broken = fmt.Errorf("%w: the change may already be durable: %v", ErrIndeterminate, err)
		return nil, k.broken
	}
	if err := k.f.Sync(); err != nil {
		k.broken = fmt.Errorf("%w: the change may already be durable: %v", ErrIndeterminate, err)
		return nil, k.broken
	}
	// Committed. Trim what neither superblock copy references any more: the
	// losing copy's registry stays addressable, so that a later loss of the
	// live copy opens the file one commit behind rather than not at all. The
	// trim cuts against the physical end, so that a registry an earlier
	// commit left past the last flip's end goes with it; one that fails is
	// ignored, and the scrub below zeroes what it left. A cut is not durable
	// before an fsync after it, which the scrub makes: the handle owes that
	// fsync from before the cut, so that a retirement whose cut a power loss
	// could still undo is never reported as landed.
	prev := *k.sb
	prev.Seq = k.diskSeq // the copy as it lies on disk, now the losing one
	end := max(next.RegistryOff+next.RegistryLen+format.TagSize, prev.RegistryOff+prev.RegistryLen+format.TagSize)
	if end < k.end {
		k.unsynced = true
		terr := faultAt("trim")
		if terr == nil {
			terr = k.f.Truncate(int64(end))
		}
		if terr == nil {
			k.end = end
		} else if st, err := k.f.Stat(); err == nil && uint64(st.Size()) < k.end {
			// An earlier write that raised the end may not have reached it;
			// what the file does not hold needs no scrub, and the size shown
			// is the file's.
			k.end = max(uint64(st.Size()), end)
		}
	}
	k.size = k.end
	k.sb, k.diskSeq, k.live, k.region, k.hdr, k.slots, k.ct = &next, next.Seq, target, region, hdr, slots, ct
	k.reg = tx.reg
	k.Stale = nil
	k.loser = &prev

	// The predicate again (R41). A registry-only commit wrote no region, so
	// the first two clauses keep their values, and its losing copy is the
	// state just replaced, at the same generation and naming the live copy.
	// A change leaves its earlier state in the other copy and the losing
	// superblock, its retirement following at once. A retiring commit reads
	// the file, below, since an old tail its flip has just left in the copy
	// that is no longer live keeps the file owed. The fourth clause is owed
	// until the scrub says otherwise: this flip has just left a registry
	// unnamed.
	switch {
	case changed:
		k.owed.liveTail, k.owed.other, k.owed.loser = false, true, true
	case !tx.retire:
		k.owed.loser = false
	}
	k.owed.remnant = true
	scrubErr = k.scrub()
	if tx.retire && k.broken == nil {
		if err := k.rereadRetire(); err != nil && scrubErr == nil {
			scrubErr = err
		}
	}
	return scrubErr, nil
}

// oweRegistryWrite marks the handle owed, by the fourth clause, for a
// registry about to be written over [off, end), as readRetire would find it if
// the write landed and its flip did not: a unit no named registry reaches is
// a remnant, which a scrub clears; one that also holds a byte of the losing
// copy's registry, which the new one may overwrite, is mixed, which waits for
// a commit that leaves that registry unnamed. A unit wholly inside a named
// extent counts for neither, as it does at load.
func (k *Keystore) oweRegistryWrite(off, end uint64) {
	named := k.namedExtents(k.loser)
	for s := off &^ (regUnit - 1); s < end; s += regUnit {
		e := min(s+regUnit, k.end) // as the scan bounds the last unit
		whole, touches := false, false
		for _, x := range named {
			whole = whole || (x[0] <= s && e <= x[1])
			touches = touches || (x[0] < e && s < x[1])
		}
		switch {
		case whole:
		case touches:
			k.owed.mixed = true
		default:
			k.owed.remnant = true
		}
	}
}

// writeAt is every write a commit, a scrub or zeroOther makes, told to the
// test seam first so that a test can check where each one landed.
func (k *Keystore) writeAt(what string, b []byte, off uint64) error {
	if writeSeen != nil {
		writeSeen(what, int64(off), int64(len(b)))
	}
	_, err := k.f.WriteAt(b, int64(off))
	return err
}

// registryTarget picks where a registry of n bytes goes: the fixed offset when
// it fits below the live registry, else right after the live one, 4 KiB
// aligned. Either way it never overlaps what a reader could still need.
func (k *Keystore) registryTarget(n uint64) uint64 {
	liveOff, liveEnd := k.sb.RegistryOff, k.sb.RegistryOff+k.sb.RegistryLen+format.TagSize
	if liveOff > format.RegistryMinOff && format.RegistryMinOff+n+format.TagSize <= liveOff {
		return format.RegistryMinOff
	}
	return (liveEnd + 4095) &^ 4095
}

// regPadEnd is where a registry written at off with n plaintext bytes stops
// its zero padding: the 4 KiB boundary after its tag, but not past k.end,
// the file's physical end before the commit, and not into the live
// registry's extent (R41). Past k.end nothing was ever written, so a
// registry placed there is not padded and the file ends with its tag; and a
// file another writer laid out off the 4 KiB grid can begin its live
// registry inside the unit a new one ends in, which is never written.
func (k *Keystore) regPadEnd(off, n uint64) uint64 {
	tagEnd := off + n + format.TagSize
	end := (tagEnd + regUnit - 1) &^ (regUnit - 1)
	if end > k.end {
		end = max(k.end, tagEnd)
	}
	if liveOff := k.sb.RegistryOff; off < liveOff && end > liveOff {
		end = max(liveOff, tagEnd)
	}
	return end
}

// regUnit is the unit the registry area is padded and scrubbed in: FORMAT's
// atomic-write and alignment unit (§17), so that a write of one never
// rewrites a sector a reader of a named registry reads.
const regUnit = 4096

// retireState is R41's predicate kept by clause, so that Retire knows whether
// a retiring commit is needed or a scrub suffices. RetireOwed is their union.
type retireState struct {
	liveTail bool // clause 1: the live region copy holds a nonzero byte past slot_region_len
	other    bool // clause 2: the other copy is neither the live region and zeros, nor zeros
	loser    bool // clause 3: the losing superblock is damaged, of another generation, names another region or a registry past the end
	remnant  bool // clause 4: an unnamed 4 KiB unit of the registry area holds a nonzero byte
	mixed    bool // clause 4, by a unit that also holds a byte of a named registry (legacy)
}

func (st retireState) owed() bool {
	return st.liveTail || st.other || st.loser || st.remnant || st.mixed
}

// needsCommit reports whether what is owed takes a retiring commit: a scrub
// writes no slot region and no superblock, and never a unit that holds a
// byte of a named registry, so only whole unnamed units are its alone.
func (st retireState) needsCommit() bool {
	return st.liveTail || st.other || st.loser || st.mixed
}

// RetireOwed reports whether the file still holds a state, or a piece of
// one, that a reader of the live state does not need (R41): (1) the live slot
// region copy holds anything past slot_region_len but zeros; (2) the other
// copy holds anything but the live region followed by zeros, or zeros alone;
// (3) the losing superblock copy fails its checksum, carries a vmk_generation
// other than the live one's, names a slot region copy whose bytes
// [0, its slot_region_len) are not exactly the live region, or names a
// registry extent that does not lie inside the file; (4) a 4 KiB unit
// of the registry area, [0x42000, the file's physical end), holds a nonzero
// byte outside both registry extents the superblock copies name. Read from
// the file's bytes at Open; set before a commit writes its first byte, so a
// commit that fails leaves it set; computed again after every flip. It holds
// too while a trim or a scrub's zeros wait for the fsync that makes them
// durable, since until then a power loss can bring back what they removed:
// the retirement has not landed. A read of memory, no I/O.
func (k *Keystore) RetireOwed() bool { return k.owed.owed() || k.unsynced }

// RetireRemnantOnly reports whether what is owed is the fourth clause
// alone, through whole unnamed registry units, or the fsync a trim or a
// scrub's zeros wait for: no slot region copy and no superblock copy holds
// an earlier state, only earlier registry ciphertext does, or may again
// after a power loss, which a scrub clears with no commit. The act's end
// words its bar by it. A read of memory, no I/O.
func (k *Keystore) RetireRemnantOnly() bool {
	return (k.owed.remnant || k.unsynced) && !k.owed.needsCommit()
}

// readRetire computes retireState from the file: both region copies, the
// losing superblock's decode (nil when it fails its checksum), and the 4 KiB
// units of [0x42000, k.end) not wholly inside a named registry extent. Called
// by load and after every retiring commit. It writes nothing.
//
// A clause whose bytes could not be read is owed, and the read's error is
// returned beside the state, so that nothing is taken as retired that was
// not seen to be. None of these bytes is one a reader of either state needs
// (the live region is the one load read or the retiring commit wrote), so a
// caller may keep the handle with that state, as load does.
func (k *Keystore) readRetire(loser *format.KeystoreSuperblock) (retireState, error) {
	var st retireState
	var errs []error
	var copies [2][]byte // nil where the read failed
	for _, c := range []format.Copy{format.CopyA, format.CopyB} {
		b := make([]byte, format.SlotRegionSize)
		if _, err := k.f.ReadAt(b, int64(c.SlotRegionOff())); err != nil {
			errs = append(errs, err)
			continue
		}
		copies[c] = b
	}
	live, other := copies[k.sb.LiveSlotRegion()], copies[k.sb.LiveSlotRegion().Other()]
	n := k.sb.SlotRegionLen
	region := k.region
	st.liveTail = live == nil || !allZero(live[n:])
	st.other = other == nil || (!allZero(other) && !(bytes.Equal(other[:n], region) && allZero(other[n:])))
	// The vault ID too, which the rule does not list: a losing copy of another
	// vault is no state of this one's, and a retiring commit overwrites it.
	// And the registry extent, as the fourth clause's reading of the losing
	// copy means: one that does not lie inside the file names nothing the
	// scrub can keep apart from a remnant.
	st.loser = loser == nil || loser.VaultID != k.sb.VaultID || loser.VMKGeneration != k.sb.VMKGeneration ||
		loser.ValidateExtents(k.end) != nil || loser.SlotRegionLen != n
	if !st.loser {
		named := copies[loser.LiveSlotRegion()]
		st.loser = named == nil || !bytes.Equal(named[:n], region)
	}
	remnant, mixed, err := k.scanRegistryArea(loser, false)
	st.remnant, st.mixed = remnant, mixed
	return st, errors.Join(append(errs, err)...)
}

// rereadRetire is the predicate after a retiring commit, from the file: the
// losing superblock read again from its copy, then readRetire, whose state
// keeps owed what it could not read. When the losing copy itself cannot be
// read, the copy this flip replaced stays the one whose registry the scrub
// must never write, and the clauses a retiring commit can leave are kept
// owed, so that nothing is taken as retired that was not seen to be. It
// computes the clauses only: unsynced, which bytes read through the cache
// cannot show, stays as the scrub left it, so that zeros a failed fsync left
// in the cache never count as retired.
func (k *Keystore) rereadRetire() error {
	var b [format.SuperblockSize]byte
	if _, err := k.f.ReadAt(b[:], int64(k.live.Other().KeystoreSuperblockOff())); err != nil {
		k.owed.other, k.owed.loser, k.owed.remnant = true, true, true
		return err
	}
	loser, err := format.DecodeKeystoreSuperblock(b[:])
	if err != nil {
		loser = nil
	}
	k.loser = loser
	k.owed, err = k.readRetire(loser)
	return err
}

// namedExtents are the registry extents [off, end) the superblock copies
// name: the live one, and the losing copy's when it decodes and lies inside
// the file, since one that does not is owed by the third clause already.
func (k *Keystore) namedExtents(loser *format.KeystoreSuperblock) [][2]uint64 {
	named := [][2]uint64{{k.sb.RegistryOff, k.sb.RegistryOff + k.sb.RegistryLen + format.TagSize}}
	if loser != nil {
		off, end := loser.RegistryOff, loser.RegistryOff+loser.RegistryLen+format.TagSize
		if off >= format.RegistryMinOff && end <= k.end {
			named = append(named, [2]uint64{off, end})
		}
	}
	return named
}

// scanRegistryArea walks the 4 KiB units of [0x42000, k.end) against the two
// named registry extents, the live one and the losing copy's when it decodes
// and lies inside the file. A unit wholly inside one is not read. A unit that
// holds no named byte and a nonzero one is a remnant, which scrub writes
// zeros over (the last unit only as far as the file goes); a unit that holds
// both a named byte and a nonzero byte outside every named extent is mixed,
// which is never written. With scrub set, remnant is what remains after the
// writes, false unless one of them failed, and unsynced is set before the
// first: the fsync that makes them durable is the scrub's.
func (k *Keystore) scanRegistryArea(loser *format.KeystoreSuperblock, scrub bool) (remnant, mixed bool, err error) {
	named := k.namedExtents(loser)
	inNamed := func(off uint64) bool {
		for _, x := range named {
			if x[0] <= off && off < x[1] {
				return true
			}
		}
		return false
	}
	buf := make([]byte, regUnit)
	var zeros [regUnit]byte
	for s := uint64(format.RegistryMinOff); s < k.end; s += regUnit {
		e := min(s+regUnit, k.end)
		whole, touches := false, false
		for _, x := range named {
			if x[0] <= s && e <= x[1] {
				whole = true
			}
			if x[0] < e && s < x[1] {
				touches = true
			}
		}
		if whole {
			continue
		}
		// Bytes past the file's length read as nothing: a commit raised end
		// before a write that may not have reached it.
		got, rerr := k.f.ReadAt(buf[:e-s], int64(s))
		if rerr != nil && !errors.Is(rerr, io.EOF) {
			return true, mixed, rerr
		}
		b := buf[:got]
		if touches {
			for i := range b {
				if b[i] != 0 && !inNamed(s+uint64(i)) {
					mixed = true
					break
				}
			}
			continue
		}
		if allZero(b) {
			continue
		}
		if !scrub {
			remnant = true
			continue
		}
		// Owed before the write, as a commit's bodies are: a write that
		// fails, or lands in the cache and is never flushed, leaves the
		// fsync owed.
		k.unsynced = true
		if err := k.writeAt("scrub", zeros[:got], s); err != nil {
			return true, mixed, err
		}
	}
	return remnant, mixed, nil
}

// scrub first re-reads the two superblock copies (checkOnDisk) and writes
// nothing when another writer has committed since the flip, leaving the
// handle Broken with ErrConflict. Then it writes zeros over every 4 KiB unit
// of [0x42000, k.end) that holds no byte of the live registry's extent or
// the losing superblock's and holds a nonzero byte; it leaves a unit that
// holds both (a legacy one) and reports it as mixed. Then it fsyncs whenever
// the handle owes one (unsynced): after it wrote, after a trim that cut the
// file, and after an earlier scrub whose write or fsync failed, since a unit
// that reads as zeros through the cache is no proof that the disk holds
// them, and a retry that finds nothing left to write must still flush.
// It writes no superblock and no region, so it has no commit point: an I/O
// error leaves the file owed and the handle sound. Called by commit after
// its trim and by Retire.
func (k *Keystore) scrub() error {
	if err := faultAt("scrub"); err != nil {
		return err
	}
	// The exclusive lock stops another Enfold, not a writer that ignores
	// it: whatever such a writer committed since the flip could lie in the
	// units about to be written.
	if err := k.checkOnDisk(); err != nil {
		return err
	}
	remnant, mixed, err := k.scanRegistryArea(k.loser, true)
	if err != nil {
		k.owed.remnant = true
		return err
	}
	k.owed.remnant, k.owed.mixed = remnant, mixed
	if !k.unsynced {
		return nil
	}
	if err := faultAt("scrubsync"); err != nil {
		return err
	}
	if err := k.f.Sync(); err != nil {
		return err
	}
	k.unsynced = false
	return nil
}

// zeroOther writes zeros over the whole slot region copy neither superblock
// copy names, then fsyncs: Retire's Tampered branch, once the losing copy
// names the live region copy (R41). It refuses, writing nothing, unless both
// superblock copies name the live copy. It writes no superblock and no
// region a superblock names, so it has no commit point: an error leaves the
// file owed and the handle sound. The two copies are 4 KiB aligned and
// 128 KiB long, so the write shares no unit with the live one.
func (k *Keystore) zeroOther() error {
	if k.loser == nil || k.loser.SlotRegionOff != k.sb.SlotRegionOff {
		return fmt.Errorf("%w: a superblock copy still names the slot region copy to be zeroed", ErrParams)
	}
	// As the scrub does, and for its reason: a writer that ignores the lock
	// may have named that copy since.
	if err := k.checkOnDisk(); err != nil {
		return err
	}
	if err := faultAt("zero"); err != nil {
		return err
	}
	if err := k.writeAt("zero", make([]byte, format.SlotRegionSize), k.sb.LiveSlotRegion().Other().SlotRegionOff()); err != nil {
		return err
	}
	if err := k.f.Sync(); err != nil {
		return err
	}
	k.owed.other = false
	return nil
}

// retire finishes what R41 owes, in full, over the Metadata key and the
// generation the caller holds (Unlocked.Retire, Session.Retire); tampered is
// R25's verdict on the live region.
//
// While the region verifies, a retiring commit for every clause a scrub cannot
// settle, and the scrub alone where only whole unnamed units remain, or only
// the fsync a trim or a scrub's zeros wait for. One retiring commit retires a
// file this writer made; a legacy tail in the live copy, or past the tag in the
// live registry's last unit, takes a second once the first has left that copy
// or registry no longer live. A third is never needed: after two, both region
// copies were written whole by them, the losing superblock is the first one's,
// both named registries were written on the grid and padded, and every unnamed
// unit was offered to two scrubs. So a file still owed after two is a
// corruption.
//
// On a tampered region nothing is copied forward (R25): a registry-only
// commit, which carries the hash it was given, stands in for each retiring
// commit; once both superblock copies name the live copy, zeros go over the
// other; the scrub runs. What remains then is the live copy's own tail,
// which only a region write could clear: ErrTampered while it does.
func (k *Keystore) retire(meta []byte, gen uint64, tampered bool) error {
	commits := 0
	for {
		st := k.owed
		if !k.RetireOwed() {
			return nil
		}
		commit := st.needsCommit()
		if tampered {
			// Zeros may go over the other copy only once no superblock copy
			// names it; until then a registry-only commit, whose superblock
			// write overwrites the losing copy, is what moves it.
			namesOther := k.loser == nil || k.loser.SlotRegionOff != k.sb.SlotRegionOff
			commit = st.loser || st.mixed || (st.other && namesOther)
		}
		if !commit {
			switch {
			case tampered && st.other:
				if err := k.zeroOther(); err != nil {
					return err
				}
			case st.remnant || k.unsynced:
				// The scrub fsyncs whenever one is owed, so that a retry
				// whose units already read as zeros, the writes before it
				// having reached only the cache, still makes them durable.
				if err := k.scrub(); err != nil {
					return err
				}
				// No flip follows to set the size (rule 8), and a commit
				// that failed after writing its registry past the last
				// flip's end left the file longer than the size this handle
				// shows, a tail the scrub has just zeroed and the next
				// flip's trim cuts: the size is taken from the file, so that
				// the size shown is the file's.
				if k.size < k.end {
					if fi, err := k.f.Stat(); err == nil {
						k.size = min(k.end, uint64(fi.Size()))
					}
				}
			default: // tampered, and the live copy's own tail alone
				return fmt.Errorf("%w: the live slot region copy holds bytes past its length, which only a region write could clear", ErrTampered)
			}
			continue
		}
		if commits == 2 {
			return corrupt("the file still owes a retirement after two retiring commits")
		}
		commits++
		// Only the date advances (R35): the records are the ones the live
		// state already holds.
		reg, err := cloneRegistry(k.reg)
		if err != nil {
			return err
		}
		scrubErr, err := k.commitScrub(txn{reg: reg, meta: meta, gen: gen, retire: !tampered})
		if err != nil {
			return err
		}
		if scrubErr != nil {
			return scrubErr
		}
	}
}

// allZero reports whether b holds no nonzero byte.
func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

// create writes a brand-new keystore file: superblock A with seq 1 and B
// with seq 0, slot region A, and the registry at the fixed offset. path must
// not exist.
func create(path string, vaultID [16]byte, hdr format.SlotRegionHeader, slots []format.SlotRecord, reg *format.Registry, meta []byte, gen uint64, prev int64) (*Keystore, error) {
	// The same fail-closed writer check commit makes (§7.6, R38).
	if err := reg.CheckEntangleAgreement(hdr.Entangle); err != nil {
		return nil, err
	}
	region, err := (&format.SlotRegion{Header: hdr, Slots: slots}).Encode()
	if err != nil {
		return nil, err
	}
	reg.SlotRegionHash = format.SlotRegionHash(region)
	// prev is the source's modified_at for an export, so that a clock set
	// back cannot date a backup before the vault it was taken from (R35).
	reg.ModifiedAt = stamp(prev)
	plain, err := reg.Encode()
	if err != nil {
		return nil, err
	}
	defer kdf.Zero(plain)
	sb := format.KeystoreSuperblock{
		Seq:           1,
		VaultID:       vaultID,
		SlotRegionOff: format.CopyA.SlotRegionOff(),
		SlotRegionLen: uint64(len(region)),
		RegistryOff:   format.RegistryMinOff,
		RegistryLen:   uint64(len(plain)),
		VMKGeneration: gen,
		ModifiedAt:    reg.ModifiedAt,
	}
	if _, err := rand.Read(sb.RegistryNonce[:]); err != nil {
		return nil, err
	}
	g, err := registryAEAD(meta)
	if err != nil {
		return nil, err
	}
	sealed := g.Seal(nil, sb.RegistryNonce[:], plain, sb.RegistryAAD())
	sb.RegistryTag = [format.TagSize]byte(sealed[len(plain):])
	encodedSB, err := sb.Encode()
	if err != nil {
		return nil, err
	}

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	lock, err := lockFile(f, path)
	if err != nil {
		f.Close()
		os.Remove(path)
		return nil, err
	}
	// Every failure after the exclusive create leaves no file behind, so
	// that "path must not exist" holds for the retry.
	failed := true
	defer func() {
		if failed {
			lock.release()
			f.Close()
			os.Remove(path)
		}
	}()
	write := func(b []byte, off uint64) error {
		_, err := f.WriteAt(b, int64(off))
		return err
	}
	err = errors.Join(
		write(region, sb.SlotRegionOff),
		write(sealed, sb.RegistryOff),
	)
	if err == nil {
		err = f.Sync()
	}
	if err == nil {
		// Copy B gets the same superblock at seq 0: a valid, older copy, so
		// that a fresh file has no damaged loser to report. A ties would be
		// corruption to the reader; a predecessor is what every later commit
		// leaves behind anyway.
		older := sb
		older.Seq = 0
		encodedOlder, encErr := older.Encode()
		err = encErr
		if err == nil {
			err = errors.Join(
				write(encodedSB, format.CopyA.KeystoreSuperblockOff()),
				write(encodedOlder, format.CopyB.KeystoreSuperblockOff()),
			)
		}
	}
	if err == nil {
		err = f.Sync()
	}
	if err != nil {
		return nil, err
	}
	k, err := load(f, path)
	if err != nil {
		return nil, err
	}
	k.lock = lock
	failed = false
	return k, nil
}

// Removable reports whether the slot could be removed: the invariant
// (§6.4) would still hold without it. Evaluated over the records and the
// header, both plaintext, so that a page can grey the action out before any
// ceremony is run for it.
func (k *Keystore) Removable(recipientID [16]byte) bool {
	slots := cloneSlots(k.slots)
	for i := range slots {
		if slots[i].RecipientID == recipientID {
			slots = append(slots[:i], slots[i+1:]...)
			return checkInvariant(k.hdr, slots) == nil
		}
	}
	return false
}

// CanEnableEntangled evaluates §6.4 for a 0→1 switch of the header's entangle
// byte, with no VMK and before any ceremony, so that a page can grey the
// switch out as Removable greys Remove. nil means the switch could be turned
// on; ErrInvariant means it could not — a vault whose active slots are all
// hardware slots must add a recovery slot first, since the vault password
// would then be one and the same secret for every way in.
func (k *Keystore) CanEnableEntangled() error {
	hdr := k.hdr
	hdr.Entangle = true
	return checkInvariant(hdr, k.slots)
}

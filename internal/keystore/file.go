package keystore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/dreamxwarden01/enfold/internal/format"
	"github.com/dreamxwarden01/enfold/internal/kdf"
)

// now is the clock every timestamp comes from; tests replace it.
var now = func() int64 { return time.Now().Unix() }

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
	f      *os.File
	path   string
	size   uint64
	sb     *format.KeystoreSuperblock
	live   format.Copy // which superblock copy is live
	region []byte      // the live slot region as written
	slots  []format.SlotRecord
	ct     []byte           // registry ciphertext ‖ tag
	reg    *format.Registry // the decrypted registry, once a credential opened it
	lock   *fileLock        // exclusive while open: one process, one handle
	// Stale is the damage found on the superblock copy that lost, when it
	// lost by being damaged rather than older: the file opened, but its last
	// write may not have completed. nil when both copies were sound.
	Stale  error
	broken error // set once a commit's outcome is unknown; see Broken
	closed bool
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
	region := make([]byte, sb.SlotRegionLen)
	if _, err := f.ReadAt(region, int64(sb.SlotRegionOff)); err != nil {
		return nil, err
	}
	slots, err := format.DecodeSlotRegion(region)
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
	return &Keystore{f: f, path: path, size: size, sb: sb, live: live, region: region, slots: slots, ct: ct, Stale: stale}, nil
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
	if sb.Seq != k.sb.Seq || sb.VaultID != k.sb.VaultID {
		k.broken = fmt.Errorf("%w: seq %d on disk, %d in memory", ErrConflict, sb.Seq, k.sb.Seq)
		return k.broken
	}
	return nil
}

// VaultID is the vault's immutable identity.
func (k *Keystore) VaultID() [16]byte { return k.sb.VaultID }

// Generation is the current VMK generation (§6.2).
func (k *Keystore) Generation() uint64 { return k.sb.VMKGeneration }

// RotationPending reports whether a rotation left slots behind (§8). The UI
// must keep surfacing it.
func (k *Keystore) RotationPending() bool { return k.sb.RotationPending != 0 }

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
func (k *Keystore) openRegistry(meta []byte) (*format.Registry, error) {
	g, err := registryAEAD(meta)
	if err != nil {
		return nil, err
	}
	plain, err := g.Open(nil, k.sb.RegistryNonce[:], k.ct, k.sb.RegistryAAD())
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
	slots   []format.SlotRecord // the new slot region; nil leaves it as it is
	reg     *format.Registry    // the registry to seal; every commit carries one (R25, R35)
	meta    []byte              // Metadata key, required when reg is set
	gen     uint64              // the new VMK generation
	pending bool                // rotation_pending after the commit
}

// commit writes tx in the order of §4 and §8: slot region into the inactive
// copy, registry into free space, sync, superblock into the inactive copy,
// sync, then trim the file to the live registry's end. On error before the
// superblock write nothing has changed; the superblock write itself is the
// commit point.
func (k *Keystore) commit(tx txn) error {
	if err := k.usable(); err != nil {
		return err
	}
	// Every commit re-seals the registry. It carries the slot region's hash,
	// which is what authenticates a region nobody else vouches for (R25),
	// and its AAD binds modified_at (R35), which every commit advances — a
	// commit that sealed nothing would leave a superblock whose date no
	// longer matches the ciphertext it points at, and the file would never
	// unlock again.
	if tx.reg == nil {
		return fmt.Errorf("%w: commit without a registry", ErrParams)
	}
	if err := k.checkOnDisk(); err != nil {
		return err
	}
	next := *k.sb
	next.Seq++
	next.VMKGeneration = tx.gen
	next.RotationPending = 0
	if tx.pending {
		next.RotationPending = 1
	}
	next.ModifiedAt = stamp(k.sb.ModifiedAt)

	region, slots := k.region, k.slots
	if tx.slots != nil {
		encoded, err := format.EncodeSlotRegion(tx.slots)
		if err != nil {
			return err
		}
		target := k.sb.LiveSlotRegion().Other()
		if _, err := k.f.WriteAt(encoded, int64(target.SlotRegionOff())); err != nil {
			return err
		}
		next.SlotRegionOff = target.SlotRegionOff()
		next.SlotRegionLen = uint64(len(encoded))
		region, slots = encoded, tx.slots
	}

	if tx.meta == nil {
		return fmt.Errorf("%w: registry without a Metadata key", ErrParams)
	}
	var ct []byte
	{
		if tx.slots != nil {
			tx.reg.SlotRegionHash = format.SlotRegionHash(region)
		}
		// Otherwise the hash stays what the registry carried in: the last
		// value the Metadata key authenticated. A registry-only write never
		// blesses a region nobody wrote (R25) — with a tampered region on
		// disk, the mismatch survives the write and is reported again at the
		// next unlock.
		tx.reg.ModifiedAt = next.ModifiedAt
		plain, err := encodeRegistry(tx.reg)
		if err != nil {
			return err
		}
		defer kdf.Zero(plain)
		if uint64(len(plain)) > format.MaxRegistryLen {
			return corrupt("registry of %d bytes exceeds %d", len(plain), format.MaxRegistryLen)
		}
		next.RegistryOff = k.registryTarget(uint64(len(plain)))
		next.RegistryLen = uint64(len(plain))
		if _, err := rand.Read(next.RegistryNonce[:]); err != nil {
			return err
		}
		g, err := registryAEAD(tx.meta)
		if err != nil {
			return err
		}
		sealed := g.Seal(nil, next.RegistryNonce[:], plain, next.RegistryAAD())
		next.RegistryTag = [format.TagSize]byte(sealed[len(plain):])
		if _, err := k.f.WriteAt(sealed, int64(next.RegistryOff)); err != nil {
			return err
		}
		ct = sealed
	}

	if err := k.f.Sync(); err != nil {
		return err
	}
	encoded, err := next.Encode()
	if err != nil {
		return err
	}
	// From here on a failure leaves the outcome unknown: the superblock may
	// be durable in part or in full. This handle stops reasoning about the
	// file (Broken); the caller reopens it and sees whichever state won.
	target := k.live.Other()
	if _, err := k.f.WriteAt(encoded, int64(target.KeystoreSuperblockOff())); err != nil {
		k.broken = fmt.Errorf("%w: superblock write failed at the commit point: %v", ErrIndeterminate, err)
		return k.broken
	}
	if err := k.f.Sync(); err != nil {
		k.broken = fmt.Errorf("%w: the change may already be durable: %v", ErrIndeterminate, err)
		return k.broken
	}
	// Committed. Trim what neither superblock copy references any more: the
	// losing copy's registry stays addressable, so that a later loss of the
	// live copy opens the file one commit behind rather than not at all.
	end := max(next.RegistryOff+next.RegistryLen+format.TagSize, k.sb.RegistryOff+k.sb.RegistryLen+format.TagSize)
	if end < k.size {
		if err := k.f.Truncate(int64(end)); err == nil {
			k.size = end
		}
	} else {
		k.size = end
	}
	k.sb, k.live, k.region, k.slots, k.ct = &next, target, region, slots, ct
	if tx.reg != nil {
		k.reg = tx.reg
		k.Stale = nil
	}
	return nil
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

// create writes a brand-new keystore file: superblock A with seq 1 and B
// with seq 0, slot region A, and the registry at the fixed offset. path must
// not exist.
func create(path string, vaultID [16]byte, slots []format.SlotRecord, reg *format.Registry, meta []byte, gen uint64, prev int64) (*Keystore, error) {
	region, err := format.EncodeSlotRegion(slots)
	if err != nil {
		return nil, err
	}
	reg.SlotRegionHash = format.SlotRegionHash(region)
	// prev is the source's modified_at for an export, so that a clock set
	// back cannot date a backup before the vault it was taken from (R35).
	reg.ModifiedAt = stamp(prev)
	plain, err := encodeRegistry(reg)
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

// encodeRegistry is the registry's encoder behind every commit — a seam,
// so that a test can write the version-1 registry an older Enfold wrote
// and watch it come back as version 2 (R38).
var encodeRegistry = func(g *format.Registry) ([]byte, error) { return g.Encode() }

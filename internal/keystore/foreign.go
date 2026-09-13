package keystore

import (
	"errors"
	"fmt"

	"github.com/dreamxwarden01/enfold/internal/format"
	"github.com/dreamxwarden01/enfold/internal/kdf"
)

// ForeignRegistry is another keystore file's registry, read for a merge
// (§18.2; APP.md §13 "Merge records"). Nothing in either file is written and no
// slot of the foreign file is unlocked by OpenForeign: the vault knows every
// VMK it ever had, so a backup of itself from any earlier generation opens with
// no key from the user at all.
type ForeignRegistry struct {
	// Registry is the foreign registry with every version record's archive key
	// already re-wrapped under this vault's KWK, with a fresh nonce and the same
	// AAD (R22). A rotation never changes an archive key, only its wrapper, so
	// the keys are unchanged; what changes is that the caller can copy a version
	// record verbatim into this vault and no bare archive key ever leaves this
	// package (APP.md §13, APP.md §1's boundary).
	Registry *format.Registry
	// VaultID and Generation are the foreign superblock's, reported for the
	// dialog. Generation ordered nothing and decided nothing: it is plaintext
	// and checksummed, never authenticated (§5, §18.2).
	VaultID    [16]byte
	Generation uint64
	// ModifiedAt is the foreign superblock's, so the dialog can date the copy.
	ModifiedAt int64
	// OpenedAt is the generation of this vault's VMK that opened the file, and
	// zero when the file was opened with its own key (OpenForeignUnlocked).
	OpenedAt uint64
	// Tampered is the foreign file's own R25 verdict: its slot region does not
	// match the hash its own registry authenticates. Reported, never a refusal —
	// nothing in that file is unlocked, every merged field comes from its
	// authenticated registry, and refusing would block a recovery path (§9 Q5).
	Tampered error
}

// tryForeign is one candidate VMK's attempt at a foreign registry: that VMK's
// Metadata key over the other file's own ciphertext, offset, length and nonce,
// with the AAD built from its own superblock fields — the AEAD deciding is what
// proves the file is this vault's (§18.2). A variable so that a test can count
// the attempts and hold the package to "each candidate at most once".
var tryForeign = func(vmk [32]byte, other *Keystore) (*format.Registry, error) {
	meta := kdf.MetadataKey(vmk, other.sb.VaultID)
	defer kdf.Zero(meta)
	return other.openRegistry(meta)
}

// OpenForeign reads another keystore file's registry without unlocking any of
// its slots and without writing anything, by trial against this vault's VMKs:
// the current one first, then each vmk_history VMK in descending generation,
// each attempted at most once (APP.md §13). No plaintext fact of the other file
// short-circuits the trial — not its vault_id, not its vmk_generation — because
// those are checksummed and unauthenticated (§5); only the AEAD decides.
//
// ErrForeign when nothing opens it: another vault, or a backup from a
// generation this vault no longer keeps, which happens when this vault was
// rolled back to an older copy. That is never reported as corruption of the
// file that was read (APP.md §13).
func (u *Unlocked) OpenForeign(other *Keystore) (*ForeignRegistry, error) {
	if err := u.current(); err != nil {
		return nil, err
	}
	if other == nil || other == u.k {
		return nil, fmt.Errorf("%w: OpenForeign needs a second keystore file", ErrParams)
	}
	if err := other.usable(); err != nil {
		return nil, err
	}
	reg, err := tryForeign(u.vmk(), other)
	if err == nil {
		return u.convert(other, reg, u.vmk(), u.gen)
	}
	if !errors.Is(err, ErrAuth) {
		// The AEAD opened and the plaintext did not decode: the file is this
		// vault's and its registry is damaged. That is corruption, and saying
		// "not this vault" instead would send the user looking for the wrong
		// file.
		return nil, err
	}

	vaultID := u.k.sb.VaultID
	kwks := kdf.KWKSecrets(u.vmk(), vaultID)
	defer kdf.Zero(kwks)
	gens := historyGens(u.k.reg)
	for i := len(gens) - 1; i >= 0; i-- {
		rec := u.k.reg.Secret(format.SecretVMKHistory, format.VMKHistoryID(gens[i]))
		if rec == nil {
			continue
		}
		vmk, err := openSecretWith(kwks, vaultID, rec)
		if err != nil {
			return nil, corrupt("the vmk_history record for generation %d does not open under the current KWK_secrets", gens[i])
		}
		reg, err := tryForeign(vmk, other)
		if err == nil {
			fr, cerr := u.convert(other, reg, vmk, gens[i])
			kdf.Zero(vmk[:])
			return fr, cerr
		}
		kdf.Zero(vmk[:])
		if !errors.Is(err, ErrAuth) {
			return nil, err
		}
	}
	return nil, ErrForeign
}

// OpenForeignUnlocked is OpenForeign for a file this vault's VMKs did not open:
// the caller has staged it and unlocked it with its own recovery key, and the
// records are converted from that file's VMK exactly as they would be from a
// history VMK (APP.md §13). Nothing is written to either file.
func (u *Unlocked) OpenForeignUnlocked(other *Unlocked) (*ForeignRegistry, error) {
	if err := u.current(); err != nil {
		return nil, err
	}
	if other == nil || other.k == u.k {
		return nil, fmt.Errorf("%w: OpenForeignUnlocked needs a second keystore file", ErrParams)
	}
	if err := other.current(); err != nil {
		return nil, err
	}
	// A copy of the foreign registry, so that re-wrapping the keys for this
	// vault does not touch the object that file's own handles share.
	reg, err := cloneRegistry(other.k.reg)
	if err != nil {
		return nil, err
	}
	// convert computes Tampered from the same region and the same authenticated
	// hash the foreign Unlocked read it against, so the verdict it reports is
	// that Unlocked's own.
	return u.convert(other.k, reg, other.vmk(), 0)
}

// convert re-wraps every version record's archive key from the KWK of the VMK
// that opened the foreign file — over that file's own vault_id — to this
// vault's KWK, with a fresh nonce and the AAD unchanged (R22). Every bare key
// is zeroed before it returns.
func (u *Unlocked) convert(other *Keystore, reg *format.Registry, opened [32]byte, openedAt uint64) (*ForeignRegistry, error) {
	from := kdf.KWK(opened, other.sb.VaultID)
	defer kdf.Zero(from)
	to := kdf.KWK(u.vmk(), u.k.sb.VaultID)
	defer kdf.Zero(to)
	for a := range reg.Archives {
		ar := &reg.Archives[a]
		for v := range ar.Versions {
			ver := &ar.Versions[v]
			aad := format.ArchiveKeyAAD(ar.ArchiveID, ver.KID)
			key, err := kdf.UnwrapKey(from, ver.WrappedArchiveKey, ver.WrapNonce, aad)
			if err != nil {
				return nil, corrupt("archive %x version %x does not unwrap under the VMK that opened this file", ar.ArchiveID, ver.KID)
			}
			ver.WrappedArchiveKey, ver.WrapNonce, err = kdf.WrapKey(to, key, aad)
			kdf.Zero(key[:])
			if err != nil {
				return nil, err
			}
		}
	}
	fr := &ForeignRegistry{
		Registry:   reg,
		VaultID:    other.sb.VaultID,
		Generation: other.sb.VMKGeneration,
		ModifiedAt: other.sb.ModifiedAt,
		OpenedAt:   openedAt,
	}
	if err := reg.VerifySlotRegion(other.region); err != nil {
		fr.Tampered = fmt.Errorf("%w: %v", ErrTampered, err)
	}
	return fr, nil
}

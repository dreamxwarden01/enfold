package kdf

import (
	"crypto/hkdf"
	"crypto/sha256"
	"errors"
	"fmt"

	"golang.org/x/crypto/argon2"

	"github.com/dreamxwarden01/enfold/internal/format"
)

// Sizes, in bytes.
const (
	KeySize          = 32
	SeedXSize        = 32
	SeedKSize        = 64
	IDSize           = 16
	SaltSize         = 32
	EntangleSaltSize = 16 // the slot region header's entangle_salt (FORMAT §6)
	NonceSize        = 12
	TagSize          = 16
	VMKWrapSize      = KeySize + 8 + TagSize // R12: VMK ‖ u64 generation, sealed
	// KeyWrapSize seals a 32-byte key: an archive key, a DEK, the identity
	// key — and every secrets record (§7.6, R22), whose plaintext is padded
	// to 32 where the secret is shorter.
	KeyWrapSize = KeySize + TagSize
)

// Salt is a slot record's `salt` field: the input to salt' for Argon2id in the
// standalone password slot, and since Revision 2 in that slot alone — no
// hardware slot runs Argon2id, and a hardware and a recovery slot write the
// field as zero (R13, R24). SlotSalt is the record's `slot_salt`: the HKDF salt
// for seed derivation in software slots. They are distinct types so that the
// two 32-byte fields cannot be swapped without a visible conversion — the
// confusion R13 exists to prevent.
type (
	Salt     [SaltSize]byte
	SlotSalt [SaltSize]byte
)

// EntangleSalt is the slot region header's entangle_salt (FORMAT §6): the
// vault's, 16 bytes, redrawn whenever the password is set or changed. A
// distinct type and a distinct length from Salt and SlotSalt (R13); this
// package does not police its value, since §6 puts the one enforcement point
// in format.SlotRegionHeader.Validate.
type EntangleSalt [EntangleSaltSize]byte

// Info-string prefixes (R3). ASCII, no terminator; the identities that follow
// are appended raw (R2).
const (
	InfoIK           = "Enfold/v1/IK"
	InfoEntangle     = "Enfold/v1/entangle"
	InfoRecoveryX    = "Enfold/v1/recovery/x25519"
	InfoRecoveryK    = "Enfold/v1/recovery/mlkem"
	InfoRecoveryPre  = "Enfold/v1/recovery/combine"
	InfoPasswordX    = "Enfold/v1/password/x25519"
	InfoPasswordK    = "Enfold/v1/password/mlkem"
	InfoPasswordPre  = "Enfold/v1/password/combine"
	InfoMetadata     = "Enfold/v1/metadata"
	InfoDB           = "Enfold/v1/db"
	InfoKWK          = "Enfold/v1/wrap/archive"
	InfoKWKIdentity  = "Enfold/v1/wrap/identity"
	InfoKWKSecrets   = "Enfold/v1/wrap/secrets"
	InfoArchiveIndex = "Enfold/v1/archive/index"
	InfoArchiveWrap  = "Enfold/v1/archive/wrap"
)

var (
	// ErrEmptyPassword is returned for a password that is empty after
	// normalisation (R4): "no password" is a recorded state, never an inference.
	ErrEmptyPassword = errors.New("kdf: empty password")
	// ErrParams is returned for Argon2id parameters outside R24's bounds.
	ErrParams = errors.New("kdf: invalid argon2 parameters")
	// ErrKeySize is returned for key material of the wrong length.
	ErrKeySize = errors.New("kdf: wrong key size")
	// ErrSecretPadding is returned for a secrets-record plaintext whose pad is
	// not zero (§7.6): a reader takes the first sixteen bytes of a
	// recovery_escrow record and rejects the record if the tail is not zero.
	ErrSecretPadding = errors.New("kdf: secret plaintext is not zero-padded")
)

// Argon2Params are the per-slot Argon2id parameters (R6). MemKiB is in KiB and
// Threads is the parallelism p, which is part of the function.
type Argon2Params struct {
	MemKiB  uint32
	Time    uint32
	Threads uint8
}

// Validate applies the format layer's bounds (R24): m in [8·p, 2 GiB] KiB,
// t in [1, 32], p in [1, 32]. The upper bounds are what keep a hostile slot
// record from turning an unlock into a multi-gigabyte allocation.
func (p Argon2Params) Validate() error {
	if err := format.ValidateArgon2(p.MemKiB, p.Time, p.Threads); err != nil {
		return fmt.Errorf("%w: m=%d KiB t=%d p=%d", ErrParams, p.MemKiB, p.Time, p.Threads)
	}
	return nil
}

// hkdfN is HKDF-SHA256, Extract-then-Expand (R1). A nil or empty salt is the
// zero-length salt, which Extract treats as 32 zero bytes. Every caller asks
// for 32 or 64 bytes with an IKM of at least 16, so hkdf.Key's errors are not
// expected outside FIPS-140-only mode, which this program does not run in.
//
// The IKM here may be a slice of a secmem page — the VMK's, say. crypto/hkdf
// copies it into the HMAC state, and the output is a fresh Go slice: both are
// heap the wrapper does not reach, which is what SCOPE.md's line means by a
// library's own copy.
func hkdfN(ikm, salt, info []byte, n int) []byte {
	out, err := hkdf.Key(sha256.New, ikm, salt, string(info), n)
	if err != nil {
		panic("kdf: hkdf: " + err.Error())
	}
	return out
}

// Info builds prefix ‖ id₁ ‖ id₂ … with no separators (R2, R3).
func Info(prefix string, ids ...[IDSize]byte) []byte {
	out := make([]byte, 0, len(prefix)+len(ids)*IDSize)
	out = append(out, prefix...)
	for i := range ids {
		out = append(out, ids[i][:]...)
	}
	return out
}

// Conventions: fixed-size secrets that enter a derivation (pre, VMK, archive
// key) are arrays, so a wrong-length value cannot be passed; derived keys come
// back as 32-byte slices, since that is what the AEAD and HKDF consume next.

// DeriveIK is the last step every slot shares:
// IK = HKDF(pre, salt = ∅, info = "Enfold/v1/IK" ‖ vault_id ‖ recipient_id) → 32.
func DeriveIK(pre [KeySize]byte, vaultID, recipientID [IDSize]byte) []byte {
	return hkdfN(pre[:], nil, Info(InfoIK, vaultID, recipientID), KeySize)
}

// argon2id is Argon2id version 0x13 with m in KiB, p threads exactly, 32 bytes
// out (R6). Parameters are validated by the callers that own them.
func argon2id(pwd, salt []byte, p Argon2Params) []byte {
	return argon2.IDKey(pwd, salt, p.Time, p.MemKiB, p.Threads, KeySize)
}

// SaltPrime is salt' = SHA-256(salt ‖ vault_id ‖ recipient_id), the Argon2id
// salt of the standalone password slot (R8, R13). Since Revision 2 no hardware
// slot runs Argon2id, so this is the only slot type that has a salt' at all;
// the vault's entangled password uses VaultSaltPrime instead.
func SaltPrime(salt Salt, vaultID, recipientID [IDSize]byte) [SaltSize]byte {
	buf := make([]byte, 0, SaltSize+2*IDSize)
	buf = append(buf, salt[:]...)
	buf = append(buf, vaultID[:]...)
	buf = append(buf, recipientID[:]...)
	return sha256.Sum256(buf)
}

// VaultSaltPrime is vault_salt' = SHA-256(entangle_salt ‖ vault_id), the
// Argon2id salt of K_P (§3.1). No recipient_id: the salt is the vault's, since
// the entangled password is the vault's and not a slot's (§18.1).
func VaultSaltPrime(es EntangleSalt, vaultID [IDSize]byte) [SaltSize]byte {
	buf := make([]byte, 0, EntangleSaltSize+IDSize)
	buf = append(buf, es[:]...)
	buf = append(buf, vaultID[:]...)
	return sha256.Sum256(buf)
}

// Subordinate keys below the VMK (§3).

// MetadataKey derives the key that encrypts the registry.
func MetadataKey(vmk [KeySize]byte, vaultID [IDSize]byte) []byte {
	return hkdfN(vmk[:], nil, Info(InfoMetadata, vaultID), KeySize)
}

// DBKey derives the key for local caches.
func DBKey(vmk [KeySize]byte, vaultID [IDSize]byte) []byte {
	return hkdfN(vmk[:], nil, Info(InfoDB, vaultID), KeySize)
}

// KWK derives the key that wraps archive keys.
func KWK(vmk [KeySize]byte, vaultID [IDSize]byte) []byte {
	return hkdfN(vmk[:], nil, Info(InfoKWK, vaultID), KeySize)
}

// KWKIdentity derives the key that wraps the sync identity key — a separate
// wrapping domain from KWK on purpose (§3.2).
func KWKIdentity(vmk [KeySize]byte, vaultID [IDSize]byte) []byte {
	return hkdfN(vmk[:], nil, Info(InfoKWKIdentity, vaultID), KeySize)
}

// KWKSecrets derives the key that seals every record of the secrets section
// (§7.6): escrowed recovery keys (R38), the entangled password's key K_P
// (§3.1), and every retired VMK (§8). Its own wrapping domain, and one a
// Session never derives — only a holder of the VMK opens a secrets record.
func KWKSecrets(vmk [KeySize]byte, vaultID [IDSize]byte) []byte {
	return hkdfN(vmk[:], nil, Info(InfoKWKSecrets, vaultID), KeySize)
}

// Keys below an archive key (§3).

// ArchiveIndexKey derives the key that encrypts an archive's file index.
func ArchiveIndexKey(archiveKey [KeySize]byte, archiveID [IDSize]byte) []byte {
	return hkdfN(archiveKey[:], nil, Info(InfoArchiveIndex, archiveID), KeySize)
}

// ArchiveWrapKey derives the key that wraps an archive's per-file DEKs.
func ArchiveWrapKey(archiveKey [KeySize]byte, archiveID [IDSize]byte) []byte {
	return hkdfN(archiveKey[:], nil, Info(InfoArchiveWrap, archiveID), KeySize)
}

// Zero overwrites a secret. Go may hold other copies; this removes the one the
// caller controls.
func Zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

package format

// FormatVersion is the on-disk format version written into every superblock
// and envelope, and mixed into every AAD that carries it.
const FormatVersion uint16 = 1

// Fixed sizes and offsets (docs/FORMAT.md §4, §5, §9, §12).
const (
	SuperblockSize = 4096
	checksumOffset = SuperblockSize - 32 // SHA-256 over [0, 4064)

	KeystoreSuperblockAOff = 0x00000
	KeystoreSuperblockBOff = 0x01000
	SlotRegionAOff         = 0x02000
	SlotRegionBOff         = 0x22000
	SlotRegionSize         = 128 * 1024
	RegistryMinOff         = 0x42000

	EnvelopeOff           = 0x0000
	ArchiveSuperblockAOff = 0x1000
	ArchiveSuperblockBOff = 0x2000
	ArchiveDataStart      = 0x3000

	// MaxSlots caps slot_count (§4): a vault with more unlock methods is a
	// misconfiguration, and the cap bounds what a parser will allocate.
	MaxSlots = 32

	ChunkSize      = 65536
	TagSize        = 16
	NonceSize      = 12
	WrappedVMKSize = 56 // VMK ‖ u64 generation (40) + tag
	WrappedKeySize = 48 // 32-byte key + tag
	// WrappedSecretSize is one secrets-section ciphertext (§7.6, R22): a
	// 32-byte plaintext and its tag. A secret shorter than 32 bytes is padded
	// by the crypto layer, never by this one.
	WrappedSecretSize = 32 + TagSize

	// MinSlotRegionLen is the slot region header (§6): u32 slot_count, u8
	// entangle, u8 argon2_p, u8[2] reserved, u32 argon2_m, u32 argon2_t and
	// u8[16] entangle_salt. The first record begins at offset 32 and a region
	// shorter than the header is invalid.
	MinSlotRegionLen = 32

	// MaxDescriptionLen bounds an archive record's description (§7.1): at most
	// 1 024 bytes of UTF-8, longer is invalid.
	MaxDescriptionLen = 1024

	MLKEMEKSize   = 1568
	MLKEMCTSize   = 1568
	P256PubSize   = 65 // 0x04 ‖ X ‖ Y
	X25519PubSize = 32

	// Argon2id parameter bounds (R24). The lower bounds are the function's
	// own; the upper bounds exist so that a hostile slot record cannot turn an
	// unlock attempt into a multi-gigabyte allocation. 2 GiB is twice the top
	// of DESIGN.md's recommended range.
	MinArgon2MemKiB  = 8
	MaxArgon2MemKiB  = 2 << 20 // 2 GiB
	MaxArgon2Time    = 32
	MaxArgon2Threads = 32
	// MaxArgon2Work bounds m_KiB × t: 8 GiB·passes, i.e. 2 GiB × 4, 1 GiB × 8
	// or 512 MiB × 16. The memory ceiling alone would still allow 32 passes
	// over 2 GiB from a hostile record.
	MaxArgon2Work = 8 << 20
)

// Magic values. Files are identified by these, never by extension (§2).
var (
	KeystoreMagic          = [8]byte{'E', 'N', 'F', 'O', 'L', 'D', 'K', 1}
	ArchiveMagic           = [8]byte{'E', 'N', 'F', 'O', 'L', 'D', 'A', 1}
	ArchiveSuperblockMagic = [8]byte{'E', 'N', 'F', 'O', 'L', 'D', 'S', 1}
)

// SlotState is slot_state (§6).
type SlotState uint8

const (
	SlotEmpty   SlotState = 0
	SlotActive  SlotState = 1
	SlotRetired SlotState = 2
)

// SlotType is slot_type (§6, §14).
type SlotType uint8

const (
	SlotExternalECDH       SlotType = 1
	SlotStandalonePassword SlotType = 2
	SlotRecovery           SlotType = 3
)

// KeySource is key_source, meaningful for SlotExternalECDH only (§6, §14).
type KeySource uint8

const (
	KeySourceYubiKeyPIV  KeySource = 1
	KeySourcePRFDerived  KeySource = 2 // reserved: a v1 reader fails closed on it
	KeySourcePhoneNative KeySource = 3
)

// CurveID is curve_id (§14).
type CurveID uint8

const (
	CurveP256   CurveID = 1
	CurveX25519 CurveID = 2
)

// Slot flags (§6). Bit 0 (was has_entangled_password) and bit 3 (was
// rewrap_stale) are reserved since Revision 2 and must be zero: the vault's
// entangled password lives in the slot region header (§18.1) and no rotation
// is deferred any more (§8), so R29 and R30 are retired with them.
const (
	FlagPRFRawSaltMode uint32 = 1 << 1 // reserved with key_source 2
	FlagUVRequired     uint32 = 1 << 2

	knownSlotFlags = FlagPRFRawSaltMode | FlagUVRequired
)

// SecretKind is a secrets-section record's kind (§7.6). A value outside 1–3
// fails closed (§1).
type SecretKind uint8

const (
	// SecretRecoveryEscrow keeps one recovery key per active recovery slot,
	// keyed by that slot's recipient_id (R38).
	SecretRecoveryEscrow SecretKind = 1
	// SecretEntangledKey keeps K_P, the vault's entangled password key
	// (§3.1); at most one record, its id sixteen zero bytes.
	SecretEntangledKey SecretKind = 2
	// SecretVMKHistory keeps one retired VMK per past generation (§8), its id
	// that generation as a little-endian u64 in bytes 0–7, bytes 8–15 zero.
	SecretVMKHistory SecretKind = 3
)

// ForgottenRetentionSeconds is the thirty days of §18.2: a writer may drop a
// forgotten archive record only in a write whose own modified_at exceeds
// forgotten_at by more than this.
const ForgottenRetentionSeconds int64 = 2_592_000

// Archive record policy bits (§7.1).
const (
	PolicyAlwaysRequireFullAuth uint32 = 1 << 0
	// PolicyHidden: the archive is not listed; its record, keys included,
	// stays in the registry. The reversible form of "remove from list".
	PolicyHidden uint32 = 1 << 1
	// PolicyNoCompression: every file in the archive is stored raw (DESIGN.md
	// §11 trap 8); a writer's choice that must travel with the archive.
	PolicyNoCompression uint32 = 1 << 2

	knownPolicyBits = PolicyAlwaysRequireFullAuth | PolicyHidden | PolicyNoCompression
)

// VersionState is the state of a version record (§7.2).
type VersionState uint8

const (
	VersionCurrent VersionState = 1
	VersionRetired VersionState = 2
)

// Peer capabilities and device classes (§7.5).
const (
	CapOpenArchive uint32 = 1 << 0
	CapAppendDEK   uint32 = 1 << 1
	CapFullSync    uint32 = 1 << 2

	knownCapabilities = CapOpenArchive | CapAppendDEK | CapFullSync
)

// DeviceClass is device_class (§7.5).
type DeviceClass uint8

const (
	DeviceEphemeralSession DeviceClass = 1
	DeviceEnrolledPersonal DeviceClass = 2
)

// FileState is the state of a file record (§11).
type FileState uint8

const (
	FileLive      FileState = 1
	FileTombstone FileState = 2
)

// Storage is the storage field of a file record (§11, §14).
type Storage uint8

const (
	StorageRaw      Storage = 1
	StorageZstd     Storage = 2
	StorageZstdDict Storage = 3
)

// AlgID is alg_id (§14).
type AlgID uint16

const (
	AlgAES256GCM        AlgID = 1
	AlgChaCha20Poly1305 AlgID = 2 // reserved: a v1 reader fails closed on it
)

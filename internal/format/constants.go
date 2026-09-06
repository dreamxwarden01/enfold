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

// Slot flags (§6).
const (
	FlagEntangledPassword uint32 = 1 << 0
	FlagPRFRawSaltMode    uint32 = 1 << 1 // reserved with key_source 2
	FlagUVRequired        uint32 = 1 << 2
	FlagRewrapStale       uint32 = 1 << 3

	knownSlotFlags = FlagEntangledPassword | FlagPRFRawSaltMode | FlagUVRequired | FlagRewrapStale
)

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

package app

// The view structs that cross into the WebView. Nothing here carries a key,
// an offset, a generation or a wrapped blob; ids are hex strings; times are
// Unix seconds (APP.md §3).

// VaultState is the session's state.
type VaultState string

const (
	StateNone      VaultState = "none"      // no vault configured yet
	StateLocked    VaultState = "locked"    // keys not in memory
	StateUnlocking VaultState = "unlocking" // a ceremony is running
	StateReleasing VaultState = "releasing" // a ceremony is ending; the card is being released
	StateUnlocked  VaultState = "unlocked"
	StateBroken    VaultState = "broken" // a commit's outcome is unknown; Reopen
	StateBusy      VaultState = "busy"   // the vault file is open in another process
)

// VaultStatus is the whole state the frontend renders from; every field is
// safe to show.
type VaultStatus struct {
	Seq             uint64         `json:"seq"`
	State           VaultState     `json:"state"`
	Path            string         `json:"path"`
	DisplayName     string         `json:"displayName"`
	LastUnlockedAt  int64          `json:"lastUnlockedAt"`
	LocksAt         int64          `json:"locksAt"`    // idle deadline, 0 when locked
	AbsoluteAt      int64          `json:"absoluteAt"` // absolute deadline, 0 when locked
	ModifiedAt      int64          `json:"modifiedAt"` // the keystore's own date (R35)
	RotationPending bool           `json:"rotationPending"`
	Tampered        bool           `json:"tampered"`
	Warnings        []Code         `json:"warnings"`
	Ceremony        *CeremonyState `json:"ceremony,omitempty"`
	Ops             []OpView       `json:"ops"`
	OpenArchives    int            `json:"openArchives"`
	DirtyArchives   int            `json:"dirtyArchives"`
	HasPasswordSlot bool           `json:"hasPasswordSlot"`
	HasHardwareSlot bool           `json:"hasHardwareSlot"`
	SetupNeeded     bool           `json:"setupNeeded"`     // only a recovery slot: FinishSetup is the one action
	DefaultPath     string         `json:"defaultPath"`     // where the vault lives unless kept elsewhere
	MissingPath     string         `json:"missingPath"`     // a configured vault that could not be opened at start
	KeptElsewhere   bool           `json:"keptElsewhere"`   // the vault is not at DefaultPath (the override is set)
	RetiredCopies   int            `json:"retiredCopies"`   // vault-replaced-*.eks files in the data folder
	RetiredPath     string         `json:"retiredPath"`     // the newest of them
	Damaged         bool           `json:"damaged"`         // MissingPath is there and not a keystore: the rebuild of APP.md §2.1 applies
	DamagedCopyPath string         `json:"damagedCopyPath"` // the newest vault-damaged-*.eks kept for salvage
}

// CeremonyStep is where the unlock or enrollment ceremony is.
type CeremonyStep string

const (
	StepWaitingForKey CeremonyStep = "waiting_for_key"
	StepTwoKeys       CeremonyStep = "two_keys"
	StepProbing       CeremonyStep = "probing"
	StepNoMatch       CeremonyStep = "no_match"
	StepBusy          CeremonyStep = "busy"
	StepPassword      CeremonyStep = "password"
	StepPIN           CeremonyStep = "pin"
	StepTouch         CeremonyStep = "touch"
	StepBlocked       CeremonyStep = "blocked"
	StepDeriving      CeremonyStep = "deriving"
	StepManagementKey CeremonyStep = "management_key"
	StepRecovery      CeremonyStep = "recovery"
	StepSwapKey       CeremonyStep = "swap_key"
	StepReleasing     CeremonyStep = "releasing"
	StepDone          CeremonyStep = "done"
	StepFailed        CeremonyStep = "failed"
)

// CeremonyState is the ceremony as the panel shows it.
type CeremonyState struct {
	Seq          uint64       `json:"seq"`
	Kind         string       `json:"kind"` // unlock | create | enroll | remove | rotate | export | import | setup | verify | reveal
	Step         CeremonyStep `json:"step"`
	PromptID     string       `json:"promptId,omitempty"`
	Choose       bool         `json:"choose"` // the prompt asks for a new secret (create, enrol), not an existing one
	SlotLabel    string       `json:"slotLabel,omitempty"`
	Retries      int          `json:"retries"`
	RetriesKnown bool         `json:"retriesKnown"`
	Verified     bool         `json:"verified"`
	ReaderCount  int          `json:"readerCount"`
	N            int          `json:"n"` // touch ordinal on this token handle
	PINAsked     bool         `json:"pinAsked"`
	Error        Code         `json:"error,omitempty"`
	// SwapKey: the labels involved.
	RemoveLabel string `json:"removeLabel,omitempty"`
	InsertLabel string `json:"insertLabel,omitempty"`
	// Verify, at Done: how many archives the backup's registry names.
	Archives int `json:"archives"`
}

// Reader is a PC/SC reader that looks like a YubiKey.
type Reader struct {
	Name string `json:"name"`
}

// TextPreview is a text file's head for the preview pane.
type TextPreview struct {
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
}

// SlotView describes a way to unlock.
type SlotView struct {
	RecipientID string `json:"recipientId"`
	Type        string `json:"type"` // hardware | password | recovery
	Label       string `json:"label"`
	CreatedAt   int64  `json:"createdAt"`
	Entangled   bool   `json:"entangled"`
	Stale       bool   `json:"stale"`
	Escrowed    bool   `json:"escrowed"` // a recovery slot whose key can be shown again (FORMAT R38); known while Unlocked
}

// ArchiveSummary is a row of the archives list, from the registry plus
// what the core knows about open archives.
type ArchiveSummary struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Path          string `json:"path"`
	StoredSize    uint64 `json:"storedSize"`
	LastWrittenAt int64  `json:"lastWrittenAt"`
	KeyVersion    int    `json:"keyVersion"`
	Open          bool   `json:"open"`
	Dirty         int    `json:"dirty"`
	ReceiptOwed   bool   `json:"receiptOwed"`
	NoCompression bool   `json:"noCompression"`
	Hidden        bool   `json:"hidden"`
	HashBehind    uint64 `json:"hashBehind"` // commits since the hash was refreshed
	Note          Code   `json:"note,omitempty"`
	// For open archives only.
	Files     int    `json:"files"`
	FreeSpace uint64 `json:"freeSpace"`
	State     string `json:"state,omitempty"` // open | dirty | compacting | needs_reopen
}

// ArchiveStat is the open archive's status strip.
type ArchiveStat struct {
	Seq          uint64 `json:"seq"`
	ID           string `json:"id"`
	Name         string `json:"name"`
	Size         uint64 `json:"size"`
	Files        int    `json:"files"`
	FreeSpace    uint64 `json:"freeSpace"`
	KeyVersion   int    `json:"keyVersion"`
	LastSavedAt  int64  `json:"lastSavedAt"`
	Dirty        int    `json:"dirty"`
	State        string `json:"state"`
	ExpiresAt    int64  `json:"expiresAt"` // the archive's own idle deadline
	CapAt        int64  `json:"capAt"`     // the dirty cap, 0 when clean
	ReceiptOwed  bool   `json:"receiptOwed"`
	SessionAlive bool   `json:"sessionAlive"` // Save is possible now
	CopyMismatch bool   `json:"copyMismatch"` // the file is not the copy the vault last saw
}

// FileRow is one row of a page.
type FileRow struct {
	FileID       string `json:"fileId"`
	Path         string `json:"path"` // the full stored name
	Name         string `json:"name"` // the leaf within the folder asked for
	Size         uint64 `json:"size"`
	Storage      string `json:"storage"` // raw | zstd | zstd+dict
	SavedPercent int    `json:"savedPercent"`
	ModifiedAt   int64  `json:"modifiedAt"`
	Pending      string `json:"pending,omitempty"` // added | replaced | renamed | deleted
	IsFolder     bool   `json:"isFolder"`
	Files        int    `json:"files"` // folders: files under it
}

// Page is one screenful of an archive folder.
type Page struct {
	Seq    uint64    `json:"seq"`
	Folder string    `json:"folder"`
	Rows   []FileRow `json:"rows"`
	Total  int       `json:"total"`
}

// Collision is what CheckNames reports.
type Collision struct {
	Name     string `json:"name"`
	Existing string `json:"existing"` // the file id of the row it collides with
	Pending  bool   `json:"pending"`
}

// FileOutcome is one file's result inside a batch operation.
type FileOutcome struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Outcome string `json:"outcome"` // added | replaced | renamed | skipped | extracted | failed
	Code    Code   `json:"code,omitempty"`
}

// OpView is a running or finished long operation.
type OpView struct {
	ID        string        `json:"id"`
	Kind      string        `json:"kind"`
	ArchiveID string        `json:"archiveId,omitempty"`
	Done      uint64        `json:"done"`
	Total     uint64        `json:"total"`
	Phase     string        `json:"phase"`
	StartedAt int64         `json:"startedAt"`
	Finished  bool          `json:"finished"`
	Error     Code          `json:"error,omitempty"`
	Results   []FileOutcome `json:"results,omitempty"`
}

// FileInfo is what a backup file says about itself before any unlock.
type FileInfo struct {
	Path         string `json:"path"`
	Kind         string `json:"kind"` // vault | backup (recovery slots only, R28)
	ModifiedAt   int64  `json:"modifiedAt"`
	VaultMatches bool   `json:"vaultMatches"` // the same vault as the one kept (plaintext; proven only by an unlock)
	SlotCount    int    `json:"slotCount"`
	Hardware     int    `json:"hardware"` // slots by kind, so a confirmation can say what is traded away
	Password     int    `json:"password"`
	Recovery     int    `json:"recovery"`
	Newer        bool   `json:"newer"` // than the vault kept (plaintext, R35: dated, not authenticated)
}

const (
	FileKindVault  = "vault"
	FileKindBackup = "backup"
)

// Settings is the machine-local settings file plus the registry's timeouts.
type Settings struct {
	VaultPath          string `json:"vaultPath"`
	DisplayName        string `json:"displayName"`
	CloseToTray        string `json:"closeToTray"` // destroy | hide
	Theme              string `json:"theme"`       // system | light | dark
	Look               string `json:"look"`        // native
	RecoveryRecordPct  int    `json:"recoveryRecordPct"`
	DictionaryBelow    int64  `json:"dictionaryBelow"`
	IdleMinutes        int    `json:"idleMinutes"`     // from the registry; 0 = default
	AbsoluteMinutes    int    `json:"absoluteMinutes"` // from the registry; 0 = default
	TimeoutsFromVault  bool   `json:"timeoutsFromVault"`
	TimeoutsAdjustable bool   `json:"timeoutsAdjustable"` // only while unlocked
}

// Events the core emits.
const (
	EventVaultState      = "vault.state"
	EventVaultCeremony   = "vault.ceremony"
	EventVaultWarning    = "vault.warning"
	EventArchivesChanged = "archives.changed"
	EventArchiveChanged  = "archive.changed"
	EventArchiveExpiring = "archive.expiring"
	EventOpProgress      = "op.progress"
	EventOpDone          = "op.done"
)

// ArchiveChanged is the payload of EventArchiveChanged.
type ArchiveChanged struct {
	ID  string `json:"id"`
	Seq uint64 `json:"seq"`
}

// ArchiveExpiring is the payload of EventArchiveExpiring.
type ArchiveExpiring struct {
	ID       string `json:"id"`
	ClosesAt int64  `json:"closesAt"`
	Dirty    int    `json:"dirty"`
}

// Warning is the payload of EventVaultWarning.
type Warning struct {
	Code Code `json:"code"`
}

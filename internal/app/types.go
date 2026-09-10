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
	Seq            uint64     `json:"seq"`
	State          VaultState `json:"state"`
	Path           string     `json:"path"`
	DisplayName    string     `json:"displayName"`
	LastUnlockedAt int64      `json:"lastUnlockedAt"`
	LocksAt        int64      `json:"locksAt"`    // idle deadline, 0 when locked
	AbsoluteAt     int64      `json:"absoluteAt"` // absolute deadline, 0 when locked
	ModifiedAt     int64      `json:"modifiedAt"` // the keystore's own date (R35)
	// Entangled is the slot region header's switch (FORMAT.md §6, §18.1):
	// the vault's password, not a slot's, and a plaintext fact the lock
	// screen has while Locked.
	Entangled bool `json:"entangled"`
	// VaultFileSize is the vault file's own size, for the Archives header
	// line (APP.md §13).
	VaultFileSize uint64 `json:"vaultFileSize"`
	Tampered      bool   `json:"tampered"`
	// TamperedReason names which check said so: vault.tampered_hash (the
	// slot region does not match the registry, R25) or
	// vault.tampered_generation (the slot region and the superblock do not
	// belong together, FORMAT.md §6.2).
	TamperedReason  Code           `json:"tamperedReason,omitempty"`
	Warnings        []Code         `json:"warnings"`
	Ceremony        *CeremonyState `json:"ceremony,omitempty"`
	Ops             []OpView       `json:"ops"`
	OpenArchives    int            `json:"openArchives"`
	HasPasswordSlot bool           `json:"hasPasswordSlot"`
	HasHardwareSlot bool           `json:"hasHardwareSlot"`
	// PendingTouch: a cancelled ceremony's key call is still answering —
	// the key waits for a touch — and an unlock can pick it up (§2.2).
	PendingTouch bool `json:"pendingTouch"`
	// Note is the quiet line the lock screen carries from a ceremony that
	// has already ended: token.password_deadline, the five minutes from
	// the touch running out with the vault's password not given (§2.2).
	// The next ceremony clears it.
	Note            Code   `json:"note,omitempty"`
	SetupNeeded     bool   `json:"setupNeeded"`     // only a recovery slot: FinishSetup is the one action
	DefaultPath     string `json:"defaultPath"`     // where the vault lives unless kept elsewhere
	MissingPath     string `json:"missingPath"`     // a configured vault that could not be opened at start
	KeptElsewhere   bool   `json:"keptElsewhere"`   // the vault is not at DefaultPath (the override is set)
	RetiredCopies   int    `json:"retiredCopies"`   // vault-replaced-*.eks files in the data folder
	RetiredPath     string `json:"retiredPath"`     // the newest of them
	Damaged         bool   `json:"damaged"`         // MissingPath is there and not a keystore: the rebuild of APP.md §2.1 applies
	DamagedCopyPath string `json:"damagedCopyPath"` // the newest vault-damaged-*.eks kept for salvage
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
	StepRecords       CeremonyStep = "records"
	StepSwapKey       CeremonyStep = "swap_key"
	StepReleasing     CeremonyStep = "releasing"
	StepDone          CeremonyStep = "done"
	StepFailed        CeremonyStep = "failed"
)

// CeremonyState is the ceremony as the panel shows it.
type CeremonyState struct {
	Seq  uint64 `json:"seq"`
	Kind string `json:"kind"` // unlock | create | enroll | remove | rotate | export | import | setup | verify | reveal | entangle
	// Method is the way in this ceremony took: token | password | recovery.
	// The lock screen's wording follows the way in chosen, not whether a
	// secret was asked — with the vault's password on, a token unlock asks
	// for one too (APP.md §2.2, §13).
	Method    string       `json:"method,omitempty"`
	Step      CeremonyStep `json:"step"`
	PromptID  string       `json:"promptId,omitempty"`
	Choose    bool         `json:"choose"` // the prompt asks for a new secret (create, enrol), not an existing one
	SlotLabel string       `json:"slotLabel,omitempty"`
	// RecoveryID is the recovery key's ID at StepRecovery, XXXX-XXXX
	// (FORMAT.md §18.4): the sheet the digits belong to.
	RecoveryID   string `json:"recoveryId,omitempty"`
	Retries      int    `json:"retries"`
	RetriesKnown bool   `json:"retriesKnown"`
	Verified     bool   `json:"verified"`
	ReaderCount  int    `json:"readerCount"`
	N            int    `json:"n"` // touch ordinal on this token handle
	PINAsked     bool   `json:"pinAsked"`
	Error        Code   `json:"error,omitempty"`
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

// SlotView describes a way to unlock. Whether a hardware slot needs the
// vault's password is not a slot fact since Revision 2 — it is
// VaultStatus.Entangled — and every recovery slot has its kept key from the
// commit that made it (FORMAT R38), so neither is on the view.
type SlotView struct {
	RecipientID string `json:"recipientId"`
	Type        string `json:"type"` // hardware | password | recovery
	Label       string `json:"label"`
	CreatedAt   int64  `json:"createdAt"`
	// RecoveryID is the key's ID, XXXX-XXXX, on recovery slots only
	// (FORMAT.md §18.4): it catches the wrong sheet before a digit is typed.
	RecoveryID string `json:"recoveryId,omitempty"`
	Removable  bool   `json:"removable"` // the invariant would still hold without it (keystore.Removable)
}

// EntangledState is the Keys page's row for the vault's password (APP.md
// §13): whether the switch is on, and whether it could be turned on at all.
type EntangledState struct {
	On        bool `json:"on"`
	CanEnable bool `json:"canEnable"`
	Reason    Code `json:"reason,omitempty"` // vault.invariant when CanEnable is false
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
	ReceiptOwed   bool   `json:"receiptOwed"`
	// Method is how the archive is compressed — store | fastest | normal |
	// better | best — read from the record's policy (FORMAT.md §7.1 bits
	// 2–5) and followed by every writer of it.
	Method     string `json:"method"`
	Hidden     bool   `json:"hidden"`
	HashBehind uint64 `json:"hashBehind"` // commits since the hash was refreshed
	Note       Code   `json:"note,omitempty"`
	// Description is the record's own second line and ForgottenAt is zero
	// unless the record was forgotten, in which case it is the modified_at
	// of the write that forgot it (FORMAT.md §7.1, §18.2; APP.md §13).
	Description string `json:"description,omitempty"`
	ForgottenAt int64  `json:"forgottenAt"`
	// For open archives only.
	Files     int    `json:"files"`
	FreeSpace uint64 `json:"freeSpace"`
	State     string `json:"state,omitempty"` // open | compacting | needs_reopen
}

// VersionView is one key version of an archive record (FORMAT.md §7.2), for
// the details modal: a key, never a snapshot, and never the key itself.
type VersionView struct {
	KID       string `json:"kid"`
	CreatedAt int64  `json:"createdAt"`
	RetiredAt int64  `json:"retiredAt"`
	State     string `json:"state"` // current | retired
}

// ArchiveDetails is one registry record read whole (APP.md §13): hex strings
// for ids and hashes, Unix seconds for times, the policy as named booleans.
// It carries no wrapped_archive_key, no nonce and no offset, so §1's boundary
// holds.
type ArchiveDetails struct {
	ArchiveID             string `json:"archiveId"`
	Name                  string `json:"name"`
	Description           string `json:"description"`
	CreatedAt             int64  `json:"createdAt"`
	LastPath              string `json:"lastPath"`
	CurrentKID            string `json:"currentKid"`
	Revision              uint64 `json:"revision"`
	LastWriter            string `json:"lastWriter"`
	LastSeq               uint64 `json:"lastSeq"`
	HashAtSeq             uint64 `json:"hashAtSeq"`
	LastCiphertextHash    string `json:"lastCiphertextHash"`
	LastStoredSize        uint64 `json:"lastStoredSize"`
	LastWrittenAt         int64  `json:"lastWrittenAt"`
	ForgottenAt           int64  `json:"forgottenAt"`
	AlwaysRequireFullAuth bool   `json:"alwaysRequireFullAuth"`
	Hidden                bool   `json:"hidden"`
	// Method is the compression the archive was created with — store |
	// fastest | normal | better | best (FORMAT.md §7.1 bits 2–5).
	Method   string        `json:"method"`
	Versions []VersionView `json:"versions"`
}

// Difference is one field an incoming record holds differently: the local
// value is kept and the row says what the other vault had (APP.md §13).
type Difference struct {
	Field  string `json:"field"`  // name | description | policy
	Theirs string `json:"theirs"` // rendered for display only
}

// IncomingRecord is one row of a merge's checklist (APP.md §13). Action is
// what ticking it would do; Ticked is the default.
type IncomingRecord struct {
	ArchiveID   string       `json:"archiveId"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	CreatedAt   int64        `json:"createdAt"`
	Versions    int          `json:"versions"` // key versions the incoming record holds
	Action      string       `json:"action"`   // skip | version | add | forgotten
	ForgottenAt int64        `json:"forgottenAt"`
	Differs     []Difference `json:"differs"`
	Ticked      bool         `json:"ticked"`
}

// ArchivesChanged is the payload of EventArchivesChanged: the names the
// purge dropped, empty on every other change (APP.md §13).
type ArchivesChanged struct {
	Purged []string `json:"purged"`
}

// ArchiveStat is the open archive's status strip. There is no Dirty, no
// CapAt and no SessionAlive since 2026-09-09: every operation is its own
// transaction, so the archive is clean between them (APP.md §2.3).
type ArchiveStat struct {
	Seq   uint64 `json:"seq"`
	ID    string `json:"id"`
	Name  string `json:"name"`
	Size  uint64 `json:"size"`
	Files int    `json:"files"`
	// Records counts live files and directories together, which is what
	// Extract all is greyed on: the file count alone cannot say whether the
	// tree holds anything (APP.md §3).
	Records      int    `json:"records"`
	FreeSpace    uint64 `json:"freeSpace"`
	KeyVersion   int    `json:"keyVersion"`
	LastSavedAt  int64  `json:"lastSavedAt"`
	State        string `json:"state"`
	ExpiresAt    int64  `json:"expiresAt"` // the archive's own idle deadline
	ReceiptOwed  bool   `json:"receiptOwed"`
	CopyMismatch bool   `json:"copyMismatch"` // the file is not the copy the vault last saw
}

// FileRow is one row of a page: one committed record, file or directory
// alike (APP.md §3). ID and ParentID are the record's own ids — 32 lowercase
// hex digits, the all-zero id being the root — Name is the record's own name
// and Path the joined one (FORMAT.md R20, R39). A directory's Size is the
// sum beneath it and its ModifiedAt the record's own. A row is committed or
// it is not listed: there is no pending word since 2026-09-09.
type FileRow struct {
	ID           string `json:"id"`
	ParentID     string `json:"parentId"`
	IsDir        bool   `json:"isDir"`
	Name         string `json:"name"`
	Path         string `json:"path"`
	Size         uint64 `json:"size"`
	Storage      string `json:"storage"` // raw | zstd | zstd+dict
	SavedPercent int    `json:"savedPercent"`
	ModifiedAt   int64  `json:"modifiedAt"`
}

// Crumb is one step of the breadcrumb: a directory's id and its name, the
// root's being the archive's own name (APP.md §3).
type Crumb struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Page is one screenful of one directory. Total counts that directory's
// children, not its subtree, and Crumbs is the chain from the root down to
// the folder shown, inclusive and never empty — the page draws the whole
// breadcrumb from it and takes no name from Stat.
type Page struct {
	Seq    uint64    `json:"seq"`
	Rows   []FileRow `json:"rows"`
	Total  int       `json:"total"`
	Crumbs []Crumb   `json:"crumbs"`
}

// Collision is what CheckNames reports: the kind on both sides — what is
// being offered and what is in the way — so the dialog can say "Photos is a
// file here" and grey Replace whenever the two differ (APP.md §3).
type Collision struct {
	Name          string `json:"name"`  // the name as it was offered
	IsDir         bool   `json:"isDir"` // the kind offered: a name given with a trailing "/"
	Existing      string `json:"existing"`
	ExistingIsDir bool   `json:"existingIsDir"`
}

// FileOutcome is one record's result inside a batch operation. IsDir tells a
// directory's outcome from a file's: created (a directory record made) and
// entered (an existing one descended into) are a directory's, added and
// replaced a file's (APP.md §3).
type FileOutcome struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	IsDir   bool   `json:"isDir"`
	Outcome string `json:"outcome"` // added | replaced | created | entered | skipped | extracted | failed
	Code    Code   `json:"code,omitempty"`
}

// OpView is a running or finished long operation. Kind is add | replace |
// extract | compact | reclaim | verify | rotate; reclaim is the compaction
// the core runs itself after a commit that leaves the free space over
// APP.md §2.3's thresholds, and the strip shows it as Reclaiming space.
type OpView struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	ArchiveID string `json:"archiveId,omitempty"`
	Done      uint64 `json:"done"`
	Total     uint64 `json:"total"`
	// Items is what the operation plans: the files an add, a replace or an
	// extract will write, known once the plan is made and zero before that
	// and for every operation without a count. It is what lets the strip say
	// "Adding 3 files" rather than a phase word — and "Adding 1 file", since
	// a count of one is singular wherever the page counts (APP.md §3).
	Items     int           `json:"items"`
	Phase     string        `json:"phase"`
	StartedAt int64         `json:"startedAt"`
	Finished  bool          `json:"finished"`
	Error     Code          `json:"error,omitempty"`
	Results   []FileOutcome `json:"results,omitempty"`
}

// SlotBrief names one recovery slot of an incoming file, so that a dialog
// can say which key it will want before the button is pressed (APP.md §13).
type SlotBrief struct {
	RecipientID string `json:"recipientId"`
	Label       string `json:"label"`
	CreatedAt   int64  `json:"createdAt"`
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
	// RecoverySlots names the file's recovery slots and Generation is its
	// plaintext vmk_generation (FORMAT.md §5): both inform a dialog and
	// decide nothing (APP.md §13, FORMAT.md §18.2).
	RecoverySlots []SlotBrief `json:"recoverySlots"`
	Generation    uint64      `json:"generation"`
}

const (
	FileKindVault  = "vault"
	FileKindBackup = "backup"
)

// Settings is the machine-local settings file plus the registry's timeouts.
type Settings struct {
	VaultPath         string `json:"vaultPath"`
	DisplayName       string `json:"displayName"`
	CloseToTray       string `json:"closeToTray"` // destroy | hide
	Theme             string `json:"theme"`       // system | light | dark
	Look              string `json:"look"`        // native
	RecoveryRecordPct int    `json:"recoveryRecordPct"`
	DictionaryBelow   int64  `json:"dictionaryBelow"`
	// LastArchiveFolder is where the last archive was created, for the New
	// archive dialog (APP.md §6), and LastExtractFolder is where the last
	// extraction went, for the extract dialog's destination (§3). Both are
	// read-only: Set ignores them, since only a create and an extract write
	// them.
	LastArchiveFolder  string `json:"lastArchiveFolder"`
	LastExtractFolder  string `json:"lastExtractFolder"`
	IdleMinutes        int    `json:"idleMinutes"`     // from the registry; 0 = default
	AbsoluteMinutes    int    `json:"absoluteMinutes"` // from the registry; 0 = default
	TimeoutsFromVault  bool   `json:"timeoutsFromVault"`
	TimeoutsAdjustable bool   `json:"timeoutsAdjustable"` // only while unlocked
}

// Events the core emits. There is no archive.expiring since 2026-09-09: an
// archive is clean between operations, so its idle clock simply closes it
// (APP.md §2.3, DESIGN.md §10).
const (
	EventVaultState      = "vault.state"
	EventVaultCeremony   = "vault.ceremony"
	EventVaultWarning    = "vault.warning"
	EventArchivesChanged = "archives.changed"
	EventArchiveChanged  = "archive.changed"
	EventOpProgress      = "op.progress"
	EventOpDone          = "op.done"
)

// ArchiveChanged is the payload of EventArchiveChanged.
type ArchiveChanged struct {
	ID  string `json:"id"`
	Seq uint64 `json:"seq"`
}

// Warning is the payload of EventVaultWarning.
type Warning struct {
	Code Code `json:"code"`
}

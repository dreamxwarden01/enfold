package app

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/dreamxwarden01/enfold/internal/archive"
	"github.com/dreamxwarden01/enfold/internal/format"
	"github.com/dreamxwarden01/enfold/internal/kdf"
	"github.com/dreamxwarden01/enfold/internal/keystore"
)

// Code is what the frontend sees of an error: a stable identifier it maps
// to copy, and nothing else. The original error — with its offsets,
// generations and file names — goes to the core's log only (APP.md §3).
type Code string

const (
	CodeInternal Code = "internal"

	// Vault and session.
	CodeNoVault       Code = "vault.none"
	CodeVaultLocked   Code = "vault.locked"
	CodeVaultBroken   Code = "vault.broken"
	CodeVaultBusy     Code = "vault.busy"
	CodeVaultTampered Code = "vault.tampered"
	// The two causes, for VaultStatus.TamperedReason and the park a
	// mismatch leaves: the slot region does not match the registry (R25),
	// or it does not belong with the superblock (FORMAT.md §6.2).
	CodeTamperedHash       Code = "vault.tampered_hash"
	CodeTamperedGeneration Code = "vault.tampered_generation"
	CodeVaultStale         Code = "vault.stale"
	CodeVaultNotFound      Code = "vault.not_found"
	CodeVaultInvalid       Code = "vault.invalid"
	CodeVaultExists        Code = "vault.exists"        // a vault is already kept; importing needs replace
	CodeSetupNeeded        Code = "vault.setup_needed"  // the vault has only a recovery slot: finish setup
	CodeArchivesOpen       Code = "vault.archives_open" // close the open archives before replacing the vault
	CodeVaultUnlocked      Code = "vault.unlocked"      // lock the vault first
	CodeSettingsUnsaved    Code = "settings.unsaved"    // warning: the vault's place could not be recorded
	CodeNeedsUnlock        Code = "vault.needs_unlock"
	CodeAuth               Code = "vault.auth"
	CodeNoSlot             Code = "vault.no_slot"
	CodePasswordNeeded     Code = "vault.password_required"
	CodePasswordShort      Code = "vault.password_short" // a chosen password under the minimum
	CodeInvariant          Code = "vault.invariant"
	CodeSlotPolicy         Code = "vault.slot_policy"
	CodeDuplicateSlot      Code = "vault.duplicate_slot"
	CodeNoRecoverySlot     Code = "vault.no_recovery_slot"
	CodeSlotNotFound       Code = "vault.slot_not_found"
	CodeEscrowMissing      Code = "vault.escrow_missing"  // the vault keeps no copy of this recovery key: add a new one, then remove this
	CodeEscrowMismatch     Code = "vault.escrow_mismatch" // the kept recovery key does not open its slot: never shown
	CodeRecoveryPlace      Code = "vault.recovery_place"  // a recovery key is not saved into the data folder, the vault's folder or under a staging name
	CodeVaultKept          Code = "vault.kept"            // a vault is kept: a second one is never made; only a damaged one is rebuilt
	// CodeNotThisVault: no VMK of this vault and no key given opened the
	// incoming registry — another vault, or a backup from a generation this
	// vault no longer keeps. Never corruption (FORMAT.md §18.2, APP.md §13).
	CodeNotThisVault    Code = "vault.not_this_vault"
	CodeConflict        Code = "vault.conflict"
	CodeIndeterminate   Code = "vault.indeterminate"
	CodeCeremonyRunning Code = "ceremony.in_progress"
	CodeReleasing       Code = "ceremony.releasing"
	CodeNoCeremony      Code = "ceremony.none"
	CodeStalePrompt     Code = "ceremony.stale_prompt"
	CodeCancelled       Code = "ceremony.cancelled"

	// Token.
	CodeTokenNoService  Code = "token.no_service"
	CodeTokenNoReader   Code = "token.no_reader"
	CodeTokenNoCard     Code = "token.no_card"
	CodeTokenBusy       Code = "token.busy"
	CodeTokenNoPIV      Code = "token.no_piv"
	CodeTokenUnsupport  Code = "token.unsupported"
	CodeTokenNoKey      Code = "token.no_key"
	CodeTokenNotUsable  Code = "token.not_usable"
	CodeTokenPIN        Code = "token.pin"
	CodeTokenProof      Code = "token.proof" // the key's agreement does not match its public key: not enrolled
	CodeTokenPINBlocked Code = "token.pin_blocked"
	CodeTokenPINAgain   Code = "token.pin_required"
	CodeTokenTouch      Code = "token.touch"
	CodeTokenPending    Code = "token.pending" // a cancelled ceremony's key call is still answering: the file or the card is held until it does
	// CodePasswordDeadline: the five minutes from the touch ran out with
	// the vault's password not given, so the kept shared secret went and
	// the ceremony ended at the lock screen's first step (APP.md §2.2).
	// "The password was not given within five minutes; unlock again from
	// the key."
	CodePasswordDeadline Code = "token.password_deadline"
	CodeTokenTooMany     Code = "token.too_many_operations"
	CodeTokenReset       Code = "token.reset_failed"
	CodeTokenOccupied    Code = "token.slot_occupied"
	CodeTokenFull        Code = "token.no_empty_slot"
	CodeTokenNoMgmtKey   Code = "token.no_protected_management_key"
	CodeTokenMgmtKey     Code = "token.management_key"
	CodeTokenTwoKeys     Code = "token.two_keys"

	// Archives.
	CodeArchiveNotOpen      Code = "archive.not_open"
	CodeArchiveOpen         Code = "archive.already_open"
	CodeArchiveDirty        Code = "archive.dirty"
	CodeArchiveBusy         Code = "archive.busy"
	CodeArchiveCompacting   Code = "archive.compacting"
	CodeArchiveNeedsReopen  Code = "archive.needs_reopen"
	CodeArchiveKey          Code = "archive.key"
	CodeArchiveReadOnly     Code = "archive.read_only"
	CodeArchiveNotFound     Code = "archive.not_found"
	CodeArchiveInvalid      Code = "archive.invalid"
	CodeArchiveMissing      Code = "archive.file_missing"
	CodeArchiveCopyMismatch Code = "archive.copy_mismatch"
	// Forget, restore and delete (APP.md §13). A forgotten record still
	// holds its keys, so every other operation on it says so rather than
	// archive.not_found, which stays the answer for a record already purged.
	CodeArchiveForgotten Code = "archive.forgotten"
	// CodeArchiveNotThisOne: the parent folder was opened and the leaf was
	// not in it, or the envelope holds another archive_id. Nothing removed;
	// the page asks once more before forgetting.
	CodeArchiveNotThisOne Code = "archive.not_this_archive"
	// CodeArchiveUnreachable: the volume, share or folder is not there.
	// Nothing removed and nothing forgotten.
	CodeArchiveUnreachable Code = "archive.file_unreachable"
	// CodeArchiveDeleteFailed: the archive_id matched and the removal still
	// failed. The record is kept, since its keys open a file that is there.
	CodeArchiveDeleteFailed Code = "archive.delete_failed"
	// CodeDescriptionLong: over MaxDescriptionLen bytes, or not UTF-8
	// (FORMAT.md §7.1).
	CodeDescriptionLong Code = "archive.description_long"
	// CodeArchiveName: an empty name, one over 1 024 bytes or one that is
	// not UTF-8. Bounded app-side only: the confirmation for Forget and
	// Delete is the archive's name typed (APP.md §13, FORMAT.md §7.4).
	CodeArchiveName Code = "archive.name_invalid"
	// CodeArchiveExists: the path a create was given already holds a file.
	// The archive layer creates with O_EXCL and Enfold never overwrites a
	// file it did not make (APP.md §6, DESIGN.md trap 28). "A file is
	// already there. Enfold never overwrites; choose another name."
	CodeArchiveExists Code = "archive.exists"
	CodeFileExists    Code = "file.exists"
	CodeFileNotFound  Code = "file.not_found"
	CodeFileName      Code = "file.name"
	// The three per-item codes of the tree (APP.md §3, FORMAT.md R39).
	// CodeKindMismatch: the incoming item and the item in the way are of
	// different kinds, and kinds that differ never replace — replacing a
	// folder with a file would tombstone its subtree in one write, and
	// replacing a file with a folder is not an edit of that file.
	CodeKindMismatch Code = "file.kind_mismatch"
	// CodeMoveIntoSelf: a directory would be moved into itself or into one
	// of its own descendants.
	CodeMoveIntoSelf Code = "file.move_into_self"
	// CodeTreeBounds: the change would stand a directory more than 255
	// parents from the root, or join a record to a path over 4096 bytes.
	// Both bounds are the moved or created subtree's and not the named
	// record's, so they are caught in the pre-flight rather than at the
	// seal.
	CodeTreeBounds    Code = "file.tree_bounds"
	CodeSourceChanged Code = "file.source_changed"
	CodeContentHash   Code = "file.content_hash"
	CodeNoSpace       Code = "archive.no_space"
	CodeDictInUse     Code = "archive.dictionary_in_use"
	CodeOpNotFound    Code = "op.not_found"
	CodeOpCancelled   Code = "op.cancelled"
	CodeOpRunning     Code = "op.in_progress"
	CodeTooSlow       Code = "op.too_slow_for_session"

	CodeParams Code = "params"
	CodeIO     Code = "io"
)

// Error is the one error type a service returns. Error() is the code and
// nothing else, so no prose from a lower layer reaches the WebView
// (Wails ships Error() before any marshaller runs).
type Error struct {
	Code    Code   `json:"code"`
	Retries *int   `json:"retries,omitempty"` // token.pin: attempts left
	Slot    string `json:"slot,omitempty"`    // token.slot_occupied: the slot, as text
}

func (e *Error) Error() string { return string(e.Code) }

// Is lets errors.Is(err, &Error{Code: c}) match on the code alone.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

func coded(c Code) *Error { return &Error{Code: c} }

// classify turns any error of the lower layers into an *Error. It is the
// only path an error takes to a service's return value or an event.
func classify(err error) *Error {
	if err == nil {
		return nil
	}
	var ae *Error
	if errors.As(err, &ae) {
		return ae
	}
	var pe *TokenPINError
	if errors.As(err, &pe) {
		n := pe.Retries
		return &Error{Code: CodeTokenPIN, Retries: &n}
	}
	var oe *TokenOccupiedError
	if errors.As(err, &oe) {
		return &Error{Code: CodeTokenOccupied, Slot: oe.Slot.String()}
	}
	for _, m := range classifyTable {
		if errors.Is(err, m.err) {
			return coded(m.code)
		}
	}
	return coded(CodeInternal)
}

// classifyTable is checked in order: the more specific sentinel first
// where two can match the same chain.
var classifyTable = []struct {
	err  error
	code Code
}{
	// Token (app-side sentinels; the piv adapter wraps into these).
	{ErrTokenNoService, CodeTokenNoService},
	{ErrTokenNoReader, CodeTokenNoReader},
	{ErrTokenNoCard, CodeTokenNoCard},
	{ErrTokenReset, CodeTokenNoCard}, // a reset the flow could not heal: the key is as good as gone
	{ErrTokenBusy, CodeTokenBusy},
	{ErrTokenNoPIV, CodeTokenNoPIV},
	{ErrTokenUnsupported, CodeTokenUnsupport},
	{ErrTokenNoKey, CodeTokenNoKey},
	{ErrTokenNotUsable, CodeTokenNotUsable},
	{ErrTokenPINBlocked, CodeTokenPINBlocked},
	{ErrTokenPINRequired, CodeTokenPINAgain},
	{ErrTokenTouch, CodeTokenTouch},
	{ErrTokenTooMany, CodeTokenTooMany},
	{ErrTokenResetFailed, CodeTokenReset},
	{ErrTokenFull, CodeTokenFull},
	{ErrTokenNoProtectedKey, CodeTokenNoMgmtKey},
	{ErrTokenManagementKey, CodeTokenMgmtKey},
	{ErrTokenCancelled, CodeCancelled},

	// Keystore.
	{keystore.ErrIndeterminate, CodeIndeterminate},
	{keystore.ErrConflict, CodeConflict},
	{keystore.ErrBusy, CodeVaultBusy},
	{keystore.ErrTampered, CodeVaultTampered},
	{keystore.ErrStale, CodeVaultStale},
	{keystore.ErrPasswordRequired, CodePasswordNeeded},
	{keystore.ErrNoSlot, CodeNoSlot},
	{keystore.ErrVerifier, CodeAuth},
	{keystore.ErrAuth, CodeAuth},
	{keystore.ErrInvariant, CodeInvariant},
	{keystore.ErrPolicy, CodeSlotPolicy},
	{keystore.ErrDuplicate, CodeDuplicateSlot},
	{keystore.ErrNoRecoverySlot, CodeNoRecoverySlot},
	{keystore.ErrNotFound, CodeSlotNotFound},
	{keystore.ErrForeign, CodeNotThisVault},
	{keystore.ErrEscrowMissing, CodeEscrowMissing},
	{keystore.ErrEscrowMismatch, CodeEscrowMismatch},
	{keystore.ErrClosed, CodeVaultLocked},
	{keystore.ErrParams, CodeParams},

	// Archive.
	{archive.ErrIndeterminate, CodeIndeterminate},
	{archive.ErrKey, CodeArchiveKey},
	{archive.ErrReadOnly, CodeArchiveReadOnly},
	{archive.ErrBusy, CodeArchiveBusy},
	{archive.ErrTxOpen, CodeArchiveDirty},
	{archive.ErrExists, CodeFileExists},
	{archive.ErrNotFound, CodeFileNotFound},
	{archive.ErrMoveIntoSelf, CodeMoveIntoSelf},
	{archive.ErrTreeBounds, CodeTreeBounds},
	{archive.ErrKindMismatch, CodeKindMismatch},
	{archive.ErrSourceChanged, CodeSourceChanged},
	{archive.ErrContentHash, CodeContentHash},
	{archive.ErrDictInUse, CodeDictInUse},
	{archive.ErrNoSpace, CodeNoSpace},
	{archive.ErrClosed, CodeArchiveNotOpen},
	{archive.ErrParams, CodeParams},
	{archive.ErrInternal, CodeInternal},

	// Format and kdf.
	{format.ErrInvalid, CodeArchiveInvalid},
	{format.ErrTruncated, CodeArchiveInvalid},
	{kdf.ErrParams, CodeParams},

	// The file system, when a path the user gave is the problem. An
	// O_EXCL refusal reaches here only from a create (archive.ErrExists,
	// a file inside an archive, is matched above).
	{fs.ErrExist, CodeArchiveExists},
	{fs.ErrNotExist, CodeFileNotFound},
	{fs.ErrPermission, CodeIO},
}

// internalf logs the original and returns the internal code.
func (c *Core) internalf(op string, err error) *Error {
	c.log("%s: %v", op, err)
	return coded(CodeInternal)
}

// fail classifies and logs.
func (c *Core) fail(op string, err error) *Error {
	if err == nil {
		return nil
	}
	e := classify(err)
	if e.Code == CodeInternal {
		c.log("%s: %v", op, err)
	}
	return e
}

func paramsf(format string, a ...any) *Error {
	_ = fmt.Sprintf(format, a...) // the text stays core-side; only the code travels
	return coded(CodeParams)
}

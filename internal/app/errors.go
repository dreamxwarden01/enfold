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
	CodeNoVault         Code = "vault.none"
	CodeVaultLocked     Code = "vault.locked"
	CodeVaultBroken     Code = "vault.broken"
	CodeVaultBusy       Code = "vault.busy"
	CodeVaultTampered   Code = "vault.tampered"
	CodeVaultStale      Code = "vault.stale"
	CodeVaultNotFound   Code = "vault.not_found"
	CodeVaultInvalid    Code = "vault.invalid"
	CodeNeedsUnlock     Code = "vault.needs_unlock"
	CodeAuth            Code = "vault.auth"
	CodeNoSlot          Code = "vault.no_slot"
	CodePasswordNeeded  Code = "vault.password_required"
	CodeInvariant       Code = "vault.invariant"
	CodeSlotPolicy      Code = "vault.slot_policy"
	CodeDuplicateSlot   Code = "vault.duplicate_slot"
	CodeNoRecoverySlot  Code = "vault.no_recovery_slot"
	CodeSlotNotFound    Code = "vault.slot_not_found"
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
	CodeTokenPINBlocked Code = "token.pin_blocked"
	CodeTokenPINAgain   Code = "token.pin_required"
	CodeTokenTouch      Code = "token.touch"
	CodeTokenTooMany    Code = "token.too_many_operations"
	CodeTokenReset      Code = "token.reset_failed"
	CodeTokenOccupied   Code = "token.slot_occupied"
	CodeTokenFull       Code = "token.no_empty_slot"
	CodeTokenNoMgmtKey  Code = "token.no_protected_management_key"
	CodeTokenMgmtKey    Code = "token.management_key"
	CodeTokenTwoKeys    Code = "token.two_keys"

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
	CodeFileExists          Code = "file.exists"
	CodeFileNotFound        Code = "file.not_found"
	CodeFileName            Code = "file.name"
	CodeSourceChanged       Code = "file.source_changed"
	CodeContentHash         Code = "file.content_hash"
	CodeNoSpace             Code = "archive.no_space"
	CodeDictInUse           Code = "archive.dictionary_in_use"
	CodeOpNotFound          Code = "op.not_found"
	CodeOpCancelled         Code = "op.cancelled"
	CodeOpRunning           Code = "op.in_progress"
	CodeTooSlow             Code = "op.too_slow_for_session"

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

	// The file system, when a path the user gave is the problem.
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

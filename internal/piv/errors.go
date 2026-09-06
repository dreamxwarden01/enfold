//go:build windows

package piv

import (
	"errors"
	"fmt"
)

var (
	// ErrNoService: the Windows Smart Card service (SCardSvr) is not running,
	// so no reader can be reached.
	ErrNoService = errors.New("piv: smart card service is not running")
	// ErrNoReader: no reader with a YubiKey is attached, or the named reader
	// is gone.
	ErrNoReader = errors.New("piv: no YubiKey reader")
	// ErrNoCard: the reader has no card, or the card was removed, reset or
	// powered down under an operation.
	ErrNoCard = errors.New("piv: no card, or the card went away")
	// ErrBusy: another program holds the card. It is opened exclusively, and
	// so do most others.
	ErrBusy = errors.New("piv: card is in use by another program")
	// ErrInUse: this Card is in the middle of another operation on another
	// goroutine. Operations are refused rather than queued, so that a
	// second touch prompt never appears out of nowhere.
	ErrInUse = errors.New("piv: an operation is in progress on this card")
	// ErrNoPIVApplet: the reader's card has no PIV application — a YubiKey
	// with PIV disabled over USB, or not a YubiKey at all.
	ErrNoPIVApplet = errors.New("piv: no PIV application on the card")
	// ErrUnsupported: the token cannot do what this package needs — firmware
	// older than 5.3 (no GET METADATA), or an answer the package does not
	// understand.
	ErrUnsupported = errors.New("piv: token not supported")
	// ErrClosed: the Card is closed; a Token made from it is finished too.
	ErrClosed = errors.New("piv: closed")
	// ErrForbiddenSlot: the slot is outside the allowlist (9d and the retired
	// slots 82–95). Nothing in this package addresses any other key slot.
	ErrForbiddenSlot = errors.New("piv: slot is not one this program may use")
	// ErrEmpty: the slot holds neither a key nor a certificate.
	ErrEmpty = errors.New("piv: slot is empty")
	// ErrOccupied: the slot holds a key or a certificate and Overwrite was
	// not given. The error is an *OccupiedError carrying what is there.
	ErrOccupied = errors.New("piv: slot is occupied")
	// ErrFull: 9d and every retired slot are occupied; nothing can be
	// generated without overwriting.
	ErrFull = errors.New("piv: no empty slot")
	// ErrNoKey: no allowlisted slot holds the public key.
	ErrNoKey = errors.New("piv: token does not hold this key")
	// ErrNotUsable: the key exists but the keystore may not use it. The
	// message says why (KeyInfo.WhyNotUsable).
	ErrNotUsable = errors.New("piv: key is not usable for a hardware slot")
	// ErrNoProtectedKey: the token stores no PIN-protected management key.
	ErrNoProtectedKey = errors.New("piv: no PIN-protected management key on the token")
	// ErrManagementKey: the management key was refused.
	ErrManagementKey = errors.New("piv: management key refused")
	// ErrPINBlocked: the PIN is blocked; only the PUK can unblock it, and
	// this program never touches the PUK.
	ErrPINBlocked = errors.New("piv: PIN is blocked")
	// ErrPINRequired: the token refused a key operation for want of a PIN
	// although one had just been accepted — a PIN policy the card enforces
	// differently from what its metadata said. The operation can be retried.
	ErrPINRequired = errors.New("piv: token wants the PIN verified again")
	// ErrTouch: the token refused the operation: not touched in time. The
	// same status word also covers an unsatisfied PIN policy; the package
	// asks the card afterwards and reports ErrPINRequired when that is the
	// case, so this one means the touch.
	ErrTouch = errors.New("piv: token was not touched in time")
	// ErrCancelled: the prompter declined to provide the PIN.
	ErrCancelled = errors.New("piv: cancelled")
	// ErrTooManyOperations: a Token has done MaxOperations ECDH operations. A
	// fresh Token — a fresh user action — is needed for more; no caller can
	// drive an unbounded run of touch prompts through one.
	ErrTooManyOperations = errors.New("piv: operation limit reached for this token handle")
	// ErrAttestation: the slot's attestation does not verify against the
	// token's attestation certificate and Yubico's roots.
	ErrAttestation = errors.New("piv: attestation does not verify")
	// ErrResetFailed: the Card was closed but the card could not be reset and
	// is still PIN-verified, for up to 10 s (DESIGN.md §11 trap 14). The
	// caller tells the user; Card.ResetFailed keeps the fact.
	ErrResetFailed = errors.New("piv: card released but still PIN-verified")
	// ErrParams: an argument the package refuses.
	ErrParams = errors.New("piv: invalid parameters")
)

// PINError: the PIN was wrong. Retries is what remains before the PIN
// blocks; the prompter shows it before the next attempt. The package never
// retries on its own.
type PINError struct {
	Retries int
}

func (e *PINError) Error() string {
	if e.Retries == 1 {
		return "piv: wrong PIN (1 retry left)"
	}
	return fmt.Sprintf("piv: wrong PIN (%d retries left)", e.Retries)
}

// OccupiedError wraps ErrOccupied with what the slot holds, so a caller can
// offer to reuse the key rather than overwrite it.
type OccupiedError struct {
	Key KeyInfo
}

func (e *OccupiedError) Error() string {
	if e.Key.Algorithm == AlgorithmUnknown {
		return fmt.Sprintf("piv: slot %s holds a certificate without a key", e.Key.Slot)
	}
	return fmt.Sprintf("piv: slot %s already holds a %s key", e.Key.Slot, e.Key.Algorithm)
}

func (e *OccupiedError) Unwrap() error { return ErrOccupied }

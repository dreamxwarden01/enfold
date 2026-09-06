package keystore

import (
	"errors"
	"fmt"

	"github.com/dreamxwarden01/enfold/internal/format"
)

var (
	// ErrNoSlot: no active slot fits the credential — no password slot, no
	// recovery slot, or no hardware slot whose stored key is the token's.
	ErrNoSlot = errors.New("keystore: no slot matches this credential")
	// ErrVerifier: the credential does not produce this slot's key (§6.3). For
	// a password slot that is usually a wrong password; for a recovery slot,
	// wrong digits. Distinct from ErrAuth so that a wrong password is never
	// reported as corruption. The converse does not hold: a file whose
	// slot_pubkey was substituted makes the right credential fail this way
	// too, so a credential the user is sure of failing repeatedly is itself a
	// tamper signal for the UI to surface, with the export as the check.
	ErrVerifier = errors.New("keystore: credential does not produce this slot's key")
	// ErrAuth: the slot's key was derived but the wrapped VMK did not open. A
	// hardware slot has no verifier for its entangled password, so for one
	// this means a wrong password or a damaged record; for a software slot,
	// whose verifier already passed, it means a damaged record.
	ErrAuth = errors.New("keystore: wrapped VMK failed to authenticate")
	// ErrStale: the slot opened but holds a VMK from before a rotation (§6.2,
	// §8). Unlock another way first; then RewrapStale brings it up to date.
	ErrStale = errors.New("keystore: this credential is behind a rotation")
	// ErrTampered: the live slot region does not match the hash the registry
	// authenticates (R25). The vault opened through the slot that was used,
	// and stays readable; no rotation, re-wrap or slot mutation proceeds.
	ErrTampered = errors.New("keystore: slot region does not match the registry")
	// ErrInvariant: the mutation would leave fewer than two active slots with
	// disjoint required-secret sets (§6.4).
	ErrInvariant = errors.New("keystore: two independent ways in are required")
	// ErrPolicy: a standalone password slot may not coexist with a hardware
	// slot (DESIGN.md §5).
	ErrPolicy = errors.New("keystore: a standalone password slot cannot coexist with a hardware slot")
	// ErrPasswordRequired: the hardware slot has an entangled password and
	// none was given.
	ErrPasswordRequired = errors.New("keystore: this slot requires its entangled password")
	// ErrNoRecoverySlot: an export needs a recovery slot to carry.
	ErrNoRecoverySlot = errors.New("keystore: no recovery slot to export")
	// ErrBusy: another process holds the keystore file open; this program
	// opens it exclusively.
	ErrBusy = errors.New("keystore: already open in another process")
	// ErrConflict: the file on disk moved on since this handle read it —
	// another writer committed — so this handle's view is stale. Nothing was
	// written; reopen the file.
	ErrConflict = errors.New("keystore: file changed on disk since it was opened")
	// ErrClosed: the Keystore, Unlocked or Session has been closed or locked.
	ErrClosed = errors.New("keystore: closed")
	// ErrDuplicate: the token's key is already enrolled, or the new slot's
	// random recipient ID collides with an existing one.
	ErrDuplicate = errors.New("keystore: slot already exists")
	// ErrIndeterminate: a commit failed at or after its commit point, so the
	// file may hold either state. Reopen it (Keystore.Broken).
	ErrIndeterminate = errors.New("keystore: commit outcome unknown")
	// ErrNotFound: no slot has this recipient ID.
	ErrNotFound = errors.New("keystore: no such slot")
	// ErrParams: an argument the package refuses.
	ErrParams = errors.New("keystore: invalid parameters")
)

// corrupt wraps a structural failure so that errors.Is(err, format.ErrInvalid)
// answers "was this file well-formed" for the whole stack.
func corrupt(msg string, a ...any) error {
	return fmt.Errorf("%w: keystore: %s", format.ErrInvalid, fmt.Sprintf(msg, a...))
}

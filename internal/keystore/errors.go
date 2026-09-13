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
	// ErrStale: this handle was derived at a VMK generation the file has moved
	// past — its own Rotate, or another handle's — so its keys no longer wrap
	// anything in the file. It is about the liveness of an Unlocked or a
	// Session, never about a credential: since Revision 2 a rotation re-wraps
	// every active slot in the one flip (§8), so no slot can be behind. Derive
	// a fresh Unlocked and, from it, a fresh Session.
	ErrStale = errors.New("keystore: this handle is behind a rotation")
	// ErrTampered has two meanings, both statements about the slot region,
	// which is checksummed but not authenticated (§5).
	//
	// The live slot region does not match the hash the registry authenticates
	// (R25). The vault opened through the slot that was used and stays
	// readable; no rotation, re-wrap or slot mutation proceeds.
	//
	// Or a slot opened to a VMK generation that is not the superblock's, in
	// either direction: the region and the superblock do not belong together —
	// a spliced or rolled-back region (§6.2, §18.1). Since Revision 2 that is
	// a verdict, not a diagnosis: the unlock is refused, never reported as a
	// credential being behind. The same verdict covers a registry that
	// disagrees with the header it was written beside (§7.6) and a kept K_P
	// that is not the one the header derives.
	ErrTampered = errors.New("keystore: slot region does not match the registry")
	// ErrInvariant: the mutation would leave fewer than two active slots with
	// disjoint required-secret sets (§6.4).
	ErrInvariant = errors.New("keystore: two independent ways in are required")
	// ErrPolicy: a standalone password slot may not coexist with a hardware
	// slot (DESIGN.md §5).
	ErrPolicy = errors.New("keystore: a standalone password slot cannot coexist with a hardware slot")
	// ErrPasswordRequired: the vault's entangled password was not given. It is
	// decided from the slot region header's entangle byte (§6) before any
	// token is touched, never from a slot record and never from the length of
	// what was handed in (R4, §18.1).
	ErrPasswordRequired = errors.New("keystore: this vault requires its entangled password")
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
	// ErrEscrowMissing: the active recovery slot has no recovery_escrow record
	// in the secrets section (§7.6, R38). Registry version 3 is the only one
	// read or written and every recovery slot gets its record from the commit
	// that creates it, so this is a bookkeeping fault, not a slot made before
	// escrow: there is nothing to repair — the key exists only on paper — and
	// the cure is replacing the recovery slot. It never refuses an unlock.
	ErrEscrowMissing = errors.New("keystore: the registry keeps no copy of this recovery key")
	// ErrForeign: another keystore file's registry opened under none of this
	// vault's VMKs — the current one or any the secrets section remembers
	// (§18.2). Not this vault, or from a generation this vault no longer
	// keeps; never reported as corruption of the file that was read.
	ErrForeign = errors.New("keystore: not this vault, or from a generation this vault no longer keeps")
	// ErrEscrowMismatch: the escrow record opened, but the key it holds does
	// not derive the slot's public key (§6.3): it is not this slot's key,
	// and is never shown.
	ErrEscrowMismatch = errors.New("keystore: the kept recovery key does not match its slot")
	// ErrParams: an argument the package refuses.
	ErrParams = errors.New("keystore: invalid parameters")
	// ErrSeqExhausted: the superblock's seq is at 2^64 − 1 and the commit
	// would have to wrap it to 0, which would make the new state lose to the
	// old one. The writer refuses before anything is written (§4); the file
	// is untouched and the handle stays usable for everything but a commit.
	ErrSeqExhausted = errors.New("keystore: the superblock sequence is exhausted")
)

// corrupt wraps a structural failure so that errors.Is(err, format.ErrInvalid)
// answers "was this file well-formed" for the whole stack.
func corrupt(msg string, a ...any) error {
	return fmt.Errorf("%w: keystore: %s", format.ErrInvalid, fmt.Sprintf(msg, a...))
}

// Package keystore is the keystore file of docs/FORMAT.md Part I: the slots
// that wrap the vault master key, the encrypted registry of archives and
// their keys, and the two-copy superblock that commits every change in one
// flip.
//
// The shape follows the session model of DESIGN.md §10. Open reads a file and
// leaves it locked. Unlock takes one credential — a standalone password, the
// recovery key, or a token together with its optional entangled password —
// finds the slot it fits, derives that slot's key, unwraps the VMK, decrypts
// the registry, and verifies the slot region against the registry's
// authenticated hash (R25). The result, an Unlocked, holds the VMK for as
// long as the caller needs it for slot mutations, rotation or export, and no
// longer: Session derives the three cached keys (KWK, Metadata, DB), and
// Close destroys the VMK.
//
// Every mutation lands in one superblock flip (§4): the new slot region is
// written to the inactive copy, the new registry to a location that does not
// overlap the live one, both are synced, and only then is the inactive
// superblock written with the next sequence number. A crash at any point
// leaves the previous state intact.
//
// The slot invariant (§6.4) is evaluated before every slot mutation as a
// predicate over the whole set: there must remain two active slots whose
// required-secret sets are disjoint. The file cannot tell two entangled
// passwords apart, so every entangled password counts as the same secret;
// two tokens each with its own password still need a recovery slot. A
// standalone password slot may not coexist with a hardware slot (DESIGN.md
// §5). Rotation (§8) re-wraps the VMK into every active slot from the stored
// public keys — which is why R25 must hold first — and marks the slots it
// cannot reach as stale rather than leaving them behind silently.
//
// An export (§15, R28) is a keystore file of the same format whose slot
// region holds only the recovery slots: the recovery key opens it like any
// keystore, which is also how it is verified.
//
// The registry also keeps every recovery key once more (R38): wrapped under
// KWK_recovery, a key derived from the VMK, in a record written and removed
// with the slot and re-wrapped by rotation. Only an Unlocked — which holds
// the VMK — opens it (RecoveryKey), so that a human can be shown the key
// again; a Session cannot.
//
// Retired slots (slot_state 2) are read and preserved but never opened,
// counted or re-wrapped: nothing in v1 produces one.
package keystore

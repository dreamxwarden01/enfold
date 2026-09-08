// Package keystore is the keystore file of docs/FORMAT.md Part I: the slots
// that wrap the vault master key, the encrypted registry of archives and
// their keys, and the two-copy superblock that commits every change in one
// flip.
//
// The shape follows the session model of DESIGN.md §10. Open reads a file and
// leaves it locked. Unlock takes one credential — a standalone password, the
// recovery key, or a token together with the vault's entangled password when
// the vault has one — finds the slot it fits, derives that slot's key, unwraps
// the VMK, decrypts the registry, and verifies the slot region against the
// registry's authenticated hash (R25). The result, an Unlocked, holds the VMK
// for as long as the caller needs it for slot mutations, rotation or export,
// and no longer: Session derives the three cached keys (KWK, Metadata, DB),
// and Close destroys the VMK.
//
// Every mutation lands in one superblock flip (§4): the new slot region is
// written to the inactive copy, the new registry to a location that does not
// overlap the live one, both are synced, and only then is the inactive
// superblock written with the next sequence number. A crash at any point
// leaves the previous state intact.
//
// The entangled password is the vault's, not a slot's (§18.1). The slot
// region header (§6) carries the switch, the Argon2id parameters and the
// entangle_salt for the whole vault; the password yields
// K_P = Argon2id(P, vault_salt') alone, and every hardware slot's pre mixes
// the token in after the KDF. K_P is kept under KWK_secrets in the registry's
// secrets section (§7.6), so an Unlocked holds it whatever credential opened
// the vault, and enrolling a key, changing the password, turning it on or off
// and rotating the VMK are all offline: no token present, no password typed.
// At a hardware unlock the kept copy is authoritative and the derived one is
// compared with it in constant time; a mismatch is a statement about the
// header and is ErrTampered.
//
// Turning the vault's password on, changing it and turning it off are one
// commit each (SetEntangled, ChangeEntangledPassword): a fresh entangle_salt,
// a new K_P, every active hardware slot re-wrapped from its stored slot_pubkey,
// and the entangled_key record written or dropped, all in the same superblock
// flip as the header — so the header and the registry can never disagree on
// disk. The old password is never asked and cannot be: nothing in either call
// takes one. An adopted export takes its entanglement at its first way in
// (AddFirstWayIn), which lands the slot and the header together (§15, R28).
//
// The slot invariant (§6.4) is evaluated before every slot mutation — and
// before any change to the header's entangle byte — as a predicate over the
// whole set: there must remain two active slots whose required-secret sets are
// disjoint. While the vault is entangled every active hardware slot needs the
// vault password as well as its token, one and the same secret for all of
// them; slots of type 2 and 3 are never entangled. A standalone password slot
// may not coexist with a hardware slot (DESIGN.md §5).
//
// Rotation (§8) is one flip and is never deferred: step 3 re-encrypts every
// secrets record the commit keeps under the new KWK_secrets and appends the
// retiring VMK as a vmk_history record, and step 4 re-wraps the new VMK into
// every active slot from its stored public keys plus the K_P step 3 held. A
// rotation that commits is complete — there is no stale slot and no partial
// rotation — and one that cannot re-encrypt a kept record is abandoned before
// the flip.
//
// An export (§15, R28) is a keystore file of the same format whose slot region
// holds only the active recovery slots: the recovery key opens it like any
// keystore, which is also how it is verified. Its header is written with
// entangle 0, and of the secrets section it carries the recovery_escrow
// records of the slots it holds and every vmk_history record, never the
// entangled_key.
//
// The registry keeps every recovery key once more (R38): under KWK_secrets,
// in a record written and removed with its slot and re-encrypted by every
// rotation. Only an Unlocked — which holds the VMK — opens it (RecoveryKey),
// so that a human can be shown the key again; a Session cannot.
//
// A rotation also remembers the VMK it retires, under KWK_secrets, keyed by
// the generation that VMK held (§7.6, §18.2). So the vault knows every VMK it
// ever had, and OpenForeign reads a backup of itself from any earlier
// generation with no key from the user: the current VMK, then each retired one
// in descending generation, each attempted at most once, the AEAD deciding.
// Nothing plaintext in the other file short-circuits that trial — not its
// vault_id, not its vmk_generation — and a file no VMK opens is ErrForeign,
// never corruption. What comes back has every archive key re-wrapped under this
// vault's KWK, so no bare key leaves the package.
//
// Retired slots (slot_state 2) are read and preserved but never opened,
// counted or re-wrapped: nothing in v1 produces one.
package keystore

// Package format implements the byte-level encoding of Enfold's two file types
// as specified in docs/FORMAT.md: the keystore (superblock, slot region,
// registry plaintext) and the archive (envelope, superblock, file index
// plaintext, free-space map), plus the AAD and nonce constructions that the
// crypto layer feeds to AES-256-GCM.
//
// The package does no encryption. It turns structs into bytes and bytes into
// structs, and it is strict in both directions: every integer is little-endian,
// strings are UTF-8 with a u16 length prefix, reserved fields are written as
// zero and ignored on read, and anything unknown — a slot type, a key source,
// an algorithm identifier, a flag bit — makes decoding fail closed rather than
// skip. Decoders never panic on hostile input; that property is the subject of
// the fuzz targets in fuzz_test.go.
//
// Choices this package pins where FORMAT.md left room (each is mirrored back
// into FORMAT.md §3.4):
//
//   - The AAD for wrapped_vmk is every byte of the slot record from slot_state
//     through wrap_nonce inclusive, followed by vault_id, with no exception
//     since Revision 2. record_len and wrapped_vmk are not part of it, and
//     neither is the slot region header.
//   - The slot region opens with a 32-byte header (§6) carrying the vault's
//     entangle switch, its Argon2id parameters and entangle_salt; the header
//     is validated before any record is decoded, so a hostile Argon2 header
//     never reaches a derivation.
//   - The registry is read and written as version 3 only, and its secrets
//     section (§7.6) is strictly ascending by (kind, id).
//   - Public keys carry a u16 length prefix, like strings and byte fields.
//   - The archive superblock has magic ENFOLDS\x01 and carries the index
//     (offset, ciphertext length, nonce, tag) and the free-space map (offset,
//     length, SHA-256). Both are relocatable extents.
//   - The index plaintext is: u32 index_version=2, u32-prefixed zstd dictionary,
//     u32 dir_count and the directory records, then u32 file_count and the file
//     records, each record prefixed by a u32 record_len. Version 1 — the
//     object-key model, a full path in every file record — is refused, not
//     migrated.
//   - The index is a tree (R39): a directory is a record with an id, every
//     record hangs off a parent by that id, and the root is implicit at the
//     all-zero id. Index.Validate walks it in the reader's order — identities,
//     chains to the root, files against the directories, sibling uniqueness
//     under simple case folding, then the joined path bound — and Encode runs
//     the same walk, so this package never writes an index it would refuse.
//   - The free-space map is plaintext, hashed in the superblock, and must be
//     sorted, non-overlapping and free of zero-length extents.
package format

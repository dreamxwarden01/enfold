package format

// AADs for the three 32-byte key wraps (docs/FORMAT.md R22). Each binds a
// wrapped key to the record that carries it, with an ASCII prefix for domain
// separation, so that a wrapped key moved between records inside an otherwise
// authenticated structure fails to open.

// ArchiveKeyAAD is the AAD for a version record's wrapped_archive_key (§7.2):
// "Enfold/v1/aad/archive-key" ‖ archive_id ‖ kid.
func ArchiveKeyAAD(archiveID, kid [16]byte) []byte {
	w := &writer{b: make([]byte, 0, 25+32)}
	w.fixed([]byte("Enfold/v1/aad/archive-key"))
	w.fixed(archiveID[:])
	w.fixed(kid[:])
	return w.b
}

// DEKAAD is the AAD for a file record's wrapped_dek (§11):
// "Enfold/v1/aad/dek" ‖ archive_id ‖ file_id ‖ u32 dek_epoch.
func DEKAAD(archiveID, fileID [16]byte, dekEpoch uint32) []byte {
	w := &writer{b: make([]byte, 0, 17+32+4)}
	w.fixed([]byte("Enfold/v1/aad/dek"))
	w.fixed(archiveID[:])
	w.fixed(fileID[:])
	w.u32(dekEpoch)
	return w.b
}

// IdentityKeyAAD is the AAD for the registry's wrapped_identity_key (§7):
// "Enfold/v1/aad/identity" ‖ vault_id ‖ device_id.
func IdentityKeyAAD(vaultID, deviceID [16]byte) []byte {
	w := &writer{b: make([]byte, 0, 22+32)}
	w.fixed([]byte("Enfold/v1/aad/identity"))
	w.fixed(vaultID[:])
	w.fixed(deviceID[:])
	return w.b
}

package format

import "encoding/binary"

// ChunkNonce builds the 12-byte STREAM nonce for one data chunk (docs/FORMAT.md
// §12): an 11-byte little-endian counter followed by one byte that is 0x01 for
// the final chunk and 0x00 otherwise. A uint64 counter never reaches the
// 11-byte limit, so no overflow check is needed.
func ChunkNonce(counter uint64, final bool) [NonceSize]byte {
	var n [NonceSize]byte
	binary.LittleEndian.PutUint64(n[:8], counter)
	if final {
		n[11] = 1
	}
	return n
}

// ChunkAAD is the associated data for every chunk of one file (§12):
// archive_id ‖ file_id ‖ alg_id ‖ chunk_size. 38 bytes.
func ChunkAAD(archiveID, fileID [16]byte, alg AlgID, chunkSize uint32) []byte {
	w := &writer{b: make([]byte, 0, 16+16+2+4)}
	w.fixed(archiveID[:])
	w.fixed(fileID[:])
	w.u16(uint16(alg))
	w.u32(chunkSize)
	return w.b
}

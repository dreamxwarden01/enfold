package stream

import (
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"fmt"

	"github.com/dreamxwarden01/enfold/internal/format"
)

const (
	// ChunkSize is the plaintext carried by every chunk but the last (§12).
	ChunkSize = format.ChunkSize
	// TagSize is the GCM tag appended to every chunk.
	TagSize = format.TagSize
	// SealedChunkSize is the on-disk footprint of a full chunk.
	SealedChunkSize = ChunkSize + TagSize
	// KeySize is the DEK length: an AES-256 key, the same 32 bytes as every
	// key in the hierarchy (kdf.KeySize; asserted equal in the tests).
	KeySize = 32

	// MaxChunks bounds the counter: a blob has at most 2^32 chunks, which is
	// both what R19's 2^48-byte file limit works out to and the NIST SP 800-38D
	// bound on invocations of AES-GCM under one key.
	MaxChunks = format.MaxOrigSize / ChunkSize
)

var (
	// ErrAuth is returned when a chunk does not authenticate under its expected
	// counter and final flag: tampering, reordering, a blob cut at a chunk
	// boundary, or the wrong DEK, archive or file.
	ErrAuth = errors.New("stream: authentication failed")
	// ErrClosed is returned by Write or Read after Close.
	ErrClosed = errors.New("stream: closed")
	// ErrTooLarge is returned by a Writer asked to exceed format.MaxOrigSize.
	ErrTooLarge = errors.New("stream: plaintext exceeds 2^48 bytes")
)

// chunkCount is the number of chunks in a blob of stored bytes: the ceiling of
// stored/SealedChunkSize. Every derived length — the plaintext size, the final
// chunk's length, the bounds the Reader slices within — comes from this one
// value, so it is computed in one place.
func chunkCount(stored uint64) uint64 {
	chunks := stored / SealedChunkSize
	if stored%SealedChunkSize != 0 {
		chunks++
	}
	return chunks
}

// PlaintextLen returns the plaintext length of a canonical blob of stored
// bytes, inverting format.RawStoredSize. It rejects, with an error wrapping
// format.ErrInvalid, any length that no canonical framing produces: fewer than
// 16 bytes, a final chunk shorter than tag plus one byte when it is not the
// only chunk, or more than MaxChunks chunks.
func PlaintextLen(stored uint64) (uint64, error) {
	if stored < TagSize {
		return 0, fmt.Errorf("%w: stream of %d bytes is shorter than one tag", format.ErrInvalid, stored)
	}
	chunks := chunkCount(stored)
	if chunks > MaxChunks {
		return 0, fmt.Errorf("%w: stream of %d bytes has more than %d chunks", format.ErrInvalid, stored, uint64(MaxChunks))
	}
	last := stored - (chunks-1)*SealedChunkSize // in [1, SealedChunkSize]
	if last < TagSize || (chunks > 1 && last == TagSize) {
		return 0, fmt.Errorf("%w: stream of %d bytes has a %d-byte final chunk", format.ErrInvalid, stored, last)
	}
	return stored - chunks*TagSize, nil
}

// chunker seals and opens single chunks under one DEK and one file's AAD.
type chunker struct {
	aead cipher.AEAD
	aad  []byte
}

func newChunker(dek [KeySize]byte, archiveID, fileID [16]byte) (*chunker, error) {
	defer clear(dek[:])
	block, err := aes.NewCipher(dek[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &chunker{
		aead: aead,
		aad:  format.ChunkAAD(archiveID, fileID, format.AlgAES256GCM, ChunkSize),
	}, nil
}

func (c *chunker) seal(dst []byte, counter uint64, final bool, plaintext []byte) []byte {
	nonce := format.ChunkNonce(counter, final)
	return c.aead.Seal(dst, nonce[:], plaintext, c.aad)
}

func (c *chunker) open(dst []byte, counter uint64, final bool, ciphertext []byte) ([]byte, error) {
	nonce := format.ChunkNonce(counter, final)
	out, err := c.aead.Open(dst, nonce[:], ciphertext, c.aad)
	if err != nil {
		return nil, fmt.Errorf("%w: chunk %d", ErrAuth, counter)
	}
	return out, nil
}

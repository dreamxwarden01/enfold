package format

import (
	"encoding/binary"
	"strings"
	"unicode/utf8"
)

// MaxOrigSize bounds a file's plaintext length (docs/FORMAT.md R19): 2^48
// bytes, 256 TiB, far past anything an archive holds and far short of where
// the chunk arithmetic could overflow.
const MaxOrigSize = 1 << 48

// MaxFileNameLen bounds a file record's name in bytes (R20).
const MaxFileNameLen = 4096

// MaxDictSize bounds the index's zstd dictionary (R27): 16 MiB, far above
// the ~110 KiB a trained dictionary is worth and small enough that every
// decoder holding a copy stays cheap.
const MaxDictSize = 16 << 20

// Index is the plaintext of the archive file index (§11): an optional trained
// zstd dictionary followed by one record per file, tombstones included.
type Index struct {
	Dict  []byte
	Files []FileRecord
}

// FileRecord is one file (§11). pack_id is on the wire (16 bytes, reserved,
// written as zero and ignored on read) but not in the struct.
type FileRecord struct {
	FileID       [16]byte
	State        FileState
	Name         string // see ValidateFileName
	OrigSize     uint64 // plaintext length, ≤ MaxOrigSize
	StoredSize   uint64 // bytes occupied in the data region, tags included
	Storage      Storage
	ContentHash  [32]byte // SHA-256 of the plaintext
	DataOff      uint64
	ChunkSize    uint32 // ChunkSize in v1
	Alg          AlgID
	DEKNonce     [NonceSize]byte
	WrappedDEK   [WrappedKeySize]byte // under the archive wrap key
	DEKEpoch     uint32
	DEKCreatedAt int64
	Revision     uint64
	LastWriter   [16]byte
	ModifiedAt   int64
}

const (
	indexVersion  = 1
	minFileRecord = 4 + 16 + 1 + 2 + 8 + 8 + 1 + 32 + 8 + 4 + 2 + NonceSize + WrappedKeySize + 4 + 8 + 16 + 8 + 16 + 8
)

// RawStoredSize is the data-region footprint of a file of n plaintext bytes
// stored raw under AES-256-GCM STREAM: every chunk carries a tag, and a file
// always has at least one (possibly empty) final chunk. Defined for
// n ≤ MaxOrigSize; larger inputs return 0, which no valid record can carry.
func RawStoredSize(n uint64) uint64 {
	if n > MaxOrigSize {
		return 0
	}
	chunks := n / ChunkSize
	if n%ChunkSize != 0 || n == 0 {
		chunks++
	}
	return n + chunks*TagSize
}

// ValidateFileName applies the rules of R20 to a live file's name: a relative
// path with '/' separators, no empty, '.' or '..' elements, no control or
// Windows-reserved characters, no reserved device names, no element ending in
// a space or a dot, at most MaxFileNameLen bytes.
func ValidateFileName(name string) error {
	if name == "" {
		return invalidf("file name is empty")
	}
	if len(name) > MaxFileNameLen {
		return invalidf("file name of %d bytes exceeds %d", len(name), MaxFileNameLen)
	}
	if !utf8.ValidString(name) {
		return invalidf("file name is not valid UTF-8")
	}
	for _, c := range name {
		if c < 0x20 || c == 0x7F || strings.ContainsRune(`\:*?"<>|`, c) {
			return invalidf("file name contains forbidden character %q", c)
		}
	}
	for _, el := range strings.Split(name, "/") {
		switch {
		case el == "":
			return invalidf("file name %q has an empty path element", name)
		case el == "." || el == "..":
			return invalidf("file name %q has a %q element", name, el)
		case el[len(el)-1] == ' ' || el[len(el)-1] == '.':
			return invalidf("file name %q has an element ending in a space or dot", name)
		case reservedDeviceName(el):
			return invalidf("file name %q uses a reserved device name", name)
		}
	}
	return nil
}

func reservedDeviceName(el string) bool {
	base := el
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	if len(base) != 3 && len(base) != 4 {
		return false
	}
	up := strings.ToUpper(base)
	switch up {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(up) == 4 && (up[:3] == "COM" || up[:3] == "LPT") && up[3] >= '1' && up[3] <= '9' {
		return true
	}
	return false
}

// validate checks one file record. hasDict says whether the index carries a
// dictionary, which StorageZstdDict requires.
func (f *FileRecord) validate(hasDict bool) error {
	switch f.State {
	case FileLive, FileTombstone:
	default:
		return invalidf("file %x state %d unknown", f.FileID, f.State)
	}
	if f.State == FileLive {
		if err := ValidateFileName(f.Name); err != nil {
			return err
		}
	} else if !utf8.ValidString(f.Name) {
		return invalidf("file %x name is not valid UTF-8", f.FileID)
	}
	switch f.Storage {
	case StorageRaw:
	case StorageZstd, StorageZstdDict:
		if f.State == FileLive && f.OrigSize == 0 {
			// R27: an empty file is stored raw; a zstd frame for it would
			// have nothing to bound its window by.
			return invalidf("file %x is empty but compressed", f.FileID)
		}
		if f.Storage == StorageZstdDict && !hasDict {
			return invalidf("file %x uses a dictionary the index does not carry", f.FileID)
		}
	default:
		return invalidf("file %x storage %d unknown", f.FileID, f.Storage)
	}
	switch f.Alg {
	case AlgAES256GCM:
	case AlgChaCha20Poly1305:
		return invalidf("file %x alg_id chacha20-poly1305 is reserved", f.FileID)
	default:
		return invalidf("file %x alg_id %d unknown", f.FileID, f.Alg)
	}
	if f.ChunkSize != ChunkSize {
		return invalidf("file %x chunk_size %d, want %d", f.FileID, f.ChunkSize, ChunkSize)
	}
	if f.OrigSize > MaxOrigSize {
		return invalidf("file %x orig_size %d exceeds %d", f.FileID, f.OrigSize, uint64(MaxOrigSize))
	}
	if f.State == FileLive {
		if f.DataOff < ArchiveDataStart {
			return invalidf("file %x data_off 0x%x is inside the fixed regions", f.FileID, f.DataOff)
		}
		if f.DataOff+f.StoredSize < f.DataOff {
			return invalidf("file %x extent overflows", f.FileID)
		}
		if f.StoredSize < TagSize {
			return invalidf("file %x stored_size %d is shorter than one tag", f.FileID, f.StoredSize)
		}
		if f.Storage == StorageRaw && f.StoredSize != RawStoredSize(f.OrigSize) {
			return invalidf("file %x stored_size %d does not match %d raw bytes", f.FileID, f.StoredSize, f.OrigSize)
		}
	}
	return nil
}

func (f *FileRecord) body() ([]byte, error) {
	w := &writer{b: make([]byte, 0, minFileRecord+len(f.Name))}
	w.fixed(f.FileID[:])
	w.u8(uint8(f.State))
	w.str(f.Name)
	w.u64(f.OrigSize)
	w.u64(f.StoredSize)
	w.u8(uint8(f.Storage))
	w.fixed(f.ContentHash[:])
	w.u64(f.DataOff)
	w.u32(f.ChunkSize)
	w.u16(uint16(f.Alg))
	w.fixed(f.DEKNonce[:])
	w.fixed(f.WrappedDEK[:])
	w.u32(f.DEKEpoch)
	w.i64(f.DEKCreatedAt)
	w.zeros(16) // pack_id: reserved, written as zero (§1)
	w.u64(f.Revision)
	w.fixed(f.LastWriter[:])
	w.i64(f.ModifiedAt)
	return w.done()
}

func decodeFileBody(r *reader) *FileRecord {
	f := &FileRecord{}
	r.fixed(f.FileID[:])
	f.State = FileState(r.u8())
	f.Name = r.str()
	f.OrigSize = r.u64()
	f.StoredSize = r.u64()
	f.Storage = Storage(r.u8())
	r.fixed(f.ContentHash[:])
	f.DataOff = r.u64()
	f.ChunkSize = r.u32()
	f.Alg = AlgID(r.u16())
	r.fixed(f.DEKNonce[:])
	r.fixed(f.WrappedDEK[:])
	f.DEKEpoch = r.u32()
	f.DEKCreatedAt = r.i64()
	r.skip(16) // pack_id: reserved, ignored on read (§1)
	f.Revision = r.u64()
	r.fixed(f.LastWriter[:])
	f.ModifiedAt = r.i64()
	return f
}

// Validate checks the whole index, including that file identities are unique.
func (x *Index) Validate() error {
	if len(x.Dict) > MaxDictSize {
		return invalidf("index dictionary of %d bytes exceeds %d", len(x.Dict), MaxDictSize)
	}
	if len(x.Dict) > 0 {
		// R27: a zstd dictionary in the reference format — the magic
		// 0xEC30A437 little-endian, then a non-zero little-endian u32 ID.
		if len(x.Dict) < 8 || string(x.Dict[:4]) != "\x37\xa4\x30\xec" {
			return invalidf("index dictionary is not a zstd dictionary")
		}
		if binary.LittleEndian.Uint32(x.Dict[4:8]) == 0 {
			return invalidf("index dictionary has ID 0")
		}
	}
	seen := make(map[[16]byte]struct{}, len(x.Files))
	for i := range x.Files {
		f := &x.Files[i]
		if err := f.validate(len(x.Dict) > 0); err != nil {
			return err
		}
		if _, dup := seen[f.FileID]; dup {
			return invalidf("file %x appears twice", f.FileID)
		}
		seen[f.FileID] = struct{}{}
	}
	return nil
}

// Encode returns the index plaintext: u32 index_version, u32-prefixed
// dictionary, u32 file_count, then u32-prefixed file records.
func (x *Index) Encode() ([]byte, error) {
	if err := x.Validate(); err != nil {
		return nil, err
	}
	w := &writer{b: make([]byte, 0, 4+4+len(x.Dict)+4+len(x.Files)*(minFileRecord+32))}
	w.u32(indexVersion)
	w.bytes32(x.Dict)
	w.u32(uint32(len(x.Files)))
	for i := range x.Files {
		body, err := x.Files[i].body()
		if err != nil {
			return nil, err
		}
		w.u32(uint32(len(body)))
		w.fixed(body)
	}
	return w.done()
}

// DecodeIndex parses an index plaintext.
func DecodeIndex(b []byte) (*Index, error) {
	if uint64(len(b)) > MaxIndexLen {
		return nil, invalidf("index of %d bytes exceeds %d", len(b), MaxIndexLen)
	}
	r := newReader(b, "index")
	if v := r.u32(); r.err == nil && v != indexVersion {
		return nil, invalidf("index_version %d unsupported", v)
	}
	x := &Index{}
	x.Dict = r.bytes32()
	n := r.count(r.u32(), minFileRecord)
	x.Files = make([]FileRecord, 0, n)
	for i := 0; i < n && r.err == nil; i++ {
		l := r.u32()
		if r.err != nil {
			break
		}
		if uint64(l) > uint64(r.remaining()) {
			r.truncatedf("record_len %d exceeds the %d bytes left", l, r.remaining())
			break
		}
		body := r.sub(int(l), "file record "+itoa(i))
		f := decodeFileBody(body)
		if err := body.done(); err != nil {
			return nil, err
		}
		x.Files = append(x.Files, *f)
	}
	if err := r.done(); err != nil {
		return nil, err
	}
	if err := x.Validate(); err != nil {
		return nil, err
	}
	return x, nil
}

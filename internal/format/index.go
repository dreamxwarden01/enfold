package format

import (
	"encoding/binary"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// MaxOrigSize bounds a file's plaintext length (docs/FORMAT.md R19): 2^48
// bytes, 256 TiB, far past anything an archive holds and far short of where
// the chunk arithmetic could overflow.
const MaxOrigSize = 1 << 48

// MaxNameUnits bounds one record's name (R20): 255 UTF-16 code units, which
// is what NTFS, ReFS and SMB count a path component in, so no name is refused
// for its length that the volume it came from could hold. 255 code units of
// valid UTF-8 is at most 765 bytes, which is what bounds the record on the
// wire.
const MaxNameUnits = 255

// MaxPathLen bounds the path a live record extracts to (R20, R39): its
// ancestors' names and its own, joined by '/', at most 4096 bytes. No name
// carries a path any more — the bound is on the join, not on the field.
const MaxPathLen = 4096

// MaxTreeDepth bounds how far a live directory stands below the root (R39):
// every live directory reaches the root by following parent_id in at most
// this many steps.
const MaxTreeDepth = 255

// MaxDictSize bounds the index's zstd dictionary (R27): 16 MiB, far above
// the ~110 KiB a trained dictionary is worth and small enough that every
// decoder holding a copy stays cheap.
const MaxDictSize = 16 << 20

// RootID is the archive's root directory (R39): the all-zero id, implicit and
// never a record of its own, the parent of every top-level file and folder.
var RootID [16]byte

// Index is the plaintext of the archive file index (§11): an optional trained
// zstd dictionary, then one record per directory, then one per file,
// tombstones included in both. The index is a tree, not a list of keys — a
// directory is a record with an id and a record hangs off its parent by that
// id, so nothing is ever derived from a path (R39, DESIGN.md trap 31).
type Index struct {
	Dict  []byte
	Dirs  []DirRecord
	Files []FileRecord
}

// DirRecord is one directory (§11). Deleting a folder leaves its record, as
// deleting a file does, and a tombstone keeps dir_id, parent_id and name and
// advances revision and last_writer only (R32).
type DirRecord struct {
	DirID    [16]byte
	State    FileState
	ParentID [16]byte // RootID is the root, which has no record
	Name     string   // one path element; see ValidateName
	// ModifiedAt is the folder's own time, not a change clock: the source
	// folder's time when it was added, the time of creation when it was made
	// in the app, and nothing writes it again (R32) — the opposite of a file
	// record's, which every mutation advances.
	ModifiedAt int64
	Revision   uint64
	LastWriter [16]byte
}

// FileRecord is one file (§11). pack_id is on the wire (16 bytes, reserved,
// written as zero and ignored on read) but not in the struct.
type FileRecord struct {
	FileID       [16]byte
	State        FileState
	ParentID     [16]byte // RootID is the root, which has no record
	Name         string   // one path element, never a path; see ValidateName
	OrigSize     uint64   // plaintext length, ≤ MaxOrigSize
	StoredSize   uint64   // bytes occupied in the data region, tags included
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
	// indexVersion is 2 — directories, ruled 2026-09-08 (§11). A 1 is
	// refused: only test archives were ever written under it, so there is no
	// migration and no compatibility to keep.
	indexVersion = 2

	minDirRecord  = 4 + 16 + 1 + 16 + 2 + 8 + 8 + 16
	minFileRecord = 4 + 16 + 1 + 16 + 2 + 8 + 8 + 1 + 32 + 8 + 4 + 2 + NonceSize + WrappedKeySize + 4 + 8 + 16 + 8 + 16 + 8
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

// ValidateName applies R20 to a live record's name — a file's or a
// directory's. A name is **one path element**: valid UTF-8 of at most
// MaxNameUnits UTF-16 code units, not empty, not "." or "..", with no '/', no
// control character, none of \ : * ? " < > |, not ending in a space or a dot,
// and not a Windows reserved device name with or without an extension. The
// reader enforces it, not only the writer: an archive from an untrusted place
// must not be able to name a file `..\..\something`. Tombstones keep whatever
// name they had and are not held to this.
func ValidateName(name string) error {
	if name == "" {
		return invalidf("name is empty")
	}
	if !utf8.ValidString(name) {
		return invalidf("name is not valid UTF-8")
	}
	if n := len(utf16.Encode([]rune(name))); n > MaxNameUnits {
		return invalidf("name of %d UTF-16 code units exceeds %d", n, MaxNameUnits)
	}
	if name == "." || name == ".." {
		return invalidf("name %q is a relative element", name)
	}
	for _, c := range name {
		// R20 says no control character, which is unicode.IsControl — C0 and
		// DEL and the C1 block U+0080–U+009F, not C0 and DEL alone (the
		// outside audit of 2026-09-09).
		if unicode.IsControl(c) || c == '/' || strings.ContainsRune(`\:*?"<>|`, c) {
			return invalidf("name %q contains forbidden character %q", name, c)
		}
	}
	if last := name[len(name)-1]; last == ' ' || last == '.' {
		return invalidf("name %q ends in a space or a dot", name)
	}
	if reservedDeviceName(name) {
		return invalidf("name %q is a reserved device name", name)
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

// FoldKey is a representative of a name's simple-case-folding class: each
// rune replaced by the smallest of its fold orbit. strings.EqualFold compares
// two names orbit by orbit, so two names fold onto one another (R39's rule for
// live siblings) exactly when their keys are equal — which lets a whole
// directory's children be checked with a map instead of pairwise. It is
// exported for the one other place that must agree with R39's rule: an
// extract's plan-time de-duplication of destinations (app.Extract).
func FoldKey(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		low := r
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			if f < low {
				low = f
			}
		}
		b.WriteRune(low)
	}
	return b.String()
}

// validate checks one directory record's own fields. The tree it stands in is
// Index.Validate's business.
func (d *DirRecord) validate() error {
	switch d.State {
	case FileLive:
		if err := ValidateName(d.Name); err != nil {
			return invalidf("directory %x: %s", d.DirID, unwrapInvalid(err))
		}
	case FileTombstone:
		if !utf8.ValidString(d.Name) {
			return invalidf("directory %x name is not valid UTF-8", d.DirID)
		}
	default:
		return invalidf("directory %x state %d unknown", d.DirID, d.State)
	}
	return nil
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
		if err := ValidateName(f.Name); err != nil {
			return invalidf("file %x: %s", f.FileID, unwrapInvalid(err))
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

// unwrapInvalid strips the "format: invalid: " prefix so that a wrapped
// message reads as one sentence rather than repeating the sentinel.
func unwrapInvalid(err error) string {
	s := err.Error()
	if p := ErrInvalid.Error() + ": "; strings.HasPrefix(s, p) {
		return s[len(p):]
	}
	return s
}

func (d *DirRecord) body() ([]byte, error) {
	w := &writer{b: make([]byte, 0, minDirRecord+len(d.Name))}
	w.fixed(d.DirID[:])
	w.u8(uint8(d.State))
	w.fixed(d.ParentID[:])
	w.str(d.Name)
	w.i64(d.ModifiedAt)
	w.u64(d.Revision)
	w.fixed(d.LastWriter[:])
	return w.done()
}

func decodeDirBody(r *reader) *DirRecord {
	d := &DirRecord{}
	r.fixed(d.DirID[:])
	d.State = FileState(r.u8())
	r.fixed(d.ParentID[:])
	d.Name = r.str()
	d.ModifiedAt = r.i64()
	d.Revision = r.u64()
	r.fixed(d.LastWriter[:])
	return d
}

func (f *FileRecord) body() ([]byte, error) {
	w := &writer{b: make([]byte, 0, minFileRecord+len(f.Name))}
	w.fixed(f.FileID[:])
	w.u8(uint8(f.State))
	w.fixed(f.ParentID[:])
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
	r.fixed(f.ParentID[:])
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

// dirPos is where a live directory sits once its chain to the root is known:
// how many parents up the root is, and the byte length its children's paths
// start at — the directory's own joined path plus one for the separator.
type dirPos struct {
	depth  int
	prefix int
}

// Validate checks the whole index against R39, in the reader's order:
// identities first — no id is resolved until they are known distinct, since
// every check below assumes one id names one record — then every live
// directory's chain to the root, then the files against those directories,
// then sibling uniqueness among the live children of one parent, then the
// joined path of every live record.
//
// Encode calls it too (the writer side of R39), so no commit, compaction or
// key rotation publishes an index the next open would reject on these grounds.
func (x *Index) Validate() error {
	if err := x.validateDict(); err != nil {
		return err
	}

	// 1. Identities. An all-zero id is the root, which has no record; no id
	// names two records, tombstones counted alongside live ones, and the
	// dir_ids and file_ids are one id space.
	dirs := make(map[[16]byte]*DirRecord, len(x.Dirs))
	for i := range x.Dirs {
		d := &x.Dirs[i]
		if d.DirID == RootID {
			return invalidf("a directory record carries the all-zero id, which is the root")
		}
		if _, dup := dirs[d.DirID]; dup {
			return invalidf("directory %x appears twice", d.DirID)
		}
		dirs[d.DirID] = d
	}
	files := make(map[[16]byte]*FileRecord, len(x.Files))
	for i := range x.Files {
		f := &x.Files[i]
		if f.FileID == RootID {
			return invalidf("a file record carries the all-zero id, which is the root")
		}
		if _, dup := files[f.FileID]; dup {
			return invalidf("file %x appears twice", f.FileID)
		}
		if _, clash := dirs[f.FileID]; clash {
			return invalidf("id %x names both a file and a directory", f.FileID)
		}
		files[f.FileID] = f
	}

	// 2. Each record's own fields, names included.
	for i := range x.Dirs {
		if err := x.Dirs[i].validate(); err != nil {
			return err
		}
	}
	hasDict := len(x.Dict) > 0
	for i := range x.Files {
		if err := x.Files[i].validate(hasDict); err != nil {
			return err
		}
	}

	// 3. Every live directory reaches the root in at most MaxTreeDepth steps
	// without meeting itself, through live directories only. A tombstone's
	// parent may name anything.
	pos, err := x.dirPositions(dirs)
	if err != nil {
		return err
	}

	// 4. A live file's parent is the root or a live directory.
	for i := range x.Files {
		f := &x.Files[i]
		if f.State != FileLive || f.ParentID == RootID {
			continue
		}
		p, ok := dirs[f.ParentID]
		if !ok {
			return invalidf("file %x names parent %x, which is not a directory of this index", f.FileID, f.ParentID)
		}
		if p.State != FileLive {
			return invalidf("live file %x stands under tombstoned directory %x", f.FileID, f.ParentID)
		}
	}

	// 5. Among the live children of one parent, files and directories share
	// one namespace and their names are unique under simple case folding.
	type sibling struct {
		parent [16]byte
		folded string
	}
	seen := make(map[sibling]struct{}, len(x.Dirs)+len(x.Files))
	claim := func(parent [16]byte, name string) error {
		k := sibling{parent, FoldKey(name)}
		if _, dup := seen[k]; dup {
			return invalidf("two live children of %x fold onto the name %q", parent, name)
		}
		seen[k] = struct{}{}
		return nil
	}
	for i := range x.Dirs {
		if d := &x.Dirs[i]; d.State == FileLive {
			if err := claim(d.ParentID, d.Name); err != nil {
				return err
			}
		}
	}
	for i := range x.Files {
		if f := &x.Files[i]; f.State == FileLive {
			if err := claim(f.ParentID, f.Name); err != nil {
				return err
			}
		}
	}

	// 6. The joined path of every live record — its ancestors' names and its
	// own, separated by '/' — is at most MaxPathLen bytes.
	prefixOf := func(parent [16]byte) int {
		if parent == RootID {
			return 0
		}
		return pos[parent].prefix
	}
	for i := range x.Dirs {
		d := &x.Dirs[i]
		if d.State != FileLive {
			continue
		}
		if n := prefixOf(d.ParentID) + len(d.Name); n > MaxPathLen {
			return invalidf("directory %x joins to a path of %d bytes, over %d", d.DirID, n, MaxPathLen)
		}
	}
	for i := range x.Files {
		f := &x.Files[i]
		if f.State != FileLive {
			continue
		}
		if n := prefixOf(f.ParentID) + len(f.Name); n > MaxPathLen {
			return invalidf("file %x joins to a path of %d bytes, over %d", f.FileID, n, MaxPathLen)
		}
	}
	return nil
}

func (x *Index) validateDict() error {
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
	return nil
}

// dirPositions walks every live directory to the root and returns each one's
// depth and path prefix. A chain that meets itself, that leaves the live
// directories, or that is longer than MaxTreeDepth is invalid (R39). Each
// directory is walked once: a chain stops at the first ancestor already
// placed, and the walk is then written back from that ancestor downwards.
func (x *Index) dirPositions(dirs map[[16]byte]*DirRecord) (map[[16]byte]dirPos, error) {
	pos := make(map[[16]byte]dirPos, len(x.Dirs))
	for i := range x.Dirs {
		start := &x.Dirs[i]
		if start.State != FileLive {
			continue
		}
		if pos[start.DirID].depth != 0 {
			continue
		}
		var chain [][16]byte
		onPath := make(map[[16]byte]struct{})
		base := dirPos{} // the root, until an ancestor already placed says otherwise
		for cur := start; ; {
			if _, loop := onPath[cur.DirID]; loop {
				return nil, invalidf("directory %x is its own ancestor", cur.DirID)
			}
			onPath[cur.DirID] = struct{}{}
			chain = append(chain, cur.DirID)
			if cur.ParentID == RootID {
				break
			}
			p, ok := dirs[cur.ParentID]
			if !ok {
				return nil, invalidf("directory %x names parent %x, which is not a directory of this index", cur.DirID, cur.ParentID)
			}
			if p.State != FileLive {
				return nil, invalidf("live directory %x stands under tombstoned directory %x", cur.DirID, cur.ParentID)
			}
			if known := pos[p.DirID]; known.depth != 0 {
				base = known
				break
			}
			cur = p
		}
		// chain runs from the deepest directory up to the topmost one walked,
		// so place it back to front.
		at := base
		for j := len(chain) - 1; j >= 0; j-- {
			d := dirs[chain[j]]
			at.depth++
			if at.depth > MaxTreeDepth {
				return nil, invalidf("directory %x stands more than %d directories below the root", d.DirID, MaxTreeDepth)
			}
			at.prefix += len(d.Name) + 1
			pos[d.DirID] = at
		}
	}
	return pos, nil
}

// Encode returns the index plaintext (§11): u32 index_version, u32-prefixed
// dictionary, u32 dir_count and the directory records, then u32 file_count and
// the file records, each record prefixed by its own u32 record_len (R15).
// Directories come first so that a reader validates every parent in one pass.
func (x *Index) Encode() ([]byte, error) {
	if err := x.Validate(); err != nil {
		return nil, err
	}
	w := &writer{b: make([]byte, 0, 4+4+len(x.Dict)+4+len(x.Dirs)*(minDirRecord+32)+4+len(x.Files)*(minFileRecord+32))}
	w.u32(indexVersion)
	w.bytes32(x.Dict)
	w.u32(uint32(len(x.Dirs)))
	for i := range x.Dirs {
		body, err := x.Dirs[i].body()
		if err != nil {
			return nil, err
		}
		w.u32(uint32(len(body)))
		w.fixed(body)
	}
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

// DecodeIndex parses an index plaintext. index_version 2 is the only version
// read: an index_version 1 — the object-key model, where a file record carried
// a full path — is refused, not migrated (§11).
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

	nd := r.count(r.u32(), minDirRecord)
	x.Dirs = make([]DirRecord, 0, nd)
	for i := 0; i < nd && r.err == nil; i++ {
		body := r.record(i, "directory record ")
		if body == nil {
			break
		}
		d := decodeDirBody(body)
		if err := body.done(); err != nil {
			return nil, err
		}
		x.Dirs = append(x.Dirs, *d)
	}

	nf := r.count(r.u32(), minFileRecord)
	x.Files = make([]FileRecord, 0, nf)
	for i := 0; i < nf && r.err == nil; i++ {
		body := r.record(i, "file record ")
		if body == nil {
			break
		}
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

// record reads one u32-prefixed record body (R15) and returns a reader over
// exactly its bytes, or nil once the input is exhausted or a record_len claims
// more than is left.
func (r *reader) record(i int, what string) *reader {
	l := r.u32()
	if r.err != nil {
		return nil
	}
	if uint64(l) > uint64(r.remaining()) {
		r.truncatedf("record_len %d exceeds the %d bytes left", l, r.remaining())
		return nil
	}
	return r.sub(int(l), what+itoa(i))
}

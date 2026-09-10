package archive

import (
	"time"

	"github.com/dreamxwarden01/enfold/internal/compress"
	"github.com/dreamxwarden01/enfold/internal/format"
)

// Key is one candidate for opening an archive: the kid the envelope may
// name and the archive key the registry holds for it.
type Key struct {
	KID [16]byte
	Key [32]byte
}

// Options configures Open and Create. DeviceID is required for a writable
// handle; the rest have defaults.
type Options struct {
	// DeviceID is this replica's identity (the registry's device_id), written
	// as last_writer on every record this handle changes. Required unless
	// ReadOnly.
	DeviceID [16]byte
	// ArchiveID is the archive_id the caller's own record holds for this
	// file, and it is the reader's way in when the envelope is not
	// (FORMAT.md R33, amended 2026-09-09): rotation rewrites the 4 KiB
	// envelope in place, so a crash inside that write leaves a checksum that
	// fails, and an envelope that does not decode is then treated as absent —
	// every key is tried against the index at this id, whose AAD binds
	// archive_id ‖ kid and is what decides. Zero leaves the old behaviour: an
	// envelope that does not decode is the file's answer.
	ArchiveID [16]byte
	// ReadOnly opens without an OS lock and refuses every mutation. It does
	// not open beside another handle on the same file: one handle per path
	// per process either way (doc.go "Handles").
	ReadOnly bool
	// Compress configures the zstd writer: level, window, concurrency,
	// padding (DESIGN.md §11 trap 8 — the mitigation for the compressed-size
	// side channel, off by default). Dict is ignored; the index carries the
	// archive's dictionary. Zero value: level Default. This is a property of
	// the writer, not of the archive: the format records only each file's
	// storage.
	Compress compress.Params
	// NoCompression stores every file raw, the per-archive choice trap 8
	// asks for.
	NoCompression bool
	// DictBelow is the largest file the index dictionary is used for; larger
	// files build their own history. Default 256 KiB.
	DictBelow int64
	// InMemoryBelow is the largest file compressed into memory before it is
	// placed, so that its exact size can be allocated first-fit. Larger files
	// are written into a reservation at the end of the file. Default 8 MiB.
	InMemoryBelow int64
}

func (o Options) withDefaults() Options {
	if o.Compress.Level == 0 {
		o.Compress.Level = compress.Default
	}
	o.Compress.Dict = nil
	if o.DictBelow == 0 {
		o.DictBelow = 256 << 10
	}
	if o.InMemoryBelow == 0 {
		o.InMemoryBelow = 8 << 20
	}
	return o
}

// FileInfo describes a live file without its keys. Name is the record's own
// one path element, never a path (R20); ParentID is the directory it hangs
// off, format.RootID for a top-level file (R39). The path is Archive.Path.
type FileInfo struct {
	ID          [16]byte
	ParentID    [16]byte
	Name        string
	Size        uint64 // plaintext length
	StoredSize  uint64 // bytes in the data region, tags included
	Storage     format.Storage
	ContentHash [32]byte
	DEKEpoch    uint32
	Revision    uint64
	LastWriter  [16]byte
	ModifiedAt  int64
}

func infoOf(f *format.FileRecord) FileInfo {
	return FileInfo{
		ID: f.FileID, ParentID: f.ParentID, Name: f.Name, Size: f.OrigSize, StoredSize: f.StoredSize, Storage: f.Storage,
		ContentHash: f.ContentHash, DEKEpoch: f.DEKEpoch, Revision: f.Revision, LastWriter: f.LastWriter, ModifiedAt: f.ModifiedAt,
	}
}

// DirInfo describes a live directory. A folder is a record of its own, never
// a prefix of a name (FORMAT §11, R39, DESIGN.md trap 31), so an empty folder
// exists and a folder's time survives.
type DirInfo struct {
	ID       [16]byte
	ParentID [16]byte
	Name     string
	// ModifiedAt is the folder's own time, not a change clock: the source
	// folder's time when it was added, the time of creation when it was made,
	// and nothing writes it again — a rename, a move, a child added or
	// removed beneath it and the tombstoning of the record all leave it alone
	// (R32).
	ModifiedAt int64
	Revision   uint64
	LastWriter [16]byte
}

func dirInfoOf(d *format.DirRecord) DirInfo {
	return DirInfo{
		ID: d.DirID, ParentID: d.ParentID, Name: d.Name,
		ModifiedAt: d.ModifiedAt, Revision: d.Revision, LastWriter: d.LastWriter,
	}
}

// Receipt is what a commit reports, for the registry's last_stored_size and
// last_written_at: the superblock sequence the state now carries, the file's
// size, and when it was written.
type Receipt struct {
	Seq       uint64
	Size      uint64
	WrittenAt int64
}

// now is the clock every timestamp comes from; tests replace it.
var now = func() int64 { return time.Now().Unix() }

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
	// ReadOnly opens without a lock and refuses every mutation.
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

// FileInfo describes a live file without its keys.
type FileInfo struct {
	ID          [16]byte
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
		ID: f.FileID, Name: f.Name, Size: f.OrigSize, StoredSize: f.StoredSize, Storage: f.Storage,
		ContentHash: f.ContentHash, DEKEpoch: f.DEKEpoch, Revision: f.Revision, LastWriter: f.LastWriter, ModifiedAt: f.ModifiedAt,
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

package archive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/dreamxwarden01/enfold/internal/compress"
	"github.com/dreamxwarden01/enfold/internal/format"
	"github.com/dreamxwarden01/enfold/internal/stream"
)

// FuzzOpen mutates an archive and opens it with the archive_id the caller's
// own registry record holds and every key that record knows. Open must refuse
// with format.ErrInvalid or ErrKey, or open; whatever opens must then extract
// every file either correctly or with an error from the AEAD, the framing or
// the compressor — never a panic, never wrong bytes. Open never writes, so a
// rejected open must leave the file byte for byte as it was (R33).
//
// The corpus is the states a reader meets on disk, not one archive: a torn
// envelope and a mid-rotation file (R33's envelope recovery, unreachable
// without the trusted archive_id and a second key), the two winning
// superblock copies, an extent under quarantine, a commit that gave the tail
// back, and an R40 move. Each is built by the package's own writers, so the
// corpus cannot drift from what a writer produces.
func FuzzOpen(f *testing.F) {
	dir := f.TempDir()
	archiveID, kid := [16]byte{1}, [16]byte{2}
	var key [32]byte
	key[0] = 3
	// The key a rotation moves to: the registry records it before the archive
	// is re-keyed, so a reader holds both (R33).
	next := Key{KID: [16]byte{5}, Key: [32]byte{6}}
	opts := Options{DeviceID: [16]byte{4}, InMemoryBelow: 1 << 10}
	// The expected contents, keyed by the file's name rather than by its
	// file_id: ids are drawn at random by the writer, so they differ between
	// the process that collected a corpus entry and the worker that replays
	// it, and an oracle keyed by them would silently never fire. Names are
	// the fixtures' own and are the same in every process, so they are
	// required to be unique across the corpus.
	contents := map[string][]byte{}

	build := func(name string, o Options, fn func(*Archive)) []byte {
		path := filepath.Join(dir, name)
		a, err := Create(path, archiveID, kid, key, o)
		if err != nil {
			f.Fatal(err)
		}
		fn(a)
		if err := a.Close(); err != nil {
			f.Fatal(err)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			f.Fatal(err)
		}
		return b
	}
	store := func(a *Archive, parent [16]byte, name string, data []byte) FileInfo {
		info, _, err := a.Add(context.Background(), parent, name, bytes.NewReader(data), int64(len(data)))
		if err != nil {
			f.Fatal(err)
		}
		if had, ok := contents[info.Name]; ok && !bytes.Equal(had, data) {
			f.Fatalf("two fixture files are named %q with different contents", info.Name)
		}
		contents[info.Name] = data
		return info
	}
	remove := func(a *Archive, id [16]byte) {
		if _, err := a.Delete(context.Background(), id); err != nil {
			f.Fatal(err)
		}
	}

	// A tree, not a flat list: a directory record, a file under it, an empty
	// folder and a tombstone of each kind, so the corpus carries both record
	// tables (§11, R39).
	tree := build("tree.efd", opts, func(a *Archive) {
		sub, _, err := a.AddDir(context.Background(), format.RootID, "d", 1700000000)
		if err != nil {
			f.Fatal(err)
		}
		if _, _, err := a.AddDir(context.Background(), sub.ID, "empty", 1700000001); err != nil {
			f.Fatal(err)
		}
		gone, _, err := a.AddDir(context.Background(), format.RootID, "gone", 1700000002)
		if err != nil {
			f.Fatal(err)
		}
		first := store(a, format.RootID, "t", text(3000, 1))
		store(a, format.RootID, "n", noise(700, 2))
		store(a, sub.ID, "big", text(5000, 3))
		store(a, gone.ID, "swept", text(400, 4))
		store(a, format.RootID, "e", []byte{})
		// One deletion of each kind: a file's tombstone, and a directory's
		// with the subtree it takes in the same write.
		remove(a, first.ID)
		remove(a, gone.ID)
	})

	// A torn envelope, as an interrupted in-place rewrite leaves it: the
	// checksum fails, so the envelope is absent rather than a verdict, and the
	// caller's archive_id is the way in (R33).
	torn := append([]byte(nil), tree...)
	for i := format.EnvelopeOff + format.SuperblockSize - 32; i < format.EnvelopeOff+format.SuperblockSize; i++ {
		torn[i] ^= 0xff
	}
	if _, err := format.DecodeEnvelope(torn[format.EnvelopeOff : format.EnvelopeOff+format.SuperblockSize]); err == nil {
		f.Fatal("the torn envelope still decodes")
	}

	// The other side of that: an envelope that decodes and whose checksum
	// holds, of a format_version this reader does not support. It is refused
	// outright rather than recovered from, so it is a seed of the refusal
	// path and never one of the structural seeds below (§10, R33).
	future := append([]byte(nil), tree...)
	binary.LittleEndian.PutUint16(future[format.EnvelopeOff+8:], format.FormatVersion+1)
	sum := sha256.Sum256(future[format.EnvelopeOff : format.EnvelopeOff+format.SuperblockSize-32])
	copy(future[format.EnvelopeOff+format.SuperblockSize-32:], sum[:])
	if _, err := format.DecodeEnvelope(future[format.EnvelopeOff : format.EnvelopeOff+format.SuperblockSize]); !errors.Is(err, format.ErrVersion) {
		f.Fatalf("the future envelope is not a version refusal: %v", err)
	}

	// The other winning copy. A fresh file has A at seq 1 and B at 0 (§4), so
	// an empty commit or two puts the winner on B.
	bWins := build("b.efd", opts, func(a *Archive) {
		store(a, format.RootID, "one", text(1200, 11))
		for i := 0; i < 4 && a.live != format.CopyB; i++ {
			if _, err := a.Publish(context.Background()); err != nil {
				f.Fatal(err)
			}
		}
		if a.live != format.CopyB {
			f.Fatal("could not reach a state where copy B wins")
		}
	})

	// An interrupted key rotation: the index is under the new key, the
	// envelope still names the old one. Rotation rewrites the envelope last,
	// so this is what a crash inside that write leaves (R33).
	rotating := build("rot.efd", opts, func(a *Archive) {
		store(a, format.RootID, "r", text(2500, 21))
		if _, err := a.RotateKey(context.Background(), next.KID, next.Key); err != nil {
			f.Fatal(err)
		}
	})
	copy(rotating[format.EnvelopeOff:], (&format.Envelope{ArchiveID: archiveID, KID: kid}).Encode())

	// Quarantine: the extent the last commit freed is still referenced by the
	// losing superblock copy, so it is published free and allocated only from
	// the commit after next (R31).
	quarantined := build("q.efd", opts, func(a *Archive) {
		first := store(a, format.RootID, "a", noise(9000, 31))
		store(a, format.RootID, "b", noise(4000, 32))
		remove(a, first.ID)
	})

	// A commit that gave the tail back, and the empty commit R31 requires
	// before the file may be truncated: the losing copy is retired onto the
	// state just committed, then both copies name the follow-up's.
	trimmed := build("t.efd", opts, func(a *Archive) {
		store(a, format.RootID, "keep", noise(2000, 41))
		last := store(a, format.RootID, "tail", noise(40000, 42))
		remove(a, last.ID)
	})

	// An R40 move: a live extent copied verbatim into a hole wholly before
	// it, the source freed and quarantined by the same commit.
	moved := build("m.efd", Options{DeviceID: opts.DeviceID, NoCompression: true}, func(a *Archive) {
		front := store(a, format.RootID, "front.bin", noise(30000, 51))
		store(a, format.RootID, "keep.bin", noise(20000, 52))
		store(a, format.RootID, "last.bin", noise(8000, 53))
		remove(a, front.ID)
		plan := a.PlanReclaim(0)
		if len(plan.Moves) == 0 {
			f.Fatal("the move fixture planned no move")
		}
		if _, err := a.MoveExtents(context.Background(), plan.Moves, nil); err != nil {
			f.Fatal(err)
		}
	})

	keys := []Key{{KID: kid, Key: key}, next}
	// Every structural seed must open as itself, or the corpus would only be
	// teaching the fuzzer the shape of a file that is refused.
	opens := func(name string, b []byte, staleEnvelope bool) {
		path := filepath.Join(dir, "check-"+name)
		if err := os.WriteFile(path, b, 0o600); err != nil {
			f.Fatal(err)
		}
		a, err := Open(path, keys, Options{ReadOnly: true, ArchiveID: archiveID})
		if err != nil {
			f.Fatalf("seed %s does not open: %v", name, err)
		}
		if a.EnvelopeStale() != staleEnvelope {
			f.Fatalf("seed %s: envelope stale is %v", name, a.EnvelopeStale())
		}
		a.Close()
	}
	opens("tree", tree, false)
	opens("torn", torn, true)
	opens("bWins", bWins, false)
	opens("rotating", rotating, true)
	opens("quarantined", quarantined, false)
	opens("trimmed", trimmed, false)
	opens("moved", moved, false)

	for _, s := range [][]byte{tree, torn, bWins, rotating, quarantined, trimmed, moved} {
		f.Add(s, uint32(0), uint8(0))
	}
	f.Add(future, uint32(0), uint8(0))
	f.Add(tree, uint32(format.ArchiveDataStart+50), uint8(1))
	f.Add(tree, uint32(format.CopyA.ArchiveSuperblockOff()+40), uint8(0x80))
	f.Add(bWins, uint32(format.CopyB.ArchiveSuperblockOff()+40), uint8(0x80))
	f.Add(rotating, uint32(format.EnvelopeOff+24), uint8(0x10))
	f.Add(tree[:len(tree)-100], uint32(0), uint8(0))

	f.Fuzz(func(t *testing.T, data []byte, at uint32, x uint8) {
		if int(at) < len(data) {
			data[at] ^= x
		}
		if len(data) > 1<<20 {
			data = data[:1<<20]
		}
		p := filepath.Join(t.TempDir(), "f.efd")
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		// The archive_id the caller's own record holds: R33's way in when the
		// envelope does not decode.
		a, err := Open(p, keys, Options{ReadOnly: true, ArchiveID: archiveID})
		if err != nil {
			if !errors.Is(err, format.ErrInvalid) && !errors.Is(err, ErrKey) {
				t.Fatalf("unexpected Open error: %v", err)
			}
		} else {
			for _, info := range a.Files() {
				var buf bytes.Buffer
				err := a.Extract(context.Background(), info.ID, &buf)
				if err == nil {
					if want, ok := contents[info.Name]; ok && !bytes.Equal(buf.Bytes(), want) {
						t.Fatalf("file %q (%x) extracted wrong bytes", info.Name, info.ID)
					}
					continue
				}
				switch {
				case errors.Is(err, stream.ErrAuth), errors.Is(err, format.ErrInvalid), errors.Is(err, compress.ErrCorrupt),
					errors.Is(err, ErrContentHash), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, compress.ErrParams):
				default:
					t.Fatalf("unexpected extract error: %v", err)
				}
			}
			if err := a.Close(); err != nil {
				t.Fatalf("close: %v", err)
			}
		}
		// Open never writes — not for a stale envelope, not for a free map it
		// had to rebuild, and not for a file it refused (R33).
		got, rerr := os.ReadFile(p)
		if rerr != nil || !bytes.Equal(got, data) {
			t.Fatalf("opening the archive changed the file: %v", rerr)
		}
	})
}

package archive

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/dreamxwarden01/enfold/internal/compress"
	"github.com/dreamxwarden01/enfold/internal/format"
	"github.com/dreamxwarden01/enfold/internal/stream"
)

// FuzzOpen mutates a small archive and opens it with the right key. Open
// must refuse with format.ErrInvalid or ErrKey, or open; whatever opens must
// then extract every file either correctly or with an error from the AEAD,
// the framing or the compressor — never a panic, never wrong bytes.
func FuzzOpen(f *testing.F) {
	dir := f.TempDir()
	path := filepath.Join(dir, "seed.efd")
	archiveID, kid := [16]byte{1}, [16]byte{2}
	var key [32]byte
	key[0] = 3
	opts := Options{DeviceID: [16]byte{4}, InMemoryBelow: 1 << 10}
	a, err := Create(path, archiveID, kid, key, opts)
	if err != nil {
		f.Fatal(err)
	}
	contents := map[[16]byte][]byte{}
	for name, data := range map[string][]byte{"t": text(3000, 1), "n": noise(700, 2), "big": text(5000, 3), "e": {}} {
		info, _, err := a.Add(context.Background(), name, bytes.NewReader(data), int64(len(data)))
		if err != nil {
			f.Fatal(err)
		}
		contents[info.ID] = data
	}
	if _, err := a.Delete(context.Background(), a.Files()[0].ID); err != nil {
		f.Fatal(err)
	}
	a.Close()
	seed, err := os.ReadFile(path)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed, uint32(0), uint8(0))
	f.Add(seed, uint32(format.ArchiveDataStart+50), uint8(1))
	f.Add(seed, uint32(format.CopyA.ArchiveSuperblockOff()+40), uint8(0x80))
	f.Add(seed[:len(seed)-100], uint32(0), uint8(0))
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
		a, err := Open(p, []Key{{KID: kid, Key: key}}, Options{ReadOnly: true})
		if err != nil {
			if !errors.Is(err, format.ErrInvalid) && !errors.Is(err, ErrKey) {
				t.Fatalf("unexpected Open error: %v", err)
			}
			return
		}
		defer a.Close()
		for _, info := range a.Files() {
			var buf bytes.Buffer
			err := a.Extract(context.Background(), info.ID, &buf)
			if err == nil {
				if want, ok := contents[info.ID]; ok && !bytes.Equal(buf.Bytes(), want) {
					t.Fatalf("file %x extracted wrong bytes", info.ID)
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
	})
}

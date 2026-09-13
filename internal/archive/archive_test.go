package archive

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	mrand "math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dreamxwarden01/enfold/internal/compress"
	"github.com/dreamxwarden01/enfold/internal/format"
)

var (
	ctx      = context.Background()
	deviceID = [16]byte{0xd1, 0xd1, 0xd1, 0xd1, 0xd1, 0xd1, 0xd1, 0xd1, 0xd1, 0xd1, 0xd1, 0xd1, 0xd1, 0xd1, 0xd1, 0xd1}
	// root is the implicit root directory, the parent of every top-level
	// record (FORMAT R39). It has no record of its own.
	root = format.RootID
)

func rnd16(t testing.TB) [16]byte {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	return b
}

func rnd32(t testing.TB) [32]byte {
	t.Helper()
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	return b
}

// text is compressible, noise is not.
func text(n int, seed uint64) []byte {
	words := []string{"archive", "record", "extent", "index", "chunk", "vault", "key", "rotate", "free", "map", "0123456789", "\n"}
	rng := mrand.New(mrand.NewPCG(seed, 3))
	var b bytes.Buffer
	for b.Len() < n {
		b.WriteString(words[rng.IntN(len(words))])
		b.WriteByte(' ')
	}
	return b.Bytes()[:n]
}

func noise(n int, seed uint64) []byte {
	rng := mrand.New(mrand.NewPCG(seed, 5))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(rng.Uint32())
	}
	return b
}

type fixture struct {
	path      string
	archiveID [16]byte
	key       Key
	opts      Options
}

func newFixture(t testing.TB, opts Options) (*Archive, *fixture) {
	t.Helper()
	fx := &fixture{path: filepath.Join(t.TempDir(), "a.efd"), archiveID: rnd16(t), key: Key{KID: rnd16(t), Key: rnd32(t)}}
	if opts.DeviceID == [16]byte{} {
		opts.DeviceID = deviceID
	}
	fx.opts = opts
	a, err := Create(fx.path, fx.archiveID, fx.key.KID, fx.key.Key, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return a, fx
}

func (fx *fixture) open(t testing.TB) *Archive {
	t.Helper()
	a, err := Open(fx.path, []Key{fx.key}, fx.opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return a
}

// add stores one file under parentID; name is one path element (R20), never
// a path — nothing derives a folder from a prefix any more.
func add(t testing.TB, a *Archive, parentID [16]byte, name string, data []byte) FileInfo {
	t.Helper()
	info, _, err := a.Add(ctx, parentID, name, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("add %s: %v", name, err)
	}
	return info
}

// mkdir stages one directory under parentID and commits it.
func mkdir(t testing.TB, a *Archive, parentID [16]byte, name string, modifiedAt int64) DirInfo {
	t.Helper()
	info, _, err := a.AddDir(ctx, parentID, name, modifiedAt)
	if err != nil {
		t.Fatalf("mkdir %s: %v", name, err)
	}
	return info
}

// pathOf is the joined path of a live record, which no record carries.
func pathOf(t testing.TB, a *Archive, id [16]byte) string {
	t.Helper()
	p, err := a.Path(id)
	if err != nil {
		t.Fatalf("path %x: %v", id, err)
	}
	return p
}

// findByPath walks the tree for the record at a '/'-joined path, which is how
// a test names a record now that no lookup by name exists.
func findByPath(t testing.TB, a *Archive, path string) ([16]byte, bool) {
	t.Helper()
	cur := root
	els := strings.Split(path, "/")
	for i, el := range els {
		dirs, files, err := a.Children(cur)
		if err != nil {
			t.Fatalf("children of %x: %v", cur, err)
		}
		next, ok := [16]byte{}, false
		for _, d := range dirs {
			if d.Name == el {
				next, ok = d.ID, true
			}
		}
		if !ok && i == len(els)-1 {
			for _, f := range files {
				if f.Name == el {
					next, ok = f.ID, true
				}
			}
		}
		if !ok {
			return [16]byte{}, false
		}
		cur = next
	}
	return cur, true
}

func extract(t testing.TB, a *Archive, id [16]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := a.Extract(ctx, id, &buf); err != nil {
		t.Fatalf("extract %x: %v", id, err)
	}
	return buf.Bytes()
}

func snapshot(t testing.TB, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func restore(t testing.TB, path string, b []byte) {
	t.Helper()
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCreateAndOpen(t *testing.T) {
	a, fx := newFixture(t, Options{})
	if a.ID() != fx.archiveID || a.KID() != fx.key.KID || a.EnvelopeStale() || a.Stale() != nil || a.FreeMapRebuilt() != nil {
		t.Fatalf("fresh archive: stale=%v", a.Stale())
	}
	size, files, free := a.Stat()
	if files != 0 || free != 0 || size < format.ArchiveDataStart {
		t.Fatalf("stat: %d %d %d", size, files, free)
	}
	env, err := ReadEnvelope(fx.path)
	if err != nil || env.ArchiveID != fx.archiveID || env.KID != fx.key.KID {
		t.Fatalf("envelope: %+v %v", env, err)
	}
	// One handle per path per process (doc.go "Handles"): neither a second
	// writer nor a read-only handle beside this one.
	if _, err := Open(fx.path, []Key{fx.key}, fx.opts); !errors.Is(err, ErrBusy) {
		t.Errorf("second writer: %v", err)
	}
	if _, err := Open(fx.path, []Key{fx.key}, Options{ReadOnly: true}); !errors.Is(err, ErrBusy) {
		t.Errorf("read-only handle beside a writer: %v", err)
	}
	a.Close()
	// A read-only handle of its own opens, and refuses every write.
	ro, err := Open(fx.path, []Key{fx.key}, Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ro.Add(ctx, root, "x", bytes.NewReader([]byte("x")), 1); !errors.Is(err, ErrReadOnly) {
		t.Errorf("add through read-only: %v", err)
	}
	if err := ro.RepairEnvelope(); !errors.Is(err, ErrReadOnly) {
		t.Errorf("repair through read-only: %v", err)
	}
	ro.Close()
	// Wrong key, wrong kid, no keys.
	if _, err := Open(fx.path, []Key{{KID: fx.key.KID, Key: rnd32(t)}}, fx.opts); !errors.Is(err, ErrKey) {
		t.Errorf("wrong key: %v", err)
	}
	if _, err := Open(fx.path, []Key{{KID: rnd16(t), Key: fx.key.Key}}, fx.opts); !errors.Is(err, ErrKey) {
		t.Errorf("wrong kid: %v", err)
	}
	if _, err := Open(fx.path, nil, fx.opts); !errors.Is(err, ErrParams) {
		t.Errorf("no keys: %v", err)
	}
	if _, err := Open(fx.path, []Key{fx.key}, Options{}); !errors.Is(err, ErrParams) {
		t.Errorf("no device id: %v", err)
	}
	if _, err := Create(fx.path, fx.archiveID, fx.key.KID, fx.key.Key, fx.opts); err == nil {
		t.Error("create over an existing file")
	}
	// After Close the lock is gone and the file reopens.
	fx.open(t)
}

// One handle per path per process (doc.go "Handles", FORMAT.md R31 as amended
// on 2026-09-09): the extents a writer may not truncate are the ones its own
// Readers hold, so a second handle — whose Readers the first cannot see, and
// which takes no OS lock at all when it is read-only — is refused.
func TestOneHandlePerPathPerProcess(t *testing.T) {
	a, fx := newFixture(t, Options{})
	ro := fx.opts
	ro.ReadOnly = true
	// Another spelling of the same path is the same claim: it is cleaned and,
	// on Windows, folded to one case before it is looked up.
	uncleaned := filepath.Dir(fx.path) + string(filepath.Separator) + "." + string(filepath.Separator) + filepath.Base(fx.path)

	for _, tc := range []struct {
		what string
		path string
		opts Options
	}{
		{"a second writer", fx.path, fx.opts},
		{"a read-only handle beside a writer", fx.path, ro},
		{"a read-only handle on an uncleaned path", uncleaned, ro},
	} {
		if b, err := Open(tc.path, []Key{fx.key}, tc.opts); !errors.Is(err, ErrBusy) {
			if err == nil {
				b.Close()
			}
			t.Errorf("%s: %v", tc.what, err)
		}
	}
	a.Close()

	// The path is free again, and two read-only handles share it — neither
	// writes, so neither can move the ground under the other.
	r1, err := Open(fx.path, []Key{fx.key}, ro)
	if err != nil {
		t.Fatalf("read-only after the writer closed: %v", err)
	}
	r2, err := Open(fx.path, []Key{fx.key}, ro)
	if err != nil {
		t.Fatalf("a second read-only handle: %v", err)
	}
	if _, err := Open(fx.path, []Key{fx.key}, fx.opts); !errors.Is(err, ErrBusy) {
		t.Errorf("a writer beside two readers: %v", err)
	}
	r1.Close()
	if _, err := Open(fx.path, []Key{fx.key}, fx.opts); !errors.Is(err, ErrBusy) {
		t.Errorf("a writer beside the reader that is left: %v", err)
	}
	r2.Close()
	r2.Close() // idempotent, and it does not give a claim back twice

	// Every claim given back: the path opens for writing again. An Open that
	// fails gives its claim back too.
	if _, err := Open(fx.path, []Key{{KID: fx.key.KID, Key: rnd32(t)}}, fx.opts); !errors.Is(err, ErrKey) {
		t.Errorf("wrong key: %v", err)
	}
	b := fx.open(t)
	if _, err := Open(fx.path, []Key{fx.key}, ro); !errors.Is(err, ErrBusy) {
		t.Errorf("read-only beside the reopened writer: %v", err)
	}
	b.Close()
	c, err := Open(fx.path, []Key{fx.key}, ro)
	if err != nil {
		t.Fatalf("read-only after the writer closed again: %v", err)
	}
	c.Close()
}

func TestAddAndExtract(t *testing.T) {
	// A small InMemoryBelow exercises the end-of-file reservation path.
	a, fx := newFixture(t, Options{InMemoryBelow: 64 << 10})
	big := mkdir(t, a, root, "big", 1700000000)
	// Every name is one element; "big/text.txt" is a record named text.txt
	// under the directory record big, and its path is derived from the tree.
	inputs := map[string][]byte{
		"empty.txt":      {},
		"tiny.txt":       []byte("x"),
		"text.txt":       text(50<<10, 1),
		"big/text.txt":   text(300<<10, 2),
		"noise.bin":      noise(200<<10, 3),
		"big/noise.bin":  noise(300<<10, 4),
		"zeros.bin":      make([]byte, 100<<10),
		"one-chunk.bin":  noise(65536, 5),
		"two-chunks.txt": text(65537, 6),
	}
	want := map[string]format.Storage{
		"empty.txt": format.StorageRaw, "tiny.txt": format.StorageRaw, "text.txt": format.StorageZstd,
		"big/text.txt": format.StorageZstd, "noise.bin": format.StorageRaw, "big/noise.bin": format.StorageRaw,
		"zeros.bin": format.StorageZstd, "one-chunk.bin": format.StorageRaw, "two-chunks.txt": format.StorageZstd,
	}
	ids := map[string][16]byte{}
	for name, data := range inputs {
		parent, el := root, name
		if base, ok := strings.CutPrefix(name, "big/"); ok {
			parent, el = big.ID, base
		}
		info := add(t, a, parent, el, data)
		ids[name] = info.ID
		if info.Storage != want[name] || info.Size != uint64(len(data)) || info.DEKEpoch != 1 || info.Revision != 1 || info.LastWriter != deviceID {
			t.Errorf("%s: %+v", name, info)
		}
		if info.ParentID != parent || info.Name != el {
			t.Errorf("%s: parent %x name %q", name, info.ParentID, info.Name)
		}
		if got := pathOf(t, a, info.ID); got != name {
			t.Errorf("%s: path %q", name, got)
		}
		if info.Storage == format.StorageRaw && info.StoredSize != format.RawStoredSize(uint64(len(data))) {
			t.Errorf("%s: raw stored size %d", name, info.StoredSize)
		}
		if got := extract(t, a, info.ID); !bytes.Equal(got, data) {
			t.Errorf("%s: extracted %d bytes, want %d", name, len(got), len(data))
		}
	}
	if len(a.Files()) != len(inputs) || len(a.Dirs()) != 1 {
		t.Fatalf("%d files, %d dirs", len(a.Files()), len(a.Dirs()))
	}
	// Children lists one directory's own, never a subtree and never a prefix
	// projection; an id that is not the root or a live directory is refused.
	dirs, files, err := a.Children(root)
	if err != nil || len(dirs) != 1 || len(files) != 7 {
		t.Fatalf("children of the root: %d dirs %d files %v", len(dirs), len(files), err)
	}
	if _, files, err := a.Children(big.ID); err != nil || len(files) != 2 {
		t.Fatalf("children of big: %d files %v", len(files), err)
	}
	if _, _, err := a.Children(ids["text.txt"]); !errors.Is(err, ErrNotFound) {
		t.Errorf("children of a file: %v", err)
	}
	if _, _, err := a.Children(rnd16(t)); !errors.Is(err, ErrNotFound) {
		t.Errorf("children of an unknown id: %v", err)
	}
	if got := pathOf(t, a, big.ID); got != "big" {
		t.Errorf("directory path %q", got)
	}
	if got, _ := a.Path(root); got != "" {
		t.Errorf("the root's path is %q", got)
	}
	// One namespace per parent, folded: the same name in another folder is
	// free, and the folded one is not.
	if _, _, err := a.Add(ctx, root, "text.txt", bytes.NewReader([]byte("dup")), 3); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate name: %v", err)
	}
	if _, _, err := a.Add(ctx, root, "TEXT.TXT", bytes.NewReader([]byte("dup")), 3); !errors.Is(err, ErrExists) {
		t.Errorf("folded duplicate name: %v", err)
	}
	if _, _, err := a.AddDir(ctx, root, "Text.txt", 1); !errors.Is(err, ErrExists) {
		t.Errorf("a directory folding onto a file's name: %v", err)
	}
	if _, _, err := a.Add(ctx, root, "../escape", bytes.NewReader([]byte("x")), 1); !errors.Is(err, format.ErrInvalid) {
		t.Errorf("bad name: %v", err)
	}
	if _, _, err := a.Add(ctx, root, "a/b", bytes.NewReader([]byte("x")), 1); !errors.Is(err, format.ErrInvalid) {
		t.Errorf("a path as a name: %v", err)
	}
	if _, _, err := a.Add(ctx, rnd16(t), "orphan", bytes.NewReader([]byte("x")), 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("add under an unknown parent: %v", err)
	}
	if _, _, err := a.Add(ctx, ids["text.txt"], "orphan", bytes.NewReader([]byte("x")), 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("add under a file: %v", err)
	}
	// ExtractTo: temp beside the target, refuses to overwrite.
	dst := filepath.Join(t.TempDir(), "out.txt")
	if err := a.ExtractTo(ctx, ids["big/text.txt"], dst); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(dst); !bytes.Equal(got, inputs["big/text.txt"]) {
		t.Error("ExtractTo content")
	}
	if err := a.ExtractTo(ctx, ids["big/text.txt"], dst); !errors.Is(err, os.ErrExist) {
		t.Errorf("ExtractTo over an existing file: %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Dir(dst)); len(entries) != 1 {
		t.Errorf("temp files left behind: %d entries", len(entries))
	}
	// Everything survives a reopen, tree and all.
	a.Close()
	b := fx.open(t)
	for name, data := range inputs {
		if got := extract(t, b, ids[name]); !bytes.Equal(got, data) {
			t.Errorf("%s after reopen", name)
		}
		if got := pathOf(t, b, ids[name]); got != name {
			t.Errorf("%s: path %q after reopen", name, got)
		}
	}
	if err := b.Extract(ctx, rnd16(t), io.Discard); !errors.Is(err, ErrNotFound) {
		t.Errorf("extract unknown: %v", err)
	}
	if err := b.Extract(ctx, big.ID, io.Discard); !errors.Is(err, ErrNotFound) {
		t.Errorf("extract a directory: %v", err)
	}
	if d, ok := b.InfoDir(big.ID); !ok || d.Name != "big" || d.ModifiedAt != 1700000000 || d.ParentID != root {
		t.Errorf("directory record after reopen: %+v %v", d, ok)
	}
	if _, ok := b.Info(big.ID); ok {
		t.Error("Info answered for a directory id")
	}
	if _, ok := b.InfoDir(ids["text.txt"]); ok {
		t.Error("InfoDir answered for a file id")
	}
}

// lyingReader claims one size and has another.
type lyingReader struct {
	data []byte
}

func (l lyingReader) ReadAt(p []byte, off int64) (int, error) {
	return bytes.NewReader(l.data).ReadAt(p, off)
}

func TestSourceChanged(t *testing.T) {
	a, _ := newFixture(t, Options{})
	data := text(10000, 7)
	if _, _, err := a.Add(ctx, root, "short", lyingReader{data[:5000]}, 10000); !errors.Is(err, ErrSourceChanged) {
		t.Errorf("short source: %v", err)
	}
	if _, _, err := a.Add(ctx, root, "long", lyingReader{data}, 5000); !errors.Is(err, ErrSourceChanged) {
		t.Errorf("long source: %v", err)
	}
	// Neither left a record, and the aborted transactions left no growth.
	if len(a.Files()) != 0 {
		t.Error("records left by failed adds")
	}
	size, _, _ := a.Stat()
	st, _ := os.Stat(a.path)
	if uint64(st.Size()) != size {
		t.Errorf("file %d bytes, state says %d", st.Size(), size)
	}
	if _, _, err := a.Add(ctx, root, "neg", bytes.NewReader(nil), -1); !errors.Is(err, ErrNoSpace) {
		t.Errorf("negative size: %v", err)
	}
}

func TestTransactionAndMutations(t *testing.T) {
	a, fx := newFixture(t, Options{})
	tx, err := a.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Begin(); !errors.Is(err, ErrTxOpen) {
		t.Errorf("second Begin: %v", err)
	}
	var infos []FileInfo
	for i := 0; i < 5; i++ {
		info, err := tx.Add(ctx, root, fmt.Sprintf("f%d.txt", i), bytes.NewReader(text(20000+i, uint64(i))), int64(20000+i))
		if err != nil {
			t.Fatal(err)
		}
		infos = append(infos, info)
	}
	if len(a.Files()) != 0 {
		t.Error("uncommitted files visible")
	}
	rec, err := tx.Commit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Seq != 2 || len(a.Files()) != 5 {
		t.Fatalf("after commit: %+v, %d files", rec, len(a.Files()))
	}
	if _, err := tx.Add(ctx, root, "late", bytes.NewReader([]byte("x")), 1); !errors.Is(err, ErrClosed) {
		t.Errorf("add after commit: %v", err)
	}

	// Replace: new epoch, new content, same identity and name.
	newData := text(30000, 99)
	info, rec2, err := a.Replace(ctx, infos[1].ID, bytes.NewReader(newData), int64(len(newData)))
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != infos[1].ID || info.Name != infos[1].Name || info.DEKEpoch != 2 || info.Revision != 2 || rec2.Seq != 3 {
		t.Fatalf("replace: %+v", info)
	}
	if got := extract(t, a, info.ID); !bytes.Equal(got, newData) {
		t.Error("replaced content")
	}
	// Delete and rename.
	if _, err := a.Delete(ctx, infos[2].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Delete(ctx, infos[2].ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete twice: %v", err)
	}
	if _, err := a.Rename(ctx, infos[3].ID, "renamed.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Rename(ctx, infos[4].ID, "renamed.txt"); !errors.Is(err, ErrExists) {
		t.Errorf("rename onto a live name: %v", err)
	}
	if _, err := a.Rename(ctx, infos[4].ID, "f2.txt"); err != nil {
		t.Errorf("rename onto a deleted file's name: %v", err)
	}
	if _, err := a.Rename(ctx, infos[4].ID, "CON"); !errors.Is(err, format.ErrInvalid) {
		t.Errorf("rename to a reserved name: %v", err)
	}
	// The tombstone is in the index, not in the listing; deletion freed space.
	if len(a.Files()) != 4 || len(a.index.Files) != 5 {
		t.Fatalf("%d live, %d records", len(a.Files()), len(a.index.Files))
	}
	if _, _, free := a.Stat(); free == 0 {
		t.Error("nothing freed by delete")
	}
	// Abort truncates appended data and publishes nothing.
	size0, _, _ := a.Stat()
	tx, _ = a.Begin()
	if _, err := tx.Add(ctx, root, "aborted", bytes.NewReader(noise(200000, 8)), 200000); err != nil {
		t.Fatal(err)
	}
	tx.Abort()
	if size1, _, _ := a.Stat(); size1 != size0 || len(a.Files()) != 4 {
		t.Errorf("abort: size %d → %d", size0, size1)
	}
	// An empty transaction commits nothing.
	tx, _ = a.Begin()
	if rec, err := tx.Commit(ctx); err != nil || rec.Seq != a.sb.Seq {
		t.Errorf("empty commit: %+v %v", rec, err)
	}
	a.Close()
	b := fx.open(t)
	if len(b.Files()) != 4 || len(b.index.Files) != 5 {
		t.Fatalf("after reopen: %d live, %d records", len(b.Files()), len(b.index.Files))
	}
	if got := extract(t, b, infos[1].ID); !bytes.Equal(got, newData) {
		t.Error("replaced content after reopen")
	}
	if _, ok := findByPath(t, b, "renamed.txt"); !ok {
		t.Error("rename lost")
	}
}

// The quarantine is what a commit that does not give the tail back leaves
// behind: a replace frees the old extent and appends the new one, so the
// file keeps its length and no follow-up commit retires the losing copy
// (trim.go). The freed extent is then the one R31 protects.
func TestQuarantineAndReuse(t *testing.T) {
	a, _ := newFixture(t, Options{})
	data := noise(100000, 9)
	first := add(t, a, root, "a", data)
	firstExt := extent{Off: a.record(first.ID).DataOff, Len: first.StoredSize}
	seq := a.Seq()
	if _, _, err := a.Replace(ctx, first.ID, bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatal(err)
	}
	if a.Seq() != seq+1 {
		t.Fatalf("the replace gave a tail back (%d commits): the quarantine is not what this test is watching", a.Seq()-seq)
	}
	// The next transaction must not reuse the extent the previous commit
	// freed (the losing superblock still references it); the one after may.
	second := add(t, a, root, "b", data)
	if second.StoredSize != first.StoredSize {
		t.Fatalf("sizes differ")
	}
	if sec := a.record(second.ID); overlaps(extent{Off: sec.DataOff, Len: sec.StoredSize}, firstExt) {
		t.Errorf("extent reused one commit too early: second at 0x%x, freed [0x%x, 0x%x)", sec.DataOff, firstExt.Off, end(firstExt))
	}
	third := add(t, a, root, "c", data)
	if th := a.record(third.ID); !overlaps(extent{Off: th.DataOff, Len: th.StoredSize}, firstExt) {
		t.Errorf("freed extent not reused after quarantine: third at 0x%x, freed [0x%x, 0x%x)", th.DataOff, firstExt.Off, end(firstExt))
	}
	for _, id := range [][16]byte{second.ID, third.ID} {
		if got := extract(t, a, id); !bytes.Equal(got, data) {
			t.Error("content")
		}
	}
}

// TestQuarantineProtectsFallback is the case R31's quarantine exists for: a
// transaction in flight allocates while the live superblock copy is then
// found damaged. The fallback state must still have every extent it
// references — including the one the last commit freed. The commit here is a
// replace, which frees an extent without leaving a tail, so nothing retires
// the losing copy and the quarantine is what stands between the two states
// (a delete's freed tail is trim.go's, and is proved there).
func TestQuarantineProtectsFallback(t *testing.T) {
	a, fx := newFixture(t, Options{})
	data := noise(80000, 33)
	keep := add(t, a, root, "keep", text(20000, 34))
	victim := add(t, a, root, "victim", data)
	// State N frees the victim's old extent and appends its new content.
	if _, _, err := a.Replace(ctx, victim.ID, bytes.NewReader(text(30000, 35)), 30000); err != nil {
		t.Fatal(err)
	}
	// Transaction N+1: a same-size add that first-fit would put into the
	// freed extent if it were not quarantined. Write it, snapshot, abort.
	tx, err := a.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Add(ctx, root, "intruder", bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatal(err)
	}
	inflight := snapshot(t, fx.path)
	tx.Abort()
	a.Close()
	// Damage the live copy (state N) in the in-flight snapshot: the reader
	// falls back to N−1, where the victim is live.
	sbA, _ := format.DecodeArchiveSuperblock(inflight[format.CopyA.ArchiveSuperblockOff() : format.CopyA.ArchiveSuperblockOff()+format.SuperblockSize])
	sbB, _ := format.DecodeArchiveSuperblock(inflight[format.CopyB.ArchiveSuperblockOff() : format.CopyB.ArchiveSuperblockOff()+format.SuperblockSize])
	liveOff := format.CopyA.ArchiveSuperblockOff()
	if sbB.Seq > sbA.Seq {
		liveOff = format.CopyB.ArchiveSuperblockOff()
	}
	inflight[liveOff+200] ^= 1
	restore(t, fx.path, inflight)
	b := fx.open(t)
	if b.Stale() == nil {
		t.Fatal("damaged live copy not reported")
	}
	if got := extract(t, b, victim.ID); !bytes.Equal(got, data) {
		t.Error("the fallback state's file was overwritten by the in-flight transaction")
	}
	if got := extract(t, b, keep.ID); !bytes.Equal(got, text(20000, 34)) {
		t.Error("keep")
	}
}

func TestReaderSeek(t *testing.T) {
	a, _ := newFixture(t, Options{})
	rawData := noise(300000, 10)
	zData := text(300000, 11)
	raw := add(t, a, root, "raw", rawData)
	z := add(t, a, root, "z", zData)
	if raw.Storage != format.StorageRaw || z.Storage != format.StorageZstd {
		t.Fatalf("storage %v %v", raw.Storage, z.Storage)
	}
	rng := mrand.New(mrand.NewPCG(7, 8))
	for _, c := range []struct {
		id   [16]byte
		data []byte
	}{{raw.ID, rawData}, {z.ID, zData}} {
		r, err := a.OpenReader(c.id)
		if err != nil {
			t.Fatal(err)
		}
		if end, err := r.Seek(0, io.SeekEnd); err != nil || end != int64(len(c.data)) {
			t.Fatalf("SeekEnd %d %v", end, err)
		}
		if _, err := r.Seek(0, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		head := make([]byte, 512)
		if _, err := io.ReadFull(r, head); err != nil || !bytes.Equal(head, c.data[:512]) {
			t.Fatalf("head after rewind: %v", err)
		}
		for i := 0; i < 30; i++ {
			off := rng.IntN(len(c.data))
			n := rng.IntN(5000)
			if _, err := r.Seek(int64(off), io.SeekStart); err != nil {
				t.Fatal(err)
			}
			buf := make([]byte, n)
			got, err := io.ReadFull(r, buf)
			want := min(n, len(c.data)-off)
			if got != want || (err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF)) {
				t.Fatalf("off %d n %d: got %d want %d (%v)", off, n, got, want, err)
			}
			if !bytes.Equal(buf[:got], c.data[off:off+got]) {
				t.Fatalf("off %d: bytes differ", off)
			}
			if cur, _ := r.Seek(0, io.SeekCurrent); cur != int64(off+got) {
				t.Fatalf("position %d, want %d", cur, off+got)
			}
		}
		if _, err := r.Seek(-1, io.SeekStart); err == nil {
			t.Error("negative seek")
		}
		r.Close()
		if _, err := r.Read(make([]byte, 1)); !errors.Is(err, ErrClosed) {
			t.Errorf("read after close: %v", err)
		}
	}
}

func TestHeldExtentSurvivesReplace(t *testing.T) {
	a, _ := newFixture(t, Options{})
	old := noise(150000, 12)
	info := add(t, a, root, "f", old)
	r, err := a.OpenReader(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Replace and then fill the archive with more data; the reader must
	// still see the old bytes, and the space must be reused only after it
	// closes.
	replacement := noise(150000, 13)
	if _, _, err := a.Replace(ctx, info.ID, bytes.NewReader(replacement), int64(len(replacement))); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		add(t, a, root, fmt.Sprintf("fill%d", i), noise(150000, uint64(20+i)))
	}
	got, err := io.ReadAll(r)
	if err != nil || !bytes.Equal(got, old) {
		t.Fatalf("held reader: %v", err)
	}
	if _, ok := a.held[r.e]; !ok {
		t.Error("extent not held")
	}
	r.Close()
	if _, ok := a.held[r.e]; ok {
		t.Error("extent still held after close")
	}
	// Now it is reusable (after the quarantine of the commit that freed it,
	// long past).
	late := add(t, a, root, "late", noise(150000, 30))
	if lr := a.record(late.ID); !overlaps(extent{Off: lr.DataOff, Len: lr.StoredSize}, r.e) {
		t.Errorf("released extent not reused: late at 0x%x, held was 0x%x", lr.DataOff, r.e.Off)
	}
	if got := extract(t, a, info.ID); !bytes.Equal(got, replacement) {
		t.Error("replacement content")
	}
	// Archive.Close ends every open Reader.
	r2, err := a.OpenReader(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r2.Read(make([]byte, 10)); err != nil {
		t.Fatal(err)
	}
	a.Close()
	if _, err := r2.Read(make([]byte, 10)); !errors.Is(err, ErrClosed) {
		t.Errorf("read after Archive.Close: %v", err)
	}
	if _, err := r2.Seek(0, io.SeekStart); !errors.Is(err, ErrClosed) {
		t.Errorf("seek after Archive.Close: %v", err)
	}
	if err := r2.Close(); err != nil {
		t.Errorf("Reader.Close after Archive.Close: %v", err)
	}
}

func TestCrashBeforeFlipAndFallback(t *testing.T) {
	a, fx := newFixture(t, Options{})
	f1 := add(t, a, root, "one", text(40000, 40))
	f2 := add(t, a, root, "two", noise(40000, 41))
	a.Close()
	before := snapshot(t, fx.path)
	a = fx.open(t)
	liveCopy := a.live
	// One commit that touches everything: a replace, a delete, an add.
	tx, _ := a.Begin()
	if _, err := tx.Replace(ctx, f1.ID, bytes.NewReader(text(50000, 42)), 50000); err != nil {
		t.Fatal(err)
	}
	if err := tx.Delete(f2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Add(ctx, root, "three", bytes.NewReader(noise(30000, 43)), 30000); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	a.Close()
	after := snapshot(t, fx.path)

	// Crash before the flip: the new superblock write undone.
	crashed := bytes.Clone(after)
	target := liveCopy.Other().ArchiveSuperblockOff()
	copy(crashed[target:target+format.SuperblockSize], before[target:target+format.SuperblockSize])
	restore(t, fx.path, crashed)
	b := fx.open(t)
	if len(b.Files()) != 2 {
		t.Fatalf("crashed file: %d files", len(b.Files()))
	}
	if got := extract(t, b, f1.ID); !bytes.Equal(got, text(40000, 40)) {
		t.Error("old content of one")
	}
	if got := extract(t, b, f2.ID); !bytes.Equal(got, noise(40000, 41)) {
		t.Error("old content of two")
	}
	// The tail the crashed transaction appended is reclaimed as free.
	if _, _, free := b.Stat(); free == 0 {
		t.Error("appended tail not reclaimed")
	}
	b.Close()

	// The live copy torn after the commit: the file opens one commit behind.
	// (What protects that state here is the published map itself — a state
	// never lists its own extents as free; the quarantine's own case is
	// TestQuarantineProtectsFallback.)
	restore(t, fx.path, after)
	c := fx.open(t)
	add(t, c, root, "four", noise(20000, 44)) // one more commit on top
	c.Close()
	torn := snapshot(t, fx.path)
	var damaged [format.SuperblockSize]byte
	// Find the live copy: the one with the higher seq.
	cA, _ := format.DecodeArchiveSuperblock(torn[format.CopyA.ArchiveSuperblockOff() : format.CopyA.ArchiveSuperblockOff()+format.SuperblockSize])
	cB, _ := format.DecodeArchiveSuperblock(torn[format.CopyB.ArchiveSuperblockOff() : format.CopyB.ArchiveSuperblockOff()+format.SuperblockSize])
	liveOff := format.CopyA.ArchiveSuperblockOff()
	if cB.Seq > cA.Seq {
		liveOff = format.CopyB.ArchiveSuperblockOff()
	}
	copy(torn[liveOff:], damaged[:])
	restore(t, fx.path, torn)
	d := fx.open(t)
	if d.Stale() == nil {
		t.Error("damaged live copy not reported")
	}
	if len(d.Files()) != 2 {
		t.Fatalf("one commit behind: %d files", len(d.Files()))
	}
	if got := extract(t, d, f1.ID); !bytes.Equal(got, text(50000, 42)) {
		t.Error("one commit behind: one")
	}
	if _, ok := findByPath(t, d, "three"); !ok {
		t.Error("one commit behind: three")
	}
	if _, ok := findByPath(t, d, "four"); ok {
		t.Error("the torn commit's file is visible")
	}
	d.Close()
}

func TestRotateKey(t *testing.T) {
	a, fx := newFixture(t, Options{})
	infos := []FileInfo{add(t, a, root, "a", text(30000, 50)), add(t, a, root, "b", noise(30000, 51))}
	if _, err := a.Delete(ctx, infos[1].ID); err != nil {
		t.Fatal(err)
	}
	newKey := Key{KID: rnd16(t), Key: rnd32(t)}
	if _, err := a.RotateKey(ctx, fx.key.KID, newKey.Key); !errors.Is(err, ErrParams) {
		t.Errorf("rotate to the same kid: %v", err)
	}
	rec, err := a.RotateKey(ctx, newKey.KID, newKey.Key)
	if err != nil {
		t.Fatal(err)
	}
	// Seven commits for five changes: the delete gave the file's tail back
	// and so did the rotation, which frees the index and free map it
	// replaces, and each of those is followed by its empty commit (R31 as
	// amended, trim.go).
	if a.KID() != newKey.KID || a.EnvelopeStale() || rec.Seq != 7 {
		t.Fatalf("after rotation: kid %x stale %v seq %d", a.KID(), a.EnvelopeStale(), rec.Seq)
	}
	if got := extract(t, a, infos[0].ID); !bytes.Equal(got, text(30000, 50)) {
		t.Error("content after rotation")
	}
	if a.record(infos[0].ID).DEKEpoch != 1 {
		t.Error("rotation moved dek_epoch")
	}
	a.Close()
	if _, err := Open(fx.path, []Key{fx.key}, fx.opts); !errors.Is(err, ErrKey) {
		t.Errorf("old key after rotation: %v", err)
	}
	if env, _ := ReadEnvelope(fx.path); env.KID != newKey.KID {
		t.Error("envelope not rewritten")
	}
	// An interrupted rotation: the envelope still names the old kid. Open
	// with both candidates finds the index under the new one, reports the
	// envelope, and RepairEnvelope fixes it.
	b := snapshot(t, fx.path)
	oldEnv := format.Envelope{ArchiveID: fx.archiveID, KID: fx.key.KID}
	copy(b[format.EnvelopeOff:], oldEnv.Encode())
	restore(t, fx.path, b)
	c, err := Open(fx.path, []Key{fx.key, newKey}, fx.opts)
	if err != nil {
		t.Fatal(err)
	}
	if !c.EnvelopeStale() || c.KID() != newKey.KID {
		t.Fatalf("stale envelope: stale=%v kid=%x", c.EnvelopeStale(), c.KID())
	}
	if got := extract(t, c, infos[0].ID); !bytes.Equal(got, text(30000, 50)) {
		t.Error("content through a stale envelope")
	}
	if err := c.RepairEnvelope(); err != nil || c.EnvelopeStale() {
		t.Fatalf("repair: %v", err)
	}
	c.Close()
	if env, _ := ReadEnvelope(fx.path); env.KID != newKey.KID {
		t.Error("envelope not repaired")
	}
	fx.key = newKey
	fx.open(t)
}

// R33, amended after the outside audit of 2026-09-09: rotation rewrites the
// 4 KiB envelope in place, so a crash inside that write leaves a checksum
// that fails and used to leave an archive that would not open — the reader
// decoded the envelope before it tried a key. An envelope that does not
// decode is now absent, not a verdict: with the archive_id the caller's own
// record holds, every key is tried against the index (whose AAD binds
// archive_id ‖ kid, which is what decides), the envelope is reported stale
// and RepairEnvelope writes it again. Only a file no key opens is corrupt.
func TestTornEnvelopeOpensUnderTheCallersArchiveID(t *testing.T) {
	a, fx := newFixture(t, Options{})
	info := add(t, a, root, "a", text(30000, 70))
	newKey := Key{KID: rnd16(t), Key: rnd32(t)}
	if _, err := a.RotateKey(ctx, newKey.KID, newKey.Key); err != nil {
		t.Fatal(err)
	}
	a.Close()

	// The envelope's checksum, as a torn in-place write leaves it.
	tear := func() {
		b := snapshot(t, fx.path)
		for i := format.EnvelopeOff + format.SuperblockSize - 32; i < format.EnvelopeOff+format.SuperblockSize; i++ {
			b[i] ^= 0xff
		}
		restore(t, fx.path, b)
		if _, err := format.DecodeEnvelope(b[format.EnvelopeOff : format.EnvelopeOff+format.SuperblockSize]); err == nil {
			t.Fatal("the envelope still decodes")
		}
	}
	tear()

	// With no archive id to open at, the envelope is still the file's answer.
	if _, err := Open(fx.path, []Key{fx.key, newKey}, fx.opts); !errors.Is(err, format.ErrInvalid) {
		t.Fatalf("without an archive id: %v", err)
	}
	opts := fx.opts
	opts.ArchiveID = fx.archiveID
	c, err := Open(fx.path, []Key{fx.key, newKey}, opts)
	if err != nil {
		t.Fatalf("open with a torn envelope: %v", err)
	}
	if !c.EnvelopeStale() || c.KID() != newKey.KID || c.ID() != fx.archiveID {
		t.Fatalf("torn envelope: stale=%v kid=%x id=%x", c.EnvelopeStale(), c.KID(), c.ID())
	}
	if got := extract(t, c, info.ID); !bytes.Equal(got, text(30000, 70)) {
		t.Error("content through a torn envelope")
	}
	if err := c.RepairEnvelope(); err != nil || c.EnvelopeStale() {
		t.Fatalf("repair: %v, stale=%v", err, c.EnvelopeStale())
	}
	c.Close()
	env, err := ReadEnvelope(fx.path)
	if err != nil || env.KID != newKey.KID || env.ArchiveID != fx.archiveID {
		t.Fatalf("the envelope was not written again: %+v %v", env, err)
	}

	// A torn envelope and no key that opens the index: that is a corrupt
	// file, and it says so rather than blaming the key.
	tear()
	if _, err := Open(fx.path, []Key{{KID: rnd16(t), Key: rnd32(t)}}, opts); !errors.Is(err, format.ErrInvalid) {
		t.Fatalf("a torn envelope under a key that opens nothing: %v", err)
	}
}

func TestCompact(t *testing.T) {
	a, fx := newFixture(t, Options{})
	keep := map[string][]byte{"k1": text(40000, 60), "k2": noise(40000, 61), "k3": text(70000, 62)}
	drop := []FileInfo{add(t, a, root, "d1", noise(60000, 63)), add(t, a, root, "d2", noise(60000, 64))}
	ids := map[string][16]byte{}
	for name, data := range keep {
		ids[name] = add(t, a, root, name, data).ID
	}
	for _, d := range drop {
		if _, err := a.Delete(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
	}
	sizeBefore, _, freeBefore := a.Stat()
	if freeBefore == 0 {
		t.Fatal("nothing to compact")
	}
	// A reader blocks compaction.
	r, _ := a.OpenReader(ids["k1"])
	if _, _, err := a.Compact(ctx, nil); !errors.Is(err, ErrBusy) {
		t.Errorf("compact with a reader open: %v", err)
	}
	r.Close()
	var last uint64
	hash, size, err := a.Compact(ctx, func(done, total uint64) { last = done })
	if err != nil {
		t.Fatal(err)
	}
	if size >= sizeBefore || last == 0 {
		t.Errorf("compacted %d → %d, progress %d", sizeBefore, size, last)
	}
	if _, _, err := a.Add(ctx, root, "x", bytes.NewReader([]byte("x")), 1); !errors.Is(err, ErrClosed) {
		t.Errorf("handle after compact: %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Dir(fx.path)); len(entries) != 1 {
		t.Errorf("temp files left: %d entries", len(entries))
	}
	b := fx.open(t)
	if h, err := b.Hash(ctx); err != nil || h != hash {
		t.Errorf("hash: %x vs %x, %v", h, hash, err)
	}
	if s, files, free := b.Stat(); s != size || files != 3 || free != 0 {
		t.Errorf("after compact: size %d files %d free %d", s, files, free)
	}
	if len(b.index.Files) != 5 {
		t.Errorf("tombstones dropped: %d records", len(b.index.Files))
	}
	for name, data := range keep {
		if got := extract(t, b, ids[name]); !bytes.Equal(got, data) {
			t.Errorf("%s after compact", name)
		}
	}
	// Still writable and the tombstones' names are reusable.
	add(t, b, root, "d1", []byte("again"))
}

// Compaction copies a record chunk by chunk and looks for a cancel at every
// one, not only between files: one record can be many gigabytes, and a cancel
// that waits for the next file is not a cancel (the outside audit of
// 2026-09-09). Nothing is left behind: the original file is untouched and the
// half-written temporary is gone.
func TestCompactCancelsInsideOneFile(t *testing.T) {
	a, fx := newFixture(t, Options{NoCompression: true})
	data := noise(6<<20, 80) // several copies of the 1 MiB chunk buffer
	kept := add(t, a, root, "big", data)
	drop := add(t, a, root, "drop", noise(1<<20, 81))
	if _, err := a.Delete(ctx, drop.ID); err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var calls, last uint64
	if _, _, err := a.Compact(cctx, func(done, total uint64) {
		calls++
		last = done
		cancel() // the first chunk of the first file is enough
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancel inside one file: %v", err)
	}
	if calls != 1 {
		t.Fatalf("the copy went on for %d chunks (%d bytes) after the cancel", calls, last)
	}
	if entries, _ := os.ReadDir(filepath.Dir(fx.path)); len(entries) != 1 {
		t.Errorf("the cancelled compaction left its temporary: %d entries", len(entries))
	}
	a.Close()
	b := fx.open(t)
	if got := extract(t, b, kept.ID); !bytes.Equal(got, data) {
		t.Error("the file after a cancelled compaction")
	}
}

func TestDictionary(t *testing.T) {
	a, fx := newFixture(t, Options{})
	var samples [][]byte
	for i := 0; i < 100; i++ {
		samples = append(samples, []byte(fmt.Sprintf(`{"id":%d,"user":"person-%d","roles":["reader","writer"],"note":"record number %d"}`, i*7919%1000, i, i)))
	}
	dict, err := compress.BuildDict(samples, 42, compress.Default, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.SetDictionary(ctx, dict); err != nil {
		t.Fatal(err)
	}
	small := add(t, a, root, "small.json", samples[3])
	large := add(t, a, root, "large.txt", text(400<<10, 70))
	if small.Storage != format.StorageZstdDict || large.Storage != format.StorageZstd {
		t.Fatalf("storage %v %v", small.Storage, large.Storage)
	}
	if _, err := a.SetDictionary(ctx, nil); !errors.Is(err, ErrDictInUse) {
		t.Errorf("clear while in use: %v", err)
	}
	if _, err := a.SetDictionary(ctx, []byte("junk")); !errors.Is(err, ErrDictInUse) {
		t.Errorf("replace while in use: %v", err)
	}
	if got := extract(t, a, small.ID); !bytes.Equal(got, samples[3]) {
		t.Error("dictionary file content")
	}
	a.Close()
	b := fx.open(t)
	if got := extract(t, b, small.ID); !bytes.Equal(got, samples[3]) {
		t.Error("dictionary file content after reopen")
	}
	if _, err := b.Delete(ctx, small.ID); err != nil {
		t.Fatal(err)
	}
	// With the referencing file gone (its tombstone drops the reference),
	// the dictionary can be cleared; junk is refused.
	if _, err := b.SetDictionary(ctx, []byte("junk")); !errors.Is(err, compress.ErrParams) {
		t.Errorf("junk dictionary: %v", err)
	}
	if _, err := b.SetDictionary(ctx, nil); err != nil {
		t.Errorf("clear: %v", err)
	}
	if got := extract(t, b, large.ID); !bytes.Equal(got, text(400<<10, 70)) {
		t.Error("plain zstd file after clearing the dictionary")
	}
}

func TestFreeMapDamage(t *testing.T) {
	a, fx := newFixture(t, Options{})
	info := add(t, a, root, "f", text(30000, 80))
	if _, err := a.Delete(ctx, add(t, a, root, "g", noise(30000, 81)).ID); err != nil {
		t.Fatal(err)
	}
	a.Close()
	b := snapshot(t, fx.path)
	sbA, _ := format.DecodeArchiveSuperblock(b[format.CopyA.ArchiveSuperblockOff() : format.CopyA.ArchiveSuperblockOff()+format.SuperblockSize])
	sbB, _ := format.DecodeArchiveSuperblock(b[format.CopyB.ArchiveSuperblockOff() : format.CopyB.ArchiveSuperblockOff()+format.SuperblockSize])
	sb := sbA
	if sbB.Seq > sbA.Seq {
		sb = sbB
	}
	b[sb.FreeMapOff] ^= 1
	restore(t, fx.path, b)
	c := fx.open(t)
	if c.FreeMapRebuilt() == nil {
		t.Fatal("damaged free map not reported")
	}
	if got := extract(t, c, info.ID); !bytes.Equal(got, text(30000, 80)) {
		t.Error("content with a rebuilt map")
	}
	if _, _, free := c.Stat(); free == 0 {
		t.Error("rebuilt map found no free space")
	}
	add(t, c, root, "h", noise(1000, 82)) // writes the rebuilt map
	c.Close()
	d := fx.open(t)
	if d.FreeMapRebuilt() != nil {
		t.Errorf("map still reported damaged after a commit: %v", d.FreeMapRebuilt())
	}
}

// TestHostileMetadata builds files whose checksums are valid but whose free
// map or losing superblock lie: Open must neither panic nor take
// quadratic time, and must never hand out a live extent.
func TestHostileMetadata(t *testing.T) {
	a, fx := newFixture(t, Options{})
	info := add(t, a, root, "f", text(30000, 91))
	if _, err := a.Delete(ctx, add(t, a, root, "g", noise(30000, 92)).ID); err != nil {
		t.Fatal(err)
	}
	a.Close()
	good := snapshot(t, fx.path)
	sbAt := func(b []byte, c format.Copy) *format.ArchiveSuperblock {
		off := c.ArchiveSuperblockOff()
		sb, err := format.DecodeArchiveSuperblock(b[off : off+format.SuperblockSize])
		if err != nil {
			t.Fatal(err)
		}
		return sb
	}
	liveCopy := format.CopyA
	if sbAt(good, format.CopyB).Seq > sbAt(good, format.CopyA).Seq {
		liveCopy = format.CopyB
	}
	live := sbAt(good, liveCopy)

	// A free map that is checksummed correctly but wrong: overlapping a live
	// file, past the end, or thousands of interleaved one-byte extents.
	withMap := func(b []byte, m *format.FreeMap) []byte {
		b = bytes.Clone(b)
		enc, hash, err := m.Hash()
		if err != nil {
			t.Fatal(err)
		}
		sb := *live
		// Put the new map at the end so its extent is valid.
		sb.FreeMapOff, sb.FreeMapLen, sb.FreeMapHash = uint64(len(b)), uint64(len(enc)), hash
		b = append(b, enc...)
		encSB, err := sb.Encode()
		if err != nil {
			t.Fatal(err)
		}
		// The forgery edits the live copy where it stands, at its own seq.
		// It cannot be published as a commit that never happened — a
		// superblock at seq + 1 names an index sealed at seq, and the seq is
		// in the index AAD (§11), so such a copy opens nothing. The free map
		// is the one thing a file with write access can still lie about,
		// which is what this test is about.
		off := liveCopy.ArchiveSuperblockOff()
		copy(b[off:], encSB)
		return b
	}
	liveRec := a.index.Files[0]
	cases := map[string]*format.FreeMap{
		"overlaps a live file": {Extents: []extent{{Off: liveRec.DataOff + 10, Len: 100}}},
		"past the end":         {Extents: []extent{{Off: uint64(len(good)) + 4096, Len: 100}}},
	}
	var many []extent
	for off := uint64(format.ArchiveDataStart); off < uint64(len(good)); off += 2 {
		many = append(many, extent{Off: off, Len: 1})
	}
	cases["interleaved"] = &format.FreeMap{Extents: many}
	for name, m := range cases {
		restore(t, fx.path, withMap(good, m))
		b, err := Open(fx.path, []Key{fx.key}, fx.opts)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		// Whatever the map said, the live file is intact and readable, and
		// space allocated afterwards never overlaps it.
		if got := extract(t, b, info.ID); !bytes.Equal(got, text(30000, 91)) {
			t.Errorf("%s: content", name)
		}
		if name != "interleaved" && b.FreeMapRebuilt() == nil {
			t.Errorf("%s: lying map not reported", name)
		}
		n := add(t, b, root, "n", noise(20000, 93))
		if nr := b.record(n.ID); overlaps(extent{Off: nr.DataOff, Len: nr.StoredSize}, extent{Off: liveRec.DataOff, Len: liveRec.StoredSize}) {
			t.Errorf("%s: allocation over a live file", name)
		}
		b.Close()
	}

	// A losing superblock whose free-map extent overlaps a freed data
	// extent: quarantine construction must tolerate the overlap.
	restore(t, fx.path, good)
	b := bytes.Clone(good)
	loser := sbAt(b, liveCopy.Other())
	freed := extent{Off: format.ArchiveDataStart, Len: 100} // somewhere in the freed region or not; must not matter
	loser.FreeMapOff, loser.FreeMapLen = freed.Off, freed.Len
	encL, err := loser.Encode()
	if err != nil {
		t.Fatal(err)
	}
	copy(b[liveCopy.Other().ArchiveSuperblockOff():], encL)
	restore(t, fx.path, b)
	c, err := Open(fx.path, []Key{fx.key}, fx.opts)
	if err != nil {
		t.Fatalf("hostile loser: %v", err)
	}
	if got := extract(t, c, info.ID); !bytes.Equal(got, text(30000, 91)) {
		t.Error("hostile loser: content")
	}
	c.Close()
	restore(t, fx.path, good)
}

// TestConcurrentReadersAndWrites runs a transaction against readers on
// other goroutines; the race detector, when on, is the assertion.
func TestConcurrentReadersAndWrites(t *testing.T) {
	a, _ := newFixture(t, Options{InMemoryBelow: 1 << 10})
	base := add(t, a, root, "base", text(200000, 95))
	done := make(chan struct{})
	errs := make(chan error, 64)
	go func() {
		defer close(done)
		for i := 0; i < 30; i++ {
			r, err := a.OpenReader(base.ID)
			if err != nil {
				errs <- err
				return
			}
			if _, err := io.CopyN(io.Discard, r, 5000); err != nil {
				errs <- err
			}
			a.Stat()
			a.Files()
			r.Close()
		}
	}()
	for i := 0; i < 6; i++ {
		add(t, a, root, fmt.Sprintf("w%d", i), text(300000, uint64(96+i)))
		if _, _, err := a.Replace(ctx, base.ID, bytes.NewReader(text(200000, 95)), 200000); err != nil {
			t.Fatal(err)
		}
	}
	<-done
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if _, err := a.Hash(ctx); err != nil {
		t.Error(err)
	}
}

func TestOpenRefusesDamage(t *testing.T) {
	a, fx := newFixture(t, Options{})
	info := add(t, a, root, "f", text(30000, 90))
	a.Close()
	good := snapshot(t, fx.path)
	rec := func() (*format.ArchiveSuperblock, uint64) {
		sbA, _ := format.DecodeArchiveSuperblock(good[format.CopyA.ArchiveSuperblockOff() : format.CopyA.ArchiveSuperblockOff()+format.SuperblockSize])
		sbB, _ := format.DecodeArchiveSuperblock(good[format.CopyB.ArchiveSuperblockOff() : format.CopyB.ArchiveSuperblockOff()+format.SuperblockSize])
		if sbB.Seq > sbA.Seq {
			return sbB, format.CopyB.ArchiveSuperblockOff()
		}
		return sbA, format.CopyA.ArchiveSuperblockOff()
	}
	sb, _ := rec()
	cases := map[string]func([]byte) []byte{
		"truncated to the fixed regions": func(b []byte) []byte { return b[:format.ArchiveDataStart] },
		"truncated inside the index":     func(b []byte) []byte { return b[:sb.IndexOff+sb.IndexLen/2] },
		"garbage":                        func(b []byte) []byte { g := make([]byte, len(b)); rand.Read(g); return g },
		"index tag mismatch":             func(b []byte) []byte { b[sb.IndexOff+sb.IndexLen] ^= 1; return b },
		"envelope damaged":               func(b []byte) []byte { b[format.EnvelopeOff+100] ^= 1; return b },
	}
	for name, mutate := range cases {
		restore(t, fx.path, mutate(bytes.Clone(good)))
		if _, err := Open(fx.path, []Key{fx.key}, fx.opts); !errors.Is(err, format.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Index ciphertext damage is a key failure, not a panic.
	b := bytes.Clone(good)
	b[sb.IndexOff+sb.IndexLen/2] ^= 1
	restore(t, fx.path, b)
	if _, err := Open(fx.path, []Key{fx.key}, fx.opts); !errors.Is(err, ErrKey) {
		t.Errorf("index damage: %v", err)
	}
	// Data damage is caught by the chunk AEAD at read time, and Extract
	// reports it rather than a hash mismatch.
	b = bytes.Clone(good)
	b[format.ArchiveDataStart+100] ^= 1
	restore(t, fx.path, b)
	c := fx.open(t)
	if err := c.Extract(ctx, info.ID, io.Discard); err == nil || errors.Is(err, ErrContentHash) {
		t.Errorf("data damage: %v", err)
	}
	c.Close()
	restore(t, fx.path, good)
}

func TestPaddingAndNoCompression(t *testing.T) {
	a, _ := newFixture(t, Options{NoCompression: true})
	info := add(t, a, root, "t", text(50000, 100))
	if info.Storage != format.StorageRaw {
		t.Errorf("NoCompression stored %v", info.Storage)
	}
	a.Close()
	b, _ := newFixture(t, Options{Compress: compress.Params{Level: compress.Fastest, Padding: 4096}})
	info = add(t, b, root, "t", text(50000, 101))
	if info.Storage != format.StorageZstd {
		t.Errorf("padded stored %v", info.Storage)
	}
	if got := extract(t, b, info.ID); !bytes.Equal(got, text(50000, 101)) {
		t.Error("padded content")
	}
}

func TestContextCancel(t *testing.T) {
	a, _ := newFixture(t, Options{})
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := a.Add(cctx, root, "x", bytes.NewReader(text(100000, 110)), 100000); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled add: %v", err)
	}
	if len(a.Files()) != 0 {
		t.Error("cancelled add left a record")
	}
	info := add(t, a, root, "y", text(100000, 111))
	if err := a.Extract(cctx, info.ID, io.Discard); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled extract: %v", err)
	}
	if _, err := a.Hash(cctx); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled hash: %v", err)
	}
}

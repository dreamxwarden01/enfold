package archive

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/dreamxwarden01/enfold/internal/format"
)

// The tail comes back (FORMAT.md R31 as amended on 2026-09-09, APP.md §2.3,
// DECISIONS 2026-09-09 "Space comes back"): a commit that leaves a free run
// at the end of the file is followed at once by an empty commit, after which
// the file is truncated — so an archive does not stay the size of what it
// held. Every step is proved against the file itself, read back through Open,
// and against R31's own promise: a torn live superblock still opens a state
// whose every file reads.

// metadataSlack is what a file may keep over the size it had before an add
// that was then deleted: the index carries a tombstone the file did not have,
// and the follow-up commit's own index and free map lie at the end.
const metadataSlack = 4096

// The crash seam of trim.go, armed for one write of the follow-up commit.
// The steps are the ones the order in trim.go numbers: 1 the retirement, 2
// the relocated index and free map — two writes, so nth tells them apart —
// 3 the flip, 4 the other copy.

// landing is what the seam handed the disk: the offset of the write it tore
// and the prefix of that write it let through. A step whose torn bytes go
// where no superblock points leaves no other trace, so this is what tells a
// torn write from one that never began.
type landing struct {
	off uint64
	b   []byte
}

// on asserts that the file holds what the seam landed, and nothing of the
// write beyond it is claimed. Call it before any further commit, which would
// be free to write over those bytes.
func (l *landing) on(t testing.TB, path string) {
	t.Helper()
	if len(l.b) == 0 {
		t.Fatal("the seam tore no write")
	}
	img := snapshot(t, path)
	if uint64(len(img)) < l.off+uint64(len(l.b)) {
		t.Fatalf("the seam landed %d bytes at %d; the file is %d bytes", len(l.b), l.off, len(img))
	}
	if !bytes.Equal(img[l.off:l.off+uint64(len(l.b))], l.b) {
		t.Errorf("the %d bytes the seam handed the disk at %d are not what the file holds", len(l.b), l.off)
	}
}

// skipTrimAt stops the step's nth write before its first byte: the crash that
// caught it before it began, which leaves that step simply undone.
func skipTrimAt(step int) trimSeam {
	return armTrim(step, 1, 0, nil)
}

// tearTrimAt lands the first half of the step's nth write and then fails it:
// 2048 bytes of a 4096-byte superblock, which leaves the checksum of whatever
// stood there before and so a copy that does not decode. got, when it is not
// nil, is filled with the bytes that reached the file and where.
func tearTrimAt(step, nth int, got *landing) trimSeam {
	return armTrim(step, nth, -1, got)
}

// trimSeam is failTrimAt's type, written out because the table below names it.
type trimSeam = func(step int, b []byte, off uint64) (int, bool)

// armTrim builds the seam. tear is the byte count that reaches the file; -1
// asks for half the write, whatever its size.
func armTrim(step, nth, tear int, got *landing) trimSeam {
	seen := 0
	return func(s int, b []byte, off uint64) (int, bool) {
		if s != step {
			return 0, false
		}
		if seen++; seen != nth {
			return 0, false
		}
		n := tear
		if n < 0 {
			n = len(b) / 2
		}
		if n > len(b) {
			n = len(b)
		}
		if got != nil {
			*got = landing{off: off, b: append([]byte(nil), b[:n]...)}
		}
		return n, true
	}
}

func sizeOnDisk(t testing.TB, path string) uint64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return uint64(fi.Size())
}

// reread hands back what the file holds now — the state a reader would find,
// not the one the writer believes in — by opening a copy of its bytes. One
// handle per path per process (doc.go "Handles") is why it is a copy: the
// writer under test still holds the original, and a second handle beside it
// is exactly what the archive layer refuses.
func reread(t testing.TB, fx *fixture) *Archive {
	t.Helper()
	path := filepath.Join(t.TempDir(), "copy.efd")
	restore(t, path, snapshot(t, fx.path))
	opts := fx.opts
	opts.ReadOnly = true
	a, err := Open(path, []Key{fx.key}, opts)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { a.Close() })
	return a
}

// insideTheFile is R31's floor, read off the file itself: every superblock
// copy that still decodes names extents that lie inside the file, at least
// one does decode, and the live state's own records overlap nothing.
func insideTheFile(t testing.TB, a *Archive) {
	t.Helper()
	size := sizeOnDisk(t, a.path)
	img := snapshot(t, a.path)
	decoded := 0
	for _, c := range []format.Copy{format.CopyA, format.CopyB} {
		sb, err := format.DecodeArchiveSuperblock(img[c.ArchiveSuperblockOff() : c.ArchiveSuperblockOff()+format.SuperblockSize])
		if err != nil {
			continue // the torn copy, which is what the other one is for
		}
		decoded++
		if err := sb.ValidateExtents(size); err != nil {
			t.Errorf("superblock copy %d points past the end of the %d-byte file: %v", c, size, err)
		}
	}
	if decoded == 0 {
		t.Fatal("neither superblock copy decodes")
	}
	if _, err := liveExtents(a.index, a.sb, size); err != nil {
		t.Errorf("the live state: %v", err)
	}
}

// disjoint is what "nothing was freed twice" means on the file: the published
// free map decodes under the hash the superblock carries, its extents are
// sorted, non-empty and disjoint, they lie inside the file, and none of them
// overlaps anything the live state references — the index, the map itself, or
// a live file's data.
func disjoint(t testing.TB, a *Archive) {
	t.Helper()
	size := sizeOnDisk(t, a.path)
	img := snapshot(t, a.path)
	sb := a.sb
	m, err := format.DecodeFreeMapChecked(img[sb.FreeMapOff:sb.FreeMapOff+sb.FreeMapLen], sb.FreeMapHash)
	if err != nil {
		t.Fatalf("the published free map: %v", err)
	}
	live, err := liveExtents(a.index, sb, size)
	if err != nil {
		t.Fatalf("the live state: %v", err)
	}
	used := append([]extent{
		{Off: sb.IndexOff, Len: sb.IndexLen + format.TagSize},
		{Off: sb.FreeMapOff, Len: sb.FreeMapLen},
	}, live...)
	var prev extent
	for i, e := range m.Extents {
		if e.Len == 0 {
			t.Errorf("free extent %d is empty", i)
		}
		if i > 0 && e.Off < end(prev) {
			t.Errorf("free extents %d and %d overlap or repeat: %+v %+v", i-1, i, prev, e)
		}
		if end(e) > size {
			t.Errorf("free extent %+v lies past the end of the %d-byte file", e, size)
		}
		for _, u := range used {
			if overlaps(e, u) {
				t.Errorf("free extent %+v overlaps the live extent %+v", e, u)
			}
		}
		prev = e
	}
}

// liveNames is what a state holds, for comparing one against another.
func liveNames(t testing.TB, a *Archive) []string {
	t.Helper()
	var out []string
	for _, f := range a.Files() {
		out = append(out, pathOf(t, a, f.ID))
	}
	return out
}

func TestTheTailComesBack(t *testing.T) {
	a, fx := newFixture(t, Options{})
	keepData := noise(20000, 300)
	keep := add(t, a, root, "keep.bin", keepData)
	before, _, _ := a.Stat()
	if s := sizeOnDisk(t, fx.path); s != before {
		t.Fatalf("the archive says %d bytes, the file is %d", before, s)
	}

	lastData := noise(200000, 301)
	seq := a.Seq()
	last := add(t, a, root, "last.bin", lastData)
	grown, _, _ := a.Stat()
	if grown < before+uint64(len(lastData)) {
		t.Fatalf("the add did not grow the file: %d → %d", before, grown)
	}
	if a.Seq() != seq+1 {
		t.Fatalf("an add that freed no tail took %d commits", a.Seq()-seq)
	}
	b := reread(t, fx)
	if got := extract(t, b, last.ID); !bytes.Equal(got, lastData) {
		t.Error("the added file read back from the file")
	}
	b.Close()

	// The delete: one commit for the tombstone, one for the tail.
	seq = a.Seq()
	if _, err := a.Delete(ctx, last.ID); err != nil {
		t.Fatal(err)
	}
	if a.Seq() != seq+2 {
		t.Errorf("the delete and its follow-up commit advanced the sequence by %d", a.Seq()-seq)
	}
	after, _, _ := a.Stat()
	if after > before+metadataSlack {
		t.Errorf("the file stood at %d after the delete; it was %d before the add", after, before)
	}
	if s := sizeOnDisk(t, fx.path); s != after {
		t.Errorf("the archive says %d bytes, the file is %d", after, s)
	}
	c := reread(t, fx)
	if got := extract(t, c, keep.ID); !bytes.Equal(got, keepData) {
		t.Error("the file that stayed, after the tail came back")
	}
	if names := liveNames(t, c); len(names) != 1 || names[0] != "keep.bin" {
		t.Errorf("live records after the delete: %v", names)
	}
	if c.Stale() != nil || c.FreeMapRebuilt() != nil {
		t.Errorf("the truncated file opened with damage: stale=%v map=%v", c.Stale(), c.FreeMapRebuilt())
	}
	c.Close()
	a.Close()

	// R31's fallback over the truncated file: the newest superblock damaged,
	// and the copy behind it — which the follow-up commit brought onto the
	// same state, since nothing it names may lie past the new end — still
	// reads every file of it.
	img := snapshot(t, fx.path)
	sbA, err := format.DecodeArchiveSuperblock(img[format.CopyA.ArchiveSuperblockOff() : format.CopyA.ArchiveSuperblockOff()+format.SuperblockSize])
	if err != nil {
		t.Fatal(err)
	}
	sbB, err := format.DecodeArchiveSuperblock(img[format.CopyB.ArchiveSuperblockOff() : format.CopyB.ArchiveSuperblockOff()+format.SuperblockSize])
	if err != nil {
		t.Fatal(err)
	}
	liveOff, loser := format.CopyA.ArchiveSuperblockOff(), sbB
	if sbB.Seq > sbA.Seq {
		liveOff, loser = format.CopyB.ArchiveSuperblockOff(), sbA
	}
	if end(extent{Off: loser.IndexOff, Len: loser.IndexLen + format.TagSize}) > after ||
		end(extent{Off: loser.FreeMapOff, Len: loser.FreeMapLen}) > after {
		t.Fatalf("the losing copy points past the end of the %d-byte file: %+v", after, loser)
	}
	img[liveOff+200] ^= 1
	restore(t, fx.path, img)
	d := fx.open(t)
	if d.Stale() == nil {
		t.Error("the damaged live copy was not reported")
	}
	if got := extract(t, d, keep.ID); !bytes.Equal(got, keepData) {
		t.Error("the fallback state does not read every file")
	}
}

// A hole in the middle is not a tail: nothing is truncated over live data,
// the freed extent stays in the map, and Compact is what gives it back.
func TestADeleteInTheMiddleLeavesItsHole(t *testing.T) {
	a, _ := newFixture(t, Options{})
	midData := noise(120000, 310)
	mid := add(t, a, root, "middle.bin", midData)
	tailData := noise(20000, 311)
	tail := add(t, a, root, "tail.bin", tailData)
	midExt := extent{Off: a.record(mid.ID).DataOff, Len: mid.StoredSize}
	before, _, freeBefore := a.Stat()

	if _, err := a.Delete(ctx, mid.ID); err != nil {
		t.Fatal(err)
	}
	after, _, free := a.Stat()
	if after < end(midExt) {
		t.Errorf("the file was cut to %d, over a hole that ends at %d", after, end(midExt))
	}
	if after > before {
		t.Errorf("the file grew over a delete: %d → %d", before, after)
	}
	if free < freeBefore+mid.StoredSize {
		t.Errorf("free went %d → %d, expected at least %d more", freeBefore, free, mid.StoredSize)
	}
	if got := extract(t, a, tail.ID); !bytes.Equal(got, tailData) {
		t.Error("the file above the hole")
	}
}

// Two files deleted in one commit take the whole run they hold at the end.
func TestDeletingTheLastTwoFilesShrinksPastBoth(t *testing.T) {
	a, fx := newFixture(t, Options{})
	firstData := text(20000, 320)
	first := add(t, a, root, "first.bin", firstData)
	base, _, _ := a.Stat()
	second := add(t, a, root, "second.bin", noise(150000, 321))
	third := add(t, a, root, "third.bin", noise(150000, 322))

	tx, err := a.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Delete(second.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Delete(third.ID); err != nil {
		t.Fatal(err)
	}
	rec, err := tx.Commit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	after, _, _ := a.Stat()
	if after > base+metadataSlack {
		t.Errorf("the file stood at %d after both deletes; one file alone made it %d", after, base)
	}
	// The receipt is the one that follows the truncation, so the registry
	// records the state the file is actually in.
	if rec.Seq != a.Seq() || rec.Size != after {
		t.Errorf("receipt %+v, archive at seq %d size %d", rec, a.Seq(), after)
	}
	b := reread(t, fx)
	if got := extract(t, b, first.ID); !bytes.Equal(got, firstData) {
		t.Error("the file that stayed")
	}
	if names := liveNames(t, b); len(names) != 1 {
		t.Errorf("live records: %v", names)
	}
}

// The follow-up commit's own flip fails — the disk gone between the two
// commits. The first commit stands, whole and consistent, the file keeps its
// tail, and the next commit gives it back.
func TestAFailedFollowUpCommitLeavesTheFirstCommitStanding(t *testing.T) {
	a, fx := newFixture(t, Options{})
	keepData := text(20000, 330)
	keep := add(t, a, root, "keep.bin", keepData)
	before, _, _ := a.Stat()
	last := add(t, a, root, "last.bin", noise(200000, 331))
	grown, _, _ := a.Stat()
	seq := a.Seq()

	failTrimAt = skipTrimAt(3)
	_, err := a.Delete(ctx, last.ID)
	failTrimAt = nil
	if err != nil {
		t.Fatal(err)
	}
	if a.Seq() != seq+1 {
		t.Errorf("the follow-up commit landed after all: the sequence advanced by %d", a.Seq()-seq)
	}
	if s, _, _ := a.Stat(); s < grown {
		t.Errorf("a follow-up commit that failed truncated the file: %d → %d", grown, s)
	}
	a.Close()

	b := fx.open(t)
	if b.Stale() != nil || b.FreeMapRebuilt() != nil {
		t.Errorf("reopened with damage: stale=%v map=%v", b.Stale(), b.FreeMapRebuilt())
	}
	if names := liveNames(t, b); len(names) != 1 || names[0] != "keep.bin" {
		t.Fatalf("the first commit's state: %v", names)
	}
	if got := extract(t, b, keep.ID); !bytes.Equal(got, keepData) {
		t.Error("the file that stayed")
	}
	// The next commit finds the tail and tries again.
	if _, err := b.Rename(ctx, keep.ID, "kept.bin"); err != nil {
		t.Fatal(err)
	}
	after, _, _ := b.Stat()
	if after > before+metadataSlack {
		t.Errorf("the next commit did not reclaim the tail: %d, the file was %d before the add", after, before)
	}
	if s := sizeOnDisk(t, fx.path); s != after {
		t.Errorf("the archive says %d bytes, the file is %d", after, s)
	}
	if got := extract(t, b, keep.ID); !bytes.Equal(got, keepData) {
		t.Error("the file that stayed, after the tail came back")
	}
}

// The other half of the crash: the flip landed and the write that brings the
// other copy onto the same state did not. The follow-up commit is durable, so
// the sequence advanced by two — but nothing may be truncated while a copy
// still names the old index and free map, so the tail stays, as free space,
// until a later commit gives it back.
func TestACrashSettlingTheOtherCopyKeepsTheTail(t *testing.T) {
	a, fx := newFixture(t, Options{})
	keepData := text(20000, 340)
	keep := add(t, a, root, "keep.bin", keepData)
	before, _, _ := a.Stat()
	last := add(t, a, root, "last.bin", noise(200000, 341))
	grown, _, freeBefore := a.Stat()
	seq := a.Seq()

	failTrimAt = skipTrimAt(4)
	_, err := a.Delete(ctx, last.ID)
	failTrimAt = nil
	if err != nil {
		t.Fatal(err)
	}
	if a.Seq() != seq+2 {
		t.Errorf("the follow-up commit did not land: the sequence advanced by %d", a.Seq()-seq)
	}
	size, _, free := a.Stat()
	if size < grown {
		t.Errorf("the file was cut with a copy still naming the old state: %d → %d", grown, size)
	}
	if s := sizeOnDisk(t, fx.path); s != size {
		t.Errorf("the archive says %d bytes, the file is %d", size, s)
	}
	if free < freeBefore+150000 {
		t.Errorf("the tail that was not truncated is not free either: %d → %d", freeBefore, free)
	}
	a.Close()

	// Both copies decode — the losing one at the state the first commit
	// published, the live one at the follow-up's — and neither points past
	// the end, which is exactly what not truncating bought.
	img := snapshot(t, fx.path)
	for _, c := range []format.Copy{format.CopyA, format.CopyB} {
		sb, err := format.DecodeArchiveSuperblock(img[c.ArchiveSuperblockOff() : c.ArchiveSuperblockOff()+format.SuperblockSize])
		if err != nil {
			t.Errorf("superblock copy %d does not decode: %v", c, err)
			continue
		}
		if err := sb.ValidateExtents(size); err != nil {
			t.Errorf("superblock copy %d points past the end of the %d-byte file: %v", c, size, err)
		}
	}
	b := fx.open(t)
	if b.Stale() != nil || b.FreeMapRebuilt() != nil {
		t.Errorf("reopened with damage: stale=%v map=%v", b.Stale(), b.FreeMapRebuilt())
	}
	if names := liveNames(t, b); len(names) != 1 || names[0] != "keep.bin" {
		t.Fatalf("the follow-up commit's state: %v", names)
	}
	if got := extract(t, b, keep.ID); !bytes.Equal(got, keepData) {
		t.Error("the file that stayed")
	}
	// The next commit finds the tail and gives it back.
	if _, err := b.Rename(ctx, keep.ID, "kept.bin"); err != nil {
		t.Fatal(err)
	}
	after, _, _ := b.Stat()
	if after > before+metadataSlack {
		t.Errorf("the next commit did not reclaim the tail: %d, the file was %d before the add", after, before)
	}
	if s := sizeOnDisk(t, fx.path); s != after {
		t.Errorf("the archive says %d bytes, the file is %d", after, s)
	}
	if got := extract(t, b, keep.ID); !bytes.Equal(got, keepData) {
		t.Error("the file that stayed, after the tail came back")
	}
}

// R31's "nor into one an open reader still holds", at the truncation: a
// Reader goes on reading the content it opened after the file is deleted, so
// the follow-up commit may not cut the file back over its extent. The tail
// comes back with the first commit after the Reader closes.
func TestAnOpenReaderHoldsTheTail(t *testing.T) {
	a, fx := newFixture(t, Options{})
	keepData := text(20000, 350)
	keep := add(t, a, root, "keep.bin", keepData)
	before, _, _ := a.Stat()
	lastData := noise(200000, 351)
	last := add(t, a, root, "last.bin", lastData)
	grown, _, _ := a.Stat()

	r, err := a.OpenReader(last.ID)
	if err != nil {
		t.Fatal(err)
	}
	held := r.e
	if end(held) > grown {
		t.Fatalf("the reader's extent %+v lies past the end of the %d-byte file", held, grown)
	}
	if _, err := a.Delete(ctx, last.ID); err != nil {
		t.Fatal(err)
	}
	size, _, _ := a.Stat()
	if size < end(held) {
		t.Errorf("the file was cut to %d, over an extent an open reader holds that ends at %d", size, end(held))
	}
	if s := sizeOnDisk(t, fx.path); s != size {
		t.Errorf("the archive says %d bytes, the file is %d", size, s)
	}
	got, err := io.ReadAll(r)
	if err != nil || !bytes.Equal(got, lastData) {
		t.Fatalf("the deleted file's reader, after the commit that freed it: %v", err)
	}
	r.Close()

	// Nothing holds the tail now, and the next commit gives it back.
	if _, err := a.Rename(ctx, keep.ID, "kept.bin"); err != nil {
		t.Fatal(err)
	}
	after, _, _ := a.Stat()
	if after > before+metadataSlack {
		t.Errorf("the tail did not come back once the reader closed: %d, the file was %d before the add", after, before)
	}
	if s := sizeOnDisk(t, fx.path); s != after {
		t.Errorf("the archive says %d bytes, the file is %d", after, s)
	}
	if got := extract(t, a, keep.ID); !bytes.Equal(got, keepData) {
		t.Error("the file that stayed")
	}
}

// A torn write inside the follow-up commit (the outside audit of 2026-09-09:
// the seam skipped writes rather than exercising torn ones). Each of the four
// steps in turn lands the first half of its write and then fails — 2048 bytes
// of a 4096-byte superblock, which leaves behind the checksum of what stood
// there and so a copy that does not decode. What the file holds afterwards is
// R31's promise either way: one of the two states, complete, every file of it
// readable, nothing referenced past the end of the file. The next commit on
// the reopened handle reclaims the tail and frees nothing twice.
//
// That the write tore rather than never began is read off the file itself:
// the seam records what it handed the disk and the test finds those bytes at
// that offset. Step 2's two writes need it — they go where the first commit's
// state holds free space and no superblock points, so a half-written index or
// free map is otherwise indistinguishable from one that was skipped.
func TestATornWriteInTheFollowUpCommit(t *testing.T) {
	for _, tc := range []struct {
		what  string
		arm   func(*landing) trimSeam
		state string // the state R31 leaves the file on
		steps uint64 // how far the sequence moved
		stale bool   // a superblock copy left torn
		indet bool   // the commit reported an outcome it cannot know
	}{
		// The retirement is the losing copy's own write: torn, it is the copy
		// that may be lost, and the live one still names the first commit.
		{"the retirement", func(got *landing) trimSeam { return tearTrimAt(1, 1, got) }, "the first commit's", 1, true, false},
		// The relocated index and free map go where the first commit's state
		// holds free space: torn, they are bytes no superblock names.
		{"the relocated index", func(got *landing) trimSeam { return tearTrimAt(2, 1, got) }, "the first commit's", 1, false, false},
		{"the relocated free map", func(got *landing) trimSeam { return tearTrimAt(2, 2, got) }, "the first commit's", 1, false, false},
		// The flip is the commit point: a write that began and failed leaves an
		// outcome the writer cannot know, and the file on the state the intact
		// copy names.
		{"the flip", func(got *landing) trimSeam { return tearTrimAt(3, 1, got) }, "the first commit's", 1, true, true},
		// After the flip the follow-up commit is durable; the settlement is
		// what the truncation waits for, and a torn one keeps the tail.
		{"the other copy", func(got *landing) trimSeam { return tearTrimAt(4, 1, got) }, "the follow-up commit's", 2, true, false},
	} {
		t.Run(tc.what, func(t *testing.T) {
			a, fx := newFixture(t, Options{})
			keepData := text(20000, 360)
			keep := add(t, a, root, "keep.bin", keepData)
			before, _, _ := a.Stat()
			last := add(t, a, root, "last.bin", noise(200000, 361))
			grown, _, _ := a.Stat()
			seq := a.Seq()

			var tore landing
			failTrimAt = tc.arm(&tore)
			_, err := a.Delete(ctx, last.ID)
			failTrimAt = nil
			switch {
			case tc.indet:
				if !errors.Is(err, ErrIndeterminate) || a.Broken() == nil {
					t.Fatalf("a torn commit point: err=%v broken=%v", err, a.Broken())
				}
			case err != nil:
				t.Fatal(err)
			}
			if s := sizeOnDisk(t, fx.path); s < grown {
				t.Errorf("the file was cut to %d over a follow-up commit that tore; it was %d", s, grown)
			}
			a.Close()

			// The write tore: the prefix the seam handed the disk is on the
			// file, and the rest of the write is not. Read before any further
			// commit, which is free to write over those bytes.
			tore.on(t, fx.path)

			// The file, reopened. Which of the two states it landed on is what
			// the sequence says (R36, the keyless identity of a state).
			b := fx.open(t)
			if b.Seq() != seq+tc.steps {
				t.Errorf("the file is at seq %d; %s state is seq %d", b.Seq(), tc.state, seq+tc.steps)
			}
			if (b.Stale() != nil) != tc.stale {
				t.Errorf("a torn superblock copy: stale=%v, expected %v", b.Stale(), tc.stale)
			}
			if b.FreeMapRebuilt() != nil {
				t.Errorf("the free map did not survive the torn write: %v", b.FreeMapRebuilt())
			}
			if names := liveNames(t, b); len(names) != 1 || names[0] != "keep.bin" {
				t.Fatalf("%s state holds %v", tc.state, names)
			}
			if got := extract(t, b, keep.ID); !bytes.Equal(got, keepData) {
				t.Error("the file that stayed does not read")
			}
			insideTheFile(t, b)
			disjoint(t, b)

			// The next commit on that handle finds the tail and gives it back.
			if _, err := b.Rename(ctx, keep.ID, "kept.bin"); err != nil {
				t.Fatal(err)
			}
			after, _, _ := b.Stat()
			if after > before+metadataSlack {
				t.Errorf("the next commit did not reclaim the tail: %d, the file was %d before the add", after, before)
			}
			if s := sizeOnDisk(t, fx.path); s != after {
				t.Errorf("the archive says %d bytes, the file is %d", after, s)
			}
			insideTheFile(t, b)
			disjoint(t, b)

			// And nothing was freed twice: an add lands in space the index
			// references nowhere, and both files read.
			addedData := noise(30000, 362)
			added := add(t, b, root, "added.bin", addedData)
			insideTheFile(t, b)
			disjoint(t, b)
			if got := extract(t, b, added.ID); !bytes.Equal(got, addedData) {
				t.Error("the file added after the reclaim")
			}
			if got := extract(t, b, keep.ID); !bytes.Equal(got, keepData) {
				t.Error("the file that stayed, after the add")
			}
		})
	}
}

// An interrupted follow-up commit, retried on the same handle (the outside
// audit of 2026-09-09: the tests reopened the file before retrying, so nothing
// proved the handle's own view of the file survived the interruption). The
// commit the caller asked for is durable and the Archive usable, so the work
// goes on here: an allocating transaction commits, an Abort gives its space
// back, and a commit that frees an extent an open Reader holds neither
// overwrites nor truncates it.
func TestRetryingAnInterruptedFollowUpOnTheSameHandle(t *testing.T) {
	for _, tc := range []struct {
		what  string
		step  int
		steps uint64
	}{
		{"interrupted before the flip", 3, 1},
		{"interrupted settling the other copy", 4, 2},
	} {
		t.Run(tc.what, func(t *testing.T) {
			a, fx := newFixture(t, Options{})
			keepData := text(20000, 370)
			keep := add(t, a, root, "keep.bin", keepData)
			before, _, _ := a.Stat()
			last := add(t, a, root, "last.bin", noise(200000, 371))
			grown, _, _ := a.Stat()
			seq := a.Seq()

			failTrimAt = skipTrimAt(tc.step)
			_, err := a.Delete(ctx, last.ID)
			failTrimAt = nil
			if err != nil {
				t.Fatal(err)
			}
			if a.Seq() != seq+tc.steps {
				t.Fatalf("the sequence advanced by %d, expected %d", a.Seq()-seq, tc.steps)
			}
			if a.Broken() != nil {
				t.Fatalf("the handle broke over an interrupted follow-up: %v", a.Broken())
			}
			if s, _, _ := a.Stat(); s < grown {
				t.Fatalf("the file was cut to %d by a follow-up commit that did not land; it was %d", s, grown)
			}

			// An allocating transaction, on this handle: it lands in the space
			// the interrupted commit left free, and its own commit gives the
			// tail back.
			addedData := noise(60000, 372)
			added := add(t, a, root, "added.bin", addedData)
			if got := extract(t, a, added.ID); !bytes.Equal(got, addedData) {
				t.Error("the file added after the interruption")
			}
			if got := extract(t, a, keep.ID); !bytes.Equal(got, keepData) {
				t.Error("the file that stayed")
			}
			insideTheFile(t, a)
			disjoint(t, a)
			if size, _, _ := a.Stat(); size > before+added.StoredSize+metadataSlack {
				t.Errorf("the file stands at %d; it was %d before a %d-byte file was added into the tail", size, before, added.StoredSize)
			}

			// An Abort on the same handle: what it appended is truncated away,
			// the archive is where it was, and the next commit still lands.
			size0, _, free0 := a.Stat()
			tx, err := a.Begin()
			if err != nil {
				t.Fatal(err)
			}
			goneData := noise(80000, 373)
			if _, err := tx.Add(ctx, root, "gone.bin", bytes.NewReader(goneData), int64(len(goneData))); err != nil {
				t.Fatal(err)
			}
			tx.Abort()
			if size, _, free := a.Stat(); size != size0 || free != free0 {
				t.Errorf("the aborted transaction left %d bytes and %d free; it found %d and %d", size, free, size0, free0)
			}
			if s := sizeOnDisk(t, fx.path); s != size0 {
				t.Errorf("the archive says %d bytes after the abort, the file is %d", size0, s)
			}
			if _, err := a.Rename(ctx, keep.ID, "kept.bin"); err != nil {
				t.Fatal(err)
			}
			insideTheFile(t, a)
			disjoint(t, a)

			// A commit that frees an extent a Reader on this handle holds: the
			// reader finishes on the right bytes, and the tail waits for it.
			r, err := a.OpenReader(added.ID)
			if err != nil {
				t.Fatal(err)
			}
			held := r.e
			if _, err := a.Delete(ctx, added.ID); err != nil {
				t.Fatal(err)
			}
			if size, _, _ := a.Stat(); size < end(held) {
				t.Errorf("the file was cut to %d, over an extent an open reader holds that ends at %d", size, end(held))
			}
			if s := sizeOnDisk(t, fx.path); s < end(held) {
				t.Errorf("the file on disk is %d bytes, under the reader's extent that ends at %d", s, end(held))
			}
			got, err := io.ReadAll(r)
			if err != nil || !bytes.Equal(got, addedData) {
				t.Fatalf("the deleted file's reader, after the commit that freed it: %v", err)
			}
			r.Close()

			// Only now, and with the next commit, the tail comes back.
			if _, err := a.Rename(ctx, keep.ID, "keep.bin"); err != nil {
				t.Fatal(err)
			}
			after, _, _ := a.Stat()
			if after > before+metadataSlack {
				t.Errorf("the tail did not come back once the reader closed: %d, the file was %d before the adds", after, before)
			}
			if s := sizeOnDisk(t, fx.path); s != after {
				t.Errorf("the archive says %d bytes, the file is %d", after, s)
			}
			insideTheFile(t, a)
			disjoint(t, a)
			if got := extract(t, a, keep.ID); !bytes.Equal(got, keepData) {
				t.Error("the file that stayed, after all of it")
			}
		})
	}
}

// R31's fallback over a follow-up commit interrupted before its flip: the
// live copy damaged, and the copy behind it reads every file of the state it
// names. That copy is the retirement's work — step 1 gives it the state just
// committed, and that is what makes step 2 legal, since the relocated index
// and free map go into the run that state holds free. Without the retirement
// the copy behind would still name what this commit gave back, and step 2
// would have written over it.
func TestTheFallbackAfterAnInterruptedFollowUp(t *testing.T) {
	a, fx := newFixture(t, Options{})
	keepData := text(20000, 380)
	keep := add(t, a, root, "keep.bin", keepData)
	last := add(t, a, root, "last.bin", noise(200000, 381))

	failTrimAt = skipTrimAt(3)
	_, err := a.Delete(ctx, last.ID)
	failTrimAt = nil
	if err != nil {
		t.Fatal(err)
	}
	a.Close()

	// The live copy damaged: what answers now is the copy the retirement
	// wrote, and R31 promises it is a complete state.
	img := snapshot(t, fx.path)
	sbA, err := format.DecodeArchiveSuperblock(img[format.CopyA.ArchiveSuperblockOff() : format.CopyA.ArchiveSuperblockOff()+format.SuperblockSize])
	if err != nil {
		t.Fatal(err)
	}
	sbB, err := format.DecodeArchiveSuperblock(img[format.CopyB.ArchiveSuperblockOff() : format.CopyB.ArchiveSuperblockOff()+format.SuperblockSize])
	if err != nil {
		t.Fatal(err)
	}
	liveOff := format.CopyA.ArchiveSuperblockOff()
	if sbB.Seq > sbA.Seq {
		liveOff = format.CopyB.ArchiveSuperblockOff()
	}
	img[liveOff+200] ^= 1
	restore(t, fx.path, img)

	b := fx.open(t)
	if b.Stale() == nil {
		t.Error("the damaged live copy was not reported")
	}
	if names := liveNames(t, b); len(names) != 1 || names[0] != "keep.bin" {
		t.Fatalf("the fallback holds %v; the retirement stands it on the commit that is durable", names)
	}
	if got := extract(t, b, keep.ID); !bytes.Equal(got, keepData) {
		t.Error("the fallback state does not read every file")
	}
	insideTheFile(t, b)
	// And the tail still comes back, from the fallback state, on the next
	// commit.
	if _, err := b.Rename(ctx, keep.ID, "kept.bin"); err != nil {
		t.Fatal(err)
	}
	disjoint(t, b)
	if got := extract(t, b, keep.ID); !bytes.Equal(got, keepData) {
		t.Error("the file that stayed, after the tail came back")
	}
}

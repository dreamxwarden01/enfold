package archive

import (
	"bytes"
	"errors"
	"io"
	"os"
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

// failAtStep is the crash seam of trim.go, armed for one of the follow-up
// commit's two superblock writes: 3 is the flip, 4 the other copy.
func failAtStep(step int) func(int) error {
	return func(s int) error {
		if s == step {
			return errors.New("the disk is gone")
		}
		return nil
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

// reread opens the file again without a lock and hands back what it holds:
// the state a reader would find, not the one the writer believes in.
func reread(t testing.TB, fx *fixture) *Archive {
	t.Helper()
	opts := fx.opts
	opts.ReadOnly = true
	a, err := Open(fx.path, []Key{fx.key}, opts)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { a.Close() })
	return a
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

	failTrimAt = failAtStep(3)
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

	failTrimAt = failAtStep(4)
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

package archive

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"

	"github.com/dreamxwarden01/enfold/internal/format"
)

// The adversary's pass over R40's move step (reclaim.go), on the file
// itself: a crash at every write of a move commit — the copy, the index, the
// free map, the flip, and each step of the follow-up that gives the tail back
// — and after each, what R31 promises: Open finds a complete state whose
// every file reads, and the losing copy, where it still decodes, names a
// state that reads as well. The main commit has no seam of its own, so its
// writes are replayed one at a time, whole and torn in half, onto the image
// the copy left (failMoveAt step 2); the follow-up's writes have trim.go's
// seam. A test that passes here is the proof that the step held; one that
// fails is left in place, named, for the fixer.

// moveFixture is the layout every scenario here starts from: a hole at the
// front, a file that stays, and a file at the tail that fits the hole — the
// plan is one move of the tail file into the hole.
type moveFixture struct {
	a        *Archive
	fx       *fixture
	keep     FileInfo
	last     FileInfo
	keepData []byte
	lastData []byte
	from     extent
	plan     ReclaimPlan
}

func newMoveFixture(t testing.TB, seed uint64) *moveFixture {
	t.Helper()
	a, fx := newFixture(t, Options{NoCompression: true})
	front := add(t, a, root, "front.bin", noise(100000, seed))
	m := &moveFixture{a: a, fx: fx, keepData: noise(200000, seed+1), lastData: noise(50000, seed+2)}
	m.keep = add(t, a, root, "keep.bin", m.keepData)
	m.last = add(t, a, root, "last.bin", m.lastData)
	if _, err := a.Delete(ctx, front.ID); err != nil {
		t.Fatal(err)
	}
	m.from = extentOf(t, a, m.last.ID)
	m.plan = a.PlanReclaim(0)
	if len(m.plan.Moves) != 1 || m.plan.Moves[0].From != m.from || m.plan.NeedsPublish {
		t.Fatalf("the fixture's plan: %+v", m.plan)
	}
	return m
}

// files is what every state of the fixture holds, for reading them all back.
func (m *moveFixture) files() []wantFile {
	return []wantFile{{m.keep.ID, m.keepData}, {m.last.ID, m.lastData}}
}

type wantFile struct {
	id   [16]byte
	data []byte
}

// readsAll extracts every file and compares it: the content hash verifies
// inside Extract, so a file that "reads" is one whose every tag verified.
func readsAll(t testing.TB, a *Archive, what string, want []wantFile) {
	t.Helper()
	for _, f := range want {
		if got := extract(t, a, f.id); !bytes.Equal(got, f.data) {
			t.Errorf("%s: file %x does not read back as stored", what, f.id)
		}
	}
}

// sbAt decodes the superblock copy at off in img, or fails the test.
func sbAt(t testing.TB, img []byte, off uint64) *format.ArchiveSuperblock {
	t.Helper()
	sb, err := format.DecodeArchiveSuperblock(img[off : off+format.SuperblockSize])
	if err != nil {
		t.Fatalf("the superblock copy at 0x%x: %v", off, err)
	}
	return sb
}

// copies is what Open would find in img: the offset of the copy it picks
// and of the other one, and whether the other decodes at all.
func copies(t testing.TB, img []byte) (live, loser uint64, loserDecodes bool) {
	t.Helper()
	offA, offB := format.CopyA.ArchiveSuperblockOff(), format.CopyB.ArchiveSuperblockOff()
	sbA, errA := format.DecodeArchiveSuperblock(img[offA : offA+format.SuperblockSize])
	sbB, errB := format.DecodeArchiveSuperblock(img[offB : offB+format.SuperblockSize])
	switch {
	case errA != nil && errB != nil:
		t.Fatal("neither superblock copy decodes")
	case errA != nil:
		return offB, offA, false
	case errB != nil:
		return offA, offB, false
	case sbA.Seq > sbB.Seq:
		return offA, offB, true
	default:
		return offB, offA, true
	}
	return 0, 0, false
}

// openImage writes img to a fresh path and opens it writable, the way a
// reopen after a crash would — the writer's own handle, where one exists,
// still holds the original and a second handle beside it is refused.
func openImage(t testing.TB, fx *fixture, img []byte, name string) *Archive {
	t.Helper()
	path := filepath.Join(t.TempDir(), name+".efd")
	restore(t, path, img)
	a, err := Open(path, []Key{fx.key}, fx.opts)
	if err != nil {
		t.Fatalf("%s: Open: %v", name, err)
	}
	t.Cleanup(func() { a.Close() })
	return a
}

// fallbackReads is R31's promise put to the file: with the live copy zeroed,
// what the losing copy names must open, lie inside the file, and read in
// full. It requires both copies to decode — a torn loser is the live copy's
// own case, and there the live copy is what the promise is about.
func fallbackReads(t testing.TB, fx *fixture, img []byte, name string, want []wantFile) {
	t.Helper()
	live, _, ok := copies(t, img)
	if !ok {
		t.Fatalf("%s: the losing copy does not decode; nothing to fall back on", name)
	}
	torn := bytes.Clone(img)
	clear(torn[live : live+format.SuperblockSize])
	c := openImage(t, fx, torn, name+"-fallback")
	if c.Stale() == nil {
		t.Errorf("%s: the zeroed live copy went unreported", name)
	}
	readsAll(t, c, name+", the fallback state", want)
	insideTheFile(t, c)
	disjoint(t, c)
	c.Close()
}

// grow pads img with zeros to n bytes when it is shorter: what a file system
// leaves under a write that extended the file and did not finish.
func grow(img []byte, n uint64) []byte {
	out := bytes.Clone(img)
	if uint64(len(out)) < n {
		out = append(out, make([]byte, n-uint64(len(out)))...)
	}
	return out
}

// A crash at every write of the move commit itself, replayed onto the image
// the copy left: the index torn and whole, the free map torn and whole, the
// flip torn, and the flip whole with no follow-up. Before the flip every
// image opens on the original state with the copy in free space and nothing
// truncated; the torn flip opens on the original state one copy short; after
// the flip the moved state stands with its source quarantined and the
// original still readable behind it. Each image then carries the run on.
func TestACrashAtEveryWriteOfTheMoveCommit(t *testing.T) {
	m := newMoveFixture(t, 600)
	a, fx := m.a, m.fx
	seq := a.Seq()
	orig := snapshot(t, fx.path)
	to := m.plan.Moves[0].To
	cipher := orig[m.from.Off:end(m.from)]

	var pre []byte
	failMoveAt = func(step int, b []byte, off uint64) (int, bool) {
		if step == 2 {
			pre = snapshot(t, fx.path)
		}
		return 0, false
	}
	failTrimAt = skipTrimAt(1) // the first commit alone: the follow-up never begins
	_, err := a.MoveExtents(ctx, m.plan.Moves, nil)
	failMoveAt, failTrimAt = nil, nil
	if err != nil {
		t.Fatal(err)
	}
	if a.Seq() != seq+1 {
		t.Fatalf("the move committed %d times", a.Seq()-seq)
	}
	post := snapshot(t, fx.path)
	if pre == nil || !bytes.Equal(pre[to.Off:end(to)], cipher) {
		t.Fatal("the copy is not on the file before the commit")
	}
	for _, off := range []uint64{format.CopyA.ArchiveSuperblockOff(), format.CopyB.ArchiveSuperblockOff()} {
		if !bytes.Equal(pre[off:off+format.SuperblockSize], orig[off:off+format.SuperblockSize]) {
			t.Fatal("a superblock changed before the commit began")
		}
	}
	liveOff, _, _ := copies(t, post)
	psb := sbAt(t, post, liveOff)
	if psb.Seq != seq+1 {
		t.Fatalf("the live copy after the move is at seq %d, want %d", psb.Seq, seq+1)
	}
	ie := extent{Off: psb.IndexOff, Len: psb.IndexLen + format.TagSize}
	me := extent{Off: psb.FreeMapOff, Len: psb.FreeMapLen}

	// The commit's writes, replayed in order onto pre: a crash inside each
	// leaves the prefix; a crash after leaves it whole.
	half := func(img []byte, e extent) []byte {
		n := e.Len / 2
		out := grow(img, e.Off+n)
		copy(out[e.Off:e.Off+n], post[e.Off:e.Off+n])
		return out
	}
	whole := func(img []byte, e extent) []byte {
		out := grow(img, end(e))
		copy(out[e.Off:end(e)], post[e.Off:end(e)])
		return out
	}
	indexTorn := half(pre, ie)
	indexWhole := whole(pre, ie)
	mapTorn := half(indexWhole, me)
	mapWhole := whole(indexWhole, me)
	flipTorn := half(mapWhole, extent{Off: liveOff, Len: format.SuperblockSize})
	if replayed := whole(mapWhole, extent{Off: liveOff, Len: format.SuperblockSize}); !bytes.Equal(replayed, post) {
		t.Fatal("the replay of the commit's writes does not reproduce the committed file: the commit wrote something else")
	}

	for _, tc := range []struct {
		what        string
		img         []byte
		seq         uint64
		stale       bool
		at          extent // where last.bin lies in the state Open finds
		loser       bool   // both copies decode: the fallback is checkable
		quarantined bool
	}{
		{"the index, torn", indexTorn, seq, false, m.from, true, false},
		{"the index, whole", indexWhole, seq, false, m.from, true, false},
		{"the free map, torn", mapTorn, seq, false, m.from, true, false},
		{"the free map, whole", mapWhole, seq, false, m.from, true, false},
		{"the flip, torn", flipTorn, seq, true, m.from, false, false},
		{"the flip, whole; no follow-up", post, seq + 1, false, to, true, true},
	} {
		t.Run(tc.what, func(t *testing.T) {
			if uint64(len(tc.img)) < uint64(len(orig)) {
				t.Fatalf("the image is %d bytes; the file was %d, and nothing may be truncated before the follow-up", len(tc.img), len(orig))
			}
			c := openImage(t, fx, tc.img, "crashed")
			if c.Seq() != tc.seq {
				t.Errorf("opened at seq %d, want %d", c.Seq(), tc.seq)
			}
			if (c.Stale() != nil) != tc.stale {
				t.Errorf("stale=%v, want %v", c.Stale(), tc.stale)
			}
			if c.FreeMapRebuilt() != nil {
				t.Errorf("the free map was rebuilt: %v", c.FreeMapRebuilt())
			}
			if got := extentOf(t, c, m.last.ID); got != tc.at {
				t.Errorf("last.bin lies at %+v, want %+v", got, tc.at)
			}
			readsAll(t, c, "the state Open found", m.files())
			insideTheFile(t, c)
			disjoint(t, c)
			free, retired, pool := spaces(c)
			if tc.quarantined {
				if !free.contains(m.from) || !retired.intersects(m.from) || pool.intersects(m.from) {
					t.Errorf("the source %+v: free=%v retired=%v pool=%v; want published free and quarantined", m.from, free.contains(m.from), retired.intersects(m.from), pool.intersects(m.from))
				}
			} else if !free.contains(to) {
				t.Errorf("the copy's bytes at %+v are not free space in the original state", to)
			}
			// The bytes the losing copy names, and the original state's
			// data, are where they were: the source was never written.
			if !bytes.Equal(tc.img[m.from.Off:end(m.from)], cipher) {
				t.Error("the source's ciphertext was written over")
			}
			if tc.loser {
				fallbackReads(t, fx, tc.img, "crashed", m.files())
			}

			// The run goes on from here: the plan or, with nothing left to
			// move, the empty commit gives the tail back.
			again := c.PlanReclaim(0)
			if again.NeedsPublish {
				t.Errorf("the plan after the crash wants a Publish first: %+v", again)
			}
			if len(again.Moves) > 0 {
				if _, err := c.MoveExtents(ctx, again.Moves, nil); err != nil {
					t.Fatalf("the run after the crash: %v", err)
				}
			} else if again.TailReturned == 0 {
				t.Errorf("nothing to move and nothing to give back: %+v, the file is %d bytes and was %d", again, sizeOnDisk(t, c.path), len(orig))
			} else if _, err := c.Publish(ctx); err != nil {
				t.Fatalf("the empty commit after the crash: %v", err)
			}
			if s := sizeOnDisk(t, c.path); s >= uint64(len(orig)) {
				t.Errorf("the run gave nothing back: %d bytes, the file was %d", s, len(orig))
			}
			if got := extentOf(t, c, m.last.ID); end(got) > m.from.Off {
				t.Errorf("last.bin lies at %+v after the run, not below its source %+v", got, m.from)
			}
			readsAll(t, c, "after the run", m.files())
			insideTheFile(t, c)
			disjoint(t, c)
			fallbackReads(t, fx, snapshot(t, c.path), "after-the-run", m.files())
		})
	}
}

// A crash at each step of the follow-up commit that gives a move's tail
// back, through trim.go's seam: the retirement, the relocated index and free
// map — which land where the source's ciphertext lay, both copies by then
// naming the moved state — the flip, and the settlement of the other copy.
// After each the file opens on a complete state whose every file reads, the
// moved file at its copy, and the losing copy where it decodes names a state
// that reads as well; the next commit then gives the tail back.
func TestACrashAtEveryStepOfAMovesFollowUp(t *testing.T) {
	for _, tc := range []struct {
		what  string
		arm   func(*landing) trimSeam
		steps uint64 // how far the sequence moved
		stale bool   // a superblock copy left torn
		indet bool   // the commit reported an outcome it cannot know
	}{
		{"the retirement, torn", func(got *landing) trimSeam { return tearTrimAt(1, 1, got) }, 1, true, false},
		{"the retirement, not begun", func(*landing) trimSeam { return skipTrimAt(1) }, 1, false, false},
		{"the relocated index, torn", func(got *landing) trimSeam { return tearTrimAt(2, 1, got) }, 1, false, false},
		{"the relocated free map, torn", func(got *landing) trimSeam { return tearTrimAt(2, 2, got) }, 1, false, false},
		{"the flip, not begun", func(*landing) trimSeam { return skipTrimAt(3) }, 1, false, false},
		{"the flip, torn", func(got *landing) trimSeam { return tearTrimAt(3, 1, got) }, 1, true, true},
		{"the other copy, torn", func(got *landing) trimSeam { return tearTrimAt(4, 1, got) }, 2, true, false},
		{"the other copy, not begun", func(*landing) trimSeam { return skipTrimAt(4) }, 2, false, false},
	} {
		t.Run(tc.what, func(t *testing.T) {
			m := newMoveFixture(t, 620)
			a, fx := m.a, m.fx
			seq := a.Seq()
			before, _, _ := a.Stat()
			to := m.plan.Moves[0].To

			var tore landing
			failTrimAt = tc.arm(&tore)
			_, err := a.MoveExtents(ctx, m.plan.Moves, nil)
			failTrimAt = nil
			switch {
			case tc.indet:
				if !errors.Is(err, ErrIndeterminate) || a.Broken() == nil {
					t.Fatalf("a torn commit point: err=%v broken=%v", err, a.Broken())
				}
			case err != nil:
				t.Fatal(err)
			}
			img := snapshot(t, fx.path)
			if s := uint64(len(img)); s < before-m.from.Len {
				t.Fatalf("the file is %d bytes; it was %d and only the moved tail may go", s, before)
			}
			if !tc.indet {
				if a.Seq() != seq+tc.steps {
					t.Errorf("the handle is at seq %d, want %d", a.Seq(), seq+tc.steps)
				}
				if got := extentOf(t, a, m.last.ID); got != to {
					t.Errorf("on the handle last.bin lies at %+v, the move went to %+v", got, to)
				}
				readsAll(t, a, "on the handle", m.files())
				insideTheFile(t, a)
				disjoint(t, a)
			}

			// The file as the crash left it.
			c := openImage(t, fx, img, "crashed")
			if c.Seq() != seq+tc.steps {
				t.Errorf("opened at seq %d, want %d", c.Seq(), seq+tc.steps)
			}
			if (c.Stale() != nil) != tc.stale {
				t.Errorf("stale=%v, want %v", c.Stale(), tc.stale)
			}
			if c.FreeMapRebuilt() != nil {
				t.Errorf("the free map was rebuilt: %v", c.FreeMapRebuilt())
			}
			if got := extentOf(t, c, m.last.ID); got != to {
				t.Errorf("reopened, last.bin lies at %+v, the move went to %+v", got, to)
			}
			readsAll(t, c, "the state Open found", m.files())
			insideTheFile(t, c)
			disjoint(t, c)
			if !tc.stale {
				fallbackReads(t, fx, img, "crashed", m.files())
			}

			// The next commit gives the tail back, whatever the crash left.
			if _, err := c.Publish(ctx); err != nil {
				t.Fatal(err)
			}
			if s := sizeOnDisk(t, c.path); s > before-m.from.Len {
				t.Errorf("after the next commit the file is %d bytes; it was %d before a %d-byte file moved down", s, before, m.from.Len)
			}
			readsAll(t, c, "after the next commit", m.files())
			insideTheFile(t, c)
			disjoint(t, c)
			fallbackReads(t, fx, snapshot(t, c.path), "after-the-next-commit", m.files())
		})
	}
}

package archive

import (
	"crypto/rand"
	"fmt"

	"github.com/dreamxwarden01/enfold/internal/format"
)

// The tail comes back (FORMAT.md R31 as amended on 2026-09-09, APP.md §2.3,
// DECISIONS 2026-09-09 "Space comes back").
//
// R31 forbids a writer to touch what the previous commit freed: those
// extents are published free but allocated only from the commit after next,
// so that an archive whose live superblock copy is torn opens one commit
// behind with every file of that state readable. That is also why a delete
// could not give the file's tail back — the losing copy still referenced the
// data, and truncating it would have left that copy pointing past the end of
// the file. The freed space waited for a compaction, and an emptied archive
// stood at the size of what it had held.
//
// The amendment is that the quarantine lasts exactly one commit and that the
// archive layer issues that commit itself: a commit that leaves a free run at
// the end of the file is followed at once, inside the same Commit call, by a
// second, empty commit — the same index, one sequence further on — whose two
// extents are placed at the lowest offset nothing else may hold and whose
// free map drops the trailing run. Then the file is truncated to the end of
// that map. A commit therefore advances the sequence by one, or by two when
// the tail came back with it.
//
// The order is what makes it safe, and every step of it leaves at least one
// superblock copy naming a state whose every extent lies inside the file:
//
//  1. The losing copy is retired where it stands — given this commit's own
//     superblock at a lower sequence — so both copies name the state just
//     committed. Nothing then references what the commit freed, which is
//     what makes the next step legal; and a torn live copy still opens a
//     complete state, one whose files are all readable.
//  2. The index is resealed at its new place and the free map written after
//     it, both into the run the retirement has just released, and synced.
//     A crash here leaves both copies naming the first commit's state, with
//     those bytes lying in space that state holds free.
//  3. The flip: the follow-up commit's superblock, into the copy step 1
//     retired. This is its commit point.
//  4. The other copy is brought onto the same state, again at a lower
//     sequence, so that neither copy names the old index and free map.
//  5. Only now the truncation, which no copy can be left pointing past.
//
// A failure before step 3 is not an error the caller hears: the commit it
// asked for is durable and the tail simply waits for the next one, which
// finds it and tries again. The flip itself is the one write whose failure
// leaves the outcome unknown, as any commit's does, and breaks the Archive.
// A failure after it — step 4's copy, or the truncation — is not an error
// either: the follow-up commit is durable and whole, and all that is left is
// a tail no *live* superblock references but the losing copy still might, so
// it is not truncated. It stays free space at the end of the file, which the
// next commit gives back and which Open reclaims as free in any case.
//
// The fallback the two copies are for is therefore, after a trimming commit,
// the state that commit published rather than the one before it: step 1 has
// already retired the losing copy onto it, since nothing may reference the
// run being given back. That is R31 as amended — the promise is a complete
// state whose every file is readable, not a state one commit old.

// failTrimAt stands in for a disk that fails one of the follow-up commit's
// two superblock writes, for the crash-safety tests: it is called with the
// number of the step above that is about to run — 3, the flip, and 4, the
// other copy — and a non-nil answer stops that write. It is nil outside those
// tests, which set it while nothing else runs.
var failTrimAt func(step int) error

// trimFails asks the seam whether the given step is to fail.
func trimFails(step int) bool { return failTrimAt != nil && failTrimAt(step) != nil }

// reclaimTail runs the follow-up commit described above, if the file can be
// made shorter by it. plain is the index just committed — the follow-up
// commit publishes the very same records — and kid with indexKey are the key
// that commit sealed it under, which is not always the Archive's own: a key
// rotation commits under the new key before it adopts it.
//
// Caller holds a.mu, and calls it only from commit, with the first flip
// durable and the transaction finished. It reports the error that broke the
// Archive, if one did; every other failure leaves the archive at the first
// commit's state and is not the caller's to report.
func (a *Archive) reclaimTail(plain []byte, kid [16]byte, indexKey []byte) error {
	if a.opts.ReadOnly || a.closed || a.broken != nil || a.sb.Seq < 2 {
		return nil
	}

	// What must keep its place: the data of every live file, and the extents
	// open Readers hold — a Reader goes on reading the content it opened
	// even after a Replace or a Delete, so its extent is neither reused nor
	// truncated while it lives.
	keepEnd := uint64(format.ArchiveDataStart)
	live := newSpace(nil)
	for i := range a.index.Files {
		r := &a.index.Files[i]
		if r.State != format.FileLive || r.StoredSize == 0 {
			continue
		}
		e := extent{Off: r.DataOff, Len: r.StoredSize}
		live.union(e)
		keepEnd = max(keepEnd, end(e))
	}
	for e := range a.held {
		keepEnd = max(keepEnd, end(e))
	}

	// The follow-up commit's own two extents go at keepEnd, one after the
	// other: the lowest place they can lie, so that what is truncated is
	// everything above the last byte anything holds. Only this commit's own
	// index and free map can stand in the way there — they are the live
	// copy's until the flip and are not written over — and the place then
	// moves above them, giving back what lies past that instead. What is
	// left free is the holes below, which is what the map says and what
	// Compact is for: truncation gives the tail back, compaction the middle.
	cur := []extent{
		{Off: a.sb.IndexOff, Len: a.sb.IndexLen + format.TagSize},
		{Off: a.sb.FreeMapOff, Len: a.sb.FreeMapLen},
	}
	ilen := uint64(len(plain)) + format.TagSize
	// holesBelow is the free space under an offset: everything but the live
	// data, this commit's two extents among it since the follow-up commit
	// replaces them.
	holesBelow := func(off uint64) *space {
		s := newSpace(nil)
		if off > format.ArchiveDataStart {
			s.insert(extent{Off: format.ArchiveDataStart, Len: off - format.ArchiveDataStart})
			s.subtract(live)
		}
		return s
	}
	at, holes := keepEnd, holesBelow(keepEnd)
	mlen := 4 + 16*uint64(len(holes.x))
	// Two rounds settle it: the place moves above at most two extents, and
	// moving it can only add those two to the map it takes its size from.
	for round := 0; round < 3; round++ {
		moved := false
		for _, e := range cur {
			if overlaps(extent{Off: at, Len: ilen + mlen}, e) {
				at, moved = end(e), true
			}
		}
		if !moved {
			break
		}
		holes = holesBelow(at)
		mlen = 4 + 16*uint64(len(holes.x))
	}
	region := extent{Off: at, Len: ilen + mlen}
	for _, e := range cur {
		if overlaps(region, e) {
			return nil
		}
	}
	if mlen > format.MaxFreeMapLen {
		return nil
	}
	newSize := at + ilen + mlen
	if newSize >= a.size {
		return nil // no tail to give back
	}

	// 1. Retire the losing copy where it stands.
	loserCopy := a.live.Other()
	older := *a.sb
	older.Seq = a.sb.Seq - 1
	encOlder, err := older.Encode()
	if err != nil {
		return nil
	}
	if _, err := a.f.WriteAt(encOlder, int64(loserCopy.ArchiveSuperblockOff())); err != nil {
		return nil
	}
	if err := a.f.Sync(); err != nil {
		return nil
	}
	// Both copies name this state now, so the quarantine is spent: what the
	// commit freed is nobody's and may be allocated at once.
	a.loser = append(append([]extent(nil), cur...), live.extents()...)
	a.retired = newSpace(nil)
	a.rebuildPool()

	// 2. The same index, resealed where it now lies, and the map that drops
	//    the trailing run.
	next := *a.sb
	next.Seq = a.sb.Seq + 1
	next.IndexOff, next.IndexLen = at, uint64(len(plain))
	if _, err := rand.Read(next.IndexNonce[:]); err != nil {
		return nil
	}
	sealed, tag, err := sealIndex(indexKey, plain, &next, a.archiveID, kid)
	if err != nil {
		return nil
	}
	next.IndexTag = tag
	if _, err := a.f.WriteAt(sealed, int64(next.IndexOff)); err != nil {
		return nil
	}
	encMap, hash, err := holes.freeMap().Hash()
	if err != nil {
		return nil
	}
	if uint64(len(encMap)) != mlen {
		return nil // the map was planned at a size it did not encode to
	}
	next.FreeMapOff, next.FreeMapLen, next.FreeMapHash = at+ilen, mlen, hash
	if _, err := a.f.WriteAt(encMap, int64(next.FreeMapOff)); err != nil {
		return nil
	}
	if err := a.f.Sync(); err != nil {
		return nil
	}
	encNext, err := next.Encode()
	if err != nil {
		return nil
	}
	if trimFails(3) {
		return nil
	}

	// 3. The flip: the follow-up commit's commit point.
	if _, err := a.f.WriteAt(encNext, int64(loserCopy.ArchiveSuperblockOff())); err != nil {
		a.broken = fmt.Errorf("%w: the follow-up commit's superblock write failed: %v", ErrIndeterminate, err)
		return a.broken
	}
	if err := a.f.Sync(); err != nil {
		a.broken = fmt.Errorf("%w: the follow-up commit may already be durable: %v", ErrIndeterminate, err)
		return a.broken
	}
	a.sb, a.live = &next, loserCopy
	newIndex := extent{Off: next.IndexOff, Len: next.IndexLen + format.TagSize}
	newMap := extent{Off: next.FreeMapOff, Len: next.FreeMapLen}

	// 4. The other copy onto the same state, and 5. the truncation, which
	//    only a copy that no longer names the old extents allows.
	settled := false
	older2 := next
	older2.Seq = next.Seq - 1
	if enc2, err := older2.Encode(); err == nil && !trimFails(4) {
		if _, err := a.f.WriteAt(enc2, int64(loserCopy.Other().ArchiveSuperblockOff())); err == nil {
			settled = a.f.Sync() == nil
		}
	}
	truncated := false
	if settled {
		if err := a.f.Truncate(int64(newSize)); err == nil {
			// The length is metadata: a truncation that is not durable
			// leaves the file longer than the state it holds, which the
			// next Open reclaims as free like any other unreferenced tail.
			a.f.Sync()
			a.size = newSize
			truncated = true
		}
	}

	// The state the file is in now. What the other copy names is what the
	// quarantine protects, and once it names this state there is nothing to
	// protect: the free map is the pool.
	loserRefs := []extent{newIndex, newMap}
	if !settled {
		loserRefs = cur
	}
	loserRefs = append(loserRefs, live.extents()...)
	a.free = holes.clone()
	if !truncated {
		a.free = a.free.merged(newSpace([]extent{{Off: newSize, Len: a.size - newSize}}))
	}
	used := live.clone()
	used.union(newIndex)
	used.union(newMap)
	a.loser = loserRefs
	a.retired = newSpace(loserRefs)
	a.retired.subtract(used)
	a.rebuildPool()
	return nil
}

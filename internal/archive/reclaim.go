package archive

import (
	"context"
	"fmt"
	"sort"

	"github.com/dreamxwarden01/enfold/internal/format"
)

// Compaction in place (FORMAT.md R40, APP.md §2.3 "Space comes back" as
// amended on 2026-09-10, DECISIONS 2026-09-10).
//
// Truncation gives the tail back (trim.go); a hole with live data above it
// is given back by moving that data down into it. A move is an ordinary
// commit: the ciphertext of a live extent is copied verbatim into a hole
// that lies wholly before it — the same DEK, the same nonces, the same tags,
// and the chunk AAD names no offset — the record's data_off is pointed at
// the copy, and the source is freed by that commit and quarantined for one
// commit like a deleted file's data. Nothing is retargeted: a Reader holding
// the source keeps reading it where it lies, and R31 keeps a held extent
// from reuse and from truncation until the hold ends. What a move gives back
// is the tail, when the last live byte moves down and the follow-up commit
// of R31 truncates it (trim.go, inside the same Commit); a commit's own
// metadata may leave the file larger for that moment — the free map is
// always appended — and the follow-up takes it back with the rest. The one
// thing that could stand in the follow-up's way there is the commit's own
// index, so a move commit keeps it out of that place (followUpAt): what the
// plan said would come back is then what comes back.
//
// The placement is the plan's, not the allocator's: alloc prefers an exact
// fit anywhere and appends otherwise, either of which could put the copy
// above the source, so a destination is taken exactly (Tx.take) from the
// pool the plan drew on — the published map less what is quarantined and
// what readers hold — and is never appended. That is what makes an aborted
// move cheap and safe: every destination is taken before a byte is copied,
// so a plan that no longer fits is refused whole and writes nothing; the
// copies of one that fails later lie in free space the committed map already
// lists, the file did not grow, and Abort's truncation to the size at Begin
// cannot reach them.
//
// What a crash leaves, at each point: during or after the copies and before
// the commit — the originals live and the copies in free space, which the
// next Open lists free and the next commit may write over; at the flip — one
// of the two states, as for any commit; after the flip — the moved state with
// the source quarantined, the follow-up commit's own steps being trim.go's.

// Move is one live extent's relocation: the file whose data lies at From,
// and the hole To it moves into, of the same length and wholly before it.
type Move struct {
	ID       [16]byte
	From, To extent
}

// ReclaimPlan is the dry run of R40 over the committed state: which extents
// would move where, and what that would give back — for the one commit the
// plan answers for, and for the whole run that commit begins.
type ReclaimPlan struct {
	// Moves, in ascending order of From; each To is a hole of its own. One
	// commit's worth: a plan is good for one, and the caller plans again
	// after each. The list is whole whatever budget the plan was asked for
	// — a caller with a budget per commit (APP.md §2.3) takes the leading
	// moves that fit it and plans again, and PlanReclaim's budget is what
	// the run-level figures below are measured against, not a knife taken
	// to this list.
	Moves []Move
	// BytesToMove is the ciphertext the moves copy: the sum of From.Len.
	BytesToMove uint64
	// TailReturned is what the file would shrink by once the last move has
	// landed and the follow-up commit has truncated the tail: the file's
	// size less the planned layout's last kept byte and the follow-up's own
	// index and free map after it, placed as trim.go places them. It is an
	// estimate on the low side — the map is taken to be no smaller than the
	// one on disk, and MoveExtents keeps the commit's own index out of the
	// follow-up's place, so no metadata of the run stands between the figure
	// and the tail — and a hole filled behind a file that cannot move counts
	// as nothing. With no moves it is what any commit would give back: a
	// free tail an interrupted follow-up left, which Publish gives back the
	// same way.
	TailReturned uint64
	// RunBytesToMove and RunTailReturned are those two figures for the whole
	// run rather than for its first commit, and they are what the caller's
	// worth rule measures (APP.md §2.3): a plan one commit deep says nothing
	// of what the second gives back, and the layout [hole S][A: S][B: S] —
	// the first of three equal files deleted — moves A into the hole and
	// returns nothing at all, while the run that goes on from there returns
	// S. They come from a dry run of the caller's own plan-move-plan loop
	// over this same free-space model, in memory: step k planned exactly as
	// this plan is, the sources step k frees becoming holes for step k+2
	// (R31: freed at k, published in k's map, allocatable from the commit
	// after next), with the empty commit that spends the quarantine where a
	// step has no moves and the step after it would, and every step held to
	// the budget the caller passed — the commits the caller will actually
	// make are the budgeted ones, and a step the budget splits leaves a hole
	// standing that the next step's coalescing turns into a place the
	// unbudgeted run never had, so the run then moves far more than an
	// unbudgeted dry run would say (the outside review of 2026-09-10,
	// finding 1). Estimates on the low side as TailReturned is, each step's
	// index and map headroom taken as that one takes it, and cut short —
	// answering for what it walked — on a layout whose run is longer than
	// the dry run's bound.
	RunBytesToMove  uint64
	RunTailReturned uint64
	// Commits is how many commits that run would take, the empty ones among
	// them: for a log line and for a test, never for a decision. It counts
	// the run's plans, not its writes — a caller with a budget per commit
	// (APP.md §2.3) splits any of them into several.
	Commits int
	// NeedsPublish reports that a chosen hole is still under R31's
	// quarantine — freed by the commit just made, with no follow-up to
	// publish it — so the run must begin with Publish; MoveExtents refuses
	// such a plan (ErrStalePlan) until it has.
	NeedsPublish bool
}

// PlanReclaim plans R40 on the committed state, in memory and without
// writing: live extents in ascending order of offset, each into the earliest
// hole of the published map — less what readers hold — that lies wholly
// before it and holds it, the hole then spent; a file no hole before it holds
// is skipped, the plan going on to the next; an extent a Reader holds is left
// where it lies for this run, the plan re-made when the reader closes. A
// plan is good for one commit: the moves free their sources into quarantine
// and the commit's own index takes a hole of its own, so the caller plans
// again after each MoveExtents rather than working through one plan in
// batches, and a plan the archive has moved on from is refused whole
// (ErrStalePlan), never acted on in part. What the plan says of the run
// beyond that one commit — RunBytesToMove, RunTailReturned, Commits — is a
// dry run of that same loop over the same model, since the rule that decides
// whether a run is worth making cannot be measured on its first commit
// alone. Empty on a closed or broken Archive.
//
// budget is the ciphertext the caller moves in one commit, 0 for a caller
// with no budget at all. Moves is the whole commit either way — the caller
// takes the leading moves that fit and plans again — but the dry run takes
// that same prefix at every step of it, because those are the commits the
// caller will make and their placements are not the unbudgeted ones: a
// budget that stops a step short leaves a hole for the step after, where it
// coalesces with what the commit freed into a place no unbudgeted step ever
// offered, and files the unbudgeted run left alone move after all.
func (a *Archive) PlanReclaim(budget uint64) ReclaimPlan {
	var plan ReclaimPlan
	s := a.reclaimSnapshot()
	if s == nil {
		return plan
	}
	// The one commit this plan answers for, over the published map: a
	// quarantined hole is among the candidates — Publish is what makes it
	// allocatable, and NeedsPublish says so.
	moves, _, _, newSize := s.step(s.holes.clone(), 0)
	plan.Moves = moves
	for _, m := range moves {
		plan.BytesToMove += m.From.Len
		if s.retired.intersects(m.To) {
			plan.NeedsPublish = true
		}
	}
	if newSize < s.size {
		plan.TailReturned = s.size - newSize
	}
	plan.RunBytesToMove, plan.RunTailReturned, plan.Commits = s.estimateRun(budget)
	return plan
}

// source is one live file's data where a plan finds it. The run-level
// estimate moves them about in memory, so a layout is a slice of these.
type source struct {
	id [16]byte
	e  extent
}

// byOffset puts a layout in the order a plan walks it.
func byOffset(live []source) {
	sort.Slice(live, func(i, j int) bool { return live[i].e.Off < live[j].e.Off })
}

// reclaimState is the free-space model a plan is made over: the archive's
// own, copied under its lock, so that the dry run of a whole run can step
// over it without holding one and without touching the file. Extents and
// three lengths — there is no I/O anywhere below.
type reclaimState struct {
	size     uint64   // the file's length
	live     []source // the live data, ascending by offset
	holes    *space   // the published map less what readers hold
	retired  *space   // what of that R31 still quarantines
	held     *space   // what readers hold: never moved, never allocated
	ilen     uint64   // the index and its tag, which a move does not resize
	mapFloor uint64   // the map on disk: no step's map is taken to be smaller
	keepMin  uint64   // the data start, or the last byte a reader holds
}

// reclaimSnapshot copies the model under a.mu, and answers nil for a handle
// that has nothing to plan: a closed or broken Archive.
func (a *Archive) reclaimSnapshot() *reclaimState {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.usable() != nil {
		return nil
	}
	s := &reclaimState{
		size:     a.size,
		holes:    a.free.clone(),
		retired:  a.retired.clone(),
		held:     newSpace(nil),
		ilen:     a.sb.IndexLen + format.TagSize,
		mapFloor: a.sb.FreeMapLen,
		keepMin:  format.ArchiveDataStart,
	}
	// What readers hold is neither a hole to fill nor a source to move, and
	// no truncation passes it.
	for e := range a.held {
		s.held.union(e)
		s.holes.removeOverlap(e)
		s.keepMin = max(s.keepMin, end(e))
	}
	for i := range a.index.Files {
		r := &a.index.Files[i]
		if r.State != format.FileLive || r.StoredSize == 0 {
			continue
		}
		s.live = append(s.live, source{r.FileID, extent{Off: r.DataOff, Len: r.StoredSize}})
	}
	byOffset(s.live)
	return s
}

// step plans one commit over the state: live extents in ascending order of
// offset, each into the earliest hole of holes that lies wholly before it and
// holds it, the hole then spent; a file no hole before it holds is skipped,
// and an extent a Reader holds is left where it lies. holes is consumed —
// what is left of it is the free space the commit would leave below the tail.
//
// budget, when it is not 0, is the ciphertext this commit may move, and the
// commit is the leading moves whose sources fit in it, never fewer than one
// so that a file larger than the budget still moves on its own — the rule
// the caller batches by (APP.md §2.3, ops.go reclaimRule.batch). The first
// move the budget cannot take ends the placing: nothing below it moves in
// this commit either, and the hole it wanted stays for the plan after. A
// file no hole holds is not a move and spends nothing of the budget.
//
// It answers the moves, the layout they leave, the last byte that layout
// keeps, and the size R31's follow-up commit would then truncate to: the
// follow-up's two extents go at keepEnd — the index, which a move does not
// resize, and a map of the holes below, no smaller than the map on disk, so
// the figure never promises more than the moves give. That is where trim.go
// will put them, because the commit keeps its own index out of that place
// (followUpAt) and the map it appends lies past it whenever there is a tail
// to give back.
func (s *reclaimState) step(holes *space, budget uint64) (moves []Move, layout []source, keepEnd, newSize uint64) {
	layout = make([]source, 0, len(s.live))
	placed := newSpace(nil)
	keepEnd = s.keepMin
	var spent uint64
	full := false
	for _, f := range s.live {
		at := f.e
		if !full && !s.held.intersects(f.e) {
			if to, ok := holes.firstFitBefore(f.e.Len, f.e.Off); ok {
				if budget != 0 && len(moves) > 0 && spent+f.e.Len > budget {
					// The commit is full. The hole goes back: this commit
					// does not take it, and the next plan finds it where it
					// was — with whatever this one frees beside it.
					holes.insert(to)
					full = true
				} else {
					moves = append(moves, Move{ID: f.id, From: f.e, To: to})
					at = to
					spent += f.e.Len
				}
			}
		}
		layout = append(layout, source{id: f.id, e: at})
		placed.union(at)
		keepEnd = max(keepEnd, end(at))
	}
	byOffset(layout)
	return moves, layout, keepEnd, keepEnd + s.ilen + max(mapLenBelow(keepEnd, placed), s.mapFloor)
}

// The dry run's bounds. A run under a budget is not a handful of steps —
// the budget is what makes it many, one commit per budget's worth of
// ciphertext and an empty one where a quarantine stands in the way — so the
// step bound is generous and the work bound is what really holds: a plan is
// made after every commit, so an estimate is worth a bounded walk over the
// extents and never an unbounded one. reclaimEstimateSteps bounds the steps
// and reclaimEstimateExtents the work, so that an archive of many files
// takes fewer steps and one of a few takes all of them. What the dry run
// answers for a run it did not walk to the end is what it saw: on the low
// side, like everything else here — which is why the caller keeps a guard of
// its own over the run it started (APP.md §2.3, ops.go startReclaim).
const (
	reclaimEstimateSteps   = 1 << 10
	reclaimEstimateExtents = 1 << 20
)

// estimateRun is the whole run rather than its first commit (APP.md §2.3):
// the same single-commit planner, step after step over the same model in
// memory, so that a caller's rule is measured on what the run would give back
// and not on what its first commit would — [hole S][A: S][B: S] moves A and
// returns nothing, and the archive never shrinks if that is what decides it.
//
// A step is planned over everything the published map holds, as PlanReclaim
// is; if it wants a hole still under R31's quarantine, the commit that step
// stands for is the empty one that spends it, and the moves come at the step
// after — which is R31 read forward: a source freed at step k is published in
// step k's map and allocatable from the commit after next, so it is a hole
// for step k+2. The tail each step's follow-up would cut is taken off the
// file as trim.go takes it, and nothing at or above the last byte a layout
// keeps is a hole any later move can use — it is that commit's own metadata,
// or the tail it cut. The run ends when a step has no moves and no quarantine
// stands between it and one.
//
// Every step is held to the caller's budget, because the caller's commits
// are (step, ops.go reclaimRule.batch): simulating one unbudgeted step where
// the caller will make several budgeted ones does not merely count the same
// moves in a different order, it counts different moves. A step that places
// A and, further down, D into what was left of A's hole leaves nothing for
// the files between them; the budgeted commit that places A alone leaves
// that remainder standing, the next plan finds it coalesced with the source
// A freed, and the files between move after all. The unbudgeted figure is
// then not on the low side at all, and the caller's worth rule rests on it
// (the outside review of 2026-09-10, finding 1).
func (s *reclaimState) estimateRun(budget uint64) (moved, returned uint64, commits int) {
	run := *s
	run.live = append([]source(nil), s.live...)
	// What may be allocated now, and what R31 holds back for one commit.
	avail := s.holes.clone()
	avail.subtract(s.retired)
	pending := s.holes.clone()
	pending.subtract(avail)
	size := s.size

	for visited := 0; commits < reclaimEstimateSteps && visited < reclaimEstimateExtents; visited += len(run.live) + 1 {
		holes := avail.merged(pending)
		moves, layout, keepEnd, newSize := run.step(holes, budget)
		quarantined := false
		for _, m := range moves {
			if pending.intersects(m.To) {
				quarantined = true
				break
			}
		}
		switch {
		case quarantined || (len(moves) == 0 && newSize < size):
			// The empty commit: it moves nothing — the hole the plan wants
			// is still quarantined, or a free tail an interrupted follow-up
			// left is all there is to take — and what it spends is the
			// quarantine. What it can give back is the tail over the layout
			// as it stands, so the step is planned again with no holes.
			_, _, keepEnd, newSize = run.step(newSpace(nil), budget)
			avail, pending = avail.merged(pending), newSpace(nil)
		case len(moves) == 0:
			return moved, returned, commits
		default:
			freed := newSpace(nil)
			for _, m := range moves {
				moved += m.From.Len
				freed.union(m.From)
			}
			// The holes the moves did not spend, the quarantine of the step
			// before spent with this commit; what this one freed waits.
			run.live, avail, pending = layout, holes, freed
		}
		commits++
		if newSize < size {
			returned += size - newSize
			size = newSize
		}
		if keepEnd < s.size {
			above := extent{Off: keepEnd, Len: s.size - keepEnd}
			avail.removeOverlap(above)
			pending.removeOverlap(above)
		}
	}
	return moved, returned, commits
}

// mapLenBelow is the encoded size of a free map of the holes under limit —
// everything there but live — which is the map the follow-up commit writes
// (trim.go).
func mapLenBelow(limit uint64, live *space) uint64 {
	below := newSpace(nil)
	if limit > format.ArchiveDataStart {
		below.insert(extent{Off: format.ArchiveDataStart, Len: limit - format.ArchiveDataStart})
		below.subtract(live)
	}
	return 4 + 16*uint64(len(below.x))
}

// followUpAt is the run the follow-up commit of R31 will take once index is
// committed (trim.go): from the last byte of live data or of an extent a
// reader holds, an index of ilen bytes and a map of the holes below it. The
// only thing that can stand in its way there is the committing transaction's
// own index — the follow-up cannot write over what the live copy references
// and moves its place above it instead, leaving the file longer than
// PlanReclaim said — so a commit that answers for a plan keeps this run out
// of its pool: its index takes a hole below, or is appended and goes with the
// tail. Caller holds a.mu.
func (a *Archive) followUpAt(index *format.Index, ilen uint64) extent {
	keepEnd := uint64(format.ArchiveDataStart)
	live := newSpace(nil)
	for i := range index.Files {
		r := &index.Files[i]
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
	return extent{Off: keepEnd, Len: ilen + mapLenBelow(keepEnd, live)}
}

// Publish commits the index unchanged: an empty commit that advances the
// sequence and spends R31's quarantine, so that what the previous commit
// freed becomes allocatable — the first commit of a run whose plan wants a
// hole the commit just made freed (ReclaimPlan.NeedsPublish). The follow-up
// truncation applies as to any commit: a free tail comes back with it, and
// the Receipt is then the follow-up's. A Tx that changed nothing commits
// nothing; this is the explicit way to commit nothing.
func (a *Archive) Publish(ctx context.Context) (Receipt, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.committable(); err != nil {
		return Receipt{}, err
	}
	if a.tx != nil {
		return Receipt{}, ErrTxOpen
	}
	index, err := cloneIndex(a.index)
	if err != nil {
		return Receipt{}, err
	}
	tx := &Tx{a: a, index: index, tree: newTree(index), pool: a.pool.clone(), allocs: newSpace(nil), pending: newSpace(nil), size0: a.size, changed: true}
	tx.pool.removeOverlap(a.followUpAt(index, a.sb.IndexLen+format.TagSize))
	rec, err := a.commit(ctx, tx, index, a.kid, a.indexKey)
	if err != nil {
		if a.broken == nil {
			tx.abortLocked()
		}
		return Receipt{}, err
	}
	return rec, nil
}

// moveChunk is the copy's unit: the bytes read and written between two
// looks at ctx, and one call of progress. Tests lower it to walk a small
// file in several chunks.
var moveChunk uint64 = 1 << 20

// failMoveAt stands in for a disk or a crash inside MoveExtents, for the
// crash-safety tests, in the shape of trim.go's failTrimAt. It is called
// with the step about to write and the write itself: step 1 is one chunk of
// a copy — the bytes and the destination offset they go to — and step 2 the
// commit, called once with no bytes after every copy has been written, the
// moment a crash leaves the copies in free space and the originals live. The
// answer is how much of the write reaches the file before it fails and
// whether it fails at all; step 2 carries no bytes, so its only answer is
// whether the commit begins. nil outside those tests, which set it while
// nothing else runs.
var failMoveAt func(step int, b []byte, off uint64) (tear int, fail bool)

// moveWrite runs one write of a move through the seam when one is armed.
func (a *Archive) moveWrite(step int, b []byte, off uint64) error {
	tear, fail := 0, false
	if failMoveAt != nil {
		tear, fail = failMoveAt(step, b, off)
	}
	if !fail {
		if len(b) == 0 {
			return nil
		}
		_, err := a.f.WriteAt(b, int64(off))
		return err
	}
	if tear > 0 {
		if tear > len(b) {
			tear = len(b)
		}
		if _, err := a.f.WriteAt(b[:tear], int64(off)); err != nil {
			return err
		}
		if err := a.f.Sync(); err != nil {
			return err
		}
	}
	return errTornWrite
}

// MoveExtents performs one commit of R40: every destination is taken
// exactly from the pool before any byte is copied, each source is copied
// verbatim into its destination in chunks with ctx looked at before every
// one, each record's data_off is pointed at the copy and its source freed at
// Commit and quarantined like a deleted file's data; then the commit, with
// R31's follow-up truncation when a freed source was the tail (the Receipt
// is then the follow-up's, as for any commit). Any error or cancel before
// the commit point aborts: the originals stay live, the copies lie in free
// space, and the file is no larger than it was — a destination is never
// appended. The commit's own index goes into a hole below the last kept
// byte or is appended, never where the follow-up commit will put its own
// (followUpAt), so the tail PlanReclaim promised is the tail that comes
// back. A move must lower its extent, into a hole of the same length
// wholly before it (ErrParams otherwise); a move whose source is not where
// the plan found it, or whose destination is not free space this transaction
// may allocate — live, quarantined (ReclaimPlan.NeedsPublish), held by a
// reader, or taken by another move — is ErrStalePlan, and nothing is
// written. A Reader holding a source keeps reading it where it lies and is
// never retargeted; the plan leaves such extents alone, and a move of one is
// honoured all the same, the hold keeping the source from reuse and from
// truncation until it ends.
//
// progress, when non-nil, is called on this goroutine without the Archive's
// lock, once per chunk copied, with the bytes copied so far and the bytes to
// copy; cancel through ctx.
func (a *Archive) MoveExtents(ctx context.Context, moves []Move, progress func(done, total uint64)) (Receipt, error) {
	if len(moves) == 0 {
		return Receipt{}, fmt.Errorf("%w: no moves", ErrParams)
	}
	tx, err := a.Begin()
	if err != nil {
		return Receipt{}, err
	}
	// Every move is staged before any is copied, so that a plan that does
	// not fit is refused whole and writes nothing.
	var total uint64
	a.mu.Lock()
	if err = tx.live(); err == nil {
		for _, m := range moves {
			if err = tx.stageMove(m); err != nil {
				break
			}
			total += m.From.Len
		}
	}
	if err == nil {
		// The index must not land where the follow-up will place its
		// extents: the tail would stop above it (followUpAt).
		tx.pool.removeOverlap(a.followUpAt(tx.index, a.sb.IndexLen+format.TagSize))
	}
	a.mu.Unlock()
	if err != nil {
		tx.Abort()
		return Receipt{}, err
	}
	// The copies, without the lock: readers may be reading a source
	// meanwhile, and a destination is nobody's but this transaction's.
	buf := make([]byte, min(moveChunk, total))
	var done uint64
	for _, m := range moves {
		if err := tx.copyExtent(ctx, m, buf, &done, total, progress); err != nil {
			tx.Abort()
			return Receipt{}, err
		}
	}
	if err := a.moveWrite(2, nil, 0); err != nil {
		tx.Abort()
		return Receipt{}, err
	}
	// The commit's own Sync, before its flip, is what makes the copies
	// durable ahead of the index that names them.
	return tx.Commit(ctx)
}

// stageMove records one move on the transaction: the destination taken
// exactly, the working record pointed at it, the source freed at Commit.
// The record is pointed before the copy rather than after because nothing
// of the working index is published until Commit and Abort discards all of
// it — and it is what refuses the same file twice, since its extent is then
// no longer where the plan found it. Caller holds a.mu.
func (tx *Tx) stageMove(m Move) error {
	switch {
	case m.From.Len == 0 || m.To.Len != m.From.Len:
		return fmt.Errorf("%w: a move of [0x%x, 0x%x) to [0x%x, 0x%x) is not one extent to another of its length", ErrParams, m.From.Off, end(m.From), m.To.Off, end(m.To))
	case end(m.To) > m.From.Off:
		return fmt.Errorf("%w: the destination [0x%x, 0x%x) does not lie wholly before the source [0x%x, 0x%x)", ErrParams, m.To.Off, end(m.To), m.From.Off, end(m.From))
	}
	r := tx.tree.liveFile(m.ID)
	if r == nil {
		return fmt.Errorf("%w: file %x is not live", ErrStalePlan, m.ID)
	}
	if at := (extent{Off: r.DataOff, Len: r.StoredSize}); at != m.From {
		return fmt.Errorf("%w: file %x lies at [0x%x, 0x%x), not at [0x%x, 0x%x)", ErrStalePlan, m.ID, at.Off, end(at), m.From.Off, end(m.From))
	}
	if err := tx.take(m.To); err != nil {
		return err
	}
	r.DataOff = m.To.Off
	tx.pending.insert(m.From)
	tx.changed = true
	return nil
}

// copyExtent copies m's ciphertext from source to destination, a chunk at a
// time, looking at ctx before each — one record can be many gigabytes, and a
// cancel that waits for the end of it is not a cancel — and reporting each
// through progress. done runs across the moves of one commit. The lock is
// not held.
func (tx *Tx) copyExtent(ctx context.Context, m Move, buf []byte, done *uint64, total uint64, progress func(done, total uint64)) error {
	a := tx.a
	for off := uint64(0); off < m.From.Len; {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := min(uint64(len(buf)), m.From.Len-off)
		got, err := a.f.ReadAt(buf[:n], int64(m.From.Off+off))
		if uint64(got) != n {
			if err == nil {
				err = corrupt("file %x: extent [0x%x, 0x%x) is short", m.ID, m.From.Off, end(m.From))
			}
			return err
		}
		if err := a.moveWrite(1, buf[:n], m.To.Off+off); err != nil {
			return err
		}
		off += n
		*done += n
		if progress != nil {
			progress(*done, total)
		}
	}
	return nil
}

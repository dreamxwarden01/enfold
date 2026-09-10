package archive

import (
	"bytes"
	"testing"
)

// The adversary's pass over ReclaimPlan.TailReturned, which reclaim.go
// documents as an estimate on the low side — a number the caller's rule
// (APP.md §2.3) may trust never to promise more than the run gives back.
// The layout below is where it promises more: the move commit's own index
// lands in the first hole above the new last live byte, the follow-up must
// place its extents above that index (trim.go: this commit's own two
// extents are the one thing that stands in the way), and the file ends one
// index and one map longer than the plan said. The next commit gives the
// rest back; the plan is wrong for the one it answers for.

// Two deleted files at the front whose extents together hold exactly the
// two live files behind them, a tiny live file and a deleted one between:
// the run fills the front hole to the byte, the commit's index takes the
// hole behind the tiny file's source, and the follow-up stops above it.
func TestTailReturnedOverPromisesWhenTheIndexLandsAboveTheNewTail(t *testing.T) {
	a, fx := newFixture(t, Options{NoCompression: true})
	tx, err := a.Begin()
	if err != nil {
		t.Fatal(err)
	}
	stage := func(name string, data []byte) FileInfo {
		t.Helper()
		f, err := tx.Add(ctx, root, name, bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	x := stage("x.bin", noise(100, 800))
	y := stage("y.bin", noise(50000, 801))
	aData := noise(100, 802)
	fa := stage("a.bin", aData)
	z := stage("z.bin", noise(3000, 803)) // its hole holds the commit's index (five records, over a KiB)
	bData := noise(50000, 804)
	fb := stage("b.bin", bData)
	if _, err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Compact(ctx, nil); err != nil {
		t.Fatal(err)
	}
	a = fx.open(t)
	tx, err = a.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range [][16]byte{x.ID, y.ID, z.ID} {
		if err := tx.Delete(id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	tighten(t, a)
	aAt, bAt := extentOf(t, a, fa.ID), extentOf(t, a, fb.ID)
	free, _, _ := spaces(a)
	if len(free.x) != 2 || end(free.x[0]) != aAt.Off || free.x[0].Len != aAt.Len+bAt.Len || free.x[1].Off != end(aAt) || end(free.x[1]) != bAt.Off {
		t.Fatalf("the layout is not [hole of a+b][a][hole][b]: free=%+v a=%+v b=%+v", free.x, aAt, bAt)
	}

	plan := a.PlanReclaim(0)
	if len(plan.Moves) != 2 || plan.Moves[0].ID != fa.ID || plan.Moves[1].ID != fb.ID || plan.NeedsPublish {
		t.Fatalf("plan %+v", plan)
	}
	if end(plan.Moves[1].To) != aAt.Off {
		t.Fatalf("the front hole is not filled to the byte: %+v", plan.Moves)
	}
	before, _, _ := a.Stat()
	if _, err := a.MoveExtents(ctx, plan.Moves, nil); err != nil {
		t.Fatal(err)
	}
	after, _, _ := a.Stat()
	readsAll(t, a, "after the moves", []wantFile{{fa.ID, aData}, {fb.ID, bData}})
	insideTheFile(t, a)
	disjoint(t, a)
	t.Logf("the plan promised %d bytes back; the file went %d → %d, %d back", plan.TailReturned, before, after, before-after)
	if after > before-plan.TailReturned {
		t.Errorf("TailReturned promised %d bytes and the run gave %d: the estimate is not on the low side", plan.TailReturned, before-after)
	}
}

// The same pass over the run-level figure, which the caller's rule now
// measures: RunTailReturned must never promise more than the whole run gives
// back. The dry run is on the low side by construction — the holes a step
// frees wait the commit R31 makes them wait and no longer, each step's map is
// taken to be no smaller than the one on disk, and the run's own index
// extents are not modelled as holes anyone may fill — and the proof is the
// run itself: the moves are made, on three layouts, and the file's size
// before less its size after is compared with what the plan promised.
func TestRunTailReturnedNeverPromisesMoreThanTheRunGives(t *testing.T) {
	for _, c := range []struct {
		what  string
		sizes []int
		drop  []int // the files deleted, by their place in sizes
	}{
		// The first of three equal files: one move, then the publish of what
		// it freed, then the move that gives the hole back.
		{"three equal files, the first deleted", []int{100000, 100000, 100000}, []int{0}},
		// A hole a smaller file cannot reach until the file that fits it
		// exactly has taken it and freed its own source.
		{"a hole spent to the byte, a smaller file behind it", []int{200000, 200000, 150000}, []int{0}},
		// Five holes under five files of one size: every step lowers each
		// file by one hole and the tail by one, and the run is five of them
		// with the empty commits between.
		{"five holes under five files", []int{40000, 40000, 40000, 40000, 40000, 40000, 40000, 40000, 40000, 40000}, []int{0, 2, 4, 6, 8}},
	} {
		t.Run(c.what, func(t *testing.T) {
			a, fx := newFixture(t, Options{NoCompression: true})
			a, files, data := endToEnd(t, fx, a, 950, c.sizes...)
			tx, err := a.Begin()
			if err != nil {
				t.Fatal(err)
			}
			gone := map[int]bool{}
			for _, i := range c.drop {
				if err := tx.Delete(files[i].ID); err != nil {
					t.Fatal(err)
				}
				gone[i] = true
			}
			if _, err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			tighten(t, a)
			var want []wantFile
			for i := range files {
				if !gone[i] {
					want = append(want, wantFile{files[i].ID, data[i]})
				}
			}
			before, _, _ := a.Stat()
			plan := a.PlanReclaim(0)
			commits := runReclaim(t, a)
			after, _, _ := a.Stat()
			t.Logf("the run promised %d bytes back for %d moved over %d commits; the file went %d → %d, %d back over %d commits",
				plan.RunTailReturned, plan.RunBytesToMove, plan.Commits, before, after, before-after, commits)
			if plan.RunTailReturned == 0 {
				t.Fatalf("the estimate promises nothing: %+v", plan)
			}
			if after > before-plan.RunTailReturned {
				t.Errorf("RunTailReturned promised %d bytes and the run gave %d: the estimate is not on the low side", plan.RunTailReturned, before-after)
			}
			if plan.RunTailReturned < plan.TailReturned {
				t.Errorf("the run promises %d back and its first commit %d", plan.RunTailReturned, plan.TailReturned)
			}
			readsAll(t, a, "after the run", want)
			insideTheFile(t, a)
			disjoint(t, a)
			if p := a.PlanReclaim(0); len(p.Moves) != 0 || p.RunTailReturned != 0 {
				t.Errorf("the run did not converge: %+v", p)
			}
		})
	}
}

// The budget is not a schedule laid over one plan: it is what decides where
// the moves land (the outside review of 2026-09-10, finding 1). A dry run
// that simulates one unbudgeted step where the caller will make several
// budgeted ones answers for a run nobody makes, and the caller's worth rule
// rests on it.
//
// The layout: [hole H][A][B][D], where the hole holds A with room enough
// behind it for D but not for B, and holds B whole. Unbudgeted, one step
// takes A into the front of the hole and D into what is left of it, B is
// passed over for want of room, and there the run ends — nothing is ever
// free below B again. Under a budget that A alone fills, the same step is
// the commit that moves A and stops; what is left of the hole stays where
// it is, the next plan finds it grown by the source A freed, and B moves
// into it — and then D into B's. The budgeted run moves every one of the
// three, half again what the unbudgeted dry run said, which is what the
// budgeted dry run must say.
func TestTheBudgetedEstimateAnswersForTheCommitsTheCallerMakes(t *testing.T) {
	a, fx := newFixture(t, Options{NoCompression: true})
	a, files, data := endToEnd(t, fx, a, 960, 90000, 40000, 60000, 40000)
	if _, err := a.Delete(ctx, files[0].ID); err != nil {
		t.Fatal(err)
	}
	tighten(t, a)
	aAt, bAt, dAt := extentOf(t, a, files[1].ID), extentOf(t, a, files[2].ID), extentOf(t, a, files[3].ID)
	hole := oneHole(t, a, aAt)
	switch {
	case hole.Len-aAt.Len < dAt.Len:
		t.Fatalf("the hole %+v leaves no room for D (%+v) behind A (%+v)", hole, dAt, aAt)
	case hole.Len-aAt.Len >= bAt.Len:
		t.Fatalf("the hole %+v holds B (%+v) behind A (%+v): a budget would change nothing", hole, bAt, aAt)
	case hole.Len < bAt.Len:
		t.Fatalf("the hole %+v does not hold B (%+v) even whole", hole, bAt)
	}
	// A commit's worth: A fills it, and A with D behind it is over it.
	budget := aAt.Len
	before, _, _ := a.Stat()

	loose, tight := a.PlanReclaim(0), a.PlanReclaim(budget)
	t.Logf("unbudgeted: %d moved, %d back over %d commits; budgeted: %d moved, %d back over %d commits",
		loose.RunBytesToMove, loose.RunTailReturned, loose.Commits,
		tight.RunBytesToMove, tight.RunTailReturned, tight.Commits)
	if loose.RunBytesToMove != aAt.Len+dAt.Len {
		t.Errorf("the unbudgeted dry run moves %d bytes; expected A and D, %d", loose.RunBytesToMove, aAt.Len+dAt.Len)
	}
	if tight.RunBytesToMove != aAt.Len+bAt.Len+dAt.Len {
		t.Errorf("the budgeted dry run moves %d bytes; expected A, B and D, %d", tight.RunBytesToMove, aAt.Len+bAt.Len+dAt.Len)
	}
	// The plan for the one commit is whole either way: the caller is the one
	// that takes the leading moves within its budget.
	if len(loose.Moves) != len(tight.Moves) || loose.BytesToMove != tight.BytesToMove {
		t.Errorf("the budget cut the commit's own plan: %d moves of %d bytes against %d of %d",
			len(tight.Moves), tight.BytesToMove, len(loose.Moves), loose.BytesToMove)
	}

	commits, moved := runReclaimBudget(t, a, budget)
	after, _, _ := a.Stat()
	t.Logf("the budgeted run made %d commits and moved %d bytes; the file went %d → %d, %d back", commits, moved, before, after, before-after)
	if moved != tight.RunBytesToMove {
		t.Errorf("the budgeted run moved %d bytes and the budgeted estimate said %d", moved, tight.RunBytesToMove)
	}
	if moved <= loose.RunBytesToMove+metadataSlack {
		t.Errorf("the run moved %d bytes and the unbudgeted estimate said %d: this layout no longer tests the gap", moved, loose.RunBytesToMove)
	}
	if after > before-tight.RunTailReturned {
		t.Errorf("RunTailReturned promised %d bytes and the run gave %d: the budgeted estimate is not on the low side", tight.RunTailReturned, before-after)
	}
	if got := extentOf(t, a, files[2].ID); got.Off >= bAt.Off {
		t.Errorf("B lies at %+v, where it was (%+v): the budgeted run should have moved it", got, bAt)
	}
	readsAll(t, a, "after the budgeted run", []wantFile{{files[1].ID, data[1]}, {files[2].ID, data[2]}, {files[3].ID, data[3]}})
	insideTheFile(t, a)
	disjoint(t, a)
}

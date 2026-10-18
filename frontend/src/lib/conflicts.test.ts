import { describe, expect, it } from "vitest";
import type { FileOutcome } from "./api";
import {
  allOf, collisionPlan, conflictsOf, restExtracted, continueLabel, hasConflicts, namesFor, planIsEmpty, reissuePlan, sameDateAndSize,
  shownName, skipSameLabel, startingDecisions, summarise, ticks, toggle, withSkipSame,
} from "./conflicts";
import type { ConflictRow } from "./conflicts";
import { collisionCopy, compareGoText, conflictCopy, conflictTitle } from "./strings";

// The conflicts an extract with the `ask` policy comes back with (APP.md
// §3, ruled 2026-09-10): extract what collides with nothing, then ask.
// Every outcome carries the record's id and the archive copy's size and
// date, so nothing is walked.

function outcome(over: Partial<FileOutcome> = {}): FileOutcome {
  return {
    path: "D:\\Extracted\\a.jpg", name: "a.jpg", isDir: false, outcome: "conflict",
    id: "1".repeat(32), size: 10, modifiedAt: 100,
    ...over,
  } as FileOutcome;
}

function conflict(over: Partial<ConflictRow> = {}): ConflictRow {
  return {
    id: "1".repeat(32), path: "a.jpg", dest: "D:\\Extracted\\a.jpg", name: "a.jpg",
    size: 10, modifiedAt: 100, existingSize: 20, existingModifiedAt: 200,
    ...over,
  };
}

describe("reading an operation's outcomes", () => {
  it("takes the conflicts and nothing else, each with both copies' figures", () => {
    const list = conflictsOf([
      outcome({ name: "photos/a.jpg", outcome: "extracted" }),
      outcome({
        id: "b".repeat(32), name: "photos/b.jpg", path: "D:\\X\\photos\\b.jpg", size: 7, modifiedAt: 33,
        existing: { size: 42, modifiedAt: 900 },
      }),
      outcome({ name: "photos/c.jpg", outcome: "failed" }),
    ]);
    expect(list).toHaveLength(1);
    expect(list[0]).toEqual({
      id: "b".repeat(32), path: "photos/b.jpg", dest: "D:\\X\\photos\\b.jpg", name: "b.jpg",
      size: 7, modifiedAt: 33, existingSize: 42, existingModifiedAt: 900,
    });
  });

  it("still asks about a conflict whose file went between the refusal and the stat", () => {
    const list = conflictsOf([outcome({ name: "a.jpg", existing: null })]);
    expect(list).toHaveLength(1);
    expect(list[0].existingSize).toBe(0);
    expect(list[0].id).toBe("1".repeat(32));
  });

  it("is empty for an operation that reported nothing", () => {
    expect(conflictsOf(null)).toEqual([]);
    expect(conflictsOf(undefined)).toEqual([]);
    expect(hasConflicts(null)).toBe(false);
  });

  it("says whether there is a question to ask at all", () => {
    expect(hasConflicts([outcome({ outcome: "extracted" })])).toBe(false);
    expect(hasConflicts([outcome({ outcome: "extracted" }), outcome()])).toBe(true);
  });
});

// The foot of the compare list (APP.md §3).
describe("the copies with the same date and size", () => {
  it("is both figures, not one", () => {
    expect(sameDateAndSize(conflict({ size: 10, existingSize: 10, modifiedAt: 5, existingModifiedAt: 5 }))).toBe(true);
    expect(sameDateAndSize(conflict({ size: 10, existingSize: 10, modifiedAt: 5, existingModifiedAt: 6 }))).toBe(false);
    expect(sameDateAndSize(conflict({ size: 11, existingSize: 10, modifiedAt: 5, existingModifiedAt: 5 }))).toBe(false);
  });

  it("counts them in the foot's tick, singular at one, and says nothing when there are none", () => {
    const same = conflict({ path: "s.jpg", size: 1, existingSize: 1, modifiedAt: 2, existingModifiedAt: 2 });
    expect(skipSameLabel([same, conflict()])).toBe("Skip 1 file with the same date and size");
    expect(skipSameLabel([same, { ...same, path: "t.jpg" }])).toBe("Skip 2 files with the same date and size");
    expect(skipSameLabel([conflict()])).toBe("");
  });

  it("starts them skipped and everything else replaced — the list is entered to update a folder (ruled 2026-10-17)", () => {
    const same = conflict({ path: "s.jpg", size: 1, existingSize: 1, modifiedAt: 2, existingModifiedAt: 2 });
    const other = conflict({ path: "o.jpg" });
    expect(startingDecisions([same, other])).toEqual({ "s.jpg": "skip", "o.jpg": "replace" });
  });

  it("is a tick that moves only the rows it is about", () => {
    const same = conflict({ path: "s.jpg", size: 1, existingSize: 1, modifiedAt: 2, existingModifiedAt: 2 });
    const other = conflict({ path: "o.jpg" });
    const rows = [same, other];
    const chosen = { "s.jpg": "skip", "o.jpg": "both" } as Record<string, "replace" | "skip" | "both">;
    expect(withSkipSame(rows, chosen, false)).toEqual({ "s.jpg": "replace", "o.jpg": "both" });
    expect(withSkipSame(rows, { ...chosen, "s.jpg": "replace" }, true)).toEqual({ "s.jpg": "skip", "o.jpg": "both" });
  });
});

// A tick on either side or both (APP.md §3): left is the archive's copy,
// right the destination's, both is keep both.
describe("the ticks", () => {
  it("read either way round", () => {
    expect(ticks("replace")).toEqual({ left: true, right: false });
    expect(ticks("skip")).toEqual({ left: false, right: true });
    expect(ticks("both")).toEqual({ left: true, right: true });
  });

  it("makes keep both out of one side and then the other", () => {
    expect(toggle("replace", "right")).toBe("both");
    expect(toggle("skip", "left")).toBe("both");
  });

  it("takes a side away again", () => {
    expect(toggle("both", "right")).toBe("replace");
    expect(toggle("both", "left")).toBe("skip");
  });

  it("leaves the destination's file where it is when neither side is ticked", () => {
    expect(toggle("replace", "left")).toBe("skip");
  });
});

// *Continue* re-issues Extract for the ids the ticks chose (APP.md §3).
describe("the re-issue", () => {
  const a = conflict({ path: "a.jpg", id: "a".repeat(32) });
  const b = conflict({ path: "b.jpg", id: "b".repeat(32) });
  const c = conflict({ path: "c.jpg", id: "c".repeat(32) });

  it("is one call for replace and one for keep both, and nothing for a skip", () => {
    const plan = reissuePlan([a, b, c], { "a.jpg": "replace", "b.jpg": "both", "c.jpg": "skip" });
    expect(plan).toEqual({ replace: ["a".repeat(32)], rename: ["b".repeat(32)] });
  });

  it("drops a conflict that came without an id: there is nothing to send", () => {
    const gone = conflict({ path: "g.jpg", id: "" });
    expect(reissuePlan([gone], { "g.jpg": "replace" })).toEqual({ replace: [], rename: [] });
  });

  it("is empty when everything was skipped, and nothing is sent", () => {
    expect(planIsEmpty(reissuePlan([a, b], allOf([a, b], "skip")))).toBe(true);
    expect(planIsEmpty(reissuePlan([a], allOf([a], "replace")))).toBe(false);
  });

  it("takes one answer over every row for Replace all and Skip all", () => {
    expect(allOf([a, b], "replace")).toEqual({ "a.jpg": "replace", "b.jpg": "replace" });
    expect(reissuePlan([a, b], allOf([a, b], "both")).rename).toHaveLength(2);
  });

  it("treats a row nobody decided about as the default, replace", () => {
    expect(reissuePlan([a], {}).replace).toEqual(["a".repeat(32)]);
  });
});

// Continue reads what it will do (direction 7; the 2026-10-17 count
// beside the foot's tick, moved into the button).
describe("Continue's label", () => {
  const a = conflict({ path: "a.jpg", id: "a".repeat(32) });
  const b = conflict({ path: "b.jpg", id: "b".repeat(32) });
  const c = conflict({ path: "c.jpg", id: "c".repeat(32) });
  const same = conflict({ path: "s.jpg", id: "s".repeat(32), size: 1, existingSize: 1, modifiedAt: 2, existingModifiedAt: 2 });

  it("starts by saying the differing rows will be replaced and the matching ones skipped", () => {
    const rows = [a, b, c, same, { ...same, path: "t.jpg", id: "t".repeat(32) }];
    expect(summarise(rows, startingDecisions(rows))).toEqual({ replace: 3, both: 0, skip: 2 });
    expect(continueLabel(rows, startingDecisions(rows))).toBe("Extract 3, skip 2");
  });

  it("follows the ticks as they change, keep both counted too", () => {
    const rows = [a, b, c];
    let d = startingDecisions(rows);
    d = { ...d, "b.jpg": toggle(d["b.jpg"], "right") };
    d = { ...d, "c.jpg": toggle(d["c.jpg"], "left") };
    expect(summarise(rows, d)).toEqual({ replace: 1, both: 1, skip: 1 });
    expect(continueLabel(rows, d)).toBe("Extract 2, skip 1");
  });

  it("leaves out a part with nothing in it", () => {
    expect(continueLabel([a, b], allOf([a, b], "skip"))).toBe("Skip 2");
    expect(continueLabel([a, b, c], allOf([a, b, c], "replace"))).toBe("Extract 3");
    expect(continueLabel([a, b], allOf([a, b], "both"))).toBe("Extract 2");
    expect(compareGoText({ replace: 0, both: 0, skip: 3 })).toBe("Skip 3");
  });

  it("counts a row with no id as skipped, since nothing is re-issued for it", () => {
    const gone = conflict({ path: "g.jpg", id: "" });
    expect(summarise([a, gone], {})).toEqual({ replace: 1, both: 0, skip: 1 });
  });
});

// A conflict met under a name a Shorten or Rename… chose is re-issued
// under that name, not the record's own (APP.md §3; the review's finding
// 7: the re-issue passed null and met the refusal again).
describe("the names a re-issue carries", () => {
  const inForce = { ["a".repeat(32)]: "IMG.HEIC", ["c".repeat(32)]: "n.txt" };

  it("are the ones in force for the ids re-issued, and no other", () => {
    expect(namesFor(["a".repeat(32), "b".repeat(32)], inForce)).toEqual({ ["a".repeat(32)]: "IMG.HEIC" });
  });

  it("are null when the extract carried none, or none for these ids", () => {
    expect(namesFor(["a".repeat(32)], null)).toBeNull();
    expect(namesFor(["a".repeat(32)], undefined)).toBeNull();
    expect(namesFor(["b".repeat(32)], inForce)).toBeNull();
    expect(namesFor([], inForce)).toBeNull();
  });
});

// The question asked before the compare list (APP.md §3), from the one
// strings table (§7).
describe("the question", () => {
  const dest = "D:\\Archives\\Photos 2024";

  it("names the file and the place when there is one", () => {
    expect(conflictTitle(1, "itinerary.pdf", dest)).toBe("A file named itinerary.pdf is already in D:\\Archives\\Photos 2024");
  });

  it("counts them when there are several", () => {
    expect(conflictTitle(3, "a", dest)).toBe("3 files with the same names are already in D:\\Archives\\Photos 2024");
    expect(conflictTitle(2, "a", "")).toBe("2 files with the same names are already in the destination");
  });

  // Explorer's "Replace or Skip Files": three rows of equal weight, each a
  // strong line and what it does.
  it("says each row's consequence, counted", () => {
    expect(conflictCopy.replace(3)).toBe("Replace the files in the destination");
    expect(conflictCopy.replaceSays(3)).toBe("The 3 files already there are overwritten.");
    expect(conflictCopy.skip(3)).toBe("Skip these files");
    expect(conflictCopy.skipSays(3, 10)).toBe("The 3 files stay as they are in the destination; the rest were extracted.");
    expect(conflictCopy.decide(3)).toBe("Let me decide for each file");
    expect(conflictCopy.decideSays(3)).toBe("Compare the two copies side by side.");
  });

  it("says one file in the singular, and no rest when nothing else was written", () => {
    expect(conflictCopy.replace(1)).toBe("Replace the file in the destination");
    expect(conflictCopy.replaceSays(1)).toBe("The file already there is overwritten.");
    expect(conflictCopy.skip(1)).toBe("Skip this file");
    expect(conflictCopy.skipSays(1, 0)).toBe("The file stays as it is in the destination.");
    expect(conflictCopy.decide(1)).toBe("Compare both files");
  });
});

// Adding names that exist (APP.md §3): the same shape, the folder named,
// the kinds counted — a collision carries no size or date.
describe("the add collisions", () => {
  const files = { files: 2, folders: 0 };

  it("counts what is already there by its kind, and names the folder", () => {
    expect(collisionCopy.title(files, "a.jpg", "Photos 2024")).toBe("2 files with the same names are already in Photos 2024");
    expect(collisionCopy.title({ files: 1, folders: 0 }, "a.jpg", "2024")).toBe("A file named a.jpg is already in 2024");
    expect(collisionCopy.title({ files: 0, folders: 1 }, "Trips", "Photos 2024")).toBe("A folder named Trips is already in Photos 2024");
    expect(collisionCopy.title({ files: 1, folders: 1 }, "a", "Photos 2024")).toBe("2 items with the same names are already in Photos 2024");
  });

  it("says each name's kind, both kinds when they differ", () => {
    expect(collisionCopy.kind(false, false)).toBe("File");
    expect(collisionCopy.kind(true, true)).toBe("Folder; the two are merged");
    expect(collisionCopy.kind(true, false)).toBe("A file here; a folder is being added");
    expect(collisionCopy.kind(false, true)).toBe("A folder here; a file is being added");
  });

  it("words the rows", () => {
    expect(collisionCopy.replace(files, "Photos 2024")).toBe("Replace the 2 files in Photos 2024");
    expect(collisionCopy.replace({ files: 1, folders: 0 }, "Photos 2024")).toBe("Replace the file in Photos 2024");
    expect(collisionCopy.replaceSays(2)).toBe("The copies already in the archive are overwritten.");
    expect(collisionCopy.keepBothSays(2)).toBe("The new ones get a number in their names.");
    expect(collisionCopy.skip(files)).toBe("Skip these files");
    expect(collisionCopy.skipSays(files, 3)).toBe("The 2 files already here stay; the rest are added.");
    expect(collisionCopy.skipSays({ files: 1, folders: 0 }, 0)).toBe("The file already here stays; nothing is added.");
  });
});

// A folder that meets a folder is merged whatever the policy (internal/app
// ops.go planNodes), so the rows never say it is overwritten, numbered or
// left out: they count the clashes alone, and a note says the merge.
describe("collisionPlan", () => {
  const file = (name: string) => ({ name, isDir: false, existingIsDir: false });
  const folder = (name: string) => ({ name: `${name}/`, isDir: true, existingIsDir: true });

  it("speaks of files alone when only files meet", () => {
    const p = collisionPlan([file("a.jpg"), file("b.jpg")], 3, "Photos 2024");
    expect(p.title).toBe("2 files with the same names are already in Photos 2024");
    expect(p.note).toBe("");
    expect(p.replace).toEqual({ label: "Replace the 2 files in Photos 2024", says: "The copies already in the archive are overwritten.", off: undefined });
    expect(p.keepBoth.says).toBe("The new ones get a number in their names.");
    expect(p.skip).toEqual({ label: "Skip these files", says: "The 2 files already here stay; the rest are added." });
  });

  it("says a folder-only batch as a merge, its rows for the files inside", () => {
    const p = collisionPlan([folder("Trips")], 1, "Photos 2024");
    expect(p.title).toBe("A folder named Trips is already in Photos 2024");
    expect(p.note).toBe("The folder is merged with the one already here. The choice below is for any file inside it with the same name as one already there.");
    expect(p.replace).toEqual({ label: "Replace files with the same names", says: "Those copies already in the archive are overwritten." });
    expect(p.keepBoth).toEqual({ label: "Keep both", says: "The new ones get a number in their names." });
    expect(p.skip).toEqual({ label: "Skip files with the same names", says: "Those copies already in the archive stay; the other files are added." });
    expect(collisionPlan([folder("Trips"), folder("2024")], 2, "Photos 2024").note).toBe(
      "The 2 folders are merged with the ones already here. The choice below is for any file inside them with the same name as one already there.",
    );
  });

  it("counts only the clashes in a mixed batch, and notes the merge", () => {
    const p = collisionPlan([file("a.jpg"), folder("Trips"), file("b.jpg")], 3, "Photos 2024");
    expect(p.title).toBe("3 items with the same names are already in Photos 2024");
    expect(p.note).toBe("The folder of the same name is merged with the one already here, and the choice below also applies to the files inside it.");
    expect(p.replace.label).toBe("Replace the 2 files in Photos 2024");
    expect(p.replace.off).toBeUndefined();
    expect(p.skip.says).toBe("The 2 files already here stay; the rest are added.");
  });

  it("greys Replace when a file meets a folder, and nothing else", () => {
    const clash = { name: "2024", isDir: false, existingIsDir: true };
    const p = collisionPlan([clash, file("a.jpg"), folder("Trips")], 3, "Photos 2024");
    expect(p.replace.off).toBe("A file and a folder never replace one another.");
    expect(p.replace.label).toBe("Replace the 2 items in Photos 2024");
    expect(p.skip.says).toBe("The 2 items already here stay; the rest are added.");
    expect(collisionPlan([file("a.jpg")], 1, "2024").skip.says).toBe("The file already here stays; nothing is added.");
  });

  it("names a folder without the mark it was offered with", () => {
    expect(shownName("Trips/")).toBe("Trips");
    expect(shownName("a.jpg")).toBe("a.jpg");
  });
});

// The conflict question's Skip says "the rest were extracted" only when the
// rest all were: a refusal or a failure in the same op drops the clause.
describe("restExtracted", () => {
  const o = (outcome: string) => ({ outcome } as FileOutcome);
  it("counts the files written beside the conflicts", () => {
    expect(restExtracted([o("extracted"), o("created"), o("conflict"), o("extracted")])).toBe(2);
    expect(restExtracted([o("conflict")])).toBe(0);
    expect(restExtracted(null)).toBe(0);
  });
  it("is 0 when the op also refused or failed something", () => {
    expect(restExtracted([o("extracted"), o("conflict"), o("name_refused")])).toBe(0);
    expect(restExtracted([o("extracted"), o("conflict"), o("path_refused")])).toBe(0);
    expect(restExtracted([o("extracted"), o("conflict"), o("failed")])).toBe(0);
  });
});

import { describe, expect, it } from "vitest";
import type { FileOutcome } from "./api";
import {
  allOf, conflictsOf, hasConflicts, planIsEmpty, reissuePlan, sameDateAndSize,
  skipSameLabel, startingDecisions, ticks, toggle, withSkipSame,
} from "./conflicts";
import type { ConflictRow } from "./conflicts";
import { conflictTitle } from "./strings";

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

  it("starts them skipped and everything else replaced — the dialog's own default", () => {
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

// The question asked before the compare list (APP.md §3), from the one
// strings table (§7).
describe("the question", () => {
  it("names the file when there is one", () => {
    expect(conflictTitle(1, "IMG_7201.HEIC")).toBe("The destination already has a file named IMG_7201.HEIC");
  });

  it("counts them when there are several", () => {
    expect(conflictTitle(2, "a")).toBe("The destination has 2 files with the same names");
  });
});

import { describe, expect, it } from "vitest";
import type { FileOutcome } from "./api";
import { Code } from "./api";
import { codeText } from "./strings";
import { hasTrouble, summaryLine, tally, troubles } from "./results";

function out(o: Partial<FileOutcome>): FileOutcome {
  return { path: "", name: "", isDir: false, outcome: "added", ...o };
}

const results: FileOutcome[] = [
  out({ name: "a.jpg", outcome: "added" }),
  out({ name: "b.jpg", outcome: "added" }),
  out({ name: "c.jpg", outcome: "replaced" }),
  out({ name: "Trips", isDir: true, outcome: "created" }),
  out({ name: "2024", isDir: true, outcome: "entered" }),
  out({ name: "Photos", isDir: true, outcome: "failed", code: Code.CodeKindMismatch }),
  out({ name: "deep", isDir: true, outcome: "failed", code: Code.CodeTreeBounds }),
  out({ path: "old/notes.md", outcome: "skipped" }),
];

describe("what a batch reported", () => {
  it("counts every outcome, folders among them", () => {
    expect(tally(results)).toEqual({ added: 2, replaced: 1, created: 1, entered: 1, skipped: 1, extracted: 0, failed: 2 });
    expect(tally(null)).toEqual({ added: 0, replaced: 0, created: 0, entered: 0, skipped: 0, extracted: 0, failed: 0 });
  });

  it("counts created and entered folders beside the added files", () => {
    const line = summaryLine(tally(results));
    expect(line).toBe("2 files added · 1 replaced · 1 folder created · 1 folder entered · 1 skipped · 2 failed");
  });

  it("says nothing when nothing was reported", () => {
    expect(summaryLine(tally([]))).toBe("");
  });

  it("counts an extraction's files", () => {
    expect(summaryLine(tally([out({ outcome: "extracted" }), out({ outcome: "extracted" })]))).toBe("2 files extracted");
  });

  it("lists what failed and what was skipped, with the code's own copy", () => {
    const t = troubles(results);
    expect(t).toHaveLength(3);
    expect(t[0]).toEqual({ name: "Photos", isDir: true, outcome: "failed", text: codeText(Code.CodeKindMismatch) });
    expect(t[1].text).toBe(codeText(Code.CodeTreeBounds));
    expect(t[1].text).not.toMatch(/^Error:/);
    // A skipped item carries no code; it is named from its path.
    expect(t[2].name).toBe("notes.md");
    expect(t[2].text).toBeTruthy();
  });

  it("counts only its own seven counters", () => {
    // An outcome word naming a member of Object's prototype is not one of
    // them: it must leave every count a number.
    const odd = tally([out({ outcome: "constructor" }), out({ outcome: "toString" }), out({ outcome: "added" })]);
    expect(odd).toEqual({ added: 1, replaced: 0, created: 0, entered: 0, skipped: 0, extracted: 0, failed: 0 });
    expect(summaryLine(odd)).toBe("1 file added");
  });

  it("gives a codeless failure a reason of its own", () => {
    const t = troubles([out({ name: "x.bin", outcome: "failed" })]);
    expect(t[0].text).toBeTruthy();
    expect(t[0].text).not.toBe("");
  });

  it("has nothing to show when everything went in", () => {
    expect(hasTrouble([out({ outcome: "added" }), out({ outcome: "created", isDir: true })])).toBe(false);
    expect(hasTrouble(results)).toBe(true);
    expect(troubles([])).toEqual([]);
  });
});

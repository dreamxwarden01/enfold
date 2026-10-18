import { describe, expect, it } from "vitest";
import type { FileOutcome } from "./api";
import { Code } from "./api";
import { codeText } from "./strings";
import { detailsText, hasTrouble, resultsTitle, rowName, summaryLine, tally, troubles } from "./results";

function out(o: Partial<FileOutcome>): FileOutcome {
  return { path: "", name: "", isDir: false, outcome: "added", size: 0, modifiedAt: 0, ...o };
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
    expect(t[0]).toEqual({ name: "Photos", isDir: true, outcome: "failed", text: codeText(Code.CodeKindMismatch), path: "" });
    expect(t[1].text).toBe(codeText(Code.CodeTreeBounds));
    expect(t[1].text).not.toMatch(/Something went wrong/); // a known code has its own copy, never the fallback
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

// The results dialog (direction 4, phase 3): the title is the outcome,
// counted, never "What happened"; each row is "name: the code's copy",
// with Show in Explorer for a file on disk.
describe("the results dialog", () => {
  const added = [
    out({ name: "a.jpg", path: "D:\\Pictures\\a.jpg", outcome: "added" }),
    out({ name: "b.jpg", path: "D:\\Pictures\\b.jpg", outcome: "added" }),
    out({ name: "thumbs.db", path: "D:\\Pictures\\thumbs.db", outcome: "failed", code: Code.CodeIO }),
    out({ name: "desktop.ini", path: "D:\\Pictures\\desktop.ini", outcome: "failed", code: Code.CodeSourceChanged }),
  ];

  it("titles itself with the outcome, counted", () => {
    expect(resultsTitle("add", added)).toBe("2 files were not added");
    expect(resultsTitle("add", [out({ name: "x", outcome: "skipped" })])).toBe("1 file was skipped");
    expect(resultsTitle("add", [...added, out({ name: "c.jpg", outcome: "skipped" })])).toBe("2 files were not added and 1 was skipped");
    expect(resultsTitle("extract", [out({ name: "x", outcome: "failed", code: Code.CodeIO })])).toBe("1 file was not extracted");
    expect(resultsTitle("replace", [out({ name: "x", outcome: "failed", code: Code.CodeIO })])).toBe("1 file was not replaced");
  });

  it("counts folders as folders, and both as items", () => {
    expect(resultsTitle("add", [out({ name: "Trips", isDir: true, outcome: "failed", code: Code.CodeKindMismatch })])).toBe("1 folder was not added");
    expect(resultsTitle("add", [out({ name: "Trips", isDir: true, outcome: "failed" }), out({ name: "x", outcome: "failed" })])).toBe("2 items were not added");
  });

  it("names a folder as one, and puts the code's copy after a colon", () => {
    const [dir] = troubles([out({ name: "Trips", isDir: true, outcome: "failed", code: Code.CodeKindMismatch })], "add");
    expect(rowName(dir)).toBe("Trips (folder)");
    const rows = troubles(added, "add");
    expect(detailsText("2 files were not added", "2 files added · 2 failed", rows)).toBe(
      ["2 files were not added", "2 files added · 2 failed", "", `thumbs.db: ${codeText(Code.CodeIO)}`, `desktop.ini: ${codeText(Code.CodeSourceChanged)}`].join("\n"),
    );
    expect(detailsText("1 file was skipped", "", [])).toBe("1 file was skipped\n");
  });

  it("offers Show in Explorer for an add's source, never for an archive path", () => {
    const rows = troubles(added, "add");
    expect(rows.map((r) => r.path)).toEqual(["D:\\Pictures\\thumbs.db", "D:\\Pictures\\desktop.ini"]);
    // An extract's failed destination was not written; its skipped one is
    // the file already there.
    const ex = troubles([out({ name: "2024/a.jpg", path: "D:\\Out\\2024\\a.jpg", outcome: "failed", code: Code.CodeIO }), out({ name: "2024/b.jpg", path: "D:\\Out\\2024\\b.jpg", outcome: "skipped" })], "extract");
    expect(ex.map((r) => r.path)).toEqual(["", "D:\\Out\\2024\\b.jpg"]);
    // A path this platform cannot open, or none, shows nothing.
    expect(troubles([out({ name: "x", path: "/Volumes/a/x", outcome: "failed" })], "add")[0].path).toBe("");
    expect(troubles([out({ name: "x", outcome: "failed" })], "add")[0].path).toBe("");
  });

  it("says a skip without a colon inside its own copy", () => {
    const [t] = troubles([out({ name: "a.jpg", outcome: "skipped" })]);
    expect(t.text).not.toMatch(/:/);
  });
});

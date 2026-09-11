import { describe, expect, it } from "vitest";
import { Code } from "./api";
import type { FileOutcome } from "./api";
import { hasRefusals, nextUndecided, planIsEmpty, refusedOf, refusedPlan, shorten, skipAllLike, splitName } from "./refused";
import type { RefusedRow } from "./refused";

// A name the destination refuses (APP.md §3, ruled 2026-09-10): the page
// asks per refused record — Shorten, Rename…, Skip — and re-issues with
// `names`. Shorten keeps the extension whole and halves the stem at rune
// boundaries, never inside a surrogate pair, down to one rune.

function outcome(over: Partial<FileOutcome> = {}): FileOutcome {
  return {
    path: "D:\\Out\\photos\\a very long name indeed.jpg", name: "photos/a very long name indeed.jpg", isDir: false,
    outcome: "name_refused", code: Code.CodeFileNameRefused, id: "1".repeat(32), size: 10, modifiedAt: 100,
    ...over,
  } as FileOutcome;
}

describe("the extension rule", () => {
  it("is the part after the last dot", () => {
    expect(splitName("a.tar.gz")).toEqual({ stem: "a.tar", ext: ".gz" });
    expect(splitName("IMG_7201.HEIC")).toEqual({ stem: "IMG_7201", ext: ".HEIC" });
  });

  it("is none when the dot is first or there is none", () => {
    expect(splitName(".env")).toEqual({ stem: ".env", ext: "" });
    expect(splitName("README")).toEqual({ stem: "README", ext: "" });
  });
});

describe("shorten", () => {
  it("halves the stem and keeps the extension whole", () => {
    expect(shorten("abcdefgh.jpeg")).toBe("abcd.jpeg");
    expect(shorten("abcd.jpeg")).toBe("ab.jpeg");
    expect(shorten("ab.jpeg")).toBe("a.jpeg");
  });

  it("stops at one rune", () => {
    expect(shorten("a.jpeg")).toBeNull();
    expect(shorten("a")).toBeNull();
    expect(shorten("")).toBeNull();
  });

  it("halves at rune boundaries and never inside a surrogate pair", () => {
    // Four astral characters, two code units each: halved is two of
    // them, whole, and never a lone surrogate.
    const emoji = "\u{1F600}\u{1F601}\u{1F602}\u{1F603}";
    const out = shorten(`${emoji}.txt`);
    expect(out).toBe("\u{1F600}\u{1F601}.txt");
    expect(out?.isWellFormed()).toBe(true);
    // Three: halved is one, not one and a half.
    expect(shorten("\u{1F600}\u{1F601}\u{1F602}.txt")).toBe("\u{1F600}.txt");
    // And the last step of an astral stem is the one rune.
    expect(shorten("\u{1F600}\u{1F601}.txt")).toBe("\u{1F600}.txt");
    expect(shorten("\u{1F600}.txt")).toBeNull();
  });

  it("odd stems round down, so the name always gets shorter", () => {
    expect(shorten("abcde.txt")).toBe("ab.txt");
    expect(shorten("abc.txt")).toBe("a.txt");
  });

  it("drops a trailing space or dot the halving leaves, which R20 refuses", () => {
    expect(shorten("ab  cd.txt")).toBe("ab.txt");
    expect(shorten("a.tar.gz")).toBe("a.gz"); // "a." halved from "a.tar"
    expect(shorten("2024-07-14 Reykjavik to Vik.HEIC")).toBe("2024-07-14 Re.HEIC");
  });

  it("finds the first real rune when the half is spaces and dots alone", () => {
    expect(shorten(". x.txt")).toBe("x.txt");
  });

  it("never halves its way down to a name Windows reserves for a device", () => {
    expect(shorten("console.txt")).not.toBe("con.txt");
    expect(shorten("console.txt")).toBe("c.txt"); // "con" is halved again
    expect(shorten("aux1234.log")).toBe("a.log"); // "aux" is halved again
  });

  it("treats a dotfile as a stem with no extension", () => {
    expect(shorten(".gitignore")).toBe(".giti");
  });
});

describe("reading an operation's outcomes", () => {
  it("takes the two refusals and nothing else, naming the element that was refused", () => {
    const rows = refusedOf([
      outcome({ outcome: "extracted", code: undefined }),
      outcome(),
      outcome({ id: "2".repeat(32), outcome: "path_refused", code: Code.CodeFilePathRefused, isDir: true, name: "photos/deep", path: "D:\\Out\\photos\\deep" }),
      outcome({ outcome: "conflict", code: undefined }),
    ]);
    expect(rows).toEqual([
      { id: "1".repeat(32), kind: "name", path: "photos/a very long name indeed.jpg", leaf: "a very long name indeed.jpg", isDir: false },
      { id: "2".repeat(32), kind: "path", path: "photos/deep", leaf: "deep", isDir: true },
    ]);
    expect(hasRefusals([outcome()])).toBe(true);
    expect(hasRefusals([outcome({ outcome: "extracted" })])).toBe(false);
    expect(hasRefusals(null)).toBe(false);
  });

  it("reads the refused element off the path attempted, so a second Shorten halves the shortened name", () => {
    const rows = refusedOf([outcome({ path: "D:\\Out\\photos\\a very long.jpg" })]);
    expect(rows[0].leaf).toBe("a very long.jpg");
    expect(shorten(rows[0].leaf)).toBe("a ver.jpg");
  });
});

describe("the re-issue", () => {
  const a: RefusedRow = { id: "a".repeat(32), kind: "name", path: "a very long name.jpg", leaf: "a very long name.jpg", isDir: false };
  const b: RefusedRow = { id: "b".repeat(32), kind: "name", path: "b.jpg", leaf: "b.jpg", isDir: false };
  const c: RefusedRow = { id: "c".repeat(32), kind: "path", path: "deep", leaf: "deep", isDir: true };

  it("carries the chosen ids and the name each is written under", () => {
    const plan = refusedPlan([a, b, c], {
      [a.id]: { kind: "shorten" },
      [b.id]: { kind: "rename", to: "beta.jpg" },
      [c.id]: { kind: "skip" },
    });
    expect(plan.ids).toEqual([a.id, b.id]);
    expect(plan.names).toEqual({ [a.id]: "a very l.jpg", [b.id]: "beta.jpg" });
    expect(planIsEmpty(plan)).toBe(false);
  });

  it("re-issues nothing for a skip, an undecided row, or a Shorten with nothing left", () => {
    expect(planIsEmpty(refusedPlan([a, b], {}))).toBe(true);
    expect(planIsEmpty(refusedPlan([b], { [b.id]: { kind: "shorten" } }))).toBe(true);
    expect(planIsEmpty(refusedPlan([{ ...a, id: "" }], { "": { kind: "shorten" } }))).toBe(true);
  });

  it("skips every undecided row of one kind and leaves the rest", () => {
    const d = skipAllLike([a, c, { ...c, id: "d".repeat(32) }], { [a.id]: { kind: "shorten" } }, "path");
    expect(d[a.id]).toEqual({ kind: "shorten" });
    expect(d[c.id]).toEqual({ kind: "skip" });
    expect(d["d".repeat(32)]).toEqual({ kind: "skip" });
    expect(nextUndecided([a, c, { ...c, id: "d".repeat(32) }], d)).toBe(-1);
  });

  it("asks about the first undecided row", () => {
    expect(nextUndecided([a, b], {})).toBe(0);
    expect(nextUndecided([a, b], { [a.id]: { kind: "skip" } })).toBe(1);
    expect(nextUndecided([a, b], { [a.id]: { kind: "skip" }, [b.id]: { kind: "skip" } })).toBe(-1);
  });
});

import { describe, expect, it } from "vitest";
import { foreignPath, renameSelection } from "./paths";

describe("foreignPath", () => {
  it("accepts what this platform could open: a drive letter, in either slash", () => {
    expect(foreignPath("D:\\Archives\\photos-2024.efd")).toBe(false);
    expect(foreignPath("D:/Archives/photos-2024.efd")).toBe(false);
    expect(foreignPath("c:\\a.efd")).toBe(false);
  });

  it("accepts a UNC path: this platform's syntax, whatever else is said about it", () => {
    // Not probed for presence (APP.md §13) — but Show in Explorer works.
    expect(foreignPath("\\\\nas\\media\\a.efd")).toBe(false);
    expect(foreignPath("//nas/media/a.efd")).toBe(false);
  });

  it("calls a path written by another system foreign", () => {
    expect(foreignPath("/Users/me/Archives/a.efd")).toBe(true);
    expect(foreignPath("/mnt/photos/a.efd")).toBe(true);
    expect(foreignPath("~/a.efd")).toBe(true);
  });

  it("calls anything that is not an absolute path foreign, and says nothing about no path", () => {
    expect(foreignPath("a.efd")).toBe(true);
    expect(foreignPath("D:a.efd")).toBe(true); // a drive-relative path opens nothing here
    expect(foreignPath("\\a.efd")).toBe(true); // rooted on no drive
    expect(foreignPath("")).toBe(false); // there is no path to say anything about
  });
});

// Rename's field opens with the stem selected (APP.md §6).
describe("renameSelection", () => {
  it("selects a file's stem, the part before the last dot", () => {
    expect(renameSelection("notes.md", false)).toEqual([0, 5]);
    expect(renameSelection("a.tar.gz", false)).toEqual([0, 5]);
  });

  it("selects the whole name when there is no dot, or the dot comes first", () => {
    expect(renameSelection("README", false)).toEqual([0, 6]);
    expect(renameSelection(".env", false)).toEqual([0, 4]);
    expect(renameSelection("", false)).toEqual([0, 0]);
  });

  it("selects a folder's whole name, dot and all", () => {
    expect(renameSelection("v1.2", true)).toEqual([0, 4]);
  });
});

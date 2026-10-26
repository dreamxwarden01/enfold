import { describe, expect, it } from "vitest";
import { clipOf, pastePlan, planOf } from "./paste";
import type { Clipboard as ClipboardView } from "./api";

// Paste (ruled 2026-10-26; APP.md §6): the core's answer read for the menu
// and turned into the add a drop runs. The real clipboard is never read
// here: the answers are built by hand.
const clip = (files: { path: string; isDir: boolean }[] | null, move = false): ClipboardView => ({ files, move }) as ClipboardView;

describe("planOf", () => {
  it("splits the paths by the shell's word on each, in order", () => {
    expect(planOf(["D:/a.txt", "D:/Photos", "D:/b.txt"], [false, true, false])).toEqual({ files: ["D:/a.txt", "D:/b.txt"], dirs: ["D:/Photos"] });
  });

  it("reads a path with no word on it as a file, as a drop does", () => {
    expect(planOf(["D:/a", "D:/b"], [true])).toEqual({ files: ["D:/b"], dirs: ["D:/a"] });
    expect(planOf(["D:/a"])).toEqual({ files: ["D:/a"], dirs: [] });
  });
});

describe("clipOf", () => {
  it("is unknown for a read that failed", () => {
    expect(clipOf(null)).toBe("unknown");
  });

  it("is empty when the clipboard names no files, whatever the move bit says", () => {
    expect(clipOf(clip([]))).toBe("empty");
    expect(clipOf(clip(null))).toBe("empty");
    expect(clipOf(clip([], true))).toBe("empty");
  });

  it("is a copy or a move, as Explorer left it", () => {
    expect(clipOf(clip([{ path: "D:/a.txt", isDir: false }]))).toBe("copy");
    expect(clipOf(clip([{ path: "D:/a.txt", isDir: false }], true))).toBe("move");
  });
});

describe("pastePlan", () => {
  it("is nothing for an empty clipboard", () => {
    expect(pastePlan(clip([]))).toBeNull();
    expect(pastePlan(clip(null))).toBeNull();
    expect(pastePlan(clip([{ path: "", isDir: false }]))).toBeNull();
  });

  it("adds the files and the folders the clipboard names", () => {
    const c = clip([
      { path: "D:/Pictures/IMG_0001.HEIC", isDir: false },
      { path: "D:/Pictures/2024", isDir: true },
    ]);
    expect(pastePlan(c)).toEqual({ files: ["D:/Pictures/IMG_0001.HEIC"], dirs: ["D:/Pictures/2024"] });
  });

  it("adds a cut as copied: the move bit plays no part", () => {
    const files = [{ path: "D:/a.txt", isDir: false }];
    expect(pastePlan(clip(files, true))).toEqual(pastePlan(clip(files, false)));
  });
});

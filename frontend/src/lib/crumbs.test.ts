import { describe, expect, it } from "vitest";
import { CRUMB_CAP, MENU_ROWS, MENU_SLACK, ROOT_CAP, capOf, clampFolded, fold, menuCapped, menuRows } from "./crumbs";

const gaps = { sep: 20, more: 28 };
const path = (n: number): string[] => Array.from({ length: n }, (_, i) => (i === 0 ? "Photos 2024" : `Folder ${i}`));

describe("fold", () => {
  it("shows a whole path that fits", () => {
    // 100 + 20 + 50 + 20 + 60 = 250
    expect(fold(path(3), [100, 50, 60], 250, gaps)).toEqual({ shown: [0, 1, 2], folded: [] });
  });

  it("never folds the root or the folder shown, however narrow", () => {
    expect(fold(path(1), [300], 10, gaps)).toEqual({ shown: [0], folded: [] });
    expect(fold(path(2), [300, 240], 10, gaps)).toEqual({ shown: [0, 1], folded: [] });
    // Three deep with no room: only the middle folds.
    expect(fold(path(3), [300, 100, 240], 10, gaps)).toEqual({ shown: [0, 2], folded: [1] });
  });

  it("adds parents back from the folder shown outward while they fit", () => {
    // Seven deep, every crumb 100: root + sep + more + sep + current = 268;
    // each parent costs 120.
    const widths = [100, 100, 100, 100, 100, 100, 100];
    expect(fold(path(7), widths, 268, gaps)).toEqual({ shown: [0, 6], folded: [1, 2, 3, 4, 5] });
    expect(fold(path(7), widths, 388, gaps)).toEqual({ shown: [0, 5, 6], folded: [1, 2, 3, 4] });
    expect(fold(path(7), widths, 507, gaps)).toEqual({ shown: [0, 5, 6], folded: [1, 2, 3, 4] });
    expect(fold(path(7), widths, 508, gaps)).toEqual({ shown: [0, 4, 5, 6], folded: [1, 2, 3] });
  });

  it("folds one run under the root: a parent that does not fit stops the walk", () => {
    // The parent next to the folder shown is wide; a narrower one further
    // out would fit on its own, but a gap in the path is never drawn.
    const widths = [100, 40, 40, 240, 100];
    expect(fold(path(5), widths, 268 + 100, gaps)).toEqual({ shown: [0, 4], folded: [1, 2, 3] });
  });

  it("shows the whole path rather than an ellipsis standing in for nothing", () => {
    // 4 crumbs of 100 and 3 separators = 460.
    expect(fold(path(4), [100, 100, 100, 100], 460, gaps)).toEqual({ shown: [0, 1, 2, 3], folded: [] });
    expect(fold(path(4), [100, 100, 100, 100], 459, gaps).folded.length).toBeGreaterThan(0);
  });

  it("takes an unmeasured width as nothing rather than failing", () => {
    expect(fold(path(3), [Number.NaN, 50, 60], 200, gaps)).toEqual({ shown: [0, 1, 2], folded: [] });
  });

  it("an empty path is nothing", () => {
    expect(fold([], [], 500)).toEqual({ shown: [], folded: [] });
  });
});

describe("the ellipsis menu's height", () => {
  it("shows a short list whole, the slack included", () => {
    expect(menuRows(1)).toBe(1);
    expect(menuRows(MENU_ROWS)).toBe(MENU_ROWS);
    expect(menuRows(MENU_ROWS + MENU_SLACK)).toBe(MENU_ROWS + MENU_SLACK);
    expect(menuCapped(MENU_ROWS + MENU_SLACK)).toBe(false);
  });

  it("caps a longer list at the rows and half a row, and scrolls", () => {
    expect(menuRows(MENU_ROWS + MENU_SLACK + 1)).toBe(MENU_ROWS + 0.5);
    expect(menuRows(40)).toBe(MENU_ROWS + 0.5);
    expect(menuCapped(40)).toBe(true);
  });

  it("has no rows for nothing", () => {
    expect(menuRows(0)).toBe(0);
    expect(menuRows(Number.NaN)).toBe(0);
  });
});

describe("capOf", () => {
  it("cuts the root at 320 and a folder at 240", () => {
    expect(capOf(0)).toBe(ROOT_CAP);
    expect(ROOT_CAP).toBe(320);
    expect(capOf(1)).toBe(CRUMB_CAP);
    expect(capOf(6)).toBe(240);
  });
});

describe("clampFolded", () => {
  it("keeps a fold that still fits the path drawn", () => {
    expect(clampFolded(3, 7)).toBe(3);
    expect(clampFolded(5, 7)).toBe(5);
  });
  it("never folds the folder shown when the path gets shorter before it is measured", () => {
    // Seven deep, five folded; then one deep, or three.
    expect(clampFolded(5, 1)).toBe(0);
    expect(clampFolded(5, 2)).toBe(0);
    expect(clampFolded(5, 3)).toBe(1);
  });
  it("reads nonsense as nothing folded", () => {
    expect(clampFolded(-1, 7)).toBe(0);
    expect(clampFolded(Number.NaN, 7)).toBe(0);
  });
});

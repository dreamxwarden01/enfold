import { describe, expect, it } from "vitest";
import { buttonKey, contextPlacement, dropPlacement, groups, menuKey, secondLine } from "./menu";

describe("buttonKey (APP.md §6, Menus)", () => {
  it("opens on the first item with Down, Enter and Space", () => {
    expect(buttonKey("ArrowDown", 3)).toEqual({ kind: "open", active: 0 });
    expect(buttonKey("Enter", 3)).toEqual({ kind: "open", active: 0 });
    expect(buttonKey(" ", 3)).toEqual({ kind: "open", active: 0 });
  });

  it("opens on the last item with Up", () => {
    expect(buttonKey("ArrowUp", 3)).toEqual({ kind: "open", active: 2 });
  });

  it("leaves every other key to the page", () => {
    expect(buttonKey("Escape", 3)).toEqual({ kind: "none" });
    expect(buttonKey("Tab", 3)).toEqual({ kind: "none" });
    expect(buttonKey("a", 3)).toEqual({ kind: "none" });
  });

  it("does not open a menu with nothing in it", () => {
    expect(buttonKey("ArrowDown", 0)).toEqual({ kind: "none" });
    expect(buttonKey("ArrowUp", 0)).toEqual({ kind: "none" });
  });
});

describe("menuKey (APP.md §6, Menus)", () => {
  it("walks the items and wraps at either end", () => {
    expect(menuKey("ArrowDown", 0, 3)).toEqual({ kind: "move", active: 1 });
    expect(menuKey("ArrowDown", 2, 3)).toEqual({ kind: "move", active: 0 });
    expect(menuKey("ArrowUp", 1, 3)).toEqual({ kind: "move", active: 0 });
    expect(menuKey("ArrowUp", 0, 3)).toEqual({ kind: "move", active: 2 });
  });

  it("reaches the ends with Home and End", () => {
    expect(menuKey("Home", 2, 3)).toEqual({ kind: "move", active: 0 });
    expect(menuKey("End", 0, 3)).toEqual({ kind: "move", active: 2 });
  });

  it("takes the item under the focus with Enter and Space", () => {
    expect(menuKey("Enter", 1, 3)).toEqual({ kind: "choose", index: 1 });
    expect(menuKey(" ", 2, 3)).toEqual({ kind: "choose", index: 2 });
  });

  it("closes on Escape and on Tab", () => {
    expect(menuKey("Escape", 1, 3)).toEqual({ kind: "close" });
    expect(menuKey("Tab", 1, 3)).toEqual({ kind: "close" });
  });

  it("reads a focus on no item as none, and never guesses one for Enter", () => {
    expect(menuKey("Enter", -1, 3)).toEqual({ kind: "none" });
    expect(menuKey("Enter", 7, 3)).toEqual({ kind: "none" });
    expect(menuKey("ArrowDown", -1, 3)).toEqual({ kind: "move", active: 0 });
    expect(menuKey("ArrowUp", -1, 3)).toEqual({ kind: "move", active: 2 });
  });

  it("closes rather than walking a menu with nothing in it", () => {
    expect(menuKey("ArrowDown", -1, 0)).toEqual({ kind: "close" });
  });

  it("leaves every other key to the page", () => {
    expect(menuKey("a", 0, 3)).toEqual({ kind: "none" });
    expect(menuKey("ArrowLeft", 0, 3)).toEqual({ kind: "none" });
  });
});

// The right-click menu of the details modal (APP.md §13): one item,
// *Copy*, placed at the pointer and never off the window's edge.
describe("where a context menu goes", () => {
  const box = { width: 140, height: 36 };
  const view = { width: 1000, height: 700 };

  it("opens at the pointer where there is room", () => {
    expect(contextPlacement({ x: 300, y: 200 }, box, view)).toEqual({ x: 300, y: 200 });
  });

  it("flips to the other side of the pointer against the right edge", () => {
    expect(contextPlacement({ x: 980, y: 200 }, box, view)).toEqual({ x: 840, y: 200 });
  });

  it("flips upwards against the foot of the window", () => {
    expect(contextPlacement({ x: 300, y: 690 }, box, view)).toEqual({ x: 300, y: 654 });
  });

  it("flips both at once in the far corner", () => {
    expect(contextPlacement({ x: 995, y: 695 }, box, view)).toEqual({ x: 855, y: 659 });
  });

  it("clamps to the margin when neither side has room", () => {
    // A window narrower than the menu itself: it is pulled inside, and the
    // margin wins over an edge that would cut it.
    const tiny = { width: 100, height: 40 };
    expect(contextPlacement({ x: 60, y: 30 }, box, tiny)).toEqual({ x: 6, y: 6 });
  });

  it("takes the one item's own menu, so the keyboard lands on it", () => {
    expect(menuKey("ArrowDown", 0, 1)).toEqual({ kind: "move", active: 0 });
    expect(menuKey("Enter", 0, 1)).toEqual({ kind: "choose", index: 0 });
    expect(menuKey("Escape", 0, 1)).toEqual({ kind: "close" });
  });
});

// What a menu holds (direction 6): groups with a separator between them,
// and a greyed command's reason said under its label.
describe("a menu's groups", () => {
  type Item = { label: string; sep?: boolean };
  const a: Item = { label: "Refresh fingerprint" };
  const b: Item = { label: "Compact…" };
  const hide: Item = { label: "Hide" };
  const forget: Item = { label: "Forget archive…" };
  const del: Item = { label: "Delete archive…" };

  it("puts a separator above the first item of every group but the first", () => {
    expect(groups([a, b], [hide], [forget, del]).map((i) => [i.label, i.sep])).toEqual([
      ["Refresh fingerprint", false],
      ["Compact…", false],
      ["Hide", true],
      ["Forget archive…", true],
      ["Delete archive…", false],
    ]);
  });

  it("leaves no separator for an empty group, at either end or between", () => {
    expect(groups([], [hide], [], [forget, del]).map((i) => [i.label, i.sep])).toEqual([
      ["Hide", false],
      ["Forget archive…", true],
      ["Delete archive…", false],
    ]);
    expect(groups([], [])).toEqual([]);
  });

  it("decides every separator itself, whatever the items said", () => {
    expect(groups([{ ...a, sep: true }], [{ ...hide, sep: false }, { ...forget, sep: true }]).map((i) => i.sep)).toEqual([false, true, false]);
  });

  it("copies the items rather than changing the caller's", () => {
    const item: Item = { label: "Hide", sep: false };
    groups([a], [item]);
    expect(item.sep).toBe(false);
  });
});

describe("a greyed item's second line", () => {
  it("is the reason, for an item that cannot run", () => {
    expect(secondLine({ disabled: true, title: "Unlock the vault first." })).toBe("Unlock the vault first.");
  });

  it("is nothing for an item that can run, whatever its title", () => {
    expect(secondLine({ title: "Delete archive… (Del)" })).toBe("");
    expect(secondLine({ disabled: false, title: "x" })).toBe("");
  });

  it("is nothing for a greyed item with no reason", () => {
    expect(secondLine({ disabled: true })).toBe("");
    expect(secondLine({ disabled: true, title: "  " })).toBe("");
  });
});

// A menu drawn over the window from a box that clips it (direction 6):
// the Archives pane's More, and the bottom panel's at the window's foot.
describe("where a floating menu goes", () => {
  const view = { width: 1000, height: 700 };
  const box = { width: 200, height: 160 };
  const button = { left: 600, right: 680, top: 100, bottom: 128 };

  it("drops under its button, on the button's left edge", () => {
    expect(dropPlacement(button, box, view)).toEqual({ x: 600, y: 132, up: false });
  });

  it("stands on the button's right edge for a button at the right of its row", () => {
    expect(dropPlacement(button, box, view, true)).toEqual({ x: 480, y: 132, up: false });
  });

  it("opens upwards when the window has no room below, as at a panel's foot", () => {
    const low = { left: 600, right: 680, top: 640, bottom: 668 };
    expect(dropPlacement(low, box, view)).toEqual({ x: 600, y: 476, up: true });
  });

  it("is pulled inside the window's margin at its edges", () => {
    expect(dropPlacement({ left: 900, right: 980, top: 100, bottom: 128 }, box, view).x).toBe(794);
    expect(dropPlacement({ left: 0, right: 80, top: 100, bottom: 128 }, box, view, true).x).toBe(6);
  });

  it("takes the side with more room when neither holds it, clamped", () => {
    const tall = { width: 200, height: 690 };
    expect(dropPlacement({ left: 10, right: 90, top: 100, bottom: 128 }, tall, view)).toEqual({ x: 10, y: 6, up: false });
    expect(dropPlacement({ left: 10, right: 90, top: 600, bottom: 628 }, tall, view)).toEqual({ x: 10, y: 6, up: true });
  });
});

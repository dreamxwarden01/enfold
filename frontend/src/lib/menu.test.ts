import { describe, expect, it } from "vitest";
import { buttonKey, contextPlacement, menuKey } from "./menu";

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

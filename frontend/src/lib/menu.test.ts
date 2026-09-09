import { describe, expect, it } from "vitest";
import { buttonKey, menuKey } from "./menu";

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

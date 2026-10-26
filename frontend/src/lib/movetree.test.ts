import { describe, expect, it } from "vitest";
import { TREE_LIMIT, ancestry, expandable, failed, folderLines, lines, needsRead, newTree, offReason, pathOf, read, reading, setOpen, treeKey } from "./movetree";
import type { Tree } from "./movetree";
import type { KeyIn } from "./listkeys";

// Move to… (direction 7; APP.md §6): a tree of the archive's folders,
// read a folder at a time as it opens, the folder the items are in and
// the moved folders greyed with why.

const ROOT = "00";
const row = (id: string, name: string, isDir = true) => ({ id, name, isDir });

// Photos 2024 ─ 2024 ─ Trips ─ Day one
//             └ Empty folder
function photos(): Tree {
  let t = newTree(ROOT, "Photos 2024");
  t = read(t, ROOT, [row("y", "2024"), row("e", "Empty folder"), row("f1", "a.jpg", false)], 3);
  t = setOpen(t, "y", true);
  t = read(t, "y", [row("tr", "Trips"), row("f2", "b.jpg", false)], 2);
  t = setOpen(t, "tr", true);
  t = read(t, "tr", [row("d1", "Day one"), row("f3", "day-one.md", false)], 2);
  return t;
}

const names = (t: Tree) => lines(t).map((l) => (l.kind === "folder" ? `${"  ".repeat(l.depth)}${l.name}` : `${"  ".repeat(l.depth)}(${l.note})`));

describe("the tree", () => {
  it("starts on the archive, open and unread", () => {
    const t = newTree(ROOT, "Photos 2024");
    expect(lines(t)).toEqual([{ kind: "folder", id: ROOT, name: "Photos 2024", depth: 0, open: true, expandable: true }]);
    expect(needsRead(t, ROOT)).toBe(true);
  });
  it("says a read on its way inside the folder", () => {
    const t = reading(newTree(ROOT, "Photos 2024"), ROOT);
    expect(names(t)).toEqual(["Photos 2024", "  (loading)"]);
    expect(needsRead(t, ROOT)).toBe(false);
  });
  it("keeps the folders a read gave, in its order, and leaves the files out", () => {
    expect(names(photos())).toEqual(["Photos 2024", "  2024", "    Trips", "      Day one", "  Empty folder"]);
  });
  it("draws a chevron on a folder until it is read empty", () => {
    const t = photos();
    expect(expandable(t.folders["e"])).toBe(true);
    const r = read(t, "e", [], 0);
    expect(expandable(r.folders["e"])).toBe(false);
    expect(expandable(r.folders["y"])).toBe(true);
  });
  it("hides what a closed folder holds, and remembers it when opened again", () => {
    let t = setOpen(photos(), "y", false);
    expect(names(t)).toEqual(["Photos 2024", "  2024", "  Empty folder"]);
    t = setOpen(t, "y", true);
    expect(names(t)).toContain("      Day one");
    expect(needsRead(t, "y")).toBe(false);
  });
  it("keeps what an opened folder knew across a re-read of its parent", () => {
    let t = photos();
    t = read(t, "y", [row("new", "Album"), row("tr", "Trips"), row("f2", "b.jpg", false)], 3);
    expect(names(t)).toEqual(["Photos 2024", "  2024", "    Album", "    Trips", "      Day one", "  Empty folder"]);
  });
  it("says a refused read with its code, and reads again when opened", () => {
    let t = failed(reading(photos(), "e"), "e", "file.not_found");
    t = setOpen(t, "e", true);
    const note = lines(t).find((l) => l.kind === "note");
    expect(note).toEqual({ kind: "note", parent: "e", depth: 2, note: "failed", code: "file.not_found" });
    expect(needsRead(t, "e")).toBe(true);
  });
  it("says a folder holds more than the tree shows when the page stopped on a folder", () => {
    let t = newTree(ROOT, "Big");
    const many = Array.from({ length: TREE_LIMIT }, (_, i) => row(`d${i}`, `Folder ${i}`));
    t = read(t, ROOT, many, TREE_LIMIT + 20);
    expect(t.folders[ROOT].cut).toBe(true);
    expect(lines(t).at(-1)).toMatchObject({ kind: "note", note: "cut" });
    // Directories come first: a page that ended on a file held them all.
    const mixed = [...many.slice(0, 10), row("f", "a.jpg", false)];
    expect(read(newTree(ROOT, "Big"), ROOT, mixed, 900).folders[ROOT].cut).toBe(false);
  });
  it("keeps every name of a folder, files too, for Create folder's judge", () => {
    expect(photos().folders["y"].names).toEqual(["Trips", "b.jpg"]);
  });
});

describe("a folder that cannot take the move", () => {
  const t = photos();
  it("is the folder the items are in now: Already here", () => {
    expect(offReason(t, "y", "y", ["f2"])).toBe("here");
    expect(offReason(t, ROOT, ROOT, ["f1"])).toBe("here");
  });
  it("is a moved folder, and anything beneath it", () => {
    expect(offReason(t, "tr", "y", ["tr", "f2"])).toBe("self");
    expect(offReason(t, "d1", "y", ["tr"])).toBe("self");
  });
  it("is no other", () => {
    expect(offReason(t, ROOT, "y", ["tr"])).toBeNull();
    expect(offReason(t, "e", "y", ["tr"])).toBeNull();
  });
});

describe("the path under the tree", () => {
  it("runs from the archive down to the folder", () => {
    const t = photos();
    expect(pathOf(t, "d1")).toEqual(["Photos 2024", "2024", "Trips", "Day one"]);
    expect(pathOf(t, ROOT)).toEqual(["Photos 2024"]);
    expect(ancestry(t, "tr")).toEqual(["tr", "y", ROOT]);
  });
});

describe("the tree's keys", () => {
  const k = (key: string, m: Partial<Omit<KeyIn, "key">> = {}): KeyIn => ({ key, ctrl: false, shift: false, alt: false, ...m });
  const t = photos();
  const rows = folderLines(t);
  const at = (id: string) => rows.findIndex((r) => r.id === id);
  it("walk the rows with the arrows, Home and End", () => {
    expect(treeKey(k("ArrowDown"), t, rows, at(ROOT))).toEqual({ kind: "select", index: 1 });
    expect(treeKey(k("ArrowUp"), t, rows, at(ROOT))).toEqual({ kind: "select", index: 0 });
    expect(treeKey(k("ArrowDown"), t, rows, rows.length - 1)).toEqual({ kind: "select", index: rows.length - 1 });
    expect(treeKey(k("End"), t, rows, 0)).toEqual({ kind: "select", index: rows.length - 1 });
    expect(treeKey(k("Home"), t, rows, 3)).toEqual({ kind: "select", index: 0 });
  });
  it("open a closed folder with →, and step into an open one", () => {
    expect(treeKey(k("ArrowRight"), t, rows, at("e"))).toEqual({ kind: "open", id: "e" });
    expect(treeKey(k("ArrowRight"), t, rows, at("y"))).toEqual({ kind: "select", index: at("tr") });
  });
  it("close an open folder with ←, and step out of a closed one", () => {
    expect(treeKey(k("ArrowLeft"), t, rows, at("y"))).toEqual({ kind: "close", id: "y" });
    expect(treeKey(k("ArrowLeft"), t, rows, at("d1"))).toEqual({ kind: "select", index: at("tr") });
    expect(treeKey(k("ArrowLeft"), t, rows, at("e"))).toEqual({ kind: "select", index: at(ROOT) });
  });
  it("open a closed folder with Enter, and choose any other", () => {
    expect(treeKey(k("Enter"), t, rows, at("e"))).toEqual({ kind: "open", id: "e" });
    expect(treeKey(k("Enter"), t, rows, at("y"))).toEqual({ kind: "choose" });
    const leaf = read(t, "d1", [], 0);
    expect(treeKey(k("Enter"), leaf, folderLines(leaf), at("d1"))).toEqual({ kind: "choose" });
  });
  it("type ahead with a character, Shift or not, and leave the rest alone", () => {
    expect(treeKey(k("t"), t, rows, 0)).toEqual({ kind: "typeahead", char: "t" });
    expect(treeKey(k("T", { shift: true }), t, rows, 0)).toEqual({ kind: "typeahead", char: "T" });
    expect(treeKey(k("t", { ctrl: true }), t, rows, 0).kind).toBe("none");
    expect(treeKey(k(" "), t, rows, 0).kind).toBe("none");
    expect(treeKey(k("Tab"), t, rows, 0).kind).toBe("none");
    expect(treeKey(k("ArrowDown"), t, [], 0).kind).toBe("none");
  });
});

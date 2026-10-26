// Move to… (direction 7, Explorer's habits; APP.md §6, the Archive page):
// the selection into a folder chosen in a tree of the archive's folders —
// the keyboard's way to what a drag does, and a way past the one level a
// drag reaches without its spring. Ctrl+Shift+M, the row menu's *Move to…*
// and the toolbar's More open it.
//
// The tree is the archive's, folders only, read a folder at a time as it
// is opened: Archive.Page(id, dirID, "name", 0, TREE_LIMIT), whose rows
// come directories first in name order (APP.md §3), keeping the folders.
// The root is the archive, named by it, and open. A folder that cannot
// take the move is greyed with why — the folder the items are in now
// ("Already here"), and a moved folder or anything beneath it ("A folder
// cannot move into itself") — and stays selectable, so *Create folder…*
// can make a new one inside it.
//
// Pure: the tree as data, what the rows show, which are greyed, the path
// said under the tree, and what a key does. The dialog holds the rest.
import type { KeyIn } from "./listkeys";

// One Page call per folder: past this, the folder says it has more than
// the tree shows.
export const TREE_LIMIT = 500;

export type Load = "unread" | "loading" | "read" | "failed";

export interface TreeFolder {
  id: string;
  name: string;
  parent: string | null;
  depth: number;
  open: boolean;
  load: Load;
  // The folders in it, in the order Page gave them (name order).
  folders: string[];
  // Every name in it, files too, for *Create folder…*'s judge: a name is
  // taken whichever kind holds it (APP.md §6, Forms).
  names: string[];
  // cut: the folder holds more than TREE_LIMIT items and Page stopped
  // before its last folder, so some folders are not in the tree.
  cut: boolean;
  // The code a read was refused with.
  code: string;
}

export interface Tree {
  root: string;
  folders: Record<string, TreeFolder>;
}

export function newTree(rootId: string, rootName: string): Tree {
  return {
    root: rootId,
    folders: { [rootId]: blank(rootId, rootName, null, 0, true) },
  };
}

function blank(id: string, name: string, parent: string | null, depth: number, open = false): TreeFolder {
  return { id, name, parent, depth, open, load: "unread", folders: [], names: [], cut: false, code: "" };
}

function put(t: Tree, f: TreeFolder): Tree {
  return { ...t, folders: { ...t.folders, [f.id]: f } };
}

// reading: a read of the folder began.
export function reading(t: Tree, id: string): Tree {
  const f = t.folders[id];
  return f ? put(t, { ...f, load: "loading", code: "" }) : t;
}

export interface PageRow {
  id: string;
  name: string;
  isDir: boolean;
}

// read: the folder's Page answered. Its folders join the tree in Page's
// order; one already known keeps what it knew (open, read) — a re-read
// after *Create folder…* leaves the opened ones open.
export function read(t: Tree, id: string, rows: readonly PageRow[], total: number): Tree {
  const f = t.folders[id];
  if (!f) return t;
  const dirs = rows.filter((r) => r.isDir);
  // Directories come first: a page that ends on a file holds every
  // folder; one that ends on a folder and stops short of the total may not.
  const last = rows[rows.length - 1];
  const cut = total > rows.length && (last === undefined || last.isDir);
  let out = put(t, { ...f, load: "read", folders: dirs.map((d) => d.id), names: rows.map((r) => r.name), cut, code: "" });
  for (const d of dirs) {
    const known = out.folders[d.id];
    out = put(out, known ? { ...known, name: d.name, parent: id, depth: f.depth + 1 } : blank(d.id, d.name, id, f.depth + 1));
  }
  return out;
}

// failed: the read was refused; the folder says the code's line where its
// folders would be, and opening it again tries again.
export function failed(t: Tree, id: string, code: string): Tree {
  const f = t.folders[id];
  return f ? put(t, { ...f, load: "failed", code }) : t;
}

export function setOpen(t: Tree, id: string, open: boolean): Tree {
  const f = t.folders[id];
  return f && f.open !== open ? put(t, { ...f, open }) : t;
}

// needsRead: opening this folder asks Page for it — never read, or its
// read refused.
export function needsRead(t: Tree, id: string): boolean {
  const f = t.folders[id];
  return !!f && (f.load === "unread" || f.load === "failed");
}

// expandable: whether the row draws a chevron. Unread, it might hold
// folders; read, it does or it does not.
export function expandable(f: TreeFolder): boolean {
  return f.load !== "read" || f.folders.length > 0 || f.cut;
}

// A line the tree draws inside an open folder that is not a folder: the
// read on its way, the read refused, or more than the tree shows.
export type Note = "loading" | "failed" | "cut";

export type Line =
  | { kind: "folder"; id: string; name: string; depth: number; open: boolean; expandable: boolean }
  | { kind: "note"; parent: string; depth: number; note: Note; code: string };

// lines: what the tree draws, top to bottom — each open folder followed
// by its folders, and by a note where its read is on its way, was refused
// or stopped short.
export function lines(t: Tree): Line[] {
  const out: Line[] = [];
  const walk = (id: string) => {
    const f = t.folders[id];
    if (!f) return;
    out.push({ kind: "folder", id, name: f.name, depth: f.depth, open: f.open, expandable: expandable(f) });
    if (!f.open) return;
    if (f.load === "loading") out.push({ kind: "note", parent: id, depth: f.depth + 1, note: "loading", code: "" });
    else if (f.load === "failed") out.push({ kind: "note", parent: id, depth: f.depth + 1, note: "failed", code: f.code });
    if (f.load !== "read") return;
    for (const c of f.folders) walk(c);
    if (f.cut) out.push({ kind: "note", parent: id, depth: f.depth + 1, note: "cut", code: "" });
  };
  walk(t.root);
  return out;
}

// The folder rows alone, in drawing order: what the keys walk.
export function folderLines(t: Tree): Extract<Line, { kind: "folder" }>[] {
  return lines(t).filter((l): l is Extract<Line, { kind: "folder" }> => l.kind === "folder");
}

// ancestry: the folder and the folders above it, nearest first.
export function ancestry(t: Tree, id: string): string[] {
  const out: string[] = [];
  let at: string | null = id;
  while (at !== null) {
    const f: TreeFolder | undefined = t.folders[at];
    if (!f) break;
    out.push(at);
    at = f.parent;
  }
  return out;
}

// Why a folder cannot take the move: the folder the items are in now, or
// one of the moved folders or anything beneath one. null: it can.
export type Off = "here" | "self";

export function offReason(t: Tree, id: string, current: string, moving: readonly string[]): Off | null {
  if (id === current) return "here";
  const carried = new Set(moving);
  if (ancestry(t, id).some((a) => carried.has(a))) return "self";
  return null;
}

// pathOf: the names from the archive down to the folder, for the line
// under the tree that says where the move goes.
export function pathOf(t: Tree, id: string): string[] {
  return ancestry(t, id)
    .reverse()
    .map((a) => t.folders[a].name);
}

// ---- the keys in the tree (the tree pattern; Explorer's own) -----------
//
// ↑ ↓ move the selection, Home and End reach the ends; → opens a closed
// folder, or steps into an open one's first folder; ← closes an open one,
// or steps out to its parent; Enter opens a closed folder that can open,
// and on any other row chooses it — the dialog's *Move*, when the folder
// can take it. A printable character is type-ahead (lib/listkeys.ts).

export type TreeAction =
  | { kind: "select"; index: number }
  | { kind: "open"; id: string }
  | { kind: "close"; id: string }
  | { kind: "choose" }
  | { kind: "typeahead"; char: string }
  | { kind: "none" };

const NONE: TreeAction = { kind: "none" };

export function treeKey(k: KeyIn, t: Tree, rows: readonly Extract<Line, { kind: "folder" }>[], at: number): TreeAction {
  const plain = !k.ctrl && !k.alt && !k.shift;
  const n = rows.length;
  if (n === 0) return NONE;
  const i = at >= 0 && at < n ? at : -1;
  const row = i >= 0 ? rows[i] : null;
  if (!plain) {
    if (!k.ctrl && !k.alt && k.key.length === 1 && k.key !== " ") return { kind: "typeahead", char: k.key };
    return NONE;
  }
  switch (k.key) {
    case "ArrowDown":
      return { kind: "select", index: i < 0 ? 0 : Math.min(i + 1, n - 1) };
    case "ArrowUp":
      return { kind: "select", index: i < 0 ? 0 : Math.max(i - 1, 0) };
    case "Home":
      return { kind: "select", index: 0 };
    case "End":
      return { kind: "select", index: n - 1 };
    case "ArrowRight":
      if (!row) return NONE;
      if (row.expandable && !row.open) return { kind: "open", id: row.id };
      if (row.open && i + 1 < n && rows[i + 1].depth === row.depth + 1) return { kind: "select", index: i + 1 };
      return NONE;
    case "ArrowLeft": {
      if (!row) return NONE;
      if (row.open && row.expandable) return { kind: "close", id: row.id };
      const parent = t.folders[row.id]?.parent;
      const p = parent ? rows.findIndex((r) => r.id === parent) : -1;
      return p >= 0 ? { kind: "select", index: p } : NONE;
    }
    case "Enter":
      if (!row) return NONE;
      return row.expandable && !row.open ? { kind: "open", id: row.id } : { kind: "choose" };
  }
  if (k.key.length === 1 && k.key !== " ") return { kind: "typeahead", char: k.key };
  return NONE;
}

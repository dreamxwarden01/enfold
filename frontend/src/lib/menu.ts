// A button's menu (APP.md §6, Menus): the keyboard, as a state machine
// the component obeys. The component holds two things — whether the menu
// is open and which item the focus is on — and every key is answered
// here, so the machine can be read and tested without a DOM.
//
// A <select> stays native and its picker is styled; a menu of actions is
// not a <select>, so this is hand-built and answers the keys a menu
// answers everywhere: the arrows walk it and wrap, Home and End reach the
// ends, Enter and Space take the item under the focus, Escape and Tab
// close it.

export type MenuAction =
  | { kind: "none" }
  | { kind: "open"; active: number }
  | { kind: "move"; active: number }
  | { kind: "choose"; index: number }
  | { kind: "close" };

const NONE: MenuAction = { kind: "none" };
const CLOSE: MenuAction = { kind: "close" };

// buttonKey: what a key does on the button while the menu is closed. Down
// opens it on the first item, Up on the last; Enter and Space open it the
// way a press does. A menu with nothing in it does not open.
export function buttonKey(key: string, count: number): MenuAction {
  if (count <= 0) return NONE;
  switch (key) {
    case "ArrowDown":
    case "Enter":
    case " ":
      return { kind: "open", active: 0 };
    case "ArrowUp":
      return { kind: "open", active: count - 1 };
  }
  return NONE;
}

// menuKey: what a key does while the menu is open. active is the item the
// focus is on, -1 when it is on none of them (anything out of range is
// read as that). Enter on no item does nothing rather than guessing one.
export function menuKey(key: string, active: number, count: number): MenuAction {
  if (count <= 0) return CLOSE;
  const at = Number.isInteger(active) && active >= 0 && active < count ? active : -1;
  switch (key) {
    case "ArrowDown":
      return { kind: "move", active: at < 0 ? 0 : (at + 1) % count };
    case "ArrowUp":
      return { kind: "move", active: at < 0 ? count - 1 : (at - 1 + count) % count };
    case "Home":
      return { kind: "move", active: 0 };
    case "End":
      return { kind: "move", active: count - 1 };
    case "Enter":
    case " ":
      return at < 0 ? NONE : { kind: "choose", index: at };
    case "Escape":
    case "Tab":
      return CLOSE;
  }
  return NONE;
}

// ---- what a menu holds (APP.md §6, Menus; direction 6) -----------------
//
// A menu is groups of items, a separator between one group and the next:
// the Archives bar's More is the verbs the bar had no room for, then
// Hide, then the two that destroy. A group can be empty — no verb folded,
// no Close for an archive that is not open — and an empty group leaves no
// separator behind, so a menu never starts with one, never ends with one
// and never draws two in a row.
export interface Groupable {
  sep?: boolean;
}

export function groups<T extends Groupable>(...gs: readonly (readonly T[])[]): T[] {
  const out: T[] = [];
  for (const g of gs) {
    if (g.length === 0) continue;
    g.forEach((item, i) => out.push({ ...item, sep: i === 0 && out.length > 0 }));
  }
  return out;
}

// secondLine: what a menu item says under its label. A command that
// cannot run now keeps its reason there, in the caption (direction 6),
// where the keyboard and the eye both read it without a hover; an item
// that can run says nothing more, its title staying the native tooltip —
// unless it carries a note, a fact the user needs before choosing it (the
// blank area's *Paste* of files cut in Explorer: "The originals stay where
// they are", ruled 2026-10-26).
export interface Explained {
  disabled?: boolean;
  title?: string;
  note?: string;
}

export function secondLine(item: Explained): string {
  return ((item.disabled ? item.title : item.note) ?? "").trim();
}

// ---- a menu that floats free of its box (direction 6) -------------------
//
// A menu inside a box that clips — the Archives pane, which scrolls, and
// the bottom panel, whose card clips its own height as it moves — is drawn
// over the window instead, placed against its button: under it, its left
// edge on the button's (its right edge, for a button at the right of its
// row), and above the button when the window has no room for it below — a
// panel at the window's foot opens its menus upwards. Pulled inside the
// window's margin either way.
export interface Anchor {
  left: number;
  right: number;
  top: number;
  bottom: number;
}

export function dropPlacement(anchor: Anchor, box: Size, view: Size, end = false, gap = 4, margin = 6): Point & { up: boolean } {
  const want = end ? anchor.right - box.width : anchor.left;
  const x = Math.max(margin, Math.min(want, view.width - box.width - margin));
  const below = anchor.bottom + gap;
  if (below + box.height <= view.height - margin) return { x, y: below, up: false };
  const above = anchor.top - gap - box.height;
  if (above >= margin) return { x, y: above, up: true };
  // Room on neither side: the side with more of it, clamped to the margin.
  const roomBelow = view.height - anchor.bottom;
  if (roomBelow >= anchor.top) return { x, y: Math.max(margin, view.height - box.height - margin), up: false };
  return { x, y: margin, up: true };
}

// ---- the context menu (APP.md §13; direction 7) --------------------------
//
// A right-click opens the app's own menu: on a row of either list, on a
// list's blank area, in a text field that is not a secret, and on a value
// in the details modal. 2026-09-06 turned WebView2's own context menu off
// — its "Save image as" wrote a decrypted preview to Downloads — and that
// ruling stands: these menus are drawn by the page (ContextMenu.svelte),
// offer only the app's own commands, and are not WebView2's. Their
// keyboard is the machine above, and where the box goes is here.

export interface Size {
  width: number;
  height: number;
}

// How a context menu closed, for its opener to put the focus back: a key
// (Escape, Tab) or a command taken return it to what opened the menu — the
// row, the field — and a press elsewhere leaves it where that press put it.
export type MenuClose = "key" | "choose" | "pointer";

export interface Point {
  x: number;
  y: number;
}

// contextPlacement puts the box's top-left at the pointer and pulls it
// back inside the viewport when it would hang off the right or the
// bottom: the box is flipped to the other side of the pointer where there
// is room for it, and clamped to the margin where there is not, so a menu
// opened in a corner is never partly off screen and never under the
// pointer's own path. margin keeps it off the very edge.
export function contextPlacement(at: Point, box: Size, view: Size, margin = 6): Point {
  const place = (p: number, size: number, extent: number): number => {
    if (p + size + margin <= extent) return p; // it fits below/right of the pointer
    const flipped = p - size;
    if (flipped >= margin) return flipped; // it fits above/left of it
    return Math.max(margin, extent - size - margin);
  };
  return { x: place(at.x, box.width, view.width), y: place(at.y, box.height, view.height) };
}

// ---- a row's menu from the keyboard (direction 7) -----------------------
//
// Shift+F10 and the Menu key open the menu a right-click opens, with no
// pointer to place it at: it stands at the focused row's left edge, 8 px
// in, under the row — where Explorer puts it — and contextPlacement then
// pulls it inside the window like any other.
export const KEY_INSET = 8;

export function keyAnchor(row: Pick<Anchor, "left" | "bottom">): Point {
  return { x: row.left + KEY_INSET, y: row.bottom };
}

// The Menu key and Shift+F10 also raise the WebView's own contextmenu
// event once the key is answered (on Windows, as the key comes up); one
// arriving this soon after a menu was opened from the keyboard is that
// echo, and is spent.
export const KEY_ECHO_MS = 600;

// ---- the commands a row's menu offers (direction 7) ---------------------
//
// A page's commands as a menu holds them: what each one is called, its key,
// its icon, whether it is the default (drawn bold, first — what Enter and
// a double-click do), whether it destroys, and, when it cannot run, why.
// The pages keep one function per command and map cmd to it, so the bar,
// the pane's More and the row's menu run the same thing.
export interface CmdItem<C extends string> extends Groupable, Explained {
  cmd: C;
  label: string;
  icon?: string;
  keys?: string;
  isDefault?: boolean;
  danger?: boolean;
}

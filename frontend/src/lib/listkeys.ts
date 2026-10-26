// The lists' keys (direction 7, Explorer's habits; APP.md §6): one pure
// map from a key and where it was pressed to what it means, so each list
// handles every key in one place and vitest covers the whole table.
//
// Arrows move the selection, Explorer's way: the row arrived at becomes
// the one selected row and the anchor, and the pane follows. This
// reverses 2026-09-10's "Space selects, arrows move the focus", which
// suited a checkbox list and left the preview behind every arrow. The
// file list keeps Explorer's whole set — Shift extends from the anchor,
// Ctrl moves the focus alone, Ctrl+Space toggles; the Archives list is
// single-select for the keys (one row at a time; its ticks stay ticks).
//
// Nothing here touches the DOM: the page says what kind of element the
// key came from and acts on the intent it gets back.

export type ListKind = "files" | "archives";

export interface KeyIn {
  key: string;
  ctrl: boolean;
  shift: boolean;
  alt: boolean;
}

export interface KeyCtx {
  list: ListKind;
  // onUp: the focus is on the file list's `..` row, which goes up on
  // Enter and Space and takes the arrows like any row.
  onUp: boolean;
  // inField: the focus is in a text field, which keeps its own keys.
  inField: boolean;
  // dialogUp: a dialog is over the page, which then takes no key.
  dialogUp: boolean;
  // viewerUp: the viewer is up over the layer (direction 7; lib/viewer.ts),
  // which takes its own keys; the page behind takes none, as under a
  // dialog. Ctrl+L stays the foot's (locksNow), which reads no viewer.
  viewerUp?: boolean;
  // typing: a type-ahead buffer is live (within TYPEAHEAD_MS), so a Space
  // is part of the name being typed rather than a selection.
  typing?: boolean;
  // onControl: the focus is on a control of its own inside the list's
  // region that is not a row — a column's sort button, the tick-all box,
  // the empty vault's New archive… — which keeps its own Enter and Space
  // (controlKeepsKeys). A row's own tick box is part of its row.
  onControl?: boolean;
}

// A move: one row either way, the first or last row, or a page.
export type Step = 1 | -1 | "home" | "end" | "pageUp" | "pageDown";

export type Intent =
  | { kind: "move"; by: Step }
  | { kind: "extend"; by: Step }
  | { kind: "focusOnly"; by: Step }
  | { kind: "select" }
  | { kind: "toggle" }
  | { kind: "selectAll" }
  | { kind: "clear" }
  | { kind: "open" }
  | { kind: "up" }
  | { kind: "back" }
  | { kind: "extract" }
  | { kind: "delete" }
  | { kind: "rename" }
  | { kind: "newFolder" }
  | { kind: "refresh" }
  | { kind: "details" }
  | { kind: "newArchive" }
  | { kind: "openFile" }
  | { kind: "filter" }
  | { kind: "lock" }
  | { kind: "previewPane" }
  | { kind: "menu" }
  | { kind: "moveTo" }
  | { kind: "typeahead"; char: string }
  | { kind: "none" };

const NONE: Intent = { kind: "none" };

// The arrows and their kin, and the step each one takes.
const STEPS: Record<string, Step> = {
  ArrowDown: 1,
  ArrowUp: -1,
  Home: "home",
  End: "end",
  PageUp: "pageUp",
  PageDown: "pageDown",
};

// intent maps a key pressed on a list's page to what it means there.
export function intent(k: KeyIn, ctx: KeyCtx): Intent {
  // A dialog over the page takes every key itself (lib/dialogs.ts), and
  // so does the viewer over the layer (lib/viewer.ts viewerKey).
  if (ctx.dialogUp || ctx.viewerUp) return NONE;
  const files = ctx.list === "files";
  const letter = k.key.length === 1 ? k.key.toLowerCase() : "";
  const plain = !k.ctrl && !k.alt && !k.shift;
  const ctrlOnly = k.ctrl && !k.alt && !k.shift;

  // A text field keeps its own keys — its arrows, Home and End, its
  // select-all, its Backspace and Delete — with two exceptions that edit
  // no text: Esc, which is the list's (the Archives page's panel rule),
  // and Ctrl+O on the Archives page, Open file… from anywhere on the page,
  // the Filter included (ruled 2026-10-14).
  if (ctx.inField) {
    if (k.key === "Escape" && plain) return { kind: "clear" };
    if (!files && ctrlOnly && letter === "o") return { kind: "openFile" };
    return NONE;
  }

  // Shift+F10 and the Menu key open the focused row's own menu, the one a
  // right-click opens (direction 7, Explorer's habits) — on both lists;
  // with no row to act on, the list's blank-area menu. Shift is allowed
  // with the Menu key, as Windows reads it; Ctrl and Alt are not.
  if (opensMenu(k)) return { kind: "menu" };

  // Lock now, on every page while unlocked: the foot's own command, so
  // the foot acts on it (LayerFoot, through locksNow) and the list pages
  // pass it by.
  if (ctrlOnly && letter === "l") return { kind: "lock" };

  const step = STEPS[k.key];
  if (step !== undefined) {
    // Alt+Up is the parent, as in Explorer; Alt+Left (below) is back.
    if (k.alt) return files && !k.ctrl && !k.shift && k.key === "ArrowUp" ? { kind: "up" } : NONE;
    if (!files) return plain ? { kind: "move", by: step } : NONE;
    if (k.ctrl && k.shift) return NONE;
    if (k.ctrl) return { kind: "focusOnly", by: step };
    if (k.shift) return { kind: "extend", by: step };
    return { kind: "move", by: step };
  }

  switch (k.key) {
    case "Enter":
      if (k.alt && !k.ctrl && !k.shift) return files ? NONE : { kind: "details" };
      if (!plain || ctx.onControl) return NONE;
      return ctx.onUp ? { kind: "up" } : { kind: "open" };
    case " ":
      if (ctx.onControl) return NONE;
      if (ctx.onUp) return plain ? { kind: "up" } : NONE;
      if (plain && ctx.typing) return { kind: "typeahead", char: " " };
      if (plain) return { kind: "select" };
      if (files && ctrlOnly) return { kind: "toggle" };
      return NONE;
    case "Escape":
      return plain ? { kind: "clear" } : NONE;
    case "Backspace":
      return files && plain ? { kind: "up" } : NONE;
    case "ArrowLeft":
      return files && k.alt && !k.ctrl && !k.shift ? { kind: "back" } : NONE;
    case "Delete":
      return plain ? { kind: "delete" } : NONE;
    case "F2":
      return plain ? { kind: "rename" } : NONE;
    case "F5":
      return plain ? { kind: "refresh" } : NONE;
  }

  if (ctrlOnly) {
    switch (letter) {
      case "a":
        return { kind: "selectAll" };
      case "e":
        return files ? { kind: "extract" } : NONE;
      case "n":
        return files ? NONE : { kind: "newArchive" };
      case "o":
        return files ? NONE : { kind: "openFile" };
      case "f":
        return files ? NONE : { kind: "filter" };
    }
    return NONE;
  }
  if (k.ctrl && k.shift && !k.alt && letter === "n") return files ? { kind: "newFolder" } : NONE;
  // Move to… (direction 7): the selection into a folder chosen in a tree,
  // the keyboard's way to what a drag does.
  if (k.ctrl && k.shift && !k.alt && letter === "m") return files ? { kind: "moveTo" } : NONE;
  // Alt+P shows or hides the Archive page's preview pane, as in Explorer
  // (direction 3, phase 4a; the View menu's *Preview pane*).
  if (k.alt && !k.ctrl && !k.shift && letter === "p") return files ? { kind: "previewPane" } : NONE;

  // A printable character, typed with or without Shift: type-ahead. AltGr
  // arrives as Ctrl+Alt, so a character typed with both is a character too
  // (the é, the @ of a layout that needs AltGr for them).
  const altGr = k.ctrl && k.alt;
  if ((altGr || (!k.ctrl && !k.alt)) && k.key.length === 1 && k.key !== " ") return { kind: "typeahead", char: k.key };
  return NONE;
}

// opensMenu: Shift+F10 or the Menu key — the row's menu on a list, a text
// field's own menu in a field (lib/fieldmenu.ts), which reads the same keys.
export function opensMenu(k: KeyIn): boolean {
  return (k.key === "ContextMenu" && !k.ctrl && !k.alt) || (k.key === "F10" && k.shift && !k.ctrl && !k.alt);
}

// SCOPED: the intents that belong to the list itself, taken only while
// the focus is in the list (or nowhere, on the page's body). The rest are
// the page's commands and run from anywhere on it but a text field.
// The menu key is the list's too: its menu is the focused row's.
const LIST_SCOPED = new Set<Intent["kind"]>(["move", "extend", "focusOnly", "select", "toggle", "selectAll", "open", "typeahead", "menu"]);

export function listScoped(i: Intent): boolean {
  return LIST_SCOPED.has(i.kind);
}

// isTextEntry: the elements that keep their own keys — a text input, a
// text area, a select, anything editable. A checkbox or a button does not:
// a row's checkbox is part of its row.
const NOT_TEXT = new Set(["checkbox", "radio", "button", "submit", "reset", "image", "range", "color", "file"]);

export function isTextEntry(tag: string, type: string, editable: boolean): boolean {
  if (editable) return true;
  const t = tag.toUpperCase();
  if (t === "TEXTAREA" || t === "SELECT") return true;
  return t === "INPUT" && !NOT_TEXT.has(type.toLowerCase());
}

// controlKeepsKeys: the focus is on a control that takes its own Enter
// and Space — a button, a link, a box, a field — and is not a part of a
// row. A row's tick box (tabindex -1, reached only by a click) is its
// row's, so Space there selects as on the row. Without this a sort
// button, the tick-all box and the empty vault's buttons, which sit in
// the list's region, would lose their keys to the rows.
export const CONTROLS =
  'button, a[href], input, select, textarea, summary, [role="button"], [role="checkbox"], [role="link"], [role="switch"], [role="tab"]';
const ROW_PART = "tbody td.chk input";

export interface ClosestLike {
  closest(selector: string): { matches(selector: string): boolean } | null;
}

export function controlKeepsKeys(t: ClosestLike | null): boolean {
  const c = t?.closest(CONTROLS);
  return !!c && !c.matches(ROW_PART);
}

// target is where a step lands, as an index into the rows; -1 is the `..`
// row (hasUp), which takes the arrows like a row but is never selected.
// from is the focused row, or null when the focus is not on a row: then
// every step lands on the first row but End, which lands on the last.
// Home and End are the first and last row that can be selected, so Home
// never lands on `..`; only ArrowUp and PgUp from the top reach it.
// page is how many rows a PgUp or PgDn moves (pageRows).
export function target(from: number | null, by: Step, n: number, page: number, hasUp: boolean): number | null {
  if (n === 0) return hasUp ? -1 : null;
  if (from === null) return by === "end" ? n - 1 : 0;
  switch (by) {
    case 1:
      return Math.min(from + 1, n - 1);
    case -1:
      if (from > 0) return from - 1;
      return hasUp ? -1 : 0;
    case "home":
      return 0;
    case "end":
      return n - 1;
    case "pageDown":
      return Math.min(Math.max(from, 0) + page, n - 1);
    case "pageUp":
      if (from <= 0) return hasUp ? -1 : 0;
      return Math.max(from - page, 0);
  }
}

// pageRows: a PgUp or PgDn moves by the rows that fit in the list's
// viewport under its header, less one, so the row left stays in view as
// Explorer keeps it; never less than one.
export function pageRows(viewport: number, header: number, row: number): number {
  if (row <= 0) return 1;
  return Math.max(1, Math.floor((viewport - header) / row) - 1);
}

// The type-ahead (Explorer's): characters typed within TYPEAHEAD_MS of
// the last one build a prefix; a longer pause starts a new one.
export const TYPEAHEAD_MS = 700;

export function typeahead(buffer: string, char: string, now: number, last: number): string {
  return buffer !== "" && now - last <= TYPEAHEAD_MS ? buffer + char : char;
}

// repeated: a buffer of one character typed again and again ("ppp"),
// which steps through the names starting with it rather than looking for
// a name that starts "ppp" — unless one does.
function repeated(b: string): boolean {
  return b.length > 1 && [...b].every((c) => c === b[0]);
}

const fold = (s: string): string => s.normalize("NFC").toLocaleLowerCase();

// jump is the row the buffer lands on, or -1 when no name starts with it.
// from is the focused row (-1 or null for none). A new single character,
// or one typed again and again, starts after the focused row and wraps,
// so pressing P steps through the names starting with P; a longer prefix
// starts at the focused row itself, so the row already found stays found
// while the name is typed on.
export function jump(names: readonly string[], buffer: string, from: number | null = null): number {
  const n = names.length;
  if (n === 0 || buffer === "") return -1;
  const b = fold(buffer);
  const at = from === null || from < 0 ? -1 : Math.min(from, n - 1);
  const find = (prefix: string, start: number): number => {
    for (let k = 0; k < n; k++) {
      const i = (start + k + n) % n;
      if (fold(names[i]).startsWith(prefix)) return i;
    }
    return -1;
  };
  if (b.length === 1) return find(b, at + 1);
  if (repeated(b)) {
    const whole = find(b, at < 0 ? 0 : at);
    return whole >= 0 ? whole : find(b[0], at + 1);
  }
  return find(b, at < 0 ? 0 : at);
}

// locksNow is Ctrl+L read through the same map, for the foot, which is on
// every page: Lock now from anywhere but a text field, a dialog or a menu
// (the caller checks the menu and the vault).
export function locksNow(k: KeyIn, ctx: { inField: boolean; dialogUp: boolean }): boolean {
  return intent(k, { list: "archives", onUp: false, ...ctx }).kind === "lock";
}

// survivor names the row that takes the focus and the selection when the
// rows `gone` leave the list (a Delete): the first row after the last one
// gone that stays, else the nearest before the first one gone, else none —
// Explorer's habit, so a keyboard user keeps their place (direction 4,
// list motion: the focus is kept across the motion; the final shoot of
// phase 4b found it dropped to the body).
export function survivor(ids: readonly string[], gone: ReadonlySet<string>): string | null {
  let last = -1;
  let first = -1;
  ids.forEach((id, i) => {
    if (!gone.has(id)) return;
    if (first < 0) first = i;
    last = i;
  });
  if (first < 0) return null;
  for (let i = last + 1; i < ids.length; i++) if (!gone.has(ids[i])) return ids[i];
  for (let i = first - 1; i >= 0; i--) if (!gone.has(ids[i])) return ids[i];
  return null;
}

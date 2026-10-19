import { describe, expect, it } from "vitest";
import { TYPEAHEAD_MS, controlKeepsKeys, intent, isTextEntry, jump, listScoped, locksNow, pageRows, target, typeahead } from "./listkeys";
import type { Intent, KeyCtx, KeyIn } from "./listkeys";

// The lists' keys (direction 7; APP.md §6): one keymap, Explorer's habits.
// Arrows move the selection (reversing 2026-09-10's "arrows move the
// focus"); the file list extends, toggles and moves the focus alone; the
// Archives list is single-select for the keys.

const k = (key: string, m: Partial<Omit<KeyIn, "key">> = {}): KeyIn => ({ key, ctrl: false, shift: false, alt: false, ...m });
const files: KeyCtx = { list: "files", onUp: false, inField: false, dialogUp: false };
const archives: KeyCtx = { list: "archives", onUp: false, inField: false, dialogUp: false };
const kind = (i: Intent) => i.kind;

describe("the file list", () => {
  const table: [KeyIn, Intent][] = [
    [k("ArrowDown"), { kind: "move", by: 1 }],
    [k("ArrowUp"), { kind: "move", by: -1 }],
    [k("Home"), { kind: "move", by: "home" }],
    [k("End"), { kind: "move", by: "end" }],
    [k("PageDown"), { kind: "move", by: "pageDown" }],
    [k("PageUp"), { kind: "move", by: "pageUp" }],
    [k("ArrowDown", { shift: true }), { kind: "extend", by: 1 }],
    [k("ArrowUp", { shift: true }), { kind: "extend", by: -1 }],
    [k("Home", { shift: true }), { kind: "extend", by: "home" }],
    [k("End", { shift: true }), { kind: "extend", by: "end" }],
    [k("PageDown", { shift: true }), { kind: "extend", by: "pageDown" }],
    [k("ArrowDown", { ctrl: true }), { kind: "focusOnly", by: 1 }],
    [k("ArrowUp", { ctrl: true }), { kind: "focusOnly", by: -1 }],
    [k(" "), { kind: "select" }],
    [k(" ", { ctrl: true }), { kind: "toggle" }],
    [k("a", { ctrl: true }), { kind: "selectAll" }],
    [k("A", { ctrl: true }), { kind: "selectAll" }],
    [k("Escape"), { kind: "clear" }],
    [k("Enter"), { kind: "open" }],
    [k("Backspace"), { kind: "up" }],
    [k("ArrowUp", { alt: true }), { kind: "up" }],
    [k("ArrowLeft", { alt: true }), { kind: "back" }],
    [k("e", { ctrl: true }), { kind: "extract" }],
    [k("Delete"), { kind: "delete" }],
    [k("F2"), { kind: "rename" }],
    [k("N", { ctrl: true, shift: true }), { kind: "newFolder" }],
    [k("F5"), { kind: "refresh" }],
    [k("l", { ctrl: true }), { kind: "lock" }],
    // The preview pane's View item (direction 3, phase 4a), Explorer's key.
    [k("p", { alt: true }), { kind: "previewPane" }],
    [k("P", { alt: true }), { kind: "previewPane" }],
    [k("p"), { kind: "typeahead", char: "p" }],
    [k("P", { shift: true }), { kind: "typeahead", char: "P" }],
    [k("2"), { kind: "typeahead", char: "2" }],
  ];
  it.each(table)("%o → %o", (key, want) => {
    expect(intent(key, files)).toEqual(want);
  });

  it("leaves the Archives list's own commands alone", () => {
    for (const key of [k("n", { ctrl: true }), k("o", { ctrl: true }), k("f", { ctrl: true }), k("Enter", { alt: true })]) {
      expect(kind(intent(key, files))).toBe("none");
    }
  });

  it("takes no other modifier on a command key", () => {
    for (const key of [k("Delete", { shift: true }), k("F2", { ctrl: true }), k("Backspace", { ctrl: true }), k("ArrowLeft"), k("ArrowRight", { alt: true }), k("ArrowDown", { alt: true }), k("e", { ctrl: true, shift: true }), k("Escape", { shift: true }), k("ArrowDown", { ctrl: true, shift: true }), k("x", { ctrl: true }), k("p", { alt: true, shift: true }), k("q", { alt: true })]) {
      expect(kind(intent(key, files))).toBe("none");
    }
  });

  it("sends Enter and Space on `..` up a level, and takes the arrows there like a row", () => {
    const up = { ...files, onUp: true };
    expect(intent(k("Enter"), up)).toEqual({ kind: "up" });
    expect(intent(k(" "), up)).toEqual({ kind: "up" });
    expect(intent(k(" ", { ctrl: true }), up)).toEqual({ kind: "none" });
    expect(intent(k("ArrowDown"), up)).toEqual({ kind: "move", by: 1 });
    expect(intent(k("ArrowDown", { shift: true }), up)).toEqual({ kind: "extend", by: 1 });
  });

  it("reads a Space as part of a name while one is being typed", () => {
    expect(intent(k(" "), { ...files, typing: true })).toEqual({ kind: "typeahead", char: " " });
    expect(intent(k(" ", { ctrl: true }), { ...files, typing: true })).toEqual({ kind: "toggle" });
  });
});

describe("the Archives list", () => {
  const table: [KeyIn, Intent][] = [
    [k("ArrowDown"), { kind: "move", by: 1 }],
    [k("ArrowUp"), { kind: "move", by: -1 }],
    [k("Home"), { kind: "move", by: "home" }],
    [k("End"), { kind: "move", by: "end" }],
    [k("PageDown"), { kind: "move", by: "pageDown" }],
    [k(" "), { kind: "select" }],
    [k("Enter"), { kind: "open" }],
    [k("Enter", { alt: true }), { kind: "details" }],
    [k("n", { ctrl: true }), { kind: "newArchive" }],
    [k("o", { ctrl: true }), { kind: "openFile" }],
    [k("f", { ctrl: true }), { kind: "filter" }],
    [k("Delete"), { kind: "delete" }],
    [k("F2"), { kind: "rename" }],
    [k("F5"), { kind: "refresh" }],
    [k("Escape"), { kind: "clear" }],
    [k("a", { ctrl: true }), { kind: "selectAll" }],
    [k("l", { ctrl: true }), { kind: "lock" }],
    [k("m"), { kind: "typeahead", char: "m" }],
  ];
  it.each(table)("%o → %o", (key, want) => {
    expect(intent(key, archives)).toEqual(want);
  });

  it("is single-select for the keys: no extend, no focus-only move, no toggle", () => {
    for (const key of [k("ArrowDown", { shift: true }), k("End", { shift: true }), k("ArrowDown", { ctrl: true }), k(" ", { ctrl: true })]) {
      expect(kind(intent(key, archives))).toBe("none");
    }
  });

  it("has no parent, no back, no Extract, no Create folder and no preview pane", () => {
    for (const key of [k("Backspace"), k("ArrowUp", { alt: true }), k("ArrowLeft", { alt: true }), k("e", { ctrl: true }), k("N", { ctrl: true, shift: true }), k("p", { alt: true })]) {
      expect(kind(intent(key, archives))).toBe("none");
    }
  });
});

describe("a text field", () => {
  it("keeps its own keys, Esc aside", () => {
    for (const ctx of [{ ...files, inField: true }, { ...archives, inField: true }]) {
      for (const key of [k("ArrowDown"), k("Home"), k("End", { shift: true }), k(" "), k("a", { ctrl: true }), k("Backspace"), k("Delete"), k("F2"), k("p"), k("Enter"), k("l", { ctrl: true }), k("e", { ctrl: true }), k("f", { ctrl: true }), k("p", { alt: true })]) {
        expect(kind(intent(key, ctx))).toBe("none");
      }
      expect(intent(k("Escape"), ctx)).toEqual({ kind: "clear" });
    }
  });

  it("lets Ctrl+O through on the Archives page, which edits no text (ruled 2026-10-14)", () => {
    expect(intent(k("o", { ctrl: true }), { ...archives, inField: true })).toEqual({ kind: "openFile" });
    expect(kind(intent(k("o", { ctrl: true }), { ...files, inField: true }))).toBe("none");
  });
});

describe("a control inside the list's region", () => {
  // A sort button, the tick-all box, the empty vault's New archive…: each
  // keeps its own Enter and Space, which the rows would otherwise take.
  it("keeps its Enter and Space and leaves the rest to the list", () => {
    for (const ctx of [{ ...files, onControl: true }, { ...archives, onControl: true }]) {
      for (const key of [k("Enter"), k(" "), k(" ", { ctrl: true }), k(" ", { shift: true })]) {
        expect(kind(intent(key, ctx))).toBe("none");
      }
      expect(kind(intent(k(" "), { ...ctx, typing: true }))).toBe("none");
      expect(intent(k("ArrowDown"), ctx)).toEqual({ kind: "move", by: 1 });
      expect(intent(k("a", { ctrl: true }), ctx)).toEqual({ kind: "selectAll" });
      expect(intent(k("p"), ctx)).toEqual({ kind: "typeahead", char: "p" });
      expect(intent(k("Escape"), ctx)).toEqual({ kind: "clear" });
    }
    expect(intent(k("Enter", { alt: true }), { ...archives, onControl: true })).toEqual({ kind: "details" });
  });

  // A fake element: closest finds the control the selector names, and
  // matches says whether it is a row's tick box.
  const el = (control: boolean, rowPart: boolean) => ({
    closest: (sel: string) => (control && sel.includes("button") ? { matches: (m: string) => rowPart && m.includes("tbody") } : null),
  });

  it("is a control that is not a part of a row", () => {
    expect(controlKeepsKeys(el(true, false))).toBe(true);
    expect(controlKeepsKeys(el(true, true))).toBe(false);
    expect(controlKeepsKeys(el(false, false))).toBe(false);
    expect(controlKeepsKeys(null)).toBe(false);
  });
});

describe("a dialog over the page", () => {
  it("takes every key, Esc included", () => {
    for (const ctx of [{ ...files, dialogUp: true }, { ...archives, dialogUp: true }, { ...files, dialogUp: true, inField: true }]) {
      for (const key of [k("ArrowDown"), k("Escape"), k("Enter"), k("Delete"), k("l", { ctrl: true }), k("o", { ctrl: true }), k("p")]) {
        expect(kind(intent(key, ctx))).toBe("none");
      }
    }
  });
});

describe("the scope of an intent", () => {
  it("keeps the list's own keys to the list and lets the commands run from the page", () => {
    for (const s of ["move", "extend", "focusOnly", "select", "toggle", "selectAll", "open", "typeahead"] as const) {
      expect(listScoped({ kind: s } as Intent)).toBe(true);
    }
    for (const s of ["clear", "up", "back", "extract", "delete", "rename", "newFolder", "refresh", "details", "newArchive", "openFile", "filter", "lock", "previewPane"] as const) {
      expect(listScoped({ kind: s } as Intent)).toBe(false);
    }
  });
});

describe("isTextEntry", () => {
  it("is a text input, a text area, a select or anything editable — never a checkbox or a button", () => {
    expect(isTextEntry("INPUT", "text", false)).toBe(true);
    expect(isTextEntry("INPUT", "search", false)).toBe(true);
    expect(isTextEntry("INPUT", "", false)).toBe(true);
    expect(isTextEntry("TEXTAREA", "", false)).toBe(true);
    expect(isTextEntry("SELECT", "", false)).toBe(true);
    expect(isTextEntry("DIV", "", true)).toBe(true);
    expect(isTextEntry("INPUT", "checkbox", false)).toBe(false);
    expect(isTextEntry("BUTTON", "button", false)).toBe(false);
    expect(isTextEntry("TR", "", false)).toBe(false);
  });
});

describe("target", () => {
  it("steps one row and stops at the ends", () => {
    expect(target(2, 1, 5, 3, false)).toBe(3);
    expect(target(4, 1, 5, 3, false)).toBe(4);
    expect(target(2, -1, 5, 3, false)).toBe(1);
    expect(target(0, -1, 5, 3, false)).toBe(0);
  });

  it("reaches `..` from the first row and comes back down from it", () => {
    expect(target(0, -1, 5, 3, true)).toBe(-1);
    expect(target(-1, -1, 5, 3, true)).toBe(-1);
    expect(target(-1, 1, 5, 3, true)).toBe(0);
    expect(target(0, "pageUp", 5, 3, true)).toBe(-1);
  });

  it("lands Home and End on the first and last row that can be selected, never on `..`", () => {
    expect(target(3, "home", 5, 3, true)).toBe(0);
    expect(target(-1, "end", 5, 3, true)).toBe(4);
  });

  it("moves a page at a time and stops at the ends", () => {
    expect(target(0, "pageDown", 10, 3, false)).toBe(3);
    expect(target(8, "pageDown", 10, 3, false)).toBe(9);
    expect(target(7, "pageUp", 10, 3, false)).toBe(4);
    expect(target(2, "pageUp", 10, 3, false)).toBe(0);
    expect(target(-1, "pageDown", 10, 3, true)).toBe(3);
  });

  it("starts from the top with no row focused, End from the bottom", () => {
    expect(target(null, 1, 5, 3, false)).toBe(0);
    expect(target(null, -1, 5, 3, false)).toBe(0);
    expect(target(null, "end", 5, 3, false)).toBe(4);
  });

  it("has only `..` or nothing in an empty folder", () => {
    expect(target(null, 1, 0, 3, true)).toBe(-1);
    expect(target(null, 1, 0, 3, false)).toBeNull();
  });
});

describe("pageRows", () => {
  it("is the rows that fit under the header, less the one kept in view", () => {
    // 600 px of list, a 36 px header, 36 px rows: 15 fit, 14 move.
    expect(pageRows(600, 36, 36)).toBe(14);
  });
  it("is never less than one", () => {
    expect(pageRows(40, 36, 36)).toBe(1);
    expect(pageRows(0, 0, 0)).toBe(1);
  });
});

describe("type-ahead", () => {
  it("builds a prefix within the window and starts again after it", () => {
    expect(typeahead("", "p", 1000, 0)).toBe("p");
    expect(typeahead("p", "h", 1000 + TYPEAHEAD_MS, 1000)).toBe("ph");
    expect(typeahead("ph", "o", 3000, 1000)).toBe("o");
  });

  const names = ["apple.txt", "Photos 2024", "notes.md", "phone.jpg", "Pictures", "zeta"];

  it("jumps to the first name that starts with the buffer, case-folded", () => {
    expect(jump(names, "PH")).toBe(1);
    expect(jump(names, "no")).toBe(2);
    expect(jump(names, "q")).toBe(-1);
    expect(jump([], "a")).toBe(-1);
    expect(jump(names, "")).toBe(-1);
  });

  it("steps through the names that start with one character typed again, wrapping from the focused row", () => {
    expect(jump(names, "p", 1)).toBe(3);
    expect(jump(names, "p", 3)).toBe(4);
    expect(jump(names, "p", 4)).toBe(1);
    expect(jump(names, "pp", 3)).toBe(4);
    expect(jump(names, "ppp", 4)).toBe(1);
  });

  it("keeps the row found while the name is typed on", () => {
    expect(jump(names, "ph", 3)).toBe(3);
    expect(jump(names, "pho", 3)).toBe(3);
    expect(jump(names, "phot", 3)).toBe(1);
  });

  it("takes a character typed with AltGr, which arrives as Ctrl+Alt", () => {
    expect(intent(k("@", { ctrl: true, alt: true }), files)).toEqual({ kind: "typeahead", char: "@" });
    expect(intent(k("é", { ctrl: true, alt: true, shift: true }), archives)).toEqual({ kind: "typeahead", char: "é" });
    // Alt alone is not AltGr: Alt+Q is nothing (Alt+P is the preview pane's).
    expect(kind(intent(k("q", { alt: true }), files))).toBe("none");
    expect(kind(intent(k("x", { ctrl: true }), files))).toBe("none");
  });

  it("prefers a name that really starts with a repeated character", () => {
    expect(jump(["aardvark", "apple", "axe"], "aa", 1)).toBe(0);
  });
});

describe("locksNow, the foot's Ctrl+L on every page", () => {
  const key = (o: Partial<KeyIn>): KeyIn => ({ key: "l", ctrl: true, shift: false, alt: false, ...o });
  it("is Ctrl+L alone", () => {
    expect(locksNow(key({}), { inField: false, dialogUp: false })).toBe(true);
    expect(locksNow(key({ key: "L" }), { inField: false, dialogUp: false })).toBe(true);
    expect(locksNow(key({ shift: true }), { inField: false, dialogUp: false })).toBe(false);
    expect(locksNow(key({ alt: true }), { inField: false, dialogUp: false })).toBe(false);
    expect(locksNow(key({ ctrl: false }), { inField: false, dialogUp: false })).toBe(false);
  });
  it("leaves a field its keys and a dialog its own", () => {
    expect(locksNow(key({}), { inField: true, dialogUp: false })).toBe(false);
    expect(locksNow(key({}), { inField: false, dialogUp: true })).toBe(false);
  });
});

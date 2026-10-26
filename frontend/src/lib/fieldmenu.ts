// A text field's menu (direction 7, Explorer's habits): *Cut* · *Copy* ·
// *Paste* · ─ · *Select all*, each with its key, on a right-click, Shift+F10
// or the Menu key in a field — the app's own menu (ContextMenu), since the
// shell turned WebView2's off (2026-09-06, for its "Save image as"; an
// app-drawn menu is not that one). One action, use:fieldMenu, applied by
// TextField and by every plain field of the app: the Archives page's
// Filter, the record's name and description, the extract destination, the
// delete dialog's typed name, the import path, a key's label.
//
// Cut and Copy run on the field's selection through document.execCommand,
// greyed with none; Paste reads the clipboard through the WebView
// (navigator.clipboard.readText()) and writes it over the selection, and
// where the WebView refuses that read the item is greyed with the key that
// still pastes (fieldCopy.pasteRefused, "Use Ctrl+V"). Select all selects
// the field, greyed when it is empty. A read-only field keeps Copy and
// Select all.
//
// A SECRET FIELD GETS NO MENU. The PIN, the password, the recovery key's
// cells and its readback are marked data-secret (SecretField,
// RecoveryInput, RecoveryReveal), and the action refuses any field inside
// that mark or of type password (fieldMenuFits): a secret never passes
// through the page's own menu code, and those fields keep the WebView's
// nothing. fieldmenu.test.ts holds the rule and the marks.
import { mount, unmount } from "svelte";
import ContextMenu from "../components/ContextMenu.svelte";
import { opensMenu } from "./listkeys";
import { KEY_ECHO_MS, groups, keyAnchor } from "./menu";
import type { CmdItem, MenuClose, Point } from "./menu";
import { fieldCopy, keyNames } from "./strings";

// The mark a secret field carries, on itself or on what holds it.
export const SECRET_ATTR = "data-secret";

export interface FieldFacts {
  tag: string;
  // An input's type ("text" when it names none); "" for a text area.
  type: string;
  // The field is inside the secret mark.
  secret: boolean;
}

const TEXT_TYPES = new Set(["text", "search", "url", "email", "tel", ""]);

// fieldMenuFits: the fields that get the menu — a text input or a text
// area that is not a secret. A password field never does, whatever marks
// it carries.
export function fieldMenuFits(f: FieldFacts): boolean {
  if (f.secret) return false;
  const tag = f.tag.toUpperCase();
  if (tag === "TEXTAREA") return true;
  return tag === "INPUT" && TEXT_TYPES.has(f.type.toLowerCase());
}

export type FieldCmd = "cut" | "copy" | "paste" | "selectAll";

export interface FieldState {
  // Some of the field's text is selected.
  selected: boolean;
  empty: boolean;
  readOnly: boolean;
  // The WebView will not hand the page the clipboard.
  pasteRefused: boolean;
}

// fieldItems: the menu as the field stands when it is opened.
export function fieldItems(s: FieldState): CmdItem<FieldCmd>[] {
  const paste = s.readOnly ? { disabled: true } : s.pasteRefused ? { disabled: true, title: fieldCopy.pasteRefused } : {};
  return groups<CmdItem<FieldCmd>>(
    [
      { cmd: "cut", label: fieldCopy.cut, keys: keyNames.cut, disabled: s.readOnly || !s.selected },
      { cmd: "copy", label: fieldCopy.copy, keys: keyNames.copy, disabled: !s.selected },
      { cmd: "paste", label: fieldCopy.paste, keys: keyNames.paste, ...paste },
    ],
    [{ cmd: "selectAll", label: fieldCopy.selectAll, keys: keyNames.selectAll, disabled: s.empty }],
  );
}

// ---- the action ---------------------------------------------------------

type Field = HTMLInputElement | HTMLTextAreaElement;

function factsOf(el: Field): FieldFacts {
  return { tag: el.tagName, type: el instanceof HTMLInputElement ? el.type : "", secret: el.closest(`[${SECRET_ATTR}]`) !== null };
}

// refused: the WebView refused a read once, and will again; the next menu
// greys Paste rather than offering what does nothing.
let refused = false;

async function pasteBlocked(): Promise<boolean> {
  if (refused || !navigator.clipboard?.readText) return true;
  try {
    const p = await navigator.permissions?.query({ name: "clipboard-read" as PermissionName });
    return p?.state === "denied";
  } catch {
    // A WebView that cannot say: offered, and greyed after a refusal.
    return false;
  }
}

// One field menu at a time, across the app.
let current: { node: Field; close: () => void } | null = null;

async function open(node: Field, at: Point, keyboard: boolean): Promise<void> {
  current?.close();
  // The selection as the menu found it: a right-click may move the caret,
  // and a command acts on what was selected when it was opened.
  const start = node.selectionStart ?? 0;
  const end = node.selectionEnd ?? 0;
  const blocked = await pasteBlocked();
  if (!node.isConnected || node.disabled) return;
  const state: FieldState = { selected: end > start, empty: node.value === "", readOnly: node.readOnly, pasteRefused: blocked };

  let inst: ReturnType<typeof mount> | null = null;
  const entry = { node, close: () => done("pointer") };
  // The focus and the selection go back to the field on a key or a
  // command, as a native menu leaves them; a press elsewhere keeps its own.
  function done(how: MenuClose): void {
    if (inst) void unmount(inst, { outro: true });
    inst = null;
    if (current === entry) current = null;
    if (how !== "pointer" && node.isConnected) {
      node.focus({ preventScroll: true });
      node.setSelectionRange(start, end);
    }
  }
  // The items as ContextMenu takes them (MenuButton's MenuItem): each with
  // the function that runs it.
  const items: (CmdItem<FieldCmd> & { run: () => void })[] = fieldItems(state).map((i) => ({ ...i, run: () => run(node, i.cmd, start, end) }));
  current = entry;
  inst = mount(ContextMenu, { target: document.body, props: { x: at.x, y: at.y, items, keyboard, label: fieldCopy.menuLabel, onclose: done }, intro: true });
}

function run(node: Field, cmd: FieldCmd, start: number, end: number): void {
  node.focus({ preventScroll: true });
  switch (cmd) {
    case "cut":
    case "copy":
      // The command runs in the press that chose it, so the WebView lets
      // the page write the clipboard.
      node.setSelectionRange(start, end);
      document.execCommand(cmd);
      break;
    case "selectAll":
      node.select();
      break;
    case "paste":
      void paste(node, start, end);
      break;
  }
}

async function paste(node: Field, start: number, end: number): Promise<void> {
  let text: string;
  try {
    text = await navigator.clipboard.readText();
  } catch {
    refused = true;
    return;
  }
  if (!node.isConnected) return;
  node.focus({ preventScroll: true });
  node.setSelectionRange(start, end);
  // insertText keeps the field's own undo and raises its input event, so a
  // bound value follows; where it is not run, the text is set and the
  // event raised by hand.
  if (!document.execCommand("insertText", false, text)) {
    node.setRangeText(text, start, end, "end");
    node.dispatchEvent(new Event("input", { bubbles: true }));
  }
}

// fieldMenu: the action. A field the rule refuses gets nothing — no
// listener at all — so a secret's right-click and Menu key stay the
// WebView's, which is nothing.
export function fieldMenu(node: Field): { destroy: () => void } | undefined {
  if (!fieldMenuFits(factsOf(node))) return undefined;
  // The Menu key's own contextmenu event, which Windows sends after the
  // key the action has already answered, is spent rather than opening a
  // second menu at the pointer.
  let keyAt = -Infinity;
  const onMenu = (e: MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    if (performance.now() - keyAt < KEY_ECHO_MS) return;
    void open(node, { x: e.clientX, y: e.clientY }, false);
  };
  const onKey = (e: KeyboardEvent) => {
    if (e.isComposing || !opensMenu({ key: e.key, ctrl: e.ctrlKey || e.metaKey, shift: e.shiftKey, alt: e.altKey })) return;
    e.preventDefault();
    e.stopPropagation();
    keyAt = performance.now();
    void open(node, keyAnchor(node.getBoundingClientRect()), true);
  };
  // As an element: the two field types' listener overloads do not unite.
  const el: HTMLElement = node;
  el.addEventListener("contextmenu", onMenu);
  el.addEventListener("keydown", onKey);
  return {
    destroy() {
      el.removeEventListener("contextmenu", onMenu);
      el.removeEventListener("keydown", onKey);
      if (current?.node === node) current.close();
    },
  };
}

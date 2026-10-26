import { describe, expect, it } from "vitest";
import { fieldItems, fieldMenuFits } from "./fieldmenu";
import type { FieldState } from "./fieldmenu";
import { fieldCopy } from "./strings";

// A text field's menu (direction 7): Cut · Copy · Paste · Select all for a
// field that is not a secret, and nothing at all for one that is.

describe("which fields get the menu", () => {
  it("is a text input — text, search and their kin — or a text area", () => {
    for (const type of ["text", "search", "url", "email", "tel", "TEXT"]) expect(fieldMenuFits({ tag: "INPUT", type, secret: false }), type).toBe(true);
    expect(fieldMenuFits({ tag: "textarea", type: "", secret: false })).toBe(true);
  });

  it("is never a secret: a field inside the data-secret mark, or a password", () => {
    expect(fieldMenuFits({ tag: "INPUT", type: "text", secret: true })).toBe(false);
    expect(fieldMenuFits({ tag: "TEXTAREA", type: "", secret: true })).toBe(false);
    // SecretField's revealed state is type=text: the mark still holds it.
    expect(fieldMenuFits({ tag: "INPUT", type: "password", secret: false })).toBe(false);
  });

  it("is never a box, a button, a slider or a select", () => {
    for (const type of ["checkbox", "radio", "range", "button", "submit", "file", "number"]) expect(fieldMenuFits({ tag: "INPUT", type, secret: false }), type).toBe(false);
    expect(fieldMenuFits({ tag: "SELECT", type: "", secret: false })).toBe(false);
  });
});

// The secret components, read as source: no field of theirs takes the
// action, and every field they draw carries the mark the rule reads.
const secretSources = import.meta.glob(["../components/SecretField.svelte", "../components/SecretInput.svelte", "../components/RecoveryInput.svelte", "../components/RecoveryReveal.svelte"], { query: "?raw", import: "default", eager: true }) as Record<string, string>;
const plainSources = import.meta.glob(["../components/TextField.svelte"], { query: "?raw", import: "default", eager: true }) as Record<string, string>;

describe("the secret fields (direction 7: a secret never passes through the page's menu code)", () => {
  it("are all read", () => {
    expect(Object.keys(secretSources)).toHaveLength(4);
  });

  it("never apply the field menu", () => {
    for (const [file, src] of Object.entries(secretSources)) expect(src.includes("fieldMenu"), file).toBe(false);
  });

  it("mark every field they draw data-secret", () => {
    let fields = 0;
    for (const [file, src] of Object.entries(secretSources)) {
      for (const tag of src.match(/<input\b[^>]*>/g) ?? []) {
        fields++;
        expect(tag.includes("data-secret"), `${file}: ${tag.slice(0, 60)}`).toBe(true);
      }
    }
    // SecretField's input, RecoveryInput's cells, RecoveryReveal's readback.
    expect(fields).toBe(3);
  });

  it("while TextField, the plain field, applies it", () => {
    expect(Object.values(plainSources)[0]).toContain("use:fieldMenu");
  });
});

describe("the menu's items", () => {
  const at = (o: Partial<FieldState> = {}): FieldState => ({ selected: true, empty: false, readOnly: false, pasteRefused: false, ...o });
  const read = (s: FieldState) => fieldItems(s).map((i) => `${i.sep ? "── " : ""}${i.label} ${i.keys}${i.disabled ? ` [off${i.title ? `: ${i.title}` : ""}]` : ""}`);

  it("is Cut, Copy, Paste, then Select all, each with its key", () => {
    expect(read(at())).toEqual(["Cut Ctrl+X", "Copy Ctrl+C", "Paste Ctrl+V", "── Select all Ctrl+A"]);
  });

  it("greys Cut and Copy with nothing selected, Select all in an empty field", () => {
    expect(read(at({ selected: false }))).toEqual(["Cut Ctrl+X [off]", "Copy Ctrl+C [off]", "Paste Ctrl+V", "── Select all Ctrl+A"]);
    expect(read(at({ selected: false, empty: true }))[3]).toBe("── Select all Ctrl+A [off]");
  });

  it("greys Paste with Use Ctrl+V where the WebView refuses the read", () => {
    expect(read(at({ pasteRefused: true }))[2]).toBe(`Paste Ctrl+V [off: ${fieldCopy.pasteRefused}]`);
  });

  it("keeps Copy and Select all in a read-only field", () => {
    expect(read(at({ readOnly: true, pasteRefused: true }))).toEqual(["Cut Ctrl+X [off]", "Copy Ctrl+C", "Paste Ctrl+V [off]", "── Select all Ctrl+A"]);
  });
});

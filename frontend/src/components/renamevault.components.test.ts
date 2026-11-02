// Rename the vault (APP.md §6, the Vault page; ruled 2026-11-02), read as
// source. No component is mounted in a test here: the rule is
// lib/validate's (vaultNameProblem, vaultNameReady), tested there, so this
// pins the wiring that test cannot see — that the field says what the rule
// says and nothing else, judged against the name the dialog opened with,
// that the dialog's submit is greyed by the rule, that Enter in the field
// is still the form's press while it is greyed, and that nothing sends a
// name the rule would not enable. The precedent is rekey.components.test.ts.
import { describe, expect, it } from "vitest";

const sources = import.meta.glob(["./RenameVaultDialog.svelte"], { query: "?raw", import: "default", eager: true }) as Record<string, string>;

function src(): string {
  const hit = Object.values(sources)[0];
  if (!hit) throw new Error("not read: RenameVaultDialog");
  return hit.replace(/\r\n/g, "\n");
}

// The markup alone: no script, no style, no comment.
function markup(): string {
  return src()
    .replace(/<script[\s\S]*?<\/script>/g, "")
    .replace(/<style[\s\S]*?<\/style>/g, "")
    .replace(/<!--[\s\S]*?-->/g, "");
}

describe("Rename the vault, read as source", () => {
  it("judges the field by the vault's name rule alone", () => {
    const s = src();
    expect(s).toContain("const judge = (v: string): string => vaultNameProblem(v, opened);");
    expect(markup()).toMatch(/<TextField id="vault-name"[^>]*\{judge\}/);
  });

  // The store shows the typed name the moment a save begins, so the `name`
  // prop is the name being saved while the save runs: the rule is asked of
  // the name the dialog opened with, or Rename would grey under its own
  // pending ring.
  it("judges against the name the dialog opened with, never the live prop", () => {
    const s = src();
    expect(s).toContain("const opened = name;");
    expect(s).toContain("let value = $state(opened);");
    expect(s).not.toMatch(/vaultName(Problem|Ready)\([^)]*\bname\)/);
  });

  it("greys its submit until the rule enables it, and sends nothing it would not", () => {
    const s = src();
    expect(s).toContain("const ready = $derived(vaultNameReady(value, opened));");
    expect(markup()).toMatch(/<button type="submit" class="btn accent" disabled=\{!ready\}/);
    const body = s.slice(s.indexOf("async function submit()"));
    expect(body.indexOf("if (!ready) return;")).toBeGreaterThan(-1);
    expect(body.indexOf("if (!ready) return;")).toBeLessThan(body.indexOf("store.applySettings"));
  });

  // A form whose default button is disabled gets no submit from Enter, so
  // the field's own keydown makes it the form's press while Rename is
  // greyed: the press marks an empty or too long name and sends nothing.
  it("takes Enter in the field as the form's press while its submit is greyed", () => {
    const s = src();
    expect(markup()).toContain('<div role="presentation" onkeydown={pressWhileGreyed}><TextField id="vault-name"');
    const handler = s.slice(s.indexOf("function pressWhileGreyed(e: KeyboardEvent)"), s.indexOf("async function submit()"));
    expect(handler).toContain('if (e.key !== "Enter" || e.isComposing || ready || saving) return;');
    expect(handler.indexOf("e.preventDefault();")).toBeGreaterThan(-1);
    expect(handler.indexOf("void submit();")).toBeGreaterThan(handler.indexOf("e.preventDefault();"));
    const body = s.slice(s.indexOf("async function submit()"));
    expect(body.indexOf("attempt++;")).toBeGreaterThan(-1);
    expect(body.indexOf("attempt++;")).toBeLessThan(body.indexOf("if (!ready) return;"));
  });

  // app.css's `.dialog p` gives a paragraph the second ink, which would
  // win over .field-error's danger ink.
  it("says a refusal in the danger ink, never as a paragraph of the dialog", () => {
    expect(markup()).toContain('<div class="field-error" role="alert">{refused}</div>');
    expect(markup()).not.toMatch(/<p class="field-error"/);
  });

  it("keeps Cancel as it was, greyed only while a save is under way", () => {
    expect(markup()).toContain('<button type="button" class="btn" disabled={!!saving} onclick={onclose}>{dialogCopy.cancel}</button>');
  });
});

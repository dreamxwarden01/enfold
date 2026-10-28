// The components of the acts that may re-key the vault (ruled 2026-10-27;
// docs/research/vmk-rotation.md 3.1-3.4, 3.6, 3.8), read as source. No
// component is mounted in a test here (the decisions are lib/rekey.ts's,
// lib/lockscreen.ts's and lib/reveal.ts's, tested there), so these pin the
// wiring those tests cannot see: which dialogs are forms and what they open
// on, which bound calls carry the answer and the backup's file, that every
// act is recorded for its end's next step, that the panel asks the tested
// predicates rather than lists of its own, and that no new component says
// on screen what strings.ts does not. The precedent is fieldmenu.test.ts
// and infobar.test.ts, which read components the same way.
import { describe, expect, it } from "vitest";

const sources = import.meta.glob(
  [
    "./KeysPage.svelte",
    "./EntangledDialog.svelte",
    "./ReplaceDialog.svelte",
    "./RekeyQuestion.svelte",
    "./BackupChoice.svelte",
    "./CeremonyPanel.svelte",
    "./RecoveryReveal.svelte",
    "./Dialog.svelte",
    "./Lock.svelte",
    "./ImportDialog.svelte",
  ],
  { query: "?raw", import: "default", eager: true },
) as Record<string, string>;

function src(name: string): string {
  const hit = Object.entries(sources).find(([f]) => f.endsWith(`/${name}.svelte`));
  if (!hit) throw new Error(`not read: ${name}`);
  // One line ending, so a pattern that spans lines matches in a CRLF file.
  return hit[1].replace(/\r\n/g, "\n");
}

// The markup alone: no script, no style, no comment, so a test of what is
// drawn reads only what is drawn.
function markup(name: string): string {
  return src(name)
    .replace(/<script[\s\S]*?<\/script>/g, "")
    .replace(/<style[\s\S]*?<\/style>/g, "")
    .replace(/<!--[\s\S]*?-->/g, "");
}

// The opening tag that contains a marker, whole.
function tagWith(s: string, marker: string): string {
  const at = s.indexOf(marker);
  if (at < 0) return "";
  const from = s.lastIndexOf("<", at);
  return s.slice(from, s.indexOf(">", at) + 1);
}

describe("the dialogs that may re-key, read as source", () => {
  it("reads every component it pins", () => {
    expect(Object.keys(sources)).toHaveLength(10);
  });

  it("asks the fact in a fieldset marked as the question, the re-keying answer first and each answer's second line drawn", () => {
    const q = markup("RekeyQuestion");
    expect(q).toMatch(/<fieldset[^>]*data-question/);
    expect(q).toContain("<legend");
    const radios = q.match(/<input type="radio"[^>]*>/g) ?? [];
    expect(radios.map((r) => r.match(/value="(\w+)"/)?.[1])).toEqual(["rekey", "keep"]);
    for (const r of radios) expect(r).toContain("bind:group={answer}");
    expect(q).toContain("dialogCopy.rekeyQuestion.rekeys");
    expect(q).toContain("dialogCopy.rekeyQuestion.keeps");
  });

  it("opens a form with no field on the checked answer of its question, and only then on its submit", () => {
    const d = src("Dialog");
    expect(d).toContain(`'fieldset input[type="radio"]:checked:not([disabled])'`);
    expect(d).toContain("(answer ?? act ?? box).focus()");
  });

  it("makes the removal, the change, turning on and the replacement forms whose primary is the submit", () => {
    const k = markup("KeysPage");
    expect(k).toMatch(/<Dialog title=\{dialogCopy\.removeUnlockMethod\.title\([^\n]*onsubmit=\{\(\) => void doRemove\(\)\}>/);
    expect(k).toMatch(/<button type="submit" class="btn danger-fill">\{primaryOf\("remove"/);
    const e = markup("EntangledDialog");
    expect(tagWith(e, "<Dialog")).toContain("onsubmit={asks ? go : undefined}");
    expect(e).toMatch(/<button type="submit" class="btn accent">\{primary\}/);
    const r = markup("ReplaceDialog");
    expect(tagWith(r, "<Dialog")).toContain("onsubmit={go}");
    expect(r).toMatch(/<button type="submit" class="btn accent">\{primaryOf\("replace"/);
  });

  it("keeps turning the password off a standard button that asks nothing", () => {
    const e = markup("EntangledDialog");
    expect(e).toContain('<button type="button" class="btn" onclick={go}>{primary}</button>');
    expect(src("EntangledDialog")).toContain('Keys.SetEntangled(false, false, "")');
  });

  it("names each primary by what the press does, never by the words the design removed", () => {
    for (const name of ["KeysPage", "EntangledDialog", "ReplaceDialog"]) {
      const s = src(name);
      expect(s, name).toContain("primaryOf(");
      expect(s, name).not.toContain("removeUnlockMethod.primary");
      expect(s, name).not.toContain("primaryWithBackup");
      expect(s, name).not.toContain("backupFirst");
    }
  });

  it("begins every act with its answer and the file its backup goes to", () => {
    const k = src("KeysPage");
    expect(k).toContain("Keys.RemoveSlot(id, rekey, b)");
    expect(k).toContain("Keys.RotateNow(b)");
    expect(k).toContain("Keys.ExportBackup(b)");
    expect(k).toContain("Keys.BeginEnroll(kind, label, false)");
    expect(k).toContain("Keys.BeginEnroll(kind, label, true)");
    const e = src("EntangledDialog");
    expect(e).toContain("Keys.SetEntangled(true, rekey, b)");
    expect(e).toContain("Keys.ChangeEntangledPassword(rekey, b)");
    expect(src("ReplaceDialog")).toContain("Keys.ReplaceRecoveryKeys(b)");
    // The re-key that never re-keyed (the export, then RotateNow refused
    // while the export's ceremony ran) is gone with the function.
    expect(k).not.toMatch(/async function rekey\(/);
  });

  it("asks the Save dialog for the backup's file before anything begins, and begins nothing when it is cancelled", () => {
    for (const [name, pick] of [["KeysPage", "pickBackup()"], ["EntangledDialog", "pick()"], ["ReplaceDialog", "pick()"]] as const) {
      const s = src(name);
      const n = s.split(`path = await ${pick};`).length - 1;
      expect(n, name).toBeGreaterThan(0);
      expect(s.split("if (!path) return").length - 1, name).toBe(n);
    }
  });

  it("records every act the page begins for its end's next step, and an act with none as none", () => {
    const k = src("KeysPage");
    expect(k).toContain('store.noteAct({ act: "remove", type: target.type, rekey, fromFollow: false })');
    expect(k).toContain('store.noteAct({ act: "rotate", rekey: true, fromFollow: false })');
    expect(k).toContain('store.noteAct({ act: "enroll", type: kind, fromFollow })');
    // An export and a reveal have no next step.
    expect(k.split("store.noteAct(null)").length - 1).toBe(2);
    expect(k).toContain("store.noteRecoverySwitch()");
    const e = src("EntangledDialog");
    expect(e).toContain('store.noteAct({ act: m === "on" ? "on" : "password", rekey, fromFollow })');
    expect(e).toContain("store.noteAct(null)");
    expect(src("ReplaceDialog")).toContain('store.noteAct({ act: "replace", rekey: true, fromFollow })');
  });

  it("draws the panel by the tested predicates, never by lists or lookups of its own", () => {
    const c = src("CeremonyPanel");
    for (const f of ["provesFirst(c.kind)", "choosesVaultPassword(c.kind)", "doneTitleOf(c)", "phaseLeadOf(c)", "recoveryNoteOf(c, store.lastAct, recoveryKeys)", "failedNotes(c)", "offersRecoverySwitch(c, store.lastAct, store.slots, second)", "endsWithMore(c,"]) {
      expect(c, f).toContain(f);
    }
    expect(c).not.toContain('c.kind === "entangle"');
    expect(c).not.toContain("ceremonyCopy.doneTitle[");
    expect(c).not.toContain("recoveryNote[c.kind]");
    expect(c).not.toMatch(/\]\.includes\(c\.kind\)/);
  });

  it("answers the replacement's prompts through the one mailbox, and keep mode is not closed by Escape", () => {
    expect(src("CeremonyPanel")).toContain('submitSecret("confirm", c.promptId, "")');
    const r = src("RecoveryReveal");
    expect(r).toContain('submitSecret("kept", keep.promptId, "")');
    expect(r).toContain("keepBand(");
    expect(markup("RecoveryReveal")).toContain("onclose={() => {}}");
  });

  it("mounts keep mode in the act's dialog's place at StepKeepKey, one key after another", () => {
    const k = markup("KeysPage");
    expect(k).toContain("c.step === CeremonyStep.StepKeepKey");
    expect(k).toMatch(/\{#key c\.slotLabel\}\s*<RecoveryReveal[^>]*keep=\{/);
  });

  // The ready line's minutes are the ones the session runs on: the
  // settings answer the vault's minutes as stored, which the core replaces
  // with the default when out of range (settings.go timeouts), so an
  // imported vault that stores 60 would be promised 60 and locked after 10.
  it("hands the replacement the idle time the session runs on, never the setting as stored", () => {
    // The whole element, to its self-closing end: its onclose's arrow holds
    // a ">", where tagWith would stop.
    const tag = markup("KeysPage").match(/<ReplaceDialog\b[^\n]*?\/>/)?.[0] ?? "";
    expect(tag).toContain("onclose=");
    expect(tag).toContain("minutes={replaceMinutes(st)}");
    expect(tag).not.toContain("idleMinutes");
  });

  it("titles the ceremony dialog by its kind, a replacement by its count", () => {
    const k = src("KeysPage");
    expect(k).toContain("ceremonyTitleOf(shownC, recoveryKeys)");
    expect(k).not.toContain("vaultCopy.ceremonyTitle[");
  });

  it("greys Replace recovery keys… and a YubiKey's Remove… with what to do as the title", () => {
    const k = markup("KeysPage");
    expect(k).toContain("disabled={!!replaceWhy} title={replaceWhy}");
    expect(src("KeysPage")).toContain("removeRule(s.type)");
  });

  it("greys Finish setting up… on a vault whose password is on, says why under the title, and gives the accent and the focus to the recovery key", () => {
    const l = markup("Lock");
    expect(src("Lock")).toContain("finishGreyed(st)");
    // The caption says what the greyed button's title says, never the
    // setup line that asks for the method the button cannot add.
    expect(l).toContain('<div class="cap">{setupNeeded ? (finishWhy ?? lockCopy.setupLine) :');
    expect(l).toMatch(/\{:else if setupNeeded && finishWhy\}\s*<button type="button" class="btn wide" disabled title=\{finishWhy\}>/);
    expect(l).toMatch(/<button type="button" class="btn accent wide" onclick=\{\(\) => begin\("recovery"\)\} use:takeFocus>\{lockCopy\.unlockWithRecovery\}/);
  });

  it("draws the backup's tick only where a backup can be made, and the line that says why in its place", () => {
    const b = markup("BackupChoice");
    expect(b).toMatch(/\{#if possible\}\s*<label class="check"><input type="checkbox" bind:checked=\{ticked\} \/>\{dialogCopy\.backupAfter\.tick\}<\/label>\s*\{:else\}\s*<p>\{dialogCopy\.backupAfter\.none\}<\/p>/);
    expect(src("BackupChoice")).toContain("possible && backupTicked(");
  });

  it("says the import's two sentences where the design puts them", () => {
    const i = markup("ImportDialog");
    expect(i.split("{#if info.rekeyed}<p>{importCopy.beforeRekey}</p>{/if}").length - 1).toBe(2);
    expect(i).toContain("st.retireOwed ? ` ${importCopy.replacedOwes}` : \"\"");
  });

  // The final shoot's two fixes (at a 1086 x 694 window): a refusal
  // of the Vault page's own press brings its bar into view, and the import
  // dialog says its confirmation before its cards, in the medium box, and
  // brings the cards into view when its press marks them.
  it("brings the page's refusal into view, and the import's confirmation above its cards", () => {
    expect(src("KeysPage")).toMatch(/function fail\(e: unknown\) \{\s*store\.notice\(codeText\(errorOf\(e\)\.code\), "error"\);\s*slot\?\.reveal\(\);/);
    expect(markup("KeysPage")).toContain("<NoticeSlot flow bind:this={slot}>");
    const i = markup("ImportDialog");
    expect(i).toContain("doImport())} anchored medium>");
    const tick = i.indexOf('<label class="check"><input type="checkbox" bind:checked={confirm} />');
    expect(tick).toBeGreaterThan(i.lastIndexOf("{#if info.rekeyed}<p>{importCopy.beforeRekey}</p>{/if}"));
    expect(i.indexOf("<FirstWayIn ")).toBeGreaterThan(tick);
    expect(i.indexOf('<select id="im-method"')).toBeGreaterThan(tick);
    expect(src("ImportDialog")).toMatch(/if \(!way\) \{[^}]*first\?\.reveal\(\);\s*return;\s*\}/);
  });

  it("says nothing on screen that strings.ts does not, in the components this act added", () => {
    for (const name of ["RekeyQuestion", "BackupChoice", "ReplaceDialog"]) {
      const words = markup(name).match(/>\s*[A-Za-z][^<{]*</g) ?? [];
      expect(words, name).toEqual([]);
    }
  });
});

<script lang="ts">
  // The recovery key's dialog (APP.md §3 Keys, §6; direction 9, ruled
  // 2026-10-18): fetched once from the one-time URL the core minted, shown
  // in groups, and left by one of three ways — printed, saved to a text
  // file the core writes, or written down. The URL's token is the handle a
  // save uses; the digits never ride a bound call.
  //
  // 520 px (Dialog's medium), the key ID once as the heading's label, both
  // risks in two sentences, and *Print…* the suggested way: a sheet on
  // paper, away from this PC. *Save as a text file…* opens the native Save
  // dialog at once; its caution is a caption in the body.
  //
  // The saved file and the print are named "Enfold Keystore Recovery Key
  // <ID>" — BitLocker's own shape, the key's ID (FORMAT.md §18.4) and
  // never the vault's name, which says nothing about which sheet this is.
  // The same string heads the sheet and is document.title while the print
  // runs, which is what Print to PDF offers as the file name.
  //
  // None of the three asks a second time (ruled 2026-09-09). A print is
  // not watched for: the spooler watch that once told a print from a
  // cancelled one could not see *Save as PDF* at all — the WebView's print
  // dialog writes that PDF itself and no spooler job exists — so pressing
  // *Print…* counts as done, and so does *I have written it down*. The two
  // ways that leave the machine stand in a row under the digits, at their
  // own width; the band holds the one statement, *I have written it down*,
  // which is *Done* after any of the three and then closes with one click
  // (lib/reveal.ts ways, band; ruled 2026-10-18, the user's).
  //
  // The readback, *Check what I wrote*, is optional and never a gate: two
  // groups drawn once per dialog, each ticked when its six digits match.
  // What is typed there is compared in this page's memory and nowhere
  // else, and goes with the dialog.
  //
  // Keep mode (keep set) is the same dialog for a new key Replace recovery
  // keys… shows before it is the vault's (APP.md §13, ruled 2026-10-27,
  // R-B), which the Vault page mounts in the act's dialog's place while the
  // ceremony is at StepKeepKey, one key after another (keyed on the URL).
  // Its differences: the title counts the keys, a caption says which old
  // key this one replaces so the new sheet goes where the old one is, the
  // lead says from when the key opens the vault, and the digits' caption
  // that nothing changes until the confirmation. Each way still counts at
  // once, with no second confirmation: the act's own confirmation, before
  // its commit, pairs every new key with the one it replaces. The band goes
  // on to the next key (lib/reveal.ts keepBand), its press answering the
  // ceremony's kept prompt; Cancel is its one way out, which cancels the
  // replacement, and Escape and the backdrop do nothing here as in every
  // showing. A key that cannot be shown leaves Cancel alone: any other
  // button would answer that a key the user never saw was kept.
  import { tick } from "svelte";
  import Dialog from "./Dialog.svelte";
  import { Keys, Shell, Vault, errorOf, submitSecret } from "../lib/api";
  import { codeText, revealCopy } from "../lib/strings";
  import { recoveryKeyFileName, recoveryKeyTitle } from "../lib/print";
  import { band, digitsLine, digitsOnly, keepBand, keepTitle, keptSomehow, pickGroups, readbackMark, ways } from "../lib/reveal";
  import type { Act, KeepAct } from "../lib/reveal";
  import { digitsLater } from "../lib/rekey";
  import { staysUnlocked } from "../lib/foot";
  import { store } from "../lib/state.svelte";

  interface Props {
    url: string;
    kind: string;
    vaultName: string;
    // recoveryId: this key's ID (FORMAT.md §18.4). It names the saved file
    // and the print, heads the sheet, and is the dialog heading's label so
    // the sheet can be told from another one later.
    recoveryId?: string;
    ondone?: () => void;
    // keep: keep mode, a replacement's new key — the prompt its press
    // answers, which of the new keys this is and how many, and the caption
    // that names the old key it replaces. oncancel is keep mode's Cancel,
    // which cancels the replacement.
    keep?: { promptId: string; index: number; count: number; replaces: string };
    oncancel?: () => void;
  }
  let { url, kind, vaultName, recoveryId = "", ondone, keep, oncancel }: Props = $props();
  let digits = $state("");
  let failed = $state(false);
  // Keep mode: the handle is no longer held (a save answered
  // ceremony.stale_prompt), so the key cannot be kept from here; and the
  // press already answered, so a second one asks nothing of a prompt gone.
  let stale = $state(false);
  let answered = $state(false);
  let savedTo = $state("");
  let printedAt = $state("");
  // Whether *Print…* has been pressed and whether *I have written it down*
  // has: each of them, like a save the core acknowledged, puts the dialog
  // into its done state at once.
  let printed = $state(false);
  let wroteDown = $state(false);
  const handle = $derived(url.slice(url.lastIndexOf("/") + 1));
  const title = $derived(recoveryKeyTitle(recoveryId));

  $effect(() => {
    let cancelled = false;
    fetch(url, { cache: "no-store" })
      .then((r) => (r.ok ? r.text() : Promise.reject(new Error(String(r.status)))))
      .then((t) => {
        if (!cancelled) digits = t.replace(/\D/g, "");
      })
      .catch(() => {
        if (!cancelled) failed = true;
      });
    return () => {
      cancelled = true;
    };
  });

  // The key is eight groups of six digits; it is shown the way it is typed.
  const groups = $derived(digits.match(/.{1,6}/g) ?? []);

  // The readback: closed until asked for; its two groups drawn once, as
  // the dialog opens; the fields this component's state alone.
  let checking = $state(false);
  const picked = pickGroups();
  let typed = $state(["", ""]);
  const marks = $derived(picked.map((g, i) => readbackMark(typed[i], groups[g] ?? "")));

  function finish() {
    typed = ["", ""];
    void Keys.DropRecoveryKey(handle).catch(() => {});
    ondone?.();
  }

  async function save() {
    const p = await Shell.SaveFile(revealCopy.saveTitle, recoveryKeyFileName(recoveryId), "");
    if (!p) return;
    try {
      await Keys.SaveRecoveryKey(handle, p);
      savedTo = p;
    } catch (e) {
      const code = errorOf(e).code;
      // In keep mode the ordinary words ("show it again from the Vault
      // page") are false before the commit: the dialog says keepGone.
      if (keep && code === "ceremony.stale_prompt") stale = true;
      else store.notice(code === "ceremony.stale_prompt" ? revealCopy.notHeld : codeText(code), "error");
    }
  }

  // *Print…* sets the tab's title — which is what Print to PDF offers as
  // the file name — opens the browser's print dialog, and puts the title
  // back when the dialog goes down. Nothing is read of what happened
  // there: pressing it counts as done (APP.md §6).
  async function print() {
    printedAt = new Date().toLocaleString();
    const held = document.title;
    // The sheet carries the time of this print, so it is rendered before
    // the dialog opens over it.
    await tick();
    const restore = () => {
      document.title = held;
    };
    // afterprint, not the return of window.print(): a WebView that keeps
    // the dialog up returns first, and the title must stand until then.
    window.addEventListener("afterprint", restore, { once: true });
    try {
      document.title = title;
      // window.print() holds until the print dialog is dismissed, and
      // afterprint has fired by the time it returns.
      window.print();
    } finally {
      // A print the WebView refuses fires no afterprint at all, and a
      // title left changed would name the wrong thing for the rest of the
      // session.
      if (document.title === title) {
        window.removeEventListener("afterprint", restore);
        restore();
      }
      printed = true;
    }
  }

  const acts: Record<Act, () => void> = {
    print: () => void print(),
    save: () => void save(),
    wrote: () => (wroteDown = true),
    done: finish,
  };
  const row = $derived(ways({ savedTo, printed, wroteDown }));
  const foot = $derived(band({ savedTo, printed, wroteDown }));
  const kept = $derived(keptSomehow({ savedTo, printed, wroteDown }));

  const dialogTitle = $derived(keep ? keepTitle(keep.index, keep.count) : kind === "reveal" ? revealCopy.title : revealCopy.titleNew);
  // The digits' caption: keep mode's says nothing changes until the
  // confirmation; a recovery key added with Use recovery key instead says
  // when it can be shown again, no YubiKey or password being at hand to
  // prove with (lib/rekey.ts digitsLater); any other promises the Vault
  // page's showing.
  const digitsCaption = $derived(
    keep ? digitsLine(digits.length, "keep", keep.count) : digitsLine(digits.length, kind === "enroll" && digitsLater(store.lastAct) ? "later" : ""),
  );

  // Keep mode's band (lib/reveal.ts keepBand). Stay unlocked stands in the
  // idle deadline's last minute, as the status bar's does, since this
  // dialog stands over the status bar: keeping a key by hand is minutes
  // without input, and the press is real input.
  const gone = $derived(failed || stale);
  const stay = $derived(!!store.status && store.unlocked && staysUnlocked(store.status.locksAt, store.status.absoluteAt, store.now));
  const keepFoot = $derived(keep ? keepBand({ savedTo, printed, wroteDown }, { last: keep.index >= keep.count, gone, stay }) : []);

  // The key is kept: the ceremony's prompt answered, the handle dropped
  // (the core drops what it minted at the act's end as well) and the
  // readback's fields cleared. The next key's dialog takes this one's
  // place on the next event.
  function keepNext() {
    if (!keep || answered) return;
    answered = true;
    typed = ["", ""];
    checking = false;
    submitSecret("kept", keep.promptId, "");
    void Keys.DropRecoveryKey(handle).catch(() => {});
  }

  function keepAct(a: KeepAct) {
    if (a === "wrote") wroteDown = true;
    else if (a === "next") keepNext();
    else if (a === "stay") void Vault.Activity().catch((e) => store.notice(codeText(errorOf(e).code), "error"));
    else if (a === "cancel") oncancel?.();
  }

  function onInput(i: number, e: Event) {
    const el = e.currentTarget as HTMLInputElement;
    const v = digitsOnly(el.value);
    typed[i] = v;
    // A paste of "123 456" leaves the field holding what it compares.
    if (el.value !== v) el.value = v;
  }
</script>

<Dialog title={dialogTitle} onclose={() => {}} medium>
  {#snippet heading()}
    {#if recoveryId && !failed}<span class="kid" title={revealCopy.idLabel} aria-label="{revealCopy.idLabel} {recoveryId}">{recoveryId}</span>{/if}
  {/snippet}
  {#if failed && !keep}
    <p>{revealCopy.gone}</p>
    <div class="bar"><svg class="i i-14"><use href="#i-info" /></svg><span>{revealCopy.goneHow}</span></div>
  {:else}
    <!-- Keep mode names the key this one replaces first, so the new sheet
         goes where the old one is. -->
    {#if keep}<p class="t-quiet">{keep.replaces}</p>{/if}
    <!-- Both risks, one sentence each (direction 9): what the key opens,
         and what it cannot open without. A new key in keep mode opens
         nothing until the confirmation, so its first sentence says from
         when it does. -->
    <p>{keep ? revealCopy.keepOpens : revealCopy.leadOpens}</p>
    <p>{revealCopy.leadNothing}</p>
    {#if keep && gone}
      <!-- In place of the digits: nothing has changed, and the way on is
           to cancel and start again. -->
      <div class="bar danger"><svg class="i i-14"><use href="#i-error" /></svg><span>{revealCopy.keepGone}</span></div>
    {:else if !digits}
      <p>{revealCopy.loading}</p>
    {:else}
      <div class="digits">
        {#each groups as g, i (i)}<span>{g}</span>{/each}
      </div>
      <!-- The two ways that leave the machine, with the digits they act on
           and at their own width (ruled 2026-10-18, the user's): *Print…*
           the suggested one until a way is taken; the readback's quiet
           button beside them. -->
      <div class="row ways">
        {#each row as b (b.act)}
          <button type="button" class="btn" class:accent={b.accent} onclick={acts[b.act]}>{#if b.act === "print" && b.accent}<svg class="i i-14" aria-hidden="true"><use href="#i-doc" /></svg>{/if}{b.label}</button>
        {/each}
        {#if !checking}<button type="button" class="btn subtle sm" onclick={() => (checking = true)}>{revealCopy.check}</button>{/if}
      </div>
      <!-- The readback (direction 9; ruled 2026-09-09, nothing asked
           twice): offered by a quiet button, never required, and nothing
           in the band waits on it. -->
      {#if checking}
        <div class="readback">
          {#each picked as g, i (g)}
            <div class="field">
              <div class="field-top"><label for="rb-{g}">{revealCopy.group(g + 1)}</label></div>
              <span class="cellbox">
                <!-- data-secret: the readback is the key's digits, and gets
                     no menu of the page's own (lib/fieldmenu.ts). -->
                <input
                  id="rb-{g}"
                  data-secret=""
                  class="input cell"
                  type="text"
                  inputmode="numeric"
                  autocomplete="off"
                  spellcheck="false"
                  aria-invalid={marks[i] === "bad"}
                  aria-describedby={marks[i] === "bad" ? `rb-${g}-said` : undefined}
                  value={typed[i]}
                  oninput={(e) => onInput(i, e)}
                />
                {#if marks[i] === "tick"}<svg class="ok" viewBox="0 0 10 10" aria-hidden="true"><path d="m2 5.2 2 2 4-4.4" /></svg>{/if}
              </span>
              {#if marks[i] === "bad"}<div id="rb-{g}-said" class="field-error">{revealCopy.compare}</div>{/if}
            </div>
          {/each}
        </div>
        <p class="t-quiet">{revealCopy.onlyACheck}</p>
      {/if}
      <p class="t-quiet">{digitsCaption}</p>
      <p class="t-quiet">{revealCopy.textFile}</p>
      {#if savedTo}
        <div class="bar accent"><svg class="i i-14"><use href="#i-check" /></svg><span>{revealCopy.savedTo(savedTo)}</span></div>
      {/if}
      {#if printed}
        <div class="bar accent"><svg class="i i-14"><use href="#i-check" /></svg><span>{revealCopy.sentToPrint(title)}</span></div>
      {/if}
      {#if wroteDown}
        <div class="bar accent"><svg class="i i-14"><use href="#i-check" /></svg><span>{revealCopy.writtenDown}</span></div>
      {/if}
    {/if}
  {/if}
  {#snippet actions()}
    {#if keep}
      <!-- Keep mode's band: the act, any other way on, the way out
           (lib/reveal.ts keepBand). The act waits for the digits, and is
           pressed once. -->
      {#each keepFoot as b (b.act)}
        <button type="button" class="btn" class:accent={b.accent} class:subtle={b.subtle} disabled={b.act !== "cancel" && b.act !== "stay" && (!digits || answered)} onclick={() => keepAct(b.act)}>{b.label}</button>
      {/each}
    {:else if failed}
      <button type="button" class="btn accent" onclick={finish}>{revealCopy.done}</button>
    {:else}
      <!-- One button, lib/reveal.ts band: *I have written it down* until a
           way is taken, *Done* after. Never greyed by the readback (ruled
           2026-09-09). -->
      <button type="button" class="btn" class:accent={foot.accent} disabled={!digits} onclick={acts[foot.act]}>{foot.label}</button>
    {/if}
  {/snippet}
</Dialog>

{#if digits}
  <!-- The print sheet: the only thing a print shows (app.css @media print).
       Its heading is the same string that names the saved file and titles
       the print (APP.md §6). -->
  <div class="print-sheet" aria-hidden="true">
    <h1>{title}</h1>
    <p>Vault: {vaultName}</p>
    <p>Printed: {printedAt}</p>
    <div class="print-digits">
      {#each groups as g, i (i)}<span>{g}</span>{/each}
    </div>
    <p>This key opens the vault without a YubiKey or a password: anyone who has it can open every archive the vault holds the keys to. Keep it secret, and keep it where you can reach it when you need it. It does not replace the vault file itself: without that file, no key opens the archives.</p>
  </div>
{/if}

<style>
  /* The key ID beside the title, as the heading's label (direction 9):
     the section size in mono at its regular weight, in the second ink, on
     the title's baseline. The groups' checksum catches a mistyped group;
     the ID catches the wrong sheet (FORMAT.md §18.4). */
  .kid { margin-left: var(--s2); font: var(--t-sect); font-family: var(--font-mono); font-weight: 400; letter-spacing: 0.04em; color: var(--ink-2); user-select: text; }
  /* The two readback fields side by side, each a recovery cell's size:
     the digits at the sub size in mono, 40 px high (RecoveryInput). */
  .readback { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: var(--s3); }
  .cellbox { position: relative; display: block; min-width: 0; }
  .cell { width: 100%; height: 40px; font: var(--t-sub); font-family: var(--font-mono); font-weight: 400; letter-spacing: 0; text-align: center; padding: 0 var(--s1); }
  /* A group that matches: the recovery cells' small accent tick in the
     top-right corner (RecoveryInput .ok). */
  .ok { position: absolute; top: 2px; right: 2px; width: 8px; height: 8px; fill: none; stroke: var(--accent); stroke-width: 1.6; stroke-linecap: round; stroke-linejoin: round; pointer-events: none; }
  .field-error { margin-top: 0; }
  /* The ways wrap on a narrow dialog rather than spill. */
  .ways { flex-wrap: wrap; }
</style>

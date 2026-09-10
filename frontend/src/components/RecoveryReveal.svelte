<script lang="ts">
  // The recovery key's dialog (APP.md §3 Keys, §6): fetched once from the
  // one-time URL the core minted, shown in groups, and left by one of
  // three ways — saved to a text file the core writes, printed, or written
  // down. The URL's token is the handle a save uses; the digits never ride
  // a bound call.
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
  // *Print…* counts as done, and so does *I have written it down*. After
  // any of the three the corner button is *Done*, and one click closes.
  import { tick } from "svelte";
  import Dialog from "./Dialog.svelte";
  import { Keys, Shell, errorOf } from "../lib/api";
  import { codeText } from "../lib/strings";
  import { recoveryKeyFileName, recoveryKeyTitle } from "../lib/print";
  import { cornerLabel, keptSomehow } from "../lib/reveal";
  import { store } from "../lib/state.svelte";

  interface Props {
    url: string;
    kind: string;
    vaultName: string;
    // recoveryId: this key's ID (FORMAT.md §18.4). It names the saved file
    // and the print, heads the sheet, and is shown here so the sheet can
    // be told from another one later.
    recoveryId?: string;
    ondone: () => void;
  }
  let { url, kind, vaultName, recoveryId = "", ondone }: Props = $props();
  let digits = $state("");
  let failed = $state(false);
  // The one dialog that still stands over this one: where to keep the
  // text file. Neither the print nor the written-down way asks anything.
  let ask = $state<"" | "save">("");
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

  function finish() {
    ask = "";
    void Keys.DropRecoveryKey(handle).catch(() => {});
    ondone();
  }

  async function save() {
    ask = "";
    const p = await Shell.SaveFile("Save the recovery key", recoveryKeyFileName(recoveryId), "");
    if (!p) return;
    try {
      await Keys.SaveRecoveryKey(handle, p);
      savedTo = p;
    } catch (e) {
      const code = errorOf(e).code;
      store.toast(code === "ceremony.stale_prompt" ? "The key is no longer held. Show it again from the Keys page." : codeText(code), "error");
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

  // Once the key has been saved, printed or written down, the dialog is in
  // its done state: the ways out stay, and *Done* is the accented one
  // (lib/reveal.ts).
  const kept = $derived(keptSomehow({ savedTo, printed, wroteDown }));
  const corner = $derived(cornerLabel({ savedTo, printed, wroteDown }));
</script>

<Dialog title={kind === "reveal" ? "Recovery key" : "Your recovery key"} onclose={() => {}} covered={ask !== ""}>
  {#if failed}
    <p>The key could not be shown here: its one-time link was already used.</p>
    <div class="bar attention"><svg class="i i-14"><use href="#i-info" /></svg><span>Show it again from the Keys page: unlock, then prove a YubiKey or a password once more.</span></div>
  {:else}
    <p>These digits open the vault without a YubiKey or a password. Keep them somewhere safe: anyone who has them can open every archive.</p>
    {#if !digits}
      <p>Loading…</p>
    {:else}
      {#if recoveryId}<div class="key-id"><span class="kid-label">Key ID</span><span class="kid">{recoveryId}</span></div>{/if}
      <div class="digits">
        {#each groups as g, i (i)}<span>{g}</span>{/each}
      </div>
      <p class="t-quiet">{digits.length} digits{recoveryId ? `, key ID ${recoveryId}` : ""}. They can be shown again from the Keys page — unlock, then prove a YubiKey or a password once more.</p>
      {#if savedTo}
        <div class="bar accent"><svg class="i i-14"><use href="#i-check" /></svg><span>Saved to {savedTo}.</span></div>
      {/if}
      {#if printed}
        <div class="bar accent"><svg class="i i-14"><use href="#i-check" /></svg><span>Sent to print as “{title}”.</span></div>
      {/if}
      {#if wroteDown}
        <div class="bar accent"><svg class="i i-14"><use href="#i-check" /></svg><span>Written down.</span></div>
      {/if}
    {/if}
  {/if}
  {#snippet actions()}
    {#if failed}
      <button type="button" class="btn accent" onclick={finish}>Done</button>
    {:else}
      <button type="button" class="btn" disabled={!digits} onclick={() => (ask = "save")}>{savedTo ? "Save again…" : "Save as a text file…"}</button>
      <button type="button" class="btn" disabled={!digits} onclick={print}>{printed ? "Print again…" : "Print…"}</button>
      <button type="button" class="btn accent" disabled={!digits} onclick={kept ? finish : () => (wroteDown = true)}>{corner}</button>
    {/if}
  {/snippet}
</Dialog>

{#if ask === "save"}
  <Dialog title="Where to keep it" onclose={() => (ask = "")}>
    <p>Choose a place that is safe and secret, and that you can reach when you need it — a drive you keep offline, a password manager's file store. Not Enfold's own folder or the vault's, and not a folder that syncs to somewhere you would not want this to go.</p>
    {#snippet actions()}
      <button type="button" class="btn" onclick={() => (ask = "")}>Go back</button>
      <button type="button" class="btn accent" onclick={save}>Choose a place…</button>
    {/snippet}
  </Dialog>
{/if}

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

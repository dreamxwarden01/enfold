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
  // A print is no longer guessed at: the shell watches the print spooler
  // around window.print() (Shell.PrintBegin / PrintEnd). A job the spooler
  // saw is a print, done with no second question; one it did not see is
  // said to have been cancelled, in place, with *Print…* still offered;
  // and only a spooler that cannot be read at all falls back to the
  // second confirmation. *I have written it down* asks its own, as it did.
  import { tick } from "svelte";
  import Dialog from "./Dialog.svelte";
  import { Keys, Shell, errorOf } from "../lib/api";
  import { codeText } from "../lib/strings";
  import { askSpooler, recoveryKeyFileName, recoveryKeyTitle, type PrintOutcome } from "../lib/print";
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
  let ask = $state<"" | "save" | "print" | "written">("");
  let savedTo = $state("");
  let printedAt = $state("");
  // What the spooler said about the last print, and whether any print was
  // ever submitted: a submitted one puts the dialog into its done state as
  // a save does; a cancelled one is one line, with *Print…* still offered.
  // Only the last outcome is shown, so the two lines never stand together.
  let lastPrint = $state<"" | "done" | "cancelled">("");
  let printedOnce = $state(false);
  let printing = $state(false);
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

  // How long afterprint is waited for before the print is given up on. The
  // event is what the poll of the spooler is timed from, and a WebView that
  // never fires it must not leave the dialog stuck.
  const afterPrintWaitMs = 30_000;

  // afterprint, not the return of window.print(): the shell's poll of the
  // spooler starts where the print dialog was dismissed.
  function afterPrint(): Promise<void> {
    return new Promise((resolve) => {
      window.addEventListener("afterprint", () => resolve(), { once: true });
    });
  }

  async function print() {
    if (printing) return;
    printing = true;
    lastPrint = "";
    printedAt = new Date().toLocaleString();
    // The sheet carries the time of this print, and the tab's title is
    // what Print to PDF offers as the file name.
    const held = document.title;
    await tick();
    // A spooler that cannot even be snapshotted is the unreadable one: the
    // print still goes ahead and the second question follows it.
    let watching = true;
    try {
      await Shell.PrintBegin();
    } catch {
      watching = false;
    }
    // Whatever happens from here, the tab's title goes back and *Print…*
    // is offered again: a print the WebView refuses, or a window closed
    // over the preview, never fires afterprint, and a dialog left with
    // *Print…* disabled for good would be the one thing §6 does not allow.
    let outcome: PrintOutcome = "ask";
    try {
      document.title = title;
      const done = afterPrint();
      window.print();
      await Promise.race([done, new Promise<void>((r) => setTimeout(r, afterPrintWaitMs))]);
      if (watching) outcome = await askSpooler(() => Shell.PrintEnd());
    } finally {
      document.title = held;
      printing = false;
    }
    switch (outcome) {
      case "done":
        lastPrint = "done";
        printedOnce = true;
        break;
      case "cancelled":
        lastPrint = "cancelled";
        break;
      default:
        ask = "print";
    }
  }

  // Once the key has been saved or printed, the dialog is in its done
  // state: the ways out stay, and *Done* is the accented one.
  const kept = $derived(!!savedTo || printedOnce);
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
      {#if lastPrint === "done"}
        <div class="bar accent"><svg class="i i-14"><use href="#i-check" /></svg><span>Sent to the printer as “{title}”.</span></div>
      {:else if lastPrint === "cancelled"}
        <div class="bar attention"><svg class="i i-14"><use href="#i-info" /></svg><span>The print was cancelled.</span></div>
      {/if}
    {/if}
  {/if}
  {#snippet actions()}
    {#if failed}
      <button type="button" class="btn accent" onclick={finish}>Done</button>
    {:else if kept}
      <button type="button" class="btn" onclick={() => (ask = "save")}>{savedTo ? "Save again…" : "Save as a text file…"}</button>
      <button type="button" class="btn" disabled={printing} onclick={print}>{printedOnce ? "Print again…" : "Print…"}</button>
      <button type="button" class="btn accent" onclick={finish}>Done</button>
    {:else}
      <button type="button" class="btn" disabled={!digits} onclick={() => (ask = "save")}>Save as a text file…</button>
      <button type="button" class="btn" disabled={!digits || printing} onclick={print}>Print…</button>
      <button type="button" class="btn accent" disabled={!digits} onclick={() => (ask = "written")}>I have written it down</button>
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
{:else if ask === "print"}
  <!-- Only when the spooler could not be read at all (APP.md §3 Keys). -->
  <Dialog title="Did it print?" onclose={() => (ask = "")}>
    <p>Enfold could not read the print queue, so it cannot tell whether the page printed. Did it, with all 48 digits legible?</p>
    {#snippet actions()}
      <button type="button" class="btn" onclick={() => (ask = "")}>Go back</button>
      <button type="button" class="btn accent" onclick={finish}>Yes, I have the page</button>
    {/snippet}
  </Dialog>
{:else if ask === "written"}
  <Dialog title="Written down?" onclose={() => (ask = "")}>
    <p>Have you written down all 48 digits, and checked them against the screen? This key is the way in when nothing else works.</p>
    {#snippet actions()}
      <button type="button" class="btn" onclick={() => (ask = "")}>Go back</button>
      <button type="button" class="btn accent" onclick={finish}>Yes, I have it</button>
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

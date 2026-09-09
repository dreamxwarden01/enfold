<script lang="ts">
  // The recovery key's dialog (APP.md §3 Keys, §6): fetched once from the
  // one-time URL the core minted, shown in groups, and left by one of three
  // ways — saved to a text file the core writes, printed, or written down —
  // the last two confirmed a second time, since the page cannot tell a
  // print from a cancelled one and the first click is a reflex. The URL's
  // token is the handle a save uses; the digits never ride a bound call.
  import { tick } from "svelte";
  import Dialog from "./Dialog.svelte";
  import { Keys, Shell, errorOf } from "../lib/api";
  import { codeText } from "../lib/strings";
  import { store } from "../lib/state.svelte";

  interface Props {
    url: string;
    kind: string;
    vaultName: string;
    // recoveryId: this key's ID (FORMAT.md §18.4), shown here and printed
    // on the sheet so the sheet can be told from another one later. The
    // core derives it once and the saved text file carries the same line.
    recoveryId?: string;
    ondone: () => void;
  }
  let { url, kind, vaultName, recoveryId = "", ondone }: Props = $props();
  let digits = $state("");
  let failed = $state(false);
  let ask = $state<"" | "save" | "print" | "written">("");
  let savedTo = $state("");
  let printedAt = $state("");
  const handle = $derived(url.slice(url.lastIndexOf("/") + 1));

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
    const name = (vaultName || "vault").replace(/[\\/:*?"<>|]+/g, "-");
    const p = await Shell.SaveFile("Save the recovery key", `enfold-recovery-key-${name}.txt`, "");
    if (!p) return;
    try {
      await Keys.SaveRecoveryKey(handle, p);
      savedTo = p;
    } catch (e) {
      const code = errorOf(e).code;
      store.toast(code === "ceremony.stale_prompt" ? "The key is no longer held. Show it again from the Keys page." : codeText(code), "error");
    }
  }

  async function print() {
    printedAt = new Date().toLocaleString();
    await tick(); // the sheet carries the time of this print
    window.print();
    ask = "print";
  }
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
    {/if}
  {/if}
  {#snippet actions()}
    {#if failed}
      <button type="button" class="btn accent" onclick={finish}>Done</button>
    {:else if savedTo}
      <button type="button" class="btn" onclick={() => (ask = "save")}>Save again…</button>
      <button type="button" class="btn" onclick={print}>Print…</button>
      <button type="button" class="btn accent" onclick={finish}>Done</button>
    {:else}
      <button type="button" class="btn" disabled={!digits} onclick={() => (ask = "save")}>Save as a text file…</button>
      <button type="button" class="btn" disabled={!digits} onclick={print}>Print…</button>
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
  <Dialog title="Did it print?" onclose={() => (ask = "")}>
    <p>Enfold cannot tell whether the page printed. Did it, with all 48 digits legible?</p>
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
  <!-- The print sheet: the only thing a print shows (app.css @media print). -->
  <div class="print-sheet" aria-hidden="true">
    <h1>Enfold recovery key</h1>
    <p>Vault: {vaultName}</p>
    {#if recoveryId}<p class="key-id">Key ID: <span class="kid">{recoveryId}</span></p>{/if}
    <p>Printed: {printedAt}</p>
    <div class="print-digits">
      {#each groups as g, i (i)}<span>{g}</span>{/each}
    </div>
    <p>This key opens the vault without a YubiKey or a password: anyone who has it can open every archive the vault holds the keys to. Keep it secret, and keep it where you can reach it when you need it. It does not replace the vault file itself: without that file, no key opens the archives.</p>
  </div>
{/if}

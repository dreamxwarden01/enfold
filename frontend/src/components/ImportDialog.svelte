<script lang="ts">
  // Importing a vault or a backup (APP.md §2.1, §6): pick the file, see
  // what it is and what will happen, confirm a replacement by name, then
  // the ceremony proves the file on the lock screen. The secret itself —
  // the recovery key, a password, a PIN — is never a field here.
  import { Shell, Vault, VaultState, errorOf } from "../lib/api";
  import type { FileInfo } from "../lib/api";
  import { store } from "../lib/state.svelte";
  import { codeText } from "../lib/strings";
  import { dateTime, leaf } from "../lib/format";
  import Dialog from "./Dialog.svelte";
  import FirstWayIn from "./FirstWayIn.svelte";
  import TextField from "./TextField.svelte";

  interface Props {
    onclose: () => void;
    prefill?: string;
  }
  let { onclose, prefill = "" }: Props = $props();

  const st = $derived(store.status);
  const kept = $derived(!!st && st.state !== VaultState.StateNone);

  let path = $state("");
  let info = $state<FileInfo | null>(null);
  let inspecting = $state(false);
  let problem = $state("");
  let name = $state("");
  let method = $state<"token" | "password" | "recovery">("password");
  let kind = $state<"token" | "password">("token");
  let label = $state("");
  let entangle = $state(false);
  let confirm = $state(false);
  let nameValid = $state(true);
  let attempt = $state(0);
  // copy: the file is copied in and proved; inplace: it becomes the vault
  // where it is (advanced), copying and retiring nothing.
  let mode = $state<"copy" | "inplace">("copy");

  const isBackup = $derived(info?.kind === "backup");
  // A backup is always copied in: in-place is a vault's choice only.
  const eff = $derived(isBackup ? "copy" : mode);
  const ways = $derived.by(() => {
    const out: Array<{ v: "token" | "password" | "recovery"; t: string }> = [];
    if (info?.hardware) out.push({ v: "token", t: "YubiKey (PIN + touch)" });
    if (info?.password) out.push({ v: "password", t: "Password" });
    out.push({ v: "recovery", t: "Recovery key" });
    return out;
  });

  async function pick() {
    const p = (await Shell.PickFiles("Import a vault or a backup", false)) ?? [];
    if (p.length) await inspect(p[0]);
  }

  async function inspect(p: string) {
    path = p;
    info = null;
    problem = "";
    confirm = false;
    mode = "copy";
    inspecting = true;
    try {
      const i = await Vault.InspectFile(p);
      info = i;
      name = i.vaultMatches && st?.displayName ? st.displayName : leaf(p).replace(/\.eks$/i, "");
      method = i.hardware ? "token" : i.password ? "password" : "recovery";
    } catch (e) {
      problem = codeText(errorOf(e).code);
    } finally {
      inspecting = false;
    }
  }

  const ready = $derived(!!info && (!kept || confirm));
  const openArchives = $derived(st?.openArchives ?? 0);

  async function doImport() {
    attempt++;
    if (!info || !ready || !nameValid) return;
    onclose();
    store.dismissCeremony();
    try {
      await Vault.ImportFile(path, name, method, kind, kind === "token" ? label || "YubiKey" : "Password", kind === "token" && entangle, kept && confirm);
    } catch (e) {
      store.toast(codeText(errorOf(e).code), "error");
    }
  }

  // The advanced choice: the file stays where it is and becomes the
  // vault by reference; nothing is copied and nothing is retired.
  async function useInPlace() {
    attempt++;
    if (!info || isBackup || !ready || !nameValid) return;
    onclose();
    store.dismissCeremony();
    try {
      await Vault.OpenVaultFile(path, name);
    } catch (e) {
      store.toast(codeText(errorOf(e).code), "error");
    }
  }

  $effect(() => {
    if (prefill) void inspect(prefill);
  });
</script>

<Dialog title="Import a vault or a backup" {onclose}>
  <div class="field">
    <div class="field-top"><label for="im-path">File</label></div>
    <div class="row">
      <input id="im-path" class="input grow" readonly value={path} placeholder="A vault.eks, or a backup exported by Enfold" />
      <button type="button" class="btn" onclick={pick}>Choose…</button>
    </div>
  </div>
  {#if inspecting}
    <p>Reading the file…</p>
  {:else if problem}
    <div class="bar danger"><svg class="i i-14"><use href="#i-warn" /></svg><span>{problem}</span></div>
  {:else if info}
    <dl class="facts plain">
      <div class="fact"><dt>What it is</dt><dd>{isBackup ? "A backup — opens with its recovery key only" : `A vault — ${info.hardware} YubiKey, ${info.password} password, ${info.recovery} recovery key`}</dd></div>
      <div class="fact"><dt>Dated</dt><dd>{dateTime(info.modifiedAt)}</dd></div>
      {#if kept}
        <div class="fact"><dt>This vault</dt><dd>{info.vaultMatches ? (info.newer ? "yes — newer than the one kept here" : "yes — not newer than the one kept here") : "no — another vault"}</dd></div>
      {/if}
    </dl>
    <TextField id="im-name" label="Name" bind:value={name} bind:valid={nameValid} {attempt} />
    {#if isBackup}
      <p>It is copied into Enfold's folder and opened with its recovery key; you then choose the first way in, as when creating a vault. Archives added after this backup was made are not in it.</p>
      <FirstWayIn bind:kind bind:label bind:entangle idPrefix="im" />
    {:else}
      <p>It is copied into Enfold's folder and must unlock with one of its own ways in first — that is what makes it yours, not what the file says about itself. Then unlock it as usual.</p>
      <div class="field">
        <div class="field-top"><label for="im-method">Prove it with</label></div>
        <select id="im-method" class="input" bind:value={method}>
          {#each ways as w (w.v)}<option value={w.v}>{w.t}</option>{/each}
        </select>
      </div>
    {/if}
    {#if openArchives > 0}
      <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span>{openArchives} archive{openArchives === 1 ? " is" : "s are"} still open. Close them first: their saves would land in the wrong vault.</span></div>
    {:else if kept && st}
      <div class="bar attention">
        <svg class="i i-14"><use href="#i-warn" /></svg>
        <span>{eff === "inplace" ? "Enfold switches to this file" : "Importing replaces the vault kept here"} — <strong>{st.displayName}</strong>, changed {dateTime(st.modifiedAt)}. {eff === "inplace" || st.keptElsewhere ? `It stays where it is${st.keptElsewhere ? ", at " + st.path : ""}, untouched.` : "It is kept beside the new one as a dated copy, never deleted."} Its ways in stay valid for it; the archives it holds the keys to open only with it.</span>
      </div>
      <label class="check"><input type="checkbox" bind:checked={confirm} />{eff === "inplace" ? "Switch to this file" : "Replace the vault kept here"} ({st.displayName})</label>
    {/if}
    {#if !isBackup}
      <p class="quiet">Advanced: {#if eff === "copy"}<button type="button" class="btn link" onclick={() => (mode = "inplace")}>use the file where it is</button> instead of copying it. Only on a BitLocker-protected fixed drive; removable media and network shares are outside Enfold's protection.{:else}the file stays where it is and becomes the vault by reference; nothing is copied, nothing is proved first. <button type="button" class="btn link" onclick={() => (mode = "copy")}>Copy it in instead</button>.{/if}</p>
    {/if}
  {:else}
    <p>Have the recovery key ready to import a backup: a backup opens with nothing else. A full vault opens with any of its ways in.</p>
  {/if}
  {#snippet actions()}
    <button type="button" class="btn" onclick={onclose}>Cancel</button>
    {#if eff === "inplace"}
      <button type="button" class="btn accent" disabled={!ready || openArchives > 0} onclick={useInPlace}>Use it here</button>
    {:else}
      <button type="button" class="btn accent" disabled={!ready || openArchives > 0} onclick={doImport}>Import</button>
    {/if}
  {/snippet}
</Dialog>

<style>
  .facts.plain { padding: 0; }
  .quiet { color: var(--ink-3); font-size: 12px; }
</style>

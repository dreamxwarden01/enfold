<script lang="ts">
  // Replacing the vault with a copy, or restoring one at first run (APP.md
  // §2.1, §6; the two titles ruled 2026-10-17): pick the file, see what it
  // is and what will happen, confirm a replacement by name, then the
  // ceremony proves the file on the lock screen. The secret that proves it
  // — the recovery key, a password, a PIN — is never a field here; a
  // backup's new password is (FirstWayIn), typed twice and sent by the
  // page at the ceremony's Choose prompt, after the recovery key (APP.md
  // §6, ruled 2026-10-17) — never an argument of the bound call.
  import { Shell, Vault, VaultState, errorOf } from "../lib/api";
  import type { FileInfo } from "../lib/api";
  import { store } from "../lib/state.svelte";
  import { codeText, dialogCopy, firstMethodCopy, importCopy, vaultCopy } from "../lib/strings";
  import { dateTime, leaf, plural } from "../lib/format";
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
  // With a vault kept here the file replaces it; at first run it is
  // restored, there being nothing to replace.
  const title = $derived(kept ? importCopy.replaceTitle : importCopy.restoreTitle);

  let path = $state("");
  let info = $state<FileInfo | null>(null);
  let inspecting = $state(false);
  let problem = $state("");
  let name = $state("");
  let method = $state<"token" | "password" | "recovery">("password");
  // A backup's first unlock method (FirstWayIn): the cards hold it, the
  // password among it, until the import's button takes it.
  let first = $state<ReturnType<typeof FirstWayIn>>();
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
    if (info?.hardware) out.push({ v: "token", t: dialogCopy.addUnlockMethod.token });
    if (info?.password) out.push({ v: "password", t: dialogCopy.addUnlockMethod.password });
    out.push({ v: "recovery", t: "Recovery key" });
    return out;
  });
  // A vault's unlock methods by kind, counted ("1 YubiKey, 1 recovery key").
  const methods = $derived(
    info
      ? ([[info.hardware, "YubiKey"], [info.password, "password"], [info.recovery, "recovery key"]] as [number, string][])
          .filter(([n]) => n > 0)
          .map(([n, w]) => plural(n, w))
          .join(", ")
      : "",
  );

  async function pick() {
    const p = (await Shell.PickFiles(title, false)) ?? [];
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
    // A backup takes its first unlock method from the cards, which judge
    // their own parts under the same press and answer null while any
    // refuses (lib/firstwayin); a vault proves itself with its own and
    // takes none — the core reads kind, label and tick for a backup alone.
    const way = isBackup ? first?.take() : { kind: "token" as const, label: "", entangle: false, password: "" };
    if (!way) return;
    onclose();
    store.dismissCeremony();
    // Held before the call, for the Choose prompt the adopted backup asks
    // after its recovery key (APP.md §6, ruled 2026-10-17).
    store.holdChosen("import", way.password);
    try {
      await Vault.ImportFile(path, name, method, way.kind, way.label, way.entangle, kept && confirm);
    } catch (e) {
      store.dropChosen();
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

<!-- A form (APP.md §6, Dialogs stay put; ruled 2026-10-17): Enter in the
     name or a backup's password presses the primary shown — Use this file
     in place, else Replace or Restore vault — under the form rule; a
     greyed primary leaves Enter doing nothing, as the browser's implicit
     submission does. The read-only path is no field to type in, so the
     focus opens on the box until a file is read. Anchored at the top: a
     file read, and a card opened, make it taller, and the title must not
     climb. -->
<Dialog {title} {onclose} onsubmit={() => void (eff === "inplace" ? useInPlace() : doImport())} anchored>
  <div class="field">
    <div class="field-top"><label for="im-path">{vaultCopy.file}</label></div>
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
    <dl class="kv">
      <dt>{vaultCopy.inspected.kind}</dt><dd>{isBackup ? vaultCopy.inspected.backup : vaultCopy.inspected.vault}</dd>
      {#if !isBackup}<dt>{importCopy.methods}</dt><dd>{methods}</dd>{/if}
      <dt>{vaultCopy.inspected.dated}</dt><dd>{dateTime(info.modifiedAt)}</dd>
      {#if kept}
        <dt>{vaultCopy.inspected.thisVault}</dt><dd>{info.vaultMatches ? (info.newer ? "Yes, and newer than this one" : "Yes, and not newer than this one") : vaultCopy.inspected.no}</dd>
      {/if}
    </dl>
    <TextField id="im-name" label={vaultCopy.name} bind:value={name} bind:valid={nameValid} {attempt} />
    {#if isBackup}
      <p>{kept ? importCopy.backupReplaces : importCopy.backupRestores}</p>
      <FirstWayIn bind:this={first} heading={firstMethodCopy.headImport} context="adopt" {attempt} idPrefix="im" />
    {:else}
      <p>{kept ? importCopy.vaultReplaces : importCopy.vaultRestores}</p>
      <div class="field">
        <div class="field-top"><label for="im-method">{importCopy.proveWith}</label></div>
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
        <span>{eff === "inplace" ? "Enfold switches to this file from" : "This replaces"} <strong>{st.displayName}</strong>, changed {dateTime(st.modifiedAt)}. {eff === "inplace" || st.keptElsewhere ? `It stays where it is${st.keptElsewhere ? ", at " + st.path : ""}, untouched` : "It is kept as a dated copy in Enfold's folder, never deleted"}: its own unlock methods still open it, and archives only it holds open only with it.</span>
      </div>
      <label class="check"><input type="checkbox" bind:checked={confirm} />{eff === "inplace" ? "Switch to this file" : "Replace this vault"} ({st.displayName})</label>
    {/if}
    {#if !isBackup}
      <p class="t-quiet">Advanced: {#if eff === "copy"}<button type="button" class="btn link" onclick={() => (mode = "inplace")}>use the file where it is</button> instead of copying it. Only on a BitLocker-protected fixed drive; removable media and network shares are outside Enfold's protection.{:else}the file stays where it is and becomes the vault by reference; nothing is copied, nothing is proved first. <button type="button" class="btn link" onclick={() => (mode = "copy")}>Copy it in instead</button>.{/if}</p>
    {/if}
  {:else}
    <p>For a backup, have its recovery key ready: a backup opens with nothing else. A full vault opens with any of its unlock methods.</p>
  {/if}
  {#snippet actions()}
    {#if eff === "inplace"}
      <button type="submit" class="btn accent" disabled={!ready || openArchives > 0}>Use this file</button>
    {:else}
      <button type="submit" class="btn accent" disabled={!ready || openArchives > 0}>{kept ? "Replace vault" : "Restore vault"}</button>
    {/if}
    <button type="button" class="btn" onclick={onclose}>{dialogCopy.cancel}</button>
  {/snippet}
</Dialog>

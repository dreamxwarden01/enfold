<script lang="ts">
  import { CeremonyStep, Keys, Shell, Vault, errorOf } from "../lib/api";
  import type { FileInfo } from "../lib/api";
  import { store } from "../lib/state.svelte";
  import { codeText, warningCopy } from "../lib/strings";
  import { countdown, date, dateTime, leaf } from "../lib/format";
  import Dialog from "./Dialog.svelte";
  import CeremonyPanel from "./CeremonyPanel.svelte";

  const st = $derived(store.status);
  const unlocked = $derived(store.unlocked);
  const tampered = $derived(st?.tampered ?? false);
  const c = $derived(store.ceremony && !["unlock", "create", "import", "setup", "verify"].includes(store.ceremony.kind) ? store.ceremony : null);

  let selected = $state<string | null>(null);
  const sel = $derived(store.slots.find((s) => s.recipientId === selected) ?? null);

  let adding = $state(false);
  let addKind = $state<"token" | "password" | "recovery">("token");
  let addLabel = $state("");
  let addEntangle = $state(false);
  let removing = $state(false);
  let rotating = $state(false);
  let backup = $state<FileInfo | null>(null);

  function fail(e: unknown) {
    store.toast(codeText(errorOf(e).code), "error");
  }

  async function begin(p: Promise<unknown>) {
    store.dismissCeremony();
    try {
      await p;
    } catch (e) {
      fail(e);
    }
  }

  function slotIcon(t: string): string {
    return t === "hardware" ? "i-yubi" : t === "recovery" ? "i-recovery" : "i-password";
  }

  function slotMeta(s: { type: string; entangled: boolean; createdAt: number }): string {
    const kind = s.type === "hardware" ? (s.entangled ? "hardware key + password · PIN + touch, then a password" : "hardware key · PIN + touch") : s.type === "recovery" ? "recovery key" : "password";
    return `${kind} · added ${date(s.createdAt)}`;
  }

  async function exportBackup() {
    const p = await Shell.SaveFile("Export a backup of the vault", `backup-${new Date().toISOString().slice(0, 10)}.eks`);
    if (p) await begin(Keys.ExportBackup(p));
  }

  async function checkBackup() {
    const p = (await Shell.PickFiles("Check a backup", false)) ?? [];
    if (!p.length) return;
    try {
      backup = await Vault.InspectFile(p[0]);
    } catch (e) {
      fail(e);
    }
  }
</script>

<div class="layer-head"><h1 class="t-title">Keystore</h1>{#if !unlocked}<span class="chip warn"><svg class="i i-14"><use href="#i-lock" /></svg>Locked</span>{/if}</div>

<div class="layer-body">
  {#if tampered}
    <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span>{warningCopy("vault.tampered")}</span></div>
  {/if}
  {#if !unlocked}
    <div class="bar"><svg class="i i-14"><use href="#i-lock" /></svg><span class="grow">Changing the ways in needs the vault unlocked.</span><button type="button" class="btn sm accent" onclick={() => store.go("lock")}>Unlock</button></div>
  {:else if st?.setupNeeded}
    <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span class="grow">This vault has only its recovery key. Add a key — a YubiKey or a password — to finish setting it up; the recovery key is asked for first.</span></div>
  {/if}

  <div class="ks-cols">
    <div class="ks-col">
      <div class="ks-head"><h2 class="t-section">Ways to unlock</h2><span class="t-quiet">{store.slots.length} slot{store.slots.length === 1 ? "" : "s"}</span></div>
      {#each store.slots as s (s.recipientId)}
        <button type="button" class="slot" aria-pressed={selected === s.recipientId} onclick={() => (selected = s.recipientId)}>
          <div class="tile"><svg class="i"><use href="#{slotIcon(s.type)}" /></svg></div>
          <div class="body">
            <b>{s.label}</b>
            <span class="meta">{slotMeta(s)}</span>
            {#if s.stale}<div class="warnline"><svg class="i i-14"><use href="#i-warn" /></svg><span>Holds the previous key — rotation deferred until it is rewrapped</span></div>{/if}
          </div>
        </button>
      {/each}
      <div class="rule"><svg class="i i-14"><use href="#i-info" /></svg>At least two independent ways in are always kept; a recovery key is one of them.</div>
      <div class="ks-actions">
        <button type="button" class="btn accent" disabled={!unlocked || tampered} onclick={() => (adding = true)}><svg class="i i-14"><use href="#i-plus" /></svg>Add a key</button>
        <button type="button" class="btn" disabled={!sel || !unlocked || tampered} onclick={() => (removing = true)}>Remove</button>
        <button type="button" class="btn" disabled={!unlocked || tampered} onclick={() => (rotating = true)}><svg class="i i-14"><use href="#i-rotate" /></svg>Rotate now</button>
      </div>

      <div class="ks-head top"><h2 class="t-section">Backups</h2></div>
      <div class="card backup">
        <p class="t-sub">A backup is the vault file without the archive keys' history it does not need: the registry and the recovery slots. It carries its own date, so Enfold can tell an old one from a new one.</p>
        <div class="ks-actions">
          <button type="button" class="btn" disabled={!unlocked || tampered} onclick={exportBackup}><svg class="i i-14"><use href="#i-backup" /></svg>Export backup</button>
          <button type="button" class="btn" onclick={checkBackup}>Check a backup…</button>
        </div>
        {#if backup}
          <dl class="facts">
            <div class="fact"><dt>File</dt><dd title={backup.path}>{leaf(backup.path)}</dd></div>
            <div class="fact"><dt>What it is</dt><dd>{backup.kind === "backup" ? "a backup — opens with the recovery key only" : "a full vault"}</dd></div>
            <div class="fact"><dt>Dated</dt><dd>{dateTime(backup.modifiedAt)}</dd></div>
            <div class="fact"><dt>This vault</dt><dd>{backup.vaultMatches ? "yes" : "no — another vault"}</dd></div>
            <div class="fact"><dt>Slots</dt><dd>{backup.slotCount}</dd></div>
          </dl>
          {#if backup.vaultMatches && !backup.newer && st}
            <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span>Older than this vault (changed {dateTime(st.modifiedAt)}).</span></div>
          {:else if backup.vaultMatches && backup.newer}
            <div class="bar accent"><svg class="i i-14"><use href="#i-info" /></svg><span>Newer than the vault kept here. Lock, then import it from the lock screen.</span></div>
          {/if}
          <p class="t-quiet">To prove it opens, lock the vault and use "Check that a backup opens…" on the lock screen: a copy is opened with the recovery key.</p>
        {/if}
      </div>
    </div>

    <div class="ks-col">
      <div class="ks-head"><h2 class="t-section">This vault</h2></div>
      <div class="card setcard">
        <div class="setrow"><div class="lab"><b>Name</b><span>{st?.displayName}</span></div></div>
        <div class="setrow"><div class="lab"><b>File</b><span class="ellipsis" title={st?.path}>{st?.path}</span></div></div>
        <div class="setrow"><div class="lab"><b>Last changed</b><span>{dateTime(st?.modifiedAt ?? 0)}</span></div></div>
        <div class="setrow"><div class="lab"><b>Rotation</b><span>{st?.rotationPending ? "deferred — a way in still holds the old key" : "complete"}</span></div></div>
        {#if (st?.retiredCopies ?? 0) > 0 && st?.retiredPath}
          <div class="setrow"><div class="lab"><b>Replaced copies</b><span>{st.retiredCopies} in Enfold's folder, kept when a vault was replaced; yours to delete.</span></div><div class="ctl"><button type="button" class="btn sm" onclick={() => void Shell.Reveal(st?.retiredPath ?? "")}>Show</button></div></div>
        {/if}
      </div>
    </div>
  </div>
</div>

<div class="layer-foot">
  <span>{st?.displayName} · keystore {unlocked ? "unlocked" : "locked"}</span>
  {#if unlocked && st}<span class="lockchip"><svg class="i i-14"><use href="#i-lock" /></svg>Locks in {countdown(st.locksAt, store.now)}</span>{/if}
</div>

{#if adding}
  <Dialog title="Add a key" onclose={() => (adding = false)}>
    <p>The vault asks for a current way in first, then adds the new one.</p>
    <div class="field">
      <div class="field-top"><label for="ak-kind">Kind</label></div>
      <select id="ak-kind" class="input" bind:value={addKind}>
        <option value="token">YubiKey (PIN + touch)</option>
        <option value="password" disabled={st?.hasHardwareSlot}>Password{st?.hasHardwareSlot ? " — not with a hardware key in the vault" : ""}</option>
        <option value="recovery">Another recovery key</option>
      </select>
    </div>
    {#if addKind === "password"}
      <p>You choose the password once the current way in is checked.</p>
    {:else}
      <div class="field">
        <div class="field-top"><label for="ak-label">{addKind === "token" ? "Name this key" : "Name this recovery key"}</label><span class="hint">Shown in the list of ways in.</span></div>
        <input id="ak-label" class="input" bind:value={addLabel} placeholder={addKind === "token" ? "YubiKey 5 NFC — travel" : "Printed, in the safe"} />
      </div>
    {/if}
    {#if addKind === "token"}
      <label class="check"><input type="checkbox" bind:checked={addEntangle} />Also require a password with this key</label>
      {#if addEntangle}
        <p>You choose that password before the key is set up. Unlocking with this key then needs both.</p>
      {/if}
    {/if}
    {#snippet actions()}
      <button type="button" class="btn" onclick={() => (adding = false)}>Cancel</button>
      <button type="button" class="btn accent" disabled={!addLabel && addKind !== "password"} onclick={() => { adding = false; void begin(Keys.BeginEnroll(addKind, addKind === "password" ? "Password" : addLabel, addKind === "token" && addEntangle)); addLabel = ""; }}>Add</button>
    {/snippet}
  </Dialog>
{/if}

{#if removing && sel}
  <Dialog title="Remove {sel.label}?" onclose={() => (removing = false)}>
    <p>This way in stops working at once. The vault refuses if fewer than two independent ways in would remain.</p>
    {#snippet actions()}
      <button type="button" class="btn" onclick={() => (removing = false)}>Cancel</button>
      <button type="button" class="btn accent" onclick={() => { removing = false; void begin(Keys.RemoveSlot(sel.recipientId)); }}>Remove</button>
    {/snippet}
  </Dialog>
{/if}

{#if rotating}
  <Dialog title="Rotate the vault key?" onclose={() => (rotating = false)}>
    <p>Every way in is rewrapped to a fresh key. A hardware key that is not present is marked and rewrapped the next time it unlocks; until then the rotation shows as deferred.</p>
    {#snippet actions()}
      <button type="button" class="btn" onclick={() => (rotating = false)}>Cancel</button>
      <button type="button" class="btn accent" onclick={() => { rotating = false; void begin(Keys.RotateNow()); }}>Rotate</button>
    {/snippet}
  </Dialog>
{/if}

{#if c && c.step === CeremonyStep.StepRecovery && !c.promptId && c.slotLabel}
  <!-- the recovery key is revealed by App, above every route -->
{:else if c}
  <Dialog title={c.kind === "enroll" ? "Add a key" : c.kind === "remove" ? "Remove a key" : c.kind === "rotate" ? "Rotate the vault key" : "Export a backup"} onclose={() => { if (store.ceremonyIsOver) store.dismissCeremony(); }}>
    <CeremonyPanel {c} onclose={() => { store.dismissCeremony(); void store.refreshSlots(); }} />
  </Dialog>
{/if}

<style>
  .ks-head.top { margin-top: 12px; }
  .backup { padding: 14px 16px; display: flex; flex-direction: column; gap: 10px; }
  .backup p { margin: 0; font-size: 12.5px; }
  .backup .facts { padding: 0; }
</style>

<script lang="ts">
  import { CeremonyStep, Keys, Shell, Vault, errorOf } from "../lib/api";
  import type { FileInfo, SlotView } from "../lib/api";
  import { store } from "../lib/state.svelte";
  import { codeText, warningCopy } from "../lib/strings";
  import { date, dateTime, leaf } from "../lib/format";
  import Dialog from "./Dialog.svelte";
  import CeremonyPanel from "./CeremonyPanel.svelte";
  import EntangledDialog from "./EntangledDialog.svelte";

  const st = $derived(store.status);
  const unlocked = $derived(store.unlocked);
  const tampered = $derived(st?.tampered ?? false);
  // This page's own ceremonies: the ways in, the backups and the vault's
  // password. A `records` ceremony belongs to the Archives page, which
  // renders its own panel — the incoming file's recovery prompt has
  // nowhere to go here (APP.md §13).
  const c = $derived(store.ceremony && !["unlock", "create", "import", "setup", "verify", "records"].includes(store.ceremony.kind) ? store.ceremony : null);

  let selected = $state<string | null>(null);
  const sel = $derived(store.slots.find((s) => s.recipientId === selected) ?? null);

  let adding = $state(false);
  let addKind = $state<"token" | "password" | "recovery">("token");
  let addLabel = $state("");
  let removing = $state(false);
  let rotating = $state(false);
  let backup = $state<FileInfo | null>(null);

  // The vault's entangled password (APP.md §13): one switch for the whole
  // vault, on / change / off, and the old password is never asked for.
  const ent = $derived(store.entangled);
  let entangling = $state<"" | "on" | "change" | "off">("");
  // Why the switch is greyed: turning it on where every active slot is a
  // hardware key would put one password in front of every way in
  // (FORMAT.md §6.4), and the cure is a recovery key.
  const entangleBlocked = $derived(!!ent && !ent.on && !ent.canEnable && unlocked);

  function fail(e: unknown) {
    store.toast(codeText(errorOf(e).code), "error");
  }

  // Every ceremony this page starts can change the ways in or the vault's
  // switch, so both are read again when it ends (APP.md §13).
  async function begin(p: Promise<unknown>) {
    store.dismissCeremony();
    try {
      await p;
    } catch (e) {
      fail(e);
    }
    await store.refreshSlots();
    await store.refreshEntangled();
  }

  function slotIcon(t: string): string {
    return t === "hardware" ? "i-yubi" : t === "recovery" ? "i-recovery" : "i-password";
  }

  // A slot's second line. Since Revision 2 the entangled password is the
  // vault's, not a slot's (FORMAT.md §3.1), so no slot says anything about
  // it; a recovery slot names its ID, so the right sheet can be found
  // (FORMAT.md §18.4).
  function slotMeta(s: SlotView): string {
    const kind = s.type === "hardware" ? "hardware key · PIN + touch" : s.type === "recovery" ? "recovery key" : "password";
    const id = s.type === "recovery" && s.recoveryId ? ` · ${s.recoveryId}` : "";
    return `${kind}${id} · added ${date(s.createdAt)}`;
  }

  // Add a key, preset to a recovery key: the action beside the greyed
  // entangled switch (APP.md §13).
  function addRecoveryKey() {
    addKind = "recovery";
    addLabel = "";
    adding = true;
  }

  async function exportBackup(): Promise<boolean> {
    const p = await Shell.SaveFile("Export a backup of the vault", `backup-${new Date().toISOString().slice(0, 10)}.eks`, "");
    if (!p) return false;
    store.dismissCeremony();
    try {
      await Keys.ExportBackup(p);
    } catch (e) {
      fail(e);
      return false;
    }
    await store.refreshLastExport();
    return true;
  }

  // The rotate dialog asks for a backup first when the last one is over a
  // week old or none is recorded here — it asks, it never refuses, and it
  // never keeps a copy of the vault (APP.md §13, DESIGN trap 29).
  const WEEK = 604800;
  const backupAge = $derived(store.lastExportAt ? Math.floor(store.now / 1000) - store.lastExportAt : -1);
  const backupWanted = $derived(backupAge < 0 || backupAge > WEEK);
  let rotateBackup = $state(false);

  function openRotate() {
    rotateBackup = backupWanted; // pre-selected, and the user may clear it
    rotating = true;
  }

  async function doRotate() {
    rotating = false;
    if (rotateBackup && !(await exportBackup())) return; // no backup, no rotation
    await begin(Keys.RotateNow());
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

  // The foot's note (LayerFoot): the vault and its ways in.
  $effect(() => {
    const n = store.slots.length;
    store.footNote = `${st?.displayName ?? ""} · ${n} way${n === 1 ? "" : "s"} in`;
  });
</script>

<div class="layer-head"><h1 class="t-title">Keystore</h1>{#if !unlocked}<span class="chip warn"><svg class="i i-14"><use href="#i-lock" /></svg>Locked</span>{/if}</div>

<div class="layer-body">
  {#if tampered}
    <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span>{warningCopy("vault.tampered", st?.tamperedReason)}</span></div>
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
          </div>
        </button>
      {/each}
      <div class="rule"><svg class="i i-14"><use href="#i-info" /></svg>At least two independent ways in are always kept; a recovery key is one of them.</div>
      <div class="ks-actions">
        <button type="button" class="btn accent" disabled={!unlocked || tampered} onclick={() => (adding = true)}><svg class="i i-14"><use href="#i-plus" /></svg>Add a key</button>
        <button type="button" class="btn" disabled={!sel || !sel.removable || !unlocked || tampered} title={sel && !sel.removable ? "At least two independent ways in are always kept." : undefined} onclick={() => (removing = true)}>Remove</button>
        {#if sel?.type === "recovery"}
          <button type="button" class="btn" disabled={!unlocked} onclick={() => sel && begin(Keys.RevealRecoveryKey(sel.recipientId))}><svg class="i i-14"><use href="#i-recovery" /></svg>Show recovery key…</button>
        {/if}
        <button type="button" class="btn" disabled={!unlocked || tampered} onclick={openRotate}><svg class="i i-14"><use href="#i-rotate" /></svg>Rotate now</button>
      </div>

      <div class="ks-head top"><h2 class="t-section">Backups</h2></div>
      <div class="card backup">
        <p class="t-sub">A backup is the vault file without the archive keys' history it does not need: the registry and the recovery slots. It carries its own date, so Enfold can tell an old one from a new one.</p>
        <div class="ks-actions">
          <button type="button" class="btn" disabled={!unlocked || tampered} onclick={() => void exportBackup()}><svg class="i i-14"><use href="#i-backup" /></svg>Export backup</button>
          <button type="button" class="btn" onclick={checkBackup}>Check a backup…</button>
        </div>
        <div class="setrow"><div class="lab"><b>Last backup</b><span>{store.lastExportAt ? `${dateTime(store.lastExportAt)} — recorded here, not proof the file is still there` : "none recorded here"}</span></div></div>
        <!-- Import records… lives in the Archives page's command bar; this
             is the pointer to it (APP.md §13). -->
        <p class="t-quiet">To take archive records out of a backup or another copy of this vault without replacing the vault kept here, use <em>Import records…</em> on the Archives page. <button type="button" class="btn link" onclick={() => store.go("archives")}>Go there</button></p>
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
        <!-- One switch for the whole vault (FORMAT.md §3.1, APP.md §13):
             every YubiKey asks for it, the standalone password and the
             recovery key never do, and the old one is never asked for. -->
        <div class="setrow">
          <div class="lab">
            <b>Entangled password</b>
            <span>{ent?.on ? "on — every YubiKey also asks for this password, before its PIN" : "off — a YubiKey opens the vault with its PIN and touch alone"}</span>
          </div>
          <div class="ctl">
            {#if ent?.on}
              <button type="button" class="btn sm" disabled={!unlocked || tampered} onclick={() => (entangling = "change")}>Change…</button>
              <button type="button" class="btn sm" disabled={!unlocked || tampered} onclick={() => (entangling = "off")}>Turn off…</button>
            {:else}
              <button type="button" class="btn sm" disabled={!unlocked || tampered || !ent?.canEnable} title={entangleBlocked ? "Every key would then need this password — add a recovery key first." : undefined} onclick={() => (entangling = "on")}>Turn on…</button>
            {/if}
          </div>
        </div>
        {#if entangleBlocked}
          <div class="bar attention">
            <svg class="i i-14"><use href="#i-warn" /></svg>
            <div class="why">
              <span>{ent?.reason && ent.reason !== "vault.invariant" ? codeText(ent.reason) : "Every way into this vault is a YubiKey, so one password would then stand in front of all of them. Add a recovery key first."}</span>
              <button type="button" class="btn link" onclick={addRecoveryKey}>Add a recovery key…</button>
            </div>
          </div>
        {/if}
        {#if (st?.retiredCopies ?? 0) > 0 && st?.retiredPath}
          <div class="setrow"><div class="lab"><b>Replaced copies</b><span>{st.retiredCopies} in Enfold's folder, kept when a vault was replaced; yours to delete.</span></div><div class="ctl"><button type="button" class="btn sm" onclick={() => void Shell.Reveal(st?.retiredPath ?? "")}>Show</button></div></div>
        {/if}
        {#if st?.damagedCopyPath}
          <div class="setrow"><div class="lab"><b>Damaged copy</b><span>Kept in Enfold's folder when the vault was rebuilt, for whatever can be salvaged from it; yours to delete.</span></div><div class="ctl"><button type="button" class="btn sm" onclick={() => void Shell.Reveal(st?.damagedCopyPath ?? "")}>Show</button></div></div>
        {/if}
      </div>
    </div>
  </div>
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
        <div class="field-top"><label for="ak-label">{addKind === "token" ? "Name this key" : "Name this recovery key"}</label><span class="hint">{addKind === "token" ? "Left empty, its serial number names it." : "Shown in the list of ways in."}</span></div>
        <input id="ak-label" class="input" bind:value={addLabel} placeholder={addKind === "token" ? "YubiKey 5 NFC — travel" : "Printed, in the safe"} />
      </div>
    {/if}
    {#if addKind === "token" && ent?.on}
      <!-- The vault's switch is inherited, never chosen per key: the new
           key is wrapped from the kept K_P offline (APP.md §13). -->
      <p>This vault's entangled password is on, so this key will ask for it too. Nothing is asked for here.</p>
    {/if}
    {#snippet actions()}
      <button type="button" class="btn" onclick={() => (adding = false)}>Cancel</button>
      <button type="button" class="btn accent" disabled={!addLabel && addKind === "recovery"} onclick={() => { adding = false; void begin(Keys.BeginEnroll(addKind, addKind === "password" ? "Password" : addLabel)); addLabel = ""; }}>Add</button>
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
    <p>Every way in is rewrapped to a fresh key here and now — no YubiKey needs to be present, and nothing is deferred. A copy of the vault made before this rotation still opens with the ways in it held, so no copy is kept beside it.</p>
    <div class="setrow"><div class="lab"><b>Last backup</b><span>{store.lastExportAt ? dateTime(store.lastExportAt) : "none recorded here"}</span></div></div>
    {#if backupWanted}
      <p>A backup holds the recovery slots only, so keeping one revokes nothing — and it is what brings the archives' keys back if this vault is lost.</p>
    {/if}
    <label class="check"><input type="checkbox" bind:checked={rotateBackup} />Export a backup first</label>
    {#snippet actions()}
      <button type="button" class="btn" onclick={() => (rotating = false)}>Cancel</button>
      <button type="button" class="btn accent" onclick={doRotate}>{rotateBackup ? "Back up, then rotate" : "Rotate"}</button>
    {/snippet}
  </Dialog>
{/if}

{#if entangling && ent}
  <EntangledDialog mode={entangling} onclose={() => (entangling = "")} />
{/if}

{#if c && c.step === CeremonyStep.StepRecovery && !c.promptId && c.slotLabel}
  <!-- the recovery key is revealed by App, above every route -->
{:else if c}
  <Dialog title={c.kind === "enroll" ? "Add a key" : c.kind === "remove" ? "Remove a key" : c.kind === "rotate" ? "Rotate the vault key" : c.kind === "reveal" ? "Show the recovery key" : c.kind === "entangle" ? "The vault's password" : "Export a backup"} onclose={() => { if (store.ceremonyIsOver) store.dismissCeremony(); }}>
    <CeremonyPanel {c} onclose={() => { store.dismissCeremony(); void store.refreshSlots(); void store.refreshEntangled(); }} />
  </Dialog>
{/if}

<style>
  .ks-head.top { margin-top: 12px; }
  .backup { padding: 14px 16px; display: flex; flex-direction: column; gap: 10px; }
  .backup p { margin: 0; font-size: 12.5px; }
  .backup .facts { padding: 0; }
  /* The invariant's reason and its cure, stacked: this card is the narrow
     column, where a bar's inline action would be a word per line. */
  .why { display: flex; flex-direction: column; align-items: flex-start; gap: 6px; min-width: 0; }
</style>

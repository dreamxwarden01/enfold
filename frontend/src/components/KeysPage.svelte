<script lang="ts">
  import { CeremonyStep, Keys, Shell, Vault, errorOf } from "../lib/api";
  import type { FileInfo, SlotView } from "../lib/api";
  import { store } from "../lib/state.svelte";
  import { chipCopy, codeText, commandCopy, dialogCopy, sinceText, vaultCopy } from "../lib/strings";
  import { backupLine } from "../lib/backup";
  import { date, dateTime, leaf } from "../lib/format";
  import Dialog from "./Dialog.svelte";
  import CeremonyPanel from "./CeremonyPanel.svelte";
  import EntangledDialog from "./EntangledDialog.svelte";
  import TamperedBar from "./TamperedBar.svelte";

  const st = $derived(store.status);
  const unlocked = $derived(store.unlocked);
  // The banner's one predicate (store.tampered; APP.md §6).
  const tampered = $derived(store.tampered);
  // This page's own ceremonies: the unlock methods, the backups and the
  // vault's password. A `records` ceremony belongs to the Archives page, which
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
  // hardware key would put one password in front of every unlock method
  // (FORMAT.md §6.4), and the cure is a recovery key.
  const entangleBlocked = $derived(!!ent && !ent.on && !ent.canEnable && unlocked);
  // Why a change is greyed, as its title: a disabled button still takes
  // the pointer so that this can be read (APP.md §6, Controls). The
  // tampered one points at the banner, whose button is the remedy
  // (direction 9).
  const greyed = $derived(!unlocked ? codeText("vault.needs_unlock") : tampered ? vaultCopy.changesOff : undefined);

  function fail(e: unknown) {
    store.toast(codeText(errorOf(e).code), "error");
  }

  // Every ceremony this page starts can change the unlock methods or the
  // vault's switch, so both are read again when it ends (APP.md §13).
  // `again` keeps the act last begun, with what it was begun with, for
  // the dialog's Start again after a failure the user did not choose
  // (CeremonyPanel onagain; ruled 2026-10-17). Begun again, the failed
  // ceremony is left on the panel until the new one's first event takes
  // its place, so the dialog stays open and does not close and reopen.
  let again = $state<(() => Promise<unknown>) | null>(null);

  async function begin(run: () => Promise<unknown>, keep = false) {
    again = () => begin(run, true);
    if (!keep) store.dismissCeremony();
    try {
      await run();
    } catch (e) {
      fail(e);
    }
    await store.refreshSlots();
    await store.refreshEntangled();
  }

  function slotIcon(t: string): string {
    return t === "hardware" ? "i-yubi" : t === "recovery" ? "i-recovery" : "i-password";
  }

  // An unlock method's second line, in the glossary's words (ruled
  // 2026-10-17: a YubiKey is never a bare "key"). Since Revision 2 the
  // vault's password with every YubiKey is the vault's, not a slot's
  // (FORMAT.md §3.1), so no line says anything about it; a recovery key
  // names its ID, so the right sheet can be found (FORMAT.md §18.4).
  function slotMeta(s: SlotView): string {
    const kind = s.type === "hardware" ? "YubiKey · PIN and touch" : s.type === "recovery" ? "Recovery key" : "Password";
    const id = s.type === "recovery" && s.recoveryId ? ` · ${s.recoveryId}` : "";
    return `${kind}${id} · added ${date(s.createdAt)}`;
  }

  // Add an unlock method, preset to a recovery key: the action beside the
  // greyed password switch (APP.md §13).
  function addRecoveryKey() {
    addKind = "recovery";
    addLabel = "";
    adding = true;
  }

  // The file a backup is saved to, as the user picks it; empty when the
  // picker was cancelled.
  async function pickBackup(): Promise<string> {
    return (await Shell.SaveFile("Export a backup of the vault", `backup-${new Date().toISOString().slice(0, 10)}.eks`, "")) || "";
  }

  async function exportBackup() {
    const p = await pickBackup();
    if (p) await exportTo(p);
  }

  // The export itself, apart from the file picked for it, so that Start
  // again saves to the same file.
  async function exportTo(p: string, keep = false): Promise<boolean> {
    again = () => exportTo(p, true);
    if (!keep) store.dismissCeremony();
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

  // The backups card's status (direction 9, ruled 2026-10-18): the last
  // backup against what it lacks, by lib/backup.ts. The counts are read
  // only from an unlocked registry that is not Tampered (the core answers
  // them 0 otherwise), so while Locked or Tampered the line says the date
  // alone — or that no backup is recorded — and claims nothing about what
  // a backup holds. The date is relative: recency is the point.
  const backupAt = $derived(store.backup?.at ?? store.lastExportAt);
  const bline = $derived(backupLine(store.backup ?? (backupAt ? { at: backupAt, archives: 0, missing: 0, rekeyed: 0 } : null), unlocked && !tampered && !!store.backup, backupAt ? sinceText(backupAt, store.now) : ""));
  let rotateBackup = $state(false);

  function openRotate() {
    rotateBackup = backupWanted; // pre-selected, and the user may clear it
    rotating = true;
  }

  async function doRotate() {
    rotating = false;
    if (!rotateBackup) return begin(() => Keys.RotateNow());
    const p = await pickBackup();
    if (p) await rekey(p);
  }

  // The backup the rotate dialog asked for, then the rotation: no backup,
  // no rotation, and Start again after a failed backup begins with the
  // backup again, to the same file.
  async function rekey(p: string, keep = false) {
    if (!(await exportTo(p, keep))) {
      again = () => rekey(p, true);
      return;
    }
    await begin(() => Keys.RotateNow());
  }

  // Inspect a backup… reads the file's label; proving it opens is the
  // lock screen's Test that a backup opens… (the verb table, strings.ts).
  async function inspectBackup() {
    const p = (await Shell.PickFiles("Inspect a backup", false)) ?? [];
    if (!p.length) return;
    try {
      backup = await Vault.InspectFile(p[0]);
    } catch (e) {
      fail(e);
    }
  }

  // The foot's note (LayerFoot): the vault's name alone, since the page
  // says the count (ruled 2026-10-17).
  $effect(() => {
    store.footNote = vaultCopy.foot(st?.displayName ?? "");
  });

  // The ceremony dialog's two ways out. Close, once the ceremony is over,
  // is the panel's own button; Escape is Cancel while the panel offers
  // one — the ceremony's, Vault.CancelUnlock, the path its button takes —
  // and Close after (APP.md §7, "Dialogs stay put": Esc is Cancel where
  // there is one; the review's finding 12, where Escape did nothing
  // mid-ceremony).
  function closeCeremony() {
    store.dismissCeremony();
    void store.refreshSlots();
    void store.refreshEntangled();
  }

  function escapeCeremony() {
    if (store.ceremonyIsOver) closeCeremony();
    else void Vault.CancelUnlock();
  }
</script>

<div class="layer-head"><h1 class="t-title">{vaultCopy.title}</h1>{#if !unlocked}<span class="chip"><svg class="i i-14"><use href="#i-lock" /></svg>{chipCopy.locked}</span>{/if}</div>

<div class="layer-body">
  {#if tampered}
    <!-- First on the page (direction 9; APP.md §6, The Tampered banner): the danger tier, with
         the fix every greyed change here points at. -->
    <TamperedBar reason={st?.tamperedReason} />
  {/if}
  {#if !unlocked}
    <div class="bar line"><svg class="i i-14"><use href="#i-lock" /></svg><span class="grow">{vaultCopy.lockedLine}</span><button type="button" class="btn sm accent" onclick={() => store.unlock()}>{commandCopy.unlock}</button></div>
  {:else if st?.setupNeeded}
    <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span class="grow">{vaultCopy.setupLine}</span></div>
  {/if}

  <div class="ks-cols">
    <div class="ks-col">
      <div class="ks-head"><h2 class="t-section">{vaultCopy.unlockMethods}</h2><span class="t-quiet">{vaultCopy.methodCount(store.slots.length)}</span></div>
      <!-- One card per way in, 4 px from the next: Windows Settings' list,
           as the settings and This vault are (APP.md §6, ruled 2026-10-17;
           they stood 12 apart, the column's gap). -->
      <div class="slots">
        {#each store.slots as s (s.recipientId)}
          <button type="button" class="slot" aria-pressed={selected === s.recipientId} onclick={() => (selected = s.recipientId)}>
            <div class="tile"><svg class="i"><use href="#{slotIcon(s.type)}" /></svg></div>
            <div class="body">
              <b>{s.label}</b>
              <span class="meta">{slotMeta(s)}</span>
            </div>
          </button>
        {/each}
      </div>
      <div class="rule"><svg class="i i-14"><use href="#i-info" /></svg>{vaultCopy.rule}</div>
      <div class="ks-actions">
        <button type="button" class="btn accent" disabled={!unlocked || tampered} title={greyed} onclick={() => (adding = true)}><svg class="i i-14"><use href="#i-plus" /></svg>{commandCopy.addUnlockMethod}</button>
        <button type="button" class="btn" disabled={!sel || !sel.removable || !unlocked || tampered} title={greyed ?? (sel && !sel.removable ? vaultCopy.rule : undefined)} onclick={() => (removing = true)}>{commandCopy.removeUnlockMethod}</button>
        {#if sel?.type === "recovery"}
          <button type="button" class="btn" disabled={!unlocked} title={unlocked ? undefined : greyed} onclick={() => { const id = sel?.recipientId; if (id) void begin(() => Keys.RevealRecoveryKey(id)); }}><svg class="i i-14"><use href="#i-recovery" /></svg>{commandCopy.showRecoveryKey}</button>
        {/if}
        <button type="button" class="btn" disabled={!unlocked || tampered} title={greyed} onclick={openRotate}><svg class="i i-14"><use href="#i-rotate" /></svg>{commandCopy.rekeyVault}</button>
      </div>

      <div class="ks-head top"><h2 class="t-section">{vaultCopy.backups}</h2></div>
      <div class="card backup">
        <!-- The status first (direction 9): attention while a backup is
             owed, a plain line with the accent's check while one holds
             every archive key, nothing with nothing to back up; then,
             under a date, the caveat, since the stamp is this PC's record
             and not a look at the file (lib/backup.ts). -->
        {#if bline.tone}
          <div class="status">
            {#if bline.tone === "attention"}
              <div class="bar attention"><svg class="i i-14"><use href="#i-backup" /></svg><span><strong>{bline.head}</strong>{bline.rest ? ` ${bline.rest}` : ""}</span></div>
            {:else}
              <p class="current"><svg class="i i-14" class:quiet={!bline.checked}><use href={bline.checked ? "#i-check" : "#i-backup"} /></svg><span>{bline.head}{bline.rest ? ` ${bline.rest}` : ""}</span></p>
            {/if}
            {#if bline.caveat}<p class="t-quiet">{bline.caveat}</p>{/if}
          </div>
        {/if}
        <!-- The consequence, in plain words: what a backup is for. -->
        <p class="t-sub">{vaultCopy.backupsLine}</p>
        <div class="ks-actions">
          <!-- The accent while a backup is owed; it suggests and never
               gates (lastExportAt is local and unauthenticated). -->
          <button type="button" class="btn" class:accent={bline.suggestExport} disabled={!unlocked || tampered} title={greyed} onclick={() => void exportBackup()}><svg class="i i-14"><use href="#i-backup" /></svg>{commandCopy.exportBackup}</button>
          <button type="button" class="btn" onclick={inspectBackup}>{commandCopy.inspectBackup}</button>
        </div>
        <!-- Import archives from a backup… lives in the Archives page's command bar; this
             is the pointer to it (APP.md §13), and it hands the ask over
             the way the key dialog does — the page opens its picker on
             arrival — whenever that command could run; otherwise it only
             goes there, where the command shows why it is greyed. -->
        <p class="t-quiet">To take archives out of a backup without replacing this vault, {#if unlocked && !tampered}use <button type="button" class="btn link" onclick={() => store.askImportRecords()}>{commandCopy.importArchives}</button> on the Archives page.{:else}use {commandCopy.importArchives} on the Archives page. <button type="button" class="btn link" onclick={() => store.go("archives")}>{commandCopy.goThere}</button>{/if}</p>
        {#if backup}
          <dl class="kv">
            <dt>{vaultCopy.inspected.file}</dt><dd title={backup.path}>{leaf(backup.path)}</dd>
            <dt>{vaultCopy.inspected.kind}</dt><dd>{backup.kind === "backup" ? vaultCopy.inspected.backup : vaultCopy.inspected.vault}</dd>
            <dt>{vaultCopy.inspected.dated}</dt><dd>{dateTime(backup.modifiedAt)}</dd>
            <dt>{vaultCopy.inspected.thisVault}</dt><dd>{backup.vaultMatches ? vaultCopy.inspected.yes : vaultCopy.inspected.no}</dd>
            <dt>{vaultCopy.inspected.methods}</dt><dd class="num">{backup.slotCount}</dd>
          </dl>
          {#if backup.vaultMatches && !backup.newer && st}
            <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span>{vaultCopy.inspected.older(dateTime(st.modifiedAt))}</span></div>
          {:else if backup.vaultMatches && backup.newer}
            <div class="bar accent"><svg class="i i-14"><use href="#i-info" /></svg><span>{vaultCopy.inspected.newer}</span></div>
          {/if}
          <p class="t-quiet">{vaultCopy.inspected.testHint}</p>
        {/if}
      </div>
    </div>

    <div class="ks-col">
      <div class="ks-head"><h2 class="t-section">{vaultCopy.thisVault}</h2></div>
      <div class="setcard">
        <!-- Facts, drawn as every fact is (APP.md §6): the label a caption,
             the value beside it; the path under its label, whole. They
             are one row of the list, a card like the settings under. -->
        <div class="setrow"><div class="grow stack">
          <dl class="kv">
            <dt>{vaultCopy.name}</dt><dd title={st?.displayName}>{st?.displayName}</dd>
            <dt>{vaultCopy.lastChanged}</dt><dd>{dateTime(st?.modifiedAt ?? 0)}</dd>
          </dl>
          <dl class="kv stk"><dt>{vaultCopy.file}</dt><dd>{st?.path}</dd></dl>
        </div></div>
        <!-- One switch for the whole vault (FORMAT.md §3.1, APP.md §13):
             every YubiKey asks for it, the standalone password and the
             recovery key never do, and the old one is never asked for. -->
        <div class="setrow">
          <div class="lab">
            <b>{vaultCopy.password}</b>
            <span>{ent?.on ? vaultCopy.passwordOn : vaultCopy.passwordOff}</span>
          </div>
          <div class="ctl">
            {#if ent?.on}
              <button type="button" class="btn sm" disabled={!unlocked || tampered} title={greyed} onclick={() => (entangling = "change")}>{vaultCopy.change}</button>
              <button type="button" class="btn sm" disabled={!unlocked || tampered} title={greyed} onclick={() => (entangling = "off")}>{vaultCopy.turnOff}</button>
            {:else}
              <button type="button" class="btn sm" disabled={!unlocked || tampered || !ent?.canEnable} title={greyed ?? (entangleBlocked ? vaultCopy.passwordBlockedShort : undefined)} onclick={() => (entangling = "on")}>{vaultCopy.turnOn}</button>
            {/if}
          </div>
        </div>
        {#if entangleBlocked}
          <div class="bar attention">
            <svg class="i i-14"><use href="#i-warn" /></svg>
            <div class="why">
              <span>{ent?.reason && ent.reason !== "vault.invariant" ? codeText(ent.reason) : vaultCopy.passwordBlocked}</span>
              <!-- The cure is a command, so a button (APP.md §6, Controls). -->
              <button type="button" class="btn sm" onclick={addRecoveryKey}>{commandCopy.addRecoveryKey}</button>
            </div>
          </div>
        {/if}
        {#if (st?.retiredCopies ?? 0) > 0 && st?.retiredPath}
          <div class="setrow"><div class="lab"><b>{vaultCopy.replacedCopies}</b><span>{vaultCopy.replacedLine(st.retiredCopies)}</span></div><div class="ctl"><button type="button" class="btn link" onclick={() => void Shell.Reveal(st?.retiredPath ?? "")}>{vaultCopy.show}</button></div></div>
        {/if}
        {#if st?.damagedCopyPath}
          <div class="setrow"><div class="lab"><b>{vaultCopy.damagedCopy}</b><span>{vaultCopy.damagedLine}</span></div><div class="ctl"><button type="button" class="btn link" onclick={() => void Shell.Reveal(st?.damagedCopyPath ?? "")}>{vaultCopy.show}</button></div></div>
        {/if}
      </div>
    </div>
  </div>
</div>


{#if adding}
  <Dialog title={dialogCopy.addUnlockMethod.title} onclose={() => (adding = false)}>
    <p>{dialogCopy.addUnlockMethod.body}</p>
    <div class="field">
      <div class="field-top"><label for="ak-kind">{dialogCopy.addUnlockMethod.kind}</label></div>
      <select id="ak-kind" class="input" bind:value={addKind}>
        <option value="token">{dialogCopy.addUnlockMethod.token}</option>
        <option value="password" disabled={st?.hasHardwareSlot}>{st?.hasHardwareSlot ? dialogCopy.addUnlockMethod.passwordNotHere : dialogCopy.addUnlockMethod.password}</option>
        <option value="recovery">{dialogCopy.addUnlockMethod.recovery}</option>
      </select>
    </div>
    {#if addKind === "password"}
      <p>{dialogCopy.addUnlockMethod.passwordNext}</p>
    {:else}
      <div class="field">
        <!-- The placeholder says what an empty field produces — a key is
             named after its serial number by the core — and a recovery
             key, which must be named, has none: an invented name there
             read as a default already filled in. -->
        <div class="field-top"><label for="ak-label">{addKind === "token" ? dialogCopy.addUnlockMethod.nameKey : dialogCopy.addUnlockMethod.nameRecovery}</label><span class="hint">{addKind === "token" ? dialogCopy.addUnlockMethod.optional : dialogCopy.addUnlockMethod.nameRecoveryHint}</span></div>
        <input id="ak-label" class="input" bind:value={addLabel} placeholder={addKind === "token" ? dialogCopy.addUnlockMethod.keyPlaceholder : undefined} />
      </div>
    {/if}
    {#if addKind === "token" && ent?.on}
      <!-- The vault's switch is inherited, never chosen per key: the new
           key is wrapped from the kept K_P offline (APP.md §13). -->
      <p>{dialogCopy.addUnlockMethod.passwordInherited}</p>
    {/if}
    {#snippet actions()}
      <button type="button" class="btn accent" disabled={!addLabel && addKind === "recovery"} title={!addLabel && addKind === "recovery" ? dialogCopy.addUnlockMethod.needName : undefined} onclick={() => { adding = false; const kind = addKind, label = addKind === "password" ? "Password" : addLabel; void begin(() => Keys.BeginEnroll(kind, label)); addLabel = ""; }}>{dialogCopy.addUnlockMethod.primary}</button>
      <button type="button" class="btn" onclick={() => (adding = false)}>{dialogCopy.cancel}</button>
    {/snippet}
  </Dialog>
{/if}

{#if removing && sel}
  <!-- The dialog template (strings.ts): the consequence, then what still
       opens; the mechanism behind "How this works" (ruled 2026-10-17). -->
  <Dialog title={dialogCopy.removeUnlockMethod.title(sel.label)} onclose={() => (removing = false)}>
    <p>{dialogCopy.removeUnlockMethod.body(sel.label)}</p>
    <details class="how"><summary>{dialogCopy.howThisWorks}</summary><p>{dialogCopy.removeUnlockMethod.how}</p></details>
    {#snippet actions()}
      <button type="button" class="btn danger-fill" onclick={() => { removing = false; const id = sel.recipientId; void begin(() => Keys.RemoveSlot(id)); }}>{dialogCopy.removeUnlockMethod.primary}</button>
      <button type="button" class="btn" onclick={() => (removing = false)}>{dialogCopy.cancel}</button>
    {/snippet}
  </Dialog>
{/if}

{#if rotating}
  <Dialog title={dialogCopy.rekeyVault.title} onclose={() => (rotating = false)}>
    <p>{dialogCopy.rekeyVault.body}</p>
    <dl class="kv"><dt>{dialogCopy.rekeyVault.lastBackup}</dt><dd>{store.lastExportAt ? sinceText(store.lastExportAt, store.now) : dialogCopy.rekeyVault.noBackup}</dd></dl>
    {#if backupWanted}
      <p>{dialogCopy.rekeyVault.backupWhy}</p>
    {/if}
    <label class="check"><input type="checkbox" bind:checked={rotateBackup} />{dialogCopy.rekeyVault.backupFirst}</label>
    {#snippet actions()}
      <button type="button" class="btn accent" onclick={doRotate}>{rotateBackup ? dialogCopy.rekeyVault.primaryWithBackup : dialogCopy.rekeyVault.primary}</button>
      <button type="button" class="btn" onclick={() => (rotating = false)}>{dialogCopy.cancel}</button>
    {/snippet}
  </Dialog>
{/if}

{#if entangling && ent}
  <EntangledDialog mode={entangling} onclose={() => (entangling = "")} {begin} />
{/if}

{#if c && c.step === CeremonyStep.StepRecovery && !c.promptId && c.slotLabel}
  <!-- the recovery key is revealed by App, above every route -->
{:else if c}
  <Dialog title={vaultCopy.ceremonyTitle[c.kind] ?? vaultCopy.ceremonyTitle.export} onclose={escapeCeremony} dismissable={store.ceremonyIsOver}>
    <CeremonyPanel {c} onclose={closeCeremony} onagain={again ? () => void again?.() : undefined} />
  </Dialog>
{/if}

<style>
  .ks-head.top { margin-top: var(--s3); }
  /* The ways in: separate cards, 4 apart, as .setcard's rows are. */
  .slots { display: flex; flex-direction: column; gap: var(--s1); }
  .backup { padding: var(--s4); display: flex; flex-direction: column; gap: var(--s3); }
  .backup p { margin: 0; }
  /* The status and its caveat read as one: 4 apart, the card's 12 after. */
  .status { display: flex; flex-direction: column; gap: var(--s1); }
  /* A current backup is a fact, not a notice: no box, the check in the
     accent's ink before the sentence. */
  .current { display: flex; align-items: flex-start; gap: var(--s2); color: var(--ink); }
  .current .i { flex: none; margin-top: 3px; color: var(--accent-ink); }
  /* The date alone, with nothing counted (Locked, Tampered): no receipt,
     so the quiet backup icon instead of the check. */
  .current .i.quiet { color: var(--ink-3); }
  /* The invariant's reason and its cure, stacked: this card is the narrow
     column, where a bar's inline action would be a word per line. */
  .why { display: flex; flex-direction: column; align-items: flex-start; gap: var(--s1); min-width: 0; }
</style>

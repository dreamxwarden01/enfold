<script lang="ts">
  // The Vault page (direction 10, phase 4a; APP.md §6): every vault thing
  // in one place, in three sections read in order — Unlock methods, This
  // vault, Backups and copies — in one column. The vault's name is edited
  // here only (Rename…), the lock times are the vault's policy and live
  // here, and every backup action is here, the two that need a lock among
  // them: they lock and hand the lock screen its ask. Nothing on the page
  // is saved by a bar: a choice applies when it is made (SaveBar's one job).
  import { Archives, CeremonyStep, Keys, Shell, Vault, errorOf } from "../lib/api";
  import type { ArchiveSummary, FileInfo, SlotView } from "../lib/api";
  import { store } from "../lib/state.svelte";
  import { chipCopy, codeText, commandCopy, dialogCopy, importCopy, lockCopy, pendingCopy, sinceText, vaultCopy } from "../lib/strings";
  import { backupLine } from "../lib/backup";
  import { backupLine as backupDone, checkedLine } from "../lib/foot";
  import { date, dateTime, leaf, minutesLabel } from "../lib/format";
  import { pending } from "../lib/pending";
  import { filesLine } from "../lib/vaultfiles";
  import Dialog from "./Dialog.svelte";
  import CeremonyPanel from "./CeremonyPanel.svelte";
  import EntangledDialog from "./EntangledDialog.svelte";
  import PendingLabel from "./PendingLabel.svelte";
  import RenameVaultDialog from "./RenameVaultDialog.svelte";
  import TamperedBar from "./TamperedBar.svelte";

  const st = $derived(store.status);
  const s = $derived(store.settings);
  const unlocked = $derived(store.unlocked);
  // The banner's one predicate (store.tampered; APP.md §6).
  const tampered = $derived(store.tampered);
  // This page's own ceremonies: the unlock methods, the backups and the
  // vault's password. A `records` ceremony belongs to the merge dialog,
  // which renders its own panel — the incoming file's recovery prompt has
  // nowhere to go here (APP.md §13).
  const c = $derived(store.ceremony && !["unlock", "create", "import", "setup", "verify", "records"].includes(store.ceremony.kind) ? store.ceremony : null);

  let adding = $state(false);
  let addKind = $state<"token" | "password" | "recovery">("token");
  let addLabel = $state("");
  // The card whose *Remove…* was pressed (direction 10): each card carries
  // its own actions, so the cards are no longer a selection — the dialog
  // takes the method it was asked from.
  let removing = $state<SlotView | null>(null);
  let rotating = $state(false);
  let renaming = $state(false);
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

  // A card's *Remove…*: greyed by the vault's state first, then by the
  // two-method rule, each with its reason as the title.
  function removeTitle(s: SlotView): string | undefined {
    return greyed ?? (s.removable ? undefined : vaultCopy.rule);
  }

  // Add an unlock method, preset to a recovery key: the action beside the
  // greyed password switch (APP.md §13).
  function addRecoveryKey() {
    addKind = "recovery";
    addLabel = "";
    adding = true;
  }

  // ---- This vault ---------------------------------------------------------

  // The name the vault is called by: the setting Rename… writes, which the
  // status follows at once (APP.md §3, Settings); store.vaultName, as the
  // rail's head and the lock screen read it.
  const vaultName = $derived(store.vaultName);

  // The lock times (direction 10): the vault's policy, chosen here and
  // applied the moment they are chosen, one write each (store.choose).
  // They can be changed only while the vault is unlocked; meanwhile the
  // selects are greyed and the row's caption says so. Locked, the core has
  // no lock times to give — GetSettings answers 0, which the selects would
  // draw as "10 minutes (default)", a time the vault may not keep — so
  // each select holds one disabled placeholder, "Unlock to see" (ruled
  // 2026-10-19).
  const idleChoices = [0, 2, 5, 10, 15, 20, 30];
  const absChoices = [0, 30, 60, 120, 240, 480];
  const IDLE_DEFAULT = 10;
  const ABS_DEFAULT = 60;
  const timesOpen = $derived(unlocked && !!s?.timeoutsAdjustable);
  // A timeout's value as its select shows it: the default says so after
  // the time it stands for.
  function timeoutLabel(m: number, fallback: number): string {
    return m === 0 ? vaultCopy.defaultValue(minutesLabel(fallback)) : minutesLabel(m);
  }

  function num(e: Event): number {
    return Number((e.currentTarget as HTMLSelectElement).value);
  }

  // ---- Backups and copies -------------------------------------------------

  // The file a backup is saved to, as the user picks it; empty when the
  // picker was cancelled.
  async function pickBackup(): Promise<string> {
    return (await Shell.SaveFile(vaultCopy.exportPicker, `backup-${new Date().toISOString().slice(0, 10)}.eks`, "")) || "";
  }

  // The file a plain export is writing, for its done line: said in the
  // foot once its dialog is closed on "Backup saved", with the way to the
  // file (direction 4). A backup taken before a re-key leaves none, since
  // the re-key is the act that ends there.
  let exportedTo = "";

  async function exportBackup() {
    const p = await pickBackup();
    if (!p) return;
    exportedTo = p;
    await exportTo(p);
  }

  // The Archives page's summary's *Export…* came here to start this
  // (store.askExport, direction 6): taken once, and only where the button
  // itself would run — unlocked and not Tampered; otherwise dropped, and
  // the page's own greyed button says why.
  $effect(() => {
    if (!store.exportAsked) return;
    const asked = store.takeExportAsked();
    if (asked && unlocked && !tampered) void exportBackup();
  });

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

  // The backups' status (direction 9, ruled 2026-10-18): the last backup
  // against what it lacks, by lib/backup.ts. The counts are read only
  // from an unlocked registry that is not Tampered (the core answers them
  // 0 otherwise), so while Locked or Tampered the line says the date
  // alone — or that no backup is recorded — and claims nothing about what
  // a backup holds. The date is relative: recency is the point.
  const backupAt = $derived(store.backup?.at ?? store.lastExportAt);
  const bline = $derived(backupLine(store.backup ?? (backupAt ? { at: backupAt, archives: 0, missing: 0, rekeyed: 0 } : null), unlocked && !tampered && !!store.backup, backupAt ? sinceText(backupAt, store.now) : ""));
  const addAccent = $derived(unlocked && !tampered && !bline.suggestExport);
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

  // Inspect a backup… reads the file's label; proving it opens is *Test
  // that a backup opens…*, beside it (the verb table, strings.ts).
  async function inspectBackup() {
    const p = (await Shell.PickFiles(vaultCopy.inspectPicker, false)) ?? [];
    if (!p.length) return;
    try {
      backup = await Vault.InspectFile(p[0]);
    } catch (e) {
      fail(e);
    }
  }

  // Test that a backup opens… (direction 10): the file first, here, so a
  // cancelled picker leaves the vault as it was; then the store locks and
  // the lock screen starts the check on its stage (store.askTestBackup;
  // APP.md §2.1, the core checks a backup only while Locked).
  async function testBackup() {
    const p = (await Shell.PickFiles(lockCopy.testPicker, false)) ?? [];
    if (p.length) await store.askTestBackup(p[0]);
  }

  // Import archives from a backup… runs here (direction 10): the picker,
  // then the merge dialog, which stands above every route (App.svelte).
  async function importArchives() {
    const p = (await Shell.PickFiles(importCopy.archivesTitle, false)) ?? [];
    if (p.length) store.merging = p[0];
  }

  // The archives' files (direction 10; lib/vaultfiles.ts says what may be
  // claimed from what). The count is over the whole registry, hidden
  // records among it, so the page reads its own list with hidden ones
  // shown unless the store's already is; it reads it again whenever the
  // store's list is read again (archives.changed, a pass's end), and
  // holds none while the vault is not Unlocked, when the core lists the
  // open archives alone.
  let registry = $state<ArchiveSummary[] | null>(null);
  $effect(() => {
    const list = store.archives;
    if (!unlocked) {
      registry = null;
      return;
    }
    if (store.showHidden) {
      registry = list;
      return;
    }
    let live = true;
    Archives.List(true)
      .then((r) => {
        if (live) registry = r ?? [];
      })
      .catch(() => {
        if (live) registry = null;
      });
    return () => {
      live = false;
    };
  });
  const files = $derived(filesLine({ unlocked, records: registry, check: store.filesCheck }));

  // Find missing files (APP.md §13), as the Archives page runs it: the
  // core answers once its pass has ended, the foot says what it found
  // (direction 4), and the list is read again for the line above. The
  // button waits with the ring; a second press meanwhile is ignored.
  let checking = $state<Promise<unknown> | null>(null);
  let checkArmed = $state(false);
  async function findMissing() {
    if (checking) return;
    const p = store.checkFiles();
    checking = p;
    try {
      const r = await p;
      store.showDone(checkedLine(r));
      await store.refreshArchives();
    } catch (e) {
      fail(e);
    } finally {
      checking = null;
    }
  }

  // The foot's note (LayerFoot): the vault's name alone, since the page
  // says the count (ruled 2026-10-17).
  $effect(() => {
    store.footNote = vaultCopy.foot(vaultName);
  });

  // The ceremony dialog's two ways out. Close, once the ceremony is over,
  // is the panel's own button; Escape is Cancel while the panel offers
  // one — the ceremony's, Vault.CancelUnlock, the path its button takes —
  // and Close after (APP.md §7, "Dialogs stay put": Esc is Cancel where
  // there is one; the review's finding 12, where Escape did nothing
  // mid-ceremony).
  function closeCeremony() {
    const ended = store.ceremony;
    if (exportedTo && ended?.kind === "export" && ended.step === CeremonyStep.StepDone) store.showDone(backupDone(exportedTo));
    exportedTo = "";
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
  <!-- One column, the sections in the order they are read (direction 10):
       the page is a list of things about one vault, and a second column
       put the lock times' 220 px selects in a 316 px box. The banners
       stand in the column too, so every row shares its right edge (the
       final shoot of phase 4a: the locked line ran the body's width, its
       Unlock far past the cards). -->
  <div class="vpage">
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
    <section class="vsec" aria-labelledby="vs-methods">
      <!-- The vault-wide acts stand in the section's head, apart from the
           cards, so none reads as a card's (direction 10). -->
      <div class="ks-head">
        <h2 id="vs-methods" class="t-section">{vaultCopy.unlockMethods}</h2>
        <span class="t-quiet count">{vaultCopy.methodCount(store.slots.length)}</span>
        <span class="head-acts">
          <!-- One accent per surface (APP.md §6): *Add an unlock method…*
               is the page's while nothing else asks for it; the locked
               line's *Unlock* or an owed backup's *Export backup…* takes
               it, and this one is standard then (the final shoot of
               phase 4a counted two). -->
          <button type="button" class="btn sm" class:accent={addAccent} disabled={!unlocked || tampered} title={greyed} onclick={() => (adding = true)}><svg class="i i-14"><use href="#i-plus" /></svg>{commandCopy.addUnlockMethod}</button>
          <button type="button" class="btn sm" disabled={!unlocked || tampered} title={greyed} onclick={openRotate}><svg class="i i-14"><use href="#i-rotate" /></svg>{commandCopy.rekeyVault}</button>
        </span>
      </div>
      <!-- One card per way in, 4 px from the next: Windows Settings' list
           (APP.md §6, ruled 2026-10-17). Each card carries its own actions
           at its right (direction 10): small standard buttons that show
           on hover and on focus, and stay while the card's dialog is up —
           inline rather than behind a ⋯ menu, since at 1024 the row holds
           both with room to spare, and a greyed *Remove…* keeps its reason
           one hover away instead of two presses. Where nothing hovers (a
           touch screen) they always show. A method cannot be renamed:
           there is no call for it. -->
      <ul class="slots">
        {#each store.slots as m (m.recipientId)}
          <li class="slot" class:held={removing?.recipientId === m.recipientId}>
            <div class="tile"><svg class="i"><use href="#{slotIcon(m.type)}" /></svg></div>
            <div class="body">
              <b>{m.label}</b>
              <span class="meta">{slotMeta(m)}</span>
            </div>
            <div class="slot-acts">
              {#if m.type === "recovery"}
                <button type="button" class="btn sm" disabled={!unlocked} title={unlocked ? undefined : greyed} onclick={() => { const id = m.recipientId; void begin(() => Keys.RevealRecoveryKey(id)); }}>{commandCopy.showRecoveryKey}</button>
              {/if}
              <button type="button" class="btn sm" disabled={!m.removable || !unlocked || tampered} title={removeTitle(m)} onclick={() => (removing = m)}>{commandCopy.removeUnlockMethod}</button>
            </div>
          </li>
        {/each}
      </ul>
      <div class="rule"><svg class="i i-14"><use href="#i-info" /></svg>{vaultCopy.rule}</div>
      <div class="setcard">
        <!-- One switch for the whole vault (FORMAT.md §3.1, APP.md §13):
             every YubiKey asks for it, the standalone password and the
             recovery key never do, and the old one is never asked for. -->
        <div class="setrow">
          <div class="lab">
            <b>{vaultCopy.password}</b>
            <span>{ent?.on ? vaultCopy.passwordOn : vaultCopy.passwordOff}</span>
          </div>
          <div class="ctl row">
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
            <span class="grow">{ent?.reason && ent.reason !== "vault.invariant" ? codeText(ent.reason) : vaultCopy.passwordBlocked}</span>
            <!-- The cure is a command, so a button (APP.md §6, Controls). -->
            <button type="button" class="btn sm" onclick={addRecoveryKey}>{commandCopy.addRecoveryKey}</button>
          </div>
        {/if}
      </div>
    </section>

    <section class="vsec" aria-labelledby="vs-vault">
      <div class="ks-head"><h2 id="vs-vault" class="t-section">{vaultCopy.thisVault}</h2></div>
      <div class="setcard">
        <!-- Facts, drawn as every fact is (APP.md §6): the label a caption,
             the value beside it; the path under its label, whole. The name
             is edited here and nowhere else, through its dialog. -->
        <div class="setrow"><div class="grow stack">
          <dl class="kv">
            <dt>{vaultCopy.name}</dt><dd><span class="vname" title={vaultName}>{vaultName}</span><button type="button" class="btn link" onclick={() => (renaming = true)}>{commandCopy.rename}</button></dd>
            <dt>{vaultCopy.lastChanged}</dt><dd>{dateTime(st?.modifiedAt ?? 0)}</dd>
          </dl>
          <dl class="kv stk"><dt>{vaultCopy.file}</dt><dd>{st?.path}</dd></dl>
        </div></div>
        {#if s}
          <!-- The lock times: a choice applies when it is made (direction
               10) — the ring while it is written, the code's line under the
               select and the old value back if it is refused. -->
          <div class="setrow">
            <div class="lab"><b id="vs-idle">{vaultCopy.idleLock}</b><span>{timesOpen ? vaultCopy.idleLine(minutesLabel(s.idleMinutes || IDLE_DEFAULT)) : vaultCopy.unlockToChange}</span></div>
            <div class="ctl choice">
              <span class="selwrap" use:pending={store.choosing.idleMinutes}>
                <select class="input" aria-labelledby="vs-idle" disabled={!timesOpen} value={unlocked ? s.idleMinutes : ""} aria-invalid={!!store.choiceRefused.idleMinutes || undefined} onchange={(e) => void store.choose("idleMinutes", num(e))}>
                  {#if unlocked}
                    {#each idleChoices as v (v)}<option value={v}>{timeoutLabel(v, IDLE_DEFAULT)}</option>{/each}
                  {:else}
                    <option value="" disabled>{vaultCopy.unlockToSee}</option>
                  {/if}
                </select>
              </span>
              {#if store.choiceRefused.idleMinutes}<span class="field-error" role="alert">{store.choiceRefused.idleMinutes}</span>{/if}
            </div>
          </div>
          <div class="setrow">
            <div class="lab"><b id="vs-abs">{vaultCopy.absoluteLock}</b><span>{timesOpen ? vaultCopy.absoluteLine : vaultCopy.unlockToChange}</span></div>
            <div class="ctl choice">
              <span class="selwrap" use:pending={store.choosing.absoluteMinutes}>
                <select class="input" aria-labelledby="vs-abs" disabled={!timesOpen} value={unlocked ? s.absoluteMinutes : ""} aria-invalid={!!store.choiceRefused.absoluteMinutes || undefined} onchange={(e) => void store.choose("absoluteMinutes", num(e))}>
                  {#if unlocked}
                    {#each absChoices as v (v)}<option value={v}>{timeoutLabel(v, ABS_DEFAULT)}</option>{/each}
                  {:else}
                    <option value="" disabled>{vaultCopy.unlockToSee}</option>
                  {/if}
                </select>
              </span>
              {#if store.choiceRefused.absoluteMinutes}<span class="field-error" role="alert">{store.choiceRefused.absoluteMinutes}</span>{/if}
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
    </section>

    <section class="vsec" aria-labelledby="vs-backups">
      <div class="ks-head"><h2 id="vs-backups" class="t-section">{vaultCopy.backups}</h2></div>
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
        <!-- Every backup action in one wrapping row (direction 10), the
             commands in the verb table's words; the two that need the
             vault Locked lock it first and hand the lock screen its ask. -->
        <div class="acts-block">
          <div class="ks-actions">
            <!-- The accent while a backup is owed; it suggests and never
                 gates (lastExportAt is local and unauthenticated). -->
            <button type="button" class="btn" class:accent={bline.suggestExport} disabled={!unlocked || tampered} title={greyed} onclick={() => void exportBackup()}><svg class="i i-14"><use href="#i-backup" /></svg>{commandCopy.exportBackup}</button>
            <button type="button" class="btn" onclick={inspectBackup}>{commandCopy.inspectBackup}</button>
            <button type="button" class="btn" onclick={() => void testBackup()}>{commandCopy.testBackup}</button>
            <button type="button" class="btn" disabled={!unlocked || tampered} title={greyed} onclick={() => void importArchives()}>{commandCopy.importArchives}</button>
            <button type="button" class="btn" onclick={() => void store.askReplaceVault()}>{commandCopy.replaceVault}</button>
          </div>
          <p class="t-quiet">{vaultCopy.backupsNote}</p>
        </div>
        <div class="files-line">
          <span>{vaultCopy.files} <span class="files-state" class:owed={files.owed}>{files.text}</span></span>
          <button type="button" class="btn link" disabled={!unlocked} title={unlocked ? undefined : greyed} onclick={() => void findMissing()} use:pending={{ on: checking, armed: (b) => (checkArmed = b) }}><PendingLabel armed={checkArmed} rest={commandCopy.findMissing} busy={pendingCopy.checking} /></button>
        </div>
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
        {/if}
      </div>
    </section>
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

{#if removing}
  {@const target = removing}
  <!-- The dialog template (strings.ts): the consequence, then what still
       opens; the mechanism behind "How this works" (ruled 2026-10-17). -->
  <Dialog title={dialogCopy.removeUnlockMethod.title(target.label)} onclose={() => (removing = null)}>
    <p>{dialogCopy.removeUnlockMethod.body(target.label)}</p>
    <details class="how"><summary>{dialogCopy.howThisWorks}</summary><p>{dialogCopy.removeUnlockMethod.how}</p></details>
    {#snippet actions()}
      <button type="button" class="btn danger-fill" onclick={() => { removing = null; const id = target.recipientId; void begin(() => Keys.RemoveSlot(id)); }}>{dialogCopy.removeUnlockMethod.primary}</button>
      <button type="button" class="btn" onclick={() => (removing = null)}>{dialogCopy.cancel}</button>
    {/snippet}
  </Dialog>
{/if}

{#if renaming}
  <RenameVaultDialog name={vaultName} onclose={() => (renaming = false)} />
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
  /* The page is one column (direction 10), as wide as Settings' at most,
     its sections 24 apart and each its head and its rows 12 apart. */
  .vpage { display: flex; flex-direction: column; gap: var(--s6); width: 100%; max-width: var(--page-col); padding-bottom: var(--s3); }
  .vsec { display: flex; flex-direction: column; gap: var(--s3); min-width: 0; }
  /* The section's head: the title, its count, and the vault-wide acts at
     the right, centred on the title's line. */
  .ks-head .count { margin-left: 0; }
  .head-acts { margin-left: auto; display: flex; gap: var(--s2); align-self: center; }
  /* The ways in: separate cards, 4 apart, as .setcard's rows are. */
  .slots { display: flex; flex-direction: column; gap: var(--s1); margin: 0; padding: 0; list-style: none; }
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
  /* The row and its pre-note read as one: the caption 8 under the row. */
  .acts-block { display: flex; flex-direction: column; gap: var(--s2); }
  /* The archives' files: a fact and its command on one line, a stroke
     above it closing the card's actions off from the files. */
  .files-line { display: flex; align-items: baseline; flex-wrap: wrap; gap: var(--s1) var(--s3); padding-top: var(--s3); border-top: 1px solid var(--stroke); color: var(--ink-2); }
  .files-state { color: var(--ink); }
  .files-state.owed { font-weight: 600; }
  /* The name and its Rename… share the value's line. */
  .vname { overflow-wrap: anywhere; margin-right: var(--s3); }
</style>

<script lang="ts">
  // The Vault page (direction 10, phase 4a; APP.md §6): every vault thing
  // in one place, in three sections read in order — Unlock methods, This
  // vault, Backups and copies — in one column. The vault's name is edited
  // here only (Rename…), the lock times are the vault's policy and live
  // here, and every backup action is here, the two that need a lock among
  // them: they lock and hand the lock screen its ask. Nothing on the page
  // is saved by a bar: a choice applies when it is made (SaveBar's one job).
  //
  // The acts that may re-key the vault (ruled 2026-10-27) ask the fact first
  // and offer the backup taken after the change (RekeyQuestion,
  // BackupChoice); their ends carry the next step a compromise calls for,
  // one chain of acts begun from one another's ends (lib/rekey.ts, the
  // store's lastAct and chain). Replace recovery keys… is a vault-wide act,
  // so it stands in the section's head; its new keys are shown here, in the
  // recovery key's dialog in keep mode, while its ceremony waits for each.
  import { Archives, CeremonyStep, Keys, Shell, Vault, errorOf } from "../lib/api";
  import type { AfterFacts, ArchiveSummary, CeremonyState, FileInfo, SlotView } from "../lib/api";
  import { store } from "../lib/state.svelte";
  import { ceremonyCopy, chipCopy, codeText, commandCopy, dialogCopy, importCopy, lockCopy, noticeCopy, pendingCopy, replaceCopy, revealCopy, sinceText, vaultCopy } from "../lib/strings";
  import { backupLine } from "../lib/backup";
  import { backupLine as backupDone, checkedLine } from "../lib/foot";
  import type { Answer, FollowUp } from "../lib/rekey";
  import { backupSavedOnClose, ceremonyTitleOf, lastProtector, primaryOf, questionOf, rekeyLimitOf, removeRule, replaceGreyed, replaceMinutes } from "../lib/rekey";
  import { date, dateTime, leaf, minutesLabel } from "../lib/format";
  import { pending } from "../lib/pending";
  import { fieldMenu } from "../lib/fieldmenu";
  import { filesLine } from "../lib/vaultfiles";
  import Dialog from "./Dialog.svelte";
  import CeremonyPanel from "./CeremonyPanel.svelte";
  import EntangledDialog from "./EntangledDialog.svelte";
  import RekeyQuestion from "./RekeyQuestion.svelte";
  import BackupChoice from "./BackupChoice.svelte";
  import ReplaceDialog from "./ReplaceDialog.svelte";
  import RecoveryReveal from "./RecoveryReveal.svelte";
  import PendingLabel from "./PendingLabel.svelte";
  import RenameVaultDialog from "./RenameVaultDialog.svelte";
  import TamperedBar from "./TamperedBar.svelte";
  import NoticeSlot from "./NoticeSlot.svelte";
  import { unfold } from "../lib/motion";

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
  let replacing = $state(false);
  let backup = $state<FileInfo | null>(null);
  // Whether the dialog now open was opened by an end's next step, its
  // button, so that the act it begins goes on in that end's chain
  // (lib/rekey.ts chainFor); every other opening is the page's own, and
  // begins a chain of its own when the act re-keys.
  let fromFollow = $state(false);

  // The vault's entangled password (APP.md §13): one switch for the whole
  // vault, on / change / off, and no field asks for the old password.
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

  // A refusal of this page's own press is an error InfoBar in the notice
  // slot, at the top of the column; the press may have been made far below
  // it — the backups card, which a short window reaches only by scrolling,
  // where the barrier refuses *Export backup…* while a retirement is owed —
  // so the slot is brought into view with it (NoticeSlot reveal).
  let slot = $state<ReturnType<typeof NoticeSlot>>();
  function fail(e: unknown) {
    store.notice(codeText(errorOf(e).code), "error");
    slot?.reveal();
  }

  // Every ceremony this page starts can change the unlock methods or the
  // vault's switch, so both are read again when it ends (APP.md §13).
  // `again` keeps the act last begun, with what it was begun with, for
  // the dialog's Start again after a failure the user did not choose
  // (CeremonyPanel onagain; ruled 2026-10-17). Begun again, the failed
  // ceremony is left on the panel until the new one's first event takes
  // its place, so the dialog stays open and does not close and reopen.
  // Begun again, an act records no new start: it keeps the chain its first
  // begin recorded (store.noteAct). Whether the act could be begun is the
  // answer, for the recovery-key switch below.
  let again = $state<(() => Promise<unknown>) | null>(null);

  async function begin(run: () => Promise<unknown>, keep = false): Promise<boolean> {
    again = () => begin(run, true);
    if (!keep) {
      store.dismissCeremony();
      heldFollow = null;
    }
    let ok = true;
    try {
      await run();
    } catch (e) {
      ok = false;
      fail(e);
    }
    await store.refreshSlots();
    await store.refreshEntangled();
    return ok;
  }

  // An act that may take a backup after its change (APP.md §13; ruled
  // 2026-10-27): the file is kept for the status bar's done line when the
  // dialog closes on a Done that wrote it (CeremonyState.backedUp). Start
  // again repeats the act with the same file: an act that failed changed
  // nothing and wrote nothing.
  function beginWith(run: (backup: string) => Promise<unknown>, backup: string): Promise<boolean> {
    backupTo = backup;
    return begin(() => run(backup));
  }

  // The vault's recovery keys, as the cards list them.
  const recoveryKeys = $derived(store.slots.filter((s) => s.type === "recovery").length);

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
  // two-method rule, each with its reason as the title. A YubiKey's says
  // what to add first, which is how a lost one becomes removable (ruled
  // 2026-10-27).
  function removeTitle(s: SlotView): string | undefined {
    return greyed ?? (s.removable ? undefined : removeRule(s.type));
  }

  // Add an unlock method…, from the head, or from an end's next step
  // (follow), which adds the new YubiKey first.
  function openAdd(follow = false) {
    fromFollow = follow;
    if (follow) {
      addKind = "token";
      addLabel = "";
    }
    adding = true;
  }

  // Add an unlock method, preset to a recovery key: the action beside the
  // greyed password switch (APP.md §13).
  function addRecoveryKey() {
    fromFollow = false;
    addKind = "recovery";
    addLabel = "";
    adding = true;
  }

  // An enrolment begins with a YubiKey of the vault as its proof wherever
  // there is one (keys.go acquireUnlocked). Use recovery key instead begins
  // it again with a recovery key (ruled 2026-10-27): LastAct carries no
  // label and again re-runs the act as begun, so the start keeps this
  // second closure beside it, which keeps the chain and the start as the
  // first begin recorded them; the switch marks only withRecoveryKey.
  let toRecovery = $state<(() => Promise<boolean>) | null>(null);

  function startEnroll(kind: "token" | "password" | "recovery", label: string) {
    store.noteAct({ act: "enroll", type: kind, fromFollow });
    toRecovery = () => begin(() => Keys.BeginEnroll(kind, label, true), true);
    void begin(() => Keys.BeginEnroll(kind, label, false));
  }

  // The ceremony drawn while the switch cancels one and begins the next,
  // so that the dialog stays up until the new one's first event takes its
  // place, as Start again's does (the cancelled one is dropped by the store
  // at once).
  let held = $state<CeremonyState | null>(null);
  let holdUntilNew = false;

  async function switchToRecovery() {
    const go = toRecovery;
    if (!go || !c) return;
    held = c;
    await Vault.CancelUnlock().catch(() => {});
    await untilDropped();
    store.noteRecoverySwitch();
    holdUntilNew = true;
    const begun = await go();
    // Refused, nothing will take the held state's place; begun, the new
    // ceremony's first event has normally arrived by the time the call has
    // answered and the unlock methods are read again, and a hold that
    // outlived it would leave a dialog no button can end.
    if (!begun) release();
    else setTimeout(() => holdUntilNew && release(), 2000);
  }

  function release() {
    holdUntilNew = false;
    held = null;
  }

  // The cancelled ceremony is gone from the store, or ended, or 8 s have
  // passed: the lock screen's own bound for the same wait (Lock.svelte
  // untilLocked).
  function untilDropped(): Promise<void> {
    return new Promise((resolve) => {
      const started = Date.now();
      const tick = () => {
        if (!store.ceremony || store.ceremonyIsOver) return resolve();
        if (Date.now() - started > 8000) return resolve();
        setTimeout(tick, 50);
      };
      tick();
    });
  }

  // ---- The remove dialog (ruled 2026-10-27) -------------------------------

  // It asks why, and the answer that re-keys is pre-selected; the backup's
  // tick follows the answer (BackupChoice), set again each time it opens.
  let removeAnswer = $state<Answer>("rekey");
  let removeTicked = $state(false);

  function openRemove(m: SlotView) {
    removeAnswer = "rekey";
    removing = m;
  }

  // Every password is named "Password", so two cards can read alike: the
  // title then says when this one was added, as its card does.
  function removeAdded(m: SlotView): string | undefined {
    return store.slots.some((s) => s.recipientId !== m.recipientId && s.label === m.label) ? date(m.createdAt) : undefined;
  }

  // The recovery keys left besides the one removed: the limit's second
  // sentence speaks of them, so it is drawn only while there is one.
  function otherRecovery(m: SlotView): number {
    return store.slots.filter((s) => s.type === "recovery" && s.recipientId !== m.recipientId).length;
  }

  // The form's submit: the dialog closes, the Save dialog asks for the
  // backup's file when the tick is on (cancelled, nothing is removed), the
  // act is recorded for its end's next step, then it begins.
  async function doRemove() {
    const target = removing;
    if (!target) return;
    const rekey = removeAnswer === "rekey";
    const tick = removeTicked;
    removing = null;
    let path = "";
    if (tick) {
      path = await pickBackup();
      if (!path) return;
    }
    const id = target.recipientId;
    store.noteAct({ act: "remove", type: target.type, rekey, fromFollow: false });
    await beginWith((b) => Keys.RemoveSlot(id, rekey, b), path);
  }

  // ---- The password's dialogs ----------------------------------------------

  function openEntangle(mode: "on" | "change" | "off", follow = false) {
    fromFollow = follow;
    entangling = mode;
  }

  // ---- Replace recovery keys… ------------------------------------------------

  // Greyed with its reason as the title (APP.md §6, Controls): locked,
  // Tampered, no recovery key, or every unlock method a recovery key.
  const replaceWhy = $derived(replaceGreyed({ unlocked, tampered, recoveryKeys, setupNeeded: !!st?.setupNeeded }));
  // from: the end whose next step opened the dialog, whose committed facts
  // it reads (lib/rekey.ts replaceFacts).
  let replaceFrom = $state<{ after: AfterFacts; changed: boolean } | undefined>(undefined);

  function openReplace(from?: { after: AfterFacts; changed: boolean }) {
    fromFollow = !!from;
    replaceFrom = from;
    replacing = true;
  }

  // Keep mode's caption: the old key the new one replaces, by its name and
  // ID, so the new sheet goes where the old one is (the new key at
  // keepIndex replaces oldIds[keepIndex-1]).
  function replacesLine(cer: CeremonyState): string {
    const old = cer.oldIds?.[cer.keepIndex - 1] ?? "";
    const label = store.slots.find((s) => s.type === "recovery" && s.recoveryId === old)?.label ?? ceremonyCopy.recoveryKey;
    return revealCopy.replaces(label, old);
  }

  // ---- The end's next step ---------------------------------------------------

  // The command a next step's button opens, in the verb table's words.
  function followLabel(f: FollowUp): string {
    return f.act === "add" ? commandCopy.addUnlockMethod : f.act === "change" ? vaultCopy.change : commandCopy.replaceRecoveryKeys;
  }

  // The press closes the ceremony dialog and opens the next act's, which
  // goes on in the same chain: the Add dialog, the password's Change…, or
  // the replacement's with the end's committed facts and the chain's
  // changed (ruled 2026-10-27).
  function openFollow(f: FollowUp, after: AfterFacts | null | undefined) {
    const changed = store.chain?.changed ?? false;
    closeCeremony();
    heldFollow = null;
    if (f.act === "add") openAdd(true);
    else if (f.act === "change") openEntangle("change", true);
    else openReplace(after ? { after, changed } : undefined);
  }

  // The end's next step for the ceremony on the screen (store.followUp,
  // decided from its committed facts and the chain, never from the cards).
  const follow = $derived.by(() => {
    const f = store.followUp;
    const ended = c;
    if (!f || !ended) return undefined;
    return { text: f.text, label: followLabel(f), onpress: () => openFollow(f, ended.after) };
  });

  // A recovery key's enrolment ends in the recovery key's dialog, which
  // App draws above every route, and not in this page's ceremony dialog
  // (internal/app/keys.go BeginEnroll). The next step its end carries — an
  // enrolment begun from an end's "add" that leaves the vault with recovery
  // keys alone still says to add the new YubiKey — is kept here and said in
  // the page's notice slot once that dialog has closed, with its button. A
  // new act, the button, its close and a lock drop it.
  let heldFollow = $state<{ f: FollowUp; after: AfterFacts } | null>(null);
  $effect(() => {
    const cer = store.ceremony;
    if (!cer || cer.kind !== "enroll" || cer.step !== CeremonyStep.StepRecovery || cer.promptId || !cer.after) return;
    const f = store.followUp;
    if (f) heldFollow = { f, after: cer.after };
  });
  $effect(() => {
    if (!unlocked) heldFollow = null;
  });
  const heldShown = $derived(heldFollow && unlocked && !store.ceremony ? heldFollow : null);

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

  // The file a backup is writing, for its done line: said in the foot
  // once its dialog is closed on a Done that wrote it — a plain export's
  // "Backup saved", or an act's that took the backup after its change —
  // with the way to the file (direction 4; ruled 2026-10-27).
  let backupTo = "";

  // An export has no next step: the chain ends with it.
  async function exportBackup() {
    const p = await pickBackup();
    if (!p) return;
    store.noteAct(null);
    if (await beginWith((b) => Keys.ExportBackup(b), p)) await store.refreshLastExport();
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

  // The backups' status (direction 9, ruled 2026-10-18): the last backup
  // against what it lacks, by lib/backup.ts. The counts are read only
  // from an unlocked registry that is not Tampered (the core answers them
  // 0 otherwise), so while Locked or Tampered the line says the date
  // alone — or that no backup is recorded — and claims nothing about what
  // a backup holds. The date is relative: recency is the point.
  const backupAt = $derived(store.backup?.at ?? store.lastExportAt);
  const bline = $derived(
    backupLine(store.backup ?? (backupAt ? { at: backupAt, archives: 0, missing: 0, rekeyed: 0, vaultRekeyed: false, keysReplaced: false } : null), unlocked && !tampered && !!store.backup, backupAt ? sinceText(backupAt, store.now) : ""),
  );
  const addAccent = $derived(unlocked && !tampered && !bline.suggestExport);

  // The Re-key dialog: the backup is the shared block's, ticked whenever a
  // recovery key is there to back up, and taken after the re-key (ruled
  // 2026-10-27: a backup taken before would bring the old key back). It
  // asks and never refuses, and never keeps a copy of the vault (APP.md
  // §13, DESIGN trap 29). One act, so Start again after a failed backup is
  // the re-key and its backup again, to the same file.
  let rotateBackup = $state(false);

  function openRotate() {
    rotating = true;
  }

  async function doRotate() {
    const tick = rotateBackup;
    rotating = false;
    let path = "";
    if (tick) {
      path = await pickBackup();
      if (!path) return;
    }
    store.noteAct({ act: "rotate", rekey: true, fromFollow: false });
    await beginWith((b) => Keys.RotateNow(b), path);
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
    if (backupTo && backupSavedOnClose(ended)) store.showDone(backupDone(backupTo));
    backupTo = "";
    held = null;
    store.dismissCeremony();
    void store.refreshSlots();
    void store.refreshEntangled();
  }

  // While the switch holds the last state, the next ceremony is about to
  // take its place: Escape waits for it.
  function escapeCeremony() {
    if (held) return;
    if (store.ceremonyIsOver) closeCeremony();
    else void Vault.CancelUnlock();
  }

  // The ceremony on the panel: the store's, or the one the switch holds
  // until the new one's first event.
  const shownC = $derived(c ?? held);
  $effect(() => {
    if (c && holdUntilNew) release();
  });
  // A lock closes every dialog, the held one too.
  $effect(() => {
    if (!unlocked && held) release();
  });
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
  <!-- The notice slot (direction 4, phase 4b; NoticeSlot): the banners at
       the top of the column in their tiers' order, each opening and
       closing its height, and the InfoBars in flow under them, each
       opening its height as the lock screen's do: a floating bar covered
       the column's first heading and its commands (the final shoot of
       phase 4b), and a page with no list has nothing to hold still. The
       backups line stays in its card: it is the card's fact, not the
       page's state. -->
  <NoticeSlot flow bind:this={slot}>
    {#if tampered}
      <!-- First on the page (direction 9; APP.md §6, The Tampered banner): the danger tier, with
           the fix every greyed change here points at. -->
      <div class="fold" transition:unfold><TamperedBar reason={st?.tamperedReason} /></div>
    {/if}
    {#if store.replaceNotice}
      <!-- The keys a replacement showed and did not replace with (ruled
           2026-10-27): named until closed or until the next replacement
           begins, across a lock, since an ID is not secret. -->
      {@const rn = store.replaceNotice}
      <div class="fold" transition:unfold><div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span class="grow">{rn.known ? replaceCopy.notAdded(rn.ids) : replaceCopy.unknown(rn.ids)}</span><button type="button" class="x20" title={noticeCopy.close} aria-label={noticeCopy.close} onclick={() => store.dismissReplaceNotice()}><svg class="i i-14" aria-hidden="true"><use href="#i-x" /></svg></button></div></div>
    {/if}
    {#if heldShown}
      <!-- A recovery key's enrolment's next step, said once its dialog has
           closed; it says what the setup line would, and more. -->
      {@const hf = heldShown}
      <div class="fold" transition:unfold><div class="bar"><svg class="i i-14"><use href="#i-info" /></svg><span class="grow">{hf.f.text}</span><button type="button" class="btn sm" onclick={() => openFollow(hf.f, hf.after)}>{followLabel(hf.f)}</button><button type="button" class="x20" title={noticeCopy.close} aria-label={noticeCopy.close} onclick={() => (heldFollow = null)}><svg class="i i-14" aria-hidden="true"><use href="#i-x" /></svg></button></div></div>
    {:else if st?.setupNeeded && unlocked}
      <div class="fold" transition:unfold><div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span class="grow">{vaultCopy.setupLine(recoveryKeys)}</span></div></div>
    {/if}
    {#if !unlocked}
      <!-- Locked is the safe state: the plain bar, no tier's colour
           (direction 9, ruled 2026-10-18). -->
      <div class="fold" transition:unfold><div class="bar line"><svg class="i i-14"><use href="#i-lock" /></svg><span class="grow">{vaultCopy.lockedLine}</span><button type="button" class="btn sm accent" onclick={() => store.unlock()}>{commandCopy.unlock}</button></div></div>
    {/if}
  </NoticeSlot>
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
          <button type="button" class="btn sm" class:accent={addAccent} disabled={!unlocked || tampered} title={greyed} onclick={() => openAdd()}><svg class="i i-14"><use href="#i-plus" /></svg>{commandCopy.addUnlockMethod}</button>
          <button type="button" class="btn sm" disabled={!unlocked || tampered} title={greyed} onclick={openRotate}><svg class="i i-14"><use href="#i-rotate" /></svg>{commandCopy.rekeyVault}</button>
          <!-- Every recovery key at once, so here and not on a card, whose
               button would promise to replace that one (ruled 2026-10-27). -->
          <button type="button" class="btn sm" disabled={!!replaceWhy} title={replaceWhy} onclick={() => openReplace()}><svg class="i i-14"><use href="#i-recovery" /></svg>{commandCopy.replaceRecoveryKeys}</button>
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
                <button type="button" class="btn sm" disabled={!unlocked} title={unlocked ? undefined : greyed} onclick={() => { const id = m.recipientId; store.noteAct(null); void begin(() => Keys.RevealRecoveryKey(id)); }}>{commandCopy.showRecoveryKey}</button>
              {/if}
              <button type="button" class="btn sm" disabled={!m.removable || !unlocked || tampered} title={removeTitle(m)} onclick={() => openRemove(m)}>{commandCopy.removeUnlockMethod}</button>
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
              <button type="button" class="btn sm" disabled={!unlocked || tampered} title={greyed} onclick={() => openEntangle("change")}>{vaultCopy.change}</button>
              <button type="button" class="btn sm" disabled={!unlocked || tampered} title={greyed} onclick={() => openEntangle("off")}>{vaultCopy.turnOff}</button>
            {:else}
              <button type="button" class="btn sm" disabled={!unlocked || tampered || !ent?.canEnable} title={greyed ?? (entangleBlocked ? vaultCopy.passwordBlockedShort : undefined)} onclick={() => openEntangle("on")}>{vaultCopy.turnOn}</button>
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
        <input id="ak-label" class="input" bind:value={addLabel} placeholder={addKind === "token" ? dialogCopy.addUnlockMethod.keyPlaceholder : undefined} use:fieldMenu />
      </div>
    {/if}
    {#if addKind === "token" && ent?.on}
      <!-- The vault's switch is inherited, never chosen per key: the new
           key is wrapped from the kept K_P offline (APP.md §13). -->
      <p>{dialogCopy.addUnlockMethod.passwordInherited}</p>
    {/if}
    {#snippet actions()}
      <button type="button" class="btn accent" disabled={!addLabel && addKind === "recovery"} title={!addLabel && addKind === "recovery" ? dialogCopy.addUnlockMethod.needName : undefined} onclick={() => { adding = false; const kind = addKind, label = addKind === "password" ? "Password" : addLabel; startEnroll(kind, label); addLabel = ""; }}>{dialogCopy.addUnlockMethod.primary}</button>
      <button type="button" class="btn" onclick={() => (adding = false)}>{dialogCopy.cancel}</button>
    {/snippet}
  </Dialog>
{/if}

{#if removing}
  {@const target = removing}
  {@const q = questionOf("remove", target.type)}
  <!-- The dialog template (strings.ts): the consequence, then what still
       opens; the mechanism behind "How this works" (ruled 2026-10-17). A
       form that asks why first (ruled 2026-10-27): it opens on the checked
       answer, Enter presses the primary, Escape is Cancel. Its height does
       not change with the answer or the tick, so it stays centred. -->
  <Dialog title={dialogCopy.removeUnlockMethod.title(target.label, removeAdded(target))} onclose={() => (removing = null)} onsubmit={() => void doRemove()}>
    <p>{dialogCopy.removeUnlockMethod.body(target.label, target.type)}</p>
    {#if (st?.retiredCopies ?? 0) > 0}
      <!-- Replaced copies keep their unlock methods (APP.md §13). -->
      <p>{dialogCopy.removeUnlockMethod.retired(st?.retiredCopies ?? 0, target.label)}</p>
    {/if}
    {#if lastProtector(store.slots, target)}
      <p>{dialogCopy.removeUnlockMethod.lastProtector}</p>
    {/if}
    <RekeyQuestion legend={q.legend} rekey={q.rekey} keep={q.keep} bind:answer={removeAnswer} />
    <!-- The limit of a re-key and what is left to do, whichever the answer:
         a sentence, not a gate. -->
    <p class="infoline"><svg class="i i-14" aria-hidden="true"><use href="#i-info" /></svg><span>{rekeyLimitOf({ act: "remove", type: target.type, passwordOn: !!ent?.on, otherLeft: otherRecovery(target) })}</span></p>
    <BackupChoice act="remove" rekey={removeAnswer === "rekey"} removing={target.type} label={target.label} bind:ticked={removeTicked} />
    <details class="how"><summary>{dialogCopy.howThisWorks}</summary><p>{dialogCopy.removeUnlockMethod.how(target.label, target.type)}</p></details>
    {#snippet actions()}
      <!-- The destructive act, so danger-fill, naming what the press does,
           the backup included while ticked. -->
      <button type="submit" class="btn danger-fill">{primaryOf("remove", removeAnswer === "rekey", removeTicked)}</button>
      <button type="button" class="btn" onclick={() => (removing = null)}>{dialogCopy.cancel}</button>
    {/snippet}
  </Dialog>
{/if}

{#if renaming}
  <RenameVaultDialog name={vaultName} onclose={() => (renaming = false)} />
{/if}

{#if rotating}
  <!-- The body says what authorises the act: one unlock method proves it
       is you, a YubiKey wherever the vault has one (keys.go
       acquireUnlocked). -->
  <Dialog title={dialogCopy.rekeyVault.title} onclose={() => (rotating = false)}>
    <p>{dialogCopy.rekeyVault.body(!!st?.hasHardwareSlot)}</p>
    <BackupChoice act="rotate" rekey={true} bind:ticked={rotateBackup} />
    {#snippet actions()}
      <button type="button" class="btn accent" onclick={() => void doRotate()}>{primaryOf("rotate", true, rotateBackup)}</button>
      <button type="button" class="btn" onclick={() => (rotating = false)}>{dialogCopy.cancel}</button>
    {/snippet}
  </Dialog>
{/if}

{#if entangling && ent}
  <EntangledDialog mode={entangling} onclose={() => (entangling = "")} {begin} {beginWith} pick={pickBackup} {fromFollow} />
{/if}

{#if replacing}
  <ReplaceDialog onclose={() => (replacing = false)} {beginWith} pick={pickBackup} from={replaceFrom} minutes={replaceMinutes(st)} />
{/if}

{#if c && c.step === CeremonyStep.StepRecovery && !c.promptId && c.slotLabel}
  <!-- the recovery key is revealed by App, above every route -->
{:else if c && c.step === CeremonyStep.StepKeepKey && c.promptId && c.slotLabel}
  <!-- A replacement's new key, shown by the recovery key's dialog in keep
       mode in the act's dialog's place, one after another (keyed on the
       one-time URL, as App keys its mount); its Cancel ends the act. -->
  {#key c.slotLabel}
    <RecoveryReveal url={c.slotLabel} kind={c.kind} vaultName={store.vaultName} recoveryId={c.recoveryId ?? ""} keep={{ promptId: c.promptId, index: c.keepIndex, count: c.keepCount, replaces: replacesLine(c) }} oncancel={() => void Vault.CancelUnlock()} />
  {/key}
{:else if shownC}
  <Dialog title={ceremonyTitleOf(shownC, recoveryKeys)} onclose={escapeCeremony} dismissable={store.ceremonyIsOver && !held}>
    <CeremonyPanel c={shownC} onclose={closeCeremony} onagain={again ? () => void again?.() : undefined} {follow} onrecovery={shownC.kind === "enroll" && toRecovery ? () => void switchToRecovery() : undefined} />
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
  /* Three acts beside the title and its count (ruled 2026-10-27): a
     narrower window wraps them under the title rather than overflowing. */
  .ks-head { flex-wrap: wrap; row-gap: var(--s2); }
  .head-acts { margin-left: auto; display: flex; flex-wrap: wrap; gap: var(--s2); align-self: center; }
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

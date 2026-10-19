// The page's state: what the core reported, applied in sequence order, and
// the little the page adds (route, selection, toasts). Every event is
// subscribed in boot() before the first fetch (APP.md §2.4).
import { Events } from "@wailsio/runtime";
import { Archive, Archives, Keys, Settings, Shell, Vault, errorOf, Code, submitSecret } from "./api";
import type { ArchiveDetails, ArchiveStat, ArchiveSummary, BackupStatus, CeremonyState, EntangledState, IncomingRecord, OpenRequest, OpView, SettingsView, SlotView, VaultStatus } from "./api";
import { CeremonyStep, VaultState } from "./api";
import { codeText, moreNotOpened, openElsewhereText, purgedText, warningCopy } from "./strings";
import { methodAfter, outcomeAfter } from "./outcome";
import { NOT_PASSED, explicitMethod, passedAfter } from "./lockscreen";
import type { Passed } from "./lockscreen";
import { delay, SETTLE } from "./motion";
import { ROOT_ID, retryChain, shownDir, wentName } from "./tree";
import { hasTrouble } from "./results";
import { hasConflicts } from "./conflicts";
import { hasRefusals } from "./refused";
import { DEFAULT_SORT, sortString } from "./sort";
import type { SortState } from "./sort";
import { clickRow, emptySelection, selectAll, survive, toggleRow } from "./selection";
import type { Modifiers, Selection } from "./selection";
import { PAGE_LIMIT, applyReply, hasMore, liveIds, nextOffset } from "./paging";
import type { Listing } from "./paging";
import { opErrorLine } from "./ops";
import { clearsDone, doneLine, nextDone, sayDone } from "./foot";
import type { DoneLine, DoneTurn } from "./foot";
import type { Outcome } from "./outcome";
import { answer as answerDrag, baseName, beginFlight, land, ownDrop } from "./dragout";
import type { Flight, Landed, PendingMove, Verdict } from "./dragout";
import type { Decision } from "./closing";
import { droppedArchives } from "./archlist";
import { toLeave } from "./leave";
import { tamperedShown, tier } from "./notices";
import { dialogIsUp } from "./dialogs";

// How long a done line holds in the foot (direction 4).
const DONE_MS = 6000;

export type Route = "archives" | "archive" | "keys" | "settings" | "lock";

// How long a self-drop's flight waits for the WebView's drop once the call
// has answered (settleDrag): the two travel different roads and either may
// arrive first, and a release over the window's frame brings no drop at
// all.
export const SELF_DROP_GRACE = 1000;

export interface Toast {
  id: number;
  text: string;
  kind: "info" | "error";
}

// The shell's file drop (APP.md §3, Shell): the paths, whether each one is
// a directory — the page cannot stat a path, and a directory handed to
// AddFiles is one failed outcome — and the target the page named, the
// archive and the directory *id* it is showing (`data-archive-id` /
// `data-dir-id`), never a path.
export interface Drop {
  paths: string[];
  isDir?: boolean[] | null;
  archiveId: string;
  dirId: string;
}

// The archives.changed payload (APP.md §13): the names the purge dropped
// at the end of an unlock, empty on every other change. Wails generates
// no model for it — it rides no bound signature — so it is declared here.
export interface ArchivesChanged {
  purged?: string[] | null;
}

// A record's staged Rename and Description, keyed by archive id, so the
// save bar survives a visit to another page. Dirty is derived by diffing
// against the record, never stored (APP.md §6) — base is the record's own
// values as they were when the draft was staged, and the page re-stages it
// whenever the record moves under an untouched draft, so the diff is
// always against what the record holds now.
export interface RecordDraft {
  base: { name: string; description: string };
  name: string;
  description: string;
}

class Store {
  status = $state<VaultStatus | null>(null);
  ceremony = $state<CeremonyState | null>(null);
  // ceremonyPassed: whether the running ceremony has left its opening
  // WaitingForKey, followed here on every event accepted, so that a wait
  // for a YubiKey after it is drawn as one however fast the events come;
  // with it, whether a touch is behind the ceremony with nothing asked
  // since, so that the green holds through what follows the touch, and
  // whether a derivation is behind it (lib/lockscreen passedAfter; APP.md
  // §6). Raw: replaced whole.
  ceremonyPassed = $state.raw<Passed>(NOT_PASSED);
  // recoveryDraft: the eight groups as typed, kept across the derivation
  // step so that a key the core refused comes back for correction
  // (APP.md §6); dropped when the ceremony ends.
  recoveryDraft = $state<string[] | null>(null);
  // The password chosen in a create, restore or finish-setup dialog
  // (APP.md §6, ruled 2026-10-17): typed there, in two fields, and held
  // here from the dialog's Continue until the ceremony of that kind asks
  // its Choose prompt — the first way in's password, which the core asks
  // before the YubiKey is set up (§3) — then sent on that prompt over the
  // raw channel and dropped. This is the only place it lives meanwhile:
  // the dialog that typed it has closed. Not $state, since nothing
  // renders it. It is dropped at every other end too: the ceremony
  // ending first (failed, cancelled, blocked, done), the call that would
  // have started it refused (dropChosen), and the panel dismissed for
  // another ceremony (dismissCeremony).
  private chosen: { kind: string; value: string } | null = null;
  // chosenSent: the prompt the page answered from the dialog. Card 2
  // shows no field for it, so a password vault never shows the Choose
  // prompt in the normal case; a refusal of the answer (secret.refused)
  // clears it, the prompt stands, and the panel's password step asks it as before.
  chosenSent = $state("");
  ops = $state<Record<string, OpView>>({});
  archives = $state<ArchiveSummary[]>([]);
  // filesCheck: what the last *Find missing files* pass of this unlock
  // answered (APP.md §13), from either page that runs it; null until one
  // has, and dropped whenever the vault leaves Unlocked, since the
  // presence it measured is the session's and goes with it. The Vault
  // page's "Archive files:" line claims "all present" only on its word
  // (lib/vaultfiles.ts): a record no pass has measured carries no note,
  // so an empty count of notes is not by itself a presence.
  filesCheck = $state<{ probed: number; missing: number } | null>(null);
  showHidden = $state(false);
  route = $state<Route>("archives");
  current = $state<string | null>(null);
  // The open archive the page let go of with its *Close* while something
  // still read it (APP.md §2.3): the core lists it in VaultStatus.Opened
  // until the last body or its own operation ends, and the page stands
  // meanwhile with *Force close* in its head (ruled 2026-10-18). Nothing
  // more is read of it — every page-facing method answers
  // archive.not_open — and it is dropped once the status stops naming it.
  draining = $state<string | null>(null);
  // The same for an archive the page does not hold: one left by its rail
  // row's × or the inspector's *Close* while a reader or an operation
  // still held it. The inspector offers *Force close* for it and the rail
  // row loses its ×, since a second Leave would do nothing (APP.md §2.3).
  // Pruned as the status stops naming them.
  lingering = $state<string[]>([]);
  // Opens on their way to the core: while one travels, a list or a status
  // read before it landed cannot say that the archive the page holds has
  // closed (closedUnderUs).
  private opensInFlight = 0;
  private openGen = 0;
  // Whether a status has named the page's archive since it became current:
  // only then does a status that stops naming it mean it closed.
  private seenOpened: string | null = null;
  stat = $state<ArchiveStat | null>(null);
  // The open archive's file path, from its registry row: ArchiveStat does
  // not carry one, and the extract dialog is prefilled from the archive's
  // own folder (APP.md §3, ruled 2026-09-10). Kept here so the field is
  // right even while the vault is locked and the list is not being read.
  currentPath = $state("");
  // The listing held: every row from offset zero to what has arrived, at
  // one Seq, paged in as the list scrolls (APP.md §2.4, lib/paging.ts).
  page = $state<Listing | null>(null);
  // The directory the page is showing, as an id: the all-zero id is the
  // archive's root (APP.md §3). The heading is the page's own Crumbs,
  // never built from this.
  dirId = $state(ROOT_ID);
  // The list's order (APP.md §6, lib/sort.ts): the column, its direction
  // and the Name column's own as the tie-break. Kept while the archive is
  // open; an archive opens on name.
  sort = $state<SortState>(DEFAULT_SORT);
  // The selection (APP.md §6, lib/selection.ts): the ticked ids and the
  // anchor Shift ranges from. Kept here rather than on the page so that a
  // newer listing can prune it — the ids that still exist survive — and
  // so that entering a folder and going up clear it, wherever that was
  // asked from. Every change goes through the setter, which counts them:
  // a reply that arrives for a selection since changed is dropped by the
  // count (selectFolder).
  #sel = $state<Selection>(emptySelection());
  private selGen = 0;

  get sel(): Selection {
    return this.#sel;
  }

  set sel(s: Selection) {
    this.#sel = s;
    this.selGen++;
  }
  // The row to focus once the listing lands: after going up, the folder
  // just left (APP.md §6).
  focusAfter = $state<string | null>(null);
  // The last batch operation of the open archive that did not go through
  // whole (a failed or skipped item): the page shows what happened once,
  // and clears it (APP.md §3, FileOutcome).
  results = $state<OpView | null>(null);
  // The done line (direction 4, phase 3): how the last operation ended,
  // said in the layer's foot on whichever page is up, for DONE_MS or until
  // the next press in a page's body (LayerFoot). One at a time: an ending
  // that arrives while another is up waits its turn (lib/foot.ts sayDone),
  // so two operations ending together are both said. The id is the
  // cross-fade's key.
  done = $state<(DoneLine & { id: number }) | null>(null);
  private doneWaiting: (DoneLine & { id: number })[] = [];
  private doneId = 0;
  private doneTimer: ReturnType<typeof setTimeout> | undefined;
  // The extracts whose conflicts the page has answered or dismissed, by op
  // id. The question itself is never stored: it is derived from the ops
  // (conflictQuestion), so it survives the page's remount across the lock
  // scene and cannot be lost to a race between Extract's return and the
  // op's end (APP.md §3, ruled 2026-09-10).
  handledConflicts = $state<Record<string, true>>({});
  // The extracts whose refused names the page has answered, the same way
  // (refusedQuestion, APP.md §3, ruled 2026-09-10).
  handledRefusals = $state<Record<string, true>>({});
  // The `names` an extract was issued with, by op id — what a *Shorten* or
  // *Rename…* chose — kept while the op can still ask, so that a conflict
  // the renamed file then meets is re-issued under the same name and not
  // the record's own, which the destination has already refused (APP.md
  // §3, the review's finding 7). Dropped with the op.
  extractNames: Record<string, Record<string, string>> = {};
  slots = $state<SlotView[]>([]);
  // The vault's entangled password (APP.md §13): one switch for the whole
  // vault, its On known while Locked, its CanEnable only while Unlocked.
  entangled = $state<EntangledState | null>(null);
  // The selected record read whole (Archives.Details), refreshed when the
  // selection changes and not when the modal opens (APP.md §13).
  details = $state<ArchiveDetails | null>(null);
  // The save bar's staged Rename and Description, by archive id.
  recordDraft = $state<Record<string, RecordDraft>>({});
  // The last backup of this vault, Unix seconds, 0 = never: the core
  // applied the four checks (APP.md §13). It chooses wording and
  // pre-selects the rotate dialog's tick; it gates nothing.
  lastExportAt = $state(0);
  // What the last backup lacks (Vault.BackupStatus; direction 9): read
  // wherever lastExportAt is, and again when the archives change — a
  // create, a re-key or a forget moves its counts. The Vault page's
  // status line is lib/backup.ts's; like lastExportAt it gates nothing.
  backup = $state<BackupStatus | null>(null);
  // The Tampered banner's *Replace with a copy…* on a page (direction 9):
  // the import runs from the lock screen, so the page locks and goes
  // there, and the lock screen at rest takes this once and opens its
  // import dialog (takeReplaceAsked), as the Archive page takes addAsked.
  replaceAsked = $state(false);
  // The Vault page's *Test that a backup opens…*: the file picked, for
  // the lock screen to check once it is at rest (askTestBackup).
  testAsked = $state("");
  // The Archives page's summary's *Export…* (direction 6): the export is
  // the Vault page's, where its ceremony and its receipt are drawn, so the
  // summary goes there and the page takes this once and starts it
  // (askExport, takeExportAsked).
  exportAsked = $state(false);
  // *Import archives from a backup…*'s merge dialog, by the file picked
  // for it (direction 10, phase 4a): mounted above every route
  // (App.svelte), so the Vault page opens it as the Archives page does. A
  // lock closes it: the core drops the merge's handle with every held key.
  merging = $state("");
  // A merge handle, the records it read and whether that file fails its
  // own slot-region hash, until MergeRecords consumes it, the dialog
  // discards it, or a lock trigger drops it.
  merge = $state<{ handle: string; records: IncomingRecord[]; tampered: boolean } | null>(null);
  // New archive… ends on the new archive's empty root with the Add menu
  // open (APP.md §13, ruled 2026-10-14): openNew names the archive just
  // before the open, and that archive's page takes it once as it mounts
  // (takeAddAsked). A refused open asks for nothing.
  addAsked: string | null = null;
  // The archive the Archive page let go of on the consent of its own
  // Delete archive… (APP.md §13, ruled 2026-10-14), while that dialog still
  // stands over the page to say how the act ended: nothing more is read of
  // it — a Stat would answer archive.not_open and drop the page, and the
  // dialog with it, mid-act. The dialog's close leaves the page, and
  // dropArchive clears this.
  releasedForDelete: string | null = null;
  // Opening an archive from Explorer (APP.md §14). Three fields:
  //
  //   - openWhenUnlocked: the path OpenPath answered vault.needs_unlock
  //     for. The page shows the lock scene and finishes the open on the
  //     Unlocked transition, once (applyStatus).
  //   - keyNotInVault: the path of a file that *is* an Enfold archive and
  //     whose keys no record here holds. The dialog over every route
  //     offers Import records… and Close; the file is not touched.
  //   - importRecordsAsked: that dialog's Import records… handed to the
  //     Archives page, which owns the picker and the merge dialog.
  openWhenUnlocked = $state("");
  keyNotInVault = $state<string | null>(null);
  importRecordsAsked = $state(false);
  // The request openWhenUnlocked belongs to: the unlock's retry is that
  // request's, and a newer one accepted meanwhile drops it.
  private openWhenUnlockedSeq = 0;
  // The file opened from Explorer whose key was not in this vault, kept
  // when its dialog's *Import archives from a backup…* was taken: once the
  // merge that followed succeeds and its dialog closes, the file is opened
  // again, so the file the user double-clicked ends up open (direction 3,
  // phase 4a). A cancelled picker or merge, a lock and a newer open forget
  // it.
  private importFor = "";
  // The archive opened last, per vault, for the life of the process
  // (direction 3, phase 4a): the vault's path and the archive's id. It is
  // memory only — never settings.json, never any file: a name is the
  // registry's, and even an id points at one .efd, since the envelope's
  // archive_id is readable without a key, so a file on disk would record
  // which archive was used last. A vault of another path drops it.
  private lastOpened = $state<{ vault: string; id: string } | null>(null);
  // The Archives list's one ask after an unlock: select and focus the row
  // of the archive opened last, so Enter opens it (takeFocusAsked).
  focusArchiveAsked = $state<string | null>(null);
  settings = $state<SettingsView | null>(null);
  toasts = $state<Toast[]>([]);
  // The last ceremony that ended with something to say on the lock screen
  // (an import, a check of a backup, a create): it outlives the ceremony
  // object, which the reveal dismisses.
  outcome = $state<Outcome | null>(null);
  // The way in the running ceremony took — token | password | recovery:
  // the lock screen's wording follows it for the rest of the ceremony,
  // and not whether a secret was asked (APP.md §2.2, §13).
  unlockMethod = $state("");
  drop = $state<Drop | null>(null);
  // The close question (APP.md §2.4, ruled 2026-09-13): the shell cancelled
  // a close — the button, Alt+F4, the taskbar's close — and asked the page
  // to ask. closeBusy is the answer on its way back to the shell; the
  // dialog stays put until the window goes.
  closeAsked = $state(false);
  closeBusy = $state(false);
  // The drag out of the window in flight (APP.md §3, lib/dragout.ts): the
  // ids the gesture took and the folder they left, from the press-and-move
  // until Shell.DragOut has answered and, for a self-drop, the WebView's
  // drop has said where it landed. A second gesture while one is in flight
  // is ignored; a Leave or Close drops it with the page.
  dragOut = $state<Flight | null>(null);
  // The Move a self-drop asks for, handed to the page — which performs it
  // and says a refusal against the target — the way `drop` is handed over.
  selfDrop = $state<PendingMove | null>(null);
  // The drag's folder the last drag out named (DragOutResult.folder, the
  // manifest at its top and every path handed out under its items): a
  // self-drop's own drop can reach the page after the call has answered
  // and the flight is over, and its paths — under this folder, files that
  // never exist — must not be an add.
  private lastDragFolder = "";
  private dragGrace: ReturnType<typeof setTimeout> | undefined;
  now = $state(Date.now());
  // booted: the first Status() call has completed — a reply, or a failure
  // with a status accepted from an event meanwhile — and the page draws
  // the scene the newest accepted status names; until then it draws
  // nothing of the vault's, never the lock scene (APP.md §2.4, ruled
  // 2026-09-10). An event that lands before the reply is applied by its
  // Seq like any other but does not open the gate (the review's finding
  // 14: a Locked event drew the lock scene while the reply still to come
  // said Unlocked).
  booted = $state(false);
  // bootFailed: the first Status() failed with nothing accepted meanwhile —
  // what it said, the sentence of the boot failure's panel (APP.md §2.4;
  // direction 9). bootCode and bootFailedAt are the code it failed with
  // and when (Unix ms), which *Copy details* copies.
  bootFailed = $state("");
  bootCode = $state("");
  bootFailedAt = $state(0);

  private seq = 0;
  private ceremonySeq = 0;
  // The last shell open request acted on (APP.md §14): the shell numbers
  // them from one, and a request is opened once whether it came from the
  // slot at boot or from `shell.open` — a second launch fills both, since
  // the window it recreates may have no page listening yet.
  private lastShellSeq = 0;
  // The newest open asked for, from the shell or from the page's own Open
  // file… and .efd drop (ruled 2026-10-14): numbered here and not by the
  // shell, whose numbers the page's own opens cannot share, and what an
  // older open's answer is dropped against.
  private lastOpenSeq = 0;
  private archiveSeq: Record<string, number> = {};
  private toastId = 0;
  private lastActivity = 0;

  // remembered: the archive opened last in this vault, by id; empty when
  // none was opened in this process or the vault is another one.
  get remembered(): string {
    const l = this.lastOpened;
    return l && this.status && l.vault === this.status.path ? l.id : "";
  }

  // previewPane: the Archive page's View › Preview pane (Settings.previewPane,
  // true until the settings are read and whenever they say nothing).
  get previewPane(): boolean {
    return this.settings?.previewPane ?? true;
  }

  get unlocked(): boolean {
    return this.status?.state === VaultState.StateUnlocked;
  }

  // tampered is whether the Tampered banner is drawn, the one fact the
  // lock screen, Archives, an archive's page, Vault and Settings share
  // (APP.md §6, The Tampered banner; lib/notices.ts): a lock clears
  // VaultStatus.tampered but keeps the vault.tampered warning, and the
  // banner stays until the vault is replaced or unlocks clean.
  get tampered(): boolean {
    return tamperedShown(this.status);
  }

  // vaultName is the vault's name as the user last gave it (direction 10;
  // APP.md §3, Settings): the setting *Rename…* writes, then the status's.
  // The core moves the status's name with the setting and emits it, so the
  // two agree; the setting first only covers the moment between the write
  // and that status. Empty when neither has one.
  get vaultName(): string {
    return this.settings?.displayName || this.status?.displayName || "";
  }

  // checkFiles is *Find missing files* (APP.md §13), the Vault page's
  // alone since phase 4a: the core answers once its pass has ended, and the answer is
  // kept for the Vault page's line (filesCheck) — only while the vault is
  // still Unlocked, since a lock meanwhile ended the session it measured.
  async checkFiles(): Promise<{ probed: number; missing: number }> {
    const r = await Archives.CheckFiles();
    if (this.unlocked) this.filesCheck = { probed: r.probed, missing: r.missing };
    return r;
  }

  get runningOps(): OpView[] {
    return Object.values(this.ops).filter((o) => !o.finished);
  }

  // sortBy is the sort as Page and Children take it.
  get sortBy(): string {
    return sortString(this.sort);
  }

  // asksAbout: a finished extract with the `ask` policy that reported
  // conflicts and has not been answered. The policy and the destination
  // are the op's own (OpView.Policy, OpView.Destination), so nothing about
  // the call that started it has to be remembered here.
  private asksAbout(o: OpView): boolean {
    return o.finished && o.policy === "ask" && !this.handledConflicts[o.id] && hasConflicts(o.results);
  }

  // conflictQuestion is the question the open archive's page shows: the
  // oldest unanswered `ask` of that archive (APP.md §3). Null when there is
  // none — and while another archive is on screen, since only the page of
  // the archive it came from can re-issue for it.
  get conflictQuestion(): OpView | null {
    let found: OpView | null = null;
    for (const o of Object.values(this.ops)) {
      if (o.archiveId !== this.current || !this.asksAbout(o)) continue;
      if (!found || o.startedAt < found.startedAt) found = o;
    }
    return found;
  }

  // settleConflicts is the page's answer — re-issued, skipped or dismissed
  // — after which the op has nothing more to say and goes, unless it still
  // has a refused name to ask about.
  settleConflicts(opId: string): void {
    this.handledConflicts[opId] = true;
    this.forgetSettled(opId);
  }

  // asksRefused: a finished extract that reported a name or a path the
  // destination refused and has not been answered (APP.md §3, ruled
  // 2026-09-10). Any policy: a refusal is not a collision.
  private asksRefused(o: OpView): boolean {
    return o.finished && !this.handledRefusals[o.id] && hasRefusals(o.results);
  }

  // refusedQuestion is the refused-name question the open archive's page
  // shows: the oldest unanswered one of that archive, derived from the ops
  // as conflictQuestion is, so it survives the page's remount and cannot
  // be lost between Extract's return and the op's end. The page asks it
  // after the conflict question of the same op, not beside it.
  get refusedQuestion(): OpView | null {
    let found: OpView | null = null;
    for (const o of Object.values(this.ops)) {
      if (o.archiveId !== this.current || !this.asksRefused(o)) continue;
      if (!found || o.startedAt < found.startedAt) found = o;
    }
    return found;
  }

  // settleRefusals is the page's answer to the refused names — re-issued
  // with `names`, or skipped.
  settleRefusals(opId: string): void {
    this.handledRefusals[opId] = true;
    this.forgetSettled(opId);
  }

  private forgetSettled(opId: string): void {
    const o = this.ops[opId];
    if (o && !this.asksAbout(o) && !this.asksRefused(o)) this.forgetOp(opId);
  }

  // forgetOp drops an op the page has nothing more to say about, and the
  // names it was issued with.
  private forgetOp(opId: string): void {
    delete this.ops[opId];
    delete this.extractNames[opId];
  }

  // noteExtractNames records the names a re-issue was sent with, against
  // the op it started; nothing is kept for a call without names.
  noteExtractNames(opId: string, names: Record<string, string> | null): void {
    if (names && Object.keys(names).length > 0) this.extractNames[opId] = names;
  }

  // boot subscribes first, then asks for everything.
  boot(): void {
    Events.On("vault.state", (e) => this.applyStatus(e.data as VaultStatus));
    Events.On("vault.ceremony", (e) => this.applyCeremony(e.data as CeremonyState));
    Events.On("vault.warning", (e) => {
      const w = e.data as { code: string };
      // The moment one arrives is still a toast (ruled 2026-10-13); its
      // kind follows the tier (direction 9): an info notice is never red.
      this.toast(warningCopy(w.code), tier(w.code) === "info" ? "info" : "error");
    });
    Events.On("archives.changed", (e) => {
      // The purge names what it dropped at the end of an unlock; every
      // other change carries an empty list (APP.md §13).
      const purged = (e.data as ArchivesChanged | undefined)?.purged ?? [];
      if (purged.length) {
        this.toast(purgedText(purged));
      }
      void this.refreshArchives();
      void this.refreshBackup();
    });
    Events.On("archive.changed", (e) => {
      const d = e.data as { id: string; seq: number };
      if (d.id === this.current && d.seq > (this.archiveSeq[d.id] ?? 0)) {
        // The revision is recorded here, not only off the Stat reply: a
        // Stat answered before this change was made must not put an
        // older tree back (APP.md §2.4, the review's finding 9).
        this.archiveSeq[d.id] = d.seq;
        void this.refreshArchive();
      }
    });
    Events.On("op.progress", (e) => this.applyOp(e.data as OpView));
    Events.On("op.done", (e) => this.applyOp(e.data as OpView, true));
    Events.On("shell.drop", (e) => {
      const d = e.data as Drop;
      // Our own staged paths coming back from a self-drop are never an
      // add (APP.md §3): the Move, if there is one, is the landing's
      // business (dragLanded). Real files from Explorer go on being the
      // add of §3 — a flight still waiting for its landing included
      // (lib/dragout.ts ownDrop).
      if (ownDrop(d.paths ?? [], this.dragOut, this.lastDragFolder)) return;
      // On the Archives page an .efd is opened as Explorer's double-click
      // opens it, and nothing else dropped there does anything (APP.md
      // §14, ruled 2026-10-14) — nor does any drop while a dialog holds
      // the page (§7).
      if (!d.archiveId && this.route === "archives") {
        if (!dialogIsUp()) void this.openFiles(droppedArchives(d.paths ?? [], d.isDir ?? []));
        return;
      }
      if (!d.archiveId) {
        this.toast("Drop files onto an open archive's file list.", "error");
        return;
      }
      this.drop = d;
    });
    Events.On("secret.refused", (e) => {
      const r = e.data as { code: string };
      // The prompt the dialog's password answered still stands: the panel's
      // password step asks it now (APP.md §6, ruled 2026-10-17).
      this.chosenSent = "";
      this.toast(codeText(r.code), "error");
    });
    // The close the shell cancelled (APP.md §2.4). No payload: the question
    // is the same one every time. A second one arriving while the question
    // is up is nothing — the shell cancels every close it does not let
    // through, so pressing the button twice must not stack two dialogs nor
    // restart the one already asking.
    Events.On("shell.close", () => {
      this.closeAsked = true;
    });
    // The tray's Unlock… (APP.md §2.4, ruled 2026-10-17): it has begun the
    // ceremony and shown the window, and the lock scene is where that
    // ceremony is seen — over an open archive too, as the locked banner's
    // own Unlock shows it. No payload. Nothing to show with no vault yet,
    // or with the vault already unlocked by the time this lands.
    Events.On("shell.unlock", () => {
      const s = this.status?.state;
      if (!s || s === VaultState.StateNone || s === VaultState.StateUnlocked) return;
      this.go("lock");
    });
    // A second launch's file (APP.md §14): the page is already up, so it
    // is told rather than asked. The first launch's own argument cannot
    // travel this way — there was no page to hear it — and waits in the
    // shell for the PendingOpen of refreshAll.
    Events.On("shell.open", (e) => {
      void (async () => {
        await this.openFromShell(e.data as OpenRequest);
        // The shell staged that same request in its slot as well, since
        // a second launch may be the one recreating a window whose page
        // is not listening yet. This page *was* listening, so the slot
        // is emptied here rather than left for the next window to open
        // all over again; the request is numbered, so this take opens
        // nothing twice.
        await this.takePendingOpen();
      })();
    });
    setInterval(() => {
      this.now = Date.now();
    }, 1000);
    void this.refreshAll();
  }

  async refreshAll(): Promise<void> {
    this.bootFailed = "";
    try {
      const st = await Vault.Status();
      this.applyStatus(st, true);
    } catch (e) {
      // A failed first status is the line with Retry and nothing else —
      // never the lock scene, which would say the vault is locked on no
      // evidence; with a status accepted from an event meanwhile there is
      // a scene to draw, and the failure is then a toast like any other.
      if (!this.status) {
        const code = errorOf(e).code;
        this.bootFailed = codeText(code);
        this.bootCode = code;
        this.bootFailedAt = Date.now();
        return;
      }
      this.toast(codeText(errorOf(e).code), "error");
    }
    // The gate opens on the call's completion, never on a payload: the
    // scene drawn is the newest status accepted by then, the reply's or
    // a later event's (APP.md §2.4).
    this.booted = true;
    await this.refreshArchives();
    // The file this launch was asked to open (APP.md §14). It is asked
    // for and not waited for as an event, since a launch argument exists
    // before there is any page to emit to: the shell keeps it, and this
    // is the page coming for it. Here rather than last — after the first
    // status, so the answer lands on the scene the vault's state names,
    // and after the list, from which an opened archive takes its path —
    // because a user who double-clicked a file is waiting for it, and the
    // keys, the switch and the settings below are nobody's hurry.
    await this.takePendingOpen();
    await this.refreshSlots();
    await this.refreshEntangled();
    await this.refreshLastExport();
    await this.refreshSettings();
  }

  // takePendingOpen asks the shell whether this launch carried a file
  // (APP.md §14). The ask is the take: a request is handed over once.
  async takePendingOpen(): Promise<void> {
    try {
      await this.openFromShell(await Shell.PendingOpen());
    } catch {
      // No shell to ask — the mock, a dev page — is no request.
    }
  }

  // openFromShell opens what Explorer handed over: the first path, with
  // the rest named in a toast, and nothing at all for a request already
  // acted on.
  //
  // Accepting a request supersedes the one before it on the screen — the
  // key dialog it left up, and the path it was waiting on an unlock for
  // (the review's finding 8): the user has asked for something else, and
  // an answer about the last file is no longer an answer about anything
  // they are looking at.
  async openFromShell(req: OpenRequest | null | undefined): Promise<void> {
    if (!req || !req.seq || !req.path || req.seq <= this.lastShellSeq) return;
    this.lastShellSeq = req.seq;
    await this.openFiles([req.path, ...(req.rest ?? [])]);
  }

  // openFiles is an open the page itself was asked for — Open file… on
  // the Archives page, or .efd files dropped there (APP.md §14, ruled
  // 2026-10-14) — and the tail of Explorer's: the same chain, the same
  // answers, the first path opened and the rest named in a toast.
  async openFiles(paths: readonly string[]): Promise<void> {
    if (!paths.length) return;
    const seq = ++this.lastOpenSeq;
    this.keyNotInVault = null;
    this.openWhenUnlocked = "";
    this.importFor = "";
    const rest = paths.slice(1);
    if (rest.length) this.toast(moreNotOpened(rest.map(baseName)));
    await this.queueOpen(paths[0], seq);
  }

  // The shell's opens run one at a time (the review's finding 5). Two can
  // be in flight — a second launch while the boot's own take is still
  // asking — and a slower older one finishing after a newer one would
  // otherwise land its route, or its dialog, over what the newer one put
  // there. The chain serialises them; openPath's own seq check drops a
  // result the page has moved past, which is the half the chain cannot do
  // (a request that was already awaiting its answer when the newer one
  // was accepted).
  private openChain: Promise<void> = Promise.resolve();

  private queueOpen(path: string, seq: number, retried = false): Promise<void> {
    const run = this.openChain.then(() => this.openPath(path, seq, retried));
    this.openChain = run.catch(() => {});
    return run;
  }

  // openPath is Archives.OpenPath and what each of its answers means
  // (APP.md §14): the archive's page on success, by the same route a
  // row's double-click takes; the lock scene with the path kept, to be
  // finished on the unlock, while the vault is locked; the key's own
  // dialog with Import records… when no record here holds the archive_id;
  // and a toast for everything else — a file that is not an archive, a
  // copy of one already open elsewhere, a forgotten record, Enfold's own
  // folder.
  //
  // `seq` is the request this is acting on: a reply that lands once a
  // newer request has been accepted is dropped, whatever it says.
  // `retried` marks the one attempt made after an unlock: a second
  // vault.needs_unlock is said and not waited on again, so an open cannot
  // ride from unlock to unlock.
  async openPath(path: string, seq: number, retried = false): Promise<void> {
    // Superseded while it waited its turn in the chain: the core is never
    // asked at all, so there is no mount to give back (the second
    // review's finding 2).
    if (seq !== this.lastOpenSeq) return;
    try {
      const r = await Archives.OpenPath(path);
      if (seq !== this.lastOpenSeq) {
        // The core mounted it. A mount no page is going to show is an
        // orphan — nothing holds it and nothing will leave it, and the
        // next open of that archive would meet archive.open_elsewhere
        // against a handle nobody wanted. It goes back, exactly as
        // leaving its page gives it back, unless the newer request has
        // already put that very archive on the page.
        if (r.archiveId && r.archiveId !== this.current) await this.releaseMount(r.archiveId);
        return;
      }
      this.openWhenUnlocked = "";
      await this.openForShell(r.archiveId, seq);
    } catch (e) {
      if (seq !== this.lastOpenSeq) return;
      const err = errorOf(e);
      if (!retried && (err.code === Code.CodeNeedsUnlock || err.code === Code.CodeVaultLocked)) {
        // The vault may have been unlocked while this call was in flight
        // — the refusal was decided before the unlock landed — and then
        // there is no transition left to wait for (the review's finding
        // 3). The state the page holds is the newer word: ask again at
        // once, the one retry this open gets.
        if (this.unlocked) {
          await this.openPath(path, seq, true);
          return;
        }
        this.openWhenUnlocked = path;
        this.openWhenUnlockedSeq = seq;
        // A file opened from Explorer is an explicit Unlock: the lock
        // scene opens on the ceremony already begun (APP.md §6, ruled
        // 2026-10-17).
        this.unlock();
        return;
      }
      this.openWhenUnlocked = "";
      if (err.code === Code.CodeKeyNotInVault) {
        this.keyNotInVault = path;
        return;
      }
      // archive.open_elsewhere names the file the handle is on; every
      // other code says the one thing it says.
      this.toast(err.path ? openElsewhereText(err.path) : codeText(err.code), "error");
    }
  }

  // openForShell opens an archive Explorer named. An archive already open
  // is left first: opening another is one of the explicit leaves (APP.md
  // §2.3, ruled 2026-10-18), and the old handle would otherwise stay
  // mounted with no page holding it (the review's finding 4). The same
  // archive again is not a leave: it is the page it is already on.
  //
  // The leave is **waited for** before the open goes on (the second
  // review's finding 4). A → B → A, with the leave of the first A still
  // travelling, would otherwise reach the core after the second A had
  // been opened and close the archive the page is showing. The shell's
  // opens run one after another, so waiting here is enough to put them in
  // order: leave, open, leave, open.
  private async openForShell(id: string, seq: number): Promise<void> {
    if (this.current && this.current !== id) await this.leaveForShell();
    if (seq !== this.lastOpenSeq) {
      // The core mounted this archive before the leave of the one on the
      // page was waited for, and a newer request was accepted inside
      // that wait. Returning here without giving the mount back would
      // abandon it: nothing holds it and nothing will ever leave it (the
      // third review's finding 2).
      if (id !== this.current) await this.releaseMount(id);
      return;
    }
    await this.openArchive(id, () => seq === this.lastOpenSeq);
  }

  // leaveForShell is leaveArchive with the core's Leave waited for.
  private async leaveForShell(): Promise<void> {
    const left = this.dropArchive(false);
    if (this.route === "archive") this.setRoute("archives");
    await left;
  }

  // releaseMount gives back a mount the page asked for and then had no
  // use for: a shell open the user overtook with another (APP.md §14).
  // The same call a page leaving makes, and it cannot fail in any way
  // that matters — an archive that is not open was already given back.
  private async releaseMount(id: string): Promise<void> {
    try {
      await Archives.Leave(id);
    } catch {
      /* a mount cannot fail to be left */
    }
    void this.refreshArchives();
  }

  // askImportRecords is the key dialog's one action: the Archives page
  // owns *Import records…*, so the ask is handed to it and the route
  // follows.
  askImportRecords(): void {
    this.importFor = this.keyNotInVault ?? "";
    this.keyNotInVault = null;
    this.importRecordsAsked = true;
    this.go("archives");
  }

  // mergeDone is the merge dialog's success, said once the dialog has
  // closed: the file opened from Explorer that led to the import is opened
  // now, by the chain Explorer's double-click takes (direction 3, phase
  // 4a). A merge begun by hand has no file, and does nothing more.
  mergeDone(): void {
    const path = this.importFor;
    this.importFor = "";
    if (path) void this.openFiles([path]);
  }

  // forgetImport: the picker or the merge dialog closed without a merge;
  // the file waits for nothing any more.
  forgetImport(): void {
    this.importFor = "";
  }

  // forgetOpenWhenUnlocked is the lock screen's × on "Unlock to open …"
  // (direction 3, phase 4a): the file is not opened by the unlock. The
  // unlock itself, begun or not, is the user's to finish or cancel.
  forgetOpenWhenUnlocked(): void {
    this.openWhenUnlocked = "";
    this.openWhenUnlockedSeq = 0;
  }

  // takeFocusAsked is the Archives list's one read of the ask: the id, once.
  takeFocusAsked(): string | null {
    const id = this.focusArchiveAsked;
    this.focusArchiveAsked = null;
    return id;
  }

  private remember(id: string): void {
    const vault = this.status?.path ?? "";
    if (vault) this.lastOpened = { vault, id };
  }

  // applySettings is the one write of a choice that applies the moment it
  // is made, as Windows applies one (direction 10, phase 4a; APP.md §6).
  // It echoes the struct the core just read, so the lock times ride back
  // with the read's own timeoutsFromVault: a read taken while Locked
  // carries false and zeros, and the core writes timeouts only from a
  // struct that carries true (APP.md §3, Settings; ruled 2026-10-19) — a
  // choice made here never needs to tell the two apart. It covers
  // every Settings choice, the Vault page's lock times and its name, and
  // View › Preview pane. Settings.Set takes the whole struct, so each
  // write reads the settings first and changes only its own fields in
  // them, and the writes go one after another on one chain: two quick
  // choices — on one control, or on two pages — land in their order and
  // neither puts the other's field back. What the page shows moves at
  // once; once a write has settled the settings are read again with the
  // choices still queued laid over them, so a refusal puts the old value
  // back without a later choice flickering. A refusal is thrown to the
  // caller, which says it where the choice was made.
  private settingsChain: Promise<void> = Promise.resolve();
  private settingsQueued: Partial<SettingsView>[] = [];

  applySettings(patch: Partial<SettingsView>): Promise<void> {
    if (this.settings) this.settings = { ...this.settings, ...patch };
    this.settingsQueued.push(patch);
    const run = this.settingsChain.then(async () => {
      try {
        const now = await Settings.Get();
        await Settings.Set({ ...now, ...patch });
      } finally {
        this.settingsQueued.shift();
        try {
          const read = await Settings.Get();
          this.settings = Object.assign({ ...read }, ...this.settingsQueued);
        } catch {
          /* what is shown stands until the next read */
        }
      }
    });
    this.settingsChain = run.catch(() => {});
    return run;
  }

  // choose is a control's choice through applySettings (direction 10,
  // phase 4a): the control wears the pending ring while its write is
  // under way (choosing, read by lib/pending.ts) and, refused, the
  // code's line under it (choiceRefused) with the old value back. No
  // toast on success: a choice that applied is its own confirmation
  // (DECISIONS 2026-09-07). A refusal is said only for the latest choice
  // of that control, so an older one refused cannot stand under a newer
  // one that applied.
  choosing = $state<Partial<Record<keyof SettingsView, Promise<void>>>>({});
  choiceRefused = $state<Partial<Record<keyof SettingsView, string>>>({});

  choose<K extends keyof SettingsView>(key: K, value: SettingsView[K]): Promise<void> {
    delete this.choiceRefused[key];
    const p: Promise<void> = this.applySettings({ [key]: value } as Partial<SettingsView>).then(
      () => {
        if (this.choosing[key] === p) delete this.choosing[key];
      },
      (e) => {
        if (this.choosing[key] !== p) return;
        delete this.choosing[key];
        this.choiceRefused[key] = codeText(errorOf(e).code);
      },
    );
    this.choosing[key] = p;
    return p;
  }

  // setPreviewPane is View › Preview pane and Alt+P (direction 3, phase
  // 4a): a choice applied at once through applySettings; the Archive page
  // has no line under its menu, so a refusal is a toast.
  setPreviewPane(on: boolean): Promise<void> {
    return this.applySettings({ previewPane: on }).catch((e) => this.toast(codeText(errorOf(e).code), "error"));
  }

  private applyStatus(s: VaultStatus, snapshot = false): void {
    // Older payloads are dropped; a snapshot of the same seq as the last
    // event is the same state and may apply (the first-run snapshot is
    // seq 0).
    if (s.seq < this.seq || (s.seq === this.seq && !snapshot)) return;
    const before = this.status?.state;
    const prior = this.status;
    this.seq = s.seq;
    this.status = s;
    // A vault file opened anew (a switch, an import, a create) carries the
    // name the core just wrote into the settings with its path: the
    // settings are read again, so vaultName, which reads them first, never
    // shows the name of the vault before.
    if (prior && (prior.path !== s.path || prior.displayName !== s.displayName)) void this.refreshSettings();
    // The archive remembered is this vault's: another vault — another
    // path, or none — drops it (direction 3, phase 4a).
    if (this.lastOpened && this.lastOpened.vault !== s.path) this.lastOpened = null;
    // An archive left while something still read it is gone once the
    // status no longer names it (APP.md §2.3): its page goes with it.
    const named = (s.opened ?? []).map((a) => a.id);
    if (this.draining && !named.includes(this.draining)) this.drained();
    if (this.lingering.some((x) => !named.includes(x))) this.lingering = this.lingering.filter((x) => named.includes(x));
    // The page's archive closed by an act outside the store — the
    // inspector's kill switch, say — is gone too, once a status that had
    // named it stops naming it (the review of 2026-10-18).
    if (this.current && named.includes(this.current)) this.seenOpened = this.current;
    else if (this.current && this.seenOpened === this.current) this.closedUnderUs(this.current);
    // A status accepted after the first call failed is the evidence the
    // gate waited for, so it opens on it (APP.md §2.4, ruled 2026-10-18; it
    // cleared the failure and left the gate shut: a bare splash, no Retry).
    if (this.bootFailed) this.booted = true;
    this.bootFailed = "";
    if (s.ops) {
      for (const o of s.ops) {
        // A status carried by an event is a snapshot taken under the core's
        // lock and emitted after it, so a newer op.progress may already have
        // landed: it fills in the operations the page does not know and never
        // overwrites one it tracks. A status the page asked for — its own
        // Status() — replaces them (APP.md §2.4, ruled 2026-09-12). Neither
        // ever un-finishes an operation that has ended.
        const known = this.ops[o.id];
        if (known && (!snapshot || known.finished)) continue;
        // A status can bring a new operation before its first op.progress
        // does; its start clears the done line here as it would there.
        if (!o.finished) this.opBegins(o);
        this.ops[o.id] = o;
      }
    }
    if (s.ceremony && s.ceremony.seq > this.ceremonySeq) {
      this.ceremonySeq = s.ceremony.seq;
      this.sendChosen(s.ceremony);
      this.ceremony = s.ceremony;
      this.noteOutcome(s.ceremony);
      this.exportEnded(s.ceremony);
    }
    // The first status accepted is a scene drawn, not a transition: a
    // window opened from the tray on an unlocked vault showed the lock
    // scene for the settle before this guard (APP.md §2.4, ruled
    // 2026-09-10). What the scene needs read is refreshAll's to fetch.
    if (before !== undefined && before !== s.state) {
      // Settings opened from the lock scene was a visit to it at rest; a
      // lock, an unlock or a ceremony begun elsewhere ends the visit, so
      // the scene the new state names is drawn.
      this.settingsLocked = false;
      // The backup counts were read in the state just left: a read made
      // while Locked holds 0s, and one made while Unlocked is not the
      // registry's after a lock. Dropped here, the card says the date
      // alone until the new state's read lands, and never claims "It
      // holds every archive key." from a count of nothing (APP.md §6, The
      // backups card).
      this.backup = null;
      this.backupRead++; // and a read still in flight is not believed
      // So with the last Find missing files' answer: the presence it
      // measured was this session's (filesCheck).
      this.filesCheck = null;
      // The unlock's full stop (APP.md §6, Motion): the lock screen keeps
      // showing the ceremony's last frame for the settle after the state
      // says Unlocked — the Done event lands a few milliseconds after the
      // state's, and the check must be seen.
      clearTimeout(this.settleTimer);
      if (s.state === VaultState.StateUnlocked) {
        this.settling = true;
        this.settleTimer = setTimeout(() => (this.settling = false), delay(SETTLE));
      } else {
        this.settling = false;
      }
      if (s.state === VaultState.StateUnlocked) {
        this.outcome = null;
        void this.refreshArchives();
        void this.refreshSlots();
        void this.refreshEntangled();
        void this.refreshLastExport();
        void this.refreshSettings();
        if (this.stat) void this.refreshArchive(); // the receipt it owed is paid
        if (this.route === "lock") this.setRoute(this.current ? "archive" : "archives");
        // Back on the list with nothing open, the archive opened last is
        // the likely next one: its row is selected and focused, so Enter
        // opens it (direction 3, phase 4a) — unless Explorer's file is
        // about to be opened instead.
        this.focusArchiveAsked = !this.current && !this.openWhenUnlocked && this.route === "archives" && this.remembered ? this.remembered : null;
        // The file Explorer handed over while the vault was locked
        // (APP.md §14): the unlock has landed, so the open is finished
        // now — once, whatever that attempt answers.
        if (this.openWhenUnlocked) {
          const path = this.openWhenUnlocked;
          const seq = this.openWhenUnlockedSeq;
          this.openWhenUnlocked = "";
          void this.queueOpen(path, seq, true);
        }
      } else if (s.state === VaultState.StateLocked) {
        // The lock screen lists the vault's recovery slots while Locked —
        // Slots() is a cached fact (APP.md §2.1) — and CanEnable is false
        // there, so the Keys page greys the switch rather than guessing.
        void this.refreshSlots();
        void this.refreshEntangled();
        this.details = null;
        // The staged Rename and Description go with the record they were
        // diffed against: nothing here may be written while Locked, and a
        // draft kept over a lock would come back with a baseline no read
        // of the registry stands behind (APP.md §6).
        this.recordDraft = {};
        this.merge = null; // the core dropped the handle with every held key
        this.merging = ""; // and the dialog that held it closes
        this.importFor = ""; // and the merge that would have reopened the file with it
        this.focusArchiveAsked = null;
        if (before === VaultState.StateUnlocked) {
          void this.refreshArchives();
          if (this.stat) void this.refreshArchive();
        }
      }
    }
  }

  private applyCeremony(c: CeremonyState): void {
    if (c.seq <= this.ceremonySeq) return;
    this.ceremonySeq = c.seq;
    this.sendChosen(c);
    // A cancelled ceremony is over the moment it says so: nothing to
    // read, nothing to close.
    this.ceremony = c.step === CeremonyStep.StepFailed && c.error === Code.CodeCancelled ? null : c;
    this.noteOutcome(c);
    this.exportEnded(c);
    if (c.step === CeremonyStep.StepDone || c.step === CeremonyStep.StepFailed || (c.step === CeremonyStep.StepRecovery && !c.promptId)) {
      this.recoveryDraft = null;
    }
  }

  // exportEnded: an export records its date as it ends, inside the
  // ceremony; the read made when ExportBackup returned came before it
  // (that call only begins the ceremony), so the backups card is read
  // again at the Done, or it keeps the old date (APP.md §6, The backups
  // card). Both ways a ceremony step arrives — the status and its own
  // event — come through here, each step once (the seq).
  private exportEnded(c: CeremonyState): void {
    if (c.kind === "export" && c.step === CeremonyStep.StepDone) void this.refreshLastExport();
  }

  private noteOutcome(c: CeremonyState): void {
    this.outcome = outcomeAfter(this.outcome, c);
    this.unlockMethod = methodAfter(this.unlockMethod, c);
    this.ceremonyPassed = passedAfter(this.ceremonyPassed, c);
  }

  // dismissCeremony clears a finished panel; the core has already forgotten it.
  dismissCeremony(): void {
    this.ceremony = null;
    this.ceremonyPassed = NOT_PASSED;
    this.recoveryDraft = null;
    this.dropChosen();
  }

  // holdChosen keeps the password a dialog's Continue judged, for the
  // Choose prompt of the ceremony of that kind the dialog is about to
  // start — create, import or setup (APP.md §6, ruled 2026-10-17). Called
  // after dismissCeremony and before the bound call, so the prompt can
  // never arrive first. An empty value holds nothing: the YubiKey without
  // the tick chooses no password.
  holdChosen(kind: "create" | "import" | "setup", value: string): void {
    this.chosen = value ? { kind, value } : null;
    this.chosenSent = "";
  }

  // dropChosen forgets the held password: the bound call was refused, or
  // the ceremony it was held for is over.
  dropChosen(): void {
    this.chosen = null;
    this.chosenSent = "";
  }

  // sendChosen reads each ceremony event as it is accepted: the held
  // password answers the first Choose password prompt of its own kind, at
  // once, and is dropped; any end of that ceremony before it drops it
  // unsent. An event of another kind changes nothing — the held password
  // is never another ceremony's (the Vault page's Choose prompts are
  // typed on that page).
  private sendChosen(c: CeremonyState): void {
    const ended = c.step === CeremonyStep.StepDone || c.step === CeremonyStep.StepFailed || c.step === CeremonyStep.StepBlocked;
    if (ended) this.chosenSent = "";
    const held = this.chosen;
    if (!held || c.kind !== held.kind) return;
    if (c.step === CeremonyStep.StepPassword && c.choose && c.promptId) {
      this.chosen = null;
      this.chosenSent = c.promptId;
      submitSecret("password", c.promptId, held.value);
      return;
    }
    if (ended) this.dropChosen();
  }

  private applyOp(o: OpView, done = false): void {
    // A new operation takes the last done line away as it begins, so the
    // foot answers the press that started it, never with an old ending
    // (direction 4). One the core starts itself — the reclaim a commit's
    // ending can set off at once — leaves the line and the queue alone
    // (clearsDone, APP.md §6 *The foot*).
    if (!done) this.opBegins(o);
    this.ops[o.id] = o;
    if (!done) return;
    // A failure is still an error toast until InfoBars (phase 4); the
    // user's own Cancel is not a failure, and says what is left in the
    // foot's done line instead (direction 4; it was a neutral "Stopped."
    // toast, ruled 2026-10-14).
    if (o.error && o.error !== Code.CodeOpCancelled) {
      this.toast(opErrorLine(o.kind, o.items, o.error, codeText(o.error)), "error");
    }
    const here = !!o.archiveId && o.archiveId === this.current;
    const line = doneLine(o, new Map(this.archives.map((a) => [a.id, a.name])));
    if (line) this.showDone(line);
    // Something was left out — a kind that differs, a name the tree
    // cannot hold, a skipped subtree. The page it happened on says what
    // happened, item by item, at once (APP.md §3); anywhere else the done
    // line's Details opens the same dialog.
    if (!o.error && hasTrouble(o.results) && here) this.results = o;
    if (o.archiveId && o.archiveId === this.current) void this.refreshArchive();
    void this.refreshArchives();
    // An extract records nothing: the destination is prefilled by the rule
    // of APP.md §3 from the archive's own folder, and no folder is kept
    // from the last time (ruled 2026-09-10). What an extract with the
    // `ask` policy does come back with is its conflicts, which the page of
    // the archive it came from reads off this op (conflictQuestion) — so
    // an op still asking is kept until the page answers, and one whose
    // page has gone is settled with the page (dropArchive).
    setTimeout(() => {
      const kept = this.ops[o.id];
      if (kept && (this.asksAbout(kept) || this.asksRefused(kept)) && kept.archiveId === this.current) return;
      this.forgetOp(o.id);
    }, 8000);
  }

  async refreshArchives(): Promise<void> {
    const cur = this.current;
    const gen = this.openGen;
    try {
      this.archives = (await Archives.List(this.showHidden)) ?? [];
      const here = this.current ? this.archives.find((a) => a.id === this.current) : undefined;
      if (here) this.currentPath = here.path;
      // The list says an archive is closed as soon as the core does
      // (archives.changed), which may be before a status says so: the
      // drain is over, or the page's archive was closed outside the store,
      // and the status is read for the rail's rows. Only a list asked for
      // after the last open landed can say so (closedUnderUs).
      if (here && !here.open && here.id === cur && gen === this.openGen && this.opensInFlight === 0) {
        this.closedUnderUs(here.id);
        void this.readStatus();
      }
      if (this.lingering.length && gen === this.openGen) {
        const shut = new Set(this.archives.filter((a) => !a.open).map((a) => a.id));
        if (this.lingering.some((x) => shut.has(x))) this.lingering = this.lingering.filter((x) => !shut.has(x));
      }
    } catch (e) {
      const code = errorOf(e).code;
      if (code !== "vault.needs_unlock" && code !== "vault.none") this.toast(codeText(code), "error");
    }
  }

  async refreshSlots(): Promise<void> {
    try {
      this.slots = (await Keys.Slots()) ?? [];
    } catch {
      this.slots = [];
    }
  }

  // refreshEntangled reads the vault's switch: On is a cached fact and
  // answers while Locked, CanEnable is false there (APP.md §13).
  async refreshEntangled(): Promise<void> {
    try {
      this.entangled = await Keys.EntangledState();
    } catch {
      this.entangled = null;
    }
  }

  async refreshLastExport(): Promise<void> {
    try {
      this.lastExportAt = (await Vault.LastExportAt()) ?? 0;
    } catch {
      this.lastExportAt = 0; // never recorded here
    }
    await this.refreshBackup();
  }

  // refreshBackup reads what the last backup lacks (APP.md §6, The backups
  // card). A refusal is no status: the card then draws from lastExportAt
  // alone, as while Locked (lib/backup.ts). A read is believed only if no
  // lock or unlock has passed since it was asked: one asked while Locked
  // (counts of 0) could otherwise land after the unlock's reset and read as
  // "every archive key is held".
  private backupRead = 0;
  async refreshBackup(): Promise<void> {
    const read = ++this.backupRead;
    try {
      const b = (await Vault.BackupStatus()) ?? null;
      if (read === this.backupRead) this.backup = b;
    } catch {
      if (read === this.backupRead) this.backup = null;
    }
  }

  // askReplaceVault is the Tampered banner's *Replace with a copy…* on a
  // page (direction 9, ruled 2026-10-18): the import replaces the vault
  // file, which only the lock screen does, so the vault is locked first if
  // it is unlocked, and the lock screen, at rest, opens its import dialog.
  // A vault already Locked is not locked again: a lock in that state still
  // runs its every-state half (internal/app/session.go lockLocked), which
  // drops the token.reset_failed notice, a pending touch and a merge's
  // records. The route alone ends a visit to Settings from the gear.
  async askReplaceVault(): Promise<void> {
    this.replaceAsked = true;
    if (this.unlocked) {
      try {
        await Vault.Lock();
      } catch (e) {
        this.replaceAsked = false;
        this.toast(codeText(errorOf(e).code), "error");
        return;
      }
    }
    this.go("lock");
  }

  // takeReplaceAsked is the lock screen's one read of replaceAsked: asked
  // or not, the ask is spent, so the dialog opens once.
  takeReplaceAsked(): boolean {
    const asked = this.replaceAsked;
    if (asked) this.replaceAsked = false;
    return asked;
  }

  // askTestBackup is the Vault page's *Test that a backup opens…*
  // (direction 10, phase 4a): the check opens the file on a copy, and the
  // core runs it only while the vault is Locked (APP.md §2.1), so the
  // page picks the file first, then the vault is locked and the path is
  // handed to the lock screen, which at rest takes it once and starts the
  // check on its stage (takeTestAsked) — as askReplaceVault hands the
  // replace. A route that leaves the lock screen withdraws it (setRoute).
  async askTestBackup(path: string): Promise<void> {
    if (!path) return;
    this.testAsked = path;
    if (this.unlocked) {
      try {
        await Vault.Lock();
      } catch (e) {
        this.testAsked = "";
        this.toast(codeText(errorOf(e).code), "error");
        return;
      }
    }
    this.go("lock");
  }

  // askExport is the Archives page's summary's *Export…*: to the Vault
  // page, which starts its *Export backup…* once it is shown
  // (takeExportAsked). A route that leaves the Vault page first
  // withdraws it (setRoute), so no picker opens later unasked.
  askExport(): void {
    this.exportAsked = true;
    this.go("keys");
  }

  // takeExportAsked is the Vault page's one read of exportAsked.
  takeExportAsked(): boolean {
    const asked = this.exportAsked;
    if (asked) this.exportAsked = false;
    return asked;
  }

  // takeTestAsked is the lock screen's one read of testAsked: the path,
  // once.
  takeTestAsked(): string {
    const path = this.testAsked;
    if (path) this.testAsked = "";
    return path;
  }

  // leaveOpened is the *Close* of the Archives page's locked line, the
  // lock screen's archives-open notice and the Archive page's
  // archive.needs_reopen bar (APP.md §2.3, ruled 2026-10-19: one meaning
  // for Close): the graceful leave of each archive named that a leave has
  // not already left (lib/leave.ts). One still held after it is named
  // draining or lingering, and the same button then reads *Force close*,
  // which is closeOpened.
  async leaveOpened(ids: readonly string[]): Promise<void> {
    for (const id of toLeave(ids, (x) => this.leftHeld(x))) await this.closeArchive(id);
  }

  // closeOpened is the kill switch of those same three buttons and of the
  // Archives page's *More* (*Force close*, *Force close all*; APP.md §2.3):
  // one archive by its id — Archives.Close — or every open one
  // (Archives.CloseAll). The archive page that showed one closed here has
  // nothing left to show, so it is dropped as closed; an archive CloseAll
  // could not close (one being compacted holds its handle) is said.
  async closeOpened(id?: string): Promise<void> {
    let kept: string[] = [];
    try {
      if (id) await Archives.Close(id);
      else kept = (await Archives.CloseAll()) ?? [];
    } catch (e) {
      this.toast(codeText(errorOf(e).code), "error");
      return;
    }
    if (kept.length) this.toast(codeText(Code.CodeArchiveCompacting), "error");
    this.lingering = id ? this.lingering.filter((x) => x !== id) : this.lingering.filter((x) => kept.includes(x));
    if (this.current && (id ? id === this.current : !kept.includes(this.current))) this.leaveArchive(true);
    await this.refreshArchives();
  }

  private detailsToken = 0;

  // loadDetails reads one registry record whole for the details pane. Like
  // loadPage it drops a superseded reply, so a fast walk down the list
  // never leaves the pane showing another record's facts.
  async loadDetails(id: string | null): Promise<void> {
    const t = ++this.detailsToken;
    if (!id || !this.unlocked) {
      this.details = null;
      return;
    }
    try {
      const d = await Archives.Details(id);
      if (t !== this.detailsToken) return;
      this.details = d;
    } catch (e) {
      if (t !== this.detailsToken) return;
      this.details = null;
      const code = errorOf(e).code;
      if (code !== "vault.locked" && code !== "vault.needs_unlock" && code !== "archive.not_found") this.toast(codeText(code), "error");
    }
  }

  // discardMerge drops the merge handle in the core as well: the dialog
  // was closed without merging (APP.md §13).
  discardMerge(): void {
    const handle = this.merge?.handle;
    this.merge = null;
    if (handle) void Vault.DiscardRecords(handle).catch(() => {});
  }

  async refreshSettings(): Promise<void> {
    try {
      // A choice still on its way to the core stays shown over the read
      // (applySettings), so a refresh landing between a choice and its
      // write does not flick the control back.
      const read = await Settings.Get();
      this.settings = Object.assign({ ...read }, ...this.settingsQueued);
    } catch {
      /* the defaults stand */
    }
  }

  // dismissClose is Esc on the close question: the window stays and the
  // shell is told nothing at all (APP.md §2.4, §6).
  dismissClose(): void {
    if (!this.closeBusy) this.closeAsked = false;
  }

  // decideClose answers it. With remember the shell writes CloseAction
  // through the core's settings before it acts, so the settings page is
  // read again here — a settings save emits no event of its own, and the
  // row must not still show *Ask each time*. The window is destroyed on
  // the way to the tray and the process ends on a quit, so nothing waits
  // on that read.
  async decideClose(action: Decision, remember: boolean): Promise<void> {
    if (this.closeBusy) return;
    this.closeBusy = true;
    try {
      await Shell.CloseDecided(action, remember);
      this.closeAsked = false;
      if (remember) void this.refreshSettings();
    } catch (e) {
      this.toast(codeText(errorOf(e).code), "error");
    } finally {
      this.closeBusy = false;
    }
  }

  // openArchive opens (or shows) an archive and goes to it.
  //
  // `fresh`, when a caller gives one, is asked once the core has answered
  // and before anything at all is written or said. The Open is a second
  // call of its own, and a shell open the user has overtaken must not
  // land its stat, its route or its toast over the request that overtook
  // it (APP.md §14, the second review's finding 3). The mount it was
  // given goes back, since no page is going to show it.
  //
  // Leaving is explicit (APP.md §2.3, ruled 2026-10-18), so the archive
  // already open is simply shown again — its row's Open, its rail row,
  // Explorer naming it again — in the folder, sort and selection it was
  // left in; one draining after its *Close* is mounted again and carries
  // on where it stood. Another archive is opened over it only once the
  // core has mounted the new one, and the old one is then left — waited
  // for, so that A → B → A cannot have the leave of A land after A was
  // opened again (the second review's finding 4).
  async openArchive(id: string, fresh?: () => boolean): Promise<boolean> {
    // The shortcut asks the list too: an archive the list says is closed
    // goes through Open, which joins a handle the core still holds and
    // mounts one it does not (the review of 2026-10-18: a kill switch
    // outside the store left a page to show for a closed handle).
    if (id === this.current && this.draining !== id && this.archives.find((a) => a.id === id)?.open !== false) {
      if (fresh && !fresh()) return false;
      this.remember(id);
      this.show();
      return true;
    }
    let stat: ArchiveStat;
    this.opensInFlight++;
    this.openGen++;
    try {
      stat = await Archives.Open(id);
    } catch (e) {
      if (fresh && !fresh()) return false;
      this.toast(codeText(errorOf(e).code), "error");
      return false;
    } finally {
      this.opensInFlight--;
      this.openGen++;
    }
    if (this.lingering.includes(id)) this.lingering = this.lingering.filter((x) => x !== id);
    if (fresh && !fresh()) {
      // Waited for, not fired off (the third review's finding 3): the
      // shell's opens run one after another, and a chain that advanced
      // while this leave was still travelling could open the same
      // archive again and have the leave arrive afterwards and close the
      // archive the page is showing. The same-id guard is asked here,
      // when the cleanup runs.
      if (id !== this.current) await this.releaseMount(id);
      return false;
    }
    this.remember(id);
    if (id === this.current) {
      this.draining = null;
      this.stat = stat;
      this.show();
      return true;
    }
    const prev = this.current;
    if (prev) {
      try {
        await Archives.Leave(prev);
      } catch {
        /* an archive cannot fail to be left */
      }
      if (fresh && !fresh()) {
        if (id !== this.current) await this.releaseMount(id);
        return false;
      }
      if (this.current === prev) void this.dropArchive(true);
      void this.refreshArchives();
      void this.readStatus();
    }
    this.stat = stat;
    this.current = id;
    this.seenOpened = null;
    this.currentPath = this.archives.find((a) => a.id === id)?.path ?? "";
    this.dirId = ROOT_ID;
    this.page = null;
    this.results = null;
    this.sort = DEFAULT_SORT; // an archive opens on name (APP.md §3)
    this.sel = emptySelection();
    this.focusAfter = null;
    this.setRoute("archive");
    // The list is read again for the row's open: the archive now stays
    // open while the list shows (APP.md §2.3, ruled 2026-10-18), and its
    // row and inspector say so — *Close* is there — whether or not the
    // core's archives.changed has arrived yet.
    void this.refreshArchives();
    await this.loadPage();
    return true;
  }

  // openNew opens an archive New archive… has just made (APP.md §13, ruled
  // 2026-10-14): on its empty root, as any archive opens, with the Add menu
  // asked for — filling it is the next thing to do. A refused open (the
  // vault locked meanwhile) asks for nothing.
  async openNew(id: string): Promise<boolean> {
    this.addAsked = id;
    const opened = await this.openArchive(id);
    if (!opened) this.addAsked = null;
    return opened;
  }

  // takeAddAsked is the Archive page's one read of addAsked as it mounts:
  // whether the Add menu was asked for this archive. Asked or not, the ask
  // is spent, so the menu opens once and never on a later visit.
  takeAddAsked(id: string): boolean {
    const asked = id !== "" && this.addAsked === id;
    this.addAsked = null;
    return asked;
  }

  private statToken = 0;

  // refreshArchive re-reads the archive's Stat and then its listing. The
  // reply is applied by the rule of §2.4: it answers the newest request —
  // the token — and its Seq is not older than the last accepted revision,
  // the event's or an earlier Stat's; two refreshes overlapping could
  // otherwise leave a delayed older Stat standing over a newer one (the
  // review's finding 9).
  async refreshArchive(): Promise<void> {
    const id = this.current;
    // Let go of, its dialog still up; or draining, when every read answers
    // archive.not_open and would drop the page that holds Force close.
    if (!id || id === this.releasedForDelete || id === this.draining) return;
    const t = ++this.statToken;
    try {
      const stat = await Archive.Stat(id);
      if (this.current !== id || t !== this.statToken) return; // the user moved on, or asked again
      if (stat.seq < (this.archiveSeq[id] ?? 0)) return; // older than what was accepted
      this.stat = stat;
      this.archiveSeq[id] = stat.seq;
      await this.loadPage();
    } catch (e) {
      if (this.current !== id || t !== this.statToken) return;
      const code = errorOf(e).code;
      if (code === "archive.not_open") {
        this.leaveArchive(true); // it is closed already; there is nothing to leave
      } else {
        this.toast(codeText(code), "error");
      }
    }
  }

  private pageToken = 0;
  // The name of the folder the page is stepping into, until its listing
  // arrives: the crumbs do not name it yet, so this is what a folder that
  // went is called if it went before it was ever listed.
  private entering = "";
  // The sort the listing held was read under: a listing under another
  // sort is another order, and a reply for it starts afresh. While it
  // differs from the sort chosen the listing on screen is stale — kept
  // until the first page of the new order lands, but never added to.
  private pageSort = "";
  // The token of the loadMore in flight, 0 with none: one at a time, and
  // cleared on every exit — the reply applied or dropped, an error, and
  // at once by a listing restarted from zero, which supersedes it (the
  // review's finding 4: a flag left set by a superseded request stopped
  // every later page).
  private moreInFlight = 0;

  // loadPage lists the directory the page holds from offset zero, a page
  // of up to PAGE_LIMIT rows; loadMore fetches the next as the list
  // scrolls. The page can hold a directory that is gone — a Delete took a
  // subtree the page was standing in — and the core answers
  // file.not_found rather than an empty listing under a heading that
  // still names the place (APP.md §3). The crumbs last held are then
  // walked upwards, retrying until one answers; the root always does.
  //
  // A reply is applied by the rule of §2.4: it must answer the newest
  // request — the token — and be no older than the pages held; a reply
  // newer than them restarts the listing from zero (lib/paging.ts). The
  // ids survive it: the selection keeps those that still exist.
  async loadPage(): Promise<void> {
    const id = this.current;
    if (!id) return;
    const t = ++this.pageToken; // a superseded reply is dropped
    this.moreInFlight = 0; // and a loadMore in flight with it
    const held = this.page?.crumbs ?? [];
    const chain = retryChain(held, this.dirId);
    let target = this.dirId;
    let went = "";
    for (;;) {
      try {
        const reply = await Archive.Page(id, target, this.sortBy, 0, PAGE_LIMIT);
        if (this.current !== id || t !== this.pageToken) return;
        // The pages held are this folder's under this sort, or they are
        // another listing altogether, which the reply replaces whole.
        const same = this.page !== null && shownDir(this.page.crumbs) === target && this.pageSort === this.sortBy;
        const a = applyReply(same ? this.page : null, reply, 0);
        if (a.kind === "applied") {
          this.page = a.listing;
          this.pageSort = this.sortBy;
        }
        this.dirId = target;
        this.entering = "";
        if (went) {
          const now = reply.crumbs?.[reply.crumbs.length - 1]?.name ?? "";
          this.toast(`${went} is no longer in the archive.${now ? ` Showing ${now}.` : ""}`);
        }
        // The row to focus after going up (focusAfter) is not judged here:
        // it is applied by the page once its row is on screen, which may
        // be a later page of a long parent, and the next navigation
        // clears it (APP.md §6, the review's finding 13).
        await this.reconcileSelection(t);
        return;
      } catch (e) {
        if (this.current !== id || t !== this.pageToken) return;
        const code = errorOf(e).code;
        const up = chain.shift();
        if (code !== Code.CodeFileNotFound || up === undefined) {
          // The listing did not arrive and the page still shows the folder
          // it showed before. dirId is what every write names — the drop
          // target's data-dir-id, Create folder, Add files, Add folder —
          // so it is put back where the page actually stands (the last
          // crumb of the listing on screen, root-inclusive and never
          // empty), rather than left naming a folder the user never
          // entered (APP.md §3, DESIGN.md trap 31).
          this.dirId = shownDir(this.page?.crumbs);
          this.entering = "";
          this.focusAfter = null;
          this.toast(codeText(code), "error");
          return;
        }
        if (!went) {
          const name = wentName(held, target) || this.entering;
          went = name ? `"${name}"` : "That folder";
        }
        target = up;
      }
    }
  }

  // loadMore asks for the next page of the folder shown, when the list has
  // scrolled near its end and there is one (APP.md §3: a long folder pages
  // in as the list scrolls). One at a time: a second request while one is
  // in flight would only be dropped by the token. Nothing is asked while
  // the listing is stale — read under a sort no longer chosen — since a
  // page of the new order appended to rows of the old would be no order
  // at all (the review's finding 3); the first page of the new sort is
  // on its way and restarts the listing.
  async loadMore(): Promise<void> {
    const id = this.current;
    const l = this.page;
    if (!id || !l || !hasMore(l) || this.moreInFlight || this.pageSort !== this.sortBy) return;
    const t = ++this.pageToken;
    const offset = nextOffset(l);
    const dir = shownDir(l.crumbs);
    this.moreInFlight = t;
    try {
      const reply = await Archive.Page(id, dir, this.sortBy, offset, PAGE_LIMIT);
      if (this.current !== id || t !== this.pageToken) return;
      const a = applyReply(this.page, reply, offset);
      if (a.kind === "applied") this.page = a.listing;
      // Newer than the pages held: rows may have moved between offsets,
      // so the listing starts again from zero (APP.md §2.4).
      else if (a.kind === "restart") await this.loadPage();
    } catch (e) {
      if (this.current !== id || t !== this.pageToken) return;
      this.toast(codeText(errorOf(e).code), "error");
    } finally {
      // This request's own mark and no later one's: a restart has set it
      // to zero already, and a newer loadMore owns it now.
      if (this.moreInFlight === t) this.moreInFlight = 0;
    }
  }

  // reconcileSelection prunes the selection against the folder after a
  // listing landed (APP.md §2.4): against the rows held when the whole
  // folder is on hand, else against Children — the header's tick and
  // Ctrl+A select ids the list may never have loaded.
  private async reconcileSelection(t: number): Promise<void> {
    if (this.sel.ids.size === 0) return;
    const live = liveIds(this.page);
    if (live) {
      this.sel = survive(this.sel, live);
      return;
    }
    const id = this.current;
    if (!id) return;
    try {
      const kids = (await Archive.Children(id, this.dirId, this.sortBy)) ?? [];
      if (this.current !== id || t !== this.pageToken) return;
      this.sel = survive(this.sel, new Set(kids.map((k) => k.id)));
    } catch {
      /* the next listing prunes it */
    }
  }

  // enterDir shows one directory of the open archive, by id; the name is
  // what the row said, for the message a folder that went leaves behind.
  // Entering a folder clears the selection and the anchor (APP.md §6).
  //
  // A crumb going up the path names the folder it came through, which is
  // selected and focused once its row is on screen, as `..` does (the
  // head's crumbs, direction 3).
  async enterDir(dirId: string, name = "", focusAfter: string | null = null): Promise<void> {
    await this.navigate(dirId, name, focusAfter);
  }

  // goUp is the `..` row: one level up, and once the row of the folder
  // just left is on screen the focus goes to it (APP.md §6).
  async goUp(): Promise<void> {
    const crumbs = this.page?.crumbs ?? [];
    if (crumbs.length < 2) return;
    const parent = crumbs[crumbs.length - 2];
    await this.navigate(parent.id, parent.name, this.dirId);
  }

  // navigate is what both share: the selection and the anchor go, and
  // focusAfter is the folder just left or nothing — the last navigation's
  // is cleared, whether or not its row ever appeared.
  private async navigate(dirId: string, name: string, focusAfter: string | null): Promise<void> {
    this.sel = emptySelection();
    this.dirId = dirId;
    this.entering = name;
    this.focusAfter = focusAfter;
    await this.loadPage();
  }

  // setSort re-reads the folder in the new order; the selection is ids and
  // stays (a header is a control of the list, not its blank area). The
  // listing held is stale from this moment — pageSort no longer matches —
  // so nothing is added to it before the new order's first page lands.
  async setSort(s: SortState): Promise<void> {
    this.sort = s;
    await this.loadPage();
  }

  // The selection's gestures (lib/selection.ts): order is the rows as
  // they stand, top to bottom, for Shift's range.
  selectClick(id: string, m: Modifiers = {}): void {
    this.sel = clickRow(this.sel, (this.page?.rows ?? []).map((r) => r.id), id, m);
  }

  selectToggle(id: string): void {
    this.sel = toggleRow(this.sel, id);
  }

  clearSelection(): void {
    this.sel = emptySelection();
  }

  // selectFolder ticks the whole folder — every id Children answers,
  // loaded or not (APP.md §6): the header's tick and Ctrl+A.
  async selectFolder(): Promise<void> {
    const id = this.current;
    const dir = this.dirId;
    const gen = this.selGen;
    if (!id) return;
    try {
      const kids = (await Archive.Children(id, dir, this.sortBy)) ?? [];
      // The reply ticks the folder only over the selection it was asked
      // for: one changed meanwhile — a blank click cleared it, a row was
      // clicked — stands, and a Delete asked in that interval takes what
      // the user sees (the review's finding 2).
      if (this.current !== id || this.dirId !== dir || this.selGen !== gen) return; // the user moved on
      this.sel = selectAll(this.sel, kids.map((k) => k.id));
    } catch (e) {
      this.toast(codeText(errorOf(e).code), "error");
    }
  }

  // beginDragOut is the one gesture's start (APP.md §3, §6, ruled
  // 2026-09-11): a press-and-move over selected rows. The native drag runs
  // from Shell.DragOut, which answers when DoDragDrop has returned — the
  // strip follows the operation meanwhile — and the ids stay in flight
  // until the answer and, for a self-drop, the WebView's landing add up to
  // a verdict (lib/dragout.ts). A gesture while one is in flight is
  // ignored, and so is one with nothing selected.
  async beginDragOut(ids: string[]): Promise<void> {
    const id = this.current;
    if (!id || this.dragOut || ids.length === 0) return;
    // The names the loaded rows give the ids, so a drop can be told to be
    // this gesture's own (lib/dragout.ts ownNames); ids past the loaded
    // rows — a whole folder taken by Ctrl+A — have none to give.
    const byId = new Map((this.page?.rows ?? []).map((r) => [r.id, r.name]));
    const names = ids.map((i) => byId.get(i)).filter((n): n is string => n !== undefined);
    this.dragOut = beginFlight(ids, this.dirId, names);
    let a;
    try {
      a = await Shell.DragOut(id, ids);
    } catch (e) {
      // drag.busy, drag.unsupported, or the plan's own refusal — a name two
      // records would share at the top of the staging folder, an id gone.
      if (this.dragOut && this.current === id) this.dragOut = null;
      this.toast(codeText(errorOf(e).code), "error");
      return;
    }
    this.lastDragFolder = a.folder;
    const f = this.dragOut;
    if (!f || this.current !== id) return; // the page was left under the drag
    const r = answerDrag(f, a, a.opId);
    this.dragOut = r.flight;
    this.settleDrag(r.verdict);
  }

  // dragLanded is the WebView's own drop while a drag is in flight: on a
  // folder row, the `..` row or the heading — the page names which — or
  // elsewhere on the page; or someone else's files, which the page tells
  // apart by their names (lib/dragout.ts ownNames) and which end the
  // flight with nothing to do while the drop goes on being the add of §3.
  // Nothing lands without a flight.
  dragLanded(l: Landed): void {
    const f = this.dragOut;
    if (!f) return;
    const r = land(f, l);
    this.dragOut = r.flight;
    this.settleDrag(r.verdict);
  }

  // settleDrag ends the flight on a verdict, or keeps it while the other
  // half is still to come. A self-drop answered before its landing waits a
  // moment for the WebView's drop, which travels a different road from the
  // call's return; a release over the window frame itself never lands on
  // the page, and the flight ends as nothing.
  private settleDrag(v: Verdict): void {
    clearTimeout(this.dragGrace);
    if (v.kind === "wait") {
      if (this.dragOut?.answer?.selfDrop) {
        this.dragGrace = setTimeout(() => {
          this.dragOut = null;
        }, SELF_DROP_GRACE);
      }
      return;
    }
    this.dragOut = null;
    if (v.kind === "move") this.selfDrop = v.move;
  }

  // dropArchive tells the core the page is gone and forgets what the page
  // held. Leaving is `Archives.Leave`, never `Close` (APP.md §2.3, ruled
  // 2026-09-10): the archive closes at once and its keys go, unless a
  // preview body is still in flight, in which case it drains and closes
  // itself after the last one — where Close is the page's kill switch and
  // drops the readers too. `closed` says the caller has already closed it
  // (the kill switch, a Delete archive…), so there is nothing to leave.
  private dropArchive(closed: boolean): Promise<void> {
    const id = this.current;
    this.current = null;
    this.seenOpened = null;
    this.draining = null;
    this.releasedForDelete = null;
    this.stat = null;
    this.currentPath = "";
    this.page = null;
    this.pageSort = "";
    this.moreInFlight = 0;
    this.dirId = ROOT_ID;
    this.entering = "";
    this.results = null;
    this.sel = emptySelection();
    this.focusAfter = null;
    // A drag in flight goes with the page too: Leave lets its operation
    // finish and Close cancels it, and neither has a page left to move
    // anything on.
    clearTimeout(this.dragGrace);
    this.dragOut = null;
    this.selfDrop = null;
    // A question the page did not answer goes with the page: the archive
    // closes behind it, and nothing could re-issue for it.
    for (const o of Object.values(this.ops)) {
      if (o.archiveId !== id) continue;
      if (this.asksAbout(o)) this.settleConflicts(o.id);
      if (this.asksRefused(o)) this.settleRefusals(o.id);
    }
    // The leave is answered, and the answer is handed back: every caller
    // but one ignores it, and the shell's own opens wait for it so that
    // A → B → A cannot close the A the page has just put back (APP.md
    // §14, leaveForShell).
    if (id && !closed) {
      return Archives.Leave(id)
        .then(() => {
          void this.readStatus();
          return this.refreshArchives();
        })
        .catch(() => {
          /* a page cannot fail to be left */
        });
    }
    return Promise.resolve();
  }

  // readStatus reads the status after a leave: the rail's rows are the
  // archives it names (VaultStatus.Opened), and a leave that closed one is
  // seen at once rather than at the core's next word.
  private async readStatus(): Promise<void> {
    try {
      const st = await Vault.Status();
      if (st) this.applyStatus(st, true);
    } catch {
      /* the next status says it */
    }
  }

  leaveArchive(closed = false): void {
    void this.dropArchive(closed);
    if (this.route === "archive") this.setRoute("archives");
  }

  go(route: Route): void {
    if (route === "archive" && this.current) this.show();
    else this.setRoute(route);
  }

  // show puts the open archive's page back (APP.md §2.3, ruled
  // 2026-10-18): the store kept its folder, its sort and its selection
  // while another page was up, and the core is asked for that folder
  // again — what changed meanwhile lands, the selection keeps the ids that
  // still exist — rather than starting from the root.
  private show(): void {
    const away = this.route !== "archive";
    this.setRoute("archive");
    if (away) void this.refreshArchive();
  }

  // closeArchive is the graceful leave asked for by name (APP.md §2.3,
  // ruled 2026-10-18): the rail row's × and the Archive page's *Close*.
  // Archives.Leave, then the status: an archive the core still names is
  // draining — a preview body still served, or its own operation still
  // running — and its page, when it is the one the store holds, stands
  // with *Force close* until the status stops naming it. Anything else is
  // gone, and the page goes back to the list.
  async closeArchive(id: string): Promise<void> {
    try {
      await Archives.Leave(id);
    } catch (e) {
      this.toast(codeText(errorOf(e).code), "error");
      return;
    }
    let still = false;
    try {
      const st = await Vault.Status();
      still = (st.opened ?? []).some((a) => a.id === id);
      this.applyStatus(st, true);
    } catch {
      /* unread: taken as closed, and the next status says otherwise */
    }
    if (id === this.current) {
      if (still) this.draining = id;
      else this.leaveArchive(true);
    } else if (still && !this.lingering.includes(id)) {
      this.lingering = [...this.lingering, id];
    }
    void this.refreshArchives();
  }

  // leftHeld: an archive a *Close* has left that something still holds —
  // the page's own, draining, or another, lingering. Its kill switch is
  // *Force close*, and its rail row has no × (APP.md §2.3).
  leftHeld(id: string): boolean {
    return this.draining === id || this.lingering.includes(id);
  }

  // drained ends the wait of a *Close* once the core no longer holds the
  // archive: the page that stood for it goes too.
  private drained(): void {
    const id = this.draining;
    this.draining = null;
    if (id && id === this.current) this.leaveArchive(true);
  }

  // closedUnderUs drops the page's archive once the core has closed it by
  // an act that did not go through the store (the review of 2026-10-18):
  // left standing, the rail kept a row for a closed handle and an Open of
  // it showed a page whose first read bounced back to the list. One let go
  // of by its Delete archive… dialog stays until the dialog ends, and an
  // open still travelling is not contradicted.
  private closedUnderUs(id: string): void {
    if (id !== this.current || id === this.releasedForDelete || this.opensInFlight > 0) return;
    if (this.draining === id) {
      this.drained();
      return;
    }
    this.leaveArchive(true);
  }

  // settingsLocked: Settings was opened from the lock scene's gear, which
  // it can be — Settings is this PC's and needs no vault (APP.md §6,
  // ruled 2026-10-17). The scene steps aside for the Settings page until
  // the route moves on or the vault's state changes.
  settingsLocked = $state(false);

  openSettingsLocked(): void {
    this.setRoute("settings");
    this.settingsLocked = true;
  }

  // unlock is an explicit Unlock — the locked banner's, the foot's, the
  // Vault page's, a file opened from Explorer: the lock scene, with the
  // ceremony already begun by the vault's everyday way in, as the tray's
  // Unlock… begins it in the shell (APP.md §6, ruled 2026-10-17). A vault
  // that needs setting up, or is not at rest, gets the scene alone, its
  // panel saying what comes first; an idle lock never comes this way. A
  // refusal is the lock screen's to say, as its own buttons say it.
  unlock(): void {
    this.setRoute("lock");
    const method = explicitMethod(this.status);
    if (!method) return;
    void (async () => {
      try {
        await Vault.BeginUnlock(method);
      } catch (e) {
        this.toast(codeText(errorOf(e).code), "error");
      }
    })();
  }

  // footNote is what the layer's foot says on the left: the page sets it
  // (APP.md §6).
  footNote = $state("");

  // settling: the vault is Unlocked and the lock screen is still showing
  // the ceremony's end (APP.md §6, Motion).
  settling = $state(false);
  private settleTimer: ReturnType<typeof setTimeout> | undefined;

  // nav is the direction the last route change travelled, for the page
  // transition (APP.md §6): 1 into an archive, -1 back out, 0 sideways.
  nav = $state(0);

  // A route change leaves nothing (APP.md §2.3, ruled 2026-10-18; it used
  // to leave the archive, which made a glance at Settings close it): the
  // archive stays open and mounted in the core, `current` and its folder,
  // sort and selection are kept, and the rail names it on every page. It
  // is left only by an explicit act — its rail row's ×, the head's
  // *Close*, opening another, *Delete archive…* — or by the window
  // closing (the shell's LeaveAll).
  private setRoute(route: Route): void {
    const from = this.route;
    if (route !== "settings") this.settingsLocked = false;
    // The Tampered banner's ask is the lock screen's to take; a route
    // that leaves it (an unlock, the rail) withdraws it, so the import
    // never opens later unasked.
    if (route !== "lock") this.replaceAsked = false;
    // The same for the Vault page's test of a backup (askTestBackup).
    if (route !== "lock") this.testAsked = "";
    // And for the summary's export, the Vault page's to take.
    if (route !== "keys") this.exportAsked = false;
    // A refusal said under a control belongs to the page it was said on.
    if (route !== from) this.choiceRefused = {};
    this.nav = from === "archives" && route === "archive" ? 1 : from === "archive" && route === "archives" ? -1 : 0;
    this.route = route;
  }

  // showDone puts a done line in the foot (direction 4): an operation's
  // ending (applyOp), Find missing files' count, a backup saved. It holds
  // DONE_MS; one that arrives while another is up waits its turn.
  showDone(line: DoneLine): void {
    const q = sayDone<DoneLine & { id: number }>({ shown: this.done, waiting: this.doneWaiting }, { ...line, id: ++this.doneId });
    this.turnDone(q);
  }

  // clearDone ends the line up now, and the next waiting one is said: the
  // next press in a page's body (LayerFoot) is the user moving on from it.
  clearDone(): void {
    this.turnDone(nextDone({ shown: this.done, waiting: this.doneWaiting }));
  }

  // opBegins reads an operation the store has not seen before (from an
  // op.progress or a status) as its start: one the user started ends the
  // line and everything waiting, the foot answering the press with the
  // operation itself; one the core started (clearsDone) leaves them, and
  // shows once their turn is over. An op already known is no start.
  private opBegins(o: OpView): void {
    if (o.id in this.ops || !clearsDone(o)) return;
    this.turnDone({ shown: null, waiting: [] });
  }

  private turnDone(q: DoneTurn<DoneLine & { id: number }>): void {
    this.doneWaiting = q.waiting;
    if (q.shown === this.done) return;
    clearTimeout(this.doneTimer);
    this.done = q.shown;
    const id = q.shown?.id;
    if (id === undefined) return;
    this.doneTimer = setTimeout(() => {
      if (this.done?.id === id) this.clearDone();
    }, DONE_MS);
  }

  toast(text: string, kind: "info" | "error" = "info"): void {
    const id = ++this.toastId;
    this.toasts.push({ id, text, kind });
    setTimeout(() => {
      this.toasts = this.toasts.filter((t) => t.id !== id);
    }, kind === "error" ? 9000 : 4500);
  }

  // activity is the heartbeat (APP.md: a request, granted only when the
  // session saw input). Throttled here; the core rate-limits again.
  activity(): void {
    const t = Date.now();
    if (t - this.lastActivity < 5000 || !this.unlocked) return;
    this.lastActivity = t;
    void Vault.Activity();
  }

  // ceremonyIsOver: the final event has arrived and the panel may be
  // dismissed. A ceremony with an outstanding prompt is never over: the
  // recovery step is a prompt when it asks and a reveal when it shows.
  get ceremonyIsOver(): boolean {
    const c = this.ceremony;
    if (!c) return true;
    if (c.promptId) return false;
    return c.step === CeremonyStep.StepDone || c.step === CeremonyStep.StepFailed || c.step === CeremonyStep.StepRecovery;
  }

  // Reveals already dismissed: the ceremony's final event repeats the
  // one-time URL, which must not bring the dialog back.
  private revealed = new Set<string>();

  dismissReveal(url: string): void {
    this.revealed.add(url);
    this.dismissCeremony();
  }

  revealPending(url: string): boolean {
    return !this.revealed.has(url);
  }
}

export const store = new Store();

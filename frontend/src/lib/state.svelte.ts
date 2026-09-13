// The page's state: what the core reported, applied in sequence order, and
// the little the page adds (route, selection, toasts). Every event is
// subscribed in boot() before the first fetch (APP.md §2.4).
import { Events } from "@wailsio/runtime";
import { Archive, Archives, Keys, Settings, Shell, Vault, errorOf, Code } from "./api";
import type { ArchiveDetails, ArchiveStat, ArchiveSummary, CeremonyState, EntangledState, IncomingRecord, OpView, SettingsView, SlotView, VaultStatus } from "./api";
import { CeremonyStep, VaultState } from "./api";
import { codeText, warningCopy } from "./strings";
import { methodAfter, outcomeAfter } from "./outcome";
import { delay, SETTLE } from "./motion";
import { ROOT_ID, retryChain, shownDir, wentName } from "./tree";
import { hasTrouble, summaryLine, tally } from "./results";
import { hasConflicts } from "./conflicts";
import { hasRefusals } from "./refused";
import { DEFAULT_SORT, sortString } from "./sort";
import type { SortState } from "./sort";
import { clickRow, emptySelection, selectAll, survive, toggleRow } from "./selection";
import type { Modifiers, Selection } from "./selection";
import { PAGE_LIMIT, applyReply, hasMore, liveIds, nextOffset } from "./paging";
import type { Listing } from "./paging";
import { opErrorLine, opLabel, reclaimedLine } from "./ops";
import type { Outcome } from "./outcome";
import { answer as answerDrag, beginFlight, land, ownDrop } from "./dragout";
import type { Flight, Landed, PendingMove, Verdict } from "./dragout";
import type { Decision } from "./closing";

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
  // recoveryDraft: the eight groups as typed, kept across the derivation
  // step so that a key the core refused comes back for correction
  // (APP.md §6); dropped when the ceremony ends.
  recoveryDraft = $state<string[] | null>(null);
  ops = $state<Record<string, OpView>>({});
  archives = $state<ArchiveSummary[]>([]);
  showHidden = $state(false);
  route = $state<Route>("archives");
  current = $state<string | null>(null);
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
  // A merge handle and the records it read, until MergeRecords consumes
  // it, the dialog discards it, or a lock trigger drops it.
  merge = $state<{ handle: string; records: IncomingRecord[] } | null>(null);
  // Delete archive… asked for on the Archive page: the archive is closed
  // first (APP.md §13), which leaves that page, so the id is handed to the
  // Archives page, which opens the one delete dialog over its own list.
  deleteAfterClose = $state<string | null>(null);
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
  // what it said, shown as one plain line with Retry.
  bootFailed = $state("");

  private seq = 0;
  private ceremonySeq = 0;
  private archiveSeq: Record<string, number> = {};
  private toastId = 0;
  private lastActivity = 0;

  get unlocked(): boolean {
    return this.status?.state === VaultState.StateUnlocked;
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
      this.toast(warningCopy(w.code), "error");
    });
    Events.On("archives.changed", (e) => {
      // The purge names what it dropped at the end of an unlock; every
      // other change carries an empty list (APP.md §13).
      const purged = (e.data as ArchivesChanged | undefined)?.purged ?? [];
      if (purged.length) {
        this.toast(purged.length === 1 ? `The key of "${purged[0]}" was dropped: it was forgotten more than thirty days ago.` : `${purged.length} forgotten archives' keys were dropped: ${purged.join(", ")}.`);
      }
      void this.refreshArchives();
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
      if (!d.archiveId) {
        this.toast("Drop files onto an open archive's file list.", "error");
        return;
      }
      this.drop = d;
    });
    Events.On("secret.refused", (e) => {
      const r = e.data as { code: string };
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
        this.bootFailed = codeText(errorOf(e).code);
        return;
      }
      this.toast(codeText(errorOf(e).code), "error");
    }
    // The gate opens on the call's completion, never on a payload: the
    // scene drawn is the newest status accepted by then, the reply's or
    // a later event's (APP.md §2.4).
    this.booted = true;
    await this.refreshArchives();
    await this.refreshSlots();
    await this.refreshEntangled();
    await this.refreshLastExport();
    await this.refreshSettings();
  }

  private applyStatus(s: VaultStatus, snapshot = false): void {
    // Older payloads are dropped; a snapshot of the same seq as the last
    // event is the same state and may apply (the first-run snapshot is
    // seq 0).
    if (s.seq < this.seq || (s.seq === this.seq && !snapshot)) return;
    const before = this.status?.state;
    this.seq = s.seq;
    this.status = s;
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
        this.ops[o.id] = o;
      }
    }
    if (s.ceremony && s.ceremony.seq > this.ceremonySeq) {
      this.ceremonySeq = s.ceremony.seq;
      this.ceremony = s.ceremony;
      this.noteOutcome(s.ceremony);
    }
    // The first status accepted is a scene drawn, not a transition: a
    // window opened from the tray on an unlocked vault showed the lock
    // scene for the settle before this guard (APP.md §2.4, ruled
    // 2026-09-10). What the scene needs read is refreshAll's to fetch.
    if (before !== undefined && before !== s.state) {
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
        this.deleteAfterClose = null;
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
    // A cancelled ceremony is over the moment it says so: nothing to
    // read, nothing to close.
    this.ceremony = c.step === CeremonyStep.StepFailed && c.error === Code.CodeCancelled ? null : c;
    this.noteOutcome(c);
    if (c.step === CeremonyStep.StepDone || c.step === CeremonyStep.StepFailed || (c.step === CeremonyStep.StepRecovery && !c.promptId)) {
      this.recoveryDraft = null;
    }
  }

  private noteOutcome(c: CeremonyState): void {
    this.outcome = outcomeAfter(this.outcome, c);
    this.unlockMethod = methodAfter(this.unlockMethod, c);
  }

  // dismissCeremony clears a finished panel; the core has already forgotten it.
  dismissCeremony(): void {
    this.ceremony = null;
    this.recoveryDraft = null;
  }

  private applyOp(o: OpView, done = false): void {
    this.ops[o.id] = o;
    if (!done) return;
    if (o.error) {
      this.toast(opErrorLine(o.kind, o.items, o.error, codeText(o.error)), "error");
    } else if (reclaimedLine(o)) {
      // A reclaim reports what came back, on whichever page is up: the
      // strip that showed it running has gone with it.
      this.toast(reclaimedLine(o));
    } else if (hasTrouble(o.results)) {
      // Something was left out — a kind that differs, a name the tree
      // cannot hold, a skipped subtree. The page it happened on says what
      // happened, item by item; another archive's op gets the summary as a
      // toast, since its own surface is not on screen.
      if (o.archiveId && o.archiveId === this.current) this.results = o;
      else this.toast(`${opLabel(o.kind, o.items)}: ${summaryLine(tally(o.results))}`, "error");
    }
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
    try {
      this.archives = (await Archives.List(this.showHidden)) ?? [];
      const here = this.current ? this.archives.find((a) => a.id === this.current) : undefined;
      if (here) this.currentPath = here.path;
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
      this.settings = await Settings.Get();
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
  async openArchive(id: string): Promise<boolean> {
    try {
      this.stat = await Archives.Open(id);
      this.current = id;
      this.currentPath = this.archives.find((a) => a.id === id)?.path ?? "";
      this.dirId = ROOT_ID;
      this.page = null;
      this.results = null;
      this.sort = DEFAULT_SORT; // an archive opens on name (APP.md §3)
      this.sel = emptySelection();
      this.focusAfter = null;
      this.setRoute("archive");
      await this.loadPage();
      return true;
    } catch (e) {
      this.toast(codeText(errorOf(e).code), "error");
      return false;
    }
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
    if (!id) return;
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
  async enterDir(dirId: string, name = ""): Promise<void> {
    await this.navigate(dirId, name, null);
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
  private dropArchive(closed: boolean): void {
    const id = this.current;
    this.current = null;
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
    if (id && !closed) {
      void Archives.Leave(id)
        .then(() => this.refreshArchives())
        .catch(() => {
          /* a page cannot fail to be left */
        });
    }
  }

  leaveArchive(closed = false): void {
    this.dropArchive(closed);
    if (this.route === "archive") this.setRoute("archives");
  }

  go(route: Route): void {
    this.setRoute(route);
  }

  // settingsDraft holds the settings page's staged edits, key by key, so
  // that they survive a visit to another page; the page diffs it against
  // the saved settings (APP.md §6, the save bar).
  settingsDraft = $state<Record<string, unknown>>({});

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

  // Any route change away from the archive's page is leaving it, and the
  // archive is left (APP.md §2.3): the rail, the breadcrumb's *Archives*,
  // a *Delete archive…*. The lock screen is the one exception — it is a
  // scene over the same page, `current` is kept and the unlock comes
  // straight back to it (applyStatus), so the page was never left.
  private setRoute(route: Route): void {
    const from = this.route;
    if (from === "archive" && route !== "archive" && route !== "lock" && this.current) this.dropArchive(false);
    this.nav = from === "archives" && route === "archive" ? 1 : from === "archive" && route === "archives" ? -1 : 0;
    this.route = route;
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

// The page's state: what the core reported, applied in sequence order, and
// the little the page adds (route, selection, toasts). Every event is
// subscribed in boot() before the first fetch (APP.md §2.4).
import { Events } from "@wailsio/runtime";
import { Archive, Archives, Keys, Settings, Vault, errorOf, Code } from "./api";
import type { ArchiveDetails, ArchiveStat, ArchiveSummary, CeremonyState, EntangledState, IncomingRecord, OpView, Page, SettingsView, SlotView, VaultStatus } from "./api";
import { CeremonyStep, VaultState } from "./api";
import { codeText, warningCopy } from "./strings";
import { methodAfter, outcomeAfter } from "./outcome";
import { delay, SETTLE } from "./motion";
import { ROOT_ID, retryChain, shownDir, wentName } from "./tree";
import { hasTrouble, summaryLine, tally } from "./results";
import { hasConflicts } from "./conflicts";
import { opLabel } from "./ops";
import type { Outcome } from "./outcome";

export type Route = "archives" | "archive" | "keys" | "settings" | "lock";

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
  page = $state<Page | null>(null);
  // The directory the page is showing, as an id: the all-zero id is the
  // archive's root (APP.md §3). The breadcrumb is the page's own Crumbs,
  // never built from this.
  dirId = $state(ROOT_ID);
  sortBy = $state("name");
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
  now = $state(Date.now());
  booted = $state(false);

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
  // — after which the op has nothing more to say and goes.
  settleConflicts(opId: string): void {
    this.handledConflicts[opId] = true;
    delete this.ops[opId];
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
        void this.refreshArchive();
      }
    });
    Events.On("op.progress", (e) => this.applyOp(e.data as OpView));
    Events.On("op.done", (e) => this.applyOp(e.data as OpView, true));
    Events.On("shell.drop", (e) => {
      const d = e.data as Drop;
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
    setInterval(() => {
      this.now = Date.now();
    }, 1000);
    void this.refreshAll();
  }

  async refreshAll(): Promise<void> {
    try {
      const st = await Vault.Status();
      this.applyStatus(st, true);
    } catch (e) {
      this.toast(codeText(errorOf(e).code), "error");
    }
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
    if (s.ops) {
      for (const o of s.ops) {
        if (!this.ops[o.id]?.finished) this.ops[o.id] = o;
      }
    }
    if (s.ceremony && s.ceremony.seq > this.ceremonySeq) {
      this.ceremonySeq = s.ceremony.seq;
      this.ceremony = s.ceremony;
      this.noteOutcome(s.ceremony);
    }
    if (before !== s.state) {
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
      this.toast(`${opLabel(o.kind, o.items)}: ${codeText(o.error)}`, "error");
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
      if (kept && this.asksAbout(kept) && kept.archiveId === this.current) return;
      delete this.ops[o.id];
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

  // openArchive opens (or shows) an archive and goes to it.
  async openArchive(id: string): Promise<boolean> {
    try {
      this.stat = await Archives.Open(id);
      this.current = id;
      this.currentPath = this.archives.find((a) => a.id === id)?.path ?? "";
      this.dirId = ROOT_ID;
      this.page = null;
      this.results = null;
      this.setRoute("archive");
      await this.loadPage();
      return true;
    } catch (e) {
      this.toast(codeText(errorOf(e).code), "error");
      return false;
    }
  }

  async refreshArchive(): Promise<void> {
    const id = this.current;
    if (!id) return;
    try {
      const stat = await Archive.Stat(id);
      if (this.current !== id) return; // the user moved on meanwhile
      this.stat = stat;
      this.archiveSeq[id] = stat.seq;
      await this.loadPage();
    } catch (e) {
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

  // loadPage lists the directory the page holds. The page can hold one
  // that is gone — a Delete took a subtree the page was standing in — and
  // the core answers file.not_found rather than an empty listing under a
  // breadcrumb that still names the place (APP.md §3). The crumbs last held are then
  // walked upwards, retrying until one answers; the root always does.
  async loadPage(): Promise<void> {
    const id = this.current;
    if (!id) return;
    const t = ++this.pageToken; // a superseded reply is dropped
    const held = this.page?.crumbs ?? [];
    const chain = retryChain(held, this.dirId);
    let target = this.dirId;
    let went = "";
    for (;;) {
      try {
        const page = await Archive.Page(id, target, this.sortBy, 0, 2000);
        if (this.current !== id || t !== this.pageToken) return;
        this.page = page;
        this.dirId = target;
        this.entering = "";
        if (went) {
          const now = page.crumbs?.[page.crumbs.length - 1]?.name ?? "";
          this.toast(`${went} is no longer in the archive.${now ? ` Showing ${now}.` : ""}`);
        }
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

  // enterDir shows one directory of the open archive, by id; the name is
  // what the row said, for the message a folder that went leaves behind.
  async enterDir(dirId: string, name = ""): Promise<void> {
    this.dirId = dirId;
    this.entering = name;
    await this.loadPage();
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
    this.dirId = ROOT_ID;
    this.entering = "";
    this.results = null;
    // A question the page did not answer goes with the page: the archive
    // closes behind it, and nothing could re-issue for it.
    for (const o of Object.values(this.ops)) {
      if (o.archiveId === id && this.asksAbout(o)) this.settleConflicts(o.id);
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

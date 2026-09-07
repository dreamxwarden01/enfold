// The page's state: what the core reported, applied in sequence order, and
// the little the page adds (route, selection, toasts). Every event is
// subscribed in boot() before the first fetch (APP.md §2.4).
import { Events } from "@wailsio/runtime";
import { Archive, Archives, Keys, Settings, Vault, errorOf } from "./api";
import type { ArchiveStat, ArchiveSummary, CeremonyState, OpView, Page, SettingsView, SlotView, VaultStatus } from "./api";
import { CeremonyStep, VaultState } from "./api";
import { codeText, warningCopy } from "./strings";

export type Route = "archives" | "archive" | "keys" | "settings" | "lock";

export interface Toast {
  id: number;
  text: string;
  kind: "info" | "error";
}

export interface Drop {
  paths: string[];
  archiveId: string;
  folder: string;
}

export interface Expiring {
  id: string;
  closesAt: number;
  dirty: number;
}

class Store {
  status = $state<VaultStatus | null>(null);
  ceremony = $state<CeremonyState | null>(null);
  ops = $state<Record<string, OpView>>({});
  archives = $state<ArchiveSummary[]>([]);
  showHidden = $state(false);
  route = $state<Route>("archives");
  current = $state<string | null>(null);
  stat = $state<ArchiveStat | null>(null);
  page = $state<Page | null>(null);
  folder = $state("");
  sortBy = $state("name");
  slots = $state<SlotView[]>([]);
  settings = $state<SettingsView | null>(null);
  toasts = $state<Toast[]>([]);
  expiring = $state<Expiring | null>(null);
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

  // boot subscribes first, then asks for everything.
  boot(): void {
    Events.On("vault.state", (e) => this.applyStatus(e.data as VaultStatus));
    Events.On("vault.ceremony", (e) => this.applyCeremony(e.data as CeremonyState));
    Events.On("vault.warning", (e) => {
      const w = e.data as { code: string };
      this.toast(warningCopy(w.code), "error");
    });
    Events.On("archives.changed", () => void this.refreshArchives());
    Events.On("archive.changed", (e) => {
      const d = e.data as { id: string; seq: number };
      if (d.id === this.current && d.seq > (this.archiveSeq[d.id] ?? 0)) {
        void this.refreshArchive();
      }
    });
    Events.On("archive.expiring", (e) => {
      this.expiring = e.data as Expiring;
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
    }
    if (before !== s.state) {
      if (s.state === VaultState.StateUnlocked) {
        void this.refreshArchives();
        void this.refreshSlots();
        void this.refreshSettings();
        if (this.stat) void this.refreshArchive(); // sessionAlive follows the new session
        if (this.route === "lock") this.route = this.current ? "archive" : "archives";
      } else if (s.state === VaultState.StateLocked && before === VaultState.StateUnlocked) {
        void this.refreshArchives();
        if (this.stat) void this.refreshArchive();
      }
    }
  }

  private applyCeremony(c: CeremonyState): void {
    if (c.seq <= this.ceremonySeq) return;
    this.ceremonySeq = c.seq;
    this.ceremony = c;
  }

  // dismissCeremony clears a finished panel; the core has already forgotten it.
  dismissCeremony(): void {
    this.ceremony = null;
  }

  private applyOp(o: OpView, done = false): void {
    this.ops[o.id] = o;
    if (!done) return;
    if (o.error) {
      this.toast(`${opLabel(o.kind)}: ${codeText(o.error)}`, "error");
    } else {
      const failed = (o.results ?? []).filter((r) => r.outcome === "failed").length;
      if (failed > 0) this.toast(`${opLabel(o.kind)}: ${failed} file(s) failed`, "error");
    }
    if (o.archiveId && o.archiveId === this.current) void this.refreshArchive();
    void this.refreshArchives();
    setTimeout(() => {
      delete this.ops[o.id];
    }, 8000);
  }

  async refreshArchives(): Promise<void> {
    try {
      this.archives = (await Archives.List(this.showHidden)) ?? [];
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
      this.folder = "";
      this.route = "archive";
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
        this.leaveArchive();
      } else {
        this.toast(codeText(code), "error");
      }
    }
  }

  private pageToken = 0;

  async loadPage(): Promise<void> {
    const id = this.current;
    if (!id) return;
    const t = ++this.pageToken; // a superseded reply is dropped
    try {
      const page = await Archive.Page(id, this.folder, this.sortBy, 0, 2000);
      if (this.current !== id || t !== this.pageToken) return;
      this.page = page;
    } catch (e) {
      this.toast(codeText(errorOf(e).code), "error");
    }
  }

  async enterFolder(folder: string): Promise<void> {
    this.folder = folder;
    await this.loadPage();
  }

  leaveArchive(): void {
    this.current = null;
    this.stat = null;
    this.page = null;
    this.folder = "";
    if (this.route === "archive") this.route = "archives";
  }

  go(route: Route): void {
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

export function opLabel(kind: string): string {
  switch (kind) {
    case "add":
      return "Adding";
    case "replace":
      return "Replacing";
    case "extract":
      return "Extracting";
    case "save":
      return "Saving";
    case "verify":
      return "Verifying";
    case "compact":
      return "Compacting";
    case "rotate":
      return "Rotating key";
  }
  return kind;
}

export const store = new Store();

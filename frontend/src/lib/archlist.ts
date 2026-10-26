// The Archives list (APP.md §6, §13; direction 6, phase 4a): one archive
// selected at a time, no ticks — every action on this list takes one
// archive, so a checkbox selected for nothing (reopening 2026-09-10's
// "checkboxes on both lists" for this list alone; the file list keeps
// them). The selection is the page's one id, the details pane follows it,
// and the rules below are the list's shape: which records a view shows,
// what a row's second line says, which chips follow its name.

// ---- the views (View ▾; APP.md §13) -------------------------------------
// The list proper hides hidden and forgotten records; Show hidden lists
// the hidden ones beside the rest and never a forgotten one, which shows
// only in the Forgotten view — the Recycle Bin's logic: a deleted file is
// in the Bin, not among the hidden ones (ruled 2026-10-19). The Forgotten
// view lists the forgotten records alone, each with the date its key
// goes. Both read the whole
// registry (Archives.List(true)) and filter here, so a count the page
// gives — Forgotten (2) — is right whichever view is up.
export interface ViewRow {
  readonly hidden: boolean;
  readonly forgottenAt: number;
}

export type ListView = "list" | "forgotten";

export function viewRows<T extends ViewRow>(all: readonly T[], view: ListView, showHidden: boolean): T[] {
  if (view === "forgotten") return all.filter((a) => a.forgottenAt > 0);
  return all.filter((a) => !(a.forgottenAt > 0) && (showHidden || !a.hidden));
}

// ---- a row's second line -------------------------------------------------
// folderOf: the folder an archive's file is in — the path's parent, in the
// path's own separators — which is the row's second line when it has no
// description (the page cuts it from the left, so the end of the path,
// the folder's own name, is what stays). A drive's root keeps its
// separator ("D:\"), as Explorer writes it; a bare name has no folder.
export function folderOf(path: string): string {
  const p = (path ?? "").trim();
  const i = Math.max(p.lastIndexOf("/"), p.lastIndexOf("\\"));
  if (i < 0) return "";
  const head = p.slice(0, i);
  if (head === "" || /^[A-Za-z]:$/.test(head)) return head + p[i];
  // A UNC share's own root ("\\nas\x.efd" is "\\nas") reads as the
  // server, which is where the file is.
  return head;
}

// ---- the chips after a row's name ----------------------------------------
// What the row is, in the chips' words (chipCopy), on the name's own line,
// in this order: Missing — a button, Locate… for its row (direction 7) —
// Hidden, Forgotten, then what is happening to it: Open, or an operation
// on it. *Missing* is only ever the core's note, never a guess about a
// path nobody measured (APP.md §13). The Forgotten view says Forgotten of
// every row by being that view, so its rows do not.
export type ChipKind = "missing" | "hidden" | "forgotten" | "open" | "compacting" | "refreshing" | "rekeying";

export interface ChipRow extends RowFacts {
  readonly hidden: boolean;
  readonly open: boolean;
  readonly note?: string;
}

export function chipKinds(a: ChipRow, ops: readonly OpFacts[], view: ListView = "list"): ChipKind[] {
  const out: ChipKind[] = [];
  if (a.note === "archive.file_missing") out.push("missing");
  if (a.hidden) out.push("hidden");
  if (a.forgottenAt > 0 && view !== "forgotten") out.push("forgotten");
  // A compaction refuses the open and every operation until it ends, so
  // it says so first; a refresh or a re-key is queued behind the handle,
  // never refused, so the row says what runs and nothing is greyed for it.
  if (compacting(a, ops)) out.push("compacting");
  else {
    const k = runningKind(a, ops);
    if (k === "verify") out.push("refreshing");
    else if (k === "rotate") out.push("rekeying");
    else if (a.open) out.push("open");
  }
  return out;
}

// ---- the pane with nothing selected (direction 6) ------------------------
// The vault's summary: how many archives it holds and what they take,
// over every record but the forgotten ones (a forgotten record's file is
// on its way out of the vault), hidden ones included — the vault holds
// them all the same.
export interface FigureRow {
  readonly storedSize: number;
  readonly forgottenAt: number;
  readonly hidden?: boolean;
}

// hidden is how many of the counted are hidden: the summary names them
// apart while the list leaves them out, so its count squares with the
// title's "N archives shown".
export function vaultFigures(all: readonly FigureRow[]): { count: number; size: number; hidden: number } {
  let count = 0;
  let size = 0;
  let hidden = 0;
  for (const a of all) {
    if (a.forgottenAt > 0) continue;
    count++;
    if (a.hidden) hidden++;
    size += Number.isFinite(a.storedSize) && a.storedSize > 0 ? a.storedSize : 0;
  }
  return { count, size, hidden };
}

// ---- what stops an action on a row (APP.md §13, §2.3) --------------------
// The core refuses an archive two ways the row itself can say beforehand:
// a forgotten record answers archive.forgotten to Open, Compact, Re-key
// archive, Refresh fingerprint and Hide alike (§13), and an archive being compacted
// answers archive.compacting to every operation that needs its handle
// (§2.3) — Open itself mounts the handle, but the Archive page's first
// Page and Stat are refused, so an Open landed on an empty page under an
// error InfoBar, and Close is refused outright. The page greys those commands with the code's
// own words rather than letting each one end in an error InfoBar (ruled
// 2026-10-14). The codes are the core's strings, as the Status column's
// note is.
export interface RowFacts {
  readonly id: string;
  readonly forgottenAt: number;
  // the core's word for an open handle: open | compacting | needs_reopen
  readonly state?: string;
}

export interface OpFacts {
  readonly kind: string;
  readonly archiveId?: string;
  readonly finished: boolean;
}

// compacting: the row's handle says so, or a Compact of that archive is
// still running. The second is what the page hears first — the list is
// not re-read when a compaction starts, and the operation's strip is —
// and it covers the wait before the handle turns, which the archive's
// first page is refused across all the same.
export function compacting(a: RowFacts, ops: readonly OpFacts[]): boolean {
  return a.state === "compacting" || ops.some((o) => !o.finished && o.kind === "compact" && o.archiveId === a.id);
}

// runningKind: the operation running on this row that the core queues
// behind the handle rather than refusing — a Refresh fingerprint (verify)
// or a re-key (rotate) — so the row can say so with a chip while nothing
// is greyed for it; "" when neither runs. A compaction is compacting's.
export function runningKind(a: RowFacts, ops: readonly OpFacts[]): "" | "verify" | "rotate" {
  const o = ops.find((o) => !o.finished && o.archiveId === a.id && (o.kind === "verify" || o.kind === "rotate"));
  return o ? (o.kind as "verify" | "rotate") : "";
}

// rowRefusal: the code that stops Open, Compact, Re-key archive and Refresh
// fingerprint on this row, or "" when nothing does. Forgotten comes
// first: Restore is the way back, whatever else is going on.
export function rowRefusal(a: RowFacts, ops: readonly OpFacts[]): string {
  if (a.forgottenAt > 0) return "archive.forgotten";
  if (compacting(a, ops)) return "archive.compacting";
  return "";
}

// ---- the pane's Fingerprint fact (direction 9, APP.md §2.3 Verify) -----
// The fingerprint is the file's SHA-256 the registry keeps. Only *Refresh
// fingerprint* (Verify) and Compact record one; Create writes LastSeq 1
// with HashAtSeq 0 and every ordinary save moves LastSeq alone, so most
// records have never had one taken. The summary's hashBehind (LastSeq −
// HashAtSeq) cannot tell "never taken" from "taken N saves ago", so the
// fact also reads the record whole (Archives.Details), and is not drawn
// without it: while Locked there is no record to read, and the open
// archive's summary then carries hashBehind 0 for want of a registry,
// which would claim a match nobody measured.
export interface FingerprintRow {
  readonly hashBehind: number;
  readonly receiptOwed: boolean;
}

export interface FingerprintRecord {
  readonly lastSeq: number;
  readonly hashAtSeq: number;
  readonly lastCiphertextHash: string;
}

// none: never taken. current: taken at the registry's last save. behind:
// taken `behind` saves before it. owed: taken, but a save's receipt is
// still owed (a lock, a failed registry write), so the file has saves
// the registry has not counted and no number of them is known.
export type FingerprintKind = "none" | "current" | "behind" | "owed";

export interface Fingerprint {
  readonly kind: FingerprintKind;
  readonly behind: number;
}

// fingerprintOf: the fact, or null when there is no record to say it
// from. The record is read when the pane's row or its hashBehind changes,
// so for a moment it can be older than the summary; a hash once taken is
// never untaken (HashAtSeq only moves to a later save's seq), so a
// summary whose hashBehind has fallen below that record's LastSeq proves
// a hash was taken since, and the stale record is not believed over it.
export function fingerprintOf(row: FingerprintRow, rec: FingerprintRecord | null): Fingerprint | null {
  if (!rec) return null;
  const hashed = rec.hashAtSeq > 0 || /[^0\s]/.test(rec.lastCiphertextHash ?? "") || row.hashBehind < rec.lastSeq;
  if (!hashed) return { kind: "none", behind: 0 };
  if (row.receiptOwed) return { kind: "owed", behind: row.hashBehind };
  if (row.hashBehind <= 0) return { kind: "current", behind: 0 };
  return { kind: "behind", behind: row.hashBehind };
}

// ---- an .efd dropped on the Archives page (APP.md §14) -----------------
// droppedArchives: the files of a drop that name an archive by their
// extension, in the drop's order — the same files Explorer's double-click
// opens. A folder is never one, whatever it is called, and every other
// file is no business of this page's: a drop of nothing else opens
// nothing (ruled 2026-10-14).
export function droppedArchives(paths: readonly string[], isDir: readonly boolean[] = []): string[] {
  return paths.filter((p, i) => !isDir[i] && /\.efd$/i.test(p));
}

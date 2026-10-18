// The Archives list's ticks (APP.md §6, ruled 2026-09-10): the same rules
// as the file list's (lib/selection.ts) over the rows the filter shows,
// and the one rule of its own — which row the details pane shows. The
// list is whole and filtered on the page, so "every row" is the shown
// order and nothing is asked of the core.
import type { Selection } from "./selection";
import { emptySelection, selectAll } from "./selection";

// shownTicked: the header's checkbox is ticked exactly when every row the
// filter shows is, never a third state, and never for an empty list. A
// tick on a row the filter hides stays a tick and does not count.
export function shownTicked(s: Selection, shown: readonly string[]): boolean {
  return shown.length > 0 && shown.every((id) => s.ids.has(id));
}

// clickShownHeader: ticked, it clears; otherwise it ticks the rows the
// filter shows — those and no other, so the ticks are what is on screen.
export function clickShownHeader(s: Selection, shown: readonly string[]): Selection {
  return shownTicked(s, shown) ? emptySelection() : selectAll(s, shown);
}

// paneRow is the archive the details pane shows: the row last clicked
// while it is ticked, else the sole ticked row, else none — the pane and
// the ticks then never name different archives, and with several ticked
// and the last click gone the pane shows a count instead.
export function paneRow(s: Selection, lastClicked: string | null): string | null {
  if (lastClicked !== null && s.ids.has(lastClicked)) return lastClicked;
  if (s.ids.size === 1) return s.ids.values().next().value ?? null;
  return null;
}

// ---- what stops an action on a row (APP.md §13, §2.3) --------------------
// The core refuses an archive two ways the row itself can say beforehand:
// a forgotten record answers archive.forgotten to Open, Compact, Re-key
// archive, Refresh fingerprint and Hide alike (§13), and an archive being compacted
// answers archive.compacting to every operation that needs its handle
// (§2.3) — Open itself mounts the handle, but the Archive page's first
// Page and Stat are refused, so an Open landed on an empty page under an
// error toast, and Close is refused outright. The page greys those commands with the code's
// own words rather than letting each one end in an error toast (ruled
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

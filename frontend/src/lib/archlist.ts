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
// a forgotten record answers archive.forgotten to Open, Compact, Rotate
// key, Verify and Hide alike (§13), and an archive being compacted
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

// rowRefusal: the code that stops Open, Compact, Rotate key and Verify on
// this row, or "" when nothing does. Forgotten comes first: Restore is
// the way back, whatever else is going on.
export function rowRefusal(a: RowFacts, ops: readonly OpFacts[]): string {
  if (a.forgottenAt > 0) return "archive.forgotten";
  if (compacting(a, ops)) return "archive.compacting";
  return "";
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

// What a batch operation reports back (APP.md §3): one FileOutcome per
// record, folders among them. IsDir tells a directory's outcome from a
// file's — created (a directory record made) and entered (an existing one
// descended into) are a directory's, added and replaced a file's — and a
// per-item code says why one failed: file.kind_mismatch, file.tree_bounds,
// file.name, file.exists. The summary counts them and the failures are
// listed with the code's own copy.
import type { FileOutcome } from "./api";
import { leaf, plural } from "./format";
import { foreignPath } from "./paths";
import { codeText, resultsCopy } from "./strings";

export interface Tally {
  added: number;
  replaced: number;
  created: number;
  entered: number;
  skipped: number;
  extracted: number;
  failed: number;
}

export function tally(results: FileOutcome[] | null | undefined): Tally {
  const t: Tally = { added: 0, replaced: 0, created: 0, entered: 0, skipped: 0, extracted: 0, failed: 0 };
  for (const r of results ?? []) {
    // Exactly "is this one of my seven counters": `in` would answer for
    // an outcome word naming an Object.prototype member as well.
    if (Object.prototype.hasOwnProperty.call(t, r.outcome)) t[r.outcome as keyof Tally]++;
  }
  return t;
}

// summaryLine names what happened, folders beside files and in that order:
// "3 files added · 1 replaced · 2 folders created · 1 entered · 1 skipped ·
// 1 failed". Empty when nothing was reported.
export function summaryLine(t: Tally): string {
  const parts: string[] = [];
  if (t.added > 0) parts.push(`${plural(t.added, "file")} added`);
  if (t.replaced > 0) parts.push(`${t.replaced} replaced`);
  if (t.extracted > 0) parts.push(`${plural(t.extracted, "file")} extracted`);
  if (t.created > 0) parts.push(`${plural(t.created, "folder")} created`);
  if (t.entered > 0) parts.push(`${plural(t.entered, "folder")} entered`);
  if (t.skipped > 0) parts.push(`${t.skipped} skipped`);
  if (t.failed > 0) parts.push(`${t.failed} failed`);
  return parts.join(" · ");
}

export interface Trouble {
  name: string;
  isDir: boolean;
  outcome: string;
  text: string;
  // path: the item's file on disk, for its Show in Explorer — an add's
  // source; empty where there is no file to show (resultsPaths).
  path: string;
}

// troubles are the items the summary's counts are not enough for: what
// failed, with the code's copy, and what was skipped. A skipped directory
// is one entry, for its top (APP.md §3).
export function troubles(results: FileOutcome[] | null | undefined, kind = ""): Trouble[] {
  return (results ?? [])
    .filter((r) => r.outcome === "failed" || r.outcome === "skipped")
    .map((r) => ({
      name: r.name || leaf(r.path),
      isDir: r.isDir,
      outcome: r.outcome,
      text: r.code ? codeText(r.code) : r.outcome === "skipped" ? resultsCopy.skippedReason : resultsCopy.failedReason,
      path: shownPath(kind, r),
    }));
}

// shownPath is the file a row's Show in Explorer opens (direction 4): an
// add's or a replace's source, which is on disk whatever became of it,
// and an extract's skipped file, which is the one already at the
// destination. Never an archive path, never a destination that was not
// written, and never a path this platform could not open (paths.ts).
function shownPath(kind: string, r: FileOutcome): string {
  const p = r.path ?? "";
  if (!p || foreignPath(p)) return "";
  if (kind === "add" || kind === "replace") return p;
  if (kind === "extract" && r.outcome === "skipped") return p;
  return "";
}

// The verb a title says an item was not given, by the operation's kind.
const VERB: Record<string, string> = { add: "added", replace: "replaced", extract: "extracted" };

// unitOf names what the rows are: files, folders, or items when both.
function unitOf(rows: Trouble[]): string {
  const dirs = rows.filter((t) => t.isDir).length;
  if (dirs === 0) return "file";
  return dirs === rows.length ? resultsCopy.folder : "item";
}

// resultsTitle is the results dialog's title: the outcome, counted —
// "2 files were not added", "1 file was skipped", "2 files were not added
// and 1 was skipped" (direction 4). Never "What happened".
export function resultsTitle(kind: string, results: FileOutcome[] | null | undefined): string {
  const rows = troubles(results, kind);
  const failed = rows.filter((t) => t.outcome === "failed").length;
  const skipped = rows.length - failed;
  const unit = unitOf(rows);
  const verb = VERB[kind] ?? "done";
  if (failed > 0 && skipped > 0) return resultsCopy.notDoneAndSkipped(failed, unit, verb, skipped);
  if (failed > 0) return resultsCopy.notDone(failed, unit, verb);
  return resultsCopy.skipped(skipped, unit);
}

// rowName is a row's name as the dialog and its copy say it: a folder
// says so.
export function rowName(t: Trouble): string {
  return t.isDir ? `${t.name} (${resultsCopy.folder})` : t.name;
}

// detailsText is what Copy details puts on the clipboard: the title, the
// summary, and every row as the dialog shows it, one per line.
export function detailsText(title: string, summary: string, rows: Trouble[]): string {
  const head = summary ? [title, summary] : [title];
  return [...head, "", ...rows.map((t) => `${rowName(t)}: ${t.text}`)].join("\n");
}

export function hasTrouble(results: FileOutcome[] | null | undefined): boolean {
  return (results ?? []).some((r) => r.outcome === "failed" || r.outcome === "skipped");
}

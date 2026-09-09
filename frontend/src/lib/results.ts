// What a batch operation reports back (APP.md §3): one FileOutcome per
// record, folders among them. IsDir tells a directory's outcome from a
// file's — created (a directory record made) and entered (an existing one
// descended into) are a directory's, added and replaced a file's — and a
// per-item code says why one failed: file.kind_mismatch, file.tree_bounds,
// file.name, file.exists. The summary counts them and the failures are
// listed with the code's own copy.
import type { FileOutcome } from "./api";
import { leaf } from "./format";
import { codeText } from "./strings";

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

function plural(n: number, one: string): string {
  return `${n} ${one}${n === 1 ? "" : "s"}`;
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
}

// troubles are the items the summary's counts are not enough for: what
// failed, with the code's copy, and what was skipped. A skipped directory
// is one entry, for its top (APP.md §3).
export function troubles(results: FileOutcome[] | null | undefined): Trouble[] {
  return (results ?? [])
    .filter((r) => r.outcome === "failed" || r.outcome === "skipped")
    .map((r) => ({
      name: r.name || leaf(r.path),
      isDir: r.isDir,
      outcome: r.outcome,
      text: r.code
        ? codeText(r.code)
        : r.outcome === "skipped"
          ? "Skipped: something of that name is already here."
          : "It could not be done.",
    }));
}

export function hasTrouble(results: FileOutcome[] | null | undefined): boolean {
  return (results ?? []).some((r) => r.outcome === "failed" || r.outcome === "skipped");
}

// The conflicts an extract with the `ask` policy comes back with (APP.md
// §3, ruled 2026-09-10). The core extracts everything that collides with
// nothing and reports each collision as a `conflict` outcome carrying the
// record's id, the archive copy's size and date, and the existing file's;
// the page then asks — one question for one conflict, another for several
// — and re-issues Extract for the ids the user chose, with `replace` or
// `rename`. Nothing is walked: every row of the compare list and every id
// of the re-issue is read straight off the outcomes. All of it is a
// string-and-number rule, so all of it is here rather than in the
// component.
import type { FileOutcome } from "./api";
import { leaf } from "./format";
import { skipSameText } from "./strings";

// One conflict as the core reported it: id is the record's, which is what
// the re-issue names; path is the record's path inside the archive and
// dest where it would have gone on disk; size and modifiedAt are the
// archive's copy, existingSize and existingModifiedAt the file in the way.
export interface ConflictRow {
  id: string;
  path: string;
  dest: string;
  name: string;
  size: number;
  modifiedAt: number;
  existingSize: number;
  existingModifiedAt: number;
}

// What one row's ticks say. Replace takes the archive's copy over the one
// in the destination, skip leaves the destination's alone, and both keeps
// the two — the incoming one numbered, which is the `rename` policy.
export type Decision = "replace" | "skip" | "both";

// conflictsOf picks the conflicts out of an operation's outcomes. An
// outcome with no `existing` is still a conflict — the file in the way went
// between the refusal and the stat — and is asked about with what is known.
export function conflictsOf(results: FileOutcome[] | null | undefined): ConflictRow[] {
  return (results ?? [])
    .filter((r) => r.outcome === "conflict")
    .map((r) => ({
      id: r.id ?? "",
      path: r.name,
      dest: r.path,
      name: leaf(r.name || r.path),
      size: r.size ?? 0,
      modifiedAt: r.modifiedAt ?? 0,
      existingSize: r.existing?.size ?? 0,
      existingModifiedAt: r.existing?.modifiedAt ?? 0,
    }));
}

// hasConflicts: does an operation have a question to ask.
export function hasConflicts(results: FileOutcome[] | null | undefined): boolean {
  return (results ?? []).some((r) => r.outcome === "conflict");
}

// sameDateAndSize is the foot's rule: the two copies are the same file as
// far as a file manager can tell, so taking either changes nothing.
export function sameDateAndSize(r: ConflictRow): boolean {
  return r.size === r.existingSize && r.modifiedAt === r.existingModifiedAt;
}

// skipSameLabel is the foot's tick, empty when no two copies match.
export function skipSameLabel(rows: ConflictRow[]): string {
  return skipSameText(rows.filter(sameDateAndSize).length);
}

// startingDecisions: replace by default — the policy the dialog's own
// default is — with the copies that match already skipped, which is what
// the foot's pre-ticked box means.
export function startingDecisions(rows: ConflictRow[]): Record<string, Decision> {
  const out: Record<string, Decision> = {};
  for (const r of rows) out[r.path] = sameDateAndSize(r) ? "skip" : "replace";
  return out;
}

// withSkipSame applies the foot's tick to the rows it is about and leaves
// every other row as the user left it: ticking it skips the matching
// copies, unticking it takes them from the archive.
export function withSkipSame(rows: ConflictRow[], now: Record<string, Decision>, on: boolean): Record<string, Decision> {
  const out = { ...now };
  for (const r of rows) {
    if (sameDateAndSize(r)) out[r.path] = on ? "skip" : "replace";
  }
  return out;
}

// The ticks of one row, either way round: left is the archive's copy,
// right the destination's, and both is keep both.
export function ticks(d: Decision): { left: boolean; right: boolean } {
  return { left: d === "replace" || d === "both", right: d === "skip" || d === "both" };
}

// toggle is a click on one side. Unticking the only side that is ticked
// leaves neither, which takes nothing from the archive and leaves the
// destination's file where it is — a skip.
export function toggle(d: Decision, side: "left" | "right"): Decision {
  const t = ticks(d);
  const left = side === "left" ? !t.left : t.left;
  const right = side === "right" ? !t.right : t.right;
  if (left && right) return "both";
  if (left) return "replace";
  return "skip";
}

// The plan the *Continue* button carries out: one Extract for the records
// to be replaced and one for the records to be kept beside what is there,
// the skipped ones in neither. A conflict that came without an id — a
// core older than the outcome's shape — is dropped: nothing can be
// re-issued for an id that is not known.
export interface Reissue {
  replace: string[];
  rename: string[];
}

export function reissuePlan(rows: ConflictRow[], decisions: Record<string, Decision>): Reissue {
  const plan: Reissue = { replace: [], rename: [] };
  for (const r of rows) {
    if (!r.id) continue;
    const d = decisions[r.path] ?? "replace";
    if (d === "replace") plan.replace.push(r.id);
    else if (d === "both") plan.rename.push(r.id);
  }
  return plan;
}

export function planIsEmpty(p: Reissue): boolean {
  return p.replace.length === 0 && p.rename.length === 0;
}

// namesFor is what a conflict's re-issue carries in `names`: of the names
// the extract that met the conflict was issued with — what a *Shorten* or
// *Rename…* chose after a refusal — the ones for the ids re-issued, so
// the file is replaced or numbered under the name the destination
// accepted and not the record's own, which it had refused (APP.md §3: a
// collision under the new name follows policy). Null with none to carry,
// which is the usual case: an extract issued without names.
export function namesFor(ids: readonly string[], names: Record<string, string> | null | undefined): Record<string, string> | null {
  if (!names) return null;
  const out: Record<string, string> = {};
  for (const id of ids) {
    if (names[id] !== undefined) out[id] = names[id];
  }
  return Object.keys(out).length === 0 ? null : out;
}

// allOf is *Replace all* and *Skip all* taken over every row at once.
export function allOf(rows: ConflictRow[], d: Decision): Record<string, Decision> {
  const out: Record<string, Decision> = {};
  for (const r of rows) out[r.path] = d;
  return out;
}

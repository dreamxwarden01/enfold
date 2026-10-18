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
import { collisionCopy, compareGoText, skipSameText } from "./strings";
import type { KindCount } from "./strings";

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

// startingDecisions: every row whose date or size differs starts on
// replace, and the copies that match start skipped, which is what the
// foot's pre-ticked box means (ruled 2026-10-17). The extract dialog's own
// default became ask on 2026-10-14, but this list is reached by choosing
// to decide, and extracting into a folder that already has the files is,
// most of the time, putting new copies over old ones; adding only what is
// missing is the rarer wish, and it is one click a row, or *Skip these
// files* before the list, or *Cancel* in it.
// Starting with nothing ticked and Continue held until every row is
// answered was weighed and not taken: it taxes the common case to guard
// the rare one. *Continue* itself says what it will do (continueLabel).
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

// summarise counts what *Continue* will do with the ticks as they stand,
// by the plan's own rule: an undecided row is a replace, and a row with no
// id is a skip whatever its ticks say, since nothing is re-issued for it.
export function summarise(rows: ConflictRow[], decisions: Record<string, Decision>): { replace: number; both: number; skip: number } {
  const plan = reissuePlan(rows, decisions);
  const replace = plan.replace.length;
  const both = plan.rename.length;
  return { replace, both, skip: rows.length - replace - both };
}

// continueLabel is *Continue* reading what it will do with the ticks as
// they stand (direction 7): "Extract 2, skip 1", "Extract 3", "Skip 3" —
// the button says it, so no count line under the list repeats it (the
// 2026-10-17 count beside the foot's tick moved into the button).
export function continueLabel(rows: ConflictRow[], decisions: Record<string, Decision>): string {
  return compareGoText(summarise(rows, decisions));
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

// The add collisions (APP.md §3, CheckNames) as the dialog says them. A
// folder that meets a folder is a merge, not a clash: the add enters it
// whatever the policy, and the policy reaches only the files inside
// (internal/app/ops.go planNodes). So the title counts every name already
// there, but the rows count and speak of the clashes alone — files that
// meet files, and a file that meets a folder — and Replace is greyed
// whenever one of those is a file meeting a folder. With merges a note
// says what the choice does inside them; when every name is a merge the
// rows speak only of the files inside, which CheckNames cannot see.
export interface NameMeet {
  name: string;
  isDir: boolean;
  existingIsDir: boolean;
}

export interface PlanRow {
  label: string;
  says: string;
  off?: string;
}

export interface CollisionPlan {
  title: string;
  note: string;
  replace: PlanRow;
  keepBoth: PlanRow;
  skip: PlanRow;
}

function kindsOf(list: readonly NameMeet[]): KindCount {
  const c = { files: 0, folders: 0 };
  for (const m of list) {
    if (m.existingIsDir) c.folders++;
    else c.files++;
  }
  return c;
}

// shownName is a collision's name as the dialog says it: CheckNames hands
// back the name as offered, and a folder is offered with a trailing "/".
export function shownName(name: string): string {
  return name.endsWith("/") ? name.slice(0, -1) : name;
}

export function isMerge(m: NameMeet): boolean {
  return m.isDir && m.existingIsDir;
}

export function collisionPlan(list: readonly NameMeet[], offered: number, place: string): CollisionPlan {
  const clashes = list.filter((m) => !isMerge(m));
  const merges = list.length - clashes.length;
  const title = collisionCopy.title(kindsOf(list), shownName(list[0]?.name ?? ""), place);
  const note = merges > 0 ? collisionCopy.mergeNote(merges, clashes.length) : "";
  if (clashes.length === 0) {
    return {
      title,
      note,
      replace: { label: collisionCopy.replaceInside, says: collisionCopy.replaceInsideSays },
      keepBoth: { label: collisionCopy.keepBoth, says: collisionCopy.keepBothInsideSays },
      skip: { label: collisionCopy.skipInside, says: collisionCopy.skipInsideSays },
    };
  }
  const n = clashes.length;
  const kinds = kindsOf(clashes);
  const differ = clashes.some((m) => m.isDir !== m.existingIsDir);
  return {
    title,
    note,
    replace: { label: collisionCopy.replace(kinds, place), says: collisionCopy.replaceSays(n), off: differ ? collisionCopy.replaceOff : undefined },
    keepBoth: { label: collisionCopy.keepBoth, says: collisionCopy.keepBothSays(n) },
    skip: { label: collisionCopy.skip(kinds), says: collisionCopy.skipSays(kinds, offered - n) },
  };
}

// restExtracted is what the conflict question's Skip may call "the rest
// were extracted" (direction 7): the files the extract wrote besides the
// conflicts — or 0, so the clause is not said, when the same op also
// refused a name or a path or failed a file, since then the rest were not
// all extracted (those are asked about or named after the conflicts).
export function restExtracted(results: readonly FileOutcome[] | null | undefined): number {
  const rs = results ?? [];
  if (rs.some((r) => r.outcome === "failed" || r.outcome === "name_refused" || r.outcome === "path_refused")) return 0;
  return rs.filter((r) => r.outcome === "extracted").length;
}

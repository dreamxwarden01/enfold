// A name the destination refuses (APP.md §3, ruled 2026-09-10). A name is
// checked on the way in against R20 — Windows' own rules — so the name
// itself fits any Windows volume; what cannot be known beforehand is the
// destination's path length and the volume's own limit (an SMB share, an
// exFAT stick), and only its refusal says so. The core reports that record
// as a `name_refused` outcome — the final name refused on placement, where
// a shorter one may do — or `path_refused` — the temporary or a folder's
// path refused, where no name helps — with the rest of the batch written.
// The page then asks per refused record and re-issues Extract for the
// chosen ids with `names`, the one path element each is written under in
// that extract only. The rules — what is asked about, what *Shorten*
// produces, what the re-issue carries — are here, tested without a DOM.
import type { FileOutcome } from "./api";
import { leaf } from "./format";

export type RefusedKind = "name" | "path";

// One refused record as the core reported it: id is the record's, which
// the re-issue names; path is the record's path inside the archive; leaf
// is the path element the volume refused — the record's own name, or the
// name a previous *Shorten* or *Rename…* gave it, since the outcome's
// path is the one actually attempted.
export interface RefusedRow {
  id: string;
  kind: RefusedKind;
  path: string;
  leaf: string;
  isDir: boolean;
}

export function refusedOf(results: FileOutcome[] | null | undefined): RefusedRow[] {
  return (results ?? [])
    .filter((r) => r.outcome === "name_refused" || r.outcome === "path_refused")
    .map((r) => ({
      id: r.id ?? "",
      kind: r.outcome === "name_refused" ? "name" : "path",
      path: r.name,
      leaf: leaf(r.path) || leaf(r.name),
      isDir: r.isDir,
    }));
}

// hasRefusals: does an operation have a name to ask about.
export function hasRefusals(results: FileOutcome[] | null | undefined): boolean {
  return (results ?? []).some((r) => r.outcome === "name_refused" || r.outcome === "path_refused");
}

// splitName is the extension rule of APP.md §6: the part after the last
// dot, none when the dot is the first character (`.env`) or there is no
// dot; `gz` for `a.tar.gz`. ext keeps its dot, so stem + ext is the name.
export function splitName(name: string): { stem: string; ext: string } {
  const i = name.lastIndexOf(".");
  if (i <= 0) return { stem: name, ext: "" };
  return { stem: name.slice(0, i), ext: name.slice(i) };
}

// The names Windows reserves for devices, with or without an extension
// (R20): a stem *Shorten* must never halve its way down to — "console.txt"
// halved is "con.txt", which every Windows volume refuses and the core
// answers params for.
const DEVICES = new Set(["CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9"]);

function reserved(stem: string): boolean {
  const i = stem.indexOf(".");
  return DEVICES.has((i < 0 ? stem : stem.slice(0, i)).toUpperCase());
}

// shorten is *Shorten*'s one step: the extension kept whole, the stem
// halved at rune boundaries — Array.from splits a string at code points,
// so a surrogate pair is never cut in two — down to one rune. A stem that
// ends up ending in a space or a dot loses them (R20 refuses such a name,
// and the shell would strip them in silence); a stem of one rune has
// nothing left to halve, and null says so — the button is greyed then.
export function shorten(name: string): string | null {
  const { stem, ext } = splitName(name);
  const runes = Array.from(stem);
  if (runes.length <= 1) return null;
  let half = runes.slice(0, Math.floor(runes.length / 2)).join("").replace(/[ .]+$/, "");
  if (half === "") {
    // The first half was spaces and dots alone: the first rune that is
    // neither is the shortest name that is still a name.
    const first = runes.find((r) => r !== " " && r !== ".");
    if (first === undefined) return null;
    half = first;
  }
  const out = half + ext;
  if (out === name) return null;
  return reserved(half) ? shorten(out) : out;
}

// What the dialog decided for one refused record: *Shorten*, *Rename…* to
// a name typed, or *Skip*.
export type RefusedDecision = { kind: "shorten" } | { kind: "rename"; to: string } | { kind: "skip" };

// The re-issue *Shorten* and *Rename…* send: the ids chosen and the name
// each is written under in that extract only. A row skipped, a row that
// came without an id, or a *Shorten* with nothing left to shorten is in
// neither. The core answers params for a name that breaks R20, so a typed
// name is judged by the dialog before it gets here.
export interface RefusedPlan {
  ids: string[];
  names: Record<string, string>;
}

export function refusedPlan(rows: RefusedRow[], decisions: Record<string, RefusedDecision>): RefusedPlan {
  const plan: RefusedPlan = { ids: [], names: {} };
  for (const r of rows) {
    if (!r.id) continue;
    const d = decisions[r.id] ?? { kind: "skip" };
    let to: string | null = null;
    if (d.kind === "shorten") to = shorten(r.leaf);
    else if (d.kind === "rename") to = d.to;
    if (to === null || to === "") continue;
    plan.ids.push(r.id);
    plan.names[r.id] = to;
  }
  return plan;
}

export function planIsEmpty(p: RefusedPlan): boolean {
  return p.ids.length === 0;
}

// skipAllLike is *Skip all like it*: every row of that kind not yet
// decided is skipped, the rows already decided left as they are.
export function skipAllLike(rows: RefusedRow[], decisions: Record<string, RefusedDecision>, kind: RefusedKind): Record<string, RefusedDecision> {
  const out = { ...decisions };
  for (const r of rows) {
    if (r.kind === kind && !(r.id in out)) out[r.id] = { kind: "skip" };
  }
  return out;
}

// nextUndecided is the row the dialog asks about next: the first without
// a decision, or -1 when every row has one and the re-issue can go.
export function nextUndecided(rows: RefusedRow[], decisions: Record<string, RefusedDecision>): number {
  return rows.findIndex((r) => !(r.id in decisions));
}

// The foot as the status bar (direction 4, phase 3; APP.md §6): the pure
// part. The layer's foot is 34 px and always there, so an operation never
// moves the list; its left slot says the page's note at rest, the running
// operation while one runs, and how it ended — the done line — for a few
// seconds after. Its right slot is the lock chip, with the standing notices
// beside it while the vault is unlocked. Everything that decides what the
// foot says lives here, so vitest can feed it; LayerFoot.svelte draws it.
import type { FileOutcome, OpView } from "./api";
import { bytes, nearerDeadline } from "./format";
import { hasTrouble } from "./results";
import { hasConflicts } from "./conflicts";
import { hasRefusals } from "./refused";
import { ranked, tier } from "./notices";
import { doneCopy, footCopy, reclaimedText } from "./strings";

// ---- time left -----------------------------------------------------------
//
// The rate is measured, not promised: the bytes the last 5 s of progress
// moved, over those 5 s. Nothing is said for the first 3 s — the first
// file's open and the plan make the start slow, and a guess made then is
// the one users remember — and nothing once 10 s or less remain, when the
// line is about to end anyway. Minutes are rounded up, so "about 2 min"
// is never a promise kept late; under a minute is "less than a minute".

export const RATE_WINDOW = 5000;
export const SAY_AFTER = 3000;
export const QUIET_UNDER = 10000;

export interface Sample {
  t: number; // ms
  done: number; // bytes
}

// Track is what the foot remembers of one operation's progress: when it was
// first seen, and the samples of the last RATE_WINDOW.
export interface Track {
  since: number;
  samples: Sample[];
}

// track adds a sample. A sample that repeats the last one's bytes moves
// nothing; one whose bytes went back — a drag out's staging after its
// hover — starts the window again, since the rate before it measured
// something else.
export function track(prev: Track | undefined, s: Sample): Track {
  if (!prev) return { since: s.t, samples: [s] };
  const last = prev.samples[prev.samples.length - 1];
  if (last && s.done === last.done) return prev;
  if (last && s.done < last.done) return { since: prev.since, samples: [s] };
  return { since: prev.since, samples: [...prev.samples.filter((x) => x.t >= s.t - RATE_WINDOW), s] };
}

// timeLeft is the caption's second half, or "" when nothing is worth saying.
export function timeLeft(tr: Track | undefined, total: number, now: number): string {
  if (!tr || total <= 0 || now - tr.since < SAY_AFTER) return "";
  const w = tr.samples.filter((x) => x.t >= now - RATE_WINDOW);
  if (w.length < 2) return "";
  const a = w[0];
  const b = w[w.length - 1];
  const dt = b.t - a.t;
  const dd = b.done - a.done;
  if (dt <= 0 || dd <= 0) return "";
  const left = (total - b.done) / (dd / dt);
  if (left <= QUIET_UNDER) return "";
  if (left < 60000) return footCopy.underAMinute;
  return footCopy.minutesLeft(Math.ceil(left / 60000));
}

// progressCaption: "9.4 MB of 28 MB · about 2 min left", or the bytes
// alone, or nothing while the total is not known.
export function progressCaption(done: number, total: number, left = ""): string {
  if (total <= 0) return "";
  const b = footCopy.bytesOf(bytes(Math.min(done, total)), bytes(total));
  return left ? `${b} · ${left}` : b;
}

// fraction is the progress line's fill, 0 to 1, or null while the total is
// not known (the line's moving segment).
export function fraction(done: number, total: number): number | null {
  if (total <= 0) return null;
  return Math.max(0, Math.min(1, done / total));
}

// ---- which operation the foot shows ---------------------------------------

type Running = Pick<OpView, "id" | "startedAt" | "finished">;

// latestOp is the running operation the foot names: the one begun last,
// the last listed among those begun in the same second. The rest are
// behind the "+N" button.
export function latestOp<T extends Running>(ops: readonly T[]): T | null {
  let found: T | null = null;
  for (const o of ops) {
    if (o.finished) continue;
    if (!found || o.startedAt >= found.startedAt) found = o;
  }
  return found;
}

// ---- the rail's mark --------------------------------------------------------
//
// While anything runs the Archives item wears an 8 px accent dot; while
// exactly one runs and its total is known, a 14 px ring filled to its
// percentage. It is seen from every page, so a 48 GB extract started
// before going to the Vault page is still in sight.
export type RailMark = "none" | "dot" | { ring: number };

export function railMark(ops: readonly Pick<OpView, "finished" | "done" | "total">[]): RailMark {
  const running = ops.filter((o) => !o.finished);
  if (running.length === 0) return "none";
  if (running.length === 1 && running[0].total > 0) {
    return { ring: Math.round((Math.max(0, Math.min(running[0].done, running[0].total)) / running[0].total) * 100) };
  }
  return "dot";
}

// ---- the countdown's last minute ------------------------------------------
//
// Stay unlocked is offered in the last 60 s of the idle deadline only:
// it sends Activity, which moves the idle deadline and nothing else. The
// absolute cap's last minute offers nothing, since nothing extends it.
// The rule for which deadline is nearer is lockLine's (lib/format.ts).
export function staysUnlocked(locksAt: number, absoluteAt: number, nowMs: number): boolean {
  const cap = absoluteAt > 0 && (locksAt <= 0 || absoluteAt <= locksAt);
  if (cap || locksAt <= 0) return false;
  const left = nearerDeadline(locksAt, absoluteAt) - nowMs / 1000;
  return left > 0 && left <= 60;
}

// ---- the standing notices ---------------------------------------------------
//
// The status's warnings, ranked by tier (direction 9), with the Tampered
// one left out: its banner is the page's own, first in the body.
export function standingNotices(warnings: readonly string[] | null | undefined): string[] {
  return ranked((warnings ?? []).filter((w) => tier(w) !== "danger"));
}

// ---- the done line ----------------------------------------------------------

export type DoneAction = { kind: "reveal"; path: string } | { kind: "details"; op: OpView };

export interface DoneLine {
  // ok: the accent check; neutral: the info circle in the second ink — a
  // stop, or an ending that left something out.
  tone: "ok" | "neutral";
  text: string;
  action?: DoneAction;
}

const WRITTEN = new Set(["added", "replaced", "extracted"]);

// counted is how many files an operation wrote, and of how many: the
// file outcomes when there are any, else what it planned. Folders made
// along the way are not files written.
function counted(o: Pick<OpView, "items" | "results">): { written: number; of: number; folders: number } {
  const res: FileOutcome[] = o.results ?? [];
  const files = res.filter((r) => !r.isDir);
  const folders = res.filter((r) => r.isDir && r.outcome === "created").length;
  if (files.length === 0 && folders === 0) return { written: o.items, of: o.items, folders: 0 };
  const written = files.filter((r) => WRITTEN.has(r.outcome)).length;
  return { written, of: Math.max(o.items, files.length), folders };
}

function reveal(path: string | undefined): DoneAction | undefined {
  return path ? { kind: "reveal", path } : undefined;
}

// doneLine is what the foot says when an operation ends, or null when the
// ending is said elsewhere: a failure keeps its error InfoBar; a drag out
// that was a self-drop or cancelled said nothing to begin with. An
// extract that comes back with a question — conflicts under Ask, a name
// the destination refused — says what it did write, "Extracted 10 of 13
// files to …", and the page's question follows (one that wrote nothing
// says nothing: the question is its news); each re-issue it answers
// with is an extract of its own, with its own line. names maps an
// archive's id to its registry name (OpView carries the id alone).
export function doneLine(o: OpView, names: ReadonlyMap<string, string>): DoneLine | null {
  const name = names.get(o.archiveId ?? "") ?? "";
  const cancelled = o.error === "op.cancelled";
  if (o.error && !cancelled) return null;
  if (o.kind === "dragout") {
    if (o.dragResult !== "moved" && o.dragResult !== "copied") return null;
    return { tone: "ok", text: doneCopy.draggedOut(o.dragItems || o.items) };
  }
  if (cancelled) {
    switch (o.kind) {
      case "extract": {
        // The core stops between files and keeps what it wrote (APP.md
        // §2.3): the line says how many and where.
        const { written, of } = counted({ items: o.items, results: o.results ?? [] });
        const n = (o.results ?? []).length ? written : 0;
        if (n === 0) return { tone: "neutral", text: doneCopy.extractStoppedNone };
        return { tone: "neutral", text: doneCopy.extractStopped(n, of, o.destination ?? ""), action: reveal(o.destination) };
      }
      case "add":
        return { tone: "neutral", text: doneCopy.stoppedAdd(name) };
      case "replace":
        return { tone: "neutral", text: doneCopy.stoppedReplace(name) };
      case "verify":
        return { tone: "neutral", text: doneCopy.stoppedRefresh(name) };
      case "reclaim":
        return { tone: "neutral", text: doneCopy.stoppedReclaim(name) };
    }
    return { tone: "neutral", text: doneCopy.stoppedOther(name) };
  }
  const trouble = hasTrouble(o.results);
  const details: DoneAction = { kind: "details", op: o };
  switch (o.kind) {
    case "extract": {
      const { written, of } = counted(o);
      const dest = o.destination ?? "";
      // An extract that wrote nothing and comes back with a question — a
      // conflict under Ask, a name the destination refused — has nothing
      // to say yet and nothing to show: the question standing over the
      // page is its news, and its answer's re-issue gets its own line.
      if (written === 0 && (hasConflicts(o.results) || hasRefusals(o.results))) return null;
      const text = written < of ? doneCopy.extractedOf(written, of, dest) : doneCopy.extracted(written, dest);
      return { tone: trouble || written < of ? "neutral" : "ok", text, action: trouble ? details : reveal(dest) };
    }
    case "add": {
      const { written, of, folders } = counted(o);
      if (of === 0 && folders > 0 && !trouble) return { tone: "ok", text: doneCopy.addedFolders(folders, name) };
      if (trouble || written < of) return { tone: "neutral", text: doneCopy.addedOf(written, of, name), action: trouble ? details : undefined };
      return { tone: "ok", text: doneCopy.added(written, name) };
    }
    case "replace": {
      const { written, of } = counted(o);
      if (trouble || written < of) return { tone: "neutral", text: doneCopy.replacedOf(written, of, name), action: trouble ? details : undefined };
      return { tone: "ok", text: doneCopy.replaced(written, name) };
    }
    case "compact":
      return { tone: "ok", text: o.returned > 0 ? doneCopy.compactedBack(name, bytes(o.returned)) : doneCopy.compacted(name) };
    case "reclaim":
      // Nothing given back is nothing to say: the run was the core's own.
      return o.returned > 0 ? { tone: "ok", text: reclaimedText(o.returned) } : null;
    case "verify":
      return { tone: "ok", text: doneCopy.refreshed(name) };
    case "rotate":
      return { tone: "ok", text: doneCopy.rekeyed(name) };
  }
  return null;
}

// checkedLine is Find missing files' ending (APP.md §13, direction 4):
// what the pass looked at and what it found, with nothing to do from the
// line itself — the Missing chips in the list are the way on.
//
// A pass that measured nothing establishes nothing: every archive on a
// removable, network or UNC path (left unmeasured by §13's probe rules),
// an empty vault, or a pass behind a probe still held over its budget all
// answer probed 0. That is a neutral line with no claim of presence; "all
// files present" and the check are said only of a pass that checked some.
export function checkedLine(r: { probed: number; missing: number }): DoneLine {
  if (r.probed <= 0) return { tone: "neutral", text: doneCopy.checkedNone };
  return { tone: r.missing > 0 ? "neutral" : "ok", text: doneCopy.checked(r.probed, r.missing) };
}

// backupLine is the export's ending, said once its dialog is closed: where
// the backup went, and the way to it.
export function backupLine(path: string): DoneLine {
  return { tone: "ok", text: doneCopy.backupSaved(path), action: reveal(path) };
}

// ---- the done lines' turn -------------------------------------------------
//
// Two operations can end inside one line's 6 s — an add and a compaction
// started together end together — and each ending is said: a line that
// arrives while another is up waits its turn, and is said when that one's
// time is up or the next press in a page's body moves past it. At most
// DONE_WAITING wait; past that the oldest waiting goes unsaid, since a
// parade of old endings would keep the foot from saying what runs now.
// A new operation's start clears them all (the store's applyOp): the
// press that started it is answered by it, never by an old ending. An
// operation the core starts itself (a reclaim) clears nothing: see
// clearsDone.
export const DONE_WAITING = 3;

export interface DoneTurn<T> {
  shown: T | null;
  waiting: T[];
}

// clearsDone is whether an operation's start takes the line and its queue
// away (APP.md §6, *The foot*). One the user started does: the press is
// answered by the operation itself. A reclaim is the core's own run,
// which no press started. It begins straight after the commit that left
// the space behind (finishOp emits op.done, then reclaimIfWorth may
// start a reclaim at once), so clearing for it would erase the very
// ending it follows: "Replaced 1 file in Photos 2024" said and gone in
// the same moment, and every ending waiting behind it unsaid.
export function clearsDone(o: Pick<OpView, "kind">): boolean {
  return o.kind !== "reclaim";
}

// sayDone is a new ending's arrival: said at once when the foot is free,
// else queued behind the one up.
export function sayDone<T>(q: DoneTurn<T>, line: T): DoneTurn<T> {
  if (q.shown === null) return { shown: line, waiting: [] };
  return { shown: q.shown, waiting: [...q.waiting, line].slice(-DONE_WAITING) };
}

// nextDone is the shown line's end: the next waiting one, or none.
export function nextDone<T>(q: DoneTurn<T>): DoneTurn<T> {
  const [first, ...rest] = q.waiting;
  return { shown: first ?? null, waiting: rest };
}

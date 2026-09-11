// The drag out of the window, the page's half (APP.md §3, §6, ruled
// 2026-09-10 and 2026-09-11): one gesture serves the list's own moves and
// the drag out. Rows are not HTML5-draggable — a page's own drag cannot
// become the native one, and two drags cannot run at once — so a
// press-and-move over selected rows calls Shell.DragOut and the native
// drag runs from there, the page keeping the ids in flight. A release over
// Enfold's own window is a self-drop: nothing is extracted, and the page,
// receiving the WebView's drop on a folder row, the `..` row or the
// heading, performs the Move it always was; a drop elsewhere on the page
// is nothing. Real files dropped in from Explorer are told apart by their
// paths and go on being the add of §3.
//
// Everything that decides is here, over plain values, so it is tested
// without a DOM: when a press has become a drag, what a landing and the
// call's answer add up to — in either order, since the WebView's drop and
// the bound call's return travel different roads — and whether a drop,
// the WebView's or the shell's, is our own gesture or someone else's
// files (the outside review's finding 3: a self-drop answered over the
// window frame stays in flight a moment for its landing, and a real
// Explorer drop in that moment must not supply it and move the previous
// selection).
import { dropVerdict } from "./tree";
import { clean } from "./paths";

// A press becomes a drag once the pointer has moved past this many pixels
// on either axis — Explorer's own SM_CXDRAG / SM_CYDRAG default — so a
// click with a tremor in it stays a click.
export const DRAG_THRESHOLD = 4;

export function pastThreshold(dx: number, dy: number, threshold = DRAG_THRESHOLD): boolean {
  return Math.abs(dx) > threshold || Math.abs(dy) > threshold;
}

// Where a self-drop landed: a folder row, the `..` row (up one level) or
// the heading (the root) — the three surfaces a Move may be dropped on —
// or elsewhere on the page, which is nothing.
export type DropAt = "row" | "up" | "head";

export interface Landing {
  id: string;
  at: DropAt;
  // a row is a directory when it says so; `..` and the heading always are
  isDir: boolean;
}

// What the bound call answers, as much of it as the decision needs.
export interface DragAnswer {
  selfDrop: boolean;
  folder: string;
}

// Where the WebView's drop landed: on one of the three surfaces, elsewhere
// on the page, or — a drop that is not ours at all (ownNames) — foreign.
export type Landed = Landing | "elsewhere" | "foreign";

// A drag in flight: the ids it carries, their names as far as the page
// knows them, and the folder they were dragged out of, then — in whichever
// order they come — where the WebView's drop landed and what Shell.DragOut
// answered.
export interface Flight {
  ids: string[];
  // The names the ids carry, as far as the page has them: the rows loaded
  // say theirs, and a whole folder taken by Ctrl+A past the loaded rows
  // has ids the page cannot name. A drop is told to be ours by them.
  names: string[];
  from: string;
  landed?: Landed;
  answer?: DragAnswer;
  // the dragout operation's id, once the answer names it
  opId?: string;
}

// The Move a self-drop asks for: the ids in flight to the target landed
// on, and which surface that was, so a refusal is said against it.
export interface PendingMove {
  ids: string[];
  to: string;
  at: DropAt;
}

// wait: one of the two halves is still to come. move: a self-drop landed
// on a place the ids can move to. nothing: the drag is over with nothing
// for the page to do — it went out of the window, or the self-drop landed
// where a Move means nothing (a file row, the folder they are already in,
// a folder on itself, elsewhere on the page).
export type Verdict = { kind: "wait" } | { kind: "move"; move: PendingMove } | { kind: "nothing" };

export function beginFlight(ids: string[], from: string, names: string[] = []): Flight {
  return { ids: [...ids], names: [...names], from };
}

// land is the WebView's drop: the first landing counts, and a second drop
// event for the same flight changes nothing.
export function land(f: Flight, l: Landed): { flight: Flight; verdict: Verdict } {
  const flight = f.landed === undefined ? { ...f, landed: l } : f;
  return { flight, verdict: judge(flight) };
}

// answer is Shell.DragOut's return.
export function answer(f: Flight, a: DragAnswer, opId = ""): { flight: Flight; verdict: Verdict } {
  const flight: Flight = { ...f, answer: { selfDrop: a.selfDrop, folder: a.folder } };
  if (opId) flight.opId = opId;
  return { flight, verdict: judge(flight) };
}

function judge(f: Flight): Verdict {
  // Someone else's files landed while the ids were in flight: the drop
  // goes on being the add of §3, and this flight is over with nothing to
  // do — the answer, if it is still to come, has no landing to meet.
  if (f.landed === "foreign") return { kind: "nothing" };
  if (!f.answer) return { kind: "wait" };
  // The drag went out of the window: the strip follows the operation from
  // here, and no landing is coming.
  if (!f.answer.selfDrop) return { kind: "nothing" };
  if (f.landed === undefined) return { kind: "wait" };
  if (f.landed === "elsewhere") return { kind: "nothing" };
  if (dropVerdict({ id: f.landed.id, isDir: f.landed.isDir }, { ids: f.ids, from: f.from }) !== "move") return { kind: "nothing" };
  return { kind: "move", move: { ids: [...f.ids], to: f.landed.id, at: f.landed.at } };
}

// underFolder: the path lies beneath the folder — compared the way the
// core compares paths (lib/paths.ts clean: separators unified, case
// folded), since the shell hands the drop's paths back in whatever
// spelling the WebView gave them.
export function underFolder(path: string, folder: string): boolean {
  if (!path || !folder) return false;
  const root = clean(folder);
  const p = clean(path);
  return p.length > root.length + 1 && p.startsWith(root) && p[root.length] === "\\";
}

// ownNames: the drop carries our own gesture's items and not someone
// else's files. Shell.DragOut hands out one path per id at the top of the
// staging folder's items — a record under its own name, a folder record
// as its name with the subtree beneath — so the WebView's drop names one
// File per id, folders included, and the shell's drop one path per id:
// ours is a drop of exactly that many names among which every name the
// page knows is found, compared case-folded as the core compares names.
// Names the page does not know (rows past the loaded page) are judged by
// the count alone; a drop of fewer, more, or other names is Explorer's.
export function ownNames(dropNames: string[], f: Flight): boolean {
  if (dropNames.length === 0 || dropNames.length !== f.ids.length) return false;
  const have = new Set(dropNames.map((n) => n.toLowerCase()));
  return f.names.every((n) => have.has(n.toLowerCase()));
}

// baseName: the last segment of a path in either slash, for a shell drop
// told apart by names before the call has said where the folder is.
export function baseName(path: string): string {
  const i = Math.max(path.lastIndexOf("\\"), path.lastIndexOf("/"));
  return i < 0 ? path : path.slice(i + 1);
}

// ownDrop: the shell's drop carries our own staged paths, not files from
// Explorer. The files never exist, so an add of them would only fail; the
// Move, if there is one, is the landing's business. The paths say whose
// they are: the drag's folder — Shell.DragOut's answer, or the last one
// it gave, since a self-drop's drop can reach the page after the call has
// answered and the flight is over — holds every path it handed out, under
// its items. Before the call has answered no folder is known, and the
// paths' own names are told against the flight's instead. A flight alone
// makes nothing ours: a self-drop answered over the window frame stays in
// flight a moment for its landing, and a real file from Explorer dropped
// in that moment is the add it always was.
export function ownDrop(paths: string[], flight: Flight | null, lastFolder: string): boolean {
  if (paths.length === 0) return false;
  const folder = flight?.answer?.folder || lastFolder;
  if (folder && paths.every((p) => underFolder(p, folder))) return true;
  return !!flight && !flight.answer && ownNames(paths.map(baseName), flight);
}

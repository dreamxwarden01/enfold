// The archive's index is a tree (APP.md §3, FORMAT.md R39): the page holds
// a directory *id*, never a path, and every call that names a place names
// it by id. This module is the page's pure tree logic — the root's
// spelling, the walk a vanished folder asks for, what a drag may be
// dropped on, and how a selection is counted for the delete confirmation —
// so it can be tested without a DOM or a core.
import { plural } from "./format";

// The root is the all-zero id: the same value the format writes as a
// top-level record's parent_id, spelled one way by the core, the page, the
// bindings and the mock (APP.md §3).
export const ROOT_ID = "00000000000000000000000000000000";

export function isRootId(id: string): boolean {
  return id === ROOT_ID;
}

export interface CrumbLike {
  id: string;
  name: string;
}

// retryChain: the ids to try, in order, when Page answered file.not_found
// for dirId — the page held a folder that has gone (a Delete took it, or
// took an ancestor of it). The chain is the crumbs
// the page last held, walked upwards from just above dirId, and it always
// ends at the root, which always answers (APP.md §3). Crumbs that do not
// name dirId at all are the chain whole: the page had just stepped into a
// folder it never got a listing for, so everything it does hold is above
// it.
export function retryChain(crumbs: CrumbLike[], dirId: string): string[] {
  const at = crumbs.findIndex((c) => c.id === dirId);
  const above = at >= 0 ? crumbs.slice(0, at) : crumbs.slice();
  const chain = above.map((c) => c.id).reverse();
  if (chain[chain.length - 1] !== ROOT_ID) chain.push(ROOT_ID);
  return chain.filter((c) => c !== dirId);
}

// shownDir: the folder the page actually stands in, read from the crumbs
// of the listing on screen — the last one, the crumbs being root-inclusive
// and never empty. It is what the page falls back to when a listing does
// not arrive at all (the archive is compacting, the session went): the
// folder id is what every write names — the drop target's data-dir-id,
// Create folder, Add files, Add folder — so it must never name a folder
// the user is not looking at (APP.md §3, DESIGN.md trap 31). With no
// listing at all the page stands at the root.
export function shownDir(crumbs: CrumbLike[] | null | undefined): string {
  const list = crumbs ?? [];
  return list[list.length - 1]?.id ?? ROOT_ID;
}

// wentName: what to call the folder that went, from the crumbs the page
// held. Empty when they do not name it — the caller then falls back to the
// name it was entering, or says "That folder".
export function wentName(crumbs: CrumbLike[], dirId: string): string {
  return crumbs.find((c) => c.id === dirId)?.name ?? "";
}

// A drag of the selection onto a folder row or a crumb is a Move (APP.md
// §6). What the page can judge for itself is here; the rest — a folder
// dragged into its own descendant, which the page cannot see from one
// listing, a name the destination already holds, the tree's bounds — is
// the core's, refused whole and in place (file.move_into_self,
// file.exists, file.tree_bounds).
export interface DropTarget {
  id: string;
  // a crumb is a directory; a row is one when it says so
  isDir: boolean;
}

export interface DragState {
  // the records being dragged
  ids: string[];
  // the directory they are being dragged out of
  from: string;
}

// move: the drop is a Move. here: the target is the folder they are
// already in — the row's own folder, or the last crumb — and a drop is a
// no-op. self: a folder cannot be dropped on itself. file: a file row is
// not a place. nothing: an empty drag. A row deleted is no row at all
// since 2026-09-09: a delete commits at once, so nothing on screen is a
// tombstone.
export type DropVerdict = "move" | "here" | "self" | "file" | "nothing";

export function dropVerdict(target: DropTarget, drag: DragState): DropVerdict {
  if (drag.ids.length === 0) return "nothing";
  if (!target.isDir) return "file";
  if (drag.ids.includes(target.id)) return "self";
  if (target.id === drag.from) return "here";
  return "move";
}

export function canDrop(target: DropTarget, drag: DragState | null): boolean {
  return !!drag && dropVerdict(target, drag) === "move";
}

// The selection may hold folders as well as files, and the delete
// confirmation names them separately, because a folder takes everything
// beneath it (APP.md §3).
export interface Kinded {
  isDir: boolean;
  name: string;
}

export interface Counts {
  files: number;
  folders: number;
}

export function deleteCounts(rows: Kinded[]): Counts {
  return {
    files: rows.filter((r) => !r.isDir).length,
    folders: rows.filter((r) => r.isDir).length,
  };
}

// countPhrase: "1 file", "2 folders", "2 files and 1 folder"; empty for
// nothing at all.
export function countPhrase(c: Counts): string {
  const parts: string[] = [];
  if (c.files > 0) parts.push(plural(c.files, "file"));
  if (c.folders > 0) parts.push(plural(c.folders, "folder"));
  return parts.join(" and ");
}

// The delete dialog, worded as APP.md §3 and §6 word it: "Permanently
// delete 3 files and 1 folder from ECON 280? A folder takes everything
// beneath it. This cannot be undone." The question names the counts and
// the archive; the folder sentence appears only when a folder is in the
// selection; there is no undo anywhere in the copy, deletion being
// cryptographic erasure (FORMAT.md R32).
export function deleteTitle(rows: Kinded[], archiveName = ""): string {
  const what = countPhrase(deleteCounts(rows));
  const from = archiveName ? ` from ${archiveName}` : "";
  return `Permanently delete ${what}${from}?`;
}

export function deleteBody(rows: Kinded[]): string {
  const folders = deleteCounts(rows).folders > 0 ? "A folder takes everything beneath it. " : "";
  return `${folders}This cannot be undone.`;
}

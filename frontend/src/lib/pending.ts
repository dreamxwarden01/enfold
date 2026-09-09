// Folders created on the Archive page (APP.md §6, the Archive page). The
// format keeps paths, not folders (FORMAT.md §7): a folder is nothing but
// the prefix its files' names share, so a folder with no file in it
// cannot be written and does not exist. *Create folder* therefore makes a
// row, not a folder: the path lives here, in the page, until a file is
// added into it — from then on the archive lists it itself, as the
// prefix of that file's name, and the page lets go of it. A folder no
// file was added into is dropped at Save, at Discard, and when the
// archive is left; nothing of it was ever sent to the core.
import { fileNameProblem } from "./validate";

export const NAME_TAKEN = "There is already something with that name here.";

// The stored name's separator is "/" (FORMAT.md §7.1), whatever the
// machine that added the file used.
export function parentOf(path: string): string {
  const i = path.lastIndexOf("/");
  return i < 0 ? "" : path.slice(0, i);
}

export function leafOf(path: string): string {
  const i = path.lastIndexOf("/");
  return i < 0 ? path : path.slice(i + 1);
}

export function joinPath(folder: string, name: string): string {
  return folder ? `${folder}/${name}` : name;
}

// pendingIn: the created folders that belong in a listing of `folder` —
// those made directly under it — in name order.
export function pendingIn(pending: string[], folder: string): string[] {
  return pending.filter((p) => parentOf(p) === folder).sort((a, b) => leafOf(a).localeCompare(leafOf(b)));
}

// reconcile: what is still the page's after a listing of `folder` came
// back. A folder the archive itself now reports at that level is real —
// a file was added into it, so its name is a prefix the core knows — and
// the page drops its copy rather than showing the folder twice. A
// listing says nothing about folders anywhere else, which are left alone.
export function reconcile(pending: string[], folder: string, realFolders: string[]): string[] {
  const real = new Set(realFolders);
  return pending.filter((p) => parentOf(p) !== folder || !real.has(leafOf(p)));
}

// addPending: the list with one folder created under `folder`. The same
// path twice is the same folder, and adds nothing.
export function addPending(pending: string[], folder: string, name: string): string[] {
  const path = joinPath(folder, name.trim());
  return pending.includes(path) ? pending : [...pending, path];
}

// createProblem: what a name typed into *Create folder* is missing — the
// rule the rename obeys, and then the names this folder already holds,
// compared the way a file system compares them, since two rows of one
// name are one folder with two rows.
export function createProblem(name: string, taken: string[]): string {
  const said = fileNameProblem(name);
  if (said) return said;
  const leaf = name.trim().toLowerCase();
  return taken.some((t) => t.toLowerCase() === leaf) ? NAME_TAKEN : "";
}

// dropTo: where the page must stand once the created folders are dropped.
// A folder that was only ever the page's is gone with them, and so is
// everything under it, so the user is left at the nearest place that
// outlives the drop.
export function dropTo(pending: string[], folder: string): string {
  if (!folder) return "";
  const gone = new Set(pending);
  const segs = folder.split("/");
  for (let i = 1; i <= segs.length; i++) {
    const at = segs.slice(0, i).join("/");
    if (gone.has(at)) return parentOf(at);
  }
  return folder;
}

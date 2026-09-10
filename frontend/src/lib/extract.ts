// The extract dialog's destination field (APP.md §3, the extract dialog,
// ruled 2026-09-10). The core keeps no folder from the last time, so the
// field is prefilled by one rule: *Extract all* goes to the archive's own
// folder plus a folder named after the archive, a selection to the
// archive's own folder with no extra level. Everything the field does to a
// string is here, so it can be tested without a DOM.
import { policyLabels } from "./strings";

// The characters Windows refuses in a path element (APP.md §3 names these
// nine). A trailing dot or space is refused too — the shell strips them
// silently, which would leave a folder whose name is not the one shown.
const REFUSED = /[\\/:*?"<>|]/g;

// archiveFolderName: the folder named after the archive, sanitised. Empty
// when nothing usable is left, which is the caller's cue to add nothing
// rather than a folder called after no one.
export function archiveFolderName(archiveName: string): string {
  return (archiveName ?? "").replace(REFUSED, "").replace(/[. ]+$/, "").trim();
}

// parentFolder: the folder a file lies in — the archive's own folder, from
// the record's path. A path at the root of a drive or a share keeps its
// separator ("D:\ECON 280.efd" is in "D:\", not in "D:"), and a bare file
// name has no folder at all, which leaves the field empty rather than
// naming a relative place the core would resolve somewhere else.
export function parentFolder(path: string): string {
  const s = (path ?? "").replace(/[\\/]+$/, "");
  const i = Math.max(s.lastIndexOf("\\"), s.lastIndexOf("/"));
  if (i < 0) return "";
  const head = s.slice(0, i);
  return head === "" || /^[A-Za-z]:$/.test(head) ? s.slice(0, i + 1) : head;
}

// appendArchiveFolder adds the folder named after the archive to a
// destination. The folder is appended with a backslash, the destination's
// own trailing separators are not doubled, and an empty destination is
// left alone (there is nothing to append to, and a bare folder name would
// be a relative path the core would resolve against somewhere the user
// never chose). It is always one level more: an archive that already sits
// in a folder of its own name — D:\Archives\Photos\Photos.efd — still
// extracts into D:\Archives\Photos\Photos and never into the folder beside
// itself, which is what the rule of APP.md §3 says and what keeps the
// extracted files apart from the archive file with *Replace* the default.
// (The "not twice" exception belonged to the old "+ a folder" button,
// which could be pressed twice; the prefill runs once.)
export function appendArchiveFolder(dest: string, archiveName: string): string {
  const folder = archiveFolderName(archiveName);
  if (!folder) return dest;
  const base = dest.replace(/[\\/]+$/, "");
  if (base.trim() === "") return dest;
  return `${base}\\${folder}`;
}

// destinationFor is the prefill rule of APP.md §3 whole: *Extract all* goes
// to the archive's folder plus a folder named after the archive
// ("D:\Archives\ECON 280" for "D:\Archives\ECON 280.efd"), a selection to
// the archive's folder itself. An archive whose path is not known leaves
// the field empty, for *Browse…* to fill.
export function destinationFor(archivePath: string, archiveName: string, all: boolean): string {
  const folder = parentFolder(archivePath);
  return all ? appendArchiveFolder(folder, archiveName) : folder;
}

// The four policies of APP.md §3, in the order the dialog offers them.
// Replace is the default — the wizard default of every archiver — and
// *Ask me about each conflict* is the one that comes back with a question.
// The words are the strings table's (APP.md §7).
export const POLICIES: { value: string; label: string }[] = ["replace", "skip", "rename", "ask"].map((value) => ({
  value,
  label: policyLabels[value],
}));

export const DEFAULT_POLICY = "replace";

// destinationEmpty: an empty destination marks the field and *Extract*
// does nothing. The destination need not exist — the core creates it —
// so nothing else about it is judged here.
export function destinationEmpty(dest: string): boolean {
  return dest.trim() === "";
}

// The extract dialog's scope and destination field (APP.md §3, the
// extract dialog, ruled 2026-09-10; one Extract… with a scope, direction
// 7). The core keeps no folder from the last time, so the field is
// prefilled by one rule per scope: everything in the archive goes to the
// archive's own folder plus a folder named after the archive, a selection
// to the archive's own folder with no extra level. Everything the dialog
// does to a string is here, so it can be tested without a DOM.
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
// extracted files apart from the archive file, whatever the policy.
// (The "not twice" exception belonged to the old "+ a folder" button,
// which could be pressed twice; the prefill runs once.)
export function appendArchiveFolder(dest: string, archiveName: string): string {
  const folder = archiveFolderName(archiveName);
  if (!folder) return dest;
  const base = dest.replace(/[\\/]+$/, "");
  if (base.trim() === "") return dest;
  return `${base}\\${folder}`;
}

// destinationFor is the prefill rule of APP.md §3 whole: everything goes
// to the archive's folder plus a folder named after the archive
// ("D:\Archives\ECON 280" for "D:\Archives\ECON 280.efd"), a selection to
// the archive's folder itself. An archive whose path is not known leaves
// the field empty, for *Browse…* to fill.
export function destinationFor(archivePath: string, archiveName: string, all: boolean): string {
  const folder = parentFolder(archivePath);
  return all ? appendArchiveFolder(folder, archiveName) : folder;
}

// The dialog's scope line (direction 7): the toolbar's one *Extract…* —
// and Ctrl+E, and the pane's *Extract…* — opens one dialog whose first
// question is what to extract: the selection, or everything in the
// archive. *Everything* sends the root id alone, so the page walks
// nothing (APP.md §3).
export type ExtractScope = "selection" | "all";

// startingScope: the selection when there is one, everything when there
// is none — the selection's radio is greyed then, with its reason.
export function startingScope(selectedCount: number): ExtractScope {
  return selectedCount > 0 ? "selection" : "all";
}

// destinationForScope is the 2026-09-10 rule applied to a scope.
export function destinationForScope(archivePath: string, archiveName: string, scope: ExtractScope): string {
  return destinationFor(archivePath, archiveName, scope === "all");
}

// followScope is the field when the scope changes: the new scope's
// prefill while the field still holds the old scope's, and the field as
// the user left it once they have typed or browsed something else — the
// field is the user's from the first change they make, and a scope
// change never takes it back. A field emptied by the user is theirs too.
export function followScope(dir: string, from: ExtractScope, to: ExtractScope, archivePath: string, archiveName: string): string {
  if (dir !== destinationForScope(archivePath, archiveName, from)) return dir;
  return destinationForScope(archivePath, archiveName, to);
}

// The four policies of APP.md §3, in the order the dialog offers them —
// WinRAR's own order, the default first. *Ask me about each conflict* is
// the default (ruled 2026-10-14, reversing the 2026-09-10 "Replace, the
// wizard default of every archiver": WinRAR's help marks *Ask before
// overwrite* as its default, and Explorer's Extract All asks per
// conflict), and it is the one that comes back with a question, so a
// file already there is never overwritten unless the user says so. The
// words are the strings table's (APP.md §7).
export const POLICIES: { value: string; label: string }[] = ["ask", "replace", "skip", "rename"].map((value) => ({
  value,
  label: policyLabels[value],
}));

export const DEFAULT_POLICY = "ask";

// The policy the dialog opens on: the last one chosen in an extract the
// user went ahead with, for as long as Enfold runs, else the default. It
// is kept in memory only — no setting holds it and nothing is written to
// the vault for it — and the destination is still never kept (2026-09-10).
let lastPolicy = DEFAULT_POLICY;

export function startingPolicy(): string {
  return lastPolicy;
}

// rememberPolicy keeps the policy of an extract the user went ahead with;
// a word the core does not take is not kept.
export function rememberPolicy(policy: string): void {
  if (POLICIES.some((p) => p.value === policy)) lastPolicy = policy;
}

// destinationEmpty: an empty destination marks the field and *Extract*
// does nothing. The destination need not exist — the core creates it —
// so nothing else about it is judged here.
export function destinationEmpty(dest: string): boolean {
  return dest.trim() === "";
}

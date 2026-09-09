// The extract dialog's destination field (APP.md §3, the extract dialog).
// The native folder picker cannot prefill the name of a folder the user
// creates, so the page holds an editable destination and one action that
// appends a folder named after the archive — WinRAR's destination field,
// in effect. Everything the field does to a string is here, so it can be
// tested without a DOM.

// The characters Windows refuses in a path element (APP.md §3 names these
// nine). A trailing dot or space is refused too — the shell strips them
// silently, which would leave a folder whose name is not the one shown.
const REFUSED = /[\\/:*?"<>|]/g;

// archiveFolderName: what "+ a folder named after the archive" would add.
// Empty when nothing usable is left, which is the caller's cue to add
// nothing rather than a folder called after no one.
export function archiveFolderName(archiveName: string): string {
  return (archiveName ?? "").replace(REFUSED, "").replace(/[. ]+$/, "").trim();
}

// lastSegment: the destination's own last folder name, with any trailing
// separator ignored.
export function lastSegment(dest: string): string {
  const parts = dest.replace(/[\\/]+$/, "").split(/[\\/]/);
  return parts[parts.length - 1] ?? "";
}

// appendArchiveFolder: the action's whole rule. The folder is appended
// with a backslash, the destination's own trailing separators are not
// doubled, an empty destination is left alone (there is nothing to append
// to, and a bare folder name would be a relative path the core would
// resolve against somewhere the user never chose), and a destination that
// already ends in that folder is left alone as well, so a second press
// does not make "…\Photos 2024\Photos 2024".
export function appendArchiveFolder(dest: string, archiveName: string): string {
  const folder = archiveFolderName(archiveName);
  if (!folder) return dest;
  const base = dest.replace(/[\\/]+$/, "");
  if (base.trim() === "") return dest;
  if (lastSegment(base).toLowerCase() === folder.toLowerCase()) return base;
  return `${base}\\${folder}`;
}

// destinationEmpty: an empty destination marks the field and *Extract*
// does nothing. The destination need not exist — the core creates it —
// so nothing else about it is judged here.
export function destinationEmpty(dest: string): boolean {
  return dest.trim() === "";
}

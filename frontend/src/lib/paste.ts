// Paste (APP.md §6; ruled 2026-10-26, the user's: files copied in Explorer
// could not be pasted into an archive): Ctrl+V on the file list and *Paste*
// first in its blank-area menu add the files the clipboard names to the
// folder shown, through the add a drop from Explorer takes — CheckNames,
// the collisions dialog, AddFiles and AddFolder, the done line. The list
// is Explorer's CF_HDROP, read by the core (Shell.ClipboardFiles, APP.md
// §3): the WebView's own clipboard holds no files. A list cut in Explorer
// is added as copied; nothing is ever deleted from the disk by a paste.
// There is no *Copy* of archive files to the clipboard — that would be an
// extraction by another name, and the drag out stays the one way out.
//
// Pure: the page holds what the clipboard was last read to hold, asks the
// core, and maps the answer through here.
import type { Clipboard as ClipboardView } from "./api";

// AddPlan is one add as the page runs it: files to AddFiles, each
// directory to AddFolder, all under one folder.
export interface AddPlan {
  files: string[];
  dirs: string[];
}

// planOf splits a list of paths by whether each is a directory, as the
// shell says it (a drop's isDir, the clipboard's ClipFile.isDir): the page
// cannot stat one, and a directory handed to AddFiles is one failed
// outcome (APP.md §3). A path with no word on it is a file.
export function planOf(paths: readonly string[], isDir: readonly boolean[] = []): AddPlan {
  return {
    files: paths.filter((_, i) => !isDir[i]),
    dirs: paths.filter((_, i) => !!isDir[i]),
  };
}

// Clip is what the blank-area menu knows of the clipboard as it draws
// *Paste*: "pending" while the read is on its way (the item is enabled and
// a press waits for the answer), "unknown" when the read failed (enabled:
// a press reads again and says why it failed), "empty" (greyed, "Nothing
// to paste"), "copy" and "move" (enabled; a move — files cut in Explorer —
// carries the caption that the originals stay where they are).
export type Clip = "pending" | "unknown" | "empty" | "copy" | "move";

// clipOf reads the core's answer; null is a read that failed.
export function clipOf(c: ClipboardView | null): Clip {
  if (!c) return "unknown";
  if ((c.files ?? []).length === 0) return "empty";
  return c.move ? "move" : "copy";
}

// pastePlan is the add a paste runs, or null when the clipboard names no
// files. The move bit plays no part: a cut is added as copied.
export function pastePlan(c: ClipboardView): AddPlan | null {
  const files = (c.files ?? []).filter((f) => !!f.path);
  if (files.length === 0) return null;
  return planOf(
    files.map((f) => f.path),
    files.map((f) => f.isDir),
  );
}

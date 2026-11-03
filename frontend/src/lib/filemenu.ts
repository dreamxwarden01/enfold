// The file list's menus (direction 7, Explorer's habits; APP.md §6): what
// a right-click on a row, on the list's blank area, or Shift+F10 and the
// Menu key offer. Pure — the page maps each cmd to the one function its
// toolbar, its keys and the pane already run.
//
// A file: *Open* (the default, bold, Enter — the viewer's; never Extract,
// which is always a named command, ruled 2026-10-17) · *Extract…* · ─ ·
// *Rename…* · *Move to…* · ─ · *Delete…* · ─ · *Copy name*. A folder: the
// same without *Copy name*, its *Open* entering it. The `..` row has none.
// "Extract here" is not offered — Enfold has no "here" on disk; Extract…
// with its prefilled destination is the one command — and neither is
// "Properties": the pane is the facts.
//
// Who a command acts on is the page's: Extract…, Move to… and Delete… take
// the whole selection, Rename… and Copy name the row clicked.
import { groups } from "./menu";
import type { CmdItem } from "./menu";
import type { Clip } from "./paste";
import { archivePageCopy, commandCopy, keyNames } from "./strings";

export type FileCmd = "open" | "extract" | "rename" | "moveTo" | "delete" | "copyName";
export type FileBlankCmd = "paste" | "createFolder" | "addFiles" | "addFolder";

// Stop: what stops the archive's own commands now, and why, said as each
// greyed item's second line. Two states grey by this one path (the busy
// archive, ruled 2026-11-03): the drain after the archive's *Close* (APP.md
// §2.3), when every page-facing call answers archive.not_open, so *Open*
// is greyed too (reads); and an operation still running on the archive
// (lib/ops.ts busyReason), when what would write waits behind it but a
// read does not, so *Open* stays.
export interface Stop {
  why: string;
  reads: boolean;
}

// The drain's stop, in the menu's few words.
export const CLOSING: Stop = { why: archivePageCopy.menuClosing, reads: true };

const offFor = (why: string | undefined) => (why === undefined ? {} : { disabled: true, title: why });

// Every command is greyed with the stop's reason but *Copy name*, which
// asks nothing of the archive, and *Open* while only writes are stopped.
export function fileRowItems(isDir: boolean, stop?: Stop): CmdItem<FileCmd>[] {
  const off = offFor(stop?.why);
  const open: CmdItem<FileCmd> = { cmd: "open", label: commandCopy.open, icon: "i-open", keys: keyNames.open, isDefault: true, ...offFor(stop?.reads ? stop.why : undefined) };
  return groups<CmdItem<FileCmd>>(
    [open, { cmd: "extract", label: commandCopy.extract, icon: "i-extract", keys: keyNames.extract, ...off }],
    [
      { cmd: "rename", label: commandCopy.rename, icon: "i-rename", keys: keyNames.rename, ...off },
      { cmd: "moveTo", label: commandCopy.moveTo, icon: "i-move", keys: keyNames.moveTo, ...off },
    ],
    [{ cmd: "delete", label: commandCopy.delete, icon: "i-trash", keys: keyNames.delete, danger: true, ...off }],
    isDir ? [] : [{ cmd: "copyName", label: commandCopy.copyName, icon: "i-copy" }],
  );
}

// The blank area (Explorer's own habit): *Paste* first, as Explorer has
// it (ruled 2026-10-26; lib/paste.ts) — the files copied in Explorer added
// here — then a folder made here, then the two pickers — the Add menu's
// three ways, the one that makes something here first.
//
// clip is what the clipboard was last read to hold: greyed with "Nothing
// to paste" when it names no files; enabled while the read is on its way
// (a press waits for the answer) or after it failed (a press reads again);
// and, for files cut in Explorer, enabled with the caption that the
// originals stay where they are — a paste never deletes them. A stop greys
// every item, *Paste* first, and its reason wins over the clipboard's: the
// page reads no clipboard then (a paste would add, and the add would wait).
export function fileBlankItems(stop?: Stop, clip: Clip = "pending"): CmdItem<FileBlankCmd>[] {
  const off = offFor(stop?.why);
  const pasteNow = stop ? off : clip === "empty" ? { disabled: true, title: archivePageCopy.nothingToPaste } : clip === "move" ? { note: archivePageCopy.pasteKeepsOriginals } : {};
  return groups<CmdItem<FileBlankCmd>>(
    [{ cmd: "paste", label: commandCopy.paste, icon: "i-paste", keys: keyNames.paste, ...pasteNow }],
    [{ cmd: "createFolder", label: commandCopy.createFolder, icon: "i-folder-add", keys: keyNames.createFolder, ...off }],
    [
      { cmd: "addFiles", label: commandCopy.addFiles, icon: "i-file", ...off },
      { cmd: "addFolder", label: commandCopy.addFolder, icon: "i-folder", ...off },
    ],
  );
}

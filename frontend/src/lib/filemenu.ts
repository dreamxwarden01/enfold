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
import { archivePageCopy, commandCopy, keyNames } from "./strings";

export type FileCmd = "open" | "extract" | "rename" | "moveTo" | "delete" | "copyName";
export type FileBlankCmd = "createFolder" | "addFiles" | "addFolder";

// closing: the archive drains after its *Close* (APP.md §2.3), when every
// page-facing call answers archive.not_open — every command is greyed with
// that, but *Copy name*, which asks nothing of the archive.
export function fileRowItems(isDir: boolean, closing: boolean): CmdItem<FileCmd>[] {
  const off = closing ? { disabled: true, title: archivePageCopy.menuClosing } : {};
  const open: CmdItem<FileCmd> = { cmd: "open", label: commandCopy.open, icon: "i-open", keys: keyNames.open, isDefault: true, ...off };
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

// The blank area (Explorer's own habit): a folder made here, then the two
// pickers — the Add menu's three ways, the one that makes something here
// first.
export function fileBlankItems(closing: boolean): CmdItem<FileBlankCmd>[] {
  const off = closing ? { disabled: true, title: archivePageCopy.menuClosing } : {};
  return groups<CmdItem<FileBlankCmd>>(
    [{ cmd: "createFolder", label: commandCopy.createFolder, icon: "i-folder-add", keys: keyNames.createFolder, ...off }],
    [
      { cmd: "addFiles", label: commandCopy.addFiles, icon: "i-file", ...off },
      { cmd: "addFolder", label: commandCopy.addFolder, icon: "i-folder", ...off },
    ],
  );
}

// The selected archive's commands (APP.md §13; directions 6 and 7): one
// table of them, read by the command bar, its More, the pane's More and
// the row's own menu, so a command is greyed by one rule and for one
// reason wherever it is offered. Pure: the page passes the row's facts
// and maps each cmd to the one function that runs it.
//
// The reasons are the menu's few words (archivesCopy.why); the bar's own
// buttons keep the codes' sentences as their titles.
import { groups } from "./menu";
import type { CmdItem } from "./menu";
import { archivePageCopy, archivesCopy, commandCopy, keyNames } from "./strings";

export type ArchCmd =
  | "open"
  | "close"
  | "forceClose"
  | "showInExplorer"
  | "refreshFingerprint"
  | "compact"
  | "rekey"
  | "hide"
  | "unhide"
  | "details"
  | "locate"
  | "forget"
  | "delete"
  | "restore";

export type ArchItem = CmdItem<ArchCmd>;

// What the commands are greyed by: the vault, then the row.
export interface ArchFacts {
  unlocked: boolean;
  tampered: boolean;
  // The archive is open in this process; held: a *Close* left it draining
  // and something still holds it, so the kill switch stands instead.
  open: boolean;
  held: boolean;
  // An operation of the archive's own runs (Force close's title says so).
  running: boolean;
  forgotten: boolean;
  compacting: boolean;
  hidden: boolean;
  // The core's archive.file_missing note: *Locate…* is offered.
  missing: boolean;
  // The last path is another system's (lib/paths.ts foreignPath).
  foreign: boolean;
  // The record has been read whole (Archives.Details), which Technical
  // details… shows.
  detailsRead: boolean;
}

export interface ArchGroups {
  open: ArchItem[];
  close: ArchItem[];
  reveal: ArchItem[];
  verbs: ArchItem[];
  hide: ArchItem[];
  details: ArchItem[];
  destroy: ArchItem[];
  restore: ArchItem[];
}

const off = (why: string | undefined): { disabled: boolean; title?: string } => (why === undefined ? { disabled: false } : { disabled: true, title: why });

// archCommands: every command for one row, in groups. With no row (null)
// every command is greyed with "Select an archive first.".
export function archCommands(f: ArchFacts | null): ArchGroups {
  const why = archivesCopy.why;
  const none = f === null ? archivesCopy.selectFirst : undefined;
  const lockWhy = none ?? (!f?.unlocked ? why.locked : f.tampered ? why.tampered : undefined);
  // What the row itself stops (lib/archlist.ts rowRefusal): forgotten
  // first — Restore is the way back, whatever else runs — then a
  // compaction under way.
  const rowWhy = f?.forgotten ? why.forgotten : f?.compacting ? why.compacting : undefined;
  const compactWhy = f?.compacting ? why.compacting : undefined;

  // Open: a locked vault opens only what is already open; the row's own
  // stop first, since it holds whatever the vault does.
  const openWhy = none ?? rowWhy ?? (!f?.unlocked && !f?.open ? why.locked : undefined);
  const open: ArchItem[] = [{ cmd: "open", label: commandCopy.open, icon: "i-open", keys: keyNames.open, isDefault: true, ...off(openWhy) }];

  // *Close* for an archive that is open — the graceful leave (APP.md §2.3,
  // one meaning for Close, ruled 2026-10-19) — or, once a Close has left
  // one that something still holds, the kill switch as *Force close*. A
  // compaction holds the handle against both.
  const close: ArchItem[] =
    !f?.open
      ? []
      : f.held
        ? [{ cmd: "forceClose", label: commandCopy.forceClose, icon: "i-x", ...(compactWhy ? off(compactWhy) : { disabled: false, title: f.running ? archivePageCopy.forceCloseRunning : archivePageCopy.forceCloseServing }) }]
        : [{ cmd: "close", label: commandCopy.close, icon: "i-x", ...off(compactWhy) }];

  const reveal: ArchItem[] = [{ cmd: "showInExplorer", label: commandCopy.showInExplorer, icon: "i-folder", ...off(none ?? (f?.foreign ? why.foreign : undefined)) }];

  // The three verbs that fold, in the bar's order (lib/cmdbar.ts).
  const verbWhy = lockWhy ?? rowWhy;
  const verbs: ArchItem[] = [
    { cmd: "refreshFingerprint", label: commandCopy.refreshFingerprint, icon: "i-check", ...off(verbWhy) },
    { cmd: "compact", label: commandCopy.compact, icon: "i-box", ...off(verbWhy) },
    { cmd: "rekey", label: commandCopy.rekeyArchive, icon: "i-rotate", ...off(verbWhy) },
  ];

  // Hide is a registry write stopped by a lock and by a forgotten record.
  const hideWhy = none ?? (!f?.unlocked ? why.locked : f.forgotten ? why.forgotten : undefined);
  const hide: ArchItem[] = [{ cmd: f?.hidden ? "unhide" : "hide", label: f?.hidden ? commandCopy.unhide : commandCopy.hide, icon: "i-eye", ...off(hideWhy) }];

  // Technical details… needs the record read whole, which a locked vault
  // does not give; Locate… only while the file is missing.
  const detailsWhy = none ?? (f?.detailsRead ? undefined : !f?.unlocked ? why.locked : "");
  const details: ArchItem[] = [
    { cmd: "details", label: archivesCopy.technicalDetails, icon: "i-info", keys: keyNames.technicalDetails, ...off(detailsWhy) },
    ...(f?.missing ? [{ cmd: "locate" as const, label: commandCopy.locate, icon: "i-file", ...off(hideWhy) }] : []),
  ];

  // The two that destroy, last and in the danger ink (direction 6). A
  // record already forgotten has nothing more to forget.
  const destroy: ArchItem[] = [
    { cmd: "forget", label: commandCopy.forgetArchive, icon: "i-x", danger: true, ...off(lockWhy ?? (f?.forgotten ? why.alreadyForgotten : compactWhy)) },
    { cmd: "delete", label: commandCopy.deleteArchive, icon: "i-trash", danger: true, keys: keyNames.delete, ...off(lockWhy ?? compactWhy) },
  ];

  const restore: ArchItem[] = [{ cmd: "restore", label: commandCopy.restore, icon: "i-undo", ...off(lockWhy) }];

  return { open, close, reveal, verbs, hide, details, destroy, restore };
}

// rowMenu: a row's own menu (direction 7). On the list: *Open* (bold,
// Enter) and *Close* while it is open, *Show in Explorer* · ─ · the three
// verbs and *Hide* · ─ · *Technical details…* and *Locate…* while missing ·
// ─ · *Forget archive…*, *Delete archive…*. In the Forgotten view a record
// can only come back or go: *Restore* · ─ · *Delete archive…*.
export function rowMenu(g: ArchGroups, forgottenView: boolean): ArchItem[] {
  if (forgottenView) return groups(g.restore, g.destroy.filter((i) => i.cmd === "delete"));
  return groups([...g.open, ...g.close, ...g.reveal], [...g.verbs, ...g.hide], g.details, g.destroy);
}

// The blank area of the list (Explorer's habit): what makes or brings an
// archive, the bar's two at its left.
export type ArchBlankCmd = "newArchive" | "openFile";

export function blankMenu(unlocked: boolean, tampered: boolean): CmdItem<ArchBlankCmd>[] {
  const why = archivesCopy.why;
  return [
    { cmd: "newArchive", label: commandCopy.newArchive, icon: "i-plus", keys: keyNames.newArchive, ...off(!unlocked ? why.locked : tampered ? why.tampered : undefined) },
    // Never greyed: a locked vault answers with the lock scene, as a
    // double-click in Explorer does (APP.md §14).
    { cmd: "openFile", label: archivesCopy.openFile, icon: "i-file", keys: keyNames.openFile, disabled: false },
  ];
}

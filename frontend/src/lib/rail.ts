// The rail (APP.md §6, Rail; directions 3, 6 and 10, ruled 2026-10-18):
// the vault head, the items, and the open archive as a place of its own —
// a child row under *Archives* that a visit to Vault or Settings keeps,
// since leaving an archive is an explicit act (§2.3: its ×, the head's
// *Close*, opening another, *Delete archive…*, the window closing). What
// the rail shows is decided here; Rail.svelte draws it.

import { VaultState } from "../../bindings/github.com/dreamxwarden01/enfold/internal/app";
import { busyLabel, busyOp } from "./ops";
import type { BusyOp } from "./ops";
import { commandCopy, lockCopy, railCopy } from "./strings";

// Below a 1008 px window the rail is the compact one, 48 px of icons
// (NavigationView's LeftCompact; direction 6). The same width is
// app.css's media query on .shell, which reads --rail-w-compact there.
export const RAIL_COMPACT_QUERY = "(width < 1008px)";

export interface RailArchive {
  id: string;
  name: string;
}

// railChildren is one row per open archive: those the status names
// (VaultStatus.Opened, by name as the core sorts them), and the one the
// page holds even before a status names it — its name the page's own
// heading until then. Several stand after a lock left more than one open.
export function railChildren(current: string | null, currentName: string, opened: readonly RailArchive[] | null | undefined): RailArchive[] {
  const rows = (opened ?? []).map((a) => ({ id: a.id, name: a.name }));
  if (current && !rows.some((a) => a.id === current)) rows.unshift({ id: current, name: currentName });
  return rows;
}

// railCurrent is the key of the item drawn as the page: *Archives* while
// the list shows, the archive's own row while its page does, *Vault* and
// *Settings* for theirs; nothing on the lock scene.
export function railCurrent(route: string, current: string | null): string {
  switch (route) {
    case "archives":
    case "keys":
    case "settings":
      return route;
    case "archive":
      return current ? `archive:${current}` : "archives";
  }
  return "";
}

// ---- an open archive's row while it is busy (ruled 2026-11-03, the user's)
//
// One archive does one thing at a time (APP.md §2.3), so while an operation
// runs on an archive its row in the rail says so: the 20 px × that closes it
// gives its place to a ring — the 14 px ring of the Archives item's mark,
// 2 px of the accent over the stroke, filled from the top to the
// operation's percentage while its total is known, and a quarter of it
// turning while it is not — and the row's tooltip and name say the
// operation. The row still opens the archive; the × comes back when the
// operation ends. Busy is the Archive page's own rule (lib/ops.ts busyOp):
// any kind, a reclaim included, but a drag out still in its hover, and an
// add the page started that has said nothing yet.

// The fields the row reads of an operation (OpView's): busyOp's, and the
// bytes its ring fills by.
export interface RowOp extends BusyOp {
  done: number;
  total: number;
}

export interface RowBusy {
  // The operation, as a busy command's reason names it without its tail:
  // "Adding 20 files", "Adding files" before the count is planned.
  what: string;
  // The ring's fill, 0 to 100, or null while the total is not known: the
  // turning quarter.
  ring: number | null;
}

// rowBusy is what an open archive's row says while an operation runs on
// it, or null while it is idle: the latest begun on it, as the Archive
// page names it.
export function rowBusy(ops: readonly RowOp[], starting: readonly string[], archiveId: string): RowBusy | null {
  const b = busyOp(ops, starting, archiveId);
  if (!b) return null;
  const o = ops.find((x) => x.id === b.id);
  const ring = o && o.total > 0 ? Math.round((Math.max(0, Math.min(o.done, o.total)) / o.total) * 100) : null;
  return { what: busyLabel(b), ring };
}

export interface VaultHead {
  name: string;
  // The state in the lock header's words (lockCopy.header).
  word: string;
  unlocked: boolean;
  locked: boolean;
}

export interface VaultFacts {
  state: string;
  displayName?: string;
  setupNeeded?: boolean;
}

// vaultHead is the rail's head (direction 10): the vault's name over its
// state, in the words the lock screen's header uses for the same state.
// name, when given, is the name Rename… gave last (store.vaultName), which
// wins over the status's for the moment before the status follows it.
export function vaultHead(st: VaultFacts | null | undefined, name?: string): VaultHead {
  const state = st?.state ?? "";
  const h = lockCopy.header;
  let word = "";
  switch (state) {
    case VaultState.StateUnlocked:
      word = h.unlocked;
      break;
    case VaultState.StateLocked:
      word = st?.setupNeeded ? h.setupNeeded : h.locked;
      break;
    case VaultState.StateUnlocking:
    case VaultState.StateReleasing:
      word = h.unlocking;
      break;
    case VaultState.StateNone:
      word = h.none;
      break;
    case VaultState.StateBusy:
      word = h.busy;
      break;
    case VaultState.StateBroken:
      word = h.broken;
      break;
  }
  const none = state === VaultState.StateNone;
  return {
    name: (!none && (name || st?.displayName)) || railCopy.noVault,
    word,
    unlocked: state === VaultState.StateUnlocked,
    locked: state === VaultState.StateLocked,
  };
}

// vaultButtonLabel is the compact rail's padlock, its tooltip and its
// accessible name alike: "Personal vault · Unlocked · Lock now". The act is
// the foot's own — *Lock now* while unlocked, *Unlock* while locked — and
// any other state names none: the press shows the lock scene, which says
// what comes first.
export function vaultButtonLabel(head: VaultHead): string {
  const act = head.unlocked ? commandCopy.lockNow : head.locked ? commandCopy.unlock : "";
  return railCopy.vaultButton(head.name, head.word, act);
}

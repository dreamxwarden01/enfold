// One meaning for *Close*, app-wide (APP.md §2.3, ruled 2026-10-19): a
// *Close* is the graceful leave, Archives.Leave, wherever it stands, and
// *Force close* — the kill switch, Archives.Close or CloseAll — is offered
// only for an archive a leave left that something still holds (the
// store's draining or lingering: VaultStatus.Opened still names it). The
// three closes that once ran the kill switch under a plain name — the
// Archives page's locked line, the lock screen's archives-open notice and
// the Archive page's archive.needs_reopen bar — follow it here: the same
// button leaves, and reads *Force close* once the archives it names still
// stand after their leave.
import { commandCopy, lockCopy } from "./strings";

// Where the button stands; each keeps its own words for the leave.
export type CloseSpot = "line" | "notice" | "bar";

export interface CloseButton {
  label: string;
  // force: the kill switch; otherwise the graceful leave of each archive
  // the button names that is not already held after one.
  force: boolean;
}

// closeButton is the button's words and act for the open archives it
// names (the status's ids) — force only when every one of them has been
// left and is still held, since a leave of an archive already left does
// nothing, and a kill switch over one never left would skip the graceful
// way. With none named, the leave's own words (nothing to force).
export function closeButton(spot: CloseSpot, ids: readonly string[], held: (id: string) => boolean): CloseButton {
  const force = ids.length > 0 && ids.every(held);
  const one = ids.length <= 1;
  switch (spot) {
    case "line":
      return { force, label: force ? (one ? commandCopy.forceClose : commandCopy.forceCloseAll) : one ? commandCopy.closeArchive : commandCopy.closeAll };
    case "notice":
      return { force, label: force ? (one ? commandCopy.forceClose : lockCopy.forceCloseThem) : one ? lockCopy.closeIt : lockCopy.closeThem };
    case "bar":
      return { force, label: force ? commandCopy.forceClose : commandCopy.close };
  }
}

// toLeave: the archives a leave still has to reach — those not already
// left and held — in the status's order.
export function toLeave(ids: readonly string[], held: (id: string) => boolean): string[] {
  return ids.filter((id) => !held(id));
}

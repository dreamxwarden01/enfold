// InfoBars (direction 4 of the design review, phase 4b): what toasts were.
// A bar says what the page cannot say in place — an error a call came back
// with, a copy that worked, an operation that ended on another page — and
// sits in the page's notice slot over the top of the list (NoticeSlot,
// InfoBars.svelte; APP.md §6, Notices). It never moves the list.
//
// The rules here are the timing and the queue, kept apart from the store
// so they can be tested without a clock:
//
// - lifetime: info and success go by themselves after 3 s plus 50 ms per
//   character, at most 8 s; an error stays until it is dismissed.
// - the queue: at most three are shown, newest first; the rest wait their
//   turn, and a waiting bar's time does not run.
// - the pause: a shown bar's time runs only while the pointer is off the
//   bars and the window has the focus, and a page's bar not at all while a
//   dialog or the viewer covers the page — so a bar is never gone before it
//   could be read.
// - where: a bar raised while a dialog is up is that dialog's, shown in the
//   dialog over its foot band (the page's slot is under the dialog's dim);
//   once the last dialog closes it is the page's like any other.
import { tierIcon } from "./notices";

export type NoticeKind = "info" | "success" | "error";

export interface Notice {
  id: number;
  text: string;
  kind: NoticeKind;
  // inDialog: raised while a dialog was up; shown in the dialog until the
  // last one closes.
  inDialog: boolean;
  // left: the milliseconds of its life still to run; Infinity for an error.
  left: number;
}

export const BASE_MS = 3000;
export const PER_CHAR_MS = 50;
export const CAP_MS = 8000;
export const MAX_SHOWN = 3;

// lifetime is how long a bar stands while it is shown and nothing pauses
// it: an error until it is dismissed.
export function lifetime(text: string, kind: NoticeKind): number {
  if (kind === "error") return Infinity;
  return Math.min(BASE_MS + PER_CHAR_MS * [...text].length, CAP_MS);
}

// noticeIcon is the bar's icon, from the tiers' set (lib/notices.ts): the
// error circle for an error, the info circle for info, and for a success
// the check the accent draws.
export function noticeIcon(kind: NoticeKind): string {
  if (kind === "error") return tierIcon("danger");
  if (kind === "success") return "i-check";
  return tierIcon("info");
}

// raise adds a bar as the newest. The same words of the same kind already
// standing are not said twice: that bar goes, and the new one, with its
// whole life, takes the top.
export function raise(list: readonly Notice[], n: Notice): Notice[] {
  return [...list.filter((x) => !(x.text === n.text && x.kind === n.kind)), n];
}

export type Host = "page" | "dialog";

// shown is what a host draws, newest first and at most three: the page's
// slot draws every bar while no dialog is up, and only the page's own
// while one is; a dialog draws the bars raised under a dialog.
export function shown(list: readonly Notice[], host: Host, dialogUp: boolean): Notice[] {
  const mine = list.filter((n) => (host === "dialog" ? dialogUp && n.inDialog : !dialogUp || !n.inDialog));
  return mine.slice(-MAX_SHOWN).reverse();
}

export interface Pause {
  // hovered: the pointer is over the bars, in any host.
  hovered: boolean;
  // away: the window does not have the focus, or is hidden.
  away: boolean;
  // dialogUp: a dialog is up, which covers the page's bars.
  dialogUp: boolean;
  // viewerUp: the viewer stands over the page's area (Viewer.svelte; its
  // z 10 over the notice slot's 5), which covers the page's bars too.
  viewerUp: boolean;
}

// running is the ids whose time runs now: shown, mortal, and not paused —
// a page's bar under a dialog's dim or under the viewer is not shown to
// anyone. A dialog's bars run over the viewer as anywhere: a dialog the
// viewer opens (Extract…) stands over it.
export function running(list: readonly Notice[], p: Pause): Set<number> {
  if (p.hovered || p.away) return new Set();
  if (p.viewerUp && !p.dialogUp) return new Set();
  const on = p.dialogUp ? shown(list, "dialog", true) : shown(list, "page", false);
  return new Set(on.filter((n) => Number.isFinite(n.left)).map((n) => n.id));
}

// spend takes `ms` off the life of each running bar and drops the bars
// whose life has run out.
export function spend(list: readonly Notice[], ms: number, run: ReadonlySet<number>): Notice[] {
  if (ms <= 0 || run.size === 0) return [...list];
  return list.map((n) => (run.has(n.id) ? { ...n, left: n.left - ms } : n)).filter((n) => n.left > 0);
}

// due is how long until the next running bar runs out, or null when none
// is running.
export function due(list: readonly Notice[], run: ReadonlySet<number>): number | null {
  let min = Infinity;
  for (const n of list) if (run.has(n.id) && n.left < min) min = n.left;
  return Number.isFinite(min) ? min : null;
}

// newestError is the error Escape closes: the newest error the page's slot
// shows (APP.md §6, Notices).
export function newestError(list: readonly Notice[]): Notice | null {
  return shown(list, "page", false).find((n) => n.kind === "error") ?? null;
}

// escCloses is the bar one Escape closes, in the one order of Escape
// (lib/dialogs.ts ESC_OWNERS; APP.md §6, InfoBars): nothing while a
// dialog, a menu, a popover or the viewer is up — that Escape is theirs,
// and the error waits for the next — nor while the focus is in a text
// field, whose Escape is its own; else the newest error the page shows.
// null lets the key fall through to the list.
export interface EscState {
  dialogUp: boolean;
  // A menu, an open popover or the viewer stands (ESC_OWNERS matched).
  ownerUp: boolean;
  inField: boolean;
}

export function escCloses(list: readonly Notice[], s: EscState): Notice | null {
  if (s.dialogUp || s.ownerUp || s.inField) return null;
  return newestError(list);
}

// settled is the list once the last dialog has closed: every bar is the
// page's from then on.
export function settled(list: readonly Notice[]): Notice[] {
  return list.some((n) => n.inDialog) ? list.map((n) => (n.inDialog ? { ...n, inDialog: false } : n)) : [...list];
}

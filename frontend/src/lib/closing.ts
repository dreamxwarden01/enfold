// The close question and the setting it answers (APP.md §2.4, ruled
// 2026-09-13: "we must not default to going to the tray"). The first close
// — the button, Alt+F4, the taskbar's close — is cancelled by the shell,
// which emits `shell.close`; the page asks, and the answer goes back as
// Shell.CloseDecided(action, remember). The setting itself is
// Settings.closeAction, shown in Settings so the remembering can be undone
// there. And the quit question (ruled 2026-11-03, the user's): a quit asked
// for while an operation runs — the window's close under the Quit setting,
// the tray's Quit — is asked by the page too, `shell.quit` carrying the
// operations, and answered as Shell.QuitDecided(proceed).
import { busyLabel } from "./ops";
import type { BusyOp } from "./ops";
import { closeCopy } from "./strings";

// CloseAction is what the settings file holds: ask — the close asks — tray,
// or quit. Nothing else is a value; a file that holds something else is
// read as the default by the core, so the page only ever sees these three.
export type CloseAction = "ask" | "tray" | "quit";

// Decision is what the question can be answered with. Never "ask": that is
// the question, not an answer to it.
export type Decision = Exclude<CloseAction, "ask">;

// The Settings row's own mapping: the three choices in the order they are
// offered, and the words the save bar names a staged one with. The old
// destroy/hide labels went with the setting they belonged to — the window
// is destroyed either way since 2026-09-13.
export const closeChoices: [CloseAction, string][] = [
  ["ask", "Ask each time"],
  ["tray", "Keep running in the tray"],
  ["quit", "Quit"],
];

// closeActionLabel is one choice's words, for the select and for the bar.
// A value that is none of the three reads as the default, exactly as the
// core reads a settings file it does not recognise.
export function closeActionLabel(v: unknown): string {
  return closeChoices.find((c) => c[0] === v)?.[1] ?? closeChoices[0][1];
}

// asksOnClose reports whether a close would raise the question at all.
export function asksOnClose(v: unknown): boolean {
  return v !== "tray" && v !== "quit";
}

// ---- what the two questions name (ruled 2026-11-03, the user's) -----------
//
// While an operation runs, the close question and the quit question say so
// in a sentence: the operation, and what each answer does — "Adding 20 files
// to Photos 2024 is still running. Quit stops it; in the tray it goes on."
// A question, once up, stays until it is answered: an operation that ends
// meanwhile does not take it down, nothing quits by itself, and the
// sentence then says the operation has finished. So the page follows every
// operation a standing question has named, from the moment it is named to
// its end.

// AskedOp is one operation a standing question has named: the operation as
// last seen, whether the page has seen it at all (the shell can name one
// whose first word has not reached a page made again for the question), and
// whether it has ended.
export interface AskedOp {
  op: BusyOp;
  seen: boolean;
  ended: boolean;
}

// opsNow is what the page knows of the operations at this moment: every one
// it holds, finished ones included while it keeps them, then the ones its
// Archive page started that have said nothing yet (the store's starting
// ids, by archive), as count-less adds — lib/ops.ts busyOps' rule, over
// every archive.
export function opsNow(ops: Readonly<Record<string, BusyOp>>, starting: Readonly<Record<string, readonly string[]>>): BusyOp[] {
  const known = Object.values(ops);
  const ids = new Set(known.map((o) => o.id));
  const quiet: BusyOp[] = [];
  for (const [archiveId, list] of Object.entries(starting)) {
    for (const id of list) if (!ids.has(id)) quiet.push({ id, kind: "add", archiveId, items: 0, startedAt: 0 });
  }
  return [...known, ...quiet];
}

// follow brings a standing question's list up to date. now is opsNow; named
// is what the shell's `shell.quit` named, which may hold an operation the
// page has not heard of yet (it stays unseen, and running, until the page
// hears of it) or one already finished (the shell saw it end before the
// window could). An operation the page knows is followed to its end; one it
// has seen and no longer holds — kept for a while after its end, then
// forgotten — has ended. Every operation that begins while the question
// stands joins it, since quitting would stop that one too. The list it was
// given comes back unchanged when nothing moved, so a caller can tell.
export function follow(asked: readonly AskedOp[], now: readonly BusyOp[], named: readonly BusyOp[] = []): AskedOp[] {
  const byId = new Map(now.map((o) => [o.id, o]));
  let changed = false;
  const out: AskedOp[] = asked.map((a) => {
    const o = byId.get(a.op.id);
    if (o) {
      const ended = a.ended || !!o.finished;
      if (o === a.op && a.seen && ended === a.ended) return a;
      changed = true;
      return { op: o, seen: true, ended };
    }
    if (a.seen && !a.ended) {
      changed = true;
      return { ...a, ended: true };
    }
    return a;
  });
  const listed = new Set(out.map((a) => a.op.id));
  for (const o of named) {
    if (listed.has(o.id)) continue;
    listed.add(o.id);
    changed = true;
    const k = byId.get(o.id);
    out.push(k ? { op: k, seen: true, ended: !!k.finished } : { op: o, seen: false, ended: !!o.finished });
  }
  for (const o of now) {
    if (o.finished || listed.has(o.id)) continue;
    listed.add(o.id);
    changed = true;
    out.push({ op: o, seen: true, ended: false });
  }
  return changed ? out : (asked as AskedOp[]);
}

// latest: the one begun last, the last listed among those begun in the
// same second (lib/foot.ts latestOp's rule).
function latest(asked: readonly AskedOp[]): AskedOp {
  let found = asked[0];
  for (const a of asked) if (a.op.startedAt >= found.op.startedAt) found = a;
  return found;
}

// askedLine is the questions' sentence, or "" when nothing was named: the
// latest still running and how many others run, then what the answers do;
// once every one named has ended, that they have finished. tray: the
// close question's, which offers the tray. names maps an archive's id to
// its name (OpView carries the id alone).
export function askedLine(asked: readonly AskedOp[], names: ReadonlyMap<string, string>, tray: boolean): string {
  if (asked.length === 0) return "";
  const live = asked.filter((a) => !a.ended);
  const pick = live.length > 0 ? live : asked;
  const top = latest(pick);
  const label = busyLabel(top.op, names.get(top.op.archiveId ?? "") ?? "");
  return live.length > 0 ? closeCopy.stillRunning(label, live.length - 1, tray) : closeCopy.finished(label, asked.length - 1);
}

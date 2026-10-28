// The recovery key's dialog, the part that decides (APP.md §6, ruled
// 2026-09-09: "None asks a second time"; direction 9, ruled 2026-10-18).
//
// The key is left by one of three ways — printed, saved to a text file the
// core writes, or written down — and each of them counts at once. There
// is no second confirmation for any of them: a user set on leaving is not
// stopped by one, the key can be shown again from the Vault page, and at
// the moment it is first shown the vault holds nothing yet.
//
// The readback (*Check what I wrote*) is optional and never a gate: two
// groups drawn at random, each ticked when its six digits match, and
// *I have written it down* stays pressable whatever it says — so nothing
// is asked twice.
//
// Keep mode is the same dialog for a new key Replace recovery keys… shows
// before it is the vault's (APP.md §13, ruled 2026-10-27, R-B): the ways
// count at once there too, with no second confirmation, since the act's
// own confirmation before its commit pairs every new key with the one it
// replaces. Its band goes on to the next key rather than closing, and
// Cancel is its one way out, which cancels the replacement.
import { dialogCopy, footCopy, revealCopy } from "./strings";

export interface Kept {
  // where the core wrote the text file, empty until it has
  savedTo: string;
  // *Print…* was pressed — nothing here reads a printer
  printed: boolean;
  wroteDown: boolean;
}

export function keptSomehow(k: Kept): boolean {
  return k.savedTo.trim() !== "" || k.printed || k.wroteDown;
}

export type Act = "print" | "save" | "wrote" | "done";

export interface BandButton {
  act: Act;
  label: string;
  accent: boolean;
}

// ways is the row under the digits (ruled 2026-10-18, the user's, after
// seeing the build: the band had held all three ways, equal and wide).
// The two ways that leave the machine stand with the digits they act on,
// at their own width: *Print…* the suggested one — the accent, with the
// document icon — until any way is taken, then both standard, worded
// again for whichever of them has already been done.
export function ways(k: Kept): BandButton[] {
  return [
    { act: "print", label: k.printed ? revealCopy.printAgain : revealCopy.print, accent: !keptSomehow(k) },
    { act: "save", label: k.savedTo.trim() ? revealCopy.saveAgain : revealCopy.save, accent: false },
  ];
}

// band is the dialog's foot: one button. *I have written it down*,
// standard, until any way is taken — a print, a save the core
// acknowledged, or its own press — and *Done*, the accent, from then on,
// which closes with one click. One accent at a time: the row's until a way
// is taken, the band's after.
export function band(k: Kept): BandButton {
  return keptSomehow(k) ? { act: "done", label: revealCopy.done, accent: true } : { act: "wrote", label: revealCopy.wroteDown, accent: false };
}

// ---- Keep mode ------------------------------------------------------------

export type KeepAct = Act | "next" | "stay" | "cancel";

export interface KeepButton {
  act: KeepAct;
  label: string;
  accent: boolean;
  // Stay unlocked is a subtle button, as the status bar's is.
  subtle?: boolean;
}

// keepBand is keep mode's foot, in the band's order: the act, any other way
// on, the way out. Before any way is taken, I have written it down
// (standard) and Cancel; once one is, Next recovery key (the accent), or
// Continue for the last, and Cancel. Each way counts at once, I have
// written it down included. A key that could not be shown (its one-time
// link failed, or the handle is no longer held) leaves Cancel alone: any
// other button would answer that a key the user never saw was kept, and a
// key never kept must never become the vault's. Stay unlocked stands in the
// idle deadline's last minute (lib/foot.ts staysUnlocked), since the dialog
// stands over the status bar that offers it, and keeping a key by hand is
// minutes without input.
export function keepBand(k: Kept, o: { last: boolean; gone: boolean; stay: boolean }): KeepButton[] {
  const cancel: KeepButton = { act: "cancel", label: dialogCopy.cancel, accent: false };
  if (o.gone) return [cancel];
  const act: KeepButton = keptSomehow(k)
    ? { act: "next", label: o.last ? revealCopy.continue : revealCopy.next, accent: true }
    : { act: "wrote", label: revealCopy.wroteDown, accent: false };
  return o.stay ? [act, { act: "stay", label: footCopy.stayUnlocked, accent: false, subtle: true }, cancel] : [act, cancel];
}

// keepTitle is keep mode's title: which of the new keys this is, and one
// key in the singular.
export function keepTitle(index: number, count: number): string {
  return revealCopy.titleKeep(index, count);
}

// digitsLine is the caption under the digits: a key shown before the
// replacement's confirmation says nothing changes until then, in the
// singular when the replacement makes one key (keepCount, which keep mode
// must pass); a recovery key added with Use recovery key instead says when
// it can be shown again, there being no YubiKey or password at hand to
// prove with; any other promises the Vault page's showing.
export function digitsLine(n: number, mode?: "later" | ""): string;
export function digitsLine(n: number, mode: "keep", keepCount: number): string;
export function digitsLine(n: number, mode: "keep" | "later" | "" = "", keepCount = 0): string {
  return mode === "keep" ? revealCopy.keepDigitsLine(n, keepCount) : mode === "later" ? revealCopy.digitsLineLater(n) : revealCopy.digitsLine(n);
}

// The key is eight groups of six digits (FORMAT.md §18.4).
export const GROUPS = 8;
export const GROUP_LEN = 6;

// pickGroups draws the readback's two groups, distinct, once per dialog:
// zero-based indices, in the order they stand on the sheet. random is
// Math.random's shape, injected so a test can name the draw.
export function pickGroups(random: () => number = Math.random, count = GROUPS): [number, number] {
  const draw = (n: number) => Math.min(n - 1, Math.floor(random() * n));
  const a = draw(count);
  // The second is drawn from the others, so the two are never the same.
  let b = draw(count - 1);
  if (b >= a) b++;
  return a < b ? [a, b] : [b, a];
}

// digitsOnly keeps what a readback field may hold: digits, at most a
// group's six. A paste of "123 456" is "123456".
export function digitsOnly(s: string, max = GROUP_LEN): string {
  return s.replace(/\D/g, "").slice(0, max);
}

export type Mark = "" | "tick" | "bad";

// readbackMark judges one field against its group: nothing until six
// digits are typed, then the tick or the danger underline. The digits
// are compared here, in the page's memory, and go nowhere else.
export function readbackMark(typed: string, group: string): Mark {
  if (typed.length < GROUP_LEN) return "";
  return typed === group ? "tick" : "bad";
}

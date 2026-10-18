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
import { revealCopy } from "./strings";

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

// band is the dialog's foot, in the order its buttons stand. Before any of
// the three, *Print…* is the suggested way (the accent, first) — a sheet
// on paper, away from this PC — then *Save as a text file…*, then *I have
// written it down*, standard. From the first of them on, *Done* is the
// accent and first, and one click closes; printing and saving stay
// offered, worded again for whichever of them has already been done.
export function band(k: Kept): BandButton[] {
  const save = { act: "save" as const, label: k.savedTo.trim() ? revealCopy.saveAgain : revealCopy.save, accent: false };
  if (!keptSomehow(k)) {
    return [{ act: "print", label: revealCopy.print, accent: true }, save, { act: "wrote", label: revealCopy.wroteDown, accent: false }];
  }
  return [{ act: "done", label: revealCopy.done, accent: true }, { act: "print", label: k.printed ? revealCopy.printAgain : revealCopy.print, accent: false }, save];
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

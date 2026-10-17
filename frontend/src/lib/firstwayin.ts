// The first unlock method of a new vault, as the create, restore and
// finish-setup dialogs ask it (APP.md §6, ruled 2026-10-17): a radio
// group of cards — YubiKey, Password — over the recovery key's fixed
// line. Until one is chosen each card shows its line alone; the chosen
// card opens on its fields. The password, the standalone one or the one
// every YubiKey must carry, is typed here in two fields, judged here, and
// sent by the page at the ceremony's first Choose prompt (the store's
// holdChosen) — never as an argument of the bound call (APP.md §1).
//
// Pure: which card is open, which fields it shows, what Continue refuses,
// what the two lines under the cards say and what the dialog starts the
// ceremony with. FirstWayIn renders from it.
import { firstMethodCopy } from "./strings";
import { confirmSecretProblem, secretProblem } from "./validate";

// FirstMethod is the card chosen; "" until one is.
export type FirstMethod = "" | "token" | "password";

// Context: a create makes a recovery key and shows it once; an adopted
// backup (a restore, a finish setting up) opens with its own and keeps it.
export type FirstContext = "create" | "adopt";

export interface FirstForm {
  method: FirstMethod;
  // entangle: the tick for the password with every YubiKey — a YubiKey's
  // option alone; the Password card ignores it.
  entangle: boolean;
  password: string;
  again: string;
}

// asksPassword says whether the vault's password is chosen in this
// dialog: the Password card, or the YubiKey card with the tick.
export function asksPassword(method: FirstMethod, entangle: boolean): boolean {
  return method === "password" || (method === "token" && entangle);
}

// Shown is what the dialog carries beyond the cards' own lines.
//   name     — the YubiKey's optional name and its tick
//   password — the two password fields, under the tick for a YubiKey
export interface Shown {
  name: boolean;
  password: boolean;
}

export function shown(method: FirstMethod, entangle: boolean): Shown {
  return { name: method === "token", password: asksPassword(method, entangle) };
}

// keepsPassword says whether what was typed in the two fields survives a
// change of the choice. It does only while the same card keeps asking for
// it: a card that closes, or a tick taken off, clears the fields, so the
// secret is never kept in a field no one sees, nor carried into the other
// card.
export function keepsPassword(from: { method: FirstMethod; entangle: boolean }, to: { method: FirstMethod; entangle: boolean }): boolean {
  return from.method === to.method && asksPassword(to.method, to.entangle);
}

// Problems is what each part of the form refuses now, "" where nothing is
// wrong: the choice itself, and the two fields while they are shown — the
// minimum of 8 and the two copies the same (lib/validate). The form rule
// (lib/forms.ts) decides when each is said.
export interface Problems {
  method: string;
  password: string;
  again: string;
}

export function problems(f: FirstForm): Problems {
  const asks = asksPassword(f.method, f.entangle);
  return {
    method: f.method ? "" : firstMethodCopy.chooseOne,
    password: asks ? secretProblem("password", f.password, true) : "",
    again: asks ? confirmSecretProblem(f.password, f.again) : "",
  };
}

export function refuses(f: FirstForm): boolean {
  const p = problems(f);
  return !!(p.method || p.password || p.again);
}

// fixedLine is the recovery key's line under the two cards: not a choice,
// so not a card (ruled 2026-10-17; it was a third card, and with a card
// open the dialog outgrew an 820 px window). A create makes one and shows
// it after; an adopted backup keeps its own, which opens it first.
export function fixedLine(context: FirstContext = "create"): string {
  return context === "adopt" ? firstMethodCopy.recoveryKept : firstMethodCopy.recoveryMade;
}

// nextLine is the line under the cards: what comes after Continue, by the
// choice, in a caption. Empty until a card is chosen — the line keeps one
// caption line's height, so nothing below it moves when it fills.
export function nextLine(method: FirstMethod, context: FirstContext = "create"): string {
  if (!method) return "";
  const lines = context === "adopt" ? firstMethodCopy.nextAdopt : firstMethodCopy.next;
  return lines[method];
}

// Begin is what the dialog starts the ceremony with: the kind, the label
// and the tick go to the bound call; the password is held by the store for
// the prompt and never goes with them.
export interface Begin {
  kind: "token" | "password";
  label: string;
  entangle: boolean;
  password: string;
}

// begin answers null while the form refuses. A password unlock method is
// named "Password", whatever was typed for a YubiKey before the card was
// changed; a YubiKey's empty name goes as empty and the core names it
// after its serial number ("YubiKey 12345678"), which is what the field's
// placeholder promises. password is "" when none is chosen here.
export function begin(f: FirstForm, label: string): Begin | null {
  if (refuses(f) || !f.method) return null;
  const token = f.method === "token";
  return {
    kind: f.method,
    label: token ? label : "Password",
    entangle: token && f.entangle,
    password: asksPassword(f.method, f.entangle) ? f.password : "",
  };
}

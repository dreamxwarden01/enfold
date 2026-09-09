// The token way in's card 2, as a state machine (APP.md §6, the ruling of
// 2026-09-08): on a vault whose entangled password is on, the key's PIN and
// the vault's password stand on one card under one *Continue*. The core
// asks the PIN first and verifies it at the card (APP.md §2.2), so a wrong
// PIN marks that field alone and moves nothing else; the password field
// keeps whatever was typed and goes out by itself, with no further click,
// the moment the prompt an accepted PIN brings arrives.
//
// Pure: given the form as it stands and the ceremony's next event, what
// each field shows and which prompt the next Continue answers. The lock
// screen's card 2 and the Keys page's ceremony panel both render from it.
import { CeremonyStep } from "./api";

// Shows is what the card carries.
//   none     — this step asks for no PIN and no password here
//   pin      — the key's PIN alone: the switch is off, or the password has
//              already gone out in this ceremony (the ECDH fallback)
//   both     — the merged card: the PIN above, the vault's password below
//   password — the vault's password alone: the retry after a wrong one
//              (the core asks no PIN again while it holds the cached H),
//              and every way in that is not the key's
export type Shows = "none" | "pin" | "both" | "password";

export interface PinForm {
  shows: Shows;
  // What the next Continue answers, and under which prompt id. On the
  // merged card that is the PIN's prompt: the password waits for its own.
  answers: "" | "pin" | "password";
  promptId: string;
  // The core's refusal of the last answer, as its code — only the field it
  // belongs to is marked.
  pinError: string;
  passwordError: string;
  // Whether each field starts empty in this state. A PIN is always asked
  // again with a clean field; the password field is emptied only when the
  // core refused it, and never by a refused PIN.
  clearPin: boolean;
  clearPassword: boolean;
  // send: the page answers promptId at once from the field as it stands,
  // in whatever state the user left it, with no further click.
  send: "" | "password";
  // held: the merged card carries a password beside the PIN, waiting for
  // the prompt an accepted PIN brings. Memory, never rendered.
  held: boolean;
  // given: the vault's password has gone out in this ceremony, or the
  // ceremony is past the touch and it was never wanted, so a PIN asked
  // now is the fallback inside ECDH and stands alone. Memory.
  given: boolean;
}

export const NO_FORM: PinForm = {
  shows: "none",
  answers: "",
  promptId: "",
  pinError: "",
  passwordError: "",
  clearPin: false,
  clearPassword: false,
  send: "",
  held: false,
  given: false,
};

// FormEvent is the ceremony event as this machine reads it. merged: the
// way in is the key and this vault's entangled password is on, so one card
// carries both fields — the caller knows the vault's switch and the
// ceremony's method; a chosen password (create, entangle) is never merged.
export interface FormEvent {
  step: CeremonyStep;
  promptId: string;
  error: string;
  merged: boolean;
}

// nextForm is the form after ev, given the form before it.
export function nextForm(prev: PinForm, ev: FormEvent): PinForm {
  // A step that asks for nothing here shows nothing and remembers
  // everything: the touch stands between the password going out and the
  // core's word on it.
  const quiet: PinForm = { ...NO_FORM, held: prev.held, given: prev.given };
  switch (ev.step) {
    case CeremonyStep.StepPIN: {
      if (!ev.promptId) return quiet;
      const both = ev.merged && !prev.given;
      return {
        shows: both ? "both" : "pin",
        answers: "pin",
        promptId: ev.promptId,
        pinError: ev.error === "token.pin" ? ev.error : "",
        passwordError: "",
        clearPin: true,
        clearPassword: false,
        send: "",
        held: both,
        given: prev.given,
      };
    }
    case CeremonyStep.StepPassword: {
      if (!ev.promptId) return quiet;
      // held: the PIN was accepted and this prompt is the one the merged
      // card was waiting for — the field goes as it stands.
      const take = prev.held;
      return {
        shows: "password",
        answers: "password",
        promptId: ev.promptId,
        pinError: "",
        passwordError: !take && ev.error === "vault.auth" ? ev.error : "",
        clearPin: true,
        clearPassword: !take,
        send: take ? "password" : "",
        held: false,
        given: take || prev.given,
      };
    }
    // The touch. The core asks the vault's password before the agreement
    // and never after it (APP.md §2.2), so by now the password has either
    // gone out or is not wanted, and the card is done with it: whatever
    // this ceremony is asked for next stands alone. This is how a
    // ceremony that adopted a pending touch learns it — it opens at the
    // touch with nothing before it, the password already in the attempt
    // it took over, so no StepPassword ever reached the page.
    case CeremonyStep.StepTouch:
      return { ...quiet, given: true };
    // The ceremony is over, or a new one opens: nothing is held.
    case CeremonyStep.StepWaitingForKey:
    case CeremonyStep.StepDone:
    case CeremonyStep.StepFailed:
    case CeremonyStep.StepBlocked:
      return NO_FORM;
    default:
      return quiet;
  }
}

// key is what the card's body is keyed on so that its text fades when what
// it shows changes (APP.md §6). The merged card and the password alone
// share one key: the password typed beside the PIN must survive the prompt
// that follows it, and a re-key would take the field with it.
export function key(f: PinForm): string {
  return f.shows === "both" ? "password" : f.shows;
}

// CeremonyFacts is the little of CeremonyState this machine reads.
export interface CeremonyFacts {
  seq: number;
  step: CeremonyStep;
  promptId?: string;
  error?: string;
}

// Tracked is the form with the event it was last advanced by: the machine
// steps once per ceremony event and never on a re-render.
export interface Tracked {
  seq: number;
  form: PinForm;
}

export const NO_TRACK: Tracked = { seq: -1, form: NO_FORM };

// track advances the form on a new ceremony event and answers the same
// object when there is none, so a component can assign it unconditionally.
export function track(prev: Tracked, c: CeremonyFacts | null | undefined, merged: boolean): Tracked {
  if (!c) return prev.seq === -1 ? prev : NO_TRACK;
  if (c.seq === prev.seq) return prev;
  return {
    seq: c.seq,
    form: nextForm(prev.form, { step: c.step, promptId: c.promptId ?? "", error: c.error ?? "", merged }),
  };
}

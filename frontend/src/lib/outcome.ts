// What a ceremony's event means for the lock screen's outcome bar and its
// wording (APP.md §6): pure, so it can be tested apart from the store.
import type { CeremonyState } from "./api";
import { CeremonyStep } from "./api";

export interface Outcome {
  kind: string;
  archives: number;
}

// opening reports the first event of a ceremony: everything about the
// previous one is forgotten on it.
export function opening(c: CeremonyState): boolean {
  return c.step === CeremonyStep.StepWaitingForKey && !c.promptId;
}

// outcomeAfter is the outcome to show after event c, given the one shown
// before it.
export function outcomeAfter(prev: Outcome | null, c: CeremonyState): Outcome | null {
  if (opening(c)) return null;
  const final = c.step === CeremonyStep.StepDone || (c.step === CeremonyStep.StepRecovery && !c.promptId);
  if (final && (c.kind === "import" || c.kind === "verify" || c.kind === "create")) {
    return { kind: c.kind, archives: c.archives };
  }
  return prev;
}

// methodAfter tracks the way in this ceremony took — token, password or
// recovery (APP.md §2.2) — which decides the strip's wording for the rest
// of it. It is the ceremony's own Method and not a guess from what was
// asked: with the vault's entangled password on, a token unlock asks for a
// password beside the PIN and is still a token way in (APP.md §13). Empty
// until the core has chosen, and forgotten at the next opening.
export function methodAfter(prev: string, c: CeremonyState): string {
  if (opening(c)) return c.method ?? "";
  return c.method || prev;
}

// wordedForToken says whether the three cards read as the token flow —
// "Waiting for the key", the PIN, the touch — rather than as a typed
// secret. A check of a backup and a finish-setup are never worded for a
// token, whatever they ask for. Before the core names the method, the
// step is the only evidence there is: a password or recovery prompt is
// not a token flow.
export function wordedForToken(method: string, c: CeremonyState | null | undefined): boolean {
  if (!c) return true;
  if (c.kind === "verify" || c.kind === "setup") return false;
  if (method) return method === "token";
  return c.step !== CeremonyStep.StepPassword && c.step !== CeremonyStep.StepRecovery;
}

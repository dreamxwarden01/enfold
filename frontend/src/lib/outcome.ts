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

// secretAskedAfter tracks whether this ceremony asked for a typed secret —
// a password or the recovery digits — which decides the strip's wording
// for the rest of it. A reveal (the recovery step with no prompt) is not
// an ask.
export function secretAskedAfter(prev: boolean, c: CeremonyState): boolean {
  if (opening(c)) return false;
  if ((c.step === CeremonyStep.StepPassword || c.step === CeremonyStep.StepRecovery) && !!c.promptId) return true;
  return prev;
}

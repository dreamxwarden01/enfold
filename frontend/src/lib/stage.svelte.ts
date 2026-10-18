// The view a ceremony's panel holds, as it follows the view the facts ask
// for (lib/lockscreen): the lock screen's stage and the dialogs' ceremony
// panel turn their views the same way (APP.md §6, Motion: Unlock; ruled
// 2026-10-17), so the rule lives once, here.
//
//   · "hold" keeps the view on the panel (shownAfter);
//   · the key art keeps its found LED for FOUND_HOLD before the next step
//     crosses in (holdBefore), and the insert view keeps the step it was
//     drawn for meanwhile, so its words do not change under that LED;
//   · the view turns in a pre-effect, in the same pass as the facts that
//     turn it, so the leaving view is paused before it could redraw with
//     the next step's facts and leaves as it was — the PIN's fields as the
//     touch arrives, the touch's words as it times out. A post-effect let
//     the old view redraw once, emptied, before it left.
//
// Constructed while a component initialises, since its effects belong to
// that component.
import { untrack } from "svelte";
import { CeremonyStep } from "./api";
import { holdBefore, shownAfter } from "./lockscreen";
import type { View } from "./lockscreen";
import { delay } from "./motion";

export class HeldView {
  shown = $state<View>("wait");
  // found: the key art's LED is held solid, the YubiKey having answered.
  found = $state(false);
  // insertStep: the insert view's own step, kept while the panel holds it
  // after the core has moved on.
  insertStep = $state<CeremonyStep>(CeremonyStep.StepWaitingForKey);

  constructor(want: () => View, c: () => { step: CeremonyStep; removeLabel?: string } | null | undefined) {
    this.shown = untrack(() => shownAfter(null, want()));
    $effect.pre(() => {
      const w = want();
      const from = untrack(() => this.shown);
      const to = shownAfter(from, w);
      if (to === from) {
        this.found = false;
        return;
      }
      const ms = delay(holdBefore(from, to));
      if (!ms) {
        this.found = false;
        this.shown = to;
        return;
      }
      this.found = true;
      const t = setTimeout(() => {
        this.found = false;
        this.shown = to;
      }, ms);
      return () => clearTimeout(t);
    });
    $effect.pre(() => {
      const ev = c();
      const s = ev?.step;
      if (s === CeremonyStep.StepWaitingForKey || s === CeremonyStep.StepTwoKeys || s === CeremonyStep.StepProbing) this.insertStep = s;
      // An enrolment's SwapKey with nothing to remove is the same wait.
      else if (s === CeremonyStep.StepSwapKey && !ev?.removeLabel) this.insertStep = CeremonyStep.StepWaitingForKey;
    });
  }
}

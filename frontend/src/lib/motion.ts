// Motion (APP.md §6): one vocabulary for every transition, in milliseconds,
// mirrored by the stylesheet's --dur-* tokens. The duration collapses to
// zero under prefers-reduced-motion, checked when the transition starts.
import { prefersReducedMotion } from "svelte/motion";
import { cubicIn, cubicOut } from "svelte/easing";

export const TAP = 90; // press feedback
export const LEAVE = 100; // exit
export const HOVER = 120; // a control answering
export const FAST = 140; // enter
export const OUT = 160; // a control settling when left; a scene leaving
export const MOVE = 220; // a change of scene
export const SETTLE = 320; // the one deliberate pause: the unlock's full stop
export const GAP = 60; // what arrives waits this long for what leaves: no two texts at once

// enter eases out (arrives and settles). A crossfade's exit eases out
// too — fast first, so the old text is gone before the new one is
// readable; exit (eases in) is for a thing that travels away.
export const enter = cubicOut;
export const exit = cubicIn;

export function motion<T extends object>(ms: number = FAST, extra?: T): { duration: number } & T {
  return { duration: prefersReducedMotion.current ? 0 : ms, ...(extra ?? ({} as T)) };
}

// delay is a delay that reduced motion also removes.
export function delay(ms: number): number {
  return prefersReducedMotion.current ? 0 : ms;
}

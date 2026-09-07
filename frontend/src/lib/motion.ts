// Motion (APP.md §6): every dialog, menu and panel enters and leaves with
// one short fade; the duration collapses to zero under
// prefers-reduced-motion, checked when the transition starts.
import { prefersReducedMotion } from "svelte/motion";

export const FAST = 140; // enter
export const LEAVE = 100; // exit

export function motion<T extends object>(ms: number = FAST, extra?: T): { duration: number } & T {
  return { duration: prefersReducedMotion.current ? 0 : ms, ...(extra ?? ({} as T)) };
}

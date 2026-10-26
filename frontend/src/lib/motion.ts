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

// List motion (direction 4 of the design review, phase 4b; APP.md §6,
// Motion): only the list's body moves, never its header or the toolbar.
// A folder change fades the tbody in from SLIDE px on the side it came
// from — into a folder from the right, up from the left — over FAST; a
// row an operation added wears the accent wash for WASH, fading out
// (app.css .fresh); a row a Delete takes fades out over LEAVE before the
// list is read again. None of it under reduced motion.
export const SLIDE = 8;
export const WASH = 1200;

// Travel is the side a list's new contents come from: 1 from the right
// (deeper), -1 from the left (back up), 0 for no change of place.
export type Travel = -1 | 0 | 1;

// travel compares two paths, each the ids from the root down to the
// place shown: a path that extends the old one went in; any other change
// of place came back up (a crumb, `..`, a folder gone from under the
// page) or across, which reads as leaving the place first.
export function travel(from: readonly string[], to: readonly string[]): Travel {
  if (from.length === 0 || to.length === 0) return 0;
  if (from.length === to.length && from.every((id, i) => id === to[i])) return 0;
  if (to.length > from.length && from.every((id, i) => id === to[i])) return 1;
  return -1;
}

// slideIn plays the folder change on the list's body: Web Animations on
// the element itself, so nothing is remounted and the keyboard focus stays
// where it was. A second change while one plays starts over.
//
// The offset moves in whole device pixels (slideSteps): a table's body
// painted at a fractional offset draws every cell's edge anti-aliased, so
// the selected row's wash and the focus ring showed a seam at each column
// for the length of the fade (the final shoot of phase 4b). The opacity
// runs smoothly on the entering curve beside it.
export function slideIn(el: Element | null | undefined, from: Travel): void {
  if (!el || from === 0 || prefersReducedMotion.current || typeof el.animate !== "function") return;
  for (const a of el.getAnimations()) a.cancel();
  el.animate([{ opacity: 0 }, { opacity: 1 }], {
    duration: FAST,
    // The stylesheet's entering curve, read from its token.
    easing: getComputedStyle(document.documentElement).getPropertyValue("--ease-enter").trim() || "ease-out",
  });
  el.animate(
    slideSteps(from, window.devicePixelRatio || 1).map(([offset, x]) => ({ offset, transform: x === 0 ? "none" : `translateX(${x}px)`, easing: "steps(1, jump-end)" })),
    { duration: FAST },
  );
}

// slideSteps: the travel's offsets as [time 0..1, px] keyframes, each held
// until the next (steps(1, jump-end)): SLIDE px on the side it came from,
// down to none along the entering curve, every value a whole device pixel
// (1 / dpr of a CSS pixel), sampled every SLIDE_TICK of the time — finer
// than a frame, so the curve keeps its shape. Pure, for the tests.
export const SLIDE_TICK = 1 / 14;

export function slideSteps(from: Travel, dpr: number): [number, number][] {
  const snap = (x: number) => Math.round(x * dpr) / dpr;
  const out: [number, number][] = [];
  const n = Math.round(1 / SLIDE_TICK);
  for (let i = 0; i <= n; i++) {
    const t = i / n;
    const x = snap(from * SLIDE * (1 - enter(t))) || 0;
    if (out.length && out[out.length - 1][1] === x && i < n) continue;
    out.push([t, x]);
  }
  out[out.length - 1] = [1, 0];
  return out;
}

// unfold opens and closes a stateful banner's height (NoticeSlot; direction
// 4: grid rows 0fr → 1fr over MOVE on the entering curve, the opacity with
// it), so the list under it slides rather than jumps. The node is a
// one-track grid whose child has min-height: 0 (app.css .fold); the
// overflow is clipped only while it moves. Local, as every transition is
// by default: a banner standing when its page is drawn is simply there.
export function unfold(_node: Element, opts: { duration?: number } = {}) {
  const duration = opts.duration ?? (prefersReducedMotion.current ? 0 : MOVE);
  return {
    duration,
    easing: enter,
    css: (t: number) => `grid-template-rows: minmax(0, ${t}fr); opacity: ${t}; overflow: hidden;`,
  };
}

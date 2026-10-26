// Spring-loaded folders (direction 7, Explorer's habits; APP.md §6, the
// Archive page): during the list's own drag of its selected rows — a
// self-drag, the native drag Shell.DragOut runs — a folder row the
// pointer rests on for SPRING_MS is entered, and the `..` row goes up, as
// Explorer's do; the drag goes on and the drop lands where the pointer is
// in the folder now shown. Never for a drag from outside the window: an
// Explorer drop is an add (APP.md §3), not a move, and its hover is the
// runtime's.
//
// The page reports every dragover with the target under the pointer — or
// none — and the spring fires once one target has been hovered without a
// break for SPRING_MS. A dragover arrives about every 50 ms while a drag
// hovers, moving or still, so a hover not refreshed within FRESH_MS has
// ended — the pointer left the window, the drop came — and the spring
// does not fire for it even if no dragleave said so (one between two
// cells of the same row says nothing about the row). FRESH_MS is wide of
// the HTML drag model's cadence for a still pointer (a dragover every
// 350 ms, give or take 200), so a hover resting still is never taken for
// one that ended.
//
// Once a spring fires, the folder shown has changed under a pointer that
// has not moved, and another row now sits beneath it — in a subfolder the
// `..` row stands where the first folder stood. Arming again at once would
// trade the two places every SPRING_MS for as long as the pointer rests
// (into 2024, up to the root, into 2024 again), and the drop would land in
// whichever happened to be shown. So after a spring the pointer must move
// past REST_SLOP from where it fired before anything arms again, as
// Explorer waits for the mouse to move.
import { DRAG_THRESHOLD } from "./dragout";
import type { DropAt } from "./dragout";

export const SPRING_MS = 700;
export const FRESH_MS = 600;
export const REST_SLOP = DRAG_THRESHOLD;

// Point: the dragover's clientX and clientY.
export interface Point {
  x: number;
  y: number;
}

// moved: whether a pointer at `p` has left the rest at `from`.
export function moved(from: Point, p: Point, slop = REST_SLOP): boolean {
  return Math.abs(p.x - from.x) > slop || Math.abs(p.y - from.y) > slop;
}

export interface SpringTarget {
  id: string;
  isDir: boolean;
}

// springs: whether resting on this target enters it. A folder row the
// drag does not carry — the carried folder cannot be entered, and its
// own drop is refused as a move into itself — and the `..` row, which
// goes up even into the folder the drag came from, since the drag may be
// on its way further up. A file row and a crumb in the head do not.
export function springs(t: SpringTarget, at: DropAt, carried: readonly string[]): boolean {
  if (at === "up") return true;
  return at === "row" && t.isDir && !carried.includes(t.id);
}

// springKey names a hover: the row's id, or `..` by the folder it leads
// to, so going up twice in a row is two hovers.
export function springKey(t: SpringTarget, at: DropAt): string {
  return `${at}:${t.id}`;
}

export interface SpringClock {
  now(): number;
  set(fn: () => void, ms: number): unknown;
  clear(handle: unknown): void;
}

const realClock: SpringClock = {
  now: () => performance.now(),
  set: (fn, ms) => setTimeout(fn, ms),
  clear: (h) => clearTimeout(h as ReturnType<typeof setTimeout>),
};

export interface Spring {
  // hover: a dragover at `at` on `key` (null: on nothing that springs).
  // The same key again keeps the timer running; another key starts it
  // anew. After a spring has fired nothing arms until a dragover comes
  // from a point that has moved past REST_SLOP from where it fired.
  hover(key: string | null, at: Point, fire: () => void): void;
  // cancel: the drag ended, dropped or left; nothing fires, and the rest
  // after a fired spring is forgotten.
  cancel(): void;
  readonly armed: string | null;
}

export function spring(clock: SpringClock = realClock, wait = SPRING_MS, fresh = FRESH_MS): Spring {
  let armed: string | null = null;
  let handle: unknown = undefined;
  let lastAt = 0;
  let fireNow: (() => void) | null = null;
  // lastPoint: where the latest dragover was; rest: where the pointer
  // stood when a spring fired, until it moves away.
  let lastPoint: Point | null = null;
  let rest: Point | null = null;

  function disarm() {
    if (handle !== undefined) clock.clear(handle);
    handle = undefined;
    armed = null;
    fireNow = null;
  }

  return {
    hover(key, at, fire) {
      lastPoint = at;
      if (rest) {
        if (!moved(rest, at)) {
          disarm();
          return;
        }
        rest = null;
      }
      if (key === null) {
        disarm();
        return;
      }
      lastAt = clock.now();
      fireNow = fire;
      if (key === armed) return;
      disarm();
      armed = key;
      fireNow = fire;
      handle = clock.set(() => {
        handle = undefined;
        const f = fireNow;
        const stale = clock.now() - lastAt > fresh;
        armed = null;
        fireNow = null;
        if (!f || stale) return;
        rest = lastPoint;
        f();
      }, wait);
    },
    cancel() {
      disarm();
      rest = null;
      lastPoint = null;
    },
    get armed() {
      return armed;
    },
  };
}

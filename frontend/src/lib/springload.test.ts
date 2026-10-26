import { describe, expect, it } from "vitest";
import { FRESH_MS, REST_SLOP, SPRING_MS, moved, spring, springKey, springs } from "./springload";
import type { Point, SpringClock } from "./springload";

// Spring-loaded folders (direction 7; APP.md §6): our own drag resting
// 700 ms on a folder row enters it, on `..` goes up.

// A clock the test moves by hand.
function fakeClock() {
  let now = 0;
  let next = 1;
  const timers = new Map<number, { at: number; fn: () => void }>();
  const clock: SpringClock = {
    now: () => now,
    set: (fn, ms) => {
      const h = next++;
      timers.set(h, { at: now + ms, fn });
      return h;
    },
    clear: (h) => {
      timers.delete(h as number);
    },
  };
  const advance = (ms: number) => {
    const end = now + ms;
    for (;;) {
      const due = [...timers.entries()].filter(([, t]) => t.at <= end).sort((a, b) => a[1].at - b[1].at)[0];
      if (!due) break;
      now = due[1].at;
      timers.delete(due[0]);
      due[1].fn();
    }
    now = end;
  };
  return { clock, advance, pending: () => timers.size };
}

// A drag hovering at `at`: a dragover every 50 ms for `ms`.
const still: Point = { x: 100, y: 40 };
function hoverFor(s: ReturnType<typeof spring>, f: ReturnType<typeof fakeClock>, key: string | null, ms: number, fire: () => void, at: Point = still) {
  for (let t = 0; t < ms; t += 50) {
    s.hover(key, at, fire);
    f.advance(50);
  }
}

describe("what springs", () => {
  it("is a folder row the drag does not carry, and `..`", () => {
    expect(springs({ id: "f", isDir: true }, "row", ["a", "b"])).toBe(true);
    expect(springs({ id: "a", isDir: true }, "row", ["a", "b"])).toBe(false);
    expect(springs({ id: "x", isDir: false }, "row", ["a"])).toBe(false);
    expect(springs({ id: "p", isDir: true }, "up", ["a"])).toBe(true);
  });
  it("is never a crumb in the head, nor the list's blank area", () => {
    expect(springs({ id: "r", isDir: true }, "head", ["a"])).toBe(false);
    expect(springs({ id: "r", isDir: true }, "list", ["a"])).toBe(false);
  });
  it("names `..` by the folder it leads to", () => {
    expect(springKey({ id: "p1", isDir: true }, "up")).not.toBe(springKey({ id: "p2", isDir: true }, "up"));
    expect(springKey({ id: "p1", isDir: true }, "up")).not.toBe(springKey({ id: "p1", isDir: true }, "row"));
  });
});

describe("the spring", () => {
  it("fires once a hover has rested SPRING_MS on one target", () => {
    const f = fakeClock();
    const s = spring(f.clock);
    let fired = 0;
    hoverFor(s, f, "row:a", SPRING_MS - 50, () => fired++);
    expect(fired).toBe(0);
    hoverFor(s, f, "row:a", 100, () => fired++);
    expect(fired).toBe(1);
    expect(SPRING_MS).toBe(700);
  });
  it("starts again on another target", () => {
    const f = fakeClock();
    const s = spring(f.clock);
    const fired: string[] = [];
    hoverFor(s, f, "row:a", 500, () => fired.push("a"));
    hoverFor(s, f, "row:b", 500, () => fired.push("b"));
    expect(fired).toEqual([]);
    hoverFor(s, f, "row:b", 300, () => fired.push("b"));
    expect(fired).toEqual(["b"]);
  });
  it("stops over nothing that springs, and on cancel", () => {
    const f = fakeClock();
    const s = spring(f.clock);
    let fired = 0;
    hoverFor(s, f, "row:a", 400, () => fired++);
    s.hover(null, still, () => fired++);
    expect(s.armed).toBeNull();
    f.advance(1000);
    hoverFor(s, f, "row:a", 400, () => fired++);
    s.cancel();
    f.advance(1000);
    expect(fired).toBe(0);
    expect(f.pending()).toBe(0);
  });
  it("does not fire for a hover that went quiet — the pointer left the window, the drop came", () => {
    const f = fakeClock();
    const s = spring(f.clock);
    let fired = 0;
    s.hover("row:a", still, () => fired++);
    f.advance(FRESH_MS + 50);
    f.advance(SPRING_MS);
    expect(fired).toBe(0);
  });
  it("keeps a still hover alive at the slow cadence of a still pointer", () => {
    // The HTML drag model fires dragover about every 350 ms (give or take
    // 200) while the pointer does not move.
    const f = fakeClock();
    const s = spring(f.clock);
    let fired = 0;
    for (let t = 0; t < SPRING_MS + 550; t += 550) {
      s.hover("row:a", still, () => fired++);
      f.advance(550);
    }
    expect(FRESH_MS).toBeGreaterThan(550);
    expect(fired).toBe(1);
  });
  it("does not arm again under a pointer that has not moved since it fired", () => {
    // The review's case: resting on the root's first folder enters it, and
    // the `..` row now sits under the still pointer; it must not go up, nor
    // the folder be entered again, until the pointer moves.
    const f = fakeClock();
    const s = spring(f.clock);
    const fired: string[] = [];
    hoverFor(s, f, "row:2024", SPRING_MS + 50, () => fired.push("into 2024"));
    expect(fired).toEqual(["into 2024"]);
    hoverFor(s, f, "up:root", SPRING_MS * 4, () => fired.push("up"));
    expect(fired).toEqual(["into 2024"]);
    expect(s.armed).toBeNull();
    // A wobble within the slop is still a rest.
    hoverFor(s, f, "up:root", SPRING_MS * 2, () => fired.push("up"), { x: still.x + REST_SLOP, y: still.y - REST_SLOP });
    expect(fired).toEqual(["into 2024"]);
    // Moved away and back on `..`: that is a new hover, and it goes up.
    hoverFor(s, f, "up:root", SPRING_MS + 50, () => fired.push("up"), { x: still.x + 30, y: still.y });
    expect(fired).toEqual(["into 2024", "up"]);
  });
  it("forgets the rest when the drag ends", () => {
    const f = fakeClock();
    const s = spring(f.clock);
    let fired = 0;
    hoverFor(s, f, "row:a", SPRING_MS + 50, () => fired++);
    s.cancel();
    hoverFor(s, f, "row:a", SPRING_MS + 50, () => fired++);
    expect(fired).toBe(2);
  });
  it("measures the move on either axis past the slop", () => {
    expect(moved({ x: 0, y: 0 }, { x: REST_SLOP, y: REST_SLOP })).toBe(false);
    expect(moved({ x: 0, y: 0 }, { x: REST_SLOP + 1, y: 0 })).toBe(true);
    expect(moved({ x: 0, y: 0 }, { x: 0, y: -REST_SLOP - 1 })).toBe(true);
  });
});

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { PENDING_DELAY, pending, pendingTimer } from "./pending";
import type { PendingNode } from "./pending";

// The one pending primitive (direction 4, phase 3): nothing for 300 ms,
// then the ring and aria-busy, gone when the wait settles.

beforeEach(() => {
  vi.useFakeTimers();
});
afterEach(() => {
  vi.useRealTimers();
});

describe("the pending timer", () => {
  it("shows nothing for a wait under 300 ms", () => {
    const seen: boolean[] = [];
    const t = pendingTimer((s) => seen.push(s));
    t.start();
    vi.advanceTimersByTime(PENDING_DELAY - 1);
    t.settle();
    vi.advanceTimersByTime(1000);
    expect(seen).toEqual([]);
    expect(t.showing).toBe(false);
  });

  it("shows once the wait passes 300 ms, and goes when it settles", () => {
    const seen: boolean[] = [];
    const t = pendingTimer((s) => seen.push(s));
    t.start();
    vi.advanceTimersByTime(PENDING_DELAY);
    expect(seen).toEqual([true]);
    expect(t.showing).toBe(true);
    t.settle();
    expect(seen).toEqual([true, false]);
  });

  it("does not restart on a second start, nor say anything on a second settle", () => {
    const seen: boolean[] = [];
    const t = pendingTimer((s) => seen.push(s));
    t.start();
    vi.advanceTimersByTime(200);
    t.start();
    vi.advanceTimersByTime(100);
    expect(seen).toEqual([true]);
    t.start();
    t.settle();
    t.settle();
    expect(seen).toEqual([true, false]);
  });
});

function node() {
  const classes = new Set<string>();
  const attrs = new Map<string, string>();
  const n: PendingNode = {
    classList: { add: (c) => void classes.add(c), remove: (c) => void classes.delete(c) },
    setAttribute: (k, v) => void attrs.set(k, v),
    removeAttribute: (k) => void attrs.delete(k),
  };
  return { n, classes, attrs };
}

describe("the pending action", () => {
  it("marks the control after 300 ms of a promise, and clears it when the promise settles", async () => {
    const { n, classes, attrs } = node();
    let resolve!: () => void;
    const p = new Promise<void>((r) => (resolve = r));
    pending(n, p);
    vi.advanceTimersByTime(PENDING_DELAY);
    expect(classes.has("pending")).toBe(true);
    expect(attrs.get("aria-busy")).toBe("true");
    resolve();
    await vi.runAllTimersAsync();
    expect(classes.has("pending")).toBe(false);
    expect(attrs.has("aria-busy")).toBe(false);
  });

  it("settles on a rejection too", async () => {
    const { n, classes } = node();
    let reject!: (e: unknown) => void;
    const p = new Promise<void>((_, r) => (reject = r));
    pending(n, p);
    vi.advanceTimersByTime(PENDING_DELAY);
    reject(new Error("refused"));
    await vi.runAllTimersAsync();
    expect(classes.has("pending")).toBe(false);
  });

  it("follows a flag, and tells armed when the ring shows and goes", () => {
    const { n, classes } = node();
    const armed: boolean[] = [];
    const a = pending(n, { on: true, armed: (s) => armed.push(s) });
    vi.advanceTimersByTime(PENDING_DELAY);
    expect(classes.has("pending")).toBe(true);
    a.update({ on: false, armed: (s) => armed.push(s) });
    expect(classes.has("pending")).toBe(false);
    expect(armed).toEqual([true, false]);
  });

  it("ignores the settling of a promise a newer one replaced", async () => {
    const { n, classes } = node();
    let first!: () => void;
    const p1 = new Promise<void>((r) => (first = r));
    const p2 = new Promise<void>(() => {});
    const a = pending(n, p1);
    a.update(p2);
    vi.advanceTimersByTime(PENDING_DELAY);
    first();
    await vi.runAllTimersAsync();
    expect(classes.has("pending")).toBe(true);
    a.destroy();
    expect(classes.has("pending")).toBe(false);
  });

  it("shows nothing for null or false", () => {
    const { n, classes } = node();
    pending(n, null);
    pending(n, false);
    vi.advanceTimersByTime(1000);
    expect(classes.has("pending")).toBe(false);
  });
});

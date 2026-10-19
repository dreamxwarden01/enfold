import { describe, expect, it } from "vitest";
import { TIP_DELAY, TIP_GAP, TIP_MARGIN, Tips, tipBeside, tipPlacement } from "./tip";

// The app's tooltip (APP.md §6, Controls; ruled 2026-10-18): 300 ms of
// hover or keyboard focus at once, gone on leave, blur, Escape or a press,
// one at a time. The clock here is a list of timers advanced by hand.
function rig() {
  let now = 0;
  let next = 1;
  const timers = new Map<number, { at: number; fn: () => void }>();
  const log: string[] = [];
  const tips = new Tips<string>(
    { show: (o) => log.push(`show ${o}`), hide: (o) => log.push(`hide ${o}`) },
    {
      set: (fn, ms) => {
        const id = next++;
        timers.set(id, { at: now + ms, fn });
        return id;
      },
      clear: (h) => void timers.delete(h as number),
    },
  );
  const advance = (ms: number) => {
    now += ms;
    for (const [id, t] of [...timers]) {
      if (t.at <= now) {
        timers.delete(id);
        t.fn();
      }
    }
  };
  return { tips, log, advance, pending: () => timers.size };
}

describe("the tooltip's timing", () => {
  it("shows after 300 ms of hover, and not a moment before", () => {
    const r = rig();
    r.tips.hover("a");
    r.advance(TIP_DELAY - 1);
    expect(r.log).toEqual([]);
    r.advance(1);
    expect(r.log).toEqual(["show a"]);
    expect(r.tips.current).toBe("a");
  });

  it("shows nothing when the pointer leaves before the delay", () => {
    const r = rig();
    r.tips.hover("a");
    r.advance(200);
    r.tips.leave("a");
    r.advance(500);
    expect(r.log).toEqual([]);
    expect(r.pending()).toBe(0);
  });

  it("shows at once on focus, and goes on blur", () => {
    const r = rig();
    r.tips.focus("a");
    expect(r.log).toEqual(["show a"]);
    r.tips.blur("a");
    expect(r.log).toEqual(["show a", "hide a"]);
    expect(r.tips.current).toBeNull();
  });

  it("goes on leave once shown", () => {
    const r = rig();
    r.tips.hover("a");
    r.advance(TIP_DELAY);
    r.tips.leave("a");
    expect(r.log).toEqual(["show a", "hide a"]);
  });

  it("keeps one at a time: a newer tip replaces the one up", () => {
    const r = rig();
    r.tips.focus("a");
    r.tips.hover("b");
    // a stays until b's own delay has passed.
    r.advance(TIP_DELAY - 1);
    expect(r.tips.current).toBe("a");
    r.advance(1);
    expect(r.log).toEqual(["show a", "hide a", "show b"]);
    expect(r.tips.current).toBe("b");
  });

  it("lets only the newest hover's timer run", () => {
    const r = rig();
    r.tips.hover("a");
    r.advance(200);
    r.tips.hover("b");
    r.advance(200);
    expect(r.log).toEqual([]);
    r.advance(100);
    expect(r.log).toEqual(["show b"]);
  });

  it("closes on Escape (dismiss), the waiting one included", () => {
    const r = rig();
    r.tips.focus("a");
    r.tips.hover("b");
    r.tips.dismiss();
    r.advance(TIP_DELAY * 2);
    expect(r.log).toEqual(["show a", "hide a"]);
    expect(r.tips.current).toBeNull();
  });

  it("closes on a press and stays closed until the pointer leaves", () => {
    const r = rig();
    r.tips.hover("a");
    r.advance(TIP_DELAY);
    r.tips.press("a");
    r.tips.hover("a"); // the pointer still resting on it
    r.advance(TIP_DELAY * 2);
    expect(r.log).toEqual(["show a", "hide a"]);
    r.tips.leave("a");
    r.tips.hover("a");
    r.advance(TIP_DELAY);
    expect(r.log).toEqual(["show a", "hide a", "show a"]);
  });

  it("ignores a leave or a blur of an owner whose tip is not up", () => {
    const r = rig();
    r.tips.focus("a");
    r.tips.leave("b");
    r.tips.blur("b");
    expect(r.tips.current).toBe("a");
    expect(r.log).toEqual(["show a"]);
  });
});

describe("where the tooltip stands", () => {
  const view = { width: 1000, height: 600 };
  const box = { width: 120, height: 24 };

  it("stands 4 px under the element, on its left edge", () => {
    expect(tipPlacement({ left: 100, top: 40, bottom: 72 }, box, view)).toEqual({ x: 100, y: 72 + TIP_GAP, above: false });
  });

  it("goes above when there is no room below", () => {
    expect(tipPlacement({ left: 100, top: 560, bottom: 590 }, box, view)).toEqual({ x: 100, y: 560 - TIP_GAP - 24, above: true });
  });

  it("is pulled back inside the window's edges", () => {
    expect(tipPlacement({ left: 950, top: 40, bottom: 72 }, box, view).x).toBe(1000 - 120 - TIP_MARGIN);
    expect(tipPlacement({ left: 2, top: 40, bottom: 72 }, box, view).x).toBe(TIP_MARGIN);
  });
});

describe("where the rail's tooltip stands", () => {
  const view = { width: 1000, height: 600 };
  const box = { width: 120, height: 24 };

  it("stands 4 px right of the element, centred on it", () => {
    expect(tipBeside({ left: 4, right: 44, top: 100, bottom: 140 }, box, view)).toEqual({ x: 44 + TIP_GAP, y: 120 - 12, above: false });
  });

  it("is kept inside the window's height", () => {
    expect(tipBeside({ left: 4, right: 44, top: 0, bottom: 10 }, box, view).y).toBe(TIP_MARGIN);
    expect(tipBeside({ left: 4, right: 44, top: 590, bottom: 600 }, box, view).y).toBe(600 - 24 - TIP_MARGIN);
  });

  it("falls back to the place under the element with no room to the right", () => {
    const a = { left: 900, right: 990, top: 100, bottom: 140 };
    expect(tipBeside(a, box, view)).toEqual(tipPlacement(a, box, view));
  });
});

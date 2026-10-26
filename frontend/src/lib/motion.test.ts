import { describe, expect, it } from "vitest";
import { MOVE, SLIDE, slideSteps, travel, unfold } from "./motion";

describe("travel", () => {
  it("comes from the right going into a folder", () => {
    expect(travel(["r"], ["r", "a"])).toBe(1);
    expect(travel(["r", "a"], ["r", "a", "b", "c"])).toBe(1);
  });

  it("comes from the left going up, or across", () => {
    expect(travel(["r", "a"], ["r"])).toBe(-1);
    expect(travel(["r", "a", "b"], ["r"])).toBe(-1);
    expect(travel(["r", "a"], ["r", "b"])).toBe(-1);
  });

  it("does not move for the same place, or with nothing before", () => {
    expect(travel(["r", "a"], ["r", "a"])).toBe(0);
    expect(travel([], ["r"])).toBe(0);
    expect(travel(["r"], [])).toBe(0);
  });
});

describe("unfold", () => {
  it("opens the grid's one row with the opacity, over the scene's time", () => {
    const t = unfold({} as Element);
    expect(t.duration).toBe(MOVE);
    expect(t.css(0)).toContain("grid-template-rows: minmax(0, 0fr)");
    expect(t.css(1)).toContain("grid-template-rows: minmax(0, 1fr)");
    expect(t.css(0.5)).toContain("opacity: 0.5");
  });
});

describe("slideSteps, the folder change's offset in whole device pixels", () => {
  for (const dpr of [1, 1.25, 1.5, 2]) {
    it(`starts at SLIDE on its side, ends at none and holds whole pixels at ${dpr}x`, () => {
      for (const from of [1, -1] as const) {
        const k = slideSteps(from, dpr);
        expect(k[0]).toEqual([0, from * SLIDE]);
        expect(k[k.length - 1]).toEqual([1, 0]);
        for (const [, x] of k) expect(Number.isInteger(Math.round(x * dpr * 1e9) / 1e9)).toBe(true);
        for (let i = 1; i < k.length; i++) {
          expect(k[i][0]).toBeGreaterThan(k[i - 1][0]);
          expect(Math.abs(k[i][1])).toBeLessThanOrEqual(Math.abs(k[i - 1][1]));
        }
      }
    });
  }
});

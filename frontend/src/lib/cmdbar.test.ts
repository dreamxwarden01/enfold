import { describe, expect, it } from "vitest";
import { fold, folded } from "./cmdbar";

// The three verbs as the bar draws them: Refresh fingerprint (with the
// separator before it), Compact…, Re-key archive….
const W = [170, 96, 132];

describe("fold (direction 6, the command bar)", () => {
  it("keeps every verb when there is room for all of them", () => {
    expect(fold(W, 1000)).toBe(3);
    expect(fold(W, 398)).toBe(3); // exactly the room they need
  });

  it("folds from the right, one verb at a time", () => {
    expect(fold(W, 397)).toBe(2);
    expect(fold(W, 266)).toBe(2);
    expect(fold(W, 265)).toBe(1);
    expect(fold(W, 170)).toBe(1);
    expect(fold(W, 169)).toBe(0);
  });

  it("folds everything when there is no room at all", () => {
    expect(fold(W, 0)).toBe(0);
    expect(fold(W, -40)).toBe(0);
  });

  it("never keeps a verb whose left neighbour folded", () => {
    // Re-key archive… alone would fit in what Compact… cannot have.
    expect(fold([170, 300, 20], 200)).toBe(1);
  });

  it("starts whole before the bar is measured", () => {
    expect(fold([0, 0, 0], 0)).toBe(3);
    expect(fold([Number.NaN, 96, 132], 228)).toBe(3);
    expect(fold(W, Number.NaN)).toBe(3);
    expect(fold(W, Number.POSITIVE_INFINITY)).toBe(3);
  });

  it("has nothing to fold in an empty bar", () => {
    expect(fold([], 100)).toBe(0);
  });
});

describe("folded (the top of More)", () => {
  const verbs = ["refresh", "compact", "rekey"];

  it("is what the bar left out, in the bar's order", () => {
    expect(folded(verbs, 3)).toEqual([]);
    expect(folded(verbs, 2)).toEqual(["rekey"]);
    expect(folded(verbs, 1)).toEqual(["compact", "rekey"]);
    expect(folded(verbs, 0)).toEqual(["refresh", "compact", "rekey"]);
  });

  it("reads a count out of range as the nearest end", () => {
    expect(folded(verbs, 7)).toEqual([]);
    expect(folded(verbs, -1)).toEqual(verbs);
    expect(folded(verbs, 1.6)).toEqual(["compact", "rekey"]);
  });
});

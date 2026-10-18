import { describe, expect, it } from "vitest";
import { band, digitsOnly, keptSomehow, pickGroups, readbackMark } from "./reveal";

const none = { savedTo: "", printed: false, wroteDown: false };
const labels = (k: typeof none) => band(k).map((b) => `${b.accent ? "*" : ""}${b.label}`);

// "None asks a second time" (APP.md §6, ruled 2026-09-09): each of the
// three ways out counts at once, and from the first of them *Done* leads.
describe("the reveal's done state", () => {
  it("is not reached by showing the key alone", () => {
    expect(keptSomehow(none)).toBe(false);
  });
  it("is reached by a save the core acknowledged", () => {
    expect(keptSomehow({ ...none, savedTo: "D:\\Safe\\key.txt" })).toBe(true);
  });
  it("is reached by pressing Print…, with nothing read of what happened there", () => {
    expect(keptSomehow({ ...none, printed: true })).toBe(true);
  });
  it("is reached by *I have written it down*, with no second dialog", () => {
    expect(keptSomehow({ ...none, wroteDown: true })).toBe(true);
  });
  it("is not reached by a path the shell handed back empty", () => {
    expect(keptSomehow({ ...none, savedTo: "   " })).toBe(false);
  });
});

// The band (direction 9, ruled 2026-10-18): Print… suggested first, then
// the text file, then the written-down way; after any of them, Done.
describe("the reveal's band", () => {
  it("suggests the print before anything is done", () => {
    expect(labels(none)).toEqual(["*Print…", "Save as a text file…", "I have written it down"]);
    expect(band(none).map((b) => b.act)).toEqual(["print", "save", "wrote"]);
  });
  it("leads with Done after a print", () => {
    expect(labels({ ...none, printed: true })).toEqual(["*Done", "Print again…", "Save as a text file…"]);
  });
  it("leads with Done after a save", () => {
    expect(labels({ ...none, savedTo: "E:\\key.txt" })).toEqual(["*Done", "Print…", "Save again…"]);
  });
  it("leads with Done after *I have written it down*", () => {
    expect(labels({ ...none, wroteDown: true })).toEqual(["*Done", "Print…", "Save as a text file…"]);
    expect(band({ ...none, wroteDown: true }).map((b) => b.act)).toEqual(["done", "print", "save"]);
  });
  it("has exactly one accent, first", () => {
    for (const k of [none, { ...none, printed: true }, { ...none, savedTo: "x", printed: true, wroteDown: true }]) {
      const b = band(k);
      expect(b.filter((x) => x.accent).length).toBe(1);
      expect(b[0].accent).toBe(true);
    }
  });
});

// The readback: optional, two random groups, never a gate.
describe("the readback's two groups", () => {
  const seq = (...xs: number[]) => {
    let i = 0;
    return () => xs[i++ % xs.length];
  };
  it("draws two distinct groups, in sheet order", () => {
    expect(pickGroups(seq(0, 0))).toEqual([0, 1]);
    expect(pickGroups(seq(0.99, 0.99))).toEqual([6, 7]);
    expect(pickGroups(seq(0.5, 0.0))).toEqual([0, 4]);
    expect(pickGroups(seq(0.5, 0.5))).toEqual([3, 4]);
  });
  it("never draws the same group twice, whatever the random says", () => {
    for (let a = 0; a < 1; a += 0.05) {
      for (let b = 0; b < 1; b += 0.05) {
        const [x, y] = pickGroups(seq(a, b));
        expect(x).toBeLessThan(y);
        expect(x).toBeGreaterThanOrEqual(0);
        expect(y).toBeLessThan(8);
      }
    }
  });
  it("stays in range for a random that returns 1", () => {
    const [x, y] = pickGroups(seq(1, 1));
    expect([x, y]).toEqual([6, 7]);
  });
});

describe("a readback field", () => {
  it("holds digits only, at most six", () => {
    expect(digitsOnly("123 456")).toBe("123456");
    expect(digitsOnly("12a3")).toBe("123");
    expect(digitsOnly("1234567")).toBe("123456");
  });
  it("marks nothing before six digits are typed", () => {
    expect(readbackMark("", "123456")).toBe("");
    expect(readbackMark("12345", "999999")).toBe("");
  });
  it("ticks six digits that match, and marks six that do not", () => {
    expect(readbackMark("123456", "123456")).toBe("tick");
    expect(readbackMark("123465", "123456")).toBe("bad");
  });
});

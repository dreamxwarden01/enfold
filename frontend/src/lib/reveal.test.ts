import { describe, expect, it } from "vitest";
import { band, digitsLine, digitsOnly, keepBand, keepTitle, keptSomehow, pickGroups, readbackMark, ways } from "./reveal";
import { staysUnlocked } from "./foot";

const none = { savedTo: "", printed: false, wroteDown: false };
const row = (k: typeof none) => ways(k).map((b) => `${b.accent ? "*" : ""}${b.label}`);
const foot = (k: typeof none) => `${band(k).accent ? "*" : ""}${band(k).label}`;

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
describe("the reveal's ways and its band", () => {
  it("suggests the print in the row before anything is done, with the one statement in the band", () => {
    expect(row(none)).toEqual(["*Print…", "Save as a text file…"]);
    expect(foot(none)).toBe("I have written it down");
    expect(band(none).act).toBe("wrote");
  });
  it("turns the band into Done after a print, the row worded again", () => {
    expect(row({ ...none, printed: true })).toEqual(["Print again…", "Save as a text file…"]);
    expect(foot({ ...none, printed: true })).toBe("*Done");
  });
  it("turns the band into Done after a save", () => {
    expect(row({ ...none, savedTo: "E:\\key.txt" })).toEqual(["Print…", "Save again…"]);
    expect(foot({ ...none, savedTo: "E:\\key.txt" })).toBe("*Done");
  });
  it("turns the band into Done after *I have written it down*, which then closes", () => {
    expect(row({ ...none, wroteDown: true })).toEqual(["Print…", "Save as a text file…"]);
    expect(band({ ...none, wroteDown: true }).act).toBe("done");
  });
  it("has exactly one accent across the row and the band", () => {
    for (const k of [none, { ...none, printed: true }, { ...none, savedTo: "x" }, { ...none, savedTo: "x", printed: true, wroteDown: true }]) {
      const accents = [...ways(k), band(k)].filter((x) => x.accent).length;
      expect(accents).toBe(1);
    }
  });
});

// Keep mode (APP.md §13, ruled 2026-10-27, R-B): a replacement's new key,
// shown before it is the vault's. Each way counts at once with no second
// confirmation, the band goes on to the next key, and Cancel is the one
// way out.
describe("keep mode's band", () => {
  const kb = (k: typeof none, o: Partial<{ last: boolean; gone: boolean; stay: boolean }> = {}) =>
    keepBand(k, { last: false, gone: false, stay: false, ...o }).map((b) => `${b.accent ? "*" : ""}${b.subtle ? "~" : ""}${b.label}`);
  it("offers I have written it down and Cancel before any way is taken", () => {
    expect(kb(none)).toEqual(["I have written it down", "Cancel"]);
    expect(keepBand(none, { last: false, gone: false, stay: false }).map((b) => b.act)).toEqual(["wrote", "cancel"]);
  });
  it("goes on at once after any way, with no second confirmation", () => {
    for (const k of [{ ...none, wroteDown: true }, { ...none, printed: true }, { ...none, savedTo: "E:\\key.txt" }]) {
      expect(kb(k)).toEqual(["*Next recovery key", "Cancel"]);
      expect(kb(k, { last: true })).toEqual(["*Continue", "Cancel"]);
    }
    expect(keepBand({ ...none, wroteDown: true }, { last: false, gone: false, stay: false })[0].act).toBe("next");
  });
  it("leaves Cancel alone when the key could not be shown", () => {
    expect(kb(none, { gone: true })).toEqual(["Cancel"]);
    expect(kb({ ...none, wroteDown: true }, { gone: true, stay: true })).toEqual(["Cancel"]);
  });
  it("carries Stay unlocked in the idle deadline's last minute only, subtle, before Cancel", () => {
    const at = 1_760_000_000;
    const stay = staysUnlocked(at, 0, (at - 30) * 1000);
    expect(stay).toBe(true);
    expect(staysUnlocked(at, 0, (at - 120) * 1000)).toBe(false);
    expect(kb(none, { stay })).toEqual(["I have written it down", "~Stay unlocked", "Cancel"]);
    expect(kb({ ...none, printed: true }, { stay, last: true })).toEqual(["*Continue", "~Stay unlocked", "Cancel"]);
  });
  it("has one accent across the row and the band", () => {
    for (const k of [none, { ...none, printed: true }, { ...none, wroteDown: true }]) {
      const accents = [...ways(k), ...keepBand(k, { last: false, gone: false, stay: true })].filter((x) => x.accent).length;
      expect(accents).toBe(1);
    }
  });
  it("leaves the ordinary showing's band as it was", () => {
    expect(foot(none)).toBe("I have written it down");
    expect(foot({ ...none, wroteDown: true })).toBe("*Done");
  });
  it("titles a new key by its place, and one key in the singular", () => {
    expect(keepTitle(1, 2)).toBe("New recovery key 1 of 2");
    expect(keepTitle(2, 2)).toBe("New recovery key 2 of 2");
    expect(keepTitle(1, 1)).toBe("Your new recovery key");
  });
  it("captions the digits by when the key can be shown again", () => {
    expect(digitsLine(48)).toBe("48 digits. They can be shown again from the Vault page: unlock, then prove a YubiKey or a password once more.");
    expect(digitsLine(48, "keep", 2)).toBe("48 digits. Nothing changes until every new recovery key is kept and you confirm.");
    // One new key is singular (a count of one is singular everywhere).
    expect(digitsLine(48, "keep", 1)).toBe("48 digits. Nothing changes until this recovery key is kept and you confirm.");
    expect(digitsLine(48, "later")).toBe("48 digits. They can be shown again from the Vault page only after you add a YubiKey or a password and prove it.");
  });
});

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

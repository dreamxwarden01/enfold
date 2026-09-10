import { describe, expect, it } from "vitest";
import { cornerLabel, keptSomehow } from "./reveal";

const none = { savedTo: "", printed: false, wroteDown: false };

// "None asks a second time" (APP.md §6, ruled 2026-09-09): each of the
// three ways out counts at once, and from the first of them the corner
// button is *Done*.
describe("the reveal's done state", () => {
  it("is not reached by showing the key alone", () => {
    expect(keptSomehow(none)).toBe(false);
    expect(cornerLabel(none)).toBe("I have written it down");
  });

  it("is reached by a save the core acknowledged", () => {
    const k = { ...none, savedTo: "D:\\Safe\\key.txt" };
    expect(keptSomehow(k)).toBe(true);
    expect(cornerLabel(k)).toBe("Done");
  });

  it("is reached by pressing Print…, with nothing read of what happened there", () => {
    const k = { ...none, printed: true };
    expect(keptSomehow(k)).toBe(true);
    expect(cornerLabel(k)).toBe("Done");
  });

  it("is reached by *I have written it down*, with no second dialog", () => {
    const k = { ...none, wroteDown: true };
    expect(keptSomehow(k)).toBe(true);
    expect(cornerLabel(k)).toBe("Done");
  });

  it("is not reached by a path the shell handed back empty", () => {
    expect(keptSomehow({ ...none, savedTo: "   " })).toBe(false);
  });
});

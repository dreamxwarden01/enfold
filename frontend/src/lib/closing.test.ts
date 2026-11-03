import { describe, expect, it } from "vitest";
import { askedLine, asksOnClose, closeActionLabel, closeChoices, follow, opsNow } from "./closing";
import type { AskedOp } from "./closing";
import type { BusyOp } from "./ops";

// The Settings row of APP.md §2.4 (ruled 2026-09-13): *When the window
// closes: Ask each time · Keep running in the tray · Quit*. The old
// destroy/hide labels are gone with the setting they belonged to.
describe("the close action's row", () => {
  it("offers the three choices, asking first", () => {
    expect(closeChoices).toEqual([
      ["ask", "Ask each time"],
      ["tray", "Keep running in the tray"],
      ["quit", "Quit"],
    ]);
  });

  it("names each one the way the row and the save bar say it", () => {
    expect(closeActionLabel("ask")).toBe("Ask each time");
    expect(closeActionLabel("tray")).toBe("Keep running in the tray");
    expect(closeActionLabel("quit")).toBe("Quit");
  });

  it("reads anything else as the default, as the core reads the file", () => {
    expect(closeActionLabel("destroy")).toBe("Ask each time");
    expect(closeActionLabel("hide")).toBe("Ask each time");
    expect(closeActionLabel("")).toBe("Ask each time");
    expect(closeActionLabel(undefined)).toBe("Ask each time");
  });

  it("says whether a close asks at all", () => {
    expect(asksOnClose("ask")).toBe(true);
    expect(asksOnClose("tray")).toBe(false);
    expect(asksOnClose("quit")).toBe(false);
    // Whatever is not one of the two answers asks: the close button never
    // means "to the tray" until the user has said so.
    expect(asksOnClose("destroy")).toBe(true);
    expect(asksOnClose(undefined)).toBe(true);
  });
});

// The two questions while an operation runs (APP.md §2.4; ruled 2026-11-03,
// the user's): what they name, how they follow it, and the sentence.
describe("what the close and quit questions name", () => {
  const add = (x: Partial<BusyOp> = {}): BusyOp => ({ id: "op-add", kind: "add", archiveId: "a1", items: 20, startedAt: 100, finished: false, ...x });
  const reclaim = (x: Partial<BusyOp> = {}): BusyOp => ({ id: "op-rc", kind: "reclaim", archiveId: "a1", items: 0, startedAt: 120, finished: false, ...x });
  const extract = (x: Partial<BusyOp> = {}): BusyOp => ({ id: "op-ex", kind: "extract", archiveId: "b2", items: 13, startedAt: 110, finished: false, ...x });
  const names = new Map([
    ["a1", "Photos 2024"],
    ["b2", "Family videos"],
  ]);
  const ids = (a: readonly AskedOp[]) => a.map((x) => `${x.op.id}:${x.seen ? "seen" : "unseen"}:${x.ended ? "ended" : "live"}`);

  it("knows the page's operations and the adds it started that have said nothing yet", () => {
    const now = opsNow({ "op-add": add(), "op-old": add({ id: "op-old", finished: true }) }, { a1: ["op-add", "op-new"], b2: ["op-b"] });
    expect(now.map((o) => o.id)).toEqual(["op-add", "op-old", "op-new", "op-b"]);
    expect(now[2]).toEqual({ id: "op-new", kind: "add", archiveId: "a1", items: 0, startedAt: 0 });
  });

  it("names what runs as the question opens, and nothing finished", () => {
    const a = follow([], [add(), extract({ finished: true })]);
    expect(ids(a)).toEqual(["op-add:seen:live"]);
    expect(askedLine(a, names, true)).toBe("Adding 20 files to Photos 2024 is still running. Quit stops it; in the tray it goes on.");
    expect(askedLine(a, names, false)).toBe("Adding 20 files to Photos 2024 is still running. Quit stops it.");
  });

  it("names the latest and counts the rest", () => {
    const a = follow([], [add(), extract()]);
    expect(askedLine(a, names, true)).toBe("Extracting 13 files from Family videos and 1 other operation are still running. Quit stops them; in the tray they go on.");
    const b = follow([], [add(), extract(), reclaim()]);
    expect(askedLine(b, names, false)).toBe("Reclaiming space in Photos 2024 and 2 other operations are still running. Quit stops them.");
  });

  it("says nothing when nothing was named", () => {
    expect(askedLine(follow([], []), names, true)).toBe("");
    expect(askedLine([], names, false)).toBe("");
  });

  it("follows what it named to its end, and the question then says it has finished", () => {
    let a = follow([], [add()]);
    a = follow(a, [add({ finished: true })]);
    expect(ids(a)).toEqual(["op-add:seen:ended"]);
    expect(askedLine(a, names, false)).toBe("Adding 20 files to Photos 2024 has finished.");
    expect(askedLine(a, names, true)).toBe("Adding 20 files to Photos 2024 has finished.");
    // Forgotten after its end: still ended, still named.
    a = follow(a, []);
    expect(askedLine(a, names, false)).toBe("Adding 20 files to Photos 2024 has finished.");
  });

  it("takes one seen and then gone as ended, its last count kept", () => {
    let a = follow([], [add({ items: 0 })]);
    a = follow(a, [add()]);
    a = follow(a, []);
    expect(ids(a)).toEqual(["op-add:seen:ended"]);
    expect(askedLine(a, names, false)).toBe("Adding 20 files to Photos 2024 has finished.");
  });

  it("names what the shell named before the page has heard of it, and follows it once it has", () => {
    // A window made again for the question: its first status not in yet.
    let a = follow([], [], [add()]);
    expect(ids(a)).toEqual(["op-add:unseen:live"]);
    expect(askedLine(a, names, false)).toBe("Adding 20 files to Photos 2024 is still running. Quit stops it.");
    // Unseen and not held is not ended: the page has not heard of it yet.
    a = follow(a, []);
    expect(ids(a)).toEqual(["op-add:unseen:live"]);
    a = follow(a, [add({ finished: true })]);
    expect(askedLine(a, names, false)).toBe("Adding 20 files to Photos 2024 has finished.");
  });

  it("takes the shell's word that one has already finished", () => {
    const a = follow([], [], [add({ finished: true })]);
    expect(askedLine(a, names, false)).toBe("Adding 20 files to Photos 2024 has finished.");
  });

  it("counts one begun while the question stands, which quitting would stop too", () => {
    let a = follow([], [add()]);
    a = follow(a, [add({ finished: true }), reclaim()]);
    expect(ids(a)).toEqual(["op-add:seen:ended", "op-rc:seen:live"]);
    expect(askedLine(a, names, false)).toBe("Reclaiming space in Photos 2024 is still running. Quit stops it.");
    a = follow(a, [reclaim({ finished: true })]);
    expect(askedLine(a, names, true)).toBe("Reclaiming space in Photos 2024 and 1 other operation have finished.");
  });

  it("says an add whose count is not planned yet as adding files, never a bare verb", () => {
    const a = follow([], opsNow({}, { a1: ["op-new"] }));
    expect(askedLine(a, names, true)).toBe("Adding files to Photos 2024 is still running. Quit stops it; in the tray it goes on.");
  });

  it("gives the list back unchanged when nothing moved", () => {
    const now = [add()];
    const a = follow([], now);
    expect(follow(a, now)).toBe(a);
    expect(follow(a, now, [add()])).toBe(a);
  });
});

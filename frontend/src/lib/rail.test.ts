import { describe, expect, it } from "vitest";
import { railChildren, railCurrent, rowBusy, vaultButtonLabel, vaultHead } from "./rail";
import type { BusyOp } from "./ops";
import { railCopy } from "./strings";

// The rail (APP.md §6, Rail; directions 3, 6 and 10, ruled 2026-10-18):
// the open archive is a row of its own, the vault head says the lock
// header's word, and the compact padlock names what a press does.
describe("the rail's archive rows", () => {
  it("draws one row per archive the status names, in its order", () => {
    const opened = [
      { id: "b", name: "Family videos" },
      { id: "a", name: "Photos 2024" },
    ];
    expect(railChildren("a", "Photos 2024", opened)).toEqual(opened);
  });

  it("draws the page's archive before a status names it, by the page's heading", () => {
    expect(railChildren("a", "Photos 2024", [])).toEqual([{ id: "a", name: "Photos 2024" }]);
    expect(railChildren("a", "Photos 2024", undefined)).toEqual([{ id: "a", name: "Photos 2024" }]);
  });

  it("draws an archive the status names with no page holding it — after a new window, or while it drains", () => {
    expect(railChildren(null, "", [{ id: "b", name: "Family videos" }])).toEqual([{ id: "b", name: "Family videos" }]);
  });

  it("draws nothing when nothing is open", () => {
    expect(railChildren(null, "", [])).toEqual([]);
    expect(railChildren(null, "", null)).toEqual([]);
  });
});

describe("the rail's current item", () => {
  it("is Archives while the list shows, and the archive's own row while its page does", () => {
    expect(railCurrent("archives", "a")).toBe("archives");
    expect(railCurrent("archive", "a")).toBe("archive:a");
  });

  it("is the Vault and Settings items on theirs, and nothing on the lock scene", () => {
    expect(railCurrent("keys", "a")).toBe("keys");
    expect(railCurrent("settings", null)).toBe("settings");
    expect(railCurrent("lock", "a")).toBe("");
  });
});

describe("the vault head", () => {
  it("names the vault over the lock header's word for its state", () => {
    expect(vaultHead({ state: "unlocked", displayName: "Personal vault" })).toEqual({ name: "Personal vault", word: "Unlocked", unlocked: true, locked: false });
    expect(vaultHead({ state: "locked", displayName: "Personal vault" }).word).toBe("Locked");
    expect(vaultHead({ state: "locked", displayName: "Personal vault", setupNeeded: true }).word).toBe("Needs setting up");
    expect(vaultHead({ state: "busy", displayName: "Personal vault" }).word).toBe("Open elsewhere");
  });

  it("names no vault where there is none", () => {
    expect(vaultHead({ state: "none", displayName: "" })).toEqual({ name: "Vault", word: "No vault yet", unlocked: false, locked: false });
    // The name Rename… gave wins over the status's, for the moment before
    // the status follows it; with no vault there is none.
    expect(vaultHead({ state: "unlocked", displayName: "Personal vault" }, "Home vault").name).toBe("Home vault");
    expect(vaultHead({ state: "unlocked", displayName: "Personal vault" }, "").name).toBe("Personal vault");
    expect(vaultHead({ state: "none", displayName: "" }, "Home vault").name).toBe("Vault");
  });

  it("gives the compact padlock the foot's own act", () => {
    expect(vaultButtonLabel(vaultHead({ state: "unlocked", displayName: "Personal vault" }))).toBe("Personal vault · Unlocked · Lock now");
    expect(vaultButtonLabel(vaultHead({ state: "locked", displayName: "Personal vault" }))).toBe("Personal vault · Locked · Unlock");
    expect(vaultButtonLabel(vaultHead({ state: "none" }))).toBe("Vault · No vault yet");
  });
});

// An open archive's row while an operation runs on it (APP.md §6, Rail;
// ruled 2026-11-03, the user's): the rail's spinner in place of its ×, with
// no percentage (ruled 2026-11-04), and the operation in the row's tooltip
// and name.
describe("an open archive's row while it is busy", () => {
  const op = (x: Partial<BusyOp> = {}): BusyOp => ({ id: "op1", kind: "add", archiveId: "a1", items: 20, startedAt: 100, finished: false, ...x });

  it("is idle with nothing running on its archive", () => {
    expect(rowBusy([], [], "a1")).toBeNull();
    expect(rowBusy([op({ finished: true })], [], "a1")).toBeNull();
    expect(rowBusy([op({ archiveId: "b2" })], [], "a1")).toBeNull();
  });

  it("says the operation, and the add's files before its count", () => {
    expect(rowBusy([op()], [], "a1")).toEqual({ what: "Adding 20 files" });
    expect(rowBusy([op({ items: 0 })], [], "a1")).toEqual({ what: "Adding files" });
    // An add the page started that has said nothing yet (the starting ids).
    expect(rowBusy([], ["op9"], "a1")).toEqual({ what: "Adding files" });
    expect(rowBusy([op({ kind: "reclaim", items: 0 })], [], "a1")).toEqual({ what: "Reclaiming space" });
  });

  it("names the latest begun on its archive, of any kind, but a drag out still in its hover", () => {
    const ops = [op({ id: "a", startedAt: 10 }), op({ id: "b", kind: "extract", items: 13, startedAt: 12 }), op({ id: "c", archiveId: "b2", startedAt: 20 })];
    expect(rowBusy(ops, [], "a1")).toEqual({ what: "Extracting 13 files" });
    expect(rowBusy([op({ kind: "dragout", phase: "dragging", dragItems: 2 })], [], "a1")).toBeNull();
    expect(rowBusy([op({ kind: "dragout", phase: "preparing", dragItems: 2 })], [], "a1")).toEqual({ what: "Extracting 2 items" });
  });

  it("says the archive and the operation in the row's name", () => {
    expect(railCopy.busyRow("Photos 2024", "Adding 20 files")).toBe("Photos 2024 · Adding 20 files");
  });
});

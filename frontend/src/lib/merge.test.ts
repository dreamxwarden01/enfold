import { describe, expect, it } from "vitest";
import type { IncomingRecord } from "./api";
import { actionLabel, actionNote, actionOf, defaultTicks, differLine, differText, summarise, tickable, tickedIds } from "./merge";

function row(over: Partial<IncomingRecord>): IncomingRecord {
  return { archiveId: "a1", name: "Photos 2024", description: "", createdAt: 0, versions: 1, action: "add", forgottenAt: 0, differs: null, ticked: true, ...over } as IncomingRecord;
}

describe("the four row kinds", () => {
  it("names each action the way the checklist reads (APP.md §13)", () => {
    expect(actionLabel(row({ action: "add" }))).toBe("new");
    expect(actionLabel(row({ action: "version" }))).toBe("updates");
    expect(actionLabel(row({ action: "forgotten" }))).toBe("forgotten here");
    expect(actionLabel(row({ action: "skip" }))).toBe("already here");
  });

  it("reads an action this build does not know as a skip, which changes nothing", () => {
    expect(actionOf(row({ action: "something_new" }))).toBe("skip");
    expect(tickable(row({ action: "something_new" }))).toBe(false);
  });

  it("counts the versions a new record brings", () => {
    expect(actionNote(row({ action: "add", versions: 1 }))).toMatch(/one key version/);
    expect(actionNote(row({ action: "add", versions: 3 }))).toMatch(/3 key versions/);
    expect(actionNote(row({ action: "forgotten" }))).toMatch(/restores/);
  });
});

describe("the default ticks", () => {
  it("ticks what the core ticked, and never a row the merge would not change", () => {
    const rows = [
      row({ archiveId: "a1", action: "add", ticked: true }),
      row({ archiveId: "a2", action: "version", ticked: true }),
      row({ archiveId: "a3", action: "skip", ticked: false }),
      row({ archiveId: "a4", action: "forgotten", ticked: false }),
    ];
    expect(defaultTicks(rows)).toEqual({ a1: true, a2: true, a3: false, a4: false });
  });

  it("never ticks a skip even if the core said so — nothing would be written", () => {
    expect(defaultTicks([row({ archiveId: "a3", action: "skip", ticked: true })])).toEqual({ a3: false });
  });

  it("restoring a record forgotten here is an explicit tick", () => {
    const rows = [row({ archiveId: "a4", action: "forgotten", ticked: false })];
    expect(defaultTicks(rows).a4).toBe(false);
    expect(tickable(rows[0])).toBe(true); // it can be ticked, just not by default
  });
});

describe("what is sent to MergeRecords", () => {
  const rows = [
    row({ archiveId: "a1", action: "add" }),
    row({ archiveId: "a2", action: "version" }),
    row({ archiveId: "a3", action: "skip" }),
    row({ archiveId: "a4", action: "forgotten" }),
  ];

  it("sends the ticked ids in the order shown, and never an unticked or untickable one", () => {
    expect(tickedIds(rows, { a1: true, a2: false, a3: true, a4: true })).toEqual(["a1", "a4"]);
    expect(tickedIds(rows, {})).toEqual([]);
  });

  it("summarises what the write will do", () => {
    expect(summarise(rows, { a1: true, a2: true, a3: true, a4: true })).toEqual({ added: 1, updated: 1, restored: 1, total: 3 });
    expect(summarise(rows, { a3: true })).toEqual({ added: 0, updated: 0, restored: 0, total: 0 });
  });
});

describe("what differed", () => {
  it("says the other vault's value and that this vault's is kept", () => {
    expect(differText({ field: "name", theirs: "Photos 2023" })).toBe("named “Photos 2023” there");
    expect(differText({ field: "description", theirs: "Iceland" })).toBe("described “Iceland” there");
    expect(differText({ field: "description", theirs: "" })).toBe("no description there");
    expect(differText({ field: "policy", theirs: "hidden" })).toBe("policy there: hidden");
    expect(differText({ field: "kid", theirs: "x" })).toBe("kid there: x");
  });

  it("has nothing to say when the two agree", () => {
    expect(differLine(null)).toBe("");
    expect(differLine([])).toBe("");
    expect(differLine([{ field: "name", theirs: "Photos 2023" }])).toMatch(/this vault's value is kept$/);
    expect(differLine([{ field: "name", theirs: "A" }, { field: "policy", theirs: "hidden" }])).toContain("; ");
  });
});

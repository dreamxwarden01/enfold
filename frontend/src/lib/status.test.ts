import { describe, expect, it } from "vitest";
import type { ArchiveStat } from "./api";
import { freeSpaceFloor, freeWorthShowing, statusNote } from "./status";

function stat(over: Partial<ArchiveStat> = {}): ArchiveStat {
  return {
    seq: 1,
    id: "a".repeat(32),
    name: "Photos 2024",
    size: 1_610_612_736,
    files: 1,
    records: 1,
    freeSpace: 0,
    keyVersion: 1,
    lastSavedAt: 0,
    state: "open",
    receiptOwed: false,
    copyMismatch: false,
    ...over,
  } as ArchiveStat;
}

// The archive's status strip (APP.md §3, §6).
describe("the status strip", () => {
  it("is singular at one file (ruled 2026-09-09: \"1 files\" was on the line)", () => {
    expect(statusNote(stat({ files: 1 }))).toBe("1 file · 1.5 GB · key v1");
  });

  it("counts and groups the rest", () => {
    expect(statusNote(stat({ files: 12406 }))).toBe("12,406 files · 1.5 GB · key v1");
    expect(statusNote(stat({ files: 0 }))).toBe("0 files · 1.5 GB · key v1");
  });

  it("names the last save only once there has been one", () => {
    expect(statusNote(stat({ lastSavedAt: 0 }))).not.toMatch(/last saved/);
    expect(statusNote(stat({ lastSavedAt: 1_757_000_000 }))).toMatch(/· last saved \d{4}-\d{2}-\d{2} \d{2}:\d{2}$/);
  });

  it("is empty with no archive open", () => {
    expect(statusNote(null)).toBe("");
    expect(statusNote(undefined)).toBe("");
  });
});

// The free space is worth knowing from the floor of the core's own rule
// up: the core moves live data down only for a tail worth 64 MiB and a
// quarter of the move, so a figure over the floor is what it left where
// it lay, and the only thing that says why the file is bigger than the
// files inside it (APP.md §2.3, FORMAT.md R40).
describe("the free space on the strip", () => {
  it("starts at 64 MiB", () => {
    expect(freeSpaceFloor).toBe(64 * 1024 * 1024);
    expect(freeWorthShowing(freeSpaceFloor)).toBe(true);
    expect(freeWorthShowing(freeSpaceFloor - 1)).toBe(false);
  });

  it("says nothing below it, whatever the archive", () => {
    expect(freeWorthShowing(0)).toBe(false);
    expect(freeWorthShowing(undefined)).toBe(false);
    expect(statusNote(stat({ freeSpace: 12_000_000 }))).not.toMatch(/free/);
  });

  it("is appended to the line above it", () => {
    expect(statusNote(stat({ freeSpace: 3_100_000_000 }))).toBe("1 file · 1.5 GB · key v1 · 2.9 GB free");
  });

  it("comes after the last save, so the figures the file has always had come first", () => {
    expect(statusNote(stat({ files: 2, lastSavedAt: 1_757_000_000, freeSpace: 3_100_000_000 }))).toMatch(
      /^2 files · 1\.5 GB · key v1 · last saved .* · 2\.9 GB free$/,
    );
  });
});

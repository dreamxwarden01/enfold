import { describe, expect, it } from "vitest";
import { RETENTION_SECONDS, purgeAfter } from "./retention";

describe("purgeAfter", () => {
  it("is thirty days after the record was forgotten (FORMAT.md §18.2)", () => {
    expect(RETENTION_SECONDS).toBe(2_592_000);
    expect(RETENTION_SECONDS).toBe(30 * 24 * 60 * 60);
    expect(purgeAfter(1_757_000_000)).toBe(1_757_000_000 + RETENTION_SECONDS);
  });

  it("has no date for a record that is not forgotten", () => {
    expect(purgeAfter(0)).toBe(0);
  });

  it("has no date for a stamp that is not a time, rather than one in 1970", () => {
    expect(purgeAfter(-1)).toBe(0);
    expect(purgeAfter(NaN)).toBe(0);
    expect(purgeAfter(Infinity)).toBe(0);
  });
});

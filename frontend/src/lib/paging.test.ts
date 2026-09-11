import { describe, expect, it } from "vitest";
import type { FileRow, Page } from "./api";
import { PAGE_LIMIT, applyReply, hasMore, liveIds, nearEnd, nextOffset } from "./paging";
import type { Listing } from "./paging";

// The list's paging (APP.md §2.4, ruled 2026-09-10): pages of up to 1 000
// as the list scrolls; a reply is applied when it is not older than the
// pages held, and one newer than them restarts the listing from zero.

function row(id: string): FileRow {
  return { id, parentId: "0".repeat(32), isDir: false, name: id, path: id, size: 1, storage: "raw", savedPercent: 0, modifiedAt: 1 };
}

function reply(seq: number, ids: string[], total: number): Page {
  return { seq, rows: ids.map(row), total, crumbs: [{ id: "0".repeat(32), name: "A" }] };
}

const idsOf = (l: Listing) => l.rows.map((r) => r.id);

describe("the page size", () => {
  it("is the core's ceiling, not beyond it", () => {
    expect(PAGE_LIMIT).toBe(1000);
  });
});

describe("the first page", () => {
  it("becomes the listing", () => {
    const a = applyReply(null, reply(3, ["a", "b"], 5), 0);
    expect(a.kind).toBe("applied");
    if (a.kind !== "applied") return;
    expect(idsOf(a.listing)).toEqual(["a", "b"]);
    expect(a.listing.seq).toBe(3);
    expect(a.listing.total).toBe(5);
    expect(hasMore(a.listing)).toBe(true);
    expect(nextOffset(a.listing)).toBe(2);
  });
});

describe("the next page at the same Seq", () => {
  const held: Listing = { seq: 3, rows: ["a", "b"].map(row), total: 4, crumbs: [] };

  it("is appended at its offset", () => {
    const a = applyReply(held, reply(3, ["c", "d"], 4), 2);
    if (a.kind !== "applied") throw new Error(a.kind);
    expect(idsOf(a.listing)).toEqual(["a", "b", "c", "d"]);
    expect(hasMore(a.listing)).toBe(false);
  });

  it("re-read at the first page keeps the pages the scroll had loaded", () => {
    const four = { ...held, rows: ["a", "b", "c", "d"].map(row) };
    const a = applyReply(four, reply(3, ["a", "b2"], 4), 0, 2);
    if (a.kind !== "applied") throw new Error(a.kind);
    expect(idsOf(a.listing)).toEqual(["a", "b2", "c", "d"]);
  });

  it("short of the limit is the last page, and nothing past it stands", () => {
    const a = applyReply({ ...held, rows: ["a", "b", "c", "d"].map(row) }, reply(3, ["c2"], 3), 2);
    if (a.kind !== "applied") throw new Error(a.kind);
    expect(idsOf(a.listing)).toEqual(["a", "b", "c2"]);
    expect(a.listing.total).toBe(3);
  });
});

describe("a reply at another Seq", () => {
  const held: Listing = { seq: 3, rows: ["a", "b"].map(row), total: 4, crumbs: [] };

  it("newer and from offset zero restarts the listing with it", () => {
    const a = applyReply(held, reply(4, ["b", "x"], 2), 0);
    if (a.kind !== "applied") throw new Error(a.kind);
    expect(idsOf(a.listing)).toEqual(["b", "x"]);
    expect(a.listing.seq).toBe(4);
  });

  it("newer and from a later offset asks for a restart, since rows may have moved", () => {
    expect(applyReply(held, reply(4, ["c", "d"], 4), 2)).toEqual({ kind: "restart" });
  });

  it("older is dropped", () => {
    expect(applyReply(held, reply(2, ["c"], 4), 2)).toEqual({ kind: "stale" });
    expect(applyReply(held, reply(2, ["z"], 1), 0)).toEqual({ kind: "stale" });
  });
});

describe("the scroll's end", () => {
  it("is near within a few rows of the bottom, and a list that cannot scroll is at it", () => {
    expect(nearEnd(0, 400, 400, 36)).toBe(true);
    expect(nearEnd(0, 400, 380, 36)).toBe(true);
    expect(nearEnd(0, 400, 4000, 36)).toBe(false);
    expect(nearEnd(3400, 400, 4000, 36)).toBe(true);
    expect(nearEnd(3000, 400, 4000, 36)).toBe(false);
  });
});

describe("what the selection is pruned against", () => {
  it("is every row held once the whole folder is on hand, and nothing otherwise", () => {
    expect(liveIds({ seq: 1, rows: ["a", "b"].map(row), total: 2, crumbs: [] })).toEqual(new Set(["a", "b"]));
    expect(liveIds({ seq: 1, rows: ["a", "b"].map(row), total: 3, crumbs: [] })).toBeNull();
    expect(liveIds(null)).toBeNull();
  });
});

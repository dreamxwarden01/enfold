import { describe, expect, it } from "vitest";
import { DEFAULT_SORT, arrowOf, clickHeader, firstDirection, sortString } from "./sort";

// The list's sort (APP.md §3, §6, ruled 2026-09-10): a header click sorts
// by its column — Name and Type ascending on the first click, Modified
// descending, Size ascending, the second click the other way — and the
// page keeps the Name column's own direction as the tie-break while
// another column sorts, sending both as one string.

describe("an archive opens on name", () => {
  it("ascending, sent as the one key", () => {
    expect(DEFAULT_SORT).toEqual({ key: "name", desc: false, nameDesc: false });
    expect(sortString(DEFAULT_SORT)).toBe("name");
  });
});

describe("the first click's direction", () => {
  it("is ascending for Name, Size and Type and descending for Modified", () => {
    expect(firstDirection("name")).toBe(false);
    expect(firstDirection("size")).toBe(false);
    expect(firstDirection("type")).toBe(false);
    expect(firstDirection("modified")).toBe(true);
  });

  it("is taken on the first click, whatever the column before was doing", () => {
    expect(clickHeader(DEFAULT_SORT, "modified")).toEqual({ key: "modified", desc: true, nameDesc: false });
    expect(clickHeader(DEFAULT_SORT, "size")).toEqual({ key: "size", desc: false, nameDesc: false });
    expect(clickHeader(DEFAULT_SORT, "type")).toEqual({ key: "type", desc: false, nameDesc: false });
    // A column already descending elsewhere does not carry its direction over.
    expect(clickHeader({ key: "modified", desc: true, nameDesc: false }, "size").desc).toBe(false);
  });
});

describe("the second click", () => {
  it("turns the same column the other way", () => {
    const m = clickHeader(DEFAULT_SORT, "modified");
    expect(clickHeader(m, "modified")).toEqual({ key: "modified", desc: false, nameDesc: false });
    const s = clickHeader(DEFAULT_SORT, "size");
    expect(clickHeader(s, "size").desc).toBe(true);
    expect(clickHeader(clickHeader(s, "size"), "size").desc).toBe(false);
  });

  it("on Name turns the tie-break with it", () => {
    const n = clickHeader(DEFAULT_SORT, "name");
    expect(n).toEqual({ key: "name", desc: true, nameDesc: true });
    expect(clickHeader(n, "name")).toEqual(DEFAULT_SORT);
  });
});

describe("the tie-break", () => {
  it("is the Name column's own direction, kept while another column sorts", () => {
    const nameDown = clickHeader(DEFAULT_SORT, "name");
    const bySize = clickHeader(nameDown, "size");
    expect(bySize).toEqual({ key: "size", desc: false, nameDesc: true });
    expect(sortString(bySize)).toBe("size,-name");
    expect(sortString(clickHeader(bySize, "size"))).toBe("-size,-name");
  });

  it("is where Name comes back to when it is clicked again", () => {
    const bySize = clickHeader(clickHeader(DEFAULT_SORT, "name"), "size");
    expect(clickHeader(bySize, "name")).toEqual({ key: "name", desc: true, nameDesc: true });
  });
});

describe("the string Page and Children take", () => {
  it("is one signed key for Name and two for every other column", () => {
    expect(sortString({ key: "name", desc: true, nameDesc: true })).toBe("-name");
    expect(sortString({ key: "modified", desc: true, nameDesc: false })).toBe("-modified,name");
    expect(sortString({ key: "type", desc: false, nameDesc: false })).toBe("type,name");
  });
});

describe("the arrow", () => {
  it("sits in the sorting column's cell and nowhere else", () => {
    const m = clickHeader(DEFAULT_SORT, "modified");
    expect(arrowOf(m, "modified")).toBe("desc");
    expect(arrowOf(m, "name")).toBeNull();
    expect(arrowOf(DEFAULT_SORT, "name")).toBe("asc");
  });
});

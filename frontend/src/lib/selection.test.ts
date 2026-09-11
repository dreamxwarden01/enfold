import { describe, expect, it } from "vitest";
import { clickHeader, clickRow, emptySelection, headerTicked, selectAll, survive, toggleRow } from "./selection";

// The list's selection (APP.md §6, ruled 2026-09-10): ticks are the
// selection; a plain click selects one and sets the anchor, Ctrl toggles,
// Shift ranges from the anchor in the current row order, and with no
// anchor Shift selects the clicked row alone.

const order = ["a", "b", "c", "d", "e"];
const ids = (s: { ids: ReadonlySet<string> }) => [...s.ids].sort();

describe("a plain click", () => {
  it("selects that row alone and makes it the anchor", () => {
    const s = clickRow(clickRow(emptySelection(), order, "b"), order, "d");
    expect(ids(s)).toEqual(["d"]);
    expect(s.anchor).toBe("d");
  });
});

describe("Ctrl+click", () => {
  it("toggles that row and makes it the anchor", () => {
    let s = clickRow(emptySelection(), order, "b");
    s = clickRow(s, order, "d", { ctrl: true });
    expect(ids(s)).toEqual(["b", "d"]);
    expect(s.anchor).toBe("d");
    s = clickRow(s, order, "b", { ctrl: true });
    expect(ids(s)).toEqual(["d"]);
    expect(s.anchor).toBe("b");
  });

  it("that empties the selection drops the anchor, so the next Shift+click selects the clicked row alone", () => {
    let s = clickRow(emptySelection(), order, "b");
    s = clickRow(s, order, "b", { ctrl: true });
    expect(ids(s)).toEqual([]);
    expect(s.anchor).toBeNull();
    s = clickRow(s, order, "d", { shift: true });
    expect(ids(s)).toEqual(["d"]);
  });

  it("wins over Shift when both are held", () => {
    const s = clickRow(clickRow(emptySelection(), order, "a"), order, "c", { ctrl: true, shift: true });
    expect(ids(s)).toEqual(["a", "c"]);
  });
});

describe("Shift+click", () => {
  it("ranges from the anchor in the current row order, either way round", () => {
    const from = clickRow(emptySelection(), order, "b");
    expect(ids(clickRow(from, order, "d", { shift: true }))).toEqual(["b", "c", "d"]);
    expect(ids(clickRow(from, order, "a", { shift: true }))).toEqual(["a", "b"]);
  });

  it("keeps the anchor, so a second Shift+click ranges from the same row", () => {
    let s = clickRow(emptySelection(), order, "b");
    s = clickRow(s, order, "e", { shift: true });
    s = clickRow(s, order, "c", { shift: true });
    expect(ids(s)).toEqual(["b", "c"]);
    expect(s.anchor).toBe("b");
  });

  it("with no anchor selects the clicked row alone and makes it the anchor", () => {
    const s = clickRow(emptySelection(), order, "c", { shift: true });
    expect(ids(s)).toEqual(["c"]);
    expect(s.anchor).toBe("c");
  });

  it("after a blank click ranges from nothing — the stale anchor fault (recon finding 4)", () => {
    let s = clickRow(emptySelection(), order, "b");
    s = emptySelection(); // the blank click clears both
    s = clickRow(s, order, "d", { shift: true });
    expect(ids(s)).toEqual(["d"]);
  });

  it("with an anchor no longer on screen selects the clicked row alone", () => {
    const s = clickRow(clickRow(emptySelection(), order, "b"), ["c", "d"], "d", { shift: true });
    expect(ids(s)).toEqual(["d"]);
    expect(s.anchor).toBe("d");
  });
});

describe("a row's checkbox", () => {
  it("toggles that row alone and makes it the anchor", () => {
    let s = clickRow(emptySelection(), order, "a");
    s = toggleRow(s, "c");
    expect(ids(s)).toEqual(["a", "c"]);
    expect(s.anchor).toBe("c");
    s = toggleRow(s, "a");
    expect(ids(s)).toEqual(["c"]);
    expect(s.anchor).toBe("a");
  });

  it("unticking the last ticked row drops the anchor with it", () => {
    let s = toggleRow(emptySelection(), "b");
    s = toggleRow(s, "b");
    expect(ids(s)).toEqual([]);
    expect(s.anchor).toBeNull();
    s = clickRow(s, order, "d", { shift: true });
    expect(ids(s)).toEqual(["d"]);
    expect(s.anchor).toBe("d");
  });
});

describe("the header's checkbox", () => {
  it("is ticked exactly when the selection holds every one of Total ids", () => {
    expect(headerTicked(selectAll(emptySelection(), order), 5)).toBe(true);
    expect(headerTicked(clickRow(emptySelection(), order, "a"), 5)).toBe(false);
    expect(headerTicked(selectAll(emptySelection(), order), 6)).toBe(false); // a row not loaded yet
  });

  it("is never ticked for an empty folder", () => {
    expect(headerTicked(emptySelection(), 0)).toBe(false);
  });

  it("ticks the whole folder, loaded or not, and clears it when ticked", () => {
    const folder = [...order, "f", "g"];
    const all = clickHeader(clickRow(emptySelection(), order, "a"), folder.length, folder);
    expect(ids(all)).toEqual(folder);
    expect(all.anchor).toBe("a");
    expect(headerTicked(all, folder.length)).toBe(true);
    expect(clickHeader(all, folder.length, folder)).toEqual(emptySelection());
  });
});

describe("a newer listing", () => {
  it("keeps the ids that still exist and the anchor its row if it does", () => {
    let s = selectAll(clickRow(emptySelection(), order, "c"), order);
    s = survive(s, new Set(["a", "c", "x"]));
    expect(ids(s)).toEqual(["a", "c"]);
    expect(s.anchor).toBe("c");
    const gone = survive(s, new Set(["a", "x"]));
    expect(ids(gone)).toEqual(["a"]);
    expect(gone.anchor).toBeNull();
  });

  it("does not select rows a commit added", () => {
    const s = survive(selectAll(emptySelection(), order), new Set([...order, "new"]));
    expect(s.ids.has("new")).toBe(false);
    expect(headerTicked(s, 6)).toBe(false);
  });

  it("returns the same state when nothing changed", () => {
    const s = clickRow(emptySelection(), order, "b");
    expect(survive(s, new Set(order))).toBe(s);
  });
});

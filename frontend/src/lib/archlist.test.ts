import { describe, expect, it } from "vitest";
import { clickShownHeader, compacting, droppedArchives, fingerprintOf, paneRow, rowRefusal, runningKind, shownTicked } from "./archlist";
import { clickRow, emptySelection, toggleRow } from "./selection";

// The Archives list's ticks (APP.md §6, ruled 2026-09-10): the header
// ticks the rows the filter shows and is ticked exactly when all of them
// are; the details pane shows the row last clicked while it is ticked,
// else the sole ticked row, else nothing.

const shown = ["a", "b", "c"];
const ids = (s: { ids: ReadonlySet<string> }) => [...s.ids].sort();

describe("a toggle that empties the ticks", () => {
  it("drops the anchor here too, so a Shift+click after it takes the clicked row alone", () => {
    let s = toggleRow(emptySelection(), "b");
    s = clickRow(s, shown, "b", { ctrl: true });
    expect(s.anchor).toBeNull();
    expect(ids(clickRow(s, shown, "c", { shift: true }))).toEqual(["c"]);
  });
});

describe("the header's tick", () => {
  it("is ticked exactly when every shown row is", () => {
    let s = clickRow(emptySelection(), shown, "a");
    expect(shownTicked(s, shown)).toBe(false);
    s = clickRow(s, shown, "c", { shift: true });
    expect(shownTicked(s, shown)).toBe(true);
  });
  it("is never ticked over an empty list", () => {
    expect(shownTicked(clickRow(emptySelection(), shown, "a"), [])).toBe(false);
  });
  it("counts the rows the filter shows, not the ticks it hides", () => {
    const s = clickRow(clickRow(emptySelection(), shown, "a"), shown, "c", { shift: true });
    expect(shownTicked(s, ["a", "b"])).toBe(true);
    expect(shownTicked(s, ["a", "d"])).toBe(false);
  });
  it("ticks the shown rows, and clears when they are all ticked", () => {
    let s = clickShownHeader(emptySelection(), shown);
    expect(ids(s)).toEqual(["a", "b", "c"]);
    s = clickShownHeader(s, shown);
    expect(ids(s)).toEqual([]);
  });
  it("ticks the shown rows alone when the filter narrowed the list", () => {
    const s = clickShownHeader(clickRow(emptySelection(), shown, "c"), ["a", "b"]);
    expect(ids(s)).toEqual(["a", "b"]);
  });
});

describe("the details pane's row", () => {
  it("is the row last clicked while it is ticked", () => {
    const s = clickRow(clickRow(emptySelection(), shown, "a"), shown, "c", { shift: true });
    expect(paneRow(s, "c")).toBe("c");
  });
  it("is the sole ticked row when the last click is no longer ticked", () => {
    let s = clickRow(emptySelection(), shown, "a");
    s = toggleRow(s, "b");
    s = toggleRow(s, "b"); // b was clicked last, and is unticked again
    expect(paneRow(s, "b")).toBe("a");
  });
  it("is nothing with several ticked and the last click among none of them", () => {
    let s = clickRow(clickRow(emptySelection(), shown, "a"), shown, "c", { shift: true });
    s = toggleRow(s, "b");
    expect(paneRow(s, "b")).toBe(null);
  });
  it("is nothing with nothing ticked", () => {
    expect(paneRow(emptySelection(), "a")).toBe(null);
    expect(paneRow(emptySelection(), null)).toBe(null);
  });
});

describe("what stops an action on a row", () => {
  const row = { id: "a", forgottenAt: 0 };
  const compact = { kind: "compact", archiveId: "a", finished: false };

  it("is nothing for a plain record", () => {
    expect(rowRefusal(row, [])).toBe("");
    expect(compacting(row, [])).toBe(false);
  });

  it("is the core's own forgotten code for a forgotten record, ahead of a compaction", () => {
    expect(rowRefusal({ ...row, forgottenAt: 1_700_000_000 }, [compact])).toBe("archive.forgotten");
  });

  it("reads a compaction off the handle's state or off a running Compact of that archive", () => {
    expect(rowRefusal({ ...row, state: "compacting" }, [])).toBe("archive.compacting");
    expect(rowRefusal(row, [compact])).toBe("archive.compacting");
  });

  it("names a refresh or a re-key running on the row, and nothing else", () => {
    expect(runningKind(row, [{ kind: "verify", archiveId: "a", finished: false }])).toBe("verify");
    expect(runningKind(row, [{ kind: "rotate", archiveId: "a", finished: false }])).toBe("rotate");
    expect(runningKind(row, [{ kind: "rotate", archiveId: "a", finished: true }])).toBe("");
    expect(runningKind(row, [{ kind: "verify", archiveId: "b", finished: false }])).toBe("");
    expect(runningKind(row, [compact])).toBe("");
  });

  it("ignores a finished Compact, another archive's, and another kind of operation", () => {
    expect(compacting(row, [{ ...compact, finished: true }])).toBe(false);
    expect(compacting(row, [{ ...compact, archiveId: "b" }])).toBe(false);
    expect(compacting(row, [{ ...compact, kind: "verify" }])).toBe(false);
    expect(compacting({ ...row, state: "open" }, [])).toBe(false);
  });
});

describe("an .efd dropped on the Archives page", () => {
  it("keeps the archives, in the drop's order, whatever the extension's case", () => {
    expect(droppedArchives(["C:\\a\\one.efd", "C:\\a\\notes.txt", "D:\\b\\Two.EFD"])).toEqual(["C:\\a\\one.efd", "D:\\b\\Two.EFD"]);
  });

  it("never takes a folder named like an archive, and nothing else at all", () => {
    expect(droppedArchives(["C:\\a\\odd.efd"], [true])).toEqual([]);
    expect(droppedArchives(["C:\\a\\photo.jpg", "C:\\a\\efd"])).toEqual([]);
  });
});

// The pane's Fingerprint fact (direction 9): a hash only Refresh
// fingerprint and Compact take, so a record never refreshed has none,
// and the summary's hashBehind alone cannot say so.
describe("the fingerprint fact", () => {
  const zeros = "0".repeat(64);
  const hash = "ab".repeat(32);
  const row = (hashBehind: number, receiptOwed = false) => ({ hashBehind, receiptOwed });

  it("is not drawn without the record, as while Locked", () => {
    expect(fingerprintOf(row(0), null)).toBeNull();
  });

  it("says none for a record never refreshed, however many saves it has", () => {
    // Create: LastSeq 1, HashAtSeq 0, so hashBehind 1 — not "1 save ago".
    expect(fingerprintOf(row(1), { lastSeq: 1, hashAtSeq: 0, lastCiphertextHash: zeros })).toEqual({ kind: "none", behind: 0 });
    expect(fingerprintOf(row(7), { lastSeq: 7, hashAtSeq: 0, lastCiphertextHash: zeros })).toEqual({ kind: "none", behind: 0 });
    // a record written before the archive ever was: nothing at all
    expect(fingerprintOf(row(0), { lastSeq: 0, hashAtSeq: 0, lastCiphertextHash: "" })).toEqual({ kind: "none", behind: 0 });
  });

  it("matches only when a hash was taken at the last save", () => {
    expect(fingerprintOf(row(0), { lastSeq: 9, hashAtSeq: 9, lastCiphertextHash: hash })).toEqual({ kind: "current", behind: 0 });
  });

  it("counts the saves since a hash was taken", () => {
    expect(fingerprintOf(row(2), { lastSeq: 9, hashAtSeq: 7, lastCiphertextHash: hash })).toEqual({ kind: "behind", behind: 2 });
    expect(fingerprintOf(row(1), { lastSeq: 9, hashAtSeq: 8, lastCiphertextHash: hash })).toEqual({ kind: "behind", behind: 1 });
  });

  it("claims no number while a receipt is owed", () => {
    expect(fingerprintOf(row(0, true), { lastSeq: 9, hashAtSeq: 9, lastCiphertextHash: hash })).toEqual({ kind: "owed", behind: 0 });
    expect(fingerprintOf(row(3, true), { lastSeq: 9, hashAtSeq: 6, lastCiphertextHash: hash })).toEqual({ kind: "owed", behind: 3 });
    // and an owed receipt is still no hash when none was taken
    expect(fingerprintOf(row(4, true), { lastSeq: 4, hashAtSeq: 0, lastCiphertextHash: zeros })).toEqual({ kind: "none", behind: 0 });
  });

  it("believes a summary that proves a hash taken since a stale record was read", () => {
    // record read at seq 5 with no hash; a Refresh fingerprint since
    // leaves hashBehind 0, below that record's LastSeq
    expect(fingerprintOf(row(0), { lastSeq: 5, hashAtSeq: 0, lastCiphertextHash: zeros })).toEqual({ kind: "current", behind: 0 });
    // a save since that refresh: still a taken hash
    expect(fingerprintOf(row(1), { lastSeq: 5, hashAtSeq: 0, lastCiphertextHash: zeros })).toEqual({ kind: "behind", behind: 1 });
  });
});

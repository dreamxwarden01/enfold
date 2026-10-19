import { describe, expect, it } from "vitest";
import { chipKinds, compacting, droppedArchives, fingerprintOf, folderOf, rowRefusal, runningKind, vaultFigures, viewRows } from "./archlist";

// The Archives list (direction 6, phase 4a): one archive selected at a
// time, no ticks; the views over the whole registry, a row's second line,
// the chips after its name, and the vault's figures when nothing is
// selected.

describe("which records a view shows", () => {
  const plain = { id: "a", hidden: false, forgottenAt: 0 };
  const hidden = { id: "b", hidden: true, forgottenAt: 0 };
  const gone = { id: "c", hidden: false, forgottenAt: 1_760_000_000 };
  const both = { id: "d", hidden: true, forgottenAt: 1_760_000_000 };
  const all = [plain, hidden, gone, both];
  const ids = (rows: { id: string }[]) => rows.map((r) => r.id);

  it("lists neither hidden nor forgotten records until Show hidden", () => {
    expect(ids(viewRows(all, "list", false))).toEqual(["a"]);
  });

  it("lists the hidden records under Show hidden and never a forgotten one, hidden or not", () => {
    expect(ids(viewRows(all, "list", true))).toEqual(["a", "b"]);
  });

  it("lists the forgotten records alone in the Forgotten view, hidden or not", () => {
    expect(ids(viewRows(all, "forgotten", false))).toEqual(["c", "d"]);
    expect(ids(viewRows(all, "forgotten", true))).toEqual(["c", "d"]);
  });

  it("hands back a list of its own, never the caller's", () => {
    const out = viewRows(all, "list", true);
    expect(out).not.toBe(all);
  });
});

describe("a row's folder, its second line without a description", () => {
  it("is the path's parent, in the path's own separators", () => {
    expect(folderOf("D:\\Archives\\photos-2024.efd")).toBe("D:\\Archives");
    expect(folderOf("D:/Archives/photos-2024.efd")).toBe("D:/Archives");
    expect(folderOf("/Volumes/Media/family-videos.efd")).toBe("/Volumes/Media");
  });

  it("keeps a drive's root and the file system's root whole", () => {
    expect(folderOf("D:\\photos.efd")).toBe("D:\\");
    expect(folderOf("E:/photos.efd")).toBe("E:/");
    expect(folderOf("/photos.efd")).toBe("/");
  });

  it("names a share's server and folder", () => {
    expect(folderOf("\\\\nas\\backups\\laptop.efd")).toBe("\\\\nas\\backups");
  });

  it("is nothing for a bare name or no path", () => {
    expect(folderOf("photos.efd")).toBe("");
    expect(folderOf("")).toBe("");
  });
});

describe("the chips after a row's name", () => {
  const row = { id: "a", forgottenAt: 0, hidden: false, open: false };
  const op = (kind: string) => ({ kind, archiveId: "a", finished: false });

  it("says nothing of a plain closed record", () => {
    expect(chipKinds(row, [])).toEqual([]);
  });

  it("puts Missing first, then Hidden, Forgotten and what is happening", () => {
    const r = { ...row, note: "archive.file_missing", hidden: true, forgottenAt: 1, open: true };
    expect(chipKinds(r, [])).toEqual(["missing", "hidden", "forgotten", "open"]);
  });

  it("says Missing only from the core's note", () => {
    expect(chipKinds({ ...row, note: "archive.something_else" }, [])).toEqual([]);
  });

  it("leaves Forgotten to the Forgotten view itself", () => {
    expect(chipKinds({ ...row, forgottenAt: 1 }, [], "forgotten")).toEqual([]);
  });

  it("says what runs on the row in place of Open", () => {
    expect(chipKinds({ ...row, open: true }, [op("compact")])).toEqual(["compacting"]);
    expect(chipKinds({ ...row, open: true }, [op("verify")])).toEqual(["refreshing"]);
    expect(chipKinds({ ...row, open: true }, [op("rotate")])).toEqual(["rekeying"]);
    expect(chipKinds({ ...row, state: "compacting" }, [])).toEqual(["compacting"]);
  });

  it("ignores an operation that ended, or one on another archive", () => {
    expect(chipKinds({ ...row, open: true }, [{ kind: "verify", archiveId: "a", finished: true }])).toEqual(["open"]);
    expect(chipKinds(row, [{ kind: "compact", archiveId: "b", finished: false }])).toEqual([]);
  });
});

describe("the vault's figures with nothing selected", () => {
  it("counts every record but the forgotten ones, hidden ones included", () => {
    const all = [
      { storedSize: 100, forgottenAt: 0 },
      { storedSize: 50, forgottenAt: 0 },
      { storedSize: 7, forgottenAt: 1_760_000_000 },
    ];
    expect(vaultFigures(all)).toEqual({ count: 2, size: 150, hidden: 0 });
  });

  it("takes a size it was not told as nothing", () => {
    expect(vaultFigures([{ storedSize: Number.NaN, forgottenAt: 0 }, { storedSize: -1, forgottenAt: 0 }])).toEqual({ count: 2, size: 0, hidden: 0 });
  });

  it("is nothing for an empty vault", () => {
    expect(vaultFigures([])).toEqual({ count: 0, size: 0, hidden: 0 });
  });

  it("counts the hidden ones it counted, apart", () => {
    const all = [
      { storedSize: 10, forgottenAt: 0, hidden: true },
      { storedSize: 20, forgottenAt: 0, hidden: false },
      { storedSize: 30, forgottenAt: 1_760_000_000, hidden: true },
    ];
    expect(vaultFigures(all)).toEqual({ count: 2, size: 30, hidden: 1 });
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

import { describe, expect, it } from "vitest";
import { clockTime, date, ext, fileIcon, hashText, keyId, labelledText, lockLine, nearerDeadline, plural, recoveryWords } from "./format";

// The foot's countdown (APP.md §6, ruled 2026-09-12 after a review found
// the foot reading the idle deadline alone): the absolute cap locks the
// vault regardless of activity, so the nearer of the two is what it says.
describe("nearerDeadline", () => {
  it("is the nearer of the two, whichever that is", () => {
    expect(nearerDeadline(1000, 1200)).toBe(1000);
    expect(nearerDeadline(1200, 1000)).toBe(1000);
    expect(nearerDeadline(1000, 1000)).toBe(1000);
  });

  it("takes the other one when a deadline is none at all — a zero is not 1970", () => {
    expect(nearerDeadline(0, 1200)).toBe(1200);
    expect(nearerDeadline(1200, 0)).toBe(1200);
  });

  it("is none when neither is set, which is what a locked vault reports", () => {
    expect(nearerDeadline(0, 0)).toBe(0);
  });
});

// The quiet countdown (APP.md §6, ruled 2026-10-14): the nearer deadline
// still decides, but the chip says the rule or the clock time and counts
// only in the last minute.
describe("lockLine", () => {
  // 2026-10-14 16:00:00 local, in Unix seconds and in milliseconds.
  const t0 = new Date(2026, 9, 14, 16, 0, 0).getTime() / 1000;
  const now = t0 * 1000;

  it("says the idle rule, never a number, while more than a minute is left", () => {
    expect(lockLine(t0 + 600, t0 + 3600, 10, now)).toEqual({ text: "Locks after 10 min idle", urgent: false });
    // 1:30 left: still the rule, nothing between 2:00 and 1:00 changes.
    expect(lockLine(t0 + 90, t0 + 3600, 10, now)).toEqual({ text: "Locks after 10 min idle", urgent: false });
  });

  it("counts the last minute of the idle deadline, urgent", () => {
    expect(lockLine(t0 + 60, t0 + 3600, 10, now)).toEqual({ text: "Locks in 1:00", urgent: true });
    expect(lockLine(t0 + 5, t0 + 3600, 10, now)).toEqual({ text: "Locks in 0:05", urgent: true });
  });

  it("says when the session ends when the absolute cap is nearer, as a clock time", () => {
    expect(lockLine(t0 + 600, t0 + 300, 10, now)).toEqual({ text: `Session ends ${clockTime(t0 + 300)}`, urgent: false });
    expect(clockTime(t0 + 42 * 60)).toBe("16:42");
    // Equal deadlines are the cap's: no input moves it.
    expect(lockLine(t0 + 600, t0 + 600, 10, now).text).toBe(`Session ends ${clockTime(t0 + 600)}`);
  });

  it("counts the cap's last minute too", () => {
    expect(lockLine(t0 + 600, t0 + 42, 10, now)).toEqual({ text: "Session ends in 0:42", urgent: true });
  });

  it("is the same text from one second to the next until the last minute", () => {
    expect(lockLine(t0 + 600, t0 + 3600, 10, now + 1000).text).toBe(lockLine(t0 + 600, t0 + 3600, 10, now).text);
    expect(lockLine(t0 + 600, t0 + 300, 10, now + 1000).text).toBe(lockLine(t0 + 600, t0 + 300, 10, now).text);
  });

  // The minutes are the status's idleEffectiveMinutes, the core's clamp
  // already applied (ruled 2026-10-17): said as given, with no rule of
  // the page's own between.
  it("says the core's minutes as they are", () => {
    expect(lockLine(t0 + 1500, t0 + 3600, 25, now).text).toBe("Locks after 25 min idle");
  });

  it("names no number while the status names none", () => {
    expect(lockLine(t0 + 600, t0 + 3600, 0, now).text).toBe("Locks when idle");
  });
});

describe("keyId", () => {
  it("groups the first eight hex digits, upper-cased (FORMAT.md §18.4)", () => {
    expect(keyId("3f7a9c21d4e5f60718293a4b5c6d7e8f")).toBe("3F7A-9C21");
    expect(keyId("3F7A9C21D4E5F60718293A4B5C6D7E8F")).toBe("3F7A-9C21");
    expect(keyId("00000000000000000000000000000000")).toBe("0000-0000");
    expect(keyId("deadbeef")).toBe("DEAD-BEEF"); // exactly eight is enough
  });

  it("reads the id and nothing else — the same eight digits whatever follows", () => {
    expect(keyId("aabbccdd" + "11".repeat(12))).toBe(keyId("aabbccdd" + "99".repeat(12)));
  });

  it("has none for a short or non-hex id, so the page shows the label alone", () => {
    expect(keyId("")).toBe("");
    expect(keyId("3f7a9c2")).toBe(""); // seven digits
    expect(keyId("3f7a9c2g")).toBe(""); // g is not hex
    expect(keyId("Recovery key — printed")).toBe("");
    expect(keyId("3f7a-9c21d4e5f607")).toBe(""); // a grouped id is not an id
  });
});

// The details modal's ciphertext hash (APP.md §13): a hash of all zeros is
// no commit yet, and reads N/A rather than sixty-four zeros.
describe("recoveryWords", () => {
  const at = 1760000000;
  it("keeps the label's own words, without the kind the list already says", () => {
    expect(recoveryWords("Recovery key — printed, in the safe", at)).toBe("printed, in the safe");
    expect(recoveryWords("Recovery key - the first one", at)).toBe("the first one");
    expect(recoveryWords("recovery key: office", at)).toBe("office");
    expect(recoveryWords("In the safe", at)).toBe("In the safe");
  });
  it("leaves a word that only begins like the kind alone", () => {
    expect(recoveryWords("Recovery keys for Mum", at)).toBe("Recovery keys for Mum");
  });
  it("says the day it was added for a key named by its kind alone", () => {
    expect(recoveryWords("Recovery key", at)).toBe(`added ${date(at)}`);
    expect(recoveryWords("  ", at)).toBe(`added ${date(at)}`);
    expect(recoveryWords("Recovery key", 0)).toBe("");
  });
});

describe("hashText", () => {
  it("is the hash itself, whole and never shortened", () => {
    const h = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08";
    expect(hashText(h)).toBe(h);
  });

  it("reads N/A while it is all zeros", () => {
    expect(hashText("0".repeat(64))).toBe("N/A");
    expect(hashText("0")).toBe("N/A");
  });

  it("reads N/A when there is none at all", () => {
    expect(hashText("")).toBe("N/A");
    expect(hashText("   ")).toBe("N/A");
    expect(hashText(undefined)).toBe("N/A");
  });

  it("does not read N/A for a hash that merely begins with zeros", () => {
    expect(hashText("0".repeat(63) + "1")).toBe("0".repeat(63) + "1");
  });
});

// A count of one is singular everywhere the page counts (APP.md §3, ruled
// 2026-09-09 — "1 files" was on the status line).
describe("plural", () => {
  it("is singular at one", () => {
    expect(plural(1, "file")).toBe("1 file");
    expect(plural(1, "archive")).toBe("1 archive");
    expect(plural(1, "folder")).toBe("1 folder");
  });

  it("is plural at everything else, nothing included", () => {
    expect(plural(0, "file")).toBe("0 files");
    expect(plural(2, "file")).toBe("2 files");
  });

  it("groups the number as the rest of the page groups one", () => {
    expect(plural(12406, "file")).toBe("12,406 files");
  });

  it("takes an irregular plural rather than guessing at one", () => {
    expect(plural(1, "entry", "entries")).toBe("1 entry");
    expect(plural(3, "entry", "entries")).toBe("3 entries");
  });
});

// The sprite symbol a row wears, shared by the file list and the conflict
// dialog's compare list (APP.md §6).
describe("fileIcon", () => {
  it("is the folder's, whatever the name says", () => {
    expect(fileIcon("notes.txt", true)).toBe("i-folder");
    expect(fileIcon("2024", true)).toBe("i-folder");
  });

  it("follows the five kinds the preview knows", () => {
    expect(fileIcon("IMG_7201.jpg")).toBe("i-image");
    expect(fileIcon("DJI_0042.MP4")).toBe("i-video");
    expect(fileIcon("song.flac")).toBe("i-audio");
    expect(fileIcon("trip-notes.md")).toBe("i-doc");
  });

  it("is a plain file for everything else, a PDF included", () => {
    expect(fileIcon("itinerary.pdf")).toBe("i-file");
    expect(fileIcon("noext")).toBe("i-file");
  });
});

// The extension rule of APP.md §6: the part after the last dot, none when
// the dot is first or absent.
describe("ext", () => {
  it("is the part after the last dot, folded", () => {
    expect(ext("IMG_7201.HEIC")).toBe("heic");
    expect(ext("a.tar.gz")).toBe("gz");
  });

  it("is none when the dot is the first character or there is none", () => {
    expect(ext(".env")).toBe("");
    expect(ext("README")).toBe("");
    expect(ext("")).toBe("");
  });
});

describe("labelledText (the details modal's Copy all as text)", () => {
  it("is one Label: value line per row, the empty ones included", () => {
    expect(labelledText([{ label: "Archive ID", value: "ab12" }, { label: "Last writer", value: "—" }, { label: "Revision", value: "7" }])).toBe("Archive ID: ab12\nLast writer: —\nRevision: 7");
    expect(labelledText([])).toBe("");
  });
});

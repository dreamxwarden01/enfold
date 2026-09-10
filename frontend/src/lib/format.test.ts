import { describe, expect, it } from "vitest";
import { hashText, keyId, plural } from "./format";

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

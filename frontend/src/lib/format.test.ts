import { describe, expect, it } from "vitest";
import { keyId } from "./format";

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

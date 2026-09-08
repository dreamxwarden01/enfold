import { describe, expect, it } from "vitest";
import { foreignPath } from "./paths";

describe("foreignPath", () => {
  it("accepts what this platform could open: a drive letter, in either slash", () => {
    expect(foreignPath("D:\\Archives\\photos-2024.efd")).toBe(false);
    expect(foreignPath("D:/Archives/photos-2024.efd")).toBe(false);
    expect(foreignPath("c:\\a.efd")).toBe(false);
  });

  it("accepts a UNC path: this platform's syntax, whatever else is said about it", () => {
    // Not probed for presence (APP.md §13) — but Show in Explorer works.
    expect(foreignPath("\\\\nas\\media\\a.efd")).toBe(false);
    expect(foreignPath("//nas/media/a.efd")).toBe(false);
  });

  it("calls a path written by another system foreign", () => {
    expect(foreignPath("/Users/me/Archives/a.efd")).toBe(true);
    expect(foreignPath("/mnt/photos/a.efd")).toBe(true);
    expect(foreignPath("~/a.efd")).toBe(true);
  });

  it("calls anything that is not an absolute path foreign, and says nothing about no path", () => {
    expect(foreignPath("a.efd")).toBe(true);
    expect(foreignPath("D:a.efd")).toBe(true); // a drive-relative path opens nothing here
    expect(foreignPath("\\a.efd")).toBe(true); // rooted on no drive
    expect(foreignPath("")).toBe(false); // there is no path to say anything about
  });
});

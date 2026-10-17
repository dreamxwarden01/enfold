import { describe, expect, it } from "vitest";
import { foreignPath, middlePath, renameSelection } from "./paths";

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

// Rename's field opens with the stem selected (APP.md §6).
describe("renameSelection", () => {
  it("selects a file's stem, the part before the last dot", () => {
    expect(renameSelection("notes.md", false)).toEqual([0, 5]);
    expect(renameSelection("a.tar.gz", false)).toEqual([0, 5]);
  });

  it("selects the whole name when there is no dot, or the dot comes first", () => {
    expect(renameSelection("README", false)).toEqual([0, 6]);
    expect(renameSelection(".env", false)).toEqual([0, 4]);
    expect(renameSelection("", false)).toEqual([0, 0]);
  });

  it("selects a folder's whole name, dot and all", () => {
    expect(renameSelection("v1.2", true)).toEqual([0, 4]);
  });
});

describe("middlePath", () => {
  const usual = "C:/Users/me/AppData/Local/Enfold/vault.eks";

  it("leaves a path that fits as it is", () => {
    expect(middlePath(usual, usual.length)).toBe(usual);
    expect(middlePath("D:\\vault.eks", 8)).toBe("D:\\vault.eks"); // nothing between to cut
  });

  it("keeps the drive and the file name, and the folders nearest each as they fit", () => {
    expect(middlePath(usual, 34)).toBe("C:/Users/…/Local/Enfold/vault.eks");
    expect(middlePath(usual, 27)).toBe("C:/Users/…/Enfold/vault.eks");
    expect(middlePath(usual, 21)).toBe("C:/…/Enfold/vault.eks");
    // Too short for anything but the ends: the ends all the same.
    expect(middlePath(usual, 5)).toBe("C:/…/vault.eks");
  });

  it("puts back a folder nearer the root when the one nearer the file does not fit", () => {
    expect(middlePath("C:\\a\\b\\a-long-folder-name\\vault.eks", 20)).toBe("C:\\a\\b\\…\\vault.eks");
  });

  it("never exceeds the budget once the ends fit, and keeps the path's own separator", () => {
    const p = "C:\\Users\\wang_\\AppData\\Local\\Enfold\\vault.eks";
    for (let max = 16; max < p.length; max++) {
      const s = middlePath(p, max);
      expect(s.length).toBeLessThanOrEqual(max);
      expect(s.startsWith("C:\\")).toBe(true);
      expect(s.endsWith("\\vault.eks")).toBe(true);
      expect(s).not.toContain("/");
    }
  });

  it("keeps a UNC share and a leading separator whole", () => {
    expect(middlePath("\\\\nas\\media\\vaults\\2026\\home\\vault.eks", 28)).toBe("\\\\nas\\media\\…\\home\\vault.eks");
    expect(middlePath("/home/me/.local/share/enfold/vault.eks", 24)).toBe("/home/…/enfold/vault.eks");
  });
});

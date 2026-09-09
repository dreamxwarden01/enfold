import { describe, expect, it } from "vitest";
import { askSpooler, printOutcome, recoveryKeyFileName, recoveryKeyTitle } from "./print";

// BitLocker's own shape, "BitLocker Recovery Key <id>": the key's ID and
// never the vault's name, which says nothing about which sheet this is
// (APP.md §6, DECISIONS 2026-09-09).
describe("the recovery key's name", () => {
  it("is the key's ID, in BitLocker's shape", () => {
    expect(recoveryKeyTitle("A7F3-92B1")).toBe("Enfold Keystore Recovery Key A7F3-92B1");
    expect(recoveryKeyFileName("A7F3-92B1")).toBe("Enfold Keystore Recovery Key A7F3-92B1.txt");
  });

  it("never carries the vault's name", () => {
    expect(recoveryKeyTitle("A7F3-92B1")).not.toMatch(/vault/i);
  });

  it("stands on its own when the ID is not to hand", () => {
    expect(recoveryKeyTitle("")).toBe("Enfold Keystore Recovery Key");
    expect(recoveryKeyTitle(undefined)).toBe("Enfold Keystore Recovery Key");
    expect(recoveryKeyFileName("")).toBe("Enfold Keystore Recovery Key.txt");
  });

  it("ignores space around the ID rather than printing it", () => {
    expect(recoveryKeyTitle("  A7F3-92B1 ")).toBe("Enfold Keystore Recovery Key A7F3-92B1");
  });
});

// The shell watches the print spooler around window.print() (APP.md §3
// Shell): a new job — Microsoft Print to PDF included, and one that later
// fails as well — is a submission.
describe("what the spooler's answer means", () => {
  it("maps a submitted job to a print that is done", () => {
    expect(printOutcome(true)).toBe("done");
  });

  it("maps nothing submitted to a cancelled print", () => {
    expect(printOutcome(false)).toBe("cancelled");
  });

  it("reads the three answers of PrintEnd", async () => {
    expect(await askSpooler(async () => true)).toBe("done");
    expect(await askSpooler(async () => false)).toBe("cancelled");
    // A spooler that cannot be read at all is the only fall back to the
    // second question.
    expect(await askSpooler(async () => {
      throw new Error("io");
    })).toBe("ask");
  });

  it("asks whatever the refusal was", async () => {
    expect(await askSpooler(() => Promise.reject({ code: "internal" }))).toBe("ask");
  });
});

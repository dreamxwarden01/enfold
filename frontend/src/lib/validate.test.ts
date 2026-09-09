import { describe, expect, it } from "vitest";
import { MGMT_RULE, PASSWORD_RULE, PIN_RULE, RECOVERY_RULE, REQUIRED, autoSendable, requiredProblem, secretProblem, secretRule, GROUP_MISTYPED, recoveryGroupProblem, CONFIRM_NAME, CONFIRM_SECRET, DESCRIPTION_MAX, DESCRIPTION_RULE, confirmNameProblem, confirmSecretProblem, descriptionProblem, NAME_MAX, NAME_RULE, nameProblem, LEAF_RULE, fileNameProblem } from "./validate";

describe("secretProblem", () => {
  it("requires every secret", () => {
    for (const k of ["pin", "password", "recovery", "mgmtkey"] as const) expect(secretProblem(k, "")).toBe(REQUIRED);
  });
  it("measures a chosen password, never an existing one", () => {
    expect(secretProblem("password", "short", true)).toBe(PASSWORD_RULE);
    expect(secretProblem("password", "eight ch", true)).toBe("");
    expect(secretProblem("password", "x", false)).toBe("");
    expect(secretProblem("password", "ééééééé", true)).toBe(PASSWORD_RULE); // 7 characters, not bytes
  });
  it("knows the PIN, recovery and management key shapes", () => {
    expect(secretProblem("pin", "1234")).toBe(""); // an existing PIN is what the key was given
    expect(secretProblem("pin", "123456")).toBe("");
    expect(secretProblem("pin", "123456789")).toBe(PIN_RULE);
    expect(secretProblem("pin", "\u00e9\u00e9\u00e9\u00e9\u00e9")).toBe(PIN_RULE); // 10 bytes, as the card counts
    expect(secretProblem("recovery", "1234 5678 ".repeat(6))).toBe("");
    expect(secretProblem("recovery", "123456\u2013654321\u2013".repeat(4))).toBe(""); // en dashes, as a word processor writes them
    expect(secretProblem("recovery", "123456")).toBe(RECOVERY_RULE);
    expect(secretProblem("mgmtkey", "0123456789abcdef".repeat(2))).toBe("");
    expect(secretProblem("mgmtkey", "0123456789abcdef 0123456789abcdef")).toBe("");
    expect(secretProblem("mgmtkey", "0123456789abcdef\u00a00123456789abcdef")).toBe(MGMT_RULE); // a no-break space the core refuses
    expect(secretProblem("mgmtkey", "0123456789abcdef".repeat(3))).toBe("");
    expect(secretProblem("mgmtkey", "xyz")).toBe(MGMT_RULE);
  });
  it("states the rule only where one is shown", () => {
    expect(secretRule("password", true)).toBe(PASSWORD_RULE);
    expect(secretRule("password", false)).toBe("");
    expect(secretRule("pin")).toBe(PIN_RULE);
  });
  it("requires a name once whitespace is gone", () => {
    expect(requiredProblem("   ")).toBe(REQUIRED);
    expect(requiredProblem("Mine")).toBe("");
  });
});

describe("autoSendable", () => {
  // The password prompt an accepted PIN brings is answered from the field
  // as the user left it, with no click — and judged all the same: an
  // empty field is refused and marked, not sent (APP.md §6).
  it("refuses an empty password and passes a typed one", () => {
    expect(autoSendable("password", "")).toBe(false);
    expect(autoSendable("password", "x")).toBe(true); // an existing secret is never measured
  });
  it("judges by the same rule the button applies", () => {
    for (const k of ["pin", "password", "recovery", "mgmtkey"] as const) {
      for (const v of ["", "1234", "short", "0123456789abcdef".repeat(2)]) {
        expect(autoSendable(k, v), `${k}:${v}`).toBe(!secretProblem(k, v));
      }
    }
    expect(autoSendable("password", "short", true)).toBe(false); // a chosen one is
  });
});

describe("descriptionProblem", () => {
  it("measures bytes of UTF-8, not characters (FORMAT.md §7.1)", () => {
    expect(DESCRIPTION_MAX).toBe(1024);
    expect(descriptionProblem("")).toBe(""); // empty clears it
    expect(descriptionProblem("a".repeat(1024))).toBe("");
    expect(descriptionProblem("a".repeat(1025))).toBe(DESCRIPTION_RULE);
    // 400 four-byte emoji: 400 characters, 1 600 bytes — over the bound
    // although a character count would pass it.
    const emoji = "\u{1F5C4}".repeat(400);
    expect([...emoji].length).toBe(400);
    expect(descriptionProblem(emoji)).toBe(DESCRIPTION_RULE);
    // 512 two-byte characters fit exactly; one more does not.
    expect(descriptionProblem("é".repeat(512))).toBe("");
    expect(descriptionProblem("é".repeat(513))).toBe(DESCRIPTION_RULE);
  });
});

describe("confirmNameProblem", () => {
  it("compares trimmed and exactly, case and all (APP.md §13)", () => {
    expect(confirmNameProblem("Photos 2024", "Photos 2024")).toBe("");
    expect(confirmNameProblem("  Photos 2024  ", "Photos 2024")).toBe("");
    expect(confirmNameProblem("Photos 2024", "  Photos 2024 ")).toBe("");
    expect(confirmNameProblem("photos 2024", "Photos 2024")).toBe(CONFIRM_NAME);
    expect(confirmNameProblem("Photos 202", "Photos 2024")).toBe(CONFIRM_NAME);
    expect(confirmNameProblem("Photos  2024", "Photos 2024")).toBe(CONFIRM_NAME); // inner space
  });
  it("asks for the name before it judges it", () => {
    expect(confirmNameProblem("", "Photos 2024")).toBe(REQUIRED);
    expect(confirmNameProblem("   ", "Photos 2024")).toBe(REQUIRED);
  });
});

describe("confirmSecretProblem", () => {
  it("passes only two that are the same, byte for byte", () => {
    expect(confirmSecretProblem("a long passphrase", "a long passphrase")).toBe("");
    expect(confirmSecretProblem("a long passphrase", "a long passphras")).toBe(CONFIRM_SECRET);
    expect(confirmSecretProblem("Passphrase", "passphrase")).toBe(CONFIRM_SECRET);
    expect(confirmSecretProblem("pass ", "pass")).toBe(CONFIRM_SECRET); // never trimmed
  });
  it("asks for the second field before it judges it", () => {
    expect(confirmSecretProblem("", "")).toBe(REQUIRED);
    expect(confirmSecretProblem("something", "")).toBe(REQUIRED);
  });
});

describe("nameProblem", () => {
  it("refuses an empty name and one over 1 024 bytes (APP.md \u00a713)", () => {
    expect(nameProblem("Photos 2026")).toBe("");
    expect(nameProblem("   ")).toBe(REQUIRED);
    expect(nameProblem("a".repeat(NAME_MAX))).toBe("");
    expect(nameProblem("a".repeat(NAME_MAX + 1))).toBe(NAME_RULE);
    expect(nameProblem("\u{1F600}".repeat(300))).toBe(NAME_RULE); // 300 characters, 1 200 bytes
  });
});

describe("recoveryGroupProblem", () => {
  it("passes a finished group only when its checksum holds", () => {
    expect(recoveryGroupProblem("")).toBe("");
    expect(recoveryGroupProblem("12345")).toBe("");
    expect(recoveryGroupProblem("000000")).toBe("");
    expect(recoveryGroupProblem("000011")).toBe("");
    expect(recoveryGroupProblem("720885")).toBe("");
    expect(recoveryGroupProblem("000001")).toBe(GROUP_MISTYPED);
    expect(recoveryGroupProblem("720896")).toBe(GROUP_MISTYPED);
    expect(recoveryGroupProblem("12a456")).toBe(GROUP_MISTYPED);
  });
});

describe("fileNameProblem", () => {
  it("asks for a leaf, and refuses one that is really two", () => {
    expect(fileNameProblem("notes.md")).toBe("");
    expect(fileNameProblem("")).toBe(REQUIRED);
    expect(fileNameProblem("   ")).toBe(REQUIRED);
    expect(fileNameProblem("2024/notes.md")).toBe(LEAF_RULE);
    expect(fileNameProblem("/notes.md")).toBe(LEAF_RULE);
  });
});

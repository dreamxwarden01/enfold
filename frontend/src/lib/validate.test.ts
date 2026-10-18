import { describe, expect, it } from "vitest";
import { MGMT_RULE, PASSWORD_RULE, PIN_RULE, RECOVERY_RULE, REQUIRED, autoSendable, requiredProblem, secretProblem, secretRule, GROUP_MISTYPED, recoveryGroupProblem, recoveryGroupMarks, CONFIRM_NAME, CONFIRM_SECRET, DESCRIPTION_MAX, DESCRIPTION_RULE, confirmNameProblem, confirmSecretProblem, descriptionProblem, NAME_MAX, NAME_RULE, nameProblem, LEAF_RULE, fileNameProblem, NAME_TAKEN, newNameProblem, NAME_LONG, NAME_CONTROL, NAME_TRAILING, NAME_RESERVED } from "./validate";

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

describe("recoveryGroupMarks", () => {
  it("judges each group by its own check digit alone", () => {
    // 123453 and 401236 are multiples of 11; 345671 is not.
    expect(recoveryGroupMarks(["123453", "345671", "40", ""])).toEqual(["tick", "bad", "", ""]);
  });
  it("ticks every group of a key that checks out, whatever the vault says of it", () => {
    // A refused key is marked as a whole by its line, not cell by cell.
    expect(recoveryGroupMarks(Array(8).fill("123453"))).toEqual(Array(8).fill("tick"));
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

  // The core's own vectors (internal/format/format_test.go,
  // TestValidateName): what R20 takes, the page takes, and what it refuses
  // the page refuses with the part of the rule that is broken.
  it("takes every name R20 takes", () => {
    for (const n of ["a", "file.txt", "目录.mp4", "COM", "COM10", "LPT0", "config.txt", "a b.e", "..a",
      "世".repeat(255), "a".repeat(255), "\u{1F600}".repeat(127) + "a", "café.txt", "naïve"]) {
      expect(fileNameProblem(n), n).toBe("");
    }
  });

  it("counts UTF-16 code units, as the core does: an emoji is two", () => {
    expect(fileNameProblem("世".repeat(256))).toBe(NAME_LONG);
    expect(fileNameProblem("a".repeat(256))).toBe(NAME_LONG);
    expect(fileNameProblem("\u{1F600}" + "a".repeat(254))).toBe(NAME_LONG);
    expect(fileNameProblem("\u{1F600}".repeat(128))).toBe(NAME_LONG);
    expect(NAME_LONG).toBe("At most 255 characters.");
  });

  it("refuses the forbidden characters", () => {
    for (const n of ["/", "a/b", "dir/file.txt", "/abs", "a/", "a\\b", "C:x", "a:b", "a*", "q?", "<", ">", "|", '"']) {
      expect(fileNameProblem(n), n).toBe(LEAF_RULE);
    }
    expect(LEAF_RULE).toBe('A name cannot contain < > : " / \\ | ? *');
  });

  it("refuses a control character, C1 included", () => {
    for (const n of ["a\x00b", "a\tb", "a\x7fb", "a\u0085b", "a\u0080b", "a\u009fb", "\u0085"]) {
      expect(fileNameProblem(n), JSON.stringify(n)).toBe(NAME_CONTROL);
    }
  });

  it("refuses a trailing dot, the relative elements with it", () => {
    for (const n of [".", "..", "x.", "prn."]) expect(fileNameProblem(n), n).toBe(NAME_TRAILING);
  });

  it("refuses a device name, with or without an extension", () => {
    for (const n of ["nul", "NUL.txt", "con", "Com1", "LPT9.log", "aux", "PRN", "lpt1.tar.gz"]) {
      expect(fileNameProblem(n), n).toBe(NAME_RESERVED);
    }
  });

  // Every caller sends the name trimmed, so a trailing space typed is
  // not the core's "x " — it never reaches the core.
  it("judges the name trimmed, as it is sent", () => {
    expect(fileNameProblem("x ")).toBe("");
    expect(fileNameProblem(" notes.md ")).toBe("");
    expect(fileNameProblem("x. ")).toBe(NAME_TRAILING);
  });
});

describe("the name a new folder may take", () => {
  it("obeys the rename's rule: a name is required, and carries no separator", () => {
    expect(newNameProblem("Receipts", [])).toBe("");
    expect(newNameProblem("", [])).toBe(REQUIRED);
    expect(newNameProblem("   ", [])).toBe(REQUIRED);
    expect(newNameProblem("a/b", [])).toBe(LEAF_RULE);
  });

  it("refuses a name the folder already holds, whatever its case", () => {
    expect(newNameProblem("Receipts", ["Receipts"])).toBe(NAME_TAKEN);
    expect(newNameProblem("receipts", ["Receipts"])).toBe(NAME_TAKEN);
    expect(newNameProblem(" Receipts ", ["notes.md", "Receipts"])).toBe(NAME_TAKEN);
    expect(newNameProblem("Bills", ["Receipts"])).toBe("");
  });
});

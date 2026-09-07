import { describe, expect, it } from "vitest";
import { MGMT_RULE, PASSWORD_RULE, PIN_RULE, RECOVERY_RULE, REQUIRED, requiredProblem, secretProblem, secretRule } from "./validate";

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

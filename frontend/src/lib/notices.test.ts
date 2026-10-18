import { describe, expect, it } from "vitest";
import { OPENED, lockNotices, ranked, tamperedShown, tier, tierIcon } from "./notices";

// The three tiers (direction 9, ruled 2026-10-18): danger for the
// Tampered state's codes, info for what heals itself or only informs,
// attention for the rest — a code not yet classified among them.
describe("a warning's tier", () => {
  it("is danger for every tampered code", () => {
    for (const c of ["vault.tampered", "vault.tampered_hash", "vault.tampered_generation"]) expect(tier(c), c).toBe("danger");
  });
  it("is info for what heals itself or only informs", () => {
    expect(tier("vault.stale")).toBe("info");
    expect(tier("vault.timeouts_clamped")).toBe("info");
  });
  it("is attention for a weakened guarantee", () => {
    for (const c of ["token.reset_failed", "shell.lock_detection_unavailable", "settings.unsaved"]) expect(tier(c), c).toBe("attention");
  });
  it("is attention for a code nobody has classified", () => {
    expect(tier("something.new")).toBe("attention");
    expect(tier("")).toBe("attention");
  });
  it("wears the icon of its tier", () => {
    expect(tierIcon("danger")).toBe("i-error");
    expect(tierIcon("attention")).toBe("i-warn");
    expect(tierIcon("info")).toBe("i-info");
  });
});

describe("the order the notices are drawn in", () => {
  it("puts danger first, then attention, then info", () => {
    expect(ranked(["vault.stale", "token.reset_failed", "vault.tampered"])).toEqual(["vault.tampered", "token.reset_failed", "vault.stale"]);
  });
  it("keeps the core's order within a tier", () => {
    expect(ranked(["settings.unsaved", "vault.stale", "token.reset_failed", "vault.timeouts_clamped"])).toEqual(["settings.unsaved", "token.reset_failed", "vault.stale", "vault.timeouts_clamped"]);
  });
  it("ranks the lock screen's open-archives notice as attention, above info", () => {
    expect(tier(OPENED)).toBe("attention");
    expect(ranked(["vault.tampered", "token.reset_failed", "vault.stale", OPENED])).toEqual(["vault.tampered", "token.reset_failed", OPENED, "vault.stale"]);
    expect(ranked(["vault.stale", OPENED])).toEqual([OPENED, "vault.stale"]);
  });
  it("leaves the list it was given alone", () => {
    const given = ["vault.stale", "vault.tampered"];
    ranked(given);
    expect(given).toEqual(["vault.stale", "vault.tampered"]);
    expect(ranked([])).toEqual([]);
  });
});

// One predicate for every surface's Tampered banner (APP.md §6): after a
// lock the core clears VaultStatus.tampered but keeps the vault.tampered
// warning, and the lock screen and Settings opened from its gear must
// agree on what they draw.
describe("whether the Tampered banner is shown (tamperedShown)", () => {
  it("is shown for the state while unlocked or after a failed unlock", () => {
    expect(tamperedShown({ tampered: true, warnings: ["vault.tampered"] })).toBe(true);
    expect(tamperedShown({ tampered: true, warnings: [] })).toBe(true);
  });
  it("is still shown after a lock, from the warning the lock keeps", () => {
    expect(tamperedShown({ tampered: false, warnings: ["vault.stale", "vault.tampered"] })).toBe(true);
    expect(tamperedShown({ tampered: false, warnings: ["vault.tampered_hash"] })).toBe(true);
  });
  it("is not shown for other warnings or no status", () => {
    expect(tamperedShown({ tampered: false, warnings: ["vault.stale", "token.reset_failed"] })).toBe(false);
    expect(tamperedShown({ tampered: false, warnings: null })).toBe(false);
    expect(tamperedShown(null)).toBe(false);
    expect(tamperedShown(undefined)).toBe(false);
  });
});

describe("the lock screen's notices (lockNotices)", () => {
  it("draws the Tampered banner once, first, whichever fact raised it", () => {
    expect(lockNotices(["vault.stale", "vault.tampered", "token.reset_failed"], true, false)).toEqual(["vault.tampered", "token.reset_failed", "vault.stale"]);
    expect(lockNotices(["vault.stale"], true, true)).toEqual(["vault.tampered", OPENED, "vault.stale"]);
    expect(lockNotices(["vault.tampered_hash", "vault.tampered"], true, false)).toEqual(["vault.tampered"]);
  });
  it("draws no Tampered banner when it is not shown", () => {
    expect(lockNotices(["vault.stale", "token.reset_failed"], false, true)).toEqual(["token.reset_failed", OPENED, "vault.stale"]);
    expect(lockNotices([], false, false)).toEqual([]);
  });
});

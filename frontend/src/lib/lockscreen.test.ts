import { describe, expect, it } from "vitest";
import { CeremonyStep } from "./api";
import type { CeremonyState } from "./api";
import { methodAfter, outcomeAfter, wordedForToken } from "./outcome";
import { samePath } from "./paths";
import { firstRunCard } from "./firstrun";
import type { VaultStatus } from "./api";
import { VaultState } from "./api";

function ev(kind: string, step: CeremonyStep, promptId = "", archives = 0, method = ""): CeremonyState {
  return { seq: 1, kind, method, step, promptId, choose: false, slotLabel: "", recoveryId: "", retries: 0, retriesKnown: false, verified: false, readerCount: 0, n: 0, pinAsked: false, error: "" as never, removeLabel: "", insertLabel: "", archives };
}

describe("samePath", () => {
  it("compares like the core's samePath", () => {
    expect(samePath("C:/Users/me/AppData/Local/Enfold/vault.eks", "c:\\users\\me\\appdata\\local\\enfold\\vault.eks")).toBe(true);
    expect(samePath("C:\\data\\.\\vault.eks", "C:\\data\\\\vault.eks")).toBe(true);
    expect(samePath("C:\\data\\x\\..\\vault.eks", "C:\\data\\vault.eks\\")).toBe(true);
    expect(samePath("C:\\data\\vault.eks", "D:\\data\\vault.eks")).toBe(false);
    expect(samePath("", "C:\\data\\vault.eks")).toBe(false);
  });
});

describe("outcomeAfter", () => {
  it("keeps an import's outcome until a new ceremony opens, and clears it then", () => {
    let o = outcomeAfter(null, ev("import", CeremonyStep.StepWaitingForKey));
    o = outcomeAfter(o, ev("import", CeremonyStep.StepPassword, "p1"));
    o = outcomeAfter(o, ev("import", CeremonyStep.StepDone));
    expect(o).toEqual({ kind: "import", archives: 0 });
    o = outcomeAfter(o, ev("unlock", CeremonyStep.StepWaitingForKey));
    expect(o).toBeNull();
  });
  it("takes a verify's archive count and a create's reveal, not a recovery prompt", () => {
    expect(outcomeAfter(null, ev("verify", CeremonyStep.StepDone, "", 3))).toEqual({ kind: "verify", archives: 3 });
    expect(outcomeAfter(null, ev("create", CeremonyStep.StepRecovery))).toEqual({ kind: "create", archives: 0 });
    expect(outcomeAfter(null, ev("create", CeremonyStep.StepRecovery, "p2"))).toBeNull();
    expect(outcomeAfter(null, ev("enroll", CeremonyStep.StepDone))).toBeNull();
  });
});

describe("tokenWording", () => {
  it("keeps the method the core named for the rest of the ceremony, and forgets it at the next opening", () => {
    let m = methodAfter("", ev("unlock", CeremonyStep.StepWaitingForKey));
    expect(m).toBe("");
    m = methodAfter(m, ev("unlock", CeremonyStep.StepProbing, "", 0, "token"));
    expect(m).toBe("token");
    m = methodAfter(m, ev("unlock", CeremonyStep.StepPassword, "p1")); // no method on this event
    expect(m).toBe("token");
    m = methodAfter(m, ev("unlock", CeremonyStep.StepWaitingForKey)); // a new ceremony
    expect(m).toBe("");
    m = methodAfter(m, ev("unlock", CeremonyStep.StepPassword, "p2", 0, "password"));
    expect(m).toBe("password");
  });

  it("words the strip for the key through an entangled vault's password prompt", () => {
    // The silent breakage this replaces: with VaultStatus.Entangled on, a
    // token unlock reaches StepPassword before the touch, and the old rule
    // read that as "a secret was asked" and stopped describing the flow the
    // user is in.
    const c = ev("unlock", CeremonyStep.StepPassword, "p1", 0, "token");
    expect(wordedForToken(methodAfter("token", c), c)).toBe(true);
    const pin = ev("unlock", CeremonyStep.StepPIN, "p2", 0, "token");
    expect(wordedForToken("token", pin)).toBe(true);
  });

  it("never words a password or recovery way in, a check or a setup for the key", () => {
    expect(wordedForToken("password", ev("unlock", CeremonyStep.StepPassword, "p1", 0, "password"))).toBe(false);
    expect(wordedForToken("recovery", ev("unlock", CeremonyStep.StepRecovery, "p1", 0, "recovery"))).toBe(false);
    expect(wordedForToken("token", ev("verify", CeremonyStep.StepRecovery, "p1", 0, "token"))).toBe(false);
    expect(wordedForToken("token", ev("setup", CeremonyStep.StepRecovery, "p1", 0, "token"))).toBe(false);
  });

  it("falls back to the step until the core names a method, and to the key with no ceremony at all", () => {
    expect(wordedForToken("", ev("unlock", CeremonyStep.StepWaitingForKey))).toBe(true);
    expect(wordedForToken("", ev("unlock", CeremonyStep.StepPassword, "p1"))).toBe(false);
    expect(wordedForToken("", ev("unlock", CeremonyStep.StepRecovery, "p1"))).toBe(false);
    expect(wordedForToken("", null)).toBe(true);
  });
});

describe("firstRunCard", () => {
  const st = (over: Partial<VaultStatus>): VaultStatus => ({ state: VaultState.StateNone, missingPath: "", damaged: false, ...over }) as unknown as VaultStatus;
  it("offers a create only on first run, and a rebuild only over a file that is there and not a keystore", () => {
    expect(firstRunCard(null)).toBeNull();
    expect(firstRunCard(st({ state: VaultState.StateLocked }))).toBeNull();
    expect(firstRunCard(st({ state: VaultState.StateBroken, missingPath: "C:\\v.eks", damaged: true }))).toBeNull();
    expect(firstRunCard(st({}))).toBe("fresh");
    expect(firstRunCard(st({ missingPath: "C:\\v.eks" }))).toBe("absent");
    expect(firstRunCard(st({ missingPath: "C:\\v.eks", damaged: true }))).toBe("damaged");
  });
});

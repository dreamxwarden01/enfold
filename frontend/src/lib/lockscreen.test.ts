import { describe, expect, it } from "vitest";
import { CeremonyStep } from "./api";
import type { CeremonyState } from "./api";
import { outcomeAfter, secretAskedAfter } from "./outcome";
import { samePath } from "./paths";
import { firstRunCard } from "./firstrun";
import type { VaultStatus } from "./api";
import { VaultState } from "./api";

function ev(kind: string, step: CeremonyStep, promptId = "", archives = 0): CeremonyState {
  return { seq: 1, kind, step, promptId, choose: false, slotLabel: "", retries: 0, retriesKnown: false, verified: false, readerCount: 0, n: 0, pinAsked: false, error: "" as never, removeLabel: "", insertLabel: "", archives, cancelling: false };
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

describe("secretAskedAfter", () => {
  it("is set by a password or recovery prompt, not by a reveal, and forgotten at the next opening", () => {
    let s = secretAskedAfter(false, ev("unlock", CeremonyStep.StepWaitingForKey));
    expect(s).toBe(false);
    s = secretAskedAfter(s, ev("unlock", CeremonyStep.StepPassword, "p1"));
    expect(s).toBe(true);
    s = secretAskedAfter(s, ev("unlock", CeremonyStep.StepDeriving));
    expect(s).toBe(true);
    s = secretAskedAfter(s, ev("create", CeremonyStep.StepWaitingForKey));
    expect(s).toBe(false);
    s = secretAskedAfter(s, ev("create", CeremonyStep.StepRecovery));
    expect(s).toBe(false);
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

import { describe, expect, it } from "vitest";
import { CeremonyStep } from "./api";
import type { CeremonyState } from "./api";
import { methodAfter, outcomeAfter, wordedForToken } from "./outcome";
import { samePath } from "./paths";
import { firstRunCard, retiredAt } from "./firstrun";
import type { VaultStatus } from "./api";
import { VaultState } from "./api";
import { FOUND_HOLD, NOT_PASSED, NO_HALVES, NO_PHASE, PAST, choosesVaultPassword, dialogFoot, derivingOf, dialogViewOf, dialogWay, discs, endsWithMore, explicitMethod, finishGreyed, halvesAfter, failedShown, green, holdBefore, insertLed, lastTry, lineKind, nothingChanged, offersRecoverySwitch, padlockOpen, passedAfter, phaseAfter, phaseOf, pinLeads, provesFirst, retriesTone, shownAfter, typedProofLeads, viewOf, wayOf } from "./lockscreen";
import { codeText } from "./strings";
import type { Way } from "./lockscreen";

function ev(kind: string, step: CeremonyStep, promptId = "", archives = 0, method = ""): CeremonyState {
  return { seq: 1, kind, method, step, promptId, choose: false, slotLabel: "", recoveryId: "", retries: 0, retriesKnown: false, verified: false, readerCount: 0, n: 0, pinAsked: false, error: "" as never, removeLabel: "", insertLabel: "", archives, rekey: false, backedUp: false, keepIndex: 0, keepCount: 0 };
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

// The stage (APP.md §6, ruled 2026-10-17): one panel changing in place.
describe("the stage's views", () => {
  const S = CeremonyStep;
  const at = (step: CeremonyStep, o: Partial<{ kind: string; error: string; promptId: string }> = {}) => ({ kind: "unlock", step, error: "", promptId: "", ...o });
  const run = (way: Way, c: ReturnType<typeof at> | null, running = true) => viewOf({ running, firstRun: false, way, c });

  it("rests when nothing runs, and at first run shows the first-run panel", () => {
    expect(viewOf({ running: false, firstRun: false, way: "", c: null })).toBe("rest");
    expect(viewOf({ running: false, firstRun: true, way: "", c: null })).toBe("firstrun");
    // A cancelled unlock is no failure to keep on the panel.
    expect(run("token", at(S.StepFailed, { error: "ceremony.cancelled" }), false)).toBe("rest");
  });
  it("draws the key art only for a YubiKey's way in, and holds for the others' first instant", () => {
    expect(run("token", at(S.StepWaitingForKey))).toBe("insert");
    expect(run("password", at(S.StepWaitingForKey))).toBe("hold");
    expect(run("recovery", at(S.StepWaitingForKey))).toBe("hold");
    expect(run("token", at(S.StepTwoKeys))).toBe("insert");
    expect(run("token", at(S.StepProbing))).toBe("insert");
    expect(run("token", at(S.StepNoMatch))).toBe("edge");
  });
  it("keeps the touch, the derivation, the release and Done of a YubiKey on one green view", () => {
    for (const s of [S.StepTouch, S.StepDeriving, S.StepReleasing, S.StepDone]) {
      expect(run("token", at(s))).toBe("touch");
      expect(green(run("token", at(s)))).toBe(true);
    }
    // A typed way in derives on the neutral panel.
    expect(run("password", at(S.StepDeriving))).toBe("opening");
    expect(run("recovery", at(S.StepDone))).toBe("done");
    expect(green(run("recovery", at(S.StepDone)))).toBe(false);
  });
  it("asks for secrets in the secret view and the recovery key in its cells", () => {
    expect(run("token", at(S.StepPIN, { promptId: "p" }))).toBe("secret");
    expect(run("password", at(S.StepPassword, { promptId: "p" }))).toBe("secret");
    expect(run("recovery", at(S.StepRecovery, { promptId: "r" }))).toBe("recovery");
    expect(run("", at(S.StepRecovery, { kind: "create" }))).toBe("wait");
    expect(run("token", at(S.StepBlocked))).toBe("blocked");
  });
  it("keeps an unlock's failure on the panel after the state went back to Locked", () => {
    expect(run("token", at(S.StepFailed, { error: "token.touch" }), false)).toBe("failed");
    expect(run("token", at(S.StepFailed, { error: "token.touch" }), true)).toBe("failed");
    // An import's failure is said on the rest panel, as a bar.
    expect(run("", at(S.StepFailed, { kind: "import", error: "vault.auth" }), false)).toBe("rest");
    expect(failedShown({ running: false, firstRun: false, way: "token", c: at(S.StepFailed, { error: "token.touch" }) })).toBe(true);
  });
  it("holds what is on the panel, and the found LED before the PIN", () => {
    expect(shownAfter("insert", "hold")).toBe("insert");
    expect(shownAfter(null, "hold")).toBe("wait");
    expect(shownAfter("rest", "secret")).toBe("secret");
    expect(holdBefore("insert", "secret")).toBe(FOUND_HOLD);
    expect(FOUND_HOLD).toBe(200);
    expect(holdBefore("insert", "failed")).toBe(0);
    expect(holdBefore("insert", "rest")).toBe(0);
    expect(holdBefore("insert", "insert")).toBe(0);
    expect(holdBefore("secret", "touch")).toBe(0);
  });
});

describe("the step line", () => {
  const S = CeremonyStep;
  it("is an unlock's alone: the YubiKey's three steps or the recovery key's two", () => {
    expect(lineKind("unlock", "token")).toBe("token");
    expect(lineKind("unlock", "recovery")).toBe("recovery");
    expect(lineKind("unlock", "password")).toBe("");
    expect(lineKind("create", "token")).toBe("");
    expect(lineKind("verify", "recovery")).toBe("");
  });
  it("fills its discs as steps pass", () => {
    expect(discs(3, phaseOf("token", S.StepWaitingForKey, 0))).toEqual(["live", "next", "next"]);
    expect(discs(3, phaseOf("token", S.StepPIN, 0))).toEqual(["done", "live", "next"]);
    expect(discs(3, phaseOf("token", S.StepTouch, 1))).toEqual(["done", "done", "live"]);
    expect(discs(3, phaseOf("token", S.StepDeriving, 2))).toEqual(["done", "done", "done"]);
    expect(discs(2, phaseOf("recovery", S.StepRecovery, 0))).toEqual(["live", "next"]);
    expect(discs(2, phaseOf("recovery", S.StepDeriving, 0))).toEqual(["done", "live"]);
    expect(discs(2, phaseOf("recovery", S.StepDone, 1))).toEqual(["done", "done"]);
  });
  it("stays where the ceremony stopped", () => {
    // A touch that timed out is the touch's, wherever the page joined.
    expect(phaseOf("token", S.StepFailed, 0, "token.touch")).toBe(2);
    expect(phaseOf("token", S.StepFailed, 1, "token.too_many_operations")).toBe(1);
    expect(phaseOf("token", S.StepFailed, PAST, "vault.tampered_generation")).toBe(2);
    expect(phaseOf("token", S.StepBlocked, 1)).toBe(1);
    // A wrong password after the touch goes back to the PIN's step.
    expect(phaseOf("token", S.StepPassword, 2)).toBe(1);
  });
  it("advances once per event", () => {
    let p = phaseAfter(NO_PHASE, "token", { seq: 1, step: S.StepPIN });
    expect(p.phase).toBe(1);
    const same = phaseAfter(p, "token", { seq: 1, step: S.StepPIN });
    expect(same).toBe(p);
    p = phaseAfter(p, "token", { seq: 2, step: S.StepFailed, error: "token.no_card" });
    expect(p.phase).toBe(1);
    expect(phaseAfter(p, "token", null)).toEqual(NO_PHASE);
  });
});

describe("the way in", () => {
  const S = CeremonyStep;
  it("takes the core's method first, then the button pressed, then the step", () => {
    expect(wayOf("recovery", "token", { kind: "unlock", step: S.StepWaitingForKey })).toBe("recovery");
    expect(wayOf("", "password", { kind: "unlock", step: S.StepWaitingForKey })).toBe("password");
    expect(wayOf("", "", { kind: "create", step: S.StepTouch })).toBe("token");
    expect(wayOf("", "", { kind: "verify", step: S.StepRecovery })).toBe("recovery");
    expect(wayOf("", "", null)).toBe("");
  });
});

describe("the PIN's retries", () => {
  it("turn attention at two and danger at one, and the last try is said", () => {
    const r = (retries: number, verified = false, retriesKnown = true) => ({ retries, verified, retriesKnown });
    expect(retriesTone(r(3))).toBe("");
    expect(retriesTone(r(2))).toBe("attention");
    expect(retriesTone(r(1))).toBe("danger");
    expect(lastTry(r(1))).toBe(true);
    expect(lastTry(r(2))).toBe(false);
    // A verified card cannot count, so it warns of nothing.
    expect(retriesTone(r(1, true))).toBe("");
    expect(lastTry(r(1, false, false))).toBe(false);
  });
});

// Every wait for a YubiKey is drawn, whatever the ceremony (APP.md §2.2,
// §6): the views a run of the core's events asks for, as the page tracks
// them — the way in (wayOf) and whether the ceremony has left its opening
// WaitingForKey (passedAfter).
describe("the waits for a YubiKey in every ceremony", () => {
  const S = CeremonyStep;
  type Ev = { kind: string; step: CeremonyStep; method?: string; promptId?: string; removeLabel?: string };
  // views plays the events and returns the view each one leaves on the
  // panel; begun is the button the page pressed (an unlock's alone).
  function views(evs: Ev[], begun: Way = ""): string[] {
    let passed = NOT_PASSED;
    let shown: ReturnType<typeof shownAfter> | null = "rest";
    return evs.map((e) => {
      passed = passedAfter(passed, e);
      const way = wayOf(e.method ?? "", begun, e);
      shown = shownAfter(shown, viewOf({ running: true, firstRun: false, way, c: { ...e, error: "" }, passed: passed.passed, afterTouch: passed.afterTouch }));
      return shown;
    });
  }

  it("draws a create's wait for the YubiKey it adds, with and without the password asked first", () => {
    // The core names no method for a create; its token flow opens at
    // SwapKey with nothing to remove, then waits for a reader.
    expect(views([{ kind: "create", step: S.StepWaitingForKey }, { kind: "create", step: S.StepSwapKey }, { kind: "create", step: S.StepWaitingForKey }])).toEqual(["rest", "insert", "insert"]);
    expect(
      views([
        { kind: "create", step: S.StepWaitingForKey },
        { kind: "create", step: S.StepPassword, promptId: "c" },
        { kind: "create", step: S.StepSwapKey },
        { kind: "create", step: S.StepWaitingForKey },
        { kind: "create", step: S.StepPIN, promptId: "p" },
      ]),
    ).toEqual(["rest", "secret", "insert", "insert", "secret"]);
    // A create whose way in is a password never shows the key art.
    expect(views([{ kind: "create", step: S.StepWaitingForKey }, { kind: "create", step: S.StepPassword, promptId: "c" }, { kind: "create", step: S.StepDeriving }])).toEqual(["rest", "secret", "opening"]);
  });
  it("draws a setup's wait for its YubiKey after the recovery key, though the core named the recovery key's way", () => {
    expect(
      views([
        { kind: "setup", step: S.StepWaitingForKey },
        { kind: "setup", step: S.StepWaitingForKey, method: "recovery" },
        { kind: "setup", step: S.StepRecovery, method: "recovery", promptId: "r" },
        { kind: "setup", step: S.StepDeriving, method: "recovery" },
        { kind: "setup", step: S.StepSwapKey, method: "recovery" },
        { kind: "setup", step: S.StepWaitingForKey, method: "recovery" },
      ]),
    ).toEqual(["rest", "rest", "recovery", "opening", "insert", "insert"]);
    // Even with no SwapKey seen (the page joined late), a wait after
    // another step is a real one.
    expect(views([{ kind: "setup", step: S.StepDeriving, method: "recovery" }, { kind: "setup", step: S.StepWaitingForKey, method: "recovery" }])).toEqual(["opening", "insert"]);
  });
  it("draws a YubiKey pulled during an enrolment's PIN as the wait it is", () => {
    expect(views([{ kind: "create", step: S.StepPIN, promptId: "p" }, { kind: "create", step: S.StepWaitingForKey }])).toEqual(["secret", "insert"]);
  });
  it("still holds over the opening instant of a typed way in", () => {
    expect(views([{ kind: "unlock", step: S.StepWaitingForKey }, { kind: "unlock", step: S.StepWaitingForKey, method: "password" }, { kind: "unlock", step: S.StepPassword, method: "password", promptId: "p" }], "password")).toEqual(["rest", "rest", "secret"]);
    expect(views([{ kind: "unlock", step: S.StepWaitingForKey }, { kind: "unlock", step: S.StepRecovery, method: "recovery", promptId: "r" }], "recovery")).toEqual(["rest", "recovery"]);
  });
  it("starts afresh after a ceremony ends, and on a new kind", () => {
    let p = passedAfter(NOT_PASSED, { kind: "setup", step: S.StepRecovery });
    expect(p.passed).toBe(true);
    p = passedAfter(p, { kind: "setup", step: S.StepFailed });
    expect(p.passed).toBe(false);
    p = passedAfter(passedAfter(NOT_PASSED, { kind: "setup", step: S.StepRecovery }), { kind: "unlock", step: S.StepWaitingForKey });
    expect(p).toMatchObject({ kind: "unlock", passed: false });
    expect(passedAfter(p, null)).toBe(NOT_PASSED);
    // A copy of the empty value is kept, not replaced: the page's effect settles.
    const copy = { ...NOT_PASSED };
    expect(passedAfter(copy, null)).toBe(copy);
    // Unchanged facts keep the same object, so the page's effect settles.
    expect(passedAfter(p, { kind: "unlock", step: S.StepWaitingForKey })).toBe(p);
  });
  // The green holds through whatever follows a touch in the same ceremony
  // (APP.md §6, ruled 2026-10-17): the core derives after a create's and a
  // setup's YubiKey has proved itself by its touch (keys.go CreateVault,
  // vaultfile.go adoptBackup and the setup before AddFirstWayIn), and that
  // derivation was drawn neutral, the wash leaving mid-ceremony.
  it("keeps a create's derivation after its YubiKey's touch on the green", () => {
    expect(
      views([
        { kind: "create", step: S.StepWaitingForKey },
        { kind: "create", step: S.StepSwapKey },
        { kind: "create", step: S.StepWaitingForKey },
        { kind: "create", step: S.StepPIN, promptId: "p" },
        { kind: "create", step: S.StepDeriving }, // a key made on the YubiKey, before its touch
        { kind: "create", step: S.StepTouch },
        { kind: "create", step: S.StepDeriving },
        { kind: "create", step: S.StepRecovery, removeLabel: "" }, // the new recovery key, shown
      ]),
    ).toEqual(["rest", "insert", "insert", "secret", "opening", "touch", "touch", "wait"]);
    // The password ticked first changes nothing after the touch.
    expect(
      views([
        { kind: "create", step: S.StepPassword, promptId: "c" },
        { kind: "create", step: S.StepSwapKey },
        { kind: "create", step: S.StepWaitingForKey },
        { kind: "create", step: S.StepPIN, promptId: "p" },
        { kind: "create", step: S.StepTouch },
        { kind: "create", step: S.StepDeriving },
        { kind: "create", step: S.StepDone },
      ]),
    ).toEqual(["secret", "insert", "insert", "secret", "touch", "touch", "touch"]);
  });
  it("keeps a setup's derivation and Done after its YubiKey's touch on the green, though the core named the recovery key's way", () => {
    expect(
      views([
        { kind: "setup", step: S.StepRecovery, method: "recovery", promptId: "r" },
        { kind: "setup", step: S.StepDeriving, method: "recovery" },
        { kind: "setup", step: S.StepSwapKey, method: "recovery" },
        { kind: "setup", step: S.StepWaitingForKey, method: "recovery" },
        { kind: "setup", step: S.StepPIN, method: "recovery", promptId: "p" },
        { kind: "setup", step: S.StepTouch, method: "recovery" },
        { kind: "setup", step: S.StepDeriving, method: "recovery" },
        { kind: "setup", step: S.StepDone, method: "recovery" },
      ]),
    ).toEqual(["recovery", "opening", "insert", "insert", "secret", "touch", "touch", "touch"]);
  });
  it("lets the green go once something is asked after the touch, and with the ceremony's end", () => {
    let p = passedAfter(NOT_PASSED, { kind: "create", step: S.StepTouch });
    expect(p.afterTouch).toBe(true);
    p = passedAfter(p, { kind: "create", step: S.StepDeriving });
    expect(p.afterTouch).toBe(true);
    expect(passedAfter(p, { kind: "create", step: S.StepPassword }).afterTouch).toBe(false);
    p = passedAfter(p, { kind: "create", step: S.StepDone });
    expect(p.afterTouch).toBe(true); // Done is drawn on the green
    expect(passedAfter(p, { kind: "create", step: S.StepWaitingForKey }).afterTouch).toBe(false);
    // A new kind starts afresh.
    expect(passedAfter(passedAfter(NOT_PASSED, { kind: "unlock", step: S.StepTouch }), { kind: "setup", step: S.StepDeriving }).afterTouch).toBe(false);
  });
  it("leaves a swap proper, with a YubiKey to remove, as a wait that asks nothing", () => {
    expect(viewOf({ running: true, firstRun: false, way: "", c: { kind: "setup", step: S.StepSwapKey, removeLabel: "YubiKey 5C" } })).toBe("wait");
  });
});

describe("the touch done before a password asked again", () => {
  const S = CeremonyStep;
  it("checks the touch while the PIN's step is live again", () => {
    let p = phaseAfter(NO_PHASE, "token", { seq: 1, step: S.StepPIN });
    p = phaseAfter(p, "token", { seq: 2, step: S.StepPassword });
    expect(p.touched).toBe(false); // the password beside the PIN, before the touch
    expect(discs(3, p.phase, p.touched)).toEqual(["done", "live", "next"]);
    p = phaseAfter(p, "token", { seq: 3, step: S.StepTouch, n: 1 });
    p = phaseAfter(p, "token", { seq: 4, step: S.StepDeriving, n: 1 });
    p = phaseAfter(p, "token", { seq: 5, step: S.StepPassword, error: "vault.auth", n: 1 });
    expect(p).toMatchObject({ phase: 1, touched: true });
    expect(discs(3, p.phase, p.touched)).toEqual(["done", "live", "done"]);
    // A failure there keeps it; the derivation after the right password passes all.
    expect(phaseAfter(p, "token", { seq: 6, step: S.StepFailed, error: "ceremony.cancelled" }).touched).toBe(true);
    expect(phaseAfter(p, "token", { seq: 6, step: S.StepDeriving, n: 1 })).toMatchObject({ phase: PAST, touched: false });
  });
  it("knows it from the event for a page that joined at the password", () => {
    const p = phaseAfter(NO_PHASE, "token", { seq: 9, step: S.StepPassword, error: "vault.auth", n: 1 });
    expect(discs(3, p.phase, p.touched)).toEqual(["done", "live", "done"]);
    expect(phaseAfter(NO_PHASE, "token", { seq: 9, step: S.StepPassword, n: 0 }).touched).toBe(false);
  });
  it("is the YubiKey's line alone", () => {
    expect(phaseAfter(NO_PHASE, "recovery", { seq: 1, step: S.StepPassword, n: 1 }).touched).toBe(false);
  });
});

describe("an explicit Unlock", () => {
  const at = (o: Partial<{ state: string; setupNeeded: boolean; hasHardwareSlot: boolean; hasPasswordSlot: boolean }>) => ({ state: "locked", setupNeeded: false, hasHardwareSlot: true, hasPasswordSlot: false, ...o });
  it("begins with the YubiKey, else the password, as the tray does", () => {
    expect(explicitMethod(at({}))).toBe("token");
    expect(explicitMethod(at({ hasHardwareSlot: false, hasPasswordSlot: true }))).toBe("password");
  });
  it("begins nothing where the panel has its own words first", () => {
    expect(explicitMethod(null)).toBe(null);
    expect(explicitMethod(at({ setupNeeded: true }))).toBe(null);
    expect(explicitMethod(at({ state: "unlocking" }))).toBe(null);
    expect(explicitMethod(at({ state: "busy" }))).toBe(null);
    expect(explicitMethod(at({ state: "none" }))).toBe(null);
    expect(explicitMethod(at({ hasHardwareSlot: false, hasPasswordSlot: false }))).toBe(null);
  });
});

describe("an enrolment's two halves", () => {
  const S = CeremonyStep;
  const e = (seq: number, step: CeremonyStep, choose = false, kind = "enroll") => ({ seq, kind, step, choose });
  it("turns at the swap or at the new secret's prompt", () => {
    let h = halvesAfter(NO_HALVES, e(1, S.StepWaitingForKey));
    expect(h.phase).toBe(0);
    h = halvesAfter(h, e(2, S.StepPIN));
    h = halvesAfter(h, e(3, S.StepTouch));
    expect(h.phase).toBe(0);
    h = halvesAfter(h, e(4, S.StepSwapKey));
    expect(h.phase).toBe(1);
    h = halvesAfter(h, e(5, S.StepWaitingForKey));
    expect(h.phase).toBe(1); // the new YubiKey's wait
    h = halvesAfter(h, e(6, S.StepDone));
    expect(h.phase).toBe(PAST);
    expect(halvesAfter(h, e(7, S.StepWaitingForKey)).phase).toBe(0); // the next enrolment
    expect(halvesAfter(halvesAfter(NO_HALVES, e(1, S.StepRecovery)), e(2, S.StepPassword, true)).phase).toBe(1);
  });
  it("is an enrolment's alone", () => {
    expect(halvesAfter(NO_HALVES, e(1, S.StepSwapKey, false, "remove"))).toBe(NO_HALVES);
    expect(halvesAfter({ seq: 3, phase: 1 }, null)).toEqual(NO_HALVES);
  });
});

describe("the ceremony in a dialog", () => {
  const S = CeremonyStep;
  it("heads a typed proof with what to type, and nothing else typed", () => {
    expect(typedProofLeads({ way: "password", shows: "password" })).toBe(true);
    // A password being chosen, or the enrolment's second half, is the line's.
    expect(typedProofLeads({ way: "password", shows: "password", choose: true })).toBe(false);
    expect(typedProofLeads({ way: "password", shows: "password", second: true })).toBe(false);
    // The vault's password beside a YubiKey's PIN heads with the PIN's lead.
    expect(typedProofLeads({ way: "token", shows: "password" })).toBe(false);
    expect(typedProofLeads({ way: "password", shows: "none" })).toBe(false);
  });
  it("holds the act and the way out in the foot band, in that order", () => {
    expect(dialogFoot({ over: false, view: "secret", asks: true, again: true })).toEqual(["continue", "cancel"]);
    expect(dialogFoot({ over: false, view: "insert", asks: false, again: true })).toEqual(["cancel"]);
    // A failure the page can begin again offers Start again, as the lock
    // screen's does; without it, and at Done, Close alone.
    expect(dialogFoot({ over: true, view: "failed", asks: false, again: true })).toEqual(["again", "cancel"]);
    expect(dialogFoot({ over: true, view: "failed", asks: false, again: false })).toEqual(["close"]);
    expect(dialogFoot({ over: true, view: "done", asks: false, again: true })).toEqual(["close"]);
    expect(dialogFoot({ over: true, view: "touch", asks: false, again: true })).toEqual(["close"]);
  });
  const at = (step: CeremonyStep, o: Partial<{ kind: string; error: string; promptId: string; removeLabel: string }> = {}) => ({ kind: "enroll", step, error: "", promptId: "", removeLabel: "", ...o });
  it("proves with a YubiKey where the vault has one, before the core names it", () => {
    expect(dialogWay("", true, at(S.StepWaitingForKey))).toBe("token");
    expect(dialogWay("", false, at(S.StepWaitingForKey))).toBe("");
    // The core's word, then the step's evidence, outrank the guess.
    expect(dialogWay("password", true, at(S.StepWaitingForKey))).toBe("password");
    expect(dialogWay("", true, at(S.StepRecovery, { kind: "records" }))).toBe("recovery");
    expect(dialogWay("", false, at(S.StepPIN))).toBe("token");
  });
  it("draws the lock screen's views", () => {
    expect(dialogViewOf({ c: at(S.StepWaitingForKey), way: "token" })).toBe("insert");
    expect(dialogViewOf({ c: at(S.StepWaitingForKey), way: "password" })).toBe("hold");
    expect(dialogViewOf({ c: at(S.StepPIN, { promptId: "p" }), way: "token" })).toBe("secret");
    expect(dialogViewOf({ c: at(S.StepNoMatch), way: "token" })).toBe("edge");
    expect(dialogViewOf({ c: at(S.StepBlocked), way: "token" })).toBe("blocked");
    expect(dialogViewOf({ c: at(S.StepRecovery, { kind: "records", promptId: "r" }), way: "recovery" })).toBe("recovery");
    // The dialog stands while the ceremony is on it: a failure is said in it.
    expect(dialogViewOf({ c: at(S.StepFailed, { kind: "remove", error: "token.touch" }), way: "token" })).toBe("failed");
    expect(dialogViewOf({ c: at(S.StepFailed, { error: "ceremony.cancelled" }), way: "token" })).toBe("hold");
  });
  // dialogViews plays a dialog's events as CeremonyPanel follows them —
  // the way (dialogWay) and passedAfter — and returns the view each leaves.
  type Ev = { kind?: string; step: CeremonyStep; method?: string; promptId?: string; removeLabel?: string };
  function dialogViews(evs: Ev[], hasHardware = true): string[] {
    let passed = NOT_PASSED;
    let shown: ReturnType<typeof shownAfter> | null = null;
    return evs.map((e) => {
      const c = at(e.step, { kind: e.kind ?? "enroll", promptId: e.promptId ?? "", removeLabel: e.removeLabel ?? "" });
      passed = passedAfter(passed, c);
      const way = dialogWay(e.method ?? "", hasHardware, c);
      shown = shownAfter(shown, dialogViewOf({ c, way, passed: passed.passed, afterTouch: passed.afterTouch }));
      return shown;
    });
  }
  it("turns the box green for the proof's touch, its derivation and a proof's Done", () => {
    expect(
      dialogViews([
        { kind: "rotate", step: S.StepWaitingForKey },
        { kind: "rotate", step: S.StepPIN, promptId: "p" },
        { kind: "rotate", step: S.StepTouch },
        { kind: "rotate", step: S.StepDeriving },
        { kind: "rotate", step: S.StepReleasing },
        { kind: "rotate", step: S.StepDone },
      ]),
    ).toEqual(["insert", "secret", "touch", "touch", "touch", "touch"]);
    // A typed proof is never touched: it derives and ends on the neutral panel.
    expect(
      dialogViews(
        [
          { kind: "rotate", step: S.StepWaitingForKey, method: "password" },
          { kind: "rotate", step: S.StepPassword, method: "password", promptId: "p" },
          { kind: "rotate", step: S.StepDeriving, method: "password" },
          { kind: "rotate", step: S.StepDone, method: "password" },
        ],
        false,
      ),
    ).toEqual(["wait", "secret", "opening", "done"]);
  });
  // The core has the new YubiKey prove itself by its PIN and its touch
  // (keys.go proveKey): the green holds from that touch to Done, as it
  // does after the proof's (ruled 2026-10-17; it used to leave at once).
  it("keeps an enrolment's second-half touch on the green through Done", () => {
    expect(
      dialogViews([
        { step: S.StepWaitingForKey },
        { step: S.StepPIN, promptId: "p" },
        { step: S.StepTouch },
        { step: S.StepDeriving },
        { step: S.StepSwapKey, removeLabel: "YubiKey 5C — desk" },
        { step: S.StepSwapKey },
        { step: S.StepWaitingForKey },
        { step: S.StepPIN, promptId: "n" },
        { step: S.StepDeriving }, // a key made on the new YubiKey, before its touch
        { step: S.StepTouch },
        { step: S.StepDone },
      ]),
    ).toEqual(["insert", "secret", "touch", "touch", "swap", "insert", "insert", "secret", "opening", "touch", "touch"]);
    // After a password's proof, the new YubiKey's touch is the first green.
    expect(
      dialogViews([
        { step: S.StepWaitingForKey, method: "password" },
        { step: S.StepPassword, method: "password", promptId: "p" },
        { step: S.StepDeriving, method: "password" },
        { step: S.StepSwapKey, method: "password" },
        { step: S.StepWaitingForKey, method: "password" },
        { step: S.StepPIN, method: "password", promptId: "n" },
        { step: S.StepTouch, method: "password" },
        { step: S.StepDone, method: "password" },
      ]),
    ).toEqual(["wait", "secret", "opening", "insert", "insert", "secret", "touch", "touch"]);
  });
  it("ends on the neutral panel when something was asked after the proof's touch", () => {
    // A new password chosen after the YubiKey's proof: the green left at
    // the prompt and does not come back for Done.
    expect(
      dialogViews([
        { step: S.StepWaitingForKey },
        { step: S.StepPIN, promptId: "p" },
        { step: S.StepTouch },
        { step: S.StepDeriving },
        { step: S.StepPassword, promptId: "c" },
        { step: S.StepDone },
      ]),
    ).toEqual(["insert", "secret", "touch", "touch", "secret", "done"]);
  });
  it("draws the swap with the key art, and the wait for the new YubiKey as an insert", () => {
    expect(dialogViewOf({ c: at(S.StepSwapKey, { removeLabel: "YubiKey 5C — desk" }), way: "token" })).toBe("swap");
    expect(dialogViewOf({ c: at(S.StepSwapKey), way: "token" })).toBe("insert");
    // After a password's proof the new YubiKey's wait is a real one.
    expect(dialogViewOf({ c: at(S.StepWaitingForKey), way: "password", passed: true })).toBe("insert");
  });
});

// The acts that may re-key the vault in a dialog (APP.md §13, ruled
// 2026-10-27): the password change's views, the replacement's confirmation,
// the end that has more to say, the recovery-key switch, and which acts
// prove first, choose the vault's password, and changed nothing by failing.
describe("the re-keying acts in a dialog", () => {
  const S = CeremonyStep;
  const at = (step: CeremonyStep, o: Partial<{ kind: string; error: string; promptId: string; removeLabel: string }> = {}) => ({ kind: "password", step, error: "", promptId: "", removeLabel: "", ...o });
  type Ev = { kind?: string; step: CeremonyStep; method?: string; promptId?: string };
  function dialogViews(evs: Ev[], hasHardware = true, more = false): string[] {
    let passed = NOT_PASSED;
    let shown: ReturnType<typeof shownAfter> | null = null;
    return evs.map((e) => {
      const c = at(e.step, { kind: e.kind ?? "password", promptId: e.promptId ?? "" });
      passed = passedAfter(passed, c);
      const way = dialogWay(e.method ?? "", hasHardware, c);
      shown = shownAfter(shown, dialogViewOf({ c, way, passed: passed.passed, afterTouch: passed.afterTouch, endsWithMore: more }));
      return shown;
    });
  }
  it("draws a password change proved with a YubiKey as a re-key's, the new password on the neutral panel", () => {
    expect(
      dialogViews([
        { step: S.StepWaitingForKey },
        { step: S.StepPIN, promptId: "p" },
        { step: S.StepTouch },
        { step: S.StepDeriving },
        { step: S.StepPassword, promptId: "c" },
        { step: S.StepDeriving }, // Phase commit: the change and its re-key
        { step: S.StepDone },
      ]),
    ).toEqual(["insert", "secret", "touch", "touch", "secret", "opening", "done"]);
  });
  it("draws a password change proved with a typed password on the neutral panel throughout", () => {
    expect(
      dialogViews(
        [
          { step: S.StepWaitingForKey, method: "password" },
          { step: S.StepPassword, method: "password", promptId: "p" },
          { step: S.StepDeriving, method: "password" },
          { step: S.StepPassword, method: "password", promptId: "c" },
          { step: S.StepDeriving, method: "password" },
          { step: S.StepDone, method: "password" },
        ],
        false,
      ),
    ).toEqual(["wait", "secret", "opening", "secret", "opening", "done"]);
  });
  it("asks the replacement's confirmation in its own view, with Continue and Cancel", () => {
    const c = at(S.StepConfirm, { kind: "replace", promptId: "k3" });
    expect(viewOf({ running: true, firstRun: false, way: "token", c })).toBe("confirm");
    expect(dialogViewOf({ c, way: "token", passed: true })).toBe("confirm");
    expect(dialogFoot({ over: false, view: "confirm", asks: true, again: true })).toEqual(["continue", "cancel"]);
    // The new keys are the recovery key's dialog's to show; the panel waits.
    expect(dialogViewOf({ c: at(S.StepKeepKey, { kind: "replace", promptId: "k1" }), way: "token", passed: true })).toBe("wait");
  });
  it("carries the green from the proof's touch to the confirmation's end only on the neutral panel", () => {
    expect(
      dialogViews([
        { kind: "replace", step: S.StepWaitingForKey },
        { kind: "replace", step: S.StepPIN, promptId: "p" },
        { kind: "replace", step: S.StepTouch },
        { kind: "replace", step: S.StepDeriving },
        { kind: "replace", step: S.StepKeepKey, promptId: "k1" },
        { kind: "replace", step: S.StepConfirm, promptId: "k2" },
        { kind: "replace", step: S.StepDeriving },
        { kind: "replace", step: S.StepDone },
      ]),
    ).toEqual(["insert", "secret", "touch", "touch", "wait", "confirm", "opening", "done"]);
  });
  it("ends on the neutral done view after a touch when the end has more, and on the green without", () => {
    const evs: Ev[] = [
      { kind: "remove", step: S.StepWaitingForKey },
      { kind: "remove", step: S.StepPIN, promptId: "p" },
      { kind: "remove", step: S.StepTouch },
      { kind: "remove", step: S.StepDeriving },
      { kind: "remove", step: S.StepDone },
    ];
    expect(dialogViews(evs)).toEqual(["insert", "secret", "touch", "touch", "touch"]);
    expect(dialogViews(evs, true, true)).toEqual(["insert", "secret", "touch", "touch", "done"]);
  });
  it("says an end has more for a bar, a receipt, a follow-up or the switch's next line, and only at Done", () => {
    const d = { kind: "remove", step: S.StepDone };
    expect(endsWithMore(d)).toBe(false);
    expect(endsWithMore({ ...d, retireError: "io" })).toBe(true);
    expect(endsWithMore({ ...d, backupError: "file.exists" })).toBe(true);
    expect(endsWithMore({ kind: "replace", step: S.StepDone, newIds: ["3F7A-9C21"] })).toBe(true);
    expect(endsWithMore({ kind: "replace", step: S.StepDone, newIds: null })).toBe(false);
    expect(endsWithMore(d, { follow: true })).toBe(true);
    expect(endsWithMore({ kind: "enroll", step: S.StepDone }, { nextLine: true })).toBe(true);
    expect(endsWithMore({ kind: "remove", step: S.StepFailed, retireError: "io" })).toBe(false);
  });
  it("offers Use recovery key at No match only with the switch, and Cancel alone without it", () => {
    expect(dialogFoot({ over: false, view: "edge", asks: false, again: true, recovery: true, step: S.StepNoMatch })).toEqual(["recovery", "cancel"]);
    expect(dialogFoot({ over: false, view: "edge", asks: false, again: true, step: S.StepNoMatch })).toEqual(["cancel"]);
    // The YubiKey in use is not a No match; the wait keeps its own foot.
    expect(dialogFoot({ over: false, view: "edge", asks: false, again: true, recovery: true, step: S.StepBusy })).toEqual(["cancel"]);
    expect(dialogFoot({ over: false, view: "insert", asks: false, again: true, recovery: true, step: S.StepWaitingForKey })).toEqual(["cancel"]);
  });
  it("lists the acts that prove first, choose the vault's password, and changed nothing by failing", () => {
    for (const k of ["enroll", "remove", "rotate", "export", "reveal", "entangle", "password", "records", "replace"]) expect(provesFirst(k), k).toBe(true);
    for (const k of ["unlock", "create", "import", "setup", "verify", ""]) expect(provesFirst(k), k).toBe(false);
    for (const k of ["entangle", "password"]) expect(choosesVaultPassword(k), k).toBe(true);
    for (const k of ["enroll", "remove", "rotate", "replace", "create", "setup"]) expect(choosesVaultPassword(k), k).toBe(false);
    for (const k of ["remove", "password", "entangle", "rotate", "replace"]) {
      expect(nothingChanged({ kind: k, error: "token.touch" }), k).toBe(true);
      expect(nothingChanged({ kind: k, error: "ceremony.cancelled" }), k).toBe(true);
      expect(nothingChanged({ kind: k, error: "vault.indeterminate" }), k).toBe(false);
      expect(nothingChanged({ kind: k, error: "vault.conflict" }), k).toBe(false);
    }
    for (const k of ["enroll", "export", "reveal", "records", "unlock", "verify"]) expect(nothingChanged({ kind: k, error: "token.touch" }), k).toBe(false);
  });
});

// The recovery-key switch at an enrolment (ruled 2026-10-27): one rule for
// the insert view's button and No match's foot, APP.md §6's one exception.
describe("offersRecoverySwitch", () => {
  const vault = [{ type: "hardware" }, { type: "recovery" }];
  const enrol = { kind: "enroll" };
  it("offers it at the enrolment of a YubiKey or a recovery key on a vault with both", () => {
    expect(offersRecoverySwitch(enrol, { act: "enroll", type: "token" }, vault, false)).toBe(true);
    expect(offersRecoverySwitch(enrol, { act: "enroll", type: "recovery" }, vault, false)).toBe(true);
  });
  it("never in the second half, once taken, or for a password", () => {
    expect(offersRecoverySwitch(enrol, { act: "enroll", type: "token" }, vault, true)).toBe(false);
    expect(offersRecoverySwitch(enrol, { act: "enroll", type: "token", withRecoveryKey: true }, vault, false)).toBe(false);
    expect(offersRecoverySwitch(enrol, { act: "enroll", type: "password" }, vault, false)).toBe(false);
  });
  it("never for another act", () => {
    expect(offersRecoverySwitch({ kind: "remove" }, { act: "remove", type: "hardware" }, vault, false)).toBe(false);
    expect(offersRecoverySwitch({ kind: "rotate" }, { act: "rotate" }, vault, false)).toBe(false);
    expect(offersRecoverySwitch({ kind: "replace" }, { act: "replace" }, vault, false)).toBe(false);
    expect(offersRecoverySwitch({ kind: "reveal" }, { act: "enroll", type: "token" }, vault, false)).toBe(false);
  });
  it("never on a vault without a YubiKey or without a recovery key, nor with no act recorded", () => {
    expect(offersRecoverySwitch(enrol, { act: "enroll", type: "token" }, [{ type: "password" }, { type: "recovery" }], false)).toBe(false);
    expect(offersRecoverySwitch(enrol, { act: "enroll", type: "token" }, [{ type: "hardware" }, { type: "hardware" }], false)).toBe(false);
    expect(offersRecoverySwitch(enrol, null, vault, false)).toBe(false);
  });
});

// The lock screen's Finish setting up… while the vault's password is on
// (ruled 2026-10-27): greyed, with the core's refusal as its reason.
describe("finishGreyed", () => {
  it("greys it while setup is needed and the password is on", () => {
    expect(finishGreyed({ setupNeeded: true, entangled: true })).toBe(codeText("vault.setup_password_on"));
  });
  it("leaves it as it was otherwise", () => {
    expect(finishGreyed({ setupNeeded: true, entangled: false })).toBeUndefined();
    expect(finishGreyed({ setupNeeded: false, entangled: true })).toBeUndefined();
    expect(finishGreyed(null)).toBeUndefined();
  });
});

// What a derivation says it does (APP.md §6, ruled 2026-10-17).
describe("derivingOf", () => {
  const S = CeremonyStep;
  it("opens an unlock's and a test's vault, and creates a create's", () => {
    expect(derivingOf("unlock", false)).toBe("opening");
    expect(derivingOf("verify", false)).toBe("opening");
    expect(derivingOf("create", false)).toBe("creating");
    expect(derivingOf("create", true)).toBe("creating");
  });
  it("adds a setup's and an import's first way in after the derivation that opened the vault", () => {
    let p = passedAfter(NOT_PASSED, { kind: "setup", step: S.StepRecovery });
    p = passedAfter(p, { kind: "setup", step: S.StepDeriving });
    expect(derivingOf("setup", p.opened)).toBe("opening");
    // The same derivation said again is still the opening one.
    p = passedAfter(p, { kind: "setup", step: S.StepDeriving });
    expect(p.opened).toBe(false);
    p = passedAfter(p, { kind: "setup", step: S.StepSwapKey });
    p = passedAfter(p, { kind: "setup", step: S.StepTouch });
    p = passedAfter(p, { kind: "setup", step: S.StepDeriving });
    expect(derivingOf("setup", p.opened)).toBe("adding");
    expect(derivingOf("import", true)).toBe("adding");
    // The end forgets it: the next ceremony opens afresh.
    p = passedAfter(p, { kind: "setup", step: S.StepDone });
    expect(p.opened).toBe(false);
  });
});

describe("retiredAt", () => {
  it("dates a retired copy by its name", () => {
    expect(retiredAt("C:/Users/me/AppData/Local/Enfold/vault-replaced-1759276800-1.eks")).toBe(1759276800);
    expect(retiredAt("C:/x/personal.eks")).toBe(0);
    expect(retiredAt("")).toBe(0);
  });
});

describe("the stage's edge states, each where it arises", () => {
  const S = CeremonyStep;
  it("keeps No match, Two keys and Busy on the YubiKey's step and Blocked on the PIN's", () => {
    for (const step of [S.StepNoMatch, S.StepTwoKeys, S.StepBusy, S.StepProbing]) expect(phaseOf("token", step, 0), step).toBe(0);
    expect(phaseOf("token", S.StepBlocked, 0)).toBe(1);
    expect(discs(3, phaseOf("token", S.StepBlocked, 0))).toEqual(["done", "live", "next"]);
  });
  it("draws no step line for first run's flows, a setup, an import or a backup test", () => {
    for (const kind of ["create", "setup", "import", "verify"]) {
      expect(lineKind(kind, "token"), kind).toBe("");
      expect(lineKind(kind, "recovery"), kind).toBe("");
    }
    expect(lineKind("unlock", "recovery")).toBe("recovery");
  });
  it("gives each state its own view, never the rest panel's", () => {
    const f = (step: CeremonyStep, promptId = "") => viewOf({ running: true, firstRun: false, way: "token", c: { kind: "unlock", step, promptId } });
    expect(f(S.StepNoMatch)).toBe("edge");
    expect(f(S.StepTwoKeys)).toBe("insert");
    expect(f(S.StepBlocked)).toBe("blocked");
    expect(viewOf({ running: true, firstRun: false, way: "recovery", c: { kind: "unlock", step: S.StepRecovery, promptId: "rk-1" } })).toBe("recovery");
    expect(viewOf({ running: false, firstRun: true, way: "", c: null })).toBe("firstrun");
  });
});

describe("the key art's LED", () => {
  const S = CeremonyStep;
  it("breathes while listening, holds once found, shows two keys", () => {
    expect(insertLed(S.StepWaitingForKey, false, { step: S.StepWaitingForKey })).toBe("listen");
    expect(insertLed(S.StepWaitingForKey, true, { step: S.StepPIN })).toBe("found");
    expect(insertLed(S.StepTwoKeys, false, { step: S.StepTwoKeys, error: "" })).toBe("two");
    expect(insertLed(S.StepProbing, false, { step: S.StepProbing })).toBe("found");
  });
  it("stops breathing while the Smart Card service is not running", () => {
    expect(insertLed(S.StepWaitingForKey, false, { step: S.StepWaitingForKey, error: "token.no_service" })).toBe("still");
    // Another error at the wait is said on its bar; the LED still listens.
    expect(insertLed(S.StepWaitingForKey, false, { step: S.StepWaitingForKey, error: "token.no_piv" })).toBe("listen");
    // Found wins: the YubiKey answered after all.
    expect(insertLed(S.StepWaitingForKey, true, { step: S.StepWaitingForKey, error: "token.no_service" })).toBe("found");
  });
});

describe("padlockOpen", () => {
  it("is open on arrival and shut once the lock has settled", () => {
    expect(padlockOpen(false, false, null)).toBe(true);
    expect(padlockOpen(true, false, null)).toBe(false);
  });
  it("opens again at an unlock's Done, where the header reads Unlocked", () => {
    expect(padlockOpen(true, true, { kind: "unlock", step: CeremonyStep.StepDone })).toBe(true);
    expect(padlockOpen(true, true, { kind: "unlock", step: CeremonyStep.StepTouch })).toBe(false);
  });
  it("stays shut at the Done of a ceremony that opens nothing", () => {
    for (const kind of ["create", "setup", "verify", "import"]) expect(padlockOpen(true, true, { kind, step: CeremonyStep.StepDone }), kind).toBe(false);
  });
});

describe("pinLeads", () => {
  it("heads a PIN's step with its instruction where no step line names it", () => {
    for (const kind of ["create", "setup", "import", "verify"]) expect(pinLeads(kind, "pin"), kind).toBe(true);
  });
  it("leaves the unlock's PIN step to its step line, and a step that asks no PIN to the act", () => {
    expect(pinLeads("unlock", "pin")).toBe(false);
    expect(pinLeads("unlock", "both")).toBe(false);
    expect(pinLeads("create", "password")).toBe(false);
    expect(pinLeads("create", "none")).toBe(false);
  });
});

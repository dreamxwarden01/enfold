import { describe, expect, it } from "vitest";
import {
  WEEK,
  actEnd,
  afterRecoveryProof,
  backupBar,
  backupOwed,
  backupPossible,
  backupReason,
  backupSavedOnClose,
  backupTicked,
  ceremonyTitleOf,
  chainAfter,
  chainFor,
  digitsLater,
  failedNotes,
  followUp,
  lastProtector,
  pairRows,
  primaryOf,
  questionOf,
  recoveryNoteOf,
  rekeyLimitOf,
  removeRule,
  replaceBackupItems,
  replaceFacts,
  replaceGreyed,
  replaceMinutes,
  replaceNotice,
  replaceResult,
  retireBar,
} from "./rekey";
import type { AfterFacts, Chain, LastAct } from "./rekey";
import { ceremonyCopy, codeText, dialogCopy, replaceCopy, retireHeld, vaultCopy } from "./strings";

// The acts that may re-key the vault (APP.md §13, ruled 2026-10-27): the
// backup taken after the change, the dialogs' primaries, the end's next
// step along one chain, and what a replacement's end means for the keys it
// showed. KeysPage has no component test, so its decisions are pinned here.

const now = 1_760_000_000_000;
const sec = now / 1000;
const day = 86_400;

describe("the question and its limit (questionOf, rekeyLimitOf)", () => {
  const q = dialogCopy.rekeyQuestion;
  const l = dialogCopy.rekeyLimit;
  it("asks a YubiKey and a recovery key whether lost, a password whether known, the switch whether copied", () => {
    expect(questionOf("remove", "hardware")).toEqual({ legend: "Why are you removing this YubiKey?", rekey: "Lost, stolen, or I am not sure", keep: "I still have it and it was never out of my control" });
    expect(questionOf("remove", "recovery")).toEqual({ legend: q.recovery, rekey: q.lost, keep: q.kept });
    expect(questionOf("remove", "password")).toEqual({ legend: q.password, rekey: q.known, keep: q.onlyMe });
    expect(questionOf("password")).toEqual({ legend: "Why are you changing the password?", rekey: "Someone else may know it, or I am not sure", keep: "Only I have ever known it" });
    expect(questionOf("on")).toEqual({ legend: "Could the vault file have been copied before now?", rekey: "Yes, or I am not sure", keep: "No, it was never copied" });
  });
  it("says a YubiKey's and a password's limit, the password first where it is on", () => {
    expect(rekeyLimitOf({ act: "remove", type: "hardware", passwordOn: false, otherLeft: 2 })).toBe(`${l.future} ${l.method("YubiKey")}`);
    expect(rekeyLimitOf({ act: "remove", type: "password", passwordOn: false, otherLeft: 2 })).toBe(`${l.future} ${l.method("password")}`);
    expect(rekeyLimitOf({ act: "remove", type: "hardware", passwordOn: true, otherLeft: 2 })).toBe(`${l.future} ${l.methodOn}`);
  });
  it("says one recovery key left in the singular, every act's line", () => {
    // The acceptance scenario's vault: one YubiKey removed, one recovery key left.
    expect(rekeyLimitOf({ act: "remove", type: "hardware", passwordOn: false, otherLeft: 1 })).toBe(
      `${l.future} If an earlier copy of the vault file may be out there, whoever opened it with this YubiKey could also read the recovery key, which still opens this vault: replace it afterwards with Replace recovery keys… on this page.`,
    );
    expect(rekeyLimitOf({ act: "remove", type: "password", passwordOn: false, otherLeft: 1 })).toBe(`${l.future} ${l.methodOne("password")}`);
    expect(rekeyLimitOf({ act: "remove", type: "hardware", passwordOn: true, otherLeft: 1 })).toBe(`${l.future} ${l.methodOnOne}`);
    expect(rekeyLimitOf({ act: "remove", type: "recovery", passwordOn: false, otherLeft: 1 })).toBe(`${l.future} ${l.recoveryOne}`);
    expect(rekeyLimitOf({ act: "remove", type: "recovery", passwordOn: true, otherLeft: 1 })).toBe(`${l.future} ${l.recoveryOne} Then change the password.`);
    expect(rekeyLimitOf({ act: "password", passwordOn: true, otherLeft: 1 })).toBe(`${l.future} ${l.changeOne}`);
    expect(rekeyLimitOf({ act: "on", passwordOn: false, otherLeft: 1 })).toBe(`${l.future} ${l.onOne}`);
    for (const t of [l.methodOne("YubiKey"), l.methodOnOne, l.recoveryOne, l.changeOne, l.onOne]) expect(t, t).not.toMatch(/recovery keys(?!…)/);
  });
  it("draws the second sentence only while a recovery key is left besides any removed", () => {
    expect(rekeyLimitOf({ act: "remove", type: "hardware", passwordOn: false, otherLeft: 0 })).toBe(l.future);
    expect(rekeyLimitOf({ act: "remove", type: "hardware", passwordOn: true, otherLeft: 0 })).toBe(`${l.future} ${l.methodOnNoRecovery}`);
    expect(rekeyLimitOf({ act: "remove", type: "recovery", passwordOn: true, otherLeft: 0 })).toBe(l.future);
    expect(rekeyLimitOf({ act: "password", passwordOn: true, otherLeft: 0 })).toBe(l.future);
    expect(rekeyLimitOf({ act: "on", passwordOn: false, otherLeft: 0 })).toBe(l.future);
  });
  it("points a recovery key's removal to the replacement, then the password where it is on", () => {
    expect(rekeyLimitOf({ act: "remove", type: "recovery", passwordOn: false, otherLeft: 2 })).toBe(`${l.future} ${l.recovery}`);
    expect(rekeyLimitOf({ act: "remove", type: "recovery", passwordOn: true, otherLeft: 2 })).toBe(`${l.future} ${l.recovery} Then change the password.`);
  });
  it("says the change's and the switch's", () => {
    expect(rekeyLimitOf({ act: "password", passwordOn: true, otherLeft: 2 })).toBe(`${l.future} ${l.change}`);
    expect(rekeyLimitOf({ act: "on", passwordOn: false, otherLeft: 2 })).toBe(`${l.future} ${l.on}`);
  });
});

describe("whether a backup is owed (backupOwed)", () => {
  const owed = (o: Partial<{ lastExportAt: number; vaultRekeyed: boolean; keysReplaced: boolean }>) =>
    backupOwed({ lastExportAt: 0, nowMs: now, vaultRekeyed: false, keysReplaced: false, ...o });
  it("is owed with none recorded here, and when the last is over a week old", () => {
    expect(owed({})).toBe(true);
    expect(owed({ lastExportAt: sec - 8 * day })).toBe(true);
    expect(WEEK).toBe(7 * day);
  });
  it("is not owed for a backup six days old", () => {
    expect(owed({ lastExportAt: sec - 6 * day })).toBe(false);
  });
  it("is owed for a recent backup the vault's re-key or a replacement left behind", () => {
    expect(owed({ lastExportAt: sec - 6 * day, vaultRekeyed: true })).toBe(true);
    expect(owed({ lastExportAt: sec - 6 * day, keysReplaced: true })).toBe(true);
  });
  it("trusts no stamp ahead of the clock", () => {
    expect(owed({ lastExportAt: sec + day })).toBe(true);
  });
});

describe("whether a backup can be taken after the act (backupPossible)", () => {
  it("needs a recovery key left after it", () => {
    expect(backupPossible({ recoveryKeys: 1, removing: "hardware" })).toBe(true);
    expect(backupPossible({ recoveryKeys: 1, removing: "recovery" })).toBe(false);
    expect(backupPossible({ recoveryKeys: 2, removing: "recovery" })).toBe(true);
    expect(backupPossible({ recoveryKeys: 0 })).toBe(false);
    expect(backupPossible({ recoveryKeys: 1 })).toBe(true);
  });
});

describe("the backup's tick (backupTicked)", () => {
  it("is ticked by the re-keying answer whatever the date", () => {
    expect(backupTicked({ owed: false, act: "remove", rekey: true, removing: "hardware" })).toBe(true);
    expect(backupTicked({ owed: false, act: "password", rekey: true })).toBe(true);
    expect(backupTicked({ owed: false, act: "on", rekey: true })).toBe(true);
  });
  it("follows the owed backup on the answer that keeps the vault's key", () => {
    expect(backupTicked({ owed: false, act: "remove", rekey: false, removing: "hardware" })).toBe(false);
    expect(backupTicked({ owed: true, act: "remove", rekey: false, removing: "hardware" })).toBe(true);
    expect(backupTicked({ owed: false, act: "password", rekey: false })).toBe(false);
  });
  it("is ticked at a recovery key's removal on either answer", () => {
    expect(backupTicked({ owed: false, act: "remove", rekey: false, removing: "recovery" })).toBe(true);
    expect(backupTicked({ owed: false, act: "remove", rekey: true, removing: "recovery" })).toBe(true);
  });
  it("is always ticked for a re-key and a replacement", () => {
    expect(backupTicked({ owed: false, act: "rotate", rekey: true })).toBe(true);
    expect(backupTicked({ owed: false, act: "replace", rekey: true })).toBe(true);
  });
});

describe("the reason under the last backup (backupReason)", () => {
  it("says the re-key's on the re-keying answer and for a re-key", () => {
    expect(backupReason({ owed: false, act: "remove", rekey: true, removing: "hardware" })).toBe(dialogCopy.backupAfter.whyRekey);
    expect(backupReason({ owed: false, act: "rotate", rekey: true })).toBe(dialogCopy.backupAfter.whyRekey);
  });
  it("names the recovery key removed, on either answer", () => {
    const why = "Backups made while Paper was an unlock method hold it and open with it; one made afterwards does not.";
    expect(backupReason({ owed: false, act: "remove", rekey: false, removing: "recovery", label: "Paper" })).toBe(why);
    expect(backupReason({ owed: false, act: "remove", rekey: true, removing: "recovery", label: "Paper" })).toBe(why);
  });
  it("says the replacement's, in the singular for one key", () => {
    expect(backupReason({ owed: false, act: "replace", rekey: true, count: 2 })).toBe(dialogCopy.backupAfter.whyReplace);
    expect(backupReason({ owed: false, act: "replace", rekey: true, count: 1 })).toBe(dialogCopy.backupAfter.whyReplaceOne);
  });
  it("says the owed one's, and nothing otherwise", () => {
    expect(backupReason({ owed: true, act: "password", rekey: false })).toBe("A backup is what brings your archives back if this vault is lost.");
    expect(backupReason({ owed: false, act: "password", rekey: false })).toBeUndefined();
  });
});

describe("the dialogs' primaries (primaryOf)", () => {
  it("names what the press does, the backup included, for every act", () => {
    const all = (act: Parameters<typeof primaryOf>[0]) => [primaryOf(act, false, false), primaryOf(act, true, false), primaryOf(act, false, true), primaryOf(act, true, true)];
    expect(all("remove")).toEqual(["Remove unlock method", "Remove and re-key", "Remove, then back up", "Remove, re-key and back up"]);
    expect(all("password")).toEqual(["Change password", "Change and re-key", "Change, then back up", "Change, re-key and back up"]);
    expect(all("on")).toEqual(["Turn on password", "Turn on and re-key", "Turn on, then back up", "Turn on, re-key and back up"]);
    expect(all("rotate")).toEqual(["Re-key vault", "Re-key vault", "Re-key, then back up", "Re-key, then back up"]);
    expect(all("replace")).toEqual(["Replace recovery keys", "Replace recovery keys", "Replace, then back up", "Replace, then back up"]);
  });
  it("says a replacement of one key in the singular", () => {
    expect(primaryOf("replace", true, false, 1)).toBe("Replace recovery key");
    expect(primaryOf("replace", true, false, 2)).toBe("Replace recovery keys");
    expect(primaryOf("replace", true, true, 1)).toBe("Replace, then back up");
  });
  it("never ends a primary on an ellipsis", () => {
    for (const act of ["remove", "password", "on", "rotate", "replace"] as const) {
      for (const p of [primaryOf(act, false, false), primaryOf(act, true, true, 1)]) expect(p.endsWith("…"), p).toBe(false);
    }
  });
});

describe("the last protector (lastProtector)", () => {
  const A = { recipientId: "a", type: "hardware" };
  const B = { recipientId: "b", type: "hardware" };
  const R1 = { recipientId: "r1", type: "recovery" };
  const R2 = { recipientId: "r2", type: "recovery" };
  it("is the only YubiKey beside recovery keys alone", () => {
    expect(lastProtector([A, R1, R2], A)).toBe(true);
  });
  it("is not a YubiKey with another beside it, nor a recovery key", () => {
    expect(lastProtector([A, B, R1], A)).toBe(false);
    expect(lastProtector([A, R1, R2], R1)).toBe(false);
  });
});

// One chain, the password first (the outside review's second reading,
// finding 6): the acts begun from one another's ends.
const after = (recoveryIds: string[] | null, entangled = false, setupNeeded = false): AfterFacts => ({ recoveryIds, entangled, setupNeeded });
const done = (kind: string, o: { rekey?: boolean; after?: AfterFacts | null; newIds?: string[]; retireError?: string } = {}) => ({ kind, step: "done", ...o });
const act = (a: Partial<LastAct> & Pick<LastAct, "act">): LastAct => ({ fromFollow: false, chain: null, ...a });

describe("the chain an act begins in (chainFor) and leaves (chainAfter)", () => {
  const ch: Chain = { id: 3, replaced: ["AAAA-0001"], changed: false };
  it("begins a new chain from the page for an act that re-keys, with the next id", () => {
    expect(chainFor(ch, false, "remove", true, 4)).toEqual({ id: 4, replaced: [], changed: false });
    expect(chainFor(null, false, "replace", true, 1)).toEqual({ id: 1, replaced: [], changed: false });
  });
  it("begins none from the page for an act that does not re-key", () => {
    expect(chainFor(ch, false, "remove", false, 4)).toBeNull();
    expect(chainFor(ch, false, "enroll", false, 4)).toBeNull();
  });
  it("continues the chain of the end whose button began it", () => {
    expect(chainFor(ch, true, "password", true, 4)).toBe(ch);
    expect(chainFor(ch, true, "enroll", false, 4)).toBe(ch);
  });
  it("takes a replacement's new keys into replaced", () => {
    expect(chainAfter(ch, { kind: "replace", step: "done", rekey: true, newIds: ["BBBB-0002"] })).toEqual({ id: 3, replaced: ["AAAA-0001", "BBBB-0002"], changed: false });
  });
  it("marks a re-keying password change or turning-on as changed", () => {
    expect(chainAfter(ch, { kind: "password", step: "done", rekey: true })?.changed).toBe(true);
    expect(chainAfter(ch, { kind: "entangle", step: "done", rekey: true })?.changed).toBe(true);
    expect(chainAfter(ch, { kind: "password", step: "done", rekey: false })).toBe(ch);
  });
  it("is left by a removal, a failure and no chain at all", () => {
    expect(chainAfter(ch, { kind: "remove", step: "done", rekey: true })).toBe(ch);
    expect(chainAfter(ch, { kind: "replace", step: "failed", newIds: ["BBBB-0002"] })).toBe(ch);
    expect(chainAfter(null, { kind: "replace", step: "done", newIds: ["BBBB-0002"] })).toBeNull();
  });
});

describe("the step an act ends at (actEnd)", () => {
  it("is Done for every act, and a recovery key's enrolment's final showing", () => {
    expect(actEnd({ kind: "remove", step: "done" })).toBe(true);
    expect(actEnd({ kind: "enroll", step: "done" })).toBe(true);
    // The core ends a recovery key's enrolment at StepRecovery, no prompt,
    // its committed facts set (keys.go BeginEnroll; finish keeps the step).
    expect(actEnd({ kind: "enroll", step: "recovery", promptId: "", after: after(["AAAA-0001", "AAAA-0002", "AAAA-0003"]) })).toBe(true);
  });
  it("is not a showing that ended nothing, nor any step before the end", () => {
    expect(actEnd({ kind: "reveal", step: "recovery", promptId: "", after: after(["AAAA-0001"]) })).toBe(false);
    expect(actEnd({ kind: "enroll", step: "recovery", promptId: "" })).toBe(false);
    expect(actEnd({ kind: "enroll", step: "recovery", promptId: "p1", after: after(["AAAA-0001"]) })).toBe(false);
    expect(actEnd({ kind: "enroll", step: "deriving", after: after(["AAAA-0001"]) })).toBe(false);
    expect(actEnd({ kind: "remove", step: "failed" })).toBe(false);
  });
});

describe("the end's next step (followUp)", () => {
  const t = ceremonyCopy.followUp;
  const fresh = (): Chain => ({ id: 1, replaced: [], changed: false });
  it("offers the replacement after a re-keying removal of a YubiKey with the password off", () => {
    const a = act({ act: "remove", type: "hardware", rekey: true, chain: fresh() });
    expect(followUp(a, done("remove", { rekey: true, after: after(["AAAA-0001", "AAAA-0002"]) }))).toEqual({ act: "replace", text: t.replace });
    expect(followUp(a, done("remove", { rekey: true, after: after(["AAAA-0001"]) }))).toEqual({ act: "replace", text: t.replaceOne });
  });
  it("offers the password first with it on", () => {
    const a = act({ act: "remove", type: "hardware", rekey: true, chain: fresh() });
    expect(followUp(a, done("remove", { rekey: true, after: after(["AAAA-0001", "AAAA-0002"], true) }))).toEqual({ act: "change", text: t.changeFirst });
    expect(followUp(a, done("remove", { rekey: true, after: after(["AAAA-0001"], true) }))).toEqual({ act: "change", text: t.changeFirstOne });
  });
  it("says the acceptance scenario's one recovery key in the singular", () => {
    // Desk YubiKey removed as lost, one recovery key left (3.4, step 4), then the change (step 5).
    const a = act({ act: "remove", type: "hardware", rekey: true, chain: fresh() });
    expect(followUp(a, done("remove", { rekey: true, after: after(["51D0-77AA"]) }))?.text).toBe("If an earlier copy of the vault file may be out there, replace the recovery key too.");
    expect(followUp(a, done("remove", { rekey: true, after: after(["51D0-77AA"], true) }))?.text).toBe("If an earlier copy of the vault file may be out there, change the password, then replace the recovery key.");
    const change = act({ act: "password", rekey: true, fromFollow: true, chain: fresh() });
    expect(followUp(change, done("password", { rekey: true, after: after(["51D0-77AA"], true) }))?.text).toBe("If an earlier copy of the vault file may be out there, replace the recovery key too.");
  });
  it("offers to add an unlock method first where the removal left recovery keys alone", () => {
    const a = act({ act: "remove", type: "hardware", rekey: true, chain: fresh() });
    expect(followUp(a, done("remove", { rekey: true, after: after(["AAAA-0001", "AAAA-0002"], false, true) }))).toEqual({ act: "add", text: t.addFirst });
    expect(followUp(a, done("remove", { rekey: true, after: after(["AAAA-0001", "AAAA-0002"], true, true) }))).toEqual({ act: "add", text: t.addFirstOn });
  });
  it("says the other recovery keys after a recovery key's removal, one of them in the singular", () => {
    const a = act({ act: "remove", type: "recovery", rekey: true, chain: fresh() });
    expect(followUp(a, done("remove", { rekey: true, after: after(["AAAA-0002", "AAAA-0003"]) }))).toEqual({ act: "replace", text: t.replaceRecovery });
    expect(followUp(a, done("remove", { rekey: true, after: after(["AAAA-0002", "AAAA-0003"], true) }))).toEqual({ act: "change", text: t.changeFirstRecovery });
    expect(followUp(a, done("remove", { rekey: true, after: after(["AAAA-0002"]) }))).toEqual({ act: "replace", text: t.replaceRecoveryOne });
    expect(followUp(a, done("remove", { rekey: true, after: after(["AAAA-0002"], true) }))).toEqual({ act: "change", text: t.changeFirstRecoveryOne });
  });
  it("goes on after an enrolment begun from a follow-up, and says nothing after one begun from the page", () => {
    // The vault was left with recovery keys alone, so at least 2 of them.
    const ch = fresh();
    expect(followUp(act({ act: "enroll", type: "token", fromFollow: true, chain: ch }), done("enroll", { after: after(["AAAA-0001", "AAAA-0002"], true) }))).toEqual({ act: "change", text: t.changeFirst });
    expect(followUp(act({ act: "enroll", type: "token", fromFollow: true, chain: ch }), done("enroll", { after: after(["AAAA-0001", "AAAA-0002"]) }))).toEqual({ act: "replace", text: t.replace });
    expect(followUp(act({ act: "enroll", type: "token" }), done("enroll", { after: after(["AAAA-0001", "AAAA-0002"], true) }))).toBeNull();
  });
  it("offers the replacement after a re-keying password change and a re-keying turn-on", () => {
    expect(followUp(act({ act: "password", rekey: true, chain: fresh() }), done("password", { rekey: true, after: after(["AAAA-0001", "AAAA-0002"], true) }))).toEqual({ act: "replace", text: t.replace });
    expect(followUp(act({ act: "on", rekey: true, chain: fresh() }), done("entangle", { rekey: true, after: after(["AAAA-0001", "AAAA-0002"], true) }))).toEqual({ act: "replace", text: t.replace });
    expect(followUp(act({ act: "password", rekey: true, chain: fresh() }), done("password", { rekey: true, after: after(["AAAA-0001"], true) }))).toEqual({ act: "replace", text: t.replaceOne });
    expect(followUp(act({ act: "on", rekey: true, chain: fresh() }), done("entangle", { rekey: true, after: after(["AAAA-0001"], true) }))).toEqual({ act: "replace", text: t.replaceOne });
  });
  it("never loops in one chain: a replacement then a change", () => {
    // The replacement, begun from the page, then the change its end offered.
    let ch: Chain | null = chainFor(null, false, "replace", true, 1);
    const replace = done("replace", { rekey: true, newIds: ["NEW1-0001"], after: after(["NEW1-0001"], true) });
    ch = chainAfter(ch, replace);
    expect(followUp(act({ act: "replace", rekey: true, chain: ch }), replace)).toEqual({ act: "change", text: t.changeAfterReplace });
    ch = chainFor(ch, true, "password", true, 2);
    const change = done("password", { rekey: true, after: after(["NEW1-0001"], true) });
    ch = chainAfter(ch, change);
    // replaced holds every recovery ID the change's end has.
    expect(followUp(act({ act: "password", rekey: true, fromFollow: true, chain: ch }), change)).toBeNull();
  });
  it("never loops in one chain: a change then a replacement", () => {
    let ch: Chain | null = chainFor(null, false, "password", true, 1);
    const change = done("password", { rekey: true, after: after(["OLD1-0001"], true) });
    ch = chainAfter(ch, change);
    expect(followUp(act({ act: "password", rekey: true, chain: ch }), change)).toEqual({ act: "replace", text: t.replaceOne });
    ch = chainFor(ch, true, "replace", true, 2);
    const replace = done("replace", { rekey: true, newIds: ["NEW1-0001"], after: after(["NEW1-0001"], true) });
    ch = chainAfter(ch, replace);
    expect(ch?.changed).toBe(true);
    expect(followUp(act({ act: "replace", rekey: true, fromFollow: true, chain: ch }), replace)).toBeNull();
  });
  it("offers the password after a replacement begun from the head with it on, and nothing with it off", () => {
    expect(followUp(act({ act: "replace", rekey: true, chain: fresh() }), done("replace", { rekey: true, after: after(["NEW1-0001"], true) }))).toEqual({ act: "change", text: t.changeAfterReplace });
    expect(followUp(act({ act: "replace", rekey: true, chain: fresh() }), done("replace", { rekey: true, after: after(["NEW1-0001"]) }))).toBeNull();
  });
  it("gives the whole advice again for a later compromise, once the chain has gone", () => {
    // A chain that replaced the keys, dropped at a lock; then a re-keying
    // change begun from its card, the same recovery IDs in after.
    const later = chainFor(null, false, "password", true, 2);
    const change = done("password", { rekey: true, after: after(["NEW1-0001", "NEW2-0002"], true) });
    expect(followUp(act({ act: "password", rekey: true, chain: chainAfter(later, change) }), change)).toEqual({ act: "replace", text: t.replace });
  });
  it("says nothing without the committed facts, after a kept removal, a failure or a turn-on without a re-key", () => {
    const a = act({ act: "remove", type: "hardware", rekey: true, chain: fresh() });
    expect(followUp(a, done("remove", { rekey: true }))).toBeNull();
    expect(followUp(a, done("remove", { rekey: true, after: null }))).toBeNull();
    expect(followUp(act({ act: "remove", type: "hardware", rekey: false }), done("remove", { rekey: false, after: after(["AAAA-0001"], true) }))).toBeNull();
    expect(followUp(a, { kind: "remove", step: "failed", rekey: true, after: after(["AAAA-0001"]) })).toBeNull();
    expect(followUp(act({ act: "on", rekey: false }), done("entangle", { rekey: false, after: after(["AAAA-0001"], true) }))).toBeNull();
    expect(followUp(act({ act: "rotate", rekey: true, chain: fresh() }), done("rotate", { after: after(["AAAA-0001"], true) }))).toBeNull();
    expect(followUp(null, done("remove", { rekey: true, after: after(["AAAA-0001"]) }))).toBeNull();
  });
  it("says nothing where no recovery key is left, and for a recovery key's removal from a vault of recovery keys alone", () => {
    expect(followUp(act({ act: "remove", type: "recovery", rekey: true, chain: fresh() }), done("remove", { rekey: true, after: after([]) }))).toBeNull();
    expect(followUp(act({ act: "remove", type: "recovery", rekey: true, chain: fresh() }), done("remove", { rekey: true, after: after(["AAAA-0002", "AAAA-0003"], false, true) }))).toBeNull();
  });
  it("offers to add only after a YubiKey's removal: a password's that leaves recovery keys alone says nothing", () => {
    // A password and two recovery keys; the password removed with a re-key.
    const a = act({ act: "remove", type: "password", rekey: true, chain: fresh() });
    expect(followUp(a, done("remove", { rekey: true, after: after(["AAAA-0001", "AAAA-0002"], false, true) }))).toBeNull();
  });
  it("answers a vault left with recovery keys alone first: add again, never change or replace", () => {
    const ch = fresh();
    // A recovery key added from the "add" button leaves the vault so still.
    // Its enrolment ends where the core ends it: the new key's showing, no
    // prompt, the committed facts set (never Done while the vault is open).
    const recoveryEnd = { kind: "enroll", step: "recovery", promptId: "", after: after(["AAAA-0001", "AAAA-0002", "AAAA-0003"], true, true) };
    expect(followUp(act({ act: "enroll", type: "recovery", fromFollow: true, chain: ch }), recoveryEnd)).toEqual({ act: "add", text: t.addFirstOn });
    expect(followUp(act({ act: "enroll", type: "recovery", fromFollow: true, chain: ch }), { ...recoveryEnd, after: after(["AAAA-0001", "AAAA-0002", "AAAA-0003"], false, true) })).toEqual({ act: "add", text: t.addFirst });
    // Not before that end.
    expect(followUp(act({ act: "enroll", type: "recovery", fromFollow: true, chain: ch }), { ...recoveryEnd, step: "deriving" })).toBeNull();
    // The same enrolment begun from the page offers nothing: the Vault page's own line says to add one.
    expect(followUp(act({ act: "enroll", type: "recovery", chain: null }), recoveryEnd)).toBeNull();
    expect(followUp(act({ act: "password", rekey: true, chain: ch }), done("password", { rekey: true, after: after(["AAAA-0001", "AAAA-0002"], true, true) }))).toBeNull();
  });
  it("is only the act LastAct names: turning the password off is not its turn-on", () => {
    expect(followUp(act({ act: "on", rekey: true, chain: fresh() }), done("remove", { rekey: true, after: after(["AAAA-0001"]) }))).toBeNull();
    expect(followUp(act({ act: "remove", type: "hardware", rekey: true, chain: fresh() }), done("entangle", { rekey: false, after: after(["AAAA-0001"]) }))).toBeNull();
  });
  it("says the press retries the clearing first while the retirement is owed", () => {
    const a = act({ act: "remove", type: "hardware", rekey: true, chain: fresh() });
    expect(followUp(a, done("remove", { rekey: true, after: after(["AAAA-0001", "AAAA-0002"]), retireError: "io" }))).toEqual({ act: "replace", text: t.replace + t.retryFirst });
    expect(followUp(a, done("remove", { rekey: true, after: after(["AAAA-0001"]), retireError: "io" }))).toEqual({ act: "replace", text: t.replaceOne + t.retryFirst });
  });
  it("reads a core's null list of recovery keys as none", () => {
    expect(followUp(act({ act: "password", rekey: true, chain: fresh() }), done("password", { rekey: true, after: after(null, true) }))).toBeNull();
  });
});

describe("Replace recovery keys… greyed (replaceGreyed)", () => {
  const f = { unlocked: true, tampered: false, recoveryKeys: 2, setupNeeded: false };
  it("says why, each reason in its turn", () => {
    expect(replaceGreyed({ ...f, unlocked: false })).toBe(codeText("vault.needs_unlock"));
    expect(replaceGreyed({ ...f, tampered: true })).toBe(vaultCopy.changesOff);
    expect(replaceGreyed({ ...f, recoveryKeys: 0 })).toBe(vaultCopy.noRecoveryToReplace);
    expect(replaceGreyed({ ...f, setupNeeded: true })).toBe(vaultCopy.replaceNeedsProtector);
    expect(replaceGreyed(f)).toBeUndefined();
  });
  it("says its own words, not the setup's", () => {
    expect(vaultCopy.replaceNeedsProtector).not.toBe(codeText("vault.setup_needed"));
  });
});

describe("what the replacement's dialog reads (replaceFacts)", () => {
  it("reads a follow-up's committed facts and the chain, though the store's differ", () => {
    const from = { after: after(["AAAA-0001"], true), changed: true };
    expect(replaceFacts({ from, recoveryKeys: 3, entangled: false })).toEqual({ count: 1, passwordOn: true, changed: true });
  });
  it("reads the store from the page, a new chain beginning with it", () => {
    expect(replaceFacts({ recoveryKeys: 2, entangled: true })).toEqual({ count: 2, passwordOn: true, changed: false });
  });
});

describe("the idle time the replacement's ready line names (replaceMinutes)", () => {
  // The status's idleEffectiveMinutes, the core's clamp already applied
  // (settings.go timeouts), so that the line promises the time the session
  // gives. The setting as stored is not the same number: an imported vault
  // that stores 60 runs on 10, and its status says 10.
  it("names the minutes the session runs on, as the status gives them", () => {
    expect(replaceMinutes({ idleEffectiveMinutes: 2 })).toBe(2);
    expect(replaceMinutes({ idleEffectiveMinutes: 30 })).toBe(30);
    const stored60 = { idleMinutes: 60, idleEffectiveMinutes: 10 };
    expect(replaceMinutes(stored60)).toBe(10);
    expect(dialogCopy.replaceRecoveryKeys.ready(replaceMinutes(stored60), 2)).toContain("Enfold locks after 10 minutes without input");
  });
  it("falls back to the default only while the status names no number", () => {
    expect(replaceMinutes({ idleEffectiveMinutes: 0 })).toBe(10);
    expect(replaceMinutes(null)).toBe(10);
    expect(replaceMinutes(undefined)).toBe(10);
  });
});

describe("the recovery view's note in a dialog (recoveryNoteOf)", () => {
  const n = ceremonyCopy.recoveryNote;
  it("says the removal's when the YubiKey removed is left out", () => {
    expect(recoveryNoteOf({ kind: "remove", without: "a1b2" }, act({ act: "remove", type: "hardware", rekey: true }), 2)).toBe(n.remove);
  });
  it("says the switch's for an enrolment begun with it, by the kind it adds", () => {
    expect(recoveryNoteOf({ kind: "enroll" }, act({ act: "enroll", type: "token", withRecoveryKey: true }), 1)).toBe(n.enroll);
    expect(recoveryNoteOf({ kind: "enroll" }, act({ act: "enroll", type: "recovery", withRecoveryKey: true }), 1)).toBe(n.enrollRecovery);
  });
  it("says a vault of recovery keys alone proves with one, with what comes next for an enrolment", () => {
    expect(recoveryNoteOf({ kind: "enroll" }, act({ act: "enroll", type: "password" }), 2)).toBe(n.only + n.nextMethod);
    expect(recoveryNoteOf({ kind: "remove" }, act({ act: "remove", type: "recovery", rekey: true }), 3)).toBe(n.only);
    expect(recoveryNoteOf({ kind: "replace" }, null, 2)).toBe(n.only);
  });
  it("says a vault of one recovery key, never set up, in the singular", () => {
    expect(recoveryNoteOf({ kind: "enroll" }, act({ act: "enroll", type: "token" }), 1)).toBe("This vault has only its recovery key, so it proves it is you. The new unlock method comes next.");
    expect(recoveryNoteOf({ kind: "rotate" }, null, 1)).toBe(n.onlyOne);
  });
  it("keeps the notes of the kinds that always ask a recovery key", () => {
    expect(recoveryNoteOf({ kind: "verify" }, null, 1)).toBe("The backup's recovery key. Nothing here changes.");
    expect(recoveryNoteOf({ kind: "import" }, null, 1)).toBe(n.import);
    expect(recoveryNoteOf({ kind: "records" }, null, 1)).toBe(n.records);
  });
  it("never says no YubiKey is at hand for an enrolment that did not say so", () => {
    expect(recoveryNoteOf({ kind: "enroll" }, act({ act: "enroll", type: "password" }), 2)).not.toMatch(/at hand/);
  });
});

describe("what a replacement's end means for the keys it showed (replaceResult)", () => {
  const r = (step: string, error = "", newIds: string[] | null = ["NEW1-0001"]) => replaceResult({ kind: "replace", step, error, newIds });
  it("is replaced at Done", () => {
    expect(r("done")).toBe("replaced");
  });
  it("is never added after a cancel once keys were shown", () => {
    expect(r("failed", "ceremony.cancelled")).toBe("notAdded");
    expect(r("failed", "io")).toBe("notAdded");
  });
  it("is unknown for a save that could not be confirmed, and for a conflict, never not added", () => {
    expect(r("failed", "vault.indeterminate")).toBe("unknown");
    // whether the conflict was the commit's or the retirement's
    expect(r("failed", "vault.conflict")).toBe("unknown");
  });
  it("is nothing before any key was shown, while it runs, and for another act", () => {
    expect(r("failed", "ceremony.cancelled", [])).toBeNull();
    expect(r("failed", "ceremony.cancelled", null)).toBeNull();
    expect(r("keep_key")).toBeNull();
    expect(replaceResult({ kind: "remove", step: "done" })).toBeNull();
  });
});

describe("the Vault page's notice about the keys a replacement showed (replaceNotice)", () => {
  const ids = ["NEW1-0001", "NEW2-0002"];
  const failed = (error: string) => ({ kind: "replace", step: "failed", error, newIds: ids });
  it("is raised as known by a never-added end and as not known by an unknown one", () => {
    expect(replaceNotice(null, failed("ceremony.cancelled"))).toEqual({ ids, known: true });
    expect(replaceNotice(null, failed("vault.conflict"))).toEqual({ ids, known: false });
  });
  it("is cleared at Done and at a new replacement's first event", () => {
    const n = { ids, known: true };
    expect(replaceNotice(n, { kind: "replace", step: "done", newIds: ["NEW3-0003"] })).toBeNull();
    expect(replaceNotice(n, { kind: "replace", step: "waiting_for_key" })).toBeNull();
  });
  it("is kept across unrelated events", () => {
    const n = { ids, known: true };
    expect(replaceNotice(n, { kind: "remove", step: "done" })).toBe(n);
    expect(replaceNotice(n, null)).toBe(n);
    expect(replaceNotice(n, { kind: "replace", step: "failed", error: "ceremony.cancelled" })).toBe(n);
  });
  it("settles an unknown one by the recovery keys listed after the reopen", () => {
    const n = { ids, known: false };
    expect(replaceNotice(n, null, ["NEW1-0001", "NEW2-0002"])).toBeNull();
    expect(replaceNotice(n, null, ["OLD1-0001", "OLD2-0002"])).toEqual({ ids, known: true });
    // A listing that names some and not others says nothing either way.
    expect(replaceNotice(n, null, ["NEW1-0001", "OLD2-0002"])).toBe(n);
    // A known one is not unsettled by a listing.
    const k = { ids, known: true };
    expect(replaceNotice(k, null, ids)).toBe(k);
  });
});

describe("a replacement's receipt and its end (pairRows, replaceBackupItems)", () => {
  const slots = [
    { recoveryId: "51D0-77AA", label: "Safe" },
    { recoveryId: "0B44-1E07", label: "Office" },
  ];
  it("pairs each new key with the one it replaces, under the name it takes", () => {
    expect(pairRows({ oldIds: ["51D0-77AA", "0B44-1E07"], newIds: ["3F7A-9C21", "9E10-4D3B"] }, slots)).toEqual([
      { label: "Safe", text: "3F7A-9C21 replaces 51D0-77AA" },
      { label: "Office", text: "9E10-4D3B replaces 0B44-1E07" },
    ]);
    // Read again after the act: the cards then hold the new IDs, the names kept.
    expect(pairRows({ oldIds: ["AAAA-0001"], newIds: ["51D0-77AA"] }, slots)[0].label).toBe("Safe");
    expect(pairRows({ oldIds: null, newIds: null }, slots)).toEqual([]);
  });
  it("says to destroy the replaced sheets once the new backup opens, when it was written", () => {
    expect(replaceBackupItems({ kind: "replace", backedUp: true, keepCount: 2 })).toEqual([{ tone: "plain", text: replaceCopy.backedUp }]);
    expect(replaceBackupItems({ kind: "replace", backedUp: true, keepCount: 1 })).toEqual([{ tone: "plain", text: replaceCopy.backedUpOne }]);
  });
  it("bars that no backup holds the new keys when none was asked for", () => {
    expect(replaceBackupItems({ kind: "replace", keepCount: 2 })).toEqual([{ tone: "attention", text: replaceCopy.noBackup }]);
    expect(replaceBackupItems({ kind: "replace", keepCount: 1 })).toEqual([{ tone: "attention", text: replaceCopy.noBackupOne }]);
  });
  it("says why a backup failed, then when to destroy the replaced sheets", () => {
    expect(replaceBackupItems({ kind: "replace", backupError: "file.exists", keepCount: 1 })).toEqual([
      { tone: "attention", text: ceremonyCopy.backupNotSaved(codeText("file.exists")) },
      { tone: "plain", text: replaceCopy.afterFailedBackupOne },
    ]);
  });
  it("leaves a backup the retirement held back to the retirement's bar, then says none holds the new keys", () => {
    expect(replaceBackupItems({ kind: "replace", backupError: "vault.retire_owed", keepCount: 2 })).toEqual([{ tone: "attention", text: replaceCopy.noBackup }]);
  });
  it("says nothing of the kind for another act", () => {
    expect(replaceBackupItems({ kind: "remove", backedUp: true })).toEqual([]);
  });
});

describe("a failed act's captions (failedNotes)", () => {
  it("says nothing was changed for the acts whose failure leaves the file as it was", () => {
    for (const kind of ["remove", "password", "entangle", "rotate"]) expect(failedNotes({ kind, step: "failed", error: "token.touch" }), kind).toEqual(["Nothing was changed."]);
    expect(failedNotes({ kind: "remove", step: "failed", error: "vault.indeterminate" })).toEqual([]);
    expect(failedNotes({ kind: "enroll", step: "failed", error: "token.touch" })).toEqual([]);
    expect(failedNotes({ kind: "remove", step: "done" })).toEqual([]);
  });
  it("adds what is known of the keys a replacement showed, singular for one", () => {
    expect(failedNotes({ kind: "replace", step: "failed", error: "io", newIds: ["A", "B"] })).toEqual(["Nothing was changed.", ceremonyCopy.keysNeverAdded]);
    expect(failedNotes({ kind: "replace", step: "failed", error: "io", newIds: ["A"] })).toEqual(["Nothing was changed.", ceremonyCopy.keysNeverAddedOne]);
    expect(failedNotes({ kind: "replace", step: "failed", error: "vault.conflict", newIds: ["A", "B"] })).toEqual([ceremonyCopy.keysWorkIfListed]);
    expect(failedNotes({ kind: "replace", step: "failed", error: "vault.indeterminate", newIds: ["A"] })).toEqual([ceremonyCopy.keysWorkIfListedOne]);
    expect(failedNotes({ kind: "replace", step: "failed", error: "token.touch", newIds: null })).toEqual(["Nothing was changed."]);
  });
});

describe("the end's bars (retireBar, backupBar)", () => {
  it("says what the file still holds, why, what waits, and the backup too when it waits", () => {
    const bar = retireBar({ kind: "remove", rekey: true, retireError: "io", backupError: "vault.retire_owed" });
    expect(bar).toBe(
      `The vault file still holds its state from before this change: a copy of it taken now still opens with the unlock method removed. ${codeText("io")} Until Enfold has cleared it, ${retireHeld} The backup you asked for waits too.`,
    );
    expect(retireBar({ kind: "remove", rekey: true })).toBeUndefined();
  });
  it("bars a backup not saved, but not one the retirement held back, nor a replacement's", () => {
    expect(backupBar({ kind: "remove", backupError: "io" })).toBe(ceremonyCopy.backupNotSaved(codeText("io")));
    expect(backupBar({ kind: "remove", backupError: "vault.retire_owed" })).toBeUndefined();
    expect(backupBar({ kind: "replace", backupError: "io" })).toBeUndefined();
    expect(backupBar({ kind: "remove" })).toBeUndefined();
  });
});

describe("the small decisions of the page", () => {
  it("says the lost YubiKey comes next only after an enrolment of a YubiKey begun with the switch", () => {
    const d = { kind: "enroll", step: "done" };
    expect(afterRecoveryProof(d, act({ act: "enroll", type: "token", withRecoveryKey: true }))).toBe(true);
    expect(afterRecoveryProof(d, act({ act: "enroll", type: "token" }))).toBe(false);
    expect(afterRecoveryProof(d, act({ act: "enroll", type: "recovery", withRecoveryKey: true }))).toBe(false);
    expect(afterRecoveryProof({ kind: "enroll", step: "failed" }, act({ act: "enroll", type: "token", withRecoveryKey: true }))).toBe(false);
  });
  it("says when a recovery key added with the switch can be shown again", () => {
    expect(digitsLater(act({ act: "enroll", type: "recovery", withRecoveryKey: true }))).toBe(true);
    expect(digitsLater(act({ act: "enroll", type: "recovery" }))).toBe(false);
    expect(digitsLater(null)).toBe(false);
  });
  it("shows the backup's done line on close after an export and after an act that wrote one", () => {
    expect(backupSavedOnClose({ kind: "export", step: "done" })).toBe(true);
    expect(backupSavedOnClose({ kind: "remove", step: "done", backedUp: true })).toBe(true);
    expect(backupSavedOnClose({ kind: "remove", step: "done", backedUp: false })).toBe(false);
    expect(backupSavedOnClose({ kind: "export", step: "failed" })).toBe(false);
    expect(backupSavedOnClose(null)).toBe(false);
  });
  it("titles the ceremony dialog by its act, a replacement of one key in the singular", () => {
    expect(ceremonyTitleOf({ kind: "password" }, 2)).toBe("Change the password for every YubiKey");
    expect(ceremonyTitleOf({ kind: "replace", keepCount: 2 }, 1)).toBe("Replace the recovery keys");
    expect(ceremonyTitleOf({ kind: "replace", keepCount: 0 }, 1)).toBe("Replace the recovery key");
    expect(ceremonyTitleOf({ kind: "replace", keepCount: 1 }, 3)).toBe("Replace the recovery key");
    // A new kind is never titled as another act.
    expect(ceremonyTitleOf({ kind: "remove" }, 1)).toBe("Remove an unlock method");
  });
  it("says what to add first on a YubiKey's greyed Remove…, and the rule on the others", () => {
    expect(removeRule("hardware")).toBe(vaultCopy.ruleYubiKey);
    expect(removeRule("recovery")).toBe(vaultCopy.rule);
    expect(removeRule("password")).toBe(vaultCopy.rule);
  });
});

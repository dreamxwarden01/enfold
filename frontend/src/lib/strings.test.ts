import { describe, expect, it } from "vitest";
import { CeremonyStep, Code } from "../../bindings/github.com/dreamxwarden01/enfold/internal/app";
import * as strings from "./strings";
import * as validate from "./validate";
import { archivePageCopy, archivesCopy, bootCopy, leadText, tamperedLead, ceremonyCopy, chipCopy, closeCopy, codeCopy, codeText, collisionCopy, commandCopy, compareCopy, compressedText, conflictCopy, refusedCopy, lockedOpenText, dialogCopy, doneTitleOf, extensionNames, extractCopy, importCopy, lockCopy, phaseLeadOf, purgedText, reclaimedText, replaceCopy, retireHeld, retireLeadOf, retriesText, revealCopy, sinceText, stepCopy, tamperedCopy, technicalCopy, typeLabel, unknownCodeParts, vaultCopy, warningCopy } from "./strings";

// ---- One word per thing (APP.md §7, ruled 2026-10-17) -------------------
//
// The format's words never reach a main surface: they stand in
// technicalCopy (an archive's Details, "Why?", "How this works") and nowhere
// else. Each banned term carries the word the screen uses instead.
const banned: [RegExp, string][] = [
  [/\bkeystores?\b/i, "the file is the vault"],
  [/\bKeys page\b/i, "the page is Vault"],
  [/\bslots?\b/i, "an unlock method"],
  [/\bways? (in|into|to unlock)\b/i, "an unlock method"],
  [/\bentangle(d|ment)?\b/i, "Password with every YubiKey"],
  [/\bsuperblocks?\b/i, "the Details dialog only"],
  [/\bregistry\b/i, "the Details dialog only"],
  [/\bKIDs?\b/, "the Details dialog only"],
  [/\bVMKs?\b/, "the Details dialog only"],
  [/\bsession keys?\b/i, "Opening the vault…"],
  [/\bgenerations?\b/i, "the Details dialog only"],
  [/\bseq\b/, "the Details dialog only"],
  [/\bzstd\b/i, "Compressed"],
  [/\benvelopes?\b/i, "the Details dialog only"],
  [/\brotat(e|es|ed|ing|ion)\b/i, "Re-key"],
  [/\benrol(l|ls|led|ling|ment)?\b/i, "add an unlock method"],
  [/\bImport records\b/i, "Import archives from a backup…"],
  [/\bImport a (copy|backup)\b/i, "Replace this vault with a copy…"],
  [/\bForget key\b/i, "Forget archive…"],
  [/\bCheck files\b/i, "Find missing files"],
  [/\bCheck (a|that a) backup\b/i, "Inspect a backup… / Test that a backup opens…"],
  // The archive's hash checks nothing inside the file (direction 9).
  [/\b(Verify|Verifying|Last verified)\b/, "Refresh fingerprint / Fingerprint"],
  // The archive row's dialog is *Details…* (ruled 2026-10-26, the user's).
  [/\bTechnical details\b/i, "Details…"],
];

// A word that must stay where it is, with the line it stays in and why.
const exceptions: { term: RegExp; line: string; why: string }[] = [
  {
    term: /\bslots?\b/i,
    line: codeCopy[Code.CodeTokenOccupied],
    why: "a PIV slot on the YubiKey, the key's own word as Yubico's tools show it, not the vault's",
  },
  {
    term: /\bslots?\b/i,
    line: codeCopy[Code.CodeTokenFull],
    why: "a PIV slot on the YubiKey, the key's own word as Yubico's tools show it, not the vault's",
  },
];

// Arguments a copy function is tried with; one that throws on a shape is
// tried with the next.
const samples: unknown[][] = [
  [],
  ["Photos 2024"],
  [3],
  [1],
  [3, "Photos 2024"],
  ["Photos 2024", true],
  [["a.efd", "b.efd"]],
  [{ replace: 1, both: 1, skip: 1 }],
  [{ retries: 3, retriesKnown: true, verified: false }],
  [Code.CodeVaultTampered, Code.CodeTamperedGeneration],
  ["raw", 0],
  ["deflate", 71],
  [1_760_000_000, 1_760_100_000_000],
  // A recovery key's branches and the singular for one: a type of
  // "recovery", a list of one ID, and a count of one after a number.
  ["Paper", "recovery"],
  [["3F7A-9C21"]],
  [48, 1],
];

function lines(path: string, v: unknown, out: [string, string][], depth = 0): void {
  if (depth > 4 || v === null || v === undefined) return;
  if (typeof v === "string") {
    out.push([path, v]);
  } else if (typeof v === "function") {
    for (const args of samples) {
      try {
        lines(`${path}(${args.length ? "…" : ""})`, (v as (...a: unknown[]) => unknown)(...args), out, depth + 1);
      } catch {
        // a shape this function does not take
      }
    }
  } else if (Array.isArray(v)) {
    v.forEach((x, i) => lines(`${path}[${i}]`, x, out, depth + 1));
  } else if (typeof v === "object") {
    for (const [k, x] of Object.entries(v as Record<string, unknown>)) lines(`${path}.${k}`, x, out, depth + 1);
  }
}

function everyLine(): [string, string][] {
  const out: [string, string][] = [];
  for (const [name, v] of Object.entries(strings)) {
    if (name === "technicalCopy") continue; // the second register
    lines(name, v, out);
  }
  for (const [name, v] of Object.entries(validate)) lines(`validate.${name}`, v, out);
  for (const code of Object.values(Code)) if (code) out.push([`codeText(${code})`, codeText(code)]);
  for (const reason of [undefined, Code.CodeTamperedHash, Code.CodeTamperedGeneration]) {
    for (const w of [Code.CodeVaultTampered, Code.CodeVaultStale, Code.CodeTokenReset]) out.push([`warningCopy(${w}, ${reason})`, warningCopy(w, reason)]);
  }
  for (const step of Object.values(CeremonyStep)) if (step) out.push([`stepText(${step})`, strings.stepText(step).title + " " + strings.stepText(step).body]);
  return out;
}

describe("one word per thing", () => {
  it("keeps the format's words off every main surface", () => {
    const hits: string[] = [];
    for (const [path, text] of everyLine()) {
      for (const [term, instead] of banned) {
        if (!term.test(text)) continue;
        if (exceptions.some((e) => e.term.source === term.source && e.line === text)) continue;
        hits.push(`${path}: ${term} in "${text}" (say: ${instead})`);
      }
    }
    expect(hits).toEqual([]);
  });
  it("walks enough of the table to mean something", () => {
    expect(everyLine().length).toBeGreaterThan(400);
  });
  it("lets the second register's table, and only it, keep them", () => {
    const tech: [string, string][] = [];
    lines("technicalCopy", technicalCopy, tech);
    expect(tech.some(([, t]) => banned.some(([term]) => term.test(t)))).toBe(true);
  });
  it("gives every exception a reason and a line that still needs it", () => {
    for (const e of exceptions) {
      expect(e.why).toBeTruthy();
      expect(e.term.test(e.line), e.line).toBe(true);
    }
  });
});

// The verb table and the dialog template (strings.ts head).
describe("the verb table", () => {
  it("ends a command with an ellipsis exactly when a dialog or a picker follows", () => {
    const opensNothing = ["open", "close", "closeArchive", "closeAll", "hide", "unhide", "restore", "refreshFingerprint", "showInExplorer", "findMissing", "reopen", "lockNow", "unlock", "forceClose", "forceCloseAll", "copyName", "paste"];
    for (const [k, v] of Object.entries(commandCopy)) {
      expect(v.endsWith("…"), k).toBe(!opensNothing.includes(k));
    }
  });
  it("has no ellipsis on a dialog's primary, save where a picker follows", () => {
    const primaries = Object.entries(dialogCopy).flatMap(([k, v]) =>
      typeof v === "object" && v && "primary" in v ? [[k, (v as { primary: string }).primary] as const] : [],
    );
    expect(primaries.length).toBeGreaterThan(8);
    for (const [k, p] of primaries) expect(p.endsWith("…"), k).toBe(k === "newArchive");
  });
  it("asks a consequence dialog's title as a question, and keeps its body to two sentences", () => {
    const d = dialogCopy;
    const asks = [d.deleteArchive.title("Photos 2024"), d.forgetArchive.title("Photos 2024"), d.compact.title("Photos 2024"), d.rekeyArchive.title("Photos 2024"), d.rekeyVault.title, d.removeUnlockMethod.title("Desk key"), d.removeUnlockMethod.title("Password", "2026-09-01"), d.passwordOn.title, d.passwordChange.title, d.passwordOff.title, d.replaceRecoveryKeys.title(1), d.replaceRecoveryKeys.title(2)];
    for (const t of asks) expect(t).toMatch(/\?$/);
    const bodies = [
      d.deleteArchive.body("D:\\Archives\\photos-2024.efd"), d.forgetArchive.body("2026-11-12"), d.compact.body, d.rekeyArchive.body, d.rekeyVault.body(true), d.rekeyVault.body(false),
      d.removeUnlockMethod.body("Desk key", "hardware"), d.removeUnlockMethod.body("Paper", "recovery"), d.removeUnlockMethod.body("Password", "password"), d.passwordOn.body, d.passwordChange.body, d.passwordOff.body,
      d.replaceRecoveryKeys.body(1), d.replaceRecoveryKeys.body(2),
    ];
    for (const b of bodies) expect(b.split(/(?<=[.?!])\s+(?=[A-Z])/).length, b).toBeLessThanOrEqual(2);
    // A backup holds the recovery keys alone (FORMAT R28): only a recovery
    // key's removal leaves backups that still open with it.
    expect(d.removeUnlockMethod.body("Desk key", "hardware")).toContain("a backup never held it");
    expect(d.removeUnlockMethod.body("Password", "password")).toContain("a backup never held it");
    expect(d.removeUnlockMethod.body("Paper", "recovery")).toContain("Copies and backups");
    expect(d.removeUnlockMethod.title("Password", "2026-09-01")).toBe("Remove Password (added 2026-09-01)?");
  });
  it("puts every chip in sentence case", () => {
    for (const [k, v] of Object.entries(chipCopy)) {
      const t = typeof v === "function" ? v("2026-10-13") : v;
      expect(t[0], k).toBe(t[0].toUpperCase());
    }
  });
});

// The phase 1 visual checks (ruled 2026-10-17): one name and one order
// for card 2, the YubiKey never a bare "key", the purge's consequence
// first, and one sentence for the backup that may still open an archive.
describe("the lock screen's and the dialogs' words", () => {
  it("names card 2 in its fields' order, the PIN first", () => {
    expect(ceremonyCopy.pinAndPassword).toBe("PIN and password");
    for (const line of [ceremonyCopy.pinAndPassword, vaultCopy.passwordOn]) {
      expect(line.indexOf("PIN"), line).toBeGreaterThanOrEqual(0);
      expect(line.indexOf("PIN"), line).toBeLessThan(line.indexOf("password"));
    }
  });
  it("never calls the YubiKey a bare key", () => {
    const bare = /\b(the|this) key\b/i;
    for (const code of Object.values(Code)) {
      if (code && code.startsWith("token.")) expect(codeText(code), code).not.toMatch(bare);
    }
    expect(codeText(Code.CodePasswordDeadline)).not.toMatch(/\bthe key\b/i);
    expect(lockCopy.insertLine("Personal vault")).toBe("Waiting for a YubiKey that unlocks Personal vault.");
    for (const s of [lockCopy.stepKey, lockCopy.lastTry, lockCopy.stoppedWaiting, lockCopy.stoppedLine, lockCopy.insertStill]) expect(s, s).not.toMatch(/\b(the|this|a) key\b|^key\b/i);
  });
  it("says what the purge means, the consequence first and no key", () => {
    expect(purgedText(["Old laptop backup"])).toBe('"Old laptop backup" was forgotten more than 30 days ago, so it can no longer be opened.');
    expect(purgedText(["A", "B"])).toMatch(/^2 forgotten archives can no longer be opened: A, B\./);
    for (const t of [purgedText(["A"]), purgedText(["A", "B"])]) expect(t).not.toMatch(/\bkeys?\b/i);
  });
  it("keeps the backup's safety net to one sentence", () => {
    const line = dialogCopy.lastBackup("2026-09-25");
    expect(line).toBe("The last backup of this vault, 2026-09-25, can open this archive if you still have it.");
    expect(line.split(/(?<=[.;?!])\s+/).length).toBe(1);
  });
  it("ends a lock-screen command that opens a dialog with an ellipsis", () => {
    expect(lockCopy.createInstead.endsWith("…")).toBe(true);
    for (const s of [lockCopy.findIt, lockCopy.useThatCopy, lockCopy.restoreOther]) expect(s.endsWith("…"), s).toBe(true);
  });
  it("says the stage's edge states in the house voice: no contraction, no bare key", () => {
    const said = [
      lockCopy.noMatch("Personal vault"), lockCopy.busyLead("Personal vault"), lockCopy.busyLine, lockCopy.absentLead("Personal vault"),
      lockCopy.damagedLead("Personal vault"), lockCopy.keptCopy("2026-10-01"), lockCopy.rebuildLine, lockCopy.finishLead("Personal vault"),
      lockCopy.recoveryLine, lockCopy.recoveryRefused("Personal vault"), lockCopy.pendingTouch, lockCopy.continueUnlocking,
      ceremonyCopy.blockedLead, ceremonyCopy.blockedMeans, ceremonyCopy.blockedMeansWayOut, lockCopy.pinLead, lockCopy.brokenLine(""), lockCopy.brokenLine("yesterday, 17:10"), lockCopy.header.busy, stepCopy[CeremonyStep.StepBlocked].body, ceremonyCopy.groupsMistyped([3]), ceremonyCopy.groupsMistyped([3, 5]),
      ceremonyCopy.insertProve, ceremonyCopy.insertProveLine(["Desk"], true), ceremonyCopy.insertProveLine(["Desk"], false), ceremonyCopy.insertNew, ...ceremonyCopy.halves,
      ceremonyCopy.swapLead("Desk"), ceremonyCopy.swapLine, ceremonyCopy.checking, ceremonyCopy.lastTryHere, ...Object.values(ceremonyCopy.doneTitle),
      ceremonyCopy.typeProve, ceremonyCopy.typeProveLine(true), ceremonyCopy.typeProveLine(false), codeText("vault.broken"), codeText("vault.indeterminate"),
      stepCopy[CeremonyStep.StepTwoKeys].title, stepCopy[CeremonyStep.StepTwoKeys].body, ...Object.values(lockCopy.derivingLead), lockCopy.done.created,
    ];
    for (const s of said) {
      expect(s, s).not.toMatch(/n't|'re|'ll|'ve|it's/i);
      // "key ID" is the recovery sheet's own word for its ID.
      expect(s, s).not.toMatch(/\b(the|this|a) key\b(?! ID)/i);
    }
    expect(lockCopy.noMatch("Personal vault")).toBe("This YubiKey is not one of Personal vault's unlock methods");
    // Only an enrolment's wait says what comes next (APP.md §13).
    expect(ceremonyCopy.insertProveLine(["Desk", "Travel"], true)).toBe("Desk, or Travel. The new unlock method comes next.");
    expect(ceremonyCopy.insertProveLine(["Desk"], false)).toBe("Desk.");
    expect(ceremonyCopy.insertProveLine([], false)).toBe("");
    // A typed proof says why; only an enrolment's what comes next.
    expect(ceremonyCopy.typeProveLine(false)).toBe("It proves it is you.");
    expect(ceremonyCopy.typeProveLine(true)).toBe("The new unlock method comes next.");
    // The broken line names what is reopened, the lock screen's button.
    expect(codeText("vault.broken")).toBe("A save could not be confirmed. Reopen the vault to see which version was kept.");
    // Whose PIN is in the lead; the bar is one sentence, the way out after its meaning.
    expect(ceremonyCopy.blockedLead).toBe("This YubiKey's PIN is blocked");
    expect(ceremonyCopy.blockedMeansWayOut.startsWith(ceremonyCopy.blockedMeans.slice(0, -1))).toBe(true);
    // A broken vault's caption names the state the header names.
    expect(lockCopy.brokenLine("")).toBe(lockCopy.header.broken);
    expect(lockCopy.brokenLine("yesterday, 17:10").startsWith(`${lockCopy.header.broken} · `)).toBe(true);
    expect(ceremonyCopy.groupsMistyped([3])).toBe("Group 3 does not check out. Compare its six digits with the sheet.");
    expect(ceremonyCopy.groupsMistyped([2, 3, 5])).toBe("Groups 2, 3 and 5 do not check out. Compare their digits with the sheet.");
    expect(ceremonyCopy.groupsTyped(5, 8)).toBe("5 of 8 groups");
    expect(stepCopy[CeremonyStep.StepPIN].body).toBe("");
  });
  it("asks the close question in the order its buttons stand", () => {
    expect(closeCopy.body.indexOf("Quit")).toBe(0);
    expect(closeCopy.body.indexOf("tray")).toBeGreaterThan(0);
  });
  it("says a backup check's outcome one way, on the lock screen and in the panel", () => {
    expect(ceremonyCopy.verified(1)).toBe("The backup opens. 1 archive inside.");
    expect(ceremonyCopy.verified(3)).toBe("The backup opens. 3 archives inside.");
  });
  it("agrees in number when archives keep a vault from being created", () => {
    expect(lockCopy.stillOpen(1)).toBe("1 archive is still open. Close it first: its saves would land in the wrong vault.");
    expect(lockCopy.stillOpen(2)).toMatch(/^2 archives are still open\. Close them first: their saves/);
  });
  it("names the YubiKey, never a bare key, in the strip's and the panel's lines", () => {
    const said = [lockCopy.pendingTouch, ceremonyCopy.touchCount(2), ceremonyCopy.swapLead("Desk"), ceremonyCopy.swapLead(""), ceremonyCopy.swapLine, stepCopy[CeremonyStep.StepBlocked].title];
    for (const s of said) expect(s).not.toMatch(/\b(the|this) key\b/i);
    // The core's InsertLabel, "the key to enroll", is never shown: the
    // swap names the YubiKey to remove and says the new one follows.
    expect(ceremonyCopy.swapLead("Desk")).toBe("Remove Desk");
    expect(ceremonyCopy.swapLine).toBe("Then insert the new YubiKey. Enfold carries on by itself.");
    expect(stepCopy[CeremonyStep.StepBlocked].title).toBe("The PIN is blocked. Use your recovery key.");
  });
});

// Direction 9 (ruled 2026-10-18): safety's receipts, said as what they are.
describe("the receipts' words", () => {
  const house = /n't|'re|'ll|'ve|it's/i;
  it("says the fingerprint is which copy, never a check", () => {
    expect(archivesCopy.fingerprint({ kind: "current", behind: 0 })).toBe("Matches the last save");
    expect(archivesCopy.fingerprint({ kind: "behind", behind: 1 })).toBe("From 1 save ago");
    expect(archivesCopy.fingerprint({ kind: "behind", behind: 2 })).toBe("From 2 saves ago");
    // never taken: said, not counted from seq 0; owed: no number claimed
    expect(archivesCopy.fingerprint({ kind: "none", behind: 0 })).toBe("Not taken yet");
    expect(archivesCopy.fingerprint({ kind: "owed", behind: 3 })).toBe("From before the last save");
    expect(archivesCopy.facts.fingerprint).toBe("Fingerprint");
    expect(commandCopy.refreshFingerprint).toBe("Refresh fingerprint");
    expect(technicalCopy.fingerprint).toBe("Fingerprint (SHA-256 of the file)");
    expect(technicalCopy.fingerprintNote).toMatch(/It is not a check of the file's contents\.$/);
    // one word for the thing in that dialog: never a bare "hash" beside it
    expect([technicalCopy.seq, technicalCopy.fingerprintSeq]).toEqual(["Last seq", "Fingerprint at seq"]);
    expect(codeText(Code.CodeArchiveCopyMismatch)).toContain("Your next save, or Refresh fingerprint, makes it the current one.");
    expect(technicalCopy.envelopeStaleWhy).not.toMatch(/\bVerify\b/);
  });
  it("names the archive row's dialog Details, its title too (ruled 2026-10-26)", () => {
    expect(archivesCopy.details).toBe("Details…");
    expect(strings.keyTitles.details).toBe("Details… (Alt+Enter)");
    expect(technicalCopy.title).toBe("Details");
    expect(technicalCopy.titleOf("Photos 2024")).toBe("Details: Photos 2024");
    // The Tampered banner's Why? keeps its own name.
    expect(technicalCopy.why).toBe("Why?");
  });
  it("says turning the password off in two sentences, on a standard button", () => {
    expect(dialogCopy.passwordOff.body).toBe("After this, a YubiKey opens the vault with its PIN and touch alone. One of the vault's unlock methods is asked for first.");
    expect(dialogCopy.passwordOff.primary).toBe("Turn off the password");
  });
  it("says what an extract makes, every time", () => {
    expect(extractCopy.ordinaryFiles).toBe("The extracted copies are ordinary files, outside Enfold's protection.");
  });
  it("gives the recovery key both risks, one paragraph each", () => {
    expect(revealCopy.leadOpens).toBe("With this key, the vault file or a backup opens without your YubiKey. Keep it from others.");
    expect(revealCopy.leadNothing).toBe("Without the vault file or a backup, this key opens nothing. Keep those too.");
    expect(revealCopy.digitsLine(48)).toMatch(/^48 digits\. /);
    expect(revealCopy.digitsLine(48)).not.toMatch(/ID/);
    expect(revealCopy.group(3)).toBe("Group 3");
  });
  it("names the boot failure and copies its details in one line", () => {
    expect(bootCopy.title).toBe("Enfold could not start");
    expect(bootCopy.details("internal", "2026-10-18T09:12:00.000Z")).toBe("Enfold could not start · internal · 2026-10-18T09:12:00.000Z");
  });
  it("says the Tampered banner's three sentences, the archive files last", () => {
    expect(tamperedCopy()).toBe("This vault file does not match itself, so it may have been changed outside Enfold. Changes to the vault are off until you replace it with a copy you trust. Your archive files are not affected.");
    // the same words whatever the reason; the cause is the Why?'s
    expect(tamperedCopy(Code.CodeTamperedGeneration)).toBe(tamperedCopy());
    expect(warningCopy(Code.CodeVaultTampered, Code.CodeTamperedHash)).toBe(tamperedCopy());
    // no "above": the banner stands under the commands on the list pages
    expect(vaultCopy.changesOff).toBe("Changes are off until this vault is replaced with a copy you trust; the banner on this page has the button.");
  });
  it("splits each banner where its strong first sentence ends", () => {
    expect(tamperedLead()).toEqual({ head: "This vault file does not match itself, so it may have been changed outside Enfold.", rest: "Changes to the vault are off until you replace it with a copy you trust. Your archive files are not affected." });
    expect(leadText(tamperedLead(Code.CodeTamperedHash))).toBe(tamperedCopy());
    expect(archivesCopy.lockedBannerLead(["Photos 2024"]).head).toBe("The vault is locked.");
    expect(archivePageCopy.lockedBannerLead("Photos 2024").head).toBe("The vault is locked.");
    expect(lockCopy.openWhileLockedLead(["Photos 2024"])).toEqual({ head: "Photos 2024 is still open in Enfold, decrypted.", rest: "Changes to it are still saved." });
    // a name holding ". " is never split inside
    expect(lockCopy.openWhileLockedLead(["Vol. 2"]).head).toBe("Vol. 2 is still open in Enfold, decrypted.");
    for (const names of [["Photos 2024"], ["A", "B"], []]) {
      expect(leadText(archivesCopy.lockedBannerLead(names))).toBe(archivesCopy.lockedBanner(names));
    }
    expect(leadText(lockCopy.openWhileLockedLead(["A", "B"]))).toBe(lockCopy.openWhileLocked(["A", "B"]));
    expect(leadText(archivePageCopy.lockedBannerLead("Photos 2024"))).toBe(archivePageCopy.lockedBanner("Photos 2024"));
  });
  it("says what a lock left open is: decrypted, and still saving", () => {
    expect(lockCopy.openWhileLocked(["Photos 2024"])).toBe("Photos 2024 is still open in Enfold, decrypted. Changes to it are still saved.");
    expect(lockCopy.openWhileLocked(["Photos 2024", "Family videos"])).toBe("2 archives are still open in Enfold, decrypted. Changes to them are still saved.");
    expect(archivesCopy.lockedBanner(["Photos 2024"])).toBe("The vault is locked. Photos 2024 is still open: decrypted, and changes to it are still saved.");
    expect(archivesCopy.lockedBanner(["Photos 2024", "Family videos"])).toBe("The vault is locked. 2 archives are still open: decrypted, and changes to them are still saved.");
    expect(archivesCopy.lockedBanner([])).toBe("The vault is locked.");
    expect(archivePageCopy.lockedBanner("Photos 2024")).toBe(lockedOpenText(["Photos 2024"]));
    expect([lockCopy.closeIt, lockCopy.closeThem, commandCopy.closeArchive, commandCopy.closeAll]).toEqual(["Close it", "Close them", "Close archive", "Close all"]);
    // the old reassurance, gone everywhere
    for (const s of [lockCopy.openWhileLocked(["A"]), lockCopy.openWhileLocked(["A", "B"]), lockedOpenText(["A"]), lockedOpenText(["A", "B"])]) {
      expect(s).not.toMatch(/brows|stays open while|needs the vault/i);
    }
  });
  it("says the backups card's consequence and caveat", () => {
    expect(vaultCopy.backupsLine).toBe("Your archives open only with this vault file or a backup of it. The recovery key opens either one; it cannot replace them.");
    expect(vaultCopy.backupCaveat).toBe("Recorded here, not proof the file is still there.");
  });
  it("keeps the house voice in every new line", () => {
    const said = [
      tamperedCopy(), vaultCopy.changesOff, vaultCopy.backupsLine, vaultCopy.backupCaveat, vaultCopy.backupNeverHead, vaultCopy.backupNever(3), vaultCopy.backupLacks(2, 1),
      lockCopy.openWhileLocked(["Photos 2024"]), lockCopy.openWhileLocked(["A", "B"]), lockedOpenText(["Photos 2024"]), lockedOpenText(["A", "B"]),
      ...Object.values(revealCopy).filter((v): v is string => typeof v === "string"), revealCopy.digitsLine(48), revealCopy.savedTo("E:\\key.txt"),
      bootCopy.title, bootCopy.openFolder, bootCopy.copied, extractCopy.ordinaryFiles, dialogCopy.passwordOff.body, archivesCopy.fingerprint({ kind: "behind", behind: 2 }), archivesCopy.fingerprint({ kind: "none", behind: 0 }), archivesCopy.fingerprint({ kind: "owed", behind: 0 }),
      technicalCopy.fingerprintNote, lockCopy.tamperedWhyTitle,
      // One Extract… and its questions (direction 7).
      extractCopy.title("Photos 2024"), extractCopy.scopeLegend, extractCopy.scopeSelection(4), extractCopy.scopeAll("Photos 2024"),
      conflictCopy.replaceSays(3), conflictCopy.skipSays(3, 9), conflictCopy.skipSays(1, 0), conflictCopy.decideSays(3), compareCopy.keepBoth,
      collisionCopy.kind(true, false), collisionCopy.replaceSays(2), collisionCopy.keepBothSays(2), collisionCopy.skipSays({ files: 2, folders: 0 }, 1), collisionCopy.replaceOff, collisionCopy.kind(true, true), collisionCopy.mergeNote(1, 0), collisionCopy.mergeNote(2, 1), collisionCopy.replaceInside, collisionCopy.replaceInsideSays, collisionCopy.keepBothInsideSays, collisionCopy.skipInside, collisionCopy.skipInsideSays,
      refusedCopy.notWritten("a.jpg"), refusedCopy.how, refusedCopy.sameForRest(3),
    ];
    for (const s of said) expect(s, s).not.toMatch(house);
  });
});

// The re-key question at a removal, a password change and turning the
// password on, and Replace recovery keys… (DESIGN §5, ruled 2026-10-27):
// every line pinned, so that a change to one is a change someone meant.
describe("the re-key question and the replacement (DESIGN §5, ruled 2026-10-27)", () => {
  const d = dialogCopy;
  const house = /n't|'re|'ll|'ve|it's/i;
  const bare = /\b(the|this|a) key\b(?! ID)/i;
  const strs = (o: object): string[] => Object.values(o).filter((v): v is string => typeof v === "string");
  // Every line this change adds, for the house voice and the no-bare-key walk.
  const added = (): string[] => [
    ...strs(d.rekeyQuestion), ...strs(d.rekeyLimit), d.rekeyLimit.method("YubiKey"), d.rekeyLimit.method("password"), d.rekeyLimit.methodOne("YubiKey"), d.rekeyLimit.methodOne("password"),
    ...strs(d.backupAfter), d.backupAfter.whyRecovery("Paper"), ...Object.values(d.primaries).flat(), d.replaceOne,
    d.removeUnlockMethod.title("Desk key", "2026-09-01"), d.removeUnlockMethod.body("Desk key", "hardware"), d.removeUnlockMethod.body("Paper", "recovery"),
    d.removeUnlockMethod.retired(1, "Desk key"), d.removeUnlockMethod.retired(2, "Desk key"), d.removeUnlockMethod.lastProtector,
    d.removeUnlockMethod.how("Desk key", "hardware"), d.removeUnlockMethod.how("Paper", "recovery"),
    d.replaceRecoveryKeys.title(1), d.replaceRecoveryKeys.title(2), d.replaceRecoveryKeys.body(1), d.replaceRecoveryKeys.body(2), d.replaceRecoveryKeys.when(1), d.replaceRecoveryKeys.when(2),
    d.replaceRecoveryKeys.whenOn, d.replaceRecoveryKeys.ready(10, 1), d.replaceRecoveryKeys.ready(10, 2), d.replaceRecoveryKeys.how, d.replaceRecoveryKeys.howOne, d.replaceRecoveryKeys.howOn,
    d.passwordOn.body, d.passwordOn.how, d.passwordChange.body, d.passwordChange.how, d.rekeyVault.body(true), d.rekeyVault.body(false),
    ...Object.values(ceremonyCopy.doneRekeyed), ...Object.values(ceremonyCopy.phaseLead), ...Object.values(ceremonyCopy.followUp), ...Object.values(ceremonyCopy.retireWhat),
    ceremonyCopy.doneTitle.password, ceremonyCopy.nothingChanged, ceremonyCopy.backupNotSaved(codeText("io")), ceremonyCopy.retireNotFinished("Lead.", "Why.", true),
    ceremonyCopy.retireLead.state(""), ceremonyCopy.retireLead.state("still opens with the unlock method removed"), ceremonyCopy.retireLead.remnant, ceremonyCopy.retireLead.remnantOldKey,
    ceremonyCopy.keysNeverAdded, ceremonyCopy.keysNeverAddedOne, ceremonyCopy.keysWorkIfListed, ceremonyCopy.keysWorkIfListedOne, ceremonyCopy.afterRecoveryProof,
    ceremonyCopy.recoveryNote.enroll, ceremonyCopy.recoveryNote.enrollRecovery, ceremonyCopy.recoveryNote.remove, ceremonyCopy.recoveryNote.only, ceremonyCopy.recoveryNote.onlyOne, ceremonyCopy.recoveryNote.nextMethod,
    ...strs(replaceCopy), replaceCopy.pair("3F7A-9C21", "51D0-77AA"), replaceCopy.notAdded(["3F7A-9C21"]), replaceCopy.notAdded(["3F7A-9C21", "0B44-1E07"]), replaceCopy.unknown(["3F7A-9C21"]), replaceCopy.unknown(["3F7A-9C21", "0B44-1E07"]),
    revealCopy.titleKeep(1, 1), revealCopy.titleKeep(1, 2), revealCopy.replaces("Safe", "51D0-77AA"), revealCopy.keepOpens, revealCopy.keepDigitsLine(48, 1), revealCopy.keepDigitsLine(48, 2), revealCopy.keepGone, revealCopy.next, revealCopy.continue, revealCopy.digitsLineLater(48),
    vaultCopy.ceremonyTitle.password, vaultCopy.ceremonyTitle.replace, vaultCopy.ceremonyTitle.replaceOne, vaultCopy.backupBeforeRekey, vaultCopy.backupKeysReplaced, vaultCopy.noRecoveryToReplace, vaultCopy.replaceNeedsProtector, vaultCopy.ruleYubiKey, vaultCopy.setupLine(1), vaultCopy.setupLine(2),
    commandCopy.replaceRecoveryKeys, importCopy.beforeRekey, importCopy.replacedOwes, retireHeld, codeText("vault.retire_owed"), codeText("vault.setup_password_on"),
    stepCopy[CeremonyStep.StepKeepKey].title, stepCopy[CeremonyStep.StepKeepKey].body, stepCopy[CeremonyStep.StepConfirm].title,
  ];

  it("asks the fact, never the technical choice, with the re-keying answer first", () => {
    expect(d.rekeyQuestion).toEqual({
      hardware: "Why are you removing this YubiKey?",
      recovery: "Why are you removing this recovery key?",
      password: "Why are you removing this password?",
      change: "Why are you changing the password?",
      on: "Could the vault file have been copied before now?",
      lost: "Lost, stolen, or I am not sure",
      kept: "I still have it and it was never out of my control",
      known: "Someone else may know it, or I am not sure",
      onlyMe: "Only I have ever known it",
      copied: "Yes, or I am not sure",
      notCopied: "No, it was never copied",
      rekeys: "The vault is re-keyed, as recommended.",
      keeps: "The vault keeps its key.",
    });
  });
  it("says the limit of a re-key plainly, and the act that closes what it leaves open", () => {
    const l = d.rekeyLimit;
    expect(l.future).toBe("Re-keying protects the future, not the past: whatever someone could open until now, they may already have copied.");
    expect(l.method("YubiKey")).toBe("If an earlier copy of the vault file may be out there, whoever opened it with this YubiKey could also read the recovery keys, which still open this vault: replace them afterwards with Replace recovery keys… on this page.");
    expect(l.method("password")).toContain("whoever opened it with this password could also read the recovery keys");
    expect(l.methodOn).toBe("If an earlier copy of the vault file may be out there, whoever opened it with this YubiKey could also read the recovery keys and the vault's own record of the password: change the password afterwards, then replace the recovery keys with Replace recovery keys… on this page.");
    expect(l.methodOnNoRecovery).toBe("If an earlier copy of the vault file may be out there, whoever opened it with this YubiKey could also read the vault's own record of the password: change the password afterwards.");
    expect(l.recovery).toBe("Whoever has this recovery key can open an earlier copy or backup of the vault and read the other recovery keys there: if one may be out there, use Replace recovery keys… on this page instead, which removes this one too. Check the unlock methods as well, and remove any you did not add.");
    expect(l.recoveryOn).toBe(" Then change the password.");
    expect(l.change).toBe("If an earlier copy of the vault file may be out there, whoever opened it with a YubiKey and the old password could also read the recovery keys: replace them afterwards with Replace recovery keys… on this page.");
    expect(l.on).toBe("Whoever opens an earlier copy of the vault file with a YubiKey alone can also read the recovery keys: if such a copy may exist, replace them afterwards with Replace recovery keys… on this page.");
  });
  it("says the limit for one recovery key in the singular, the command still named as it is", () => {
    const l = d.rekeyLimit;
    expect(l.methodOne("YubiKey")).toBe("If an earlier copy of the vault file may be out there, whoever opened it with this YubiKey could also read the recovery key, which still opens this vault: replace it afterwards with Replace recovery keys… on this page.");
    expect(l.methodOne("password")).toContain("whoever opened it with this password could also read the recovery key, which still opens this vault");
    expect(l.methodOnOne).toBe("If an earlier copy of the vault file may be out there, whoever opened it with this YubiKey could also read the recovery key and the vault's own record of the password: change the password afterwards, then replace the recovery key with Replace recovery keys… on this page.");
    expect(l.recoveryOne).toBe("Whoever has this recovery key can open an earlier copy or backup of the vault and read the other recovery key there: if one may be out there, use Replace recovery keys… on this page instead, which removes this one too. Check the unlock methods as well, and remove any you did not add.");
    expect(l.changeOne).toBe("If an earlier copy of the vault file may be out there, whoever opened it with a YubiKey and the old password could also read the recovery key: replace it afterwards with Replace recovery keys… on this page.");
    expect(l.onOne).toBe("Whoever opens an earlier copy of the vault file with a YubiKey alone can also read the recovery key: if such a copy may exist, replace it afterwards with Replace recovery keys… on this page.");
  });
  it("offers the backup after the change, with the reason that ticks it", () => {
    const b = d.backupAfter;
    expect(b.tick).toBe("Export a backup afterwards");
    expect(b.whyRekey).toBe("A backup made before the re-key is at the vault's old key, and restoring it would bring that key back; one made afterwards is not.");
    expect(b.whyRecovery("Paper")).toBe("Backups made while Paper was an unlock method hold it and open with it; one made afterwards does not.");
    expect(b.whyReplace).toBe("Backups made before now open only with the old recovery keys; one made afterwards holds the new ones.");
    expect(b.whyReplaceOne).toBe("Backups made before now open only with the old recovery key; one made afterwards holds the new one.");
    expect(b.none).toBe("No backup of this vault can be made without a recovery key.");
    // The block reads the Re-key dialog's own fact and reason.
    expect([d.rekeyVault.lastBackup, d.rekeyVault.noBackup, d.rekeyVault.backupWhy]).toEqual(["Last backup", "None recorded here", "A backup is what brings your archives back if this vault is lost."]);
    expect("backupFirst" in d.rekeyVault).toBe(false);
    expect("primaryWithBackup" in d.rekeyVault).toBe(false);
  });
  it("names what each primary does", () => {
    expect(d.primaries).toEqual({
      remove: ["Remove unlock method", "Remove and re-key", "Remove, then back up", "Remove, re-key and back up"],
      password: ["Change password", "Change and re-key", "Change, then back up", "Change, re-key and back up"],
      on: ["Turn on password", "Turn on and re-key", "Turn on, then back up", "Turn on, re-key and back up"],
      rotate: ["Re-key vault", "Re-key vault", "Re-key, then back up", "Re-key, then back up"],
      replace: ["Replace recovery keys", "Replace recovery keys", "Replace, then back up", "Replace, then back up"],
    });
    expect(d.replaceOne).toBe("Replace recovery key");
    for (const p of [...Object.values(d.primaries).flat(), d.replaceOne]) expect(p.endsWith("…"), p).toBe(false);
  });
  it("says the remove dialog's lines", () => {
    const r = d.removeUnlockMethod;
    expect(r.retired(1, "Desk key")).toBe("Enfold keeps 1 replaced vault file in its folder; if it had Desk key, it still opens with it until you delete it.");
    expect(r.retired(2, "Desk key")).toBe("Enfold keeps 2 replaced vault files in its folder; those that had Desk key still open with it until you delete them.");
    expect(r.lastProtector).toBe("After this the vault has only its recovery keys: add the new YubiKey here now, or after unlocking with a recovery key.");
    expect(r.how("Desk key", "hardware")).toBe(
      "Re-keying gives the vault a new key in the same step, and every unlock method that stays gets it. An earlier copy of the vault file opened with Desk key then reaches nothing in this vault through Desk key; the recovery keys it held still do, until they are replaced. An archive that existed then stays open to such a copy, files added later included, until that archive is re-keyed with Re-key archive… on its page. A backup made before now is at the old key; the one taken afterwards is at the new one. The vault refuses a removal that would leave fewer than 2 unlock methods.",
    );
    expect(r.how("Paper", "recovery")).toBe(
      "Re-keying gives the vault a new key in the same step, and every unlock method that stays gets it. An earlier copy or backup opened with Paper then reaches nothing in this vault through Paper; the other recovery keys it held still do, until they are replaced. An archive that existed then stays open to it, files added later included, until that archive is re-keyed with Re-key archive… on its page. The backups made while Paper was an unlock method still open with it, and this vault can still import archives from them; where you keep them is yours to decide. The vault refuses a removal that would leave fewer than 2 unlock methods.",
    );
    // The backups made while it was enrolled are said, not ordered destroyed.
    expect(r.how("Paper", "recovery")).not.toMatch(/destroy/i);
    expect("primary" in r).toBe(false);
  });
  it("says Replace recovery keys…'s dialog, singular for one key", () => {
    const r = d.replaceRecoveryKeys;
    expect([r.title(1), r.title(2)]).toEqual(["Replace the recovery key?", "Replace the 2 recovery keys?"]);
    expect(r.body(1)).toBe("Enfold makes a new recovery key for you to keep, and the old one stops unlocking this vault. Copies and backups of the vault made before now still open with the old one.");
    expect(r.body(2)).toBe("Enfold makes 2 new recovery keys for you to keep, one for each, and the old ones stop unlocking this vault. Copies and backups of the vault made before now still open with the old ones.");
    expect(r.when(1)).toBe("Replace it when an earlier copy of the vault file, or a backup, may have been opened by someone else: whoever did could read the recovery key in it, which keeps opening this vault until it is replaced. If that copy was opened with a YubiKey or a password, remove that unlock method first, and remove any unlock method you did not add.");
    expect(r.when(2)).toBe("Replace them when an earlier copy of the vault file, or a backup, may have been opened by someone else: whoever did could read every recovery key in it, and those keep opening this vault until they are replaced. If that copy was opened with a YubiKey or a password, remove that unlock method first, and remove any unlock method you did not add.");
    expect(r.whenOn).toBe(" Change the password too: the vault's own record of it was readable the same way.");
    expect(r.ready(10, 2)).toBe("Have somewhere to keep each new recovery key before you start: print it, save it as a text file, or write it down. Nothing changes until you have kept every one and confirmed. Enfold locks after 10 minutes without input, which ends the replacement with nothing changed: printing or saving is quicker than writing 48 digits by hand.");
    expect(r.ready(1, 2)).toContain("Enfold locks after 1 minute without input");
    expect(r.ready(10, 1)).toBe(
      "Have somewhere to keep the new recovery key before you start: print it, save it as a text file, or write it down. Nothing changes until you have kept it and confirmed. Enfold locks after 10 minutes without input, which ends the replacement with nothing changed: printing or saving is quicker than writing 48 digits by hand.",
    );
    expect(r.how).toBe("Enfold makes the new keys first and shows each one in turn. When you confirm, one step adds them, removes every old recovery key and re-keys the vault, so the copy of each new recovery key the vault keeps is never readable with an old one. An earlier copy or backup then reaches nothing in this vault through its recovery keys; an archive that existed then stays open to it, files added later included, until that archive is re-keyed with Re-key archive… on its page. Each new recovery key takes the name of the one it replaces, so you know where it goes.");
    expect(r.howOne).toBe(
      "Enfold makes the new recovery key first and shows it. When you confirm, one step adds it, removes the old recovery key and re-keys the vault, so the copy of the new recovery key the vault keeps is never readable with the old one. An earlier copy or backup then reaches nothing in this vault through its recovery keys; an archive that existed then stays open to it, files added later included, until that archive is re-keyed with Re-key archive… on its page. The new recovery key takes the name of the one it replaces, so you know where it goes.",
    );
    expect(r.howOn).toBe(" Such a copy also held the vault's record of the password, which only changing the password replaces.");
  });
  it("says the password's dialogs truly: no field for the old password, though a YubiKey's proof asks the current one", () => {
    expect(d.passwordChange.body).toBe("Every YubiKey asks for the new password from now on; one unlock method proves it is you first, and the other YubiKeys need not be present. You choose the password in the next step.");
    expect(d.passwordChange.how).toMatch(/^This dialog has no field for the old password: /);
    expect(d.passwordChange.how).toContain("Proving it is you with a YubiKey still asks for the current password with its PIN.");
    expect(d.passwordChange.how).not.toMatch(/is not asked for/);
    expect(d.passwordChange.how).toContain("An earlier copy of the vault file opened with a YubiKey and the old password then reaches nothing in this vault through them; the recovery keys it held still do, until they are replaced.");
    expect(d.passwordOn.body).toBe("Every YubiKey will ask for this password as well as its PIN, so a stolen YubiKey alone cannot open this vault file. The recovery key and a password unlock method do not change.");
    expect(d.passwordOn.how.startsWith("The vault asks for one of its unlock methods first, then updates every YubiKey's entry without needing the YubiKey present. Re-keying gives the vault a new key in the same step.")).toBe(true);
    expect(d.rekeyVault.body(true)).toBe("Every unlock method gets the new key now; one of them proves it is you first, and the other YubiKeys need not be present. Backups made before today keep opening with the methods they had.");
    expect(d.rekeyVault.body(false)).toBe("Every unlock method gets the new key now; one of them proves it is you first. Backups made before today keep opening with the methods they had.");
  });
  it("says the replacement's ceremony, its receipt and its end, singular for one key", () => {
    expect(replaceCopy.ready).toBe("Ready to replace");
    expect(stepCopy[CeremonyStep.StepConfirm].title).toBe(replaceCopy.ready);
    expect(replaceCopy.pair("3F7A-9C21", "51D0-77AA")).toBe("3F7A-9C21 replaces 51D0-77AA");
    expect(replaceCopy.readyLine).toBe("The replaced keys stop unlocking this vault, and every new one works from now on. Check that you can read each new recovery key before you go on.");
    expect(replaceCopy.readyLineOne).toBe("The replaced key stops unlocking this vault, and the new one works from now on. Check that you can read the new recovery key before you go on.");
    expect(replaceCopy.backedUp).toBe("Destroy the replaced keys' sheets and files once the new backup opens (Test that a backup opens…). The backups made before still open with the replaced keys, and this vault can still import archives from them.");
    expect(replaceCopy.backedUpOne).toBe("Destroy the replaced key's sheet and file once the new backup opens (Test that a backup opens…). The backups made before still open with the replaced key, and this vault can still import archives from them.");
    expect(replaceCopy.noBackup).toBe("No backup holds the new keys yet, and every earlier one opens only with the replaced keys. Export one from Backups and copies, and destroy the replaced keys' sheets and files once it opens.");
    expect(replaceCopy.noBackupOne).toBe("No backup holds the new key yet, and every earlier one opens only with the replaced key. Export one from Backups and copies, and destroy the replaced key's sheet and file once it opens.");
    expect(replaceCopy.afterFailedBackup).toBe("Destroy the replaced keys' sheets and files once a new backup opens.");
    expect(replaceCopy.afterFailedBackupOne).toBe("Destroy the replaced key's sheet and file once a new backup opens.");
    expect([replaceCopy.newKeys, replaceCopy.replacedKeys]).toEqual(["New", "Replaced"]);
    // The earlier backups are said, never ordered destroyed: a merge reads them.
    for (const s of [replaceCopy.backedUp, replaceCopy.backedUpOne]) expect(s).toContain("this vault can still import archives from them");
  });
  it("names the keys a replacement showed and never added, and never says destroy when it is not known", () => {
    expect(replaceCopy.notAdded(["3F7A-9C21"])).toBe("Nothing was replaced. Recovery key 3F7A-9C21 was shown but never added: destroy what you kept of it.");
    expect(replaceCopy.notAdded(["3F7A-9C21", "0B44-1E07"])).toBe("Nothing was replaced. Recovery keys 3F7A-9C21 and 0B44-1E07 were shown but never added: destroy what you kept of them.");
    expect(replaceCopy.notAdded(["A", "B", "C"])).toContain("Recovery keys A, B and C were shown");
    expect(replaceCopy.unknown(["3F7A-9C21"])).toBe("Recovery key 3F7A-9C21 was shown, and Enfold could not tell whether it replaced the old one. Reopen the vault; the Vault page then lists the recovery keys it has. Keep both sheets until then.");
    expect(replaceCopy.unknown(["3F7A-9C21", "0B44-1E07"])).toBe("Recovery keys 3F7A-9C21 and 0B44-1E07 were shown, and Enfold could not tell whether they replaced the old ones. Reopen the vault; the Vault page then lists the recovery keys it has. Keep every sheet until then.");
    for (const s of [replaceCopy.unknown(["A"]), replaceCopy.unknown(["A", "B"])]) {
      expect(s).not.toMatch(/destroy/i);
      expect(s).not.toMatch(/could not be confirmed/i);
    }
    expect(ceremonyCopy.keysNeverAdded).toBe("The recovery keys shown were never added: destroy what you kept of them.");
    expect(ceremonyCopy.keysNeverAddedOne).toBe("The recovery key shown was never added: destroy what you kept of it.");
    expect(ceremonyCopy.keysWorkIfListed).toBe("The new recovery keys work only if the Vault page lists their IDs after you reopen it: keep every sheet until you have looked.");
    expect(ceremonyCopy.keysWorkIfListedOne).toBe("The new recovery key works only if the Vault page lists its ID after you reopen it: keep both sheets until you have looked.");
    expect(ceremonyCopy.keysWorkIfListed).toContain("keep every sheet");
    expect(ceremonyCopy.keysWorkIfListedOne).toContain("keep both sheets");
  });
  it("says keep mode's words: by itself, and nothing changes until the confirmation", () => {
    expect(revealCopy.titleKeep(1, 2)).toBe("New recovery key 1 of 2");
    expect(revealCopy.titleKeep(1, 1)).toBe("Your new recovery key");
    expect(revealCopy.replaces("Safe", "51D0-77AA")).toBe("Replaces Safe · 51D0-77AA.");
    expect(revealCopy.keepOpens).toBe("Once you confirm, this recovery key opens the vault file by itself, and backups made from then on. Keep it from others.");
    expect(revealCopy.keepOpens).toContain("by itself");
    expect(revealCopy.keepOpens).not.toMatch(/\balone\b/);
    expect(revealCopy.keepDigitsLine(48, 2)).toBe("48 digits. Nothing changes until every new recovery key is kept and you confirm.");
    expect(revealCopy.keepDigitsLine(48, 1)).toBe("48 digits. Nothing changes until this recovery key is kept and you confirm.");
    expect(revealCopy.keepGone).toBe("This recovery key could not be shown here. Nothing has changed: cancel, and start the replacement again.");
    expect([revealCopy.next, revealCopy.continue]).toEqual(["Next recovery key", "Continue"]);
    expect(revealCopy.digitsLineLater(48)).toBe("48 digits. They can be shown again from the Vault page only after you add a YubiKey or a password and prove it.");
  });
  it("titles the new ceremonies and says the Vault page's new lines", () => {
    expect(vaultCopy.ceremonyTitle.password).toBe("Change the password for every YubiKey");
    expect(vaultCopy.ceremonyTitle.replace).toBe("Replace the recovery keys");
    expect(vaultCopy.ceremonyTitle.replaceOne).toBe("Replace the recovery key");
    expect(vaultCopy.backupBeforeRekey).toBe("It was made before the vault was re-keyed.");
    expect(vaultCopy.backupKeysReplaced).toBe("It opens only with recovery keys this vault no longer has.");
    expect(vaultCopy.noRecoveryToReplace).toBe("This vault has no recovery key to replace.");
    expect(vaultCopy.replaceNeedsProtector).toBe("Add a YubiKey or a password first: a new recovery key can be shown again only through one.");
    // Both ways serve: a new YubiKey, or a recovery key added with the switch.
    expect(vaultCopy.ruleYubiKey).toBe("To remove this YubiKey, add a new one or a recovery key first: the vault always keeps at least 2 unlock methods.");
    expect(vaultCopy.ruleYubiKey).toContain("a recovery key");
    expect(vaultCopy.setupLine(1)).toBe("This vault has only its recovery key. Add a YubiKey or a password to finish setting it up; the recovery key is asked for first.");
    expect(vaultCopy.setupLine(2)).toBe("This vault has only its recovery keys. Add a YubiKey or a password; a recovery key is asked for first.");
    expect(commandCopy.replaceRecoveryKeys).toBe("Replace recovery keys…");
    expect(importCopy.beforeRekey).toBe("It was made before this vault was re-keyed: unlock methods removed since may still open it, and whatever is added to it later.");
    expect(importCopy.replacedOwes).toBe("It also still holds its state from before its last change, so it opens with any unlock method that change removed too. Delete it once the new vault opens.");
  });
  it("says the end by kind and re-key", () => {
    expect(Object.values(ceremonyCopy.doneRekeyed)).toEqual([
      "Unlock method removed and vault re-keyed",
      "Password changed and vault re-keyed",
      "Password turned on and vault re-keyed",
      "Recovery keys replaced and vault re-keyed",
      "Recovery key replaced and vault re-keyed",
    ]);
    expect(doneTitleOf({ kind: "remove", rekey: true })).toBe("Unlock method removed and vault re-keyed");
    expect(doneTitleOf({ kind: "remove", rekey: false })).toBe("Unlock method removed");
    expect(doneTitleOf({ kind: "password", rekey: true })).toBe("Password changed and vault re-keyed");
    expect(doneTitleOf({ kind: "password", rekey: false })).toBe("Password changed");
    expect(doneTitleOf({ kind: "entangle", rekey: true })).toBe("Password turned on and vault re-keyed");
    // Turning it on or off without a re-key keeps the generic Done.
    expect(doneTitleOf({ kind: "entangle", rekey: false })).toBeUndefined();
    expect(doneTitleOf({ kind: "rotate", rekey: false })).toBe("Vault re-keyed");
    expect(doneTitleOf({ kind: "replace", rekey: true, keepCount: 2 })).toBe("Recovery keys replaced and vault re-keyed");
    expect(doneTitleOf({ kind: "replace", rekey: true, keepCount: 1 })).toBe("Recovery key replaced and vault re-keyed");
    expect(doneTitleOf({ kind: "enroll" })).toBe("Unlock method added");
    expect(doneTitleOf({ kind: "export" })).toBe("Backup saved");
  });
  it("leads with what is being done once the proof has passed", () => {
    expect(phaseLeadOf({ kind: "remove", rekey: true })).toBeUndefined();
    expect(phaseLeadOf({ kind: "remove", rekey: true, phase: "" })).toBeUndefined();
    expect(phaseLeadOf({ kind: "remove", rekey: true, phase: "commit" })).toBe("Removing the unlock method and re-keying the vault…");
    expect(phaseLeadOf({ kind: "remove", rekey: false, phase: "commit" })).toBe("Removing the unlock method…");
    expect(phaseLeadOf({ kind: "password", rekey: true, phase: "commit" })).toBe("Changing the password and re-keying the vault…");
    expect(phaseLeadOf({ kind: "password", rekey: false, phase: "commit" })).toBe("Changing the password…");
    expect(phaseLeadOf({ kind: "entangle", rekey: true, phase: "commit" })).toBe("Turning the password on and re-keying the vault…");
    expect(phaseLeadOf({ kind: "entangle", rekey: false, phase: "commit" })).toBe("Updating every YubiKey…");
    expect(phaseLeadOf({ kind: "rotate", rekey: false, phase: "commit" })).toBe("Re-keying the vault…");
    expect(phaseLeadOf({ kind: "replace", rekey: true, phase: "commit", keepCount: 2 })).toBe("Replacing the recovery keys and re-keying the vault…");
    expect(phaseLeadOf({ kind: "replace", rekey: true, phase: "commit", keepCount: 1 })).toBe("Replacing the recovery key and re-keying the vault…");
    for (const kind of ["remove", "password", "entangle", "rotate", "replace", "export"]) expect(phaseLeadOf({ kind, rekey: true, phase: "backup" }), kind).toBe("Saving the backup…");
    // An enrolment's commit, a recovery key's included, which has no second
    // half to say it: the second half's own words.
    expect(phaseLeadOf({ kind: "enroll", phase: "commit" })).toBe("Adding the unlock method…");
    expect(phaseLeadOf({ kind: "enroll", phase: "commit" })).toBe(lockCopy.derivingLead.adding);
    expect(phaseLeadOf({ kind: "enroll" })).toBeUndefined();
  });
  it("lists what waits while a retirement is owed, in the barrier's order, and what does not", () => {
    expect(retireHeld).toBe(
      "changes to the unlock methods, re-keying the vault, backups, and creating, importing, re-keying, forgetting, restoring or deleting archives wait; Enfold tries again at each of them and at the next unlock. You can still open your archives and change the files inside them.",
    );
    const order = ["unlock methods", "re-keying the vault", "backups", "creating", "importing", "re-keying,", "forgetting", "restoring", "deleting archives"];
    const at = order.map((w) => retireHeld.indexOf(w));
    for (const i of at) expect(i).toBeGreaterThanOrEqual(0);
    expect([...at].sort((a, b) => a - b)).toEqual(at);
    expect(retireHeld.endsWith("change the files inside them.")).toBe(true);
    const owed = codeText("vault.retire_owed");
    expect(owed.startsWith("Enfold has not finished clearing")).toBe(true);
    expect(owed).toContain(retireHeld);
    expect(owed).not.toMatch(/last change/i);
    expect(owed).not.toMatch(/unlock methods is not finished/i);
    expect(codeText("vault.setup_password_on")).toBe("This vault's password is on, so its setup cannot be finished here. Unlock with a recovery key, then add a YubiKey on the Vault page.");
  });
  it("says the retirement's bar, its lead by what is left", () => {
    expect(ceremonyCopy.retireNotFinished("Lead.", "Why.", false)).toBe(`Lead. Why. Until Enfold has cleared it, ${retireHeld}`);
    expect(ceremonyCopy.retireNotFinished("Lead.", "Why.", true)).toBe(`Lead. Why. Until Enfold has cleared it, ${retireHeld} The backup you asked for waits too.`);
    const state = (what: string) => `The vault file still holds its state from before this change: a copy of it taken now ${what}.`;
    expect(retireLeadOf({ kind: "remove", rekey: true })).toBe(state("still opens with the unlock method removed"));
    expect(retireLeadOf({ kind: "password", rekey: true })).toBe(state("still opens with a YubiKey and the old password"));
    expect(retireLeadOf({ kind: "rotate" })).toBe(state("still holds the vault's old key"));
    expect(retireLeadOf({ kind: "replace", rekey: true, keepCount: 2 })).toBe(state("still opens with the replaced recovery keys"));
    expect(retireLeadOf({ kind: "replace", rekey: true, keepCount: 1 })).toBe(state("still opens with the replaced recovery key"));
    expect(retireLeadOf({ kind: "entangle", rekey: true, after: { entangled: true } })).toBe(state("still opens with a YubiKey alone"));
    // An enrolment and turning the password off withdrew nothing.
    expect(retireLeadOf({ kind: "enroll" })).toBe("The vault file still holds its state from before this change.");
    expect(retireLeadOf({ kind: "entangle", rekey: false, after: { entangled: false } })).toBe("The vault file still holds its state from before this change.");
    // Only earlier versions of the list of archives are left.
    expect(retireLeadOf({ kind: "remove", rekey: true, retireRemnant: true })).toBe("The vault file still holds earlier versions of its list of archives, which the vault's old key reads.");
    expect(retireLeadOf({ kind: "rotate", retireRemnant: true })).toBe("The vault file still holds earlier versions of its list of archives, which the vault's old key reads.");
    expect(retireLeadOf({ kind: "remove", rekey: false, retireRemnant: true })).toBe("The vault file still holds earlier versions of its list of archives.");
    expect(ceremonyCopy.retireWhat.replaceOne).toBe("still opens with the replaced recovery key");
    expect(ceremonyCopy.nothingChanged).toBe("Nothing was changed.");
    expect(ceremonyCopy.backupNotSaved("A file could not be read or written.")).toBe("The backup was not saved. A file could not be read or written. Export one from Backups and copies.");
  });
  it("says the end's next steps, the password first", () => {
    expect(ceremonyCopy.followUp).toEqual({
      addFirst: "Add the new YubiKey now; then, if an earlier copy of the vault file may be out there, replace the recovery keys.",
      addFirstOn: "Add the new YubiKey now; then, if an earlier copy of the vault file may be out there, change the password and replace the recovery keys.",
      changeFirst: "If an earlier copy of the vault file may be out there, change the password, then replace the recovery keys.",
      changeFirstRecovery: "If an earlier copy or backup may be out there, change the password, then replace the other recovery keys.",
      replace: "If an earlier copy of the vault file may be out there, replace the recovery keys too.",
      replaceRecovery: "If an earlier copy or backup may be out there, replace the other recovery keys too.",
      changeFirstOne: "If an earlier copy of the vault file may be out there, change the password, then replace the recovery key.",
      changeFirstRecoveryOne: "If an earlier copy or backup may be out there, change the password, then replace the other recovery key.",
      replaceOne: "If an earlier copy of the vault file may be out there, replace the recovery key too.",
      replaceRecoveryOne: "If an earlier copy or backup may be out there, replace the other recovery key too.",
      changeAfterReplace: "If an earlier copy of the vault file may be out there, change the password too: the vault's own record of it was readable the same way.",
      retryFirst: " Enfold first tries again to finish clearing the vault file.",
    });
    expect(ceremonyCopy.afterRecoveryProof).toBe("Now remove the lost YubiKey: Remove… on its card asks why, and re-keys the vault when it was lost.");
  });
  it("says why a recovery key proves it, by cause", () => {
    const n = ceremonyCopy.recoveryNote;
    expect(n.enroll).toBe("Your recovery key proves it is you, since no YubiKey of this vault is at hand. The new YubiKey comes next.");
    expect(n.enrollRecovery).toBe("Your recovery key proves it is you, since no YubiKey of this vault is at hand. The new recovery key comes next.");
    expect(n.remove).toBe("One of this vault's recovery keys proves it is you, since the YubiKey being removed is not asked for.");
    expect(n.only).toBe("This vault has only its recovery keys, so one of them proves it is you.");
    expect(n.onlyOne).toBe("This vault has only its recovery key, so it proves it is you.");
    expect(n.nextMethod).toBe(" The new unlock method comes next.");
  });
  it("covers the replacement's two steps", () => {
    expect(stepCopy[CeremonyStep.StepKeepKey].title).toBe("Keep the new recovery key");
    expect(stepCopy[CeremonyStep.StepKeepKey].body).toBe("Print it, save it as a text file, or write it down. Nothing changes until you confirm.");
  });
  it("ends no sentence on a command's ellipsis", () => {
    for (const [path, text] of everyLine()) expect(text.includes("…."), `${path}: ${text}`).toBe(false);
  });
  it("names the act as its command, with its ellipsis or without, and never otherwise", () => {
    for (const [path, text] of everyLine()) {
      for (const m of text.matchAll(/replace recovery keys?…?/gi)) {
        expect(["Replace recovery keys…", "Replace recovery keys", "Replace recovery key"], `${path}: ${text}`).toContain(m[0]);
      }
    }
    expect(d.rekeyLimit.method("YubiKey")).toContain(commandCopy.replaceRecoveryKeys);
    expect(d.primaries.replace[0]).toBe(commandCopy.replaceRecoveryKeys.slice(0, -1));
  });
  it("keeps the house voice and never calls the YubiKey a bare key, in every line it adds", () => {
    const lines = added();
    expect(lines.length).toBeGreaterThan(100);
    for (const s of lines) {
      expect(s, s).not.toMatch(house);
      expect(s, s).not.toMatch(bare);
    }
  });
});

describe("the second register's plain words", () => {
  it("says how much smaller, never the method", () => {
    expect(compressedText("zstd+dict", 71)).toBe("71% smaller");
    expect(compressedText("zstd", 0)).toBe("Yes");
    expect(compressedText("raw", 0)).toBe("No, stored as is");
  });
});

// Relative dates only where recency is the point (strings.ts head).
describe("sinceText", () => {
  const at = (y: number, m: number, d: number, h = 3, min = 24) => new Date(y, m - 1, d, h, min).getTime() / 1000;
  const now = new Date(2026, 9, 13, 12, 0).getTime();
  it("keeps the clock time today and yesterday", () => {
    expect(sinceText(at(2026, 10, 13), now)).toBe("today, 03:24");
    expect(sinceText(at(2026, 10, 12), now)).toBe("yesterday, 03:24");
  });
  it("counts days, then falls back to the ISO form", () => {
    expect(sinceText(at(2026, 9, 21), now)).toBe("22 days ago");
    expect(sinceText(at(2026, 6, 1), now)).toBe("2026-06-01 03:24");
  });
  it("never says a time ahead of the clock is past", () => {
    expect(sinceText(at(2026, 10, 14), now)).toBe("2026-10-14 03:24");
    expect(sinceText(0, now)).toBe("—");
  });
});

describe("copy table", () => {
  it("covers every ceremony step", () => {
    for (const step of Object.values(CeremonyStep)) {
      if (!step) continue; // the generator's zero value
      expect(stepCopy[step as keyof typeof stepCopy]?.title, step).toBeTruthy();
    }
  });
  it("covers every error code", () => {
    for (const code of Object.values(Code)) {
      if (!code) continue;
      expect(codeCopy[code as keyof typeof codeCopy], code).toBeTruthy();
      expect(unknownCodeParts(codeText(code)), code).toBeNull();
    }
  });
  it("names an unknown code rather than inventing, and says where the log is", () => {
    expect(codeText("something.new")).toBe("Something went wrong (something.new). The log in the data folder has the details.");
  });
  it("splits the fallback around its code, wherever it stands in the line", () => {
    expect(unknownCodeParts(codeText("something.new"))).toEqual(["Something went wrong (", "something.new", "). The log in the data folder has the details."]);
    expect(unknownCodeParts(`Adding 3 files: ${codeText("x.y")}`)).toEqual(["Adding 3 files: Something went wrong (", "x.y", "). The log in the data folder has the details."]);
    expect(unknownCodeParts(codeText(Code.CodeArchiveDirty))).toBeNull();
  });
});

describe("retries", () => {
  it("never says 0 attempts", () => {
    expect(retriesText({ retries: 0, retriesKnown: true, verified: false })).not.toMatch(/0/);
    expect(retriesText({ retries: 0, retriesKnown: false, verified: false })).not.toMatch(/0/);
    expect(retriesText({ retries: 0, retriesKnown: true, verified: true })).not.toMatch(/0/);
  });
  it("shows the count before the PIN is asked", () => {
    expect(retriesText({ retries: 3, retriesKnown: true, verified: false })).toBe("3 tries left");
    expect(retriesText({ retries: 1, retriesKnown: true, verified: false })).toBe("1 try left");
  });
  it("says unknown when the card is verified", () => {
    expect(retriesText({ retries: 3, retriesKnown: true, verified: true })).toMatch(/unknown/);
  });
});

describe("what a finished reclaim says", () => {
  it("names the bytes the file system got back", () => {
    expect(reclaimedText(1.2 * 1024 * 1024 * 1024)).toBe("Reclaimed 1.2 GB");
  });
});

// The Type column (APP.md §6): a folder, the table's name for an
// extension, "<EXT> file" for one without a name, *File* for none.
describe("the type column", () => {
  it("names a folder, whatever its name says", () => {
    expect(typeLabel("photos.zip", true)).toBe("Folder");
  });
  it("names the common extensions and says the extension for the rest", () => {
    expect(typeLabel("IMG_7201.jpg", false)).toBe("JPEG image");
    expect(typeLabel("DJI_0042.MP4", false)).toBe("MP4 video");
    expect(typeLabel("notes.txt", false)).toBe("Text document");
    expect(typeLabel("a.tar.gz", false)).toBe("Gzip archive");
    expect(typeLabel("model.blend", false)).toBe("BLEND file");
  });
  it("is File for a name with no extension", () => {
    expect(typeLabel("README", false)).toBe("File");
    expect(typeLabel(".env", false)).toBe("File");
  });
  it("keeps every key folded, since the extension is read folded", () => {
    for (const k of Object.keys(extensionNames)) expect(k).toBe(k.toLowerCase());
  });
});

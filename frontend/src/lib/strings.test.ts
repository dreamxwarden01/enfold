import { describe, expect, it } from "vitest";
import { CeremonyStep, Code } from "../../bindings/github.com/dreamxwarden01/enfold/internal/app";
import * as strings from "./strings";
import * as validate from "./validate";
import { archivePageCopy, archivesCopy, bootCopy, leadText, tamperedLead, ceremonyCopy, chipCopy, closeCopy, codeCopy, codeText, collisionCopy, commandCopy, compareCopy, compressedText, conflictCopy, refusedCopy, lockedOpenText, dialogCopy, extensionNames, extractCopy, lockCopy, purgedText, reclaimedText, retriesText, revealCopy, sinceText, stepCopy, tamperedCopy, technicalCopy, typeLabel, unknownCodeParts, vaultCopy, warningCopy } from "./strings";

// ---- One word per thing (APP.md §7, ruled 2026-10-17) -------------------
//
// The format's words never reach a main surface: they stand in
// technicalCopy (Technical details, "Why?", "How this works") and nowhere
// else. Each banned term carries the word the screen uses instead.
const banned: [RegExp, string][] = [
  [/\bkeystores?\b/i, "the file is the vault"],
  [/\bKeys page\b/i, "the page is Vault"],
  [/\bslots?\b/i, "an unlock method"],
  [/\bways? (in|into|to unlock)\b/i, "an unlock method"],
  [/\bentangle(d|ment)?\b/i, "Password with every YubiKey"],
  [/\bsuperblocks?\b/i, "Technical details only"],
  [/\bregistry\b/i, "Technical details only"],
  [/\bKIDs?\b/, "Technical details only"],
  [/\bVMKs?\b/, "Technical details only"],
  [/\bsession keys?\b/i, "Opening the vault…"],
  [/\bgenerations?\b/i, "Technical details only"],
  [/\bseq\b/, "Technical details only"],
  [/\bzstd\b/i, "Compressed"],
  [/\benvelopes?\b/i, "Technical details only"],
  [/\brotat(e|es|ed|ing|ion)\b/i, "Re-key"],
  [/\benrol(l|ls|led|ling|ment)?\b/i, "add an unlock method"],
  [/\bImport records\b/i, "Import archives from a backup…"],
  [/\bImport a (copy|backup)\b/i, "Replace this vault with a copy…"],
  [/\bForget key\b/i, "Forget archive…"],
  [/\bCheck files\b/i, "Find missing files"],
  [/\bCheck (a|that a) backup\b/i, "Inspect a backup… / Test that a backup opens…"],
  // The archive's hash checks nothing inside the file (direction 9).
  [/\b(Verify|Verifying|Last verified)\b/, "Refresh fingerprint / Fingerprint"],
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
  it("lets the technical-details table, and only it, keep them", () => {
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
    const opensNothing = ["open", "close", "closeArchive", "closeAll", "hide", "unhide", "restore", "refreshFingerprint", "showInExplorer", "findMissing", "reopen", "lockNow", "unlock", "forceClose", "forceCloseAll", "copyName"];
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
    const asks = [d.deleteArchive.title("Photos 2024"), d.forgetArchive.title("Photos 2024"), d.compact.title("Photos 2024"), d.rekeyArchive.title("Photos 2024"), d.rekeyVault.title, d.removeUnlockMethod.title("Desk key"), d.passwordOn.title, d.passwordChange.title, d.passwordOff.title];
    for (const t of asks) expect(t).toMatch(/\?$/);
    const bodies = [d.deleteArchive.body("D:\\Archives\\photos-2024.efd"), d.forgetArchive.body("2026-11-12"), d.compact.body, d.rekeyArchive.body, d.rekeyVault.body, d.removeUnlockMethod.body("Desk key"), d.passwordOn.body, d.passwordChange.body, d.passwordOff.body];
    for (const b of bodies) expect(b.split(/(?<=[.?!])\s+(?=[A-Z])/).length, b).toBeLessThanOrEqual(2);
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

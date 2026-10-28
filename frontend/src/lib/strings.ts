// The one table of copy (APP.md §7): ceremony steps, error codes, and the
// rules the words must respect — retries are shown before a PIN is asked,
// and never as "0 attempts".
//
// One word per thing, consequences first (APP.md §7, ruled 2026-10-17;
// direction 8 of the design review). strings.test.ts holds every value
// here to it.
//
// The glossary. On screen:
// - *vault* is the .eks file and what it holds. Never "keystore", and no
//   "Keys page": the rail item and the page are *Vault*.
// - *unlock method* is a YubiKey, the password or a recovery key. Never
//   "slot", "way in", "way to unlock" or "kind": "Add an unlock
//   method…", "4 unlock methods".
// - *backup* is an exported copy of the vault; a *copy* is any other file
//   of this vault, and a backup counts as one.
// - *archive key*, KID, key versions, the slot region, superblock,
//   registry, generation, seq, VMK and session keys are format words:
//   they appear only in technicalCopy below — an archive's *Details*
//   dialog, a "Why?" or a "How this works" disclosure — never on a main
//   surface (the two registers).
// - *Details…* opens an archive's second-register dialog (the row menu,
//   the pane's and the bottom panel's action, Alt+Enter). Never
//   "Technical details" (ruled 2026-10-26) and never "Properties", which
//   promises Explorer's whole sheet.
// - The vault's password with every YubiKey is *Password with every
//   YubiKey*, never "entangled".
// - *fingerprint* is the archive file's SHA-256 the registry keeps (APP.md
//   §2.3, Verify): it tells this file from another copy of it, and checks
//   nothing inside it, so it is never "verified". The command that takes
//   it again is *Refresh fingerprint* (direction 9, ruled 2026-10-18).
//
// The verb table (commandCopy). A command names what happens and ends
// with "…" when a dialog or a picker follows; the dialog's primary
// button repeats the verb with its object (*Forget archive…* →
// [Forget archive]). Hide / Unhide are the list's alone; *Forget
// archive…* / *Restore* make a record soft for 30 days and back; *Re-key
// archive…* and *Re-key vault…* are two operations with two names;
// *Replace recovery keys…* makes a new recovery key for each one the vault
// has and re-keys the vault in the same step, the plural whatever the
// count, since a command names the act (ruled 2026-10-27); the
// backup verbs are exactly four — *Inspect a backup…*, *Test that a backup
// opens…*, *Import archives from a backup…*, *Replace this vault with a
// copy…* (*Replace with a copy…* on a banner's button) — and *Restore
// from a vault or backup…* is the last one's first-run form, when there
// is no vault to replace.
//
// The dialog template (dialogCopy). The title is a question that names
// the object; the body is at most two sentences, the consequence for the
// user's files first and then the safety net or the undo; the mechanism,
// when it is worth saying, goes behind a "How this works" disclosure
// (`how`).
//
// Conventions: sentence case everywhere, chips included; numerals for
// counts and times ("30 days", ruled 2026-10-14); at most one dash per line; the app's ISO
// date in lists, facts and dialogs, and a relative phrase (sinceText)
// only where recency is the point — the last unlock, the last backup.

import { CeremonyStep, Code } from "./api";
import type { CeremonyState, CodeKey, StepKey } from "./api";
import { bytes, clockTime, dateTime, ext, plural } from "./format";
import type { FingerprintKind } from "./archlist";

export interface StepCopy {
  title: string;
  body: string;
}

// stepCopy is what the ceremony panel says at each step; the token flow's
// three moments (insert, PIN, touch) are the ones the lock screen builds
// around.
export const stepCopy: Record<StepKey, StepCopy> = {
  [CeremonyStep.StepWaitingForKey]: { title: "Insert your YubiKey", body: "The YubiKey that unlocks this vault." },
  // The instruction is the heading, and the core carries on by itself
  // once one is out (ruled 2026-10-17): nothing to press but Cancel.
  [CeremonyStep.StepTwoKeys]: { title: "Two YubiKeys are in", body: "Remove one; Enfold carries on by itself." },
  [CeremonyStep.StepProbing]: { title: "Reading the YubiKey", body: "Checking that it is one of this vault's unlock methods." },
  // The page carries the button the body names (lockCopy.useRecoveryKey):
  // a sentence that offers the recovery key has something to press.
  [CeremonyStep.StepNoMatch]: { title: "This YubiKey is not one of this vault's unlock methods", body: "Try another YubiKey, or use your recovery key." },
  [CeremonyStep.StepBusy]: { title: "The YubiKey is in use", body: "Another program holds the YubiKey. Close it, or wait, then try again." },
  [CeremonyStep.StepPassword]: { title: "Password", body: "" },
  // No line of its own (ruled 2026-10-17): the retries in the label row
  // say what counting there is, and the field's reserved line holds its
  // rule or what is wrong.
  [CeremonyStep.StepPIN]: { title: "PIN", body: "" },
  // No number in the body: the wait is the YubiKey's and the blinking is
  // its clock (DESIGN.md trap 23; APP.md §6, ruled 2026-10-17).
  [CeremonyStep.StepTouch]: { title: "Touch your YubiKey now", body: "Touch it while it blinks. If it stops blinking, start again." },
  [CeremonyStep.StepBlocked]: { title: "The PIN is blocked. Use your recovery key.", body: "This YubiKey will not accept a PIN again until it is reset." },
  // What the step does in the user's words; the derivation it is lives in
  // technicalCopy (ruled 2026-10-17).
  [CeremonyStep.StepDeriving]: { title: "Unlocking", body: "Opening the vault…" },
  [CeremonyStep.StepManagementKey]: { title: "Management key", body: "This YubiKey has no PIN-protected management key. Type it as hex so Enfold can make a new key on it." },
  [CeremonyStep.StepRecovery]: { title: "Recovery key", body: "" },
  // The records ceremony ends here, with the incoming registry read on a
  // staged copy and nothing written to either file (APP.md §13).
  [CeremonyStep.StepRecords]: { title: "Archives found", body: "Choose which archives to bring into this vault." },
  [CeremonyStep.StepSwapKey]: { title: "Swap YubiKeys", body: "Remove the YubiKey that unlocked the vault, then insert the one to add." },
  [CeremonyStep.StepReleasing]: { title: "Releasing the YubiKey", body: "It forgets the PIN." },
  [CeremonyStep.StepDone]: { title: "Done", body: "" },
  [CeremonyStep.StepFailed]: { title: "Could not continue", body: "" },
  // A replacement's new recovery key, shown in the recovery key's dialog
  // in keep mode (APP.md §13, ruled 2026-10-27): nothing is the vault's
  // until the confirmation, so the line says so.
  [CeremonyStep.StepKeepKey]: { title: "Keep the new recovery key", body: "Print it, save it as a text file, or write it down. Nothing changes until you confirm." },
  // The replacement's one confirmation before its commit (ruled
  // 2026-10-27, R-B): the panel draws each new key beside the one it
  // replaces under replaceCopy.ready, which is this title.
  [CeremonyStep.StepConfirm]: { title: "Ready to replace", body: "" },
};

// stepText is stepCopy for any step value, the generator's zero included.
export function stepText(step: CeremonyStep): StepCopy {
  return (stepCopy as Record<string, StepCopy>)[step] ?? { title: "", body: "" };
}

// listJoin says a list the way a sentence does: "a", "a and b", "a, b and
// c". One shape for the groups mistyped and the recovery keys a
// replacement showed.
function listJoin(items: readonly (string | number)[]): string {
  return items.length < 2 ? items.join("") : `${items.slice(0, -1).join(", ")} and ${items[items.length - 1]}`;
}

// What waits while a retirement is owed (FORMAT R41, ruled 2026-10-27,
// R-A): one list for an act's bar (ceremonyCopy.retireNotFinished) and for
// the refusal of a held act (vault.retire_owed), naming by their on-screen
// words exactly the acts the core holds, then what does not wait. The
// content of an archive is not held, so the last sentence says the files
// inside can still be changed by the user, never that they change by
// themselves.
export const retireHeld =
  "changes to the unlock methods, re-keying the vault, backups, and creating, importing, re-keying, forgetting, restoring or deleting archives wait; Enfold tries again at each of them and at the next unlock. You can still open your archives and change the files inside them.";

// What a ceremony says beyond its step's own line, on the lock screen's
// panel and the Vault page's alike (APP.md §6, ruled 2026-10-17). The
// PIN's step is named in the order its fields stand: the PIN first, as the
// core asks it (APP.md §2.2), then the vault's password when the vault
// asks for one.
export const ceremonyCopy = {
  pinAndPassword: "PIN and password",
  pin: "PIN",
  password: "Password",
  recoveryKey: "Recovery key",
  managementKey: "Management key",
  managementKeyField: "Management key (hex)",
  vaultPassword: "The vault's password",
  choosePassword: "Choose a password",
  chooseVaultPassword: "Choose the vault's password",
  wrongPin: "Wrong PIN.",
  wrongPassword: "Wrong password.",
  // No match where nothing offers the recovery key (an import's proof, a
  // create, a dialog's ceremony): the line says what can be done instead
  // of naming a button the screen does not have.
  noMatchWithoutRecovery: "Try another YubiKey.",
  // A blocked PIN on the lock screen, on the PIN's step where it arises
  // (APP.md §6, ruled 2026-10-17): the lead says whose PIN, and the danger
  // bar says in one sentence what it means and, where the panel offers
  // it, the way out.
  blockedLead: "This YubiKey's PIN is blocked",
  blockedMeans: "It will not accept a PIN until it is reset.",
  blockedMeansWayOut: "It will not accept a PIN until it is reset; your recovery key still opens the vault.",
  // The recovery key's cells (APP.md §6, ruled 2026-10-17): the group to
  // fix by its number, and how far the typing is.
  groupsMistyped: (groups: number[]): string =>
    groups.length === 1
      ? `Group ${groups[0]} does not check out. Compare its six digits with the sheet.`
      : `Groups ${listJoin(groups)} do not check out. Compare their digits with the sheet.`,
  groupsTyped: (n: number, of: number): string => `${n} of ${of} groups`,
  // The dialogs' enrolment: two halves, not three steps (ruled
  // 2026-10-17) — proving it is you, then adding the new unlock method.
  halves: ["Prove it is you", "Add the new unlock method"],
  halvesLabel: "Adding an unlock method",
  // The wait of a dialog's ceremony (APP.md §13, ruled 2026-10-17): the
  // vault's YubiKeys by name, and for an enrolment what comes next; the
  // other acts prove it is you and say nothing of a next step.
  insertProve: "Insert a YubiKey that already unlocks this vault",
  insertProveLine: (labels: string[], enrol: boolean): string =>
    [labels.length ? `${labels.join(", or ")}.` : "", enrol ? "The new unlock method comes next." : ""].filter(Boolean).join(" "),
  // A typed proof (a vault with no YubiKey proves it is you with its
  // password): headed as the YubiKey's wait is, with what to type and why
  // (APP.md §13, ruled 2026-10-17) — a bare field under the dialog's title
  // said neither.
  typeProve: "Type a password that already unlocks this vault",
  typeProveLine: (enrol: boolean): string => (enrol ? "The new unlock method comes next." : "It proves it is you."),
  insertNew: "Now insert the new YubiKey",
  insertNewLine: "The one to add to this vault.",
  // An enrolment's swap: the YubiKey that proved it is you comes out
  // first, so the same one is not added twice (keys.go enrollToken). The
  // core's own InsertLabel ("the key to enroll") is never shown.
  swapLead: (label: string): string => (label ? `Remove ${label}` : "Remove the YubiKey"),
  swapLine: "Then insert the new YubiKey. Enfold carries on by itself.",
  // The proof's derivation in a dialog: the vault is open already, so
  // nothing is being opened.
  checking: "Checking that it is you…",
  // The last try in a dialog: the consequence alone, since the dialog
  // offers no recovery key (ruled 2026-10-17).
  lastTryHere: "A wrong PIN now blocks this YubiKey's PIN until it is reset.",
  // The end of a dialog's ceremony, by kind: what is now true, over the
  // dialog's Close.
  // A password change is its own act with its own end (kind "password");
  // turning the password on or off without a re-key keeps the generic
  // "Done", there being no entry for kind "entangle".
  doneTitle: {
    enroll: "Unlock method added",
    remove: "Unlock method removed",
    rotate: "Vault re-keyed",
    export: "Backup saved",
    password: "Password changed",
  } as Record<string, string>,
  // An act that re-keyed the vault in the same step as its own change
  // says both (ruled 2026-10-27; CeremonyState.rekey). doneTitleOf picks.
  doneRekeyed: {
    remove: "Unlock method removed and vault re-keyed",
    password: "Password changed and vault re-keyed",
    entangle: "Password turned on and vault re-keyed",
    replace: "Recovery keys replaced and vault re-keyed",
    replaceOne: "Recovery key replaced and vault re-keyed",
  } as Record<string, string>,
  // What is being done once the proof has passed (CeremonyState.phase):
  // the change and its re-key under "commit", the backup asked for under
  // "backup" (APP.md §13). phaseLeadOf picks.
  phaseLead: {
    remove: "Removing the unlock method…",
    removeRekey: "Removing the unlock method and re-keying the vault…",
    password: "Changing the password…",
    passwordRekey: "Changing the password and re-keying the vault…",
    entangle: "Updating every YubiKey…",
    entangleRekey: "Turning the password on and re-keying the vault…",
    rotate: "Re-keying the vault…",
    replaceRekey: "Replacing the recovery keys and re-keying the vault…",
    replaceRekeyOne: "Replacing the recovery key and re-keying the vault…",
    backup: "Saving the backup…",
  } as Record<string, string>,
  // Under the danger bar of a failed removal, password change, turning on
  // or off, re-key or replacement: a failure before the change landed
  // leaves the file as it was, and no backup is written before the change.
  // Not for vault.indeterminate or vault.conflict, whose own words say a
  // save could not be confirmed (lib/lockscreen.ts nothingChanged).
  nothingChanged: "Nothing was changed.",
  // The backup an act was asked for, not written though the change stands
  // (CeremonyState.backupError): why, then the way to take one now.
  backupNotSaved: (why: string): string => `The backup was not saved. ${why} Export one from Backups and copies.`,
  // The retirement of the file's earlier state did not land (FORMAT R41);
  // the change stands, and what writes a record or a secret waits for it
  // while the content of an archive does not (ruled 2026-10-27, R-A).
  // lead is retireLeadOf's; why is the code's own line; backupToo, the
  // act's backup skipped for it (backupError vault.retire_owed), which has
  // no bar of its own, so this one says it.
  retireNotFinished: (lead: string, why: string, backupToo: boolean): string =>
    `${lead} ${why} Until Enfold has cleared it, ${retireHeld}${backupToo ? " The backup you asked for waits too." : ""}`,
  // The bar's first sentence, by what is left: the state before the change,
  // with what a copy taken now opens with; or only earlier versions of the
  // list of archives (CeremonyState.retireRemnant), which no copy opens
  // with the method withdrawn, said by whether the act re-keyed.
  retireLead: {
    state: (what: string): string => `The vault file still holds its state from before this change${what ? `: a copy of it taken now ${what}` : ""}.`,
    remnantOldKey: "The vault file still holds earlier versions of its list of archives, which the vault's old key reads.",
    remnant: "The vault file still holds earlier versions of its list of archives.",
  },
  // What a copy of the file taken now still opens with, by the act. None
  // after an enrolment or turning the password off, which withdrew
  // nothing: the state before asked for more, not less.
  retireWhat: {
    remove: "still opens with the unlock method removed",
    password: "still opens with a YubiKey and the old password",
    entangle: "still opens with a YubiKey alone",
    replace: "still opens with the replaced recovery keys",
    replaceOne: "still opens with the replaced recovery key",
    rotate: "still holds the vault's old key",
  } as Record<string, string>,
  // A failed replacement, by what is known of the keys it showed
  // (lib/rekey.ts replaceResult): never added, so the sheets name keys
  // that never existed; or not known, so nothing says to destroy them. A
  // count of one is singular, and "both sheets" are the old and the new.
  keysNeverAdded: "The recovery keys shown were never added: destroy what you kept of them.",
  keysNeverAddedOne: "The recovery key shown was never added: destroy what you kept of it.",
  keysWorkIfListed: "The new recovery keys work only if the Vault page lists their IDs after you reopen it: keep every sheet until you have looked.",
  keysWorkIfListedOne: "The new recovery key works only if the Vault page lists its ID after you reopen it: keep both sheets until you have looked.",
  // The end's next step (ruled 2026-10-27): one chain, the password first,
  // and an unlock method first where only recovery keys are left
  // (lib/rekey.ts followUp picks one; the button names its command).
  followUp: {
    addFirst: "Add the new YubiKey now; then, if an earlier copy of the vault file may be out there, replace the recovery keys.",
    addFirstOn: "Add the new YubiKey now; then, if an earlier copy of the vault file may be out there, change the password and replace the recovery keys.",
    changeFirst: "If an earlier copy of the vault file may be out there, change the password, then replace the recovery keys.",
    changeFirstRecovery: "If an earlier copy or backup may be out there, change the password, then replace the other recovery keys.",
    replace: "If an earlier copy of the vault file may be out there, replace the recovery keys too.",
    replaceRecovery: "If an earlier copy or backup may be out there, replace the other recovery keys too.",
    // The same four for one recovery key left: a count of one is singular
    // everywhere (DECISIONS 2026-09-09), and the acceptance scenario's
    // vault has exactly one. followUp picks by the end's committed count.
    // addFirst and addFirstOn need none: a vault left with recovery keys
    // alone keeps at least 2 of them (the two-method rule).
    changeFirstOne: "If an earlier copy of the vault file may be out there, change the password, then replace the recovery key.",
    changeFirstRecoveryOne: "If an earlier copy or backup may be out there, change the password, then replace the other recovery key.",
    replaceOne: "If an earlier copy of the vault file may be out there, replace the recovery key too.",
    replaceRecoveryOne: "If an earlier copy or backup may be out there, replace the other recovery key too.",
    // After a replacement, the password on and not changed yet in the chain.
    changeAfterReplace: "If an earlier copy of the vault file may be out there, change the password too: the vault's own record of it was readable the same way.",
    // Added while the end carries a retirement that did not land: the next
    // act's begin is the retry, which the bar above says waits.
    retryFirst: " Enfold first tries again to finish clearing the vault file.",
  },
  // After an enrolment of a YubiKey begun with Use recovery key instead,
  // and after no other: the lost YubiKey is the next thing to remove.
  afterRecoveryProof: "Now remove the lost YubiKey: Remove… on its card asks why, and re-keys the vault when it was lost.",
  // Whose recovery key the panel asks for, and why one proves it (APP.md
  // §13): the kinds that always ask one, by kind; the others by cause, a
  // YubiKey left out of a removal, the switch, or a vault of recovery keys
  // alone (lib/rekey.ts recoveryNoteOf), never by kind, since adding a
  // password to such a vault would read "no YubiKey is at hand".
  recoveryNote: {
    verify: "The backup's recovery key. Nothing here changes.",
    import: "The copy's own recovery key: it replaces this vault only once it opens.",
    records: "This vault's recovery key first, if the file is a copy of this vault; if it does not open the file, the file's own recovery key is asked for next.",
    enroll: "Your recovery key proves it is you, since no YubiKey of this vault is at hand. The new YubiKey comes next.",
    enrollRecovery: "Your recovery key proves it is you, since no YubiKey of this vault is at hand. The new recovery key comes next.",
    remove: "One of this vault's recovery keys proves it is you, since the YubiKey being removed is not asked for.",
    only: "This vault has only its recovery keys, so one of them proves it is you.",
    nextMethod: " The new unlock method comes next.",
    // A vault never set up has one recovery key, and its Vault page adds
    // the first unlock method (vaultCopy.setupLine): one is singular.
    onlyOne: "This vault has only its recovery key, so it proves it is you.",
  } as Record<string, string>,
  recoveryRefused: {
    file: "That is not that file's recovery key. Check the digits against its paper.",
    records: "That recovery key was refused. Check the digits against the sheet they were printed on.",
    vault: "That is not this vault's recovery key. Check the digits against the paper.",
  },
  // The note under a password being chosen: the lock screen's create and
  // first unlock method, the panel's new unlock method, the entangle acts.
  choosePasswordNote: "Longer is better; a passphrase of several words is best.",
  chooseMethodPasswordNote: "The new unlock method's password. Longer is better; a passphrase of several words is best.",
  chooseVaultPasswordNote: "Every YubiKey in this vault will ask for it. Longer is better; a passphrase of several words is best.",
  // A slot that asks for more than one touch counts them.
  touchCount: (n: number): string => `Touch ${n} on this YubiKey`,
  // A backup check's outcome, one wording on the lock screen and in the
  // panel (it read "Opens. …" in the panel).
  verified: (archives: number): string => `The backup opens. ${plural(archives, "archive")} inside.`,
};

// doneTitleOf is the end of a dialog's ceremony, what is now true (APP.md
// §6, Controls), by kind and whether the act re-keyed (CeremonyState.rekey);
// a replacement always re-keys, and one key is singular (keepCount).
// Undefined where the end is the generic "Done" (turning the password on
// or off without a re-key).
export function doneTitleOf(c: { kind: string; rekey?: boolean; keepCount?: number }): string | undefined {
  if (c.kind === "replace") return (c.keepCount ?? 0) === 1 ? ceremonyCopy.doneRekeyed.replaceOne : ceremonyCopy.doneRekeyed.replace;
  return (c.rekey ? ceremonyCopy.doneRekeyed[c.kind] : undefined) ?? ceremonyCopy.doneTitle[c.kind];
}

// phaseLeadOf is the lead once the proof has passed, by the act's phase
// (CeremonyState.phase); undefined during the proof, where the panel says
// it is checking that it is you.
export function phaseLeadOf(c: { kind: string; rekey?: boolean; phase?: string; keepCount?: number }): string | undefined {
  if (c.phase === "backup") return ceremonyCopy.phaseLead.backup;
  if (c.phase !== "commit") return undefined;
  if (c.kind === "replace" && c.keepCount === 1) return ceremonyCopy.phaseLead.replaceRekeyOne;
  // An enrolment's commit says what its second half's wait already says: a
  // recovery key's enrolment has no second half, and would otherwise read
  // "Checking that it is you…" while its new unlock method is written (the
  // core sets the phase for every act that changes the unlock methods).
  if (c.kind === "enroll") return lockCopy.derivingLead.adding;
  return ceremonyCopy.phaseLead[c.rekey ? `${c.kind}Rekey` : c.kind];
}

// retireLeadOf is the retirement bar's first sentence: earlier versions of
// the list of archives alone, worded by whether the act re-keyed; else the
// state before the change, with what a copy taken now opens with, and
// nothing more after an enrolment or turning the password off (after
// says the switch is off), which withdrew nothing.
export function retireLeadOf(c: { kind: string; rekey?: boolean; keepCount?: number; retireRemnant?: boolean; after?: { entangled: boolean } | null }): string {
  if (c.retireRemnant) return c.rekey || c.kind === "rotate" ? ceremonyCopy.retireLead.remnantOldKey : ceremonyCopy.retireLead.remnant;
  if (c.kind === "enroll" || (c.kind === "entangle" && c.after?.entangled === false)) return ceremonyCopy.retireLead.state("");
  const key = c.kind === "replace" && c.keepCount === 1 ? "replaceOne" : c.kind;
  return ceremonyCopy.retireLead.state(ceremonyCopy.retireWhat[key] ?? "");
}

// codeCopy is what an error code says. Every code has an entry; the test
// checks it.
export const codeCopy: Record<CodeKey, string> = {
  [Code.CodeInternal]: "Something went wrong inside Enfold. The log in the data folder has the details.",
  [Code.CodeNoVault]: "No vault is open.",
  [Code.CodeVaultLocked]: "The vault is locked.",
  // The reopen banner's line (ruled 2026-10-17): what happened, then what
  // to do, naming what is reopened — the lock screen's broken panel says
  // it over *Reopen the vault*, the page's banner over *Reopen*.
  [Code.CodeVaultBroken]: "A save could not be confirmed. Reopen the vault to see which version was kept.",
  [Code.CodeVaultBusy]: "The vault file is open in another Enfold. Close it first.",
  // The three tampered codes say the same thing on a main surface: the
  // remedy is one, and which check failed is the Details dialog's and
  // the log's (technicalCopy.tamperedWhy, ruled 2026-10-17).
  [Code.CodeVaultTampered]: "This vault file does not match itself, so changes to it are off. Replace it with a copy you trust.",
  [Code.CodeTamperedHash]: "This vault file does not match itself, so changes to it are off. Replace it with a copy you trust.",
  [Code.CodeTamperedGeneration]: "This vault file does not match itself, so changes to it are off. Replace it with a copy you trust.",
  // Since Revision 2 no unlock method is left holding an old key: this is
  // the vault file's second superblock copy, damaged (FORMAT.md §5); the
  // format words are technicalCopy.staleWhy.
  [Code.CodeVaultStale]: "Part of the vault file was damaged, and Enfold used its spare copy. The next change repairs it.",
  [Code.CodeVaultNotFound]: "The vault file was not found.",
  [Code.CodeVaultInvalid]: "This is not a vault file, or it is damaged.",
  [Code.CodeVaultExists]: "A vault, or a file, is already at that place. Replacing it must be confirmed first.",
  [Code.CodePasswordShort]: "The password must be at least 8 characters.",
  [Code.CodeVaultUnlocked]: "Lock the vault first.",
  [Code.CodeSettingsUnsaved]: "The vault's place could not be recorded in the settings. Check the data folder is writable, or Enfold may open the previous vault next time.",
  [Code.CodeSetupNeeded]: "This vault has only its recovery key so far. Finish setting it up first.",
  // A retirement still owed (FORMAT R41, ruled 2026-10-27): one cause,
  // true wherever the owed state came from (a change, a re-key alone, an
  // older file's first unlock here, earlier versions of the list of
  // archives alone), and every act that waits for it. It reads right as
  // the standing notice and as the refusal of a held act.
  [Code.CodeRetireOwed]: `Enfold has not finished clearing the vault file's earlier state. Until it has, ${retireHeld}`,
  // Finish setting up refused before its ceremony while the vault's
  // password is on: its first unlock method cannot be written then, and a
  // recovery key and the Vault page can (ruled 2026-10-27). The lock
  // screen's greyed Finish setting up… says it too.
  [Code.CodeSetupPasswordOn]: "This vault's password is on, so its setup cannot be finished here. Unlock with a recovery key, then add a YubiKey on the Vault page.",
  [Code.CodeArchivesOpen]: "Close the open archives first: their saves would land in the wrong vault.",
  [Code.CodeNeedsUnlock]: "Unlock the vault first.",
  [Code.CodeAuth]: "That did not open the vault.",
  [Code.CodeNoSlot]: "None of this vault's unlock methods matches.",
  [Code.CodePasswordNeeded]: "This YubiKey also needs the vault's password.",
  [Code.CodeInvariant]: "At least 2 unlock methods are always kept, one of them a recovery key.",
  [Code.CodeSlotPolicy]: "A password on its own cannot unlock a vault that also has a YubiKey.",
  [Code.CodeDuplicateSlot]: "This YubiKey is already one of this vault's unlock methods.",
  [Code.CodeNoRecoverySlot]: "A vault always keeps a recovery key.",
  [Code.CodeSlotNotFound]: "That unlock method no longer exists.",
  [Code.CodeEscrowMissing]: "The vault keeps no copy of this recovery key. Add a new recovery key, then remove this one.",
  [Code.CodeEscrowMismatch]: "The vault's copy of this recovery key does not match the key itself, so it is not shown.",
  [Code.CodeRecoveryPlace]: "Not there: keep the recovery key out of Enfold's own folder and the vault's, and give it a file name of its own.",
  [Code.CodeVaultKept]: "This PC already has its vault, and Enfold keeps a single one. Replace it with a copy instead; only a damaged vault is rebuilt.",
  // Never worded as damage (APP.md §13). "A later version of this one" is
  // a backup from a generation this vault no longer keeps — a vault rolled
  // back to an older copy; technicalCopy.notThisVaultWhy says so.
  [Code.CodeNotThisVault]: "This vault cannot open that file: it belongs to another vault, or to a later version of this one. Its own recovery key opens it.",
  [Code.CodeConflict]: "The vault changed under Enfold. Reopen it.",
  [Code.CodeIndeterminate]: "A save could not be confirmed. Reopen the vault to see which version was kept.",
  [Code.CodeCeremonyRunning]: "An unlock is already in progress.",
  [Code.CodeReleasing]: "The YubiKey is being released; try again in a moment.",
  [Code.CodeNoCeremony]: "Nothing is waiting for that.",
  [Code.CodeStalePrompt]: "That prompt has passed.",
  [Code.CodeCancelled]: "Cancelled.",
  [Code.CodeTokenNoService]: "The Smart Card service is not running. Windows starts it when a YubiKey is plugged in; if it stays off, start the Smart Card service.",
  [Code.CodeTokenNoReader]: "No YubiKey is attached.",
  [Code.CodeTokenNoCard]: "The YubiKey went away. Insert it again to go on.",
  [Code.CodeTokenBusy]: "Another program holds the YubiKey.",
  [Code.CodeTokenNoPIV]: "This device has PIV turned off, or it is not a YubiKey.",
  [Code.CodeTokenUnsupport]: "This YubiKey's firmware is too old (5.3 or later is needed).",
  [Code.CodeTokenNoKey]: "This YubiKey is not one of this vault's unlock methods.",
  [Code.CodeTokenNotUsable]: "This YubiKey is not set to need both a PIN and a touch, so Enfold will not use it.",
  [Code.CodeTokenPIN]: "Wrong PIN.",
  [Code.CodeTokenProof]: "The YubiKey did not prove itself: what it computed does not match its own public key. It was not added.",
  [Code.CodeTokenPINBlocked]: "The PIN is blocked. Use your recovery key.",
  [Code.CodeTokenPINAgain]: "The YubiKey asks for the PIN again.",
  [Code.CodeTokenTouch]: "The YubiKey was not touched in time. Try again, and touch it when it blinks.",
  [Code.CodeTokenPending]: "The YubiKey is still answering the cancelled request. Touch it, or pull it out, to end that now.",
  // The touch is kept for five minutes so that a wrong password costs no
  // second one; past that the ceremony ends where a cancel ends it and the
  // lock screen carries this line (APP.md §2.2, the ruling of 2026-09-08).
  [Code.CodePasswordDeadline]: "The password was not given within 5 minutes; unlock again with the YubiKey.",
  [Code.CodeTokenTooMany]: "Too many operations on one YubiKey. Start again.",
  [Code.CodeTokenReset]: "The YubiKey was released but could not be reset, so it may still be PIN-verified.",
  // A slot here is the YubiKey's own (PIV), the word Yubico's tools show,
  // not the vault's: strings.test.ts lists the two as exceptions.
  [Code.CodeTokenOccupied]: "That slot on the YubiKey holds something already.",
  [Code.CodeTokenFull]: "Every slot on the YubiKey is occupied. Nothing is overwritten.",
  [Code.CodeTokenNoMgmtKey]: "The YubiKey stores no PIN-protected management key.",
  [Code.CodeTokenMgmtKey]: "The management key was refused.",
  [Code.CodeTokenTwoKeys]: "Two YubiKeys are inserted. Remove one.",
  [Code.CodeArchiveNotOpen]: "The archive is not open.",
  [Code.CodeArchiveOpen]: "The archive is already open.",
  // Not a state the user can be in since 2026-09-09 — an archive is clean
  // between operations — but the code stands for the archive layer's
  // "a transaction is already open on this handle", which is a bug.
  [Code.CodeArchiveDirty]: "Something went wrong inside Enfold: an operation was begun on an archive that already had one. The log in the data folder has the details.",
  [Code.CodeArchiveBusy]: "The archive is busy with another operation.",
  [Code.CodeArchiveCompacting]: "The archive is being compacted.",
  [Code.CodeArchiveNeedsReopen]: "The archive needs to be reopened.",
  [Code.CodeArchiveKey]: "This vault cannot open this archive.",
  [Code.CodeArchiveReadOnly]: "The archive is read-only.",
  [Code.CodeArchiveNotFound]: "The archive is not in this vault.",
  [Code.CodeArchiveInvalid]: "The file is not an archive, or it is damaged.",
  // The archive layer creates with O_EXCL: Enfold never overwrites a file
  // it did not make, whatever the save dialog's own replace prompt said
  // (APP.md §3, §6).
  [Code.CodeArchiveExists]: "A file is already there. Enfold never overwrites; choose another name.",
  [Code.CodeArchiveMissing]: "The archive file is not where the vault last saw it.",
  // The copy-mismatch banner (ruled 2026-10-17): what it is likely to be,
  // then what the next save does.
  // The command that also makes it the current one is named, since the
  // fingerprint is what tells the copies apart (direction 9).
  [Code.CodeArchiveCopyMismatch]: "This is not the copy the vault saved last; maybe an older backup. Your next save, or Refresh fingerprint, makes it the current one.",
  [Code.CodeArchiveForgotten]: "This archive is forgotten. Restore it to use it again; 30 days after it was forgotten it can no longer be opened.",
  [Code.CodeArchiveNotThisOne]: "The file there is not this archive: another archive, or a copy someone made. Nothing was deleted.",
  [Code.CodeArchiveUnreachable]: "The folder that file is in could not be reached. Nothing was deleted and nothing was forgotten.",
  [Code.CodeArchiveDeleteFailed]: "The file is this archive, and it could not be deleted. Nothing was forgotten: the archive is forgotten only once its file is gone.",
  // The two refusals of a file opened from Explorer (APP.md §14): an
  // archive whose keys are somewhere else — the page offers *Import
  // archives from a backup…* beside this line — and a file that is not one
  // at all.
  [Code.CodeKeyNotInVault]: "This archive is not in your vault.",
  [Code.CodeNotAnArchive]: "That file is not an Enfold archive, or it could not be read.",
  // The line without the path, for a list. What the InfoBar says names the
  // file the handle is on (openElsewhereText).
  [Code.CodeOpenElsewhere]: "This archive is already open from another file. Close it there first.",
  [Code.CodeDescriptionLong]: "The description is too long: at most 1 024 bytes.",
  [Code.CodeArchiveName]: "That name cannot be used: a name is needed, and at most 1 024 bytes of text.",
  [Code.CodeFileExists]: "Something with that name is already there.",
  [Code.CodeFileNotFound]: "That is no longer in the archive.",
  [Code.CodeFileName]: "That name cannot be stored.",
  // The three per-item codes of the tree (APP.md §3, FORMAT.md R39).
  [Code.CodeKindMismatch]: "A file and a folder never replace one another. Skip it, or keep both.",
  [Code.CodeMoveIntoSelf]: "A folder cannot be moved into itself, or into anything inside it.",
  [Code.CodeTreeBounds]: "That would put it too deep, or give it a name too long to store: at most 255 folders down, and 4 096 bytes of joined name.",
  [Code.CodeSourceChanged]: "The source file changed while it was being read.",
  [Code.CodeSourceIsVault]: "Enfold's own folder and the vault file are not yours to put in an archive, or to write over. Choose somewhere else.",
  [Code.CodeContentHash]: "The file's content does not match its record.",
  [Code.CodeNoSpace]: "Not enough space.",
  [Code.CodeDictInUse]: "The dictionary is still in use.",
  [Code.CodeOpNotFound]: "That operation is gone.",
  [Code.CodeOpCancelled]: "Cancelled.",
  [Code.CodeOpCommitting]: "The operation is already being saved; it will finish.",
  [Code.CodeOpRunning]: "An operation is still writing the vault. Wait for it to finish.",
  // The cap cannot be extended (APP.md §2.2): the way out is a fresh session.
  [Code.CodeTooSlow]: "This would not finish before the session ends. Lock and unlock to start a fresh session, then try again.",
  // A move commit of the reclaim the core runs after an edit failed: the
  // edit itself is saved, and the next qualifying commit takes the run up
  // again (APP.md §2.3). Never "the edit failed".
  [Code.CodeReclaimIncomplete]: "Saved. Reclaiming space did not finish.",
  // The two refusals of a destination (APP.md §3, ruled 2026-09-10): the
  // final name refused on placement, where a shorter one may do, and the
  // folder's path, where no name helps. The page asks per record in its
  // own words (refusedCopy); these are the codes' lines for a list.
  [Code.CodeDestinationLink]: "A link stands where this folder would go. An extract never follows one out of its destination.",
  [Code.CodeFileNameRefused]: "The destination cannot take a name this long.",
  [Code.CodeFilePathRefused]: "The folder's path is too long for this destination.",
  // The drag out of the window (APP.md §3): no native drag can run here,
  // or one is already running.
  [Code.CodeDragUnsupported]: "Dragging files out of the window is not available.",
  [Code.CodeDragBusy]: "Another drag is still running.",
  [Code.CodeParams]: "Enfold refused the request.",
  [Code.CodeIO]: "A file could not be read or written.",
};

// Shell warnings are codes the core does not define.
const extraCodes: Record<string, string> = {
  "shell.lock_detection_unavailable": "Workstation-lock detection is unavailable; Enfold polls instead and may lock a few seconds late.",
  "vault.timeouts_clamped": "The vault's stored timeouts were outside the permitted range and were replaced by the defaults.",
  // The envelope left behind by a rotation (FORMAT.md R33), in the verb
  // table's words.
  "archive.envelope_stale": "An archive's file still names its old key after a re-key; re-keying it again repairs it.",
};

// codeText is a code's copy. A code with none — one the core gained
// before its words were written — is never shown raw as "Error: <code>":
// the line says something went wrong, keeps the code for whoever reads
// the log, and says where the log is (ruled 2026-10-14).
export function codeText(code: string | undefined): string {
  if (!code) return "";
  return (codeCopy as Record<string, string>)[code] ?? extraCodes[code] ?? unknownCodeText(code);
}

export function unknownCodeText(code: string): string {
  return `Something went wrong (${code}). The log in the data folder has the details.`;
}

// unknownCodeParts splits a line that carries the fallback around its
// code — before, the code, after — so the place that renders it can set
// the code in muted mono (InfoBars.svelte); null for every other line. The
// fallback may stand after a prefix, as an operation's name does in its
// InfoBar (ops.ts opErrorLine).
export function unknownCodeParts(text: string): [string, string, string] | null {
  const lead = "Something went wrong (";
  const m = /Something went wrong \(([^()\s]+)\)\. The log in the data folder has the details\./.exec(text);
  if (!m) return null;
  const at = m.index + lead.length;
  return [text.slice(0, at), m[1], text.slice(at + m[1].length)];
}

// lockedOpenText is the locked banner's sentence on the Archives page and
// the Archive page (direction 9, ruled 2026-10-18): what an archive left
// open after a lock still is — decrypted, and still saving, since every
// operation commits to the archive file and owes the vault its receipt
// (APP.md §2.3, "What stays usable after a lock"). One archive is named;
// several are counted. With none open the page is not shown (App.svelte
// showLock), so the bare first sentence is only ever a moment's.
export function lockedOpenText(names: readonly string[]): string {
  return leadText(lockedOpenLead(names));
}

// A banner's sentence split where it is drawn: `head`, the first sentence,
// set strong, then `rest` (direction 9's mock, ruled 2026-10-18: a bar
// opens with what happened, in the strong weight, as the backups card's
// status does). The split is the copy's own, never a search for ". ",
// since an archive's name may hold one.
export interface Lead {
  head: string;
  rest: string;
}

export function leadText(l: Lead): string {
  return l.rest ? `${l.head} ${l.rest}` : l.head;
}

export function lockedOpenLead(names: readonly string[]): Lead {
  const head = "The vault is locked.";
  if (names.length === 0) return { head, rest: "" };
  if (names.length === 1) return { head, rest: `${names[0]} is still open: decrypted, and changes to it are still saved.` };
  return { head, rest: `${plural(names.length, "archive")} are still open: decrypted, and changes to them are still saved.` };
}

// ---- The archive page's own lines (APP.md §2.3, §6, ruled 2026-09-10) --
//
// The locked banner names what the lock left open (direction 9): the
// archive is still decrypted and still saves, which "stays open while you
// are here" did not say. The two titles are what stops *Delete archive…*,
// said on the button itself, and the third is what the kill switch does
// that leaving does not. *Force close* stands in the head only while the
// archive drains after its *Close* (APP.md §2.3, ruled 2026-10-18), and its
// title says what still holds it.
export const archivePageCopy = {
  lockedBanner: (name: string): string => lockedOpenText([name]),
  lockedBannerLead: (name: string): Lead => lockedOpenLead([name]),
  forceCloseServing: "A preview is still being served from this archive. Force close stops it and closes the archive now.",
  forceCloseRunning: "An operation is still running on this archive. Force close cancels it and closes the archive now.",
  closing: "This archive is closing. Nothing more can be done in it.",
  // Said beside the head's *Force close* while the archive drains (the
  // final shoot of phase 4a): why the page still stands after its Close.
  drainingRunning: "Closes when its operation ends",
  drainingServing: "Closes when the preview ends",
  deleteNeedsUnlock: "Unlock the vault first: the record is the vault's.",
  deleteTampered: "This vault file does not match itself, so changes to it are off.",
  // A greyed command's title says why it is greyed (APP.md §6).
  extractNothing: "This archive holds nothing yet",
  renameNeedsOne: "Select one item to rename",
  deleteNeedsSome: "Select what to delete",
  extractNeedsSome: "Select what to extract",
  // A row menu's greyed item while the archive drains after its *Close*
  // (direction 7): the toolbar's closing title in a few words, since the
  // menu says it under every item it stops.
  menuClosing: "This archive is closing",
  // The blank area's *Paste* (ruled 2026-10-26; lib/paste.ts): greyed with
  // this when the clipboard names no files, and — files cut in Explorer —
  // saying under it that a paste never deletes the originals: a cut is
  // added as copied.
  nothingToPaste: "Nothing to paste",
  pasteKeepsOriginals: "The originals stay where they are",
  // The foot's note (direction 3, phase 4a): the folder shown and its count;
  // the archive's figures moved to the pane at rest.
  foot: (folder: string, n: number): string => `${folder} · ${plural(n, "item")}`,
  // The head's crumbs (ruled 2026-10-14 and 2026-10-18): the path from the
  // archive's name, the folders that did not fit behind one ellipsis.
  crumbsLabel: "Location",
  folded: (n: number): string => `${plural(n, "folder")} in between`,
  // The toolbar's two menus at its right: More, which holds *Delete
  // archive…* (the Archives list's row menu offers it too), and View.
  more: "More",
  view: "View",
  previewPane: "Preview pane",
  // An empty folder (direction 3): named, with both ways to fill it under
  // the line and the drop said last, in the caption.
  emptyTitle: (folder: string): string => `${folder || "This folder"} is empty`,
  emptyDrop: "or drop files here",
};

// typePlural is a file type said of every file of it: "PDF documents",
// "disc images", "Go source files". The type's own name is the strings
// table's (typeLabel); a name that is a mass noun or ends in y takes
// "files" rather than an s, and a first word that is not a name — not an
// acronym, a product or a format — is lower-cased mid-sentence.
const massEnds = new Set(["data", "source", "text", "audio"]);
const commonFirst = new Set(["disc", "application", "library", "icon", "settings", "style", "batch", "shell", "raw", "bitmap", "web", "log", "text", "rich", "file"]);
export function typePlural(label: string): string {
  const words = label.split(" ");
  const last = words[words.length - 1].toLowerCase();
  if (commonFirst.has(words[0].toLowerCase())) words[0] = words[0].toLowerCase();
  const said = words.join(" ");
  return massEnds.has(last) || last.endsWith("y") ? `${said} files` : `${said}s`;
}

// The Archive page's pane (direction 3, phase 4a; lib/archpane.ts): at
// rest it describes the folder shown — "3 items · 9.3 KB", the bytes only
// once every row is on hand — and the archive; with several selected it
// counts them; with one it previews it. A file it cannot preview is said
// as a fact of its type, and *Extract…* stands as an action of its own,
// never as the cure for a missing preview.
export const paneCopy = {
  // An empty folder is "0 items", with no size beside it to say.
  figures: (n: number, total: number | null): string => (total === null || n === 0 ? plural(n, "item") : `${plural(n, "item")} · ${bytes(total)}`),
  // After the Unused space figure, when the strip would show it too
  // (lib/status.ts freeWorthShowing): Compact is the Archives page's.
  compactHint: "(Compact to reclaim)",
  noPreview: (name: string): string => {
    const label = typeLabel(name, false);
    return label === plainFileType ? "No preview for this file" : `No preview for ${typePlural(label)}`;
  },
  folderInside: (size: string): string => `Folder · ${size} inside`,
  previewFailed: "The preview could not be loaded",
  // A file selected while the archive drains after its Close, which the
  // pane does not ask for (every read would answer archive.not_open): the
  // row menu's words for it.
  closing: archivePageCopy.menuClosing,
  extractSome: (n: number): string => `Extract ${plural(n, "item")}…`,
};

// The viewer (direction 7, Explorer's habits; lib/viewer.ts): a file
// looked at large inside the window — its name, its place among the
// folder's files the viewer can show ("4 of 13"), the facts in one
// caption line, *Extract…* and the close. A kind the WebView cannot show
// says so as a fact of its type, in the pane's own words (paneCopy.noPreview).
export const viewerCopy = {
  label: (name: string): string => `Viewing ${name}`,
  counter: (at: number, of: number): string => `${at} of ${of}`,
  close: "Close (Esc)",
  previous: "Previous (←)",
  next: "Next (→)",
  // Under a text cut at the pane's cap (lib/viewer.ts PREVIEW_TEXT_CAP).
  truncated: "Only the first 64 KB are shown here.",
};

// Move to… (direction 7; lib/movetree.ts): the title counts what moves or
// names the one item, the tree says why a folder cannot take the move,
// and the line under it says where the move goes.
export const moveToCopy = {
  title: (n: number, name = ""): string => (n === 1 && name ? `Move ${name} to…` : `Move ${plural(n, "item")} to…`),
  tree: "Folders",
  alreadyHere: "Already here",
  intoItself: "A folder cannot move into itself",
  reading: "Reading the folder",
  cut: (limit: number): string => `More than ${limit} items: some folders are not shown`,
  open: (name: string): string => `Open ${name}`,
  to: (path: string): string => `To ${path}`,
  pathSep: " › ",
  newFolderIn: (folder: string): string => `New folder in ${folder}`,
  create: "Create",
  move: "Move",
};

// ---- The rail (APP.md §6, Rail; directions 3, 6 and 10, ruled 2026-10-18) --
//
// The vault head names the vault and its state in the lock header's own
// words (lockCopy.header); the open archive is a row of its own under
// *Archives*, closed by its × — "Close Photos 2024" — and the compact
// rail's padlock says the vault, its state and what a press does.
export const railCopy = {
  label: "Main",
  archives: "Archives",
  vaultPage: "Vault page",
  // The vault head's name when there is no vault yet to name.
  noVault: "Vault",
  closeArchive: (name: string): string => `Close ${name}`,
  openNav: "Open navigation",
  closeNav: "Close navigation",
  vaultButton: (name: string, word: string, act: string): string => [name, word, act].filter(Boolean).join(" · "),
};

// reclaimedText is what a finished *Reclaiming space* says: the bytes the
// file system got back — the archive's own shrinking (OpView.Returned) —
// said apart from the bytes the run moved, since the two are never the same
// figure (APP.md §2.3). Its caller decides when there is anything to say
// (ops.ts reclaimedLine).
export function reclaimedText(returned: number): string {
  return `Reclaimed ${bytes(returned)}`;
}

// The drag out of the window in the layer's foot (APP.md §3, ruled
// 2026-09-11 after the first real drag; the foot since direction 4):
// there from the moment the drag starts, so that a staging over in an
// instant is a change inside a line already on the screen and never a
// line that flashes. The hover
// shows the label alone, *Dragging 2 items*, since nothing is chosen yet
// (ruled 2026-10-14); from the drop on it is *Extracting 2 items*, and
// the staging adds the bar, which moves by bytes and offers
// *Cancel*; and when the bytes are written the bar simply stays full, its
// *Cancel* greyed and no words of its own — from then on Explorer's own window
// is in front and the only control there is — until DoDragDrop returns and
// the foot moves on. A finished drag out that failed is named by the verb its
// InfoBar counts with (ops.ts opLabel).
export const dragOutCopy = {
  dragging: "Dragging",
  extracting: "Extracting",
  name: "Dragging out",
};

// The foot's words for a running operation (ops.ts opLabel),
// here so the glossary's walk sees them. A re-key is named by the verb
// its button says, *Re-key archive…* (commandCopy.rekeyArchive): one act,
// one name, in the foot and in its InfoBar (ruled 2026-10-17). The bare
// form stands when the archive's name is not known.
export const opCopy = {
  rekeying: "Re-keying",
  rekeyingArchive: "Re-keying archive",
  // *Refresh fingerprint* running: what it does, of which archive
  // (direction 9) — never "Verifying", which promised a check.
  refreshing: "Refreshing the fingerprint",
};

// ---- The foot as the status bar (direction 4, phase 3; APP.md §6) -------
//
// The layer's foot is where status lives: the running operation with its
// line, its bytes and the time left; the done line once it ends; the
// standing notices and the countdown on the right. Cancel and Stay
// unlocked are commands, so they are buttons; Show in Explorer and
// Details reveal something, so they are hyperlink buttons.
export const footCopy = {
  cancel: "Cancel",
  cancelling: "Cancelling…",
  // In the flyout, where a compaction's or a re-key's Cancel would stand:
  // neither is the user's to stop halfway (APP.md §2.3).
  runsToEnd: "Runs to the end",
  // The button after the latest operation when more than one runs.
  more: (n: number): string => `+${n}`,
  moreLabel: (n: number): string => `${plural(n, "more operation")} running`,
  flyoutLabel: "Running operations",
  bytesOf: (done: string, total: string): string => `${done} of ${total}`,
  minutesLeft: (m: number): string => `about ${m} min left`,
  underAMinute: "less than a minute left",
  details: "Details",
  stayUnlocked: "Stay unlocked",
  notices: (n: number): string => plural(n, "notice"),
  noticesLabel: "Notices",
};

// The done line (direction 4): one sentence for how an operation ended,
// said in the foot for 6 s on whichever page is up. Figures, the archive
// named, no full stop at the end, as a status bar says it; a stop is the
// user's own ending and says what is left, never "failed".
function wereWritten(n: number): string {
  return n === 1 ? "was" : "were";
}
export const doneCopy = {
  extracted: (n: number, dest: string): string => `Extracted ${plural(n, "file")} to ${dest}`,
  extractedOf: (n: number, of: number, dest: string): string => `Extracted ${n} of ${plural(of, "file")} to ${dest}`,
  extractStopped: (n: number, of: number, dest: string): string => `Stopped. ${n} of ${plural(of, "file")} ${wereWritten(n)} written to ${dest}`,
  extractStoppedNone: "Stopped. Nothing was written",
  added: (n: number, archive: string): string => `Added ${plural(n, "file")}${archive ? ` to ${archive}` : ""}`,
  addedOf: (n: number, of: number, archive: string): string => `Added ${n} of ${plural(of, "file")}${archive ? ` to ${archive}` : ""}`,
  addedFolders: (n: number, archive: string): string => `Added ${plural(n, "folder")}${archive ? ` to ${archive}` : ""}`,
  replaced: (n: number, archive: string): string => `Replaced ${plural(n, "file")}${archive ? ` in ${archive}` : ""}`,
  replacedOf: (n: number, of: number, archive: string): string => `Replaced ${n} of ${plural(of, "file")}${archive ? ` in ${archive}` : ""}`,
  compacted: (archive: string): string => `Compacted ${archive || "the archive"}`,
  compactedBack: (archive: string, back: string): string => `Compacted ${archive || "the archive"}: ${back} given back`,
  refreshed: (archive: string): string => `Fingerprint refreshed${archive ? ` for ${archive}` : ""}`,
  rekeyed: (archive: string): string => `Re-keyed ${archive || "the archive"}`,
  draggedOut: (n: number): string => `${plural(n, "item")} left Enfold as ordinary files`,
  backupSaved: (path: string): string => `Backup saved to ${path}`,
  checked: (probed: number, missing: number): string =>
    `Checked ${plural(probed, "archive")} · ${missing > 0 ? `${plural(missing, "file")} missing` : "all files present"}`,
  // A pass that measured no path (only local fixed drives are probed, APP.md
  // §13) says so, and claims nothing about presence.
  checkedNone: "No archive was checked",
  // A stop the user chose, by kind: what did not happen, in neutral ink.
  stoppedAdd: (archive: string): string => `Stopped. Nothing was added${archive ? ` to ${archive}` : ""}`,
  stoppedReplace: (archive: string): string => `Stopped. Nothing was replaced${archive ? ` in ${archive}` : ""}`,
  stoppedRefresh: (archive: string): string => `Stopped. The fingerprint${archive ? ` of ${archive}` : ""} was not refreshed`,
  stoppedReclaim: (archive: string): string => `Stopped. Reclaiming space${archive ? ` in ${archive}` : ""} did not finish`,
  stoppedOther: (archive: string): string => `Stopped. ${archive || "The archive"} is as it was`,
};

// One verb set for a wait the user started (direction 4, the pending
// primitive), used only where the label gains something by it; elsewhere
// the label stays and the ring alone says the wait.
export const pendingCopy = {
  opening: "Opening…",
  deleting: "Deleting…",
  merging: "Merging…",
  saving: "Saving…",
  checking: "Checking…",
};

// The results dialog (APP.md §3, FileOutcome; direction 4): its title is
// the outcome, counted; each row the item's name, then the code's own
// copy after a colon.
function wasOrWere(n: number): string {
  return n === 1 ? "was" : "were";
}
export const resultsCopy = {
  notDone: (n: number, unit: string, verb: string): string => `${plural(n, unit)} ${wasOrWere(n)} not ${verb}`,
  skipped: (n: number, unit: string): string => `${plural(n, unit)} ${wasOrWere(n)} skipped`,
  notDoneAndSkipped: (n: number, unit: string, verb: string, skipped: number): string =>
    `${plural(n, unit)} ${wasOrWere(n)} not ${verb} and ${skipped} ${wasOrWere(skipped)} skipped`,
  folder: "folder",
  skippedReason: "Something of that name is already here, so it was skipped.",
  failedReason: "It could not be done.",
  copyDetails: "Copy details",
  copied: "Details copied",
  copyFailed: "The details could not be copied.",
  more: (n: number): string => `and ${n} more.`,
};

// ---- The extract dialog and what an `ask` comes back with (APP.md §3,
// §6, ruled 2026-09-10) ----------------------------------------------
//
// The four policies in the words the dialog offers them: *Keep both* is
// the `rename` policy, the incoming file numbered, and *Ask me about each
// conflict* is the one that comes back with a question.
export const policyLabels: Record<string, string> = {
  replace: "Replace",
  skip: "Skip",
  rename: "Keep both",
  ask: "Ask me about each conflict",
};

export const extractCopy = {
  policyLegend: "If a file is already there",
  // One info line above the band, on every extract (direction 9, ruled
  // 2026-10-18): true of any destination, and a check of the drive's type
  // would miss a synced folder. A sentence, not a gate.
  ordinaryFiles: "The extracted copies are ordinary files, outside Enfold's protection.",
  // One Extract… with a scope (direction 7): the title names the archive
  // (ruled 2026-10-17), and the scope line asks what first — the
  // selection, counted as the pane counts it (files and folders alike), or
  // everything. With nothing selected the first radio is greyed and keeps
  // its reason (archivePageCopy.extractNeedsSome).
  title: (archive: string): string => `Extract from ${archive || "this archive"}`,
  scopeLegend: "What to extract",
  scopeSelection: (n: number): string => (n <= 0 ? "The selected items" : n === 1 ? "The selected item" : `The ${n} selected items`),
  scopeAll: (archive: string): string => `Everything in ${archive || "this archive"}`,
};

// conflictTitle is the question asked first, in the shape of Explorer's
// own "Replace or Skip Files" (direction 7): the title counts and names
// the place — the extract's destination — and one conflict names the file.
export function conflictTitle(n: number, name: string, place: string): string {
  const where = place || "the destination";
  if (n === 1) return `A file named ${name} is already in ${where}`;
  return `${n} files with the same names are already in ${where}`;
}

// The question's three command rows (CommandRows), each a strong line and
// its consequence, of equal weight and none the accent: Replace, Skip and
// the compare list. `others` is what the extract wrote besides the
// conflicts, so the skip's consequence never speaks of a rest that is not
// there.
export const conflictCopy = {
  replace: (n: number): string => (n === 1 ? "Replace the file in the destination" : "Replace the files in the destination"),
  replaceSays: (n: number): string => (n === 1 ? "The file already there is overwritten." : `The ${n} files already there are overwritten.`),
  skip: (n: number): string => (n === 1 ? "Skip this file" : "Skip these files"),
  skipSays: (n: number, others: number): string => {
    const stay = n === 1 ? "The file stays as it is in the destination" : `The ${n} files stay as they are in the destination`;
    return others > 0 ? `${stay}; the rest were extracted.` : `${stay}.`;
  },
  decide: (n: number): string => (n === 1 ? "Compare both files" : "Let me decide for each file"),
  decideSays: (n: number): string => (n === 1 ? "See their dates and sizes side by side." : "Compare the two copies side by side."),
};

// The add collisions (APP.md §3, CheckNames), in the conflict question's
// shape (direction 7): the title counts what is already there by its kind
// and names the folder, the names follow (the first 6 and a count), then
// Replace · Keep both · Skip as command rows and Cancel in the band. A
// collision carries the two kinds only, so no size or date is said.
// A folder that meets a folder is not a clash: the add enters it and
// merges the two whatever the policy, and the policy reaches only the
// files inside (internal/app/ops.go planNodes). So the rows count and
// speak of the clashes alone — files that meet files, and a file that
// meets a folder — and a merge is said as one, with what the choice does
// inside it (lib/conflicts.ts collisionPlan puts these together).
export interface KindCount {
  files: number;
  folders: number;
}

function kindNoun(c: KindCount, n: number): string {
  if (c.folders === 0) return n === 1 ? "file" : "files";
  if (c.files === 0) return n === 1 ? "folder" : "folders";
  return n === 1 ? "item" : "items";
}

export const collisionCopy = {
  title: (c: KindCount, name: string, place: string): string => {
    const n = c.files + c.folders;
    const where = place || "this folder";
    if (n === 1) return `A ${kindNoun(c, 1)} named ${name} is already in ${where}`;
    return `${n} ${kindNoun(c, n)} with the same names are already in ${where}`;
  },
  // Each name's kind: what is here, what is being added when the two
  // differ, and a merge when two folders meet.
  kind: (isDir: boolean, existingIsDir: boolean): string => {
    if (isDir === existingIsDir) return existingIsDir ? "Folder; the two are merged" : "File";
    return existingIsDir ? "A folder here; a file is being added" : "A file here; a folder is being added";
  },
  andMore: (n: number): string => `and ${n} more`,
  // What a merge does, said once under the names: alone, the choice is
  // only for the files inside; beside clashes, it reaches inside too.
  mergeNote: (merges: number, clashes: number): string => {
    if (clashes === 0) {
      return merges === 1
        ? "The folder is merged with the one already here. The choice below is for any file inside it with the same name as one already there."
        : `The ${merges} folders are merged with the ones already here. The choice below is for any file inside them with the same name as one already there.`;
    }
    return merges === 1
      ? "The folder of the same name is merged with the one already here, and the choice below also applies to the files inside it."
      : `The ${merges} folders of the same names are merged with the ones already here, and the choice below also applies to the files inside them.`;
  },
  replace: (c: KindCount, place: string): string => {
    const n = c.files + c.folders;
    const where = place || "this folder";
    return n === 1 ? `Replace the ${kindNoun(c, 1)} in ${where}` : `Replace the ${n} ${kindNoun(c, n)} in ${where}`;
  },
  replaceSays: (n: number): string => (n === 1 ? "The copy already in the archive is overwritten." : "The copies already in the archive are overwritten."),
  // Replace's reason when a file and a folder meet (APP.md §3): kinds that
  // differ never replace one another, in either direction.
  replaceOff: "A file and a folder never replace one another.",
  keepBoth: "Keep both",
  keepBothSays: (n: number): string => (n === 1 ? "The new one gets a number in its name." : "The new ones get a number in their names."),
  skip: (c: KindCount): string => {
    const n = c.files + c.folders;
    return n === 1 ? `Skip this ${kindNoun(c, 1)}` : `Skip these ${kindNoun(c, n)}`;
  },
  skipSays: (c: KindCount, others: number): string => {
    const n = c.files + c.folders;
    const stay = n === 1 ? `The ${kindNoun(c, 1)} already here stays` : `The ${n} ${kindNoun(c, n)} already here stay`;
    return others > 0 ? `${stay}; the rest are added.` : `${stay}; nothing is added.`;
  },
  // The rows when every name is a merge: the choice is for the files
  // inside that meet files already there, which CheckNames cannot see.
  replaceInside: "Replace files with the same names",
  replaceInsideSays: "Those copies already in the archive are overwritten.",
  keepBothInsideSays: "The new ones get a number in their names.",
  skipInside: "Skip files with the same names",
  skipInsideSays: "Those copies already in the archive stay; the other files are added.",
};

// The compare list after Windows Explorer's: the two columns, the rule
// of the ticks said once over them, and its two buttons — *Continue*
// reading what it will do (compareGoText).
export const compareCopy = {
  title: "Which files do you want to keep?",
  fromArchive: "Files from the archive",
  inDestination: "Files already in the destination",
  keepBoth: "Tick both to keep both copies",
  cancel: "Cancel",
};

// skipSameText is the foot's tick, singular at one and empty at none —
// there is nothing to tick when no two copies match.
export function skipSameText(n: number): string {
  return n === 0 ? "" : `Skip ${plural(n, "file")} with the same date and size`;
}

// compareGoText is *Continue* as it reads (direction 7): what it will do
// with the ticks as they stand — "Extract 2, skip 1", "Extract 3", "Skip
// 3". A copy kept beside the one there is extracted too, under a number.
export function compareGoText(c: { replace: number; both: number; skip: number }): string {
  const extract = c.replace + c.both;
  if (extract === 0) return `Skip ${c.skip}`;
  if (c.skip === 0) return `Extract ${extract}`;
  return `Extract ${extract}, skip ${c.skip}`;
}

// retriesText is the count shown in the PIN's label row. The card is asked
// before the prompt, so the count is always known here; when the card is
// verified it cannot say, and the copy says that instead of a number. It
// never says "0". The last try is said by its ink and, on the lock screen,
// by the bar above the field (lockCopy.lastTry; ruled 2026-10-17: it read
// "1 attempt left — the last one" in the ink of the first).
export function retriesText(s: Pick<CeremonyState, "retries" | "retriesKnown" | "verified">): string {
  if (!s.retriesKnown || s.verified) return "Tries left unknown";
  if (s.retries <= 0) return "";
  return s.retries === 1 ? "1 try left" : `${s.retries} tries left`;
}

// tamperedCopy is the Tampered banner (APP.md §13): what happened and what
// to do, the same words whatever VaultStatus.TamperedReason says, since
// the remedy is one; the page carries *Replace with a copy…*. Which check
// failed — and the rule that, with no reason to hand, both causes are
// named, because guessing one would be a claim — moved to
// technicalCopy.tamperedWhy, the banner's "Why?" (ruled 2026-10-17).
// The last sentence answers the fear the first one raises (direction 9,
// ruled 2026-10-18): the freeze is the vault file's, and the archive
// files keep saving (APP.md §2.3).
export function tamperedCopy(reason?: string): string {
  return leadText(tamperedLead(reason));
}

// The same words split for the banner: what happened, strong, then the
// freeze and its limit.
export function tamperedLead(_reason?: string): Lead {
  return {
    head: "This vault file does not match itself, so it may have been changed outside Enfold.",
    rest: "Changes to the vault are off until you replace it with a copy you trust. Your archive files are not affected.",
  };
}

// warningCopy is what a status warning says in the banner.
export function warningCopy(code: string, reason?: string): string {
  switch (code) {
    case Code.CodeVaultTampered:
      return tamperedCopy(reason);
    case Code.CodeVaultStale:
      return codeCopy[Code.CodeVaultStale];
    case Code.CodeTokenReset:
      // Said only just after the failed release: the status drops it at the
      // next open of a card, which resets it, or at the next lock (APP.md
      // §2.2). How long it stays verified is not known, so no time is named.
      return "Your YubiKey could not be reset after release and may still be PIN-verified; remove it and plug it back in to be sure.";
  }
  return codeText(code);
}

// ---- The file list (APP.md §6, ruled 2026-09-10) -----------------------
//
// Four columns — Name, Size, Type, Modified — the `..` row that goes up
// one level, and what the header's checkbox and the list's empty states
// say. The column labels are one table so the Name cell can carry a
// hidden column's key beside its arrow.
export const columnLabels = {
  name: "Name",
  size: "Size",
  type: "Type",
  modified: "Modified",
};

export const listCopy = {
  up: "..",
  upLabel: "Up one level",
  tickAll: "Select everything in this folder",
  tickRow: (name: string): string => `Select ${name}`,
  // the header cell's tooltip says what the first click will do
  sortBy: (column: string): string => `Sort by ${column}`,
};

// The boot (APP.md §2.4): a first Status() that failed is a panel of its
// own (direction 9, ruled 2026-10-18) — the brand, this heading, the
// code's own words, and three ways on: Retry, the data folder that holds
// the log, and the details copied for a report. The lock scene is never
// drawn on no evidence, and nothing of the vault is.
export const bootCopy = {
  title: "Enfold could not start",
  retry: "Retry",
  openFolder: "Open the data folder",
  copyDetails: "Copy details",
  copied: "Details copied",
  copyFailed: "The details could not be copied.",
  // What *Copy details* copies: the heading, the code, and when, in ISO
  // form so a report reads the same wherever it is pasted.
  details: (code: string, iso: string): string => `Enfold could not start · ${code} · ${iso}`,
};

// The close question (APP.md §2.3, ruled 2026-09-13): the close button
// never means "to the tray" until the user has said so, so the first close
// is cancelled and this is asked — one sentence, the two answers, and the
// tick that makes the answer the setting. Esc is neither: the window
// stays. The quiet line is there because the tick is a setting and the
// user should know where to undo it.
export const closeCopy = {
  title: "Close Enfold?",
  // The two answers in the order the buttons stand (ruled 2026-10-17).
  body: "Quit, or keep it running in the tray, ready to open?",
  tray: "Keep running in the tray",
  quit: "Quit",
  remember: "Remember my choice",
  inSettings: "You can change this in Settings.",
};

// Opening an archive from Explorer (APP.md §14, decision 4): a
// double-click, an *Open with*, a path on the command line. Two of the
// three answers are said in place — the archive opens, or a file that is
// not one is an InfoBar — and the third is this dialog: the file *is* an
// Enfold archive and no record here holds its keys, so the one way on is
// *Import archives from a backup…*, and the file was not touched.
export const openCopy = {
  title: (name: string) => `Open ${name}`,
  // The code's own line, said once: the dialog is where it is read.
  keyNotInVault: codeCopy[Code.CodeKeyNotInVault],
  how: "Import it from a backup or another copy of this vault that has it. Nothing in the file was changed.",
  importRecords: "Import archives from a backup…",
  close: "Close",
};

// archive.open_elsewhere names the file the archive is already open from
// (APP.md §14): two copies of one archive, and the handle is on the other
// one. The whole path, not the leaf: the point is which of two files that
// look alike is the one being read.
export function openElsewhereText(path: string): string {
  return `This archive is already open from ${path}. Close it there first.`;
}

// purgedText is the InfoBar the purge raises at the end of an unlock (APP.md
// §13, ruled 2026-10-17): the consequence first, and in the user's words —
// it said "The key of … was dropped", the format's act on a main surface.
export function purgedText(names: string[]): string {
  return names.length === 1
    ? `"${names[0]}" was forgotten more than 30 days ago, so it can no longer be opened.`
    : `${names.length} forgotten archives can no longer be opened: ${names.join(", ")}. Each was forgotten more than 30 days ago.`;
}

// What a launch that named more than one file says about the rest: the
// first is opened and the others are named, the way the purge names the
// keys it dropped (APP.md §14, "the rest are named in a toast"). The
// names are leaves: the folder they came from is the one the user was
// standing in, and says nothing.
export function moreNotOpened(names: string[]): string {
  return `${plural(names.length, "more archive")} ${names.length === 1 ? "was" : "were"} not opened: ${names.join(", ")}. Enfold opens one at a time.`;
}

// The InfoBars (direction 4, phase 4b; InfoBars.svelte; APP.md §6,
// Notices): the region's name for a screen reader, the bar's close, whose
// title names Esc on the newest error, which the key closes, and the bars
// the page raises in words of its own.
export const noticeCopy = {
  label: "Notifications",
  close: "Close",
  closeEsc: "Close (Esc)",
  // A drop from outside the window that the page cannot place (APP.md
  // §3): on a page with no archive's list, at an archive other than the
  // one shown, or beside the list. It is refused with where to drop,
  // never retargeted at a folder the user did not aim at.
  dropNoArchive: "Drop files onto an open archive's file list.",
  dropOtherArchive: "Drop files onto the archive they belong in.",
  dropOffList: "Drop files onto the file list.",
  // Ctrl+V on the file list with no files on the clipboard (ruled
  // 2026-10-26): what a paste takes, and where it comes from.
  nothingToPaste: "Nothing to paste. Copy files in Explorer first.",
  // The folder the page stood in went — a Discard dropped it, a Delete
  // took an ancestor — and the page walked its crumbs upwards (APP.md §6):
  // its name as the page last had it, "That folder" when it had none, and
  // where the page stands now.
  folderWent: (name: string, now: string): string => `${name ? `"${name}"` : "That folder"} is no longer in the archive.${now ? ` Showing ${now}.` : ""}`,
};

// The Archives list (APP.md §6, §13; direction 6, phase 4a): one archive
// selected at a time — no checkboxes, every action here takes one — the
// details pane following it, and the vault's summary when none is.
export const archivesCopy = {
  // The page is the list of archives (direction 10): the vault's name is
  // the rail head's.
  title: "Archives",
  listLabel: "Archives",
  selectFirst: "Select an archive first.",
  selectedName: (name: string): string => `${name} selected`,
  enterOpens: (name: string): string => `Enter opens ${name}`,
  // The bottom panel the details become in a narrow body (APP.md §6,
  // refined 2026-09-12): the same eyebrow the side pane carries, and a
  // header that is the control — so its name is what a click will do,
  // and which archive it will do it for.
  panelLabel: "Selected archive",
  panelEyebrow: "Selected",
  panelShow: (name: string): string => `${name} — show the details`,
  panelHide: (name: string): string => `${name} — hide the details`,
  // Open file… (Ctrl+O) and the empty page's buttons (APP.md §13, §14,
  // ruled 2026-10-14): an .efd from anywhere, opened the way Explorer's
  // double-click opens it.
  openFile: "Open file…",
  openFileTitle: "Open an archive",
  emptyVault: "No archives in this vault yet",
  // The list's other empty states.
  noMatch: "No archive matches that.",
  allHidden: "No archives are shown. Hidden ones are listed under View › Show hidden, forgotten ones under View › Forgotten.",
  noneForgotten: "No archive is forgotten.",
  filter: "Filter archives",
  // The bar's two menus (direction 6): More, the selected archive's verbs
  // the bar has no room for and the ones that hide or destroy it; View,
  // what the list shows. Forgotten is a view of its own, like the Recycle
  // Bin, with its count, and a chip beside the title leads into it while
  // any record is forgotten.
  more: "More",
  view: "View",
  showHidden: "Show hidden",
  forgottenView: (n: number): string => `Forgotten (${n})`,
  forgottenPlain: "Forgotten",
  forgottenChip: (n: number): string => `Forgotten · ${n}`,
  forgottenChipOpen: "Show the forgotten archives",
  forgottenChipClose: "Show every archive again",
  forgottenNote: "A forgotten archive keeps its key for 30 days. After that its file cannot be opened.",
  // A forgotten row's second line: the day its key goes (FORMAT.md §18.2,
  // the thirty days direction 8's dialog names; lib/retention.ts).
  keptUntil: (when: string): string => `Key kept until ${when}`,
  // The pane with nothing selected (direction 6): the vault's summary,
  // its last backup and what the list marks missing.
  // Hidden ones are counted apart while the list does not show them, so
  // the figure squares with the title's "N archives shown".
  summary: (n: number, size: string, hidden = 0): string => `${plural(n, "archive")}${hidden > 0 ? `, ${hidden} hidden` : ""} · ${size}`,
  // While locked the core lists only what is open, so the head says
  // that and no figures (a count of the open ones is not the vault's).
  summaryLocked: (open: number): string => (open > 0 ? `Locked · ${plural(open, "archive")} open` : "Locked"),
  vaultFallback: "Vault",
  noneOpen: "No archives are open.",
  lastBackup: (when: string): string => `Last backup ${when}`,
  // The summary's way to a new backup: the Vault page's *Export backup…*,
  // started there (store.askExport); the line before it says what.
  exportLink: "Export…",
  missingRow: (name: string): string => `${name} — file missing`,
  // The name and the description, edited behind the pane's pencil
  // (direction 6) under the save bar, the one staged edit it is for.
  // Why a menu item is greyed, said under it (direction 6, lib/menu.ts
  // secondLine): the reason in a few words, since a menu says it of
  // every item it stops — the codes' own sentences stay the buttons'
  // titles. A phrase without a full stop, in the house form of every
  // greyed item's second line ("Select one item to rename", "This archive
  // is closing"; the final shoot of phase 4b found these alone ending in
  // one).
  why: {
    locked: "Unlock the vault first",
    tampered: "Changes to this vault are off",
    forgotten: "Restore it first",
    alreadyForgotten: "Already forgotten",
    compacting: "Wait until its compaction ends",
    // *Show in Explorer* on a record whose last path is another system's
    // (lib/paths.ts foreignPath): this machine has no such place to show.
    foreign: "Last seen on another system",
  },
  nameAndDescription: "Name and description",
  editNameDescription: "Edit name and description",
  noDescription: "No description",
  descriptionHint: "Shown under the name in the list",
  // The details pane's facts (ruled 2026-10-17): user words only. The key
  // version and the KID went to the Details dialog, where the KID was
  // already shown once; "free" reads as the disk's, so the space inside
  // the file is *Unused space*.
  facts: {
    size: "Size",
    files: "Files",
    created: "Created",
    lastSaved: "Last saved",
    compression: "Compression",
    unused: "Unused space",
    file: "File",
    name: "Name",
    description: "Description",
    foreign: "Last seen on another system at",
    fingerprint: "Fingerprint",
  },
  // The fingerprint's fact (direction 9, ruled 2026-10-18): how far the
  // hash the registry keeps is behind the archive's saves, said as what
  // it is — which copy of the file, not a check of what is in it. Most
  // records never had one taken (only Refresh fingerprint and Compact
  // take one), and that is said rather than counted from seq 0; while a
  // receipt is owed the file has saves the registry has not counted, so
  // no number is claimed (lib/archlist.ts fingerprintOf).
  fingerprint: (f: { kind: FingerprintKind; behind: number }): string =>
    f.kind === "none"
      ? "Not taken yet"
      : f.kind === "owed"
        ? "From before the last save"
        : f.kind === "behind" && f.behind > 0
          ? `From ${plural(f.behind, "save")} ago`
          : "Matches the last save",
  // The archive row's second-register dialog (APP.md §13): *Details…*
  // since 2026-10-26, the user's ruling; it was *Technical details…*.
  details: "Details…",
  // The Missing chip is a button that runs Locate… for its row (direction
  // 7): the chip names the trouble, the button is its cure.
  missingChip: (why: string): string => `${why} ${commandCopy.locate} asks where it is now.`,
  // The locked banner (direction 9): the archives the lock left open, by
  // name from VaultStatus.opened, and that they still save.
  lockedBanner: (names: readonly string[]): string => lockedOpenText(names),
  lockedBannerLead: (names: readonly string[]): Lead => lockedOpenLead(names),
  // The pane's line for a forgotten record (APP.md §13): when it was
  // forgotten, the undo, and the date its key goes.
  forgottenLine: (on: string, purge: string): string => `${chipCopy.forgottenOn(on)}. Restore it to open it again; after ${purge} its key is gone.`,
};

// commandCopy is the verb table (the glossary above): a command's label
// wherever it is offered — the command bar, a context menu, a card, a
// banner — so one operation never goes by two names. "…" is there
// exactly when a dialog or a picker follows.
export const commandCopy = {
  open: "Open",
  close: "Close",
  // One meaning for *Close*, app-wide (APP.md §2.3, ruled 2026-10-19): the
  // graceful leave, Archives.Leave, wherever it stands — the Archives
  // page's locked line says *Close archive* / *Close all* for it. *Force
  // close* (and *Force close all*) is the kill switch, Archives.Close or
  // CloseAll, and stands only for an archive a leave left that something
  // still holds (lib/leave.ts decides which).
  closeArchive: "Close archive",
  closeAll: "Close all",
  forceClose: "Force close",
  forceCloseAll: "Force close all",
  newArchive: "New archive…",
  hide: "Hide",
  unhide: "Unhide",
  forgetArchive: "Forget archive…",
  restore: "Restore",
  deleteArchive: "Delete archive…",
  compact: "Compact…",
  rekeyArchive: "Re-key archive…",
  rekeyVault: "Re-key vault…",
  // Named for what it does (direction 9): it hashes the file's raw bytes
  // and records the hash; it authenticates nothing, so it is not "Verify".
  refreshFingerprint: "Refresh fingerprint",
  locate: "Locate…",
  showInExplorer: "Show in Explorer",
  findMissing: "Find missing files",
  addUnlockMethod: "Add an unlock method…",
  addRecoveryKey: "Add a recovery key…",
  removeUnlockMethod: "Remove…",
  showRecoveryKey: "Show recovery key…",
  // Every recovery key at once, so it stands in the head of Unlock
  // methods and not on one card; plural whatever the count (ruled
  // 2026-10-27).
  replaceRecoveryKeys: "Replace recovery keys…",
  exportBackup: "Export backup…",
  inspectBackup: "Inspect a backup…",
  testBackup: "Test that a backup opens…",
  importArchives: "Import archives from a backup…",
  replaceVault: "Replace this vault with a copy…",
  replaceShort: "Replace with a copy…",
  restoreFrom: "Restore from a vault or backup…",
  createVault: "Create a vault…",
  rebuildVault: "Rebuild the vault…",
  reopen: "Reopen",
  // One Extract… (direction 7): the toolbar's, the pane's and Ctrl+E's,
  // whose dialog asks the scope. There is no *Extract all…*.
  extract: "Extract…",
  lockNow: "Lock now",
  unlock: "Unlock",
  // The Archive page's own commands on its rows (APP.md §6).
  rename: "Rename…",
  delete: "Delete…",
  createFolder: "Create folder…",
  // The Add menu's two pickers (direction 3): each opens a picker, so the
  // ellipsis; *Create folder…* stands apart under the separator.
  addFiles: "Add files…",
  addFolder: "Add a folder…",
  // The row menus (direction 7): the selection into a folder chosen in a
  // tree, and a row's name to the clipboard.
  moveTo: "Move to…",
  copyName: "Copy name",
  // The blank area's first item and Ctrl+V (ruled 2026-10-26): the files
  // copied in Explorer added to the folder shown, as a drop adds them. No
  // ellipsis: nothing is asked first, as with a drop — the collisions
  // dialog stands only when a name is taken.
  paste: "Paste",
};

// keyNames: the key that runs a command, as a menu shows it at an item's
// right (direction 7, Explorer's habits) — the words keyTitles puts in
// parentheses, so a menu and a title never name one key two ways.
export const keyNames = {
  open: "Enter",
  extract: "Ctrl+E",
  delete: "Del",
  rename: "F2",
  moveTo: "Ctrl+Shift+M",
  createFolder: "Ctrl+Shift+N",
  newArchive: "Ctrl+N",
  openFile: "Ctrl+O",
  details: "Alt+Enter",
  filter: "Ctrl+F",
  lockNow: "Ctrl+L",
  previewPane: "Alt+P",
  cut: "Ctrl+X",
  copy: "Ctrl+C",
  paste: "Ctrl+V",
  selectAll: "Ctrl+A",
};

// fieldCopy: a text field's menu (direction 7), the app's own since the
// shell turned WebView2's off (2026-09-06). Paste reads the clipboard
// through the WebView; where the WebView refuses the read, the item is
// greyed with the key that still pastes. A secret field has no menu at all.
export const fieldCopy = {
  menuLabel: "Edit",
  cut: "Cut",
  copy: "Copy",
  paste: "Paste",
  selectAll: "Select all",
  pasteRefused: "Use Ctrl+V",
};

// keyHint is a command's title where a key runs it (direction 7; APP.md
// §6): the label as the verb table has it, then the key — "Extract…
// (Ctrl+E)". A greyed button keeps its reason as its title instead.
export function keyHint(label: string, key: string): string {
  return `${label} (${key})`;
}

// keyTitles: every command whose key exists, titled once here so the
// glossary walk reads each title as the button shows it.
export const keyTitles = {
  extract: keyHint(commandCopy.extract, keyNames.extract),
  delete: keyHint(commandCopy.delete, keyNames.delete),
  rename: keyHint(commandCopy.rename, keyNames.rename),
  moveTo: keyHint(commandCopy.moveTo, keyNames.moveTo),
  createFolder: keyHint(commandCopy.createFolder, keyNames.createFolder),
  newArchive: keyHint(commandCopy.newArchive, keyNames.newArchive),
  openFile: keyHint(archivesCopy.openFile, keyNames.openFile),
  deleteArchive: keyHint(commandCopy.deleteArchive, keyNames.delete),
  details: keyHint(archivesCopy.details, keyNames.details),
  filter: keyHint(archivesCopy.filter, keyNames.filter),
  lockNow: keyHint(commandCopy.lockNow, keyNames.lockNow),
};

// chipCopy is every chip's word, in sentence case (ruled 2026-10-17: they
// were lower-case), on the Archives list and the import list alike.
export const chipCopy = {
  locked: "Locked",
  open: "Open",
  compacting: "Compacting",
  // A Refresh fingerprint or a re-key running on the row (direction 4): the
  // core queues a second operation behind the handle rather than refusing
  // it, so the chip says what runs and nothing is greyed for it.
  refreshing: "Refreshing fingerprint",
  rekeying: "Re-keying",
  missing: "Missing",
  hidden: "Hidden",
  forgotten: "Forgotten",
  alreadyHere: "Already here",
  update: "Update",
  new: "New",
  forgottenOn: (when: string): string => `Forgotten on ${when}`,
};

// The Type column (APP.md §6): a folder is *Folder*, a file's type is
// drawn from its extension — the part after the last dot; none when the
// dot is the first character or there is none — by this table, "<EXT>
// file" for an extension without a name of its own, and *File* for none.
export const folderType = "Folder";
export const plainFileType = "File";

export const extensionNames: Record<string, string> = {
  jpg: "JPEG image",
  jpeg: "JPEG image",
  png: "PNG image",
  gif: "GIF image",
  webp: "WebP image",
  bmp: "Bitmap image",
  svg: "SVG image",
  avif: "AVIF image",
  ico: "Icon",
  heic: "HEIC image",
  heif: "HEIF image",
  tif: "TIFF image",
  tiff: "TIFF image",
  raw: "Raw image",
  dng: "DNG image",
  cr2: "Canon raw image",
  nef: "Nikon raw image",
  arw: "Sony raw image",
  psd: "Photoshop document",
  mp4: "MP4 video",
  m4v: "MP4 video",
  mov: "QuickTime video",
  webm: "WebM video",
  mkv: "Matroska video",
  avi: "AVI video",
  wmv: "Windows Media video",
  ogv: "Ogg video",
  mp3: "MP3 audio",
  wav: "WAV audio",
  flac: "FLAC audio",
  aac: "AAC audio",
  m4a: "MPEG-4 audio",
  ogg: "Ogg audio",
  opus: "Opus audio",
  wma: "Windows Media audio",
  aiff: "AIFF audio",
  txt: "Text document",
  md: "Markdown document",
  rtf: "Rich text document",
  log: "Log file",
  csv: "CSV table",
  tsv: "TSV table",
  json: "JSON data",
  xml: "XML document",
  yaml: "YAML document",
  yml: "YAML document",
  toml: "TOML document",
  ini: "Settings file",
  pdf: "PDF document",
  doc: "Word document",
  docx: "Word document",
  odt: "OpenDocument text",
  xls: "Excel workbook",
  xlsx: "Excel workbook",
  ods: "OpenDocument spreadsheet",
  ppt: "PowerPoint presentation",
  pptx: "PowerPoint presentation",
  odp: "OpenDocument presentation",
  epub: "EPUB book",
  mobi: "Kindle book",
  zip: "ZIP archive",
  "7z": "7-Zip archive",
  rar: "RAR archive",
  tar: "Tar archive",
  gz: "Gzip archive",
  bz2: "Bzip2 archive",
  xz: "XZ archive",
  zst: "Zstandard archive",
  iso: "Disc image",
  efd: "Enfold archive",
  eks: "Enfold vault",
  exe: "Application",
  msi: "Windows installer",
  dll: "Library",
  bat: "Batch script",
  cmd: "Batch script",
  ps1: "PowerShell script",
  sh: "Shell script",
  py: "Python source",
  js: "JavaScript source",
  ts: "TypeScript source",
  go: "Go source",
  rs: "Rust source",
  c: "C source",
  h: "C header",
  cpp: "C++ source",
  cs: "C# source",
  java: "Java source",
  html: "HTML page",
  htm: "HTML page",
  css: "Style sheet",
  sql: "SQL script",
  ttf: "TrueType font",
  otf: "OpenType font",
  woff: "Web font",
  woff2: "Web font",
};

export function typeLabel(name: string, isDir: boolean): string {
  if (isDir) return folderType;
  const e = ext(name);
  if (e === "") return plainFileType;
  return extensionNames[e] ?? `${e.toUpperCase()} file`;
}

// ---- A name the destination refuses (APP.md §3, ruled 2026-09-10) -----
//
// Asked per refused record: the final name refused on placement shows the
// name it would write and offers *Use this name*, *Rename…* and *Skip*,
// with "Do the same for the other N" when more names of that kind wait
// (direction 7); a refused path — the temporary or a folder, where no name
// helps — offers *Skip* and *Skip all like it*. Esc is *Skip*. The count
// is where the record stands in the list of refused ones, so a long list
// says how much is left.
export const refusedCopy = {
  nameTitle: "The destination cannot take a name this long",
  pathTitle: "The folder's path is too long for this destination",
  notWritten: (name: string): string => `${name} was not written.`,
  // The question, around the shorter name set strong: "Write it as …?"
  writeAs: "Write it as",
  howTitle: "How names are shortened",
  how: "Use this name halves the name and keeps its extension; a name refused again is asked about again. Rename… writes it under a name you type, for this extract only. The archive keeps the name as it is.",
  pathBody: (name: string, isDir: boolean): string =>
    isDir
      ? `The folder ${name} and everything in it were not written: the destination cannot take a path this long, and no name would help. Extract it to a shorter path instead.`
      : `${name} was not written: the folder it goes in has a path the destination cannot take, and no name would help. Extract it to a shorter path instead.`,
  progress: (at: number, of: number): string => (of > 1 ? `${at} of ${of}` : ""),
  useName: "Use this name",
  nothingShorter: "The name is down to 1 character and cannot be shortened further.",
  rename: "Rename…",
  renameLabel: "Name for this extract",
  renameBack: "Back",
  renameGo: "Extract as this",
  skip: "Skip",
  skipAllLike: "Skip all like it",
  // Use this name or Skip, given to every refused name still waiting;
  // Rename… is one record's alone.
  sameForRest: (n: number): string => `Do the same for the other ${n}`,
};

// ---- The consequence dialogs (the dialog template above, ruled
// 2026-10-17) ------------------------------------------------------------
//
// Each one: a question that names the object, the consequence for the
// user's files and then the safety net, the primary's verb and object,
// and `how` — the mechanism — for a "How this works" disclosure. Screen
// 76's "Permanently delete 2 files from Photos 2024? This cannot be
// undone." is the reference. The primary stands first, in the foot band's
// order (APP.md §6, Controls).
export const dialogCopy = {
  cancel: "Cancel",
  // The title of a dialog's corner close button, which does what Esc does
  // (APP.md §6, Controls; ruled 2026-10-17); its name is plain *Cancel*.
  cancelEsc: "Cancel (Esc)",
  skipEsc: "Skip (Esc)",
  close: "Close",
  howThisWorks: "How this works",
  deleteArchive: {
    title: (name: string): string => `Delete ${name}?`,
    body: (path: string): string => `Deletes ${path} for good, after checking that the file really is this archive. Its key is kept for 30 days in case a copy of the file turns up.`,
    primary: "Delete archive",
    // What happened, then the undo and its date: a forgotten record shows
    // in View › Forgotten alone, never under Show hidden (APP.md §13,
    // ruled 2026-10-19). `until` is the purge's date.
    forgotDone: (name: string, until: string): string => `The file stays where it is. You can restore ${name} from View › Forgotten until ${until}; after that its key is gone.`,
    deletedDone: (until: string): string => `The file is gone. Its key is kept until ${until}, so you can restore the archive from View › Forgotten if a copy of the file turns up.`,
  },
  // `until` is the purge's date (APP.md §13): the first unlock after it
  // drops the key.
  forgetArchive: {
    title: (name: string): string => `Forget ${name}?`,
    body: (until: string): string => `The file stays where it is. You can restore it from View › Forgotten until ${until}; after that its key is gone and the file cannot be opened.`,
    primary: "Forget archive",
  },
  // The typed name is the brake of both (APP.md §13).
  confirmName: "Type the archive's name to confirm",
  // What is known here about a backup that holds the key (APP.md §13).
  // One sentence, the date in apposition (ruled 2026-10-17).
  lastBackup: (when: string): string => `The last backup of this vault, ${when}, can open this archive if you still have it.`,
  noBackup: "No backup of this vault is recorded here, so once its key is gone no copy of this archive can be opened.",
  // The Delete that did not delete (APP.md §13): each says nothing was
  // lost first, then what is left to do.
  notThisArchive: {
    title: "Nothing was deleted",
    body: "That file is not this archive, so nothing was deleted and nothing was forgotten.",
    next: "If the archive moved, locate it. Or forget the archive and leave that file alone.",
    forgetOnly: "Forget archive only…",
  },
  forgetOnly: {
    title: (name: string): string => `Forget ${name} only?`,
    body: "The file at that path stays exactly as it is. You can restore the archive from View › Forgotten for 30 days.",
    primary: "Forget archive",
  },
  unreachable: "That folder could not be reached, so nothing was deleted and nothing was forgotten.",
  unreadable: "That file could not be read, so nothing was deleted and nothing was forgotten. Forgetting an archive whose file may still be whole would lose its key.",
  deleteFailed: "That file is this archive, but it could not be deleted. Nothing was forgotten: the archive is forgotten only once its file is gone.",
  forgotten: "The archive is forgotten",
  compact: {
    title: (name: string): string => `Compact ${name}?`,
    body: "Rewrites the file to give back its unused space; the files inside do not change, and a big archive takes a while. It runs to the end once started.",
    primary: "Compact archive",
  },
  // Registry first, then the file (FORMAT.md R33).
  rekeyArchive: {
    title: (name: string): string => `Re-key ${name}?`,
    body: "From now on the archive opens with a new key; its old keys stay in the vault, so copies made before today still open. It runs to the end once started.",
    how: "The new key is added to the vault first, then the archive's file is changed to use it.",
    primary: "Re-key archive",
  },
  // FORMAT.md §18.3, DESIGN trap 29: the backup is asked for, never
  // required, and no copy of the vault is kept beside it.
  // The body says what authorises the act: one unlock method proves it is
  // you, a YubiKey wherever the vault has one (keys.go acquireUnlocked).
  // The backup is the shared block's (backupAfter, primaries), taken after
  // the re-key (ruled 2026-10-27).
  rekeyVault: {
    title: "Re-key the vault?",
    body: (yubiKey: boolean): string =>
      yubiKey
        ? "Every unlock method gets the new key now; one of them proves it is you first, and the other YubiKeys need not be present. Backups made before today keep opening with the methods they had."
        : "Every unlock method gets the new key now; one of them proves it is you first. Backups made before today keep opening with the methods they had.",
    lastBackup: "Last backup",
    noBackup: "None recorded here",
    backupWhy: "A backup is what brings your archives back if this vault is lost.",
    primary: "Re-key vault",
  },
  // The question at a removal, a password change and turning the password
  // on (DESIGN §5; ruled 2026-10-27): the fact the user can answer, never
  // the technical choice. The answer that re-keys stands first and is
  // pre-selected; each has a second line that says what it does.
  rekeyQuestion: {
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
  },
  // The limit of a re-key, said plainly (DESIGN §5), what the recovery
  // keys' copies in the vault leave open (FORMAT R38), and the act that
  // closes it, named and not linked (ruled 2026-10-27). The second
  // sentence of each is drawn only while the vault has a recovery key
  // besides any being removed, and none ends on the command's "…".
  rekeyLimit: {
    future: "Re-keying protects the future, not the past: whatever someone could open until now, they may already have copied.",
    method: (noun: string): string =>
      `If an earlier copy of the vault file may be out there, whoever opened it with this ${noun} could also read the recovery keys, which still open this vault: replace them afterwards with Replace recovery keys… on this page.`,
    methodOn:
      "If an earlier copy of the vault file may be out there, whoever opened it with this YubiKey could also read the recovery keys and the vault's own record of the password: change the password afterwards, then replace the recovery keys with Replace recovery keys… on this page.",
    methodOnNoRecovery:
      "If an earlier copy of the vault file may be out there, whoever opened it with this YubiKey could also read the vault's own record of the password: change the password afterwards.",
    recovery:
      "Whoever has this recovery key can open an earlier copy or backup of the vault and read the other recovery keys there: if one may be out there, use Replace recovery keys… on this page instead, which removes this one too. Check the unlock methods as well, and remove any you did not add.",
    recoveryOn: " Then change the password.",
    change:
      "If an earlier copy of the vault file may be out there, whoever opened it with a YubiKey and the old password could also read the recovery keys: replace them afterwards with Replace recovery keys… on this page.",
    on: "Whoever opens an earlier copy of the vault file with a YubiKey alone can also read the recovery keys: if such a copy may exist, replace them afterwards with Replace recovery keys… on this page.",
    // The same for one recovery key besides any being removed: a count of
    // one is singular everywhere (DECISIONS 2026-09-09), and the acceptance
    // scenario's vault has exactly one (lib/rekey.ts rekeyLimitOf picks).
    // The command keeps its plural, since a command names the act.
    methodOne: (noun: string): string =>
      `If an earlier copy of the vault file may be out there, whoever opened it with this ${noun} could also read the recovery key, which still opens this vault: replace it afterwards with Replace recovery keys… on this page.`,
    methodOnOne:
      "If an earlier copy of the vault file may be out there, whoever opened it with this YubiKey could also read the recovery key and the vault's own record of the password: change the password afterwards, then replace the recovery key with Replace recovery keys… on this page.",
    recoveryOne:
      "Whoever has this recovery key can open an earlier copy or backup of the vault and read the other recovery key there: if one may be out there, use Replace recovery keys… on this page instead, which removes this one too. Check the unlock methods as well, and remove any you did not add.",
    changeOne:
      "If an earlier copy of the vault file may be out there, whoever opened it with a YubiKey and the old password could also read the recovery key: replace it afterwards with Replace recovery keys… on this page.",
    onOne: "Whoever opens an earlier copy of the vault file with a YubiKey alone can also read the recovery key: if such a copy may exist, replace it afterwards with Replace recovery keys… on this page.",
  },
  // The backup an act offers to take after its change (APP.md §13, ruled
  // 2026-10-27): the tick, the reason under the Last backup fact by what
  // ticks it (lib/rekey.ts backupReason), and the line in the tick's place
  // where no recovery key would remain, since a backup holds the recovery
  // keys alone (FORMAT R28).
  backupAfter: {
    tick: "Export a backup afterwards",
    whyRekey: "A backup made before the re-key is at the vault's old key, and restoring it would bring that key back; one made afterwards is not.",
    whyRecovery: (label: string): string => `Backups made while ${label} was an unlock method hold it and open with it; one made afterwards does not.`,
    whyReplace: "Backups made before now open only with the old recovery keys; one made afterwards holds the new ones.",
    whyReplaceOne: "Backups made before now open only with the old recovery key; one made afterwards holds the new one.",
    none: "No backup of this vault can be made without a recovery key.",
  },
  // The primaries, by act: [kept, re-keyed, kept and backed up, re-keyed
  // and backed up] (lib/rekey.ts primaryOf). Each names what the press
  // does; none ends on "…", since no dialog follows but the Save dialog,
  // which is the system's.
  primaries: {
    remove: ["Remove unlock method", "Remove and re-key", "Remove, then back up", "Remove, re-key and back up"],
    password: ["Change password", "Change and re-key", "Change, then back up", "Change, re-key and back up"],
    on: ["Turn on password", "Turn on and re-key", "Turn on, then back up", "Turn on, re-key and back up"],
    rotate: ["Re-key vault", "Re-key vault", "Re-key, then back up", "Re-key, then back up"],
    replace: ["Replace recovery keys", "Replace recovery keys", "Replace, then back up", "Replace, then back up"],
  } as Record<string, [string, string, string, string]>,
  // The replacement's primary for one key, in the singular.
  replaceOne: "Replace recovery key",
  removeUnlockMethod: {
    // Every password is named "Password", so two cards can read alike: the
    // title then says when this one was added, as its card does.
    title: (label: string, added?: string): string => (added ? `Remove ${label} (added ${added})?` : `Remove ${label}?`),
    // A backup holds the recovery keys alone (FORMAT R28): only a recovery
    // key's removal leaves backups that still open with it.
    body: (label: string, type: string): string =>
      type === "recovery"
        ? `${label} stops unlocking this vault now. Copies and backups of the vault made earlier still open with it.`
        : `${label} stops unlocking this vault now. An earlier copy of the vault file still opens with it; a backup never held it.`,
    // Replaced copies keep their unlock methods (APP.md §13); the damaged
    // copy does not open, so it is not counted.
    retired: (n: number, label: string): string =>
      n === 1
        ? `Enfold keeps 1 replaced vault file in its folder; if it had ${label}, it still opens with it until you delete it.`
        : `Enfold keeps ${n} replaced vault files in its folder; those that had ${label} still open with it until you delete them.`,
    // Only when the YubiKey removed leaves no YubiKey and no password: such
    // a vault cannot finish its setup from the lock screen while its
    // password is on, and this page adds an unlock method either way.
    lastProtector: "After this the vault has only its recovery keys: add the new YubiKey here now, or after unlocking with a recovery key.",
    // A re-key changes no archive's key and no recovery key (FORMAT R38),
    // so this promises neither.
    how: (label: string, type: string): string =>
      type === "recovery"
        ? `Re-keying gives the vault a new key in the same step, and every unlock method that stays gets it. An earlier copy or backup opened with ${label} then reaches nothing in this vault through ${label}; the other recovery keys it held still do, until they are replaced. An archive that existed then stays open to it, files added later included, until that archive is re-keyed with Re-key archive… on its page. The backups made while ${label} was an unlock method still open with it, and this vault can still import archives from them; where you keep them is yours to decide. The vault refuses a removal that would leave fewer than 2 unlock methods.`
        : `Re-keying gives the vault a new key in the same step, and every unlock method that stays gets it. An earlier copy of the vault file opened with ${label} then reaches nothing in this vault through ${label}; the recovery keys it held still do, until they are replaced. An archive that existed then stays open to such a copy, files added later included, until that archive is re-keyed with Re-key archive… on its page. A backup made before now is at the old key; the one taken afterwards is at the new one. The vault refuses a removal that would leave fewer than 2 unlock methods.`,
  },
  // Replace recovery keys… (ruled 2026-10-27): one ceremony and one step
  // that adds the new keys, removes every old one and re-keys the vault,
  // so that no new key is ever kept in the vault where an old one can read
  // it. The title and the body count, so the user knows how many sheets to
  // have ready; one key is singular everywhere.
  replaceRecoveryKeys: {
    title: (n: number): string => (n === 1 ? "Replace the recovery key?" : `Replace the ${n} recovery keys?`),
    body: (n: number): string =>
      n === 1
        ? "Enfold makes a new recovery key for you to keep, and the old one stops unlocking this vault. Copies and backups of the vault made before now still open with the old one."
        : `Enfold makes ${n} new recovery keys for you to keep, one for each, and the old ones stop unlocking this vault. Copies and backups of the vault made before now still open with the old ones.`,
    when: (n: number): string =>
      n === 1
        ? "Replace it when an earlier copy of the vault file, or a backup, may have been opened by someone else: whoever did could read the recovery key in it, which keeps opening this vault until it is replaced. If that copy was opened with a YubiKey or a password, remove that unlock method first, and remove any unlock method you did not add."
        : "Replace them when an earlier copy of the vault file, or a backup, may have been opened by someone else: whoever did could read every recovery key in it, and those keep opening this vault until they are replaced. If that copy was opened with a YubiKey or a password, remove that unlock method first, and remove any unlock method you did not add.",
    // With the password on, unless the chain the dialog was opened in has
    // changed it already (lib/rekey.ts replaceFacts).
    whenOn: " Change the password too: the vault's own record of it was readable the same way.",
    // The idle lock counts real input only, and 48 digits written by hand
    // is minutes without any: the line names the time before it starts.
    // n is the recovery keys replaced, as for the title: one is singular.
    ready: (minutes: number, n: number): string =>
      (n === 1
        ? "Have somewhere to keep the new recovery key before you start: print it, save it as a text file, or write it down. Nothing changes until you have kept it and confirmed."
        : "Have somewhere to keep each new recovery key before you start: print it, save it as a text file, or write it down. Nothing changes until you have kept every one and confirmed.") +
      ` Enfold locks after ${plural(minutes, "minute")} without input, which ends the replacement with nothing changed: printing or saving is quicker than writing 48 digits by hand.`,
    how: "Enfold makes the new keys first and shows each one in turn. When you confirm, one step adds them, removes every old recovery key and re-keys the vault, so the copy of each new recovery key the vault keeps is never readable with an old one. An earlier copy or backup then reaches nothing in this vault through its recovery keys; an archive that existed then stays open to it, files added later included, until that archive is re-keyed with Re-key archive… on its page. Each new recovery key takes the name of the one it replaces, so you know where it goes.",
    // The same for one key. "Its recovery keys" stays plural there: they
    // are the earlier copy's, which may have held more than the vault has
    // now, and are not counted.
    howOne: "Enfold makes the new recovery key first and shows it. When you confirm, one step adds it, removes the old recovery key and re-keys the vault, so the copy of the new recovery key the vault keeps is never readable with the old one. An earlier copy or backup then reaches nothing in this vault through its recovery keys; an archive that existed then stays open to it, files added later included, until that archive is re-keyed with Re-key archive… on its page. The new recovery key takes the name of the one it replaces, so you know where it goes.",
    howOn: " Such a copy also held the vault's record of the password, which only changing the password replaces.",
  },
  addUnlockMethod: {
    title: "Add an unlock method",
    body: "The vault asks for one of its unlock methods first, then adds the new one.",
    kind: "Unlock method",
    token: "YubiKey (PIN and touch)",
    password: "Password",
    passwordNotHere: "Password (not in a vault with a YubiKey)",
    recovery: "Another recovery key",
    passwordNext: "You choose the password once the vault has checked an unlock method.",
    nameKey: "Name this key",
    optional: "Optional",
    keyPlaceholder: "Named after its serial number",
    nameRecovery: "Name this recovery key",
    nameRecoveryHint: "Shown in the list of unlock methods.",
    needName: "Name the recovery key first.",
    passwordInherited: "This vault asks for its password with every YubiKey, so this one will too. Nothing is asked for here.",
    primary: "Add unlock method",
  },
  // Password with every YubiKey (APP.md §13): no field asks for the old
  // password, though a YubiKey's proof asks the current one with its PIN;
  // why is the disclosure's, not the body's. The body says what authorises
  // the act, and turning it on promises what is true of this file only: a
  // copy made before keeps opening with a YubiKey alone.
  passwordOn: {
    title: "Require a password with every YubiKey?",
    body: "Every YubiKey will ask for this password as well as its PIN, so a stolen YubiKey alone cannot open this vault file. The recovery key and a password unlock method do not change.",
    next: "You choose the password in the next step.",
    how: "The vault asks for one of its unlock methods first, then updates every YubiKey's entry without needing the YubiKey present. Re-keying gives the vault a new key in the same step. An earlier copy of the vault file, which opens with a YubiKey alone, then reaches nothing in this vault through that YubiKey; the recovery keys it held still do, until they are replaced. An archive that existed then stays open to it until that archive is re-keyed with Re-key archive… on its page.",
    primary: "Turn on password",
  },
  passwordChange: {
    title: "Change the password for every YubiKey?",
    body: "Every YubiKey asks for the new password from now on; one unlock method proves it is you first, and the other YubiKeys need not be present. You choose the password in the next step.",
    how: "This dialog has no field for the old password: whoever reaches this page has already opened the vault, and could add an unlock method of their own. Proving it is you with a YubiKey still asks for the current password with its PIN. Re-keying gives the vault a new key in the same step. An earlier copy of the vault file opened with a YubiKey and the old password then reaches nothing in this vault through them; the recovery keys it held still do, until they are replaced. An archive that existed then stays open to it until that archive is re-keyed with Re-key archive… on its page.",
    primary: "Change password",
  },
  // Turning it off weakens the vault, so its primary is a standard button,
  // not the accent (direction 9, ruled 2026-10-18), and the body says the
  // ceremony that follows will ask for an unlock method first.
  passwordOff: {
    title: "Stop requiring a password with every YubiKey?",
    body: "After this, a YubiKey opens the vault with its PIN and touch alone. One of the vault's unlock methods is asked for first.",
    primary: "Turn off the password",
  },
  // No placeholder in the name field: an invented name read as a default
  // already filled in (ruled 2026-10-14 for the key's name).
  newArchive: {
    title: "New archive",
    name: "Name",
    compression: "Compression",
    body: "Next, choose where to save it; its file name is filled in for you.",
    primary: "Create archive…",
  },
};

// ---- The Vault page (APP.md §6; "Keystore" until 2026-10-17) -----------
//
// The rail item and the page title are *Vault*; its foot names the vault
// alone, since the page says the count.
export const vaultCopy = {
  title: "Vault",
  unlockMethods: "Unlock methods",
  methodCount: (n: number): string => plural(n, "unlock method"),
  rule: codeCopy[Code.CodeInvariant],
  // A YubiKey's greyed Remove… says what to do (ruled 2026-10-27): a new
  // YubiKey, or a second recovery key added with Use recovery key instead
  // when none is at hand yet, either of which makes it removable.
  ruleYubiKey: "To remove this YubiKey, add a new one or a recovery key first: the vault always keeps at least 2 unlock methods.",
  lockedLine: "Unlock the vault to change its unlock methods.",
  // By the recovery keys' count: one is a vault never set up; two or more
  // were left by a removal, and the vault was set up then.
  setupLine: (n: number): string =>
    n === 1
      ? "This vault has only its recovery key. Add a YubiKey or a password to finish setting it up; the recovery key is asked for first."
      : "This vault has only its recovery keys. Add a YubiKey or a password; a recovery key is asked for first.",
  // Replace recovery keys…'s greyed titles (ruled 2026-10-27): a vault
  // with none to replace, and one whose unlock methods are all recovery
  // keys, where a new key could be shown again only through a YubiKey or a
  // password it does not have.
  noRecoveryToReplace: "This vault has no recovery key to replace.",
  replaceNeedsProtector: "Add a YubiKey or a password first: a new recovery key can be shown again only through one.",
  foot: (name: string): string => name,
  // A change's greyed title while Tampered (APP.md §13): the remedy is the
  // banner's button, on this page too (direction 9). It names no side: the
  // banner stands under the commands on the Archives and Archive pages and
  // over them on the Vault page (the notice slot, ruled 2026-10-26).
  changesOff: "Changes are off until this vault is replaced with a copy you trust; the banner on this page has the button.",
  // Section 3 (direction 10, phase 4a): every backup action in one row,
  // the two that need a lock among them — they lock and hand the lock
  // screen its ask.
  backups: "Backups and copies",
  backupsNote: "Choose a drive you keep apart from this PC.",
  // What can be said of the archives' files (lib/vaultfiles.ts): the
  // archive.file_missing notes counted over the whole registry; "all
  // present" only on the word of a Find missing files pass of this
  // unlock that measured every record; nothing while Locked, when the
  // core lists no registry.
  files: "Archive files:",
  filesMissing: (n: number): string => `${n} missing`,
  filesPresent: "all present",
  filesUnread: "not checked yet",
  filesLocked: "not known while the vault is locked",
  filesUnmeasured: "none could be checked from this PC",
  filesNoneOf: (n: number): string => `none missing of the ${n} checked`,
  // The backups card (direction 9, ruled 2026-10-18): the status, then
  // the consequence, then the buttons. The status compares the last
  // backup with what the vault holds — archives created and archives
  // re-keyed since (Vault.BackupStatus), never the last change, which
  // every save moves. Which line is drawn is lib/backup.ts's; the date
  // is relative, since recency is the point (sinceText).
  backupNever: (archives: number): string =>
    archives === 1 ? "Your 1 archive is only in this vault file." : `All ${plural(archives, "archive")} are only in this vault file.`,
  backupNeverHead: "No backup is recorded here.",
  backedUp: (when: string): string => `Backed up ${when}.`,
  // What the last backup lacks: the archives created since and those
  // re-keyed since, each counted, one sentence for both.
  backupLacks: (created: number, rekeyed: number): string => {
    const verb = created + rekeyed === 1 ? "is" : "are";
    if (created > 0 && rekeyed > 0) return `${plural(created, "archive")} created since and ${rekeyed} re-keyed since are not in it.`;
    if (created > 0) return `${plural(created, "archive")} created since ${verb} not in it.`;
    return `${plural(rekeyed, "archive")} re-keyed since ${verb} not in it.`;
  },
  backupHoldsAll: "It holds every archive key.",
  // A backup behind for a reason no count says (Vault.BackupStatus): made
  // before the vault was re-keyed, or holding only recovery keys a
  // replacement removed. Each stands for the counts, and the second wins,
  // being the more pressing (lib/backup.ts).
  backupBeforeRekey: "It was made before the vault was re-keyed.",
  backupKeysReplaced: "It opens only with recovery keys this vault no longer has.",
  // The old caveat, under the status: the stamp is this PC's record of an
  // export, not a look at the file (lastExportAt gates nothing).
  backupCaveat: "Recorded here, not proof the file is still there.",
  // The consequence, the card's sentence.
  backupsLine: "Your archives open only with this vault file or a backup of it. The recovery key opens either one; it cannot replace them.",
  // What *Inspect a backup…* reads off the file's label.
  inspected: {
    file: "File",
    kind: "What it is",
    dated: "Dated",
    thisVault: "This vault",
    methods: "Unlock methods",
    backup: "A backup: opens with its recovery key only",
    vault: "A full vault",
    yes: "Yes",
    no: "No, another vault",
    older: (when: string): string => `Older than this vault (changed ${when}).`,
    newer: "Newer than this vault. Lock, then replace this vault with it from the lock screen.",
  },
  thisVault: "This vault",
  name: "Name",
  // The vault's name is edited here only, through its one-field dialog
  // (direction 10): the form rule — the primary stays enabled, and an
  // empty or unchanged name is marked in the field.
  renameTitle: "Rename the vault",
  // A setting of this PC's (Settings.displayName), which the core's status,
  // and so the tray, follows at once (APP.md §3, Settings); the line says
  // what the name is, not where it is shown.
  renameLine: "The name this PC gives the vault. The file keeps its own name.",
  renamePrimary: "Rename",
  sameName: "This is the vault's name already.",
  nameLong: (max: number): string => `Up to ${max} characters.`,
  // The lock times are the vault's policy, written into it (direction
  // 10): each applies the moment it is chosen, one write per choice.
  unlockToChange: "Unlock to change",
  // The lock times' selects while the vault is locked (ruled 2026-10-19):
  // the core reads them from the vault, so locked it has none to give, and
  // "10 minutes (default)" would be a time the vault may not keep.
  unlockToSee: "Unlock to see",
  // The pickers' own titles (Shell.SaveFile, Shell.PickFiles).
  exportPicker: "Export a backup of the vault",
  inspectPicker: "Inspect a backup",
  idleLock: "Lock when idle",
  idleLine: (value: string): string => `After ${value} without mouse or keyboard. Up to 30 minutes.`,
  absoluteLock: "Always lock after",
  absoluteLine: "Even while you are working. Up to 8 hours.",
  defaultValue: (value: string): string => `${value} (default)`,
  lastChanged: "Last changed",
  file: "File",
  password: "Password with every YubiKey",
  // The order the PIN's step asks them in: the PIN, then the password (APP.md
  // §2.2; ruled 2026-10-17, it said "before its PIN").
  passwordOn: "On: every YubiKey asks for its PIN and then this password.",
  passwordOff: "Off: a YubiKey opens the vault with its PIN and touch alone.",
  change: "Change…",
  turnOn: "Turn on…",
  turnOff: "Turn off…",
  passwordBlocked: "Every unlock method of this vault is a YubiKey, so one password would then stand in front of all of them. Add a recovery key first.",
  passwordBlockedShort: "Every YubiKey would then need this password. Add a recovery key first.",
  replacedCopies: "Replaced copies",
  replacedLine: (n: number): string => `${n} in Enfold's folder, kept when a vault was replaced; yours to delete.`,
  damagedCopy: "Damaged copy",
  damagedLine: "Kept in Enfold's folder when the vault was rebuilt, for whatever can be salvaged from it; yours to delete.",
  show: "Show",
  // The ceremony dialog's title, by ceremony kind: the act it names.
  ceremonyTitle: {
    enroll: "Add an unlock method",
    remove: "Remove an unlock method",
    rotate: "Re-key the vault",
    reveal: "Show the recovery key",
    entangle: "Password with every YubiKey",
    export: "Export a backup",
    password: "Change the password for every YubiKey",
    replace: "Replace the recovery keys",
    replaceOne: "Replace the recovery key",
  } as Record<string, string>,
};

// ---- The lock screen (APP.md §6) -----------------------------------------
//
// The header's states (ruled 2026-10-17), then the panel's lines (one
// panel changing in place, ruled 2026-10-17). A button names the next
// thing that happens: the PIN's is *Continue*, since a touch follows;
// *Start again* only where it does start again.
export const lockCopy = {
  regionLabel: "Enfold, vault locked",
  // The lead of a test of a backup: the file it opens, by its name.
  testingFile: (name: string): string => `Testing ${name}`,
  header: {
    locked: "Locked",
    unlocking: "Unlocking",
    unlocked: "Unlocked",
    replacing: "Replacing the vault",
    // The same import at first run, where there is no vault to replace.
    restoring: "Restoring a vault",
    settingUp: "Setting up",
    testing: "Testing a backup",
    creating: "Creating the vault",
    // The panel's lead says where (busyLead); the header's word is short.
    busy: "Open elsewhere",
    damaged: "Damaged",
    absent: "Not found",
    none: "No vault yet",
    broken: "Needs attention",
    setupNeeded: "Needs setting up",
    // An unlock that stopped, while the panel says where.
    failed: "Not unlocked",
  },
  // A derivation that opens no vault says what it does (APP.md §6, ruled
  // 2026-10-17): a create's makes the vault; a setup's or an import's,
  // after the one that opened the vault or the backup, adds its first way
  // in. An unlock's keeps the step's own "Opening the vault…".
  derivingLead: {
    creating: "Creating the vault…",
    adding: "Adding the unlock method…",
  },
  firstRunLead: "Create your vault",
  firstRunLine: "A vault is one file: the keys to your archives, opened by a YubiKey, a password or a recovery key.",
  keptIn: "Enfold keeps it in",
  // The vault that is gone or damaged, named; the path is the line under
  // it, in mono (ruled 2026-10-17).
  damagedLead: (vault: string): string => `${vault} could not be opened`,
  absentLead: (vault: string): string => `${vault} is not where Enfold left it`,
  // The damaged vault's best remedy first: the vault a replacement
  // retired, by the date it was retired (vault-replaced-<unix>-<n>.eks).
  keptCopy: (date: string): string => (date ? `Enfold kept the vault it replaced on ${date}.` : "Enfold kept a vault it replaced."),
  useThatCopy: "Use that copy…",
  restoreOther: "Restore from another file…",
  rebuildLine: "Rebuilding starts a new, empty vault and keeps the damaged file beside it. Archives locked to this vault then open only from a backup or a copy of it.",
  findIt: "Find it…",
  findPicker: "Find the vault",
  tryAgain: "Try again",
  // A dialog follows, so the ellipsis (the verb table's rule).
  createInstead: "Create a new vault instead…",
  // The replaced copy's bar, when no vault is kept: restoring it is the
  // first-run verb.
  restoreIt: "Restore it…",
  // The step line, shown only while an unlock runs: the YubiKey's three
  // steps (the second named as the PIN field's label is) and the
  // recovery key's two. A password's way in is one step and draws none.
  stepsLabel: "Unlock steps",
  stepKey: "YubiKey",
  stepTouch: "Touch",
  stepUnlock: "Unlock",
  // The rest panel's caption: the state, then the last unlock, relative
  // because recency is the point (sinceText).
  restLine: (when: string): string => (when ? `Locked · last unlocked ${when}` : "Locked"),
  // A broken vault's caption names the header's state, not Locked.
  brokenLine: (when: string): string => (when ? `Needs attention · last unlocked ${when}` : "Needs attention"),
  // The PIN's step of a ceremony with no step line (a create, a setup, an
  // import's proof): its own instruction, as the YubiKey's and the
  // touch's steps have theirs.
  pinLead: "Type your YubiKey's PIN",
  // The password-only vault's lead, at rest and in its one step.
  unlockVault: (vault: string): string => `Unlock ${vault}`,
  // A YubiKey that is not this vault's, named for the vault (ruled
  // 2026-10-17).
  noMatch: (vault: string): string => `This YubiKey is not one of ${vault}'s unlock methods`,
  // Open elsewhere (another Enfold has the vault): its own lead, never
  // the rest panel's.
  busyLead: (vault: string): string => `${vault} is open in another Enfold`,
  busyLine: "Close it there, then try again.",
  // The recovery key's panel: where the digits come from, and a refusal
  // that points to the sheet's ID.
  recoveryLine: "Type or paste the 48 digits from the sheet. The key ID at its top says which sheet it is.",
  recoveryRefused: (vault: string): string => `These digits do not open ${vault}. If you have more than one sheet, check its key ID.`,
  // Listening for the YubiKey; under reduced motion the LED stands still,
  // so the lead says the waiting the breathing would.
  insertLine: (vault: string): string => `Waiting for a YubiKey that unlocks ${vault}.`,
  insertStill: "Waiting for your YubiKey…",
  // A create's, a setup's or a backup's import: the YubiKey waited for
  // is the one being added, which unlocks nothing yet.
  insertAdd: "Waiting for the YubiKey to add to this vault.",
  // The PIN's last try (ruled 2026-10-17): the consequence, then the way
  // out, on an attention bar above the field.
  lastTryHead: "Last try.",
  lastTry: "A wrong PIN now blocks this YubiKey's PIN; then only the recovery key opens the vault.",
  // The receipts under the touch's lead.
  pinAccepted: "PIN accepted",
  touched: "Touched",
  // A touch that timed out, said on the touch's step and in neutral ink:
  // nothing was refused (ruled 2026-10-17).
  stoppedWaiting: "The YubiKey stopped waiting",
  stoppedLine: "It waits only a short while for a touch.",
  setupLine: "This vault has only its recovery key so far. Choose its first unlock method; the recovery key is asked for first.",
  continue: "Continue",
  startAgain: "Start again",
  useRecoveryKey: "Use recovery key",
  useRecoveryInstead: "Use recovery key instead",
  unlockWithRecovery: "Unlock with recovery key",
  // The two maintenance commands, out of the daily unlock and behind one
  // quiet menu (ruled 2026-10-17).
  backups: "Backups and copies…",
  reopen: "Reopen the vault",
  // The recovery key's note and refusal are ceremonyCopy's, shared with
  // the Vault page's panel.
  testButton: "Test",
  replaced: "Replaced. Unlock it with one of its own unlock methods.",
  restored: "Restored. Unlock it with one of its own unlock methods.",
  restoredUnopened: "Restored, but the file could not be opened afterwards. Try again.",
  createdUnopened: "Created, but the file could not be opened afterwards. Try again.",
  // The panel's word when a ceremony ends, by kind.
  done: {
    unlock: "Unlocked",
    created: "Created",
    replaced: "Replaced",
    restored: "Restored",
    setup: "Set up",
    verify: "Opens",
  },
  unlockWithYubiKey: "Unlock with YubiKey",
  unlockWithPassword: "Unlock with password",
  testPicker: "Test that a backup opens",
  createPrimary: "Create vault",
  rebuildPrimary: "Rebuild vault",
  created: "Created, but it did not open on its own. Unlock it with the unlock method you chose.",
  createTitle: "Create a vault",
  rebuildTitle: "Rebuild the vault",
  keptInLabel: "Kept in",
  keepElsewhere: "Keep it elsewhere…",
  usualPlace: "Use the usual place",
  rebuildConsent: "Keep a copy of the damaged file and start a new vault",
  finishTitle: "Finish setting up",
  finishLead: (vault: string): string => `Finish setting up ${vault}`,
  finishBody: "The vault opens with its recovery key first; then the unlock method you choose here is added.",
  entangleTick: "Also require a password with every YubiKey in this vault",
  // The touch a cancelled ceremony left on the YubiKey (APP.md §2.2).
  // The primary says what pressing it does: it picks the touch up.
  pendingTouch: "The YubiKey is still blinking from the unlock you cancelled. Continue, or touch it or pull it out to end it.",
  continueUnlocking: "Continue unlocking",
  // The gear beside the state word: Settings needs no vault.
  settings: "Settings",
  // The vault file that is damaged or gone; the path stands between the
  // two halves, in mono.
  vaultAt: "Enfold keeps your vault at",
  damagedAt: ", and that file is no longer a readable vault.",
  missingAt: ", and that file is missing.",
  defaultName: "Personal vault",
  savePicker: "Where to keep the vault",
  // The create dialog's bars. Archives open on the vault being replaced
  // would save into the wrong one, so the create waits for them.
  stillOpen: (n: number): string =>
    n === 1
      ? "1 archive is still open. Close it first: its saves would land in the wrong vault."
      : `${plural(n, "archive")} are still open. Close them first: their saves would land in the wrong vault.`,
  // Archives left open when the vault locked (direction 9, ruled
  // 2026-10-18): still decrypted, and every change to them still saves
  // (APP.md §2.3, "What stays usable after a lock"). One is named, several
  // are counted; the names are VaultStatus.opened's.
  openWhileLocked: (names: readonly string[]): string => leadText(lockCopy.openWhileLockedLead(names)),
  openWhileLockedLead: (names: readonly string[]): Lead =>
    names.length === 1
      ? { head: `${names[0]} is still open in Enfold, decrypted.`, rest: "Changes to it are still saved." }
      : { head: `${plural(names.length, "archive")} are still open in Enfold, decrypted.`, rest: "Changes to them are still saved." },
  // The notice's close is the graceful leave; *Force close* / *Force
  // close them* once a leave has left the archives still held (APP.md
  // §2.3, ruled 2026-10-19; lib/leave.ts).
  closeIt: "Close it",
  closeThem: "Close them",
  forceCloseThem: "Force close them",
  // A file opened from Explorer while locked (direction 3): the line says
  // the open waits for the unlock, the file's name in strong after it.
  unlockToOpen: "Unlock to open",
  forgetOpen: (file: string): string => `Do not open ${file}`,
  // The rebuild's bar, around the damaged file's path.
  rebuildFileAt: "The file at",
  rebuildKept: "is not a readable vault. It is kept as a dated copy in Enfold's folder, never deleted: archives that only it holds can be opened only with it, if it can ever be repaired.",
  replaceKept: "A file already at the chosen place is kept as a dated copy in Enfold's folder.",
  // The tampered bar's Why? dialog (direction 9): its title says what the
  // bar says is off; the body is technicalCopy.tamperedWhy.
  tamperedWhyTitle: "Why changes are off",
};

// ---- The first unlock method's cards (APP.md §6, ruled 2026-10-17) -----
//
// The create, restore and finish-setup dialogs offer the first unlock
// method as cards: each says what it is in one line until it is chosen,
// and the chosen one opens on its fields. The recovery key's line is
// fixed: a create always makes one, and an adopted backup keeps its own.
// The line under the cards says what comes next, by the choice.
export const firstMethodCopy = {
  headCreate: "How you will unlock this vault",
  headImport: "How you will unlock the imported vault",
  token: { title: "YubiKey", line: "PIN and touch. Recommended." },
  password: { title: "Password", line: "At least 8 characters." },
  // The recovery key is not a choice: one caption line under the cards
  // (ruled 2026-10-17; it was a third card), so it must stay one line at
  // the dialog's 412 px measure (firstwayin.test.ts).
  recoveryMade: "Recovery key — always made. You will print it or save it right after.",
  recoveryKept: "Recovery key — the backup's own. It opens the backup first and stays.",
  nameYubiKey: "Name this YubiKey",
  // The tick's hint, shown ticked or not: the switch is the vault's, so
  // every YubiKey added later inherits it (APP.md §13, FORMAT.md §3.1).
  entangleHint: "Every YubiKey added later will ask for it too; the recovery key never does.",
  passwordField: "Password",
  again: "Type it again",
  // The radio group's refusal when Continue is pressed with no card chosen.
  chooseOne: "Choose a YubiKey or a password.",
  next: {
    token: "Next: insert the YubiKey, enter its PIN and touch it. Then your recovery key is shown once.",
    password: "Next: your recovery key is shown once.",
  },
  // An adopted backup opens with its own recovery key before anything is
  // added, and no new one is made.
  nextAdopt: {
    token: "Next: type the backup's recovery key, then insert the YubiKey, enter its PIN and touch it.",
    password: "Next: type the backup's recovery key.",
  },
  // The panel while the password typed in the dialog answers the prompt.
  sent: "Using the password you chose.",
};

// ---- Replacing the vault, and importing archives (APP.md §6, §13) -------
//
// *Replace this vault with a copy…* on the lock screen and *Restore from
// a vault or backup…* at first run are one dialog with two titles; what it
// says will happen differs by whether there is a vault to replace.
export const importCopy = {
  replaceTitle: "Replace this vault with a copy",
  restoreTitle: "Restore from a vault or backup",
  backupReplaces: "It replaces this vault and opens with its recovery key; then the unlock method chosen below is added. Archives added after this backup was made are not in it.",
  vaultReplaces: "It replaces this vault once it unlocks with one of its own unlock methods. Then unlock it as usual.",
  backupRestores: "It becomes your vault and opens with its recovery key; then the unlock method chosen below is added.",
  vaultRestores: "It becomes your vault once it unlocks with one of its own unlock methods.",
  proveWith: "Prove it with",
  methods: "Unlock methods",
  // Import archives from a backup… (APP.md §13): this vault stays.
  archivesTitle: "Import archives from a backup",
  archivesBody: "The ticked archives are added to this vault, which otherwise stays as it is. Where the two differ, this vault's own name and description are kept.",
  archivesPrimary: (n: number): string => (n > 0 ? `Import ${plural(n, "archive")}` : "Import archives"),
  // A copy made before a re-key brings the key it retired back (FileInfo
  // rekeyed), under the replace sentence.
  beforeRekey: "It was made before this vault was re-keyed: unlock methods removed since may still open it, and whatever is added to it later.",
  // The vault this replaces still owes a retirement (VaultStatus
  // retireOwed): the dated copy kept of it opens with what its last change
  // withdrew, so the confirmation says so after the replaced vault's own
  // unlock methods.
  replacedOwes: "It also still holds its state from before its last change, so it opens with any unlock method that change removed too. Delete it once the new vault opens.",
};

// ---- The Settings page (APP.md §6, ruled 2026-10-17) ---------------------
//
// The rows' names and lines. *Look* is gone until a second look exists
// (the ruling of 2026-09-06 stands); the recovery record is hidden until
// volume exports ship and comes back as *Repair data*.
//
// Settings is this PC only (direction 10, phase 4a): the lock times and
// the vault's name went to the Vault page, and every choice here applies
// the moment it is made — no save bar, no InfoBar on success.
export const settingsCopy = {
  title: "Settings",
  window: "Window",
  closeAction: "When the window closes",
  closeActionLine: "In the tray the vault stays unlocked until it locks. Open archives close with the window.",
  // The section holds the dictionary threshold alone while the recovery
  // record is hidden, so it is named for that (ruled 2026-10-17; it was
  // *Exports*).
  compression: "Compression",
  repairData: "Repair data",
  repairDataLine: "Used by volume exports (coming in 1.1).",
  dictionary: "Dictionary for small files",
  dictionaryLine: "Files below this size share a compression dictionary.",
  dictionaryOff: "Off",
  appearance: "Appearance",
  theme: "Theme",
  themeLine: "The window's own theme follows at the next open.",
  themeChoices: [
    ["system", "Follow Windows"],
    ["light", "Light"],
    ["dark", "Dark"],
  ] as [string, string][],
  about: "About",
  version: "Enfold 0.1.0",
  aboutLine: "Encrypted, compressed archives unlocked with a YubiKey.",
  dataFolder: "Data folder",
  dataFolderLine: "%LOCALAPPDATA%\\Enfold: the vault, the settings and the log.",
  foot: "These settings are this PC's.",
};

// compressedText is the preview's *Compressed* fact (ruled 2026-10-17:
// "Stored as zstd + dictionary, 71% smaller" said the method, which is
// the Details dialog's): how much smaller, or that it is stored as is.
export const compressedLabel = "Compressed";
export function compressedText(storage: string, savedPercent: number): string {
  if (storage === "raw") return "No, stored as is";
  return savedPercent > 0 ? `${savedPercent}% smaller` : "Yes";
}

// sinceText is the relative date, for where recency is the point — the
// last unlock, the last backup — and nowhere else: lists, facts and
// dialogs keep the ISO form. Today and yesterday keep the clock time; up
// to 59 days it counts days; past that, and for a time ahead of the
// clock, it is the ISO form after all.
export function sinceText(unix: number, nowMs: number = Date.now()): string {
  if (!unix) return dateTime(0);
  const then = new Date(unix * 1000);
  const now = new Date(nowMs);
  if (then.getTime() > nowMs) return dateTime(unix);
  const day = (d: Date): number => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  const days = Math.round((day(now) - day(then)) / 86_400_000);
  if (days === 0) return `today, ${clockTime(unix)}`;
  if (days === 1) return `yesterday, ${clockTime(unix)}`;
  return days < 60 ? `${days} days ago` : dateTime(unix);
}

// ---- The second register (ruled 2026-10-17) -----------------------------
//
// The one table where the format's words stand: an archive's *Details*
// dialog, a warning's "Why?" and a "How this works". strings.test.ts
// exempts it from the banned terms, and nothing else. The dialog was
// *Technical details* until 2026-10-26, when the user ruled it *Details*
// (APP.md §13): *Properties* would promise Explorer's whole sheet, and the
// table is still the second register under the plainer name.
export const technicalCopy = {
  title: "Details",
  titleOf: (name: string): string => `Details: ${name}`,
  why: "Why?",
  archiveId: "Archive ID",
  currentKid: "Current KID",
  keyVersion: "Key version",
  keyVersionValue: (n: number): string => (n > 0 ? `v${n}` : "—"),
  created: "Created",
  lastStoredSize: "Last stored size",
  lastSaved: "Last saved",
  revision: "Revision",
  lastWriter: "Last writer",
  // Two rows, each its own number: one combined label wrapped in the
  // dialog's label column, and the glossary keeps "fingerprint" for the
  // hash (direction 9).
  seq: "Last seq",
  fingerprintSeq: "Fingerprint at seq",
  storedAs: "Stored as",
  policy: "Policy",
  lastPath: "Last path",
  fingerprint: "Fingerprint (SHA-256 of the file)",
  // One caption under the dialog's rows (direction 9): what the
  // fingerprint is good for, and what it is not.
  fingerprintNote: "A fingerprint tells this file from another copy of it, such as a restored backup. It is not a check of the file's contents.",
  receipt: "Receipt",
  receiptPending: "Pending",
  // A value's menu (APP.md §13): the value, or every row as
  // "Label: value" lines for a report (direction 7).
  copy: "Copy",
  copyAll: "Copy all as text",
  keyVersions: "Key versions",
  columns: { kid: "KID", created: "Created", retired: "Retired", state: "State" },
  deriving: "Deriving the session keys.",
  // The Tampered banner's "Why?" (APP.md §13), worded from
  // VaultStatus.TamperedReason: the slot region does not match the
  // registry (FORMAT.md R25), or it does not belong with the superblock
  // (§6.2). With no reason to hand — a status from before the cause was
  // known — both causes are named, because the remedy is the same and
  // guessing one would be a claim.
  tamperedWhy: (reason?: string): string => {
    const why =
      reason === Code.CodeTamperedHash
        ? "does not match its registry"
        : reason === Code.CodeTamperedGeneration
          ? "does not belong with its superblock: it holds a key from another generation"
          : "does not match its registry, or does not belong with its superblock";
    return `The vault's slot region ${why}. Every change is disabled until a copy of this vault replaces it.`;
  },
  staleWhy: "One of the vault file's two superblock copies is damaged. Enfold opened the good one; the next write repairs the other.",
  notThisVaultWhy: "No VMK this vault keeps, current or retired, opens that file's registry: it is another vault's, or a backup from a generation this vault no longer keeps, as when this vault was rolled back to an older copy.",
  envelopeStaleWhy: "The archive's envelope still names a retired key version; Refresh fingerprint or a second re-key rewrites it.",
};

// ---- The recovery key's dialog (APP.md §6; direction 9, ruled 2026-10-18)
//
// The key ID once, as the heading's label; both risks in two sentences —
// the key opens the vault without a YubiKey, and opens nothing without
// the vault file or a backup; Print… suggested; the text file's caution
// in the body, since its own dialog went; and the optional readback.
export const revealCopy = {
  title: "Recovery key",
  // A create's or an added key's first showing.
  titleNew: "Your recovery key",
  idLabel: "Recovery key ID",
  leadOpens: "With this key, the vault file or a backup opens without your YubiKey. Keep it from others.",
  leadNothing: "Without the vault file or a backup, this key opens nothing. Keep those too.",
  loading: "Loading…",
  digitsLine: (n: number): string => `${n} digits. They can be shown again from the Vault page: unlock, then prove a YubiKey or a password once more.`,
  // The core still refuses Enfold's own folder and the vault's
  // (vault.recovery_place); the rest is the user's to choose.
  textFile: "A text file is plain text. Keep it off this PC and out of any folder that syncs.",
  check: "Check what I wrote",
  group: (n: number): string => `Group ${n}`,
  compare: "Compare it with what you wrote.",
  onlyACheck: "Only a check: nothing here is required.",
  print: "Print…",
  printAgain: "Print again…",
  save: "Save as a text file…",
  saveAgain: "Save again…",
  wroteDown: "I have written it down",
  done: "Done",
  saveTitle: "Save the recovery key",
  savedTo: (path: string): string => `Saved to ${path}.`,
  sentToPrint: (title: string): string => `Sent to print as “${title}”.`,
  writtenDown: "Written down.",
  gone: "The recovery key could not be shown here: its one-time link was already used.",
  goneHow: "Show it again from the Vault page: unlock, then prove a YubiKey or a password once more.",
  notHeld: "The recovery key is no longer held. Show it again from the Vault page.",
  // A replacement's new key, shown before it is the vault's (APP.md §13,
  // ruled 2026-10-27): until the confirmation it opens nothing, so the
  // lead says from when it does, and "by itself" since "alone" read as
  // "only this key". No second confirmation here either (ruled 2026-09-09,
  // held for the replacement): the act's own confirmation is the one. Keep
  // mode, like every showing, is not dismissed by Escape or the backdrop:
  // Cancel is its one way out.
  titleKeep: (i: number, n: number): string => (n === 1 ? "Your new recovery key" : `New recovery key ${i} of ${n}`),
  replaces: (label: string, id: string): string => `Replaces ${label} · ${id}.`,
  keepOpens: "Once you confirm, this recovery key opens the vault file by itself, and backups made from then on. Keep it from others.",
  // n is the digits; count, the new keys the replacement shows
  // (CeremonyState.keepCount): one is singular.
  keepDigitsLine: (n: number, count: number): string =>
    count === 1 ? `${n} digits. Nothing changes until this recovery key is kept and you confirm.` : `${n} digits. Nothing changes until every new recovery key is kept and you confirm.`,
  // A key that cannot be shown before the commit: the ordinary showing's
  // words ("show it again from the Vault page") are false then.
  keepGone: "This recovery key could not be shown here. Nothing has changed: cancel, and start the replacement again.",
  next: "Next recovery key",
  continue: "Continue",
  // A recovery key added with Use recovery key instead: no YubiKey or
  // password is at hand to show it again with.
  digitsLineLater: (n: number): string => `${n} digits. They can be shown again from the Vault page only after you add a YubiKey or a password and prove it.`,
};

// ---- Replace recovery keys… inside its ceremony (APP.md §13, ruled 2026-10-27)
//
// The confirmation pairs each new key with the one it replaces, under the
// old key's name, and the end says the same as its receipt (FORMAT.md
// §18.4: the sheets carry the IDs). What the end says of earlier backups
// is what is true of them, never an order to destroy them: a merge still
// reads them. A count of one is singular everywhere (the One forms,
// chosen by keepCount).
export const replaceCopy = {
  ready: "Ready to replace",
  newKeys: "New",
  replacedKeys: "Replaced",
  readyLine: "The replaced keys stop unlocking this vault, and every new one works from now on. Check that you can read each new recovery key before you go on.",
  readyLineOne: "The replaced key stops unlocking this vault, and the new one works from now on. Check that you can read the new recovery key before you go on.",
  pair: (newId: string, oldId: string): string => `${newId} replaces ${oldId}`,
  backedUp: "Destroy the replaced keys' sheets and files once the new backup opens (Test that a backup opens…). The backups made before still open with the replaced keys, and this vault can still import archives from them.",
  noBackup: "No backup holds the new keys yet, and every earlier one opens only with the replaced keys. Export one from Backups and copies, and destroy the replaced keys' sheets and files once it opens.",
  afterFailedBackup: "Destroy the replaced keys' sheets and files once a new backup opens.",
  backedUpOne: "Destroy the replaced key's sheet and file once the new backup opens (Test that a backup opens…). The backups made before still open with the replaced key, and this vault can still import archives from them.",
  noBackupOne: "No backup holds the new key yet, and every earlier one opens only with the replaced key. Export one from Backups and copies, and destroy the replaced key's sheet and file once it opens.",
  afterFailedBackupOne: "Destroy the replaced key's sheet and file once a new backup opens.",
  // The Vault page's notice after a replacement ended without replacing
  // anything once keys were shown (lib/rekey.ts replaceNotice): the IDs,
  // since an ID is not secret and the sheets carry them.
  notAdded: (ids: string[]): string =>
    ids.length === 1
      ? `Nothing was replaced. Recovery key ${ids[0]} was shown but never added: destroy what you kept of it.`
      : `Nothing was replaced. Recovery keys ${listJoin(ids)} were shown but never added: destroy what you kept of them.`,
  // Its outcome not known (vault.indeterminate or vault.conflict): never
  // "destroy", and words true of both causes.
  unknown: (ids: string[]): string =>
    ids.length === 1
      ? `Recovery key ${ids[0]} was shown, and Enfold could not tell whether it replaced the old one. Reopen the vault; the Vault page then lists the recovery keys it has. Keep both sheets until then.`
      : `Recovery keys ${listJoin(ids)} were shown, and Enfold could not tell whether they replaced the old ones. Reopen the vault; the Vault page then lists the recovery keys it has. Keep every sheet until then.`,
};

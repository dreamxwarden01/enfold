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
//   they appear only in technicalCopy below — the Technical details
//   dialog, a "Why?" or a "How this works" disclosure — never on a main
//   surface (the two registers).
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
// archive…* and *Re-key vault…* are two operations with two names; the
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
};

// stepText is stepCopy for any step value, the generator's zero included.
export function stepText(step: CeremonyStep): StepCopy {
  return (stepCopy as Record<string, StepCopy>)[step] ?? { title: "", body: "" };
}

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
      : `Groups ${groups.slice(0, -1).join(", ")} and ${groups[groups.length - 1]} do not check out. Compare their digits with the sheet.`,
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
  doneTitle: {
    enroll: "Unlock method added",
    remove: "Unlock method removed",
    rotate: "Vault re-keyed",
    export: "Backup saved",
  } as Record<string, string>,
  // Whose recovery key the panel asks for follows the act (APP.md §13).
  recoveryNote: {
    verify: "The backup's recovery key. Nothing here changes.",
    import: "The copy's own recovery key: it replaces this vault only once it opens.",
    records: "This vault's recovery key first, if the file is a copy of this vault; if it does not open the file, the file's own recovery key is asked for next.",
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
  // remedy is one, and which check failed is the Technical details' and
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
  // The line without the path, for a list. What the toast says names the
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
// the code in muted mono (Toasts.svelte); null for every other line. The
// fallback may stand after a prefix, as an operation's name does in its
// toast (ops.ts opErrorLine).
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
// that leaving the page does not.
export const archivePageCopy = {
  lockedBanner: (name: string): string => lockedOpenText([name]),
  lockedBannerLead: (name: string): Lead => lockedOpenLead([name]),
  closeNow: "Closes the archive now, even while something is playing.",
  deleteNeedsUnlock: "Unlock the vault first: the record is the vault's.",
  deleteTampered: "This vault file does not match itself, so changes to it are off.",
  // A greyed command's title says why it is greyed (APP.md §6).
  extractNothing: "This archive holds nothing yet",
  renameNeedsOne: "Select one item to rename",
  deleteNeedsSome: "Select what to delete",
  extractNeedsSome: "Select what to extract",
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
// toast counts with (ops.ts opLabel).
export const dragOutCopy = {
  dragging: "Dragging",
  extracting: "Extracting",
  name: "Dragging out",
};

// The foot's words for a running operation (ops.ts opLabel),
// here so the glossary's walk sees them. A re-key is named by the verb
// its button says, *Re-key archive…* (commandCopy.rekeyArchive): one act,
// one name, in the foot and in its toast (ruled 2026-10-17). The bare
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
  // An empty folder names itself, and the root the archive (the polish
  // review's small fix 30), with both ways to fill it.
  empty: (folder: string): string => `${folder || "This folder"} is empty. Drop files here, or use Add.`,
  toRoot: "Back to the top of the archive",
  // the header cell's tooltip says what the first click will do
  sortBy: (column: string): string => `Sort by ${column}`,
  selected: (n: number): string => `${n} selected`,
  selectOne: "Select a file",
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
// not one is a toast — and the third is this dialog: the file *is* an
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

// What a launch that named more than one file says about the rest: the
// first is opened and the others are named, the way the purge names the
// keys it dropped (APP.md §14, "the rest are named in a toast"). The
// names are leaves: the folder they came from is the one the user was
// standing in, and says nothing.
// archive.open_elsewhere names the file the archive is already open from
// (APP.md §14): two copies of one archive, and the handle is on the other
// one. The whole path, not the leaf: the point is which of two files that
// look alike is the one being read.
export function openElsewhereText(path: string): string {
  return `This archive is already open from ${path}. Close it there first.`;
}

// purgedText is the toast the purge raises at the end of an unlock (APP.md
// §13, ruled 2026-10-17): the consequence first, and in the user's words —
// it said "The key of … was dropped", the format's act on a main surface.
export function purgedText(names: string[]): string {
  return names.length === 1
    ? `"${names[0]}" was forgotten more than 30 days ago, so it can no longer be opened.`
    : `${names.length} forgotten archives can no longer be opened: ${names.join(", ")}. Each was forgotten more than 30 days ago.`;
}

export function moreNotOpened(names: string[]): string {
  return `${plural(names.length, "more archive")} ${names.length === 1 ? "was" : "were"} not opened: ${names.join(", ")}. Enfold opens one at a time.`;
}

// The Archives list (APP.md §6, ruled 2026-09-10): its rows and header
// carry the file list's checkboxes; the details pane shows one row or,
// with several ticked and none of them the last clicked, a count.
export const archivesCopy = {
  listLabel: "Archives",
  tickAll: "Select every archive shown",
  selectOne: "Select an archive.",
  selectedCount: (n: number): string => `${plural(n, "archive")} selected`,
  selectedName: (name: string): string => `${name} selected`,
  // The bottom panel the details become in a narrow body (APP.md §6,
  // refined 2026-09-12): the same eyebrow the side pane carries, and a
  // header that is the control — so its name is what a click will do,
  // and which archive it will do it for.
  panelLabel: "Selected archive",
  panelEyebrow: "Selected",
  panelShow: (name: string): string => `${name} — show the details`,
  panelHide: (name: string): string => `${name} — hide the details`,
  panelNone: "No archive selected",
  // Open file… (Ctrl+O) and the empty page's buttons (APP.md §13, §14,
  // ruled 2026-10-14): an .efd from anywhere, opened the way Explorer's
  // double-click opens it.
  openFile: "Open file…",
  openFileTitle: "Open an archive",
  emptyVault: "No archives in this vault yet.",
  emptyOpen: "Open an .efd file…",
  // The list's other empty states.
  noMatch: "No archive matches that.",
  allHidden: "No archives are shown. Hidden and forgotten ones are listed under Show hidden.",
  showHidden: "Show hidden",
  filter: "Filter",
  // The details pane's facts (ruled 2026-10-17): user words only. The key
  // version and the KID went to Technical details, where the KID was
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
  technicalDetails: "Technical details…",
  // The Missing chip is a button that runs Locate… for its row (direction
  // 7): the chip names the trouble, the button is its cure.
  missingChip: (why: string): string => `${why} ${commandCopy.locate} asks where it is now.`,
  // The locked banner (direction 9): the archives the lock left open, by
  // name from VaultStatus.opened, and that they still save.
  lockedBanner: (names: readonly string[]): string => lockedOpenText(names),
  lockedBannerLead: (names: readonly string[]): Lead => lockedOpenLead(names),
};

// commandCopy is the verb table (the glossary above): a command's label
// wherever it is offered — the command bar, a context menu, a card, a
// banner — so one operation never goes by two names. "…" is there
// exactly when a dialog or a picker follows.
export const commandCopy = {
  open: "Open",
  close: "Close",
  // The kill switch (APP.md §2.3): the Archive page's head and the locked
  // banners; *Close all* is the Archives page's for several left open.
  closeArchive: "Close archive",
  closeAll: "Close all",
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
  goThere: "Go there",
  lockNow: "Lock now",
  unlock: "Unlock",
  // The Archive page's own commands on its rows (APP.md §6).
  rename: "Rename…",
  delete: "Delete…",
  createFolder: "Create folder…",
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
  extract: keyHint(commandCopy.extract, "Ctrl+E"),
  delete: keyHint(commandCopy.delete, "Del"),
  rename: keyHint(commandCopy.rename, "F2"),
  createFolder: keyHint(commandCopy.createFolder, "Ctrl+Shift+N"),
  newArchive: keyHint(commandCopy.newArchive, "Ctrl+N"),
  openFile: keyHint(archivesCopy.openFile, "Ctrl+O"),
  deleteArchive: keyHint(commandCopy.deleteArchive, "Del"),
  technicalDetails: keyHint(archivesCopy.technicalDetails, "Alt+Enter"),
  filter: keyHint(archivesCopy.filter, "Ctrl+F"),
  lockNow: keyHint(commandCopy.lockNow, "Ctrl+L"),
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
  },
  // `until` is the purge's date (APP.md §13): the first unlock after it
  // drops the key.
  forgetArchive: {
    title: (name: string): string => `Forget ${name}?`,
    body: (until: string): string => `The file stays where it is. You can restore it from Show hidden until ${until}; after that its key is gone and the file cannot be opened.`,
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
    body: "The file at that path stays exactly as it is. You can restore the archive from Show hidden for 30 days.",
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
  rekeyVault: {
    title: "Re-key the vault?",
    body: "Every unlock method gets the new key now; no YubiKey is needed for this. Backups made before today keep opening with the methods they had.",
    lastBackup: "Last backup",
    noBackup: "None recorded here",
    backupFirst: "Export a backup first",
    backupWhy: "A backup is what brings your archives back if this vault is lost.",
    primary: "Re-key vault",
    primaryWithBackup: "Back up, then re-key",
  },
  removeUnlockMethod: {
    title: (label: string): string => `Remove ${label}?`,
    body: (label: string): string => `${label} stops unlocking this vault now. Copies and backups of the vault made earlier still open with it.`,
    how: "What such a copy opens reaches this vault's archives too, until every recovery key that copy held is replaced here and the vault is then re-keyed. The vault refuses a removal that would leave fewer than 2 unlock methods.",
    primary: "Remove unlock method",
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
  // Password with every YubiKey (APP.md §13): the old password is never
  // asked, and why is the disclosure's, not the body's.
  passwordOn: {
    title: "Require a password with every YubiKey?",
    body: "Every YubiKey will ask for this password as well as its PIN, so a stolen YubiKey alone cannot open the vault. The recovery key and a password unlock method do not change.",
    next: "You choose the password in the next step.",
    how: "The vault asks for one of its unlock methods first, then updates every YubiKey's entry without needing the YubiKey present.",
    primary: "Turn on password",
  },
  passwordChange: {
    title: "Change the password for every YubiKey?",
    body: "Every YubiKey asks for the new password from now on; none needs to be present. You choose it in the next step.",
    how: "The old password is not asked for: whoever reaches this page has already opened the vault, and could add an unlock method of their own.",
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
  lockedLine: "Unlock the vault to change its unlock methods.",
  setupLine: "This vault has only its recovery key. Add a YubiKey or a password to finish setting it up; the recovery key is asked for first.",
  foot: (name: string): string => name,
  // A change's greyed title while Tampered (APP.md §13): the remedy is the
  // banner's button, on this page too (direction 9).
  changesOff: "Changes are off until this vault is replaced with a copy you trust; the banner above has the button.",
  backups: "Backups",
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
  // The old caveat, under the status: the stamp is this PC's record of an
  // export, not a look at the file (lastExportAt gates nothing).
  backupCaveat: "Recorded here, not proof the file is still there.",
  // The consequence, the card's sentence.
  backupsLine: "Your archives open only with this vault file or a backup of it. The recovery key opens either one; it cannot replace them.",
  importPointer: "To take archives out of a backup without replacing this vault, use Import archives from a backup… on the Archives page.",
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
    testHint: "To prove it opens, lock the vault and use Test that a backup opens… on the lock screen.",
  },
  thisVault: "This vault",
  name: "Name",
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
  closeIt: "Close it",
  closeThem: "Close them",
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
};

// ---- The Settings page (APP.md §6, ruled 2026-10-17) ---------------------
//
// The rows' names and lines. *Look* is gone until a second look exists
// (the ruling of 2026-09-06 stands); the recovery record is hidden until
// volume exports ship and comes back as *Repair data*.
export const settingsCopy = {
  title: "Settings",
  session: "Session",
  unlockToChange: "Unlock to change",
  idleLock: "Lock when idle",
  idleLine: (value: string): string => `After ${value} without mouse or keyboard. Up to 30 minutes.`,
  absoluteLock: "Always lock after",
  absoluteLine: "Even while you are working. Up to 8 hours.",
  defaultValue: (value: string): string => `${value} (default)`,
  closeAction: "When the window closes",
  // The section holds the dictionary threshold alone while the recovery
  // record is hidden, so it is named for that (ruled 2026-10-17; it was
  // *Exports*).
  compression: "Compression",
  repairData: "Repair data",
  repairDataLine: "Used by volume exports (coming in 1.1).",
  dictionary: "Dictionary for small files",
  dictionaryLine: "Files below this size share a compression dictionary.",
  appearance: "Appearance",
  theme: "Theme",
  themeLine: "The window's own theme follows at the next open.",
  vault: "Vault",
  vaultName: "Vault name",
  vaultNameLine: "Shown on the lock screen and in the tray.",
  about: "About",
  aboutLine: "Encrypted, compressed archives unlocked with a YubiKey.",
  foot: "These settings are this PC's; the lock times travel with the vault.",
};

// compressedText is the preview's *Compressed* fact (ruled 2026-10-17:
// "Stored as zstd + dictionary, 71% smaller" said the method, which is
// Technical details'): how much smaller, or that it is stored as is.
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

// ---- Technical details (the second register, ruled 2026-10-17) ---------
//
// The one table where the format's words stand: the Technical details
// dialog (an archive's, the vault's), a warning's "Why?" and a "How this
// works". strings.test.ts exempts it from the banned terms, and nothing
// else.
export const technicalCopy = {
  title: "Technical details",
  titleOf: (name: string): string => `Technical details: ${name}`,
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
};

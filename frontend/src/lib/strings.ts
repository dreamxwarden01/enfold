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

export interface StepCopy {
  title: string;
  body: string;
}

// stepCopy is what the ceremony panel says at each step; the token flow's
// three moments (insert, PIN, touch) are the ones the lock screen builds
// around.
export const stepCopy: Record<StepKey, StepCopy> = {
  [CeremonyStep.StepWaitingForKey]: { title: "Insert your YubiKey", body: "The YubiKey that unlocks this vault." },
  [CeremonyStep.StepTwoKeys]: { title: "More than one YubiKey is inserted", body: "Leave just one in to continue." },
  [CeremonyStep.StepProbing]: { title: "Reading the YubiKey", body: "Checking that it is one of this vault's unlock methods." },
  // The page carries the button the body names (lockCopy.useRecoveryKey):
  // a sentence that offers the recovery key has something to press.
  [CeremonyStep.StepNoMatch]: { title: "This YubiKey is not one of this vault's unlock methods", body: "Try another YubiKey, or use your recovery key." },
  [CeremonyStep.StepBusy]: { title: "The YubiKey is in use", body: "Another program holds the YubiKey. Close it, or wait, then try again." },
  [CeremonyStep.StepPassword]: { title: "Password", body: "" },
  [CeremonyStep.StepPIN]: { title: "PIN", body: "The YubiKey counts wrong tries, not Enfold." },
  [CeremonyStep.StepTouch]: { title: "Touch your YubiKey now", body: "It blinks until you touch it." },
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
// strip and the Vault page's panel alike (APP.md §6, ruled 2026-10-17).
// Card 2 is named in the order its fields stand: the PIN first, as the
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
  // No match and Blocked where nothing offers the recovery key (an
  // import's proof, a create, the Vault page's panel): the line says what
  // can be done instead of naming a button the screen does not have.
  noMatchWithoutRecovery: "Try another YubiKey.",
  blockedWithoutRecovery: "The PIN is blocked.",
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
  // The enrol's swap, when the core names the YubiKeys; the step's own
  // body is the line when it names neither.
  swapBoth: (remove: string, insert?: string): string => `Remove ${remove}, then insert ${insert || "the YubiKey to add"}.`,
  swapInsert: (insert: string): string => `Insert ${insert}.`,
  // A backup check's outcome, one wording on the lock screen and in the
  // panel (it read "Opens. …" in the panel).
  verified: (archives: number): string => `The backup opens. ${plural(archives, "archive")} inside.`,
  done: "Done.",
};

// codeCopy is what an error code says. Every code has an entry; the test
// checks it.
export const codeCopy: Record<CodeKey, string> = {
  [Code.CodeInternal]: "Something went wrong inside Enfold. The log in the data folder has the details.",
  [Code.CodeNoVault]: "No vault is open.",
  [Code.CodeVaultLocked]: "The vault is locked.",
  // The reopen banner's line (ruled 2026-10-17): what happened, then what
  // to do; the page carries *Reopen*.
  [Code.CodeVaultBroken]: "A save could not be confirmed. Reopen to see which version was kept.",
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
  [Code.CodeIndeterminate]: "A save could not be confirmed. Reopen to see which version was kept.",
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
  [Code.CodeArchiveCopyMismatch]: "This is not the copy the vault saved last; maybe an older backup. Your next save makes it the current one.",
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
  [Code.CodeTooSlow]: "This would not finish before the session locks. Extend the session first.",
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

// ---- The archive page's own lines (APP.md §2.3, §6, ruled 2026-09-10) --
//
// The locked banner is one plain line: the archive has no timeout of its
// own, and what it owes the vault is already said by the status strip. The
// two titles are what stops *Delete archive…*, said on the button itself,
// and the third is what the kill switch does that leaving the page does not.
export const archivePageCopy = {
  lockedBanner: "The vault is locked. This archive stays open while you are here.",
  closeNow: "Closes the archive now, even while something is playing.",
  deleteNeedsUnlock: "Unlock the vault first: the record is the vault's.",
  deleteTampered: "This vault file does not match itself, so changes to it are off.",
};

// reclaimedText is what a finished *Reclaiming space* says: the bytes the
// file system got back — the archive's own shrinking (OpView.Returned) —
// said apart from the bytes the run moved, since the two are never the same
// figure (APP.md §2.3). Its caller decides when there is anything to say
// (ops.ts reclaimedLine).
export function reclaimedText(returned: number): string {
  return `Reclaimed ${bytes(returned)}`;
}

// The drag out of the window on the operation strip (APP.md §3, ruled
// 2026-09-11 after the first real drag): on the strip from the moment the
// drag starts, so that a staging over in an instant is a change inside a
// strip already on the screen and never a strip that flashes. The hover
// shows the label alone, *Dragging 2 items*, since nothing is chosen yet
// (ruled 2026-10-14); from the drop on it is *Extracting 2 items*, and
// the staging adds the bar, which moves by bytes and offers
// *Cancel*; and when the bytes are written the bar simply stays full, with
// no *Cancel* and no words of its own — from then on Explorer's own window
// is in front and the only control there is — until DoDragDrop returns and
// the strip goes. A finished drag out that failed is named by the verb its
// toast counts with (ops.ts opLabel).
export const dragOutCopy = {
  dragging: "Dragging",
  extracting: "Extracting",
  name: "Dragging out",
};

// The operation strip's words for a running operation (ops.ts opLabel),
// here so the glossary's walk sees them. A re-key is named by the verb
// its button says, *Re-key archive…* (commandCopy.rekeyArchive): one act,
// one name, on the strip and in its toast (ruled 2026-10-17). The bare
// form stands when the archive's name is not known.
export const opCopy = {
  rekeying: "Re-keying",
  rekeyingArchive: "Re-keying archive",
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
};

// conflictTitle is the question asked first: one conflict names the file,
// several count them.
export function conflictTitle(n: number, name: string): string {
  if (n === 1) return `The destination already has a file named ${name}`;
  return `The destination has ${plural(n, "file")} with the same names`;
}

// The rest of the question, one way for one conflict and another for
// several: *Replace* / *Skip* / *Compare both files* against *Replace all*
// / *Skip all* / *Let me decide for each file*.
export const conflictCopy = {
  oneBody: "It was left where it is. The copy in the archive can replace it, or both can be kept.",
  andMore: (n: number): string => `and ${n} more.`,
  compare: (n: number): string => (n === 1 ? "Compare both files" : "Let me decide for each file"),
  skip: (n: number): string => (n === 1 ? "Skip" : "Skip all"),
  replace: (n: number): string => (n === 1 ? "Replace" : "Replace all"),
};

// The compare list after Windows Explorer's: the two columns, the note
// under the ticks, and its two buttons.
export const compareCopy = {
  title: "Which files do you want to keep?",
  fromArchive: "Files from the archive",
  inDestination: "Files already in the destination",
  note: "A tick on both sides keeps both: the file from the archive comes out under a new name.",
  cancel: "Cancel",
  go: "Continue",
};

// skipSameText is the foot's tick, singular at one and empty at none —
// there is nothing to tick when no two copies match.
export function skipSameText(n: number): string {
  return n === 0 ? "" : `Skip ${plural(n, "file")} with the same date and size`;
}

// compareCountText is the foot's count beside that tick: what *Continue*
// will do, before it does it — "3 will be replaced · 2 skipped" (ruled
// 2026-10-17). A part with nothing in it is left out, and keep both is
// said the way the note under the ticks says it.
export function compareCountText(c: { replace: number; both: number; skip: number }): string {
  const parts: string[] = [];
  if (c.replace > 0) parts.push(`${c.replace} will be replaced`);
  if (c.both > 0) parts.push(`${c.both} kept under a new name`);
  if (c.skip > 0) parts.push(`${c.skip} skipped`);
  return parts.join(" · ");
}

// retriesText is the count shown with the PIN prompt. The card is asked
// before the prompt, so the count is always known here; when the card is
// verified it cannot say, and the copy says that instead of a number. It
// never says "0".
export function retriesText(s: Pick<CeremonyState, "retries" | "retriesKnown" | "verified">): string {
  if (!s.retriesKnown || s.verified) return "attempts left unknown";
  if (s.retries <= 0) return "";
  return s.retries === 1 ? "1 attempt left — the last one" : `${s.retries} attempts left`;
}

// tamperedCopy is the Tampered banner (APP.md §13): what happened and what
// to do, the same words whatever VaultStatus.TamperedReason says, since
// the remedy is one; the page carries *Replace with a copy…*. Which check
// failed — and the rule that, with no reason to hand, both causes are
// named, because guessing one would be a claim — moved to
// technicalCopy.tamperedWhy, the banner's "Why?" (ruled 2026-10-17).
export function tamperedCopy(_reason?: string): string {
  return "This vault file does not match itself, so it may have been changed outside Enfold. Changes to the vault are off until you replace it with a copy you trust.";
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

// The boot (APP.md §2.4): a first Status() that failed is one plain line —
// the code's own words — and this button; the lock scene is never drawn
// on no evidence.
export const bootCopy = {
  retry: "Retry",
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
  },
  // A Verify behind the last saves, said as what it is.
  verifiedAgo: (saves: number): string => `Last verified ${plural(saves, "save")} ago`,
  technicalDetails: "Technical details…",
};

// commandCopy is the verb table (the glossary above): a command's label
// wherever it is offered — the command bar, a context menu, a card, a
// banner — so one operation never goes by two names. "…" is there
// exactly when a dialog or a picker follows.
export const commandCopy = {
  open: "Open",
  close: "Close",
  newArchive: "New archive…",
  hide: "Hide",
  unhide: "Unhide",
  forgetArchive: "Forget archive…",
  restore: "Restore",
  deleteArchive: "Delete archive…",
  compact: "Compact…",
  rekeyArchive: "Re-key archive…",
  rekeyVault: "Re-key vault…",
  verify: "Verify",
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
  extract: "Extract…",
  extractAll: "Extract all…",
  goThere: "Go there",
  lockNow: "Lock now",
  unlock: "Unlock",
};

// chipCopy is every chip's word, in sentence case (ruled 2026-10-17: they
// were lower-case), on the Archives list and the import list alike.
export const chipCopy = {
  locked: "Locked",
  open: "Open",
  compacting: "Compacting",
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
// Asked per refused record: the final name refused on placement offers
// *Shorten*, *Rename…* and *Skip*; a refused path — the temporary or a
// folder, where no name helps — offers *Skip* and *Skip all like it*. Esc
// is *Skip*. The count is where the record stands in the list of refused
// ones, so a long list says how much is left.
export const refusedCopy = {
  nameTitle: "The destination cannot take a name this long",
  pathTitle: "The folder's path is too long for this destination",
  nameBody: (name: string): string => `${name} was not written. A shorter name may fit: Shorten halves it, keeping the extension, or give it another name for this extract only. The archive keeps the name as it is.`,
  pathBody: (name: string, isDir: boolean): string =>
    isDir
      ? `The folder ${name} and everything in it were not written: the destination cannot take a path this long, and no name would help. Extract it to a shorter path instead.`
      : `${name} was not written: the folder it goes in has a path the destination cannot take, and no name would help. Extract it to a shorter path instead.`,
  progress: (at: number, of: number): string => (of > 1 ? `${at} of ${of}` : ""),
  shorten: "Shorten",
  shortenTo: (to: string): string => `Shorten to ${to}`,
  nothingShorter: "The name is down to 1 character and cannot be shortened further.",
  rename: "Rename…",
  renameLabel: "Name for this extract",
  renameBack: "Back",
  renameGo: "Extract as this",
  skip: "Skip",
  skipAllLike: "Skip all like it",
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
  close: "Close",
  howThisWorks: "How this works",
  working: "Working…",
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
    body: "Rewrites the file to give back its unused space. The files inside do not change; a big archive takes a while.",
    primary: "Compact archive",
  },
  // Registry first, then the file (FORMAT.md R33).
  rekeyArchive: {
    title: (name: string): string => `Re-key ${name}?`,
    body: "From now on the archive opens with a new key. Its old keys stay in the vault, so copies of the file made before today still open.",
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
  passwordOff: {
    title: "Stop requiring a password with every YubiKey?",
    body: "Each YubiKey opens the vault with its PIN and touch alone; none needs to be present. Nothing else about the vault changes.",
    primary: "Turn off password",
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
  backups: "Backups",
  backupsLine: "A backup is a copy of this vault that opens with its recovery key. Keep one somewhere other than this PC.",
  lastBackup: "Last backup",
  lastBackupValue: (when: string): string => `${when}; recorded here, not proof the file is still there`,
  noBackup: "None recorded here",
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
  // The order card 2 asks them in: the PIN, then the password (APP.md
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
// The header's states (ruled 2026-10-17), then the card lines. A button
// names the next thing that happens: card 2 is *Continue*; *Start again*
// only where it does start again.
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
    busy: "Open in another Enfold",
    damaged: "Damaged",
    absent: "Not found",
    none: "No vault yet",
    broken: "Needs attention",
    setupNeeded: "Needs setting up",
  },
  firstRunLead: "Create a vault, or restore one",
  firstRunLine: "A vault is one file: the keys to your archives, opened by a YubiKey, a password or a recovery key.",
  keptIn: "Enfold keeps it in",
  restoreHint: "To restore from a backup, have its recovery key ready: a backup opens with nothing else.",
  damagedLead: "The vault could not be opened",
  absentLead: "The vault could not be found",
  damagedHint: "A backup or a copy of this vault brings your archives back. Rebuilding starts a new vault and keeps the damaged file beside it.",
  tryAgain: "Try again",
  // A dialog follows, so the ellipsis (the verb table's rule).
  createInstead: "Create a new vault instead…",
  // The replaced copy's bar, when no vault is kept: restoring it is the
  // first-run verb.
  restoreIt: "Restore it…",
  stepWaiting: "Waiting for the YubiKey",
  stepMethod: "Unlock method",
  secretHint: "What this unlock method needs.",
  tokenHint: "The YubiKey's PIN.",
  tokenPasswordHint: "The YubiKey's PIN and the vault's password.",
  setupLine: "This vault has only its recovery key so far. Choose its first unlock method; the recovery key is asked for first.",
  lastUnlocked: (when: string): string => `Last unlocked ${when}`,
  // Card 2 once the YubiKey is recognised (ruled 2026-10-17).
  matched: (vault: string): string => `This YubiKey unlocks ${vault}.`,
  touchWaits: "The YubiKey waits for a touch.",
  opening: "Opening the vault…",
  continue: "Continue",
  startAgain: "Start again",
  useRecoveryKey: "Use recovery key",
  useRecoveryInstead: "Use recovery key instead",
  recoveryOnly: "Unlock with the recovery key only",
  reopen: "Reopen the vault",
  // The recovery card's note and refusal are ceremonyCopy's, shared with
  // the panel.
  testButton: "Test",
  replaced: "Replaced. Unlock it with one of its own unlock methods.",
  restored: "Restored. Unlock it with one of its own unlock methods.",
  restoredUnopened: "Restored, but the file could not be opened afterwards. Try again.",
  createdUnopened: "Created, but the file could not be opened afterwards. Try again.",
  // Card 3's word when a ceremony ends, by kind.
  done: {
    unlock: "Unlocked",
    replaced: "Replaced",
    restored: "Restored",
    setup: "Set up",
    verify: "Opens",
  },
  // Card 1 at rest on a vault with no YubiKey among its unlock methods.
  unlockTitle: "Unlock the vault",
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
  finishBody: "The vault opens with its recovery key first; then the unlock method you choose here is added.",
  entangleTick: "Also require a password with every YubiKey in this vault",
  // The touch a cancelled ceremony left on the YubiKey (APP.md §2.2).
  pendingTouch: "The YubiKey is still waiting for the touch you cancelled. Unlock again to pick it up, or touch it or pull it out to end it.",
  // Card 1's name with no vault yet, and the Blocked card's.
  stepStart: "Start",
  stepBlocked: "Blocked",
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
  // Archives left open when the vault locked: they browse, they do not save.
  openWhileLocked: (n: number): string => `${plural(n, "archive")} still open — browsing works, saving needs the vault.`,
  // The rebuild's bar, around the damaged file's path.
  rebuildFileAt: "The file at",
  rebuildKept: "is not a readable vault. It is kept as a dated copy in Enfold's folder, never deleted: archives that only it holds can be opened only with it, if it can ever be repaired.",
  replaceKept: "A file already at the chosen place is kept as a dated copy in Enfold's folder.",
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
  // Card 2 while the password typed in the dialog answers the prompt.
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
  seq: "Last seq / hash at seq",
  storedAs: "Stored as",
  policy: "Policy",
  lastPath: "Last path",
  ciphertextHash: "Ciphertext hash",
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
  envelopeStaleWhy: "The archive's envelope still names a retired key version; a Verify or a second re-key rewrites it.",
};

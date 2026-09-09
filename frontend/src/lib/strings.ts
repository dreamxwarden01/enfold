// The one table of copy (APP.md §7): ceremony steps, error codes, and the
// rules the words must respect — retries are shown before a PIN is asked,
// and never as "0 attempts".

import { CeremonyStep, Code } from "./api";
import type { CeremonyState, CodeKey, StepKey } from "./api";

export interface StepCopy {
  title: string;
  body: string;
}

// stepCopy is what the ceremony panel says at each step; the token flow's
// three moments (insert, PIN, touch) are the ones the lock screen builds
// around.
export const stepCopy: Record<StepKey, StepCopy> = {
  [CeremonyStep.StepWaitingForKey]: { title: "Insert your YubiKey", body: "The key that unlocks this vault." },
  [CeremonyStep.StepTwoKeys]: { title: "More than one YubiKey is inserted", body: "Leave just one in to continue." },
  [CeremonyStep.StepProbing]: { title: "Reading the key", body: "Looking for a slot this vault knows." },
  [CeremonyStep.StepNoMatch]: { title: "This key is not enrolled", body: "No slot in this vault matches a key on it. Try another key, or use your recovery key." },
  [CeremonyStep.StepBusy]: { title: "The key is in use", body: "Another program holds the YubiKey. Close it, or wait, then try again." },
  [CeremonyStep.StepPassword]: { title: "Password", body: "" },
  [CeremonyStep.StepPIN]: { title: "PIN", body: "The key counts wrong tries, not Enfold." },
  [CeremonyStep.StepTouch]: { title: "Touch your YubiKey now", body: "The key will blink until you touch it." },
  [CeremonyStep.StepBlocked]: { title: "PIN is blocked. Use your recovery key.", body: "This key will not accept a PIN again until it is reset." },
  [CeremonyStep.StepDeriving]: { title: "Unlocking", body: "Deriving the session keys." },
  [CeremonyStep.StepManagementKey]: { title: "Management key", body: "This key has no PIN-protected management key. Type it as hex to generate a new key." },
  [CeremonyStep.StepRecovery]: { title: "Recovery key", body: "" },
  // The records ceremony ends here, with the incoming registry read on a
  // staged copy and nothing written to either file (APP.md §13).
  [CeremonyStep.StepRecords]: { title: "Records read", body: "Choose which to bring into this vault." },
  [CeremonyStep.StepSwapKey]: { title: "Swap keys", body: "Remove the key that unlocked, insert the key to enroll." },
  [CeremonyStep.StepReleasing]: { title: "Releasing the key", body: "The key forgets the PIN." },
  [CeremonyStep.StepDone]: { title: "Done", body: "" },
  [CeremonyStep.StepFailed]: { title: "Could not continue", body: "" },
};

// stepText is stepCopy for any step value, the generator's zero included.
export function stepText(step: CeremonyStep): StepCopy {
  return (stepCopy as Record<string, StepCopy>)[step] ?? { title: "", body: "" };
}

// codeCopy is what an error code says. Every code has an entry; the test
// checks it.
export const codeCopy: Record<CodeKey, string> = {
  [Code.CodeInternal]: "Something went wrong inside Enfold. The log in the data folder has the details.",
  [Code.CodeNoVault]: "No vault is open.",
  [Code.CodeVaultLocked]: "The vault is locked.",
  [Code.CodeVaultBroken]: "A write's outcome is unknown. Reopen the vault to continue.",
  [Code.CodeVaultBusy]: "The vault file is open in another Enfold. Close it first.",
  [Code.CodeVaultTampered]: "The vault's slot region does not verify: it does not match the registry, or it does not belong with the superblock. Import a copy of this vault from the lock screen.",
  [Code.CodeTamperedHash]: "The vault's slot region does not match its registry. Import a copy of this vault from the lock screen.",
  [Code.CodeTamperedGeneration]: "The vault's slot region does not belong with its superblock: it holds a key from another generation. Import a copy of this vault from the lock screen.",
  // Since Revision 2 no way in is left holding an old key: this is the
  // vault file's second superblock copy, damaged (FORMAT.md §5).
  [Code.CodeVaultStale]: "One of the vault file's two superblock copies is damaged. Enfold opened the good one; the next write repairs the other.",
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
  [Code.CodeNoSlot]: "No way in matches.",
  [Code.CodePasswordNeeded]: "This key also needs its password.",
  [Code.CodeInvariant]: "At least two independent ways in are always kept.",
  [Code.CodeSlotPolicy]: "A standalone password cannot share a vault with a hardware key.",
  [Code.CodeDuplicateSlot]: "This key is already enrolled.",
  [Code.CodeNoRecoverySlot]: "A vault always keeps a recovery key.",
  [Code.CodeSlotNotFound]: "That way in no longer exists.",
  [Code.CodeEscrowMissing]: "The vault keeps no copy of this recovery key. Add a new recovery key, then remove this one.",
  [Code.CodeEscrowMismatch]: "The kept copy of this recovery key does not match its way in, so it is not shown.",
  [Code.CodeRecoveryPlace]: "Not there: keep the recovery key out of Enfold's own folder and the vault's, and give it a file name of its own.",
  [Code.CodeVaultKept]: "A vault is already kept. A second one is never made from here: importing replaces it, and only a damaged one is rebuilt.",
  [Code.CodeNotThisVault]: "No key of this vault opens that file: it is another vault's, or a backup from a generation this vault no longer keeps. Its own recovery key opens it.",
  [Code.CodeConflict]: "The vault changed under Enfold. Reopen it.",
  [Code.CodeIndeterminate]: "A write's outcome is unknown. Reopen the vault.",
  [Code.CodeCeremonyRunning]: "An unlock is already in progress.",
  [Code.CodeReleasing]: "The key is being released; try again in a moment.",
  [Code.CodeNoCeremony]: "Nothing is waiting for that.",
  [Code.CodeStalePrompt]: "That prompt has passed.",
  [Code.CodeCancelled]: "Cancelled.",
  [Code.CodeTokenNoService]: "The Smart Card service is not running. Windows starts it when a YubiKey is plugged in; if it stays off, start the Smart Card service.",
  [Code.CodeTokenNoReader]: "No YubiKey is attached.",
  [Code.CodeTokenNoCard]: "The YubiKey went away. Insert it again to go on.",
  [Code.CodeTokenBusy]: "Another program holds the YubiKey.",
  [Code.CodeTokenNoPIV]: "This key has PIV disabled, or it is not a YubiKey.",
  [Code.CodeTokenUnsupport]: "This YubiKey's firmware is too old (5.3 or later is needed).",
  [Code.CodeTokenNoKey]: "This key is not enrolled in the vault.",
  [Code.CodeTokenNotUsable]: "The enrolled key's policy does not require PIN and touch, so Enfold will not use it.",
  [Code.CodeTokenPIN]: "Wrong PIN.",
  [Code.CodeTokenProof]: "The key did not prove itself: what it computed does not match its own public key. It was not enrolled.",
  [Code.CodeTokenPINBlocked]: "The PIN is blocked. Use your recovery key.",
  [Code.CodeTokenPINAgain]: "The key wants the PIN again.",
  [Code.CodeTokenTouch]: "The key was not touched in time. Try again, and touch it when it blinks.",
  [Code.CodeTokenPending]: "The key is still answering the cancelled request. Touch it, or pull it out, to end that now.",
  // The touch is kept for five minutes so that a wrong password costs no
  // second one; past that the ceremony ends where a cancel ends it and the
  // lock screen carries this line (APP.md §2.2, the ruling of 2026-09-08).
  [Code.CodePasswordDeadline]: "The password was not given within five minutes; unlock again from the key.",
  [Code.CodeTokenTooMany]: "Too many operations on one key handle. Start again.",
  [Code.CodeTokenReset]: "The key was released but may still be PIN-verified for a few seconds.",
  [Code.CodeTokenOccupied]: "That slot on the key holds something already.",
  [Code.CodeTokenFull]: "Every slot on the key is occupied. Nothing is overwritten.",
  [Code.CodeTokenNoMgmtKey]: "The key stores no PIN-protected management key.",
  [Code.CodeTokenMgmtKey]: "The management key was refused.",
  [Code.CodeTokenTwoKeys]: "Two YubiKeys are inserted. Remove one.",
  [Code.CodeArchiveNotOpen]: "The archive is not open.",
  [Code.CodeArchiveOpen]: "The archive is already open.",
  [Code.CodeArchiveDirty]: "The archive has changes not yet saved.",
  [Code.CodeArchiveBusy]: "The archive is busy with another operation.",
  [Code.CodeArchiveCompacting]: "The archive is being compacted.",
  [Code.CodeArchiveNeedsReopen]: "The archive needs to be reopened.",
  [Code.CodeArchiveKey]: "No key in the vault opens this archive.",
  [Code.CodeArchiveReadOnly]: "The archive is read-only.",
  [Code.CodeArchiveNotFound]: "The archive is not in this vault.",
  [Code.CodeArchiveInvalid]: "The file is not an archive, or it is damaged.",
  // The archive layer creates with O_EXCL: Enfold never overwrites a file
  // it did not make, whatever the save dialog's own replace prompt said
  // (APP.md §3, §6).
  [Code.CodeArchiveExists]: "A file is already there. Enfold never overwrites; choose another name.",
  [Code.CodeArchiveMissing]: "The archive file is not where the vault last saw it.",
  [Code.CodeArchiveCopyMismatch]: "This file is not the copy the vault last saved: an older backup, or one written elsewhere. Saving records this copy.",
  [Code.CodeArchiveForgotten]: "This archive was forgotten. Restore it to use it again; its key is dropped thirty days after it was forgotten.",
  [Code.CodeArchiveNotThisOne]: "The file there is not this archive — another archive, or a copy someone made. Nothing was removed.",
  [Code.CodeArchiveUnreachable]: "The folder that file is in could not be reached. Nothing was removed and nothing was forgotten.",
  [Code.CodeArchiveDeleteFailed]: "The file is this archive, and it could not be removed. The record is kept: its keys open a file that is still there.",
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
  [Code.CodeContentHash]: "The file's content does not match its record.",
  [Code.CodeNoSpace]: "Not enough space.",
  [Code.CodeDictInUse]: "The dictionary is still in use.",
  [Code.CodeOpNotFound]: "That operation is gone.",
  [Code.CodeOpCancelled]: "Cancelled.",
  [Code.CodeOpRunning]: "An operation is still writing the vault. Wait for it to finish.",
  [Code.CodeTooSlow]: "This would not finish before the session locks. Extend the session first.",
  [Code.CodeParams]: "Enfold refused the request.",
  [Code.CodeIO]: "A file could not be read or written.",
};

// Shell warnings are codes the core does not define.
const extraCodes: Record<string, string> = {
  "shell.lock_detection_unavailable": "Workstation-lock detection is unavailable; Enfold polls instead and may lock a few seconds late.",
  "vault.timeouts_clamped": "The vault's stored timeouts were outside the permitted range and were replaced by the defaults.",
  "archive.changes_discarded": "An archive's unsaved changes were discarded when its time ran out.",
  "archive.envelope_stale": "An archive's envelope was left behind by a rotation; Enfold repairs it on the next save.",
};

export function codeText(code: string | undefined): string {
  if (!code) return "";
  return (codeCopy as Record<string, string>)[code] ?? extraCodes[code] ?? `Error: ${code}`;
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

// tamperedCopy is the Tampered banner, worded from VaultStatus.TamperedReason
// (APP.md §13): the slot region does not match the registry (FORMAT.md R25),
// or it does not belong with the superblock (§6.2). With no reason to hand —
// a status from before the cause was known — both causes are named, because
// the remedy is the same and guessing one would be a claim.
export function tamperedCopy(reason?: string): string {
  const why =
    reason === Code.CodeTamperedHash
      ? "does not match its registry"
      : reason === Code.CodeTamperedGeneration
        ? "does not belong with its superblock — it holds a key from another generation"
        : "does not match its registry, or does not belong with its superblock";
  return `The vault's slot region ${why}. Every change is disabled until a copy of this vault is imported from the lock screen.`;
}

// warningCopy is what a status warning says in the banner.
export function warningCopy(code: string, reason?: string): string {
  switch (code) {
    case Code.CodeVaultTampered:
      return tamperedCopy(reason);
    case Code.CodeVaultStale:
      return "One of the vault file's two superblock copies is damaged. Enfold opened the good one and repairs the other on the next write.";
    case Code.CodeTokenReset:
      return "Your YubiKey may stay PIN-verified for a few seconds after release.";
  }
  return codeText(code);
}

// The one table of copy (APP.md §7): ceremony steps, error codes, and the
// rules the words must respect — retries are shown before a PIN is asked,
// and never as "0 attempts".

import { CeremonyStep, Code } from "./api";
import type { CeremonyState, CodeKey, StepKey } from "./api";
import { bytes, ext, plural } from "./format";

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
  // Not a state the user can be in since 2026-09-09 — an archive is clean
  // between operations — but the code stands for the archive layer's
  // "a transaction is already open on this handle", which is a bug.
  [Code.CodeArchiveDirty]: "Something went wrong inside Enfold: an operation was begun on an archive that already had one. The log in the data folder has the details.",
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
  "archive.envelope_stale": "An archive's envelope was left behind by a rotation; rotating its key again repairs it.",
};

export function codeText(code: string | undefined): string {
  if (!code) return "";
  return (codeCopy as Record<string, string>)[code] ?? extraCodes[code] ?? `Error: ${code}`;
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
  deleteTampered: "The vault's slot region does not verify; every change is disabled.",
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
// 2026-09-11 after the first real drag): one label, *Extracting 2 items*,
// from the moment the drag starts and through all three of its phases, so
// that a staging over in an instant is a change inside a strip already on
// the screen and never a strip that flashes. The hover shows the label
// alone; the staging adds the bar, which moves by bytes and offers
// *Cancel*; and when the bytes are written the bar simply stays full, with
// no *Cancel* and no words of its own — from then on Explorer's own window
// is in front and the only control there is — until DoDragDrop returns and
// the strip goes. A finished drag out that failed is named by the verb its
// toast counts with (ops.ts opLabel).
export const dragOutCopy = {
  extracting: "Extracting",
  name: "Dragging out",
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
  emptyRoot: "Nothing here yet. Add files, or drop them here.",
  emptyFolder: "This folder is empty.",
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

// The close question (APP.md §2.4, ruled 2026-09-13): the close button
// never means "to the tray" until the user has said so, so the first close
// is cancelled and this is asked — one sentence, the two answers, and the
// tick that makes the answer the setting. Esc is neither: the window
// stays. The quiet line is there because the tick is a setting and the
// user should know where to undo it.
export const closeCopy = {
  title: "Close Enfold?",
  body: "Keep it running in the tray, ready to open, or quit?",
  tray: "Keep running in the tray",
  quit: "Quit",
  remember: "Remember my choice",
  inSettings: "You can change this in Settings.",
};

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
  nothingShorter: "The name is down to one character and cannot be shortened further.",
  rename: "Rename…",
  renameLabel: "Name for this extract",
  renameBack: "Back",
  renameGo: "Extract as this",
  skip: "Skip",
  skipAllLike: "Skip all like it",
};

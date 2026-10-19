// The Vault page's "Archive files:" line (direction 10, phase 4a; APP.md
// §6, the Vault page › Backups and copies): what can be said of the
// archives' files from what the page holds, and nothing more.
//
// Three facts bound it. The core lists the registry only while the vault
// is Unlocked — while Locked its list is the open archives alone, with no
// notes — so a count read then is a count of nothing (APP.md §13). A
// record carries archive.file_missing only once a presence pass has
// measured it absent, and a record no pass measured carries no note at
// all, so an empty count of notes is never by itself "all present": that
// is said only on the word of a *Find missing files* pass of this unlock
// that measured something. And hidden records are records too, so the
// count is over the whole registry, hidden among it, forgotten ones
// apart (their files are no longer the vault's concern).
import { vaultCopy } from "./strings";

export interface FilesFacts {
  // The vault is Unlocked, so the list below is the registry's.
  unlocked: boolean;
  // The registry's records, hidden ones among them; null until read.
  records: readonly { note?: string; forgottenAt?: number }[] | null;
  // The last Find missing files' answer this unlock (store.filesCheck).
  check: { probed: number; missing: number } | null;
}

export interface FilesLine {
  text: string;
  // Some file is missing: the line is said in the warning's ink.
  owed: boolean;
}

export const FILE_MISSING = "archive.file_missing";

export function filesLine(f: FilesFacts): FilesLine {
  if (!f.unlocked) return { text: vaultCopy.filesLocked, owed: false };
  if (!f.records) return { text: vaultCopy.filesUnread, owed: false };
  const n = f.records.filter((r) => r.note === FILE_MISSING && !r.forgottenAt).length;
  if (n > 0) return { text: vaultCopy.filesMissing(n), owed: true };
  if (!f.check) return { text: vaultCopy.filesUnread, owed: false };
  // A pass that measured nothing (every path removable, network or UNC,
  // or none at all) establishes nothing (lib/foot.ts checkedLine).
  if (f.check.probed <= 0) return { text: vaultCopy.filesUnmeasured, owed: false };
  // A pass that left some records unmeasured speaks only for the ones it
  // measured: "all present" would claim the others too.
  const live = f.records.filter((r) => !r.forgottenAt).length;
  if (f.check.probed < live) return { text: vaultCopy.filesNoneOf(f.check.probed), owed: false };
  return { text: vaultCopy.filesPresent, owed: false };
}

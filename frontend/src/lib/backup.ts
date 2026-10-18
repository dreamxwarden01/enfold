// The backups card's status line (direction 9 of the design review, ruled
// 2026-10-18; APP.md §6, The backups card): the last backup said against what the vault
// holds now, so the card is a receipt and not only a date.
//
// The comparison is Vault.BackupStatus's: the archives created after the
// last backup and those it holds that were re-keyed after it (each counted
// once, however many re-keys it had) — never the vault's
// last change, which every save moves and which would nag forever. Its
// stamp is lastExportAt, local and unauthenticated, so the line words and
// suggests; it gates nothing (the export stays where it was).
//
// - none: nothing to say — no archives, and no backup either.
// - never: no backup recorded here, with archives the vault alone holds.
// - behind: a backup, lacking archives created or re-keyed since.
// - current: a backup holding every archive key.
//
// The counts are read only from an unlocked registry that is not Tampered
// (the core answers 0 otherwise), so `known` says whether they can be
// believed; without them the line says the date alone, or that no backup
// is recorded, and claims nothing about what a backup holds — so it is
// the plain line with the quiet icon, never the attention bar, and the
// export is not suggested: whether anything is owed was not counted.
//
// The caveat ("Recorded here, not proof the file is still there.") is
// about a recorded backup, so it stands only under a date (at > 0).
import type { BackupStatus } from "./api";
import { vaultCopy } from "./strings";

export type BackupKind = "none" | "never" | "behind" | "current";

export function backupKind(b: BackupStatus | null, known: boolean): BackupKind {
  const at = b?.at ?? 0;
  if (!known) return at > 0 ? "current" : "never";
  if (at <= 0) return (b?.archives ?? 0) > 0 ? "never" : "none";
  return (b?.missing ?? 0) > 0 || (b?.rekeyed ?? 0) > 0 ? "behind" : "current";
}

// The line as drawn: `tone` attention is a bar with the backup icon, plain
// is a line with no box and the check in the accent's ink; `head` is the
// first sentence, set strong in a bar; `caveat` the caption under it.
export interface BackupLine {
  kind: BackupKind;
  tone: "attention" | "plain" | null;
  head: string;
  rest: string;
  caveat: string;
  // Export backup… is the accent while a backup is owed.
  suggestExport: boolean;
  // The plain line's check, in the accent's ink, is the receipt that the
  // backup holds every archive key: drawn only when that was counted.
  // The date alone (Locked, Tampered) wears the quiet backup icon.
  checked: boolean;
}

export function backupLine(b: BackupStatus | null, known: boolean, when: string): BackupLine {
  const kind = backupKind(b, known);
  const at = b?.at ?? 0;
  const line: BackupLine = { kind, tone: null, head: "", rest: "", caveat: at > 0 ? vaultCopy.backupCaveat : "", suggestExport: known && (kind === "never" || kind === "behind"), checked: false };
  switch (kind) {
    case "never":
      // While Locked or Tampered nothing is counted: no claim about what
      // the vault alone holds, so no bar either — the quiet line.
      line.tone = known ? "attention" : "plain";
      line.head = vaultCopy.backupNeverHead;
      line.rest = known ? vaultCopy.backupNever(b?.archives ?? 0) : "";
      break;
    case "behind":
      line.tone = "attention";
      line.head = vaultCopy.backedUp(when);
      line.rest = vaultCopy.backupLacks(b?.missing ?? 0, b?.rekeyed ?? 0);
      break;
    case "current":
      line.tone = "plain";
      line.head = vaultCopy.backedUp(when);
      line.rest = known ? vaultCopy.backupHoldsAll : "";
      line.checked = known;
      break;
  }
  return line;
}

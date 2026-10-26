// The archive's status strip (APP.md §3, §6): what the open archive holds,
// how big its file is, when it was last saved, and — when it is worth
// knowing — how much of the file is unused space inside it. The key
// version is not on it (ruled 2026-10-17): it is a format word, and
// the Details dialog carries it (APP.md §7). Nothing here reads the
// store, so the whole line can be tested.
import type { ArchiveStat } from "./api";
import { bytes, dateTime, plural } from "./format";

// The free space is shown from 64 MiB up. The figure is the archive's
// published free map — what a whole-file *Compact* would give back — and
// the floor is that of the rule the core reclaims on (APP.md §2.3, FORMAT.md
// R40): a run of moves happens when it would give the file system back
// 64 MiB or more of the *tail*, and that is at least a quarter of what it
// would have to move — bytes returned, never the size of any hole, since a
// hole filled behind a file that cannot move returns nothing. So a figure
// at or over the floor on the strip is space the core could not or would
// not move for — a file larger than every hole before it, a gain under a
// quarter of the move, an extent a reader holds — and it is the only thing
// that says why the file is bigger than the files inside it, and what
// *Compact* on the Archives page is for. Below 64 MiB there is nothing
// worth a reader's attention and the line stays short.
export const freeSpaceFloor = 64 * 1024 * 1024;

export function freeWorthShowing(freeSpace: number | undefined): boolean {
  const n = freeSpace ?? 0;
  return Number.isFinite(n) && n >= freeSpaceFloor;
}

// statusNote is the whole strip: "12,406 files · 48.1 GB · last saved
// 2026-09-05 21:14 · 2.9 GB unused". The figure is space inside the
// archive's file, never free space on the disk, so it is not called
// "free", which reads as the disk's (ruled 2026-10-14, the polish review's
// small fix 17). Nothing is appended to it any more — an open archive has
// no timeout of its own, locked vault or not, so there is no deadline to
// count down (APP.md §2.3, ruled 2026-09-10).
// A count of one is singular — "1 file" (ruled 2026-09-09).
export function statusNote(stat: ArchiveStat | null | undefined): string {
  if (!stat) return "";
  const parts = [plural(stat.files, "file"), bytes(stat.size)];
  if (stat.lastSavedAt) parts.push(`last saved ${dateTime(stat.lastSavedAt)}`);
  if (freeWorthShowing(stat.freeSpace)) parts.push(`${bytes(stat.freeSpace)} unused`);
  return parts.join(" · ");
}

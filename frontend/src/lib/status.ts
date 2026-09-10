// The archive's status strip (APP.md §3, §6): what the open archive holds,
// how big its file is, which key version writes it, when it was last
// saved, and — when it is worth knowing — how much of the file is free
// space. Nothing here reads the store, so the whole line can be tested.
import type { ArchiveStat } from "./api";
import { bytes, dateTime, plural } from "./format";

// The free space is shown from 64 MiB up, which is the floor of the rule
// the core reclaims on (APP.md §2.3: free space at or above 64 MiB *and* a
// quarter of the file). Below the quarter the core leaves the holes where
// they are, and the figure is then the only thing that says why the file
// is bigger than the files inside it; below 64 MiB there is nothing worth
// a reader's attention and the line stays short.
export const freeSpaceFloor = 64 * 1024 * 1024;

export function freeWorthShowing(freeSpace: number | undefined): boolean {
  const n = freeSpace ?? 0;
  return Number.isFinite(n) && n >= freeSpaceFloor;
}

// statusNote is the whole strip: "12,406 files · 48.1 GB · key v1 · last
// saved 2026-09-05 21:14 · 2.9 GB free". Nothing is appended to it any
// more — an open archive has no timeout of its own, locked vault or not,
// so there is no deadline to count down (APP.md §2.3, ruled 2026-09-10).
// A count of one is singular — "1 file" (ruled 2026-09-09).
export function statusNote(stat: ArchiveStat | null | undefined): string {
  if (!stat) return "";
  const parts = [plural(stat.files, "file"), bytes(stat.size), `key v${stat.keyVersion}`];
  if (stat.lastSavedAt) parts.push(`last saved ${dateTime(stat.lastSavedAt)}`);
  if (freeWorthShowing(stat.freeSpace)) parts.push(`${bytes(stat.freeSpace)} free`);
  return parts.join(" · ");
}

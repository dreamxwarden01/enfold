// A forgotten record's retention (FORMAT.md §18.2, APP.md §13): the
// record keeps its keys for thirty days after it was forgotten, and the
// first unlock whose own registry write is past that drops it. The page
// only says when — the arithmetic that decides belongs to the writer.
export const RETENTION_SECONDS = 2_592_000; // thirty days

// purgeAfter is the Unix second past which the first unlock drops the
// record's key: 0 when the record is not forgotten, and 0 for a stamp
// that is not a usable time (absent, negative), which the page shows as
// nothing rather than as a date in 1970.
export function purgeAfter(forgottenAt: number): number {
  if (!Number.isFinite(forgottenAt) || forgottenAt <= 0) return 0;
  return forgottenAt + RETENTION_SECONDS;
}

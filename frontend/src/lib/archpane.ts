// The Archive page's pane describes where you are (direction 3; APP.md
// §6, the Archive page, phase 4a). At rest — nothing selected — it names
// the folder shown and counts it, then gives the archive's facts and a
// standard *Extract…* on everything; with several selected it counts the
// selection and offers its accent *Extract 4 items…*; with one it is the
// preview and the item's facts. What it counts is here, apart from the
// DOM, so the figures are tested.
import type { FileRow } from "./api";

// Figures: how many rows, and their plaintext bytes — null when not every
// row is on hand to be added up, since a long folder pages in as it
// scrolls (APP.md §3) and a sum of the rows loaded would be a smaller
// figure said as the folder's. A folder row's size is the sum beneath it,
// as its preview says.
export interface Figures {
  count: number;
  bytes: number | null;
}

const sizeOf = (r: Pick<FileRow, "size">): number => (Number.isFinite(r.size) && r.size > 0 ? r.size : 0);

// folderFigures: the folder shown — its Total, and its bytes once every
// row of it is loaded.
export function folderFigures(page: { rows: readonly Pick<FileRow, "size">[] | null; total: number } | null | undefined): Figures {
  if (!page) return { count: 0, bytes: null };
  const rows = page.rows ?? [];
  const count = Math.max(page.total, rows.length);
  if (rows.length < count) return { count, bytes: null };
  return { count, bytes: rows.reduce((n, r) => n + sizeOf(r), 0) };
}

// selectionFigures: the selection — its size, and its bytes when every
// selected id is a row on hand (the header's tick and Ctrl+A select ids
// the list may never have loaded).
export function selectionFigures(rows: readonly Pick<FileRow, "id" | "size">[], ids: ReadonlySet<string>): Figures {
  const count = ids.size;
  let bytes = 0;
  let found = 0;
  for (const r of rows) {
    if (!ids.has(r.id)) continue;
    bytes += sizeOf(r);
    found++;
  }
  return { count, bytes: found === count ? bytes : null };
}

// The pane's three shapes: at rest, several selected, one selected.
export type PaneShape = "rest" | "several" | "one";

export function paneShape(selected: number, oneOnHand: boolean): PaneShape {
  if (selected === 0) return "rest";
  if (selected === 1 && oneOnHand) return "one";
  // One selected that is not on hand — selected by the header's tick in a
  // folder of one, before its row is loaded — is counted like several.
  return "several";
}

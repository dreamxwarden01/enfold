// The list's sort (APP.md §3 Page, §6, ruled 2026-09-10). The order is the
// core's, so that every page of a long directory comes in one order: the
// page keeps a column and a direction, and the Name column's own direction
// as the tie-break while another column sorts, and sends the two as the
// sort string Page and Children take — one or two signed keys, comma-
// separated. Everything about that string is here, so it can be tested
// without a DOM.

export type SortKey = "name" | "size" | "type" | "modified";

export const SORT_KEYS: SortKey[] = ["name", "size", "type", "modified"];

export interface SortState {
  // the column the list is ordered by
  key: SortKey;
  // that column's direction
  desc: boolean;
  // the Name column's own direction — the tie-break while another column
  // sorts, and what Name comes back to when it is clicked again
  nameDesc: boolean;
}

// An archive opens on name ascending (APP.md §3), and the choice lasts
// while the archive is open.
export const DEFAULT_SORT: SortState = { key: "name", desc: false, nameDesc: false };

// The first click's direction (APP.md §6): Name and Type ascending, Size
// ascending, Modified descending — newest first, which is what a click on
// a date column asks for.
export function firstDirection(key: SortKey): boolean {
  return key === "modified";
}

// clickHeader is one click on a column's header cell: the first click on a
// column takes its own first direction, a click on the column already
// sorting turns it the other way. Name's direction is the tie-break's, so
// a click on Name moves both together; a click elsewhere leaves Name's
// where it stood.
export function clickHeader(s: SortState, key: SortKey): SortState {
  if (key === "name") {
    const desc = s.key === "name" ? !s.desc : s.nameDesc;
    return { key, desc, nameDesc: desc };
  }
  const desc = s.key === key ? !s.desc : firstDirection(key);
  return { key, desc, nameDesc: s.nameDesc };
}

// sortString is what Page and Children are sent: the column with a leading
// "-" for descending, then the tie-break's direction where it is another
// column's. A sort by name is one key — a second `name` would have to
// agree with the first and says nothing more.
export function sortString(s: SortState): string {
  const signed = (k: string, d: boolean) => (d ? `-${k}` : k);
  if (s.key === "name") return signed("name", s.desc);
  return `${signed(s.key, s.desc)},${signed("name", s.nameDesc)}`;
}

// arrowOf is the arrow a header cell wears: the sorting column's direction,
// nothing on the others.
export function arrowOf(s: SortState, key: SortKey): "asc" | "desc" | null {
  if (s.key !== key) return null;
  return s.desc ? "desc" : "asc";
}

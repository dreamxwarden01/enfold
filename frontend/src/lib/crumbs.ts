// The Archive page's head is its path (direction 3; APP.md §6, the Archive
// page; ruled 2026-10-14 and 2026-10-18): "Photos 2024 › 2024 › Trips",
// the archive's name the root, the folder shown the last crumb. The
// leading *Archives* crumb is gone — the rail carries the list and the
// open archive now.
//
// How a long path is drawn, the orchestrator's fold rule closing what the
// user's rulings left open:
//
//   - each crumb is cut at its own cap with an end ellipsis and the app's
//     tooltip — 240 px for a folder, 320 for the root;
//   - when the capped crumbs do not fit the head, the middle folds: the
//     root and the current folder always show, and the parents are added
//     back from the current one outward while they fit, so what folds is
//     always the run of folders just under the root;
//   - what folded is one ellipsis button between the root and the first
//     crumb shown, whose menu lists those folders in path order, each cut
//     to the folder's cap with its whole name in the tooltip, the menu
//     capped in height with a scrollbar;
//   - the head is measured again once per resize.
//
// Nothing here touches the DOM: the page measures each crumb as drawn and
// hands the widths in, so the rule is tested without a browser.

// A folder's crumb and the root's, at most (the end ellipsis takes over).
export const CRUMB_CAP = 240;
export const ROOT_CAP = 320;

// What stands between two crumbs, and the ellipsis button, as drawn: the
// page measures both, these are what it falls back on.
export interface Gaps {
  sep: number;
  more: number;
}
export const DEFAULT_GAPS: Gaps = { sep: 20, more: 28 };

// Fold says which crumbs show and which folded, by index into the path
// (0 the root, the last the folder shown), each in path order.
export interface Fold {
  shown: number[];
  folded: number[];
}

const range = (from: number, to: number): number[] => Array.from({ length: Math.max(0, to - from) }, (_, i) => from + i);

// fold takes the path's names, each crumb's width as drawn (already
// capped), and the width the head gives the crumbs.
export function fold(names: readonly string[], widths: readonly number[], available: number, gaps: Gaps = DEFAULT_GAPS): Fold {
  const n = names.length;
  if (n === 0) return { shown: [], folded: [] };
  const w = (i: number): number => {
    const v = widths[i];
    return Number.isFinite(v) && v > 0 ? v : 0;
  };
  // Two crumbs or fewer have no middle: the root and the folder shown
  // never fold, whatever the room.
  if (n <= 2) return { shown: range(0, n), folded: [] };
  let whole = (n - 1) * gaps.sep;
  for (let i = 0; i < n; i++) whole += w(i);
  if (whole <= available) return { shown: range(0, n), folded: [] };
  // Folding: the root, its separator, the ellipsis and its separator, and
  // the folder shown; then the parents from the folder shown outward,
  // each with the separator before it, until one does not fit. The first
  // that does not fit stops the walk, so what folds is one run.
  let used = w(0) + gaps.sep + gaps.more + gaps.sep + w(n - 1);
  let first = n - 1;
  for (let i = n - 2; i >= 1; i--) {
    const need = w(i) + gaps.sep;
    if (used + need > available) break;
    used += need;
    first = i;
  }
  // Every parent fitted beside the ellipsis — which takes more room than
  // nothing — so the whole path fits after all; not reachable from the
  // check above, kept so the answer is always a fold of something.
  if (first === 1) return { shown: range(0, n), folded: [] };
  return { shown: [0, ...range(first, n)], folded: range(1, first) };
}

// The ellipsis menu's height (the user's ruling of 2026-10-14: capped,
// with a scrollbar and a few rows of slack). A list of up to MENU_ROWS +
// MENU_SLACK folders shows whole — a scrollbar for one or two rows more
// than the cap would hide what a glance takes in; a longer list shows
// MENU_ROWS and half a row, the half row saying there is more to scroll.
export const MENU_ROWS = 8;
export const MENU_SLACK = 2;

export function menuRows(count: number): number {
  if (!Number.isFinite(count) || count <= 0) return 0;
  return count <= MENU_ROWS + MENU_SLACK ? count : MENU_ROWS + 0.5;
}

// menuCapped: whether the menu scrolls at all.
export function menuCapped(count: number): boolean {
  return menuRows(count) < count;
}

// capOf: the width a crumb is cut at.
export function capOf(index: number): number {
  return index === 0 ? ROOT_CAP : CRUMB_CAP;
}

// clampFolded: the count of folded crumbs a fold measured, held to the
// path now drawn — at most every crumb between the root and the folder
// shown (n - 2), none for two or fewer. The fold is measured a frame after
// a new path is drawn, and until then the count is the old path's: from
// seven deep to one deep it would have put the folder shown in the
// ellipsis menu for that frame (the review of phase 4a, ruled 2026-10-19).
export function clampFolded(count: number, n: number): number {
  if (!Number.isFinite(count) || count <= 0) return 0;
  return Math.min(Math.floor(count), Math.max(0, n - 2));
}

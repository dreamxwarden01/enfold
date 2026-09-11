// The Archives list's ticks (APP.md §6, ruled 2026-09-10): the same rules
// as the file list's (lib/selection.ts) over the rows the filter shows,
// and the one rule of its own — which row the details pane shows. The
// list is whole and filtered on the page, so "every row" is the shown
// order and nothing is asked of the core.
import type { Selection } from "./selection";
import { emptySelection, selectAll } from "./selection";

// shownTicked: the header's checkbox is ticked exactly when every row the
// filter shows is, never a third state, and never for an empty list. A
// tick on a row the filter hides stays a tick and does not count.
export function shownTicked(s: Selection, shown: readonly string[]): boolean {
  return shown.length > 0 && shown.every((id) => s.ids.has(id));
}

// clickShownHeader: ticked, it clears; otherwise it ticks the rows the
// filter shows — those and no other, so the ticks are what is on screen.
export function clickShownHeader(s: Selection, shown: readonly string[]): Selection {
  return shownTicked(s, shown) ? emptySelection() : selectAll(s, shown);
}

// paneRow is the archive the details pane shows: the row last clicked
// while it is ticked, else the sole ticked row, else none — the pane and
// the ticks then never name different archives, and with several ticked
// and the last click gone the pane shows a count instead.
export function paneRow(s: Selection, lastClicked: string | null): string | null {
  if (lastClicked !== null && s.ids.has(lastClicked)) return lastClicked;
  if (s.ids.size === 1) return s.ids.values().next().value ?? null;
  return null;
}

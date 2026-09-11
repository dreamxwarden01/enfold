// The list's selection (APP.md §6, ruled 2026-09-10): the ticks are the
// selection and the selection is the ticks. A plain click selects one row
// and makes it the anchor; Ctrl+click toggles one; Shift+click extends
// from the anchor — the last row clicked without Shift — in the current
// row order, and with no anchor selects the clicked row alone; a row's
// checkbox toggles that row alone and makes it the anchor; the header's
// checkbox and Ctrl+A take the whole folder. A blank click, entering a
// folder or going up clears both the selection and the anchor — the
// stale-anchor fault the outside recon found (finding 4): a Shift+click
// after a blank click ranged from the row that click had deselected. The
// anchor lives only while something is selected: a toggle that empties
// the selection — Ctrl+click or the checkbox on the last ticked row —
// drops it too, since with nothing selected there is no anchor and the
// next Shift+click selects the clicked row alone (the review's finding 1:
// the toggles kept the row just unticked as the anchor, so a Shift+click
// ranged from a row the user had explicitly deselected).
// Everything here is a rule over ids and an order, so it is here and
// tested without a DOM; the state is immutable and every step returns a
// new one, which is what a $state field wants.

export interface Selection {
  ids: ReadonlySet<string>;
  anchor: string | null;
}

export function emptySelection(): Selection {
  return { ids: new Set(), anchor: null };
}

export interface Modifiers {
  ctrl?: boolean;
  shift?: boolean;
}

// clickRow is a click on a row's body. order is the rows as they stand on
// screen, top to bottom, the `..` row not among them: it is never selected
// and never an anchor.
export function clickRow(s: Selection, order: readonly string[], id: string, m: Modifiers = {}): Selection {
  if (m.ctrl) {
    // Ctrl takes precedence over Shift, as it does in Explorer: one row
    // toggled, and it is the last row clicked without Shift.
    return anchored(toggled(s.ids, id), id);
  }
  if (m.shift) {
    const a = s.anchor === null ? -1 : order.indexOf(s.anchor);
    const b = order.indexOf(id);
    if (a >= 0 && b >= 0) {
      return { ids: new Set(order.slice(Math.min(a, b), Math.max(a, b) + 1)), anchor: s.anchor };
    }
    // No anchor, or one that is no longer on screen: the clicked row
    // alone, and it becomes the anchor.
    return { ids: new Set([id]), anchor: id };
  }
  return { ids: new Set([id]), anchor: id };
}

// toggleRow is a click on a row's checkbox: that row alone, and it becomes
// the anchor. The row click's own rules do not run.
export function toggleRow(s: Selection, id: string): Selection {
  return anchored(toggled(s.ids, id), id);
}

// anchored: the toggled row is the anchor while anything is selected; an
// empty selection has none.
function anchored(ids: Set<string>, id: string): Selection {
  return { ids, anchor: ids.size === 0 ? null : id };
}

function toggled(ids: ReadonlySet<string>, id: string): Set<string> {
  const out = new Set(ids);
  if (out.has(id)) out.delete(id);
  else out.add(id);
  return out;
}

// selectAll is the header's tick and Ctrl+A: every id of the folder —
// what Children answers, loaded or not. The anchor stays where it was.
export function selectAll(s: Selection, all: readonly string[]): Selection {
  return { ids: new Set(all), anchor: s.anchor };
}

// headerTicked: the header's checkbox shows ticked exactly when the
// selection holds every one of the folder's Total ids, never a third
// state, and never for an empty folder. The selection only ever holds
// ids of the folder shown (survive prunes it against the folder after
// every restart), so its size against Total is the whole test.
export function headerTicked(s: Selection, total: number): boolean {
  return total > 0 && s.ids.size >= total;
}

// clickHeader is a click on the header's checkbox: ticked, it clears;
// otherwise it ticks the whole folder.
export function clickHeader(s: Selection, total: number, all: readonly string[]): Selection {
  return headerTicked(s, total) ? emptySelection() : selectAll(s, all);
}

// survive is what a newer listing does to the selection (APP.md §2.4): the
// ids that still exist stay selected, the anchor keeps its row if it still
// exists, and rows a commit added are not selected.
export function survive(s: Selection, live: ReadonlySet<string>): Selection {
  const ids = new Set<string>();
  for (const id of s.ids) if (live.has(id)) ids.add(id);
  const anchor = s.anchor !== null && live.has(s.anchor) ? s.anchor : null;
  if (ids.size === s.ids.size && anchor === s.anchor) return s;
  return { ids, anchor };
}

// The Archives page's bottom panel (APP.md §6, ruled 2026-09-11, refined
// 2026-09-12): below the fixed threshold the details are not a column
// beside the list nor a block stacked under it but one element at the
// foot of the page — its header the whole of it when collapsed, and the
// panel growing upwards over the list when it is opened. Which width that
// happens at is the stylesheet's (a container query) and nothing here
// knows it: this is the panel's own state, the rules that move it, and
// the arithmetic of the movement over numbers the page measures.
//
// The state is two facts: whether the panel is up, and which archive it
// names — the page's own pane row (lib/archlist.ts), handed here so the
// two can never name different archives.
export interface BarState {
  readonly open: boolean;
  readonly id: string | null;
}

// Collapsed with nothing selected: the eyebrow dim, no name, the chevron
// dim with it.
export const CLOSED: BarState = { open: false, id: null };

// canExpand is the header's liveness: there is nothing to open until a
// row is selected, and a click on it then does nothing.
export function canExpand(s: BarState): boolean {
  return s.id !== null;
}

// picked: the page's selected row changed. The collapsed header's height
// never changes for it — the name appears and the header comes alive —
// and a row clicked while the panel is up keeps it up and shows that row.
// Nothing selected takes the panel down with it: the header that would
// collapse it is dead in that state.
export function picked(s: BarState, id: string | null): BarState {
  if (id === s.id) return s;
  if (id === null) return CLOSED;
  return { open: s.open, id };
}

export function expand(s: BarState): BarState {
  if (s.open || s.id === null) return s;
  return { open: true, id: s.id };
}

export function collapse(s: BarState): BarState {
  return s.open ? { open: false, id: s.id } : s;
}

// The header is one control: a click opens the panel while it is down and
// closes it while it is up.
export function toggle(s: BarState): BarState {
  return s.open ? collapse(s) : expand(s);
}

// Esc collapses, and is the page's to swallow only when it did something:
// the same state back means the key was not the panel's.
export function escape(s: BarState): BarState {
  return collapse(s);
}

// reveals: the moments the panel pushes a row up ahead of its top edge —
// the opening itself, and a different row clicked while it is up (that
// row is under the panel otherwise). A collapse never pushes; neither
// does a click while the panel is down.
export function reveals(before: BarState, after: BarState): boolean {
  return after.open && after.id !== null && (!before.open || before.id !== after.id);
}

// ---- the movement's arithmetic -------------------------------------------
// The panel's top edge pushes the selected row up ahead of it, the list
// scrolling in step, and the row rides just above the edge until it sits
// in the list's first slot — the one under the table's own header row —
// where the panel stops at its foot. Every row reaches that slot, the
// last one included: the push carries the list past its natural end over
// a spacer the page adds under the rows (overScroll), and the collapse's
// spring takes it back down again (APP.md §6). The page measures, all in
// one read before it writes anything; the arithmetic is here.
//
// Every length is in CSS pixels and every y is the page's, so a number
// read off a rect and a number read off a scroller can be added.
export interface ListMetrics {
  // the page y of the scroller's content top — its border inside
  readonly listTop: number;
  // the scroller's own height: what it shows, never what it holds
  readonly viewport: number;
  // what it holds: the table's own height plus the tail. Never
  // scrollHeight, which is the viewport when the content is shorter and
  // would say a short list has no room to give.
  readonly content: number;
  // the table's header row, which the first slot sits under
  readonly headHeight: number;
  // the selected row within that content, the header row included
  readonly rowTop: number;
  readonly rowHeight: number;
  // the page y of the card's foot, which every height is measured up from
  readonly footY: number;
  // the stylesheet's two heights: the collapsed line and the open header
  readonly line: number;
  readonly head: number;
  // where the list stands at the moment of the reading
  readonly scroll: number;
}

// openHeight: the first slot's foot to the card's own — the height the
// panel opens to, whichever row it is, since every row ends in that slot.
// The floor is the open header, so a header can never be clipped; it
// should never bind.
export function openHeight(m: ListMetrics): number {
  return Math.max(m.head, Math.round(m.footY - (m.listTop + m.headHeight + m.rowHeight)));
}

// pushScroll: where the list ends up — the row in the first slot, always,
// whatever the list's own end says.
export function pushScroll(m: ListMetrics): number {
  return Math.max(0, m.rowTop - m.headHeight);
}

// naturalEnd: how far the list scrolls on its own, with no spacer under
// it. Nothing at all for a list shorter than its viewport.
export function naturalEnd(m: ListMetrics): number {
  return Math.max(0, m.content - m.viewport);
}

// overScroll: how far past its own end the push carries the list, and so
// how much room the spacer under the rows must give it. A short list has
// no end to speak of — content less than viewport is a negative, and the
// spacer covers that too, or the last row of five could never rise.
export function overScroll(m: ListMetrics): number {
  return Math.max(0, pushScroll(m) - (m.content - m.viewport));
}

// edgeY: the panel's top edge, in page y, for a card of that height.
export function edgeY(m: ListMetrics, height: number): number {
  return m.footY - height;
}

// rideScroll: the scroll while the row rides the rising edge. The row
// stays where it is — the list at `from` — until the edge reaches its
// foot; from there the list scrolls exactly as far as the edge has come,
// and never past the first slot.
export function rideScroll(m: ListMetrics, from: number, edge: number): number {
  return Math.min(pushScroll(m), Math.max(from, m.listTop + m.rowTop + m.rowHeight - edge));
}

// springScroll: the collapse's mirror. The edge goes back down and the
// row rides it until the list stands where it stood before the opening,
// and no further than the list's own end, which is all the room the
// spacer's removal leaves. There is no first-slot cap here: a list the
// user had scrolled past the row before the opening goes back there.
export function springScroll(m: ListMetrics, before: number, edge: number): number {
  const home = Math.max(0, Math.min(before, naturalEnd(m)));
  return Math.max(home, m.listTop + m.rowTop + m.rowHeight - edge);
}

// inContact: the row is riding the edge, so the collapse is the spring.
// A list the user scrolled by hand while the panel was up is not in
// contact and eases from where it is instead (APP.md §6).
export function inContact(m: ListMetrics): boolean {
  return Math.abs(m.scroll - pushScroll(m)) <= 1;
}

// rideReachesHome: whether the ride can carry the row all the way home.
// The row leaves the edge where its foot meets the collapsed card's, and
// a row that moved far down the list while the panel was up — a sort
// under an open panel — has its foot below that edge even with the list
// at home, so the ride would stop short of where the list stood. Such a
// row eases from where it is to home instead (APP.md §6), which is what
// a list scrolled by hand does.
export function rideReachesHome(m: ListMetrics, before: number): boolean {
  const home = Math.max(0, Math.min(before, naturalEnd(m)));
  return m.listTop + m.rowTop + m.rowHeight - edgeY(m, m.line) <= home;
}

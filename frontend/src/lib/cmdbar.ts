// The Archives page's command bar folds (direction 6, phase 4a; APP.md §6,
// the Archives page): one 40 px line that never wraps. The verbs that
// take the selected archive — Refresh fingerprint, Compact…, Re-key
// archive… — stand in the bar while there is room for them and fold into
// More from the right when there is not; More shows the folded ones at
// its top, in the bar's own order. Nothing else folds: Open, New archive…
// and Open file… lead the bar, and Filter and View hold its right end.
//
// The page measures (a ResizeObserver on the bar, once per resize); the
// rule is here, over the numbers it hands in, so it is tested without a
// DOM.

// fold: how many of the foldable verbs, from the left, the bar keeps.
// `widths` are the verbs' own widths in the bar's order, each with the
// gap before it — the first one also carries the separator that stands
// before the group, which goes with it. `available` is the room the bar
// has left once everything that never folds is laid out. A verb is kept
// only if every verb before it is: what folds is always a run at the
// right. An unmeasured width (zero, negative or not a number — the bar
// before its first layout) is taken as fitting, so the bar starts whole
// and the first measurement folds it, rather than starting folded and
// flashing the verbs in.
export function fold(widths: readonly number[], available: number): number {
  if (!Number.isFinite(available)) return widths.length;
  let used = 0;
  let kept = 0;
  for (const w of widths) {
    const need = Number.isFinite(w) && w > 0 ? w : 0;
    if (used + need > available) break;
    used += need;
    kept++;
  }
  return kept;
}

// folded: the verbs More shows at its top — those `kept` left out, in the
// bar's order.
export function folded<T>(verbs: readonly T[], kept: number): T[] {
  const k = Math.max(0, Math.min(verbs.length, Math.floor(kept)));
  return verbs.slice(k);
}

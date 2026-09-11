// The list's paging (APP.md §2.4, §3, ruled 2026-09-10). A folder pages
// in as the list scrolls, up to 1 000 rows a call — the core's ceiling,
// and what the page asks for since the outside recon found it asking for
// 2 000, being answered 200 and stopping there (finding 16). The pages
// held are one listing at one Seq; a reply is applied by the rule of §2.4:
// it answers the newest request (a request token tells an older request's
// reply from a later one's at the same Seq), it is not older than the
// pages held, and a reply newer than them restarts the listing from
// offset zero, since rows may have moved between offsets. What the rule
// decides is here, over plain values, so it can be tested without a core.
import type { Crumb, FileRow, Page } from "./api";

export const PAGE_LIMIT = 1000;

// Listing is the pages held: every row from offset zero to what has
// arrived, the folder's Total, its crumbs and the Seq they came at.
export interface Listing {
  seq: number;
  rows: FileRow[];
  total: number;
  crumbs: Crumb[];
}

export type Applied =
  // the reply's rows are now the listing from `offset`
  | { kind: "applied"; listing: Listing }
  // the reply is newer than the pages held and did not start at zero:
  // the listing must be asked for again from offset zero
  | { kind: "restart" }
  // the reply is older than the pages held: dropped
  | { kind: "stale" };

// applyReply folds one Page reply into the listing held. held is null
// when the folder has nothing yet — the first page of a folder just
// entered — and a reply then simply becomes the listing, whatever its
// offset was asked at; a folder is always first asked at zero.
export function applyReply(held: Listing | null, reply: Page, offset: number, limit = PAGE_LIMIT): Applied {
  const rows = reply.rows ?? [];
  const crumbs = reply.crumbs ?? [];
  if (held === null || (reply.seq > held.seq && offset === 0)) {
    return { kind: "applied", listing: { seq: reply.seq, rows: rows.slice(), total: reply.total, crumbs } };
  }
  if (reply.seq > held.seq) return { kind: "restart" };
  if (reply.seq < held.seq) return { kind: "stale" };
  // The same Seq, so the same order: the rows at this offset are these,
  // and the pages past them stand — a re-read of the first page after a
  // Stat must not drop what the scroll had loaded — unless this page was
  // short of the limit, which makes it the last, and anything past it a
  // page beyond the end.
  const kept = held.rows.slice(0, Math.min(offset, held.rows.length));
  const tail = rows.length < limit ? [] : held.rows.slice(offset + rows.length);
  return { kind: "applied", listing: { seq: reply.seq, rows: kept.concat(rows, tail), total: reply.total, crumbs } };
}

// hasMore: rows the folder holds that the listing has not asked for yet.
export function hasMore(l: Listing | null): boolean {
  return l !== null && l.rows.length < l.total;
}

// nextOffset is where the next page starts.
export function nextOffset(l: Listing | null): number {
  return l === null ? 0 : l.rows.length;
}

// nearEnd says whether the list is scrolled close enough to its end to
// ask for the next page: within `rows` rows of it. The numbers are the
// scroller's own (scrollTop, clientHeight, scrollHeight) so the rule is
// testable; a list that cannot scroll at all — shorter than its pane — is
// at its end.
export function nearEnd(scrollTop: number, clientHeight: number, scrollHeight: number, rowHeight: number, rows = 8): boolean {
  return scrollHeight - (scrollTop + clientHeight) <= rowHeight * rows;
}

// liveIds is the set a selection is pruned against once the whole folder
// is on hand: every row held. Null when the listing does not hold the
// whole folder, which is the caller's cue to ask Children instead.
export function liveIds(l: Listing | null): Set<string> | null {
  if (l === null || l.rows.length < l.total) return null;
  return new Set(l.rows.map((r) => r.id));
}

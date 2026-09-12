import { describe, expect, it } from "vitest";
import { CLOSED, canExpand, collapse, edgeY, escape, expand, inContact, naturalEnd, openHeight, overScroll, picked, pushScroll, reveals, rideReachesHome, rideScroll, springScroll, toggle } from "./archbar";
import type { ListMetrics } from "./archbar";

// The Archives page's bottom panel (APP.md §6, ruled 2026-09-11, refined
// 2026-09-12): collapsed it is a header line with the name and a chevron
// dim until a row is selected; opened it grows up over the list, pushing
// the selected row ahead of its top edge into the list's first slot.

const onA = picked(CLOSED, "a");
const upA = expand(onA);

describe("the header", () => {
  it("is dead until a row is selected", () => {
    expect(canExpand(CLOSED)).toBe(false);
    expect(canExpand(onA)).toBe(true);
  });
  it("does nothing with nothing selected", () => {
    expect(expand(CLOSED)).toBe(CLOSED);
    expect(toggle(CLOSED)).toBe(CLOSED);
  });
  it("is one control: a click takes the panel up, the next brings it back", () => {
    const up = toggle(onA);
    expect(up.open).toBe(true);
    expect(toggle(up)).toEqual({ open: false, id: "a" });
  });
});

describe("a row picked", () => {
  it("names it without moving the panel", () => {
    expect(picked(CLOSED, "a")).toEqual({ open: false, id: "a" });
    expect(picked(upA, "b")).toEqual({ open: true, id: "b" });
  });
  it("is the same state again when it is the row already named", () => {
    expect(picked(onA, "a")).toBe(onA);
    expect(picked(upA, "a")).toBe(upA);
  });
  it("takes the panel down when nothing is selected any more", () => {
    expect(picked(upA, null)).toBe(CLOSED);
    expect(picked(onA, null)).toBe(CLOSED);
  });
});

describe("Esc", () => {
  it("collapses the panel", () => {
    expect(escape(upA)).toEqual({ open: false, id: "a" });
  });
  it("hands the same state back when the panel is already down, so the page can leave the key alone", () => {
    expect(escape(onA)).toBe(onA);
    expect(escape(CLOSED)).toBe(CLOSED);
  });
});

describe("the list's push", () => {
  it("pushes the row ahead of the panel's edge on the expand", () => {
    expect(reveals(onA, upA)).toBe(true);
  });
  it("pushes another row up when it is clicked while the panel is up", () => {
    expect(reveals(upA, picked(upA, "b"))).toBe(true);
  });
  it("leaves the list alone while the panel is down", () => {
    expect(reveals(CLOSED, onA)).toBe(false);
    expect(reveals(onA, picked(onA, "b"))).toBe(false);
  });
  it("leaves the list alone on a collapse, and on the same row clicked again", () => {
    expect(reveals(upA, collapse(upA))).toBe(false);
    expect(reveals(upA, picked(upA, "a"))).toBe(false);
  });
});

// The movement's arithmetic. A list of 36 px rows under a 32 px header
// row: the scroller's top at page y 100, 500 px of viewport, and the
// card's foot at page y 700. Forty rows is 1472 px of content, so 972 px
// of the list's own scroll; five rows is 212 px, and none.
const list = (i: number, over: Partial<ListMetrics> = {}): ListMetrics => ({
  listTop: 100,
  viewport: 500,
  content: 32 + 40 * 36,
  headHeight: 32,
  rowTop: 32 + i * 36,
  rowHeight: 36,
  footY: 700,
  line: 44,
  head: 64,
  scroll: 0,
  ...over,
});
// The card's top edge for a card of that height, which is what the two
// riding functions are asked about frame by frame.
const at = (m: ListMetrics, h: number) => edgeY(m, h);

describe("the panel's open height", () => {
  it("runs from the first slot's foot to the card's own, the same for every row", () => {
    expect(openHeight(list(10))).toBe(532); // 700 − (100 + 32 + 36)
    expect(openHeight(list(39))).toBe(532);
    expect(openHeight(list(0))).toBe(532);
  });
  it("leaves the first slot room for a two-line row, so the taller row opens a shorter panel", () => {
    expect(openHeight(list(10, { rowHeight: 52 }))).toBe(516);
  });
  it("is never shorter than the open header, which the first slot leaves room for anyway", () => {
    expect(openHeight(list(10, { footY: 180 }))).toBe(64);
  });
});

describe("the push", () => {
  it("scrolls the row into the list's first slot, under the header row", () => {
    expect(pushScroll(list(10))).toBe(360);
    expect(overScroll(list(10))).toBe(0); // the list reaches it on its own
  });
  it("leaves a row already in the first slot where it is", () => {
    expect(pushScroll(list(0))).toBe(0);
    expect(overScroll(list(0))).toBe(0);
  });
  it("carries the last row of a long list past the list's own end, over a spacer", () => {
    expect(naturalEnd(list(39))).toBe(972);
    expect(pushScroll(list(39))).toBe(1404);
    expect(overScroll(list(39))).toBe(432); // what the spacer must give
  });
  it("carries the last row of a short list too, which has no end of its own", () => {
    const five = list(4, { content: 212, viewport: 212 });
    expect(naturalEnd(five)).toBe(0);
    expect(pushScroll(five)).toBe(144);
    expect(overScroll(five)).toBe(144);
  });
  it("counts the blank under a list shorter than its viewport into the spacer's room", () => {
    // content − viewport is a negative here; the spacer covers that too,
    // or the fifth row of five could never rise to the first slot.
    const five = list(4, { content: 212, viewport: 300 });
    expect(overScroll(five)).toBe(232);
  });
  it("asks a two-line row for the same slot, its own height notwithstanding", () => {
    expect(pushScroll(list(10, { rowHeight: 52 }))).toBe(360);
  });
});

describe("the row riding the rising edge", () => {
  const m = list(10); // the row's foot is at page y 528 with the list at rest
  it("leaves the list alone until the edge reaches the row's foot", () => {
    expect(rideScroll(m, 0, at(m, m.line))).toBe(0); // the edge at 656
    expect(rideScroll(m, 0, 528)).toBe(0); // contact
  });
  it("then scrolls exactly as far as the edge has come", () => {
    expect(rideScroll(m, 0, 500)).toBe(28);
    expect(rideScroll(m, 0, 400)).toBe(128);
  });
  it("stops at the first slot, where the panel stops at the row's foot", () => {
    expect(rideScroll(m, 0, at(m, openHeight(m)))).toBe(360); // pushScroll
    expect(rideScroll(m, 0, 100)).toBe(360); // and no further
  });
  it("starts from where the list stands, never below it", () => {
    expect(rideScroll(m, 200, at(m, m.line))).toBe(200);
    expect(rideScroll(m, 200, 400)).toBe(200); // the edge has not caught up
    expect(rideScroll(m, 200, 300)).toBe(228);
  });
  it("carries the last row past the list's end all the same", () => {
    const last = list(39);
    expect(rideScroll(last, 0, at(last, openHeight(last)))).toBe(1404);
  });
});

describe("the collapse's spring", () => {
  const m = list(10);
  it("stands at the first slot as the collapse begins", () => {
    expect(springScroll(m, 0, at(m, openHeight(m)))).toBe(360);
  });
  it("rides the edge back down", () => {
    expect(springScroll(m, 0, 400)).toBe(128);
    expect(springScroll(m, 0, 500)).toBe(28);
  });
  it("stands where the list stood before the opening, and stops there", () => {
    expect(springScroll(m, 0, at(m, m.line))).toBe(0);
    expect(springScroll(m, 200, 400)).toBe(200); // the edge is still above 200's place
    expect(springScroll(m, 200, at(m, m.line))).toBe(200);
  });
  it("gives back a list the user had scrolled past the row, and no more than its own end", () => {
    expect(springScroll(m, 1200, at(m, m.line))).toBe(972);
    const five = list(4, { content: 212, viewport: 212 });
    expect(springScroll(five, 144, at(five, five.line))).toBe(0);
  });
});

describe("the ride home", () => {
  it("carries a row the collapsed edge clears, which is the ordinary collapse", () => {
    expect(rideReachesHome(list(10), 0)).toBe(true); // its foot is above the edge already
    expect(rideReachesHome(list(0), 0)).toBe(true);
  });
  it("cannot carry a row that moved far down the list while the panel was up", () => {
    // The first row opened at rest, then sorted to the end: the push took
    // the list to 1404 and the row's foot is at 916 with the card down,
    // still below the list's home. The ride would end there.
    const sorted = list(39, { scroll: 1404 });
    expect(inContact(sorted)).toBe(true);
    expect(springScroll(sorted, 0, at(sorted, sorted.line))).toBe(916);
    expect(rideReachesHome(sorted, 0)).toBe(false);
  });
  it("carries it again once home is far enough down the list", () => {
    expect(rideReachesHome(list(39, { scroll: 1404 }), 920)).toBe(true);
  });
  it("counts home as the list's own end, never past it", () => {
    // 1200 is past the list's 972, so home is 972 and the row's 916 rides.
    expect(rideReachesHome(list(39, { scroll: 1404 }), 1200)).toBe(true);
  });
});

describe("contact", () => {
  it("is the list standing at the first slot, which is where the push left it", () => {
    expect(inContact(list(10, { scroll: 360 }))).toBe(true);
    expect(inContact(list(10, { scroll: 360.4 }))).toBe(true); // a fractional scroll is still contact
  });
  it("is lost when the user scrolls the list by hand while the panel is up", () => {
    expect(inContact(list(10, { scroll: 300 }))).toBe(false);
    expect(inContact(list(10, { scroll: 0 }))).toBe(false);
  });
  it("is the resting state for a row that needed no push at all", () => {
    expect(inContact(list(0, { scroll: 0 }))).toBe(true);
  });
});

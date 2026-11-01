import { describe, expect, it } from "vitest";
import { CLOSED, canExpand, canRide, capHeight, collapse, edgeY, escape, expand, inContact, naturalEnd, openHeight, overScroll, picked, pushScroll, reveals, rideReachesHome, rideScroll, slotScroll, springScroll, toggle } from "./archbar";
import type { ListMetrics } from "./archbar";

// The Archives page's bottom panel (APP.md §6, ruled 2026-09-11, refined
// 2026-09-12): collapsed it is a header line with the name and a chevron
// dim until a row is selected; opened it grows up over the list, to its
// content's height (2026-11-01), pushing the selected row ahead of its top
// edge and at most into the list's first slot.

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
// of the list's own scroll; five rows is 212 px, and none. The body is a
// tall one unless a test says otherwise, more than the cap gives: that
// was every panel before the open height became the content's own
// (2026-11-01), so the tests written for that panel ask the same of it.
// The card has no border of its own unless a test gives it one, so a
// height here is the header's and the body's alone.
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
  body: 1000,
  border: 0,
  scroll: 0,
  ...over,
});
// The card's top edge for a card of that height, which is what the two
// riding functions are asked about frame by frame.
const at = (m: ListMetrics, h: number) => edgeY(m, h);

describe("the panel's open height", () => {
  it("is the cap for a body taller than it: the first slot's foot to the card's own, the same for every row", () => {
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
    expect(inContact(sorted, 0, openHeight(sorted))).toBe(true);
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

// Contact is asked of the list's place before the opening and the card's
// height now (2026-11-01): a panel that opens to its content's height
// does not always stand at the cap, so contact is where the spring starts
// rather than the first slot. With the card at the cap, which these are,
// the two are the same place.
const atCap = (m: ListMetrics) => inContact(m, 0, openHeight(m));

describe("contact", () => {
  it("is the list standing at the first slot, which is where the push left it", () => {
    expect(atCap(list(10, { scroll: 360 }))).toBe(true);
    expect(atCap(list(10, { scroll: 360.4 }))).toBe(true); // a fractional scroll is still contact
  });
  it("is lost when the user scrolls the list by hand while the panel is up", () => {
    expect(atCap(list(10, { scroll: 300 }))).toBe(false);
    expect(atCap(list(10, { scroll: 0 }))).toBe(false);
  });
  it("is the resting state for a row that needed no push at all", () => {
    expect(atCap(list(0, { scroll: 0 }))).toBe(true);
  });
  it("is not a list the push brought down to a row above the first slot, which goes back up to where it stood", () => {
    // The row was hidden under the header row when the panel opened, the
    // list at 500: the push brought it down to the first slot, 360. The
    // spring would start at 500, so the collapse eases instead of jumping.
    const m = list(10, { scroll: 360 });
    expect(inContact(m, 500, openHeight(m))).toBe(false);
  });
});

// The open height is the content's own (2026-11-01): the header and the
// body as they lay out, the cap only beyond that. A body of 200 px opens
// a panel of 264 on the 532 the cap gives, its edge at page y 436.
const short = (i: number, over: Partial<ListMetrics> = {}) => list(i, { body: 200, ...over });

describe("a panel shorter than the cap", () => {
  it("opens to its header and its body", () => {
    expect(capHeight(short(10))).toBe(532);
    expect(openHeight(short(10))).toBe(264); // 64 + 200
    expect(edgeY(short(10), openHeight(short(10)))).toBe(436);
  });
  it("rounds a fractional body up, so the body never scrolls by a fraction", () => {
    expect(openHeight(short(10, { body: 200.2 }))).toBe(265);
  });
  it("is the header alone when the body holds nothing", () => {
    expect(openHeight(short(10, { body: 0 }))).toBe(64);
  });
  it("leaves the cap as it was for a body taller than it gives", () => {
    expect(openHeight(list(10, { body: 468 }))).toBe(532); // exactly the cap
    expect(openHeight(list(10, { body: 467 }))).toBe(531); // a pixel short of it
    expect(openHeight(list(10, { body: 600 }))).toBe(532);
  });
  it("counts the card's own border, above and below, so the body's foot is never clipped", () => {
    expect(openHeight(short(10, { border: 2 }))).toBe(266); // 64 + 200 + 2
    expect(openHeight(short(10, { border: 1.6 }))).toBe(266); // a device's rounding, rounded up
  });
  it("leaves the cap where the first slot puts it, border or not", () => {
    expect(capHeight(list(10, { border: 2 }))).toBe(532);
    expect(openHeight(list(10, { body: 466, border: 2 }))).toBe(532); // exactly the cap
    expect(openHeight(list(10, { body: 467, border: 2 }))).toBe(532); // over it, and capped
  });
});

describe("a five-row list under a short panel", () => {
  // Five rows in the 500 px viewport: the last one's foot is at page y
  // 312, far above the edge at 436, so the edge never reaches it.
  const five = short(4, { content: 212 });
  it("leaves even the last row where it is and asks the spacer for nothing", () => {
    expect(pushScroll(five)).toBe(0);
    expect(overScroll(five)).toBe(0);
    expect(rideScroll(five, 0, at(five, openHeight(five)))).toBe(0);
  });
  it("springs back with nothing to move", () => {
    expect(inContact(five, 0, openHeight(five))).toBe(true);
    expect(rideReachesHome(five, 0)).toBe(true);
  });
  it("carries that row to the first slot only when the body is taller than the cap", () => {
    const tall = list(4, { content: 212 });
    expect(pushScroll(tall)).toBe(144);
    expect(overScroll(tall)).toBe(432); // the push's 144 and the 288 px of blank under the rows
  });
});

describe("a one-row list under a short panel", () => {
  // The one row's foot is at page y 168, far above the edge at 436.
  const one = short(0, { content: 32 + 36 });
  it("scrolls nothing and asks the spacer for nothing", () => {
    expect(openHeight(one)).toBe(264);
    expect(pushScroll(one)).toBe(0);
    expect(overScroll(one)).toBe(0); // the 432 px of blank under the row is never scrolled into
  });
  it("leaves the list alone the whole way up", () => {
    expect(rideScroll(one, 0, at(one, one.line))).toBe(0);
    expect(rideScroll(one, 0, at(one, openHeight(one)))).toBe(0);
  });
  it("springs back with nothing to move", () => {
    expect(inContact(one, 0, openHeight(one))).toBe(true);
    expect(springScroll(one, 0, at(one, openHeight(one)))).toBe(0);
    expect(springScroll(one, 0, at(one, one.line))).toBe(0);
    expect(rideReachesHome(one, 0)).toBe(true);
  });
});

describe("the last row of a long list under a short panel", () => {
  // The list scrolled to its own end, 972, where the last row shows: its
  // foot at page y 600, the viewport's foot.
  const last = short(39, { scroll: 972 });
  it("is pushed to just above the edge, not to the first slot", () => {
    expect(slotScroll(last)).toBe(1404);
    expect(pushScroll(last)).toBe(1136); // 1572 - 436: its foot on the edge
    expect(overScroll(last)).toBe(164); // past the list's end by exactly that
  });
  it("stays where it is until the edge reaches its foot, then rides it", () => {
    expect(rideScroll(last, 972, at(last, last.line))).toBe(972);
    expect(rideScroll(last, 972, 600)).toBe(972); // contact, the card at 100
    expect(rideScroll(last, 972, 500)).toBe(1072);
    expect(rideScroll(last, 972, at(last, openHeight(last)))).toBe(1136);
  });
  it("springs back down with the edge to the list's own end", () => {
    const up = short(39, { scroll: 1136 });
    expect(inContact(up, 972, openHeight(up))).toBe(true);
    expect(springScroll(up, 972, 500)).toBe(1072);
    expect(springScroll(up, 972, at(up, up.line))).toBe(972);
    expect(rideReachesHome(up, 972)).toBe(true);
  });
});

describe("a row picked while a short panel is up", () => {
  it("stays where it is when the edge is clear of it", () => {
    expect(pushScroll(short(3))).toBe(0); // its foot at 276, above 436
  });
  it("comes up to just above the edge from under the panel", () => {
    expect(pushScroll(short(12))).toBe(164); // its foot at 600
  });
  it("comes down into the first slot from above it", () => {
    expect(pushScroll(short(2, { scroll: 300 }))).toBe(72);
  });
});

describe("a body taller than the cap", () => {
  // The panel as it was: the cap binds and the body scrolls under the
  // header, every row carried to the first slot.
  const tall = list(10, { body: 600 });
  it("opens to the cap and pushes the row into the first slot", () => {
    expect(openHeight(tall)).toBe(532);
    expect(pushScroll(tall)).toBe(360);
    expect(overScroll(tall)).toBe(0);
    expect(rideScroll(tall, 0, at(tall, openHeight(tall)))).toBe(360);
  });
  it("carries the last row past the list's end as before", () => {
    const last = list(39, { body: 600 });
    expect(pushScroll(last)).toBe(1404);
    expect(overScroll(last)).toBe(432);
  });
  it("springs back from the first slot as before", () => {
    const up = list(10, { body: 600, scroll: 360 });
    expect(inContact(up, 0, openHeight(up))).toBe(true);
    expect(springScroll(up, 0, 400)).toBe(128);
    expect(springScroll(up, 0, at(up, up.line))).toBe(0);
    expect(rideReachesHome(up, 0)).toBe(true);
  });
});

describe("the cap on a short window", () => {
  // The card's foot at page y 400 and 200 px of viewport: the first slot's
  // foot is at 168, so the cap is 232, under the 264 the body asks for.
  const low = (i: number, over: Partial<ListMetrics> = {}) => short(i, { footY: 400, viewport: 200, ...over });
  it("binds, and the body scrolls under the header", () => {
    expect(capHeight(low(3))).toBe(232);
    expect(openHeight(low(3))).toBe(232);
  });
  it("takes the row to the first slot, as the cap always does", () => {
    expect(pushScroll(low(3))).toBe(108); // its foot at 276, the edge at 168
    expect(slotScroll(low(3))).toBe(108);
    expect(overScroll(low(3))).toBe(0);
  });
  it("springs back from there", () => {
    const up = low(3, { scroll: 108 });
    expect(inContact(up, 0, openHeight(up))).toBe(true);
    expect(springScroll(up, 0, at(up, up.line))).toBe(0);
    expect(rideReachesHome(up, 0)).toBe(true);
  });
  it("lets go once the window is tall enough for the body", () => {
    expect(openHeight(low(3, { footY: 700 }))).toBe(264);
  });
});

describe("a resize under an open short panel", () => {
  // Opened on the eleventh row from the top of the list: the edge at 436
  // put the row's foot, at 528, 92 px up the list.
  it("measures the body again: a narrower window wraps it taller, and the row goes up with the edge", () => {
    expect(pushScroll(short(10))).toBe(92);
    const taller = short(10, { body: 280, scroll: 92 });
    expect(openHeight(taller)).toBe(344);
    expect(pushScroll(taller)).toBe(172); // its foot on the new edge, 356
  });
  it("leaves the row where it stands when a wider window makes the body shorter: no second push", () => {
    const shorter = short(10, { body: 150, scroll: 92 });
    expect(openHeight(shorter)).toBe(214);
    expect(pushScroll(shorter)).toBe(92);
  });
  it("asks the spacer afresh for a row past the list's end", () => {
    // The spacer is taken away before the reading, so the list stands at
    // its own end again, 972.
    expect(overScroll(short(39, { scroll: 972 }))).toBe(164);
    expect(overScroll(short(39, { body: 280, scroll: 972 }))).toBe(244);
  });
});

describe("a body that changes under an open panel", () => {
  it("rides the row back down with a shrinking edge, as the collapse does", () => {
    // The pencil's fields took the body to 280 and the panel to 344, the
    // row's foot on the edge at 356 with the list at 172; Discard folds
    // them back to 200, and the edge goes down to 436.
    const m = short(10, { scroll: 172 });
    expect(inContact(m, 0, 344)).toBe(true);
    expect(springScroll(m, 0, at(m, 344))).toBe(172);
    expect(springScroll(m, 0, at(m, openHeight(m)))).toBe(92);
  });
  it("leaves a list scrolled by hand where it is", () => {
    expect(inContact(short(10, { scroll: 150 }), 0, 264)).toBe(false);
  });
  it("rides a growing edge only with a row in view between the first slot and that edge", () => {
    // The card at 264, its edge at 436: the row pushed onto it, the row in
    // the first slot, and a row well clear of the edge all ride.
    expect(canRide(short(10, { scroll: 92 }), 264)).toBe(true);
    expect(canRide(short(10, { scroll: 360 }), 264)).toBe(true);
    expect(canRide(short(3), 264)).toBe(true);
  });
  it("forgives the device's rounding of a row left on the edge", () => {
    expect(canRide(short(10, { scroll: 91.5 }), 264)).toBe(true); // its foot 0.5 past the edge
  });
  it("does not ride a row scrolled above the first slot or under the card, which would jump", () => {
    // The ride would take each to its place at once: the first down to the
    // slot, 360, from 400; the others up to the edge in the first frame.
    expect(canRide(short(10, { scroll: 400 }), 264)).toBe(false);
    expect(canRide(short(12), 264)).toBe(false); // its foot at 600
    expect(canRide(short(13, { scroll: 92 }), 264)).toBe(false); // its foot at 544
  });
});

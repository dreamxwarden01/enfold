// The app's own tooltip (APP.md §6, Controls; ruled 2026-10-18 with the
// crumbs and the compact rail): one caption-type box on the surface, 4 px
// under the element it names — above it when there is no room below; in
// the rail, 4 px to its right (tipBeside) — shown after 300 ms of hover and at once on keyboard focus, gone on
// leave, blur, Escape or a press. One at a time, so two never stand side
// by side. It is used where a name is cut or an icon stands without its
// label (the crumbs, their ellipsis menu, the compact rail); the native
// title stays everywhere else, where a second of delay and the system's
// own box do no harm.
//
// The rules are a small machine (Tips) with its clock and its view handed
// in, so the delay, the one-at-a-time rule and the press that mutes a tip
// until the pointer leaves are tested without a DOM; the action at the
// end is the DOM half.

import type { Action } from "svelte/action";

// TIP_DELAY: how long the pointer rests on an element before its tip
// shows. Focus does not wait: the keyboard has no rest to measure.
export const TIP_DELAY = 300;
// The gap between the element and the box, and the least room kept to
// the window's edges.
export const TIP_GAP = 4;
export const TIP_MARGIN = 8;

export interface TipClock {
  set(fn: () => void, ms: number): unknown;
  clear(handle: unknown): void;
}

export interface TipView<T> {
  show(owner: T): void;
  hide(owner: T): void;
}

// Tips is the one-at-a-time rule. An owner is whatever names an element;
// the view draws and removes its box.
export class Tips<T> {
  private shown: T | null = null;
  private waiting: { owner: T; handle: unknown } | null = null;
  // An element pressed shows no tip again until the pointer has left it:
  // the press answered the question the tip would ask.
  private muted: T | null = null;

  constructor(
    private view: TipView<T>,
    private clock: TipClock,
  ) {}

  get current(): T | null {
    return this.shown;
  }

  // hover: the pointer came onto the owner. Its tip shows after the
  // delay, unless it leaves first; a tip already up for another owner
  // stays until this one replaces it.
  hover(owner: T): void {
    if (this.muted === owner || this.shown === owner) return;
    this.cancel();
    const handle = this.clock.set(() => {
      this.waiting = null;
      this.open(owner);
    }, TIP_DELAY);
    this.waiting = { owner, handle };
  }

  // focus: the keyboard reached the owner; its tip shows now.
  focus(owner: T): void {
    this.cancel();
    this.open(owner);
  }

  leave(owner: T): void {
    if (this.muted === owner) this.muted = null;
    this.end(owner);
  }

  blur(owner: T): void {
    this.end(owner);
  }

  // press: a press on the owner closes its tip and keeps it closed until
  // the pointer leaves.
  press(owner: T): void {
    this.dismiss();
    this.muted = owner;
  }

  // dismiss: Escape, a press elsewhere, a scroll — whatever is up or on
  // its way goes.
  dismiss(): void {
    this.cancel();
    if (this.shown === null) return;
    const s = this.shown;
    this.shown = null;
    this.view.hide(s);
  }

  private end(owner: T): void {
    if (this.waiting?.owner === owner) this.cancel();
    if (this.shown !== owner) return;
    this.shown = null;
    this.view.hide(owner);
  }

  private open(owner: T): void {
    if (this.shown === owner) return;
    if (this.shown !== null) this.view.hide(this.shown);
    this.shown = owner;
    this.view.show(owner);
  }

  private cancel(): void {
    if (!this.waiting) return;
    this.clock.clear(this.waiting.handle);
    this.waiting = null;
  }
}

export interface TipRect {
  left: number;
  top: number;
  bottom: number;
}

export interface TipSize {
  width: number;
  height: number;
}

// tipPlacement puts the box under the element, its left edge on the
// element's, pulled back inside the window; above the element when the
// window has no room for it below.
export function tipPlacement(anchor: TipRect, box: TipSize, viewport: TipSize): { x: number; y: number; above: boolean } {
  const maxX = viewport.width - box.width - TIP_MARGIN;
  const x = Math.max(TIP_MARGIN, Math.min(anchor.left, maxX));
  const below = anchor.bottom + TIP_GAP;
  if (below + box.height <= viewport.height - TIP_MARGIN) return { x, y: below, above: false };
  const above = anchor.top - TIP_GAP - box.height;
  if (above >= TIP_MARGIN) return { x, y: above, above: true };
  return { x, y: below, above: false };
}

export interface TipSide extends TipRect {
  right: number;
}

// tipBeside puts the box 4 px to the right of the element, centred on it
// and pulled back inside the window's height: the rail's place (the final
// shoot of phase 4a), where the items stand 36 to 40 px apart and a tip
// below one covers the next — the next archive, the item drawn current —
// as NavigationView's tips stand beside its items. With no room to the
// right it falls back to the place under the element.
export function tipBeside(anchor: TipSide, box: TipSize, viewport: TipSize): { x: number; y: number; above: boolean } {
  const x = anchor.right + TIP_GAP;
  if (x + box.width > viewport.width - TIP_MARGIN) return tipPlacement(anchor, box, viewport);
  const mid = (anchor.top + anchor.bottom) / 2 - box.height / 2;
  const y = Math.max(TIP_MARGIN, Math.min(mid, viewport.height - box.height - TIP_MARGIN));
  return { x, y, above: false };
}

// ---- the action ------------------------------------------------------------
//
// use:tip={"Archives"} or use:tip={{ text: name, cut: true }}: `cut` shows
// the tip only while the name is cut by its ellipsis (the element's
// .ellipsis child, or the element itself), since a tip repeating a name
// already read whole is noise. An empty text shows nothing. `side: "end"`
// stands the box beside the element instead of under it (tipBeside), to
// the right of the closest [data-tip-anchor] around it when there is one
// — the rail's row, so a name's tip clears the row's × as well.

export type TipParam = string | { text: string; cut?: boolean; side?: "below" | "end" };

const TIP_ID = "app-tip";
interface TipOpts {
  text: string;
  cut: boolean;
  side: "below" | "end";
}

const params = new WeakMap<HTMLElement, TipOpts>();
let box: HTMLDivElement | null = null;
let tips: Tips<HTMLElement> | null = null;

function read(p: TipParam | undefined): TipOpts {
  if (!p) return { text: "", cut: false, side: "below" };
  return typeof p === "string" ? { text: p, cut: false, side: "below" } : { text: p.text ?? "", cut: !!p.cut, side: p.side ?? "below" };
}

function isCut(node: HTMLElement): boolean {
  const t = node.querySelector<HTMLElement>(".ellipsis") ?? node;
  return t.scrollWidth > t.clientWidth + 1;
}

// The tip describes its element unless it says what the element is
// already called: an icon's aria-label, or a name read whole.
function describes(node: HTMLElement, text: string): boolean {
  const name = (node.getAttribute("aria-label") ?? node.textContent ?? "").replace(/\s+/g, " ").trim();
  return name !== text.trim();
}

function setDescribed(node: HTMLElement, on: boolean): void {
  const ids = (node.getAttribute("aria-describedby") ?? "").split(/\s+/).filter((s) => s && s !== TIP_ID);
  if (on) ids.push(TIP_ID);
  if (ids.length) node.setAttribute("aria-describedby", ids.join(" "));
  else node.removeAttribute("aria-describedby");
}

function controller(): Tips<HTMLElement> {
  if (tips) return tips;
  const el = document.createElement("div");
  el.id = TIP_ID;
  el.className = "app-tip";
  el.setAttribute("role", "tooltip");
  el.hidden = true;
  document.body.appendChild(el);
  box = el;
  tips = new Tips<HTMLElement>(
    {
      show(owner) {
        const p = params.get(owner);
        if (!box || !p || !p.text || !owner.isConnected || (p.cut && !isCut(owner))) {
          // Nothing to say after all: the machine is told so, so the next
          // hover asks afresh.
          queueMicrotask(() => tips?.blur(owner));
          return;
        }
        box.textContent = p.text;
        box.hidden = false;
        box.classList.remove("on");
        const r = owner.getBoundingClientRect();
        const size = { width: box.offsetWidth, height: box.offsetHeight };
        const view = { width: window.innerWidth, height: window.innerHeight };
        let at;
        if (p.side === "end") {
          const row = owner.closest<HTMLElement>("[data-tip-anchor]")?.getBoundingClientRect() ?? r;
          at = tipBeside({ left: r.left, top: r.top, bottom: r.bottom, right: Math.max(r.right, row.right) }, size, view);
        } else {
          at = tipPlacement(r, size, view);
        }
        box.style.left = `${Math.round(at.x)}px`;
        box.style.top = `${Math.round(at.y)}px`;
        box.classList.toggle("above", at.above);
        // The fade starts from the placed box (app.css .app-tip.on).
        void box.offsetWidth;
        box.classList.add("on");
        setDescribed(owner, describes(owner, p.text));
      },
      hide(owner) {
        setDescribed(owner, false);
        if (!box) return;
        box.classList.remove("on");
        box.hidden = true;
      },
    },
    { set: (fn, ms) => setTimeout(fn, ms), clear: (h) => clearTimeout(h as ReturnType<typeof setTimeout>) },
  );
  // Escape and a scroll take whatever is up; a press is the owner's own
  // (below) or elsewhere, and either way the tip goes.
  window.addEventListener("keydown", (e) => {
    if (e.key === "Escape") tips?.dismiss();
  }, true);
  window.addEventListener("scroll", () => tips?.dismiss(), true);
  window.addEventListener("resize", () => tips?.dismiss());
  return tips;
}

export const tip: Action<HTMLElement, TipParam> = (node, param) => {
  params.set(node, read(param));
  const t = controller();
  const enter = (e: PointerEvent) => {
    if (e.pointerType !== "touch") t.hover(node);
  };
  const leave = () => t.leave(node);
  // Focus shows the tip only when the keyboard put it there: a click
  // focuses its button too, and that press has just closed the tip.
  const focus = () => {
    if (node.matches(":focus-visible")) t.focus(node);
  };
  const blur = () => t.blur(node);
  const press = () => t.press(node);
  node.addEventListener("pointerenter", enter);
  node.addEventListener("pointerleave", leave);
  node.addEventListener("focus", focus);
  node.addEventListener("blur", blur);
  node.addEventListener("pointerdown", press);
  return {
    update(p: TipParam) {
      params.set(node, read(p));
      // A tip up while its text changes says the new text.
      if (t.current === node) {
        const now = params.get(node);
        if (box && now?.text) box.textContent = now.text;
        else t.dismiss();
      }
    },
    destroy() {
      node.removeEventListener("pointerenter", enter);
      node.removeEventListener("pointerleave", leave);
      node.removeEventListener("focus", focus);
      node.removeEventListener("blur", blur);
      node.removeEventListener("pointerdown", press);
      t.leave(node);
      params.delete(node);
    },
  };
};

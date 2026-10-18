// One pending primitive (direction 4, phase 3; APP.md §6): a wait the user
// started shows on the control that started it, and only once it has
// lasted 300 ms, so a quick answer never flashes anything. Then the
// control gets .pending — the 14 px ring the preview pane draws, one rule
// in app.css — and aria-busy, and both go when the wait settles. The label
// stays unless a verb adds something (strings.ts pendingCopy): a control
// that wants one passes `armed`, told when the ring shows and goes.
//
// The timer is apart from the DOM so vitest can drive it with fake timers;
// the Svelte action below is a thin coat over it.

export const PENDING_DELAY = 300;

export interface PendingTimer {
  // start arms the timer; a start while armed or showing restarts nothing.
  start(): void;
  // settle disarms it and takes the ring away if it showed.
  settle(): void;
  readonly showing: boolean;
}

export function pendingTimer(onChange: (showing: boolean) => void, delay = PENDING_DELAY): PendingTimer {
  let timer: ReturnType<typeof setTimeout> | undefined;
  let showing = false;
  return {
    start() {
      if (timer !== undefined || showing) return;
      timer = setTimeout(() => {
        timer = undefined;
        showing = true;
        onChange(true);
      }, delay);
    },
    settle() {
      if (timer !== undefined) clearTimeout(timer);
      timer = undefined;
      if (showing) {
        showing = false;
        onChange(false);
      }
    },
    get showing() {
      return showing;
    },
  };
}

// What the action takes: the promise the control is waiting on, or a flag
// for a wait the component already tracks; or either with `armed`.
export type PendingOn = Promise<unknown> | boolean | null | undefined;
export type PendingParam = PendingOn | { on: PendingOn; armed?: (showing: boolean) => void };

function split(p: PendingParam): { on: PendingOn; armed?: (showing: boolean) => void } {
  if (p && typeof p === "object" && "on" in p) return p;
  return { on: p as PendingOn };
}

// The element the action works on: the class list and an attribute, all
// it touches, so a test can hand it a stand-in.
export interface PendingNode {
  classList: { add(c: string): void; remove(c: string): void };
  setAttribute(name: string, value: string): void;
  removeAttribute(name: string): void;
}

// pending is the Svelte action: use:pending={promise}, use:pending={flag},
// or use:pending={{ on, armed }}. A promise settles the wait however it
// ends; a newer promise replaces an older one, whose settling is then
// ignored.
export function pending(node: PendingNode, param: PendingParam) {
  let armed = split(param).armed;
  let gen = 0;
  const timer = pendingTimer((showing) => {
    if (showing) {
      node.classList.add("pending");
      node.setAttribute("aria-busy", "true");
    } else {
      node.classList.remove("pending");
      node.removeAttribute("aria-busy");
    }
    armed?.(showing);
  });

  function apply(p: PendingParam): void {
    const s = split(p);
    armed = s.armed;
    const mine = ++gen;
    if (s.on && typeof s.on === "object" && "then" in s.on) {
      timer.start();
      const done = () => {
        if (mine === gen) timer.settle();
      };
      s.on.then(done, done);
    } else if (s.on === true) {
      timer.start();
    } else {
      timer.settle();
    }
  }

  apply(param);
  return {
    update(p: PendingParam) {
      apply(p);
    },
    destroy() {
      gen++;
      timer.settle();
    },
  };
}

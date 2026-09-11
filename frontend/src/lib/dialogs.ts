// The dialogs over the page, as a stack (APP.md §7, "Dialogs stay put").
// While a dialog is up the page behind it is inert and takes no shortcut:
// the Dialog enforces that itself — a key aimed outside it is stopped
// before the page's listeners see it, and the focus is held in the box.
// Two dialogs can stand at once — a refused-name question arriving over
// Rename, the reveal's confirmation over the reveal — and then only the
// topmost owns Escape and the trap: the one below neither closes on the
// key nor pulls the focus back (the review's finding 11: every live
// dialog took the same Escape, and Tab reached the obscured one).
//
// A Dialog registers on mount and unregisters on destroy; the last live
// entry is the topmost. Live is the entry's own word: a dialog on its way
// out — Svelte marks an outroing element inert, and destroys it only once
// the fade ends — and one covered by a dialog of its own are not live and
// so never topmost, whatever their place in the stack.
export interface DialogEntry {
  live: () => boolean;
}

const stack: DialogEntry[] = [];

// registerDialog pushes an entry and returns what unregisters it.
export function registerDialog(entry: DialogEntry): () => void {
  stack.push(entry);
  return () => {
    const i = stack.indexOf(entry);
    if (i >= 0) stack.splice(i, 1);
  };
}

// topDialog is the topmost live dialog, or null with none up.
export function topDialog(): DialogEntry | null {
  for (let i = stack.length - 1; i >= 0; i--) {
    if (stack[i].live()) return stack[i];
  }
  return null;
}

export function isTopDialog(entry: DialogEntry): boolean {
  return topDialog() === entry;
}

// dialogIsUp is the page's own check beside the Dialog's, for a handler
// that wants to say so in one place.
export function dialogIsUp(): boolean {
  return topDialog() !== null;
}

// A popover a dialog owns but renders beside it — the details modal's Copy
// menu is the dialog's sibling in the DOM — carries this attribute, so the
// trap treats it as inside the dialog: the focus may rest in it and a key
// pressed there is its own (the review's finding 10: the trap pulled the
// focus straight back out of the menu).
export const POPOVER_ATTR = "data-popover";

export function inPopover(t: EventTarget | null): boolean {
  return t instanceof Element && t.closest(`[${POPOVER_ATTR}]`) !== null;
}

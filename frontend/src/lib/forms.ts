// The form rule's marks (APP.md §6, Forms and their errors), as the part
// every marked field shares: when a field is marked, when its mark goes,
// and what counts as the user leaving it.
//
// A field's mark is one flag. A leave or a press sets it when the field
// holds something wrong; it goes the moment the value is right, and only
// another leave or press brings it back — typing never marks, so a field
// left right and then emptied says nothing until it is left again. A new
// prompt is a new question: every mark on the form goes, whatever value a
// field keeps (the merged card's password, the ruling of 2026-09-08).
// Each field carries its own flag, so correcting one never un-marks
// another that still refuses.

// MarkEvent is what happened to a field (or to the form, for a prompt).
//   leave  — the user moved to something else in the page (see onLeave)
//   press  — the form's button, or Enter in one of its fields
//   change — the value changed: typed, pasted, or moved by another field
//            it is judged against (the second copy of a chosen password)
//   prompt — a new prompt arrived for the form
export type MarkEvent = "leave" | "press" | "change" | "prompt";

// nextMark is the field's mark after the event, given what is wrong with
// the field now ("" when nothing is).
export function nextMark(event: MarkEvent, marked: boolean, problem: string): boolean {
  switch (event) {
    case "leave":
    case "press":
      return !!problem;
    case "change":
      return marked && !!problem;
    case "prompt":
      return false;
  }
}

// isLeave says whether a blur is the user leaving the field. Focus moving
// to something else in the page is: the blur names where focus went
// (relatedTarget), or — a click on nothing focusable — names nothing while
// the page keeps focus and the field is still in it. The window losing
// focus is not: Alt-Tab to a password manager, a click on another window,
// the system taking the foreground. The field keeps the caret and gets it
// back when the window does, so an untouched field must not come back
// saying "This field is required." Nor is a field taken out of the page
// from under the focus (the merged card turning into the password alone):
// the page moved, not the user.
export function isLeave(relatedTarget: EventTarget | null, pageHasFocus: boolean, fieldConnected = true): boolean {
  if (relatedTarget !== null) return true;
  return pageHasFocus && fieldConnected;
}

// onLeave turns a field's blur into a leave, when it is one. A blur that
// names no target is decided a task later: when the window loses focus
// the element's blur is dispatched before the window's, so the page's own
// focus is read once both have run, and a field removed from the page in
// the meantime was not left. epoch, when given, names the question the
// form is asking (SecretInput counts its prompts): a leave decided after
// a new prompt but begun under the old one is dropped, since the prompt
// has already cleared every mark the old question made.
export function onLeave(leave: () => void, epoch?: () => number): (e: FocusEvent) => void {
  return (e) => {
    if (e.relatedTarget) {
      leave();
      return;
    }
    // currentTarget is null once dispatch is over: take the field now.
    const field = (e.currentTarget ?? e.target ?? null) as Node | null;
    const at = epoch?.();
    setTimeout(() => {
      if (epoch && epoch() !== at) return;
      if (isLeave(null, document.hasFocus(), field?.isConnected ?? true)) leave();
    }, 0);
  };
}

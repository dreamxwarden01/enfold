import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { isLeave, nextMark, onLeave } from "./forms";
import type { MarkEvent } from "./forms";
import { REQUIRED } from "./validate";

// The form rule (APP.md §6, Forms and their errors; the ruling of
// 2026-10-13): a field is marked when the user leaves it, or presses the
// button, with something wrong; the mark goes the moment the value is
// right and comes back only after another leave or press; a new prompt
// clears every mark; and the window losing focus is not a leave.

// run plays a field's history: each step is an event and what is wrong
// with the field once it has happened.
function run(steps: [MarkEvent, string][]): boolean[] {
  let marked = false;
  return steps.map(([event, problem]) => (marked = nextMark(event, marked, problem)));
}

describe("a field's mark", () => {
  it("is set by a leave or a press with something wrong", () => {
    expect(nextMark("leave", false, REQUIRED)).toBe(true);
    expect(nextMark("press", false, REQUIRED)).toBe(true);
  });

  it("is not set by a leave or a press with nothing wrong", () => {
    expect(nextMark("leave", false, "")).toBe(false);
    expect(nextMark("press", false, "")).toBe(false);
  });

  it("is never set by typing", () => {
    expect(nextMark("change", false, REQUIRED)).toBe(false);
  });

  it("goes the moment the value is right, and stays while it is still wrong", () => {
    expect(nextMark("change", true, "")).toBe(false);
    expect(nextMark("change", true, "At least 8 characters.")).toBe(true);
  });

  it("goes with a new prompt, whatever the field holds", () => {
    expect(nextMark("prompt", true, REQUIRED)).toBe(false);
    expect(nextMark("prompt", false, REQUIRED)).toBe(false);
  });

  it("does not come back on a keystroke once corrected", () => {
    // Left empty, typed right, emptied again: marked only when left again.
    expect(run([
      ["leave", REQUIRED],
      ["change", ""],
      ["change", REQUIRED],
      ["leave", REQUIRED],
    ])).toEqual([true, false, false, true]);
  });

  it("is not set when a field left right is emptied later", () => {
    // Typed, left right, come back and cleared to retype.
    expect(run([
      ["change", ""],
      ["leave", ""],
      ["change", REQUIRED],
    ])).toEqual([false, false, false]);
  });

  it("is not carried by a press made while the field was right", () => {
    // The merged card: the password is right when Continue sends the PIN;
    // the PIN is refused (a new prompt); the password is then cleared to
    // retype. Nothing is marked until it is left or pressed empty.
    expect(run([
      ["change", ""],
      ["press", ""],
      ["prompt", ""],
      ["change", REQUIRED],
    ])).toEqual([false, false, false, false]);
    expect(run([
      ["press", ""],
      ["prompt", ""],
      ["change", REQUIRED],
      ["press", REQUIRED],
    ])).toEqual([false, false, false, true]);
  });

  it("on the recovery cells, is not brought back by retyping a group", () => {
    // Pressed with seven groups in; the eighth goes in; a group is then
    // selected for retyping, and its first digit leaves the key short.
    const missing = "All 48 digits are needed.";
    expect(run([
      ["press", missing],
      ["change", ""],
      ["change", missing],
    ])).toEqual([true, false, false]);
  });

  it("does not outlive the prompt it was made under", () => {
    // Pressed empty, then the prompt is asked again: the field keeps its
    // value, and a later keystroke that leaves it wrong marks nothing.
    expect(run([
      ["press", REQUIRED],
      ["prompt", REQUIRED],
      ["change", REQUIRED],
    ])).toEqual([true, false, false]);
  });
});

describe("what counts as leaving a field", () => {
  const other = {} as EventTarget;

  it("is focus moving to something else in the page", () => {
    expect(isLeave(other, true)).toBe(true);
    // A click on nothing focusable: no target, the page keeps focus.
    expect(isLeave(null, true)).toBe(true);
  });

  it("is not the window losing focus", () => {
    expect(isLeave(null, false)).toBe(false);
  });

  it("is not the field taken out of the page from under the focus", () => {
    expect(isLeave(null, true, false)).toBe(false);
  });
});

describe("a blur, as the field sees it", () => {
  let pageHasFocus = true;

  beforeEach(() => {
    vi.useFakeTimers();
    pageHasFocus = true;
    vi.stubGlobal("document", { hasFocus: () => pageHasFocus });
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  const blur = (relatedTarget: EventTarget | null, currentTarget?: { isConnected: boolean }) =>
    ({ relatedTarget, currentTarget }) as unknown as FocusEvent;

  it("leaves at once when focus moves to another element", () => {
    const leave = vi.fn();
    onLeave(leave)(blur({} as EventTarget));
    expect(leave).toHaveBeenCalledTimes(1);
  });

  it("leaves a task later when focus goes nowhere in the page", () => {
    const leave = vi.fn();
    onLeave(leave)(blur(null));
    expect(leave).not.toHaveBeenCalled();
    vi.runAllTimers();
    expect(leave).toHaveBeenCalledTimes(1);
  });

  it("does not leave when the window lost focus", () => {
    // The element's blur comes first, while the page may still read as
    // focused; the window's own blur follows, and the decision waits for it.
    const leave = vi.fn();
    onLeave(leave)(blur(null));
    pageHasFocus = false;
    vi.runAllTimers();
    expect(leave).not.toHaveBeenCalled();
  });

  it("does not leave when the field was removed from under the focus", () => {
    // The merged card's PIN is accepted with the caret in the password:
    // the card turns into the password alone, and the focused input is
    // taken out of the page. Its blur names no target and the page keeps
    // focus, but the user left nothing.
    const leave = vi.fn();
    const field = { isConnected: true };
    onLeave(leave)(blur(null, field));
    field.isConnected = false;
    vi.runAllTimers();
    expect(leave).not.toHaveBeenCalled();
  });

  it("drops a leave begun under a prompt that has since been replaced", () => {
    // The new prompt clears every mark; a blur from before it, decided
    // after it, must not put one back.
    let prompts = 0;
    let marked = false;
    const handler = onLeave(() => (marked = nextMark("leave", marked, REQUIRED)), () => prompts);
    handler(blur(null, { isConnected: true }));
    prompts++;
    marked = nextMark("prompt", marked, REQUIRED);
    vi.runAllTimers();
    expect(marked).toBe(false);
  });

  it("still leaves under the same prompt", () => {
    const leave = vi.fn();
    onLeave(leave, () => 3)(blur(null, { isConnected: true }));
    vi.runAllTimers();
    expect(leave).toHaveBeenCalledTimes(1);
  });

  it("leaves an untouched, focused empty field unmarked across an Alt-Tab", () => {
    // A new prompt focuses the empty field; the user switches to a
    // password manager and back. The field must not say it is required.
    let marked = nextMark("prompt", false, REQUIRED);
    const handler = onLeave(() => (marked = nextMark("leave", marked, REQUIRED)));
    pageHasFocus = false;
    handler(blur(null));
    vi.runAllTimers();
    expect(marked).toBe(false);
  });
});

import { describe, expect, it } from "vitest";
import { closeButton, toLeave } from "./leave";

// One meaning for Close (APP.md §2.3, ruled 2026-10-19): the locked line,
// the lock screen's notice and the needs_reopen bar leave, and offer the
// kill switch only once what they name still stands after a leave.

describe("the close of the locked line, the notice and the bar", () => {
  const none = () => false;
  const all = () => true;

  it("leaves under its own words while nothing it names has been left", () => {
    expect(closeButton("line", ["a"], none)).toEqual({ force: false, label: "Close archive" });
    expect(closeButton("line", ["a", "b"], none)).toEqual({ force: false, label: "Close all" });
    expect(closeButton("notice", ["a"], none)).toEqual({ force: false, label: "Close it" });
    expect(closeButton("notice", ["a", "b"], none)).toEqual({ force: false, label: "Close them" });
    expect(closeButton("bar", ["a"], none)).toEqual({ force: false, label: "Close" });
  });

  it("reads Force close once every archive it names is still held after its leave", () => {
    expect(closeButton("line", ["a"], all)).toEqual({ force: true, label: "Force close" });
    expect(closeButton("line", ["a", "b"], all)).toEqual({ force: true, label: "Force close all" });
    expect(closeButton("notice", ["a"], all)).toEqual({ force: true, label: "Force close" });
    expect(closeButton("notice", ["a", "b"], all)).toEqual({ force: true, label: "Force close them" });
    expect(closeButton("bar", ["a"], all)).toEqual({ force: true, label: "Force close" });
  });

  it("leaves again while one of several has not been left, and reaches only that one", () => {
    const held = (id: string) => id === "a";
    expect(closeButton("line", ["a", "b"], held)).toEqual({ force: false, label: "Close all" });
    expect(closeButton("notice", ["a", "b"], held).force).toBe(false);
    expect(toLeave(["a", "b"], held)).toEqual(["b"]);
  });

  it("never forces with nothing named", () => {
    expect(closeButton("line", [], all).force).toBe(false);
    expect(toLeave([], none)).toEqual([]);
  });
});

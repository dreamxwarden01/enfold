import { describe, expect, it } from "vitest";
import { asksOnClose, closeActionLabel, closeChoices } from "./closing";

// The Settings row of APP.md §2.4 (ruled 2026-09-13): *When the window
// closes: Ask each time · Keep running in the tray · Quit*. The old
// destroy/hide labels are gone with the setting they belonged to.
describe("the close action's row", () => {
  it("offers the three choices, asking first", () => {
    expect(closeChoices).toEqual([
      ["ask", "Ask each time"],
      ["tray", "Keep running in the tray"],
      ["quit", "Quit"],
    ]);
  });

  it("names each one the way the row and the save bar say it", () => {
    expect(closeActionLabel("ask")).toBe("Ask each time");
    expect(closeActionLabel("tray")).toBe("Keep running in the tray");
    expect(closeActionLabel("quit")).toBe("Quit");
  });

  it("reads anything else as the default, as the core reads the file", () => {
    expect(closeActionLabel("destroy")).toBe("Ask each time");
    expect(closeActionLabel("hide")).toBe("Ask each time");
    expect(closeActionLabel("")).toBe("Ask each time");
    expect(closeActionLabel(undefined)).toBe("Ask each time");
  });

  it("says whether a close asks at all", () => {
    expect(asksOnClose("ask")).toBe(true);
    expect(asksOnClose("tray")).toBe(false);
    expect(asksOnClose("quit")).toBe(false);
    // Whatever is not one of the two answers asks: the close button never
    // means "to the tray" until the user has said so.
    expect(asksOnClose("destroy")).toBe(true);
    expect(asksOnClose(undefined)).toBe(true);
  });
});

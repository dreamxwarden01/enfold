import { describe, expect, it } from "vitest";
import { leavesHint } from "./rail";

// The rail's interim hint (APP.md §6): while an archive's page is open,
// every rail item says that a click leaves it.
describe("the rail's leaves hint", () => {
  it("names the open archive by the page's own heading", () => {
    expect(leavesHint({ route: "archive", current: "a1", pageName: "Photos 2024", listName: "Old name" })).toBe("Leaves Photos 2024");
  });

  it("falls back to the list's name before the page has its listing", () => {
    expect(leavesHint({ route: "archive", current: "a1", pageName: "", listName: "Photos 2024" })).toBe("Leaves Photos 2024");
  });

  it("still says it leaves when no name is known yet", () => {
    expect(leavesHint({ route: "archive", current: "a1" })).toBe("Leaves this archive");
  });

  it("says nothing where a click leaves nothing", () => {
    expect(leavesHint({ route: "archives", current: null, pageName: "Photos 2024" })).toBeUndefined();
    expect(leavesHint({ route: "keys", current: "a1", pageName: "Photos 2024" })).toBeUndefined();
    expect(leavesHint({ route: "archive", current: null, pageName: "Photos 2024" })).toBeUndefined();
  });
});

import { describe, expect, it } from "vitest";
import { NAME_TAKEN, addPending, createProblem, dropTo, joinPath, leafOf, parentOf, pendingIn, reconcile } from "./pending";
import { LEAF_RULE, REQUIRED } from "./validate";

describe("paths inside an archive", () => {
  it("splits a stored name on its one separator", () => {
    expect(parentOf("2024/trips/notes.md")).toBe("2024/trips");
    expect(parentOf("notes.md")).toBe("");
    expect(leafOf("2024/trips/notes.md")).toBe("notes.md");
    expect(leafOf("notes.md")).toBe("notes.md");
  });

  it("joins a leaf onto the folder shown, the root having no prefix", () => {
    expect(joinPath("", "Receipts")).toBe("Receipts");
    expect(joinPath("2024", "Receipts")).toBe("2024/Receipts");
  });
});

describe("where a created folder appears", () => {
  it("is a row of the folder it was created in, and of no other", () => {
    const pending = ["Receipts", "2024/Trips", "2024/Trips/Day one"];
    expect(pendingIn(pending, "")).toEqual(["Receipts"]);
    expect(pendingIn(pending, "2024")).toEqual(["2024/Trips"]);
    expect(pendingIn(pending, "2024/Trips")).toEqual(["2024/Trips/Day one"]);
    expect(pendingIn(pending, "2025")).toEqual([]);
  });

  it("lists them in name order", () => {
    expect(pendingIn(["Receipts", "Bills"], "")).toEqual(["Bills", "Receipts"]);
  });

  it("adds one under the folder shown, and the same one only once", () => {
    expect(addPending([], "", "Receipts")).toEqual(["Receipts"]);
    expect(addPending(["Receipts"], "2024", "Trips")).toEqual(["Receipts", "2024/Trips"]);
    expect(addPending(["Receipts"], "", "Receipts")).toEqual(["Receipts"]);
    expect(addPending([], "2024", "  Trips  ")).toEqual(["2024/Trips"]);
  });
});

describe("when a created folder stops being the page's", () => {
  it("lets go of it once the archive lists it: a file was added into it", () => {
    expect(reconcile(["Receipts"], "", ["Receipts"])).toEqual([]);
    expect(reconcile(["2024/Trips"], "2024", ["Trips"])).toEqual([]);
  });

  it("keeps one the archive does not list yet", () => {
    expect(reconcile(["Receipts"], "", ["2024"])).toEqual(["Receipts"]);
  });

  it("says nothing about folders the listing does not cover", () => {
    // A listing of the root cannot report on what is inside 2024.
    expect(reconcile(["2024/Trips"], "", ["2024"])).toEqual(["2024/Trips"]);
  });
});

describe("where the page stands once the created folders are dropped", () => {
  it("stays where it is when nothing on the way up was the page's", () => {
    expect(dropTo(["Receipts"], "2024/Trips")).toBe("2024/Trips");
    expect(dropTo(["Receipts"], "")).toBe("");
    expect(dropTo([], "2024")).toBe("2024");
  });

  it("steps out of a folder that is dropped", () => {
    expect(dropTo(["2024/Trips"], "2024/Trips")).toBe("2024");
    expect(dropTo(["Receipts"], "Receipts")).toBe("");
  });

  it("steps out of everything under one, however deep", () => {
    expect(dropTo(["2024/Trips"], "2024/Trips/Day one/Photos")).toBe("2024");
  });
});

describe("the name a created folder may take", () => {
  it("obeys the rename's rule: a name is required, and carries no separator", () => {
    expect(createProblem("Receipts", [])).toBe("");
    expect(createProblem("", [])).toBe(REQUIRED);
    expect(createProblem("   ", [])).toBe(REQUIRED);
    expect(createProblem("a/b", [])).toBe(LEAF_RULE);
  });

  it("refuses a name the folder already holds, whatever its case", () => {
    expect(createProblem("Receipts", ["Receipts"])).toBe(NAME_TAKEN);
    expect(createProblem("receipts", ["Receipts"])).toBe(NAME_TAKEN);
    expect(createProblem(" Receipts ", ["notes.md", "Receipts"])).toBe(NAME_TAKEN);
    expect(createProblem("Bills", ["Receipts"])).toBe("");
  });
});

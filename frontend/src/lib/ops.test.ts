import { describe, expect, it } from "vitest";
import { cancellable, opLabel } from "./ops";

// The operation strip (APP.md §2.3, §6).
describe("the running operation's name", () => {
  it("names each kind", () => {
    expect(opLabel("add")).toBe("Adding");
    expect(opLabel("replace")).toBe("Replacing");
    expect(opLabel("extract")).toBe("Extracting");
    expect(opLabel("verify")).toBe("Verifying");
    expect(opLabel("compact")).toBe("Compacting");
    expect(opLabel("rotate")).toBe("Rotating key");
  });

  it("has no word for a save: nothing is staged between operations", () => {
    expect(opLabel("save")).toBe("save");
  });

  it("says a kind it does not know rather than nothing at all", () => {
    expect(opLabel("something-new")).toBe("something-new");
  });
});

describe("what Cancel is offered for", () => {
  it("is an add or a replace: aborting one publishes nothing", () => {
    expect(cancellable("add")).toBe(true);
    expect(cancellable("replace")).toBe(true);
  });

  it("is nothing else", () => {
    for (const k of ["extract", "verify", "compact", "rotate", ""]) {
      expect(cancellable(k)).toBe(false);
    }
  });
});

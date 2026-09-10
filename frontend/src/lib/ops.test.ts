import { describe, expect, it } from "vitest";
import { cancellable, opLabel, opPhase } from "./ops";

// The operation strip (APP.md §2.3, §6). Since 2026-09-09 the strip says
// what the operation is doing and to how many files — "Adding — adding"
// said the same word twice.
describe("the running operation's name", () => {
  it("counts the files an add, a replace and an extract planned", () => {
    expect(opLabel("add", 3)).toBe("Adding 3 files");
    expect(opLabel("replace", 1)).toBe("Replacing 1 file");
    expect(opLabel("extract", 12)).toBe("Extracting 12 files");
  });

  it("is singular at one, everywhere it counts", () => {
    expect(opLabel("add", 1)).toBe("Adding 1 file");
    expect(opLabel("extract", 1)).toBe("Extracting 1 file");
  });

  it("groups a big count as the rest of the page groups one", () => {
    expect(opLabel("add", 12406)).toBe("Adding 12,406 files");
  });

  it("stands on the bare verb until the plan is made", () => {
    // Items is zero until the operation has walked its source (APP.md §3).
    expect(opLabel("add")).toBe("Adding");
    expect(opLabel("add", 0)).toBe("Adding");
    expect(opLabel("extract", 0)).toBe("Extracting");
  });

  it("names the kinds that count nothing", () => {
    expect(opLabel("verify", 0)).toBe("Verifying");
    expect(opLabel("compact", 0)).toBe("Compacting");
    expect(opLabel("rotate", 0)).toBe("Rotating key");
  });

  it("says what the core's own compaction is for, not what it is", () => {
    // The follow-on the core runs after a commit that leaves the free
    // space over APP.md §2.3's thresholds.
    expect(opLabel("reclaim", 0)).toBe("Reclaiming space");
    // Its Items is zero and stays zero: it moves bytes, not files.
    expect(opLabel("reclaim", 5)).toBe("Reclaiming space");
  });

  it("has no word for a save: nothing is staged between operations", () => {
    expect(opLabel("save", 0)).toBe("save");
  });

  it("says a kind it does not know rather than nothing at all", () => {
    expect(opLabel("something-new", 4)).toBe("something-new");
  });
});

// No phase word beside the verb (APP.md §6): the phase is a second line
// only where it says something the name does not.
describe("the phase beside the name", () => {
  it("drops a phase that only says the verb again", () => {
    expect(opPhase("adding")).toBe("");
    expect(opPhase("replacing")).toBe("");
    expect(opPhase("extracting")).toBe("");
    // A reclaim *is* a compaction; the name has already said what it is for.
    expect(opPhase("compacting")).toBe("");
    // The phase every operation carries before it has done anything: it
    // says nothing about where the work has got to.
    expect(opPhase("starting")).toBe("");
  });

  it("drops the phase an operation ends on, which no running strip shows", () => {
    expect(opPhase("verified")).toBe("");
    expect(opPhase("compacted")).toBe("");
    expect(opPhase("rotated")).toBe("");
  });

  it("keeps a phase that says where the work has got to", () => {
    expect(opPhase("hashing")).toBe("hashing");
    expect(opPhase("registry")).toBe("registry");
    expect(opPhase("archive")).toBe("archive");
  });

  it("says nothing when there is no phase yet", () => {
    expect(opPhase("")).toBe("");
    expect(opPhase(undefined)).toBe("");
    expect(opPhase("   ")).toBe("");
  });
});

describe("what Cancel is offered for", () => {
  it("is an add or a replace: aborting one publishes nothing", () => {
    expect(cancellable("add")).toBe(true);
    expect(cancellable("replace")).toBe(true);
  });

  it("is a reclaim: the user never asked for it, so it is theirs to stop", () => {
    expect(cancellable("reclaim")).toBe(true);
  });

  it("is nothing else", () => {
    for (const k of ["extract", "verify", "compact", "rotate", ""]) {
      expect(cancellable(k)).toBe(false);
    }
  });
});

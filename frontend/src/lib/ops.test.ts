import { describe, expect, it } from "vitest";
import { cancellable, hasBar, opErrorLine, opLabel, opPhase, reclaimedLine, showsCancel, stripCount } from "./ops";
import { dragOutCopy, reclaimedText } from "./strings";

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

// The drag out on the strip (APP.md §3, ruled 2026-09-11 after the first
// real drag, whose staging was over before the strip appeared): one label,
// *Extracting N items*, from the moment the drag starts and through all
// three of its phases — the hover (the label alone), the staging (the byte
// bar with *Cancel*) and the wait on Explorer (that same bar, standing
// full, with nothing to cancel and no words of its own). The operation ends
// when DoDragDrop returns, and the strip goes with it.
describe("the drag out on the strip", () => {
  it("counts what the gesture named, folders included, singular at one", () => {
    expect(opLabel("dragout", 2, "dragging")).toBe("Extracting 2 items");
    expect(opLabel("dragout", 2, "preparing")).toBe("Extracting 2 items");
    expect(opLabel("dragout", 2, "awaiting")).toBe("Extracting 2 items");
    expect(opLabel("dragout", 1, "dragging")).toBe("Extracting 1 item");
    expect(opLabel("dragout", 1, "awaiting")).toBe("Extracting 1 item");
    expect(dragOutCopy.extracting).toBe("Extracting");
  });

  it("counts the records the drag named, not the files beneath them", () => {
    // A folder of forty files dragged out is one item on the strip, and
    // forty files under the bar (OpView.DragItems against OpView.Items).
    expect(stripCount({ kind: "dragout", items: 40, dragItems: 1 })).toBe(1);
    expect(stripCount({ kind: "extract", items: 40, dragItems: 1 })).toBe(40);
    expect(stripCount({ kind: "add", items: 3 })).toBe(3);
    // Before the core has said — an older event, a mock that says nothing —
    // the bare verb stands rather than a wrong number.
    expect(stripCount({ kind: "dragout", items: 40 })).toBe(0);
    expect(opLabel("dragout", 0, "dragging")).toBe("Extracting");
  });

  it("is the label alone during the hover: no bar, nothing to cancel", () => {
    expect(hasBar("dragout", "dragging")).toBe(false);
    expect(cancellable("dragout", "dragging")).toBe(false);
    expect(showsCancel("dragout", "dragging")).toBe(false);
  });

  it("shows the byte bar with Cancel while it stages", () => {
    expect(hasBar("dragout", "preparing")).toBe(true);
    expect(cancellable("dragout", "preparing")).toBe(true);
    expect(showsCancel("dragout", "preparing")).toBe(true);
  });

  it("keeps that bar, full and uncancellable, while Explorer copies", () => {
    // No second phase and no words of their own: Explorer's own window is
    // in front by then, and it is the only control there is.
    expect(hasBar("dragout", "awaiting")).toBe(true);
    expect(cancellable("dragout", "awaiting")).toBe(false);
    expect(opLabel("dragout", 3, "awaiting")).toBe("Extracting 3 items");
  });

  it("keeps Cancel in its place while it stands full, greyed and unclickable", () => {
    // The button is drawn and cannot be pressed: one that vanished would
    // reflow the strip at the very moment the user is watching the bar
    // (APP.md §3, ruled 2026-09-11).
    expect(showsCancel("dragout", "awaiting")).toBe(true);
    expect(cancellable("dragout", "awaiting")).toBe(false);
  });

  it("draws the button for every other kind exactly when it can be pressed", () => {
    for (const [kind, phase] of [["add", ""], ["replace", ""], ["reclaim", "moving"], ["extract", "extracting"], ["verify", "hashing"], ["compact", "compacting"]] as const) {
      expect(showsCancel(kind, phase)).toBe(cancellable(kind, phase));
    }
  });

  it("says nothing about Explorer anywhere in its copy", () => {
    expect(JSON.stringify(dragOutCopy)).not.toContain("Awaiting");
    expect(JSON.stringify(dragOutCopy)).not.toContain("Explorer");
  });

  it("is named for what it was once it has ended, as its toast counts it", () => {
    expect(opLabel("dragout", 2)).toBe("Dragging out 2 files");
    expect(opErrorLine("dragout", 1, "file.name_refused", "The destination cannot take a name this long.")).toBe("Dragging out 1 file: The destination cannot take a name this long.");
  });

  it("never says its phase beside its name: the phase is not the user's word", () => {
    expect(opPhase("dragging")).toBe("");
    expect(opPhase("preparing")).toBe("");
    expect(opPhase("awaiting")).toBe("");
  });

  it("leaves every other kind's bar alone", () => {
    expect(hasBar("add", "")).toBe(true);
    expect(hasBar("extract", "extracting")).toBe(true);
    expect(cancellable("dragout")).toBe(false);
  });
});

// No phase word beside the verb (APP.md §6): the phase is a second line
// only where it says something the name does not.
describe("the phase beside the name", () => {
  it("drops a phase that only says the verb again", () => {
    expect(opPhase("adding")).toBe("");
    expect(opPhase("replacing")).toBe("");
    expect(opPhase("extracting")).toBe("");
    // A reclaim is made of copies, and a Compact of one; the name has
    // already said what either is for.
    expect(opPhase("compacting")).toBe("");
    expect(opPhase("moving")).toBe("");
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

// When a finished reclaim says what came back (APP.md §2.3): the file's own
// shrinking, said apart from what was moved. The words themselves are the
// copy table's and are pinned there (strings.test.ts).
describe("when a finished reclaim says what came back", () => {
  it("says what the file system got back", () => {
    const back = 1.2 * 1024 * 1024 * 1024;
    expect(reclaimedLine({ kind: "reclaim", returned: back })).toBe(reclaimedText(back));
  });

  it("says nothing for a run that gave nothing back, or ended in a cancel or a failure", () => {
    expect(reclaimedLine({ kind: "reclaim", returned: 0 })).toBe("");
    expect(reclaimedLine({ kind: "reclaim" })).toBe("");
    expect(reclaimedLine({ kind: "reclaim", error: "op.cancelled", returned: 4096 })).toBe("");
  });

  it("is nothing for any other kind", () => {
    expect(reclaimedLine({ kind: "add", returned: 4096 })).toBe("");
    expect(reclaimedLine({ kind: "compact", returned: 4096 })).toBe("");
  });
});

// A finished operation's error names the operation first — except where the
// copy already does, as the reclaim's "Saved. Reclaiming space did not
// finish." does, since a name in front would say the same thing twice.
describe("a finished operation's error line", () => {
  it("names the operation before the copy", () => {
    expect(opErrorLine("add", 3, "io", "A file could not be read or written.")).toBe("Adding 3 files: A file could not be read or written.");
    expect(opErrorLine("reclaim", 0, "op.cancelled", "Cancelled.")).toBe("Reclaiming space: Cancelled.");
  });

  it("lets the reclaim's own outcome stand alone", () => {
    expect(opErrorLine("reclaim", 0, "archive.reclaim_incomplete", "Saved. Reclaiming space did not finish.")).toBe("Saved. Reclaiming space did not finish.");
  });
});

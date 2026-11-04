import { describe, expect, it } from "vitest";
import { busyLabel, busyOp, busyOps, busyReason, cancellable, hasBar, opErrorLine, opLabel, RELEASE_INSET, RELEASE_MS, releasedInside, showsCancel, stopsFirst, stripCount } from "./ops";
import type { BusyOp } from "./ops";
import { dragOutCopy } from "./strings";

// The running operation in the foot (APP.md §2.3, §6). Since 2026-09-09 the foot says
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
    expect(opLabel("verify", 0)).toBe("Refreshing the fingerprint");
    expect(opLabel("compact", 0)).toBe("Compacting");
    expect(opLabel("rotate", 0)).toBe("Re-keying archive");
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

  // The foot shows every running operation, on the Archives page too,
  // where the verb alone does not say whose it is (ruled 2026-10-14).
  it("names the archive it is working on, when the name is known", () => {
    expect(opLabel("add", 3, "", "Photos 2024")).toBe("Adding 3 files to Photos 2024");
    expect(opLabel("add", 0, "", "Photos 2024")).toBe("Adding to Photos 2024");
    expect(opLabel("replace", 1, "", "Photos 2024")).toBe("Replacing 1 file in Photos 2024");
    expect(opLabel("extract", 12, "extracting", "Photos 2024")).toBe("Extracting 12 files from Photos 2024");
    expect(opLabel("verify", 0, "hashing", "Photos 2024")).toBe("Refreshing the fingerprint of Photos 2024");
    expect(opLabel("compact", 0, "", "Photos 2024")).toBe("Compacting Photos 2024");
    expect(opLabel("reclaim", 0, "moving", "Photos 2024")).toBe("Reclaiming space in Photos 2024");
    expect(opLabel("rotate", 0, "", "Photos 2024")).toBe("Re-keying Photos 2024");
    expect(opLabel("dragout", 2, "dragging", "Photos 2024")).toBe("Dragging 2 items from Photos 2024");
    expect(opLabel("dragout", 2, "preparing", "Photos 2024")).toBe("Extracting 2 items from Photos 2024");
  });

  it("is the verb alone when the archive's name is not known", () => {
    expect(opLabel("verify", 0, "", "")).toBe("Refreshing the fingerprint");
    expect(opLabel("rotate", 0, "")).toBe("Re-keying archive");
  });
});

// The drag out in the foot (APP.md §3, ruled 2026-09-11 after the first
// real drag, whose staging was over before the foot appeared): on the
// strip from the moment the drag starts. The hover (the label alone) says
// *Dragging N items*, since nothing is chosen yet (ruled 2026-10-14); the
// staging (the byte bar with *Cancel*) and the wait on Explorer (that same
// bar, standing full, with nothing to cancel and no words of its own) say
// *Extracting N items*. The operation ends when DoDragDrop returns, and
// the foot goes with it.
describe("the drag out in the foot", () => {
  it("counts what the gesture named, folders included, singular at one", () => {
    expect(opLabel("dragout", 2, "dragging")).toBe("Dragging 2 items");
    expect(opLabel("dragout", 2, "preparing")).toBe("Extracting 2 items");
    expect(opLabel("dragout", 2, "awaiting")).toBe("Extracting 2 items");
    expect(opLabel("dragout", 1, "dragging")).toBe("Dragging 1 item");
    expect(opLabel("dragout", 1, "awaiting")).toBe("Extracting 1 item");
    expect(dragOutCopy.dragging).toBe("Dragging");
    expect(dragOutCopy.extracting).toBe("Extracting");
  });

  it("states no outcome while the drag hovers: nothing is chosen yet", () => {
    // The user may still drop it back on the window or press Escape.
    expect(opLabel("dragout", 4, "dragging")).not.toMatch(/Extracting/);
  });

  it("counts the records the drag named, not the files beneath them", () => {
    // A folder of forty files dragged out is one item in the foot, and
    // forty files under the bar (OpView.DragItems against OpView.Items).
    expect(stripCount({ kind: "dragout", items: 40, dragItems: 1 })).toBe(1);
    expect(stripCount({ kind: "extract", items: 40, dragItems: 1 })).toBe(40);
    expect(stripCount({ kind: "add", items: 3 })).toBe(3);
    // Before the core has said — an older event, a mock that says nothing —
    // the bare verb stands rather than a wrong number.
    expect(stripCount({ kind: "dragout", items: 40 })).toBe(0);
    expect(opLabel("dragout", 0, "dragging")).toBe("Dragging");
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
    // reflow the foot at the very moment the user is watching the bar
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

  it("leaves every other kind's bar alone", () => {
    expect(hasBar("add", "")).toBe(true);
    expect(hasBar("extract", "extracting")).toBe(true);
    expect(cancellable("dragout")).toBe(false);
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

  it("is a refresh of the fingerprint and an extract (direction 4): the core stops either between steps", () => {
    expect(cancellable("verify")).toBe(true);
    expect(cancellable("extract")).toBe(true);
    expect(showsCancel("extract")).toBe(true);
  });

  it("is never a compaction or a re-key, which run to the end once started", () => {
    for (const k of ["compact", "rotate", ""]) {
      expect(cancellable(k)).toBe(false);
      expect(showsCancel(k)).toBe(false);
    }
  });
});


// A finished operation's error names the operation first — except where the
// copy already does, as the reclaim's "Saved. Reclaiming space did not
// finish." does, since a name in front would say the same thing twice.
describe("a finished operation's error line", () => {
  it("names the operation before the copy", () => {
    expect(opErrorLine("add", 3, "io", "A file could not be read or written.")).toBe("Adding 3 files: A file could not be read or written.");
  });

  it("lets the reclaim's own outcome stand alone", () => {
    expect(opErrorLine("reclaim", 0, "archive.reclaim_incomplete", "Saved. Reclaiming space did not finish.")).toBe("Saved. Reclaiming space did not finish.");
  });
});

// The busy archive (APP.md §2.3, §6; ruled 2026-11-03): what keeps one
// archive busy, which of several the page names, and the words.
describe("the busy archive", () => {
  const A = "a1".repeat(16);
  const B = "b2".repeat(16);
  const op = (id: string, kind: string, startedAt: number, extra: Partial<BusyOp> = {}): BusyOp => ({ id, kind, archiveId: A, items: 0, dragItems: 0, phase: "", startedAt, finished: false, ...extra });

  it("is idle with nothing running on the archive", () => {
    expect(busyOp([], [], A)).toBeNull();
    expect(busyOp([op("o1", "add", 5, { finished: true, items: 3 })], [], A)).toBeNull();
  });

  it("counts every kind, the core's own reclaim included, with the operation's reason", () => {
    const cases: [BusyOp, string][] = [
      [op("o", "add", 1, { items: 20 }), "Adding 20 files… wait for it to end"],
      [op("o", "extract", 1, { items: 13 }), "Extracting 13 files… wait for it to end"],
      [op("o", "replace", 1, { items: 1 }), "Replacing 1 file… wait for it to end"],
      [op("o", "compact", 1), "Compacting… wait for it to end"],
      [op("o", "reclaim", 1), "Reclaiming space… wait for it to end"],
      [op("o", "verify", 1), "Refreshing the fingerprint… wait for it to end"],
      [op("o", "rotate", 1), "Re-keying archive… wait for it to end"],
      [op("o", "dragout", 1, { phase: "preparing", dragItems: 4 }), "Extracting 4 items… wait for it to end"],
    ];
    for (const [o, why] of cases) {
      const b = busyOp([o], [], A);
      expect(b?.id, o.kind).toBe("o");
      expect(busyReason(b as BusyOp), o.kind).toBe(why);
    }
  });

  it("leaves out a drag out still in its hover, which no one has chosen yet", () => {
    expect(busyOp([op("d", "dragout", 1, { phase: "dragging", dragItems: 2 })], [], A)).toBeNull();
    expect(busyOp([op("d", "dragout", 1, { phase: "awaiting", dragItems: 2 })], [], A)?.id).toBe("d");
  });

  it("names the latest begun on this archive, never another archive's", () => {
    const ops = [op("o1", "add", 10, { items: 3 }), op("o2", "extract", 20, { items: 5 }), op("o3", "add", 30, { archiveId: B, items: 9 })];
    expect(busyOp(ops, [], A)?.id).toBe("o2");
    expect(busyOp(ops, [], B)?.id).toBe("o3");
    expect(busyOp(ops, [], "c3".repeat(16))).toBeNull();
    expect(busyOps(ops, [], A).map((o) => o.id)).toEqual(["o1", "o2"]);
  });

  it("holds an add the page started before the core has said a word of it, its count not known", () => {
    const b = busyOp([], ["s1"], A);
    expect(b).toMatchObject({ id: "s1", kind: "add", items: 0 });
    expect(busyReason(b as BusyOp)).toBe("Adding files… wait for it to end");
    expect(busyLabel(b as BusyOp, "Photos 2024")).toBe("Adding files to Photos 2024");
    // Once heard of, the operation is the one in ops, not the placeholder.
    expect(busyOp([op("s1", "add", 4, { items: 7 })], ["s1"], A)).toMatchObject({ id: "s1", items: 7 });
  });

  it("prefers what the foot can show while anything of this archive is seen running", () => {
    const ops = [op("o1", "add", 10, { items: 20 })];
    expect(busyOp(ops, ["s2"], A)?.id).toBe("o1");
    expect(busyOps(ops, ["s2"], A).map((o) => o.id)).toEqual(["o1", "s2"]);
  });

  it("says a count not planned yet as files, never as a bare verb", () => {
    expect(busyLabel(op("o", "add", 1), "Photos 2024")).toBe("Adding files to Photos 2024");
    expect(busyLabel(op("o", "extract", 1))).toBe("Extracting files");
    expect(busyLabel(op("o", "replace", 1))).toBe("Replacing files");
    expect(busyLabel(op("o", "add", 1, { items: 20 }), "Photos 2024")).toBe("Adding 20 files to Photos 2024");
    expect(busyLabel(op("o", "reclaim", 1), "Photos 2024")).toBe("Reclaiming space in Photos 2024");
  });

  it("lets Delete archive… stop what the foot can cancel, and not a compaction or a re-key", () => {
    for (const k of ["add", "replace", "extract", "reclaim", "verify"]) expect(stopsFirst(op("o", k, 1)), k).toBe(true);
    for (const k of ["compact", "rotate"]) expect(stopsFirst(op("o", k, 1)), k).toBe(false);
  });
});

// Files held over a busy list (APP.md §6, *The busy archive*; the user's
// rulings of 2026-11-03): a leave from a busy list is a refused release
// when the last dragover, or the leave itself, stood well inside the list.
describe("a leave from a busy list", () => {
  const b = { left: 100, top: 50, right: 600, bottom: 450 };
  const zero = { x: 0, y: 0 };

  it("is a release when the last dragover stood inside, though the leave says 0,0 (the real window)", () => {
    expect(releasedInside({ x: 300, y: 200 }, zero, b)).toBe(true);
    expect(releasedInside({ x: 300, y: 200 }, zero, b, null)).toBe(true);
    // Exactly the inset from an edge still counts.
    expect(releasedInside({ x: 100 + RELEASE_INSET, y: 450 - RELEASE_INSET }, zero, b)).toBe(true);
  });

  it("is a plain leave when the last dragover was near the edge and the leave outside", () => {
    expect(releasedInside({ x: 596, y: 200 }, { x: 604, y: 200 }, b)).toBe(false);
    expect(releasedInside({ x: 300, y: 54 }, { x: 300, y: 46 }, b)).toBe(false);
    expect(releasedInside({ x: 100 + RELEASE_INSET - 1, y: 200 }, zero, b)).toBe(false);
    expect(releasedInside({ x: 300, y: 450 - RELEASE_INSET + 0.5 }, zero, b)).toBe(false);
    expect(releasedInside(null, zero, b)).toBe(false);
  });

  it("is a release when the leave's own point stands inside, whatever the last dragover", () => {
    expect(releasedInside({ x: 596, y: 200 }, { x: 300, y: 200 }, b)).toBe(true);
    expect(releasedInside(null, { x: 300, y: 200 }, b)).toBe(true);
    // The leave's point on the edge or within the inset is no yes.
    expect(releasedInside(null, { x: 600, y: 200 }, b)).toBe(false);
    expect(releasedInside(null, { x: 595, y: 200 }, b)).toBe(false);
  });

  it("is nothing when a dragover came within RELEASE_MS of the leave", () => {
    expect(releasedInside({ x: 300, y: 200 }, zero, b, 50)).toBe(false);
    expect(releasedInside(null, { x: 300, y: 200 }, b, RELEASE_MS - 1)).toBe(false);
    expect(releasedInside({ x: 300, y: 200 }, zero, b, RELEASE_MS)).toBe(true);
  });
});

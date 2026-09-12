// The operation strip's pure part (APP.md §2.3, §6). Since 2026-09-09 an
// operation *is* a transaction: it begins, writes and commits at its end,
// so the strip has one job — name what is running, move a bar by the bytes
// the core reports, and offer *Cancel* where a cancel means something.
import { plural } from "./format";
import { dragOutCopy, reclaimedText } from "./strings";

// opLabel is the running operation's name, as the strip says it: the verb
// and what it is working on — *Adding 3 files*, *Adding 1 file*,
// *Extracting 12 files* — from OpView's Kind and its count (ruled
// 2026-09-09: "Adding — adding" said the same word twice). The count is
// what the operation planned (stripCount); it is zero until the plan is
// made and for every kind that counts nothing, and the bare verb stands
// until it arrives. There is no "save": nothing is staged between
// operations any more.
//
// A drag out is named by what it carries and not by where it has got to
// (APP.md §3, ruled 2026-09-11 after the first real drag, whose staging was
// over before the strip appeared): *Extracting 2 items* from the moment the
// drag starts and through all three of its phases — the hover, the staging
// and the wait on Explorer — so that a fast staging is a change inside a
// strip already on the screen and never a strip that flashes. Its count is
// the records the gesture named, folders included (OpView.DragItems,
// stripCount), not the files the extraction writes. With no phase given it
// is the finished operation's name, as its toast counts it.
export function opLabel(kind: string, items = 0, phase = ""): string {
  switch (kind) {
    case "dragout":
      if (phase !== "") return counted(dragOutCopy.extracting, items, "item");
      return counted(dragOutCopy.name, items);
    case "add":
      return counted("Adding", items);
    case "replace":
      return counted("Replacing", items);
    case "extract":
      return counted("Extracting", items);
    case "verify":
      return "Verifying";
    case "compact":
      return "Compacting";
    // The in-place compaction the core runs itself after a commit, or after
    // the last reader closes, when moving live data down into the holes
    // would give the file system enough of the tail back (APP.md §2.3,
    // FORMAT.md R40). It is named for what the user gets, not for the
    // machinery: *Reclaiming space*.
    case "reclaim":
      return "Reclaiming space";
    case "rotate":
      return "Rotating key";
  }
  return kind;
}

function counted(verb: string, items: number, unit = "file"): string {
  return items > 0 ? `${verb} ${plural(items, unit)}` : verb;
}

// stripCount is the number the strip says for an operation. Every kind
// counts the files it will write (OpView.Items) — *Adding 3 files* — but a
// drag out counts the records the gesture named, folders included
// (OpView.DragItems): a folder dragged out is one item and however many
// files beneath it, and the strip says what the user picked up, while the
// bar goes on measuring the bytes those files take (APP.md §3).
export function stripCount(o: { kind: string; items: number; dragItems?: number }): number {
  return o.kind === "dragout" ? (o.dragItems ?? 0) : o.items;
}

// opPhase is the strip's second line: the phase, but only where it says
// something the name does not (APP.md §6 — no phase word beside the verb).
// A verify's "hashing" and a rotation's "registry" are where the work has
// got to; "adding" beside *Adding 3 files* is the same word twice, and a
// reclaim's own phase — "moving", the copies it is made of — is what the
// name has already put in the user's words. "starting" is the phase every
// operation carries before it has done anything, so it is a restatement
// too: it says nothing about where the work has got to, and the strip's
// own presence already says the operation began. A drag out's phases are
// its name (opLabel), so they are never said beside it.
const restatements = new Set([
  "starting",
  "adding",
  "replacing",
  "extracting",
  "compacting",
  "compacted",
  "moving",
  "verified",
  "rotated",
  "dragging",
  "preparing",
  "awaiting",
]);

export function opPhase(phase: string | undefined): string {
  const p = (phase ?? "").trim();
  return restatements.has(p.toLowerCase()) ? "" : p;
}

// cancellable: *Cancel* is offered for an add, a replace and a reclaim
// (APP.md §2.3). Aborting an add or a replace publishes nothing and loses
// nothing — the bytes it wrote lie in extents the committed free map still
// holds free — and a reclaim is a compaction the user never asked for, so
// it is theirs to stop; the archive is untouched until it finishes. A
// compaction they did ask for, a rotation and a verify are not the user's
// to abort halfway, and an extract has already written files on disk. A
// drag out is cancellable while it is staging — the cancel fails the drop's
// request and Explorer abandons the drop — and at no other moment: during
// the hover the gesture itself is the way out, and once the bytes are
// written the drop is Explorer's, whose own window is then in front and the
// only control there is (APP.md §3, ruled 2026-09-11).
export function cancellable(kind: string, phase = ""): boolean {
  if (kind === "dragout") return phase === "preparing";
  return kind === "add" || kind === "replace" || kind === "reclaim";
}

// showsCancel is the other half of that question, and a different one:
// whether the strip draws the button at all, where cancellable says whether
// it can be pressed. They part company in one place — a drag out's awaiting,
// where the bar stands full and there is nothing left to cancel: the button
// stays where it is, greyed and unclickable, because a button that vanishes
// reflows the strip under the user's eyes and a greyed one keeps its shape
// (APP.md §3, ruled 2026-09-11). The hover draws none, the label standing
// alone there; the strip's own height does not depend on it (app.css, .op).
export function showsCancel(kind: string, phase = ""): boolean {
  if (kind === "dragout") return phase === "preparing" || phase === "awaiting";
  return cancellable(kind, phase);
}

// hasBar: whether the strip draws a bar for the operation — indeterminate
// until Total is known, and moving by bytes once it is. A drag out's hover
// is the one phase without one: nothing is being written yet, and the label
// stands alone. The bar that appears when the staging begins is ours, and
// it stays — full, once the bytes are written, for as long as Explorer is
// inside its own Drop, with no second phase and no words of its own — until
// DoDragDrop returns and the whole strip goes (APP.md §3, ruled
// 2026-09-11).
export function hasBar(kind: string, phase = ""): boolean {
  return !(kind === "dragout" && phase === "dragging");
}

// reclaimedLine is when a finished reclaim says what the file system got
// back — the file's own shrinking, OpView.Returned — apart from what was
// moved, since the two are never the same figure (APP.md §2.3): a move
// commit's own metadata can leave the file larger for a moment, and the
// bytes copied are not the bytes returned. Nothing for any other kind, and
// nothing for a run that ended before it gave anything back — a cancel or a
// failure already has its line. The words are the copy table's
// (strings.ts reclaimedText, APP.md §7); this decides whether they are said.
export function reclaimedLine(o: { kind: string; error?: string; returned?: number }): string {
  if (o.kind !== "reclaim" || o.error || !(o.returned ?? 0)) return "";
  return reclaimedText(o.returned ?? 0);
}

// opErrorLine is the toast a finished operation's error makes: the
// operation's name, then the code's copy — except where the copy already
// names the operation and its outcome in full, as the reclaim's does
// ("Saved. Reclaiming space did not finish."), where a name in front would
// say "Reclaiming space" twice.
export function opErrorLine(kind: string, items: number, code: string, text: string): string {
  if (code === "archive.reclaim_incomplete") return text;
  return `${opLabel(kind, items)}: ${text}`;
}

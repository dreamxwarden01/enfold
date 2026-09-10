// The operation strip's pure part (APP.md §2.3, §6). Since 2026-09-09 an
// operation *is* a transaction: it begins, writes and commits at its end,
// so the strip has one job — name what is running, move a bar by the bytes
// the core reports, and offer *Cancel* where a cancel means something.
import { plural } from "./format";

// opLabel is the running operation's name, as the strip says it: the verb
// and what it is working on — *Adding 3 files*, *Adding 1 file*,
// *Extracting 12 files* — from OpView's Kind and Items (ruled 2026-09-09:
// "Adding — adding" said the same word twice). Items is the count the
// operation planned; it is zero until the plan is made and for every kind
// that counts nothing, and the bare verb stands until it arrives. There is
// no "save": nothing is staged between operations any more.
export function opLabel(kind: string, items = 0): string {
  switch (kind) {
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
    // The compaction the core runs itself after a commit that leaves the
    // free space over APP.md §2.3's thresholds. It is named for what the
    // user gets, not for the machinery: *Reclaiming space*.
    case "reclaim":
      return "Reclaiming space";
    case "rotate":
      return "Rotating key";
  }
  return kind;
}

function counted(verb: string, items: number): string {
  return items > 0 ? `${verb} ${plural(items, "file")}` : verb;
}

// opPhase is the strip's second line: the phase, but only where it says
// something the name does not (APP.md §6 — no phase word beside the verb).
// A verify's "hashing" and a rotation's "registry" are where the work has
// got to; "adding" beside *Adding 3 files* is the same word twice, and a
// reclaim's own phase is the compaction it is made of, which the name has
// already put in the user's words. "starting" is the phase every operation
// carries before it has done anything, so it is a restatement too: it says
// nothing about where the work has got to, and the strip's own presence
// already says the operation began.
const restatements = new Set([
  "starting",
  "adding",
  "replacing",
  "extracting",
  "compacting",
  "compacted",
  "verified",
  "rotated",
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
// to abort halfway, and an extract has already written files on disk.
export function cancellable(kind: string): boolean {
  return kind === "add" || kind === "replace" || kind === "reclaim";
}

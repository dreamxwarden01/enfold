// The operation strip's pure part (APP.md §2.3, §6). Since 2026-09-09 an
// operation *is* a transaction: it begins, writes and commits at its end,
// so the strip has one job — name what is running, move a bar by the bytes
// the core reports, and offer *Cancel* where a cancel means something.

// opLabel is the running operation's name, as the strip says it. There is
// no "save": nothing is staged between operations any more.
export function opLabel(kind: string): string {
  switch (kind) {
    case "add":
      return "Adding";
    case "replace":
      return "Replacing";
    case "extract":
      return "Extracting";
    case "verify":
      return "Verifying";
    case "compact":
      return "Compacting";
    case "rotate":
      return "Rotating key";
  }
  return kind;
}

// cancellable: *Cancel* is offered for an add and a replace alone (APP.md
// §2.3). Aborting one of those publishes nothing and loses nothing — the
// bytes it wrote lie in extents the committed free map still holds free —
// while a compaction, a rotation or a verify is not the user's to abort
// halfway, and an extract has already written files on disk.
export function cancellable(kind: string): boolean {
  return kind === "add" || kind === "replace";
}

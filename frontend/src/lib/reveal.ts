// The recovery key's dialog, the part that decides (APP.md §6, ruled
// 2026-09-09: "None asks a second time").
//
// The key is left by one of three ways — saved to a text file the core
// writes, printed, or written down — and each of them counts at once.
// There is no second confirmation for any of them: a user set on leaving
// is not stopped by one, the key can be shown again from the Keys page,
// and at the moment it is first shown the vault holds nothing yet. After
// any of the three the corner button is *Done* and one click closes.

export interface Kept {
  // where the core wrote the text file, empty until it has
  savedTo: string;
  // *Print…* was pressed — nothing here reads a printer
  printed: boolean;
  wroteDown: boolean;
}

export function keptSomehow(k: Kept): boolean {
  return k.savedTo.trim() !== "" || k.printed || k.wroteDown;
}

// cornerLabel is the accented button in the corner. Before any of the
// three it is the third way itself, which is the one that takes no
// machinery; from the first of them on it is *Done*, and one click on it
// closes the dialog.
export function cornerLabel(k: Kept): string {
  return keptSomehow(k) ? "Done" : "I have written it down";
}

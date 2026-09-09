// An archive's compression method (FORMAT.md §7.1 bits 2–5, the ruling of
// 2026-09-08): chosen once, at creation, and carried in the record so that
// every writer of the archive compresses the same way. Store keeps every
// file as it is; at every other level already-compressed media is detected
// by sampling and stored raw by itself, per file (DESIGN.md §9).

export const METHODS = [
  { value: "store", word: "Store" },
  { value: "fastest", word: "Fast" },
  { value: "normal", word: "Normal" },
  { value: "better", word: "Better" },
  { value: "best", word: "Best" },
] as const;

export type Method = (typeof METHODS)[number]["value"];

// The level the dialog offers before anything is chosen, and the level a
// record written before this field existed is read as (an unset level is
// the writer's default).
export const DEFAULT_METHOD: Method = "normal";

// The note under the control: what Store means, and what the rest share.
export const METHOD_NOTE = "Store keeps every file as is. At every other level, already-compressed media is detected and stored raw by itself.";

// methodWord names a method as the pages show it. An empty or unknown
// word — an older record, a level this build does not name — reads as the
// default rather than as a claim about the file.
export function methodWord(method: string | undefined): string {
  return METHODS.find((m) => m.value === method)?.word ?? METHODS.find((m) => m.value === DEFAULT_METHOD)!.word;
}

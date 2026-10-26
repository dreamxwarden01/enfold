// The viewer (direction 7, Explorer's habits; APP.md §6, the Archive
// page): a file looked at large, inside the window, over the layer — the
// long look, where the pane is the quick one. Enter, a double-click and
// the row menu's *Open* open it on a file; it reads what the pane reads,
// from the same pipeline (APP.md §4: the loopback URL with no-store, the
// text through PreviewText under the pane's 64 KiB cap), and shows exactly
// what the pane can show, larger. A kind the WebView cannot show — a PDF,
// a HEIC, anything previewKind calls "none" — opens on its type icon and
// facts with *Extract…* in the bar, never a renderer in the window
// (2026-09-13). Nothing is written to disk and nothing is kept after it
// closes.
//
// Pure: which files the arrows step through, where a step lands, what a
// key means while the viewer is up. The component holds the rest.
import { previewKind } from "./format";
import type { PreviewKind } from "./format";
import type { KeyIn } from "./listkeys";

// The text cap the pane reads a text file under (APP.md §6, the pane):
// the viewer reads the same, so the long look never asks for more.
export const PREVIEW_TEXT_CAP = 64 * 1024;

export interface ViewerRow {
  id: string;
  name: string;
  isDir: boolean;
}

// sequence: the files ← and → step through, in the list's order — the
// folder's files the viewer can show, folders and "none" kinds left out;
// when every file is a "none" kind they are all stepped through, since
// each still opens on its facts. pinned is the file the viewer was opened
// on, kept in the sequence whatever its kind, so a PDF opened by Enter
// has a place to step from and back to.
export function sequence(rows: readonly ViewerRow[], pinned: string | null = null): string[] {
  const files = rows.filter((r) => !r.isDir);
  const shown = files.filter((r) => previewKind(r.name) !== "none" || r.id === pinned);
  const someShow = files.some((r) => previewKind(r.name) !== "none");
  return (someShow ? shown : files).map((r) => r.id);
}

// position is the file's place in the sequence for "4 of 13": 1-based,
// 0 when the file is not in it.
export function position(seq: readonly string[], id: string): { at: number; of: number } {
  return { at: seq.indexOf(id) + 1, of: seq.length };
}

// counter is "4 of 13" for the file shown: its place among the folder's
// files the viewer can show, never counting the pinned file — so the
// total is the folder's and does not change with where the viewer was
// opened (the final shoot of phase 4b: a PDF opened by Enter made it
// "of 7" where an image made it "of 6"). A file outside that count — a
// "none" kind opened on its facts, a waypoint the arrows step from — has
// no place in it: at is 0 and the bar shows no counter.
export function counter(rows: readonly ViewerRow[], id: string): { at: number; of: number } {
  return position(sequence(rows), id);
}

export type ViewerStep = 1 | -1 | "first" | "last";

// stepTo: where a step from `id` lands, or null when it lands nowhere new
// — the arrows stop at the ends rather than wrapping, as the list's do.
export function stepTo(seq: readonly string[], id: string, by: ViewerStep): string | null {
  if (seq.length === 0) return null;
  const at = seq.indexOf(id);
  let to: number;
  switch (by) {
    case "first":
      to = 0;
      break;
    case "last":
      to = seq.length - 1;
      break;
    default:
      if (at < 0) return null;
      to = at + by;
  }
  if (to < 0 || to >= seq.length || to === at) return null;
  return seq[to];
}

// canStep: whether the previous or next button has somewhere to go.
export function canStep(seq: readonly string[], id: string, by: 1 | -1): boolean {
  return stepTo(seq, id, by) !== null;
}

// wantsMore: a long folder pages in as the list scrolls (APP.md §3); the
// viewer steps through the rows loaded, and asks for the next page once
// it stands within NEAR_END files of the last of them.
export const NEAR_END = 3;

export function wantsMore(seq: readonly string[], id: string, more: boolean): boolean {
  if (!more) return false;
  const at = seq.indexOf(id);
  return at >= 0 && seq.length - 1 - at < NEAR_END;
}

// The stage: what the middle of the viewer shows for a file — the kind
// the pane would show, waiting while its URL or text is on its way, the
// type's facts for a kind the WebView cannot show, or the line of a code
// the core refused the preview with.
export type Stage = PreviewKind | "wait" | "refused";

export function stageOf(name: string, got: { url: string; text: boolean; refused: string }): Stage {
  const k = previewKind(name);
  if (k === "none") return "none";
  if (got.refused) return "refused";
  if (k === "text") return got.text ? "text" : "wait";
  return got.url ? k : "wait";
}

// ---- the keys while the viewer is up -----------------------------------
//
// ← → step, Home and End go to the first and the last, Esc closes and so
// does Enter (focus returns to the row), Ctrl+E is *Extract…* on the file
// shown. The page behind takes no key meanwhile (lib/listkeys.ts viewerUp).
// A control in the bar keeps its own Enter and Space; a video or an audio
// player with the focus keeps its own arrows, Home, End and Space — they
// seek and play — and Esc still closes. The text panel with the focus
// keeps its Home and End, which scroll it. A dialog over the viewer (the
// Extract… it opened) takes every key itself, and so does the rail or the
// foot when the focus is there: they stay live beside the viewer, and an
// arrow in the rail is the rail's.

export type ViewerAction = { kind: "step"; by: ViewerStep } | { kind: "close" } | { kind: "extract" } | { kind: "none" };

export interface ViewerKeyCtx {
  // onControl: a button in the viewer's bar or a nav button has the focus.
  onControl: boolean;
  // onMedia: a video or audio element (its own controls) has the focus.
  onMedia: boolean;
  // onText: the text panel has the focus.
  onText?: boolean;
  // dialogUp: a dialog stands over the viewer.
  dialogUp: boolean;
  // outside: the focus is outside the viewer (the rail, the foot) — not
  // the viewer, nor the body it falls to.
  outside?: boolean;
}

const NONE: ViewerAction = { kind: "none" };

export function viewerKey(k: KeyIn, ctx: ViewerKeyCtx): ViewerAction {
  if (ctx.dialogUp || ctx.outside) return NONE;
  const plain = !k.ctrl && !k.alt && !k.shift;
  if (k.key === "Escape" && plain) return { kind: "close" };
  if (k.ctrl && !k.alt && !k.shift && k.key.toLowerCase() === "e") return { kind: "extract" };
  if (!plain) return NONE;
  if (k.key === "Enter") return ctx.onControl ? NONE : { kind: "close" };
  if (ctx.onMedia) return NONE;
  switch (k.key) {
    case "ArrowRight":
      return { kind: "step", by: 1 };
    case "ArrowLeft":
      return { kind: "step", by: -1 };
    case "Home":
      return ctx.onText ? NONE : { kind: "step", by: "first" };
    case "End":
      return ctx.onText ? NONE : { kind: "step", by: "last" };
  }
  return NONE;
}

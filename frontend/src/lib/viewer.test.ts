import { describe, expect, it } from "vitest";
import { NEAR_END, PREVIEW_TEXT_CAP, canStep, counter, position, sequence, stageOf, stepTo, viewerKey, wantsMore } from "./viewer";
import type { ViewerKeyCtx } from "./viewer";
import type { KeyIn } from "./listkeys";

// The viewer (direction 7; APP.md §6): the long look, the pane's pipeline
// larger. Which files the arrows step through, where a step lands, and
// what a key means while it is up.

const r = (id: string, name: string, isDir = false) => ({ id, name, isDir });
const folder = [
  r("d1", "2024", true),
  r("a", "a.jpg"),
  r("p", "itinerary.pdf"),
  r("h", "IMG_7201.HEIC"),
  r("t", "notes.txt"),
  r("v", "clip.mp4"),
  r("d2", "Trips", true),
  r("m", "song.mp3"),
];

describe("the files the viewer steps through", () => {
  it("are the folder's files it can show, in the list's order, folders and none kinds left out", () => {
    expect(sequence(folder)).toEqual(["a", "t", "v", "m"]);
  });
  it("keep the file it was opened on, whatever its kind", () => {
    expect(sequence(folder, "p")).toEqual(["a", "p", "t", "v", "m"]);
    expect(sequence(folder, "a")).toEqual(["a", "t", "v", "m"]);
  });
  it("are every file when none of them can be shown", () => {
    const none = [r("d", "Trips", true), r("p", "a.pdf"), r("h", "b.heic"), r("z", "c.zip")];
    expect(sequence(none, "h")).toEqual(["p", "h", "z"]);
    expect(sequence([])).toEqual([]);
  });
  it("say where the file stands among them, 1-based", () => {
    const seq = sequence(folder);
    expect(position(seq, "t")).toEqual({ at: 2, of: 4 });
    expect(position(seq, "p")).toEqual({ at: 0, of: 4 });
  });
  it("count the same total wherever the viewer was opened, a pinned none kind left uncounted", () => {
    expect(counter(folder, "a")).toEqual({ at: 1, of: 4 });
    expect(counter(folder, "t")).toEqual({ at: 2, of: 4 });
    expect(counter(folder, "p")).toEqual({ at: 0, of: 4 });
  });
  it("count every file when none of them can be shown", () => {
    const none = [r("d", "Trips", true), r("p", "a.pdf"), r("h", "b.heic")];
    expect(counter(none, "h")).toEqual({ at: 2, of: 2 });
  });
});

describe("a step", () => {
  const seq = ["a", "t", "v", "m"];
  it("goes one either way and stops at the ends", () => {
    expect(stepTo(seq, "t", 1)).toBe("v");
    expect(stepTo(seq, "t", -1)).toBe("a");
    expect(stepTo(seq, "a", -1)).toBeNull();
    expect(stepTo(seq, "m", 1)).toBeNull();
  });
  it("goes to the first and the last with Home and End, and nowhere when already there", () => {
    expect(stepTo(seq, "v", "first")).toBe("a");
    expect(stepTo(seq, "t", "last")).toBe("m");
    expect(stepTo(seq, "a", "first")).toBeNull();
    expect(stepTo(seq, "m", "last")).toBeNull();
  });
  it("from a file not among them goes nowhere by one, and to the ends by Home and End", () => {
    expect(stepTo(seq, "x", 1)).toBeNull();
    expect(stepTo(seq, "x", "first")).toBe("a");
    expect(stepTo([], "a", "first")).toBeNull();
  });
  it("greys the buttons at the ends", () => {
    expect(canStep(seq, "a", -1)).toBe(false);
    expect(canStep(seq, "a", 1)).toBe(true);
    expect(canStep(seq, "m", 1)).toBe(false);
  });
});

describe("a long folder", () => {
  const seq = ["1", "2", "3", "4", "5", "6"];
  it("asks for its next page within NEAR_END files of the last row loaded, and only when there is one", () => {
    expect(NEAR_END).toBe(3);
    expect(wantsMore(seq, "3", true)).toBe(false);
    expect(wantsMore(seq, "4", true)).toBe(true);
    expect(wantsMore(seq, "6", true)).toBe(true);
    expect(wantsMore(seq, "6", false)).toBe(false);
    expect(wantsMore(seq, "x", true)).toBe(false);
  });
});

describe("the stage", () => {
  it("reads text under the pane's own cap", () => {
    expect(PREVIEW_TEXT_CAP).toBe(64 * 1024);
  });
  it("shows a kind once its URL or text is in, and waits until then", () => {
    expect(stageOf("a.jpg", { url: "", text: false, refused: "" })).toBe("wait");
    expect(stageOf("a.jpg", { url: "http://127.0.0.1/p/x", text: false, refused: "" })).toBe("image");
    expect(stageOf("clip.mp4", { url: "u", text: false, refused: "" })).toBe("video");
    expect(stageOf("song.mp3", { url: "u", text: false, refused: "" })).toBe("audio");
    expect(stageOf("notes.txt", { url: "", text: false, refused: "" })).toBe("wait");
    expect(stageOf("notes.txt", { url: "", text: true, refused: "" })).toBe("text");
  });
  it("opens a kind the WebView cannot show on its facts — never a renderer (2026-09-13)", () => {
    for (const n of ["itinerary.pdf", "IMG_7201.HEIC", "backup.zip"]) expect(stageOf(n, { url: "u", text: true, refused: "" })).toBe("none");
  });
  it("says a refusal in place of the file", () => {
    expect(stageOf("a.jpg", { url: "", text: false, refused: "The archive is not open" })).toBe("refused");
  });
});

describe("the viewer's keys", () => {
  const k = (key: string, m: Partial<Omit<KeyIn, "key">> = {}): KeyIn => ({ key, ctrl: false, shift: false, alt: false, ...m });
  const at: ViewerKeyCtx = { onControl: false, onMedia: false, dialogUp: false };
  it("step, close and extract", () => {
    expect(viewerKey(k("ArrowRight"), at)).toEqual({ kind: "step", by: 1 });
    expect(viewerKey(k("ArrowLeft"), at)).toEqual({ kind: "step", by: -1 });
    expect(viewerKey(k("Home"), at)).toEqual({ kind: "step", by: "first" });
    expect(viewerKey(k("End"), at)).toEqual({ kind: "step", by: "last" });
    expect(viewerKey(k("Escape"), at)).toEqual({ kind: "close" });
    expect(viewerKey(k("Enter"), at)).toEqual({ kind: "close" });
    expect(viewerKey(k("e", { ctrl: true }), at)).toEqual({ kind: "extract" });
    expect(viewerKey(k("E", { ctrl: true }), at)).toEqual({ kind: "extract" });
  });
  it("leave a control its Enter, and a player its arrows, Home and End, but Esc always closes", () => {
    expect(viewerKey(k("Enter"), { ...at, onControl: true }).kind).toBe("none");
    for (const key of ["ArrowRight", "ArrowLeft", "Home", "End", " "]) expect(viewerKey(k(key), { ...at, onMedia: true }).kind).toBe("none");
    expect(viewerKey(k("Escape"), { ...at, onMedia: true }).kind).toBe("close");
    expect(viewerKey(k("Escape"), { ...at, onControl: true }).kind).toBe("close");
  });
  it("leave the text panel its Home and End, which scroll it, but step with its arrows", () => {
    expect(viewerKey(k("Home"), { ...at, onText: true }).kind).toBe("none");
    expect(viewerKey(k("End"), { ...at, onText: true }).kind).toBe("none");
    expect(viewerKey(k("ArrowRight"), { ...at, onText: true })).toEqual({ kind: "step", by: 1 });
  });
  it("take nothing while the focus is in the rail or the foot beside the viewer", () => {
    for (const key of ["ArrowRight", "ArrowLeft", "Home", "End", "Enter", "Escape"]) expect(viewerKey(k(key), { ...at, outside: true }).kind).toBe("none");
    expect(viewerKey(k("e", { ctrl: true }), { ...at, outside: true }).kind).toBe("none");
  });
  it("take nothing under a dialog, and nothing with a modifier but Ctrl+E", () => {
    for (const key of ["Escape", "ArrowRight", "Enter"]) expect(viewerKey(k(key), { ...at, dialogUp: true }).kind).toBe("none");
    expect(viewerKey(k("e", { ctrl: true }), { ...at, dialogUp: true }).kind).toBe("none");
    expect(viewerKey(k("ArrowRight", { shift: true }), at).kind).toBe("none");
    expect(viewerKey(k("ArrowRight", { alt: true }), at).kind).toBe("none");
    expect(viewerKey(k("Escape", { ctrl: true }), at).kind).toBe("none");
    expect(viewerKey(k(" "), at).kind).toBe("none");
  });
});

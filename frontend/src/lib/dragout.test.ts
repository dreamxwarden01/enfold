import { describe, expect, it } from "vitest";
import { DRAG_THRESHOLD, answer, baseName, beginFlight, land, ownDrop, ownNames, pastThreshold, underFolder } from "./dragout";
import type { Flight, Landing } from "./dragout";
import { ROOT_ID } from "./tree";

// The one gesture (APP.md §3, §6, ruled 2026-09-11): a press-and-move over
// selected rows starts the native drag; a release over Enfold's own window
// is a self-drop the page turns into the Move it always was, by the ids it
// kept in flight; a release anywhere else is the drag out proper.

const y2024 = "aa".repeat(16);
const trips = "bb".repeat(16);
const photo = "cc".repeat(16);
const other = "dd".repeat(16);
// The drag's folder as Shell.DragOut answers it — the manifest at its top —
// and the items folder beneath it, where every path handed out lies.
const STAGING = "C:\\Users\\me\\AppData\\Local\\Enfold\\drag\\1a2b3c4d";
const ITEMS = `${STAGING}\\items`;

const folderRow = (id: string): Landing => ({ id, at: "row", isDir: true });
const fileRow = (id: string): Landing => ({ id, at: "row", isDir: false });
const up = (id: string): Landing => ({ id, at: "up", isDir: true });
const head: Landing = { id: ROOT_ID, at: "head", isDir: true };

describe("when a press becomes a drag", () => {
  it("is past four pixels on either axis, Explorer's own threshold", () => {
    expect(DRAG_THRESHOLD).toBe(4);
    expect(pastThreshold(5, 0)).toBe(true);
    expect(pastThreshold(0, -5)).toBe(true);
    expect(pastThreshold(-5, 5)).toBe(true);
  });

  it("is not a tremor inside it: a click stays a click", () => {
    expect(pastThreshold(0, 0)).toBe(false);
    expect(pastThreshold(4, 4)).toBe(false);
    expect(pastThreshold(-4, 3)).toBe(false);
  });
});

describe("a self-drop", () => {
  const flight: Flight = beginFlight([photo, other], y2024);

  it("landed on a folder row is a Move of the ids in flight into it", () => {
    const a = land(flight, folderRow(trips));
    expect(a.verdict).toEqual({ kind: "wait" }); // the call has not answered yet
    const b = answer(a.flight, { selfDrop: true, folder: STAGING }, "op7");
    expect(b.verdict).toEqual({ kind: "move", move: { ids: [photo, other], to: trips, at: "row" } });
    expect(b.flight.opId).toBe("op7");
  });

  it("landed on the `..` row moves up one level, and on the heading to the root", () => {
    const answered = answer(flight, { selfDrop: true, folder: STAGING }).flight;
    expect(land(answered, up(ROOT_ID)).verdict).toEqual({ kind: "move", move: { ids: [photo, other], to: ROOT_ID, at: "up" } });
    const deeper = answer(beginFlight([photo], trips), { selfDrop: true, folder: STAGING }).flight;
    expect(land(deeper, head).verdict).toEqual({ kind: "move", move: { ids: [photo], to: ROOT_ID, at: "head" } });
  });

  it("is the same Move whichever half arrives first", () => {
    const dropFirst = answer(land(flight, folderRow(trips)).flight, { selfDrop: true, folder: STAGING });
    const answerFirst = land(answer(flight, { selfDrop: true, folder: STAGING }).flight, folderRow(trips));
    expect(dropFirst.verdict).toEqual(answerFirst.verdict);
    expect(dropFirst.verdict.kind).toBe("move");
  });

  it("waits for the drop when the call answered first", () => {
    expect(answer(flight, { selfDrop: true, folder: STAGING }).verdict).toEqual({ kind: "wait" });
  });

  it("landed elsewhere on the page is nothing", () => {
    const a = land(flight, "elsewhere");
    expect(a.verdict).toEqual({ kind: "wait" });
    expect(answer(a.flight, { selfDrop: true, folder: STAGING }).verdict).toEqual({ kind: "nothing" });
  });

  it("landed where a Move means nothing is nothing", () => {
    const answered = answer(flight, { selfDrop: true, folder: STAGING }).flight;
    // a file row is not a place
    expect(land(answered, fileRow(trips)).verdict).toEqual({ kind: "nothing" });
    // the folder the rows are already in
    expect(land(answered, folderRow(y2024)).verdict).toEqual({ kind: "nothing" });
    expect(land(answered, up(y2024)).verdict).toEqual({ kind: "nothing" });
    // a folder dropped on itself
    const self = answer(beginFlight([trips], y2024), { selfDrop: true, folder: STAGING }).flight;
    expect(land(self, folderRow(trips)).verdict).toEqual({ kind: "nothing" });
  });

  it("counts the first landing only", () => {
    const first = land(flight, folderRow(trips)).flight;
    const second = land(first, "elsewhere").flight;
    expect(second.landed).toEqual(folderRow(trips));
  });
});

describe("the drag out proper", () => {
  it("has nothing for the page to do once the call answers: the strip follows the operation", () => {
    const f = beginFlight([photo], y2024);
    expect(answer(f, { selfDrop: false, folder: STAGING }, "op3").verdict).toEqual({ kind: "nothing" });
  });

  it("performs no Move even when a stray drop reached a folder row", () => {
    // The button came up outside the window; a landing here is not the
    // WebView's own drop.
    const f = land(beginFlight([photo], y2024), folderRow(trips)).flight;
    expect(answer(f, { selfDrop: false, folder: STAGING }).verdict).toEqual({ kind: "nothing" });
  });
});

describe("the ids in flight", () => {
  it("are copied, never shared with the selection that made them", () => {
    const ids = [photo];
    const names = ["IMG_7201.HEIC"];
    const f = beginFlight(ids, y2024, names);
    ids.push(other);
    names.push("b.jpg");
    expect(f.ids).toEqual([photo]);
    expect(f.names).toEqual(["IMG_7201.HEIC"]);
  });
});

// Whose drop it is (the outside review's finding 3): a self-drop answered
// over the window frame stays in flight a moment for its landing, and a
// real Explorer drop in that moment is the add as always — never the
// landing that moves the previous selection. The WebView's drop is told
// apart by its names: one File per id at the top of the staging folder's
// items, a folder record as its name.
describe("whether the WebView's drop is our own", () => {
  const flight = beginFlight([photo, other], y2024, ["IMG_7201.HEIC", "Trips"]);

  it("is ours when it names the flight's items, a folder as its name, in any order or case", () => {
    expect(ownNames(["IMG_7201.HEIC", "Trips"], flight)).toBe(true);
    expect(ownNames(["trips", "img_7201.heic"], flight)).toBe(true);
  });

  it("is foreign when the names are someone else's", () => {
    expect(ownNames(["a.jpg", "b.jpg"], flight)).toBe(false);
  });

  it("is foreign when one of ours is missing, or the count differs", () => {
    expect(ownNames(["IMG_7201.HEIC", "b.jpg"], flight)).toBe(false);
    expect(ownNames(["IMG_7201.HEIC"], flight)).toBe(false);
    expect(ownNames(["IMG_7201.HEIC", "Trips", "b.jpg"], flight)).toBe(false);
    expect(ownNames([], flight)).toBe(false);
  });

  it("judges an id the page could not name by the count alone", () => {
    // a whole folder taken by Ctrl+A past the loaded rows
    const partly = beginFlight([photo, other], y2024, ["IMG_7201.HEIC"]);
    expect(ownNames(["IMG_7201.HEIC", "whatever"], partly)).toBe(true);
    expect(ownNames(["IMG_7201.HEIC"], partly)).toBe(false);
    expect(ownNames(["a.jpg", "b.jpg"], partly)).toBe(false);
  });
});

describe("a foreign drop while the ids are in flight", () => {
  it("ends a self-drop's grace as nothing: the drop goes on being the add", () => {
    const f = answer(beginFlight([photo], y2024, ["IMG_7201.HEIC"]), { selfDrop: true, folder: STAGING }).flight;
    const r = land(f, "foreign");
    expect(r.verdict).toEqual({ kind: "nothing" });
    expect(r.flight.landed).toBe("foreign");
  });

  it("ends the flight as nothing even before the call has answered", () => {
    const r = land(beginFlight([photo], y2024, ["IMG_7201.HEIC"]), "foreign");
    expect(r.verdict).toEqual({ kind: "nothing" });
    expect(answer(r.flight, { selfDrop: true, folder: STAGING }).verdict).toEqual({ kind: "nothing" });
  });
});

// The shell's drop (APP.md §3): a self-drop's paths lie under the drag's
// folder, beneath its items, and the files never exist, so they are never
// an add; real files dropped in from Explorer go on being the add of §3.
describe("whether a shell drop is our own", () => {
  const flight = beginFlight([photo, other], y2024, ["IMG_7201.HEIC", "Trips"]);
  const answered = answer(flight, { selfDrop: true, folder: STAGING }).flight;

  it("is ours while the answered flight waits for its landing when the paths lie under the drag's folder", () => {
    expect(ownDrop([`${ITEMS}\\IMG_7201.HEIC`, `${ITEMS}\\Trips`], answered, STAGING)).toBe(true);
  });

  it("is a real file's when the paths lie elsewhere, a flight in its grace notwithstanding", () => {
    expect(ownDrop(["D:\\Pictures\\a.jpg", "D:\\Pictures\\b.jpg"], answered, STAGING)).toBe(false);
    // even a drop of the same names, from somewhere else
    expect(ownDrop(["D:\\Pictures\\IMG_7201.HEIC", "D:\\Pictures\\Trips"], answered, STAGING)).toBe(false);
  });

  it("is told by its names before the call has said where the folder is", () => {
    expect(ownDrop([`${ITEMS}\\IMG_7201.HEIC`, `${ITEMS}\\Trips`], flight, "")).toBe(true);
    expect(ownDrop(["c:/users/me/appdata/local/enfold/drag/1a2b3c4d/items/img_7201.heic", `${ITEMS}/Trips`], flight, "")).toBe(true);
    expect(ownDrop(["D:\\Pictures\\a.jpg", "D:\\Pictures\\b.jpg"], flight, "")).toBe(false);
    expect(ownDrop([`${ITEMS}\\IMG_7201.HEIC`], flight, "")).toBe(false);
  });

  it("is ours after the flight when every path lies under the folder the last drag named", () => {
    expect(ownDrop([`${ITEMS}\\IMG_7201.HEIC`, `${ITEMS}\\Trips`], null, STAGING)).toBe(true);
  });

  it("is foreign when no drag is in flight and the paths lie elsewhere", () => {
    expect(ownDrop(["D:\\Pictures\\a.jpg"], null, STAGING)).toBe(false);
    expect(ownDrop(["D:\\Pictures\\a.jpg"], null, "")).toBe(false);
    // one foreign path among ours is not ours
    expect(ownDrop([`${ITEMS}\\IMG_7201.HEIC`, "D:\\Pictures\\b.jpg"], null, STAGING)).toBe(false);
  });

  it("is nothing for no paths at all", () => {
    expect(ownDrop([], null, STAGING)).toBe(false);
    expect(ownDrop([], answered, STAGING)).toBe(false);
  });
});

describe("a path's last name", () => {
  it("is the segment after the last separator of either kind, or the whole of a bare name", () => {
    expect(baseName(`${ITEMS}\\IMG_7201.HEIC`)).toBe("IMG_7201.HEIC");
    expect(baseName("c:/x/y/Trips")).toBe("Trips");
    expect(baseName("Trips")).toBe("Trips");
  });
});

describe("a path under the staging folder", () => {
  it("is compared the way the core compares paths: case folded, either slash", () => {
    expect(underFolder(`${ITEMS}\\a.jpg`, STAGING)).toBe(true);
    expect(underFolder("c:/users/ME/AppData/Local/Enfold/drag/1A2B3C4D/items/a.jpg", STAGING)).toBe(true);
    expect(underFolder(`${STAGING}\\`, STAGING)).toBe(false);
  });

  it("is not the folder itself, a sibling, or a folder whose name merely begins the same way", () => {
    expect(underFolder(STAGING, STAGING)).toBe(false);
    expect(underFolder(`${STAGING}0\\a.jpg`, STAGING)).toBe(false);
    expect(underFolder("C:\\Users\\me\\AppData\\Local\\Enfold\\drag\\ffffffff\\a.jpg", STAGING)).toBe(false);
  });

  it("is nothing for an empty path or folder", () => {
    expect(underFolder("", STAGING)).toBe(false);
    expect(underFolder(`${STAGING}\\a.jpg`, "")).toBe(false);
  });
});

import { describe, expect, it } from "vitest";
import { ROOT_ID, canDrop, countPhrase, deleteBody, deleteCounts, deleteTitle, dropVerdict, isRootId, retryChain, shownDir, wentName } from "./tree";

const root = { id: ROOT_ID, name: "Photos 2024" };
const y2024 = { id: "aa".repeat(16), name: "2024" };
const trips = { id: "bb".repeat(16), name: "Trips" };
const held = [root, y2024, trips];

describe("the root's spelling", () => {
  it("is the all-zero id, 32 hex digits", () => {
    expect(ROOT_ID).toHaveLength(32);
    expect(ROOT_ID).toMatch(/^0+$/);
    expect(isRootId(ROOT_ID)).toBe(true);
    expect(isRootId(y2024.id)).toBe(false);
  });
});

describe("the walk a folder that went asks for", () => {
  it("walks the crumbs upwards from just above the folder shown", () => {
    expect(retryChain(held, trips.id)).toEqual([y2024.id, ROOT_ID]);
    expect(retryChain(held, y2024.id)).toEqual([ROOT_ID]);
  });

  it("ends at the root, which always answers", () => {
    expect(retryChain(held, trips.id).at(-1)).toBe(ROOT_ID);
    expect(retryChain([], "cc".repeat(16))).toEqual([ROOT_ID]);
  });

  it("has nothing left to try when the root itself was asked for", () => {
    expect(retryChain(held, ROOT_ID)).toEqual([]);
    expect(retryChain([root], ROOT_ID)).toEqual([]);
  });

  it("takes the whole chain when the crumbs do not name the folder", () => {
    // The page stepped into a folder row and never got a listing for it:
    // everything it holds is above that folder.
    expect(retryChain(held, "cc".repeat(16))).toEqual([trips.id, y2024.id, ROOT_ID]);
  });

  it("names the folder that went when the crumbs know it", () => {
    expect(wentName(held, trips.id)).toBe("Trips");
    expect(wentName(held, "cc".repeat(16))).toBe("");
  });
});

describe("the folder the page stands in", () => {
  it("is the last crumb of the listing on screen", () => {
    expect(shownDir(held)).toBe(trips.id);
    expect(shownDir([root])).toBe(ROOT_ID);
  });

  it("is the root when no listing arrived at all", () => {
    // A listing that failed for anything but file.not_found leaves the
    // page showing what it showed before; the folder id every write names
    // goes back with it, never staying on the folder never entered.
    expect(shownDir(null)).toBe(ROOT_ID);
    expect(shownDir(undefined)).toBe(ROOT_ID);
    expect(shownDir([])).toBe(ROOT_ID);
  });
});

describe("what a drag may be dropped on", () => {
  const drag = { ids: [trips.id, "dd".repeat(16)], from: y2024.id };

  it("moves onto another folder", () => {
    expect(dropVerdict({ id: "ee".repeat(16), isDir: true }, drag)).toBe("move");
    expect(canDrop({ id: "ee".repeat(16), isDir: true }, drag)).toBe(true);
  });

  it("moves onto a crumb above", () => {
    expect(dropVerdict({ id: ROOT_ID, isDir: true }, drag)).toBe("move");
  });

  it("is a no-op onto the folder the rows are already in", () => {
    // The last crumb, and the row's own folder, are the same place.
    expect(dropVerdict({ id: y2024.id, isDir: true }, drag)).toBe("here");
    expect(canDrop({ id: y2024.id, isDir: true }, drag)).toBe(false);
  });

  it("refuses a folder dropped on itself", () => {
    expect(dropVerdict({ id: trips.id, isDir: true }, drag)).toBe("self");
  });

  it("refuses a row staged for deletion: nothing may be moved beneath a tombstone", () => {
    expect(dropVerdict({ id: "ee".repeat(16), isDir: true, pending: "deleted" }, drag)).toBe("deleted");
  });

  it("refuses a file row: it is not a place", () => {
    expect(dropVerdict({ id: "ee".repeat(16), isDir: false }, drag)).toBe("file");
    expect(dropVerdict({ id: "ee".repeat(16), isDir: false, pending: "added" }, drag)).toBe("file");
  });

  it("does nothing for an empty drag", () => {
    expect(dropVerdict({ id: "ee".repeat(16), isDir: true }, { ids: [], from: y2024.id })).toBe("nothing");
    expect(canDrop({ id: "ee".repeat(16), isDir: true }, null)).toBe(false);
  });

  it("leaves a descendant the page cannot see to the core", () => {
    // A folder dragged onto a folder that is inside it, listed nowhere on
    // this page: nothing here refuses it, and Move answers
    // file.move_into_self.
    expect(dropVerdict({ id: "ff".repeat(16), isDir: true }, drag)).toBe("move");
  });
});

describe("the delete confirmation", () => {
  const file = { isDir: false, name: "notes.md" };
  const other = { isDir: false, name: "budget.csv" };
  const folder = { isDir: true, name: "Trips" };

  it("counts files and folders apart", () => {
    expect(deleteCounts([file, other, folder])).toEqual({ files: 2, folders: 1 });
    expect(deleteCounts([])).toEqual({ files: 0, folders: 0 });
  });

  it("names them separately", () => {
    expect(countPhrase({ files: 1, folders: 0 })).toBe("1 file");
    expect(countPhrase({ files: 0, folders: 2 })).toBe("2 folders");
    expect(countPhrase({ files: 2, folders: 1 })).toBe("2 files and 1 folder");
    expect(countPhrase({ files: 0, folders: 0 })).toBe("");
  });

  it("names the one thing when there is one", () => {
    expect(deleteTitle([file])).toBe('Delete "notes.md"?');
    expect(deleteTitle([folder])).toBe('Delete "Trips"?');
    expect(deleteTitle([file, folder])).toBe("Delete 1 file and 1 folder?");
  });

  it("says a folder takes everything beneath it, and only then", () => {
    expect(deleteBody([file])).not.toMatch(/beneath/);
    expect(deleteBody([folder])).toMatch(/takes everything beneath it/);
    expect(deleteBody([folder, { isDir: true, name: "Bills" }])).toMatch(/Each folder/);
  });
});

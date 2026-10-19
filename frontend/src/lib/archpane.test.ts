import { describe, expect, it } from "vitest";
import { folderFigures, paneShape, selectionFigures } from "./archpane";
import { archivePageCopy, archivesCopy, lockCopy, paneCopy, typePlural } from "./strings";

// The Archive page's pane (direction 3, phase 4a): what it counts at rest
// and with a selection, and how it words a file it cannot show.

const row = (id: string, size: number) => ({ id, size });

describe("folderFigures", () => {
  it("counts the folder and adds up its bytes once every row is on hand", () => {
    expect(folderFigures({ rows: [row("a", 1000), row("b", 2000), row("c", 6500)], total: 3 })).toEqual({ count: 3, bytes: 9500 });
  });

  it("says the count alone while a long folder is still paging in", () => {
    expect(folderFigures({ rows: [row("a", 1000)], total: 1200 })).toEqual({ count: 1200, bytes: null });
  });

  it("is an empty folder, and nothing before the listing", () => {
    expect(folderFigures({ rows: [], total: 0 })).toEqual({ count: 0, bytes: 0 });
    expect(folderFigures({ rows: null, total: 0 })).toEqual({ count: 0, bytes: 0 });
    expect(folderFigures(null)).toEqual({ count: 0, bytes: null });
  });

  it("takes a size it cannot read as nothing", () => {
    expect(folderFigures({ rows: [row("a", Number.NaN), row("b", -1), row("c", 5)], total: 3 })).toEqual({ count: 3, bytes: 5 });
  });
});

describe("selectionFigures", () => {
  const rows = [row("a", 100), row("b", 200), row("c", 300)];

  it("counts the selection and its bytes", () => {
    expect(selectionFigures(rows, new Set(["a", "c"]))).toEqual({ count: 2, bytes: 400 });
  });

  it("says the count alone when the selection holds rows not loaded", () => {
    expect(selectionFigures(rows, new Set(["a", "z"]))).toEqual({ count: 2, bytes: null });
  });
});

describe("paneShape", () => {
  it("is at rest with nothing selected, one with one row on hand, several otherwise", () => {
    expect(paneShape(0, false)).toBe("rest");
    expect(paneShape(1, true)).toBe("one");
    expect(paneShape(1, false)).toBe("several");
    expect(paneShape(4, false)).toBe("several");
  });
});

describe("the pane's words", () => {
  it("counts items and their size", () => {
    expect(paneCopy.figures(3, 9523)).toBe("3 items · 9.3 KB");
    expect(paneCopy.figures(1, 0)).toBe("1 item · 0 B");
    expect(paneCopy.figures(1204, null)).toBe("1,204 items");
    expect(paneCopy.figures(0, 0)).toBe("0 items");
  });

  it("offers the selection's extract by its count", () => {
    expect(paneCopy.extractSome(4)).toBe("Extract 4 items…");
    expect(paneCopy.extractSome(1)).toBe("Extract 1 item…");
  });

  it("says a missing preview as a fact of the file's type, never as a cure", () => {
    expect(paneCopy.noPreview("itinerary.pdf")).toBe("No preview for PDF documents");
    expect(paneCopy.noPreview("IMG_7201.HEIC")).toBe("No preview for HEIC images");
    expect(paneCopy.noPreview("disk.iso")).toBe("No preview for disc images");
    expect(paneCopy.noPreview("main.go")).toBe("No preview for Go source files");
    expect(paneCopy.noPreview("song.mp3")).toBe("No preview for MP3 audio files");
    expect(paneCopy.noPreview("x.dll")).toBe("No preview for library files");
    expect(paneCopy.noPreview("x.xyz")).toBe("No preview for XYZ files");
    expect(paneCopy.noPreview("README")).toBe("No preview for this file");
    expect(paneCopy.previewFailed).not.toMatch(/extract/i);
    for (const n of ["a.pdf", "b.heic", "c.zip", "README"]) expect(paneCopy.noPreview(n)).not.toMatch(/extract/i);
  });

  it("puts a type in the plural as a sentence says it", () => {
    expect(typePlural("Word document")).toBe("Word documents");
    expect(typePlural("Windows installer")).toBe("Windows installers");
    expect(typePlural("JSON data")).toBe("JSON data files");
    expect(typePlural("Application")).toBe("applications");
  });

  it("words the foot, the empty folder and the getting-there lines", () => {
    expect(archivePageCopy.foot("Trips", 3)).toBe("Trips · 3 items");
    expect(archivePageCopy.foot("Trips", 1)).toBe("Trips · 1 item");
    expect(archivePageCopy.emptyTitle("Trips")).toBe("Trips is empty");
    expect(archivePageCopy.emptyTitle("")).toBe("This folder is empty");
    expect(archivePageCopy.folded(3)).toBe("3 folders in between");
    expect(archivesCopy.enterOpens("Photos 2024")).toBe("Enter opens Photos 2024");
    expect(lockCopy.unlockToOpen).toBe("Unlock to open");
    expect(lockCopy.forgetOpen("photos-2024.efd")).toBe("Do not open photos-2024.efd");
  });
});

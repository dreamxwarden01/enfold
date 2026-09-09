import { describe, expect, it } from "vitest";
import { appendArchiveFolder, archiveFolderName, destinationEmpty, lastSegment } from "./extract";

// The extract dialog's destination field (APP.md §3): one editable path,
// *Browse…*, and the one action that appends a folder named after the
// archive — the native picker cannot prefill the name of a folder the user
// makes, so the field does what WinRAR's destination field does.
describe("the folder named after the archive", () => {
  it("is the archive's name", () => {
    expect(archiveFolderName("Photos 2024")).toBe("Photos 2024");
  });

  it("drops the nine characters a path element cannot hold", () => {
    expect(archiveFolderName('a\\b/c:d*e?f"g<h>i|j')).toBe("abcdefghij");
    expect(archiveFolderName("ECON 280: notes")).toBe("ECON 280 notes");
  });

  it("drops a trailing dot or space, which the shell would strip in silence", () => {
    expect(archiveFolderName("Backups. ")).toBe("Backups");
    expect(archiveFolderName("  Tax returns  ")).toBe("Tax returns");
  });

  it("is empty when nothing usable is left", () => {
    expect(archiveFolderName("///")).toBe("");
    expect(archiveFolderName("...")).toBe("");
    expect(archiveFolderName("")).toBe("");
  });
});

describe("appending it to the destination", () => {
  it("adds it with a backslash", () => {
    expect(appendArchiveFolder("D:\\Extracted", "Photos 2024")).toBe("D:\\Extracted\\Photos 2024");
  });

  it("does not double the separator the destination already ends with", () => {
    expect(appendArchiveFolder("D:\\Extracted\\", "Photos 2024")).toBe("D:\\Extracted\\Photos 2024");
    expect(appendArchiveFolder("D:/Extracted/", "Photos 2024")).toBe("D:/Extracted\\Photos 2024");
  });

  it("sanitises the name it appends", () => {
    expect(appendArchiveFolder("D:\\Extracted", "Tax: 2016/2024")).toBe("D:\\Extracted\\Tax 20162024");
  });

  it("does nothing a second time: one press is one folder", () => {
    const once = appendArchiveFolder("D:\\Extracted", "Photos 2024");
    expect(appendArchiveFolder(once, "Photos 2024")).toBe(once);
    // and whatever the case the user typed it in
    expect(appendArchiveFolder("D:\\Extracted\\photos 2024", "Photos 2024")).toBe("D:\\Extracted\\photos 2024");
  });

  it("leaves an empty destination alone: a bare name would be a relative path", () => {
    expect(appendArchiveFolder("", "Photos 2024")).toBe("");
    expect(appendArchiveFolder("   ", "Photos 2024")).toBe("   ");
  });

  it("leaves the destination alone when the archive's name sanitises away", () => {
    expect(appendArchiveFolder("D:\\Extracted", "??")).toBe("D:\\Extracted");
  });
});

describe("the destination itself", () => {
  it("is required, and nothing more is judged of it — the core creates it", () => {
    expect(destinationEmpty("")).toBe(true);
    expect(destinationEmpty("   ")).toBe(true);
    expect(destinationEmpty("D:\\Somewhere that is not there yet")).toBe(false);
  });

  it("reads its own last folder either way round", () => {
    expect(lastSegment("D:\\Extracted\\Photos")).toBe("Photos");
    expect(lastSegment("D:/Extracted/Photos/")).toBe("Photos");
    expect(lastSegment("D:\\")).toBe("D:");
  });
});

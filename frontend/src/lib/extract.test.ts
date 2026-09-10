import { describe, expect, it } from "vitest";
import {
  DEFAULT_POLICY, POLICIES, appendArchiveFolder, archiveFolderName, destinationEmpty,
  destinationFor, parentFolder,
} from "./extract";

// The extract dialog's destination field (APP.md §3, ruled 2026-09-10):
// one editable path, *Browse…*, and a prefill that comes from the archive's
// own folder rather than from anything kept about the last time.
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

  it("is always one level more, even when the destination already ends in that name", () => {
    // The "not twice" exception belonged to the old "+ a folder" button,
    // which could be pressed twice; the prefill rule runs once and always
    // adds the level (APP.md §3, ruled 2026-09-10).
    expect(appendArchiveFolder("D:\\Extracted\\Photos 2024", "Photos 2024")).toBe("D:\\Extracted\\Photos 2024\\Photos 2024");
  });

  it("leaves an empty destination alone: a bare name would be a relative path", () => {
    expect(appendArchiveFolder("", "Photos 2024")).toBe("");
    expect(appendArchiveFolder("   ", "Photos 2024")).toBe("   ");
  });

  it("leaves the destination alone when the archive's name sanitises away", () => {
    expect(appendArchiveFolder("D:\\Extracted", "??")).toBe("D:\\Extracted");
  });
});

// The archive's own folder, from the record's path.
describe("the archive's folder", () => {
  it("is the folder the archive file lies in", () => {
    expect(parentFolder("D:\\Archives\\ECON 280.efd")).toBe("D:\\Archives");
    expect(parentFolder("D:/Archives/ECON 280.efd")).toBe("D:/Archives");
    expect(parentFolder("\\\\nas\\backups\\laptop.efd")).toBe("\\\\nas\\backups");
  });

  it("keeps the separator at the root of a drive: D:\\ is a folder, D: is not", () => {
    expect(parentFolder("D:\\ECON 280.efd")).toBe("D:\\");
    expect(parentFolder("D:/ECON 280.efd")).toBe("D:/");
  });

  it("is nothing at all when the path names no folder", () => {
    expect(parentFolder("ECON 280.efd")).toBe("");
    expect(parentFolder("")).toBe("");
  });
});

// The prefill rule of APP.md §3, whole.
describe("the destination the dialog opens with", () => {
  it("is the archive's folder plus a folder named after it, for Extract all", () => {
    expect(destinationFor("D:\\Archives\\ECON 280.efd", "ECON 280", true)).toBe("D:\\Archives\\ECON 280");
  });

  it("is the archive's folder itself, with no extra level, for a selection", () => {
    expect(destinationFor("D:\\Archives\\ECON 280.efd", "ECON 280", false)).toBe("D:\\Archives");
  });

  it("sanitises the folder it names after the archive", () => {
    expect(destinationFor("D:\\Archives\\tax.efd", "Tax: 2016/2024", true)).toBe("D:\\Archives\\Tax 20162024");
  });

  it("is empty when the archive's own folder is not known, for Browse… to fill", () => {
    expect(destinationFor("", "ECON 280", true)).toBe("");
    expect(destinationFor("", "ECON 280", false)).toBe("");
  });

  it("adds the folder named after the archive even when the archive already sits in one", () => {
    // D:\Archives\Photos\Photos.efd extracts into D:\Archives\Photos\Photos:
    // the rule has no exception, so the extracted files never land beside
    // the archive file with *Replace* the default (APP.md §3).
    expect(destinationFor("D:\\Archives\\Photos\\Photos.efd", "Photos", true)).toBe("D:\\Archives\\Photos\\Photos");
  });
});

// The four policies of APP.md §3, ruled 2026-09-10.
describe("the policy for a file already there", () => {
  it("is replace by default", () => {
    expect(DEFAULT_POLICY).toBe("replace");
    expect(POLICIES[0].value).toBe("replace");
  });

  it("offers exactly the four words the core takes", () => {
    expect(POLICIES.map((p) => p.value)).toEqual(["replace", "skip", "rename", "ask"]);
  });

  it("says keep both rather than rename, which is what it does", () => {
    expect(POLICIES.find((p) => p.value === "rename")?.label).toBe("Keep both");
  });
});

describe("the destination itself", () => {
  it("is required, and nothing more is judged of it — the core creates it", () => {
    expect(destinationEmpty("")).toBe(true);
    expect(destinationEmpty("   ")).toBe(true);
    expect(destinationEmpty("D:\\Somewhere that is not there yet")).toBe(false);
  });
});

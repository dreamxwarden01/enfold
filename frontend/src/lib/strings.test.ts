import { describe, expect, it } from "vitest";
import { CeremonyStep, Code } from "../../bindings/github.com/dreamxwarden01/enfold/internal/app";
import { codeCopy, codeText, extensionNames, reclaimedText, retriesText, stepCopy, typeLabel } from "./strings";

describe("copy table", () => {
  it("covers every ceremony step", () => {
    for (const step of Object.values(CeremonyStep)) {
      if (!step) continue; // the generator's zero value
      expect(stepCopy[step as keyof typeof stepCopy]?.title, step).toBeTruthy();
    }
  });
  it("covers every error code", () => {
    for (const code of Object.values(Code)) {
      if (!code) continue;
      expect(codeCopy[code as keyof typeof codeCopy], code).toBeTruthy();
      expect(codeText(code)).not.toMatch(/^Error:/);
    }
  });
  it("names an unknown code rather than inventing", () => {
    expect(codeText("something.new")).toBe("Error: something.new");
  });
});

describe("retries", () => {
  it("never says 0 attempts", () => {
    expect(retriesText({ retries: 0, retriesKnown: true, verified: false })).not.toMatch(/0/);
    expect(retriesText({ retries: 0, retriesKnown: false, verified: false })).not.toMatch(/0/);
    expect(retriesText({ retries: 0, retriesKnown: true, verified: true })).not.toMatch(/0/);
  });
  it("shows the count before the PIN is asked", () => {
    expect(retriesText({ retries: 3, retriesKnown: true, verified: false })).toBe("3 attempts left");
    expect(retriesText({ retries: 1, retriesKnown: true, verified: false })).toMatch(/last/);
  });
  it("says unknown when the card is verified", () => {
    expect(retriesText({ retries: 3, retriesKnown: true, verified: true })).toMatch(/unknown/);
  });
});

describe("what a finished reclaim says", () => {
  it("names the bytes the file system got back", () => {
    expect(reclaimedText(1.2 * 1024 * 1024 * 1024)).toBe("Reclaimed 1.2 GB");
  });
});

// The Type column (APP.md §6): a folder, the table's name for an
// extension, "<EXT> file" for one without a name, *File* for none.
describe("the type column", () => {
  it("names a folder, whatever its name says", () => {
    expect(typeLabel("photos.zip", true)).toBe("Folder");
  });
  it("names the common extensions and says the extension for the rest", () => {
    expect(typeLabel("IMG_7201.jpg", false)).toBe("JPEG image");
    expect(typeLabel("DJI_0042.MP4", false)).toBe("MP4 video");
    expect(typeLabel("notes.txt", false)).toBe("Text document");
    expect(typeLabel("a.tar.gz", false)).toBe("Gzip archive");
    expect(typeLabel("model.blend", false)).toBe("BLEND file");
  });
  it("is File for a name with no extension", () => {
    expect(typeLabel("README", false)).toBe("File");
    expect(typeLabel(".env", false)).toBe("File");
  });
  it("keeps every key folded, since the extension is read folded", () => {
    for (const k of Object.keys(extensionNames)) expect(k).toBe(k.toLowerCase());
  });
});

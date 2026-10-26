import { describe, expect, it } from "vitest";
import { fileBlankItems, fileRowItems } from "./filemenu";
import { secondLine } from "./menu";
import type { CmdItem } from "./menu";
import type { Clip } from "./paste";
import { archivePageCopy, commandCopy } from "./strings";

// The file list's menus (direction 7): a file, a folder, the blank area.
const read = (items: CmdItem<string>[]) => items.flatMap((i) => [...(i.sep ? ["──"] : []), `${i.label}${i.keys ? ` [${i.keys}]` : ""}`]);

describe("a row's menu", () => {
  it("on a file: Open, Extract… · Rename…, Move to… · Delete… · Copy name, each with its key", () => {
    expect(read(fileRowItems(false, false))).toEqual([
      `${commandCopy.open} [Enter]`,
      `${commandCopy.extract} [Ctrl+E]`,
      "──",
      `${commandCopy.rename} [F2]`,
      `${commandCopy.moveTo} [Ctrl+Shift+M]`,
      "──",
      `${commandCopy.delete} [Del]`,
      "──",
      commandCopy.copyName,
    ]);
  });

  it("on a folder: the same without Copy name", () => {
    expect(fileRowItems(true, false).map((i) => i.cmd)).toEqual(["open", "extract", "rename", "moveTo", "delete"]);
  });

  it("has Open as its one default, and Delete… as its one danger", () => {
    for (const dir of [true, false]) {
      const m = fileRowItems(dir, false);
      expect(m.filter((i) => i.isDefault).map((i) => i.cmd)).toEqual(["open"]);
      expect(m.filter((i) => i.danger).map((i) => i.cmd)).toEqual(["delete"]);
      expect(m.every((i) => !i.disabled)).toBe(true);
    }
  });

  it("offers neither Extract here nor Properties", () => {
    const labels = fileRowItems(false, false).map((i) => i.label);
    expect(labels.some((l) => /here|propert/i.test(l))).toBe(false);
  });

  it("greys every command while the archive is closing, with why — but Copy name", () => {
    const m = fileRowItems(false, true);
    for (const i of m) {
      if (i.cmd === "copyName") expect(i.disabled).toBeFalsy();
      else expect(i).toMatchObject({ disabled: true, title: archivePageCopy.menuClosing });
    }
  });
});

describe("the blank area's menu", () => {
  it("is Paste · Create folder… · Add files…, Add a folder…", () => {
    expect(read(fileBlankItems(false))).toEqual([`${commandCopy.paste} [Ctrl+V]`, "──", `${commandCopy.createFolder} [Ctrl+Shift+N]`, "──", commandCopy.addFiles, commandCopy.addFolder]);
    for (const clip of ["pending", "unknown", "empty", "copy", "move"] as const) {
      expect(fileBlankItems(true, clip).every((i) => i.disabled && i.title === archivePageCopy.menuClosing && !i.note)).toBe(true);
    }
  });
});

// Paste (ruled 2026-10-26; lib/paste.ts): the files copied in Explorer.
describe("the blank area's Paste", () => {
  const paste = (clip: Clip) => fileBlankItems(false, clip).find((i) => i.cmd === "paste");

  it("is enabled with nothing under it while the read is on its way, after a failed read, and for a copy", () => {
    for (const clip of ["pending", "unknown", "copy"] as const) {
      const p = paste(clip);
      expect(p?.disabled, clip).toBeFalsy();
      expect(secondLine(p ?? {}), clip).toBe("");
    }
  });

  it("is greyed with why when the clipboard names no files", () => {
    expect(paste("empty")).toMatchObject({ disabled: true, title: archivePageCopy.nothingToPaste });
    expect(secondLine(paste("empty") ?? {})).toBe("Nothing to paste");
  });

  it("says the originals stay for files cut in Explorer, and stays enabled", () => {
    const p = paste("move");
    expect(p?.disabled).toBeFalsy();
    expect(secondLine(p ?? {})).toBe("The originals stay where they are");
  });

  it("greys nothing else with the clipboard", () => {
    expect(fileBlankItems(false, "empty").filter((i) => i.disabled).map((i) => i.cmd)).toEqual(["paste"]);
  });
});

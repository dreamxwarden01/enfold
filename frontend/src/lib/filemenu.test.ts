import { describe, expect, it } from "vitest";
import { fileBlankItems, fileRowItems } from "./filemenu";
import type { CmdItem } from "./menu";
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
  it("is Create folder… · Add files…, Add a folder…", () => {
    expect(read(fileBlankItems(false))).toEqual([`${commandCopy.createFolder} [Ctrl+Shift+N]`, "──", commandCopy.addFiles, commandCopy.addFolder]);
    expect(fileBlankItems(true).every((i) => i.disabled && i.title === archivePageCopy.menuClosing)).toBe(true);
  });
});

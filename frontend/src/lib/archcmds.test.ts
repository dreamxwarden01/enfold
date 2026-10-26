import { describe, expect, it } from "vitest";
import { archCommands, blankMenu, rowMenu } from "./archcmds";
import type { ArchFacts, ArchItem } from "./archcmds";
import { archivePageCopy, archivesCopy, commandCopy } from "./strings";

// The selected archive's commands (directions 6 and 7): one table for the
// bar, its More, the pane's More and the row's menu.

const facts = (o: Partial<ArchFacts> = {}): ArchFacts => ({
  unlocked: true,
  tampered: false,
  open: false,
  held: false,
  running: false,
  forgotten: false,
  compacting: false,
  hidden: false,
  missing: false,
  foreign: false,
  detailsRead: true,
  ...o,
});
const why = archivesCopy.why;
// A menu as it reads: labels, "──" for a separator before an item, and the
// reason of a greyed one after a bar.
const read = (items: ArchItem[]) => items.flatMap((i) => [...(i.sep ? ["──"] : []), i.disabled ? `${i.label} | ${i.title ?? ""}` : i.label]);

describe("a row's menu on the list", () => {
  it("is Open, Show in Explorer, the verbs and Hide, the details, then the two that destroy", () => {
    expect(read(rowMenu(archCommands(facts()), false))).toEqual([
      commandCopy.open,
      commandCopy.showInExplorer,
      "──",
      commandCopy.refreshFingerprint,
      commandCopy.compact,
      commandCopy.rekeyArchive,
      commandCopy.hide,
      "──",
      archivesCopy.technicalDetails,
      "──",
      commandCopy.forgetArchive,
      commandCopy.deleteArchive,
    ]);
  });

  it("has Open as its default, bold, with Enter; Technical details… Alt+Enter; Delete archive… Del", () => {
    const m = rowMenu(archCommands(facts()), false);
    expect(m.filter((i) => i.isDefault).map((i) => i.cmd)).toEqual(["open"]);
    expect(m[0].keys).toBe("Enter");
    expect(m.find((i) => i.cmd === "details")?.keys).toBe("Alt+Enter");
    expect(m.find((i) => i.cmd === "delete")?.keys).toBe("Del");
    expect(m.filter((i) => i.danger).map((i) => i.cmd)).toEqual(["forget", "delete"]);
  });

  it("offers Locate… only while the file is missing, and Unhide for a hidden row", () => {
    const m = rowMenu(archCommands(facts({ missing: true, hidden: true })), false);
    expect(m.map((i) => i.cmd)).toContain("locate");
    expect(m.map((i) => i.cmd)).toContain("unhide");
    expect(m.map((i) => i.cmd)).not.toContain("hide");
    expect(rowMenu(archCommands(facts()), false).map((i) => i.cmd)).not.toContain("locate");
  });

  it("offers Close while the archive is open, and Force close once a Close left it held", () => {
    expect(rowMenu(archCommands(facts({ open: true })), false)[1].cmd).toBe("close");
    const held = rowMenu(archCommands(facts({ open: true, held: true, running: true })), false)[1];
    expect(held.cmd).toBe("forceClose");
    expect(held.title).toBe(archivePageCopy.forceCloseRunning);
    expect(held.disabled).toBe(false);
  });

  it("greys by the vault: locked, then Tampered", () => {
    const locked = archCommands(facts({ unlocked: false, detailsRead: false }));
    expect(locked.open[0]).toMatchObject({ disabled: true, title: why.locked });
    expect(locked.verbs.every((i) => i.disabled && i.title === why.locked)).toBe(true);
    expect(locked.details[0]).toMatchObject({ disabled: true, title: why.locked });
    expect(locked.destroy.every((i) => i.title === why.locked)).toBe(true);
    // A locked vault still opens what is open, and still shows the file.
    expect(archCommands(facts({ unlocked: false, open: true })).open[0].disabled).toBe(false);
    expect(locked.reveal[0].disabled).toBe(false);
    const tampered = archCommands(facts({ tampered: true }));
    expect(tampered.verbs.every((i) => i.title === why.tampered)).toBe(true);
    expect(tampered.destroy.every((i) => i.title === why.tampered)).toBe(true);
    // Hide is not a change Tampered stops (as the bar had it).
    expect(tampered.hide[0].disabled).toBe(false);
  });

  it("greys by the row: forgotten first, then a compaction", () => {
    const f = archCommands(facts({ forgotten: true, compacting: true }));
    expect(f.open[0].title).toBe(why.forgotten);
    expect(f.verbs[0].title).toBe(why.forgotten);
    expect(f.hide[0].title).toBe(why.forgotten);
    expect(f.destroy[0].title).toBe(why.alreadyForgotten);
    expect(f.destroy[1].title).toBe(why.compacting);
    const c = archCommands(facts({ compacting: true, open: true }));
    expect(c.close[0]).toMatchObject({ disabled: true, title: why.compacting });
    expect(c.destroy.every((i) => i.title === why.compacting)).toBe(true);
  });

  it("greys Show in Explorer for another system's path", () => {
    expect(archCommands(facts({ foreign: true })).reveal[0]).toMatchObject({ disabled: true, title: why.foreign });
  });

  it("greys Technical details… with no reason while the record is still being read", () => {
    expect(archCommands(facts({ detailsRead: false })).details[0]).toMatchObject({ disabled: true, title: "" });
  });

  it("greys everything for no row", () => {
    const g = archCommands(null);
    for (const i of [...g.open, ...g.verbs, ...g.hide, ...g.details, ...g.destroy, ...g.restore]) expect(i).toMatchObject({ disabled: true, title: archivesCopy.selectFirst });
  });
});

describe("a row's menu in the Forgotten view", () => {
  it("is Restore, then Delete archive…", () => {
    expect(read(rowMenu(archCommands(facts({ forgotten: true })), true))).toEqual([commandCopy.restore, "──", commandCopy.deleteArchive]);
    expect(read(rowMenu(archCommands(facts({ forgotten: true, unlocked: false })), true))).toEqual([`${commandCopy.restore} | ${why.locked}`, "──", `${commandCopy.deleteArchive} | ${why.locked}`]);
  });
});

describe("the list's blank area", () => {
  it("is New archive… and Open file…, New archive greyed by the vault", () => {
    expect(blankMenu(true, false).map((i) => [i.label, i.keys, i.disabled])).toEqual([
      [commandCopy.newArchive, "Ctrl+N", false],
      [archivesCopy.openFile, "Ctrl+O", false],
    ]);
    expect(blankMenu(false, false)[0]).toMatchObject({ disabled: true, title: why.locked });
    expect(blankMenu(true, true)[0]).toMatchObject({ disabled: true, title: why.tampered });
    expect(blankMenu(false, true)[1].disabled).toBe(false);
  });
});

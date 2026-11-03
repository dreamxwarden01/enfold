// Names in a dialog (APP.md §6, Dialogs; ruled 2026-11-03, after the
// user's add conflict whose long file name ran past the box's edge and
// gave its body a sideways scrollbar). The split of a title at its names
// is Dialog.svelte's own module function, tested here as a function; the
// rest is read as source, as rekey.components.test.ts reads its dialogs:
// that each dialog whose title or rows say a user-given name hands the
// name to the rule, that a row's name is cut with the app's tooltip, and
// that the cap stands where the record says. app.css is not read here
// (the test runner hands a stylesheet back empty), and how it all lays
// out is the mock's to show: the shots of the build measure every
// dialog body's scrollWidth against its clientWidth.
import { describe, expect, it } from "vitest";

// Dialog.svelte's module exports, taken as the test runner compiles them.
// tsc reads a .svelte file through the ambient "*.svelte" module, which
// declares a default export alone (svelte-check knows the rest), so the
// three are named here with their types.
interface NamePart {
  text: string;
  name: boolean;
  tail?: string;
}
const { NAME_CAP, NAME_EDGE, nameParts } = (await import("./Dialog.svelte")) as unknown as {
  NAME_CAP: number;
  NAME_EDGE: number;
  nameParts: (text: string, names?: readonly string[]) => NamePart[];
};

const sources = import.meta.glob(
  [
    "./Dialog.svelte",
    "./CommandRows.svelte",
    "./ArchivePage.svelte",
    "./ArchivesPage.svelte",
    "./ArchiveDetailsDialog.svelte",
    "./ConflictDialog.svelte",
    "./DeleteArchiveDialog.svelte",
    "./ExtractDialog.svelte",
    "./ImportDialog.svelte",
    "./KeysPage.svelte",
    "./MergeDialog.svelte",
    "./MoveToDialog.svelte",
    "./OpenKeyDialog.svelte",
    "./ResultsDialog.svelte",
  ],
  { query: "?raw", import: "default", eager: true },
) as Record<string, string>;

function src(name: string): string {
  const hit = Object.entries(sources).find(([f]) => f.endsWith(`/${name}`));
  if (!hit) throw new Error(`not read: ${name}`);
  return hit[1].replace(/\r\n/g, "\n");
}

// The one tag that opens with `start` and holds `marker`, up to its end.
function tagWith(s: string, start: string, marker: string): string {
  let at = s.indexOf(start);
  while (at >= 0) {
    const end = s.indexOf(">\n", at);
    const tag = s.slice(at, end < 0 ? undefined : end + 1);
    if (tag.includes(marker)) return tag;
    at = s.indexOf(start, at + 1);
  }
  throw new Error(`no ${start} holding ${marker}`);
}

describe("a title split at its names", () => {
  it("cuts the names it says, in the order it says them, and leaves the words whole", () => {
    expect(nameParts("A file named ECON_280_Week3.mp4 is already in test", ["ECON_280_Week3.mp4", "test"])).toEqual([
      { text: "A file named ", name: false },
      { text: "ECON_280_Week3.mp4", name: true, tail: "" },
      { text: " is already in ", name: false },
      { text: "test", name: true, tail: "" },
    ]);
  });

  it("is the text alone when it names nothing", () => {
    expect(nameParts("Create folder")).toEqual([{ text: "Create folder", name: false }]);
    expect(nameParts("Create folder", [])).toEqual([{ text: "Create folder", name: false }]);
    expect(nameParts("", [])).toEqual([{ text: "", name: false }]);
  });

  it("passes over a name the text does not say, and an empty one", () => {
    // A title that counts instead of naming still cuts its folder.
    expect(nameParts("2 files with the same names are already in test", ["IMG_7201.HEIC", "test"])).toEqual([
      { text: "2 files with the same names are already in ", name: false },
      { text: "test", name: true, tail: "" },
    ]);
    expect(nameParts("Permanently delete 1 file?", [""])).toEqual([{ text: "Permanently delete 1 file?", name: false }]);
  });

  it("finds a short name where it stands as a word, not inside one", () => {
    expect(nameParts("Rename e", ["e"])).toEqual([
      { text: "Rename ", name: false },
      { text: "e", name: true, tail: "" },
    ]);
    expect(nameParts("A file named am is already in test", ["am", "test"])).toEqual([
      { text: "A file named ", name: false },
      { text: "am", name: true, tail: "" },
      { text: " is already in ", name: false },
      { text: "test", name: true, tail: "" },
    ]);
  });

  it("finds a name that ends the text, or one followed by its question mark, which it keeps on its line", () => {
    expect(nameParts("Details: Photos 2024", ["Photos 2024"])).toEqual([
      { text: "Details: ", name: false },
      { text: "Photos 2024", name: true, tail: "" },
    ]);
    expect(nameParts("Compact Photos 2024?", ["Photos 2024"])).toEqual([
      { text: "Compact ", name: false },
      { text: "Photos 2024", name: true, tail: "?" },
    ]);
  });

  it("glues only punctuation to a name, never a word or a space", () => {
    expect(nameParts("Delete ECON 280?", ["ECON 280"])[1]).toEqual({ text: "ECON 280", name: true, tail: "?" });
    expect(nameParts("Remove YubiKey (added 2026-10-01)?", ["YubiKey"])[1]).toEqual({ text: "YubiKey", name: true, tail: "" });
    expect(nameParts("Move IMG_7201.HEIC to…", ["IMG_7201.HEIC"])[1]).toEqual({ text: "IMG_7201.HEIC", name: true, tail: "" });
  });

  it("holds a name of 255 code units whole as one part", () => {
    const long = "a".repeat(251) + ".mp4";
    const parts = nameParts(`A file named ${long} is already in test`, [long, "test"]);
    expect(parts[1]).toEqual({ text: long, name: true, tail: "" });
    expect(parts.map((p) => p.text).join("")).toBe(`A file named ${long} is already in test`);
  });
});

describe("the cap", () => {
  it("lets a dialog grow to the wide dialog's 640, less the 20 px it keeps from each edge of the window", () => {
    expect(NAME_CAP).toBe(640);
    expect(NAME_EDGE).toBe(20);
    const d = src("Dialog.svelte");
    expect(d).toContain("Math.min(NAME_CAP, window.innerWidth - 2 * NAME_EDGE)");
    expect(d).toContain(".dialog.wide { width: 640px; }");
  });

  it("measures the cut names as the dialog opens and again when its title changes, never narrowing the box", () => {
    const d = src("Dialog.svelte");
    expect(d).toContain('querySelectorAll<HTMLElement>(".cut-name")');
    expect(d).toMatch(/void title;\s+void names;\s+if \(box\) fit\(box\);/);
    expect(d).toContain("if (w0 >= cap) return;");
    expect(d).not.toMatch(/style\.width = ""|removeProperty\("width"\)/);
  });

  it("draws the title through its names, with the app's tooltip on a cut one", () => {
    const d = src("Dialog.svelte");
    expect(d.match(/<h2>\{@render named\(title, names\)\}\{@render heading\?\.\(\)\}<\/h2>/g)).toHaveLength(2);
    expect(d).toContain('{#if p.name}<span class="cut-name" use:tip={{ text: p.text, cut: true }}>{p.text}{p.tail}</span>{:else}{p.text}{/if}');
  });
});

describe("every dialog that says a user-given name hands it to the rule", () => {
  it("in its title", () => {
    const a = src("ArchivePage.svelte");
    expect(tagWith(a, "<Dialog", 'title="Rename {renaming.name}"')).toContain("names={[renaming.name]}");
    expect(tagWith(a, "<Dialog", "deleteTitle(")).toContain('names={[stat?.name ?? ""]}');
    expect(tagWith(a, "<Dialog", "conflictTitle(")).toContain("names={conflicts.length === 1 ? [conflicts[0].name] : []}");
    expect(tagWith(a, "<Dialog", "collisionSaid.title")).toContain("names={collisionNames}");
    expect(a).toContain("[shownName(collisions.list[0].name), collisionPlace]");
    expect(tagWith(src("ArchivesPage.svelte"), "<Dialog", "confirm.title")).toContain("names={confirm.names}");
    expect(src("ArchivesPage.svelte").match(/names: \[a\.name\]/g)).toHaveLength(2);
    expect(tagWith(src("ArchiveDetailsDialog.svelte"), "<Dialog", "technicalCopy.titleOf")).toContain("names={[d.name]}");
    expect(tagWith(src("DeleteArchiveDialog.svelte"), "<Dialog", "{title}")).toContain("names={[name]}");
    expect(tagWith(src("ExtractDialog.svelte"), "<Dialog", "extractCopy.title")).toContain("names={[archiveName]}");
    expect(tagWith(src("KeysPage.svelte"), "<Dialog", "removeUnlockMethod.title")).toContain("names={[target.label]}");
    expect(tagWith(src("MoveToDialog.svelte"), "<Dialog", "moveToCopy.title")).toContain("names={[name]}");
    expect(tagWith(src("OpenKeyDialog.svelte"), "<Dialog", "openCopy.title")).toContain("names={[baseName(path)]}");
  });

  it("in its rows, cut with the app's tooltip", () => {
    expect(src("ArchivePage.svelte")).toContain('<span class="nm cut-name" use:tip={{ text: shownName(c.name), cut: true }}>{shownName(c.name)}</span>');
    expect(src("ArchivePage.svelte")).toContain("names: [collisionPlace],");
    expect(src("CommandRows.svelte")).toContain("<b>{@render named(r.label, r.names)}</b>");
    expect(src("MoveToDialog.svelte")).toContain('<span class="nm cut-name" use:tip={{ text: l.name, cut: true }}>{l.name}</span>');
    expect(src("ResultsDialog.svelte")).toContain('<span class="cut-run"><b class="cut-name" use:tip={{ text: t.name, cut: true }}>{t.name}</b>{#if after}<b>{after}</b>{/if}:</span>');
    expect(src("MergeDialog.svelte")).toContain('<b class="cut-name" use:tip={{ text: r.name, cut: true }}>');
    expect(src("ExtractDialog.svelte")).toContain('<span class="cut-run">{@render named(extractCopy.scopeAll(archiveName), [archiveName])}</span>');
    // The vault's name on the replacement's tick, which the bar above it
    // says whole.
    expect(src("ImportDialog.svelte")).toContain("(${st.displayName})`, [st.displayName])}</span></label>");
    // The compare list's tooltip is the whole path, which says more than
    // the name, so it shows whether the name is cut or not.
    const c = src("ConflictDialog.svelte");
    expect(c).toContain('<b class="cut-name" use:tip={{ text: r.path }}>{r.name}</b>');
    expect(c).toContain('<b class="cut-name" use:tip={{ text: r.dest }}>{r.name}</b>');
    expect(c).not.toContain("title={r.path}");
  });

  it("never cuts the name Delete archive asks to be typed", () => {
    const d = src("DeleteArchiveDialog.svelte");
    expect(d).toContain('<div class="field-top name-top"><label for="del-name">{dialogCopy.confirmName}</label><span class="hint">{name}</span></div>');
    expect(d).toContain(".name-top { flex-wrap: wrap; }");
  });
});

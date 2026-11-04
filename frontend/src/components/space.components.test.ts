// Unused space (APP.md §6; the reclaim study's ruling of 2026-11-03 and
// the owner's of 2026-11-04): the bar's segments and its fold, the share,
// the figure read into its five kinds, the Details dialog's block in every
// state, the pane's row and the panel's collapsed line — SpaceBar.svelte's
// module functions, tested as functions, as names.components.test.ts tests
// Dialog's — and then the wiring read as source: where the block stands,
// that Close keeps the dialog's accent, that the bar is hidden from
// Narrator, that the pane's row is hidden below the floor and never
// greyed. How it all lays out, in both themes and both contrast palettes,
// is the mock's to show.
import { describe, expect, it } from "vitest";
import type { ReclaimView } from "../lib/api";

type SegKind = "live" | "back" | "only" | "track";
type Seg = { kind: SegKind; grow: number };
type SpaceView = { kind: string; size: number; freeSpace: number; back: number; only: number; move: number; complete: boolean };
type RunFig = { done: number; total: number; returned: number };
type BlockIn = {
  open: boolean;
  size: number;
  freeSpace: number;
  reclaim?: ReclaimView | null;
  compactNeeds: number;
  path: string;
  run?: RunFig | null;
  runWhy?: string;
  busyWhy?: string;
  reclaimWhy?: string;
  compactWhy?: string;
  held?: boolean;
};
type Row = { disabled: boolean; hint: string };
type BlockOut = {
  bar: { size: number; freeSpace: number; back: number; complete: boolean } | null;
  share: string;
  strong: string;
  rest: string;
  legend: { kind: string; label: string }[];
  reclaim: (Row & { cancel: boolean }) | null;
  progress?: number | null;
  compact: Row;
};
type RowOut = { bar: boolean; share: string; line: { kind: "link"; title: string; disabled: boolean } | { kind: "hint" | "note"; text: string } | null };

// SpaceBar.svelte's module exports, as the test runner compiles them (tsc
// reads a .svelte file as a default export alone, so they are typed here).
const S = (await import("./SpaceBar.svelte")) as unknown as {
  SPACE_FLOOR: number;
  segments: (size: number, freeSpace: number, back: number, complete: boolean, striped: boolean) => Seg[];
  shareText: (size: number, freeSpace: number) => string;
  spaceView: (f: { size: number; freeSpace: number; reclaim?: ReclaimView | null }) => SpaceView;
  barOf: (v: SpaceView) => { size: number; freeSpace: number; back: number; complete: boolean };
  volumeOf: (path: string) => string;
  spaceBlock: (b: BlockIn) => BlockOut;
  spaceRow: (v: SpaceView, run: RunFig | null | undefined, busyWhy?: string) => RowOut;
  panelLine: (v: SpaceView | null, running: boolean) => string;
};

const MiB = 1024 * 1024;
const GiB = 1024 * MiB;
// The staged figures (tools/uimock/server.py "space"): 2.9 GB unused of
// 48 GB, all of it given back by a run that moves about 11 GB; and the
// split, 3.1 GB unused of 8.3 GB, 1.2 GB of it in place.
const SIZE = 51_700_000_000;
const FREE = 3_100_000_000;
const MOVE = 11_800_000_000;
const fig = (back: number, move: number, complete = true, atSeq = 7): ReclaimView => ({ back, move, complete, atSeq });
const kinds = (s: Seg[]) => s.map((x) => x.kind);

describe("the bar's segments", () => {
  it("is all data under the floor, and nothing for an empty file", () => {
    expect(S.SPACE_FLOOR).toBe(64 * MiB);
    expect(S.segments(SIZE, 20 * MiB, 20 * MiB, true, true)).toEqual([{ kind: "live", grow: 1 }]);
    expect(S.segments(0, 0, 0, true, true)).toEqual([]);
  });

  it("draws the data, then what Reclaim gives back, each by its share of the file", () => {
    const s = S.segments(SIZE, FREE, FREE, true, true);
    expect(kinds(s)).toEqual(["live", "back"]);
    expect(s[0].grow + s[1].grow).toBeCloseTo(1);
    expect(s[1].grow).toBeCloseTo(FREE / SIZE);
  });

  it("splits what only Compact reaches, striped where tall and plain track where slim", () => {
    expect(kinds(S.segments(8.9e9, 3.33e9, 1.29e9, true, true))).toEqual(["live", "back", "only"]);
    expect(kinds(S.segments(8.9e9, 3.33e9, 1.29e9, true, false))).toEqual(["live", "back", "track"]);
  });

  it("folds an only-Compact remainder under 64 MiB into what Reclaim gives back", () => {
    const s = S.segments(SIZE, FREE, FREE - 63 * MiB, true, true);
    expect(kinds(s)).toEqual(["live", "back"]);
    expect(s[1].grow).toBeCloseTo(FREE / SIZE);
    // At 64 MiB it is drawn.
    expect(kinds(S.segments(SIZE, FREE, FREE - 64 * MiB, true, true))).toEqual(["live", "back", "only"]);
  });

  it("stripes all of it when a move gives back less than the floor", () => {
    expect(kinds(S.segments(SIZE, FREE, 10 * MiB, true, true))).toEqual(["live", "only"]);
    expect(kinds(S.segments(SIZE, FREE, 0, true, false))).toEqual(["live", "track"]);
  });

  it("draws a figure cut short, or not planned yet, unsplit: the back fill where tall, plain track in a row", () => {
    expect(kinds(S.segments(SIZE, FREE, 0, false, true))).toEqual(["live", "back"]);
    // A row has no legend, and the accent fill there would say what a
    // cut-short plan does not know (the judge's risk 1).
    expect(kinds(S.segments(SIZE, FREE, 0, false, false))).toEqual(["live", "track"]);
  });

  it("never draws more unused space than the file, nor a Back over the unused", () => {
    const s = S.segments(SIZE, SIZE * 2, SIZE * 3, true, true);
    expect(kinds(s)).toEqual(["back"]);
    expect(s[0].grow).toBe(1);
  });
});

describe("the share", () => {
  it("is whole percent, <1 % for a sliver, and never 100 % while data is left", () => {
    expect(S.shareText(SIZE, FREE)).toBe("6 %");
    expect(S.shareText(8.9e9, 3.33e9)).toBe("37 %");
    expect(S.shareText(SIZE, 100 * MiB)).toBe("<1 %");
    // Tested before the rounding: 0.6 % is still under one percent.
    expect(S.shareText(1000, 6)).toBe("<1 %");
    expect(S.shareText(1000, 9.9)).toBe("<1 %");
    expect(S.shareText(1000, 10)).toBe("1 %");
    expect(S.shareText(SIZE, 0)).toBe("0 %");
    expect(S.shareText(1000 * GiB, 999.9 * GiB)).toBe("99 %");
    expect(S.shareText(0, 0)).toBe("0 %");
  });
});

describe("the figure, read", () => {
  it("is under the floor below 64 MiB, whatever the plan says", () => {
    expect(S.spaceView({ size: SIZE, freeSpace: 63 * MiB, reclaim: fig(63 * MiB, 0) }).kind).toBe("under");
  });

  it("is pending before the first plan, and incomplete for a plan cut short", () => {
    expect(S.spaceView({ size: SIZE, freeSpace: FREE, reclaim: null })).toMatchObject({ kind: "pending", back: 0, only: 0, complete: false });
    // The every-10th layout (the core's test): a floor, never an answer.
    expect(S.spaceView({ size: SIZE, freeSpace: FREE, reclaim: fig(0, 3776 * MiB, false) })).toMatchObject({ kind: "incomplete", back: 0, complete: false });
  });

  it("is reclaimable from 64 MiB back, with the remainder folded or kept", () => {
    expect(S.spaceView({ size: SIZE, freeSpace: FREE, reclaim: fig(FREE, MOVE) })).toMatchObject({ kind: "reclaimable", back: FREE, only: 0, move: MOVE });
    expect(S.spaceView({ size: 8.9e9, freeSpace: 3.33e9, reclaim: fig(1.29e9, 0.43e9) })).toMatchObject({ kind: "reclaimable", back: 1.29e9, only: 3.33e9 - 1.29e9 });
    expect(S.spaceView({ size: SIZE, freeSpace: FREE, reclaim: fig(FREE - 10 * MiB, MOVE) })).toMatchObject({ back: FREE, only: 0 });
  });

  it("is only Compact's when a move gives back less than the floor", () => {
    expect(S.spaceView({ size: SIZE, freeSpace: FREE, reclaim: fig(10 * MiB, MOVE) })).toMatchObject({ kind: "compactOnly", back: 0, only: FREE });
  });

  it("keeps the last plan while the next is made, its Back held to the unused space there is now", () => {
    // An add filled holes after the plan: Back may not exceed FreeSpace.
    const v = S.spaceView({ size: SIZE, freeSpace: 1 * GiB, reclaim: fig(FREE, MOVE, true, 3) });
    expect(v).toMatchObject({ kind: "reclaimable", back: 1 * GiB, only: 0 });
    expect(S.barOf(v)).toEqual({ size: SIZE, freeSpace: 1 * GiB, back: 1 * GiB, complete: true });
  });
});

describe("the drive a path is on", () => {
  it("is the letter, a share, or its drive", () => {
    expect(S.volumeOf("D:/Archives/photos-2024.efd")).toBe("D:");
    expect(S.volumeOf("e:\\x.efd")).toBe("E:");
    expect(S.volumeOf("\\\\nas\\backups\\laptop.efd")).toBe("\\\\nas\\backups");
    expect(S.volumeOf("/Volumes/Media/family-videos.efd")).toBe("its drive");
  });
});

describe("the Details dialog's block", () => {
  const base: BlockIn = { open: true, size: SIZE, freeSpace: FREE, reclaim: fig(FREE, MOVE), compactNeeds: SIZE - FREE, path: "D:/Archives/photos-2024.efd" };
  const block = (o: Partial<BlockIn> = {}) => S.spaceBlock({ ...base, ...o });
  const line = (b: BlockOut) => b.strong + b.rest;

  it("says a closed archive has no figure, and offers Compact… alone, bounded by its size", () => {
    const b = block({ open: false, freeSpace: 0, reclaim: null, compactNeeds: 0 });
    expect(b).toMatchObject({ bar: null, share: "", strong: "", rest: "Open the archive to see its unused space.", legend: [], reclaim: null });
    expect(b.compact).toEqual({ disabled: false, hint: "Rewrites the whole file; needs up to 48 GB free on D:" });
  });

  it("is reclaimable: the share, all of it back, its cost, Compact's figure", () => {
    const b = block();
    expect(b.share).toBe("6 % unused");
    expect([b.strong, b.rest]).toEqual(["2.9 GB", " unused of 48 GB · Reclaim space gives all of it back"]);
    expect(b.legend.map((l) => l.label)).toEqual(["Archive data", "Reclaim gives back"]);
    expect(b.reclaim).toEqual({ cancel: false, disabled: false, hint: "Moves about 11 GB inside the file. Adding and extracting wait until it ends; you can cancel it." });
    expect(b.compact).toEqual({ disabled: false, hint: "Rewrites the whole file; needs about 45 GB free on D:" });
    expect(b.bar).toEqual({ size: SIZE, freeSpace: FREE, back: FREE, complete: true });
  });

  it("is split: what Reclaim gives back, and the rest Compact's, in the legend too", () => {
    const b = block({ size: 8.9e9, freeSpace: 3.33e9, reclaim: fig(1.29e9, 0.43e9), compactNeeds: 8.9e9 - 3.33e9 });
    expect(line(b)).toBe("3.1 GB unused of 8.3 GB · Reclaim space gives back 1.2 GB; only Compact reaches the rest");
    expect(b.legend.map((l) => [l.kind, l.label])).toEqual([["live", "Archive data"], ["back", "Reclaim gives back"], ["only", "Only Compact reaches"]]);
  });

  it("greys Reclaim under the floor with nothing to reclaim, and keeps Compact", () => {
    const b = block({ freeSpace: 20 * MiB, reclaim: fig(20 * MiB, 0) });
    expect(b.share).toBe("");
    expect(line(b)).toBe("No unused space worth reclaiming · 48 GB");
    expect(b.legend.map((l) => l.kind)).toEqual(["live"]);
    expect(b.reclaim).toEqual({ cancel: false, disabled: true, hint: "Nothing to reclaim" });
    expect(b.compact.disabled).toBe(false);
  });

  it("greys Reclaim when only Compact reaches the unused space", () => {
    const b = block({ reclaim: fig(10 * MiB, MOVE) });
    expect(line(b)).toBe("2.9 GB unused of 48 GB · only Compact reaches it");
    expect(b.reclaim).toEqual({ cancel: false, disabled: true, hint: "Moving files would give nothing back" });
    expect(b.legend.map((l) => l.kind)).toEqual(["live", "only"]);
  });

  it("says no in-place figure while the plan is cut short or not made, and offers the open run", () => {
    for (const reclaim of [fig(0, 3776 * MiB, false), null]) {
      const b = block({ reclaim });
      expect(line(b)).toBe("2.9 GB unused of 48 GB");
      expect(b.legend.map((l) => [l.kind, l.label])).toEqual([["live", "Archive data"], ["unsplit", "Unused"]]);
      expect(b.reclaim).toEqual({ cancel: false, disabled: false, hint: "Moves files inside the archive until no more space comes back. You can cancel it." });
      expect(b.bar?.complete).toBe(false);
    }
  });

  it("puts Cancel, the progress and the moved line in Reclaim's place during a run, and greys Compact", () => {
    const why = "Reclaiming space… wait for it to end";
    const b = block({ run: { done: 4.83e9, total: MOVE, returned: 1.18e9 }, runWhy: why });
    expect(b.reclaim).toEqual({ cancel: true, disabled: false, hint: "Reclaiming space · 4.5 GB of 11 GB moved · 1.1 GB back so far" });
    expect(b.progress).toBeCloseTo(4.83e9 / MOVE);
    expect(b.compact).toEqual({ disabled: true, hint: why });
    // A run whose total is not known yet moves the line's segment.
    const open = block({ run: { done: 2e9, total: 0, returned: 0 }, runWhy: why });
    expect(open.progress).toBeNull();
    expect(open.reclaim?.hint).toBe("Reclaiming space · 1.9 GB moved · 0 B back so far");
  });

  it("is reclaimable again once a cancelled run has ended: nothing resumes by itself", () => {
    const b = block({ reclaim: fig(1.9e9, 7e9), freeSpace: 1.9e9, run: null });
    expect(b.reclaim).toMatchObject({ cancel: false, disabled: false });
  });

  it("greys both while a compaction or another operation holds the archive", () => {
    const why = "Compacting… wait for it to end";
    const b = block({ busyWhy: why });
    expect(b.reclaim).toEqual({ cancel: false, disabled: true, hint: why });
    expect(b.compact).toEqual({ disabled: true, hint: why });
    expect(line(b)).toBe("2.9 GB unused of 48 GB · Reclaim space gives all of it back"); // the old figures stay
  });

  it("greys each by the vault's own stops", () => {
    const b = block({ reclaimWhy: "Changes are off", compactWhy: "Unlock the vault first" });
    expect(b.reclaim).toMatchObject({ disabled: true, hint: "Changes are off" });
    expect(b.compact).toEqual({ disabled: true, hint: "Unlock the vault first" });
    expect(block({ open: false, compactWhy: "Unlock the vault first" }).compact.disabled).toBe(true);
  });

  it("says nothing of a closed archive when a compaction holds an open one with no figure kept", () => {
    const why = "Compacting… wait for it to end";
    const b = block({ open: false, held: true, busyWhy: why });
    expect(b.rest).toBe("");
    expect(b.bar).toBeNull();
    expect(b.compact).toEqual({ disabled: true, hint: why });
    expect(block({ open: false }).rest).toBe("Open the archive to see its unused space.");
  });

  it("never calls the space inside the file free", () => {
    const all = [block(), block({ reclaim: null }), block({ freeSpace: 0 }), block({ reclaim: fig(0, 1, true) }), block({ open: false })];
    for (const b of all) expect(`${b.share} ${b.strong}${b.rest} ${b.legend.map((l) => l.label).join(" ")} ${b.reclaim?.hint ?? ""}`).not.toMatch(/\bfree\b/i);
  });
});

describe("the pane's row and the panel's open facts", () => {
  const v = (o: Partial<{ freeSpace: number; reclaim: ReclaimView | null }> = {}) => S.spaceView({ size: SIZE, freeSpace: FREE, reclaim: fig(FREE, MOVE), ...o });

  it("offers the Reclaim space link with its cost when a whole plan gives back the floor or more", () => {
    expect(S.spaceRow(v(), null)).toEqual({ bar: true, share: "6 %", line: { kind: "link", title: "Moves about 11 GB inside the file", disabled: false } });
  });

  it("greys the link with the reason while another operation holds the archive", () => {
    expect(S.spaceRow(v(), null, "Adding 3 files… wait for it to end").line).toEqual({ kind: "link", title: "Adding 3 files… wait for it to end", disabled: true });
  });

  it("keeps today's (Compact to reclaim) when only Compact reaches the unused space", () => {
    expect(S.spaceRow(v({ reclaim: fig(0, MOVE) }), null).line).toEqual({ kind: "hint", text: "(Compact to reclaim)" });
  });

  it("says the run in the link's place", () => {
    expect(S.spaceRow(v(), { done: 1, total: 2, returned: 1.18e9 }).line).toEqual({ kind: "note", text: "Reclaiming · 1.1 GB back so far" });
  });

  it("offers the link titled with what the run does while the plan is pending or cut short, as Details does", () => {
    const open = { kind: "link", title: "Moves files inside the archive until no more space comes back", disabled: false };
    expect(S.spaceRow(v({ reclaim: null }), null)).toEqual({ bar: true, share: "6 %", line: open });
    expect(S.spaceRow(v({ reclaim: fig(0, MOVE, false) }), null)).toEqual({ bar: true, share: "6 %", line: open });
    expect(S.spaceRow(v({ reclaim: null }), null, "Adding 3 files… wait for it to end").line).toEqual({ kind: "link", title: "Adding 3 files… wait for it to end", disabled: true });
  });

  it("hides the bar and the control below the floor, never greying them (the owner's ruling of 2026-11-04)", () => {
    expect(S.spaceRow(v({ freeSpace: 0 }), null)).toEqual({ bar: false, share: "", line: null });
    expect(S.spaceRow(v({ freeSpace: 63 * MiB }), null, "Adding 3 files… wait for it to end")).toEqual({ bar: false, share: "", line: null });
  });
});

describe("the panel's collapsed line", () => {
  it("says what Reclaim gives back, the run, or nothing", () => {
    const view = S.spaceView({ size: SIZE, freeSpace: FREE, reclaim: fig(FREE, MOVE) });
    expect(S.panelLine(view, false)).toBe("2.9 GB reclaimable");
    expect(S.panelLine(view, true)).toBe("Reclaiming space");
    expect(S.panelLine(null, false)).toBe(""); // a closed archive
    expect(S.panelLine(S.spaceView({ size: SIZE, freeSpace: FREE, reclaim: fig(0, MOVE, false) }), false)).toBe("");
    expect(S.panelLine(S.spaceView({ size: SIZE, freeSpace: FREE, reclaim: fig(0, MOVE) }), false)).toBe("");
    expect(S.panelLine(S.spaceView({ size: SIZE, freeSpace: 0, reclaim: null }), false)).toBe("");
  });
});

// ---- the wiring, read as source -------------------------------------------

const sources = import.meta.glob(["./SpaceBar.svelte", "./ArchiveDetailsDialog.svelte", "./ArchivePage.svelte", "./ArchivesPage.svelte"], {
  query: "?raw",
  import: "default",
  eager: true,
}) as Record<string, string>;

function src(name: string): string {
  const hit = Object.entries(sources).find(([f]) => f.endsWith(`/${name}`));
  if (!hit) throw new Error(`not read: ${name}`);
  return hit[1].replace(/\r\n/g, "\n");
}
const markup = (s: string) => s.slice(s.lastIndexOf("</script>"));

describe("the wiring", () => {
  it("hides the bar from Narrator and animates nothing", () => {
    const bar = markup(src("SpaceBar.svelte"));
    expect(bar).toContain('aria-hidden="true"');
    expect(bar).not.toMatch(/transition|animate|in:|out:/);
  });

  it("stands the block above the IDs, with each hint linked to its button, and Close keeps the accent", () => {
    const d = markup(src("ArchiveDetailsDialog.svelte"));
    expect(d.indexOf('class="spaceblock"')).toBeGreaterThan(0);
    expect(d.indexOf('class="spaceblock"')).toBeLessThan(d.indexOf('<dl class="kv">'));
    expect(d).toContain('aria-describedby="sb-reclaim-hint"');
    expect(d).toContain('aria-describedby="sb-compact-hint"');
    expect(d).toContain('id="sb-compact-hint"');
    // One accent, Close's: the block's buttons are standard.
    expect(d.match(/class="btn accent"/g)?.length).toBe(1);
    expect(d).toContain('<button type="button" class="btn accent" onclick={onclose}>');
    expect(d.slice(d.indexOf('class="spaceblock"'), d.indexOf("</section>"))).not.toContain("accent");
  });

  it("gives the Details dialog the row, the run and Compact's own confirm", () => {
    const p = src("ArchivesPage.svelte");
    expect(p).toContain("space={pane}");
    expect(p).toContain("run={reclaimRun}");
    expect(p).toContain("oncompact={askCompact}");
  });

  it("keeps the pane's row today's figure alone below the floor", () => {
    const html = markup(src("ArchivePage.svelte"));
    const row = html.slice(html.indexOf("{archivesCopy.facts.unused}"), html.indexOf("{archivesCopy.facts.compression}"));
    expect(row).toContain("{#if paneSpace && paneRow?.bar}");
    expect(row).toContain('{:else}\n                  <span class="num">{stat ? bytes(stat.freeSpace) : ""}</span>');
    expect(row).not.toContain("freeWorthShowing");
  });

  it("says the collapsed panel's words only for an open archive", () => {
    const p = src("ArchivesPage.svelte");
    expect(p).toContain('const panelWords = $derived(one?.open ? panelLine(paneSpace, !!reclaimRun) : "");');
    expect(p).toContain('{#if panelWords}<span class="hreclaim">{panelWords}</span>{/if}');
  });

  it("keeps the Compact confirm up through the call and says a refusal for room in it", () => {
    const p = src("ArchivesPage.svelte");
    expect(p).toContain("err.code === Code.CodeCompactNoSpace");
    expect(p).toContain("dialogCopy.compact.noSpace(bytes(err.needed ?? 0), bytes(err.free ?? 0)");
    expect(p).toContain("{#if confirm.refused}");
    // The need is said once: the note gives way to the refusal.
    expect(p).toContain("{#if confirm.note && !confirm.refused}");
  });

  it("greys Reclaim space by no stop of the vault's, on either page (changes off freezes the vault file only)", () => {
    const p = src("ArchivesPage.svelte");
    expect(p).toContain("spaceRow(paneSpace, reclaimRun, otherBusy)");
    expect(p).not.toMatch(/reclaimWhy=\{tampered/);
    expect(src("ArchivePage.svelte")).not.toMatch(/reclaimLinkWhy = \$derived\([^)]*tampered/);
  });
});

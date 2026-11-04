<script lang="ts" module>
  // The unused space inside an archive's file, drawn and said (APP.md §6,
  // *Unused space*; the reclaim study's ruling of 2026-11-03, the judge's
  // "What to build, exactly"). One bar for three places — the Details
  // dialog's block, the Archive page's pane and the Archives page's
  // panel — and the pure rules that choose their words, here so that the
  // three cannot drift apart and vitest can feed them
  // (space.components.test.ts imports this module as names.components.test.ts
  // imports Dialog's).
  //
  // The figures are the core's: FreeSpace, the archive's published free
  // map, which a whole-file Compact gives back, and ArchiveStat.Reclaim,
  // the last dry run of the in-place run (FORMAT.md R40, APP.md §2.3) —
  // Back, what that run would give the file system back, Move, what it
  // would copy for it, Complete, whether the dry run reached its end, and
  // AtSeq, the Seq it was made at. What only Compact reaches is FreeSpace
  // less Back. A figure not complete is a floor and not an answer, so the
  // page shows no in-place figure rather than a low one (the judge's risk
  // 1); a figure made before the last change is kept until the next lands,
  // tens of milliseconds later, so that nothing flickers.
  import type { ReclaimView } from "../lib/api";
  import { bytes } from "../lib/format";
  import { freeSpaceFloor } from "../lib/status";
  import { paneCopy, spaceCopy } from "../lib/strings";

  // The rule's floor (ops.go reclaimRule, 64 MiB): below it nothing is
  // worth a reader's attention, and an only-Compact remainder under it is
  // folded into what Reclaim gives back rather than drawn as a sliver.
  export const SPACE_FLOOR = freeSpaceFloor;

  export type SegKind = "live" | "back" | "only" | "track";
  export interface Seg {
    kind: SegKind;
    // The share of the file, 0 to 1: the segment's flex-grow.
    grow: number;
  }

  const clampFree = (size: number, free: number): number => Math.max(0, Math.min(Number.isFinite(free) ? free : 0, size));

  // segments: the bar's segments, in order. live is the archive's data;
  // back what the in-place run gives back; only what a whole-file Compact
  // alone reaches, striped where the bar is tall enough for stripes (the
  // Details dialog) and plain track where it is not (4 px blur them). A
  // figure not complete — or not planned yet — draws its unused space as
  // one segment, unsplit: in the Details dialog the back fill, which its
  // legend names "Unused" (the judge's table "D's states"), and elsewhere
  // plain track, since a row has no legend and the accent fill there says
  // "Reclaim gives back", the in-place figure a cut-short plan does not
  // know (the judge's risk 1). Under the floor the bar is all data.
  export function segments(size: number, freeSpace: number, back: number, complete: boolean, striped: boolean): Seg[] {
    if (!(size > 0)) return [];
    const free = clampFree(size, freeSpace);
    const live = size - free;
    if (free < SPACE_FLOOR) return [{ kind: "live", grow: 1 }];
    const out: Seg[] = [];
    if (live > 0) out.push({ kind: "live", grow: live / size });
    if (!complete) return [...out, { kind: striped ? "back" : "track", grow: free / size }];
    let b = Math.max(0, Math.min(back, free));
    let only = free - b;
    if (b < SPACE_FLOOR) {
      b = 0;
      only = free;
    } else if (only < SPACE_FLOOR) {
      b = free;
      only = 0;
    }
    if (b > 0) out.push({ kind: "back", grow: b / size });
    if (only > 0) out.push({ kind: striped ? "only" : "track", grow: only / size });
    return out;
  }

  // shareText: FreeSpace over the file's size in whole percent, "<1 %"
  // below one percent (the judge's rule, tested before the rounding, so
  // that 0.6 % is not said as 1 %), never 100 % while any data is left.
  export function shareText(size: number, freeSpace: number): string {
    if (!(size > 0)) return "0 %";
    const free = clampFree(size, freeSpace);
    if (free === 0) return "0 %";
    if (free / size < 0.01) return "<1 %";
    const pct = Math.round((free / size) * 100);
    return `${free < size ? Math.min(pct, 99) : pct} %`;
  }

  // ---- the figure, read --------------------------------------------------
  //
  // under:       FreeSpace under the floor; nothing worth reclaiming.
  // pending:     no plan of this handle has landed yet.
  // incomplete:  the last plan was cut short, so it says nothing of Back.
  // reclaimable: a whole plan gives back the floor or more in place.
  // compactOnly: a whole plan gives back less than the floor: only Compact.
  export type SpaceKind = "under" | "pending" | "incomplete" | "reclaimable" | "compactOnly";

  export interface SpaceFig {
    size: number;
    freeSpace: number;
    reclaim?: ReclaimView | null;
  }

  export interface SpaceView {
    kind: SpaceKind;
    size: number;
    freeSpace: number;
    // What Reclaim gives back, after the fold; 0 but for reclaimable.
    back: number;
    // What only Compact reaches, after the fold; 0 under the floor and
    // while the figure is unknown.
    only: number;
    move: number;
    complete: boolean;
  }

  // spaceView reads one archive's figures. A plan whose AtSeq is behind
  // the archive's Seq is the last one, kept while the next is made, its
  // Back held to the unused space there is now.
  export function spaceView(f: SpaceFig): SpaceView {
    const size = Math.max(0, f.size || 0);
    const free = clampFree(size, f.freeSpace);
    const base = { size, freeSpace: free, back: 0, only: 0, move: 0, complete: false };
    if (free < SPACE_FLOOR) return { ...base, kind: "under", complete: true };
    const r = f.reclaim;
    if (!r) return { ...base, kind: "pending" };
    if (!r.complete) return { ...base, kind: "incomplete", move: r.move };
    const back = Math.min(r.back, free);
    if (back < SPACE_FLOOR) return { ...base, kind: "compactOnly", only: free, move: r.move, complete: true };
    const only = free - back;
    return only < SPACE_FLOOR
      ? { ...base, kind: "reclaimable", back: free, move: r.move, complete: true }
      : { ...base, kind: "reclaimable", back, only, move: r.move, complete: true };
  }

  // The bar's four inputs for a view.
  export function barOf(v: SpaceView): { size: number; freeSpace: number; back: number; complete: boolean } {
    return { size: v.size, freeSpace: v.freeSpace, back: v.back, complete: v.complete };
  }

  // volumeOf: the drive a path is on, as Windows names it — "D:", a share
  // "\\nas\backups" — or "its drive" when the path names neither.
  export function volumeOf(path: string): string {
    const drive = /^([A-Za-z]:)[\\/]/.exec(path);
    if (drive) return drive[1].toUpperCase();
    const share = /^(\\\\[^\\/]+[\\/][^\\/]+)/.exec(path);
    return share ? share[1] : spaceCopy.itsDrive;
  }

  // ---- the Details dialog's block (D) ------------------------------------

  export interface RunFig {
    done: number;
    total: number;
    returned: number;
  }

  export interface BlockIn {
    open: boolean;
    // The archive's size: the open handle's, or the registry's last stored
    // size for a closed one, which bounds Compact's need.
    size: number;
    freeSpace: number;
    reclaim?: ReclaimView | null;
    compactNeeds: number;
    path: string;
    // An open archive whose figure is not to hand: a compaction holds it
    // and Details was opened after it began, so no last figure was kept.
    // The block then says nothing of the archive being closed; Compact's
    // greyed hint says what holds it.
    held?: boolean;
    // A reclaim of the archive running, requested or the core's own, and
    // its busy reason ("Reclaiming space… wait for it to end").
    run?: RunFig | null;
    runWhy?: string;
    // Another operation holding the archive — a compaction included —
    // by its busy reason.
    busyWhy?: string;
    // The vault's own stops: Reclaim's (changes off), Compact's (locked,
    // changes off, a forgotten record).
    reclaimWhy?: string;
    compactWhy?: string;
  }

  export interface BlockRow {
    disabled: boolean;
    // The hint beside the button: its cost, or why it is greyed.
    hint: string;
  }

  export type LegendKind = SegKind | "unsplit";

  export interface BlockOut {
    // The bar's inputs; null for a closed archive, which has no figure.
    bar: { size: number; freeSpace: number; back: number; complete: boolean } | null;
    share: string;
    // The one line: the figure strong, then the rest.
    strong: string;
    rest: string;
    legend: { kind: LegendKind; label: string }[];
    // Reclaim's row, or Cancel in its place while a run goes on (cancel);
    // null for a closed archive, whose only action is Compact.
    reclaim: (BlockRow & { cancel: boolean }) | null;
    // The run's progress line's fill, 0 to 1, or null while its total is
    // not known; undefined when no run goes on.
    progress?: number | null;
    compact: BlockRow;
  }

  function legendOf(v: SpaceView): { kind: LegendKind; label: string }[] {
    const L = spaceCopy.legend;
    const live = { kind: "live" as const, label: L.live };
    switch (v.kind) {
      case "under":
        return [live];
      case "pending":
      case "incomplete":
        return [live, { kind: "unsplit", label: L.unsplit }];
      case "compactOnly":
        return [live, { kind: "only", label: L.only }];
      case "reclaimable":
        return v.only > 0 ? [live, { kind: "back", label: L.back }, { kind: "only", label: L.only }] : [live, { kind: "back", label: L.back }];
    }
  }

  function lineOf(v: SpaceView): { strong: string; rest: string } {
    if (v.kind === "under") return { strong: "", rest: spaceCopy.underFloor(bytes(v.size)) };
    const strong = bytes(v.freeSpace);
    const of = spaceCopy.unusedOf(bytes(v.size));
    switch (v.kind) {
      case "reclaimable":
        return { strong, rest: of + (v.only > 0 ? spaceCopy.someBack(bytes(v.back)) : spaceCopy.allBack) };
      case "compactOnly":
        return { strong, rest: of + spaceCopy.noneBack };
      default:
        return { strong, rest: of };
    }
  }

  // spaceBlock: the block's every state (the judge's table "D's states").
  export function spaceBlock(b: BlockIn): BlockOut {
    const vol = volumeOf(b.path);
    const compactCost = b.compactNeeds > 0 ? spaceCopy.compactHint(bytes(b.compactNeeds), vol) : spaceCopy.compactHintClosed(bytes(Math.max(0, b.size)), vol);
    if (!b.open) {
      const why = b.busyWhy ?? b.compactWhy;
      return { bar: null, share: "", strong: "", rest: b.held ? "" : spaceCopy.closed, legend: [], reclaim: null, compact: { disabled: why !== undefined, hint: why ?? compactCost } };
    }
    const v = spaceView(b);
    const head = { bar: barOf(v), share: v.kind === "under" ? "" : spaceCopy.share(shareText(v.size, v.freeSpace)), ...lineOf(v), legend: legendOf(v) };
    if (b.run) {
      const r = b.run;
      const total = r.total > 0 ? bytes(r.total) : "";
      const hint = spaceCopy.running(bytes(Math.min(r.done, r.total > 0 ? r.total : r.done)), total, bytes(r.returned));
      const why = b.runWhy ?? b.busyWhy ?? b.compactWhy;
      return {
        ...head,
        reclaim: { cancel: true, disabled: false, hint },
        progress: r.total > 0 ? Math.max(0, Math.min(1, r.done / r.total)) : null,
        compact: { disabled: why !== undefined, hint: why ?? compactCost },
      };
    }
    const own = v.kind === "under" ? spaceCopy.nothingToReclaim : v.kind === "compactOnly" ? spaceCopy.nothingBack : undefined;
    const reclaimWhy = b.busyWhy ?? b.reclaimWhy ?? own;
    const cost = v.kind === "reclaimable" ? spaceCopy.reclaimHint(bytes(v.move)) : spaceCopy.reclaimHintOpen;
    const compactWhy = b.busyWhy ?? b.compactWhy;
    return {
      ...head,
      reclaim: { cancel: false, disabled: reclaimWhy !== undefined, hint: reclaimWhy ?? cost },
      compact: { disabled: compactWhy !== undefined, hint: compactWhy ?? compactCost },
    };
  }

  // ---- the pane's row (P1) and the panel's open facts ---------------------

  export interface RowOut {
    // The 4 px bar and the share beside the figure; neither under the floor.
    bar: boolean;
    share: string;
    // The line under the bar: the Reclaim space link, today's "(Compact to
    // reclaim)", or the run's note; null only under the floor.
    line: { kind: "link"; title: string; disabled: boolean } | { kind: "hint" | "note"; text: string } | null;
  }

  // spaceRow: the row "Unused space" (the judge's P1 table). Below the
  // floor the row is today's, the figure alone: the control is hidden
  // there, never greyed, so the pane's frame looks the same whenever
  // nothing can be given back (the owner's ruling of 2026-11-04). A figure
  // pending or cut short offers the link as the Details dialog offers the
  // run, titled with what it does rather than a figure not known (the
  // judge's P1 table, "Incomplete"), over a bar that draws its unused
  // space as plain track (segments). busyWhy greys the link while another
  // operation holds the archive, its reason as the title.
  export function spaceRow(v: SpaceView, run: RunFig | null | undefined, busyWhy?: string): RowOut {
    if (v.kind === "under") return { bar: false, share: "", line: null };
    const share = shareText(v.size, v.freeSpace);
    if (run) return { bar: true, share, line: { kind: "note", text: spaceCopy.rowRunning(bytes(run.returned)) } };
    switch (v.kind) {
      case "reclaimable":
        return { bar: true, share, line: { kind: "link", title: busyWhy ?? spaceCopy.linkTitle(bytes(v.move)), disabled: busyWhy !== undefined } };
      case "compactOnly":
        return { bar: true, share, line: { kind: "hint", text: paneCopy.compactHint } };
      default:
        return { bar: true, share, line: { kind: "link", title: busyWhy ?? spaceCopy.linkTitleOpen, disabled: busyWhy !== undefined } };
    }
  }

  // panelLine: the Archives page's collapsed panel, after the name — what
  // Reclaim would give back when a whole plan says it is worth it, or the
  // run; nothing for a closed archive (null), which has no figure.
  export function panelLine(v: SpaceView | null, running: boolean): string {
    if (running) return spaceCopy.panelRunning;
    if (v?.kind === "reclaimable") return spaceCopy.reclaimable(bytes(v.back));
    return "";
  }
</script>

<script lang="ts">
  // The bar itself: a flex row of one segment per kind with a 1 px gap of
  // the ground between them (the surface it stands on, --spacebar-gap) and
  // rounded ends; 8 px and striped in the Details dialog (tall), 4 px
  // elsewhere. It is aria-hidden: the words beside it carry the figure, so
  // Narrator does not read it twice. No width animation — the bar is
  // redrawn when the status refreshes — and never red: unused space is no
  // danger. app.css draws it, the contrast themes included.
  interface Props {
    size: number;
    freeSpace: number;
    back: number;
    complete: boolean;
    tall?: boolean;
  }
  let { size, freeSpace, back, complete, tall = false }: Props = $props();
  const segs = $derived(segments(size, freeSpace, back, complete, tall));
</script>

<span class="spacebar" class:tall aria-hidden="true">{#each segs as s (s.kind)}<i class="seg {s.kind}" style:flex-grow={s.grow}></i>{/each}</span>

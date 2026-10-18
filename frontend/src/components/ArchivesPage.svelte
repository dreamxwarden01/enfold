<script lang="ts">
  // The Archives page is the vault's inspector (APP.md §13): it lists
  // registry records, not files. Name carries the description as its
  // muted second line; Size is right-aligned so magnitudes line up; there
  // is no Files column and no Key column since 2026-10-14 — a count the
  // core has only for an open archive read "—" on every row, and the key
  // version is Technical details'. Status carries what the row is, in the
  // chips' words (chipCopy, ruled 2026-10-17) — Open, Compacting, Missing,
  // Hidden, Forgotten — and *Missing* is only ever the core's own note, so
  // the cell is blank until a presence pass has run and never says
  // "missing" about a path nobody has measured. The header line sums the
  // rows shown and says so, beside the vault file's own size and the
  // registry's date (§2.1). Every label is the verb table's or the
  // dialog template's (strings.ts; APP.md §7, ruled 2026-10-17).
  import { tick, untrack } from "svelte";
  import { Archives, Code, Shell, errorOf } from "../lib/api";
  import type { ArchiveSummary } from "../lib/api";
  import { store } from "../lib/state.svelte";
  import { archivesCopy, chipCopy, codeText, commandCopy, dialogCopy, importCopy, listCopy, vaultCopy } from "../lib/strings";
  import { bytes, count, date, dateTime, plural } from "../lib/format";
  import { clickRow, emptySelection, selectAll, survive, toggleRow } from "../lib/selection";
  import type { Selection } from "../lib/selection";
  import { clickShownHeader, compacting, fingerprintOf, paneRow, rowRefusal, shownTicked } from "../lib/archlist";
  import { CLOSED, canExpand, collapse, edgeY, escape, inContact, naturalEnd, openHeight, overScroll, picked, pushScroll, reveals, rideReachesHome, rideScroll, springScroll, toggle } from "../lib/archbar";
  import type { BarState, ListMetrics } from "../lib/archbar";
  import { MOVE, enter, motion } from "../lib/motion";
  import { dialogIsUp } from "../lib/dialogs";
  import { freeWorthShowing } from "../lib/status";
  import { DEFAULT_METHOD, METHODS, METHOD_NOTE, methodWord } from "../lib/method";
  import { foreignPath } from "../lib/paths";
  import { purgeAfter } from "../lib/retention";
  import { DESCRIPTION_RULE, descriptionProblem, nameProblem } from "../lib/validate";
  import Dialog from "./Dialog.svelte";
  import Segmented from "./Segmented.svelte";
  import TextField from "./TextField.svelte";
  import OpsBar from "./OpsBar.svelte";
  import SaveBar from "./SaveBar.svelte";
  import type { PendingItem } from "./SaveBar.svelte";
  import ArchiveDetailsDialog from "./ArchiveDetailsDialog.svelte";
  import DeleteArchiveDialog from "./DeleteArchiveDialog.svelte";
  import MergeDialog from "./MergeDialog.svelte";
  import TamperedBar from "./TamperedBar.svelte";

  // The selection (APP.md §6, ruled 2026-09-10): the ticks, under the file
  // list's rules (lib/selection.ts) over the rows the filter shows, and
  // the row last clicked, which the details pane shows while it is ticked
  // — else the sole ticked row, else nothing but a count (lib/archlist.ts).
  // Kept on the page: nothing here pages or restarts under it.
  let sel = $state<Selection>(emptySelection());
  let lastClicked = $state<string | null>(null);
  const paneId = $derived(paneRow(sel, lastClicked));
  const pane = $derived(store.archives.find((a) => a.id === paneId) ?? null);
  // one: the archive an action that takes one archive works on — the
  // pane's row, and only while exactly one is ticked; every such action
  // is greyed otherwise, the pane's own buttons included.
  const one = $derived(sel.ids.size === 1 ? pane : null);
  const st = $derived(store.status);
  const unlocked = $derived(store.unlocked);
  // The banner's one predicate (store.tampered; APP.md §6).
  const tampered = $derived(store.tampered);
  // The archives a lock left open, by name (direction 9; APP.md §2.3).
  const opened = $derived(st?.opened ?? []);
  const openedNames = $derived(opened.map((a) => a.name));
  // The record read whole, and only when it is the pane's: a reply for a
  // record the user has left is not this pane's (APP.md §13).
  const det = $derived(store.details && pane && store.details.archiveId === pane.id ? store.details : null);
  // The pane's Fingerprint fact (direction 9): null — no row — until the
  // record is read, and so always while Locked (lib/archlist.ts).
  const fp = $derived(pane ? fingerprintOf(pane, det) : null);

  // pick is what a create or a delete begun elsewhere does: that one row
  // ticked, clicked, and so in the pane.
  function pick(id: string) {
    sel = { ids: new Set([id]), anchor: id };
    lastClicked = id;
  }

  let creating = $state(false);
  let newName = $state("");
  // The compression method the archive is created with (FORMAT.md §7.1,
  // the ruling of 2026-09-08): chosen here, carried in the record, obeyed
  // by every later writer. Normal is preselected.
  let newMethod = $state<string>(DEFAULT_METHOD);
  const methodOptions = METHODS.map((m) => ({ value: m.value, label: m.word }));
  let newNameValid = $state(true);
  let newAttempt = $state(0);
  // The path is never a field: *Create archive…* opens the save dialog, and a place
  // that already holds a file is refused here rather than overwritten
  // (APP.md §6). The refused name is kept, so the line goes the moment the
  // name is changed and never outlives what it is about.
  let newExists = $state("");
  const existsShown = $derived(!!newExists && newName.trim() === newExists);
  // A consequence dialog (the dialog template, ruled 2026-10-17): the
  // question, the body, the primary that repeats the command's verb, and
  // the mechanism, when there is one worth saying, behind *How this works*.
  let confirm = $state<null | { title: string; body: string; how?: string; button: string; run: () => Promise<unknown> }>(null);
  let showDetails = $state(false);
  // The Forget/Delete dialog runs off a snapshot of the record, never off
  // the live row: a forgotten record leaves the list the moment the write
  // lands (it is listed only under Show hidden), and a dialog gated on the
  // selection would unmount with it — taking the outcome screen and the
  // purge date it names with it (APP.md §13).
  interface DeleteAsk {
    id: string;
    name: string;
    path: string;
    createdAt: number;
    mode: "delete" | "forget";
  }
  let del = $state<DeleteAsk | null>(null);
  let merging = $state("");
  let saving = $state(false);

  // The table: a filter over name, description and file name, then one
  // sortable column.
  type SortKey = "name" | "size" | "saved" | "status";
  let filter = $state("");
  let sortKey = $state<SortKey>("name");
  let sortAsc = $state(true);

  function fail(e: unknown) {
    store.toast(codeText(errorOf(e).code), "error");
  }

  async function run(p: Promise<unknown>) {
    try {
      await p;
    } catch (e) {
      fail(e);
    }
  }

  // ---- the Status column ------------------------------------------------
  interface Status {
    label: string;
    tone: string;
    title?: string;
  }

  function statuses(a: ArchiveSummary): Status[] {
    const out: Status[] = [];
    if (a.forgottenAt > 0) out.push({ label: chipCopy.forgotten, tone: "forgotten", title: chipCopy.forgottenOn(date(a.forgottenAt)) });
    // Nothing is "dirty" since 2026-09-09: an archive is clean between
    // operations, each of them its own transaction (APP.md §2.3).
    // A compaction refuses the open and every operation until it ends, so
    // the row says so beside the greyed commands (ruled 2026-10-14).
    if (compacting(a, store.runningOps)) out.push({ label: chipCopy.compacting, tone: "busy", title: codeText(Code.CodeArchiveCompacting) });
    else if (a.open) out.push({ label: chipCopy.open, tone: "open" });
    // Never computed here: the core keeps a presence per record and the
    // note is the only thing that says a file is gone (APP.md §13).
    if (a.note === "archive.file_missing") out.push({ label: chipCopy.missing, tone: "missing", title: codeText(a.note) });
    if (a.hidden) out.push({ label: chipCopy.hidden, tone: "hidden" });
    return out;
  }

  function statusRank(a: ArchiveSummary): number {
    if (a.forgottenAt > 0) return 5;
    if (a.note === "archive.file_missing") return 4;
    if (compacting(a, store.runningOps)) return 3;
    if (a.open) return 2;
    if (a.hidden) return 1;
    return 0;
  }

  const rows = $derived.by(() => {
    const q = filter.trim().toLowerCase();
    const list = store.archives.filter((a) => !q || a.name.toLowerCase().includes(q) || (a.description ?? "").toLowerCase().includes(q) || a.path.toLowerCase().includes(q));
    const dir = sortAsc ? 1 : -1;
    return [...list].sort((a, b) => {
      switch (sortKey) {
        case "size":
          return dir * (a.storedSize - b.storedSize);
        case "saved":
          return dir * (a.lastWrittenAt - b.lastWrittenAt);
        case "status":
          return dir * (statusRank(a) - statusRank(b) || a.name.localeCompare(b.name));
      }
      return dir * a.name.localeCompare(b.name);
    });
  });

  // vaultEmpty: the vault holds no record at all, hidden and forgotten
  // ones included — what the empty page's buttons are for (ruled
  // 2026-10-14). The list itself cannot say so while Show hidden is off,
  // so the registry is asked once more, only while that list is empty.
  // null is "not known yet": the empty list says nothing until the answer
  // comes, rather than flashing a line about Show hidden that may be
  // untrue, and a refused question leaves it saying nothing.
  let vaultEmpty = $state<boolean | null>(false);
  $effect(() => {
    const none = unlocked && store.archives.length === 0;
    const all = store.showHidden;
    if (!none || all) {
      vaultEmpty = none;
      return;
    }
    vaultEmpty = null;
    let live = true;
    Archives.List(true)
      .then((every) => {
        if (live) vaultEmpty = (every ?? []).length === 0;
      })
      .catch(() => {});
    return () => {
      live = false;
    };
  });

  // Summed by the page over the rows shown, and labelled so: a total that
  // does not match the visible rows reads as a bug (APP.md §13).
  const shownSize = $derived(rows.reduce((n, a) => n + (a.storedSize || 0), 0));

  // The rows as they stand on screen, top to bottom: what Shift ranges
  // over, what the header and Ctrl+A tick.
  const order = $derived(rows.map((a) => a.id));
  const allTicked = $derived(shownTicked(sel, order));

  // A row that left the list — forgotten, or gone with a lock — leaves
  // the ticks too; a row the filter merely hides keeps its tick.
  $effect(() => {
    const live = new Set(store.archives.map((a) => a.id));
    untrack(() => {
      const next = survive(sel, live);
      if (next !== sel) sel = next;
      if (lastClicked !== null && !live.has(lastClicked)) lastClicked = null;
    });
  });

  // The row's own click (lib/selection.ts): plain selects one and sets the
  // anchor, Ctrl toggles, Shift ranges from the anchor. Whichever it was,
  // this is the row last clicked.
  function click(e: MouseEvent, id: string) {
    sel = clickRow(sel, order, id, { ctrl: e.ctrlKey || e.metaKey, shift: e.shiftKey });
    lastClicked = id;
  }

  // A row's checkbox toggles that row alone and makes it the anchor; the
  // row click's own rules do not run (APP.md §6).
  function tick1(e: Event, id: string) {
    e.stopPropagation();
    sel = toggleRow(sel, id);
    lastClicked = id;
  }

  // The header's checkbox ticks the rows the filter shows, and clears
  // when they are all ticked.
  function tickAll(e: Event) {
    e.stopPropagation();
    sel = clickShownHeader(sel, order);
  }

  // The list's blank area — under the last row, beside the table — clears
  // the ticks and the anchor; a header cell sorts and is not blank.
  function blank(e: MouseEvent) {
    const t = e.target as HTMLElement;
    if (!t.closest("tbody tr") && !t.closest("thead")) {
      sel = emptySelection();
      lastClicked = null;
    }
  }

  // Ctrl+A ticks the rows the filter shows when the focus is in the list
  // or on the page's body — never in the filter input, which keeps its
  // own select-all — and not while a dialog is up (lib/dialogs.ts).
  let listEl = $state<HTMLDivElement | undefined>();
  function shortcut(e: KeyboardEvent) {
    // Esc collapses the panel (APP.md §6, ruled 2026-09-11). A dialog's
    // Esc is the dialog's, and a key that moves nothing here is left
    // alone rather than swallowed.
    if (e.key === "Escape") {
      if (dialogIsUp()) return;
      const next = escape(bar);
      if (next === bar) return;
      e.preventDefault();
      moveBar(next);
      return;
    }
    // Ctrl+O is Open file… from anywhere on the page, the filter included
    // — it edits no text (ruled 2026-10-14).
    if ((e.ctrlKey || e.metaKey) && !e.altKey && !e.shiftKey && (e.key === "o" || e.key === "O")) {
      if (dialogIsUp()) return;
      e.preventDefault();
      void openFile();
      return;
    }
    if (!(e.ctrlKey || e.metaKey) || e.altKey || e.shiftKey || (e.key !== "a" && e.key !== "A")) return;
    const t = e.target as HTMLElement | null;
    if (!t || t.closest("input, textarea, select, [contenteditable]")) return;
    if (t !== document.body && !listEl?.contains(t)) return;
    if (dialogIsUp()) return;
    e.preventDefault();
    sel = selectAll(sel, order);
  }

  // ---- the bottom panel (lib/archbar.ts) --------------------------------
  // Below the stylesheet's threshold the details are one element at the
  // foot of the body whose header is the whole of it when collapsed
  // (APP.md §6, refined 2026-09-12). The threshold is the stylesheet's
  // and is never written here: the probe is a pixel wide below it and
  // nothing above it, and one observer reads that answer — the header is
  // a control, and the panel's growth is measured, only there.
  let bar = $state<BarState>(CLOSED);
  let cardEl = $state<HTMLElement | undefined>(); // .details, the card
  let innerEl = $state<HTMLDivElement | undefined>(); // its fixed inner
  let headEl = $state<HTMLDivElement | undefined>();
  // The header's two shapes: the stacked heading and the collapsed line,
  // whose cross-fade the movement drives frame by frame (see place).
  let stackEl = $state<HTMLDivElement | undefined>();
  let lineEl = $state<HTMLDivElement | undefined>();
  let paneBodyEl = $state<HTMLDivElement | undefined>();
  let spacerEl = $state<HTMLDivElement | undefined>();
  let bodyEl = $state<HTMLDivElement | undefined>(); // the layer's body
  let splitEl = $state<HTMLDivElement | undefined>(); // the list and the panel's box
  let probeEl = $state<HTMLElement | undefined>();
  let narrow = $state(false);
  const headLabel = $derived(!pane ? archivesCopy.panelNone : bar.open ? archivesCopy.panelHide(pane.name) : archivesCopy.panelShow(pane.name));

  // What the movement is doing, none of it reactive: the page writes these
  // to the DOM and reads them back from here, never from the layout.
  let frame = 0; // the one frame clock
  let cardH = 0; // the card's height as last written
  let over = 0; // the spacer's room, which never shrinks while the panel is up
  let held = false; // the list's viewport is pinned
  let scrollBefore = 0; // where the list stood before the opening

  // The three numbers the stylesheet owns — the collapsed line, the open
  // header, the air below the card — so the threshold's arithmetic stays
  // in one place. Read once and kept: resolving a style in the click's own
  // handler is time the movement does not have, and they change only with
  // the stylesheet, which the breakpoint re-reads for.
  let sizes: { line: number; head: number; foot: number } | null = null;
  function tokens(): { line: number; head: number; foot: number } {
    if (sizes) return sizes;
    const css = cardEl ? getComputedStyle(cardEl) : null;
    const px = (n: string) => (css ? parseFloat(css.getPropertyValue(n)) || 0 : 0);
    sizes = { line: px("--panel-line"), head: px("--panel-head"), foot: px("--panel-foot") };
    return sizes;
  }

  // measure: every number the movement needs, read in one pass. Nothing
  // is written until the last of them is in hand — a read after a write
  // inside the click's own handler cost three frames in the first build
  // (DECISIONS, "The panel's frame cost, measured"). boxH is the
  // scroller's rendered height, which the hold pins it at.
  function measure(id: string): { m: ListMetrics; boxH: number } | null {
    const wrap = listEl;
    const card = cardEl;
    const layer = bodyEl;
    const row = wrap?.querySelector<HTMLElement>(`tbody tr[data-id="${id}"]`);
    const table = wrap?.querySelector("table");
    if (!wrap || !card || !layer || !row || !table) return null;
    const t = tokens();
    const box = wrap.getBoundingClientRect();
    const rect = row.getBoundingClientRect();
    const listTop = box.top + wrap.clientTop;
    const tail = wrap.querySelector<HTMLElement>(".list-tail");
    return {
      boxH: box.height,
      m: {
        listTop,
        viewport: wrap.clientHeight,
        // What the scroller holds, which scrollHeight cannot say: that is
        // never less than the viewport, and a short list's blank below the
        // rows is exactly what the spacer has to cover.
        content: table.getBoundingClientRect().height + (tail?.getBoundingClientRect().height ?? 0),
        headHeight: wrap.querySelector("thead")?.getBoundingClientRect().height ?? 0,
        rowTop: rect.top - listTop + wrap.scrollTop,
        rowHeight: rect.height,
        footY: layer.getBoundingClientRect().bottom - t.foot,
        line: t.line,
        head: t.head,
        scroll: wrap.scrollTop,
      },
    };
  }

  const ROW = 36; // a row's height, the one the list keeps as its tail (app.css)

  // bare: the same read with no row to push — the filter took the panel's
  // row out of the list. The panel still opens to the height the first
  // slot gives, measured with a nominal row: the first row the list does
  // show, else a row's own height. Nothing is pushed, so the metrics say
  // a list that neither scrolls nor needs a spacer (APP.md §6).
  function bare(): { m: ListMetrics; boxH: number } | null {
    const wrap = listEl;
    const layer = bodyEl;
    if (!wrap || !layer || !cardEl) return null;
    const t = tokens();
    const box = wrap.getBoundingClientRect();
    const listTop = box.top + wrap.clientTop;
    const headHeight = wrap.querySelector("thead")?.getBoundingClientRect().height ?? 0;
    const rowHeight = wrap.querySelector<HTMLElement>("tbody tr")?.getBoundingClientRect().height ?? ROW;
    return {
      boxH: box.height,
      m: {
        listTop,
        viewport: wrap.clientHeight,
        content: wrap.clientHeight,
        headHeight,
        rowTop: headHeight, // the first slot itself: there is nothing to push
        rowHeight,
        footY: layer.getBoundingClientRect().bottom - t.foot,
        line: t.line,
        head: t.head,
        scroll: wrap.scrollTop,
      },
    };
  }

  // place: the values a frame of the motion writes, each to the element's
  // own style — never an inherited custom property, which restyles the
  // whole subtree for every frame. The header's two shapes cross-fade
  // here, as a function of the card's height, rather than by a transition
  // of their own: one frame clock for the whole movement (APP.md §6), so
  // a reversal part-way, or a resettle that snaps the card, can never
  // leave the header fading on another timeline. A null scroll is a list
  // this movement does not touch. Nothing is read here.
  function place(h: number, s: number | null) {
    cardH = h;
    const t = sizes ?? tokens();
    const k = Math.min(1, Math.max(0, (h - t.line) / Math.max(1, t.head - t.line)));
    if (cardEl) cardEl.style.height = `${h}px`;
    if (stackEl) stackEl.style.opacity = `${k}`;
    if (lineEl) lineEl.style.opacity = `${1 - k}`;
    if (s !== null && listEl) listEl.scrollTop = s;
  }

  // The list's viewport is pinned at its rendered height while the panel
  // is up, so the spacer scrolls a short list instead of growing it
  // (APP.md §6; the first build grew it, and the last row never rose).
  function hold(boxH: number) {
    if (listEl) listEl.style.height = `${boxH}px`;
    held = true;
  }

  function release() {
    if (held && listEl) listEl.style.removeProperty("height");
    held = false;
  }

  // The spacer after the table: the room the push scrolls into past the
  // list's own end. It never shrinks while the panel is up — a taller
  // row's need grows it, and a list already scrolled into it would jump
  // if it went the other way.
  function spacer(px: number) {
    over = px;
    if (!spacerEl) return;
    spacerEl.hidden = px <= 0;
    spacerEl.style.height = `${px}px`;
  }

  // One frame clock for both halves of the movement: the card's height
  // and the list's scroll are the same gesture in two places, and the row
  // has to ride just above the edge that pushes it. A CSS transition on
  // the height and a smooth scroll on the list would each run to their
  // own clock, and the row would fall behind the edge or run ahead of it.
  // The scroll is a function of the height here, so the two cannot drift,
  // and a movement with no list to move hands no function at all.
  // Reduced motion puts both at their targets at once, as every other
  // transition does; an interruption cancels and places (see the callers).
  function glide(to: number, scrollAt: ((h: number, k: number) => number) | null, ended?: () => void) {
    cancelAnimationFrame(frame);
    frame = 0;
    const from = cardH;
    const ms = motion(MOVE).duration;
    if (ms === 0) {
      place(to, scrollAt ? scrollAt(to, 1) : null);
      ended?.();
      return;
    }
    const began = performance.now();
    const step = (now: number) => {
      const k = Math.min(1, (now - began) / ms);
      const e = enter(k);
      const h = from + (to - from) * e;
      place(h, scrollAt ? scrollAt(h, e) : null);
      if (k < 1) {
        frame = requestAnimationFrame(step);
      } else {
        frame = 0;
        ended?.();
      }
    };
    frame = requestAnimationFrame(step);
  }

  // raise: the opening. Read everything, then write — the held viewport,
  // the spacer, the inner's height, the card's own, the state the header
  // announces — and let the first scroll go in the first frame, so the
  // handler forces no layout of its own. A row the filter has taken out
  // of the list opens the card all the same, at the height the first slot
  // gives, with nothing to push: no spacer, and the list left alone
  // (APP.md §6).
  function raise(id: string) {
    const inner = innerEl;
    const r = measure(id);
    const got = r ?? bare();
    if (!got || !cardEl || !inner) return;
    const { m, boxH } = got;
    const H = openHeight(m);
    const from = m.scroll;
    scrollBefore = from;
    if (!held) hold(boxH);
    if (r) spacer(Math.max(over, overScroll(m)));
    // The inner is laid out once, here: the card's growth over it is the
    // only thing the frames touch.
    inner.style.height = `${H}px`;
    if (cardH <= 0) cardH = m.line;
    place(cardH, null); // the card's own starting height, the header with it
    glide(H, r ? (h) => rideScroll(m, from, edgeY(m, h)) : null);
  }

  // shift: another row picked while the panel is up, or a sort, a filter
  // or a refresh that moved the row it names. The list comes to the card
  // rather than the other way about, so this is the scroll, eased from
  // where it stands to that row's first slot — but the card is driven to
  // the open height with it: an opening glide interrupted here would
  // otherwise take its own half-grown height for the final one and leave
  // the panel part-way open.
  function shift(id: string) {
    const r = measure(id);
    const inner = innerEl;
    if (!r || !inner) return;
    const { m, boxH } = r;
    if (!held) hold(boxH);
    spacer(Math.max(over, overScroll(m)));
    const H = openHeight(m);
    inner.style.height = `${H}px`;
    const from = m.scroll;
    const to = pushScroll(m);
    glide(H, (_h, k) => from + (to - from) * k);
  }

  // lower: the collapse, a spring let go. The edge goes down and the row
  // rides it back until the list stands where it stood before the
  // opening. Two rows cannot ride it home and ease from where they are to
  // there instead (APP.md §6): one under a list the user scrolled by hand
  // while the panel was up, which is not in contact with the edge, and
  // one the ride would leave short — a row sorted far down the list while
  // the panel was up, whose foot is still below the collapsed card's edge
  // with the list at home. A row that has left the list altogether — the
  // vault locked under it — leaves the scroll to ease on its own.
  function lower(id: string | null) {
    const wrap = listEl;
    if (!wrap || !cardEl) return;
    const r = id ? measure(id) : null;
    const from = r ? r.m.scroll : wrap.scrollTop;
    const line = r ? r.m.line : tokens().line;
    // The body is inert from the first frame of the collapse (the
    // attribute follows bar.open), and a focus inside it comes out to the
    // header, which is the control that opens it again.
    if (paneBodyEl?.contains(document.activeElement)) headEl?.focus({ preventScroll: true });
    const done = () => {
      spacer(0);
      release();
    };
    if (r && inContact(r.m) && rideReachesHome(r.m, scrollBefore)) {
      const m = r.m;
      glide(line, (h) => springScroll(m, scrollBefore, edgeY(m, h)), done);
      return;
    }
    const to = Math.max(0, r ? Math.min(scrollBefore, naturalEnd(r.m)) : scrollBefore);
    glide(line, (_h, k) => from + (to - from) * k, done);
  }

  // resettle: the window, or a banner above the list, changed size under
  // an open panel. Every number is measured again — the held viewport let
  // go and pinned at the new size, the spacer asked for afresh — a
  // running motion is cancelled, and the row is placed at the first slot
  // with no second push. The writes go first and the reading waits for
  // the next frame: a rect read after a write, in the observer's own
  // callback, is the forced layout the ruling forbids (DECISIONS, "The
  // panel's frame cost, measured"), and a frame is not seen.
  function resettle(id: string) {
    if (!innerEl) return;
    cancelAnimationFrame(frame);
    release();
    spacer(0);
    frame = requestAnimationFrame(() => {
      frame = 0;
      const inner = innerEl;
      const r = measure(id);
      const got = r ?? bare();
      if (!got || !inner) return;
      const { m, boxH } = got;
      const H = openHeight(m);
      hold(boxH);
      // The row is gone from the list: the panel keeps its height over a
      // list with nothing to push (APP.md §6), so no spacer and no scroll.
      if (r) spacer(overScroll(m));
      inner.style.height = `${H}px`;
      place(H, r ? pushScroll(m) : null);
    });
  }

  // strand: the filter took the panel's row out of the list while a
  // motion was running. The loop is chasing a row that is gone, so it
  // stops; the card goes on to the open height by itself and the list
  // stays where the push left it (APP.md §6, nothing to push).
  function strand() {
    cancelAnimationFrame(frame);
    frame = 0;
    const got = bare();
    const inner = innerEl;
    if (!got || !inner) return;
    const H = openHeight(got.m);
    if (Math.abs(cardH - H) < 0.5) return;
    inner.style.height = `${H}px`;
    glide(H, null);
  }

  // clearPanel: every narrow-only inline style off the card, its inner,
  // the header's two shapes and the list. The wide layout is the side
  // pane again and must carry none of the panel's numbers.
  function clearPanel() {
    cancelAnimationFrame(frame);
    frame = 0;
    cardEl?.style.removeProperty("height");
    innerEl?.style.removeProperty("height");
    stackEl?.style.removeProperty("opacity");
    lineEl?.style.removeProperty("opacity");
    spacer(0);
    release();
    cardH = 0;
  }

  $effect(() => {
    const layer = bodyEl;
    const probe = probeEl;
    const wrap = listEl;
    const split = splitEl;
    if (!layer || !probe || !wrap || !split) return;
    const seen = new ResizeObserver(() => {
      const now = probe.getBoundingClientRect().width > 0;
      if (now !== narrow) {
        // The threshold crossed, either way, starts the panel down: going
        // wide the side pane is back and carries none of this, and coming
        // back narrow the panel is a collapsed line again. The narrow
        // layout makes the body inert and hides it, so a focus inside it
        // comes out to the header, which is the control that opens it —
        // once that layout has applied and the header can take it.
        const inBody = now && !!paneBodyEl?.contains(document.activeElement);
        narrow = now;
        sizes = null; // the stylesheet is asked again, once
        clearPanel();
        bar = collapse(bar);
        if (inBody) void tick().then(() => headEl?.focus({ preventScroll: true }));
        return;
      }
      if (narrow && bar.open && bar.id) resettle(bar.id);
    });
    seen.observe(layer);
    // The split as well as the body: a banner or an operation strip
    // appearing above the list moves the list down without changing the
    // size of the body it sits in — nor of the scroller, which the panel
    // holds at its height while it is up — and the card would be left
    // over the row.
    seen.observe(split);
    seen.observe(wrap);
    return () => seen.disconnect();
  });

  // Every move goes through here, so the movement is decided in one
  // place: the panel opening on a row, another row picked while it is up,
  // and the collapse that lets the list back down.
  function moveBar(next: BarState) {
    if (next === bar) return;
    const before = bar;
    bar = next;
    if (!narrow) return;
    if (reveals(before, next) && next.id) {
      if (before.open) shift(next.id);
      else raise(next.id);
      return;
    }
    if (before.open && !next.open) lower(before.id);
  }

  // The header is the control: a click anywhere on it, or Enter or Space
  // while it has the focus, opens it and closes it again. Nothing
  // selected is a dead control, not a missing one (aria-disabled).
  function headTap() {
    if (!narrow || !canExpand(bar)) return;
    moveBar(toggle(bar));
  }

  function headKey(e: KeyboardEvent) {
    if (e.key !== "Enter" && e.key !== " ") return;
    if (!narrow || !canExpand(bar)) return;
    e.preventDefault();
    moveBar(toggle(bar));
  }

  // The panel names the pane's row and no other: one selection, one name.
  // A row picked while it is up keeps it up and brings that row to the
  // top; nothing selected takes it down, the header being dead then.
  $effect(() => {
    const id = paneId;
    untrack(() => moveBar(picked(bar, id)));
  });

  // The list is reconciled, not left behind (APP.md §6): a sort, a filter
  // or a refresh that moved the selected row while the panel is up brings
  // it to the first slot again. What is watched is the order of the rows
  // on screen — the same row changes place with no change of id — and a
  // row the filter took out of the list leaves the panel open at its
  // height, there being nothing to push — and a motion still running for
  // that row is stranded rather than left chasing it.
  let placed = "";
  $effect(() => {
    const ids = order.join(" ");
    untrack(() => {
      const moved = ids !== placed;
      placed = ids;
      if (!moved || !narrow || !bar.open || !bar.id) return;
      if (!order.includes(bar.id)) {
        strand();
        return;
      }
      shift(bar.id);
    });
  });

  function sortOn(k: SortKey) {
    if (sortKey === k) sortAsc = !sortAsc;
    else {
      sortKey = k;
      sortAsc = k === "name";
    }
  }

  function ariaSort(k: SortKey): "ascending" | "descending" | "none" {
    return sortKey !== k ? "none" : sortAsc ? "ascending" : "descending";
  }

  // ---- the details pane's staged edits ----------------------------------
  // The draft is the store's, keyed by archive id, so it survives a visit
  // to another page; dirty is derived by diffing it against the record and
  // is never stored (APP.md §6).
  const draft = $derived(pane ? store.recordDraft[pane.id] : undefined);
  const nameChanged = $derived(!!draft && draft.name !== draft.base.name);
  const descChanged = $derived(!!draft && draft.description !== draft.base.description);
  // An edit takes one archive, like any action: greyed unless exactly one
  // is ticked (APP.md §6).
  const editable = $derived(!!one && unlocked && !tampered && one.forgottenAt === 0);

  const pending = $derived.by((): PendingItem[] => {
    if (!draft) return [];
    const items: PendingItem[] = [];
    if (nameChanged) items.push({ key: "name", label: `Name · ${draft.name.trim() || "(empty)"}` });
    if (descChanged) items.push({ key: "desc", label: draft.description ? "Description" : "Description cleared", tone: draft.description ? "add" : "remove" });
    return items;
  });

  const invalid = $derived.by(() => {
    if (!draft) return "";
    if (nameChanged && nameProblem(draft.name)) return nameProblem(draft.name);
    if (descChanged && descriptionProblem(draft.description)) return DESCRIPTION_RULE;
    return "";
  });

  function stage(a: ArchiveSummary) {
    const base = { name: a.name, description: a.description ?? "" };
    store.recordDraft[a.id] = { base, name: base.name, description: base.description };
  }

  // The baseline follows the record while the draft is untouched: a merge
  // that fills an empty description, another device's rename arriving over
  // archives.changed, a Locate — all move the row under a live selection,
  // and a stale baseline would raise the save bar for a change the user
  // never made and write the old value back on Save (APP.md §6). A draft
  // the user has touched is left alone: it is theirs.
  $effect(() => {
    const a = pane;
    if (!a) return;
    const d = store.recordDraft[a.id];
    if (!d) {
      stage(a);
      return;
    }
    const name = a.name;
    const description = a.description ?? "";
    if (d.base.name === name && d.base.description === description) return;
    if (d.name === d.base.name && d.description === d.base.description) stage(a);
  });

  // The record is read when the pane's row changes, not when the modal
  // opens and not on every advancing status (APP.md §13): loadDetails
  // reads store.unlocked before its first await, so an unguarded call
  // would make every vault.state event re-issue Archives.Details.
  // The record is read again when the row's hashBehind moves — a save, a
  // Refresh fingerprint, a Compact — so the Fingerprint fact and Technical
  // details never stand on a record older than the summary for longer
  // than one Details call, and again on an unlock, which brings the
  // record back after a lock dropped it. A primitive key, so a list refresh that leaves
  // the row as it was reads nothing.
  const detailsKey = $derived(paneId ? `${paneId}:${pane?.hashBehind ?? 0}:${pane?.receiptOwed ?? false}:${store.unlocked}` : "");
  $effect(() => {
    void detailsKey;
    untrack(() => void store.loadDetails(paneId));
  });

  async function saveRecord() {
    const a = one;
    const d = draft;
    if (!a || !d) return;
    saving = true;
    try {
      if (nameChanged) await Archives.Rename(a.id, d.name.trim());
      if (descChanged) await Archives.SetDescription(a.id, d.description);
      await store.refreshArchives();
      await store.loadDetails(a.id);
      const fresh = store.archives.find((x) => x.id === a.id);
      if (fresh) stage(fresh);
    } catch (e) {
      fail(e); // the draft stays, so Save can be pressed again (APP.md §6)
    }
    saving = false;
  }

  // ---- the commands -----------------------------------------------------
  function openCreate() {
    newName = "";
    newMethod = DEFAULT_METHOD;
    newAttempt = 0;
    newExists = "";
    creating = true;
  }

  // create asks for the name and the method here and for the place in the
  // native save dialog, prefilled with <name>.efd in the folder last used
  // (APP.md §6). A cancelled dialog creates nothing and leaves this one
  // standing; only a create that succeeded closes it, and the new archive
  // is then opened on its empty root, where the next thing to do is to
  // add to it (ruled 2026-10-14) — not left as a ticked row.
  async function create() {
    newAttempt++;
    if (!newNameValid) return;
    const name = newName.trim();
    const p = await Shell.SaveFile("Where to keep the archive", `${name}.efd`, store.settings?.lastArchiveFolder ?? "");
    if (!p) return;
    try {
      const id = await Archives.Create(p, name, newMethod);
      creating = false;
      newExists = "";
      await store.refreshArchives();
      await store.refreshSettings(); // the folder this create remembered
      // A refused open (the vault locked meanwhile) leaves the row ticked
      // here instead, once it is in the list, or the prune would drop it.
      if (!(await store.openNew(id))) pick(id);
    } catch (e) {
      const code = errorOf(e).code;
      // Said in place, with the name field to correct: Enfold never
      // overwrites a file it did not make, whatever the dialog offered.
      if (code === Code.CodeArchiveExists) {
        newExists = name;
        document.getElementById("na-name")?.focus();
        return;
      }
      fail(e);
    }
  }

  async function locate(a: ArchiveSummary) {
    const p = (await Shell.PickFiles(`Where is ${a.name} now?`, false)) ?? [];
    if (p.length) {
      await run(Archives.Locate(a.id, p[0]));
      await store.refreshArchives();
      await store.loadDetails(a.id);
    }
  }

  // Open file… (Ctrl+O, APP.md §14, ruled 2026-10-14): an .efd from
  // anywhere, through the picker *Import archives from a backup…* uses, opened by the
  // chain Explorer's double-click takes — the envelope's archive_id, the
  // lock scene first while locked, the key's own dialog when no record
  // here holds it.
  async function openFile() {
    const p = (await Shell.PickFiles(archivesCopy.openFileTitle, false)) ?? [];
    if (p.length) await store.openFiles(p);
  }

  async function importRecords() {
    const p = (await Shell.PickFiles(importCopy.archivesTitle, false)) ?? [];
    if (p.length) merging = p[0];
  }

  // *Import archives from a backup…* asked for from the dialog a file opened from Explorer
  // put up (APP.md §14): its key is not in this vault, and the picker and
  // the merge dialog live here.
  $effect(() => {
    if (!store.importRecordsAsked) return;
    store.importRecordsAsked = false;
    void importRecords();
  });

  // ---- Forget archive… and Delete archive… -------------------------------
  // Both need the archive closed (APP.md §13), and the close is the
  // dialog's release: run once the name is typed and the act confirmed,
  // never before (ruled 2026-10-14), so a Cancel leaves an archive that is
  // still draining a preview exactly as it was. A close the core refuses
  // ends the dialog with its reason and nothing removed.
  function ask(mode: "delete" | "forget"): DeleteAsk | null {
    const a = one;
    if (!a) return null;
    return { id: a.id, name: a.name, path: a.path, createdAt: det?.createdAt ?? 0, mode };
  }

  function startDelete(mode: "delete" | "forget") {
    const snap = ask(mode);
    if (snap) del = snap;
  }

  // letGo: the close, when the row is open at the moment of consent —
  // read off the list then, not when the dialog opened. It throws what
  // the core refused it with.
  async function letGo(id: string): Promise<void> {
    if (!store.archives.find((x) => x.id === id)?.open) return;
    try {
      await Archives.Close(id);
    } catch (e) {
      // An archive that has closed itself since the row was read — a page
      // left it, and nothing was holding it — is already where this wanted
      // it (APP.md §2.3).
      if (errorOf(e).code !== Code.CodeArchiveNotOpen) throw e;
    }
    // The Archive page may still be holding this one as its current
    // archive: it has no handle any more, so there is nothing to leave.
    if (store.current === id) store.leaveArchive(true);
    await store.refreshArchives();
  }

  // The keyboard path: Space selects, Enter selects and opens, the arrows
  // move focus.
  function rowKey(e: KeyboardEvent, id: string) {
    const el = e.currentTarget as HTMLElement;
    switch (e.key) {
      case " ":
        e.preventDefault();
        sel = clickRow(sel, order, id);
        lastClicked = id;
        break;
      case "Enter":
        e.preventDefault();
        sel = clickRow(sel, order, id);
        lastClicked = id;
        openRow(id);
        break;
      case "ArrowDown":
        e.preventDefault();
        (el.nextElementSibling as HTMLElement | null)?.focus();
        break;
      case "ArrowUp":
        e.preventDefault();
        (el.previousElementSibling as HTMLElement | null)?.focus();
        break;
    }
  }

  // openRow is a row's double-click and Enter: nothing for a row whose
  // Open is greyed — forgotten, or being compacted — since the command
  // already says why (ruled 2026-10-14) and the core would only refuse.
  function openRow(id: string) {
    const a = store.archives.find((x) => x.id === id);
    if (!a || rowRefusal(a, store.runningOps) || (!unlocked && !a.open)) return;
    void store.openArchive(id);
  }

  // What stops a record-level operation, said on the button itself. The
  // archive being open is never one of them since 2026-09-10: *Refresh
  // fingerprint*, *Compact…* and *Re-key archive…* unwrap the key with the session's KWK, open
  // the file for the operation and close it again, so the page never asks
  // for the archive to be opened first (APP.md §2.3). Only the vault does:
  // while it is locked they are disabled with its own reason.
  // The locked banner's sentence, its first strong (direction 9's mock).
  const lockedLead = $derived(archivesCopy.lockedBannerLead(openedNames));

  const opReason = $derived(
    !unlocked
      ? codeText(Code.CodeVaultLocked)
      : tampered
        ? vaultCopy.changesOff // the banner says why; the title says only that changes are off
        : undefined,
  );

  // What the row itself stops (lib/archlist.ts): a forgotten record, or a
  // compaction under way. Hide, Locate and the name and description are
  // registry writes and are stopped by the first alone; Open, Compact,
  // Re-key archive and Refresh fingerprint by either. Close, Forget archive and Delete archive
  // by the second alone: the close the last two need is refused while a
  // compaction holds the handle, and asking for the name first would only
  // end in that refusal (APP.md §2.3, §13).
  const rowStop = $derived(one ? rowRefusal(one, store.runningOps) : "");
  const rowWhy = $derived(rowStop ? codeText(rowStop) : undefined);
  const hideStop = $derived(!!one && one.forgottenAt > 0);
  const forgotWhy = $derived(hideStop ? codeText(Code.CodeArchiveForgotten) : undefined);
  const compactStop = $derived(!!one && compacting(one, store.runningOps));
  const compactWhy = $derived(compactStop ? codeText(Code.CodeArchiveCompacting) : undefined);

  const foreign = $derived(!!pane && foreignPath(pane.path));
  const purge = $derived(pane ? purgeAfter(pane.forgottenAt) : 0);

  // The foot's note (LayerFoot): the one selected, else how many are, else
  // how many there are.
  $effect(() => {
    store.footNote = one ? archivesCopy.selectedName(one.name) : sel.ids.size > 1 ? archivesCopy.selectedCount(sel.ids.size) : plural(rows.length, "archive");
  });
</script>

<svelte:window onkeydown={shortcut} />

<div class="layer-head">
  <h1 class="t-title">{st?.displayName || "Vault"}</h1>
  <!-- One caption line for the vault, the count included: a pill is for
       a state, and Locked is the head's one (APP.md §13, ruled
       2026-10-17). The block's part is one expression, separator and
       all: Svelte trims the white space at a block's edges, which made
       "stored· vault file". -->
  <span class="t-quiet num meta">{plural(rows.length, "archive")} shown · {bytes(shownSize)} stored{#if st}{` · vault file ${bytes(st.vaultFileSize)} · changed ${dateTime(st.modifiedAt)}`}{/if}</span>
  {#if !unlocked}<span class="chip"><svg class="i i-14"><use href="#i-lock" /></svg>{chipCopy.locked}</span>{/if}
</div>

<!-- The page is a drop target with no data-archive-id: Wails emits a drop
     only over an element marked data-file-drop-target, so without it an
     .efd dropped here never reaches the store, which opens it (APP.md §3,
     §14; ruled 2026-10-14). The file list's own target, on the Archive
     page, carries the ids. -->
<div class="layer-body" bind:this={bodyEl} data-file-drop-target="true">
  {#if tampered}
    <!-- First on the page, above the commands it greys (direction 9;
         APP.md §13): the danger tier, with its fix. -->
    <TamperedBar reason={st?.tamperedReason} />
  {/if}
  <div class="cmdbar">
    <!-- Every action that takes one archive works on the pane's row and is
         greyed unless exactly one is ticked (APP.md §6, ruled 2026-09-10). -->
    <button type="button" class="btn accent" disabled={!one || (!unlocked && !one.open) || !!rowStop} title={rowWhy} onclick={() => one && store.openArchive(one.id)}><svg class="i i-14"><use href="#i-open" /></svg>{commandCopy.open}</button>
    <button type="button" class="btn" disabled={!unlocked || tampered} onclick={openCreate}><svg class="i i-14"><use href="#i-plus" /></svg>{commandCopy.newArchive}</button>
    <!-- Never greyed: the chain answers a locked vault with the lock
         scene, and a file whose key is not here with its own dialog, as
         for a double-click in Explorer (APP.md §14). -->
    <button type="button" class="btn subtle" title="Ctrl+O" onclick={() => void openFile()}><svg class="i i-14"><use href="#i-folder" /></svg>{archivesCopy.openFile}</button>
    <div class="sep"></div>
    <!-- Never gated on the archive being open (APP.md §2.3, ruled
         2026-09-10): the core opens it for the operation and closes it
         again, so there is no "open the archive first". -->
    <button type="button" class="btn subtle" disabled={!one || !unlocked || tampered || !!rowStop} title={opReason ?? rowWhy} onclick={() => one && (confirm = { title: dialogCopy.compact.title(one.name), body: dialogCopy.compact.body, button: dialogCopy.compact.primary, run: () => Archives.Compact(one.id) })}>{commandCopy.compact}</button>
    <button type="button" class="btn subtle" disabled={!one || !unlocked || tampered || !!rowStop} title={opReason ?? rowWhy} onclick={() => one && (confirm = { title: dialogCopy.rekeyArchive.title(one.name), body: dialogCopy.rekeyArchive.body, how: dialogCopy.rekeyArchive.how, button: dialogCopy.rekeyArchive.primary, run: () => Archives.RotateKey(one.id) })}><svg class="i i-14"><use href="#i-rotate" /></svg>{commandCopy.rekeyArchive}</button>
    <button type="button" class="btn subtle" disabled={!one || !unlocked || tampered || !!rowStop} title={opReason ?? rowWhy} onclick={() => one && run(Archives.Verify(one.id))}>{commandCopy.refreshFingerprint}</button>
    <button type="button" class="btn subtle" disabled={!one || !unlocked || hideStop} title={forgotWhy} onclick={() => one && run(one.hidden ? Archives.Unhide(one.id) : Archives.Hide(one.id))}><svg class="i i-14"><use href="#i-eye" /></svg>{one?.hidden ? commandCopy.unhide : commandCopy.hide}</button>
    <div class="sep"></div>
    <button type="button" class="btn subtle" disabled={!unlocked} onclick={() => run(Archives.CheckFiles())}>{commandCopy.findMissing}</button>
    <button type="button" class="btn subtle" disabled={!unlocked || tampered} onclick={importRecords}><svg class="i i-14"><use href="#i-backup" /></svg>{commandCopy.importArchives}</button>
    <div class="sep"></div>
    <button type="button" class="btn subtle danger" disabled={!one || !unlocked || tampered || one.forgottenAt > 0 || compactStop} title={compactWhy} onclick={() => void startDelete("forget")}>{commandCopy.forgetArchive}</button>
    <button type="button" class="btn subtle danger" disabled={!one || !unlocked || tampered || compactStop} title={compactWhy} onclick={() => void startDelete("delete")}><svg class="i i-14"><use href="#i-trash" /></svg>{commandCopy.deleteArchive}</button>
    <div class="grow"></div>
    <label class="filterbox"><span class="vh">{archivesCopy.filter}</span><input class="input" type="search" placeholder={archivesCopy.filter} bind:value={filter} /></label>
    <label class="check"><input type="checkbox" bind:checked={store.showHidden} onchange={() => store.refreshArchives()} />{archivesCopy.showHidden}</label>
  </div>

  {#if !unlocked}
    <!-- The locked banner: one line of 40 px, everything on it centred
         (APP.md §7, ruled 2026-09-11). Attention since direction 9 (ruled
         2026-10-18): what the lock left open is still decrypted and still
         saves (APP.md §2.3), so it names them (VaultStatus.opened) and
         offers their close beside the unlock. -->
    <div class="bar line attention">
      <svg class="i i-14"><use href="#i-lock" /></svg>
      <span class="grow"><strong>{lockedLead.head}</strong> {lockedLead.rest}</span>
      {#if openedNames.length > 0}
        <button type="button" class="btn sm" onclick={() => void store.closeOpened(opened.length === 1 ? opened[0].id : undefined)}>{opened.length === 1 ? commandCopy.closeArchive : commandCopy.closeAll}</button>
      {/if}
      <button type="button" class="btn sm accent" onclick={() => store.unlock()}>{commandCopy.unlock}</button>
    </div>
  {/if}
  <OpsBar />

  <div class="arch-split" bind:this={splitEl}>
    <div class="arch-list">
      <!-- A click on the list's blank area clears the ticks and the anchor
           (APP.md §6); a click that lands on a row is the row's, and a
           header cell sorts. -->
      <!-- svelte-ignore a11y_no_noninteractive_element_interactions, a11y_click_events_have_key_events -->
      <div class="tablewrap" role="region" aria-label={archivesCopy.listLabel} bind:this={listEl} onclick={blank}>
        {#if rows.length === 0 && !filter.trim() && vaultEmpty}
          <!-- A vault with no archives yet says what to do, with the
               buttons that do it (ruled 2026-10-14), not greyed verbs over
               an empty line. *Import archives from a backup…* is the bar's own command:
               its picker and its proof step come before any key is
               asked for. A command is a button, so it is a small subtle
               one, not a hyperlink (APP.md §6, Controls; ruled
               2026-10-17). -->
          <div class="empty first">
            <p>{archivesCopy.emptyVault}</p>
            <div class="row">
              <button type="button" class="btn accent" disabled={tampered} onclick={openCreate}><svg class="i i-14"><use href="#i-plus" /></svg>{commandCopy.newArchive}</button>
              <button type="button" class="btn" onclick={() => void openFile()}><svg class="i i-14"><use href="#i-folder" /></svg>{archivesCopy.emptyOpen}</button>
            </div>
            <button type="button" class="btn subtle sm" disabled={tampered} onclick={importRecords}>{commandCopy.importArchives}</button>
          </div>
        {:else if rows.length === 0 && !filter.trim() && vaultEmpty === null}
          <div class="empty"></div>
        {:else if rows.length === 0}
          <div class="empty">{filter.trim() ? archivesCopy.noMatch : unlocked ? archivesCopy.allHidden : "No archives are open."}</div>
        {:else}
          <!-- The same checkboxes as the file list's, under the same rules:
               the header ticks the rows the filter shows and is ticked
               exactly when all of them are, never a third state; they
               show on hover, focus and selection, and a selected row is
               the wash and the pill alone (app.css; APP.md §6, ruled
               2026-10-17). One sort glyph, the 10 px chevron, on the
               sorter's own hover fill, as on the file list. -->
          <table>
            <colgroup><col class="w-check" /><col /><col class="w-size" /><col class="w-date" /><col class="w-status" /></colgroup>
            <thead>
              <tr>
                <th scope="col" class="chk"><input type="checkbox" checked={allTicked} aria-label={archivesCopy.tickAll} onclick={tickAll} /></th>
                <th scope="col" aria-sort={ariaSort("name")}><button type="button" class="sorter" onclick={() => sortOn("name")}>Name<svg class="i caret" class:on={sortKey === "name"} class:asc={sortAsc}><use href="#i-chevdown" /></svg></button></th>
                <th scope="col" class="num" aria-sort={ariaSort("size")}><button type="button" class="sorter" onclick={() => sortOn("size")}>Size<svg class="i caret" class:on={sortKey === "size"} class:asc={sortAsc}><use href="#i-chevdown" /></svg></button></th>
                <th scope="col" aria-sort={ariaSort("saved")}><button type="button" class="sorter" onclick={() => sortOn("saved")}>Last saved<svg class="i caret" class:on={sortKey === "saved"} class:asc={sortAsc}><use href="#i-chevdown" /></svg></button></th>
                <th scope="col" aria-sort={ariaSort("status")}><button type="button" class="sorter" onclick={() => sortOn("status")}>Status<svg class="i caret" class:on={sortKey === "status"} class:asc={sortAsc}><use href="#i-chevdown" /></svg></button></th>
              </tr>
            </thead>
            <tbody>
              {#each rows as a, i (a.id)}
                <!-- svelte-ignore a11y_no_noninteractive_tabindex a11y_no_noninteractive_element_interactions -->
                <!-- data-id: how the panel finds the row it must bring to
                     the list's first line (lib/archbar.ts). -->
                <tr data-id={a.id} tabindex={paneId === a.id || (!paneId && i === 0) ? 0 : -1} aria-selected={sel.ids.has(a.id)} class:forgotten={a.forgottenAt > 0} onclick={(e) => click(e, a.id)} ondblclick={() => openRow(a.id)} onkeydown={(e) => rowKey(e, a.id)}>
                  <td class="chk"><input type="checkbox" checked={sel.ids.has(a.id)} tabindex="-1" aria-label={listCopy.tickRow(a.name)} onclick={(e) => tick1(e, a.id)} ondblclick={(e) => e.stopPropagation()} /></td>
                  <td class="sel-mark">
                    <div class="fname">
                      <svg class="i i-14"><use href="#i-box" /></svg>
                      <div class="two-line"><b>{a.name}</b><small title={a.description}>{a.description || ""}</small></div>
                    </div>
                  </td>
                  <!-- Size alone is a .num cell, right-aligned (APP.md §6);
                       the date keeps its tabular digits on the left. -->
                  <td class="num">{a.storedSize ? bytes(a.storedSize) : "—"}</td>
                  <td><span class="num">{dateTime(a.lastWrittenAt)}</span></td>
                  <td>
                    <div class="status-cell">
                      {#each statuses(a) as s (s.label)}<span class="st {s.tone}" title={s.title}>{s.label}</span>{/each}
                    </div>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        {/if}
        <!-- The room the push scrolls into past the list's own end
             (lib/archbar.ts's overScroll): an element after the table, so
             the sticky header row and every row's place are untouched by
             it. Of no height and not there at all until the panel asks
             for it, and taken away when the collapse's motion ends. -->
        <div class="over-spacer" bind:this={spacerEl} hidden aria-hidden="true"></div>
      </div>
    </div>

    <!-- The pane shows the row last clicked while it is ticked, else the
         sole ticked row, else nothing but a count; its buttons take one
         archive and are greyed unless exactly one is ticked (APP.md §6).
         Below the stylesheet's threshold this one element is the bottom
         panel (APP.md §6, refined 2026-09-12): its header is the whole of
         it when collapsed and stays fixed at its top when it is up, and
         the body below scrolls under that header. Above the threshold it
         is the 300 px side pane, unchanged. -->
    <aside
      class="details"
      id="arch-details"
      class:no-row={!pane}
      class:up={bar.open}
      aria-label={archivesCopy.panelLabel}
      bind:this={cardEl}
    >
      <!-- The inner is what the card holds, and its size never changes:
           below the threshold it is laid out once at the panel's open
           height and the card's height alone animates over it, clipping
           it (APP.md §6, refined 2026-09-12). Above the threshold it is
           no box at all (display: contents) and the pane is what it was. -->
      <div class="details-inner" bind:this={innerEl}>
        <!-- The header is the control below the threshold, and a heading
             above it: the role, the focus and what a click does are the
             narrow layout's alone (app.css decides which that is; the
             probe below reads its answer). The chevron is a sign, not a
             button of its own. The collapsed line and the two-line stack
             are the same heading in its two shapes, the header a fixed
             64 px whose top 44 px are the line, and the two cross-fade
             as the card grows. -->
        <!-- The role and the tabindex are one decision the compiler cannot
             see through: below the threshold this is a button, above it a
             heading, so both are conditional and neither is a lie. -->
        <!-- svelte-ignore a11y_no_static_element_interactions, a11y_click_events_have_key_events, a11y_no_noninteractive_tabindex -->
        <div
          class="details-head"
          role={narrow ? "button" : undefined}
          tabindex={narrow ? 0 : undefined}
          aria-expanded={narrow ? bar.open : undefined}
          aria-disabled={narrow && !canExpand(bar) ? "true" : undefined}
          aria-controls={narrow ? "arch-details-body" : undefined}
          aria-label={narrow ? headLabel : undefined}
          onclick={headTap}
          onkeydown={headKey}
          bind:this={headEl}
        >
          <div class="head-stack" bind:this={stackEl}><div class="eyebrow">{archivesCopy.panelEyebrow}</div><h3>{pane?.name ?? ""}</h3></div>
          <div class="head-line" aria-hidden="true" bind:this={lineEl}><span class="eyebrow">{archivesCopy.panelEyebrow}</span><span class="hname">{pane?.name ?? ""}</span></div>
          <svg class="i i-14 chev" class:up={!bar.open}><use href="#i-chevdown" /></svg>
        </div>

        <!-- Clipped is not gone: the body is inert from the first frame of
             a collapse, so no Tab and no click reaches a field nobody can
             see, and the stylesheet hides it outright once the card is
             down. -->
        <div class="details-body" id="arch-details-body" inert={narrow && !bar.open} bind:this={paneBodyEl}>
          {#if pane}
            {#if purge}
              <div class="pad">
                <div class="bar attention">
                  <svg class="i i-14"><use href="#i-warn" /></svg>
                  <div class="why">
                    <!-- What it means, then the undo, which the bar carries
                         (the dialog template, ruled 2026-10-17). -->
                    <span>{chipCopy.forgottenOn(date(pane.forgottenAt))}. Restore it to open it again; after {date(purge)} its key is gone.</span>
                    <button type="button" class="btn sm" disabled={!one || !unlocked || tampered} onclick={() => one && run(Archives.Restore(one.id)).then(() => store.refreshArchives())}>{commandCopy.restore}</button>
                  </div>
                </div>
              </div>
            {/if}

            {#if draft}
              <div class="pad fields">
                <div class="field">
                  <div class="field-top"><label for="ar-name">Name</label></div>
                  <input id="ar-name" class="input" class:invalid={nameChanged && !!nameProblem(draft.name)} aria-invalid={nameChanged && nameProblem(draft.name) ? "true" : undefined} bind:value={store.recordDraft[pane.id].name} disabled={!editable} title={forgotWhy} />
                </div>
                <div class="field">
                  <div class="field-top"><label for="ar-desc">Description</label><span class="hint">Shown under the name in the list</span></div>
                  <textarea id="ar-desc" class="input area" class:invalid={descChanged && !!descriptionProblem(draft.description)} aria-invalid={descChanged && descriptionProblem(draft.description) ? "true" : undefined} rows="2" bind:value={store.recordDraft[pane.id].description} disabled={!editable} title={forgotWhy}></textarea>
                </div>
              </div>
            {/if}

            <!-- The one label · value list (APP.md §6, Type and spacing):
                 each value beside its label, never pushed to the far edge
                 of a wide panel. User words only (ruled 2026-10-17): the key
                 version, the KID and the receipt are Technical details'. -->
            <dl class="kv pad">
              <div><dt>{archivesCopy.facts.size}</dt><dd class="num">{pane.storedSize ? bytes(pane.storedSize) : "—"}</dd></div>
              {#if pane.open}<div><dt>{archivesCopy.facts.files}</dt><dd class="num">{count(pane.files)}</dd></div>{/if}
              <div><dt>{archivesCopy.facts.created}</dt><dd class="num">{det ? dateTime(det.createdAt) : "—"}</dd></div>
              <div><dt>{archivesCopy.facts.lastSaved}</dt><dd class="num">{dateTime(pane.lastWrittenAt)}</dd></div>
              <div><dt>{archivesCopy.facts.compression}</dt><dd>{methodWord(pane.method)}</dd></div>
              <!-- The figure, not advice: the core reclaims the space itself
                   after a commit that leaves it over APP.md §2.3's thresholds,
                   so "compact when convenient" would tell the user to do what
                   has already been done. What is left below the quarter is
                   why the file is bigger than the files inside it, and the
                   same floor decides whether it is worth saying (lib/status.ts).
                   *Unused*, not *free*, which reads as the disk's. -->
              {#if pane.open && freeWorthShowing(pane.freeSpace)}<div><dt>{archivesCopy.facts.unused}</dt><dd class="num">{bytes(pane.freeSpace)}</dd></div>{/if}
              <!-- Which copy of the file the registry names (direction 9, ruled
                   2026-10-18): the hash Refresh fingerprint records, against
                   the saves since — never "verified", since it checks
                   nothing inside the file (APP.md §2.3). Drawn only from the
                   record read whole: "Not taken yet" in the third ink when
                   no hash was ever taken, no tick unless it matches. -->
              {#if fp}<div><dt>{archivesCopy.facts.fingerprint}</dt><dd class="num">{#if fp.kind === "current"}<span class="fp"><svg class="i i-14" aria-hidden="true"><use href="#i-check" /></svg>{archivesCopy.fingerprint(fp)}</span>{:else}<span class:none={fp.kind === "none"}>{archivesCopy.fingerprint(fp)}</span>{/if}</dd></div>{/if}
            </dl>
            <!-- *Technical details…* reveals more of these facts, so it is a
                 hyperlink button under them, as *Show in Explorer* and
                 *Locate…* are under the path (APP.md §6, Controls; ruled
                 2026-10-17: it was a standard button beside *Close*). -->
            <div class="pad"><button type="button" class="btn link" disabled={!det || !one} onclick={() => (showDetails = true)}>{archivesCopy.technicalDetails}</button></div>

            <!-- A path is the stacked form: its label over it, and the
                 path wrapping rather than cut (APP.md §6, §13). -->
            <div class="pad pathblock">
              <dl class="kv stk"><dt>{foreign ? archivesCopy.facts.foreign : archivesCopy.facts.file}</dt><dd>{pane.path || "—"}</dd></dl>
              <div class="row">
                {#if !foreign}<button type="button" class="btn link" disabled={!one} onclick={() => one && void Shell.Reveal(one.path)}>{commandCopy.showInExplorer}</button>{/if}
                <button type="button" class="btn link" disabled={!one || !unlocked || hideStop} title={forgotWhy} onclick={() => one && locate(one)}>{commandCopy.locate}</button>
              </div>
            </div>

            {#if pane.note === "archive.file_missing"}
              <div class="pad"><div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span class="grow">{codeText(pane.note)}</span></div></div>
            {/if}

            <div class="details-actions">
              <!-- A standard button: the bar's Open is the one accent on
                   screen (ruled 2026-10-14). -->
              <button type="button" class="btn wide" disabled={!one || (!unlocked && !one.open) || !!rowStop} title={rowWhy} onclick={() => one && store.openArchive(one.id)}><svg class="i i-14"><use href="#i-open" /></svg>{commandCopy.open}</button>
              {#if pane.open}
                <div class="row2">
                  <button type="button" class="btn" disabled={!one || compactStop} title={compactWhy} onclick={() => one && run(Archives.Close(one.id)).then(() => store.refreshArchives())}>{commandCopy.close}</button>
                </div>
              {/if}
            </div>

            {#if pending.length > 0}
              <SaveBar items={pending} {invalid} busy={saving} onsave={() => void saveRecord()} ondiscard={() => pane && stage(pane)} />
            {/if}
          {:else}
            <div class="empty">{sel.ids.size > 1 ? archivesCopy.selectedCount(sel.ids.size) : archivesCopy.selectOne}</div>
          {/if}
        </div>
      </div>
    </aside>

    <!-- The stylesheet owns the threshold, and this is how the script
         reads its answer without knowing the number: a probe of no size
         that the container query gives a pixel of width below it. -->
    <i class="arch-probe" bind:this={probeEl}></i>
  </div>
</div>


{#if creating}
  <Dialog title={dialogCopy.newArchive.title} onclose={() => (creating = false)}>
    <!-- judged by the rename's rule too: a name over the bound is said
         here, before a file is made for it (APP.md §6). No placeholder:
         an invented name read as a default already filled in (ruled
         2026-10-17). -->
    <TextField id="na-name" label={dialogCopy.newArchive.name} bind:value={newName} bind:valid={newNameValid} attempt={newAttempt} judge={nameProblem} />
    <div class="field">
      <div class="field-top"><span class="t-strong">{dialogCopy.newArchive.compression}</span></div>
      <Segmented id="na-method" label={dialogCopy.newArchive.compression} options={methodOptions} bind:value={newMethod} />
      <div class="pin-note">{METHOD_NOTE}</div>
    </div>
    {#if existsShown}
      <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span>{codeText(Code.CodeArchiveExists)}</span></div>
    {/if}
    <p>{dialogCopy.newArchive.body}</p>
    {#snippet actions()}
      <!-- The one primary that keeps its "…": the save dialog follows. -->
      <button type="button" class="btn accent" onclick={create}>{dialogCopy.newArchive.primary}</button>
      <button type="button" class="btn" onclick={() => (creating = false)}>{dialogCopy.cancel}</button>
    {/snippet}
  </Dialog>
{/if}

{#if confirm}
  <Dialog title={confirm.title} onclose={() => (confirm = null)}>
    <p>{confirm.body}</p>
    {#if confirm.how}
      <!-- The mechanism, for whoever asks (the dialog template, ruled
           2026-10-17): shut by default, never the body's second voice.
           Styled by app.css details.how, as on every dialog. -->
      <details class="how">
        <summary>{dialogCopy.howThisWorks}</summary>
        <p>{confirm.how}</p>
      </details>
    {/if}
    {#snippet actions()}
      <button type="button" class="btn accent" onclick={() => { const c = confirm; confirm = null; if (c) void run(c.run()); }}>{confirm?.button}</button>
      <button type="button" class="btn" onclick={() => (confirm = null)}>{dialogCopy.cancel}</button>
    {/snippet}
  </Dialog>
{/if}

{#if showDetails && det}
  <ArchiveDetailsDialog d={det} keyVersion={pane?.keyVersion ?? 0} receiptOwed={pane?.receiptOwed ?? false} onclose={() => (showDetails = false)} />
{/if}

{#if del}
  <DeleteArchiveDialog
    id={del.id}
    name={del.name}
    path={del.path}
    createdAt={del.createdAt}
    mode={del.mode}
    onclose={() => (del = null)}
    ondone={() => { void store.refreshArchives(); void store.loadDetails(paneId); }}
    release={() => letGo(del?.id ?? "")}
  />
{/if}

{#if merging}
  <MergeDialog path={merging} onclose={() => (merging = "")} />
{/if}

<style>
  .w-size { width: 84px; }
  .w-date { width: 130px; }
  .w-status { width: 148px; }
  /* The head's caption sits on the title's baseline (APP.md §6, ruled
     2026-10-17). The title keeps the place centring gave it in the 62 px
     head, 2 px down its 32 px line ((32 - 28) / 2), so nothing moves
     between pages; the Locked pill is a box, centred on the line. */
  .layer-head { align-items: baseline; }
  .layer-head .t-title { margin-top: 2px; }
  .layer-head .chip { align-self: center; }
  .meta { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  /* The compacting badge: the open badge's shape in the attention tone. */
  .status-cell .st.busy { background: var(--attention-wash); border-color: var(--attention-line); color: var(--attention); }
  /* The empty vault's block: centred, the two buttons on one line and the
     import a small subtle button under them. */
  .empty.first { display: flex; flex-direction: column; align-items: center; gap: var(--s3); }
  .empty.first p { margin: 0; }
  /* The pane's parts: each at the card's inset, 12 below the one above,
     and the last 12 clear of the actions' rule. */
  .pad { padding: var(--s3) var(--s4) 0; }
  .pad:has(+ .details-actions) { padding-bottom: var(--s3); }
  .fields { display: flex; flex-direction: column; gap: var(--s3); }
  /* The hint stands beside its label, not across the panel from it. */
  .fields .field-top { justify-content: flex-start; }
  /* Two body lines and the field's own insets: the rows decide the height,
     and nothing else does — a WinUI text box has no resize grip (ruled
     2026-10-17); a longer description scrolls inside its two lines. */
  .area { height: auto; padding: var(--s2) var(--s3); resize: none; }
  .pathblock { display: flex; flex-direction: column; gap: var(--s1); }
  /* The fingerprint's check: the i-check in the accent's ink, before
     the words, only while the fingerprint matches the last save. */
  .fp { display: inline-flex; align-items: center; gap: var(--s2); }
  .fp .i { color: var(--accent-ink); }
  .none { color: var(--ink-3); }
  .why { display: flex; flex-direction: column; align-items: flex-start; gap: var(--s2); min-width: 0; }
  tbody tr.forgotten .two-line b { color: var(--ink-2); }
  .vh { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; }
</style>

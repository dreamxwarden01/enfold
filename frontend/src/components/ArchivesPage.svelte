<script lang="ts">
  // The Archives page is the vault's inspector (APP.md §13), at its real
  // size (direction 6, phase 4a). Its title is *Archives* — the vault's
  // name is the rail head's (direction 10). The command bar is one 40 px
  // line that never wraps: the selected archive's verbs fold into More
  // from the right when the bar has no room (lib/cmdbar.ts), and the
  // destructive ones always live there. Three columns: Name — always two
  // lines in a 52 px row, the name with its chips (lib/archlist.ts
  // chipKinds), then the description or, without one, the file's folder —
  // Size and Last saved; the Status column went into the chips. One
  // archive is selected at a time and there are no checkboxes: every
  // action here takes one archive (reopening 2026-09-10 for this list
  // alone). The pane follows the selected row, and with none it is the
  // vault's summary. *Missing* is only ever the core's own note, so a row
  // never says "missing" about a path nobody has measured. The header line
  // sums the rows shown and says so, beside the vault file's own size and
  // the registry's date (§2.1). Every label is the verb table's or the
  // dialog template's (strings.ts; APP.md §7, ruled 2026-10-17).
  import { tick, untrack } from "svelte";
  import { Archives, Code, Shell, errorOf } from "../lib/api";
  import type { ArchiveSummary } from "../lib/api";
  import { store } from "../lib/state.svelte";
  import { archivesCopy, chipCopy, codeText, commandCopy, dialogCopy, importCopy, keyHint, keyTitles, pendingCopy, sinceText, vaultCopy } from "../lib/strings";
  import { bytes, date, dateTime, plural } from "../lib/format";
  import { chipKinds, compacting, fingerprintOf, folderOf, rowRefusal, vaultFigures, viewRows } from "../lib/archlist";
  import type { ChipKind, ListView } from "../lib/archlist";
  import { fold, folded } from "../lib/cmdbar";
  import { KEY_ECHO_MS, groups, keyAnchor } from "../lib/menu";
  import type { MenuClose } from "../lib/menu";
  import { archCommands, blankMenu, rowMenu } from "../lib/archcmds";
  import type { ArchCmd, ArchFacts, ArchItem } from "../lib/archcmds";
  import { closeButton } from "../lib/leave";
  import { backupLine } from "../lib/backup";
  import { CLOSED, canExpand, collapse, edgeY, escape, expand, inContact, naturalEnd, openHeight, overScroll, picked, pushScroll, reveals, rideReachesHome, rideScroll, springScroll, toggle } from "../lib/archbar";
  import type { BarState, ListMetrics } from "../lib/archbar";
  import { MOVE, enter, motion, slideIn, unfold } from "../lib/motion";
  import { POPOVER_ATTR, dialogIsUp } from "../lib/dialogs";
  // The pending action, by another name: `pending` here is the save bar's
  // list of staged edits.
  import { pending as waiting } from "../lib/pending";
  import PendingLabel from "./PendingLabel.svelte";
  import { TYPEAHEAD_MS, controlKeepsKeys, intent, isTextEntry, jump, listScoped, pageRows, target, typeahead } from "../lib/listkeys";
  import type { Intent } from "../lib/listkeys";
  import { DEFAULT_METHOD, METHODS, METHOD_NOTE, methodWord } from "../lib/method";
  import { foreignPath } from "../lib/paths";
  import { fieldMenu } from "../lib/fieldmenu";
  import { purgeAfter } from "../lib/retention";
  import { DESCRIPTION_RULE, descriptionProblem, nameProblem } from "../lib/validate";
  import Dialog from "./Dialog.svelte";
  import Segmented from "./Segmented.svelte";
  import TextField from "./TextField.svelte";
  import SaveBar from "./SaveBar.svelte";
  import type { PendingItem } from "./SaveBar.svelte";
  import ArchiveDetailsDialog from "./ArchiveDetailsDialog.svelte";
  import DeleteArchiveDialog from "./DeleteArchiveDialog.svelte";
  import TamperedBar from "./TamperedBar.svelte";
  import NoticeSlot from "./NoticeSlot.svelte";
  import MenuButton from "./MenuButton.svelte";
  import type { MenuItem } from "./MenuButton.svelte";
  import ContextMenu from "./ContextMenu.svelte";

  // The selection (direction 6, phase 4a): one archive, by id — a click
  // selects it, the arrows move it, and Shift and Ctrl add nothing here.
  // Kept on the page: nothing here pages or restarts under it.
  let selId = $state<string | null>(null);
  const st = $derived(store.status);
  const unlocked = $derived(store.unlocked);

  // The view (View ▾; lib/archlist.ts viewRows): the list, or the
  // Forgotten view, which lists the forgotten records alone. A page's own
  // choice, back to the list each time the page is shown.
  let view = $state<ListView>("list");

  // A change of view plays the list's folder change (direction 4, list
  // motion; lib/motion.ts): the rows fade in from 8 px — the Forgotten view
  // from the right, as a place gone into, the list from the left, as one
  // come back to — while the header and the commands stay still. Only the
  // tbody moves, in place, so the focus stays where it was.
  let rowsEl: HTMLTableSectionElement | undefined = $state();
  let viewShown: ListView = "list";
  $effect(() => {
    const v = view;
    if (v === viewShown) return;
    viewShown = v;
    slideIn(untrack(() => rowsEl), v === "forgotten" ? 1 : -1);
  });

  // every: the whole registry, hidden and forgotten records included, for
  // what the store's own list cannot say while Show hidden is off — the
  // Forgotten view, its count, the vault's figures and whether the vault
  // is empty. Read again whenever the store's list is (archives.changed, a
  // write here), and the store's list itself while it already holds every
  // record or while Locked, when the core lists only what is open.
  // everyOf is the store list it was read against: until the read lands,
  // a list that went empty is not yet known to be a vault that is.
  let every = $state.raw<ArchiveSummary[] | null>(null);
  let everyOf = $state.raw<ArchiveSummary[] | null>(null);
  $effect(() => {
    const list = store.archives;
    if (store.showHidden || !unlocked) {
      every = list;
      everyOf = list;
      return;
    }
    let live = true;
    Archives.List(true)
      .then((all) => {
        if (!live) return;
        every = all ?? [];
        everyOf = list;
      })
      .catch(() => {});
    return () => {
      live = false;
    };
  });
  const forgottenAll = $derived(unlocked ? viewRows(every ?? [], "forgotten", true) : []);
  // The view a lock leaves: the Forgotten view needs the whole registry,
  // which a locked vault does not list.
  $effect(() => {
    if (!unlocked && untrack(() => view) === "forgotten") view = "list";
  });

  // source: the records the view lists, before the filter. The list is the
  // store's (Archives.List(showHidden)), held to the view's own rule: the
  // core's List(true) carries forgotten records too, and Show hidden lists
  // the hidden ones only — a forgotten record shows in the Forgotten view
  // alone (APP.md §13, ruled 2026-10-19).
  const source = $derived(view === "forgotten" ? forgottenAll : viewRows(store.archives, "list", store.showHidden));
  // The pane's record: the selected row's, from the view it was selected
  // in — a forgotten record is not in the store's list while Show hidden
  // is off.
  const pane = $derived(selId ? (source.find((a) => a.id === selId) ?? null) : null);
  const paneId = $derived(pane?.id ?? null);
  // one: the archive an action that takes one archive works on — the
  // selected row, the pane's.
  const one = $derived(pane);
  // The banner's one predicate (store.tampered; APP.md §6).
  const tampered = $derived(store.tampered);
  // The archives a lock left open, by name (direction 9; APP.md §2.3).
  const opened = $derived(st?.opened ?? []);
  const openedNames = $derived(opened.map((a) => a.name));
  // The locked line's close (APP.md §2.3, ruled 2026-10-19: one meaning for
  // Close): the graceful leave of what the lock left open, then *Force
  // close* / *Force close all* once what it names still stands after the
  // leave (lib/leave.ts).
  const openedIds = $derived(opened.map((a) => a.id));
  const lockedClose = $derived(closeButton("line", openedIds, (x) => store.leftHeld(x)));
  function closeLocked(): void {
    if (lockedClose.force) void store.closeOpened(openedIds.length === 1 ? openedIds[0] : undefined);
    else void store.leaveOpened(openedIds);
  }
  // The record read whole, and only when it is the pane's: a reply for a
  // record the user has left is not this pane's (APP.md §13).
  const det = $derived(store.details && pane && store.details.archiveId === pane.id ? store.details : null);
  // The pane's Fingerprint fact (direction 9): null — no row — until the
  // record is read, and so always while Locked (lib/archlist.ts).
  const fp = $derived(pane ? fingerprintOf(pane, det) : null);

  // pick: that one row selected, and so in the pane.
  function pick(id: string) {
    selId = id;
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
  // lands (it is listed only in View › Forgotten), and a dialog gated
  // on the selection would unmount with it — taking the outcome screen and the
  // purge date it names with it (APP.md §13).
  interface DeleteAsk {
    id: string;
    name: string;
    path: string;
    createdAt: number;
    mode: "delete" | "forget";
  }
  let del = $state<DeleteAsk | null>(null);
  let saving = $state(false);

  // The table: a filter over name, description and file name, then one
  // sortable column of three.
  type SortKey = "name" | "size" | "saved";
  let filter = $state("");
  let sortKey = $state<SortKey>("name");
  let sortAsc = $state(true);

  function fail(e: unknown) {
    store.notice(codeText(errorOf(e).code), "error");
  }

  async function run(p: Promise<unknown>) {
    try {
      await p;
    } catch (e) {
      fail(e);
    }
  }

  // ---- the chips after a row's name (lib/archlist.ts chipKinds) ---------
  function chipLabel(k: ChipKind): string {
    switch (k) {
      case "missing":
        return chipCopy.missing;
      case "hidden":
        return chipCopy.hidden;
      case "forgotten":
        return chipCopy.forgotten;
      case "open":
        return chipCopy.open;
      case "compacting":
        return chipCopy.compacting;
      case "refreshing":
        return chipCopy.refreshing;
      case "rekeying":
        return chipCopy.rekeying;
    }
  }

  function chipTitle(k: ChipKind, a: ArchiveSummary): string | undefined {
    if (k === "forgotten") return chipCopy.forgottenOn(date(a.forgottenAt));
    if (k === "compacting") return codeText(Code.CodeArchiveCompacting);
    return undefined;
  }

  const rows = $derived.by(() => {
    const q = filter.trim().toLowerCase();
    const list = source.filter((a) => !q || a.name.toLowerCase().includes(q) || (a.description ?? "").toLowerCase().includes(q) || a.path.toLowerCase().includes(q));
    const dir = sortAsc ? 1 : -1;
    return [...list].sort((a, b) => {
      switch (sortKey) {
        case "size":
          return dir * (a.storedSize - b.storedSize);
        case "saved":
          return dir * (a.lastWrittenAt - b.lastWrittenAt);
      }
      return dir * a.name.localeCompare(b.name);
    });
  });

  // vaultEmpty: the vault holds no record at all, hidden and forgotten
  // ones included — what the empty page's buttons are for (ruled
  // 2026-10-14). The list itself cannot say so while Show hidden is off,
  // so it is the whole registry's answer (every), read against this very
  // list. null is "not known yet": the empty list says nothing until the
  // answer comes, rather than flashing a line about Show hidden that may
  // be untrue, and a refused question leaves it saying nothing.
  const vaultEmpty = $derived<boolean | null>(!(unlocked && store.archives.length === 0) ? false : everyOf === store.archives && every ? every.length === 0 : null);
  // The empty vault's page (direction 6): the block alone — no command
  // bar, no pane, no verb greyed for want of a row.
  const emptyShown = $derived(view === "list" && rows.length === 0 && !filter.trim() && vaultEmpty === true);

  // Summed by the page over the rows shown, and labelled so: a total that
  // does not match the visible rows reads as a bug (APP.md §13).
  const shownSize = $derived(rows.reduce((n, a) => n + (a.storedSize || 0), 0));

  // The rows as they stand on screen, top to bottom: what the arrows walk.
  const order = $derived(rows.map((a) => a.id));

  // A row that left the view — forgotten, restored, gone with a lock, or
  // the view changed under it — leaves the selection too; a row the filter
  // merely hides keeps it, as the pane's.
  $effect(() => {
    const live = new Set(source.map((a) => a.id));
    untrack(() => {
      if (selId !== null && !live.has(selId)) selId = null;
    });
  });

  // A row's click selects it, whatever the modifiers: Shift and Ctrl add
  // nothing on a list whose every action takes one archive (direction 6).
  function click(id: string) {
    selId = id;
  }

  // The list's blank area — under the last row, beside the table — clears
  // the selection; a header cell sorts and is not blank.
  function blank(e: MouseEvent) {
    const t = e.target as HTMLElement;
    if (!t.closest("tbody tr") && !t.closest("thead")) selId = null;
  }

  // ---- the keys (lib/listkeys.ts; direction 7, APP.md §6) --------------
  // Every key of the list and of the page is taken here, in one handler,
  // through the one keymap. The list is single-select: an arrow, Home,
  // End, PgUp, PgDn or a typed name selects that one row and the details
  // pane follows (reversing 2026-09-10's "arrows move the focus"), and
  // Ctrl+A selects nothing more (direction 6). The list's own keys are taken while
  // the focus is in the list or on the page's body; the page's commands
  // from anywhere on it but a text field — Esc and Ctrl+O even from the
  // Filter, since neither edits text (ruled 2026-09-11, 2026-10-14).
  // Nothing is taken while a dialog is up (lib/dialogs.ts) or from an
  // open menu, whose keys are its own.
  let listEl = $state<HTMLDivElement | undefined>();
  let filterEl = $state<HTMLInputElement | undefined>();
  // The type-ahead's buffer and when its last character came (700 ms).
  let typed = "";
  let typedAt = 0;

  function onKey(e: KeyboardEvent) {
    if (e.defaultPrevented || e.isComposing) return;
    const t = e.target instanceof HTMLElement ? e.target : null;
    if (t?.closest(`[role="menu"], [${POPOVER_ATTR}]`)) return;
    const now = Date.now();
    const i = intent(
      { key: e.key, ctrl: e.ctrlKey || e.metaKey, shift: e.shiftKey, alt: e.altKey },
      {
        list: "archives",
        onUp: false,
        inField: !!t && isTextEntry(t.tagName, t.getAttribute("type") ?? "", t.isContentEditable),
        dialogUp: dialogIsUp(),
        typing: typed !== "" && now - typedAt <= TYPEAHEAD_MS,
        // A sort button, a row's own buttons (Restore, Delete archive…,
        // the Missing chip) and the empty vault's buttons sit in the
        // list's region but keep their own Enter and Space.
        onControl: controlKeepsKeys(t),
      },
    );
    // Ctrl+L is the foot's (LayerFoot): Lock now on every page.
    if (i.kind === "none" || i.kind === "lock") return;
    const inList = !t || t === document.body || !!listEl?.contains(t);
    if (listScoped(i) && !inList) return;
    // Esc collapses the panel (APP.md §6, ruled 2026-09-11), and is the
    // page's to swallow only when it did: a key that moves nothing here is
    // left alone, so the Filter's own Esc still clears it.
    if (i.kind === "clear") {
      const next = escape(bar);
      if (next === bar) return;
      e.preventDefault();
      moveBar(next);
      return;
    }
    e.preventDefault();
    act(i, focusIndex(t), now);
  }

  // focusIndex: the row the keys start from — the focused row, else, with
  // the focus on the page's body, the pane's; else none.
  function focusIndex(t: HTMLElement | null): number | null {
    const tr = t && listEl?.contains(t) ? t.closest("tr") : null;
    const id = tr?.getAttribute("data-id") ?? selId;
    if (!id) return null;
    const at = order.indexOf(id);
    return at >= 0 ? at : null;
  }

  function act(i: Intent, from: number | null, now: number) {
    const atRow = from !== null && from >= 0 ? rows[from] : null;
    switch (i.kind) {
      case "move":
        land(target(from, i.by, rows.length, pageSize(), false));
        break;
      case "select":
        if (atRow) pick(atRow.id);
        break;
      case "selectAll":
        // One archive at a time: Ctrl+A has nothing to select here.
        break;
      case "open":
        if (!atRow) break;
        pick(atRow.id);
        openRow(atRow.id);
        break;
      case "details":
        // Technical details… for the selected row, as its button.
        if (det && one) showDetails = true;
        break;
      case "newArchive":
        if (unlocked && !tampered) openCreate();
        break;
      case "openFile":
        void openFile();
        break;
      case "filter":
        filterEl?.focus();
        filterEl?.select();
        break;
      case "delete":
        // Delete archive… for the selected row, greyed by the same rule as
        // More's.
        if (one && unlocked && !tampered && !compactStop) startDelete("delete", one);
        break;
      case "rename":
        editName();
        break;
      case "refresh":
        void store.refreshArchives();
        break;
      case "menu":
        keyMenu(atRow);
        break;
      case "typeahead": {
        typed = typeahead(typed, i.char, now, typedAt);
        typedAt = now;
        const to = jump(rows.map((a) => a.name), typed, atRow ? from : null);
        if (to >= 0) land(to);
        break;
      }
    }
  }

  // land: the row a key arrived at becomes the one selected row, the
  // anchor and the pane's, and takes the focus. With the narrow panel up,
  // the panel brings it to the list's first line itself (lib/archbar.ts);
  // otherwise it is brought into view the nearest way, clear of the
  // sticky header row.
  function land(to: number | null) {
    if (to === null || to < 0) return;
    const id = rows[to].id;
    pick(id);
    void tick().then(() => {
      const el = listEl?.querySelector<HTMLElement>(`tr[data-id="${CSS.escape(id)}"]`);
      if (!el) return;
      el.focus({ preventScroll: true });
      if (!(narrow && bar.open)) reveal(el);
    });
  }

  function reveal(el: HTMLElement) {
    const box = listEl;
    if (!box) return;
    const head = box.querySelector("thead")?.getBoundingClientRect().height ?? 0;
    const r = el.getBoundingClientRect();
    const b = box.getBoundingClientRect();
    if (r.top < b.top + head) box.scrollTop -= b.top + head - r.top;
    else if (r.bottom > b.bottom) box.scrollTop += r.bottom - b.bottom;
  }

  // PgUp and PgDn move by the rows that fit (lib/listkeys.ts pageRows);
  // a row here is the ruled 52 px, its two lines (direction 6).
  function pageSize(): number {
    const box = listEl;
    if (!box) return 1;
    const row = box.querySelector("tbody tr")?.getBoundingClientRect().height ?? ROW;
    return pageRows(box.clientHeight, box.querySelector("thead")?.getBoundingClientRect().height ?? 0, row);
  }

  // F2 edits the name (direction 7): the pane's pencil opens the fields
  // and the Name field takes the focus with the name selected, the narrow
  // panel opening first when it is down. A row whose fields are greyed —
  // forgotten, locked — opens none.
  function editName() {
    if (!one || !editable) return;
    editOpen = true;
    if (narrow && !bar.open) moveBar(expand(bar));
    void tick().then(() => {
      const el = document.getElementById("ar-name") as HTMLInputElement | null;
      if (!el || el.disabled) return;
      el.focus({ preventScroll: true });
      el.select();
    });
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
  let headEl = $state<HTMLDivElement | undefined>(); // the header's control (.head-tap)
  // The panel's actions in its header row (direction 6): their width keeps
  // the header's two shapes clear of them.
  let actsW = $state(0);
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
  // The control's name while it opens something; with nothing selected the
  // line is the vault's summary, read as it stands — a name over it would
  // hide the summary from assistive tech (ruled 2026-10-19).
  const headLabel = $derived(!pane ? undefined : bar.open ? archivesCopy.panelHide(pane.name) : archivesCopy.panelShow(pane.name));

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

  const ROW = 52; // a row's height, two lines (app.css; direction 6)

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
    // The split as well as the body: a banner appearing above the list
    // (the operations are the foot's since direction 4, and never move it) moves the list down without changing the
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
  // An edit takes the selected archive; a forgotten record, a locked vault
  // or a Tampered one edits nothing (APP.md §6).
  const editable = $derived(!!one && unlocked && !tampered && one.forgottenAt === 0);
  // The fields are behind the pencil (direction 6): shut until it is
  // pressed or F2 asks for them, and open while the draft holds a change
  // the save bar names — a draft kept from an earlier visit opens them
  // again. Discard and a save that landed shut them; another row shuts
  // them for this one.
  let editOpen = $state(false);
  $effect(() => {
    void paneId;
    untrack(() => (editOpen = false));
  });
  const editReason = $derived(!one ? undefined : !unlocked ? codeText(Code.CodeVaultLocked) : tampered ? vaultCopy.changesOff : one.forgottenAt > 0 ? codeText(Code.CodeArchiveForgotten) : undefined);

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

  // The fields are shown while the pencil has them open or the draft holds
  // a change (see editOpen).
  const fieldsShown = $derived(!!draft && (editOpen || pending.length > 0));

  // Discard: the draft back to the record, and the fields folded away.
  function discard() {
    if (pane) stage(pane);
    editOpen = false;
  }

  // The pencil: opens the fields; pressed again with nothing changed it
  // folds them, and with a change staged it leaves them to the save bar's
  // Discard and takes the focus to the name.
  function pencil() {
    if (!fieldsShown) {
      editName();
      return;
    }
    if (pending.length === 0) editOpen = false;
    else document.getElementById("ar-name")?.focus();
  }

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
      editOpen = false;
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
  // add to it (ruled 2026-10-14) — not left as a selected row.
  // The create itself, once the place is chosen: the dialog's primary
  // waits on it with the ring (direction 4), and a second press meanwhile
  // is ignored.
  let createWait = $state<Promise<unknown> | null>(null);
  async function create() {
    if (createWait) return;
    newAttempt++;
    if (!newNameValid) return;
    const name = newName.trim();
    const p = await Shell.SaveFile("Where to keep the archive", `${name}.efd`, store.settings?.lastArchiveFolder ?? "");
    if (!p) return;
    const made = Archives.Create(p, name, newMethod);
    createWait = made;
    void made.then(
      () => (createWait = null),
      () => (createWait = null),
    );
    try {
      const id = await made;
      creating = false;
      newExists = "";
      await store.refreshArchives();
      await store.refreshSettings(); // the folder this create remembered
      // A refused open (the vault locked meanwhile) leaves the row selected
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
    // The merge dialog stands above every route (App.svelte, store.merging),
    // so the Vault page's *Import archives from a backup…* opens it too.
    if (p.length) store.merging = p[0];
    // A picker closed with nothing: the file opened from Explorer that led
    // here waits for no merge any more (store.mergeDone).
    else store.forgetImport();
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
  // The dialog takes the row it was asked for: the selected one, or a
  // forgotten row's own *Delete archive…* in the Forgotten view.
  function startDelete(mode: "delete" | "forget", a: ArchiveSummary | null) {
    if (!a) return;
    const created = det && det.archiveId === a.id ? det.createdAt : 0;
    del = { id: a.id, name: a.name, path: a.path, createdAt: created, mode };
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

  // openRow is a row's double-click and Enter: nothing for a row whose
  // Open is greyed — forgotten, or being compacted — since the command
  // already says why (ruled 2026-10-14) and the core would only refuse.
  function openRow(id: string) {
    const a = store.archives.find((x) => x.id === id);
    if (!a || rowRefusal(a, store.runningOps) || (!unlocked && !a.open)) return;
    openArchive(id);
  }

  // One open at a time (direction 4, the pending primitive): the row's
  // status cell wears the ring once the open has taken 300 ms, and the
  // Open buttons say *Opening…*; a second double-click, Enter or Open
  // while one is on its way is ignored rather than queued behind it.
  let opening = $state<{ id: string; p: Promise<boolean> } | null>(null);
  let openArmed = $state(false);
  function openArchive(id: string) {
    if (opening) return;
    const p = store.openArchive(id);
    opening = { id, p };
    void p.finally(() => {
      if (opening?.p === p) opening = null;
    });
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

  // ---- the selected archive's commands, as menus hold them -----------------
  // One table of the row's commands (lib/archcmds.ts; directions 6 and 7)
  // for the bar, its More, the pane's More and the row's own menu: each
  // item greyed by one rule with its reason, which a menu draws as its
  // second line (lib/menu.ts secondLine), and each run by one function
  // (runArch). The bar's own buttons keep the codes' sentences as titles.
  const verbStop = $derived(!one || !unlocked || tampered || !!rowStop);
  const verbWhy = $derived(!one ? archivesCopy.selectFirst : (opReason ?? rowWhy));
  const why = archivesCopy.why;

  function refreshFingerprint() {
    if (one) void run(Archives.Verify(one.id));
  }

  function askCompact() {
    const a = one;
    if (a) confirm = { title: dialogCopy.compact.title(a.name), body: dialogCopy.compact.body, button: dialogCopy.compact.primary, run: () => Archives.Compact(a.id) };
  }

  function askRekey() {
    const a = one;
    if (a) confirm = { title: dialogCopy.rekeyArchive.title(a.name), body: dialogCopy.rekeyArchive.body, how: dialogCopy.rekeyArchive.how, button: dialogCopy.rekeyArchive.primary, run: () => Archives.RotateKey(a.id) };
  }

  function toggleHidden() {
    const a = one;
    if (a) void run(a.hidden ? Archives.Unhide(a.id) : Archives.Hide(a.id));
  }

  // The row's facts the table greys by: the vault, then the row.
  const facts = $derived<ArchFacts | null>(
    one
      ? {
          unlocked,
          tampered,
          open: !!one.open,
          held: store.leftHeld(one.id),
          running: store.runningOps.some((o) => o.archiveId === one.id),
          forgotten: hideStop,
          compacting: compactStop,
          hidden: !!one.hidden,
          missing: one.note === "archive.file_missing",
          foreign,
          detailsRead: !!det,
        }
      : null,
  );
  const cmds = $derived(archCommands(facts));

  // runArch: one function per command, whichever surface offered it — the
  // bar, a More, the row's menu. *Close* is the graceful leave, the head's
  // and the rail row's by the same word (APP.md §2.3); *Force close* the
  // kill switch, once a Close has left the archive held.
  function runArch(cmd: ArchCmd) {
    const a = one;
    if (!a) return;
    switch (cmd) {
      case "open":
        openRow(a.id);
        break;
      case "close":
        void store.closeArchive(a.id);
        break;
      case "forceClose":
        void store.closeOpened(a.id);
        break;
      case "showInExplorer":
        void run(Shell.Reveal(a.path));
        break;
      case "refreshFingerprint":
        refreshFingerprint();
        break;
      case "compact":
        askCompact();
        break;
      case "rekey":
        askRekey();
        break;
      case "hide":
      case "unhide":
        toggleHidden();
        break;
      case "details":
        if (det) showDetails = true;
        break;
      case "locate":
        void locate(a);
        break;
      case "forget":
        startDelete("forget", a);
        break;
      case "delete":
        startDelete("delete", a);
        break;
      case "restore":
        restore(a);
        break;
    }
  }

  const asMenu = (items: ArchItem[]): MenuItem[] => items.map((i) => ({ ...i, run: () => runArch(i.cmd) }));
  // The three verbs that fold, in the bar's order (lib/cmdbar.ts); Close
  // while the row is open; Hide; the two that destroy, last and in the
  // danger ink (direction 6).
  const verbItems = $derived(asMenu(cmds.verbs));
  const closeItems = $derived(asMenu(cmds.close));
  const hideItems = $derived(asMenu(cmds.hide));
  const destroyItems = $derived(asMenu(cmds.destroy));

  // ---- the bar's fold (lib/cmdbar.ts) ---------------------------------------
  // kept: how many of the three verbs the bar has room for. The bar is
  // measured once per resize (a ResizeObserver, one frame at most): what
  // never folds as it stands, and the verbs' own widths off an unseen copy
  // of them, so a folded verb is measured all the same.
  let barEl = $state<HTMLDivElement | undefined>();
  let verbProbe = $state<HTMLDivElement | undefined>();
  let kept = $state(3);
  $effect(() => {
    const bar = barEl;
    const probe = verbProbe;
    if (!bar || !probe) return;
    let raf = 0;
    const measure = () => {
      raf = 0;
      const gap = parseFloat(getComputedStyle(bar).columnGap) || 0;
      // What never folds, and a gap for each: the spring between More and
      // the filter is one item more, of no width of its own — its width
      // now is the room left over, which is what is being measured.
      const fixed = Array.from(bar.querySelectorAll<HTMLElement>(":scope > [data-bar='fixed']"));
      const fixedW = fixed.reduce((n, el) => n + el.getBoundingClientRect().width, 0) + gap * fixed.length;
      const sep = probe.querySelector<HTMLElement>(".sep")?.getBoundingClientRect().width ?? 0;
      const verbs = Array.from(probe.querySelectorAll<HTMLElement>(".btn")).map((el) => el.getBoundingClientRect().width + gap);
      if (verbs.length) verbs[0] += sep + gap;
      kept = fold(verbs, bar.clientWidth - fixedW);
    };
    const seen = new ResizeObserver(() => {
      if (!raf) raf = requestAnimationFrame(measure);
    });
    seen.observe(bar);
    // The verbs' widths are the font's: measured again once it is in.
    void document.fonts?.ready.then(() => {
      if (!raf) raf = requestAnimationFrame(measure);
    });
    return () => {
      seen.disconnect();
      cancelAnimationFrame(raf);
    };
  });

  // More (the bar's): the verbs the bar had no room for, at its top in the
  // bar's order, then Close while the row is open, then Hide, then the two
  // that destroy (lib/menu.ts groups).
  const barMore = $derived(groups(folded(verbItems, kept), closeItems, hideItems, destroyItems));
  // More (the pane's): the row's verbs, every one of them, then the same.
  const paneMore = $derived(groups(verbItems, closeItems, hideItems, destroyItems));

  // ---- View (direction 6) ----------------------------------------------------
  function toggleShowHidden() {
    store.showHidden = !store.showHidden;
    void store.refreshArchives();
  }

  function toggleForgotten() {
    view = view === "forgotten" ? "list" : "forgotten";
  }

  const viewItems = $derived<MenuItem[]>([
    { label: archivesCopy.showHidden, checked: store.showHidden, run: toggleShowHidden },
    {
      // Its count only while it can be counted: a locked vault lists
      // only what is open.
      label: unlocked ? archivesCopy.forgottenView(forgottenAll.length) : archivesCopy.forgottenPlain,
      checked: view === "forgotten",
      disabled: !unlocked || (view !== "forgotten" && forgottenAll.length === 0),
      // The reason only while greyed: MenuButton shows an enabled item's
      // title as its tooltip, and "No archive is forgotten." over
      // Forgotten (2) would be false.
      title: !unlocked ? why.locked : view !== "forgotten" && forgottenAll.length === 0 ? archivesCopy.noneForgotten : undefined,
      run: toggleForgotten,
    },
  ]);

  // ---- the pane with nothing selected: the vault's summary (direction 6) ---
  // The figures are the whole registry's but the forgotten records', and
  // only while unlocked: a locked vault's list (the core's ListArchives)
  // holds just what is open, so a count from it would understate the
  // vault — then the head says it is locked and how many archives are
  // open, which is what can be known (APP.md §6). The
  // last backup is the Vault page's line (lib/backup.ts), the date alone,
  // in the attention tier while a backup is owed; and one row for each
  // archive the list marks missing, with its cure.
  const figures = $derived(unlocked ? vaultFigures(every ?? store.archives) : null);
  const backupAt = $derived(store.backup?.at ?? store.lastExportAt);
  const backupOwed = $derived(backupLine(store.backup ?? (backupAt ? { at: backupAt, archives: 0, missing: 0, rekeyed: 0 } : null), unlocked && !tampered && !!store.backup, "").suggestExport);
  // The missing rows are the vault's, not the filter's: read from the
  // whole registry as the list view shows it (hidden ones with Show
  // hidden), whatever the filter box holds.
  const missingRows = $derived(view === "list" ? viewRows(unlocked ? (every ?? store.archives) : store.archives, "list", store.showHidden).filter((a) => a.note === "archive.file_missing") : []);

  // *Export…* from the summary is the Vault page's own *Export backup…*:
  // the backups card is where its ceremony and its receipt are drawn, so
  // it goes there and the page starts it (store.askExport).
  function toBackups() {
    store.askExport();
  }
  // The bottom panel's line carries the summary with nothing selected,
  // and its backup row at the right, while there are figures to give.
  const panelSum = $derived(narrow && !pane && unlocked && !emptyShown);

  // ---- a forgotten row's own buttons (the Forgotten view) -------------------
  function restore(a: ArchiveSummary) {
    void run(Archives.Restore(a.id)).then(() => store.refreshArchives());
  }

  // ---- the row menus (direction 7, Explorer's habits; lib/archcmds.ts) ------
  // A right-click on a row selects it — one archive at a time — and opens
  // its menu: the same commands as the bar and the pane's More, Open the
  // default (bold, Enter). The Forgotten view's rows offer Restore and
  // Delete archive… alone. The list's blank area offers what makes or
  // brings an archive: New archive…, Open file…. Shift+F10 and the Menu key
  // open the focused row's menu under it (lib/menu.ts keyAnchor), or the
  // blank area's with no row; Escape closes it and the focus goes back to
  // the row. The menu is the app's (ContextMenu): 2026-09-06 turned
  // WebView2's own off, and an app-drawn menu is not that one.
  let ctx = $state<{ x: number; y: number; row: string | null; key: boolean } | null>(null);
  // When a menu was last opened from the keyboard: the WebView's own
  // contextmenu event that follows the key is its echo (KEY_ECHO_MS).
  let keyMenuAt = -Infinity;
  const echo = () => performance.now() - keyMenuAt < KEY_ECHO_MS;

  const rowItems = $derived(asMenu(rowMenu(cmds, view === "forgotten")));
  const blankItems = $derived<MenuItem[]>(blankMenu(unlocked, tampered).map((i) => ({ ...i, run: i.cmd === "newArchive" ? openCreate : () => void openFile() })));
  const ctxItems = $derived(!ctx ? [] : ctx.row ? rowItems : blankItems);

  function rowCtx(e: MouseEvent, a: ArchiveSummary) {
    e.preventDefault();
    e.stopPropagation();
    if (echo()) return;
    const rides = willRide(a.id);
    pick(a.id);
    (e.currentTarget as HTMLElement).focus({ preventScroll: true });
    if (!rides) {
      ctx = { x: e.clientX, y: e.clientY, row: a.id, key: false };
      return;
    }
    const x = e.clientX;
    afterRide(a.id, (el) => {
      ctx = { x, y: el.getBoundingClientRect().bottom, row: a.id, key: false };
    });
  }

  // With the narrow panel up, picking another row sends the list riding
  // that row to its first line (moveBar → shift, APP.md §6) over the
  // frames of the glide. A menu put up at once would stand where the row
  // was, and the ride's scroll would close it (ContextMenu closes on a
  // scroll under it). So the menu waits for the ride: the effect that
  // starts it runs on the next tick, then the one frame clock is watched
  // until it stops, and the menu opens under the row where it came to
  // rest. A later menu, or a press that took the selection elsewhere,
  // drops the wait.
  let rideWait = 0;
  function willRide(id: string): boolean {
    return narrow && bar.open && bar.id !== id;
  }
  function afterRide(id: string, open: (el: HTMLElement) => void) {
    const mine = ++rideWait;
    void tick().then(() => {
      const wait = () => {
        if (mine !== rideWait || selId !== id) return;
        if (frame) {
          requestAnimationFrame(wait);
          return;
        }
        const el = listEl?.querySelector<HTMLElement>(`tr[data-id="${CSS.escape(id)}"]`);
        if (el) open(el);
      };
      wait();
    });
  }

  // The blank area: as a click there does, it clears the selection. A
  // header cell is not blank.
  function blankCtx(e: MouseEvent) {
    e.preventDefault();
    const t = e.target as HTMLElement;
    if (echo() || t.closest("thead") || t.closest("tbody tr")) return;
    selId = null;
    ctx = { x: e.clientX, y: e.clientY, row: null, key: false };
  }

  // keyMenu: the row the keys start from (focusIndex), selected as a
  // right-click selects it; with none, the blank area's menu at the
  // list's top left, under its header.
  function keyMenu(a: ArchiveSummary | null) {
    keyMenuAt = performance.now();
    if (!a) {
      const box = listEl?.getBoundingClientRect();
      if (!box) return;
      const head = listEl?.querySelector("thead")?.getBoundingClientRect().height ?? 0;
      ctx = { ...keyAnchor({ left: box.left, bottom: box.top + head }), row: null, key: true };
      return;
    }
    const rides = willRide(a.id);
    pick(a.id);
    const el = listEl?.querySelector<HTMLElement>(`tr[data-id="${CSS.escape(a.id)}"]`);
    if (!el) return;
    el.focus({ preventScroll: true });
    if (rides) {
      afterRide(a.id, (at) => {
        ctx = { ...keyAnchor(at.getBoundingClientRect()), row: a.id, key: true };
      });
      return;
    }
    // reveal scrolls now and the rect below is read after it, so the
    // menu stands under the row where it has come to; the scroll event
    // the browser sends next frame is the opening's (ContextMenu).
    if (!(narrow && bar.open)) reveal(el);
    ctx = { ...keyAnchor(el.getBoundingClientRect()), row: a.id, key: true };
  }

  // A key or a command puts the focus back on the row; a press elsewhere
  // leaves it where that press put it.
  function closeCtx(how: MenuClose) {
    const c = ctx;
    ctx = null;
    if (how === "pointer" || !c?.row) return;
    listEl?.querySelector<HTMLElement>(`tr[data-id="${CSS.escape(c.row)}"]`)?.focus({ preventScroll: true });
  }

  // The foot's note (LayerFoot): the one selected, else how many are, else
  // how many there are. The archive opened last, while it is the one row
  // selected, says what Enter does (direction 3, phase 4a).
  $effect(() => {
    store.footNote = one && one.id === store.remembered ? archivesCopy.enterOpens(one.name) : one ? archivesCopy.selectedName(one.name) : plural(rows.length, "archive");
  });

  // After an unlock, the archive opened last in this vault is the likely
  // next one (direction 3, phase 4a; the store remembers it in memory
  // only): its row is selected and focused once the list holds it, so
  // Enter opens it. Asked once; a row the list does not show is not
  // looked for again.
  $effect(() => {
    const want = store.focusArchiveAsked;
    if (!want || store.archives.length === 0) return;
    store.takeFocusAsked();
    if (!rows.some((a) => a.id === want)) return;
    pick(want);
    void tick().then(() => listEl?.querySelector<HTMLElement>(`tr[data-id="${CSS.escape(want)}"]`)?.focus());
  });
</script>

<svelte:window onkeydown={onKey} />

<div class="layer-head">
  <h1 class="t-title">{archivesCopy.title}</h1>
  <!-- One caption line for the vault, the count included: a pill is for
       a state, and Locked is the head's one (APP.md §13, ruled
       2026-10-17). The block's part is one expression, separator and
       all: Svelte trims the white space at a block's edges, which made
       "stored· vault file". -->
  {#if emptyShown}
    {#if st}<span class="t-quiet num meta">{`vault file ${bytes(st.vaultFileSize)} · changed ${dateTime(st.modifiedAt)}`}</span>{/if}
  {:else}
    <span class="t-quiet num meta">{plural(rows.length, "archive")} shown · {bytes(shownSize)} stored{#if st}{` · vault file ${bytes(st.vaultFileSize)} · changed ${dateTime(st.modifiedAt)}`}{/if}</span>
  {/if}
  <!-- The way into the Forgotten view while any record is forgotten
       (direction 6), and out of it while it is up: a chip that is a
       button, pressed while the view is shown. -->
  {#if unlocked && (forgottenAll.length > 0 || view === "forgotten")}
    <button type="button" class="chip fchip" class:on={view === "forgotten"} aria-pressed={view === "forgotten"} title={view === "forgotten" ? archivesCopy.forgottenChipClose : archivesCopy.forgottenChipOpen} onclick={toggleForgotten}>
      {#if view === "forgotten"}<svg class="i i-14" aria-hidden="true"><use href="#i-x" /></svg>{/if}{archivesCopy.forgottenChip(forgottenAll.length)}
    </button>
  {/if}
  {#if !unlocked}<span class="chip"><svg class="i i-14"><use href="#i-lock" /></svg>{chipCopy.locked}</span>{/if}
</div>

<!-- The page is a drop target with no data-archive-id: Wails emits a drop
     only over an element marked data-file-drop-target, so without it an
     .efd dropped here never reaches the store, which opens it (APP.md §3,
     §14; ruled 2026-10-14). The file list's own target, on the Archive
     page, carries the ids. -->
<div class="layer-body" bind:this={bodyEl} data-file-drop-target="true">
  {#if !emptyShown}
    <!-- The command bar (direction 6): one 40 px line that never wraps.
         Open, the one accent; then what makes or brings an archive; then
         the selected archive's three verbs, which fold into More from the
         right when the bar has no room (lib/cmdbar.ts — the items marked
         data-bar="fixed" are what never folds, the spring between them
         being the room left); then Filter and View at the
         right. Find missing files and Import archives from a backup… are
         the Vault page's (direction 10). Never gated on the archive being
         open (APP.md §2.3, ruled 2026-09-10): the core opens it for an
         operation and closes it again. -->
    <div class="cmdbar oneline" bind:this={barEl}>
      <button type="button" class="btn accent" data-bar="fixed" disabled={!one || (!unlocked && !one.open) || !!rowStop} title={!one ? archivesCopy.selectFirst : rowWhy} onclick={() => one && openArchive(one.id)} use:waiting={{ on: opening?.p ?? null, armed: (b) => (openArmed = b) }}><PendingLabel armed={openArmed} icon="i-open" rest={commandCopy.open} busy={pendingCopy.opening} /></button>
      <button type="button" class="btn" data-bar="fixed" disabled={!unlocked || tampered} title={opReason ?? keyTitles.newArchive} onclick={openCreate}><svg class="i i-14"><use href="#i-plus" /></svg>{commandCopy.newArchive}</button>
      <!-- Never greyed: the chain answers a locked vault with the lock
           scene, and a file whose key is not here with its own dialog, as
           for a double-click in Explorer (APP.md §14). -->
      <button type="button" class="btn subtle" data-bar="fixed" title={keyTitles.openFile} onclick={() => void openFile()}><svg class="i i-14"><use href="#i-file" /></svg>{archivesCopy.openFile}</button>
      {#if kept > 0}<div class="sep"></div>{/if}
      {#if kept > 0}<button type="button" class="btn subtle" disabled={verbStop} title={verbWhy} onclick={refreshFingerprint}><svg class="i i-14"><use href="#i-check" /></svg>{commandCopy.refreshFingerprint}</button>{/if}
      {#if kept > 1}<button type="button" class="btn subtle" disabled={verbStop} title={verbWhy} onclick={askCompact}><svg class="i i-14"><use href="#i-box" /></svg>{commandCopy.compact}</button>{/if}
      {#if kept > 2}<button type="button" class="btn subtle" disabled={verbStop} title={verbWhy} onclick={askRekey}><svg class="i i-14"><use href="#i-rotate" /></svg>{commandCopy.rekeyArchive}</button>{/if}
      <div class="barslot" data-bar="fixed"><MenuButton id="arch-more" label={archivesCopy.more} title={one ? archivesCopy.more : archivesCopy.selectFirst} icon="i-more" iconOnly disabled={!one} items={barMore} /></div>
      <div class="grow"></div>
      <label class="filterbox" data-bar="fixed"><span class="vh">{archivesCopy.filter}</span><input class="input" type="search" placeholder={archivesCopy.filter} title={keyTitles.filter} bind:this={filterEl} bind:value={filter} use:fieldMenu /></label>
      <div class="barslot" data-bar="fixed"><MenuButton id="arch-view" label={archivesCopy.view} icon="i-view" end items={viewItems} /></div>
    </div>
    <!-- The verbs as the bar draws them, unseen, so a folded one is still
         measured (lib/cmdbar.ts). -->
    <div class="cmdbar verb-probe" aria-hidden="true" inert bind:this={verbProbe}>
      <div class="sep"></div>
      <span class="btn subtle"><svg class="i i-14"><use href="#i-check" /></svg>{commandCopy.refreshFingerprint}</span>
      <span class="btn subtle"><svg class="i i-14"><use href="#i-box" /></svg>{commandCopy.compact}</span>
      <span class="btn subtle"><svg class="i i-14"><use href="#i-rotate" /></svg>{commandCopy.rekeyArchive}</span>
    </div>
  {/if}

  <!-- The notice slot (direction 4, phase 4b; NoticeSlot): the page's
       stateful banners between the commands and the list, in their tiers'
       order, each opening and closing its height so the list slides; and
       the InfoBars over the list's top, stopping at its column. -->
  <NoticeSlot beside={emptyShown ? undefined : "archives"}>
    {#if tampered}
      <!-- Danger, first (direction 9; APP.md §13): the Tampered banner,
           with its fix. It stood above the commands it greys until the
           slot (direction 4) gave every banner one place under them. -->
      <div class="fold" transition:unfold><TamperedBar reason={st?.tamperedReason} /></div>
    {/if}
    {#if !unlocked}
      <!-- The locked banner: one line of 40 px, everything on it centred
           (APP.md §7, ruled 2026-09-11). Attention since direction 9 (ruled
           2026-10-18): what the lock left open is still decrypted and still
           saves (APP.md §2.3), so it names them (VaultStatus.opened) and
           offers their close beside the unlock: the leave, or the kill
           switch once a leave has left them held (ruled 2026-10-19). -->
      <div class="fold" transition:unfold>
        <div class="bar line attention">
          <svg class="i i-14"><use href="#i-lock" /></svg>
          <span class="grow"><strong>{lockedLead.head}</strong> {lockedLead.rest}</span>
          {#if openedNames.length > 0}
            <button type="button" class="btn sm" onclick={closeLocked}>{lockedClose.label}</button>
          {/if}
          <button type="button" class="btn sm accent" onclick={() => store.unlock()}>{commandCopy.unlock}</button>
        </div>
      </div>
    {/if}
  </NoticeSlot>

  {#if view === "forgotten"}
    <!-- The Forgotten view says what it is, once, above its rows. -->
    <p class="t-quiet fnote">{archivesCopy.forgottenNote}</p>
  {/if}

  <div class="arch-split" class:bare={emptyShown} bind:this={splitEl}>
    <!-- The list's whole column is its blank area for a right-click
         (direction 7), the room under a short table included; a row's
         own menu stops the event before it gets here. -->
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div class="arch-list" oncontextmenu={blankCtx}>
      <!-- A click on the list's blank area clears the selection (APP.md
           §6); a click that lands on a row is the row's, and a header
           cell sorts. -->
      <!-- svelte-ignore a11y_no_noninteractive_element_interactions, a11y_click_events_have_key_events -->
      <div class="tablewrap" role="region" aria-label={archivesCopy.listLabel} bind:this={listEl} onclick={blank}>
        {#if emptyShown}
          <!-- A vault with no archives yet says what to do, with the
               buttons that do it (ruled 2026-10-14; direction 6), not
               greyed verbs over an empty line: no bar, no pane.
               *Import archives from a backup…* is the Vault page's
               command (direction 10), run here as it runs there — its
               picker, then the merge dialog over every page. A command is
               a button, so it is a small subtle one, not a hyperlink
               (APP.md §6, Controls; ruled 2026-10-17). -->
          <div class="empty first">
            <svg class="i i-20" aria-hidden="true"><use href="#i-archives" /></svg>
            <h2 class="t-section">{archivesCopy.emptyVault}</h2>
            <div class="row">
              <button type="button" class="btn accent" disabled={tampered} onclick={openCreate}><svg class="i i-14"><use href="#i-plus" /></svg>{commandCopy.newArchive}</button>
              <button type="button" class="btn" onclick={() => void openFile()}><svg class="i i-14"><use href="#i-file" /></svg>{archivesCopy.openFile}</button>
            </div>
            <button type="button" class="btn subtle sm" disabled={tampered} onclick={importRecords}>{commandCopy.importArchives}</button>
          </div>
        {:else if view === "list" && rows.length === 0 && !filter.trim() && vaultEmpty === null}
          <div class="empty"></div>
        {:else if rows.length === 0}
          <div class="empty">{filter.trim() ? archivesCopy.noMatch : view === "forgotten" ? archivesCopy.noneForgotten : unlocked ? archivesCopy.allHidden : archivesCopy.noneOpen}</div>
        {:else}
          <!-- Three columns (direction 6): Name, two lines in a 52 px row;
               Size, right-aligned so magnitudes line up; Last saved. The
               Forgotten view adds the row's own two buttons at its right.
               One sort glyph, the 10 px chevron, on the sorter's own hover
               fill, as on the file list. -->
          <table>
            <colgroup><col /><col class="w-size" /><col class="w-date" />{#if view === "forgotten"}<col class="w-act" />{/if}</colgroup>
            <thead>
              <tr>
                <th scope="col" aria-sort={ariaSort("name")}><button type="button" class="sorter" onclick={() => sortOn("name")}>Name<svg class="i caret" class:on={sortKey === "name"} class:asc={sortAsc}><use href="#i-chevdown" /></svg></button></th>
                <th scope="col" class="num" aria-sort={ariaSort("size")}><button type="button" class="sorter" onclick={() => sortOn("size")}>Size<svg class="i caret" class:on={sortKey === "size"} class:asc={sortAsc}><use href="#i-chevdown" /></svg></button></th>
                <th scope="col" aria-sort={ariaSort("saved")}><button type="button" class="sorter" onclick={() => sortOn("saved")}>Last saved<svg class="i caret" class:on={sortKey === "saved"} class:asc={sortAsc}><use href="#i-chevdown" /></svg></button></th>
                {#if view === "forgotten"}<th scope="col"><span class="vh">{archivesCopy.forgottenChipOpen}</span></th>{/if}
              </tr>
            </thead>
            <tbody bind:this={rowsEl}>
              {#each rows as a, i (a.id)}
                <!-- svelte-ignore a11y_no_noninteractive_tabindex a11y_no_noninteractive_element_interactions a11y_click_events_have_key_events -->
                <!-- data-id: how the panel finds the row it must bring to
                     the list's first line (lib/archbar.ts). The keys are
                     the page's one handler (onKey), not the row's. -->
                <tr data-id={a.id} tabindex={selId === a.id || (!paneId && i === 0) ? 0 : -1} aria-selected={selId === a.id} class:forgotten={a.forgottenAt > 0} onclick={() => click(a.id)} oncontextmenu={(e) => rowCtx(e, a)} ondblclick={() => openRow(a.id)}>
                  <td class="sel-mark">
                    <div class="aname">
                      <svg class="i i-14"><use href="#i-box" /></svg>
                      <div class="two-line">
                        <!-- Line 1: the name, cut before its chips are. -->
                        <div class="nline">
                          <b class="ellipsis">{a.name}</b>
                          <!-- The open's ring stands here once the open has
                               taken 300 ms (direction 4). The Missing chip
                               is a button that runs Locate… for its row
                               (direction 7): the chip names the trouble,
                               the button is its cure, greyed by Locate…'s
                               own rule. -->
                          <span class="chips" use:waiting={opening?.id === a.id ? opening.p : null}>
                            {#each chipKinds(a, store.runningOps, view) as k (k)}
                              {#if k === "missing"}
                                <button type="button" class="st missing" tabindex="-1" disabled={!unlocked || a.forgottenAt > 0} title={!unlocked ? codeText(Code.CodeVaultLocked) : a.forgottenAt > 0 ? codeText(Code.CodeArchiveForgotten) : archivesCopy.missingChip(codeText(a.note))} onclick={(e) => { e.stopPropagation(); pick(a.id); void locate(a); }} ondblclick={(e) => e.stopPropagation()}>{chipLabel(k)}</button>
                              {:else}
                                <span class="st {k}" title={chipTitle(k, a)}>{chipLabel(k)}</span>
                              {/if}
                            {/each}
                          </span>
                        </div>
                        <!-- Line 2: the day a forgotten record's key goes;
                             else the description; else the file's folder,
                             cut from the left so its own name stays. -->
                        {#if view === "forgotten"}
                          <small class="ellipsis">{archivesCopy.keptUntil(date(purgeAfter(a.forgottenAt)))}</small>
                        {:else if a.description}
                          <small class="ellipsis" title={a.description}>{a.description}</small>
                        {:else}
                          <small class="lcut" title={a.path}><bdi>{folderOf(a.path)}</bdi></small>
                        {/if}
                      </div>
                    </div>
                  </td>
                  <!-- Size alone is a .num cell, right-aligned (APP.md §6);
                       the date keeps its tabular digits on the left. -->
                  <td class="num">{a.storedSize ? bytes(a.storedSize) : "—"}</td>
                  <td><span class="num">{dateTime(a.lastWrittenAt)}</span></td>
                  {#if view === "forgotten"}
                    <td class="act">
                      <div class="rbtns">
                        <button type="button" class="btn sm" disabled={!unlocked || tampered} title={opReason} onclick={(e) => { e.stopPropagation(); pick(a.id); restore(a); }} ondblclick={(e) => e.stopPropagation()}>{commandCopy.restore}</button>
                        <button type="button" class="btn sm subtle danger" disabled={!unlocked || tampered || compacting(a, store.runningOps)} title={opReason} onclick={(e) => { e.stopPropagation(); pick(a.id); startDelete("delete", a); }} ondblclick={(e) => e.stopPropagation()}>{commandCopy.deleteArchive}</button>
                      </div>
                    </td>
                  {/if}
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

    {#if !emptyShown}
      <!-- The pane follows the selected row, and with none it is the
           vault's summary (direction 6). Below the stylesheet's threshold
           this one element is the bottom panel (APP.md §6, refined
           2026-09-12): its header is the whole of it when collapsed and
           stays fixed at its top when it is up, and the body below scrolls
           under that header. Above the threshold it is the 300 px side
           pane. -->
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
          <!-- The header: a heading above the threshold; below it, the
               control — the whole header but the actions at its right
               (direction 6), which are buttons of their own and so stand
               beside the control, never inside it. The collapsed line and
               the two-line stack are the same heading in its two shapes,
               the header a fixed 64 px whose top 44 px are the line, and
               the two cross-fade as the card grows. -->
          <div class="details-head" style:--acts="{narrow && (pane || panelSum) && actsW ? actsW + 8 : 0}px">
            <!-- The role and the tabindex are one decision the compiler
                 cannot see through: below the threshold this is a button,
                 above it part of a heading, so both are conditional and
                 neither is a lie. -->
            <!-- svelte-ignore a11y_no_static_element_interactions, a11y_click_events_have_key_events, a11y_no_noninteractive_tabindex -->
            <div
              class="head-tap"
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
              <div class="head-stack" aria-hidden={pane ? undefined : "true"} bind:this={stackEl}><div class="eyebrow">{archivesCopy.panelEyebrow}</div><h3>{pane?.name ?? ""}</h3></div>
              <!-- With nothing selected the collapsed line is the vault's
                   summary, its head as the side pane's (the final shoot
                   of phase 4a: below the threshold it was an empty
                   "Selected" line and the summary was nowhere). -->
              <div class="head-line" aria-hidden={pane ? "true" : undefined} bind:this={lineEl}>{#if pane || !narrow}<span class="eyebrow">{archivesCopy.panelEyebrow}</span><span class="hname">{pane?.name ?? ""}</span>{:else}<span class="hsum">{@render sumHead()}</span>{/if}</div>
              <svg class="i i-14 chev" class:up={!bar.open}><use href="#i-chevdown" /></svg>
            </div>
            {#if narrow && pane}
              <div class="head-acts" bind:clientWidth={actsW}>
                {@render actions("panel", true)}
              </div>
            {:else if panelSum}
              <!-- The summary's backup row at the line's right: the one
                   fact the list does not carry itself (each missing file
                   is its row's Missing chip, with Locate… in it). -->
              <div class="head-acts" bind:clientWidth={actsW}>
                {@render backupRow()}
              </div>
            {/if}
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
                      <span>{archivesCopy.forgottenLine(date(pane.forgottenAt), date(purge))}</span>
                      <button type="button" class="btn sm" disabled={!one || !unlocked || tampered} onclick={() => one && restore(one)}>{commandCopy.restore}</button>
                    </div>
                  </div>
                </div>
              {/if}

              <div class="pbody">
                <!-- The facts first (direction 6): the one label · value
                     list (APP.md §6, Type and spacing), each value beside
                     its label. User words only (ruled 2026-10-17): the key
                     version, the KID and the receipt are Technical
                     details'. -->
                <dl class="kv pfacts">
                  <div><dt>{archivesCopy.facts.size}</dt><dd class="num">{pane.storedSize ? bytes(pane.storedSize) : "—"}</dd></div>
                  <div><dt>{archivesCopy.facts.lastSaved}</dt><dd class="num">{dateTime(pane.lastWrittenAt)}</dd></div>
                  <div><dt>{archivesCopy.facts.created}</dt><dd class="num">{det ? dateTime(det.createdAt) : "—"}</dd></div>
                  <div><dt>{archivesCopy.facts.compression}</dt><dd>{methodWord(pane.method)}</dd></div>
                  <!-- Which copy of the file the registry names (direction 9,
                       ruled 2026-10-18): the hash Refresh fingerprint
                       records, against the saves since — never "verified",
                       since it checks nothing inside the file (APP.md
                       §2.3). Drawn only from the record read whole: "Not
                       taken yet" in the third ink when no hash was ever
                       taken, no tick unless it matches. -->
                  {#if fp}<div><dt>{archivesCopy.facts.fingerprint}</dt><dd class="num">{#if fp.kind === "current"}<span class="fp"><svg class="i i-14" aria-hidden="true"><use href="#i-check" /></svg>{archivesCopy.fingerprint(fp)}</span>{:else}<span class:none={fp.kind === "none"}>{archivesCopy.fingerprint(fp)}</span>{/if}</dd></div>{/if}
                </dl>

                <div class="pside">
                  <!-- A path is the stacked form: its label over it, and the
                       path wrapping rather than cut (APP.md §6, §13). -->
                  <div class="pathblock">
                    <dl class="kv stk"><dt>{foreign ? archivesCopy.facts.foreign : archivesCopy.facts.file}</dt><dd>{pane.path || "—"}</dd></dl>
                    <div class="row links">
                      {#if !foreign}<button type="button" class="btn link" onclick={() => one && void Shell.Reveal(one.path)}>{commandCopy.showInExplorer}</button><span class="dot" aria-hidden="true">·</span>{/if}
                      <button type="button" class="btn link" disabled={!unlocked || hideStop} title={!unlocked ? codeText(Code.CodeVaultLocked) : forgotWhy} onclick={() => one && locate(one)}>{commandCopy.locate}</button>
                    </div>
                  </div>

                  {#if pane.note === "archive.file_missing"}
                    <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span class="grow">{codeText(pane.note)}</span></div>
                  {/if}

                  <!-- Name and description, behind the pencil (direction
                       6): the description read at rest, the two fields
                       and the save bar under them once it is pressed —
                       the staged edit the bar is for (APP.md §6, the save
                       bar; ruled 2026-09-07), in flow, never over the
                       actions. Discard folds them back. -->
                  {#if draft}
                    <section class="namedesc" aria-label={archivesCopy.nameAndDescription}>
                      <div class="nd-head">
                        <span class="nd-label">{archivesCopy.nameAndDescription}</span>
                        <button type="button" class="btn subtle sm icon-only" aria-label={archivesCopy.editNameDescription} aria-expanded={fieldsShown} aria-controls="ar-fields" disabled={!editable} title={editReason ?? keyHint(archivesCopy.editNameDescription, "F2")} onclick={pencil}><svg class="i i-14"><use href="#i-rename" /></svg></button>
                      </div>
                      {#if fieldsShown}
                        <div class="fields" id="ar-fields">
                          <div class="field">
                            <div class="field-top"><label for="ar-name">{archivesCopy.facts.name}</label></div>
                            <input id="ar-name" class="input" class:invalid={nameChanged && !!nameProblem(draft.name)} aria-invalid={nameChanged && nameProblem(draft.name) ? "true" : undefined} bind:value={store.recordDraft[pane.id].name} disabled={!editable} title={editReason} use:fieldMenu />
                          </div>
                          <div class="field">
                            <div class="field-top"><label for="ar-desc">{archivesCopy.facts.description}</label><span class="hint">{archivesCopy.descriptionHint}</span></div>
                            <textarea id="ar-desc" class="input area" class:invalid={descChanged && !!descriptionProblem(draft.description)} aria-invalid={descChanged && descriptionProblem(draft.description) ? "true" : undefined} rows="2" bind:value={store.recordDraft[pane.id].description} disabled={!editable} title={editReason} use:fieldMenu></textarea>
                          </div>
                          {#if pending.length > 0}
                            <SaveBar items={pending} {invalid} busy={saving} onsave={() => void saveRecord()} ondiscard={discard} />
                          {/if}
                        </div>
                      {:else}
                        <p class="nd-text" class:none={!pane.description}>{pane.description || archivesCopy.noDescription}</p>
                      {/if}
                    </section>
                  {/if}
                </div>
              </div>

              {#if !narrow}
                <!-- The actions at the pane's foot (direction 6): Technical
                     details… and the row's verbs in More — and no second
                     Open, which is the bar's accent and the row's
                     double-click and Enter. -->
                <div class="details-actions">
                  <div class="row2">{@render actions("pane", false)}</div>
                </div>
              {/if}
            {:else if !narrow}
              <!-- Nothing selected (direction 6): the vault's summary — its
                   name and figures, its last backup with the way to a new
                   one, and a row for each archive the list marks missing. -->
              <div class="summary">
                <p class="sum-head">{@render sumHead()}</p>
                {@render backupRow()}
                {#each missingRows as a (a.id)}
                  <div class="sum-row">
                    <svg class="i i-14 warn" aria-hidden="true"><use href="#i-warn" /></svg>
                    <span class="grow ellipsis" title={archivesCopy.missingRow(a.name)}>{archivesCopy.missingRow(a.name)}</span>
                    <button type="button" class="btn link" disabled={!unlocked} title={!unlocked ? codeText(Code.CodeVaultLocked) : undefined} onclick={() => { if (rows.some((r) => r.id === a.id)) pick(a.id); void locate(a); }}>{commandCopy.locate}</button>
                  </div>
                {/each}
              </div>
            {/if}
          </div>
        </div>
      </aside>
    {/if}

    <!-- The stylesheet owns the threshold, and this is how the script
         reads its answer without knowing the number: a probe of no size
         that the container query gives a pixel of width below it. -->
    <i class="arch-probe" bind:this={probeEl}></i>
  </div>
</div>

<!-- A row's menu, or the blank area's (direction 7): keyed on the open,
     so a second right-click elsewhere is a menu of its own, placed anew. -->
{#if ctx}
  {@const c = ctx}
  {#key c}
    <ContextMenu x={c.x} y={c.y} keyboard={c.key} items={ctxItems} label={c.row ? (one?.name ?? "") : archivesCopy.listLabel} onclose={closeCtx} />
  {/key}
{/if}

<!-- The vault's summary's head (direction 6): the name strong, then the
     figures — over every record but the forgotten, hidden ones included
     and counted apart while the list does not show them, so it squares
     with the title's "N archives shown" (the final shoot of phase 4a). -->
{#snippet sumHead()}
  <strong>{store.vaultName || archivesCopy.vaultFallback}</strong> · {figures ? archivesCopy.summary(figures.count, bytes(figures.size), store.showHidden ? 0 : figures.hidden) : archivesCopy.summaryLocked(store.archives.filter((a) => a.open).length)}
{/snippet}

<!-- The last backup, one line with its *Export…* after it (direction 6;
     the brief's one line, where it had become a card over a second line):
     tinted attention while a backup is owed. -->
{#snippet backupRow()}
  <div class="sum-row" class:owed={backupOwed}>
    <svg class="i i-14" aria-hidden="true"><use href="#i-backup" /></svg>
    <span class="grow ellipsis">{backupAt ? archivesCopy.lastBackup(sinceText(backupAt, store.now)) : vaultCopy.backupNeverHead}</span>
    <button type="button" class="btn link" disabled={!unlocked || tampered} title={!unlocked ? codeText(Code.CodeVaultLocked) : undefined} onclick={toBackups}>{archivesCopy.exportLink}</button>
  </div>
{/snippet}

<!-- The selected archive's two actions (direction 6): the pane's foot,
     or the panel's header row, never both. -->
{#snippet actions(where: string, small: boolean)}
  <button type="button" class="btn" class:sm={small} disabled={!det || !one} title={det && one ? keyTitles.technicalDetails : undefined} onclick={() => (showDetails = true)}>{archivesCopy.technicalDetails}</button>
  <MenuButton id="{where}-more" label={archivesCopy.more} standard {small} end float items={paneMore} />
{/snippet}

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
      <button type="button" class="btn accent" onclick={create} use:waiting={createWait}>{dialogCopy.newArchive.primary}</button>
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


<style>
  /* Size and Last saved at what their content asks for ("227 GB",
     "2026-10-13 01:24"); Name takes the rest (direction 6). The Forgotten
     view's two buttons take their own words' width. */
  .w-size { width: 88px; }
  .w-date { width: 136px; }
  .w-act { width: 220px; }
  /* The head's caption sits on the title's baseline (APP.md §6, ruled
     2026-10-17). The title keeps the place centring gave it in the 62 px
     head, 2 px down its 32 px line ((32 - 28) / 2), so nothing moves
     between pages; the pills are boxes, centred on the line. */
  .layer-head { align-items: baseline; }
  .layer-head .t-title { margin-top: 2px; }
  .layer-head .chip { align-self: center; flex: none; }
  .meta { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  /* The Forgotten chip is a button into its view (direction 6): the chip's
     own box, the subtle fill on hover; pressed while the view is up, in
     the ink with the stronger stroke and the × that leaves it — never the
     accent, which is Open's on this page. */
  .fchip { cursor: default; transition: background var(--dur-out) var(--ease-enter); }
  .fchip:hover { background: var(--fill-1); transition-duration: var(--dur-hover); }
  .fchip.on { color: var(--ink); border-color: var(--stroke-strong); }
  .fchip .i { color: var(--ink-3); }
  /* The Forgotten view's one line about itself, over its rows. */
  .fnote { margin: 0; flex: none; }
  /* The command bar's slots: a menu button standing in the bar as one item. */
  .barslot { display: inline-flex; flex: none; }
  /* The verbs as the bar draws them, out of the flow and unseen: what
     lib/cmdbar.ts measures a folded verb by. */
  .verb-probe { position: absolute; left: 0; top: 0; visibility: hidden; pointer-events: none; flex-wrap: nowrap; }
  /* A row's name and its chips on line 1: the name cut first, the chips
     kept whole (direction 6). */
  .aname { display: flex; align-items: flex-start; gap: var(--s2); min-width: 0; color: var(--ink); font: var(--t-body); }
  .aname > .i { color: var(--ink-3); flex: none; height: 20px; }
  .nline { display: flex; align-items: center; gap: var(--s2); min-width: 0; }
  .nline b { flex: 0 1 auto; min-width: 3em; font: var(--t-body); color: var(--ink); }
  .chips { display: inline-flex; align-items: center; gap: var(--s1); flex: none; }
  .chips:empty { display: none; }
  .chips:global(.pending) { display: inline-flex; }
  tbody tr.forgotten .nline b { color: var(--ink-2); }
  /* A folder cut from its left (direction 6): the end of the path — the
     folder's own name — is what stays, the ellipsis at the start. The
     path itself stays left-to-right inside its isolate. */
  .lcut { direction: rtl; text-align: left; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .lcut bdi { direction: ltr; unicode-bidi: isolate; }
  /* The Missing chip as a button: the chip's own box and words, a pointer,
     and the danger ink's stroke on hover; greyed, it is the chip again. */
  .chips button.st { cursor: pointer; transition: border-color var(--dur-hover) var(--ease-enter); }
  .chips button.st.missing:hover:not(:disabled) { border-color: var(--danger); }
  .chips button.st:disabled { cursor: default; }
  /* A forgotten row's own buttons, at its right, on the name's line. */
  td.act { overflow: visible; }
  .rbtns { display: flex; justify-content: flex-end; gap: var(--s2); margin-top: -4px; }
  /* The empty vault's block: centred, the two buttons on one line and the
     import a small subtle button under them. */
  .empty.first { display: flex; flex-direction: column; align-items: center; gap: var(--s3); }
  .empty.first > .i { color: var(--ink-3); }
  .empty.first h2 { color: var(--ink); }
  /* The pane's banner at its inset. */
  .pad { padding: var(--s3) var(--s4) 0; }
  /* The pane's body (direction 6): the facts, then the file, then the name
     and description, each 16 apart at the card's inset. The bottom panel
     lays the same parts out in two columns (app.css). */
  .pbody { display: flex; flex-direction: column; gap: var(--s4); padding: var(--s3) var(--s4) var(--s4); min-width: 0; }
  .pside { display: flex; flex-direction: column; gap: var(--s4); min-width: 0; }
  .pathblock { display: flex; flex-direction: column; gap: var(--s1); }
  .links { gap: var(--s1); flex-wrap: wrap; align-items: center; }
  .links .dot { color: var(--ink-3); }
  /* Name and description: its label and the pencil on one line, the
     description under them at rest, the fields and the save bar once the
     pencil is pressed. */
  .nd-head { display: flex; align-items: center; justify-content: space-between; gap: var(--s2); min-height: 28px; }
  .nd-label { font: var(--t-cap); color: var(--ink-2); }
  .nd-text { margin: 0; font: var(--t-body); color: var(--ink); overflow-wrap: anywhere; }
  .fields { display: flex; flex-direction: column; gap: var(--s3); max-width: 460px; }
  /* The hint stands beside its label, not across the pane from it. */
  .fields .field-top { justify-content: flex-start; gap: var(--s2); }
  /* Two body lines and the field's own insets: the rows decide the height,
     and nothing else does — a WinUI text box has no resize grip (ruled
     2026-10-17); a longer description scrolls inside its two lines. */
  .area { height: auto; padding: var(--s2) var(--s3); resize: none; }
  /* The fingerprint's check: the i-check in the accent's ink, before
     the words, only while the fingerprint matches the last save. */
  .fp { display: inline-flex; align-items: center; gap: var(--s2); }
  .fp .i { color: var(--accent-ink); }
  .none { color: var(--ink-3); }
  .why { display: flex; flex-direction: column; align-items: flex-start; gap: var(--s2); min-width: 0; }
  /* The vault's summary with nothing selected (direction 6): the name and
     figures, then one row per fact with its command at its right — the
     last backup, in the attention tier while one is owed, and each
     archive the list marks missing, its sign in the danger ink. */
  .summary { display: flex; flex-direction: column; gap: var(--s2); padding: var(--s4); }
  .sum-head { margin: 0 0 var(--s2); font: var(--t-body); color: var(--ink-2); overflow-wrap: anywhere; }
  .sum-head strong { font: var(--t-strong); color: var(--ink); }
  /* One line each: the fact cut by its ellipsis before the command is,
     the command after it. A plain row (the mock's), and the backup's
     tinted attention while one is owed. */
  .sum-row { display: flex; align-items: center; gap: var(--s2); min-height: 32px; padding: var(--s1) var(--s1) var(--s1) var(--s2); border: 1px solid transparent; border-radius: var(--r-card); font: var(--t-body); color: var(--ink); min-width: 0; }
  .sum-row > .grow { flex: 1 1 auto; min-width: 0; }
  .details-head .sum-row { min-height: 28px; padding-top: 0; padding-bottom: 0; }
  .hsum { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--ink-2); }
  .hsum strong { font: var(--t-strong); color: var(--ink); }
  .sum-row .i { flex: none; color: var(--ink-3); }
  .sum-row .i.warn { color: var(--danger); }
  .sum-row.owed { background: var(--attention-wash); border-color: var(--attention-line); }
  .sum-row.owed .i { color: var(--attention); }
  .sum-row .btn.link { flex: none; margin-left: auto; }
  .vh { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; }
</style>

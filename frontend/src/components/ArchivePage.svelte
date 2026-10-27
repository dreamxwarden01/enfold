<script lang="ts">
  import { tick, untrack } from "svelte";
  import { Clipboard } from "@wailsio/runtime";
  import { Archive, Archives, Code, Shell, errorOf } from "../lib/api";
  import type { Clipboard as ClipboardView, Collision, Crumb, FileRow } from "../lib/api";
  import { store } from "../lib/state.svelte";
  import { archivePageCopy, archivesCopy, codeText, vaultCopy, collisionCopy, columnLabels, commandCopy, compressedLabel, compressedText, conflictCopy, conflictTitle, dialogCopy, keyNames, keyTitles, listCopy, noticeCopy, paneCopy, typeLabel } from "../lib/strings";
  import { bytes, dateTime, fileIcon, previewKind } from "../lib/format";
  import type { PreviewKind } from "../lib/format";
  import { freeWorthShowing } from "../lib/status";
  import { methodWord } from "../lib/method";
  import { CRUMB_CAP, ROOT_CAP, clampFolded, fold, menuRows } from "../lib/crumbs";
  import { closeButton } from "../lib/leave";
  import { folderFigures, paneShape, selectionFigures } from "../lib/archpane";
  import { tip } from "../lib/tip";
  import { newNameProblem } from "../lib/validate";
  import { renameSelection } from "../lib/paths";
  import { ROOT_ID, canDrop, deleteBody, deleteTitle } from "../lib/tree";
  import type { DropTarget, Kinded } from "../lib/tree";
  import { noDrag, ownNames, pastThreshold } from "../lib/dragout";
  import type { DropAt, Flight, PendingMove } from "../lib/dragout";
  import type { ExtractScope } from "../lib/extract";
  import { allOf, collisionPlan, conflictsOf, namesFor, planIsEmpty, reissuePlan, restExtracted, shownName } from "../lib/conflicts";
  import type { Decision } from "../lib/conflicts";
  import { SORT_KEYS, arrowOf, clickHeader as clickSortHeader } from "../lib/sort";
  import type { SortKey } from "../lib/sort";
  import { headerTicked } from "../lib/selection";
  import { hasMore, nearEnd } from "../lib/paging";
  import { planIsEmpty as refusedPlanIsEmpty, refusedOf, refusedPlan } from "../lib/refused";
  import type { RefusedDecision } from "../lib/refused";
  import { POPOVER_ATTR, dialogIsUp } from "../lib/dialogs";
  import { pending } from "../lib/pending";
  import { compacting } from "../lib/archlist";
  import { TYPEAHEAD_MS, controlKeepsKeys, intent, isTextEntry, jump, listScoped, pageRows, survivor, target, typeahead } from "../lib/listkeys";
  import type { Intent } from "../lib/listkeys";
  import { KEY_ECHO_MS, keyAnchor } from "../lib/menu";
  import type { MenuClose } from "../lib/menu";
  import { fileBlankItems, fileRowItems } from "../lib/filemenu";
  import type { FileBlankCmd, FileCmd } from "../lib/filemenu";
  import { clipOf, pastePlan, planOf } from "../lib/paste";
  import type { AddPlan, Clip } from "../lib/paste";
  import { PREVIEW_TEXT_CAP } from "../lib/viewer";
  import { spring, springKey, springs } from "../lib/springload";
  import ContextMenu from "./ContextMenu.svelte";
  import MoveToDialog from "./MoveToDialog.svelte";
  import Viewer from "./Viewer.svelte";
  import Dialog from "./Dialog.svelte";
  import CommandRows from "./CommandRows.svelte";
  import type { CommandRow } from "./CommandRows.svelte";
  import ConflictDialog from "./ConflictDialog.svelte";
  import DeleteArchiveDialog from "./DeleteArchiveDialog.svelte";
  import ExtractDialog from "./ExtractDialog.svelte";
  import RefusedDialog from "./RefusedDialog.svelte";
  import MenuButton from "./MenuButton.svelte";
  import type { MenuItem } from "./MenuButton.svelte";
  import TextField from "./TextField.svelte";
  import TamperedBar from "./TamperedBar.svelte";
  import NoticeSlot from "./NoticeSlot.svelte";
  import { LEAVE, delay, slideIn, travel, unfold } from "../lib/motion";

  const id = $derived(store.current ?? "");
  const stat = $derived(store.stat);
  // The index is a tree (APP.md §3): a row is a record with an id, the
  // folder shown is a directory id, and the head is the page's own Crumbs —
  // root-inclusive, the archive's name first — drawn from nothing else
  // (direction 3, phase 4a; reversing 2026-09-10's "the heading is the
  // archive's name alone"). The list's first row `..` still goes up one
  // level.
  const rows = $derived(store.page?.rows ?? []);
  const total = $derived(store.page?.total ?? 0);
  const crumbs = $derived(store.page?.crumbs ?? []);

  // List motion (direction 4, phase 4b; lib/motion.ts). A change of folder
  // fades the rows in from 8 px on the side they came from — into a folder
  // from the right, up from the left — while the header and the commands
  // stay still: the tbody alone moves, in place, so nothing is remounted
  // and the keyboard focus stays where it was. Judged on the listing that
  // landed (its crumbs), not on the request, so a folder gone from under
  // the page that leaves it higher up comes in from the left too.
  let rowsEl: HTMLTableSectionElement | undefined = $state();
  let placeShown: string[] = [];
  $effect(() => {
    const place = crumbs.map((c) => c.id);
    const from = travel(placeShown, place);
    placeShown = place;
    if (from !== 0) slideIn(untrack(() => rowsEl), from);
  });
  // The rows a Delete takes, fading out before the list is read again;
  // kept faded until the listing no longer holds them, whichever re-read
  // brings that — the page's own or the archive's change event's.
  let leaving = $state<ReadonlySet<string>>(new Set());
  $effect(() => {
    if (leaving.size === 0) return;
    const live = new Set(rows.map((r) => r.id));
    if (![...untrack(() => leaving)].some((id) => live.has(id))) leaving = new Set();
  });
  const archiveName = $derived(crumbs[0]?.name ?? "");
  const dirId = $derived(store.dirId);
  // In a folder — anywhere but the root — the parent is the crumb before
  // the last, and `..` is a row.
  const parentId = $derived(crumbs.length > 1 ? crumbs[crumbs.length - 2].id : null);
  // The folder shown, by name — the archive's at the root — for the pane at
  // rest, the foot and the empty folder's "Trips is empty".
  const folderName = $derived(crumbs[crumbs.length - 1]?.name ?? "");
  // Empty once its listing has landed: until then nothing is said of it.
  const isEmpty = $derived(!!store.page && rows.length === 0);
  const sort = $derived(store.sort);
  // Everything on this page except *Delete archive...* stays usable after
  // a lock (APP.md §2.3): Page, Stat, the previews, Extract and every
  // operation - each committing the archive and owing its receipt until
  // the vault comes back. Only the registry write needs the session.
  // The banner's one predicate (store.tampered; APP.md §6).
  const tampered = $derived(store.tampered);
  // The locked banner names this archive as the status names it
  // (VaultStatus.opened), the page's own name until the status has it.
  const lockedName = $derived(store.status?.opened?.find((a) => a.id === id)?.name || archiveName);
  // The locked banner's sentence, its first strong (direction 9's mock).
  const lockedLead = $derived(archivePageCopy.lockedBannerLead(lockedName));
  // Draining after the head's Close (APP.md §2.3): the title of Force
  // close says what still holds the archive.
  const draining = $derived(id !== "" && store.draining === id);
  const runningHere = $derived(store.runningOps.some((o) => o.archiveId === id));
  const forceTitle = $derived(runningHere ? archivePageCopy.forceCloseRunning : archivePageCopy.forceCloseServing);
  const drainNote = $derived(runningHere ? archivePageCopy.drainingRunning : archivePageCopy.drainingServing);
  // A compaction is the one operation the kill switch refuses rather than
  // cancels (CloseArchive answers archive.compacting), so Force close is
  // greyed with that reason while one runs (the review of 2026-10-18).
  const compactingHere = $derived(id !== "" && compacting({ id, forgottenAt: 0, state: stat?.state }, store.runningOps));
  // While it drains, every page-facing call answers archive.not_open, so
  // the toolbar's commands are greyed with why rather than each press
  // ending in an InfoBar (the review of 2026-10-18).
  const closingWhy = $derived(draining ? archivePageCopy.closing : undefined);
  // The archive.needs_reopen bar's button (APP.md §2.3, ruled 2026-10-19:
  // one meaning for Close): the head's graceful *Close*, and, once that
  // leave has left the archive draining, the head's *Force close*.
  const reopenClose = $derived(closeButton("bar", id ? [id] : [], (x) => store.leftHeld(x)));

  // The selection is the store's (lib/selection.ts): ticks are the
  // selection and the selection is the ticks.
  const selected = $derived(store.sel.ids);
  const allTicked = $derived(headerTicked(store.sel, total));
  const one = $derived(selected.size === 1 ? rows.find((r) => r.id === [...selected][0]) ?? null : null);
  // The selection may hold folders as well as files (APP.md §6): a folder
  // is extracted, deleted and renamed like anything else. Only the rows
  // loaded can be named here; an action on the selection sends the ids.
  const chosen = $derived(rows.filter((r) => selected.has(r.id)));
  const chosenIds = $derived([...selected]);

  let previewUrl = $state("");
  let previewText = $state<{ text: string; truncated: boolean } | null>(null);
  let kind = $state<PreviewKind>("none");
  let previewFailed = $state(false);
  // previewClosing: a file selected while the archive drains, which the
  // pane did not ask for — its kind stays "none", so the pane draws the
  // icon and a line, and the line says the archive is closing.
  let previewClosing = $state(false);

  let renaming = $state<FileRow | null>(null);
  let renameTo = $state("");
  let renameValid = $state(true);
  let renameAttempt = $state(0);
  let creating = $state(false);
  let newFolder = $state("");
  let newFolderValid = $state(true);
  let newFolderAttempt = $state(0);
  // The names this folder already holds, so *Create folder* cannot make a
  // second row of one name. Every row on the page is a committed record
  // now, so every one of them holds its name (FORMAT.md R39).
  const taken = $derived(rows.map((r) => r.name));
  const judgeFolder = $derived((v: string) => newNameProblem(v, taken));
  // *Rename* is judged by the same rule, against every name here but the
  // row's own — the core leaves the record out of its siblings, so a
  // change of case alone is a rename (APP.md §6, Forms).
  // The name the row already has is never judged: R20 lets a stored name
  // begin with a space, which the trimmed judgement would otherwise lose.
  const judgeRename = $derived((v: string) =>
    v === renaming?.name ? "" : newNameProblem(v, rows.filter((r) => r.id !== renaming?.id).map((r) => r.name)),
  );
  // The delete question: the ids it is about and their kinds, counted
  // from the whole selection (askDelete).
  let deleting = $state<{ ids: string[]; rows: Kinded[] } | null>(null);
  // The extract dialog (direction 7): the selection as it stood when the
  // dialog opened — its ids, and its count as the pane counts it — for the
  // scope line to offer; *Everything* sends the root id instead.
  let extracting = $state<{ ids: string[]; count: number } | null>(null);
  // The conflicts an extract with the `ask` policy came back with (APP.md
  // §3): the question is the store's, derived from the finished op itself
  // — its policy, its destination, its outcomes — so nothing here has to
  // remember what was asked, and the question is still there after the
  // lock scene remounts this page or when the op finished before Extract
  // even returned. The rows come straight off the outcomes: each carries
  // the record's id and the archive copy's size and date, so nothing is
  // walked. Only which of the two dialogs is up is this page's.
  const question = $derived(store.conflictQuestion);
  const conflicts = $derived(question ? conflictsOf(question.results) : []);
  let comparing = $state(false);
  // The question's three command rows (direction 7; Explorer's "Replace or
  // Skip Files"), and what the extract wrote besides the conflicts, so
  // Skip's consequence speaks of a rest only when there is one and all of
  // it was extracted (lib/conflicts.ts restExtracted).
  const extractedBeside = $derived(restExtracted(question?.results));
  const conflictRows: CommandRow[] = $derived([
    { label: conflictCopy.replace(conflicts.length), says: conflictCopy.replaceSays(conflicts.length), run: () => decide("replace") },
    { label: conflictCopy.skip(conflicts.length), says: conflictCopy.skipSays(conflicts.length, extractedBeside), run: () => decide("skip") },
    { label: conflictCopy.decide(conflicts.length), says: conflictCopy.decideSays(conflicts.length), run: () => (comparing = true) },
  ]);
  // The names the destination refused (APP.md §3, ruled 2026-09-10): the
  // same shape, asked after the conflict question of the same op.
  const refused = $derived(store.refusedQuestion);
  const refusedRows = $derived(refused ? refusedOf(refused.results) : []);
  // The add collisions (APP.md §3): `offered` is how many names the
  // batch held, so Skip's consequence knows whether anything is added.
  let collisions = $state<{ at: string; plan: AddPlan; list: Collision[]; offered: number } | null>(null);
  // The folder the batch is going into, by name — the folder shown, or the
  // folder row a drop landed on — and what the dialog says (lib/conflicts.ts
  // collisionPlan): a folder meeting a folder is a merge, not a clash, so
  // the rows speak of the clashes alone and a note says the merge.
  const collisionPlace = $derived(collisions ? (collisions.at === dirId ? folderName : rows.find((r) => r.id === collisions?.at)?.name ?? folderName) : "");
  const collisionSaid = $derived(collisions ? collisionPlan(collisions.list, collisions.offered, collisionPlace) : null);
  const collisionRows: CommandRow[] = $derived.by(() => {
    const c = collisions;
    const p = collisionSaid;
    if (!c || !p) return [];
    return [
      {
        label: p.replace.label,
        says: p.replace.says,
        disabled: p.replace.off !== undefined,
        reason: p.replace.off,
        run: () => void addWith(c.at, c.plan, "replace"),
      },
      { label: p.keepBoth.label, says: p.keepBoth.says, run: () => void addWith(c.at, c.plan, "keep-both") },
      { label: p.skip.label, says: p.skip.says, run: () => void addWith(c.at, c.plan, "skip") },
    ];
  });
  let dropping = $state(false);

  function fail(e: unknown) {
    store.notice(codeText(errorOf(e).code), "error");
  }

  const rowIcon = (r: FileRow) => fileIcon(r.name, r.isDir);

  // The row's own click (lib/selection.ts): plain selects one and sets the
  // anchor, Ctrl toggles, Shift ranges from the anchor. A click whose
  // press already selected the row, or became a drag, is spent: the press
  // ran the same rules once, and a second run would toggle the row back.
  function click(e: MouseEvent, r: FileRow) {
    const p = press;
    press = null;
    if (p && p.id === r.id && (p.selected || p.dragged)) return;
    moveError = null;
    store.selectClick(r.id, { ctrl: e.ctrlKey || e.metaKey, shift: e.shiftKey });
  }

  // The one gesture (APP.md §3, §6, ruled 2026-09-11): rows are not
  // HTML5-draggable — a page's own drag cannot become the native one, and
  // two drags cannot run at once — so a press on a row, then movement past
  // the threshold, calls Shell.DragOut with the selection and the native
  // drag runs from there, serving the list's own moves (a self-drop) and
  // the drag out alike. A press on an unselected row first selects it by
  // the click's own rules, as a file manager does, so the drag takes that
  // row with the modifiers' meaning; a press on a selected row takes the
  // selection as it stands. The checkbox is the checkbox's, and only the
  // primary button presses.
  let press: { id: string; x: number; y: number; selected: boolean; dragged: boolean } | null = null;

  function pointerdown(e: PointerEvent, r: FileRow) {
    if (e.button !== 0 || (e.target as HTMLElement).closest("input")) return;
    let selectedNow = false;
    if (!selected.has(r.id)) {
      moveError = null;
      store.selectClick(r.id, { ctrl: e.ctrlKey || e.metaKey, shift: e.shiftKey });
      selectedNow = true;
    }
    press = { id: r.id, x: e.clientX, y: e.clientY, selected: selectedNow, dragged: false };
    window.addEventListener("pointermove", pressMove);
    window.addEventListener("pointerup", pressEnd);
    window.addEventListener("pointercancel", pressEnd);
  }

  function pressMove(e: PointerEvent) {
    const p = press;
    if (!p || !pastThreshold(e.clientX - p.x, e.clientY - p.y)) return;
    pressEnd();
    p.dragged = true;
    // The native drag takes the mouse from here; a second gesture while
    // this one is in flight is ignored by the store.
    void store.beginDragOut([...store.sel.ids]);
  }

  function pressEnd() {
    window.removeEventListener("pointermove", pressMove);
    window.removeEventListener("pointerup", pressEnd);
    window.removeEventListener("pointercancel", pressEnd);
  }

  // A row's checkbox toggles that row alone and makes it the anchor; the
  // row click's own rules do not run (APP.md §6).
  function tick1(e: Event, r: FileRow) {
    e.stopPropagation();
    moveError = null;
    store.selectToggle(r.id);
  }

  // The header's checkbox ticks the whole folder — every id Children
  // answers, loaded or not — and clears it when it is ticked.
  function tickAll(e: Event) {
    e.stopPropagation();
    if (allTicked) store.clearSelection();
    else void store.selectFolder();
  }

  // ---- the keys (lib/listkeys.ts; direction 7, APP.md §6) --------------
  // Every key of the list and of the page is taken here, in one handler,
  // through the one keymap. Arrows move the selection, Explorer's way: the
  // row arrived at becomes the one selected row and the anchor, the focus
  // moves with it and the pane follows (reversing 2026-09-10's "Space
  // selects, arrows move the focus"). The list's own keys are taken while
  // the focus is in the list or on the page's body; the page's commands
  // from anywhere on it but a text field. Nothing is taken while a dialog
  // is up — the Dialog stops the key itself (lib/dialogs.ts), and the
  // keymap says so too — nor from an open menu, whose keys are its own.
  // The focus ring is :focus-visible's, so it is drawn once the keyboard
  // is used and not for a click.
  let listEl = $state<HTMLDivElement | undefined>();
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
        list: "files",
        onUp: !!t?.closest("tr.up"),
        inField: !!t && isTextEntry(t.tagName, t.getAttribute("type") ?? "", t.isContentEditable),
        dialogUp: dialogIsUp(),
        // The viewer takes its own keys (Viewer.svelte); the page behind
        // takes none while it is up, as under a dialog.
        viewerUp,
        typing: typed !== "" && now - typedAt <= TYPEAHEAD_MS,
        // A sort button, the tick-all box (and on the Archives page the
        // empty vault's buttons) sit in the list's region but keep their
        // own Enter and Space.
        onControl: controlKeepsKeys(t),
      },
    );
    // Ctrl+L is the foot's (LayerFoot): Lock now on every page.
    if (i.kind === "none" || i.kind === "lock") return;
    const inList = !t || t === document.body || !!listEl?.contains(t);
    if (listScoped(i) && !inList) return;
    // Every key the map names is spent here, F5's reload and Ctrl+E's
    // browser search among them, whether or not there is anything to do.
    e.preventDefault();
    act(i, focusIndex(t), now);
  }

  // focusIndex: the row the keys start from — the focused row, `..` as -1
  // — else, with the focus on the page's body, the anchor; else none.
  function focusIndex(t: HTMLElement | null): number | null {
    const tr = t && listEl?.contains(t) ? t.closest("tr") : null;
    if (tr?.classList.contains("up")) return -1;
    const id = tr?.getAttribute("data-id") ?? store.sel.anchor;
    if (!id) return null;
    const at = rows.findIndex((r) => r.id === id);
    return at >= 0 ? at : null;
  }

  function act(i: Intent, from: number | null, now: number) {
    const hasUp = !!parentId;
    const atRow = from !== null && from >= 0 ? rows[from] : null;
    switch (i.kind) {
      case "move":
      case "extend":
      case "focusOnly":
        land(target(from, i.by, rows.length, pageSize(), hasUp), i.kind);
        break;
      case "select":
        if (atRow) store.selectClick(atRow.id);
        break;
      case "toggle":
        if (atRow) store.selectToggle(atRow.id);
        break;
      case "selectAll":
        void store.selectFolder();
        break;
      case "clear":
        store.clearSelection();
        moveError = null;
        break;
      case "open": {
        // Enter opens a folder, and a file in the viewer (openFile) —
        // never Extract, which is always a named command (ruled
        // 2026-10-17). Space keeps selecting, as in Explorer.
        const r = atRow ?? one;
        if (!r) break;
        store.selectClick(r.id);
        openItem(r);
        break;
      }
      case "menu":
        keyMenu(from);
        break;
      case "moveTo":
        if (selected.size > 0) askMoveTo(chosenIds);
        break;
      case "up":
        if (parentId) nav(() => store.goUp());
        break;
      case "back":
        // Back to the Archives list; the archive stays open, as it does
        // for the rail (APP.md §2.3, ruled 2026-10-18).
        store.go("archives");
        break;
      case "extract":
        extractCommand();
        break;
      case "delete":
        if (selected.size > 0) void askDelete();
        break;
      case "rename":
        if (one) askRename(one);
        break;
      case "newFolder":
        askFolder();
        break;
      case "refresh":
        void store.refreshArchive();
        break;
      case "previewPane":
        void store.setPreviewPane(!store.previewPane);
        break;
      case "paste":
        // Not while the archive drains after its Close: every add would
        // answer archive.not_open, and the page already says it is closing.
        if (!draining) void paste(readClip());
        break;
      case "typeahead": {
        typed = typeahead(typed, i.char, now, typedAt);
        typedAt = now;
        const to = jump(rows.map((r) => r.name), typed, atRow ? from : null);
        if (to >= 0) land(to, "move");
        break;
      }
    }
  }

  // land: a step arrived at row `to` (-1 is `..`). A move selects that row
  // alone and makes it the anchor; an extend ranges from the anchor to it
  // (lib/selection.ts, Shift's own rule); a focus-only move selects
  // nothing. `..` is never selected: a move onto it leaves nothing
  // selected, since the selection would otherwise sit on a row the arrows
  // have left.
  function land(to: number | null, how: "move" | "extend" | "focusOnly") {
    if (to === null) return;
    const id = to >= 0 ? rows[to].id : null;
    if (id !== null && how === "move") store.selectClick(id);
    else if (id !== null && how === "extend") store.selectClick(id, { shift: true });
    else if (id === null && how === "move") store.clearSelection();
    moveError = null;
    void tick().then(() => {
      const el = id === null ? listEl?.querySelector<HTMLElement>("tr.up") : listEl?.querySelector<HTMLElement>(`tr[data-id="${CSS.escape(id)}"]`);
      if (!el) return;
      el.focus({ preventScroll: true });
      reveal(el);
    });
  }

  // reveal brings a row into view clear of the sticky header row — the
  // nearest way, so a row already in view does not move.
  function reveal(el: HTMLElement) {
    const box = listEl;
    if (!box) return;
    const head = box.querySelector("thead")?.getBoundingClientRect().height ?? 0;
    const r = el.getBoundingClientRect();
    const b = box.getBoundingClientRect();
    if (r.top < b.top + head) box.scrollTop -= b.top + head - r.top;
    else if (r.bottom > b.bottom) box.scrollTop += r.bottom - b.bottom;
  }

  // PgUp and PgDn move by the rows that fit (lib/listkeys.ts pageRows).
  function pageSize(): number {
    const box = listEl;
    if (!box) return 1;
    return pageRows(box.clientHeight, box.querySelector("thead")?.getBoundingClientRect().height ?? 0, ROW_HEIGHT);
  }

  // One navigation at a time: a second double-click, or Enter pressed
  // again, while a folder is still being entered is ignored rather than
  // queued behind it (direction 7; the pending stage adds the ring).
  let navigating = false;
  function nav(go: () => Promise<void>) {
    if (navigating) return;
    navigating = true;
    void go().finally(() => {
      navigating = false;
    });
  }

  // A header cell sorts by its column (lib/sort.ts); the selection is ids
  // and stays.
  function sortBy(key: SortKey) {
    void store.setSort(clickSortHeader(sort, key));
  }

  // One sort glyph on both lists (APP.md §6, ruled 2026-10-17): the 10 px
  // chevron, shown on the sorting column and turned for ascending; the
  // other columns keep theirs hidden, so a header's words never move.
  const ascending = (key: SortKey) => arrowOf(sort, key) === "asc";

  // The list's blank area: everything in the pane that is neither a row
  // nor the header — a header is a control of the list, not its blank
  // area, and sorting from one must not drop the selection. The row's own
  // click has already run by the time this one does.
  function blank(e: MouseEvent) {
    const t = e.target as HTMLElement;
    if (!t.closest("tbody tr") && !t.closest("thead")) {
      store.clearSelection();
      // The one gesture that means "I am done with that": a refusal
      // standing against a target nothing is aimed at goes with it.
      moveError = null;
    }
  }

  // Entering a folder is its id, never its name (APP.md §3); the store
  // clears the selection with it.
  function open(r: FileRow) {
    if (!r.isDir) return;
    nav(() => store.enterDir(r.id, r.name));
  }

  // Open, a row's default — Enter, a double-click, the menu's bold first
  // item (direction 7): a folder is entered, a file is looked at.
  function openItem(r: FileRow) {
    if (r.isDir) open(r);
    else openFile(r);
  }

  // A file's Open is the viewer's (direction 7; Viewer.svelte, lib/viewer.ts):
  // the file large, inside the window, over the layer, read from the
  // loopback URL with no-store as the pane reads it — nothing decrypted
  // written to disk. The row opened is the one selected, and the selection
  // follows the viewer as it steps, so the row shown is the selected row;
  // closing it puts the focus back on that row. Never while the archive
  // drains: every read would answer archive.not_open (the menu's Open is
  // greyed then too).
  let viewing = $state<{ id: string; pinned: string } | null>(null);
  const viewerUp = $derived(viewing !== null);

  function openFile(r: FileRow) {
    store.selectClick(r.id);
    if (draining) return;
    moveError = null;
    viewing = { id: r.id, pinned: r.id };
  }

  function viewStep(to: string) {
    if (!viewing) return;
    store.selectClick(to);
    viewing = { ...viewing, id: to };
  }

  function closeViewer() {
    const v = viewing;
    viewing = null;
    if (!v) return;
    // The page is no longer inert once the viewer is gone: then the row
    // shown takes the focus, brought into view the nearest way.
    void tick().then(() => {
      const el = listEl?.querySelector<HTMLElement>(`tr[data-id="${CSS.escape(v.id)}"]`);
      if (!el) return;
      el.focus({ preventScroll: true });
      reveal(el);
    });
  }

  // The viewer's *Extract…* (Ctrl+E there too): the one dialog, on the file
  // shown, over the viewer.
  function viewExtract(r: FileRow) {
    extracting = { ids: [r.id], count: 1 };
  }

  // Move to… (direction 7; Ctrl+Shift+M, the row menu and More;
  // MoveToDialog.svelte, lib/movetree.ts): the selection into a folder
  // chosen in a tree, the keyboard's way to what a drag does. It moves the
  // ids as they stood when the dialog opened, as a drag moves its flight's;
  // the title names the item when it is one.
  let moving = $state<{ ids: string[]; name: string } | null>(null);
  function askMoveTo(ids: string[]) {
    if (draining || ids.length === 0) return;
    const name = ids.length === 1 ? (rows.find((r) => r.id === ids[0])?.name ?? "") : "";
    moving = { ids: [...ids], name };
  }

  // Moved: as a drag's Move ends (moveTo below) — the selection goes with
  // the items, and the folder shown is read again.
  async function movedTo() {
    moving = null;
    moveError = null;
    store.clearSelection();
    await store.refreshArchive();
  }

  // Cancelled: nothing moved, and the focus goes back to the row the keys
  // start from, the selection's anchor, as a menu's Escape puts it back.
  function cancelMoveTo() {
    moving = null;
    const at = store.sel.anchor;
    if (!at) return;
    void tick().then(() => listEl?.querySelector<HTMLElement>(`tr[data-id="${CSS.escape(at)}"]`)?.focus({ preventScroll: true }));
  }

  // ---- the row menus (direction 7, Explorer's habits; lib/filemenu.ts) ----
  // A right-click on a row opens its menu, selecting the row first when it
  // is not selected and keeping the selection when it is (Explorer's rule;
  // the keymap's click rule, store.selectClick). Extract…, Move to… and
  // Delete… act on the whole selection; Rename… and Copy name on the row
  // the menu is about. The list's blank area has a menu of its own —
  // Paste, Create folder…, Add files…, Add a folder… — and the `..` row
  // none.
  // Shift+F10 and the Menu key open the focused row's menu under it
  // (lib/menu.ts keyAnchor), or the blank area's with no row to act on;
  // Escape closes it and the focus goes back to the row. The menu is the
  // app's (ContextMenu): 2026-09-06 turned WebView2's own off, and an
  // app-drawn menu is not that one.
  let ctx = $state<{ x: number; y: number; row: FileRow | null; key: boolean } | null>(null);
  // When a menu was last opened from the keyboard: the WebView's own
  // contextmenu event that follows the key is its echo (KEY_ECHO_MS).
  let keyMenuAt = -Infinity;

  const ctxItems: MenuItem[] = $derived.by(() => {
    const c = ctx;
    if (!c) return [];
    const r = c.row;
    if (r) return fileRowItems(r.isDir, draining).map((i) => ({ ...i, run: () => runRowCmd(i.cmd, r) }));
    return fileBlankItems(draining, clip).map((i) => ({ ...i, run: () => runBlankCmd(i.cmd) }));
  });

  function runRowCmd(cmd: FileCmd, r: FileRow) {
    switch (cmd) {
      case "open":
        openItem(r);
        break;
      case "extract":
        extractCommand();
        break;
      case "rename":
        askRename(r);
        break;
      case "moveTo":
        askMoveTo(chosenIds);
        break;
      case "delete":
        void askDelete();
        break;
      case "copyName":
        // The runtime's clipboard, as the details modal's Copy: the
        // WebView's is not dependable under the custom scheme.
        void Clipboard.SetText(r.name).catch(() => {});
        break;
    }
  }

  function runBlankCmd(cmd: FileBlankCmd) {
    switch (cmd) {
      case "paste":
        // The answer the menu asked for as it opened, awaited if still on
        // its way; a read that failed is asked again, the press being the
        // user's own ask.
        void paste(clip === "unknown" || !clipRead ? readClip() : clipRead);
        break;
      case "createFolder":
        askFolder();
        break;
      case "addFiles":
        void pickFiles();
        break;
      case "addFolder":
        void pickFolder();
        break;
    }
  }

  const echo = () => performance.now() - keyMenuAt < KEY_ECHO_MS;

  function rowMenu(e: MouseEvent, r: FileRow) {
    e.preventDefault();
    e.stopPropagation();
    if (echo()) return;
    if (!selected.has(r.id)) {
      moveError = null;
      store.selectClick(r.id);
    }
    (e.currentTarget as HTMLElement).focus({ preventScroll: true });
    ctx = { x: e.clientX, y: e.clientY, row: r, key: false };
  }

  // The blank area: as a click there does, it clears the selection, then
  // offers what makes something here. A header cell is not blank.
  function blankMenu(e: MouseEvent) {
    e.preventDefault();
    const t = e.target as HTMLElement;
    if (echo() || t.closest("thead") || t.closest("tbody tr")) return;
    store.clearSelection();
    moveError = null;
    void readClip();
    ctx = { x: e.clientX, y: e.clientY, row: null, key: false };
  }

  // keyMenu: Shift+F10 or the Menu key on the row the keys start from
  // (focusIndex) — selected first, as a right-click selects it — or, with
  // none, the blank area's menu at the list's top left, under its header.
  function keyMenu(from: number | null) {
    if (from === -1) return; // `..` has no menu
    const r = from !== null ? rows[from] : null;
    keyMenuAt = performance.now();
    if (!r) {
      const box = listEl?.getBoundingClientRect();
      if (!box) return;
      const head = listEl?.querySelector("thead")?.getBoundingClientRect().height ?? 0;
      void readClip();
      ctx = { ...keyAnchor({ left: box.left, bottom: box.top + head }), row: null, key: true };
      return;
    }
    if (!selected.has(r.id)) store.selectClick(r.id);
    const el = listEl?.querySelector<HTMLElement>(`tr[data-id="${CSS.escape(r.id)}"]`);
    if (!el) return;
    el.focus({ preventScroll: true });
    // reveal scrolls now and the rect below is read after it, so a row
    // scrolled out of sight is brought back and the menu stands under it;
    // the scroll event the browser sends next frame is the opening's, and
    // ContextMenu does not close on it.
    reveal(el);
    ctx = { ...keyAnchor(el.getBoundingClientRect()), row: r, key: true };
  }

  // A key or a command puts the focus back on the row the menu was about;
  // a press elsewhere leaves it where that press put it.
  function closeCtx(how: MenuClose) {
    const c = ctx;
    ctx = null;
    if (how === "pointer" || !c?.row) return;
    listEl?.querySelector<HTMLElement>(`tr[data-id="${CSS.escape(c.row.id)}"]`)?.focus({ preventScroll: true });
  }

  // A long folder pages in as the list scrolls (APP.md §3, lib/paging.ts):
  // near the end, the next page is asked for — and again when a page did
  // not reach far enough to make the list scroll at all.
  const ROW_HEIGHT = 36;
  function scrolled() {
    const el = listEl;
    if (!el) return;
    if (nearEnd(el.scrollTop, el.clientHeight, el.scrollHeight, ROW_HEIGHT)) void store.loadMore();
  }

  $effect(() => {
    void rows.length;
    const el = listEl;
    if (!el || !hasMore(store.page)) return;
    void tick().then(() => {
      if (el.scrollHeight <= el.clientHeight) void store.loadMore();
    });
  });

  // After going up, the row of the folder just left is selected and takes
  // the focus (APP.md §6), as Explorer does — the focus and the selection
  // stay together (direction 7). The store names it once the listing lands.
  $effect(() => {
    const want = store.focusAfter;
    if (!want || !rows.some((r) => r.id === want)) return;
    void tick().then(() => {
      const el = listEl?.querySelector<HTMLElement>(`tr[data-id="${want}"]`);
      if (el && store.focusAfter === want) {
        // Only into an empty selection: in a long parent the row may land
        // a page later, after the user has chosen something else.
        if (store.sel.ids.size === 0) store.selectClick(want);
        el.focus();
        store.focusAfter = null;
      }
    });
  });

  // The preview follows the single selection. Every row is committed - an
  // operation commits at its end (APP.md §2.3) - so nothing here waits
  // for a save.
  //
  // While the archive drains after its Close (APP.md §2.3) every read
  // answers archive.not_open, which would stand as an error bar: the pane
  // asks for nothing then. The row it already previews keeps its preview —
  // one still being served is what the drain waits on — and a file
  // selected meanwhile says the archive is closing. previewOf is the row
  // the pane's preview was set for.
  let previewOf = "";
  $effect(() => {
    const r = one;
    if (draining && !viewerUp && r && r.id === previewOf) return;
    previewOf = "";
    previewUrl = "";
    previewText = null;
    previewFailed = false;
    previewClosing = false;
    kind = "none";
    // While the viewer is up it reads the file itself, and the pane behind
    // it asks for nothing: one body served at a time, read again when the
    // viewer closes.
    if (viewerUp) return;
    if (!r || r.isDir) return;
    previewOf = r.id;
    const k = previewKind(r.name);
    if (draining && k !== "none") {
      previewClosing = true;
      return;
    }
    kind = k;
    const failed = (e: unknown) => {
      if (one === r) previewFailed = true;
      fail(e);
    };
    if (k === "text") {
      Archive.PreviewText(id, r.id, PREVIEW_TEXT_CAP).then((t) => { if (one === r) previewText = t; }).catch(failed);
    } else if (k !== "none") {
      Archive.PreviewURL(id, r.id).then((u) => { if (one === r) previewUrl = u; }).catch(failed);
    }
  });

  // The kind comes from the name at once (format.ts previewKind); what
  // waits is the URL or the text. While it does, the art box keeps its
  // size and shows nothing — "No preview for this type" is said only of a
  // kind the pane cannot show, never of one still on its way — and a 14 px
  // ring appears only once the wait passes 300 ms, so a quick preview
  // never flashes it (the polish review's direction 4, ruled 2026-10-14).
  // A call that failed stops the wait; its InfoBar says why.
  const previewLoading = $derived(!!one && !one.isDir && kind !== "none" && !previewUrl && !previewText && !previewFailed);

  // A change of folder or of archive leaves no refusal standing against a
  // target that is no longer on screen. The selection is the store's and
  // is cleared there.
  $effect(() => {
    void id;
    void dirId;
    moveError = null;
  });

  // Adding (APP.md §3): files go to AddFiles and each dropped or picked
  // directory to AddFolder, both under the id of the folder shown. The
  // page asks CheckNames once for the whole batch, offering a directory
  // by a trailing "/" so the collision carries the kind on both sides.
  // (AddPlan: lib/paste.ts, which a drop and a paste both fill.)

  function base(p: string): string {
    const parts = p.split(/[\\/]/).filter((s) => s !== "");
    return parts[parts.length - 1] ?? p;
  }

  async function addAt(at: string, plan: AddPlan) {
    if (plan.files.length === 0 && plan.dirs.length === 0) return;
    const names = [...plan.files.map(base), ...plan.dirs.map((p) => `${base(p)}/`)];
    try {
      const list = (await Archive.CheckNames(id, at, names)) ?? [];
      if (list.length > 0) {
        collisions = { at, plan, list, offered: names.length };
        return;
      }
      await addWith(at, plan, "skip");
    } catch (e) {
      fail(e);
    }
  }

  async function addWith(at: string, plan: AddPlan, policy: string) {
    collisions = null;
    try {
      if (plan.files.length > 0) await Archive.AddFiles(id, at, plan.files, policy);
      for (const d of plan.dirs) await Archive.AddFolder(id, at, d, policy);
    } catch (e) {
      fail(e);
    }
  }

  async function pickFiles() {
    const p = (await Shell.PickFiles("Add files", true)) ?? [];
    await addAt(dirId, { files: p, dirs: [] });
  }

  async function pickFolder() {
    const p = await Shell.PickFolder("Add a folder");
    if (p) await addAt(dirId, { files: [], dirs: [p] });
  }

  // One *Add* button, three ways to add (APP.md §6; direction 3): the two
  // pickers, then — under a separator, since it changes the archive's own
  // tree rather than bringing files in — a folder made here, which is a
  // record from the moment it is made, its key at its right as menus show
  // an accelerator. Each opens a picker or a dialog, so each ends with an
  // ellipsis, as every command on this page that asks something first does.
  const addItems: MenuItem[] = $derived([
    { label: commandCopy.addFiles, icon: "i-file", run: () => void pickFiles() },
    { label: commandCopy.addFolder, icon: "i-folder", run: () => void pickFolder() },
    { label: commandCopy.createFolder, icon: "i-folder-add", keys: "Ctrl+Shift+N", sep: true, run: askFolder },
  ]);

  // More (direction 3): *Delete archive…* left the toolbar, where it stood
  // beside the file-level *Delete…*, for this menu — in the danger ink, last;
  // the Archives list's row menu offers it too (direction 7). Greyed
  // as it was: locked (the registry write needs the session) or Tampered
  // (APP.md §13, R25: the flow closes the archive before the write is even
  // tried, so a refusal at the end would have shut it for nothing); and
  // while the archive drains after its *Close*, with the toolbar's closing
  // title (ruled 2026-10-19), since the head's *Force close* is the way.
  // *Move to…* stands first in it (direction 7), the selection's, with its
  // key; greyed while nothing is selected — with no reason under it, since
  // the list itself shows nothing picked and the line read as clutter in
  // the real window (ruled 2026-10-27) — and while the archive drains,
  // with the closing title, which the list does not show.
  const moreItems: MenuItem[] = $derived([
    {
      label: commandCopy.moveTo,
      icon: "i-move",
      keys: keyNames.moveTo,
      disabled: draining || selected.size === 0,
      title: closingWhy,
      run: () => askMoveTo(chosenIds),
    },
    {
      sep: true,
      label: commandCopy.deleteArchive,
      icon: "i-trash",
      danger: true,
      disabled: draining || !store.unlocked || tampered,
      title: closingWhy ?? (!store.unlocked ? archivePageCopy.deleteNeedsUnlock : tampered ? archivePageCopy.deleteTampered : undefined),
      run: () => void askDeleteArchive(),
    },
  ]);

  // View (direction 3): *Preview pane*, ticked while shown, Alt+P as in
  // Explorer. Hiding it gives the list the whole width. The choice is
  // Settings.previewPane, applied the moment it is made (store.setPreviewPane).
  const paneOn = $derived(store.previewPane);
  const viewItems: MenuItem[] = $derived([
    { label: archivePageCopy.previewPane, keys: "Alt+P", checked: paneOn, run: () => void store.setPreviewPane(!paneOn) },
  ]);

  // New archive… ends here, on the new archive's empty root with the Add
  // button focused (APP.md §13): the store names the archive it was asked
  // for, and this page takes the ask once as it mounts. Phase 0's "with
  // the Add menu ready" (ruled 2026-10-14) is read as the focused button,
  // not an open menu (ruled 2026-10-26, the user's: the menu popped open
  // without a click). Enter, Space or the Down arrow opens it on its first
  // item (lib/menu.ts buttonKey), and nothing stands over the page until
  // the user asks.
  let cmdbar = $state<HTMLDivElement | undefined>();
  $effect(() => {
    const bar = cmdbar;
    if (!bar || !untrack(() => store.takeAddAsked(id))) return;
    bar.querySelector<HTMLButtonElement>("#add")?.focus();
  });

  function askFolder() {
    newFolder = "";
    newFolderValid = true;
    newFolderAttempt = 0;
    creating = true;
  }

  // CreateFolder commits at once and returns the new record's id: the
  // folder is immediately a real parent - files may be dropped into it and
  // records moved into it - and an empty folder is a record of its own
  // (FORMAT.md R39).
  async function makeFolder() {
    newFolderAttempt++;
    if (!newFolderValid) return;
    const name = newFolder.trim();
    creating = false;
    try {
      store.markFresh([await Archive.CreateFolder(id, dirId, name)]);
      await store.refreshArchive();
    } catch (e) {
      fail(e);
    }
  }

  // A drop from outside the window: the shell says which archive and which
  // directory id the target was showing, and whether each path is a
  // directory — the page cannot stat one, and a directory handed to
  // AddFiles is one failed outcome (APP.md §3).
  $effect(() => {
    const d = store.drop;
    if (!d) return;
    store.drop = null;
    if (d.archiveId !== id) {
      store.notice(noticeCopy.dropOtherArchive, "error");
      return;
    }
    // The target is an id or it is nothing: a drop the page cannot vouch
    // for is refused, never resolved to some folder. Retargeting a drop at
    // the root would write the files somewhere the user did not aim at,
    // and the core cannot refuse it — the root always resolves (§3).
    if (!d.dirId) {
      store.notice(noticeCopy.dropOffList, "error");
      return;
    }
    void addAt(d.dirId, planOf(d.paths, d.isDir ?? []));
  });

  // ---- Paste (ruled 2026-10-26, the user's; APP.md §6; lib/paste.ts) ----
  // Ctrl+V on the file list and *Paste* first in its blank-area menu add
  // the files Explorer left on the clipboard to the folder shown, through
  // the add a drop takes (addAt: CheckNames, the collisions dialog,
  // AddFiles and AddFolder, the operation on the foot, the done line). The
  // list is read by the core (Shell.ClipboardFiles, APP.md §3): the
  // WebView's clipboard holds no files. A cut is added as copied — nothing
  // is deleted from the disk — and the menu says so under the item. While
  // the vault is locked a paste still runs: an add commits the archive and
  // owes its receipt (APP.md §2.3), as a drop's does.
  //
  // clip is what the blank-area menu shows, refreshed on each menu open
  // and on each Ctrl+V: one call per ask, the item enabled while it is on
  // its way and a press waiting for its answer (clipRead). A read that
  // failed leaves the item enabled; a press asks again and says why it
  // failed rather than greying the item on a clipboard another program
  // held for a moment.
  type ClipRead = { ok: true; c: ClipboardView } | { ok: false; e: unknown };
  let clip = $state<Clip>("pending");
  let clipRead: Promise<ClipRead> | null = null;
  let clipSeq = 0;

  function readClip(): Promise<ClipRead> {
    const seq = ++clipSeq;
    clip = "pending";
    const p = Shell.ClipboardFiles().then(
      (c): ClipRead => ({ ok: true, c }),
      (e: unknown): ClipRead => ({ ok: false, e }),
    );
    clipRead = p;
    // Only the latest ask paints the menu: an older answer arriving late
    // does not overwrite a newer one.
    void p.then((r) => {
      if (seq === clipSeq) clip = clipOf(r.ok ? r.c : null);
    });
    return p;
  }

  // paste runs the add the clipboard names, into the folder shown at the
  // press — not one navigated to while the read was on its way.
  async function paste(read: Promise<ClipRead>) {
    const arch = id;
    const at = dirId;
    const r = await read;
    if (arch !== id || draining) return;
    if (!r.ok) {
      fail(r.e);
      return;
    }
    const plan = pastePlan(r.c);
    if (!plan) {
      store.notice(noticeCopy.nothingToPaste, "info");
      return;
    }
    await addAt(at, plan);
  }

  // A drag of the selection onto a folder row, onto `..` (up one level) or
  // onto an ancestor's crumb in the head (direction 3) is a Move (APP.md §6): the native drag
  // of the one gesture released over Enfold's own window — a self-drop —
  // whose landing the WebView's own drop reports here while the ids are in
  // flight (store.dragOut, lib/dragout.ts). It is refused whole and in
  // place — nothing is staged when any item fails — so the reason is shown
  // against the target that was aimed at, and the selection stays where it
  // was. `at` says which surface that was: the parent's id is the root's
  // when the folder shown is one level down, and the refusal must sit
  // where the drag went. Without a flight these handlers do nothing, and
  // the drop bubbles to the runtime's own listener: real files from
  // Explorer are the add of §3, which the shell reports (store.drop). A
  // flight alone does not make a drop ours (the outside review's finding
  // 3): a self-drop answered over the window frame waits a moment for its
  // landing, and a real Explorer drop in that moment — its files' names
  // not the gesture's, lib/dragout.ts ownNames — is the add as always,
  // left to bubble, and ends the flight with nothing to do.
  let dropTarget = $state<{ id: string; at: DropAt } | null>(null);
  let moveError = $state<{ id: string; at: DropAt; text: string } | null>(null);
  const upTarget = $derived<DropTarget | null>(parentId ? { id: parentId, isDir: true } : null);

  // The names a drop carries: one File per item dropped, a folder as its
  // name.
  const dropNames = (e: DragEvent) => Array.from(e.dataTransfer?.files ?? [], (f) => f.name);

  // While a drag hovers its names are hidden from the page, but not how
  // many files it carries: one that does not carry the flight's count is
  // someone else's, and the runtime's own hover effect is left to it.
  function foreignHover(e: DragEvent, f: Flight): boolean {
    const items = e.dataTransfer?.items;
    if (!items) return false;
    let files = 0;
    for (const it of items) if (it.kind === "file") files++;
    return files !== f.ids.length;
  }

  function over(e: DragEvent, t: DropTarget, at: DropAt, name = "") {
    const f = store.dragOut;
    if (!f || foreignHover(e, f)) {
      springer.cancel();
      return;
    }
    // Ours: the runtime's document-level listener must not see it, since
    // it would set the effect to none over the heading, which is outside
    // the file-drop target, and Explorer's cursor over a file row would
    // otherwise offer a copy that means nothing.
    e.preventDefault();
    e.stopPropagation();
    const ok = canDrop(t, f);
    if (e.dataTransfer) e.dataTransfer.dropEffect = ok ? "move" : "none";
    dropTarget = ok ? { id: t.id, at } : null;
    springOver(e, t, at, f.ids, name);
  }

  // Spring-loaded folders (direction 7; lib/springload.ts): our own drag
  // resting 700 ms on a folder row enters it, and on `..` goes up, as
  // Explorer's do; the drag goes on, and the drop lands where the pointer
  // is in the folder now shown — a folder row, `..`, or the list's blank
  // area (overBlank). A drag from outside the window never springs: it
  // is an add (APP.md §3), and `over` has left it to the runtime already.
  // Each hover carries the pointer's place: once a spring fires, nothing
  // arms again until the pointer moves (lib/springload.ts), or the `..`
  // now under a still pointer would go up again, and the folder row it
  // left would be entered again, every 700 ms.
  const springer = spring();
  const pointOf = (e: DragEvent) => ({ x: e.clientX, y: e.clientY });

  function springOver(e: DragEvent, t: DropTarget, at: DropAt, carried: string[], name: string) {
    if (!springs(t, at, carried)) {
      springer.hover(null, pointOf(e), () => {});
      return;
    }
    springer.hover(springKey(t, at), pointOf(e), () => {
      if (!store.dragOut) return;
      dropTarget = null;
      if (at === "up") nav(() => store.goUp());
      else nav(() => store.enterDir(t.id, name));
    });
  }

  // The spring goes with the flight, and with the page.
  $effect(() => {
    if (!store.dragOut) springer.cancel();
  });
  $effect(() => () => springer.cancel());

  // The list's blank area under our own drag (APP.md §3). In the folder
  // the drag came from it is no target — no effect, and a release there is
  // nothing; in a folder a spring entered it is that folder — the folder
  // shown takes the move, washed as a row is. A row's handler has already
  // stopped the event by the time it would reach here.
  const hereTarget = $derived<DropTarget>({ id: dirId, isDir: true });

  function overBlank(e: DragEvent) {
    const f = store.dragOut;
    if (!f || foreignHover(e, f)) return;
    e.preventDefault();
    e.stopPropagation();
    springer.hover(null, pointOf(e), () => {});
    const ok = canDrop(hereTarget, f);
    if (e.dataTransfer) e.dataTransfer.dropEffect = ok ? "move" : "none";
    dropTarget = ok ? { id: hereTarget.id, at: "list" } : null;
  }

  // A drop on the blank area: the folder shown's, when it is a target;
  // otherwise it goes on to the window's own listener (dropElsewhere) and
  // the runtime's, as before.
  function dropBlank(e: DragEvent) {
    dropping = false;
    if (store.dragOut && isOver(hereTarget.id, "list")) dropOn(e, hereTarget, "list");
  }

  function leave(t: DropTarget, at: DropAt) {
    if (dropTarget?.id === t.id && dropTarget.at === at) dropTarget = null;
  }

  const isOver = (id: string, at: DropAt) => dropTarget?.id === id && dropTarget.at === at;
  const refusedAt = (id: string, at: DropAt) => moveError?.id === id && moveError.at === at;

  function dropOn(e: DragEvent, t: DropTarget, at: DropAt) {
    const f = store.dragOut;
    if (!f) return;
    springer.cancel();
    dropTarget = null;
    if (!ownNames(dropNames(e), f)) {
      // Someone else's files: the runtime's listener gets the drop and
      // the shell reports the add; the flight is over.
      store.dragLanded("foreign");
      return;
    }
    e.preventDefault();
    e.stopPropagation();
    store.dragLanded({ id: t.id, at, isDir: t.isDir });
  }

  // A drop that reached the window without landing on a target: elsewhere
  // on the page, which is nothing for a self-drop; the runtime's listener
  // has had it already, and what it reports (store.drop) is told apart by
  // its paths. A foreign drop ends the flight the same way, as nothing.
  function dropElsewhere(e: DragEvent) {
    springer.cancel();
    dropTarget = null;
    const f = store.dragOut;
    if (f) store.dragLanded(ownNames(dropNames(e), f) ? "elsewhere" : "foreign");
  }

  // The Move a self-drop adds up to (store.selfDrop): performed here, so
  // that a refusal is said against the target it was aimed at.
  $effect(() => {
    const m = store.selfDrop;
    if (!m) return;
    store.selfDrop = null;
    void moveTo(m);
  });

  async function moveTo(m: PendingMove) {
    try {
      await Archive.Move(id, m.ids, m.to);
      moveError = null;
      store.clearSelection();
      await store.refreshArchive();
    } catch (err) {
      moveError = { id: m.to, at: m.at, text: codeText(errorOf(err).code) };
    }
  }

  // *Rename…* opens on the row's name with its stem selected, as Explorer
  // does (lib/paths.ts renameSelection); the field is judged under the
  // form rule and the button is never disabled — pressing it, or Enter,
  // marks what would refuse (APP.md §6, Forms).
  function askRename(r: FileRow) {
    renameTo = r.name;
    renameValid = true;
    renameAttempt = 0;
    renaming = r;
  }

  async function doRename() {
    const r = renaming;
    if (!r) return;
    // An untouched field renames nothing, so nothing is sent — and a name
    // with a leading space keeps it (judgeRename).
    if (renameTo === r.name) {
      renaming = null;
      return;
    }
    renameAttempt++;
    if (!renameValid) return;
    try {
      await Archive.Rename(id, r.id, renameTo.trim());
      renaming = null;
      await store.refreshArchive();
    } catch (e) {
      fail(e);
    }
  }

  // Delete takes files and folders alike. The page asks first - naming
  // files and folders apart, saying that a folder takes everything beneath
  // it and that this cannot be undone - and the operation then commits at
  // once: there is no undo, deletion being cryptographic erasure (APP.md
  // §3, FORMAT.md R32). The question counts the whole selection (§6):
  // from the rows on hand when they hold every selected id, else from
  // Children — the header's tick and Ctrl+A select ids the list may never
  // have loaded — so what is asked about is what is deleted, and a
  // selection made wholly past the loaded page is confirmed and deleted
  // like any other (the review's finding 8). The ids are taken now, and
  // the Delete acts on the ones the question named.
  async function askDelete() {
    const want = new Set(chosenIds);
    if (want.size === 0) return;
    if (chosen.length === want.size) {
      deleting = { ids: chosen.map((r) => r.id), rows: chosen };
      return;
    }
    try {
      const kids = (await Archive.Children(id, dirId, store.sortBy)) ?? [];
      const live = kids.filter((k) => want.has(k.id));
      if (live.length === 0) return;
      deleting = { ids: live.map((k) => k.id), rows: live.map((k) => ({ isDir: k.isDir, name: "" })) };
    } catch (e) {
      fail(e);
    }
  }

  // The rows go first (direction 4, list motion): they fade out over
  // LEAVE, and only then is the Delete sent — so the archive's own change
  // event, which re-reads the list, cannot take them before they have
  // faded. A refusal brings them back with its InfoBar.
  //
  // The place is kept (the motion keeps the focus where it was): the row
  // after the last one taken — else the nearest before — is selected and
  // takes the focus as soon as the Delete is done, through the same
  // store.focusAfter the way up uses (lib/listkeys.ts survivor; Explorer's
  // habit). With no row left it is `..`, or the empty folder's first
  // command once the listing lands. The final shoot of phase 4b found the
  // focus dropped to the body and the selection empty.
  async function doDelete() {
    const d = deleting;
    deleting = null;
    if (!d) return;
    const gone = new Set(d.ids);
    const keep = survivor(rows.map((r) => r.id), gone);
    leaving = gone;
    await new Promise((r) => setTimeout(r, delay(LEAVE)));
    try {
      await Archive.Delete(id, d.ids);
      store.clearSelection();
      if (keep) store.focusAfter = keep;
      await store.refreshArchive();
      if (!keep) {
        await tick();
        const el = listEl?.querySelector<HTMLElement>("tr.up") ?? listEl?.querySelector<HTMLElement>("button, [tabindex='0']");
        el?.focus();
      }
    } catch (e) {
      leaving = new Set();
      fail(e);
    }
  }

  // One Extract… (direction 7): the toolbar's, Ctrl+E's and the pane's
  // open the same dialog, whose scope line asks what — the selection, or
  // everything in the archive — opening on the selection when there is
  // one. The destination lives in it, prefilled by the rule of §3 per
  // scope, rather than in a native picker the page opens first; it is
  // never kept from the last time (ruled 2026-09-10), and the policy is,
  // while Enfold runs (lib/extract.ts startingPolicy, ruled 2026-10-14).
  // An archive that holds nothing has nothing to extract, and the toolbar's
  // button is greyed then.
  function extractCommand() {
    if (selected.size === 0 && (stat?.records ?? 0) === 0) return;
    extracting = { ids: chosenIds, count: selected.size };
  }

  // The pane at rest offers the archive whole (direction 3): the same
  // dialog on *Everything*, its selection line greyed since nothing is
  // selected.
  function extractEverything() {
    if ((stat?.records ?? 0) === 0) return;
    extracting = { ids: [], count: 0 };
  }

  async function doExtract(scope: ExtractScope, dir: string, policy: string) {
    const x = extracting;
    extracting = null;
    if (!x) return;
    // Everything is the root id alone (APP.md §3): the page walks nothing.
    const ids = scope === "all" || x.ids.length === 0 ? [ROOT_ID] : x.ids;
    try {
      // An `ask` comes back with its question on the op itself (APP.md
      // §3): nothing is kept here against the op id.
      await Archive.Extract(id, ids, dir, policy, null);
    } catch (e) {
      fail(e);
    }
  }

  // extractWith issues one re-issue and records the names it carried
  // against the op it started (store.extractNames), so that a question
  // this op asks in turn — a conflict met under a chosen name — is
  // re-issued under the same names.
  async function extractWith(ids: string[], dir: string, policy: string, names: Record<string, string> | null) {
    const opId = await Archive.Extract(id, ids, dir, policy, names);
    store.noteExtractNames(opId, names);
  }

  // The question's answers (APP.md §3; the rows of direction 7): *Replace
  // the files* re-issues for every record with `replace`, *Skip these
  // files* re-issues nothing, and *Let me decide* opens the compare list.
  // Closing either dialog answers nothing and the question is settled: the
  // files in the way stay where they are.
  function decide(d: Decision) {
    void reissue(allOf(conflicts, d));
  }

  function dismissQuestion() {
    if (question) store.settleConflicts(question.id);
    comparing = false;
  }

  async function reissue(decisions: Record<string, Decision>) {
    const q = question;
    const rows = conflicts;
    comparing = false;
    if (!q) return;
    // The destination is the op's own (OpView.Destination): the re-issue
    // goes where the first extract went, and under the names that extract
    // was issued with — what a *Shorten* or *Rename…* chose, kept on the
    // store until the op is forgotten — for the ids re-issued, never null
    // in their place: a conflict met under the chosen name is settled
    // there, not by trying the refused name again (lib/conflicts.ts,
    // namesFor). Read before the op is settled, which forgets it.
    const dir = q.destination ?? "";
    const names = store.extractNames[q.id] ?? null;
    store.settleConflicts(q.id);
    const plan = reissuePlan(rows, decisions);
    if (planIsEmpty(plan)) return;
    try {
      if (plan.replace.length > 0) await extractWith(plan.replace, dir, "replace", namesFor(plan.replace, names));
      if (plan.rename.length > 0) await extractWith(plan.rename, dir, "rename", namesFor(plan.rename, names));
    } catch (e) {
      fail(e);
    }
  }

  // The refused names' answers (APP.md §3, lib/refused.ts): one Extract
  // for the chosen ids with `names` — the element each is written under
  // in that extract only — under the op's own policy and destination. A
  // name refused again comes back as a new question.
  async function reissueRefused(decisions: Record<string, RefusedDecision>) {
    const q = refused;
    const rows = refusedRows;
    if (!q) return;
    const dir = q.destination ?? "";
    const policy = q.policy || "replace";
    store.settleRefusals(q.id);
    const plan = refusedPlan(rows, decisions);
    if (refusedPlanIsEmpty(plan)) return;
    try {
      await extractWith(plan.ids, dir, policy, plan.names);
    } catch (e) {
      fail(e);
    }
  }

  // The kill switch (APP.md §2.3) — the head's *Force close*, shown only
  // while the archive drains — closes now, readers or not, dropping every
  // preview body in flight, where *Close* (the head's and the banners')
  // closes it only once nothing is reading it.
  // Refresh fingerprint from the copy_mismatch note: the Archives page's
  // command, run on this archive; the layer's foot shows it running.
  async function refreshFingerprint() {
    try {
      await Archives.Verify(id);
    } catch (e) {
      fail(e);
    }
  }

  async function close() {
    try {
      await Archives.Close(id);
      store.leaveArchive(true);
      await store.refreshArchives();
    } catch (e) {
      fail(e);
    }
  }

  // Delete archive… is the same act as the Archives page's, on the archive
  // this page shows, in the same dialog (APP.md §13) — asked here, over
  // this page, and the archive is left only on consent (ruled 2026-10-14,
  // refining 2026-09-07's close first): a Cancel leaves the user exactly
  // where they were, in their folder, with its sort and selection. The
  // record is read first, so the backup sentence has the created_at it is
  // chosen against; unread, the sentence says the harder thing. It is
  // disabled while the vault is locked (§2.3): the registry write needs the
  // session's key. Nothing is asked about unfinished changes — there are
  // none between operations — and a running one is cancelled when the
  // handle goes.
  let deletingArchive = $state<{ id: string; name: string; path: string; createdAt: number } | null>(null);
  // released: the consent was given and the archive let go of, so this
  // page has nothing left to show once the dialog closes.
  let released = false;

  async function askDeleteArchive() {
    const archiveId = id;
    let createdAt = 0;
    try {
      createdAt = (await Archives.Details(archiveId)).createdAt;
    } catch {
      /* unknown: the backup sentence then says the harder thing */
    }
    released = false;
    deletingArchive = { id: archiveId, name: archiveName || (stat?.name ?? ""), path: store.currentPath, createdAt };
  }

  // letGo is the dialog's release, run once the name is typed and the act
  // confirmed and never before. It is the leave the rail performs —
  // Archives.Leave, not the kill switch (§2.3, ruled 2026-09-10) — waited
  // for, with this page kept under the dialog until it closes
  // (store.releasedForDelete); then, as on the Archives page, the record's
  // own close when a preview body or an operation still holds the archive,
  // since the delete is refused while either does. It throws what the core
  // refused the close with, which ends the dialog with nothing removed.
  async function letGo(archiveId: string): Promise<void> {
    store.releasedForDelete = archiveId;
    released = true;
    await Archives.Leave(archiveId);
    await store.refreshArchives();
    if (!store.archives.find((x) => x.id === archiveId)?.open) return;
    try {
      await Archives.Close(archiveId);
    } catch (e) {
      // Closed by itself meanwhile — the last reader ended — which is
      // where this wanted it (§2.3).
      if (errorOf(e).code !== Code.CodeArchiveNotOpen) throw e;
    }
  }

  // The dialog's close: before the consent nothing was touched and the
  // page stays as it was; after it, whatever the act came to, the archive
  // is no longer open here and the page goes back to the list — the
  // store's own leave, which reads the list again. A route change leaves
  // nothing any more (APP.md §2.3, ruled 2026-10-18), so the leave is
  // asked for by name; the archive let go of already, it is a no-op in
  // the core.
  function closeDeleteArchive() {
    deletingArchive = null;
    if (released) store.leaveArchive();
  }

  // This page gone with the dialog still up — a lock scene drawn over it
  // takes both — the archive it let go of is read like any other again,
  // and the Stat's archive.not_open then leaves it (refreshArchive).
  $effect(() => () => {
    if (released) store.releasedForDelete = null;
  });

  // The foot's note (LayerFoot): where you are — "Trips · 3 items" — beside
  // the lock state (direction 3, phase 4a). The archive's figures moved to
  // the pane at rest. Nothing counts down here: an open archive has no
  // timeout of its own, locked vault or not (APP.md §2.3, ruled 2026-09-10).
  $effect(() => {
    store.footNote = folderName ? archivePageCopy.foot(folderName, total) : "";
  });

  // ---- the head's crumbs (direction 3, phase 4a; lib/crumbs.ts) ---------
  // The path from the archive's name to the folder shown. Every crumb is
  // cut at its cap (CRUMB_CAP, the root ROOT_CAP: lib/crumbs.ts, set on the
  // head as --crumb-cap and --crumb-root-cap for app.css, so the fold and
  // the drawing share one source) with the app's tooltip; when the
  // path does not fit, the folders under the root fold into the ellipsis
  // button's menu, the root and the folder shown always standing, and the
  // parents added back from the folder shown outward while they fit. The
  // crumbs are measured as drawn — an unseen copy of the row — and the fold
  // is worked out again once per resize of the head.
  let crumbsEl = $state<HTMLElement | undefined>();
  let measureEl = $state<HTMLElement | undefined>();
  let foldedCount = $state(0);
  const names = $derived(crumbs.map((c) => c.name));
  // The measured fold, clamped to the path now drawn (clampFolded): a
  // path from seven deep to one deep never draws the folder shown inside
  // the ellipsis menu for the frame before the new path is measured.
  const folds = $derived(clampFolded(foldedCount, crumbs.length));
  // What the head draws: the root, the ellipsis when anything folded, then
  // the crumbs from the first one still shown to the folder shown.
  const shownCrumbs = $derived(crumbs.map((c, i) => ({ c, i })).filter(({ i }) => i === 0 || i > folds));
  const foldedCrumbs = $derived(crumbs.slice(1, 1 + folds).map((c, k) => ({ c, i: k + 1 })));
  const foldedItems: MenuItem[] = $derived(foldedCrumbs.map(({ c, i }) => ({ label: c.name, icon: "i-folder", cut: true, run: () => goCrumb(i) })));

  function measure() {
    const head = crumbsEl;
    const m = measureEl;
    if (!head || !m) return;
    const widths = Array.from(m.querySelectorAll<HTMLElement>("[data-m='crumb']"), (e) => e.getBoundingClientRect().width);
    const sep = m.querySelector<HTMLElement>("[data-m='sep']")?.getBoundingClientRect().width;
    const more = m.querySelector<HTMLElement>("[data-m='more']")?.getBoundingClientRect().width;
    const f = fold(names, widths, head.clientWidth, { sep: sep ?? 20, more: more ?? 28 });
    foldedCount = f.folded.length;
  }

  // Once per resize: the observer's reports within a frame are one
  // measure.
  let measuring = 0;
  function measureSoon() {
    if (measuring) return;
    measuring = requestAnimationFrame(() => {
      measuring = 0;
      measure();
    });
  }

  $effect(() => {
    const head = crumbsEl;
    if (!head) return;
    const ro = new ResizeObserver(measureSoon);
    ro.observe(head);
    // The display face may land after the first measure.
    void document.fonts?.ready.then(measureSoon);
    return () => {
      ro.disconnect();
      cancelAnimationFrame(measuring);
      measuring = 0;
    };
  });

  // A new path is measured once it is drawn.
  $effect(() => {
    void names.join("/");
    void tick().then(measure);
  });

  // A crumb goes to its folder, as the `..` row goes up: one navigation at
  // a time, and the folder it came through selected and focused once its
  // row is on screen.
  function goCrumb(i: number) {
    const c = crumbs[i];
    if (!c || i >= crumbs.length - 1) return;
    moveError = null;
    nav(() => store.enterDir(c.id, c.name, crumbs[i + 1]?.id ?? null));
  }

  const crumbTarget = (c: Crumb): DropTarget => ({ id: c.id, isDir: true });

  // ---- the pane (direction 3, phase 4a; lib/archpane.ts) -----------------
  // At rest it describes where you are; with several selected it counts
  // them; with one it previews it.
  const shape = $derived(paneShape(selected.size, !!one));
  const here = $derived(folderFigures(store.page));
  const picked = $derived(selectionFigures(rows, selected));
  const method = $derived(store.archives.find((a) => a.id === id)?.method);
  const noPreview = $derived(!!one && !one.isDir && (kind === "none" || previewFailed));
</script>

<svelte:window onkeydown={onKey} ondrop={dropElsewhere} />

<!-- While the viewer is up the page under it is inert: no key, no click,
     no tab stop reaches it (the viewer is over it). -->
<div class="layer-head" style:--crumb-cap="{CRUMB_CAP}px" style:--crumb-root-cap="{ROOT_CAP}px" inert={viewerUp || undefined}>
  <!-- The head is the path (direction 3, phase 4a; the user's rulings of
       2026-10-14): the archive's name the root, the folder shown last in
       the ink, its ancestors buttons in the second ink — each goes there,
       and each is a Move drop target, a refusal said under the head — and
       the leading *Archives* crumb gone, since the rail carries the list
       and the open archive. Page's Crumbs, never Stat. -->
  <nav class="crumbs" aria-label={archivePageCopy.crumbsLabel} bind:this={crumbsEl}>
    <h1 class="t-title crumbline">
      {#each shownCrumbs as { c, i }, j (c.id)}
        {#if j > 0}<span class="crumb-sep" aria-hidden="true"><svg class="i"><use href="#i-chevron" /></svg></span>{/if}
        {#if i === crumbs.length - 1}
          <span class="crumb here" class:root={i === 0} aria-current="location" use:tip={{ text: c.name, cut: true }}><span class="ellipsis">{c.name}</span></span>
        {:else}
          <button
            type="button"
            class="crumb"
            class:root={i === 0}
            class:drop-into={isOver(c.id, "head")}
            onclick={() => goCrumb(i)}
            ondragover={(e) => over(e, crumbTarget(c), "head")}
            ondragleave={() => leave(crumbTarget(c), "head")}
            ondrop={(e) => dropOn(e, crumbTarget(c), "head")}
            use:tip={{ text: c.name, cut: true }}
          ><span class="ellipsis">{c.name}</span></button>
        {/if}
        {#if i === 0 && foldedCrumbs.length > 0}
          <!-- What folded, in path order, behind one ellipsis (ruled
               2026-10-14): each item cut at a folder's cap with its whole
               name in the app's tooltip, the menu capped in height. -->
          <span class="crumb-sep" aria-hidden="true"><svg class="i"><use href="#i-chevron" /></svg></span>
          <MenuButton id="crumbs-more" label={archivePageCopy.folded(foldedCrumbs.length)} tipText={archivePageCopy.folded(foldedCrumbs.length)} icon="i-more" iconOnly small items={foldedItems} maxRows={menuRows(foldedCrumbs.length)} />
        {/if}
      {/each}
    </h1>
    <!-- The crumbs as they would be drawn, unseen, for the fold to
         measure (lib/crumbs.ts): each at its cap, a separator and the
         ellipsis button. -->
    <span class="crumb-measure t-title" aria-hidden="true" bind:this={measureEl}>
      {#each crumbs as c, i (c.id)}<span class="crumb" class:root={i === 0} data-m="crumb"><span class="ellipsis">{c.name}</span></span>{/each}
      <span class="crumb-sep" data-m="sep"><svg class="i"><use href="#i-chevron" /></svg></span>
      <span class="btn subtle sm icon-only" data-m="more"><svg class="i i-14"><use href="#i-more" /></svg></span>
    </span>
  </nav>
  <!-- One exit (APP.md §2.3, ruled 2026-10-18): Close is the graceful
       leave the rail row's × runs, one standard button. While the archive
       drains after it — a preview still served, or its own operation
       still running — the page stands and the kill switch takes its place
       as Force close. -->
  {#if draining}
    <!-- Why the page still stands after its Close, said beside the
         button rather than only in its title (the final shoot of phase
         4a): the archive closes on its own once that work ends. -->
    <span class="drain-note" role="status">{drainNote}</span>
    <button type="button" class="btn" disabled={compactingHere} title={compactingHere ? codeText(Code.CodeArchiveCompacting) : forceTitle} onclick={close}>{commandCopy.forceClose}</button>
  {:else}
    <button type="button" class="btn" onclick={() => void store.closeArchive(id)}>{commandCopy.close}</button>
  {/if}
</div>

<div class="layer-body" inert={viewerUp || undefined}>
  {#if moveError && (moveError.at === "head" || moveError.at === "list")}
    <!-- A Move onto a crumb refused, said under the head it was aimed at —
         and one dropped on the list's blank area of a folder a spring
         entered, said here too, above the list it was aimed at. -->
    <div class="move-note">Not moved: {moveError.text}</div>
  {/if}
  <div class="cmdbar" bind:this={cmdbar}>
    <MenuButton id="add" label="Add" icon="i-plus" items={addItems} disabled={draining} title={closingWhy} />
    <!-- Greyed on Records, never on Files: a record count is the only
         thing that can say whether the tree holds anything, since a
         folder occupies no data region and an archive of folders alone
         reports 0 files while holding real records (APP.md §3). -->
    <button type="button" class="btn subtle" disabled={draining || (stat?.records ?? 0) === 0} title={closingWhy ?? ((stat?.records ?? 0) === 0 ? archivePageCopy.extractNothing : keyTitles.extract)} onclick={extractCommand}><svg class="i i-14"><use href="#i-extract" /></svg>{commandCopy.extract}</button>
    <button type="button" class="btn subtle" disabled={draining || !one} title={closingWhy ?? (one ? keyTitles.rename : archivePageCopy.renameNeedsOne)} onclick={() => { if (one) askRename(one); }}><svg class="i i-14"><use href="#i-rename" /></svg>{commandCopy.rename}</button>
    <button type="button" class="btn subtle danger" disabled={draining || selected.size === 0} title={closingWhy ?? (selected.size === 0 ? archivePageCopy.deleteNeedsSome : keyTitles.delete)} onclick={() => void askDelete()}><svg class="i i-14"><use href="#i-trash" /></svg>{commandCopy.delete}</button>
    <div class="grow"></div>
    <!-- [⋯ More] [View ▾] at the right (direction 3): the archive's own
         command, and how the page is laid out. -->
    <MenuButton id="more" label={archivePageCopy.more} title={archivePageCopy.more} icon="i-more" iconOnly end items={moreItems} />
    <MenuButton id="view" label={archivePageCopy.view} end items={viewItems} />
  </div>

  <!-- The notice slot (direction 4, phase 4b; NoticeSlot): the page's
       stateful banners between the commands and the list, in their tiers'
       order — danger, then attention — each opening and closing its
       height so the list slides; and the InfoBars over the list's top,
       stopping at its column when the preview pane stands beside it. -->
  <NoticeSlot beside={paneOn ? "files" : undefined}>
    {#if tampered}
      <!-- Danger, first (direction 9; APP.md §13): the archive keeps
           saving, but the vault file it reports to may have been changed
           outside Enfold, and this page said nothing of it. It stood above
           the commands until the slot (direction 4) gave every banner one
           place under them. -->
      <div class="fold" transition:unfold><TamperedBar reason={store.status?.tamperedReason} /></div>
    {/if}
    {#if !store.unlocked}
      <!-- One line of 40 px, everything on it centred (APP.md §7, ruled
           2026-09-11): .bar.line. -->
      <div class="fold" transition:unfold>
        <div class="bar line attention">
          <svg class="i i-14"><use href="#i-lock" /></svg>
          <!-- What the lock left (direction 9, ruled 2026-10-18): this archive
               is still decrypted and still saves (APP.md §2.3), so the banner
               is attention and carries the way out beside the unlock — the
               head's own *Close*, the graceful leave, by the same word and the
               same act (leaving is explicit and named once; the final shoot of
               phase 4a found a kill switch here under another name). While
               the archive drains after it, the head's *Force close* is the one
               way left, and the banner offers nothing a second press would not
               repeat. -->
          <span class="grow"><strong>{lockedLead.head}</strong> {lockedLead.rest}</span>
          {#if !draining}
            <button type="button" class="btn sm" onclick={() => void store.closeArchive(id)}>{commandCopy.close}</button>
          {/if}
          <button type="button" class="btn sm accent" onclick={() => store.unlock()}>{commandCopy.unlock}</button>
        </div>
      </div>
    {/if}
    {#if stat?.state === "needs_reopen"}
      <!-- The button is the head's *Close*, by the same word, since it is
           the same act — the graceful leave: it closes, and the archive is
           opened again from the list. "Close and reopen" promised a reopen
           it does not do (one word per thing, consequences first: APP.md §7,
           ruled 2026-10-17). While it drains after that leave, the same
           button is the head's *Force close* (ruled 2026-10-19: one meaning
           for Close, lib/leave.ts). -->
      <div class="fold" transition:unfold><div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span class="grow">{codeText("archive.needs_reopen")}</span>{#if !reopenClose.force}<button type="button" class="btn sm" onclick={() => void store.closeArchive(id)}>{reopenClose.label}</button>{/if}</div></div>
    {/if}
    {#if stat?.copyMismatch}
      <!-- The note names *Refresh fingerprint*, so the command stands in the
           note: the Archives page's command bar, where it otherwise lives,
           is not on this page (direction 9; APP.md §2.3 Verify). It is a
           registry write, greyed while Locked or Tampered as there. -->
      <div class="fold" transition:unfold><div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span class="grow">{codeText("archive.copy_mismatch")}</span><button type="button" class="btn sm" disabled={!store.unlocked || tampered} title={!store.unlocked ? codeText(Code.CodeVaultLocked) : tampered ? vaultCopy.changesOff : undefined} onclick={() => void refreshFingerprint()}>{commandCopy.refreshFingerprint}</button></div></div>
    {/if}
  </NoticeSlot>
  <!-- View › Preview pane off gives the list the whole width (direction 3). -->
  <div class="file-split" class:dropping class:bare={!paneOn}>
    <!-- A click on the list's blank area — under the last row, or in the
         container beside the table — clears the selection and the anchor
         (APP.md §6); a click that lands on a row is the row's. The drop
         target carries the directory id the page is showing, never a
         name: a stale id is refused, where a stale path would resolve to
         whatever folder now happens to carry that name (§3). -->
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions, a11y_click_events_have_key_events -->
    <div class="tablewrap" class:drop-into={isOver(hereTarget.id, "list")} data-file-drop-target="true" data-archive-id={id} data-dir-id={dirId}
      role="region" aria-label="Files" bind:this={listEl} onclick={blank} oncontextmenu={blankMenu} onscroll={scrolled}
      ondragenter={() => { if (!store.dragOut) dropping = true; }} ondragover={overBlank} ondragleave={() => (dropping = false)} ondrop={dropBlank}>
      {#if isEmpty && !parentId}
        {@render emptyFolder()}
      {:else}
        <!-- Four columns (APP.md §6, ruled 2026-09-10): Name, Size, Type,
             Modified, a checkbox heading every row and the header row.
             A row's box shares one slot with its type glyph, the input
             first so the CSS can hide the glyph after a shown box (ruled
             2026-10-26, app.css). Each header cell sorts by its column;
             when the pane narrows the columns give way from the right,
             and the Name cell then carries the hidden column's key beside
             its chevron (app.css). -->
        <table>
          <colgroup><col class="w-check" /><col /><col class="w-size" /><col class="w-type" /><col class="w-date" /></colgroup>
          <thead>
            <tr>
              <th scope="col" class="chk"><input type="checkbox" checked={allTicked} disabled={total === 0} aria-label={listCopy.tickAll} onclick={tickAll} /></th>
              {#each SORT_KEYS as key (key)}
                <th scope="col" class="col-{key}" class:num={key === "size"} aria-sort={sort.key === key ? (sort.desc ? "descending" : "ascending") : undefined}>
                  <button type="button" class="sorter" title={listCopy.sortBy(columnLabels[key])} onclick={() => sortBy(key)}>{columnLabels[key]}<svg class="i caret" class:on={sort.key === key} class:asc={ascending(key)} aria-hidden="true"><use href="#i-chevdown" /></svg></button>
                  {#if key === "name" && sort.key !== "name"}
                    <span class="hidden-sort k-{sort.key}">· {columnLabels[sort.key]} <svg class="i caret on" class:asc={ascending(sort.key)} aria-hidden="true"><use href="#i-chevdown" /></svg></span>
                  {/if}
                </th>
              {/each}
            </tr>
          </thead>
          <tbody bind:this={rowsEl}>
            {#if parentId && upTarget}
              <!-- `..` (APP.md §6): up one level, pinned at the top under
                   every sort, no checkbox (its slot holds only the
                   folder glyph), never selected; it takes focus like any
                   row, and a drop on it moves up. -->
              <!-- svelte-ignore a11y_no_noninteractive_tabindex a11y_no_noninteractive_element_interactions a11y_click_events_have_key_events -->
              <tr
                class="up"
                tabindex="0"
                class:drop-into={isOver(upTarget.id, "up")}
                class:refused={refusedAt(upTarget.id, "up")}
                aria-label={listCopy.upLabel}
                onclick={(e) => { e.stopPropagation(); moveError = null; }}
                oncontextmenu={(e) => { e.preventDefault(); e.stopPropagation(); }}
                ondblclick={() => nav(() => store.goUp())}
                ondragover={(e) => over(e, upTarget, "up", listCopy.up)}
                ondragleave={() => leave(upTarget, "up")}
                ondrop={(e) => dropOn(e, upTarget, "up")}
              >
                <td class="chk"><span class="chk-slot"><svg class="i i-14 glyph" aria-hidden="true"><use href="#i-folder" /></svg></span></td>
                <td class="sel-mark">
                  <div class="fname"><span>{listCopy.up}</span></div>
                  {#if moveError && refusedAt(upTarget.id, "up")}<span class="move-note">Not moved: {moveError.text}</span>{/if}
                </td>
                <td></td>
                <td></td>
                <td></td>
              </tr>
            {/if}
            {#each rows as r, i (r.id)}
              <!-- svelte-ignore a11y_no_noninteractive_tabindex a11y_no_noninteractive_element_interactions a11y_click_events_have_key_events -->
              <tr
                data-id={r.id}
                tabindex={selected.has(r.id) || (selected.size === 0 && i === 0 && !parentId) ? 0 : -1}
                aria-selected={selected.has(r.id)}
                class:fresh={store.fresh.has(r.id)}
                class:leaving={leaving.has(r.id)}
                class:drop-into={isOver(r.id, "row")}
                class:refused={refusedAt(r.id, "row")}
                onpointerdown={(e) => pointerdown(e, r)}
                onclick={(e) => click(e, r)}
                oncontextmenu={(e) => rowMenu(e, r)}
                ondblclick={() => openItem(r)}
                ondragover={(e) => over(e, { id: r.id, isDir: r.isDir }, "row", r.name)}
                ondragleave={() => leave({ id: r.id, isDir: r.isDir }, "row")}
                ondrop={(e) => dropOn(e, { id: r.id, isDir: r.isDir }, "row")}
              >
                <td class="chk"><span class="chk-slot"><input type="checkbox" checked={selected.has(r.id)} tabindex="-1" aria-label={listCopy.tickRow(r.name)} onclick={(e) => tick1(e, r)} ondblclick={(e) => e.stopPropagation()} /><svg class="i i-14 glyph" aria-hidden="true"><use href="#{rowIcon(r)}" /></svg></span></td>
                <td class="sel-mark">
                  <div class="fname">
                    <span>{r.name}</span>
                  </div>
                  {#if moveError && refusedAt(r.id, "row")}<span class="move-note">Not moved: {moveError.text}</span>{/if}
                </td>
                <!-- A folder shows no size and the type *Folder*; a file
                     its plaintext size and a type drawn from its
                     extension (APP.md §6). The folder's sum beneath it
                     and a file's *Stored as* stay in the preview. The
                     Size cell, and its header, are .num: right-aligned,
                     so magnitudes line up (the polish review's small
                     fix 16). A date is not a magnitude: it keeps the
                     left edge and only its figures' one width. -->
                <td class="num">{r.isDir ? "" : bytes(r.size)}</td>
                <td class="type" title={typeLabel(r.name, r.isDir)}>{typeLabel(r.name, r.isDir)}</td>
                <td><span class="num">{dateTime(r.modifiedAt)}</span></td>
              </tr>
            {/each}
          </tbody>
        </table>
        {#if isEmpty}
          {@render emptyFolder()}
        {:else}
          <!-- One row's height of empty space under the last row, scrolled
               or not, so the last row can be brought clear of the pane's
               edge (APP.md §6). -->
          <div class="list-tail" aria-hidden="true"></div>
        {/if}
      {/if}
    </div>

    {#if paneOn}
      <aside class="preview" aria-label="Preview" ondragstart={noDrag}>
        {#if shape === "rest"}
          <!-- At rest the pane describes where you are (direction 3): the
               folder — the root by the archive's name — and what it holds,
               then the archive's facts, then the archive whole to extract.
               It was a grey "Select a file" over a greyed accent slab. -->
          <div class="pane-body">
            <div class="pane-head">
              <h2 class="t-section">{folderName}</h2>
              <span class="t-quiet num">{paneCopy.figures(here.count, here.bytes)}</span>
            </div>
            <dl class="kv pane-facts">
              <dt>{archivesCopy.facts.size}</dt><dd class="num">{stat ? bytes(stat.size) : ""}</dd>
              <dt>{archivesCopy.facts.lastSaved}</dt><dd class="num">{stat?.lastSavedAt ? dateTime(stat.lastSavedAt) : "—"}</dd>
              <dt>{archivesCopy.facts.unused}</dt><dd><span class="num">{stat ? bytes(stat.freeSpace) : ""}</span>{#if freeWorthShowing(stat?.freeSpace)}{" "}<span class="hint">{paneCopy.compactHint}</span>{/if}</dd>
              <dt>{archivesCopy.facts.compression}</dt><dd>{methodWord(method)}</dd>
            </dl>
          </div>
          <div class="preview-actions">
            <button type="button" class="btn" disabled={draining || (stat?.records ?? 0) === 0} title={closingWhy ?? ((stat?.records ?? 0) === 0 ? archivePageCopy.extractNothing : keyTitles.extract)} onclick={extractEverything}><svg class="i i-14"><use href="#i-extract" /></svg>{commandCopy.extract}</button>
          </div>
        {:else if shape === "several"}
          <!-- Several selected: how many and how much — the bytes once every
               selected row is on hand — and the pane's one accent, the
               dialog on the selection. -->
          <div class="pane-body">
            <div class="pane-head">
              <h2 class="t-section num">{paneCopy.figures(picked.count, picked.bytes)}</h2>
            </div>
          </div>
          <div class="preview-actions">
            <!-- One accent per surface (ruled 2026-10-19): while the locked
                 line shows, its *Unlock* is the page's, and this is a
                 standard button. -->
            <button type="button" class="btn" class:accent={store.unlocked} disabled={draining} title={closingWhy ?? keyTitles.extract} onclick={extractCommand}><svg class="i i-14"><use href="#i-extract" /></svg>{paneCopy.extractSome(selected.size)}</button>
          </div>
        {:else if one}
          {@const size = bytes(one.size)}
          {@const type = typeLabel(one.name, one.isDir)}
          {@const stored = compressedText(one.storage, one.savedPercent)}
          {@const modified = dateTime(one.modifiedAt)}
          {#if one.isDir || noPreview}
            <!-- A folder, or a file the pane cannot show: its type's icon
                 and what is so, said as a fact of the type — extracting is
                 offered below as an action, never as the cure. A file
                 selected while the archive drains says it is closing. -->
            <div class="pane-none">
              <svg class="i i-48" aria-hidden="true"><use href="#{rowIcon(one)}" /></svg>
              <span class="t-strong name">{one.name}</span>
              <span class="t-quiet">{one.isDir ? paneCopy.folderInside(bytes(one.size)) : previewClosing ? paneCopy.closing : previewFailed ? paneCopy.previewFailed : paneCopy.noPreview(one.name)}</span>
            </div>
          {:else}
            <!-- The art takes the pane's height (flex: 1 1 auto, at least
                 186 px), so text and images use it. -->
            <div class="art">
              {#if kind === "image" && previewUrl}
                <img src={previewUrl} alt={one.name} draggable="false" />
              {:else if kind === "video" && previewUrl}
                <!-- svelte-ignore a11y_media_has_caption -->
                <video src={previewUrl} controls controlslist="nodownload noplaybackrate" disablepictureinpicture draggable="false"></video>
              {:else if kind === "audio" && previewUrl}
                <audio src={previewUrl} controls controlslist="nodownload"></audio>
              {:else if kind === "text" && previewText}
                <pre>{previewText.text}{previewText.truncated ? "\n…" : ""}</pre>
              {:else if previewLoading}
                <!-- Keyed on the row, so a move from one loading preview to
                     the next waits its own 300 ms again. The ring is the
                     one pending rule (lib/pending.ts, app.css .pending). -->
                {#key one.id}
                  <span class="wait" aria-hidden="true" use:pending={true}></span>
                {/key}
              {/if}
            </div>
            <div class="pname"><svg class="i i-14"><use href="#{rowIcon(one)}" /></svg><span>{one.name}</span></div>
          {/if}
          <!-- The facts are the one label · value list (APP.md §6, Type and
               spacing; ruled 2026-10-17): each value beside its label,
               wrapping rather than cut — and the path under its label.
               *Compressed* says how much smaller, never the method: "zstd +
               dictionary" is the Details dialog's (APP.md §7, ruled 2026-10-17). -->
          <div class="pfacts">
            <dl class="kv">
              <dt>{one.isDir ? "Size beneath" : "Size"}</dt><dd class="num" title={size}>{size}</dd>
              <dt>{columnLabels.type}</dt><dd title={type}>{type}</dd>
              {#if !one.isDir}
                <div><dt>{compressedLabel}</dt><dd title={stored}>{stored}</dd></div>
              {/if}
              <dt>Modified</dt><dd class="num" title={modified}>{modified}</dd>
            </dl>
            <dl class="kv stk"><dt>Path</dt><dd>{one.path}</dd></dl>
          </div>
          <div class="preview-actions">
            <button type="button" class="btn" disabled={draining} title={closingWhy ?? keyTitles.extract} onclick={extractCommand}><svg class="i i-14"><use href="#i-extract" /></svg>{commandCopy.extract}</button>
          </div>
        {/if}
      </aside>
    {/if}
  </div>
</div>


<!-- Rename and Create folder are forms (APP.md §6, ruled 2026-10-14):
     Enter presses the accent button, Esc cancels, the field has the focus
     as the dialog opens — Rename's with the stem selected. Every dialog's
     buttons stand in its foot band in equal widths, the accent or filled
     action first and *Cancel* last (APP.md §6, ruled 2026-10-17). -->
{#if renaming}
  <!-- The title names what is renamed, and a quiet line under the field
       says what Enter does and when a taken name is said (APP.md §6,
       ruled 2026-10-17). -->
  <Dialog title="Rename {renaming.name}" onclose={() => (renaming = null)} onsubmit={doRename} select={(v) => renameSelection(v, renaming?.isDir ?? false)}>
    <TextField id="rn" label="New name" bind:value={renameTo} bind:valid={renameValid} attempt={renameAttempt} judge={judgeRename} />
    <p class="t-quiet">Enter renames. A name already in {folderName} is refused here, not after.</p>
    {#snippet actions()}
      <button type="submit" class="btn accent">Rename</button>
      <button type="button" class="btn" onclick={() => (renaming = null)}>{dialogCopy.cancel}</button>
    {/snippet}
  </Dialog>
{/if}

<!-- Create folder (APP.md §6): the name is judged by the rename's own
     rule, and against the names this folder already holds. The button is
     never disabled for an empty field — pressing it marks the field. No
     placeholder: an invented name reads as the one an empty field gives,
     and an empty field gives none. The body says what the user gets, not
     how the record is written; the primary repeats the verb with its
     object (APP.md §7, ruled 2026-10-17). -->
{#if creating}
  <Dialog title="Create folder" onclose={() => (creating = false)} onsubmit={makeFolder}>
    <TextField id="cf-name" label="Name" bind:value={newFolder} bind:valid={newFolderValid} attempt={newFolderAttempt} judge={judgeFolder} />
    <p>The folder is created now and stays, even while it is empty.</p>
    {#snippet actions()}
      <button type="submit" class="btn accent">Create folder</button>
      <button type="button" class="btn" onclick={() => (creating = false)}>{dialogCopy.cancel}</button>
    {/snippet}
  </Dialog>
{/if}

<!-- The delete dialog (APP.md §3, §6): files and folders counted apart,
     the folder sentence only when a folder is in the selection, and no
     undo anywhere in the copy - deletion is cryptographic erasure. -->
{#if deleting}
  <Dialog title={deleteTitle(deleting.rows, stat?.name ?? "")} onclose={() => (deleting = null)}>
    <p>{deleteBody(deleting.rows)}</p>
    {#snippet actions()}
      <button type="button" class="btn danger-fill" onclick={doDelete}>Delete</button>
      <button type="button" class="btn" onclick={() => (deleting = null)}>{dialogCopy.cancel}</button>
    {/snippet}
  </Dialog>
{/if}

<!-- Delete archive… (APP.md §13): the Archives page's one dialog, over
     this page; the archive is let go of only on consent (letGo). -->
{#if deletingArchive}
  <DeleteArchiveDialog
    id={deletingArchive.id}
    name={deletingArchive.name}
    path={deletingArchive.path}
    createdAt={deletingArchive.createdAt}
    mode="delete"
    onclose={closeDeleteArchive}
    release={() => letGo(deletingArchive?.id ?? "")}
  />
{/if}

{#if extracting}
  <ExtractDialog
    archiveName={archiveName || (stat?.name ?? "")}
    archivePath={store.currentPath}
    selectedCount={extracting.count}
    onextract={(scope, dir, policy) => void doExtract(scope, dir, policy)}
    oncancel={() => (extracting = null)}
  />
{/if}

<!-- What an `ask` came back with (APP.md §3), in the shape of Explorer's
     own "Replace or Skip Files" (direction 7): the title counts and names
     the destination — one conflict names the file — and three command
     rows of equal weight say what each answer does. Skipping re-issues
     nothing. The words are the strings table's (§7). -->
{#if question && conflicts.length > 0 && !comparing}
  <!-- Esc and the corner close are Skip — never Replace — and the
       backdrop is nothing: the dialog has a choice (APP.md §6, "Dialogs
       stay put"). No row wears the accent, so the one that looks like the
       default is never the mass overwrite (ruled 2026-10-14). -->
  <Dialog title={conflictTitle(conflicts.length, conflicts[0].name, question.destination ?? "")} onclose={() => decide("skip")} closeButton closeTitle={dialogCopy.skipEsc}>
    <CommandRows rows={conflictRows} />
  </Dialog>
{/if}

{#if question && conflicts.length > 0 && comparing}
  {#key question.id}
    <ConflictDialog
      rows={conflicts}
      oncontinue={(d) => void reissue(d)}
      oncancel={dismissQuestion}
    />
  {/key}
{/if}

<!-- The names the destination refused (APP.md §3, ruled 2026-09-10),
     asked per record once the same op's conflicts, if any, are answered.
     Keyed on the op, so a name refused again starts a fresh question. -->
{#if refused && refusedRows.length > 0 && !question}
  {#key refused.id}
    <RefusedDialog rows={refusedRows} ondone={(d) => void reissueRefused(d)} />
  {/key}
{/if}

<!-- The add collisions (APP.md §3) in the conflict question's shape
     (direction 7): the title counts what is already there by its kind and
     names the folder, the names follow with each one's kind — both kinds
     when they differ — and then Replace · Keep both · Skip as command rows,
     Cancel in the band. Replace is greyed with its reason whenever a file
     and a folder meet: kinds that differ never replace one another, in
     either direction. A folder meeting a folder is said as a merge, and a
     note under the names says what the choice does inside it. No row
     wears the accent (ruled 2026-10-14). A collision carries the two kinds
     only, so no size or date is said. -->
{#if collisions && collisionSaid}
  <Dialog title={collisionSaid.title} onclose={() => (collisions = null)}>
    <ul class="collide">
      {#each collisions.list.slice(0, 6) as c (c.name)}
        <li>
          <svg class="i i-14" aria-hidden="true"><use href="#{fileIcon(c.name, c.existingIsDir)}" /></svg>
          <span class="nm">{shownName(c.name)}</span>
          <span class="kd" class:clash={c.isDir !== c.existingIsDir}>{collisionCopy.kind(c.isDir, c.existingIsDir)}</span>
        </li>
      {/each}
      {#if collisions.list.length > 6}<li class="more">{collisionCopy.andMore(collisions.list.length - 6)}</li>{/if}
    </ul>
    {#if collisionSaid.note}<p class="merge">{collisionSaid.note}</p>{/if}
    <CommandRows rows={collisionRows} />
    {#snippet actions()}
      <button type="button" class="btn" onclick={() => (collisions = null)}>{dialogCopy.cancel}</button>
    {/snippet}
  </Dialog>
{/if}

<!-- An empty folder (direction 3): named — the root by the archive's
     name — with both ways to fill it under the line and the drop said
     last, in the caption, centred in the list, which stays the drop
     target as a whole. -->
<!-- A row's menu, or the blank area's (direction 7): keyed on the open,
     so a second right-click elsewhere is a menu of its own, placed anew. -->
{#if ctx}
  {@const c = ctx}
  {#key c}
    <ContextMenu x={c.x} y={c.y} keyboard={c.key} items={ctxItems} label={c.row?.name ?? folderName} onclose={closeCtx} />
  {/key}
{/if}

<!-- Move to… (direction 7): the folder tree over the page. -->
{#if moving}
  <MoveToDialog
    archiveId={id}
    archiveName={archiveName || (stat?.name ?? "")}
    rootId={ROOT_ID}
    current={dirId}
    trail={crumbs.map((c) => c.id)}
    ids={moving.ids}
    name={moving.name}
    onmoved={() => void movedTo()}
    oncreated={() => void store.refreshArchive()}
    oncancel={cancelMoveTo}
  />
{/if}

<!-- The viewer (direction 7): over the page's area of the layer, under
     every dialog — its own Extract… opens over it. -->
{#if viewing}
  <Viewer
    archiveId={id}
    {rows}
    current={viewing.id}
    pinned={viewing.pinned}
    more={hasMore(store.page)}
    extractWhy={closingWhy}
    onstep={viewStep}
    onclose={closeViewer}
    onextract={viewExtract}
    onmore={() => void store.loadMore()}
  />
{/if}

{#snippet emptyFolder()}
  <div class="empty-folder">
    <h2 class="t-section">{archivePageCopy.emptyTitle(folderName)}</h2>
    <div class="row">
      <button type="button" class="btn" disabled={draining} onclick={() => void pickFiles()}><svg class="i i-14"><use href="#i-file" /></svg>{commandCopy.addFiles}</button>
      <button type="button" class="btn" disabled={draining} onclick={() => void pickFolder()}><svg class="i i-14"><use href="#i-folder" /></svg>{commandCopy.addFolder}</button>
    </div>
    <span class="t-quiet">{archivePageCopy.emptyDrop}</span>
  </div>
{/snippet}

<style>
  /* The draining line beside *Force close*: caption ink-2 on one line, the
     crumbs giving way to it (they are cut, never it). */
  .drain-note { flex: none; font: var(--t-cap); color: var(--ink-2); white-space: nowrap; }

  /* The facts under the preview's name: the card's 16 at the sides, 12
     above and below and between the two lists (APP.md §6, Type and
     spacing). A long path wraps rather than being cut, so in a short
     window the facts scroll in their own box and the pane's *Extract…*
     stays in view. */
  .pfacts {
    display: flex;
    flex-direction: column;
    gap: var(--s3);
    padding: var(--s3) var(--s4);
    flex: 0 1 auto;
    min-height: 0;
    overflow-y: auto;
  }

  /* The preview's wait (APP.md §6, ruled 2026-10-14): the pending ring
     (app.css .pending), which the action shows after 300 ms. */
  .wait { display: inline-flex; }

  /* The add collisions' names (direction 7): the kind's icon, the name,
     and its kind in caption at the right — the clash in the attention
     ink, since that is what greys Replace. One line each, 4 apart. */
  .collide { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--s1); }
  .collide li { display: flex; align-items: center; gap: var(--s2); min-width: 0; }
  .collide .i { color: var(--ink-3); }
  .collide .nm { flex: 1 1 auto; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .collide .kd { flex: none; font: var(--t-cap); color: var(--ink-2); }
  .collide .kd.clash { color: var(--attention); }
  .collide .more { color: var(--ink-2); }
  .merge { margin: 0; font: var(--t-cap); color: var(--ink-2); }

  /* A folder a spring-loaded hover entered takes a drop on its blank area
     (direction 7): the list is washed as a folder row is under the drag. */
  .tablewrap.drop-into { background-color: var(--accent-wash); outline: 1px solid var(--accent-line); outline-offset: -1px; }
</style>

<script lang="ts">
  import { tick, untrack } from "svelte";
  import { Archive, Archives, Code, Shell, errorOf } from "../lib/api";
  import type { Collision, FileRow } from "../lib/api";
  import { store } from "../lib/state.svelte";
  import { archivePageCopy, codeText, columnLabels, commandCopy, compressedLabel, compressedText, conflictCopy, conflictTitle, listCopy, typeLabel } from "../lib/strings";
  import { bytes, dateTime, fileIcon, previewKind } from "../lib/format";
  import type { PreviewKind } from "../lib/format";
  import { statusNote } from "../lib/status";
  import { newNameProblem } from "../lib/validate";
  import { renameSelection } from "../lib/paths";
  import { ROOT_ID, canDrop, countPhrase, deleteBody, deleteCounts, deleteTitle } from "../lib/tree";
  import type { DropTarget, Kinded } from "../lib/tree";
  import { ownNames, pastThreshold } from "../lib/dragout";
  import type { DropAt, Flight, PendingMove } from "../lib/dragout";
  import { summaryLine, tally, troubles } from "../lib/results";
  import { destinationFor } from "../lib/extract";
  import { allOf, conflictsOf, namesFor, planIsEmpty, reissuePlan } from "../lib/conflicts";
  import type { Decision } from "../lib/conflicts";
  import { SORT_KEYS, arrowOf, clickHeader as clickSortHeader } from "../lib/sort";
  import type { SortKey } from "../lib/sort";
  import { headerTicked } from "../lib/selection";
  import { hasMore, nearEnd } from "../lib/paging";
  import { planIsEmpty as refusedPlanIsEmpty, refusedOf, refusedPlan } from "../lib/refused";
  import type { RefusedDecision } from "../lib/refused";
  import { dialogIsUp } from "../lib/dialogs";
  import Dialog from "./Dialog.svelte";
  import ConflictDialog from "./ConflictDialog.svelte";
  import DeleteArchiveDialog from "./DeleteArchiveDialog.svelte";
  import ExtractDialog from "./ExtractDialog.svelte";
  import RefusedDialog from "./RefusedDialog.svelte";
  import MenuButton from "./MenuButton.svelte";
  import type { MenuItem } from "./MenuButton.svelte";
  import OpsBar from "./OpsBar.svelte";
  import TextField from "./TextField.svelte";

  const id = $derived(store.current ?? "");
  const stat = $derived(store.stat);
  // The index is a tree (APP.md §3): a row is a record with an id, the
  // folder shown is a directory id, and the heading is the page's own
  // Crumbs' first entry — the archive's name — drawn from nothing else. No
  // folder crumb is drawn (§6, ruled 2026-09-10): the list itself shows
  // the folder, its first row `..` going up one level.
  const rows = $derived(store.page?.rows ?? []);
  const total = $derived(store.page?.total ?? 0);
  const crumbs = $derived(store.page?.crumbs ?? []);
  const archiveName = $derived(crumbs[0]?.name ?? "");
  const dirId = $derived(store.dirId);
  // In a folder — anywhere but the root — the parent is the crumb before
  // the last, and `..` is a row.
  const parentId = $derived(crumbs.length > 1 ? crumbs[crumbs.length - 2].id : null);
  // The folder shown, by name — the archive's at the root — for the empty
  // folder's line: "Trips is empty. Drop files here, or use Add."
  const folderName = $derived(crumbs[crumbs.length - 1]?.name ?? "");
  const sort = $derived(store.sort);
  // Everything on this page except *Delete archive...* stays usable after
  // a lock (APP.md §2.3): Page, Stat, the previews, Extract and every
  // operation - each committing the archive and owing its receipt until
  // the vault comes back. Only the registry write needs the session.
  const tampered = $derived(store.status?.tampered ?? false);

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
  let extracting = $state<{ ids: string[]; label: string; all: boolean } | null>(null);
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
  // The names the destination refused (APP.md §3, ruled 2026-09-10): the
  // same shape, asked after the conflict question of the same op.
  const refused = $derived(store.refusedQuestion);
  const refusedRows = $derived(refused ? refusedOf(refused.results) : []);
  let collisions = $state<{ at: string; plan: AddPlan; list: Collision[] } | null>(null);
  const kindsDiffer = $derived((collisions?.list ?? []).some((c) => c.isDir !== c.existingIsDir));
  let dropping = $state(false);

  function fail(e: unknown) {
    store.toast(codeText(errorOf(e).code), "error");
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

  // The keyboard path: Space selects, Enter selects and opens, the arrows
  // move the focus between rows — `..` among them.
  function keydown(e: KeyboardEvent, r: FileRow) {
    switch (e.key) {
      case " ":
        e.preventDefault();
        store.selectClick(r.id);
        break;
      case "Enter":
        e.preventDefault();
        store.selectClick(r.id);
        open(r);
        break;
      default:
        arrows(e);
    }
  }

  // `..` takes focus like any row (APP.md §6): Enter or Space goes up.
  function upKeydown(e: KeyboardEvent) {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      void store.goUp();
    } else {
      arrows(e);
    }
  }

  function arrows(e: KeyboardEvent) {
    const el = e.currentTarget as HTMLElement;
    if (e.key === "ArrowDown") {
      e.preventDefault();
      (el.nextElementSibling as HTMLElement | null)?.focus();
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      (el.previousElementSibling as HTMLElement | null)?.focus();
    }
  }

  // Ctrl+A selects the whole folder but `..` (APP.md §6) when the focus is
  // in the list or on the page's body: an input keeps its own select-all,
  // and while a dialog is up the page behind it takes no shortcut — the
  // Dialog stops the key itself (lib/dialogs.ts); the check here says so.
  let listEl = $state<HTMLDivElement | undefined>();
  function shortcut(e: KeyboardEvent) {
    if (!(e.ctrlKey || e.metaKey) || e.altKey || e.shiftKey || (e.key !== "a" && e.key !== "A")) return;
    const t = e.target as HTMLElement | null;
    if (!t || t.closest("input, textarea, select, [contenteditable]")) return;
    if (t !== document.body && !listEl?.contains(t)) return;
    if (dialogIsUp()) return;
    e.preventDefault();
    void store.selectFolder();
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
    void store.enterDir(r.id, r.name);
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

  // After going up, focus lands on the row of the folder just left (APP.md
  // §6): the store names it once the listing lands.
  $effect(() => {
    const want = store.focusAfter;
    if (!want || !rows.some((r) => r.id === want)) return;
    void tick().then(() => {
      const el = listEl?.querySelector<HTMLElement>(`tr[data-id="${want}"]`);
      if (el && store.focusAfter === want) {
        el.focus();
        store.focusAfter = null;
      }
    });
  });

  // The preview follows the single selection. Every row is committed - an
  // operation commits at its end (APP.md §2.3) - so nothing here waits
  // for a save.
  $effect(() => {
    const r = one;
    previewUrl = "";
    previewText = null;
    previewFailed = false;
    kind = "none";
    if (!r || r.isDir) return;
    const k = previewKind(r.name);
    kind = k;
    const failed = (e: unknown) => {
      if (one === r) previewFailed = true;
      fail(e);
    };
    if (k === "text") {
      Archive.PreviewText(id, r.id, 64 * 1024).then((t) => { if (one === r) previewText = t; }).catch(failed);
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
  // A call that failed stops the wait; its toast says why.
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
  interface AddPlan {
    files: string[];
    dirs: string[];
  }

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
        collisions = { at, plan, list };
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

  // One *Add* button, three ways to add (APP.md §6): the two pickers, and
  // a folder made here, which is a record from the moment it is made.
  // Each opens a picker or a dialog, so each ends with an ellipsis, as
  // every command on this page that asks something first does.
  const addItems: MenuItem[] = $derived([
    { label: "Add files…", icon: "i-plus", run: () => void pickFiles() },
    { label: "Add folder…", icon: "i-folder-add", run: () => void pickFolder() },
    { label: "Create folder…", icon: "i-folder", run: askFolder },
  ]);

  // New archive… ends here, on the new archive's empty root with the Add
  // menu open (APP.md §13, ruled 2026-10-14): the store names the archive
  // it was asked for, and this page takes the ask once as it mounts. The
  // menu is opened by the press it answers anyway — the button focused
  // and clicked — so it is the menu the user would have opened: the arrows
  // walk it, and Escape or a click elsewhere closes it.
  let cmdbar = $state<HTMLDivElement | undefined>();
  $effect(() => {
    const bar = cmdbar;
    if (!bar || !untrack(() => store.takeAddAsked(id))) return;
    const add = bar.querySelector<HTMLButtonElement>("#add");
    add?.focus();
    add?.click();
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
      await Archive.CreateFolder(id, dirId, name);
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
      store.toast("Drop files onto the archive they belong in.", "error");
      return;
    }
    // The target is an id or it is nothing: a drop the page cannot vouch
    // for is refused, never resolved to some folder. Retargeting a drop at
    // the root would write the files somewhere the user did not aim at,
    // and the core cannot refuse it — the root always resolves (§3).
    if (!d.dirId) {
      store.toast("Drop files onto the file list.", "error");
      return;
    }
    const kinds = d.isDir ?? [];
    void addAt(d.dirId, {
      files: d.paths.filter((_, i) => !kinds[i]),
      dirs: d.paths.filter((_, i) => !!kinds[i]),
    });
  });

  // A drag of the selection onto a folder row, onto `..` (up one level) or
  // onto the heading (to the root) is a Move (APP.md §6): the native drag
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
  const headTarget: DropTarget = { id: ROOT_ID, isDir: true };

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

  function over(e: DragEvent, t: DropTarget, at: DropAt) {
    const f = store.dragOut;
    if (!f || foreignHover(e, f)) return;
    // Ours: the runtime's document-level listener must not see it, since
    // it would set the effect to none over the heading, which is outside
    // the file-drop target, and Explorer's cursor over a file row would
    // otherwise offer a copy that means nothing.
    e.preventDefault();
    e.stopPropagation();
    const ok = canDrop(t, f);
    if (e.dataTransfer) e.dataTransfer.dropEffect = ok ? "move" : "none";
    dropTarget = ok ? { id: t.id, at } : null;
  }

  // The list's blank area under our own drag: no effect, and a release
  // there is nothing (APP.md §3). A row's handler has already stopped the
  // event by the time it would reach here.
  function overBlank(e: DragEvent) {
    const f = store.dragOut;
    if (!f || foreignHover(e, f)) return;
    e.preventDefault();
    e.stopPropagation();
    if (e.dataTransfer) e.dataTransfer.dropEffect = "none";
    dropTarget = null;
  }

  function leave(t: DropTarget, at: DropAt) {
    if (dropTarget?.id === t.id && dropTarget.at === at) dropTarget = null;
  }

  const isOver = (id: string, at: DropAt) => dropTarget?.id === id && dropTarget.at === at;
  const refusedAt = (id: string, at: DropAt) => moveError?.id === id && moveError.at === at;

  function dropOn(e: DragEvent, t: DropTarget, at: DropAt) {
    const f = store.dragOut;
    if (!f) return;
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

  async function doDelete() {
    const d = deleting;
    deleting = null;
    if (!d) return;
    try {
      await Archive.Delete(id, d.ids);
      store.clearSelection();
      await store.refreshArchive();
    } catch (e) {
      fail(e);
    }
  }

  // Both ways in open the same dialog (APP.md §3): the destination lives
  // in it, prefilled by the rule of §3 from the archive's own folder —
  // *Extract all…* one level deeper, in a folder named after the archive —
  // rather than in a native picker the page opens first. The destination is
  // never kept from the last time (ruled 2026-09-10); the policy is, while
  // Enfold runs (lib/extract.ts startingPolicy, ruled 2026-10-14).
  function startExtract(ids: string[], label: string, all = false) {
    if (ids.length === 0) return;
    extracting = { ids, label, all };
  }

  // *Extract all…* is the archive's, not the selection's (APP.md §6): the
  // root id alone means everything, so the page sends that one id and
  // walks nothing.
  function extractAll() {
    // The title names the archive (APP.md §3, ruled 2026-10-17):
    // "Extract everything in Photos 2024".
    startExtract([ROOT_ID], archiveName ? `everything in ${archiveName}` : "everything in this archive", true);
  }

  async function doExtract(dir: string, policy: string) {
    const x = extracting;
    extracting = null;
    if (!x) return;
    try {
      // An `ask` comes back with its question on the op itself (APP.md
      // §3): nothing is kept here against the op id.
      await Archive.Extract(id, x.ids, dir, policy, null);
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

  // The one-conflict and many-conflict answers (APP.md §3): *Replace* and
  // *Replace all* re-issue for every record with `replace`, *Skip* and
  // *Skip all* re-issue nothing, and *Compare* / *Let me decide* open the
  // compare list. Closing either dialog answers nothing and the question
  // is settled: the files in the way stay where they are.
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

  // *Close archive* is the kill switch (APP.md §2.3): it closes now,
  // readers or not, dropping every preview body in flight — where simply
  // leaving the page closes it only once nothing is reading it.
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
  // store's own leave, which reads the list again.
  function closeDeleteArchive() {
    deletingArchive = null;
    if (released) store.go("archives");
  }

  // This page gone with the dialog still up — a lock scene drawn over it
  // takes both — the archive it let go of is read like any other again,
  // and the Stat's archive.not_open then leaves it (refreshArchive).
  $effect(() => () => {
    if (released) store.releasedForDelete = null;
  });

  // What a batch reported when something was left out (lib/results.ts):
  // the counts — folders created and entered beside the files added — and
  // then the items themselves, each with its own code's copy.
  const results = $derived(store.results);
  const resultLine = $derived(summaryLine(tally(results?.results)));
  const resultItems = $derived(troubles(results?.results));

  // The foot's note (LayerFoot, lib/status.ts): the archive's figures —
  // singular at one, and the free space when it is worth knowing. Nothing
  // counts down here: an open archive has no timeout of its own, locked
  // vault or not (APP.md §2.3, ruled 2026-09-10).
  $effect(() => {
    store.footNote = statusNote(stat);
  });
</script>

<svelte:window onkeydown={shortcut} ondrop={dropElsewhere} />

<div class="layer-head">
  <button type="button" class="btn subtle back" onclick={() => { store.leaveArchive(); }}>Archives</button>
  <!-- The heading is the archive's name alone (APP.md §6, ruled
       2026-09-10) — Page's Crumbs[0], never Stat — a button to the root
       and a drop target, so a drag can move a selection to the top. -->
  <h1 class="t-title">
    <button
      type="button"
      class="head-name"
      class:drop-into={isOver(ROOT_ID, "head")}
      title={listCopy.toRoot}
      onclick={() => void store.enterDir(ROOT_ID)}
      ondragover={(e) => over(e, headTarget, "head")}
      ondragleave={() => leave(headTarget, "head")}
      ondrop={(e) => dropOn(e, headTarget, "head")}
    >{archiveName}</button>
  </h1>
  <div class="grow"></div>
  <!-- The kill switch (APP.md §2.3): leaving the page closes the archive
       on its own, and this closes it even while something is playing. -->
  <button type="button" class="btn sm" title={archivePageCopy.closeNow} onclick={close}>Close archive</button>
</div>

<div class="layer-body">
  {#if moveError && refusedAt(ROOT_ID, "head")}
    <div class="move-note">Not moved: {moveError.text}</div>
  {/if}
  <div class="cmdbar" bind:this={cmdbar}>
    <MenuButton id="add" label="Add" icon="i-plus" items={addItems} />
    <!-- Greyed on Records, never on Files: a record count is the only
         thing that can say whether the tree holds anything, since a
         folder occupies no data region and an archive of folders alone
         reports 0 files while holding real records (APP.md §3). -->
    <button type="button" class="btn subtle" disabled={(stat?.records ?? 0) === 0} title={(stat?.records ?? 0) === 0 ? "This archive holds nothing yet" : undefined} onclick={extractAll}><svg class="i i-14"><use href="#i-extract" /></svg>{commandCopy.extractAll}</button>
    <button type="button" class="btn subtle" disabled={!one} title={one ? undefined : "Select one item to rename"} onclick={() => { if (one) askRename(one); }}><svg class="i i-14"><use href="#i-rename" /></svg>Rename…</button>
    <button type="button" class="btn subtle danger" disabled={selected.size === 0} title={selected.size === 0 ? "Select what to delete" : undefined} onclick={() => void askDelete()}><svg class="i i-14"><use href="#i-trash" /></svg>Delete…</button>
    <div class="grow"></div>
    <!-- Tampered disables it here as it does on the Archives page (APP.md
         §13, R25): the flow closes the archive before the write is even
         attempted, so a refusal at the end would have shut the user's
         archive for nothing. -->
    <button type="button" class="btn subtle danger" disabled={!store.unlocked || tampered} title={!store.unlocked ? archivePageCopy.deleteNeedsUnlock : tampered ? archivePageCopy.deleteTampered : undefined} onclick={() => void askDeleteArchive()}><svg class="i i-14"><use href="#i-trash" /></svg>{commandCopy.deleteArchive}</button>
  </div>

  {#if !store.unlocked}
    <!-- One line of 40 px, everything on it centred (APP.md §7, ruled
         2026-09-11): .bar.line. -->
    <div class="bar line">
      <svg class="i i-14"><use href="#i-lock" /></svg>
      <!-- One plain line (APP.md §2.3, §6, ruled 2026-09-10): the archive
           has no timeout of its own, and what it owes the vault is
           already said by the status strip. -->
      <span class="grow">{archivePageCopy.lockedBanner}</span>
      <button type="button" class="btn sm accent" onclick={() => store.unlock()}>{commandCopy.unlock}</button>
    </div>
  {/if}
  {#if stat?.state === "needs_reopen"}
    <!-- The button is the head's *Close archive*, by the same word, since
         it is the same act: it closes, and the archive is opened again
         from the list. "Close and reopen" promised a reopen it does not do
         (one word per thing, consequences first: APP.md §7, ruled
         2026-10-17). -->
    <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span class="grow">{codeText("archive.needs_reopen")}</span><button type="button" class="btn sm" onclick={close}>Close archive</button></div>
  {/if}
  {#if stat?.copyMismatch}
    <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span>{codeText("archive.copy_mismatch")}</span></div>
  {/if}
  <!-- The operation strip and nothing else: no pending bar, no Save, no
       Discard, since every operation commits at its end (APP.md §2.3). -->
  <OpsBar />

  <div class="file-split" class:dropping>
    <!-- A click on the list's blank area — under the last row, or in the
         container beside the table — clears the selection and the anchor
         (APP.md §6); a click that lands on a row is the row's. The drop
         target carries the directory id the page is showing, never a
         name: a stale id is refused, where a stale path would resolve to
         whatever folder now happens to carry that name (§3). -->
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions, a11y_click_events_have_key_events -->
    <div class="tablewrap" data-file-drop-target="true" data-archive-id={id} data-dir-id={dirId}
      role="region" aria-label="Files" bind:this={listEl} onclick={blank} onscroll={scrolled}
      ondragenter={() => { if (!store.dragOut) dropping = true; }} ondragover={overBlank} ondragleave={() => (dropping = false)} ondrop={() => (dropping = false)}>
      {#if rows.length === 0 && !parentId}
        <div class="empty">{listCopy.empty(folderName)}</div>
      {:else}
        <!-- Four columns (APP.md §6, ruled 2026-09-10): Name, Size, Type,
             Modified, a checkbox heading every row and the header row.
             Each header cell sorts by its column; when the pane narrows
             the columns give way from the right, and the Name cell then
             carries the hidden column's key beside its chevron (app.css). -->
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
          <tbody>
            {#if parentId && upTarget}
              <!-- `..` (APP.md §6): up one level, pinned at the top under
                   every sort, no checkbox, never selected; it takes
                   focus like any row, and a drop on it moves up. -->
              <!-- svelte-ignore a11y_no_noninteractive_tabindex a11y_no_noninteractive_element_interactions -->
              <tr
                class="up"
                tabindex="0"
                class:drop-into={isOver(upTarget.id, "up")}
                class:refused={refusedAt(upTarget.id, "up")}
                aria-label={listCopy.upLabel}
                onclick={(e) => { e.stopPropagation(); moveError = null; }}
                ondblclick={() => void store.goUp()}
                onkeydown={upKeydown}
                ondragover={(e) => over(e, upTarget, "up")}
                ondragleave={() => leave(upTarget, "up")}
                ondrop={(e) => dropOn(e, upTarget, "up")}
              >
                <td class="chk"></td>
                <td class="sel-mark">
                  <div class="fname"><svg class="i i-14"><use href="#i-folder" /></svg><span>{listCopy.up}</span></div>
                  {#if moveError && refusedAt(upTarget.id, "up")}<span class="move-note">Not moved: {moveError.text}</span>{/if}
                </td>
                <td></td>
                <td></td>
                <td></td>
              </tr>
            {/if}
            {#each rows as r, i (r.id)}
              <!-- svelte-ignore a11y_no_noninteractive_tabindex a11y_no_noninteractive_element_interactions -->
              <tr
                data-id={r.id}
                tabindex={selected.has(r.id) || (selected.size === 0 && i === 0 && !parentId) ? 0 : -1}
                aria-selected={selected.has(r.id)}
                class:drop-into={isOver(r.id, "row")}
                class:refused={refusedAt(r.id, "row")}
                onpointerdown={(e) => pointerdown(e, r)}
                onclick={(e) => click(e, r)}
                ondblclick={() => open(r)}
                onkeydown={(e) => keydown(e, r)}
                ondragover={(e) => over(e, { id: r.id, isDir: r.isDir }, "row")}
                ondragleave={() => leave({ id: r.id, isDir: r.isDir }, "row")}
                ondrop={(e) => dropOn(e, { id: r.id, isDir: r.isDir }, "row")}
              >
                <td class="chk"><input type="checkbox" checked={selected.has(r.id)} tabindex="-1" aria-label={listCopy.tickRow(r.name)} onclick={(e) => tick1(e, r)} ondblclick={(e) => e.stopPropagation()} /></td>
                <td class="sel-mark">
                  <div class="fname">
                    <svg class="i i-14"><use href="#{rowIcon(r)}" /></svg>
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
        {#if rows.length === 0}
          <div class="empty">{listCopy.empty(folderName)}</div>
        {/if}
        <!-- One row's height of empty space under the last row, scrolled
             or not, so the last row can be brought clear of the pane's
             edge (APP.md §6). -->
        <div class="list-tail" aria-hidden="true"></div>
      {/if}
    </div>

    <aside class="preview" aria-label="Preview">
      <div class="art">
        {#if one && kind === "image" && previewUrl}
          <img src={previewUrl} alt={one.name} />
        {:else if one && kind === "video" && previewUrl}
          <!-- svelte-ignore a11y_media_has_caption -->
          <video src={previewUrl} controls controlslist="nodownload noplaybackrate" disablepictureinpicture></video>
        {:else if one && kind === "audio" && previewUrl}
          <audio src={previewUrl} controls controlslist="nodownload"></audio>
        {:else if one && kind === "text" && previewText}
          <pre>{previewText.text}{previewText.truncated ? "\n…" : ""}</pre>
        {:else if previewLoading}
          <!-- Keyed on the row, so a move from one loading preview to the
               next waits its own 300 ms again. -->
          {#key one?.id}
            <span class="wait" aria-hidden="true"></span>
          {/key}
        {:else if one && one.isDir}
          <span class="none">Folder · {bytes(one.size)} inside</span>
        {:else if one && previewFailed}
          <span class="none">The preview could not be loaded — extract it</span>
        {:else if one}
          <span class="none">No preview for this type — extract it</span>
        {:else}
          <span class="none">{selected.size > 1 ? listCopy.selected(selected.size) : listCopy.selectOne}</span>
        {/if}
      </div>
      {#if one}
        {@const size = bytes(one.size)}
        {@const type = typeLabel(one.name, one.isDir)}
        {@const stored = compressedText(one.storage, one.savedPercent)}
        {@const modified = dateTime(one.modifiedAt)}
        <div class="pname"><svg class="i i-14"><use href="#{rowIcon(one)}" /></svg><span>{one.name}</span></div>
        <!-- The facts are the one label · value list (APP.md §6, Type and
             spacing; ruled 2026-10-17): each value beside its label, never
             pushed to the pane's far edge, wrapping rather than cut — and
             the path under its label, wrapping too.
             *Compressed* says how much smaller, never the method: "zstd +
             dictionary" is Technical details' (APP.md §7, ruled 2026-10-17). -->
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
      {/if}
      <div class="preview-actions">
        <button type="button" class="btn accent" disabled={selected.size === 0} title={selected.size === 0 ? "Select what to extract" : undefined} onclick={() => startExtract(chosenIds, countPhrase(deleteCounts(chosen)) || listCopy.selected(selected.size))}>{commandCopy.extract}</button>
      </div>
    </aside>
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
      <button type="button" class="btn" onclick={() => (renaming = null)}>Cancel</button>
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
      <button type="button" class="btn" onclick={() => (creating = false)}>Cancel</button>
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
      <button type="button" class="btn" onclick={() => (deleting = null)}>Cancel</button>
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
    label={extracting.label}
    initial={destinationFor(store.currentPath, stat?.name ?? "", extracting.all)}
    onextract={(dir, policy) => void doExtract(dir, policy)}
    oncancel={() => (extracting = null)}
  />
{/if}

<!-- What an `ask` came back with (APP.md §3): one conflict names the file,
     several count them, and *Compare* / *Let me decide* open the compare
     list. Skipping re-issues nothing. The words are the strings table's
     (§7). -->
{#if question && conflicts.length > 0 && !comparing}
  <!-- Esc is Skip, or Skip all — never Replace — and the backdrop is
       nothing: the dialog has a choice (APP.md §6, "Dialogs stay put").
       No button wears the accent, so the one that looks like the default
       is never the mass overwrite (ruled 2026-10-14). -->
  <Dialog title={conflictTitle(conflicts.length, conflicts[0].name)} onclose={() => decide("skip")}>
    {#if conflicts.length === 1}
      <p>{conflictCopy.oneBody}</p>
    {:else}
      <ul class="plain">
        {#each conflicts.slice(0, 6) as c (c.path)}<li>{c.path}</li>{/each}
        {#if conflicts.length > 6}<li>{conflictCopy.andMore(conflicts.length - 6)}</li>{/if}
      </ul>
    {/if}
    {#snippet actions()}
      <button type="button" class="btn" onclick={() => (comparing = true)}>{conflictCopy.compare(conflicts.length)}</button>
      <button type="button" class="btn" onclick={() => decide("skip")}>{conflictCopy.skip(conflicts.length)}</button>
      <button type="button" class="btn" onclick={() => decide("replace")}>{conflictCopy.replace(conflicts.length)}</button>
    {/snippet}
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

<!-- The collision dialog says the kind on both sides — "Photos is a file
     here" — and greys *Replace* whenever the two differ: kinds that differ
     never replace one another, in either direction (APP.md §3). Every
     button is a standard one: the accent never sits on Replace (ruled
     2026-10-14). -->
{#if collisions}
  <Dialog title="Some names already exist" onclose={() => (collisions = null)}>
    <ul class="plain">
      {#each collisions.list.slice(0, 6) as c (c.name)}
        <li>{c.name} is {c.existingIsDir ? "a folder" : "a file"} here{c.isDir === c.existingIsDir ? "" : c.isDir ? ", and a folder is being added" : ", and a file is being added"}.</li>
      {/each}
      {#if collisions.list.length > 6}<li>and {collisions.list.length - 6} more.</li>{/if}
    </ul>
    {#if kindsDiffer}<p class="dim">A file and a folder never replace one another, so <b>Replace</b> is off: skip those, or keep both.</p>{/if}
    {#snippet actions()}
      <button type="button" class="btn" onclick={() => collisions && void addWith(collisions.at, collisions.plan, "skip")}>Skip those</button>
      <button type="button" class="btn" onclick={() => collisions && void addWith(collisions.at, collisions.plan, "keep-both")}>Keep both</button>
      <button type="button" class="btn" disabled={kindsDiffer} title={kindsDiffer ? "A file and a folder never replace one another" : undefined} onclick={() => collisions && void addWith(collisions.at, collisions.plan, "replace")}>Replace</button>
      <button type="button" class="btn" onclick={() => (collisions = null)}>Cancel</button>
    {/snippet}
  </Dialog>
{/if}

<!-- What a batch left out: the counts first — folders created and entered
     beside the files added — then each item with its own code's copy
     (APP.md §3, FileOutcome). It waits behind the questions of the same
     op — the conflicts, the refused names — which are answered first. -->
{#if results && !question && !refused}
  <Dialog title="What happened" onclose={() => (store.results = null)} dismissable>
    <p>{resultLine}</p>
    <ul class="plain">
      {#each resultItems.slice(0, 12) as t, i (i)}
        <li><b>{t.name}</b>{t.isDir ? " (folder)" : ""} — {t.text}</li>
      {/each}
      {#if resultItems.length > 12}<li>and {resultItems.length - 12} more.</li>{/if}
    </ul>
    {#snippet actions()}
      <button type="button" class="btn accent" onclick={() => (store.results = null)}>Close</button>
    {/snippet}
  </Dialog>
{/if}

<style>
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

  /* The preview's wait (APP.md §6, ruled 2026-10-14): a 14 px ring, 2 px,
     the accent, one turn a second — shown only after 300 ms, so a quick
     preview never flashes it. Under reduced motion it stands still. */
  .wait {
    width: 14px;
    height: 14px;
    border-radius: 50%;
    border: 2px solid var(--accent-line);
    border-top-color: var(--accent);
    opacity: 0;
    animation: wait-show 0s linear 300ms forwards, wait-turn 1s linear infinite;
  }
  @keyframes wait-show { to { opacity: 1; } }
  @keyframes wait-turn { to { transform: rotate(360deg); } }
  @media (prefers-reduced-motion: reduce) {
    .wait { animation: wait-show 0s linear 300ms forwards; }
  }
</style>

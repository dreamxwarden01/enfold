<script lang="ts">
  // The Archives page is the vault's inspector (APP.md §13): it lists
  // registry records, not files. Name carries the description as its
  // muted second line; Status carries what the row is — open, file
  // missing, hidden, forgotten — and *file missing* is only ever the
  // core's own note, so the cell is blank until a presence pass has run
  // and never says "missing" about a path nobody has measured. The header
  // line sums the rows shown and says so, beside the vault file's own size
  // and the registry's date (§2.1).
  import { untrack } from "svelte";
  import { Archives, Code, Shell, errorOf } from "../lib/api";
  import type { ArchiveSummary } from "../lib/api";
  import { store } from "../lib/state.svelte";
  import { archivesCopy, codeText, listCopy, warningCopy } from "../lib/strings";
  import { bytes, count, date, dateTime, plural } from "../lib/format";
  import { clickRow, emptySelection, selectAll, survive, toggleRow } from "../lib/selection";
  import type { Selection } from "../lib/selection";
  import { clickShownHeader, paneRow, shownTicked } from "../lib/archlist";
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
  const tampered = $derived(st?.tampered ?? false);
  // The record read whole, and only when it is the pane's: a reply for a
  // record the user has left is not this pane's (APP.md §13).
  const det = $derived(store.details && pane && store.details.archiveId === pane.id ? store.details : null);

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
  // The path is never a field: *Create* opens the save dialog, and a place
  // that already holds a file is refused here rather than overwritten
  // (APP.md §6). The refused name is kept, so the line goes the moment the
  // name is changed and never outlives what it is about.
  let newExists = $state("");
  const existsShown = $derived(!!newExists && newName.trim() === newExists);
  let confirm = $state<null | { title: string; body: string; button: string; run: () => Promise<unknown> }>(null);
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
  type SortKey = "name" | "size" | "files" | "saved" | "key" | "status";
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
    if (a.forgottenAt > 0) out.push({ label: "forgotten", tone: "forgotten", title: `Forgotten on ${date(a.forgottenAt)}` });
    // Nothing is "dirty" since 2026-09-09: an archive is clean between
    // operations, each of them its own transaction (APP.md §2.3).
    if (a.open) out.push({ label: "open", tone: "open" });
    // Never computed here: the core keeps a presence per record and the
    // note is the only thing that says a file is gone (APP.md §13).
    if (a.note === "archive.file_missing") out.push({ label: "file missing", tone: "missing", title: codeText(a.note) });
    if (a.hidden) out.push({ label: "hidden", tone: "hidden" });
    return out;
  }

  function statusRank(a: ArchiveSummary): number {
    if (a.forgottenAt > 0) return 5;
    if (a.note === "archive.file_missing") return 4;
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
        case "files":
          return dir * ((a.open ? a.files : -1) - (b.open ? b.files : -1));
        case "saved":
          return dir * (a.lastWrittenAt - b.lastWrittenAt);
        case "key":
          return dir * (a.keyVersion - b.keyVersion);
        case "status":
          return dir * (statusRank(a) - statusRank(b) || a.name.localeCompare(b.name));
      }
      return dir * a.name.localeCompare(b.name);
    });
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
    if (!(e.ctrlKey || e.metaKey) || e.altKey || e.shiftKey || (e.key !== "a" && e.key !== "A")) return;
    const t = e.target as HTMLElement | null;
    if (!t || t.closest("input, textarea, select, [contenteditable]")) return;
    if (t !== document.body && !listEl?.contains(t)) return;
    if (dialogIsUp()) return;
    e.preventDefault();
    sel = selectAll(sel, order);
  }

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
  $effect(() => {
    const id = paneId;
    untrack(() => void store.loadDetails(id));
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
  // standing; only a create that succeeded closes it.
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
      pick(id); // once the row is in the list, or the prune would drop the tick
      await store.refreshSettings(); // the folder this create remembered
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

  async function importRecords() {
    const p = (await Shell.PickFiles("Import records from a backup or a copy of this vault", false)) ?? [];
    if (p.length) merging = p[0];
  }

  // ---- Forget key… and Delete archive… -----------------------------------
  // Both close the archive first (APP.md §13); the core refuses either
  // with archive.busy while a preview reader is live, and a refusal on
  // the consent screen would offer neither the close nor a reason.
  function ask(mode: "delete" | "forget"): DeleteAsk | null {
    const a = one;
    if (!a) return null;
    return { id: a.id, name: a.name, path: a.path, createdAt: det?.createdAt ?? 0, mode };
  }

  async function startDelete(mode: "delete" | "forget") {
    const a = one;
    const snap = ask(mode);
    if (!a || !snap) return;
    if (a.open && !(await closeFirst(snap.id))) return;
    del = snap;
  }

  async function closeFirst(id: string): Promise<boolean> {
    try {
      await Archives.Close(id);
    } catch (e) {
      // An archive that has closed itself since the row was read — a page
      // left it, and nothing was holding it — is already where this wanted
      // it (APP.md §2.3).
      if (errorOf(e).code !== Code.CodeArchiveNotOpen) {
        fail(e);
        return false;
      }
    }
    // The Archive page may still be holding this one as its current
    // archive: it has no handle any more, so there is nothing to leave.
    if (store.current === id) store.leaveArchive(true);
    await store.refreshArchives();
    return true;
  }

  // A Delete archive… begun on the Archive page: the archive was closed
  // there, which left that page, and the dialog opens here (APP.md §13).
  // The record is read first, so the backup sentence has the created_at it
  // is chosen against.
  $effect(() => {
    const want = store.deleteAfterClose;
    if (!want) return;
    store.deleteAfterClose = null;
    pick(want);
    void (async () => {
      await store.loadDetails(want);
      const a = store.archives.find((x) => x.id === want);
      if (!a) return;
      // The Archive page left it, which closes it; a preview body still in
      // flight leaves it draining, and the record's own close is what ends
      // that before the consent screen (APP.md §2.3, §13).
      if (a.open && !(await closeFirst(want))) return;
      const d = store.details;
      del = { id: a.id, name: a.name, path: a.path, createdAt: d && d.archiveId === want ? d.createdAt : 0, mode: "delete" };
    })();
  });

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
        void store.openArchive(id);
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

  // What stops a record-level operation, said on the button itself. The
  // archive being open is never one of them since 2026-09-10: *Verify*,
  // *Compact* and *Rotate key* unwrap the key with the session's KWK, open
  // the file for the operation and close it again, so the page never asks
  // for the archive to be opened first (APP.md §2.3). Only the vault does:
  // while it is locked they are disabled with its own reason.
  const opReason = $derived(
    !unlocked
      ? codeText(Code.CodeVaultLocked)
      : tampered
        ? warningCopy(Code.CodeVaultTampered, st?.tamperedReason)
        : undefined,
  );

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
  <span class="chip num">{plural(rows.length, "archive")} shown · {bytes(shownSize)} stored</span>
  {#if st}<span class="t-quiet">vault file {bytes(st.vaultFileSize)} · changed {dateTime(st.modifiedAt)}</span>{/if}
  {#if !unlocked}<span class="chip warn"><svg class="i i-14"><use href="#i-lock" /></svg>Locked</span>{/if}
</div>

<div class="layer-body">
  <div class="cmdbar">
    <!-- Every action that takes one archive works on the pane's row and is
         greyed unless exactly one is ticked (APP.md §6, ruled 2026-09-10). -->
    <button type="button" class="btn accent" disabled={!one || (!unlocked && !one.open)} onclick={() => one && store.openArchive(one.id)}><svg class="i i-14"><use href="#i-open" /></svg>Open</button>
    <button type="button" class="btn" disabled={!unlocked || tampered} onclick={openCreate}><svg class="i i-14"><use href="#i-plus" /></svg>New archive</button>
    <div class="sep"></div>
    <!-- Never gated on the archive being open (APP.md §2.3, ruled
         2026-09-10): the core opens it for the operation and closes it
         again, so there is no "open the archive first". -->
    <button type="button" class="btn subtle" disabled={!one || !unlocked || tampered} title={opReason} onclick={() => one && (confirm = { title: "Compact this archive?", body: "Free space is reclaimed by rewriting the file. Nothing changes for the files inside; it takes time in proportion to the size.", button: "Compact", run: () => Archives.Compact(one.id) })}>Compact</button>
    <button type="button" class="btn subtle" disabled={!one || !unlocked || tampered} title={opReason} onclick={() => one && (confirm = { title: "Rotate this archive's key?", body: "A new key is wrapped into the vault first, then the archive is re-keyed. Old versions of the key stay readable.", button: "Rotate key", run: () => Archives.RotateKey(one.id) })}><svg class="i i-14"><use href="#i-rotate" /></svg>Rotate key</button>
    <button type="button" class="btn subtle" disabled={!one || !unlocked} title={!unlocked ? codeText(Code.CodeVaultLocked) : undefined} onclick={() => one && run(Archives.Verify(one.id))}><svg class="i i-14"><use href="#i-check" /></svg>Verify</button>
    <button type="button" class="btn subtle" disabled={!one || !unlocked} onclick={() => one && run(one.hidden ? Archives.Unhide(one.id) : Archives.Hide(one.id))}><svg class="i i-14"><use href="#i-eye" /></svg>{one?.hidden ? "Unhide" : "Hide"}</button>
    <div class="sep"></div>
    <button type="button" class="btn subtle" disabled={!unlocked} onclick={() => run(Archives.CheckFiles())}>Check files</button>
    <button type="button" class="btn subtle" disabled={!unlocked || tampered} onclick={importRecords}><svg class="i i-14"><use href="#i-backup" /></svg>Import records…</button>
    <div class="sep"></div>
    <button type="button" class="btn subtle danger" disabled={!one || !unlocked || tampered || one.forgottenAt > 0} onclick={() => void startDelete("forget")}>Forget key…</button>
    <button type="button" class="btn subtle danger" disabled={!one || !unlocked || tampered} onclick={() => void startDelete("delete")}><svg class="i i-14"><use href="#i-trash" /></svg>Delete archive…</button>
    <div class="grow"></div>
    <label class="filterbox"><span class="vh">Filter</span><input class="input" type="search" placeholder="Filter" bind:value={filter} /></label>
    <label class="check"><input type="checkbox" bind:checked={store.showHidden} onchange={() => store.refreshArchives()} />Show hidden</label>
  </div>

  {#if !unlocked}
    <div class="bar">
      <svg class="i i-14"><use href="#i-lock" /></svg>
      <span class="grow">The vault is locked. Open archives can still be browsed; everything else needs the vault.</span>
      <button type="button" class="btn sm accent" onclick={() => store.go("lock")}>Unlock</button>
    </div>
  {/if}
  {#if tampered}
    <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span>{warningCopy("vault.tampered", st?.tamperedReason)}</span></div>
  {/if}
  <OpsBar />

  <div class="arch-split">
    <div class="arch-list">
      <!-- A click on the list's blank area clears the ticks and the anchor
           (APP.md §6); a click that lands on a row is the row's, and a
           header cell sorts. -->
      <!-- svelte-ignore a11y_no_noninteractive_element_interactions, a11y_click_events_have_key_events -->
      <div class="tablewrap" role="region" aria-label={archivesCopy.listLabel} bind:this={listEl} onclick={blank}>
        {#if rows.length === 0}
          <div class="empty">{filter.trim() ? "No archive matches that." : unlocked ? "No archives yet. Create one, or open a file you already have." : "No archives are open."}</div>
        {:else}
          <!-- The same checkboxes as the file list's, under the same rules:
               the header ticks the rows the filter shows and is ticked
               exactly when all of them are, never a third state. -->
          <table>
            <colgroup><col class="w-check" /><col /><col class="w-size" /><col class="w-files" /><col class="w-date" /><col class="w-key" /><col class="w-status" /></colgroup>
            <thead>
              <tr>
                <th scope="col" class="chk"><input type="checkbox" checked={allTicked} aria-label={archivesCopy.tickAll} onclick={tickAll} /></th>
                <th scope="col" aria-sort={ariaSort("name")}><button type="button" onclick={() => sortOn("name")}>Name<svg class="i i-14 caret" class:on={sortKey === "name"} class:asc={sortAsc}><use href="#i-chevdown" /></svg></button></th>
                <th scope="col" aria-sort={ariaSort("size")}><button type="button" onclick={() => sortOn("size")}>Size<svg class="i i-14 caret" class:on={sortKey === "size"} class:asc={sortAsc}><use href="#i-chevdown" /></svg></button></th>
                <th scope="col" aria-sort={ariaSort("files")}><button type="button" onclick={() => sortOn("files")}>Files<svg class="i i-14 caret" class:on={sortKey === "files"} class:asc={sortAsc}><use href="#i-chevdown" /></svg></button></th>
                <th scope="col" aria-sort={ariaSort("saved")}><button type="button" onclick={() => sortOn("saved")}>Last saved<svg class="i i-14 caret" class:on={sortKey === "saved"} class:asc={sortAsc}><use href="#i-chevdown" /></svg></button></th>
                <th scope="col" aria-sort={ariaSort("key")}><button type="button" onclick={() => sortOn("key")}>Key<svg class="i i-14 caret" class:on={sortKey === "key"} class:asc={sortAsc}><use href="#i-chevdown" /></svg></button></th>
                <th scope="col" aria-sort={ariaSort("status")}><button type="button" onclick={() => sortOn("status")}>Status<svg class="i i-14 caret" class:on={sortKey === "status"} class:asc={sortAsc}><use href="#i-chevdown" /></svg></button></th>
              </tr>
            </thead>
            <tbody>
              {#each rows as a, i (a.id)}
                <!-- svelte-ignore a11y_no_noninteractive_tabindex a11y_no_noninteractive_element_interactions -->
                <tr tabindex={paneId === a.id || (!paneId && i === 0) ? 0 : -1} aria-selected={sel.ids.has(a.id)} class:forgotten={a.forgottenAt > 0} onclick={(e) => click(e, a.id)} ondblclick={() => store.openArchive(a.id)} onkeydown={(e) => rowKey(e, a.id)}>
                  <td class="chk"><input type="checkbox" checked={sel.ids.has(a.id)} tabindex="-1" aria-label={listCopy.tickRow(a.name)} onclick={(e) => tick1(e, a.id)} ondblclick={(e) => e.stopPropagation()} /></td>
                  <td class="sel-mark">
                    <div class="fname">
                      <svg class="i i-14"><use href="#i-box" /></svg>
                      <div class="two-line"><b>{a.name}</b><small title={a.description}>{a.description || ""}</small></div>
                    </div>
                  </td>
                  <td class="num">{a.storedSize ? bytes(a.storedSize) : "—"}</td>
                  <td class="num">{a.open ? count(a.files) : "—"}</td>
                  <td class="num">{dateTime(a.lastWrittenAt)}</td>
                  <td><span class="chip key">v{a.keyVersion}</span></td>
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
      </div>
    </div>

    <!-- The pane shows the row last clicked while it is ticked, else the
         sole ticked row, else nothing but a count; its buttons take one
         archive and are greyed unless exactly one is ticked (APP.md §6). -->
    <aside class="details" aria-label="Selected archive">
      {#if pane}
        <div class="details-head"><div class="eyebrow">Selected</div><h3>{pane.name}</h3></div>

        {#if purge}
          <div class="pad">
            <div class="bar attention">
              <svg class="i i-14"><use href="#i-warn" /></svg>
              <div class="why">
                <span>Forgotten — restore it to open this archive again; its key is dropped at the first unlock after {date(purge)}.</span>
                <button type="button" class="btn sm" disabled={!one || !unlocked || tampered} onclick={() => one && run(Archives.Restore(one.id)).then(() => store.refreshArchives())}>Restore</button>
              </div>
            </div>
          </div>
        {/if}

        {#if draft}
          <div class="pad fields">
            <div class="field">
              <div class="field-top"><label for="ar-name">Name</label></div>
              <input id="ar-name" class="input" class:invalid={nameChanged && !!nameProblem(draft.name)} aria-invalid={nameChanged && nameProblem(draft.name) ? "true" : undefined} bind:value={store.recordDraft[pane.id].name} disabled={!editable} />
            </div>
            <div class="field">
              <div class="field-top"><label for="ar-desc">Description</label><span class="hint">a second line in the list</span></div>
              <textarea id="ar-desc" class="input area" class:invalid={descChanged && !!descriptionProblem(draft.description)} aria-invalid={descChanged && descriptionProblem(draft.description) ? "true" : undefined} rows="2" bind:value={store.recordDraft[pane.id].description} disabled={!editable}></textarea>
            </div>
          </div>
        {/if}

        <dl class="facts">
          <div class="fact"><dt>Size</dt><dd>{pane.storedSize ? bytes(pane.storedSize) : "—"}</dd></div>
          {#if pane.open}<div class="fact"><dt>Files</dt><dd>{count(pane.files)}</dd></div>{/if}
          <div class="fact"><dt>Created</dt><dd>{det ? dateTime(det.createdAt) : "—"}</dd></div>
          <div class="fact"><dt>Last saved</dt><dd>{dateTime(pane.lastWrittenAt)}</dd></div>
          <div class="fact"><dt>Key version</dt><dd>v{pane.keyVersion}</dd></div>
          <div class="fact"><dt>KID</dt><dd class="mono" title={det?.currentKid}>{det?.currentKid || "—"}</dd></div>
          <div class="fact"><dt>Compression</dt><dd>{methodWord(pane.method)}</dd></div>
          {#if pane.receiptOwed}<div class="fact"><dt>Receipt</dt><dd>pending</dd></div>{/if}
          {#if pane.hashBehind > 0}<div class="fact"><dt>Verified</dt><dd>{plural(pane.hashBehind, "save")} ago</dd></div>{/if}
          <!-- The figure, not advice: the core reclaims the space itself
               after a commit that leaves it over APP.md §2.3's thresholds,
               so "compact when convenient" would tell the user to do what
               has already been done. What is left below the quarter is
               why the file is bigger than the files inside it, and the
               same floor decides whether it is worth saying (lib/status.ts). -->
          {#if pane.open && freeWorthShowing(pane.freeSpace)}<div class="fact"><dt>Free space</dt><dd>{bytes(pane.freeSpace)}</dd></div>{/if}
        </dl>

        <div class="pad pathblock">
          <div class="plabel">{foreign ? "Last seen on another system at" : "File"}</div>
          <div class="ppath" title={pane.path}>{pane.path || "—"}</div>
          <div class="row">
            {#if !foreign}<button type="button" class="btn link" disabled={!one} onclick={() => one && void Shell.Reveal(one.path)}>Show in Explorer</button>{/if}
            <button type="button" class="btn link" disabled={!one || !unlocked} onclick={() => one && locate(one)}>Locate…</button>
          </div>
        </div>

        {#if pane.note === "archive.file_missing"}
          <div class="pad"><div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span class="grow">{codeText(pane.note)}</span></div></div>
        {/if}

        <div class="details-actions">
          <button type="button" class="btn accent wide" disabled={!one || (!unlocked && !one.open)} onclick={() => one && store.openArchive(one.id)}><svg class="i i-14"><use href="#i-open" /></svg>Open</button>
          <div class="row2">
            {#if pane.open}<button type="button" class="btn" disabled={!one} onclick={() => one && run(Archives.Close(one.id)).then(() => store.refreshArchives())}>Close</button>{/if}
            <button type="button" class="btn" disabled={!det || !one} onclick={() => (showDetails = true)}>Details…</button>
          </div>
        </div>

        {#if pending.length > 0}
          <SaveBar items={pending} {invalid} busy={saving} onsave={() => void saveRecord()} ondiscard={() => pane && stage(pane)} />
        {/if}
      {:else}
        <div class="empty">{sel.ids.size > 1 ? archivesCopy.selectedCount(sel.ids.size) : archivesCopy.selectOne}</div>
      {/if}
    </aside>
  </div>
</div>


{#if creating}
  <Dialog title="New archive" onclose={() => (creating = false)}>
    <!-- judged by the rename's rule too: a name over the bound is said
         here, before a file is made for it (APP.md §6). -->
    <TextField id="na-name" label="Name" bind:value={newName} bind:valid={newNameValid} attempt={newAttempt} judge={nameProblem} placeholder="Photos 2026" />
    <div class="field">
      <div class="field-top"><span class="lbl">Compression</span></div>
      <Segmented id="na-method" label="Compression" options={methodOptions} bind:value={newMethod} />
      <div class="pin-note">{METHOD_NOTE}</div>
    </div>
    {#if existsShown}
      <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span>{codeText(Code.CodeArchiveExists)}</span></div>
    {/if}
    <p><em>Create</em> asks where to keep it, with the name above and <span class="mono">.efd</span> already filled in.</p>
    {#snippet actions()}
      <button type="button" class="btn" onclick={() => (creating = false)}>Cancel</button>
      <button type="button" class="btn accent" onclick={create}>Create…</button>
    {/snippet}
  </Dialog>
{/if}

{#if confirm}
  <Dialog title={confirm.title} onclose={() => (confirm = null)}>
    <p>{confirm.body}</p>
    {#snippet actions()}
      <button type="button" class="btn" onclick={() => (confirm = null)}>Cancel</button>
      <button type="button" class="btn accent" onclick={() => { const c = confirm; confirm = null; if (c) void run(c.run()); }}>{confirm?.button}</button>
    {/snippet}
  </Dialog>
{/if}

{#if showDetails && det}
  <ArchiveDetailsDialog d={det} onclose={() => (showDetails = false)} />
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
  />
{/if}

{#if merging}
  <MergeDialog path={merging} onclose={() => (merging = "")} />
{/if}

<style>
  .w-size { width: 84px; }
  .w-files { width: 70px; }
  .w-date { width: 130px; }
  .w-key { width: 56px; }
  .w-status { width: 148px; }
  .lbl { font-size: 12.5px; font-weight: 600; color: var(--ink); }
  .pad { padding: 0 16px 12px; }
  .fields { display: flex; flex-direction: column; gap: 10px; padding-top: 12px; }
  .area { height: auto; min-height: 52px; padding: 7px 10px; resize: vertical; line-height: 1.35; }
  .pathblock { display: flex; flex-direction: column; gap: 4px; }
  .plabel { color: var(--ink-2); font-size: 12px; }
  .ppath { font-size: 12px; color: var(--ink); overflow-wrap: anywhere; user-select: text; }
  .mono { font-family: var(--font-mono); font-size: 11.5px; }
  .why { display: flex; flex-direction: column; align-items: flex-start; gap: 6px; min-width: 0; }
  tbody tr.forgotten .two-line b { color: var(--ink-3); }
  .vh { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; }
</style>

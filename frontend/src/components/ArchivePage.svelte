<script lang="ts">
  import { Archive, Archives, Shell, errorOf } from "../lib/api";
  import type { Collision, FileRow } from "../lib/api";
  import { store } from "../lib/state.svelte";
  import { codeText } from "../lib/strings";
  import { bytes, countdown, dateTime, previewKind, storageLabel } from "../lib/format";
  import type { PreviewKind } from "../lib/format";
  import { statusNote } from "../lib/status";
  import { fileNameProblem, newNameProblem } from "../lib/validate";
  import { ROOT_ID, canDrop, countPhrase, deleteBody, deleteCounts, deleteTitle } from "../lib/tree";
  import type { DragState, DropTarget } from "../lib/tree";
  import { summaryLine, tally, troubles } from "../lib/results";
  import Dialog from "./Dialog.svelte";
  import ExtractDialog from "./ExtractDialog.svelte";
  import MenuButton from "./MenuButton.svelte";
  import type { MenuItem } from "./MenuButton.svelte";
  import OpsBar from "./OpsBar.svelte";
  import TextField from "./TextField.svelte";

  const id = $derived(store.current ?? "");
  const stat = $derived(store.stat);
  // The index is a tree (APP.md §3): a row is a record with an id, the
  // folder shown is a directory id, and the breadcrumb is the page's own
  // Crumbs — root-inclusive, its first entry the archive's name, its last
  // the folder shown — drawn from nothing else.
  const rows = $derived(store.page?.rows ?? []);
  const crumbs = $derived(store.page?.crumbs ?? []);
  const dirId = $derived(store.dirId);
  // Everything on this page except *Delete archive...* stays usable after
  // a lock (APP.md §2.3): Page, Stat, the previews, Extract and every
  // operation - each committing the archive and owing its receipt until
  // the vault comes back. Only the registry write needs the session.
  const tampered = $derived(store.status?.tampered ?? false);

  let selected = $state<Set<string>>(new Set());
  let anchor = $state<string | null>(null);
  const one = $derived(selected.size === 1 ? rows.find((r) => r.id === [...selected][0]) ?? null : null);
  // The selection may hold folders as well as files (APP.md §6): a folder
  // is extracted, deleted and renamed like anything else.
  const chosen = $derived(rows.filter((r) => selected.has(r.id)));

  let previewUrl = $state("");
  let previewText = $state<{ text: string; truncated: boolean } | null>(null);
  let kind = $state<PreviewKind>("none");

  let renaming = $state<FileRow | null>(null);
  let renameTo = $state("");
  let creating = $state(false);
  let newFolder = $state("");
  let newFolderValid = $state(true);
  let newFolderAttempt = $state(0);
  // The names this folder already holds, so *Create folder* cannot make a
  // second row of one name. Every row on the page is a committed record
  // now, so every one of them holds its name (FORMAT.md R39).
  const taken = $derived(rows.map((r) => r.name));
  const judgeFolder = $derived((v: string) => newNameProblem(v, taken));
  let deleting = $state<FileRow[]>([]);
  let extracting = $state<{ ids: string[]; label: string } | null>(null);
  let collisions = $state<{ at: string; plan: AddPlan; list: Collision[] } | null>(null);
  const kindsDiffer = $derived((collisions?.list ?? []).some((c) => c.isDir !== c.existingIsDir));
  let dropping = $state(false);

  function fail(e: unknown) {
    store.toast(codeText(errorOf(e).code), "error");
  }

  function fileIcon(r: FileRow): string {
    if (r.isDir) return "i-folder";
    switch (previewKind(r.name)) {
      case "image": return "i-image";
      case "video": return "i-video";
      case "audio": return "i-audio";
      case "text": return "i-doc";
    }
    return "i-file";
  }

  function click(e: MouseEvent, r: FileRow) {
    moveError = null;
    if (e.ctrlKey) {
      const s = new Set(selected);
      if (s.has(r.id)) s.delete(r.id); else s.add(r.id);
      selected = s;
    } else if (e.shiftKey && anchor) {
      const keys = rows.map((x) => x.id);
      const a = keys.indexOf(anchor), b = keys.indexOf(r.id);
      if (a >= 0 && b >= 0) selected = new Set(keys.slice(Math.min(a, b), Math.max(a, b) + 1));
    } else {
      selected = new Set([r.id]);
      anchor = r.id;
    }
  }

  // The keyboard path: Space selects, Enter selects and opens, the arrows
  // move the focus between rows.
  function keydown(e: KeyboardEvent, r: FileRow) {
    const el = e.currentTarget as HTMLElement;
    switch (e.key) {
      case " ":
        e.preventDefault();
        selected = new Set([r.id]);
        anchor = r.id;
        break;
      case "Enter":
        e.preventDefault();
        selected = new Set([r.id]);
        anchor = r.id;
        open(r);
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

  // The list's blank area: everything in the pane that is neither a row
  // nor the header — a header is a control of the list, not its blank
  // area, and sorting from one must not drop the selection. The row's own
  // click has already run by the time this one does.
  function blank(e: MouseEvent) {
    const t = e.target as HTMLElement;
    if (!t.closest("tbody tr") && !t.closest("thead")) {
      selected = new Set();
      // The one gesture that means "I am done with that": a refusal
      // standing against a target nothing is aimed at goes with it.
      moveError = null;
    }
  }

  // Entering a folder is its id, never its name (APP.md §3).
  function open(r: FileRow) {
    if (!r.isDir) return;
    selected = new Set();
    void store.enterDir(r.id, r.name);
  }

  // The preview follows the single selection. Every row is committed - an
  // operation commits at its end (APP.md §2.3) - so nothing here waits
  // for a save.
  $effect(() => {
    const r = one;
    previewUrl = "";
    previewText = null;
    kind = "none";
    if (!r || r.isDir) return;
    const k = previewKind(r.name);
    kind = k;
    if (k === "text") {
      Archive.PreviewText(id, r.id, 64 * 1024).then((t) => { if (one === r) previewText = t; }).catch(fail);
    } else if (k !== "none") {
      Archive.PreviewURL(id, r.id).then((u) => { if (one === r) previewUrl = u; }).catch(fail);
    }
  });

  // A change of folder or of archive starts with nothing selected and no
  // refusal standing against a target that is no longer on screen.
  $effect(() => {
    void id;
    void dirId;
    selected = new Set();
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
  const addItems: MenuItem[] = $derived([
    { label: "Add files", icon: "i-plus", run: () => void pickFiles() },
    { label: "Add folder", icon: "i-folder-add", run: () => void pickFolder() },
    { label: "Create folder", icon: "i-folder", run: askFolder },
  ]);

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

  // A drag of the selection onto a folder row or a crumb is a Move
  // (APP.md §6). It is refused whole and in place — nothing is staged when
  // any item fails — so the reason is shown against the target that was
  // aimed at, and the selection stays where it was.
  let drag = $state<DragState | null>(null);
  let dropTarget = $state<string | null>(null);
  let moveError = $state<{ id: string; text: string } | null>(null);

  function dragStart(e: DragEvent, r: FileRow) {
    // Dragging a row outside the selection takes that row alone, the way
    // a file manager does.
    const ids = selected.has(r.id) ? chosen.map((x) => x.id) : [r.id];
    if (ids.length === 0) {
      e.preventDefault();
      return;
    }
    drag = { ids, from: dirId };
    moveError = null;
    e.dataTransfer?.setData("text/plain", ids.join(" "));
    if (e.dataTransfer) e.dataTransfer.effectAllowed = "move";
  }

  function dragEnd() {
    drag = null;
    dropTarget = null;
  }

  function over(e: DragEvent, t: DropTarget) {
    if (!canDrop(t, drag)) return;
    e.preventDefault();
    if (e.dataTransfer) e.dataTransfer.dropEffect = "move";
    dropTarget = t.id;
  }

  function leave(t: DropTarget) {
    if (dropTarget === t.id) dropTarget = null;
  }

  async function dropOn(e: DragEvent, t: DropTarget) {
    if (!drag) return;
    e.preventDefault();
    e.stopPropagation();
    const d = drag;
    drag = null;
    dropTarget = null;
    // A drop onto the folder the rows are already in, onto a row staged
    // for deletion, or onto a file is a no-op: nothing is sent.
    if (!canDrop(t, d)) return;
    try {
      await Archive.Move(id, d.ids, t.id);
      moveError = null;
      selected = new Set();
      await store.refreshArchive();
    } catch (err) {
      moveError = { id: t.id, text: codeText(errorOf(err).code) };
    }
  }

  async function doRename() {
    const r = renaming;
    if (!r) return;
    try {
      await Archive.Rename(id, r.id, renameTo);
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
  // §3, FORMAT.md R32).
  async function doDelete() {
    const ids = deleting.map((r) => r.id);
    deleting = [];
    try {
      await Archive.Delete(id, ids);
      selected = new Set();
      await store.refreshArchive();
    } catch (e) {
      fail(e);
    }
  }

  // Both ways in open the same dialog (APP.md §3): the destination lives
  // in it, prefilled from the folder last extracted to, rather than in a
  // native picker the page opens first.
  function startExtract(ids: string[], label: string) {
    if (ids.length === 0) return;
    extracting = { ids, label };
  }

  // *Extract all* is the archive's, not the selection's (APP.md §6): the
  // root id alone means everything, so the page sends that one id and
  // walks nothing.
  function extractAll() {
    void startExtract([ROOT_ID], "everything in this archive");
  }

  async function doExtract(dir: string, policy: string) {
    const x = extracting;
    extracting = null;
    if (!x) return;
    try {
      await Archive.Extract(id, x.ids, dir, policy);
    } catch (e) {
      fail(e);
    }
  }

  async function close() {
    try {
      await Archives.Close(id);
      store.leaveArchive();
      await store.refreshArchives();
    } catch (e) {
      fail(e);
    }
  }

  // Delete archive… is the same act as the Archives page's, on the archive
  // this page shows (APP.md §13): the archive is closed first and, since
  // closing leaves this page, the id is handed to the Archives page, which
  // opens the one dialog. It is disabled while the vault is locked with
  // the archive open (§2.3): the registry write needs the session's key.
  // Nothing is asked about unfinished changes — there are none between
  // operations — and a running one is cancelled by the close.
  async function closeThenDelete() {
    const archiveId = id;
    try {
      await Archives.Close(archiveId);
    } catch (e) {
      fail(e);
      return;
    }
    store.deleteAfterClose = archiveId;
    store.leaveArchive();
    await store.refreshArchives();
  }

  // What a batch reported when something was left out (lib/results.ts):
  // the counts — folders created and entered beside the files added — and
  // then the items themselves, each with its own code's copy.
  const results = $derived(store.results);
  const resultLine = $derived(summaryLine(tally(results?.results)));
  const resultItems = $derived(troubles(results?.results));

  // The foot's note (LayerFoot, lib/status.ts): the archive's figures —
  // singular at one, and the free space when it is worth knowing — and,
  // while the vault is locked and the archive still open, when it closes.
  $effect(() => {
    let note = statusNote(stat);
    if (!store.unlocked && stat?.expiresAt) note += ` · archive closes in ${countdown(stat.expiresAt, store.now)}`;
    store.footNote = note;
  });
</script>

<div class="layer-head">
  <!-- The breadcrumb is Page's Crumbs and nothing else: root-inclusive,
       its first entry the archive's own name, its last the folder shown
       (APP.md §3). Each crumb is a drop target, so a drag can move a
       selection up the tree. -->
  <nav class="crumbs" aria-label="Breadcrumb">
    <button type="button" class="c" onclick={() => { store.leaveArchive(); }}>Archives</button>
    {#each crumbs as c, i (c.id)}
      <svg class="i"><use href="#i-chevron" /></svg>
      <button
        type="button"
        class="c"
        class:here={i === crumbs.length - 1}
        class:drop-into={dropTarget === c.id}
        onclick={() => void store.enterDir(c.id, c.name)}
        ondragover={(e) => over(e, { id: c.id, isDir: true })}
        ondragleave={() => leave({ id: c.id, isDir: true })}
        ondrop={(e) => void dropOn(e, { id: c.id, isDir: true })}
      >{c.name}</button>
    {/each}
  </nav>
  <div class="grow"></div>
  <button type="button" class="btn sm" onclick={close}>Close archive</button>
</div>

<div class="layer-body">
  {#if moveError && crumbs.some((c) => c.id === moveError?.id)}
    <div class="move-note">Not moved: {moveError.text}</div>
  {/if}
  <div class="cmdbar">
    <MenuButton id="add" label="Add" icon="i-plus" items={addItems} />
    <!-- Greyed on Records, never on Files: a record count is the only
         thing that can say whether the tree holds anything, since a
         folder occupies no data region and an archive of folders alone
         reports 0 files while holding real records (APP.md §3). -->
    <button type="button" class="btn subtle" disabled={(stat?.records ?? 0) === 0} onclick={extractAll}><svg class="i i-14"><use href="#i-extract" /></svg>Extract all</button>
    <button type="button" class="btn subtle" disabled={!one} onclick={() => { if (one) { renaming = one; renameTo = one.name; } }}><svg class="i i-14"><use href="#i-rename" /></svg>Rename</button>
    <button type="button" class="btn subtle danger" disabled={chosen.length === 0} onclick={() => (deleting = chosen)}><svg class="i i-14"><use href="#i-trash" /></svg>Delete</button>
    <div class="grow"></div>
    <!-- Tampered disables it here as it does on the Archives page (APP.md
         §13, R25): the flow closes the archive before the write is even
         attempted, so a refusal at the end would have shut the user's
         archive for nothing. -->
    <button type="button" class="btn subtle danger" disabled={!store.unlocked || tampered} title={!store.unlocked ? "Unlock the vault first: the record is the vault's." : tampered ? "The vault's slot region does not verify; every change is disabled." : undefined} onclick={() => void closeThenDelete()}><svg class="i i-14"><use href="#i-trash" /></svg>Delete archive…</button>
  </div>

  {#if !store.unlocked}
    <div class="bar">
      <svg class="i i-14"><use href="#i-lock" /></svg>
      <span class="grow">The vault is locked. This archive stays open until it goes idle — you can browse it, change it and extract from it; what you change is recorded in the vault at the next unlock.</span>
      <button type="button" class="btn sm accent" onclick={() => store.go("lock")}>Unlock</button>
    </div>
  {/if}
  {#if stat?.state === "needs_reopen"}
    <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span class="grow">{codeText("archive.needs_reopen")}</span><button type="button" class="btn sm" onclick={close}>Close and reopen</button></div>
  {/if}
  {#if stat?.copyMismatch}
    <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span>{codeText("archive.copy_mismatch")}</span></div>
  {/if}
  <!-- The operation strip and nothing else: no pending bar, no Save, no
       Discard, since every operation commits at its end (APP.md §2.3). -->
  <OpsBar />

  <div class="file-split" class:dropping>
    <!-- A click on the list's blank area — under the last row, or in the
         container beside the table — clears the selection (APP.md §6); a
         click that lands on a row is the row's. The drop target carries
         the directory id the page is showing, never a name: a stale id is
         refused, where a stale path would resolve to whatever folder now
         happens to carry that name (§3). -->
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions, a11y_click_events_have_key_events -->
    <div class="tablewrap" data-file-drop-target="true" data-archive-id={id} data-dir-id={dirId}
      role="region" aria-label="Files" onclick={blank}
      ondragenter={() => { if (!drag) dropping = true; }} ondragleave={() => (dropping = false)} ondrop={() => (dropping = false)}>
      {#if rows.length === 0}
        <div class="empty">{crumbs.length > 1 ? "This folder is empty." : "Nothing here yet. Add files, or drop them here."}</div>
      {:else}
        <table>
          <colgroup><col /><col class="w-size" /><col class="w-store" /><col class="w-date" /></colgroup>
          <thead><tr><th scope="col">Name</th><th scope="col">Size</th><th scope="col">Stored as</th><th scope="col">Modified</th></tr></thead>
          <tbody>
            {#each rows as r, i (r.id)}
              <!-- svelte-ignore a11y_no_noninteractive_tabindex a11y_no_noninteractive_element_interactions -->
              <tr
                tabindex={selected.has(r.id) || (selected.size === 0 && i === 0) ? 0 : -1}
                aria-selected={selected.has(r.id)}
                class:drop-into={dropTarget === r.id}
                class:refused={moveError?.id === r.id}
                draggable={true}
                onclick={(e) => click(e, r)}
                ondblclick={() => open(r)}
                onkeydown={(e) => keydown(e, r)}
                ondragstart={(e) => dragStart(e, r)}
                ondragend={dragEnd}
                ondragover={(e) => over(e, { id: r.id, isDir: r.isDir })}
                ondragleave={() => leave({ id: r.id, isDir: r.isDir })}
                ondrop={(e) => void dropOn(e, { id: r.id, isDir: r.isDir })}
              >
                <td class="sel-mark">
                  <div class="fname">
                    <svg class="i i-14"><use href="#{fileIcon(r)}" /></svg>
                    <span>{r.name}</span>
                  </div>
                  {#if moveError && moveError.id === r.id}<span class="move-note">Not moved: {moveError.text}</span>{/if}
                </td>
                <!-- A directory's Size is the sum beneath it and its
                     Modified the record's own; it is stored as nothing,
                     being a record and not content (APP.md §3). -->
                <td class="num">{bytes(r.size)}</td>
                <!-- The column is fixed at what "zstd, 79% smaller" needs
                     (APP.md §6), so a longer label — a dictionary's —
                     ellipsizes; the cell carries the whole of it, so a
                     hover reads the part that was cut. -->
                <td title={r.isDir ? undefined : storageLabel(r.storage, r.savedPercent)}>{r.isDir ? "" : storageLabel(r.storage, r.savedPercent)}</td>
                <td class="num">{dateTime(r.modifiedAt)}</td>
              </tr>
            {/each}
          </tbody>
        </table>
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
        {:else if one && one.isDir}
          <span class="none">Folder · {bytes(one.size)} inside</span>
        {:else if one}
          <span class="none">No preview for this type — extract it</span>
        {:else}
          <span class="none">{selected.size > 1 ? `${selected.size} selected` : "Select a file"}</span>
        {/if}
      </div>
      {#if one}
        <div class="pname"><svg class="i i-14"><use href="#{fileIcon(one)}" /></svg><span>{one.name}</span></div>
        <dl class="facts">
          <div class="fact"><dt>{one.isDir ? "Size beneath" : "Size"}</dt><dd>{bytes(one.size)}</dd></div>
          {#if !one.isDir}
            <div class="fact"><dt>Stored as</dt><dd>{storageLabel(one.storage, one.savedPercent)}</dd></div>
          {/if}
          <div class="fact"><dt>Modified</dt><dd>{dateTime(one.modifiedAt)}</dd></div>
          <div class="fact"><dt>Path</dt><dd title={one.path}>{one.path}</dd></div>
        </dl>
      {/if}
      <div class="preview-actions">
        <button type="button" class="btn accent" disabled={chosen.length === 0} onclick={() => startExtract(chosen.map((r) => r.id), countPhrase(deleteCounts(chosen)))}>Extract…</button>
      </div>
    </aside>
  </div>
</div>


{#if renaming}
  <Dialog title="Rename" onclose={() => (renaming = null)}>
    <div class="field"><div class="field-top"><label for="rn">New name</label></div><input id="rn" class="input" bind:value={renameTo} /></div>
    {#snippet actions()}
      <button type="button" class="btn" onclick={() => (renaming = null)}>Cancel</button>
      <button type="button" class="btn accent" disabled={!!fileNameProblem(renameTo)} onclick={doRename}>Rename</button>
    {/snippet}
  </Dialog>
{/if}

<!-- Create folder (APP.md §6): the name is judged by the rename's own
     rule, and against the names this folder already holds. The button is
     never disabled for an empty field — pressing it marks the field. -->
{#if creating}
  <Dialog title="Create folder" onclose={() => (creating = false)}>
    <TextField id="cf-name" label="Name" bind:value={newFolder} bind:valid={newFolderValid} attempt={newFolderAttempt} judge={judgeFolder} placeholder="Receipts" />
    <p>A folder is a record of its own: it is written now, and it stays whether or not anything is put into it.</p>
    {#snippet actions()}
      <button type="button" class="btn" onclick={() => (creating = false)}>Cancel</button>
      <button type="button" class="btn accent" onclick={makeFolder}>Create</button>
    {/snippet}
  </Dialog>
{/if}

<!-- The delete dialog (APP.md §3, §6): files and folders counted apart,
     the folder sentence only when a folder is in the selection, and no
     undo anywhere in the copy - deletion is cryptographic erasure. -->
{#if deleting.length > 0}
  <Dialog title={deleteTitle(deleting, stat?.name ?? "")} onclose={() => (deleting = [])}>
    <p>{deleteBody(deleting)}</p>
    {#snippet actions()}
      <button type="button" class="btn" onclick={() => (deleting = [])}>Cancel</button>
      <button type="button" class="btn accent danger" onclick={doDelete}>Delete</button>
    {/snippet}
  </Dialog>
{/if}

{#if extracting}
  <ExtractDialog
    label={extracting.label}
    archiveName={stat?.name ?? ""}
    initial={store.settings?.lastExtractFolder ?? ""}
    onextract={(dir, policy) => void doExtract(dir, policy)}
    oncancel={() => (extracting = null)}
  />
{/if}

<!-- The collision dialog says the kind on both sides — "Photos is a file
     here" — and greys *Replace* whenever the two differ: kinds that differ
     never replace one another, in either direction (APP.md §3). -->
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
      <button type="button" class="btn" onclick={() => (collisions = null)}>Cancel</button>
      <button type="button" class="btn" onclick={() => collisions && void addWith(collisions.at, collisions.plan, "skip")}>Skip those</button>
      <button type="button" class="btn" onclick={() => collisions && void addWith(collisions.at, collisions.plan, "keep-both")}>Keep both</button>
      <button type="button" class="btn accent" disabled={kindsDiffer} onclick={() => collisions && void addWith(collisions.at, collisions.plan, "replace")}>Replace</button>
    {/snippet}
  </Dialog>
{/if}

<!-- What a batch left out: the counts first — folders created and entered
     beside the files added — then each item with its own code's copy
     (APP.md §3, FileOutcome). -->
{#if results}
  <Dialog title="What happened" onclose={() => (store.results = null)}>
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

<script lang="ts">
  import { untrack } from "svelte";
  import { Archive, Archives, Shell, errorOf } from "../lib/api";
  import type { FileRow } from "../lib/api";
  import { store } from "../lib/state.svelte";
  import { codeText } from "../lib/strings";
  import { bytes, count, countdown, dateTime, previewKind, storageLabel } from "../lib/format";
  import type { PreviewKind } from "../lib/format";
  import { fileNameProblem } from "../lib/validate";
  import { addPending, createProblem, dropTo, leafOf, parentOf, pendingIn, reconcile } from "../lib/pending";
  import Dialog from "./Dialog.svelte";
  import MenuButton from "./MenuButton.svelte";
  import type { MenuItem } from "./MenuButton.svelte";
  import OpsBar from "./OpsBar.svelte";
  import TextField from "./TextField.svelte";

  const id = $derived(store.current ?? "");
  const stat = $derived(store.stat);
  const saved = $derived(store.page?.rows ?? []);
  const crumbs = $derived(store.folder ? store.folder.split("/") : []);
  const alive = $derived(stat?.sessionAlive ?? false);
  const dirty = $derived(stat?.dirty ?? 0);
  const tampered = $derived(store.status?.tampered ?? false);

  // Folders created here (APP.md §6, lib/pending.ts). The format keeps
  // paths, not folders, so a created folder is a row of this page and
  // nothing else until a file is added into it; then the archive lists it
  // itself and the page lets go of it. Kept per archive: the list is
  // dropped when another archive is opened, at Save and at Discard.
  let pending = $state<string[]>([]);
  const realFolders = $derived(saved.filter((r) => r.isFolder).map((r) => r.name));
  const live = $derived(reconcile(pending, store.folder, realFolders));
  const rows = $derived([...pendingIn(live, store.folder).map(folderRow), ...saved]);

  // A created folder's row: a folder with nothing in it, marked pending
  // the way an added file is.
  function folderRow(path: string): FileRow {
    return { fileId: "", path, name: leafOf(path), size: 0, storage: "", savedPercent: 0, modifiedAt: 0, isFolder: true, files: 0, pending: "added" };
  }

  // A listing that names a folder the page created is the archive taking
  // it over: the page's copy goes, so the folder is one row, not two.
  $effect(() => {
    void store.folder;
    void realFolders;
    const next = untrack(() => reconcile(pending, store.folder, realFolders));
    if (next.length !== untrack(() => pending).length) pending = next;
  });

  // Rows are keyed by kind and path: folder rows carry no file id.
  function rowKey(r: FileRow): string {
    return (r.isFolder ? "d:" : "f:") + r.path;
  }
  let selected = $state<Set<string>>(new Set());
  let anchor = $state<string | null>(null);
  const one = $derived(selected.size === 1 ? rows.find((r) => rowKey(r) === [...selected][0]) ?? null : null);

  let previewUrl = $state("");
  let previewText = $state<{ text: string; truncated: boolean } | null>(null);
  let kind = $state<PreviewKind>("none");

  let renaming = $state<FileRow | null>(null);
  let renameTo = $state("");
  let creating = $state(false);
  let newFolder = $state("");
  let newFolderValid = $state(true);
  let newFolderAttempt = $state(0);
  // The names this folder already holds — its rows, the created ones
  // among them — so that Create folder cannot make a second row of one
  // folder (lib/pending.ts). These are the rows the listing loaded (the
  // store asks for 2000): a name that exists only beyond them is not
  // seen. The core has no check to borrow — CheckNames matches a file of
  // that exact name, and a folder is a prefix, not a record — so the
  // page's rule is the rows it shows, and a folder past the page's limit
  // is the listing's own limit, not this rule's.
  const taken = $derived(rows.map((r) => r.name));
  const judgeFolder = $derived((v: string) => createProblem(v, taken));
  let deleting = $state<FileRow[]>([]);
  let extracting = $state<FileRow[]>([]);
  let extractDir = $state("");
  let extractPolicy = $state<"skip" | "rename">("skip");
  let collisions = $state<{ paths: string[]; names: string[]; dir: boolean } | null>(null);
  let dropping = $state(false);

  function fail(e: unknown) {
    store.toast(codeText(errorOf(e).code), "error");
  }

  function fileIcon(r: FileRow): string {
    if (r.isFolder) return "i-folder";
    switch (previewKind(r.name)) {
      case "image": return "i-image";
      case "video": return "i-video";
      case "audio": return "i-audio";
      case "text": return "i-doc";
    }
    return "i-file";
  }

  function click(e: MouseEvent, r: FileRow) {
    const k = rowKey(r);
    if (e.ctrlKey) {
      const s = new Set(selected);
      if (s.has(k)) s.delete(k); else s.add(k);
      selected = s;
    } else if (e.shiftKey && anchor) {
      const keys = rows.map(rowKey);
      const a = keys.indexOf(anchor), b = keys.indexOf(k);
      if (a >= 0 && b >= 0) selected = new Set(keys.slice(Math.min(a, b), Math.max(a, b) + 1));
    } else {
      selected = new Set([k]);
      anchor = k;
    }
  }

  // The keyboard path: Space selects, Enter selects and opens, the arrows
  // move the focus between rows.
  function keydown(e: KeyboardEvent, r: FileRow) {
    const el = e.currentTarget as HTMLElement;
    switch (e.key) {
      case " ":
        e.preventDefault();
        selected = new Set([rowKey(r)]);
        anchor = rowKey(r);
        break;
      case "Enter":
        e.preventDefault();
        selected = new Set([rowKey(r)]);
        anchor = rowKey(r);
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
    if (!t.closest("tbody tr") && !t.closest("thead")) selected = new Set();
  }

  function open(r: FileRow) {
    if (r.isFolder) {
      selected = new Set();
      void store.enterFolder(store.folder ? `${store.folder}/${r.name}` : r.name);
    }
  }

  // The preview follows the single selection: committed rows only.
  $effect(() => {
    const r = one;
    previewUrl = "";
    previewText = null;
    kind = "none";
    if (!r || r.isFolder || r.pending === "added" || r.pending === "replaced" || r.pending === "deleted") return;
    const k = previewKind(r.name);
    kind = k;
    if (k === "text") {
      Archive.PreviewText(id, r.fileId, 64 * 1024).then((t) => { if (one === r) previewText = t; }).catch(fail);
    } else if (k !== "none") {
      Archive.PreviewURL(id, r.fileId).then((u) => { if (one === r) previewUrl = u; }).catch(fail);
    }
  });

  // Reload the listing when the folder or archive changes.
  $effect(() => {
    void id;
    void store.folder;
    selected = new Set();
  });

  // Created folders belong to the archive they were created in: another
  // archive starts with none.
  $effect(() => {
    void id;
    untrack(() => (pending = []));
  });

  async function addFiles(paths: string[], dir = false) {
    if (!paths.length) return;
    const names = paths.map((p) => p.split(/[\\/]/).pop() ?? p);
    try {
      const col = (await Archive.CheckNames(id, store.folder, names)) ?? [];
      if (col.length > 0) {
        collisions = { paths, names: col.map((c) => c.name), dir };
        return;
      }
      await addWith(paths, dir, "skip");
    } catch (e) {
      fail(e);
    }
  }

  async function addWith(paths: string[], dir: boolean, policy: string) {
    collisions = null;
    try {
      if (dir) await Archive.AddFolder(id, store.folder, paths[0], policy);
      else await Archive.AddFiles(id, store.folder, paths, policy);
    } catch (e) {
      fail(e);
    }
  }

  async function pickFiles() {
    const p = (await Shell.PickFiles("Add files", true)) ?? [];
    await addFiles(p);
  }

  async function pickFolder() {
    const p = await Shell.PickFolder("Add a folder");
    if (p) await addFiles([p], true);
  }

  // One *Add* button, three ways to add (APP.md §6): the two pickers, and
  // a folder made here that waits for a file.
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

  function makeFolder() {
    newFolderAttempt++;
    if (!newFolderValid) return;
    pending = addPending(pending, store.folder, newFolder);
    creating = false;
  }

  $effect(() => {
    const d = store.drop;
    if (!d) return;
    store.drop = null;
    if (d.archiveId !== id) {
      store.toast("Drop files onto the archive they belong in.", "error");
      return;
    }
    void addFiles(d.paths);
  });

  async function save() {
    try {
      await Archive.Save(id);
      // A created folder a file was added into is the archive's now and
      // is listed as that file's prefix; one that never got a file is
      // written nowhere and goes with the save. The page may be standing
      // in one of those: the archive's own listing says which, so the
      // page asks it after the save rather than guessing, and steps out
      // the way Discard does.
      const stood = dropTo(pending, store.folder) !== store.folder ? store.folder : "";
      pending = [];
      if (stood) await standWhereItExists(stood);
    } catch (e) {
      fail(e);
    }
  }

  // The nearest folder at or above `from` that the archive itself lists —
  // a created folder a file was added into is there, one that was written
  // nowhere is not — and the page moves there if it is not there already.
  async function standWhereItExists(from: string) {
    let at = from;
    while (at) {
      const up = parentOf(at);
      const page = await Archive.Page(id, up, "name", 0, PAGE_LIMIT);
      if ((page.rows ?? []).some((r) => r.isFolder && r.name === leafOf(at))) break;
      at = up;
    }
    if (at !== store.folder) await store.enterFolder(at);
  }

  async function discard() {
    try {
      await Archive.Discard(id);
      // Discard drops every staged add, so every created folder is empty
      // and gone — the page steps out of one it was standing in.
      const to = dropTo(pending, store.folder);
      pending = [];
      if (to !== store.folder) await store.enterFolder(to);
      await store.refreshArchive();
    } catch (e) {
      fail(e);
    }
  }

  async function doRename() {
    if (!renaming) return;
    try {
      await Archive.Rename(id, renaming.fileId, renameTo);
      renaming = null;
    } catch (e) {
      fail(e);
    }
  }

  async function doDelete() {
    const ids = deleting.filter((r) => !r.isFolder).map((r) => r.fileId);
    deleting = [];
    try {
      await Archive.Delete(id, ids);
    } catch (e) {
      fail(e);
    }
  }

  async function startExtract(list: FileRow[]) {
    extracting = list.filter((r) => !r.isFolder);
    if (extracting.length === 0) return;
    const p = await Shell.PickFolder("Extract to");
    if (!p) {
      extracting = [];
      return;
    }
    extractDir = p;
  }

  // *Extract all* is the archive's, not the selection's (APP.md §6): every
  // file under every folder. The core hands out one folder at a time, so
  // the page walks them — a page at a time, the core's own limit — and
  // gives startExtract the whole list. A staged add or replace is not in
  // the archive yet and the core skips it (ops.go, Extract), so the page
  // skips it too: the destination dialog then counts what will be
  // written, and an archive that is nothing but staged adds falls into
  // startExtract's empty guard rather than failing with file_not_found.
  const PAGE_LIMIT = 1000;

  async function everyFile(): Promise<FileRow[]> {
    const out: FileRow[] = [];
    const walk = [""];
    while (walk.length > 0) {
      const folder = walk.shift() as string;
      for (let offset = 0; ; ) {
        const page = await Archive.Page(id, folder, "name", offset, PAGE_LIMIT);
        const got = page.rows ?? [];
        for (const r of got) {
          if (r.isFolder) walk.push(r.path);
          else if (r.pending !== "added" && r.pending !== "replaced") out.push(r);
        }
        offset += got.length;
        if (got.length === 0 || offset >= page.total) break;
      }
    }
    return out;
  }

  async function extractAll() {
    try {
      await startExtract(await everyFile());
    } catch (e) {
      fail(e);
    }
  }

  async function doExtract() {
    const ids = extracting.map((r) => r.fileId);
    extracting = [];
    try {
      await Archive.Extract(id, ids, extractDir, extractPolicy);
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
  // this page shows (APP.md §13): the archive is closed first — unsaved
  // changes asked about — and, since closing leaves this page, the id is
  // handed to the Archives page, which opens the one dialog. It is
  // disabled while the vault is locked with the archive open (§2.3): the
  // registry write needs the session's key.
  let deleteAsk = $state(false);

  async function askDelete() {
    if (dirty > 0) {
      deleteAsk = true;
      return;
    }
    await closeThenDelete();
  }

  async function closeThenDelete(saveFirst = false) {
    deleteAsk = false;
    const archiveId = id;
    try {
      if (saveFirst) await Archive.Save(archiveId);
      await Archives.Close(archiveId);
    } catch (e) {
      fail(e);
      return;
    }
    store.deleteAfterClose = archiveId;
    store.leaveArchive();
    await store.refreshArchives();
  }

  // selectedRows are the files in the selection; folders are containers
  // here, not things to extract or delete. Known limitation, following
  // the ruling as written: a folder created here is a folder row, so
  // *Delete* does not see it and *Rename* is off for it — a typo in its
  // name is undone by creating the right one and letting the wrong one go
  // at Save or Discard, both of which drop a folder no file was added
  // into.
  const selectedRows = $derived(rows.filter((r) => !r.isFolder && selected.has(rowKey(r))));

  // The foot's note (LayerFoot): the archive's figures; while the vault is
  // locked and the archive still open, when it closes.
  $effect(() => {
    let note = stat ? `${count(stat.files)} files · ${bytes(stat.size)} · key v${stat.keyVersion}${stat.lastSavedAt ? ` · last saved ${dateTime(stat.lastSavedAt)}` : ""}` : "";
    if (!alive && stat?.expiresAt) note += ` · archive closes in ${countdown(stat.expiresAt, store.now)}`;
    store.footNote = note;
  });
</script>

<div class="layer-head">
  <nav class="crumbs" aria-label="Breadcrumb">
    <button type="button" class="c" onclick={() => { store.leaveArchive(); }}>Archives</button>
    <svg class="i"><use href="#i-chevron" /></svg>
    <button type="button" class="c" class:here={crumbs.length === 0} onclick={() => store.enterFolder("")}>{stat?.name ?? ""}</button>
    {#each crumbs as seg, i (i)}
      <svg class="i"><use href="#i-chevron" /></svg>
      <button type="button" class="c" class:here={i === crumbs.length - 1} onclick={() => store.enterFolder(crumbs.slice(0, i + 1).join("/"))}>{seg}</button>
    {/each}
  </nav>
  <div class="grow"></div>
  <button type="button" class="btn sm" onclick={close} disabled={dirty > 0 && stat?.state !== "needs_reopen"}>Close archive</button>
</div>

<div class="layer-body">
  <div class="cmdbar">
    <MenuButton id="add" label="Add" icon="i-plus" items={addItems} disabled={!alive} />
    <button type="button" class="btn subtle" disabled={(stat?.files ?? 0) === 0} onclick={() => void extractAll()}><svg class="i i-14"><use href="#i-extract" /></svg>Extract all</button>
    <button type="button" class="btn subtle" disabled={!one || one.isFolder || !alive} onclick={() => { if (one) { renaming = one; renameTo = one.name; } }}><svg class="i i-14"><use href="#i-rename" /></svg>Rename</button>
    <button type="button" class="btn subtle danger" disabled={selectedRows.length === 0 || !alive} onclick={() => (deleting = selectedRows)}><svg class="i i-14"><use href="#i-trash" /></svg>Delete</button>
    <div class="grow"></div>
    <!-- Tampered disables it here as it does on the Archives page (APP.md
         §13, R25): the flow closes the archive before the write is even
         attempted, so a refusal at the end would have shut the user's
         archive for nothing. -->
    <button type="button" class="btn subtle danger" disabled={!store.unlocked || tampered} title={!store.unlocked ? "Unlock the vault first: the record is the vault's." : tampered ? "The vault's slot region does not verify; every change is disabled." : undefined} onclick={askDelete}><svg class="i i-14"><use href="#i-trash" /></svg>Delete archive…</button>
  </div>

  {#if !alive}
    <div class="bar">
      <svg class="i i-14"><use href="#i-lock" /></svg>
      <span class="grow">The vault is locked. You can browse and extract; adding, renaming and saving need the vault.</span>
      <button type="button" class="btn sm accent" onclick={() => store.go("lock")}>Unlock</button>
    </div>
  {/if}
  {#if stat?.state === "needs_reopen"}
    <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span class="grow">{codeText("archive.needs_reopen")}</span><button type="button" class="btn sm" onclick={close}>Close and reopen</button></div>
  {/if}
  {#if stat?.copyMismatch}
    <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span>{codeText("archive.copy_mismatch")}</span></div>
  {/if}
  {#if dirty > 0}
    <div class="pending">
      <svg class="i i-14"><use href="#i-save" /></svg>
      <!-- The space before "Discarded" is written out: whitespace at the
           start of a block is not kept, and the two sentences ran into
           one another. -->
      <div class="txt"><b class="num">{dirty} change{dirty === 1 ? "" : "s"} not yet saved</b> — written as one step, or not at all.{#if stat?.capAt}{" "}Discarded at {dateTime(stat.capAt).slice(11)} if not saved.{/if}</div>
      <div class="acts">
        <button type="button" class="btn accent sm" disabled={!alive} onclick={save}>Save changes</button>
        <button type="button" class="btn sm" onclick={discard}>Discard</button>
      </div>
    </div>
  {/if}
  <OpsBar />

  <div class="file-split" class:dropping>
    <!-- A click on the list's blank area — under the last row, or in the
         container beside the table — clears the selection (APP.md §6); a
         click that lands on a row is the row's. -->
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions, a11y_click_events_have_key_events -->
    <div class="tablewrap" data-file-drop-target="true" data-archive-id={id} data-folder={store.folder}
      role="region" aria-label="Files" onclick={blank}
      ondragenter={() => (dropping = true)} ondragleave={() => (dropping = false)} ondrop={() => (dropping = false)}>
      {#if rows.length === 0}
        <div class="empty">{store.folder ? "This folder is empty." : "Nothing here yet. Add files, or drop them here."}</div>
      {:else}
        <table>
          <colgroup><col /><col class="w-size" /><col class="w-store" /><col class="w-date" /></colgroup>
          <thead><tr><th scope="col">Name</th><th scope="col">Size</th><th scope="col">Stored as</th><th scope="col">Modified</th></tr></thead>
          <tbody>
            {#each rows as r, i (rowKey(r))}
              <!-- svelte-ignore a11y_no_noninteractive_tabindex a11y_no_noninteractive_element_interactions -->
              <tr tabindex={selected.has(rowKey(r)) || (selected.size === 0 && i === 0) ? 0 : -1} aria-selected={selected.has(rowKey(r))} class:pending-deleted={r.pending === "deleted"} onclick={(e) => click(e, r)} ondblclick={() => open(r)} onkeydown={(e) => keydown(e, r)}>
                <td class="sel-mark">
                  <div class="fname">
                    <svg class="i i-14"><use href="#{fileIcon(r)}" /></svg>
                    <span>{r.name}</span>
                    {#if r.pending}<span class="chip">{r.pending}</span>{/if}
                  </div>
                </td>
                <td class="num">{r.isFolder ? `${count(r.files)} files` : bytes(r.size)}</td>
                <!-- The column is fixed at what "zstd, 79% smaller" needs
                     (APP.md §6), so a longer label — a dictionary's —
                     ellipsizes; the cell carries the whole of it, so a
                     hover reads the part that was cut. -->
                <td title={r.isFolder ? undefined : storageLabel(r.storage, r.savedPercent)}>{r.isFolder ? "" : storageLabel(r.storage, r.savedPercent)}</td>
                <td class="num">{r.isFolder ? "" : dateTime(r.modifiedAt)}</td>
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
        {:else if one && one.isFolder}
          <span class="none">Folder · {count(one.files)} files</span>
        {:else if one && (one.pending === "added" || one.pending === "replaced")}
          <span class="none">Preview after saving</span>
        {:else if one}
          <span class="none">No preview for this type — extract it</span>
        {:else}
          <span class="none">{selected.size > 1 ? `${selected.size} selected` : "Select a file"}</span>
        {/if}
      </div>
      {#if one}
        <div class="pname"><svg class="i i-14"><use href="#{fileIcon(one)}" /></svg><span>{one.name}</span></div>
        <dl class="facts">
          {#if !one.isFolder}
            <div class="fact"><dt>Size</dt><dd>{bytes(one.size)}</dd></div>
            <div class="fact"><dt>Stored as</dt><dd>{storageLabel(one.storage, one.savedPercent)}</dd></div>
            <div class="fact"><dt>Modified</dt><dd>{dateTime(one.modifiedAt)}</dd></div>
          {/if}
          <div class="fact"><dt>Path</dt><dd title={one.path}>{one.path}</dd></div>
        </dl>
      {/if}
      <div class="preview-actions">
        <button type="button" class="btn accent" disabled={selectedRows.length === 0} onclick={() => startExtract(selectedRows)}>Extract…</button>
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
    <p>Enfold stores paths, not folders: this one is the page's until you add a file into it. Add none and it is gone when you save or discard.</p>
    {#snippet actions()}
      <button type="button" class="btn" onclick={() => (creating = false)}>Cancel</button>
      <button type="button" class="btn accent" onclick={makeFolder}>Create</button>
    {/snippet}
  </Dialog>
{/if}

{#if deleting.length > 0}
  <Dialog title="Delete {deleting.length === 1 ? deleting[0].name : `${deleting.length} items`}?" onclose={() => (deleting = [])}>
    <p>The change is staged and written when you save. Folders are not deleted as a whole in this version; select their files.</p>
    {#snippet actions()}
      <button type="button" class="btn" onclick={() => (deleting = [])}>Cancel</button>
      <button type="button" class="btn accent" onclick={doDelete}>Delete</button>
    {/snippet}
  </Dialog>
{/if}

{#if deleteAsk}
  <Dialog title="Save the unsaved changes first?" onclose={() => (deleteAsk = false)}>
    <p>{stat?.name ?? "This archive"} has {dirty} unsaved change{dirty === 1 ? "" : "s"}. Deleting the archive removes the file, so anything not saved goes with it either way — saving first only puts the changes into the file that is about to be removed.</p>
    {#snippet actions()}
      <button type="button" class="btn" onclick={() => (deleteAsk = false)}>Cancel</button>
      <button type="button" class="btn" onclick={() => void closeThenDelete(true)}>Save, then continue</button>
      <button type="button" class="btn accent" onclick={() => void closeThenDelete(false)}>Discard and continue</button>
    {/snippet}
  </Dialog>
{/if}

{#if extracting.length > 0 && extractDir}
  <Dialog title="Extract {extracting.length} file{extracting.length === 1 ? '' : 's'}" onclose={() => (extracting = [])}>
    <p>To <span class="mono">{extractDir}</span>. Existing files are never overwritten.</p>
    <div class="field">
      <div class="field-top"><label for="xp">If a file already exists</label></div>
      <select id="xp" class="input" bind:value={extractPolicy}><option value="skip">Skip it</option><option value="rename">Extract under a new name</option></select>
    </div>
    {#snippet actions()}
      <button type="button" class="btn" onclick={() => (extracting = [])}>Cancel</button>
      <button type="button" class="btn accent" onclick={doExtract}>Extract</button>
    {/snippet}
  </Dialog>
{/if}

{#if collisions}
  <Dialog title="Some names already exist" onclose={() => (collisions = null)}>
    <p>{collisions.names.slice(0, 5).join(", ")}{collisions.names.length > 5 ? ` and ${collisions.names.length - 5} more` : ""}</p>
    {#snippet actions()}
      <button type="button" class="btn" onclick={() => (collisions = null)}>Cancel</button>
      <button type="button" class="btn" onclick={() => collisions && addWith(collisions.paths, collisions.dir, "skip")}>Skip those</button>
      <button type="button" class="btn" onclick={() => collisions && addWith(collisions.paths, collisions.dir, "keep-both")}>Keep both</button>
      <button type="button" class="btn accent" onclick={() => collisions && addWith(collisions.paths, collisions.dir, "replace")}>Replace</button>
    {/snippet}
  </Dialog>
{/if}

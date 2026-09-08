<script lang="ts">
  import { Archive, Archives, Shell, errorOf } from "../lib/api";
  import type { FileRow } from "../lib/api";
  import { store } from "../lib/state.svelte";
  import { codeText } from "../lib/strings";
  import { bytes, count, countdown, dateTime, previewKind, storageLabel } from "../lib/format";
  import type { PreviewKind } from "../lib/format";
  import Dialog from "./Dialog.svelte";
  import OpsBar from "./OpsBar.svelte";

  const id = $derived(store.current ?? "");
  const stat = $derived(store.stat);
  const rows = $derived(store.page?.rows ?? []);
  const crumbs = $derived(store.folder ? store.folder.split("/") : []);
  const alive = $derived(stat?.sessionAlive ?? false);
  const dirty = $derived(stat?.dirty ?? 0);

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
    } catch (e) {
      fail(e);
    }
  }

  async function discard() {
    try {
      await Archive.Discard(id);
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
    const p = await Shell.PickFolder("Extract to");
    if (!p) {
      extracting = [];
      return;
    }
    extractDir = p;
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

  // selectedRows are the files in the selection; folders are containers
  // here, not things to extract or delete.
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
    <button type="button" class="btn subtle" disabled={!alive} onclick={pickFiles}><svg class="i i-14"><use href="#i-plus" /></svg>Add files</button>
    <button type="button" class="btn subtle" disabled={!alive} onclick={pickFolder}><svg class="i i-14"><use href="#i-folder-add" /></svg>Add folder</button>
    <button type="button" class="btn subtle" disabled={selectedRows.length === 0} onclick={() => startExtract(selectedRows)}><svg class="i i-14"><use href="#i-extract" /></svg>Extract</button>
    <button type="button" class="btn subtle" disabled={!one || one.isFolder || !alive} onclick={() => { if (one) { renaming = one; renameTo = one.name; } }}><svg class="i i-14"><use href="#i-rename" /></svg>Rename</button>
    <button type="button" class="btn subtle danger" disabled={selectedRows.length === 0 || !alive} onclick={() => (deleting = selectedRows)}><svg class="i i-14"><use href="#i-trash" /></svg>Delete</button>
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
      <div class="txt"><b class="num">{dirty} change{dirty === 1 ? "" : "s"} not yet saved</b> — written as one step, or not at all.{#if stat?.capAt} Discarded at {dateTime(stat.capAt).slice(11)} if not saved.{/if}</div>
      <div class="acts">
        <button type="button" class="btn accent sm" disabled={!alive} onclick={save}>Save changes</button>
        <button type="button" class="btn sm" onclick={discard}>Discard</button>
      </div>
    </div>
  {/if}
  <OpsBar />

  <div class="file-split" class:dropping>
    <div class="tablewrap" data-file-drop-target="true" data-archive-id={id} data-folder={store.folder}
      role="region" aria-label="Files"
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
                <td>{r.isFolder ? "" : storageLabel(r.storage, r.savedPercent)}</td>
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
      <button type="button" class="btn accent" disabled={!renameTo || renameTo.includes("/")} onclick={doRename}>Rename</button>
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

<style>
  .w-size { width: 90px; }
  .w-store { width: 200px; }
  .w-date { width: 130px; }
</style>

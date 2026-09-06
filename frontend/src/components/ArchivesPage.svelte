<script lang="ts">
  import { Archives, Shell, Vault, errorOf } from "../lib/api";
  import type { ArchiveSummary } from "../lib/api";
  import { store } from "../lib/state.svelte";
  import { codeText, warningCopy } from "../lib/strings";
  import { bytes, count, countdown, dateTime, leaf } from "../lib/format";
  import Dialog from "./Dialog.svelte";
  import OpsBar from "./OpsBar.svelte";

  let selected = $state<string | null>(null);
  const sel = $derived(store.archives.find((a) => a.id === selected) ?? null);
  const st = $derived(store.status);
  const unlocked = $derived(store.unlocked);
  const tampered = $derived(st?.tampered ?? false);

  let creating = $state(false);
  let newName = $state("");
  let newPath = $state("");
  let newRaw = $state(false);
  let confirm = $state<null | { title: string; body: string; button: string; run: () => Promise<unknown> }>(null);

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

  async function pickNewPath() {
    const p = await Shell.SaveFile("Where to keep the archive", `${newName || "archive"}.efd`);
    if (p) newPath = p;
  }

  async function create() {
    creating = false;
    try {
      const id = await Archives.Create(newPath, newName, newRaw);
      selected = id;
      newName = "";
      newPath = "";
    } catch (e) {
      fail(e);
    }
  }

  async function locate(a: ArchiveSummary) {
    const p = (await Shell.PickFiles(`Where is ${a.name} now?`, false)) ?? [];
    if (p.length) await run(Archives.Locate(a.id, p[0]));
  }

  function note(a: ArchiveSummary): string {
    if (a.note) return codeText(a.note);
    const parts: string[] = [];
    if (a.open && a.dirty > 0) parts.push(`${a.dirty} unsaved change(s)`);
    else if (a.open) parts.push("open");
    if (a.receiptOwed) parts.push("receipt pending");
    if (a.noCompression) parts.push("stored raw");
    if (a.hidden) parts.push("hidden");
    if (a.open && a.storedSize > 0 && a.freeSpace > a.storedSize * 0.3) parts.push("compact when convenient");
    return parts.join(" · ") || "—";
  }
</script>

<div class="layer-head">
  <h1 class="t-title">{st?.displayName || "Vault"}</h1>
  <span class="chip num">{store.archives.length} archive{store.archives.length === 1 ? "" : "s"}</span>
  {#if !unlocked}<span class="chip warn"><svg class="i i-14"><use href="#i-lock" /></svg>Locked</span>{/if}
</div>

<div class="layer-body">
  <div class="cmdbar">
    <button type="button" class="btn accent" disabled={!sel || !unlocked && !sel.open} onclick={() => sel && store.openArchive(sel.id)}><svg class="i i-14"><use href="#i-open" /></svg>Open</button>
    <button type="button" class="btn" disabled={!unlocked || tampered} onclick={() => (creating = true)}><svg class="i i-14"><use href="#i-plus" /></svg>New archive</button>
    <div class="sep"></div>
    <button type="button" class="btn subtle" disabled={!sel || !unlocked || tampered} onclick={() => sel && (confirm = { title: "Compact this archive?", body: "Free space is reclaimed by rewriting the file. Nothing changes for the files inside; it takes time in proportion to the size.", button: "Compact", run: () => Archives.Compact(sel.id) })}>Compact</button>
    <button type="button" class="btn subtle" disabled={!sel || !unlocked || tampered} onclick={() => sel && (confirm = { title: "Rotate this archive's key?", body: "A new key is wrapped into the vault first, then the archive is re-keyed. Old versions of the key stay readable.", button: "Rotate key", run: () => Archives.RotateKey(sel.id) })}><svg class="i i-14"><use href="#i-rotate" /></svg>Rotate key</button>
    <button type="button" class="btn subtle" disabled={!sel || !unlocked} onclick={() => sel && run(Archives.Verify(sel.id))}><svg class="i i-14"><use href="#i-check" /></svg>Verify</button>
    <button type="button" class="btn subtle" disabled={!sel || !unlocked} onclick={() => sel && run(sel.hidden ? Archives.Unhide(sel.id) : Archives.Hide(sel.id))}><svg class="i i-14"><use href="#i-eye" /></svg>{sel?.hidden ? "Unhide" : "Hide"}</button>
    <div class="grow"></div>
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
    <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span>{warningCopy("vault.tampered")}</span></div>
  {/if}
  {#if st?.rotationPending}
    <div class="bar attention">
      <svg class="i i-14"><use href="#i-warn" /></svg>
      <span class="grow">A key rotation is deferred: a way in has not been rewrapped yet.</span>
      <button type="button" class="btn link" onclick={() => store.go("keys")}>Details</button>
    </div>
  {/if}
  <OpsBar />

  <div class="arch-split">
    <div class="arch-list">
      <div class="tablewrap">
        {#if store.archives.length === 0}
          <div class="empty">{unlocked ? "No archives yet. Create one, or open a file you already have." : "No archives are open."}</div>
        {:else}
          <table>
            <colgroup><col /><col class="w-size" /><col class="w-files" /><col class="w-date" /><col class="w-key" /></colgroup>
            <thead><tr><th scope="col">Name</th><th scope="col">Size</th><th scope="col">Files</th><th scope="col">Last saved</th><th scope="col">Key</th></tr></thead>
            <tbody>
              {#each store.archives as a (a.id)}
                <tr aria-selected={selected === a.id} onclick={() => (selected = a.id)} ondblclick={() => store.openArchive(a.id)}>
                  <td class="sel-mark">
                    <div class="fname">
                      <svg class="i i-14"><use href="#i-box" /></svg>
                      <div class="two-line"><b>{a.name}</b><small>{note(a)}</small></div>
                    </div>
                  </td>
                  <td class="num">{a.storedSize ? bytes(a.storedSize) : "—"}</td>
                  <td class="num">{a.open ? count(a.files) : "—"}</td>
                  <td class="num">{dateTime(a.lastWrittenAt)}</td>
                  <td><span class="chip key">v{a.keyVersion}</span></td>
                </tr>
              {/each}
            </tbody>
          </table>
        {/if}
      </div>
    </div>

    <aside class="details" aria-label="Selected archive">
      {#if sel}
        <div class="details-head"><div class="eyebrow">Selected</div><h3>{sel.name}</h3></div>
        <dl class="facts">
          <div class="fact"><dt>File</dt><dd title={sel.path}>{leaf(sel.path)}</dd></div>
          <div class="fact"><dt>Size</dt><dd>{sel.storedSize ? bytes(sel.storedSize) : "—"}</dd></div>
          {#if sel.open}<div class="fact"><dt>Files</dt><dd>{count(sel.files)}</dd></div>{/if}
          <div class="fact"><dt>Last saved</dt><dd>{dateTime(sel.lastWrittenAt)}</dd></div>
          <div class="fact"><dt>Key version</dt><dd>v{sel.keyVersion}</dd></div>
          <div class="fact"><dt>Compression</dt><dd>{sel.noCompression ? "off (stored raw)" : "on"}</dd></div>
          {#if sel.hashBehind > 0}<div class="fact"><dt>Verified</dt><dd>{sel.hashBehind} save(s) ago</dd></div>{/if}
        </dl>
        {#if sel.note === "archive.file_missing"}
          <div class="pad"><div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span class="grow">{codeText(sel.note)}</span><button type="button" class="btn link" onclick={() => locate(sel)}>Locate…</button></div></div>
        {/if}
        <div class="details-actions">
          <button type="button" class="btn accent wide" disabled={!unlocked && !sel.open} onclick={() => store.openArchive(sel.id)}><svg class="i i-14"><use href="#i-open" /></svg>Open</button>
          {#if sel.open}<button type="button" class="btn wide" onclick={() => run(Archives.Close(sel.id)).then(() => store.refreshArchives())}>Close</button>{/if}
          <button type="button" class="btn wide" onclick={() => void Shell.Reveal(sel.path)}>Show in Explorer</button>
        </div>
      {:else}
        <div class="empty">Select an archive.</div>
      {/if}
    </aside>
  </div>
</div>

<div class="layer-foot">
  <span>{sel ? `${sel.name} selected` : `${store.archives.length} archive(s)`}</span>
  {#if unlocked && st}
    <span class="lockchip"><svg class="i i-14"><use href="#i-lock" /></svg>Locks in {countdown(st.locksAt, store.now)}<button type="button" class="btn link" onclick={() => void Vault.Lock()}>Lock now</button></span>
  {/if}
</div>

{#if creating}
  <Dialog title="New archive" onclose={() => (creating = false)}>
    <div class="field"><div class="field-top"><label for="na-name">Name</label></div><input id="na-name" class="input" bind:value={newName} placeholder="Photos 2026" /></div>
    <div class="field">
      <div class="field-top"><label for="na-path">File</label></div>
      <div class="row"><input id="na-path" class="input grow" readonly value={newPath} placeholder="Choose where the archive file lives" /><button type="button" class="btn" onclick={pickNewPath}>Choose…</button></div>
    </div>
    <label class="check"><input type="checkbox" bind:checked={newRaw} />Store files raw (for already-compressed media)</label>
    {#snippet actions()}
      <button type="button" class="btn" onclick={() => (creating = false)}>Cancel</button>
      <button type="button" class="btn accent" disabled={!newName || !newPath} onclick={create}>Create</button>
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

<style>
  .w-size { width: 84px; }
  .w-files { width: 70px; }
  .w-date { width: 130px; }
  .w-key { width: 56px; }
  .pad { padding: 0 16px 14px; }
</style>

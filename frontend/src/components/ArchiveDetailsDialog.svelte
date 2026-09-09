<script lang="ts">
  // One registry record read whole (APP.md §13): the versions table and
  // the record's values, rendered from what the details pane already
  // loaded — the record is read when the selection changes, not when this
  // modal opens. It carries no wrapped_archive_key, no nonce and no
  // offset: there is no raw archive-key reveal anywhere (APP.md §1, §13).
  //
  // Since 2026-09-09 there is no column of *Copy* buttons: every value is
  // selectable and a right-click on one opens a menu of a single item,
  // *Copy*. The shell disables WebView2's own context menu, so that is the
  // only one on the page. The ciphertext hash wraps onto a second line
  // rather than being cut, and reads N/A while it is all zeros — no commit
  // yet. The modal scrolls as a whole, and the versions table shows three
  // rows before it scrolls on its own.
  import { Clipboard } from "@wailsio/runtime";
  import type { ArchiveDetails } from "../lib/api";
  import { bytes, date, dateTime, hashText } from "../lib/format";
  import { methodWord } from "../lib/method";
  import { purgeAfter } from "../lib/retention";
  import ContextMenu from "./ContextMenu.svelte";
  import Dialog from "./Dialog.svelte";

  interface Props {
    d: ArchiveDetails;
    onclose: () => void;
  }
  let { d, onclose }: Props = $props();

  // The menu the right-click opened: where it is, and what it copies.
  let menu = $state<{ x: number; y: number; text: string } | null>(null);

  // Clipboard.SetText is the runtime's, not navigator.clipboard, which is
  // not dependable under the WebView2 custom scheme.
  function copy(text: string) {
    void Clipboard.SetText(text).catch(() => {});
  }

  // The click's own selection is left alone: a right-click on a value the
  // user has already selected part of copies the whole value, which is
  // what the row's menu offers — the selection itself is theirs to copy
  // with the keyboard.
  function askCopy(e: MouseEvent, text: string) {
    e.preventDefault();
    if (!text || text === "—") return;
    menu = { x: e.clientX, y: e.clientY, text };
  }

  const policy = $derived(
    [d.alwaysRequireFullAuth ? "always asks for a full unlock" : "", d.hidden ? "hidden" : ""].filter(Boolean).join(" · ") || "none set",
  );
  const purge = $derived(purgeAfter(d.forgottenAt));

  // long marks the rows whose value can be cut short: tooltips only there,
  // never over text that is fully visible (APP.md §13). wrap marks the one
  // value that is never cut at all — the hash, which takes a second line.
  interface Row {
    key: string;
    label: string;
    value: string;
    mono?: boolean;
    long?: boolean;
    wrap?: boolean;
  }
  const rows = $derived<Row[]>([
    { key: "id", label: "Archive ID", value: d.archiveId, mono: true, long: true },
    { key: "kid", label: "Current KID", value: d.currentKid || "—", mono: true, long: true },
    { key: "created", label: "Created", value: dateTime(d.createdAt) },
    { key: "path", label: "Last path", value: d.lastPath || "—", long: true },
    { key: "size", label: "Last stored size", value: d.lastStoredSize ? bytes(d.lastStoredSize) : "—" },
    { key: "saved", label: "Last saved", value: dateTime(d.lastWrittenAt) },
    { key: "rev", label: "Revision", value: String(d.revision) },
    { key: "writer", label: "Last writer", value: d.lastWriter || "—", mono: true, long: true },
    { key: "seq", label: "Last seq / hash at seq", value: `${d.lastSeq} / ${d.hashAtSeq}` },
    { key: "hash", label: "Ciphertext hash", value: hashText(d.lastCiphertextHash), mono: true, wrap: true },
    // The compression the archive was created with, by its word: every
    // writer of this archive follows it (FORMAT.md §7.1 bits 2-5).
    { key: "method", label: "Compression", value: methodWord(d.method) },
    { key: "policy", label: "Policy", value: policy },
  ]);
</script>

<Dialog title="Details — {d.name}" wide onclose={onclose}>
  {#if d.description}<p class="t-sub desc">{d.description}</p>{/if}
  {#if purge}
    <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span>Forgotten on {date(d.forgottenAt)}. Its key is dropped at the first unlock after {date(purge)}.</span></div>
  {/if}

  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <div class="kv">
    {#each rows as r (r.key)}
      <div class="k">{r.label}</div>
      <div
        class="v" class:mono={r.mono} class:wrap={r.wrap}
        title={r.long ? r.value : undefined}
        role="note"
        oncontextmenu={(e) => askCopy(e, r.value)}
      >{r.value}</div>
    {/each}
  </div>

  <h3 class="t-section vh">Key versions</h3>
  <div class="tablewrap vers">
    <table>
      <colgroup><col /><col class="w-date" /><col class="w-date" /><col class="w-state" /></colgroup>
      <thead><tr><th scope="col">KID</th><th scope="col">Created</th><th scope="col">Retired</th><th scope="col">State</th></tr></thead>
      <tbody>
        {#each d.versions ?? [] as v (v.kid)}
          <tr>
            <td class="mono" title={v.kid}>{v.kid}</td>
            <td class="num">{dateTime(v.createdAt)}</td>
            <td class="num">{v.retiredAt ? dateTime(v.retiredAt) : "—"}</td>
            <td>{v.state}</td>
          </tr>
        {/each}
      </tbody>
    </table>
    {#if (d.versions ?? []).length === 0}<div class="empty">This record names no key version.</div>{/if}
  </div>

  {#snippet actions()}
    <button type="button" class="btn accent" onclick={onclose}>Close</button>
  {/snippet}
</Dialog>

{#if menu}
  {@const m = menu}
  <ContextMenu x={m.x} y={m.y} items={[{ label: "Copy", icon: "i-copy", run: () => copy(m.text) }]} onclose={() => (menu = null)} />
{/if}

<style>
  .desc { margin: 0 0 4px; }
  .vh { margin: 14px 0 0; }
  /* The table takes its content's height: three rows are visible before it
     scrolls at all, and it never scrolls past eight — beyond that the
     modal's own scroll takes over (APP.md §13). It may not be squeezed by
     the flex column above it. */
  .vers { flex: 0 0 auto; max-height: 320px; }
  .w-date { width: 128px; }
  .w-state { width: 72px; }
  .mono { font-family: var(--font-mono); font-size: 11.5px; }
</style>

<script lang="ts">
  // One registry record read whole (APP.md §13): the versions table and
  // the copyable rows, rendered from what the details pane already
  // loaded — the record is read when the selection changes, not when this
  // modal opens. It carries no wrapped_archive_key, no nonce and no
  // offset: there is no raw archive-key reveal anywhere (APP.md §1, §13).
  import { Clipboard } from "@wailsio/runtime";
  import type { ArchiveDetails } from "../lib/api";
  import { bytes, date, dateTime } from "../lib/format";
  import { purgeAfter } from "../lib/retention";
  import Dialog from "./Dialog.svelte";

  interface Props {
    d: ArchiveDetails;
    onclose: () => void;
  }
  let { d, onclose }: Props = $props();

  // A copy says so in place — the button's own label for a moment — and
  // never as a toast: a toast is for what the page cannot say where the
  // action was (APP.md §6).
  let copied = $state("");
  let copiedTimer: ReturnType<typeof setTimeout> | undefined;

  // Clipboard.SetText is the runtime's, not navigator.clipboard, which is
  // not dependable under the WebView2 custom scheme.
  async function copy(key: string, text: string) {
    try {
      await Clipboard.SetText(text);
      copied = key;
      clearTimeout(copiedTimer);
      copiedTimer = setTimeout(() => (copied = ""), 1400);
    } catch {
      copied = "";
    }
  }

  const policy = $derived(
    [d.alwaysRequireFullAuth ? "always asks for a full unlock" : "", d.hidden ? "hidden" : "", d.noCompression ? "stored raw" : ""].filter(Boolean).join(" · ") || "none set",
  );
  const purge = $derived(purgeAfter(d.forgottenAt));

  // long marks the rows whose value can be cut short: tooltips only there,
  // never over text that is fully visible (APP.md §13).
  interface Row {
    key: string;
    label: string;
    value: string;
    mono?: boolean;
    long?: boolean;
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
    { key: "hash", label: "Ciphertext hash", value: d.lastCiphertextHash || "—", mono: true, long: true },
    { key: "policy", label: "Policy", value: policy },
  ]);
</script>

<Dialog title="Details — {d.name}" wide onclose={onclose}>
  {#if d.description}<p class="t-sub desc">{d.description}</p>{/if}
  {#if purge}
    <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span>Forgotten on {date(d.forgottenAt)}. Its key is dropped at the first unlock after {date(purge)}.</span></div>
  {/if}

  <div class="kv">
    {#each rows as r (r.key)}
      <div class="k">{r.label}</div>
      <div class="v" class:mono={r.mono} title={r.long ? r.value : undefined}>{r.value}</div>
      <button type="button" class="btn sm" onclick={() => copy(r.key, r.value)}>{copied === r.key ? "Copied" : "Copy"}</button>
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

<style>
  .desc { margin: 0 0 4px; }
  .vh { margin: 14px 0 0; }
  .vers { max-height: 190px; }
  .w-date { width: 128px; }
  .w-state { width: 72px; }
  .mono { font-family: var(--font-mono); font-size: 11.5px; }
</style>

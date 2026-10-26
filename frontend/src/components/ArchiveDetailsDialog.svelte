<script lang="ts">
  // *Details* (APP.md §13): the second register, the one dialog where the
  // format's words stand, each label from technicalCopy. It was *Details*,
  // then *Technical details* (ruled 2026-10-17), and is *Details* again
  // since the user's ruling of 2026-10-26: the plainer word for the row's
  // facts, where *Properties* would promise Explorer's whole sheet. The key version, the KID and the receipt live here
  // and nowhere on the main surfaces.
  //
  // One registry record read whole (APP.md §13): the versions table and
  // the record's values, rendered from what the details pane already
  // loaded — the record is read when the selection changes, not when this
  // modal opens. It carries no wrapped_archive_key, no nonce and no
  // offset: there is no raw archive-key reveal anywhere (APP.md §1, §13).
  //
  // Since 2026-09-09 there is no column of *Copy* buttons: every value is
  // selectable and a right-click on one opens the app's menu (ContextMenu)
  // of two items: *Copy*, the value, and *Copy all as text*, every row as
  // "Label: value" lines for a bug report (direction 7). 2026-09-06 turned
  // WebView2's own context menu off; this one is the page's.
  //
  // The ciphertext hash wraps onto a second line rather than being cut,
  // and reads N/A while it is all zeros — no commit yet. The modal scrolls
  // as a whole, and the versions table shows three rows before it scrolls
  // on its own.
  import { Clipboard } from "@wailsio/runtime";
  import type { ArchiveDetails } from "../lib/api";
  import { bytes, date, dateTime, hashText, labelledText } from "../lib/format";
  import { methodWord } from "../lib/method";
  import { purgeAfter } from "../lib/retention";
  import { archivesCopy, chipCopy, dialogCopy, technicalCopy } from "../lib/strings";
  import ContextMenu from "./ContextMenu.svelte";
  import Dialog from "./Dialog.svelte";

  interface Props {
    d: ArchiveDetails;
    // The key version the list's row carries: the list has no Key column
    // since 2026-10-14, and the number matters only around a rotation, so
    // it is read here, beside the KID it names. 0 when there is no row.
    keyVersion: number;
    // receiptOwed is the row's too: a save whose receipt the registry has
    // not taken yet. A format fact, so it left the pane for here (ruled
    // 2026-10-17).
    receiptOwed?: boolean;
    onclose: () => void;
  }
  let { d, keyVersion, receiptOwed = false, onclose }: Props = $props();

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
  // with the keyboard. A value with nothing in it ("—") has no Copy, but
  // the menu still offers the whole dialog as text.
  function askCopy(e: MouseEvent, text: string) {
    e.preventDefault();
    menu = { x: e.clientX, y: e.clientY, text: text === "—" ? "" : text };
  }

  // The policy bits by their words, the line in sentence case like every
  // value (ruled 2026-10-17).
  const policy = $derived.by(() => {
    const line = [d.hidden ? "hidden" : "", d.alwaysRequireFullAuth ? "full unlock always (reserved, not enforced)" : ""].filter(Boolean).join(" · ");
    return line ? line[0].toUpperCase() + line.slice(1) : "None set";
  });
  const purge = $derived(purgeAfter(d.forgottenAt));

  // long marks the rows whose value can be cut short: tooltips only there,
  // never over text that is fully visible (APP.md §13). The path and the
  // hash are never cut at all: they are the stacked list's, below, whose
  // values wrap.
  interface Row {
    key: string;
    label: string;
    value: string;
    mono?: boolean;
    num?: boolean;
    long?: boolean;
  }
  const inline = $derived<Row[]>([
    { key: "id", label: technicalCopy.archiveId, value: d.archiveId, mono: true, long: true },
    { key: "kid", label: technicalCopy.currentKid, value: d.currentKid || "—", mono: true, long: true },
    { key: "keyv", label: technicalCopy.keyVersion, value: technicalCopy.keyVersionValue(keyVersion) },
    { key: "created", label: technicalCopy.created, value: dateTime(d.createdAt), num: true },
    { key: "size", label: technicalCopy.lastStoredSize, value: d.lastStoredSize ? bytes(d.lastStoredSize) : "—", num: true },
    { key: "saved", label: technicalCopy.lastSaved, value: dateTime(d.lastWrittenAt), num: true },
    { key: "rev", label: technicalCopy.revision, value: String(d.revision), num: true },
    { key: "writer", label: technicalCopy.lastWriter, value: d.lastWriter || "—", mono: true, long: true },
    { key: "seq", label: technicalCopy.seq, value: String(d.lastSeq), num: true },
    // 0 is no fingerprint taken yet (lib/archlist.ts fingerprintOf), said
    // as the fingerprint row says it (format.ts hashText), not as a seq.
    { key: "hashseq", label: technicalCopy.fingerprintSeq, value: d.hashAtSeq ? String(d.hashAtSeq) : "N/A", num: true },
    // The compression the archive was created with, by its word: every
    // writer of this archive follows it (FORMAT.md §7.1 bits 2-5).
    { key: "method", label: archivesCopy.facts.compression, value: methodWord(d.method) },
    { key: "policy", label: technicalCopy.policy, value: policy, long: true },
    ...(receiptOwed ? [{ key: "receipt", label: technicalCopy.receipt, value: technicalCopy.receiptPending }] : []),
  ]);
  const stacked = $derived<Row[]>([
    { key: "path", label: technicalCopy.lastPath, value: d.lastPath || "—" },
    { key: "hash", label: technicalCopy.fingerprint, value: hashText(d.lastCiphertextHash), mono: true },
  ]);
  // *Copy all as text*: every row of both lists, in the dialog's order.
  const allText = $derived(labelledText([...inline, ...stacked]));
</script>

<!-- Close is its one action, so the backdrop closes it too (APP.md §7). -->
<Dialog title={technicalCopy.titleOf(d.name)} wide onclose={onclose} dismissable>
  {#if d.description}<p class="t-sub">{d.description}</p>{/if}
  {#if purge}
    <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span>{chipCopy.forgottenOn(date(d.forgottenAt))}. Its key is dropped at the first unlock after {date(purge)}.</span></div>
  {/if}

  <!-- The record's values in the one label · value list (APP.md §6, Type
       and spacing); the path and the hash in its stacked form, the label
       over a value that wraps rather than being cut (APP.md §13). -->
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <dl class="kv">
    {#each inline as r (r.key)}
      <div>
        <dt>{r.label}</dt>
        <dd class:mono={r.mono} class:num={r.num} title={r.long ? r.value : undefined} oncontextmenu={(e) => askCopy(e, r.value)}>{r.value}</dd>
      </div>
    {/each}
  </dl>
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <dl class="kv stk">
    {#each stacked as r (r.key)}
      <div>
        <dt>{r.label}</dt>
        <dd class:mono={r.mono} oncontextmenu={(e) => askCopy(e, r.value)}>{r.value}</dd>
      </div>
    {/each}
  </dl>
  <!-- What the fingerprint is for, and what it is not (direction 9): it
       tells this file from another copy, and checks nothing inside it. -->
  <p class="t-quiet">{technicalCopy.fingerprintNote}</p>

  <h3 class="t-section vh">{technicalCopy.keyVersions}</h3>
  <div class="tablewrap vers">
    <table>
      <colgroup><col /><col class="w-date" /><col class="w-date" /><col class="w-state" /></colgroup>
      <thead><tr><th scope="col">{technicalCopy.columns.kid}</th><th scope="col">{technicalCopy.columns.created}</th><th scope="col">{technicalCopy.columns.retired}</th><th scope="col">{technicalCopy.columns.state}</th></tr></thead>
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
    <button type="button" class="btn accent" onclick={onclose}>{dialogCopy.close}</button>
  {/snippet}
</Dialog>

{#if menu}
  {@const m = menu}
  <ContextMenu
    x={m.x} y={m.y} label={technicalCopy.titleOf(d.name)}
    items={[
      { label: technicalCopy.copy, icon: "i-copy", disabled: !m.text, run: () => copy(m.text) },
      { label: technicalCopy.copyAll, run: () => copy(allText) },
    ]}
    onclose={() => (menu = null)}
  />
{/if}

<style>
  /* A section heading stands a step further off than the parts of the
     dialog (APP.md §6, Type and spacing). */
  .vh { margin: var(--s3) 0 0; }
  /* The table takes its content's height: three rows are visible before it
     scrolls at all, and it never scrolls past eight — beyond that the
     modal's own scroll takes over (APP.md §13). It may not be squeezed by
     the flex column above it. */
  .vers { flex: 0 0 auto; max-height: 320px; }
  .w-date { width: 128px; }
  .w-state { width: 72px; }
</style>

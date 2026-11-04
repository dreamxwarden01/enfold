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
  //
  // Above the IDs, the block *Unused space* (APP.md §6; the reclaim
  // study's ruling of 2026-11-03, its placement D): the one surface wide
  // enough for a bar that carries information, with both ways to give the
  // space back and what each costs — *Reclaim space*, the in-place run, and
  // *Compact…*, which opens the Archives page's own confirm over this
  // dialog. Both are standard buttons: Close keeps the dialog's accent and
  // Enter (the owner's ruling of 2026-11-04), so Enter never starts a run
  // that moves gigabytes. The states and the words are SpaceBar.svelte's
  // spaceBlock.
  import { Clipboard } from "@wailsio/runtime";
  import { Archive, errorOf } from "../lib/api";
  import type { ArchiveDetails, ArchiveSummary, OpView, ReclaimView } from "../lib/api";
  import { bytes, date, dateTime, hashText, labelledText } from "../lib/format";
  import { methodWord } from "../lib/method";
  import { purgeAfter } from "../lib/retention";
  import { store } from "../lib/state.svelte";
  import { archivesCopy, chipCopy, codeText, commandCopy, dialogCopy, footCopy, spaceCopy, technicalCopy } from "../lib/strings";
  import ContextMenu from "./ContextMenu.svelte";
  import Dialog from "./Dialog.svelte";
  import SpaceBar, { spaceBlock } from "./SpaceBar.svelte";

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
    // The block's figures: the list's row, which carries an open
    // archive's (ArchiveSummary.Reclaim, CompactNeeds; storedSize is the
    // open handle's size) and only the registry's for a closed one.
    space?: ArchiveSummary | null;
    // A reclaim of the archive running, requested or the core's own, and
    // its busy reason; another operation holding it, by its reason; the
    // vault's stops for each action (lib/archcmds.ts).
    run?: OpView | null;
    runWhy?: string;
    busyWhy?: string;
    reclaimWhy?: string;
    compactWhy?: string;
    // Compact… opens the page's own confirm, over this dialog.
    oncompact?: () => void;
    onclose: () => void;
  }
  let { d, keyVersion, receiptOwed = false, space = null, run = null, runWhy, busyWhy, reclaimWhy, compactWhy, oncompact, onclose }: Props = $props();

  // A compaction holds the handle and the row carries no figure while it
  // runs (ArchiveSummary.Reclaim is nil while compacting), so the block
  // keeps the last figures it was shown until the reopen refreshes them
  // (the judge's table, "Whole-file Compact running").
  interface Fig {
    id: string;
    size: number;
    freeSpace: number;
    reclaim: ReclaimView | null;
    compactNeeds: number;
  }
  let lastFig = $state<Fig | null>(null);
  const live = $derived(!!space?.open && space.state !== "compacting" && space.state !== "needs_reopen");
  $effect(() => {
    if (space && live) lastFig = { id: space.id, size: space.storedSize, freeSpace: space.freeSpace, reclaim: space.reclaim ?? null, compactNeeds: space.compactNeeds ?? 0 };
  });
  const block = $derived.by(() => {
    if (!space) return null;
    const kept = !live && space.state === "compacting" && lastFig?.id === space.id ? lastFig : null;
    const fig = live ? { size: space.storedSize, freeSpace: space.freeSpace, reclaim: space.reclaim ?? null, compactNeeds: space.compactNeeds ?? 0 } : kept;
    return spaceBlock({
      open: !!fig,
      held: !fig && !!space.open && space.state === "compacting",
      size: fig?.size ?? space.storedSize,
      freeSpace: fig?.freeSpace ?? 0,
      reclaim: fig?.reclaim ?? null,
      compactNeeds: fig?.compactNeeds ?? 0,
      path: space.path,
      run: run ? { done: run.done, total: run.total, returned: run.returned } : null,
      runWhy,
      busyWhy,
      reclaimWhy,
      compactWhy,
    });
  });

  // The press is answered at once: the button stays greyed until the
  // core has answered, and the run's own operation greys it after that.
  let asking = $state(false);
  async function reclaim() {
    if (!space || asking) return;
    asking = true;
    try {
      await store.reclaimSpace(space.id);
    } finally {
      asking = false;
    }
  }
  // Cancel, as the foot's: once pressed it waits for the run to stop, and
  // a refusal (the commit in hand) gives the button back.
  let cancelling = $state("");
  async function cancel() {
    const id = run?.id;
    if (!id || cancelling === id) return;
    cancelling = id;
    try {
      await Archive.CancelOp(id);
    } catch (e) {
      cancelling = "";
      store.notice(codeText(errorOf(e).code), "error");
    }
  }

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
<Dialog title={technicalCopy.titleOf(d.name)} names={[d.name]} wide onclose={onclose} dismissable>
  {#if d.description}<p class="t-sub">{d.description}</p>{/if}
  {#if purge}
    <div class="bar attention"><svg class="i"><use href="#i-warn" /></svg><span>{chipCopy.forgottenOn(date(d.forgottenAt))}. Its key is dropped at the first unlock after {date(purge)}.</span></div>
  {/if}

  {#if block}
    {@const b = block}
    <!-- Unused space (APP.md §6): the head and its share, the bar, one
         line, the legend of the segments drawn, then each action with its
         hint beside it, linked by aria-describedby. The bar is aria-hidden:
         the line says it. A greyed button's hint is its reason. -->
    <section class="spaceblock" aria-labelledby="sb-title">
      <div class="sb-head"><span class="sb-title" id="sb-title">{spaceCopy.title}</span>{#if b.share}<span class="sb-share">{b.share}</span>{/if}</div>
      {#if b.bar}<SpaceBar size={b.bar.size} freeSpace={b.bar.freeSpace} back={b.bar.back} complete={b.bar.complete} tall />{/if}
      {#if b.strong || b.rest}<p class="sb-line">{#if b.strong}<strong>{b.strong}</strong>{/if}{b.rest}</p>{/if}
      {#if b.legend.length > 0}
        <div class="spacelegend">{#each b.legend as l (l.kind)}<span><i class={l.kind} aria-hidden="true"></i>{l.label}</span>{/each}</div>
      {/if}
      <div class="sb-acts">
        {#if b.reclaim?.cancel}
          <button type="button" class="btn" disabled={cancelling === run?.id} aria-describedby="sb-reclaim-hint" onclick={cancel}>{cancelling === run?.id ? footCopy.cancelling : footCopy.cancel}</button>
          <span class="sb-run" id="sb-reclaim-hint">
            <span class="fprog" class:indet={b.progress === null} role="progressbar" aria-label={spaceCopy.panelRunning} aria-valuemin={0} aria-valuemax={100} aria-valuenow={b.progress == null ? undefined : Math.round(b.progress * 100)}><i style:width={b.progress == null ? undefined : `${b.progress * 100}%`}></i></span>
            <span class="sb-hint">{b.reclaim.hint}</span>
          </span>
        {:else if b.reclaim}
          <button type="button" class="btn" disabled={b.reclaim.disabled || asking} title={b.reclaim.disabled ? b.reclaim.hint : undefined} aria-describedby="sb-reclaim-hint" onclick={reclaim}>{spaceCopy.reclaim}</button>
          <span class="sb-hint" id="sb-reclaim-hint">{b.reclaim.hint}</span>
        {/if}
        <button type="button" class="btn" disabled={b.compact.disabled || !oncompact} title={b.compact.disabled ? b.compact.hint : undefined} aria-describedby="sb-compact-hint" onclick={() => oncompact?.()}>{commandCopy.compact}</button>
        <span class="sb-hint" id="sb-compact-hint">{b.compact.hint}</span>
      </div>
    </section>
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

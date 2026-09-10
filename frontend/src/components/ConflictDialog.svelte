<script lang="ts">
  // The compare list of APP.md §3, after Windows Explorer's: a row per
  // file with the type's icon, *Files from the archive* on the left and
  // *Files already in the destination* on the right, each side's date and
  // size, a tick on either or both — both keeps both, which is the
  // `rename` policy — and "Skip N files with the same date and size" at
  // the foot, pre-ticked for the copies that match. *Continue* re-issues
  // the extract for the ids the ticks chose.
  //
  // Every rule it obeys is in lib/conflicts.ts and every word it says is
  // the strings table's (APP.md §7); this file draws it.
  import { untrack } from "svelte";
  import Dialog from "./Dialog.svelte";
  import { bytes, dateTime, fileIcon } from "../lib/format";
  import { compareCopy } from "../lib/strings";
  import { sameDateAndSize, skipSameLabel, startingDecisions, ticks, toggle, withSkipSame } from "../lib/conflicts";
  import type { ConflictRow, Decision } from "../lib/conflicts";

  interface Props {
    rows: ConflictRow[];
    oncontinue: (decisions: Record<string, Decision>) => void;
    oncancel: () => void;
  }
  let { rows, oncontinue, oncancel }: Props = $props();

  // The ticks are the user's from the moment the dialog opens: where they
  // start is the rule of §3, not something that follows the list.
  let decisions = $state<Record<string, Decision>>(untrack(() => startingDecisions(rows)));
  const sameLabel = $derived(skipSameLabel(rows));
  // The foot's box is ticked while every matching copy is being skipped,
  // which is how it starts.
  const skipSame = $derived(rows.filter(sameDateAndSize).every((r) => decisions[r.path] === "skip"));

  function hit(path: string, side: "left" | "right") {
    decisions = { ...decisions, [path]: toggle(decisions[path] ?? "replace", side) };
  }
</script>

<Dialog title={compareCopy.title} onclose={oncancel} wide>
  <div class="cmp-head">
    <span class="ch">{compareCopy.fromArchive}</span>
    <span class="ch">{compareCopy.inDestination}</span>
  </div>
  <ul class="cmp">
    {#each rows as r (r.path)}
      {@const t = ticks(decisions[r.path] ?? "replace")}
      <li>
        <label class="side">
          <input type="checkbox" checked={t.left} onchange={() => hit(r.path, "left")} />
          <svg class="i i-14"><use href="#{fileIcon(r.name)}" /></svg>
          <span class="who">
            <b title={r.path}>{r.name}</b>
            <small>{bytes(r.size)} · {dateTime(r.modifiedAt)}</small>
          </span>
        </label>
        <label class="side">
          <input type="checkbox" checked={t.right} onchange={() => hit(r.path, "right")} />
          <svg class="i i-14"><use href="#{fileIcon(r.name)}" /></svg>
          <span class="who">
            <b title={r.dest}>{r.name}</b>
            <small>{bytes(r.existingSize)} · {dateTime(r.existingModifiedAt)}</small>
          </span>
        </label>
      </li>
    {/each}
  </ul>

  {#if sameLabel}
    <label class="check foot"><input type="checkbox" checked={skipSame} onchange={(e) => (decisions = withSkipSame(rows, decisions, e.currentTarget.checked))} />{sameLabel}</label>
  {/if}
  <p class="dim">{compareCopy.note}</p>

  {#snippet actions()}
    <button type="button" class="btn" onclick={oncancel}>{compareCopy.cancel}</button>
    <button type="button" class="btn accent" onclick={() => oncontinue(decisions)}>{compareCopy.go}</button>
  {/snippet}
</Dialog>

<style>
  .cmp-head { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; padding: 0 0 6px; }
  .ch { font-size: 12.5px; font-weight: 600; color: var(--ink); }
  .cmp { list-style: none; margin: 0; padding: 0; max-height: 44vh; overflow-y: auto; }
  .cmp li { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; padding: 4px 0; border-top: 1px solid var(--stroke); }
  .side { display: flex; align-items: center; gap: 8px; min-width: 0; }
  .side input { accent-color: var(--accent); flex: none; }
  .side .i { flex: none; color: var(--ink-2); }
  .who { display: flex; flex-direction: column; min-width: 0; }
  .who b { font-size: 12.5px; font-weight: 500; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .who small { font-size: 11.5px; color: var(--ink-3); }
  .foot { padding-top: 10px; }
</style>

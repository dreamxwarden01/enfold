<script lang="ts">
  // The compare list of APP.md §3, after Windows Explorer's: a row per
  // file with the type's icon, *Files from the archive* on the left and
  // *Files already in the destination* on the right, each side's date and
  // size, a tick on either or both — both keeps both, which is the
  // `rename` policy, said once over the ticks — and "Skip N files with the
  // same date and size" at the foot, pre-ticked for the copies that match
  // (ruled 2026-10-17). *Continue* reads what it will do — "Extract 2,
  // skip 1" (direction 7) — and re-issues the extract for the ids the
  // ticks chose.
  //
  // Every rule it obeys is in lib/conflicts.ts and every word it says is
  // the strings table's (APP.md §7); this file draws it.
  import { untrack } from "svelte";
  import Dialog from "./Dialog.svelte";
  import { bytes, dateTime, fileIcon } from "../lib/format";
  import { compareCopy } from "../lib/strings";
  import { continueLabel, sameDateAndSize, skipSameLabel, startingDecisions, ticks, toggle, withSkipSame } from "../lib/conflicts";
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
  // What Continue will do, said on Continue itself before it does it; it
  // follows every tick.
  const go = $derived(continueLabel(rows, decisions));

  function hit(path: string, side: "left" | "right") {
    decisions = { ...decisions, [path]: toggle(decisions[path] ?? "replace", side) };
  }
</script>

<!-- Esc is Cancel, which re-issues nothing — every conflict skipped, never
     replaced (APP.md §7); the backdrop is nothing. -->
<Dialog title={compareCopy.title} onclose={oncancel} wide>
  <div class="cmp-head">
    <span class="ch">{compareCopy.fromArchive}</span>
    <span class="ch">{compareCopy.inDestination}</span>
    <!-- The ticks' one rule, said once over them (direction 7). -->
    <span class="tickrule">{compareCopy.keepBoth}</span>
  </div>
  <ul class="cmp">
    {#each rows as r (r.path)}
      {@const t = ticks(decisions[r.path] ?? "replace")}
      <li>
        <label class="side">
          <input type="checkbox" checked={t.left} onchange={() => hit(r.path, "left")} />
          <svg class="i i-14"><use href="#{fileIcon(r.name)}" /></svg>
          <span class="two-line who">
            <b title={r.path}>{r.name}</b>
            <small>{bytes(r.size)} · {dateTime(r.modifiedAt)}</small>
          </span>
        </label>
        <label class="side">
          <input type="checkbox" checked={t.right} onchange={() => hit(r.path, "right")} />
          <svg class="i i-14"><use href="#{fileIcon(r.name)}" /></svg>
          <span class="two-line who">
            <b title={r.dest}>{r.name}</b>
            <small>{bytes(r.existingSize)} · {dateTime(r.existingModifiedAt)}</small>
          </span>
        </label>
      </li>
    {/each}
  </ul>

  {#if sameLabel}
    <label class="check"><input type="checkbox" checked={skipSame} onchange={(e) => (decisions = withSkipSame(rows, decisions, e.currentTarget.checked))} />{sameLabel}</label>
  {/if}

  {#snippet actions()}
    <button type="button" class="btn accent" onclick={() => oncontinue(decisions)}>{go}</button>
    <button type="button" class="btn" onclick={oncancel}>{compareCopy.cancel}</button>
  {/snippet}
</Dialog>

<style>
  /* The type and the grid are the ramp's (APP.md §6, Type and spacing):
     the two heads strong, a name body over its date and size in caption
     (.two-line), and every gap a step of 4. The parts of the dialog stand
     12 apart by the dialog's own gap. */
  .cmp-head { display: grid; grid-template-columns: 1fr 1fr; gap: 0 var(--s3); }
  .ch { font: var(--t-strong); color: var(--ink); }
  /* Centred across both heads, so it reads as the rule over both columns. */
  .tickrule { grid-column: 1 / -1; text-align: center; font: var(--t-cap); color: var(--ink-2); }
  .cmp { list-style: none; margin: 0; padding: 0; max-height: 44vh; overflow-y: auto; }
  .cmp li { display: grid; grid-template-columns: 1fr 1fr; gap: var(--s3); padding: var(--s1) 0; border-top: 1px solid var(--stroke); }
  .side { display: flex; align-items: center; gap: var(--s2); min-width: 0; }
  .side input { flex: none; }
  .side .i { flex: none; color: var(--ink-3); }
  /* One line each, cut with an ellipsis: the whole path is the name's title. */
  .who > * { white-space: nowrap; }
</style>

<script lang="ts">
  // What a batch left out (APP.md §3, FileOutcome; direction 4, phase 3).
  // The title is the outcome, counted — "2 files were not added" — with
  // the summary under it as a caption; then each item, its name strong
  // and the code's own copy after a colon, with Show in Explorer where the
  // item is a file on disk (an add's source). Copy details and Close are
  // equal, standard buttons: nothing here is the suggested act. It opens
  // on the page the batch ran on at once, and anywhere from the done
  // line's Details, so it stands above every route (App.svelte).
  import { Clipboard } from "@wailsio/runtime";
  import type { OpView } from "../lib/api";
  import { Shell, errorOf } from "../lib/api";
  import { store } from "../lib/state.svelte";
  import { detailsText, resultsTitle, rowName, summaryLine, tally, troubles } from "../lib/results";
  import { codeText, commandCopy, resultsCopy, unknownCodeParts } from "../lib/strings";
  import Dialog from "./Dialog.svelte";

  interface Props {
    op: OpView;
    onclose: () => void;
  }
  let { op, onclose }: Props = $props();

  const rows = $derived(troubles(op.results, op.kind));
  const title = $derived(resultsTitle(op.kind, op.results));
  const summary = $derived(summaryLine(tally(op.results)));
  // Twelve rows and a count of the rest, as before: the copy carries all.
  const SHOWN = 12;

  // Clipboard.SetText is the runtime's, as the Details dialog's Copy is:
  // navigator.clipboard is not dependable under the WebView2 custom scheme.
  async function copy() {
    try {
      await Clipboard.SetText(detailsText(title, summary, rows));
      store.notice(resultsCopy.copied, "success");
    } catch {
      store.notice(resultsCopy.copyFailed, "error");
    }
  }

  async function show(path: string) {
    try {
      await Shell.Reveal(path);
    } catch (e) {
      store.notice(codeText(errorOf(e).code), "error");
    }
  }
</script>

<Dialog {title} {onclose} dismissable>
  {#if summary}<p class="cap">{summary}</p>{/if}
  <ul class="plain results">
    {#each rows.slice(0, SHOWN) as t, i (i)}
      {@const parts = unknownCodeParts(t.text)}
      <li>
        <span class="what"><b>{rowName(t)}</b>: {#if parts}{parts[0]}<span class="mono">{parts[1]}</span>{parts[2]}{:else}{t.text}{/if}</span>
        {#if t.path}<button type="button" class="btn link" onclick={() => void show(t.path)}>{commandCopy.showInExplorer}</button>{/if}
      </li>
    {/each}
    {#if rows.length > SHOWN}<li>{resultsCopy.more(rows.length - SHOWN)}</li>{/if}
  </ul>
  {#snippet actions()}
    <button type="button" class="btn" onclick={() => void copy()}>{resultsCopy.copyDetails}</button>
    <button type="button" class="btn" onclick={onclose}>{commandCopy.close}</button>
  {/snippet}
</Dialog>

<style>
  .cap { margin: 0; font: var(--t-cap); color: var(--ink-2); }
  .results li { display: flex; align-items: baseline; gap: var(--s3); }
  .results .what { flex: 1; min-width: 0; }
  .results b { color: var(--ink); font-weight: 600; }
  .results .btn.link { flex: none; }
</style>

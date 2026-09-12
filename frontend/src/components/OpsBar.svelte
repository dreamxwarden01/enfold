<script lang="ts">
  // The operation strip (APP.md §2.3, §6): the running operation's name
  // with what it is working on — *Adding 3 files*, *Extracting 12 files*,
  // *Reclaiming space* — a bar that moves by *bytes* — an add, a replace
  // and an extract count the bytes of the file in hand, so a single large
  // file moves it — and *Cancel* for an add, a replace or a reclaim, which
  // aborts its transaction and publishes nothing. The phase stands beside
  // the name only where it says something the name does not (lib/ops.ts).
  // There is no pending bar, no Save and no Discard: every operation
  // commits at its end. A drag out of the window is on the strip from the
  // moment the drag starts, under one label — *Extracting 2 items* (APP.md
  // §3, ruled 2026-09-11): the label alone during the hover, the byte bar
  // with *Cancel* while the drop's request runs the extraction, and then
  // that same bar standing full, with nothing to cancel, for as long as
  // Explorer is inside its own Drop — its window is in front by then — until
  // DoDragDrop returns, the operation ends and the strip goes.
  import { Archive, errorOf } from "../lib/api";
  import { store } from "../lib/state.svelte";
  import { cancellable, hasBar, opLabel, opPhase, stripCount } from "../lib/ops";
  import { codeText } from "../lib/strings";
  import { bytes } from "../lib/format";

  async function cancel(opId: string) {
    try {
      await Archive.CancelOp(opId);
    } catch (e) {
      store.toast(codeText(errorOf(e).code), "error");
    }
  }

  // Every running operation is on the strip, from the moment it begins:
  // nothing is held back any more, the drag out's hover included.
  const shown = $derived(store.runningOps);
</script>

{#if shown.length > 0}
  <div class="ops">
    {#each shown as o (o.id)}
      <div class="op">
        <span class="opname">{opLabel(o.kind, stripCount(o), o.phase)}</span>
        {#if opPhase(o.phase)}<span class="opphase">{opPhase(o.phase)}</span>{/if}
        {#if hasBar(o.kind, o.phase)}
          <progress value={o.total > 0 ? o.done : undefined} max={o.total > 0 ? o.total : undefined}></progress>
          {#if o.total > 0}<span class="num opbytes">{bytes(o.done)} of {bytes(o.total)}</span>{/if}
        {:else}
          <span class="grow"></span>
        {/if}
        {#if cancellable(o.kind, o.phase)}
          <button type="button" class="btn sm" onclick={() => void cancel(o.id)}>Cancel</button>
        {/if}
      </div>
    {/each}
  </div>
{/if}

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
  // moment the drag starts (APP.md §3, ruled 2026-09-11): the label alone
  // during the hover — *Dragging 2 items*, nothing chosen yet (ruled
  // 2026-10-14) — and *Extracting 2 items* from the drop on: the byte bar
  // with *Cancel* while the drop's request runs the extraction, and then
  // that same bar standing full for as long as Explorer is inside its own
  // Drop — its window is in front by then — with *Cancel* still in its
  // place, greyed and unclickable, because a button that vanishes reflows
  // the strip; and at DoDragDrop's return the operation ends and the strip
  // goes. Each operation names its archive (lib/ops.ts opLabel, ruled
  // 2026-10-14): the strip shows them all, on the Archives page too.
  import { Archive, errorOf } from "../lib/api";
  import { store } from "../lib/state.svelte";
  import { cancellable, hasBar, opLabel, opPhase, showsCancel, stripCount } from "../lib/ops";
  import { codeText } from "../lib/strings";
  import { bytes } from "../lib/format";

  // The operations whose Cancel was pressed: the button says
  // *Cancelling…* and stays greyed until the operation's end takes the row
  // away (ruled 2026-10-14) — a cancel waits for the work in hand to stop,
  // and a button that looked pressable meanwhile invited a second press.
  // A refusal (the operation already committing) gives the button back.
  let cancelling = $state<string[]>([]);

  async function cancel(opId: string) {
    if (cancelling.includes(opId)) return;
    cancelling.push(opId);
    try {
      await Archive.CancelOp(opId);
    } catch (e) {
      cancelling = cancelling.filter((id) => id !== opId);
      store.toast(codeText(errorOf(e).code), "error");
    }
  }

  // The registry row's name for each archive: OpView carries the id only.
  const names = $derived(new Map(store.archives.map((a) => [a.id, a.name])));

  // Every running operation is on the strip, from the moment it begins:
  // nothing is held back any more, the drag out's hover included.
  const shown = $derived(store.runningOps);
</script>

{#if shown.length > 0}
  <div class="ops">
    {#each shown as o (o.id)}
      {@const label = opLabel(o.kind, stripCount(o), o.phase, names.get(o.archiveId ?? "") ?? "")}
      <div class="op">
        <span class="opname ellipsis" title={label}>{label}</span>
        {#if opPhase(o.phase)}<span class="opphase">{opPhase(o.phase)}</span>{/if}
        {#if hasBar(o.kind, o.phase)}
          <progress value={o.total > 0 ? o.done : undefined} max={o.total > 0 ? o.total : undefined}></progress>
          {#if o.total > 0}<span class="num opbytes">{bytes(o.done)} of {bytes(o.total)}</span>{/if}
        {:else}
          <span class="grow"></span>
        {/if}
        {#if showsCancel(o.kind, o.phase)}
          {@const pending = cancelling.includes(o.id)}
          {@const can = cancellable(o.kind, o.phase) && !pending}
          <button
            type="button"
            class="btn sm"
            disabled={!can}
            aria-disabled={!can}
            onclick={can ? () => void cancel(o.id) : undefined}>{pending ? "Cancelling…" : "Cancel"}</button>
        {/if}
      </div>
    {/each}
  </div>
{/if}

<style>
  /* A long archive name gives way before the bar does: the label shrinks
     to an ellipsis (its title has the whole) rather than squeezing the
     progress out of the row. */
  .op .opname { flex: 0 1 auto; min-width: 0; max-width: 60%; }
</style>

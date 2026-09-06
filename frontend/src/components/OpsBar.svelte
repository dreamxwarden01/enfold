<script lang="ts">
  import { Archive } from "../lib/api";
  import { store, opLabel } from "../lib/state.svelte";
</script>

{#if store.runningOps.length > 0}
  <div class="ops">
    {#each store.runningOps as o (o.id)}
      <div class="op">
        <span>{opLabel(o.kind)}{o.phase ? ` — ${o.phase}` : ""}</span>
        <progress value={o.total > 0 ? o.done : undefined} max={o.total > 0 ? o.total : undefined}></progress>
        <button type="button" class="btn sm" onclick={() => void Archive.CancelOp(o.id)}>Cancel</button>
      </div>
    {/each}
  </div>
{/if}

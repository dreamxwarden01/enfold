<script lang="ts">
  import { fly } from "svelte/transition";
  import { store } from "../lib/state.svelte";
  import { motion, enter, exit, FAST, LEAVE } from "../lib/motion";
</script>

<!-- Top of the window, centred on it (APP.md §6): a toast drops in a few pixels as it fades and rises as it leaves. -->
<div class="toasts" aria-live="polite">
  {#each store.toasts as t (t.id)}
    <div class="toast" class:error={t.kind === "error"} in:fly={motion(FAST, { y: -6, easing: enter })} out:fly={motion(LEAVE, { y: -6, easing: exit })}>{t.text}</div>
  {/each}
</div>

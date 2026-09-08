<script lang="ts">
  import { fade, fly } from "svelte/transition";
  import { store } from "../lib/state.svelte";
  import { motion, enter, exit, LEAVE } from "../lib/motion";
</script>

<div class="toasts" aria-live="polite">
  {#each store.toasts as t (t.id)}
    <div class="toast" class:error={t.kind === "error"} in:fly={motion(140, { y: 6, easing: enter })} out:fade={motion(LEAVE, { easing: exit })}>{t.text}</div>
  {/each}
</div>

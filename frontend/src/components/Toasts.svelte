<script lang="ts">
  import { fade } from "svelte/transition";
  import { store } from "../lib/state.svelte";
  import { motion } from "../lib/motion";
</script>

<div class="toasts" aria-live="polite">
  {#each store.toasts as t (t.id)}
    <div class="toast" class:error={t.kind === "error"} transition:fade={motion()}>{t.text}</div>
  {/each}
</div>

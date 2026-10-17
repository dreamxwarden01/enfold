<script lang="ts">
  import { fly } from "svelte/transition";
  import { store } from "../lib/state.svelte";
  import { unknownCodeParts } from "../lib/strings";
  import { motion, enter, exit, FAST, LEAVE } from "../lib/motion";
</script>

<!-- Top of the window, centred on it (APP.md §6): a toast drops in a few pixels as it fades and rises as it leaves. -->
<div class="toasts" aria-live="polite">
  {#each store.toasts as t (t.id)}
    {@const parts = unknownCodeParts(t.text)}
    <div class="toast" class:error={t.kind === "error"} in:fly={motion(FAST, { y: -6, easing: enter })} out:fly={motion(LEAVE, { y: -6, easing: exit })}>
      <!-- A code with no words of its own (strings.ts codeText) is there
           for the log, not for reading: muted and in mono, so the sentence
           around it reads first (ruled 2026-10-14). -->
      {#if parts}{parts[0]}<span class="mono code">{parts[1]}</span>{parts[2]}{:else}{t.text}{/if}
    </div>
  {/each}
</div>

<style>
  .code { user-select: text; }
</style>

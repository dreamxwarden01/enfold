<script lang="ts">
  import type { Snippet } from "svelte";

  interface Props {
    title: string;
    onclose?: () => void;
    children: Snippet;
    actions?: Snippet;
    wide?: boolean;
  }
  let { title, onclose, children, actions, wide = false }: Props = $props();

  function key(e: KeyboardEvent) {
    if (e.key === "Escape" && onclose) onclose();
  }
</script>

<svelte:window onkeydown={key} />

<div class="backdrop" role="presentation" onclick={(e) => { if (e.target === e.currentTarget && onclose) onclose(); }}>
  <div class="dialog" class:wide role="dialog" aria-modal="true" aria-label={title}>
    <h2>{title}</h2>
    {@render children()}
    {#if actions}
      <div class="actions">{@render actions()}</div>
    {/if}
  </div>
</div>

<style>
  .dialog.wide { width: 640px; }
</style>

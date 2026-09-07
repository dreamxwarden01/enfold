<script lang="ts">
  import type { Snippet } from "svelte";
  import { fade, scale } from "svelte/transition";
  import { FAST, LEAVE, motion } from "../lib/motion";

  interface Props {
    title: string;
    onclose?: () => void;
    children: Snippet;
    actions?: Snippet;
    wide?: boolean;
  }
  let { title, onclose, children, actions, wide = false }: Props = $props();

  // Once the dialog is closed it answers nothing while it fades out: the
  // window listener lives until the fade ends, and a second Escape in
  // that window must not reach a dialog the user has already closed.
  // Svelte marks the element inert the moment its outro begins and
  // clears that if the dialog is reopened mid-fade, so inert is the flag.
  let backdrop: HTMLDivElement | undefined = $state();
  const leaving = () => backdrop?.inert === true;

  function key(e: KeyboardEvent) {
    if (e.key === "Escape" && onclose && !leaving()) onclose();
  }
</script>

<svelte:window onkeydown={key} />

<div class="backdrop" role="presentation" bind:this={backdrop} in:fade|global={motion()} out:fade|global={motion(LEAVE)} onclick={(e) => { if (e.target === e.currentTarget && onclose && !leaving()) onclose(); }}>
  <div class="dialog" class:wide role="dialog" aria-modal="true" aria-label={title} in:scale|global={motion(FAST, { start: 0.97 })} out:fade|global={motion(LEAVE)}>
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

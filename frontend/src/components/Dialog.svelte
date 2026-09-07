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
    // covered: another dialog stands over this one — it answers no key or
    // click and leaves the tab order until that one closes.
    covered?: boolean;
  }
  let { title, onclose, children, actions, wide = false, covered = false }: Props = $props();

  // Focus moves into a dialog as it opens, so that a keyboard user is in
  // it and not still on the button that opened it.
  let box: HTMLDivElement | undefined = $state();
  $effect(() => {
    box?.focus();
  });

  // Once the dialog is closed it answers nothing while it fades out: the
  // window listener lives until the fade ends, and a second Escape in
  // that window must not reach a dialog the user has already closed.
  // Svelte marks the element inert the moment its outro begins and
  // clears that if the dialog is reopened mid-fade, so inert is the flag.
  let backdrop: HTMLDivElement | undefined = $state();
  const leaving = () => backdrop?.inert === true;

  function key(e: KeyboardEvent) {
    if (e.key === "Escape" && onclose && !leaving() && !covered) onclose();
  }
</script>

<svelte:window onkeydown={key} />

<div class="backdrop" role="presentation" bind:this={backdrop} in:fade|global={motion()} out:fade|global={motion(LEAVE)} onclick={(e) => { if (e.target === e.currentTarget && onclose && !leaving()) onclose(); }}>
  <div class="dialog" class:wide role="dialog" aria-modal={covered ? undefined : true} aria-label={title} tabindex="-1" bind:this={box} inert={covered || undefined} in:scale|global={motion(FAST, { start: 0.97 })} out:fade|global={motion(LEAVE)}>
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

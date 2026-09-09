<script lang="ts">
  // A button and the menu under it (APP.md §6, Menus). The list appears
  // whole, falling 4 px and fading in over the vocabulary's fast, and
  // fades out over its leave — the select's picker, hand-built, because a
  // menu of actions is not a <select>. It opens on a click, closes on
  // Escape, on a click outside and after an item is taken, and its
  // keyboard is lib/menu.ts's machine.
  import { fade, fly } from "svelte/transition";
  import { FAST, LEAVE, motion } from "../lib/motion";
  import { buttonKey, menuKey } from "../lib/menu";

  export interface MenuItem {
    label: string;
    icon?: string;
    run: () => void;
  }

  interface Props {
    id: string;
    label: string;
    icon?: string;
    items: MenuItem[];
    disabled?: boolean;
  }
  let { id, label, icon = "", items, disabled = false }: Props = $props();

  let open = $state(false);
  let active = $state(-1);
  let wrap: HTMLDivElement | undefined = $state();
  let button: HTMLButtonElement | undefined = $state();
  let itemEls: (HTMLButtonElement | undefined)[] = $state([]);

  // The focus is the menu's state: it moves to the item the machine made
  // active, and back to the button when the menu closes, so a keyboard
  // never lands where the menu used to be.
  $effect(() => {
    if (open && active >= 0) itemEls[active]?.focus();
  });

  function close(toButton = true): void {
    if (!open) return;
    open = false;
    active = -1;
    if (toButton) button?.focus();
  }

  function choose(i: number): void {
    const item = items[i];
    close();
    item?.run();
  }

  function toggle(): void {
    if (open) close();
    else {
      open = true;
      active = -1;
    }
  }

  function key(e: KeyboardEvent): void {
    const act = open ? menuKey(e.key, active, items.length) : buttonKey(e.key, items.length);
    switch (act.kind) {
      case "open":
        e.preventDefault();
        open = true;
        active = act.active;
        break;
      case "move":
        e.preventDefault();
        active = act.active;
        break;
      case "choose":
        e.preventDefault();
        choose(act.index);
        break;
      case "close":
        // Tab closes and goes on its way; Escape only closes.
        if (e.key !== "Tab") e.preventDefault();
        close(e.key !== "Tab");
        break;
    }
  }

  // A press anywhere else closes the menu, and the press itself stands:
  // the control it landed on is the one the user meant.
  function outside(e: PointerEvent): void {
    if (open && wrap && !wrap.contains(e.target as Node)) close(false);
  }
</script>

<svelte:window onpointerdown={outside} />

<div class="menu-wrap" bind:this={wrap}>
  <button
    type="button" class="btn subtle" {id} bind:this={button} {disabled}
    aria-haspopup="menu" aria-expanded={open} aria-controls="{id}-menu"
    onclick={toggle} onkeydown={key}
  >
    {#if icon}<svg class="i i-14"><use href="#{icon}" /></svg>{/if}
    {label}
    <svg class="i i-14 caret" class:up={open}><use href="#i-chevdown" /></svg>
  </button>
  {#if open}
    <div class="menu" id="{id}-menu" role="menu" tabindex="-1" aria-labelledby={id} onkeydown={key} in:fly|global={motion(FAST, { y: -4 })} out:fade|global={motion(LEAVE)}>
      {#each items as item, i (item.label)}
        <button type="button" role="menuitem" tabindex={i === active ? 0 : -1} bind:this={itemEls[i]} onclick={() => choose(i)}>
          {#if item.icon}<svg class="i i-14"><use href="#{item.icon}" /></svg>{/if}
          {item.label}
        </button>
      {/each}
    </div>
  {/if}
</div>

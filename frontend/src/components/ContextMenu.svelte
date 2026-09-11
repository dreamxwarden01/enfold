<script lang="ts">
  // A menu opened by a right-click (APP.md §13): the details modal's
  // values are selectable and carry one item, *Copy*. The shell disables
  // WebView2's own context menu, so this is the only one on the page.
  //
  // It is the button menu's machine (lib/menu.ts) without a button: the
  // arrows walk it, Enter and Space take the item under the focus, Escape
  // and Tab close it, a press anywhere else closes it, and the box is
  // placed at the pointer and pulled back inside the window when it would
  // hang off an edge. It is rendered as the dialog's sibling, so it marks
  // itself a popover (data-popover, lib/dialogs.ts) and the dialog's trap
  // leaves the focus in it; Escape closes the menu alone — prevented
  // here, so the dialog reads it as taken.
  import { untrack } from "svelte";
  import { fade, fly } from "svelte/transition";
  import { FAST, LEAVE, motion } from "../lib/motion";
  import { contextPlacement, menuKey } from "../lib/menu";
  import type { MenuItem } from "./MenuButton.svelte";

  interface Props {
    x: number;
    y: number;
    items: MenuItem[];
    onclose: () => void;
  }
  let { x, y, items, onclose }: Props = $props();

  let active = $state(0);
  let box: HTMLDivElement | undefined = $state();
  let itemEls: (HTMLButtonElement | undefined)[] = $state([]);
  // Where the box actually sits: the pointer to begin with — the props do
  // not move once the menu is open, one press being one menu — then the
  // placement once it has been measured.
  let at = $state(untrack(() => ({ x, y })));

  // Placed once it has a size: the box is measured, then moved, so a menu
  // opened against the right edge or the foot of the window is whole.
  $effect(() => {
    const el = box;
    if (!el) return;
    at = contextPlacement({ x, y }, { width: el.offsetWidth, height: el.offsetHeight }, { width: window.innerWidth, height: window.innerHeight });
    itemEls[active]?.focus();
  });

  function choose(i: number): void {
    const item = items[i];
    onclose();
    item?.run();
  }

  function key(e: KeyboardEvent): void {
    const act = menuKey(e.key, active, items.length);
    switch (act.kind) {
      case "move":
        e.preventDefault();
        active = act.active;
        itemEls[act.active]?.focus();
        break;
      case "choose":
        e.preventDefault();
        choose(act.index);
        break;
      case "close":
        if (e.key !== "Tab") e.preventDefault();
        onclose();
        break;
    }
  }

  // A press anywhere else closes it, and the press itself stands. A
  // second right-click elsewhere opens the menu there, which is why this
  // is pointerdown and not click.
  function outside(e: PointerEvent): void {
    if (box && !box.contains(e.target as Node)) onclose();
  }
</script>

<svelte:window onpointerdown={outside} onresize={onclose} />

<div
  class="ctxmenu" role="menu" tabindex="-1" bind:this={box}
  data-popover=""
  style="left: {at.x}px; top: {at.y}px"
  onkeydown={key}
  oncontextmenu={(e) => e.preventDefault()}
  in:fly|global={motion(FAST, { y: -4 })}
  out:fade|global={motion(LEAVE)}
>
  {#each items as item, i (item.label)}
    <button type="button" role="menuitem" tabindex={i === active ? 0 : -1} bind:this={itemEls[i]} onclick={() => choose(i)}>
      {#if item.icon}<svg class="i i-14"><use href="#{item.icon}" /></svg>{/if}
      {item.label}
    </button>
  {/each}
</div>

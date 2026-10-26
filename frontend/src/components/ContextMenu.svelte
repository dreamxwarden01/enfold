<script lang="ts">
  // The app's own context menu (APP.md §13; direction 7, Explorer's
  // habits): what a right-click opens on a row of either list, on a list's
  // blank area, in a text field that is not a secret (lib/fieldmenu.ts) and
  // on a value in the details modal — and what Shift+F10 and the Menu key
  // open on the focused row. 2026-09-06 turned WebView2's own context menu
  // off (its "Save image as" wrote a decrypted preview to Downloads); that
  // ruling stands, and this menu is not that one: it is drawn by the page
  // and offers only the app's commands.
  //
  // It says what a button's menu says (MenuButton; app.css .menu): a
  // separator between groups, the default item — what Enter and a
  // double-click do — bold and first, a command's key at its right in the
  // caption ink, the danger ink for a command that destroys, and a command
  // that cannot run now greyed with why as its second line, still reached
  // by the arrows. Its keyboard is lib/menu.ts's machine: the arrows walk
  // it, Enter and Space take the item under the focus, Escape and Tab close
  // it. A press anywhere else, a scroll under it, a resize or the window
  // losing the focus closes it. It is placed at the pointer — or, opened
  // from the keyboard, where its opener says (lib/menu.ts keyAnchor) — and
  // pulled back inside the window when it would hang off an edge. It
  // marks itself a popover (data-popover, lib/dialogs.ts), so a dialog's
  // trap leaves the focus in it; Escape closes the menu alone — prevented
  // here, so a dialog under it reads the key as taken.
  import { untrack } from "svelte";
  import { fade, fly } from "svelte/transition";
  import { FAST, LEAVE, motion } from "../lib/motion";
  import { contextPlacement, menuKey, secondLine } from "../lib/menu";
  import type { MenuClose } from "../lib/menu";
  import type { MenuItem } from "./MenuButton.svelte";

  interface Props {
    x: number;
    y: number;
    items: MenuItem[];
    // onclose says how it closed (lib/menu.ts MenuClose), so the opener
    // puts the focus back on a key or a command and not after a press
    // elsewhere. A chosen command runs after onclose.
    onclose: (how: MenuClose) => void;
    // label: the menu's accessible name — the row's name, the field's.
    label?: string;
    // keyboard: opened by Shift+F10 or the Menu key, so the first item
    // takes the focus at once, as Windows highlights it; a menu opened by
    // the pointer highlights nothing until an arrow is pressed.
    keyboard?: boolean;
  }
  let { x, y, items, onclose, label, keyboard = false }: Props = $props();

  let active = $state(untrack(() => (keyboard ? 0 : -1)));
  let box: HTMLDivElement | undefined = $state();
  let itemEls: (HTMLButtonElement | undefined)[] = $state([]);
  // Where the box actually sits: the point to begin with — the props do
  // not move once the menu is open, one press being one menu — then the
  // placement once it has been measured.
  let at = $state(untrack(() => ({ x, y })));
  // An icon's slot on every item when any item has an icon, so the labels
  // line up.
  const icons = $derived(items.some((i) => !!i.icon));

  // Placed once it has a size: the box is measured, then moved, so a menu
  // opened against the right edge or the foot of the window is whole. The
  // focus goes into it — on the first item from the keyboard, on the box
  // itself from the pointer, where the arrows then start.
  $effect(() => {
    const el = box;
    if (!el) return;
    untrack(() => {
      at = contextPlacement({ x, y }, { width: el.offsetWidth, height: el.offsetHeight }, { width: window.innerWidth, height: window.innerHeight });
      (active >= 0 ? itemEls[active] : el)?.focus({ preventScroll: true });
    });
  });

  // Placed again when its items change while it stands: the blank area's
  // *Paste* learns what the clipboard holds after the menu is up (ruled
  // 2026-10-26; lib/paste.ts), and its caption line for a cut makes the
  // box taller — a menu opened near the window's foot is kept whole.
  $effect(() => {
    void items;
    const el = box;
    if (!el) return;
    untrack(() => {
      at = contextPlacement({ x, y }, { width: el.offsetWidth, height: el.offsetHeight }, { width: window.innerWidth, height: window.innerHeight });
    });
  });

  // One close per menu, whatever closes it first.
  let closed = false;
  function close(how: MenuClose): void {
    if (closed) return;
    closed = true;
    onclose(how);
  }

  function choose(i: number): void {
    const item = items[i];
    if (!item || item.disabled) return;
    close("choose");
    item.run();
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
        close("key");
        break;
    }
  }

  // A press anywhere else closes it, and the press itself stands. A
  // second right-click elsewhere opens the menu there, which is why this
  // is pointerdown and not click.
  function outside(e: PointerEvent): void {
    if (box && !box.contains(e.target as Node)) close("pointer");
  }

  // A scroll under it would leave it standing away from what it is about,
  // as a resize would: either closes it, as a native menu does. The wheel
  // is the user scrolling and closes it at once. A scroll event closes it
  // only once the menu has stood two frames: the opener may have just
  // brought its row into view (Shift+F10 on a row scrolled out of sight,
  // ArchivePage/ArchivesPage keyMenu), and the browser sends that scroll
  // on the next frame, after the menu is up and measured against the row
  // where it now stands — that scroll is the opening, not a reason to
  // close. The scrollbar is a press, which closes it already.
  let armed = false;
  $effect(() => {
    let id = requestAnimationFrame(() => {
      id = requestAnimationFrame(() => {
        armed = true;
      });
    });
    return () => cancelAnimationFrame(id);
  });
  function scrolled(e: Event): void {
    if (!armed) return;
    if (e.target instanceof Node && box?.contains(e.target)) return;
    close("pointer");
  }
  function wheeled(e: WheelEvent): void {
    if (e.target instanceof Node && box?.contains(e.target)) return;
    close("pointer");
  }
</script>

<svelte:window onpointerdown={outside} onresize={() => close("pointer")} onblur={() => close("pointer")} onscrollcapture={scrolled} onwheelcapture={wheeled} />

<div
  class="menu ctxmenu" role="menu" tabindex="-1" aria-label={label} bind:this={box}
  data-popover=""
  style="left: {at.x}px; top: {at.y}px"
  onkeydown={key}
  oncontextmenu={(e) => e.preventDefault()}
  in:fly|global={motion(FAST, { y: -4 })}
  out:fade|global={motion(LEAVE)}
>
  {#each items as item, i (i)}
    {#if item.sep && i > 0}<div class="msep" role="separator"></div>{/if}
    <button
      type="button" role="menuitem" tabindex={i === active || (active < 0 && i === 0) ? 0 : -1} bind:this={itemEls[i]}
      class:danger={item.danger} class:default={item.isDefault} class:two={!!secondLine(item)}
      aria-disabled={item.disabled || undefined} title={item.disabled ? undefined : item.title}
      onclick={() => choose(i)}
    >
      {#if item.icon}<svg class="i i-14" aria-hidden="true"><use href="#{item.icon}" /></svg>{:else if icons}<span class="i i-14" aria-hidden="true"></span>{/if}
      {#if secondLine(item)}
        <span class="mlabel"><span>{item.label}</span><span class="mwhy">{secondLine(item)}</span></span>
      {:else}
        <span class="mlabel">{item.label}</span>
      {/if}
      {#if item.keys}<span class="acc">{item.keys}</span>{/if}
    </button>
  {/each}
</div>

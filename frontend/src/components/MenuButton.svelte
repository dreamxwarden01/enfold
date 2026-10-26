<script lang="ts">
  // A button and the menu under it (APP.md §6, Menus). The list appears
  // whole, falling 4 px and fading in over the vocabulary's fast, and
  // fades out over its leave — the select's picker, hand-built, because a
  // menu of actions is not a <select>. It opens on a click, closes on
  // Escape, on a click outside and after an item is taken, and its
  // keyboard is lib/menu.ts's machine.
  //
  // What a menu says, as menus say it everywhere (direction 3, phase 4a):
  // a separator sets a group apart, an item's key stands at its right as
  // an accelerator does, a setting shown or hidden carries a tick, a
  // command that destroys is in the danger ink, and one that cannot run
  // now is greyed with why as a second line under its label, in the
  // caption (direction 6, lib/menu.ts secondLine) — still reachable by the
  // arrows, so the keyboard reads why as the eye does.
  import { fade, fly } from "svelte/transition";
  import { FAST, LEAVE, motion } from "../lib/motion";
  import { buttonKey, dropPlacement, menuKey, secondLine } from "../lib/menu";
  import { tip } from "../lib/tip";

  export interface MenuItem {
    label: string;
    icon?: string;
    run: () => void;
    // sep: a separator is drawn above this item.
    sep?: boolean;
    // keys: the key that runs the command anywhere, shown at the right.
    keys?: string;
    // checked: a setting the item turns on and off; the tick says it is on.
    checked?: boolean;
    danger?: boolean;
    // isDefault: what Enter and a double-click on the row do — a row's
    // menu draws it bold and first (direction 7, as Explorer's do).
    isDefault?: boolean;
    disabled?: boolean;
    // title: why a greyed item is greyed — drawn as its second line — or,
    // for one that can run, what it does, as the native tooltip.
    title?: string;
    // cut: the label is cut at a crumb's cap (lib/crumbs.ts) with its whole
    // name in the app's tooltip — the crumbs' ellipsis menu.
    cut?: boolean;
  }

  interface Props {
    id: string;
    label: string;
    icon?: string;
    items: MenuItem[];
    disabled?: boolean;
    // small: the 28 px subtle button, for a quiet command among others
    // (the lock screen's *Backups and copies…*, the crumbs' ellipsis).
    small?: boolean;
    // standard: the standard button rather than the subtle one, for a menu
    // that stands among standard buttons (the Archives pane's *More*).
    standard?: boolean;
    // iconOnly: the icon alone, the label its accessible name and no
    // caret (the toolbar's More, the crumbs' ellipsis).
    iconOnly?: boolean;
    // title: the button's native title; tipText the app's own tooltip,
    // for the head's crumbs (lib/tip.ts).
    title?: string;
    tipText?: string;
    // end: the menu's right edge on the button's, for a button at the
    // right of its bar.
    end?: boolean;
    // maxRows: the rows the menu shows before it scrolls (lib/crumbs.ts
    // menuRows); none shows every item.
    maxRows?: number;
    // float: the menu drawn over the window rather than inside the button's
    // own box, for a button in a box that clips — the Archives pane, which
    // scrolls, and the bottom panel, whose card clips its height as it
    // moves (direction 6; lib/menu.ts dropPlacement). It opens upwards
    // where the window has no room below.
    float?: boolean;
  }
  let { id, label, icon = "", items, disabled = false, small = false, standard = false, iconOnly = false, title, tipText = "", end = false, maxRows = 0, float = false }: Props = $props();

  let open = $state(false);
  let active = $state(-1);
  let wrap: HTMLDivElement | undefined = $state();
  let button: HTMLButtonElement | undefined = $state();
  let itemEls: (HTMLButtonElement | undefined)[] = $state([]);
  // A tick slot is kept on every item of a menu that has a setting in it,
  // so the labels line up.
  const ticks = $derived(items.some((i) => i.checked !== undefined));
  // And an icon's slot on every item of a menu where any item has one, so
  // an item without (the pane's *Close*) lines up with the rest.
  const icons = $derived(items.some((i) => !!i.icon));
  // 28 px a row, the menu's 4 px padding and its stroke (app.css .menu).
  const cap = $derived(maxRows > 0 ? `max-height: calc(${maxRows} * 28px + 2 * var(--s1) + 2px)` : undefined);

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
    if (!item || item.disabled) return;
    close();
    item.run();
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
  // the control it landed on is the one the user meant. A floating menu
  // is not inside the wrap, so a press on it is its own.
  let menuEl: HTMLDivElement | undefined = $state();
  function outside(e: PointerEvent): void {
    const t = e.target as Node;
    if (open && wrap && !wrap.contains(t) && !menuEl?.contains(t)) close(false);
  }

  // floating: the menu moved to the document's body and placed against
  // the button there, out of every box that would clip it. It drifts in
  // from its side by the stylesheet's animation (app.css .menu.floating),
  // since which side is only known once it is placed. A scroll under it or
  // a resize would leave it standing away from its button, so either
  // closes it, as a native menu does.
  function floating(node: HTMLElement, on: boolean) {
    if (!on || !button) return;
    document.body.appendChild(node);
    const b = button.getBoundingClientRect();
    node.style.minWidth = `${Math.max(b.width, 176)}px`;
    const at = dropPlacement({ left: b.left, right: b.right, top: b.top, bottom: b.bottom }, { width: node.offsetWidth, height: node.offsetHeight }, { width: window.innerWidth, height: window.innerHeight }, end);
    node.style.left = `${Math.round(at.x)}px`;
    node.style.top = `${Math.round(at.y)}px`;
    node.classList.toggle("up", at.up);
    const gone = (e: Event) => {
      if (e.type === "scroll" && e.target instanceof Node && node.contains(e.target)) return;
      close(false);
    };
    window.addEventListener("scroll", gone, true);
    window.addEventListener("resize", gone);
    return {
      destroy() {
        window.removeEventListener("scroll", gone, true);
        window.removeEventListener("resize", gone);
      },
    };
  }
</script>

<svelte:window onpointerdown={outside} />

<div class="menu-wrap" bind:this={wrap}>
  <button
    type="button" class="btn" class:subtle={!standard} class:sm={small} class:icon-only={iconOnly} {id} bind:this={button} {disabled} {title}
    aria-label={iconOnly ? label : undefined}
    aria-haspopup="menu" aria-expanded={open} aria-controls="{id}-menu"
    onclick={toggle} onkeydown={key} use:tip={tipText}
  >
    {#if icon}<svg class="i i-14" aria-hidden="true"><use href="#{icon}" /></svg>{/if}
    {#if !iconOnly}
      {label}
      <svg class="i i-14 caret" class:up={open} aria-hidden="true"><use href="#i-chevdown" /></svg>
    {/if}
  </button>
  {#if open}
    <div
      class="menu" class:end class:floating={float} class:scrolls={!!cap} id="{id}-menu" role="menu" tabindex="-1" aria-labelledby={id} style={cap}
      bind:this={menuEl} use:floating={float}
      onkeydown={key} in:fly|global={float ? { duration: 0 } : motion(FAST, { y: -4 })} out:fade|global={motion(LEAVE)}
    >
      {#each items as item, i (i)}
        {#if item.sep && i > 0}<div class="msep" role="separator"></div>{/if}
        <button
          type="button" role={item.checked !== undefined ? "menuitemcheckbox" : "menuitem"} aria-checked={item.checked}
          class:danger={item.danger} class:two={!!secondLine(item)} aria-disabled={item.disabled || undefined} title={item.disabled ? undefined : item.title}
          tabindex={i === active ? 0 : -1} bind:this={itemEls[i]} onclick={() => choose(i)}
          use:tip={item.cut ? { text: item.label, cut: true } : ""}
        >
          {#if ticks}<svg class="i i-14 tick" class:on={item.checked} aria-hidden="true"><use href="#i-tick" /></svg>{/if}
          {#if item.icon}<svg class="i i-14" aria-hidden="true"><use href="#{item.icon}" /></svg>{:else if icons}<span class="i i-14" aria-hidden="true"></span>{/if}
          {#if item.cut}
            <span class="ellipsis cut">{item.label}</span>
          {:else if secondLine(item)}
            <span class="mlabel"><span>{item.label}</span><span class="mwhy">{secondLine(item)}</span></span>
          {:else}
            <span class="mlabel">{item.label}</span>
          {/if}
          {#if item.keys}<span class="acc">{item.keys}</span>{/if}
        </button>
      {/each}
    </div>
  {/if}
</div>

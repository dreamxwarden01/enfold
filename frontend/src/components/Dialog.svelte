<script lang="ts">
  import type { Snippet } from "svelte";
  import { fade, scale } from "svelte/transition";
  import { FAST, LEAVE, motion } from "../lib/motion";
  import { inPopover, isTopDialog, registerDialog } from "../lib/dialogs";

  interface Props {
    title: string;
    // onclose is what Escape does (APP.md §7, "Dialogs stay put"): Cancel
    // where there is one, Close where the one action is Close, Skip — or
    // Skip all — in the conflict and refused-name dialogs, never Replace.
    // Left out, Escape does nothing: a dialog busy with the work it asked
    // about has nothing to cancel.
    onclose?: () => void;
    // dismissable: a click on the backdrop closes as well. Only a dialog
    // whose one action is Close says so — the details modal, a What
    // happened list; a dialog that offers a choice ignores the click,
    // since it has a Cancel (ruled 2026-09-10).
    dismissable?: boolean;
    children: Snippet;
    actions?: Snippet;
    wide?: boolean;
    // covered: another dialog stands over this one — it answers no key or
    // click and leaves the tab order until that one closes.
    covered?: boolean;
  }
  let { title, onclose, dismissable = false, children, actions, wide = false, covered = false }: Props = $props();

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
  const live = () => !leaving() && !covered;

  // The dialog stack (lib/dialogs.ts): registered for as long as this one
  // is mounted, and only the topmost live dialog owns Escape and the trap
  // — a refused-name question arriving over Rename takes the key alone,
  // and Rename neither closes nor pulls the focus back until it is
  // topmost again.
  const entry = { live };
  $effect(() => registerDialog(entry));
  const topmost = () => live() && isTopDialog(entry);

  // Escape is this dialog's unless a popover inside it took the key first
  // — a menu closes on Escape and prevents it, so the dialog under it
  // stays (APP.md §7: Esc closes only the menu).
  function key(e: KeyboardEvent) {
    if (e.key === "Escape" && !e.defaultPrevented && onclose && topmost()) onclose();
  }

  // While a dialog is up the page behind it is inert (APP.md §7): the
  // dialog is drawn inside the page's own tree, so the attribute cannot go
  // on the shell without taking the dialog with it, and the dialog holds
  // the page's place itself. A key aimed at anything outside this dialog
  // — the body, once a click on the dim dropped the focus; a dialog under
  // this one — is stopped before the page's window listeners see it, so no
  // list shortcut fires, and the focus is put back in the box; a key aimed
  // inside the box, or inside a popover the dialog owns and renders beside
  // itself (the details modal's Copy menu), is that dialog's and bubbles
  // as before.
  const inside = (t: EventTarget | null) => (t instanceof Node && box?.contains(t) === true) || inPopover(t);

  function trap(e: KeyboardEvent) {
    if (!topmost() || inside(e.target)) return;
    e.stopPropagation();
    // Select-all on the body would select the page's text behind the dim.
    if ((e.ctrlKey || e.metaKey) && (e.key === "a" || e.key === "A")) e.preventDefault();
    if (e.key === "Escape" && onclose) onclose();
    else box?.focus();
  }

  // Tab past the dialog's last control lands in the page behind it, which
  // is inert in every sense but the tab order: the focus is brought back
  // in at the end the user was heading for.
  function focusables(): HTMLElement[] {
    if (!box) return [];
    const all = box.querySelectorAll<HTMLElement>('a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])');
    return [...all].filter((el) => !el.closest("[inert]") && el.offsetParent !== null);
  }

  function refocus(e: FocusEvent) {
    if (!topmost() || inside(e.target)) return;
    const list = focusables();
    const from = e.relatedTarget;
    if (list.length === 0) box?.focus();
    else if (from === box || from === list[0]) list[list.length - 1].focus();
    else list[0].focus();
  }

  function backdropClick(e: MouseEvent) {
    if (e.target === e.currentTarget && dismissable && onclose && topmost()) onclose();
  }

  // A click on the dim of a dialog that stays put is nothing at all: it
  // must not even move the focus out of the box.
  function backdropDown(e: MouseEvent) {
    if (e.target === e.currentTarget) e.preventDefault();
  }
</script>

<svelte:window onkeydowncapture={trap} onkeydown={key} onfocusin={refocus} />

<div class="backdrop" role="presentation" bind:this={backdrop} in:fade|global={motion()} out:fade|global={motion(LEAVE)} onmousedown={backdropDown} onclick={backdropClick}>
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

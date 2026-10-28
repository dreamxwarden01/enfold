<script lang="ts">
  import { setContext, untrack } from "svelte";
  import type { Snippet } from "svelte";
  import { fade, scale } from "svelte/transition";
  import { FAST, LEAVE, motion } from "../lib/motion";
  import { DIALOG_HOST, inPopover, isTopDialog, registerDialog } from "../lib/dialogs";
  import type { DialogHost } from "../lib/dialogs";
  import { dialogCopy } from "../lib/strings";
  import InfoBars from "./InfoBars.svelte";

  interface Props {
    title: string;
    // onclose is what Escape does (APP.md §6, "Dialogs stay put"): Cancel
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
    // actions: the dialog's buttons, drawn in its foot band (ruled
    // 2026-10-17) — the accent or filled-danger action and its Cancel, in
    // equal widths that wrap to rows of their own rather than spill.
    actions?: Snippet;
    wide?: boolean;
    // medium: 520 px, between the default 460 and wide's 640 — the
    // recovery key's dialog, whose eight groups of digits at the sub size
    // and two readback fields want the room (direction 9, ruled 2026-10-18).
    medium?: boolean;
    // heading: what stands beside the title, inside its heading — the
    // recovery key's ID, said once as the heading's label (direction 9).
    heading?: Snippet;
    // covered: another dialog stands over this one — it answers no key or
    // click and leaves the tab order until that one closes.
    covered?: boolean;
    // onsubmit makes the dialog a form (APP.md §6, ruled 2026-10-14): its
    // accent action is the type="submit" button, so Enter in a field
    // presses it — which, under the form rule, marks what would refuse
    // (lib/forms.ts) — and Escape still cancels. A form dialog opens with
    // the focus in its first field, the caret at the end, or over what
    // `select` picks from the field's value (Rename's stem); one with no
    // field to type in opens on the checked answer of its question, where
    // it asks one, and else on its submit button, as WinUI's ContentDialog
    // opens on its default button — the close question's Quit (APP.md
    // §2.3, ruled 2026-10-17).
    onsubmit?: () => void;
    select?: (value: string) => [number, number];
    // closeButton: a close button in the dialog's top-right corner, on the
    // title's line, that does exactly what Escape does — greyed while
    // onclose is left out (APP.md §6, Controls; ruled 2026-10-17). Opt-in:
    // the close question has one, since its foot holds its two answers and
    // no Cancel.
    closeButton?: boolean;
    // closeTitle: what the corner button says it does when it is not Cancel
    // (the extract's conflict question: Skip).
    closeTitle?: string;
    // anchored: the box stands at a fixed distance from the top of the
    // window instead of at its middle (APP.md §6, Dialogs stay put; ruled
    // 2026-10-17). Opt-in, for a dialog whose height changes with a
    // choice made in it — the create, finish-setup and restore dialogs,
    // whose cards open — so its title stays put while the box grows
    // downward; every other dialog is centred.
    anchored?: boolean;
  }
  let { title, onclose, dismissable = false, children, actions, wide = false, medium = false, heading, covered = false, onsubmit, select, closeButton = false, closeTitle, anchored = false }: Props = $props();

  // What the body lends a ceremony panel (lib/dialogs DialogHost): the
  // foot band while it is mounted, and the touch's green over the box
  // (APP.md §6, ruled 2026-10-17).
  const host: DialogHost = $state({ foot: null, green: false, ceremony: false });
  setContext(DIALOG_HOST, host);

  // Focus moves into a dialog as it opens, so that a keyboard user is in
  // it and not still on the button that opened it: into the first field
  // of a form, onto the submit button of a form with no field, else onto
  // the box. A form whose first thing is a question — a fieldset of
  // radios, the remove dialog's and the password's (ruled 2026-10-27) —
  // opens on its checked answer, so that a screen reader reads the
  // question and the answer chosen before anything else, and Enter there
  // still presses the submit. Scoped to a fieldset, so the cards of
  // Finish setting up, radios outside any, still open on Continue.
  let box: HTMLDivElement | undefined = $state();
  $effect(() => {
    if (!box) return;
    const form = untrack(() => onsubmit) !== undefined;
    const field = form ? firstField(box) : null;
    if (!field) {
      const answer = form ? box.querySelector<HTMLInputElement>('fieldset input[type="radio"]:checked:not([disabled])') : null;
      const act = form ? box.querySelector<HTMLButtonElement>('button[type="submit"]:not([disabled])') : null;
      (answer ?? act ?? box).focus();
      return;
    }
    field.focus();
    const end = field.value.length;
    const [from, to] = untrack(() => select?.(field.value)) ?? [end, end];
    field.setSelectionRange(from, to);
  });

  // The first field to type in: never a read-only one (the import
  // dialog's path, filled by its Choose… button) nor one inside a closed
  // card, which is inert and would refuse the focus (FirstWayIn).
  function firstField(root: HTMLElement): HTMLInputElement | HTMLTextAreaElement | null {
    const all = root.querySelectorAll<HTMLInputElement | HTMLTextAreaElement>(
      'input:not([type="checkbox"]):not([type="radio"]):not([type="hidden"]):not([disabled]):not([readonly]), textarea:not([disabled]):not([readonly])',
    );
    return [...all].find((el) => !el.closest("[inert]")) ?? null;
  }

  function submit(e: SubmitEvent) {
    e.preventDefault();
    if (topmost()) onsubmit?.();
  }

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
  // stays (APP.md §6: Esc closes only the menu).
  function key(e: KeyboardEvent) {
    if (e.key === "Escape" && !e.defaultPrevented && onclose && topmost()) onclose();
  }

  // The corner close button is Escape by another hand: the same onclose,
  // under the same topmost rule.
  function cancel() {
    if (onclose && topmost()) onclose();
  }

  // While a dialog is up the page behind it is inert (APP.md §6): the
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

<div class="backdrop" class:anchored role="presentation" bind:this={backdrop} in:fade|global={motion()} out:fade|global={motion(LEAVE)} onmousedown={backdropDown} onclick={backdropClick}>
  <div class="dialog" class:wide class:medium class:ceremony={host.ceremony} class:green={host.green} role="dialog" aria-modal={covered ? undefined : true} aria-label={title} tabindex="-1" bind:this={box} inert={covered || undefined} in:scale|global={motion(FAST, { start: 0.97 })} out:fade|global={motion(LEAVE)}>
    {#if onsubmit}
      <!-- display: contents makes the body and the foot the box's own
           flex children, as if the form were not there. -->
      <form class="dialog-form" novalidate onsubmit={submit}>{@render parts()}</form>
    {:else}
      {@render parts()}
    {/if}
  </div>
</div>

<!-- The body scrolls and the foot band stays (app.css, APP.md §6
     Controls): `actions` is the foot — every dialog's buttons go there,
     and the band draws them in equal widths. -->
{#snippet parts()}
  <div class="dialog-body">
    {#if closeButton}
      <div class="dialog-head">
        <h2>{title}{@render heading?.()}</h2>
        <button type="button" class="btn subtle dialog-close" title={onclose ? (closeTitle ?? dialogCopy.cancelEsc) : undefined} aria-label={closeTitle ?? dialogCopy.cancel} disabled={!onclose} onclick={cancel}><svg class="i" aria-hidden="true"><use href="#i-x" /></svg></button>
      </div>
    {:else}
      <h2>{title}{@render heading?.()}</h2>
    {/if}
    {@render children()}
  </div>
  <!-- The InfoBars raised while a dialog is up, which the page's slot,
       under the dim, cannot show (direction 4; lib/infobar.ts): in the
       topmost dialog only, over the body's foot, just above the band. -->
  {#if !covered}<div class="dialog-bars"><InfoBars host="dialog" flow /></div>{/if}
  <!-- A ceremony panel in the body draws the foot while it is mounted:
       its buttons follow the ceremony's step (lib/dialogs DialogHost). -->
  {#if host.foot}
    <div class="dialog-foot">{@render host.foot()}</div>
  {:else if actions}
    <div class="dialog-foot">{@render actions()}</div>
  {/if}
{/snippet}

<style>
  /* 640, past WinUI ContentDialog's 548 cap, on purpose (checked
     2026-10-17). The compare list's two columns and the import checklist
     would keep at 548 (500 of content, 244 a column), but the
     Details dialog's versions table would not: its KID, 32 hex digits in mono
     beside two dates and a state, needs 231 px and gets 164 at 548, so a
     key version could no longer be read off its row (APP.md §6, §13). At
     640 it fits whole, and at the 880 px minimum window 640 still stands
     clear of the edges. */
  .dialog.wide { width: 640px; }
  .dialog.medium { width: 520px; }
  .dialog-form { display: contents; }
  /* The top anchor (APP.md §6, Dialogs stay put; ruled 2026-10-17): the
     box's top at a tenth of the window, as WinUI's tall dialogs sit high,
     but never lower than where a 780 px box would stand centred — the
     create dialog with the YubiKey's tick on, 775 px, rounded up — so
     that one still fits whole once it grows (the import dialog's backup
     with the tick on is taller and scrolls at 820 px; 780 is also that
     window's cap, 820 less 20 at each end); and never nearer the edge
     than the 20 px every dialog keeps. So the top is at 20 at 820 px and
     below, 60 at 900, 108 at 1080; the box grows downward only, to 20 px
     from the bottom, past which its body scrolls as every dialog's does. */
  .backdrop.anchored { --anchor: max(var(--s5), min(10vh, 50vh - 390px)); align-items: flex-start; }
  .backdrop.anchored .dialog { margin-top: var(--anchor); max-height: calc(100vh - var(--anchor) - var(--s5)); }
  /* The title's line with the corner close button (APP.md §6, Controls;
     ruled 2026-10-17): a 32 px subtle button at the body's own 24 px
     inset, centred on the title's first 28 px line. The 2 px it stands
     past that line above and below are taken back, so the body under the
     title starts where it does in every other dialog; a long title wraps
     beside it, never under it. */
  .dialog-head { display: flex; align-items: flex-start; gap: var(--s2); }
  .dialog-head h2 { flex: 1 1 auto; min-width: 0; }
  .dialog-close { flex: none; width: 32px; margin: -2px 0; padding: 0; justify-content: center; }
</style>

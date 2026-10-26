<script lang="ts">
  // The InfoBars (direction 4 of the design review, phase 4b; APP.md §6,
  // Notices): what the page cannot say in place — an error a call came
  // back with, a copy that worked, an operation that ended on another page.
  // They replace the toasts, which stood at the top centre of the window
  // over the page's title with no icon and no close (DECISIONS 2026-09-07,
  // superseded here). A bar sits in the page's notice slot over the top of
  // the list (NoticeSlot), floating with --shadow-pop so it never moves
  // what is under it: the tier's icon, the sentence left-aligned, and a
  // 28 px × in the shape of a dialog's corner close.
  //
  // The store keeps them and their clock (lib/infobar.ts): info and success
  // go by themselves after 3 s plus 50 ms a character, at most 8 s, and not
  // while the pointer is over the bars or the window is away; an error stays
  // until it is closed — its ×, or Esc for the newest (App.svelte). Three at
  // most, newest first; the rest wait their turn.
  //
  // host: "page" is the slot's, on every page, on the lock screen and on
  // the boot failure's panel; "dialog" is the topmost dialog's, above its
  // foot band, for a bar raised while the dialog was up — the page's slot
  // is under the dim then.
  import { untrack } from "svelte";
  import { flip } from "svelte/animate";
  import { fade, fly } from "svelte/transition";
  import { store } from "../lib/state.svelte";
  import { noticeCopy, unknownCodeParts } from "../lib/strings";
  import { newestError, noticeIcon, shown } from "../lib/infobar";
  import type { Host, Notice } from "../lib/infobar";
  import { FAST, LEAVE, enter, motion, unfold } from "../lib/motion";

  // flow: the bars stand in flow and open their height, rather than float
  // over what is under them — in a dialog, on the lock screen's stage and
  // the boot failure's, where floating would cover the panel's own heading,
  // and in the Vault and Settings columns, where it covered the first
  // heading and its commands.
  let { host = "page", flow = false }: { host?: Host; flow?: boolean } = $props();

  // A dialog's host counts itself in for as long as it stands: while one
  // does, the page's bars are under the dim and their time waits.
  $effect(() => {
    if (untrack(() => host) !== "dialog") return;
    untrack(() => store.noticeHost(1));
    return () => untrack(() => store.noticeHost(-1));
  });

  const bars = $derived(shown(store.notices, host, store.noticeDialogs > 0));
  // The bar Esc closes, which says so in its ×'s title.
  const escId = $derived(host === "page" && store.noticeDialogs === 0 ? newestError(store.notices)?.id : undefined);

  // The pointer over a bar holds every bar's time; a host that goes from
  // under the pointer — a page left, a dialog closed — lets it go. So does
  // a bar that leaves the list from under it: one Esc closed or a repeat
  // replaced (lib/infobar.ts raise) fires no pointerleave, and the hold
  // would stand until the pointer next crossed a bar.
  let hovered: number | null = null;
  function hover(id: number | null) {
    hovered = id;
    store.pauseNotices({ hovered: id !== null });
  }
  // leave lets go only of the hold its own bar took: a bar still fading
  // out after it left the list can report its leave after the next bar's
  // enter.
  function leave(id: number) {
    if (hovered === id) hover(null);
  }
  $effect(() => {
    const ids = bars.map((n) => n.id);
    untrack(() => {
      if (hovered !== null && !ids.includes(hovered)) hover(null);
    });
  });
  $effect(() => () => {
    if (hovered !== null) untrack(() => store.pauseNotices({ hovered: false }));
  });

  function close(id: number) {
    store.dismissNotice(id);
    hover(null);
  }
</script>

<div class="ibars" class:flow role="region" aria-label={noticeCopy.label} aria-live="polite">
  {#if flow}
    <!-- In flow, each bar opening and closing its height (lib/motion.ts
         unfold), so it never covers what the dialog or the panel says —
         a dialog's list of what failed, the lock screen's heading. -->
    {#each bars as n (n.id)}
      <div class="fold" transition:unfold>{@render bar(n)}</div>
    {/each}
  {:else}
    {#each bars as n (n.id)}
      <div class="ibar-float" animate:flip={{ duration: motion(FAST).duration, easing: enter }} in:fly={motion(FAST, { y: -6, easing: enter })} out:fade={motion(LEAVE)}>{@render bar(n)}</div>
    {/each}
  {/if}
</div>

{#snippet bar(n: Notice)}
  {@const parts = unknownCodeParts(n.text)}
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="ibar {n.kind}" role={n.kind === "error" ? "alert" : "status"} onpointerenter={() => hover(n.id)} onpointerleave={() => leave(n.id)}>
    <svg class="i i-14" aria-hidden="true"><use href="#{noticeIcon(n.kind)}" /></svg>
    <!-- A code with no words of its own (strings.ts codeText) is there
         for the log, not for reading: muted and in mono, so the sentence
         around it reads first (ruled 2026-10-14). -->
    <span class="ibar-text">{#if parts}{parts[0]}<span class="mono code">{parts[1]}</span>{parts[2]}{:else}{n.text}{/if}</span>
    <button type="button" class="btn subtle ibar-x" title={n.id === escId ? noticeCopy.closeEsc : noticeCopy.close} aria-label={noticeCopy.close} onclick={() => close(n.id)}><svg class="i" aria-hidden="true"><use href="#i-x" /></svg></button>
  </div>
{/snippet}

<style>
  .code { user-select: text; }
</style>

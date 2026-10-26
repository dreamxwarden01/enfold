<script lang="ts">
  // The save bar (APP.md §6, "The save bar"): the one way a page stages
  // edits and commits them. Hidden while nothing is pending; while
  // something is, a frosted bar at the foot of the scroll pane names the
  // changes — a count, as many chips as fit, the rest folded into "+N
  // more…" with a popover — and offers Discard and Save. Invalid input
  // greys Save and says why, in the bar and never only there; saving
  // swaps the label and disables both; success and failure are the
  // page's InfoBar, not the bar's. Dirty is the caller's, derived by
  // diffing, never stored.
  //
  // The bar's one job (direction 10, phase 4a; DECISIONS 2026-09-07): a
  // choice applies when it is made; text that can be wrong in between is
  // saved by the bar. So the bar stands where several text fields are
  // edited together and saved at once — the archive's name and
  // description on the Archives page — and nowhere else: Settings and
  // the Vault page are made of choices, which apply at once
  // (store.choose), and the vault's name has its own one-field dialog
  // (RenameVaultDialog).
  import { fade, fly, scale } from "svelte/transition";
  import { motion, enter, exit, FAST, LEAVE } from "../lib/motion";
  import { pending } from "../lib/pending";
  import PendingLabel from "./PendingLabel.svelte";
  import { pendingCopy } from "../lib/strings";

  export interface PendingItem {
    key: string;
    label: string;
    tone?: "add" | "remove";
  }

  interface Props {
    items: PendingItem[];
    invalid?: string; // why Save is disabled; "" when it is not
    busy?: boolean;
    saveLabel?: string;
    onsave: () => void;
    ondiscard: () => void;
  }
  let { items, invalid = "", busy = false, saveLabel = "Save changes", onsave, ondiscard }: Props = $props();

  // How many chips fit: the strip's width against the chips' own, measured
  // on a hidden rail that renders every chip plus the "+N more…" probe.
  let strip: HTMLElement | undefined = $state();
  let rail: HTMLElement | undefined = $state();
  let stripWidth = $state(0);
  let fit = $state(Infinity);
  let open = $state(false);
  // The save has been waited on for 300 ms (lib/pending.ts).
  let saveArmed = $state(false);

  $effect(() => {
    if (!strip) return;
    const ro = new ResizeObserver(() => (stripWidth = strip?.clientWidth ?? 0));
    ro.observe(strip);
    stripWidth = strip.clientWidth;
    return () => ro.disconnect();
  });

  $effect(() => {
    // Re-measure whenever the items or the width change.
    void items.length;
    void stripWidth;
    if (!rail || !stripWidth) {
      fit = Infinity;
      return;
    }
    const chips = Array.from(rail.querySelectorAll<HTMLElement>(".sb-tag"));
    const probe = rail.querySelector<HTMLElement>(".sb-more");
    // The pending row's own gap, read off the strip rather than copied: it
    // is a step of the 4 px grid (APP.md §6, Type and spacing), and a
    // stale copy here measured chips that did not fit.
    const gap = strip ? parseFloat(getComputedStyle(strip).columnGap) || 0 : 0;
    let used = 0;
    let n = 0;
    for (const c of chips) {
      const w = c.offsetWidth + (n ? gap : 0);
      // The last chip may take the room the probe would have needed.
      const need = n + 1 < chips.length ? used + w + gap + (probe?.offsetWidth ?? 0) : used + w;
      if (need > stripWidth) break;
      used += w;
      n++;
    }
    fit = n;
    if (n >= chips.length) open = false;
  });

  const shown = $derived(fit === Infinity ? items : items.slice(0, fit));
  const folded = $derived(fit === Infinity ? [] : items.slice(fit));

  function text(it: PendingItem): string {
    return (it.tone === "add" ? "+ " : it.tone === "remove" ? "− " : "") + it.label;
  }

  function onkeydown(e: KeyboardEvent) {
    if (items.length === 0) return;
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "s") {
      e.preventDefault();
      if (!busy && !invalid) onsave();
    }
  }
</script>

<svelte:window {onkeydown} />

{#if items.length > 0}
  <div class="savebar-wrap" in:fly={motion(FAST, { y: 8, easing: enter })} out:fly={motion(LEAVE, { y: 8, easing: exit })}>
    {#if open && folded.length > 0}
      <div class="sb-pop" in:scale={motion(FAST, { start: 0.97, easing: enter })} out:fade={motion(LEAVE, { easing: exit })}>
        {#each folded as it (it.key)}<span class="sb-tag" class:add={it.tone === "add"} class:remove={it.tone === "remove"}>{text(it)}</span>{/each}
      </div>
    {/if}
    <div class="savebar" class:invalid={!!invalid} role="region" aria-label="Unsaved changes">
      {#if invalid}<span class="sb-note">{invalid}</span>{/if}
      <button type="button" class="sb-left" class:clk={folded.length > 0} disabled={folded.length === 0} title={folded.length ? (open ? "Hide the other changes" : "Show the other changes") : undefined} onclick={() => (open = !open)}>
        <span class="sb-count">{items.length}</span>
        <span class="sb-pending" bind:this={strip}>
          {#each shown as it (it.key)}<span class="sb-tag" class:add={it.tone === "add"} class:remove={it.tone === "remove"}>{text(it)}</span>{/each}
          {#if folded.length > 0}<span class="sb-more">+{folded.length} more…</span>{/if}
        </span>
        <span class="sb-rail" aria-hidden="true" bind:this={rail}>
          {#each items as it (it.key)}<span class="sb-tag" class:add={it.tone === "add"} class:remove={it.tone === "remove"}>{text(it)}</span>{/each}
          <span class="sb-more">+{items.length} more…</span>
        </span>
      </button>
      <button type="button" class="btn" disabled={busy} onclick={ondiscard}>Discard</button>
      <!-- The wait is the one pending primitive (direction 4): the ring
           and *Saving…* once it has taken 300 ms, so a quick save never
           flashes either; the button is greyed from the press. -->
      <button type="button" class="btn accent" disabled={busy || !!invalid} title={!busy && invalid ? invalid : undefined} onclick={onsave} use:pending={{ on: busy, armed: (b) => (saveArmed = b) }}><PendingLabel armed={saveArmed} rest={saveLabel} busy={pendingCopy.saving} /></button>
    </div>
  </div>
{/if}

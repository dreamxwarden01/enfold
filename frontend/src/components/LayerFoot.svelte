<script lang="ts">
  // The layer's one foot, below the pages and outside their crossfade
  // (APP.md §6): the page's note on the left, which fades in when the page
  // changes and updates in place otherwise; the lock state on the right,
  // always — the open padlock, when the vault locks and Lock now while
  // unlocked, the closed padlock and Unlock while locked with archives
  // still open. *Lock now* and *Unlock* are commands, so they are subtle
  // buttons 24 px high in the 34 px foot, never links (APP.md §6, ruled
  // 2026-10-17).
  import { fade } from "svelte/transition";
  import { Vault } from "../lib/api";
  import { store } from "../lib/state.svelte";
  import { lockLine } from "../lib/format";
  import { motion } from "../lib/motion";
  import { chipCopy, commandCopy } from "../lib/strings";

  interface Props {
    pageKey: string;
  }
  let { pageKey }: Props = $props();
  const st = $derived(store.status);
  // The idle minutes are the status's, as the core runs the session on
  // them (ruled 2026-10-17): no copy of its clamp here.
  const line = $derived(st ? lockLine(st.locksAt, st.absoluteAt, st.idleEffectiveMinutes, store.now) : null);
</script>

<div class="layer-foot">
  {#key pageKey}<span class="note ellipsis" in:fade={motion()}>{store.footNote}</span>{/key}
  {#if store.unlocked && st}
    <!-- The nearer of the idle deadline and the absolute one (APP.md §6,
         ruled 2026-09-12): the absolute cap locks regardless of activity,
         so the idle one alone would promise time the vault does not have.
         Quiet until the last minute (ruled 2026-10-14, lib/format.ts
         lockLine): the idle rule or the cap's clock time, then a count in
         the attention colour. -->
    <span class="lockchip" class:urgent={line?.urgent}><svg class="i i-14"><use href="#i-unlock" /></svg>{line?.text}<button type="button" class="btn subtle xs" onclick={() => void Vault.Lock()}>{commandCopy.lockNow}</button></span>
  {:else}
    <!-- *Locked*, the lock header's own word (ruled 2026-10-17: it read
         "Keystore locked", a format word; APP.md §7). -->
    <span class="lockchip"><svg class="i i-14"><use href="#i-lock" /></svg>{chipCopy.locked}<button type="button" class="btn subtle xs" onclick={() => store.go("lock")}>{commandCopy.unlock}</button></span>
  {/if}
</div>

<style>
  .note { min-width: 0; }
  .lockchip.urgent, .lockchip.urgent .i { color: var(--attention); }
</style>

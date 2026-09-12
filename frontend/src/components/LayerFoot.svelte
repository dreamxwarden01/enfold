<script lang="ts">
  // The layer's one foot, below the pages and outside their crossfade
  // (APP.md §6): the page's note on the left, which fades in when the page
  // changes and updates in place otherwise; the lock state on the right,
  // always — the open padlock, the time to the lock and Lock now while
  // unlocked, the closed padlock and Unlock while locked with archives
  // still open.
  import { fade } from "svelte/transition";
  import { Vault } from "../lib/api";
  import { store } from "../lib/state.svelte";
  import { countdown, nearerDeadline } from "../lib/format";
  import { motion } from "../lib/motion";

  interface Props {
    pageKey: string;
  }
  let { pageKey }: Props = $props();
  const st = $derived(store.status);
</script>

<div class="layer-foot">
  {#key pageKey}<span class="note ellipsis" in:fade={motion()}>{store.footNote}</span>{/key}
  {#if store.unlocked && st}
    <!-- The nearer of the idle deadline and the absolute one (APP.md §6,
         ruled 2026-09-12): the absolute cap locks regardless of activity,
         so the idle one alone would promise time the vault does not have. -->
    <span class="lockchip"><svg class="i i-14"><use href="#i-unlock" /></svg>Locks in {countdown(nearerDeadline(st.locksAt, st.absoluteAt), store.now)}<button type="button" class="btn link" onclick={() => void Vault.Lock()}>Lock now</button></span>
  {:else}
    <span class="lockchip"><svg class="i i-14"><use href="#i-lock" /></svg>Keystore locked<button type="button" class="btn link" onclick={() => store.go("lock")}>Unlock</button></span>
  {/if}
</div>

<style>
  .note { min-width: 0; }
</style>

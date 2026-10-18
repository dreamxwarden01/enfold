<script lang="ts">
  // The Tampered banner (APP.md §13; direction 9, ruled 2026-10-18): the
  // danger tier, above the lock screen's panel and first in the body of
  // every page — Archives, an archive, Vault, Settings — so a vault file
  // that may have been changed outside Enfold is never news only to the
  // lock screen. The sentence is tamperedCopy's, the same whatever the
  // reason, since the remedy is one; which check failed is the Why?
  // dialog's, in the second register (technicalCopy.tamperedWhy).
  //
  // The fix is the import, which only the lock screen runs: there the
  // button opens its dialog (onreplace); on a page the vault is locked
  // first and the lock screen opens it (store.askReplaceVault).
  import { store } from "../lib/state.svelte";
  import { commandCopy, dialogCopy, lockCopy, tamperedLead, technicalCopy } from "../lib/strings";
  import { tierIcon } from "../lib/notices";
  import Dialog from "./Dialog.svelte";

  let { reason, onreplace }: { reason?: string; onreplace?: () => void } = $props();

  let whyOpen = $state(false);
  // What happened, strong; then the freeze and its limit (direction 9's
  // mock: a bar opens with its news in the strong weight).
  const lead = $derived(tamperedLead(reason));

  function replace() {
    if (onreplace) onreplace();
    else void store.askReplaceVault();
  }
</script>

<div class="bar danger tampered-bar">
  <svg class="i i-14"><use href="#{tierIcon('danger')}" /></svg>
  <!-- The sentence, then its fix and its Why? on a row under it, 8 apart
       as a bar's parts are: the sentence is three, and a row beside it
       would leave it a word per line in the lock screen's 440 px. -->
  <div class="grow body">
    <span><strong>{lead.head}</strong> {lead.rest}</span>
    <div class="row">
      <button type="button" class="btn sm" onclick={replace}>{commandCopy.replaceShort}</button>
      <button type="button" class="btn link" onclick={() => (whyOpen = true)}>{technicalCopy.why}</button>
    </div>
  </div>
</div>

{#if whyOpen}
  <!-- Why changes are off: which check failed, in the second register's
       words (APP.md §13). Close is its one action, so the backdrop closes
       it too. -->
  <Dialog title={lockCopy.tamperedWhyTitle} onclose={() => (whyOpen = false)} dismissable>
    <p>{technicalCopy.tamperedWhy(reason)}</p>
    {#snippet actions()}
      <button type="button" class="btn accent" onclick={() => (whyOpen = false)}>{dialogCopy.close}</button>
    {/snippet}
  </Dialog>
{/if}

<style>
  .body { display: flex; flex-direction: column; align-items: flex-start; gap: var(--s2); min-width: 0; }
</style>

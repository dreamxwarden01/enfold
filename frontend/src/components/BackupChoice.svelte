<script lang="ts">
  // The backup an act takes after its change (APP.md §13; ruled 2026-10-27:
  // written after the change and its retirement, at the new key), one block
  // for every dialog that may re-key: the last backup as a fact, the reason
  // under it, and the tick — or, where no recovery key would remain, the
  // line that says why there is none, since a backup holds the recovery
  // keys alone (FORMAT R28). It asks and never refuses: nothing is gated on
  // the date, which is local and unauthenticated (APP.md §13).
  //
  // The decisions are lib/rekey.ts's. The tick follows the answer: set by
  // the rule when the dialog opens and again whenever the answer changes, a
  // clear the user made standing until then. Only the tick's state and the
  // reason's words change with the answer, and the reasons the two answers
  // can give share one cell, so the dialog keeps its height and stays
  // centred (APP.md §6, Dialogs stay put).
  import { untrack } from "svelte";
  import type { Act } from "../lib/rekey";
  import { backupOwed, backupPossible, backupReason, backupTicked } from "../lib/rekey";
  import { store } from "../lib/state.svelte";
  import { dialogCopy, sinceText } from "../lib/strings";

  interface Props {
    act: Act;
    // rekey: the answer (true for a re-key and a replacement).
    rekey: boolean;
    // removing: the type of the unlock method a removal takes away, and
    // label its name, for the removed recovery key's reason.
    removing?: string;
    label?: string;
    // count: the recovery keys a replacement replaces, for its reason in
    // the singular; recoveryKeys, the vault's recovery keys when the
    // dialog knows them better than the page's last read (a replacement
    // opened from an end's button reads that end's committed facts).
    count?: number;
    recoveryKeys?: number;
    ticked: boolean;
  }
  let { act, rekey, removing, label, count, recoveryKeys, ticked = $bindable() }: Props = $props();

  const recovery = $derived(recoveryKeys ?? store.slots.filter((s) => s.type === "recovery").length);
  const owed = $derived(
    backupOwed({ lastExportAt: store.lastExportAt, nowMs: store.now, vaultRekeyed: !!store.backup?.vaultRekeyed, keysReplaced: !!store.backup?.keysReplaced }),
  );
  const possible = $derived(backupPossible({ recoveryKeys: recovery, removing }));
  const reasonOf = (r: boolean) => backupReason({ owed, act, rekey: r, removing, label, count });
  const reason = $derived(reasonOf(rekey));
  // Both answers' reasons, drawn in one cell with the other hidden, so the
  // block is as tall whichever answer is chosen. None where no backup can
  // be made: a reason to take one would only contradict the line that says
  // none can.
  const reasons = $derived(possible ? [...new Set([reasonOf(true), reasonOf(false)].filter((r): r is string => !!r))] : []);

  $effect.pre(() => {
    const r = rekey;
    untrack(() => {
      ticked = possible && backupTicked({ owed, act, rekey: r, removing });
    });
  });
</script>

<dl class="kv"><dt>{dialogCopy.rekeyVault.lastBackup}</dt><dd>{store.lastExportAt ? sinceText(store.lastExportAt, store.now) : dialogCopy.rekeyVault.noBackup}</dd></dl>
{#if reasons.length}
  <div class="why">
    {#each reasons as r (r)}<p class:off={r !== reason} aria-hidden={r !== reason || undefined}>{r}</p>{/each}
  </div>
{/if}
{#if possible}
  <label class="check"><input type="checkbox" bind:checked={ticked} />{dialogCopy.backupAfter.tick}</label>
{:else}
  <p>{dialogCopy.backupAfter.none}</p>
{/if}

<style>
  /* The reasons share one cell; the one the answer does not give holds
     its place unseen. */
  .why { display: grid; min-width: 0; }
  .why > p { grid-area: 1 / 1; }
  .why > .off { visibility: hidden; }
</style>

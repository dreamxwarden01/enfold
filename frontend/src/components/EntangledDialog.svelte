<script lang="ts">
  // The vault's entangled password, turned on, changed or turned off
  // (APP.md §13, FORMAT.md §3.1). One switch for the whole vault: every
  // hardware key asks for the password beside its PIN, the standalone
  // password and the recovery key never do. The new password is chosen in
  // the ceremony that follows — a typed secret never rides a bound-method
  // argument (APP.md §1) — and **no field here asks for the old password**
  // (the ruling of 2026-09-07): whoever reaches this page already holds
  // the VMK and could enrol an unlock method of their own; a YubiKey's
  // proof still asks the current one with its PIN. Why is the
  // disclosure's, not the body's: the body says what changes for the
  // user, the dialog template (strings.ts, ruled 2026-10-17).
  //
  // Changing it and turning it on may re-key the vault in the same step
  // (ruled 2026-10-27), so those two are forms that ask the fact first
  // (RekeyQuestion), say the limit of a re-key and the act that closes what
  // it leaves open, and offer the backup taken after the change
  // (BackupChoice). Turning it off asks nothing: it withdraws nothing a
  // re-key could take back, since the file then opens without it.
  import { Keys } from "../lib/api";
  import type { Answer } from "../lib/rekey";
  import { primaryOf, questionOf, rekeyLimitOf } from "../lib/rekey";
  import { store } from "../lib/state.svelte";
  import { dialogCopy } from "../lib/strings";
  import Dialog from "./Dialog.svelte";
  import RekeyQuestion from "./RekeyQuestion.svelte";
  import BackupChoice from "./BackupChoice.svelte";

  interface Props {
    mode: "on" | "change" | "off";
    onclose: () => void;
    // begin and beginWith are the Vault page's: each starts the act, reads
    // the unlock methods and the switch again when it ends, and keeps the
    // act for the ceremony dialog's Start again (KeysPage); beginWith also
    // keeps the file the act's backup goes to, which pick asks for.
    begin: (run: () => Promise<unknown>) => Promise<unknown>;
    beginWith: (run: (backup: string) => Promise<unknown>, backup: string) => Promise<unknown>;
    pick: () => Promise<string>;
    // fromFollow: an end's next step opened this dialog, so the act goes on
    // in that end's chain (lib/rekey.ts chainFor).
    fromFollow?: boolean;
  }
  let { mode, onclose, begin, beginWith, pick, fromFollow = false }: Props = $props();

  const copy = $derived(mode === "on" ? dialogCopy.passwordOn : mode === "change" ? dialogCopy.passwordChange : dialogCopy.passwordOff);
  const act = $derived(mode === "on" ? "on" : "password");
  const asks = $derived(mode !== "off");
  const q = $derived(questionOf(act));
  const recoveryKeys = $derived(store.slots.filter((s) => s.type === "recovery").length);
  const limit = $derived(rekeyLimitOf({ act, passwordOn: !!store.entangled?.on, otherLeft: recoveryKeys }));
  const how = $derived(mode === "on" ? `${dialogCopy.passwordOn.next} ${dialogCopy.passwordOn.how}` : mode === "change" ? dialogCopy.passwordChange.how : "");

  let answer = $state<Answer>("rekey");
  let ticked = $state(false);
  const primary = $derived(asks ? primaryOf(act, answer === "rekey", ticked) : copy.primary);

  async function go() {
    const m = mode;
    const rekey = answer === "rekey";
    const tick = ticked;
    onclose();
    if (m === "off") {
      // An act with no next step: the chain ends with it.
      store.noteAct(null);
      await begin(() => Keys.SetEntangled(false, false, ""));
      return;
    }
    let path = "";
    if (tick) {
      path = await pick();
      if (!path) return; // the Save dialog cancelled: nothing begins
    }
    store.noteAct({ act: m === "on" ? "on" : "password", rekey, fromFollow });
    await beginWith((b) => (m === "on" ? Keys.SetEntangled(true, rekey, b) : Keys.ChangeEntangledPassword(rekey, b)), path);
  }
</script>

<Dialog title={copy.title} {onclose} onsubmit={asks ? go : undefined}>
  <p>{copy.body}</p>
  {#if asks}
    <RekeyQuestion legend={q.legend} rekey={q.rekey} keep={q.keep} bind:answer />
    <p class="infoline"><svg class="i i-14" aria-hidden="true"><use href="#i-info" /></svg><span>{limit}</span></p>
    <BackupChoice {act} rekey={answer === "rekey"} bind:ticked />
  {/if}
  <!-- The body keeps to 2 sentences; that the password is chosen next
       goes with the mechanism, behind the disclosure (direction 8). -->
  {#if how}
    <details class="how"><summary>{dialogCopy.howThisWorks}</summary><p>{how}</p></details>
  {/if}
  {#snippet actions()}
    <!-- Turning the password off weakens the vault, so nothing suggests
         it: a standard button beside Cancel (direction 9, ruled
         2026-10-18). Turning it on and changing it keep the accent, and
         are the forms' submit. -->
    {#if asks}
      <button type="submit" class="btn accent">{primary}</button>
    {:else}
      <button type="button" class="btn" onclick={go}>{primary}</button>
    {/if}
    <button type="button" class="btn" onclick={onclose}>{dialogCopy.cancel}</button>
  {/snippet}
</Dialog>

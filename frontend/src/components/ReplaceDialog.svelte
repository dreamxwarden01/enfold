<script lang="ts">
  // Replace recovery keys… (APP.md §13; ruled 2026-10-27): one ceremony and
  // one step that adds new recovery keys, removes every old one and re-keys
  // the vault, so that no new key is ever kept in the vault where an old
  // one can read it. Opened by the Vault page's head, or by an end's next
  // step (from), when the act that ended says a copy of the vault file may
  // be out there.
  //
  // A form with no field and no question, so it opens on its submit and
  // Enter presses it (APP.md §6). The title and the body count, so the user
  // knows how many sheets to have ready before pressing; one key is said in
  // the singular everywhere. The primary is the accent, not danger: the act
  // protects, and the keys it ends are the ones being replaced.
  //
  // What it reads is lib/rekey.ts replaceFacts': opened from an end, that
  // end's committed facts (its count and whether the password is on) and
  // the chain's changed, since the page's own reads may still be from
  // before that act; opened from the page, the page's, a new chain
  // beginning with it. changed leaves out the sentences that say to change
  // the password too: the chain has changed it already.
  import { Keys } from "../lib/api";
  import type { AfterFacts } from "../lib/rekey";
  import { primaryOf, replaceFacts } from "../lib/rekey";
  import { store } from "../lib/state.svelte";
  import { dialogCopy } from "../lib/strings";
  import Dialog from "./Dialog.svelte";
  import BackupChoice from "./BackupChoice.svelte";

  interface Props {
    onclose: () => void;
    // beginWith and pick are the Vault page's: the act begun with the file
    // its backup goes to, and the Save dialog that picks that file.
    beginWith: (run: (backup: string) => Promise<unknown>, backup: string) => Promise<unknown>;
    pick: () => Promise<string>;
    // from: the end whose next step opened this dialog.
    from?: { after: AfterFacts; changed: boolean };
    // minutes: the idle time the session runs on (lib/rekey.ts
    // replaceMinutes), the core's clamp applied, never the setting as
    // stored: the ready line promises it.
    minutes: number;
  }
  let { onclose, beginWith, pick, from, minutes }: Props = $props();

  const copy = dialogCopy.replaceRecoveryKeys;
  const f = $derived(replaceFacts({ from, recoveryKeys: store.slots.filter((s) => s.type === "recovery").length, entangled: !!store.entangled?.on }));
  const n = $derived(f.count);
  // The password's sentences, with it on and not changed yet in the chain.
  const passwordToo = $derived(f.passwordOn && !f.changed);
  let ticked = $state(true);

  async function go() {
    const tick = ticked;
    const fromFollow = !!from;
    onclose();
    let path = "";
    if (tick) {
      path = await pick();
      if (!path) return; // the Save dialog cancelled: nothing begins
    }
    store.noteAct({ act: "replace", rekey: true, fromFollow });
    await beginWith((b) => Keys.ReplaceRecoveryKeys(b), path);
  }
</script>

<Dialog title={copy.title(n)} {onclose} onsubmit={go}>
  <p>{copy.body(n)}</p>
  <!-- When it is needed, said every time: an info line, a sentence and
       not a gate (APP.md §6). -->
  <p class="infoline"><svg class="i i-14" aria-hidden="true"><use href="#i-info" /></svg><span>{copy.when(n)}{passwordToo ? copy.whenOn : ""}</span></p>
  <!-- What to be ready for: the idle lock counts real input only, and 48
       digits written by hand is minutes without any. -->
  <p>{copy.ready(minutes, n)}</p>
  <BackupChoice act="replace" rekey={true} count={n} recoveryKeys={n} bind:ticked />
  <details class="how"><summary>{dialogCopy.howThisWorks}</summary><p>{n === 1 ? copy.howOne : copy.how}{passwordToo ? copy.howOn : ""}</p></details>
  {#snippet actions()}
    <button type="submit" class="btn accent">{primaryOf("replace", true, ticked, n)}</button>
    <button type="button" class="btn" onclick={onclose}>{dialogCopy.cancel}</button>
  {/snippet}
</Dialog>

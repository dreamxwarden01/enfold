<script lang="ts">
  // The vault's entangled password, turned on, changed or turned off
  // (APP.md §13, FORMAT.md §3.1). One switch for the whole vault: every
  // hardware key asks for the password beside its PIN, the standalone
  // password and the recovery key never do. The new password is chosen in
  // the ceremony that follows — a typed secret never rides a bound-method
  // argument (APP.md §1) — and **the old password is never a field here**
  // (the ruling of 2026-09-07): whoever reaches this page already holds
  // the VMK and could enrol an unlock method of their own. Why is the
  // disclosure's, not the body's: the body says what changes for the
  // user, the dialog template (strings.ts, ruled 2026-10-17).
  import { Keys, errorOf } from "../lib/api";
  import { store } from "../lib/state.svelte";
  import { codeText, dialogCopy } from "../lib/strings";
  import Dialog from "./Dialog.svelte";

  interface Props {
    mode: "on" | "change" | "off";
    onclose: () => void;
  }
  let { mode, onclose }: Props = $props();

  const copy = $derived(mode === "on" ? dialogCopy.passwordOn : mode === "change" ? dialogCopy.passwordChange : dialogCopy.passwordOff);
  const how = $derived(mode === "on" ? dialogCopy.passwordOn.how : mode === "change" ? dialogCopy.passwordChange.how : "");

  async function go() {
    onclose();
    store.dismissCeremony();
    try {
      if (mode === "off") await Keys.SetEntangled(false);
      else if (mode === "on") await Keys.SetEntangled(true);
      else await Keys.ChangeEntangledPassword();
    } catch (e) {
      store.toast(codeText(errorOf(e).code), "error");
    }
    await store.refreshSlots();
    await store.refreshEntangled();
  }
</script>

<Dialog title={copy.title} {onclose}>
  <p>{copy.body}</p>
  <!-- The body keeps to 2 sentences; that the password is chosen next
       goes with the mechanism, behind the disclosure (direction 8). -->
  {#if how}
    <details class="how"><summary>{dialogCopy.howThisWorks}</summary><p>{mode === "on" ? `${dialogCopy.passwordOn.next} ${how}` : how}</p></details>
  {/if}
  {#snippet actions()}
    <button type="button" class="btn accent" onclick={go}>{copy.primary}</button>
    <button type="button" class="btn" onclick={onclose}>{dialogCopy.cancel}</button>
  {/snippet}
</Dialog>

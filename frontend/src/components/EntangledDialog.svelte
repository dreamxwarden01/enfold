<script lang="ts">
  // The vault's entangled password, turned on, changed or turned off
  // (APP.md §13, FORMAT.md §3.1). One switch for the whole vault: every
  // hardware key asks for the password before its PIN, the standalone
  // password and the recovery key never do. The new password is chosen in
  // the ceremony that follows — a typed secret never rides a bound-method
  // argument (APP.md §1) — and **the old password is never a field here**
  // (the ruling of 2026-09-07): whoever reaches this page already holds
  // the VMK and could enrol a way in of their own, so asking for the old
  // one would be friction and not a guard.
  import { Keys, errorOf } from "../lib/api";
  import { store } from "../lib/state.svelte";
  import { codeText } from "../lib/strings";
  import Dialog from "./Dialog.svelte";

  interface Props {
    mode: "on" | "change" | "off";
    onclose: () => void;
  }
  let { mode, onclose }: Props = $props();

  const title = $derived(mode === "on" ? "Require a password with every YubiKey?" : mode === "change" ? "Change the vault's password" : "Stop requiring a password?");
  const act = $derived(mode === "on" ? "Turn on" : mode === "change" ? "Change" : "Turn off");

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

<Dialog {title} {onclose}>
  {#if mode === "on"}
    <p>Every YubiKey in this vault will ask for this password before its PIN. A stolen key alone will not open the vault; a forgotten password will not shut you out, because the recovery key and any standalone password are untouched by it.</p>
    <p>You choose it in the next step and type it twice. The vault asks for a way in first, then re-wraps every YubiKey without needing one present.</p>
  {:else if mode === "change"}
    <p>You choose the new password in the next step and type it twice. Every YubiKey is re-wrapped to it at once, and none needs to be present.</p>
    <p>The old password is not asked for. Anyone who can reach this page has already opened the vault and could add a way in of their own, so asking would be friction and not a guard.</p>
  {:else}
    <p>The vault's YubiKeys stop asking for a password: each will open the vault with its PIN and touch alone. Every key is re-wrapped at once, and none needs to be present.</p>
    <p>The old password is not asked for, and nothing else about the vault changes.</p>
  {/if}
  {#snippet actions()}
    <button type="button" class="btn" onclick={onclose}>Cancel</button>
    <button type="button" class="btn accent" onclick={go}>{act}</button>
  {/snippet}
</Dialog>

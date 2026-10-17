<script lang="ts">
  // The first unlock method's fields (APP.md §6): which method, a name for
  // a YubiKey — never for a password, whose slot is "Password" — and the
  // tick for the password with every YubiKey. The words are the glossary's
  // (strings.ts, ruled 2026-10-17). Shared by the create, import and finish-setup
  // dialogs; the secret itself is chosen in the ceremony, never here.
  //
  // Since Revision 2 the tick is the vault's one switch, not this key's
  // (APP.md §13, FORMAT.md §3.1): every YubiKey enrolled later inherits it
  // from the kept K_P and is never asked about it, so the words here say
  // the vault's fact rather than the key's.
  import { dialogCopy, lockCopy } from "../lib/strings";

  interface Props {
    kind: "token" | "password";
    label: string;
    entangle: boolean;
    idPrefix?: string;
  }
  let { kind = $bindable(), label = $bindable(), entangle = $bindable(), idPrefix = "fw" }: Props = $props();
</script>

<div class="field">
  <div class="field-top"><label for="{idPrefix}-kind">{lockCopy.firstMethod}</label><span class="hint">{lockCopy.firstMethodHint}</span></div>
  <select id="{idPrefix}-kind" class="input" bind:value={kind}>
    <option value="token">{dialogCopy.addUnlockMethod.token}</option>
    <option value="password">{dialogCopy.addUnlockMethod.password}</option>
  </select>
</div>
{#if kind === "token"}
  <div class="field">
    <!-- The placeholder is what an empty field produces — the core names
         the key "YubiKey <serial>" — never an invented name that would
         read as a default already filled in. -->
    <div class="field-top"><label for="{idPrefix}-label">{dialogCopy.addUnlockMethod.nameKey}</label><span class="hint">{dialogCopy.addUnlockMethod.optional}</span></div>
    <input id="{idPrefix}-label" class="input" bind:value={label} placeholder={dialogCopy.addUnlockMethod.keyPlaceholder} />
  </div>
  <label class="check"><input type="checkbox" bind:checked={entangle} />{lockCopy.entangleTick}</label>
  {#if entangle}
    <p>{lockCopy.entangleFirst}</p>
  {/if}
{:else}
  <p>{lockCopy.passwordNext}</p>
{/if}

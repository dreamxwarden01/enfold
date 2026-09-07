<script lang="ts">
  // The first way in's fields (APP.md §6): the kind, a name for a key —
  // never for a password, whose slot is "Password" — and the entangled
  // password's tick. Shared by the create, import and finish-setup
  // dialogs; the secret itself is chosen in the ceremony, never here.
  interface Props {
    kind: "token" | "password";
    label: string;
    entangle: boolean;
    idPrefix?: string;
  }
  let { kind = $bindable(), label = $bindable(), entangle = $bindable(), idPrefix = "fw" }: Props = $props();
</script>

<div class="field">
  <div class="field-top"><label for="{idPrefix}-kind">First way in</label><span class="hint">A recovery key is always kept too.</span></div>
  <select id="{idPrefix}-kind" class="input" bind:value={kind}>
    <option value="token">YubiKey (PIN + touch)</option>
    <option value="password">Password</option>
  </select>
</div>
{#if kind === "token"}
  <div class="field">
    <div class="field-top"><label for="{idPrefix}-label">Name this key</label><span class="hint">Shown in the list of ways in.</span></div>
    <input id="{idPrefix}-label" class="input" bind:value={label} placeholder="YubiKey 5C — desk" />
  </div>
  <label class="check"><input type="checkbox" bind:checked={entangle} />Also require a password with this key</label>
  {#if entangle}
    <p>You choose that password first, before the key is set up. Unlocking then needs both.</p>
  {/if}
{:else}
  <p>You choose the password in the next step.</p>
{/if}

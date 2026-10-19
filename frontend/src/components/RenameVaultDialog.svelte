<script lang="ts">
  // Rename the vault (direction 10, phase 4a; APP.md §6, the Vault page):
  // the one place the vault's name is edited, a dialog of one field under
  // the form rule (APP.md §6, Forms) — the primary stays enabled, and a
  // press with the field empty, or holding the name the vault has, marks
  // the field in place rather than greying the button. The name is a
  // setting of this PC (Settings.displayName), written through the one
  // chain of choices (store.applySettings), so it cannot race a choice
  // made on another page. A refusal is said in the dialog, which stays.
  import { errorOf } from "../lib/api";
  import { store } from "../lib/state.svelte";
  import { codeText, dialogCopy, vaultCopy } from "../lib/strings";
  import { requiredProblem } from "../lib/validate";
  import { pending } from "../lib/pending";
  import Dialog from "./Dialog.svelte";
  import TextField from "./TextField.svelte";

  interface Props {
    // name: the vault's name as the page shows it.
    name: string;
    onclose: () => void;
  }
  let { name, onclose }: Props = $props();

  // svelte-ignore state_referenced_locally
  let value = $state(name);
  let valid = $state(true);
  let attempt = $state(0);
  let saving = $state<Promise<void> | null>(null);
  let refused = $state("");

  // Spaces at the ends are allowed while typing and stripped when saved,
  // so the name compares trimmed: never a change on its own. The length is
  // the one the Settings field held it to (60), counted in characters.
  const MAX_NAME = 60;
  function judge(v: string): string {
    const t = v.trim();
    if (!t) return requiredProblem(t);
    if ([...t].length > MAX_NAME) return vaultCopy.nameLong(MAX_NAME);
    return t === name.trim() ? vaultCopy.sameName : "";
  }

  async function submit() {
    if (saving) return;
    attempt++;
    refused = "";
    if (judge(value)) return;
    const p = store.applySettings({ displayName: value.trim() });
    saving = p;
    try {
      await p;
      onclose();
    } catch (e) {
      refused = codeText(errorOf(e).code);
    } finally {
      saving = null;
    }
  }
</script>

<Dialog title={vaultCopy.renameTitle} onclose={saving ? undefined : onclose} onsubmit={() => void submit()}>
  <TextField id="vault-name" label={vaultCopy.name} bind:value bind:valid {attempt} {judge} />
  <p class="t-quiet">{vaultCopy.renameLine}</p>
  {#if refused}<p class="field-error" role="alert">{refused}</p>{/if}
  {#snippet actions()}
    <button type="submit" class="btn accent" use:pending={saving}>{vaultCopy.renamePrimary}</button>
    <button type="button" class="btn" disabled={!!saving} onclick={onclose}>{dialogCopy.cancel}</button>
  {/snippet}
</Dialog>

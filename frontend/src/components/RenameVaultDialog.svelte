<script lang="ts">
  // Rename the vault (direction 10, phase 4a; APP.md §6, the Vault page):
  // the one place the vault's name is edited, a dialog of one field that
  // leaves the form rule (APP.md §6, Forms) by the user's call (ruled
  // 2026-11-02). Rename is greyed until the field holds a name that can be
  // saved and is not the vault's own, so the dialog never sends a name it
  // would refuse, nor one that changes nothing. The name the vault has is
  // never said to be wrong: leaving the field unchanged, or pressing Enter
  // in it, marks nothing. An empty or too long name is said under the
  // field by TextField's own marks (lib/forms.ts): once the field is left
  // with it or Enter is pressed in it, and from then on while the typing
  // keeps it wrong, gone the moment the name is right — never as the
  // dialog opens. The greyed button takes no press, but a click on it
  // takes the focus to the box, so it is a leave like any other.
  // The rule is lib/validate's (vaultNameProblem, vaultNameReady). The name
  // is a setting of this PC (Settings.displayName), written through the one
  // chain of choices (store.applySettings), so it cannot race a choice
  // made on another page. A refusal is said in the dialog, which stays.
  import { errorOf } from "../lib/api";
  import { store } from "../lib/state.svelte";
  import { codeText, dialogCopy, vaultCopy } from "../lib/strings";
  import { vaultNameProblem, vaultNameReady } from "../lib/validate";
  import { pending } from "../lib/pending";
  import Dialog from "./Dialog.svelte";
  import TextField from "./TextField.svelte";

  interface Props {
    // name: the vault's name as the page shows it.
    name: string;
    onclose: () => void;
  }
  let { name, onclose }: Props = $props();

  // The name the question is about: the one the dialog opened with, which
  // the field starts from. The `name` prop moves the moment a save begins,
  // since store.applySettings shows the typed name at once; judged against
  // the prop, the name being saved would count as the vault's own, and
  // Rename would grey under its own pending ring and drop the focus.
  // svelte-ignore state_referenced_locally
  const opened = name;
  let value = $state(opened);
  let attempt = $state(0);
  let saving = $state<Promise<void> | null>(null);
  let refused = $state("");

  const judge = (v: string): string => vaultNameProblem(v, opened);
  const ready = $derived(vaultNameReady(value, opened));

  // Enter in the field while Rename is greyed is still the form's press:
  // the browser submits nothing for a form whose default button is
  // disabled, so without this an empty or too long name would meet Enter
  // with nothing done and nothing said, the grey its only signal. Enter
  // that ends an IME composition belongs to the composition, not the form.
  function pressWhileGreyed(e: KeyboardEvent) {
    if (e.key !== "Enter" || e.isComposing || ready || saving) return;
    e.preventDefault();
    void submit();
  }

  async function submit() {
    if (saving) return;
    // Every press counts as the form rule's press, the greyed Enter
    // (pressWhileGreyed) among them: it marks an empty or too long name,
    // and nothing for the vault's own; only an enabled Rename sends.
    attempt++;
    refused = "";
    if (!ready) return;
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
  <div role="presentation" onkeydown={pressWhileGreyed}><TextField id="vault-name" label={vaultCopy.name} bind:value {attempt} {judge} /></div>
  <p class="t-quiet">{vaultCopy.renameLine}</p>
  {#if refused}<div class="field-error" role="alert">{refused}</div>{/if}
  {#snippet actions()}
    <button type="submit" class="btn accent" disabled={!ready} use:pending={saving}>{vaultCopy.renamePrimary}</button>
    <button type="button" class="btn" disabled={!!saving} onclick={onclose}>{dialogCopy.cancel}</button>
  {/snippet}
</Dialog>

<style>
  /* The refusal line is not a paragraph: app.css's `.dialog p` would give
     it the second ink, and a refusal is said in the danger ink. It stands
     in the body's own gap, as the quiet line above it does, so it takes
     none of the field error's own space above it. */
  .field-error { margin-top: 0; }
</style>

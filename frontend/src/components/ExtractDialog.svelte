<script lang="ts">
  // The extract dialog (APP.md §3, the extract dialog, ruled 2026-09-10),
  // for the pane's *Extract…* and the toolbar's *Extract all…* alike: an
  // editable destination prefilled by one rule — *Extract all…* to the
  // archive's own folder plus a folder named after the archive, a
  // selection to the archive's own folder, with no extra level — *Browse…*
  // opening the native picker into it, the four policies with *Ask me
  // about each conflict* the default (ruled 2026-10-14), and *Extract*. No
  // folder is kept from the last time and there is no action that appends
  // a folder: the prefill is the rule; the policy last chosen is kept while
  // Enfold runs (lib/extract.ts). The destination need not exist: the core
  // creates it. The dialog is a form: Enter in the field extracts, and the
  // field has the focus as it opens. The field has no placeholder: an
  // invented path read as where an empty field would extract to, and an
  // empty field extracts nowhere (APP.md §7, ruled 2026-10-17).
  import { untrack } from "svelte";
  import Dialog from "./Dialog.svelte";
  import { Shell } from "../lib/api";
  import { POLICIES, destinationEmpty, rememberPolicy, startingPolicy } from "../lib/extract";
  import { extractCopy } from "../lib/strings";

  interface Props {
    // what is being extracted, for the title: "everything in Photos
    // 2024" (the archive named, ruled 2026-10-17), "2 files and 1 folder"
    label: string;
    // the destination the rule of §3 prefills, empty when the archive's
    // own folder is not known
    initial: string;
    onextract: (dir: string, policy: string) => void;
    oncancel: () => void;
  }
  let { label, initial, onextract, oncancel }: Props = $props();

  // The field is the user's from the moment the dialog opens: the
  // prefill is where it starts, not what it follows.
  let dir = $state(untrack(() => initial));
  let policy = $state(startingPolicy());
  // The field is marked once the user has asked for an extract it cannot
  // do — never while they are still typing into an empty field.
  let attempted = $state(false);
  const empty = $derived(destinationEmpty(dir));

  async function browse() {
    const p = await Shell.PickFolder("Extract to");
    if (p) {
      dir = p;
      attempted = false;
    }
  }

  function go() {
    attempted = true;
    if (empty) return;
    rememberPolicy(policy);
    onextract(dir.trim(), policy);
  }
</script>

<Dialog title="Extract {label}" onclose={oncancel} onsubmit={go}>
  <div class="field">
    <div class="field-top"><label for="xd">Destination folder</label><span class="hint">Created if it is not there yet</span></div>
    <div class="row">
      <input
        id="xd" class="input" bind:value={dir}
        class:invalid={attempted && empty}
        aria-invalid={attempted && empty ? "true" : undefined}
        aria-describedby={attempted && empty ? "xd-said" : undefined}
        oninput={() => (attempted = false)}
      />
      <button type="button" class="btn" onclick={browse}>Browse…</button>
    </div>
    {#if attempted && empty}<div id="xd-said" class="field-error">This field is required.</div>{/if}
  </div>

  <!-- *Ask me about each conflict* is the default (APP.md §3, ruled
       2026-10-14): the one that comes back with a question, so nothing
       already there is overwritten unasked. The last choice is kept while
       Enfold runs. -->
  <fieldset class="field policies">
    <legend class="lbl">{extractCopy.policyLegend}</legend>
    {#each POLICIES as p (p.value)}
      <label class="check"><input type="radio" name="xpolicy" value={p.value} bind:group={policy} />{p.label}</label>
    {/each}
  </fieldset>

  {#snippet actions()}
    <button type="submit" class="btn accent">Extract</button>
    <button type="button" class="btn" onclick={oncancel}>Cancel</button>
  {/snippet}
</Dialog>

<style>
  /* A fieldset that is a .field (APP.md §6, Type and spacing): the options
     stand 8 apart, and the legend — which sits outside the column's gap —
     is a field label, strong, 8 above the first of them. */
  .policies { border: 0; margin: 0; padding: 0; }
  .policies .lbl { font: var(--t-strong); color: var(--ink); padding: 0 0 var(--s2); }
</style>

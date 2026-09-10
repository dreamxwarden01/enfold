<script lang="ts">
  // The extract dialog (APP.md §3, the extract dialog, ruled 2026-09-10),
  // for the pane's *Extract…* and the toolbar's *Extract all* alike: an
  // editable destination prefilled by one rule — *Extract all* to the
  // archive's own folder plus a folder named after the archive, a
  // selection to the archive's own folder, with no extra level — *Browse…*
  // opening the native picker into it, the four policies with *Replace*
  // the default, and *Extract*. No folder is kept from the last time and
  // there is no action that appends a folder: the prefill is the rule. The
  // destination need not exist: the core creates it.
  import { untrack } from "svelte";
  import Dialog from "./Dialog.svelte";
  import { Shell } from "../lib/api";
  import { DEFAULT_POLICY, POLICIES, destinationEmpty } from "../lib/extract";
  import { extractCopy } from "../lib/strings";

  interface Props {
    // what is being extracted, for the title: "everything in this
    // archive", "2 files and 1 folder"
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
  let policy = $state(DEFAULT_POLICY);
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
    onextract(dir.trim(), policy);
  }
</script>

<Dialog title="Extract {label}" onclose={oncancel}>
  <div class="field">
    <div class="field-top"><label for="xd">Destination folder</label><span class="hint">created if it is not there yet</span></div>
    <div class="row">
      <input
        id="xd" class="input" bind:value={dir} placeholder="D:\Extracted"
        class:invalid={attempted && empty}
        aria-invalid={attempted && empty ? "true" : undefined}
        aria-describedby={attempted && empty ? "xd-said" : undefined}
        oninput={() => (attempted = false)}
      />
      <button type="button" class="btn" onclick={browse}>Browse…</button>
    </div>
    {#if attempted && empty}<div id="xd-said" class="field-error">This field is required.</div>{/if}
  </div>

  <!-- Replace is the default (APP.md §3): the wizard default of every
       archiver, and the case the first design forgot. *Ask me about each
       conflict* is the one that comes back with a question. -->
  <fieldset class="field policies">
    <legend class="lbl">{extractCopy.policyLegend}</legend>
    {#each POLICIES as p (p.value)}
      <label class="check"><input type="radio" name="xpolicy" value={p.value} bind:group={policy} />{p.label}</label>
    {/each}
  </fieldset>

  {#snippet actions()}
    <button type="button" class="btn" onclick={oncancel}>Cancel</button>
    <button type="button" class="btn accent" onclick={go}>Extract</button>
  {/snippet}
</Dialog>

<style>
  .policies { border: 0; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 6px; }
  .policies .lbl { font-size: 12.5px; font-weight: 600; color: var(--ink); padding: 0 0 2px; }
</style>

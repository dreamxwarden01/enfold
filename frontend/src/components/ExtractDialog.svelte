<script lang="ts">
  // The extract dialog (APP.md §3, the extract dialog), for the pane's
  // *Extract…* and the toolbar's *Extract all* alike: an editable
  // destination prefilled with the folder last extracted to
  // (settings.json, lastExtractFolder), *Browse…* opening the native
  // picker into it, one action that appends a folder named after the
  // archive — the native picker cannot prefill the name of a folder the
  // user creates, so the field does what WinRAR's destination field does
  // — the skip | rename policy, and *Extract*. The destination need not
  // exist: the core creates it.
  import { untrack } from "svelte";
  import Dialog from "./Dialog.svelte";
  import { Shell } from "../lib/api";
  import { appendArchiveFolder, archiveFolderName, destinationEmpty } from "../lib/extract";

  interface Props {
    // what is being extracted, for the title: "everything in this
    // archive", "2 files and 1 folder"
    label: string;
    archiveName: string;
    // the folder last extracted to, or empty
    initial: string;
    onextract: (dir: string, policy: string) => void;
    oncancel: () => void;
  }
  let { label, archiveName, initial, onextract, oncancel }: Props = $props();

  // The field is the user's from the moment the dialog opens: the
  // prefill is where it starts, not what it follows.
  let dir = $state(untrack(() => initial));
  let policy = $state("skip");
  // The field is marked once the user has asked for an extract it cannot
  // do — never while they are still typing into an empty field.
  let attempted = $state(false);
  const empty = $derived(destinationEmpty(dir));
  const folder = $derived(archiveFolderName(archiveName));

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

  {#if folder}
    <button type="button" class="btn link" onclick={() => (dir = appendArchiveFolder(dir, archiveName))}>+ a folder named after the archive</button>
  {/if}

  <div class="field">
    <div class="field-top"><label for="xp">If a file already exists</label></div>
    <select id="xp" class="input" bind:value={policy}><option value="skip">Skip it</option><option value="rename">Extract under a new name</option></select>
  </div>

  {#snippet actions()}
    <button type="button" class="btn" onclick={oncancel}>Cancel</button>
    <button type="button" class="btn accent" onclick={go}>Extract</button>
  {/snippet}
</Dialog>

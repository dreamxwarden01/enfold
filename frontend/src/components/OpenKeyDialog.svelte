<script lang="ts">
  // A file opened from Explorer whose key is not in this vault (APP.md
  // §14, decision 4). The file *is* an Enfold archive — its envelope
  // decoded — and no record here holds the archive_id in it, so there is
  // one way in and this dialog offers it: *Import records…*, which lives
  // on the Archives page. Nothing of the file was changed, and the dialog
  // says so, because a user who double-clicked an archive deserves to
  // know it is still whole.
  //
  // A dialog and not a toast because a toast carries no action (Toasts
  // draws text alone), and the way in is the only thing worth saying here.
  import { openCopy } from "../lib/strings";
  import { baseName } from "../lib/dragout";
  import Dialog from "./Dialog.svelte";

  interface Props {
    // The path Explorer handed over; only its leaf is shown.
    path: string;
    onclose: () => void;
    onimport: () => void;
  }
  let { path, onclose, onimport }: Props = $props();
</script>

<Dialog title={openCopy.title(baseName(path))} onclose={onclose}>
  <p>{openCopy.keyNotInVault}</p>
  <p class="t-quiet note">{openCopy.how}</p>

  {#snippet actions()}
    <button type="button" class="btn" onclick={onclose}>{openCopy.close}</button>
    <button type="button" class="btn accent" onclick={onimport}>{openCopy.importRecords}</button>
  {/snippet}
</Dialog>

<style>
  .note { margin: 10px 0 0; }
</style>

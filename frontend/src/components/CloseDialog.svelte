<script lang="ts">
  // The close question (APP.md §2.4, ruled 2026-09-13: "we must not
  // default to going to the tray"). The shell cancels the first close —
  // the button, Alt+F4, the taskbar's close — and emits `shell.close`;
  // this is what it asks. There is no Cancel button because there are only
  // two answers, and the way out is the dialogs' own rule (APP.md §6,
  // "Dialogs stay put"): a click on the backdrop is nothing at all, and
  // Esc is the cancel — the window simply stays, and nothing is called.
  import { closeCopy } from "../lib/strings";
  import type { Decision } from "../lib/closing";
  import Dialog from "./Dialog.svelte";

  interface Props {
    // onclose is Esc: the window stays and the shell is told nothing.
    onclose: () => void;
    ondecide: (action: Decision, remember: boolean) => void;
    // busy: the answer is on its way to the shell. The dialog stays put
    // and answers nothing more; the window's going is what closes it.
    busy?: boolean;
  }
  let { onclose, ondecide, busy = false }: Props = $props();

  // Unticked: remembering is the user's to ask for, never the default.
  let remember = $state(false);
</script>

<Dialog title={closeCopy.title} onclose={busy ? undefined : onclose}>
  <p>{closeCopy.body}</p>
  <label class="check"><input type="checkbox" bind:checked={remember} disabled={busy} />{closeCopy.remember}</label>
  <p class="t-quiet note">{closeCopy.inSettings}</p>

  {#snippet actions()}
    <button type="button" class="btn" disabled={busy} onclick={() => ondecide("quit", remember)}>{closeCopy.quit}</button>
    <button type="button" class="btn accent" disabled={busy} onclick={() => ondecide("tray", remember)}>{closeCopy.tray}</button>
  {/snippet}
</Dialog>

<style>
  .note { margin: 10px 0 0; }
</style>

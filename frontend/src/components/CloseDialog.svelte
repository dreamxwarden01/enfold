<script lang="ts">
  // The close question (APP.md §2.3, ruled 2026-09-13: "we must not
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
  <p class="t-quiet">{closeCopy.inSettings}</p>

  {#snippet actions()}
    <!-- Two standard buttons, Quit first, and the box keeps the focus
         (APP.md §2.3, ruled 2026-10-17): the close button never means the
         tray by default (ruled 2026-09-13), so neither answer is the
         primary, and the foot band's primary-first order (§6, Controls)
         has nothing to put first. -->
    <button type="button" class="btn" disabled={busy} onclick={() => ondecide("quit", remember)}>{closeCopy.quit}</button>
    <button type="button" class="btn" disabled={busy} onclick={() => ondecide("tray", remember)}>{closeCopy.tray}</button>
  {/snippet}
</Dialog>

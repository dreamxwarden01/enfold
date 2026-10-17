<script lang="ts">
  // The close question (APP.md §2.3). The shell cancels the first close —
  // the button, Alt+F4, the taskbar's close — and emits `shell.close`;
  // this is what it asks. Quit is the default (ruled 2026-10-17, the
  // user's), which keeps the ruling of 2026-09-13 ("we must not default to
  // going to the tray"). There is no Cancel button: the foot holds the two
  // answers, and the way out is the close button in the title's corner or
  // Esc — the window simply stays, and nothing is called. A click on the
  // backdrop is nothing at all (APP.md §6, "Dialogs stay put").
  import { closeCopy } from "../lib/strings";
  import type { Decision } from "../lib/closing";
  import Dialog from "./Dialog.svelte";

  interface Props {
    // onclose is Esc and the corner's close button: the window stays and
    // the shell is told nothing.
    onclose: () => void;
    ondecide: (action: Decision, remember: boolean) => void;
    // busy: the answer is on its way to the shell. The dialog stays put
    // and answers nothing more; the window's going is what closes it.
    busy?: boolean;
  }
  let { onclose, ondecide, busy = false }: Props = $props();

  // Unticked: remembering is the user's to ask for, never the default.
  // Either answer carries it.
  let remember = $state(false);

  // Quit is the form's submit, so Enter presses it. The dialog stays a
  // form while busy — dropping onsubmit would remount its body — and the
  // disabled buttons and this guard keep a second answer from going out.
  function quit() {
    if (!busy) ondecide("quit", remember);
  }
</script>

<Dialog title={closeCopy.title} onclose={busy ? undefined : onclose} onsubmit={quit} closeButton>
  <p>{closeCopy.body}</p>
  <label class="check"><input type="checkbox" bind:checked={remember} disabled={busy} />{closeCopy.remember}</label>
  <p class="t-quiet">{closeCopy.inSettings}</p>

  {#snippet actions()}
    <!-- Quit is the primary (APP.md §2.3, ruled 2026-10-17): the accent,
         first in the foot band (§6, Controls), and the submit, so Enter
         presses it; the dialog opens with the focus on it, as a form with
         no field does (Dialog.svelte), and never on the tray. Keep running
         in the tray is the standard button beside it. -->
    <button type="submit" class="btn accent" disabled={busy}>{closeCopy.quit}</button>
    <button type="button" class="btn" disabled={busy} onclick={() => ondecide("tray", remember)}>{closeCopy.tray}</button>
  {/snippet}
</Dialog>

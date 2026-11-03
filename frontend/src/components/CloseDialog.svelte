<script lang="ts">
  // The close question (APP.md §2.3). The shell cancels the first close —
  // the button, Alt+F4, the taskbar's close — and emits `shell.close`;
  // this is what it asks. Quit is the default (ruled 2026-10-17, the
  // user's), which keeps the ruling of 2026-09-13 ("we must not default to
  // going to the tray"). There is no Cancel button: the foot holds the two
  // answers, and the way out is the close button in the title's corner or
  // Esc — the window simply stays, and nothing is called. A click on the
  // backdrop is nothing at all (APP.md §6, "Dialogs stay put").
  //
  // The quit question is the same dialog with its own answers (mode
  // "quit"; APP.md §2.4, ruled 2026-11-03, the user's): a quit asked for
  // while an operation runs — the window's close under the Quit setting,
  // which never goes to the tray, or the tray's Quit, which shows the
  // window first — asks Quit or Keep running, with no tray and nothing to
  // remember; Esc and the corner's close are Keep running.
  //
  // While an operation runs each question says so in a sentence (line):
  // what runs and what each answer does, "Adding 20 files to Photos 2024 is
  // still running. Quit stops it; in the tray it goes on." The question
  // stays until it is answered — an operation that ends meanwhile neither
  // takes it down nor quits anything — and the sentence then says it has
  // finished (lib/closing.ts askedLine). It is a polite live region, so
  // that change is read out as well as seen.
  import { closeCopy } from "../lib/strings";
  import type { Decision } from "../lib/closing";
  import Dialog from "./Dialog.svelte";

  interface Props {
    // close: the close question; quit: the quit question.
    mode?: "close" | "quit";
    // The sentence about what runs, or "" when nothing was named.
    line?: string;
    // onclose is Esc and the corner's close button: for the close question
    // the window stays and the shell is told nothing; for the quit
    // question it is Keep running.
    onclose: () => void;
    // The answer: tray or quit for the close question, with remember;
    // quit alone for the quit question, which remembers nothing — its Keep
    // running is onclose, the same answer as Esc.
    ondecide: (action: Decision, remember: boolean) => void;
    // busy: the answer is on its way to the shell. The dialog stays put
    // and answers nothing more; the window's going is what closes it.
    busy?: boolean;
  }
  let { mode = "close", line = "", onclose, ondecide, busy = false }: Props = $props();
  const asksQuit = $derived(mode === "quit");

  // Unticked: remembering is the user's to ask for, never the default.
  // Either answer carries it.
  let remember = $state(false);

  // Quit is the form's submit, so Enter presses it. The dialog stays a
  // form while busy — dropping onsubmit would remount its body — and the
  // disabled buttons and this guard keep a second answer from going out.
  function quit() {
    if (!busy) ondecide("quit", !asksQuit && remember);
  }
</script>

<Dialog title={asksQuit ? closeCopy.quitTitle : closeCopy.title} onclose={busy ? undefined : onclose} onsubmit={quit} closeButton closeTitle={asksQuit ? closeCopy.keepRunningEsc : undefined}>
  {#if !asksQuit}<p>{closeCopy.body}</p>{/if}
  <p class="asked" aria-live="polite" hidden={!line}>{line}</p>
  {#if !asksQuit}
    <label class="check"><input type="checkbox" bind:checked={remember} disabled={busy} />{closeCopy.remember}</label>
    <p class="t-quiet">{closeCopy.inSettings}</p>
  {/if}

  {#snippet actions()}
    <!-- Quit is the primary (APP.md §2.3, ruled 2026-10-17): the accent,
         first in the foot band (§6, Controls), and the submit, so Enter
         presses it; the dialog opens with the focus on it, as a form with
         no field does (Dialog.svelte), and never on the tray. Keep running
         in the tray is the standard button beside it — Keep running in the
         quit question, which offers no tray. -->
    <button type="submit" class="btn accent" disabled={busy}>{closeCopy.quit}</button>
    {#if asksQuit}
      <button type="button" class="btn" disabled={busy} onclick={onclose}>{closeCopy.keepRunning}</button>
    {:else}
      <button type="button" class="btn" disabled={busy} onclick={() => ondecide("tray", remember)}>{closeCopy.tray}</button>
    {/if}
  {/snippet}
</Dialog>

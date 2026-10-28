<script lang="ts">
  // The question a dialog asks before an act that may re-key the vault: a
  // removal, a password change, turning the password on (DESIGN §5; ruled
  // 2026-10-27). It asks the fact the user can answer, never the technical
  // choice, and the answer that re-keys stands first and pre-selected, so a
  // user who reads one line reads the one that re-keys. Each answer has a
  // second line that says what it does, always drawn, so the dialog keeps
  // its height whichever is chosen (APP.md §6, Type and spacing: an
  // option's label is the ink, its description the second ink).
  //
  // A fieldset with its legend as the field label, marked data-question:
  // Dialog opens a form on its checked answer (Dialog.svelte's focus), so
  // a screen reader reads the question first, and Enter on it still
  // presses the form's submit. The words are the dialog's (lib/rekey.ts
  // questionOf); the two second lines are the same for every question.
  import type { Answer } from "../lib/rekey";
  import { dialogCopy } from "../lib/strings";

  interface Props {
    legend: string;
    // rekey: the re-keying answer's words; keep: the other's.
    rekey: string;
    keep: string;
    answer: Answer;
  }
  let { legend, rekey, keep, answer = $bindable() }: Props = $props();

  // One name per question, so two dialogs' radios never join one group.
  const name = $props.id();
</script>

<fieldset class="field choices" data-question>
  <legend class="lbl">{legend}</legend>
  <label class="check">
    <input type="radio" {name} value="rekey" bind:group={answer} />
    <span class="two"><span>{rekey}</span><small>{dialogCopy.rekeyQuestion.rekeys}</small></span>
  </label>
  <label class="check">
    <input type="radio" {name} value="keep" bind:group={answer} />
    <span class="two"><span>{keep}</span><small>{dialogCopy.rekeyQuestion.keeps}</small></span>
  </label>
</fieldset>

<style>
  /* A fieldset that is a .field (APP.md §6, Type and spacing), as the
     extract dialog's: the options stand 8 apart, and the legend — which
     sits outside the column's gap — is a field label, strong, 8 above the
     first of them. */
  .choices { border: 0; margin: 0; padding: 0; min-width: 0; }
  .choices .lbl { font: var(--t-strong); color: var(--ink); padding: 0 0 var(--s2); }
  /* Each answer is two lines, its words in the ink and what it does under
     them in caption: the radio stands on the first line, as a greyed
     radio's does in the extract dialog. */
  .check { align-items: flex-start; }
  .check input { margin-top: 3px; flex: none; }
  .two { display: flex; flex-direction: column; min-width: 0; }
  .two small { font: var(--t-cap); color: var(--ink-2); }
</style>

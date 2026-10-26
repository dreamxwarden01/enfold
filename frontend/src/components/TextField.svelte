<script lang="ts">
  // A required text field under the form rule (APP.md §6): its error is
  // shown once the user leaves it or the form is submitted with something
  // wrong, gone the moment the value is right, back only after the field
  // is left again — never on a keystroke, and never because the window
  // lost focus (lib/forms.ts). The parent bumps `attempt` on submit and
  // reads `valid`. Its right-click, Shift+F10 and Menu key open the app's
  // Cut · Copy · Paste · Select all (lib/fieldmenu.ts; direction 7): it is
  // never a secret's field — those are SecretField's.
  import { untrack } from "svelte";
  import { fade } from "svelte/transition";
  import { fieldMenu } from "../lib/fieldmenu";
  import { nextMark, onLeave } from "../lib/forms";
  import { motion } from "../lib/motion";
  import { requiredProblem } from "../lib/validate";

  interface Props {
    id: string;
    label: string;
    value: string;
    valid?: boolean;
    attempt?: number;
    hint?: string;
    placeholder?: string;
    required?: boolean;
    // judge: what this field's value is missing, when being filled in is
    // not all that is asked of it — an archive's name is bounded in bytes
    // as well (APP.md §6), and a bound the app knows is judged here rather
    // than paid for with a file on disk. Required-only by default.
    judge?: (value: string) => string;
  }
  let { id, label, value = $bindable(""), valid = $bindable(true), attempt = 0, hint = "", placeholder = "", required = true, judge = requiredProblem }: Props = $props();
  let mark = $state(false);
  let seen = $state(untrack(() => attempt)); // only a press made while this field is on screen counts

  const problem = $derived(required ? judge(value) : "");
  const show = $derived(mark && !!problem);

  $effect(() => {
    valid = !problem;
  });
  $effect(() => {
    if (attempt > seen) {
      seen = attempt;
      mark = nextMark("press", untrack(() => mark), untrack(() => problem));
    }
  });

  function typed() {
    mark = nextMark("change", mark, required ? judge(value) : "");
  }

  const leave = onLeave(() => (mark = nextMark("leave", mark, problem)));
</script>

<div class="field">
  <div class="field-top"><label for={id}>{label}</label>{#if hint}<span class="hint">{hint}</span>{/if}</div>
  <input {id} class="input" class:invalid={show} bind:value {placeholder} aria-invalid={show} aria-describedby={show ? `${id}-said` : undefined} onblur={leave} oninput={typed} use:fieldMenu />
  {#if show}<div id="{id}-said" class="field-error" transition:fade={motion()}>{problem}</div>{/if}
</div>

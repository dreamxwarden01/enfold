<script lang="ts">
  // A required text field under the form rule (APP.md §6): its error is
  // shown once the user leaves it or the form is submitted, gone the
  // moment the value is right, back only after the field is left again.
  // The parent bumps `attempt` on submit and reads `valid`.
  import { untrack } from "svelte";
  import { fade } from "svelte/transition";
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
  }
  let { id, label, value = $bindable(""), valid = $bindable(true), attempt = 0, hint = "", placeholder = "", required = true }: Props = $props();
  let left = $state(false);
  let pressed = $state(false);
  let seen = $state(untrack(() => attempt)); // only a press made while this field is on screen counts

  const problem = $derived(required ? requiredProblem(value) : "");
  const show = $derived((left || pressed) && !!problem);

  $effect(() => {
    valid = !problem;
  });
  $effect(() => {
    if (attempt > seen) {
      seen = attempt;
      pressed = true;
    }
  });

  function typed() {
    if (!(required ? requiredProblem(value) : "")) {
      left = false;
      pressed = false;
    }
  }
</script>

<div class="field">
  <div class="field-top"><label for={id}>{label}</label>{#if hint}<span class="hint">{hint}</span>{/if}</div>
  <input {id} class="input" class:invalid={show} bind:value {placeholder} aria-invalid={show} aria-describedby={show ? `${id}-said` : undefined} onblur={() => (left = true)} oninput={typed} />
  {#if show}<div id="{id}-said" class="field-error" transition:fade={motion()}>{problem}</div>{/if}
</div>

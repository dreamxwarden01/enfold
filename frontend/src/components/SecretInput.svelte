<script lang="ts">
  // One prompt's field. The value goes over the raw channel on submit and
  // the field is cleared; it is never held anywhere else on this side.
  // Its errors follow the form rule (APP.md §6): shown once the user
  // leaves the field or presses the button, gone the moment the value is
  // right, and back only after the field is left again.
  import { fade } from "svelte/transition";
  import { submitSecret } from "../lib/api";
  import { motion } from "../lib/motion";
  import { RECOVERY_SEPARATORS, secretProblem, secretRule } from "../lib/validate";
  import type { SecretKind } from "../lib/validate";

  interface Props {
    kind: SecretKind;
    promptId: string;
    label: string;
    hint?: string;
    note?: string;
    button?: string;
    choose?: boolean;
    // error: what the core said about the last answer under this prompt
    // — "Wrong PIN.", say — shown red beneath until the user types again.
    error?: string;
  }
  let { kind, promptId, label, hint = "", note = "", button = "Continue", choose = false, error = "" }: Props = $props();
  let value = $state("");
  let el: HTMLInputElement | undefined = $state();
  let left = $state(false); // the field was left with what it holds
  let pressed = $state(false); // the button was pressed with what it holds
  let typedSince = $state(false); // typed since the core's error arrived

  const numeric = $derived(kind === "pin" || kind === "recovery");
  const problem = $derived(secretProblem(kind, value, choose));
  const rule = $derived(secretRule(kind, choose));
  const show = $derived((left || pressed) && !!problem);
  // The rule itself turns red when it is what is broken; anything else is
  // said beneath it.
  const ruleBroken = $derived(show && problem === rule);
  const refused = $derived(!!error && !typedSince);
  const said = $derived(show && problem !== rule ? problem : refused ? error : "");

  $effect(() => {
    // A new prompt: a clean field, focused.
    void promptId;
    value = "";
    left = false;
    pressed = false;
    typedSince = false;
    el?.focus();
  });

  function typed() {
    typedSince = true;
    if (!secretProblem(kind, value, choose)) {
      left = false;
      pressed = false;
    }
  }

  function submit(e: Event) {
    e.preventDefault();
    pressed = true;
    if (problem) return;
    const v = kind === "recovery" ? value.replace(RECOVERY_SEPARATORS, "") : value;
    submitSecret(kind, promptId, v);
    value = "";
    left = false;
    pressed = false;
  }
</script>

<form class="field" onsubmit={submit} novalidate>
  <div class="field-top">
    <label for="secret-{promptId}">{label}</label>
    {#if hint}<span class="hint num">{hint}</span>{/if}
  </div>
  <input
    id="secret-{promptId}"
    bind:this={el}
    bind:value
    class="input secret"
    class:invalid={show || refused}
    type={kind === "mgmtkey" ? "text" : "password"}
    inputmode={numeric ? "numeric" : "text"}
    autocomplete="off"
    autocapitalize="off"
    spellcheck="false"
    aria-label={label}
    aria-invalid={show || refused}
    aria-describedby={[rule ? `secret-${promptId}-rule` : "", said ? `secret-${promptId}-said` : ""].filter(Boolean).join(" ") || undefined}
    onblur={() => (left = true)}
    oninput={typed}
  />
  {#if rule}<div id="secret-{promptId}-rule" class="pin-note" class:danger={ruleBroken}>{rule}</div>{/if}
  {#if said}<div id="secret-{promptId}-said" class="field-error" transition:fade={motion()}>{said}</div>{/if}
  {#if note}<div class="pin-note">{note}</div>{/if}
  <div class="submit"><button type="submit" class="btn accent wide">{button}</button></div>
</form>

<style>
  .submit { margin-top: 12px; }
</style>

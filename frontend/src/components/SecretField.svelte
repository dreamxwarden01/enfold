<script lang="ts">
  // One secret's field: the label, the count or hint beside it, the masked
  // input, the static rule under it and what is wrong now. It holds no
  // policy of its own — SecretInput decides when a field is marked and
  // what it says (APP.md §6, Forms) — so that a form carrying more than
  // one field says it the same way in each.
  import { fade } from "svelte/transition";
  import { motion } from "../lib/motion";

  interface Props {
    id: string;
    label: string;
    hint?: string;
    // rule: the standing requirement, red when it is what is broken.
    rule?: string;
    ruleBroken?: boolean;
    // said: what the field is refusing now, or the core's own refusal.
    said?: string;
    // note: a quiet line under the field, when the field carries one.
    note?: string;
    text?: boolean; // a management key is typed in the clear
    numeric?: boolean;
    invalid?: boolean;
    value: string;
    el?: HTMLInputElement;
    onblur?: () => void;
    oninput?: () => void;
  }
  let {
    id,
    label,
    hint = "",
    rule = "",
    ruleBroken = false,
    said = "",
    note = "",
    text = false,
    numeric = false,
    invalid = false,
    value = $bindable(""),
    el = $bindable(),
    onblur,
    oninput,
  }: Props = $props();
</script>

<div class="field-top">
  <label for={id}>{label}</label>
  {#if hint}<span class="hint num">{hint}</span>{/if}
</div>
<input
  {id}
  bind:this={el}
  bind:value
  class="input secret"
  class:invalid
  type={text ? "text" : "password"}
  inputmode={numeric ? "numeric" : "text"}
  autocomplete="off"
  autocapitalize="off"
  spellcheck="false"
  aria-label={label}
  aria-invalid={invalid}
  aria-describedby={[rule ? `${id}-rule` : "", said ? `${id}-said` : ""].filter(Boolean).join(" ") || undefined}
  onblur={() => onblur?.()}
  oninput={() => oninput?.()}
/>
{#if rule}<div id="{id}-rule" class="pin-note" class:danger={ruleBroken}>{rule}</div>{/if}
{#if said}<div id="{id}-said" class="field-error" transition:fade={motion()}>{said}</div>{/if}
{#if note}<div class="pin-note">{note}</div>{/if}

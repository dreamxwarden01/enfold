<script lang="ts">
  // One secret's field: the label, the count or hint beside it, the masked
  // input, the static rule under it and what is wrong now. It holds no
  // policy of its own — SecretInput decides when a field is marked and
  // what it says (APP.md §6, Forms) — so that a form carrying more than
  // one field says it the same way in each.
  import { fade } from "svelte/transition";
  import { FAST, GAP, delay, motion } from "../lib/motion";

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
    // hintTone: the hint's ink — the retries turn attention at two and
    // danger at one (APP.md §6, ruled 2026-10-17).
    hintTone?: "" | "attention" | "danger";
    // reserve: one line under the field, always there, where the rule and
    // what is wrong cross-fade in place (the lock screen's panel, ruled
    // 2026-10-17): an error arriving moves nothing below it.
    reserve?: boolean;
    text?: boolean; // a management key is typed in the clear
    numeric?: boolean;
    invalid?: boolean;
    value: string;
    el?: HTMLInputElement;
    onblur?: (e: FocusEvent) => void;
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
    hintTone = "",
    reserve = false,
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
  {#if hint}<span class="hint num" class:attention={hintTone === "attention"} class:danger={hintTone === "danger"}>{hint}</span>{/if}
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
  onblur={(e) => onblur?.(e)}
  oninput={() => oninput?.()}
/>
{#if reserve}
  <!-- One 16 px line: what is wrong replaces the rule in it, 140 ms, and
       the rule comes back when it is right. -->
  <div class="fieldline">
    {#if said}
      <div id="{id}-said" class="field-error" in:fade={motion(FAST, { delay: delay(GAP) })} out:fade={motion(FAST)}>{said}</div>
    {:else if rule}
      <div id="{id}-rule" class="pin-note" class:danger={ruleBroken} in:fade={motion(FAST, { delay: delay(GAP) })} out:fade={motion(FAST)}>{rule}</div>
    {/if}
  </div>
{:else}
  {#if rule}<div id="{id}-rule" class="pin-note" class:danger={ruleBroken}>{rule}</div>{/if}
  {#if said}<div id="{id}-said" class="field-error" transition:fade={motion()}>{said}</div>{/if}
{/if}
{#if note}<div class="pin-note">{note}</div>{/if}

<style>
  .fieldline { display: grid; min-height: 16px; }
  .fieldline > :global(*) { grid-area: 1 / 1; margin-top: 0; }
  .hint.attention { color: var(--attention); }
  .hint.danger { color: var(--danger); font-weight: 600; }
</style>

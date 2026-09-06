<script lang="ts">
  // One prompt's field. The value goes over the raw channel on submit and
  // the field is cleared; it is never held anywhere else on this side.
  import { submitSecret } from "../lib/api";

  interface Props {
    kind: "pin" | "password" | "recovery" | "mgmtkey";
    promptId: string;
    label: string;
    hint?: string;
    note?: string;
    button?: string;
  }
  let { kind, promptId, label, hint = "", note = "", button = "Continue" }: Props = $props();
  let value = $state("");
  let el: HTMLInputElement | undefined = $state();

  const numeric = $derived(kind === "pin" || kind === "recovery");

  $effect(() => {
    // A new prompt: a clean field, focused.
    void promptId;
    value = "";
    el?.focus();
  });

  function submit(e: Event) {
    e.preventDefault();
    const v = kind === "recovery" ? value.replace(/[\s-]/g, "") : value;
    if (!v) return;
    submitSecret(kind, promptId, v);
    value = "";
  }
</script>

<form class="field" onsubmit={submit}>
  <div class="field-top">
    <label for="secret-{promptId}">{label}</label>
    {#if hint}<span class="hint num">{hint}</span>{/if}
  </div>
  <input
    id="secret-{promptId}"
    bind:this={el}
    bind:value
    class="input secret"
    type={kind === "mgmtkey" ? "text" : "password"}
    inputmode={numeric ? "numeric" : "text"}
    autocomplete="off"
    autocapitalize="off"
    spellcheck="false"
    aria-label={label}
  />
  {#if note}<div class="pin-note">{note}</div>{/if}
  <div class="submit"><button type="submit" class="btn accent wide" disabled={!value}>{button}</button></div>
</form>

<style>
  .submit { margin-top: 12px; }
</style>

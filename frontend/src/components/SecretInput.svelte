<script lang="ts">
  // One prompt's form. The value goes over the raw channel on submit and
  // the field is cleared; it is never held anywhere else on this side.
  // Its errors follow the form rule (APP.md §6): shown once the user
  // leaves the field or presses the button, gone the moment the value is
  // right, and back only after the field is left again.
  //
  // Three shapes, one form and one button:
  //  · a secret asked for (a PIN, a password, a management key);
  //  · a secret chosen, typed twice under one prompt (APP.md §13);
  //  · the merged "Password and PIN" card (APP.md §6, the ruling of
  //    2026-09-08), where the button answers the PIN's prompt and this
  //    form's own field — the vault's password — keeps what was typed
  //    across a refused PIN and goes out on its own prompt, unpressed.
  import { untrack } from "svelte";
  import { submitSecret } from "../lib/api";
  import { PIN_RULE, RECOVERY_SEPARATORS, autoSendable, confirmSecretProblem, secretProblem, secretRule } from "../lib/validate";
  import type { SecretKind } from "../lib/validate";
  import SecretField from "./SecretField.svelte";

  // PinSide is the key's PIN above the vault's password on the merged
  // card: its own copy, its own count and its own refusal.
  interface PinSide {
    label: string;
    hint?: string;
    note?: string;
    error?: string;
  }

  interface Props {
    kind: SecretKind;
    // promptId: the prompt this form's button answers — the PIN's when a
    // pin side is given.
    promptId: string;
    label: string;
    hint?: string;
    note?: string;
    button?: string;
    choose?: boolean;
    // error: what the core said about the last answer under this prompt
    // — "Wrong PIN.", say — shown red beneath until the user types again.
    error?: string;
    pin?: PinSide | null;
    // send: answer promptId at once from this field as it stands, with no
    // further click (the password prompt an accepted PIN brought).
    send?: boolean;
  }
  let { kind, promptId, label, hint = "", note = "", button = "Continue", choose = false, error = "", pin = null, send = false }: Props = $props();
  let value = $state("");
  let el: HTMLInputElement | undefined = $state();
  let left = $state(false); // the field was left with what it holds
  // Pressing the button marks every field that would refuse, and a mark
  // clears when *that* field is corrected (APP.md §6): the flag is per
  // field, so correcting one never un-marks the other while it still
  // refuses.
  let pressed = $state(false); // the button was pressed with what it holds
  let typedSince = $state(false); // typed since the core's error arrived

  // A chosen password is typed twice (APP.md §13, the ruling of
  // 2026-09-07): one core prompt, two fields under its id, one
  // submission — the secret crosses the boundary once.
  let confirm = $state("");
  let confirmLeft = $state(false);
  let confirmPressed = $state(false);
  const twice = $derived(kind === "password" && choose);
  const confirmProblem = $derived(twice ? confirmSecretProblem(value, confirm) : "");
  const confirmSaid = $derived((confirmLeft || confirmPressed) && confirmProblem ? confirmProblem : "");

  // The merged card's own field, above this one.
  const paired = $derived(!!pin);
  let pinValue = $state("");
  let pinEl: HTMLInputElement | undefined = $state();
  let pinLeft = $state(false);
  let pinPressed = $state(false);
  let pinTypedSince = $state(false);
  const pinProblem = $derived(paired ? secretProblem("pin", pinValue) : "");
  const pinShow = $derived((pinLeft || pinPressed) && !!pinProblem);
  const pinRefused = $derived(!!pin?.error && !pinTypedSince);
  const pinSaid = $derived(pinShow && pinProblem !== PIN_RULE ? pinProblem : pinRefused ? (pin?.error ?? "") : "");

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
    // A new prompt. Which field it belongs to is the form's shape: on the
    // merged card the prompt is the PIN's, and the password beside it is
    // left exactly as the user has it — a wrong PIN moves nothing else.
    void promptId;
    void send;
    untrack(() => {
      if (send && promptId) {
        // The prompt an accepted PIN brought: the field goes as it stands,
        // in whatever state it was left in, with no further click — but
        // judged first, like every typed secret (APP.md §6). A field the
        // form would refuse, emptied in the moment between the press and
        // the prompt, is marked here instead and waits for a Continue.
        if (!autoSendable(kind, value, choose)) {
          pressed = true;
          el?.focus();
          return;
        }
        submitSecret(kind, promptId, value);
        value = "";
        left = false;
        pressed = false;
        typedSince = false;
        return;
      }
      if (paired) {
        pinValue = "";
        pinLeft = false;
        pinPressed = false;
        pinTypedSince = false;
        pinEl?.focus();
        return;
      }
      value = "";
      confirm = "";
      left = false;
      confirmLeft = false;
      pressed = false;
      confirmPressed = false;
      typedSince = false;
      el?.focus();
    });
  });

  function typed() {
    typedSince = true;
    if (!secretProblem(kind, value, choose)) {
      left = false;
      pressed = false;
    }
  }

  function typedPin() {
    pinTypedSince = true;
    if (!secretProblem("pin", pinValue)) {
      pinLeft = false;
      pinPressed = false;
    }
  }

  function typedConfirm() {
    if (!confirmSecretProblem(value, confirm)) {
      confirmLeft = false;
      confirmPressed = false;
    }
  }

  function submit(e: Event) {
    e.preventDefault();
    pressed = true;
    confirmPressed = true;
    pinPressed = true;
    if (paired) {
      // Both fields are judged, so an empty password is marked here and
      // not paid for with a touch; only the PIN goes out.
      if (pinProblem || problem) return;
      submitSecret("pin", promptId, pinValue);
      pinValue = "";
      pinLeft = false;
      pinPressed = false;
      return;
    }
    if (problem || confirmProblem) return;
    const v = kind === "recovery" ? value.replace(RECOVERY_SEPARATORS, "") : value;
    submitSecret(kind, promptId, v);
    value = "";
    confirm = "";
    left = false;
    confirmLeft = false;
    pressed = false;
    confirmPressed = false;
  }
</script>

<form class="field" onsubmit={submit} novalidate>
  {#if pin}
    <SecretField
      id="secret-{promptId}-pin"
      label={pin.label}
      hint={pin.hint ?? ""}
      rule={PIN_RULE}
      ruleBroken={pinShow && pinProblem === PIN_RULE}
      said={pinSaid}
      note={pin.note ?? ""}
      numeric
      invalid={pinShow || pinRefused}
      bind:value={pinValue}
      bind:el={pinEl}
      onblur={() => (pinLeft = true)}
      oninput={typedPin}
    />
    <div class="pairfield">
      <SecretField id="secret-{promptId}" {label} {hint} said={said} invalid={show || refused} bind:value bind:el onblur={() => (left = true)} oninput={typed} />
    </div>
  {:else}
    <SecretField
      id="secret-{promptId}"
      {label}
      {hint}
      {rule}
      {ruleBroken}
      said={said}
      text={kind === "mgmtkey"}
      {numeric}
      invalid={show || refused}
      bind:value
      bind:el
      onblur={() => (left = true)}
      oninput={typed}
    />
  {/if}
  {#if twice}
    <div class="pairfield">
      <SecretField id="secret-{promptId}-again" label="Type it again" said={confirmSaid} invalid={!!confirmSaid} bind:value={confirm} onblur={() => (confirmLeft = true)} oninput={typedConfirm} />
    </div>
  {/if}
  {#if note}<div class="pin-note">{note}</div>{/if}
  <div class="submit"><button type="submit" class="btn accent wide">{button}</button></div>
</form>

<style>
  .submit { margin-top: 12px; }
  .pairfield { display: flex; flex-direction: column; gap: 4px; margin-top: 10px; }
</style>

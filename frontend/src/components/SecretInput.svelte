<script lang="ts">
  // One prompt's form. The value goes over the raw channel on submit and
  // the field is cleared; it is never held anywhere else on this side.
  // Its errors follow the form rule (APP.md §6): shown once the user
  // leaves the field or presses the button with something wrong, gone the
  // moment the value is right, and back only after the field is left
  // again — never on a keystroke, never because the window lost focus, and
  // never carried into a new prompt (lib/forms.ts).
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
  import { nextMark, onLeave } from "../lib/forms";
  import { PIN_RULE, RECOVERY_SEPARATORS, autoSendable, confirmSecretProblem, secretProblem, secretRule } from "../lib/validate";
  import type { SecretKind } from "../lib/validate";
  import SecretField from "./SecretField.svelte";

  // PinSide is the key's PIN above the vault's password on the merged
  // card: its own copy, its own count and its own refusal.
  interface PinSide {
    label: string;
    hint?: string;
    hintTone?: "" | "attention" | "danger";
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
    // The lock screen's panel (APP.md §6, ruled 2026-10-17): each field's
    // one reserved line, the hint's ink, and *Cancel* as a standard button
    // beside the accent *Continue* rather than a link far below it.
    reserve?: boolean;
    hintTone?: "" | "attention" | "danger";
    cancel?: { label: string; run: () => void } | null;
    // formId: the form's buttons are a dialog's, in its foot band (APP.md
    // §6, Controls; ruled 2026-10-17): the form takes this id and draws
    // none, and the foot's submit names it, so Enter in a field still
    // presses it.
    formId?: string;
  }
  let { kind, promptId, label, hint = "", note = "", button = "Continue", choose = false, error = "", pin = null, send = false, reserve = false, hintTone = "", cancel = null, formId }: Props = $props();
  let value = $state("");
  let el: HTMLInputElement | undefined = $state();
  // mark: the field is marked — left or pressed with something wrong, and
  // not corrected since (APP.md §6). Pressing the button marks every field
  // that would refuse, and a mark clears when *that* field is corrected:
  // the flag is per field, so correcting one never un-marks the other
  // while it still refuses (lib/forms.ts).
  let mark = $state(false);
  let typedSince = $state(false); // typed since the core's error arrived

  // A chosen password is typed twice (APP.md §13, the ruling of
  // 2026-09-07): one core prompt, two fields under its id, one
  // submission — the secret crosses the boundary once.
  let confirm = $state("");
  let confirmMark = $state(false);
  const twice = $derived(kind === "password" && choose);
  const confirmProblem = $derived(twice ? confirmSecretProblem(value, confirm) : "");
  const confirmSaid = $derived(confirmMark && confirmProblem ? confirmProblem : "");

  // The merged card's own field, above this one.
  const paired = $derived(!!pin);
  let pinValue = $state("");
  let pinEl: HTMLInputElement | undefined = $state();
  let pinMark = $state(false);
  let pinTypedSince = $state(false);
  const pinProblem = $derived(paired ? secretProblem("pin", pinValue) : "");
  const pinShow = $derived(pinMark && !!pinProblem);
  const pinRefused = $derived(!!pin?.error && !pinTypedSince);
  const pinSaid = $derived(pinShow && pinProblem !== PIN_RULE ? pinProblem : pinRefused ? (pin?.error ?? "") : "");

  const numeric = $derived(kind === "pin" || kind === "recovery");
  const problem = $derived(secretProblem(kind, value, choose));
  const rule = $derived(secretRule(kind, choose));
  const show = $derived(mark && !!problem);
  // The rule itself turns red when it is what is broken; anything else is
  // said beneath it.
  const ruleBroken = $derived(show && problem === rule);
  const refused = $derived(!!error && !typedSince);
  const said = $derived(show && problem !== rule ? problem : refused ? error : "");

  $effect(() => {
    // A new prompt. Which field it belongs to is the form's shape: on the
    // merged card the prompt is the PIN's, and the password beside it
    // keeps exactly what the user typed — a wrong PIN moves nothing else.
    // Every mark goes all the same (lib/forms.ts): a new prompt is a new
    // question, and a mark carried over from the last one would come back
    // on a keystroke, without a leave or a press. The marks are cleared
    // after the focus moves, so the blur that move causes is not taken
    // for the user leaving a field; and a leave still being decided when
    // the prompt arrives (a blur that named no target, such as the
    // focused password removed when the merged card turns into the
    // password alone) is dropped with them.
    void promptId;
    void send;
    untrack(() => {
      if (send && promptId) {
        // The prompt an accepted PIN brought: the field goes as it stands,
        // in whatever state it was left in, with no further click — but
        // judged first, like every typed secret (APP.md §6). A field the
        // form would refuse, emptied in the moment between the press and
        // the prompt, is marked here instead, as the press it answers
        // would have, and waits for a Continue.
        if (!autoSendable(kind, value, choose)) {
          el?.focus();
          unmark();
          mark = nextMark("press", false, problem);
          return;
        }
        submitSecret(kind, promptId, value);
        value = "";
        typedSince = false;
        unmark();
        return;
      }
      if (paired) {
        pinValue = "";
        pinTypedSince = false;
        pinEl?.focus();
        unmark();
        return;
      }
      value = "";
      confirm = "";
      typedSince = false;
      el?.focus();
      unmark();
    });
  });

  // prompts counts the questions this form has been asked, so that a
  // leave begun under one is not counted under the next (lib/forms.ts).
  let prompts = 0;

  function unmark() {
    prompts++;
    mark = nextMark("prompt", mark, problem);
    confirmMark = nextMark("prompt", confirmMark, confirmProblem);
    pinMark = nextMark("prompt", pinMark, pinProblem);
  }

  function typed() {
    typedSince = true;
    mark = nextMark("change", mark, secretProblem(kind, value, choose));
    // The second copy is judged against this one, so it changed too.
    confirmMark = nextMark("change", confirmMark, twice ? confirmSecretProblem(value, confirm) : "");
  }

  function typedPin() {
    pinTypedSince = true;
    pinMark = nextMark("change", pinMark, secretProblem("pin", pinValue));
  }

  function typedConfirm() {
    confirmMark = nextMark("change", confirmMark, confirmSecretProblem(value, confirm));
  }

  // A blur marks only when it is the user leaving for something else in
  // the page; the window losing focus is not (lib/forms.ts).
  const epoch = () => prompts;
  const leftField = onLeave(() => (mark = nextMark("leave", mark, problem)), epoch);
  const leftPin = onLeave(() => (pinMark = nextMark("leave", pinMark, pinProblem)), epoch);
  const leftConfirm = onLeave(() => (confirmMark = nextMark("leave", confirmMark, confirmProblem)), epoch);

  function submit(e: Event) {
    e.preventDefault();
    // Every field that would refuse is marked, and only those: a field
    // that was right at the press carries no mark for a later edit to
    // bring back.
    mark = nextMark("press", mark, problem);
    confirmMark = nextMark("press", confirmMark, confirmProblem);
    pinMark = nextMark("press", pinMark, pinProblem);
    if (paired) {
      // Both fields are judged, so an empty password is marked here and
      // not paid for with a touch; only the PIN goes out.
      if (pinProblem || problem) return;
      submitSecret("pin", promptId, pinValue);
      pinValue = "";
      return;
    }
    if (problem || confirmProblem) return;
    const v = kind === "recovery" ? value.replace(RECOVERY_SEPARATORS, "") : value;
    submitSecret(kind, promptId, v);
    value = "";
    confirm = "";
  }
</script>

<form class="field" id={formId} onsubmit={submit} novalidate>
  {#if pin}
    <SecretField
      id="secret-{promptId}-pin"
      label={pin.label}
      hint={pin.hint ?? ""}
      hintTone={pin.hintTone ?? ""}
      {reserve}
      rule={PIN_RULE}
      ruleBroken={pinShow && pinProblem === PIN_RULE}
      said={pinSaid}
      note={pin.note ?? ""}
      numeric
      invalid={pinShow || pinRefused}
      bind:value={pinValue}
      bind:el={pinEl}
      onblur={leftPin}
      oninput={typedPin}
    />
    <div class="pairfield">
      <SecretField id="secret-{promptId}" {label} {hint} {reserve} said={said} invalid={show || refused} bind:value bind:el onblur={leftField} oninput={typed} />
    </div>
  {:else}
    <SecretField
      id="secret-{promptId}"
      {label}
      {hint}
      {hintTone}
      {reserve}
      {rule}
      {ruleBroken}
      said={said}
      text={kind === "mgmtkey"}
      {numeric}
      invalid={show || refused}
      bind:value
      bind:el
      onblur={leftField}
      oninput={typed}
    />
  {/if}
  {#if twice}
    <div class="pairfield">
      <SecretField id="secret-{promptId}-again" label="Type it again" {reserve} said={confirmSaid} invalid={!!confirmSaid} bind:value={confirm} onblur={leftConfirm} oninput={typedConfirm} />
    </div>
  {/if}
  {#if note}<div class="pin-note">{note}</div>{/if}
  {#if formId}
    <!-- The submit is the dialog's foot's, which names this form. -->
  {:else if cancel}
    {@const c = cancel}
    <div class="submit row" class:snug={reserve}><button type="submit" class="btn accent grow">{button}</button><button type="button" class="btn" onclick={() => c.run()}>{c.label}</button></div>
  {:else}
    <div class="submit"><button type="submit" class="btn accent wide">{button}</button></div>
  {/if}
</form>

<style>
  .submit { margin-top: var(--s3); }
  .submit.row { display: flex; gap: var(--s2); }
  /* The reserved line above is already the space before the buttons. */
  .submit.snug { margin-top: 0; }
  .submit .grow { flex: 1; }
  .pairfield { display: flex; flex-direction: column; gap: var(--s2); margin-top: var(--s1); }
</style>

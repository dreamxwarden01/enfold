<script lang="ts">
  // The compact ceremony panel for enrolment and slot changes (a dialog
  // over the Keystore page); the lock screen has its own three-step strip.
  import { CeremonyStep, Code, Vault } from "../lib/api";
  import type { CeremonyState } from "../lib/api";
  import { fade } from "svelte/transition";
  import { untrack } from "svelte";
  import { store } from "../lib/state.svelte";
  import { codeText, retriesText, stepText } from "../lib/strings";
  import { motion } from "../lib/motion";
  import { NO_TRACK, key as formKey, track } from "../lib/pinform";
  import SecretInput from "./SecretInput.svelte";
  import RecoveryInput from "./RecoveryInput.svelte";

  interface Props {
    c: CeremonyState;
    onclose: () => void;
  }
  let { c, onclose }: Props = $props();

  const over = $derived(c.step === CeremonyStep.StepDone || c.step === CeremonyStep.StepFailed);
  const copy = $derived(stepText(c.step));

  // The same two fields as the lock screen's card 2 (APP.md §6): every
  // token ceremony on the Keys page asks the PIN and the vault's password
  // in one form, in the order the core asks them (APP.md §2.2). A chosen
  // password — an enrolment's, the entangle acts' — is never merged.
  const merged = $derived(!!store.status?.entangled && (c.method || store.unlockMethod) === "token" && !c.choose);
  let tracked = $state(NO_TRACK);
  $effect(() => {
    const next = track(untrack(() => tracked), c, untrack(() => merged));
    if (next !== tracked) tracked = next;
  });
  const form = $derived(tracked.form);
  // The step's own key, except that the merged card and the password
  // alone share one: the password typed beside the PIN must survive the
  // prompt an accepted PIN brings (lib/pinform).
  const k = $derived(form.shows === "none" ? c.step : formKey(form));
  const secretLabel = $derived(
    form.shows === "pin" ? "PIN" : c.choose ? (c.kind === "entangle" ? "Choose the vault's password" : "Choose a password") : "The vault's password",
  );
  const secretNote = $derived(
    form.shows === "pin"
      ? stepText(CeremonyStep.StepPIN).body
      : c.choose
        ? c.kind === "entangle"
          ? "Every YubiKey in this vault will ask for it. Longer is better; a passphrase of several words is best."
          : "The new way in's password. Longer is better; a passphrase of several words is best."
        : "",
  );
  const secretError = $derived(form.shows === "pin" ? (form.pinError ? "Wrong PIN." : "") : form.passwordError ? "Wrong password." : "");

  // The act, not the mechanism (APP.md §6): what the user asked for.
  function kindTitle(kind: string): string {
    switch (kind) {
      case "enroll": return "Add a key";
      case "remove": return "Remove a key";
      case "rotate": return "Rotate the vault key";
      case "export": return "Export a backup";
      case "create": return "Create the vault";
      case "import": return "Import";
      case "setup": return "Finish setting up";
      case "verify": return "Check a backup";
      case "reveal": return "Show the recovery key";
      // One kind for the three entangle acts; which one is being done is
      // the dialog's title, not the ceremony's (APP.md §13).
      case "entangle": return "The vault's password";
      case "records": return "Import records";
    }
    return "Unlock";
  }

  // The note above the eight cells: which sheet the digits come from.
  function recoveryNote(kind: string): string {
    switch (kind) {
      case "verify":
        return "The backup's recovery key. Nothing here changes.";
      case "import":
        return "The incoming file's own recovery key: what makes it yours is that it opens, not what it claims.";
      case "records":
        // Two prompts can appear under this one kind and nothing on the
        // wire tells them apart, so the note names both in order.
        return "This vault's recovery key opens the file when the file is this vault's; if no key of this vault opens it, its own recovery key is asked for next.";
    }
    return "";
  }

  function recoveryRefused(kind: string): string {
    switch (kind) {
      case "verify":
      case "import":
        return "That is not that file's recovery key. Check the digits against its paper.";
      case "records":
        return "That recovery key was refused. Check the digits against the sheet they were printed on.";
    }
    return "That is not this vault's recovery key. Check the digits against the paper.";
  }
</script>

<div class="panel">
  <div class="t-quiet">{kindTitle(c.kind)}</div>
  {#key k}
  <div class="stepbox" in:fade={motion()}>
  {#if c.step === CeremonyStep.StepTouch}
    <div class="touchbox">
      <div class="rings live" aria-hidden="true"><span></span><span></span><span></span><div class="core"><svg viewBox="0 0 20 20"><use href="#i-touchdot" /></svg></div></div>
      <h3 class="touch-lead">{copy.title}</h3>
      <p class="touch-sub">{copy.body}</p>
      {#if c.n > 1}<p class="touch-sub num">Touch {c.n} of this key handle</p>{/if}
    </div>
  {:else if form.shows !== "none"}
    <!-- Since Revision 2 the entangled password is the vault's, never a
         slot's (FORMAT.md §3.1): one switch, one password — and since the
         ruling of 2026-09-08 it stands on the PIN's own card, the PIN
         verified at the key before it is used (APP.md §2.2, §6). -->
    {#if c.slotLabel && form.shows !== "password"}<span class="slotchip"><svg class="i i-14"><use href="#i-yubi" /></svg>{c.slotLabel}</span>{/if}
    <SecretInput
      kind={form.shows === "pin" ? "pin" : "password"}
      promptId={form.promptId}
      send={form.send === "password"}
      pin={form.shows === "both" ? { label: "PIN", hint: retriesText(c), note: stepText(CeremonyStep.StepPIN).body, error: form.pinError ? "Wrong PIN." : "" } : null}
      label={secretLabel}
      hint={form.shows === "pin" ? retriesText(c) : ""}
      note={secretNote}
      choose={form.shows === "password" && c.choose}
      error={secretError}
    />
  {:else if c.step === CeremonyStep.StepRecovery && c.promptId}
    <!-- Whose key is being asked for follows the act (APP.md §13): a check
         of a backup and an import prove the incoming file with the file's
         own key, and a merge asks this vault's way in first and then, only
         when no VMK of this vault opens the file, that file's own — so its
         refusal names no sheet rather than the wrong one. -->
    <RecoveryInput promptId={c.promptId} note={recoveryNote(c.kind)} error={c.error === "vault.auth" ? recoveryRefused(c.kind) : ""} />
  {:else if c.step === CeremonyStep.StepManagementKey && c.promptId}
    <SecretInput kind="mgmtkey" promptId={c.promptId} label="Management key (hex)" note={copy.body} />
  {:else if c.step === CeremonyStep.StepBlocked}
    <div class="bar attention"><svg class="i i-14"><use href="#i-shield" /></svg><div><strong>{copy.title}</strong><br />{copy.body}</div></div>
  {:else if c.step === CeremonyStep.StepFailed}
    <div class="bar danger"><svg class="i i-14"><use href="#i-warn" /></svg><span>{codeText(c.error)}</span></div>
  {:else if c.step === CeremonyStep.StepDone}
    <div class="bar accent"><svg class="i i-14"><use href="#i-check" /></svg><span>{c.kind === "verify" ? `Opens. ${c.archives} archive${c.archives === 1 ? "" : "s"} inside.` : "Done."}</span></div>
  {:else if c.step === CeremonyStep.StepSwapKey}
    <h3 class="u-lead">{copy.title}</h3>
    <p class="u-meta">{c.removeLabel ? `Remove ${c.removeLabel}, then insert ${c.insertLabel || "the key to enroll"}.` : c.insertLabel ? `Insert ${c.insertLabel}.` : copy.body}</p>
  {:else}
    <h3 class="u-lead">{copy.title}</h3>
    <p class="u-meta">{copy.body}</p>
    {#if c.error === Code.CodeTokenPending}
      <!-- The touch a cancelled ceremony left behind is said in place while
           this one waits, as a note and not a failure (APP.md §6). -->
      <div class="bar"><svg class="i i-14"><use href="#i-info" /></svg><span>{codeText(c.error)}</span></div>
    {:else if c.error && c.step !== CeremonyStep.StepDeriving}
      <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span>{codeText(c.error)}</span></div>
    {/if}
  {/if}
  </div>
  {/key}
  <div class="acts">
    {#if over}
      <button type="button" class="btn accent" onclick={onclose}>Close</button>
    {:else}
      <button type="button" class="btn" onclick={() => void Vault.CancelUnlock()}>Cancel</button>
    {/if}
  </div>
</div>

<style>
  .panel, .stepbox { display: flex; flex-direction: column; gap: 12px; }
  .touchbox { display: flex; flex-direction: column; align-items: center; text-align: center; padding: 10px 0; background: linear-gradient(160deg, var(--touch-bg) 0%, var(--touch-bg-2) 100%); color: var(--touch-ink); border-radius: var(--r-card); }
  .touchbox .touch-lead { font-size: 21px; }
  .acts { display: flex; justify-content: flex-end; gap: 8px; }
  .u-meta { margin: 0; }
</style>

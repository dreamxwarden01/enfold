<script lang="ts">
  // The compact ceremony panel for enrolment and the other changes to the
  // unlock methods (a dialog over the Vault page); the lock screen has its
  // own three-step strip.
  import { CeremonyStep, Code, Vault } from "../lib/api";
  import type { CeremonyState } from "../lib/api";
  import { fade } from "svelte/transition";
  import { untrack } from "svelte";
  import { store } from "../lib/state.svelte";
  import { ceremonyCopy, codeText, dialogCopy, retriesText, stepText } from "../lib/strings";
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
  // No match and Blocked name the recovery key on the lock screen, which
  // carries its button; this panel offers only Cancel, so it says what it
  // can do instead of promising a button it does not have (direction 8).
  const copy = $derived(
    c.step === CeremonyStep.StepNoMatch
      ? { title: stepText(c.step).title, body: ceremonyCopy.noMatchWithoutRecovery }
      : c.step === CeremonyStep.StepBlocked
        ? { title: ceremonyCopy.blockedWithoutRecovery, body: stepText(c.step).body }
        : stepText(c.step),
  );

  // The same two fields as the lock screen's card 2 (APP.md §6): every
  // token ceremony on the Vault page asks the PIN and the vault's password
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
    form.shows === "pin" ? ceremonyCopy.pin : c.choose ? (c.kind === "entangle" ? ceremonyCopy.chooseVaultPassword : ceremonyCopy.choosePassword) : ceremonyCopy.vaultPassword,
  );
  const secretNote = $derived(
    form.shows === "pin"
      ? stepText(CeremonyStep.StepPIN).body
      : c.choose
        ? c.kind === "entangle"
          ? ceremonyCopy.chooseVaultPasswordNote
          : ceremonyCopy.chooseMethodPasswordNote
        : "",
  );
  const secretError = $derived(form.shows === "pin" ? (form.pinError ? ceremonyCopy.wrongPin : "") : form.passwordError ? ceremonyCopy.wrongPassword : "");

  // The note above the eight cells: which sheet the digits come from.
  // A merge's note names both prompts in order: two can appear under
  // that one kind and nothing on the wire tells them apart.
  function recoveryNote(kind: string): string {
    return ceremonyCopy.recoveryNote[kind] ?? "";
  }

  function recoveryRefused(kind: string): string {
    switch (kind) {
      case "verify":
      case "import":
        return ceremonyCopy.recoveryRefused.file;
      case "records":
        return ceremonyCopy.recoveryRefused.records;
    }
    return ceremonyCopy.recoveryRefused.vault;
  }
</script>

<!-- No title of its own: the panel always sits in a Dialog, whose title
     already names the act (KeysPage, MergeDialog), and a second line saying
     it again was the title printed twice. -->
<div class="panel">
  {#key k}
  <div class="stepbox" in:fade={motion()}>
  {#if c.step === CeremonyStep.StepTouch}
    <div class="touchbox">
      <div class="rings live" aria-hidden="true"><span></span><span></span><span></span><div class="core"><svg viewBox="0 0 20 20"><use href="#i-touchdot" /></svg></div></div>
      <h3 class="touch-lead">{copy.title}</h3>
      <p class="touch-sub">{copy.body}</p>
      {#if c.n > 1}<p class="touch-sub num">{ceremonyCopy.touchCount(c.n)}</p>{/if}
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
      pin={form.shows === "both" ? { label: ceremonyCopy.pin, hint: retriesText(c), note: stepText(CeremonyStep.StepPIN).body, error: form.pinError ? ceremonyCopy.wrongPin : "" } : null}
      label={secretLabel}
      hint={form.shows === "pin" ? retriesText(c) : ""}
      note={secretNote}
      choose={form.shows === "password" && c.choose}
      error={secretError}
    />
  {:else if c.step === CeremonyStep.StepRecovery && c.promptId}
    <!-- Whose key is being asked for follows the act (APP.md §13): a check
         of a backup and an import prove the incoming file with the file's
         own key, and a merge asks this vault's recovery key first and then, only
         when no VMK of this vault opens the file, that file's own — so its
         refusal names no sheet rather than the wrong one. -->
    <RecoveryInput promptId={c.promptId} note={recoveryNote(c.kind)} error={c.error === "vault.auth" ? recoveryRefused(c.kind) : ""} />
  {:else if c.step === CeremonyStep.StepManagementKey && c.promptId}
    <SecretInput kind="mgmtkey" promptId={c.promptId} label={ceremonyCopy.managementKeyField} note={copy.body} />
  {:else if c.step === CeremonyStep.StepBlocked}
    <div class="bar attention"><svg class="i i-14"><use href="#i-shield" /></svg><div><strong>{copy.title}</strong><br />{copy.body}</div></div>
  {:else if c.step === CeremonyStep.StepFailed}
    <div class="bar danger"><svg class="i i-14"><use href="#i-warn" /></svg><span>{codeText(c.error)}</span></div>
  {:else if c.step === CeremonyStep.StepDone}
    <div class="bar accent"><svg class="i i-14"><use href="#i-check" /></svg><span>{c.kind === "verify" ? ceremonyCopy.verified(c.archives) : ceremonyCopy.done}</span></div>
  {:else if c.step === CeremonyStep.StepSwapKey}
    <h3 class="u-lead">{copy.title}</h3>
    <p class="u-meta">{c.removeLabel ? ceremonyCopy.swapBoth(c.removeLabel, c.insertLabel) : c.insertLabel ? ceremonyCopy.swapInsert(c.insertLabel) : copy.body}</p>
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
      <button type="button" class="btn accent" onclick={onclose}>{dialogCopy.close}</button>
    {:else}
      <button type="button" class="btn" onclick={() => void Vault.CancelUnlock()}>{dialogCopy.cancel}</button>
    {/if}
  </div>
</div>

<style>
  .panel, .stepbox { display: flex; flex-direction: column; gap: var(--s3); }
  /* The lead is the stylesheet's .touch-lead, the hero as on the lock
     screen: the touch is the one hero in the app, a dialog's included
     (APP.md §6, ruled 2026-10-17; it was a 21 px size of its own). */
  .touchbox { display: flex; flex-direction: column; align-items: center; text-align: center; padding: var(--s3) var(--s4); background: linear-gradient(160deg, var(--touch-bg) 0%, var(--touch-bg-2) 100%); color: var(--touch-ink); border-radius: var(--r-card); }
  /* The dialog's own `.dialog p` outranks the stylesheet's .touch-sub and
     painted the line in --ink-2 on the green, where it was not seen; its
     type is the stylesheet's, as on the lock screen. */
  .touchbox .touch-sub { color: var(--touch-soft); }
  .acts { display: flex; justify-content: flex-end; gap: var(--s2); }
  .u-meta { margin: 0; }
</style>

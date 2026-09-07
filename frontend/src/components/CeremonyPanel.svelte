<script lang="ts">
  // The compact ceremony panel for enrolment and slot changes (a dialog
  // over the Keystore page); the lock screen has its own three-step strip.
  import { CeremonyStep, Vault } from "../lib/api";
  import type { CeremonyState } from "../lib/api";
  import { fade } from "svelte/transition";
  import { codeText, retriesText, stepText } from "../lib/strings";
  import { motion } from "../lib/motion";
  import SecretInput from "./SecretInput.svelte";

  interface Props {
    c: CeremonyState;
    onclose: () => void;
  }
  let { c, onclose }: Props = $props();

  const over = $derived(c.step === CeremonyStep.StepDone || c.step === CeremonyStep.StepFailed);
  const copy = $derived(stepText(c.step));

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
    }
    return "Unlock";
  }
</script>

<div class="panel">
  <div class="t-quiet">{kindTitle(c.kind)}</div>
  {#key c.step}
  <div class="stepbox" in:fade={motion()}>
  {#if c.step === CeremonyStep.StepTouch}
    <div class="touchbox">
      <div class="rings live" aria-hidden="true"><span></span><span></span><span></span><div class="core"><svg viewBox="0 0 20 20"><use href="#i-touchdot" /></svg></div></div>
      <h3 class="touch-lead">{copy.title}</h3>
      <p class="touch-sub">{copy.body}</p>
      {#if c.n > 1}<p class="touch-sub num">Touch {c.n} of this key handle</p>{/if}
    </div>
  {:else if c.step === CeremonyStep.StepPIN && c.promptId}
    {#if c.slotLabel}<span class="slotchip"><svg class="i i-14"><use href="#i-yubi" /></svg>{c.slotLabel}</span>{/if}
    <SecretInput kind="pin" promptId={c.promptId} label="PIN" hint={retriesText(c)} note={copy.body} />
  {:else if c.step === CeremonyStep.StepPassword && c.promptId}
    <SecretInput kind="password" promptId={c.promptId} label={c.choose ? "Choose a password" : c.slotLabel ? "Password for this key" : "Vault password"} choose={c.choose} note={c.choose ? "The new way in's password. Longer is better; a passphrase of several words is best." : ""} />
  {:else if c.step === CeremonyStep.StepRecovery && c.promptId}
    <SecretInput kind="recovery" promptId={c.promptId} label="Recovery key" note={c.kind === "verify" ? "The backup's recovery key. Nothing here changes." : ""} />
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
    {#if c.error && c.step !== CeremonyStep.StepDeriving}<div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span>{codeText(c.error)}</span></div>{/if}
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

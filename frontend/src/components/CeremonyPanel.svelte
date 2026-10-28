<script lang="ts">
  // A ceremony inside a dialog (APP.md §6, §13; ruled 2026-10-17): the
  // Vault page's acts — Add an unlock method…, Remove…, Re-key…, Show the
  // recovery key…, Export a backup…, the password with every YubiKey — and
  // the proof of Import archives from a backup…. Its insides are the lock
  // screen's stage at a dialog's size: the same views, chosen by the same
  // rule (lib/lockscreen dialogViewOf, viewOf's dialog form) and turned
  // the same way (lib/stage HeldView); the same key art, slot chip, fields
  // with their reserved lines, and the touch's green — here over the
  // dialog's body, the box's stroke fading and the foot band staying. An
  // enrolment draws a two-part line, since it is two halves and not three
  // steps. The panel keeps one height for every step, so the box never
  // jumps, and its buttons are the dialog's foot band's: Continue at a
  // prompt beside Cancel, Cancel alone while nothing is asked, Start again
  // beside Cancel after a failure the page can begin again, Close at the
  // end (lib/lockscreen dialogFoot, lib/dialogs DialogHost). The steps are
  // the core's (APP.md §2.2); this only draws them.
  //
  // The acts that may re-key the vault (ruled 2026-10-27) add four things.
  // The lead after the proof says what is being done (CeremonyState.phase).
  // A replacement's one confirmation is a view of its own, each new key
  // beside the one it replaces (StepConfirm). An end that carries more than
  // its title — the replacement's receipt, a retirement or a backup that did
  // not land, the next step's button (follow), the line after an enrolment
  // proved with the switch — is drawn on the neutral done view, those items
  // under the title in that order (lib/lockscreen endsWithMore). And an
  // enrolment whose proof waits for a YubiKey of the vault offers the
  // recovery key instead (onrecovery; lib/lockscreen offersRecoverySwitch),
  // the one exception to a dialog's No match holding Cancel alone.
  import { CeremonyStep, Code, Vault, submitSecret } from "../lib/api";
  import type { CeremonyState } from "../lib/api";
  import { fade } from "svelte/transition";
  import { cubicOut } from "svelte/easing";
  import { getContext, untrack } from "svelte";
  import { store } from "../lib/state.svelte";
  import { ceremonyCopy, codeText, dialogCopy, doneTitleOf, lockCopy, phaseLeadOf, replaceCopy, retriesText, stepText } from "../lib/strings";
  import { motion, delay, FAST, GAP } from "../lib/motion";
  import { NO_TRACK, key as formKey, track } from "../lib/pinform";
  import { NO_HALVES, choosesVaultPassword, dialogFoot, dialogViewOf, dialogWay, discs, endsWithMore, green, halvesAfter, insertLed, lastTry, offersRecoverySwitch, pinLeads, provesFirst, retriesTone, typedProofLeads } from "../lib/lockscreen";
  import { afterRecoveryProof, backupBar, failedNotes, pairRows, primaryOf, recoveryNoteOf, replaceBackupItems, retireBar } from "../lib/rekey";
  import { HeldView } from "../lib/stage.svelte";
  import { DIALOG_HOST } from "../lib/dialogs";
  import type { DialogHost } from "../lib/dialogs";
  import SecretInput from "./SecretInput.svelte";
  import RecoveryInput from "./RecoveryInput.svelte";
  import KeyArt from "./KeyArt.svelte";

  interface Props {
    c: CeremonyState;
    onclose: () => void;
    // onagain begins the act again with what it was begun with (the page
    // keeps that; ruled 2026-10-17): a failure the user did not choose —
    // the YubiKey stopped waiting, a refusal — offers Start again in the
    // dialog, as the lock screen's does, rather than only Close and the
    // page's button pressed anew. Left out, a failure has Close alone.
    onagain?: () => void;
    // follow: the end's next step (lib/rekey.ts followUp, read by the page
    // from the act's committed facts and its chain): the sentence, and the
    // command its button opens. The press closes this dialog and opens the
    // next act's.
    follow?: { text: string; label: string; onpress: () => void };
    // onrecovery: Use recovery key instead, which the page passes for an
    // enrolment; drawn only while offersRecoverySwitch holds. The page
    // cancels this ceremony and begins the act again with a recovery key.
    onrecovery?: () => void;
  }
  let { c, onclose, onagain, follow, onrecovery }: Props = $props();

  // The dialog this panel stands in (Dialog.svelte): its foot band and its
  // green are lent to the panel while it is mounted.
  const host = getContext<DialogHost | undefined>(DIALOG_HOST);

  const over = $derived(c.step === CeremonyStep.StepDone || c.step === CeremonyStep.StepFailed);

  // An enrolment is two halves, not three steps: proving it is you, then
  // adding the new unlock method. The core's SwapKey, or the new secret's
  // prompt, is the turn (lib/lockscreen halvesAfter). Settled before the
  // view is (a pre-effect), since the second half draws its own way.
  let halves = $state(NO_HALVES);
  $effect.pre(() => {
    const next = halvesAfter(untrack(() => halves), c);
    if (next !== halves) halves = next;
  });
  const lineShown = $derived(c.kind === "enroll");
  const halfStates = $derived(discs(2, halves.phase));
  const second = $derived(c.kind === "enroll" && halves.phase >= 1);

  // The way it proves it is you, and the view the step asks for.
  const way = $derived(dialogWay(c.method ?? "", !!store.status?.hasHardwareSlot, c));
  // After an enrolment of a YubiKey begun with the switch, the end says the
  // lost YubiKey is the next to remove (lib/rekey.ts afterRecoveryProof).
  const nextLine = $derived(afterRecoveryProof(c, store.lastAct));
  // An end with more to say than its title stands on the neutral done view.
  const more = $derived(endsWithMore(c, { follow: !!follow, nextLine }));
  // The green holds through what follows a touch — the proof's, or the new
  // YubiKey's in an enrolment's second half — and only that (afterTouch).
  const sameCeremony = $derived(store.ceremonyPassed.kind === c.kind);
  const want = $derived(
    dialogViewOf({ c, way, passed: store.ceremonyPassed.passed && sameCeremony, afterTouch: store.ceremonyPassed.afterTouch && sameCeremony, endsWithMore: more }),
  );
  // A derivation in the proof checks it is you, the vault being open
  // already; one in an enrolment's second half makes the new unlock method
  // (a key made on the new YubiKey before its touch). Once the proof has
  // passed, an act that writes the vault says what it is doing, by its
  // phase: the change (and its re-key), then the backup asked for.
  const derivingLead = $derived(phaseLeadOf(c) ?? (second ? lockCopy.derivingLead.adding : ceremonyCopy.checking));

  // The PIN's step, as on the lock screen (APP.md §6): every token
  // ceremony here asks the PIN and the vault's password in one form, in
  // the order the core asks them (APP.md §2.2). A chosen password — an
  // enrolment's, the password with every YubiKey's — is never merged.
  const merged = $derived(!!store.status?.entangled && way === "token" && !c.choose);
  let tracked = $state(NO_TRACK);
  $effect.pre(() => {
    const next = track(untrack(() => tracked), c, untrack(() => merged));
    if (next !== tracked) tracked = next;
  });
  const form = $derived(tracked.form);
  const secretLabel = $derived(
    // The vault's password is the one asked beside a YubiKey; a typed
    // proof on a vault with no YubiKey asks its password, plainly.
    form.shows === "pin"
      ? ceremonyCopy.pin
      : c.choose
        ? choosesVaultPassword(c.kind)
          ? ceremonyCopy.chooseVaultPassword
          : ceremonyCopy.choosePassword
        : way === "token"
          ? ceremonyCopy.vaultPassword
          : ceremonyCopy.password,
  );
  const secretNote = $derived(
    form.shows === "pin" || !c.choose ? "" : choosesVaultPassword(c.kind) ? ceremonyCopy.chooseVaultPasswordNote : ceremonyCopy.chooseMethodPasswordNote,
  );
  const secretError = $derived(form.shows === "pin" ? (form.pinError ? ceremonyCopy.wrongPin : "") : form.passwordError ? ceremonyCopy.wrongPassword : "");

  // The secret view is keyed on what its form shows, not on the step —
  // the password typed beside the PIN must survive the prompt an accepted
  // PIN brings (lib/pinform) — and the key is taken only while the facts
  // ask for that view, so the form emptying as the touch arrives does not
  // cross the leaving view into itself. Taken before the view turns (the
  // HeldView below), so a view that turns to the secret in the same pass
  // is keyed once: keyed with the last key first, the form was drawn
  // twice, and the one that took the focus first marked its field as
  // left (APP.md §6, Forms).
  let secretKey = $state("");
  $effect.pre(() => {
    if (want !== "secret") return;
    secretKey = form.shows === "none" ? c.step : formKey(form);
  });
  const held = new HeldView(() => want, () => c);
  const shown = $derived(held.shown);
  const isGreen = $derived(green(shown));
  const insertStep = $derived(held.insertStep);

  // Every act on the Vault page, and the import's proof, proves it is you
  // with an unlock method the vault already has, so its wait for a
  // YubiKey says which (lib/lockscreen provesFirst). A re-keying removal of
  // a YubiKey does not ask for the one it removes (CeremonyState.without),
  // so the line leaves it out.
  const proves = $derived(provesFirst(c.kind));
  const hardwareLabels = $derived(store.slots.filter((s) => s.type === "hardware" && !(c.without && s.recipientId === c.without)).map((s) => s.label));
  const waitTitle = $derived(
    insertStep !== CeremonyStep.StepWaitingForKey
      ? stepText(insertStep).title
      : second
        ? ceremonyCopy.insertNew
        : proves
          ? ceremonyCopy.insertProve
          : stepText(insertStep).title,
  );
  const waitLine = $derived(
    insertStep !== CeremonyStep.StepWaitingForKey
      ? stepText(insertStep).body
      : second
        ? ceremonyCopy.insertNewLine
        : proves
          ? ceremonyCopy.insertProveLine(hardwareLabels, c.kind === "enroll")
          : stepText(insertStep).body,
  );

  // The views cross in place as the lock screen's do: the old one leaves
  // fast first, the new one arrives a gap later (APP.md §6, Motion).
  const xin = () => motion(FAST, { delay: delay(GAP) });
  const xout = () => motion(FAST, { easing: cubicOut });
  const viewKey = $derived(
    shown === "secret" ? `secret:${secretKey}` : shown === "edge" || shown === "opening" || shown === "wait" || shown === "swap" ? `${shown}:${c.step}` : shown,
  );
  // The form's id, one per view, for the foot's Continue: the leaving view
  // keeps its own while it fades, so the button never names two forms.
  const formId = $derived(`ceremony-${viewKey.replace(/[^a-z0-9_-]/gi, "-")}`);
  const asks = $derived(
    !over &&
      ((shown === "secret" && !!c.promptId && (form.shows !== "none" || c.step === CeremonyStep.StepManagementKey)) ||
        (shown === "recovery" && !!c.promptId) ||
        (shown === "confirm" && !!c.promptId)),
  );
  // The recovery-key switch (ruled 2026-10-27): an enrolment of a YubiKey
  // or a recovery key, begun without it, in its proof, on a vault with a
  // YubiKey and a recovery key — one tested rule (lib/lockscreen).
  const switchOn = $derived(!!onrecovery && offersRecoverySwitch(c, store.lastAct, store.slots, second));
  const buttons = $derived(dialogFoot({ over, view: shown, asks, again: !!onagain, recovery: switchOn, step: c.step }));

  // Use recovery key instead, pressed once per ceremony: the page cancels
  // this one and begins the act again in this same dialog, so the panel
  // starts over with it, as Start again's does.
  let switchFrom = -1;
  function recovery() {
    if (switchFrom === c.seq) return;
    switchFrom = c.seq;
    halves = NO_HALVES;
    tracked = NO_TRACK;
    onrecovery?.();
  }

  // The replacement's confirmation (ruled 2026-10-27, R-B): each new key
  // beside the one it replaces, under the old key's name, which the new one
  // takes; the press answers the confirm prompt, once.
  const keys = $derived(c.keepCount || c.newIds?.length || 0);
  const rows = $derived(pairRows(c, store.slots));
  let confirmedFor = "";
  function confirmIt(e: SubmitEvent) {
    e.preventDefault();
    if (!c.promptId || confirmedFor === c.promptId) return;
    confirmedFor = c.promptId;
    submitSecret("confirm", c.promptId, "");
  }

  // The end's items, in the order they stand under the title (ruled
  // 2026-10-27): a replacement's receipt and what its backup did, a
  // retirement that did not land, a backup that was not saved, the next
  // step, the line after an enrolment proved with the switch.
  const ends = $derived(replaceBackupItems(c));
  const retire = $derived(retireBar(c));
  const backupNotSaved = $derived(backupBar(c));
  // The recovery view's note, by why a recovery key proves it (lib/rekey.ts
  // recoveryNoteOf), never by the kind alone.
  const recoveryKeys = $derived(store.slots.filter((s) => s.type === "recovery").length);

  // Start again: the page begins the act anew in this same dialog, so the
  // box stays where it is and the panel reads the new ceremony from its
  // opening wait — the halves and the form start over with it, as a fresh
  // panel's would (lib/lockscreen halvesAfter keeps a second half
  // otherwise).
  // Pressed once per failure: a second press before the new ceremony's
  // first event would ask the core for a second one.
  let againFrom = -1;
  function again() {
    if (againFrom === c.seq) return;
    againFrom = c.seq;
    halves = NO_HALVES;
    tracked = NO_TRACK;
    onagain?.();
  }

  // What the touch view says, by the step: only the words cross-fade, the
  // rings and the green stay (APP.md §6, Motion: Unlock). In a dialog the
  // vault is open already, so the derivation checks it is you.
  const touchLead = $derived(
    c.step === CeremonyStep.StepDone
      ? (doneTitleOf(c) ?? stepText(c.step).title)
      : c.step === CeremonyStep.StepDeriving
        ? derivingLead
        : stepText(c.step).title,
  );
  const touchSub = $derived(
    c.step === CeremonyStep.StepTouch || c.step === CeremonyStep.StepReleasing
      ? stepText(c.step).body
      : c.step === CeremonyStep.StepDeriving && c.error
        ? codeText(c.error)
        : "",
  );
  const touchReceipt = $derived.by(() => {
    if (c.step === CeremonyStep.StepTouch) {
      return [c.pinAsked ? lockCopy.pinAccepted : "", c.slotLabel, c.n > 1 ? ceremonyCopy.touchCount(c.n) : ""].filter(Boolean).join(" · ");
    }
    return [lockCopy.touched, c.slotLabel].filter(Boolean).join(" · ");
  });

  // Whose recovery key the cells ask for follows the act (APP.md §13): a
  // merge asks this vault's first and then, only when no key of this
  // vault opens the file, that file's own — so its refusal names no sheet
  // rather than the wrong one.
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

  // The panel lends itself to the dialog: the wash under its body, the
  // green while the touch is on, and the foot band's buttons.
  $effect(() => {
    if (!host) return;
    host.ceremony = true;
    host.foot = foot;
    return () => {
      host.ceremony = false;
      host.green = false;
      if (host.foot === foot) host.foot = null;
    };
  });
  $effect(() => {
    if (host) host.green = isGreen;
  });
</script>

<!-- No title of its own: the dialog's names the act (ruled 2026-10-14). -->
<div class="cer-panel">
  {#if lineShown}
    <ol class="steps start" aria-label={ceremonyCopy.halvesLabel}>
      {#each ceremonyCopy.halves as label, i (i)}
        <li class={halfStates[i]} aria-current={halfStates[i] === "live" ? "step" : undefined}>
          <span class="disc">{#if halfStates[i] === "done"}<svg class="i" viewBox="5 5 10 10"><path d="m6.8 10.2 2.2 2.2 4.3-4.6" /></svg>{:else}<span class="num">{i + 1}</span>{/if}</span>{label}
        </li>
      {/each}
    </ol>
  {/if}
  <div class="views">
    {#key viewKey}
      <div class="view" class:center={shown === "touch" || shown === "done"} in:fade={xin()} out:fade={xout()}>
        {#if shown === "insert"}
          <KeyArt led={insertLed(insertStep, held.found, c)} />
          <div class="lead"><h3 class="t-section">{waitTitle}</h3>{#if waitLine}<div class="cap">{waitLine}</div>{/if}</div>
          {#if switchOn && insertStep !== CeremonyStep.StepTwoKeys}
            <!-- No YubiKey of this vault at hand: the lock screen's own words
                 for the same switch, a subtle button under the lead. Two
                 YubiKeys in, the core carries on by itself once one is out. -->
            <button type="button" class="btn subtle sm start" onclick={recovery}>{lockCopy.useRecoveryInstead}</button>
          {/if}
          {#if c.error && c.step === CeremonyStep.StepWaitingForKey}
            <!-- A cancelled touch still pending is a note, not a fault; the
                 Smart Card service not running is said in the attention ink
                 (APP.md §6, ruled 2026-10-17). -->
            <div class="infoline" class:att={c.error !== Code.CodeTokenPending}><svg class="i i-14"><use href={c.error === Code.CodeTokenPending ? "#i-info" : "#i-warn"} /></svg><span>{codeText(c.error)}</span></div>
          {/if}
        {:else if shown === "swap"}
          <!-- The YubiKey that proved it is you comes out first, so the same
               one is not added twice (keys.go enrollToken). -->
          <KeyArt led="found" />
          <div class="lead"><h3 class="t-section">{ceremonyCopy.swapLead(c.removeLabel ?? "")}</h3><div class="cap">{ceremonyCopy.swapLine}</div></div>
        {:else if shown === "edge"}
          <KeyArt led={c.step === CeremonyStep.StepNoMatch ? "off" : "found"} />
          <div class="lead">
            <h3 class="t-section">{stepText(c.step).title}</h3>
            <!-- No match names the button the foot then has where the switch
                 is offered, as the lock screen's line does; elsewhere the
                 line says what can be done instead of naming one. -->
            <div class="cap">{c.step === CeremonyStep.StepNoMatch && !switchOn ? ceremonyCopy.noMatchWithoutRecovery : stepText(c.step).body}</div>
          </div>
        {:else if shown === "secret"}
          <!-- With no step line naming it, the PIN's step heads with its own
               instruction, as the waits and the touch do (APP.md §6). -->
          {#if pinLeads(c.kind, form.shows)}
            <div class="lead"><h3 class="t-section">{lockCopy.pinLead}</h3></div>
          {:else if typedProofLeads({ way, shows: form.shows, choose: c.choose, second })}
            <!-- A typed proof heads as the YubiKey's wait does: what to type,
                 and why (APP.md §13, ruled 2026-10-17). -->
            <div class="lead"><h3 class="t-section">{ceremonyCopy.typeProve}</h3><div class="cap">{ceremonyCopy.typeProveLine(c.kind === "enroll")}</div></div>
          {/if}
          {#if c.slotLabel && c.step !== CeremonyStep.StepManagementKey && form.shows !== "password"}<span class="slotchip"><svg class="i i-14"><use href="#i-yubi" /></svg>{c.slotLabel}</span>{/if}
          {#if c.promptId && form.shows !== "none"}
            {#if (form.shows === "pin" || form.shows === "both") && lastTry(c)}
              <!-- The last try, before it is typed: the consequence. The
                   dialog offers no recovery key, so no way out is named. -->
              <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span class="grow"><strong>{lockCopy.lastTryHead}</strong> {ceremonyCopy.lastTryHere}</span></div>
            {/if}
            <SecretInput
              kind={form.shows === "pin" ? "pin" : "password"}
              promptId={form.promptId}
              send={form.send === "password"}
              pin={form.shows === "both" ? { label: ceremonyCopy.pin, hint: retriesText(c), hintTone: retriesTone(c), error: form.pinError ? ceremonyCopy.wrongPin : "" } : null}
              label={secretLabel}
              hint={form.shows === "pin" ? retriesText(c) : ""}
              hintTone={form.shows === "pin" ? retriesTone(c) : ""}
              note={secretNote}
              choose={form.shows === "password" && c.choose}
              error={secretError}
              reserve
              {formId}
            />
          {:else if c.promptId && c.step === CeremonyStep.StepManagementKey}
            <SecretInput kind="mgmtkey" promptId={c.promptId} label={ceremonyCopy.managementKeyField} note={stepText(c.step).body} reserve {formId} />
          {/if}
        {:else if shown === "recovery"}
          <RecoveryInput
            promptId={c.promptId ?? ""}
            note={recoveryNoteOf(c, store.lastAct, recoveryKeys)}
            error={c.error === "vault.auth" ? recoveryRefused(c.kind) : ""}
            {formId}
          />
        {:else if shown === "touch"}
          <!-- The touch on the dialog's green: the rings, the hero lead, its
               line and the receipt, as on the lock screen; the derivation
               and Done hold the rings still and cross-fade the words. -->
          <div class="rings" class:live={c.step === CeremonyStep.StepTouch} class:still={c.step === CeremonyStep.StepDeriving || c.step === CeremonyStep.StepReleasing} class:done={c.step === CeremonyStep.StepDone} aria-hidden="true">
            <span></span><span></span><span></span>
            {#if c.step === CeremonyStep.StepDone}
              <div class="core pop"><svg viewBox="0 0 20 20"><path d="m5.6 10.3 2.9 2.9 5.9-6.2" /></svg></div>
            {:else}
              <div class="core"><svg viewBox="0 0 20 20"><use href="#i-touchdot" /></svg></div>
            {/if}
          </div>
          <div class="xf">{#key touchLead}<h3 class="touch-lead" in:fade={xin()} out:fade={xout()}>{touchLead}</h3>{/key}</div>
          <div class="xf">{#key touchSub}<div class="touch-sub" in:fade={xin()} out:fade={xout()}>{touchSub}</div>{/key}</div>
          <div class="xf">
            {#key touchReceipt}
              <div class="touch-slot" in:fade={xin()} out:fade={xout()}><svg class="i i-14"><use href="#i-yubi" /></svg>{touchReceipt}</div>
            {/key}
          </div>
        {:else if shown === "opening"}
          <!-- A derivation with no touch behind it is drawn on the neutral
               panel: a typed proof's, or the key made on the new YubiKey
               before its touch. -->
          <div class="lead">
            <h3 class="t-section">{c.step === CeremonyStep.StepDeriving ? derivingLead : stepText(c.step).title}</h3>
            {#if c.step === CeremonyStep.StepDeriving ? !!c.error : !!stepText(c.step).body}<div class="cap">{c.step === CeremonyStep.StepDeriving ? codeText(c.error ?? "") : stepText(c.step).body}</div>{/if}
          </div>
        {:else if shown === "confirm"}
          <!-- The replacement's one confirmation: the pairing and where each
               sheet goes stand to the last check, then what the press does.
               The foot's submit names this form. -->
          <form id={formId} class="confirm" novalidate onsubmit={confirmIt}>
            <div class="lead"><h3 class="t-section">{replaceCopy.ready}</h3></div>
            <dl class="kv">
              {#each rows as r, i (i)}<dt>{r.label}</dt><dd>{r.text}</dd>{/each}
            </dl>
            <p class="t-quiet">{keys === 1 ? replaceCopy.readyLineOne : replaceCopy.readyLine}</p>
          </form>
        {:else if shown === "done"}
          <div class="donedisc pop" aria-hidden="true"><svg viewBox="0 0 20 20"><path d="m5.6 10.3 2.9 2.9 5.9-6.2" /></svg></div>
          <div class="lead"><h3 class="t-section">{doneTitleOf(c) ?? stepText(c.step).title}</h3></div>
          {#if more}
            <!-- What the end carries, under the title at the view's width and
                 read from the left, each only when it applies. -->
            <div class="ends">
              {#if c.kind === "replace" && rows.length}
                <dl class="kv">
                  {#each rows as r, i (i)}<dt>{r.label}</dt><dd>{r.text}</dd>{/each}
                </dl>
              {/if}
              {#each ends as item, i (i)}
                {#if item.tone === "attention"}
                  <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span>{item.text}</span></div>
                {:else}
                  <p>{item.text}</p>
                {/if}
              {/each}
              {#if retire}<div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span>{retire}</span></div>{/if}
              {#if backupNotSaved}<div class="bar attention"><svg class="i i-14"><use href="#i-backup" /></svg><span>{backupNotSaved}</span></div>{/if}
              {#if follow}
                <!-- The cure is a command, so a button (APP.md §6, Controls),
                     beside the sentence that says when it is needed. -->
                <div class="infoline follow">
                  <svg class="i i-14"><use href="#i-info" /></svg>
                  <div class="said"><span>{follow.text}</span><button type="button" class="btn sm" onclick={follow.onpress}>{follow.label}</button></div>
                </div>
              {/if}
              {#if nextLine}<p>{ceremonyCopy.afterRecoveryProof}</p>{/if}
            </div>
          {/if}
        {:else if shown === "failed"}
          {#if c.error === "token.touch"}
            <!-- Where it happened and in neutral ink: the YubiKey stopped
                 waiting, nothing refused anything (ruled 2026-10-17). -->
            <svg class="i i-20 restlock" viewBox="0 0 20 20" aria-hidden="true"><use href="#i-touchdot" /></svg>
            <div class="lead"><h3 class="t-section">{lockCopy.stoppedWaiting}</h3><div class="cap">{lockCopy.stoppedLine}</div></div>
          {:else}
            <div class="lead"><h3 class="t-section">{stepText(c.step).title}</h3></div>
            <div class="bar danger"><svg class="i i-14"><use href="#i-error" /></svg><span>{codeText(c.error ?? "")}</span></div>
            <!-- That nothing was changed, for the acts whose failure before
                 the change leaves the file as it was, and what is known of
                 the keys a replacement showed (lib/rekey.ts failedNotes). -->
            {#each failedNotes(c) as note, i (i)}<p class="t-quiet">{note}</p>{/each}
          {/if}
        {:else if shown === "blocked"}
          <!-- On the PIN's step, where it arises: whose PIN, then what it
               means on a danger bar (APP.md §6, ruled 2026-10-17). -->
          {#if c.slotLabel}<span class="slotchip"><svg class="i i-14"><use href="#i-yubi" /></svg>{c.slotLabel}</span>{/if}
          <div class="lead"><h3 class="t-section">{ceremonyCopy.blockedLead}</h3></div>
          <div class="bar danger"><svg class="i i-14"><use href="#i-error" /></svg><span>{ceremonyCopy.blockedMeans}</span></div>
        {:else if c.step !== CeremonyStep.StepWaitingForKey}
          <!-- A step that asks nothing here (the records found, a recovery
               key being shown). The opening wait of a typed proof draws
               nothing for the instant before its prompt. -->
          <div class="lead">
            <h3 class="t-section">{stepText(c.step).title}</h3>
            {#if stepText(c.step).body}<div class="cap">{stepText(c.step).body}</div>{/if}
          </div>
        {/if}
      </div>
    {/key}
  </div>
</div>

<!-- The foot band's buttons follow the step (APP.md §6, Controls; ruled
     2026-10-17): the act first and the way out after it, as every dialog's.
     Continue submits the view's form by its id, so Enter in a field still
     presses it; Cancel ends the ceremony as Escape does, and once it is
     over closes the dialog, nothing having changed. -->
{#snippet foot()}
  {#each buttons as b (b)}
    {#if b === "continue"}
      <!-- At the replacement's confirmation the submit names what it does. -->
      <button type="submit" class="btn accent" form={formId}>{shown === "confirm" ? primaryOf("replace", true, false, keys) : lockCopy.continue}</button>
    {:else if b === "recovery"}
      <button type="button" class="btn accent" onclick={recovery}>{lockCopy.useRecoveryKey}</button>
    {:else if b === "again"}
      <button type="button" class="btn accent" onclick={again}>{lockCopy.startAgain}</button>
    {:else if b === "close"}
      <button type="button" class="btn accent" onclick={onclose}>{dialogCopy.close}</button>
    {:else}
      <button type="button" class="btn" onclick={() => (over ? onclose() : void Vault.CancelUnlock())}>{dialogCopy.cancel}</button>
    {/if}
  {/each}
{/snippet}

<style>
  /* One height for every step, the tallest's, so the box never jumps as
     the steps swap (APP.md §13, ruled 2026-10-17). The tallest is the
     PIN's last try under an enrolment's line: the line (16 and its 16
     below), the lead, the slot chip, the last try's bar and the PIN and
     the vault's password with their reserved lines, 354 px in Edge
     (2026-10-17), to the 4 px grid 356; the dialog is then 519 px at
     every step. The touch's 240, the recovery key's cells and a chosen
     password's two fields (228) stand inside it. It was 380, the mock's
     guess, which left 26 px of nothing under the tallest. The views
     share one cell, so the old one leaves where the new one arrives. */
  .cer-panel { --ceremony-h: 356px; min-height: var(--ceremony-h); display: flex; flex-direction: column; }
  .views { flex: 1; display: grid; min-width: 0; }
  .view { grid-area: 1 / 1; display: flex; flex-direction: column; gap: var(--s3); min-width: 0; }
  /* The touch, and the end on the neutral panel, stand in the middle, as
     on the lock screen. */
  .view.center { align-items: center; justify-content: center; text-align: center; }
  /* The line reads from the left, under the dialog's title. */
  .steps.start { justify-content: flex-start; margin: 0 0 var(--s4); }
  /* The dialog's rings are the stylesheet's 132 px; the view's gap spaces
     them from the lead. */
  .view .rings { margin-bottom: 0; }
  .view .touch-lead { margin: 0; }
  .view .touch-sub { min-height: 20px; }
  .view .touch-slot { padding-top: 0; }
  /* The switch under the wait's lead stands at the view's start. */
  .view .start { align-self: flex-start; }
  /* The confirmation is the view's own column. */
  .confirm { display: contents; }
  /* An end's items: under the centred check and title, at the view's
     full width and read from the left, 12 apart (APP.md §6). */
  .ends { align-self: stretch; display: flex; flex-direction: column; gap: var(--s3); text-align: left; }
  /* The next step's line: its sentence, and under it the command's button
     at its own width. */
  .follow .said { display: flex; flex-direction: column; align-items: flex-start; gap: var(--s2); min-width: 0; }
</style>

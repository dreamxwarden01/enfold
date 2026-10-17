<script lang="ts">
  // The lock screen (APP.md §6): one panel, the token ceremony's three
  // moments side by side, the other unlock methods below the first. Its
  // words are lockCopy's and the verb table's (strings.ts, ruled
  // 2026-10-17).
  import { CeremonyStep, Keys, Shell, Vault, VaultState, errorOf } from "../lib/api";
  import { store } from "../lib/state.svelte";
  import { ceremonyCopy, codeText, commandCopy, dialogCopy, lockCopy, retriesText, sinceText, stepText, vaultCopy, warningCopy } from "../lib/strings";
  import { leaf } from "../lib/format";
  import { fade } from "svelte/transition";
  import { onMount, untrack } from "svelte";
  import { motion, delay, LEAVE, OUT } from "../lib/motion";
  import { NO_TRACK, key as formKey, track } from "../lib/pinform";
  import { samePath } from "../lib/paths";
  import { firstRunCard } from "../lib/firstrun";
  import { wordedForToken } from "../lib/outcome";
  import SecretInput from "./SecretInput.svelte";
  import RecoveryInput from "./RecoveryInput.svelte";
  import Dialog from "./Dialog.svelte";
  import FirstWayIn from "./FirstWayIn.svelte";
  import ImportDialog from "./ImportDialog.svelte";
  import TextField from "./TextField.svelte";

  const st = $derived(store.status);
  const c = $derived(store.ceremony);
  // running: a ceremony is on the screen — including the settle after the
  // unlock, when the third card shows the check (APP.md §6, Motion).
  const running = $derived(st?.state === VaultState.StateUnlocking || st?.state === VaultState.StateReleasing || (st?.state === VaultState.StateUnlocked && store.settling));
  const firstRun = $derived(st?.state === VaultState.StateNone);
  const busy = $derived(st?.state === VaultState.StateBusy);
  const broken = $derived(st?.state === VaultState.StateBroken);
  // A backup adopted but not set up: the one action is finishing setup.
  const setupNeeded = $derived(!!st?.setupNeeded && st?.state === VaultState.StateLocked);

  // Which of the three cards is live.
  const live = $derived.by((): 0 | 1 | 2 | 3 => {
    if (!c || !running) return 0;
    switch (c.step) {
      case CeremonyStep.StepPIN:
      case CeremonyStep.StepPassword:
      case CeremonyStep.StepRecovery:
      case CeremonyStep.StepManagementKey:
        return 2;
      case CeremonyStep.StepTouch:
      case CeremonyStep.StepDeriving:
      case CeremonyStep.StepReleasing:
      case CeremonyStep.StepDone:
        return 3;
    }
    return 1;
  });

  // tokenOnly is a fact about the ceremony's way in, not about the step:
  // the strip stays worded for the key from the moment the core names the
  // method until the ceremony ends. With the vault's entangled password
  // on, a token unlock asks for a password beside the PIN and is still a
  // token flow (APP.md §13); a check or a setup is never worded for a
  // token.
  const tokenOnly = $derived(wordedForToken(store.unlockMethod, c));
  const outcome = $derived(store.outcome);
  const same = samePath;
  // entangled: this vault asks for its own password beside the key's PIN.
  // Known while Locked (APP.md §2.1) and said before the button is
  // pressed; the standalone password and the recovery key never ask for
  // it, whatever the switch says.
  const entangled = $derived(!!st?.entangled);

  // Card 2's two fields (APP.md §6, the ruling of 2026-09-08): the key's
  // PIN and the vault's password on one card, under one Continue, whenever
  // the switch is on and the way in is the key. What each field shows and
  // which prompt the button answers is lib/pinform's, so that the Keys
  // page's ceremonies say it the same way.
  const merged = $derived(entangled && tokenOnly && !c?.choose);
  let tracked = $state(NO_TRACK);
  $effect(() => {
    const next = track(untrack(() => tracked), c, untrack(() => merged));
    if (next !== tracked) tracked = next;
  });
  const form = $derived(tracked.form);
  const secretLabel = $derived(form.shows === "pin" ? ceremonyCopy.pin : c?.choose ? ceremonyCopy.choosePassword : tokenOnly ? ceremonyCopy.vaultPassword : ceremonyCopy.password);
  const secretNote = $derived(form.shows === "pin" ? stepText(CeremonyStep.StepPIN).body : c?.choose ? ceremonyCopy.choosePasswordNote : "");
  const secretError = $derived(form.shows === "pin" ? (form.pinError ? ceremonyCopy.wrongPin : "") : form.passwordError ? ceremonyCopy.wrongPassword : "");
  // Card 2's name follows its fields' order, the PIN first (APP.md §6;
  // ruled 2026-10-17, it read "Password and PIN" live and "PIN" at rest):
  // *PIN and password* whenever a token way in on this vault asks for both
  // — on the card itself and, dim, before and after it — and *PIN* alone
  // when the vault asks for no password. Keyed on `merged`, not on what
  // the form shows: the password prompt an accepted PIN brings, and the
  // retry after a wrong password, show the password alone on the same
  // card, which keeps its name (APP.md §6, "live, dim and at rest alike").
  const step2Label = $derived(
    form.shows === "both" || (merged && c?.step !== CeremonyStep.StepRecovery && c?.step !== CeremonyStep.StepManagementKey)
      ? ceremonyCopy.pinAndPassword
      : c?.step === CeremonyStep.StepPassword
        ? ceremonyCopy.password
        : c?.step === CeremonyStep.StepRecovery
          ? ceremonyCopy.recoveryKey
          : c?.step === CeremonyStep.StepManagementKey
            ? ceremonyCopy.managementKey
            : ceremonyCopy.pin,
  );
  // Card 2 always reads *Continue* (ruled 2026-10-17): after a PIN a touch
  // still follows, so *Unlock* there was a promise the card did not keep.
  const secretButton = lockCopy.continue;
  // The vault's recovery slots, as the prompt lists them so the right
  // sheet is picked before a digit is typed (APP.md §13, FORMAT.md §18.4).
  // Only where the key asked for is this vault's: an import's and a
  // check's is the incoming file's own.
  const recoverySlots = $derived(
    c && (c.kind === "unlock" || c.kind === "setup")
      ? store.slots.filter((s) => s.type === "recovery").map((s) => ({ label: s.label, id: s.recoveryId ?? "", createdAt: s.createdAt }))
      : [],
  );

  // Each card's body fades in when what it shows changes (APP.md §6): a
  // dim card shows a line that depends on tokenOnly alone, so it is keyed
  // on that, not on the step.
  const k1 = $derived(live === 1 ? `1:${c?.step ?? ""}` : live === 0 ? "0" : "x");
  // Card 2 is keyed on what the card shows and not on the step: the
  // password typed beside the PIN must survive the prompt an accepted PIN
  // brings, and a re-key would take the field with it (lib/pinform).
  const k2 = $derived(live === 2 ? `2:${form.shows === "none" ? (c?.step ?? "") : formKey(form)}` : `x:${tokenOnly}`);
  const k3 = $derived(live === 3 ? `3:${c?.step ?? ""}` : `x:${tokenOnly}`);

  let create = $state(false);
  let createName = $state(lockCopy.defaultName);
  let createPath = $state("");
  let createKind = $state<"token" | "password">("token");
  let createLabel = $state("");
  let createEntangle = $state(false);
  let createConfirm = $state(false);
  let createNameValid = $state(true);
  let createAttempt = $state(0);
  // Where the create lands, and what that replaces: the vault kept here
  // (at its own file, or at the one place), or the file that could not
  // be opened. Anything else already at the place is retired unasked
  // beyond the save dialog's own question.
  // Which first-run card: a vault to make or import, a configured file
  // that is absent, or one there and not a keystore — the rebuild (§2.1).
  const card = $derived(firstRunCard(st));
  const rebuild = $derived(card === "damaged");
  // A rebuild is pinned to the damaged file's place: no other destination.
  const dest = $derived(rebuild ? (st?.missingPath ?? "") : createPath || st?.defaultPath || "");
  const createReplaces = $derived(rebuild && same(dest, st?.missingPath));
  const openArchives = $derived(st?.openArchives ?? 0);

  let importing = $state(false);
  let importPrefill = $state("");
  // restoring: the import was begun at first run, where there is no vault
  // to replace (the verb table: *Restore from a vault or backup…*).
  let restoring = $state(false);
  function openImport(prefill = "") {
    importPrefill = prefill;
    restoring = firstRun;
    importing = true;
  }

  // The vault's name as card 2 says it: "This YubiKey unlocks Personal vault."
  const vaultName = $derived(st?.displayName || leaf(st?.path ?? ""));

  // Only an unlock can turn to the recovery key: an import, a create or a
  // setup proves another file or adds a key, and begin("recovery") would
  // cancel it for an unlock of the vault kept here. So No match and
  // Blocked name the recovery key, and offer it, for an unlock alone.
  const offersRecovery = $derived(c?.kind === "unlock");

  // The method the last unlock began with, so Start again really starts
  // again (direction 8: the label says what the button does).
  let lastMethod = $state<"token" | "password" | "recovery" | null>(null);
  const restartable = $derived(c?.kind === "unlock" && lastMethod !== null);

  // An import at first run restores, elsewhere it replaces: the header,
  // card 1's lead and the outcome say which (the verb table).
  const importHeader = $derived(restoring ? lockCopy.header.restoring : lockCopy.header.replacing);

  let setup = $state(false);
  let setupKind = $state<"token" | "password">("token");
  let setupLabel = $state("");
  let setupEntangle = $state(false);

  async function doSetup() {
    setup = false;
    store.dismissCeremony();
    try {
      // A key's label left empty goes as empty: the core names it after
      // its serial number ("YubiKey 12345678"), which is what the field
      // promises (FirstWayIn) — never a bare "YubiKey".
      await Vault.FinishSetup(setupKind, setupKind === "token" ? setupLabel : "Password", setupKind === "token" && setupEntangle);
    } catch (e) {
      store.toast(codeText(errorOf(e).code), "error");
    }
  }

  async function retryMissing() {
    if (!st?.missingPath) return;
    store.dismissCeremony();
    try {
      await Vault.OpenVaultFile(st.missingPath, ""); // the name it already has
    } catch (e) {
      store.toast(codeText(errorOf(e).code), "error");
    }
  }

  function openCreate() {
    createConfirm = false; // a replacement is confirmed each time
    createAttempt = 0;
    create = true;
  }

  // testBackup proves a backup opens, on a copy, from the lock screen —
  // the one screen a ceremony runs on while the vault is locked.
  async function testBackup() {
    const p = (await Shell.PickFiles(lockCopy.testPicker, false)) ?? [];
    if (!p.length) return;
    store.dismissCeremony();
    try {
      await Keys.VerifyBackup(p[0]);
    } catch (e) {
      store.toast(codeText(errorOf(e).code), "error");
    }
  }

  // What card 3 says when a ceremony ends, by kind.
  function doneTitle(kind: string): string {
    switch (kind) {
      case "import": return restoring ? lockCopy.done.restored : lockCopy.done.replaced;
      case "setup": return lockCopy.done.setup;
      case "verify": return lockCopy.done.verify;
    }
    return lockCopy.done.unlock;
  }

  // begin starts a ceremony. One that is still running (the token flow
  // waiting for a PIN, a parked state) is cancelled first and its end
  // awaited; the panel keeps showing it until the new one's first event.
  async function begin(method: "token" | "password" | "recovery") {
    lastMethod = method;
    try {
      if (running) {
        await Vault.CancelUnlock().catch(() => {});
        await untilLocked();
      }
      await Vault.BeginUnlock(method);
      if (store.ceremonyIsOver) store.dismissCeremony();
    } catch (e) {
      store.toast(codeText(errorOf(e).code), "error");
    }
  }

  function untilLocked(): Promise<void> {
    return new Promise((resolve) => {
      const started = Date.now();
      const tick = () => {
        const s = store.status?.state;
        if (s !== VaultState.StateUnlocking && s !== VaultState.StateReleasing) return resolve();
        if (Date.now() - started > 8000) return resolve();
        setTimeout(tick, 50);
      };
      tick();
    });
  }

  async function pickCreatePath() {
    const p = await Shell.SaveFile(lockCopy.savePicker,"vault.eks", "");
    if (p) createPath = p;
  }

  async function doCreate() {
    createAttempt++;
    if (!createNameValid || (createReplaces && !createConfirm)) return;
    create = false;
    store.dismissCeremony();
    try {
      // A password unlock method is named "Password", whatever was typed for a key
      // before the kind was switched; entangling is a token's option only.
      // Elsewhere, a file already at the chosen place is retired as a
      // dated copy — the save dialog asked about it; here, the tick.
      // A key's label left empty becomes its serial number in the core.
      await Vault.CreateVault(rebuild ? dest : createPath, createName, createKind, createKind === "token" ? createLabel : "Password", createKind === "token" && createEntangle, createReplaces ? createConfirm : !!createPath);
    } catch (e) {
      store.toast(codeText(errorOf(e).code), "error");
    }
  }

  const warnings = $derived(st?.warnings ?? []);

  // The header's padlock closes over the first moment the lock screen
  // shows (APP.md §6, Motion): open on arrival, shut after the settle.
  let closed = $state(false);
  onMount(() => {
    const t = setTimeout(() => (closed = true), delay(OUT));
    return () => clearTimeout(t);
  });
</script>

<section class="unlock" aria-label={lockCopy.regionLabel}>
  <div class="u-head">
    <div class="brand"><svg class="mark i" viewBox="0 0 20 20"><use href="#i-mark" /></svg>Enfold</div>
    <div class="state">
      <span class="lockbox" aria-hidden="true">
        {#key closed}<svg class="i i-14 padlock" in:fade={motion()} out:fade={motion(LEAVE)}><use href={closed ? "#i-lock" : "#i-unlock"} /></svg>{/key}
      </span>
      {#if running}{c?.step === CeremonyStep.StepDone && c?.kind === "unlock" ? lockCopy.header.unlocked : c?.kind === "import" ? importHeader : c?.kind === "create" ? lockCopy.header.creating : c?.kind === "setup" ? lockCopy.header.settingUp : c?.kind === "verify" ? lockCopy.header.testing : lockCopy.header.unlocking}{:else if card === "damaged"}{lockCopy.header.damaged}{:else if card === "absent"}{lockCopy.header.absent}{:else if firstRun}{lockCopy.header.none}{:else if busy}{lockCopy.header.busy}{:else if broken}{lockCopy.header.broken}{:else if setupNeeded}{lockCopy.header.setupNeeded}{:else}{lockCopy.header.locked}{/if}
    </div>
  </div>

  {#if firstRun && !running}
    <div class="u-strip first">
      <article class="step">
        <div class="step-label"><b>1</b>{lockCopy.stepStart}</div>
        <div class="step-body">
          {#if card === "damaged" && st}
            <h2 class="u-lead">{lockCopy.damagedLead}</h2>
            <div class="u-meta">{lockCopy.vaultAt} <span class="mono">{st.missingPath}</span>{lockCopy.damagedAt}</div>
            <div class="u-links">
              <button type="button" class="btn accent" onclick={retryMissing}>{lockCopy.tryAgain}</button>
              <button type="button" class="btn" onclick={() => openImport()}><svg class="i i-14"><use href="#i-open" /></svg>{commandCopy.restoreFrom}</button>
              <button type="button" class="btn subtle sm" onclick={openCreate}>{commandCopy.rebuildVault}</button>
            </div>
            <div class="u-meta q gap">{lockCopy.damagedHint}</div>
          {:else if card === "absent" && st}
            <h2 class="u-lead">{lockCopy.absentLead}</h2>
            <div class="u-meta">{lockCopy.vaultAt} <span class="mono">{st.missingPath}</span>{lockCopy.missingAt}</div>
            <div class="u-links">
              <button type="button" class="btn accent" onclick={retryMissing}>{lockCopy.tryAgain}</button>
              <button type="button" class="btn" onclick={() => openImport()}><svg class="i i-14"><use href="#i-open" /></svg>{commandCopy.restoreFrom}</button>
              <button type="button" class="btn subtle sm" onclick={openCreate}>{lockCopy.createInstead}</button>
            </div>
          {:else}
            <h2 class="u-lead">{lockCopy.firstRunLead}</h2>
            <div class="u-meta">{lockCopy.firstRunLine} {lockCopy.keptIn} <span class="mono">{st?.defaultPath}</span>.</div>
            <div class="u-links">
              <button type="button" class="btn accent" onclick={openCreate}><svg class="i i-14"><use href="#i-plus" /></svg>{commandCopy.createVault}</button>
              <button type="button" class="btn" onclick={() => openImport()}><svg class="i i-14"><use href="#i-open" /></svg>{commandCopy.restoreFrom}</button>
              <button type="button" class="btn subtle sm" onclick={testBackup}>{commandCopy.testBackup}</button>
            </div>
            <div class="u-meta q gap">{lockCopy.restoreHint}</div>
          {/if}
          {#if c && c.step === CeremonyStep.StepFailed && c.error !== "ceremony.cancelled"}
            <div class="bar danger u-bar"><svg class="i i-14"><use href="#i-warn" /></svg><span>{codeText(c.error)}</span></div>
          {:else if outcome?.kind === "verify"}
            <div class="bar accent u-bar"><svg class="i i-14"><use href="#i-check" /></svg><span>{ceremonyCopy.verified(outcome.archives)}</span></div>
          {:else if outcome?.kind === "import"}
            <div class="bar accent u-bar"><svg class="i i-14"><use href="#i-check" /></svg><span>{lockCopy.restoredUnopened}</span></div>
          {:else if outcome?.kind === "create"}
            <div class="bar accent u-bar"><svg class="i i-14"><use href="#i-check" /></svg><span>{lockCopy.createdUnopened}</span></div>
          {/if}
          {#if (st?.retiredCopies ?? 0) > 0 && st?.retiredPath}
            <div class="u-foot">
              <!-- Restoring is a command, so a button, on the 40 px line a bar
                   with a small button takes (APP.md §6, Controls). -->
              <div class="bar line">
                <svg class="i i-14"><use href="#i-info" /></svg>
                <span class="grow">A replaced copy of a vault is in Enfold's folder: {leaf(st.retiredPath)}.</span>
                <button type="button" class="btn sm" onclick={() => openImport(st?.retiredPath ?? "")}>{lockCopy.restoreIt}</button>
              </div>
            </div>
          {/if}
        </div>
      </article>
    </div>
  {:else}
    <div class="u-strip">
      <!-- 1: the key -->
      <article class="step" class:dim={live !== 1 && live !== 0}>
        <div class="step-label"><b>1</b>{tokenOnly ? lockCopy.stepWaiting : lockCopy.stepMethod}</div>
        {#key k1}
        <div class="step-body" in:fade={motion()}>
          {#if live === 1 && c}
            {#if c.step === CeremonyStep.StepFailed}
              <h2 class="u-lead">{stepText(c.step).title}</h2>
              <div class="bar danger"><svg class="i i-14"><use href="#i-warn" /></svg><span>{codeText(c.error)}</span></div>
              <div class="u-links">
                {#if restartable && lastMethod}
                  {@const m = lastMethod}
                  <button type="button" class="btn accent" onclick={() => begin(m)}>{lockCopy.startAgain}</button>
                {:else}
                  <button type="button" class="btn accent" onclick={() => void Vault.CancelUnlock()}>{dialogCopy.close}</button>
                {/if}
              </div>
            {:else if c.step === CeremonyStep.StepNoMatch || c.step === CeremonyStep.StepBusy}
              <h2 class="u-lead">{stepText(c.step).title}</h2>
              <div class="u-meta">{c.step === CeremonyStep.StepNoMatch && !offersRecovery ? ceremonyCopy.noMatchWithoutRecovery : stepText(c.step).body}</div>
              <div class="u-links">
                <!-- No match offers what its line names (ruled 2026-10-17),
                     and names the recovery key for an unlock alone. -->
                {#if c.step === CeremonyStep.StepNoMatch && offersRecovery}<button type="button" class="btn accent" onclick={() => begin("recovery")}>{lockCopy.useRecoveryKey}</button>{/if}
                <button type="button" class="btn" onclick={() => void Vault.CancelUnlock()}>{dialogCopy.cancel}</button>
              </div>
            {:else}
              <div class="keyart" aria-hidden="true">
                <svg width="150" height="72" viewBox="0 0 150 72" fill="none" class="keyart-svg">
                  <rect x="1" y="18" width="52" height="36" rx="4" class="k-port" stroke-width="1.5" />
                  <rect x="9" y="26" width="36" height="20" rx="2" class="k-slot" stroke-width="1.5" />
                  <path d="M53 30h10M53 42h10" class="k-dash" stroke-width="1.5" stroke-dasharray="3 4" />
                  <rect x="70" y="22" width="72" height="28" rx="8" class="k-body" stroke-width="1.5" />
                  <rect x="62" y="29" width="10" height="14" rx="2" class="k-plug" stroke-width="1.5" />
                  <circle cx="126" cy="36" r="7.5" class="k-ring" stroke-width="1.5" />
                  <circle cx="126" cy="36" r="2.6" class="k-dot" />
                  <path d="M80 30v12M88 30v12" class="k-lines" stroke-width="1.5" stroke-linecap="round" />
                </svg>
              </div>
              <h2 class="u-lead">{stepText(c.step).title}</h2>
              <div class="u-meta">{st?.displayName}</div>
              {#if c.step === CeremonyStep.StepTwoKeys}
                <div class="u-foot"><div class="bar"><svg class="i i-14"><use href="#i-info" /></svg><span>{stepText(c.step).body}</span></div></div>
              {:else}
                <div class="u-meta q">{stepText(c.step).body}</div>
              {/if}
              {#if c.error && c.step === CeremonyStep.StepWaitingForKey}
                <div class="bar attention u-bar"><svg class="i i-14"><use href="#i-warn" /></svg><span>{codeText(c.error)}</span></div>
              {/if}
              <div class="u-links">
                <button type="button" class="btn sm" onclick={() => void Vault.CancelUnlock()}>{dialogCopy.cancel}</button>
              </div>
            {/if}
          {:else if live === 0}
            <div class="keyart" aria-hidden="true">
              <svg width="150" height="72" viewBox="0 0 150 72" fill="none" class="keyart-svg">
                <rect x="1" y="18" width="52" height="36" rx="4" class="k-port" stroke-width="1.5" />
                <rect x="9" y="26" width="36" height="20" rx="2" class="k-slot" stroke-width="1.5" />
                <path d="M53 30h10M53 42h10" class="k-dash" stroke-width="1.5" stroke-dasharray="3 4" />
                <rect x="70" y="22" width="72" height="28" rx="8" class="k-body" stroke-width="1.5" />
                <rect x="62" y="29" width="10" height="14" rx="2" class="k-plug" stroke-width="1.5" />
                <circle cx="126" cy="36" r="7.5" class="k-ring" stroke-width="1.5" />
                <circle cx="126" cy="36" r="2.6" class="k-dot" />
                <path d="M80 30v12M88 30v12" class="k-lines" stroke-width="1.5" stroke-linecap="round" />
              </svg>
            </div>
            <h2 class="u-lead">{setupNeeded ? lockCopy.finishTitle : st?.hasHardwareSlot ? stepText(CeremonyStep.StepWaitingForKey).title : lockCopy.unlockTitle}</h2>
            <div class="u-meta">{vaultName}</div>
            {#if setupNeeded}
              <div class="u-meta q">{lockCopy.setupLine}</div>
            {:else if st?.lastUnlockedAt}<div class="u-meta q">{lockCopy.lastUnlocked(sinceText(st.lastUnlockedAt, store.now))}</div>{/if}
            {#if st?.pendingTouch}<div class="u-meta q pendingline">{lockCopy.pendingTouch}</div>{/if}
            <!-- The status's own note: the five minutes from the touch ran
                 out with the vault's password not given, and the ceremony
                 ended where a cancel ends it (APP.md §2.2). The next
                 ceremony clears it, so it needs no dismissal. -->
            {#if st?.note}<div class="u-meta q pendingline">{codeText(st.note)}</div>{/if}
            {#if c && c.step === CeremonyStep.StepFailed && c.error !== "ceremony.cancelled"}
              <div class="bar danger u-bar"><svg class="i i-14"><use href="#i-warn" /></svg><span>{codeText(c.error)}</span></div>
            {:else if outcome?.kind === "import"}
              <div class="bar accent u-bar"><svg class="i i-14"><use href="#i-check" /></svg><span>{restoring ? lockCopy.restored : lockCopy.replaced}</span></div>
            {:else if outcome?.kind === "verify"}
              <div class="bar accent u-bar"><svg class="i i-14"><use href="#i-check" /></svg><span>{ceremonyCopy.verified(outcome.archives)}</span></div>
            {:else if outcome?.kind === "create"}
              <div class="bar accent u-bar"><svg class="i i-14"><use href="#i-check" /></svg><span>{lockCopy.created}</span></div>
            {/if}
            <div class="u-links">
              {#if busy}
                <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span>{codeText("vault.busy")}</span></div>
                <button type="button" class="btn" onclick={() => void Vault.Reopen()}>{lockCopy.tryAgain}</button>
              {:else if broken}
                <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span>{codeText("vault.broken")}</span></div>
                <button type="button" class="btn accent" onclick={() => void Vault.Reopen()}>{lockCopy.reopen}</button>
              {:else if setupNeeded}
                <button type="button" class="btn accent" onclick={() => (setup = true)}><svg class="i i-14"><use href="#i-plus" /></svg>{lockCopy.finishTitle}…</button>
                <button type="button" class="btn sm" onclick={() => begin("recovery")}>{lockCopy.recoveryOnly}</button>
              {:else if !firstRun}
                {#if st?.hasHardwareSlot}
                  <!-- One label whatever the switch (ruled 2026-09-08): the
                       card that follows says which secrets it asks for. -->
                  <button type="button" class="btn accent" onclick={() => begin("token")}><svg class="i i-14"><use href="#i-yubi" /></svg>{lockCopy.unlockWithYubiKey}</button>
                {/if}
                {#if st?.hasPasswordSlot}
                  <button type="button" class="btn" class:accent={!st?.hasHardwareSlot} onclick={() => begin("password")}><svg class="i i-14"><use href="#i-password" /></svg>{lockCopy.unlockWithPassword}</button>
                {/if}
                <button type="button" class="btn sm" onclick={() => begin("recovery")}>{lockCopy.useRecoveryKey}</button>
              {/if}
              {#if !busy && !broken}
                <!-- Commands, so subtle buttons: a hyperlink button is for
                     going somewhere (APP.md §6, Controls; ruled 2026-10-17). -->
                <button type="button" class="btn subtle sm" onclick={() => openImport()}>{commandCopy.replaceVault}</button>
                <button type="button" class="btn subtle sm" onclick={testBackup}>{commandCopy.testBackup}</button>
              {/if}
            </div>
            <div class="u-foot">
              {#each warnings as w (w)}
                <!-- The reason names which cause the status carries; every
                     other code ignores it (APP.md §13, VaultStatus.TamperedReason). -->
                <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span>{warningCopy(w, st?.tamperedReason)}</span></div>
              {/each}
              {#if (st?.openArchives ?? 0) > 0}
                <div class="bar">
                  <svg class="i i-14"><use href="#i-info" /></svg>
                  <span class="grow num">{lockCopy.openWhileLocked(st?.openArchives ?? 0)}</span>
                  <button type="button" class="btn link" onclick={() => store.go(store.current ? "archive" : "archives")}>{vaultCopy.show}</button>
                </div>
              {/if}
            </div>
          {:else}
            <h2 class="u-lead">{st?.displayName || (c?.kind === "verify" ? lockCopy.header.testing : c?.kind === "import" ? importHeader : c?.kind === "create" ? lockCopy.header.creating : "")}</h2>
            {#if c?.slotLabel && c.step !== CeremonyStep.StepRecovery}<div class="u-meta">Matched {c.slotLabel}.</div>{/if}
          {/if}
        </div>
        {/key}
      </article>

      <!-- 2: the secret -->
      <article class="step" class:dim={live !== 2}>
        <div class="step-label"><b>2</b>{step2Label}</div>
        {#key k2}
        <div class="step-body" in:fade={motion()}>
          {#if live === 2 && c && c.promptId}
            {#if form.shows !== "none"}
              {#if c.slotLabel}<span class="slotchip"><svg class="i i-14"><use href="#i-yubi" /></svg>{c.slotLabel}</span>{/if}
              <!-- Only an unlock's recognised YubiKey unlocks the vault named
                   here: an import's matches the incoming file, and a create's
                   or a setup's is being added. -->
              {#if form.shows !== "password" && c.kind === "unlock" && c.slotLabel && vaultName}<div class="u-meta q gap">{lockCopy.matched(vaultName)}</div>{/if}
              <SecretInput
                kind={form.shows === "pin" ? "pin" : "password"}
                promptId={form.promptId}
                send={form.send === "password"}
                pin={form.shows === "both" ? { label: ceremonyCopy.pin, hint: retriesText(c), note: stepText(CeremonyStep.StepPIN).body, error: form.pinError ? ceremonyCopy.wrongPin : "" } : null}
                label={secretLabel}
                hint={form.shows === "pin" ? retriesText(c) : ""}
                note={secretNote}
                choose={form.shows === "password" && c.choose}
                button={secretButton}
                error={secretError}
              />
            {:else if c.step === CeremonyStep.StepRecovery}
              <RecoveryInput promptId={c.promptId} slots={recoverySlots} note={c.kind === "verify" ? ceremonyCopy.recoveryNote.verify : ""} button={c.kind === "verify" ? lockCopy.testButton : lockCopy.continue} error={c.error === "vault.auth" ? (c.kind === "verify" || c.kind === "import" ? ceremonyCopy.recoveryRefused.file : ceremonyCopy.recoveryRefused.vault) : ""} />
            {:else if c.step === CeremonyStep.StepManagementKey}
              <SecretInput kind="mgmtkey" promptId={c.promptId} label={ceremonyCopy.managementKeyField} note={stepText(c.step).body} />
            {/if}
            <div class="u-foot">
              <div class="u-links tight">
                {#if offersRecovery && (form.shows === "pin" || form.shows === "both")}<button type="button" class="btn subtle sm" onclick={() => begin("recovery")}>{lockCopy.useRecoveryInstead}</button>{/if}
                <button type="button" class="btn subtle sm" onclick={() => void Vault.CancelUnlock()}>{dialogCopy.cancel}</button>
              </div>
            </div>
          {:else}
            <div class="u-meta q">{tokenOnly ? (entangled ? lockCopy.tokenPasswordHint : lockCopy.tokenHint) : lockCopy.secretHint}</div>
          {/if}
        </div>
        {/key}
      </article>

      <!-- 3: the touch -->
      <!-- Live, the third card is the touch's green whatever its step —
           the touch, the derivation, the release and Done — so it never
           flashes back to white between them (APP.md §6, Motion). -->
      <article class="step" class:touch={live === 3} class:dim={live !== 3}>
        <div class="step-label"><b>3</b>{c?.step === CeremonyStep.StepTouch ? "Touch" : tokenOnly ? "Touch" : "Unlock"}</div>
        {#key k3}
        <div class="fill" in:fade={motion()}>
        {#if live === 3 && c}
          <div class="touch-body">
            {#if c.step === CeremonyStep.StepTouch}
              <div class="rings live" aria-hidden="true"><span></span><span></span><span></span><div class="core"><svg viewBox="0 0 20 20"><use href="#i-touchdot" /></svg></div></div>
              <h2 class="touch-lead">{stepText(c.step).title}</h2>
              <p class="touch-sub">{stepText(c.step).body}</p>
              <div class="touch-slot"><svg class="i i-14"><use href="#i-yubi" /></svg>{c.pinAsked ? "PIN accepted" : "Touch"}{c.slotLabel ? ` · ${c.slotLabel}` : ""}{c.n > 1 ? ` · touch ${c.n}` : ""}</div>
            {:else if c.step === CeremonyStep.StepDone}
              <!-- The full stop: the same green card, a check in a white disc that pops, the word in the middle (APP.md §6, Motion). -->
              <div class="rings done" aria-hidden="true"><span></span><span></span><span></span><div class="core pop"><svg viewBox="0 0 20 20"><use href="#i-check" /></svg></div></div>
              <h2 class="touch-lead">{doneTitle(c.kind)}</h2>
              <div class="touch-slot"><svg class="i i-14"><use href="#i-unlock" /></svg>{vaultName}</div>
            {:else}
              <div class="rings" aria-hidden="true"><span></span><span></span><span></span><div class="core"></div></div>
              <h2 class="touch-lead">{stepText(c.step).title}</h2>
              <p class="touch-sub">{stepText(c.step).body}</p>
              {#if c.error && c.step === CeremonyStep.StepDeriving}<p class="touch-sub">{codeText(c.error)}</p>{/if}
            {/if}
          </div>
        {:else}
          <div class="step-body"><div class="u-meta q">{tokenOnly ? lockCopy.touchWaits : lockCopy.opening}</div></div>
        {/if}
        </div>
        {/key}
      </article>
    </div>

    {#if c?.step === CeremonyStep.StepBlocked}
      <div class="blocked">
        <span class="step-label bare"><b>4</b>{lockCopy.stepBlocked}</span>
        <div class="tile"><svg class="i"><use href="#i-shield" /></svg></div>
        <div class="txt"><b>{offersRecovery ? stepText(c.step).title : ceremonyCopy.blockedWithoutRecovery}</b><span>{stepText(c.step).body}</span></div>
        {#if offersRecovery}<div class="tag"><button type="button" class="btn" onclick={() => begin("recovery")}>{lockCopy.useRecoveryKey}</button></div>{/if}
      </div>
    {/if}
  {/if}
</section>

{#if create}
  <Dialog title={rebuild ? lockCopy.rebuildTitle : lockCopy.createTitle} onclose={() => (create = false)}>
    <TextField id="cv-name" label={vaultCopy.name} bind:value={createName} bind:valid={createNameValid} attempt={createAttempt} />
    <div class="field">
      <div class="field-top"><label for="cv-path">{lockCopy.keptInLabel}</label>{#if rebuild}{:else if createPath}<button type="button" class="btn subtle xs" onclick={() => (createPath = "")}>{lockCopy.usualPlace}</button>{:else}<button type="button" class="btn subtle xs" onclick={pickCreatePath}>{lockCopy.keepElsewhere}</button>{/if}</div>
      <div id="cv-path" class="path mono">{dest}</div>
    </div>
    <FirstWayIn bind:kind={createKind} bind:label={createLabel} bind:entangle={createEntangle} idPrefix="cv" />
    {#if openArchives > 0}
      <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span>{lockCopy.stillOpen(openArchives)}</span></div>
    {:else if rebuild && st}
      <div class="bar attention">
        <svg class="i i-14"><use href="#i-warn" /></svg>
        <span>{lockCopy.rebuildFileAt} <span class="mono">{st.missingPath}</span> {lockCopy.rebuildKept}</span>
      </div>
      <label class="check"><input type="checkbox" bind:checked={createConfirm} />{lockCopy.rebuildConsent}</label>
    {:else if createPath}
      <p>{lockCopy.replaceKept}</p>
    {/if}
    {#snippet actions()}
      <button type="button" class="btn accent" disabled={(createReplaces && !createConfirm) || openArchives > 0} onclick={doCreate}>{rebuild ? lockCopy.rebuildPrimary : lockCopy.createPrimary}</button>
      <button type="button" class="btn" onclick={() => (create = false)}>{dialogCopy.cancel}</button>
    {/snippet}
  </Dialog>
{/if}

{#if setup}
  <Dialog title={lockCopy.finishTitle} onclose={() => (setup = false)}>
    <p>{lockCopy.finishBody}</p>
    <FirstWayIn bind:kind={setupKind} bind:label={setupLabel} bind:entangle={setupEntangle} idPrefix="su" />
    {#snippet actions()}
      <button type="button" class="btn accent" onclick={doSetup}>{lockCopy.continue}</button>
      <button type="button" class="btn" onclick={() => (setup = false)}>{dialogCopy.cancel}</button>
    {/snippet}
  </Dialog>
{/if}

{#if importing}
  <ImportDialog prefill={importPrefill} onclose={() => (importing = false)} />
{/if}

<style>
  .u-strip.first { grid-template-columns: minmax(320px, 520px); justify-content: center; }
  /* A path wraps and is never cut (APP.md §6, a fact): the face, size and
     ink are .mono's. */
  .path { overflow-wrap: anywhere; user-select: text; }
  .fill { flex: 1; min-height: 0; display: flex; flex-direction: column; }
  .gap { margin-top: var(--s3); margin-bottom: var(--s3); }
  .u-links.tight { margin-top: 0; }
  .u-bar { margin-top: var(--s3); }
  .step-label.bare { height: auto; border: none; background: none; padding: 0; }
  .k-port { fill: var(--surface-alt); stroke: var(--stroke-strong); }
  .k-slot { fill: var(--ground); stroke: var(--stroke-strong); }
  .k-dash { stroke: var(--stroke-strong); }
  .k-body { fill: var(--surface); stroke: var(--ink-3); }
  .k-plug { fill: var(--surface-alt); stroke: var(--ink-3); }
  .k-ring { fill: var(--accent-wash); stroke: var(--accent); }
  .k-dot { fill: var(--accent); }
  .k-lines { stroke: var(--ink-3); }
  .pendingline { margin-top: var(--s2); max-width: 34em; }
  .lockbox { position: relative; width: 14px; height: 14px; display: inline-block; }
  .lockbox .padlock { position: absolute; inset: 0; }
</style>

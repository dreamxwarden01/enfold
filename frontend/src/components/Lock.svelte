<script lang="ts">
  // The lock screen (APP.md §6): one panel, the token ceremony's three
  // moments side by side, the secondary ways in below the first.
  import { CeremonyStep, Keys, Shell, Vault, VaultState, errorOf } from "../lib/api";
  import { store } from "../lib/state.svelte";
  import { codeText, retriesText, stepText, warningCopy } from "../lib/strings";
  import { dateTime, leaf } from "../lib/format";
  import { fade } from "svelte/transition";
  import { motion } from "../lib/motion";
  import { samePath } from "../lib/paths";
  import { firstRunCard } from "../lib/firstrun";
  import SecretInput from "./SecretInput.svelte";
  import RecoveryInput from "./RecoveryInput.svelte";
  import Dialog from "./Dialog.svelte";
  import FirstWayIn from "./FirstWayIn.svelte";
  import ImportDialog from "./ImportDialog.svelte";
  import TextField from "./TextField.svelte";

  const st = $derived(store.status);
  const c = $derived(store.ceremony);
  const running = $derived(st?.state === VaultState.StateUnlocking || st?.state === VaultState.StateReleasing);
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

  // tokenOnly is a fact about the ceremony, not the step: once a secret
  // was asked for, the strip stays worded for a secret until the ceremony
  // ends (the store tracks it from the events), and a check or a setup is
  // never worded for a token.
  const tokenOnly = $derived(!c || (!store.secretAsked && c.kind !== "verify" && c.kind !== "setup" && c.step !== CeremonyStep.StepPassword && c.step !== CeremonyStep.StepRecovery));
  const outcome = $derived(store.outcome);
  const same = samePath;

  // Each card's body fades in when what it shows changes (APP.md §6): a
  // dim card shows a line that depends on tokenOnly alone, so it is keyed
  // on that, not on the step.
  const k1 = $derived(live === 1 ? `1:${c?.step ?? ""}` : live === 0 ? "0" : "x");
  const k2 = $derived(live === 2 ? `2:${c?.step ?? ""}` : `x:${tokenOnly}`);
  const k3 = $derived(live === 3 ? `3:${c?.step ?? ""}` : `x:${tokenOnly}`);

  let create = $state(false);
  let createName = $state("Personal vault");
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
  function openImport(prefill = "") {
    importPrefill = prefill;
    importing = true;
  }

  let setup = $state(false);
  let setupKind = $state<"token" | "password">("token");
  let setupLabel = $state("");
  let setupEntangle = $state(false);

  async function doSetup() {
    setup = false;
    store.dismissCeremony();
    try {
      await Vault.FinishSetup(setupKind, setupKind === "token" ? setupLabel || "YubiKey" : "Password", setupKind === "token" && setupEntangle);
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

  // checkBackup proves a backup opens, on a copy, from the lock screen —
  // the one screen a ceremony runs on while the vault is locked.
  async function checkBackup() {
    const p = (await Shell.PickFiles("Check that a backup opens", false)) ?? [];
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
      case "import": return "Imported";
      case "setup": return "Set up";
      case "verify": return "Opens";
    }
    return "Unlocked";
  }

  // begin starts a ceremony. One that is still running (the token flow
  // waiting for a PIN, a parked state) is cancelled first and its end
  // awaited; the panel keeps showing it until the new one's first event.
  async function begin(method: "token" | "password" | "recovery") {
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
    const p = await Shell.SaveFile("Where to keep the vault", "vault.eks");
    if (p) createPath = p;
  }

  async function doCreate() {
    createAttempt++;
    if (!createNameValid || (createReplaces && !createConfirm)) return;
    create = false;
    store.dismissCeremony();
    try {
      // A password slot is named "Password", whatever was typed for a key
      // before the kind was switched; entangling is a token's option only.
      // Elsewhere, a file already at the chosen place is retired as a
      // dated copy — the save dialog asked about it; here, the tick.
      // A key's label left empty becomes its serial number in the core.
      await Vault.CreateVault(rebuild ? dest : createPath, createName, createKind, createKind === "token" ? createLabel : "Password", createKind === "token" && createEntangle, createReplaces ? createConfirm : !!createPath);
    } catch (e) {
      store.toast(codeText(errorOf(e).code), "error");
    }
  }

  const warnings = $derived((st?.warnings ?? []).filter((w) => w !== "vault.stale" || true));
</script>

<section class="unlock" aria-label="Enfold — keystore locked">
  <div class="u-head">
    <div class="brand"><svg class="mark i" viewBox="0 0 20 20"><use href="#i-mark" /></svg>Enfold</div>
    <div class="state">
      <svg class="i i-14"><use href="#i-lock" /></svg>
      {#if running}{c?.kind === "import" ? "Importing" : c?.kind === "setup" ? "Setting up" : c?.kind === "verify" ? "Checking a backup" : "Unlocking"}{:else if card === "damaged"}Vault damaged{:else if card === "absent"}Vault not found{:else if firstRun}No vault yet{:else if busy}Vault open elsewhere{:else if broken}Needs attention{:else if setupNeeded}Needs setting up{:else}Keystore locked{/if}
    </div>
  </div>

  {#if firstRun && !running}
    <div class="u-strip first">
      <article class="step">
        <div class="step-label"><b>1</b>Start</div>
        <div class="step-body">
          {#if card === "damaged" && st}
            <h2 class="u-lead">The vault could not be opened</h2>
            <div class="u-meta">Enfold keeps your vault at <span class="mono">{st.missingPath}</span>, and that file is no longer a readable vault.</div>
            <div class="u-links">
              <button type="button" class="btn accent" onclick={retryMissing}>Try again</button>
              <button type="button" class="btn" onclick={() => openImport()}><svg class="i i-14"><use href="#i-open" /></svg>Import a vault or a backup…</button>
              <button type="button" class="btn link" onclick={openCreate}>Rebuild the vault…</button>
            </div>
            <div class="u-meta q gap">A backup or a copy of this vault brings the archives' keys back. Rebuilding starts a new vault and keeps the damaged file beside it.</div>
          {:else if card === "absent" && st}
            <h2 class="u-lead">The vault could not be found</h2>
            <div class="u-meta">Enfold keeps your vault at <span class="mono">{st.missingPath}</span>, and that file is missing.</div>
            <div class="u-links">
              <button type="button" class="btn accent" onclick={retryMissing}>Try again</button>
              <button type="button" class="btn" onclick={() => openImport()}><svg class="i i-14"><use href="#i-open" /></svg>Import a vault or a backup…</button>
              <button type="button" class="btn link" onclick={openCreate}>Create a new vault instead</button>
            </div>
          {:else}
            <h2 class="u-lead">Create a vault, or import one</h2>
            <div class="u-meta">A vault is one file: the keys to your archives, opened by a YubiKey, a password or a recovery key. Enfold keeps it in <span class="mono">{st?.defaultPath}</span>.</div>
            <div class="u-links">
              <button type="button" class="btn accent" onclick={openCreate}><svg class="i i-14"><use href="#i-plus" /></svg>Create a vault</button>
              <button type="button" class="btn" onclick={() => openImport()}><svg class="i i-14"><use href="#i-open" /></svg>Import a vault or a backup…</button>
              <button type="button" class="btn link" onclick={checkBackup}>Check that a backup opens…</button>
            </div>
            <div class="u-meta q gap">To import a backup, have its recovery key ready: a backup opens with nothing else.</div>
          {/if}
          {#if c && c.step === CeremonyStep.StepFailed && c.error !== "ceremony.cancelled"}
            <div class="bar danger u-bar"><svg class="i i-14"><use href="#i-warn" /></svg><span>{codeText(c.error)}</span></div>
          {:else if outcome?.kind === "verify"}
            <div class="bar accent u-bar"><svg class="i i-14"><use href="#i-check" /></svg><span>The backup opens. {outcome.archives} archive{outcome.archives === 1 ? "" : "s"} inside.</span></div>
          {:else if outcome?.kind === "import"}
            <div class="bar accent u-bar"><svg class="i i-14"><use href="#i-check" /></svg><span>Imported, but the file could not be opened afterwards. Try again.</span></div>
          {:else if outcome?.kind === "create"}
            <div class="bar accent u-bar"><svg class="i i-14"><use href="#i-check" /></svg><span>Created, but the file could not be opened afterwards. Try again.</span></div>
          {/if}
          {#if (st?.retiredCopies ?? 0) > 0 && st?.retiredPath}
            <div class="u-foot">
              <div class="bar">
                <svg class="i i-14"><use href="#i-info" /></svg>
                <span class="grow">A replaced copy of a vault is in Enfold's folder: {leaf(st.retiredPath)}.</span>
                <button type="button" class="btn link" onclick={() => openImport(st?.retiredPath ?? "")}>Import it…</button>
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
        <div class="step-label"><b>1</b>{tokenOnly ? "Waiting for the key" : "The way in"}</div>
        {#key k1}
        <div class="step-body" in:fade={motion()}>
          {#if live === 1 && c}
            {#if c.step === CeremonyStep.StepFailed}
              <h2 class="u-lead">{stepText(c.step).title}</h2>
              <div class="bar danger"><svg class="i i-14"><use href="#i-warn" /></svg><span>{codeText(c.error)}</span></div>
              <div class="u-links">
                <button type="button" class="btn accent" onclick={() => void Vault.CancelUnlock()}>Start again</button>
              </div>
            {:else if c.step === CeremonyStep.StepNoMatch || c.step === CeremonyStep.StepBusy}
              <h2 class="u-lead">{stepText(c.step).title}</h2>
              <div class="u-meta">{stepText(c.step).body}</div>
              <div class="u-links">
                <button type="button" class="btn" onclick={() => void Vault.CancelUnlock()}>Cancel</button>
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
                <button type="button" class="btn sm" onclick={() => void Vault.CancelUnlock()}>Cancel</button>
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
            <h2 class="u-lead">{setupNeeded ? "Finish setting up" : st?.hasHardwareSlot ? "Insert your YubiKey" : "Unlock the vault"}</h2>
            <div class="u-meta">{st?.displayName || leaf(st?.path ?? "")}</div>
            {#if setupNeeded}
              <div class="u-meta q">This vault has only its recovery key so far. Choose the first way in; the recovery key is asked for first.</div>
            {:else if st?.lastUnlockedAt}<div class="u-meta q">Last unlocked {dateTime(st.lastUnlockedAt)}</div>{/if}
            {#if st?.pendingTouch}<div class="u-meta q pending">The key is still waiting for the touch you cancelled. Unlock again to pick it up, or touch it or pull it out to end it.</div>{/if}
            {#if c && c.step === CeremonyStep.StepFailed && c.error !== "ceremony.cancelled"}
              <div class="bar danger u-bar"><svg class="i i-14"><use href="#i-warn" /></svg><span>{codeText(c.error)}</span></div>
            {:else if outcome?.kind === "import"}
              <div class="bar accent u-bar"><svg class="i i-14"><use href="#i-check" /></svg><span>Imported. Unlock it with its own way in.</span></div>
            {:else if outcome?.kind === "verify"}
              <div class="bar accent u-bar"><svg class="i i-14"><use href="#i-check" /></svg><span>The backup opens. {outcome.archives} archive{outcome.archives === 1 ? "" : "s"} inside.</span></div>
            {:else if outcome?.kind === "create"}
              <div class="bar accent u-bar"><svg class="i i-14"><use href="#i-check" /></svg><span>Created, but it did not open on its own. Unlock it with the way in you chose.</span></div>
            {/if}
            <div class="u-links">
              {#if busy}
                <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span>{codeText("vault.busy")}</span></div>
                <button type="button" class="btn" onclick={() => void Vault.Reopen()}>Try again</button>
              {:else if broken}
                <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span>{codeText("vault.broken")}</span></div>
                <button type="button" class="btn accent" onclick={() => void Vault.Reopen()}>Reopen the vault</button>
              {:else if setupNeeded}
                <button type="button" class="btn accent" onclick={() => (setup = true)}><svg class="i i-14"><use href="#i-plus" /></svg>Finish setting up</button>
                <button type="button" class="btn sm" onclick={() => begin("recovery")}>Unlock with the recovery key only</button>
              {:else if !firstRun}
                {#if st?.hasHardwareSlot}
                  <button type="button" class="btn accent" onclick={() => begin("token")}><svg class="i i-14"><use href="#i-yubi" /></svg>Unlock with YubiKey</button>
                {/if}
                {#if st?.hasPasswordSlot}
                  <button type="button" class="btn" class:accent={!st?.hasHardwareSlot} onclick={() => begin("password")}><svg class="i i-14"><use href="#i-password" /></svg>Unlock with password</button>
                {/if}
                <button type="button" class="btn sm" onclick={() => begin("recovery")}>Use recovery key</button>
              {/if}
              {#if !busy && !broken}
                <button type="button" class="btn link" onclick={() => openImport()}>Import a backup or a copy of this vault…</button>
                <button type="button" class="btn link" onclick={checkBackup}>Check that a backup opens…</button>
              {/if}
            </div>
            <div class="u-foot">
              {#each warnings as w (w)}
                <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span>{warningCopy(w)}</span></div>
              {/each}
              {#if (st?.openArchives ?? 0) > 0}
                <div class="bar">
                  <svg class="i i-14"><use href="#i-info" /></svg>
                  <span class="grow num">{st?.openArchives} archive(s) still open — browsing works, saving needs the vault.</span>
                  <button type="button" class="btn link" onclick={() => store.go(store.current ? "archive" : "archives")}>Show</button>
                </div>
              {/if}
            </div>
          {:else}
            <h2 class="u-lead">{st?.displayName || (c?.kind === "verify" ? "Checking a backup" : c?.kind === "import" ? "Importing" : c?.kind === "create" ? "Creating the vault" : "")}</h2>
            {#if c?.slotLabel && c.step !== CeremonyStep.StepRecovery}<div class="u-meta">Matched {c.slotLabel}.</div>{/if}
          {/if}
        </div>
        {/key}
      </article>

      <!-- 2: the secret -->
      <article class="step" class:dim={live !== 2}>
        <div class="step-label"><b>2</b>{c?.step === CeremonyStep.StepPassword ? "Password" : c?.step === CeremonyStep.StepRecovery ? "Recovery key" : c?.step === CeremonyStep.StepManagementKey ? "Management key" : "PIN"}</div>
        {#key k2}
        <div class="step-body" in:fade={motion()}>
          {#if live === 2 && c && c.promptId}
            {#if c.step === CeremonyStep.StepPIN}
              {#if c.slotLabel}<span class="slotchip"><svg class="i i-14"><use href="#i-yubi" /></svg>{c.slotLabel}</span>{/if}
              <div class="u-meta q gap">Matched a slot in this keystore.</div>
              <SecretInput kind="pin" promptId={c.promptId} label="PIN" hint={retriesText(c)} note={stepText(c.step).body} button="Unlock" error={c.error === "token.pin" ? "Wrong PIN." : ""} />
            {:else if c.step === CeremonyStep.StepPassword}
              {#if c.slotLabel}<span class="slotchip"><svg class="i i-14"><use href="#i-yubi" /></svg>{c.slotLabel}</span>{/if}
              <SecretInput kind="password" promptId={c.promptId} label={c.choose ? "Choose a password" : "Password"} choose={c.choose} note={c.choose ? "Longer is better; a passphrase of several words is best." : ""} button={c.choose || c.kind !== "unlock" ? "Continue" : "Unlock"} error={c.error === "vault.auth" ? "Wrong password." : ""} />
            {:else if c.step === CeremonyStep.StepRecovery}
              <RecoveryInput promptId={c.promptId} note={c.kind === "verify" ? "The backup's recovery key. Nothing here changes." : ""} button={c.kind === "verify" ? "Check" : c.kind === "unlock" ? "Unlock" : "Continue"} error={c.error === "vault.auth" ? "That is not this vault's recovery key. Check the digits against the paper." : ""} />
            {:else if c.step === CeremonyStep.StepManagementKey}
              <SecretInput kind="mgmtkey" promptId={c.promptId} label="Management key (hex)" note={stepText(c.step).body} />
            {/if}
            <div class="u-foot">
              <div class="u-links tight">
                {#if c.step === CeremonyStep.StepPIN}<button type="button" class="btn link" onclick={() => begin("recovery")}>Use recovery key instead</button>{/if}
                <button type="button" class="btn link" onclick={() => void Vault.CancelUnlock()}>Cancel</button>
              </div>
            </div>
          {:else}
            <div class="u-meta q">{tokenOnly ? "The key's PIN, once it is matched." : "The secret this way in needs."}</div>
          {/if}
        </div>
        {/key}
      </article>

      <!-- 3: the touch -->
      <article class="step" class:touch={live === 3 && c?.step === CeremonyStep.StepTouch} class:dim={live !== 3}>
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
            {:else}
              <div class="rings" aria-hidden="true"><span></span><span></span><span></span><div class="core"><svg viewBox="0 0 20 20"><use href="#i-check" /></svg></div></div>
              <h2 class="touch-lead quiet">{c.step === CeremonyStep.StepDone ? doneTitle(c.kind) : stepText(c.step).title}</h2>
              <p class="touch-sub quiet">{stepText(c.step).body}</p>
              {#if c.error && c.step === CeremonyStep.StepDeriving}<p class="touch-sub">{codeText(c.error)}</p>{/if}
            {/if}
          </div>
        {:else}
          <div class="step-body"><div class="u-meta q">{tokenOnly ? "The key waits for a touch." : "The session keys are derived."}</div></div>
        {/if}
        </div>
        {/key}
      </article>
    </div>

    {#if c?.step === CeremonyStep.StepBlocked}
      <div class="blocked">
        <span class="step-label bare"><b>4</b>Blocked</span>
        <div class="tile"><svg class="i"><use href="#i-shield" /></svg></div>
        <div class="txt"><b>{stepText(c.step).title}</b><span>{stepText(c.step).body}</span></div>
        <div class="tag"><button type="button" class="btn" onclick={() => begin("recovery")}>Use recovery key</button></div>
      </div>
    {/if}
  {/if}
</section>

{#if create}
  <Dialog title={rebuild ? "Rebuild the vault" : "Create a vault"} onclose={() => (create = false)}>
    <TextField id="cv-name" label="Name" bind:value={createName} bind:valid={createNameValid} attempt={createAttempt} />
    <div class="field">
      <div class="field-top"><label for="cv-path">Kept in</label>{#if rebuild}{:else if createPath}<button type="button" class="btn link" onclick={() => (createPath = "")}>Use the usual place</button>{:else}<button type="button" class="btn link" onclick={pickCreatePath}>Keep it elsewhere…</button>{/if}</div>
      <div id="cv-path" class="path ellipsis" title={dest}>{dest}</div>
    </div>
    <FirstWayIn bind:kind={createKind} bind:label={createLabel} bind:entangle={createEntangle} idPrefix="cv" />
    {#if openArchives > 0}
      <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span>{openArchives} archive{openArchives === 1 ? " is" : "s are"} still open. Close them first: their saves would land in the wrong vault.</span></div>
    {:else if rebuild && st}
      <div class="bar attention">
        <svg class="i i-14"><use href="#i-warn" /></svg>
        <span>The file at <span class="mono">{st.missingPath}</span> is not a readable vault. It is kept beside the new vault as a dated copy in Enfold's folder, never deleted — the archives it held the keys to open only with it, if it can ever be repaired.</span>
      </div>
      <label class="check"><input type="checkbox" bind:checked={createConfirm} />Keep a copy of the damaged file and start a new vault</label>
    {:else if createPath}
      <p>A file already at the chosen place is kept as a dated copy in Enfold's folder.</p>
    {/if}
    {#snippet actions()}
      <button type="button" class="btn" onclick={() => (create = false)}>Cancel</button>
      <button type="button" class="btn accent" disabled={(createReplaces && !createConfirm) || openArchives > 0} onclick={doCreate}>{rebuild ? "Rebuild" : "Create"}</button>
    {/snippet}
  </Dialog>
{/if}

{#if setup}
  <Dialog title="Finish setting up" onclose={() => (setup = false)}>
    <p>The vault opens with its recovery key first; then the way in you choose here is added.</p>
    <FirstWayIn bind:kind={setupKind} bind:label={setupLabel} bind:entangle={setupEntangle} idPrefix="su" />
    {#snippet actions()}
      <button type="button" class="btn" onclick={() => (setup = false)}>Cancel</button>
      <button type="button" class="btn accent" onclick={doSetup}>Continue</button>
    {/snippet}
  </Dialog>
{/if}

{#if importing}
  <ImportDialog prefill={importPrefill} onclose={() => (importing = false)} />
{/if}

<style>
  .u-strip.first { grid-template-columns: minmax(320px, 520px); justify-content: center; }
  .path { font-size: 12.5px; color: var(--ink-2); font-family: var(--font-mono); }
  .fill { flex: 1; min-height: 0; display: flex; flex-direction: column; }
  .gap { margin-top: 10px; margin-bottom: 14px; }
  .u-links.tight { margin-top: 0; }
  .u-bar { margin-top: 12px; }
  .touch-lead.quiet, .touch-sub.quiet { color: var(--ink); }
  .touch-sub.quiet { color: var(--ink-2); }
  .step-label.bare { height: auto; border: none; background: none; padding: 0; }
  .k-port { fill: var(--surface-alt); stroke: var(--stroke-strong); }
  .k-slot { fill: var(--ground); stroke: var(--stroke-strong); }
  .k-dash { stroke: var(--stroke-strong); }
  .k-body { fill: var(--surface); stroke: var(--ink-3); }
  .k-plug { fill: var(--surface-alt); stroke: var(--ink-3); }
  .k-ring { fill: var(--accent-wash); stroke: var(--accent); }
  .k-dot { fill: var(--accent); }
  .k-lines { stroke: var(--ink-3); }
</style>

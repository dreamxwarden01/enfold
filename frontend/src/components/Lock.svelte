<script lang="ts">
  // The lock screen (APP.md §6): one panel, the token ceremony's three
  // moments side by side, the secondary ways in below the first.
  import { CeremonyStep, Shell, Vault, VaultState, errorOf } from "../lib/api";
  import { store } from "../lib/state.svelte";
  import { codeText, retriesText, stepText, warningCopy } from "../lib/strings";
  import { dateTime, leaf } from "../lib/format";
  import SecretInput from "./SecretInput.svelte";
  import RecoveryReveal from "./RecoveryReveal.svelte";
  import Dialog from "./Dialog.svelte";

  const st = $derived(store.status);
  const c = $derived(store.ceremony);
  const running = $derived(st?.state === VaultState.StateUnlocking || st?.state === VaultState.StateReleasing);
  const firstRun = $derived(st?.state === VaultState.StateNone);
  const busy = $derived(st?.state === VaultState.StateBusy);
  const broken = $derived(st?.state === VaultState.StateBroken);

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

  const tokenOnly = $derived(!c || c.kind !== "unlock" || (c.step !== CeremonyStep.StepPassword && c.step !== CeremonyStep.StepRecovery));

  let create = $state(false);
  let createName = $state("Personal vault");
  let createPath = $state("");
  let createKind = $state<"token" | "password">("token");
  let createLabel = $state("");
  let createEntangle = $state(false);

  async function begin(method: "token" | "password" | "recovery") {
    store.dismissCeremony();
    try {
      await Vault.BeginUnlock(method);
    } catch (e) {
      store.toast(codeText(errorOf(e).code), "error");
    }
  }

  async function openFile() {
    const p = (await Shell.PickFiles("Open a vault or a backup", false)) ?? [];
    if (!p.length) return;
    try {
      await Vault.OpenVaultFile(p[0], leaf(p[0]).replace(/\.eks$/i, ""));
    } catch (e) {
      store.toast(codeText(errorOf(e).code), "error");
    }
  }

  async function pickCreatePath() {
    const p = await Shell.SaveFile("Where to keep the vault", "vault.eks");
    if (p) createPath = p;
  }

  async function doCreate() {
    if (!createPath || !createName) return;
    create = false;
    store.dismissCeremony();
    try {
      await Vault.CreateVault(createPath, createName, createKind, createLabel || (createKind === "token" ? "YubiKey" : "Password"), createEntangle);
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
      {#if running}Unlocking{:else if firstRun}No vault yet{:else if busy}Vault open elsewhere{:else if broken}Needs attention{:else}Keystore locked{/if}
    </div>
  </div>

  {#if firstRun}
    <div class="u-strip first">
      <article class="step">
        <div class="step-label"><b>1</b>Start</div>
        <div class="step-body">
          <h2 class="u-lead">Create a vault, or open one</h2>
          <div class="u-meta">A vault is one file: the keys to your archives, opened by a YubiKey, a password or a recovery key.</div>
          <div class="u-links">
            <button type="button" class="btn accent" onclick={() => (create = true)}><svg class="i i-14"><use href="#i-plus" /></svg>Create a vault</button>
            <button type="button" class="btn" onclick={openFile}><svg class="i i-14"><use href="#i-open" /></svg>Open a vault file</button>
          </div>
        </div>
      </article>
    </div>
  {:else}
    <div class="u-strip">
      <!-- 1: the key -->
      <article class="step" class:dim={live !== 1 && live !== 0}>
        <div class="step-label"><b>1</b>{tokenOnly ? "Waiting for the key" : "The way in"}</div>
        <div class="step-body">
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
            <h2 class="u-lead">{st?.hasHardwareSlot ? "Insert your YubiKey" : "Unlock the vault"}</h2>
            <div class="u-meta">{st?.displayName || leaf(st?.path ?? "")}</div>
            {#if st?.lastUnlockedAt}<div class="u-meta q">Last unlocked {dateTime(st.lastUnlockedAt)}</div>{/if}
            {#if c && c.step === CeremonyStep.StepFailed && c.error !== "ceremony.cancelled"}
              <div class="bar danger u-bar"><svg class="i i-14"><use href="#i-warn" /></svg><span>{codeText(c.error)}</span></div>
            {/if}
            <div class="u-links">
              {#if busy}
                <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span>{codeText("vault.busy")}</span></div>
                <button type="button" class="btn" onclick={() => void Vault.Reopen()}>Try again</button>
              {:else if broken}
                <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span>{codeText("vault.broken")}</span></div>
                <button type="button" class="btn accent" onclick={() => void Vault.Reopen()}>Reopen the vault</button>
              {:else}
                {#if st?.hasHardwareSlot}
                  <button type="button" class="btn accent" onclick={() => begin("token")}><svg class="i i-14"><use href="#i-yubi" /></svg>Unlock with YubiKey</button>
                {/if}
                {#if st?.hasPasswordSlot}
                  <button type="button" class="btn" class:accent={!st?.hasHardwareSlot} onclick={() => begin("password")}><svg class="i i-14"><use href="#i-password" /></svg>Unlock with password</button>
                {/if}
                <button type="button" class="btn sm" onclick={() => begin("recovery")}>Use recovery key</button>
              {/if}
              <button type="button" class="btn link" onclick={openFile}>Open a backup or another vault</button>
              <button type="button" class="btn link" onclick={() => (create = true)}>Create a new vault</button>
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
            <h2 class="u-lead">{st?.displayName}</h2>
            {#if c?.slotLabel && c.step !== CeremonyStep.StepRecovery}<div class="u-meta">Matched {c.slotLabel}.</div>{/if}
          {/if}
        </div>
      </article>

      <!-- 2: the secret -->
      <article class="step" class:dim={live !== 2}>
        <div class="step-label"><b>2</b>{c?.step === CeremonyStep.StepPassword ? "Password" : c?.step === CeremonyStep.StepRecovery ? "Recovery key" : c?.step === CeremonyStep.StepManagementKey ? "Management key" : "PIN"}</div>
        <div class="step-body">
          {#if live === 2 && c && c.promptId}
            {#if c.step === CeremonyStep.StepPIN}
              {#if c.slotLabel}<span class="slotchip"><svg class="i i-14"><use href="#i-yubi" /></svg>{c.slotLabel}</span>{/if}
              <div class="u-meta q gap">Matched a slot in this keystore.</div>
              <SecretInput kind="pin" promptId={c.promptId} label="PIN" hint={retriesText(c)} note={stepText(c.step).body} button="Unlock" />
            {:else if c.step === CeremonyStep.StepPassword}
              {#if c.slotLabel}<span class="slotchip"><svg class="i i-14"><use href="#i-yubi" /></svg>{c.slotLabel}</span>{/if}
              <SecretInput kind="password" promptId={c.promptId} label={c.kind === "create" ? "Choose a password" : "Password"} note={c.kind === "create" ? "This password opens the vault. Choose a long one." : ""} button={c.kind === "create" ? "Create" : "Unlock"} />
            {:else if c.step === CeremonyStep.StepRecovery}
              <SecretInput kind="recovery" promptId={c.promptId} label="Recovery key" note="The digits you wrote down, with or without spaces." button="Unlock" />
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
      </article>

      <!-- 3: the touch -->
      <article class="step" class:touch={live === 3 && c?.step === CeremonyStep.StepTouch} class:dim={live !== 3}>
        <div class="step-label"><b>3</b>{c?.step === CeremonyStep.StepTouch ? "Touch" : tokenOnly ? "Touch" : "Unlock"}</div>
        {#if live === 3 && c}
          <div class="touch-body">
            {#if c.step === CeremonyStep.StepTouch}
              <div class="rings live" aria-hidden="true"><span></span><span></span><span></span><div class="core"><svg viewBox="0 0 20 20"><use href="#i-touchdot" /></svg></div></div>
              <h2 class="touch-lead">{stepText(c.step).title}</h2>
              <p class="touch-sub">{stepText(c.step).body}</p>
              <div class="touch-slot"><svg class="i i-14"><use href="#i-yubi" /></svg>{c.pinAsked ? "PIN accepted" : "Touch"}{c.slotLabel ? ` · ${c.slotLabel}` : ""}{c.n > 1 ? ` · touch ${c.n}` : ""}</div>
            {:else}
              <div class="rings" aria-hidden="true"><span></span><span></span><span></span><div class="core"><svg viewBox="0 0 20 20"><use href="#i-check" /></svg></div></div>
              <h2 class="touch-lead quiet">{c.step === CeremonyStep.StepDone ? "Unlocked" : stepText(c.step).title}</h2>
              <p class="touch-sub quiet">{stepText(c.step).body}</p>
            {/if}
          </div>
        {:else}
          <div class="step-body"><div class="u-meta q">{tokenOnly ? "The key waits for a touch." : "The session keys are derived."}</div></div>
        {/if}
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

{#if c && c.step === CeremonyStep.StepRecovery && !c.promptId && c.slotLabel && store.ceremonyIsOver}
  <RecoveryReveal url={c.slotLabel} ondone={() => store.dismissCeremony()} />
{/if}

{#if create}
  <Dialog title="Create a vault" onclose={() => (create = false)}>
    <div class="field">
      <div class="field-top"><label for="cv-name">Name</label></div>
      <input id="cv-name" class="input" bind:value={createName} />
    </div>
    <div class="field">
      <div class="field-top"><label for="cv-path">File</label></div>
      <div class="row">
        <input id="cv-path" class="input grow" readonly value={createPath} placeholder="Choose where the vault file lives" />
        <button type="button" class="btn" onclick={pickCreatePath}>Choose…</button>
      </div>
    </div>
    <div class="field">
      <div class="field-top"><label for="cv-kind">First way in</label><span class="hint">A recovery key is always added too.</span></div>
      <select id="cv-kind" class="input" bind:value={createKind}>
        <option value="token">YubiKey (PIN + touch)</option>
        <option value="password">Password</option>
      </select>
    </div>
    <div class="field">
      <div class="field-top"><label for="cv-label">Label</label></div>
      <input id="cv-label" class="input" bind:value={createLabel} placeholder={createKind === "token" ? "YubiKey 5C — desk" : "Password"} />
    </div>
    {#if createKind === "token"}
      <label class="check"><input type="checkbox" bind:checked={createEntangle} />Also require a password with this key</label>
    {/if}
    {#snippet actions()}
      <button type="button" class="btn" onclick={() => (create = false)}>Cancel</button>
      <button type="button" class="btn accent" disabled={!createPath || !createName} onclick={doCreate}>Create</button>
    {/snippet}
  </Dialog>
{/if}

<style>
  .u-strip.first { grid-template-columns: minmax(320px, 520px); justify-content: center; }
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

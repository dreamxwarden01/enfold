<script lang="ts">
  // The lock screen (APP.md §6, ruled 2026-10-17): one centred panel that
  // changes in place, a step line above it while an unlock runs, and the
  // touch's green over the whole stage — the ruling of 2026-09-06 ("one
  // panel changing in place … the touch moment is a full-surface
  // takeover"), built at last in place of the three cards. Where each of
  // the core's steps is drawn is lib/lockscreen's; its words are
  // lockCopy's and the verb table's (strings.ts).
  import { CeremonyStep, Keys, Shell, Vault, VaultState, errorOf } from "../lib/api";
  import { store } from "../lib/state.svelte";
  import { ceremonyCopy, codeText, commandCopy, dialogCopy, firstMethodCopy, lockCopy, retriesText, sinceText, stepText, vaultCopy, warningCopy } from "../lib/strings";
  import { date, leaf } from "../lib/format";
  import { fade } from "svelte/transition";
  import { cubicOut } from "svelte/easing";
  import { prefersReducedMotion } from "svelte/motion";
  import { onMount, untrack } from "svelte";
  import { motion, delay, FAST, GAP, LEAVE, MOVE, OUT } from "../lib/motion";
  import { NO_TRACK, key as formKey, track } from "../lib/pinform";
  import { middlePath, samePath } from "../lib/paths";
  import { firstRunCard, retiredAt } from "../lib/firstrun";
  import { wordedForToken } from "../lib/outcome";
  import { NO_PHASE, derivingOf, discs, green, insertLed, lastTry, lineKind, padlockOpen as padlockOpenOf, phaseAfter, pinLeads, retriesTone, viewOf, wayOf } from "../lib/lockscreen";
  import { HeldView } from "../lib/stage.svelte";
  import type { View, Way } from "../lib/lockscreen";
  import SecretInput from "./SecretInput.svelte";
  import RecoveryInput from "./RecoveryInput.svelte";
  import Dialog from "./Dialog.svelte";
  import FirstWayIn from "./FirstWayIn.svelte";
  import ImportDialog from "./ImportDialog.svelte";
  import TextField from "./TextField.svelte";
  import MenuButton from "./MenuButton.svelte";
  import KeyArt from "./KeyArt.svelte";

  const st = $derived(store.status);
  const c = $derived(store.ceremony);
  // running: a ceremony is on the screen — including the settle after the
  // unlock, when the panel shows the check (APP.md §6, Motion).
  const running = $derived(st?.state === VaultState.StateUnlocking || st?.state === VaultState.StateReleasing || (st?.state === VaultState.StateUnlocked && store.settling));
  const firstRun = $derived(st?.state === VaultState.StateNone);
  const busy = $derived(st?.state === VaultState.StateBusy);
  const broken = $derived(st?.state === VaultState.StateBroken);
  // A backup adopted but not set up: the one action is finishing setup.
  const setupNeeded = $derived(!!st?.setupNeeded && st?.state === VaultState.StateLocked);
  // A vault whose everyday way in is a password: no YubiKey among its
  // unlock methods (keystore.ErrPolicy forbids the two side by side).
  const passwordOnly = $derived(!!st?.hasPasswordSlot && !st?.hasHardwareSlot);

  // tokenOnly is a fact about the ceremony's way in, not about the step:
  // the panel stays worded for the YubiKey from the moment the core names
  // the method until the ceremony ends. With the vault's entangled
  // password on, a token unlock asks for a password beside the PIN and is
  // still a token flow (APP.md §13); a check or a setup is never worded
  // for a token.
  const tokenOnly = $derived(wordedForToken(store.unlockMethod, c));
  const outcome = $derived(store.outcome);
  const same = samePath;
  // entangled: this vault asks for its own password beside the YubiKey's
  // PIN. Known while Locked (APP.md §2.1); the standalone password and the
  // recovery key never ask for it, whatever the switch says.
  const entangled = $derived(!!st?.entangled);

  // The PIN's step (APP.md §6, the ruling of 2026-09-08): the YubiKey's
  // PIN and the vault's password in one form, under one Continue, whenever
  // the switch is on and the way in is the YubiKey. What each field shows
  // and which prompt the button answers is lib/pinform's, so that the
  // Vault page's ceremonies say it the same way.
  const merged = $derived(entangled && tokenOnly && !c?.choose);
  let tracked = $state(NO_TRACK);
  // Before the template, as the view's own facts are (below): the form a
  // view draws is settled in the same pass as the view itself.
  $effect.pre(() => {
    const next = track(untrack(() => tracked), c, untrack(() => merged));
    if (next !== tracked) tracked = next;
  });
  const form = $derived(tracked.form);
  const secretLabel = $derived(form.shows === "pin" ? ceremonyCopy.pin : c?.choose ? ceremonyCopy.choosePassword : tokenOnly ? ceremonyCopy.vaultPassword : ceremonyCopy.password);
  const secretNote = $derived(c?.choose && form.shows === "password" ? ceremonyCopy.choosePasswordNote : "");
  const secretError = $derived(form.shows === "pin" ? (form.pinError ? ceremonyCopy.wrongPin : "") : form.passwordError ? ceremonyCopy.wrongPassword : "");
  // The PIN's step is named in its fields' order, the PIN first (APP.md
  // §6; ruled 2026-10-17): *PIN and password* on a vault that asks for
  // both, *PIN* alone on one that asks for no password.
  const pinStep = $derived(entangled ? ceremonyCopy.pinAndPassword : ceremonyCopy.pin);
  // The vault's recovery slots, as the prompt lists them so the right
  // sheet is picked before a digit is typed (APP.md §13, FORMAT.md §18.4).
  // Only where the key asked for is this vault's: an import's and a
  // check's is the incoming file's own.
  const recoverySlots = $derived(
    c && (c.kind === "unlock" || c.kind === "setup")
      ? store.slots.filter((s) => s.type === "recovery").map((s) => ({ label: s.label, id: s.recoveryId ?? "", createdAt: s.createdAt }))
      : [],
  );

  let create = $state(false);
  let createName = $state(lockCopy.defaultName);
  let createPath = $state("");
  // The first unlock method's cards (FirstWayIn): what they hold, the
  // password among it, is theirs until Continue takes it.
  let createFirst = $state<ReturnType<typeof FirstWayIn>>();
  let createConfirm = $state(false);
  let createNameValid = $state(true);
  let createAttempt = $state(0);
  // Which first-run panel: a vault to make or import, a configured file
  // that is absent, or one there and not a keystore — the rebuild (§2.1).
  const card = $derived(firstRunCard(st));
  const rebuild = $derived(card === "damaged");
  // A rebuild is pinned to the damaged file's place: no other destination.
  const dest = $derived(rebuild ? (st?.missingPath ?? "") : createPath || st?.defaultPath || "");
  const createReplaces = $derived(rebuild && same(dest, st?.missingPath));
  // The place on the "Kept in" line, cut in the middle to what fits its
  // one row beside the button (APP.md §6, ruled 2026-10-17): 412 px of
  // body, less "Kept in" (37), the wider button ("Use the usual place",
  // 102) and two 8 px gaps, leaves 257 px — 39 characters of the
  // caption's mono at 6.5 px (measured in Edge, 2026-10-17); 34 leaves
  // room for a wider fallback face. A rebuild has no button: 367 px, and
  // 54. The full path is the line's title.
  const destShown = $derived(middlePath(dest, rebuild ? 54 : 34));
  const openArchives = $derived(st?.openArchives ?? 0);

  let importing = $state(false);
  let importPrefill = $state("");
  // Find it… opens the file where it is, the import's advanced choice
  // already taken (APP.md §6, ruled 2026-10-17).
  let importPrefer = $state<"copy" | "inplace">("copy");
  // restoring: the import was begun at first run, where there is no vault
  // to replace (the verb table: *Restore from a vault or backup…*).
  let restoring = $state(false);
  function openImport(prefill = "", prefer: "copy" | "inplace" = "copy") {
    importPrefill = prefill;
    importPrefer = prefer;
    restoring = firstRun;
    importing = true;
  }

  // The vault's name, as the panel says it — at first run, the one that
  // is gone or damaged, by its name or its file's.
  const vaultName = $derived(st?.displayName || leaf(st?.path ?? ""));
  const missingName = $derived(vaultName || leaf(st?.missingPath ?? "") || lockCopy.defaultName);
  // The damaged vault's best remedy: the vault a replacement kept, dated
  // by its file's name (APP.md §6, ruled 2026-10-17).
  const keptCopy = $derived(rebuild && (st?.retiredCopies ?? 0) > 0 && !!st?.retiredPath);
  const keptDate = $derived(retiredAt(st?.retiredPath ?? "") ? date(retiredAt(st?.retiredPath ?? "")) : "");

  // Only an unlock can turn to the recovery key: an import, a create or a
  // setup proves another file or adds a key, and begin("recovery") would
  // cancel it for an unlock of the vault kept here. So No match and
  // Blocked name the recovery key, and offer it, for an unlock alone.
  const offersRecovery = $derived(c?.kind === "unlock");

  // The method the last unlock began with, so Start again really starts
  // again (direction 8: the label says what the button does).
  let lastMethod = $state<"token" | "password" | "recovery" | null>(null);
  // The way in, as far as the page knows it (lib/lockscreen wayOf).
  const way = $derived<Way>(wayOf(store.unlockMethod, lastMethod ?? "", c));
  const againWay = $derived(lastMethod ?? (way || null));
  const restartable = $derived(c?.kind === "unlock" && againWay !== null);

  // An import at first run restores, elsewhere it replaces: the header,
  // the panel's lead and the outcome say which (the verb table).
  const importHeader = $derived(restoring ? lockCopy.header.restoring : lockCopy.header.replacing);
  // The lead of a ceremony that is not an unlock: the header's word for
  // the act, since no vault is being opened.
  const actLead = $derived(c?.kind === "verify" ? lockCopy.header.testing : c?.kind === "import" ? importHeader : c?.kind === "create" ? lockCopy.header.creating : c?.kind === "setup" ? lockCopy.header.settingUp : vaultName);

  // ---- The stage (APP.md §6, ruled 2026-10-17) ---------------------------
  // The view the facts ask for, and the one on the panel: the panel holds
  // its view while the core passes through a step that draws nothing new,
  // and the key art keeps its found LED for FOUND_HOLD before the PIN's
  // step crosses in (APP.md §6, Motion: Unlock).
  // A wait for a YubiKey after the ceremony's opening step is a real one,
  // drawn whatever way the core named; the store follows that on every
  // event (lib/lockscreen passedAfter).
  // The touch's green holds through what follows it in the same ceremony
  // (afterTouch), whatever the ceremony's kind or the way the core named.
  const sameCeremony = $derived(store.ceremonyPassed.kind === c?.kind);
  const want = $derived<View>(
    viewOf({ running, firstRun, way, c, passed: store.ceremonyPassed.passed && sameCeremony, afterTouch: store.ceremonyPassed.afterTouch && sameCeremony }),
  );
  // What a derivation says it does, by the act (lib/lockscreen derivingOf):
  // a create's never opens a vault, a setup's after the vault opened adds
  // its first way in.
  const derivingLead = $derived.by(() => {
    const d = derivingOf(c?.kind ?? "unlock", store.ceremonyPassed.opened && sameCeremony);
    return d === "opening" ? stepText(CeremonyStep.StepDeriving).body : lockCopy.derivingLead[d];
  });
  // The view on the panel, the found LED and the insert view's own step
  // follow `want` as the dialogs' ceremony panel's do (lib/stage): turned
  // in a pre-effect, so the leaving view leaves as it was (APP.md §6,
  // Motion: the old view leaves fast first).
  // The secret view is keyed on what its form shows, not on the step: the
  // password typed beside the PIN must survive the prompt an accepted PIN
  // brings (lib/pinform). The key is taken only while the facts ask for
  // that view, so the form emptying as the touch arrives does not cross
  // the old view into itself on its way out; and before the view turns,
  // so a view turning to the secret in the same pass — the password asked
  // again after the touch — is keyed once, not drawn with the last key and
  // then again (APP.md §6, Forms: the first form took the focus and marked
  // its field as left).
  let secretKey = $state("");
  $effect.pre(() => {
    if (want !== "secret") return;
    secretKey = store.chosenSent && store.chosenSent === c?.promptId ? "sent" : form.shows === "none" ? (c?.step ?? "") : formKey(form);
  });
  const held = new HeldView(() => want, () => c);
  const shown = $derived(held.shown);
  const found = $derived(held.found);
  const insertStep = $derived(held.insertStep);
  const isGreen = $derived(green(shown));

  // The step line: an unlock's alone, shown while it runs and while its
  // failure is on the panel; at rest it is absent and its line is kept.
  const line = $derived(lineKind(c?.kind, way));
  const steps = $derived(line === "token" ? [lockCopy.stepKey, pinStep, lockCopy.stepTouch] : line === "recovery" ? [ceremonyCopy.recoveryKey, lockCopy.stepUnlock] : []);
  const lineShown = $derived(steps.length > 0 && shown !== "rest" && shown !== "firstrun");
  let phased = $state(NO_PHASE);
  $effect(() => {
    const next = phaseAfter(untrack(() => phased), line, c);
    if (next !== phased) phased = next;
  });
  const states = $derived(discs(steps.length, phased.phase, phased.touched));
  // The vault's password asked again after the touch: nothing follows it
  // but the opening, so its button is *Unlock*, and the touch keeps its
  // check (APP.md §6, ruled 2026-10-17).
  const afterTouch = $derived(line === "token" && phased.touched);

  // Each view crosses in place: the old one leaves fast first, the new one
  // arrives a gap later (APP.md §6, Motion).
  const xin = () => motion(FAST, { delay: delay(GAP) });
  const xout = () => motion(FAST, { easing: cubicOut });
  const viewKey = $derived(shown === "secret" ? `secret:${secretKey}` : shown === "edge" || shown === "opening" || shown === "wait" ? `${shown}:${c?.step ?? ""}` : shown);

  // The panel's height follows its view (APP.md §6, Motion: Unlock; ruled
  // 2026-10-17). The two views of a crossing share one cell, so left to
  // itself the panel would jump to the taller the instant the new one is
  // laid out, under the old one's fading words. As a view arrives the
  // height eases from the old view's to the new one's over the scene
  // change (220 ms, the entering curve), clipped at the panel's edge
  // meanwhile; between crossings it is the view's own, so a line arriving
  // inside a view moves nothing late. Reduced motion skips the easing.
  let currentView: HTMLElement | null = null;
  let viewH = -1;
  let easeEnd: ReturnType<typeof setTimeout> | undefined;
  function sized(node: HTMLElement) {
    const box = node.parentElement;
    const easing = !!box?.classList.contains("easing");
    // Mid-ease the panel is where its transition has got to; otherwise it
    // is the old view's, as last measured.
    const from = easing && box ? box.getBoundingClientRect().height : viewH;
    currentView = node;
    viewH = node.getBoundingClientRect().height;
    if (box && from >= 0 && Math.abs(from - viewH) >= 1 && delay(MOVE) > 0) {
      clearTimeout(easeEnd);
      box.classList.remove("easing");
      box.style.height = `${from}px`;
      void box.offsetHeight; // lay the start out before the move
      box.classList.add("easing");
      box.style.height = `${viewH}px`;
      easeEnd = setTimeout(() => {
        box.classList.remove("easing");
        box.style.height = "";
      }, delay(MOVE));
    }
    const ro = new ResizeObserver(() => {
      if (currentView !== node) return;
      viewH = node.getBoundingClientRect().height;
      if (box?.classList.contains("easing")) box.style.height = `${viewH}px`;
    });
    ro.observe(node);
    return {
      destroy() {
        ro.disconnect();
        if (currentView === node) currentView = null;
      },
    };
  }

  // What the touch view says, by the step: only the words cross-fade, the
  // rings and the green stay (APP.md §6, Motion: Unlock).
  const touchStep = $derived(c?.step ?? CeremonyStep.StepTouch);
  const touchLead = $derived(
    touchStep === CeremonyStep.StepDone ? doneTitle(c?.kind ?? "unlock") : touchStep === CeremonyStep.StepDeriving ? derivingLead : stepText(touchStep).title,
  );
  const touchSub = $derived(
    touchStep === CeremonyStep.StepTouch ? stepText(touchStep).body : touchStep === CeremonyStep.StepReleasing ? stepText(touchStep).body : touchStep === CeremonyStep.StepDeriving && c?.error ? codeText(c.error) : "",
  );
  const touchReceipt = $derived.by(() => {
    if (touchStep === CeremonyStep.StepDone) return vaultName;
    const slot = c?.slotLabel ?? "";
    if (touchStep === CeremonyStep.StepTouch) {
      const parts = [c?.pinAsked ? lockCopy.pinAccepted : "", slot, (c?.n ?? 0) > 1 ? ceremonyCopy.touchCount(c?.n ?? 0) : ""].filter(Boolean);
      return parts.join(" · ");
    }
    return [lockCopy.touched, slot].filter(Boolean).join(" · ");
  });

  // The password-only vault's unlock is one step, the rest panel turned
  // into its field: *Unlock*, since nothing follows the password but the
  // opening (the PIN's *Continue* is for the touch that follows it).
  const oneStep = $derived(passwordOnly && c?.kind === "unlock" && way === "password");

  // The rest panel's caption: the state and the last unlock, relative.
  const restLine = $derived(lockCopy.restLine(st?.lastUnlockedAt ? sinceText(st.lastUnlockedAt, store.now) : ""));
  const brokenLine = $derived(lockCopy.brokenLine(st?.lastUnlockedAt ? sinceText(st.lastUnlockedAt, store.now) : ""));

  // The maintenance commands, behind one quiet menu (ruled 2026-10-17).
  const backupItems = $derived([
    { label: commandCopy.testBackup, run: () => void testBackup() },
    { label: commandCopy.replaceVault, run: () => openImport() },
  ]);

  // The rest panel's first button takes the focus as it arrives, so Enter
  // begins the unlock (APP.md §6).
  function takeFocus(node: HTMLElement) {
    // The ring is not drawn for a focus the user did not move: it shows
    // once the keyboard is used, as anywhere else.
    if (!create && !setup && !importing) node.focus({ preventScroll: true, focusVisible: false } as FocusOptions);
  }

  // Leaving a failure the core has finished with: nothing to cancel, the
  // panel is dismissed.
  function leaveFailed() {
    if (running) void Vault.CancelUnlock();
    else store.dismissCeremony();
  }

  let setup = $state(false);
  let setupFirst = $state<ReturnType<typeof FirstWayIn>>();
  let setupAttempt = $state(0);

  async function doSetup() {
    setupAttempt++;
    const first = setupFirst?.take();
    if (!first) return;
    setup = false;
    store.dismissCeremony();
    // The password typed in the dialog answers the setup's Choose prompt,
    // after the recovery key (APP.md §6, ruled 2026-10-17); the bound call
    // carries the kind, the name and the tick, never the secret (§1). A
    // YubiKey's name left empty goes as empty: the core names it after its
    // serial number ("YubiKey 12345678"), which is what the field promises.
    store.holdChosen("setup", first.password);
    try {
      await Vault.FinishSetup(first.kind, first.label, first.entangle);
    } catch (e) {
      store.dropChosen();
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

  function openSetup() {
    setupAttempt = 0;
    setup = true;
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

  // What the panel says when a ceremony ends, by kind.
  function doneTitle(kind: string): string {
    switch (kind) {
      case "import": return restoring ? lockCopy.done.restored : lockCopy.done.replaced;
      case "setup": return lockCopy.done.setup;
      // Drawn only when a lock lands before the new recovery key is shown.
      case "create": return lockCopy.done.created;
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

  // Find it…: the vault moved, or its drive is not this letter now. The
  // file picked opens through the import dialog with "use the file where
  // it is" taken, the route that already says what the file is.
  async function findIt() {
    const p = (await Shell.PickFiles(lockCopy.findPicker, false)) ?? [];
    if (p.length) openImport(p[0], "inplace");
  }

  async function pickCreatePath() {
    const p = await Shell.SaveFile(lockCopy.savePicker,"vault.eks", "");
    if (p) createPath = p;
  }

  async function doCreate() {
    createAttempt++;
    if (!createNameValid || (createReplaces && !createConfirm)) return;
    // The cards judge their own parts under the same press (attempt) and
    // answer null while any refuses (lib/firstwayin).
    const first = createFirst?.take();
    if (!first) return;
    create = false;
    store.dismissCeremony();
    // The ceremony asks the chosen password first, before the YubiKey is
    // set up (APP.md §3): the store answers that prompt with the password
    // typed in the dialog, at once, and drops it (APP.md §6, ruled
    // 2026-10-17). Held before the call, so the prompt cannot come first.
    store.holdChosen("create", first.password);
    try {
      // A password unlock method is named "Password", and the tick is a
      // YubiKey's alone (lib/firstwayin begin). Elsewhere, a file already
      // at the chosen place is retired as a dated copy — the save dialog
      // asked about it; here, the tick.
      await Vault.CreateVault(rebuild ? dest : createPath, createName, first.kind, first.label, first.entangle, createReplaces ? createConfirm : !!createPath);
    } catch (e) {
      store.dropChosen();
      store.toast(codeText(errorOf(e).code), "error");
    }
  }

  const warnings = $derived(st?.warnings ?? []);

  // The header's padlock closes over the first moment the lock screen
  // shows (APP.md §6, Motion): open on arrival, shut after the settle.
  let closed = $state(false);
  onMount(() => {
    const t = setTimeout(() => (closed = true), delay(OUT));
    return () => {
      clearTimeout(t);
      clearTimeout(easeEnd);
    };
  });
  // The glyph says what the word beside it says: open again at Done, where
  // the header reads *Unlocked* (APP.md §6, Motion: Done).
  const padlockOpen = $derived(padlockOpenOf(closed, running, c));
  const unlockedNow = $derived(running && c?.kind === "unlock" && c?.step === CeremonyStep.StepDone);
</script>

<section class="unlock" class:green={isGreen} aria-label={lockCopy.regionLabel}>
  <!-- The touch's green: an overlay over the whole stage whose opacity
       moves, since a gradient cannot transition (APP.md §6, Motion). -->
  <div class="wash" aria-hidden="true"></div>
  <!-- The stage scrolls inside the section, so the wash under it covers
       the whole window however far a short one scrolls. -->
  <div class="u-scroll">
  <div class="u-head">
    <div class="brand"><svg class="mark i" viewBox="0 0 20 20"><use href="#i-mark" /></svg>Enfold</div>
    <div class="state">
      <span class="lockbox" aria-hidden="true">
        {#key padlockOpen}<svg class="i i-14 padlock" in:fade={motion()} out:fade={motion(LEAVE)}><use href={padlockOpen ? "#i-unlock" : "#i-lock"} /></svg>{/key}
      </span>
      {#if shown === "failed" && c?.kind === "unlock"}{lockCopy.header.failed}{:else if running}{unlockedNow ? lockCopy.header.unlocked : c?.kind === "import" ? importHeader : c?.kind === "create" ? lockCopy.header.creating : c?.kind === "setup" ? lockCopy.header.settingUp : c?.kind === "verify" ? lockCopy.header.testing : lockCopy.header.unlocking}{:else if card === "damaged"}{lockCopy.header.damaged}{:else if card === "absent"}{lockCopy.header.absent}{:else if firstRun}{lockCopy.header.none}{:else if busy}{lockCopy.header.busy}{:else if broken}{lockCopy.header.broken}{:else if setupNeeded}{lockCopy.header.setupNeeded}{:else}{lockCopy.header.locked}{/if}
      <!-- Settings needs no vault (APP.md §6, ruled 2026-10-17): the gear
           opens it from the lock scene at rest, but not while a ceremony
           runs or its failure is on the panel, which the scene is there to
           show. -->
      {#if shown === "rest" || shown === "firstrun"}
        <button type="button" class="btn subtle xs gear" aria-label={lockCopy.settings} title={lockCopy.settings} onclick={() => store.openSettingsLocked()}><svg class="i i-14"><use href="#i-settings" /></svg></button>
      {/if}
    </div>
  </div>

  <div class="u-stage">
    {#if !firstRun && (warnings.length > 0 || openArchives > 0)}
      <!-- Notices stand above the panel, its width, so nothing warns from
           below the buttons (APP.md §6, ruled 2026-10-17). -->
      <div class="notices">
        {#each warnings as w (w)}
          <!-- The reason names which cause the status carries; every
               other code ignores it (APP.md §13, VaultStatus.TamperedReason). -->
          <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span>{warningCopy(w, st?.tamperedReason)}</span></div>
        {/each}
        {#if openArchives > 0}
          <div class="bar">
            <svg class="i i-14"><use href="#i-info" /></svg>
            <span class="grow num">{lockCopy.openWhileLocked(openArchives)}</span>
            <button type="button" class="btn link" onclick={() => store.go(store.current ? "archive" : "archives")}>{vaultCopy.show}</button>
          </div>
        {/if}
      </div>
    {/if}

    <!-- The step line: only while an unlock runs; at rest its line stays
         empty so the panel does not move when one begins. -->
    <ol class="steps" aria-label={lockCopy.stepsLabel} aria-hidden={!lineShown}>
      {#if lineShown}
        {#each steps as label, i (i)}
          <li class={states[i]} aria-current={states[i] === "live" ? "step" : undefined} in:fade={motion()} out:fade={motion(LEAVE)}>
            <span class="disc">{#if states[i] === "done"}<svg class="i" viewBox="5 5 10 10"><path d="m6.8 10.2 2.2 2.2 4.3-4.6" /></svg>{:else}<span class="num">{i + 1}</span>{/if}</span>{label}
          </li>
        {/each}
      {/if}
    </ol>

    <div class="panel">
      <div class="views">
      {#key viewKey}
      <div class="view" class:center={shown === "touch" || shown === "done"} in:fade={xin()} out:fade={xout()} use:sized>
        {#if shown === "firstrun"}
          {#if card === "damaged" && st}
            <!-- The best remedy first (APP.md §6, ruled 2026-10-17): the
                 vault a replacement kept, then trying again or another
                 file, and the rebuild last, in danger ink, with what it
                 costs on its own line. -->
            <div class="lead"><h2 class="t-title">{lockCopy.damagedLead(missingName)}</h2><div class="cap"><span class="mono">{st.missingPath}</span></div></div>
            {#if keptCopy}
              <div class="bar attention">
                <svg class="i i-14"><use href="#i-warn" /></svg>
                <span class="grow">{lockCopy.keptCopy(keptDate)}</span>
                <button type="button" class="btn sm" onclick={() => openImport(st?.retiredPath ?? "")} use:takeFocus>{lockCopy.useThatCopy}</button>
              </div>
            {/if}
            <div class="row">
              {#if keptCopy}
                <button type="button" class="btn grow" onclick={retryMissing}>{lockCopy.tryAgain}</button>
              {:else}
                <button type="button" class="btn accent grow" onclick={retryMissing} use:takeFocus>{lockCopy.tryAgain}</button>
              {/if}
              <button type="button" class="btn grow" onclick={() => openImport()}>{lockCopy.restoreOther}</button>
            </div>
            <!-- The rebuild and what it costs read as one: its line sits
                 under it at the lead's gap, not the panel's. -->
            <div class="then">
              <button type="button" class="btn subtle sm danger" onclick={openCreate}>{commandCopy.rebuildVault}</button>
              <div class="cap">{lockCopy.rebuildLine}</div>
            </div>
          {:else if card === "absent" && st}
            <!-- Gone from where Enfold left it: try again, find it where it
                 went, restore, and last a new vault (ruled 2026-10-17). -->
            <div class="lead"><h2 class="t-title">{lockCopy.absentLead(missingName)}</h2><div class="cap"><span class="mono">{st.missingPath}</span></div></div>
            <button type="button" class="btn accent wide" onclick={retryMissing} use:takeFocus>{lockCopy.tryAgain}</button>
            <button type="button" class="btn wide" onclick={findIt}><svg class="i i-14"><use href="#i-folder" /></svg>{lockCopy.findIt}</button>
            <button type="button" class="btn wide" onclick={() => openImport()}><svg class="i i-14"><use href="#i-open" /></svg>{commandCopy.restoreFrom}</button>
            <button type="button" class="btn subtle sm start" onclick={openCreate}>{lockCopy.createInstead}</button>
          {:else}
            <!-- The panel is its content's height; what a restore needs is
                 said in the restore dialog, where it applies. -->
            <div class="lead"><h2 class="t-title">{lockCopy.firstRunLead}</h2><div class="cap">{lockCopy.firstRunLine}</div></div>
            <button type="button" class="btn accent wide" onclick={openCreate} use:takeFocus><svg class="i i-14"><use href="#i-plus" /></svg>{commandCopy.createVault}</button>
            <button type="button" class="btn wide" onclick={() => openImport()}><svg class="i i-14"><use href="#i-open" /></svg>{commandCopy.restoreFrom}</button>
            <button type="button" class="btn subtle sm start" onclick={testBackup}>{commandCopy.testBackup}</button>
          {/if}
          {#if c && c.step === CeremonyStep.StepFailed && c.error !== "ceremony.cancelled"}
            <div class="bar danger"><svg class="i i-14"><use href="#i-warn" /></svg><span>{codeText(c.error)}</span></div>
          {:else if outcome?.kind === "verify"}
            <div class="bar accent"><svg class="i i-14"><use href="#i-check" /></svg><span>{ceremonyCopy.verified(outcome.archives)}</span></div>
          {:else if outcome?.kind === "import"}
            <div class="bar accent"><svg class="i i-14"><use href="#i-check" /></svg><span>{lockCopy.restoredUnopened}</span></div>
          {:else if outcome?.kind === "create"}
            <div class="bar accent"><svg class="i i-14"><use href="#i-check" /></svg><span>{lockCopy.createdUnopened}</span></div>
          {/if}
          {#if !rebuild && (st?.retiredCopies ?? 0) > 0 && st?.retiredPath}
            <!-- Restoring is a command, so a button, on the 40 px line a bar
                 with a small button takes (APP.md §6, Controls). -->
            <div class="bar line">
              <svg class="i i-14"><use href="#i-info" /></svg>
              <span class="grow">A replaced copy of a vault is in Enfold's folder: {leaf(st.retiredPath)}.</span>
              <button type="button" class="btn sm" onclick={() => openImport(st?.retiredPath ?? "")}>{lockCopy.restoreIt}</button>
            </div>
          {/if}
        {:else if shown === "rest"}
          {#if st?.pendingTouch && st?.hasHardwareSlot && !busy && !broken && !setupNeeded}
            <!-- The YubiKey really is still waiting for a touch: its LED
                 keeps breathing (APP.md §6, ruled 2026-10-17). -->
            <KeyArt led="listen" />
          {:else if !setupNeeded}
            <!-- Finishing setup is first run's shape, not the locked
                 vault's: no padlock (APP.md §6, ruled 2026-10-17). -->
            <svg class="i i-20 restlock" aria-hidden="true"><use href="#i-lock" /></svg>
          {/if}
          <!-- Each state says its own lead; Open elsewhere never
               borrows the rest panel's (ruled 2026-10-17). -->
          <div class="lead">
            <h2 class="t-title">{setupNeeded ? lockCopy.finishLead(vaultName) : busy ? lockCopy.busyLead(vaultName) : passwordOnly ? lockCopy.unlockVault(vaultName) : vaultName}</h2>
            <!-- The caption names the state the header names: a broken vault
                 is not simply locked (ruled 2026-10-17). -->
            <div class="cap">{setupNeeded ? lockCopy.setupLine : busy ? lockCopy.busyLine : broken ? brokenLine : restLine}</div>
          </div>
          {#if st?.pendingTouch}<div class="infoline"><svg class="i i-14"><use href="#i-info" /></svg><span>{lockCopy.pendingTouch}</span></div>{/if}
          <!-- The status's own note: the five minutes from the touch ran
               out with the vault's password not given, and the ceremony
               ended where a cancel ends it (APP.md §2.2). The next
               ceremony clears it, so it needs no dismissal. -->
          {#if st?.note}<div class="infoline"><svg class="i i-14"><use href="#i-info" /></svg><span>{codeText(st.note)}</span></div>{/if}
          {#if c && c.step === CeremonyStep.StepFailed && c.error !== "ceremony.cancelled"}
            <div class="bar danger"><svg class="i i-14"><use href="#i-warn" /></svg><span>{codeText(c.error)}</span></div>
          {:else if outcome?.kind === "import"}
            <div class="bar accent"><svg class="i i-14"><use href="#i-check" /></svg><span>{restoring ? lockCopy.restored : lockCopy.replaced}</span></div>
          {:else if outcome?.kind === "verify"}
            <div class="bar accent"><svg class="i i-14"><use href="#i-check" /></svg><span>{ceremonyCopy.verified(outcome.archives)}</span></div>
          {:else if outcome?.kind === "create"}
            <div class="bar accent"><svg class="i i-14"><use href="#i-check" /></svg><span>{lockCopy.created}</span></div>
          {/if}
          {#if busy}
            <button type="button" class="btn accent wide" onclick={() => void Vault.Reopen()} use:takeFocus>{lockCopy.tryAgain}</button>
          {:else if broken}
            <!-- An attention line, not a bar: the boxed bar is kept for the
                 last try and the kept copy (APP.md §6, ruled 2026-10-17). -->
            <div class="infoline att"><svg class="i i-14"><use href="#i-warn" /></svg><span>{codeText("vault.broken")}</span></div>
            <button type="button" class="btn accent wide" onclick={() => void Vault.Reopen()} use:takeFocus>{lockCopy.reopen}</button>
          {:else if setupNeeded}
            <button type="button" class="btn accent wide" onclick={openSetup} use:takeFocus><svg class="i i-14"><use href="#i-plus" /></svg>{lockCopy.finishTitle}…</button>
            <button type="button" class="btn wide" onclick={() => begin("recovery")}>{lockCopy.unlockWithRecovery}</button>
          {:else}
            {#if st?.hasHardwareSlot}
              <!-- One label whatever the switch (ruled 2026-09-08): the
                   PIN's step says which secrets it asks for. With a touch
                   still pending, pressing it picks that touch up (APP.md
                   §2.2), and it says so. -->
              <button type="button" class="btn accent wide" onclick={() => begin("token")} use:takeFocus><svg class="i i-14"><use href="#i-yubi" /></svg>{st?.pendingTouch ? lockCopy.continueUnlocking : lockCopy.unlockWithYubiKey}</button>
            {/if}
            {#if st?.hasPasswordSlot}
              {#if passwordOnly}
                <button type="button" class="btn accent wide" onclick={() => begin("password")} use:takeFocus><svg class="i i-14"><use href="#i-password" /></svg>{lockCopy.unlockWithPassword}</button>
              {:else}
                <button type="button" class="btn wide" onclick={() => begin("password")}><svg class="i i-14"><use href="#i-password" /></svg>{lockCopy.unlockWithPassword}</button>
              {/if}
            {/if}
            <button type="button" class="btn wide" onclick={() => begin("recovery")}>{lockCopy.unlockWithRecovery}</button>
          {/if}
          {#if !busy && !broken}
            <!-- Commands, so a subtle button: a hyperlink button is for
                 going somewhere (APP.md §6, Controls). The two leave the
                 daily unlock for one quiet menu (ruled 2026-10-17). -->
            <div class="start"><MenuButton id="lock-backups" label={lockCopy.backups} items={backupItems} small /></div>
          {/if}
        {:else if shown === "insert" && c}
          <!-- With the Smart Card service not running nothing is heard
               from the reader, so the LED stops breathing (APP.md §6,
               ruled 2026-10-17). -->
          <KeyArt led={insertLed(insertStep, found, c)} />
          <div class="lead">
            {#if insertStep === CeremonyStep.StepTwoKeys}
              <h2 class="t-title">{stepText(insertStep).title}</h2>
              <div class="cap">{stepText(insertStep).body}</div>
            {:else if insertStep === CeremonyStep.StepProbing}
              <h2 class="t-title">{stepText(insertStep).title}</h2>
              <div class="cap">{stepText(insertStep).body}</div>
            {:else}
              <h2 class="t-title">{prefersReducedMotion.current ? lockCopy.insertStill : stepText(CeremonyStep.StepWaitingForKey).title}</h2>
              <!-- An unlock's YubiKey unlocks the vault; an enrolment's is
                   the one being added (a create, a setup, a backup's
                   import: the core names no YubiKey way in for them). -->
              <div class="cap">{c.kind === "unlock" && vaultName ? lockCopy.insertLine(vaultName) : c.kind !== "verify" && way !== "token" ? lockCopy.insertAdd : stepText(CeremonyStep.StepWaitingForKey).body}</div>
            {/if}
          </div>
          {#if c.error && c.step === CeremonyStep.StepWaitingForKey}
            <!-- An attention line under the lead, as the broken vault's. -->
            <div class="infoline att"><svg class="i i-14"><use href="#i-warn" /></svg><span>{codeText(c.error)}</span></div>
          {/if}
          <div class="row">
            <!-- The view that replaced the focused button takes the focus,
                 so the keyboard keeps its place (APP.md §6); here Cancel is
                 the one standard button. -->
            <button type="button" class="btn" onclick={() => void Vault.CancelUnlock()} use:takeFocus>{dialogCopy.cancel}</button>
            <!-- Two YubiKeys in: the core carries on by itself once one is
                 out, so Cancel is all there is to press (ruled 2026-10-17). -->
            {#if offersRecovery && insertStep !== CeremonyStep.StepTwoKeys}<button type="button" class="btn subtle sm" onclick={() => begin("recovery")}>{lockCopy.useRecoveryInstead}</button>{/if}
          </div>
        {:else if shown === "edge" && c}
          <KeyArt led={c.step === CeremonyStep.StepNoMatch ? "off" : "found"} />
          <div class="lead">
            <h2 class="t-title">{c.step === CeremonyStep.StepNoMatch && c.kind === "unlock" && vaultName ? lockCopy.noMatch(vaultName) : stepText(c.step).title}</h2>
            <div class="cap">{c.step === CeremonyStep.StepNoMatch && !offersRecovery ? ceremonyCopy.noMatchWithoutRecovery : stepText(c.step).body}</div>
          </div>
          <div class="row">
            <!-- No match offers what its line names, for an unlock alone.
                 Every two-button row of the panel is the accent, grown,
                 then Cancel at its own width — the PIN's, a timeout's, a
                 blocked PIN's (ruled 2026-10-17); the recovery key's cells
                 alone keep the accent at its width, the count at the row's
                 end. -->
            {#if c.step === CeremonyStep.StepNoMatch && offersRecovery}
              <button type="button" class="btn accent grow" onclick={() => begin("recovery")} use:takeFocus>{lockCopy.useRecoveryKey}</button>
              <button type="button" class="btn" onclick={() => void Vault.CancelUnlock()}>{dialogCopy.cancel}</button>
            {:else}
              <button type="button" class="btn" onclick={() => void Vault.CancelUnlock()} use:takeFocus>{dialogCopy.cancel}</button>
            {/if}
          </div>
        {:else if shown === "secret" && c}
          {#if oneStep}
            <!-- The password-only vault's one step: the rest panel's padlock
                 and lead stay where they were, the field arrives under
                 them (APP.md §6, ruled 2026-10-17). -->
            <svg class="i i-20 restlock" aria-hidden="true"><use href="#i-lock" /></svg>
            <div class="lead"><h2 class="t-title">{lockCopy.unlockVault(vaultName)}</h2><div class="cap">{restLine}</div></div>
          {:else if c.kind !== "unlock"}
            <!-- With no step line, the PIN's step heads with its own
                 instruction, as the YubiKey's and the touch's do; the act's
                 name is the header's (ruled 2026-10-17). A step that asks
                 no PIN keeps the act's name. -->
            <div class="lead"><h2 class="t-title">{pinLeads(c.kind, form.shows) ? lockCopy.pinLead : actLead}</h2></div>
          {/if}
          {#if c.slotLabel && c.step !== CeremonyStep.StepManagementKey}<span class="slotchip"><svg class="i i-14"><use href="#i-yubi" /></svg>{c.slotLabel}</span>{/if}
          {#if store.chosenSent && store.chosenSent === c.promptId}
            <!-- The Choose prompt the dialog's password answered (APP.md
                 §6, ruled 2026-10-17): no field to fill. Refused all the
                 same, the prompt stands and is asked below as before. -->
            <div class="cap">{firstMethodCopy.sent}</div>
          {:else if c.promptId && form.shows !== "none"}
            {#if (form.shows === "pin" || form.shows === "both") && lastTry(c)}
              <!-- The last try, before it is typed: the consequence and the
                   way out (ruled 2026-10-17). -->
              <div class="bar attention">
                <svg class="i i-14"><use href="#i-warn" /></svg>
                <span class="grow"><strong>{lockCopy.lastTryHead}</strong> {lockCopy.lastTry}</span>
                {#if offersRecovery}<button type="button" class="btn sm" onclick={() => begin("recovery")}>{lockCopy.useRecoveryKey}</button>{/if}
              </div>
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
              button={oneStep || afterTouch ? commandCopy.unlock : lockCopy.continue}
              error={secretError}
              reserve
              cancel={{ label: dialogCopy.cancel, run: () => void Vault.CancelUnlock() }}
            />
          {:else if c.promptId && c.step === CeremonyStep.StepManagementKey}
            <SecretInput kind="mgmtkey" promptId={c.promptId} label={ceremonyCopy.managementKeyField} note={stepText(c.step).body} reserve cancel={{ label: dialogCopy.cancel, run: () => void Vault.CancelUnlock() }} />
          {/if}
          {#if oneStep}<button type="button" class="btn subtle sm start" onclick={() => begin("recovery")}>{lockCopy.unlockWithRecovery}</button>{/if}
          <!-- On the last try the bar above the field carries the way out,
               and only once. -->
          {#if offersRecovery && (form.shows === "pin" || form.shows === "both") && !lastTry(c)}<button type="button" class="btn subtle sm start" onclick={() => begin("recovery")}>{lockCopy.useRecoveryInstead}</button>{/if}
          {#if !c.promptId || (store.chosenSent && store.chosenSent === c.promptId)}
            <!-- The panel's Cancel is one standard button wherever it
                 stands, as beside the fields (ruled 2026-10-17). -->
            <div class="row"><button type="button" class="btn" onclick={() => void Vault.CancelUnlock()}>{dialogCopy.cancel}</button></div>
          {/if}
        {:else if shown === "recovery" && c}
          <!-- The recovery key (APP.md §6, ruled 2026-10-17): the vault, where
               the digits come from, its keys ID first, the cells, and
               *Unlock* beside *Cancel* — nothing follows but the opening. -->
          <!-- A setup's recovery key is this vault's too, so the panel names
               the vault; the header already says *Setting up*. -->
          <div class="lead"><h2 class="t-title">{c.kind === "unlock" || c.kind === "setup" ? vaultName : actLead}</h2><div class="cap">{lockCopy.recoveryLine}</div></div>
          <RecoveryInput
            promptId={c.promptId ?? ""}
            slots={recoverySlots}
            heading={false}
            note={c.kind === "verify" ? ceremonyCopy.recoveryNote.verify : ""}
            button={c.kind === "verify" ? lockCopy.testButton : c.kind === "unlock" ? commandCopy.unlock : lockCopy.continue}
            error={c.error === "vault.auth" ? (c.kind === "verify" || c.kind === "import" ? ceremonyCopy.recoveryRefused.file : lockCopy.recoveryRefused(vaultName)) : ""}
            cancel={{ label: dialogCopy.cancel, run: () => void Vault.CancelUnlock() }}
          />
        {:else if shown === "touch"}
          <!-- The touch, the derivation, the release and Done of a YubiKey's
               way in are one view on the green: the rings stay, pulsing for
               the touch and held still after it, and only the words
               cross-fade (APP.md §6, Motion: Unlock). -->
          <div class="rings big" class:live={touchStep === CeremonyStep.StepTouch} class:still={touchStep === CeremonyStep.StepDeriving || touchStep === CeremonyStep.StepReleasing} class:done={touchStep === CeremonyStep.StepDone} aria-hidden="true">
            <span></span><span></span><span></span>
            {#if touchStep === CeremonyStep.StepDone}
              <!-- A bare tick: the disc is its circle (#i-check would draw
                   a ring inside the ring). -->
              <div class="core pop"><svg viewBox="0 0 20 20"><path d="m5.6 10.3 2.9 2.9 5.9-6.2" /></svg></div>
            {:else}
              <div class="core"><svg viewBox="0 0 20 20"><use href="#i-touchdot" /></svg></div>
            {/if}
          </div>
          <div class="xf">{#key touchLead}<h2 class="touch-lead" in:fade={xin()} out:fade={xout()}>{touchLead}</h2>{/key}</div>
          <div class="xf">{#key touchSub}<p class="touch-sub" in:fade={xin()} out:fade={xout()}>{touchSub}</p>{/key}</div>
          <div class="xf">
            {#key touchReceipt}
              <div class="touch-slot" in:fade={xin()} out:fade={xout()}>
                <svg class="i i-14"><use href={touchStep === CeremonyStep.StepDone ? "#i-unlock" : "#i-yubi"} /></svg>{touchReceipt}
              </div>
            {/key}
          </div>
        {:else if shown === "opening" && c}
          <!-- A typed way in derives on the neutral panel: no green, since
               nothing is touched (APP.md §6). -->
          <div class="lead">
            <h2 class="t-title">{c.step === CeremonyStep.StepDeriving ? derivingLead : stepText(c.step).title}</h2>
            <!-- A create's lead names its act already; the header says it too. -->
            <div class="cap">{c.step === CeremonyStep.StepDeriving ? (c.error ? codeText(c.error) : c.kind === "unlock" ? vaultName : c.kind === "create" ? "" : actLead) : stepText(c.step).body}</div>
          </div>
        {:else if shown === "done" && c}
          <div class="donedisc pop" aria-hidden="true"><svg viewBox="0 0 20 20"><path d="m5.6 10.3 2.9 2.9 5.9-6.2" /></svg></div>
          <h2 class="t-title">{doneTitle(c.kind)}</h2>
          <div class="cap">{vaultName}</div>
        {:else if shown === "failed" && c}
          {#if c.error === "token.touch"}
            <!-- Where it happened and in neutral ink: the YubiKey stopped
                 waiting, nothing refused anything (ruled 2026-10-17). -->
            <svg class="i i-20 restlock" viewBox="0 0 20 20" aria-hidden="true"><use href="#i-touchdot" /></svg>
            <div class="lead"><h2 class="t-title">{lockCopy.stoppedWaiting}</h2><div class="cap">{lockCopy.stoppedLine}</div></div>
          {:else}
            <div class="lead"><h2 class="t-title">{stepText(c.step).title}</h2></div>
            <div class="bar danger"><svg class="i i-14"><use href="#i-warn" /></svg><span>{codeText(c.error)}</span></div>
          {/if}
          <div class="row">
            {#if restartable && againWay}
              {@const m = againWay}
              <button type="button" class="btn accent grow" onclick={() => begin(m)} use:takeFocus>{lockCopy.startAgain}</button>
              <button type="button" class="btn" onclick={leaveFailed}>{dialogCopy.cancel}</button>
            {:else}
              <button type="button" class="btn accent" onclick={leaveFailed} use:takeFocus>{dialogCopy.close}</button>
            {/if}
          </div>
        {:else if shown === "blocked" && c}
          <!-- On the PIN's step, where it arises: whose PIN in the lead,
               then one sentence on a danger bar — what it means and, in an
               unlock, the way out — and that way out as the accent (APP.md
               §6, ruled 2026-10-17). -->
          {#if c.slotLabel}<span class="slotchip"><svg class="i i-14"><use href="#i-yubi" /></svg>{c.slotLabel}</span>{/if}
          <div class="lead"><h2 class="t-title">{ceremonyCopy.blockedLead}</h2></div>
          <div class="bar danger"><svg class="i i-14"><use href="#i-warn" /></svg><span>{offersRecovery ? ceremonyCopy.blockedMeansWayOut : ceremonyCopy.blockedMeans}</span></div>
          <div class="row">
            {#if offersRecovery}
              <button type="button" class="btn accent grow" onclick={() => begin("recovery")} use:takeFocus>{lockCopy.useRecoveryKey}</button>
              <button type="button" class="btn" onclick={() => void Vault.CancelUnlock()}>{dialogCopy.cancel}</button>
            {:else}
              <button type="button" class="btn" onclick={() => void Vault.CancelUnlock()} use:takeFocus>{dialogCopy.cancel}</button>
            {/if}
          </div>
        {:else}
          <!-- A step that asks nothing here (records, a swap, a recovery
               key being shown), or the instant before the first event. -->
          <div class="lead">
            <h2 class="t-title">{c ? stepText(c.step).title || actLead : actLead}</h2>
            {#if c && stepText(c.step).body}<div class="cap">{stepText(c.step).body}</div>{/if}
          </div>
          {#if c && running}<div class="row"><button type="button" class="btn" onclick={() => void Vault.CancelUnlock()}>{dialogCopy.cancel}</button></div>{/if}
        {/if}
      </div>
      {/key}
      </div>
    </div>
  </div>
  </div>
</section>

{#if create}
  <!-- A form (APP.md §6, Dialogs stay put; ruled 2026-10-17): Enter in
       the name or a password presses Create vault, under the form rule,
       and the focus opens in the name. Anchored at the top, since a card
       that opens makes it taller and the title must not climb. -->
  <Dialog title={rebuild ? lockCopy.rebuildTitle : lockCopy.createTitle} onclose={() => (create = false)} onsubmit={doCreate} anchored>
    <div class="named">
      <TextField id="cv-name" label={vaultCopy.name} bind:value={createName} bind:valid={createNameValid} attempt={createAttempt} />
      <!-- Where it lives, one caption line under the name, 4 px below it
           (ruled 2026-10-17): the place in mono, cut in the middle so the
           drive and the file name stay, then the hyperlink button that
           changes it, in the line's caption — none for a rebuild, which is
           pinned to the damaged file's place. -->
      <div class="where">
        <span class="wherelabel">{lockCopy.keptInLabel}</span>
        <span class="path mono" title={dest}>{destShown}</span>
        {#if rebuild}{:else if createPath}<button type="button" class="btn link" onclick={() => (createPath = "")}>{lockCopy.usualPlace}</button>{:else}<button type="button" class="btn link" onclick={pickCreatePath}>{lockCopy.keepElsewhere}</button>{/if}
      </div>
    </div>
    <FirstWayIn bind:this={createFirst} heading={firstMethodCopy.headCreate} attempt={createAttempt} idPrefix="cv" />
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
      <button type="submit" class="btn accent" disabled={(createReplaces && !createConfirm) || openArchives > 0}>{rebuild ? lockCopy.rebuildPrimary : lockCopy.createPrimary}</button>
      <button type="button" class="btn" onclick={() => (create = false)}>{dialogCopy.cancel}</button>
    {/snippet}
  </Dialog>
{/if}

{#if setup}
  <!-- A form, anchored, as the create dialog is: with no field shown
       until a card opens, it opens on Continue (Dialog.svelte). -->
  <Dialog title={lockCopy.finishTitle} onclose={() => (setup = false)} onsubmit={doSetup} anchored>
    <p>{lockCopy.finishBody}</p>
    <FirstWayIn bind:this={setupFirst} heading={firstMethodCopy.headCreate} context="adopt" attempt={setupAttempt} idPrefix="su" />
    {#snippet actions()}
      <button type="submit" class="btn accent">{lockCopy.continue}</button>
      <button type="button" class="btn" onclick={() => (setup = false)}>{dialogCopy.cancel}</button>
    {/snippet}
  </Dialog>
{/if}

{#if importing}
  <ImportDialog prefill={importPrefill} prefer={importPrefer} onclose={() => (importing = false)} />
{/if}


<style>
  .named { display: flex; flex-direction: column; gap: var(--s1); }
  /* The "Kept in" line (APP.md §6, ruled 2026-10-17): one caption row
     that never wraps. The path arrives cut in the middle (destShown);
     should the window's fonts still make it too wide, its end is clipped
     rather than the button pushed to a row of its own — the full path is
     the title. The face, size and ink are .mono's; the hyperlink button
     takes the line's caption, its fill growing past the line's 16 px
     into the margins it gives back (app.css .btn.link). */
  .where { display: flex; align-items: center; gap: var(--s2); font: var(--t-cap); color: var(--ink-2); }
  .wherelabel, .where .btn { flex: none; }
  .where .btn.link { font: inherit; }
  .path { flex: 0 1 auto; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; user-select: text; }
  /* The gear beside the state word: a quiet icon button in the header's
     ink. Its 24 px overhang the header's 20 px line by 2 above and below,
     so the line, and the panel's 14vh under it, do not move. */
  .gear { margin: -2px 0 -2px var(--s1); padding: 0 var(--s1); color: inherit; }
  .lockbox { position: relative; width: 14px; height: 14px; display: inline-block; }
  .lockbox .padlock { position: absolute; inset: 0; }
</style>

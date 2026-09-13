<script lang="ts">
  import { CeremonyStep, VaultState } from "./lib/api";
  import { store } from "./lib/state.svelte";
  import { bootCopy } from "./lib/strings";
  import Icons from "./components/Icons.svelte";
  import Toasts from "./components/Toasts.svelte";
  import Rail from "./components/Rail.svelte";
  import Lock from "./components/Lock.svelte";
  import ArchivesPage from "./components/ArchivesPage.svelte";
  import ArchivePage from "./components/ArchivePage.svelte";
  import KeysPage from "./components/KeysPage.svelte";
  import SettingsPage from "./components/SettingsPage.svelte";
  import RecoveryReveal from "./components/RecoveryReveal.svelte";
  import CloseDialog from "./components/CloseDialog.svelte";
  import LayerFoot from "./components/LayerFoot.svelte";
  import { fade, fly } from "svelte/transition";
  import { motion, delay, enter, GAP, OUT, MOVE } from "./lib/motion";

  const st = $derived(store.status);
  const theme = $derived(store.settings?.theme ?? "system");

  $effect(() => {
    const root = document.documentElement;
    if (theme === "light" || theme === "dark") root.dataset.theme = theme;
    else delete root.dataset.theme;
  });

  // The lock screen shows whenever the vault is not unlocked and nothing
  // is open to browse, or when the user asks for it.
  const showLock = $derived.by(() => {
    if (!st) return true;
    if (st.state === VaultState.StateNone) return true;
    if (st.state === VaultState.StateUnlocked) return store.settling; // the unlock's full stop (APP.md §6)
    if (store.route === "lock") return true;
    return st.openArchives === 0;
  });

  // pageKey changes when the page does; the transition's direction is
  // the store's last navigation.
  const pageKey = $derived(store.route === "archive" && store.current ? `archive:${store.current}` : store.route);
  // The incoming page waits GAP for the outgoing one to be mostly gone,
  // so two texts never share a spot at readable opacity (APP.md §6).
  const pageIn = () => motion(160, { x: store.nav * 10, y: store.nav ? 0 : 6, delay: delay(GAP), easing: enter });

  // The recovery key's one reveal lives above every route: the ceremony
  // that minted it (create, or enrol) may have switched the view meanwhile.
  const reveal = $derived.by(() => {
    const c = store.ceremony;
    if (!c || c.step !== CeremonyStep.StepRecovery || c.promptId || !c.slotLabel || !store.revealPending(c.slotLabel)) return "";
    // A create's key is shown on a vault that ended Locked; any other
    // showing is on an unlocked vault, and closes when it locks (the core
    // drops the held key on a lock trigger).
    if (c.kind !== "create" && store.status?.state !== VaultState.StateUnlocked) return "";
    return c.slotLabel;
  });

  // The heartbeat is the vault's session and nothing else: an open archive
  // has no timeout of its own, so input on the archive page keeps the
  // session alive and asks nothing about the archive (APP.md §2.3,
  // DESIGN.md §10, ruled 2026-09-10).
  const activity = () => store.activity();
</script>

<svelte:window onpointerdown={activity} onkeydown={activity} onwheel={activity} />

<Icons />
<!-- Nothing is drawn until the first status is accepted (APP.md §2.4):
     the splash, then the scene that status names — never the lock scene
     on no evidence. A first status that failed is one plain line and
     Retry. -->
{#if !store.booted}
  <div class="splash">
    {#if store.bootFailed}
      <div class="boot-failed" role="alert"><p>{store.bootFailed}</p><button type="button" class="btn" onclick={() => void store.refreshAll()}>{bootCopy.retry}</button></div>
    {:else}
      <div class="brand"><svg class="mark i" viewBox="0 0 20 20"><use href="#i-mark" /></svg>Enfold</div>
    {/if}
  </div>
{:else}
  <div class="stage">
    {#if showLock}
      <div class="scene" in:fly={motion(MOVE, { y: 8, delay: delay(GAP), easing: enter })} out:fly={motion(OUT, { y: -8, easing: enter })}>
        <Lock />
      </div>
    {:else}
      <div class="scene shell" in:fade={motion(MOVE, { delay: delay(GAP) })} out:fly={motion(120, { y: 6, easing: enter })}>
        <Rail />
        <div class="layer" in:fly={motion(MOVE, { y: 8, delay: delay(GAP + 40), easing: enter })}>
          {#key pageKey}
            <div class="page" in:fly={pageIn()} out:fade={motion(80, { easing: enter })}>
              {#if store.route === "archive" && store.current}
                <ArchivePage />
              {:else if store.route === "keys"}
                <KeysPage />
              {:else if store.route === "settings"}
                <SettingsPage />
              {:else}
                <ArchivesPage />
              {/if}
            </div>
          {/key}
          <LayerFoot {pageKey} />
        </div>
      </div>
    {/if}
  </div>
{/if}

{#if reveal}
  {#key reveal}
    <RecoveryReveal url={reveal} kind={store.ceremony?.kind ?? ""} vaultName={store.status?.displayName ?? ""} recoveryId={store.ceremony?.recoveryId ?? ""} ondone={() => { store.dismissReveal(reveal); void store.refreshSlots(); }} />
  {/key}
{/if}

<!-- The close question (APP.md §2.4): above every route, and above the
     splash too — a close while the first status is still on its way
     deserves the same answer. -->
{#if store.closeAsked}
  <CloseDialog busy={store.closeBusy} onclose={() => store.dismissClose()} ondecide={(a, r) => void store.decideClose(a, r)} />
{/if}

<Toasts />

<style>
  .splash { height: 100%; display: flex; align-items: center; justify-content: center; color: var(--ink-3); }
  .boot-failed { display: flex; flex-direction: column; align-items: center; gap: 12px; color: var(--ink-2); font-size: 12.5px; }
  .boot-failed p { margin: 0; }
</style>

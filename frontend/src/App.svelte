<script lang="ts">
  import { Archive, CeremonyStep, VaultState, errorOf } from "./lib/api";
  import { store } from "./lib/state.svelte";
  import { codeText } from "./lib/strings";
  import { dateTime } from "./lib/format";
  import Icons from "./components/Icons.svelte";
  import Toasts from "./components/Toasts.svelte";
  import Rail from "./components/Rail.svelte";
  import Lock from "./components/Lock.svelte";
  import ArchivesPage from "./components/ArchivesPage.svelte";
  import ArchivePage from "./components/ArchivePage.svelte";
  import KeysPage from "./components/KeysPage.svelte";
  import SettingsPage from "./components/SettingsPage.svelte";
  import RecoveryReveal from "./components/RecoveryReveal.svelte";
  import Dialog from "./components/Dialog.svelte";

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
    if (st.state === VaultState.StateUnlocked) return false;
    if (store.route === "lock") return true;
    return st.openArchives === 0;
  });

  // The recovery key's one reveal lives above every route: the ceremony
  // that minted it (create, or enrol) may have switched the view meanwhile.
  const reveal = $derived.by(() => {
    const c = store.ceremony;
    if (c && c.step === CeremonyStep.StepRecovery && !c.promptId && c.slotLabel && store.revealPending(c.slotLabel)) return c.slotLabel;
    return "";
  });

  // A dirty archive about to close asks wherever the user is.
  const expiring = $derived(store.expiring);
  const expiringName = $derived(store.archives.find((a) => a.id === store.expiring?.id)?.name ?? "An archive");

  async function keepOpen() {
    const id = store.expiring?.id;
    store.expiring = null;
    if (!id) return;
    try {
      await Archive.KeepOpen(id);
    } catch (e) {
      store.toast(codeText(errorOf(e).code), "error");
    }
  }

  async function saveNow() {
    const id = store.expiring?.id;
    store.expiring = null;
    if (!id) return;
    try {
      await Archive.Save(id);
    } catch (e) {
      store.toast(codeText(errorOf(e).code), "error");
    }
  }

  const activity = () => store.activity();
</script>

<svelte:window onpointerdown={activity} onkeydown={activity} onwheel={activity} />

<Icons />
{#if !store.booted}
  <div class="splash"><div class="brand"><svg class="mark i" viewBox="0 0 20 20"><use href="#i-mark" /></svg>Enfold</div></div>
{:else if showLock}
  <Lock />
{:else}
  <div class="shell">
    <Rail />
    <div class="layer">
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
  </div>
{/if}

{#if reveal}
  {#key reveal}
    <RecoveryReveal url={reveal} ondone={() => { store.dismissReveal(reveal); void store.refreshSlots(); }} />
  {/key}
{/if}

{#if expiring}
  <Dialog title="Unsaved changes are waiting" onclose={() => (store.expiring = null)}>
    <p>{expiringName} has {expiring.dirty} unsaved change{expiring.dirty === 1 ? "" : "s"} and has been idle. It closes at {dateTime(expiring.closesAt).slice(11)} unless you keep it open or save.</p>
    {#snippet actions()}
      <button type="button" class="btn" onclick={keepOpen}>Keep open</button>
      <button type="button" class="btn accent" disabled={!store.unlocked} onclick={saveNow}>Save now</button>
    {/snippet}
  </Dialog>
{/if}

<Toasts />

<style>
  .splash { height: 100%; display: flex; align-items: center; justify-content: center; color: var(--ink-3); }
</style>

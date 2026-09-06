<script lang="ts">
  import { VaultState } from "./lib/api";
  import { store } from "./lib/state.svelte";
  import Icons from "./components/Icons.svelte";
  import Toasts from "./components/Toasts.svelte";
  import Rail from "./components/Rail.svelte";
  import Lock from "./components/Lock.svelte";
  import ArchivesPage from "./components/ArchivesPage.svelte";
  import ArchivePage from "./components/ArchivePage.svelte";
  import KeysPage from "./components/KeysPage.svelte";
  import SettingsPage from "./components/SettingsPage.svelte";

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
    if (store.route === "lock") return true;
    if (st.state === VaultState.StateUnlocked) return false;
    return st.openArchives === 0;
  });

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
<Toasts />

<style>
  .splash { height: 100%; display: flex; align-items: center; justify-content: center; color: var(--ink-3); }
</style>

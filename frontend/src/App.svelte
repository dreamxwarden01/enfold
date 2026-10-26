<script lang="ts">
  import { CeremonyStep, Code, Shell, VaultState, errorOf } from "./lib/api";
  import { store } from "./lib/state.svelte";
  import { bootCopy, codeText } from "./lib/strings";
  import Icons from "./components/Icons.svelte";
  import NoticeSlot from "./components/NoticeSlot.svelte";
  import Rail from "./components/Rail.svelte";
  import Lock from "./components/Lock.svelte";
  import ArchivesPage from "./components/ArchivesPage.svelte";
  import ArchivePage from "./components/ArchivePage.svelte";
  import KeysPage from "./components/KeysPage.svelte";
  import SettingsPage from "./components/SettingsPage.svelte";
  import RecoveryReveal from "./components/RecoveryReveal.svelte";
  import CloseDialog from "./components/CloseDialog.svelte";
  import OpenKeyDialog from "./components/OpenKeyDialog.svelte";
  import LayerFoot from "./components/LayerFoot.svelte";
  import ResultsDialog from "./components/ResultsDialog.svelte";
  import MergeDialog from "./components/MergeDialog.svelte";
  import { fade, fly } from "svelte/transition";
  import { motion, delay, enter, GAP, OUT, MOVE } from "./lib/motion";
  import { ESC_OWNERS, dialogIsUp } from "./lib/dialogs";
  import { escCloses } from "./lib/infobar";
  import { isTextEntry } from "./lib/listkeys";

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
    // Settings, opened from the lock scene's gear: it needs no vault
    // (APP.md §6, ruled 2026-10-17), so the scene steps aside for it.
    if (store.settingsLocked && store.route === "settings") return false;
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

  // The boot failure's ways on (APP.md §2.4; direction 9, ruled
  // 2026-10-18). Retry has the focus, without a ring until the keyboard
  // is used, as the lock screen's first button has.
  function focusFirst(node: HTMLElement) {
    node.focus({ preventScroll: true, focusVisible: false } as FocusOptions);
  }
  // The data folder holds the log the sentence points at; the shell opens
  // its own folder, so the page names no path of the machine's.
  async function openDataFolder() {
    try {
      await Shell.OpenDataFolder();
    } catch (e) {
      store.notice(codeText(errorOf(e).code), "error");
    }
  }
  // The InfoBars' time waits while the window does not have the focus or is
  // hidden (direction 4; lib/infobar.ts), so a bar is never gone before it
  // could be read. Told by the window's own focus and blur, not read from
  // document.hasFocus() at boot: a WebView that starts without the focus
  // inside an active window would otherwise hold every bar until a click.
  function away(blurred = false) {
    store.pauseNotices({ away: blurred || document.visibilityState === "hidden" });
  }

  // Esc closes the newest error InfoBar (direction 4; APP.md §6, Notices) —
  // only when nothing else owns the key: no dialog, menu, open popover
  // (the rail's flyout, the foot's flyouts) or viewer is up and the focus
  // is not in a field (lib/infobar.ts escCloses, lib/dialogs.ts
  // ESC_OWNERS — the one order). It is taken before the page's own
  // listeners (the capture phase, and prevented, which the list's keymap
  // respects), so a list's Esc clears its selection only once no error is
  // left to close; otherwise the key falls through untouched.
  function escNotice(e: KeyboardEvent) {
    if (e.key !== "Escape" || e.defaultPrevented || e.isComposing || e.ctrlKey || e.altKey || e.shiftKey || e.metaKey) return;
    const t = e.target instanceof HTMLElement ? e.target : null;
    const n = escCloses(store.notices, {
      dialogUp: dialogIsUp(),
      ownerUp: document.querySelector(ESC_OWNERS) !== null,
      inField: !!t && isTextEntry(t.tagName, t.getAttribute("type") ?? "", t.isContentEditable),
    });
    if (!n) return;
    e.preventDefault();
    store.dismissNotice(n.id);
  }

  // What a report needs: the heading, the code and when it failed.
  async function copyDetails() {
    try {
      await navigator.clipboard.writeText(bootCopy.details(store.bootCode, new Date(store.bootFailedAt || Date.now()).toISOString()));
      store.notice(bootCopy.copied, "success");
    } catch {
      store.notice(bootCopy.copyFailed, "error");
    }
  }
</script>

<svelte:window onpointerdown={activity} onkeydown={activity} onwheel={activity} onkeydowncapture={escNotice} onfocus={() => away()} onblur={() => away(true)} />
<svelte:document onvisibilitychange={() => away()} />

<Icons />
<!-- Nothing is drawn until the first status is accepted (APP.md §2.4):
     the splash, then the scene that status names — never the lock scene
     on no evidence. A first status that failed is a panel where the lock
     screen's stands (direction 9): the brand, the heading, the code's
     sentence and three ways on — and nothing of the vault. -->
{#if !store.booted}
  <div class="splash">
    {#if store.bootFailed}
      <!-- Its InfoBars (Copy details, Open data folder) stand where the lock
           screen's do: at the top of the panel's column, above it. -->
      <div class="boot-stack">
      <NoticeSlot flow />
      <div class="boot-failed" role="alert">
        <div class="brand"><svg class="mark i" viewBox="0 0 20 20"><use href="#i-mark" /></svg>Enfold</div>
        <h1 class="t-title">{bootCopy.title}</h1>
        <p>{store.bootFailed}</p>
        <div class="row">
          <button type="button" class="btn accent" onclick={() => void store.refreshAll()} use:focusFirst>{bootCopy.retry}</button>
          <button type="button" class="btn" onclick={() => void openDataFolder()}>{bootCopy.openFolder}</button>
          <button type="button" class="btn" onclick={() => void copyDetails()}>{bootCopy.copyDetails}</button>
        </div>
      </div>
      </div>
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
    <RecoveryReveal url={reveal} kind={store.ceremony?.kind ?? ""} vaultName={store.vaultName} recoveryId={store.ceremony?.recoveryId ?? ""} ondone={() => { store.dismissReveal(reveal); void store.refreshSlots(); }} />
  {/key}
{/if}

<!-- The close question (APP.md §2.4): above every route, and above the
     splash too — a close while the first status is still on its way
     deserves the same answer. -->
{#if store.closeAsked}
  <CloseDialog busy={store.closeBusy} onclose={() => store.dismissClose()} ondecide={(a, r) => void store.decideClose(a, r)} />
{/if}

<!-- A file opened from Explorer whose key is not in this vault (APP.md
     §14): above every route, since Explorer can reach the page on any of
     them, and the lock scene included — the vault was unlocked to get
     here. -->
{#if store.keyNotInVault}
  <OpenKeyDialog path={store.keyNotInVault} onclose={() => (store.keyNotInVault = null)} onimport={() => store.askImportRecords()} />
{/if}

<!-- What a batch left out (APP.md §3; direction 4): above every route,
     since the done line's Details opens it from any page. It waits behind
     the questions of the same op — the conflicts, the refused names — which
     the archive's page asks first. -->
{#if store.results && !store.conflictQuestion && !store.refusedQuestion}
  <ResultsDialog op={store.results} onclose={() => (store.results = null)} />
{/if}

<!-- *Import archives from a backup…* (APP.md §13): above every route,
     since the Vault page and the Archives page both open it (direction 10,
     phase 4a; store.merging). Closed after a merge, the store has already
     opened the file that led here, if one did (store.mergeDone); closed
     without one, it forgets it. A lock closes it with the merge's handle. -->
{#if store.merging}
  <MergeDialog path={store.merging} onclose={() => { store.merging = ""; store.forgetImport(); }} />
{/if}

<style>
  .splash { height: 100%; display: flex; align-items: center; justify-content: center; color: var(--ink-2); }
  /* The boot failure's panel stands where the lock screen's does: 440 px,
     its top at 14vh, 28 px in, a flat 1 px stroke (app.css .unlock .panel;
     APP.md §6). The splash's centring gives way to it. */
  .splash:has(.boot-failed) { align-items: flex-start; padding: 0 var(--s6); }
  /* The panel's column, its top at 14vh: the notice slot first, empty but
     for the InfoBars, in flow above the panel (app.css .nslot.flow). */
  .boot-stack { width: min(var(--stage-w), 100%); margin-top: var(--stage-top); display: flex; flex-direction: column; }
  .boot-failed {
    width: 100%; box-sizing: border-box; padding: calc(var(--s6) + var(--s1));
    display: flex; flex-direction: column; gap: var(--s3); color: var(--ink);
    background: var(--surface); border: 1px solid var(--stroke); border-radius: var(--r-card);
  }
  /* The brand as the lock scene's header draws it, without the rail's
     inset: it heads the panel. */
  .boot-failed .brand { padding: 0 0 var(--s1); }
  .boot-failed p { margin: 0; color: var(--ink-2); }
  .boot-failed .row { flex-wrap: wrap; margin-top: var(--s1); }
</style>

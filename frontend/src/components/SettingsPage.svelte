<script lang="ts">
  import { Settings, errorOf } from "../lib/api";
  import type { SettingsView } from "../lib/api";
  import { store } from "../lib/state.svelte";
  import { codeText } from "../lib/strings";
  import { minutesLabel } from "../lib/format";

  const s = $derived(store.settings);
  const unlocked = $derived(store.unlocked);

  const idleChoices = [0, 2, 5, 10, 15, 20, 30];
  const absChoices = [0, 30, 60, 120, 240, 480];

  async function set(patch: Partial<SettingsView>) {
    if (!s) return;
    const next = { ...s, ...patch };
    try {
      await Settings.Set(next);
      await store.refreshSettings();
    } catch (e) {
      store.toast(codeText(errorOf(e).code), "error");
      await store.refreshSettings();
    }
  }

  function num(e: Event): number {
    return Number((e.currentTarget as HTMLSelectElement | HTMLInputElement).value);
  }
  function str(e: Event): string {
    return (e.currentTarget as HTMLSelectElement | HTMLInputElement).value;
  }

  // The foot's note (LayerFoot).
  $effect(() => {
    store.footNote = "Settings are machine-local; the session timeouts live in the vault.";
  });
</script>

<div class="layer-head"><h1 class="t-title">Settings</h1></div>

<div class="layer-body">
  {#if s}
    <div class="ks-cols">
      <div class="ks-col">
        <div class="ks-head"><h2 class="t-section">Session</h2><span class="t-quiet">{unlocked ? "stored in the vault" : "unlock to change"}</span></div>
        <div class="card setcard">
          <div class="setrow">
            <div class="lab"><b>Idle lock</b><span>No mouse or keyboard. Never off; at most 30 minutes.</span></div>
            <div class="ctl"><select class="input" disabled={!s.timeoutsAdjustable} value={s.idleMinutes} onchange={(e) => set({ idleMinutes: num(e) })}>
              {#each idleChoices as m (m)}<option value={m}>{m === 0 ? "default (10 minutes)" : minutesLabel(m)}</option>{/each}
            </select></div>
          </div>
          <div class="setrow">
            <div class="lab"><b>Absolute lock</b><span>Time since unlocking, whatever happens. At most 8 hours.</span></div>
            <div class="ctl"><select class="input" disabled={!s.timeoutsAdjustable} value={s.absoluteMinutes} onchange={(e) => set({ absoluteMinutes: num(e) })}>
              {#each absChoices as m (m)}<option value={m}>{m === 0 ? "default (1 hour)" : minutesLabel(m)}</option>{/each}
            </select></div>
          </div>
          <div class="setrow">
            <div class="lab"><b>When the window closes</b><span>Enfold stays in the tray either way.</span></div>
            <div class="ctl"><select class="input" value={s.closeToTray} onchange={(e) => set({ closeToTray: str(e) })}>
              <option value="destroy">Free the window's memory</option>
              <option value="hide">Keep the window in memory</option>
            </select></div>
          </div>
        </div>

        <div class="ks-head top"><h2 class="t-section">Exports</h2></div>
        <div class="card setcard">
          <div class="setrow">
            <div class="lab"><b>Recovery record</b><span>Repairs an exported volume damaged in storage. Costs space.</span></div>
            <div class="ctl row"><input type="range" min="1" max="20" value={s.recoveryRecordPct} onchange={(e) => set({ recoveryRecordPct: num(e) })} /><span class="num pct">{s.recoveryRecordPct}%{s.recoveryRecordPct === 3 ? " (default)" : ""}</span></div>
          </div>
          <div class="setrow">
            <div class="lab"><b>Dictionary for small files</b><span>Files below this size share a compression dictionary.</span></div>
            <div class="ctl"><select class="input" value={s.dictionaryBelow} onchange={(e) => set({ dictionaryBelow: num(e) })}>
              <option value={0}>off</option>
              <option value={65536}>64 KB</option>
              <option value={262144}>256 KB</option>
              <option value={1048576}>1 MB</option>
              <option value={4194304}>4 MB</option>
            </select></div>
          </div>
        </div>
      </div>

      <div class="ks-col">
        <div class="ks-head"><h2 class="t-section">Appearance</h2></div>
        <div class="card setcard">
          <div class="setrow">
            <div class="lab"><b>Theme</b><span>The window's own theme follows at the next open.</span></div>
            <div class="ctl"><select class="input" value={s.theme} onchange={(e) => set({ theme: str(e) })}>
              <option value="system">Follow Windows</option>
              <option value="light">Light</option>
              <option value="dark">Dark</option>
            </select></div>
          </div>
          <div class="setrow">
            <div class="lab"><b>Look</b><span>Native is the only look in this version.</span></div>
            <div class="ctl"><span class="chip">Native</span></div>
          </div>
        </div>
        <div class="ks-head top"><h2 class="t-section">About</h2></div>
        <div class="card setcard">
          <div class="setrow"><div class="lab"><b>Enfold 0.1.0</b><span>Compression and encryption archives, unlocked by a YubiKey.</span></div></div>
          <div class="setrow"><div class="lab"><b>Vault</b><span class="ellipsis" title={s.vaultPath || store.status?.defaultPath}>{s.vaultPath ? `${s.vaultPath} — kept elsewhere` : store.status?.defaultPath ?? ""}</span></div></div>
          <div class="setrow"><div class="lab"><b>Data folder</b><span>%LOCALAPPDATA%\Enfold — the vault, settings and the log.</span></div></div>
        </div>
      </div>
    </div>
  {/if}
</div>


<style>
  .ks-head.top { margin-top: 12px; }
  .pct { min-width: 92px; text-align: right; }
  input[type="range"] { accent-color: var(--accent); width: 140px; }
</style>

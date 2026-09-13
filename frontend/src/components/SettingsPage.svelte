<script lang="ts">
  import { Settings, errorOf } from "../lib/api";
  import type { SettingsView } from "../lib/api";
  import { store } from "../lib/state.svelte";
  import { codeText } from "../lib/strings";
  import { minutesLabel } from "../lib/format";
  import { closeActionLabel, closeChoices } from "../lib/closing";
  import SaveBar from "./SaveBar.svelte";
  import type { PendingItem } from "./SaveBar.svelte";

  const s = $derived(store.settings);
  const unlocked = $derived(store.unlocked);

  const idleChoices = [0, 2, 5, 10, 15, 20, 30];
  const absChoices = [0, 30, 60, 120, 240, 480];
  const dictChoices: [number, string][] = [[0, "off"], [65536, "64 KB"], [262144, "256 KB"], [1048576, "1 MB"], [4194304, "4 MB"]];

  // The staged edits live in the store (they survive a visit to another
  // page); the page shows saved ⊕ draft and diffs the two for the bar
  // (APP.md §6, the save bar): dirty is derived, never stored, so an edit
  // put back by hand un-dirties itself.
  type Key = "displayName" | "idleMinutes" | "absoluteMinutes" | "closeAction" | "recoveryRecordPct" | "dictionaryBelow" | "theme";
  const draft = $derived({ ...(s ?? ({} as SettingsView)), ...(store.settingsDraft as Partial<SettingsView>) } as SettingsView);

  function edit<K extends Key>(key: K, value: SettingsView[K]) {
    if (!s) return;
    if (value === s[key]) delete store.settingsDraft[key];
    else store.settingsDraft[key] = value;
  }

  // Only what can be changed counts: the timeouts while unlocked.
  function editable(key: Key): boolean {
    if (key === "idleMinutes" || key === "absoluteMinutes") return !!s?.timeoutsAdjustable;
    return true;
  }

  const labels: Record<Key, string> = {
    displayName: "Vault name",
    idleMinutes: "Idle lock",
    absoluteMinutes: "Absolute lock",
    closeAction: "When the window closes",
    recoveryRecordPct: "Recovery record",
    dictionaryBelow: "Dictionary",
    theme: "Theme",
  };

  function shown(key: Key, v: unknown): string {
    switch (key) {
      case "idleMinutes": return v === 0 ? "default (10 minutes)" : minutesLabel(v as number);
      case "absoluteMinutes": return v === 0 ? "default (1 hour)" : minutesLabel(v as number);
      case "closeAction": return closeActionLabel(v);
      case "recoveryRecordPct": return `${v}%`;
      case "dictionaryBelow": return dictChoices.find((c) => c[0] === v)?.[1] ?? String(v);
      case "theme": return v === "system" ? "follow Windows" : String(v);
      case "displayName": return String(v).trim() ? `“${String(v).trim()}”` : "(empty)";
    }
  }

  // Whether a staged value differs from the saved one: the name compares
  // trimmed, since spaces at its ends are allowed while typing and
  // stripped when saved — never a change on their own, never invalid.
  function differs(k: Key): boolean {
    if (!s) return false;
    const v = store.settingsDraft[k];
    if (k === "displayName") return String(v).trim() !== s.displayName;
    return v !== s[k];
  }

  const items = $derived.by((): PendingItem[] => {
    if (!s) return [];
    return (Object.keys(store.settingsDraft) as Key[])
      .filter((k) => editable(k) && differs(k))
      .map((k) => ({ key: k, label: `${labels[k]} · ${shown(k, store.settingsDraft[k])}` }));
  });

  // Invalid input greys Save and says why — in the bar, and at the field.
  const nameBad = $derived("displayName" in store.settingsDraft && draft.displayName.trim() === "");
  const invalid = $derived(nameBad ? "Give the vault a name to save." : "");

  let busy = $state(false);

  async function save() {
    if (!s || busy || invalid) return;
    busy = true;
    const next: SettingsView = { ...draft, displayName: draft.displayName.trim() };
    try {
      await Settings.Set(next);
      await store.refreshSettings();
      store.settingsDraft = {}; // the bar's leaving is the confirmation; no toast
    } catch (e) {
      // The draft stays: the user can press Save again.
      store.toast(codeText(errorOf(e).code), "error");
    } finally {
      busy = false;
    }
  }

  function discard() {
    store.settingsDraft = {};
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
            <div class="ctl"><select class="input" disabled={!s.timeoutsAdjustable} value={draft.idleMinutes} onchange={(e) => edit("idleMinutes", num(e))}>
              {#each idleChoices as m (m)}<option value={m}>{m === 0 ? "default (10 minutes)" : minutesLabel(m)}</option>{/each}
            </select></div>
          </div>
          <div class="setrow">
            <div class="lab"><b>Absolute lock</b><span>Time since unlocking, whatever happens. At most 8 hours.</span></div>
            <div class="ctl"><select class="input" disabled={!s.timeoutsAdjustable} value={draft.absoluteMinutes} onchange={(e) => edit("absoluteMinutes", num(e))}>
              {#each absChoices as m (m)}<option value={m}>{m === 0 ? "default (1 hour)" : minutesLabel(m)}</option>{/each}
            </select></div>
          </div>
          <div class="setrow">
            <div class="lab"><b>When the window closes</b><span>The close button asks until this says otherwise. Going to the tray leaves every archive's page, as closing the window always does; Enfold reopens from the tray at the list.</span></div>
            <div class="ctl"><select class="input" value={draft.closeAction} onchange={(e) => edit("closeAction", str(e))}>
              {#each closeChoices as [v, label] (v)}<option value={v}>{label}</option>{/each}
            </select></div>
          </div>
        </div>

        <div class="ks-head top"><h2 class="t-section">Exports</h2></div>
        <div class="card setcard">
          <div class="setrow">
            <div class="lab"><label for="recovery-pct"><b>Recovery record</b></label><span>Parity kept beside an exported volume, so damage in storage can be repaired without a key. Costs the same share of space. Recommended: 3%; 0% is off. Applies to exports made from now on.</span></div>
            <div class="ctl row slider"><input id="recovery-pct" type="range" min="0" max="20" value={draft.recoveryRecordPct} oninput={(e) => edit("recoveryRecordPct", num(e))} /><span class="num pct">{draft.recoveryRecordPct}%</span></div>
          </div>
          <div class="setrow">
            <div class="lab"><b>Dictionary for small files</b><span>Files below this size share a compression dictionary.</span></div>
            <div class="ctl"><select class="input" value={draft.dictionaryBelow} onchange={(e) => edit("dictionaryBelow", num(e))}>
              {#each dictChoices as [v, label] (v)}<option value={v}>{label}</option>{/each}
            </select></div>
          </div>
        </div>
      </div>

      <div class="ks-col">
        <div class="ks-head"><h2 class="t-section">Appearance</h2></div>
        <div class="card setcard">
          <div class="setrow">
            <div class="lab"><b>Theme</b><span>The window's own theme follows at the next open.</span></div>
            <div class="ctl"><select class="input" value={draft.theme} onchange={(e) => edit("theme", str(e))}>
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

        <div class="ks-head top"><h2 class="t-section">Vault</h2></div>
        <div class="card setcard">
          <div class="setrow">
            <div class="lab"><label for="vault-name"><b>Name</b></label><span>How this vault is called on the lock screen and in the tray.</span></div>
            <div class="ctl"><input id="vault-name" class="input" type="text" maxlength="60" value={draft.displayName} aria-invalid={nameBad || undefined} oninput={(e) => edit("displayName", str(e))} /></div>
          </div>
          <div class="setrow"><div class="lab"><b>File</b><span class="ellipsis" title={s.vaultPath || store.status?.defaultPath}>{s.vaultPath ? `${s.vaultPath} — kept elsewhere` : store.status?.defaultPath ?? ""}</span></div></div>
        </div>

        <div class="ks-head top"><h2 class="t-section">About</h2></div>
        <div class="card setcard">
          <div class="setrow"><div class="lab"><b>Enfold 0.1.0</b><span>Compression and encryption archives, unlocked by a YubiKey.</span></div></div>
          <div class="setrow"><div class="lab"><b>Data folder</b><span>%LOCALAPPDATA%\Enfold — the vault, settings and the log.</span></div></div>
        </div>
      </div>
    </div>
    <SaveBar {items} {invalid} {busy} onsave={save} ondiscard={discard} />
  {/if}
</div>


<style>
  .ks-head.top { margin-top: 12px; }
  .ctl.slider { gap: 6px; }
  .ctl.slider input[type="range"] { accent-color: var(--accent); width: 140px; margin: 0; }
  .pct { min-width: 30px; text-align: right; }
  .lab label { cursor: default; }
</style>

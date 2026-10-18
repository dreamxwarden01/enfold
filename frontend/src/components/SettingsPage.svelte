<script lang="ts">
  import { Settings, errorOf } from "../lib/api";
  import type { SettingsView } from "../lib/api";
  import { store } from "../lib/state.svelte";
  import { codeText, settingsCopy, vaultCopy } from "../lib/strings";
  import { minutesLabel } from "../lib/format";
  import { closeActionLabel, closeChoices } from "../lib/closing";
  import SaveBar from "./SaveBar.svelte";
  import TamperedBar from "./TamperedBar.svelte";
  import type { PendingItem } from "./SaveBar.svelte";

  const s = $derived(store.settings);
  const unlocked = $derived(store.unlocked);

  const idleChoices = [0, 2, 5, 10, 15, 20, 30];
  const absChoices = [0, 30, 60, 120, 240, 480];
  // The recovery record's slider runs to 20%; the track's accent part is
  // the value's share of that, which the page states (app.css, the slider).
  const PCT_MAX = 20;
  const dictChoices: [number, string][] = [[0, "Off"], [65536, "64 KB"], [262144, "256 KB"], [1048576, "1 MB"], [4194304, "4 MB"]];

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

  // The bar names a change by its row's own name (APP.md §6, the save
  // bar; ruled 2026-10-17).
  const labels: Record<Key, string> = {
    displayName: settingsCopy.vaultName,
    idleMinutes: settingsCopy.idleLock,
    absoluteMinutes: settingsCopy.absoluteLock,
    closeAction: settingsCopy.closeAction,
    recoveryRecordPct: settingsCopy.repairData,
    dictionaryBelow: settingsCopy.dictionary,
    theme: settingsCopy.theme,
  };

  // A timeout's value as its select shows it: the default says so after
  // the time it stands for.
  const IDLE_DEFAULT = 10;
  const ABS_DEFAULT = 60;
  function timeoutLabel(m: number, fallback: number): string {
    return m === 0 ? settingsCopy.defaultValue(minutesLabel(fallback)) : minutesLabel(m);
  }

  // The recovery record's row is hidden until volume exports exist (ruled
  // 2026-10-17): today nothing reads the setting, and an exported backup
  // carries no parity. The setting keeps its storage; the row comes back
  // as *Repair data* with them.
  const repairDataShown = false;

  function shown(key: Key, v: unknown): string {
    switch (key) {
      case "idleMinutes": return timeoutLabel(v as number, IDLE_DEFAULT);
      case "absoluteMinutes": return timeoutLabel(v as number, ABS_DEFAULT);
      case "closeAction": return closeActionLabel(v);
      case "recoveryRecordPct": return `${v}%`;
      case "dictionaryBelow": return dictChoices.find((c) => c[0] === v)?.[1] ?? String(v);
      case "theme": return v === "system" ? "Follow Windows" : v === "dark" ? "Dark" : v === "light" ? "Light" : String(v);
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

  // The foot's note (LayerFoot): the one place the page says which
  // settings are this machine's and which the vault's.
  $effect(() => {
    store.footNote = settingsCopy.foot;
  });
</script>

<div class="layer-head"><h1 class="t-title">{settingsCopy.title}</h1></div>

<div class="layer-body">
  {#if store.tampered}
    <!-- First on every page (direction 9; APP.md §13), Settings opened
         from the lock screen's gear included: these settings are this
         PC's, but the vault beside them may have been changed outside
         Enfold, and the fix is one press away. -->
    <TamperedBar reason={store.status?.tamperedReason} />
  {/if}
  {#if s}
    <div class="ks-cols">
      <div class="ks-col">
        <!-- Which settings live where is the foot's to say, once; the head
             says only what stands in the way of changing these. -->
        <div class="ks-head"><h2 class="t-section">{settingsCopy.session}</h2>{#if !unlocked}<span class="t-quiet">{settingsCopy.unlockToChange}</span>{/if}</div>
        <div class="setcard">
          <div class="setrow">
            <div class="lab"><b>{settingsCopy.idleLock}</b><span>{settingsCopy.idleLine(minutesLabel(draft.idleMinutes || IDLE_DEFAULT))}</span></div>
            <div class="ctl"><select class="input" disabled={!s.timeoutsAdjustable} value={draft.idleMinutes} onchange={(e) => edit("idleMinutes", num(e))}>
              {#each idleChoices as m (m)}<option value={m}>{timeoutLabel(m, IDLE_DEFAULT)}</option>{/each}
            </select></div>
          </div>
          <div class="setrow">
            <div class="lab"><b>{settingsCopy.absoluteLock}</b><span>{settingsCopy.absoluteLine}</span></div>
            <div class="ctl"><select class="input" disabled={!s.timeoutsAdjustable} value={draft.absoluteMinutes} onchange={(e) => edit("absoluteMinutes", num(e))}>
              {#each absChoices as m (m)}<option value={m}>{timeoutLabel(m, ABS_DEFAULT)}</option>{/each}
            </select></div>
          </div>
          <div class="setrow">
            <div class="lab"><b>{settingsCopy.closeAction}</b><span>The close button asks until this says otherwise. Going to the tray leaves every archive's page, as closing the window always does; Enfold reopens from the tray at the list.</span></div>
            <div class="ctl"><select class="input" value={draft.closeAction} onchange={(e) => edit("closeAction", str(e))}>
              {#each closeChoices as [v, label] (v)}<option value={v}>{label}</option>{/each}
            </select></div>
          </div>
        </div>

        <!-- *Compression*, not *Exports*: with the recovery record's row
             hidden the section holds the dictionary alone (APP.md §6,
             ruled 2026-10-17). -->
        <div class="ks-head top"><h2 class="t-section">{settingsCopy.compression}</h2></div>
        <div class="setcard">
          {#if repairDataShown}
          <div class="setrow">
            <div class="lab"><label for="recovery-pct"><b>{settingsCopy.repairData}</b></label><span>{settingsCopy.repairDataLine} Costs the same share of space; 3% is recommended, 0% is off.</span></div>
            <div class="ctl row slider"><input id="recovery-pct" type="range" min="0" max={PCT_MAX} value={draft.recoveryRecordPct} style="--range-fill: {(draft.recoveryRecordPct / PCT_MAX) * 100}%" oninput={(e) => edit("recoveryRecordPct", num(e))} /><span class="num pct">{draft.recoveryRecordPct}%</span></div>
          </div>
          {/if}
          <div class="setrow">
            <div class="lab"><b>{settingsCopy.dictionary}</b><span>{settingsCopy.dictionaryLine}</span></div>
            <div class="ctl"><select class="input" value={draft.dictionaryBelow} onchange={(e) => edit("dictionaryBelow", num(e))}>
              {#each dictChoices as [v, label] (v)}<option value={v}>{label}</option>{/each}
            </select></div>
          </div>
        </div>
      </div>

      <div class="ks-col">
        <div class="ks-head"><h2 class="t-section">{settingsCopy.appearance}</h2></div>
        <div class="setcard">
          <div class="setrow">
            <div class="lab"><b>{settingsCopy.theme}</b><span>{settingsCopy.themeLine}</span></div>
            <div class="ctl"><select class="input" value={draft.theme} onchange={(e) => edit("theme", str(e))}>
              <option value="system">Follow Windows</option>
              <option value="light">Light</option>
              <option value="dark">Dark</option>
            </select></div>
          </div>
        </div>

        <div class="ks-head top"><h2 class="t-section">{settingsCopy.vault}</h2></div>
        <div class="setcard">
          <div class="setrow">
            <div class="lab"><label for="vault-name"><b>{settingsCopy.vaultName}</b></label><span>{settingsCopy.vaultNameLine}</span></div>
            <div class="ctl"><input id="vault-name" class="input" type="text" maxlength="60" value={draft.displayName} aria-invalid={nameBad || undefined} oninput={(e) => edit("displayName", str(e))} /></div>
          </div>
          <!-- A path is a fact, its label over it and never cut (APP.md §6). -->
          <div class="setrow"><dl class="kv stk grow"><dt>{vaultCopy.file}</dt><dd>{s.vaultPath ? `${s.vaultPath} — kept elsewhere` : store.status?.defaultPath ?? ""}</dd></dl></div>
        </div>

        <div class="ks-head top"><h2 class="t-section">{settingsCopy.about}</h2></div>
        <div class="setcard">
          <div class="setrow"><div class="lab"><b>Enfold 0.1.0</b><span>{settingsCopy.aboutLine}</span></div></div>
          <div class="setrow"><dl class="kv stk grow"><dt>Data folder</dt><dd>%LOCALAPPDATA%\Enfold — the vault, settings and the log.</dd></dl></div>
        </div>
      </div>
    </div>
    <SaveBar {items} {invalid} {busy} onsave={save} ondiscard={discard} />
  {/if}
</div>


<style>
  .ks-head.top { margin-top: var(--s3); }
  .ctl.slider { gap: var(--s2); }
  .ctl.slider input[type="range"] { width: 140px; }
  .pct { min-width: 30px; text-align: right; }
  .lab label { cursor: default; }
</style>

<script lang="ts">
  // Settings is this PC only (direction 10, phase 4a; APP.md §6): the
  // window's close, the theme, the compression dictionary and About. The
  // lock times and the vault's name are the vault's and live on the Vault
  // page. Every row is a choice that applies the moment it is made, as
  // Windows Settings applies one — no save bar (SaveBar's one job), no
  // toast on success: a choice that applied is its own confirmation. A
  // refusal is said under the control, and the old value comes back
  // (store.choose). One column at most 760 px wide, every control 220 px,
  // so the controls' column is one straight edge. The page works from the
  // lock screen's gear too (store.settingsLocked): nothing here needs the
  // vault.
  import { store } from "../lib/state.svelte";
  import { settingsCopy } from "../lib/strings";
  import { closeChoices } from "../lib/closing";
  import { pending } from "../lib/pending";
  import TamperedBar from "./TamperedBar.svelte";

  const s = $derived(store.settings);

  // The recovery record's slider runs to 20%; the track's accent part is
  // the value's share of that, which the page states (app.css, the slider).
  const PCT_MAX = 20;
  const dictChoices: [number, string][] = [[0, settingsCopy.dictionaryOff], [65536, "64 KB"], [262144, "256 KB"], [1048576, "1 MB"], [4194304, "4 MB"]];

  // The recovery record's row is hidden until volume exports exist (ruled
  // 2026-10-17): today nothing reads the setting, and an exported backup
  // carries no parity. The setting keeps its storage; the row comes back
  // as *Repair data* with them.
  const repairDataShown = false;

  // The close action as a radio reads it: a value that is none of the
  // three reads as the default, as the core reads it (lib/closing.ts).
  const closeAction = $derived(closeChoices.some(([v]) => v === s?.closeAction) ? s?.closeAction : closeChoices[0][0]);

  function num(e: Event): number {
    return Number((e.currentTarget as HTMLSelectElement | HTMLInputElement).value);
  }
  function str(e: Event): string {
    return (e.currentTarget as HTMLSelectElement).value;
  }

  // The foot's note (LayerFoot): whose settings these are, and nothing
  // about the vault's, which are on their own page now.
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
    <div class="spage">
      <section class="ssec" aria-labelledby="st-window">
        <div class="ks-head"><h2 id="st-window" class="t-section">{settingsCopy.window}</h2></div>
        <div class="setcard">
          <!-- Three choices, all shown: radios rather than a select
               (direction 10), applied at once. -->
          <div class="setrow top">
            <div class="lab"><b id="st-close">{settingsCopy.closeAction}</b><span>{settingsCopy.closeActionLine}</span></div>
            <div class="ctl choice">
              <div class="radios" role="radiogroup" aria-labelledby="st-close">
                {#each closeChoices as [v, label] (v)}
                  <label class="check"><input type="radio" name="close-action" value={v} checked={closeAction === v} onchange={() => void store.choose("closeAction", v)} />{label}</label>
                {/each}
              </div>
              {#if store.choiceRefused.closeAction}<span class="field-error" role="alert">{store.choiceRefused.closeAction}</span>{/if}
            </div>
          </div>
        </div>
      </section>

      <section class="ssec" aria-labelledby="st-appearance">
        <div class="ks-head"><h2 id="st-appearance" class="t-section">{settingsCopy.appearance}</h2></div>
        <div class="setcard">
          <div class="setrow">
            <div class="lab"><b id="st-theme">{settingsCopy.theme}</b><span>{settingsCopy.themeLine}</span></div>
            <div class="ctl choice">
              <span class="selwrap" use:pending={store.choosing.theme}>
                <select class="input" aria-labelledby="st-theme" value={s.theme} aria-invalid={!!store.choiceRefused.theme || undefined} onchange={(e) => void store.choose("theme", str(e))}>
                  {#each settingsCopy.themeChoices as [v, label] (v)}<option value={v}>{label}</option>{/each}
                </select>
              </span>
              {#if store.choiceRefused.theme}<span class="field-error" role="alert">{store.choiceRefused.theme}</span>{/if}
            </div>
          </div>
        </div>
      </section>

      <!-- *Compression*, not *Exports*: with the recovery record's row
           hidden the section holds the dictionary alone (APP.md §6,
           ruled 2026-10-17). -->
      <section class="ssec" aria-labelledby="st-compression">
        <div class="ks-head"><h2 id="st-compression" class="t-section">{settingsCopy.compression}</h2></div>
        <div class="setcard">
          {#if repairDataShown}
          <div class="setrow">
            <div class="lab"><label for="recovery-pct"><b>{settingsCopy.repairData}</b></label><span>{settingsCopy.repairDataLine}</span></div>
            <div class="ctl row slider"><input id="recovery-pct" type="range" min="0" max={PCT_MAX} value={s.recoveryRecordPct} style="--range-fill: {(s.recoveryRecordPct / PCT_MAX) * 100}%" onchange={(e) => void store.choose("recoveryRecordPct", num(e))} /><span class="num pct">{s.recoveryRecordPct}%</span></div>
          </div>
          {/if}
          <div class="setrow">
            <div class="lab"><b id="st-dict">{settingsCopy.dictionary}</b><span>{settingsCopy.dictionaryLine}</span></div>
            <div class="ctl choice">
              <span class="selwrap" use:pending={store.choosing.dictionaryBelow}>
                <select class="input" aria-labelledby="st-dict" value={s.dictionaryBelow} aria-invalid={!!store.choiceRefused.dictionaryBelow || undefined} onchange={(e) => void store.choose("dictionaryBelow", num(e))}>
                  {#each dictChoices as [v, label] (v)}<option value={v}>{label}</option>{/each}
                </select>
              </span>
              {#if store.choiceRefused.dictionaryBelow}<span class="field-error" role="alert">{store.choiceRefused.dictionaryBelow}</span>{/if}
            </div>
          </div>
        </div>
      </section>

      <section class="ssec" aria-labelledby="st-about">
        <div class="ks-head"><h2 id="st-about" class="t-section">{settingsCopy.about}</h2></div>
        <div class="setcard">
          <div class="setrow"><div class="lab"><b>{settingsCopy.version}</b><span>{settingsCopy.aboutLine}</span></div></div>
          <!-- A path is a fact, its label over it and never cut (APP.md §6). -->
          <div class="setrow"><dl class="kv stk grow"><dt>{settingsCopy.dataFolder}</dt><dd>{settingsCopy.dataFolderLine}</dd></dl></div>
        </div>
      </section>
    </div>
  {/if}
</div>


<style>
  /* One column (direction 10), its sections 24 apart. */
  .spage { display: flex; flex-direction: column; gap: var(--s6); width: 100%; max-width: var(--page-col); padding-bottom: var(--s3); }
  .ssec { display: flex; flex-direction: column; gap: var(--s3); min-width: 0; }
  /* The radios' row reads from the top: its name on the first radio's line. */
  .setrow.top { align-items: flex-start; }
  .ctl.slider { gap: var(--s2); }
  .ctl.slider input[type="range"] { width: 140px; }
  .pct { min-width: 30px; text-align: right; }
  .lab label { cursor: default; }
</style>

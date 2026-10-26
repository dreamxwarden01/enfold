<script lang="ts">
  // The extract dialog (APP.md §3, the extract dialog, ruled 2026-09-10;
  // one Extract… with a scope, direction 7), for the toolbar's *Extract…*,
  // Ctrl+E and the pane's *Extract…* alike. It asks what first: the scope
  // line — the selection, or everything in the archive, opening on the
  // selection when there is one (lib/extract.ts startingScope). Then an
  // editable destination prefilled by one rule per scope — everything to
  // the archive's own folder plus a folder named after the archive, a
  // selection to the archive's own folder, with no extra level — and
  // recomputed when the scope changes until the user has made the field
  // their own (followScope); *Browse…*
  // opening the native picker into it, the four policies with *Ask me
  // about each conflict* the default (ruled 2026-10-14), and *Extract*. No
  // folder is kept from the last time and there is no action that appends
  // a folder: the prefill is the rule; the policy last chosen is kept while
  // Enfold runs (lib/extract.ts). The destination need not exist: the core
  // creates it. The dialog is a form: Enter in the field extracts, and the
  // field has the focus as it opens. The field has no placeholder: an
  // invented path read as where an empty field would extract to, and an
  // empty field extracts nowhere (APP.md §7, ruled 2026-10-17).
  import { untrack } from "svelte";
  import Dialog from "./Dialog.svelte";
  import { fieldMenu } from "../lib/fieldmenu";
  import { Shell } from "../lib/api";
  import { POLICIES, destinationEmpty, destinationForScope, followScope, rememberPolicy, startingPolicy, startingScope } from "../lib/extract";
  import type { ExtractScope } from "../lib/extract";
  import { archivePageCopy, extractCopy } from "../lib/strings";

  interface Props {
    // the archive, for the title and the second radio ("Extract from
    // Photos 2024", ruled 2026-10-17), and its file's path, for the prefill
    archiveName: string;
    archivePath: string;
    // the selection as the pane counts it, files and folders alike; 0
    // greys the first radio
    selectedCount: number;
    onextract: (scope: ExtractScope, dir: string, policy: string) => void;
    oncancel: () => void;
  }
  let { archiveName, archivePath, selectedCount, onextract, oncancel }: Props = $props();

  let scope = $state<ExtractScope>(untrack(() => startingScope(selectedCount)));
  // The field starts on the scope's prefill and follows the scope only
  // while it still holds that prefill: once the user has typed or browsed,
  // it is theirs and a scope change leaves it be.
  let dir = $state(untrack(() => destinationForScope(archivePath, archiveName, scope)));
  let policy = $state(startingPolicy());

  function setScope(to: ExtractScope) {
    if (to === scope) return;
    dir = followScope(dir, scope, to, archivePath, archiveName);
    scope = to;
  }
  // The field is marked once the user has asked for an extract it cannot
  // do — never while they are still typing into an empty field.
  let attempted = $state(false);
  const empty = $derived(destinationEmpty(dir));

  async function browse() {
    const p = await Shell.PickFolder("Extract to");
    if (p) {
      dir = p;
      attempted = false;
    }
  }

  function go() {
    attempted = true;
    if (empty) return;
    rememberPolicy(policy);
    onextract(scope, dir.trim(), policy);
  }
</script>

<Dialog title={extractCopy.title(archiveName)} onclose={oncancel} onsubmit={go}>
  <!-- The scope line (direction 7): what first, then where. With nothing
       selected the first radio is greyed and says why beneath it. -->
  <fieldset class="field choices">
    <legend class="lbl">{extractCopy.scopeLegend}</legend>
    <label class="check" class:off={selectedCount === 0} title={selectedCount === 0 ? archivePageCopy.extractNeedsSome : undefined}>
      <input type="radio" name="xscope" value="selection" checked={scope === "selection"} disabled={selectedCount === 0} onchange={() => setScope("selection")} />
      <span class="two">
        <span>{extractCopy.scopeSelection(selectedCount)}</span>
        {#if selectedCount === 0}<small>{archivePageCopy.extractNeedsSome}</small>{/if}
      </span>
    </label>
    <label class="check">
      <input type="radio" name="xscope" value="all" checked={scope === "all"} onchange={() => setScope("all")} />{extractCopy.scopeAll(archiveName)}
    </label>
  </fieldset>

  <div class="field">
    <div class="field-top"><label for="xd">Destination folder</label><span class="hint">Created if it is not there yet</span></div>
    <div class="row">
      <input
        id="xd" class="input" bind:value={dir}
        class:invalid={attempted && empty}
        aria-invalid={attempted && empty ? "true" : undefined}
        aria-describedby={attempted && empty ? "xd-said" : undefined}
        oninput={() => (attempted = false)}
        use:fieldMenu
      />
      <button type="button" class="btn" onclick={browse}>Browse…</button>
    </div>
    {#if attempted && empty}<div id="xd-said" class="field-error">This field is required.</div>{/if}
  </div>

  <!-- *Ask me about each conflict* is the default (APP.md §3, ruled
       2026-10-14): the one that comes back with a question, so nothing
       already there is overwritten unasked. The last choice is kept while
       Enfold runs. -->
  <fieldset class="field choices">
    <legend class="lbl">{extractCopy.policyLegend}</legend>
    {#each POLICIES as p (p.value)}
      <label class="check"><input type="radio" name="xpolicy" value={p.value} bind:group={policy} />{p.label}</label>
    {/each}
  </fieldset>

  <!-- What an extract makes, said every time (direction 9, ruled
       2026-10-18): an info line in the second ink, no box — a sentence,
       not a gate. -->
  <p class="infoline"><svg class="i i-14" aria-hidden="true"><use href="#i-info" /></svg><span>{extractCopy.ordinaryFiles}</span></p>

  {#snippet actions()}
    <button type="submit" class="btn accent">Extract</button>
    <button type="button" class="btn" onclick={oncancel}>Cancel</button>
  {/snippet}
</Dialog>

<style>
  /* A fieldset that is a .field (APP.md §6, Type and spacing): the options
     stand 8 apart, and the legend — which sits outside the column's gap —
     is a field label, strong, 8 above the first of them. */
  .choices { border: 0; margin: 0; padding: 0; }
  .choices .lbl { font: var(--t-strong); color: var(--ink); padding: 0 0 var(--s2); }
  /* A greyed radio's words are greyed with it, and its reason stands
     under them in caption, as a disabled menu item keeps its reason as a
     second line (direction 7). */
  .check.off { color: var(--ink-dis); align-items: flex-start; }
  .check.off input { margin-top: 3px; }
  .two { display: flex; flex-direction: column; }
  .two small { font: var(--t-cap); color: var(--ink-2); }
  /* The icon 8 from its words, centred on the first 20 px line as a
     bar's is (app.css .bar .i.i-14). */
  .infoline { display: flex; align-items: flex-start; gap: var(--s2); color: var(--ink-2); }
  .infoline .i { margin-top: 3px; }
</style>

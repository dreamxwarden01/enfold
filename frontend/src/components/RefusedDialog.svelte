<script lang="ts">
  // A name the destination refused (APP.md §3, ruled 2026-09-10), asked
  // per refused record: the final name refused on placement shows the
  // name it would write — "Write it as …?", the extension kept, the stem
  // halved (lib/refused.ts shorten) — and offers *Use this name*,
  // *Rename…* for this extract only, and *Skip*, all standard buttons
  // (direction 7); with more names of that kind waiting, a tick "Do the
  // same for the other N" gives Use this name or Skip to them too, never
  // Rename… (applyToRest). How the name is shortened stands behind a
  // disclosure. A refused path — the temporary or a folder, where no name
  // helps — offers *Skip* and *Skip all like it*. Esc is *Skip*; a click
  // outside is nothing (APP.md §6). A name refused again is asked about
  // again, as a new question. When every row has its answer the decisions
  // go back to the page, which re-issues Extract with `names`. The
  // rows are the operation's outcomes; nothing is kept here but what was
  // decided so far, and the page keys this dialog on the operation so a
  // new question starts afresh.
  import { tick } from "svelte";
  import Dialog from "./Dialog.svelte";
  import TextField from "./TextField.svelte";
  import { renameSelection } from "../lib/paths";
  import { applyToRest, nextUndecided, restLike, shorten, skipAllLike } from "../lib/refused";
  import type { RefusedDecision, RefusedRow } from "../lib/refused";
  import { refusedCopy } from "../lib/strings";
  import { fileNameProblem } from "../lib/validate";

  interface Props {
    rows: RefusedRow[];
    ondone: (decisions: Record<string, RefusedDecision>) => void;
  }
  let { rows, ondone }: Props = $props();

  let decisions = $state<Record<string, RefusedDecision>>({});
  const at = $derived(nextUndecided(rows, decisions));
  const row = $derived(at >= 0 ? rows[at] : null);
  const shorter = $derived(row ? shorten(row.leaf) : null);
  // "Do the same for the other N": offered for a refused name while others
  // of its kind wait, off until ticked — each name is asked about unless
  // the user says otherwise.
  const rest = $derived(row && row.kind === "name" ? restLike(rows, decisions, row) : 0);
  let forRest = $state(false);

  // *Rename…* turns the dialog into a name field for this row; *Back*
  // returns to the question.
  let renaming = $state(false);
  let newName = $state("");
  let newNameValid = $state(true);
  let newNameAttempt = $state(0);

  function decide(d: RefusedDecision) {
    if (!row) return;
    renaming = false;
    const next = forRest && rest > 0 ? applyToRest(rows, decisions, row, d) : { ...decisions, [row.id]: d };
    // The tick is this question's: the next one opens with it off, as
    // every question does — after a Rename…, which reached this record
    // alone, the others are still asked about one by one.
    forRest = false;
    settle(next);
  }

  function skipAll() {
    if (!row) return;
    settle(skipAllLike(rows, decisions, row.kind));
  }

  function settle(next: Record<string, RefusedDecision>) {
    decisions = next;
    if (nextUndecided(rows, next) < 0) ondone(next);
  }

  function askRename() {
    if (!row) return;
    newName = row.leaf;
    newNameValid = true;
    newNameAttempt = 0;
    renaming = true;
    // The field arrives after the dialog opened, so it is given the focus
    // here, with the stem selected, as Rename's is (APP.md §6).
    const r = row;
    void tick().then(() => {
      const el = document.getElementById("rf-name") as HTMLInputElement | null;
      if (!el || !renaming) return;
      el.focus();
      el.setSelectionRange(...renameSelection(el.value, r.isDir));
    });
  }

  function rename() {
    newNameAttempt++;
    if (!newNameValid) return;
    decide({ kind: "rename", to: newName.trim() });
  }
</script>

{#if row}
  <!-- While it asks for a name it is a form: Enter renames, Esc skips. -->
  <Dialog title={row.kind === "name" ? refusedCopy.nameTitle : refusedCopy.pathTitle} onclose={() => decide({ kind: "skip" })} onsubmit={renaming ? rename : undefined}>
    {#if renaming}
      <TextField id="rf-name" label={refusedCopy.renameLabel} bind:value={newName} bind:valid={newNameValid} attempt={newNameAttempt} judge={fileNameProblem} />
    {:else if row.kind === "name"}
      <!-- The name it would write, strong and breaking anywhere: a name too
           long for the destination is long in the dialog too. -->
      <p class="nm">{refusedCopy.notWritten(row.leaf)}</p>
      {#if shorter === null}
        <p class="dim">{refusedCopy.nothingShorter}</p>
      {:else}
        <p class="nm">{refusedCopy.writeAs} <strong>{shorter}</strong>?</p>
      {/if}
      <details class="how"><summary>{refusedCopy.howTitle}</summary><p>{refusedCopy.how}</p></details>
    {:else}
      <p>{refusedCopy.pathBody(row.leaf, row.isDir)}</p>
    {/if}
    {#if rows.length > 1}<p class="dim">{refusedCopy.progress(at + 1, rows.length)}</p>{/if}
    {#if !renaming && rest > 0}
      <label class="check"><input type="checkbox" bind:checked={forRest} />{refusedCopy.sameForRest(rest)}</label>
    {/if}
    {#snippet actions()}
      {#if renaming}
        <button type="submit" class="btn accent">{refusedCopy.renameGo}</button>
        <button type="button" class="btn" onclick={() => (renaming = false)}>{refusedCopy.renameBack}</button>
      {:else if row.kind === "name"}
        <!-- Standard, first: writing under another name is an answer, not
             the default to press (direction 7). -->
        <button type="button" class="btn" disabled={shorter === null} title={shorter === null ? refusedCopy.nothingShorter : undefined} onclick={() => decide({ kind: "shorten" })}>{refusedCopy.useName}</button>
        <button type="button" class="btn" onclick={askRename}>{refusedCopy.rename}</button>
        <button type="button" class="btn" onclick={() => decide({ kind: "skip" })}>{refusedCopy.skip}</button>
      {:else}
        <button type="button" class="btn" onclick={() => decide({ kind: "skip" })}>{refusedCopy.skip}</button>
        <button type="button" class="btn" onclick={skipAll}>{refusedCopy.skipAllLike}</button>
      {/if}
    {/snippet}
  </Dialog>
{/if}

<style>
  /* A refused name is long by definition: it wraps anywhere rather than
     pushing the dialog wider. */
  .nm { overflow-wrap: anywhere; }
  .nm strong { font: var(--t-strong); }
</style>

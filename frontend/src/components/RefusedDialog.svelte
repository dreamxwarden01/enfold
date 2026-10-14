<script lang="ts">
  // A name the destination refused (APP.md §3, ruled 2026-09-10), asked
  // per refused record: the final name refused on placement offers
  // *Shorten* — the extension kept, the stem halved — *Rename…* for this
  // extract only, and *Skip*; a refused path — the temporary or a folder,
  // where no name helps — offers *Skip* and *Skip all like it*. Esc is
  // *Skip*; a click outside is nothing (APP.md §6). When every row has its answer the decisions go back to the
  // page, which re-issues Extract with `names` (lib/refused.ts). The
  // rows are the operation's outcomes; nothing is kept here but what was
  // decided so far, and the page keys this dialog on the operation so a
  // new question starts afresh.
  import { tick } from "svelte";
  import Dialog from "./Dialog.svelte";
  import TextField from "./TextField.svelte";
  import { renameSelection } from "../lib/paths";
  import { nextUndecided, shorten, skipAllLike } from "../lib/refused";
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

  // *Rename…* turns the dialog into a name field for this row; *Back*
  // returns to the question.
  let renaming = $state(false);
  let newName = $state("");
  let newNameValid = $state(true);
  let newNameAttempt = $state(0);

  function decide(d: RefusedDecision) {
    if (!row) return;
    renaming = false;
    settle({ ...decisions, [row.id]: d });
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
      <p>{refusedCopy.nameBody(row.leaf)}</p>
      {#if shorter === null}<p class="dim">{refusedCopy.nothingShorter}</p>{/if}
    {:else}
      <p>{refusedCopy.pathBody(row.leaf, row.isDir)}</p>
    {/if}
    {#if rows.length > 1}<p class="dim">{refusedCopy.progress(at + 1, rows.length)}</p>{/if}
    {#snippet actions()}
      {#if renaming}
        <button type="button" class="btn" onclick={() => (renaming = false)}>{refusedCopy.renameBack}</button>
        <button type="submit" class="btn accent">{refusedCopy.renameGo}</button>
      {:else if row.kind === "name"}
        <button type="button" class="btn" onclick={() => decide({ kind: "skip" })}>{refusedCopy.skip}</button>
        <button type="button" class="btn" onclick={askRename}>{refusedCopy.rename}</button>
        <button type="button" class="btn accent" disabled={shorter === null} title={shorter === null ? undefined : refusedCopy.shortenTo(shorter)} onclick={() => decide({ kind: "shorten" })}>{refusedCopy.shorten}</button>
      {:else}
        <button type="button" class="btn" onclick={skipAll}>{refusedCopy.skipAllLike}</button>
        <button type="button" class="btn accent" onclick={() => decide({ kind: "skip" })}>{refusedCopy.skip}</button>
      {/if}
    {/snippet}
  </Dialog>
{/if}

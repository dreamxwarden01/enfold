<script lang="ts">
  // The recovery key's eight cells (APP.md §6): plain digits, since they
  // are read back against paper, not a password; each cell moves on when
  // its six digits are in, a paste fills them all with the dashes
  // stripped, a click lands on the first cell still to be typed — or,
  // once all eight are in, where it was aimed, the group selected for
  // retyping. A finished group is judged at once (ruled 2026-10-17): one
  // that checks out wears a small tick, one that fails a 2 px danger
  // underline, and the line under the cells names it by its number. The
  // digits go over the raw channel on submit; the store keeps them across
  // the derivation, so that a key the core refused comes back for
  // correction in place.
  import { untrack } from "svelte";
  import { fade } from "svelte/transition";
  import { submitSecret } from "../lib/api";
  import { motion } from "../lib/motion";
  import { store } from "../lib/state.svelte";
  import { ceremonyCopy } from "../lib/strings";
  import { nextMark } from "../lib/forms";
  import { recoveryWords } from "../lib/format";
  import { RECOVERY_GROUPS, RECOVERY_GROUP_LEN, RECOVERY_SEPARATORS, recoveryGroupMarks, recoveryGroupProblem } from "../lib/validate";

  // A recovery slot as the prompt lists it: ID first, then the label's own
  // words, so the right sheet is picked before a digit is typed — the ID
  // is what the sheet prints at its top (APP.md §13, ruled 2026-10-17;
  // FORMAT.md §18.4). A quiet list: the kind the label repeats and the
  // date are left out, the date kept only for a key with no words of its
  // own (lib/format recoveryWords). The ID is derived by the core, once.
  interface RecoverySlotLine {
    label: string;
    id: string;
    createdAt: number;
  }

  interface Props {
    promptId: string;
    note?: string;
    button?: string;
    // slots: the vault's recovery keys, when the key asked for is one of
    // them. Empty where it is another file's.
    slots?: RecoverySlotLine[];
    // error: what the core said about the last key under this prompt.
    error?: string;
    // heading: the field's own label row. The lock screen's panel says
    // what the digits are in its lead and draws none.
    heading?: boolean;
    // cancel: the button beside the primary, where the panel has no foot
    // of its own (the lock screen); a dialog's Cancel is its foot's.
    cancel?: { label: string; run: () => void } | null;
    // formId: the dialog's foot band holds the submit, which names this
    // form (SecretInput's formId; APP.md §6, Controls).
    formId?: string;
  }
  let { promptId, note = "", button = "Continue", slots = [], error = "", heading = true, cancel = null, formId }: Props = $props();
  let groups = $state<string[]>(Array(RECOVERY_GROUPS).fill(""));
  let cells: HTMLInputElement[] = $state([]);
  let pressed = $state(false);
  let typedSince = $state(false);

  const complete = $derived(groups.every((g) => g.length === RECOVERY_GROUP_LEN));
  // open: the first cell still to be typed; -1 once all eight are in.
  const open = $derived(groups.findIndex((g) => g.length < RECOVERY_GROUP_LEN));
  const mistyped = $derived(groups.map(recoveryGroupProblem));
  const marks = $derived(recoveryGroupMarks(groups));
  const anyMistyped = $derived(mistyped.some(Boolean));
  // The groups to fix, by the number the sheet would give them.
  const badGroups = $derived(mistyped.flatMap((m, i) => (m ? [i + 1] : [])));
  // How far the typing is, while it is not done.
  const typed = $derived(groups.filter((g) => g.length === RECOVERY_GROUP_LEN).length);
  const progress = $derived(complete ? "" : ceremonyCopy.groupsTyped(typed, RECOVERY_GROUPS));
  // missing: what the press's mark says ("" once all 48 digits are in).
  // The mark follows the form rule (APP.md §6, lib/forms.ts): set by a
  // press with digits missing, gone the moment the eighth group is in,
  // and not brought back by a group selected for retyping. A mistyped
  // group needs no press: its checksum is judged as it is finished.
  const missing = $derived(complete ? "" : "All 48 digits are needed.");
  const refused = $derived(!!error && !typedSince);
  const said = $derived(anyMistyped ? ceremonyCopy.groupsMistyped(badGroups) : pressed && missing ? missing : refused ? error : "");

  $effect(() => {
    // A prompt that says the key was refused brings the typed groups back
    // from the store; a fresh ceremony starts clean.
    void promptId;
    const draft = untrack(() => store.recoveryDraft);
    if (error && draft) {
      groups = [...draft];
    } else {
      groups = Array(RECOVERY_GROUPS).fill("");
      store.recoveryDraft = null;
    }
    pressed = false;
    typedSince = false;
    // Untracked: the focus reads the groups, and this effect is about the
    // prompt, not about every keystroke.
    untrack(focusOpen);
  });

  function remember() {
    store.recoveryDraft = [...groups];
    typedSince = true;
    pressed = nextMark("change", pressed, missing);
  }

  function focusOpen() {
    const i = open < 0 ? RECOVERY_GROUPS - 1 : open;
    cells[i]?.focus();
  }

  // fill distributes digits from cell `from` onwards, six per cell, and
  // resyncs the cell the user is in: Svelte writes a cell only when its
  // state changed, so a stray non-digit must be undone by hand.
  function fill(from: number, text: string) {
    const digits = text.replace(RECOVERY_SEPARATORS, "").replace(/\D/g, "");
    const next = [...groups];
    let i = from;
    let pos = 0;
    while (i < RECOVERY_GROUPS && pos < digits.length) {
      const room = RECOVERY_GROUP_LEN - next[i].length;
      next[i] += digits.slice(pos, pos + room);
      pos += room;
      if (next[i].length === RECOVERY_GROUP_LEN) i++;
    }
    groups = next;
    remember();
  }

  function input(i: number, e: Event) {
    const el = e.currentTarget as HTMLInputElement;
    const digits = el.value.replace(/\D/g, "");
    groups[i] = "";
    fill(i, digits);
    el.value = groups[i];
    if (groups[i].length === RECOVERY_GROUP_LEN) focusOpen();
  }

  function paste(i: number, e: ClipboardEvent) {
    e.preventDefault();
    const text = e.clipboardData?.getData("text") ?? "";
    // A whole key replaces everything; a fragment fills from here.
    const digits = text.replace(RECOVERY_SEPARATORS, "").replace(/\D/g, "");
    if (digits.length >= RECOVERY_GROUPS * RECOVERY_GROUP_LEN) {
      groups = Array(RECOVERY_GROUPS).fill("");
      fill(0, digits);
    } else {
      groups[i] = "";
      fill(i, digits);
    }
    focusOpen();
  }

  function key(i: number, e: KeyboardEvent) {
    if (e.key === "Backspace" && groups[i] === "" && i > 0) {
      // An empty cell: the previous one, at its end.
      e.preventDefault();
      const prev = cells[i - 1];
      prev?.focus();
      prev?.setSelectionRange(prev.value.length, prev.value.length);
    } else if (e.key === "Enter") {
      e.preventDefault();
      submit();
    }
  }

  function click(i: number, e: MouseEvent) {
    const el = e.currentTarget as HTMLInputElement;
    if (complete) {
      el.select(); // aimed at this group: retype it whole
    } else if (i !== open) {
      focusOpen();
    }
  }

  function submit(e?: Event) {
    e?.preventDefault();
    pressed = nextMark("press", pressed, missing);
    if (!complete || anyMistyped) {
      focusOpen();
      return;
    }
    submitSecret("recovery", promptId, groups.join(""));
    // The digits stay: the core's answer decides whether they were right.
  }
</script>

<form class="field" id={formId} onsubmit={submit} novalidate>
  {#if heading}
    <!-- The hint becomes the count of groups typed once typing starts. -->
    <div class="field-top"><span class="t-strong">Recovery key</span><span class="hint">{typed > 0 && progress ? progress : "48 digits, in 8 groups."}</span></div>
  {/if}
  {#if slots.length}
    <!-- Which sheet: the digits' checksum catches a mistyped group, the
         ID catches the wrong sheet (FORMAT.md §18.4). -->
    <ul class="keylist">
      {#each slots as s (s.label + s.id + s.createdAt)}
        {@const words = recoveryWords(s.label, s.createdAt)}
        <li>{#if s.id}<span class="kl-id">{s.id}</span>{#if words}<span aria-hidden="true">·</span>{/if}{/if}{#if words}<span class="kl-label">{words}</span>{/if}</li>
      {/each}
    </ul>
  {/if}
  <!-- A refused key is the key's fault, not a group's: the line under the
       cells says so, described to the whole group, and each cell keeps its
       own judgement (ruled 2026-10-17; every cell underlined read as eight
       wrong groups). -->
  <div class="cells" role="group" aria-label="Recovery key, 8 groups of 6 digits" aria-describedby={said ? `recovery-${promptId}-said` : undefined}>
    {#each groups as g, i (i)}
      <span class="cellbox">
      <input
        bind:this={cells[i]}
        class="input cell"
        class:bad={marks[i] === "bad"}
        type="text"
        inputmode="numeric"
        autocomplete="off"
        autocapitalize="off"
        spellcheck="false"
        maxlength={RECOVERY_GROUP_LEN}
        tabindex={complete || i === open ? 0 : -1}
        aria-label={`Group ${i + 1}`}
        aria-invalid={marks[i] === "bad"}
        value={g}
        onclick={(e) => click(i, e)}
        oninput={(e) => input(i, e)}
        onpaste={(e) => paste(i, e)}
        onkeydown={(e) => key(i, e)}
      />
      <!-- The tick is the group's own check digit, true of a refused key's
           groups too; that the digits are another sheet's is the line's to
           say, by the key ID (ruled 2026-10-17). -->
      {#if marks[i] === "tick"}
        <svg class="ok" viewBox="0 0 10 10" aria-hidden="true" transition:fade={motion()}><path d="m2 5.2 2 2 4-4.4" /></svg>
      {/if}
      </span>
    {/each}
  </div>
  {#if said}<div id="recovery-{promptId}-said" class="field-error" transition:fade={motion()}>{said}</div>{/if}
  {#if note}<div class="pin-note">{note}</div>{/if}
  {#if formId}
    <!-- The submit is the dialog's foot's, which names this form. -->
  {:else if cancel}
    <!-- The primary at its natural width with Cancel beside it, and the
         count of groups typed at the row's end (ruled 2026-10-17). -->
    <div class="submit row">
      <button type="submit" class="btn accent">{button}</button>
      <button type="button" class="btn" onclick={() => cancel?.run()}>{cancel.label}</button>
      <!-- The count only once typing has begun: "0 of 8 groups" says
           nothing the empty cells do not. -->
      <span class="count grow num">{typed > 0 ? progress : ""}</span>
    </div>
  {:else}
    <div class="submit"><button type="submit" class="btn accent wide">{button}</button></div>
  {/if}
</form>

<style>
  .cells { display: grid; grid-template-columns: repeat(4, 1fr); gap: var(--s2); }
  .keylist { list-style: none; margin: 0 0 var(--s2); padding: 0; display: flex; flex-direction: column; gap: var(--s1); }
  .keylist li { display: flex; align-items: baseline; gap: var(--s2); font: var(--t-cap); color: var(--ink-2); min-width: 0; }
  .kl-label { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .kl-id { font-family: var(--font-mono); letter-spacing: 0.04em; color: var(--ink); flex: none; }
  /* The digits at the sub size in mono, read back against paper (APP.md
     §6, ruled 2026-10-17): 20 px in a 40 px cell, four to a row, unspaced
     as the mock's — the mono's own advance keeps them apart — so the six
     sit 72 px wide in a 90 px cell and leave its corners to the tick and
     the caret room at the end. */
  .cellbox { position: relative; display: block; min-width: 0; }
  .cell { width: 100%; height: 40px; font: var(--t-sub); font-family: var(--font-mono); font-weight: 400; letter-spacing: 0; text-align: center; padding: 0 var(--s1); }
  /* A group that fails its check digit: a 2 px danger underline, not a
     frame — the field's own 1 px, doubled, so it reads at a glance. */
  .cell.bad, .cell.bad:focus { border-bottom-color: var(--danger); box-shadow: inset 0 -2px 0 var(--danger); }
  /* A group that checks out: a small accent tick in its top-right corner,
     8 px, 2 px in as the mock's, above and beyond the sixth digit's
     shoulder (measured in Edge, 2026-10-17). */
  .ok { position: absolute; top: 2px; right: 2px; width: 8px; height: 8px; fill: none; stroke: var(--accent); stroke-width: 1.6; stroke-linecap: round; stroke-linejoin: round; pointer-events: none; }
  .submit { margin-top: var(--s3); }
  .submit.row { display: flex; align-items: center; gap: var(--s2); }
  .count { font: var(--t-cap); color: var(--ink-2); text-align: right; }
</style>

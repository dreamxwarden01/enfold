<script lang="ts">
  // The recovery key's eight cells (APP.md §6): plain digits, since they
  // are read back against paper, not a password; each cell moves on when
  // its six digits are in, a paste fills them all with the dashes
  // stripped, a click lands on the first cell still to be typed — or,
  // once all eight are in, where it was aimed, the group selected for
  // retyping — and a finished group that fails its checksum turns red at
  // once. The digits go over the raw channel on submit; the store keeps
  // them across the derivation, so that a key the core refused comes back
  // for correction in place.
  import { untrack } from "svelte";
  import { fade } from "svelte/transition";
  import { submitSecret } from "../lib/api";
  import { motion } from "../lib/motion";
  import { store } from "../lib/state.svelte";
  import { date } from "../lib/format";
  import { RECOVERY_GROUPS, RECOVERY_GROUP_LEN, RECOVERY_SEPARATORS, recoveryGroupProblem } from "../lib/validate";

  // A recovery slot as the prompt lists it: label · ID · date, so the
  // right sheet is picked before a digit is typed (APP.md §13,
  // FORMAT.md §18.4). The ID is derived by the core, once.
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
  }
  let { promptId, note = "", button = "Continue", slots = [], error = "" }: Props = $props();
  let groups = $state<string[]>(Array(RECOVERY_GROUPS).fill(""));
  let cells: HTMLInputElement[] = $state([]);
  let pressed = $state(false);
  let typedSince = $state(false);

  const complete = $derived(groups.every((g) => g.length === RECOVERY_GROUP_LEN));
  // open: the first cell still to be typed; -1 once all eight are in.
  const open = $derived(groups.findIndex((g) => g.length < RECOVERY_GROUP_LEN));
  const mistyped = $derived(groups.map(recoveryGroupProblem));
  const anyMistyped = $derived(mistyped.some(Boolean));
  const refused = $derived(!!error && !typedSince);
  const said = $derived(anyMistyped ? "A group is mistyped: each is six digits, and the last digit checks the other five." : pressed && !complete ? "All 48 digits are needed." : refused ? error : "");

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
    pressed = true;
    if (!complete || anyMistyped) {
      focusOpen();
      return;
    }
    submitSecret("recovery", promptId, groups.join(""));
    // The digits stay: the core's answer decides whether they were right.
  }
</script>

<form class="field" onsubmit={submit} novalidate>
  <div class="field-top"><span class="label">Recovery key</span><span class="hint">48 digits, in eight groups.</span></div>
  {#if slots.length}
    <!-- Which sheet: the digits' checksum catches a mistyped group, the
         ID catches the wrong sheet (FORMAT.md §18.4). -->
    <ul class="keylist">
      {#each slots as s (s.label + s.id + s.createdAt)}
        <li><span class="kl-label">{s.label}</span>{#if s.id}<span class="kl-id">{s.id}</span>{/if}<span class="kl-date">{date(s.createdAt)}</span></li>
      {/each}
    </ul>
  {/if}
  <div class="cells" role="group" aria-label="Recovery key, eight groups of six digits" aria-describedby={said ? `recovery-${promptId}-said` : undefined}>
    {#each groups as g, i (i)}
      <input
        bind:this={cells[i]}
        class="input cell"
        class:invalid={!!mistyped[i] || refused}
        type="text"
        inputmode="numeric"
        autocomplete="off"
        autocapitalize="off"
        spellcheck="false"
        maxlength={RECOVERY_GROUP_LEN}
        tabindex={complete || i === open ? 0 : -1}
        aria-label={`Group ${i + 1}`}
        aria-invalid={!!mistyped[i] || refused}
        value={g}
        onclick={(e) => click(i, e)}
        oninput={(e) => input(i, e)}
        onpaste={(e) => paste(i, e)}
        onkeydown={(e) => key(i, e)}
      />
    {/each}
  </div>
  {#if said}<div id="recovery-{promptId}-said" class="field-error" transition:fade={motion()}>{said}</div>{/if}
  {#if note}<div class="pin-note">{note}</div>{/if}
  <div class="submit"><button type="submit" class="btn accent wide">{button}</button></div>
</form>

<style>
  .cells { display: grid; grid-template-columns: repeat(4, 1fr); gap: 6px; }
  .keylist { list-style: none; margin: 0 0 10px; padding: 0; display: flex; flex-direction: column; gap: 3px; }
  .keylist li { display: flex; align-items: baseline; gap: 8px; font-size: 11.5px; color: var(--ink-3); min-width: 0; }
  .kl-label { color: var(--ink-2); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .kl-id { font-family: var(--font-mono); letter-spacing: 0.04em; color: var(--ink); flex: none; }
  .kl-date { margin-left: auto; flex: none; }
  .cell { font-family: var(--font-mono); font-size: 15px; letter-spacing: 0.08em; text-align: center; padding: 8px 4px; }
  .label { font-size: 12.5px; }
  .submit { margin-top: 12px; }
</style>

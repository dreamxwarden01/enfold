<script lang="ts">
  // The recovery key's eight cells (APP.md §6): plain digits, since they
  // are read back against paper, not a password; each cell moves on when
  // its six digits are in, a paste fills them all with the dashes
  // stripped, a click lands on the first cell still to be typed, and a
  // finished group that fails its checksum turns red at once. The digits
  // go over the raw channel on submit and stay when the core says they
  // did not open the vault, so the user corrects them in place.
  import { untrack } from "svelte";
  import { fade } from "svelte/transition";
  import { submitSecret } from "../lib/api";
  import { motion } from "../lib/motion";
  import { RECOVERY_GROUPS, RECOVERY_GROUP_LEN, RECOVERY_SEPARATORS, recoveryGroupProblem } from "../lib/validate";

  interface Props {
    promptId: string;
    note?: string;
    button?: string;
    // error: what the core said about the last key under this prompt.
    error?: string;
  }
  let { promptId, note = "", button = "Continue", error = "" }: Props = $props();
  let groups = $state<string[]>(Array(RECOVERY_GROUPS).fill(""));
  let cells: HTMLInputElement[] = $state([]);
  let pressed = $state(false);
  let typedSince = $state(false);
  let lastPrompt = "";

  const complete = $derived(groups.every((g) => g.length === RECOVERY_GROUP_LEN));
  const mistyped = $derived(groups.map(recoveryGroupProblem));
  const anyMistyped = $derived(mistyped.some(Boolean));
  const refused = $derived(!!error && !typedSince);
  const said = $derived(anyMistyped ? "A group is mistyped: each is six digits, and the last digit checks the other five." : pressed && !complete ? "All 48 digits are needed." : refused ? error : "");

  $effect(() => {
    // A new prompt keeps the digits when the core refused them (the user
    // corrects in place); a fresh ceremony starts clean.
    void promptId;
    const again = !!error && lastPrompt !== "";
    lastPrompt = promptId;
    if (!again) groups = Array(RECOVERY_GROUPS).fill("");
    pressed = false;
    typedSince = false;
    // Untracked: the focus reads the groups, and this effect is about the
    // prompt, not about every keystroke.
    untrack(focusNext);
  });

  function firstOpen(): number {
    const i = groups.findIndex((g) => g.length < RECOVERY_GROUP_LEN);
    return i < 0 ? RECOVERY_GROUPS - 1 : i;
  }

  function focusNext() {
    cells[firstOpen()]?.focus();
  }

  function fill(from: number, text: string) {
    // Distribute digits from cell `from` onwards, six per cell.
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
    typedSince = true;
    focusNext();
  }

  function input(i: number, e: Event) {
    const el = e.currentTarget as HTMLInputElement;
    const digits = el.value.replace(/\D/g, "");
    groups[i] = "";
    el.value = "";
    fill(i, digits);
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
  }

  function key(i: number, e: KeyboardEvent) {
    if (e.key === "Backspace" && groups[i] === "" && i > 0) {
      e.preventDefault();
      groups[i - 1] = groups[i - 1].slice(0, -1);
      typedSince = true;
      cells[i - 1]?.focus();
    } else if (e.key === "Backspace") {
      e.preventDefault();
      groups[i] = groups[i].slice(0, -1);
      typedSince = true;
    } else if (e.key === "Enter") {
      e.preventDefault();
      submit();
    }
  }

  function submit(e?: Event) {
    e?.preventDefault();
    pressed = true;
    if (!complete || anyMistyped) {
      focusNext();
      return;
    }
    submitSecret("recovery", promptId, groups.join(""));
    // The digits stay: the core's answer decides whether they were right.
  }
</script>

<form class="field" onsubmit={submit} novalidate>
  <div class="field-top"><span class="label">Recovery key</span><span class="hint">48 digits, in eight groups.</span></div>
  <div class="cells" role="group" aria-label="Recovery key, eight groups of six digits">
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
        aria-label={`Group ${i + 1}`}
        aria-invalid={!!mistyped[i] || refused}
        value={g}
        onclick={focusNext}
        onfocus={(e) => { if (i !== firstOpen()) { e.preventDefault(); focusNext(); } }}
        oninput={(e) => input(i, e)}
        onpaste={(e) => paste(i, e)}
        onkeydown={(e) => key(i, e)}
      />
    {/each}
  </div>
  {#if said}<div class="field-error" transition:fade={motion()}>{said}</div>{/if}
  {#if note}<div class="pin-note">{note}</div>{/if}
  <div class="submit"><button type="submit" class="btn accent wide">{button}</button></div>
</form>

<style>
  .cells { display: grid; grid-template-columns: repeat(4, 1fr); gap: 6px; }
  .cell { font-family: var(--font-mono); font-size: 15px; letter-spacing: 0.08em; text-align: center; padding: 8px 4px; }
  .label { font-size: 12.5px; }
  .submit { margin-top: 12px; }
</style>

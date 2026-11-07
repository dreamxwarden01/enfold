<script lang="ts">
  // Switch to a YubiKey…'s form (APP.md §13; ruled 2026-11-07, the study's
  // U1 under the owner's rulings): everything is said before anything is
  // asked, and nothing is asked here that the ceremony asks — no password is
  // typed in this form (a secret is a prompt's, never a bound call's
  // argument, APP.md §1). It opens from the password's card, or from Add an
  // unlock method… with a YubiKey chosen on a password vault, the name typed
  // there carried over.
  //
  // In the order it is read: what changes, and that the recovery keys stay;
  // the replaced vault files that still open with the password, while there
  // are any; the one attention bar — a lost YubiKey leaves only the recovery
  // key — with the tick inside it that gates the primary, unticked every
  // time the form opens (the one mistake that costs access); the YubiKey's
  // name; which password goes on with it, the same words by default; Turn
  // on…'s question, re-keying by default; the limit of a re-key, whichever
  // the answers; How this works. No backup is offered (the owner,
  // 2026-11-07). The password always goes on: a user who wants the YubiKey
  // alone turns it off afterwards with Turn off…, the deliberate act for it.
  // Its height does not change with the answers, so it stays centred
  // (APP.md §6, Dialogs stay put); where the window is too short, its body
  // scrolls under a fixed foot, as every dialog's does (Dialog.svelte).
  import type { Answer } from "../lib/rekey";
  import { questionOf, switchPrimary } from "../lib/rekey";
  import { dialogCopy, switchCopy } from "../lib/strings";
  import { fieldMenu } from "../lib/fieldmenu";
  import Dialog from "./Dialog.svelte";
  import RekeyQuestion from "./RekeyQuestion.svelte";

  interface Props {
    // label: the name carried over from Add an unlock method…, else empty.
    label?: string;
    // recoveryKeys: the vault's, for the singular; retired: the replaced
    // vault files Enfold's folder keeps (VaultStatus.RetiredCopies).
    recoveryKeys: number;
    retired: number;
    onclose: () => void;
    // onstart begins the act with what the form chose (KeysPage startSwitch).
    onstart: (o: { label: string; keep: boolean; rekey: boolean }) => void;
  }
  let { label = "", recoveryKeys, retired, onclose, onstart }: Props = $props();

  // Unticked each time the form opens: the component is mounted per open.
  let known = $state(false);
  let name = $state("");
  let password = $state<"same" | "new">("same");
  let answer = $state<Answer>("rekey");
  // The name carried over, taken once as the form opens.
  $effect.pre(() => {
    name = label;
  });

  const q = questionOf("on");
  const n = $derived(recoveryKeys);
  const primary = $derived(switchPrimary(answer === "rekey"));
  const group = $props.id();

  // The form's own focus: on the checked answer of the password's question,
  // the first decision after the bar (the study's U1), rather than on the
  // optional name, which Dialog would pick as the form's first field. Taken
  // after Dialog's own, once, as the box opens.
  function first(node: HTMLInputElement) {
    const t = setTimeout(() => {
      if (node.checked && node.isConnected) node.focus();
    }, 0);
    return { destroy: () => clearTimeout(t) };
  }

  // Enter in the name field submits the form: the tick is asked again, so
  // that nothing begins without it.
  function go() {
    if (!known) return;
    onclose();
    onstart({ label: name.trim(), keep: password === "same", rekey: answer === "rekey" });
  }
</script>

<Dialog title={switchCopy.title} medium {onclose} onsubmit={go}>
  <p>{switchCopy.body(n)}</p>
  {#if retired > 0}
    <!-- Replaced copies keep their unlock methods (APP.md §13): the
         removal's own line, for the password that goes. -->
    <p>{dialogCopy.removeUnlockMethod.retired(retired, "the password")}</p>
  {/if}
  <!-- The one bar: attention, a weaker guarantee rather than a failure
       (APP.md §6, Notices). Its tick is the user's word that the recovery
       key can be found, and gates the primary. -->
  <div class="bar attention sw-bar">
    <svg class="i" aria-hidden="true"><use href="#i-warn" /></svg>
    <div class="grow sw-said">
      <strong>{switchCopy.lost(n)}</strong>
      <label class="check"><input type="checkbox" bind:checked={known} />{switchCopy.tick(n)}</label>
      <span class="t-quiet sw-cap">{switchCopy.tickCaption}</span>
    </div>
  </div>
  <div class="field">
    <div class="field-top"><label for="sw-label">{dialogCopy.addUnlockMethod.nameKey}</label><span class="hint">{dialogCopy.addUnlockMethod.optional}</span></div>
    <input id="sw-label" class="input" bind:value={name} maxlength="120" placeholder={dialogCopy.addUnlockMethod.keyPlaceholder} use:fieldMenu />
  </div>
  <!-- Which password goes on with the YubiKey: drawn as the house's
       question is (RekeyQuestion), each answer with its second line,
       always drawn. -->
  <fieldset class="field sw-choices" data-question>
    <legend class="lbl">{switchCopy.legend}</legend>
    <label class="check">
      <input type="radio" name={group} value="same" bind:group={password} use:first />
      <span class="two"><span>{switchCopy.same}</span><small>{switchCopy.sameLine}</small></span>
    </label>
    <label class="check">
      <input type="radio" name={group} value="new" bind:group={password} />
      <span class="two"><span>{switchCopy.fresh}</span><small>{switchCopy.freshLine}</small></span>
    </label>
  </fieldset>
  <RekeyQuestion legend={q.legend} rekey={q.rekey} keep={q.keep} bind:answer />
  <!-- The limit of a re-key, whichever the answers: a sentence, not a gate. -->
  <p class="infoline"><svg class="i" aria-hidden="true"><use href="#i-info" /></svg><span>{switchCopy.limit(n)}</span></p>
  <details class="how"><summary>{dialogCopy.howThisWorks}</summary><p>{switchCopy.how}</p></details>
  {#snippet actions()}
    <!-- The accent: the switch strengthens the vault. Greyed until the
         tick is given, its reason as its title (APP.md §6, Controls). -->
    <button type="submit" class="btn accent" disabled={!known} title={known ? undefined : switchCopy.tickFirst(n)}>{primary}</button>
    <button type="button" class="btn" onclick={onclose}>{dialogCopy.cancel}</button>
  {/snippet}
</Dialog>

<style>
  /* The bar's sentence, its tick and the tick's caption stand in one
     column, 8 apart: the bar's words first, the tick under them as the
     thing to do, the caption under the tick as its way out. */
  .sw-said { display: flex; flex-direction: column; gap: var(--s2); min-width: 0; }
  .sw-bar .check { align-items: flex-start; }
  .sw-bar .check input { margin-top: 3px; flex: none; }
  /* The caption lines up under the tick's words, past the box. */
  .sw-cap { padding-left: calc(20px + var(--s2)); }
  /* The password's question, as RekeyQuestion draws the house's (its
     rules are scoped there, so the same five here). */
  .sw-choices { border: 0; margin: 0; padding: 0; min-width: 0; }
  .sw-choices .lbl { font: var(--t-strong); color: var(--ink); padding: 0 0 var(--s2); }
  .sw-choices .check { align-items: flex-start; }
  .sw-choices .check input { margin-top: 3px; flex: none; }
  .two { display: flex; flex-direction: column; min-width: 0; }
  .two small { font: var(--t-cap); color: var(--ink-2); }
</style>

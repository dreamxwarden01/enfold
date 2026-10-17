<script lang="ts">
  // The first unlock method of a new vault (APP.md §6, ruled 2026-10-17):
  // a radio group of cards — YubiKey, Password — and the recovery key's
  // fixed line under them. Until one is chosen each card shows its icon,
  // its title and one line; the chosen card opens on its fields, the other
  // closing as it opens. Shared by the create, restore and finish-setup
  // dialogs; what each card shows, what Continue refuses and the two lines
  // under the cards are lib/firstwayin's.
  //
  // The password — the standalone one, or the one every YubiKey must carry
  // — is typed here, twice, and judged here by the form rule
  // (lib/forms.ts). It lives in this component until the dialog's
  // Continue takes it (take), which hands it to the store to answer the
  // ceremony's Choose prompt (store.holdChosen); a dialog closed without
  // Continue takes it nowhere, and it goes with the component. It is
  // never an argument of a bound call (APP.md §1).
  //
  // Since Revision 2 the tick is the vault's one switch, not this
  // YubiKey's (APP.md §13, FORMAT.md §3.1): every YubiKey added later
  // inherits it, so the words say the vault's fact.
  import { onDestroy, untrack } from "svelte";
  import { fade } from "svelte/transition";
  import { nextMark, onLeave } from "../lib/forms";
  import { begin, fixedLine, keepsPassword, nextLine, problems } from "../lib/firstwayin";
  import type { Begin, FirstContext, FirstMethod } from "../lib/firstwayin";
  import { MOVE, motion } from "../lib/motion";
  import { dialogCopy, firstMethodCopy, lockCopy } from "../lib/strings";
  import { PASSWORD_RULE } from "../lib/validate";
  import SecretField from "./SecretField.svelte";
  import TextField from "./TextField.svelte";

  interface Props {
    heading: string;
    // create: a recovery key is made and shown after; adopt: a backup's
    // own opens it first and stays.
    context?: FirstContext;
    // attempt: the dialog's Continue, counted — each press marks every
    // part that would refuse (lib/forms.ts), as TextField's does.
    attempt?: number;
    idPrefix?: string;
  }
  let { heading, context = "create", attempt = 0, idPrefix = "fw" }: Props = $props();

  let method = $state<FirstMethod>("");
  let entangle = $state(false);
  let label = $state("");
  let password = $state("");
  let again = $state("");

  const p = $derived(problems({ method, entangle, password, again }));
  const next = $derived(nextLine(method, context));

  // The marks (APP.md §6, Forms and their errors): one flag for the
  // choice and one for each field. The choice is never left — a radio
  // group is not typed into — so only a press marks it, and choosing
  // clears it.
  let methodMark = $state(false);
  let pwMark = $state(false);
  let againMark = $state(false);
  const methodShow = $derived(methodMark && !!p.method);
  const pwShow = $derived(pwMark && !!p.password);
  const againShow = $derived(againMark && !!p.again);

  let seen = $state(untrack(() => attempt)); // only a press made while this is on screen counts
  // taken: Continue took the password and emptied the fields; the press
  // that took it marks nothing, or the closing dialog would say "This
  // field is required." under fields it has just emptied itself. The next
  // input ends it (touched), and so does that press once skipped, so a
  // dialog kept open would mark again.
  let taken = false;
  function touched() {
    taken = false;
  }
  $effect(() => {
    if (attempt > seen) {
      seen = attempt;
      // A second press with no input between marks as any press does.
      if (taken) { taken = false; return; }
      untrack(() => {
        methodMark = nextMark("press", methodMark, p.method);
        pwMark = nextMark("press", pwMark, p.password);
        againMark = nextMark("press", againMark, p.again);
      });
    }
  });

  // A card that closes, or a tick taken off, clears the two fields and
  // their marks: the secret is never kept in a field no one sees, nor
  // carried into the other card (lib/firstwayin keepsPassword).
  function settle(from: { method: FirstMethod; entangle: boolean }) {
    if (keepsPassword(from, { method, entangle })) return;
    password = "";
    again = "";
    pwMark = false;
    againMark = false;
  }

  function choose(m: FirstMethod) {
    touched();
    const from = { method, entangle };
    method = m;
    settle(from);
    methodMark = nextMark("change", methodMark, p.method);
  }

  function tick(on: boolean) {
    touched();
    const from = { method, entangle };
    entangle = on;
    settle(from);
  }

  function typedPassword() {
    touched();
    pwMark = nextMark("change", pwMark, p.password);
    // The second copy is judged against this one, so it changed too.
    againMark = nextMark("change", againMark, p.again);
  }
  function typedAgain() {
    touched();
    againMark = nextMark("change", againMark, p.again);
  }
  const leftPassword = onLeave(() => (pwMark = nextMark("leave", pwMark, p.password)));
  const leftAgain = onLeave(() => (againMark = nextMark("leave", againMark, p.again)));

  // take is the dialog's Continue: what the ceremony starts with, or null
  // while anything here refuses. The two fields are emptied as the
  // password leaves them; the caller hands it to the store at once.
  export function take(): Begin | null {
    const b = begin({ method, entangle, password, again }, label);
    if (!b) return null;
    taken = true;
    password = "";
    again = "";
    return b;
  }

  // The dialog closed without Continue: nothing was taken, and the fields
  // are emptied with the component.
  onDestroy(() => {
    password = "";
    again = "";
  });

  // hold keeps a closing card's fields on the page while the card's row
  // closes over them (220 ms on the entering curve; none under reduced
  // motion), so the card shrinks rather than jumping shut.
  function hold(_node: Element) {
    return motion(MOVE);
  }
</script>

{#snippet pair(where: string, on: boolean)}
  {#if on}
    <div class="pair" out:hold>
      <div class="field">
        <SecretField
          id="{idPrefix}-{where}-password"
          label={firstMethodCopy.passwordField}
          rule={PASSWORD_RULE}
          ruleBroken={pwShow && p.password === PASSWORD_RULE}
          said={pwShow && p.password !== PASSWORD_RULE ? p.password : ""}
          invalid={pwShow}
          bind:value={password}
          onblur={leftPassword}
          oninput={typedPassword}
        />
      </div>
      <div class="field">
        <SecretField
          id="{idPrefix}-{where}-again"
          label={firstMethodCopy.again}
          said={againShow ? p.again : ""}
          invalid={againShow}
          bind:value={again}
          onblur={leftAgain}
          oninput={typedAgain}
        />
      </div>
    </div>
  {/if}
{/snippet}

<div class="methods">
  <div class="field-top"><span id="{idPrefix}-head" class="head">{heading}</span></div>
  <div class="group">
  <div class="cards" role="radiogroup" aria-labelledby="{idPrefix}-head" aria-describedby={methodShow ? `${idPrefix}-said` : undefined}>
    <!-- Each card's head is a label round a real radio of one group, so the
         arrow keys move the choice and the card follows, and the whole head
         is the click target (APP.md §6). -->
    <div class="mcard" class:open={method === "token"}>
      <label class="mhead">
        <input type="radio" name="{idPrefix}-method" value="token" checked={method === "token"} onchange={() => choose("token")} aria-describedby="{idPrefix}-token-line" />
        <svg class="i i-20 micon" aria-hidden="true"><use href="#i-yubi" /></svg>
        <span class="mtext"><span class="mtitle">{firstMethodCopy.token.title}</span><span id="{idPrefix}-token-line" class="mline">{firstMethodCopy.token.line}</span></span>
      </label>
      <div class="mgrow" class:on={method === "token"} inert={method !== "token"}>
        <div class="minner">
          <div class="mbody">
            <!-- The placeholder is what an empty field produces — the core
                 names the YubiKey "YubiKey <serial>" — never an invented
                 name that would read as a default already filled in. -->
            <TextField id="{idPrefix}-label" label={firstMethodCopy.nameYubiKey} hint={dialogCopy.addUnlockMethod.optional} placeholder={dialogCopy.addUnlockMethod.keyPlaceholder} required={false} bind:value={label} />
            <!-- The tick's hint is the vault's fact, said ticked or not: the
                 switch is the vault's, not this YubiKey's. -->
            <div class="tick">
              <label class="check"><input type="checkbox" checked={entangle} onchange={(e) => tick(e.currentTarget.checked)} aria-describedby="{idPrefix}-tick-hint" />{lockCopy.entangleTick}</label>
              <span id="{idPrefix}-tick-hint" class="tickhint">{firstMethodCopy.entangleHint}</span>
            </div>
            <div class="mgrow" class:on={method === "token" && entangle} inert={!(method === "token" && entangle)}>
              <div class="minner">{@render pair("token", method === "token" && entangle)}</div>
            </div>
          </div>
        </div>
      </div>
    </div>

    <div class="mcard" class:open={method === "password"}>
      <label class="mhead">
        <input type="radio" name="{idPrefix}-method" value="password" checked={method === "password"} onchange={() => choose("password")} aria-describedby={method === "password" ? undefined : `${idPrefix}-password-line`} />
        <svg class="i i-20 micon" aria-hidden="true"><use href="#i-password" /></svg>
        <!-- The card's line is the rule the first field states under it
             (PASSWORD_RULE): while the card is open the line folds away
             as the fields open, so the requirement is said once, in the
             one place the form rule turns red (APP.md §6, Forms; ruled
             2026-10-17). The YubiKey's tick opens the same two fields
             with no such line above them, so the rule stays the field's. -->
        <span class="mtext">
          <span class="mtitle">{firstMethodCopy.password.title}</span>
          <span class="mgrow" class:on={method !== "password"} aria-hidden={method === "password" || undefined}><span class="minner"><span id="{idPrefix}-password-line" class="mline">{firstMethodCopy.password.line}</span></span></span>
        </span>
      </label>
      <div class="mgrow" class:on={method === "password"} inert={method !== "password"}>
        <div class="minner"><div class="mbody">{@render pair("password", method === "password")}</div></div>
      </div>
    </div>
  </div>
  {#if methodShow}<div id="{idPrefix}-said" class="field-error" transition:fade={motion()}>{p.method}</div>{/if}
  <!-- Not a choice, so not a card: a create always makes a recovery key,
       and an adopted backup keeps its own — one caption line, 4 px under
       the cards as they are under each other (ruled 2026-10-17; the third
       card it was pushed the dialog past an 820 px window with a card
       open). -->
  <p class="fixed"><svg class="i i-14" aria-hidden="true"><use href="#i-recovery" /></svg><span>{fixedLine(context)}</span></p>
  </div>
  <!-- What comes after Continue, by the choice, in a caption; empty until
       one is made and one caption line tall then, so filling it moves
       nothing but what a second line needs (ruled 2026-10-17; it held two
       body lines, which left 40 px empty under the shorter lines). -->
  <p class="next" aria-live="polite">{next}</p>
</div>

<style>
  /* Compact by the scale's smaller steps (APP.md §6, Type and spacing;
     ruled 2026-10-17), so the create and restore dialogs fit an 820 px
     window with any card open: 8 between the group's parts, 4 between the
     cards, and each card's inset 12, a strip's, rather than a card's 16. */
  .methods { display: flex; flex-direction: column; gap: var(--s2); }
  .head { font: var(--t-strong); color: var(--ink); }
  .group, .cards { display: flex; flex-direction: column; gap: var(--s1); }
  .mcard {
    border: 1px solid var(--stroke); border-radius: var(--r-card); background: var(--surface);
    transition: border-color var(--dur-out) var(--ease-enter);
  }
  .mcard.open { border-color: var(--accent); }
  /* The card wears the focus ring its radio would: the whole card is
     the control (APP.md §6, Controls). */
  .mcard:has(.mhead input:focus-visible) {
    outline: 2px solid var(--focus-outer); outline-offset: 2px; box-shadow: 0 0 0 2px var(--focus-inner);
  }
  .mhead input:focus-visible { outline: none; box-shadow: none; }
  .mhead {
    display: flex; align-items: center; gap: var(--s3); padding: var(--s3);
    border-radius: calc(var(--r-card) - 1px);
    transition: background var(--dur-out) var(--ease-enter);
  }
  .mcard:not(.open) .mhead:hover { background: var(--fill-1); transition-duration: var(--dur-hover); }
  .micon { color: var(--ink-2); }
  .mcard.open .micon { color: var(--accent-ink); }
  .mtext { display: flex; flex-direction: column; min-width: 0; }
  .mtitle { font: var(--t-strong); color: var(--ink); }
  /* A block, so the caption's 16 px line is not stood on the body's 20 px
     strut inside the line's fold. */
  .mline { display: block; font: var(--t-cap); color: var(--ink-2); }
  /* The expansion (APP.md §6, Motion): the row opens from 0fr to 1fr over
     220 ms on the entering curve; the duration token is zero under
     prefers-reduced-motion, so nothing animates there. The Password
     card's line folds the same way as its fields open. */
  .mgrow { display: grid; grid-template-rows: 0fr; transition: grid-template-rows var(--dur-move) var(--ease-enter); }
  .mgrow.on { grid-template-rows: 1fr; }
  .minner { min-height: 0; overflow: hidden; }
  /* The fields stand under the card's icon, past the radio. */
  /* 8 between the YubiKey's name, its tick and the fields the tick opens;
     12 between the two password fields, past the rule under the first,
     so "Type it again" reads as the next field and not the rule's. */
  .mbody { display: flex; flex-direction: column; gap: var(--s2); padding: 0 var(--s3) var(--s3) calc(var(--s3) + 20px + var(--s3)); }
  .pair { display: flex; flex-direction: column; gap: var(--s3); }
  /* The tick's hint is the option's description: the caption in the
     second ink, under the label's words, past the 20 px box and its 8. */
  .tick { display: flex; flex-direction: column; }
  .tickhint { padding-left: calc(20px + var(--s2)); font: var(--t-cap); color: var(--ink-2); text-wrap: pretty; }
  .fixed { display: flex; align-items: center; gap: var(--s2); font: var(--t-cap); }
  .fixed .i { color: var(--ink-3); }
  .next { min-height: 1lh; font: var(--t-cap); text-wrap: pretty; }
</style>

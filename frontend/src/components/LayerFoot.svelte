<script lang="ts">
  // The layer's one foot, below the pages and outside their crossfade
  // (APP.md §6), and since direction 4 (phase 3) the window's status bar.
  // It is 34 px and always there, so an operation never moves the list —
  // the 42 px operation strip that pushed the list down as an operation
  // began is retired, and its 2026-09-11 rule, "a button that vanishes
  // reflows the strip; a greyed one keeps its shape", is kept by a slot
  // that never reflows at all.
  //
  // The left slot cycles, the old line leaving before the new one comes
  // (the house crossfade, GAP): the page's note at rest;
  // the running operation — its name with its archive (lib/ops.ts
  // opLabel), a 160 px progress line, the bytes and the time left
  // (lib/foot.ts), and Cancel where a cancel means something; then how it
  // ended, the done line, for 6 s or until the next press in a page's
  // body; then the note again. With two or more running the latest shows,
  // and "+1" opens the flyout that lists them all.
  //
  // The right slot is the lock state, always: the open padlock, the
  // nearer deadline said quietly (lib/format.ts lockLine), Stay unlocked
  // in the idle deadline's last minute, and Lock now; the closed padlock
  // and Unlock while locked with archives still open. While unlocked, the
  // status's standing notices gather beside it behind "1 notice"
  // (direction 9 left them to this foot). Commands are subtle buttons 24 px
  // high; what reveals something is a hyperlink button.
  import { untrack } from "svelte";
  import { fade, fly } from "svelte/transition";
  import { Archive, Shell, Vault, errorOf } from "../lib/api";
  import type { OpView } from "../lib/api";
  import { store } from "../lib/state.svelte";
  import { lockLine } from "../lib/format";
  import { motion, delay, enter, FAST, GAP, LEAVE } from "../lib/motion";
  import { cancellable, hasBar, opLabel, showsCancel, stripCount } from "../lib/ops";
  import { fraction, latestOp, progressCaption, standingNotices, staysUnlocked, timeLeft, track } from "../lib/foot";
  import type { DoneAction, Track } from "../lib/foot";
  import { tier, tierIcon } from "../lib/notices";
  import { POPOVER_ATTR, dialogIsUp } from "../lib/dialogs";
  import { isTextEntry, locksNow } from "../lib/listkeys";
  import { chipCopy, codeText, commandCopy, footCopy, keyTitles, warningCopy } from "../lib/strings";

  interface Props {
    pageKey: string;
  }
  let { pageKey }: Props = $props();
  const st = $derived(store.status);
  // The idle minutes are the status's, as the core runs the session on
  // them (ruled 2026-10-17): no copy of its clamp here.
  const line = $derived(st ? lockLine(st.locksAt, st.absoluteAt, st.idleEffectiveMinutes, store.now) : null);
  const stay = $derived(!!st && store.unlocked && staysUnlocked(st.locksAt, st.absoluteAt, store.now));

  // ---- the running operations ---------------------------------------------
  const running = $derived(store.runningOps);
  const latest = $derived(latestOp(running));
  // The registry row's name for each archive: OpView carries the id only.
  const names = $derived(new Map(store.archives.map((a) => [a.id, a.name])));

  function label(o: OpView): string {
    return opLabel(o.kind, stripCount(o), o.phase, names.get(o.archiveId ?? "") ?? "");
  }

  // What the foot has seen of each operation's bytes, for the time left
  // (lib/foot.ts track): a sample each time the bytes move.
  let tracks = $state<Record<string, Track>>({});
  $effect(() => {
    const seen = new Set<string>();
    for (const o of running) {
      seen.add(o.id);
      const was = untrack(() => tracks[o.id]);
      const now = track(was, { t: Date.now(), done: o.done });
      if (now !== was) tracks[o.id] = now;
    }
    untrack(() => {
      for (const id of Object.keys(tracks)) if (!seen.has(id)) delete tracks[id];
    });
  });

  // The caption's two halves: the bytes, and the time left, which is the
  // first thing to give way when the foot is narrow (fit, below).
  function bytesOf(o: OpView): string {
    return progressCaption(o.done, o.total);
  }
  function leftOf(o: OpView): string {
    return o.total > 0 ? timeLeft(tracks[o.id], o.total, store.now) : "";
  }

  // fit keeps the running line's name whole at the narrowest window
  // (APP.md §6, the 1024 px minimum): what gives way is the extra — first
  // the time left, then the progress line's length (160 px down to 64 px)
  // — and only then does the name take an ellipsis. It is measured, since
  // the name's length and the foot's width are both the window's: the
  // steps are tried in order and the first that lets the name stand whole
  // is kept, as the wrapper's data-fit (the styles below).
  function fit(node: HTMLElement, _key: string) {
    const run = () => {
      const name = node.querySelector<HTMLElement>(".opname");
      if (!name) return;
      for (let level = 0; level <= 2; level++) {
        node.dataset.fit = String(level);
        if (name.scrollWidth <= name.clientWidth) break;
      }
    };
    const ro = new ResizeObserver(run);
    ro.observe(node.parentElement ?? node);
    run();
    return { update: run, destroy: () => ro.disconnect() };
  }

  // The operations whose Cancel was pressed: the button says *Cancelling…*
  // and stays greyed until the operation's end takes it away (ruled
  // 2026-10-14) — a cancel waits for the work in hand to stop, and a
  // button that looked pressable meanwhile invited a second press. A
  // refusal (the operation already committing) gives the button back.
  let cancelling = $state<string[]>([]);
  // An operation that has ended leaves the list, so it does not grow for
  // the length of the session.
  $effect(() => {
    const live = new Set(running.map((o) => o.id));
    if (cancelling.some((id) => !live.has(id))) cancelling = cancelling.filter((id) => live.has(id));
  });

  async function cancel(opId: string) {
    if (cancelling.includes(opId)) return;
    cancelling.push(opId);
    try {
      await Archive.CancelOp(opId);
    } catch (e) {
      cancelling = cancelling.filter((id) => id !== opId);
      store.notice(codeText(errorOf(e).code), "error");
    }
  }

  // ---- the left slot ---------------------------------------------------------
  //
  // The done line holds its 6 s even while another operation still runs,
  // then the running one comes back; a new operation takes the line away
  // as it begins (store.applyOp), so a press never answers with an old
  // ending.
  const slot = $derived(store.done ? `done:${store.done.id}` : latest ? `op:${latest.id}` : `note:${pageKey}`);

  // The next press in a page's body is the user moving on: the line goes.
  function pressed(e: PointerEvent) {
    if (store.done && (e.target as Element | null)?.closest?.(".layer-body")) store.clearDone();
  }

  async function act(a: DoneAction) {
    if (a.kind === "details") {
      store.results = a.op;
      return;
    }
    try {
      await Shell.Reveal(a.path);
    } catch (e) {
      store.notice(codeText(errorOf(e).code), "error");
    }
  }

  // ---- the two popovers --------------------------------------------------------
  //
  // Each is a box above the foot, the menu's surface and shadow (app.css
  // .foot-pop): the operations' flyout at the left, the notices at the
  // right. One at a time; Escape, a press outside, or the thing it lists
  // going away closes it.
  let open = $state<"" | "ops" | "notices">("");

  const notices = $derived(store.unlocked ? standingNotices(st?.warnings) : []);

  $effect(() => {
    if (open === "ops" && running.length < 2) open = "";
    if (open === "notices" && notices.length === 0) open = "";
  });

  function toggle(which: "ops" | "notices") {
    open = open === which ? "" : which;
  }

  function outside(e: PointerEvent) {
    pressed(e);
    if (!open) return;
    const t = e.target as Element | null;
    if (t && (t.closest(".foot-pop") || t.closest("[data-pop-toggle]"))) return;
    open = "";
  }

  // The flyouts own their Escape before the error InfoBars do: their
  // toggles carry ESC_OWNER_ATTR (lib/dialogs.ts), so App.svelte's
  // escNotice leaves the key alone while one is open, whichever window
  // listener runs first; and a key already taken is not taken twice.
  function key(e: KeyboardEvent) {
    if (e.defaultPrevented) return;
    if (e.key === "Escape" && open) {
      e.preventDefault();
      e.stopPropagation();
      open = "";
    }
  }

  // Ctrl+L is Lock now on every page while unlocked (direction 7): the
  // foot's own command, so the foot is where the key is read — through the
  // lists' one keymap (lib/listkeys.ts locksNow), which leaves a text field
  // its keys and a dialog its own; a menu keeps its keys too.
  function lockKey(e: KeyboardEvent) {
    if (e.defaultPrevented || e.isComposing || !store.unlocked) return;
    const t = e.target instanceof HTMLElement ? e.target : null;
    if (t?.closest(`[role="menu"], [${POPOVER_ATTR}]`)) return;
    const k = { key: e.key, ctrl: e.ctrlKey || e.metaKey, shift: e.shiftKey, alt: e.altKey };
    const inField = !!t && isTextEntry(t.tagName, t.getAttribute("type") ?? "", t.isContentEditable);
    if (!locksNow(k, { inField, dialogUp: dialogIsUp() })) return;
    e.preventDefault();
    lockNow();
  }

  function lockNow() {
    Vault.Lock().catch((e) => store.notice(codeText(errorOf(e).code), "error"));
  }
</script>

<svelte:window onpointerdown={outside} onkeydowncapture={key} onkeydown={lockKey} />

{#snippet progressLine(o: OpView, wide: boolean)}
  {@const f = fraction(o.done, o.total)}
  <span class="fprog" class:wide class:indet={f === null} role="progressbar" aria-label={label(o)} aria-valuemin={0} aria-valuemax={100} aria-valuenow={f === null ? undefined : Math.round(f * 100)}>
    <i style:width={f === null ? null : `${f * 100}%`}></i>
  </span>
{/snippet}

{#snippet cancelButton(o: OpView)}
  {@const pending = cancelling.includes(o.id)}
  {@const can = cancellable(o.kind, o.phase) && !pending}
  <button type="button" class="btn subtle xs" disabled={!can} aria-disabled={!can} onclick={can ? () => void cancel(o.id) : undefined}>{pending ? footCopy.cancelling : footCopy.cancel}</button>
{/snippet}

<div class="layer-foot">
  <div class="foot-left">
    {#key slot}
      <!-- The house crossfade (lib/motion.ts GAP): the old line leaves
           fast first and the new one waits GAP for it, so two lines are
           never read on top of each other — the final shoot of phase 4b
           caught a drag's line and the note under it, both half drawn. -->
      <div class="foot-slot" in:fade={motion(FAST, { delay: delay(GAP), easing: enter })} out:fade={motion(LEAVE, { easing: enter })}>
        {#if slot.startsWith("done:") && store.done}
          {@const d = store.done}
          <span class="done" class:neutral={d.tone === "neutral"}>
            <svg class="i i-14"><use href={d.tone === "ok" ? "#i-check" : "#i-info"} /></svg>
            <span class="ellipsis" title={d.text}>{d.text}</span>
            {#if d.action}
              {@const a = d.action}
              <button type="button" class="btn link" onclick={() => void act(a)}>{a.kind === "details" ? footCopy.details : commandCopy.showInExplorer}</button>
            {/if}
          </span>
        {:else if slot.startsWith("op:") && latest}
          {@const o = latest}
          <span class="fit" use:fit={`${label(o)}|${bytesOf(o)}|${leftOf(o)}|${running.length}`}>
            <span class="opname ellipsis" title={label(o)}>{label(o)}</span>
            {#if hasBar(o.kind, o.phase)}
              {@render progressLine(o, false)}
              {#if bytesOf(o)}<span class="num opcap">{bytesOf(o)}{#if leftOf(o)}<span class="opleft">{` · ${leftOf(o)}`}</span>{/if}</span>{/if}
            {/if}
            {#if showsCancel(o.kind, o.phase)}{@render cancelButton(o)}{/if}
            {#if running.length > 1}
              <button type="button" class="btn subtle xs" data-pop-toggle data-owns-esc aria-expanded={open === "ops"} aria-label={footCopy.moreLabel(running.length - 1)} onclick={() => toggle("ops")}>{footCopy.more(running.length - 1)}</button>
            {/if}
          </span>
        {:else}
          <span class="note ellipsis">{store.footNote}</span>
        {/if}
      </div>
    {/key}
  </div>

  {#if store.unlocked && st}
    <span class="foot-right">
      {#if notices.length > 0}
        <!-- The standing notices, gathered (direction 4; the InfoBar still
             marks the moment one arrives, ruled 2026-10-13). The Tampered
             one is the page's banner and is not listed. -->
        <button type="button" class="btn subtle xs notice-btn" data-pop-toggle data-owns-esc aria-expanded={open === "notices"} onclick={() => toggle("notices")}><span class="att-dot {tier(notices[0])}" aria-hidden="true"></span>{footCopy.notices(notices.length)}</button>
      {/if}
      <!-- The nearer of the idle deadline and the absolute one (APP.md §6,
           ruled 2026-09-12): the absolute cap locks regardless of activity,
           so the idle one alone would promise time the vault does not have.
           Quiet until the last minute (ruled 2026-10-14, lib/format.ts
           lockLine): the idle rule or the cap's clock time, then a count in
           the attention colour. Stay unlocked is the idle minute's alone:
           it sends Activity at once, not through the page's throttle, and
           nothing extends the cap (lib/foot.ts staysUnlocked). -->
      <span class="lockchip" class:urgent={line?.urgent}><svg class="i i-14"><use href="#i-unlock" /></svg>{line?.text}</span>
      {#if stay}<button type="button" class="btn subtle xs" onclick={() => Vault.Activity().catch((e) => store.notice(codeText(errorOf(e).code), "error"))}>{footCopy.stayUnlocked}</button>{/if}
      <button type="button" class="btn subtle xs" title={keyTitles.lockNow} onclick={lockNow}>{commandCopy.lockNow}</button>
    </span>
  {:else}
    <!-- *Locked*, the lock header's own word (ruled 2026-10-17: it read
         "Keystore locked", a format word; APP.md §7). -->
    <span class="lockchip"><svg class="i i-14"><use href="#i-lock" /></svg>{chipCopy.locked}<button type="button" class="btn subtle xs" onclick={() => store.unlock()}>{commandCopy.unlock}</button></span>
  {/if}

  {#if open === "ops"}
    <div class="foot-pop ops-pop" role="group" aria-label={footCopy.flyoutLabel} transition:fly={motion(FAST, { y: 4, easing: enter })}>
      {#each running as o (o.id)}
        {@const cap = progressCaption(o.done, o.total, leftOf(o))}
        <div class="frow">
          <div class="frow-main">
            <span class="frow-name ellipsis" title={label(o)}>{label(o)}</span>
            {#if cap}<span class="num opcap ellipsis">{cap}</span>{/if}
            {#if hasBar(o.kind, o.phase)}{@render progressLine(o, true)}{/if}
          </div>
          {#if showsCancel(o.kind, o.phase)}
            {@render cancelButton(o)}
          {:else if o.kind === "compact" || o.kind === "rotate"}
            <span class="runs">{footCopy.runsToEnd}</span>
          {/if}
        </div>
      {/each}
    </div>
  {:else if open === "notices"}
    <div class="foot-pop notice-pop" role="group" aria-label={footCopy.noticesLabel} transition:fly={motion(FAST, { y: 4, easing: enter })}>
      {#each notices as code (code)}
        <div class="nrow {tier(code)}"><svg class="i i-14"><use href="#{tierIcon(tier(code))}" /></svg><span>{warningCopy(code)}</span></div>
      {/each}
    </div>
  {/if}
</div>

<style>
  /* The slot is a one-cell grid, so the old line and the new one share it
     while they cross-fade and nothing beside them moves. */
  .foot-left { flex: 1; min-width: 0; display: grid; align-items: center; height: 100%; }
  .foot-slot { grid-area: 1 / 1; display: flex; align-items: center; gap: var(--s3); min-width: 0; }
  .note { min-width: 0; }
  /* The running line's parts in one row. The name is the one part that
     can shrink, and only once fit has taken the extras away. */
  .fit { display: flex; align-items: center; gap: var(--s3); min-width: 0; }
  .opname { flex: 0 1 auto; min-width: 0; color: var(--ink); }
  .opcap { flex: none; color: var(--ink-2); }
  /* data-fit is the fit action's, set on the element, so it is global. */
  .fit:global([data-fit="1"]) .opleft, .fit:global([data-fit="2"]) .opleft { display: none; }
  .fit:global([data-fit="2"]) :global(.fprog) { width: 64px; }
  .done { display: flex; align-items: center; gap: var(--s2); min-width: 0; color: var(--ink); }
  .done .i { color: var(--accent-ink); }
  .done.neutral, .done.neutral .i { color: var(--ink-2); }
  .done .btn.link { flex: none; font: var(--t-cap); margin-left: var(--s1); }
  .foot-right { display: inline-flex; align-items: center; gap: var(--s2); flex: none; }
  .lockchip.urgent, .lockchip.urgent .i { color: var(--attention); transition: color var(--dur-out) var(--ease-enter); }
  .notice-btn { gap: var(--s1); }
  /* The dot wears the highest tier gathered: attention, or the third ink
     when every notice only informs (direction 9: info is never amber). */
  .att-dot { width: 6px; height: 6px; border-radius: 50%; background: var(--attention); flex: none; }
  .att-dot.info { background: var(--ink-3); }

  /* The flyout's rows, 40 px each, carrying what the d4 mock's rows say:
     the name; under it the bytes and the time left; under both, the line
     the whole width of the text. Cancel, or "Runs to the end", stands at
     the right. */
  .frow { display: flex; align-items: center; gap: var(--s3); height: 40px; padding: 0 var(--s2); }
  .frow + .frow { border-top: 1px solid var(--stroke); }
  .frow-main { flex: 1; min-width: 0; display: flex; flex-direction: column; }
  .frow-name { min-width: 0; color: var(--ink); }
  .frow-main .opcap { min-width: 0; }
  .frow-main :global(.fprog) { width: 100%; margin-top: 2px; }
  .runs { flex: none; color: var(--ink-2); }
  .nrow { display: flex; align-items: flex-start; gap: var(--s2); padding: var(--s2); color: var(--ink); font: var(--t-cap); }
  .nrow .i { margin-top: 1px; }
  .nrow.attention .i { color: var(--attention); }
  .nrow.info .i { color: var(--ink-2); }
  .nrow.danger .i { color: var(--danger); }
</style>

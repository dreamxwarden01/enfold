<script lang="ts">
  import { store } from "../lib/state.svelte";
  import type { Route } from "../lib/state.svelte";
  import { Vault, errorOf } from "../lib/api";
  import { fade, fly } from "svelte/transition";
  import { MediaQuery } from "svelte/reactivity";
  import { motion, delay, enter, GAP, MOVE, FAST, LEAVE } from "../lib/motion";
  import { RAIL_COMPACT_QUERY, railChildren, railCurrent, vaultButtonLabel, vaultHead } from "../lib/rail";
  import { railMark } from "../lib/foot";
  import { codeText, railCopy, settingsCopy, vaultCopy } from "../lib/strings";
  import { tip } from "../lib/tip";

  // The rail (APP.md §6, Rail; directions 3, 6 and 10, ruled 2026-10-18).
  // Expanded at a window of 1008 px and more: the brand row (direction 5's
  // caption is phase 5's), the vault head, *Archives* with a row for each
  // open archive under it, *Vault*, the gap, *Settings*. Compact below
  // that: 48 px of icons with the app's tooltips, a padlock for the vault
  // head, and the labels back in a flyout from the toggle at the top.
  // The widths are app.css's tokens (--rail-w, --rail-w-compact), read by
  // .shell under the same query.
  const narrow = new MediaQuery(RAIL_COMPACT_QUERY, false);
  const compact = $derived(narrow.current);

  // The vault's page is *Vault* (ruled 2026-10-17; it was "Keystore",
  // a format word, while every sentence said "vault"). The route keeps
  // its name: it is code, not copy.
  const items: { route: Route; label: string; icon: string }[] = [
    { route: "archives", label: railCopy.archives, icon: "i-archives" },
    { route: "keys", label: vaultCopy.title, icon: "i-keystore" },
  ];

  // The rail's tips stand beside their items, not under them (lib/tip.ts
  // tipBeside; the final shoot of phase 4a): items 36 to 40 px apart, a
  // tip under one covered the next — the open archive under *Archives*,
  // the item drawn current under a row's ×.

  // Work in progress, seen from every page (direction 4): an accent dot on
  // Archives while anything runs, a ring filled to the percentage while
  // exactly one runs with a known total (lib/foot.ts railMark).
  const mark = $derived(railMark(store.runningOps));
  const head = $derived(vaultHead(store.status, store.vaultName));
  const headLabel = $derived(vaultButtonLabel(head));
  const here = $derived(railCurrent(store.route, store.current));
  // The open archives (lib/rail.ts): the page's own by its heading until
  // a status names it, then every one the status names.
  const currentName = $derived(store.page?.crumbs?.[0]?.name || store.archives.find((a) => a.id === store.current)?.name || "");
  const children = $derived(railChildren(store.current, currentName, store.status?.opened));

  // A row goes back to its archive's page: the one the store holds as it
  // was left, another — a second left open by a lock — by opening it,
  // which leaves the first (APP.md §2.3).
  function showArchive(id: string) {
    if (id === store.current) store.go("archive");
    else void store.openArchive(id);
  }

  // The padlock's press is the foot's own act: Lock now while unlocked,
  // Unlock otherwise — the lock scene, which says what comes first.
  function vaultAct() {
    if (head.unlocked) Vault.Lock().catch((e) => store.toast(codeText(errorOf(e).code), "error"));
    else store.unlock();
  }

  // ---- the compact rail's flyout -----------------------------------------
  // The toggle shows the labels as NavigationView does: a box to the right
  // of the rail on the menu's surface. Escape, a press outside or a choice
  // closes it; Escape gives the focus back to the toggle.
  let flyOpen = $state(false);
  let toggleEl = $state<HTMLButtonElement>();
  let flyEl = $state<HTMLDivElement>();

  $effect(() => {
    if (!compact) flyOpen = false;
  });

  $effect(() => {
    if (flyOpen && flyEl) flyEl.querySelector<HTMLButtonElement>("button")?.focus();
  });

  function choose(fn: () => void) {
    flyOpen = false;
    fn();
  }

  function flyKey(e: KeyboardEvent) {
    if (e.key === "Escape") {
      e.preventDefault();
      flyOpen = false;
      toggleEl?.focus();
      return;
    }
    if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
    const all = [...(flyEl?.querySelectorAll<HTMLButtonElement>("button") ?? [])];
    if (!all.length) return;
    e.preventDefault();
    const at = all.indexOf(document.activeElement as HTMLButtonElement);
    const next = e.key === "ArrowDown" ? (at + 1) % all.length : (at - 1 + all.length) % all.length;
    all[next]?.focus();
  }

  function outside(e: PointerEvent) {
    if (!flyOpen) return;
    const t = e.target as Node;
    if (flyEl?.contains(t) || toggleEl?.contains(t)) return;
    flyOpen = false;
  }
</script>

<svelte:window onpointerdown={outside} />

<nav class="rail" class:compact aria-label={railCopy.label} in:fly={motion(MOVE, { x: -12, delay: delay(GAP), easing: enter })}>
  {#if compact}
    <button
      type="button" class="rail-btn" bind:this={toggleEl}
      aria-label={flyOpen ? railCopy.closeNav : railCopy.openNav} aria-expanded={flyOpen} aria-controls="rail-fly"
      use:tip={{ text: flyOpen ? railCopy.closeNav : railCopy.openNav, side: "end" }}
      onclick={() => (flyOpen = !flyOpen)}
    ><svg class="i"><use href="#i-menu" /></svg></button>
    <div class="brand" aria-hidden="true"><svg class="mark i" viewBox="0 0 20 20"><use href="#i-mark" /></svg></div>
    <!-- The vault head as a state button (direction 6): the padlock open
         while unlocked, closed otherwise; its name says the vault, the
         state and what a press does. -->
    <button type="button" class="rail-btn vault-state" class:open={head.unlocked} aria-label={headLabel} use:tip={{ text: headLabel, side: "end" }} onclick={vaultAct}>
      <svg class="i"><use href={head.unlocked ? "#i-unlock" : "#i-lock"} /></svg>
    </button>
    {#each items as it (it.route)}
      <button type="button" class="rail-item" aria-current={here === it.route ? "page" : undefined} aria-label={it.label} use:tip={{ text: it.label, side: "end" }} onclick={() => store.go(it.route)}>
        <span class="rail-ico">
          <svg class="i"><use href="#{it.icon}" /></svg>
          {#if it.route === "archives" && mark !== "none"}
            <span class="rail-mark" class:ring={typeof mark === "object"} style:--pct={typeof mark === "object" ? mark.ring : null} aria-hidden="true"></span>
          {/if}
        </span>
      </button>
      {#if it.route === "archives"}
        <!-- The open archive, an indented glyph (direction 6): current
             while its page shows, named in its tooltip; it has no × here —
             the head's Close is the way, so nothing in the compact rail
             closes an archive by accident. -->
        {#each children as c (c.id)}
          <button type="button" class="rail-item rail-child" aria-current={here === `archive:${c.id}` ? "page" : undefined} aria-label={c.name} use:tip={{ text: c.name, side: "end" }} onclick={() => showArchive(c.id)}>
            <svg class="i i-20"><use href="#i-box" /></svg>
          </button>
        {/each}
      {/if}
    {/each}
    <div class="rail-gap"></div>
    <div class="rail-foot">
      <button type="button" class="rail-item" aria-current={here === "settings" ? "page" : undefined} aria-label={settingsCopy.title} use:tip={{ text: settingsCopy.title, side: "end" }} onclick={() => store.go("settings")}>
        <span class="rail-ico"><svg class="i"><use href="#i-settings" /></svg></span>
      </button>
    </div>

    {#if flyOpen}
      <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
      <div class="rail-fly" id="rail-fly" role="group" aria-label={railCopy.label} bind:this={flyEl} onkeydown={flyKey} in:fly={motion(FAST, { x: -4 })} out:fade={motion(LEAVE)}>
        <button type="button" class="vault-head" onclick={() => choose(() => store.go("keys"))}>
          <span class="t-strong ellipsis">{head.name}</span><span class="vault-word">{head.word}</span>
        </button>
        {#each items as it (it.route)}
          <button type="button" class="rail-item" aria-current={here === it.route ? "page" : undefined} onclick={() => choose(() => store.go(it.route))}>
            <svg class="i"><use href="#{it.icon}" /></svg>{it.label}
          </button>
          {#if it.route === "archives"}
            {#each children as c (c.id)}
              <button type="button" class="rail-item rail-child" aria-current={here === `archive:${c.id}` ? "page" : undefined} onclick={() => choose(() => showArchive(c.id))}>
                <svg class="i i-20"><use href="#i-box" /></svg><span class="ellipsis">{c.name}</span>
              </button>
            {/each}
          {/if}
        {/each}
        <div class="rail-foot">
          <button type="button" class="rail-item" aria-current={here === "settings" ? "page" : undefined} onclick={() => choose(() => store.go("settings"))}>
            <svg class="i"><use href="#i-settings" /></svg>{settingsCopy.title}
          </button>
        </div>
      </div>
    {/if}
  {:else}
    <div class="brand"><svg class="mark i" viewBox="0 0 20 20"><use href="#i-mark" /></svg>Enfold</div>
    <!-- The vault head (direction 10): the vault's name over its state in
         the lock header's words, a quiet way to the Vault page — the Vault
         item below stays the one drawn current there. -->
    <button type="button" class="vault-head" title={railCopy.vaultPage} onclick={() => store.go("keys")}>
      <span class="t-strong ellipsis">{head.name}</span><span class="vault-word">{head.word}</span>
    </button>
    {#each items as it (it.route)}
      <button type="button" class="rail-item" aria-current={here === it.route ? "page" : undefined} onclick={() => store.go(it.route)}>
        <svg class="i"><use href="#{it.icon}" /></svg>{it.label}
        {#if it.route === "archives" && mark !== "none"}
          <span class="rail-mark" class:ring={typeof mark === "object"} style:--pct={typeof mark === "object" ? mark.ring : null} aria-hidden="true"></span>
        {/if}
      </button>
      {#if it.route === "archives"}
        <!-- The open archive as a place (direction 3): a row under
             Archives, current while its page shows, that a visit to Vault
             or Settings keeps; its × is the graceful leave, the head's
             Close by another name (APP.md §2.3). -->
        {#each children as c (c.id)}
          {@const name = c.name}
          <div class="rail-item rail-child" class:current={here === `archive:${c.id}`} data-tip-anchor>
            <button type="button" class="rail-go" aria-current={here === `archive:${c.id}` ? "page" : undefined} use:tip={{ text: name, cut: true, side: "end" }} onclick={() => showArchive(c.id)}>
              <svg class="i i-20"><use href="#i-box" /></svg><span class="ellipsis">{name}</span>
            </button>
            <!-- Gone once a Close has left it and something still holds
                 it: a second Leave does nothing, and the kill switch is
                 the head's or the inspector's *Force close* (the review of
                 2026-10-18). -->
            {#if !store.leftHeld(c.id)}
              <button type="button" class="rail-x" aria-label={railCopy.closeArchive(name)} use:tip={{ text: railCopy.closeArchive(name), side: "end" }} onclick={() => void store.closeArchive(c.id)}>
                <svg class="i i-14"><use href="#i-x" /></svg>
              </button>
            {/if}
          </div>
        {/each}
      {/if}
    {/each}
    <div class="rail-gap"></div>
    <div class="rail-foot">
      <button type="button" class="rail-item" aria-current={here === "settings" ? "page" : undefined} onclick={() => store.go("settings")}>
        <svg class="i"><use href="#i-settings" /></svg>{settingsCopy.title}
      </button>
    </div>
  {/if}
</nav>

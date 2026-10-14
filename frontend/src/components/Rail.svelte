<script lang="ts">
  import { store } from "../lib/state.svelte";
  import type { Route } from "../lib/state.svelte";
  import { fly } from "svelte/transition";
  import { motion, delay, enter, GAP, MOVE } from "../lib/motion";
  import { leavesHint } from "../lib/rail";

  const items: { route: Route; label: string; icon: string }[] = [
    { route: "archives", label: "Archives", icon: "i-archives" },
    { route: "keys", label: "Keystore", icon: "i-keystore" },
  ];

  function current(route: Route): boolean {
    if (route === "archives") return store.route === "archives" || store.route === "archive";
    return store.route === route;
  }

  // Every item leaves an open archive (APP.md §2.3), the current Archives
  // one included, so each says so on hover until the rail shows the
  // archive as a place of its own (lib/rail.ts).
  const leaves = $derived(
    leavesHint({
      route: store.route,
      current: store.current,
      pageName: store.page?.crumbs?.[0]?.name,
      listName: store.archives.find((a) => a.id === store.current)?.name,
    }),
  );
</script>

<nav class="rail" aria-label="Main" in:fly={motion(MOVE, { x: -12, delay: delay(GAP), easing: enter })}>
  <div class="brand"><svg class="mark i" viewBox="0 0 20 20"><use href="#i-mark" /></svg>Enfold</div>
  {#each items as it (it.route)}
    <button type="button" class="rail-item" aria-current={current(it.route) ? "page" : undefined} title={leaves} onclick={() => store.go(it.route)}>
      <svg class="i"><use href="#{it.icon}" /></svg>{it.label}
    </button>
  {/each}
  <div class="rail-gap"></div>
  <div class="rail-foot">
    <button type="button" class="rail-item" aria-current={store.route === "settings" ? "page" : undefined} title={leaves} onclick={() => store.go("settings")}>
      <svg class="i"><use href="#i-settings" /></svg>Settings
    </button>
  </div>
</nav>

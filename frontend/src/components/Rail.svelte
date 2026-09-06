<script lang="ts">
  import { store } from "../lib/state.svelte";
  import type { Route } from "../lib/state.svelte";

  const items: { route: Route; label: string; icon: string }[] = [
    { route: "archives", label: "Archives", icon: "i-archives" },
    { route: "keys", label: "Keystore", icon: "i-keystore" },
  ];

  function current(route: Route): boolean {
    if (route === "archives") return store.route === "archives" || store.route === "archive";
    return store.route === route;
  }
</script>

<nav class="rail" aria-label="Main">
  <div class="brand"><svg class="mark i" viewBox="0 0 20 20"><use href="#i-mark" /></svg>Enfold</div>
  {#each items as it (it.route)}
    <button type="button" class="rail-item" aria-current={current(it.route) ? "page" : undefined} onclick={() => store.go(it.route)}>
      <svg class="i"><use href="#{it.icon}" /></svg>{it.label}
    </button>
  {/each}
  <div class="rail-gap"></div>
  <div class="rail-foot">
    <button type="button" class="rail-item" aria-current={store.route === "settings" ? "page" : undefined} onclick={() => store.go("settings")}>
      <svg class="i"><use href="#i-settings" /></svg>Settings
    </button>
  </div>
</nav>

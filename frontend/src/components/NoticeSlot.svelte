<script lang="ts">
  // The notice slot (direction 4 of the design review, phase 4b; APP.md §6,
  // Notices): one place for what a page has to say, between its toolbar and
  // its list on the Archives and Archive pages, at the top of the body on
  // the Vault and Settings pages.
  //
  // It holds the page's stateful banners — the ones that change what the
  // page can do — in flow, in direction 9's fixed order: danger (the
  // Tampered banner), then attention (the locked line, needs reopening,
  // the copy mismatch), then info. The page writes them in that order, each
  // in a <div class="fold" transition:unfold>, so a banner that appears takes
  // its tier's place rather than pushing in at the top, and it opens and
  // closes its height (lib/motion.ts unfold: grid rows 0fr → 1fr over 220 ms
  // on the entering curve, the opacity with it; none under reduced motion)
  // — the list under it slides rather than jumps.
  //
  // On the two list pages the InfoBars float at the slot's bottom edge,
  // which is the list's top, over the list and within its column
  // (InfoBars.svelte; beside): they never move it. So the list moves
  // only when the page's state changes, and then smoothly.
  //
  // The slot takes back the container's gap it would add when empty
  // (app.css .nslot): --slot-gap is that gap, the layer body's 12 px by
  // default and the Vault page column's 24.
  import type { Snippet } from "svelte";
  import InfoBars from "./InfoBars.svelte";

  // flow: the bars stand in the slot's flow instead of over the list —
  // wherever there is no list to hold still: the lock screen's stage and
  // the boot failure's, whose panel's heading a floating bar would cover,
  // and the Vault and Settings columns, whose first heading and commands it
  // covered (the final shoot of phase 4b).
  // beside: the side pane standing beside the list under the slot — the
  // Archives page's details or the Archive page's preview — so the bars
  // stop at the list's column rather than run over the pane (app.css
  // .nslot.beside-*). Undefined when the list has the whole width.
  let { children, flow = false, beside }: { children?: Snippet; flow?: boolean; beside?: "archives" | "files" } = $props();
</script>

<div class="nslot {beside ? `beside-${beside}` : ''}" class:flow>
  {@render children?.()}
  <div class="nslot-bars"><InfoBars {flow} /></div>
</div>

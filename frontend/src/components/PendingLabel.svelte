<script lang="ts">
  // A button's label that changes while the button's wait is pending
  // (direction 4, phase 3; lib/pending.ts and strings.ts pendingCopy):
  // "Open" becomes "Opening…" with the ring where the icon stood. Nothing
  // beside the button may move while it waits (APP.md §6), so both labels
  // share one grid cell and the one not shown is hidden: the button is as
  // wide as the wider of the two from the start (app.css .swap). The
  // button's own .pending ring is not drawn — the ring stands in the
  // waiting label instead, so it adds no width either.
  interface Props {
    // armed: the wait has passed its 300 ms (the action's `armed`).
    armed: boolean;
    rest: string;
    busy: string;
    // icon: the rest label's icon, by its sprite id.
    icon?: string;
  }
  let { armed, rest, busy, icon }: Props = $props();
</script>

<span class="swap"><span class:off={armed}>{#if icon}<svg class="i i-14"><use href="#{icon}" /></svg>{/if}{rest}</span><span class="busy" class:off={!armed}>{busy}</span></span>

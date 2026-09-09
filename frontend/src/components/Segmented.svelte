<script lang="ts">
  // A segmented control: one choice out of a few, side by side, with the
  // highlight sliding to what was picked (APP.md §6, Motion — MOVE and the
  // enter easing; reduced motion snaps, as every --dur- token does).
  // Radiogroup semantics: one tab stop, the arrows move the choice.

  interface Option {
    value: string;
    label: string;
  }
  interface Props {
    id: string;
    label: string; // names the group for a screen reader
    options: readonly Option[];
    value: string;
  }
  let { id, label, options, value = $bindable() }: Props = $props();

  const index = $derived(Math.max(0, options.findIndex((o) => o.value === value)));

  function pick(i: number) {
    const o = options[i];
    if (!o) return;
    value = o.value;
    document.getElementById(`${id}-${o.value}`)?.focus();
  }

  function keydown(e: KeyboardEvent) {
    const n = options.length;
    switch (e.key) {
      case "ArrowRight":
      case "ArrowDown":
        pick((index + 1) % n);
        break;
      case "ArrowLeft":
      case "ArrowUp":
        pick((index - 1 + n) % n);
        break;
      case "Home":
        pick(0);
        break;
      case "End":
        pick(n - 1);
        break;
      default:
        return;
    }
    e.preventDefault();
  }
</script>

<div class="seg" role="radiogroup" aria-label={label} tabindex="-1" onkeydown={keydown}>
  <span class="seg-hl" aria-hidden="true" style="width: calc((100% - 4px) / {options.length}); transform: translateX({index * 100}%);"></span>
  {#each options as o, i (o.value)}
    <button
      type="button"
      id="{id}-{o.value}"
      class="seg-btn"
      class:on={o.value === value}
      role="radio"
      aria-checked={o.value === value}
      tabindex={i === index ? 0 : -1}
      onclick={() => (value = o.value)}
    >{o.label}</button>
  {/each}
</div>

<style>
  .seg {
    position: relative;
    display: grid;
    grid-auto-flow: column;
    grid-auto-columns: 1fr;
    padding: 2px;
    background: var(--surface-alt);
    border: 1px solid var(--ctl-stroke);
    border-radius: var(--r-ctl);
  }
  .seg-hl {
    position: absolute;
    top: 2px;
    bottom: 2px;
    left: 2px;
    /* the track is the padded box, so the width is of it, not of the border box */
    box-sizing: border-box;
    background: var(--ctl);
    border: 1px solid var(--ctl-stroke);
    border-bottom-color: var(--ctl-bottom);
    border-radius: var(--r-sm);
    box-shadow: var(--shadow-card);
    transition: transform var(--dur-move) var(--ease-enter);
    pointer-events: none;
  }
  .seg-btn {
    position: relative;
    z-index: 1;
    appearance: none;
    background: none;
    border: none;
    border-radius: var(--r-sm);
    padding: 5px 4px;
    font: inherit;
    font-size: 12.5px;
    color: var(--ink-2);
    cursor: pointer;
    transition: color var(--dur-out) var(--ease-enter);
  }
  .seg-btn:hover { color: var(--ink); transition-duration: var(--dur-hover); }
  .seg-btn.on { color: var(--ink); font-weight: 600; }
</style>

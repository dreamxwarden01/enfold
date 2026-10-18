<script lang="ts" module>
  // One command row: a strong first line that names the act and a caption
  // that says its consequence. A greyed row keeps its reason in place of
  // the consequence, and as its title (APP.md §6, Controls).
  export interface CommandRow {
    label: string;
    says: string;
    run: () => void;
    disabled?: boolean;
    reason?: string;
  }
</script>

<script lang="ts">
  // The command rows of Explorer's own "Replace or Skip Files" (direction
  // 7; APP.md §3, the conflict question and the add collisions): each
  // answer a full-width standard button of equal weight, none of them the
  // accent, so the button that looks like the default is never the mass
  // overwrite (ruled 2026-10-14). The rows stand in the dialog's body; a
  // Cancel, where the dialog has one, stays in the foot band.
  interface Props {
    rows: CommandRow[];
  }
  let { rows }: Props = $props();
</script>

<div class="cmdrows" role="group">
  {#each rows as r, i (i)}
    <button type="button" class="btn cmdrow" disabled={r.disabled} title={r.disabled ? r.reason : undefined} onclick={r.run}>
      <svg class="i i-14" aria-hidden="true"><use href="#i-chevron" /></svg>
      <span class="cmdrow-text">
        <b>{r.label}</b>
        <small>{r.disabled && r.reason ? r.reason : r.says}</small>
      </span>
    </button>
  {/each}
</div>

<style>
  /* The rows stand 8 apart, each a standard button grown to its two
     lines: 8 above and below, the body's 12 at the sides, the chevron
     centred on the first 20 px line (APP.md §6, Type and spacing). */
  .cmdrows { display: flex; flex-direction: column; gap: var(--s2); }
  .cmdrow { width: 100%; height: auto; min-height: 32px; padding: var(--s2) var(--s3); align-items: flex-start; gap: var(--s2); white-space: normal; text-align: left; }
  .cmdrow > .i { margin-top: 3px; color: var(--accent-ink); }
  .cmdrow:disabled > .i { color: inherit; }
  .cmdrow-text { display: flex; flex-direction: column; min-width: 0; }
  .cmdrow-text b { font: var(--t-strong); overflow-wrap: anywhere; }
  .cmdrow-text small { font: var(--t-cap); color: var(--ink-2); }
  .cmdrow:disabled .cmdrow-text small { color: inherit; }
</style>

<script lang="ts">
  // The key art (APP.md §6, ruled 2026-10-17; the mock's): a port, the
  // plug, the YubiKey's body and its LED in a ring. The lock screen's
  // stage and the dialogs' ceremony panel draw the same object, so that a
  // wait for a YubiKey looks the same wherever it is.
  //   listen  the LED breathes: the app is listening for a YubiKey
  //   found   the LED solid: the YubiKey answered
  //   off     plugged in but not this vault's: the LED dark, no dashes
  //           between port and plug, so the screen shows which object is
  //           wrong
  //   still   the LED dim and not breathing: the app has stopped hearing
  //           from the reader (the Smart Card service is not running)
  //   two     two YubiKeys, the second fading: remove one
  interface Props {
    led: "listen" | "found" | "still" | "off" | "two";
  }
  let { led }: Props = $props();
</script>

<div class="keyart" aria-hidden="true">
  {#if led === "two"}
    <svg width="150" height="80" viewBox="0 0 150 80" fill="none">
      <g opacity="0.45">
        <rect x="70" y="4" width="72" height="28" rx="8" class="k-body" stroke-width="1.5" />
        <rect x="62" y="11" width="10" height="14" rx="2" class="k-plug" stroke-width="1.5" />
        <circle cx="126" cy="18" r="7.5" class="k-ring" stroke-width="1.5" />
      </g>
      <rect x="1" y="30" width="52" height="36" rx="4" class="k-port" stroke-width="1.5" />
      <rect x="70" y="40" width="72" height="28" rx="8" class="k-body" stroke-width="1.5" />
      <rect x="62" y="47" width="10" height="14" rx="2" class="k-plug" stroke-width="1.5" />
      <circle cx="126" cy="54" r="7.5" class="k-ring" stroke-width="1.5" />
      <circle cx="126" cy="54" r="2.6" class="k-dot" />
    </svg>
  {:else}
    <svg width="150" height="72" viewBox="0 0 150 72" fill="none">
      <rect x="1" y="18" width="52" height="36" rx="4" class="k-port" stroke-width="1.5" />
      <rect x="9" y="26" width="36" height="20" rx="2" class="k-slot" stroke-width="1.5" />
      {#if led !== "off"}<path d="M53 30h10M53 42h10" class="k-dash" stroke-width="1.5" stroke-dasharray="3 4" />{/if}
      <rect x="70" y="22" width="72" height="28" rx="8" class="k-body" stroke-width="1.5" />
      <rect x="62" y="29" width="10" height="14" rx="2" class="k-plug" stroke-width="1.5" />
      <circle cx="126" cy="36" r="7.5" class={led === "off" ? "k-port" : "k-ring"} stroke-width="1.5" />
      {#if led !== "off"}<circle cx="126" cy="36" r="2.6" class="k-dot" class:breathe={led === "listen"} class:dim={led === "still"} />{/if}
      <path d="M80 30v12M88 30v12" class="k-lines" stroke-width="1.5" stroke-linecap="round" />
    </svg>
  {/if}
</div>

<style>
  .keyart { display: flex; justify-content: center; padding: var(--s1) 0 var(--s2); }
  .k-port { fill: var(--surface-alt); stroke: var(--stroke-strong); }
  .k-slot { fill: var(--ground); stroke: var(--stroke-strong); }
  .k-dash { stroke: var(--stroke-strong); }
  .k-body { fill: var(--surface); stroke: var(--ink-3); }
  .k-plug { fill: var(--surface-alt); stroke: var(--ink-3); }
  .k-ring { fill: var(--accent-wash); stroke: var(--accent); }
  .k-dot { fill: var(--accent); }
  .k-lines { stroke: var(--ink-3); }
  /* Listening: the LED breathes, 0.35 to 1 and back over 1.6 s — the one
     loop beside the touch rings, because here the motion is the state
     (APP.md §6, Motion; ruled 2026-10-17). Still under reduced motion,
     where the lead says the waiting instead. */
  @media (prefers-reduced-motion: no-preference) {
    .k-dot.breathe { animation: breathe var(--loop-listen) ease-in-out infinite; }
  }
  /* Stopped listening: the LED at the breath's low point, held. */
  .k-dot.dim { opacity: 0.35; }
  @keyframes breathe { 0%, 100% { opacity: 0.35; } 50% { opacity: 1; } }
</style>

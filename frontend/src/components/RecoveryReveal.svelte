<script lang="ts">
  // The recovery key, shown once: fetched from the one-time URL the core
  // minted (APP.md §1), rendered in groups, forgotten when dismissed.
  import Dialog from "./Dialog.svelte";

  interface Props {
    url: string;
    ondone: () => void;
  }
  let { url, ondone }: Props = $props();
  let digits = $state("");
  let failed = $state(false);

  $effect(() => {
    let cancelled = false;
    fetch(url, { cache: "no-store" })
      .then((r) => (r.ok ? r.text() : Promise.reject(new Error(String(r.status)))))
      .then((t) => {
        if (!cancelled) digits = t.replace(/\D/g, "");
      })
      .catch(() => {
        if (!cancelled) failed = true;
      });
    return () => {
      cancelled = true;
    };
  });

  // The key is eight groups of six digits; it is shown the way it is typed.
  const groups = $derived(digits.match(/.{1,6}/g) ?? []);
</script>

<Dialog title="Your recovery key" onclose={() => {}}>
  <p>Write these digits down and keep them somewhere safe. They open the vault without a YubiKey or a password, and they are shown only now.</p>
  {#if failed}
    <div class="bar danger"><svg class="i i-14"><use href="#i-warn" /></svg><span>The key could not be shown. Remove this recovery slot and add a new one.</span></div>
  {:else if !digits}
    <p>Loading…</p>
  {:else}
    <div class="digits">
      {#each groups as g, i (i)}<span>{g}</span>{/each}
    </div>
    <p class="t-quiet">{digits.length} digits. Enfold cannot show them again.</p>
  {/if}
  {#snippet actions()}
    <button type="button" class="btn accent" onclick={ondone} disabled={!digits && !failed}>I have written it down</button>
  {/snippet}
</Dialog>

<script lang="ts">
  // Import records… (APP.md §13): a backup or another copy of this vault
  // is opened over a staged copy and proved before anything is listed.
  // Three moments in one dialog —
  //   1. the proof step, which says what the file is and, from its own
  //      plaintext recovery slots, which key would be wanted if no VMK of
  //      this vault opens it: the claim informs and decides nothing;
  //   2. the ceremony (kind `records`), rendered through the shared panel,
  //      since the Keys page deliberately leaves this kind alone;
  //   3. the checklist, from the rows the core classified by running the
  //      merge on the copy, so the row and the write cannot disagree.
  // Nothing of that file is installed, and the handle dies with the
  // dialog, with a lock trigger, or with the merge that consumes it.
  import { CeremonyStep, Vault, errorOf } from "../lib/api";
  import type { FileInfo, IncomingRecord } from "../lib/api";
  import { store } from "../lib/state.svelte";
  import { codeText } from "../lib/strings";
  import { date, dateTime, keyId, leaf } from "../lib/format";
  import { actionLabel, actionNote, actionOf, defaultTicks, differLine, summarise, tickable, tickedIds } from "../lib/merge";
  import Dialog from "./Dialog.svelte";
  import CeremonyPanel from "./CeremonyPanel.svelte";

  interface Props {
    path: string;
    onclose: () => void;
  }
  let { path, onclose }: Props = $props();

  let info = $state<FileInfo | null>(null);
  let looked = $state(false);
  let ticks = $state<Record<string, boolean>>({});
  let merging = $state(false);
  let fetched = $state("");
  // reading: the ceremony was asked for and its first event has not
  // landed yet, so the proof screen does not flash back.
  let reading = $state(false);

  // This page's ceremony, and only this one: the Keys page renders every
  // other kind.
  const cer = $derived(store.ceremony && store.ceremony.kind === "records" ? store.ceremony : null);
  const rows = $derived<IncomingRecord[]>(store.merge?.records ?? []);
  const listed = $derived(!!store.merge);
  const sum = $derived(summarise(rows, ticks));

  function fail(e: unknown) {
    store.toast(codeText(errorOf(e).code), "error");
  }

  // What the file says about itself before any credential (R35: dated,
  // not authenticated).
  $effect(() => {
    void path;
    void (async () => {
      try {
        info = await Vault.InspectFile(path);
      } catch (e) {
        fail(e);
      }
      looked = true;
    })();
  });

  // The ceremony ends at StepRecords with the handle in slotLabel, exactly
  // as the reveal puts its one-time URL there; the list is then a call of
  // its own (APP.md §13).
  $effect(() => {
    const c = cer;
    if (!c || c.step !== CeremonyStep.StepRecords || !c.slotLabel) return;
    const handle = c.slotLabel;
    if (fetched === handle) return;
    fetched = handle;
    void (async () => {
      try {
        const recs = (await Vault.IncomingRecords(handle)) ?? [];
        store.merge = { handle, records: recs };
        ticks = defaultTicks(recs);
        store.dismissCeremony();
      } catch (e) {
        fail(e);
      }
    })();
  });

  // The first ceremony event ends the staging note; a ceremony that is
  // cancelled drops to null and the proof screen comes back, rather than
  // leaving "Staging a copy of the file…" standing with nothing behind it
  // (APP.md §6).
  $effect(() => {
    if (cer) reading = false;
  });

  async function read() {
    store.dismissCeremony();
    reading = true;
    try {
      await Vault.InspectRecords(path);
    } catch (e) {
      reading = false;
      fail(e);
    }
  }

  async function merge() {
    const handle = store.merge?.handle;
    if (!handle) return;
    merging = true;
    try {
      await Vault.MergeRecords(handle, tickedIds(rows, ticks));
      store.merge = null;
      await store.refreshArchives();
      onclose();
    } catch (e) {
      fail(e);
    }
    merging = false;
  }

  // Closing discards: the handle is the core's, and it is dropped there as
  // well as here. A ceremony still running is cancelled the way any is.
  function close() {
    if (cer && cer.step !== CeremonyStep.StepDone && cer.step !== CeremonyStep.StepFailed) void Vault.CancelUnlock();
    if (store.merge) store.discardMerge();
    onclose();
  }

  // A lock trigger drops the handle with every held key; the dialog has
  // nothing left to show.
  $effect(() => {
    if (fetched && !store.merge && !cer) onclose();
  });

  function tick(id: string, on: boolean) {
    ticks = { ...ticks, [id]: on };
  }

  const recoveryLines = $derived((info?.recoverySlots ?? []).map((s) => ({ label: s.label, id: keyId(s.recipientId), createdAt: s.createdAt })));
</script>

<Dialog title="Import records" wide onclose={close}>
  {#if listed}
    <p class="t-sub">Ticked records are written into this vault's registry. Nothing of that file is installed, and this vault's own values are kept wherever the two differ.</p>
    <div class="merge-list" role="group" aria-label="Incoming records">
      {#each rows as r (r.archiveId)}
        <div class="mrow" class:skip={!tickable(r)}>
          <input type="checkbox" id="mg-{r.archiveId}" checked={!!ticks[r.archiveId]} disabled={!tickable(r)} onchange={(e) => tick(r.archiveId, e.currentTarget.checked)} />
          <div class="mbody">
            <label for="mg-{r.archiveId}"><b>{r.name || "(unnamed)"}</b><span class="tag">{actionLabel(r)}</span></label>
            {#if actionOf(r) === "forgotten" && r.forgottenAt}
              <span class="note">forgotten here on {date(r.forgottenAt)} — ticking it is an explicit restore</span>
            {:else}
              <span class="note">{actionNote(r)}</span>
            {/if}
            {#if differLine(r.differs)}<span class="note diff">{differLine(r.differs)}</span>{/if}
          </div>
          <div class="mmeta num">{r.createdAt ? date(r.createdAt) : "—"}</div>
        </div>
      {/each}
      {#if rows.length === 0}<div class="empty">That file names no archive this vault could take.</div>{/if}
    </div>
  {:else if cer}
    <CeremonyPanel c={cer} onclose={close} />
  {:else if reading}
    <p class="t-sub">Staging a copy of the file…</p>
  {:else}
    <p class="t-sub">A copy of the file is opened here and proved before anything is listed: this vault's key first, and its own recovery key only if no key of this vault opens it.</p>
    <div class="kv">
      <div class="k">File</div>
      <div class="v" title={path}>{leaf(path)}</div>
      <div></div>
      <div class="k">What it is</div>
      <div class="v">{info ? (info.kind === "backup" ? "a backup — recovery slots only" : "a full vault") : looked ? "unreadable" : "reading…"}</div>
      <div></div>
      {#if info}
        <div class="k">Dated</div>
        <div class="v">{dateTime(info.modifiedAt)}</div>
        <div></div>
        <div class="k">Claims to be</div>
        <div class="v">{info.vaultMatches ? "this vault" : "another vault"} · generation {info.generation}</div>
        <div></div>
      {/if}
    </div>
    {#if recoveryLines.length > 0}
      <div class="bar">
        <svg class="i i-14"><use href="#i-recovery" /></svg>
        <div class="grow">
          <span>If no key of this vault opens it — another vault's file, or a backup from a generation this vault no longer keeps — its own recovery key is asked for. That file names {recoveryLines.length === 1 ? "one recovery key" : `${recoveryLines.length} recovery keys`}:</span>
          <ul class="slotlist">
            {#each recoveryLines as s (s.label + s.id)}
              <li>{s.label}{s.id ? ` · ${s.id}` : ""} · {date(s.createdAt)}</li>
            {/each}
          </ul>
        </div>
      </div>
    {/if}
  {/if}

  {#snippet actions()}
    {#if listed}
      <button type="button" class="btn" disabled={merging} onclick={close}>Discard</button>
      <button type="button" class="btn accent" disabled={merging || sum.total === 0} onclick={merge}>{merging ? "Writing…" : sum.total === 0 ? "Nothing ticked" : `Merge ${sum.total} record${sum.total === 1 ? "" : "s"}`}</button>
    {:else if !cer && !reading}
      <button type="button" class="btn" onclick={close}>Cancel</button>
      <button type="button" class="btn accent" disabled={!looked} onclick={read}>Read the records</button>
    {:else if !cer}
      <!-- staging: nothing to cancel yet in the core -->
      <button type="button" class="btn" onclick={close}>Cancel</button>
    {/if}
    <!-- While the ceremony runs the panel carries its own Cancel (APP.md §6). -->
  {/snippet}
</Dialog>

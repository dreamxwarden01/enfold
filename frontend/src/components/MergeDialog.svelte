<script lang="ts">
  // Import archives from a backup… (APP.md §13; *Import records…* until
  // 2026-10-17): a backup or another copy of this vault is opened over a
  // staged copy and proved before anything is listed. The screen says
  // archives and unlock methods; the registry, its records, the VMK and
  // the generation are this comment's words, not the dialog's.
  // Three moments in one dialog —
  //   1. the proof step, which says what the file is and, from its own
  //      plaintext recovery slots, which key would be wanted if no VMK of
  //      this vault opens it: the claim informs and decides nothing;
  //   2. the ceremony (kind `records`), rendered through the shared panel,
  //      since the Vault page deliberately leaves this kind alone;
  //   3. the checklist, from the rows the core classified by running the
  //      merge on the copy, so the row and the write cannot disagree.
  // Nothing of that file is installed, and the handle dies with the
  // dialog, with a lock trigger, or with the merge that consumes it.
  import { CeremonyStep, Vault, errorOf } from "../lib/api";
  import type { FileInfo, IncomingRecord } from "../lib/api";
  import { store } from "../lib/state.svelte";
  import { chipCopy, codeText, dialogCopy, importCopy, lockCopy, pendingCopy, vaultCopy } from "../lib/strings";
  import { pending } from "../lib/pending";
  import PendingLabel from "./PendingLabel.svelte";
  import { date, dateTime, keyId, leaf, plural } from "../lib/format";
  import { actionOf, defaultTicks, differLine, incomingOf, summarise, tickable, tickedIds } from "../lib/merge";
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
  // The merge has been waited on for 300 ms (lib/pending.ts).
  let mergeArmed = $state(false);
  let fetched = $state("");
  // reading: the ceremony was asked for and its first event has not
  // landed yet, so the proof screen does not flash back.
  let reading = $state(false);

  // This page's ceremony, and only this one: the Vault page renders every
  // other kind.
  const cer = $derived(store.ceremony && store.ceremony.kind === "records" ? store.ceremony : null);
  const rows = $derived<IncomingRecord[]>(store.merge?.records ?? []);
  const listed = $derived(!!store.merge);
  // The file failed its own check (APP.md §13, R25): said in user words —
  // which part of it, and why the rows still stand, is the spec's.
  const tamperedBar = $derived(store.merge?.tampered ? "Something changed that file after it was written, so do not keep it or trust it as a backup. The archives in it can still be imported." : "");
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
        const { records, tampered } = incomingOf(await Vault.IncomingRecords(handle));
        store.merge = { handle, records, tampered };
        ticks = defaultTicks(records);
        store.dismissCeremony();
      } catch (e) {
        fail(e);
      }
    })();
  });

  // The first ceremony event ends the staging note; a ceremony that is
  // cancelled drops to null and the proof screen comes back, rather than
  // leaving "Opening a copy of the file…" standing with nothing behind it
  // (APP.md §6).
  $effect(() => {
    if (cer) reading = false;
  });

  // keep: begun again from the panel's Start again (CeremonyPanel onagain;
  // ruled 2026-10-17) — the failed ceremony stays on the panel until the
  // new one's first event takes its place, so the box does not fall back
  // to the proof screen and return.
  async function read(keep = false) {
    if (!keep) store.dismissCeremony();
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

  // A row's chip in the chips' own words, in sentence case (chipCopy,
  // ruled 2026-10-17); what each action does is lib/merge.ts's.
  function chip(r: IncomingRecord): string {
    switch (actionOf(r)) {
      case "add":
        return chipCopy.new;
      case "version":
        return chipCopy.update;
      case "forgotten":
        return chipCopy.forgotten;
    }
    return chipCopy.alreadyHere;
  }

  // What ticking the row does, in the screen's words: archives, not
  // records or key versions (the glossary, ruled 2026-10-17).
  function note(r: IncomingRecord): string {
    switch (actionOf(r)) {
      case "add":
        return "Not in this vault yet.";
      case "version":
        return "Already in this vault; this copy adds what it is missing.";
      case "forgotten":
        return r.forgottenAt ? `Forgotten here on ${date(r.forgottenAt)}. Ticking it restores it.` : "Forgotten here. Ticking it restores it.";
    }
    return "Already in this vault.";
  }

  const recoveryLines = $derived((info?.recoverySlots ?? []).map((s) => ({ label: s.label, id: keyId(s.recipientId), createdAt: s.createdAt })));
</script>

<!-- Esc is the Cancel, and nothing while the merge is being
     written: a cancel under a running merge dropped the handle the
     merge was using (APP.md §7). -->
<Dialog title={importCopy.archivesTitle} wide onclose={merging ? undefined : close}>
  {#if listed}
    <p class="t-sub">{importCopy.archivesBody}</p>
    <!-- The file failed its own R25 check: a fact about the file, not about
         any row, so it stands above the list and the ticks still work
         (APP.md §13). -->
    {#if tamperedBar}
      <!-- Danger, as the Tampered banner is: the file may have been changed
           outside Enfold (APP.md §6, Notices and their tiers; ruled 2026-10-18). -->
      <div class="bar danger"><svg class="i i-14"><use href="#i-error" /></svg><span>{tamperedBar}</span></div>
    {/if}
    <div class="merge-list" role="group" aria-label="Archives in that file">
      {#each rows as r (r.archiveId)}
        <div class="mrow" class:skip={!tickable(r)}>
          <input type="checkbox" id="mg-{r.archiveId}" checked={!!ticks[r.archiveId]} disabled={!tickable(r)} onchange={(e) => tick(r.archiveId, e.currentTarget.checked)} />
          <div class="mbody">
            <label for="mg-{r.archiveId}"><b>{r.name || "(unnamed)"}</b><span class="tag">{chip(r)}</span></label>
            <span class="note">{note(r)}</span>
            {#if differLine(r.differs)}<span class="note diff">{differLine(r.differs)}</span>{/if}
          </div>
          <div class="mmeta num">{r.createdAt ? date(r.createdAt) : "—"}</div>
        </div>
      {/each}
      {#if rows.length === 0}<div class="empty">That file has no archive this vault could take.</div>{/if}
    </div>
  {:else if cer}
    <CeremonyPanel c={cer} onclose={close} onagain={() => void read(true)} />
  {:else if reading}
    <p class="t-sub">Opening a copy of the file…</p>
  {:else}
    <!-- Consequence first, then what opens it (the dialog template, ruled
         2026-10-17): this vault's own unlock methods, else the file's
         recovery key. -->
    <p class="t-sub">Nothing changes until you choose which archives to import. The file opens with one of this vault's unlock methods, or with its own recovery key if none of them opens it.</p>
    <!-- The file's own facts as the one label · value list (APP.md §6);
         the name alone, the whole path in its title. The words are
         *Inspect a backup…*'s, which reads the same label, save a backup's
         kind: there it opens with its recovery key only, but here this
         vault's own keys are tried first, so the fact makes no claim about
         how the file opens and leaves that to the body (APP.md §13). -->
    <dl class="kv">
      <div><dt>{vaultCopy.inspected.file}</dt><dd title={path}>{leaf(path)}</dd></div>
      <div><dt>{vaultCopy.inspected.kind}</dt><dd>{info ? (info.kind === "backup" ? "A backup" : vaultCopy.inspected.vault) : looked ? "Could not be read" : "Reading…"}</dd></div>
      {#if info}
        <div><dt>{vaultCopy.inspected.dated}</dt><dd class="num">{dateTime(info.modifiedAt)}</dd></div>
        <div><dt>{vaultCopy.inspected.thisVault}</dt><dd>{info.vaultMatches ? vaultCopy.inspected.yes : vaultCopy.inspected.no}</dd></div>
      {/if}
    </dl>
    {#if recoveryLines.length > 0}
      <div class="bar">
        <svg class="i i-14"><use href="#i-recovery" /></svg>
        <div class="grow">
          <span>If no unlock method of this vault opens it, its own recovery key is asked for. That file has {plural(recoveryLines.length, "recovery key")}:</span>
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
    <!-- The act first and the way out after it, in the foot band's equal
         widths (APP.md §6, Controls; ruled 2026-10-17). -->
    {#if listed}
      <!-- [Import 2 archives] [Cancel] (the verb table, ruled 2026-10-17):
           the count is the ticks', and with none ticked the verb stands
           alone, greyed. Cancel discards what was read. -->
      <button type="button" class="btn accent" disabled={merging || sum.total === 0} onclick={merge} use:pending={{ on: merging, armed: (b) => (mergeArmed = b) }}><PendingLabel armed={mergeArmed} rest={importCopy.archivesPrimary(sum.total)} busy={pendingCopy.merging} /></button>
      <button type="button" class="btn" disabled={merging} onclick={close}>{dialogCopy.cancel}</button>
    {:else if !cer && !reading}
      <!-- What happens next is the unlock that opens the file. -->
      <button type="button" class="btn accent" disabled={!looked} onclick={() => void read()}>{lockCopy.continue}</button>
      <button type="button" class="btn" onclick={close}>{dialogCopy.cancel}</button>
    {:else if !cer}
      <!-- staging: nothing to cancel yet in the core -->
      <button type="button" class="btn" onclick={close}>{dialogCopy.cancel}</button>
    {/if}
    <!-- While the ceremony runs its panel draws this band instead: its
         Continue and Cancel follow the step (CeremonyPanel; APP.md §6,
         ruled 2026-10-17). -->
  {/snippet}
</Dialog>

<script lang="ts">
  // Forget key… and Delete archive… (APP.md §13, DESIGN trap 28), one
  // dialog for both pages. Both confirm by the archive's name typed —
  // compared trimmed and exactly, case and all — and the warning says only
  // what is known here: whether a backup of this vault recorded on this
  // machine was taken after the record was created. There is no ceremony:
  // the registry write needs the session's key, and the brakes are the
  // typed name, the retention and the backup.
  //
  // Delete has four outcomes and only two touch the record: the file
  // removed and the record forgotten; a proven mismatch, which asks once
  // more before forgetting; a path that could not be reached, which
  // touches nothing; and a file that could not be read, which keeps the
  // record — a record dropped against a file that is still intact would
  // destroy the keys of an archive nobody could open again.
  import { Archives, Shell, errorOf } from "../lib/api";
  import { store } from "../lib/state.svelte";
  import { codeText } from "../lib/strings";
  import { date } from "../lib/format";
  import { RETENTION_SECONDS } from "../lib/retention";
  import { CONFIRM_NAME, confirmNameProblem } from "../lib/validate";
  import Dialog from "./Dialog.svelte";

  interface Props {
    id: string;
    name: string;
    path: string;
    // createdAt is the record's, for the backup sentence; 0 while the
    // record has not been read, which the sentence treats as unknown.
    createdAt: number;
    mode: "delete" | "forget";
    onclose: () => void;
    // ondone: the record changed — the list and the details want reading
    // again. Not called where nothing was touched.
    ondone?: () => void;
  }
  let { id, name, path, createdAt, mode, onclose, ondone }: Props = $props();

  type Phase = "consent" | "busy" | "done" | "mismatch" | "mismatch-confirm" | "unreachable" | "unreadable" | "failed" | "error";
  let phase = $state<Phase>("consent");
  let problem = $state("");
  let typed = $state("");
  let pressed = $state(false);
  let left = $state(false);
  // Locate… moves the record's path under us; until it does, the path is
  // the record's own.
  let located = $state("");
  const here = $derived(located || path);
  let forgottenAt = $state(0);

  const nameSaid = $derived((left || pressed) && confirmNameProblem(typed, name) ? confirmNameProblem(typed, name) : "");

  // The backup sentence (APP.md §13): a backup of this vault taken at or
  // after the record's own creation holds this archive's key. lastExportAt
  // is a local convenience, unauthenticated, and it gates nothing — with
  // no date, or none we can place against this record, the sentence says
  // the harder thing.
  const backupHolds = $derived(store.lastExportAt > 0 && createdAt > 0 && store.lastExportAt >= createdAt);
  const backupLine = $derived(
    backupHolds
      ? `The last backup of this vault was made on ${date(store.lastExportAt)}; if you still have it, it holds this key.`
      : "No backup of this vault is recorded here — every other copy of this archive becomes unopenable when the record is dropped.",
  );

  function fail(e: unknown): string {
    return errorOf(e).code;
  }

  async function forget() {
    phase = "busy";
    try {
      await Archives.Forget(id);
      forgottenAt = Math.floor(Date.now() / 1000);
      phase = "done";
      ondone?.();
    } catch (e) {
      problem = fail(e);
      phase = "error";
    }
  }

  async function remove() {
    phase = "busy";
    try {
      await Archives.Delete(id, true);
      forgottenAt = Math.floor(Date.now() / 1000);
      phase = "done";
      ondone?.();
    } catch (e) {
      problem = fail(e);
      switch (problem) {
        case "archive.not_this_archive":
          phase = "mismatch";
          break;
        case "archive.file_unreachable":
          phase = "unreachable";
          break;
        case "archive.busy":
        case "archive.invalid":
          phase = "unreadable";
          break;
        case "archive.delete_failed":
          phase = "failed";
          break;
        default:
          phase = "error";
      }
    }
  }

  function go() {
    pressed = true;
    if (confirmNameProblem(typed, name)) return;
    void (mode === "forget" ? forget() : remove());
  }

  async function locate() {
    const p = (await Shell.PickFiles(`Where is ${name} now?`, false)) ?? [];
    if (!p.length) return;
    try {
      await Archives.Locate(id, p[0]);
      located = p[0];
      phase = "consent";
      ondone?.();
    } catch (e) {
      problem = fail(e);
      phase = "error";
    }
  }

  const title = $derived(
    phase === "consent" || phase === "busy"
      ? mode === "forget"
        ? `Forget the key of ${name}?`
        : `Delete ${name}?`
      : phase === "mismatch-confirm"
        ? `Forget the key of ${name}?`
        : phase === "done"
          ? mode === "forget"
            ? "The record is forgotten"
            : "Deleted"
          : "Nothing was removed",
  );
</script>

<Dialog {title} onclose={phase === "busy" ? undefined : onclose}>
  {#if phase === "consent" || phase === "busy"}
    {#if mode === "forget"}
      <p>The record is dropped softly: it keeps its keys for thirty days, shows under <em>Show hidden</em> as forgotten, and <em>Restore</em> brings it back. The file at the path below is left exactly as it is.</p>
    {:else}
      <p>The file is removed first and only then is the record forgotten. Nothing is deleted unless the file at the path below proves to be this archive.</p>
    {/if}
    <div class="kv one">
      <div class="k">File</div>
      <div class="v" title={here}>{here || "—"}</div>
    </div>
    <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span>{backupLine}</span></div>
    <div class="field">
      <div class="field-top"><label for="del-name">Type the archive's name to confirm</label><span class="hint">{name}</span></div>
      <input id="del-name" class="input" class:invalid={!!nameSaid} aria-invalid={nameSaid ? "true" : undefined} bind:value={typed} onblur={() => (left = true)} oninput={() => (left = false)} placeholder={name} disabled={phase === "busy"} />
      {#if nameSaid}<div class="field-error">{nameSaid === CONFIRM_NAME ? CONFIRM_NAME : nameSaid}</div>{/if}
    </div>
  {:else if phase === "done"}
    {#if mode === "forget"}
      <p>{name} is forgotten. It shows under <em>Show hidden</em> and <em>Restore</em> brings it back until its key is dropped, at the first unlock after {date(forgottenAt + RETENTION_SECONDS)}.</p>
    {:else}
      <p>The file was removed and the record forgotten. Its key is kept until the first unlock after {date(forgottenAt + RETENTION_SECONDS)}, so <em>Restore</em> still brings the record back — the file itself is gone.</p>
    {/if}
  {:else if phase === "mismatch"}
    <p>That file is not this archive: the folder was read and either the file is not in it, or the one there holds another archive. <strong>Nothing was removed and nothing was forgotten.</strong></p>
    <p class="t-sub">Locate the archive if it moved, or forget the key on its own and leave whatever is at that path alone.</p>
  {:else if phase === "mismatch-confirm"}
    <p>The file at that path is left exactly as it is. Only the record and its keys are dropped, softly: they are kept for thirty days and <em>Restore</em> brings them back.</p>
    <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span>{backupLine}</span></div>
  {:else if phase === "unreachable"}
    <p>The path could not be reached — the volume, share or folder is not there. <strong>Nothing was removed and nothing was forgotten.</strong></p>
    <div class="kv one"><div class="k">File</div><div class="v" title={here}>{here || "—"}</div></div>
  {:else if phase === "unreadable"}
    <p>{codeText(problem)}</p>
    <p><strong>Nothing was removed and nothing was forgotten.</strong> A record dropped against a file that could not be read destroys the keys of an archive that may still be whole, so the record was kept.</p>
  {:else if phase === "failed"}
    <p>That file is this archive, but it could not be removed. <strong>The record was kept</strong> — it is forgotten only after the file is gone.</p>
  {:else}
    <div class="bar danger"><svg class="i i-14"><use href="#i-warn" /></svg><span>{codeText(problem)}</span></div>
  {/if}

  {#snippet actions()}
    {#if phase === "consent" || phase === "busy"}
      <button type="button" class="btn" disabled={phase === "busy"} onclick={onclose}>Cancel</button>
      <button type="button" class="btn accent" disabled={phase === "busy"} onclick={go}>{phase === "busy" ? "Working…" : mode === "forget" ? "Forget the key" : "Delete archive"}</button>
    {:else if phase === "done"}
      <button type="button" class="btn accent" onclick={onclose}>Close</button>
    {:else if phase === "mismatch"}
      <button type="button" class="btn" onclick={onclose}>Close</button>
      <button type="button" class="btn" onclick={locate}>Locate…</button>
      <button type="button" class="btn accent" onclick={() => (phase = "mismatch-confirm")}>Forget the key only…</button>
    {:else if phase === "mismatch-confirm"}
      <button type="button" class="btn" onclick={() => (phase = "mismatch")}>Go back</button>
      <button type="button" class="btn accent" onclick={() => void forget()}>Forget the key</button>
    {:else if phase === "unreachable"}
      <button type="button" class="btn" onclick={onclose}>Close</button>
      <button type="button" class="btn" onclick={locate}>Locate…</button>
      <button type="button" class="btn accent" onclick={() => void remove()}>Retry</button>
    {:else if phase === "unreadable"}
      <button type="button" class="btn" onclick={onclose}>Close</button>
      <button type="button" class="btn" onclick={locate}>Locate…</button>
      <button type="button" class="btn" onclick={() => (phase = "mismatch-confirm")}>Forget the key only…</button>
      <button type="button" class="btn accent" onclick={() => void remove()}>Retry</button>
    {:else if phase === "failed"}
      <button type="button" class="btn" onclick={onclose}>Close</button>
      <button type="button" class="btn accent" onclick={() => void remove()}>Retry</button>
    {:else}
      <button type="button" class="btn accent" onclick={onclose}>Close</button>
    {/if}
  {/snippet}
</Dialog>

<style>
  .kv.one { margin: 2px 0 10px; }
</style>

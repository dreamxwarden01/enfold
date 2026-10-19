<script lang="ts">
  // Forget archive… and Delete archive… (APP.md §13, DESIGN trap 28), one
  // dialog for both pages, in the dialog template's words (dialogCopy,
  // ruled 2026-10-17): the question that names the archive, what happens
  // to the file and then the safety net, and a primary that repeats the
  // command's verb. Both confirm by the archive's name typed —
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
  import { archivesCopy, codeText, commandCopy, dialogCopy, pendingCopy } from "../lib/strings";
  import { pending } from "../lib/pending";
  import PendingLabel from "./PendingLabel.svelte";
  import { date } from "../lib/format";
  import { RETENTION_SECONDS } from "../lib/retention";
  import { nextMark, onLeave } from "../lib/forms";
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
    // release: what has to let go of the archive before either act can run
    // — its close — run once the name is typed and the act confirmed, and
    // never before (APP.md §13, ruled 2026-10-14): a Cancel must leave the
    // archive exactly as it was. It throws the coded error that stopped
    // it, which ends the dialog with nothing removed.
    release?: () => Promise<void>;
  }
  let { id, name, path, createdAt, mode, onclose, ondone, release }: Props = $props();

  type Phase = "consent" | "busy" | "done" | "mismatch" | "mismatch-confirm" | "unreachable" | "unreadable" | "failed" | "error";
  let phase = $state<Phase>("consent");
  // The act has been waited on for 300 ms (lib/pending.ts).
  let busyArmed = $state(false);
  let problem = $state("");
  let typed = $state("");
  // mark: the typed name is marked under the form rule (APP.md §6,
  // lib/forms.ts) — left or pressed wrong, and not corrected since.
  let mark = $state(false);
  // Locate… moves the record's path under us; until it does, the path is
  // the record's own.
  let located = $state("");
  const here = $derived(located || path);
  let forgottenAt = $state(0);
  // acted: the act the core was last asked for, which is what the outcome
  // reports — not the mode the dialog opened in. A Delete that ends in
  // Forget archive only… forgot the archive and left the file alone, and
  // its done screen must say so (APP.md §13). Empty until an act runs: a
  // release the core refused falls back to the mode's own words.
  let acted = $state<"" | "delete" | "forget">("");
  const did = $derived(acted || mode);

  const nameSaid = $derived(mark && confirmNameProblem(typed, name) ? confirmNameProblem(typed, name) : "");
  const leave = onLeave(() => (mark = nextMark("leave", mark, confirmNameProblem(typed, name))));

  // The backup sentence (APP.md §13): a backup of this vault taken at or
  // after the record's own creation holds this archive's key. lastExportAt
  // is a local convenience, unauthenticated, and it gates nothing — with
  // no date, or none we can place against this record, the sentence says
  // the harder thing.
  const backupHolds = $derived(store.lastExportAt > 0 && createdAt > 0 && store.lastExportAt >= createdAt);
  const backupLine = $derived(backupHolds ? dialogCopy.lastBackup(date(store.lastExportAt)) : dialogCopy.noBackup);
  // The date a forget asked for now would run to: the first unlock after
  // it drops the key (lib/retention.ts).
  const until = $derived(date(Math.floor(Date.now() / 1000) + RETENTION_SECONDS));

  function fail(e: unknown): string {
    return errorOf(e).code;
  }

  async function forget() {
    acted = "forget";
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
    acted = "delete";
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

  // released: the consent is given, so the archive is let go of now. A
  // close the core refused — archive.compacting, a compaction that began
  // while the dialog was up (Close is the kill switch for everything
  // else, readers included) — is said here with its own words, and
  // nothing is removed.
  async function released(): Promise<boolean> {
    if (!release) return true;
    phase = "busy";
    try {
      await release();
      return true;
    } catch (e) {
      problem = fail(e);
      phase = "error";
      return false;
    }
  }

  async function go() {
    if (phase !== "consent") return;
    mark = nextMark("press", mark, confirmNameProblem(typed, name));
    if (confirmNameProblem(typed, name)) return;
    if (!(await released())) return;
    await (mode === "forget" ? forget() : remove());
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

  // A question while something is asked; what happened once it is over,
  // keyed on the act that ran (did), never on the mode. Every outcome that
  // touched nothing says so in its title, and an error in a forget says
  // what a forget would have done.
  const title = $derived(
    phase === "consent" || phase === "busy"
      ? mode === "forget"
        ? dialogCopy.forgetArchive.title(name)
        : dialogCopy.deleteArchive.title(name)
      : phase === "mismatch-confirm"
        ? dialogCopy.forgetOnly.title(name)
        : phase === "done"
          ? did === "forget"
            ? dialogCopy.forgotten
            : `${name} is deleted`
          : phase === "error" && did === "forget"
            ? "Nothing was forgotten"
            : dialogCopy.notThisArchive.title,
  );
</script>

<!-- Esc: nothing while busy, Go back where that is the way out, else the
     Cancel or the Close; the backdrop closes only the phases whose one
     action is Close (APP.md §7). -->
<!-- While it asks, the dialog is a form (APP.md §6, Forms; phase 0's
     form-dialog rule): it opens with the focus in the name field, not on
     the box, and Enter there presses the primary, which marks the field
     if the name does not match. -->
<Dialog {title} onclose={phase === "busy" ? undefined : phase === "mismatch-confirm" ? () => (phase = "mismatch") : onclose} dismissable={phase === "done" || phase === "error"} onsubmit={phase === "consent" || phase === "busy" ? () => void go() : undefined}>
  {#if phase === "consent" || phase === "busy"}
    {#if mode === "forget"}
      <p>{dialogCopy.forgetArchive.body(until)}</p>
      <!-- A path is the stacked label · value: it wraps, never cut (APP.md
           §6). Delete names it in its body; Forget leaves it, so says
           which file stays. -->
      <dl class="kv stk"><dt>{archivesCopy.facts.file}</dt><dd>{here || "—"}</dd></dl>
    {:else}
      <!-- A record with no path yet still reads as a sentence. -->
      <p class="path-body">{dialogCopy.deleteArchive.body(here || "its file")}</p>
    {/if}
    <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span>{backupLine}</span></div>
    <!-- The name to type stands once, as the label's hint; a placeholder
         saying it again read as a value already filled in (ruled
         2026-10-17). -->
    <div class="field">
      <div class="field-top"><label for="del-name">{dialogCopy.confirmName}</label><span class="hint">{name}</span></div>
      <input id="del-name" class="input" class:invalid={!!nameSaid} aria-invalid={nameSaid ? "true" : undefined} bind:value={typed} onblur={leave} oninput={() => (mark = nextMark("change", mark, confirmNameProblem(typed, name)))} disabled={phase === "busy"} />
      {#if nameSaid}<div class="field-error">{nameSaid === CONFIRM_NAME ? CONFIRM_NAME : nameSaid}</div>{/if}
    </div>
  {:else if phase === "done"}
    <!-- What happened, then the undo and its date. A Delete that ended in
         Forget archive only… left the file alone, and says so. The undo is
         in View › Forgotten, where forgotten records alone show (APP.md
         §13, ruled 2026-10-19). -->
    {#if did === "forget"}
      <p>{dialogCopy.deleteArchive.forgotDone(name, date(forgottenAt + RETENTION_SECONDS))}</p>
    {:else}
      <p>{dialogCopy.deleteArchive.deletedDone(date(forgottenAt + RETENTION_SECONDS))}</p>
    {/if}
  {:else if phase === "mismatch"}
    <p>{dialogCopy.notThisArchive.body}</p>
    <p class="t-sub">{dialogCopy.notThisArchive.next}</p>
  {:else if phase === "mismatch-confirm"}
    <p>{dialogCopy.forgetOnly.body}</p>
    <div class="bar attention"><svg class="i i-14"><use href="#i-warn" /></svg><span>{backupLine}</span></div>
  {:else if phase === "unreachable"}
    <p>{dialogCopy.unreachable}</p>
    <dl class="kv stk"><dt>{archivesCopy.facts.file}</dt><dd>{here || "—"}</dd></dl>
  {:else if phase === "unreadable"}
    <p>{dialogCopy.unreadable}</p>
    <!-- The core's own reason, said once and quietly: held, or not an
         archive at all. -->
    <p class="t-quiet">{codeText(problem)}</p>
  {:else if phase === "failed"}
    <p>{dialogCopy.deleteFailed}</p>
  {:else}
    <div class="bar danger"><svg class="i i-14"><use href="#i-error" /></svg><span>{codeText(problem)}</span></div>
  {/if}

  {#snippet actions()}
    <!-- The foot reads in its own order (APP.md §6, Controls; ruled
         2026-10-17): the act first, then the other ways on, and the way
         out last — Cancel, Close or Go back. -->
    {#if phase === "consent" || phase === "busy"}
      <!-- The button that carries the destructive act out is filled red
           (APP.md §6, ruled 2026-09-10), here and in every confirmation.
           It stays enabled with the name field empty (APP.md §6, Forms:
           a primary is never disabled for an empty field; a press marks
           the field instead). The mock's greyed primary is wrong by the
           spec, and is not followed. -->
      <!-- The wait is the one pending primitive (direction 4): greyed from
           the press, the ring after 300 ms, and *Deleting…* for a delete,
           which is the label's news; a forget keeps its words. -->
      <button type="submit" class="btn danger-fill" disabled={phase === "busy"} use:pending={{ on: phase === "busy", armed: (b) => (busyArmed = b) }}>{#if mode === "delete"}<PendingLabel armed={busyArmed} rest={dialogCopy.deleteArchive.primary} busy={pendingCopy.deleting} />{:else}{dialogCopy.forgetArchive.primary}{/if}</button>
      <button type="button" class="btn" disabled={phase === "busy"} onclick={onclose}>{dialogCopy.cancel}</button>
    {:else if phase === "done"}
      <button type="button" class="btn accent" onclick={onclose}>{dialogCopy.close}</button>
    {:else if phase === "mismatch"}
      <button type="button" class="btn accent" onclick={() => (phase = "mismatch-confirm")}>{dialogCopy.notThisArchive.forgetOnly}</button>
      <button type="button" class="btn" onclick={locate}>{commandCopy.locate}</button>
      <button type="button" class="btn" onclick={onclose}>{dialogCopy.close}</button>
    {:else if phase === "mismatch-confirm"}
      <button type="button" class="btn danger-fill" onclick={() => void forget()}>{dialogCopy.forgetOnly.primary}</button>
      <button type="button" class="btn" onclick={() => (phase = "mismatch")}>Go back</button>
    {:else if phase === "unreachable"}
      <button type="button" class="btn accent" onclick={() => void remove()}>Retry</button>
      <button type="button" class="btn" onclick={locate}>{commandCopy.locate}</button>
      <button type="button" class="btn" onclick={onclose}>{dialogCopy.close}</button>
    {:else if phase === "unreadable"}
      <button type="button" class="btn accent" onclick={() => void remove()}>Retry</button>
      <button type="button" class="btn" onclick={locate}>{commandCopy.locate}</button>
      <button type="button" class="btn" onclick={() => (phase = "mismatch-confirm")}>{dialogCopy.notThisArchive.forgetOnly}</button>
      <button type="button" class="btn" onclick={onclose}>{dialogCopy.close}</button>
    {:else if phase === "failed"}
      <button type="button" class="btn accent" onclick={() => void remove()}>Retry</button>
      <button type="button" class="btn" onclick={onclose}>{dialogCopy.close}</button>
    {:else}
      <button type="button" class="btn accent" onclick={onclose}>{dialogCopy.close}</button>
    {/if}
  {/snippet}
</Dialog>

<style>
  /* Delete names the path in its body: it wraps anywhere, never cut and
     never pushing the dialog wider (APP.md §13). */
  .path-body { overflow-wrap: anywhere; }
</style>

<script lang="ts">
  // The viewer (direction 7, Explorer's habits; APP.md §6, the Archive
  // page; lib/viewer.ts): the file large, inside the window, over the page's
  // area of the layer — the foot and the rail stay, and a dialog it opens
  // (Extract…) stands over it. It is the pane's own pipeline, larger
  // (APP.md §4): Archive.PreviewURL's no-store loopback URL for a picture, a
  // video or a sound, Archive.PreviewText under the pane's 64 KiB cap for
  // text. A kind the WebView cannot show — a PDF, a HEIC — opens on its type
  // icon and the bar's facts with *Extract…* in the bar; no renderer of its
  // own is ever put in the window (2026-09-13). Nothing decrypted is written
  // to disk, and nothing is kept once it closes: the URL is dropped and
  // every media element's source cleared as it goes.
  import { untrack } from "svelte";
  import { fade } from "svelte/transition";
  import { Archive, errorOf } from "../lib/api";
  import type { FileRow } from "../lib/api";
  import { bytes, dateTime, fileIcon, previewKind } from "../lib/format";
  import { motion } from "../lib/motion";
  import { pending } from "../lib/pending";
  import { dialogIsUp } from "../lib/dialogs";
  import { noDrag } from "../lib/dragout";
  import { store } from "../lib/state.svelte";
  import { tip } from "../lib/tip";
  import { codeText, commandCopy, keyNames, keyTitles, paneCopy, typeLabel, viewerCopy } from "../lib/strings";
  import { PREVIEW_TEXT_CAP, canStep, counter, sequence, stageOf, stepTo, viewerKey, wantsMore } from "../lib/viewer";
  import type { ViewerStep } from "../lib/viewer";

  interface Props {
    archiveId: string;
    // The list's rows in its order: what ← and → step through.
    rows: FileRow[];
    // The file shown — the selected row, which follows the viewer.
    current: string;
    // The file the viewer was opened on, kept among the steps whatever its
    // kind (lib/viewer.ts sequence).
    pinned: string;
    // The folder has rows past those loaded (APP.md §3, paging).
    more: boolean;
    // Why *Extract…* is greyed — the archive draining after its Close.
    extractWhy?: string;
    onstep: (id: string) => void;
    onclose: () => void;
    onextract: (row: FileRow) => void;
    onmore: () => void;
  }
  let { archiveId, rows, current, pinned, more, extractWhy, onstep, onclose, onextract, onmore }: Props = $props();

  const row = $derived(rows.find((r) => r.id === current) ?? null);
  const seq = $derived(sequence(rows, pinned));
  // "4 of 13" counts the folder's showable files alone, the pinned one
  // left out, so the total holds wherever the viewer was opened; a PDF
  // opened on its facts shows no counter (lib/viewer.ts counter).
  const pos = $derived(counter(rows, current));
  const shownId = $derived(row?.id ?? "");
  const shownName = $derived(row?.name ?? "");
  const facts = $derived(row ? [bytes(row.size), typeLabel(row.name, false), dateTime(row.modifiedAt)].join(" · ") : "");

  let url = $state("");
  let text = $state<{ text: string; truncated: boolean } | null>(null);
  let refused = $state("");
  const stage = $derived(stageOf(shownName, { url, text: text !== null, refused }));

  // One read per file shown; a reply for a file stepped past is dropped.
  let token = 0;
  $effect(() => {
    const id = shownId;
    const name = shownName;
    const t = ++token;
    url = "";
    text = null;
    refused = "";
    if (!id) return;
    const refuse = (e: unknown) => {
      if (t === token) refused = codeText(errorOf(e).code);
    };
    const kind = previewKind(name);
    if (kind === "none") return;
    if (kind === "text") {
      Archive.PreviewText(archiveId, id, PREVIEW_TEXT_CAP).then((x) => { if (t === token) text = x; }).catch(refuse);
    } else {
      Archive.PreviewURL(archiveId, id).then((u) => { if (t === token) url = u; }).catch(refuse);
    }
  });

  // The file shown left the folder — deleted, moved, the folder changed —
  // and the viewer has nothing left to show.
  $effect(() => {
    if (!row) untrack(() => onclose());
  });

  // A long folder pages in as the viewer nears the last row loaded.
  $effect(() => {
    if (wantsMore(seq, current, more)) untrack(() => onmore());
  });

  function step(by: ViewerStep) {
    const to = stepTo(seq, current, by);
    if (to) onstep(to);
  }

  function extract() {
    if (row && !extractWhy) onextract(row);
  }

  function key(e: KeyboardEvent) {
    if (e.defaultPrevented || e.isComposing) return;
    const t = e.target instanceof HTMLElement ? e.target : null;
    const a = viewerKey(
      { key: e.key, ctrl: e.ctrlKey || e.metaKey, shift: e.shiftKey, alt: e.altKey },
      {
        onControl: !!t?.closest("button, a[href]"),
        onMedia: !!t?.closest("video, audio"),
        onText: !!t?.closest(".vtext"),
        dialogUp: dialogIsUp(),
        // The rail and the foot stay live beside the viewer: a key there is
        // theirs. The body is where the focus falls when nothing holds it.
        outside: !!t && t !== document.body && !root?.contains(t),
      },
    );
    if (a.kind === "none") return;
    e.preventDefault();
    if (a.kind === "close") onclose();
    else if (a.kind === "extract") extract();
    else step(a.by);
  }

  // The viewer stands over the page's notice slot (its z 10 over the
  // slot's 5), so a bar raised while it is up is not seen: the page's bars
  // wait until it closes, as they wait under a dialog's dim (lib/infobar.ts
  // running; APP.md §6, Notices).
  $effect(() => {
    untrack(() => store.pauseNotices({ viewerUp: true }));
    return () => untrack(() => store.pauseNotices({ viewerUp: false }));
  });

  // The keys start here: the page behind is inert while the viewer is up.
  let root = $state<HTMLDivElement | undefined>();
  $effect(() => {
    root?.focus({ preventScroll: true });
  });

  // Nothing kept: the URL goes, and every source in the stage is cleared
  // so a video or a sound stops streaming from the loopback the moment
  // the viewer closes.
  $effect(() => () => {
    token++;
    for (const m of root?.querySelectorAll<HTMLImageElement | HTMLMediaElement>("img, video, audio") ?? []) {
      m.removeAttribute("src");
      if (m instanceof HTMLMediaElement) m.load();
    }
  });
</script>

<svelte:window onkeydown={key} />

<div class="viewer" role="region" aria-label={viewerCopy.label(shownName)} tabindex="-1" bind:this={root} ondragstart={noDrag} in:fade={motion()}>
  <div class="vbar">
    <strong class="vname" use:tip={{ text: shownName, cut: true }}>{shownName}</strong>
    {#if pos.at > 0}<span class="vcap num">{viewerCopy.counter(pos.at, pos.of)}</span>{/if}
    <span class="vcap vfacts num">{facts}</span>
    <span class="grow"></span>
    <button type="button" class="vbtn" disabled={!!extractWhy} title={extractWhy ?? keyTitles.extract} onclick={extract}><svg class="i i-14" aria-hidden="true"><use href="#i-extract" /></svg>{commandCopy.extract}<span class="vkey" aria-hidden="true">{keyNames.extract}</span></button>
    <button type="button" class="vbtn icon" title={viewerCopy.close} aria-label={viewerCopy.close} onclick={onclose}><svg class="i" aria-hidden="true"><use href="#i-x" /></svg></button>
  </div>
  <div class="vstage">
    <button type="button" class="vnav prev" title={viewerCopy.previous} aria-label={viewerCopy.previous} disabled={!canStep(seq, current, -1)} onclick={() => step(-1)}><svg class="i" aria-hidden="true"><use href="#i-chevron" /></svg></button>
    <div class="vshow">
      {#if row}
        {#if stage === "image"}
          <img src={url} alt={shownName} draggable="false" />
        {:else if stage === "video"}
          <!-- The pane's own player: its controls, no download, no
               picture-in-picture window outside Enfold's. -->
          <!-- svelte-ignore a11y_media_has_caption -->
          <video src={url} controls controlslist="nodownload noplaybackrate" disablepictureinpicture draggable="false"></video>
        {:else if stage === "audio"}
          <div class="vnone">
            <svg class="i vbig" aria-hidden="true"><use href="#i-audio" /></svg>
            <audio src={url} controls controlslist="nodownload"></audio>
          </div>
        {:else if stage === "text" && text}
          <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
          <div class="vtext" tabindex="0">
            <pre>{text.text}{text.truncated ? "\n…" : ""}</pre>
            {#if text.truncated}<p class="vcap">{viewerCopy.truncated}</p>{/if}
          </div>
        {:else if stage === "wait"}
          <!-- Keyed on the file, so each one waits its own 300 ms before
               the ring shows (lib/pending.ts). -->
          {#key shownId}<span class="vwait" aria-hidden="true" use:pending={true}></span>{/key}
        {:else}
          <!-- A kind the WebView cannot show, or a preview the core
               refused: the type's icon and what is so. Extract… is in the
               bar, an action of its own, never the cure. -->
          <div class="vnone">
            <svg class="i vbig" aria-hidden="true"><use href="#{fileIcon(shownName)}" /></svg>
            <span class="vline">{stage === "refused" ? refused : paneCopy.noPreview(shownName)}</span>
          </div>
        {/if}
      {/if}
    </div>
    <button type="button" class="vnav" title={viewerCopy.next} aria-label={viewerCopy.next} disabled={!canStep(seq, current, 1)} onclick={() => step(1)}><svg class="i" aria-hidden="true"><use href="#i-chevron" /></svg></button>
  </div>
</div>

<style>
  /* Over the page's area of the layer (app.css .page is its containing
     block), under every dialog (z 20) and menu, dark in both themes (the
     --viewer-* tokens). The focus ring is drawn in the viewer's own inks
     so it shows on the dark ground in the light theme too. */
  .viewer {
    position: absolute; inset: 0; z-index: 10;
    display: flex; flex-direction: column; min-width: 0; min-height: 0;
    background: var(--viewer-bg); color: var(--viewer-ink);
    color-scheme: dark;
    --focus-outer: var(--viewer-ink); --focus-inner: var(--viewer-bg);
    outline: none;
  }
  /* The 40 px bar: the name, "4 of 13", the facts in one caption line,
     then Extract… and the close at the right. */
  .vbar { display: flex; align-items: center; gap: var(--s2); min-height: 40px; padding: var(--s1) var(--s2) var(--s1) var(--s3); flex: none; min-width: 0; }
  .vname { font: var(--t-strong); color: var(--viewer-ink); min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; flex: 0 1 auto; }
  .vcap { font: var(--t-cap); color: var(--viewer-ink-2); white-space: nowrap; flex: none; }
  .vfacts { flex: 0 1 auto; min-width: 0; overflow: hidden; text-overflow: ellipsis; }
  .grow { flex: 1 1 0; }
  .vbtn {
    display: inline-flex; align-items: center; gap: var(--s2); height: 28px; padding: 0 var(--s3); flex: none;
    border-radius: var(--r-ctl); border: 1px solid var(--viewer-stroke); background: var(--viewer-ctl); color: var(--viewer-ink);
    font: var(--t-body); white-space: nowrap; cursor: default;
    transition: background-color var(--dur-hover) var(--ease-enter);
  }
  .vbtn:hover:not(:disabled) { background: var(--viewer-ctl-hover); }
  .vbtn:disabled { color: var(--viewer-ink-2); opacity: 0.6; }
  /* The command's key in the button, as the demo's d7 bar shows it: the
     viewer has no menu to carry it, and the bar is where the eye is. In
     the caption ink, as a menu draws an accelerator. */
  .vkey { font: var(--t-cap); color: var(--viewer-ink-2); }
  .vbtn.icon { width: 28px; padding: 0; justify-content: center; border-color: transparent; background: transparent; }
  .vbtn.icon:hover { background: var(--viewer-ctl); }
  /* The stage: the previous and next buttons at the sides, the file in the
     middle filling what is left. */
  .vstage { flex: 1 1 auto; min-height: 0; display: grid; grid-template-columns: 32px minmax(0, 1fr) 32px; align-items: center; gap: var(--s2); padding: var(--s1) var(--s2) var(--s3); }
  .vnav {
    width: 32px; height: 32px; border-radius: 50%; border: none; display: grid; place-items: center; padding: 0;
    background: var(--viewer-ctl); color: var(--viewer-ink); cursor: default;
    transition: background-color var(--dur-hover) var(--ease-enter), opacity var(--dur-hover) var(--ease-enter);
  }
  .vnav:hover:not(:disabled) { background: var(--viewer-ctl-hover); }
  .vnav:disabled { opacity: 0.3; }
  .vnav.prev .i { transform: rotate(180deg); }
  .vshow { height: 100%; min-height: 0; min-width: 0; display: flex; align-items: center; justify-content: center; }
  /* A picture is shown at its own size and only made smaller to fit, as
     Photos does: a small icon is not blown up into a blur. */
  .vshow img, .vshow video { max-width: 100%; max-height: 100%; object-fit: contain; display: block; }
  .vshow audio { width: min(420px, 100%); }
  /* Text: the pane's monospace on a panel of the viewer's own, as wide as
     a line wants and no wider, scrolling inside itself. */
  .vtext {
    align-self: stretch; width: min(100%, 100ch); margin: 0 auto; overflow: auto; min-height: 0;
    background: var(--viewer-ctl); border: 1px solid var(--viewer-stroke); border-radius: var(--r-ctl);
    padding: var(--s3) var(--s4);
  }
  .vtext pre { margin: 0; font: 400 13px/20px var(--font-mono); color: var(--viewer-ink); white-space: pre-wrap; overflow-wrap: anywhere; }
  .vtext .vcap { margin: var(--s2) 0 0; white-space: normal; }
  .vwait { display: inline-flex; }
  /* What cannot be shown: the type's icon at 96 px in the second ink, and
     the line under it. */
  .vnone { display: flex; flex-direction: column; align-items: center; gap: var(--s3); text-align: center; padding: var(--s4); }
  .vbig { width: 96px; height: 96px; color: var(--viewer-ink-2); stroke-width: 0.5; }
  .vline { font: var(--t-body); color: var(--viewer-ink); overflow-wrap: anywhere; }
</style>

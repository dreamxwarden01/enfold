// The busy archive (APP.md §6, *The busy archive*; ruled 2026-11-03), read
// as source, as names.components.test.ts reads the dialogs: the pure part
// (lib/ops.ts busyOp, busyReason; lib/filemenu.ts Stop; the store's
// starting ids and stopOps) has its own tests, and how it all lays out and
// moves is the mock's to show (the build's tour drives a real drag through
// CDP and measures the veil, the plate and the callout). What is pinned
// here is the wiring a refactor could undo without a unit test noticing:
// that the list stops being a drop target while busy, that one held state
// drives the ring and the veil, that the reason reaches every place the
// draining reason did, and where the callout and the delete's sentence are
// drawn.
import { describe, expect, it } from "vitest";

const sources = import.meta.glob(["./ArchivePage.svelte", "./LayerFoot.svelte", "./DeleteArchiveDialog.svelte", "./ArchivesPage.svelte"], {
  query: "?raw",
  import: "default",
  eager: true,
}) as Record<string, string>;

function src(name: string): string {
  const hit = Object.entries(sources).find(([f]) => f.endsWith(`/${name}`));
  if (!hit) throw new Error(`not read: ${name}`);
  return hit[1].replace(/\r\n/g, "\n");
}

// The markup alone: the part after the script.
const markup = (s: string) => s.slice(s.indexOf("</script>"));

describe("the busy archive's page", () => {
  const page = src("ArchivePage.svelte");
  const html = markup(page);

  it("takes the list off the drop targets while busy, and gives it back when idle", () => {
    expect(html).toContain('data-file-drop-target={busy ? undefined : "true"}');
    expect(html).not.toContain('data-file-drop-target="true"');
  });

  it("drives the ring and the veil by one held state, which a crossing onto a row does not end", () => {
    expect(page).not.toMatch(/let dropping|dropping = |class:dropping/);
    expect(html).toContain("class:held={held && !busy}");
    expect(html).toContain("ondragenter={holdOn}");
    expect(html).toContain("ondragleave={holdOff}");
    expect(page).toContain("listEl?.contains(to)");
  });

  it("draws the veil in the list's own cell, a status, its words on the plate", () => {
    const veil = html.slice(html.indexOf("{#if held && busy && !draining}"), html.indexOf("{#if paneOn}"));
    expect(veil).toContain('class="busy-veil" role="status"');
    expect(veil).toContain('class="busy-plate"');
    expect(veil).toContain("{busyName}");
    expect(veil).toContain("{busyCopy.dropHere}");
    // Between the list and the pane, inside the split: the list's cell.
    expect(html.indexOf("{#if held && busy && !draining}")).toBeGreaterThan(html.indexOf('<div class="tablewrap"'));
  });

  it("greys by one reason wherever the draining reason reached, Delete archive… apart", () => {
    expect(html).not.toMatch(/disabled=\{draining/);
    expect(html).not.toMatch(/title=\{closingWhy/);
    expect(html).toContain("extractWhy={blockedWhy}");
    expect(html).toContain('<MenuButton id="add" label="Add" icon="i-plus" items={addItems} disabled={blocked} title={blockedWhy} />');
    // The script's one closingWhy beyond the blocked reason: Delete
    // archive…, which stays live while busy (the user's ruling).
    expect(page.match(/closingWhy \?\?/g)?.length).toBe(2);
    expect(page).toContain("const blockedWhy = $derived(closingWhy ?? busyWhy);");
  });

  it("gives the empty folder's caption to the add, and hides it for any other operation", () => {
    const empty = html.slice(html.indexOf("{#snippet emptyFolder()}"));
    expect(empty).toContain("{busyCopy.appearing}");
    expect(empty).toContain('busy.kind === "add"');
    expect(empty.match(/disabled=\{blocked\} title=\{blockedWhy\}/g)?.length).toBe(2);
  });

  it("refuses a drop that still arrives, and decides a paste at the clipboard's answer", () => {
    expect(page).toContain("raiseTip(busyCopy.dropAgain)");
    expect(page).toContain("raiseTip(busyCopy.pasteAgain)");
    const paste = page.slice(page.indexOf("async function paste("));
    expect(paste.indexOf("noticeCopy.nothingToPaste")).toBeLessThan(paste.indexOf("busyCopy.pasteAgain"));
  });

  // The page review of 2026-11-03: More hangs from a 32 px button, so a
  // reason that keeps to one line must floor the menu's width (app.css,
  // min-width: max-content, which the test runner cannot read). The page's
  // part is the mark: More's Delete archive… takes the one-line rule only
  // when its reason is the operation's, behind a compaction or a re-key,
  // and keeps the vault's own sentences wrapping.
  it("marks More's Delete archive… when its reason is the operation's", () => {
    expect(page).toContain("const deleteWaits = $derived(deleteWhy !== undefined && deleteWhy === busyWhy);");
    expect(html).toContain('<div class="cmdbar" class:busy={!!busy && !draining} class:delete-waits={deleteWaits}');
    const more = page.slice(page.indexOf("const moreItems"), page.indexOf("const paneOn"));
    expect(more).toContain("title: deleteWhy,");
  });

  it("stops what runs before it lets the archive go for Delete archive…", () => {
    const letGo = page.slice(page.indexOf("async function letGo("));
    expect(letGo.indexOf("store.stopOps(archiveId)")).toBeLessThan(letGo.indexOf("Archives.Leave(archiveId)"));
    expect(html).toContain("running={deleteStops}");
  });
});

describe("the callout and the delete's sentence", () => {
  it("is drawn by the foot, a status anchored to its running line, which it washes", () => {
    const foot = src("LayerFoot.svelte");
    expect(foot).toContain('class="foot-tip" role="status"');
    expect(foot).toContain("class:washed={!!tip && tip.opId === o.id}");
  });

  it("says what Delete archive… stops above the name to type", () => {
    const d = markup(src("DeleteArchiveDialog.svelte"));
    expect(d.indexOf('<p class="stops">{stops}</p>')).toBeGreaterThan(-1);
    expect(d.indexOf('<p class="stops">{stops}</p>')).toBeLessThan(d.indexOf('id="del-name"'));
  });

  // A delete that stopped what ran first and is still refused as busy met a
  // race on the archive's turn (the page review of 2026-11-03): the dialog
  // says it as any error, by the outcome lib/archcmds.ts deleteOutcome
  // picks, never as a file that could not be read.
  it("says a busy refusal after its stop as any error", () => {
    const d = src("DeleteArchiveDialog.svelte");
    expect(d).toContain('phase = deleteOutcome(problem, stopsAtAct !== "");');
    expect(d).not.toMatch(/case "archive\.busy":/);
    // The act freezes what it stopped before it lets the archive go.
    const go = d.slice(d.indexOf("async function go()"));
    expect(go.indexOf("stopsAtAct = running;")).toBeLessThan(go.indexOf("await released()"));
  });

  it("stops what runs from the Archives page too", () => {
    const a = src("ArchivesPage.svelte");
    expect(a).toContain("running={delStops}");
    expect(a).toContain('if (mode === "delete") await stopFirst(id);');
  });
});

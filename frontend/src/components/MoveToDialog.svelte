<script lang="ts">
  // Move to… (direction 7, Explorer's habits; APP.md §6, the Archive page;
  // lib/movetree.ts): the selection into a folder chosen in a tree of the
  // archive's folders. The root is the archive, open; each folder is read
  // from Archive.Page when it is first opened, folders only; the folders
  // down to the one shown are opened as the dialog opens, so where the
  // items are now is in view, greyed "Already here", and selected. A moved
  // folder and everything beneath it are greyed "A folder cannot move into
  // itself"; a greyed folder can still be selected, so *Create folder…*
  // can make a new one inside it. *Move* is the accent, greyed with the
  // reason while the folder selected cannot take the move; a refusal — a
  // name already there, as a drag's Move is refused — is said under the
  // tree in the code's own line, and the dialog stays.
  import { tick, untrack } from "svelte";
  import { Archive, errorOf } from "../lib/api";
  import { codeText, dialogCopy, moveToCopy, commandCopy } from "../lib/strings";
  import { newNameProblem } from "../lib/validate";
  import { jump, typeahead } from "../lib/listkeys";
  import { TREE_LIMIT, failed, folderLines, lines, needsRead, newTree, offReason, pathOf, read, reading, setOpen, treeKey } from "../lib/movetree";
  import type { Off, Tree } from "../lib/movetree";
  import { pending } from "../lib/pending";
  import Dialog from "./Dialog.svelte";
  import TextField from "./TextField.svelte";

  interface Props {
    archiveId: string;
    archiveName: string;
    rootId: string;
    // The folder shown, where the items are now, and the ids from the root
    // down to it (the page's crumbs), so the tree opens on it.
    current: string;
    trail: string[];
    // What moves, and its name when it is one item.
    ids: string[];
    name: string;
    onmoved: () => void;
    // A folder was created: the page re-reads its listing, since the
    // folder may be the one it shows.
    oncreated: () => void;
    oncancel: () => void;
  }
  let { archiveId, archiveName, rootId, current, trail, ids, name, onmoved, oncreated, oncancel }: Props = $props();

  // svelte-ignore state_referenced_locally
  let tree = $state<Tree>(newTree(rootId, archiveName));
  // svelte-ignore state_referenced_locally
  let selected = $state(current);
  let refusal = $state("");
  let busy = $state(false);

  const drawn = $derived(lines(tree));
  const folders = $derived(folderLines(tree));
  const off = $derived<Off | null>(offReason(tree, selected, current, ids));
  const offText = (o: Off | null) => (o === "here" ? moveToCopy.alreadyHere : o === "self" ? moveToCopy.intoItself : "");
  const where = $derived(pathOf(tree, selected).join(moveToCopy.pathSep));

  async function load(id: string) {
    tree = reading(tree, id);
    try {
      const p = await Archive.Page(archiveId, id, "name", 0, TREE_LIMIT);
      tree = read(tree, id, p.rows ?? [], p.total);
    } catch (e) {
      tree = failed(tree, id, errorOf(e).code);
    }
  }

  async function toggle(id: string) {
    const f = tree.folders[id];
    if (!f) return;
    if (f.open) {
      tree = setOpen(tree, id, false);
      // The selection inside a folder closed over it moves to that folder,
      // as a tree's does.
      if (insideOf(selected, id)) selected = id;
      return;
    }
    tree = setOpen(tree, id, true);
    if (needsRead(tree, id)) await load(id);
  }

  function insideOf(child: string, folder: string): boolean {
    let at = tree.folders[child]?.parent ?? null;
    while (at !== null) {
      if (at === folder) return true;
      at = tree.folders[at]?.parent ?? null;
    }
    return false;
  }

  // Opening: the root, then each folder down to the one shown, so it is
  // in view and selected; the focus goes to it once it is drawn.
  // Once, as the dialog mounts: untracked, since the reads write the tree
  // they read.
  let treeEl = $state<HTMLDivElement | undefined>();
  $effect(() => {
    untrack(() => void opening());
  });

  async function opening() {
    await load(rootId);
    for (const id of trail.slice(1, -1)) {
      if (!tree.folders[id]) break;
      tree = setOpen(tree, id, true);
      await load(id);
    }
    if (!tree.folders[selected]) selected = rootId;
    await focusSelected();
  }

  async function focusSelected() {
    await tick();
    const el = treeEl?.querySelector<HTMLElement>(`[data-id="${CSS.escape(selected)}"]`);
    el?.focus({ preventScroll: true });
    el?.scrollIntoView({ block: "nearest" });
  }

  function pick(id: string) {
    selected = id;
    refusal = "";
    void focusSelected();
  }

  // The keys (lib/movetree.ts treeKey): the tree pattern, Explorer's own,
  // and the lists' type-ahead (lib/listkeys.ts).
  let typed = "";
  let typedAt = 0;
  function treeKeydown(e: KeyboardEvent) {
    if (e.defaultPrevented || e.isComposing) return;
    const at = folders.findIndex((f) => f.id === selected);
    const a = treeKey({ key: e.key, ctrl: e.ctrlKey || e.metaKey, shift: e.shiftKey, alt: e.altKey }, tree, folders, at);
    if (a.kind === "none") return;
    e.preventDefault();
    switch (a.kind) {
      case "select":
        pick(folders[a.index].id);
        break;
      case "open":
      case "close":
        void toggle(a.id);
        break;
      case "choose":
        void move();
        break;
      case "typeahead": {
        const now = Date.now();
        typed = typeahead(typed, a.char, now, typedAt);
        typedAt = now;
        const to = jump(folders.map((f) => f.name), typed, at);
        if (to >= 0) pick(folders[to].id);
        break;
      }
    }
  }

  async function move() {
    if (off !== null || busy) return;
    busy = true;
    refusal = "";
    try {
      await Archive.Move(archiveId, ids, selected);
      onmoved();
    } catch (e) {
      refusal = codeText(errorOf(e).code);
    } finally {
      busy = false;
    }
  }

  // *Create folder…* in place (APP.md §6, Forms): a field under the tree
  // for a folder in the one selected, judged against every name already in
  // it; Enter creates, Esc puts the button back. The new folder is
  // selected, so *Move* goes into it.
  let naming = $state(false);
  let newName = $state("");
  let newValid = $state(true);
  let newAttempt = $state(0);
  let namingIn = $state("");
  const taken = $derived(tree.folders[namingIn]?.names ?? []);
  const judge = $derived((v: string) => newNameProblem(v, taken));
  const namingInName = $derived(tree.folders[namingIn]?.name ?? "");

  async function askCreate() {
    namingIn = selected;
    refusal = "";
    if (needsRead(tree, selected)) await load(selected);
    newName = "";
    newValid = true;
    newAttempt = 0;
    naming = true;
    await tick();
    document.getElementById("mt-new")?.focus();
  }

  async function create() {
    newAttempt++;
    if (!newValid || busy) return;
    const parent = namingIn;
    busy = true;
    try {
      const made = await Archive.CreateFolder(archiveId, parent, newName.trim());
      naming = false;
      tree = setOpen(tree, parent, true);
      await load(parent);
      oncreated();
      pick(made);
    } catch (e) {
      refusal = codeText(errorOf(e).code);
    } finally {
      busy = false;
    }
  }

  function fieldKeydown(e: KeyboardEvent) {
    if (e.key === "Enter") {
      e.preventDefault();
      void create();
    } else if (e.key === "Escape") {
      // The field's own Esc: the button comes back and the dialog stays.
      e.preventDefault();
      e.stopPropagation();
      naming = false;
      void focusSelected();
    }
  }
</script>

<Dialog title={moveToCopy.title(ids.length, name)} medium onclose={oncancel}>
  <!-- The tree (the ARIA tree pattern): one row in the tab order, the
       selected one; a greyed row says why at its right. -->
  <!-- svelte-ignore a11y_interactive_supports_focus -->
  <div class="mtree" role="tree" aria-label={moveToCopy.tree} bind:this={treeEl} onkeydown={treeKeydown}>
    {#each drawn as l (l.kind === "folder" ? l.id : `${l.parent}:${l.note}`)}
      {#if l.kind === "folder"}
        {@const o = offReason(tree, l.id, current, ids)}
        <!-- svelte-ignore a11y_click_events_have_key_events -->
        <div
          class="tn"
          class:sel={l.id === selected}
          class:off={o !== null}
          role="treeitem"
          aria-level={l.depth + 1}
          aria-expanded={l.expandable ? l.open : undefined}
          aria-selected={l.id === selected}
          aria-disabled={o !== null || undefined}
          tabindex={l.id === selected ? 0 : -1}
          data-id={l.id}
          style:--depth={l.depth}
          onclick={() => pick(l.id)}
          ondblclick={() => { if (l.expandable) void toggle(l.id); }}
        >
          {#if l.expandable}
            <button type="button" class="chev" class:open={l.open} tabindex="-1" aria-label={moveToCopy.open(l.name)} onclick={(e) => { e.stopPropagation(); pick(l.id); void toggle(l.id); }}><svg class="i i-14" aria-hidden="true"><use href="#i-chevron" /></svg></button>
          {:else}
            <span class="chev" aria-hidden="true"></span>
          {/if}
          <svg class="i i-14 kind" aria-hidden="true"><use href={l.depth === 0 ? "#i-box" : "#i-folder"} /></svg>
          <span class="nm">{l.name}</span>
          <!-- Every greyed row says why at its right, the folders beneath a
               moved one included (the brief: the moved folders and their
               descendants, each with its reason; the final shoot of phase
               4b found a descendant greyed with nothing said), as a
               greyed menu item carries its reason. -->
          {#if o}<span class="why">{offText(o)}</span>{/if}
        </div>
      {:else}
        <div class="tn note" class:bad={l.note === "failed"} style:--depth={l.depth} role="none">
          <span class="chev" aria-hidden="true"></span>
          {#if l.note === "loading"}
            <span class="wait" aria-label={moveToCopy.reading} use:pending={true}></span>
          {:else if l.note === "failed"}
            <span class="nm">{codeText(l.code)}</span>
          {:else}
            <span class="nm">{moveToCopy.cut(TREE_LIMIT)}</span>
          {/if}
        </div>
      {/if}
    {/each}
  </div>

  {#if naming}
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div class="mt-new" onkeydown={fieldKeydown}>
      <div class="grow"><TextField id="mt-new" label={moveToCopy.newFolderIn(namingInName)} bind:value={newName} bind:valid={newValid} attempt={newAttempt} judge={judge} /></div>
      <button type="button" class="btn" disabled={busy} onclick={() => void create()}>{moveToCopy.create}</button>
    </div>
  {:else}
    <button type="button" class="btn subtle sm mt-create" disabled={busy} onclick={() => void askCreate()}><svg class="i i-14" aria-hidden="true"><use href="#i-folder-add" /></svg>{commandCopy.createFolder}</button>
  {/if}

  {#if refusal}
    <p class="mt-said bad" role="alert">{refusal}</p>
  {:else}
    <p class="mt-said">{off !== null ? offText(off) : moveToCopy.to(where)}</p>
  {/if}

  {#snippet actions()}
    <button type="button" class="btn accent" disabled={off !== null} title={off !== null ? offText(off) : undefined} use:pending={busy} onclick={() => void move()}>{moveToCopy.move}</button>
    <button type="button" class="btn" onclick={oncancel}>{dialogCopy.cancel}</button>
  {/snippet}
</Dialog>

<style>
  /* The tree: a fixed height that scrolls, so the dialog does not grow as
     folders open (APP.md §6, Dialogs stay put); 28 px rows, 18 px a
     level, the selected row in the accent's wash. */
  .mtree {
    height: 264px; overflow: auto; padding: var(--s1);
    border: 1px solid var(--stroke); border-radius: var(--r-ctl); background: var(--surface);
    display: flex; flex-direction: column;
  }
  .tn {
    display: flex; align-items: center; gap: var(--s1); height: 28px; flex: none; min-width: 0;
    padding: 0 var(--s2) 0 calc(var(--s1) + var(--depth) * 18px); border-radius: var(--r-ctl); cursor: default;
  }
  .tn:hover:not(.note) { background: var(--fill-2); }
  .tn.sel { background: var(--accent-wash); }
  .tn .kind { color: var(--ink-3); flex: none; }
  .tn .nm { flex: 1 1 auto; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .tn.off .nm { color: var(--ink-dis); }
  .tn .why { flex: none; font: var(--t-cap); color: var(--ink-2); white-space: nowrap; }
  .tn.note .nm { font: var(--t-cap); color: var(--ink-2); }
  .tn.note.bad .nm { color: var(--danger); }
  .tn .wait { display: inline-flex; width: 14px; height: 14px; }
  .chev {
    flex: none; width: 18px; height: 18px; display: grid; place-items: center; padding: 0;
    border: none; background: transparent; color: var(--ink-2); border-radius: var(--r-sm);
  }
  button.chev:hover { background: var(--fill-1); }
  .chev .i { transition: transform var(--dur-fast) var(--ease-enter); }
  .chev.open .i { transform: rotate(90deg); }
  .mt-create { align-self: flex-start; }
  .mt-new { display: flex; align-items: flex-end; gap: var(--s2); }
  .mt-new .grow { flex: 1 1 auto; min-width: 0; }
  .mt-said { margin: 0; font: var(--t-cap); color: var(--ink-2); overflow-wrap: anywhere; }
  .mt-said.bad { color: var(--danger); }
</style>

import { describe, expect, it } from "vitest";
import { CAP_MS, MAX_SHOWN, due, escCloses, lifetime, newestError, noticeIcon, raise, running, settled, shown, spend } from "./infobar";
import type { Notice, NoticeKind } from "./infobar";
import { ESC_OWNERS, ESC_OWNER_ATTR } from "./dialogs";

const bar = (id: number, kind: NoticeKind = "info", text = `bar ${id}`, inDialog = false): Notice => ({ id, text, kind, inDialog, left: lifetime(text, kind) });
const free = { hovered: false, away: false, dialogUp: false, viewerUp: false };

describe("lifetime", () => {
  it("is 3 s and 50 ms a character for info and success", () => {
    expect(lifetime("Details copied", "success")).toBe(3000 + 50 * 14);
    expect(lifetime("", "info")).toBe(3000);
  });

  it("stops at 8 s however long the sentence", () => {
    expect(lifetime("x".repeat(100), "info")).toBe(CAP_MS);
    expect(lifetime("x".repeat(500), "success")).toBe(8000);
  });

  it("counts characters, not UTF-16 units", () => {
    expect(lifetime("😀😀", "info")).toBe(3100);
  });

  it("never ends an error by itself", () => {
    expect(lifetime("Windows would not let Enfold read it.", "error")).toBe(Infinity);
  });
});

describe("noticeIcon", () => {
  it("takes the tiers' icons, and the check for a success", () => {
    expect(noticeIcon("error")).toBe("i-error");
    expect(noticeIcon("info")).toBe("i-info");
    expect(noticeIcon("success")).toBe("i-check");
  });
});

describe("the queue", () => {
  it("shows three, newest first, and the fourth waits", () => {
    let list: Notice[] = [];
    for (let i = 1; i <= 4; i++) list = raise(list, bar(i));
    expect(shown(list, "page", false).map((n) => n.id)).toEqual([4, 3, 2]);
    expect(MAX_SHOWN).toBe(3);
  });

  it("brings the waiting bar back when a shown one goes", () => {
    let list: Notice[] = [];
    for (let i = 1; i <= 4; i++) list = raise(list, bar(i, "error"));
    list = list.filter((n) => n.id !== 3);
    expect(shown(list, "page", false).map((n) => n.id)).toEqual([4, 2, 1]);
  });

  it("does not say the same words twice: the repeat takes the top with a whole life", () => {
    let list = raise([], bar(1, "error", "The disk is full."));
    list = raise(list, bar(2));
    list = raise(list, bar(3, "error", "The disk is full."));
    expect(list.map((n) => n.id)).toEqual([2, 3]);
  });

  it("keeps the same words of another kind", () => {
    const list = raise(raise([], bar(1, "info", "Same")), bar(2, "error", "Same"));
    expect(list).toHaveLength(2);
  });
});

describe("the hosts", () => {
  const list = [bar(1), bar(2, "error", "in the dialog", true), bar(3)];

  it("draws every bar in the page's slot while no dialog is up", () => {
    expect(shown(list, "page", false).map((n) => n.id)).toEqual([3, 2, 1]);
    expect(shown(list, "dialog", false)).toEqual([]);
  });

  it("draws a dialog's bars in the dialog while one is up, the page's under the dim", () => {
    expect(shown(list, "dialog", true).map((n) => n.id)).toEqual([2]);
    expect(shown(list, "page", true).map((n) => n.id)).toEqual([3, 1]);
  });

  it("gives every bar to the page once the last dialog has closed", () => {
    expect(settled(list).every((n) => !n.inDialog)).toBe(true);
  });
});

describe("the clock", () => {
  it("runs the shown, mortal bars", () => {
    const list = [bar(1), bar(2, "error"), bar(3), bar(4), bar(5)];
    expect([...running(list, free)].sort()).toEqual([3, 4, 5]);
    expect(running([bar(1), bar(2, "error")], free).has(2)).toBe(false);
  });

  it("pauses everything while the bars are hovered or the window is away", () => {
    const list = [bar(1), bar(2)];
    expect(running(list, { ...free, hovered: true }).size).toBe(0);
    expect(running(list, { ...free, away: true }).size).toBe(0);
  });

  it("stops a page's bar under a dialog and runs the dialog's own", () => {
    const list = [bar(1), bar(2, "success", "Details copied", true)];
    expect([...running(list, { ...free, dialogUp: true })]).toEqual([2]);
  });

  // The viewer stands over the page's notice slot: a bar raised under it
  // would run its clock unseen.
  it("stops the page's bars while the viewer stands, and runs a dialog's over it", () => {
    const list = [bar(1), bar(2, "success", "Details copied", true)];
    expect(running(list, { ...free, viewerUp: true }).size).toBe(0);
    expect([...running(list, { ...free, viewerUp: true, dialogUp: true })]).toEqual([2]);
    expect([...running([bar(1)], free)]).toEqual([1]);
  });

  it("spends time on the running bars only and drops the ones run out", () => {
    const list = [bar(1, "info", "a"), bar(2, "info", "a longer sentence")];
    const run = new Set([1, 2]);
    const after = spend(list, 3050, run);
    expect(after.map((n) => n.id)).toEqual([2]);
    expect(after[0].left).toBe(lifetime("a longer sentence", "info") - 3050);
    expect(spend(list, 9000, new Set([2])).map((n) => n.id)).toEqual([1]);
  });

  it("says when the next running bar runs out", () => {
    const list = [bar(1, "info", "abc"), bar(2, "error")];
    expect(due(list, new Set([1, 2]))).toBe(3150);
    expect(due(list, new Set([2]))).toBeNull();
    expect(due(list, new Set())).toBeNull();
  });

  it("lets a waiting bar keep its whole life", () => {
    let list: Notice[] = [];
    for (let i = 1; i <= 4; i++) list = raise(list, bar(i, "info", `abc${i}`));
    list = spend(list, 1000, running(list, free));
    expect(list.find((n) => n.id === 1)?.left).toBe(3200);
    expect(list.find((n) => n.id === 4)?.left).toBe(2200);
  });
});

describe("newestError", () => {
  it("is the newest error the slot shows", () => {
    const list = [bar(1, "error"), bar(2), bar(3, "error"), bar(4)];
    expect(newestError(list)?.id).toBe(3);
  });

  it("is none with no error shown — a waiting one included", () => {
    expect(newestError([bar(1), bar(2)])).toBeNull();
    expect(newestError([bar(1, "error"), bar(2), bar(3), bar(4)])).toBeNull();
  });
});

// Escape has one order (APP.md §6, InfoBars; lib/dialogs.ts ESC_OWNERS):
// a dialog, a menu, a popover, the viewer, then the newest error, then the
// list. The review's finding: with the compact rail's flyout open and an
// error up, one Escape closed both.
describe("which bar an Escape closes", () => {
  const list = [bar(1, "error", "first"), bar(2, "info"), bar(3, "error", "newest")];
  const none = { dialogUp: false, ownerUp: false, inField: false };

  it("is the newest error when nothing else owns the key", () => {
    expect(escCloses(list, none)?.id).toBe(3);
  });

  it("is none while a dialog, a menu, a popover or the viewer is up, or in a field", () => {
    expect(escCloses(list, { ...none, dialogUp: true })).toBeNull();
    expect(escCloses(list, { ...none, ownerUp: true })).toBeNull();
    expect(escCloses(list, { ...none, inField: true })).toBeNull();
  });

  it("takes the flyout first and the error with the next Escape", () => {
    // First Escape: the flyout's toggle is expanded, so the bar stays.
    expect(escCloses(list, { ...none, ownerUp: true })).toBeNull();
    // The flyout closed; the second Escape is the error's.
    expect(escCloses(list, none)?.id).toBe(3);
  });

  it("is none with no error shown, and the key goes to the list", () => {
    expect(escCloses([bar(1, "info"), bar(2, "success")], none)).toBeNull();
  });
});

// The popovers the page draws mark their toggles, read as source, so the
// order above sees them open.
const popoverSources = import.meta.glob(["../components/Rail.svelte", "../components/LayerFoot.svelte"], { query: "?raw", import: "default", eager: true }) as Record<string, string>;

describe("the page's popovers own their Escape", () => {
  it("reads both", () => {
    expect(Object.keys(popoverSources)).toHaveLength(2);
  });

  it("mark every toggle with aria-expanded as an Escape owner", () => {
    for (const [file, src] of Object.entries(popoverSources)) {
      const toggles = (src.match(/<button\b[^>]*>/g) ?? []).filter((t) => t.includes("aria-expanded"));
      expect(toggles.length, file).toBeGreaterThan(0);
      for (const t of toggles) expect(t.includes(ESC_OWNER_ATTR), `${file}: ${t}`).toBe(true);
    }
  });

  it("is in the selector App.svelte's escNotice reads", () => {
    expect(ESC_OWNERS).toContain(`[${ESC_OWNER_ATTR}][aria-expanded="true"]`);
    expect(ESC_OWNERS).toContain('[role="menu"]');
    expect(ESC_OWNERS).toContain(".viewer");
  });
});

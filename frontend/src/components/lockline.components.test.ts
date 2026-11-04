// The lock screen's line for the archives the lock left open (APP.md §6,
// Archives open while the vault is locked; ruled 2026-11-03), read as
// source, as rekey.components.test.ts reads its components: when the line
// stands is lib/lockscreen's openLineAfter and its words are strings.ts's,
// both tested there, so these pin the wiring those tests cannot see: that
// the line is no bar among the notices, that it is asked of openLineAfter,
// that a line gone is out of the tab order and the reader and its room
// folds with it, and that its tooltip holds the whole notice.
import { describe, expect, it } from "vitest";
import lockSource from "./Lock.svelte?raw";

const src = lockSource.replace(/\r\n/g, "\n");
const markup = src
  .replace(/<script[\s\S]*?<\/script>/g, "")
  .replace(/<style[\s\S]*?<\/style>/g, "")
  .replace(/<!--[\s\S]*?-->/g, "");

describe("the lock screen's open-archive line, read as source", () => {
  it("is no attention bar among the notices, which are asked without it", () => {
    expect(markup).not.toMatch(/class="bar attention">\s*<svg class="i"><use href="#i-lock"/);
    expect(src).toContain("lockNotices(warnings, store.tampered)");
    expect(src).not.toContain("OPENED");
  });

  it("is one line after the notices and before the step line, its room opening and closing as the archives come or go", () => {
    const line = markup.indexOf('class="openline"');
    expect(line).toBeGreaterThan(markup.indexOf('class="notices"'));
    expect(line).toBeLessThan(markup.indexOf('class="steps"'));
    expect(markup).toMatch(/\{#if !firstRun && openedNames\.length > 0\}\s*<div class="fold openfold" class:shut=\{!lineOn\} transition:unfold=\{\{ duration: lineOn \? undefined : 0 \}\}>/);
  });

  it("stands as openLineAfter says, and a line gone is inert, its room folded", () => {
    expect(src).toContain("openLineAfter(untrack(() => lineOn), shown)");
    expect(markup).toMatch(/<div class="openline" class:off=\{!lineOn\} inert=\{!lineOn\}/);
    expect(markup).not.toMatch(/\{#if[^}]*lineOn/);
  });

  it("names the archive or counts them, with the whole notice as its tooltip and its name for a reader", () => {
    expect(src).toContain("lockCopy.openLine(openedNames)");
    expect(src).toContain("lockCopy.openWhileLocked(openedNames)");
    expect(markup).toContain("use:tip={openedWhole}");
    expect(markup).toContain('aria-label={openedWhole}');
    expect(markup).toContain('<strong class="ol-name">{openedLine.head}</strong>');
  });

  it("offers Show and the close as text buttons", () => {
    const acts = markup.slice(markup.indexOf('class="ol-acts"'), markup.indexOf("</span>", markup.indexOf('class="ol-acts"')));
    expect(acts.match(/class="btn link"/g)).toHaveLength(2);
    expect(acts).toContain("{vaultCopy.show}");
    expect(acts).toContain("onclick={closeOpened}>{noticeClose.label}");
  });
});

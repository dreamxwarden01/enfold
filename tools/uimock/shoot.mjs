// An example tour on the driver (cdp.mjs): the lock stage, the Archives
// page and an archive's file list scrolled, each in light and dark and in
// the two contrast palettes Chromium can emulate (black and white); then the
// lock stage under each palette with the other Theme setting (Light under
// black, Dark under white), a pair the app really meets. It also checks the
// driver's two lessons on every run, and exits with 1 if either has come
// undone:
//
//   - the page lays SVG out on whole device pixels (Page.snapCheck: strokes
//     in boxes at fractional device positions stay two full columns), which
//     only the native scale gives; and the lock stage's 96 px mark (fork D's
//     drawing, Art.svelte) has its left vertical on whole device pixels.
//     That is measured against the colour the stroke is painted with: full
//     columns side by side and no partial one, and ink as wide as the
//     geometry gives, centred where it gives;
//   - the scrolled file list draws a scrollbar: a bar thicker than 0 and a
//     thumb found in its pixels, which --hide-scrollbars would take away.
//
// Usage, after npm run build in frontend/ (the mock serves frontend/dist):
//
//   node tools/uimock/shoot.mjs --out <dir> --profile <dir> --mock-port <port> --cdp-port <port>
//
// Pick ports and a profile no other session uses; the mock's default 8125 is
// refused. Optional: --scale 1.25, --size 1086x694, and --repo <root>, the
// repository's root, which a copy of this tour and cdp.mjs outside the
// repository must pass (the repository's own copy finds it). It writes,
// under <dir>: lock-, archives- and files-<look>.png for the looks light,
// dark, forced-black and forced-white, and lock-<look>.png for
// forced-black-light-setting and forced-white-dark-setting;
// mark-zoom-<look>.png and scrollbar-zoom-<look>.png, enlarged so each
// device pixel shows; and tour.json, the measurements.
import { writeFileSync, mkdirSync } from "node:fs";
import path from "node:path";
import { launch, startMock, strokeProfile, sleep, REPO } from "./cdp.mjs";

function args() {
  const a = {};
  const v = process.argv.slice(2);
  for (let i = 0; i < v.length; i += 2) {
    if (!v[i].startsWith("--") || v[i + 1] === undefined) throw new Error(`bad argument ${v[i]}`);
    a[v[i].slice(2)] = v[i + 1];
  }
  for (const k of ["out", "profile", "mock-port", "cdp-port"]) if (!a[k]) throw new Error(`pass --${k}`);
  if (+a["mock-port"] === 8125) throw new Error("--mock-port 8125 is the mock's default, which a person's own session uses");
  if (!a.repo && !REPO) throw new Error("pass --repo <the repository's root>: this cdp.mjs is a copy outside the repository");
  return a;
}

const A = args();
const OUT = path.resolve(A.out);
const ROOT = A.repo ? path.resolve(A.repo) : REPO;
const SCALE = A.scale ? +A.scale : 1.25;
const [W, H] = (A.size || "1086x694").split("x").map(Number);
const now = Math.floor(Date.now() / 1000);
const ROW = `tbody tr[data-id="${"a1".repeat(16)}"]`; // Photos 2024 on the Archives page
const LIST = ".file-split .tablewrap"; // the archive's file list, beside the preview pane
// The lock stage's mark: fork D's 96 px folded sheet, Sheet(96) cropped to
// 96 x 72 (Art.svelte, held to internal/brand by art_test.go). It replaced
// the header's 20 px i-mark, which went with the brand row (2026-11-01).
const MARK = ".unlock .stage-mark .idart";
// How far the mark's drawn ink may stray from the geometry's width and
// centre, in device px: a quarter pixel, well over the rasteriser's own
// error (within 0.01 in every look here) and half the half pixel a box off
// the grid is out by.
const TOL = 0.25;

// setting is the app's Theme setting under a palette (setForcedColors'
// theme): System for the first two forced looks, the other one for the
// last two, which shoot the lock stage only.
const LOOKS = [
  { name: "light", theme: "light", palette: null },
  { name: "dark", theme: "dark", palette: null },
  { name: "forced-black", theme: "dark", palette: "black" },
  { name: "forced-white", theme: "light", palette: "white" },
  { name: "forced-black-light-setting", theme: "light", palette: "black", setting: "light", lockOnly: true },
  { name: "forced-white-dark-setting", theme: "dark", palette: "white", setting: "dark", lockOnly: true },
];

const LOCKED = { state: "locked", locksAt: 0, absoluteAt: 0, idleEffectiveMinutes: 0, absoluteEffectiveMinutes: 0 };
const UNLOCKED = { state: "unlocked", locksAt: now + 3000, absoluteAt: now + 3 * 3600, idleEffectiveMinutes: 60, absoluteEffectiveMinutes: 180 };

// Photos 2024's root, lengthened so the list scrolls at 1086 x 694: the
// mock stages nine rows, which fit. Two folders and 36 photos; the ids are
// 32 hex digits, as the core's are.
function longList() {
  const root = "0".repeat(32);
  const rows = [
    { id: "f0".repeat(16), parentId: root, isDir: true, name: "2024", size: 0, storage: "", savedPercent: 0, modifiedAt: now - 900000 },
    { id: "fa".repeat(16), parentId: root, isDir: true, name: "Empty folder", size: 0, storage: "", savedPercent: 0, modifiedAt: now - 30000 },
  ];
  for (let i = 1; i <= 36; i++) {
    rows.push({ id: (0xa000 + i).toString(16).padStart(32, "c"), parentId: root, isDir: false, name: `IMG_${7200 + i}.HEIC`,
      size: 3_500_000 + i * 83_000, storage: "raw", savedPercent: 0, modifiedAt: now - 90000 + i * 60 });
  }
  return rows;
}

// The pointer parked in the window's bottom-right corner, so no hover
// colours a row or wakes a scrollbar in the shot.
const park = (p) => p.mouse(W - 2, H - 2);

// The scrollbar in the pixels: a column down the middle of the bar's
// gutter, and the runs in it that differ from the track. A run that lies
// wholly within a bar's width of the gutter's top or bottom is an arrow
// button's glyph; the others are the thumb, so a bar that draws its arrows
// and no thumb is caught.
function barPixels(img, list, bar, dpr) {
  const right = Math.round((list.css.x + list.css.width) * dpr);
  const x = right - Math.max(1, Math.round((bar * dpr) / 2)) - 1;
  const y0 = Math.ceil(list.css.y * dpr), y1 = Math.floor((list.css.y + list.css.height) * dpr);
  const counts = new Map();
  const col = [];
  for (let y = y0; y < y1; y++) {
    const c = img.at(x, y).slice(0, 3);
    col.push(c);
    const k = c.join(",");
    counts.set(k, (counts.get(k) || 0) + 1);
  }
  const track = [...counts.entries()].sort((a, b) => b[1] - a[1])[0][0].split(",").map(Number);
  const ends = Math.ceil(bar * dpr);
  const arrows = [], thumbs = [];
  let start = -1;
  col.forEach((c, i) => {
    const off = Math.abs(c[0] - track[0]) + Math.abs(c[1] - track[1]) + Math.abs(c[2] - track[2]) > 24;
    if (off && start < 0) start = i;
    if ((!off || i === col.length - 1) && start >= 0) {
      const end = off ? i + 1 : i;
      if (end - start >= 6) {
        const run = { top: y0 + start, height: end - start, rgb: col[start + ((end - start) >> 1)] };
        (run.top + run.height <= y0 + ends || run.top >= y1 - ends ? arrows : thumbs).push(run);
      }
      start = -1;
    }
  });
  return { x, track, arrows, thumbs };
}

const mock = await startMock({ port: +A["mock-port"], root: ROOT });
let p;
const tour = { size: [W, H], scale: SCALE, looks: {} };
const problems = [];
try {
  p = await launch({ port: +A["cdp-port"], profile: A.profile, scale: SCALE, width: W, height: H });
  mkdirSync(OUT, { recursive: true });
  tour.edge = p.version;
  tour.dpr = await p.ev("devicePixelRatio");
  await mock.state({ records: longList() });
  for (const look of LOOKS) {
    const r = {};
    tour.looks[look.name] = r;
    await p.setTheme(look.theme);
    await p.setForcedColors(look.palette, { theme: look.setting || "system" });

    // The lock stage, the vault locked and nothing left open.
    await mock.state({ vault: LOCKED, openNames: [] });
    await p.goto(mock.url, { waitFor: ".unlock" });
    await park(p);
    await sleep(300);
    r.systemColors = await p.systemColors();
    r.root = await p.ev(`({ dataTheme: document.documentElement.dataset.theme || null,
      prefersDark: matchMedia("(prefers-color-scheme: dark)").matches })`);
    await p.screenshot(path.join(OUT, `lock-${look.name}.png`));
    const mark = await p.measure(MARK);
    const img = await p.pixels();
    r.capture = [img.w, img.h];
    // The mark's left side is in the .art-line path, stroked with
    // var(--accent) (app.css, the identity drawings), and in a contrast
    // theme with CanvasText (the forced-colours block); its colour is
    // resolved in the drawing's own context. The side runs at x 17.6 of the
    // box's 96 units, from y 18.449 to 53.551 of its 72, 1.6 units wide.
    // Rows at 26-50 units miss the diagonal and the lowest edges, and
    // columns 0-24 units hold the ground on the left and the wash on the
    // right, short of the lines of writing at 25.6. A box that layout puts
    // at a half device pixel (a window an odd number of device pixels
    // narrower than this one) still paints on whole ones, half a pixel to
    // the right, so the box's origin is read rounded.
    const ink = await p.resolveColor(MARK, look.palette ? "CanvasText" : "var(--accent)");
    const u = mark.device.width / 96;
    const x0 = Math.round(mark.device.x), y0 = Math.round(mark.device.y);
    const expected = { centre: +(x0 + 17.6 * u).toFixed(3), width: +(1.6 * u).toFixed(3) };
    r.mark = { box: mark, ink, expected };
    if (!ink || ink[3] !== 1) {
      problems.push(`${look.name}: the mark's stroke colour is not an opaque colour (${JSON.stringify(ink)}), so its columns cannot be scored`);
    } else {
      const s = strokeProfile(img, { x0, x1: x0 + Math.ceil(24 * u), y0: y0 + Math.ceil(26 * u), y1: y0 + Math.floor(50 * u), ink });
      r.mark.stroke = s;
      if (s.whole !== true) problems.push(`${look.name}: the mark's left side is not on whole device pixels: full ${s.full.join(" ") || "none"}, partial ${s.partial.join(" ") || "none"}`);
      if (Math.abs(s.sum - expected.width) > TOL) problems.push(`${look.name}: the mark's left side draws ${s.sum} device px of ink, not ${expected.width}`);
      if (s.centre === null || Math.abs(s.centre - expected.centre) > TOL) problems.push(`${look.name}: the mark's left side is centred at ${s.centre}, not ${expected.centre}`);
    }
    await p.screenshot(path.join(OUT, `mark-zoom-${look.name}.png`), MARK, { pad: 2, zoom: 6 });
    r.snap = await p.snapCheck();
    if (r.snap.whole === false) problems.push(`${look.name}: SVG boxes do not land on whole device pixels: ${JSON.stringify(r.snap.boxes)}`);
    if (look.lockOnly) continue;

    // The Archives page.
    await mock.state({ vault: UNLOCKED });
    await p.goto(mock.url, { waitFor: ROW });
    await park(p);
    await sleep(300);
    await p.screenshot(path.join(OUT, `archives-${look.name}.png`));

    // Photos 2024 opened by a double-click on its row, its list scrolled
    // by the wheel.
    const row = await p.measure(ROW);
    await p.dblclick(row.css.x + 140, row.css.y + row.css.height / 2);
    await p.waitFor(LIST);
    await sleep(700);
    const list = await p.measure(LIST);
    await p.wheel(list.css.x + list.css.width / 2, list.css.y + list.css.height / 2, 360);
    await sleep(900);
    await park(p);
    await sleep(400);
    const m = await p.scrollMetrics(LIST);
    await p.screenshot(path.join(OUT, `files-${look.name}.png`));
    const bar = Math.round(m.bar);
    r.list = { box: list, metrics: m, bar };
    if (bar > 0) {
      r.list.pixels = barPixels(await p.pixels(), list, bar, tour.dpr);
      await p.screenshot(path.join(OUT, `scrollbar-zoom-${look.name}.png`), { x: list.css.x + list.css.width - bar - 6, y: list.css.y, width: bar + 6, height: list.css.height }, { zoom: 3 });
    }
    if (bar <= 0) problems.push(`${look.name}: the file list draws no scrollbar (bar ${m.bar})`);
    else if (!r.list.pixels.thumbs.length) problems.push(`${look.name}: no thumb found in the bar's pixels`);
    if (m.scrollTop <= 0) problems.push(`${look.name}: the wheel did not scroll the list`);
  }
} catch (e) {
  problems.push(String((e && e.stack) || e));
} finally {
  tour.problems = problems;
  mkdirSync(OUT, { recursive: true });
  writeFileSync(path.join(OUT, "tour.json"), JSON.stringify(tour, null, 1));
  if (p) await p.close();
  await mock.stop();
}
const runs = (list) => list.map((t) => `${t.top}+${t.height}`).join(" ") || "none";
for (const [name, r] of Object.entries(tour.looks)) {
  const snap = !r.snap ? "not run" : r.snap.whole === null ? "not checkable at this factor" : r.snap.whole ? "whole" : "smeared";
  const s = r.mark && r.mark.stroke;
  const mark = s ? `mark full ${s.full.join(",") || "none"} partial ${s.partial.join(",") || "none"}, ink ${s.sum} px at ${s.centre}` : "mark not measured";
  const px = r.list && r.list.pixels;
  const list = r.list ? `; list bar ${r.list.bar} px, scrollTop ${r.list.metrics.scrollTop}, thumb ${px ? runs(px.thumbs) : "none"}, arrows ${px ? runs(px.arrows) : "none"}` : "";
  console.log(`${name}: snap ${snap}; ${mark}${list}`);
}
if (problems.length) {
  console.log("PROBLEMS:\n" + problems.join("\n"));
  process.exitCode = 1;
}

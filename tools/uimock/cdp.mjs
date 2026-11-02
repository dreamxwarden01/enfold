// The headless Edge driver for the mock's tours: it launches msedge, drives
// one page over the Chrome DevTools Protocol, and reads the device pixels
// back, so a tour can shoot the page on the path a WebView2 window at 125 %
// is recalled to take, and prove what landed where. What it shoots is
// headless Edge's own drawing, its scrollbar included; whether WebView2
// lays out and draws the same is for a screenshot of the real window to
// confirm. tools/uimock/shoot.mjs is a small tour built on it; README.md
// beside it says how to run one, and what a copy of this file outside the
// repository has to pass.
//
// Two lessons are built in, each paid for once:
//
// 1. Shoot at the native scale. A WebView2 window at 125 % lays the page out
//    in device pixels (Chromium's zoom-for-DSF path; recalled, and borne out
//    by the shots, not yet by a look at the real window). Headless Edge does
//    the same only when the factor is on its command line,
//    --force-device-scale-factor=1.25. Emulation.setDeviceMetricsOverride
//    with deviceScaleFactor 1.25 instead lays the page out in CSS pixels and
//    scales the result: a box at 254 CSS px lands at 317.5 device px, and a
//    bitmap or a stroke in a box at a fractional device position blurs or
//    smears over three pixels. The phase-5 icon study (2026-11-01) ranked
//    its glyph sets wrongly because of it, and designed a snap action that
//    nothing needs. So the factor goes on the command line, and the
//    override passes deviceScaleFactor 0, which keeps the native factor
//    while it fixes the viewport's size in CSS px.
//
// 2. Never pass --hide-scrollbars, in any form. Chromium reads a boolean
//    switch by its presence, so --hide-scrollbars=false hides every
//    scrollbar exactly as --hide-scrollbars does; on Windows it also reads
//    a switch in any case and after one dash or a slash. The driver copied
//    through phases 4a-4e passed it, and none of their tours showed a
//    scrollbar: a scroller's offsetWidth - clientWidth was its 2 px of
//    border, against 17 without the switch. launch() refuses the switch in
//    every one of those forms.
//
// The pixel helpers read one capture of the whole viewport and crop it in
// device px, rather than asking CDP for a clip: a clip whose edge falls
// between device pixels is resampled, and the proof would measure the
// resampling instead of the page.
import { spawn } from "node:child_process";
import { existsSync, mkdirSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { deflateSync, inflateSync } from "node:zlib";

export const EDGE = "C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe";
// The repository's root, from this file's place in tools/uimock, or null
// when this file is a copy outside the repository (the workflow's agents
// each copy the driver into their own folder): two levels above a copy
// there is no tools/uimock/server.py, so startMock() then needs root.
const HERE = path.dirname(fileURLToPath(import.meta.url));
export const REPO = existsSync(path.join(HERE, "..", "..", "tools", "uimock", "server.py")) ? path.resolve(HERE, "..", "..") : null;

export const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// Whether something already answers on a local port. A tour that attached
// to another tour's browser, or posted state to another tour's mock, would
// spoil both, so launch() and startMock() check before they start.
async function answers(url) {
  try {
    await fetch(url, { signal: AbortSignal.timeout(800) });
    return true;
  } catch {
    return false;
  }
}

// Launch headless Edge and attach one page.
//   port     the remote-debugging port, the caller's own (required)
//   profile  the user-data-dir, the caller's own (required): two browsers
//            on one profile hand off to each other and one of them exits
//   scale    the device scale factor, on the command line (default 1.25)
//   width, height   the window and the viewport, in CSS px (1086 x 694, the
//            owner's window less its frame)
//   reducedMotion   emulate prefers-reduced-motion: reduce (default off)
//   args     extra switches for msedge; --hide-scrollbars is refused, in
//            any case and after one dash, two or a slash
export async function launch({ port, profile, scale = 1.25, width = 1086, height = 694, reducedMotion = false, args = [] } = {}) {
  if (!port) throw new Error("launch: pass the remote-debugging port this tour owns");
  if (!profile) throw new Error("launch: pass the user-data-dir this tour owns");
  if (args.some((a) => /^(--?|\/)hide-scrollbars(=|$)/i.test(a))) {
    throw new Error("launch: --hide-scrollbars hides every scrollbar, whatever its value; leave it out");
  }
  if (await answers(`http://127.0.0.1:${port}/json/version`)) {
    throw new Error(`launch: port ${port} already answers; another browser is on it`);
  }
  mkdirSync(profile, { recursive: true });
  const proc = spawn(EDGE, [
    "--headless=new",
    `--remote-debugging-port=${port}`,
    `--user-data-dir=${profile}`,
    `--force-device-scale-factor=${scale}`,
    `--window-size=${width},${height}`,
    "--no-first-run",
    "--no-default-browser-check",
    "--disable-extensions",
    ...args,
    "about:blank",
  ], { stdio: "ignore" });
  // msedge.exe hands the launch to a second process of its own and exits
  // with 0 at once (measured: within 50 ms, on every launch, a new profile
  // or not), so proc is only the launcher and its exit says nothing about
  // the browser; close() ends the browser through Browser.close. A missing
  // msedge.exe still ends the wait at once, rather than after 15 s.
  let failed = null;
  proc.on("error", (e) => { failed = `could not start ${EDGE}: ${e.message}`; });
  let ver;
  for (let i = 0; i < 60 && !ver && !failed; i++) {
    try {
      ver = await (await fetch(`http://127.0.0.1:${port}/json/version`)).json();
    } catch {
      await sleep(250);
    }
  }
  if (!ver) {
    try { proc.kill(); } catch {}
    throw new Error(`launch: ${failed || `Edge did not answer on port ${port} within 15 s`}`);
  }
  const ws = new WebSocket(ver.webSocketDebuggerUrl);
  try {
    await new Promise((res, rej) => { ws.onopen = res; ws.onerror = rej; });
  } catch {
    try { proc.kill(); } catch {}
    throw new Error("launch: the DevTools socket did not open");
  }
  let id = 0;
  const pending = new Map();
  const listeners = new Set();
  ws.onmessage = (m) => {
    const msg = JSON.parse(m.data);
    if (msg.id && pending.has(msg.id)) {
      const { res, rej } = pending.get(msg.id);
      pending.delete(msg.id);
      if (msg.error) rej(new Error(`${msg.error.message} (${msg.error.code})`));
      else res(msg.result);
    } else if (msg.method) {
      for (const l of listeners) l(msg);
    }
  };
  const send = (method, params = {}, sessionId) => new Promise((res, rej) => {
    const mid = ++id;
    pending.set(mid, { res, rej });
    ws.send(JSON.stringify({ id: mid, method, params, sessionId }));
  });
  const { targetId } = await send("Target.createTarget", { url: "about:blank" });
  const { sessionId } = await send("Target.attachToTarget", { targetId, flatten: true });
  const page = new Page({ send, sessionId, listeners, proc, ws, version: ver.Browser, scale, reducedMotion });
  try {
    await page.send("Page.enable");
    await page.send("Runtime.enable");
    // The window being shot is the active one, as the owner's is: without
    // this a headless page is unfocused and draws no :focus-visible ring.
    await page.send("Emulation.setFocusEmulationEnabled", { enabled: true });
    await page.size(width, height);
    await page.media();
    const dpr = await page.ev("devicePixelRatio");
    if (Math.abs(dpr - scale) > 1e-6) throw new Error(`launch: devicePixelRatio is ${dpr}, not ${scale}`);
  } catch (e) {
    await page.close();
    throw e;
  }
  return page;
}

export class Page {
  constructor({ send, sessionId, listeners, proc, ws, version, scale, reducedMotion }) {
    this._send = send;
    this.sessionId = sessionId;
    this.listeners = listeners;
    this.proc = proc;
    this.ws = ws;
    this.version = version;
    this.dpr = scale;
    this.reducedMotion = reducedMotion;
    this.width = 0;
    this.height = 0;
    this.theme = "light";
    this.palette = null;
    this.forcedTheme = "system";
  }

  // A raw CDP call on this page's session.
  send(method, params = {}) {
    return this._send(method, params, this.sessionId);
  }

  // Evaluate an expression in the page and return its value (awaited, by
  // value). An exception in the page is thrown here with its message.
  async ev(expr) {
    const r = await this.send("Runtime.evaluate", { expression: expr, awaitPromise: true, returnByValue: true });
    if (r.exceptionDetails) throw new Error("ev: " + (r.exceptionDetails.exception?.description || r.exceptionDetails.text));
    return r.result.value;
  }

  // The viewport in CSS px. deviceScaleFactor 0 keeps the factor from the
  // command line (lesson 1 above); a non-zero one here would put the page
  // back on the emulated path.
  async size(width, height) {
    this.width = width;
    this.height = height;
    await this.send("Emulation.setDeviceMetricsOverride", { width, height, deviceScaleFactor: 0, mobile: false });
  }

  // Open a URL and wait for the page to settle: the load event, the fonts,
  // then the selector if one is named, then a short wait for the entering
  // transitions. On the mock it also starts the event drain its docstring
  // describes (server.py, "Events"), since the page has no Wails socket
  // there. The theme is put back on the root, which a reload clears.
  async goto(url, { waitFor = null, wait = 600, drain = true, timeout = 8000 } = {}) {
    let l;
    const loaded = new Promise((res) => {
      l = (m) => {
        if (m.method === "Page.loadEventFired" && m.sessionId === this.sessionId) res();
      };
      this.listeners.add(l);
    });
    await this.send("Page.navigate", { url });
    await Promise.race([loaded, sleep(timeout)]);
    // Taken out whether the load event came or the wait ran out, so a slow
    // page leaves no listener behind for every later message to call.
    this.listeners.delete(l);
    await this.ev("document.fonts.ready.then(() => true)");
    if (drain && /^https?:/.test(url)) {
      await this.ev(`(() => {
        if (window.__drain) return true;
        window.__drain = setInterval(async () => {
          try {
            for (const e of await (await fetch("/mock/events")).json()) window._wails.dispatchWailsEvent(e);
          } catch {}
        }, 200);
        return true;
      })()`);
    }
    if (waitFor) await this.waitFor(waitFor, timeout);
    await sleep(wait);
    await this.applyTheme();
  }

  // Wait until an element matching the selector is rendered.
  async waitFor(selector, timeout = 8000) {
    const end = Date.now() + timeout;
    while (Date.now() < end) {
      const found = await this.ev(`(() => {
        const e = document.querySelector(${JSON.stringify(selector)});
        return !!e && e.getClientRects().length > 0;
      })()`);
      if (found) return;
      await sleep(100);
    }
    throw new Error(`waitFor: nothing matches ${selector}`);
  }

  // The app's theme: data-theme on the root, as App.svelte sets it from the
  // setting, with prefers-color-scheme emulated to agree, so a rule that
  // reads the media query (app.css's system theme) sees the same theme.
  async setTheme(theme) {
    if (theme !== "light" && theme !== "dark") throw new Error(`setTheme: ${theme}`);
    this.theme = theme;
    await this.media();
    await this.applyTheme();
  }

  // A Windows contrast theme: forced-colors active, with the palette chosen
  // by prefers-color-scheme, which is how Chromium's emulation picks one -
  // "black" is the dark palette (Canvas black, CanvasText white), "white" the
  // light one. theme is the app's own Theme setting under it. "system", the
  // default, leaves data-theme off the root, as App.svelte does for that
  // setting, so app.css follows the palette's scheme. "light" or "dark"
  // sets data-theme, as the Light or Dark setting does under any contrast
  // theme. Light under HC Black is a real pair, and it draws differently
  // from System: the root keeps the light tokens, which show wherever the
  // palette does not force a colour (the mark's SVG paint, for one). The
  // palette and the theme are both kept across goto. null turns forced
  // colours off and puts back the theme setTheme last chose. Windows 11's
  // other palettes (Aquatic, Desert, Dusk, Night sky) cannot be emulated;
  // only the real window shows them.
  async setForcedColors(palette, { theme = "system" } = {}) {
    if (palette !== null && palette !== "black" && palette !== "white") throw new Error(`setForcedColors: ${palette}`);
    if (theme !== "system" && theme !== "light" && theme !== "dark") throw new Error(`setForcedColors: theme ${theme}`);
    this.palette = palette;
    this.forcedTheme = theme;
    await this.media();
    await this.applyTheme();
  }

  // The scheme prefers-color-scheme reports: the palette's while forced
  // colours are on, as Windows reports its contrast theme's, whatever the
  // app's own setting.
  get scheme() {
    return this.palette ? (this.palette === "black" ? "dark" : "light") : this.theme;
  }

  // The root's data-theme: setTheme's choice, or under forced colours the
  // theme passed with the palette, null (none) for "system".
  get rootTheme() {
    if (!this.palette) return this.theme;
    return this.forcedTheme === "system" ? null : this.forcedTheme;
  }

  async media() {
    const features = [
      { name: "prefers-color-scheme", value: this.scheme },
      { name: "forced-colors", value: this.palette ? "active" : "none" },
      { name: "prefers-reduced-motion", value: this.reducedMotion ? "reduce" : "no-preference" },
    ];
    await this.send("Emulation.setEmulatedMedia", { features });
  }

  async applyTheme() {
    const t = this.rootTheme;
    await this.ev(t ? `document.documentElement.dataset.theme = ${JSON.stringify(t)}` : "delete document.documentElement.dataset.theme");
  }

  // What the forced palette resolved to, as the page's computed system
  // colours: proof that the emulation took, and which palette it is.
  async systemColors() {
    return this.ev(`(() => {
      const d = document.createElement("div");
      d.style.cssText = "color: CanvasText; background: Canvas; border: 1px solid Highlight; outline: 1px solid GrayText";
      document.body.append(d);
      const s = getComputedStyle(d);
      const r = { forced: matchMedia("(forced-colors: active)").matches, canvas: s.backgroundColor,
        canvasText: s.color, highlight: s.borderTopColor, grayText: s.outlineColor };
      d.remove();
      return r;
    })()`);
  }

  // A colour as the page resolves it in an element's own context: value
  // (such as "var(--accent)") set on property (stroke by default) of a
  // probe put inside the first match, and read back computed. It gives
  // strokeProfile() the ink a stroke is painted with where the painted
  // element cannot be reached: a <use>'s shadow tree is closed to the DOM.
  // The probe inherits what the element inherits, forced-color-adjust
  // included, so a palette that forces the property forces it here too.
  // [r, g, b, a] (a from 0 to 1), or null when nothing matches or the value
  // is not a plain colour (none, a url()).
  async resolveColor(selector, value, property = "stroke") {
    return this.ev(`(() => {
      const e = document.querySelector(${JSON.stringify(selector)});
      if (!e) return null;
      const p = e instanceof SVGElement ? document.createElementNS("http://www.w3.org/2000/svg", "g") : document.createElement("span");
      p.style.setProperty(${JSON.stringify(property)}, ${JSON.stringify(value)});
      e.append(p);
      const v = getComputedStyle(p).getPropertyValue(${JSON.stringify(property)}).trim();
      p.remove();
      const m = /^rgba?\\(([^)]*)\\)$/.exec(v);
      if (!m) return null;
      const n = m[1].split(/[\\s,\\/]+/).filter(Boolean).map(Number);
      return [n[0], n[1], n[2], n.length > 3 ? n[3] : 1];
    })()`);
  }

  // An element's box (getBoundingClientRect) in CSS px and in device px,
  // and whether its left and top edges fall on whole device pixels in
  // layout. null when nothing matches. Layout is not paint: a box at a half
  // device pixel in layout can still paint on whole ones, which only the
  // pixels show (pixels() and strokeProfile()).
  async measure(selector) {
    const r = await this.ev(`(() => {
      const e = document.querySelector(${JSON.stringify(selector)});
      if (!e) return null;
      const b = e.getBoundingClientRect();
      return { x: b.x, y: b.y, width: b.width, height: b.height, dpr: devicePixelRatio };
    })()`);
    if (!r) return null;
    const k = r.dpr;
    const round = (v) => Math.round(v * 1000) / 1000;
    const device = { x: round(r.x * k), y: round(r.y * k), width: round(r.width * k), height: round(r.height * k) };
    return {
      css: { x: r.x, y: r.y, width: r.width, height: r.height },
      device,
      dpr: k,
      whole: { x: Number.isInteger(device.x), y: Number.isInteger(device.y) },
    };
  }

  // A scroller's facts: the vertical bar's thickness in CSS px (the box's
  // width less its borders and its client width: 0 when no bar is drawn or
  // the bars are hidden; clientWidth is a whole number, so the figure is
  // good to a pixel), the same for the horizontal bar, and how far the
  // scroller scrolls and has scrolled.
  async scrollMetrics(selector) {
    return this.ev(`(() => {
      const e = document.querySelector(${JSON.stringify(selector)});
      if (!e) return null;
      const s = getComputedStyle(e), b = e.getBoundingClientRect();
      const px = (v) => parseFloat(v) || 0;
      const r2 = (v) => Math.round(v * 100) / 100;
      return {
        bar: r2(b.width - px(s.borderLeftWidth) - px(s.borderRightWidth) - e.clientWidth),
        barBottom: r2(b.height - px(s.borderTopWidth) - px(s.borderBottomWidth) - e.clientHeight),
        scrollTop: e.scrollTop, scrollHeight: e.scrollHeight, clientHeight: e.clientHeight, clientWidth: e.clientWidth,
      };
    })()`);
  }

  // Whether this page lays SVG boxes out on whole device pixels, which is
  // lesson 1 made checkable. Three 20 px boxes go over the page's top-left
  // corner for one capture, at whole CSS offsets that the factor puts
  // between device pixels (snapOffsets(): 21, 42 and 63 CSS px at 125 %, so
  // 26.25, 52.5 and 78.75 device px), each holding a vertical stroke two
  // device px wide whose edges fall on device pixels when its box does. On
  // the native path every box snaps to a whole device pixel and every
  // stroke is two full columns; on the emulated path the boxes keep their
  // fractions and the strokes smear into partial columns. A mark at a whole
  // CSS position, as the old brand row's was at 20 px, is crisp either way
  // and proves nothing about the path, which is why this probe exists. At a
  // whole factor (1, 2) every whole CSS offset is a whole device offset on
  // both paths, so the probe cannot tell them apart: whole is then null,
  // and note says why.
  async snapCheck() {
    const k = this.dpr;
    const offsets = snapOffsets(k);
    if (!offsets) {
      return { whole: null, factor: k, boxes: [], note: `at factor ${k} no whole CSS offset falls between device pixels, so the check cannot tell the native path from the emulated one` };
    }
    // One unit of the 20-unit box is one CSS px, which is k device px, so a
    // stroke centred at 4 / k units and 2 / k units wide covers device px 3
    // to 5 of its box.
    const svg = (x) => `<svg style="position: absolute; left: ${x}px; top: 10px; width: 20px; height: 20px; display: block" `
      + `viewBox="0 0 20 20"><path d="M${4 / k} 0V20" stroke="#000" stroke-width="${2 / k}" fill="none"/></svg>`;
    await this.ev(`(() => {
      const h = document.createElement("div");
      h.id = "__snapcheck";
      h.style.cssText = "position: fixed; left: 0; top: 0; width: ${offsets[2] + 30}px; height: 40px; background: #fff; "
        + "z-index: 2147483647; forced-color-adjust: none";
      h.innerHTML = ${JSON.stringify(offsets.map(svg).join(""))};
      document.body.append(h);
      return true;
    })()`);
    await sleep(100);
    let img;
    try {
      img = await this.pixels();
    } finally {
      await this.ev(`document.getElementById("__snapcheck")?.remove(), true`);
    }
    const y0 = Math.ceil(12 * k), y1 = Math.floor(28 * k);
    const boxes = offsets.map((x) => {
      const at = x * k;
      const prof = strokeProfile(img, { x0: Math.floor(at), x1: Math.floor(at) + 7, y0, y1, ink: [0, 0, 0] });
      return { css: x, device: at, full: prof.full, partial: prof.partial, whole: prof.whole && prof.full.length === 2 };
    });
    return { whole: boxes.every((b) => b.whole), factor: k, boxes };
  }

  // One capture of the viewport, decoded: device px, top-left at (0, 0).
  async pixels() {
    const r = await this.send("Page.captureScreenshot", { format: "png" });
    return Bitmap.decode(Buffer.from(r.data, "base64"));
  }

  // Write a PNG of the viewport, or of a clip of it: clip is a CSS-px rect
  // {x, y, width, height} or a selector (its box, grown by pad CSS px). The
  // clip is cut from one capture of the viewport on whole device pixels
  // (outward), so the file holds the window's own pixels. zoom > 1 enlarges
  // it by nearest neighbour, for showing pixels one by one. Returns the
  // device-px rect written.
  async screenshot(file, clip = null, { pad = 0, zoom = 1 } = {}) {
    const img = await this.pixels();
    let rect = { x: 0, y: 0, width: img.w, height: img.h };
    if (clip) {
      let c = clip;
      if (typeof clip === "string") {
        const m = await this.measure(clip);
        if (!m) throw new Error(`screenshot: nothing matches ${clip}`);
        c = { x: m.css.x - pad, y: m.css.y - pad, width: m.css.width + 2 * pad, height: m.css.height + 2 * pad };
      }
      const k = this.dpr;
      const x0 = Math.max(0, Math.floor(c.x * k)), y0 = Math.max(0, Math.floor(c.y * k));
      const x1 = Math.min(img.w, Math.ceil((c.x + c.width) * k)), y1 = Math.min(img.h, Math.ceil((c.y + c.height) * k));
      rect = { x: x0, y: y0, width: x1 - x0, height: y1 - y0 };
    }
    let out = img.crop(rect.x, rect.y, rect.width, rect.height);
    if (zoom > 1) out = out.zoom(zoom);
    mkdirSync(path.dirname(file), { recursive: true });
    writeFileSync(file, out.encode());
    return rect;
  }

  // Real input, through CDP, at CSS-px points.
  async mouse(x, y, type = "mouseMoved", button = "none", clickCount = 0) {
    await this.send("Input.dispatchMouseEvent", { type, x, y, button, clickCount });
  }
  async click(x, y, button = "left") {
    await this.mouse(x, y, "mouseMoved");
    await this.mouse(x, y, "mousePressed", button, 1);
    await this.mouse(x, y, "mouseReleased", button, 1);
  }
  async rightClick(x, y) {
    await this.click(x, y, "right");
  }
  async dblclick(x, y) {
    await this.click(x, y);
    await this.mouse(x, y, "mousePressed", "left", 2);
    await this.mouse(x, y, "mouseReleased", "left", 2);
  }
  async wheel(x, y, deltaY, deltaX = 0) {
    await this.send("Input.dispatchMouseEvent", { type: "mouseWheel", x, y, deltaX, deltaY });
  }
  async key(key, code, keyCode, modifiers = 0) {
    await this.send("Input.dispatchKeyEvent", { type: "keyDown", key, code, windowsVirtualKeyCode: keyCode, modifiers });
    await this.send("Input.dispatchKeyEvent", { type: "keyUp", key, code, windowsVirtualKeyCode: keyCode, modifiers });
  }
  async type(text) {
    await this.send("Input.insertText", { text });
  }

  // Browser.close is what ends the browser: the process launch() spawned
  // is only the launcher, gone by now, so the kill after it is a fallback
  // for an msedge.exe that stays.
  async close() {
    try { await this.send("Browser.close"); } catch {}
    try { this.ws.close(); } catch {}
    await sleep(500);
    try { this.proc.kill(); } catch {}
  }
}

// snapCheck()'s offsets: three whole CSS offsets, a box apart, that the
// factor k puts well between device pixels (a fraction from 0.2 to 0.8,
// where a smear shows as partial columns), different fractions first; null
// when there are not three below 400 px, as at a whole factor.
function snapOffsets(k) {
  const frac = (x) => Math.round((x * k - Math.floor(x * k + 1e-9)) * 1000) / 1000;
  const fits = [];
  for (let x = 21; x < 400; x++) if (frac(x) >= 0.2 && frac(x) <= 0.8) fits.push(x);
  const picked = [];
  for (const distinct of [true, false]) {
    for (const x of fits) {
      if (picked.length === 3) break;
      if (picked.some((p) => Math.abs(p - x) < 21)) continue;
      if (distinct && picked.some((p) => frac(p) === frac(x))) continue;
      picked.push(x);
    }
  }
  return picked.length === 3 ? picked.sort((a, b) => a - b) : null;
}

// An RGBA bitmap in device px, with the PNG decoder and encoder the proofs
// need (8-bit RGB or RGBA, not interlaced: what CDP writes), so nothing has
// to be installed to read a pixel.
export class Bitmap {
  constructor(w, h, px) {
    this.w = w;
    this.h = h;
    this.px = px;
  }

  static decode(buf) {
    if (buf.readUInt32BE(0) !== 0x89504e47) throw new Error("not a PNG");
    let off = 8, w = 0, h = 0, depth = 0, ctype = 0, inter = 0;
    const idat = [];
    while (off < buf.length) {
      const len = buf.readUInt32BE(off);
      const type = buf.toString("ascii", off + 4, off + 8);
      const data = buf.subarray(off + 8, off + 8 + len);
      if (type === "IHDR") { w = data.readUInt32BE(0); h = data.readUInt32BE(4); depth = data[8]; ctype = data[9]; inter = data[12]; }
      else if (type === "IDAT") idat.push(data);
      else if (type === "IEND") break;
      off += 12 + len;
    }
    if (depth !== 8 || inter !== 0 || (ctype !== 6 && ctype !== 2)) throw new Error(`unsupported PNG ${depth}/${ctype}/${inter}`);
    const bpp = ctype === 6 ? 4 : 3;
    const raw = inflateSync(Buffer.concat(idat));
    const stride = w * bpp;
    const out = new Uint8Array(w * h * 4);
    const prev = new Uint8Array(stride);
    const cur = new Uint8Array(stride);
    for (let y = 0; y < h; y++) {
      const f = raw[y * (stride + 1)];
      const line = raw.subarray(y * (stride + 1) + 1, (y + 1) * (stride + 1));
      for (let i = 0; i < stride; i++) {
        const a = i >= bpp ? cur[i - bpp] : 0, b = prev[i], c = i >= bpp ? prev[i - bpp] : 0;
        let v = line[i];
        if (f === 1) v += a;
        else if (f === 2) v += b;
        else if (f === 3) v += (a + b) >> 1;
        else if (f === 4) { const p = a + b - c, pa = Math.abs(p - a), pb = Math.abs(p - b), pc = Math.abs(p - c); v += pa <= pb && pa <= pc ? a : pb <= pc ? b : c; }
        cur[i] = v & 255;
      }
      for (let x = 0; x < w; x++) {
        const d = (y * w + x) * 4;
        out[d] = cur[x * bpp]; out[d + 1] = cur[x * bpp + 1]; out[d + 2] = cur[x * bpp + 2];
        out[d + 3] = bpp === 4 ? cur[x * bpp + 3] : 255;
      }
      prev.set(cur);
    }
    return new Bitmap(w, h, out);
  }

  // [r, g, b, a] at a device pixel.
  at(x, y) {
    const i = (y * this.w + x) * 4;
    return [this.px[i], this.px[i + 1], this.px[i + 2], this.px[i + 3]];
  }

  crop(x, y, w, h) {
    const out = new Uint8Array(w * h * 4);
    for (let j = 0; j < h; j++) out.set(this.px.subarray(((y + j) * this.w + x) * 4, ((y + j) * this.w + x + w) * 4), j * w * 4);
    return new Bitmap(w, h, out);
  }

  // Nearest neighbour, k times: each device pixel becomes a k x k square.
  zoom(k) {
    const w = this.w * k, h = this.h * k, out = new Uint8Array(w * h * 4);
    for (let y = 0; y < h; y++) for (let x = 0; x < w; x++) {
      const s = (Math.floor(y / k) * this.w + Math.floor(x / k)) * 4, d = (y * w + x) * 4;
      out[d] = this.px[s]; out[d + 1] = this.px[s + 1]; out[d + 2] = this.px[s + 2]; out[d + 3] = this.px[s + 3];
    }
    return new Bitmap(w, h, out);
  }

  encode() {
    const { w, h, px } = this;
    const raw = Buffer.alloc((w * 4 + 1) * h);
    for (let y = 0; y < h; y++) Buffer.from(px.buffer, px.byteOffset + y * w * 4, w * 4).copy(raw, y * (w * 4 + 1) + 1);
    const ihdr = Buffer.alloc(13);
    ihdr.writeUInt32BE(w, 0); ihdr.writeUInt32BE(h, 4); ihdr[8] = 8; ihdr[9] = 6;
    return Buffer.concat([Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]), chunk("IHDR", ihdr), chunk("IDAT", deflateSync(raw)), chunk("IEND", Buffer.alloc(0))]);
  }
}

const CRC = (() => {
  const t = new Uint32Array(256);
  for (let n = 0; n < 256; n++) { let c = n; for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1; t[n] = c >>> 0; }
  return t;
})();
function chunk(type, data) {
  const td = Buffer.concat([Buffer.from(type, "ascii"), data]);
  let c = 0xffffffff;
  for (const b of td) c = CRC[(c ^ b) & 255] ^ (c >>> 8);
  const len = Buffer.alloc(4); len.writeUInt32BE(data.length);
  const crc = Buffer.alloc(4); crc.writeUInt32BE((c ^ 0xffffffff) >>> 0);
  return Buffer.concat([len, td, crc]);
}

// Where a vertical stroke falls on the device-pixel grid. The region
// (device px, x1 and y1 exclusive) spans the stroke with some ground on
// either side and rows along the stroke's straight run. Each column's mean
// colour is placed between its side's ground (the region's first column on
// the left, its last on the right, so a stroke over a fill is read against
// the fill) and ink, the colour the stroke is painted with ([r, g, b], as
// Page.resolveColor() gives it): 1 is full ink, 0 is ground. The sides
// meet halfway between the stroke's outer columns (the first from the left
// with ink against the left ground, the first from the right with ink
// against the right one), so each edge column is read against the ground
// it blends with. Read against the other ground, a column of a stroke
// drawn over black beside a pale fill overshoots and is clipped to 1,
// which would pass a partial column as full. A column at
// 0.85 or more is full, one at 0.15 or less is clear, anything between is
// partial. A stroke on whole device pixels has full columns side by side
// and no partial one; the same stroke at a half pixel shows a partial
// column at each edge. sum is the columns' ink added up, the stroke's
// width in device px as drawn, and centre the ink's middle in device px
// (a column x spans x to x + 1), to hold against the width and the centre
// the geometry gives.
//
// Pass ink. Without it the column farthest from both grounds stands in for
// the ink, so every column is scored against the stroke's own peak, and a
// stroke split evenly over two columns (a 1 px stroke centred on a pixel
// edge, 0.5 and 0.5 of the true ink) reads as two full columns. The result
// is then marked relative, and whole is null: it proves nothing about the
// grid.
export function strokeProfile(img, { x0, x1, y0, y1, ink = null }) {
  const cols = [];
  for (let x = x0; x < x1; x++) {
    const m = [0, 0, 0];
    for (let y = y0; y < y1; y++) { const p = img.at(x, y); m[0] += p[0]; m[1] += p[1]; m[2] += p[2]; }
    cols.push({ x, rgb: m.map((v) => Math.round(v / (y1 - y0))) });
  }
  const left = cols[0].rgb, right = cols[cols.length - 1].rgb;
  const dist = (a, b) => Math.hypot(a[0] - b[0], a[1] - b[1], a[2] - b[2]);
  let peak = 0, best = -1;
  cols.forEach((c, i) => { const d = Math.min(dist(c.rgb, left), dist(c.rgb, right)); if (d > best) { best = d; peak = i; } });
  const inkRgb = ink ? ink.slice(0, 3) : cols[peak].rgb;
  const frac = (c, g) => {
    const v = [inkRgb[0] - g[0], inkRgb[1] - g[1], inkRgb[2] - g[2]];
    const n = v[0] * v[0] + v[1] * v[1] + v[2] * v[2];
    if (!n) return 0;
    const f = ((c[0] - g[0]) * v[0] + (c[1] - g[1]) * v[1] + (c[2] - g[2]) * v[2]) / n;
    return Math.max(0, Math.min(1, f));
  };
  const fl = cols.map((c) => frac(c.rgb, left)), fr = cols.map((c) => frac(c.rgb, right));
  const a = fl.findIndex((f) => f > 0.15), b = fr.findLastIndex((f) => f > 0.15);
  const mid = a >= 0 && b >= a ? (a + b) / 2 : peak;
  const columns = cols.map((c, i) => {
    const f = Math.round((i <= mid ? fl[i] : fr[i]) * 1000) / 1000;
    return { x: c.x, rgb: c.rgb, ink: f, kind: f >= 0.85 ? "full" : f > 0.15 ? "partial" : "clear" };
  });
  const full = columns.filter((c) => c.kind === "full").map((c) => c.x);
  const partial = columns.filter((c) => c.kind === "partial").map((c) => c.x);
  const adjacent = full.every((x, i) => i === 0 || x === full[i - 1] + 1);
  const total = columns.reduce((s, c) => s + c.ink, 0);
  const sum = Math.round(total * 1000) / 1000;
  const centre = total ? Math.round((columns.reduce((s, c) => s + (c.x + 0.5) * c.ink, 0) / total) * 1000) / 1000 : null;
  const whole = full.length > 0 && partial.length === 0 && adjacent;
  return { columns, ground: { left, right }, ink: inkRgb, relative: !ink, full, partial, sum, centre, whole: ink ? whole : null };
}

// The mock (server.py), started from the repository's root on a port the
// caller owns, never its default 8125, which a person's own session uses.
// root is the repository's root: REPO by default, which a copy of this file
// outside the repository does not have, so a copy passes it. Before it
// starts anything it checks that root holds server.py and the built page,
// since a missing page still lets the mock answer, with a 404; and if the
// server exits before it answers, the wait ends at once with the server's
// last words. state() and preview() post to /mock/state and /mock/preview.
export async function startMock({ port, root = REPO, python = "python" } = {}) {
  if (!port) throw new Error("startMock: pass the port this tour owns");
  if (!root) throw new Error("startMock: this cdp.mjs is a copy outside the repository; pass root, the repository's root");
  const script = path.join(root, "tools", "uimock", "server.py");
  if (!existsSync(script)) throw new Error(`startMock: no ${script}; root must be the repository's root`);
  const index = path.join(root, "frontend", "dist", "index.html");
  if (!existsSync(index)) throw new Error(`startMock: no ${index}; run npm run build in frontend/ first`);
  const url = `http://127.0.0.1:${port}/`;
  if (await answers(url)) throw new Error(`startMock: port ${port} already answers; another server is on it`);
  const proc = spawn(python, ["-I", script, String(port)], { cwd: root, stdio: ["ignore", "ignore", "pipe"] });
  // The server's stderr, read all along so the pipe never fills, and its
  // last 2000 characters kept for an error message.
  let tail = "";
  proc.stderr.setEncoding("utf8");
  proc.stderr.on("data", (d) => { tail = (tail + d).slice(-2000); });
  let failed = null;
  proc.on("error", (e) => { failed ??= `could not start ${python}: ${e.message}`; });
  proc.on("close", (code) => { failed ??= `server.py exited with code ${code} before it answered`; });
  for (let i = 0; i < 60 && !failed; i++) {
    if (await answers(url)) return new Mock(url, proc, () => tail);
    await sleep(200);
  }
  try { proc.kill(); } catch {}
  const last = tail.trim().split(/\r?\n/).slice(-3).join(" | ");
  throw new Error(`startMock: ${failed || `the mock did not answer on port ${port} within 12 s`}${last ? `; its stderr ends: ${last}` : ""}`);
}

export class Mock {
  constructor(url, proc, tail = () => "") {
    this.url = url;
    this.proc = proc;
    this.tail = tail;
  }
  // The last of the server's stderr: a handler's traceback, if one failed.
  get stderr() {
    return this.tail();
  }
  async post(route, body) {
    const r = await fetch(this.url + route.replace(/^\//, ""), { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
    return r.json();
  }
  state(body) {
    return this.post("/mock/state", body);
  }
  preview(body) {
    return this.post("/mock/preview", body);
  }
  async stop() {
    const gone = new Promise((res) => this.proc.once("exit", res));
    try { this.proc.kill(); } catch {}
    await Promise.race([gone, sleep(3000)]);
  }
}

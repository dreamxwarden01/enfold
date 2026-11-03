# uimock: the page without the app

`server.py` stands in for the Wails backend: it serves the built page (`frontend/dist`) and
answers the bindings with canned data, so the interface can be exercised in a browser with no
vault, no YubiKey and no shell. Its docstring is the manual: every staged state, every preview
and every knob is described there.

`cdp.mjs` is the headless Edge driver the project's tours use, and `shoot.mjs` is a small tour
built on it. What they shoot is headless Edge's own drawing, its scrollbar included; whether a
WebView2 window lays out and draws the same is for a screenshot of the real window to confirm.

## Running the mock by hand

```
cd frontend && npm run build && cd ..
python -I tools/uimock/server.py 8125
```

Then open `http://127.0.0.1:8125/`. A tour does not use this copy: it starts its own.

## Shooting a tour

```
node tools/uimock/shoot.mjs --out <dir> --profile <dir> --mock-port <port> --cdp-port <port>
```

Run it after `npm run build`. Give it ports and an Edge profile that nothing else on the machine
is using; the driver refuses a port that already answers, and the tour refuses 8125. It shoots
the lock stage, the Archives page and an archive's file list, scrolled, in light and dark and in
the two contrast palettes Chromium can emulate, then the lock stage under each palette with
the other Theme setting, at 1086 x 694 CSS px and 125 %. Beside the shots it writes enlarged
crops of the mark and the scrollbar and a `tour.json` with the measurements, and it exits with 1
if either lesson below has come undone, or if the file list's bar is not the one its look draws:
the app's own in light and dark (app.css: a 12 px gutter with no arrows, its line on whole
device pixels), the system's under a contrast palette.

A copy of `cdp.mjs` outside the repository, such as the one each of the workflow's agents keeps
in its own folder, cannot tell where the repository is. Pass `--repo <root>` to a copied
`shoot.mjs`, and `root` to `startMock()` in a copied tour; without it `startMock()` says so and
starts nothing. A tour can also import the repository's own `cdp.mjs` by its file URL instead of
copying it.

## The driver

`launch({ port, profile, scale, width, height })` starts `msedge --headless=new` and returns a
`Page`:

- `goto(url, { waitFor })`: load, wait for the fonts and the selector, and start the mock's
  event drain;
- `setTheme("light" | "dark")`, and `setForcedColors("black" | "white" | null, { theme })`,
  where `theme` is the app's Theme setting under the palette: `"system"` (the default, no
  `data-theme`) or `"light"` or `"dark"`, kept across `goto`;
- `measure(selector)`: the box in CSS px and in device px;
- `screenshot(file, clip, { zoom })`: the viewport or a clip of it, cut on whole device pixels;
- `pixels()`, with `strokeProfile()`: the device pixels, and where a vertical stroke falls on
  them. Pass `ink`, the colour the stroke is painted with (`resolveColor(selector, value)` gives
  it): without it a stroke split evenly over two columns reads as two full ones, so `whole` is
  then `null`;
- `snapCheck()`: whether SVG boxes land on whole device pixels at all (`whole` is `null` at a
  whole factor, where it proves nothing);
- `scrollMetrics(selector)`, and real input: `click`, `dblclick`, `rightClick`, `wheel`, `key`,
  `type`.

`startMock({ port, root })` checks that `root` holds `server.py` and the built page, runs
`server.py` on the tour's own port, and stops at once with the server's last words if it exits;
its `state()` and `preview()` post to `/mock/state` and `/mock/preview`.

Two lessons are built into it, and its header comment gives the detail:

1. **The native scale.** The factor goes on Edge's command line
   (`--force-device-scale-factor=1.25`) and the viewport override passes `deviceScaleFactor: 0`.
   Emulating the factor in the override instead lays the page out in CSS px, and a bitmap or a
   stroke in a box at a fractional device position blurs or smears over three pixels. A WebView2
   window at 125 % lays the page out in device px, as recalled; one look at the real window is
   still owed to confirm it.
2. **No `--hide-scrollbars`.** Chromium reads the switch by its presence, so even
   `--hide-scrollbars=false` hides every scrollbar, and on Windows it takes the switch in any
   case and after one dash or a slash too. The tours of phases 4a-4e showed no scrollbar
   because of it. `launch()` refuses the switch in every such form.

# UI direction

Static mockups that fix the look of the 1.0 interface. They are design artefacts, not code:
open them in a browser. Each renders four screens at 1100 × 700 — the lock screen with the token
ceremony's three moments, the archive list, the inside of an archive, and keys & backups — with
the same content, and respects `prefers-color-scheme` (or `data-theme="light|dark"` on the root).

- `native.html` — **the chosen direction for 1.0** (DECISIONS.md 2026-09-06): Enfold as a
  first-party Windows 11 application — mica window ground, a labelled navigation rail, an
  elevated content layer, WinUI-style controls, Segoe UI Variable, a verdigris accent, and the
  touch moment as a full-surface takeover. Every screen and state the app builds is measured
  against this file.
- `two-looks.html` — a feasibility prototype, not a plan: the same four screens from one DOM
  under two looks (`data-skin="native"` or `"workbench"`, press `S` to switch, `T` for the theme).
  It shows that a second look would cost a second token set plus seven structural CSS rules,
  listed in a comment block at the top of its stylesheet. A Workbench look is deferred past 1.0.
- `brief.md` — the brief the mockups were built from: the screens, the fixed content, and the
  product truths a design must respect (retries shown before a PIN is asked, never "0 attempts",
  the touch moment unmistakable, one YubiKey at a time, saving as one atomic step).

The three rejected directions (Instrument, Ledger, Workbench as a primary look) are not kept.

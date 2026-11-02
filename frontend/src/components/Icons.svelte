<!-- The icon sprite of the Native look, once per page; use with <use href="#i-..."/>.
     Option A of the phase-5 study (ruled 2026-11-01): one hand for every glyph, drawn for 16 px on
     a 20-unit grid, so at the user's 125 % one unit is one device pixel and the grid is the
     screen's. One weight, 1.5 (app.css .i); one corner radius, 1.5, and 1 for a part under 6 units;
     round caps and joins; a dot is a filled disc. Every vertical and horizontal stroke is centred on
     a half unit, the middle of a device pixel, so it draws one solid pixel and two light quarters
     rather than the two three-quarter ones a stroke on a pixel's edge draws; a glyph with a stem on
     its axis is centred on 10.5 for the same reason. Where Windows has a metaphor the glyph takes
     it: a gear for Settings, Explorer's Rename, Move to and View, and Edit's pencil. No glyph is
     drawn at 14 px: 1.25 times 14 is 17.5 device pixels, which no grid lands on. i-mark alone is
     not of this hand: internal/brand draws it. -->
<svg width="0" height="0" class="sprite" aria-hidden="true" focusable="false">
  <defs>
    <symbol id="i-archives" viewBox="0 0 20 20"><rect x="2.5" y="3.5" width="15" height="3" rx="1"/><path d="M3.5 6.5V15A1.5 1.5 0 0 0 5 16.5h10a1.5 1.5 0 0 0 1.5-1.5V6.5"/><path d="M8 10.5h4"/></symbol>
    <symbol id="i-keystore" viewBox="0 0 20 20"><circle cx="7" cy="7" r="3.5"/><path d="m9.5 9.5 7 7"/><path d="m14.5 14.5 2-2"/></symbol>
    <!-- Settings: a gear, Windows' own, which the lock screen's header already called it. -->
    <symbol id="i-settings" viewBox="0 0 20 20"><path d="M8.5 4.708 8.5 2.5 11.5 2.5 11.5 4.708 12.681 5.198 14.243 3.636 16.364 5.757 14.802 7.319 15.292 8.5 17.5 8.5 17.5 11.5 15.292 11.5 14.802 12.681 16.364 14.243 14.243 16.364 12.681 14.802 11.5 15.292 11.5 17.5 8.5 17.5 8.5 15.292 7.319 14.802 5.757 16.364 3.636 14.243 5.198 12.681 4.708 11.5 2.5 11.5 2.5 8.5 4.708 8.5 5.198 7.319 3.636 5.757 5.757 3.636 7.319 5.198Z"/><circle cx="10" cy="10" r="2.5"/></symbol>
    <symbol id="i-lock" viewBox="0 0 20 20"><rect x="4.5" y="8.5" width="12" height="8" rx="1.5"/><path d="M7.5 8.5v-2a3 3 0 0 1 6 0v2"/></symbol>
    <symbol id="i-unlock" viewBox="0 0 20 20"><rect x="4.5" y="8.5" width="12" height="8" rx="1.5"/><path d="M7.5 8.5v-2a3 3 0 0 1 5.598-1.5"/></symbol>
    <symbol id="i-chevron" viewBox="0 0 20 20"><path d="m7.5 4.5 5.5 5.5-5.5 5.5"/></symbol>
    <symbol id="i-chevdown" viewBox="0 0 20 20"><path d="m4.5 7.5 5.5 5.5 5.5-5.5"/></symbol>
    <symbol id="i-check" viewBox="0 0 20 20"><circle cx="10.5" cy="10.5" r="7"/><path d="m7.5 10.5 2 2 4-4.5"/></symbol>
    <symbol id="i-info" viewBox="0 0 20 20"><circle cx="10.5" cy="10.5" r="7"/><path d="M10.5 10v3.5"/><circle cx="10.5" cy="7" r="1" fill="currentColor" stroke="none"/></symbol>
    <symbol id="i-warn" viewBox="0 0 20 20"><path d="M10.5 3.5 17.5 16.5h-14Z"/><path d="M10.5 8v3"/><circle cx="10.5" cy="13.75" r="1" fill="currentColor" stroke="none"/></symbol>
    <!-- The danger tier (lib/notices.ts): the circle of i-info with the exclamation of i-warn, in the same strokes. -->
    <symbol id="i-error" viewBox="0 0 20 20"><circle cx="10.5" cy="10.5" r="7"/><path d="M10.5 6.5V11"/><circle cx="10.5" cy="14" r="1" fill="currentColor" stroke="none"/></symbol>
    <symbol id="i-yubi" viewBox="0 0 20 20"><rect x="2.5" y="6.5" width="15" height="8" rx="1.5"/><circle cx="12.5" cy="10.5" r="2"/><path d="M5.5 9.5v2"/></symbol>
    <symbol id="i-touchdot" viewBox="0 0 20 20"><circle cx="10.5" cy="10.5" r="3"/><path d="M10.5 2.5V4M10.5 17v1.5M2.5 10.5H4M17 10.5h1.5"/></symbol>
    <symbol id="i-plus" viewBox="0 0 20 20"><path d="M10.5 4.5v12M4.5 10.5h12"/></symbol>
    <symbol id="i-x" viewBox="0 0 20 20"><path d="m5.5 5.5 9 9M14.5 5.5l-9 9"/></symbol>
    <symbol id="i-open" viewBox="0 0 20 20"><path d="M6.5 7.5V6A1.5 1.5 0 0 1 8 4.5h8A1.5 1.5 0 0 1 17.5 6v9a1.5 1.5 0 0 1-1.5 1.5H8A1.5 1.5 0 0 1 6.5 15v-1.5"/><path d="M3.5 10.5h8"/><path d="m8.5 7.5 3 3-3 3"/></symbol>
    <symbol id="i-folder" viewBox="0 0 20 20"><path d="M2.5 6.5V5A1.5 1.5 0 0 1 4 3.5h3.5l2 3"/><path d="M2.5 6.5H16A1.5 1.5 0 0 1 17.5 8v7a1.5 1.5 0 0 1-1.5 1.5H4A1.5 1.5 0 0 1 2.5 15Z"/></symbol>
    <!-- Create folder: Windows' NewFolder, the folder with a plus at its corner. -->
    <symbol id="i-folder-add" viewBox="0 0 20 20"><path d="M2.5 6.5V5A1.5 1.5 0 0 1 4 3.5h3.5l2 3"/><path d="M17.5 11.5V8A1.5 1.5 0 0 0 16 6.5H2.5V15A1.5 1.5 0 0 0 4 16.5h6.5"/><path d="M15.5 13.5v4M13.5 15.5h4"/></symbol>
    <symbol id="i-extract" viewBox="0 0 20 20"><path d="M10.5 3.5V12"/><path d="m7 8.5 3.5 3.5 3.5-3.5"/><path d="M3.5 13.5v2a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-2"/></symbol>
    <symbol id="i-trash" viewBox="0 0 20 20"><path d="M3.5 5.5h14"/><path d="M8.5 5.5v-2h4v2"/><path d="m5.5 5.5.9 11h8.2l.9-11"/></symbol>
    <symbol id="i-file" viewBox="0 0 20 20"><path d="M11.5 2.5H7A1.5 1.5 0 0 0 5.5 4v12A1.5 1.5 0 0 0 7 17.5h7a1.5 1.5 0 0 0 1.5-1.5V6.5Z"/><path d="M11.5 2.5V5A1.5 1.5 0 0 0 13 6.5h2.5"/></symbol>
    <symbol id="i-image" viewBox="0 0 20 20"><rect x="2.5" y="4.5" width="15" height="12" rx="1.5"/><circle cx="13" cy="8" r="1.5"/><path d="m3.5 15.5 4-4 3.5 3.5 2-2 3.5 3.5"/></symbol>
    <symbol id="i-video" viewBox="0 0 20 20"><rect x="2.5" y="5.5" width="11" height="10" rx="1.5"/><path d="m13.5 9 4-2.5v8l-4-2.5"/></symbol>
    <symbol id="i-audio" viewBox="0 0 20 20"><path d="M8.5 14V5l7-1.5v9"/><circle cx="6.5" cy="14.5" r="2"/><circle cx="13.5" cy="12.5" r="2"/></symbol>
    <symbol id="i-doc" viewBox="0 0 20 20"><path d="M11.5 2.5H7A1.5 1.5 0 0 0 5.5 4v12A1.5 1.5 0 0 0 7 17.5h7a1.5 1.5 0 0 0 1.5-1.5V6.5Z"/><path d="M11.5 2.5V5A1.5 1.5 0 0 0 13 6.5h2.5"/><path d="M8 10.5h5M8 13.5h5"/></symbol>
    <!-- An archive, and nothing else (the rail's open archives, the Archives list, Move to's root). -->
    <symbol id="i-box" viewBox="0 0 20 20"><path d="M10.5 2.5 17.5 6v8l-7 3.5-7-3.5V6Z"/><path d="m3.5 6 7 3.5 7-3.5"/><path d="M10.5 9.5v8"/></symbol>
    <symbol id="i-recovery" viewBox="0 0 20 20"><rect x="2.5" y="4.5" width="15" height="12" rx="1.5"/><path d="M5.5 9.5h3"/><path d="M5.5 12.5h6"/><path d="M13.5 8.5h1"/></symbol>
    <symbol id="i-password" viewBox="0 0 20 20"><rect x="3.5" y="6.5" width="14" height="8" rx="1.5"/><circle cx="6.5" cy="10.5" r="1.25" fill="currentColor" stroke="none"/><circle cx="10.5" cy="10.5" r="1.25" fill="currentColor" stroke="none"/><circle cx="14.5" cy="10.5" r="1.25" fill="currentColor" stroke="none"/></symbol>
    <symbol id="i-backup" viewBox="0 0 20 20"><path d="M11.5 2.5H7A1.5 1.5 0 0 0 5.5 4v12A1.5 1.5 0 0 0 7 17.5h7a1.5 1.5 0 0 0 1.5-1.5V6.5Z"/><path d="M11.5 2.5V5A1.5 1.5 0 0 0 13 6.5h2.5"/><path d="M10.5 9.5v5"/><path d="m8 12 2.5 2.5 2.5-2.5"/></symbol>
    <symbol id="i-rotate" viewBox="0 0 20 20"><path d="M16.5 10.5a6 6 0 1 1-1.757-4.243"/><path d="M16.5 3.5v3h-3"/></symbol>
    <!-- Compact (the Archives bar and an archive's menu): two arrows pressing onto one line, so the box, i-box, is an archive and nothing else; it stood for both. Each arrow runs 5 units under a head 6 wide and 3 deep, its tip on a half unit so that the head's arms cross pixel centres: at 4 units under a head 5 wide, each arrow drew a blob at 16 px and the glyph read as a division sign. -->
    <symbol id="i-compact" viewBox="0 0 20 20"><path d="M3.5 10.5h14"/><path d="M10.5 2.5v5M7.5 4.5l3 3 3-3"/><path d="M10.5 18.5v-5M7.5 16.5l3-3 3 3"/></symbol>
    <!-- Move to… (direction 7): Windows' MoveToFolder, the folder with an arrow at its corner. -->
    <symbol id="i-move" viewBox="0 0 20 20"><path d="M2.5 6.5V5A1.5 1.5 0 0 1 4 3.5h3.5l2 3"/><path d="M17.5 11.5V8A1.5 1.5 0 0 0 16 6.5H2.5V15A1.5 1.5 0 0 0 4 16.5h4.5"/><path d="M11.5 15.5h6.5m-2.5-2.5 2.5 2.5-2.5 2.5"/></symbol>
    <!-- Restore (direction 7, the Forgotten view's row menu): back the way it went. -->
    <symbol id="i-undo" viewBox="0 0 20 20"><path d="M4.5 7.5h8a4 4 0 0 1 0 8h-3"/><path d="m7.5 4.5-3 3 3 3"/></symbol>
    <!-- Paste (ruled 2026-10-26): a clipboard, its clip at the top. -->
    <symbol id="i-paste" viewBox="0 0 20 20"><path d="M7.5 4.5H6A1.5 1.5 0 0 0 4.5 6v9A1.5 1.5 0 0 0 6 16.5h9a1.5 1.5 0 0 0 1.5-1.5V6A1.5 1.5 0 0 0 15 4.5h-1.5"/><rect x="7.5" y="3.5" width="6" height="3" rx="1"/><path d="M7.5 10.5h6M7.5 13.5h4"/></symbol>
    <symbol id="i-copy" viewBox="0 0 20 20"><rect x="7.5" y="3.5" width="9" height="9" rx="1.5"/><path d="M12.5 14.5v.5a1.5 1.5 0 0 1-1.5 1.5H5A1.5 1.5 0 0 1 3.5 15V9A1.5 1.5 0 0 1 5 7.5h.5"/></symbol>
    <!-- Rename (a file's, the file list): Windows' own, a text field with the caret's I-beam through it. -->
    <symbol id="i-rename" viewBox="0 0 20 20"><path d="M11 6.5H4A1.5 1.5 0 0 0 2.5 8v5A1.5 1.5 0 0 0 4 14.5h7"/><path d="M16 6.5A1.5 1.5 0 0 1 17.5 8v5a1.5 1.5 0 0 1-1.5 1.5"/><path d="M13.5 3.5v14M12 3.5h3M12 17.5h3"/><path d="M5.5 10.5h3"/></symbol>
    <!-- Edit (the Archives pane's Edit name and description, F2): Windows' Edit, a pencil, which opens two fields behind it rather than renaming in place; i-rename drew a pencil for both until Rename took Explorer's own glyph. Its ferrule, its nib's two corners and its round end's centre on half units, the middles of device pixels; the nib a point. -->
    <symbol id="i-edit" viewBox="0 0 20 20"><path d="M13 4 4.5 12.5 3 17l4.5-1.5L16 7a2.121 2.121 0 0 0-3-3Z"/><path d="m11.5 5.5 3 3"/></symbol>
    <symbol id="i-eye" viewBox="0 0 20 20"><path d="M3 10.5C4.6 7.6 7.4 5.5 10.5 5.5S16.4 7.6 18 10.5c-1.6 2.9-4.4 5-7.5 5S4.6 13.4 3 10.5Z"/><circle cx="10.5" cy="10.5" r="2"/></symbol>
    <!-- The compact rail's toggle (direction 6): three lines, NavigationView's own glyph. -->
    <symbol id="i-menu" viewBox="0 0 20 20"><path d="M2.5 5.5h15M2.5 10.5h15M2.5 15.5h15"/></symbol>
    <!-- The mark, the folded sheet: internal/brand's Sheet(20), its sides and top fold on whole device pixels at 125 %; brand's sheet_test.go fails when the two differ, and prints this symbol as Sheet draws it. The wash, then the flap, then the lines over both. -->
    <symbol id="i-mark" viewBox="0 0 20 20"><path class="mark-fill" d="M3.2 6.305 9.6 2.61 16 6.305 16 13.695 9.6 17.39 3.2 13.695Z"/><path class="mark-flap" d="M9.6 2.61 16 6.305 9.6 10Z"/><path class="mark-line" d="M3.2 6.305 9.6 2.61 16 6.305 16 13.695 9.6 17.39 3.2 13.695ZM9.6 2.61 9.6 10M3.2 6.305 16 13.695"/></symbol>
    <!-- The toolbar's More and the crumbs' ellipsis (direction 3): three dots, filled, since a stroked dot of this size reads as nothing; each centred on a grid line, so its middle is two by two whole pixels. -->
    <symbol id="i-more" viewBox="0 0 20 20"><circle cx="4" cy="10" r="1.25" fill="currentColor" stroke="none"/><circle cx="10" cy="10" r="1.25" fill="currentColor" stroke="none"/><circle cx="16" cy="10" r="1.25" fill="currentColor" stroke="none"/></symbol>
    <!-- The Archives bar's View (direction 6): Windows' PreviewLink, a window over a list and a pane, not the eye Hide uses. -->
    <symbol id="i-view" viewBox="0 0 20 20"><rect x="2.5" y="3.5" width="15" height="13" rx="1.5"/><path d="M2.5 7.5h15"/><path d="M5.5 10.5h3M5.5 13.5h3"/><rect x="11.5" y="10.5" width="3" height="3" rx="1"/></symbol>
    <!-- A menu's tick beside a setting that is on (View > Preview pane): the check of i-check without its circle. -->
    <symbol id="i-tick" viewBox="0 0 20 20"><path d="m4.5 10.5 3.5 3.5 7.5-7.5"/></symbol>
  </defs>
</svg>

<style>
  .sprite { position: absolute; }
  .mark-fill { fill: var(--accent-wash); stroke: none; }
  .mark-flap { fill: var(--mark-flap); stroke: none; }
  .mark-line { fill: none; stroke: var(--accent); }
</style>

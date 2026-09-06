# Enfold — UI direction brief (shared by every designer)

Enfold is a Windows 11 desktop application: an archive manager whose archives are compressed
and encrypted, with a keystore unlocked by a YubiKey (PIN + touch), a recovery key, or a
password. It is a *manager*: people keep a handful of archives (photo libraries, documents,
backups) and open them often. The UI runs in a WebView2 window (Wails v3); there is no browser
chrome and no native title bar in the mockup — draw the whole window content area. The product's
name is **Enfold**. UI language is English.

The user of this mockup is the product's author, who has not yet chosen a visual style. Your job
is to propose ONE distinctive, fully worked direction so they can compare it with three others.
Content is fixed (below) so that only the design differs between proposals.

## Hard constraints (the file is embedded in a comparison page; violations are rejected)

- Write exactly one file: the path you are given. A complete standalone HTML document.
- Everything inline: CSS in `<style>`, JS (if any) in `<script>`, icons as inline SVG, images
  drawn with CSS/SVG/canvas. The ONLY external resources allowed are Google Fonts stylesheet
  links (`https://fonts.googleapis.com/css2?...&display=swap`); declare fallback stacks. No CDNs,
  no `<img src="http…">`, no fetches, no emoji anywhere (not as icons, not as text).
- Themes, token-level, exactly this structure: `:root { … }` defines the complete light palette
  (a dark-first design defines its dark palette there and swaps consistently);
  `@media (prefers-color-scheme: dark) { :root:not([data-theme="light"]) { …tokens… } }`;
  `:root[data-theme="dark"] { …tokens… }`. Every color in the page comes from a token; `body`
  sets `background` from a token. Both themes must look designed, not inverted.
- Structure: `<body>` contains `<main>` with exactly four `<section class="screen">` elements,
  in this order and with these ids: `unlock`, `archives`, `archive`, `keystore`. Each section is
  a fixed box: `width:1100px; height:700px; overflow:hidden; position:relative;
  box-sizing:border-box`, and represents the whole window content area at that size. Stack
  them vertically with `gap: 48px`, nothing else in `<main>`. No page-level header above them.
- Everything visible at rest: all four screens fully rendered on load, no scrolling inside a
  screen, no state hidden behind interaction. Hover states are welcome; nothing depends on them.
- Keep the file under 160 KB. Respect `prefers-reduced-motion`. Close every element, quote
  every attribute, no cascade collisions.
- Real content only, from below. No lorem ipsum, no placeholder boxes labelled "image".

## The four screens and their content

### 1. `unlock` — the lock screen and the token ceremony

The keystore is locked. Show the ceremony as **three states side by side or stacked, all
visible at once** (a state strip, a storyboard, a stepper — your call), labelled so a reader
knows they are moments in time:

1. **Waiting for the key.** Text: "Insert your YubiKey". Below it, smaller: the vault's name
   "Personal vault" and "Last unlocked yesterday, 21:14". Also show the secondary options as
   quiet links/buttons: "Use recovery key" and "Open a backup". Include the rule for two keys as
   a muted note or a variant state: "Two YubiKeys are inserted — remove one to continue."
2. **PIN.** The key was found and matched a slot: show the slot's label "YubiKey 5C — desk"
   and the PIN field (masked, 6 dots typed), with "3 attempts left" visible *before* typing, and a
   primary button "Unlock". Do not show a number "0" anywhere.
3. **Touch.** PIN accepted; the card is now waiting for a touch. This is the moment the UI must be
   unmistakable: "Touch your YubiKey now" as the dominant element, with a calm secondary line
   "The key will blink until you touch it." The design should make this state look different from
   the others at a glance (that is the whole point of the state).

Optionally a fourth, smaller: **Blocked** — "PIN is blocked. Use your recovery key." (no attempt
counter shown).

### 2. `archives` — the main window after unlock

A list/table of the vault's archives, with a way to open one and to add one. The vault name
"Personal vault" appears; a session indicator shows "Locks in 9:41" (idle timeout) somewhere
quiet. Columns/facts per archive: name, size, files, last modified, key version, and a status
note where given. Rows (exactly these):

| Name | Size | Files | Last modified | Key | Note |
| --- | --- | --- | --- | --- | --- |
| Photos 2024 | 48.2 GB | 12,406 | 2026-09-05 21:14 | v3 | 6% free space — compact when convenient |
| Family videos | 212 GB | 96 | 2026-08-30 19:02 | v1 | stored raw (already compressed) |
| Tax returns | 184 MB | 231 | 2026-08-14 10:37 | v2 | — |
| Project Enfold — notes | 22 MB | 1,180 | 2026-09-05 23:02 | v3 | dictionary on |
| Passport scans | 9.8 MB | 14 | 2026-07-02 08:15 | v2 | — |

Primary actions: "Open", "New archive", and per-row secondary actions "Export volumes…",
"Compact", "Rotate key". One row (Photos 2024) is selected. A quiet banner at the top of the
list: "Key rotation deferred for ‘YubiKey 5 NFC — travel’ — it has not been seen since
2026-09-01." with a "Details" link.

### 3. `archive` — inside an archive (Photos 2024)

Breadcrumb "Photos 2024 › 2024 › 07 Iceland". A file table and a preview pane for the selected
file. Columns: name, size, stored as, modified. Rows:

| Name | Size | Stored | Modified |
| --- | --- | --- | --- |
| IMG_7201.HEIC | 4.1 MB | raw | 2024-07-12 14:02 |
| IMG_7202.HEIC | 3.9 MB | raw | 2024-07-12 14:03 |
| IMG_7203.HEIC | 4.4 MB | raw | 2024-07-12 14:03 |
| DJI_0042.MP4 | 812 MB | raw | 2024-07-12 16:40 |
| trip-notes.md | 12 KB | zstd + dictionary, 71% smaller | 2024-07-14 22:10 |
| itinerary.pdf | 1.2 MB | zstd, 18% smaller | 2024-07-01 09:20 |
| receipts.csv | 48 KB | zstd + dictionary, 84% smaller | 2024-07-15 08:05 |

IMG_7202.HEIC is selected; the preview pane shows an abstract, non-photographic stand-in
(gradient/geometric composition — never a fake photo), with facts: 4,032 × 3,024, 3.9 MB,
"Content verified" (a checkmark-type indicator drawn as SVG), and actions "Extract…", "Open with…".

Transactions are real in this product: changes are staged and committed as one. Show a pending
bar: "3 changes not yet saved" with buttons "Save changes" and "Discard". Toolbar actions:
"Add files", "Add folder", "Extract", "Delete". A quiet stat line: "12,406 files · 48.2 GB ·
key v3 · last saved 2026-09-05 21:14".

### 4. `keystore` — keys, slots and backups

Section "Ways to unlock" listing the slots (exactly these):

- "YubiKey 5C — desk" — hardware key · PIN + touch · added 2026-08-30
- "YubiKey 5 NFC — travel" — hardware key + password · PIN + touch, then a password · added
  2026-08-31 · warning: "Not seen since 2026-09-01 — rotation deferred"
- "Recovery key — printed, in the safe" — recovery key · 48 digits · added 2026-08-30

A rule line under the list: "At least two independent ways in are always kept." Actions:
"Add a key", "Remove", "Rotate now".

Section "Backups": a card/list for a backup file: "backup-2026-09-05.eks — saved 2026-09-05
22:41 — older than this vault (changed 2026-09-06 00:12)" with actions "Export backup" and
"Verify backup". (This date comparison is a feature: the file carries its own date.)

Section "Session": idle lock 10 minutes, absolute lock 1 hour, close to tray: "Lock and free
memory" (chosen) vs "Keep window in memory". Section "Exports": recovery record "3% (default)"
with a control to change it.

## Truths of the product to respect

- Never a "0 attempts" display. Retries are shown before a PIN is asked.
- The touch moment must be unmistakable, distinct from the PIN moment.
- Deleting is cryptographic erasure; saving is one atomic step ("Save changes"). No "sync".
- No cloud, no accounts, no marketing tone. The voice is plain and specific.
- Windows 11 desktop, mouse and keyboard; hit targets and type sizes for a desktop app, not
  a phone (body 13–14 px is normal here, 12 px for data tables is acceptable).

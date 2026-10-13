<!-- The eight-lens polish review of 2026-10-13 (Opus 5.5 panel; each lens verified; synthesised,
then checked for completeness). The companion demo page — the current screenshots with numbered
markers beside before/after mocks — is the artifact https://claude.ai/artifact/PX9sqcChxcgK3T2TNjHgWk ; the
122 screenshots it cites (design-tour/*.png) were captured from the mock backend with headless Edge
and are not committed. Nothing here is ruled: the user picks the directions. -->

# Enfold UI: polish directions

A synthesis of an eight-lens design review (information architecture, craft, motion, copy, native fit, drift from the mockup, trust, flows) of the current build, read against docs/APP.md, docs/DESIGN.md, docs/DECISIONS.md and docs/ui/brief.md. Every finding id below (ia-, craft-, motion-, copy-, native-, drift-, trust-, flows-) is a verified finding from that panel. Screenshot names refer to the design tour (`design-tour/*.png`, 1280x820 unless noted). The tray menu and tooltip were read from `tray_windows.go`, since a browser cannot capture them. The companion page `design-demo.html` shows directions 1 to 10 and the four larger forks against the screenshots, with numbered markers and before/after mocks built from the app's own tokens.

Past rulings were read as context, not as constraints. Where a direction goes against one, it names the ruling and says why it deserves a second look. The ten directions polish the Native look; section (e) steps outside it, with four different paths and a verdict on each.

How to read it: (a) names the roughness; (b) gives ten directions, ranked; (c) lists small exact fixes; (d) says what was considered and kept, and why; (e) lays out the larger forks; (f) is a short dark-theme pass; (g) puts everything in order, with a 1.0 line; (h) maps every captured screen to the place that handles it.

## (a) What the roughness is made of

Nothing in Enfold is broken. The roughness is the sum of many near-misses that never got a system. Visually there is no scale: 15 font sizes (five of them within 2 px of each other), 23 spacing values and four ways to draw a fact. On top of that, the mockup's storyboard devices (three side-by-side ceremony cards, a 252 px fact column) were built literally and then stretched, so screens are almost aligned, and each is either mostly empty or mostly cramped. In behaviour, the controls carry web habits under a Windows coat: fields that always look focused, disabled buttons drawn as translucent colour, a toolbar that wraps, no right-click, and arrow keys that don't select. State changes have no home and no ending either: the ops strip shoves the list down, operations finish in silence, every warning wears the same amber, and the idle countdown ticks all day. In structure, the app follows the core's data model rather than the user's task. A rail click silently closes the open archive, vault upkeep is split across three screens, and one thing often goes by two or three names ("vault" and "Keystore", four kinds of "key"), in copy that explains the mechanism instead of the consequence.

## (b) Directions, ranked

The ranking weighs how much of the felt roughness a direction removes, how often you meet it, and what it costs. Directions 1 to 3 change how every screen reads, 4 to 7 change how daily work feels, and 8 to 10 are about words and where things live. They are not independent: 2's tokens and 8's words come first, 3 and 10 share one rail, and 10's rail head waits for 5's window work. Section (g) gives the order and the release line, and the last column below summarises it (1.0 = before the 1.0 line; 1.0.x = right after; 1.1 = the structure release). Effort: S = an afternoon, M = a few days, L = a week or more.

| # | Direction | Effort | When |
|---|---|---|---|
| 1 | One stage for the unlock | L | 1.0 |
| 2 | A type ramp and a 4 px grid | M | 1.0, first |
| 3 | The open archive is a place | M | S parts 1.0; the rail child 1.1 |
| 4 | Status gets a home and an ending | M | The countdown and the preview's loading state 1.0; the foot and the done line 1.0.x; InfoBars 1.1 |
| 5 | Windows controls, not web controls | M | The CSS states 1.0; the window later |
| 6 | The Archives page at its real size | M | Two S parts 1.0; the rest 1.1 |
| 7 | Explorer's habits: keys, right-click, Enter, one Extract | M (Find: L) | Dialogs as forms and the Extract default 1.0; keys and conflicts 1.0.x; menus, viewer, Move to 1.1 |
| 8 | One word per thing, consequences first | M | The glossary and verbs 1.0, first; the copy pass with each screen |
| 9 | Rank the warnings; give safety its receipts | M | 1.0 |
| 10 | Vault things in one place | M | The tray line 1.0; the rest 1.1, with 3's rail |

---

### 1. One stage for the unlock — effort L

**Places.**
- The lock screen at rest and every ceremony moment: 01-lock-rest, 02-ceremony-insert, 03-ceremony-pin-and-password, 04-ceremony-touch, 06-ceremony-pin-refused, 07-ceremony-pin-last-attempt, 09-ceremony-password-refused, 10-ceremony-deriving, 11-ceremony-done, 15-ceremony-no-match, 16-ceremony-failed, 22-lock-busy, 28-lock-setup-needed.
- The recovery key's entry: 12-ceremony-recovery, 13-ceremony-recovery-typed, 14-ceremony-recovery-refused.
- The edge states of APP.md §6: 17-ceremony-two-keys, 18-lock-deadline-note, 20-lock-pending-touch, and the ones the tour could not stage (Blocked, `token.no_service`, Broken).
- A vault whose only everyday way in is a password. Shot 21 is not it: it shows a YubiKey beside a standalone password, which keystore.ErrPolicy forbids. The real case has no YubiKey at all.
- First run: 23-firstrun-fresh, 24-firstrun-create-dialog, 25-firstrun-import-dialog, 26-firstrun-absent, 27-firstrun-damaged, and the lock screen's import dialog, 29.
- The ceremony inside dialogs: 98/99/99b (Add a key) and 45 (Import records).

**What is wrong now.**
- The screen you see most often is the mockup's storyboard, built literally. There are three 383x730 px cards, and two of them are dimmed to 0.55 with one grey line each. The live content fills the top 370 px of its card, so two-thirds of the window is placeholder.
- The rest state borrows the ceremony's furniture. It shows "1 WAITING FOR THE KEY" while nothing waits, and "Insert your YubiKey" above a button you must click first. After the click (02) almost nothing changes, and nothing moves to show the app is listening.
- Done steps look like future steps: both are dim and wear the same number disc. After the PIN is accepted, card 2 falls back to its placeholder. In the password-after-touch order (09), card 3 still says "The key waits for a touch" after the touch.
- The touch was ruled a full-surface takeover, but it fills one third of the window. Deriving flips that card from green to white and drops the lead text by about 220 px, and Done flips it back. Both are hard cuts, because a gradient background cannot transition.
- A touch timeout is reported in card 1, 800 px from where you were looking, in red, under "Could not continue".
- On card 2, the label says "PASSWORD AND PIN" but the fields run PIN then password. Three helper lines surround the field, and "Wrong PIN." lands between them. That pushes the password field and Continue down 28 px, and they jump back up at the next keystroke. "1 attempt left — the last one" is the same grey as "3 attempts left". The button reads "Unlock" on the PIN-only card, although a touch still follows. Cancel is an underlined link 300 px below Continue.
- Inside dialogs, the ceremony prints its title twice and has no step line. The touch sub-line is #55565d on green (1.2:1), because `.dialog p` outranks `.touch-sub`.
- **Recovery key entry (12-14).** The eight cells already advance on their own and take a pasted key (APP.md §6), but nothing shows it: the screen offers no hint that paste works and no sign that a finished group is right. A mistyped group turns its whole cell pink, and the error line explains the checksum ("the last digit checks the other five") without saying which group to fix. "Unlock" is a full-width slab, Cancel is an underlined link 420 px further down, and card 1 is left as a faded "Personal vault" with nothing else in it.
- **The edge states.** Each one borrows the rest card's heading, so the screen says "Insert your YubiKey" above a note that the five minutes ran out (18), or above a key that is still blinking from a cancelled touch (20). Two keys inserted (17) puts a grey bar 450 px below its heading. A blocked PIN, a missing Smart Card service and a broken vault each have copy (strings.ts) but no designed place.
- **First run (23-27).** It is a 520 px card stretched to 730 px under a one-step "1 START" line, with about 440 px of white under four lines of content. The create dialog asks for the "First way in" from a select, with the recovery key mentioned only in a right-aligned hint and an invented name ("YubiKey 5C — desk") as the placeholder. The import dialog opens on an empty path field and a disabled Import, although nothing can happen until a file is chosen. On a damaged vault (27) the best remedy, a replaced copy Enfold kept, sits in a grey bar at the very bottom, 400 px below "Try again".
- **"Finish setting up" (28)** keeps the strip "WAITING FOR THE KEY · PIN · TOUCH", which does not describe that flow.

**Proposed change.**
- **Layout.** One centred panel, `width: min(440px, 100% - 32px)`, its top at about 14vh, 28 px padding, radius 8, a flat 1 px stroke. The state word sits top-right and reads Locked, Unlocking, Not unlocked, Open elsewhere, Damaged or No vault yet; beside it, a gear opens Settings, which needs no vault (see 10). The brand stays top-left until direction 5 gives the window a caption that names the app; then the stage drops it, as the rail does.
- **Notices** (tampered, lock detection unavailable, an archive still open) sit above the panel, full panel width, in the tiers of direction 9. Nothing warns from below the buttons any more.
- **Rest (no step line).**
  - Content: a 20 px padlock, the vault name in 20/28, and one caption line, "Locked · last unlocked yesterday, 03:24". The date is relative.
  - Primary "Unlock with YubiKey". It is focused on mount, so Enter starts the ceremony.
  - Start the ceremony at once when the lock screen was reached by an explicit Unlock: from the banner, the foot, the tray, or a pending Explorer open. Never start it after an idle lock or a Windows lock.
  - Secondary "Unlock with recovery key" as a standard button.
  - One quiet subtle button, "Backups and copies…", opens a small chooser with "Test that a backup opens…" and "Replace this vault with a copy…", the verbs of direction 8 (see 10).
- **A password-only vault.** The lead reads "Unlock Personal vault" and the primary is "Unlock with password". Pressing it, or Enter, swaps the panel to the field in place. It is one step, so there is no step line, and the field keeps the 2026-10-13 form rules. Starting that ceremony on mount would save a keypress, but a running ceremony makes Import and the backup test refuse until it is cancelled, so it is not proposed.
- **Step line.** Shown only while a ceremony runs, as caption 12/16 above the panel.
  - Labels: "Key · PIN · Touch" for a YubiKey and "Recovery key · Unlock" for recovery. The setup, import and check flows get no step line.
  - A done step gets a 16 px accent disc with a white check, and its name in ink-2. The live step is ink at weight 600. Pending steps are ink-3.
  - The panel keeps one receipt line per done step: "YubiKey 5C — desk found", "PIN accepted".
- **Insert.** The key art stays.
  - While the app listens, the key's LED dot breathes (opacity 0.35 to 1, 1.6 s, ease-in-out, looping). When the key arrives, the dot holds solid accent for 200 ms, then the content cross-fades (140 ms) to the PIN step.
  - Under reduced motion the art is static, and the lead reads "Waiting for your YubiKey…".
- **PIN (and password).**
  - When the two fields are merged, the label is "PIN and password", in field order.
  - Each field gets one reserved 16 px line under it, where hint and error cross-fade in place, so nothing reflows.
  - Retries sit in the label row, in ink-2 at 3, --attention at 2 and --danger at 1. At 1, add an attention bar above the field: "Last try. A wrong PIN now blocks this YubiKey's PIN; then only the recovery key opens the vault. [Use recovery key]".
  - The primary button always reads "Continue", because a touch follows. "Cancel" becomes a standard button beside it. "Use recovery key instead" is the one secondary action below them, drawn as a subtle button with no underline, because it is a command (direction 5's rule for link-style buttons).
  - Keep the "Up to 8 characters." rule line (the 2026-09-07 form rule) and drop "The key counts wrong tries, not Enfold."
- **Touch.**
  - The whole lock stage behind the panel eases to the --touch-bg gradient. Fade an overlay's opacity over 220 ms ease-enter instead of animating the gradient. The panel's border goes transparent, so the panel dissolves into the green.
  - Rings are 160 px and centred. The lead is 28/36.
  - Sub-line: "Touch it while it blinks. If it stops blinking, start again." (no number, per DESIGN trap 23). Bottom receipt: "PIN accepted · YubiKey 5C — desk".
- **Deriving and Releasing.**
  - The stage stays green: keep `.touch` through StepDeriving and StepReleasing. The rings stop pulsing and hold at 50% opacity.
  - The lead cross-fades to "Opening the vault…" and the receipt becomes "Touched · YubiKey 5C — desk". This applies to a YubiKey unlock only; a password or recovery unlock derives on the neutral panel.
- **Done.** As now: the check disc pops (240 ms ease-pop), holds 320 ms, and the shell arrives.
- **Failure where it happened.**
  - A touch timeout, after the core's two rounds, stops the rings and returns the stage to neutral. It says "The key stopped waiting." with [Start again] in neutral ink. Red is kept for real refusals.
  - The state word becomes "Not unlocked".
  - No-match gets a real [Use recovery key] button beside Cancel, under "This key isn't one of Personal vault's unlock methods. Try another YubiKey, or use your recovery key." The key art stays, with the LED solid ink-3, so the screen shows which object is wrong.
- **The recovery key** (step line "Recovery key · Unlock").
  - The panel heads with the vault name and one line: "Type or paste the 48 digits from the sheet. The key ID at the top of the sheet says which one it is." Under it, the vault's recovery keys as a quiet list, ID first: "0303-0303 · printed, in the safe" and "0404-0404 · the first one".
  - Eight cells in two rows of four, 20 px Cascadia Mono, about 90 px wide in the 440 px panel. They keep today's behaviour: each moves on after six digits, a paste fills all eight with the dashes stripped, and they are one tab stop.
  - A finished group that passes the check digit shows a small accent tick in its top-right corner. A group that fails gets a 2 px danger underline (not a pink frame), and the line under the cells names it: "Group 3 doesn't check out. Compare its six digits with the sheet."
  - Below the cells, "5 of 8 groups" in caption ink-2 while typing.
  - [Unlock] at natural width, accent, with [Cancel] beside it. Unlock is the right word here, because nothing follows but the derivation.
  - A refused key keeps the digits for correction (as now) and says: "These digits don't open Personal vault. If you have more than one sheet, check its key ID."
- **The edge states, each in its own place.** The step line stays on the step where the state arises, and the rest-card heading is never borrowed.
  - Two keys (17), step 1: the key art shows two keys, one fading. "Two YubiKeys are in. Remove one; Enfold carries on by itself." There is no bar, because the core polls and continues on its own.
  - `token.no_service`, step 1 after ten seconds: the LED stops breathing, and an attention line in the panel says "The Smart Card service isn't running. Windows starts it when a YubiKey goes in; if it stays off, start the Smart Card service."
  - Blocked, step 2: a danger bar, "This YubiKey's PIN is blocked. It won't accept a PIN until it is reset; the recovery key still opens the vault.", with [Use recovery key] as the accent and [Cancel].
  - The deadline note (18), at rest: an info line inside the panel, under the vault name. "The last unlock stopped: the password wasn't given within 5 minutes of the touch. Start again from the key." The primary is unchanged.
  - A pending touch (20), at rest: the key art's LED keeps breathing, because the key really is still waiting. The primary reads "Continue unlocking", since pressing it adopts the touch (APP.md §2.2). One info line under it: "The key is still blinking from the unlock you cancelled. Continue, or touch it or pull it out to end it."
  - Busy (22): the lead reads "Personal vault is open in another Enfold", with the line "Close it there, then try again." and [Try again]. The state word is "Open elsewhere".
  - Broken: an attention line, "A save's outcome is unknown. Reopen the vault to see which version was kept.", with [Reopen the vault] as the accent.
  - The workstation-lock warning is an attention notice above the panel (direction 9). The BitLocker warnings need no place: the check was struck on 2026-09-13 (APP.md §12, SCOPE). If it returns, it takes the same attention slot.
- **First run and setup** use the same panel at the same place, with no step line. The flows have no fixed steps, and a one-step line says nothing.
  - **Fresh (23).** Heading "Create your vault" (20/28), then the shot's own good sentence: "A vault is one file: the keys to your archives, opened by a YubiKey, a password or a recovery key." Primary [Create a vault…]. Secondary [Restore from a vault or backup…]. Then the subtle "Test that a backup opens…". The panel is sized to its content. "Have the recovery key ready" moves into the restore dialog, where it applies.
  - **The create dialog (24).** The first way in becomes two radio cards, not a select: "YubiKey: PIN and touch (recommended)" and "Password: at least 8 characters". A fixed third row, not a choice, reads "Recovery key: always made. You'll print it or save it right after." The tick "Also require a password with every YubiKey" moves inside the YubiKey card, the only place it applies. "Name this key" shows "Named after its serial number" as its placeholder. The path stays a caption, with "Keep it elsewhere…". The button is [Continue], and the line above it says what follows: "Next: insert the key, enter its PIN and touch it. Then your recovery key is shown once."
  - **Restore (25 and 29).** The dialog opens on [Choose a file…] as the accent, with no empty path field, and accepts a file dropped on it. After the pick it shows what it already shows today: the kind, the date, the ways in, and the consequence.
  - **Absent (26).** "Personal vault isn't where Enfold left it", the path in caption mono, then [Try again], [Find it…], [Restore from a vault or backup…], and last the subtle "Create a new vault instead". [Find it…] opens a picker and then the import dialog with "Use the file where it is" already chosen, an existing route. When the path's drive is not mounted, a core check of the drive root can tell, and the line says "Drive D: isn't connected."
  - **Damaged (27).** The kept copy comes first, as an attention notice inside the panel: "Enfold kept a copy of this vault from 2026-10-01. [Use that copy]". Then [Try again] and [Restore from another file…]. "Rebuild the vault…" comes last, in danger ink, with its consequence on its own line: "A new, empty vault. Archives locked to this one open only with the damaged file."
  - **Finish setting up (28).** No step line. The lead is "Finish setting up Personal vault", then "It has only its recovery key so far. Add the way you'll unlock it every day; the recovery key is asked first." Primary [Finish setting up…], secondary [Unlock with recovery key].
- **In dialogs** (Add a key, Import records).
  - The same panel internals, at min-height 380 px, so steps swap without the box jumping. No second copy of the title.
  - Enrolment shows a two-part line, "Prove it's you · Add the new key". Its copy runs "Insert a key that already unlocks this vault", then "Now insert the new key".
- **Docs.** Rewrite APP.md §6 Motion ("the lock screen's three cards", "the third card stays the touch's green") and the "card" wording of DECISIONS 2026-09-07/08 to the one-panel model.

**Absorbs.** ia-12, craft-4, native-13, drift-1, drift-6, drift-11, drift-12, trust-2, trust-6, trust-10, trust-11, trust-12, motion-3, motion-6, motion-8, motion-10, copy-4 (ceremony buttons), copy-5, copy-6, copy-9, copy-11, flows-13, craft-2. Also the screens no lens took up: recovery entry, the edge states and first run (12-14, 17, 18, 20, 23-29).

**Rulings.**
- This restores DECISIONS 2026-09-06: "the ceremony is one panel changing in place … the touch moment is a full-surface takeover".
- It keeps APP.md §6's recovery cells (plain digits, auto-advance, paste, one tab stop, the check digit judged per group) and only draws them. It keeps the lock screen's precedence (Busy, then Broken, then setup needed, then the ways in) and makes "Tampered is a banner over any" literal, above the panel.
- It goes against the shipped build, and against several documents:
  - docs/ui/brief.md's "three states side by side", which was a comparison device;
  - native.html:782, where the rest card says "Insert your YubiKey";
  - the APP.md §6 Motion paragraph, and the 2026-09-07/08 entries that describe cards.
- It challenges APP.md §6 "nothing loops but the touch rings", for the LED breathe; here the motion is the state.
- It challenges APP.md:1204-1205's label "Password and PIN"; the label should follow the field order that the same paragraph rules.
- Core needs:
  - The relative last-unlock date is already known.
  - "with YubiKey 5C — desk" on the rest panel would need the core to keep the last slot label in memory.
  - An archive count cannot be shown while locked, because it is not plaintext.

---

### 2. A type ramp and a 4 px grid — effort M

**Places.** Every screen. It is worst in the Archives panel (32-archives-panel-expanded), Settings (100-settings), Keystore's This vault card (90-keys), the dialogs (40, 42, 71, 99c) and the page heads (30, 60).

**What is wrong now.**
- There are 15 font sizes, from 10.5 to 25 px. Body text comes in five sizes 0.5 px apart (11, 11.5, 12, 12.5, 13), and headings in eight sizes 1 to 2 px apart (14, 15, 16, 17, 19, 20, 21, 25). Line height is a ratio (1.45), so line boxes come out fractional: 18.85, 18.125, 16.675.
- Secondary text is --ink-3 at 11 to 11.5 px. That gives 3.3 to 3.7:1 contrast, below both 4.5:1 and Windows' 12 px minimum.
- There are 23 spacing values: card insets run from 10 to 22 px and icon gaps from 5 to 12.
- Facts are drawn four different ways, and the emphasis flips between them. In the This vault card the label is bold and the value faint.
- Eyebrows are uppercase and tracked ("SELECTED", "WAITING FOR THE KEY"), and prose uses italics.
- The head row centres items of different heights, so baselines miss by about 4 px. The Archive page head is also 4 px taller than the Archives head, so everything jumps when you enter an archive.

**Proposed change.**
- **Six type tokens, and nothing else.**
  - --t-cap: 400 12px/16px.
  - --t-body: 400 13px/20px.
  - --t-strong: 600 13px/20px.
  - --t-sect: 600 16px/22px Display.
  - --t-sub: 600 20px/28px Display, for page and dialog titles.
  - --t-hero: 600 28px/36px Display, for the touch lead only.
  - Delete 10.5, 11, 11.5, 12.5, 14, 15, 17, 19, 21 and 25. Windows' own Body is 14/20; if 13 reads small after the change, that is one token to move.
- **Mapping.**
  - Names in lists: body.
  - Other cells, table headers, hints, the foot and chips: caption.
  - Field labels: strong. Section headings: sect.
  - Mono values: 0.92em in --ink-2, which stops Cascadia from reading bold.
- **Colour.** Secondary text uses --ink-2 (7.3:1). Keep --ink-3 for placeholders, disabled text and the "—" cell only.
- **Spacing tokens.** --s1 to --s6 are 4, 8, 12, 16, 20 and 24.
  - Card inset: 16 (12 for strips: .op, .bar).
  - Icon-to-label gap: 8 (12 beside a 20 px icon).
  - Layer gutter: 24.
  - Rail padding 12px 8px and brand padding 8px 12px, so the mark and the item icons share one centre line.
- **One `<dl class="kv">`.**
  - Inline form: `grid-template-columns: fit-content(120px) minmax(0,1fr); gap: 6px 16px`. dt is caption in ink-2, dd is body in ink, and values sit left, next to their labels.
  - Stacked form: label over value, for paths and hashes.
  - Use it in the side pane, the preview facts, the panel, the dialogs (replacing the 148 px column), This vault and About. Never put a value more than about 200 px from its label.
- **Head.**
  - `align-items: baseline; min-height: 59px` on every page.
  - `.head-name` gets `padding: 0 6px` with a -6 px margin.
  - The archive count becomes caption text like the meta line ("3 archives · 260 GB · vault file 145 KB · changed …"). Pills are for state only.
- **Case and style.** Sentence case everywhere ("Waiting for the key", "Selected"), no letter-spacing, no italics. A command named in prose is quoted in its own words.

**Absorbs.** craft-1, craft-3, craft-9, craft-10, craft-12, craft-15, native-12, native-18, drift-9.

**Rulings.**
- This goes against the sizes inherited from docs/ui/native.html, where 12.5 and 11.5 dominate, and partly against brief.md:131 ("12 px for data tables"). The 12 px survives as caption.
- The argument is the aim of 2026-09-06 itself, a first-party Windows 11 feel: the Windows type ramp has no half-pixel steps.
- The fixed heights stay: the 40 px locked line, the 44/64 panel and the 36 px file rows. Only insets and gaps snap to the grid. The 42 px ops strip is the one fixed height that goes, and direction 4 retires it, not this one: running work moves into the 34 px foot.

---

### 3. The open archive is a place — effort M

**Places.**
- The Archive page head and the rail: 60-archive-root, 64-archive-subfolder, 65-archive-nested-folder, 82-archive-locked-banner, 99d-keys-locked.
- The preview pane at rest: 60, 63, 65; with a file it cannot preview: 62.
- An empty folder: 66. The Add menu: 67. The page at 900 px: 83, 84.
- Archives after unlock: 30.
- The Explorer double-click paths: 51, 44-46.
- The empty Archives page: 37.

**What is wrong now.**
- **The rail closes the archive.** Inside an archive, the rail draws "Archives" as the current page. Yet any rail click closes the archive, even a glance at Settings to check the idle timeout. Its keys are destroyed, and its folder, sort and selection are lost.
- **Worse while locked.** The rail click drops the last open archive and with it the whole shell. The archive cannot be reopened until the next unlock.
- **No location.** Two levels down, nothing names the folder. The head says "Photos 2024", the list starts with "..", and the foot gives archive-wide totals.
- **Two exits.** The way back is the bare word "Archives" beside the title. A second exit, "Close archive", sits in the opposite corner. It is a kill switch with different semantics, explained only in a tooltip.
- **Mixed scopes.** The vault-level "Delete archive…" shares the toolbar with the file-level "Delete".
- **An empty pane.** The preview pane's resting state, on arrival and after every folder change, is a grey "Select a file" box over a disabled full-width accent slab. With a PDF or a HEIC selected (62), the box says "No preview for this type — extract it", which offers writing plaintext to disk as the remedy for a missing preview.
- **An empty folder (66)** shows ".." and "This folder is empty." in the middle of 600 px of white, with no way to fill it from there.
- **The Add menu (67)** mixes two pickers with a command that makes a folder in the archive, without ellipses or shortcuts: "Add files · Add folder · Create folder".
- **At 900 px (83, 84)** the list drops Type and Modified while the rail keeps its 212 px and the pane its 264 px.
- **No memory.** Every session starts from zero; the app does not remember the last archive.
- **Explorer opens get lost.** A double-click on an .efd while locked shows the generic lock screen. The key-not-in-vault path ends after the merge without opening the file. There is no "Open file…" command.

**Proposed change.**
- **Rail child.**
  - While an archive is open, an indented row sits under Archives: the archive icon, its name, and a 20 px × that runs the graceful Leave. The row carries the current pill while the archive page is shown.
  - Switching to Vault or Settings keeps the page mounted, as a lock already does. The child row brings you back to the same folder, sort and selection.
  - Clicking "Archives" shows the list and keeps the archive open.
  - The archive closes by its ×, by Close in the head, by closing the window or quitting, or by opening another archive.
  - The rail is shared with direction 10 (the vault head) and direction 4 (the op dot), and its compact form is defined once, in direction 6.
  - Interim for 1.0, before the rail is rebuilt: make the leave visible. Rail items inside an archive carry the hover text "Leaves Photos 2024". While locked, the items that would close the last open archive are greyed, with that reason.
- **Head.**
  - The title becomes crumbs: "Photos 2024 › 2024 › Trips" in 20/28. Ancestors are --ink-2 with a hover fill, the current folder is --ink, and the 12 px chevrons are --ink-3.
  - When space runs out, the middle collapses to "…", which opens a menu.
  - Each crumb is a button and a Move drop target, which brings back drop-onto-grandparent.
  - On the right, one [Close] standard button runs the graceful leave. The kill switch becomes "Force close", shown only while a preview body is draining.
- **Toolbar.** [+ Add ▾] [Extract…] [Rename] [Delete…] … [⋯]. "Delete archive…" leaves this page for the Archives row menu (see 6 and 7).
  - The Add menu reads "Add files… · Add a folder… · ─ · New folder Ctrl+Shift+N". The ellipses mark the two pickers, the separator sets apart the command that changes the archive's own tree, and the shortcut teaches the key (direction 7).
- **At 900 px.** The compact rail (direction 6) gives back 164 px, which brings Modified back. View ▾ › Preview pane (Alt+P, as in Explorer) hides the pane and gives the list the whole width; the choice is remembered.
- **The pane at rest describes where you are.**
  - It shows the folder name (16/22), then "3 items · 9.3 KB".
  - Then the archive's facts as a kv list: Size 48 GB · Last saved 2026-10-13 01:34 · Unused 2.9 GB (Compact to reclaim) · Compression Normal.
  - Then a standard [Extract all…].
- **The pane with a selection.**
  - With several items selected it shows "4 items · 830 MB" and an accent [Extract 4 items…].
  - The art box becomes flexible (`flex: 1 1 auto; min-height: 186px`), so text and images use the pane's height.
  - A file with no preview shows its type icon at 48 px and "No preview for PDF documents", then the facts and a standard [Extract…]. Extracting is offered as an action, not as the cure for a missing preview.
  - The foot keeps only "Trips · 3 items" and the lock state.
- **An empty folder** says "Trips is empty", with [Add files…] and [Add a folder…] under it and "or drop files here" in caption ink-2. The drop target is the whole list, as today.
- **Getting there.**
  - Remember the last opened archive per vault, in memory, for the life of the process. The tray keeps the process alive between windows, so this covers the daily path. Do not write it to settings.json: names are encrypted registry data, and even the "opaque" id is not harmless on disk. The envelope's archive_id can be read without any key, so a stored id points at one specific .efd file, and a plaintext settings file would then record which archive was used last.
  - After unlock, select and focus that row so Enter opens it. The foot hints "Enter opens Photos 2024".
  - Add "Open file…" (Ctrl+O) to the Archives bar: Shell.PickFiles for *.efd, then store.openPath. An .efd dropped on the list goes to openPath instead of an error toast.
  - A locked Explorer open shows "Unlock to open photos-2024.efd ×" on the rest panel. store.openWhenUnlocked already holds the path.
  - After an Import-records merge, open the pending file automatically. In the merge list, pin its record at the top, ticked and tagged "the file you opened". This needs the core to return the envelope's archive_id with archive.key_not_in_vault.

**Absorbs.** ia-1, ia-8, ia-9, ia-10, ia-13, native-14, drift-4, drift-5, flows-5, flows-6, flows-7, flows-16. Also the empty folder (66), no preview (62), the Add menu (67) and the narrow page (83, 84), which no lens took up.

**Rulings.**
- **APP.md §2.3 / DECISIONS 2026-09-10, "No timeout of the archive's own … leaving the page closes it".**
  - That ruling removed a timer that cut work off. A glance at Settings is the same kind of cut.
  - The rail child keeps the ruling's spirit: the archive is closed by an explicit act or by closing the window, never by a timer or a side effect. It also names the open archive on every page.
  - That gives DESIGN §2/§10's conceded residual (an unattended desktop keeps the page mounted) a visible signal.
- **DECISIONS 2026-09-10, "The heading is the archive's name alone".** This was your own fourth-pass call. The ".." row shows that a parent exists, not where you are, and store.page.crumbs already carries the chain.
- **Also:** APP.md §13 (Delete archive… on the Archive page) and the 2026-09-10 visible kill switch.

---

### 4. Status gets a home and an ending — effort M

**Places.**
- The ops strip: 47-archives-op-verify, 68-archive-op-add, 72-archive-op-extract, 80-archive-op-reclaim, and the drag-out shots 86-88.
- Banners: 82, 89b.
- The foot countdown, on every unlocked page.
- Toasts: 48, 49, 81. The results dialog: 79.
- The preview pane while loading, the dialogs' busy labels, and Open from the list.

**What is wrong now.**
- **The list jumps.** The ops strip and the banners insert themselves above the list with no transition. The list drops 54 px when an op starts and jumps back when it ends. During a drag-out the rows shift under the cursor while you aim at a folder row.
- **Silent endings.**
  - A clean Extract, Verify, Compact, Rotate or Export ends with the strip vanishing: no result, no "Show in Explorer".
  - A Verify leaves no trace at all.
  - A 48 GB extract started before switching to Keystore is invisible from there.
  - Your own Cancel ends in a red error toast.
- **The countdown.** "Locks in m:ss" ticks every second and jumps back on input. It looks the same at 14:30 as at 0:05, and the absolute cap arrives with no warning.
- **No sign of waiting.** Opening an archive and Check files show nothing while they work. The dialogs use five different busy words (Working…, Writing…, Saving…, reading…, Loading…).
- **Previews.** While a preview loads, the pane says "No preview for this type — extract it".
- **Toasts.** Error toasts vanish after 9 s, can't be closed, cover the page title and carry no icon.
- **No list motion.** Folder changes swap instantly, and rows an op added appear unmarked.
- **The results dialog (79)** is titled "What happened" and opens on a count ("2 files added · 2 failed"), then one line per item in the "name — reason" form. It offers no way to copy the list, nothing to do about a failed item, and Close is its accent. (The raw "Error: file.read" in the shot is a crafted code; only the fallback behind it ships.)

**Proposed change.**
- **The foot becomes the status bar.** It is 34 px and always present, so an operation never moves the list.
  - The left slot cycles: page note at rest, then the running op, then a done line, then back. Changes cross-fade in 140 ms.
  - Running: "<verb> <archive>" (archive named), a 160 px progress line 3 px high, "9.4 MB of 28 MB · 2 min left", and a subtle, 24 px [Cancel] button. It is a command, so it is a button, not a link (direction 5). Pressed, it turns into a disabled "Cancelling…".
  - With two or more ops, the latest shows with "+1", which opens a flyout above the foot. The flyout lists each op on its own row: the archive, the verb, its own progress line and its own Cancel, with "Runs to the end" in place of Cancel for Compact and Re-key.
  - This retires the 42 px ops strip above the list (see Rulings).
  - The drag-out hover reads "Dragging 4 items", not "Extracting".
- **A done line**, for 6 s or until the next click in the list: an accent check, a sentence and one action.
  - "Extracted 15 files to D:\Archives\Photos 2024 [Show in Explorer]". Shell.Reveal exists, and OpView already carries the destination.
  - "Compacted Photos 2024: 2.9 GB given back".
  - "Added 2 files to 2024/Trips".
  - "Added 2 of 4 files [Details]". Details opens the results dialog, rewritten: its title is the outcome ("2 files weren't added"), each row reads "thumbs.db: Windows wouldn't let Enfold read it" with [Show in Explorer] for the source, the buttons are [Copy details] and [Close] at equal weight, and the unknown-code fallback of small fix 20 applies.
  - "Backup saved to E:\backup-2026-10-13.eks [Show in Explorer]".
  - A cancel, in neutral ink: "Stopped. 6 of 15 files were written to … [Show in Explorer]".
  - The line leaves in 100 ms. Finished ops already stay in store.ops for 8 s, so this mostly means widening the runningOps filter.
- **While anything runs.** The Archives rail item carries an 8 px accent dot at its right edge; in the compact rail, the dot sits on the icon's top-right corner. The dot becomes a 14 px conic ring at the op's percentage when only one op runs. A completion that happens on another page arrives as an info InfoBar with the same line.
- **One notice slot above the list, with a fixed order.** It sits between the toolbar and the list on every page.
  - Banners are stateful: tampered, the vault locked with an archive open, needs reopening, copy mismatch. They stay in flow, because they change what the page can do, and they open and close their height (grid-template-rows 0fr→1fr, 220 ms ease-enter), so the list slides instead of jumping.
  - Their order is fixed, top to bottom: danger (tampered), then attention (locked with an archive open, needs reopening, copy mismatch), then info. A new banner takes its tier's place and never pushes in at the top.
  - InfoBars (what toasts were) are transient. They overlay the top of the list under the banners, with --shadow-pop, and never push it. They stack newest first, at most three.
  - So the list moves only when the page's state changes, and then smoothly. An operation, a toast or a countdown never moves it.
- **A quiet countdown.** It shows the nearer of the two deadlines, as ruled 2026-09-12, and changes form, not just its number.
  - Idle nearer: static text, "Locks after 10 min idle", for as long as more than 60 s remain. Any input resets the timer, so a ticking number would only be noise. Between 2:00 and 1:00 nothing changes.
  - Idle, last 60 s: the chip turns --attention, counts m:ss, and offers [Stay unlocked], which sends Activity.
  - Absolute cap nearer: static clock time, "Session ends 16:42", which never needs to tick. At 2:00 left, an attention InfoBar appears once: "The session ends at 16:42; the key will be asked again." In the last 60 s the chip counts m:ss in --attention, with no [Stay unlocked], since nothing can extend it.
  - The too-slow refusal gives numbers and the real way out: "This needs about 14 min; the session ends in 6 (the 1-hour limit). Lock and unlock to start a fresh hour, then retry."
- **One pending primitive.**
  - After 300 ms of waiting, the triggering control shows a 14 px ring (2 px, --accent, 1 s linear) and gets aria-busy.
  - The label stays unless a verb adds information. Use one verb set: Opening…, Deleting…, Merging…, Saving….
  - Open from the list puts the ring in the row's status cell and ignores repeat double-clicks.
  - Check files ends with a line: "Checked 3 archives · all files present".
- **Preview loading.**
  - Set `loading = kind !== 'none' && !previewUrl && !previewText`. While loading, the art box keeps its size as a surface-alt block and shows a ring after 300 ms.
  - The image fades in over 140 ms on its load event.
  - Arrow-key browsing keeps the previous preview at 0.4 opacity until the next is ready.
  - A failed call shows an error state in the pane.
- **Toasts become InfoBars.**
  - They sit in the notice slot, overlaying the top of the list, with a severity icon, left-aligned text and ×.
  - Info and success dismiss themselves after 3 s plus 50 ms per character, at most 8 s, and pause on hover or while the window is unfocused.
  - Errors stay until dismissed. At most 3 show at once, and Esc closes the top one.
- **Cancellable work.** Verify and Extract become cancellable. Verify's hashFile already checks ctx; stopping an extract between files needs core work. The Compact and Re-key confirms say "runs to the end once started".
- **List motion.** Animate only the tbody's opacity and transform.
  - On a folder change, the tbody fades in from x ±8 px over 140 ms; the header and toolbar stay still.
  - Rows an op added get a 1.2 s accent wash that fades out.
  - Removed rows fade out over 100 ms.
- **Standing notices** (the key may still be PIN-verified, lock detection unavailable, timeouts clamped). While the vault is unlocked they live in one place: a "1 notice" dot on the foot's lock chip, which opens a popover with each line and its action. On the lock screen the same notices sit above the panel (direction 1). Their colour comes from their tier in direction 9 (these three are attention or info) wherever they show. They show exactly while the status carries them, and the toast still marks the moment one arrives.

**Absorbs.** motion-1, motion-2, motion-4, motion-5, motion-7, motion-9, motion-11, flows-1, flows-9, trust-3, trust-9, native-15. Also motion's added items: cancel ends in a red toast, Cancel has no pending state, the drag label states an unchosen outcome, the strip has no archive name, and there is no rate or time left.

**Rulings.**
- **2026-09-11 "Drag out, integrated"** put the strip on screen from the drag's start. In the list area that trades a flash for a 54 px shift; the foot satisfies both.
- **The ops strip above the list** (min-height 42 px, app.css:870) is retired, not resized. Its shape is protected by the 2026-09-11 drag-out ruling, written after you objected to a strip that reflowed thinner: "a button that vanishes reflows the strip; a greyed one keeps its shape" (APP.md §3). A slot that never reflows answers that objection better than a strip that keeps its shape while pushing the list.
- **APP.md §6 save bar, "Success is the bar leaving — nothing else says so".** That is right for a settings save, not for a 48 GB extract.
- **2026-09-12, "the countdown's refresh is the design".** Keep the nearer deadline; drop the always-on seconds.
- **The rest:**
  - 2026-09-07 "what a toast is for" (top centre of the window);
  - APP.md §6 "no spinner";
  - ops.ts / APP.md §2.3 "not the user's to abort halfway", for verify and extract.
- The 2026-10-13 "the toast says it once" lifetime is kept.

---

### 5. Windows controls, not web controls — effort M

**Places.**
- Every text field: the filter in 30, PIN and password in 03, the panel fields in 32, the vault name in 100, and the dialogs.
- Disabled buttons: 30, 37, 60, 65.
- The rail: 30, 114. The lists: 30, 31, 60, 61.
- Dialogs: 42, 50, 71, 99c. Checkboxes, radios, slider and progress: 71, 100, 68.
- The window ground: 01, 30-dark.

**What is wrong now.**
- **Fields.** Every text field has a 2 px accent bottom edge at rest, and `.input:focus{outline:none}` removes the only focus cue. So every field looks focused, and the focused one looks like all the rest.
- **Disabled buttons.** Disabled means opacity 0.5: an accent button becomes a pale-teal slab that still looks primary, and a danger button turns pink. `pointer-events:none` also hides every disabled button's reason tooltip.
- **Rail.** The current item is a raised white card with a border, a shadow and bold text, and its tick hangs 4 px outside it. Rail hover is three levels off the ground and can't be seen.
- **Cards.** Every card in the layer floats on a drop shadow, while the list right beside the preview is flat.
- **Lists.** They look like spreadsheets:
  - a filled grey semibold header;
  - a rule under every row;
  - four markers for one selection (checkbox, wash, a floating tick, bold);
  - two different sort glyphs.
- **Form controls.** The checkboxes, radios, slider and progress bar are Chromium's own.
- **Dialogs** look like web modals: a 17 px title, 20/22 px padding, and compact right-aligned buttons on the same white.
- **Window.** It paints a fixed green radial glow where Mica would be, under a stock caption bar that is a separate surface.
- **Contrast themes.** Nothing is defined for forced-colors mode.

**Proposed change.** These are WinUI values; check each against the WinUI Gallery before shipping.
- **Text field.**
  - Rest: a 1 px --ctl-bottom underline and a --ctl fill. Hover: --ctl-hover.
  - Focus: a 2 px --accent underline and a --surface fill, with padding-bottom reduced by 1 px so the text doesn't jump.
  - Invalid: a 2 px --danger underline, at rest and on focus.
  - Remove the inner box-shadow from fields.
- **Disabled.** Set opacity to 1.
  - Standard, subtle and danger buttons: text rgba(0,0,0,.36) in light and rgba(255,255,255,.36) in dark, on a transparent or --ctl fill.
  - Accent and filled danger: fill rgba(0,0,0,.22) / rgba(255,255,255,.16), text white / rgba(255,255,255,.53).
  - Drop pointer-events:none so the reason tooltips show, or print the reason as a line.
  - Becoming enabled transitions over 120 ms.
- **Rail (NavigationView).**
  - Items are transparent, with no border and no shadow. Hover and current use a fill of rgba(0,0,0,.06) (dark rgba(255,255,255,.06)); press uses .04.
  - The current item stays at regular weight.
  - A 3x16 px pill (radius 1.5) sits inside the item's left edge.
  - One indicator element slides to the new item over 220 ms ease-enter, stretching mid-travel. Two ticks no longer shrink and grow at once.
- **Cards.**
  - Cards are flat: 1 px --stroke and no --shadow-card inside the layer.
  - Shadows stay only on what floats: the bottom panel while it is over the list, menus, the save bar, dialogs and InfoBars.
  - Settings and Vault share one row-list idiom: separate cards 4 px apart, as in Windows Settings.
- **Lists.**
  - The header is transparent, in caption type, --ink-2, regular weight. A 1 px stroke runs under the header only, and rows have no rules.
  - Row hover is rgba(0,0,0,.04), with 4 px rounded ends inset 4 px from the card edge.
  - Selection is --accent-wash plus a 3 px pill at the row's true left edge, with no bold.
  - On the file list, where several items are extracted or deleted together, checkboxes appear on hover, on selected rows, or once any row is selected. The Archives list has none: it becomes single-select, because every action on it is one archive (direction 6).
  - Use one sort glyph: a 10 px chevron in --ink-3. Right-align Size.
- **Controls.**
  - Checkbox: 16 px in tables and 20 px elsewhere, 4 px radius, accent fill with a white check.
  - Radio: 20 px; when checked, an accent ring around a white dot.
  - Slider: a 4 px track and a 20 px white thumb with a 12 px accent core.
  - Progress: 3 px.
  - One rule for link-looking things. A command is a button: Cancel, "Use recovery key instead", Lock now and the foot's Cancel become subtle buttons (24 px high in the foot). A hyperlink button (accent text, no underline at rest, a fill on hover) is kept for going somewhere or revealing something: Show in Explorer, Locate…, Details, "1 notice". The underline stays only on links inside running text.
- **Dialogs.**
  - 24 px padding and a 20/28 title.
  - A footer band (margin 0 -24 -24, padding 24, fill #f3f3f3 in light and #202020 in dark, 1 px top stroke) holding 2 or 3 equal-width buttons.
  - Width 320 to 548 px. Backdrop rgba(0,0,0,.30).
- **Window.**
  - Create it with Wails beta.20's BackgroundTypeTranslucent and the Windows BackdropType Mica. Make the body and the rail transparent and the layer semi-opaque: rgba(255,255,255,.5) in light, rgba(58,58,58,.3) in dark. Drop the radial gradient.
  - Below build 22621, fall back to a solid --ground via data-backdrop=solid.
  - When the theme is saved, call DwmSetWindowAttribute(20) so the caption follows at once.
  - The rail's brand row can then go, since the caption names the app, and the rail head can hold the vault (see 10). The lock stage drops its brand row at the same time, so no scene draws a second "Enfold" under the caption. Until then, small fix 26 keeps the two brands on one anchor.
  - All of this needs a real-window check on 22H2 or later.
- **Contrast themes.** An `@media (forced-colors: active)` block:
  - row focus `outline: 2px solid Highlight`;
  - selected rows in Highlight/HighlightText;
  - the current rail item with a 3 px Highlight border;
  - disabled controls in GrayText.
- **Later.**
  - Chrome glyphs from Segoe Fluent Icons. It is a local font; fall back to MDL2 and take the code points from Microsoft's reference.
  - Shell file-type icons via SHGetFileInfoW(SHGFI_USEFILEATTRIBUTES), cached per extension, which gives yellow folders.
  - Fluent overlay scrollbars through ICoreWebView2EnvironmentOptions8.PutScrollBarStyle. Check the current scrollbars in a real window first.
  - An optional "Accent: Enfold green / Windows accent" (app.Env.GetAccentColor()). The touch card keeps the verdigris either way.

**Absorbs.** native-1, native-2, native-6, native-7, native-8, native-9, native-10, native-11, native-16, native-19, native-20, craft-11, craft-13, craft-14, motion-12, motion-14, drift-10. Also native's added items: disabled reasons never show, and text fields have no context menu (Cut/Copy/Paste go in direction 7).

**Rulings.**
- **docs/ui/native.html.** The mockup has the raised white rail tab, --shadow-card on every card and the radial "Mica" gradient. It also shows the focused PIN box's accent underline, which became the rest state of every field. DECISIONS 2026-09-06 made the mockup "the reference the app is measured against", but these are its stills of WinUI states, not the states themselves.
- **APP.md §6 Controls.** The link underline at rest, from 2026-09-07.
- **2026-09-10 "The list", a checkbox at the head of every row on both lists.** Kept on the file list, revealed on hover or selection, which is Explorer's own compromise. Dropped on the Archives list, where it brings nothing (direction 6 says why).
- **2026-09-06, the verdigris accent.** Challenged only as an option.
- The 2026-09-10 filled danger confirm stays.

---

### 6. The Archives page at its real size — effort M

**Places.** The Archives command bar, list, details panel and pane, the empty state, and Show hidden: 30-archives-list, 31, 32-archives-panel-expanded, 33, 34-archives-multi-select, 35, 36, 37-archives-empty, 52-archives-wide-side-pane, and the narrow shots 53/54.

**What is wrong now.**
- **The bar wraps at every width.** Ten commands, Filter and Show hidden share one flat row. At 1280 "Delete archive…" sits alone on line 2; at 900 the bar takes three lines; at 1600 "Show hidden" sits alone.
- **The bar mixes scopes.** Vault-wide actions (Check files, Import records…) sit between per-archive ones. With nothing selected, 7 of 10 are greyed. Compact, Check files and Forget key… have no icon.
- **The columns.**
  - Files reads "—" for every row in real use, because the core reports it only for open archives.
  - Key v1/v2/v3 matters only around a rotation, and Status is mostly empty.
  - Together these columns take 522 px of fixed width, so at 900 px the names are cut to "Photos 20…".
  - A row with a description squeezes 12.5 and 11 px text into 36 px.
- **The panel at 1280.** At the default 1280 window the body is about 1020 px, under the 1120 px threshold, so you always get the bottom sheet.
  - With nothing selected, it is a 44 px "SELECTED" bar.
  - Expanded, it covers all but one row and puts editable Name and Description first, in 985 px fields.
  - It spreads facts 985 px from their labels and ends in a second, full-width accent Open.
- **Multi-select.** Ticking several rows enables nothing.
- **Forgotten records** hide behind "Show hidden".

**Proposed change.**
- **The command bar.**
  - One 40 px line that never wraps: [Open] (the one accent) [+ New archive] [Open file…] │ [Verify] [Compact…] [Re-key archive…] [⋯ More] … then on the right [Filter archives] (200 px) and [View ▾].
  - More holds, from the top: whichever of Verify, Compact… and Re-key archive… the bar had no room for, then Hide, then a separator, then Forget archive… and Delete archive…, the destructive ones last in danger ink.
  - View holds Show hidden ✓ and Forgotten (1). Its icon is a layout glyph, not the eye that Hide uses.
  - A ResizeObserver folds verbs into More from the right; destructive verbs always live there, and the menu shows the folded verbs above the rest in the bar's own order.
  - Disabled items keep their reason as a second line in the menu.
  - The vault-wide "Find missing files" and "Import archives from a backup…" move to the Vault page (10). The same verbs also go on the row's context menu (7).
- **Columns.**
  - Name is always two lines in a 52 px row. Line 1 is the name in 13/20 --ink. Line 2 is caption --ink-2: the description, or the file's folder when there is none.
  - Status badges follow the name: file missing, hidden, forgotten, open.
  - Then Size (right-aligned) and Last saved.
  - Drop Files, or have the core record a file count at each commit. Move Key version into the details.
- **Rows.** Single-select rows without checkboxes on this list. Every action is one-archive, and this frees 34 px. Arrow keys move the selection and the pane follows (direction 7); Shift and Ctrl do nothing more here. The file list keeps its checkboxes (direction 5). Batch Verify can come later, and would bring the checkboxes back with it.
- **The side pane at the default window.**
  - With the narrower table, re-measure the 2026-09-11 rule (Name never shorter than Last saved). The expected threshold is about 860 to 900 px of body, so a 1280 window gets a 280 to 300 px pane. The bottom sheet stays for genuinely narrow windows.
  - In the pane, facts come first (kv: Size, Last saved, Created, Compression, Last verified).
  - Then File, stacked, with Show in Explorer · Locate….
  - Then "Name and description" behind an Edit pencil, using the same save bar.
  - Actions: [Details…] [More ▾], and no second Open.
- **Nothing selected.** The pane becomes a vault summary:
  - "Personal vault · 3 archives · 260 GB";
  - "Last backup 22 days ago [Export…]";
  - warnings as rows, such as "Passport scans — file missing [Locate…]".
- **The bottom sheet** (narrow windows only). Its body has two columns: the facts as a kv grid, and the file with the description. Name and description are edited behind Edit, in fields capped at 460 px, with the save bar in flow, never over the actions. The actions (Details…, More ▾) sit in its header row. Under about 520 px the two columns become one.
- **Empty list.** A centred block: "No archives in this vault yet" · [+ New archive] [Open an .efd file…] · and a subtle "Import archives from a backup…". No pane and no selection verbs.
- **Forgotten view** (View ▾ › Forgotten, or a "Forgotten · 1" chip in the head). Each row shows its date: "Old laptop backup · key kept until 2026-11-07" (shot 36's record, forgotten on 2026-10-08), with [Restore] and "Drop now…" on the row. An archive forgotten today would show 2026-11-12, thirty days on, as direction 8's Forget dialog says.
- **Narrow windows.** Below a 1008 px window the rail collapses to 48 px icons (NavigationView LeftCompact), giving 164 px back. This is the one definition of the compact rail, shared with directions 3 and 10:
  - The vault head shrinks to a 32 px state button: a padlock, open or closed, whose tooltip and accessible name are "Personal vault · Unlocked · Lock now".
  - Archives keeps its icon, and the op dot from direction 4 sits on the icon's corner.
  - The open archive's child row stays as an indented 20 px archive glyph under Archives, current while shown, with the archive's name in its tooltip. Its × moves to the Archive page head's [Close], so nothing in the compact rail closes an archive by accident.
  - Vault and Settings keep their icons; no brand.
  - The rail's labels come back as a flyout when the hamburger at the top is pressed, as NavigationView does.

**Absorbs.** ia-2, ia-6, ia-7, ia-11, ia-14, craft-5, craft-6, craft-7, native-5, drift-2, drift-3, drift-13, drift-14, flows-8. Also ia's added item: a forgotten row keeps Open, Compact, Verify and Hide enabled.

**Rulings.**
- **DECISIONS 2026-09-07 Revision 2, "the Archives page becomes the vault's inspector".** Nothing in it makes every verb a top-level button.
- **APP.md §13, the inspector's columns** (Files "open archives only", Key vN, Status). Also DECISIONS 2026-09-06 (no file counts in the registry), if you choose to record counts.
- **DECISIONS 2026-09-11/12, the 1120 px side-pane threshold.** The panel's motion is your own design and stays for narrow windows; the threshold is re-measured, not dropped.
- **APP.md §6 / 2026-09-11, "nothing is selected → the name is empty, the chevron dim".**
- **2026-09-10 "The list" (checkboxes on both lists).** Reopened for this list only. Its aim was one list idiom across the app, but on the Archives list every action is one archive, so the checkbox selects for nothing (flows-8). The file list keeps it.
- **APP.md §13 (forgotten records show under Show hidden).**

---

### 7. Explorer's habits: keys, right-click, Enter, one Extract — effort M

**Places.**
- The Archive page file list: 60, 61, 63, 64, and 113-focus-row-keyboard.
- The Archives list.
- Every dialog with a field: 77-archive-rename-dialog, 78, 38, 42.
- Extract: 71. Move, which is drag-only.
- The preview of a picture: 69. The conflict dialogs: 70 (adding), 73 and 74 (extracting). The refused name: 75. The Details dialog's Copy menu: 41.

**What is wrong now.**
- **Keys.** Arrows move a green ring but not the selection, so the preview doesn't follow; Space selects. There is no Del, F2, Backspace or Alt+Up, Alt+Left, Home/End, Shift+Arrow, type-ahead or Ctrl+F. Enter or a double-click on a file does nothing.
- **Right-click** does nothing anywhere, except one Copy in Details.
- **Dialogs** open with focus on the box, not the field, and don't submit on Enter. Rename doesn't select the name's stem.
- **Extract.** "Extract all" on the toolbar ignores a selection, and the selection's "Extract…" sits at the bottom-right of the preview pane. The Extract dialog defaults to Replace, with the destination next to the .efd.
- **Move** is a native drag, one folder level at a time.
- **Find.** There is no find inside an archive.
- **Looking at a picture.** In 69 a photo from "Photos 2024" gets a 264x186 box, with about 400 px of empty pane below it. To see it properly the only way is Extract…, which writes a plaintext copy to disk. That is the exposure direction 9 worries about, offered as the way to look at a file.
- **The conflict dialogs put the accent on overwriting.** Adding files whose names exist (70) offers Cancel · Skip those · Keep both · **Replace**. Extracting into a folder that has the same names (73) offers Let me decide for each file · Skip all · **Replace all**. In both the filled button, the one Enter would press, is the mass overwrite. 73 also lists the names without saying which copy is newer; only the compare list (74) does that.
- **The refused name (75)** explains the rule in a paragraph ("Shorten halves it, keeping the extension…") instead of showing the shorter name it would write, and asks again for each file.

**Proposed change.**
- **One pure keymap module**, lib/listkeys.ts, mapping a key event plus context to an intent; vitest can cover it.
  - Both lists: Arrow moves the selection and the pane follows; Home and End go to the first and last row.
  - File list, selection: Arrow sets the anchor; Shift+Arrow, Shift+Home, Shift+End, PgUp and PgDn extend; Ctrl+Arrow moves focus only; Ctrl+Space toggles; Ctrl+A selects all; Esc clears. The Archives list is single-select (direction 6), so these do nothing more there.
  - File list, opening and navigating: Enter opens a folder; on a file, Enter or Space opens it in the viewer (below), never Extract. Backspace or Alt+Up goes to the parent; Alt+Left goes back to the Archives list.
  - File list, commands: Ctrl+E → Extract…; Del → Delete…; F2 → Rename; Ctrl+Shift+N → New folder; F5 refreshes; Alt+Enter → details; Shift+F10 or the Menu key → row menu.
  - Both lists, type-ahead: a 700 ms buffer that jumps to the first matching name.
  - Archives list: Enter opens the archive; Ctrl+N New archive, Ctrl+O Open file…, Del Delete archive…, F2 edits the name, Ctrl+F Filter.
  - Everywhere: Ctrl+L Lock now.
  - Show the shortcuts in titles and menus.
- **Row context menus.** Use ContextMenu.svelte, extended with disabled items, separators and accelerators.
  - File: **Open** Enter (the viewer; the default item, in bold) · Extract… Ctrl+E · Extract here · ─ · Rename F2 · Move to… Ctrl+Shift+M · ─ · Delete… Del · ─ · Copy name · Properties Alt+Enter.
  - Folder: Open · Extract… · Rename · Delete….
  - Archive: Open Enter · Show in Explorer · ─ · Verify · Compact… · Re-key archive… · Hide · ─ · Details… Alt+Enter · Locate… (when missing) · ─ · Forget archive… · Delete archive….
  - A right-click on an unselected row selects it first.
  - Text fields get Cut · Copy · Paste · Select all. Secret fields get Paste only, through the core's clipboard.
  - The "file missing" chip becomes a button that runs Locate….
  - The Details dialog's one menu (41) is fine as it is: Copy on a value. Add "Copy all as text" for a bug report, and draw it with the same menu component.
- **The viewer: look without extracting.** Enter or Space on a file opens it large, inside the window, over the layer. It is built on the preview pipeline that already exists: the loopback URL with `no-store`, decrypted on demand, nothing written to disk.
  - The picture or text fills the layer on a dark backdrop. The name, "4 of 13" and the facts sit in a 40 px top bar, with [Extract…] (Ctrl+E) at its right as a standard button.
  - ← and → step through the folder's previewable files, Esc or Enter closes, and focus returns to the row. Video and audio play with their own controls, as in the pane.
  - Types the WebView cannot show (HEIC, PDF) open on their facts and type icon, with Extract… in the bar. PDF stays out of the window, as 2026-09-13 ruled.
  - The pane's art box keeps its job as the quick look; the viewer is the long look. Section (e)'s Viewer-first fork goes further.
- **Conflicts: the safe answer is the default, as in Explorer.**
  - Extracting into a folder with the same names (73) uses the shape of Explorer's own "Replace or Skip Files" dialog: a title that counts ("3 files with the same names are already in D:\Archives"), then three command rows of equal weight, each with a consequence line. They are "Replace the 3 files in the destination (the old ones are deleted)", "Skip these files" and "Let me decide for each file". No row is filled with the accent. Esc and the window's close are Skip.
  - Adding files whose names exist (70) gets the same three rows plus Keep both: "Replace the 2 files in Photos 2024", "Keep both (the new ones get a new name)", "Skip these files", "Let me decide for each file". The rows show each pair's size and date, because 70 today says only "a.jpg is a file here."
  - The compare list (74) is good and stays. Its button reads what it will do: "Extract 2, skip 1". The rule "a tick on both sides keeps both" becomes a label on the tick column, "Keep both".
- **A refused name (75)** shows the name it would write: "Write it as 2024-07-14 Reykjavik to Vik - the long….HEIC?" [Use this name] [Rename…] [Skip]. When more records are refused, a tick "Do the same for the other 3" sits above the buttons. The paragraph about how Shorten works moves behind "How names are shortened".
- **Dialogs.**
  - The body is a `<form>`. The accent action is type=submit, disabled while it would refuse.
  - The first enabled field gets focus.
  - Rename selects the stem (`setSelectionRange(0, lastDot)`). It uses TextField, so a clash with a sibling name is judged in place, as Create folder already does.
- **One "Extract…" on the toolbar.**
  - Its dialog gets a scope line: "(•) The 4 selected items ( ) Everything in Photos 2024". With nothing selected it opens on Everything. The 2026-09-10 destination rule is re-applied per scope.
  - "If a file is already there" defaults to "Ask me about each conflict". The conflict question and compare list already exist. Remember the last choice.
  - Ctrl+E opens it, and so does the viewer's bar. The pane's Extract… stays as a shortcut. Enter never extracts: writing plaintext out is always a named command, never the default action of a file.
- **Move to…** (in the menu, Ctrl+Shift+M). A folder-tree dialog built from Archive.Page per folder, folders only, with the current folder greyed and Create folder inside. Also spring-loaded folders during a self-drag: hovering 700 ms over a folder enters it.
- **Find.** Type-ahead now (S). Later, a "Find in archive" box backed by a new core Archive.Find, with a flat result list and a Path column (L).

**Absorbs.** native-3, native-4, flows-2, flows-3, flows-4, flows-10, flows-11, flows-14, flows-15. Also the added items: no Explorer keys and no find (ia), inputs without a menu (native), and a rename clash reported as a toast (flows). And the screens no lens took up: the picture preview (69), the conflict dialogs (70, 73, 74), the refused name (75) and the Details menu (41).

**Rulings.**
- **APP.md §6 list keys** (2026-09-10: Space selects, arrows move focus). That suits a checkbox list, not Explorer.
- **APP.md §6, "Extract all … the pane's Extract… is the selection's".**
- **2026-09-10 "Extract"** made Replace the default as "the wizard default of every archiver". WinRAR's own help marks "Ask before overwrite" as the default, and Windows' Extract All asks per conflict, so the premise does not hold.
- **2026-09-09** left "Move has no keyboard path" open; this closes it.
- **2026-09-13, the PDF viewer not in the window.** Kept: the viewer shows only what the pane already shows, larger.
- **APP.md §3, the conflict question and the refused-name question** keep their answers and their order of asking; only the default, the shape and the words change.
- **Not against 2026-09-06 "context menu off".** That ruling disabled WebView2's own menu (Save image as). An app-drawn menu is unaffected; say so in the entry so the two are not confused later.

---

### 8. One word per thing, consequences first — effort M

**Places.** All copy, and in particular:
- the rail and lock header ("Keystore") and the Keystore page (90);
- the dialogs: 38, 39, 42, 43, 44, 46, 93-97;
- the banners: 19, 89b, 92, 93b;
- Settings (100) and the feet (30, 60, 90, 100);
- Details (32, 40) and the recovery reveal (99c).

**What is wrong now.**
- **The vault's name.** The one file you own is "Keystore" in the rail, the page title, the lock header and the foot, and "vault" in every sentence. The reveal points to a "Keys page" that doesn't exist.
- **Unlock methods** go by five names: way to unlock, way in, slot, key and kind. On screen 90 alone, the header says "Ways to unlock", the count "4 slots" and the foot "4 ways in".
- **"Key"** means a YubiKey, the recovery key, an archive's key and the vault's master key.
- **Verbs collide.** "Rotate key" and "Rotate now" are different operations. There are four "check" verbs, and two of them are different backup checks with nearly identical names.
- **Mechanism instead of consequence.** Dialog bodies narrate the core's procedure in the passive voice, or justify a design choice ("asking would be friction and not a guard").
- **Format words** reach the main surfaces: slot region, superblock, registry, generation, KID, seq, zstd, session keys, entangled.
- **Wrong promises.** "Unlock" appears although a touch still follows; "use your recovery key" appears with no button for it; "open a file you already have" appears with no such command.
- **Conventions drift.** "…" is missing on some commands that open a dialog; chips are lower-case; "thirty days" sits beside "30 minutes"; em dashes chain clauses.
- **Scattered copy.** Most copy is inline in about 15 components, despite APP.md's "one table".

**Proposed change.**
- **A glossary at the head of strings.ts, enforced by a test.**
  - Vault = your .eks file and what it holds. Never "keystore" on screen. The printed recovery sheet's title needs its own decision, because sheets already printed say "Keystore".
  - Unlock method = a YubiKey, the password, or a recovery key. So "Add an unlock method…", "4 unlock methods", and "At least two unlock methods are always kept, one of them a recovery key."
  - Archive key = Technical details only.
  - Backup = an exported copy of the vault.
  - The rail item and page title become "Vault". Header states: Locked / Unlocking / Unlocked / Open in another Enfold / Damaged / No vault yet.
- **A verb table.**
  - Hide / Unhide: the list only.
  - Forget archive… / Restore: the record goes soft for 30 days; the chip reads "Forgotten".
  - Delete archive…: deletes the file once it proves to be this archive, then forgets the record.
  - Compact…: rewrites the file to give space back.
  - Re-key archive… versus Re-key vault….
  - Verify: see 9, which makes it honest.
  - Find missing files: vault-wide.
  - Inspect a backup… (reads its label) versus Test that a backup opens… (opens it with the recovery key).
  - Import archives from a backup…: takes archive records out of a backup or a copy and keeps this vault. Its buttons are "Import 2 archives" and "Cancel".
  - Replace this vault with a copy…: today's lock-screen import. A backup counts as a copy here, since the glossary says a backup is an exported copy. Short form on a banner's button: "Replace with a copy…".
  - Restore from a vault or backup…: the same import at first run, when there is no vault to replace.
  - These four are the only backup verbs, and every direction uses them: the lock screen's chooser (1), the tampered banner (9) and the Vault page (10). "Import a copy…" and "Import a backup or a copy…" go.
- **A dialog template.**
  - The title is a question that names the object.
  - The body is at most two sentences: the consequence for your files, then the safety net or the undo.
  - Buttons are verb plus object.
  - Mechanism goes behind a "How this works" disclosure, or into APP.md.
  - Rewrite these eight first: Delete, Forget, Re-key vault, Change password, Turn off password, New archive, Compact, Add an unlock method. Screen 76 ("Permanently delete 2 files from Photos 2024? This cannot be undone.") is the reference.
  - Example Delete: "Delete Photos 2024? Deletes D:\Archives\photos-2024.efd for good, after checking that the file really is this archive. Its key is kept for 30 days in case a copy of the file turns up." [Delete archive] [Cancel]. The primary comes first, as in a ContentDialog's footer band (direction 5).
  - Example Forget, on 2026-10-13: "Forget Photos 2024? The file stays where it is. You can restore it from View › Forgotten until 2026-11-12; after that its key is gone and the file can't be opened." [Forget archive] [Cancel].
- **Two registers.**
  - Main surfaces use user words. The Details dialog becomes "Technical details" and keeps KID, seq, the hashes and the generation.
  - "Deriving the session keys." → "Opening the vault…".
  - "Matched a slot in this keystore." → "This key unlocks Personal vault.".
  - "Stored as zstd + dictionary, 71% smaller" → "Compressed: 71% smaller".
  - The foot's "key v3" goes, and "2.9 GB free" → "2.9 GB unused".
  - "Entangled password" → "Password with every YubiKey".
  - The Backups paragraph → "A backup is a copy of this vault that opens with its recovery key. Keep one somewhere other than this PC."
- **Buttons name the next thing that happens.** Card 2 always reads "Continue". "Start again" only if it really starts again. No-match gets [Use recovery key]. The empty Archives page gets real buttons.
- **Banners say** what happened, what to do, and carry the button.
  - Reopen: "A save couldn't be confirmed. Reopen to see which version was kept. [Reopen]".
  - Copy mismatch: "This isn't the copy the vault saved last; maybe an older backup. Your next save makes it the current one."
- **Settings words.**
  - "Lock when idle": "After 10 minutes without mouse or keyboard. Up to 30 minutes."
  - "Always lock after": "Even while you're working. Up to 8 hours."
  - Values read "10 minutes (default)".
  - Remove "Look" until a second look exists.
  - Hide "Recovery record" until volume exports ship in 1.1, or label it "Used by volume exports (coming in 1.1)". When it returns, call it "Repair data".
  - About: "Encrypted, compressed archives unlocked with a YubiKey."
- **Feet** say only what the page doesn't already say.
- **Conventions.**
  - Use "…" whenever a dialog or picker follows: Extract all…, Export backup…, Add an unlock method…, Re-key vault…, Compact….
  - Chips in sentence case: Missing, Forgotten, Hidden, Already here, Update, New.
  - Numerals: 30 days.
  - At most one dash per line.
  - One date style: the app's ISO form (2026-10-13 01:24) in lists, facts and dialogs; a relative phrase ("yesterday, 03:24", "22 days ago") only where recency is the point, such as the last unlock or the last backup.
- **One table, enforced.**
  - Move every literal into strings.ts by screen: lockCopy, archivesCopy, vaultCopy, settingsCopy, dialogCopy.
  - Add a vitest that flags capitalised text nodes in components.
  - Add a banned-terms test over string values (keystore, slot, superblock, registry, KID, way in); only the technical-details table may bypass it.

**Absorbs.** ia-5, copy-1, copy-2, copy-3, copy-4, copy-7, copy-8, copy-10, copy-12, copy-13, copy-14, trust-15, drift-7. Also copy's added items: placeholders with invented names, and the key ID shown twice.

**Rulings.**
- **The approved native.html** says "Keystore", "Ways to unlock", "3 slots" and "ways in", and APP.md uses "way in". The spec can keep its term.
- **tamperedCopy's rule**, "naming one cause would be a claim". The precision moves to Technical details and the log; the banner says what to do.
- **DECISIONS 2026-09-07 R2 "*Forget key*"** and **2026-09-10 "Reclaiming space"**.
- **2026-09-06 "Native look for 1.0"** is why the Look row exists. The ruling stands; the row goes.
- **DECISIONS 2026-09-07**, the recovery-record caption (what the number means goes on the row's own line), and the 2026-09-07 form rule (the PIN rule stays shown).

---

### 9. Rank the warnings; give safety its receipts — effort M

**Places.**
- Lock screen notices: 19-lock-tampered-stale, 16-ceremony-failed.
- The tampered bars: 92, 93b. The locked header chip and the banners: 56, 57, 82.
- The Keystore backups card (90), the close question (50), the recovery reveal (99c), and turning the password off (97).
- Verify and "Verified N saves ago" (32, 47). Extract (71).
- The boot failure: 115.

**What is wrong now.**
- **One amber for everything.** Tampered (a possible attack; every change disabled) and stale (heals itself) wear the same amber and the same icon. They sit stacked at the bottom of card 1, below the Unlock button. A harmless touch timeout is red, and the "Locked" chip is amber, so safety looks like a warning.
- **Missing banners.** The Archive page and Settings show no tampered banner at all.
- **Backups.** The backup card shows "Last backup 2026-09-21" and "Last changed 2026-10-13" in two different cards and never compares them. The card opens with a format definition.
- **The locked copy is wrong.** Add, Delete, Rename and Move still commit after a lock (APP.md §2.3), and Add is enabled in 82. Three places say less than that:
  - the lock screen's bar (57; Lock.svelte:426): "1 archive still open — browsing works, saving needs the vault";
  - the Archives page banner (56; ArchivesPage.svelte:974): "Open archives can still be browsed; everything else needs the vault";
  - the Archive page banner (82; strings.ts:200): "The vault is locked. This archive stays open while you are here." This one is not false, but it says nothing about the archive still being decrypted or still saving.
  
  The first two are wrong, and wrong in the reassuring direction.
- **The close question.** It doesn't say the vault stays unlocked in the tray, and "Keep running in the tray" is the accent button.
- **The recovery reveal** speaks only about theft. It is a standard 460 px modal and shows the key ID twice.
- **Verify overstates.**
  - It re-hashes the ciphertext and records the hash (FORMAT R36) without authenticating any chunk.
  - So it cannot report damage, and it would record a damaged file's hash as the new baseline.
  - Yet "Verified · 2 saves ago" reads like a seal.
- **Turning the password off** uses an accent "Turn off" button.
- **Extract** writes plaintext outside Enfold without a word about it.
- **The boot failure (115)** is one grey sentence and a Retry in the middle of an empty window, with no brand, no heading, and no way to reach the log it mentions.

**Proposed change.**
- **Three tiers with fixed meanings.**
  - Info (neutral, ⓘ): heals itself or only informs. Stale, timeouts clamped.
  - Attention (amber): a weakened guarantee. Lock detection unavailable, key may still be PIN-verified, backup behind, an archive open while locked.
  - Danger (red): tampered.
  - No tier, neutral: the touch timeout ("The key stopped waiting.") and the Locked chip (closed padlock, --ink-2).
  - The tier sets the colour and icon; the place is set by state. On the lock screen, notices sit above the panel (direction 1). While unlocked, banners take the notice slot above the list in tier order, and standing notices such as "your YubiKey may still be PIN-verified" live in the foot's "1 notice" popover (direction 4).
- **Tampered.**
  - A full-width danger banner above the lock panel, and as the first element of every unlocked page: Archives, the Archive page, Vault and Settings.
  - Copy: "This vault file doesn't match itself, so it may have been changed outside Enfold. Changes to the vault are off until you replace it with a copy you trust. Your archive files are untouched. [Replace with a copy…] Why?"
  - Why? discloses the format cause. The button locks if needed and opens the import flow.
  - Check what the R25 freeze actually blocks before promising "off".
- **A backup status line** at the top of the backups card.
  - The predicate is "archives or key versions the last backup lacks": each record's created_at and key-version dates against lastExportAt. Not modifiedAt, which moves with every save and would nag forever.
  - Behind: attention, "Backed up 22 days ago. 2 archives created since are not in it.", with Export backup… as the accent.
  - Current: "✓ Backed up 2026-10-13 14:02. It holds every archive key."
  - Then the ruled consequence in plain words: "Your archives open only with this vault file or a backup of it. The recovery key opens either one; it can't replace them."
  - Optionally, an attention dot on the Vault rail item.
  - lastExportAt is local and unauthenticated, so it can drive wording, not gating.
- **Locked with an archive open** (attention): "The vault is locked. Photos 2024 is still open in this window — decrypted, and changes to it are still saved. [Close archive] [Unlock]". The Archives page banner says the same of every open archive. On the lock screen, above the panel: "Photos 2024 is still open in Enfold, decrypted. [Show] [Close it]".
- **The close question.**
  - Unlocked: "The vault is unlocked. In the tray it stays unlocked until 14:29, or until you lock Windows. Quit locks it now."
  - Equal weight: [Quit] [Keep running in the tray]. Or a third answer, "Lock and keep in tray", as the accent; the tray menu already has Lock now.
  - Locked: as now. Mirror the sentence on the Settings row.
- **The recovery reveal.**
  - 520 px wide, digits at 20 px Cascadia Mono, the key ID shown once as the heading's label.
  - Two sentences: "With this key, the vault file or a backup opens without your YubiKey. Keep it from others." and "Without the vault file or a backup, this key opens nothing. Keep those too." The print sheet already says the second.
  - Print… is the suggested action. Save as text… carries its caution inline.
  - An optional "Check what I wrote": two random groups that tick green. It is never a gate.
  - Then "I have written it down".
- **An honest Verify.**
  - Either rename it to what it does ("Refresh fingerprint"), or make it a real authenticated pass; it already reads the whole file.
  - The fact becomes "Fingerprint taken 2026-10-11 · 2 saves since", which needs a date in ArchiveSummary.
  - Status shows a quiet check while current, and "never checked" in --ink-3.
- **Turning the password off.**
  - Copy: "After this, a YubiKey and its PIN alone open the vault."
  - A standard button, "Turn off the password". The ceremony it already runs stays, and the copy says an unlock method will be asked for.
- **Extract** to a removable, network or synced folder adds one info line to the dialog, above its buttons: "The extracted copies are ordinary files, outside Enfold's protection." It is a sentence, not a gate. A drag-out says it in the foot's done line ("Copied 4 files to Desktop as ordinary files").
- **A boot failure you can act on (115).** The brand mark at its usual place, a heading "Enfold couldn't start", the existing sentence, then [Retry] (accent), [Open data folder] (Shell.Reveal on the data folder, which holds the log) and [Copy details], which copies the error code and the time. Nothing of the vault is drawn, as APP.md §2.4 rules for the boot gate.

**Absorbs.** trust-1, trust-4, trust-5, trust-7, trust-8, trust-13, trust-14, drift-8. Also the added items: banners that understate what still works after a lock, no tampered banner on the Archive page or Settings, plaintext extract (all trust), and Verify promising a check it does not perform (flows). And the boot failure (115), which no lens took up.

**Rulings.**
- **2026-09-13, "the close button never means to the tray by default".** The accent on "Keep running in the tray" brings back a visual default.
- **2026-09-07 "the first click is a reflex" / 2026-09-09 "the reveal asks nothing twice".** The readback is optional, so nothing is asked twice.
- **The tamperedCopy two-cause rule.** Kept, behind Why?.
- **APP.md §2.4, a failed first Status shows "one plain line with Retry".** It grows a heading and two buttons. The part of the rule that matters, never the lock scene, stays.

---

### 10. Vault things in one place — effort M

**Places.**
- Keystore: 90, 91, 94-97. Settings: 100, 105, 106.
- The save bar: 105 (Settings) and 33 (the Archives panel).
- The lock screen links (01), and Check files / Import records on Archives (30).
- The tampered banners (92, 93b), and the export-then-check backup path.
- The tray's menu and tooltip, read from `tray_windows.go`.

**What is wrong now.**
- **Upkeep lives on three screens.**
  - Import and "Check that a backup opens…" are on the lock screen.
  - Export and "Check a backup…" (a header read) are on Keystore.
  - Import records… and Check files are on Archives.
- **Prose cross-references.** Keystore points at itself in prose ("use Import records… on the Archives page. Go there"), and "Go there" only switches the route.
- **Duplicates and a split.**
  - The vault's name and file appear twice: editable in Settings, read-only on Keystore.
  - Session timeouts are vault policy but live in Settings, and the foot has to explain the split.
  - The Archives title is the vault name while its rail item says Archives.
- **Hidden actions on Keystore.**
  - Slot cards look static but are selectable.
  - "Show recovery key…" appears only after the right card is clicked.
  - The vault-wide "Rotate now" shares the per-slot row.
- **Proving a backup.** To prove a fresh backup opens: export, lock, pick the file again, type 48 digits, then unlock fully again.
- **Settings.**
  - "Exports" holds the compression dictionary.
  - Settings can't be reached while locked.
  - Select widths follow their value (748/763/763) and move when it changes.
  - Theme and Name stack in a 316 px column while their siblings don't.
- **The save bar on choices.** On Settings (105), picking "Keep running in the tray" from a select raises a bar that asks you to save it. That is the pattern of a web admin page. Windows Settings applies a choice the moment it is made, and here every Settings control is a choice with no invalid state, except the vault's name. And the moves below take the name off this page: it is the one field the 2026-09-07 ruling put under the bar, to give the bar a real invalid case. Neither Settings nor the new Vault page has decided whether it keeps the bar.
- **The tray** (tray_windows.go:41-49, 83-117). The menu is Open · Lock now · ─ · Quit, with Lock now greyed while locked and no "Unlock…". The tooltip reads "Enfold — unlocked, locks in 14 min · 1 archive open · 1 operation running". It never names the vault, although Settings says the name is "how this vault is called on the lock screen and in the tray" (SettingsPage.svelte:178). It does not say which deadline it counts, and a running operation is only a count.

**Proposed change.**
- **Rail: Archives · Vault · (gap) · Settings.** Once the caption names the app (direction 5's window work comes first), the rail head replaces the brand row and shows the vault: "Personal vault" (strong) and "Unlocked" (caption). Until then the brand row stays and the vault head sits under it. The Archives page title becomes "Archives". The rail is one spec with direction 3's archive child and direction 4's op dot, and its compact form is defined in direction 6.
- **Vault page, section 1: Unlock methods.**
  - Each card has its own actions: "Rename · Show key… · Remove…" inline, or a trailing ⋯.
  - Remove is greyed, with its reason, when the two-method invariant forbids it.
  - The section header holds [+ Add an unlock method…] [Re-key vault…].
  - Then the row "Password with every YubiKey: On [Change…] [Turn off…]".
- **Vault page, section 2: This vault.**
  - Name, with [Rename…], which opens a one-field dialog under the form rules, so an empty name is refused there, in place. This is the only place the name is edited.
  - File and last changed, read-only.
  - Lock when idle and Always lock after, as two 220 px selects that apply when chosen. Each choice is one vault write: the select shows the pending ring of direction 4 while it saves, and a refusal puts an error line under it and puts the old value back. While locked they are greyed, with "Unlock to change".
- **Vault page, section 3: Backups and copies.**
  - The status line from 9.
  - [Export backup…], with a pre-note, "Choose a drive you keep apart from this PC", and afterwards "Saved backup-2026-10-13.eks to E:\ · [Test that it opens…]".
  - [Inspect a backup…].
  - [Test that a backup opens…]: locks, then starts the check with the file preselected. The core gates VerifyBackup to the locked state.
  - [Import archives from a backup…].
  - [Replace this vault with a copy…]: locks, then runs today's import.
  - "Archive files: 1 missing [Find missing files]".
- **Lock screen.** One quiet "Backups and copies…" subtle button (see 1). Its chooser holds the two actions that must run while locked: "Test that a backup opens…" and "Replace this vault with a copy…".
- **Settings is this machine only.**
  - One column, at most 760 px wide.
  - When the window closes: three radios, Ask each time / Keep running in the tray / Quit.
  - Theme. Compression (the dictionary for small files; not "Exports"). About.
  - Every control is 220 px wide, so the control column is one straight edge. Descriptions are at most 52ch.
  - Every choice applies at once, as in Windows Settings. There is no save bar on this page.
  - Settings becomes reachable from the lock screen, via a gear in the lock header (drawn in direction 1).
- **The save bar's one job.** One rule: a choice applies when it is made; text that can be wrong in between is saved by the bar. So Settings and the Vault page have no save bar (the name goes through Rename…, above). The bar stays where several text fields are edited together: the archive's name and description, behind direction 6's Edit pencil. That is the case you designed it for, "edits staged and named, saved together", and its principles (dirty by diffing, named changes, Discard unconfirmed, Ctrl+S) carry over unchanged.
- **The tray says which vault, which deadline and what runs.**
  - The menu gains a disabled header line, "Personal vault · Unlocked, locks after 10 min idle", or "Personal vault · Locked".
  - "Unlock…" replaces "Open" while locked: it opens the window on the lock screen and starts the ceremony, the explicit Unlock of direction 1.
  - While an operation runs, one more disabled line reads "Extracting Photos 2024 · 58%".
  - The tooltip names the vault and the deadline in the foot's words: "Personal vault — unlocked · session ends 16:42 · 1 archive open". Then the Settings line about the tray is true. If the name should not show in the taskbar, drop "and in the tray" from Settings instead.
- **Error banners** name their fix as a button: tampered → Replace with a copy….

**Absorbs.** ia-3, ia-4, ia-15, craft-8, flows-12. Also the added items: Settings groups mislabelled and unreachable while locked (ia), and the Recovery record row being inert (copy). And two surfaces no lens took up: the save bar on choices (105) and the tray.

**Rulings.**
- **DECISIONS 2026-09-07 ("the settings page's shape").** The Vault card went to Settings to give the save bar an invalid case. That is a reason about the save bar, not about where the vault's name belongs.
- **DECISIONS 2026-09-07, "The save bar".** You asked for it, after your own admin pages, and it stays for the edits it was designed for. It leaves pages made only of choices: those pages never had a staged edit worth naming, and Windows applies such choices at once. "Settings saved" toasts stay out, as ruled: a choice that applied is its own confirmation.
- **APP.md §2.4, the tray menu Open · Lock now · Quit.** Kept, with a header line and Unlock… when locked. "Close all archives", removed on 2026-09-13, is not brought back.
- **APP.md §2.1 (VerifyBackup on the lock screen).** Kept, by locking first. Running it while unlocked would need the importGateLocked gate relaxed for a staged copy.
- **DECISIONS 2026-09-07, "Show recovery key… only while a recovery slot is selected".** Per-card actions meet its aim (never disabled beside a YubiKey) without hiding the button.
- **The spec is already split here.** APP.md §6 puts session settings under "Keys & backups", while DECISIONS 2026-09-06 put Appearance in Settings.
- **APP.md §6 Responsive (two columns)** is dropped on Settings only.

---

## (c) Small exact fixes

One-liners, not worth a direction, that add up. File hints are from the panel's verified evidence.

1. **Invisible touch sub-line in dialogs.** `.dialog p` (app.css:889) outranks `.touch-sub` (:506). Scope it with `.touchbox .touch-sub, .touchbox .touch-slot { color: var(--touch-soft) }` (CeremonyPanel.svelte:108-109). [craft-2, drift-6, trust-11]
2. **Lost danger underline.** `.input[aria-invalid="true"]` (app.css:679) comes after `.input.invalid` (:335) and replaces the --danger bottom edge with --danger-line on all sides, plus a 2 px pink ring. Reorder them or merge the rules. The same rule draws the pink frames on the recovery cells (13, 14). [trust-6]
3. **Hidden reasons.** `.btn:disabled { pointer-events: none }` (app.css:225) hides every disabled button's title reason (Remove, Turn on…, opReason). [native, added]
4. **Keystore "Go there"** should call `store.askImportRecords()` (KeysPage.svelte, the Backups card), not just switch the route. [ia-3]
5. **RecoveryReveal says "the Keys page"** (RecoveryReveal.svelte:87, 134, 144); no page has that name. [copy-1]
6. **Ceremony dialogs print their title twice.** Drop `kindTitle` when the panel sits inside a Dialog (CeremonyPanel.svelte:101). [copy-11]
7. **Your own Cancel ends in a red error toast.** Treat `op.cancelled` (ops.go:191) as a neutral "Stopped." line in `applyOp` (state.svelte.ts), and update ops.test.ts:219. [motion, added]
8. **The strip's Cancel has no pending state.** Show "Cancelling…", disabled, until op.done (OpsBar.svelte). [motion, added]
9. **The drag-out hover says "Extracting N items"** before the outcome is chosen. Change it to "Dragging N items" (lib/ops.ts `opLabel`, `dragOutCopy.extracting`). [motion, added]
10. **The ops strip on Archives names only the verb.** Add the archive's name (lib/ops.ts `opLabel`). [motion and flows, added]
11. **Compacting rows stay openable.** While an archive is compacting, its Open and double-click stay enabled and end in an "archive.compacting" error toast. Grey them with the reason (ArchivesPage `statuses()`). [motion, added]
12. **Forgotten rows stay actionable.** A selected forgotten record keeps Open, Compact, Rotate key, Verify and Hide enabled, and the core refuses each one. Grey them with the reason (ArchivesPage.svelte:948-962). [ia, added]
13. **The step transition.** `.step` (app.css:470) animates a gradient background, which cannot interpolate, using plain `ease`. Animate opacity and box-shadow with --ease-enter, and keep `.touch` through Deriving and Releasing (Lock.svelte:479), even before direction 1. [motion-3]
14. **Rail hover is invisible.** `--surface-alt` on `--ground` (app.css:390). Use `color-mix(in srgb, var(--ink) 6%, transparent)`. Also align the tick's 160 ms with the 180 ms in APP.md, or update the spec. [motion-12]
15. **The Archive page head is 4 px taller.** Caused by `.head-name { padding: 2px 6px }` (app.css:589). Use `padding: 0 6px; margin: 0 -6px`. [craft-10]
16. **Size columns are left-aligned** on both lists, so magnitudes never line up. Right-align `.num` cells. [craft, added]
17. **"2.9 GB free" in the Archive foot** is unused space inside the archive, which reads like free disk space. Say "2.9 GB unused" (status.ts:8-20). [copy-13]
18. **Numerals:** "thirty days" → "30 days" (strings.ts:131). [copy-14]
19. **Missing ellipses.** These commands open a dialog or picker without "…": Extract all, Export backup, Add a key, Rotate now, Compact, Rotate key (ArchivePage, KeysPage, ArchivesPage). [copy-14]
20. **The unknown-code fallback** prints "Error: <code>" (strings.ts:190). Make it "Something went wrong ({code}). The log in the data folder has the details." with the code in muted mono, and update its pinning test. The raw codes in shots 49 and 79 are capture artefacts. [copy-15]
21. **Invented placeholder names.** Key-name placeholders show names that read as defaults (KeysPage.svelte:268, FirstWayIn.svelte:30). Show what an empty field produces: "Named after its serial number". [copy, added]
22. **The recovery reveal shows the key ID twice**, above and below the digits. Keep the one above. [copy, added]
23. **Two accent Open buttons** are on screen at once, the toolbar's and the panel's (ArchivesPage.svelte:1160). Make the panel's a standard button, or drop it. [craft, added]
24. **New archive** ends with the new row ticked. Open it instead, with the Add menu ready. [flows, added]
25. **Delete archive… from inside an archive** leaves the archive before asking (`closeThenDelete`), so Cancel strands you on the list with your folder lost. Ask first, and close only on confirm. [flows, added]
26. **The brand mark jumps** between (61, 36) on the lock screen and (29, 28) in the rail on every lock and unlock. Until direction 5 gives the window a caption, anchor both scenes' brand at the same point. After it, neither scene draws a brand row, and the caption carries the name. [craft, added]
27. **The vault/machine split is explained twice in Settings**, in the foot and in the card note (SettingsPage.svelte:111, :121). Keep one. [copy-8]
28. **Settings promises the vault's name "in the tray"** (SettingsPage.svelte:178), but the tray's tooltip and menu say only "Enfold" (tray_windows.go:83-99). Show the name (direction 10), or drop the clause. [found reading the tray's source]
29. **The extract conflict question (73) and the add collisions (70) put the accent on Replace all / Replace.** Until direction 7 reshapes them, make every button standard, so Enter is not a mass overwrite. [found in 70, 73]
30. **The empty-folder line (66)** sits 70 px under "..", centred in a 600 px list. Name the folder and offer Add (direction 3); as a one-liner meanwhile, "Trips is empty. Drop files here, or use Add." [found in 66]


## (d) What was considered and kept, and why

Nothing here was off limits. Each item was weighed, most against a fork in (e), and kept for the reason given. If the reason stops holding, the item is open again.

- **The core and the security model.** What is encrypted, when keys die, the session timers and the commit-and-receipt model were looked at for what they cost the UI. They were kept because none of the roughness traced back to them, and changing them would cost more than any direction here. Where a direction needs the core, it says so:
  - the last-slot label (1), and a drive-root check for an absent vault (1);
  - archive_id returned with archive.key_not_in_vault (3);
  - a file count at commit (6);
  - a verify date and an authenticated verify (9);
  - Archive.Find (7), and thumbnails if fork B is taken (e);
  - VerifyBackup while unlocked, avoided by locking first (10);
  - extract cancellation between files (4).

  Direction 3 touches the model's edges twice, on purpose, and says so. It keeps an archive's keys alive while you look at another page: the same residual DESIGN §2/§10 already accepts for a mounted page, now named in the rail. And it remembers the last archive in memory only, never on disk, because a stored archive_id points at one .efd file.
- **The Native look for 1.0** (2026-09-06). Considered against Workbench, Viewer-first and Tray-first in (e). Kept because the roughness is in how Native was executed (no scale, web states, no home for status, scattered words), not in the choice of Native. The ten directions fix that for less than a second look would cost, and a second look would double the surfaces to polish just when 1.0 is held for polish. Workbench's two best ideas, the keyboard model and a dense row, come in anyway: through direction 7 and fork A's density setting.
- **The verdigris accent and the green touch moment.** Considered against the Windows accent (direction 5 offers it as an option) and against more character (fork D). Kept because the touch is the one moment the brief asks to be unmistakable, and a colour that follows the system would make it look like any other app's prompt. Fork D adds character around it, not instead of it.
- **The motion vocabulary.** The durations and easings, the 320 ms settle after unlock, the page cross-fly, and the bottom panel's push and spring (your own design) were considered and kept. They are measured, one system (lib/motion.ts), and nothing in the review found them wrong. The roughness is in what has no motion at all: the list jumping, the ops strip popping in, endings that never arrive. So the directions add motion where it is missing (the listening LED, banner heights, list fades) and bend two rules openly: "nothing loops but the touch rings" (direction 1) and the rail's two ticks moving at once (direction 5).
- **Fixed heights and geometry.** The 40 px locked line, the 44/64 px bottom panel and the 36 px file rows were considered and kept: they were set against real content and they work. The 42 px ops strip is not kept; direction 4 retires it, because its trouble is where it sits, not how tall it is.
- **What the capture could not show.** Windows chrome (Mica, the caption bar) and the print sheet were not captured by the headless browser, so they were not judged. The tray could not be captured either, but its menu and tooltip were read from `tray_windows.go` and are judged in direction 10 and fork C. Dark mode was captured for the main pages and is judged in (f); dialogs and error states in dark were not captured. The Mica, caption, scrollbar and contrast-theme proposals in 5 need a real-window check that this pass was not allowed to run.
- **Tour artefacts excluded.**
  - **The focus ring left on "Add"** after a mouse menu choice (shots 68, 69). Two verifiers repeated it with real mouse input and saw no ring (native-17 and drift-15 dropped). Only motion-13's residual stays: a one-time check in the real window after the native Open dialog returns. The demo uses shot 72, not 68, for direction 4, so the ring does not read as a finding.
  - **The raw "Error: token.reset" and "Error: file.read"** in 49 and 79 came from crafted codes. Only the generic fallback is a fix.
  - **Shot 21** (a YubiKey plus a standalone password) cannot occur, because keystore.ErrPolicy forbids it. The password-only vault it seems to stand for is designed in direction 1.
  - **The Files column's dashes are not an artefact.** They are what real use shows.
- **Scope.** Volume exports (1.1), the PDF viewer, the external player, "Open with…" and per-file verification were weighed and not pulled forward: each needs a scope decision or core work bigger than polish. The viewer of direction 7 shows only what the pane already shows. The Recovery record row is hidden, not designed.
- **Performance.** The 2026-09-12 frame-cost measurements of the bottom panel were not redone. Where a direction touches the panel, it changes the panel's body and threshold, not its motion.
- **Dependencies.** No new ones are proposed. Segoe Fluent Icons is a local system font, and every mock in the demo, the forks included, uses the app's own fonts and tokens. (The two-looks prototype loaded IBM Plex from Google Fonts; fork A's mock does not.)

## (e) Larger forks

The ten directions make Native better at being Native. These four leave that path. Each one reopens a ruling, so each gets the ruling, what it would cost, and a verdict. The demo gives each one a mock.

### A. Workbench: dense, keyboard-first, two panes

**What it is.** The look you liked beside Native on 2026-09-06, from 7-Zip's lineage (docs/ui/two-looks.html):
- a menu bar (File · Edit · View · Vault · Help) where every command lives with its accelerator;
- one screen instead of two: a tree on the left with archives as roots and their folders under them, the file list on the right, a 5 px splitter between;
- 28 px rows with column rules and no cards, shadows or rounded panels;
- a 24 px status strip of monospace cells (vault · state · deadline · selection · operation).

Opening an archive expands it in the tree. The rail, the Archives page and the Archive page become one window.

**Reopens.** DECISIONS 2026-09-06, "Native alone in 1.0 … Workbench later as a second look", and docs/ui/README.md, "A Workbench look is deferred past 1.0".

**Cost.** L, two to three weeks, then a standing tax. The prototype measured a second token set plus seven structural rules for four screens. The app has about 25 surfaces (dialogs, the ceremony, the panel, Settings), and every later change is drawn twice. The core is untouched (DESIGN §14 holds). The tree makes direction 3's rail child unnecessary.

**Verdict.** Not for 1.0, and not soon as a second look. Take its two best ideas instead, for far less:
- the keyboard model (direction 7), with accelerators shown in every menu and tooltip;
- a density setting, "Rows: Comfortable · Compact" in Settings. It is one token (the row height, 36 → 28 px, with the type ramp's caption size in cells).

If Compact still feels too airy after a month of daily use, a full Workbench look is the honest next step, and the core is ready for it.

### B. Viewer-first: an archive of photos is a gallery

**What it is.**
- A folder whose files are mostly pictures opens as a thumbnail grid. View ▾ switches between Thumbnails and Details and is remembered per folder.
- A click selects. Enter, Space or a double-click opens the full-window viewer of direction 7, where ← and → step through the folder.
- Extract… is the secondary command, in the viewer's bar and the grid's toolbar.
- Documents keep the list.

This fits what Enfold is for: looking at your own photos never needs a plaintext copy on disk.

**Reopens.** APP.md §6's Archive page, a list with a 264 px preview pane. Also the preview pipeline's scope, one file at a time, which a grid turns into dozens at once.

**Cost.** L, two weeks or more, mostly in the core.
- A thumbnail means decrypting a whole image (a 14 MB JPEG) and scaling it. For a folder of 2,000 photos that needs a core thumbnail service: decode in Go (image/jpeg and image/png, CGO-free), scale to 256 px, serve over the same no-store loopback, keep a memory-only cache with a byte cap, and cancel on a folder change.
- HEIC, most phone photos, has no decoder in Go's standard library, so the iPhone half of "Photos 2024" would show icons, not pictures. Whether WebView2 can show HEIC through Windows' HEIF extension is unverified.
- No thumbnail may ever be written to disk.

**Verdict.**
- The viewer: yes. It is already in direction 7, M, after 1.0.
- The grid: after the viewer, only for the formats the core can decode, and only after the decrypt-and-scale cost is measured on a real 48 GB folder.
- Not as the default for every archive.

### C. Tray-first: the vault lives in the tray

**What it is.**
- A left-click on the tray icon opens a 360 x 520 flyout above the taskbar, like the Wi-Fi or volume flyout. It shows the vault's state and deadline, [Unlock] or [Lock now], the last three archives (a click opens the window inside one), and running operations with their progress and Cancel.
- The main window becomes the place for browsing and managing, opened from the flyout.
- Closing the window is normal, and the close question goes.

**Reopens.**
- DECISIONS 2026-09-13, "the close button never means to the tray by default", and the close question built from it.
- APP.md §2.4's tray: Open · Lock now · Quit, the window destroyed on close, and Wails' AttachWindow helpers deliberately unused.

**Cost.** L.
- A second, frameless, always-on-top window type in Wails v3 beta.20. It must place itself at the tray icon and close when focus leaves. Wails' tray-window helpers assume a window that only hides, the model APP.md §2.4 rejected.
- A ceremony cannot run in a window that closes when focus leaves, and a YubiKey touch makes people look away, so the flyout would hand the unlock to the main window anyway.
- The archive names in the flyout must vanish at lock.

**Verdict.** No, as the primary shape. Enfold is opened on purpose to do work in a window, and the close-button ruling was your own. Take the cheap half now: the richer tray menu of direction 10, S. It names the vault and its state, offers Unlock… when locked, and shows the running operation. Reconsider a flyout only if the tray menu becomes the place you look most.

### D. More identity: the moments between work get a face

**What it is.** The working pages stay plain; the screens you pass through get character.
- The lock stage carries the Enfold mark, the folded sheet, drawn large (96 px) as a quiet line illustration above the panel. A soft verdigris wash runs across the top band of the panel itself, not the window, so it never fights direction 5's real Mica.
- First run uses the same mark, larger.
- The empty states (no archives, an empty folder, no vault yet) get a small line drawing in the same style, a folded sheet or an open box, instead of a grey sentence.
- The green touch moment stays exactly as direction 1 draws it. It is the one loud moment.

**Reopens.**
- The aim of 2026-09-06, "a first-party Windows 11 feel". First-party apps are plain at work, but several of them, Windows Security and the Windows 11 setup screens among them, put an illustration on their welcome and empty states, so this stays within the idiom.
- The restraint of docs/ui/brief.md.

**Cost.** S to M. Three or four SVG drawings made with the app's tokens (two stroke weights, the accent and --ink-3), one wash per theme, no dependency and no motion.

**Verdict.** Yes, in a measured dose, right after direction 1, which gives it a place. The lock screen is the screen you see most and it has no face today. Character there costs nothing on the pages where you work. Keep it static, and keep green for the touch.

## (f) Dark theme pass

The dark shots (01, 03, 04, 30, 31, 32, 55, 60, 61, 84, 90 and 100, dark) were checked against the light judgements. The tokens hold up: --ink-3 reaches 4.0 to 5.0:1 on the dark surfaces, which is better than light; the selected row's #173430 wash reads well; and the touch moment (04-dark), deep green with mint rings, is the best screen in either theme. Five things are dark-specific or worse in dark.

1. **Danger ink at rest is the loudest thing on the page.** In 60-dark the resting "Delete archive…" is #f19a9a salmon on #252529, brighter than the page title. In 30-dark the disabled "Forget key…" and "Delete archive…" still glow pink at half opacity. In light, #a12b2b at rest is quiet; in dark the same role becomes an alarm. Rule: a destructive command at rest is drawn in --ink (its icon may keep the danger tint), and the danger colour appears on the confirming button and in danger notices only. Directions 3 and 6 already move these verbs into menus, where this holds by itself. WinUI's dark critical colour is, as recalled, a similar salmon (check it in the WinUI Gallery), so the fix is where the colour is used, not the colour.
2. **Large accent fills glare.** The mint #54d2b9 is the brightest colour in the dark theme. Full-width it becomes a slab: the 985 px "Open" bar in 32-dark and the "Extract…" bar in 60-dark and 84-dark outshine everything else. Rule: accent fills only on buttons of natural width, which directions 3 and 6 already require. Never a full-width accent fill, in either theme.
3. **Disabled accent reads as dim teal**, not as unavailable (60-dark Extract…, 30-dark Open). Direction 5's neutral disabled fill fixes it; the dark values are given there.
4. **The painted Mica is a green light leak.** The radial gradient's #20302d sits in the rail's top-left in every dark shot (30, 60, 90, 100). Real Mica in dark is a neutral tint of the wallpaper. Direction 5 drops the gradient.
5. **Every field's mint underline** (the Filter in 30-dark, the vault name in 100-dark) is brighter than the light theme's teal and so even more "always focused". Direction 5's focus-only underline fixes it.

Nothing else dark-specific was found. One gap stays open: every dialog, toast and error state was captured in light only, so their dark forms are unjudged. The first build of direction 5's dialog band and of direction 9's tiers should be checked in dark.

## (g) Order, dependencies and the 1.0 line

The work is about six to nine weeks for one developer: one L (direction 1) and nine M, plus the small fixes. The directions are not independent, so the order matters more than the ranking.

**What depends on what.**

| Direction | Needs first | Shares a surface with | Feeds |
|---|---|---|---|
| 2 Type and grid | nothing | every mock | every direction: all mocks are drawn in its tokens |
| 8 Words (glossary and verbs) | nothing | | 1, 6, 9, 10: their labels and verbs are 8's |
| 5 Controls | 2 | | 6 and 7 (menus with disabled items and separators); 10's rail head waits for 5's window caption |
| 9 Warnings | 8 | 1 (notices above the panel), 4 (the notice slot) | 10 (the backup status line) |
| 1 One stage | 2, 8; 9's tiers for its notices | 9 | fork D |
| 4 Status | 2, 9's tiers | 3 (the op dot on the rail) | 7 (the done line of an extract) |
| 7 Habits | 5 (the menu component), 8 (verbs) | 6 (the archive row menu) | fork B (the viewer) |
| 3 The archive as a place | 2; its rail child needs the one rail spec | 10, 4, 6: one rail | |
| 6 The Archives page | 2, 5, 7, 8 | 3 and 10 (the compact rail) | |
| 10 Vault things | 8, 9; 5's caption for the rail head | 3: one rail | |

**The sequence, with a 1.0 line.**

| Phase | What | About | Release |
|---|---|---|---|
| 0. S wins | The small fixes of (c), and the S-sized pieces listed below that need no foundation | 1.5 weeks | 1.0 |
| 1. Foundations | 2 (type, spacing, kv); 8's glossary and verb table applied to every label; 5's CSS states (fields, disabled, rail item, flat cards, list header and rows, the dialog band, the WinUI controls) | 1 week | 1.0 |
| 2. The daily path | 1 (one stage, the recovery entry, the edge states, first run); 9 (tiers, the tampered banner, the backup status, truthful locked copy, the reveal, an honest Verify name, the boot failure) | 2 weeks | 1.0 |
| | **The 1.0 line, about four and a half weeks in.** Everything 1.0 says is true; the daily unlock and the first run are finished; one visual system. | | |
| 3. Daily work | 4 (the foot as a status bar, the done line, the pending primitive, the ops flyout); 7's keys, one Extract…, the conflict and refused-name dialogs | 1.5 weeks | 1.0.x |
| 4. Structure | 3 and 10 together (one rail: vault head, archive child, op dot, compact form), the Vault page and the save-bar rule; 6 (the bar's overflow, columns, the side pane re-measured, the bottom sheet, the Forgotten view); 7's row menus, Move to… and the viewer; 4's InfoBars and list motion | 2.5 weeks | 1.1 |
| 5. Window and forks | 5's window (Mica, the caption, Fluent icons, shell icons, scrollbars, contrast themes; each needs a real-window check); fork D after 1; fork A's density setting; fork B's grid after the viewer; Find in archive | when wanted | later |

Why the line falls there: a 1.0 should not say anything false, and its first impression and daily unlock should feel finished. Phases 0 to 2 cover exactly that: the wrong locked copy, an overstated Verify, a tamper warning in the wrong tier, the lock screen and the first run. Daily-work feel (phase 3) is high value too, and it is the first thing after 1.0. Structure (phase 4) reopens the most rulings and deserves its own release, with time to live with it.

**S-sized wins**, each about an afternoon. Those that need no foundation are phase 0; the ones that belong to 5, 8 and 9 land with their direction in phases 1 and 2. All of them are before the 1.0 line:
- the quiet countdown, and the preview's loading state (4);
- Extract defaulting to Ask, and no accent on a mass Replace (7, small fix 29);
- dialogs as forms: Enter submits, the field gets focus, Rename selects the stem (7);
- crumbs in the Archive head, Open file… and the .efd drop, and the interim "Leaves Photos 2024" hint on the rail (3);
- dropping the Files column and moving the key version into Details; an empty Archives page with real buttons (6);
- neutral disabled buttons with their reasons visible; a field underline on focus only; the rail's hover and current fill with the pill inside (5);
- truthful locked copy in its three places; the close question's sentence and equal buttons; Verify renamed "Refresh fingerprint" until it is real (9);
- the glossary renames on labels; the Look row removed; the Recovery record row hidden (8);
- the tray's header line, which also makes the Settings sentence about the tray true (10).

## (h) Coverage: every captured screen

Every shot in the tour, and where it is handled. "Fine as is" means it was looked at and nothing beyond the shared directions (2, 5, 8) is proposed for it.

| Shots | Screen | Handled in |
|---|---|---|
| 01-11 | Lock screen at rest and the token ceremony | 1; notices 9 |
| 12-14 | Recovery key entry | 1 |
| 15, 16 | No match; touch timed out | 1 |
| 17, 18, 20, 22 | Two keys; the deadline note; a pending touch; busy | 1 |
| 19 | Tampered and stale notices | 9 (tiers), 1 (above the panel) |
| 21 | YubiKey with a standalone password | Artefact; the password-only vault is in 1 |
| 23-27 | First run: fresh, create, import, absent, damaged | 1 |
| 28 | Finish setting up | 1 |
| 29 | Import from the lock screen | 1 (the restore dialog), 10 (the chooser), 8 (verbs) |
| 30-37 | Archives: list, panel, multi-select, hidden, forgotten, empty | 6; empty state 6; forgotten view 6 |
| 38, 39 | New archive; Compact confirm | 8 (dialog template), 7 (forms) |
| 40 | Details dialog | 8 ("Technical details") |
| 41 | Details dialog's Copy menu | Fine as is; "Copy all as text" in 7 |
| 42, 43 | Delete; Forget | 8 |
| 44-46 | Import records (merge) | 3 (the opened file pinned), 8 (verbs), 10 (its place) |
| 47 | Verify running | 4 |
| 48, 49, 81 | Toasts | 4 (InfoBars) |
| 50 | Close question | 9 |
| 51 | Key not in this vault | 3 |
| 52-55 | Archives wide and narrow | 6 |
| 56, 57 | Locked with an archive open | 9 (copy), 3 (the rail) |
| 60-65 | Archive page: root, previews, multi-select, folders | 3, 5, 7 |
| 62 | No preview for this type | 3 |
| 66 | Empty folder | 3; small fix 30 |
| 67 | Add menu | 3 |
| 68, 72, 80, 86-88 | Operations running, drag-out phases | 4 |
| 69 | Picture preview | 7 (the viewer), fork B |
| 70, 73, 74 | Name conflicts: adding, extracting, compare list | 7; small fix 29 |
| 71 | Extract dialog | 7 (scope, default), 9 (the plaintext line) |
| 75 | Refused name | 7 |
| 76 | Delete records | Fine as is; the reference dialog for 8 |
| 77, 78 | Rename; Create folder | 7 |
| 79 | What happened (results) | 4 |
| 82, 89b | Locked banner; reopen and copy-mismatch bars | 9 (copy), 4 (the notice slot), 8 (words) |
| 83, 84 | Archive page at 900 px | 3 (preview pane toggle), 6 (compact rail) |
| 85 | Archive page at 1600 px | Fine as is |
| 90, 91, 94-97 | Keystore and its dialogs | 10; 8 (copy); 9 (turning the password off) |
| 92, 93b | Tampered banners | 9 |
| 93 | Add a key | 8, 10 |
| 98, 99, 99b | The ceremony in a dialog | 1 |
| 99c | Recovery key reveal | 9 |
| 99d | Keystore while locked | 3, 10 |
| 100, 105, 106, 106b | Settings, the save bar, narrow | 10 |
| 110-112, 114 | Hover and focus | 5 |
| 113 | Row focus after arrow keys | 7 |
| 115 | Boot failure | 9 |
| (source) | Tray menu and tooltip | 10; fork C |
| (not captured) | Mica, caption, print sheet; dark dialogs | 5 (window), (f) |

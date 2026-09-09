# Enfold — the application layer

The layer that turns the four libraries — `internal/keystore`, `internal/archive`, `internal/piv`
and `internal/format` — into a Windows program: the Go core (`internal/app`), the Wails v3 shell
(`main_windows.go` and its companions at the repository root, as Wails requires) and the frontend
(`frontend/`, Svelte 5 + TypeScript, the Native look of `docs/ui/native.html`). It is the
implementation of `DESIGN.md` §10 (session model) and §14 (toolchain) and of the Application
bullets of `SCOPE.md`. Where this document and the code disagree, this document is wrong until
it is corrected, and the correction goes through `DECISIONS.md`.

The design was critiqued before any code was written (three lenses, 48 confirmed findings;
`DECISIONS.md` 2026-09-06). The rules below that begin with a bold phrase are the ones that came
out of that critique; the reasoning is in the DECISIONS entry, not repeated here.

## 1. Shape and the one boundary

One process. The **core** owns every decision and every secret: the keystore handle, the session
(`keystore.Session` — the KWK, DB and Metadata keys of DESIGN §10), the timers and lock
triggers, the unlock ceremony, open archives with their staged changes, the preview server, the
settings, the registry. It exposes **services** — methods on view structs, returning coded
errors — and emits **events**. It has no notion of a window; it runs with zero windows in the
tray. The **shell** is the Wails wiring: services registered, tray, the one window created and
destroyed on demand, file drops forwarded, the hidden Win32 window that receives lock triggers,
the shutdown hook. The **frontend** renders what the core reports and reports what the user did;
it holds no rule beyond presentation, and it can be destroyed and recreated at any moment — on
creation it subscribes to events first, then asks for the full state.

**The API the frontend can call is the exported method set of each registered service type as
reflection sees it, promoted methods included, reachable by name whether or not TypeScript
bindings were generated.** So: every type passed to `application.NewService` lives in
`internal/app/api`, is a struct with only unexported fields, embeds nothing, and holds the core
behind an unexported pointer; nothing from `keystore`, `archive`, `format` or `piv` appears in
any bound signature — services return view structs built by the core. A test enumerates the
exported methods of every registered service (skipping Wails' own `ServiceName`, `ServiceStartup`,
`ServiceShutdown`, `ServeHTTP`) against an allowlist of names *and* signatures, and the generated
TypeScript bindings are committed so that a new method or a new field on a view struct shows up
as a diff.

What crosses the boundary: the vault's state and display name, archive summaries, one page of a
file listing, slot descriptions, operation progress, coded errors. Keys, the whole decrypted
index, the registry as a whole, wrapped keys, file offsets and generations never cross. The
recovery key is the one exception, at a human's request only: shown at generation, shown again
after a *reveal* ceremony that a YubiKey or a password authorised (§3 Keys; FORMAT R38), typed
once at entry, because there is no other channel to a human (DESIGN §10 records the exception).
A copy saved to a text file is written by the core, addressed by the reveal's handle, never
carried by the page.

**Secrets across the bridge.** Four secrets cross from the frontend: the PIN, the entangled or
standalone password, the recovery key's digits, and the PIV management key typed as hex. None of
them is a bound-method argument: bound calls are marshalled and stringified by Wails before any
log-level check, and would be logged at debug level. They go through the raw message channel
(`Options.RawMessageHandler`), which Wails does not log, as `secret <kind> <promptID> <value>` messages (the kind is `pin`, `password`, `recovery` or `mgmtkey`; the shell accepts them from the main frame of the page's own origin only)
the core parses and hands to the waiting prompt. Even so a JS string cannot be zeroed and the Go
side keeps at least the copies the transport makes; the frontend clears its input on submit and
the ceremony dialogs are unmounted when they finish, and the design claims nothing more. The
shell ships with `-tags production`, never sets `Options.Logger`, leaves `LogLevel` at its
default, and a test fails if the shell mentions `slog.LevelDebug` or `application.DefaultLogger`.
`wails3 dev` builds have debug logging, DevTools and the native context menu forced on, so a dev
build may only ever be pointed at a throwaway vault.

## 2. The core's state

### 2.1 Session

```
Locked ──BeginUnlock──▶ Unlocking ──VMK derived──▶ Unlocked ──lock trigger──▶ Locked
   ▲  ▲                    │ cancel / failure / lock trigger      │
   │  └────── Releasing ◀──┘                                      │ ErrIndeterminate
   └─────────────────────────────────────────────── Broken ◀───────┘
```

**Locked.** No keystore keys in memory and no decrypted registry: the lock path is (a) cancel
or await any in-flight VMK mutation and `Unlocked.Close()`, (b) `Session.Lock()`, (c)
`Keystore.Close()` — the only call that ends the decrypted registry's life, (d) then the event
and the tray. The four plaintext facts the lock screen needs — `VaultID`, `ModifiedAt`,
`Entangled` (the slot region header's switch, FORMAT §6), `Slots()` — are cached at lock and refreshed by a fresh `keystore.Open` on
demand; a reopened file whose `VaultID` differs from the cached one is refused. The keystore
file is held open only while Unlocked.

**Unlocking.** One ceremony at a time (§2.2), on its own goroutine with a top-level `recover`
that routes to "lock and report". `BeginUnlock` while Unlocking or Releasing returns
`ceremony.in_progress` / `ceremony.releasing`, and never reaches `piv.Open`. A cancel returns
the vault to Locked at once, even while the key is still answering the ceremony's last card
call: that call outlives the ceremony as a *pending touch* (§2.2), and the status says so
(`pendingTouch`) until the card answers.

**Unlocked.** Holds `*keystore.Unlocked` only for the duration of a mutation that needs the VMK
(enroll, remove, rotate, the entangled password's switch and change, export, backup restore,
inspecting records) and otherwise only `*keystore.Session`;
the `Unlocked` is closed — VMK destroyed — the moment the Session exists (DESIGN §10). Slot
mutations therefore re-run the ceremony; the one exception is vault creation, whose `Unlocked`
enrolls the first slots. **Rotation re-derives the Session:** `Rotate` is the one call that
advances the generation, so after it the core derives the next Session from the rotating
`Unlocked`, installs it, locks the old one, then closes the `Unlocked`; the core's Session
accessor returns an error rather than a Session whose `Live()` fails, and an `ErrStale`
reaching a service is an internal invariant violation that forces Locked.

**Timers.** Idle (default 10 min) and absolute (default 60 min from unlock). **The idle timer
is reset only by corroborated user input:** `Activity()` from the frontend is a *request*,
granted only when `GetLastInputInfo` reports session input newer than the last granted reset
(32-bit tick arithmetic, a backwards step counts as no input); core operations do **not** reset
it — background work keeps its archive alive, never the session. The absolute cap applies
regardless of activity. Both values come from the registry (§3 Settings) and are clamped in the
core before the timers are armed (idle ≤ 30 min, absolute ≤ 8 h; absent, zero, unparsable or
out of range → the default, never "off"), with a warning code when a stored value was replaced.

**Lock triggers** (DESIGN §10) are one input, `lockTrigger{reason}`, accepted in every state —
and in every state a trigger drops every recovery key held for a reveal (§3 Keys), so a save
after it is refused and the page says the key can be shown again:
workstation lock / session change (`WM_WTSSESSION_CHANGE`: lock, logoff, console and remote
disconnect, remote control), display off (`GUID_SESSION_DISPLAY_STATUS` → `PowerMonitorOff`,
the primary sleep trigger on Modern Standby machines), classic suspend (Wails'
`events.Windows.APMSuspend` for `PBT_APMSUSPEND`), user inactivity (`GUID_SESSION_USER_PRESENCE`
→ `PowerUserInactive`, optional, one direction only), idle, absolute, manual (tray and
`Vault.Lock()`), exit (§5). In Unlocked the trigger locks; in Unlocking it cancels the ceremony
and sets a latch the ceremony checks at its publish point, so a ceremony that completes after
the trigger does not publish; in Locked it is a no-op. A pending touch (§2.2) is left
unadoptable by every trigger and its answer is closed unused when it comes; the lock's close of
the handle waits for it, as it waits for a cancelled ceremony. **Zeroing is synchronous on the
message thread:** the trigger handler calls the core's `lockNow`, which takes a short mutex that no
long operation ever holds, zeroes the keys (`Session.Lock()`), and hands everything unbounded —
the keystore close, the event, the tray, disk writes — to a goroutine. A logoff or shutdown
trigger runs `resolveForShutdown` (§5) instead of a bare lock.

**Broken.** Any `ErrIndeterminate` from the keystore — a slot mutation, or the registry write
that is the second half of every Save — locks the keys, stops the timers, leaves open archives
open, and shows no vault facts; the one action is `Reopen`, which closes and reopens the file
(the A/B superblocks make that succeed almost always) and lands in Locked. `Stale` on a reopened
file is a transient warning — the next commit repairs the damaged copy — not a verdict.

**Two processes.** The shell uses Wails' single-instance guard (`UniqueID`, second launch shows
the window and exits; the callback only enqueues "show window", acts on no path, unlocks
nothing). The guard is per logon session, so the keystore also takes an exclusive OS lock on
open (`keystore.ErrBusy`, shown as "this vault is open in another Enfold"), and `keystore.commit`
re-reads the live superblock's `seq` before writing and refuses when it moved. Nothing in the
core opens the keystore or starts a goroutine before `application.New` returns.

**One vault.** Enfold keeps one keystore per Windows user, at `vault.eks` in the data folder
(`%LOCALAPPDATA%\Enfold`, or `ENFOLD_DATA_DIR`); `VaultStatus.DefaultPath` names it. The one
override is `settings.vaultPath` — a vault kept elsewhere, chosen through "keep it elsewhere" when
creating or "use it where it is" when importing — and it is empty in the normal case; at start an
empty override with a `vault.eks` present adopts that file, so a copy put there by hand works
(accepted, not recommended; an override set wins over such a file, which then lies shadowed). A
configured vault that cannot be opened at start is `MissingPath` on the status and the first-run
screen names it, never "no vault yet": nothing invites a second vault over a mislaid one.

With a vault kept here there is no second one: *create* is a first-run action (ruling
2026-09-07), and the core owns the rule — `CreateVault` with a vault configured is `vault.kept`,
whatever the path and whatever `replace` says. What a vault that exists can be is **rebuilt**,
and only when its file is present and refused as not a keystore — `keystore.Open` answering
`format.ErrInvalid` or `ErrTruncated`, judged at start and on every open of the configured
path (try again, `Reopen`) — which is `Damaged` on the status beside `MissingPath`; a
permission or I/O failure is only "could not be opened, try again". The rebuild is `CreateVault`
at the damaged file's own place with `replace` (any other path is `vault.kept` too): the install
retires the file as `vault-damaged-<unix>-<n>.eks` in the data folder before the new vault takes
its place, deleting nothing, so that whatever a later tool can salvage from it is still there
(`DamagedCopyPath` names the newest; the Keys page shows it; the lock screen offers it for
nothing, since it does not open). A file that is simply absent is not damage — the first-run
card offers import and create as before — and neither is tampered: that file opens, its cure is
importing a copy (§2.2), and a rebuild over a vault that opens would lose the keys it holds.

Everything else arrives by **import**, and **nothing replaces the vault kept here until the
incoming file has proved itself.** `Vault.InspectFile` reads a file's plaintext facts through a
staged copy in the data folder (so read-only media and a file another process holds work, and the
vault's own path answers from the cached facts) — `FileInfo{Kind, ModifiedAt, VaultMatches, Newer,
SlotCount, Hardware, Password, Recovery, RecoverySlots []SlotBrief{RecipientID, Label, CreatedAt},
Generation}` (the last two since §13, so the merge dialog can name which sheet it will want),
where a *backup* has only recovery slots active (R28's
export) and a *vault* has at least one other — and every one of those is a plaintext claim (R25,
R35): they inform the dialog, they decide nothing. `Vault.ImportFile` stages the file as
`vault.incoming.eks` (read raw, never opened in place), inspects that copy, and runs the **import
ceremony** (`kind: import`) on it: a vault must unlock with one of its own ways in (`method`), a
backup opens with its recovery key and then takes the first way in exactly as at creation (`kind`,
`label`, `entangle`; `Choose` on the new secret). Only then is the vault kept here retired and the
staged file installed: retirement is a *copy* to `vault-replaced-<unix>-<n>.eks` (a name taken with
`O_EXCL`, so two retirements in one second get two names), then one rename of the staged file over
`vault.eks` — the place is never empty for an instant — after the previous handle is closed and a
pending lock's close waited for, as `openVaultFile` does. The ceremony ends **Locked**, since the
install needs the handle closed; the credential spent on proving is not reused to publish a
session. The status counts the retired copies (`RetiredCopies`, `RetiredPath`), the Keys page shows
them, and the first-run screen offers the newest one when no vault is kept — a crash between the two
steps leaves a vault under a name the app knows. Retired copies are never read by Enfold again and
are the user's to delete; their ways in stay valid for them, which the confirmation says.

With a vault kept here, *every* replacement needs `replace` — "same vault, newer" included, since
both are attacker-writable plaintext — and the page sets it only after a confirmation that names
the vault being replaced, its date and, for a backup, that archives registered after the backup's
date are not in it; without `replace` the call is `vault.exists`. An import is refused while any
archive is open (`vault.archives_open`), whose saves would land in the wrong registry, and from any
state but None and Locked (Broken needs `Reopen` first; Busy is another process). **Every create
is built as the incoming file** beside its destination and installed only after the ceremony's
latch and cancel are checked — a create cut short (Cancel, a lock trigger, a crash) leaves nothing
at the vault's place, so nothing is ever adopted whose recovery key was not shown. Installed, the
vault **opens at once** with the recovery key just made (the user chose the way in a minute ago
and proved it; a second unlock would be ceremony for its own sake): the create ends Unlocked with
the key shown over the vault, and only a lock trigger that landed meanwhile, or a file that will
not open, leaves it Locked with the key still shown. A create happens only with no vault kept
(§2.1: first run, an absent configured
file, or the rebuild over a damaged one), where `replace` covers whatever already sits at the
chosen place — a file there that could not be opened is retired rather than fought over — and
no archive may be open. **Up to the rename nothing has happened; from the
rename on the install has happened whatever follows:** the settings name the file first (a save
that fails is the `settings.unsaved` warning), a reopen that fails keeps the file's path and
name with the facts dropped and `MissingPath` set for "try again" (`Reopen` works too), and the
ceremony reports success — so a create always shows its recovery key and an import never reports
a failure for a vault that is in place. A rename that fails (retried briefly: a scanner may hold
a fresh file) removes the copy just retired, so no phantom "replaced copy" is counted, and is
reported as `vault.busy` when the file is in use; a file at the place that cannot even be looked
at is never overwritten. What is retired is inspected: a keystore becomes
`vault-replaced-…eks`; a file at the vault's own place that does not open becomes
`vault-damaged-…eks` (`DamagedCopyPath`, offered for nothing); anything else `file-replaced-…bin`,
which the status does not count.
Import and create are *committing* ceremonies (`commits`): shutdown waits for them within its
budget, as for a slot change, so an install is never half done; a quit that lands inside the
install itself still completes it, and the recovery key of a create then goes to a page that may
already be gone — the one window left, documented rather than closed. Every staged file has a
fresh name (`vault.incoming-<id>.eks` beside its destination, `vault.inspect-<id>.eks` in the
data folder), so stagings may overlap and nothing of the user's is ever removed to make room;
those names can never be the vault or an export (`OpenVaultFile`, `CreateVault` and
`ExportBackup` refuse them, and an export into the data folder), a staged copy is bounded (64
MiB, regular files only) by the copy itself, and start sweeps what an interrupted one left in
the data folder. A fresh open of a file clears the tampered warning, which only an unlock can
raise. "Use it where it is" (`OpenVaultFile`)
copies and retires nothing, follows the open-archives rule and the ceremony gate all the same,
and with an empty name keeps the name already given. A mutation on a vault that has neither a
usable token nor a password — an adopted backup being set up from a recovery-key session —
proves itself with the recovery key.

**A backup is adopted, not restored** (FORMAT §15, R28: open it, unlock with the recovery key,
enrol new slots). A backup placed by hand, or an import interrupted after the copy, is a vault with
only its recovery slot: `SetupNeeded` on the status, `BeginUnlock` with a token or a password
refused with `vault.setup_needed` (the recovery key still unlocks it — the archives stay
reachable), and the lock screen's one action is *Finish setting up* (`Vault.FinishSetup`, `kind:
setup`): the recovery key, then the first way in, ending Unlocked; a cancel after the slot committed
still leaves a vault, and the facts are refreshed. The recovery key that opened the backup stays
valid; no new one is shown. The display name is machine-local (settings), so an import asks for
one, defaulting to the current name for the same vault and to the file's name otherwise. Setup
is a mutation ceremony (`AddSlot` commits to the vault kept here): shutdown waits for it and an
unknown outcome is Broken, as for any slot change.

**A backup is verifiable without touching anything** (FORMAT §15: an untested backup is a belief):
`Keys.VerifyBackup` stages a copy, opens it with the recovery key (`kind: verify`), reports how many
archives its registry names (`CeremonyState.Archives`) and removes the copy. It runs from the lock
screen — the one screen a ceremony runs on while the vault is locked, first run included — as
"Check that a backup opens…"; the Keys page inspects a backup and points there. The outcome of an
import, a check or a create is kept by the page apart from the ceremony (`store.outcome`), so it
stays on the lock screen after the reveal is dismissed and on the first-run card when nothing is
kept.

### 2.2 Ceremony

```
WaitingForKey ──1 reader──▶ Probing ──match, password slot──▶ Password ──▶ PIN ──accepted──▶ Touch ──▶ Deriving ──▶ Unlocked
      │ >1 readers               │ match, no password ─────────────────────▶ PIN               │ 6982 touch      │ ErrAuth (password)
      ▼                          │ no enrolled key      wrong PIN ◀───────────┘                 └──▶ Touch once more, then ends failed (token.touch)  └──▶ Password
   TwoKeys                       ▼                      blocked ──▶ Blocked                ErrTooManyOperations ──▶ WaitingForKey
   (remove one)              NoMatch                    ErrBusy at Open ──▶ Busy (terminal until the user acts)
   any exit ──▶ Releasing (Card.Close in the ceremony goroutine) ──▶ Locked
```

- The driver polls `Readers()` every 500 ms while waiting; one reader → `Open`; more → `TwoKeys`
  ("more than one YubiKey is inserted; leave just one in" — the count is not said).
  A stopped Smart Card service while waiting is the empty reader set, not a failure: Windows
  starts the service when a reader arrives and stops it when the last one leaves; it is logged
  once per wait, and after ten seconds of it the waiting state carries `token.no_service` as a
  note, so a service that stays down is said on the screen, not an hour of silence. Every
  ceremony's end that is not a success is logged with its step, code and underlying error, and
  an enrolment logs what the token holds and what it decided; the log is appended across runs.
  A failed `Open` is never fed back into the poll (DESIGN trap 24): `ErrBusy` parks the ceremony
  in `Busy` until the user cancels or retries.
- Probing: `Card.Keys()` (no PIN, no touch) matched by public key against the keystore's
  hardware slots; R34 makes the match unique. No match → `NoMatch`. **PIN first, verified at
  the card before anything else** (ruled 2026-09-08): the ceremony asks the PIN and calls
  `Card.VerifyPIN` — a VERIFY, no touch — so a wrong PIN is asked again with the retries and
  nothing else moves; then, on an entangled vault (`Entangled`, FORMAT §6), it asks the password,
  which the page answers at once from the field the user filled beside the PIN, in whatever state
  that field is in at that moment; then it assembles the credential and calls `keystore.Unlock`,
  whose `ECDH` finds the card verified and waits for the touch only. One order on every vault:
  with the switch off the password step is simply absent, and the PIN prompt inside `ECDH` is a
  fallback that fires only if the card lost its verification between the two calls.
- **The card is never left idle.** The host resets an exclusive connection that carries nothing
  for about five seconds (DESIGN §11 trap 25), so while any prompt stands with a card open — the
  entangled password, the PIN inside `ECDH`, an enrolment's PIN or management key — the ceremony
  probes the card every 3 s; the probe keeps the connection alive, heals a reset it meets
  (`piv` reconnects and repeats), and notices a key pulled during the prompt: the prompt ends,
  the strip goes back to *WaitingForKey* with `token.no_card` as a note ("insert it again"), and
  the flow — unlock, import's proof, enrolment, and the unlock half of every slot change — waits
  for the key again instead of failing. The prober is joined before a prompt returns, so no
  probe is on the card when the operation resumes; a key gone while its reader is still listed
  (a removal in progress, a card that answers nothing) is tried again after a pause that doubles
  from the poll interval to two seconds, never in a spin; and a reset the probe could not heal
  (`token.reset`) is the key gone. A password enrolment releases the key that unlocked before
  asking for the password: it is not needed any more. An open that finds the card in use is
  retried for two seconds before the ceremony parks in Busy.
- Prompts (PIN, password, recovery digits, management key) are **mailboxes with an identity**:
  each `Prompter.PIN`/password call mints a `PromptID`, carried on `vault.ceremony`; a
  submission names the id, is accepted once under a mutex (a duplicate gets
  `ceremony.stale_prompt`), and never blocks a bound method; the prompt clears its slot on return.
  Cancellation is a per-ceremony `context.CancelFunc` (idempotent), never a channel close; the
  prompter selects on the context and returns an error, which `piv` turns into `ErrCancelled`
  at no cost in retries. The deadline is on the *prompt wait* and on `WaitingForKey` — the
  session's absolute default — never on the card call itself, which cannot be interrupted:
  measured, no PC/SC call cuts a touch wait short (DESIGN trap 23), and a YubiKey gives up on
  its own after about 14 s. **A cancel is immediate for the page in every step**, the touch
  included: the ceremony ends `cancelled` at once and is gone from the page — nothing to read,
  nothing to close — and the card call it was waiting on goes on without it, as a pending touch
  (next). Nothing on the page ever says "cancelling".
- **The pending touch outlives its ceremony.** A token's agreement — the card call that waits
  for the touch — runs on its own goroutine, the *attempt*, which owns what the call needs: the
  open Card (the exclusive connection, PIN-verified by this process), the token, the entangled
  password when the vault has one, the keystore handle when the ceremony opened one (an unlock
  from Locked; a slot change or a reveal uses the vault's open handle), and its *purpose*: the
  kind it was started for, the vault file, the slot. The ceremony waits for the attempt's answer
  or its own cancellation, whichever comes first. Cancelled, the ceremony *disowns* the attempt
  and ends; the attempt stands as the **pending touch**, the key still blinking, until the card
  answers: the core holds it (`pending`, beside `cer`) and the vault status says so
  (`pendingTouch`). Then:
  - **The same VMK adopts.** A ceremony that begins while a pending touch stands **adopts** it
    when the touch is the agreement it would have asked for itself: this vault's VMK, through
    one of its hardware slots — an unlock from Locked, or the unlock half of a slot change, an
    export or a reveal on the open vault, whichever kind was cancelled (the state keeps them
    apart: a Locked vault runs only unlocks, an Unlocked one only the rest). The adopter's panel
    opens at the Touch step, the key still waiting, with the touch's ordinal and whether its PIN
    was asked taken from the attempt, and a prompt the attempt needs after that — an entangled
    password refused; the PIN, when the key's policy asks for it on every operation — goes to
    the adopter's panel. An import's or a verification's agreement is another file's, and a
    proof's is its own ephemeral key: none of them is ever adopted, and none of them adopts.
    Adoption is refused after a lock trigger (§2.1: the trigger leaves the pending touch
    unadoptable) and after any slot change, and never crosses a process, since the exclusive
    connection cannot — measured (DESIGN trap 27): no other program connects to the card in any
    share mode while this one holds it, the release resets it on the exclusive handle itself
    so that no moment exists in which the card is verified and unowned, and a killed process's
    card is reset by Windows at the cleanup of its connection. **What remains is the user's
    ruling** (DECISIONS
    2026-09-07, "The same VMK adopts"): within the key's own window — at most two of its
    timeouts — whoever is at the keyboard can finish a cancelled touch for another purpose on
    the same vault without the PIN typed seconds earlier: cancel *Add a key*, press the blinking
    key under *Show recovery key*. The person is at the keyboard of a vault that is already
    Unlocked (or was, seconds ago, the one who typed the PIN), the PIN was that person's, every
    lock trigger closes the window, and the boundary that matters is the process.
  - **Two rounds, then a failure.** A *round* is one GENERAL AUTHENTICATE the key gives up on
    (`ErrTouch`; about 14 s on a 5.7.4). An attempt with an owner asks once more the moment its
    first round ends — the PIN is still verified on the exclusive connection, so the light is
    back within a fraction of a second and to the user the wait simply continues; a key whose
    PIN policy is *always* asks for its PIN first, on the owner's panel, as that policy means —
    and never a third time: the second round's end **ends** the ceremony, failed with
    `token.touch` — a finish, not a park: the core forgets the ceremony, the card is released,
    the panel's Close is right — and the user starts again. The rounds belong to the attempt,
    not to its owner: a pending touch adopted with two seconds left gets its one continuation,
    no more. An attempt without an owner never asks again and never prompts: when the card
    answers it is over — the agreement, if a touch came, is closed unused — and what it holds is
    released (the handle, then the card with its reset), exactly once, by whichever side ends
    last.
  - **A pending touch holds what its ceremony held.** To every guard in the core a pending
    touch is the ceremony it came from, still running. One born of a slot change or a reveal is
    inside `keystore.Unlock` on the vault's one handle: while it stands a registry write is
    refused with `ceremony.in_progress` and the Save owes its receipt (§3), the receipts owed —
    those the cancelled ceremony would have paid at its end included — are paid when the
    attempt ends, no slot change or reveal begins before it (their ceremonies wait, below), and
    the lock's close of the handle waits for it (§2.1). One born of an unlock from Locked holds
    the file's exclusive lock: `OpenVaultFile`, `Reopen`, an install and a rebuild answer
    `token.pending` while it stands — never `vault.busy`, which says "another Enfold", and never
    the Busy state. Exit waits for it (§5).
  - A ceremony that cannot adopt — an import, a verification, a proof; any ceremony while the
    pending touch is one of those, or left unadoptable by a trigger — and a typed credential
    that needs the handle the attempt holds *wait* for the pending touch to end, with `token.pending`
    as the note on the step they wait in (WaitingForKey; Deriving, after the password): "the key
    is still answering the cancelled request — touch it, or pull it out, to end that now". Then
    they go on as if it had never been there: the card opened afresh, the PIN asked again.
- **A pulled key answers with Win32 codes first** (DESIGN trap 26). For a moment after the key
  is pulled — before the resource manager has noticed the reader go — the CCID driver fails the
  request on the wire with a Win32 device error, `ERROR_GEN_FAILURE` (0x1f) or
  `ERROR_BAD_COMMAND` (0x16), rather than an `SCARD_` code: the VERIFY that a PIN typed just
  before the pull lands in, the agreement waiting for the touch, the open's own preflight. A
  return code outside the `SCARD_` facility from a call that addresses a card or a card handle
  is the key gone (`ErrNoCard`), in both PC/SC layers, and takes the removal's path: the PIN
  never reached the card (no retry spent), the strip goes back to *WaitingForKey* with "insert
  it again" — or the cancel wins — and never to "something went wrong"; the same code from the
  context or the reader list is the service or the session (`ErrNoService`: a remote session
  without smart-card redirection answers `ERROR_BROKEN_PIPE`), which the waiting state already
  treats as no reader. The release of a key that is gone reports nothing: a card without power
  holds no verified state, so a reset that fails because the card, the reader or the service is
  not there is not `token.reset`.
- Touch: `Prompter.Touch` fires `vault.ceremony {Step: touch, N}`; the panel takes over.
- **A wrong secret is said, in place.** A refused PIN makes the next PIN prompt carry
  `token.pin` as its note beside the count; a wrong password or mistyped recovery digits
  (`keystore.ErrVerifier`/`ErrAuth` under a typed credential) are asked for again under a fresh
  prompt id with `vault.auth` as the note — the page marks the field, says why, and keeps the
  recovery digits for correction — never a failure the user has to start over from; the
  derivation shows again after the corrected answer. A note describes its prompt only: taken,
  gone or cancelled, the prompt clears it. The management-key PIN of an enrolment follows the
  same rule. Only a token's refusal (a damaged record) parks.
- Wrong password (`ErrAuth` from Deriving): the attempt keeps the shared secret `H` the touch
  produced — one per `epk`, in the token wrapper the attempt hands the keystore — releases the
  card, and asks the password again (the page empties and marks the password field; the PIN is
  not asked); the retry re-derives from the cached `H` with no PIN and no touch, however many
  times the password is wrong. **The cache lives five minutes from the touch
  that produced it**, whatever happens in between: it is zeroed at success, at cancel, at lock, at
  the attempt's end and at the deadline, and a deadline that passes ends the ceremony at the lock
  screen's first step with the note "the password was not given within five minutes; unlock again
  from the key" — carried as `VaultStatus.Note` (`token.password_deadline`), beside
  `PendingTouch`, because the ceremony is gone as a cancel leaves it and no ceremony event
  survives that; the next ceremony clears it. The clock is armed only when the attempt carries a
  password: with the switch off there is nothing to wait for, and the memo is zeroed when the
  attempt ends, milliseconds after the touch. The cache is not a defence against a read of memory — five minutes is a window
  nothing stops — it bounds how long a hardware-verified state can be used without the password
  (ruled 2026-09-08). `MaxOperations` exhausted → "remove and reinsert the key" (WaitingForKey).
- Deriving: `keystore.Unlock(HardwareCredential{Token, Password})` → `Unlocked` → `Session()` →
  `Unlocked.Close()`. Then the ceremony goroutine's deferred `Card.Close()` runs (Releasing);
  `ErrResetFailed` is a warning event, never a failure. The publish point checks the lock latch.
- Recovery key and standalone password skip the token states. `Vault.SubmitPassword` serves
  both the standalone slot and the entangled password; there is one submit per secret kind.
- No rotation is deferred (FORMAT §8), so there is no rewrap loop and no `SwapKey` between
  tokens at the lock screen. A recovered generation that is not the superblock's is reported
  through the vault's `Tampered` state with the tampering reason (FORMAT §6.2, R25) — a spliced
  or rolled-back slot region — never as a key that is behind.

### 2.3 Archive

```
Closed ──Open──▶ Open ──first staged change──▶ Dirty ──Save──▶ Open    Open ──Compact──▶ Compacting ──▶ Closed → reopen
   ▲               │ idle (no readers, no ops)     │ Discard                 Open ──RotateKey──▶ Rotating ──▶ Open
   └───────────────┘                               │ per-archive cap        any ErrIndeterminate ──▶ NeedsReopen
```

- **Open**: keys for every version of the registry record unwrapped through
  `Session.UnwrapArchiveKey`, handed to `archive.Open` as candidates, zeroed after. The core keeps
  per archive: the committed **snapshot** (`Files()` taken once per open and re-taken after every
  index-republishing operation), the **overlay** of staged changes keyed by file id
  (added / replaced / renamed / moved / deleted, files and directories alike, with the new name,
  parent or `FileInfo`) — one entry per record and one word per entry, which is also what a row's
  `Pending` reads in §3: `added` outlives every later change to a staged-added record, `replaced`
  outranks `renamed` and `moved`, a record both renamed and moved reads `moved`, and `deleted`
  never sits on a staged add, which un-stages instead; a deleted directory is one entry, keyed by
  the directory, and the subtree it takes gets none, the **tree** — the directory records plus the overlay's staged
  directories, from which the breadcrumb and the rows come (FORMAT R39; never a prefix
  projection) — a preview **token** (32 random bytes, minted at Open, forgotten at
  Close), a reader count and a last-served time, and two clocks.
- **Dirty**: the first change calls `Begin()`; `Tx.Add` writes at once, so "3 changes not yet
  saved" means three recorded changes whose data is in the file but not published. One overlay entry is one
  change, so a deleted folder of 900 files is one change and not 901, and an entry the deletion
  swallows — a rename staged under the folder before it was deleted — leaves the overlay with it.
  `Stat.Dirty`, the pending bar and the warning that names what a discard threw away all read
  that one number. Save =
  `Commit` → receipt → one `Session.UpdateRegistry` (`LastStoredSize`, `LastWrittenAt`,
  `LastSeq`, `Revision`); Discard = `Abort`. Closing the window keeps the transaction; a lock
  keeps it. **Save and Compact are gated on `Session.Live()`** before they start
  (`NeedsUnlock`: the staged changes are kept and finish after the next unlock). The commit —
  index seal, free map, two syncs — runs under the archive's own mutex only, so the state mutex
  stays short and a lock trigger is never held up by I/O; the receipt is written under the state
  mutex right after. A lock that lands between the two, a hardware trigger inside the ~2 s
  suspend budget, or a slot-change ceremony holding the handle leaves a **receipt owed**, held in
  memory, applied when the ceremony ends or at the next unlock before any archive is opened
  (dropped if the record's kid moved), shown as "saved; vault record pending". On reopen a file whose size or `last_seq` disagrees with the
  record is reported, never adopted silently.
- **Two clocks per archive** (DESIGN §10's own idle timeout): idleness — no running op, no open
  reader, no request — closes a clean archive; a dirty one gets `archive.expiring` and a visible
  prompt (Save / Discard / Keep open, at most two bounded extensions), and a per-archive
  absolute cap from `dirtySince` that runs across a lock: on expiry `Abort` then `Close`, with a
  warning naming what was discarded. A preview range request resets the archive's clock, never
  the session's.
- **What stays usable after a lock** (DESIGN §10): `Page`, `Stat`, `PreviewText`, `Extract`,
  `PreviewURL`, in-flight readers, `CreateFolder`/`AddFiles`/`AddFolder`/`Delete`/`Rename`/`Move`
  into the staged transaction;
  Save, Compact, RotateKey, Open and Create need the session. The frontend keeps an open
  archive's view mounted across a lock (a locked banner; nothing new can be opened).
- **Compacting**: refused unless Open and clean; previews for the archive are quiesced by
  draining (stop minting URLs, refuse new requests, wait for in-flight handlers, with a timeout
  that aborts the compaction); every core entry point answers `archive.compacting` from core
  state without touching the handle; progress is coalesced to ~10 Hz; any return from `Compact`
  means the handle is finished: `Close`, reopen the path, decide from the file (the new hash and
  size go to the registry with `HashAtSeq = LastSeq`). Compact is refused when the estimate
  (live bytes over measured throughput) does not fit before the absolute cap.
- **RotateKey** is registry-first (FORMAT R33, DESIGN trap 21): gate (open, writable, clean,
  readers drained); new key and kid; registry write #1 — append the current version, retire the
  old with `RetiredAt`, move `CurrentKID`, in one update; `archive.RotateKey`; registry write #2
  — the receipt, performed even when the rotation returns a receipt with `EnvelopeStale`, which
  is then reported with `RepairEnvelope` offered. Rule A: content and layout publish to the file
  first and the registry records the receipt after. Rule B: key changes publish to the registry
  first and the archive adopts them after.
- **NeedsReopen**: `ErrIndeterminate` from any commit closes the archive and says what happened;
  Reopen shows which state won. **Verify** is an explicit op that re-hashes the file and refreshes
  `LastCiphertextHash` with `HashAtSeq`; Save never hashes.

### 2.4 Window and tray

`None ⇄ Open`. The window is destroyed on close (the provisional default,
`DisableQuitOnLastWindowClosed`) and recreated from the tray or a second launch through one
`ensureWindow()` in the shell (`app.Window.GetByName` then `NewWithOptions`); the tray's
`AttachWindow`/`ShowWindow`/`ToggleWindow` helpers are not used — they assume a window that
exists at tray creation and that close only hides. Tray: three states — Locked, Locked with
archives open, Unlocked — each with a light and a dark icon set together; tooltip with the
countdown and the open-archive count; menu Open / Lock now / Close all archives / Quit.

**Boot order in the frontend:** subscribe to every event at module scope, before the first
`await`; then `Vault.Status()`. `VaultStatus` and every `vault.*` payload carry one `Seq`
incremented under the state mutex; the frontend applies a payload only when its `Seq` is
greater than the last applied. `archive.changed {ID, Seq}` and the per-archive `Page`/`Stat`
replies carry a per-archive seq the same way. `Status()` includes `Ops []OpView` so a window
recreated mid-operation recovers progress; operation events are deltas over that snapshot.

## 3. Services

Methods are synchronous from the frontend's side and return quickly; long work runs in the core
under an operation id and reports through events. **Ids are hex strings**: 32 lowercase hex digits, and the all-zero id
`00000000000000000000000000000000` is the archive's root directory — the same value the format
writes as a top-level record's `parent_id` (FORMAT R39), so nothing is special-cased at this
boundary and the core, the page, the generated bindings and `tools/uimock/server.py` all spell it
one way. The root is a directory only where a directory is *named* — `Page`'s `dirID`, the
`parentID` of `CreateFolder`, `AddFiles`, `AddFolder`, `CheckNames` and `Move`, and among
`Extract`'s `recordIDs`, where it means the whole archive; where a record is *acted on* —
`Delete`, `Rename`, `Move`'s `recordIDs`, `Replace`, `PreviewURL`, `PreviewText` — it is
`params`, so the root is never renamed, moved, previewed or tombstoned. An id that is not 32 hex
digits is `params`. A well-formed id that names nothing live in the merged view, or names a file
where a directory is wanted, is `file.not_found`, and no call ever falls back to the root when an
id does not resolve. Times are Unix seconds; sizes are `uint64`. **Every service method returns `*app.Error`** — `{Code, Retries?, Slot?}`
whose `Error()` is the code and nothing else, produced by one `classify(err)` over every
sentinel of every package with a catch-all `internal` — and services are registered with a
`MarshalError` that emits only that shape; the original error goes to the core-side log.
`CeremonyState.Error` and `op.done.Error` use the same codes.

**Vault**
- `Status() VaultStatus{Seq, State, Path, DisplayName, LastUnlockedAt, LocksAt, AbsoluteAt,
  Entangled, VaultFileSize, Tampered, TamperedReason Code, Note Code, Warnings []Code, Ceremony *CeremonyState, Ops []OpView,
  OpenArchives int}` plus the one-vault facts of §2.1 (`SetupNeeded`, `DefaultPath`,
  `MissingPath`, `Damaged`, `DamagedCopyPath`, `KeptElsewhere`, `RetiredCopies`, `RetiredPath`);
  `Readers() []Reader{Name}`.
- `BeginUnlock()`, `CancelUnlock()`, `Lock()`, `Reopen()`, `Activity()`. Secret submissions
  arrive on the raw channel: `pin`, `password`, `recovery`, `mgmtkey`, each with its `PromptID`.
- `CreateVault(path, displayName, kind, label, entangle, replace)` (`path` empty = the one place,
  §2.1; the first way in's ceremony, then the keystore built as the incoming file and installed,
  the recovery key shown, ending Unlocked — the installed file opened with that key — with a
  vault kept, `vault.kept` — the one create with a
  vault configured is the rebuild of §2.1, over the damaged file at its own place, with
  `replace`; over any other file already at the chosen place, `replace` too),
  `InspectFile(path) FileInfo`, `ImportFile(path, displayName, method, kind, label, entangle,
  replace)` (`method` proves a vault; `kind`/`label`/`entangle` are the first way in of a backup),
  `FinishSetup(kind, label, entangle)`, `OpenVaultFile(path, displayName)` (the advanced "use it
  where it is": sets the override, copies nothing, retires nothing). **A chosen secret comes before
  the key**: with the entangled password chosen, a create — and the first way in of an adopted
  backup — asks for the password first, and only then waits for the key, inspects it and
  generates — a cancel at the password leaves nothing on the token; enrolling into an existing
  vault never asks, the key inheriting the vault's setting (§13). Such prompts carry `Choose: true` on the ceremony state (the
  page says "choose a password", never the same words as an existing one); a password typed to
  prove a current way in never does.
- Events: `vault.state` (the whole status), `vault.ceremony {Seq, Step, PromptID, SlotLabel,
  Retries, RetriesKnown, ReaderCount, N, Error, Method, RecoveryID}` (`Method` — token · password ·
  recovery — is what the lock screen's wording follows, since an adopted pending touch carries no
  argument on the page; `RecoveryID` rides `StepRecovery` beside the one-time URL in `SlotLabel`,
  §13), `vault.warning`, and from the shell
  `secret.refused {Code}`. A ceremony whose last step is the recovery key's reveal ends with that
  step in its final event, so the page shows the one-time URL above whatever view it is on.

**Archives**
- `List(showHidden) []ArchiveSummary{ID, Name, Description, Path, StoredSize, LastWrittenAt,
  KeyVersion, Open, Dirty, ReceiptOwed, NoCompression, ForgottenAt, Note Code}` — hidden and
  forgotten records are filtered unless `showHidden`. `Open(id)`, `Close(id)`, `Create(path,
  name, noCompression)`, `Hide(id)`, `Unhide(id)`, `Locate(id, newPath)`, `Compact(id) opID`,
  `RotateKey(id) opID`, `Verify(id) opID`, `CloseAll()` — `Create(path, name, method)` takes the
  compression method, `store` · `fastest` · `normal` · `better` · `best`, written into the
  record's policy (FORMAT §7.1 bits 2–5) and applied to every later open of the archive on any
  machine; `ArchiveSummary.Method` and `ArchiveDetails.Method` carry it back (they replace
  `NoCompression`); a path where a file already exists is refused with `archive.exists` — the
  archive layer creates with `O_EXCL`, Enfold never overwrites a file it did not make (DESIGN
  trap 28) — and the inspector's `Rename`,
  `SetDescription`, `Details`, `Forget`, `Restore`, `Delete` and `CheckFiles` of §13, where
  Forget and Delete — the registry record holds the only copy of the archive keys, so dropping
  it destroys the archive — are specified with their brakes.
- Events: `archives.changed`, `op.progress {OpID, Done, Total, Phase}`, `op.done {OpID, Error,
  Results []FileOutcome}`.

**Archive** (an open one)
- `Page(id, dirID, sort, offset, limit) Page{Seq, Rows []FileRow{ID, ParentID, IsDir, Name,
  Path, Size, Storage, SavedPercent, ModifiedAt, Pending}, Total, Crumbs []Crumb{ID, Name}}` —
  the live children of `dirID` (the all-zero id is the root, §3's ids) in the merged view,
  directories and files in one list; `Name` is the record's own, `Path` the joined one (R20, R39,
  bounded as R39 bounds it), and `Total` counts that directory's children, not its subtree.
  `Crumbs` is the chain from the root down to `dirID` **inclusive** and is never empty: its first
  entry is the root, `{ID: <the all-zero id>, Name: <the archive's name, the same string as
  ArchiveStat.Name>}`, its last is `dirID` itself, and a staged directory stands in it like any
  other — so the page draws the whole breadcrumb from `Crumbs` alone and takes no name from
  `Stat`. A directory row's `Size` is the sum beneath it and its `ModifiedAt` the record's own.
  The page holds a `dirID` across events and can hold one that is gone — `Discard` drops the
  folders that transaction staged, a `Delete` takes a subtree the page may be standing in — so an
  id that no longer names a live directory of the merged view answers `file.not_found`, never an
  empty listing under a breadcrumb that still names the place: the page walks the `Crumbs` it
  last held upwards, retrying until one answers (the root always does), and says which folder
  went. A live directory with nothing in it is not that case — it answers zero rows with `Total`
  zero, which is what an empty folder is. `Compact` and `RotateKey` change no id (FORMAT R33),
  so a reopen leaves the page where it was.
  `Stat(id) ArchiveStat`.
- `CreateFolder(id, parentID, name) recordID` stages a directory record (empty is fine — it is
  a record, FORMAT R39, its name validated and matched like any other's); `AddFiles(id, parentID,
  paths, policy) opID`, `AddFolder(id, parentID, path, policy) opID` — every directory the walk
  **creates** becomes a record with its own time, so an empty subfolder and every folder's
  modified time survive; every name is one element, validated with `format.ValidateName` and
  matched case folded against the live children of its parent in the merged view (R39, which is
  where the rule is kept: the archive layer refuses a folded collision again at every staged add,
  rename and move, against the transaction's own index). A source name `format.ValidateName`
  refuses is one `FileOutcome` of `failed` with `file.name`, named in the results and never
  silently skipped; when the refused name is a directory's, the walk reports that one outcome
  against the folder, adds nothing beneath it, and goes on with the folder's siblings. A
  tombstone is not a live sibling, so a deleted folder's name is free and the walk makes a new
  record. Everything the walk found is pre-flighted — names, kinds, the folded matches, and
  R39's depth and joined-path bounds over the deepest record the walk would create — **before**
  the first `Tx.Add`, so the user is asked once for the whole batch; a source that changed
  underneath between the walk and the add is one `FileOutcome`, not a failed operation.
  `policy` — `skip | replace | keep-both` — decides what happens when the incoming item and the
  item in the way are the **same kind**. Two files: `skip`, `replace` (`Tx.Replace` on that
  record, never Delete + Add) or `keep-both`. Two directories: the incoming one is **entered**
  whatever the policy — the walk descends into the existing record, which keeps its `dir_id` and
  its own `modified_at`, and `policy` goes on applying to what the walk carries inside — so one
  source folder is never split across two records and no policy ever tombstones a subtree the
  user was never shown. **Kinds that differ never replace**, in either direction: `skip` leaves
  the item out, a directory's whole subtree with it; `keep-both` takes the next free name —
  `name (2)`, `name (3)`, …, before the extension for a file and at the end of the whole name for
  a directory, the first that no live sibling holds under case folding — and everything under a
  directory goes there; `replace` fails that one item with `file.kind_mismatch` while the rest of
  the batch runs, because replacing a folder with a file would tombstone its subtree in one write
  (R39) and replacing a file with a folder is not an edit of that file. `FileOutcome` carries
  `IsDir` and, beside `added`, the outcomes `created` (a directory record made) and `entered` (an
  existing directory descended into); a subtree left out is one `skipped` entry, for its top.
  `CheckNames(id, parentID, names) []Collision` lets the UI ask once; `Collision` carries the
  kind on both sides — what is being offered and what is in the way, `Existing` being the record
  id it collides with — so the dialog can say "*Photos* is a file here" and grey *Replace*
  whenever the two differ. `Replace(id, fileID, path)
  opID` is the in-place edit (never Delete + Add). `Delete(id, recordIDs)` — a directory takes its subtree as the merged view has it, tombstoned in
  the same write (FORMAT R39): a record moved into it during this transaction goes with it, one
  moved out before the deletion does not. It is **one** staged change however large the subtree,
  and one row: the directory keeps its place in its parent's listing marked `deleted`, greyed and
  not enterable, and nothing beneath it is listed, previewed or extracted while the deletion
  stands; nothing may be added, created or moved into it or beneath it (`file.not_found`), so a
  live record is never staged under a tombstone. A tombstone is not a live sibling and reserves
  no name (R39 folds names among live children only), so a new record of that name may be made
  beside it, and the sibling check and `CheckNames` ignore staged-deleted siblings. Deleting a
  record whose whole existence is staged un-stages it instead of tombstoning — for a directory
  the records staged under it go with it, and a committed record that was moved into it goes back
  where the move found it, its move un-staged too, so no record is left naming a parent that is
  not there. A committed directory deleted with staged adds beneath it takes them into the
  tombstoning; their bytes are already in the file and the free map reclaims them at the commit.
  There is no per-row undo: `Discard` is what brings a deletion back, with everything else the
  transaction holds — `Rename(id, recordID, newName)` (one record, file or directory; a `/` is refused, and a name that
  folds onto a live sibling of the record's own parent is `file.exists` — a change of case alone
  is not one, since a record is not its own sibling), `Move(id, recordIDs, parentID)` re-parents
  each record, one record written per item whatever subtree hangs beneath it. Both, like
  `CreateFolder` and the adds, are pre-flighted against R39 on the merged view **before anything
  is staged**, and a move batch is refused whole and in place, so nothing the encoder would
  reject is ever staged and the user retries with a name rather than finding half a selection
  moved: a destination that is not the root or a directory live in the merged view — a folder
  staged by `CreateFolder` counts, one staged for deletion does not — is `file.not_found`, as is
  a `recordID` that is not live; a directory moved into itself or into one of its descendants is
  `file.move_into_self`; a name a live child of the destination already holds under case
  folding, or that two records of the same batch would both take, is `file.exists`, naming the
  record; and a moved or created subtree whose deepest live directory would then stand more than
  255 parents from the root, or any of whose records would then join to a path over 4096 bytes,
  is `file.tree_bounds` — those two bounds are the subtree's and not the named record's, so they
  are caught here rather than at the seal (FORMAT R39, which binds the encoder as well). A record
  whose own ancestor is in the same batch travels with that ancestor and is dropped from the
  batch; a record already under `parentID` is a no-op. A rename writes `name`, a move writes
  `parent_id`, both advance `revision` and `last_writer`, and neither writes `modified_at` — a
  folder's time is the folder's own (FORMAT §11). `file.kind_mismatch`, `file.move_into_self` and
  `file.tree_bounds` are per-item codes in the `file.*` namespace, declared here where they are
  used. `Extract(id, recordIDs, dir, policy) opID` — the plan is a **set** of records live in the
  merged view: each selected record, every live record beneath a selected directory, and the
  ancestor directories of all of them up to the root (the all-zero id among `recordIDs` is the
  root and extracts everything — the page's *Extract all* — which makes any other id in the call
  redundant; an empty `recordIDs` is `params`, never everything). A record reached twice is
  planned once, a staged rename or move carries its target with it, and staged adds and replaces
  are not extractable until Save, as with `PreviewURL`. The plan is ordered **parents before
  anything under them**, and a directory is in it because its record is live, never because a
  file needed a parent (DESIGN trap 31): an empty folder extracts as an empty folder, and no path
  is ever inferred into existence. Every target is `filepath.Join(dir, FromSlash(path))`, all of
  them resolved before the first byte, case-folded destinations de-duplicated there, each
  asserted after `filepath.Clean` to lie under `dir` — every record satisfying R20 and R39 does,
  so a failure means the index is not the one the reader validated and the whole operation fails
  with `file.name` before anything is written (§1, fail closed): a plan-time invariant, not an
  item's outcome. `dir` itself is `MkdirAll`ed once; below it each directory is created into the
  parent the order has already made, and an existing folder is used as it stands — never renamed
  (which would fork its whole subtree), never pre-`Lstat`ed, never emptied; a directory that
  cannot be created takes its subtree with it, each record beneath it failing in turn. A file is
  written all-or-nothing into its already-created parent, `skip | rename` on `os.ErrExist` —
  never pre-`Lstat`, never overwrite by unlinking. Each directory **this extraction created**
  then takes its `modified_at` through `os.Chtimes`, deepest first and only once everything
  beneath it has landed; a folder that was already there keeps its own time (DESIGN trap 28 —
  Enfold does not alter what it did not make), and a time that will not set leaves the folder
  `created` with `io` in its `Code`. Directories appear in `Results` as `created | skipped |
  failed` and add no bytes to the progress `Total`, which counts file plaintext only, so a plan
  of folders alone runs with `Total` 0; each file is all-or-nothing, the batch not), `Save(id) opID`,
  `Discard(id)`, `PreviewURL(id, fileID)` (only for committed rows; staged adds and replaces are
  not previewable until Save), `PreviewText(id, fileID, maxBytes) {Text, Truncated}` (over
  `OpenReader` + `LimitReader`; no cross-origin fetch exists). `Stat` carries `CopyMismatch` and the
  list a `Note` of `archive.copy_mismatch` when the file's seq is not the one the registry last
  saw (an older copy restored): shown, never adopted silently; a save records this copy. The archive layer's transaction takes the tree with it: `Tx.Add` and
  `Tx.Replace` carry the record's `parent_id`, a delete collects the subtree from the index rather
  than from the caller, sibling checks are per parent and case folded against the transaction's
  own index (FORMAT R39), and nothing is looked up by a path — there is no whole-name lookup any
  more.
- Events: `archive.changed {ID, Seq}`, `archive.expiring {ID, ClosesAt}`, progress as above.

**Keys**
- `Slots() []SlotView{RecipientID, Type, Label, CreatedAt, Removable}` — `Removable` says the
  invariant would still hold without the slot (`keystore.Removable`, which reads the vault's
  entangled switch as well as the records, FORMAT §6.4), so the page greys *Remove* before any
  ceremony is run for it. The entangled password is the vault's, not a slot's: its row is
  `EntangledState()` (§13).
- `BeginEnroll(kind, label)` runs the ceremony for the VMK and releases the key that unlocked —
  an enrolled hardware key inherits the vault's entangled password and is wrapped from the kept
  `K_P` offline, so no password is asked (§13); a `kind = password` slot asks for its own
  (`Choose`) — and then, for a YubiKey, the token flow: `SwapKey` waits for the unlocking key to be *out* of the reader
  — the one card present is probed for that key's public key (no PIN, no touch), because the
  swap may already have happened during the prompt and a swap in the same port shows the same
  reader name throughout — then for the key to enrol; `Inspect(9d)` → reuse a `Usable` key or `FirstEmptySlot` + management key
  (`ProtectedManagementKey(pin)` after `PINState`, else the hex prompt) + `Generate` — and then,
  reused or generated, **the key proves itself before it is enrolled**: its PIN (not asked again
  when the management key's VERIFY still stands) and its touch, over an agreement with an
  ephemeral key checked against its public key (`token.proof` when it does not match) — so a
  vault never depends on a key that does not work, and a key in the reader is never enrolled
  by merely being there (ruling 2026-09-07, third hardware test) → `AddSlot`. A key's label left
  empty is its serial number ("YubiKey 12345678"), which tells one from another; a password's is
  "Password"; a recovery key needs a name.
  Sub-states mirror §2.2 plus `ManagementKey`. `BeginEnroll(recovery, label)` adds a recovery
  slot and shows its digits: the reveal, below.
- **The reveal.** Every showing of a recovery key — after a create, after enrolling one, and
  `RevealRecoveryKey(recipientID)` from the Keys page — ends its ceremony at `StepRecovery` with
  the one-time URL in `SlotLabel` (§4) and the same dialog on the page, whichever view it is on.
  `RevealRecoveryKey` is a ceremony (`kind: reveal`; the panel's title "Show the recovery key")
  that recovers the VMK through a protector exactly as a slot change does — the YubiKey's PIN
  and touch, or the password; never the recovery key (the ruling: a protector authorises the
  showing) — then unwraps the slot's escrow record (FORMAT R38), checks it against the slot's
  own public key, and mints the URL. What can be refused before any prompt is:
  `vault.slot_not_found` (not an active recovery slot), `vault.setup_needed` (a vault with
  no protector — the reveal would have to ask the recovery key); after the unlock,
  `vault.escrow_mismatch` (the kept key does not open its slot: refused, never shown). It writes
  nothing, but it holds the handle while the VMK is recovered (`keystore.Unlock` is not a read of
  the file's shared handle), so — as for a slot change — it waits for a running save, verify,
  compact or rotation, and a registry write during it owes its receipt; unlike a slot change it
  runs on a tampered vault (the record is in the authenticated registry, and the key it shows may
  be the way out). The dialog offers three ways out, and the page holds the digits no longer than
  the dialog: **save as a text file** — a warning first (a safe, secret place, reachable when it
  is needed: not the vault's own folder, not somewhere that syncs to where it should not), then
  the native Save dialog, then `SaveRecoveryKey(handle, path)`, and the core writes the file
  itself: the vault's name, the way in's label, the date, the 48 digits, what the key is for and
  what it is not; an existing regular file at the path is replaced — the Save dialog asked —
  and anything else there refused; refused as a place are the data folder and everything beneath
  it and, for a vault kept elsewhere, the vault's own folder (`vault.recovery_place`); the file
  is synced before the call returns, and the folder the user chose is its protection (the mode
  is asked for where it means something; Windows gives the folder's ACL); **print** —
  `window.print()` over a print stylesheet that shows the digits, the vault's name, the date and
  what the key is for — and nothing of the app around it, and no page margin, so that the browser
  prints no header or footer of its own — the one browser-provided output the page invokes (§4:
  previews are
  decrypted content and stay without one; a recovery key is meant to leave the machine) — the
  page cannot tell a print from a cancelled one, so a second confirmation follows ("it printed,
  and all 48 digits are legible"); **written down** — a second confirmation ("all 48 digits,
  checked against the screen") before the dialog closes. After a save the core acknowledged the
  dialog closes on *Done*. The handle is the URL's token: the one-time GET consumes the URL,
  not the value, which the core keeps for the reveal's life — until `DropRecoveryKey(handle)`
  when the dialog closes, ten minutes, or a lock trigger, which drops every held key (§2.1) — so
  a save after the digits were shown does not need the ceremony again; a token in a bound call
  is a capability to a value the page already fetched, not the value. Every recovery slot has
  its record from the commit that made it (FORMAT R38, registry version 3 only).
- `RemoveSlot(recipientID)` (refused with the invariant's reason), `RotateNow()`,
  `ExportBackup(path)` (which records `lastExportAt`, §13; the "Export" of earlier drafts),
  `VerifyBackup(path)` (§2.1: a staged copy opened with the
  recovery key, nothing kept). What a backup is and whether it is this vault is
  `Vault.InspectFile`; restoring a single archive record from a backup (`RestoreArchiveRecord`,
  re-wrap under the current KWK) is deferred (§12).
- **Slot changes and registry writes never share the handle.** While a slot-change ceremony
  runs, a registry write is refused with `ceremony.in_progress` — a Save still commits its
  archive and owes the receipt, paid when the ceremony ends — and a slot change does not start
  while a save, verify, compact or rotation is running (`op.in_progress`). A pending touch left
  by a cancelled slot change or reveal (§2.2) is that ceremony still running, to this rule and
  to every other guard, until the card answers. A lock trigger in any state latches and cancels
  the ceremony, and the lock's close of the handle waits for it — and for the pending touch.
- **Tampered is a state, not a banner**: every mutating call and Export is disabled with the
  reason; the one action is importing a copy of this vault (§2.1); it is never cleared silently
  (R25). The reveal stays: it changes nothing, and the way out of a tampered vault may be the
  key it shows.

**Settings** — `Get()`, `Set()`. Machine-local, in `%LOCALAPPDATA%\Enfold\settings.json`
(temp-then-rename): the vault's path when kept elsewhere (empty: `vault.eks` in the data folder)
and its display name, close-to-tray behaviour, theme, look, recovery
record percentage, dictionary threshold, and the last export (`lastExportAt`, §13).
**Security-relevant values live in the authenticated
registry, not the file:** the idle and absolute minutes (`Registry.IdleMinutes`,
`AbsoluteMinutes`, zero = default) and the per-archive compression choice
(`ArchiveRecord.Policy` bit `no_compression`); `Compress.Padding` rides with it.

**Shell** — `ShowWindow`, `CloseWindow`, `PickFiles`, `PickFolder`, `SaveFile(title, filename,
dir)` (`dir` empty leaves the folder to the shell; the archive create passes `lastArchiveFolder`),
`Reveal`, `Quit`
(names the unsaved changes in a native Yes/No question — the only buttons a Windows message box
has — then `ResolveForShutdown`, then `app.Quit()`; never asked twice). A cancelled native file
dialog is "nothing chosen", never an error; a submitted secret that found no prompt is reported
back as the `secret.refused {Code}` event. Tray and menu callbacks run on the Wails main thread
and leave it (a goroutine) before touching the window or a dialog. The service lives in `internal/app/api` like the others and holds the Wails
calls behind an unexported `Hooks` value the shell supplies, so nothing of Wails is reflected. File
drop: the window is created with `EnableFileDrop`; the shell re-emits the dropped paths and the drop target's `data-archive-id` / `data-dir-id` —
the id the page is showing, from `Page`'s `dirID` and `Crumbs`, never a path — to the frontend,
which calls `AddFiles(id, parentID, …)` for the dropped files and `AddFolder` for each dropped
directory; the core validates that the archive is open and that the id is the root or a
directory live in the merged view (a folder staged by *Create folder* counts, one staged for
deletion does not), answering `file.not_found` otherwise, and refuses loudly. The target carries
an id and never a name for that reason: a stale id is refused, where a stale path would resolve
to whatever folder now happens to carry that name (FORMAT R39, DESIGN trap 31). Dropped paths carry no authority beyond what a file dialog would.

## 4. The preview server

A loopback `net/http` server on `127.0.0.1:<port>`, **bound once for the process lifetime**
(the CSP names the port and is fixed at document load), serving
`GET /p/<archive-token>/<fileID>` with `http.ServeContent` over one `archive.Reader` per request,
`Cache-Control: no-store`, `Content-Security-Policy: sandbox` (a preview is never a document
that runs), `Content-Type` from the name, multi-range refused (single range or
none, so the handler may `Close` its Reader on return). **The preview transport outlives the
session; it dies with the last open archive, and a live reader is archive activity.** The token
is per archive (an unknown token is a 404 with no archive id in the URL); closing the archive
drops the token and `Archive.Close` fails every in-flight body. A lock changes nothing here.
Justification: it serves only archives whose keys are in this process's memory, which a
same-user process reads regardless (DESIGN §2); the unguessable path is the access control.

**The page's CSP is a response header from the asset middleware** (production only; in
`wails3 dev` Vite's HMR needs its own origin):
`default-src 'self'; img-src 'self' http://127.0.0.1:<port>; media-src 'self'
http://127.0.0.1:<port>; connect-src 'self' http://127.0.0.1:<port>; script-src 'self'; style-src 'self'; object-src
'none'; base-uri 'none'; form-action 'none'; frame-src 'none'`, — `connect-src` names the loopback because the recovery key's one-time URL (§1) is fetched from the page, and a CORS preflight on that URL must not consume the secret — plus
`Permissions-Policy` denying camera, microphone, geolocation, sensors, display-capture,
clipboard, midi, local-fonts, window-management. The secret route serves the reveal's handle
too (§3 Keys): the URL and the value live ten minutes; the one GET consumes the URL, and the
value stays behind it for `SaveRecoveryKey` until it is dropped, a lock trigger, or the ten
minutes pass — a second, a foreign or a late fetch ends it as well, since the page fetches
once. **No preview surface may expose a
browser-provided save or print affordance** — the recovery key's dialog is not a preview surface,
and its `window.print()` over a stylesheet that prints the key's sheet and nothing of the app
around it is the one browser output the page invokes (§3 Keys): the window is created with
`DefaultContextMenuDisabled`, every WebView2 permission kind is set to Deny
(`WindowsWindow.Permissions`, autoplay excepted if it breaks click-to-play), and the `<iframe>`
PDF viewer is not used (its toolbar cannot be hidden in beta.16 and there is no download hook):
PDFs offer Extract only in 1.0; pdf.js rendered to canvas is the later option.

**WebView2 disk cache (DESIGN trap 13), honestly:** beta.16 exposes no cache-disable or
in-private option, and Chromium flags are not supported in production, so `no-store` on every
preview response is the whole defence. The user-data folder is `%LOCALAPPDATA%\Enfold\WebView2`,
swept and recreated at startup before the first window and deleted best-effort at exit;
nothing clears it at lock. Inspecting that directory after a preview is a release gate.

## 5. Lock triggers on a zero-window process

The shell creates one **message-only** window of its own on an OS-thread-locked goroutine with
its own message loop (`golang.org/x/sys/windows` + `syscall.NewCallback`, with a top-level
`recover`): `WTSRegisterSessionNotification(NOTIFY_FOR_THIS_SESSION)` — checked; on failure a
bounded backoff retry, a warning code "workstation-lock detection unavailable", and a ~5 s poll
of `WTSQuerySessionInformation(WTSSessionInfoEx).SessionFlags` as the substitute —
`RegisterPowerSettingNotification` for `GUID_SESSION_DISPLAY_STATUS` and
`GUID_SESSION_USER_PRESENCE` (unregistered at shutdown), `WM_WTSSESSION_CHANGE` handled for
lock, logoff, console/remote disconnect and remote control; `PBT_APMSUSPEND` comes from Wails'
own application event. A message-only window receives only what is addressed to it — the
registered WTS and power-setting notifications; broadcasts such as `PBT_APMSUSPEND` and
`WM_QUERYENDSESSION` never reach it, which is why suspend comes from Wails and logoff from WTS.
When WTS registration fails the poll reads `WTSINFOEXW.Data.WTSInfoExLevel1.SessionFlags` (at
offset 16: the union is 8-byte aligned) and treats the unknown state as no answer. Every handler
calls `LockNow` synchronously.

**Shutdown.** `Options.ShouldQuit` never shows UI. `Options.OnShutdown` runs
`resolveForShutdown()`: for each dirty archive `Commit` under a fresh ~2 s context, write each
receipt, close the archives, then lock — bounded to ~3 s in all; on timeout the transaction stays
unpublished, which the format tolerates. The tray's Quit asks the user first, then takes the
window and the tray away, runs the same function, and waits — unseen — for a pending touch
(§2.2) to end (`AwaitPendingTouch`: the card's own answer, about 15 s, then its release), so
the card is released and reset by this process; only then does the process end. The wait was
once inside the shutdown hook, with the window still up: fifteen frozen seconds, which is what
the hiding is for. `WTS_SESSION_LOGOFF` runs the resolution without asking and does not wait
for a touch: the OS gives a process seconds at logoff, and Windows resets a dead client's card
at the cleanup of its connection (DESIGN trap 27) — after the in-flight command has run out,
during which no program can reach the card anyway.

## 6. Screens (the Native look)

Lock screen (one panel, three-step line, the states of §2.2 including TwoKeys, NoMatch, Busy,
Blocked; `SwapKey` is the enrolment's alone since §13; on a vault whose entangled password is on,
the token way in's card 2 is **Password and PIN**: two fields under one *Continue*. The PIN goes
first and is verified at the card before anything else (§2.2): a wrong PIN marks only the PIN
field, with the retries, and nothing else moves — the password field keeps whatever was typed, as
an ordinary masked field the user may still edit, and is sent only once the PIN is right, in the
state it is in at that moment, by the page and with no further click. A wrong password, known
only after the touch, comes back with the password field emptied and marked and the PIN not
asked again (§2.2's cached `H`). The button reads *Unlock with YubiKey* whatever the switch, and
the card's dim line says both are needed. The same two-field form serves every token ceremony on
the Keys page. Secondary: recovery key, password, open a
backup — never a second vault; the
"workstation-lock detection unavailable" and BitLocker warnings; while the status says
`pendingTouch` from a cancelled unlock, one quiet line under the key card: the key is still
waiting for the touch that was cancelled — unlock again to pick it up, or touch it or pull it
out to end it; that line is the lock screen's only: on the Keys page the pending touch left by a
cancelled slot change or reveal is said by the next ceremony's own `token.pending` note while it
waits, and on the first-run card a create or an import begun while a cancelled create's touch
stands is answered with `token.pending`, a toast). Archives (list with details
pane, commands Open / New archive / Compact / Rotate key / Verify / Hide and the inspector's of
§13 — *New archive* asks the name and the compression method, a five-segment control (Store ·
Fast · Normal · Better · Best, Normal preselected; *Store* keeps every file as is, and at every
other level already-compressed media is detected by sampling and stored raw by itself, DESIGN
§9), and on *Create* opens the native Save dialog with `<name>.efd` prefilled in the folder last
used (`settings.json`, `lastArchiveFolder`); a chosen path where a file already exists is refused
in place — "A file is already there. Enfold never overwrites; choose another name." — whatever
the dialog's own replace prompt said; a cancelled dialog creates nothing — the Tampered state, the status strip with the countdown and Lock). Archive (the breadcrumb drawn from `Page`'s `Crumbs` alone — ids, never a path — its first crumb
the archive's name and its last the folder being shown, paged table with pending markers, preview pane — image, video, audio through the
loopback URL, text through `PreviewText`, everything else "Extract…" — pending bar, the toolbar:
one *Add* button whose menu holds *Add files*, *Add folder* and *Create folder* (a staged
directory record, written at save whether or not a file was added into it — FORMAT R39), *Extract all* (the whole archive, whatever is selected — the pane's *Extract…* is the
selection's), *Rename*, *Delete*; a drag of the selection onto a folder row or a crumb is `Move`,
refused in place with the reason (§3) and never a half-moved selection; a click on the list's blank area clears the
selection; the name column takes the width the others do not need, so a name is never squeezed
while *Stored as* stands empty — Size, Stored as and Modified are fixed and Modified goes first
when the pane is narrow; drag-and-drop, the expiring prompt, the locked banner). Keys & backups (slots, Add a key,
Remove — greyed while the invariant would refuse — Rotate now (its dialog says every way in is
rewrapped here and now, and asks for a backup first, §13), the entangled password's row (§13),
*Show recovery key…* — shown only while a recovery
slot is selected — backups with the R35 date,
the damaged copy when one is kept, session and appearance settings). First run (create, or
import a vault or a backup; a file that is only a backup leads into *Finish setting up*, and so
does a vault left half set up; a vault whose file is present and refused offers *try again*,
import and *rebuild* — the create dialog in its rebuild form). Dialogs: new archive (path, name,
compression), enrollment, the recovery key's (below), remove slot, rotate, progress, extract
destination and policy, collisions, errors, import.

**The import dialog** (lock screen and first run) picks the file, shows what it is — vault or
backup, its date, its ways in by kind, whether it claims to be this vault and newer — and says
what will happen before the button is pressed: a vault is copied in and must unlock with one of
its own ways in (chosen here: "prove it with"); a backup is copied in and opened with its recovery
key, then takes the first way in (the kind, a name for a key — never a secret); with a vault kept
here, a confirmation names the vault being replaced and its date, says it is kept as a dated copy
whose ways in stay valid, and, for a backup, that archives added since its date are not in it —
the button stays disabled until it is ticked. "Use the file where it is" is an advanced link for
a vault, with the warning that removable media and network shares are outside Enfold's
protection; choosing it turns the confirmation into "switch to this file — it stays where it is".
With archives open, both dialogs say so and their buttons stay disabled. The create dialog shows
where the vault will live and offers "keep it elsewhere" as a link, not a field; in its rebuild
form (§2.1) the place is pinned to the damaged file's — no link — and the tick "keep a copy of
the damaged file and start a new vault" is the one consent (cleared each time the dialog opens);
over a file that is simply absent it shows no replacement bar and no tick, since nothing is
retired; over some other file already at a chosen place it names the dated copy that keeps it.
A create ends in the vault with the recovery key's dialog over it; only when the created vault
did not open on its own does the lock screen say "Created, but it did not open on its own.
Unlock it with the way in you chose"; a ceremony that fails without parking is shown on the
first-run card as well. The
first-run
screen says to have the recovery key ready before importing a backup, because a backup opens
with nothing else; when a configured vault could not be opened it names the file and offers
"try again" and import before "create" (the file is absent) or "rebuild" (the file is present
and refused: the dialog is titled *Rebuild the vault*, says the file is kept beside the new
vault as a dated damaged copy and that the archives it held the keys to open only with it, and
its tick reads "keep a copy of the damaged file and start a new vault"); when a replaced copy
exists and no vault is kept, it names the copy and offers to import it. With a vault kept here
the lock screen offers import and a backup check, never a second vault. A vault with only its
recovery key shows *Finish setting up* as its one action, with "unlock with the recovery key
only" beneath. Precedence on the lock screen: Busy, then Broken, then setup needed, then the
ways in; Tampered is a banner over any.

**The recovery key's dialog** — after a create, after enrolling a recovery key, and after *Show
recovery key…* on the Keys page — shows the eight groups and three ways out (§3 Keys): *Save as
a text file…*, which first says what place to choose and then opens the native Save dialog;
*Print…*; and *I have written it down*. The last two ask once more, in a second dialog over the
first with a distinct button, never a tick beside the same one: "Did it print, with all 48
digits legible?" / "Have you written down all 48 digits, checked against the screen?" — *Go
back*, or *Yes, I have the page* / *Yes, I have it*; after a save the core acknowledged the
button is *Done*. It says the
key can be shown again from the Keys page — unlock, then prove a YubiKey or a password once
more — and, when the fetch of the one-time URL fails (a window recreated after the one fetch),
that the key can be shown again from there, with *Done* as the one way out. It is not dismissed
by Escape or the backdrop, and it closes when a lock trigger locks the vault it was shown on.

**The create and enrol dialogs name the key, never the secret.** A token gets "Name this key"
(the label shown in the list of ways in); a password way in has no name field — its slot is
"Password" — and the dialog says the password is chosen in the next step, because a typed
secret never rides a bound-method argument (§1). Ticking "also require a password with this
key" says the password is chosen first, before the key is set up.

**Forms and their errors.** A field that holds something wrong is marked once the user leaves it
— the underline turns red and, beneath it, either the requirement already shown there turns red
("At least 8 characters.") or a line says what is missing — and the mark goes the moment the
value is right, to return only after the field is left again with something wrong. Pressing the
form's button marks every field that would refuse, an empty required one with "This field is
required."; the button is never disabled for an empty field, only for a consent not yet given
(a replacement's tick) or a state that forbids the action (archives open). The lines fade in and
out like everything else. Every typed secret is judged before it is sent — a chosen password's
minimum of 8 characters (BitLocker's rule; the core refuses a shorter one at submit with
`vault.password_short` and keeps the prompt), the recovery key's 48 digits with any whitespace or
dash between them (the set the core ignores), the management key's hex with the whitespace the
core ignores — and an existing secret is never measured: it is what it is, so a PIN is refused
only for what the card itself refuses (more than 8 bytes), never for being short. **The
recovery key is typed into eight cells**, plain digits — they are read back against paper, not
hidden — each moving on when its six are in; a paste fills them all with the dashes stripped; a
click lands on the first cell still to be typed, or, once all eight are in, where it was aimed
with the group selected for retyping; the cells are one tab stop; a finished group that fails
BitLocker's checksum (divisible by 11, below 720 896) turns red at once, before anything is
sent; and when the core says the key did not open the vault, the digits come back for
correction — the store keeps them across the derivation step, which unmounts the field.

**Motion.** One vocabulary, in `lib/motion.ts` and the stylesheet's `--dur-*` tokens, kept
equal by hand: *tap* 90 ms (press feedback), *hover* 120 ms in and 160 ms out (a control
answers at once and settles when left), *leave* 100 ms, *fast* 140 ms (enter), *move* 220 ms
(the only long one: a change of scene), *settle* 320 ms (the one deliberate pause, below),
*gap* 60 ms (what arrives waits this long for what leaves). Entering eases out
(`cubic-bezier(0.2, 0, 0, 1)`); a thing that travels away eases in; a crossfade's exit eases
*out* — fast first, 80 ms for a page — so that no two texts share a spot at readable opacity,
which is what a ghost is. Nothing travels more than 12 px; one thing moves at a time, staggered
by at most 40 ms; nothing loops but the touch rings.
`prefers-reduced-motion` zeroes every duration — the script reads the media query when a
transition starts, the tokens collapse to 0 — and the settle with them.
- *Unlock.* At Done the third card stays the touch's green and becomes the success card: a
  white disc with the accent's check pops in where the rings were (the core scales from 60%
  with a little overshoot, 240 ms), the word — *Unlocked* — sits in the middle, the vault's name
  under the open padlock at the foot; it stays for the settle, so the ceremony has a full stop
  rather than a cut to a white card. Then the lock screen lifts
  (8 px up, fading fast-first, 160 ms) and, a gap later, the shell arrives: the rail slides in
  12 px from the left and the layer rises 8 px, 220 ms, the layer 40 ms behind. The store holds `settling` for the
  settle after the state says Unlocked, and the lock screen keeps rendering the ceremony
  meanwhile — the state's event lands a few milliseconds before the ceremony's Done, and without
  the hold the screen fell back to its first card for that instant and the check was never seen.
- *Lock.* The reverse: the shell sinks 6 px and fades fast-first (120 ms) and, a gap later,
  the lock screen rises in (220 ms); the lock screen's header shows the open padlock and closes it over the first 160 ms.
  While unlocked the status chips show the open padlock.
- *Pages.* The outgoing page fades in place, fast first (80 ms); a gap later the incoming
  fades and travels 160 ms — from the right 10 px going into an archive, from the left 10 px
  coming back, up 6 px for a rail switch — in the old one's place, the layer being a grid so
  nothing jumps. The layer's foot is not part of the page and does not travel with it: one bar
  below the pages (`LayerFoot`), the page's note on the left — set by the page
  (`store.footNote`), faded in when the page changes and updated in place otherwise — and the
  lock state on the right, always: the open padlock, *Locks in m:ss* and *Lock now* while
  unlocked; the closed padlock and *Unlock* while locked with archives still open.
- *Rail.* Hover tints in over 120 ms and out over 160 ms; a press is instant (`--ctl-press`,
  the text to `--ink-2`); the current item's accent bar grows from its middle (180 ms) and the
  previous one's shrinks.
- *Menus.* The `<select>` stays native and its picker is styled (`appearance: base-select`,
  Chromium 135+; WebView2 152 here): the list appears whole, falling 4 px and fading in over
  140 ms, rows tint on hover (120 ms), the chosen row wears the accent wash while the list
  fades out (100 ms). An older runtime ignores the declaration and shows the system popup,
  unanimated and unchanged.
- *The save bar* (`SaveBar`, the Settings page first; every page that stages edits uses it
  as it is). Edits are staged, never saved on change: the page shows saved ⊕ draft and *derives*
  what is pending by diffing the two — dirty is never stored, so an edit put back by hand
  un-dirties itself; only what the user may change counts (a timeout while locked does not).
  While nothing is pending there is no bar. While something is, a frosted bar (the surface at
  88% over a blur — enough to read through, not enough to read the content behind it — with an
  inset hairline so its edge reads) rises 8 px and fades in (fast) at the
  foot of the scroll pane — in flow after the content and sticky 12 px above the pane's bottom,
  so it can never cover the last row for good, nor the rail — and sinks 8 px and fades out
  (leave) when the last edit is reverted, discarded or saved. It names the changes rather than
  counting them: an accent count pill, then as many chips as fit — "Idle lock · 5 minutes";
  "+ label" and "− label" in the accent wash and the danger wash where a change adds or removes
  something — the rest folded into "+N more…", a click on which opens a popover of exactly the
  folded ones (fast, scaled from 97%; leave). Then *Discard* and the accent *Save changes*.
  Invalid input greys Save and says why in one red sentence at the bar's left, with the bar
  framed in the danger line; the field is marked in place too (`aria-invalid`), so the greyed
  button is never the only signal; Discard stays enabled. Saving swaps the label to *Saving…*
  and disables both; no spinner. Success is the bar leaving — nothing else says so; failure is
  the page's toast, and the draft stays, so Save can be pressed again. Discard is one
  action, instant, unconfirmed. Ctrl+S saves while the bar is up and valid. The draft survives a
  visit to another page (the store keeps it). Validation is delta-aware where that applies:
  only a problem the edit introduced blocks Save; one that was already there is shown, not
  wedging an unrelated change. The page's controls press the way every control does; the
  bar's buttons dim to 85% while pressed.
- *Responsive.* The window never goes below 880 × 560. A page's two columns merge when the
  layer's body is narrower than 840 px, and a settings row stacks — the title, its line, then
  the control on a line of its own — when its card is narrower than 470 px; both are container
  queries, not viewport ones, so a narrow column stacks its rows while a wide one keeps them
  side by side. The save bar wraps its chips onto their own line at the same width. The Archive
  page's file table is the same kind of rule: it drops *Modified* at 840 px and *Stored as* at
  700 px, so the name column always keeps room.
- *Controls.* Every dialog and popover enters over 140 ms (the box also scales from 97%) and
  leaves over 100 ms; a panel that swaps its content in place — the ceremony panel between
  steps, the lock screen's three cards — fades the new content in over 140 ms and the cards
  ease between live and dim. *Toasts* are for what the page cannot say in place — an error the
  action's own surface is gone for — never a confirmation of something the page already shows
  (a saved setting is the bar leaving). Where they go, ruled 2026-09-07 for whatever toasts come
  next: at the top of the window, centred on the window — not on the content layer — dropping
  in a little as they fade (a few pixels down, never sliding in from the top edge) and leaving
  upward as they fade out; `Toasts` does this, 14 px from the top, centred on the window's
  width, the text centred and balanced. A link-style button darkens on
  hover (`--accent-ink-hover`, a step past `--accent-ink`; brighter in the dark theme, where
  contrast goes the other way) and thickens its underline; a button's press is instant and dims
  its text. A slider's label sits close beside it, fixed in width, and says the number alone,
  so nothing shifts as the value changes; what the number means — the recommendation, what 0
  does, what it applies to — is the row's line, never a suffix that comes and goes ("Recommended:
  3%; 0% is off. Applies to exports made from now on."). No tooltip and no tick: the label is
  the value, and a lone tick reads as a stray mark. A closed dialog answers no key or click
  while it fades: Svelte marks the element inert the moment the outro is committed
  (synchronously, before any frame) and clears that if the dialog is reopened mid-fade, and
  the handlers check `inert`.

## 7. Frontend

Svelte 5 + TypeScript on the Wails template, Vite; `@wailsio/runtime` pinned to the exact Wails
version, `package-lock.json` committed, `npm ci` in the Taskfile; the template's font and
background assets removed; no remote resource; Segoe UI Variable from the system. Bindings are
generated (`wails3 generate bindings`) and committed. Strings in one table.

## 8. Memory hygiene

The session's keys live in `keystore.Session` as Go slices zeroed on lock; memguard enclaves
for them are a keystore change proposed as a follow-up, not part of this layer. Crash dumps:
`SetErrorMode(SEM_NOGPFAULTERRORBOX)`; WER's LocalDumps policy is outside a user-mode process's
control and is documented as the limit. Destroying the window on close destroys the renderer's
copy of what was on screen.

## 9. BitLocker

Two checks — the system volume and the vault's volume — each tri-state (Protected /
Unprotected / Unknown); only Unprotected warns (`system-volume-unprotected`,
`vault-volume-unprotected`); Unknown is a quiet line. The predicate is *protection status*
(suspended BitLocker counts as unprotected). **The unelevated mechanism is unverified**: the WMI
`Win32_EncryptableVolume` class is documented admin-only, and the least-privilege ruling forbids
elevation. The candidate is the shell property `System.Volume.BitLockerProtection`; it is
measured before the feature is promised (`SCOPE.md` keeps the bullet provisionally).

## 10. Testing

The core is tested without Wails: it takes app-side interfaces `Cards` (`Readers`, `Open`) and
`Card` (`Keys`, `PINState`, `Inspect`, `FirstEmptySlot`, `ProtectedManagementKey`, `Generate`,
`Token` returning `keystore.Token`, `Serial`, `Close`) with app-side `Prompter`/`PINStatus`/
`KeyInfo` types, so the ceremony and enrollment drivers run against a fake that covers every
`piv` error the states depend on; one windows-tagged adapter is the only importer of
`internal/piv`. Lock triggers are an interface the shell implements and the tests drive. Keystore
and archive are the real packages over temp files; timers take a clock. The shell is exercised
by hand in `wails3 dev` with `ENFOLD_DATA_DIR` set to a throwaway data folder — which now holds
the vault too (§2.1), so a throwaway folder is a throwaway vault — instead of `%LOCALAPPDATA%\Enfold`. Frontend tests cover the copy mapping of
ceremony states and the "never 0 attempts" rule.

## 11. Format changes this layer needs (FORMAT.md §7)

- `ArchiveRecord.policy` bits: bit1 `hidden`, bit2 `no_compression`.
- `ArchiveRecord.last_seq u64` (the archive superblock `seq` of the last commit the registry
  recorded; identity without a key) and `hash_at_seq u64` (the `last_seq` at which
  `last_ciphertext_hash` was computed; equal means fresh).
- `Registry.idle_minutes u16`, `absolute_minutes u16` (zero = default).
- `registry_version` 3 (Revision 2, §18.2): the secrets section (§7.6) under `KWK_secrets` (R3)
  — recovery escrow, the entangled password's key, the VMK history — and `description` and
  `forgotten_at` on the archive record; no older version read.

## 12. Deferred, and open for the user

pdf.js preview; memguard for the session keys; an entropy estimate for a chosen
password (SCOPE: "with an entropy estimate shown"; the minimum of 8 stands in for it);
(folder move is `Move` since the tree, §3);
`overwrite` on extraction (an archive-layer change); an unelevated BitLocker check (measure
first — if none exists, the SCOPE bullet or the least-privilege ruling has to move); the
permitted range of the timeouts beyond the clamps.

## 13. Ruled 2026-09-07, critiqued the same day, to implement (with FORMAT.md Revision 2)

**The Archives page is the vault's inspector.** It lists registry records, not files, and it
grows into the full view: columns *Name* (the description as a muted second line), *Size*
(`last_stored_size`, the registry's last observation of the file), *Files* (open archives only),
*Last saved*, *Key vN*, *Status* (open · dirty · file missing · hidden · forgotten); a header
line for the vault — archives and their stored total summed by the page over the rows, the vault
file's size (`VaultStatus.VaultFileSize`), the registry's `ModifiedAt` (§2.1); sorting by column
and a filter box. The details pane keeps what it shows and adds an editable *Description*,
*Rename* (the registry's trusted name, FORMAT §7.4), the full `last_path` with *Show in
Explorer* or *Locate…* — worded "last seen on another system at …" when the path's syntax is not
this platform's — *Created*, the current *KID*, and *Details…*, a modal with the versions table
(KID · created · retired · state), `archive_id`, revision, last writer, `last_seq` /
`hash_at_seq`, the ciphertext hash, the policy bits, each with *Copy*. The pane's data is
`Archives.Details(id)` (below), read when the selection changes, not when the modal opens.
Tooltips only where text is cut short. There is no raw archive-key reveal: nothing opens an
archive from a bare key, and the way to hand one archive to someone is a later *export one
record as a small keystore*, the precursor of sharing.

*file missing* is the `Note` code `archive.file_missing` and is never computed inside `List`:
the core keeps a presence per record, refreshed by a pass after unlock and by the page's *Check
files* action (`Archives.CheckFiles()`), one path at a time with the state mutex released and a
2 s budget each, the probe abandoned on expiry so a path that does not answer is never reported
missing. Only paths on a local fixed volume of this machine are probed; a path whose syntax is
not this platform's, a removable or network drive and every UNC path are left unmeasured and
are checked only for the one record the user acts on — `last_path` reaches the registry from
*Import records…* as easily as from *Locate…*, and opening `\\host\share` because a record says
so is an outbound authentication to a host someone else named. Until a pass has run the column
is blank, never "missing".

**Forget and delete.** *Forget key…* drops the record softly. The archive is closed first,
unsaved changes asked about as for a delete; then `forgotten_at` is set (FORMAT §7.1, §18.2) and
the record shows under *Show hidden* as "forgotten — restore it to open this archive again; its
key is dropped at the first unlock after <date>" with *Restore*, which clears `forgotten_at`.
While forgotten the record still holds the keys, so `Open`, `Rename`, `SetDescription`,
`RotateKey`, `Verify`, `Compact`, `Locate`, `Hide` and `Unhide` are refused with
`archive.forgotten` — never `archive.not_found`, which stays the answer for a record already
purged, so the page can tell "restore it" from "its key is gone" — and no registry write ever
updates a forgotten record. *Restore* and *Delete archive…* are the two actions that still work
on it, and a second *Forget* does not move `forgotten_at`: the retention clock never restarts.

*Delete archive…* is Forget plus the file (DESIGN trap 28). The file at `last_path` is opened
once and removed through that same handle, never by the name a second time, and never with a
handle this process is holding — the open archive is closed first, refused with `archive.busy`
while an operation or a preview reader is live. Four outcomes, and only two touch the record.
**`archive_id` matches**: the file is removed first, and only then is the record forgotten; if
the removal fails the record is *not* forgotten and the page says the file could not be removed
(`archive.delete_failed`). **Demonstrably not this archive** — the parent folder opened and the
leaf was not in it, or the envelope parsed and holds another `archive_id`: nothing is removed,
the page says so and asks once more before forgetting. **The path could not be reached** — the
volume, share or folder is not there: `archive.file_unreachable`, nothing removed, nothing
forgotten; *absent* is decided on the parent folder and never on the open of the file itself.
**Anything else** — the file is held, access denied, a short read, a wrong magic, an envelope
checksum that does not match: `archive.busy` or `archive.invalid`, **nothing removed and nothing
forgotten**, with *Retry*, *Locate…* and *Forget the key only* offered, because a record dropped
against a file that could not be read destroys the keys of an archive that is still intact. The
Archive page's own *Delete archive…* is the same action on the archive it shows, closing it
first — asking about unsaved changes — and is disabled while the vault is locked with the
archive open (§2.3).

Both confirm by the archive's name typed (compared trimmed and exactly, case and all), and the
warning says only what is known here: with a `lastExportAt` for this vault at or after the
record's `created_at`, "the last backup of this vault was made on <date>; if you still have it,
it holds this key"; otherwise "no backup of this vault is recorded here — every other copy of
this archive becomes unopenable when the record is dropped". No ceremony: the registry write
needs the session's key, not the VMK, and the brakes are the typed name, the retention and the
backup.

**The purge has one trigger.** Forgotten records are dropped at the end of a successful unlock
of the vault kept here, before any archive is opened, in one registry write of its own whose
`modified_at` decides (FORMAT §18.2) — beside, not inside, the write that pays the owed receipts,
since the two fail differently; a purge that would drop nothing writes nothing, so an unlock does
not restamp `modified_at` — and `archives.changed` follows with a line naming what went. No other
registry write purges, so nothing is dropped while the vault is locked and no
Save on one archive destroys another's keys; the unlock of a staged copy — `VerifyBackup`,
`InspectFile`, `InspectRecords` — purges nothing. Every record while the vault is Tampered is
skipped and left to the next unlock.

**The entangled password is the vault's** (FORMAT §3.1, §18.1). `VaultStatus.Entangled` is the
slot region header's switch, known while Locked; the lock screen asks for the password once,
before the PIN, whenever it is on — the standalone-password and recovery ways in never ask,
whatever the switch says — and the typed password lives with the attempt (§2.2), so a cancel, a
retry and an adopted pending touch neither lose it nor ask again. The Keys page shows one row,
*Entangled password: on/off · Change…*, backed by `Keys.EntangledState() {On, CanEnable,
Reason}`: turning it on asks for the new password twice, changing it asks for the new one twice,
turning it off confirms; each is a ceremony for the VMK by any way in and then re-wraps every
hardware slot offline from the kept `K_P`, drawing a fresh `entangle_salt` (FORMAT §6). **The
old password is not asked** (the ruling of 2026-09-07): whoever reaches the VMK — by the
recovery key or the standalone password as much as by a token — can already enrol a way in of
their own, so asking the old password there would be friction and not a guard, while the
password's purpose, a second factor against a stolen or broken token, is untouched: a stolen
token does not reach this page. The critique's alternative — the old asked once, checked offline
for one Argon2id run against the kept `K_P`, with a *Forgot it?* path that turns the password
off through the removal ceremony — is recorded in DECISIONS should the ruling be revisited.

Turning it on is refused where the slot invariant would break (FORMAT §6.4): in a vault whose
active slots are all hardware keys, one password would then stand in front of every way in, so
`Keys.SetEntangled(true)` is refused for the invariant's reason before the ceremony starts and
before a password is typed, worded "every key would then need this password — add a recovery
key first" with the action that runs `BeginEnroll(recovery, label)`; `CanEnable` greys the
switch ahead of any ceremony, as *Remove* is greyed from `SlotView.Removable`. The same
predicate from the other side: with the password on, `Removable` is false for the last recovery
slot when every other active slot is a hardware key. Turning it off is never refused. Enrolling a
key asks for no password — an enrolled key inherits the vault's setting and is wrapped from the
kept `K_P` offline — except the first way in of an adopted backup, which sets the vault's
entanglement afresh, a backup carrying no `K_P` (FORMAT R28). The rotate dialog loses its
"except a key whose entangled password…" clause: every way in is re-wrapped, always.

**Merge records.** Beside *Import* (which replaces the vault kept here) an *Import records…*
action opens a backup or a vault over a staged copy and proves it before anything is listed.
`Vault.InspectRecords(path)` is a ceremony for **this vault's** VMK by any way in — the VMK is
what derives `KWK_secrets` and so the retired VMKs (FORMAT §7.6) — and it tries the current VMK
and then each `vmk_history` VMK against the incoming registry, newest first by the file's own
`vmk_generation`, which orders the attempts and decides nothing (FORMAT §18.2); each attempt
derives that VMK's Metadata key and decrypts the incoming registry at *its own* offset, length
and nonce with the AAD built from *its own* superblock fields. A file that opens this way needs
no key from the user; anything else — another vault, or a backup from a *later* generation,
which happens when this vault was rolled back to an older copy — asks for that file's own
recovery key, and the dialog says which before the button is pressed, naming each recovery slot
by its ID (below). A file that no VMK and no key opens is reported as *not this vault, or from a
generation this vault no longer keeps*, never as corruption. The ceremony re-wraps what it read
under this vault's `KWK` and returns a handle with the record list; the handle lives until
`MergeRecords` consumes it, the dialog closes (`Vault.DiscardRecords(handle)`), or a lock trigger
drops it with every held key (§2.1). `Vault.MergeRecords(handle, ids)` is then an ordinary
registry write on the session, refused with `vault.archives_open` while any archive is open,
`ceremony.in_progress` while a ceremony runs and `op.in_progress` while a save, verify, compact
or rotation runs.

The records are listed with checkboxes, ticked by default: a record whose `archive_id`, KID and
key the vault already has is skipped and listed greyed as "already here"; a known archive with
a new KID gains that version (the file's envelope decides which is current); a new `archive_id`
is added; a record this vault holds as forgotten is listed unticked as "forgotten here on
<date>" and ticking it is an explicit *Restore*. Fields are never taken by counter alone: version
lists union; a field the local record leaves empty is filled; a field the two hold differently —
`name`, `description`, `policy` — keeps the local value and the row says what differed ("named
'Photos 2023' there"), except `always_require_full_auth`, which is set if either side has it and
cleared by neither; the fields that describe a *copy* — `last_path`, `last_seq`, `hash_at_seq`,
`last_ciphertext_hash`, `last_stored_size`, `last_written_at` — are the other vault's observation
of its own file and are never taken over the local ones. Every record the merge changes is
written with `revision = max(local, incoming) + 1` and this vault's `device_id` as
`last_writer`, so a merge in the other direction sees a descendant rather than a rival
(SYNC.md §5).

**The rotate dialog** shows the last backup's time and, past a week or with none, asks for a
backup first — it asks, it never refuses to rotate without one — and it never keeps a copy of
the vault (FORMAT §18.3, DESIGN trap 29). Its backup, like every `Keys.Export`, records
`lastExportAt`.

**The recovery key's ID** (FORMAT §18.4) is on the sheet, in the text file and at the reveal; the
recovery prompt lists the vault's recovery slots as *label · ID · date* (`SlotView.CreatedAt`)
so the right sheet is picked before a digit is typed.

**`lastExportAt`** is the settings file's (§3): an object `{vaultId, at}` — the hex `vault_id` of
the vault exported and Unix seconds — written only by `Keys.Export`. It reads as *never* when
absent, zero, ahead of the clock, or carrying another vault's id, and *never* makes the
confirmations above say that no backup is recorded here rather than name a date. It is a local,
unauthenticated convenience: it chooses wording and pre-selects the rotate dialog's backup
checkbox, and it never gates a cryptographic operation.

**What changes in §3.** `VaultStatus` gains `Entangled`, `VaultFileSize` and `TamperedReason`
(`vault.tampered_hash` · `vault.tampered_generation`, so the warning can tell a damaged file from a
spliced or rolled-back region); §2.1's cached facts
are `VaultID`, `ModifiedAt`, `Entangled`, `Slots()`. `SlotView` loses `Entangled`, `Stale` and
`Escrowed` — every recovery slot has its escrow record from the commit that creates it (FORMAT
R38), so `vault.no_escrow`, `EscrowOpenedKey` and the Keys page's "Made before Enfold kept
recovery keys" state go too. `BeginEnroll(kind, label)` loses `entangle` and the `Choose` step
for hardware keys; `Choose` remains for `kind = password`, the standalone slot's own.
`CreateVault`, `ImportFile` and `FinishSetup` keep `entangle` — each is the moment a vault's
switch is chosen rather than inherited. `Keys.RewrapStale`, `VaultStatus.RotationPending`,
§2.2's rewrap loop and the lock screen's `SwapKey` state go with the deferred rotation (FORMAT
§8); a generation mismatch is `Tampered`. `ArchiveSummary` gains `Description` and
`ForgottenAt`; `List(showHidden)` lists hidden and forgotten records together and the *Status*
column separates them. "There is no Forget in 1.0" is struck from §3 in favour of this section.

Bound methods this adds: `Archives.Rename(id, name)`, `Archives.SetDescription(id, text)` (at
most 1 024 bytes of UTF-8, FORMAT §7.1), `Archives.Forget(id)`, `Archives.Restore(id)`,
`Archives.Delete(id, alsoFile bool)`, `Archives.Details(id) ArchiveDetails{ArchiveID,
Description, CreatedAt, LastPath, CurrentKID, Revision, LastWriter, LastSeq, HashAtSeq,
LastCiphertextHash, ForgottenAt, AlwaysRequireFullAuth, Hidden, NoCompression, Versions
[]VersionView{KID, CreatedAt, RetiredAt, State}}` — one registry record read whole, hex strings
for ids and hashes, Unix seconds for times, the policy as named booleans, and no
`wrapped_archive_key`, no nonce and no offset, so §1's boundary holds; refused with
`vault.locked` outside Unlocked — `Archives.CheckFiles()`, `Vault.InspectRecords(path)` (a
ceremony of kind `records` that ends at `StepRecords` with the handle in `SlotLabel`, as the
reveal ends with its URL), `Vault.IncomingRecords(handle) []IncomingRecord{ArchiveID, Name,
Description, CreatedAt, Versions, Action skip · version · add · forgotten, ForgottenAt, Differs
[]{Field, Theirs}, Ticked}`, `Vault.MergeRecords(handle, ids)` (a registry write on the session)
and `Vault.DiscardRecords(handle)`, `Keys.EntangledState()`, `Keys.SetEntangled(on)` and
`Keys.ChangeEntangledPassword()` (ceremonies of kind `entangle`; the standalone password and the
first way in are the only `Choose` prompts, confirmed on the page in a second field under the one
prompt), `Vault.LastExportAt() int64` (seconds, zero for *never*). Codes this adds:
`archive.forgotten`, `archive.delete_failed`, `archive.file_unreachable`,
`archive.not_this_archive` (the proven mismatch: nothing removed, the page asks once more before
forgetting), `archive.description_long`, `archive.name_invalid` (a rename that is empty or over
1 024 bytes), `vault.not_this_vault` (no VMK and no key opened the incoming registry — never
worded as damage), `vault.tampered_hash`, `vault.tampered_generation`, `vault.escrow_missing` (the
registry keeps no copy of this recovery key — replace the slot; never a refusal of the unlock).
`vault.no_escrow` and `EscrowOpenedKey` go; `vault.stale` stays for the A/B stale-copy warning and
`Session.Live`. *Import records…* lives in the Archives page's command bar — the vault must be
Unlocked for it, so the lock screen cannot offer it — with a pointer from the Keys page's Backups
card. The rotate dialog's pre-selected backup performs the export and then the rotation in one
press; a Save dialog cancelled at the export rotates nothing.

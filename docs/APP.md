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
(32-bit tick arithmetic, a backwards step counts as no input); core operations do **not** reset it — background work keeps its archive alive, never the session.
A timer that fires checks, under the lock, that its generation is still the armed one and that
its deadline has come before it locks: a callback that lost the race to an accepted reset, or
one left over from a session since locked, does nothing (ruled 2026-09-12 — `Stop` on a timer
that has already fired is no stop). The absolute cap applies regardless of activity. Both values come from the registry (§3 Settings) and are clamped in the
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
(`DamagedCopyPath` names the newest; the Vault page shows it; the lock screen offers it for
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
session. The status counts the retired copies (`RetiredCopies`, `RetiredPath`), the Vault page shows
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
*Test that a backup opens…* (ruled 2026-10-17; it was "Check that a backup opens…"); the Vault
page's *Inspect a backup…* reads a backup's label and points there. The outcome of an
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
  the panel goes back to *WaitingForKey* with `token.no_card` as a note ("insert it again"), and
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
    the same vault without the PIN typed seconds earlier: cancel *Add an unlock method*, press the blinking
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
    as the note on the step they wait in (WaitingForKey; Deriving, after the password): "the YubiKey
    is still answering the cancelled request — touch it, or pull it out, to end that now". Then
    they go on as if it had never been there: the card opened afresh, the PIN asked again.
- **A pulled key answers with Win32 codes first** (DESIGN trap 26). For a moment after the key
  is pulled — before the resource manager has noticed the reader go — the CCID driver fails the
  request on the wire with a Win32 device error, `ERROR_GEN_FAILURE` (0x1f) or
  `ERROR_BAD_COMMAND` (0x16), rather than an `SCARD_` code: the VERIFY that a PIN typed just
  before the pull lands in, the agreement waiting for the touch, the open's own preflight. A
  return code outside the `SCARD_` facility from a call that addresses a card or a card handle
  is the key gone (`ErrNoCard`), in both PC/SC layers, and takes the removal's path: the PIN
  never reached the card (no retry spent), the panel goes back to *WaitingForKey* with "insert
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
  screen's first step with the note "the password was not given within 5 minutes; unlock again
  from the key" — carried as `VaultStatus.Note` (`token.password_deadline`), beside
  `PendingTouch`, because the ceremony is gone as a cancel leaves it and no ceremony event
  survives that; the next ceremony clears it. The clock is armed only when the attempt carries a
  password: with the switch off there is nothing to wait for, and the memo is zeroed when the
  attempt ends, milliseconds after the touch. The cache is not a defence against a read of memory — five minutes is a window
  nothing stops — it bounds how long a hardware-verified state can be used without the password
  (ruled 2026-09-08). `MaxOperations` exhausted → "remove and reinsert the key" (WaitingForKey).
- Deriving: `keystore.Unlock(HardwareCredential{Token, Password})` → `Unlocked` → `Session()` →
  `Unlocked.Close()`. Then the ceremony goroutine's deferred `Card.Close()` runs (Releasing);
  `ErrResetFailed` from every release of a card the PIN step touched — this one, or an early
  exit from the PIN prompt on — is a warning event, never a failure, and a moment rather than a
  standing state (ruled 2026-10-13): the toast says it once, and `VaultStatus.Warnings` carries
  `token.reset_failed` only until the next successful open of a card — which resets it — or the
  next lock trigger, whichever comes first, so the lock screen shows it only when the failed
  release just happened there. A release before the PIN step only logs it: nothing has asked
  that card for its PIN or sent it a VERIFY, so it cannot have been left verified, and the
  warning says the key may still be PIN-verified. A key pulled meanwhile has nothing to reset
  and reports nothing. The publish point checks the lock latch.
- Recovery key and standalone password skip the token states. `Vault.SubmitPassword` serves
  both the standalone slot and the entangled password; there is one submit per secret kind.
- No rotation is deferred (FORMAT §8), so there is no rewrap loop and no `SwapKey` between
  tokens at the lock screen. A recovered generation that is not the superblock's is reported
  through the vault's `Tampered` state with the tampering reason (FORMAT §6.2, R25) — a spliced
  or rolled-back slot region — never as a key that is behind.

### 2.3 Archive

```
Closed ──Open──▶ Open ──operation──▶ Busy ──commit / abort──▶ Open     Open ──Compact──▶ Compacting ──▶ Closed → reopen
   ▲               │ idle (no readers, no ops)                            Open ──RotateKey──▶ Rotating ──▶ Open
   └───────────────┘                                                      any ErrIndeterminate ──▶ NeedsReopen
```

- **Open**: keys for every version of the registry record unwrapped through
  `Session.UnwrapArchiveKey`, handed to `archive.Open` as candidates, zeroed after. The core keeps
  per archive: the committed **snapshot** — `Files()` and `Dirs()`, taken once per open and
  re-taken after every commit — from which the breadcrumb and the rows come (FORMAT R39; never a
  prefix projection); there is no overlay and no pending vocabulary since 2026-09-09: a row is
  committed or it is not listed, and the running operation's progress is the only in-between —
  a preview **token** (32 random bytes, minted at Open, forgotten at
  Close), a reader count and a last-served time, and two clocks.
- **An operation is a transaction** (ruled 2026-09-09: the staged model — a pending bar, *Save
  changes* and *Discard*, a dirty state with clocks of its own — is gone, as it is from every
  archiver the user compared; WinRAR's practice). Each Add, Replace, Delete, Rename, Move and
  CreateFolder calls `Begin()`, does its work, and ends in `Commit` → receipt → one
  `Session.UpdateRegistry` (`LastStoredSize`, `LastWrittenAt`, `LastSeq`, `Revision`) →
  `archive.changed`; nothing is staged between operations. *Cancel* on a running Add or Replace
  is `Abort`: nothing is published, and the bytes it wrote lie in extents the committed free map
  still holds free, so nothing is lost and nothing leaks. A Delete asks first, on the page —
  "Permanently delete 3 files and 1 folder from ECON 280? A folder takes everything beneath it.
  This cannot be undone." — and then commits; a deleted file is unreachable from then on — no
  live index carries its key — and its bytes leave the file at the next compaction (FORMAT R32,
  narrowed 2026-09-13: unreachable, not erased).
  Rename, Move and CreateFolder commit at once, with no question. **Space comes back** (ruled 2026-09-09, after an emptied archive stood at 8 GB; the in-place form
  ruled 2026-09-10): a cancelled add's appended bytes are truncated at `Abort` (the archive
  layer's rule); a commit that frees the tail of the file is followed at once by a second, empty
  commit that truncates it — R31's quarantine lasts one commit, and that commit is this one, so
  the losing superblock never points past the end of the file; and after any commit — and when the last reader holding an extent closes — the core plans a
  *Reclaiming space* run, FORMAT R40's in-place compaction. The plan is made on the free map in
  memory first: live extents in offset order, each into the earliest published hole wholly
  before it that holds it, a file no hole before it holds skipped, an extent a reader holds left
  where it lies for this run. The run happens only when the whole run — the plan dry-run commit by commit in memory, a
  source one commit frees becoming a hole for the commits after the next (R31's quarantine), so
  that deleting the first of three equal files counts the two moves it takes and not the one
  the first commit can make — would give the file system back **64 MiB or more** of the tail and that is at least a quarter
  of what it would have to move; the dry run takes the same budgeted steps the run will, since a
  budget changes which holes coalesce and so which files move, and the run itself stops, as a
  run cut short and not as a failure, if what it has moved ever exceeds four times what has come
  back and what the fresh plan still promises (the outside review of the implementation) — the floor measures bytes returned, not the size of any hole, since a hole filled
  behind a file that cannot move returns nothing, and a 64 MiB hole at the head of a 100 GB
  archive is not worth moving 100 GB for (that is what *Compact* is for, on request). Each commit moves as many extents as fit in a budget of **64 MiB** of ciphertext — an extent
  larger than the budget moves alone, in a commit of its own, since it cannot be split and would
  otherwise never move — so an archive of many small files does not pay one index rewrite per
  file, and each is planned afresh — the commit's own index takes a hole and the sources just moved are in quarantine,
  so one plan is never worked through across commits; when the hole the plan wants is
  still under R31's quarantine — freed by the commit just made, with no tail follow-up to
  publish it — the run's first commit is the empty one that does; the tail is cut by the follow-up commit as it comes free — and a move commit, like the
  publishing one, places its own index outside the run that follow-up will cut, appending it
  if no other hole holds it, so what comes back is never less than the plan promised (the
  adversarial proof of 2026-09-10). Progress is by bytes moved, and what came back is said
  separately, as `OpView.Returned` (§3) in the foot's done line ("Reclaimed 1.2 GB"; a run that
  gave nothing back says nothing, since the run was the core's own — §6, *The foot*; it was a
  toast until 2026-10-18); a cancel is honoured at any chunk of the copy — an aborted
  commit leaves the originals live and its copies in free space — and *Close archive* cancels
  it too, the archive consistent and simply less compacted, the run resumed by the next
  qualifying commit. A reclaim that fails after the user's own edit committed reports the edit
  saved and the reclaim incomplete, never the edit failed; a move commit's own metadata may
  leave the file larger for a moment (the free map is always appended), which the follow-up
  commit takes back. The whole-file *Compact* stays on the Archives page for what a move cannot
  serve (a file larger than every hole before it) and for a full defragmentation on request.
  The floor, the quarter and the budget are constants for now. **Compact and RotateKey are
  gated on `Session.Live()`** before they start (`NeedsUnlock`); an operation's own commit is
  not — the archive key is in memory — and the receipt it owes waits for the session. The
  commit — index seal, free map, two syncs — runs under the archive's own mutex only, so the
  state mutex stays short and a lock trigger is never held up by I/O; the receipt is written
  under the state mutex right after. A lock that lands between the two, a hardware trigger
  inside the ~2 s suspend budget, or a slot-change ceremony holding the handle leaves a
  **receipt owed**, held in memory, applied when the ceremony ends or at the next unlock before
  any archive is opened (dropped if the record's kid moved), shown as "saved; vault record
  pending". On reopen a file whose `last_seq` is **behind** the record's is reported as an older copy
  restored, never adopted silently; one **ahead** of it is a receipt that was lost (a crash before
  the next unlock) and is adopted, the next registry write recording it; the file's size is not
  compared — an aborted transaction's tail leaves the file larger at the same `seq`, and the
  archive layer reclaims it.
- **What a cancel means, by kind** (ruled 2026-10-18, phase 3 of the design review's direction
  4). The core can stop any operation through `CancelOp` (a context the work checks), and a
  commit is never cancelled (`op.committing`, §3); which ones the page offers to stop is the
  page's rule, `lib/ops.ts` `cancellable`. It read, until this ruling: "A compaction they did ask
  for, a rotation and a verify are not the user's to abort halfway, and an extract has already
  written files on disk" — so *Cancel* was withheld from a verify and an extract by choice,
  though the core stops both. *Cancel* is now offered for an add, a replace, a reclaim, a
  *Refresh fingerprint* and an extract, and for a drag out while it stages (§3):
  - an add or a replace is `Abort`: nothing is published (above);
  - a reclaim stops at the next chunk, the archive consistent and less compacted (above);
  - a *Refresh fingerprint* (`Verify`) stops at the next MiB its hash reads, and records nothing:
    `LastCiphertextHash`, `HashAtSeq`, `LastSeq` and `LastStoredSize` stay as they were, so the
    fingerprint is the one there was before;
  - an extract stops between files, each file being all-or-nothing, so no half-written file is
    left; it ends `op.cancelled` with the outcomes of the files written before the cancel in its
    `Results`, nothing after the cancel is attempted, and what it wrote stays where it is, since
    those are the ordinary files the user asked for — the done line counts them: "Stopped. 6 of
    15 files were written to D:\Archives\Photos 2024" with *Show in Explorer*, or "Stopped.
    Nothing was written" (§6, *The foot*).
  A compaction the user asked for and a re-key run to the end — the rule's own words, kept for
  these two: they "are not theirs to abort halfway". A compaction rewrites the whole file and a
  re-key is two registry writes around a file write (R33, below), and an end halfway serves no
  one. The core can still cancel one for reasons of its own — a kill switch, the shutdown (§5) —
  and a compaction or a re-key that ends `op.cancelled` so is said as a stop, "Stopped. Photos
  2024 is as it was". The two dialogs say so before they start
  — *Compact* "… It runs to the end once started.", *Re-key archive* the same second sentence —
  the foot shows no *Cancel* while one runs, and the operations' flyout says *Runs to the end*
  where its *Cancel* would stand (§6, *The foot*). A user's *Cancel* ends in a neutral line in the
  foot, never a toast (ruled 2026-10-18; it was the neutral "Adding 3 files: Stopped." toast of
  2026-10-14).
- **An open archive has no timeout of its own** (ruled 2026-09-10; DESIGN §10 amended). It stays
  open — its keys in memory — for as long as its page is shown in Enfold, whether or not the
  vault is locked: a lock locks the keystore, not an archive already open, and a timer that
  closed the archive under the user's eyes was found to cut work off for no reason. Leaving the
  page (`Archives.Leave`) closes it at once and destroys its DEKs — or, when one of its own
  operations is still running, when that operation ends, since closing the handle under an add
  because the user went back to the list is the cut-off this ruling reverses — unless a body is
  still being served over the preview transport, in which case the archive is **draining**: the token admits
  no new request, every bound method but `Close` answers `archive.not_open`, the bodies in flight
  finish, and the archive closes itself after the last (§4). That is all a page's own preview
  needs; the external player that will need more — new range requests after the page is left —
  gets a file-scoped lease with a deadline of its own, bound to the playback the user started and
  never renewed by a request, decided when it exists. *Close archive* on the page is the kill
  switch: it closes now, readers or not, dropping every in-flight body (§4) and cancelling an
  operation of its own that is running — Close never waits behind an add or a verify: the token
  dies and the bodies fail first, and the handle closes as soon as the cancelled operation has
  let go. **Closing the window is leaving every page** (ruled 2026-09-10, the user's own rule: "closing
the window should exit the archive; minimising to the taskbar is not that") — and **the close
button never means "to the tray" until the user has said so** (ruled 2026-09-13: "we must not
default to going to the tray"): the first close — the button, Alt+F4, the taskbar's close —
is cancelled and the page asks, in its own dialog, *Quit* or *Keep running in the tray* —
*Quit* the accent and the default, first in the foot band, with the focus on it as the dialog
opens and Enter pressing it, and *Keep running in the tray* the standard button beside it, with
no third (ruled 2026-10-17, the user's: the default is Quit, so the close still never defaults
to the tray) — with *Remember my choice* unticked, kept for whichever of the two is pressed.
The close button in the dialog's top-right corner and Esc cancel the question: the dialog
closes, the window stays, and nothing is remembered; a click on the backdrop is nothing.
The answer, remembered or not, is `Settings.CloseAction` — `ask` (the default) · `tray` ·
`quit` — and the Settings page shows it as *When the window closes: Ask each time · Keep
running in the tray · Quit*, so the remembering is undone there too. *Quit* is the shell's Quit
(§3: the running operations named, `ResolveForShutdown`, `app.Quit()`); a close the shell
performs itself (a lock trigger's, Quit's own) never asks. With *tray* the window's close —
which takes it to the tray and destroys it, §2.4 — calls `LeaveAll` in the shell, so an
  archive whose window was closed closes, or drains, exactly like one whose page was left, and a
  window recreated from the tray starts at the list. Minimising to the taskbar is not a close:
  the page stays mounted and the archive open. When the external player exists (§4), closing the
  window while something is still being served to it **asks once** before the archive goes —
  the words and the shape decided then; marked here so the close path is built with the
  question in mind. A page nobody is looking at is still a page: an archive left open on an
  unattended, unlocked desktop — the window open or minimised — stays open, past the vault's
  own lock, until someone leaves its page, closes it or closes the window — an accepted
  concession, written into DESIGN §2 and §10, chosen over a timer that cut work off.
- **What stays usable after a lock** (DESIGN §10): `Page`, `Stat`, `PreviewText`, `Extract`,
  `PreviewURL`, in-flight readers, and the operations — `CreateFolder`/`AddFiles`/`AddFolder`/
  `Delete`/`Rename`/`Move` — each committing the archive and owing its receipt; Compact,
  RotateKey, Open and Create need the session — `Open` included when the handle already exists:
  a handle the core opened for an Archives-page operation is never mounted by a page while the
  vault is locked (`vault.needs_unlock`), so a lock never hands a page an archive it did not
  have. The frontend keeps an open archive's view mounted across a lock (its locked banner, §6
  *Notices and their tiers*, names the archive and says it is still decrypted and its changes
  still saved — ruled 2026-10-18; it read "The vault is locked. This archive stays open while
  you are here.", which said neither; nothing new can be opened). `VaultStatus.Opened` (§3) names
  every archive so held, and a lock does not empty it.
- **The Archives page's operations never ask for the archive to be opened first.** While the
  vault is unlocked, *Refresh fingerprint* (the core's `Verify`, below), *Compact…*, *Re-key archive…* (*Rotate key…* until 2026-10-17) and every other record-level operation on
  an archive whose page is not open unwrap the archive key with the session's `KWK`, open the
  file for the operation, and close it — the key destroyed — when the operation ends; an archive
  whose page *is* open uses that handle. The one-handle-per-path rule of the archive layer is
  kept by the core: an operation on a closed archive opens it as the core's own handle, and a
  page opening it meanwhile joins that handle rather than opening a second. The temporary
  unlock of a later version has its own rules and is not this.
- **Compacting**: refused unless Open and clean; previews for the archive are quiesced by
  draining (stop minting URLs, refuse new requests, wait for in-flight handlers, with a timeout
  that aborts the compaction); every core entry point answers `archive.compacting` from core
  state without touching the handle; progress is coalesced to ~10 Hz; any return from `Compact`
  means the handle is finished: `Close`, reopen the path, decide from the file (the new hash and
  size go to the registry with `HashAtSeq = LastSeq`). Compact is refused when the estimate
  (live bytes over measured throughput) does not fit before the absolute cap. **A compaction
  says what it gave back** (ruled 2026-10-18; only a reclaim did): its `OpView.Returned` (§3) is
  the file's size as the compaction started less the fresh file's, recorded once the fresh
  file's receipt is and before the reopen, so a reopen that fails does not lose it — 0 for a
  compaction that failed or whose file did not come out smaller — and the done line says it,
  "Compacted Photos 2024: 2.9 GB given back", or "Compacted Photos 2024" at 0 (§6, *The foot*).
- **RotateKey** is registry-first (FORMAT R33, DESIGN trap 21): gate (open, writable, clean,
  readers drained); new key and kid; registry write #1 — append the current version, retire the
  old with `RetiredAt`, move `CurrentKID`, in one update; `archive.RotateKey`; registry write #2
  — the receipt, performed even when the rotation returns a receipt with `EnvelopeStale`, which
  is then reported with `RepairEnvelope` offered. Rule A: content and layout publish to the file
  first and the registry records the receipt after. Rule B: key changes publish to the registry
  first and the archive adopts them after.
- **NeedsReopen**: `ErrIndeterminate` from any commit closes the archive and says what happened;
  Reopen shows which state won. **Verify** is an explicit op that re-hashes the file and refreshes
  `LastCiphertextHash` with `HashAtSeq`; Save never hashes. Verify is also where a torn or
  unrewritten envelope is made good (FORMAT R33): an archive that opened with `EnvelopeStale` is
  rewritten with `RepairEnvelope` **before** the hash is taken, so the file goes back to
  describing itself and the hash recorded is of the file as it now stands. A compaction heals it
  the same way, since it writes a fresh envelope and records the new hash; an open never writes.
- **Refresh fingerprint** is the page's name for `Verify` (ruled 2026-10-18; it was *Verify*, its
  strip said *Verifying* and the pane "Last verified N saves ago"). The call keeps the core's
  name; the command is named for what it does — SHA-256 over the file's raw bytes, recorded with
  `HashAtSeq`, `LastSeq` and `LastStoredSize` — since it authenticates no chunk and checks nothing
  inside the file, and "verified" promised a check that never ran. The hash is the archive's
  *fingerprint*: it tells this file from another copy of it, such as a restored backup; it is
  shown in *Technical details…* (§13) and used nowhere else. Verify also clears the open
  archive's `CopyMismatch`, since the registry then names this copy, as the next commit does; the
  `archive.copy_mismatch` note says so in its second sentence — "This is not the copy the vault
  saved last; maybe an older backup. Your next save, or Refresh fingerprint, makes it the current
  one." — and on the Archive page carries a small standard *Refresh fingerprint* at the bar's end
  that runs `Verify` on that archive, the layer's foot showing it (§6, *The foot*), greyed while Locked
  (`vault.locked`'s sentence as its title) or Tampered (the Tampered banner's title, §6) as the
  Archives page's command is. Only Verify and Compact take a fingerprint: a create records
  `LastSeq` 1 with `HashAtSeq` 0 and an ordinary save moves `LastSeq` alone, so most records have
  none. The Archives page's details pane ends its facts with a *Fingerprint* row
  (`lib/archlist.ts` `fingerprintOf`), drawn only from the record read whole (`Archives.Details`)
  and so never while Locked — the summary of an archive a lock left open carries `HashBehind` 0
  for want of a registry, which would claim a match nobody measured. It says one of four things:
  "Not taken yet", in the third ink, when no hash was ever taken (`HashAtSeq` 0 and an all-zero
  hash); "From before the last save" when a hash exists and the row's receipt is owed, since the
  file then has saves the registry has not counted; "Matches the last save", after the check in
  the accent's ink — the only one of the four with the check — when `HashBehind` is 0; and "From N saves
  ago" otherwise. The record is read again whenever the row's `HashBehind` or `ReceiptOwed`
  changes or the vault unlocks, so the fact never stands on an older record for longer than one
  `Details` call; and since `HashAtSeq` only moves forward, a summary whose `HashBehind` has fallen
  below a stale record's `LastSeq` proves a hash was taken since and is believed over that record.
  The command has no ellipsis and no icon, and is greyed while Locked, while Tampered (it is a
  registry write), and on a forgotten record or one being compacted (§6).

### 2.4 Window and tray

`None ⇄ Open`. The window is destroyed on close (`DisableQuitOnLastWindowClosed`; decided by use
on 2026-09-13 — destroy, not hide: a reopened window shows nothing worth ~130 MB resident — and
the close reaches the tray only when the close question of §2.3 says so; the `closeToTray`
destroy/hide setting is gone) — its destruction leaving every archive page, `Core.LeaveAll`
(§2.3) — and recreated from the tray or a second launch through one
`ensureWindow()` in the shell (`app.Window.GetByName` then `NewWithOptions`); the tray's
`AttachWindow`/`ShowWindow`/`ToggleWindow` helpers are not used — they assume a window that
exists at tray creation and that close only hides. Tray: three states — Locked, Locked with
archives open, Unlocked — each with a light and a dark icon set together. The tray names the vault (ruled 2026-10-14,
so Settings' "how this vault is called … in the tray" is true): one line — the display name,
its state and, while unlocked, the nearer deadline in the foot's words that never tick (§6):
"Personal vault — Unlocked · locks after 10 min idle", "… · session ends 16:42", "Personal
vault — Locked" — is the menu's first item, greyed, and the tooltip's start, followed there by
the open-archive count and the running operations; menu *header* / Open / Lock now / Quit,
where Open reads *Unlock…* while the vault is locked — with archives open or not (ruled
2026-10-17; it used to stay Open while archives were open, since the window shows their page
and a ceremony begun under it would run unseen) — and begins the ceremony — the YubiKey where
the vault has one, the password otherwise, as the lock screen's first button does — before it
shows the window and emits `shell.unlock` (no payload), which the page answers as the locked
banner's own Unlock does, with the lock scene over whatever page it holds, so the ceremony is
seen running; the page ignores it with no vault configured or the vault already unlocked, and a
refusal of the begin (setup needed, the Smart Card service missing) is the lock screen's to
say (*Close all
archives* went 2026-09-13: the window's close already leaves every page, and while the window
is up the page closes archives; a tray item that changed the core under a page that did not
refresh was worse than none).

**Boot order in the frontend:** subscribe to every event at module scope, before the first
`await`; then `Vault.Status()`. `VaultStatus` and every `vault.*` payload carry one `Seq`
incremented under the state mutex; the frontend applies a payload only when its `Seq` is
greater than the last applied. `archive.changed {ID, Seq}` and the per-archive `Page`/`Stat`
replies carry a per-archive seq that says how fresh the tree is: a reply is applied when its
`Seq` is not older than the last applied *and* it answers the page's newest request (a request
token — a later `Page` for another folder, sort or offset at the same `Seq` is not stale; an
older request's reply is); a `Page` whose `Seq` is newer than the pages the list already holds
restarts the listing from offset zero, since rows may have moved between offsets — ids
survive it: the selection keeps the ids that still exist, the anchor its row if it still
exists, and the header's tick is recomputed against the new `Total` (rows a commit added are
not selected). `Status()` includes `Ops []OpView` so a window
recreated mid-operation recovers progress; operation events are deltas over that snapshot —
and a status carried by an event (`vault.state`, emitted for every accepted `Activity` among
other things) only fills in operations the page does not know: it never overwrites one the
page tracks, since the snapshot is taken under the core's lock and emitted after it, and a
newer `op.progress` may already have landed (a status the page asked for — its own `Status()`
— replaces them; ruled 2026-09-12).
Until that first `Status()` call has answered — a reply or a failure; an event that lands
first is applied but does not open the gate — the page draws **nothing of the vault's**: the
brand mark alone, not the lock scene, not the list; and then the scene the newest accepted
status names, with no crossfade out of a scene that was never
right: a window opened from the tray while the vault is unlocked shows the list at once (ruled 2026-09-10, after the lock scene flashed there). A first
`Status()` that fails draws **the boot failure's page**, never the lock scene (ruled 2026-10-18;
it was one plain line with *Retry*): a panel where the lock screen's stands — `min(440px, 100%)`
wide, its top at 14vh, 28 px in, the flat stroke — holding the brand, "Enfold could not start" as
its title, the code's sentence (§7) in the second ink, and three buttons on a row that wraps:
*Retry* (accent, focused as it arrives, its ring drawn only once the keyboard is used), which asks
`Status()` again; *Open the data folder*, `Shell.OpenDataFolder` (§3) — the log the sentence
points to is there — whose failure is the code's toast; and *Copy details*, which puts "Enfold
could not start · <code> · <the failure's time in ISO 8601>" on the clipboard and toasts "Details
copied". Nothing of the vault is drawn, as before. An event that arrives before the reply is
applied by its `Seq` as any other, so the first scene drawn is the newest status accepted.
One that arrives after the first call failed is the evidence the gate waited for, and opens it on
that status: the failure's panel gives way to the scene the status names (ruled 2026-10-18; the
failure was cleared and the gate stayed shut, which left the bare splash with no Retry).

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
id does not resolve. Times are Unix seconds; sizes are `uint64`. **Every service method returns `*app.Error`** — `{Code, Retries?, Slot?, Path?}` (`Path` only with `archive.open_elsewhere`, §14: the file the archive is open from)
whose `Error()` is the code and nothing else, produced by one `classify(err)` over every
sentinel of every package with a catch-all `internal` — and services are registered with a
`MarshalError` that emits only that shape; the original error goes to the core-side log.
`CeremonyState.Error` and `op.done.Error` use the same codes.

**Vault**
- `Status() VaultStatus{Seq, State, Path, DisplayName, LastUnlockedAt, LocksAt, AbsoluteAt,
  Entangled, VaultFileSize, Tampered, TamperedReason Code, Note Code, Warnings []Code, Ceremony *CeremonyState, Ops []OpView,
  OpenArchives int, Opened []ArchiveRef{ID, Name}}` plus the one-vault facts of §2.1 (`SetupNeeded`, `DefaultPath`,
  `MissingPath`, `Damaged`, `DamagedCopyPath`, `KeptElsewhere`, `RetiredCopies`, `RetiredPath`)
  and, while unlocked, `IdleEffectiveMinutes` and `AbsoluteEffectiveMinutes` — the timeouts the
  timers were armed with, the registry's minutes after the clamp of §2.1, zero while not
  unlocked — so that the foot and the tray say the core's number and keep no copy of its rule
  (one truth; ruled 2026-10-17). `Opened` (ruled 2026-10-18) names the archives this process
  holds open — the id and the registry's trusted name each was opened under, renames included (a
  rename of an open archive emits `vault.state` as well as `archives.changed`) — sorted by name
  ignoring case, then by id, and never null; a lock does not empty it, since an archive left open
  stays open (§2.3), and the lock screen and the locked banners say them from it (§6, *Notices
  and their tiers*); `OpenArchives` stays the count the foot and the tray read;
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
  prove a current way in never does. The first way in's is typed in the dialog that starts the
  ceremony and sent by the page at that prompt (§6, ruled 2026-10-17); the core is unchanged.
- `LastExportAt() int64` (§13: the delete dialog's backup sentence and the rotate dialog's tick
  read it, and keep reading it) and `BackupStatus() BackupStatus{At, Archives, Missing, Rekeyed}`
  (ruled 2026-10-18, for the Vault page's backups card, §6). `At` is `LastExportAt`'s answer, zero
  for *never*. The counts are read from the registry only while Unlocked and not Tampered, and
  are 0 otherwise, `At` still given: `Archives` counts the records not forgotten, hidden ones
  included, since a backup holds their keys too; `Missing` those created after `At` — every one
  when `At` is 0; `Rekeyed`, over the records the backup does hold, those with a key version
  created after `At`, each archive counted once however many times it was re-keyed. The
  predicate is `created_at`, of a record and of its versions, never `modified_at`: every save
  moves that, and a line keyed to it would never read current. Like `lastExportAt` it is local
  and unauthenticated — it words the line and suggests the export, and gates nothing — and it is
  not exact at its edges, so nothing calls it exact: the export snapshots the registry before
  `At` is stamped, both in whole seconds, so a record created in that moment reads as in the
  backup; and a merge keeps the incoming record's and versions' `created_at` (§13, Merge
  records), so a record older than the last backup and merged in after it reads as in it.
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
  forgotten records are filtered unless `showHidden`. `Open(id)`, `Leave(id)` (the page left: close, or drain while a body is in flight, §2.3), `Close(id)` (the kill switch), `Create(path,
  name, noCompression)`, `Hide(id)`, `Unhide(id)`, `Locate(id, newPath)`, `Compact(id) opID`,
  `RotateKey(id) opID`, `Verify(id) opID`, `CloseAll()`, `OpenPath(path) OpenPathResult{ArchiveID, Relocated}` (§14: the archive whose envelope's `archive_id` names a record is opened as a double-click would, its `last_path` moved to this path when it differs — the same write as `Locate` — with `archive.key_not_in_vault` when no record has the id, `archive.not_an_archive` when the file is not one or is not there, `needs_unlock` while the vault is Locked) — `Create(path, name, method)` takes the
  compression method, `store` · `fastest` · `normal` · `better` · `best`, written into the
  record's policy (FORMAT §7.1 bits 2–5) and applied to every later open of the archive on any
  machine; `ArchiveSummary.Method` and `ArchiveDetails.Method` carry it back (they replace
  `NoCompression`); a path where a file already exists is refused with `archive.exists` — the
  archive layer creates with `O_EXCL`, Enfold never overwrites a file it did not make (DESIGN
  trap 28) — and the inspector's `Rename`,
  `SetDescription`, `Details`, `Forget`, `Restore`, `Delete` and `CheckFiles() CheckReport{Probed,
  Missing}` of §13 (it answered only an error until 2026-10-18; it now answers once its pass has
  ended, with what the pass measured and what it found), where
  Forget and Delete — the registry record holds the only copy of the archive keys, so dropping
  it destroys the archive — are specified with their brakes.
- Events: `archives.changed`, `op.progress {OpID, Done, Total, Phase}`, `op.done {OpID, Error,
  Results []FileOutcome}`.

**Archive** (an open one)
- `Page(id, dirID, sort, offset, limit) Page{Seq, Rows []FileRow{ID, ParentID, IsDir, Name,
  Path, Size, Storage, SavedPercent, ModifiedAt, Pending}, Total, Crumbs []Crumb{ID, Name}}` —
  the live children of `dirID` (the all-zero id is the root, §3's ids) in the merged view,
  directories and files in one list, in the order `sort` names (ruled 2026-09-10): one or two signed keys, comma-separated — the
  column, `name`, `size`, `type` or `modified`, with a leading `-` for descending, then `name`
  or `-name`, the tie-breaker's direction (the Name column's own state, which the page keeps
  while another column sorts); the empty string means `name`; **directories come before files under every key and both directions**, then the key,
  then the name as the tie-breaker in the name key's direction. `name` is a general collation — the core's `x/text/collate` on the root locale with `Numeric`
  and `IgnoreCase` (checked 2026-09-10): runs of digits compared by value so *2* sorts before
  *10*, accents secondary (*resume* before *résumé*), case ignored and two names equal under it
  ordered by code point so the order is total, scripts in the Unicode collation's order (Latin,
  then Hangul, then Han and kana) and Han ideographs in the collation's implicit order — the unified block first, the
  extension blocks after it, by code point within each — not a locale's pinyin or
  stroke order, which would differ by machine — so every page of a long directory comes in one
  order; `size` is the file's plaintext size (directories, which show none, stay first in both
  directions, ordered by name among themselves); `type` is the extension, case-folded, files with
  none first; `modified` is the record's `ModifiedAt`. The sort is the core's so that paging
  stays consistent; the page keeps the choice while the archive is open and opens on `name`. `Children(id, dirID, sort) []ChildRef{ID, IsDir}` answers every live child of `dirID` in the
  same order, cheaply — ids and kinds only — what the header's tick and Ctrl+A select, the
  folder and not the rows loaded so far (a long folder pages in as the list scrolls), and what
  a Delete's question counts, files and folders apart, when the selection reaches past the
  rows loaded; `Name` is the record's own, `Path` the joined one (R20, R39,
  bounded as R39 bounds it), and `Total` counts that directory's children, not its subtree.
  `Crumbs` is the chain from the root down to `dirID` **inclusive** and is never empty: its first
  entry is the root, `{ID: <the all-zero id>, Name: <the archive's name, the same string as
  ArchiveStat.Name>}`, its last is `dirID` itself, — so the page draws its heading — the archive's name, `Crumbs[0]` — from `Crumbs` alone, takes no
  name from `Stat`, and walks `Crumbs` upwards when the folder it stood in went (below); since
  2026-09-10 no folder crumb is drawn (§6). A directory row's `Size` is the sum beneath it and its `ModifiedAt` the record's own.
  The page holds a `dirID` across events and can hold one that is gone — a `Delete` took a subtree the page was standing in — so an id that no longer names a live directory of the merged view answers `file.not_found`, never an
  empty listing under a breadcrumb that still names the place: the page walks the `Crumbs` it
  last held upwards, retrying until one answers (the root always does), and says which folder
  went. A live directory with nothing in it is not that case — it answers zero rows with `Total`
  zero, which is what an empty folder is. `Compact` and `RotateKey` change no id (FORMAT R33),
  so a reopen leaves the page where it was.
  `Stat(id) ArchiveStat`.
- `CreateFolder(id, parentID, name) recordID` commits a directory record at once (empty is fine —
  it is a record, FORMAT R39, its name validated and matched like any other's); `AddFiles(id, parentID,
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
  id it collides with — and nothing else: no size and no date, so the dialog shows none (the
  research's mock drew each pair's size and date; a collision cannot say them, and nothing is
  invented). A folder's `Name` comes back with the trailing `/` it was offered with, which the
  page never shows. **The dialog** (ruled 2026-10-18, in the shape of the extract's conflict
  question below, Explorer's "Replace or Skip Files"; it read "*Photos* is a file here" over
  *Cancel*, *Skip those*, *Keep both* and *Replace*, all standard since 2026-10-14). A folder that
  meets a folder is a **merge**, not a clash to answer: the add enters it and merges the two
  whatever the policy, which reaches only the files inside (above). The title counts every name
  already there, by its kind, and names the folder shown or the folder a drop landed on — "2 files
  with the same names are already in Photos 2024", "A folder named 2024 is already in Photos
  2024", *items* when the kinds are mixed. The first 6 names follow, each with its type's icon and
  its kind: "File"; "Folder; the two are merged"; or, where a file meets a folder, "A folder here;
  a file is being added" or "A file here; a folder is being added", in the attention ink — then
  "and N more". When a merge is among them, a caption under the names says what it does: beside
  clashes, "The folder of the same name is merged with the one already here, and the choice below
  also applies to the files inside it."; alone, "The folder is merged with the one already here.
  The choice below is for any file inside it with the same name as one already there." (each with
  its plural). Then three command rows of equal weight — full-width standard buttons, each a
  strong line over its consequence in caption, the accent on none — counting the clashes alone:
  *Replace the 2 files in Photos 2024* / "The copies already in the archive are overwritten.",
  greyed, with "A file and a folder never replace one another." in place of its consequence and as
  its title, while any clash is a file meeting a folder; *Keep both* / "The new ones get a number in
  their names."; *Skip these files* / "The 2 files already here stay; the rest are added." ("…;
  nothing is added." when every name offered clashes). When every name is a merge the dialog still
  asks, since `CheckNames` sees the top level only and a policy chosen silently could overwrite
  or skip a file inside unasked; its rows then speak of the files inside: *Replace files with the
  same names* / "Those copies already in the archive are overwritten."; *Keep both* / "The new
  ones get a number in their names."; *Skip files with the same names* / "Those copies already in
  the archive stay; the other files are added.". *Cancel* stays in the foot band and adds nothing,
  and Esc is *Cancel*. The policy each row sends is unchanged: `replace`, `keep-both`, `skip`. `Replace(id, fileID, path)
  opID` is the in-place edit (never Delete + Add). `Delete(id, recordIDs)` — a directory takes its subtree as the index has it, tombstoned in the
  same commit (FORMAT R39, R32); the page asks first, naming files and folders apart and saying a
  folder takes everything beneath it and that this cannot be undone, and the operation then runs
  at once: there is no undo, a deleted file being unreachable (FORMAT R32) — `Rename(id, recordID, newName)` (one record, file or directory; a `/` is refused, and a name that
  folds onto a live sibling of the record's own parent is `file.exists` — a change of case alone
  is not one, since a record is not its own sibling), `Move(id, recordIDs, parentID)` re-parents
  each record, one record written per item whatever subtree hangs beneath it. Both, like
  `CreateFolder` and the adds, are pre-flighted against R39 **before anything is written**, and a move batch is refused whole
  and in place, so nothing the encoder would reject is ever written and the user retries with a
  name rather than finding half a selection moved: a destination that is not the root or a live
  directory is `file.not_found`, as is
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
  used. `Extract(id, recordIDs, dir, policy, names) opID` — `names`, usually empty, maps a record id
  to the one path element to write it under *in this extract only* (what *Use this name* and
  *Rename…* send after a `name_refused`, below): the record is untouched, a name that breaks
  R20 is `params`, a directory's new name carries its subtree under it, a collision under the
  new name follows `policy`, and the outcome carries the path actually written — the plan is a
  **set** of records live in the
  merged view: each selected record, every live record beneath a selected directory, and the
  ancestor directories of all of them up to the root (the all-zero id among `recordIDs` is the
  root and extracts everything — the extract dialog's *Everything in <archive>* — which makes any other id in the call
  redundant; an empty `recordIDs` is `params`, never everything). A record reached twice is planned once. The plan is ordered **parents before
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
  of folders alone runs with `Total` 0; each file is all-or-nothing, the batch not). **The extract dialog** is one dialog for one command (ruled 2026-10-18, phase 3 of the
  design review's direction 7: the toolbar's *Extract all…* took the whole archive whatever was
  selected, and the selection's *Extract…* stood at the foot of the preview pane): the toolbar's
  *Extract…*, Ctrl+E and the pane's *Extract…* all open it (§6). It is a form, so Enter in the
  destination extracts and the field has the focus as it opens; it is titled with the archive by
  name — "Extract from Photos 2024" — and its field's hint is in sentence case, "Created if it is
  not there yet" (ruled 2026-10-17); and on every extract one info line stands under the
  policies and above the band, with no box — the info icon and "The extracted copies are ordinary
  files, outside Enfold's protection." in the second ink — ruled 2026-10-18: true of any
  destination, where a check of the drive's type would miss a folder that syncs; a sentence, not
  a gate, and *Extract* stays the accent. **The scope line** comes first, *What to extract*, two
  radios: *The 4 selected items* ("The selected item" for one; files and folders counted as the
  pane counts them) and *Everything in Photos 2024*. It opens on the selection when there is one;
  with nothing selected it opens on *Everything*, the first radio greyed with "Select what to
  extract" as its reason. *Everything* sends the all-zero id alone, so the page walks nothing;
  the selection sends the ids selected as the dialog opened. The dialog holds an editable
  destination prefilled by one rule per scope (ruled 2026-09-10): for *Everything*, the archive's
  own folder plus a folder named after the archive — `D:\Archives\ECON 280\` for
  `D:\Archives\ECON 280.efd` — created when the extract starts; for a selection, the archive's own
  folder, with no extra level. A change of scope moves the field to the new scope's prefill only
  while it still holds the old scope's: once the user has typed, browsed or emptied it, the field
  is theirs and stays (`lib/extract.ts` `followScope`). *Browse…* opens the native folder picker
  into it; the destination is created if it does not exist. **The policy for a file already there is
  `ask` by default** (ruled 2026-10-14, reversing 2026-09-10's `replace`, "the wizard default of
  every archiver": WinRAR's own help marks *Ask before overwrite* as its default, and Explorer's
  Extract All asks per conflict), offered first, with `replace`, `skip` and `rename` (keep both,
  the incoming one numbered) beside it in that order; the dialog opens on the policy last chosen
  while Enfold runs — kept in memory only, never in the vault or `settings.json`, and the
  destination is still never kept. `replace` writes through the temporary and
  places it over the existing file in one move (never by unlinking first, so a failure leaves the
  old file); `ask` extracts everything that collides with nothing first and reports each
  collision as a `conflict` outcome carrying the record's `ID`, the archive copy's `Size` and
  `ModifiedAt`, and `Existing{Size, ModifiedAt}` for the file in the way — a stat taken after the
  collision was seen, by the cheap pre-check or by the exclusive create's refusal, and never one
  that decides a write: nothing is ever placed over a file on a stat's word — so the page has
  everything the compare list shows and every id the re-issue needs without walking the tree;
  and the page then asks. **The conflict question** (ruled 2026-10-18, in the shape of
  Explorer's own "Replace or Skip Files"; it was a title over *Replace all*, *Skip all* and *Let
  me decide for each file* — *Replace*, *Skip* and *Compare both files* for one — in the foot
  band): the title counts and names the destination, "3 files with the same names are already in
  D:\Archives\Photos 2024", or for one, "A file named itinerary.pdf is already in
  D:\Archives\Photos 2024"; then three command rows of equal weight in the body — full-width
  standard buttons, each a strong line over its consequence in caption, none of them the accent,
  so the button that looks like the default is never the overwrite (ruled 2026-10-14, kept):
  *Replace the files in the destination* / "The 3 files already there are overwritten." (*Replace
  the file in the destination* / "The file already there is overwritten." for one); *Skip these
  files* / "The 3 files stay as they are in the destination; the rest were extracted." — the
  clause after the semicolon said only when the extract wrote other files and refused or failed
  none, so it never speaks of a rest that is not there; and *Let me decide for each file* /
  "Compare the two copies side by side." (*Compare both files* / "See their dates and sizes side
  by side." for one). There is no foot band and no *Cancel*: Esc and the corner close button are
  *Skip*, which re-issues nothing, and the backdrop is nothing. *Replace* re-issues `Extract`
  for every conflicting id with `replace`. While the question stands the foot says nothing of an
  extract that wrote nothing, since the question is its news; one that wrote some says so,
  "Extracted 9 of 13 files to …", and each re-issue has its own line (§6, *The foot*).
  **The compare list** — *Compare both files* and *Let me decide* are one dialog after Windows
  Explorer's: a row per file with the type's icon, *Files from the archive* on the left and *Files
  already in the destination* on the right, each side's date and size, a tick on either or both,
  the ticks' rule said once in a caption under the two heads, "Tick both to keep both copies"
  (`rename`, the archive's copy numbered), and "Skip N files with the same date and size" at the
  foot, pre-ticked. The rows whose date or size differs start on replace (ruled 2026-10-17, after
  the extract dialog's own default became `ask`: the list is entered by choosing to decide, and
  extracting into a folder is usually updating it). *Continue*, the accent, reads what it will do
  with the ticks as they stand — "Extract 2, skip 1", "Extract 3", "Skip 3", a copy kept beside
  the one there counted as extracted — and re-issues `Extract` for the ids the ticks chose with
  `replace` or `rename` (ruled 2026-10-18; this replaces 2026-10-17's count beside the foot's
  tick, "3 will be replaced · 2 skipped", and the note under the list). *Cancel* skips them all,
  and so does Esc. **A name the
  destination refuses** (ruled 2026-09-10) is the same shape: a name is checked on the way *in*
  against R20 — Windows' own rules, 255 code units, no reserved name, no forbidden character —
  so what went in can come out on any Windows volume as far as the name itself goes, but the
  path's whole length and the volume's own limits (an SMB share, an exFAT stick) are the
  destination's to know and only its refusal says so; so an extract that meets `ERROR_FILENAME_EXCED_RANGE`, `ERROR_INVALID_NAME` or
  `ENAMETOOLONG` on a name it is placing reports that record as a `name_refused` outcome
  (`file.name_refused`) — a file, or a directory, whose subtree is then not attempted and is
  reported once, for its top, as a skipped subtree is — with the rest of the batch written,
  never a failed batch, never a hang; the destination itself refusing (the root the call was
  given) is the operation's error, before any outcome, as today. The page then asks per refused
  record, titled "The destination cannot take a name this long", and **shows the name it would
  write** (ruled 2026-10-18; it explained the rule in a paragraph — "Shorten halves it, keeping
  the extension…" — under *Shorten*, *Rename…* and *Skip*): "<name> was not written." and "Write
  it as <the shorter name>?", the shorter name strong and free to break anywhere, a name too long
  for the destination being long in the dialog too; then *Use this name* (the old *Shorten*),
  *Rename…* and *Skip*, all standard buttons in the band, and the page re-issues `Extract` for
  the chosen ids with `names`. *Use this name* writes the name shown: the extension kept whole
  (the part after the last dot; none when the dot is the first character or there is none) and
  the stem halved at rune boundaries — never inside a surrogate pair — a name that then collides
  following `policy`; it is greyed, with "The name is down to 1 character and cannot be shortened
  further." in place of the question and as its title, when nothing is left to halve. How a name
  is shortened stands behind a disclosure, *How names are shortened*. With more refused names
  waiting, the dialog counts where it is ("2 of 4") and a tick above the band, "Do the same for
  the other 3", gives the answer pressed — *Use this name*, each record under its own shorter
  name, or *Skip* — to every refused name still waiting; *Rename…* is one record's and is never
  given to others, and a record already answered keeps its answer. The tick is off each time a
  question opens, the one after a *Rename…* included, so every name is asked about unless the
  user says otherwise; with it on, Esc is *Skip* and so skips them all too (ruled, not an
  accident). When what was refused is the temporary the extract writes through (a fixed short
  name in the same folder), the path and not the leaf is what the volume refuses and no name helps — the outcome is then
  `path_refused` (`file.path_refused`) — so no name is offered and the dialog says so — "The folder's path is too long
  for this destination" — with *Skip* and *Skip all like it*; a name refused again after
  *Use this name* or *Rename…* is asked about again, as a new question, *Skip* always there. No loop over names runs unbounded anywhere: keep-both numbering already stops at a bounded
  count — and when every name to `(999)` is taken the outcome is `failed` with `file.exists`,
  not `skipped` — and at the first refusal that is not "already exists" (checked 2026-09-10),
  and *Use this name* stops at one rune. **A drag out of the window** onto the desktop or an Explorer folder (ruled 2026-09-10, late,
  after three research passes and two real drops — DECISIONS has them): **the staged route**,
  7-Zip's and WinRAR's, with what their authors and users learnt folded in. **One gesture serves
  the list's own moves and the drag out** (ruled 2026-09-11): rows are not HTML5-draggable —
  a press-and-move over selected rows calls `Shell.DragOut(archiveID, recordIDs)` and the
  native drag runs from there, since a page's own drag cannot become one and two drags cannot
  run at once; the page keeps the ids in flight. A release over Enfold's own window — the
  drop source's `QueryContinueDrag` sees where the button came up — is a **self-drop**: the
  data object answers the drop's `CF_HDROP` request with the future paths and extracts
  nothing, the folder is deleted at once, and the page, receiving the WebView's drop on a
  folder row, the `..` row or the heading with paths under the staging root, performs `Move`
  of the ids in flight to that target instead of an add (a drop elsewhere on the page is
  nothing); real files dropped in from Explorer are told apart by their paths and go on being
  the add of §3. A release anywhere else is the drag out proper: the shell starts one native OLE drag on **a thread of its own** (ruled 2026-09-11 on the
  measurements in `docs/research/drag-out.md`): a locked OS thread that calls
  `OleInitialize` itself, owns a message-only window for its queue, attaches its input to the
  window thread's (`AttachThreadInput`, the drag thread attached to the window's — Chromium's
  direction, the one that worked) for the cursor and the button state, runs `DoDragDrop`
  there and posts the result back; the main thread never waits on it, so the WebView's queue
  stays alive through the whole drag — measured: posted messages reach the window within
  10 ms during the hover *and* during the extraction — where, with the main thread as the drag
  thread, Wails' dispatcher runs every pending callback in one batch (a `DoDragDrop` in it
  starves the page's events for the drag's length) and the outgoing `Drop` call into Explorer
  pumps only a handful of special messages (nothing posted during the extraction arrived before
  it ended). The data object is a pure-Go, cgo-free one, agile (the free-threaded marshaler
  aggregated, `IAgileObject` answered) so a call from Explorer's RPC thread runs there rather
  than being marshalled to the drag thread. It offers `CF_HDROP` by **delayed rendering** and, beside it, `CFSTR_PREFERREDDROPEFFECT` =
  *move* with both *copy* and *move* allowed — and **not** `IDataObjectAsyncCapability` (ruled
  2026-09-11 after the first real drag, WinRAR's model): without it Windows requires the target to answer the drop *inside* `Drop` — Explorer's
  conflict dialog, its *Replace*, *Skip* or cancel, and a same-volume move all happen before
  `DoDragDrop` returns, and the return carries the real effect (a cross-volume copy, measured,
  is handed to Explorer's own engine and runs on after the return); with it Explorer had copied in the background, never called `EndOperation`,
  and after a *Skip* neither read, nor moved, nor let the object go, so nothing could say the
  drop was over. Nothing of Enfold's is held while `DoDragDrop` runs: it runs on its own thread, the page is
  alive, and the running line in the layer's foot is the only sign (§6, *The foot*) — there is no moment to hold the window for,
  because (measured) Explorer's `Drop` hands a cross-volume copy to its own engine and returns
  before the bytes have moved (a staged file was still being read eighteen seconds after the
  return), while a same-volume drop is one rename inside `Drop`; what WinRAR holds its window
  for is its own extraction dialog, which for Enfold is the foot's line with its *Cancel*: the staged copy is
  disposable, so a drop on the same volume — the desktop, from `%LOCALAPPDATA%` — is one rename,
  no second pass over the bytes and nothing left to clean, and a drop on another volume is
  Explorer's copy followed by Explorer's own deletion of the staging; **a move never touches the
  record** (7-Zip's ruling, for the same reasons: deleting a member is a rewrite and a risk).
  **The staging folder** is one per drag, `%TEMP%\Enfold\drag\<id>` (ruled 2026-09-11, moving
  it from `%LOCALAPPDATA%\Enfold\drag`: the conventional home of an application's transient
  files — WinRAR's `Rar$DIa…` lives there — where Storage Sense and Disk Cleanup will sweep
  what is left, an incidental backstop never relied on; where a user's own `TEMP`
  redirection is honoured, a RAM disk keeping the plaintext off persistent storage for free
  and another volume costing only that a desktop drop becomes a copy; and where the later
  open-and-edit workflow's `%TEMP%\Enfold\open\<id>` will sit beside it under the same manifest
  and the same scavenge), made atomically before the drag, its small manifest — created-at, state, the owning process —
  at the folder's top and the files themselves under an `items` folder beneath it, so that no
  dragged name can collide with the manifest (a record may be called `manifest.json`) and a
  scavenge can tell an abandoned folder from a live one; a delete that fails half-way leaves
  the manifest in place (rewritten if it was taken), since a folder without it is one the
  scavenge may never touch. **When the files
  are written**: a `CF_HDROP` request that arrives during the hover — targets do ask, Raymond
  Chen and 7-Zip's comments are the witness — is answered with the *final* paths (never a
  placeholder: Edge caches the early names, 7-Zip found); the files themselves are written by the first request after the button's release — the
  core's `Extract` with `replace` into the folder, **without the `fsync` a real extract pays
  per file** (ruled 2026-09-11 after the bar was seen to stall for seconds at each file's end:
  the staged copy is disposable and Explorer's own copy is what lands; durability is the
  destination's) — **and that phase is visible** (ruled
  2026-09-11 after the measured 5 GiB drop: Explorer shows nothing until the request returns,
  and a user who sees nothing move thinks the program is dead — the reason WinRAR and 7-Zip
  put a progress window on the screen at that moment): the drag's running line is in the layer's foot from the moment the drag starts (ruled 2026-09-11, after
  a first real drag whose staging was over before the strip appeared; the foot since 2026-10-18, when the operation strip above the list was retired, §6 *The foot*): the hover shows the label alone, **Dragging N items**, since nothing is chosen yet — the user may still drop it back or press Escape (ruled 2026-10-14, refining 2026-09-11's one label throughout: "Extracting" over the hover stated an outcome nobody had chosen) — and from the drop on the label is **Extracting N items**: the staging shows a bar that moves by bytes with *Cancel*, and when the staging is done the bar
  simply stays full and *Cancel* stays where it is, greyed and unclickable (ruled 2026-09-11:
  a button that vanishes reflows the strip; a greyed one keeps its shape — restated for the foot on 2026-10-18: its slot never
  reflows at all, and nothing above it moves, since the foot is 34 px whatever it says), with no new words: from then on Explorer's own window is in front
  and the only control there is — so a fast staging is a change inside a line already on the
  screen, never a line that flashes; the window never freezes, since the request runs on Explorer's thread (the object being agile)
  and the WebView's stays free; a cancel fails the request and Explorer abandons the drop.
  When the request has returned and Explorer has the paths, the line stays at its full bar
  exactly as long as `DoDragDrop` has not returned — usually a moment — and is taken down when
  it does (ruled 2026-09-11: "the bar is ours — the staging; whatever the user does after, they
  do in Explorer's window"); a copy Explorer still has to make shows in Explorer's own dialog: the drop is then over for Enfold, whatever the user answered Explorer — *Replace*, *Skip*, a
  cancel half-way — and the result says which: *moved* (the effect was *move* and the items
  are gone), *copied* (*copy*), *cancelled* (no effect: Escape, a release over nothing, *Skip*,
  Explorer's cancel), *refused* (the target never asked for the paths), *self-drop*, or *failed*
  (the extraction); a *moved* or *copied* drag out ends in the foot's done line, "4 items left
  Enfold as ordinary files", a failed one in its error toast, and the rest say nothing, since
  nothing was chosen or nothing left (§6, *The foot*). The folder's fate: deleted at the return only when a *move* took every
  item, when it was a self-drop, or when nothing was written; **never by the target's window
  class** — the return says nothing about a copy still running in Explorer's engine (measured
  across volumes) — so every other folder stays manifested for the scavenge, which is the
  mechanism, not the exception. No watch, no idle clock (and, since 2026-09-13, an owner: a manifest records the process that wrote it — its id and
  creation time — and a folder whose manifest says the drag is live is swept only when that
  process is gone, the id dead or reused by a process of another creation time; a process that
  exists but cannot be queried counts as alive, and a manifest with no owner recorded is never
  swept while it says live) — and a request that cannot be honoured (the extraction failed or was cancelled) **fails `GetData`**
  rather than hand out half-written files, which 7-Zip's does not. A target that needs the
  files to exist at hover time (Sticky Notes refuses a path that is not there) is the
  measurement the prototype makes before the rule is final; if such targets matter, a small
  selection may be written at the first request. **When the folder goes**: at once when the drag ends with nothing written (Escape, a
  refused drop, a failed extraction), at `DoDragDrop`'s return when it was a self-drop or a
  move took every item (above); otherwise the folder outlives the drop and even the process, because consumers open the paths late (a browser reads a dropped file when the
  upload starts, a configuration dialog kept 7-Zip's paths for minutes, FileZilla and VMware
  broke on early deletion) — and the **scavenge** removes what is ours: at launch, every ten minutes while running, and
  once more at a normal exit (one pass, no backoff waits — the shutdown budget is three
  seconds; a folder still in use is left for the next launch), every manifested folder whose
  state is not live and whose age is past **one hour** (WinRAR's threshold; a longer one lingers plaintext for nothing), a folder
  still in use (a sharing violation on an exclusive open) retried with bounded backoff — 1 s,
  10 s, 60 s — and left for the next sweep, never traversing a reparse point, never touching a
  folder without our manifest. Delete-at-reboot (`MOVEFILE_DELAY_UNTIL_REBOOT`) is not relied
  on: it needs an administrator, and Chromium's use of it is an attempt, not a guarantee. The
  explicit *Extract…* stays for what staging serves badly — a large transfer to a network
  share, the whole archive (*Extract…* with *Everything*). Everything is logged per drag: the formats asked
  for and when, the extraction's start, end, result and bytes, `DoDragDrop`'s result and effect, the class of the window under the release (logged, deciding
  nothing), every deletion attempt with the Windows error, never a file name. `Extract` with the all-zero id extracts everything from the root straight into the
  destination, and the toolbar's *Extract…* is greyed, "This archive holds nothing yet", while
  `ArchiveStat.Records` is zero (live files and folders together; the file count alone cannot
  say whether the tree holds anything). `PreviewURL(id,
  fileID)`, `PreviewText(id, fileID, maxBytes) {Text, Truncated}` (over
  `OpenReader` + `LimitReader`; no cross-origin fetch exists). `Stat` carries `CopyMismatch` and the
  list a `Note` of `archive.copy_mismatch` when the file's seq is not the one the registry last
  saw (an older copy restored): shown, never adopted silently; the next commit records this copy. The archive layer's transaction takes the tree with it: `Tx.Add` and
  `Tx.Replace` carry the record's `parent_id`, a delete collects the subtree from the index rather
  than from the caller, sibling checks are per parent and case folded against the transaction's
  own index (FORMAT R39), and nothing is looked up by a path — there is no whole-name lookup any
  more.
- Events: `archive.changed {ID, Seq}` and progress as above. `OpView` carries `Items`, the files an
  add, a replace or an extract plans, known once the plan is made, so the foot can say *Adding 3
  files* rather than the phase word, and, for an extract, its `Policy` and `Destination`, so the
  conflict question is derived from the operation itself in the store — it survives the page's
  remount across the lock scene and cannot be lost to a race between the call's return and the
  operation's end — and so the done line can name where the files went; a count of one is
  singular everywhere the page counts. `Returned` is what a reclaim or, since 2026-10-18, a
  compaction gave the file system back once it ended: the file's size before less its size
  after, never the bytes moved or written; 0 for every other kind, for a run that ended before
  its first commit, for a compaction that failed and for one whose file did not come out smaller
  (§2.3). The done line says it ("Reclaimed 1.2 GB", "Compacted Photos 2024: 2.9 GB given
  back").
  Progress is by **bytes**, not by file: an add, a replace and an extract count the bytes read of the file in hand, so a single
  large file moves the bar (at most ten events a second, as before); `Done`/`Total` are plaintext
  bytes. `ArchiveStat` loses `Dirty`, `CapAt` and `SessionAlive` and gains `Records` (live files
  and directories); `Save`, `Discard` and `KeepOpen` are gone; `CancelOp` on a running Add or
  Replace aborts its transaction, and on a Verify, an Extract or a reclaim stops the work at its
  next check (§2.3, *What a cancel means, by kind*; the page offers none for a Compact or a
  RotateKey) — and a cancel that arrives once the operation has entered its
  commit is refused with `op.committing` ("The operation is already being saved; it will
  finish."), the operation's own result then being the committed one. The two are one decision
  under the core's state mutex: either the cancel lands first and nothing is published, or the
  commit is entered first and the cancel is refused. There is no answer in between.

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
  `RevealRecoveryKey(recipientID)` from the Vault page — ends its ceremony at `StepRecovery` with
  the one-time URL in `SlotLabel` (§4) and the same dialog on the page, whichever view it is on.
  `RevealRecoveryKey` is a ceremony (`kind: reveal`; the dialog's title "Show the recovery key")
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
  the dialog: **save as a text file** — the native Save dialog at once, its caution already in
  the dialog's body (a text file is plain text: off this PC and out of any folder that syncs; §6,
  ruled 2026-10-18 — a dialog of its own, *Where to keep it*, came first), then
  `SaveRecoveryKey(handle, path)`, and the core writes the file
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
  page does not try to tell a print from a cancelled one (ruled 2026-09-09: the WebView's own print
  dialog offers *Save as PDF*, which never touches the print spooler, so a spooler watch missed the
  likeliest path — built, measured at a two-second lag on a cancel, and retired): pressing
  *Print…* counts as done. So does **written down**: one click, no second confirmation — a user
  set on leaving is not stopped by one, the key can be shown again from the Vault page, and at the
  moment it is first shown the vault holds nothing yet. After a save the core acknowledged the
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
  `Vault.InspectFile`; restoring archive records from a backup — the `RestoreArchiveRecord` of
  earlier drafts, re-wrap under the current KWK — is *Import archives from a backup…*:
  `Vault.InspectRecords`, `MergeRecords` and `DiscardRecords` (§13, "Merge records").
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
  key it shows. It is still a state (ruled 2026-10-18): the core's refusals are what disable,
  and the banner that says it — its words, *Replace with a copy…* and *Why?* — is §6's
  (*Notices and their tiers*), drawn wherever the page meets the state. The freeze is the vault
  file's: an archive's own saves still commit to the archive file and owe their receipts (§2.3),
  which is why the banner ends "Your archive files are not affected." A lock of an unlocked
  vault clears `VaultStatus.Tampered` and `TamperedReason` with the session but keeps the
  `vault.tampered` warning, which only a clean unlock or a fresh open of a vault file (a replace,
  another vault) removes; the page shows the banner while either says Tampered (§6, *The Tampered banner*).

**Settings** — `Get()`, `Set()`. Machine-local, in `%LOCALAPPDATA%\Enfold\settings.json`
(temp-then-rename): the vault's path when kept elsewhere (empty: `vault.eks` in the data folder)
and its display name, the close question's answer (`CloseAction`: ask · tray · quit; the older
`closeToTray` destroy/hide choice is gone — destroy is the behaviour, decided 2026-09-13, and
an old file's value is ignored), theme, look, recovery record percentage, dictionary threshold, the last export (`lastExportAt`, §13) and the folder of
the last archive created (`lastArchiveFolder`).
**Security-relevant values live in the authenticated
registry, not the file:** the idle and absolute minutes (`Registry.IdleMinutes`,
`AbsoluteMinutes`, zero = default) and the per-archive compression choice
(`ArchiveRecord.Policy` bit `no_compression`); `Compress.Padding` rides with it.

**A source that is the vault.** `AddFiles` and `Replace` refuse a source path inside the data
folder (`%LOCALAPPDATA%\Enfold`, or the vault's own path when kept elsewhere) with
`archive.source_is_vault` — an outside review (2026-09-13) noted that a page under someone
else's control could otherwise copy `vault.eks` into an archive it can read and extract it; the
keystore's lock leaves the file readable by design (`lock_windows.go`), so the refusal is the
core's. The same for `Extract`'s destination — never inside the data folder, nor above it, since a
tree written there could reach down onto `vault.eks` — and, the other way round, `AddFolder`
of a folder above the data folder is refused too: it would walk the vault in without naming
it. A drag-out's staging root may not be the data folder itself nor stand above it (the shell
names `%TEMP%\Enfold\drag`; the core's own fallback is a `drag` folder of its own inside the
data folder, two levels above anything staged, which is fine). The name is checked first and
the file second (an outside review's finding, 2026-09-13): every spelling is canonicalised before the comparison — on Windows by opening the path and
asking the file system for its final name (`GetFinalPathNameByHandle`: links, junctions, 8.3
names, `\\?\`, volume spelling and case settled in one answer; `EvalSymlinks` was found to
leave a junction as it was), elsewhere by `EvalSymlinks` — and after a source
is opened the handle itself answers — its identity against the vault file's (`SameFile`, which
sees a hard link the name never can) and its final path against the data folder — so a link
swapped in between the check and the open is refused at the open. An extraction never
descends into a reparse point below its destination: an existing directory it would enter is
read for the reparse attribute first, and a junction there is refused (`file.destination_link`,
the item and its subtree, reported once at its top), since a junction named
`Enfold` in an allowed destination would carry an archive's `Enfoldault.eks` straight into
the data folder.

**Shell** — `ShowWindow`, `CloseWindow`, `CloseDecided(action, remember)` (the close question's
answer: `tray` or `quit`; with `remember` the setting is written first; the shell then closes
to the tray without asking again, or quits), `PickFiles`, `PickFolder`, `SaveFile(title, filename, dir)` (`dir` empty leaves the folder to the shell; the archive create passes `lastArchiveFolder`),
`Reveal`, `OpenDataFolder()` (ruled 2026-10-18, for the boot failure's page, §2.4: opens the
shell's own data folder — the settings, the WebView profile and the log — as a folder in
Explorer, not selected in its parent as `Reveal` shows a path; it takes no path from the page,
answers `io` when Explorer cannot be started and `internal` when the shell lent no folder, and
the page says either as the code's toast), `Quit`
(names the running operations, if any, in a native Yes/No question — the only buttons a Windows
message box has — then `ResolveForShutdown`, then `app.Quit()`; never asked twice). A cancelled native file
dialog is "nothing chosen", never an error; a submitted secret that found no prompt is reported
back as the `secret.refused {Code}` event. Tray and menu callbacks run on the Wails main thread
and leave it (a goroutine) before touching the window or a dialog. The service lives in `internal/app/api` like the others and holds the Wails
calls behind an unexported `Hooks` value the shell supplies, so nothing of Wails is reflected. File
drop: the window is created with `EnableFileDrop`, and Wails delivers a drop only over an element
marked `data-file-drop-target` — the Archive page's file list, and the Archives page's body,
which carries no ids and makes a drop with no `data-archive-id` that the page opens as §14 says
(ruled 2026-10-14); the shell re-emits the dropped paths and the drop target's `data-archive-id` / `data-dir-id` —
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
is per archive (an unknown token is a 404 with no archive id in the URL); closing the archive drops the token and `Archive.Close` fails every in-flight body; leaving
the page with a body in flight puts the archive in §2.3's draining state — the token admits no
new request, the bodies finish, the archive closes itself after the last. A lock changes nothing
here.
Justification: it serves only archives whose keys are in this process's memory, which a same-user
process reads regardless (DESIGN §2); the unguessable path is the access control. **A later
version may hand this URL to an external player** so a video plays in place without extraction;
when it does, the server stays bound to `127.0.0.1` and nothing else — never `0.0.0.0`, never a
LAN address — and an archive kept open for such a player after its page was left gets a file-scoped lease
with a deadline of its own, bound to the playback the user started and never renewed by a
request (§2.3), decided then — as is the one question the window's close asks while such a
player is still being served (§2.3).

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
PDF viewer is not used (its toolbar cannot be hidden and there is no download hook — measured on
beta.16; beta.20, pinned since 2026-09-13, exposes no toolbar setting either): PDFs offer
Extract only in 1.0. No PDF renderer comes into the window later either — ruled 2026-09-13:
pdf.js in the page would inherit every bound service — and the later route is a temporary copy
opened in a browser (§12; DECISIONS "Before the phone").

**WebView2 disk cache (DESIGN trap 13), honestly:** Wails exposes no cache-disable or
in-private option (measured on beta.16; still so on beta.20, pinned since 2026-09-13), and
Chromium flags are not supported in production, so `no-store` on every response — the preview's
and the page's own — is the whole defence. The user-data folder is `%LOCALAPPDATA%\Enfold\WebView2`,
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
`resolveForShutdown()`: a running operation is cancelled — its transaction aborted, nothing
published, which the format tolerates — each owed receipt is written (a keystore write, milliseconds on a healthy disk — it takes no
context, so a stalled disk is the one thing that can hold the lock past the budget),
the archives are closed, then the lock — bounded to ~3 s in all. The tray's Quit asks the user first, then takes the
window and the tray away, runs the same function, and waits — unseen — for a pending touch
(§2.2) to end (`AwaitPendingTouch`: the card's own answer, about 15 s, then its release), so
the card is released and reset by this process; only then does the process end. The wait was
once inside the shutdown hook, with the window still up: fifteen frozen seconds, which is what
the hiding is for. `WTS_SESSION_LOGOFF` runs the resolution without asking and does not wait
for a touch: the OS gives a process seconds at logoff, and Windows resets a dead client's card
at the cleanup of its connection (DESIGN trap 27) — after the in-flight command has run out,
during which no program can reach the card anyway.

## 6. Screens (the Native look)

Lock screen (**one panel changing in place**, ruled 2026-10-17: the ruling of 2026-09-06 — "the
ceremony is one panel changing in place … the touch moment is a full-surface takeover" — built
at last; the three full-height cards side by side, two of them dim placeholders, which the build
had taken from the mockup's storyboard and the rulings of 2026-09-07/08 then described, are
gone, and so is their wording). One centred panel, `min(440px, 100%)` wide, 28 px in, a flat
1 px stroke and the card's radius, its top at 14vh in every state (the header keeps its 20 px
line whatever it holds, so nothing above the panel moves); the header's state word at the top
right, and beside it, at rest and at first run, a gear that opens Settings, which needs no vault
— the lock scene steps aside for the Settings page until the route moves on or the vault's state
changes (ruled 2026-10-17); the brand where Motion's *Lock* anchors it; the notices — the warnings, the
"workstation-lock detection unavailable" one among them (the BitLocker warnings went with §9's
checks, 2026-09-13), the archives still open — above the panel at its width, never below its
buttons, ranked by tier, danger first (*Notices and their tiers*, below; ruled 2026-10-18). **The step line** draws the states of §2.2 that an unlock passes through: a YubiKey's
three steps, *YubiKey · PIN and password · Touch* (*PIN* alone on a vault that asks for no
password; the PIN's step is named in its fields' order, ruled 2026-10-17, it read *Password and
PIN* live and *PIN* at rest), or the recovery key's two, *Recovery key · Unlock*. It is a caption
above the panel and only while an unlock runs: a passed step's 16 px disc fills with the accent
and a check and its name takes the second ink, the live one is ink at 600, those to come the
third ink. A vault's password asked again after the touch (a wrong one is known only then,
§2.2) makes the PIN's step live again while the touch keeps its check, and its button reads
*Unlock*, since nothing follows it but the opening (ruled 2026-10-17). At rest, at first run
and for a password's way in (one step), the create, the setup, the import and the backup test
(no fixed steps: a one-step line would say nothing; ruled 2026-10-17), it is
absent and its 16 px line is kept, so the panel never moves when it appears. Each state of §2.2 has its place on the panel — TwoKeys, NoMatch and Busy on the
YubiKey's step, Blocked on the PIN's (`SwapKey` is the enrolment's alone since §13) — and the
rest panel's heading is never borrowed by them. A view that replaces the one holding the focus
gives the focus to its first button, its ring drawn only once the keyboard is used, so the
keyboard keeps its place: the YubiKey's step its *Cancel*, No match's and Blocked's *Use recovery
key* (their *Cancel* where nothing offers the recovery key), a failure its *Start again* or *Close* (ruled
2026-10-17; the edge states left it on the document). A ceremony with no step line heads its PIN's
step with its own instruction, "Type your YubiKey's PIN", as its YubiKey's and its touch's steps
are headed, never with the act's name the header already says (ruled 2026-10-17; a create's PIN
step alone read *Creating the vault*). Every wait for a YubiKey draws the key art,
whatever the ceremony: an unlock's by the YubiKey, and a create's, a setup's or a backup's
import's for the YubiKey it adds — `SwapKey` with nothing to remove, and any `WaitingForKey`
after another step of the same ceremony, whatever method the core has named — captioned "Waiting
for the YubiKey to add to this vault." Only the opening instant of a typed way in, which the core
leaves at once, keeps what the panel showed (ruled 2026-10-17).

**At rest**: the padlock, the vault's name (the sub size), "Locked · last unlocked yesterday,
03:24" (relative — "today, 03:24", "22 days ago" — since recency is the point), *Unlock with
YubiKey* (accent, the panel's width, focused as it arrives so Enter begins, its ring drawn only
once the keyboard is used) and *Unlock with recovery key* (standard, the panel's width); the
button reads *Unlock with YubiKey* whatever the switch — the PIN's step says which secrets it
asks for. **An explicit Unlock starts the ceremony at once** (ruled 2026-10-17): the locked
banner's, the foot's and the Vault page's *Unlock*, a file opened from Explorer while the vault
is locked, and the tray's *Unlock…* (which begins it in the shell, §2.4) all bring the lock scene
with the unlock already begun by the vault's everyday way in — the YubiKey where it has one, else
its password — so the panel opens on the YubiKey's step, never on a button to press first. Only
a vault at rest is begun: one that needs setting up, is open in another Enfold, is broken or is
not there shows its own panel first, and an idle lock or a Windows lock never begins anything. A vault whose one everyday way in is a password reads "Unlock <vault>" over *Unlock with
password*, and pressing it, or Enter, turns the panel into the password's field in place: one
step, no step line, the padlock and the lead where they were, *Unlock* with *Cancel* beside it and
*Unlock with recovery key* beneath, under the form rule. The maintenance commands leave the daily
unlock (ruled 2026-10-17): one quiet subtle button, *Backups and copies…*, opens them as a menu —
*Test that a backup opens…* and *Replace this vault with a copy…* — never a second vault. Quiet
lines under the name carry what the status says: while it says `pendingTouch` from a cancelled
unlock, the key art stands in for the padlock with its LED breathing — the YubiKey really is
still waiting — the line says "The YubiKey is still blinking from the unlock you cancelled.
Continue, or touch it or pull it out to end it.", and the primary reads *Continue unlocking*,
since pressing it picks that touch up (§2.2; ruled 2026-10-17); that line is the lock screen's
only: on the Vault page
the pending touch left by a cancelled change of unlock methods or reveal is said by the next
ceremony's own `token.pending` note while it waits, and on the first-run panel a create or an
import begun while a cancelled create's touch stands is answered with `token.pending`, a toast;
and the status's note when the five minutes from the touch ran out (§2.2). The states that are
not a way in say their own lead, never the rest panel's (ruled 2026-10-17): *Open elsewhere* (the
header's word; it read *Open in another Enfold*, which the lead now says) reads "<vault> is open in
another Enfold" and "Close it there, then try again." over *Try again* (accent); a broken vault
(§2.1) keeps the padlock and the name with the caption naming the header's state, "Needs attention ·
last unlocked yesterday, 17:10" — never the rest's *Locked* — then the code's sentence on an
attention line (the icon and the words in the attention ink, no box) over *Reopen the vault*
(accent) (ruled 2026-10-17); a vault that needs setting up — an adopted backup's first way in — takes
first run's shape, with no padlock: "Finish setting up <vault>" and its line over *Finish setting
up…* (accent) and *Unlock with recovery key* (ruled 2026-10-17).

**The YubiKey's step**: the key art — a port, the plug, the YubiKey and its LED in a ring — with
the LED breathing while the app listens, "Insert your YubiKey" and "Waiting for a YubiKey that
unlocks <vault>.", *Cancel* and the subtle "Use recovery key instead"; under reduced motion the
LED stands still and the lead reads "Waiting for your YubiKey…". With the Smart Card service not
running (`token.no_service`, said after ten seconds at this step) the LED stops breathing and
holds dim, since nothing is heard from the reader, and the code's sentence stands on an
attention line under the lead — the icon and the words in the attention ink, with no box, as the
broken vault's; the boxed attention bar is kept for the last try and the damaged vault's kept copy
(ruled 2026-10-17; it was a bar). The art stays for the YubiKey
being read, its LED solid once it answered. Two YubiKeys in draw two, the second fading, under
"Two YubiKeys are in" and "Remove one; Enfold carries on by itself.", with *Cancel* alone — the
core carries on by itself once one is out. On a YubiKey that is not one of the vault's unlock
methods the LED goes dark and the dashes between port and plug go, so the screen shows which
object is wrong; the lead names the vault ("This YubiKey is not one of <vault>'s unlock
methods"), and an unlock offers *Use recovery key* beside *Cancel* (ruled 2026-10-17) (only an unlock can turn to the recovery key: an import's proof or a create says *Try
another YubiKey.*, and a ceremony in a dialog says the same, its foot band holding *Cancel*
alone).

**The PIN's step**: the YubiKey's name as a chip, then, on a vault whose entangled password is
on, the PIN and the password as two fields under one *Continue* (the ruling of 2026-09-08). The
PIN goes first and is verified at the card before anything else (§2.2): a wrong PIN marks only
the PIN field, with the retries, and nothing else moves — the password field keeps whatever was
typed, as an ordinary masked field the user may still edit, and is sent only once the PIN is
right, in the state it is in at that moment, by the page and with no further click. A wrong
password, known only after the touch, comes back with the password field emptied and marked and
the PIN not asked again (§2.2's cached `H`). Each field has one reserved 16 px line under it,
where its rule ("Up to 8 characters.") and what is wrong ("Wrong PIN.") cross-fade, so an error
never pushes the fields and buttons below it; the retries stand in the PIN's label row — "3 tries
left" in the second ink, attention at two, danger at one — and on the last try an attention bar
above the field says "Last try. A wrong PIN now blocks this YubiKey's PIN; then only the recovery
key opens the vault." with *Use recovery key* in it (ruled 2026-10-17: "1 attempt left — the last
one" was the grey of three, and "The key counts wrong tries, not Enfold." is gone from here). The
button reads *Continue*, since a touch still follows, its label centred in the width it grows to,
with *Cancel* a standard button beside it and "Use recovery key instead" a subtle one beneath
(ruled 2026-10-17; *Cancel* was an underlined link far below) — except on the last try, where the
bar's *Use recovery key* is the one way out and the subtle one goes. Every two-button row of the
panel is drawn so, the accent grown and *Cancel* at its own width beside it — No match's and
Blocked's *Use recovery key*, a timeout's *Start again* — and a *Cancel* that stands alone, on a
step that asks nothing here, is the same standard button, never a subtle one (ruled 2026-10-17;
No match's and Blocked's accent stood at its own width, and the quiet steps' *Cancel* was
subtle); the recovery key's cells alone keep *Unlock* at its width, the count at the row's end.
The same two-field form serves every token ceremony on the Vault page, where the last try's bar
says its consequence alone — "Last try. A wrong PIN now blocks this YubiKey's PIN until it is
reset." — since a dialog offers no recovery key. Blocked stands on this step, under the YubiKey's
chip, with its own lead that says whose PIN, "This YubiKey's PIN is blocked", then one sentence on
a danger bar, what it means and, in an unlock, the way out — "It will not accept a PIN until it is
reset; your recovery key still opens the vault." — and *Use recovery key* (accent, an unlock's
alone) beside *Cancel* (ruled 2026-10-17; it was a title over a grey line, then a bar alone, then
a lead that named no YubiKey over a caption and a bar that split one sentence).

**The recovery key's step** (ruled 2026-10-17): the vault's name and "Type or paste the 48 digits
from the sheet. The key ID at its top says which sheet it is.", the vault's recovery keys as a
quiet list, the ID first, then the name's own words — "0303-0303 · printed, in the safe" — the
kind the name repeats and the date left out, the date said only for a key named by its kind
alone, "0404-0404 · added 2026-10-10" (ruled 2026-10-17; each line repeated "Recovery key —" and
carried its date at the row's end, noisier than a list that says which sheet) — and the eight cells (Forms,
below), drawn at the sub size in mono, unspaced, 40 px tall, four to a row — the six digits
72 px wide in a cell of about 90, so its corners stay clear: a finished group that checks out
wears a small accent tick in its top-right corner, 2 px in, one that fails a 2 px danger
underline (not a frame),
and the line under the cells names it — "Group 3 does not check out. Compare its six digits with
the sheet." Then *Unlock* at its natural width (the backup test's *Test*), *Cancel* beside it, and
"5 of 8 groups" in the second ink at the row's end once the typing has begun and while it is not
done. A paste fills all eight. A key the vault refuses keeps its digits for retyping, and each
cell keeps its own judgement — its tick when its group checks out, which is still true — while
the refusal, the key's and no group's, is said once, by the line under the cells: "These digits
do not open <vault>. If you have more than one sheet, check its key ID." (ruled 2026-10-17; every
cell underlined in danger read as eight wrong groups, and the ticks had gone with it). The step line
reads *Recovery key · Unlock*. Finishing setup asks the same cells first, with no step line and
*Continue*, since its first way in follows.

**The touch takes the stage**: the touch's green over the whole scene — however far a short
window scrolls — the panel's fill and stroke fading into it, the header's state and the brand,
its mark outlined, in the touch's soft ink in both themes, the 160 px rings pulsing, the hero "Touch your YubiKey now", "Touch it
while it blinks. If it stops blinking, start again." in the touch's soft ink (no number: DESIGN.md
trap 23) and the receipt "PIN accepted · <the YubiKey's name>". The derivation and the release
keep the green and the layout — the rings held still at half, "Opening the vault…", "Touched ·
<the YubiKey's name>" — and Done pops its check on the same green. **The green holds through
whatever follows a touch in the same ceremony** (ruled 2026-10-17), whatever the act and
whatever way the core named: a create's or a setup's YubiKey proves itself by its touch, and the
derivation after it stays on the green, as an unlock's does — it was drawn neutral under
"Opening the vault…", the wash leaving mid-ceremony for a vault that did not exist yet. A
derivation says what it does: an unlock's "Opening the vault…", a create's "Creating the
vault…", a setup's or an import's after the one that opened the vault or the backup "Adding the
unlock method…". Only something asked after the touch — a prompt — takes the green away, and
a password or a recovery key, which nothing touches, derives and ends on the neutral panel.
**A failure stays where it
happened**: a touch that timed out (the core's two rounds) says "The YubiKey stopped waiting" and
"It waits only a short while for a touch." on the touch's step, in neutral ink with *Start again*
and *Cancel* — red is kept for refusals — and the header says *Not unlocked*; any other failure
of an unlock says its code on a danger bar on the step it stopped at; *Start again* begins the
same unlock method again. The header names the state — *Locked*, *Unlocking*, *Not unlocked*,
*Unlocked*, *Open elsewhere*, *Damaged*, *Not found*, *No vault yet*, *Replacing the
vault* (*Restoring a vault* at first run, where there is none to replace), *Creating the vault*,
*Setting up*, *Testing a backup*, *Needs attention*, *Needs setting up* — and its padlock agrees
with its word: open on arrival, shut at rest, open again with *Unlocked* (ruled 2026-10-17). First run, an absent and
a damaged vault use the same panel, with no step line, sized to its content (ruled 2026-10-17).
**First run** reads "Create your vault" and the one sentence — "A vault is one file: the keys to
your archives, opened by a YubiKey, a password or a recovery key." — over *Create a vault…*,
*Restore from a vault or backup…* and the subtle *Test that a backup opens…*; what a restore needs
(a backup's recovery key) is said in the restore dialog, where it applies. **An absent vault**
reads "<vault> is not where Enfold left it" over its path in mono, then *Try again*, *Find it…* —
a picker, then the import dialog with "use the file where it is" already taken, the route that
already says what the file is — *Restore from a vault or backup…* and last the subtle *Create a
new vault instead…*. **A damaged vault** reads "<vault> could not be opened" over its path, then
its best remedy first: when Enfold kept a vault a replacement retired, an attention bar "Enfold
kept the vault it replaced on <date>." (the date from the copy's name) with *Use that copy…*,
which opens it in the import dialog; then *Try again* (the accent when there is no copy) and
*Restore from another file…* side by side; *Rebuild the vault…* last, a subtle button in danger
ink, with what it costs on its own line held under it at the lead's gap: "Rebuilding starts a new, empty vault and keeps
the damaged file beside it. Archives locked to this vault then open only from a backup or a copy
of it." Archives (list with details — **the details pane is a side pane of 300 px while the page's
body is at least 1120 px wide, a fixed threshold rather than a proportion (ruled 2026-09-11:
the width below which the side pane would squeeze the Name column shorter than *Last
saved*); below it the details become a bottom panel** (refined 2026-09-12 after the first
build, and again after the first measured drags of it — the frame budget at 240 Hz is
4.17 ms): one element at the foot of the page, a **card** whose **header** is the whole of it
when collapsed — a single line of 44 px, the eyebrow *Selected* (a caption in sentence case, ruled 2026-10-17) and the archive's name on the
left, a chevron on the right that is a sign and not a button — and stays fixed at its top
when expanded, two lines then (the eyebrow over the name, as the wide pane's heading). What
the card holds never changes size: its inner — the header at a fixed 64 px whose top 44 px are
the collapsed line, its two shapes cross-fading by opacity, and under it the body, name,
description, facts and actions scrolling under the header — is laid out once at the panel's
open height, and the collapse is the card's height alone going down to 44 px over that
inner, clipping it; the card and its inner are each their own compositor layer, so a frame of
the motion is one height on a flat surface and one scroll offset, never a layout of the
body's fields nor a repaint of its text. The list sits above the collapsed card and is never
resized by it: the expansion grows the card upwards **over** the list and **pushes the
selected row up ahead of its top edge** — the row stays where it is until the rising edge
reaches its foot, then rides it, the list scrolling exactly as far as the edge has come, at
most to the list's first slot under the header row, where the card stops at the row's foot;
the open height runs from the first slot's foot to the card's own. The
push is not stopped by the list's own end: a row the list could not scroll that far (the
last rows of any list, every row of a short one) is carried there past the end — a spacer
after the table gives the room, and the list's viewport is held at its height while the
panel is up so that a short list scrolls rather than grows — and the spacer never shrinks
while the panel is up (a resize, which measures everything anew, is the one exception). The collapse is a spring let go: the edge goes down and the row rides
it back until the list stands where it stood before the opening (a list scrolled by hand while the panel was up, or a row the ride could not carry all the
way home — one sorted far down the list while the panel was up, whose foot would still be
below the collapsed card's edge with the list at home — eases from where it is to there
instead), never past the list's natural end, the spacer and the held viewport taken away when the motion ends. Nothing about
the motion is guessed: the arithmetic (`lib/archbar.ts`) works on numbers read from the
page, read all together before anything is written, and every per-frame value is written to
the element's own style — never through an inherited custom property, which restyles the
whole subtree each frame, and never beside a read that forces a layout inside the handler.
**The whole header is the control**: a click anywhere on the collapsed line expands, a click
anywhere on the expanded header collapses, Enter and Space on it do the same, Esc collapses,
and a different row clicked while expanded keeps the panel up and brings that row to the
first slot; nothing is selected → the name is empty, the chevron dim, and a click does
nothing. The body is `inert` from the first frame of a collapse, and a focus inside it goes to
the header. The list is reconciled, not left behind: a sort, a filter or a refresh that moves
the selected row while the panel is up brings it to the first slot again (a row the filter
took out of the list leaves the panel open at its height, nothing to push); a resize
measures the open height again, holds the viewport at its new height and places the row
without a second push, a running motion cancelled and placed at its targets; the layout going
wide cancels the motion and clears the card's height, the spacer, the held viewport and the
panel's state, and coming back narrow starts collapsed. The card, its inner and the body's scroller carry `will-change: transform` below the
threshold and only there (measured: the list's own scroller gains nothing from a layer and
keeps none; the write to the element's own style was the win). The header's cross-fade, the
card's growth and the list's scroll all animate over the motion tokens' duration on one frame
clock, and none of it changes the wide window's side pane — its rows and its header carry the same checkboxes as
the file list, with the same rules (a blank click clears; the header ticks all, ticked exactly when all
are; the boxes show on hover, focus and selection, and a selected row is the wash and the pill,
the sort glyph the one 10 px chevron — ruled 2026-10-17); the header ticks the rows the filter shows; the details pane shows the row last clicked while
it is ticked, else the sole ticked row, else nothing but a count ("3 archives selected"); an
action that takes one archive works on the pane's row and is greyed unless exactly one is
ticked —
pane, commands Open / New archive… / Open file… / Compact… / Re-key archive… / Refresh fingerprint (*Verify* until 2026-10-18, §2.3) / Hide and the inspector's of
§13 (ruled 2026-10-14: each command that opens a dialog or a picker ends with an ellipsis; ruled
2026-10-17: every command is named from the verb table in `strings.ts`, `commandCopy` — *Re-key
archive…* was *Rotate key…*, *Forget archive…* was *Forget key…*; Open,
Compact…, Re-key archive… and Refresh fingerprint are greyed, with the core's own words as the reason, on a
forgotten record and on an archive being compacted — a running Compact of it, or its handle's
`compacting` state — and Hide on a forgotten one, and a double-click or Enter on such a row
does nothing; on an archive being compacted Close, Forget archive… and Delete archive… are greyed
too, with the same words, since the close they need is refused while a compaction runs; the details pane's Open is a standard button, the bar's being the one accent;
a vault with no records at all, hidden and forgotten ones included, shows "No archives in
this vault yet." with *New archive…*, *Open an .efd file…* and *Import archives from a backup…* under it
in place of the empty table, the last a small subtle button — a command, not a link (ruled
2026-10-17)) — *New archive* asks the name and the compression method, a five-segment control (Store ·
Fast · Normal · Better · Best, Normal preselected; *Store* keeps every file as is, and at every
other level already-compressed media is detected by sampling and stored raw by itself, DESIGN
§9), and on *Create archive…* (ruled 2026-10-17: the primary repeats the command's verb and
object; the name field has no invented placeholder) opens the native Save dialog with `<name>.efd` prefilled in the folder last
used (`settings.json`, `lastArchiveFolder`); a chosen path where a file already exists is refused
in place — "A file is already there. Enfold never overwrites; choose another name." — whatever
the dialog's own replace prompt said; a cancelled dialog creates nothing, and a created archive
is opened at once, on its empty root with the Add menu open, rather than left as a ticked row (ruled 2026-10-14);
*Open file…* (Ctrl+O) picks a file from anywhere — the picker is §3's `PickFiles`, unfiltered, and
a file that is not an archive is the core's `archive.not_an_archive` — and opens it by §14's path, and an `.efd`
dropped on the page opens the same way — the first of several, the rest named in a toast — while
a drop of anything else there does nothing (ruled 2026-10-14) — the Tampered state, the layer's foot with the countdown and Lock). Archive (**the heading is the archive's name alone**, a button to the root (ruled 2026-09-10:
no folder crumbs after it; `Crumbs` stays in `Page` for the walk upwards when a folder went);
**the list**: in a folder its first row is `..`, up one level, pinned at the top under every
sort, with no checkbox, never selected — it takes the arrows like any row but is never
selected by them, Enter or Space on it goes up, and after going up the folder just left is
selected and focused (*The lists' keys*, below); in an empty folder it is the only row — not counted by Ctrl+A, absent at the root — and
under it one line names the folder and both ways to fill it, "Trips is empty. Drop files here, or
use Add.", the root by the archive's name (ruled 2026-10-14); then the
rows in `sort`'s order (§3), directories first; **four columns — Name, Size, Type, Modified**
(*Stored as* is gone from the list; the preview's facts keep it as *Compressed*, "71% smaller"
or "No, stored as is", never the method — ruled 2026-10-17): a folder shows no size and the type
*Folder*, a file its plaintext size — right-aligned, header and cells, so the magnitudes line up
(ruled 2026-10-14) — and a type drawn from its extension — the part after the last dot;
none when the dot is the first character (`.env`) or there is no dot; `gz` for `a.tar.gz` — in
`strings.ts` —
*JPEG image*, *MP4 video*, *Text document*, *ZIP archive*, … — "*PDF file*" for an extension
without a name of its own and *File* for none; when the pane is narrow the columns give way
from the right, Modified first and then Type, Name and Size always — a hidden column's sort stays in force and
the Name cell then carries that key's chevron and name; a header cell sorts by its
column — Name and Type ascending on the first click, Modified descending (newest first), Size
ascending, the second click the other way, the cell wearing the one sort glyph of both lists,
a 10 px chevron in the third ink, turned for ascending (ruled 2026-10-17; it was an arrow
character here and a chevron on the Archives list) — and the choice stays while the
archive is open; **a checkbox heads every row** and the header row — shown on the row under the
pointer, on a selected or focused row, and on every row once anything is selected, Explorer's
own compromise (ruled 2026-10-17) — and the list is a list, not a spreadsheet: the header in
caption type on the card's ground with the one stroke under it, no rule under any row, a
row's hover a subtle fill with 4 px rounded ends inset 4 px from the card, and its selection
the accent wash with a 3 px pill at its left edge, never bold; a click on a row's checkbox toggles that
row alone and makes it the anchor (the row click's own rules do not run); the header's ticks
the whole folder — every id `Children` answers, loaded or not — and shows ticked exactly when
the selection holds every one of the folder's `Total` ids, never a third state and never for an
empty folder; ticks are the selection and the selection is the ticks — a click on the list's blank area, entering a
folder or going up clears both, nothing persists; **Ctrl+A** selects the whole folder but `..` (through `Children`) when the focus is in the list
or on the page's body — an input keeps its own select-all, and while a dialog is up the page
behind it is inert and takes no shortcut; the rest of the keys are *The lists' keys*, below; **Shift+click** extends from the anchor — the last row clicked without Shift — and with
nothing selected there is no anchor, so it selects the clicked row alone; the list always keeps
one row's height of empty space under its last row, also when it scrolls, so the last row can be
brought clear of the pane's edge; preview pane — image, video, audio through the loopback URL,
text through `PreviewText`, everything else "Extract…"; the kind comes from the name at once,
and while its URL or text is on its way the art box keeps its size and says nothing — a 14 px
ring, 2 px of the accent turning once a second, appears only once the wait passes 300 ms — so
"No preview for this type" is said only of a kind the pane cannot show, and a call that fails
says so in the pane beside its toast (ruled 2026-10-14) — the running operation and how it
ended are said in the layer's foot, the window's status bar, so an operation never moves the
list (*The foot*, below; the 42 px operation strip that stood above the list is retired, ruled
2026-10-18); no pending bar, no Save, no Discard: every operation commits at its end, §2.3), the toolbar:
one *Add* button whose menu holds *Add files…*, *Add folder…* and *Create folder…* (a staged
directory record, written at save whether or not a file was added into it — FORMAT R39),
*Extract…* (one command for the selection or the whole archive, the extract dialog's scope line
asking which, §3; titled *Extract… (Ctrl+E)*, greyed with "This archive holds nothing yet"
while the archive holds no record; ruled 2026-10-18, retiring *Extract all…*, which took the
whole archive whatever was selected, and the rule that the pane's *Extract…* was the
selection's), *Rename…* (a form: the field opens on the name with its stem — the part before
the last dot; the whole name when there is no dot, the dot is first, or it is a folder —
selected, judged as *Create folder*'s is against every name here but its own; ruled
2026-10-14; titled with the name it renames — "Rename trip-notes.md" — over a quiet line
under the field, "Enter renames. A name already in 2024 is refused here, not after.", the
field's label kept, ruled 2026-10-17), *Delete…* (a dialog first — "Permanently delete 3 files and 1 folder from ECON 280? A
folder takes everything beneath it. This cannot be undone." — whose *Delete* is a filled red
button, the danger colour as ground and white ink, never the accent's green under red text —
then the operation), the pane's
*Extract…* — the selection's shortcut, the pane's accent, greyed with "Select what to extract"
while nothing is selected — opening the same extract dialog as the toolbar's and Ctrl+E (§3;
its one info line, on every extract, says the copies are ordinary files outside Enfold's
protection) — every command
here that opens a dialog or a picker ends with an ellipsis (ruled 2026-10-14), one whose key
exists says it in its title (*The lists' keys*, below), and one that is
greyed says why in its title instead — "Select what to extract", "Select one item to rename",
"Select what to delete" (ruled 2026-10-17); a drag of the
selection onto a folder row, onto the `..` row (up one level) or onto the heading (to the
root) is `Move`,
refused in place with the reason (§3) and never a half-moved selection — the reason sits under
the target row until the next click; an op of the open archive that reports a failed or skipped
item opens the results dialog at once, and an op of another archive says it in the foot's done
line, whose *Details* opens the same dialog from any page (*The foot*, below; ruled 2026-10-18 —
it was a *What happened* dialog here and the summary as a toast elsewhere); a folder the page stood in that went (a Discard dropped it, a Delete took an ancestor)
is said by a toast naming it, the page having walked its crumbs upwards; the name column takes the width the others do not need — Size, Type and Modified are fixed;
drag-and-drop in (§3's drop) and out (§3's drag out — the staged route; the same native drag
serves the list's own moves, a self-drop being a `Move`); the extract dialog and the conflict dialogs of §3; the
locked banner — one 40 px attention line naming this archive as decrypted and still saving, with
*Close archive* and *Unlock* (*Notices and their tiers*, below; ruled 2026-10-18) — and
no timeout of the archive's own (§2.3)). Vault — the rail item and the page title (ruled
2026-10-17: they read *Keystore*, a format word, while every sentence said "vault"; the spec's
"Keys page", in older rulings, is this page, and the spec now calls it the Vault page throughout) — (*Unlock methods* with their count, "4 unlock methods" — the spec's
slots and ways in — *Add an unlock method…*,
*Remove…* — greyed while the invariant would refuse, "At least 2 unlock methods are always
kept, one of them a recovery key." — *Re-key vault…* (was *Rotate now…*; its dialog, *Re-key the
vault?*, says every unlock method gets the new key now with no YubiKey needed, and asks for a
backup first, §13), the *Password with every YubiKey* row (§13; it read *Entangled password*),
*Show recovery key…* — shown only while a recovery
slot is selected — *Backups* with its status line (*The backups card*, below; ruled 2026-10-18,
the R35 date stood as a *Last backup* fact), the consequence, *Export backup…*, *Inspect a
backup…* (was *Check a backup…*) and the import pointer,
the damaged copy when one is kept, session and appearance settings). First run (*Create a
vault…*, or *Restore from a vault or backup…*, the import of a vault or a backup (ruled
2026-10-17); a file that is only a backup leads into *Finish setting up*, and so
does a vault left half set up; a vault whose file is present and refused offers *Try again*,
the import and *Rebuild the vault…* — the create dialog in its rebuild form). Dialogs: new archive (path, name,
compression), enrollment, the recovery key's (below), remove slot, rotate, progress, extract
destination and policy, collisions, errors, import.

**The lists' keys** (ruled 2026-10-18, phase 3 of the design review's direction 7, Explorer's
habits; one pure keymap, `lib/listkeys.ts`, from a key and where it was pressed to what it
means, so vitest covers the whole table). **Arrows move the selection**, Explorer's way — this
reverses 2026-09-10's "Space selects, arrows move the focus", a checkbox list's habit that left
the preview behind every arrow. Both lists:
- ArrowUp and ArrowDown, Home, End, PgUp and PgDn make the row arrived at the one selected row
  and the anchor; the focus moves with it, the row is brought into view clear of the sticky
  header, and the pane follows. PgUp and PgDn move by the rows that fit under the header, less
  one, so the row left stays in view (at least one); End reaches the last row loaded. A step
  starts from the focused row; with the focus on the page's body, from the anchor in the file
  list and from the pane's row in the Archives list; with neither, every step lands on the first
  row but End, which lands on the last.
- Type-ahead: characters typed within 700 ms of each other build a prefix, case-folded, which
  selects the first name starting with it. One character, or the same one typed again and again,
  steps through the names starting with it from the focused row on, wrapping; a longer prefix
  searches from the focused row itself, so the row found stays found as the name is typed on.
  While a name is being typed, Space belongs to it; a character typed with AltGr (Ctrl+Alt on
  Windows) counts as one.
- Ctrl+L is *Lock now* on every page while unlocked (*The foot*, below).
The file list:
- Selection: Shift with an arrow, Home, End, PgUp or PgDn extends from the anchor by the click's
  Shift rule; Ctrl with them moves the focus alone; Space selects the focused row alone, as a
  click does; Ctrl+Space toggles it; Ctrl+A selects the folder (above); Esc clears the selection.
- The `..` row takes the arrows like a row and is never selected: ArrowUp or PgUp from the first
  row reaches it and leaves nothing selected; Home and End go to the first and last rows that can
  be selected; Enter or Space on it goes up.
- Opening: Enter on a folder opens it, and a second Enter or double-click while a folder is still
  opening is ignored. Enter on a file selects it and does nothing more until the viewer (phase 4)
  — it is never *Extract*, which is always a named command (ruled 2026-10-17). Going up —
  Backspace, Alt+Up or `..`, by key or by mouse — focuses the folder just left once its row is
  on screen, and selects it unless something else has been selected by then (in a long parent the row
  may arrive a page later, and a choice made meanwhile is not taken away).
- Commands: Backspace or Alt+Up goes to the parent; Alt+Left goes back to the Archives list,
  leaving the archive as the rail does; Ctrl+E opens *Extract…* (on the selection, or on
  *Everything* when nothing is selected, §3); Del opens *Delete…* for the selection; F2 opens
  *Rename…* for one selected row; Ctrl+Shift+N opens *Create folder…*; F5 reads the archive
  again. Alt+Enter does nothing here until phase 4.
The Archives list is single-select for the keys: Shift or Ctrl with the arrows, and Ctrl+Space,
do nothing there; the ticks stay the mouse's, and Ctrl+A ticks the rows shown. Enter opens the
row; Ctrl+N opens *New archive…*; Ctrl+O opens *Open file…* from anywhere on the page, the Filter
included (2026-10-14, a named exception to "a field keeps its keys"); Del opens *Delete archive…*
for the selected row, greyed by the command bar's rule; F2 puts the focus in the details' Name
field with the name selected, raising the narrow panel first when it is down; Ctrl+F focuses the
Filter; Alt+Enter opens *Technical details…* for the selected row; F5 reads the list again; Esc
collapses the panel when it is up (ruled 2026-09-11) and is otherwise left alone, so the Filter's
own Esc still clears it.
**Where a key acts**: a list's own keys — the moves, select, toggle, select-all, open and
type-ahead — act while the focus is in the list or on the page's body; a page's commands act
from anywhere in the window but a text field, which keeps its own keys, Esc and the Archives
page's Ctrl+O excepted — the rail and the foot included, since the page reads its keys on the
window. A control of its own inside the list's region — a column's sort button,
the tick-all box, the empty vault's *New archive…*, *Open an .efd file…* and *Import…*, the
Missing chip — keeps its own Enter and Space, so the headers sort and the boxes tick from the
keyboard; a row's own tick box (outside the Tab order, reached by a click) is part of its row,
so Space there selects as on the row. While a dialog is up, or from an open menu or popover, no
list key acts. The focus ring is `:focus-visible`'s (*The focus ring on a selectable row*,
Motion). **A command whose key exists says it in its title** as "Label (Key)" (`strings.ts`
`keyHint`, `keyTitles`): *Extract… (Ctrl+E)*, *Delete… (Del)*, *Rename… (F2)*, *New archive…
(Ctrl+N)*, *Open file… (Ctrl+O)*, *Delete archive… (Del)*, *Technical details… (Alt+Enter)*,
*Filter (Ctrl+F)* and *Lock now (Ctrl+L)*; a greyed command keeps its reason as its title
instead. *Create folder… (Ctrl+Shift+N)* is written but not yet shown, since its one control is
an item of the *Add* menu and a menu item has no title yet.

**The foot: the window's status bar** (ruled 2026-10-18, phase 3 of the design review's
direction 4, "status gets a home and an ending"). The layer's foot (`LayerFoot`; Motion, *Pages*)
is 34 px and always there, and it is where status lives, so an operation never moves the list:
the 42 px operation strip that stood above the list, and pushed it down as an operation began,
is retired. 2026-09-11's rule for the drag out's strip, "a button that vanishes reflows the
strip; a greyed one keeps its shape", is kept by a slot that never reflows at all.
- **The left slot** says the page's note at rest; the running operation while one runs; then how
  it ended, the done line; then the note again — one at a time, cross-fading in 140 ms and out
  in 100 ms. A done line holds its time even while another operation still runs, and the slot's
  *Cancel* and *+N* wait behind it; a flyout already open keeps the running operations' *Cancel*
  meanwhile.
- **The running line**: the operation's name with its count and its archive (`lib/ops.ts`
  `opLabel`) — *Adding 3 files to Photos 2024*, *Adding 1 file to …*, *Refreshing the
  fingerprint of Photos 2024* (*Verifying …* until 2026-10-18, §2.3), *Reclaiming space in
  Photos 2024*, *Re-keying Photos 2024* (ruled 2026-10-17: the button's verb), *Compacting
  Family videos*, *Extracting 13 files from Photos 2024* — since the foot shows every running
  operation, the Archives page's included, on whichever page is up (ruled 2026-10-14: the verb
  alone did not say whose it was); then a 160 × 3 px progress line, the accent filling
  `--fill-2` by bytes, a moving 40 px segment while the total is not known and a still pale line
  under reduced motion; then "9.4 MB of 28 MB · about 2 min left" or "· less than a minute
  left". The rate is the bytes the last 5 s moved over those 5 s; it is said only after 3 s and
  only while more than 10 s remain, and the minutes round up, so "about 2 min" is never a
  promise kept late. Then a 24 px subtle *Cancel* where a cancel means something (§2.3, *What a
  cancel means*), which reads *Cancelling…*, greyed, from the press until the operation ends
  (ruled 2026-10-14) — a refusal (`op.committing`) gives it back, with its toast. A compaction
  and a re-key show no *Cancel*. When the foot is narrow the extras give way so the name stays
  whole: first the time left, then the line's length (160 px down to 64), and only then does the
  name take an ellipsis (measured whole at a 1024 px window).
- **The drag out** in the foot keeps its three phases (§3): the hover, *Dragging 4 items from
  Photos 2024*, the label alone; the staging, *Extracting 4 items*, with the line by bytes and *Cancel*; Explorer's
  wait, the line full and *Cancel* greyed in place, until `DoDragDrop` returns.
- **Two or more running**: the latest begun shows, with a subtle *+1* (*+N*) after it, which
  opens the operations' flyout — a 360 px popover over the foot at its left, the menu's surface,
  stroke and shadow — one 40 px row per operation, ruled off from the next: its name, under it
  the bytes and its own time left, under both its line across the text's width, and at the right
  its *Cancel*, or *Runs to the end* for a compaction or a re-key. Esc, a press outside, or fewer
  than two running closes it.
- **The done line**: how an operation ended, said in the foot on whichever page is up — an
  accent check (it went as asked) or the info circle in the second ink (a stop, or an ending that
  left something out), one sentence with no closing full stop, as a status bar says it, and at
  most one hyperlink button: *Show in Explorer* (`Shell.Reveal`) or *Details*. It holds 6 s, or
  until the next press in a page's body; an ending that arrives while another is up waits its
  turn and is said when that one's time is up or a press moves past it, at most 3 waiting (past
  that the oldest waiting goes unsaid); a new operation's start clears the line and everything
  waiting, so the press that started it is answered by it, never by an old ending. An operation
  the core starts itself — the reclaim a commit's ending can set off at once — does not clear the
  line or the queue (`lib/foot.ts` `clearsDone`); it shows once their turn is over. The
  sentences are `lib/foot.ts` `doneLine` and `strings.ts` `doneCopy`: "Extracted 15 files to
  D:\Archives\Photos 2024" [*Show in Explorer*]; "Extracted 12 of 15 files to …" [*Details*] when
  some were left out; "Added 2 files to Photos 2024", "Added 2 of 4 files to Photos 2024"
  [*Details*], "Added 1 folder to …" when only folders were made (folders made along the way are
  never counted as files), "Replaced 1 file in …"; "Compacted Photos 2024: 2.9 GB given back"
  (§2.3); "Reclaimed 1.2 GB", said only when something came back; "Fingerprint refreshed for
  Photos 2024"; "Re-keyed Photos 2024"; "4 items left Enfold as ordinary files" for a drag out
  that moved or copied. A user's stop, neutral: "Stopped. 6 of 15 files were written to …"
  [*Show in Explorer*] or "Stopped. Nothing was written"; "Stopped. Nothing was added to Photos
  2024"; "Stopped. Nothing was replaced in …"; "Stopped. The fingerprint of Photos 2024 was not
  refreshed"; "Stopped. Reclaiming space in Photos 2024 did not finish"; "Stopped. Photos 2024
  is as it was" for a compaction or a re-key the core stopped. This retires 2026-10-14's neutral
  "Adding 3 files: Stopped." toast; a reclaim's figure moves from its toast to the line; another
  archive's trouble, a toast until now, is the line with *Details*. A failure keeps its error
  toast and gets no line (InfoBars are phase 4). An extract under *Ask* that wrote nothing and
  comes back with a question — a conflict, a refused name or path — says nothing, the question
  being its news; one that wrote some says "Extracted 9 of 13 files to …" [*Show in Explorer*]
  under its question, and each re-issue its answer makes has a line of its own. Two endings of
  the page's own also take the line: *Find missing files* (§13) and a backup export, "Backup saved
  to E:\backup-2026-10-18.eks" [*Show in Explorer*], said once the export's dialog is closed on
  its Done (a backup taken before a re-key says none: the re-key is the act that ends there).
- **The results dialog** (`ResultsDialog`; §3, `FileOutcome`) says what a batch left out. It
  stands above every route: it opens at once on the archive's page where the batch ran, and from
  the done line's *Details* on any page. Its title is the outcome, counted — "2 files were not
  added", "1 file was skipped", "2 files were not added and 1 was skipped", the verb the kind's
  (added, replaced, extracted), *folders* when every row is a folder and *items* when they mix —
  with the summary line under it as a caption ("2 files added · 1 folder created · 1 skipped").
  Each row reads the name strong, then a colon and the code's copy ("thumbs.db: …"), a folder as
  "Trips (folder)", an unknown code's fallback with the code in mono, and *Show in Explorer*
  where the item is a file on disk this machine can open — an add's or a replace's source, or an
  extract's skipped file, which is the one already in the destination; never an archive path. The
  first 12 rows and a count of the rest. *Copy details* and *Close* are equal standard buttons,
  nothing here being the suggested act; *Copy details* writes the title, the summary and every row
  through the runtime's clipboard (`Clipboard.SetText`, as every Copy does) and toasts "Details
  copied". It closes on the backdrop as well, its one act being *Close* (ruled 2026-10-18: it was
  *What happened*, the count first, rows as "name — reason", with *Close* the accent).
- **The rail's mark**: while anything runs, the rail's *Archives* item wears an 8 px accent dot
  at its right edge, 12 px in and centred in its 36 px row, seen from every page — a 48 GB
  extract started before going to the Vault page stays in sight; while exactly one runs with a
  known total, the dot is instead a 14 px ring, 2 px wide, filled to its percentage (the accent
  over `--fill-2`, moving over the fast duration).
- **The pending ring**: a wait the user started marks the control that started it only once it
  has lasted 300 ms, so a quick answer never flashes anything (`lib/pending.ts`, one primitive):
  the 14 px ring — 2 px of the accent on its pale line, one turn a second, still under reduced
  motion, in the ink of a filled button over one — standing where the button's icon was, and
  `aria-busy`. The label changes only where the verb adds something, and only to the one verb set
  (`strings.ts` `pendingCopy`): *Opening…* (the Archives page's two *Open* buttons), *Checking…*
  (*Find missing files*), *Deleting…* (*Delete archive*'s primary), *Merging…* (the import's
  merge, which read *Working…*) and *Saving…* (the save bar's *Save changes*, greyed from the
  press). A control whose label changes is as wide as the wider of its two labels from the
  start, so nothing beside it moves. *Forget archive*'s primary and *New archive*'s *Create
  archive…* keep their labels and show the ring alone. *Open* from the list — a double-click or
  Enter on a row — puts the ring in the row's status cell and ignores repeats while one is in
  flight. The preview pane's wait is the same ring under the same 300 ms (above).
- **The right slot** is the lock state, as Motion's *Pages* says, with two additions. In the
  last 60 s of the idle deadline only — when it is the nearer one, the count in the attention
  ink — a subtle *Stay unlocked* stands before *Lock now* and sends `Vault.Activity()` at once,
  which moves the idle deadline and nothing else; the absolute cap's last minute offers nothing,
  since nothing extends it, and the too-slow refusal (`op.too_slow_for_session`) says the way
  out: "This would not finish before the session ends. Lock and unlock to start a fresh
  session, then try again." (it said "Extend the session first", which nothing can). And while
  unlocked, the **standing notices** — the status's warnings, the Tampered ones left out, since
  their banner is the page's own — gather behind a subtle *1 notice* / *2 notices* button with a
  6 px attention dot, before the lock chip; it opens the same popover at the foot's right, one row
  per warning ranked by tier (*Notices and their tiers*, below), each with its tier's icon in its
  tier's colour and its words. The `vault.warning` toast still marks the moment one arrives
  (ruled 2026-10-13), and the lock screen keeps its list. One popover at a time. Ctrl+L presses
  *Lock now* on every page while unlocked: the key is read by the foot, the one place on every
  page, through the lists' keymap (`lib/listkeys.ts` `locksNow`), so a text field, a dialog, a
  menu or a popover keeps it, and the button is titled *Lock now (Ctrl+L)*.

**The import dialog** (lock screen and first run; titled *Replace this vault with a copy* on the
lock screen and *Restore from a vault or backup* at first run, after the commands that open it —
ruled 2026-10-17) picks the file, shows what it is — vault or
backup, its date, its ways in by kind (the fact reads *Unlock methods*), whether it claims to be this vault and newer — and says
what will happen before the button is pressed: a vault is copied in and must unlock with one of
its own ways in (chosen here: "prove it with"); a backup is copied in and opened with its recovery
key, then takes the first way in chosen on the same cards as the create dialog's (below; headed
*How you will unlock the imported vault*, its recovery line "Recovery key — the backup's own. It
opens the backup first and stays." (ruled 2026-10-17; it was a card),
since a backup keeps its own and no new one is made, and its line under the cards naming the
backup's recovery key first) — the password, standalone or with every YubiKey, chosen afresh
(FORMAT R28) and typed there, answering the Choose prompt the ceremony asks after the recovery
key (ruled 2026-10-17); with a vault kept
here, a confirmation names the vault being replaced and its date, says it is kept as a dated copy
whose ways in stay valid, and, for a backup, that archives added since its date are not in it —
the button stays disabled until it is ticked. "Use the file where it is" is an advanced link for
a vault, with the warning that removable media and network shares are outside Enfold's
protection; choosing it turns the confirmation into "switch to this file — it stays where it is".
With archives open, both dialogs say so and their buttons stay disabled. The create dialog shows
where the vault will live on one caption line 4 px under the name — *Kept in*, the path in mono,
then the button (ruled 2026-10-17) — and offers *Keep it elsewhere…* (once a place is chosen, *Use
the usual place*) as a hyperlink button in the line's caption, not a field. The line never wraps
(ruled 2026-10-17: the full path pushed the button to a row of its own): the path is cut in the
middle to what the row holds beside the button — the drive and the file name kept whole, the
folders nearest each put back as they fit, "…" for the rest (`lib/paths.ts` `middlePath`) — and
the full path is its title; in its rebuild
form (§2.1) the place is pinned to the damaged file's — no button — and the tick "keep a copy of
the damaged file and start a new vault" is the one consent (cleared each time the dialog opens);
over a file that is simply absent it shows no replacement bar and no tick, since nothing is
retired; over some other file already at a chosen place it names the dated copy that keeps it.
A create ends in the vault with the recovery key's dialog over it; only when the created vault
did not open on its own does the lock screen say "Created, but it did not open on its own.
Unlock it with the unlock method you chose." (ruled 2026-10-17: the screen's word for a way in
is *unlock method*); a ceremony that fails without parking is shown on the
first-run card as well. The
first-run
screen says to have the recovery key ready before importing a backup, because a backup opens
with nothing else; when a configured vault could not be opened it names the file and offers
"try again" and import before "create" (the file is absent) or "rebuild" (the file is present
and refused: the dialog is titled *Rebuild the vault*, says the file is kept beside the new
vault as a dated damaged copy and that the archives it held the keys to open only with it, and
its tick reads "keep a copy of the damaged file and start a new vault"); when a replaced copy
exists and no vault is kept, it names the copy and offers to restore it (*Restore it…*). With a
vault kept here the lock screen offers *Replace this vault with a copy…* and *Test that a backup
opens…* (ruled 2026-10-17; they were *Import a backup or a copy of this vault…* and *Check that a
backup opens…*), behind its *Backups and copies…* menu (ruled 2026-10-17), never a second vault. A vault with only its
recovery key shows *Finish setting up* as its one action, with "unlock with the recovery key
only" beneath. Precedence on the lock screen: Busy, then Broken, then setup needed, then the
ways in; Tampered is a banner over any (the danger notice, *Notices and their tiers*, below).

**The backups card** (ruled 2026-10-18, direction 9 of the design review; it was a line on what
a backup is, the buttons, and a *Last backup* fact — "<date>; recorded here, not proof the file is
still there" or "None recorded here" — which dated a backup and said nothing of what it holds) is
a receipt: the status, then under a date its caveat, then the consequence, then the buttons, then
the import pointer (§13). The status compares the last backup with what the vault holds now, by
`Vault.BackupStatus` (§3) — archives created since and archives re-keyed since, never the vault's
last change — and `lib/backup.ts` chooses one of four, <since> being `sinceText`'s relative date,
since recency is the point:
- *never* — no backup recorded, archives in the vault: the attention bar with the backup icon,
  "**No backup is recorded here.** All <n> archives are only in this vault file." ("Your 1
  archive is only in this vault file." for one);
- *behind* — a backup that lacks archives: the same bar, "**Backed up <since>.** <n> archives
  created since are not in it.", or "<k> archives re-keyed since are not in it.", or "<n>
  archives created since and <k> re-keyed since are not in it.", the verb singular for one;
- *current*: a plain line, no box, the check in the accent's ink before "Backed up <since>. It
  holds every archive key.";
- *none* — no archives and no backup: no status.
Under a dated status, and only there, since it is about a recorded backup, stands the caption
"Recorded here, not proof the file is still there." The consequence follows in the body: "Your
archives open only with this vault file or a backup of it. The recovery key opens either one; it
cannot replace them." *Export backup…* is the accent while the status is a counted *never* or
*behind* and standard otherwise; *Inspect a backup…* is standard. The counts are believed only
from a read made while Unlocked and not Tampered: while Locked or Tampered, and from a change of
state until the new state's read lands (the store drops its read on every change of state), the
status is the plain line with the quiet backup icon and no check — "Backed up <since>." or "No
backup is recorded here." — claiming nothing about what a backup holds, and the export is not
suggested. The status is read at boot, on unlock, after an export and on `archives.changed`.
Like `lastExportAt`, it words and suggests and gates nothing.

**The recovery key's dialog** — after a create, after enrolling a recovery key, and after *Show
recovery key…* on the Vault page — is 520 px wide (ruled 2026-10-18, between the default 460 and
the wide 640: the eight groups at the sub size and the readback's two fields want the room) and
titled "Your recovery key" at its first showing and "Recovery key" when shown again, with the
key's ID once beside the title as the heading's label, in mono at the section size and the
second ink (ruled 2026-10-18; it stood on a line of its own above the digits) — the groups'
checksum catches a mistyped group, the ID the wrong sheet (FORMAT §18.4). Its lead is the two
risks in two sentences (ruled 2026-10-18): "With this key, the vault file or a backup opens
without your YubiKey. Keep it from others." and "Without the vault file or a backup, this key
opens nothing. Keep those too." Then the eight groups; under them, on one row at their own width,
*Print…* and *Save as a text file…* — the two ways that leave the machine, beside the digits they
act on — and *Check what I wrote*, a small subtle button that opens the readback (ruled
2026-10-18, the user's, after seeing the build: the band had held all three ways, equal and
wide); then "48 digits. They can be shown again from the Vault
page: unlock, then prove a YubiKey or a password once more." — the page named as the rail names
it, never a "Keys page" no screen shows (ruled 2026-10-14; the rail's name is *Vault* since
2026-10-17); then the text file's caution as a caption, "A text file is plain text. Keep it off
this PC and out of any folder that syncs." It offers three ways out (§3 Keys), and *Print…* is
the suggested one — a sheet on paper, away from this PC: before any is taken *Print…* wears the
accent and the document icon in the row, and the band holds one button, *I have written it
down*, standard.
*Save as a text file…* opens the native Save dialog at once (ruled 2026-10-18; a dialog of its
own, *Where to keep it*, came first, its caution now the caption above), and the core still
refuses Enfold's folder and the vault's (`vault.recovery_place`). Each way taken is said in the
body by an accent bar with its check — "Saved to <path>.", "Sent to print as “<title>”.",
"Written down." — and none asks a second time (ruled 2026-09-09): after a save the core
acknowledged, a print, or *I have written it down*, the band's one button is *Done* (accent; one
click closes) and the row's two are standard, each worded *Print again…* or *Save again…* only
once that way was actually taken (`lib/reveal.ts` `ways`, `band`). The saved file and the print's
suggested name are both `Enfold Keystore Recovery Key <ID>.txt` — BitLocker's own shape,
"BitLocker Recovery Key <id>", with the key's ID (FORMAT §18.4) and never the vault's name, which
says nothing about which sheet this is; the same string heads the text file and the sheet, and
the page sets `document.title` to it for the print, which is what Print to PDF offers as the file
name. **The readback** (ruled 2026-10-18) is optional and never a gate: *Check what I wrote*
shows two of the groups, "Group N" and "Group M", distinct and drawn at random once per dialog,
each a field of six digits (whatever else is typed or pasted is dropped); nothing is marked
before six digits, a match gets the accent tick and a mismatch the danger underline with "Compare
it with what you wrote."; the caption "Only a check: nothing here is required." stands under the
two fields. It
compares in the page's memory only — the digits never ride a bound call — its fields are cleared
with the dialog, and nothing in the band waits on it, so 2026-09-09's "nothing asked twice"
holds. When the fetch of the one-time URL fails (a window recreated after the one fetch), the
dialog says the key could not be shown here and can be shown again from the Vault page, with
*Done* as the one way out. It is not dismissed by Escape or the backdrop, and it closes when a
lock trigger locks the vault it was shown on.

**The create dialog's first way in is a group of cards** (ruled 2026-10-17, the user's, for
direction 1), headed *How you will unlock this vault*, in this order: *YubiKey* ("PIN and touch.
Recommended."), *Password* ("At least 8 characters."), and under them, 4 px below as the cards are
below each other, the recovery key, which is not a choice and so not a card: one caption line
with its icon, "Recovery key — always made. You will print it or save it right after." (ruled
2026-10-17; it was a third card, and with a card open the dialog outgrew an 820 px window). The
cards are compact (ruled 2026-10-17): 4 px apart, their inset 12, a strip's, and 8 between the
parts of an open card but 12 between its two password fields. The two choices are
a radio group whose cards are labels round real radios: the arrow keys move the choice and the
card follows, the card's whole head is the click target, and the focus ring is the card's. Until
one is chosen — nothing is, as the dialog opens — every card shows only its icon, title and line;
the chosen card opens on its fields as the other closes, its row growing from nothing over 220 ms
on the entering curve (none under reduced motion). The YubiKey's card holds *Name this YubiKey*,
optional, and the tick "Also require a password with every YubiKey in this vault", with its hint
under it in a caption, ticked or not: "Every YubiKey added later will ask for it too; the recovery
key never does." (ruled 2026-10-17: the vault's fact the old notes said, back as the option's
description); ticking it opens *Password* and *Type it again* under the tick. The Password card
holds the same two fields, and its line folds away as they open (ruled 2026-10-17): the
requirement it states is the rule under the first field, said once, in the one place the form
rule turns red — the YubiKey's tick opens the same two fields with no such line above them; a
password unlock method has no name field — it is "Password". **The chosen password is typed in
the dialog**, never in a later step: the core asks it first, before the YubiKey is set up (§3),
so the dialog's *Create vault* judges the cards under the form rule (below: no choice made, the
minimum of 8, the two copies the same — each refusing part marked on the press), starts the
ceremony with the kind, the name and the tick alone, and the page answers the ceremony's first
prompt, the `Choose` password prompt, at once with the typed password — on the raw channel under
that prompt's id, never as an argument of the bound call (§1) — and forgets it. The store holds it
from the press until that prompt and nowhere else, and drops it unsent at every other end: the
call refused, the ceremony failed, blocked or cancelled first, the panel dismissed for another
ceremony; a dialog closed without its button takes it nowhere. So the panel never asks the `Choose`
prompt of a create in the normal case — it says "Using the password you chose." for the moment
the answer is in flight — and asks it as before, two fields under one *Continue*, only when the
core refuses the answer all the same (`vault.password_short`, which the dialog's judge makes
unreachable). A card or a tick that closes empties its fields. Under the recovery key's line one
line says what comes next and follows the choice — "Next: insert the YubiKey, enter its PIN and
touch it. Then your recovery key is shown once." or "Next: your recovery key is shown once." — a
caption, empty until a choice is made and one caption line tall then (ruled 2026-10-17; it was
two lines of body either way, which left 40 px empty under the shorter lines). Enter in the name
or a password field presses *Create vault*, and the dialog opens with the focus in the name (the
form dialogs, below). The create dialog's tallest state, the YubiKey's card ticked, fits an 820 px
window whole (775 px), and so does the import dialog's backup with the Password card open (779);
the import dialog's backup with the YubiKey's card ticked does not — its file row, facts and name
above the cards make a body of 864 px against the 705 an 820 px window leaves it, so that body
scrolls — and at 1024 × 768 the create dialog ticked and the import dialog's Password card scroll
by about 50 px (measured 2026-10-17; fitting those needs shorter import copy or a wider import
dialog, not yet ruled). *Finish setting up* uses
the same cards, its recovery line and its next line those of an adopted backup (the import
dialog, above). The enrol dialog names the key, never the secret: a token gets "Name this key" (the label
shown in the list of unlock methods), and a password is chosen in the ceremony that follows,
because the vault must first check an unlock method. The enrol dialog is *Add an unlock method*,
its kind field *Unlock method*, and a recovery key's name field says "Shown in the list of unlock
methods." (ruled 2026-10-17; it was *Add a key*, *Kind* and "ways in"). A YubiKey's name is
optional, in the create dialog and the enrol dialog alike, and its empty field says what an empty
field produces — "Named after its serial number", the core's "YubiKey 12345678" — never an
invented name that reads as a default already filled in; a recovery key's, which must be named,
shows no placeholder at all (ruled 2026-10-14).

**Notices and their tiers** (ruled 2026-10-18, direction 9 of the design review: every notice
was one amber bar, so a possible attack, a weakened guarantee and a repair that heals itself
looked alike). A notice has one of three tiers, fixed by its code (`lib/notices.ts`). The tier
sets its colour, and sets its icon (`tierIcon`: the error circle, the warning triangle, the info
circle) unless the notice carries its own: the locked lines and the archives-open notice carry the
closed padlock, and the backups card's bar carries the backup icon. It sets nothing else:
- *danger* — the danger wash and line, the error circle: the vault file may have been changed
  outside Enfold. `vault.tampered` and its two reasons, `vault.tampered_hash` and
  `vault.tampered_generation`.
- *attention* — the attention wash, the warning triangle: a guarantee is weaker than it should
  be. The YubiKey that may still be PIN-verified (`token.reset_failed`), workstation-lock
  detection unavailable (`shell.lock_detection_unavailable`), the settings not written
  (`settings.unsaved`), a backup missing or behind (*The backups card*, above), archives open while the
  vault is locked (below), and every code no one has classified — a warning is worth a look until
  someone has said it is not.
- *info* — the plain bar (`--surface-alt`, the second ink) and the info circle, never amber: it
  heals itself or only informs. `vault.stale` (the next write repairs the other superblock copy)
  and `vault.timeouts_clamped`.
Two things have no tier: the *Locked* chip on the Archives and Vault pages' heads — it was
amber; locked is the safe state, so it is the plain chip in the second ink with the closed
padlock — and the touch's timeout, which was never a bar and stays the neutral message of Motion.
A failure that stops a ceremony or a dialog — a refused unlock, a blocked PIN, an import that could
not read its file, a delete the core refused — is a moment, not a standing notice, so it is outside
the table; its bar wears the danger wash and the error circle all the same (ruled 2026-10-18; it
wore the warning triangle), and so does the import dialog's line over a file that fails its own
slot-region hash (§13, `IncomingFile.Tampered`; it was attention), for the reason the Tampered
banner is danger. The recovery key's dialog, told that its one-time link was already used, says so
on the plain bar: it only informs.
The state sets the place. On the lock screen the notices stand above the panel at its width in
one ranked list — danger, then attention, then info, the core's order kept within a tier — and
the archives still open rank among them as attention, under the Tampered banner and above an info
line. While the vault is unlocked the Tampered banner is the first element of a page's body,
above its command bar (*The Tampered banner*, below); a page's other notices keep their places —
on an archive's page the locked line, `archive.needs_reopen` and `archive.copy_mismatch` stand
under the command bar — and the status's standing warnings, the Tampered ones aside, are
gathered behind the foot's *1 notice* button, whose popover lists them in this ranked order with
each tier's icon and colour (*The foot*, above; ruled 2026-10-18, direction 4 — it was "later"
here), while a warning that arrives meanwhile is still marked by the `vault.warning` toast. The notices
that carry a lead open with their first sentence in the strong weight (600, the ink), the rest
following in the body weight: the Tampered banner, the locked lines of the Archives page and an
archive's page, the archives-open notice and the backups card's bar. The split is written into
their copy (`strings.ts` `Lead`) and never found by searching for a full stop, since an archive's
name may hold one. Every other notice is drawn unsplit in the body weight, whatever its length:
the lock screen's warnings other than the Tampered one (`vault.stale`, `settings.unsaved`,
`token.reset_failed` and the rest) and an archive's page's `archive.copy_mismatch` and
`archive.needs_reopen` bars.

**The Tampered banner** (danger) stands first among the lock screen's notices and is the first
element of every page's body while the vault is Tampered — the Archives page (above its command
bar), an archive's page, the Vault page and Settings, Settings opened from the lock screen's gear
included — so a vault file that may have been changed outside Enfold is never news to the lock
screen alone (ruled 2026-10-18; it was an amber bar on the lock screen, the Archives page and the
Vault page). Every one of these surfaces draws it from one predicate, `VaultStatus.Tampered` or a
`vault.tampered` warning (the store's `tampered`, `lib/notices.ts`), since the two disagree after
a lock: the lock clears `Tampered` and `TamperedReason` and keeps the warning until a clean
unlock, a replace or another vault (§3), so the lock screen and Settings opened beside it never
disagree. After a lock the reason is empty, and *Why?* says the general sentence. It reads the
same for either reason: "**This vault file does not match itself, so it may have been changed
outside Enfold.** Changes to the vault are off until you replace it with a
copy you trust. Your archive files are not affected." — the last sentence answering the fear the
first raises, since the freeze is the vault file's and the archives keep saving (§2.3, §3
*Tampered is a state*). Under it, on one row, *Replace with a copy…* (a small standard button)
and *Why?* (a hyperlink button). On the lock screen *Replace with a copy…* opens the import
dialog; on a page it locks the vault first if it is unlocked (`Vault.Lock`) and goes to the lock
screen, which at rest opens the import dialog once, on a copy, since the import runs only there. A
vault already Locked — Settings opened from the gear, or a page left open after a lock — is not
locked again, since a lock in any state still drops the `token.reset_failed` notice, a pending
touch and a merge's records; the route alone ends the visit. A route that leaves the lock screen
before it reaches rest withdraws the ask. *Why?* opens *Why
changes are off*, a dialog whose one action is *Close* (so the backdrop closes it too), holding
`technicalCopy`'s sentence for `TamperedReason` — which check failed, in the second register.
The Vault page's acts, the Archives page's *Compact…*, *Re-key archive…* and *Refresh
fingerprint*, and the *Refresh fingerprint* in an archive's page's `archive.copy_mismatch` note
are greyed while Tampered with the title "Changes are off until this vault is replaced with a copy
you trust; the banner above has the button." (`vaultCopy.changesOff`). The other changes the state
greys are not titled so: an archive's page's *Delete archive…* keeps its own title, "This vault
file does not match itself, so changes to it are off." (`archivePageCopy.deleteTampered`), and the
Archives page's *New archive…* (in the command bar and in the empty list), *Import archives from a
backup…*, *Forget archive…*, *Delete archive…* and *Restore* carry no title for it (*Forget
archive…* and *Delete archive…* show the compaction's title while one runs; the others none).

**Archives open while the vault is locked** (attention, with the closed padlock) are named from
`VaultStatus.Opened` (§3) and said for what they are — decrypted, and still saving, since every
operation on an open archive commits to its file and owes the vault its receipt (§2.3, *What
stays usable after a lock*; ruled 2026-10-18: the lock screen said "browsing works, saving needs
the vault", the Archives page "Open archives can still be browsed; everything else needs the
vault" and the Archive page "This archive stays open while you are here", each understating what
an open archive still does). One archive is named, several are counted, in three places:
- the lock screen, among the notices: "**<name> is still open in Enfold, decrypted.** Changes to
  it are still saved." or "**<n> archives are still open in Enfold, decrypted.** Changes to them
  are still saved.", and under it *Show* (a hyperlink button: to the archive's page when one is
  shown, else to the Archives page), then *Close it* or *Close them* (a small standard button:
  `Archives.Close` of the one, `Archives.CloseAll` of several);
- the Archives page, its 40 px locked line: "**The vault is locked.** <name> is still open:
  decrypted, and changes to it are still saved." or "**The vault is locked.** <n> archives are
  still open: decrypted, and changes to them are still saved.", with *Close archive* or *Close
  all* (small standard) and *Unlock* (small accent) — the page stands while Locked only with an
  archive open, the lock screen otherwise;
- an archive's page, its 40 px locked line: the one-archive sentence for this archive, with
  *Close archive* — the head's own kill switch, the same act — and *Unlock*.
A close from these that closes the archive whose page is shown drops that page as closed, and an
archive `CloseAll` kept — one being compacted holds its handle — is said with
`archive.compacting`'s toast. The Vault page's locked line ("Unlock the vault to change its
unlock methods." with *Unlock*) names no archive and stays the plain bar.

**Forms and their errors.** A field that holds something wrong is marked once the user leaves it
— the underline turns red, and nothing else about the field does (no frame and no ring, whether
the field is marked by its class or by `aria-invalid` alone; ruled 2026-10-14), and, beneath it,
either the requirement already shown there turns red
("At least 8 characters.") or a line says what is missing — and the mark goes the moment the
value is right, to return only after the field is left again with something wrong. Typing never
marks in a form with a button (the recovery key's checksum, below, is judged as each group is
finished; the save bar's fields, below, as they are edited), so a field left right and then
emptied says nothing until it is left again; and leaving is focus moving to something else in
the page — the window losing focus (a switch to a password manager, a click on another window) is
not, so an untouched field focused for a prompt comes back unmarked, and neither is a field taken
out of the page from under the focus (the merged card turning into the password alone once the
PIN is right). Pressing the form's button marks every field that would refuse, an empty required
one with "This field is required.", and only those. A new prompt clears every mark the form made
itself, even where a field keeps its value (the merged card's password across a refused PIN);
the core's refusal under the new prompt ("Wrong PIN.", a wrong password) is said as before, and
a password the page would refuse when the accepted PIN would send it is marked as if the button
had been pressed, and waits for *Continue* (ruled 2026-10-13). The button is never disabled for
an empty field, only for a consent not yet given (a replacement's tick) or a state that forbids
the action (archives open). The lines fade in and out like everything else. Every typed secret
is judged before it is sent — a chosen password's
minimum of 8 characters (BitLocker's rule; the core refuses a shorter one at submit with
`vault.password_short` and keeps the prompt), the recovery key's 48 digits with any whitespace or
dash between them (the set the core ignores), the management key's hex with the whitespace the
core ignores — and an existing secret is never measured: it is what it is, so a PIN is refused
only for what the card itself refuses (more than 8 bytes), never for being short. A name inside
an archive — *Rename…*, *Create folder…*, a refused name's *Rename…* — is judged trimmed, as it
is sent, by R20 itself, the core's own rule mirrored exactly (`fileNameProblem`, ruled
2026-10-14; it checked only the slash, and the rest came back as the generic "That name cannot
be stored."), and the line says which part it breaks, in the core's order: "At most 255
characters." (UTF-16 code units, as the core counts them — an emoji is two), "A name cannot
contain `< > : " / \ | ? *`", "A name cannot contain a tab or another control character." (C0,
DEL and C1), "A name cannot end in a dot or a space." (`.` and `..` among them), "That name is
reserved by Windows." (CON, PRN, AUX, NUL, COM1–9, LPT1–9, with or without an extension); then,
for a new name, "There is already something with that name here." **The
recovery key is typed into eight cells**, plain digits — they are read back against paper, not
hidden — each moving on when its six are in; a paste fills them all with the dashes stripped; a
click lands on the first cell still to be typed, or, once all eight are in, where it was aimed
with the group selected for retyping; the cells are one tab stop; a finished group is judged
against BitLocker's checksum (divisible by 11, below 720 896) at once, before anything is sent —
a tick when it passes, a 2 px danger underline and its number in the line under the cells when
it fails (ruled 2026-10-17; the whole cell turned red, and the line explained the checksum
without saying which group); and when the core says the key did not open the vault, the digits come back for
correction — the store keeps them across the derivation step, which unmounts the field.

**Motion.** One vocabulary, in `lib/motion.ts` and the stylesheet's `--dur-*` tokens, kept
equal by hand: *tap* 90 ms (press feedback), *hover* 120 ms in and 160 ms out (a control
answers at once and settles when left), *leave* 100 ms, *fast* 140 ms (enter), *move* 220 ms
(the only long one: a change of scene), *settle* 320 ms (the one deliberate pause, below),
*gap* 60 ms (what arrives waits this long for what leaves). Entering eases out
(`cubic-bezier(0.2, 0, 0, 1)`); a thing that travels away eases in; a crossfade's exit eases
*out* — fast first, 80 ms for a page — so that no two texts share a spot at readable opacity,
which is what a ghost is. Nothing travels more than 12 px; one thing moves at a time, staggered
by at most 40 ms; nothing loops but the touch rings, the 14 px waiting ring of a preview on
its way (ruled 2026-10-14) and of every other wait the user started (the pending ring, *The
foot*, ruled 2026-10-18), the foot's moving segment while an operation's total is not known, and the YubiKey's LED on the lock screen while it listens (ruled
2026-10-17: there the motion is the state), each standing still under reduced motion.
`prefers-reduced-motion` zeroes every duration — the script reads the media query when a
transition starts, the tokens collapse to 0 — and the settle with them.
- *Unlock.* One panel changing in place (ruled 2026-10-17, in place of the three cards' motion;
  every duration is a token of the vocabulary above, and reduced motion zeroes it):
  - *Listening for the YubiKey*: the LED on the key art breathes, its opacity 0.35 to 1 and back,
    a 1.6 s loop eased in and out; still under reduced motion, where the lead says "Waiting for
    your YubiKey…".
  - *The YubiKey found*: the LED holds solid for 200 ms while the step's disc pops its check
    (180 ms, the pop curve); then the panel cross-fades to the PIN's step, the old content leaving
    over 140 ms fast first and the new arriving over 140 ms a gap (60 ms) later — every view of
    the panel crosses so, its views sharing one place. The leaving view leaves as it was: it never
    redraws with the next step's facts (the PIN's fields stay as the touch arrives, the touch's
    words as it times out). The panel's height eases from the old view's to the new one's over
    the same 220 ms as a scene change, on the entering curve, what overhangs it clipped at its
    stroke meanwhile; between crossings it is the view's own, so a line arriving inside a view
    moves nothing late (ruled 2026-10-17; the bottom edge jumped at once to the taller view).
  - *A wrong PIN*: "Wrong PIN." cross-fades into the field's reserved line, 140 ms; nothing below
    moves.
  - *The PIN accepted, the touch*: an overlay with the touch's gradient fades in over the whole
    stage, 220 ms on the entering curve — its opacity moves, since a gradient cannot transition —
    the panel's fill and stroke fade out with it, and the rings start their 2.4 s pulse.
  - *Touched, opening*: the rings stop and hold at half; the lead and the receipt cross-fade,
    140 ms. No colour changes: the green stays through the derivation and the release.
  - *Done*: the check disc pops where the rings' core was (from 60% with a little overshoot,
    240 ms on the pop curve), the word — *Unlocked* — in the middle and the vault's name under the
    open padlock, and it stays for the settle (320 ms), so the ceremony has a full stop. Then the
    lock screen lifts (8 px up, fading fast-first, 160 ms) and, a gap later, the shell arrives:
    the rail slides in 12 px from the left and the layer rises 8 px, 220 ms, the layer 40 ms
    behind. The store holds `settling` for the settle after the state says Unlocked, and the
    lock screen keeps rendering the ceremony meanwhile — the state's event can land a few
    milliseconds before the ceremony's Done, and without the hold the screen fell back to rest
    for that instant and the check was never seen.
  - *The touch timed out*: the rings stop, the overlay fades out over 160 ms on the exit's curve,
    the stage's ink returning with it over the same 160 ms — dark words never stand on the green
    — and the neutral message arrives on the panel, on the touch's step.
  - *In a dialog* the same beats play inside the box (Controls, below): the found LED's hold, the
    views crossing in one place, and the touch's wash filling the body with the box's stroke
    fading — in over 220 ms, out over 160 — while the foot band stays (ruled 2026-10-17).
- *Lock.* The reverse: the shell sinks 6 px and fades fast-first (120 ms) and, a gap later,
  the lock screen rises in (220 ms); the lock screen's header shows the open padlock and closes it over the first 160 ms.
  While unlocked the status chips show the open padlock. The brand — the mark and "Enfold" —
  rests at the same point in both scenes, the rail's top-left corner, so it travels only with
  its scene's own entrance (ruled 2026-10-14; the lock screen's sat at its centred strip's corner),
  until the window has a caption that carries the name and neither scene draws one.
- *Pages.* The outgoing page fades in place, fast first (80 ms); a gap later the incoming
  fades and travels 160 ms — from the right 10 px going into an archive, from the left 10 px
  coming back, up 6 px for a rail switch — in the old one's place, the layer being a grid so
  nothing jumps. The layer's foot is not part of the page and does not travel with it: one bar
  below the pages (`LayerFoot`), 34 px and the window's status bar (*The foot*, above; ruled
  2026-10-18), the page's note on the left at rest — set by the page
  (`store.footNote`), faded in when the page changes and updated in place otherwise — giving its
  place to the running operation and the done line in turn, and the
  lock state on the right, always: the open padlock, the nearer of the idle
  and the absolute deadline, since the absolute cap locks regardless of activity (ruled
  2026-09-12 after a review found the foot reading the idle deadline alone), said quietly
  (ruled 2026-10-14, refining 2026-09-12's countdown that ticked every second all day): with
  the idle deadline nearer, the rule — *Locks after 10 min idle* — since any input resets it;
  with the cap nearer (or equal: no input moves it), the clock time — *Session ends 16:42*;
  and only in the last 60 seconds a count, *Locks in 0:42* or *Session ends in 0:42*, in the
  attention colour (`lib/format.ts` `lockLine`; the minutes are the status's
  `IdleEffectiveMinutes`, ruled 2026-10-17; the page's clock still moves once a second,
  the text only when its form or the count changes), *Stay unlocked* in the idle deadline's last
  minute — and *Lock now*
  while unlocked, the standing notices' button before them; the closed padlock, *Locked* and *Unlock* while locked with archives still
  open (ruled 2026-10-17: it read *Keystore locked*).
- *Rail.* The rail is a NavigationView (ruled 2026-10-17): an item has no card of its own —
  the current one used to be a raised white card with a border, a shadow and bold text. The
  current item is the same subtle fill as a hover, at the regular weight, with a 3 × 16 px
  accent pill inside its left edge and its icon in the accent. Hover tints in over 120 ms and out over 160 ms — the ink at 6% over the ground, a
  tint that is seen in both themes (ruled 2026-10-14; `--surface-alt` was not); a press is
  instant (4%, the text to `--ink-2`); the current item's pill grows from its
  middle (160 ms, *out*; ruled 2026-10-14, it was 180 ms) and the previous one's shrinks. While an archive's page
  is open, every rail item's title says that a click leaves it — "Leaves Photos 2024" — the
  current *Archives* item too, since any route change away from the page leaves the archive
  (§2.3); an interim hint until the rail shows the open archive as a place of its own (ruled
  2026-10-14). While an operation runs, *Archives* wears the dot or the ring of *The foot*
  (above; ruled 2026-10-18), whose ring moves to its percentage over the fast duration.
- *Menus.* The `<select>` stays native and its picker is styled (`appearance: base-select`,
  Chromium 135+; WebView2 152 here): the list appears whole, falling 4 px and fading in over
  140 ms, rows tint on hover (120 ms), the chosen row wears the accent wash while the list
  fades out (100 ms). An older runtime ignores the declaration and shows the system popup,
  unanimated and unchanged.
- *The save bar* (`SaveBar`, the Settings page first; every page that stages edits uses it
  as it is). Edits are staged, never saved on change: the page shows saved ⊕ draft and *derives*
  what is pending by diffing the two — dirty is never stored, so an edit put back by hand
  un-dirties itself; only what the user may change counts (a timeout while locked does not).
  While nothing is pending there is no bar. While something is, a frosted bar (`backdrop-filter`, the standard property alone — the
  `-webkit-` twin was found to be the only one the minifier kept, and the engine does not
  honour it, so the frost never rendered in a build; ruled 2026-09-12) (the surface at
  88% over a blur — enough to read through, not enough to read the content behind it — with an
  inset hairline so its edge reads) rises 8 px and fades in (fast) at the
  foot of the scroll pane — in flow after the content and sticky 12 px above the pane's bottom,
  so it can never cover the last row for good, nor the rail — and sinks 8 px and fades out
  (leave) when the last edit is reverted, discarded or saved. It names the changes rather than
  counting them: an accent count pill, then as many chips as fit — "Lock when idle · 5 minutes"
  (the row's own name: *Lock when idle* and *Always lock after*, ruled 2026-10-17, were *Idle
  lock* and *Absolute lock*; a value reads "10 minutes (default)", and the *Look* row is gone
  until a second look exists; the recovery record's row is hidden, its setting still stored,
  until volume exports ship, and returns as *Repair data*; the section it stood in, left
  holding *Dictionary for small files* alone, is *Compression*, ruled 2026-10-17 — it was
  *Exports*);
  "+ label" and "− label" in the accent wash and the danger wash where a change adds or removes
  something — the rest folded into "+N more…", a click on which opens a popover of exactly the
  folded ones (fast, scaled from 97%; leave). Then *Discard* and the accent *Save changes*.
  Invalid input greys Save and says why in one red sentence at the bar's left, with the bar
  framed in the danger line; the field is marked in place too (`aria-invalid`), so the greyed
  button is never the only signal; Discard stays enabled. Saving disables both from the press,
  and once the save has taken 300 ms *Save changes* shows the pending ring and reads *Saving…*,
  as wide as it was (*The foot*'s pending ring, ruled 2026-10-18; it swapped the label at once
  and showed no ring). Success is the bar leaving — nothing else says so, a settings save being
  no operation with an ending to report; failure is
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
  side by side. The save bar wraps its chips onto their own line at the same width. The Archive page's file table is the same kind of rule: it drops *Modified* first and *Type*
  next as the pane narrows (at the widths the four columns need), Name and Size always, so the
  name column always keeps room.
- *The locked banner* is one line of 40 px — on the Archives and Archive pages in the attention
  tier with its first sentence strong (*Notices and their tiers*; ruled 2026-10-18, it was the
  plain bar), on the Vault page the plain bar: its text vertically centred, its buttons (*Close
  archive* or *Close all*, then *Unlock*) the small ones (28 px), the padding 6 px above and below (ruled 2026-09-11 after the text sat below the button's
  middle) — 40 px is the line's minimum, not its cap: a window too narrow for the sentence
  wraps it, and the line grows rather than cutting it.
- *The focus ring on a selectable row* — a row of the file list or of the Archives list — is
  an even 2 px ring drawn inside the row (an inset box shadow on its cells, never an `outline`
  on the `<tr>`, which Chromium draws per cell, unevenly, and lets the row's bottom border
  thicken), in a green a step darker than the selected row's own ground (`--focus-row`, one
  token per theme), never the black outer ring of buttons and inputs (ruled 2026-09-11); and
  the ring keeps its right edge at every width — the file list gives up its Modified and Type
  columns below two widths, and the edge moves to the last cell still shown (ruled 2026-09-12
  after a review found it open there).
- *Dialogs stay put.* A dialog that offers a choice — a *Cancel*, a *Skip*, a *Replace* —
  does not close on a click outside it: the click is nothing (ruled 2026-09-10, "they have a
  Cancel"); Esc is *Cancel* where there is one, *Close* where the one action is *Close*, and
  *Skip* in the extract's conflict question (its corner close too) and in the refused-name
  question (every waiting name's *Skip* with its tick on), the compare list's *Cancel* skipping
  them all — never *Replace* (§3); while a
  dialog is up the page behind it is inert (`inert` on the shell), so no list shortcut fires. Only a dialog whose one act is *Close* —
  the details modal, the results dialog (its *Copy details* beside *Close* taking nothing away),
  a toast's expansion — closes on the backdrop as
  well. A popover (a menu) closes on any click outside, as menus do. The Archive page's dialogs
  that ask for a name or a path — *Rename…*, *Create folder…*, the extract dialog, a refused
  name's *Rename…* — are forms (ruled 2026-10-14; the Archives page's *New archive…* is not one
  yet), and so are the lock screen's dialogs that take a name or a password — *Create a vault*
  (and *Rebuild the vault*), *Finish setting up* and the import dialog (ruled 2026-10-17; Enter in
  their password fields did nothing): each one's accent action is the submit button, so Enter in a
  field presses it, under the form rule — a greyed one leaves Enter doing nothing — and Esc still
  cancels; Enter on a card's radio or the tick presses it too, as WinUI's default button does, so
  Enter on the YubiKey's card starts the ceremony with the name it has (optional); it opens with the focus in its first field to type in, the caret at the end —
  *Rename…*'s with the stem selected, the create dialog's in the name — never a read-only one (the
  import dialog's path, which its *Choose…* fills) nor one inside a closed card; one with no field
  to type in yet opens on its submit button when that is enabled (*Finish setting up*'s
  *Continue*, until a card opens) and on its box when it is not (the import dialog before a file is
  read). Every other dialog takes the focus on its box, save the close question: a form with no
  field, it opens with the focus on its submit, *Quit*, which Enter presses (§2.3, ruled
  2026-10-17). A dialog whose height changes with a choice made in it is anchored at the top
  (ruled 2026-10-17: centred, each card that opened moved the title up, from 123 px to 20): the
  create, finish-setup and import dialogs stand with their top at a tenth of the window, never
  lower than where a 780 px box would stand centred (the create dialog's tallest state, 775 px,
  rounded up) and never nearer the edge than 20 px — 20 at an 820 px window and below, 108 at
  1080 — and grow downward only, their body scrolling past 20 px from the bottom as every dialog's
  does (the import dialog's backup with the YubiKey's card ticked is taller and scrolls at 820 px,
  above). Every other dialog is centred.
- *Type and spacing* (ruled 2026-10-17; the sizes inherited from the mockup — 12.5 and 11.5
  among fifteen, on a 1.45 line — are gone). Six type tokens and no other size, each on a
  whole-pixel line: caption 12/16, body 13/20, strong (600) 13/20 in Segoe UI Variable Text;
  section 16/22, sub 20/28 and hero 28/36, all 600, in Segoe UI Variable Display. Body is
  prose and the name in a list; caption is every other cell, a table header, a hint, the foot,
  a chip; strong is a field label and a setting's name; section heads a section; sub is a
  page's and a dialog's title; hero is the touch's lead and nothing else. Mono is 0.92 of its
  line's size, in the second ink. Text that is meant to be read is the second ink at the
  least (`--ink-2`, 6:1 or better on every ground and wash in both themes); the third (`--ink-3`) is
  for what is not read — a placeholder, a disabled label, the "—" of an empty cell, an icon.
  No capitals and no tracking: an eyebrow or a step label is a caption as written, in
  sentence case. A link that runs in a sentence takes that sentence's type: in a caption line
  it is a caption, never a step larger than its own words. Every inset and gap is on a 4 px grid (4, 8, 12, 16, 20, 24): a card's inset
  is 16 and a strip's (a bar, a toast) 12 — and 12 for the compact cards of a first
  way in, which must fit a dialog (ruled 2026-10-17); an icon stands 8 from its label, 12
  beside a 20 px icon; the layer's gutter is 24. The fixed heights stay as ruled — the 40 px
  locked line, the 34 px foot (the 42 px op row went with the operation strip, ruled
  2026-10-18), the 44/64 panel, the 36 px rows, the 62 px head, and the foot's flyout rows of 40. A fact is
  drawn one way, a label · value list: the label a caption in the second ink, its column as
  wide as its longest label up to 120 px, the value in body beside it and left-aligned,
  wrapping onto as many lines as it needs (ruled 2026-10-17: it was one line that ellipsized,
  and the preview's narrow column cut "71% smaller" off its value) — or, for a path or a hash,
  the label over the value, which wraps and is never cut; the one path drawn on a single line
  beside its label, the create dialog's *Kept in*, is cut in the middle with its drive and file
  name kept and the whole path in its title (ruled 2026-10-17). An option's label — a checkbox's, a
  radio's — is the ink, since it is what is chosen; a description under it is the second ink
  (ruled 2026-10-17; the labels were the second ink and read as hints). A row taller than its
  name's line — an archive with a description, a refused name with its reason — stands every
  cell on the name's first line, where a one-line row's text sits, and grows under it: 52 px
  with a description (ruled 2026-10-17; the other cells were centred on the whole row). The Vault page's *This vault* is drawn so
  (its name and last change beside their labels, its file under its own, all one row of the
  list above the switch's), as are the backup card's facts (its *Last backup* became the card's status line, ruled
  2026-10-18), the vault file
  and the data folder on the Settings page, and the import dialog's facts (ruled 2026-10-17:
  they were a setting row's bold name over a faint line, or a value pushed to the far edge).
  The touch's lead is the hero in a ceremony dialog too, as on the lock screen (ruled
  2026-10-17; it was a 21 px size of its own there).
- *Controls.* Every dialog and popover enters over 140 ms (the box also scales from 97%) and
  leaves over 100 ms; a panel that swaps its content in place — the lock screen's one panel and
  a dialog's ceremony panel, between steps (ruled 2026-10-17; the lock screen's was three cards
  that eased between live and dim) — crosses its views in one place, the old leaving fast first
  over 140 ms and the new arriving over 140 ms a gap later (Motion, *Unlock*). **A ceremony in a
  dialog is the lock screen's stage at a dialog's size** (ruled 2026-10-17: the one panel of
  2026-09-06, for the dialogs too) — the Vault page's acts (*Add an unlock method…*, *Remove…*,
  *Re-key vault…*, *Show recovery key…*, *Export backup…*, the password with every YubiKey) and
  the proof of *Import archives from a backup…* (§13). It has no title line of its own: the
  dialog's title names the act (ruled 2026-10-14, after the title was printed twice). Its views
  are the lock screen's, chosen by the same rule and turned the same way: the key art while a
  YubiKey is waited for — "Insert a YubiKey that already unlocks this vault" over the vault's
  YubiKeys by name, with "The new unlock method comes next." for an enrolment alone (the other
  acts prove it is you and say nothing of a next step) — two YubiKeys and one that is not the
  vault's as on the lock screen, the YubiKey's name as a chip, the PIN's fields with their
  reserved lines and the retries' inks, the last try's bar, a blocked PIN's lead and danger bar,
  a failure where it happened ("The YubiKey stopped waiting" in neutral ink after a timed-out
  touch), and the end as what is now true ("Unlock method added", "Vault re-keyed") over the
  accent check. A typed proof — a vault with no YubiKey proves it is you with its password —
  heads as the YubiKey's wait does, with what to type and why: "Type a password that already
  unlocks this vault" over "It proves it is you." ("The new unlock method comes next." for an
  enrolment, whose line already says the proving) (ruled 2026-10-17; the field stood alone under
  the dialog's title and said neither). The headings, a failure's among them, are the section
  size (16/22), since the dialog's title already stands at 20/28 and a 20 px lead under it would
  rank the step with the act; the touch's lead is the hero, as on the lock screen. An enrolment draws a
  two-part line under the title, *Prove it is you · Add the new unlock method* — two halves, not
  three steps — its first half passing at the core's `SwapKey` or at the new secret's prompt;
  its swap is the key art over "Remove <the YubiKey that proved it>" and "Then insert the new
  YubiKey. Enfold carries on by itself.", and its second half's wait reads "Now insert the new
  YubiKey" (the core's own words for the swap, "the key to enroll", are never shown). **The
  touch takes the box over** as it takes the lock's stage: the touch's gradient fills the body
  under an opacity that moves (220 ms on the entering curve; 160 ms on the exit's), the box's
  stroke fades with it, the title, the line and the words take the touch's inks, the rings, the
  hero, its line and the receipt stand in the middle — and the foot band stays as it is, with its
  *Cancel*. The green holds through whatever follows a touch with nothing asked since, as on
  the lock screen: the proof's derivation ("Checking that it is you…", since the vault is open
  already) and a proof's Done, and the new YubiKey's touch in an enrolment's second half — the
  core has the new YubiKey prove itself by its PIN and its touch before the vault depends on it —
  through the Done after it (ruled 2026-10-17; the green used to leave as that Done's check
  popped, on the false ground that nothing was touched there). What has no touch behind it is
  drawn on the neutral panel: a typed proof's derivation and Done, the key made on the new
  YubiKey before its touch ("Adding the unlock method…"), and a Done after a prompt that came
  between the proof's touch and the end — a new password chosen — where the green would arrive
  only to leave. **The buttons are the dialog's foot
  band's** (ruled 2026-10-17; the panel's own *Cancel* in the body was the last of the web
  modal's shapes): *Continue* beside *Cancel* while a prompt is up — the submit of the step's
  form by its id, so Enter in a field still presses it — *Cancel* alone while nothing is asked;
  after a failure the user did not choose — the YubiKey stopped waiting, a refusal — *Start
  again* (accent) beside *Cancel*, as the lock screen's failure has them (ruled 2026-10-17; the
  dialog offered only *Close*, and the act had to be found on the page and begun anew): the page
  begins the same act with what it was begun with — the same new unlock method and name, the
  same unlock method to remove, the same backup file — in the same dialog, which stays open, the
  failure standing until the new ceremony's first event takes its place; *Close* once the
  ceremony has otherwise ended. Escape is *Cancel* while it runs and closes the dialog after. The panel
  keeps one height for every step, the tallest's — the PIN's last try under an enrolment's line,
  354 px measured in Edge, 356 on the 4 px grid — so the box, 519 px, never jumps as the steps
  swap (ruled 2026-10-17; it was the mock's 380, which left 26 px of nothing under the tallest).
  A button that grows to share a row centres its label, as a wide one does
  (*Continue*, *Unlock*, *Start again*). *Toasts* are for what the page cannot say in place — an error the
  action's own surface is gone for — never a confirmation of something the page already shows
  (a saved setting is the bar leaving). Since 2026-10-18, and until phase 4's InfoBars take
  their place, they are narrowed to errors, to the moment a status warning arrives
  (`vault.warning`), and to the few facts no surface holds — a copy made ("Details copied"), a
  folder the page stood in that went, the `.efd` files a drop did not open, the keys a purge
  dropped: how an operation ended is the foot's done line and never a toast (*The foot*; a
  cancel's neutral "Stopped." toast, a reclaim's figure and another archive's summary were
  toasts). Where they go, ruled 2026-09-07 for whatever toasts come
  next: at the top of the window, centred on the window — not on the content layer — dropping
  in a little as they fade (a few pixels down, never sliding in from the top edge) and leaving
  upward as they fade out; `Toasts` does this, 16 px from the top (ruled 2026-10-17, on the 4 px grid; it was 14), centred on the window's
  width, the text centred and balanced. The controls are Windows' own, not a web page's
  (ruled 2026-10-17, from WinUI's values; the mockup's stills of WinUI states had become the
  states themselves). One rule for what looks like a link: a command is a button — *Cancel*,
  *Lock now*, "Use recovery key instead" are subtle buttons, 24 px high in the layer's foot —
  the lock screen's further commands too (*Backups and copies…*, *Rebuild the vault…*, "Use
  recovery key instead": 28 px; the panel's *Cancel* is a standard button beside its primary) and the replaced copy's *Restore
  it…*, a small button on its bar's 40 px line (ruled 2026-10-17; each was a link) —
  and a hyperlink button (*Show in Explorer*, *Locate…*, *Technical details…*) is kept for going somewhere
  or revealing something, and for the create dialog's *Keep it elsewhere…* and *Use the usual
  place*, which go to another place for the vault and stand in its one caption line, in the
  line's caption (ruled 2026-10-17; it was a 24 px subtle button, which with the path wrapped to
  a row of its own): the accent ink with no underline at rest (the underline at rest,
  ruled 2026-09-07, is gone), a subtle fill on hover as it darkens (`--accent-ink-hover`, a
  step past `--accent-ink`; brighter in the dark theme, where contrast goes the other way).
  Only a link inside running text keeps its underline, which thickens on hover. A button's
  press is instant and dims its text. A disabled button loses its hue, not its opacity: its
  text at 36% of the ink, a filled one's ground a neutral grey with its ink over it, never the
  pale accent slab or the pink a faded danger was; becoming enabled fades its ink in over
  120 ms. It answers no hover and no press, but it still takes the pointer, so its title — the
  reason it is greyed — shows on hover (ruled 2026-10-14; it used to let the pointer through,
  and every reason went unseen). A text field has a neutral 1 px underline at rest, a fill
  under the pointer, and the 2 px accent underline over the surface only while it has the
  focus — that underline is its focus cue, and it was on every field at rest, so every field
  looked focused; a field marked wrong has the same 2 px in the danger colour, focused or not.
  Checkboxes and radios are 20 px (16 in a list's rows), the accent filled when on — a radio
  an accent ring round a dot; a slider is a 4 px track, the accent up to the value, under a
  20 px thumb with a 12 px accent core; progress is a 3 px accent line over a 1 px track. A
  card is flat, a 1 px stroke and no shadow: a shadow says a thing floats — a menu, a toast,
  the save bar, the bottom panel over the list, a dialog. The Settings page and the Vault page's
  *This vault* are Windows Settings' list: each setting a card of its own, 4 px from the next
  (ruled 2026-10-17; they were rows ruled off inside one card); the Vault page's ways to
  unlock are separate cards the same 4 px apart (ruled 2026-10-17; they stood 12 apart). A
  text box has no resize grip — the description's box is two lines, scrolling past them — a
  search box has no clear button of Chromium's own, and selected text wears the accent's line
  under the ink, never the engine's blue (ruled 2026-10-17). The window's ground is solid; the
  green radial glow where Mica would be is gone until the window has the material itself. A
  dialog is two parts: its body — the title 20/28, 24 px all round — which scrolls when the
  window is short, and a foot band a step below the surface, behind a stroke, 20 px above and
  below its buttons and 24 at the sides, which holds them in equal widths and wraps a button
  to a row of its own rather than let it spill, in WinUI's content-dialog order — the act
  first, then any other way on, and the way out (*Cancel*, *Close*, *Go back*) last (ruled
  2026-10-17; the act used to stand last, at the right); Esc and Enter do what they did. A
  dialog whose foot holds no way out has a close button in its top-right corner instead, on
  the title's line at the body's 24 px inset: a 32 px subtle button with the × glyph, titled
  *Cancel (Esc)*, that does exactly what Esc does (ruled 2026-10-17). Only the close question
  has one (§2.3), its foot being its two answers — and, since 2026-10-18, the extract's
  conflict question, which has no foot band at all, its answers being command rows in its body
  and its close being *Skip* (§3). Each page's head is one
  height, the line of a 32 px button, whether or not it carries one, so the body never starts
  at a different place from one page to the next; the Archive page's heading button is exactly
  its title's line. A column of magnitudes — a size — is right-aligned, its header with it, so
  the digits line up, and its sort chevron stands before the word rather than after it, so the
  word stays flush whichever column sorts (ruled 2026-10-17); a column of dates or IDs is not, though its figures are of one width. A slider's label sits close beside it, fixed in width, and says the number alone,
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
generated (`wails3 generate bindings`) and committed. Strings in one table. A number that is a
quantity is written in figures — "30 days", "5 minutes", "At least 2 unlock methods" —
not spelled out (ruled 2026-10-14). **One word per thing, consequences first** (ruled
2026-10-17, direction 8 of the design review): the head of `strings.ts` holds a glossary — on
screen the file is the *vault* (never "keystore"; the rail item and the page are *Vault*), a
YubiKey, the password or a recovery key is an *unlock method* (never "slot", "way in" or "way
to unlock"), a *backup* is an exported copy of the vault, the vault's password with every
YubiKey is *Password with every YubiKey*, an archive's *fingerprint* is the SHA-256 of its file
the registry keeps and is never "verified" (§2.3; ruled 2026-10-18) — and a verb table
(`commandCopy`: *Forget archive…* / *Restore*, *Re-key archive…* / *Re-key vault…*, *Refresh
fingerprint*, *Close archive* / *Close all* (*Close it* / *Close them* on the lock screen's
notice; ruled 2026-10-18), *Find missing files*, and four backup verbs only:
*Inspect a backup…*, *Test that a backup opens…*, *Import archives from a backup…*, *Replace this
vault with a copy…* — *Replace with a copy…* on a banner's button — with *Restore from a vault or
backup…* at first run); a command ends with "…" exactly when a dialog or a picker follows, and
the dialog's primary repeats its verb and object. A consequence dialog's title is a question
naming the object and its body at most two sentences, the consequence for the user's files and
then the safety net (`dialogCopy`), the mechanism behind a "How this works" disclosure. Two
registers: the format's words — keystore, slot, superblock, registry, KID, key version, seq,
generation, VMK, session keys, envelope, zstd, entangled — stand only in `technicalCopy`, which
the *Technical details* dialog, a warning's "Why?" and a "How this works" read; a vitest over
every value of `strings.ts` and `validate.ts` fails on one anywhere else, with the YubiKey's own
PIV "slot" in two token codes as its listed exceptions. This spec keeps its own terms in prose;
a label quoted from a screen is the screen's word. Chips are in sentence case; a date is the
app's ISO form in lists, facts and dialogs, and relative ("yesterday, 03:24", "22 days ago",
`sinceText`) only where recency is the point — the last unlock, the last backup. A code with no copy of its own is never shown raw: it reads
"Something went wrong (archive.foo). The log in the data folder has the details.", with the
code set in muted mono where a toast shows it (ruled 2026-10-14; the fallback had been
"Error: archive.foo").

## 8. Memory hygiene

The three retained secrets — the VMK and K_P in `keystore.Unlocked`, the KWK in
`keystore.Session` — live in pages the process allocates outside the Go heap (`internal/secmem`:
`VirtualAlloc` + `VirtualLock`, so they are never written to the pagefile; erased by a loop the
compiler cannot elide and released on `Close` and `Lock`; a page the kernel refuses to lock is
used unlocked and logged once, never fatal; three pages fit the default minimum working set of
50, so the working set is not raised). Not memguard (SCOPE, ruled 2026-09-13): a secret handed
to crypto/aes, HKDF, Argon2 or ML-KEM is copied by the library beyond any enclave's reach, and
the comments say where. Every other secret buffer the application owns is zeroed best-effort
(`kdf.Zero`) when it is done with. Crash dumps: `SetErrorMode(SEM_NOGPFAULTERRORBOX |
SEM_FAILCRITICALERRORS)` means the system does not invoke Windows Error Reporting for the
process, and `WerAddExcludedApplication` (per user, no elevation) covers what the error mode
does not — a report raised for something other than the process's own fault; WER's LocalDumps,
an administrator's opt-in that writes memory to disk independently of the rest of WER, and the
hibernation file remain outside a user-mode process's control and are the documented limit.
Destroying the window on close destroys the renderer's copy of what was on screen.

## 9. BitLocker

**No detection: the checks were struck on 2026-09-13** (DECISIONS "Before the phone"). This
section once planned two tri-state checks — the system volume and the vault's volume, warning
on *Unprotected* — but no unelevated mechanism was ever measured: the WMI
`Win32_EncryptableVolume` class is documented admin-only, the shell property
`System.Volume.BitLockerProtection` stayed an unmeasured candidate, and the least-privilege
ruling forbids elevation. Nothing in the core or the shell reads a volume's protection status,
and the only warning the shell raises is lock-trigger detection's (§5). What remains is advice:
the import dialog's in-place choice says the file must stay on a BitLocker-protected fixed drive
and that removable media and network shares are outside Enfold's protection
(`ImportDialog.svelte`).

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

PDF preview (ruled 2026-09-13: no renderer in the window, pdf.js included — a temporary copy
opened by a browser, designed with the future "open a temporary copy, edit, put it back";
DECISIONS); an entropy estimate for a chosen password (ruled out 2026-09-13: the minimum of 8
is the rule; SCOPE);
(folder move is `Move` since the tree, §3);
the permitted range of the timeouts beyond the clamps. (The BitLocker check was struck
2026-09-13, §9; overwrite on extraction shipped — `ask` by default since 2026-10-14 (it was
`replace`), with `replace`, `skip` and `rename`, §3.)

## 13. Ruled 2026-09-07, critiqued the same day, to implement (with FORMAT.md Revision 2)

**The Archives page is the vault's inspector.** It lists registry records, not files, and it
grows into the full view: columns *Name* (the description as a muted second line), *Size*
(`last_stored_size`, the registry's last observation of the file; right-aligned), *Last saved*,
*Status* (chips in sentence case, ruled 2026-10-17: *Open* · *Compacting* · *Missing* · *Hidden*
· *Forgotten*) — no *Files* and no *Key vN*
column (ruled 2026-10-14, reversing the 2026-09-07 column set: the core counts files only for an
open archive, and an archive is open on this page only while it drains, so the column read "—"
on every row; the key version matters only around a rotation and moves to *Technical
details…*); a header
line for the vault — archives and their stored total summed by the page over the rows, the vault
file's size (`VaultStatus.VaultFileSize`), the registry's `ModifiedAt` (§2.1) — one caption
line beside the title, on its baseline, the count no pill of its own (ruled 2026-10-17: a pill
in the head is a state, *Locked*); sorting by column
and a filter box. The details pane keeps what it shows and adds an editable *Description*,
*Rename* (the registry's trusted name, FORMAT §7.4), the full `last_path` — under its label,
wrapping rather than cut (ruled 2026-10-17; its facts are §6's label · value list) — with *Show in
Explorer* or *Locate…* — worded "last seen on another system at …" when the path's syntax is not
this platform's — *Created*, and *Technical details…*, a hyperlink button under the facts
it adds to (ruled 2026-10-17; it was a standard button beside *Close*) (ruled 2026-10-17: the pane's *KID* and
*Key version* moved into it, where the KID was already shown once, and the pane's *Free space*
reads *Unused space*), a modal titled *Technical details* with the versions table
(KID · created · retired · state), `archive_id`, the key version (vN, beside the current KID),
revision, last writer, `last_seq` and
`hash_at_seq` as two rows, *Last seq* and *Fingerprint at seq* (*N/A* while no fingerprint was
taken), the hash as *Fingerprint (SHA-256 of the file)* (ruled 2026-10-18; they were one row,
*Last seq / hash at seq*, and *Ciphertext hash*), the policy bits — every value selectable
(`user-select: text`) with a right-click *Copy* on it and no *Copy* buttons, the hash wrapping
onto a second line rather than being cut and reading *N/A* while it is all zero (no fingerprint
taken yet, §2.3), one caption under the rows ("A fingerprint tells this file from another copy
of it, such as a restored backup. It is not a check of the file's contents."), the modal
scrolling as a whole and the versions table showing at least three rows before it scrolls on
its own. The pane's data is
`Archives.Details(id)` (below), read when the selection changes — and again when the row's
`HashBehind` or `ReceiptOwed` moves or the vault unlocks (§2.3) — not when the modal opens.
Tooltips only where text is cut short. There is no raw archive-key reveal: nothing opens an
archive from a bare key, and the way to hand one archive to someone is a later *export one
record as a small keystore*, the precursor of sharing.

*file missing* is the `Note` code `archive.file_missing` and is never computed inside `List`:
the core keeps a presence per record, refreshed by a pass after unlock and by the page's *Find
missing files* action (`Archives.CheckFiles()`; *Check files* until 2026-10-17), and its chip
reads *Missing*, one path at a time with the state mutex released and a
2 s budget each, the probe abandoned on expiry so a path that does not answer is never reported
missing. Only paths on a local fixed volume of this machine are probed; a path whose syntax is
not this platform's, a removable or network drive and every UNC path are left unmeasured and
are checked only for the one record the user acts on — `last_path` reaches the registry from
*Import archives from a backup…* as easily as from *Locate…*, and opening `\\host\share` because a record says
so is an outbound authentication to a host someone else named. Until a pass has run the column
is blank, never "missing". **`CheckFiles()` answers when its pass has ended** (ruled
2026-10-18; it answered at once, with nothing but an error), with `CheckReport{Probed, Missing}`:
`Probed` counts the records whose path the pass measured — a local fixed path that answered
within its budget — and `Missing` those of them found absent; a path left unmeasured (UNC,
removable or network, another platform's syntax, over its budget) is in neither figure. A call
that lands while a pass runs for this vault joins it and answers its report; one that finds a
pass still running for a vault since locked away waits that one out and starts a fresh pass. The
probe rules above are unchanged, so the wait is bounded by the pass. *Find missing files* waits
with the pending ring and *Checking…* (§6, *The foot*), a second press meanwhile ignored, and ends
in the foot's done line: "Checked 3 archives · all files present" with the accent check, or
"Checked 3 archives · 1 file missing" in neutral ink, with no action — the *Missing* chips in
the list are the way on. The count is the archives checked, not the archives listed. A pass that
checked none — every archive on a removable, network or UNC path, an empty vault, a pass behind
a probe still held over its budget — establishes nothing, and ends in "No archive was checked" in
neutral ink, with no check and no claim that files are present. **The
*Missing* chip is a button** (ruled 2026-10-18, direction 7: the chip names the trouble and the
button is its cure), drawn as the chip, that selects its row and runs *Locate…* for it; its
title is the note's words and then "Locate… asks where it is now."; it is greyed by *Locate…*'s
own rule — the vault locked, the record forgotten — with that reason as its title. It is outside
the Tab order, the keyboard reaching *Locate…* through the pane's hyperlink button; once clicked
it keeps its own Enter and Space (§6, *The lists' keys*).

**Forget and delete.** *Forget archive…* (ruled 2026-10-17; it was *Forget key…*, the archive
key being a technical word) drops the record softly. Its dialog follows the template of
`strings.ts` (`dialogCopy`): "Forget Photos 2024? The file stays where it is. You can restore it
from Show hidden until <date>; after that its key is gone and the file cannot be opened."
[Forget archive] [Cancel]; Delete's reads "Delete Photos 2024? Deletes <path> for good, after
checking that the file really is this archive. Its key is kept for 30 days in case a copy of the
file turns up." [Delete archive] [Cancel]. The archive is closed first (a running operation is
cancelled) — on the Archives page once the name is typed and the act confirmed, never before,
so *Cancel* leaves an archive still draining a preview as it was, and a close the core refuses
ends the dialog with its reason and nothing removed (ruled 2026-10-14); then `forgotten_at` is set (FORMAT §7.1, §18.2) and
the record shows under *Show hidden* with the chip *Forgotten* — its key dropped at the first
unlock after <date> — and *Restore*, which clears `forgotten_at`.
While forgotten the record still holds the keys, so `Open`, `Rename`, `SetDescription`,
`RotateKey`, `Verify`, `Compact`, `Locate`, `Hide` and `Unhide` are refused with
`archive.forgotten` (the Archives page greys the ones it offers — Open, Compact…, Re-key archive…,
Refresh fingerprint, Hide or Unhide, the pane's Locate… and its name and description fields — with those
words, ruled 2026-10-14) — never `archive.not_found`, which stays the answer for a record already
purged, so the page can tell "restore it" from "its key is gone" — and no registry write ever
updates a forgotten record. *Restore* and *Delete archive…* are the two actions that still work
on it, and a second *Forget* does not move `forgotten_at`: the retention clock never restarts.

*Delete archive…* is Forget plus the file (DESIGN trap 28). The file at `last_path` is opened
once and removed through that same handle, never by the name a second time, and never with a
handle this process is holding — the open archive is closed first, after the consent as for
*Forget archive…*, refused with `archive.busy`
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
forgotten**, with *Retry*, *Locate…* and *Forget archive only…* offered (ruled 2026-10-17; it
was *Forget the key only*), because a record dropped
against a file that could not be read destroys the keys of an archive that is still intact. The Archive page's own *Delete archive…* is the same action on the archive it shows, in the same
dialog, asked first and over the Archive page: *Cancel* leaves the user where they were, in their
folder, and the archive is left only on consent — the leave the rail performs (§2.3), then
closed as on the Archives page if a preview body or an operation still holds it — with the page
going back to the list when the dialog closes (ruled 2026-10-14, refining 2026-09-07's close
first). It is disabled while the vault is locked with the archive open (§2.3).

Both confirm by the archive's name typed (compared trimmed and exactly, case and all) — the name
stands once, as the field label's hint, with no placeholder repeating it; the field has the focus
when the dialog opens and Enter there presses the primary, which stays enabled with the field
empty and marks it instead (§6, Forms; ruled 2026-10-17) — and the
warning says only what is known here: with a `lastExportAt` for this vault at or after the
record's `created_at`, "The last backup of this vault, <date>, can open this archive if you still
have it." (one sentence, ruled 2026-10-17); otherwise "No backup of this vault is recorded here, so once its key
is gone no copy of this archive can be opened." (ruled 2026-10-17, in the user's words). No ceremony: the registry write
needs the session's key, not the VMK, and the brakes are the typed name, the retention and the
backup.

**The purge has one trigger.** Forgotten records are dropped at the end of a successful unlock
of the vault kept here, before any archive is opened, in one registry write of its own whose
`modified_at` decides (FORMAT §18.2) — beside, not inside, the write that pays the owed receipts,
since the two fail differently; a purge that would drop nothing writes nothing, so an unlock does
not restamp `modified_at` — and `archives.changed` follows with a line naming what went, the
consequence first: ""Photos 2024" was forgotten more than 30 days ago, so it can no longer be
opened.", or "3 forgotten archives can no longer be opened: …" (ruled 2026-10-17; it said the
key "was dropped", the format's act on a main surface). No other
registry write purges, so nothing is dropped while the vault is locked and no
Save on one archive destroys another's keys; the unlock of a staged copy — `VerifyBackup`,
`InspectFile`, `InspectRecords` — purges nothing. Every record while the vault is Tampered is
skipped and left to the next unlock.

**The entangled password is the vault's** (FORMAT §3.1, §18.1). `VaultStatus.Entangled` is the
slot region header's switch, known while Locked; the lock screen asks for the password once,
beside the PIN and after it (§2.2's one order; ruled 2026-10-17, this said "before the PIN"),
whenever it is on — the standalone-password and recovery ways in never ask,
whatever the switch says — and the typed password lives with the attempt (§2.2), so a cancel, a
retry and an adopted pending touch neither lose it nor ask again. The Vault page shows one row,
*Password with every YubiKey*, "On: …" or "Off: …", with *Change…*, *Turn off…* or *Turn on…*
(ruled 2026-10-17: "entangled" is the format's word and stays in this prose; the row read
*Entangled password*), backed by `Keys.EntangledState() {On, CanEnable,
Reason}`: turning it on asks for the new password twice, changing it asks for the new one twice,
turning it off confirms — "Stop requiring a password with every YubiKey?", "After this, a YubiKey opens the
vault with its PIN and touch alone. One of the vault's unlock methods is asked for first." and *Turn off
the password*, a standard button beside *Cancel*, since nothing should suggest weakening the
vault (ruled 2026-10-18; its primary was the accent *Turn off password*, over a body that said
nothing of the proof asked next), while turning it on and changing it keep the accent; each is a ceremony for the VMK by any way in and then re-wraps every
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
before a password is typed, worded "Every YubiKey would then need this password. Add a recovery
key first." with the action that runs `BeginEnroll(recovery, label)`; `CanEnable` greys the
switch ahead of any ceremony, as *Remove* is greyed from `SlotView.Removable`. The same
predicate from the other side: with the password on, `Removable` is false for the last recovery
slot when every other active slot is a hardware key. Turning it off is never refused. Enrolling a
key asks for no password — an enrolled key inherits the vault's setting and is wrapped from the
kept `K_P` offline — except the first way in of an adopted backup, which sets the vault's
entanglement afresh, a backup carrying no `K_P` (FORMAT R28). The rotate dialog loses its
"except a key whose entangled password…" clause: every way in is re-wrapped, always.

**Merge records.** Beside *Replace this vault with a copy…* (which replaces the vault kept here)
an *Import archives from a backup…* action (ruled 2026-10-17; it was *Import records…*: the
screen says archives, the registry's records are this prose's) opens a backup or a vault over a staged copy and proves it before anything is listed. Its dialog bears the action's name, *Import archives from a backup*; the proof step's buttons are *Continue* and *Cancel*, and the list's *Import 2 archives* (the count the ticks', *Import archives* greyed with none) and *Cancel*, which discards what was read (ruled 2026-10-17; they were *Read the records*, *Merge 2 records* and *Discard*).
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
or rotation runs. A file that opens but fails its own slot-region hash (FORMAT R25) is listed
all the same: every field of a row comes from its authenticated registry, and what R25 guards —
the slot records' public keys, which a rotation wraps the next VMK to — is nothing a merge uses
(a recovery slot the file is opened through by its own key is checked by its own AAD).
`IncomingRecords` says so beside the rows (`IncomingFile.Tampered`, ruled 2026-10-13): the
dialog warns above the list, in the danger tier (§6, ruled 2026-10-18), that the records can be merged but the file itself is not to be kept
or trusted as a backup — Import and Verify backup refuse it.

The records are listed with checkboxes, ticked by default: a record whose `archive_id`, KID and
key the vault already has is skipped and listed greyed as *Already here* (a chip in sentence
case, ruled 2026-10-17); a known archive with
a new KID gains that version (the file's envelope decides which is current); a new `archive_id`
is added; a record this vault holds as forgotten is listed unticked with the chip *Forgotten*,
"Forgotten here on <date>. Ticking it restores it." (ruled 2026-10-17: every row's chip is
`chipCopy`'s — *New*, *Update*, *Forgotten*, *Already here* — and its line names archives, not
records or key versions), and ticking it is an explicit *Restore*. Fields are never taken by counter alone: version
lists union; a field the local record leaves empty is filled; a field the two hold differently —
`name`, `description`, `policy` — keeps the local value and the row says what differed ("named
'Photos 2023' there"), except `always_require_full_auth`, which is set if either side has it and
cleared by neither; the fields that describe a *copy* — `last_path`, `last_seq`, `hash_at_seq`,
`last_ciphertext_hash`, `last_stored_size`, `last_written_at` — are the other vault's observation
of its own file and are never taken over the local ones. Every record the merge changes is
written with `revision = max(local, incoming) + 1` and this vault's `device_id` as
`last_writer`, so a merge in the other direction sees a descendant rather than a rival
(SYNC.md §5).

**The rotate dialog** — *Re-key the vault?*, opened by *Re-key vault…* (ruled 2026-10-17; it
was *Rotate now…*, which collided with the archive's *Rotate key…*), its body "Every unlock
method gets the new key now; no YubiKey is needed for this. Backups made before today keep
opening with the methods they had." and its buttons *Re-key vault*, or *Back up, then re-key*
with the backup ticked, and *Cancel* — shows the last backup's time and, past a week or with none, asks for a
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
unauthenticated convenience: it chooses wording, pre-selects the rotate dialog's backup
checkbox and dates the backups card's status (§6, through `Vault.BackupStatus`, §3), and it
never gates a cryptographic operation.

**The acts run their ceremony in a dialog of their own** (ruled 2026-10-17). Once *Add an unlock
method*, *Remove <name>?*, the rotate dialog or the password's own dialog is answered, once
*Export backup…*'s Save dialog has named a place, and at once for *Show recovery key…*, the
act's ceremony stands in a dialog that
bears its name — *Add an unlock method*, *Remove an unlock method*, *Re-key the vault*, *Show the
recovery key*, *Export a backup*, *Password with every YubiKey* — drawn as §6, *Controls*, says:
the lock screen's views at a dialog's size, one height for every step, the touch's green over the
body, and its buttons in the foot band. The proof is the core's way in for a slot change — the
YubiKey where the vault has one, else its password (`acquireUnlocked`) — so the dialog opens on
the YubiKey's wait at once rather than on a quiet instant. The enrolment's is the one two-part
line (*Prove it is you · Add the new unlock method*); a recovery key enrolled, and the reveal,
end in the recovery key's dialog (§6), which takes over from the act's. The proof of *Import
archives from a backup…* runs the same panel inside that dialog, between its proof step and its
list, the foot band's *Continue* and *Cancel* then the panel's. A proof typed on a vault with no
YubiKey heads with what to type and why, as the YubiKey's wait does; a failure the user did not
choose offers *Start again* beside *Cancel* in the act's own dialog, which begins the act again
with what it was begun with — the rotation after a failed backup begins with the backup again,
to the same file, since no backup means no rotation (ruled 2026-10-17).

**What changes in §3.** `VaultStatus` gains `Entangled`, `VaultFileSize` and `TamperedReason`
(`vault.tampered_hash` · `vault.tampered_generation`, so the warning can tell a damaged file from a
spliced or rolled-back region); §2.1's cached facts
are `VaultID`, `ModifiedAt`, `Entangled`, `Slots()`. `SlotView` loses `Entangled`, `Stale` and
`Escrowed` — every recovery slot has its escrow record from the commit that creates it (FORMAT
R38), so `vault.no_escrow`, `EscrowOpenedKey` and the Vault page's "Made before Enfold kept
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
reveal ends with its URL), `Vault.IncomingRecords(handle) IncomingFile{Records
[]IncomingRecord{ArchiveID, Name, Description, CreatedAt, Versions, Action skip · version · add ·
forgotten, ForgottenAt, Differs []{Field, Theirs}, Ticked}, Tampered}` (Tampered: that file fails
its own R25 hash), `Vault.MergeRecords(handle, ids)` (a registry write on the session)
and `Vault.DiscardRecords(handle)`, `Keys.EntangledState()`, `Keys.SetEntangled(on)` and
`Keys.ChangeEntangledPassword()` (ceremonies of kind `entangle`; the standalone password and the
first way in are the only `Choose` prompts, each typed twice on the page and sent once under the
one prompt — the first way in's in two fields of the create, restore or finish-setup dialog,
which answer the ceremony's first `Choose` prompt from there, the rest in a second field under
the one prompt; ruled 2026-10-17), `Vault.LastExportAt() int64` (seconds, zero for *never*), and since 2026-10-18
`Vault.BackupStatus()` (§3). Codes this adds:
`archive.forgotten`, `archive.delete_failed`, `archive.file_unreachable`,
`archive.not_this_archive` (the proven mismatch: nothing removed, the page asks once more before
forgetting), `archive.description_long`, `archive.name_invalid` (a rename that is empty or over
1 024 bytes), `vault.not_this_vault` (no VMK and no key opened the incoming registry — never
worded as damage), `vault.tampered_hash`, `vault.tampered_generation`, `vault.escrow_missing` (the
registry keeps no copy of this recovery key — replace the slot; never a refusal of the unlock).
`vault.no_escrow` and `EscrowOpenedKey` go; `vault.stale` stays for the A/B stale-copy warning and
`Session.Live`. *Import archives from a backup…* lives in the Archives page's command bar — the vault must be
Unlocked for it, so the lock screen cannot offer it — with a pointer from the Vault page's Backups
card, which hands the ask over as the key dialog's *Import archives from a backup…* does: while the command could
run (Unlocked, not tampered) its link is *Import archives from a backup…* and the Archives page opens the picker
on arrival; otherwise it is *Go there* and only goes there (ruled 2026-10-14; it used to go there
alone). The rotate dialog's pre-selected backup performs the export and then the rotation in one
press; a Save dialog cancelled at the export rotates nothing.

## 14. Install, upgrade, uninstall

*Ruled 2026-09-21 (the user's "do it this way" on every point); the decisions are marked where
they are taken.*

**One installer, per user, no elevation.** The NSIS installer (`build/windows/nsis`, the only
installer — DECISIONS 2026-09-06) installs for the current user alone: `%LOCALAPPDATA%\Programs\Enfold`,
`REQUEST_EXECUTION_LEVEL user`, `WAILS_INSTALL_SCOPE user`, no UAC prompt (decision 1). An unsigned
binary behind a UAC prompt is the worst first impression an unsigned binary can make; the vault
is per user already (§2.1); and DESIGN §2 accepts the same-user attacker, so the write
protection of Program Files buys nothing the design counts on. A machine-wide install is not
offered in 1.0. The installer's pages are Welcome, the files, and Finish with *Run Enfold* — no
directory page, since there is one place and the one path the product lets a user choose is
the vault's, inside the application, and no licence page (decision 2). It writes `enfold.exe`,
`uninstall.exe` and a Start-menu shortcut; no desktop shortcut — Enfold lives in the tray, and a
shortcut is a right-click away for whoever wants one (decision 2). The uninstall entry (HKCU)
carries the version from `build/config.yml`, which is the release's.

**WebView2.** Before the files, the Evergreen runtime is looked for — the machine's key, then
the user's, a version of `0.0.0.0` counting as absent — and Microsoft's bootstrapper is run when it is absent, online only, as SCOPE says — **with its
own window**, not silently: the first VM test watched tens of seconds of an installer that
showed nothing while the runtime downloaded, so the bootstrapper's own window, with its
progress bar and its *Cancel*, is what the user sees (a cancel there is a failed run, and ends
as one), and the installer's details say that the window is Microsoft's — first as the user (a per-user runtime, no prompt), its exit code written into the
installer's details, **unless Microsoft Edge's machine-level updater is on the computer**
(its `path` under `HKLM\SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate`): then the run as the user
is skipped, since it cannot succeed and, run with its window, the bootstrapper asks for the
machine's rights by itself — the fourth VM test saw Microsoft's prompt, then Enfold's
question, then the prompt again — so the user meets one question and one prompt; and, when
the runtime is still absent after a run as the user, once more **elevated** — the first clean-machine test (2026-09-30)
found the per-user run failing on a machine that has Microsoft Edge, because Edge's machine-
level updater takes over a per-user install and needs the machine's rights (Microsoft's
distribution notes say so) — but only after asking (the user's ruling, 2026-09-30): "Enfold needs the Microsoft Edge
WebView2 Runtime. Install it now? Windows will ask for permission — it is Microsoft's
installer." with a message box's *Yes* and *No* (its buttons cannot be renamed, and the question reads as
one); the elevated run is the one UAC prompt the install can show, and it is Microsoft's signed
bootstrapper's, not Enfold's. *No*, or a runtime still absent after the elevated run (declined, or an installer that failed — its exit code shown),
ends on a **page of its own, the finish page's shape** — no progress bar, no log, since a
progress page left standing reads as a hang (the user's second test, 2026-09-30) — titled
*Enfold was not installed*: the text says that nothing was installed — which is true, the
check running before any file, key or shortcut is written — and why, in the words of what
happened: the permission declined in Windows' prompt (the elevated run never started —
there is no exit code then), or an account that cannot grant it (Windows asks such an account
for an administrator's credentials; without them the same page says that the runtime is
machine-wide, that an administrator installs it once from Microsoft's address, and that any
account can install Enfold after that), or Microsoft's installer ran and failed (the as-user run's exit code shown, in hexadecimal as
Microsoft documents them; the elevated run's is not reported — NSIS's `ExecShellWait` throws it
away — and the details say so); Microsoft's download address in every case, no *Run Enfold* box, and the one button says
*Close*. It is the finish page with its fields rewritten: the section, stopping before the
first file, writes nothing and lets the wizard run on (the progress page closes itself), and
the finish page's pre-function swaps the title, the text and the box when the stop is
recorded — a stop after the first file is written keeps Modern UI's own *Installation Aborted*,
which is then the truth. The uninstaller ends the same way when it cannot proceed (Enfold
running and *Cancel* chosen; a folder it cannot write): its finish page titled *Enfold was not
removed*, the reason, *Close*. A silent
install (`/S`) is not offered in 1.0. Windows 11 ships the runtime; a
Windows 10 machine may not, and the clean-machine test below is that path.

**A running Enfold.** The installer never kills the process: an operation may be running — a
commit is crash-safe, a staged drag is nobody else's to end, and a kill is not an exit. Before it writes, it opens `enfold.exe` for appending — a write handle that changes nothing;
a sharing violation means Enfold is running (the executable of a running process cannot be
opened for writing), and the installer says "Enfold is running. Quit it from the tray icon, then
Retry." with *Retry* and *Cancel*; an access denied is not that — a stopped executable the user
cannot write is a permissions problem, and the installer says so ("Enfold's folder cannot be
written. Check the permissions on <folder>.") and stops rather than asking for a quit that
would change nothing. The uninstaller does the same
before it removes anything.

**Upgrade** is the installer run again: the files replaced in place, the shortcut and the
association rewritten, the uninstall entry's version updated, and nothing under the data folder
touched. A downgrade is the same run — nothing in the program folder is versioned data — except
that a vault or an archive written by a newer format refuses to open under the older program,
which is FORMAT.md's rule (§10), not the installer's.

**`.efd` belongs to Enfold** (decision 3). The installer registers `.efd` for the user — the class `Enfold.efd` (a ProgID has no spaces),
shown as "Enfold archive", the application's icon; the registry view the 64-bit Explorer reads,
since the installer is a 32-bit image — and the uninstaller unregisters it, restoring what was there before — the association that was
the user's before the first install, which an upgrade must not overwrite with Enfold's own (the
backup is taken only when the current default is not `Enfold.efd`); the open command quotes the
executable's path, since a per-user install path carries the user's name and its spaces; and a
WebView2 version of `0.0.0.0` counts as absent, as Microsoft's detection rule says. Opening an archive from Explorer — a double-click, *Open with*, a path on the
command line — reaches the one running instance (§2.4's single instance: a second launch hands
its arguments over and exits) or starts one, shows the window, and asks the core to open the
file **by its envelope's `archive_id`**, never by name: a record with that id opens as if its
row had been double-clicked — the vault unlocked first when it is locked, the lock scene and
then the archive — and the record's `last_path` moves to this path when it differs, the file
having just proved where it is, the same write *Locate…* makes (decision 4); no record with
that id → the page says "This archive is not in your vault." with *Import archives from a
backup…* as the way on (ruled 2026-10-17: "key" there was the archive key, a technical word), and
the file is not touched; a file that is not an Enfold archive (the magic wrong) is a
toast and nothing more. Several paths at once: the first is opened, the rest are counted and named in a toast ("2 more
archives were not opened: spare.efd, other.efd. Enfold opens one at a time."). The Archives
page's *Open file…* (Ctrl+O) and an `.efd` dropped on that page take this same path, from the
page rather than the shell (ruled 2026-10-14): the page numbers those opens itself, apart from
the shell's request numbers, and the newest open of either kind is the one whose answer
lands. A forgotten
record answers `archive.forgotten` as everywhere else (§13); a path that is not there answers
`archive.not_an_archive`, there being no archive either way; a path inside the data folder is
refused before anything is read, §3's vault-source rule holding for a command line as for a
dialog. **How the path reaches the page** (built 2026-09-21): at launch the shell parses its
arguments — flags skipped, a relative path resolved against the working directory, folders and
missing files skipped, the first remaining file the request — and holds the request in a slot
the page asks for at boot (`Shell.PendingOpen`, right after its first `Status()`), because an
event emitted at launch has no page to hear it yet; a second launch hands its arguments to the
running instance (`SecondInstanceData.Args`, which unlike `os.Args[1:]` still carries the
program's own path first — dropped, with a test), which stages the same slot and also emits
`shell.open`, so the request opens once whether the window was up (the event) or being
recreated from the tray (the slot); a sequence number makes it once. The core's call is
`Archives.OpenPath(path)` (§3), answering the archive's id and whether the record's path moved;
a vault that is Locked answers `needs_unlock`, the page keeps the path across the lock scene
and retries once after the unlock — or at once, when the refusal arrives after the status has
already turned Unlocked. Two more rules the review of the build wrote (2026-09-21): an archive
already open from another path is not moved — `archive.open_elsewhere`, the page naming the
open path ("This archive is already open from <path>. Close it there first.") — while the same
path simply shows it; a request staged while an earlier one is still waiting merges into it, the
earlier first path staying first and the newcomer's joining the rest; the page serialises the
requests and drops a result whose number is no longer the latest, leaves the archive it was
showing before it opens another, and takes down the previous request's dialog when it accepts
a new one; the envelope is read from a handle that has passed §3's opened-source check, so a
hard link to the vault answers `archive.source_is_vault`.

**Uninstall** removes the program folder, the shortcut, the association, the uninstall entry
and the WebView2 profile at `%LOCALAPPDATA%\Enfold\WebView2` — recreatable, and meant to hold
nothing decrypted: every response is `no-store`, the profile is swept at start and removed at
exit, and inspecting it after a preview is a release gate (§4) — and **nothing else under
`%LOCALAPPDATA%\Enfold`** (decision 5):
the vault, the settings and the log stay, and the uninstaller's last page says so: "Your vault
and settings stay in %LOCALAPPDATA%\Enfold. Delete that folder yourself if you mean to." — the path
shown expanded, since it is a folder the reader may go and find. `%TEMP%\Enfold` is left to the
temp folder's own life. Nothing is removed that the user could not recreate. The uninstaller removes itself too: an NSIS uninstaller copies itself to the temporary folder
and runs from there by default — the manual's `_?=` switch exists to turn that off — so the file
it deletes is never the running image, and the folder really is gone afterwards (verified
2026-09-21 against the stub; the script must never pass `_?=`).

**Distribution** (decision 6). GitHub Releases, one asset per release — `enfold-amd64-installer.exe` —
with its SHA-256 in the release notes, and the notes saying that the binaries are unsigned
(SCOPE, ruled 2026-09-13) and what SmartScreen shows: "Windows protected your PC" → *More
info* → *Run anyway*. No portable archive in 1.0: the executable runs from anywhere with its data
in `%LOCALAPPDATA%` regardless, and can be a second asset later. Not in 1.0 (decision 7): starting
with Windows (a settings switch writing the user's Run key, later), a machine-wide install,
languages other than English.

**The clean-machine test** — a VMware guest with Windows 10 and no WebView2 runtime: install
(the bootstrapper runs), first run and a vault created, an `.efd` double-clicked from Explorer,
the installer run again over the running instance (refused, then accepted after a quit from
the tray), uninstall with the vault still in place and the uninstaller saying so, reinstall
opening the same vault. And, on the same guest, the broad test of the application itself.

**Maintenance — the next release's, designed now** (the user's ask, 2026-09-21: "a mode that
detects the installed version and offers update, repair and uninstall"). The same installer
file serves all three, so nothing else has to be shipped or found. At start it reads the
uninstall entry (HKCU, `DisplayVersion`) and, when one is there, opens on a maintenance page
instead of the welcome: the installer newer than what is installed → *Update* (the ordinary
run, the page saying which version becomes which); the same version → *Repair* (the ordinary
run: the files written again, the shortcut, the association and the entry rewritten) or
*Uninstall* (the installed `uninstall.exe` run and waited for, the installer then closing);
the installer older → the page says so and offers to install this version over the newer one
anyway, the caveat being FORMAT.md's — a file written by the newer program refuses to open —
and nothing the installer does. The uninstall entry gains `ModifyPath` pointing at the
installer's own place, so that *Apps & features* shows *Modify* beside *Uninstall* and opens the
same page (the installer copies itself to the program folder for that; the copy is what the
uninstaller removes). In 1.0 the installer over an existing install simply installs — the Update
and Repair paths without the page — since there is no second version to tell apart; the page
comes with the first release that has a predecessor. Checking for updates from inside the
application — an outbound request to GitHub — is an online service by SCOPE's rule and waits
with the rest of them.


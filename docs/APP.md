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
`RotationPending`, `Slots()` — are cached at lock and refreshed by a fresh `keystore.Open` on
demand; a reopened file whose `VaultID` differs from the cached one is refused. The keystore
file is held open only while Unlocked.

**Unlocking.** One ceremony at a time (§2.2), on its own goroutine with a top-level `recover`
that routes to "lock and report". `BeginUnlock` while Unlocking or Releasing returns
`ceremony.in_progress` / `ceremony.releasing`, and never reaches `piv.Open`.

**Unlocked.** Holds `*keystore.Unlocked` only for the duration of a mutation that needs the VMK
(enroll, remove, rotate, rewrap, export, backup restore) and otherwise only `*keystore.Session`;
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
the trigger does not publish; in Locked it is a no-op. **Zeroing is synchronous on the message
thread:** the trigger handler calls the core's `lockNow`, which takes a short mutex that no
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
SlotCount, Hardware, Password, Recovery}`, where a *backup* has only recovery slots active (R28's
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
usable token nor a password — an adopted backup being set up from a recovery-key session, a
vault whose one token is stale — proves itself with the recovery key.

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
      ▼                          │ no enrolled key      wrong PIN ◀───────────┘                 └──▶ Touch again  └──▶ Password
   TwoKeys                       ▼                      blocked ──▶ Blocked                ErrTooManyOperations ──▶ WaitingForKey
   (remove one)              NoMatch                    ErrBusy at Open ──▶ Busy (terminal until the user acts)
   any exit ──▶ Releasing (Card.Close in the ceremony goroutine) ──▶ Locked
```

- The driver polls `Readers()` every 500 ms while waiting; one reader → `Open`; more → `TwoKeys`.
  A stopped Smart Card service while waiting is the empty reader set, not a failure: Windows
  starts the service when a reader arrives and stops it when the last one leaves; it is logged
  once per wait, and after ten seconds of it the waiting state carries `token.no_service` as a
  note, so a service that stays down is said on the screen, not an hour of silence. Every
  ceremony's end that is not a success is logged with its step, code and underlying error, and
  an enrolment logs what the token holds and what it decided; the log is appended across runs.
  A failed `Open` is never fed back into the poll (DESIGN trap 24): `ErrBusy` parks the ceremony
  in `Busy` until the user cancels or retries.
- Probing: `Card.Keys()` (no PIN, no touch) matched by public key against the keystore's
  hardware slots; R34 makes the match unique. No match → `NoMatch`. The matched slot's
  `EntangledPassword` decides whether `Password` comes first — the credential is assembled before
  any prompt, because `keystore.Unlock` takes it whole and the PIN prompt fires inside `ECDH`.
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
  session's absolute default — never on the card call itself, which cannot be interrupted. A
  cancel during a card call — the touch — takes effect when the card answers (a YubiKey gives up
  waiting for a touch on its own, after a while it does not document; DESIGN trap 23): the state
  says `Cancelling` meanwhile, the panel's Cancel is spent, and every retry loop checks the
  cancellation before asking the card again, so a cancelled ceremony never prompts a second
  touch. A ceremony that ends cancelled is gone from the page at once — nothing to read, nothing
  to close.
- Touch: `Prompter.Touch` fires `vault.ceremony {Step: touch, N}`; the panel takes over.
- **A wrong secret is said, in place.** A refused PIN makes the next PIN prompt carry
  `token.pin` as its note beside the count; a wrong password or mistyped recovery digits
  (`keystore.ErrVerifier`/`ErrAuth` under a typed credential) are asked for again under a fresh
  prompt id with `vault.auth` as the note — the page marks the field, says why, and keeps the
  recovery digits for correction — never a failure the user has to start over from; the
  derivation shows again after the corrected answer. A note describes its prompt only: taken,
  gone or cancelled, the prompt clears it. The management-key PIN of an enrolment follows the
  same rule. Only a token's refusal (a damaged record, a stale slot) parks.
- Wrong password (`ErrAuth` from Deriving) returns to `Password` with the Card kept open, so the
  retry costs a touch but no PIN on a PIN-once key; the copy says so. `MaxOperations` exhausted →
  "remove and reinsert the key" (WaitingForKey).
- Deriving: `keystore.Unlock(HardwareCredential{Token, Password})` → `Unlocked` → `Session()` →
  `Unlocked.Close()`. Then the ceremony goroutine's deferred `Card.Close()` runs (Releasing);
  `ErrResetFailed` is a warning event, never a failure. The publish point checks the lock latch.
- Recovery key and standalone password skip the token states. `Vault.SubmitPassword` serves
  both the standalone slot and the entangled password; there is one submit per secret kind.
- The rewrap loop after a deferred rotation holds one `Unlocked` across all stale slots (a stale
  slot cannot unlock itself) and adds a `SwapKey` step — "remove *A*, insert *B*" — between
  tokens, one YubiKey at a time; `ErrStale` at the lock screen is reported as "this key is
  behind; unlock with another way in first", never as corruption.

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
  (added / replaced / renamed / deleted, with the new name or `FileInfo`), a **folder projection**
  over the merged view, a preview **token** (32 random bytes, minted at Open, forgotten at
  Close), a reader count and a last-served time, and two clocks.
- **Dirty**: the first change calls `Begin()`; `Tx.Add` writes at once, so "3 changes not yet
  saved" means three recorded changes whose data is in the file but not published. Save =
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
  `PreviewURL`, in-flight readers, `AddFiles`/`Delete`/`Rename` into the staged transaction;
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
under an operation id and reports through events. Ids are hex strings; times are Unix seconds;
sizes are `uint64`. **Every service method returns `*app.Error`** — `{Code, Retries?, Slot?}`
whose `Error()` is the code and nothing else, produced by one `classify(err)` over every
sentinel of every package with a catch-all `internal` — and services are registered with a
`MarshalError` that emits only that shape; the original error goes to the core-side log.
`CeremonyState.Error` and `op.done.Error` use the same codes.

**Vault**
- `Status() VaultStatus{Seq, State, Path, DisplayName, LastUnlockedAt, LocksAt, AbsoluteAt,
  RotationPending, Tampered, Warnings []Code, Ceremony *CeremonyState, Ops []OpView,
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
  the key**: for a token with an entangled password, create and enrol ask for the password
  first, and only then wait for the key, inspect it and generate — a cancel at the password
  leaves nothing on the token. Such prompts carry `Choose: true` on the ceremony state (the
  page says "choose a password", never the same words as an existing one); a password typed to
  prove a current way in never does.
- Events: `vault.state` (the whole status), `vault.ceremony {Seq, Step, PromptID, SlotLabel,
  Retries, RetriesKnown, ReaderCount, N, Error}`, `vault.warning`, and from the shell
  `secret.refused {Code}`. A ceremony whose last step is the recovery key's reveal ends with that
  step in its final event, so the page shows the one-time URL above whatever view it is on.

**Archives**
- `List() []ArchiveSummary{ID, Name, Path, StoredSize, LastWrittenAt, KeyVersion, Open, Dirty,
  ReceiptOwed, NoCompression, Note Code}` — hidden records are filtered; `ShowHidden` flag
  lists them. `Open(id)`, `Close(id)`, `Create(path, name, noCompression)`, `Hide(id)`, `Unhide(id)`,
  `Locate(id, newPath)`, `Compact(id) opID`, `RotateKey(id) opID`, `Verify(id) opID`,
  `CloseAll()`. **There is no Forget in 1.0**: the registry record holds the only copy of the
  archive keys, so dropping it destroys the archive; the destructive form, if ever wanted, is a
  separately confirmed `ForgetKey` that names that consequence.
- Events: `archives.changed`, `op.progress {OpID, Done, Total, Phase}`, `op.done {OpID, Error,
  Results []FileOutcome}`.

**Archive** (an open one)
- `Page(id, folder, sort, offset, limit) Page{Seq, Rows []FileRow{FileID, Path, Name, Size,
  Storage, SavedPercent, ModifiedAt, Pending}, Total, Folders}` over the merged view; `Name` is
  the leaf within `folder`, `Path` the full stored name. A name that is both a file and a prefix
  (`a` and `a/b`) shows as both. `Stat(id) ArchiveStat`.
- `AddFiles(id, folder, paths, policy) opID`, `AddFolder(id, folder, path, policy) opID` —
  composed names validated with `format.ValidateFileName` and checked against the merged view
  **before** the first `Tx.Add`; `policy` is `skip | replace | keep-both`; `CheckNames(id, folder,
  names) []Collision` lets the UI ask once. `Replace(id, fileID, path) opID` is the in-place
  edit (never Delete + Add). `Delete(id, fileIDs)`, `Rename(id, fileID, newLeaf)` (a `/` in the
  leaf is refused; folder rename is one rename per record under the prefix, pre-flighted),
  `Extract(id, fileIDs, dir, policy) opID` (target `filepath.Join(dir, FromSlash(name))` with a
  containment assertion, `MkdirAll` per parent, `skip | rename` on `os.ErrExist` — never
  pre-`Lstat`, never overwrite by unlinking; case-folded destinations de-duplicated by the
  planner before the first file; each file all-or-nothing, the batch not), `Save(id) opID`,
  `Discard(id)`, `PreviewURL(id, fileID)` (only for committed rows; staged adds and replaces are
  not previewable until Save), `PreviewText(id, fileID, maxBytes) {Text, Truncated}` (over
  `OpenReader` + `LimitReader`; no cross-origin fetch exists). `Stat` carries `CopyMismatch` and the
  list a `Note` of `archive.copy_mismatch` when the file's seq is not the one the registry last
  saw (an older copy restored): shown, never adopted silently; a save records this copy.
- Events: `archive.changed {ID, Seq}`, `archive.expiring {ID, ClosesAt}`, progress as above.

**Keys**
- `Slots() []SlotView{RecipientID, Type, Label, CreatedAt, Entangled, Stale, Escrowed,
  Removable}` — `Escrowed` says a recovery slot can be shown again (FORMAT R38), known while
  Unlocked; `Removable` says the invariant would still hold without the slot
  (`keystore.Removable`), so the page greys *Remove* before any ceremony is run for it.
- `BeginEnroll(kind, label, entangle bool)` runs the ceremony for the VMK and releases the key
  that unlocked, then — when `entangle` — asks for the new password (`Choose`), and only then
  for a YubiKey the token flow: `SwapKey` waits for the unlocking key to be *out* of the reader
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
  `vault.slot_not_found` (not an active recovery slot), `vault.no_escrow` (the slot predates
  escrow; `SlotView.Escrowed` lets the page say so first), `vault.setup_needed` (a vault with
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
  is a capability to a value the page already fetched, not the value. An unlock, a proof or a
  setup through a recovery key whose slot has no record writes the record then
  (`EscrowOpenedKey`; FORMAT R38), so a vault from before escrow comes under it the first time
  its key is typed.
- `RemoveSlot(recipientID)` (refused with the invariant's reason), `RotateNow()`, `RewrapStale()`
  (the loop of §2.2), `Export(path)`, `VerifyBackup(path)` (§2.1: a staged copy opened with the
  recovery key, nothing kept). What a backup is and whether it is this vault is
  `Vault.InspectFile`; restoring a single archive record from a backup (`RestoreArchiveRecord`,
  re-wrap under the current KWK) is deferred (§12).
- **Slot changes and registry writes never share the handle.** While a slot-change ceremony
  runs, a registry write is refused with `ceremony.in_progress` — a Save still commits its
  archive and owes the receipt, paid when the ceremony ends — and a slot change does not start
  while a save, verify, compact or rotation is running (`op.in_progress`). A lock trigger in any
  state latches and cancels the ceremony, and the lock's close of the handle waits for it.
- **Tampered is a state, not a banner**: every mutating call and Export is disabled with the
  reason; the one action is importing a copy of this vault (§2.1); it is never cleared silently
  (R25). The reveal stays: it changes nothing, and the way out of a tampered vault may be the
  key it shows.

**Settings** — `Get()`, `Set()`. Machine-local, in `%LOCALAPPDATA%\Enfold\settings.json`
(temp-then-rename): the vault's path when kept elsewhere (empty: `vault.eks` in the data folder)
and its display name, close-to-tray behaviour, theme, look, recovery
record percentage, dictionary threshold. **Security-relevant values live in the authenticated
registry, not the file:** the idle and absolute minutes (`Registry.IdleMinutes`,
`AbsoluteMinutes`, zero = default) and the per-archive compression choice
(`ArchiveRecord.Policy` bit `no_compression`); `Compress.Padding` rides with it.

**Shell** — `ShowWindow`, `CloseWindow`, `PickFiles`, `PickFolder`, `SaveFile`, `Reveal`, `Quit`
(names the unsaved changes in a native Yes/No question — the only buttons a Windows message box
has — then `ResolveForShutdown`, then `app.Quit()`; never asked twice). A cancelled native file
dialog is "nothing chosen", never an error; a submitted secret that found no prompt is reported
back as the `secret.refused {Code}` event. Tray and menu callbacks run on the Wails main thread
and leave it (a goroutine) before touching the window or a dialog. The service lives in `internal/app/api` like the others and holds the Wails
calls behind an unexported `Hooks` value the shell supplies, so nothing of Wails is reflected. File
drop: the window is created with `EnableFileDrop`; the shell re-emits the dropped paths and the
drop target's `data-archive-id` / `data-folder` to the frontend, which calls `AddFiles`; the
core validates that the archive is open and the folder exists in the projection, and refuses
loudly. Dropped paths carry no authority beyond what a file dialog would.

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
unpublished, which the format tolerates. The tray's Quit asks the user first and then runs the
same function; `WTS_SESSION_LOGOFF` runs it without asking.

## 6. Screens (the Native look)

Lock screen (one panel, three-step line, the states of §2.2 including TwoKeys, NoMatch, Busy,
Blocked, SwapKey; secondary: recovery key, password, open a backup — never a second vault; the
"workstation-lock detection unavailable" and BitLocker warnings). Archives (list with details
pane, commands Open / New archive / Compact / Rotate key / Verify / Hide, the deferred-rotation
banner, the Tampered state, the status strip with the countdown and Lock). Archive (breadcrumb
projection, paged table with pending markers, preview pane — image, video, audio through the
loopback URL, text through `PreviewText`, everything else "Extract…" — pending bar, toolbar,
drag-and-drop, the expiring prompt, the locked banner). Keys & backups (slots, Add a key,
Remove — greyed while the invariant would refuse — Rotate now (its dialog says every way in is
rewrapped here and now, and names the one exception: a hardware key with an entangled password
other than the one that unlocked), Rewrap, *Show recovery key…* — shown only while a recovery
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
On the Keys page a recovery slot without escrow (`Escrowed` false) has *Show recovery key…*
disabled and the line "Made before Enfold kept recovery keys: it cannot be shown again. Add a
new recovery key, then remove this one — or unlock with it once, which keeps it."

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

**Motion.** Every dialog, menu and popover enters over 140 ms (the box also scales from 97%)
and leaves over 100 ms; a panel that swaps its content in place — the ceremony panel between
steps, the lock screen's three cards — fades the new content in over 140 ms and the cards ease
between live and dim; toasts fade. The script durations live in `lib/motion.ts` (`FAST` 140,
`LEAVE` 100), the stylesheet's `--dur-fast` is the same 140 ms for the CSS-driven easing, and
the two are kept equal by hand. `prefers-reduced-motion` zeroes both: the script reads the
media query when a transition starts, the stylesheet's token collapses to 0. A closed dialog
answers no key or click while it fades: Svelte marks the element inert the moment the outro is
committed (synchronously, before any frame) and clears that if the dialog is reopened mid-fade,
and the handlers check `inert`. Besides these, only the touch rings (gated on `no-preference`)
and the 120 ms hover tint on controls animate.

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
- `registry_version` 2: the recovery-key escrow records (R38, §7.6) under `KWK_recovery` (R3);
  version 1 read and rewritten.

## 12. Deferred, and open for the user

pdf.js preview; `ForgetKey`; memguard for the session keys; an entropy estimate for a chosen
password (SCOPE: "with an entropy estimate shown"; the minimum of 8 stands in for it);
`RestoreArchiveRecord` (one record
from a backup into the vault, re-wrapped), and merging records from another vault; folder move
as one operation;
`overwrite` on extraction (an archive-layer change); an unelevated BitLocker check (measure
first — if none exists, the SCOPE bullet or the least-privilege ruling has to move); the
permitted range of the timeouts beyond the clamps.

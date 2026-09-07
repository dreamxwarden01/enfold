# Enfold v1 scope

`DESIGN.md`, `FORMAT.md` and `SYNC.md` describe a system considerably larger than v1. This
document says what v1 actually ships, so that a design covering sync, phones and post-quantum
migration does not turn into a v1 that never gets finished.

`DECISIONS.md` records the project's own assessment that **the most likely failure mode is not
imperfect memory hygiene but never shipping.** This file exists to act on that.

---

## v1 ships

**Platform:** **Windows 10 and later.** Go + Wails v3 (beta, pinned to one tag). No macOS, no
Linux, no CI matrix. The installer checks for the WebView2 Evergreen runtime and runs Microsoft's
bootstrapper when it is absent — it is part of Windows 11 but not guaranteed on every Windows 10
machine.

Windows 10 is in scope because a substantial number of people still run it, and that has
consequences worth stating up front:

- **TPM 2.0 cannot be assumed.** It is mandatory on Windows 11 and merely common on Windows 10, so
  device binding treats it as an optional enhancement, not a prerequisite (`SYNC.md` §3.1).
- **DPAPI machine scope is the floor and must be implemented and tested**, not left as a branch
  nobody exercises. It is available on every Windows version, so an unbound identity never occurs
  on this platform.
- **The newer WebAuthn APIs are unavailable**, which rules out `prf-derived` slots on Windows 10.
  They are reserved and unused in v1 regardless.

**Keystore**

- One keystore file, superblock A/B, slot region A/B, encrypted registry (`FORMAT.md` Part I)
- Slot types: **hardware (YubiKey PIV 9d, P-256 ECDH)**, **standalone password**, **recovery**
- Software slots are hybrid X25519 + ML-KEM-1024 (`FORMAT.md` §3.1)
- Optional entangled password, off by default, with an entropy estimate shown
- Recovery key: 48 digits, BitLocker encoding with its checksum
- The slot invariant enforced as a predicate on every mutation
- VMK rotation, pre-selected on removals and password changes
- Manual keystore export: registry + recovery slot only
- **One vault per Windows user**, at `%LOCALAPPDATA%\Enfold\vault.eks`; a vault or a backup from
  elsewhere is *imported* — copied in and proved with one of its own ways in before the file it
  replaces is retired as a dated copy — and a backup (recovery slot only) is adopted by finishing
  setup with the recovery key; a backup is verifiable without touching anything; a vault kept
  elsewhere is an explicit advanced choice (`APP.md` §2.1, ruling 2026-09-06)

**Archives**

- Archive files with plaintext envelope, encrypted index, per-file DEKs (`FORMAT.md` Part II)
- zstd with sampling-based skip for incompressible data; no seekable zstd
- AES-256-GCM in the STREAM chunked construction, 64 KiB chunks
- Add, extract, delete, in-place edit of one file, archive-key rotation
- Free-space map with first-fit allocation and offline compaction

**Application**

- Unlock, lock, idle and absolute timeouts, the lock triggers in `DESIGN.md` §10
- A tray-resident process. **Provisional:** closing the window destroys it (and the WebView2
  process group with it) and the window is recreated on demand — 7–9 MB idle against ~130 MB
  open, measured 2026-09-04 (`DESIGN.md` §14). Settled once the real UI exists: kept if reopening
  shows no noticeable delay, stutter or state loss; otherwise the window is hidden and ~130 MB
  resident is accepted (see "Deliberately unresolved")
- Unlocked-state banner with countdown, and a tray icon that changes when unlocked
- **Look: the Native direction** (`docs/ui/native.html`) — a first-party Windows 11 feel, light
  and dark. One look in 1.0; the interface is a frontend over the Go core's service API, so a
  second look (a denser "Workbench" one was prototyped) is a later frontend or skin, never a
  change to the core
- Loopback HTTP streaming with Range support, for media preview — every response `no-store`,
  and the WebView2 profile with caching disabled, so the browser engine never writes decrypted
  content to disk (`DESIGN.md` trap #13)
- Secrets in `memguard`; `VirtualLock`; crash dumps suppressed
- BitLocker detection with a warning when the system volume or the vault's volume is
  unprotected (suspended counts as unprotected; an unreadable state warns nobody) —
  **provisional until an unelevated mechanism is measured**: the WMI class is admin-only and the
  product never asks for elevation (`APP.md` §9)

## v1 does not ship

Written down so that "just a small addition" has to argue with a list rather than with nobody.

- **All of `SYNC.md`.** No pairing, no phone, no relay, no identity keys, no merge. The format
  fields exist and are written; nothing reads them.
- The phone application, in any form
- Small-file packs (`pack_id` is reserved and written as zero)
- Virtual filesystem (WinFsp / Dokan)
- A sandboxed child process for archive parsing (AppContainer). It would contain a parser
  compromise — the one thing AppContainer is good for here — and is a v2 candidate alongside
  fuzzing (`DECISIONS.md` 2026-09-04)
- Post-quantum hardware slots — pending hardware that does not exist (`FORMAT.md` §16)
- `prf-derived` slots
- Seekable zstd
- Automatic or scheduled backup
- Per-archive authentication policy (the `policy` field is reserved)
- Any online service

- Multi-volume archives and recovery records — **committed for 1.1**, see below; neither needs a
  v1 format change, which is why they can wait

## Committed for 1.1

Two things the user has decided are not optional for a real archiver, recorded here so they are
planned rather than rediscovered (`DECISIONS.md` 2026-09-05).

**Multi-volume archives** — an archive of tens of gigabytes is awkward to transfer or back up
online. The v1 file stays a single, live, editable file; volumes are an **export form**: the
finished archive's bytes split into fixed-size parts, each with a small plaintext header (magic,
`archive_id`, part index and count, byte range, SHA-256 of the part). Reassembly is
concatenation; a reader can also open the parts in place, since offsets map to parts. Parts are
immutable — editing means reassemble, edit, re-export — which matches how RAR volumes behave and
keeps the free-space map and the superblock flip out of it. The per-part hash makes a transfer
verifiable and resumable part by part. Nothing in the v1 format changes. **Part names follow
RAR's convention, not `.001`:** `<name>.part01.efd`, `<name>.part02.efd`, … — the ordinal is
padded to two digits, or to the width of the part count when an export has more than 99 parts
(`part001` … `part120`), so the parts sort in order in every tool and every part keeps the
`.efd` extension that Windows associates with Enfold; a bare numeric extension is associated with
nothing (the user's ruling, 2026-09-06).

**Recovery record** — optional Reed–Solomon parity so that scattered damage (bad sectors, bit
rot, a corrupted transfer) can be repaired instead of losing the file. Computed over
**ciphertext**, so repair needs no key — like `last_ciphertext_hash`, it is something a backup
tool can do without unlocking anything. It lives **beside** the archive, never inside the live
format: a parity sidecar for a single file, or one parity block per volume in the export form,
where damage actually happens. Overhead equals the chosen parity fraction (RAR's default is 3%);
it repairs up to that fraction of the archive in any distribution, and nothing beyond it — a
truncated download is not what it is for. Damage location is already free: every 64 KiB chunk
carries a GCM tag, so the damaged blocks are known exactly, which is the case Reed–Solomon
erasure decoding is best at. **On by default for volume exports at 3%, changeable in settings** (the
user's ruling, 2026-09-05). It is cheap insurance, not a commitment — with no users yet, a
v1 → v2 format bump would be nearly free anyway.

## Before the format is frozen

Once v1 ships to anyone, `FORMAT.md` becomes a compatibility obligation. These three come first.

**1. Test vectors for the KDF chain — done, on both axes.** Primitives (HKDF, Argon2id, ML-KEM-1024) are checked against RFC 5869, the Argon2 reference CLI and NIST ACVP respectively in `internal/kdf/`; the composition is checked by `testdata/kdf-vectors.json`, generated by
`tools/kdfvec` and confirmed against an independent clean-room implementation written from
`FORMAT.md` §3.3 alone (63 of 63 values identical). The path from `H` to `IK` is not a standard construction:
an HMAC fold, then Argon2id, then HKDF, with a conditional branch when no password is set, and a
hybrid variant for software slots. Nothing else will catch a transposed argument or a wrong
`info` string, because every wrong version still produces 32 plausible-looking bytes. Fix the
vectors before the code, not after.

**2. Fuzz both parsers.** The keystore and archive readers both consume attacker-supplied bytes —
an archive file is *expected* to come from an untrusted place. Fail-closed on unknown record types
is the right rule and is exactly the kind of rule that is easy to state and easy to implement
wrongly.

**3. One external review of `FORMAT.md`.** Everything in this project so far has been reviewed by
its own authors. One outside pass on the format specifically, before it becomes permanent.

## Before anyone else runs a build

Unsigned Go binaries trip cloud heuristics in mainstream anti-malware products. During
development this surfaced as Kaspersky's KSN flagging **test binaries** as
`VHO:Trojan-Downloader.Win32.Convagent.gen` — a documented false positive driven by Go's symbol
table and DWARF sections, not by anything the code does.

**On the development machine the fix is an anti-malware exclusion for `GOTMPDIR`**, which is
pinned to `D:\MyPersonalProjects\go-tmp`. Stripping symbols (`-ldflags=-s -w`, kept in
`scripts/test.ps1`) shrinks the trigger surface but was observed being flagged regardless; it is
not the fix.

Why that directory and not the default: the Claude desktop app is MSIX-packaged, and every process
it spawns has its `%LOCALAPPDATA%` writes **silently redirected** to
`%LOCALAPPDATA%\Packages\Claude_pzs8sxrjxfjjc\LocalCache\Local\`. An exclusion added for the real
path never matched the redirected one, and the two are easy to mistake for each other in a log.
Anywhere outside AppData — a workspace directory on another drive — has one path, not two.

**The same redirection will hit the application's own keystore** at `%LOCALAPPDATA%\Enfold\` when
a development build is launched from a Claude-spawned shell: the keystore lands in the redirected
location and is invisible to the same binary started from Explorer. Verify file-location behaviour
from a normal terminal before drawing conclusions from it.

**That workaround must never become user-facing advice.** A release cannot ask people to whitelist
it. The standard answer, required before any build leaves the developer's machine:

- **Authenticode code signing** of every shipped executable and installer. SmartScreen and every
  major AV weight signer reputation heavily; an EV certificate carries reputation from day one, an
  OV certificate earns it with downloads. `DESIGN.md` already requires this for the update
  channel — it is a prerequisite for the *first* release too, not only for updates.
- **False-positive submission** to the major vendors (Microsoft, Kaspersky, and whichever else
  flags a release) for each shipped build, until the signing certificate's reputation makes it
  unnecessary.
- Release builds are stripped and reproducible, so that a submitted sample matches what users
  actually receive.

## Deliberately unresolved

Recorded so they are not mistaken for oversights. Neither blocks v1.

- **Automatic backup.** Manual export covers v1; automatic backup coheres only alongside an online
  service that can own the questions it raises — when to write, where, and how stale copies
  reconcile.
- **First-contact transport** for pairing — LAN mDNS, bidirectional QR, or a relay (`SYNC.md` §8).
  Deliberately deferred, because choosing a relay means hardcoding a hostname into distributed
  binaries.
- **Divergent archive files** — the same archive edited on two devices. Keystore sync handles keys
  and metadata and deliberately does not merge archive contents (`SYNC.md` §5).
- **Destroy or hide on close-to-tray.** The default is destroy (7–9 MB idle). It switches to hide
  (~130 MB resident) if reopening the real interface shows noticeable delay, stutter or state
  loss. A trade-off to be judged by use, not decided on paper (`DECISIONS.md` 2026-09-04).
- ~~**PIV PIN policy and token session semantics.**~~ **Decided 2026-09-05** (`DECISIONS.md`):
  every unlock is PIN + touch; the token is used for the unlock alone and released — and reset —
  the moment the VMK is derived, so nothing is held for the session and no verification is
  relied on across operations. **The invariant that does not move: the card never releases a key
  without the user's participation** (touch policy always) — it must not become an oracle for
  software running on the machine. Settled regardless: never overwrite an occupied slot by
  default, never reset the PIV application, never probe with the default PIN.
- **Sync identity and trust model.** Direction set 2026-09-04 (device identity + keystore-resident
  key co-signing; trust with TTL and level; expiry means a full pairing ceremony); the details are
  designed with the sync feature itself (`SYNC.md` §8).

## The rule this file exists to enforce

> Anything not listed under "v1 ships" is not in v1, and moving something across that line is a
> decision that gets written into `DECISIONS.md` with its reasoning — not a thing that happens
> because it seemed small at the time.

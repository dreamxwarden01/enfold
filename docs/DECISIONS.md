# Decision log

Append-only. One entry per decision, newest last. When a decision is reversed, add a new
entry rather than editing the old one — the reasoning that was wrong is worth keeping.

Format: `## YYYY-MM-DD — <decision>` followed by **Why** and, where relevant, **Rejected**.

---

## 2026-08-30 — Vault is a single container file

**Why:** minimal metadata leakage — an observer cannot even count the archives inside. The
alternative (many standalone `.vault` files) would allow sharing one archive by copying a file,
but leaks the archive count and per-archive sizes.

**Cost accepted:** cloud sync re-uploads the whole file unless the sync client does block-level
delta (Dropbox does; OneDrive and Google Drive generally do not for arbitrary files). Requires
atomic superblock updates and free-space management, neither of which a directory-of-files
layout would need.

---

## 2026-08-30 — YubiKey PIV with P-256 ECDH as the KEK

**Why:** an asymmetric KEK means the public key alone is enough to create a new slot, so a
backup YubiKey can be provisioned **while it is not physically present**. A symmetric scheme
(FIDO2 hmac-secret) cannot do this — the key must be plugged in to add a wrap.

Also: `piv-go` is a pure-Go reimplementation with **zero prerequisites on Windows** (the
built-in Microsoft smart-card driver suffices), where `go-libfido2` would need cgo, libcbor and
OpenSSL 3.

**Rejected:** FIDO2 hmac-secret (cannot provision offline; painful to build on Windows).
Challenge-response HMAC-SHA1 (weakest of the three; the secret is host-generated at programming
time, so it is less hardware-bound).

---

## 2026-08-30 — P-256 rather than X25519, despite X25519 being safer

**Why:** iOS Secure Enclave stores only 256-bit EC keys and performs P-256 ECDH. Choosing
X25519 would permanently close the door on using a phone as a hardware key. Deliberate trade:
we accept the point-validation footgun (mitigated by trap #2 in DESIGN.md) to keep that option.

---

## 2026-08-30 — Entangled password, folded in with HMAC rather than Argon2's `secret`

**Why entangled at all:** hardware guarantees are not permanent (YSA-2024-03 / EUCLEAK,
unpatchable). The password makes an extracted `SK_YK` insufficient on its own. The entanglement
specifically prevents an attacker who holds only the container file from starting a password
grind, because `H` is required before Argon2 can even begin.

**Why HMAC instead of Argon2's `secret` (K) parameter:** Go's `x/crypto/argon2` (verified
against v0.55.0) exposes no `secret` and no `AD` parameter, so the pure form is inexpressible
in Go. Independently, `K` and `X` are thinly covered by test vectors across implementations,
which is a bad property for a long-lived format. `HMAC-SHA256(key=H, msg=password)` gives the
identical property with a construction everyone can verify.

---

## 2026-08-30 — Slot invariant defined on independent failure modes, not slot count

**Why:** two YubiKey slots sharing one entangled password satisfy "at least two slots" but die
together when the password is forgotten. The correct predicate is: **there exist two slots whose
required-secret sets are disjoint.** Evaluated on every mutation, not once at creation.

---

## 2026-08-30 — Recovery key stays at 128 bits

**Why:** 128 bits is already unreachable by brute force. Doubling to 256 buys nothing against a
classical attacker (Grover needs ~2^64 *sequential* quantum operations, which no error-corrected
machine will do) while doubling transcription errors on the one artefact a human copies by hand.
The binding constraint is how the key is stored, not its length.

**Also:** because it is full entropy, it needs no Argon2id — HKDF directly is sufficient.

---

## 2026-08-30 — Compress before encrypt; do not compress incompressible data

**Why:** ciphertext is incompressible, so the order is forced. But video/audio/images gain 0–2%
from zstd while losing clean random access, so they are stored raw. Detection is by **sampling**
(compress 64–256 KiB at level 1, store raw above ~0.95 ratio), not by extension.

**Consequence:** v1 does not need the zstd seekable format. The files worth seeking into are
exactly the ones stored uncompressed.

---

## 2026-08-31 — One DEK per file, not per archive

**Why:** in-place editing is a routine operation for this product. Editing a file inside an
archive shifts every subsequent byte, and those chunks cannot be re-encrypted under the same
`(DEK, counter)` — that is nonce reuse, catastrophic for GCM. With one DEK per archive, editing
a 10 KB file inside a 50 GB archive means rewriting 50 GB. With one DEK per file, it means
rewriting 10 KB.

**Cost accepted:** no solid compression across files (mitigated by zstd dictionary training),
and ~60 bytes of index per file.

---

## 2026-08-31 — AES-256-GCM in a STREAM chunked construction, 64 KiB chunks

**Why AES-GCM:** Windows x86-64 always has AES-NI, making it several times faster than
ChaCha20-Poly1305 on multi-GB archives. An algorithm ID in the header keeps ChaCha available
for ARM later.

**Why chunked AEAD and not CTR:** chunked AEAD already provides random access — that is what it
is for. CTR provides random access with **zero authentication**, which would let an attacker
flip bits in a stored file. CBC additionally brings padding oracles and malleability.

**Correcting a premise:** WinRAR's ability to add files mid-archive comes from its per-file
block layout, not from its use of CBC. Cipher mode has no bearing on whether files can be
appended.

---

## 2026-08-31 — Global KWK, cached for the session

**Why:** the product is a manager. Requiring YubiKey + PIN + touch + password for every archive
opened would be unusable.

**Rejected:** per-archive KWK derivation (`HKDF(VMK, "wrap" || archive_id)`), which would scope
the cache to the archive in use. Rejected purely for the UX reason above; it remains available
if the trade is revisited.

**Cost accepted, explicitly:** a cached KWK unwraps every DEK in the vault, so the cache window
exposes the whole vault. Mitigated by a short idle timeout, an absolute cap, aggressive lock
triggers, and making the unlocked state visible in the UI.

**But the VMK is destroyed immediately after use.** This is not cosmetic: an attacker with only
the KWK gets a one-time snapshot, whereas an attacker with the VMK can add their own YubiKey as
a legitimate slot and keep silent permanent access.

---

## 2026-08-31 — No character-class password rules; measure entropy instead

**Why:** Argon2id hardening is additive (~2^25 against these parameters) while entropy is the
base of the exponent. A 1,000-GPU adversary reaches ~2^42 guesses/year; human-chosen passwords
meeting composition rules land at 25–40 bits, i.e. inside reach within hours. Composition rules
also produce predictable patterns and *reject* strong passphrases — NIST SP 800-63B-4 now states
they SHALL NOT be imposed.

**Instead:** estimate real guessing entropy (zxcvbn or equivalent) and show the concrete
consequence in the UI. Ship a diceware generator, **offered but never mandatory** — users may
choose convenience, but not uninformed.

---

## 2026-08-31 — Session timeouts: idle ~10 min AND absolute ~1 h

**Why both:** an absolute cap does nothing for the most common real exposure, which is unlocking
and then walking away. The idle timeout is the one that actually protects that case.

**Lock triggers, in order of how often they fire:** workstation lock / session switch
(`WM_WTSSESSION_CHANGE` — most frequent and most reliable, do not omit), manual lock, idle and
absolute timeouts, sleep/hibernate/S0 (`WM_POWERBROADCAST`, best effort), application exit.

**Auto-lock does not kill already-open archives** — it destroys the KWK and VMK and blocks
opening anything new, so a running stream or in-progress edit survives. Open archives carry
their own idle timeout, or "keep it open" becomes a bypass.

---

## 2026-08-31 — Project named "Enfold"

**Why:** the product is an archive manager first, with security and privacy as its
distinguishing feature, so the name had to carry both "container / packaging" and
"protection". *Enfold* is the verb the product performs — to wrap something up and enclose it
protectively. `fold` also reads as compression, and *the fold* is itself a protected
enclosure. It resonates with **envelope encryption**, which is the formal name for the
KEK → VMK → DEK hierarchy in DESIGN.md §3.

**Consequence:** the name is now inside the HKDF `info` strings (`"Enfold/v1/IK"`). Renaming
the project from here on is a format change, not a cosmetic one.

**Rejected:** Coffer, Keyward, Portcullis, Strongroom (all leaned too far toward "vault" and
lost the archive-manager half); Cloister, Reliquary, Arca.

---

## 2026-08-31 — Recovery key uses the BitLocker encoding verbatim

48 digits, 8 groups of 6, each group divisible by 11 and below 720,896, sixth digit a check
digit (`x6 = (x1 - x2 + x3 - x4 + x5) mod 11`).

**Why:** the per-group checksum is the reason to copy this format. A mistyped group is caught
as it is typed, rather than surfacing as an opaque failure after all 48 digits are entered with
no indication of which group was wrong.

**Rejected:** Crockford Base32 (26 characters, shorter and fewer confusable glyphs) — the
familiarity of the BitLocker layout won.

---

## 2026-08-31 — Revocation requires rotating the VMK, unconditionally

**Why:** deleting a slot record revokes nothing. Anyone holding an earlier copy of that record
can still derive its IK and unwrap the VMK, which is unchanged — so the KWK is unchanged, so
every DEK is still reachable, *including files added after the removal*.

Rotating the VMK re-wraps every DEK at ~60 bytes per file (~6 MB for 100k files, seconds) and
does **not** re-encrypt file data. This is what the `IK -> VMK` indirection is really for: it
does not create isolation by itself, it makes rotation cheap enough to be unconditional.

**Applies to password changes too.** Without rotation, "I changed my password because I think
it leaked" achieves nothing — the old password plus an old container copy still decrypts files
added in the future.

**Does not depend on TRIM.** After rotation, a slot record recovered from unallocated SSD
blocks decrypts to the old VMK, which opens nothing in the current container. Wear levelling
matters only at the third tier (re-encrypting the data itself).

**Explicit non-goal:** revoking data the adversary could already decrypt when they took their
copy. That is not achievable by any design, and the UI must not imply otherwise.

---

## 2026-08-31 — Recovery slot is asymmetric (X25519 keypair derived from the 48-digit key)

Confirmed. The recovery key is a **seed for a keypair**, not a KEK used directly.

**Why:** VMK rotation must re-wrap the new VMK to every surviving slot. YubiKey slots need only
the stored public key, and the entangled password was just typed at unlock — but a symmetric
recovery slot (`IK = HKDF(recovery_key)`) would force the user to fetch the paper out of a
drawer on every rotation.

That friction is a security failure mechanism, not merely an annoyance: a user who finds
rotation painful will delete the slot record instead, which revokes nothing.

Deriving an X25519 keypair from the recovery key and storing only the public half makes every
slot type asymmetric, so rotation never needs a physical credential present and can be made
invisible and unconditional.

**X25519 rather than P-256** because an X25519 private key is any 32 bytes after clamping,
making deterministic derivation from a seed trivial; P-256 needs rejection sampling into
`[1, n-1]`. The recovery slot never touches Secure Enclave, so the constraint that forced P-256
for YubiKey slots does not apply.

---

## 2026-08-31 — Memory hygiene is not a decisive argument for Rust

**Why this was reassessed:** the earlier framing overstated the gap. `memguard` allocates via
direct syscalls outside the Go runtime, so the GC never touches the region; it applies
`VirtualLock`, guard pages and canaries, and keeps secrets **encrypted in memory**, decrypting
only for the moment of use. `zeroize` only guarantees erasure on drop, leaving the key in
cleartext while alive — which is exactly the window hibernation can land in. On that specific
axis Go + memguard is ahead of default Rust + zeroize. Go's GC is also non-moving, including
Green Tea (default in Go 1.26), so explicit zeroing works.

**What Rust does buy** is compile-time prevention of stray copies: `Secret<T>` implements
neither `Debug` nor `Clone`, so accidental logging or cloning fails to compile. In Go,
`string(key)` compiles fine and the copy is unreachable for zeroing.

**Conclusion:** capability is roughly equal; the difference is discipline versus enforcement.
The language choice should be decided on other grounds. See DESIGN.md §14.

---

## 2026-08-31 — Implementation language is Go, UI is Wails v2

**Why:** the memory-hygiene argument that favoured Rust turned out to be much weaker than it
first appeared (see the reassessment above), so the decision fell to the other criteria — and
those all point the same way. `piv-go` needs no prerequisites on Windows, `crypto/ecdh` and
`crypto/hkdf` are in the standard library, and `crypto/ecdh` validates curve points for free,
which removes trap #2 as a source of error. The learning curve is days rather than months,
and the most likely failure mode for a first desktop application is not imperfect memory
hygiene — it is never finishing.

**Verified rather than assumed:** the entire dependency stack builds and runs with
`CGO_ENABLED=0` on windows/amd64, so **no C compiler is needed**. Wails v2 dropped the CGO
requirement on Windows by moving to the pure-Go `wailsapp/go-webview2`. Resolved versions are
recorded in DESIGN.md §14.

**Wails v2, not v3:** v3 was still beta as of 2026-08.

**Consequence to stay aware of:** Go will not stop a stray `string(key)` from leaving an
unreachable copy on the GC heap. `memguard` protects only what is deliberately placed in a
`LockedBuffer`. This is a review discipline now, not a compiler guarantee.

---

## 2026-08-31 — PIV slot 9d

**Why:** 9d is the Key Management slot, the one PIV designates for decryption and key
agreement — which is exactly what this design does with it. Retired slots 82–95 remain
available to hold superseded keys so that old data stays readable after a key rotation.

---

## 2026-08-31 — The phone is a key authority, not a slot

**Correcting a misreading in this log's own earlier entries.** The phone does not act as an
unlock factor for a PC-held keystore. It carries **its own** keystore, with its own VMK and its
own slots, and either syncs with a PC that likewise has its own, or releases the keys for a
single archive to a low-trust machine while VMK and KWK never leave the phone.

**Why this is better:** a stolen or compromised PC never yields the VMK. Even malware running
while the vault is unlocked gets only the DEKs for archives actually opened in that session. This
is the same least-privilege reasoning as the KWK caching decision, but enforced by physical
separation rather than by memory hygiene — a different order of strength.

**Also corrected:** FIDO hybrid transport cannot be repurposed as a general-purpose E2EE channel.
Windows exposes a WebAuthn API returning assertions, not a socket; the tunnel carries CTAP2
messages and is closed to application data. The "zero infrastructure" research finding applies to
passkey-as-slot, which is not this design. What does transfer is the protocol's design principles
— see the no-SAS entry below.

---

## 2026-08-31 — Sync covers metadata and DEKs only; never slots, never the VMK

Each device has its own keystore and its own unlock methods. A PC must have a keystore of its own
before it can sync at all.

**Why:** this is the scoping that makes sync tractable. It removes the one case that genuinely
cannot be merged — two individually-legal slot removals on different replicas that together
violate the ≥2-disjoint-slots invariant. It also removes cross-device VMK coordination entirely,
making revocation a purely local operation.

**Consequence:** the two sides have different KWKs, so sync unwraps each DEK on the sender and
re-wraps on the receiver. **The channel therefore carries bare DEKs**, which is what forces
end-to-end encryption and forward secrecy in the handshake below.

---

## 2026-08-31 — Device classes, with capabilities in the pin record

Ephemeral session devices may open a named archive and append a new file's DEK. Enrolled personal
devices may additionally receive a full sync. Slots never cross the wire for anyone.

**Why capability bits rather than warnings:** a full sync to a public machine should be
*structurally impossible*, not merely discouraged. A warning is for something the user is allowed
to do. Storing capabilities in the pin record also makes the "persistent connection" feature safe
— a remembered ephemeral device skips the QR next time but can never gain `full_sync`.

**Note this reverses DESIGN.md §10 for one mode only.** Per-archive KWK scoping was rejected
there because a local manager should not re-prompt per archive. In the low-trust-PC mode the
prompt is a tap on the phone and the counterparty is untrusted, so narrow scoping is correct. The
two modes get different granularity deliberately.

Appending is also the cleanest sync primitive available: a new file has a fresh `file_id` and can
never conflict.

---

## 2026-08-31 — Identity key: random, wrapped in its own domain, handshake-only

**Not derived from the VMK.** `HKDF(VMK, …)` is tidy but the VMK rotates on every slot removal,
which would change the device's identity and invalidate every peer's pin. Removing one YubiKey
would silently unpair every device.

**Its own wrapping domain, `KWK_identity`, separate from `KWK`.** The low-trust channel's
job is dispensing individual file DEKs. If the identity key were just another DEK, an over-broad
request or an indexing bug could hand it out. Separate domains make that impossible structurally
rather than by remembering to check.

**Authenticates the handshake and never encrypts session traffic.** Static-static ECDH between
identity keys would mean a later compromise of either private key decrypts every recorded past
session — and those sessions carry bare DEKs. Ephemeral keys per session give forward secrecy.

**Unpairing is local**: delete the peer's pinned public key. No coordination, no network, peer
need not be present.

---

## 2026-08-31 — No short authentication string (no emoji/hash comparison)

**Why not:** both handshake branches are already authenticated, so MITM is cryptographically
impossible and a comparison has nothing to catch. A QR read off a screen is an authenticated
out-of-band channel — an attacker cannot alter the screen. A PAKE is by construction secure
against an active MITM holding only a low-entropy secret.

Telegram and Signal need an SAS because their parties are *remote* with no out-of-band channel.
Enfold's two devices are in the same room, which is exactly what permits a real authenticated
channel instead.

**And adding one has a cost: users do not compare.** Bitwarden's login-with-device rests entirely
on a user comparing five words and names that as its defence against a tampered server. The
principle, borrowed from CTAP folding its BLE advertisement into the Noise PSK rather than
checking it: *a compared value is a policy check and humans skip it; a value that is a key input
makes the attack impossible.* Put the entropy in the key derivation, not on the user's eyeballs.

**Show consequences instead of checksums** — "will be able to receive the full keystore" is read;
an emoji row is not.

**Prompts are bidirectional and asymmetric.** Prompt whenever the peer is absent from your own
pin list — a purely local test. The phone's prompt carries the warning, the 5–10 s delay and the
biometric, because the phone is the side releasing secrets and the PC is the untrusted party.
Matching is on the pinned public key; `device_name` is display-only, or an attacker names their
device "My Pixel".

---

## 2026-08-31 — Merge primitive is `(revision, last_writer)`, with union as the default

**Not timestamps.** Clock skew between a phone and a PC is ordinary and users change clocks;
last-write-wins by timestamp is the classic silent-data-loss bug. `modified_at` is retained for
display and final tie-breaking only.

**Not a content hash either.** A hash answers *different*, not *descended from*, and merging needs
the latter.

**Union by default**, because for a keystore nothing is destroyed by keeping everything — the data
extents are independent. Different files added on each side is not a conflict at all. Concurrent
edits to one `file_id` keep both. Deletions propagate as tombstones so a union cannot resurrect
them, and never win silently over a concurrent edit.

**On the user's proposed fields:** `dek_created_at` and `dek_epoch` are both kept, and they answer
the question that was actually being asked — *did the key change* — with a monotonic counter
rather than a hash, since a counter settles it in 4 bytes with no collision analysis.
`content_hash` separately answers whether the content changed and doubles as the corruption check.
Neither is the merge primitive.

---

## 2026-08-31 — Slot region is doubled A/B, 64 KiB each, `slot_count` capped at 32

**Correcting a badly-posed open question.** It had been framed as "how many slots before the index
must move", which was never the issue — the index is relocatable through the superblock pointer
already.

**The real issue is crash-atomicity of slot mutations**, and they are not rare: every VMK rotation
rewrites every slot record with a fresh `epk` and `wrapped_vmk`, and rotation happens on every
slot removal or credential change. In-place overwriting would let a crash leave a corrupt slot
region and an unopenable container.

**Rejected:** allocating the second landing site from the free-space map. That map lives in the
encrypted index and needs the Metadata key, while slot writes happen around unlock and rotation.
Entangling slot writes with index writes and the allocator adds ordering constraints for no gain.

**Chosen:** double the region and let the superblock say which copy is live — the superblock
pattern one level down, requiring no new correctness argument. 64 KiB per copy is generous
(16 worst-case slots ≈ 21 KB) and 128 KiB is rounding error in a gigabyte-scale container.

The cap at 32 is a judgement, not a technical limit: a vault with 32 unlock methods is a
misconfiguration, and each extra slot adds to the cost of reasoning about the invariant.

---

## 2026-08-31 — `content_hash` is over the plaintext

**Another badly-posed question, corrected.** It had been framed as plaintext versus ciphertext with
"only checkable while unlocked" as the trade-off. That was wrong — the hash lives inside the
encrypted index, so both options are readable only while unlocked, and the stated downside does
not distinguish them.

The question that does: **what does each hash answer that the AEAD does not?** A ciphertext hash
answers almost nothing, because every chunk already carries a GCM tag. A plaintext hash answers
three real questions — duplicate detection, whether the *content* changed during sync as distinct
from whether the *key* changed (`dek_epoch`), and whether what came out equals what went in, which
catches bugs in our own compression and chunking rather than attacks.

---

## 2026-08-31 — First-contact transport deferred until after the desktop version

**Why:** picking a relay means hardcoding a hostname into distributed binaries, and that cannot be
changed later without maintaining two of everything. Desktop-only v1 needs no hostname at all, and
LAN mDNS or QR-both-ways would let the phone version ship without one either.

**If a relay ever is needed: hardcode a name, not an endpoint.** One immutable URL under a
controlled domain, serving a signed manifest that names the current relay. Same pattern as the
update channel; it converts "can never be changed" into "change a signed file", for the cost of
one extra request.

**Sync-related format fields are still written from v1** (`device_id`, `revision`, `last_writer`,
tombstones, the peer pin list) — cheap insurance, but explicitly not load-bearing: with no users,
a v1 → v2 migration would be nearly free.

---

## 2026-09-01 — Architecture: one keystore file, many separate archive files

**Supersedes the 2026-08-30 "single container file" entry**, which had been read as putting slots,
index and data in one file. The intent was a single *keystore*; archives are separate.

- **Keystore file** — one per device, default `%LOCALAPPDATA%`. Slots plus an encrypted registry
  of archives and their keys.
- **Archive files** — many, anywhere. Envelope plus encrypted file index plus data.

**Why nothing that unlocks an archive is ever written into it:** encrypted archives are *designed*
to live in relatively untrusted places — public cloud, removable media, someone else's machine.
Putting unlock material into a container whose whole purpose is to be stored somewhere unsafe
dismantles the threat model. It would also make a centralised key manager pointless.

**What an archive does contain:** `archive_id` and `KID` in plaintext, per-file DEKs wrapped under
the archive key, an encrypted file index, and ciphertext. **No asymmetric key material**, so an
archive presents no target for Shor — an earlier note in this log said "zero key material", which
was wrong; the wrapped DEKs are there, they are simply useless without the keystore.

**Accepted cost:** plaintext `archive_id` makes versions of one archive linkable to an observer,
and separate files leak archive count, sizes and on-disk names. The alternative — trial decryption
against every known key on every open — is worse.

**Consequence that must be stated in the UI:** without the keystore, every archive is permanently
unopenable however intact the files are. The recovery key does **not** cover this: it unlocks *a*
keystore, it does not reconstruct one. Keystore backup is a separate mandatory feature, and cheap,
since the keystore is a few MB.

---

## 2026-09-01 — Three key levels, rotating on different schedules

```
VMK → KWK → archive key (one per KID) → per-file DEK → file data
```

The middle level is what makes an archive portable but locked: it carries everything except the
one archive key, which lives only in the keystore.

**The two rotations are unrelated and must not be conflated** — this is the easiest mistake to
make in this design:

| | Changes when | Why |
| --- | --- | --- |
| per-file DEK | every edit of that file | nonce reuse would be catastrophic for GCM |
| archive key / KID | explicit rotation only | withdrawing access someone was granted |

An earlier draft of `FORMAT.md` had editing a file create a new archive version. It does not.
Version count tracks rotations, which are rare, so the retired-key list stays short.

**Archive-key rotation re-wraps the per-file DEKs and rewrites the archive's index; no file data
is re-encrypted.** Its motivating case is the low-trust-PC mode: that machine received the archive
key, and rotating it is how access is withdrawn.

**Retired archive keys are retained** so that copies made before a rotation — a USB backup, a
cloud copy — stay openable. Retention widens the compromise surface, so the count is shown and
pruning is offered.

---

## 2026-09-01 — VMK rotation is user-triggered, not automatic

**Reverses the 2026-08-31 "rotate unconditionally" decision**, on the basis of a sharper analysis
of what rotation actually buys:

| Attacker has | Removal alone | Removal + rotation |
| --- | --- | --- |
| The credential only | ✅ safe — their slot record is deleted, so no IK can be derived from the current keystore | ✅ identical; rotation adds nothing |
| The credential **and** an old keystore copy | past archives lost; future ones lost too if they obtain the keystore again | past archives lost (unrecoverable); **future ones safe** |

So rotation's entire value is: **protecting archives created afterwards, when the old VMK must be
assumed to have leaked.** Removing a credential is not itself a leak — retiring an old YubiKey is
routine — and forcing the ceremony every time is friction that users defeat by simply not removing
stale slots, which is worse than the thing being prevented.

**But the framing decides whether this works.** A technical checkbox ("also rotate the VMK?") gets
skipped because users cannot answer it. Ask the factual question they can:

> Why are you removing this key?
> — I still have it, it was never out of my control → no rotation needed
> — Lost, stolen, or unsure → rotation strongly recommended

**A deferred rotation must stay visible.** `rotation_pending` in the superblock and a persistent UI
notice, or "later" becomes "never".

---

## 2026-09-01 — The registry name is the trusted name

Two names exist for every archive: the one in the encrypted registry, and the filename on disk.
Anyone can change the latter with no key at all.

**Authorisation prompts show the registry name.** Otherwise malware renames `tax-records.efd`
to `photos.efd` and the user approves "open photos" without a second thought. Using the registry
name closes that, at the cost that a prompt can show a stale name after an out-of-band rename —
worth paying, since it is a trusted source overriding an untrusted one.

Renames made through the manager with a local unlocked keystore are absorbed directly. Renames
detected on disk are surfaced after a successful authorised open, never silently absorbed — a
rename is either the user's own doing or a signal.

Archives are matched on `archive_id` / `KID`, **never on filename**.

---

## 2026-09-01 — Post-quantum deferred past 1.0, and full re-encryption retracted

**Retracting the 2026-08-31 claim that a 2.0 migration "must include full re-encryption".** It
buys almost nothing against harvest-now-decrypt-later, because that attack needs only **one**
harvest. Re-encryption protects only the narrow case of an attacker who compromised once before
migration and compromises *again* afterwards, at the cost of rewriting every archive.

**What the new architecture does buy is much better:** the Shor-attackable material — `epk` and
`slot_pubkey` — exists only in the keystore file. Archives are already post-quantum safe on their
own. The problem shrinks from "make everything quantum-resistant" to "protect one small file",
and the phone-authority mode solves even that for free by keeping keys and data on different
devices.

**A low-friction path exists when it is wanted:** FIDO2 `hmac-secret` is symmetric and therefore
Shor-immune, and its ceremony is *identical* to PIV ECDH — insert, PIN, touch. It is reachable
natively on Windows through `webauthn.dll` with no cgo, and unlike synced passkeys, a hardware
key's `CredRandom` never syncs or drifts. The cost is losing offline re-provisioning, which
"bring both keys when enrolling a backup" plus deferred re-wrapping makes tolerable.

Not for 1.0. Recorded so the path and its reasoning exist when it is wanted.

---

## 2026-09-01 — Software slots become hybrid X25519 + ML-KEM-1024; hardware slot stays classical

**The hole this closes was worse than "no post-quantum".** A classical asymmetric slot hands a
quantum attacker the private key straight from the stored public key, which **bypasses the recovery
key and the standalone password entirely** — no grinding. For anyone who had set an entangled
password, the recovery slot was a free back door around it.

**Cost is near zero:** `crypto/mlkem` is in the Go standard library. Verified empirically rather
than read off a spec — seed 64 bytes, encapsulation key 1568, ciphertext 1568, shared key 32, and
**key generation from a seed is deterministic**, which is what makes derive-from-`R` work at all.
ML-KEM-1024 rather than the recommended 768, because 768 is roughly AES-192 while the rest of the
design is 256-bit.

**The hardware slot is deliberately left classical.** The PQ half would have to run on the token
and no shipping YubiKey does ML-KEM — Yubico has a prototype, but it performs post-quantum
*signatures*, not a KEM, and is not commercial.

**Rejected: switching the hardware slot to FIDO2 `hmac-secret`.** It is symmetric and therefore
Shor-immune, with identical ceremony to PIV. But it would buy that by **permanently destroying
offline re-provisioning** — every rotation would need every hardware key physically present — to
route around a hardware gap that is expected to close. ML-KEM is asymmetric, so a future PQ PIV
applet gives post-quantum security *and* keeps the property. Waiting costs one new `slot_type` and
one new `alg_id` later; switching now costs the property forever. Also weighed: `piv-go` is
verified and mature where the Windows hmac-secret path runs through a v0.1.0 library with a known
bug in the exact code the design would depend on.

---

## 2026-09-01 — Rotation default flips to pre-selected; the question asked is factual

**Reverses the 09-01 "user-triggered" default**, on the review's argument that old keystore copies
are likely to exist (exports, second drives, a file that once sat in a synced folder), and on a new
one: without rotation, an attacker with write access to any copy can **splice an old slot region
back** and revive a deleted slot outright.

Still not silently automatic — that friction is what makes users stop removing stale slots at all.
The framing does the work: "also rotate the VMK?" is unanswerable, "why are you removing this key?"
is not, with *lost / stolen / not sure* defaulting to rotation.

Password changes need separate wording, since that question does not fit them.

---

## 2026-09-01 — `vmk_generation` travels inside the wrapped VMK

`wrapped_vmk` encrypts `VMK ‖ u64 generation` — in the plaintext rather than the AAD, so a
successful unwrap *reports* the generation instead of requiring it to be known in advance. AEAD
authenticates plaintext just as firmly.

Two jobs: it makes a spliced-in slot record detectably stale once the VMK has rotated, and it turns
"unlocked with a stale slot" into an accurate *this credential is behind* rather than a false
corruption report. It does **not** help while the VMK is unrotated — the real defence there is to
rotate, which is why the default moved.

---

## 2026-09-01 — Envelope loses `vault_id` and `created_at`

`vault_id` **broke sync**: every device has its own keystore with its own `vault_id`, so a phone
holding a legitimately-synced archive key would have rejected a file it could open. Left unchecked
it would have been pure leakage — linking every archive a person owns, wherever stored, to one
identity and to a specific keystore file, far beyond the per-archive linkability accepted in §2.

`created_at` contributed nothing to key lookup, sat unauthenticated in plaintext, and duplicated
the filesystem's mtime.

**Also corrected:** the stated reason for random KIDs was "prevents ordering archives in time",
which mtime already exposes. The real reason is coordination — archives are created independently
on several devices and a counter would collide.

---

## 2026-09-01 — First-contact QR carries a per-pairing secret, and pairing ends with a mutual confirmation

**The QR-only branch was not mutual authentication.** The key it carried is the keystore's
*long-term* identity public key, which every previously-paired device already holds — so any of
them could open a handshake and be accepted as a new device. A **fresh 128-bit secret per pairing,
mixed in as the Noise PSK**, is what makes the QR mean anything.

**That still leaves the screen-observer residual**, which the PSK cannot close: whoever photographs
the screen has the secret too. CTAP answers this with BLE proximity, which is unavailable here —
Bluetooth permission on a phone is a tracking capability users reasonably refuse, and not every PC
has a radio. So first contact ends with **both screens showing a handshake-derived value and both
users confirming**.

**This is not a reversal of the no-SAS position; it sharpens it.** That argument was about
*frequency*: a comparison on every login becomes routine and stops being read, while one performed
once in a device's lifetime — with the user actively waiting for the phone they just used to scan —
is a different act. Presented as **pick one of three**, not yes/no, because tapping *yes* without
looking is easy and picking correctly from three is not.

Already-pinned peers use **Noise KK**, not IK: both statics are known in advance.

**Full fix, if wanted later:** bidirectional QR, the PC reading the phone's screen with its webcam.
That would remove the comparison step entirely.

---

## 2026-09-01 — Ephemeral session devices hold no identity key at all

**The "remembered public computer skips the QR" idea is withdrawn.** It required that machine to
persist an identity private key — on a shared computer, a plaintext private key any later user can
take, buying them one-tap approval on the owner's phone. Strictly worse than scanning every time.

**And on reflection an ephemeral device needs no identity at all.** An identity key exists so a
peer can recognise you *next time*; with no next time there is nothing to store. Forward secrecy
comes from Noise's ephemeral keys, which never touch the disk anyway. Framing it as "generate one
and throw it away" would still leave a type, a storage path and a lifetime question in the code;
"there is none" leaves nothing.

The phone does not need to strongly authenticate the PC here: it is the party *releasing* secrets,
and the QR on the PC's screen, scanned by hand, already establishes which machine that is. A
non-authoritative familiarity hint — "3rd pairing with a machine calling itself DESKTOP-LIBRARY-04"
— is worth showing, clearly marked as spoofable and granting nothing.

**The middle case resolves itself:** a machine used often but not owned should get its own keystore,
which needs no YubiKey — standalone password plus recovery key already exists as a tier. There are
two kinds of device, not three.

---

## 2026-09-01 — Automatic backup deferred; v1 ships a manual export of registry + recovery slot only

**Withdrawing "keystore backup is a mandatory feature".** Automatic backup raises questions that
cannot be answered offline — when to write, where, and how stale copies reconcile — and coheres
only once an online service owns them. The practical guarantee for v1 is the slot invariant: at
least two independent ways in.

**What to export has a real answer.** Registry-only is not restorable (archive keys are wrapped
under `KWK`, which needs the VMK, which needs a slot). Registry plus all slots is a full keystore
copy — the one artefact that must not get out. So: **registry plus the recovery slot alone.**
Restorable via the recovery key, **post-quantum safe** because the recovery slot is the hybrid one,
and useless to splice since the slot it carries cannot be opened without the recovery key.

An export must be verifiable before it is needed. An untested backup is a belief.

---

## 2026-09-01 — Android needs a three-tier fallback, each tier disclosed

StrongBox ECDH is not a given: `KeyAgreement` arrived with StrongBox version 100 / API 31, and
StrongBox implements only a subset of algorithms, varying by vendor, with key generation permitted
to fail when the hardware lacks support.

**StrongBox → TEE-backed Keystore → pure software**, with the tier stated at enrolment and at every
unlock, and the software tier labelled plainly as basic integrity and relatively weak protection.
Silently degrading while implying hardware backing would be worse than either honest outcome.

`phone-native` on Android is **unverified until tested on real hardware** and must not be scoped
into a release on the assumption that it works.

---

## 2026-09-01 — v1 scope written down in `SCOPE.md`

The design covers sync, phones and post-quantum migration; the code is zero lines. This log's own
assessment is that the likely failure is never shipping. `SCOPE.md` states what v1 ships, what it
explicitly does not, and the three things that must happen before the format is frozen — test
vectors for the non-standard KDF chain, fuzzing both parsers, and one external review.

---

## 2026-09-04 — Device binding: TPM wrapped blob, DPAPI machine scope as the floor

An identity key wrapped only under `KWK_identity` travels with the keystore file. Enrolled devices
bind it to the machine as well, so that **a copied keystore cannot reuse the identity**.

**No attestation.** Attestation proves to a *remote third party* that a key is hardware-held, and
costs an Endorsement Key, an attestation CA and privacy-CA machinery. The counterparty here is the
user's own phone, which the user has already aimed at a specific machine by scanning its screen.
The property needed is only non-exportability, which a plain non-exportable key provides.

**Wrapped blob, never a persistent handle.** `TPM2_Create` under the storage primary, keep
`TPM2B_PRIVATE` plus the public area in the keystore file, `TPM2_Load` transiently. This consumes
**no persistent TPM storage** — which answers the hygiene concern directly: persistent handles are
scarce, and an orphaned key left in some machine's TPM would have no cleanup path. Here the key
material is in our own file and deleting the keystore deletes it.

**DPAPI machine scope, not user scope.** User scope keeps its master key in `%APPDATA%`, inside the
user profile, so a profile backup or a synced user folder carries it along — losing precisely the
protection it was chosen for. Machine scope keeps it in `%WINDIR%\System32`. Its weakness (any
process on the machine can unwrap it) is already covered by per-request approval on the phone.

**Never on the unlock path.** A TPM is lost to a clear, a firmware reset, a board replacement, a
Windows reinstall. If any of those could brick the vault the design would be indefensible. On the
sync path the worst case is *re-pair*.

Verified rather than assumed: `github.com/google/go-tpm` v0.9.8 builds with `CGO_ENABLED=0` and
reached the TPM on the development machine (Intel PTT).

---

## 2026-09-04 — The binding tier is informational; it gates nothing

**Rejected: gating the approval-skip window on TPM/DPAPI/none.** The window is bound to a session
(see below), and a stolen identity cannot inherit a session's grant — so binding strength is simply
irrelevant to it.

Two facts settle where the tier does and does not matter:

| Threat | Unbound | DPAPI | TPM |
| --- | --- | --- | --- |
| Keystore copied elsewhere | ✗ | ✓ | ✓ |
| User storage synced or backed up | ✗ | ✓ | ✓ |
| Full disk image, offline | ✗ | ✗ | ✓ |
| Live malware as the user | ✗ | ✗ | ✗ |

**TPM's only advantage over DPAPI is the offline disk image** — a TPM stops a key being *extracted*,
not *used*, so malware on the machine just asks the TPM to use it. And **DPAPI is always available
on Windows**, so "unbound" never occurs there at all.

Therefore: show the tier once at pairing, record it in the pin record, gate nothing. What the tier
*would* have gated — may this device be remembered — is already decided by whether the machine has
a keystore, and the two lines coincide.

---

## 2026-09-04 — The approval-skip grant belongs to the session, not the device

The design's answer to a stolen device identity. An attacker holding the identity key can open a
**new** session; a new session carries **no grant**, so every request prompts on a phone whose owner
did not initiate it. The identity provides connectivity; authority is something the user granted
inside one session while looking at their phone.

**Resume skips the human, never the cryptography.** A reconnect is a full Noise KK handshake with
the pinned statics *plus* proof of the session's resumption secret — so the secret is not a bearer
token, and an attacker needs both. The fresh handshake also renews forward secrecy on every
reconnect. The resumption secret is memory-only, which converts the attack window from *any time
later* to *concurrently*. Ratchet it on each resume so a captured secret is single-use and a replay
becomes visible as a failure on the legitimate side.

**Never bind a session to an IP address** — IPv6 privacy extensions rotate, CGNAT shifts, phones
move between cellular and Wi-Fi. A session is a cryptographic object.

**A new session revokes any live grant** on that peer and says so on the phone, making an
attacker's connection loud rather than silent.

Windows: 60 minutes enrolled, 15 minutes temporary, **never pre-selected on a temporary session**.
The window ends on expiry, a new session, the session closing (app exit or workstation lock), or
explicit revocation. Deliberately *not* tied to the phone locking — people lock phones constantly,
and a window that died each time would be disabled rather than used carefully.

**The two conveniences must not compose.** A persistent pin is safe alone; an approval-skip window
is bounded alone; together they are an unattended grant that whoever sits down next inherits. On a
temporary session the skip must be chosen inside that session and never restored from a saved
preference — enforced in the protocol, not by a settings toggle.

---

## 2026-09-04 — Correcting the record: process DACLs are not a mitigation

**A previous suggestion in this conversation — harden the process with a restrictive DACL — was
wrong and is withdrawn.** An object's owner implicitly holds `WRITE_DAC`, so a same-user process
rewrites the DACL and proceeds; and `SetWindowsHookEx`-style injection never calls `OpenProcess`
at all. It would have blocked only an attacker who did not know to call `SetSecurityInfo` first.

**Worse than useless: it would have invited a false security argument.** A measure that looks like
protection but is not is more dangerous than its absence, because someone later builds on it.

**Windows provides no user-mode process isolation between programs of the same user, and none can
be constructed.** Real protection of that kind is a kernel driver using `ObRegisterCallbacks` —
what anti-malware does, unavailable to an ordinary application. The only architectural answer is a
separate SID via AppContainer, whose capability-gated file access is incompatible with an archive
manager built around opening files wherever the user points.

**Nothing in the design changes**, because `DESIGN.md` §2 already listed malware-as-the-user under
*does not defend against*. What changed is that the documents now say so explicitly, so the same
wrong turn is not taken again.

---

## 2026-09-04 — v1 targets Windows 10 and later

A substantial number of people still run Windows 10. Consequences: **TPM 2.0 cannot be assumed**
(mandatory only on Windows 11), so device binding treats it as an optional enhancement;
**DPAPI machine scope is the floor and must be implemented and tested**, not left as an unexercised
branch; and the newer WebAuthn APIs are unavailable, ruling out `prf-derived` slots there — which
are reserved and unused in v1 anyway.

---

## 2026-09-04 — Wording: "temporary device session", not "public computer"

Keeping keys off a particular machine is a legitimate preference and has nothing to do with who
else uses it. The UI language describes the *session*, not a judgement about the *computer*.

---

## Open

- **Automatic backup** — deferred; v1 ships manual export of registry + recovery slot only.
- Remaining items in the "Open" sections of `FORMAT.md` and `SYNC.md`.

---

## 2026-09-04 — File extensions: `.efd` for archives, `.eks` for the keystore

`.eks` reads as **E**nfold **K**ey **S**tore. Replaces the earlier `.enfold` / `.enfoldkey`, which
were simply long.

**Neither extension is load-bearing.** Both files start with magic bytes (`ENFOLDA\x01`,
`ENFOLDK\x01`) and identification uses those, never the filename — so a renamed file still opens
and a correctly-named impostor still fails. A collision is a shell annoyance, not a correctness
problem.

**Checked rather than assumed**, since short extensions collide: `.efd` is claimed by Parallels
Desktop and a few obscure formats; `.eks` by Empower, a niche BI product. **`.enf` was evaluated
and rejected as worse** — Finale (Enigma Notation Format), EndNote filters, EnCase Forensic and
Vicon Nexus all use it, and EndNote in particular is common on Windows.

**A considered objection, resolved:** brevity was argued to be worth less than legibility for the
keystore, since it is a single rarely-touched file whose loss is unrecoverable, and someone finding
`keystore.enfoldkey` in an old backup knows immediately what it is. The acronym answers that — the
name is not arbitrary, and it pairs with the product once seen. The asymmetry behind the objection
still holds and is worth keeping in mind: archive extensions are seen constantly and benefit from
being short, while the keystore's costs nothing either way.
